# Architecture

gocrapling is one Go module with a layered layout. Public libraries live in `pkg/`, everything that is an implementation detail of the server lives in `internal/`, and each layer only imports the ones below it.

```
cmd/gocrapling         CLI: fetch, extract, crawl, shell, serve, mcp, install, version
        │
        ▼
internal/server        HTTP: auth, /mcp, /v1, /healthz, timeouts
   │         │
   ▼         ▼
internal/mcp    internal/rest        protocol adapters (JSON-RPC, REST); no business logic
        │         │
        ▼         ▼
      internal/service               fetch, screenshot, capture, crawl; options and policy
        │
        ▼
pkg/fetcher   pkg/stealth   pkg/spider   pkg/parser     public libraries
        │          │
        ▼          ▼
internal/netguard  internal/browserinstall               SSRF guard, Chrome downloader
```

| Path | Role | Import it? |
| --- | --- | --- |
| `pkg/fetcher` | HTTP client with browser TLS/HTTP2 fingerprints, proxies, retries, block detection | yes |
| `pkg/stealth` | Chrome driver with automation tells patched, Turnstile click, request capture | yes |
| `pkg/spider` | Crawl framework: scheduler, robots.txt, pipelines, checkpoints | yes |
| `pkg/parser` | CSS/XPath, adaptive selectors, Markdown | yes |
| `internal/service` | The operations every front end shares | no |
| `internal/mcp` | Model Context Protocol adapter | no |
| `internal/rest` | REST adapter | no |
| `internal/server` | HTTP composition and authentication | no |
| `internal/netguard` | Refuses non-public addresses | no |
| `internal/browserinstall` | Downloads Chrome for Testing | no |
| `api/` | OpenAPI contract (embedded) | spec only |
| `sdk/python`, `sdk/js` | Dependency-free clients for the REST API | published separately |
| `examples/` | Runnable programs and the n8n workflows | no |
| `skills/` | Claude Code skill describing the tool | no |
| `docs/` | Guides, this file, development notes | |

## Rules that keep it easy to extend

1. **One implementation, many front ends.** A new capability goes into `internal/service` first. MCP and REST then expose it with a few lines each. Never put scraping logic in an adapter.
2. **The OpenAPI file is the contract.** Change `api/openapi.json` with `internal/rest`, then update `sdk/python` and `sdk/js`.
3. **Security policy lives in the service layer.** `BlockPrivate` (SSRF), the scheme allow-list and path sanitising are enforced there, so no adapter can forget them.
4. **Libraries do not import `internal/`** except for the two leaf helpers above, and never import upward.
5. **Everything stateful is injected.** Downloaders, pipelines and storages are interfaces, so tests use `httptest` servers and fakes.

## Scaling notes

- The service is stateless apart from one shared Chrome and a response-size-bounded fetcher, so you can run N copies behind a load balancer (`docker compose up --scale mcp=N`).
- A crawl's queue and de-duplication are in memory with a checkpoint file. Splitting one crawl over machines would need a shared frontier (queue plus seen-set); `pkg/spider.Downloader`, `Pipeline` and the checkpoint format are the seams where that would plug in.
- Browser fetches cost about 100–300 MB per tab; `stealth.Options.MaxTabs` bounds that.

## Where things are tested

Unit and `httptest` tests sit next to the code. Tests needing Chrome or the public internet skip under `go test -short`. `sdk/*` have live tests that CI runs against a freshly built server.
