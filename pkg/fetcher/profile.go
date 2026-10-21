package fetcher

import (
	"math/rand/v2"
	"strings"
	"sync"

	http "github.com/bogdanfinn/fhttp"
	"github.com/bogdanfinn/tls-client/profiles"
)

// Profile ties a TLS/HTTP2 fingerprint to the headers a real browser with
// that fingerprint would send. UA and TLS must stay consistent, otherwise
// anti-bot systems flag the mismatch.
type Profile struct {
	Name    string
	TLS     profiles.ClientProfile
	Headers http.Header
	// Order is the header order on the wire (lower-case names).
	Order []string
	// Chromium-family browsers send Sec-Fetch-* and client hints.
	Chromium bool
}

func chromeProfile(platform, osUA string) Profile {
	return Profile{
		Name:     "chrome_133_" + strings.ToLower(platform),
		TLS:      profiles.Chrome_133,
		Chromium: true,
		Headers: http.Header{
			"sec-ch-ua":                 {`"Not(A:Brand";v="99", "Google Chrome";v="133", "Chromium";v="133"`},
			"sec-ch-ua-mobile":          {"?0"},
			"sec-ch-ua-platform":        {`"` + platform + `"`},
			"upgrade-insecure-requests": {"1"},
			"user-agent":                {"Mozilla/5.0 (" + osUA + ") AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36"},
			"accept":                    {"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"},
			"sec-fetch-site":            {"none"},
			"sec-fetch-mode":            {"navigate"},
			"sec-fetch-user":            {"?1"},
			"sec-fetch-dest":            {"document"},
			"accept-encoding":           {"gzip, deflate, br, zstd"},
			"accept-language":           {"en-US,en;q=0.9"},
			"priority":                  {"u=0, i"},
		},
		Order: []string{
			"sec-ch-ua", "sec-ch-ua-mobile", "sec-ch-ua-platform", "upgrade-insecure-requests",
			"user-agent", "accept", "sec-fetch-site", "sec-fetch-mode", "sec-fetch-user",
			"sec-fetch-dest", "referer", "accept-encoding", "accept-language", "cookie", "priority",
		},
	}
}

func firefoxProfile(osUA string) Profile {
	return Profile{
		Name: "firefox_135",
		TLS:  profiles.Firefox_135,
		Headers: http.Header{
			"user-agent":                {"Mozilla/5.0 (" + osUA + "; rv:135.0) Gecko/20100101 Firefox/135.0"},
			"accept":                    {"text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"},
			"accept-language":           {"en-US,en;q=0.5"},
			"accept-encoding":           {"gzip, deflate, br, zstd"},
			"upgrade-insecure-requests": {"1"},
			"sec-fetch-dest":            {"document"},
			"sec-fetch-mode":            {"navigate"},
			"sec-fetch-site":            {"none"},
			"sec-fetch-user":            {"?1"},
			"priority":                  {"u=0, i"},
		},
		Order: []string{
			"user-agent", "accept", "accept-language", "accept-encoding", "referer", "cookie",
			"upgrade-insecure-requests", "sec-fetch-dest", "sec-fetch-mode", "sec-fetch-site",
			"sec-fetch-user", "priority",
		},
	}
}

func safariProfile() Profile {
	return Profile{
		Name: "safari_16",
		TLS:  profiles.Safari_16_0,
		Headers: http.Header{
			"accept":          {"text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"},
			"sec-fetch-site":  {"none"},
			"sec-fetch-mode":  {"navigate"},
			"user-agent":      {"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.0 Safari/605.1.15"},
			"accept-language": {"en-US,en;q=0.9"},
			"sec-fetch-dest":  {"document"},
			"accept-encoding": {"gzip, deflate, br"},
		},
		Order: []string{
			"accept", "sec-fetch-site", "sec-fetch-mode", "user-agent", "referer",
			"accept-language", "sec-fetch-dest", "accept-encoding", "cookie",
		},
	}
}

var builtin = []Profile{
	chromeProfile("Windows", "Windows NT 10.0; Win64; x64"),
	chromeProfile("macOS", "Macintosh; Intel Mac OS X 10_15_7"),
	chromeProfile("Linux", "X11; Linux x86_64"),
	firefoxProfile("Windows NT 10.0; Win64; x64"),
	firefoxProfile("Macintosh; Intel Mac OS X 10.15"),
	safariProfile(),
}

var (
	regMu  sync.RWMutex
	custom []Profile
)

// RegisterProfile adds a profile to the rotation, so identities can be
// refreshed without forking. Its UA must agree with its TLS fingerprint.
func RegisterProfile(p Profile) {
	regMu.Lock()
	custom = append(custom, p)
	regMu.Unlock()
}

func allProfiles() []Profile {
	regMu.RLock()
	defer regMu.RUnlock()
	return append(append([]Profile(nil), builtin...), custom...)
}

// Profiles returns a copy of the built-in profile list.
func Profiles() []Profile { return allProfiles() }

// pickProfile resolves a family name ("chrome", "firefox", "safari",
// "random", or an exact profile name) to a Profile. Unknown names fall back
// to Chrome.
func pickProfile(name string) Profile {
	name = strings.ToLower(strings.TrimSpace(name))
	var pool []Profile
	for _, p := range allProfiles() {
		if name == "" || name == "random" || p.Name == name || strings.HasPrefix(p.Name, name) {
			pool = append(pool, p)
		}
	}
	if len(pool) == 0 {
		pool = builtin[:3]
	}
	if name == "" {
		pool = builtin[:3] // default: Chrome family, most common fingerprint
	}
	return pool[rand.IntN(len(pool))]
}

// clone returns a header copy safe to mutate per request.
func (p Profile) clone() http.Header {
	h := make(http.Header, len(p.Headers)+2)
	for k, v := range p.Headers {
		h[k] = append([]string(nil), v...)
	}
	return h
}
