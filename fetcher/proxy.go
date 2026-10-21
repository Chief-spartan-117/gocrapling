package fetcher

import (
	"math/rand/v2"
	"sync"
	"time"
)

// ProxyRotator hands out proxy URLs and learns which ones are failing.
// An empty string from Next means "connect directly".
type ProxyRotator interface {
	Next() string
	MarkBad(proxy string)
	MarkGood(proxy string)
}

type proxyState struct {
	url      string
	failures int
	until    time.Time // cooldown end
}

// PoolRotator rotates round-robin or randomly over a proxy list. A proxy
// that fails MaxFailures times in a row is benched for Cooldown.
type PoolRotator struct {
	Random      bool
	MaxFailures int
	Cooldown    time.Duration

	mu      sync.Mutex
	proxies []*proxyState
	idx     int
}

// NewPoolRotator builds a rotator. Pass random=true for random selection.
func NewPoolRotator(urls []string, random bool) *PoolRotator {
	r := &PoolRotator{Random: random, MaxFailures: 3, Cooldown: 5 * time.Minute}
	for _, u := range urls {
		r.proxies = append(r.proxies, &proxyState{url: u})
	}
	return r
}

func (r *PoolRotator) Next() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := len(r.proxies)
	if n == 0 {
		return ""
	}
	now := time.Now()
	start := r.idx
	if r.Random {
		start = rand.IntN(n)
	}
	for i := 0; i < n; i++ {
		p := r.proxies[(start+i)%n]
		if now.After(p.until) {
			r.idx = (start + i + 1) % n
			return p.url
		}
	}
	// Everything benched: use the one whose cooldown ends first.
	best := r.proxies[0]
	for _, p := range r.proxies {
		if p.until.Before(best.until) {
			best = p
		}
	}
	return best.url
}

func (r *PoolRotator) MarkBad(proxy string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.proxies {
		if p.url == proxy {
			p.failures++
			if p.failures >= r.MaxFailures {
				p.until = time.Now().Add(r.Cooldown)
				p.failures = 0
			}
		}
	}
}

func (r *PoolRotator) MarkGood(proxy string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.proxies {
		if p.url == proxy {
			p.failures = 0
		}
	}
}
