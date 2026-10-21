package spider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Chief-spartan-117/gocrapling/pkg/fetcher"
)

func TestRetryAfterRaisesDelay(t *testing.T) {
	cfg := AutoThrottle{Enabled: true}.withDefaults()
	h := &hostThrottle{delay: 100 * time.Millisecond}
	h.observe(cfg, 10*time.Millisecond, 429, false, 3*time.Second)
	if h.current() < 3*time.Second && h.current() != cfg.MaxDelay {
		t.Fatalf("delay %v should honour Retry-After 3s", h.current())
	}
	if parseRetryAfter("7") != 7*time.Second || parseRetryAfter("junk") != 0 {
		t.Fatal("parseRetryAfter")
	}
}

func TestShopifySpiderVariants(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") != "1" {
			fmt.Fprint(w, `{"products":[]}`)
			return
		}
		fmt.Fprint(w, `{"products":[{"id":1,"title":"Shirt","handle":"shirt","vendor":"V","tags":"a, b",
		"variants":[{"id":11,"title":"S","sku":"S1","price":"10.00","available":true},{"id":12,"title":"M","sku":"M1","price":"11.00"}],
		"images":[{"src":"http://img/1.png"}]}]}`)
	}))
	defer srv.Close()
	dir := t.TempDir()
	p, _ := NewJSONPipeline(filepath.Join(dir, "o.json"))
	eng, _ := NewEngine(Config{HTTP: fetcher.New(fetcher.Options{Profile: "chrome"}), Pipelines: []Pipeline{p}})
	if _, err := eng.Run(context.Background(), NewShopifySpider("shop", srv.URL)); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "o.json"))
	var items []map[string]any
	if err := json.Unmarshal(b, &items); err != nil {
		t.Fatalf("invalid JSON array: %v\n%s", err, b)
	}
	if len(items) != 2 || items[0]["sku"] == items[1]["sku"] {
		t.Fatalf("want 2 variant rows, got %v", items)
	}
}

func TestSessionsAndProxyAndBlockHook(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, "<html><body>captcha-wall</body></html>")
	}))
	defer srv.Close()
	var sessionUsed atomic.Bool
	sess := downloaderFunc(func(ctx context.Context, r fetcher.Request) (*fetcher.Response, error) {
		sessionUsed.Store(true)
		return &fetcher.Response{Status: 200, URL: r.URL, Body: []byte("ok")}, nil
	})
	var blockedSeen atomic.Bool
	sp := &Spider{Name: "t", StartRequests: func() []*Request {
		return []*Request{{URL: srv.URL + "/a", Session: "alt"}, {URL: srv.URL + "/b"}}
	}, Callbacks: map[string]Callback{"parse": func(ctx context.Context, r *Response, out *Emitter) error { return nil }},
		IsBlocked: func(r *fetcher.Response) bool {
			ok := strings.Contains(string(r.Body), "captcha-wall")
			if ok {
				blockedSeen.Store(true)
			}
			return ok
		}}
	eng, _ := NewEngine(Config{HTTP: fetcher.New(fetcher.Options{Profile: "chrome"}), Sessions: map[string]Downloader{"alt": sess}, MaxHookRetries: 1})
	if _, err := eng.Run(context.Background(), sp); err != nil {
		t.Fatal(err)
	}
	if !sessionUsed.Load() || !blockedSeen.Load() {
		t.Fatalf("session=%v blockHook=%v", sessionUsed.Load(), blockedSeen.Load())
	}
}

func TestLinkExtractorXPathCanonicalize(t *testing.T) {
	html := `<html><body><div id="nav"><a href="/x?b=2&a=1">x</a><a href="/y">y</a></div><div id="foot"><a href="/z">z</a></div></body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, html) }))
	defer srv.Close()
	f := fetcher.New(fetcher.Options{Profile: "chrome"})
	resp, err := f.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	r := &Response{Response: resp, Request: &Request{URL: srv.URL}}
	links, err := LinkExtractor{RestrictXPath: `//div[@id="nav"]`, Canonicalize: true}.Extract(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 2 || !strings.HasSuffix(links[0], "/x?a=1&b=2") {
		t.Fatalf("got %v", links)
	}
}

func TestMarkdownPathSafe(t *testing.T) {
	p, err := markdownPath("/out", "https://h.test/../../etc/passwd?x=1")
	if err != nil || !strings.HasPrefix(p, "/out/") {
		t.Fatalf("%q %v", p, err)
	}
}

type downloaderFunc func(ctx context.Context, r fetcher.Request) (*fetcher.Response, error)

func (f downloaderFunc) Do(ctx context.Context, r fetcher.Request) (*fetcher.Response, error) {
	return f(ctx, r)
}

func TestContentSpiderWritesMarkdown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			fmt.Fprint(w, `<html><head><title>Home</title></head><body><main><h1>Home</h1><a href="/docs/a">a</a></main></body></html>`)
		case "/docs/a":
			fmt.Fprint(w, `<html><head><title>A</title></head><body><main><h1>Page A</h1><p style="display:none">HIDDEN</p></main></body></html>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	eng, _ := NewEngine(Config{HTTP: fetcher.New(fetcher.Options{Profile: "chrome"}), MaxDepth: 2,
		Pipelines: []Pipeline{NewMarkdownDirPipeline(dir)}})
	sp := NewContentSpider("docs", []string{srv.URL + "/"}, ContentConfig{MainOnly: true, Safe: true})
	if _, err := eng.Run(context.Background(), sp); err != nil {
		t.Fatal(err)
	}
	var found []string
	filepath.Walk(dir, func(p string, i os.FileInfo, _ error) error {
		if !i.IsDir() {
			b, _ := os.ReadFile(p)
			found = append(found, p+": "+string(b))
		}
		return nil
	})
	all := strings.Join(found, "\n")
	if len(found) != 2 || !strings.Contains(all, "# Page A") || strings.Contains(all, "HIDDEN") {
		t.Fatalf("files=%d\n%s", len(found), all)
	}
}

func TestCSVFormulaInjectionNeutralized(t *testing.T) {
	for in, want := range map[string]string{
		"=HYPERLINK(\"http://x\")": "'=HYPERLINK(\"http://x\")", "+cmd|' /C calc'!A0": "'+cmd|' /C calc'!A0",
		"@SUM(1)": "'@SUM(1)", "-2+3": "'-2+3", "-5": "-5", "+1.5": "+1.5", "plain": "plain", "": "",
	} {
		if got := cell(in); got != want {
			t.Errorf("cell(%q)=%q want %q", in, got, want)
		}
	}
}
