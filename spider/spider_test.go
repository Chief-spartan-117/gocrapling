package spider

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"sort"
	"sync"
	"testing"
	"time"

	http "github.com/bogdanfinn/fhttp"

	"github.com/sagarshrestha/gocrapling/fetcher"
)

// fakeSite serves canned pages.
type fakeSite struct {
	mu    sync.Mutex
	pages map[string]string
	hits  map[string]int
	delay time.Duration
}

func (f *fakeSite) Do(ctx context.Context, r fetcher.Request) (*fetcher.Response, error) {
	f.mu.Lock()
	f.hits[r.URL]++
	body, ok := f.pages[r.URL]
	f.mu.Unlock()
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if !ok {
		return &fetcher.Response{Status: 404, URL: r.URL, Headers: http.Header{}}, nil
	}
	return &fetcher.Response{Status: 200, URL: r.URL, Body: []byte(body), Headers: http.Header{}}, nil
}

func site() *fakeSite {
	return &fakeSite{hits: map[string]int{}, pages: map[string]string{
		"https://x.test/robots.txt": "User-agent: *\nDisallow: /private\nAllow: /private/ok\n",
		"https://x.test/":           `<a href="/p1">1</a><a href="/p2">2</a><a href="/p1#frag">dup</a><a href="/private/secret">no</a><a href="https://other.test/">off</a>`,
		"https://x.test/p1":         `<h1>One</h1><a href="/p2">next</a>`,
		"https://x.test/p2":         `<h1>Two</h1>`,
	}}
}

func testSpider(items *[]string, mu *sync.Mutex) *Spider {
	return &Spider{
		Name:           "t",
		StartURLs:      []string{"https://x.test/"},
		AllowedDomains: []string{"x.test"},
		Callbacks: map[string]Callback{
			"parse": func(ctx context.Context, r *Response, out *Emitter) error {
				doc, err := r.Doc()
				if err != nil {
					return err
				}
				for _, h := range doc.Extract("a::attr(href)") {
					out.Follow(h, "page")
				}
				return nil
			},
			"page": func(ctx context.Context, r *Response, out *Emitter) error {
				doc, _ := r.Doc()
				mu.Lock()
				*items = append(*items, doc.First("h1").Text())
				mu.Unlock()
				out.Item(map[string]string{"title": doc.First("h1").Text(), "url": r.URL})
				for _, h := range doc.Extract("a::attr(href)") {
					out.Follow(h, "page")
				}
				return nil
			},
		},
	}
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestCrawlDedupRobotsOffsite(t *testing.T) {
	fs := site()
	var (
		items []string
		mu    sync.Mutex
		got   int
	)
	eng, err := NewEngine(Config{
		HTTP: fs, RespectRobots: true, Concurrency: 4, Logger: quiet(),
		Pipelines: []Pipeline{FuncPipeline(func(_ context.Context, it any) error { got++; return nil })},
	})
	if err != nil {
		t.Fatal(err)
	}
	st, err := eng.Run(context.Background(), testSpider(&items, &mu))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || got != 2 {
		t.Fatalf("items=%v pipeline=%d", items, got)
	}
	if fs.hits["https://x.test/p1"] != 1 || fs.hits["https://x.test/p2"] != 1 {
		t.Fatalf("dedup failed: %v", fs.hits)
	}
	if fs.hits["https://x.test/private/secret"] != 0 || st.RobotsDenied.Load() != 1 {
		t.Fatalf("robots not honoured: hits=%v denied=%d", fs.hits, st.RobotsDenied.Load())
	}
	if fs.hits["https://other.test/"] != 0 {
		t.Fatal("offsite followed")
	}
}

func TestCheckpointResume(t *testing.T) {
	fs := site()
	fs.delay = 150 * time.Millisecond
	cp := t.TempDir() + "/cp.json"
	var (
		items []string
		mu    sync.Mutex
	)
	newEng := func() *Engine {
		e, _ := NewEngine(Config{HTTP: fs, Concurrency: 1, CheckpointPath: cp, Logger: quiet()})
		return e
	}

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if _, err := newEng().Run(ctx, testSpider(&items, &mu)); err == nil {
		t.Fatal("expected interruption")
	}
	if _, err := loadCheckpoint(cp); err != nil {
		t.Fatalf("checkpoint unreadable: %v", err)
	}

	fs.delay = 0
	if _, err := newEng().Run(context.Background(), testSpider(&items, &mu)); err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("resumed crawl items = %v", items)
	}
	if c, _ := loadCheckpoint(cp); c != nil {
		t.Fatal("checkpoint should be removed after complete crawl")
	}
	for u, n := range fs.hits {
		if u != "https://x.test/" && n > 2 {
			t.Fatalf("%s fetched %d times", u, n)
		}
	}
}

func TestRobotsParse(t *testing.T) {
	r := parseRobots("User-agent: *\nDisallow: /a\nAllow: /a/b\nDisallow: /*.pdf$\nCrawl-delay: 2\n", "gocrapling")
	cases := map[string]bool{"/a": false, "/a/x": false, "/a/b": true, "/z": true, "/f.pdf": false, "/f.pdf?x": true}
	for p, want := range cases {
		if got := r.allowed(p); got != want {
			t.Errorf("%s allowed=%v want %v", p, got, want)
		}
	}
	if r.crawlDelay != 2*time.Second {
		t.Errorf("crawl delay %v", r.crawlDelay)
	}
}

func TestPriorityOrder(t *testing.T) {
	s := newScheduler()
	s.Push(&Request{URL: "https://a/1", Priority: 0})
	s.Push(&Request{URL: "https://a/2", Priority: 5})
	s.Push(&Request{URL: "https://a/3", Priority: 0})
	var order []string
	for i := 0; i < 3; i++ {
		r, _ := s.Pop(context.Background())
		order = append(order, r.URL)
	}
	if fmt.Sprint(order) != "[https://a/2 https://a/1 https://a/3]" {
		t.Fatalf("order = %v", order)
	}
}

func TestMiddlewareRetryAndDrop(t *testing.T) {
	fs := site()
	var (
		items []string
		mu    sync.Mutex
		seen  = map[string]int{}
	)
	mw := Middleware{
		Name: "t",
		OnRequest: func(_ context.Context, r *Request) error {
			if r.URL == "https://x.test/p2" {
				return ErrDrop
			}
			if r.Headers == nil {
				r.Headers = map[string]string{}
			}
			r.Headers["x-test"] = "1"
			return nil
		},
		OnResponse: func(_ context.Context, r *Request, _ *fetcher.Response) error {
			mu.Lock()
			defer mu.Unlock()
			seen[r.URL]++
			if r.URL == "https://x.test/p1" && seen[r.URL] < 3 {
				return ErrRetry
			}
			return nil
		},
	}
	eng, _ := NewEngine(Config{HTTP: fs, Concurrency: 2, Middlewares: []Middleware{mw}, Logger: quiet()})
	if _, err := eng.Run(context.Background(), testSpider(&items, &mu)); err != nil {
		t.Fatal(err)
	}
	if fs.hits["https://x.test/p1"] != 3 {
		t.Fatalf("p1 fetched %d times, want 3", fs.hits["https://x.test/p1"])
	}
	if fs.hits["https://x.test/p2"] != 0 {
		t.Fatal("dropped request was fetched")
	}
	if len(items) != 1 || items[0] != "One" {
		t.Fatalf("items = %v", items)
	}
}

type tagDL struct {
	name string
	hits *sync.Map
	next Downloader
}

func (d tagDL) Do(ctx context.Context, r fetcher.Request) (*fetcher.Response, error) {
	d.hits.Store(r.URL, d.name)
	return d.next.Do(ctx, r)
}

func TestRoutesPickDownloader(t *testing.T) {
	fs := site()
	var hits sync.Map
	var (
		items []string
		mu    sync.Mutex
	)
	eng, _ := NewEngine(Config{
		HTTP:    tagDL{"http", &hits, fs},
		Browser: tagDL{"browser", &hits, fs},
		Routes:  []Route{{Host: "x.test", Mode: ModeBrowser}},
		Logger:  quiet(),
	})
	if _, err := eng.Run(context.Background(), testSpider(&items, &mu)); err != nil {
		t.Fatal(err)
	}
	if v, _ := hits.Load("https://x.test/p1"); v != "browser" {
		t.Fatalf("p1 via %v, want browser", v)
	}

	hits = sync.Map{}
	eng, _ = NewEngine(Config{
		HTTP: tagDL{"http", &hits, fs}, Browser: tagDL{"browser", &hits, fs},
		Routes: []Route{{Host: "x.test", Mode: ModeHTTP}}, Logger: quiet(),
	})
	items = nil
	if _, err := eng.Run(context.Background(), testSpider(&items, &mu)); err != nil {
		t.Fatal(err)
	}
	if v, _ := hits.Load("https://x.test/p1"); v != "http" {
		t.Fatalf("p1 via %v, want http", v)
	}
}

func TestCSVPipeline(t *testing.T) {
	path := t.TempDir() + "/o.csv"
	p, err := NewCSVPipeline(path, "url", "title", "tags")
	if err != nil {
		t.Fatal(err)
	}
	_ = p.Process(context.Background(), map[string]any{"url": "u1", "title": "A, \"quoted\"", "tags": []string{"x", "y"}, "extra": 1})
	_ = p.Process(context.Background(), struct {
		URL   string `json:"url"`
		Title string `json:"title"`
	}{"u2", "B"})
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	want := "url,title,tags\nu1,\"A, \"\"quoted\"\"\",\"[\"\"x\"\",\"\"y\"\"]\"\nu2,B,\n"
	if string(b) != want {
		t.Fatalf("csv =\n%q\nwant\n%q", b, want)
	}
}

func TestLinkExtractorAndCrawlSpider(t *testing.T) {
	fs := &fakeSite{hits: map[string]int{}, pages: map[string]string{
		"https://x.test/":       `<a href="/cat/a">a</a><a href="/img.png">i</a><a href="/about">about</a><a href="https://ext.test/cat/z">z</a>`,
		"https://x.test/cat/a":  `<h1>A</h1><a href="/item/1">1</a>`,
		"https://x.test/item/1": `<h1>Item1</h1>`,
		"https://x.test/about":  `<h1>About</h1>`,
	}}
	var got []string
	var mu sync.Mutex
	rec := func(ctx context.Context, r *Response, out *Emitter) error {
		d, _ := r.Doc()
		mu.Lock()
		got = append(got, d.First("h1").Text())
		mu.Unlock()
		return nil
	}
	sp := NewCrawlSpider("c", []string{"https://x.test/"}, []CrawlRule{
		{Extractor: LinkExtractor{Allow: []*regexp.Regexp{regexp.MustCompile(`/cat/`)}, AllowDomains: []string{"x.test"}}, Follow: true},
		{Extractor: LinkExtractor{Allow: []*regexp.Regexp{regexp.MustCompile(`/item/`)}}, Callback: "item"},
	}, map[string]Callback{"item": rec})
	eng, _ := NewEngine(Config{HTTP: fs, Logger: quiet()})
	if _, err := eng.Run(context.Background(), sp); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "Item1" {
		t.Fatalf("got %v", got)
	}
	if fs.hits["https://x.test/img.png"] != 0 || fs.hits["https://x.test/about"] != 0 || fs.hits["https://ext.test/cat/z"] != 0 {
		t.Fatalf("filtered links fetched: %v", fs.hits)
	}
}

func TestSitemapSpider(t *testing.T) {
	fs := &fakeSite{hits: map[string]int{}, pages: map[string]string{
		"https://x.test/robots.txt": "Sitemap: https://x.test/index.xml\n",
		"https://x.test/index.xml":  `<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><sitemap><loc>https://x.test/s1.xml</loc></sitemap></sitemapindex>`,
		"https://x.test/s1.xml":     `<urlset><url><loc>https://x.test/p/1</loc></url><url><loc>https://x.test/skip/2</loc></url></urlset>`,
		"https://x.test/p/1":        `<h1>P1</h1>`,
		"https://x.test/skip/2":     `<h1>S2</h1>`,
	}}
	var got []string
	var mu sync.Mutex
	sp := NewSitemapSpider("s", []string{"https://x.test/robots.txt"},
		[]SitemapRule{{Pattern: regexp.MustCompile(`/p/`), Callback: "page"}},
		map[string]Callback{"page": func(ctx context.Context, r *Response, out *Emitter) error {
			d, _ := r.Doc()
			mu.Lock()
			got = append(got, d.First("h1").Text())
			mu.Unlock()
			return nil
		}})
	eng, _ := NewEngine(Config{HTTP: fs, Logger: quiet()})
	if _, err := eng.Run(context.Background(), sp); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "P1" || fs.hits["https://x.test/skip/2"] != 0 {
		t.Fatalf("got %v hits %v", got, fs.hits)
	}
}

func TestCachingDownloader(t *testing.T) {
	fs := site()
	c, err := NewCachingDownloader(fs, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		r, err := c.Do(context.Background(), fetcher.Request{URL: "https://x.test/p1"})
		if err != nil || r.Status != 200 {
			t.Fatalf("%v %v", r, err)
		}
	}
	if fs.hits["https://x.test/p1"] != 1 {
		t.Fatalf("origin hit %d times, want 1", fs.hits["https://x.test/p1"])
	}
	// 404s are not cached
	for i := 0; i < 2; i++ {
		c.Do(context.Background(), fetcher.Request{URL: "https://x.test/missing"})
	}
	if fs.hits["https://x.test/missing"] != 2 {
		t.Fatal("non-2xx must not be cached")
	}
}

func TestAutoThrottleObserve(t *testing.T) {
	cfg := AutoThrottle{Enabled: true}.withDefaults()
	h := &hostThrottle{delay: time.Second}
	h.observe(cfg, 100*time.Millisecond, 200, false, 0)
	if d := h.current(); d >= time.Second || d < 500*time.Millisecond {
		t.Fatalf("fast 200 should shrink delay toward latency, got %v", d)
	}
	before := h.current()
	h.observe(cfg, time.Millisecond, 404, false, 0)
	if h.current() < before {
		t.Fatal("error response must not speed up")
	}
	h.observe(cfg, time.Millisecond, 429, false, 0)
	if h.current() < 2*before-time.Millisecond {
		t.Fatalf("429 should double, got %v from %v", h.current(), before)
	}
}

func TestRobotsRequestRate(t *testing.T) {
	r := parseRobots("User-agent: *\nRequest-rate: 1/10\n", "x")
	if r.crawlDelay != 10*time.Second {
		t.Fatalf("delay %v", r.crawlDelay)
	}
	r = parseRobots("User-agent: *\nCrawl-delay: 3\nRequest-rate: 2/10\n", "x")
	if r.crawlDelay != 5*time.Second {
		t.Fatalf("delay %v, want max(3s, 5s)", r.crawlDelay)
	}
}

func TestStreamYieldsItems(t *testing.T) {
	fs := site()
	var items []string
	var mu sync.Mutex
	eng, _ := NewEngine(Config{HTTP: fs, Logger: quiet()})
	ch, wait := eng.Stream(context.Background(), testSpider(&items, &mu))
	n := 0
	for range ch {
		n++
	}
	st, err := wait()
	if err != nil || n != 2 || st.Items.Load() != 2 {
		t.Fatalf("n=%d stats=%v err=%v", n, st, err)
	}
}

func TestXMLPipeline(t *testing.T) {
	path := t.TempDir() + "/o.xml"
	p, err := NewXMLPipeline(path, "", "")
	if err != nil {
		t.Fatal(err)
	}
	_ = p.Process(context.Background(), map[string]any{"title": "A & <B>", "tags": []string{"x", "y"}, "bad name": 1})
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	var doc struct {
		Items []struct {
			Title string   `xml:"title"`
			Tags  []string `xml:"tags"`
			Bad   string   `xml:"bad_name"`
		} `xml:"item"`
	}
	if err := xml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("invalid xml: %v\n%s", err, b)
	}
	if len(doc.Items) != 1 || doc.Items[0].Title != "A & <B>" || len(doc.Items[0].Tags) != 2 || doc.Items[0].Bad != "1" {
		t.Fatalf("%+v\n%s", doc, b)
	}
}

func TestFeedSpiders(t *testing.T) {
	fs := &fakeSite{hits: map[string]int{}, pages: map[string]string{
		"https://x.test/rss":   `<rss><channel><title>ch</title><item><title>One</title><link>https://x/1</link></item><item><title>Two</title><link>https://x/2</link></item></channel></rss>`,
		"https://x.test/atom":  `<feed><entry><title>E1</title><link href="https://x/e1"/></entry></feed>`,
		"https://x.test/a.csv": "name;qty\nwidget;3\ngadget;5\n",
	}}
	var titles []string
	var mu sync.Mutex
	collect := func(s string) { mu.Lock(); titles = append(titles, s); mu.Unlock() }
	run := func(sp *Spider) {
		eng, _ := NewEngine(Config{HTTP: fs, Logger: quiet()})
		if _, err := eng.Run(context.Background(), sp); err != nil {
			t.Fatal(err)
		}
	}
	run(NewXMLFeedSpider("rss", []string{"https://x.test/rss"}, "item", func(_ context.Context, _ *Response, it map[string]string, _ *Emitter) error {
		collect(it["title"] + "|" + it["link"])
		return nil
	}))
	run(NewXMLFeedSpider("atom", []string{"https://x.test/atom"}, "entry", func(_ context.Context, _ *Response, it map[string]string, _ *Emitter) error {
		collect(it["title"] + "|" + it["link@href"])
		return nil
	}))
	run(NewCSVFeedSpider("csv", []string{"https://x.test/a.csv"}, CSVFeedOptions{Delimiter: ';'}, func(_ context.Context, _ *Response, row map[string]string, _ *Emitter) error {
		collect(row["name"] + "=" + row["qty"])
		return nil
	}))
	sort.Strings(titles)
	want := "[E1|https://x/e1 One|https://x/1 Two|https://x/2 gadget=5 widget=3]"
	if fmt.Sprint(titles) != want {
		t.Fatalf("got %v", titles)
	}
}
