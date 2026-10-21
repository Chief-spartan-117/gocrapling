package spider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// ---- ShopifySpider ----

type shopifyProducts struct {
	Products []struct {
		ID          int64  `json:"id"`
		Title       string `json:"title"`
		Handle      string `json:"handle"`
		Vendor      string `json:"vendor"`
		ProductType string `json:"product_type"`
		BodyHTML    string `json:"body_html"`
		PublishedAt string `json:"published_at"`
		// tags is a comma string in some stores and an array in others
		Tags     json.RawMessage `json:"tags"`
		Variants []struct {
			ID               int64  `json:"id"`
			Title            string `json:"title"`
			SKU              string `json:"sku"`
			Price            string `json:"price"`
			CompareAtPrice   string `json:"compare_at_price"`
			Available        bool   `json:"available"`
			Option1          string `json:"option1"`
			Option2          string `json:"option2"`
			Option3          string `json:"option3"`
			Grams            int    `json:"grams"`
			RequiresShipping bool   `json:"requires_shipping"`
			Barcode          string `json:"barcode"`
		} `json:"variants"`
		Images []struct {
			Src string `json:"src"`
		} `json:"images"`
	} `json:"products"`
}

func shopifyTags(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var arr []string
	if json.Unmarshal(raw, &arr) == nil {
		return arr
	}
	var s string
	if json.Unmarshal(raw, &s) == nil && s != "" {
		parts := strings.Split(s, ",")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		return parts
	}
	return nil
}

// NewShopifySpider scrapes a Shopify store through its public
// /products.json endpoint and emits one item per product variant, so each
// size or colour is its own row. site is the store root, e.g.
// "https://shop.example.com". It pages until an empty page is returned.
func NewShopifySpider(name, site string) *Spider {
	site = strings.TrimRight(site, "/")
	const perPage = 250
	pageURL := func(n int) string { return fmt.Sprintf("%s/products.json?limit=%d&page=%d", site, perPage, n) }
	return &Spider{
		Name:           name,
		StartRequests:  func() []*Request { return []*Request{{URL: pageURL(1), Meta: map[string]any{"page": 1}}} },
		AllowedDomains: []string{hostOf(site)},
		Callbacks: map[string]Callback{
			"parse": func(ctx context.Context, r *Response, out *Emitter) error {
				var p shopifyProducts
				if err := r.JSON(&p); err != nil {
					return fmt.Errorf("not a Shopify products.json: %w", err)
				}
				for _, pr := range p.Products {
					image := ""
					if len(pr.Images) > 0 {
						image = pr.Images[0].Src
					}
					for _, v := range pr.Variants {
						out.Item(map[string]any{
							"product_id":       pr.ID,
							"title":            pr.Title,
							"handle":           pr.Handle,
							"url":              site + "/products/" + pr.Handle,
							"vendor":           pr.Vendor,
							"product_type":     pr.ProductType,
							"tags":             shopifyTags(pr.Tags),
							"published_at":     pr.PublishedAt,
							"variant_id":       v.ID,
							"variant_title":    v.Title,
							"sku":              v.SKU,
							"barcode":          v.Barcode,
							"price":            v.Price,
							"compare_at_price": v.CompareAtPrice,
							"available":        v.Available,
							"option1":          v.Option1,
							"option2":          v.Option2,
							"option3":          v.Option3,
							"grams":            v.Grams,
							"image":            image,
						})
					}
				}
				if len(p.Products) < perPage { // short page = last page
					return nil
				}
				page := metaInt(r.Request.Meta, "page") + 1
				out.Request(&Request{URL: pageURL(page), Meta: map[string]any{"page": page}})
				return nil
			},
		},
	}
}

func metaInt(m map[string]any, k string) int {
	switch v := m[k].(type) {
	case int:
		return v
	case float64: // after a checkpoint round-trip
		return int(v)
	case string:
		n, _ := strconv.Atoi(v)
		return n
	}
	return 0
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Host
}

// ---- ContentSpider ----

// ContentConfig tunes NewContentSpider.
type ContentConfig struct {
	// Links picks which links to follow. Zero value follows every
	// same-site link (the engine's AllowedDomains still applies).
	Links LinkExtractor
	// MainOnly keeps <main>/<article> content only.
	MainOnly bool
	// Safe strips hidden content and invisible characters, for corpora fed
	// to language models.
	Safe bool
	// MinChars skips pages whose Markdown is shorter (nav-only pages).
	MinChars int
}

// NewContentSpider crawls a site and emits one item per page with its
// Markdown, building a corpus for retrieval (RAG). Items:
// {url, title, markdown, depth}. Pair it with NewMarkdownDirPipeline to
// write one .md file per page.
func NewContentSpider(name string, start []string, cfg ContentConfig) *Spider {
	sp := &Spider{Name: name, StartURLs: start, Callbacks: map[string]Callback{}}
	for _, u := range start {
		if h := hostOf(u); h != "" {
			sp.AllowedDomains = append(sp.AllowedDomains, h)
		}
	}
	sp.Callbacks["parse"] = func(ctx context.Context, r *Response, out *Emitter) error {
		doc, err := r.Doc()
		if err != nil {
			return err
		}
		md := doc.MarkdownWith(markdownOpts(cfg))
		if len([]rune(md)) >= cfg.MinChars {
			title := ""
			if t := doc.First("title"); t != nil {
				title = t.Text()
			}
			out.Item(map[string]any{"url": r.URL, "title": title, "markdown": md, "depth": r.Request.Depth})
		}
		return out.FollowLinks(cfg.Links, "parse")
	}
	return sp
}

// ---- Markdown directory pipeline ----

// MarkdownDirPipeline writes each ContentSpider item to
// <dir>/<host>/<path>.md with a small front matter block.
type MarkdownDirPipeline struct{ Dir string }

// NewMarkdownDirPipeline creates the pipeline; dir is created on demand.
func NewMarkdownDirPipeline(dir string) *MarkdownDirPipeline { return &MarkdownDirPipeline{Dir: dir} }

func (p *MarkdownDirPipeline) Process(_ context.Context, item any) error {
	m, err := toMap(item)
	if err != nil {
		return err
	}
	rawURL, _ := m["url"].(string)
	md, _ := m["markdown"].(string)
	if rawURL == "" || md == "" {
		return fmt.Errorf("markdown pipeline: item needs url and markdown")
	}
	path, err := markdownPath(p.Dir, rawURL)
	if err != nil {
		return err
	}
	title, _ := m["title"].(string)
	body := "---\nurl: " + rawURL + "\ntitle: " + strconv.Quote(title) + "\n---\n\n" + md
	return writeFileAll(path, []byte(body))
}

func (p *MarkdownDirPipeline) Close() error { return nil }
