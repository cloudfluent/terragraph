GOLANGCI_LINT_VERSION := v2.13.2
BIN := $(CURDIR)/bin

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z0-9_-]+:.*##/ {printf "  %-16s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: build
build: ## Build the terragraph binary into ./terragraph
	go build -o terragraph ./cmd/terragraph

.PHONY: install
install: ## go install terragraph onto $$GOBIN/$$GOPATH/bin
	go install ./cmd/terragraph

.PHONY: test
test: ## Run the test suite (race detector on)
	go test ./... -race

.PHONY: fmt
fmt: $(BIN)/golangci-lint ## Reformat the codebase in place
	$(BIN)/golangci-lint fmt

.PHONY: fmt-check
fmt-check: $(BIN)/golangci-lint ## Fail if anything isn't formatted (what CI runs)
	$(BIN)/golangci-lint fmt --diff

.PHONY: lint
lint: $(BIN)/golangci-lint ## Run golangci-lint
	$(BIN)/golangci-lint run ./...

.PHONY: docs
docs: ## Regenerate docs/cli/*.md from the live cobra command tree
	go run ./tools/gendocs

.PHONY: docs-check
docs-check: docs ## Fail if docs/cli/*.md is stale (what CI runs)
	git diff --exit-code -- docs/cli || \
	  (echo "docs/cli/*.md is stale, run 'make docs' and commit the result" >&2; exit 1)

.PHONY: check
check: fmt-check lint docs-check build test ## Everything CI runs, in the same order

.PHONY: release-check
release-check: ## Validate .goreleaser.yaml (requires goreleaser on PATH)
	goreleaser check

.PHONY: release-dry-run
release-dry-run: ## Build every release target locally without publishing (requires goreleaser on PATH)
	goreleaser release --snapshot --clean

.PHONY: clean
clean: ## Remove build output and installed tools
	rm -f terragraph
	rm -rf $(BIN) dist/

$(BIN)/golangci-lint:
	GOBIN=$(BIN) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
