.PHONY: dev dev-build down logs fmt vet shell db up up-build

# ─── Development ──────────────────────────────────────────────────────────────

dev: ## Start dev server with hot reload (docker)
	docker compose -f docker/docker-compose.dev.yml up

dev-build: ## Rebuild and start dev server
	docker compose -f docker/docker-compose.dev.yml up --build

dev-down: ## Stop dev server
	docker compose -f docker/docker-compose.dev.yml down

dev-logs: ## Tail dev logs
	docker compose -f docker/docker-compose.dev.yml logs -f

# Run directly on host (requires gcc + air installed: go install github.com/air-verse/air@latest)
dev-local: ## Hot reload directly on host (no Docker)
	cd app && DB_PATH=$(CURDIR)/data/db.sqlite air -c .air.toml

# ─── Production ───────────────────────────────────────────────────────────────

up: ## Start all production services (detached)
	docker compose -f docker/docker-compose.yml up -d

up-build: ## Rebuild and start production services
	docker compose -f docker/docker-compose.yml up -d --build

down: ## Stop all production services
	docker compose -f docker/docker-compose.yml down

logs: ## Tail production logs
	docker compose -f docker/docker-compose.yml logs -f

# ─── Code Quality ─────────────────────────────────────────────────────────────

fmt: ## Format all Go code and fix import order (goimports)
	find app -name '*.go' -not -path '*/vendor/*' | xargs goimports -w -local clearoutspaces

lint: ## Run golangci-lint
	cd app && golangci-lint run ./...

lint-fix: ## Run golangci-lint and auto-fix where possible
	cd app && golangci-lint run --fix ./...

vet: ## Run go vet
	cd app && go vet ./...

tidy: ## Tidy go modules
	cd app && go mod tidy

test: ## Run all unit tests
	cd app && go test ./... -v -count=1

test-db: ## Run only database tests
	cd app && go test ./internal/database/... -v -count=1

test-handlers: ## Run only handler tests
	cd app && go test ./internal/handlers/... -v -count=1

smoke: ## Run live smoke tests against the running local server (source .env first)
	cd app && go run ./cmd/smoketest/main.go

# ─── Utilities ────────────────────────────────────────────────────────────────

shell: ## Open a shell inside the running dev container
	docker exec -it clearoutspaces_api_dev /bin/sh

db: ## Open sqlite3 shell against the dev database
	sqlite3 data/db.sqlite

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'
