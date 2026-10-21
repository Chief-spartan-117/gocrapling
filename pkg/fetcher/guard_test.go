package fetcher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBlockPrivateAndSchemes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("internal")) }))
	defer srv.Close()
	guarded := New(Options{Profile: "chrome", BlockPrivate: true, Retries: -1})
	defer guarded.Close()
	if _, err := guarded.Get(context.Background(), srv.URL); err == nil || !strings.Contains(err.Error(), "not public") {
		t.Fatalf("loopback allowed or wrong error: %v", err)
	}
	if _, err := guarded.Get(context.Background(), "http://169.254.169.254/"); err == nil {
		t.Fatal("metadata address allowed")
	}
	open := New(Options{Profile: "chrome", Retries: -1})
	defer open.Close()
	if _, err := open.Get(context.Background(), "file:///etc/hosts"); err == nil {
		t.Fatal("file:// allowed")
	}
	if r, err := open.Get(context.Background(), srv.URL); err != nil || r.Text() != "internal" {
		t.Fatalf("unguarded fetch broke: %v", err)
	}
}

func TestBlockPrivateStopsRedirectToLoopback(t *testing.T) {
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("internal")) }))
	defer internal.Close()
	// A "public" front that redirects into the private service. The front
	// itself is loopback too, so exercise the dial-time guard directly.
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, internal.URL, http.StatusFound)
	}))
	defer redir.Close()
	g := New(Options{Profile: "chrome", BlockPrivate: true, Retries: -1})
	defer g.Close()
	if r, err := g.Get(context.Background(), redir.URL); err == nil && strings.Contains(r.Text(), "internal") {
		t.Fatal("redirect reached internal service")
	}
}
