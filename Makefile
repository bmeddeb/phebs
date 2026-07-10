.PHONY: dev test lint ui db-server

dev: ## boot the phebs skeleton — embedded storage, zero external services
	go run ./cmd/phebs serve

test:
	go test ./...

lint:
	golangci-lint run

ui: ## production UI build into ui/dist
	cd ui && npm ci && npm run build

db-server: ## SurrealDB server mode via compose — server-mode testing only (PLAN §1)
	docker compose up -d
