package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func restCall(t *testing.T, base, path, token, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, base+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return resp.StatusCode, m
}

func TestRESTFetchCrawlAndErrors(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body><main><h1>Hello</h1><a href="/b">b</a></main><div style="display:none">IGNORE ME</div></body></html>`)
	}))
	defer site.Close()
	s := New(Options{AllowPrivate: true})
	defer s.Close()
	api := httptest.NewServer(s.Handler("tok"))
	defer api.Close()

	// Markdown, hidden content stripped
	code, m := restCall(t, api.URL, "/v1/fetch", "tok", `{"url":"`+site.URL+`","ai_targeted":true}`)
	if code != 200 || !strings.Contains(m["content"].(string), "# Hello") || strings.Contains(m["content"].(string), "IGNORE") {
		t.Fatalf("fetch: %d %v", code, m)
	}
	// CSS results as a list
	code, m = restCall(t, api.URL, "/v1/fetch", "tok", `{"url":"`+site.URL+`","css":"h1::text"}`)
	if r, _ := m["results"].([]any); code != 200 || len(r) != 1 || r[0] != "Hello" {
		t.Fatalf("css: %d %v", code, m)
	}
	// crawl
	code, m = restCall(t, api.URL, "/v1/crawl", "tok", `{"start_url":"`+site.URL+`","fields":{"title":"h1::text"},"max_pages":3}`)
	if items, _ := m["items"].([]any); code != 200 || len(items) == 0 {
		t.Fatalf("crawl: %d %v", code, m)
	}
	// errors
	for _, tc := range []struct {
		path, body string
		want       int
	}{
		{"/v1/fetch", `{}`, 400},
		{"/v1/fetch", `{"url":"file:///etc/hosts"}`, 400},
		{"/v1/fetch", `{"url":"` + site.URL + `","bogus":1}`, 400},
		{"/v1/fetch", `not json`, 400},
		{"/v1/crawl", `{"start_url":"` + site.URL + `"}`, 400},
	} {
		if code, _ := restCall(t, api.URL, tc.path, "tok", tc.body); code != tc.want {
			t.Errorf("%s %s: got %d want %d", tc.path, tc.body, code, tc.want)
		}
	}
	if code, _ := restCall(t, api.URL, "/v1/fetch", "", `{"url":"`+site.URL+`"}`); code != 401 {
		t.Errorf("no token: %d", code)
	}
	// openapi is served and valid JSON
	req, _ := http.NewRequest(http.MethodGet, api.URL+"/v1/openapi.json", nil)
	req.Header.Set("Authorization", "Bearer tok")
	resp, _ := http.DefaultClient.Do(req)
	var doc map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil || doc["openapi"] == nil {
		t.Fatalf("openapi: %v", err)
	}
}

func TestRESTBlocksPrivateByDefault(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "internal") }))
	defer site.Close()
	s := New(Options{}) // guard on
	defer s.Close()
	api := httptest.NewServer(s.Handler("tok"))
	defer api.Close()
	code, m := restCall(t, api.URL, "/v1/fetch", "tok", `{"url":"`+site.URL+`"}`)
	e, _ := m["error"].(map[string]any)
	if code != 403 || e["code"] != "target_not_allowed" {
		t.Fatalf("want 403 target_not_allowed, got %d %v", code, m)
	}
}
