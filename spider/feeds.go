package spider

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// XMLItemHandler receives one feed item. Keys are child element names with
// their text; attributes appear as "element@attr" (Atom: "link@href"); an
// element repeated within an item keeps the first value.
type XMLItemHandler func(ctx context.Context, r *Response, item map[string]string, out *Emitter) error

// NewXMLFeedSpider walks XML feeds (RSS: itemTag "item", Atom: "entry")
// and calls handle for every item.
func NewXMLFeedSpider(name string, urls []string, itemTag string, handle XMLItemHandler) *Spider {
	return &Spider{Name: name, StartURLs: urls, Callbacks: map[string]Callback{
		"parse": func(ctx context.Context, r *Response, out *Emitter) error {
			body, err := maybeGunzip(r.Body)
			if err != nil {
				return err
			}
			items, err := parseXMLItems(body, itemTag)
			if err != nil {
				return fmt.Errorf("feed %s: %w", r.URL, err)
			}
			for _, it := range items {
				if err := handle(ctx, r, it, out); err != nil {
					return err
				}
			}
			return nil
		},
	}}
}

func parseXMLItems(b []byte, itemTag string) ([]map[string]string, error) {
	dec := xml.NewDecoder(bytes.NewReader(b))
	dec.Strict = false
	var items []map[string]string
	var cur map[string]string
	depth := 0 // depth inside the current item
	var field string
	var text strings.Builder
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return items, nil
		}
		if err != nil {
			return items, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch {
			case cur == nil && t.Name.Local == itemTag:
				cur, depth = map[string]string{}, 0
			case cur != nil:
				depth++
				if depth == 1 {
					field = t.Name.Local
					text.Reset()
					for _, a := range t.Attr {
						if _, ok := cur[field+"@"+a.Name.Local]; !ok {
							cur[field+"@"+a.Name.Local] = a.Value
						}
					}
				}
			}
		case xml.CharData:
			if cur != nil && depth >= 1 {
				text.Write(t)
			}
		case xml.EndElement:
			if cur == nil {
				continue
			}
			if depth == 0 && t.Name.Local == itemTag {
				items = append(items, cur)
				cur = nil
				continue
			}
			if depth == 1 {
				if v := strings.TrimSpace(text.String()); v != "" {
					if _, ok := cur[field]; !ok {
						cur[field] = v
					}
				}
			}
			depth--
		}
	}
}

// CSVRowHandler receives one CSV row keyed by header name.
type CSVRowHandler func(ctx context.Context, r *Response, row map[string]string, out *Emitter) error

// CSVFeedOptions configures NewCSVFeedSpider.
type CSVFeedOptions struct {
	// Delimiter defaults to ','.
	Delimiter rune
	// Headers names the columns. When empty the first row is the header.
	Headers []string
}

// NewCSVFeedSpider reads CSV feeds and calls handle for every data row.
func NewCSVFeedSpider(name string, urls []string, opt CSVFeedOptions, handle CSVRowHandler) *Spider {
	return &Spider{Name: name, StartURLs: urls, Callbacks: map[string]Callback{
		"parse": func(ctx context.Context, r *Response, out *Emitter) error {
			cr := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(r.Body, []byte("\xef\xbb\xbf"))))
			if opt.Delimiter != 0 {
				cr.Comma = opt.Delimiter
			}
			cr.FieldsPerRecord = -1
			cr.LazyQuotes = true
			headers := opt.Headers
			for {
				rec, err := cr.Read()
				if err == io.EOF {
					return nil
				}
				if err != nil {
					return fmt.Errorf("csv %s: %w", r.URL, err)
				}
				if headers == nil {
					headers = rec
					continue
				}
				row := make(map[string]string, len(headers))
				for i, h := range headers {
					if i < len(rec) {
						row[h] = rec[i]
					}
				}
				if err := handle(ctx, r, row, out); err != nil {
					return err
				}
			}
		},
	}}
}
