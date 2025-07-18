# Default target
.DEFAULT_GOAL := help

# ==============================================================================
# Running and Development
# ==============================================================================
.PHONY: run
run: ## Build frontend and run server
	@echo "--- Building frontend... ---"
	cd web && npm install && npm run build
	@echo "--- Preparing backend... ---"
	@echo "--- Starting backend... ---"
	go run ./main.go

.PHONY: dev
dev: ## Run in development mode (with race detection)
	@echo "🔧 Starting in development mode..."
	go run -race ./main.go

.PHONY: help
help: ## Show this help information
	@awk 'BEGIN {FS = ":.*?## "; printf "Usage:\n  make \033[36m<target>\033[0m\n\nTargets:\n"} /^[a-zA-Z0-9_-]+:.*?## / { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)
