.PHONY: dev dev-api build test ui-test lint ui db-server

bin/zoekt-git-index: go.mod go.sum ## index builder, same module SHA as the server (PLAN §1.1)
	go build -o $@ github.com/sourcegraph/zoekt/cmd/zoekt-git-index

dev: bin/zoekt-git-index ui ## boot phebs with embedded UI (ARGS="-config phebs.yaml" for flags)
	PHEBS_ZOEKT_GIT_INDEX=$(abspath bin/zoekt-git-index) go run -tags ui ./cmd/phebs serve $(ARGS)

dev-api: bin/zoekt-git-index ## backend-only loop: no UI build, placeholder page
	PHEBS_ZOEKT_GIT_INDEX=$(abspath bin/zoekt-git-index) go run ./cmd/phebs serve $(ARGS)

build: bin/zoekt-git-index ui ## release binary with embedded UI
	go build -tags ui -o phebs ./cmd/phebs

test:
	go test ./...

ui-test: ## Vitest UI tests (T6.4)
	cd ui && npm test

lint:
	golangci-lint run

ui: ## production UI build into ui/dist
	cd ui && npm ci && npm run build

db-server: ## SurrealDB server mode via compose — server-mode testing only (PLAN §1)
	docker compose up -d
