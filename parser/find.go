package parser

import (
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// FindOptions filters descendants. All set fields must match.
type FindOptions struct {
	// Tag restricts the element name ("" = any).
	Tag string
	// Attrs requires exact attribute values.
	Attrs map[string]string
	// HasAttrs requires the attributes to exist, with any value.
	HasAttrs []string
	// AttrRegex requires attribute values to match.
	AttrRegex map[string]*regexp.Regexp
	// Class requires a class token.
	Class string
	// Text requires the element's own text to contain it.
	Text string
	// Filter is a custom predicate applied last.
	Filter func(*Selector) bool
	// Limit stops after this many matches (0 = all).
	Limit int
}

// FindAll returns descendants matching every condition in o, in document
// order. The element itself is not included.
func (s *Selector) FindAll(o FindOptions) Selectors {
	var out Selectors
	walk(s.node, func(n *html.Node) {
		if n == s.node || n.Type != html.ElementNode || (o.Limit > 0 && len(out) >= o.Limit) {
			return
		}
		if o.Tag != "" && n.Data != o.Tag {
			return
		}
		e := s.wrap(n)
		for k, v := range o.Attrs {
			if got, ok := e.Attr(k); !ok || got != v {
				return
			}
		}
		for _, k := range o.HasAttrs {
			if _, ok := e.Attr(k); !ok {
				return
			}
		}
		for k, re := range o.AttrRegex {
			if got, ok := e.Attr(k); !ok || !re.MatchString(got) {
				return
			}
		}
		if o.Class != "" && !e.HasClass(o.Class) {
			return
		}
		if o.Text != "" && !strings.Contains(e.ownText(), o.Text) {
			return
		}
		if o.Filter != nil && !o.Filter(e) {
			return
		}
		out = append(out, e)
	})
	return out
}

// CSSPath returns a CSS selector that matches exactly this element. It
// anchors on the nearest unique id, else walks from the root using
// :nth-of-type where siblings share a tag.
func (s *Selector) CSSPath() string {
	var parts []string
	for n := s.node; n != nil && n.Type == html.ElementNode; n = n.Parent {
		if id := attrOf(n, "id"); id != "" && s.idIsUnique(id) && cssIdent(id) {
			parts = append([]string{"#" + id}, parts...)
			break
		}
		seg := n.Data
		if idx, total := typeIndex(n); total > 1 {
			seg += fmt.Sprintf(":nth-of-type(%d)", idx)
		}
		parts = append([]string{seg}, parts...)
	}
	return strings.Join(parts, " > ")
}

// XPathExpr returns an absolute XPath for this element, anchored on the
// nearest unique id when there is one.
func (s *Selector) XPathExpr() string {
	var parts []string
	anchored := false
	for n := s.node; n != nil && n.Type == html.ElementNode; n = n.Parent {
		if id := attrOf(n, "id"); id != "" && s.idIsUnique(id) && !strings.Contains(id, `"`) {
			parts = append([]string{fmt.Sprintf(`//*[@id="%s"]`, id)}, parts...)
			anchored = true
			break
		}
		seg := n.Data
		if idx, total := typeIndex(n); total > 1 {
			seg += fmt.Sprintf("[%d]", idx)
		}
		parts = append([]string{seg}, parts...)
	}
	if anchored {
		return strings.Join(parts, "/")
	}
	return "/" + strings.Join(parts, "/")
}

func attrOf(n *html.Node, k string) string {
	for _, a := range n.Attr {
		if a.Key == k {
			return a.Val
		}
	}
	return ""
}

func (s *Selector) idIsUnique(id string) bool {
	count := 0
	walk(s.ctx.root, func(n *html.Node) {
		if n.Type == html.ElementNode && attrOf(n, "id") == id {
			count++
		}
	})
	return count == 1
}

// typeIndex returns n's 1-based position among same-tag siblings and the
// number of such siblings.
func typeIndex(n *html.Node) (idx, total int) {
	if n.Parent == nil {
		return 1, 1
	}
	for c := n.Parent.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == n.Data {
			total++
			if c == n {
				idx = total
			}
		}
	}
	return idx, total
}

var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

func cssIdent(id string) bool { return identRe.MatchString(id) }
