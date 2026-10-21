# gocrapling

A Go scraping toolkit modelled on [Scrapling](https://scrapling.readthedocs.io): a fetcher that looks like a real browser on the wire, a stealth Chrome driver, an HTML parser that can find elements again after a site redesign, and a Scrapy-style spider framework.

## Packages

| Package | What it does |
|---|---|
| `fetcher` | HTTP client with browser-accurate TLS (JA3/JA4), HTTP/2 settings and header order, built on `bogdanfinn/tls-client`. Profile-consistent headers, proxy rotation with cooldown, retries with jittered backoff and `Retry-After`, block detection, optional Google referer. |
| `stealth` | Chrome via DevTools with automation tells patched (`navigator.webdriver`, plugins, `window.chrome`, WebGL strings, `HeadlessChrome` UA, permissions), proxy auth, human-like mouse movement, interstitial wait. |
| `parser` | CSS (`::text`, `::attr()`), XPath, text and regex search, `FindSimilar`, and adaptive relocation backed by memory or JSON-file storage. |
| `spider` | Priority scheduler, de-duplication, per-host concurrency and delay, `robots.txt` (wildcards, `Crawl-delay`), depth limit, offsite filter, JSONL pipeline, checkpoint pause/resume, automatic escalation from HTTP to browser when blocked. |

## CLI

```sh
go build -o bin/gocrapling ./cmd/gocrapling

bin/gocrapling fetch -css 'a::attr(href)' https://example.com
bin/gocrapling fetch -browser -css 'h1::text' https://example.com
bin/gocrapling crawl -field 'title=h1::text' -field 'price=.price::text' \
    -depth 3 -delay 1s -checkpoint crawl.cp -out items.jsonl https://shop.example
```

Ctrl-C during `crawl -checkpoint` saves state; run the same command again to resume.

## Library

```go
f := fetcher.New(fetcher.Options{
    Profile:      "chrome",
    Proxies:      fetcher.NewPoolRotator([]string{"http://u:p@host:8000"}, false),
    SpoofReferer: true,
})
defer f.Close()

resp, err := f.Get(ctx, "https://example.com")
doc, _ := resp.Parse()
for _, title := range doc.Extract("h2.title::text") { fmt.Println(title) }
```

Adaptive selectors:

```go
// first run: remember what ".price" matched
doc.CSSWith(".price", parser.QueryOptions{AutoSave: true, Identifier: "price"})
// after a redesign: relocate if the selector now matches nothing
doc.CSSWith(".price", parser.QueryOptions{Adaptive: true, Identifier: "price"})
```
Pass `parser.WithStorage(store)` to `Parse` (`parser.NewFileStorage("sigs.json")`) to persist across runs.

Spiders: see `spider/spider_test.go` for complete examples.

### Output

```go
csv, _ := spider.NewCSVPipeline("items.csv", "url", "title", "price") // header written once
jsonl, _ := spider.NewJSONLPipeline("items.jsonl")
```
In the CLI, an `-out` name ending in `.csv` writes CSV (columns: `url` plus each `-field`).

### Middleware hooks

```go
spider.Config{Middlewares: []spider.Middleware{{
    Name: "auth",
    OnRequest: func(ctx context.Context, r *spider.Request) error {
        r.Headers = map[string]string{"authorization": "Bearer " + token()}
        return nil // or spider.ErrDrop to skip
    },
    OnResponse: func(ctx context.Context, r *spider.Request, resp *fetcher.Response) error {
        if resp.Blocked { solveAndSetCookies(r); return spider.ErrRetry } // max Config.MaxHookRetries
        return nil
    },
}}}
```
`OnResponse` also sees blocked responses, which makes it the extension point for third-party CAPTCHA services.

### Per-domain HTTP or browser

```go
spider.Config{
    HTTP: f, Browser: b, EscalateOnBlock: true,
    Routes: []spider.Route{
        {Host: "spa.example.com", Mode: spider.ModeBrowser},
        {Host: "api.example.com", Mode: spider.ModeHTTP, Delay: 200 * time.Millisecond},
    },
}
```
CLI: `-route spa.example.com=browser -route api.example.com=http,delay=200ms`. Hosts match subdomains; first match wins. `Request.Browser` still forces the browser for a single request.

### Browser extras

```go
b, _ := stealth.New(stealth.Options{BlockAds: true, SolveChallenge: true})
resp, _ := b.Fetch(ctx, "https://spa.example.com", stealth.FetchOptions{
    Record: true, CaptureFilter: regexp.MustCompile(`/api/`), // background XHR/fetch JSON
})
for _, c := range resp.Captured { fmt.Println(c.URL, string(c.Body)) }
```
`SolveChallenge` waits for Cloudflare interstitials and clicks a Turnstile checkbox when it finds the widget (closed shadow roots are opened by the stealth script). Clicking is tested against a simulated widget only; real Cloudflare may still refuse. `resp.Cookies` and `b.UserAgent()` let an HTTP session reuse a browser-earned clearance:

```go
sess, _ := fetcher.NewSession(fetcher.Options{Profile: "chrome"}) // pinned identity + cookie jar
sess.SetCookies(url, resp.Cookies)
```
Clearance cookies are normally bound to UA and IP, so use the same proxy for both.

### Markdown

```go
doc, _ := resp.Parse()
md := doc.MarkdownMain() // <main>/<article> only; Markdown() for the whole page
```
CLI: `gocrapling fetch -markdown -main URL`.

### Crawl tools

- `LinkExtractor{Allow, Deny, AllowDomains, DenyExtensions}` and `Emitter.FollowLinks`.
- `NewCrawlSpider(name, start, []CrawlRule{...}, callbacks)` for rule-based crawls.
- `NewSitemapSpider(name, sitemaps, rules, callbacks)` handles indexes, gzip and `robots.txt` `Sitemap:` lines.
- `Config.AutoThrottle` adapts per-host delay to latency; 403/429/503 or a block page doubles it.
- `Config.CacheDir` (CLI `-cache dir`) is development mode: responses are cached on disk and replayed.
- CLI: `crawl -sitemap -allow '/blog/' -autothrottle -cache .cache -block-ads ...`, `fetch -record 'api/' URL`.

### More tools

- **HTTP/3 and DoH:** `fetcher.Options{HTTP3: true}` races QUIC against TCP like Chrome; `DoH: "cloudflare"` resolves direct connections over DNS-over-HTTPS (proxied traffic is resolved by the proxy). CLI: `-http3`, `-doh cloudflare`. `Response.Proto` shows what was negotiated. DoH covers the HTTP fetcher only; Chrome still uses its own resolver.
- **Screenshots and remote Chrome:** `FetchOptions{Screenshot: true}` fills `Response.Screenshot` (full-page PNG); `stealth.Options{RemoteURL: "http://127.0.0.1:9222"}` attaches to a running Chrome. CLI: `fetch -browser -screenshot out.png`, `-remote URL`.
- **Selectors:** `FindAll(FindOptions{Tag, Attrs, HasAttrs, AttrRegex, Class, Text})`, `CSSPath()` and `XPathExpr()` generate a selector for any element.
- **Streaming:** `engine.Stream(ctx, spider)` yields items on a channel as they are scraped; `wait()` returns the final stats.
- **Output:** `NewXMLPipeline` (CLI: `-out items.xml`) next to JSONL and CSV.
- **Feeds:** `NewXMLFeedSpider` (RSS `item`, Atom `entry`) and `NewCSVFeedSpider`.
- **robots.txt:** `Crawl-delay` and `Request-rate` are both honoured.
- **MCP server:** `gocrapling mcp` speaks Model Context Protocol on stdio with tools `fetch`, `screenshot`, `capture_requests` and `crawl`. Register it in an MCP client as command `gocrapling` with args `["mcp"]`.
- **Shell:** `gocrapling shell` is a line REPL: `get`, `browser`, `curl <pasted Copy-as-cURL>`, `css`, `xpath`, `find`, `similar`, `path`, `md`, `shot`. `fetcher.ParseCurl` is the same converter as a library call.
- **Docker:** `docker build -t gocrapling .` then `docker run --rm gocrapling fetch -markdown https://example.com`. The image bundles Chromium and starts it with `--no-sandbox` when running as root. The image was built and run (linux/arm64): HTTP, browser and MCP all work in it. The publish workflow has not run.

### Added in the Scrapling-gap pass

- **Managed Chrome:** `gocrapling install` downloads Chrome for Testing into the user cache; the browser finds it automatically when no system Chrome is set.
- **`extract URL FILE`:** `.md`, `.txt` or `.html` by extension; `-css`, `-main`, `-browser`, `-wait-selector`, `-steps`, `-ai-targeted`.
- **Prompt-injection defence:** `parser.MarkdownOptions{Safe: true}`, `SafeText()`, `CleanInvisible()`; CLI/MCP `ai_targeted`. Hidden elements (`hidden`, `aria-hidden`, `display:none`, ...) and invisible Unicode are removed.
- **Browser options:** `DisableResources`, `NetworkIdle`/`WaitIdle`, `SpoofReferer`, `StateFile` (logins persist), page actions as JSON steps (`stealth.ParseSteps`); CLI `-disable-resources -network-idle -state-file -steps -wait-selector -solve-cloudflare`.
- **Parser:** `AllText(TextOptions)`, `URLJoin`, `Below`, `FindOptions.Filter`, `Response.JSON(&v)`.
- **Spider:** `Config.Sessions` + `Request.Session`, `Request.Proxy`, `Spider.IsBlocked`, `Retry-After` in AutoThrottle, `LinkExtractor.RestrictXPath`/`Canonicalize`, `NewJSONPipeline`, `NewShopifySpider` (one row per variant), `NewContentSpider` + `NewMarkdownDirPipeline`.
- **MCP over HTTP:** `gocrapling mcp -http 127.0.0.1:8080 -token $T` serves `POST /mcp` with bearer auth; a non-loopback bind without a token is refused. `fetch` tool gains `ai_targeted`, `actions`, `wait_selector`, `network_idle`, `state_file` (a plain name stored under `-state-dir`).
- **Agent skill:** `skills/gocrapling/SKILL.md`.
- **Images:** `.github/workflows/docker.yml` builds multi-arch images to GHCR (and Docker Hub if configured) on `v*` tags. Not yet run.

## Limits and how to work with them

No tool defeats every protection. Where this one stops, and what to do:

- **Fingerprint age.** Profiles come from `bogdanfinn/tls-client` (Chrome 133, Firefox 135, Safari 16). Upgrade that dependency, then add matching newer identities at startup with `fetcher.RegisterProfile(fetcher.Profile{...})`. The UA string must agree with the TLS profile: a mismatch is a stronger bot signal than an old browser.
- **Interactive CAPTCHAs** (image/hCaptcha/reCAPTCHA) are not solved, and the Turnstile click is best effort (verified against Cloudflare's interactive test widget, not production sites or managed interstitials). `SolveChallenge` waits for interstitials and clicks the Turnstile checkbox. Plug a solving service into `Middleware.OnResponse` (detect, solve, set cookies or headers, return `ErrRetry`). Use `-headed` and residential proxies for hard targets.
- **IP reputation** often matters more than fingerprints. Datacenter IPs get blocked regardless; use `PoolRotator` with residential or mobile proxies.
- **Per-site behaviour.** Use `Routes` to send JS-heavy hosts to the browser and keep cheap hosts on HTTP. Keep `-delay` and `-per-domain` low-rate on sensitive sites.
- **Terms and robots.** Respect each site's terms of service. `robots.txt` is obeyed by default in `crawl`.
- **Dependency pin.** `chromedp` is pinned to v0.14.2 with a matching `cdproto`; v0.21 has a different generic API.
