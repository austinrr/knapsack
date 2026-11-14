# Thin wrappers around the go toolchain. CI runs the same commands.

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.buildVersion=$(VERSION)

# goreleaser is pinned in its own module, tools/go.mod, so its hundreds of
# dependencies and newer Go requirement stay out of Knapsack's go.mod.
# `go tool -n` builds it (cached) and prints its path; -C makes Go use the
# toolchain tools/go.mod asks for. It runs in the recipe, not in $(shell),
# so a failed build stops make with Go's own error.
GORELEASER = goreleaser=$$(go -C tools tool -n goreleaser) && "$$goreleaser"

.PHONY: all build test lint fmt vuln tidy snapshot release-check

all: lint test build

build: ## Build bin/knapsack
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/knapsack ./cmd/knapsack

test: ## Run tests with the race detector
	go test -race ./...

lint: ## Run golangci-lint (install: https://golangci-lint.run/docs/welcome/install/)
	golangci-lint run

fmt: ## Format code with the linter's formatters
	golangci-lint fmt

vuln: ## Scan dependencies for known vulnerabilities
	go tool govulncheck ./...

tidy: ## Tidy go.mod and tools/go.mod and fail if either changed
	go mod tidy
	go -C tools mod tidy
	git diff --exit-code go.mod go.sum tools/go.mod tools/go.sum

snapshot: ## Build every release archive into dist/ without publishing
	$(GORELEASER) release --snapshot --clean

release-check: ## Validate .goreleaser.yaml
	$(GORELEASER) check
