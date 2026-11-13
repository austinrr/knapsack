# Thin wrappers around the go toolchain. CI runs the same commands.

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

.PHONY: all build test lint fmt vuln tidy

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

tidy: ## Tidy go.mod and fail if it changed
	go mod tidy
	git diff --exit-code go.mod go.sum
