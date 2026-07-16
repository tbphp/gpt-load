# Default target
.DEFAULT_GOAL := help

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
