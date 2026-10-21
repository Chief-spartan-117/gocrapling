package spider

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"sync"
)

// CSVPipeline writes items as CSV rows. Items may be maps or structs
// (structs go through their JSON tags). Nested values become JSON text.
type CSVPipeline struct {
	mu      sync.Mutex
	f       *os.File
	w       *csv.Writer
	columns []string
	header  bool // header already on disk
}

// NewCSVPipeline appends to path. With no columns, they are taken from the
// first item (sorted). If the file already has content, no header is
// written again and columns must be passed to match it.
func NewCSVPipeline(path string, columns ...string) (*CSVPipeline, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	p := &CSVPipeline{f: f, w: csv.NewWriter(f), columns: columns, header: st.Size() > 0}
	if p.header && len(columns) == 0 {
		f.Close()
		return nil, fmt.Errorf("spider: %s already has data; pass columns to append", path)
	}
	return p, nil
}

func (p *CSVPipeline) Process(_ context.Context, item any) error {
	row, err := toMap(item)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.columns == nil {
		for k := range row {
			p.columns = append(p.columns, k)
		}
		sort.Strings(p.columns)
	}
	if !p.header {
		if err := p.w.Write(p.columns); err != nil {
			return err
		}
		p.header = true
	}
	rec := make([]string, len(p.columns))
	for i, c := range p.columns {
		rec[i] = cell(row[c])
	}
	if err := p.w.Write(rec); err != nil {
		return err
	}
	p.w.Flush()
	return p.w.Error()
}

func (p *CSVPipeline) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.w.Flush()
	if err := p.w.Error(); err != nil {
		p.f.Close()
		return err
	}
	return p.f.Close()
}

func toMap(item any) (map[string]any, error) {
	if m, ok := item.(map[string]any); ok {
		return m, nil
	}
	b, err := json.Marshal(item)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("spider: csv item must be an object: %w", err)
	}
	return m, nil
}

func cell(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return neutralizeFormula(x)
	case float64, bool, int, int64:
		return fmt.Sprint(x)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// neutralizeFormula defuses CSV injection. Scraped text is untrusted: a page
// can set a price to =HYPERLINK(...) or =cmd|' /C calc'!A0, and Excel or
// Sheets will run it when someone opens the export. A leading apostrophe
// makes spreadsheets treat the cell as text. Plain numbers like -5 or +1.5
// are left alone.
func neutralizeFormula(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		if _, err := strconv.ParseFloat(s, 64); err == nil {
			return s
		}
		return "'" + s
	}
	return s
}
