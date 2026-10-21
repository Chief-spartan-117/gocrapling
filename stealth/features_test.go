package stealth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestBrowser(t *testing.T, opt Options) *Browser {
	t.Helper()
	if testing.Short() || !haveChrome() {
		t.Skip("needs Chrome")
	}
	opt.Timeout = 30 * time.Second
	b, err := New(opt)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	return b
}

func TestDisableResourcesSkipsImagesAndFonts(t *testing.T) {
	var png, font, css atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(`<html><head><link rel=stylesheet href="/a.css"></head><body><img src="/pic.png"><p>hi</p></body></html>`))
		case "/pic.png":
			png.Add(1)
		case "/a.css":
			css.Add(1)
			w.Header().Set("Content-Type", "text/css")
			w.Write([]byte(`@font-face{font-family:x;src:url(/f.woff2)} body{font-family:x}`))
		case "/f.woff2":
			font.Add(1)
		}
	}))
	defer srv.Close()

	b := newTestBrowser(t, Options{})
	if _, err := b.Fetch(context.Background(), srv.URL, FetchOptions{WaitIdle: true}); err != nil {
		t.Fatal(err)
	}
	if png.Load() == 0 || css.Load() == 0 {
		t.Fatalf("control run should load image and css: png=%d css=%d", png.Load(), css.Load())
	}

	png.Store(0)
	font.Store(0)
	css.Store(0)
	b2 := newTestBrowser(t, Options{DisableResources: true})
	if _, err := b2.Fetch(context.Background(), srv.URL, FetchOptions{WaitIdle: true}); err != nil {
		t.Fatal(err)
	}
	if png.Load() != 0 || font.Load() != 0 {
		t.Fatalf("resources were fetched: png=%d font=%d", png.Load(), font.Load())
	}
	if css.Load() == 0 {
		t.Fatal("stylesheets must still load")
	}
}

func TestWaitIdleWaitsForLateRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			// request starts 300ms after load and takes 300ms
			w.Write([]byte(`<html><body><div id=o>none</div><script>
setTimeout(()=>fetch('/slow').then(r=>r.text()).then(t=>{document.getElementById('o').textContent=t}),300)
</script></body></html>`))
		case "/slow":
			time.Sleep(300 * time.Millisecond)
			w.Write([]byte("late-data"))
		}
	}))
	defer srv.Close()
	b := newTestBrowser(t, Options{})
	plain, err := b.Fetch(context.Background(), srv.URL, FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain.Text(), "late-data") {
		t.Skip("timing: page settled without waiting; cannot distinguish")
	}
	idle, err := b.Fetch(context.Background(), srv.URL, FetchOptions{WaitIdle: true, Settle: 0})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(idle.Text(), "late-data") {
		t.Fatalf("WaitIdle returned before the late request finished: %.300s", idle.Text())
	}
}

func TestSpoofRefererOnNavigationOnly(t *testing.T) {
	var mu sync.Mutex
	refs := map[string]string{}
	// https target: Chrome strips a https referrer on http downgrades.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		refs[r.URL.Path] = r.Header.Get("Referer")
		mu.Unlock()
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(`<html><body>x<script src="/s.js"></script></body></html>`))
		}
	}))
	defer srv.Close()
	b := newTestBrowser(t, Options{SpoofReferer: true, ExtraFlags: []string{"ignore-certificate-errors"}})
	if _, err := b.Fetch(context.Background(), srv.URL, FetchOptions{WaitIdle: true}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if refs["/"] != "https://www.google.com/" {
		t.Fatalf("document referer = %q", refs["/"])
	}
	if strings.Contains(refs["/s.js"], "google.com") {
		t.Fatalf("subresource must not carry the spoofed referer: %q", refs["/s.js"])
	}
}

func TestStateSaveAndRestoreLogin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/login":
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: "secret42", Path: "/", Expires: time.Now().Add(time.Hour), HttpOnly: true})
			w.Write([]byte("logged in"))
		default:
			if c, err := r.Cookie("sid"); err == nil {
				w.Write([]byte("welcome " + c.Value))
				return
			}
			w.Write([]byte("anonymous"))
		}
	}))
	defer srv.Close()
	path := t.TempDir() + "/state.json"

	b1 := newTestBrowser(t, Options{})
	if _, err := b1.Fetch(context.Background(), srv.URL+"/login", FetchOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := b1.SaveState(context.Background(), path); err != nil {
		t.Fatal(err)
	}

	b2 := newTestBrowser(t, Options{StateFile: path})
	r, err := b2.Fetch(context.Background(), srv.URL+"/me", FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Text(), "welcome secret42") {
		t.Fatalf("login not restored: %.200s", r.Text())
	}
}

func TestStepsDriveThePage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body>
<input id=q><button id=go onclick="document.getElementById('out').textContent='q='+document.getElementById('q').value">go</button>
<div id=out></div>
<script>document.getElementById('q').addEventListener('keydown',e=>{if(e.key==='Enter')document.getElementById('out').textContent='enter:'+e.target.value})</script>
</body></html>`))
	}))
	defer srv.Close()
	b := newTestBrowser(t, Options{})

	steps, err := ParseSteps([]byte(`[{"type":"fill","selector":"#q","text":"golang"},{"type":"click","selector":"#go"},{"type":"wait_for","selector":"#out"},{"type":"evaluate","script":"document.title='done'"}]`))
	if err != nil {
		t.Fatal(err)
	}
	acts, err := Actions(steps)
	if err != nil {
		t.Fatal(err)
	}
	r, err := b.Fetch(context.Background(), srv.URL, FetchOptions{Actions: acts})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Text(), "q=golang") || !strings.Contains(r.Text(), "<title>done</title>") {
		t.Fatalf("%.400s", r.Text())
	}

	steps, _ = ParseSteps([]byte(`[{"type":"fill","selector":"#q","text":"hi"},{"type":"press","key":"Enter"},{"type":"wait","ms":200}]`))
	acts, _ = Actions(steps)
	r, err = b.Fetch(context.Background(), srv.URL, FetchOptions{Actions: acts})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Text(), "enter:hi") {
		t.Fatalf("press: %.400s", r.Text())
	}

	if _, err := Actions([]Step{{Type: "click"}}); err == nil {
		t.Fatal("click without selector must fail")
	}
	if _, err := Actions([]Step{{Type: "teleport"}}); err == nil {
		t.Fatal("unknown type must fail")
	}
}
