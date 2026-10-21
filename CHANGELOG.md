# Changelog

All notable changes are recorded here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Security

- **netguard:** new package that refuses non-public addresses; `fetcher.Options.BlockPrivate`, `stealth.Options.BlockPrivate`, CLI `-block-private`. The MCP server enables it by default (`-allow-private` to opt out).
- **stealth, fetcher:** only `http` and `https` URLs are accepted. Before this, MCP `fetch` with `mode: browser` and `screenshot` could read local files through `file://`.
- **mcp:** HTTP transport without a token rejects `Origin` headers and non-loopback `Host` values (cross-origin and DNS-rebinding attacks) and requires `Content-Type: application/json`; server read timeouts added.
- **browserinstall:** download host allow-list, size and entry caps, escaping-symlink rejection.
- **spider:** CSV cells that start with `= + - @` are neutralised against spreadsheet formula injection; checkpoint, cache and adaptive-signature files are written `0600`.
- **docker:** image runs as an unprivileged user; Go toolchain pinned to 1.27.3, which fixes ten standard-library advisories reported by `govulncheck`.
- Add `SECURITY.md` with the threat model and reporting process.

## [0.1.0] - 2026-10-21

First release: a Go port of the ideas in [Scrapling](https://github.com/D4Vinci/Scrapling).

### Added

- **fetcher:** browser-accurate TLS (JA3/JA4) and HTTP/2 fingerprints through `bogdanfinn/tls-client`; Chrome, Firefox and Safari profiles and `RegisterProfile`; proxy rotation with cooldown; retries with backoff and `Retry-After`; block detection; Google referer; pinned-identity `Session`; HTTP/3 racing; DNS-over-HTTPS; `ParseCurl`; `Response.JSON`.
- **stealth:** Chrome through DevTools with automation tells patched; proxy auth; human-like mouse; Cloudflare interstitial wait and Turnstile click; XHR/fetch capture; ad and tracker blocking; resource blocking; network-idle wait; Google referer; screenshots; remote Chrome attach; saved login state; page actions as JSON steps.
- **browserinstall:** Chrome for Testing downloader (`gocrapling install`).
- **parser:** CSS with `::text` and `::attr()`, XPath, text and regex search, `FindSimilar`, adaptive relocation with memory and file storage, `FindAll` filters, selector generation, `AllText`, `URLJoin`, `Below`, Markdown output, and safe Markdown that strips hidden content against prompt injection.
- **spider:** priority scheduler, de-duplication, per-host limits, robots.txt (wildcards, Crawl-delay, Request-rate), checkpoint pause and resume, middleware hooks, per-host HTTP or browser routes, escalation on block, AutoThrottle with `Retry-After`, response cache, `LinkExtractor` (XPath scope, canonical URLs), `CrawlSpider`, `SitemapSpider`, feed spiders, `ShopifySpider`, `ContentSpider`, named sessions, per-request proxy, custom block detector, streaming; JSONL, JSON, CSV, XML and Markdown-directory pipelines.
- **mcp:** Model Context Protocol server over stdio and over HTTP with bearer auth; tools `fetch`, `screenshot`, `capture_requests`, `crawl`.
- **cli:** `fetch`, `extract`, `crawl`, `shell`, `mcp`, `install`, `fingerprint`.
- Dockerfile with Chromium, a multi-arch image workflow, and a Claude Code skill (`skills/gocrapling`).

### Known limits

- Interactive CAPTCHAs are not solved; the Turnstile click is best effort.
- No SSRF guard: the fetcher will connect to private addresses. Do not expose `mcp -http` to untrusted callers.
- Profiles are Chrome 133, Firefox 135 and Safari 16.

[Unreleased]: https://github.com/Chief-spartan-117/gocrapling/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/Chief-spartan-117/gocrapling/releases/tag/v0.1.0
