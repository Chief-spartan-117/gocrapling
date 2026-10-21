// Package parser is an HTML parsing layer with CSS, XPath, text/regex search
// and adaptive element relocation (elements survive site redesigns).
package parser

import (
	"bytes"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/andybalholm/cascadia"
	"github.com/antchfx/htmlquery"
	"golang.org/x/net/html"
)

type context struct {
	root      *html.Node
	storage   Storage
	ident     string // storage namespace, usually the page domain
	base      *url.URL
	threshold float64
}

// Selector wraps one HTML element (or the document root).
type Selector struct {
	node *html.Node
	ctx  *context
}

// Selectors is an ordered list of matches.
type Selectors []*Selector

// Option configures Parse.
type Option func(*context)

// WithStorage enables adaptive saving/relocation backed by st.
func WithStorage(st Storage) Option { return func(c *context) { c.storage = st } }

// WithURL sets the page URL; its host namespaces stored signatures.
func WithURL(raw string) Option {
	return func(c *context) {
		if u, err := url.Parse(raw); err == nil && u.Host != "" {
			c.ident = u.Host
			c.base = u
		}
	}
}

// WithThreshold sets the minimum similarity (0-1) for adaptive matches.
func WithThreshold(t float64) Option { return func(c *context) { c.threshold = t } }

// Parse parses an HTML document and returns its root selector.
func Parse(body []byte, opts ...Option) (*Selector, error) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	c := &context{root: doc, storage: NewMemoryStorage(), ident: "default", threshold: 0.4}
	for _, o := range opts {
		o(c)
	}
	return &Selector{node: doc, ctx: c}, nil
}

func (s *Selector) wrap(n *html.Node) *Selector { return &Selector{node: n, ctx: s.ctx} }

func (s *Selector) wrapAll(ns []*html.Node) Selectors {
	out := make(Selectors, 0, len(ns))
	for _, n := range ns {
		out = append(out, s.wrap(n))
	}
	return out
}

// ---- querying ----

// Select runs a CSS selector and returns an error for invalid syntax.
func (s *Selector) Select(q string) (Selectors, error) {
	sel, err := cascadia.Compile(q)
	if err != nil {
		return nil, fmt.Errorf("parser: bad css selector %q: %w", q, err)
	}
	return s.wrapAll(sel.MatchAll(s.node)), nil
}

// CSS is Select that returns an empty list on invalid syntax.
func (s *Selector) CSS(q string) Selectors {
	r, _ := s.Select(q)
	return r
}

// XPath runs an XPath expression returning element nodes.
func (s *Selector) XPath(q string) (Selectors, error) {
	ns, err := htmlquery.QueryAll(s.node, q)
	if err != nil {
		return nil, fmt.Errorf("parser: bad xpath %q: %w", q, err)
	}
	return s.wrapAll(ns), nil
}

// First returns the first CSS match or nil.
func (s *Selector) First(q string) *Selector { return s.CSS(q).First() }

var pseudoRe = regexp.MustCompile(`::(text|attr\(([^)]+)\))\s*$`)

// Extract supports Scrapy/Scrapling-style pseudo elements:
// "a.link::attr(href)", "h1::text". Without a pseudo it returns text.
func (s *Selector) Extract(q string) []string {
	m := pseudoRe.FindStringSubmatchIndex(q)
	attr := ""
	if m != nil {
		sub := pseudoRe.FindStringSubmatch(q)
		attr = strings.TrimSpace(sub[2])
		q = strings.TrimSpace(q[:m[0]])
	}
	var out []string
	for _, e := range s.CSS(q) {
		if attr != "" {
			if v, ok := e.Attr(attr); ok {
				out = append(out, v)
			}
		} else {
			out = append(out, e.Text())
		}
	}
	return out
}

// ---- Selectors helpers ----

func (l Selectors) First() *Selector {
	if len(l) == 0 {
		return nil
	}
	return l[0]
}

func (l Selectors) Texts() []string {
	out := make([]string, len(l))
	for i, e := range l {
		out[i] = e.Text()
	}
	return out
}

func (l Selectors) Attrs(name string) []string {
	var out []string
	for _, e := range l {
		if v, ok := e.Attr(name); ok {
			out = append(out, v)
		}
	}
	return out
}

// ---- element accessors ----

func (s *Selector) Tag() string {
	if s.node.Type == html.ElementNode {
		return s.node.Data
	}
	return ""
}

// Attr returns an attribute value.
func (s *Selector) Attr(name string) (string, bool) {
	for _, a := range s.node.Attr {
		if a.Key == name {
			return a.Val, true
		}
	}
	return "", false
}

func (s *Selector) Attrs() map[string]string {
	m := make(map[string]string, len(s.node.Attr))
	for _, a := range s.node.Attr {
		m[a.Key] = a.Val
	}
	return m
}

func (s *Selector) HasClass(c string) bool {
	v, _ := s.Attr("class")
	for _, f := range strings.Fields(v) {
		if f == c {
			return true
		}
	}
	return false
}

// Text returns whitespace-normalised text of the element and descendants,
// skipping script/style.
func (s *Selector) Text() string {
	var b strings.Builder
	collectText(s.node, &b)
	return strings.Join(strings.Fields(b.String()), " ")
}

func collectText(n *html.Node, b *strings.Builder) {
	if n.Type == html.TextNode {
		b.WriteString(n.Data)
		b.WriteByte(' ')
		return
	}
	if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style" || n.Data == "noscript") {
		return
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		collectText(c, b)
	}
}

// HTML returns the outer HTML.
func (s *Selector) HTML() string {
	var b bytes.Buffer
	_ = html.Render(&b, s.node)
	return b.String()
}

func (s *Selector) InnerHTML() string {
	var b bytes.Buffer
	for c := s.node.FirstChild; c != nil; c = c.NextSibling {
		_ = html.Render(&b, c)
	}
	return b.String()
}

// ---- navigation ----

func (s *Selector) Parent() *Selector {
	if s.node.Parent == nil {
		return nil
	}
	return s.wrap(s.node.Parent)
}

func (s *Selector) Children() Selectors {
	var out Selectors
	for c := s.node.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			out = append(out, s.wrap(c))
		}
	}
	return out
}

func (s *Selector) Next() *Selector {
	for n := s.node.NextSibling; n != nil; n = n.NextSibling {
		if n.Type == html.ElementNode {
			return s.wrap(n)
		}
	}
	return nil
}

func (s *Selector) Prev() *Selector {
	for n := s.node.PrevSibling; n != nil; n = n.PrevSibling {
		if n.Type == html.ElementNode {
			return s.wrap(n)
		}
	}
	return nil
}

// ---- text / regex search ----

// FindByText returns elements whose own text matches. partial=false demands
// an exact (normalised) match. tag restricts element name when non-empty.
func (s *Selector) FindByText(text string, partial bool, tag string) Selectors {
	want := strings.Join(strings.Fields(text), " ")
	return s.filter(func(e *Selector) bool {
		if tag != "" && e.Tag() != tag {
			return false
		}
		own := e.ownText()
		if partial {
			return strings.Contains(own, want)
		}
		return own == want
	})
}

// FindByRegex returns elements whose own text matches re.
func (s *Selector) FindByRegex(re *regexp.Regexp) Selectors {
	return s.filter(func(e *Selector) bool { return re.MatchString(e.ownText()) })
}

// Re returns all regex matches (group 1 if present) in the element text.
func (s *Selector) Re(re *regexp.Regexp) []string {
	var out []string
	for _, m := range re.FindAllStringSubmatch(s.Text(), -1) {
		if len(m) > 1 {
			out = append(out, m[1])
		} else {
			out = append(out, m[0])
		}
	}
	return out
}

func (s *Selector) ownText() string {
	var b strings.Builder
	for c := s.node.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
			b.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func (s *Selector) filter(pred func(*Selector) bool) Selectors {
	var out Selectors
	walk(s.node, func(n *html.Node) {
		if n.Type != html.ElementNode {
			return
		}
		if e := s.wrap(n); pred(e) {
			out = append(out, e)
		}
	})
	return out
}

func walk(n *html.Node, f func(*html.Node)) {
	f(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, f)
	}
}

// ---- adaptive ----

// QueryOptions controls adaptive behaviour of CSSWith/XPathWith.
type QueryOptions struct {
	// AutoSave stores the signature of every match under Identifier.
	AutoSave bool
	// Adaptive relocates elements from stored signatures when the selector
	// matches nothing (the site layout changed).
	Adaptive bool
	// Identifier names the stored element set; defaults to the selector.
	Identifier string
}

// CSSWith is CSS with adaptive save/relocate support.
func (s *Selector) CSSWith(q string, o QueryOptions) (Selectors, error) {
	res, err := s.Select(q)
	if err != nil {
		return nil, err
	}
	return s.adaptive(q, res, o)
}

// XPathWith is XPath with adaptive save/relocate support.
func (s *Selector) XPathWith(q string, o QueryOptions) (Selectors, error) {
	res, err := s.XPath(q)
	if err != nil {
		return nil, err
	}
	return s.adaptive(q, res, o)
}

func (s *Selector) adaptive(q string, res Selectors, o QueryOptions) (Selectors, error) {
	id := o.Identifier
	if id == "" {
		id = q
	}
	if len(res) > 0 {
		if o.AutoSave {
			sigs := make([]Signature, len(res))
			for i, e := range res {
				sigs[i] = signatureOf(e.node)
			}
			if err := s.ctx.storage.Save(s.ctx.ident, id, sigs); err != nil {
				return res, fmt.Errorf("parser: save signature: %w", err)
			}
		}
		return res, nil
	}
	if !o.Adaptive {
		return res, nil
	}
	sigs, err := s.ctx.storage.Load(s.ctx.ident, id)
	if err != nil || len(sigs) == 0 {
		return nil, err
	}
	var out Selectors
	seen := map[*html.Node]bool{}
	for _, sig := range sigs {
		if n, score := bestMatch(s.ctx.root, sig, nil); n != nil && score >= s.ctx.threshold && !seen[n] {
			seen[n] = true
			out = append(out, s.wrap(n))
		}
	}
	return out, nil
}

// FindSimilar returns elements structurally similar to s (same role in a
// list, e.g. other product cards), best first.
func (s *Selector) FindSimilar(threshold float64) Selectors {
	sig := signatureOf(s.node)
	type scored struct {
		n *html.Node
		v float64
	}
	var all []scored
	walk(s.ctx.root, func(n *html.Node) {
		if n.Type != html.ElementNode || n == s.node {
			return
		}
		if v := score(sig, signatureOf(n), 0); v >= threshold {
			all = append(all, scored{n, v})
		}
	})
	sort.SliceStable(all, func(i, j int) bool { return all[i].v > all[j].v })
	out := make(Selectors, len(all))
	for i, a := range all {
		out[i] = s.wrap(a.n)
	}
	return out
}

// TextOptions controls AllText.
type TextOptions struct {
	// Separator joins text nodes. Default " ".
	Separator string
	// Strip trims each text node and drops empty ones.
	Strip bool
	// IgnoreTags skips these elements and their children. script, style
	// and noscript are always skipped.
	IgnoreTags []string
}

// AllText returns descendant text with control over separators, trimming
// and ignored tags.
func (s *Selector) AllText(o TextOptions) string {
	sep := o.Separator
	if sep == "" {
		sep = " "
	}
	skip := map[string]bool{"script": true, "style": true, "noscript": true}
	for _, t := range o.IgnoreTags {
		skip[strings.ToLower(t)] = true
	}
	var parts []string
	var rec func(*html.Node)
	rec = func(n *html.Node) {
		if n.Type == html.ElementNode && skip[n.Data] {
			return
		}
		if n.Type == html.TextNode {
			t := n.Data
			if o.Strip {
				t = strings.TrimSpace(t)
				if t == "" {
					return
				}
			}
			parts = append(parts, t)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			rec(c)
		}
	}
	rec(s.node)
	return strings.Join(parts, sep)
}

// URLJoin resolves href against the page URL (set with WithURL). Without a
// page URL it returns href unchanged.
func (s *Selector) URLJoin(href string) string {
	if s.ctx.base == nil {
		return href
	}
	ref, err := url.Parse(strings.TrimSpace(href))
	if err != nil {
		return href
	}
	return s.ctx.base.ResolveReference(ref).String()
}

// Below returns every element that comes after this one's subtree in
// document order (what a reader would meet scrolling down).
func (s *Selector) Below() Selectors {
	var out Selectors
	passed := false
	var rec func(*html.Node)
	rec = func(n *html.Node) {
		if n == s.node {
			passed = true
			return // skip own subtree
		}
		if passed && n.Type == html.ElementNode {
			out = append(out, s.wrap(n))
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			rec(c)
		}
	}
	rec(s.ctx.root)
	return out
}
