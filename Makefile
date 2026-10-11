# Self-documenting Makefile: `make` or `make help` lists every target with a `## description`.
.DEFAULT_GOAL := help

# Developer tools are pinned here and installed into ./bin by `make setup`.
GOLANGCI_LINT_VERSION := v2.14.0
GOVULNCHECK_VERSION   := v1.9.0

BIN      := $(CURDIR)/bin
export PATH := $(BIN):$(PATH)
GO       ?= go
# Build tools with the same Go version as the module, so linters understand its code.
TOOLCHAIN := $(shell $(GO) env GOVERSION)
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -s -w -X github.com/panelarr/meta-mangabaka/internal/buildinfo.Version=$(VERSION)
IMAGE    ?= panelarr/meta-mangabaka:dev

.PHONY: help setup build test test-short lint fmt fmt-check vet vuln gen run real-dump docker clean check

help: ## List targets
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z0-9_.-]+:.*## / {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

setup: ## Install pinned developer tools into ./bin and download modules
	@mkdir -p $(BIN)
	GOTOOLCHAIN=$(TOOLCHAIN) GOBIN=$(BIN) $(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	GOTOOLCHAIN=$(TOOLCHAIN) GOBIN=$(BIN) $(GO) install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	$(GO) mod download

build: ## Build the plugin binary into ./bin
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN)/meta-mangabaka ./cmd/meta-mangabaka

test: ## Run all tests with the race detector
	$(GO) test -race -cover ./...

test-short: ## Run the fast tests only
	$(GO) test -short -race ./...

fmt: ## Format Go files
	$(GO) fmt ./...

fmt-check: ## Fail if Go files are not formatted
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "not gofmt-ed:"; echo "$$out"; exit 1; fi

vet: ## go vet
	$(GO) vet ./...

lint: ## golangci-lint
	$(BIN)/golangci-lint run ./...

vuln: ## Check dependencies and the toolchain for known vulnerabilities
	$(BIN)/govulncheck ./...

gen: ## Generate code (nothing generated in this repo yet)
	@true

run: build ## Run the plugin standalone on :8903 and print its URL
	@echo "meta-mangabaka listening on http://localhost:8903"
	$(BIN)/meta-mangabaka --listen :8903

real-dump: build ## Download and read the real MangaBaka dump once into DIR and print statistics (manual, not in CI)
	@test -n "$(DIR)" || (echo "usage: make real-dump DIR=/path/with/space [VARIANT=default]" && exit 2)
	$(BIN)/meta-mangabaka dump --dir "$(DIR)" --variant "$(or $(VARIANT),default)"

docker: ## Build the container image ($(IMAGE))
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE) .

clean: ## Remove build artifacts (keeps installed tools)
	rm -f $(BIN)/meta-mangabaka
	rm -rf dist coverage.out

check: fmt-check vet lint vuln test ## Format check, lint, static analysis, vulnerabilities and tests (run before every report/PR)
