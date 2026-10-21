package spider

import (
	"bufio"
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"sort"
	"sync"
)

// XMLPipeline writes items as an XML document:
//
//	<items><item><field>value</field>...</item></items>
//
// Items may be maps or structs (via JSON tags). Slice values repeat the
// element. The file is truncated on open and closed properly by Close, so a
// crawl that is interrupted without Close leaves an unterminated root.
type XMLPipeline struct {
	mu        sync.Mutex
	f         *os.File
	w         *bufio.Writer
	root, row string
}

// NewXMLPipeline creates path. root and item default to "items" and "item".
func NewXMLPipeline(path, root, item string) (*XMLPipeline, error) {
	if root == "" {
		root = "items"
	}
	if item == "" {
		item = "item"
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	p := &XMLPipeline{f: f, w: bufio.NewWriter(f), root: root, row: item}
	if _, err := fmt.Fprintf(p.w, "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<%s>\n", root); err != nil {
		f.Close()
		return nil, err
	}
	return p, nil
}

func (p *XMLPipeline) Process(_ context.Context, item any) error {
	m, err := toMap(item)
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	p.mu.Lock()
	defer p.mu.Unlock()
	fmt.Fprintf(p.w, "  <%s>\n", p.row)
	for _, k := range keys {
		name := xmlName(k)
		vals, _ := m[k].([]any)
		if vals == nil {
			if ss, ok := m[k].([]string); ok {
				for _, s := range ss {
					vals = append(vals, s)
				}
			} else {
				vals = []any{m[k]}
			}
		}
		for _, v := range vals {
			fmt.Fprintf(p.w, "    <%s>", name)
			if err := xml.EscapeText(p.w, []byte(cell(v))); err != nil {
				return err
			}
			fmt.Fprintf(p.w, "</%s>\n", name)
		}
	}
	fmt.Fprintf(p.w, "  </%s>\n", p.row)
	return p.w.Flush()
}

func (p *XMLPipeline) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	fmt.Fprintf(p.w, "</%s>\n", p.root)
	if err := p.w.Flush(); err != nil {
		p.f.Close()
		return err
	}
	return p.f.Close()
}

// xmlName makes k a valid XML element name.
func xmlName(k string) string {
	out := []rune{}
	for i, r := range k {
		ok := r == '_' || r == '-' || r == '.' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (i > 0 && r >= '0' && r <= '9')
		if ok {
			out = append(out, r)
		} else {
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		return "field"
	}
	return string(out)
}
