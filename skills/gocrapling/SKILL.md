---
name: gocrapling
description: Scrape web pages without getting blocked using the gocrapling Go toolkit (browser-grade TLS fetcher, stealth Chrome, adaptive parser, spider framework). Use when asked to fetch, extract, crawl or screenshot sites, get a page as Markdown, find the API behind a JS app, or keep a selector working after a redesign.
---

# gocrapling

Go module `github.com/sagarshrestha/gocrapling`. CLI binary: `gocrapling`.

## Pick the tool

1. Plain page, static HTML: `gocrapling fetch URL` (HTTP, browser TLS fingerprint, fast).
2. Blocked (403/429/challenge page) or JS-rendered: add `-browser`.
3. JS app that loads data from an API: `gocrapling fetch -record 'api/' URL` returns the JSON behind it. Prefer calling that API directly afterwards.
4. Many pages: `gocrapling crawl` or a `spider.Spider` in Go.
5. Page into a file for a model or a corpus: `gocrapling extract URL page.md`.

## CLI

```sh
gocrapling fetch -css 'h2 a::attr(href)' URL        # ::text and ::attr(name) supported
gocrapling fetch -markdown -main URL                 # Markdown, main content only
gocrapling fetch -browser -wait-selector '.price' -css '.price::text' URL
gocrapling fetch -browser -steps steps.json -state-file login URL
gocrapling extract -ai-targeted URL page.md          # .md, .txt or .html by extension
gocrapling crawl -field 'title=h1::text' -depth 2 -out items.jsonl URL
gocrapling install                                   # download a managed Chrome
gocrapling mcp                                       # MCP server on stdio
gocrapling mcp -http 127.0.0.1:8080 -token "$T"      # MCP over HTTP, bearer auth
```

Steps file (JSON array): `{"type":"click","selector":"#login"}`, `fill`, `press`, `wait`, `wait_for`, `scroll`, `evaluate`.

Use `-ai-targeted` (CLI) or `ai_targeted` (MCP) on untrusted pages. It strips hidden elements and invisible characters, so hidden prompt-injection text never reaches the model.

## Library

```go
f := fetcher.New(fetcher.Options{Profile: "chrome", Proxies: fetcher.NewPoolRotator(proxies, false)})
defer f.Close()
resp, _ := f.Get(ctx, url)
doc, _ := resp.Parse()
titles := doc.Extract("h2.title::text")
md := doc.MarkdownWith(parser.MarkdownOptions{MainOnly: true, Safe: true})
```

Adaptive selectors survive redesigns: save with `CSSWith(q, QueryOptions{AutoSave: true, Identifier: "price"})`, later `Adaptive: true`.

Spiders: `spider.Spider{StartURLs, Callbacks}` plus `spider.Config`. Helpers: `NewCrawlSpider`, `NewSitemapSpider`, `NewShopifySpider` (one item per variant), `NewContentSpider` + `NewMarkdownDirPipeline` (site to Markdown files). Per-request `Session` and `Proxy`, `Spider.IsBlocked` for custom block detection, `Config.AutoThrottle` honours `Retry-After`.

## Rules

- Respect robots.txt and each site's terms. `crawl` obeys robots by default.
- Image, hCaptcha and reCAPTCHA challenges are not solved. Turnstile click is best effort.
- Datacenter IPs are often blocked whatever the fingerprint. Use residential proxies for hard targets.
- Never put secrets in `-proxy` URLs shared in logs.
