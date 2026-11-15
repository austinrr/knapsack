# Thin wrappers around the go toolchain. CI runs the same commands.

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.buildVersion=$(VERSION)

# goreleaser is pinned in its own module, tools/go.mod, so its hundreds of
# dependencies and newer Go requirement stay out of Knapsack's go.mod.
# `go tool -n` builds it (cached) and prints its path; -C makes Go use the
# toolchain tools/go.mod asks for. It runs in the recipe, not in $(shell),
# so a failed build stops make with Go's own error.
GORELEASER = goreleaser=$$(go -C tools tool -n goreleaser) && "$$goreleaser"

# golangci-lint is pinned the same way, in tools/lint/go.mod: a module of
# its own, so its dependencies are the versions its release was built
# with, not ones goreleaser's would raise. It's built for this machine
# (GOOS and GOARCH cleared), so GOOS=windows make lint runs it here and
# lints Windows' files, as CI does.
GOLANGCI_LINT = golangci_lint=$$(GOOS= GOARCH= go -C tools/lint tool -n golangci-lint) && "$$golangci_lint"

.PHONY: all build test lint fmt vuln tidy snapshot release-check

all: lint test build

build: ## Build bin/knapsack
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/knapsack ./cmd/knapsack

test: ## Run tests with the race detector
	go test -race ./...

lint: ## Run golangci-lint, the version pinned in tools/lint/go.mod
	$(GOLANGCI_LINT) run

fmt: ## Format code with the linter's formatters
	$(GOLANGCI_LINT) fmt

vuln: ## Scan dependencies for known vulnerabilities
	go tool govulncheck ./...

tidy: ## Tidy every go.mod and fail if any changed
	go mod tidy
	go -C tools mod tidy
	go -C tools/lint mod tidy
	git diff --exit-code go.mod go.sum tools/go.mod tools/go.sum tools/lint/go.mod tools/lint/go.sum

snapshot: ## Build every release archive into dist/ without publishing
	$(GORELEASER) release --snapshot --clean

release-check: ## Validate .goreleaser.yaml
	$(GORELEASER) check
