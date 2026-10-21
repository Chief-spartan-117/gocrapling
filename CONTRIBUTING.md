# Contributing to gocrapling

Thanks for helping. This file covers setup, checks and commit style.

## Setup

```sh
git clone git@github.com:Chief-spartan-117/gocrapling.git
cd gocrapling
go build ./...
```

The Go version in `go.mod` or newer is needed. Browser features need Chrome: install one, or run `go run ./cmd/gocrapling install` to download Chrome for Testing.

## Project layout

Read [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) first; it explains the layers. In short: libraries in `pkg/`, server internals in `internal/`, the REST contract in `api/`, clients in `sdk/`. New capabilities go into `internal/service`, then get exposed by `internal/mcp` and `internal/rest`.

## Before you open a pull request

```sh
make lint          # gofmt + go vet
make test-short    # skips tests that need Chrome or the internet
make test          # everything (needs Chrome and network)
make vuln          # govulncheck
```

- Tests that need Chrome or the public internet skip with `go test -short ./...`. Run the full suite before a release.
- Add a test with every behaviour change. Prefer `httptest` servers over live sites. Live-network tests belong in `*_live_test.go` files and must skip when offline.
- Keep dependency pins: `chromedp` v0.14.2 with a matching `cdproto`. Version 0.21 has an incompatible API.
- Do not add a fetch path that skips the response-size cap or the robots.txt checks in the spider.

## Commit messages

This project uses [Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<scope>): <summary in the imperative, lower case, no period>

<optional body: what and why>
```

Types: `feat`, `fix`, `docs`, `test`, `refactor`, `perf`, `build`, `ci`, `chore`.
Scopes: `fetcher`, `stealth`, `parser`, `spider`, `service`, `mcp`, `rest`, `server`, `cli`, `api`, `sdk`, `browserinstall`, `docker`, `skill`, `deps`, `n8n`.
Mark breaking changes with `!` after the scope (`feat(spider)!: ...`) and a `BREAKING CHANGE:` footer.

Examples:

```
feat(parser): strip hidden content in safe Markdown
fix(stealth): measure the Turnstile widget after scrolling settles
docs: describe MCP over HTTP
```

Each pull request should be one logical change. **The PR title must be a Conventional Commit** (a CI check enforces it) because pull requests are squash-merged and the title becomes the commit message.

Do not edit `CHANGELOG.md` or version numbers by hand; releases generate them.

## Releases (maintainers)

Releases are automated with [release-please](https://github.com/googleapis/release-please):

1. Merging to `main` updates a **release PR** that bumps the version and `CHANGELOG.md` from the commits since the last release. `feat` bumps the minor version, `fix` and `perf` the patch version, and a `!` or `BREAKING CHANGE:` bumps the minor version while we are below 1.0.0 (major after).
2. Merging that PR creates the tag (`vX.Y.Z`) and the GitHub Release.
3. The same workflow then attaches binaries for Linux, macOS and Windows (amd64 and arm64, with checksums) and publishes the Docker image to `ghcr.io/chief-spartan-117/gocrapling`.

One-time repository setting: **Settings, Actions, General, "Allow GitHub Actions to create and approve pull requests"**. Without it, add a personal access token as the `RELEASE_PLEASE_TOKEN` secret.

The Python and JavaScript client versions follow the same number automatically; publishing them to PyPI and npm is not automated yet.

## Reporting problems

Open an issue with the command you ran, the output, your OS and whether Chrome was involved. For a site that blocks you, say which layer failed (TLS fingerprint, browser, IP reputation) if you know.

## Responsible use

gocrapling does not solve interactive CAPTCHAs and is not meant to defeat access controls. Respect each site's terms and robots.txt. Contributions that add credential theft, mass-targeting or denial-of-service features will not be accepted.
