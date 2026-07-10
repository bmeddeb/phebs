.PHONY: dev test lint ui db-server

bin/zoekt-git-index: go.mod go.sum ## index builder, same module SHA as the server (PLAN §1.1)
	go build -o $@ github.com/sourcegraph/zoekt/cmd/zoekt-git-index

dev: bin/zoekt-git-index ## boot phebs — zero external services (ARGS="-config phebs.yaml" for flags)
	PHEBS_ZOEKT_GIT_INDEX=$(abspath bin/zoekt-git-index) go run ./cmd/phebs serve $(ARGS)

test:
	go test ./...

lint:
	golangci-lint run

ui: ## production UI build into ui/dist
	cd ui && npm ci && npm run build

db-server: ## SurrealDB server mode via compose — server-mode testing only (PLAN §1)
	docker compose up -d
