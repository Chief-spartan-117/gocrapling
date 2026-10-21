# gocrapling: Progress and Roadmap

Last updated: 2026-10-21 (third pass: all 24 README gaps built)

## Done

All packages build; `go vet` and `go test -race -count=1 ./...` pass.

| Package | Delivered | Verified by |
| --- | --- | --- |
| `fetcher` | TLS/HTTP2 browser fingerprint (tls-client), Chrome/Firefox/Safari profiles, `RegisterProfile`, proxy rotation with cooldown, retries with backoff and `Retry-After`, block detection, Google referer, pinned-identity `Session` with cookie import | Live check on tls.peet.ws (h2, real Chrome JA3/JA4); httptest retry, block, header and session tests |
| `stealth` | Chrome via DevTools, automation tells patched, proxy auth, human-like mouse, interstitial wait, Turnstile click, XHR/fetch capture, ad and tracker blocking, cookie export | bot.sannysoft.com: 31 passed, 0 failed; Chrome tests for capture and click |
| `parser` | CSS with `::text` and `::attr()`, XPath, text and regex search, `FindSimilar`, adaptive relocation with memory and file storage, Markdown conversion | Unit tests incl. redesign relocation and Markdown |
| `spider` | Priority queue, dedup, per-host limits and delay, robots.txt (wildcards, Crawl-delay), depth and offsite filters, JSONL and CSV pipelines, checkpoint pause/resume, middleware hooks, per-host HTTP/browser routes, escalation on block, AutoThrottle, response cache, LinkExtractor, CrawlSpider, SitemapSpider | Race-tested crawl, resume, robots, middleware, routes, CSV, links, sitemap, cache, throttle tests |
| `cmd/gocrapling` | `fetch` (`-markdown`, `-record`, `-browser`), `crawl` (`-route`, `-sitemap`, `-allow/-deny`, `-cache`, `-autothrottle`, `-checkpoint`, `.csv` output), `fingerprint` | Live runs against example.com, go.dev, httpbin.org, sitemaps.org |

## Scrapling parity

Compared against [Scrapling's docs](https://scrapling.readthedocs.io/en/latest/).

| # | Feature | Status |
| --- | --- | --- |
| 1 | Browser network capture (`record_requests`) | Done: `FetchOptions.Record`, CLI `fetch -record`; tested in Chrome |
| 2 | Development mode (response cache) | Done: `Config.CacheDir`, CLI `-cache`; tested |
| 3 | Markdown output | Done: `Markdown()`, `MarkdownMain()`, CLI `-markdown`; tested, spot-checked on go.dev |
| 4 | AutoThrottle | Done: `Config.AutoThrottle`; unit-tested |
| 5 | LinkExtractor, CrawlSpider, SitemapSpider | Done: tested; sitemap run verified live on sitemaps.org |
| 6 | Ad and tracker blocking in browser | Done: `BlockAds`, `BlockDomains`; proven by a Chrome test (see below) |
| 7 | Public `Session` type | Done: `fetcher.NewSession`, cookie import from browser; tested |
| 8 | Turnstile click | Done, best effort: passes a simulated widget and Cloudflare's interactive test widget (see below); production sites unverified |

### Lower-priority items (all built in the second pass)

| Feature | Status |
| --- | --- |
| HTTP/3 | Done: `Options.HTTP3` (QUIC/TCP racing), `DisableHTTP3`, `Response.Proto`; live test on cloudflare-quic.com negotiated HTTP/3.0 |
| DNS-over-HTTPS | Done for the HTTP fetcher (`Options.DoH`); live test resolved and fetched example.com. Not for the browser. |
| CDP attach to a remote browser | Done: `stealth.Options.RemoteURL`; tested against a separately launched Chrome |
| Screenshots | Done: `FetchOptions.Screenshot`, CLI `-screenshot`, MCP tool; test checks PNG output |
| Selector auto-generation | Done: `CSSPath()`, `XPathExpr()`; round-trip tested |
| `find_all` attribute filters | Done: `FindAll(FindOptions{...})`; tested |
| robots Request-rate | Done; tested together with Crawl-delay |
| Streaming API | Done: `Engine.Stream`; tested |
| XML export | Done: `NewXMLPipeline`, CLI `-out x.xml`; output parsed back in test |
| Feed spiders | Done: `NewXMLFeedSpider` (RSS, Atom), `NewCSVFeedSpider`; tested |
| MCP server | Done: `gocrapling mcp`, tools fetch/screenshot/capture_requests/crawl; protocol test plus live stdio run |
| Interactive shell | Done: `gocrapling shell` with `curl` conversion (`fetcher.ParseCurl`); tested |
| Docker image | Dockerfile written and the binary cross-compiles for linux/amd64, but the image was **not built**: the Docker daemon was not running |

### Previously unproven, now checked

- **Ad and tracker blocking:** the test exposed a real bug (URL patterns without a port never matched URLs that carry one). Fixed; a test now proves a blocked third-party script does not run while the control run does.
- **Browser routing:** a spider test crawls a JS-rendered page through a real Chrome; `ModeHTTP` sees the static text, `ModeBrowser` the rendered text.
- **Turnstile on real Cloudflare:** passes Cloudflare's interactive test widget on 2captcha's demo page (token returned, headless). Two bugs surfaced and were fixed: the widget rectangle was measured mid smooth-scroll, so the click landed beside the checkbox; and CDP input reports `screenX/Y` equal to `clientX/Y`, now offset in the stealth script. See `examples/turnstile`.
- **Spider domain filter:** `AllowedDomains` entries with a port (`host:1234`) never matched; now tolerated.

## Still unverified or missing

- Docker image build (no running daemon in this session).
- Turnstile against production sites and Cloudflare managed interstitials; the test widget is Cloudflare's own dummy site key.
- DoH for the browser (Chrome resolves DNS itself).
- Markdown on very large real pages beyond go.dev (Wikipedia rate-limited the check).

## Limits and decisions

- **Interactive CAPTCHAs** (image, hCaptcha, reCAPTCHA) are not solved. Plug a solving service into `Middleware.OnResponse` and return `ErrRetry`. The Turnstile click is best effort.
- **Fingerprint age:** profiles are Chrome 133, Firefox 135, Safari 16 from `bogdanfinn/tls-client`. Refresh the dependency and UA strings together, or add identities with `fetcher.RegisterProfile`.
- **IP reputation** often beats fingerprints; datacenter IPs get blocked regardless.
- **Clearance cookies** are normally bound to User-Agent and IP. Reuse them only with the same proxy and UA.
- **Pins:** `chromedp` v0.14.2 with a matching `cdproto`, because v0.21 has a different generic API. `go.mod` says `go 1.27`.

## Gaps found in D4Vinci/Scrapling's README (2026-10-21): all built

Every row below has code and a test, except where noted.

| Area | Built | Notes |
| --- | --- | --- |
| Browser | `DisableResources`, `NetworkIdle`/`WaitIdle`, browser `SpoofReferer`, `StateFile`/`SaveState`/`LoadState`, JSON `Step` actions, `browserinstall` | Chrome tests; resolve test live |
| Parser | `MarkdownOptions.Safe`, `SafeText`, `CleanInvisible`, `AllText`, `URLJoin`, `Below`, `FindOptions.Filter`, `Response.JSON` | unit tests |
| Spider | `Sessions`/`Request.Session`, `Request.Proxy`, `Spider.IsBlocked`, `Retry-After`, `RestrictXPath`, `Canonicalize`, `JSONPipeline`, `ShopifySpider`, `ContentSpider`, `MarkdownDirPipeline` | race-tested with httptest |
| CLI / MCP | `install`, `extract`, `fetch -wait-selector/-steps/-ai-targeted`, browser flags, MCP HTTP + bearer auth, MCP `ai_targeted/actions/wait_selector/network_idle/state_file` | httptest; HTTP auth test |
| Packaging | `skills/gocrapling/SKILL.md`, `.github/workflows/docker.yml` | workflow not run; skill not loaded in an agent |

Not applicable: Scrapy `scrapling_response` decorator, type-checker claims, async classes (goroutines), fast JSON. Benchmarks could be added as Go benchmarks.

### Still unverified after this pass

- `gocrapling install`: ran for real (Chrome 156.0.8078.47, mac-arm64 installed to the user cache). `fetch -browser -steps` and `-wait-selector` then ran through it against example.com.
- Docker image: built and run (linux/arm64, 2026-10-21): HTTP fetch, `-browser` fetch as root (auto `--no-sandbox`) and MCP over stdio all work in the container. Only the GitHub Actions publish workflow (and the amd64 build) is not run.
- MCP over HTTP verified live with real Chrome: 401 without token, `wait_selector` + `actions` (click, wait_for) + `ai_targeted` + `state_file` (0600 file written), bad state name rejected.
- `MCP disable_resources` is a server start flag (`-disable-resources`), not a per-call argument, because it is a browser-wide setting.
