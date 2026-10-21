// Command gocrapling fetches, extracts and crawls from the terminal.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"regexp"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/Chief-spartan-117/gocrapling/internal/mcp"
	"github.com/Chief-spartan-117/gocrapling/internal/server"
	"github.com/Chief-spartan-117/gocrapling/internal/service"
	"github.com/Chief-spartan-117/gocrapling/pkg/fetcher"
	"github.com/Chief-spartan-117/gocrapling/pkg/parser"
	"github.com/Chief-spartan-117/gocrapling/pkg/spider"
	"github.com/Chief-spartan-117/gocrapling/pkg/stealth"
)

const usage = `gocrapling - undetected scraping toolkit

Usage:
  gocrapling fetch [flags] URL      fetch a page, optionally extract with CSS/XPath
  gocrapling crawl [flags] URL...   crawl with a declarative extraction spec
  gocrapling shell                  interactive explorer (get, css, curl, md, ...)
  gocrapling serve                  REST API (/v1) and MCP over HTTP on 127.0.0.1:8080
  gocrapling mcp                    MCP server on stdio for AI agents
  gocrapling extract [flags] URL FILE  save a page as .md, .txt or .html
  gocrapling install                download a managed Chrome for Testing
  gocrapling version                print the version
  gocrapling fingerprint            show the TLS/HTTP2 fingerprint servers see

Run "gocrapling <command> -h" for flags.
`

// version is set at release time: -ldflags "-X main.version=v1.2.3".
var version = "dev"

// buildVersion reports the release version, or the module version when
// installed with "go install", or "dev".
func buildVersion() string {
	if version != "dev" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return version
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	mcp.Version = buildVersion()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch os.Args[1] {
	case "fetch":
		err = cmdFetch(ctx, os.Args[2:])
	case "crawl":
		err = cmdCrawl(ctx, os.Args[2:])
	case "shell":
		err = cmdShell(ctx, os.Args[2:])
	case "mcp":
		err = cmdMCP(ctx, os.Args[2:])
	case "serve":
		// REST API + MCP over HTTP; same as "mcp -http 127.0.0.1:8080".
		err = cmdMCP(ctx, append([]string{"-http", "127.0.0.1:8080"}, os.Args[2:]...))
	case "install":
		err = cmdInstall(ctx, os.Args[2:])
	case "extract":
		err = cmdExtract(ctx, os.Args[2:])
	case "fingerprint":
		err = cmdFingerprint(ctx)
	case "version", "-v", "--version":
		fmt.Println("gocrapling", buildVersion())
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

type common struct {
	profile      string
	proxies      multi
	timeout      time.Duration
	retries      int
	referer      bool
	stealthy     bool
	headed       bool
	blockAds     bool
	blockPrivate bool
	http3        bool
	doh          string
	remote       string
	// browser extras
	disableResources bool
	networkIdle      bool
	stateFile        string
	solve            bool
}

func (c *common) register(fs *flag.FlagSet) {
	fs.StringVar(&c.profile, "profile", "chrome", "browser identity: chrome, firefox, safari, random")
	fs.Var(&c.proxies, "proxy", "proxy URL (repeatable; rotated per request)")
	fs.DurationVar(&c.timeout, "timeout", 30*time.Second, "per-attempt timeout")
	fs.IntVar(&c.retries, "retries", 3, "retries after the first attempt")
	fs.BoolVar(&c.referer, "google-referer", false, "send a Google search Referer")
	fs.BoolVar(&c.stealthy, "browser", false, "use the stealth Chrome browser instead of plain HTTP")
	fs.BoolVar(&c.headed, "headed", false, "with -browser: show the window")
	fs.BoolVar(&c.http3, "http3", false, "race HTTP/3 against HTTP/2 like Chrome")
	fs.StringVar(&c.doh, "doh", "", `resolve DNS over HTTPS: "cloudflare", "google" or an endpoint URL`)
	fs.StringVar(&c.remote, "remote", "", "browser: attach to a running Chrome, e.g. http://127.0.0.1:9222")
	fs.BoolVar(&c.disableResources, "disable-resources", false, "browser: skip images, fonts and media")
	fs.BoolVar(&c.networkIdle, "network-idle", false, "browser: wait for the network to go quiet after load")
	fs.StringVar(&c.stateFile, "state-file", "", "browser: load cookies from, and save them to, this file (keeps logins)")
	fs.BoolVar(&c.solve, "solve-cloudflare", true, "browser: wait out Cloudflare interstitials and click Turnstile")
	fs.BoolVar(&c.blockPrivate, "block-private", false, "refuse loopback, private and cloud-metadata addresses (use when URLs are untrusted)")
	fs.BoolVar(&c.blockAds, "block-ads", false, "browser: block ad and tracker hosts")
}

func (c *common) fetcher() *fetcher.Fetcher {
	var rot fetcher.ProxyRotator
	if len(c.proxies) > 0 {
		rot = fetcher.NewPoolRotator(c.proxies, false)
	}
	return fetcher.New(fetcher.Options{
		Profile: c.profile, Timeout: c.timeout, Retries: c.retries,
		Proxies: rot, SpoofReferer: c.referer, HTTP3: c.http3, DoH: c.doh, BlockPrivate: c.blockPrivate,
	})
}

func (c *common) browserOptions() stealth.Options {
	return stealth.Options{
		Headed: c.headed, BlockAds: c.blockAds, RemoteURL: c.remote, Timeout: 2 * c.timeout,
		DisableResources: c.disableResources, NetworkIdle: c.networkIdle, StateFile: c.stateFile,
		SpoofReferer: c.referer, BlockPrivate: c.blockPrivate,
	}
}

func (c *common) browser() (*stealth.Browser, error) {
	opt := c.browserOptions()
	opt.SolveChallenge = c.solve
	if len(c.proxies) > 0 {
		opt.Proxy = c.proxies[0]
	}
	return stealth.New(opt)
}

func cmdFetch(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("fetch", flag.ExitOnError)
	var c common
	c.register(fs)
	css := fs.String("css", "", `CSS selector; supports ::text and ::attr(name)`)
	xpath := fs.String("xpath", "", "XPath expression (prints text of matches)")
	asJSON := fs.Bool("json", false, "print result as JSON")
	showHeaders := fs.Bool("i", false, "include status and headers")
	shot := fs.String("screenshot", "", "browser: write a full-page PNG to this file")
	md := fs.Bool("markdown", false, "print the page as Markdown")
	mdMain := fs.Bool("main", false, "with -markdown: only <main>/<article> content")
	var po pageOpts
	po.register(fs)
	ai := fs.Bool("ai-targeted", false, "with -markdown: strip hidden content and invisible characters")
	record := fs.String("record", "", "browser: capture background XHR/fetch responses whose URL matches this regexp ('.' = all)")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		return fmt.Errorf("fetch needs exactly one URL")
	}
	target := fs.Arg(0)

	if *shot != "" {
		b, err := c.browser()
		if err != nil {
			return err
		}
		defer b.Close()
		resp, err := b.Fetch(ctx, target, stealth.FetchOptions{Screenshot: true})
		if err != nil {
			return err
		}
		if err := os.WriteFile(*shot, resp.Screenshot, 0o644); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "wrote %s (%d bytes)\n", *shot, len(resp.Screenshot))
		return nil
	}
	if *record != "" {
		re, err := regexp.Compile(*record)
		if err != nil {
			return fmt.Errorf("bad -record regexp: %w", err)
		}
		b, err := c.browser()
		if err != nil {
			return err
		}
		defer b.Close()
		resp, err := b.Fetch(ctx, target, stealth.FetchOptions{Record: true, CaptureFilter: re})
		if err != nil {
			return err
		}
		if *asJSON {
			type capOut struct {
				URL, Method, MimeType string
				Status                int
				Body                  string
			}
			var outs []capOut
			for _, cp := range resp.Captured {
				outs = append(outs, capOut{cp.URL, cp.Method, cp.MimeType, cp.Status, string(cp.Body)})
			}
			return json.NewEncoder(os.Stdout).Encode(outs)
		}
		for _, cp := range resp.Captured {
			fmt.Printf("# %s %s -> %d %s\n%s\n\n", cp.Method, cp.URL, cp.Status, cp.MimeType, cp.Body)
		}
		return nil
	}
	resp, closeFn, err := c.get(ctx, target, po)
	defer closeFn()
	if resp == nil {
		return err
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning:", err)
	}
	if *showHeaders {
		fmt.Fprintf(os.Stderr, "HTTP %d  profile=%s attempts=%d blocked=%v\n", resp.Status, resp.Profile, resp.Attempts, resp.Blocked)
	}

	var results []string
	switch {
	case *md:
		doc, perr := resp.Parse()
		if perr != nil {
			return perr
		}
		results = []string{doc.MarkdownWith(parser.MarkdownOptions{MainOnly: *mdMain, Safe: *ai})}
	case *css != "" || *xpath != "":
		doc, perr := resp.Parse()
		if perr != nil {
			return perr
		}
		if *css != "" {
			results = doc.Extract(*css)
		} else {
			sel, xerr := doc.XPath(*xpath)
			if xerr != nil {
				return xerr
			}
			results = sel.Texts()
		}
	default:
		results = []string{resp.Text()}
	}

	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{
			"status": resp.Status, "url": resp.URL, "results": results,
		})
	}
	for _, r := range results {
		fmt.Println(r)
	}
	return nil
}

func cmdFingerprint(ctx context.Context) error {
	f := fetcher.New(fetcher.Options{Profile: "chrome"})
	defer f.Close()
	r, err := f.Get(ctx, "https://tls.peet.ws/api/clean")
	if err != nil {
		return err
	}
	fmt.Println(r.Text())
	return nil
}

func cmdCrawl(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("crawl", flag.ExitOnError)
	var c common
	c.register(fs)
	var fields multi
	fs.Var(&fields, "field", `item field "name=css" (repeatable), e.g. -field 'title=h1::text'`)
	follow := fs.String("follow", "a::attr(href)", "CSS selector yielding links to follow (empty disables)")
	itemSel := fs.String("item", "", "optional CSS selector scoping one item per match; fields are relative to it")
	out := fs.String("out", "items.jsonl", "output file; .csv writes CSV, .xml writes XML, otherwise JSON Lines")
	var routes multi
	fs.Var(&routes, "route", `per-host mode "host=http|browser|auto[,delay=2s]" (repeatable)`)
	depth := fs.Int("depth", 2, "max link depth (0 = unlimited)")
	maxReq := fs.Int64("max", 0, "max requests (0 = unlimited)")
	conc := fs.Int("concurrency", 8, "total concurrent requests")
	perDomain := fs.Int("per-domain", 2, "concurrent requests per host")
	delay := fs.Duration("delay", 500*time.Millisecond, "minimum gap between requests to one host")
	robots := fs.Bool("robots", true, "obey robots.txt")
	sameSite := fs.Bool("same-site", true, "only follow links on the start hosts")
	checkpoint := fs.String("checkpoint", "", "checkpoint file; enables Ctrl-C pause and resume")
	cacheDir := fs.String("cache", "", "development mode: cache responses in this directory and replay them")
	autoThrottle := fs.Bool("autothrottle", false, "adapt per-host delay to latency and blocking")
	sitemap := fs.Bool("sitemap", false, "treat start URLs as sitemap.xml / sitemap index / robots.txt")
	var allow, deny multi
	fs.Var(&allow, "allow", "regexp; only follow/accept URLs matching (repeatable)")
	fs.Var(&deny, "deny", "regexp; skip URLs matching (repeatable)")
	escalate := fs.Bool("escalate", false, "retry blocked pages in the stealth browser")
	_ = fs.Parse(args)
	if fs.NArg() == 0 || len(fields) == 0 {
		return fmt.Errorf("crawl needs at least one URL and one -field")
	}

	type fieldSpec struct{ name, sel string }
	var specs []fieldSpec
	for _, f := range fields {
		n, s, ok := strings.Cut(f, "=")
		if !ok || n == "" || s == "" {
			return fmt.Errorf("bad -field %q, want name=css", f)
		}
		specs = append(specs, fieldSpec{n, s})
	}

	compile := func(list multi) ([]*regexp.Regexp, error) {
		var out []*regexp.Regexp
		for _, p := range list {
			re, err := regexp.Compile(p)
			if err != nil {
				return nil, fmt.Errorf("bad regexp %q: %w", p, err)
			}
			out = append(out, re)
		}
		return out, nil
	}
	allowRe, err := compile(allow)
	if err != nil {
		return err
	}
	denyRe, err := compile(deny)
	if err != nil {
		return err
	}
	le := spider.LinkExtractor{Allow: allowRe, Deny: denyRe}
	if *follow != "" {
		le.Restrict = *follow
	}

	extract := func(r *spider.Response, o *spider.Emitter) error {
		doc, err := r.Doc()
		if err != nil {
			return err
		}
		if *itemSel == "" {
			item := map[string]any{"url": r.URL}
			for _, f := range specs {
				v := doc.Extract(f.sel)
				switch len(v) {
				case 0:
					item[f.name] = nil
				case 1:
					item[f.name] = v[0]
				default:
					item[f.name] = v
				}
			}
			o.Item(item)
			return nil
		}
		for _, el := range doc.CSS(*itemSel) {
			item := map[string]any{"url": r.URL}
			for _, f := range specs {
				if v := el.Extract(f.sel); len(v) > 0 {
					item[f.name] = v[0]
				} else {
					item[f.name] = nil
				}
			}
			o.Item(item)
		}
		return nil
	}
	page := func(ctx context.Context, r *spider.Response, o *spider.Emitter) error {
		if err := extract(r, o); err != nil {
			return err
		}
		if *follow != "" {
			return o.FollowLinks(le, "parse")
		}
		return nil
	}

	var sp *spider.Spider
	if *sitemap {
		var rules []spider.SitemapRule
		if len(allowRe) == 0 {
			rules = []spider.SitemapRule{{Callback: "page"}}
		}
		for _, re := range allowRe {
			rules = append(rules, spider.SitemapRule{Pattern: re, Callback: "page"})
		}
		sp = spider.NewSitemapSpider("cli", fs.Args(), rules, map[string]spider.Callback{
			"page": func(ctx context.Context, r *spider.Response, o *spider.Emitter) error { return extract(r, o) },
		})
	} else {
		sp = &spider.Spider{Name: "cli", StartURLs: fs.Args(), Callbacks: map[string]spider.Callback{"parse": page}}
	}
	if *sameSite {
		for _, u := range fs.Args() {
			if i := strings.Index(u, "://"); i >= 0 {
				host := strings.SplitN(u[i+3:], "/", 2)[0]
				sp.AllowedDomains = append(sp.AllowedDomains, host)
			}
		}
	}

	var pipe spider.Pipeline
	if strings.HasSuffix(strings.ToLower(*out), ".csv") {
		cols := make([]string, 0, len(specs)+1)
		cols = append(cols, "url")
		for _, f := range specs {
			cols = append(cols, f.name)
		}
		pipe, err = spider.NewCSVPipeline(*out, cols...)
	} else if strings.HasSuffix(strings.ToLower(*out), ".xml") {
		pipe, err = spider.NewXMLPipeline(*out, "items", "item")
	} else {
		pipe, err = spider.NewJSONLPipeline(*out)
	}
	if err != nil {
		return err
	}
	var rts []spider.Route
	for _, r := range routes {
		rt, err := parseRoute(r)
		if err != nil {
			return err
		}
		rts = append(rts, rt)
	}
	f := c.fetcher()
	defer f.Close()
	cfg := spider.Config{
		HTTP: f, Concurrency: *conc, PerDomainConcurrency: *perDomain,
		DownloadDelay: *delay, RandomizeDelay: true, MaxDepth: *depth, MaxRequests: *maxReq,
		RespectRobots: *robots, RobotsUserAgent: "gocrapling",
		Pipelines: []spider.Pipeline{pipe}, CheckpointPath: *checkpoint, Routes: rts,
		CacheDir: *cacheDir, AutoThrottle: spider.AutoThrottle{Enabled: *autoThrottle},
	}
	needBrowser := c.stealthy || *escalate
	for _, r := range rts {
		needBrowser = needBrowser || r.Mode == spider.ModeBrowser
	}
	if needBrowser {
		b, err := c.browser()
		if err != nil {
			return err
		}
		defer b.Close()
		cfg.Browser = b
		cfg.EscalateOnBlock = *escalate
		if c.stealthy {
			cfg.HTTP = b
		}
	}
	eng, err := spider.NewEngine(cfg)
	if err != nil {
		return err
	}
	stats, err := eng.Run(ctx, sp)
	fmt.Fprintln(os.Stderr, stats)
	return err
}

// parseRoute reads "host=browser,delay=2s".
func parseRoute(spec string) (spider.Route, error) {
	host, rest, ok := strings.Cut(spec, "=")
	if !ok || host == "" {
		return spider.Route{}, fmt.Errorf("bad -route %q, want host=mode[,delay=D]", spec)
	}
	r := spider.Route{Host: host}
	for i, part := range strings.Split(rest, ",") {
		k, v, hasV := strings.Cut(part, "=")
		switch {
		case i == 0 && !hasV:
			switch strings.ToLower(k) {
			case "http":
				r.Mode = spider.ModeHTTP
			case "browser":
				r.Mode = spider.ModeBrowser
			case "auto":
				r.Mode = spider.ModeAuto
			default:
				return r, fmt.Errorf("bad mode %q in -route %q", k, spec)
			}
		case k == "delay" && hasV:
			d, err := time.ParseDuration(v)
			if err != nil {
				return r, fmt.Errorf("bad delay in -route %q: %w", spec, err)
			}
			r.Delay = d
		default:
			return r, fmt.Errorf("bad -route %q", spec)
		}
	}
	return r, nil
}

func cmdShell(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("shell", flag.ExitOnError)
	var c common
	c.register(fs)
	_ = fs.Parse(args)
	f := c.fetcher()
	defer f.Close()
	var br *stealth.Browser
	defer func() {
		if br != nil {
			br.Close()
		}
	}()
	getBrowser := func() (*stealth.Browser, error) {
		if br != nil {
			return br, nil
		}
		b, err := c.browser()
		if err != nil {
			return nil, err
		}
		br = b
		return br, nil
	}
	return runShell(ctx, os.Stdin, os.Stdout, f, getBrowser)
}

func cmdMCP(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	var c common
	c.register(fs)
	httpAddr := fs.String("http", "", "serve MCP over HTTP at this address (POST /mcp) instead of stdio, e.g. 127.0.0.1:8080")
	token := fs.String("token", os.Getenv("GOCRAPLING_MCP_TOKEN"), "bearer token required for -http (default $GOCRAPLING_MCP_TOKEN)")
	allowPrivate := fs.Bool("allow-private", false, "let tools reach loopback, private and link-local addresses (off: agents cannot probe internal services or cloud metadata)")
	stateDir := fs.String("state-dir", "", "directory for saved login states (default: user cache dir)")
	_ = fs.Parse(args)
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	opt := service.Options{Proxies: c.proxies, Logger: log}
	opt.Browser = c.browserOptions()
	if len(c.proxies) > 0 {
		opt.Browser.Proxy = c.proxies[0]
	}
	opt.StateDir = *stateDir
	opt.AllowPrivate = *allowPrivate
	svc := service.New(opt)
	defer svc.Close()
	srv := mcp.New(svc, log)
	if *httpAddr != "" {
		if *token == "" && !isLoopbackAddr(*httpAddr) {
			return fmt.Errorf("-http on a non-loopback address needs -token (or $GOCRAPLING_MCP_TOKEN)")
		}
		fmt.Fprintf(os.Stderr, "REST API on %[1]s/v1, MCP on %[1]s/mcp (auth: %v)\n", *httpAddr, *token != "")
		return server.ListenAndServe(ctx, *httpAddr, server.Handler(svc, srv, *token))
	}
	// stdout carries the protocol; every log line goes to stderr.
	err := srv.Serve(ctx, os.Stdin, os.Stdout)
	if err != nil && ctx.Err() != nil {
		return nil
	}
	return err
}

// isLoopbackAddr reports whether addr binds only to the local machine. An
// empty host (":8080") listens on every interface and is not loopback.
func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
