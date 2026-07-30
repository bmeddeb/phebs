VERSION ?= 0.2.1-dev
VERSION_PATTERN_BODY := (0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-(0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*))*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?
VERSION_PATTERN := ^v?$(VERSION_PATTERN_BODY)$$
RELEASE_VERSION_PATTERN := ^v$(VERSION_PATTERN_BODY)$$
GO_VERSION := $(shell tr -d '[:space:]' < .go-version)
NODE_VERSION := $(shell tr -d '[:space:]' < .node-version)
GOLANGCI_LINT_VERSION := $(patsubst v%,%,$(shell tr -d '[:space:]' < .golangci-lint-version))
SURREALDB_VERSION := $(shell tr -d '[:space:]' < .surrealdb-version)
TARGET_GOOS ?= $(shell go env GOOS)
TARGET_GOARCH ?= $(shell go env GOARCH)
RELEASE_ROOT ?= dist
RELEASE_COMMIT ?= $(shell git rev-parse HEAD)
RELEASE_STAGE = $(RELEASE_ROOT)/.build-$(VERSION)-$(TARGET_GOOS)-$(TARGET_GOARCH)
RELEASE_BUNDLE = $(RELEASE_ROOT)/phebs-$(VERSION)-$(TARGET_GOOS)-$(TARGET_GOARCH)
T2014_RESULTS_PATH ?= /private/tmp/phebs-t20.14-results.json

.PHONY: dev dev-api build clean validate-version validate-release-version validate-release-target \
	release verify-release smoke-release test ui-test lint ui db-server \
	verify-go verify-node verify-golangci-lint verify-surreal verify-glossary t20-closure \
	docs-check ci ci-static ci-go ci-race ci-ui

bin:
	mkdir -p $@

bin/zoekt-git-index: go.mod go.sum | bin ## index builder, same module SHA as the server (PLAN §1.1)
	go build -trimpath -o $@ github.com/sourcegraph/zoekt/cmd/zoekt-git-index

bin/phebs-focused-index: go.mod go.sum $(shell find cmd/phebs-focused-index internal -type f -name '*.go') | bin ## scoped index builder, same source line as the server (T30.3)
	go build -trimpath -o $@ ./cmd/phebs-focused-index

bin/buf: go.mod go.sum | bin ## compatibility child, pinned by the same go.mod as the server (T14.3)
	CGO_ENABLED=0 go build -trimpath -o $@ github.com/bufbuild/buf/cmd/buf

dev: bin/zoekt-git-index bin/phebs-focused-index bin/buf ui ## boot phebs with embedded UI (ARGS="-config phebs.yaml" for flags)
	PHEBS_ZOEKT_GIT_INDEX=$(abspath bin/zoekt-git-index) \
		PHEBS_FOCUSED_INDEX=$(abspath bin/phebs-focused-index) \
		PHEBS_BUF=$(abspath bin/buf) \
		PHEBS_INVESTIGATION_FIXTURES=$(abspath docs/fixtures/investigations) \
		PHEBS_CONTRACT_ATLAS_FIXTURE=$(abspath docs/fixtures/contracts/contract-atlas.json) \
		PHEBS_WORKBENCH_CLOSURE_REPO=$(abspath docs/fixtures/change-workbench/t2114-workbench-closure.bundle) \
		PHEBS_THRIFT_FIELD_DEMO_REPO=$(abspath docs/fixtures/thrift-field/t225-thrift-field-demo.bundle) \
		PHEBS_SYNTHETIC_WORKBENCH=1 \
		go run -tags ui ./cmd/phebs serve $(ARGS)

dev-api: bin/zoekt-git-index bin/phebs-focused-index bin/buf ## backend-only loop: no UI build, placeholder page
	PHEBS_ZOEKT_GIT_INDEX=$(abspath bin/zoekt-git-index) \
		PHEBS_FOCUSED_INDEX=$(abspath bin/phebs-focused-index) \
		PHEBS_BUF=$(abspath bin/buf) \
		PHEBS_INVESTIGATION_FIXTURES=$(abspath docs/fixtures/investigations) \
		PHEBS_CONTRACT_ATLAS_FIXTURE=$(abspath docs/fixtures/contracts/contract-atlas.json) \
		PHEBS_WORKBENCH_CLOSURE_REPO=$(abspath docs/fixtures/change-workbench/t2114-workbench-closure.bundle) \
		PHEBS_THRIFT_FIELD_DEMO_REPO=$(abspath docs/fixtures/thrift-field/t225-thrift-field-demo.bundle) \
		PHEBS_SYNTHETIC_WORKBENCH=1 \
		go run ./cmd/phebs serve $(ARGS)

validate-version:
	@printf '%s\n' "$(VERSION)" | grep -Eq '$(VERSION_PATTERN)' || { \
		printf 'invalid VERSION %s (expected SemVer, optionally prefixed with v)\n' "$(VERSION)" >&2; \
		exit 2; \
	}

validate-release-version:
	@printf '%s\n' "$(VERSION)" | grep -Eq '$(RELEASE_VERSION_PATTERN)' || { \
		printf 'invalid release VERSION %s (expected v-prefixed SemVer)\n' "$(VERSION)" >&2; \
		exit 2; \
	}

validate-release-target:
	@test "$(TARGET_GOOS)/$(TARGET_GOARCH)" = "$$(go env GOOS)/$$(go env GOARCH)" || { \
		printf 'release target %s/%s is not executable on this %s/%s smoke host\n' \
			"$(TARGET_GOOS)" "$(TARGET_GOARCH)" "$$(go env GOOS)" "$$(go env GOARCH)" >&2; \
		exit 2; \
	}

build: validate-version bin/zoekt-git-index bin/phebs-focused-index bin/buf ui ## version-stamped binary with embedded UI
	go build -trimpath -tags ui -ldflags "-X main.version=$(VERSION)" -o phebs ./cmd/phebs
	@test "$$(./phebs version)" = "$(VERSION)"

clean: ## remove only standard source-tree build/package outputs; never runtime data or caches
	@root=$$(pwd -P); \
	if [ -z "$$root" ] || [ "$$root" = "/" ] || \
		[ ! -f "$$root/Makefile" ] || [ -L "$$root/Makefile" ] || \
		[ ! -f "$$root/go.mod" ] || [ -L "$$root/go.mod" ] || \
		[ ! -f "$$root/ui/package.json" ] || [ -L "$$root/ui/package.json" ] || \
		[ ! -d "$$root/cmd/phebs" ] || [ -L "$$root/cmd/phebs" ] || \
		[ -L "$$root/.git" ] || { [ ! -f "$$root/.git" ] && [ ! -d "$$root/.git" ]; }; then \
		printf 'make clean: refusing unsafe checkout root %s\n' "$$root" >&2; \
		exit 1; \
	fi; \
	if ! IFS= read -r module_line < "$$root/go.mod" || \
		[ "$$module_line" != "module github.com/bmeddeb/phebs" ]; then \
		printf 'make clean: refusing checkout with unexpected go.mod %s\n' "$$root/go.mod" >&2; \
		exit 1; \
	fi; \
	for parent in "$$root/bin" "$$root/dist" "$$root/ui"; do \
		if [ -L "$$parent" ] || { [ -e "$$parent" ] && [ ! -d "$$parent" ]; }; then \
			printf 'make clean: refusing unsafe path %s (parent must be a real directory)\n' "$$parent" >&2; \
			exit 1; \
		fi; \
	done; \
	for output in \
		"$$root/phebs" \
		"$$root/coverage.out" \
		"$$root/bin/zoekt-git-index" \
		"$$root/bin/phebs-focused-index" \
		"$$root/bin/buf"; do \
		if [ -e "$$output" ] && [ ! -f "$$output" ] && [ ! -L "$$output" ]; then \
			printf 'make clean: refusing unsafe path %s (file output must be regular or a symlink)\n' "$$output" >&2; \
			exit 1; \
		fi; \
	done; \
	rm -f "$$root/phebs" "$$root/coverage.out" || exit 1; \
	rm -f "$$root/bin/zoekt-git-index" "$$root/bin/phebs-focused-index" "$$root/bin/buf" || exit 1; \
	rm -rf "$$root/ui/dist" || exit 1; \
	rm -rf "$$root/dist"/.build-* "$$root/dist"/phebs-* "$$root/dist"/.phebs-*.tmp-* || exit 1

release: validate-release-version validate-release-target verify-go verify-node ui ## deterministic manifest-bound release directory
	mkdir -p "$(RELEASE_STAGE)/bin"
	CGO_ENABLED=0 GOOS="$(TARGET_GOOS)" GOARCH="$(TARGET_GOARCH)" go build -trimpath -tags ui -ldflags "-X main.version=$(VERSION)" -o "$(RELEASE_STAGE)/phebs" ./cmd/phebs
	CGO_ENABLED=0 GOOS="$(TARGET_GOOS)" GOARCH="$(TARGET_GOARCH)" go build -trimpath -o "$(RELEASE_STAGE)/bin/zoekt-git-index" github.com/sourcegraph/zoekt/cmd/zoekt-git-index
	CGO_ENABLED=0 GOOS="$(TARGET_GOOS)" GOARCH="$(TARGET_GOARCH)" go build -trimpath -o "$(RELEASE_STAGE)/bin/phebs-focused-index" ./cmd/phebs-focused-index
	CGO_ENABLED=0 GOOS="$(TARGET_GOOS)" GOARCH="$(TARGET_GOARCH)" go build -trimpath -o "$(RELEASE_STAGE)/bin/buf" github.com/bufbuild/buf/cmd/buf
	@test "$$("$(RELEASE_STAGE)/phebs" version)" = "$(VERSION)"
	go run ./scripts/release bundle \
		-output "$(RELEASE_BUNDLE)" -version "$(VERSION)" -commit "$(RELEASE_COMMIT)" \
		-go-version "$(GO_VERSION)" -goos "$(TARGET_GOOS)" -goarch "$(TARGET_GOARCH)" \
		-phebs "$(RELEASE_STAGE)/phebs" \
		-zoekt "$(RELEASE_STAGE)/bin/zoekt-git-index" \
		-focused "$(RELEASE_STAGE)/bin/phebs-focused-index" \
		-buf "$(RELEASE_STAGE)/bin/buf"

verify-release: ## verify RELEASE_BUNDLE bytes, modes, and canonical manifest
	go run ./scripts/release verify -bundle "$(RELEASE_BUNDLE)"

smoke-release: verify-release verify-surreal ## empty-data sync/index/search and default-dark smoke
	go run ./scripts/release-smoke -bundle "$(RELEASE_BUNDLE)" -timeout 2m

test: verify-glossary
	go test ./... -timeout=25m

docs-check: ## resolve tracked docs, enforce map coverage, and verify sealed T11.1 bytes
	go test ./scripts \
		-run '^Test(TrackedMarkdownLinksResolve|DocumentationMapReachesTrackedDocs|SealedT111TreeDigest)$$' \
		-count=1

t20-closure: bin/zoekt-git-index verify-surreal ## T20.14 empty-data scale/failure journey; receipt defaults to /private/tmp
	PHEBS_ZOEKT_GIT_INDEX=$(abspath bin/zoekt-git-index) \
		T2014_RUN=1 T2014_RESULTS_PATH=$(T2014_RESULTS_PATH) \
		go test ./internal/api -run '^TestT2014ScaleFailureAndEndToEndClosure$$' \
		-count=1 -timeout=25m -v

ui-test: ## Vitest UI tests (T6.4); npm install is incremental (~1s when current)
	cd ui && npm install --no-audit --no-fund && npm test

lint: verify-glossary
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

verify-glossary:
	go run ./internal/glossary/cmd/glossary-gen -verify -root .

ci-static: verify-go verify-golangci-lint verify-glossary
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
