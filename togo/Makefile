# Developer tasks for the Go build. `make help` lists targets.
GOBIN := $(shell go env GOPATH)/bin
LDFLAGS := -s -w

.PHONY: help fmt fmt-check lint vet test build build-all clean tools

help: ## List targets
	@grep -E '^[a-z-]+:.*##' $(MAKEFILE_LIST) | sed 's/:.*##/\t/'

fmt: ## Format with gofmt
	gofmt -w .

fmt-check: ## Fail if anything is unformatted (CI)
	@test -z "$$(gofmt -l .)" || { echo "unformatted files:"; gofmt -l .; exit 1; }

lint: ## Run golangci-lint
	$(GOBIN)/golangci-lint run

vet: ## Run go vet
	go vet ./...

test: ## Run all tests
	go test ./...

build: ## Build a static binary for the host
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/cappu ./cmd/cappu

# Statically linked binaries for every release target (no libc dependency).
build-all: ## Cross-build static binaries for all release targets
	CGO_ENABLED=0 GOOS=linux  GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/cappu-linux-x64    ./cmd/cappu
	CGO_ENABLED=0 GOOS=linux  GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/cappu-linux-arm64  ./cmd/cappu
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/cappu-darwin-arm64 ./cmd/cappu
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/cappu-win-x64.exe ./cmd/cappu

clean: ## Remove build output
	rm -rf dist

tools: ## Install the dev tools (golangci-lint; gofmt ships with Go)
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
