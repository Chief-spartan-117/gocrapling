# Security

## Reporting a vulnerability

Please do not open a public issue. Report privately through GitHub: **Security** tab, **Report a vulnerability**, on `Chief-spartan-117/gocrapling`. Include the version, a minimal reproduction and the impact. Expect an answer within a week.

## Threat model

gocrapling fetches URLs and parses hostile content. Treat both as untrusted.

| Threat | Defence | Where it applies |
| --- | --- | --- |
| **SSRF**: an agent or user-supplied URL makes the machine fetch internal services or cloud metadata (`169.254.169.254`) | `netguard`: refuses loopback, RFC 1918, link-local, CGNAT, multicast, reserved and NAT64/6to4-embedded private ranges. HTTP fetcher checks at connect time (covers redirects and DNS rebinding). Browser checks the requested URL only | **On by default in the MCP server** (`-allow-private` turns it off). Opt-in elsewhere: `fetcher.Options.BlockPrivate`, `stealth.Options.BlockPrivate`, CLI `-block-private` |
| **Local file read** through Chrome (`file://`, `chrome://`, `javascript:`, `data:`) | Only `http` and `https` URLs are accepted by the browser and the fetcher | Always |
| **Hostile web page speaks to the MCP server** (cross-origin POST, DNS rebinding) | Without a token: requests with an `Origin` header or a non-loopback `Host` are refused; `Content-Type: application/json` is required. With a token: bearer auth, constant-time compare | `mcp -http` |
| **Unauthenticated network exposure** | A non-loopback `-http` bind without a token is refused | `mcp -http` |
| **Prompt injection** from hidden page text | `ai_targeted` / `MarkdownOptions{Safe: true}` removes hidden elements and invisible Unicode | Opt-in |
| **Path traversal** through tool arguments | `state_file` accepts a plain name only; Markdown corpus paths are sanitised; both tested | MCP, `MarkdownDirPipeline` |
| **Malicious Chrome download** | HTTPS from `storage.googleapis.com` or `edgedl.me.gvt1.com` only; 1 GiB download and 3 GiB / 20,000-entry extraction caps; zip-slip and escaping-symlink checks | `gocrapling install` |
| **CSV injection** (`=HYPERLINK(...)` in scraped text) | Cells starting with `= + - @` are prefixed with `'` (plain numbers are kept) | `CSVPipeline` |
| **Secrets at rest** | Saved logins, checkpoints, response cache and adaptive signatures are written `0600` (`0700` directories) | Always |
| **Known CVEs** | `govulncheck ./...` reports no reachable vulnerability; the toolchain is pinned to a patched Go release | CI should run it |

## Limits you must know

- **Browser SSRF is partial.** Chrome resolves and connects itself, so redirects and sub-resource requests are not checked. When URLs come from an untrusted party, also apply a network egress policy (container network without a route to internal ranges).
- **Proxied HTTP requests** are checked on the first URL only; the proxy resolves later hops.
- **The Docker image runs Chrome without its sandbox** (`GOCRAPLING_NO_SANDBOX=1`) as an unprivileged user, because containers usually cannot create user namespaces. Do not browse hostile sites in it with host mounts or elevated privileges.
- **Run `mcp -http` behind TLS** (a reverse proxy) when it leaves the machine; the bearer token travels in clear text otherwise. Prefer `GOCRAPLING_MCP_TOKEN` over `-token`, which is visible in process listings.
- **`-proxy user:pass@host`** is visible in process listings. Prefer a local proxy that holds the credentials.
- **The MCP `actions` argument can run JavaScript** (`evaluate`) inside the page being scraped. That is inside the page's origin only, but it is code supplied by the caller.
- `golang.org/x/crypto` has an advisory (GO-2026-5932) with no fix yet; no vulnerable function is reachable from this code.
- Disabling certificate checks (`InsecureSkipVerify`) is available as a library option and never enabled by the CLI.
