// Package spider is a Scrapy-style crawl framework on top of fetcher and
// stealth: priority scheduling, de-duplication, per-domain politeness,
// robots.txt, retries, browser escalation and pause/resume checkpoints.
package spider

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"net"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/sagarshrestha/gocrapling/fetcher"
	"github.com/sagarshrestha/gocrapling/parser"
)

// Callback handles a response. Emit items and follow-up requests via out.
type Callback func(ctx context.Context, r *Response, out *Emitter) error

// Spider describes a crawl.
type Spider struct {
	Name string
	// StartURLs are crawled first, handled by the "parse" callback.
	StartURLs []string
	// StartRequests, if set, replaces StartURLs.
	StartRequests func() []*Request
	// Callbacks maps names to handlers. "parse" is the default. Callbacks
	// are referenced by name so queued requests survive checkpointing.
	Callbacks map[string]Callback
	// AllowedDomains restricts followed links (host or registrable suffix).
	// Empty allows everything.
	AllowedDomains []string
	// IsBlocked decides whether a response is an anti-bot wall, replacing
	// nothing: it runs in addition to the fetcher's own detection. A
	// blocked response is escalated to the browser when configured
	// (Config.EscalateOnBlock), otherwise retried via the middleware
	// retry budget (Config.MaxHookRetries).
	IsBlocked func(*fetcher.Response) bool
	// OnError is called when a request fails permanently. Optional.
	OnError func(req *Request, err error)
}

// Request is a unit of crawl work. Everything except runtime state is
// JSON-serialisable for checkpoints.
type Request struct {
	URL      string            `json:"url"`
	Method   string            `json:"method,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	Body     []byte            `json:"body,omitempty"`
	Callback string            `json:"callback,omitempty"`
	Meta     map[string]any    `json:"meta,omitempty"`
	Priority int               `json:"priority,omitempty"`
	Depth    int               `json:"depth,omitempty"`
	// Browser forces the stealth browser downloader for this request.
	Browser bool `json:"browser,omitempty"`
	// Session names an entry of Config.Sessions to download with.
	Session string `json:"session,omitempty"`
	// Proxy overrides proxy rotation for this request (HTTP downloads).
	Proxy string `json:"proxy,omitempty"`
	// DontFilter bypasses duplicate detection.
	DontFilter bool `json:"dont_filter,omitempty"`
}

// Key is the de-duplication fingerprint.
func (r *Request) Key() string {
	h := sha1.New()
	h.Write([]byte(strings.ToUpper(r.Method) + "\n" + canonicalURL(r.URL) + "\n"))
	h.Write(r.Body)
	return hex.EncodeToString(h.Sum(nil))
}

func canonicalURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.Fragment = ""
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	if (u.Scheme == "http" && u.Port() == "80") || (u.Scheme == "https" && u.Port() == "443") {
		u.Host = u.Hostname()
	}
	q := u.Query()
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		vs := q[k]
		sort.Strings(vs)
		for _, v := range vs {
			if b.Len() > 0 {
				b.WriteByte('&')
			}
			b.WriteString(url.QueryEscape(k) + "=" + url.QueryEscape(v))
		}
	}
	u.RawQuery = b.String()
	return u.String()
}

// Response is what callbacks receive.
type Response struct {
	*fetcher.Response
	Request *Request

	once sync.Once
	doc  *parser.Selector
	err  error
}

// Doc parses the body once and caches it.
func (r *Response) Doc(opts ...parser.Option) (*parser.Selector, error) {
	r.once.Do(func() { r.doc, r.err = r.Parse(opts...) })
	return r.doc, r.err
}

// Resolve turns a possibly relative href into an absolute URL.
func (r *Response) Resolve(href string) (string, bool) {
	base, err := url.Parse(r.URL)
	if err != nil {
		return "", false
	}
	ref, err := url.Parse(strings.TrimSpace(href))
	if err != nil {
		return "", false
	}
	abs := base.ResolveReference(ref)
	if abs.Scheme != "http" && abs.Scheme != "https" {
		return "", false
	}
	abs.Fragment = ""
	return abs.String(), true
}

// Emitter collects what a callback produces. It is only valid during the
// callback.
type Emitter struct {
	resp  *Response
	items []any
	reqs  []*Request
}

// Item emits a scraped item to the pipelines.
func (e *Emitter) Item(v any) { e.items = append(e.items, v) }

// Request queues an explicit request.
func (e *Emitter) Request(r *Request) { e.reqs = append(e.reqs, r) }

// Follow queues a link found on the current page, resolved against the
// page URL. callback "" means "parse". Invalid or non-http links are
// ignored. Mods tweak the request (priority, meta, browser...).
func (e *Emitter) Follow(href, callback string, mods ...func(*Request)) {
	abs, ok := e.resp.Resolve(href)
	if !ok {
		return
	}
	r := &Request{URL: abs, Callback: callback, Depth: e.resp.Request.Depth + 1}
	for _, m := range mods {
		m(r)
	}
	e.reqs = append(e.reqs, r)
}

// WithMeta, WithPriority and WithBrowser are Follow modifiers.
func WithMeta(k string, v any) func(*Request) {
	return func(r *Request) {
		if r.Meta == nil {
			r.Meta = map[string]any{}
		}
		r.Meta[k] = v
	}
}
func WithPriority(p int) func(*Request) { return func(r *Request) { r.Priority = p } }
func WithBrowser() func(*Request)       { return func(r *Request) { r.Browser = true } }

// WithSession routes a request through the named Config.Sessions entry.
func WithSession(name string) func(*Request) { return func(r *Request) { r.Session = name } }

// WithProxy pins a proxy URL for one request.
func WithProxy(proxy string) func(*Request) { return func(r *Request) { r.Proxy = proxy } }

func (s *Spider) domainAllowed(rawURL string) bool {
	if len(s.AllowedDomains) == 0 {
		return true
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, d := range s.AllowedDomains {
		d = strings.ToLower(strings.TrimPrefix(d, "."))
		if h, _, err := net.SplitHostPort(d); err == nil {
			d = h // tolerate "host:port" entries
		}
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}
