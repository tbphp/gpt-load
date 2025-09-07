# Default target
.DEFAULT_GOAL := help

# ==============================================================================
# Run & Development
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
	@echo "🔧 Starting development mode..."
	go run -race ./main.go

# ==============================================================================
# Key Migration
# ==============================================================================
.PHONY: migrate-keys
migrate-keys: ## Execute key migration (usage: make migrate-keys ARGS="--from old --to new")
	@echo "🔑 Executing key migration..."
	@if [ -z "$(ARGS)" ]; then \
		echo "Usage:"; \
		echo "  Enable encryption: make migrate-keys ARGS=\"--to new-key\""; \
		echo "  Disable encryption: make migrate-keys ARGS=\"--from old-key\""; \
		echo "  Change key: make migrate-keys ARGS=\"--from old-key --to new-key\""; \
		echo ""; \
		echo "⚠️  Important: Always backup database before migration!"; \
		exit 1; \
	fi
	go run ./main.go migrate-keys $(ARGS)

.PHONY: help
help: ## Display this help message
	@awk 'BEGIN {FS = ":.*?## "; printf "Usage:\n  make \033[36m<target>\033[0m\n\nTargets:\n"} /^[a-zA-Z0-9_-]+:.*?## / { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)
