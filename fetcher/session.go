package fetcher

import (
	"context"
	"fmt"
	"net/url"

	http "github.com/bogdanfinn/fhttp"
)

// Session is a pinned browser identity: one TLS/UA profile, one proxy and
// one cookie jar for its whole life. Use it for logins and multi-step
// flows, where changing fingerprint or IP mid-session gets you flagged.
// Safe for concurrent use.
type Session struct {
	f     *Fetcher
	proxy string
}

// NewSession creates a session from opt. Profile (family or exact name) is
// resolved once and kept; proxy is taken once from opt.Proxies (or direct).
// Retries still apply, but a block does not change identity or proxy.
func NewSession(opt Options) (*Session, error) {
	proxy := ""
	if opt.Proxies != nil {
		proxy = opt.Proxies.Next()
	}
	opt.Proxies = nil
	opt.PinIdentity = true
	f := New(opt)
	s, err := f.session(proxy, false) // creates and pins the profile
	if err != nil {
		return nil, err
	}
	f.opt.Profile = s.profile.Name
	return &Session{f: f, proxy: proxy}, nil
}

// Profile returns the pinned profile name.
func (s *Session) Profile() string { return s.f.opt.Profile }

// UserAgent returns the pinned User-Agent.
func (s *Session) UserAgent() string {
	sess, _ := s.f.session(s.proxy, false)
	return sess.profile.Headers["user-agent"][0] // keys are lower-case
}

// Do runs a request inside the session.
func (s *Session) Do(ctx context.Context, req Request) (*Response, error) {
	req.Proxy = s.proxy
	return s.f.Do(ctx, req)
}

func (s *Session) Get(ctx context.Context, rawURL string) (*Response, error) {
	return s.Do(ctx, Request{Method: http.MethodGet, URL: rawURL})
}

func (s *Session) Post(ctx context.Context, rawURL, contentType string, body []byte) (*Response, error) {
	return s.Do(ctx, Request{Method: http.MethodPost, URL: rawURL, Body: body,
		Headers: map[string]string{"content-type": contentType}})
}

// Cookies returns the jar's cookies for rawURL.
func (s *Session) Cookies(rawURL string) ([]*http.Cookie, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	sess, err := s.f.session(s.proxy, false)
	if err != nil {
		return nil, err
	}
	return sess.client.GetCookies(u), nil
}

// SetCookies adds cookies for rawURL, for example ones a browser obtained
// after solving a challenge. Cloudflare's clearance cookie is bound to the
// User-Agent (and often the IP), so pair it with Browser UserAgent and the
// same proxy.
func (s *Session) SetCookies(rawURL string, cookies []*http.Cookie) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("fetcher: %w", err)
	}
	sess, err := s.f.session(s.proxy, false)
	if err != nil {
		return err
	}
	sess.client.SetCookies(u, cookies)
	return nil
}

// Close releases connections.
func (s *Session) Close() { s.f.Close() }
