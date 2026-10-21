.PHONY: build test test-short lint vet fmt vuln run docker clean

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

build: ## Build bin/gocrapling
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/gocrapling ./cmd/gocrapling

test: ## Full test suite with the race detector (needs Chrome and network)
	go test -race -count=1 ./...

test-short: ## Skip tests that need Chrome or the public internet
	go test -race -short -count=1 ./...

vet:
	go vet ./...

fmt: ## Fail if any file needs gofmt
	@test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)

lint: fmt vet

vuln: ## Report reachable vulnerabilities
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

run: build ## Start the REST API + MCP server on 127.0.0.1:8080
	GOCRAPLING_MCP_TOKEN=$${GOCRAPLING_MCP_TOKEN:-dev} bin/gocrapling mcp -http 127.0.0.1:8080

docker: ## Build the image
	docker build -t gocrapling:local --build-arg VERSION=$(VERSION) .

clean:
	rm -rf bin
