package fetcher

import (
	"context"
	"testing"
	"time"
)

// Live tests hit the public internet; skipped with -short.

func TestDoHResolvesAndFetches(t *testing.T) {
	if testing.Short() {
		t.Skip("network")
	}
	r := newDoHResolver("cloudflare")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ips, err := r.Lookup(ctx, "example.com")
	if err != nil || len(ips) == 0 {
		t.Fatalf("lookup: %v %v", ips, err)
	}
	f := New(Options{DoH: "cloudflare", Retries: -1})
	defer f.Close()
	resp, err := f.Get(ctx, "https://example.com")
	if err != nil || resp.Status != 200 {
		t.Fatalf("fetch via DoH: %v %v", resp, err)
	}
}

func TestHTTP3Negotiation(t *testing.T) {
	if testing.Short() {
		t.Skip("network")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	f := New(Options{Profile: "chrome", Retries: -1, HTTP3: true})
	defer f.Close()
	var protos []string
	for i := 0; i < 3; i++ {
		resp, err := f.Get(ctx, "https://cloudflare-quic.com/")
		if err != nil {
			t.Fatal(err)
		}
		protos = append(protos, resp.Proto)
	}
	t.Logf("protocols seen: %v", protos)
	f2 := New(Options{Profile: "chrome", Retries: -1, DisableHTTP3: true})
	defer f2.Close()
	resp, err := f2.Get(ctx, "https://cloudflare-quic.com/")
	if err != nil {
		t.Fatal(err)
	}
	if resp.Proto == "HTTP/3.0" {
		t.Fatalf("DisableHTTP3 still used %s", resp.Proto)
	}
}
