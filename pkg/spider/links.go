package spider

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// LinkExtractor finds and filters links on a page.
type LinkExtractor struct {
	// Restrict is the CSS selector yielding hrefs. Default "a::attr(href)".
	// Use "a::attr(href), area::attr(href)"-style single selectors only;
	// call Extract once per selector for more.
	Restrict string
	// RestrictXPath scopes link discovery to the elements this XPath
	// selects (their <a href> descendants, or the elements themselves when
	// they are links). Applied in addition to Restrict.
	RestrictXPath string
	// Canonicalize normalises every URL: lower-case scheme and host,
	// default port removed, fragment dropped, query sorted. Equivalent
	// URLs then dedupe.
	Canonicalize bool
	// Allow keeps only URLs matching any pattern (all when empty).
	Allow []*regexp.Regexp
	// Deny drops URLs matching any pattern.
	Deny []*regexp.Regexp
	// AllowDomains keeps only these hosts (subdomains included).
	AllowDomains []string
	// DenyDomains drops these hosts (subdomains included).
	DenyDomains []string
	// DenyExtensions drops URLs ending in these extensions (no dot).
	// Nil uses a default list of media, archive and binary types; an empty
	// non-nil slice disables the check.
	DenyExtensions []string
}

var defaultDenyExt = []string{
	"png", "jpg", "jpeg", "gif", "webp", "svg", "ico", "bmp", "tiff",
	"mp3", "mp4", "avi", "mov", "wmv", "flv", "webm", "ogg", "wav",
	"zip", "gz", "tar", "rar", "7z", "bz2", "exe", "dmg", "apk", "iso", "msi",
	"pdf", "doc", "docx", "xls", "xlsx", "ppt", "pptx", "css", "js", "woff", "woff2", "ttf",
}

// Extract returns unique, absolute, filtered links from r in page order.
func (le LinkExtractor) Extract(r *Response) ([]string, error) {
	doc, err := r.Doc()
	if err != nil {
		return nil, err
	}
	sel := le.Restrict
	if sel == "" {
		sel = "a::attr(href)"
	}
	deny := le.DenyExtensions
	if deny == nil {
		deny = defaultDenyExt
	}
	hrefs := doc.Extract(sel)
	if le.RestrictXPath != "" {
		scopes, err := doc.XPath(le.RestrictXPath)
		if err != nil {
			return nil, err
		}
		hrefs = hrefs[:0]
		for _, sc := range scopes {
			if sc.Tag() == "a" || sc.Tag() == "area" {
				if h, ok := sc.Attr("href"); ok {
					hrefs = append(hrefs, h)
				}
				continue
			}
			hrefs = append(hrefs, sc.CSS("a").Attrs("href")...)
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, href := range hrefs {
		abs, ok := r.Resolve(href)
		if ok && le.Canonicalize {
			abs = canonicalURL(abs)
		}
		if !ok || seen[abs] || !le.accept(abs, deny) {
			continue
		}
		seen[abs] = true
		out = append(out, abs)
	}
	return out, nil
}

func (le LinkExtractor) accept(abs string, denyExt []string) bool {
	u, err := url.Parse(abs)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if len(le.AllowDomains) > 0 && !hostIn(host, le.AllowDomains) {
		return false
	}
	if hostIn(host, le.DenyDomains) {
		return false
	}
	ext := strings.TrimPrefix(strings.ToLower(path.Ext(u.Path)), ".")
	for _, d := range denyExt {
		if ext == d {
			return false
		}
	}
	for _, re := range le.Deny {
		if re.MatchString(abs) {
			return false
		}
	}
	if len(le.Allow) == 0 {
		return true
	}
	for _, re := range le.Allow {
		if re.MatchString(abs) {
			return true
		}
	}
	return false
}

func hostIn(host string, list []string) bool {
	for _, d := range list {
		d = strings.ToLower(strings.TrimPrefix(d, "."))
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// FollowLinks extracts links with le and follows each with callback.
func (e *Emitter) FollowLinks(le LinkExtractor, callback string, mods ...func(*Request)) error {
	links, err := le.Extract(e.resp)
	if err != nil {
		return err
	}
	for _, l := range links {
		e.Follow(l, callback, mods...)
	}
	return nil
}

// CrawlRule maps matching links to a callback, like Scrapy's CrawlSpider.
type CrawlRule struct {
	Extractor LinkExtractor
	// Callback handles matched pages; "" means don't parse (only follow).
	Callback string
	// Follow keeps extracting rules from matched pages. It defaults to
	// true when Callback is empty.
	Follow bool
}

// NewCrawlSpider builds a rule-driven spider. Callbacks hold your page
// handlers; every page matched by a rule is also scanned with all rules
// when the rule has Follow (or no Callback).
func NewCrawlSpider(name string, start []string, rules []CrawlRule, callbacks map[string]Callback) *Spider {
	sp := &Spider{Name: name, StartURLs: start, Callbacks: map[string]Callback{}}
	for k, v := range callbacks {
		sp.Callbacks[k] = v
	}
	// ruleIdx travels in Meta so a followed page knows which rule found it.
	apply := func(ctx context.Context, r *Response, out *Emitter, follow bool) error {
		for i, rule := range rules {
			links, err := rule.Extractor.Extract(r)
			if err != nil {
				return err
			}
			for _, l := range links {
				cb := "_crawl"
				out.Follow(l, cb, WithMeta("_rule", i))
			}
		}
		return nil
	}
	sp.Callbacks["parse"] = func(ctx context.Context, r *Response, out *Emitter) error {
		return apply(ctx, r, out, true)
	}
	sp.Callbacks["_crawl"] = func(ctx context.Context, r *Response, out *Emitter) error {
		idx := -1
		switch v := r.Request.Meta["_rule"].(type) {
		case int:
			idx = v
		case float64: // after a checkpoint round-trip
			idx = int(v)
		}
		if idx < 0 || idx >= len(rules) {
			return nil
		}
		rule := rules[idx]
		if rule.Callback != "" {
			cb := sp.Callbacks[rule.Callback]
			if cb == nil {
				return fmt.Errorf("crawl rule %d: unknown callback %q", idx, rule.Callback)
			}
			if err := cb(ctx, r, out); err != nil {
				return err
			}
		}
		if rule.Follow || rule.Callback == "" {
			return apply(ctx, r, out, true)
		}
		return nil
	}
	return sp
}

// ---- sitemaps ----

// SitemapRule routes sitemap URLs matching Pattern (all if nil) to Callback.
type SitemapRule struct {
	Pattern  *regexp.Regexp
	Callback string
}

// NewSitemapSpider crawls sitemap.xml / sitemap indexes (gzip supported).
// An entry that is a robots.txt URL is scanned for "Sitemap:" lines.
func NewSitemapSpider(name string, sitemaps []string, rules []SitemapRule, callbacks map[string]Callback) *Spider {
	sp := &Spider{Name: name, StartURLs: sitemaps, Callbacks: map[string]Callback{}}
	for k, v := range callbacks {
		sp.Callbacks[k] = v
	}
	sp.Callbacks["parse"] = func(ctx context.Context, r *Response, out *Emitter) error {
		if strings.HasSuffix(strings.ToLower(strings.SplitN(r.URL, "?", 2)[0]), "robots.txt") {
			for _, ln := range strings.Split(string(r.Body), "\n") {
				if k, v, ok := strings.Cut(ln, ":"); ok && strings.EqualFold(strings.TrimSpace(k), "sitemap") {
					out.Follow(strings.TrimSpace(v), "parse")
				}
			}
			return nil
		}
		body, err := maybeGunzip(r.Body)
		if err != nil {
			return err
		}
		subs, urls, err := parseSitemap(body)
		if err != nil {
			return fmt.Errorf("sitemap %s: %w", r.URL, err)
		}
		for _, s := range subs {
			out.Follow(s, "parse")
		}
		for _, u := range urls {
			for _, rule := range rules {
				if rule.Pattern == nil || rule.Pattern.MatchString(u) {
					out.Follow(u, rule.Callback)
					break
				}
			}
		}
		return nil
	}
	return sp
}

func maybeGunzip(b []byte) ([]byte, error) {
	if len(b) > 2 && b[0] == 0x1f && b[1] == 0x8b {
		zr, err := gzip.NewReader(bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		defer zr.Close()
		return io.ReadAll(io.LimitReader(zr, 256<<20))
	}
	return b, nil
}

func parseSitemap(b []byte) (sitemaps, urls []string, err error) {
	dec := xml.NewDecoder(bytes.NewReader(b))
	var inSitemap, inURL bool
	for {
		tok, terr := dec.Token()
		if terr == io.EOF {
			return sitemaps, urls, nil
		}
		if terr != nil {
			return sitemaps, urls, terr
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "sitemap":
				inSitemap = true
			case "url":
				inURL = true
			case "loc":
				var loc string
				if err := dec.DecodeElement(&loc, &t); err != nil {
					return sitemaps, urls, err
				}
				loc = strings.TrimSpace(loc)
				switch {
				case inSitemap:
					sitemaps = append(sitemaps, loc)
				case inURL:
					urls = append(urls, loc)
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "sitemap":
				inSitemap = false
			case "url":
				inURL = false
			}
		}
	}
}
