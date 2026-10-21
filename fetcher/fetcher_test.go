package fetcher

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestIsBlocked(t *testing.T) {
	if !IsBlocked(403, nil) || !IsBlocked(429, nil) {
		t.Fatal("status blocks")
	}
	if !IsBlocked(200, []byte("<title>Just a moment...</title>")) {
		t.Fatal("cloudflare interstitial")
	}
	if IsBlocked(200, []byte("<html>hello</html>")) {
		t.Fatal("false positive")
	}
}

func TestPoolRotatorBenchesBadProxy(t *testing.T) {
	r := NewPoolRotator([]string{"http://a:1", "http://b:1"}, false)
	r.MaxFailures = 2
	for i := 0; i < 2; i++ {
		r.MarkBad("http://a:1")
	}
	for i := 0; i < 5; i++ {
		if got := r.Next(); got != "http://b:1" {
			t.Fatalf("got %s, benched proxy returned", got)
		}
	}
}

func TestRetryThenSuccess(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	f := New(Options{Retries: 3, Backoff: 10 * time.Millisecond})
	defer f.Close()
	resp, err := f.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text() != "ok" || resp.Attempts != 3 {
		t.Fatalf("body=%q attempts=%d", resp.Text(), resp.Attempts)
	}
}

func TestBlockedExhaustsRetries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("Attention Required! | Cloudflare"))
	}))
	defer srv.Close()

	f := New(Options{Retries: 1, Backoff: 5 * time.Millisecond})
	defer f.Close()
	resp, err := f.Get(context.Background(), srv.URL)
	if !errors.Is(err, ErrBlocked) || resp == nil || !resp.Blocked {
		t.Fatalf("err=%v resp=%+v", err, resp)
	}
}

func TestSendsBrowserHeaders(t *testing.T) {
	var ua, ch string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua, ch = r.Header.Get("User-Agent"), r.Header.Get("Accept-Language")
	}))
	defer srv.Close()
	f := New(Options{Profile: "firefox", Retries: -1})
	defer f.Close()
	if _, err := f.Get(context.Background(), srv.URL); err != nil {
		t.Fatal(err)
	}
	if ua == "" || ch == "" || ua[:7] != "Mozilla" {
		t.Fatalf("ua=%q lang=%q", ua, ch)
	}
}

func TestSessionKeepsCookiesAndIdentity(t *testing.T) {
	var uas []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uas = append(uas, r.Header.Get("User-Agent"))
		if r.URL.Path == "/login" {
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: "abc", Path: "/"})
			return
		}
		if c, err := r.Cookie("sid"); err != nil || c.Value != "abc" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte("welcome"))
	}))
	defer srv.Close()

	s, err := NewSession(Options{Profile: "random", Retries: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err := s.Get(ctx, srv.URL+"/login"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		r, err := s.Get(ctx, srv.URL+"/me")
		if err != nil || r.Text() != "welcome" {
			t.Fatalf("not logged in: %v %v", r, err)
		}
	}
	for _, ua := range uas {
		if ua != uas[0] || ua != s.UserAgent() {
			t.Fatalf("identity changed within session: %v", uas)
		}
	}
	cs, _ := s.Cookies(srv.URL)
	if len(cs) != 1 || cs[0].Name != "sid" {
		t.Fatalf("cookies = %v", cs)
	}
}

func TestParseCurl(t *testing.T) {
	r, err := ParseCurl(`curl 'https://api.x.test/v1/items?a=1' \
  -H 'accept: application/json' \
  -H $'x-note: line1\nline2' \
  -H "Authorization: Bearer abc" \
  -b 'sid=1; t=2' -e https://ref.test/ \
  --data-raw '{"q":"hi \"there\""}' --compressed -s -L`)
	if err != nil {
		t.Fatal(err)
	}
	if r.Method != "POST" || r.URL != "https://api.x.test/v1/items?a=1" || string(r.Body) != `{"q":"hi \"there\""}` {
		t.Fatalf("%+v", r)
	}
	if r.Headers["accept"] != "application/json" || r.Headers["authorization"] != "Bearer abc" ||
		r.Headers["cookie"] != "sid=1; t=2" || r.Headers["x-note"] != "line1\nline2" || r.Referer != "https://ref.test/" {
		t.Fatalf("headers: %+v", r.Headers)
	}
	r, err = ParseCurl(`curl -G --url=https://x.test/s -d q=go -d n=2 -u user:pass`)
	if err != nil || r.URL != "https://x.test/s?q=go&n=2" || r.Method != "GET" || r.Headers["authorization"] != "Basic dXNlcjpwYXNz" {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := ParseCurl(`curl -H`); err == nil {
		t.Fatal("expected error for missing value")
	}
}
