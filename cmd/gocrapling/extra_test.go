package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReorderArgs(t *testing.T) {
	got := reorderArgs([]string{"http://x", "out.md", "-css", "main", "-ai-targeted"})
	want := []string{"-css", "main", "-ai-targeted", "http://x", "out.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestIsLoopbackAddr(t *testing.T) {
	for addr, want := range map[string]bool{"127.0.0.1:80": true, "localhost:80": true, "[::1]:80": true, ":8080": false, "0.0.0.0:80": false, "example.com:80": false} {
		if isLoopbackAddr(addr) != want {
			t.Errorf("%s want %v", addr, want)
		}
	}
}

func TestExtractFormats(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><body><h1>Title</h1><div id="a">keep</div><p style="display:none">HIDDEN</p></body></html>`)
	}))
	defer srv.Close()
	dir := t.TempDir()
	cases := []struct {
		file string
		args []string
		has  []string
		not  []string
	}{
		{"p.md", nil, []string{"# Title"}, nil},
		{"p.md", []string{"-ai-targeted"}, []string{"keep"}, []string{"HIDDEN"}},
		{"p.txt", []string{"-css", "#a"}, []string{"keep"}, []string{"Title"}},
		{"p.html", []string{"-css", "#a"}, []string{`<div id="a">`}, []string{"Title"}},
	}
	for _, c := range cases {
		out := filepath.Join(dir, c.file)
		args := append([]string{srv.URL, out}, c.args...)
		if err := cmdExtract(context.Background(), args); err != nil {
			t.Fatalf("%v: %v", c, err)
		}
		b, _ := os.ReadFile(out)
		for _, h := range c.has {
			if !strings.Contains(string(b), h) {
				t.Errorf("%s %v: missing %q in %q", c.file, c.args, h, b)
			}
		}
		for _, n := range c.not {
			if strings.Contains(string(b), n) {
				t.Errorf("%s %v: unexpected %q in %q", c.file, c.args, n, b)
			}
		}
	}
	if err := cmdExtract(context.Background(), []string{srv.URL, filepath.Join(dir, "p.pdf")}); err == nil {
		t.Fatal("pdf extension accepted")
	}
}
