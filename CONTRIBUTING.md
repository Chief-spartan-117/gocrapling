# Contributing to gocrapling

Thanks for helping. This file covers setup, checks and commit style.

## Setup

```sh
git clone git@github.com:Chief-spartan-117/gocrapling.git
cd gocrapling
go build ./...
```

The Go version in `go.mod` or newer is needed. Browser features need Chrome: install one, or run `go run ./cmd/gocrapling install` to download Chrome for Testing.

## Before you open a pull request

```sh
gofmt -l .                 # must print nothing
go vet ./...
go test -race -count=1 ./...
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
Scopes: `fetcher`, `stealth`, `parser`, `spider`, `mcp`, `cli`, `browserinstall`, `docker`, `skill`.
Mark breaking changes with `!` after the scope (`feat(spider)!: ...`) and a `BREAKING CHANGE:` footer.

Examples:

```
feat(parser): strip hidden content in safe Markdown
fix(stealth): measure the Turnstile widget after scrolling settles
docs: describe MCP over HTTP
```

Each pull request should be one logical change. Update `CHANGELOG.md` under `Unreleased` for anything a user can see.

## Reporting problems

Open an issue with the command you ran, the output, your OS and whether Chrome was involved. For a site that blocks you, say which layer failed (TLS fingerprint, browser, IP reputation) if you know.

## Responsible use

gocrapling does not solve interactive CAPTCHAs and is not meant to defeat access controls. Respect each site's terms and robots.txt. Contributions that add credential theft, mass-targeting or denial-of-service features will not be accepted.
