// Package fetcher is an HTTP client that looks like a real browser on the
// wire: TLS (JA3/JA4), HTTP/2 settings, header order and header values all
// come from one consistent browser profile. It retries, rotates proxies and
// profiles, and detects anti-bot blocks.
package fetcher

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Chief-spartan-117/gocrapling/internal/netguard"
	"io"
	"math"
	"math/rand/v2"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	http "github.com/bogdanfinn/fhttp"
	tls "github.com/bogdanfinn/tls-client"

	"github.com/Chief-spartan-117/gocrapling/pkg/parser"
)

// Options configures a Fetcher. The zero value is usable.
type Options struct {
	// Profile: "chrome", "firefox", "safari", "random" or an exact name.
	// Empty means a random Chrome profile.
	Profile string
	// Timeout per attempt. Default 30s.
	Timeout time.Duration
	// Retries after the first attempt. Default 3; set -1 for none.
	Retries int
	// Backoff base for exponential retry delay. Default 1s.
	Backoff time.Duration
	// Proxies rotates proxies per attempt. Nil means direct.
	Proxies ProxyRotator
	// NoRedirect disables following redirects.
	NoRedirect bool
	// SpoofReferer sends a Google-search Referer for the target domain, the
	// way a visitor arriving from search would.
	SpoofReferer bool
	// Headers are extra/override headers applied to every request.
	Headers map[string]string
	// InsecureSkipVerify disables certificate checks.
	InsecureSkipVerify bool
	// BlockPrivate refuses loopback, private, link-local and cloud-metadata
	// addresses and every scheme except http and https. Direct connections
	// are checked at connect time (so redirects and DNS rebinding are
	// covered); proxied requests are checked on the initial URL only, because
	// the proxy resolves later hops. Turn it on whenever URLs come from an
	// untrusted source, such as an AI agent.
	BlockPrivate bool
	// MaxBodyBytes caps the response body. Default 32 MiB.
	MaxBodyBytes int64
	// DoH routes DNS lookups for direct (non-proxied) connections through
	// DNS-over-HTTPS: "cloudflare", "google" or a JSON-API endpoint URL.
	// Proxied traffic is resolved by the proxy, so nothing leaks either way.
	DoH string
	// HTTP3 races QUIC against TCP like Chrome does and remembers the
	// winner per host. Off by default: many networks drop UDP.
	HTTP3 bool
	// DisableHTTP3 forces HTTP/2 or HTTP/1.1 (wins over HTTP3).
	DisableHTTP3 bool
	// PinIdentity keeps the browser identity fixed after a block instead of
	// rotating it. Set by NewSession.
	PinIdentity bool
	// RetryOn lists extra status codes that trigger a retry.
	RetryOn []int
}

// Request describes one fetch.
type Request struct {
	Method  string
	URL     string
	Params  url.Values
	Headers map[string]string
	Body    []byte
	// Referer overrides SpoofReferer for this request.
	Referer string
	// Proxy overrides the rotator for this request.
	Proxy string
}

// Response is a completed fetch.
type Response struct {
	Status  int
	Headers http.Header
	Body    []byte
	URL     string // final URL after redirects
	Proxy   string
	Profile string
	// Proto is the negotiated protocol, e.g. "HTTP/2.0" or "HTTP/3.0".
	Proto    string
	Attempts int
	Blocked  bool
	// Captured holds background XHR/fetch responses recorded by the stealth
	// browser (see stealth.FetchOptions.Record).
	Captured []Capture
	// Screenshot is a full-page PNG when requested (stealth only).
	Screenshot []byte
	// Cookies are the browser's cookies after load (stealth only).
	Cookies []*http.Cookie

	parseOpts []parser.Option
}

// Capture is one background network response seen while a page loaded.
type Capture struct {
	URL      string
	Method   string
	Status   int
	MimeType string
	Body     []byte
}

// SearchCaptured returns captured responses whose URL matches re.
func (r *Response) SearchCaptured(re *regexp.Regexp) []Capture {
	var out []Capture
	for _, c := range r.Captured {
		if re.MatchString(c.URL) {
			out = append(out, c)
		}
	}
	return out
}

// Text returns the body as a string.
func (r *Response) Text() string { return string(r.Body) }

// JSON decodes the body into v.
func (r *Response) JSON(v any) error {
	if err := json.Unmarshal(r.Body, v); err != nil {
		return fmt.Errorf("fetcher: decode json from %s: %w", r.URL, err)
	}
	return nil
}

// Parse parses the body as HTML.
func (r *Response) Parse(opts ...parser.Option) (*parser.Selector, error) {
	return parser.Parse(r.Body, append([]parser.Option{parser.WithURL(r.URL)}, append(r.parseOpts, opts...)...)...)
}

// Fetcher is safe for concurrent use.
type Fetcher struct {
	opt Options

	mu      sync.Mutex
	clients map[string]*session
	doh     *dohResolver
}

type session struct {
	client  tls.HttpClient
	profile Profile
}

// New creates a Fetcher.
func New(opt Options) *Fetcher {
	if opt.Timeout <= 0 {
		opt.Timeout = 30 * time.Second
	}
	switch {
	case opt.Retries == 0:
		opt.Retries = 3
	case opt.Retries < 0:
		opt.Retries = 0
	}
	if opt.Backoff <= 0 {
		opt.Backoff = time.Second
	}
	if opt.MaxBodyBytes <= 0 {
		opt.MaxBodyBytes = 32 << 20
	}
	f := &Fetcher{opt: opt, clients: map[string]*session{}}
	if opt.DoH != "" {
		f.doh = newDoHResolver(opt.DoH)
		f.doh.guard = opt.BlockPrivate
	}
	return f
}

// Close releases idle connections.
func (f *Fetcher) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.clients {
		s.client.CloseIdleConnections()
	}
}

// Get fetches a URL.
func (f *Fetcher) Get(ctx context.Context, rawURL string) (*Response, error) {
	return f.Do(ctx, Request{Method: http.MethodGet, URL: rawURL})
}

// Post sends a POST with the given content type.
func (f *Fetcher) Post(ctx context.Context, rawURL, contentType string, body []byte) (*Response, error) {
	return f.Do(ctx, Request{Method: http.MethodPost, URL: rawURL, Body: body,
		Headers: map[string]string{"content-type": contentType}})
}

// session returns the client bound to a proxy. Each proxy gets its own
// cookie jar and its own consistent browser identity, so a proxy exit never
// presents two different browsers.
func (f *Fetcher) session(proxy string, rotateIdentity bool) (*session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.clients[proxy]; ok && !rotateIdentity {
		return s, nil
	}
	if old, ok := f.clients[proxy]; ok {
		old.client.CloseIdleConnections()
	}
	p := pickProfile(f.opt.Profile)
	opts := []tls.HttpClientOption{
		tls.WithTimeoutSeconds(int(math.Ceil(f.opt.Timeout.Seconds()))),
		tls.WithClientProfile(p.TLS),
		tls.WithCookieJar(tls.NewCookieJar()),
		tls.WithRandomTLSExtensionOrder(),
		tls.WithCatchPanics(),
	}
	if f.opt.NoRedirect {
		opts = append(opts, tls.WithNotFollowRedirects())
	}
	if f.opt.InsecureSkipVerify {
		opts = append(opts, tls.WithInsecureSkipVerify())
	}
	switch {
	case f.opt.DisableHTTP3:
		opts = append(opts, tls.WithDisableHttp3())
	case f.opt.HTTP3:
		opts = append(opts, tls.WithProtocolRacing())
	}
	if proxy != "" {
		opts = append(opts, tls.WithProxyUrl(proxy))
	} else if f.opt.BlockPrivate && f.doh == nil {
		opts = append(opts, tls.WithDialContext(netguard.DialContext))
	} else if f.doh != nil {
		// WithDialContext replaces built-in proxy handling, so it is only
		// used for direct connections.
		opts = append(opts, tls.WithDialContext(f.doh.DialContext))
	}
	c, err := tls.NewHttpClient(tls.NewNoopLogger(), opts...)
	if err != nil {
		return nil, fmt.Errorf("fetcher: new client: %w", err)
	}
	s := &session{client: c, profile: p}
	f.clients[proxy] = s
	return s, nil
}

// Do executes a request with retries, proxy/profile rotation and block
// detection. On persistent blocking it returns the last response together
// with ErrBlocked, so callers can still inspect it.
func (f *Fetcher) Do(ctx context.Context, req Request) (*Response, error) {
	if req.Method == "" {
		req.Method = http.MethodGet
	}
	target, err := url.Parse(req.URL)
	if err != nil || target.Host == "" {
		return nil, fmt.Errorf("fetcher: invalid url %q", req.URL)
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return nil, fmt.Errorf("fetcher: scheme %q not allowed (http and https only)", target.Scheme)
	}
	if f.opt.BlockPrivate {
		if err := netguard.CheckURL(ctx, req.URL); err != nil {
			return nil, err
		}
	}
	if len(req.Params) > 0 {
		q := target.Query()
		for k, vs := range req.Params {
			for _, v := range vs {
				q.Add(k, v)
			}
		}
		target.RawQuery = q.Encode()
	}

	var (
		last    *Response
		lastErr error
	)
	rotate := false
	for attempt := 0; attempt <= f.opt.Retries; attempt++ {
		if attempt > 0 {
			if err := sleep(ctx, f.backoff(attempt, last)); err != nil {
				return last, err
			}
		}
		proxy := req.Proxy
		if proxy == "" && f.opt.Proxies != nil {
			proxy = f.opt.Proxies.Next()
		}
		resp, err := f.once(ctx, req, target, proxy, rotate)
		if resp != nil {
			resp.Attempts = attempt + 1
		}
		if err != nil {
			lastErr = err
			last = resp
			f.markBad(proxy)
			if ctx.Err() != nil {
				return last, ctx.Err()
			}
			continue
		}
		last, lastErr = resp, nil
		if resp.Blocked {
			f.markBad(proxy)
			rotate = !f.opt.PinIdentity // new fingerprint for the next try
			lastErr = ErrBlocked
			continue
		}
		if f.retryable(resp.Status) {
			lastErr = fmt.Errorf("fetcher: status %d", resp.Status)
			continue
		}
		if f.opt.Proxies != nil && proxy != "" {
			f.opt.Proxies.MarkGood(proxy)
		}
		return resp, nil
	}
	if lastErr == nil {
		lastErr = errors.New("fetcher: exhausted retries")
	}
	return last, lastErr
}

func (f *Fetcher) markBad(proxy string) {
	if f.opt.Proxies != nil && proxy != "" {
		f.opt.Proxies.MarkBad(proxy)
	}
}

func (f *Fetcher) retryable(status int) bool {
	switch status {
	case 500, 502, 504:
		return true
	}
	for _, s := range f.opt.RetryOn {
		if s == status {
			return true
		}
	}
	return false
}

func (f *Fetcher) backoff(attempt int, last *Response) time.Duration {
	if last != nil {
		if ra := last.Headers.Get("Retry-After"); ra != "" {
			if secs, err := strconv.Atoi(ra); err == nil && secs >= 0 && secs <= 120 {
				return time.Duration(secs) * time.Second
			}
		}
	}
	d := f.opt.Backoff * time.Duration(1<<min(attempt-1, 6))
	jitter := time.Duration(rand.Int64N(int64(d/2) + 1))
	return d + jitter
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (f *Fetcher) once(ctx context.Context, r Request, target *url.URL, proxy string, rotate bool) (*Response, error) {
	s, err := f.session(proxy, rotate)
	if err != nil {
		return nil, err
	}
	var body io.Reader
	if len(r.Body) > 0 {
		body = bytes.NewReader(r.Body)
	}
	hr, err := http.NewRequestWithContext(ctx, r.Method, target.String(), body)
	if err != nil {
		return nil, err
	}

	h := s.profile.clone()
	referer := r.Referer
	if referer == "" && f.opt.SpoofReferer {
		referer = "https://www.google.com/search?q=" + url.QueryEscape(strings.TrimPrefix(target.Hostname(), "www."))
	}
	if referer != "" {
		h.Set("referer", referer)
		if s.profile.Chromium || h.Get("sec-fetch-site") != "" {
			h.Set("sec-fetch-site", "cross-site")
		}
	}
	for k, v := range f.opt.Headers {
		h.Set(k, v)
	}
	for k, v := range r.Headers {
		h.Set(k, v)
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		// Subresource-style request, not a navigation.
		h.Del("upgrade-insecure-requests")
		h.Del("sec-fetch-user")
		h.Set("sec-fetch-mode", "cors")
		h.Set("sec-fetch-dest", "empty")
		h.Set("accept", "*/*")
	}
	order := make([]string, 0, len(s.profile.Order)+len(h))
	seen := map[string]bool{}
	for _, k := range s.profile.Order {
		if _, ok := h[k]; ok || k == "cookie" {
			order = append(order, k)
			seen[k] = true
		}
	}
	for k := range h {
		if !seen[k] {
			order = append(order, k)
		}
	}
	h[http.HeaderOrderKey] = order
	hr.Header = h

	resp, err := s.client.Do(hr)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, f.opt.MaxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("fetcher: read body: %w", err)
	}
	final := target.String()
	if resp.Request != nil && resp.Request.URL != nil {
		final = resp.Request.URL.String()
	}
	out := &Response{
		Status:  resp.StatusCode,
		Headers: resp.Header,
		Body:    data,
		URL:     final,
		Proxy:   proxy,
		Profile: s.profile.Name,
		Proto:   resp.Proto,
	}
	out.Blocked = IsBlocked(out.Status, out.Body)
	return out, nil
}
