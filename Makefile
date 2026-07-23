VERSION ?= 0.1.0-dev
VERSION_PATTERN := ^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-(0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*))*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$$
GO_VERSION := $(shell tr -d '[:space:]' < .go-version)
NODE_VERSION := $(shell tr -d '[:space:]' < .node-version)
GOLANGCI_LINT_VERSION := $(patsubst v%,%,$(shell tr -d '[:space:]' < .golangci-lint-version))
SURREALDB_VERSION := $(shell tr -d '[:space:]' < .surrealdb-version)

.PHONY: dev dev-api build validate-version test ui-test lint ui db-server \
	verify-go verify-node verify-golangci-lint verify-surreal \
	ci ci-static ci-go ci-race ci-ui

bin:
	mkdir -p $@

bin/zoekt-git-index: go.mod go.sum | bin ## index builder, same module SHA as the server (PLAN §1.1)
	go build -o $@ github.com/sourcegraph/zoekt/cmd/zoekt-git-index

bin/buf: go.mod go.sum | bin ## compatibility child, pinned by the same go.mod as the server (T14.3)
	CGO_ENABLED=0 go build -o $@ github.com/bufbuild/buf/cmd/buf

dev: bin/zoekt-git-index bin/buf ui ## boot phebs with embedded UI (ARGS="-config phebs.yaml" for flags)
	PHEBS_ZOEKT_GIT_INDEX=$(abspath bin/zoekt-git-index) PHEBS_BUF=$(abspath bin/buf) PHEBS_INVESTIGATION_FIXTURES=$(abspath docs/fixtures/investigations) PHEBS_CONTRACT_ATLAS_FIXTURE=$(abspath docs/fixtures/contracts/contract-atlas.json) go run -tags ui ./cmd/phebs serve $(ARGS)

dev-api: bin/zoekt-git-index bin/buf ## backend-only loop: no UI build, placeholder page
	PHEBS_ZOEKT_GIT_INDEX=$(abspath bin/zoekt-git-index) PHEBS_BUF=$(abspath bin/buf) PHEBS_INVESTIGATION_FIXTURES=$(abspath docs/fixtures/investigations) PHEBS_CONTRACT_ATLAS_FIXTURE=$(abspath docs/fixtures/contracts/contract-atlas.json) go run ./cmd/phebs serve $(ARGS)

validate-version:
	@printf '%s\n' "$(VERSION)" | grep -Eq '$(VERSION_PATTERN)' || { \
		printf 'invalid VERSION %s (expected SemVer, optionally prefixed with v)\n' "$(VERSION)" >&2; \
		exit 2; \
	}

build: validate-version bin/zoekt-git-index bin/buf ui ## version-stamped binary with embedded UI
	go build -trimpath -tags ui -ldflags "-X main.version=$(VERSION)" -o phebs ./cmd/phebs
	@test "$$(./phebs version)" = "$(VERSION)"

test:
	go test ./...

ui-test: ## Vitest UI tests (T6.4); npm install is incremental (~1s when current)
	cd ui && npm install --no-audit --no-fund && npm test

lint:
	golangci-lint run

ui: ## production UI build into ui/dist
	cd ui && npm ci && npm run build

db-server: ## SurrealDB server mode via compose — server-mode testing only (PLAN §1)
	docker compose up -d

verify-go:
	@test "$$(go env GOVERSION)" = "go$(GO_VERSION)" || { \
		printf 'Go %s required; found %s\n' "$(GO_VERSION)" "$$(go env GOVERSION)" >&2; \
		exit 2; \
	}

verify-node:
	@test "$$(node --version)" = "v$(NODE_VERSION)" || { \
		printf 'Node %s required; found %s\n' "$(NODE_VERSION)" "$$(node --version)" >&2; \
		exit 2; \
	}

verify-golangci-lint:
	@test "$$(golangci-lint version | awk '{print $$4}')" = "$(GOLANGCI_LINT_VERSION)" || { \
		printf 'golangci-lint %s required\n' "$(GOLANGCI_LINT_VERSION)" >&2; \
		exit 2; \
	}

verify-surreal:
	@test "$$(surreal version | awk '{print $$1}')" = "$(SURREALDB_VERSION)" || { \
		printf 'SurrealDB %s required; found %s\n' "$(SURREALDB_VERSION)" "$$(surreal version 2>/dev/null | awk '{print $$1}')" >&2; \
		exit 2; \
	}

ci-static: verify-go verify-golangci-lint
	go vet ./...
	golangci-lint run
	go test ./... -run '^$$'

ci-go: verify-go verify-surreal
	go test ./... -count=1 -timeout=25m

ci-race: verify-go verify-surreal
	go test -race ./internal/store ./internal/sync ./internal/indexer ./internal/search -count=1 -timeout=40m

ci-ui: verify-node verify-go
	cd ui && npm ci
	cd ui && npm test
	cd ui && npm run lint
	cd ui && npm run build
	go build -tags ui ./...

ci: ci-static ci-go ci-race ci-ui
