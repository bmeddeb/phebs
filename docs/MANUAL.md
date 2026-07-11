# phebs — User Manual

Self-hosted code search in one Go binary. This manual covers installing,
configuring, and operating phebs. For architecture and design rationale see
[PLAN.md](../PLAN.md); for the development backlog see [BACKLOG.md](./BACKLOG.md).

**Contents**

1. [Overview](#1-overview)
2. [Install & first run](#2-install--first-run)
3. [Configuration reference](#3-configuration-reference)
4. [Connecting repositories](#4-connecting-repositories)
5. [Searching](#5-searching)
6. [Web UI](#6-web-ui)
7. [HTTP API](#7-http-api)
8. [Agents (MCP)](#8-agents-mcp)
9. [Operations](#9-operations)
10. [Troubleshooting](#10-troubleshooting)
11. [Developing phebs](#11-developing-phebs)

---

## 1. Overview

phebs mirrors git repositories to local disk, builds
[zoekt](https://github.com/sourcegraph/zoekt) trigram indexes over them, and
serves fast regex-capable code search through a web UI and an OpenAPI HTTP
API — all from a single process with zero external services.

The moving parts inside that one process:

- a **supervised SurrealDB child** storing repo state and job queues on local
  disk (`surrealkv`), started and stopped with phebs;
- a **sync worker** mirroring configured repos into bare git clones;
- an **index worker** running `zoekt-git-index` (built from the same module
  version as the server) as an OOM-isolated child per job;
- an **in-process searcher** over the shard directory, streaming results;
- the **web UI** (React + Base Web + CodeMirror), embedded in the binary.

Indexing is **HEAD-only**: the default branch of each repo (or, for watched
local repos, whatever branch is checked out).

## 2. Install & first run

### Prerequisites

| Requirement | Why | Install |
|---|---|---|
| `git` | clone/fetch mirrors, serve file content | usually present |
| `surreal` (SurrealDB ≥ 3.0) | the state/queue database child | `brew install surrealdb/tap/surreal` or `curl -sSf https://install.surrealdb.com \| sh` |
| Go ≥ 1.26 | build from source | go.dev/dl |
| Node ≥ 24 | build the web UI | nodejs.org |
| `universal-ctags` *(optional)* | symbol search (`sym:`) at index time | `brew install universal-ctags` |

### Build and run

```bash
git clone <your-clone-of-phebs> && cd phebs
make build          # builds the UI, the zoekt-git-index child, and ./phebs
./phebs serve -config phebs.yaml
```

Minimal `phebs.yaml`:

```yaml
connections:
  - name: zoekt
    type: git
    url: https://github.com/sourcegraph/zoekt.git
```

Open <http://localhost:3070>. The repo syncs and indexes within one poll
cycle (≤ ~20 s by default); watch progress on the **Repos** page.

`phebs serve` flags:

| Flag | Meaning |
|---|---|
| `-config path` | config file; omitted = defaults (no connections, data in `~/.phebs`) |
| `-addr :3070` | listen address, overrides `server.addr` |

## 3. Configuration reference

Config is a single YAML file, validated strictly at startup: unknown fields,
type mismatches, and semantic errors **fail fast with line numbers**. The
annotated example lives at [docs/config.example.yaml](./config.example.yaml).

```yaml
server:
  addr: ":3070"          # listen address (default)
  data_dir: "~/.phebs"   # all state lives here (default)

auth:
  api_key: "${PHEBS_API_KEY}"   # bearer token for the API; empty = open

sync:
  cleanup_orphans: false  # delete repos no connection claims (default off)
  poll_interval: 15s      # job-runner cadence; lower for snappier watch mode

connections:
  - name: my-conn         # required; unique; [a-z0-9-]+
    type: github | gitlab | gitea | git
    # ... see per-type fields below
```

| Key | Default | Notes |
|---|---|---|
| `server.addr` | `:3070` | |
| `server.data_dir` | `~/.phebs` | `~` expands; created if missing |
| `auth.api_key` | *(empty)* | empty leaves the API open and logs a warning; `${ENV}` references are expanded — a non-empty key that expands to empty (unset var) fails startup rather than silently opening the API |
| `sync.cleanup_orphans` | `false` | see [orphans](#orphans-and-cleanup) |
| `sync.poll_interval` | `15s` | Go duration; job pollers wake with ±50 % jitter around it |
| `sync.resync_interval` | `1h` | re-sync cadence for remote connections; `"0"` disables |
| `webhook.secret` | *(empty)* | enables `POST /api/webhook`; `${ENV}` expanded, fails closed on unset vars |

### `type: github` connections

```yaml
- name: github-personal
  type: github
  token: "${GITHUB_TOKEN}"   # PAT; omit for public repos only
  orgs:  [my-org]            # all repos of each org
  users: [bmeddeb]           # all repos owned by each user
  repos: [owner/name]        # explicit repos
  exclude:
    archived: true
    forks: true
    repos: ["*/*-mirror"]    # glob on owner/name
```

At least one of `orgs`/`users`/`repos` is required. The token is sent as a
bearer to api.github.com and injected into git fetches per-invocation — it is
never written into mirror config or the database. Rate limits are honored
automatically (the sync waits out `Retry-After` / `X-RateLimit-Reset`).

A `users:` entry naming the token's own account includes that account's
private repos: GitHub's public user listing omits them, so phebs additionally
lists the token owner via the authenticated endpoint and unions the two (a
fine-grained PAT restricted to select repositories still gets all public
repos). Other users list public repos only; private repos elsewhere are
reachable via `orgs:` or explicit `repos:` entries.

#### GitHub App auth

Instead of a PAT, a github connection can authenticate as an App
installation (higher rate limits, per-install scoping):

```yaml
- name: gh-app
  type: github
  app:
    id: 12345                  # the App's ID
    installation_id: 67890     # the installation on your org/account
    private_key_path: /etc/phebs/app.pem   # or private_key: "${APP_KEY_PEM}"
  orgs: [my-org]               # optional — omit selectors to sync every
                               # repo the installation was granted
```

`app` and `token` are mutually exclusive. Each sync run exchanges the App's
key for a fresh ~1-hour installation token (RS256 JWT, no cached state), so
tokens never go stale. Installation tokens have no user identity: `users:`
entries list public repos only under App auth. Without any selectors the
connection syncs exactly the installation's granted repositories.

### `type: gitlab` connections

```yaml
- name: gitlab-work
  type: gitlab
  url: https://git.example.com  # self-hosted base URL; omit for gitlab.com
  token: "${GITLAB_TOKEN}"      # PAT; omit for public projects only
  groups: [team/platform]       # all projects of each group, subgroups included
  users:  [dev]                 # all projects owned by each user
  repos:  [solo/tool]           # explicit projects by full path
  exclude:
    archived: true
    forks: true
    repos: ["*/*/sandbox-*"]    # glob on the full project path
```

At least one of `groups`/`users`/`repos` is required. Unlike GitHub, GitLab's
user listing is requester-scoped, so a token's own private projects appear
without special-casing. The token authenticates the API (bearer) and git
fetches (HTTP basic as the `oauth2` pseudo-user, injected per-invocation) —
it is never written into mirror config or the database. Rate limits are
honored automatically (429 `Retry-After`). Repos are named
`<host>/<full/project/path>`.

### `type: gitea` connections

```yaml
- name: gitea-forge
  type: gitea
  url: https://gitea.example.com  # required: base URL of the instance
  token: "${GITEA_TOKEN}"         # PAT; omit for public repos only
  orgs:  [acme]                   # all repos of each org
  users: [dev]                    # all repos owned by each user
  repos: [owner/name]             # explicit repos
  exclude:
    archived: true
    forks: true
    repos: ["*/*-mirror"]
```

`url` is required (there is no canonical hosted Gitea); at least one of
`orgs`/`users`/`repos` too. Listings are requester-scoped, so a token sees
its accessible private repos. The token authenticates the API
(`Authorization: token …`) and git fetches (HTTP basic, token as username,
injected per-invocation) — never persisted. Repos are named
`<host>/<owner>/<name>`.

### `type: git` connections

```yaml
- name: any-git
  type: git
  url: https://example.com/repo.git    # any clone URL: https, ssh, scp-like
```

Local repositories use a plain absolute path (preferred — git clones with
hardlinks, costing near-zero disk) or a `file://` URL:

```yaml
- name: my-project
  type: git
  url: /Users/ben/src/my-project
  watch: true            # see §4, watch mode
```

## 4. Connecting repositories

### Sync lifecycle

At boot, phebs enqueues one sync job per configured connection (skipping any
already in flight). A sync resolves the connection to repo rows, mirrors each
repo into `$DATA/repos/<host>/<path>.git`, and chains an indexing job per
synced repo. Re-syncs are incremental (`git fetch --prune`).

Beyond boot, syncs happen when:

- a **watched** local repo's HEAD moves (see below);
- the **re-sync cadence** fires (`sync.resync_interval`, default `1h`, `"0"`
  disables): every remote connection is re-synced, skipping any still in
  flight — local repos are covered by boot and watch instead;
- a **push webhook** arrives (see below);
- you press **Reindex** in the UI or call `POST /api/reindex` (re-index only);
- phebs restarts.

### Push webhooks

`POST /api/webhook` turns code-host push events into targeted fetches — the
changed repo is fetched and reindexed without waiting for a poll, and without
re-listing the host:

```yaml
webhook:
  secret: "${WEBHOOK_SECRET}"   # required to enable the endpoint
```

Point a GitHub (or Gitea — it sends GitHub-compatible headers, verified live)
webhook at `https://your-phebs/api/webhook` with content type
`application/json` and the same secret. Payload signatures
(`X-Hub-Signature-256`) are verified in constant time; the endpoint does not
exist unless a secret is configured, and it ignores pushes for repos phebs
doesn't know. `repository` and `installation_repositories` events (repo
created/deleted/renamed, App grants changed) re-sync the remote connections
so membership catches up. GitLab webhooks use a different scheme and are not
yet supported — the re-sync cadence covers those.

### Watch mode (local repos)

`watch: true` on a local git connection makes phebs poll the repo's HEAD
(every ~3 s) and re-sync + re-index whenever it moves. Because indexing is
HEAD-only, the HEAD hash is the exact change signal: **commits and branch
switches trigger reindexing; uncommitted working-tree edits do not.**

Watched mirrors **follow the branch you have checked out** — switch to
`feature`, commit, and search reflects `feature`. A detached HEAD (mid-rebase,
bisect) keeps the last good index until you land somewhere.

End-to-end latency is roughly `watch tick (≤3 s) + poll_interval + index
time`. With `sync.poll_interval: 1s`, a commit is searchable in ~1–2 s.

### Orphans and cleanup

A repo no connection claims (you removed the connection or narrowed its
filters) is flagged **orphaned** on the Repos page and in `/api/repo-status`.
By default orphans are kept; set `sync.cleanup_orphans: true` to delete their
rows, mirrors, and index shards after each sync. phebs only ever deletes data it created
under its own data directory.

## 5. Searching

phebs uses zoekt's native query language. Patterns are regular expressions;
plain text behaves like substring search. Filters and patterns combine with
implicit AND; prefix any atom with `-` to negate it.

| Syntax | Meaning |
|---|---|
| `foo bar` | files containing `foo` AND `bar` |
| `"foo bar"` | the exact phrase |
| `f[ou]+nc.*Parse` | regular expression |
| `case:yes Foo` | case-sensitive (default: smart case) |
| `repo:zoekt` | repo name matches regex |
| `file:\.go$` / `-file:_test` | file path matches / doesn't match |
| `lang:go` | language filter |
| `sym:ParseQuery` | symbol definitions (needs ctags at index time) |
| `content:foo` | match file content only (not paths) |
| `archived:yes\|no` | filter by repo archived state *(phebs, from repo metadata)* |
| `fork:yes\|no` | filter by fork state *(phebs)* |
| `public:yes\|no` | filter by visibility *(phebs)* |
| `context:backend` | restrict to a named repo set *(phebs, see below)* |

Examples:

```
watchModeNeedle repo:my-project
"TODO(ben)" -file:vendor/ lang:go
sym:ClaimJob fork:no
case:yes Searcher file:internal/
ClaimJob context:backend
```

### Search contexts

Contexts are named repo sets defined in config — shorthand for scoping
queries to a slice of the index:

```yaml
contexts:
  backend:
    - "github.com/acme/api-*"
    - "gitlab.example.com/team/platform/*"
  docs:
    - "github.com/acme/handbook"
```

`context:backend needle` searches only repos whose full name matches one of
the set's glob patterns (`*` does not cross `/`; a pattern without wildcards
is an exact name). Multiple `context:` atoms union their sets. A context is
a scope, not a predicate: it applies to the whole query and can't be
negated. Unknown names are an error; the atom is top-level phebs syntax, so
`context:` inside parentheses is passed to zoekt (and rejected) rather than
resolved.

Result bounds: `max_matches` (default 50 files, cap 500) and `context_lines`
(default 0, cap 10) on the API; searches are capped at 10 s of wall time.

## 6. Web UI

Served at `/` from the binary. Three views, all deep-linkable (hash routes):

- **Search** (`#/search?q=…`) — results stream in as shards respond, grouped
  repo → file, with match counts and highlighted spans. Line numbers link
  into the viewer.
- **File viewer** (`#/file?repo=…&path=…&L=42`) — read-only CodeMirror with
  syntax highlighting across ~30 languages (Go, JS/TS, Python, Rust, Java,
  C/C++, C#, Ruby, PHP, SQL, HTML/CSS, YAML, shell, …), a file-tree navigation
  column that auto-expands to the current file, and a highlighted, scrolled-to
  anchor line.
- **Repos** (`#/repos`) — sync/index state per repo (polled every 3 s),
  orphan flags, indexed commit, and a **Reindex** button (forces a full
  rebuild, defeating the incremental short-circuit).

> **Auth caveat (P1):** the UI does not attach a bearer token. If you set
> `auth.api_key`, use the API with curl/clients, and keep the UI for
> localhost or behind an authenticating reverse proxy.

## 7. HTTP API

The API is OpenAPI-described by itself: fetch `/api/openapi.json` or browse
the interactive docs at `/api/docs`.

**Auth:** when `auth.api_key` is set, send `Authorization: Bearer <key>`.
Always open: `/api/health`, `/api/version`, `/api/openapi*`, `/api/docs`,
and `/metrics`.

| Endpoint | Method | Purpose |
|---|---|---|
| `/api/health` | GET | liveness `{"status":"ok"}` |
| `/api/version` | GET | server version |
| `/api/search?q=&max_matches=&context_lines=` | GET | search, JSON in one shot |
| `/api/stream_search?q=…` | GET | search over SSE (below) |
| `/api/repos` | GET | repo rows |
| `/api/repo-status` | GET | repos + connections + orphan flag + last index job |
| `/api/reindex` | POST | `{"repo":"github.com/foo/bar","force":true}` → enqueue index job |
| `/api/webhook` | POST | code-host push/repository events, HMAC-authed (no bearer); 404 unless `webhook.secret` set |
| `/api/mcp` | POST/GET | MCP over Streamable HTTP (see §8) |
| `/api/source?repo=&path=&ref=` | GET | file content (`ref` defaults HEAD); binary comes base64; blobs over 10 MiB return 413 |
| `/api/folder_contents?repo=&path=&ref=` | GET | one directory level |
| `/api/tree?repo=&ref=` | GET | all file paths, recursive |
| `/metrics` | GET | Prometheus metrics |

`stream_search` emits Server-Sent Events: one `results` event per shard batch
(same JSON shape as `/api/search`), then a final `done` event with aggregate
stats; errors arrive as an `error` event. Disconnecting cancels the search.

```bash
curl 'localhost:3070/api/search?q=ClaimJob+lang:go' | jq .files[0]
curl -N 'localhost:3070/api/stream_search?q=needle'
curl -X POST localhost:3070/api/reindex \
  -H 'Content-Type: application/json' -d '{"repo":"github.com/foo/bar","force":true}'
```

## 8. Agents (MCP)

phebs is an MCP server: agents search and read your code through the same
index the UI uses. The endpoint is `/api/mcp` (Streamable HTTP, official MCP
go-sdk), guarded by the same `auth.api_key` bearer as the rest of the API.

Three tools:

| Tool | Purpose |
|---|---|
| `search_code` | full query syntax from §5, including `context:` sets; returns files with line-numbered chunks and match ranges |
| `read_file` | file content at the indexed revision; optional `start_line`/`end_line`; oversize output is truncated on line boundaries with a `truncated` flag inviting a ranged re-read |
| `list_repos` | every indexed repo with branch/visibility/index-time metadata |

Binary files and unknown repos come back as tool errors, not blobs.

### Claude Code

```bash
claude mcp add --transport http phebs http://localhost:3070/api/mcp \
  --header "Authorization: Bearer YOUR_API_KEY"
```

or the equivalent `.mcp.json`:

```json
{
  "mcpServers": {
    "phebs": {
      "type": "http",
      "url": "http://localhost:3070/api/mcp",
      "headers": { "Authorization": "Bearer YOUR_API_KEY" }
    }
  }
}
```

Any MCP client speaking Streamable HTTP works the same way. Verified live
against Claude Code: a headless session listed repos, ran a scoped search,
and read the matching file end-to-end (T8.3).

## 9. Operations

### Data layout

```
$DATA/                     # server.data_dir, default ~/.phebs
├── db/                    # SurrealDB (surrealkv) — repo rows, job queues
├── repos/<host>/<path>.git  # bare mirrors
└── index/*.zoekt          # search shards
```

**Everything under `$DATA` is derived state.** The only precious artifact is
your config file: deleting the data directory and restarting rebuilds
mirrors, indexes, and rows from scratch.

### Job system

Sync and index work runs through queues in SurrealDB, drained by pollers that
wake every `poll_interval` (±50 % jitter). Job states:
`pending → claimed → running → done | failed`.

- **Retries:** failed executions requeue with per-class backoff, up to 3
  attempts, then land in `failed` with the error recorded (visible in
  `/api/repo-status` and the UI).
- **Backoff by failure class:** generic `30s × 2ⁿ`; auth failures `10m × 2ⁿ`
  (a bad token won't heal in seconds); OOM-killed index children `5m × 2ⁿ`;
  corrupt shards retry after `1s` (rebuild usually fixes them). Capped at 1 h.
- **Crash recovery:** running jobs heartbeat; a reaper requeues jobs whose
  worker died (stale heartbeat), or fails them once attempts are exhausted.
  Kill phebs mid-index and the job recovers on next boot.

### Metrics

| Metric | Type | Labels |
|---|---|---|
| `phebs_jobs_total` | counter | `kind`, `result` (`done`/`failed`/`requeued`/`reaped`) |
| `phebs_job_errors_total` | counter | `kind`, `class` (`auth`/`oom`/`corrupt-shard`/`generic`) |
| `phebs_index_duration_seconds` | histogram | — |
| `phebs_index_shard_bytes` | gauge | — |

Plus standard Go process metrics. Scrape `/metrics`.

### Shutdown

SIGINT/SIGTERM drains gracefully: the HTTP server stops, the SurrealDB child
is stopped with it. In-flight jobs left behind are recovered by the reaper on
next boot.

## 10. Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `start surreal child: exec: "surreal": executable file not found` | SurrealDB not installed | see [prerequisites](#prerequisites) |
| log: `zoekt-git-index not found — indexing disabled` | binary built without `make build`/`make dev` | `make build`, or set `PHEBS_ZOEKT_GIT_INDEX=/path/to/zoekt-git-index` |
| `listen tcp :3070: bind: address already in use` | another phebs (or process) on the port | stop it, or `-addr :3071` |
| API answers `401` | `auth.api_key` is set | send `Authorization: Bearer <key>` |
| GitHub sync seems stalled | rate-limited; phebs is waiting out the reset window | wait, or use a PAT (5000 req/h vs 60) |
| watch mode "doesn't see my edits" | uncommitted changes — indexing is HEAD-only | commit (or amend); the watcher reacts to HEAD moves |
| a search right after reindex returns nothing once | shard file being swapped; next query is fine | transient; hardening planned (P5) |
| repo tagged `orphaned` | no connection claims it anymore | re-add the connection, or enable `sync.cleanup_orphans` |
| sync fails with `auth: git …` and retries slowly | credential failure, classified `auth` (10 m backoff) | fix the token; reindex/restart to retry immediately |

## 11. Developing phebs

| Target | Does |
|---|---|
| `make dev` | build UI + zoekt child, run with embedded UI |
| `make dev-api` | backend-only loop (placeholder UI page, fast) |
| `make build` | release binary `./phebs` with embedded UI |
| `make test` | `go test ./...` — store/sync/indexer tests spawn real surreal children and need the `surreal` binary; zoekt-git-index is auto-built by the test harness |
| `make ui-test` | Vitest UI tests (`cd ui && npm test`) — streaming, keyboard nav, facets, file tree |
| `make lint` | golangci-lint |
| `make ui` | production UI build only |
| `make db-server` | SurrealDB in server mode via docker compose (testing only) |

Live UI development: run `make dev-api`, then `cd ui && npm run dev` — Vite
serves on :5173 and proxies `/api` to :3070.

Repository documentation map: [PLAN.md](../PLAN.md) (architecture + every
decision as a dated ADR) · [PORT_MAP.md](./PORT_MAP.md) (upstream analysis
and scope) · [BACKLOG.md](./BACKLOG.md) (tickets, acceptance criteria, and
what's done) · this manual (user-facing behavior).

phebs is an independent, reference-only reimplementation inspired by
[Sourcebot](https://github.com/sourcebot-dev/sourcebot) — no upstream code is
used. phebs is licensed Apache-2.0.
