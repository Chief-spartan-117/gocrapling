package parser

import (
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// Markdown converts the element to clean Markdown suitable for LLM or RAG
// input. Scripts, styles, forms and page chrome (nav, footer, aside,
// header) are dropped; relative links resolve against the page URL.
func (s *Selector) Markdown() string { return s.markdownOpt(false, false) }

// MarkdownMain is Markdown restricted to the main content: the first
// <main> or <article> if present, else the element itself.
func (s *Selector) MarkdownMain() string { return s.markdownOpt(true, false) }

func (s *Selector) markdownOpt(mainOnly, safe bool) string {
	root := s.node
	if mainOnly {
		for _, q := range []string{"main", "article", "[role=main]"} {
			if r := s.First(q); r != nil {
				root = r.node
				break
			}
		}
	}
	w := &mdWriter{base: s.ctx.base, safe: safe}
	w.block(root)
	out := strings.TrimSpace(w.sb.String())
	// collapse 3+ newlines
	for strings.Contains(out, "\n\n\n") {
		out = strings.ReplaceAll(out, "\n\n\n", "\n\n")
	}
	return out + "\n"
}

type mdWriter struct {
	sb     strings.Builder
	base   *url.URL
	listDp int
	safe   bool
}

var mdSkip = map[string]bool{
	"script": true, "style": true, "noscript": true, "template": true, "svg": true,
	"nav": true, "footer": true, "aside": true, "header": true, "form": true,
	"iframe": true, "button": true, "select": true, "head": true,
}

func (w *mdWriter) resolve(href string) string {
	href = strings.TrimSpace(href)
	if w.base == nil || href == "" {
		return href
	}
	if u, err := url.Parse(href); err == nil {
		return w.base.ResolveReference(u).String()
	}
	return href
}

func (w *mdWriter) newline(n int) {
	cur := w.sb.String()
	have := 0
	for i := len(cur) - 1; i >= 0 && cur[i] == '\n'; i-- {
		have++
	}
	for ; have < n && len(cur) > 0; have++ {
		w.sb.WriteByte('\n')
	}
}

func (w *mdWriter) block(n *html.Node) {
	switch n.Type {
	case html.TextNode:
		w.text(n.Data)
		return
	case html.ElementNode:
	default:
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			w.block(c)
		}
		return
	}
	if mdSkip[n.Data] && n.Parent != nil && n.Parent.Type != html.DocumentNode {
		return
	}
	if w.safe && isHiddenNode(n) {
		return
	}
	switch n.Data {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		lvl, _ := strconv.Atoi(n.Data[1:])
		w.newline(2)
		w.sb.WriteString(strings.Repeat("#", lvl) + " " + w.inline(n) + "\n\n")
	case "p", "div", "section", "article", "main", "figure", "figcaption", "details", "summary", "dl", "dd", "dt":
		w.newline(2)
		hasBlock := false
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode && isBlock(c.Data) {
				hasBlock = true
				break
			}
		}
		if hasBlock {
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				w.block(c)
			}
		} else if t := strings.TrimSpace(w.inline(n)); t != "" {
			w.sb.WriteString(t)
		}
		w.newline(2)
	case "br":
		w.sb.WriteString("  \n")
	case "hr":
		w.newline(2)
		w.sb.WriteString("---\n\n")
	case "ul", "ol":
		w.newline(1)
		w.list(n)
		w.newline(2)
	case "blockquote":
		inner := &mdWriter{base: w.base, safe: w.safe}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			inner.block(c)
		}
		w.newline(2)
		for _, ln := range strings.Split(strings.TrimSpace(inner.sb.String()), "\n") {
			w.sb.WriteString("> " + ln + "\n")
		}
		w.sb.WriteString("\n")
	case "pre":
		w.newline(2)
		w.sb.WriteString("```\n" + strings.TrimRight(rawText(n), "\n") + "\n```\n\n")
	case "table":
		w.newline(2)
		w.table(n)
		w.newline(2)
	default:
		if isBlock(n.Data) {
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				w.block(c)
			}
			return
		}
		// stray inline content at block level
		if t := strings.TrimSpace(w.inline(n)); t != "" {
			w.sb.WriteString(t + " ")
		}
	}
}

func isBlock(tag string) bool {
	switch tag {
	case "p", "div", "section", "article", "main", "ul", "ol", "li", "table", "pre", "blockquote",
		"h1", "h2", "h3", "h4", "h5", "h6", "hr", "figure", "html", "body", "dl", "details":
		return true
	}
	return false
}

func (w *mdWriter) text(s string) {
	if w.safe {
		s = CleanInvisible(s)
	}
	t := strings.Join(strings.Fields(s), " ")
	if t != "" {
		w.sb.WriteString(t + " ")
	}
}

func (w *mdWriter) list(n *html.Node) {
	ordered := n.Data == "ol"
	i := 0
	indent := strings.Repeat("    ", w.listDp)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode || c.Data != "li" {
			continue
		}
		i++
		marker := "- "
		if ordered {
			marker = strconv.Itoa(i) + ". "
		}
		var head strings.Builder
		var nested []*html.Node
		for k := c.FirstChild; k != nil; k = k.NextSibling {
			if k.Type == html.ElementNode && (k.Data == "ul" || k.Data == "ol") {
				nested = append(nested, k)
				continue
			}
			head.WriteString(w.inlineNode(k))
		}
		w.sb.WriteString(indent + marker + strings.Join(strings.Fields(head.String()), " ") + "\n")
		w.listDp++
		for _, nl := range nested {
			w.list(nl)
		}
		w.listDp--
	}
}

func (w *mdWriter) table(n *html.Node) {
	var rows [][]string
	var walkRows func(*html.Node)
	walkRows = func(p *html.Node) {
		for c := p.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode {
				continue
			}
			switch c.Data {
			case "tr":
				var cells []string
				for d := c.FirstChild; d != nil; d = d.NextSibling {
					if d.Type == html.ElementNode && (d.Data == "td" || d.Data == "th") {
						t := strings.Join(strings.Fields(w.inline(d)), " ")
						cells = append(cells, strings.ReplaceAll(t, "|", `\|`))
					}
				}
				if len(cells) > 0 {
					rows = append(rows, cells)
				}
			case "thead", "tbody", "tfoot":
				walkRows(c)
			}
		}
	}
	walkRows(n)
	if len(rows) == 0 {
		return
	}
	cols := 0
	for _, r := range rows {
		if len(r) > cols {
			cols = len(r)
		}
	}
	line := func(r []string) {
		for len(r) < cols {
			r = append(r, "")
		}
		w.sb.WriteString("| " + strings.Join(r, " | ") + " |\n")
	}
	line(rows[0])
	sep := make([]string, cols)
	for i := range sep {
		sep[i] = "---"
	}
	line(sep)
	for _, r := range rows[1:] {
		line(r)
	}
}

func (w *mdWriter) inline(n *html.Node) string {
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(w.inlineNode(c))
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func (w *mdWriter) inlineNode(n *html.Node) string {
	if n.Type == html.TextNode {
		if w.safe {
			return CleanInvisible(n.Data)
		}
		return n.Data
	}
	if n.Type != html.ElementNode || mdSkip[n.Data] || (w.safe && isHiddenNode(n)) {
		return ""
	}
	switch n.Data {
	case "a":
		txt := w.inline(n)
		href := ""
		for _, a := range n.Attr {
			if a.Key == "href" {
				href = w.resolve(a.Val)
			}
		}
		if txt == "" || href == "" || strings.HasPrefix(href, "javascript:") {
			return txt
		}
		return "[" + txt + "](" + href + ")"
	case "img":
		var alt, src string
		for _, a := range n.Attr {
			switch a.Key {
			case "alt":
				alt = a.Val
			case "src":
				src = w.resolve(a.Val)
			}
		}
		if src == "" {
			return ""
		}
		return "![" + alt + "](" + src + ")"
	case "strong", "b":
		if t := w.inline(n); t != "" {
			return "**" + t + "**"
		}
	case "em", "i":
		if t := w.inline(n); t != "" {
			return "*" + t + "*"
		}
	case "code", "kbd", "samp":
		if t := rawText(n); t != "" {
			return "`" + strings.TrimSpace(t) + "`"
		}
	case "br":
		return "  \n"
	}
	return w.inline(n)
}

func rawText(n *html.Node) string {
	var b strings.Builder
	var f func(*html.Node)
	f = func(x *html.Node) {
		if x.Type == html.TextNode {
			b.WriteString(x.Data)
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(n)
	return b.String()
}
