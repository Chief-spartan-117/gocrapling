# Changelog

All notable changes are recorded here. Entries after 0.1.0 are generated from Conventional Commits by release-please; the project uses [Semantic Versioning](https://semver.org/).

## [0.2.0](https://github.com/Chief-spartan-117/gocrapling/compare/gocrapling-v0.1.0...gocrapling-v0.2.0) (2026-10-21)


### ⚠ BREAKING CHANGES

* restructure into pkg, internal, api and sdk layers
* **fetcher:** refuse non-web schemes and add private-address guard

### Features

* **api:** add rest api with openapi spec and serve command ([f12b89e](https://github.com/Chief-spartan-117/gocrapling/commit/f12b89e5a7da62939002b973067a0863b18cfa2f))
* **clients:** add python and javascript clients ([a54fc04](https://github.com/Chief-spartan-117/gocrapling/commit/a54fc043621179365e4350ac505d0adebf3f4359))
* **docker:** add compose file and health endpoint ([89dbb54](https://github.com/Chief-spartan-117/gocrapling/commit/89dbb54179d58c5be95d554664f51e7d8cd016d0))
* **n8n:** add n8n guide, compose stack and workflows ([460a094](https://github.com/Chief-spartan-117/gocrapling/commit/460a0943f49f3ef5cfc8e46d7168000ffd980cc6))


### Bug Fixes

* **browserinstall:** restrict download host, sizes and symlinks ([a5873f7](https://github.com/Chief-spartan-117/gocrapling/commit/a5873f78b84b4b79f6a80a53105d0b7aad7bf53f))
* **fetcher:** refuse non-web schemes and add private-address guard ([10b027c](https://github.com/Chief-spartan-117/gocrapling/commit/10b027ca3d7ca0bb5e92ddfd1b5c9b725f32b4fc))
* **mcp:** block private targets by default and harden http transport ([a3284fa](https://github.com/Chief-spartan-117/gocrapling/commit/a3284fa2b7138d3412e8e8143889a6e8087b0eed))
* **spider:** neutralize csv formulas and tighten file modes ([e19b9f1](https://github.com/Chief-spartan-117/gocrapling/commit/e19b9f1dc1493ef5ff269d21c7b3f0edb6c3a186))
* **stealth:** accept only http and https urls in browser fetches ([ba8f832](https://github.com/Chief-spartan-117/gocrapling/commit/ba8f832b43d2dcf31be6ba5c8b6d6e3efe777d51))


### Refactoring

* restructure into pkg, internal, api and sdk layers ([1a32441](https://github.com/Chief-spartan-117/gocrapling/commit/1a32441477f3239af121279c9b7ea3f6d809b99d))


### Documentation

* add architecture guide, makefile and contribution templates ([91640f1](https://github.com/Chief-spartan-117/gocrapling/commit/91640f18c7390fcabb6442590ec326475da3b8e0))
* add security policy and changelog entries ([9557e9a](https://github.com/Chief-spartan-117/gocrapling/commit/9557e9a7f9b8c25935a4527c55393f75b5998ebc))
* document rest api and clients ([15ce560](https://github.com/Chief-spartan-117/gocrapling/commit/15ce5607512881142c90a5ed6e1823d21a6ed6c3))


### Build

* **deps:** Bump github.com/andybalholm/cascadia from 1.3.4 to 1.3.5 ([c704c1f](https://github.com/Chief-spartan-117/gocrapling/commit/c704c1fdf38e81cf7512b1d2737f0b053f060b12))
* **deps:** Bump github.com/andybalholm/cascadia from 1.3.4 to 1.3.5 ([fd32cd8](https://github.com/Chief-spartan-117/gocrapling/commit/fd32cd825106cd202a2e6fd7ef08e0e1704e6f0f))
* pin patched go toolchain and run docker image as non-root ([63cc421](https://github.com/Chief-spartan-117/gocrapling/commit/63cc421ae453e97cceb9caed9aec86ba92161eb6))

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

[0.1.0]: https://github.com/Chief-spartan-117/gocrapling/releases/tag/v0.1.0
