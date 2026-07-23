# Default target
.DEFAULT_GOAL := help

WEB_DIR := web
PNPM ?= corepack pnpm

# ==============================================================================
# Build & Web UI
# ==============================================================================
.PHONY: web-install
web-install: ## Install frozen web dependencies
	$(PNPM) --dir $(WEB_DIR) install --frozen-lockfile

.PHONY: web-lint
web-lint: web-install ## Lint web source
	$(PNPM) --dir $(WEB_DIR) run lint

.PHONY: web-format
web-format: web-install ## Check web formatting
	$(PNPM) --dir $(WEB_DIR) run format

.PHONY: web-type-check
web-type-check: web-install ## Type-check web source
	$(PNPM) --dir $(WEB_DIR) run type-check

.PHONY: web-test
web-test: web-install ## Run web unit tests
	$(PNPM) --dir $(WEB_DIR) run test

.PHONY: web-build
web-build: web-install ## Build embedded web assets
	$(PNPM) --dir $(WEB_DIR) run build

.PHONY: web-check
web-check: web-lint web-format web-type-check web-test web-build ## Run all web quality gates

.PHONY: build
build: web-build ## Build the single GPT-Load binary
	go build -o gpt-load .

# ==============================================================================
# Run & Development
# ==============================================================================
.PHONY: run
run: ## Run server
	@echo "--- Starting backend... ---"
	go run .

.PHONY: dev
dev: ## Run in development mode (with race detection)
	@echo "🔧 Starting development mode..."
	go run -race .

# ==============================================================================
# Deferred Key Migration
# ==============================================================================
.PHONY: migrate-keys
migrate-keys: ## Reserved for the 2.0 key rotation tool
	@echo "migrate-keys will be available in a later release"
	@exit 1

.PHONY: help
help: ## Display this help message
	@awk 'BEGIN {FS = ":.*?## "; printf "Usage:\n  make \033[36m<target>\033[0m\n\nTargets:\n"} /^[a-zA-Z0-9_-]+:.*?## / { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)
