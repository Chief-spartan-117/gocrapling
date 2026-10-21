package netguard

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestIsPublic(t *testing.T) {
	for ip, want := range map[string]bool{
		"8.8.8.8": true, "1.1.1.1": true, "2606:4700:4700::1111": true,
		"127.0.0.1": false, "10.1.2.3": false, "172.16.0.1": false, "172.32.0.1": true,
		"192.168.1.1": false, "169.254.169.254": false, "100.64.0.1": false, "0.0.0.0": false,
		"224.0.0.1": false, "255.255.255.255": false, "::1": false, "fe80::1": false, "fc00::1": false,
		"::ffff:127.0.0.1": false, "::ffff:8.8.8.8": true,
		"64:ff9b::7f00:1": false, "64:ff9b::808:808": true, "2002:7f00:1::": false,
	} {
		if got := IsPublic(netip.MustParseAddr(ip)); got != want {
			t.Errorf("IsPublic(%s)=%v want %v", ip, got, want)
		}
	}
}

func TestCheckURL(t *testing.T) {
	ctx := context.Background()
	for _, bad := range []string{
		"file:///etc/passwd", "javascript:alert(1)", "chrome://version", "ftp://example.com/",
		"http://127.0.0.1/", "http://[::1]:8080/", "http://169.254.169.254/latest/meta-data/",
		"http://localhost/", "http://10.0.0.1/", "http:///nohost",
	} {
		if err := CheckURL(ctx, bad); err == nil {
			t.Errorf("CheckURL(%q) allowed", bad)
		}
	}
	if err := CheckURL(ctx, "http://8.8.8.8/"); err != nil {
		t.Errorf("public IP refused: %v", err)
	}
}

func TestDialerBlocksLoopbackAtConnect(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	c := &http.Client{Transport: &http.Transport{DialContext: DialContext}}
	_, err := c.Get(srv.URL)
	if err == nil || !errors.Is(err, ErrBlocked) {
		t.Fatalf("want ErrBlocked, got %v", err)
	}
}
