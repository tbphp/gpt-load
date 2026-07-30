.DEFAULT_GOAL := help

APP := gpt-load
WEB_DIR := web
GO ?= go
PNPM ?= corepack pnpm

.PHONY: _web-deps
_web-deps:
	$(PNPM) --dir $(WEB_DIR) install --frozen-lockfile

.PHONY: _web-build
_web-build: _web-deps
	$(PNPM) --dir $(WEB_DIR) run build

.PHONY: dev
dev: _web-build ## Build the Web UI and run with race detection
	$(GO) run -race .

.PHONY: run
run: _web-build ## Build the Web UI and run the application
	$(GO) run .

.PHONY: build
build: _web-build ## Build the Web UI and application binary
	$(GO) build -o $(APP) .

.PHONY: test
test: ## Run Go unit tests
	$(GO) test -count=1 . ./internal/...

.PHONY: check
check: _web-deps ## Run source checks and build
	@formatted_files="$$(gofmt -l .)"; test -z "$${formatted_files}"
	$(GO) mod tidy -diff
	$(GO) vet ./...
	$(PNPM) --dir $(WEB_DIR) run lint
	$(PNPM) --dir $(WEB_DIR) run format
	$(PNPM) --dir $(WEB_DIR) run type-check
	$(PNPM) --dir $(WEB_DIR) run build
	$(GO) build -o $(APP) .
	$(GO) test -race -count=1 . ./internal/...
	git diff --check

.PHONY: help
help: ## Display available targets
	@awk 'BEGIN {FS = ":.*?## "; printf "Usage:\n  make \033[36m<target>\033[0m\n\nTargets:\n"} /^[a-zA-Z0-9_-]+:.*?## / { printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)
