.PHONY: dev test lint ui db-server

dev: ## boot phebs — zero external services (ARGS="-config phebs.yaml" for flags)
	go run ./cmd/phebs serve $(ARGS)

test:
	go test ./...

lint:
	golangci-lint run

ui: ## production UI build into ui/dist
	cd ui && npm ci && npm run build

db-server: ## SurrealDB server mode via compose — server-mode testing only (PLAN §1)
	docker compose up -d
