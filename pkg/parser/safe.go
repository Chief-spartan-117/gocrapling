package parser

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

// MarkdownOptions tunes Markdown conversion.
type MarkdownOptions struct {
	// MainOnly restricts output to <main>/<article>.
	MainOnly bool
	// Safe strips content a sighted visitor would not see (hidden elements,
	// off-screen text, invisible and direction-control Unicode). Pages can
	// plant instructions there to hijack an AI agent that reads the text.
	Safe bool
}

// MarkdownWith converts with the given options.
func (s *Selector) MarkdownWith(o MarkdownOptions) string {
	return s.markdownOpt(o.MainOnly, o.Safe)
}

// SafeText is Text with hidden content and invisible characters removed.
func (s *Selector) SafeText() string {
	var b strings.Builder
	var rec func(*html.Node)
	rec = func(n *html.Node) {
		if n.Type == html.ElementNode && (isHiddenNode(n) || n.Data == "script" || n.Data == "style" || n.Data == "noscript" || n.Data == "template") {
			return
		}
		if n.Type == html.TextNode {
			b.WriteString(CleanInvisible(n.Data))
			b.WriteByte(' ')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			rec(c)
		}
	}
	rec(s.node)
	return strings.Join(strings.Fields(b.String()), " ")
}

var (
	hiddenStyleRe = regexp.MustCompile(`(?i)(display\s*:\s*none|visibility\s*:\s*(hidden|collapse)|opacity\s*:\s*0(\.0+)?\s*(;|$|!)|font-size\s*:\s*0(px|pt|em|rem|%)?\s*(;|$|!)|(?:^|;)\s*(?:left|top|right|bottom|text-indent)\s*:\s*-\d{4,}(px|em|rem|pt|%)?|clip-path\s*:\s*inset\(\s*(50|100)%|(?:^|;)\s*(?:width|height|max-height|max-width)\s*:\s*0(px)?\s*(;|$|!))`)
)

// isHiddenNode reports whether an element is invisible to a sighted user.
func isHiddenNode(n *html.Node) bool {
	if n.Type != html.ElementNode {
		return false
	}
	for _, a := range n.Attr {
		switch a.Key {
		case "hidden":
			return true
		case "aria-hidden":
			if strings.EqualFold(a.Val, "true") {
				return true
			}
		case "type":
			if n.Data == "input" && strings.EqualFold(a.Val, "hidden") {
				return true
			}
		case "style":
			if hiddenStyleRe.MatchString(a.Val) {
				return true
			}
		}
	}
	return false
}

// CleanInvisible removes zero-width, bidirectional-control and Unicode tag
// characters (used for "ASCII smuggling") and other format/control runes
// that render as nothing, keeping ordinary whitespace.
func CleanInvisible(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t' || r == '\r':
			return r
		case r >= 0xE0000 && r <= 0xE007F: // tag characters
			return -1
		case unicode.Is(unicode.Cf, r), unicode.Is(unicode.Cc, r):
			return -1
		}
		return r
	}, s)
}
