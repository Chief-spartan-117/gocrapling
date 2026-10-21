package stealth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/storage"
	"github.com/chromedp/chromedp"
)

// savedCookie is the on-disk form of one browser cookie.
type savedCookie struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Expires  float64 `json:"expires,omitempty"` // unix seconds; 0 = session cookie
	HTTPOnly bool    `json:"httpOnly,omitempty"`
	Secure   bool    `json:"secure,omitempty"`
	SameSite string  `json:"sameSite,omitempty"`
}

type stateFile struct {
	Version int           `json:"version"`
	Cookies []savedCookie `json:"cookies"`
}

// SaveState writes every cookie of the browser to path (mode 0600, since
// it holds login sessions). Pair it with LoadState to reuse a login.
func (b *Browser) SaveState(ctx context.Context, path string) error {
	var cookies []*network.Cookie
	err := b.runBrowser(ctx, chromedp.ActionFunc(func(c context.Context) error {
		var err error
		cookies, err = storage.GetCookies().Do(c)
		return err
	}))
	if err != nil {
		return fmt.Errorf("stealth: read cookies: %w", err)
	}
	st := stateFile{Version: 1}
	for _, c := range cookies {
		sc := savedCookie{Name: c.Name, Value: c.Value, Domain: c.Domain, Path: c.Path,
			HTTPOnly: c.HTTPOnly, Secure: c.Secure, SameSite: string(c.SameSite)}
		if !c.Session && c.Expires > 0 {
			sc.Expires = c.Expires
		}
		st.Cookies = append(st.Cookies, sc)
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadState restores cookies saved by SaveState. A missing file is not an
// error, so a first run can start with no state. Expired cookies are skipped.
func (b *Browser) LoadState(ctx context.Context, path string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var st stateFile
	if err := json.Unmarshal(data, &st); err != nil {
		return fmt.Errorf("stealth: bad state file %s: %w", path, err)
	}
	now := float64(time.Now().Unix())
	var params []*network.CookieParam
	for _, c := range st.Cookies {
		if c.Expires > 0 && c.Expires < now {
			continue
		}
		p := &network.CookieParam{Name: c.Name, Value: c.Value, Domain: c.Domain, Path: c.Path,
			HTTPOnly: c.HTTPOnly, Secure: c.Secure}
		if c.SameSite != "" {
			p.SameSite = network.CookieSameSite(c.SameSite)
		}
		if c.Expires > 0 {
			t := cdp.TimeSinceEpoch(time.Unix(int64(c.Expires), 0))
			p.Expires = &t
		}
		params = append(params, p)
	}
	if len(params) == 0 {
		return nil
	}
	return b.runBrowser(ctx, chromedp.ActionFunc(func(c context.Context) error {
		return storage.SetCookies(params).Do(c)
	}))
}

// runBrowser runs an action against the browser's base context.
func (b *Browser) runBrowser(ctx context.Context, a chromedp.Action) error {
	tab, cancel := chromedp.NewContext(b.ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	return chromedp.Run(tab, a)
}
