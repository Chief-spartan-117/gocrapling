package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sagarshrestha/gocrapling/fetcher"
	"github.com/sagarshrestha/gocrapling/stealth"
)

func TestShellSession(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body><ul id="l"><li class="i">Alpha</li><li class="i">Beta</li></ul><a href="https://o.test/x">x</a></body></html>`))
	}))
	defer srv.Close()
	f := fetcher.New(fetcher.Options{Retries: -1})
	defer f.Close()

	script := strings.Join([]string{
		"get " + srv.URL,
		"css li.i",
		"path 2",
		"similar 1",
		"css li.i::text",
		"find Beta",
		"links",
		"curl '" + srv.URL + "' -H 'x-a: 1'",
		"bogus",
		"quit",
	}, "\n")
	var out bytes.Buffer
	err := runShell(context.Background(), strings.NewReader(script), &out, f,
		func() (*stealth.Browser, error) { return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"HTTP 200", "2 results", "css:   #l > li:nth-of-type(2)", "xpath: //*[@id=\"l\"]/li[2]", "Alpha", "https://o.test/x", "GET " + srv.URL, `unknown command "bogus"`} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}
