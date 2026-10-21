package fetcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Chief-spartan-117/gocrapling/internal/netguard"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Well-known DNS-over-HTTPS endpoints for Options.DoH.
const (
	DoHCloudflare = "https://1.1.1.1/dns-query"
	DoHGoogle     = "https://8.8.8.8/resolve"
)

// dohResolver resolves names through a DNS-over-HTTPS JSON API so lookups
// never go to the system resolver (no DNS leak, no ISP view of targets).
type dohResolver struct {
	endpoint string
	hc       *http.Client
	// guard refuses to connect to non-public addresses.
	guard bool

	mu    sync.Mutex
	cache map[string]dohEntry
}

type dohEntry struct {
	ips     []net.IP
	expires time.Time
}

func newDoHResolver(endpoint string) *dohResolver {
	if endpoint == "" || endpoint == "cloudflare" {
		endpoint = DoHCloudflare
	} else if endpoint == "google" {
		endpoint = DoHGoogle
	}
	return &dohResolver{
		endpoint: endpoint,
		hc:       &http.Client{Timeout: 10 * time.Second},
		cache:    map[string]dohEntry{},
	}
}

type dohAnswer struct {
	Status int `json:"Status"`
	Answer []struct {
		Type int    `json:"type"`
		TTL  int    `json:"TTL"`
		Data string `json:"data"`
	} `json:"Answer"`
}

func (r *dohResolver) query(ctx context.Context, name, qtype string) ([]net.IP, time.Duration, error) {
	u, err := url.Parse(r.endpoint)
	if err != nil {
		return nil, 0, err
	}
	q := u.Query()
	q.Set("name", name)
	q.Set("type", qtype)
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/dns-json")
	resp, err := r.hc.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, 0, fmt.Errorf("doh: status %d", resp.StatusCode)
	}
	var ans dohAnswer
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&ans); err != nil {
		return nil, 0, err
	}
	var ips []net.IP
	ttl := 300
	for _, a := range ans.Answer {
		// types 1 (A) and 28 (AAAA); CNAME answers are skipped
		if a.Type != 1 && a.Type != 28 {
			continue
		}
		if ip := net.ParseIP(strings.TrimSpace(a.Data)); ip != nil {
			ips = append(ips, ip)
			if a.TTL > 0 && a.TTL < ttl {
				ttl = a.TTL
			}
		}
	}
	return ips, time.Duration(ttl) * time.Second, nil
}

// Lookup returns the addresses of host, IPv4 first.
func (r *dohResolver) Lookup(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	r.mu.Lock()
	if e, ok := r.cache[host]; ok && time.Now().Before(e.expires) {
		r.mu.Unlock()
		return e.ips, nil
	}
	r.mu.Unlock()

	v4, ttl4, err4 := r.query(ctx, host, "A")
	v6, ttl6, err6 := r.query(ctx, host, "AAAA")
	ips := append(v4, v6...)
	if len(ips) == 0 {
		if err := errors.Join(err4, err6); err != nil {
			return nil, fmt.Errorf("doh: resolve %s: %w", host, err)
		}
		return nil, fmt.Errorf("doh: no addresses for %s", host)
	}
	ttl := ttl4
	if len(v4) == 0 || (len(v6) > 0 && ttl6 < ttl) {
		ttl = ttl6
	}
	r.mu.Lock()
	r.cache[host] = dohEntry{ips: ips, expires: time.Now().Add(ttl)}
	r.mu.Unlock()
	return ips, nil
}

// DialContext resolves via DoH and dials the first address that answers.
func (r *dohResolver) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := r.Lookup(ctx, host)
	if err != nil {
		return nil, err
	}
	var d net.Dialer
	if r.guard {
		d = *netguard.Dialer()
	}
	var last error
	for _, ip := range ips {
		c, err := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return c, nil
		}
		last = err
	}
	return nil, last
}
