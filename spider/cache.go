package spider

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	http "github.com/bogdanfinn/fhttp"

	"github.com/sagarshrestha/gocrapling/fetcher"
)

// CachingDownloader stores successful responses on disk and replays them,
// so callbacks can be developed offline without hitting the site again
// (Scrapling's "development mode"). Blocked and failed responses are never
// cached.
type CachingDownloader struct {
	Next Downloader
	Dir  string
}

// NewCachingDownloader wraps next, caching under dir.
func NewCachingDownloader(next Downloader, dir string) (*CachingDownloader, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &CachingDownloader{Next: next, Dir: dir}, nil
}

type cacheEntry struct {
	Status  int                 `json:"status"`
	URL     string              `json:"url"`
	Headers map[string][]string `json:"headers"`
	Body    []byte              `json:"body"`
	Profile string              `json:"profile"`
}

func (c *CachingDownloader) path(r fetcher.Request) string {
	h := sha1.New()
	h.Write([]byte(r.Method + "\n" + canonicalURL(r.URL) + "\n"))
	h.Write(r.Body)
	return filepath.Join(c.Dir, hex.EncodeToString(h.Sum(nil))+".json")
}

func (c *CachingDownloader) Do(ctx context.Context, r fetcher.Request) (*fetcher.Response, error) {
	if r.Method == "" {
		r.Method = http.MethodGet
	}
	p := c.path(r)
	if b, err := os.ReadFile(p); err == nil {
		var e cacheEntry
		if json.Unmarshal(b, &e) == nil {
			return &fetcher.Response{
				Status: e.Status, URL: e.URL, Headers: http.Header(e.Headers),
				Body: e.Body, Profile: e.Profile + " (cached)", Attempts: 0,
			}, nil
		}
	}
	resp, err := c.Next.Do(ctx, r)
	if err != nil || resp == nil || resp.Blocked || resp.Status < 200 || resp.Status >= 300 {
		return resp, err
	}
	b, merr := json.Marshal(cacheEntry{Status: resp.Status, URL: resp.URL,
		Headers: map[string][]string(resp.Headers), Body: resp.Body, Profile: resp.Profile})
	if merr == nil {
		tmp := p + ".tmp"
		if werr := os.WriteFile(tmp, b, 0o600); werr == nil {
			if rerr := os.Rename(tmp, p); rerr != nil {
				err = errors.Join(err, rerr)
			}
		}
	}
	return resp, err
}
