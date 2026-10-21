// Package stealth drives a real Chrome via the DevTools protocol with
// automation tells removed. Use it for pages that need JavaScript or that
// block plain HTTP clients even with a perfect TLS fingerprint.
package stealth

import (
	"context"
	"errors"
	"fmt"
	"github.com/sagarshrestha/gocrapling/netguard"
	"math/rand/v2"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"

	http "github.com/bogdanfinn/fhttp"

	"github.com/sagarshrestha/gocrapling/browserinstall"
	"github.com/sagarshrestha/gocrapling/fetcher"
)

// Options configures a Browser.
type Options struct {
	// RemoteURL attaches to an already running Chrome instead of
	// launching one, e.g. "http://127.0.0.1:9222" or a ws:// DevTools URL
	// (started with --remote-debugging-port). Launch flags, Headed,
	// UserDataDir and ExecPath are then ignored. Close detaches without
	// quitting the remote browser.
	RemoteURL string
	// ExecPath of the Chrome/Chromium binary; auto-detected when empty.
	ExecPath string
	// Headed shows the window. Headed Chrome is harder to detect.
	Headed bool
	// Proxy like http://user:pass@host:port or socks5://host:port.
	Proxy string
	// UserDataDir keeps cookies/profile between runs. Temp dir when empty.
	UserDataDir string
	Locale      string // default en-US
	Timezone    string // IANA name, e.g. America/New_York; empty keeps system
	Width       int    // default 1366
	Height      int    // default 768
	// MaxTabs caps concurrent pages. Default 4.
	MaxTabs int
	// Timeout per fetch. Default 60s.
	Timeout time.Duration
	// ExtraFlags are raw Chrome flags without leading dashes, e.g. "disable-gpu".
	ExtraFlags []string
	// BlockPrivate refuses non-public targets (see netguard). Chrome
	// resolves and connects itself, so only the URL passed to Fetch is
	// checked, not redirects or sub-resources. Pair it with a network-level
	// egress policy when the URLs come from an untrusted source.
	BlockPrivate bool
	// BlockAds blocks a built-in list of ad, analytics and tracker hosts.
	BlockAds bool
	// BlockDomains blocks extra hosts and their subdomains.
	BlockDomains []string
	// RecordRequests captures background XHR/fetch responses on every
	// fetch (same as FetchOptions.Record).
	RecordRequests bool
	// DisableResources skips images, fonts and media for faster, lighter
	// loads. Pages that need them to render may break.
	DisableResources bool
	// NetworkIdle waits for the network to go quiet after load on every
	// fetch (same as FetchOptions.WaitIdle).
	NetworkIdle bool
	// SpoofReferer navigates with a Google-search Referer, like a visitor
	// arriving from search.
	SpoofReferer bool
	// StateFile loads cookies from this file at start and saves them on
	// Close, so logins persist between runs. See SaveState and LoadState.
	StateFile string
	// SolveChallenge waits for Cloudflare-style interstitials to clear.
	SolveChallenge bool
}

// FetchOptions tunes a single page load.
type FetchOptions struct {
	// WaitSelector blocks until the CSS selector exists in the DOM.
	WaitSelector string
	// Settle waits this long after load for late JS rendering.
	Settle time.Duration
	// Actions run after load, before the HTML is captured.
	Actions []chromedp.Action
	// Record captures background XHR/fetch responses into
	// Response.Captured. It waits for the API traffic to go quiet first.
	Record bool
	// CaptureFilter limits recording to URLs matching it.
	CaptureFilter *regexp.Regexp
	// WaitIdle waits for the network to go quiet after load.
	WaitIdle bool
	// Referer overrides Options.SpoofReferer for this fetch.
	Referer string
	// Screenshot stores a full-page PNG in Response.Screenshot.
	Screenshot bool
	// Humanize adds random mouse movement and scrolling.
	Humanize bool
}

// Browser is a long-lived Chrome instance. Safe for concurrent use.
type Browser struct {
	opt      Options
	allocCtx context.Context
	cancelA  context.CancelFunc
	ctx      context.Context
	cancelB  context.CancelFunc
	sem      chan struct{}
	ua       string
	proxy    proxyInfo
	once     sync.Once
}

type proxyInfo struct {
	server, user, pass string
}

// New launches Chrome.
func New(opt Options) (*Browser, error) {
	if opt.Locale == "" {
		opt.Locale = "en-US"
	}
	if opt.Width == 0 {
		opt.Width = 1366
	}
	if opt.Height == 0 {
		opt.Height = 768
	}
	if opt.MaxTabs <= 0 {
		opt.MaxTabs = 4
	}
	if opt.Timeout <= 0 {
		opt.Timeout = 60 * time.Second
	}

	flags := []chromedp.ExecAllocatorOption{
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		chromedp.Flag("headless", !opt.Headed),
		chromedp.Flag("hide-scrollbars", false),
		chromedp.Flag("mute-audio", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("enable-automation", false),
		chromedp.Flag("disable-infobars", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("lang", opt.Locale),
		chromedp.Flag("accept-lang", opt.Locale+","+strings.SplitN(opt.Locale, "-", 2)[0]),
		chromedp.Flag("disable-features", "IsolateOrigins,site-per-process,TranslateUI"),
		chromedp.Flag("webrtc-ip-handling-policy", "disable_non_proxied_udp"),
		chromedp.Flag("force-webrtc-ip-handling-policy", true),
		chromedp.WindowSize(opt.Width, opt.Height),
	}
	if opt.ExecPath == "" {
		// A browser fetched by "gocrapling install" wins over whatever
		// chromedp would find, so machines without Chrome work.
		opt.ExecPath = browserinstall.Find()
	}
	if opt.ExecPath != "" {
		flags = append(flags, chromedp.ExecPath(opt.ExecPath))
	}
	// Chrome refuses to start its sandbox as root, which is the default
	// user inside containers.
	// GOCRAPLING_NO_SANDBOX=1 does the same for non-root containers that
	// cannot create user namespaces (the Docker image sets it).
	if os.Geteuid() == 0 || os.Getenv("GOCRAPLING_NO_SANDBOX") == "1" {
		flags = append(flags, chromedp.Flag("no-sandbox", true))
	}
	if opt.UserDataDir != "" {
		flags = append(flags, chromedp.UserDataDir(opt.UserDataDir))
	}
	if opt.DisableResources {
		flags = append(flags, chromedp.Flag("blink-settings", "imagesEnabled=false"))
	}
	b := &Browser{opt: opt, sem: make(chan struct{}, opt.MaxTabs)}
	if opt.Proxy != "" {
		pi, err := parseProxy(opt.Proxy)
		if err != nil {
			return nil, err
		}
		b.proxy = pi
		flags = append(flags, chromedp.ProxyServer(pi.server))
	}
	for _, f := range opt.ExtraFlags {
		k, v, hasV := strings.Cut(f, "=")
		if hasV {
			flags = append(flags, chromedp.Flag(k, v))
		} else {
			flags = append(flags, chromedp.Flag(k, true))
		}
	}

	if opt.RemoteURL != "" {
		b.allocCtx, b.cancelA = chromedp.NewRemoteAllocator(context.Background(), opt.RemoteURL)
	} else {
		b.allocCtx, b.cancelA = chromedp.NewExecAllocator(context.Background(), append(chromedp.DefaultExecAllocatorOptions[:0:0], flags...)...)
	}
	b.ctx, b.cancelB = chromedp.NewContext(b.allocCtx)

	// The first Run starts the browser, which then lives as long as the
	// context of that call, so it must not carry a timeout.
	startTimer := time.AfterFunc(30*time.Second, b.cancelB)
	defer startTimer.Stop()
	var ver string
	if err := chromedp.Run(b.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		_, _, _, ua, _, err := browser.GetVersion().Do(ctx)
		ver = ua
		return err
	})); err != nil {
		b.Close()
		return nil, fmt.Errorf("stealth: start chrome: %w", err)
	}
	// Headless Chrome announces itself in the UA string.
	b.ua = strings.ReplaceAll(ver, "HeadlessChrome", "Chrome")
	if opt.StateFile != "" {
		lctx, lcancel := context.WithTimeout(context.Background(), 15*time.Second)
		err := b.LoadState(lctx, opt.StateFile)
		lcancel()
		if err != nil {
			b.Close()
			return nil, fmt.Errorf("stealth: load state: %w", err)
		}
	}
	return b, nil
}

func parseProxy(raw string) (proxyInfo, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return proxyInfo{}, fmt.Errorf("stealth: invalid proxy %q", raw)
	}
	pi := proxyInfo{server: u.Scheme + "://" + u.Host}
	if u.User != nil {
		pi.user = u.User.Username()
		pi.pass, _ = u.User.Password()
	}
	return pi, nil
}

// UserAgent is the User-Agent this browser presents. Use it for an HTTP
// session that reuses the browser's cookies.
func (b *Browser) UserAgent() string { return b.ua }

// Close shuts the browser down.
func (b *Browser) Close() {
	b.once.Do(func() {
		if b.opt.StateFile != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if err := b.SaveState(ctx, b.opt.StateFile); err != nil {
				_ = err // browser is going away; nothing useful to do with it
			}
			cancel()
		}
		if b.cancelB != nil {
			b.cancelB()
		}
		if b.cancelA != nil {
			b.cancelA()
		}
	})
}

// Do satisfies the same shape as fetcher.Fetcher.Do so spiders can use
// either backend. Only GET is supported.
func (b *Browser) Do(ctx context.Context, req fetcher.Request) (*fetcher.Response, error) {
	if req.Method != "" && req.Method != http.MethodGet {
		return nil, errors.New("stealth: only GET is supported")
	}
	return b.Fetch(ctx, req.URL, FetchOptions{})
}

// Fetch loads a URL in a fresh tab and returns the rendered DOM.
func (b *Browser) Fetch(ctx context.Context, rawURL string, fo FetchOptions) (*fetcher.Response, error) {
	// Chrome would happily open file://, chrome:// or javascript: URLs, which
	// turns a fetch tool into a local file reader. Only web URLs pass.
	u, perr := url.Parse(rawURL)
	if perr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("stealth: %q is not an http or https URL", rawURL)
	}
	if b.opt.BlockPrivate {
		if err := netguard.CheckURL(ctx, rawURL); err != nil {
			return nil, err
		}
	}
	select {
	case b.sem <- struct{}{}:
		defer func() { <-b.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	tabCtx, cancelTab := chromedp.NewContext(b.ctx)
	defer cancelTab()
	tabCtx, cancelT := context.WithTimeout(tabCtx, b.opt.Timeout)
	defer cancelT()
	stop := context.AfterFunc(ctx, cancelT)
	defer stop()

	var (
		mu     sync.Mutex
		status int
		docURL = rawURL
	)
	idle := newIdleTracker()
	loaded := make(chan struct{}, 1)
	var rec *capturer
	if fo.Record || b.opt.RecordRequests {
		rec = newCapturer(tabCtx, fo.CaptureFilter)
	}
	chromedp.ListenTarget(tabCtx, func(ev any) {
		switch e := ev.(type) {
		case *network.EventRequestWillBeSent:
			idle.started(e.RequestID)
		case *network.EventLoadingFinished:
			idle.done(e.RequestID)
		case *network.EventLoadingFailed:
			idle.done(e.RequestID)
		case *page.EventLoadEventFired:
			select {
			case loaded <- struct{}{}:
			default:
			}
		}
		if rec != nil {
			switch e := ev.(type) {
			case *network.EventRequestWillBeSent:
				rec.sent(e)
			case *network.EventResponseReceived:
				rec.response(e)
			case *network.EventLoadingFinished:
				rec.finished(e)
			case *network.EventLoadingFailed:
				rec.failed(e)
			}
		}
		switch e := ev.(type) {
		case *network.EventResponseReceived:
			if e.Type == network.ResourceTypeDocument {
				mu.Lock()
				status, docURL = int(e.Response.Status), e.Response.URL
				mu.Unlock()
			}
		case *fetch.EventRequestPaused:
			go func() {
				_ = chromedp.Run(tabCtx, chromedp.ActionFunc(func(c context.Context) error {
					return fetch.ContinueRequest(e.RequestID).Do(c)
				}))
			}()
		case *fetch.EventAuthRequired:
			go func() {
				_ = chromedp.Run(tabCtx, chromedp.ActionFunc(func(c context.Context) error {
					return fetch.ContinueWithAuth(e.RequestID, &fetch.AuthChallengeResponse{
						Response: fetch.AuthChallengeResponseResponseProvideCredentials,
						Username: b.proxy.user,
						Password: b.proxy.pass,
					}).Do(c)
				}))
			}()
		}
	})

	var htmlOut string
	tasks := chromedp.Tasks{
		network.Enable(),
		chromedp.ActionFunc(b.prepareTab),
		chromedp.ActionFunc(func(ctx context.Context) error {
			ref := fo.Referer
			if ref == "" && b.opt.SpoofReferer {
				ref = googleReferer(rawURL)
			}
			if ref == "" {
				return chromedp.Navigate(rawURL).Do(ctx)
			}
			return navigateWithReferrer(ctx, rawURL, ref, loaded)
		}),
	}
	if b.opt.SolveChallenge {
		tasks = append(tasks, chromedp.ActionFunc(waitChallenge))
	}
	if fo.WaitSelector != "" {
		tasks = append(tasks, chromedp.WaitReady(fo.WaitSelector, chromedp.ByQuery))
	} else {
		tasks = append(tasks, chromedp.WaitReady("body", chromedp.ByQuery))
	}
	if fo.WaitIdle || b.opt.NetworkIdle {
		tasks = append(tasks, chromedp.ActionFunc(func(ctx context.Context) error {
			return idle.wait(ctx, 500*time.Millisecond, 15*time.Second)
		}))
	}
	if fo.Humanize {
		tasks = append(tasks, chromedp.ActionFunc(humanize))
	}
	if fo.Settle > 0 {
		tasks = append(tasks, chromedp.Sleep(fo.Settle))
	}
	tasks = append(tasks, fo.Actions...)
	if rec != nil {
		tasks = append(tasks, chromedp.ActionFunc(func(ctx context.Context) error {
			return rec.settle(ctx, 600*time.Millisecond, 15*time.Second)
		}))
	}
	var shot []byte
	if fo.Screenshot {
		tasks = append(tasks, chromedp.FullScreenshot(&shot, 100))
	}
	var cookies []*network.Cookie
	tasks = append(tasks, chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		cookies, err = network.GetCookies().WithURLs([]string{rawURL}).Do(ctx)
		return err
	}))
	tasks = append(tasks, chromedp.OuterHTML("html", &htmlOut, chromedp.ByQuery))

	if err := chromedp.Run(tabCtx, tasks); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("stealth: fetch %s: %w", rawURL, err)
	}

	mu.Lock()
	st, final := status, docURL
	mu.Unlock()
	if st == 0 {
		st = 200
	}
	body := []byte("<!DOCTYPE html>" + htmlOut)
	var captured []fetcher.Capture
	if rec != nil {
		captured = rec.results()
	}
	var jar []*http.Cookie
	for _, c := range cookies {
		jar = append(jar, &http.Cookie{Name: c.Name, Value: c.Value, Domain: c.Domain, Path: c.Path,
			Secure: c.Secure, HttpOnly: c.HTTPOnly})
	}
	return &fetcher.Response{
		Cookies:    jar,
		Screenshot: shot,
		Captured:   captured,
		Status:     st,
		Headers:    http.Header{"Content-Type": {"text/html; charset=utf-8"}},
		Body:       body,
		URL:        final,
		Proxy:      b.opt.Proxy,
		Profile:    "chrome-browser",
		Blocked:    fetcher.IsBlocked(st, body),
	}, nil
}

// prepareTab applies per-tab identity: UA, locale, timezone, stealth script
// (runs before any page script) and proxy auth interception.
func (b *Browser) prepareTab(ctx context.Context) error {
	if b.proxy.user != "" {
		if err := fetch.Enable().WithHandleAuthRequests(true).Do(ctx); err != nil {
			return err
		}
	}
	pats := blockPatterns(b.opt.BlockDomains, b.opt.BlockAds)
	if b.opt.DisableResources {
		pats = append(pats, resourcePatterns()...)
	}
	if len(pats) > 0 {
		if err := network.SetBlockedURLs(pats).Do(ctx); err != nil {
			return err
		}
	}
	ov := emulation.SetUserAgentOverride(b.ua).
		WithAcceptLanguage(b.opt.Locale + "," + strings.SplitN(b.opt.Locale, "-", 2)[0] + ";q=0.9").
		WithPlatform(platformFromUA(b.ua))
	if err := ov.Do(ctx); err != nil {
		return err
	}
	if err := emulation.SetLocaleOverride().WithLocale(b.opt.Locale).Do(ctx); err != nil {
		// Some Chrome builds reject repeated overrides; not fatal.
		_ = err
	}
	if b.opt.Timezone != "" {
		if err := emulation.SetTimezoneOverride(b.opt.Timezone).Do(ctx); err != nil {
			return err
		}
	}
	_, err := page.AddScriptToEvaluateOnNewDocument(stealthJS(b.opt.Locale)).Do(ctx)
	return err
}

func platformFromUA(ua string) string {
	switch {
	case strings.Contains(ua, "Windows"):
		return "Win32"
	case strings.Contains(ua, "Macintosh"):
		return "MacIntel"
	}
	return "Linux x86_64"
}

// waitChallenge polls until the page title is no longer a known
// interstitial. While it persists it periodically tries to click a
// Turnstile checkbox, at most maxClicks times.
func waitChallenge(ctx context.Context) error {
	const maxClicks = 4
	deadline := time.Now().Add(45 * time.Second)
	clicks := 0
	lastClick := time.Time{}
	for time.Now().Before(deadline) {
		var title string
		if err := chromedp.Title(&title).Do(ctx); err != nil {
			return err
		}
		t := strings.ToLower(title)
		if !strings.Contains(t, "just a moment") && !strings.Contains(t, "attention required") &&
			!strings.Contains(t, "checking your browser") && !strings.Contains(t, "verifying") {
			return nil
		}
		if clicks < maxClicks && time.Since(lastClick) > 5*time.Second {
			// Give passive checks a moment before touching the widget.
			if clicks > 0 || time.Until(deadline) < 40*time.Second {
				if found, _ := clickTurnstile(ctx); found {
					clicks++
					lastClick = time.Now()
				}
			}
		} else {
			_ = humanize(ctx)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second + time.Duration(rand.IntN(500))*time.Millisecond):
		}
	}
	return errors.New("stealth: challenge did not clear")
}

// humanize moves the mouse along a curved path and scrolls a little.
func humanize(ctx context.Context) error {
	x, y := 100+rand.Float64()*300, 100+rand.Float64()*200
	tx, ty := 400+rand.Float64()*600, 250+rand.Float64()*350
	const steps = 12
	for i := 1; i <= steps; i++ {
		t := float64(i) / steps
		// ease-in-out plus a small wobble
		e := t * t * (3 - 2*t)
		px := x + (tx-x)*e + rand.Float64()*3
		py := y + (ty-y)*e + rand.Float64()*3
		if err := input.DispatchMouseEvent(input.MouseMoved, px, py).Do(ctx); err != nil {
			return err
		}
		time.Sleep(time.Duration(8+rand.IntN(20)) * time.Millisecond)
	}
	return input.DispatchMouseEvent(input.MouseWheel, tx, ty).WithDeltaY(120 + rand.Float64()*200).Do(ctx)
}

// googleReferer is what a click from Google search sends: Google uses a
// strict-origin referrer policy, so only the origin arrives.
func googleReferer(string) string { return "https://www.google.com/" }

// navigateWithReferrer navigates with a Referer on the top-level request
// only (SetExtraHTTPHeaders would stamp it on every subresource too) and
// waits for the load event.
func navigateWithReferrer(ctx context.Context, rawURL, ref string, loaded <-chan struct{}) error {
	select { // drop a stale signal from about:blank
	case <-loaded:
	default:
	}
	_, _, errText, _, err := page.Navigate(rawURL).WithReferrer(ref).Do(ctx)
	if err != nil {
		return err
	}
	if errText != "" {
		return fmt.Errorf("navigate: %s", errText)
	}
	select {
	case <-loaded:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
