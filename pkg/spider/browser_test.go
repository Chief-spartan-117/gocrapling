package spider_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Chief-spartan-117/gocrapling/pkg/fetcher"
	"github.com/Chief-spartan-117/gocrapling/pkg/spider"
	"github.com/Chief-spartan-117/gocrapling/pkg/stealth"
)

// A JS-rendered page crawled with a real Chrome through a Route.
func TestRoutedBrowserRendersJS(t *testing.T) {
	if testing.Short() {
		t.Skip("needs Chrome")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body><h1 id=t>static</h1><script>document.getElementById('t').textContent='rendered by js'</script></body></html>`))
	}))
	defer srv.Close()

	b, err := stealth.New(stealth.Options{Timeout: 30 * time.Second})
	if err != nil {
		t.Skipf("no Chrome: %v", err)
	}
	defer b.Close()
	f := fetcher.New(fetcher.Options{Retries: -1})
	defer f.Close()

	crawl := func(mode spider.Mode) string {
		var mu sync.Mutex
		var title string
		sp := &spider.Spider{Name: "r", StartURLs: []string{srv.URL}, Callbacks: map[string]spider.Callback{
			"parse": func(ctx context.Context, r *spider.Response, out *spider.Emitter) error {
				d, _ := r.Doc()
				mu.Lock()
				title = d.First("h1").Text()
				mu.Unlock()
				return nil
			}}}
		eng, _ := spider.NewEngine(spider.Config{
			HTTP: f, Browser: b, Routes: []spider.Route{{Host: "127.0.0.1", Mode: mode}},
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
		if _, err := eng.Run(context.Background(), sp); err != nil {
			t.Fatal(err)
		}
		return title
	}
	if got := crawl(spider.ModeHTTP); got != "static" {
		t.Fatalf("HTTP route saw %q, want static", got)
	}
	if got := crawl(spider.ModeBrowser); got != "rendered by js" {
		t.Fatalf("browser route saw %q, want rendered by js", got)
	}
}
