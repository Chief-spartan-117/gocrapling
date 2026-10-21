package stealth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

func haveChrome() bool {
	for _, n := range []string{"google-chrome", "chromium", "chromium-browser", "chrome"} {
		if _, err := exec.LookPath(n); err == nil {
			return true
		}
	}
	_, err := exec.LookPath("/Applications/Google Chrome.app/Contents/MacOS/Google Chrome")
	return err == nil
}

func TestRecordCapturesXHR(t *testing.T) {
	if testing.Short() || !haveChrome() {
		t.Skip("needs Chrome")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body><div id=out>loading</div><script>
fetch('/api/items?page=1').then(r=>r.json()).then(j=>{document.getElementById('out').textContent='n='+j.items.length})
</script></body></html>`))
	})
	mux.HandleFunc("/api/items", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"items":[1,2,3]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	b, err := New(Options{BlockAds: true, Timeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	resp, err := b.Fetch(context.Background(), srv.URL, FetchOptions{Record: true})
	if err != nil {
		t.Fatal(err)
	}
	got := resp.SearchCaptured(regexp.MustCompile(`/api/items`))
	if len(got) != 1 || !strings.Contains(string(got[0].Body), `"items":[1,2,3]`) || got[0].Status != 200 {
		t.Fatalf("captured = %+v", resp.Captured)
	}
	if !strings.Contains(resp.Text(), "n=3") {
		t.Fatal("page did not render API data")
	}
}

func TestClickTurnstileFindsFrame(t *testing.T) {
	if testing.Short() || !haveChrome() {
		t.Skip("needs Chrome")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		// widget lives in a closed shadow root, like Cloudflare's
		w.Write([]byte(`<html><body><div id=host style="margin:120px"></div><script>
const sr=document.getElementById('host').attachShadow({mode:'closed'});
sr.innerHTML='<iframe src="/frame?from=challenges.cloudflare.com" width=300 height=65 style="border:0"></iframe>';
window.addEventListener('message',e=>{document.title='clicked'});
</script></body></html>`))
	})
	mux.HandleFunc("/frame", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body style="margin:0" onclick="parent.postMessage('c','*')"><div style="width:300px;height:65px;background:#eee">box</div></body></html>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	b, err := New(Options{Timeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	resp, err := b.Fetch(context.Background(), srv.URL, FetchOptions{Actions: []chromedp.Action{
		chromedp.ActionFunc(func(ctx context.Context) error {
			found, err := clickTurnstile(ctx)
			if err != nil || !found {
				t.Errorf("found=%v err=%v", found, err)
			}
			time.Sleep(300 * time.Millisecond)
			return nil
		}),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp.Text(), "<title>clicked</title>") {
		t.Fatalf("frame was not clicked; html: %.300s", resp.Text())
	}
}

func chromePath() string {
	for _, n := range []string{"google-chrome", "chromium", "chromium-browser", "chrome"} {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	return "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
}

func TestScreenshotIsPNG(t *testing.T) {
	if testing.Short() || !haveChrome() {
		t.Skip("needs Chrome")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body style="height:2000px;background:#369">tall</body></html>`))
	}))
	defer srv.Close()
	b, err := New(Options{Timeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	resp, err := b.Fetch(context.Background(), srv.URL, FetchOptions{Screenshot: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Screenshot) < 200 || string(resp.Screenshot[1:4]) != "PNG" {
		t.Fatalf("not a PNG: %d bytes", len(resp.Screenshot))
	}
}

// The page is served from 127.0.0.1 and loads a script from "localhost",
// which is a different host as far as blocking is concerned.
func TestBlockDomainsStopsThirdPartyScript(t *testing.T) {
	if testing.Short() || !haveChrome() {
		t.Skip("needs Chrome")
	}
	tracker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		w.Write([]byte(`document.title='tracker-ran'`))
	}))
	defer tracker.Close()
	_, port, _ := strings.Cut(strings.TrimPrefix(tracker.URL, "http://127.0.0.1"), ":")
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><head><title>clean</title></head><body><script src="http://localhost:` + port + `/t.js"></script></body></html>`))
	}))
	defer page.Close()

	run := func(opt Options) string {
		opt.Timeout = 30 * time.Second
		b, err := New(opt)
		if err != nil {
			t.Fatal(err)
		}
		defer b.Close()
		resp, err := b.Fetch(context.Background(), page.URL, FetchOptions{Settle: 500 * time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		return resp.Text()
	}
	if got := run(Options{}); !strings.Contains(got, "tracker-ran") {
		t.Fatalf("control run: tracker script should execute, html: %.200s", got)
	}
	if got := run(Options{BlockDomains: []string{"localhost"}}); strings.Contains(got, "tracker-ran") {
		t.Fatal("BlockDomains did not stop the script")
	}
}

func TestRemoteAttach(t *testing.T) {
	if testing.Short() || !haveChrome() {
		t.Skip("needs Chrome")
	}
	dir := t.TempDir()
	cmd := exec.Command(chromePath(), "--headless=new", "--remote-debugging-port=9333",
		"--user-data-dir="+dir, "--no-first-run", "--no-default-browser-check", "about:blank")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()
	// wait for the debugging port
	var up bool
	for i := 0; i < 50 && !up; i++ {
		if r, err := http.Get("http://127.0.0.1:9333/json/version"); err == nil {
			r.Body.Close()
			up = true
		} else {
			time.Sleep(200 * time.Millisecond)
		}
	}
	if !up {
		t.Skip("remote debugging port did not open")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body>attached</body></html>`))
	}))
	defer srv.Close()
	b, err := New(Options{RemoteURL: "http://127.0.0.1:9333", Timeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	resp, err := b.Fetch(context.Background(), srv.URL, FetchOptions{})
	if err != nil || !strings.Contains(resp.Text(), "attached") {
		t.Fatalf("resp=%v err=%v", resp, err)
	}
}
