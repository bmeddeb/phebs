# CLAUDE.md — phebs

Self-hosted code search in one Go binary. Ground-up, **reference-only** port of
Sourcebot: zoekt in-process, SurrealDB 3.0, huma OpenAPI, Vite + React +
CodeMirror 6 UI embedded via `go:embed`. Pronounced "febz".

## Source of truth (read before working)

- `PLAN.md` — architecture + dated ADR bullets. Every decision lands here as an
  ADR bullet **in the same PR** as the change. No other design docs.
- `docs/PORT_MAP.md` — upstream analysis, scope, license verdict, EE counter-plan.
- `docs/BACKLOG.md` — epics + PR-sized tickets. Work proceeds in ticket order;
  branch names carry ticket IDs (e.g. `t1.3-job-claim-spike`).
- `docs/MANUAL.md` — the user manual. Behavior changes (config, API, UI,
  operations) update it in the same PR.

## Stack

Go (latest stable, 1.26 line) · `github.com/sourcegraph/zoekt` as a library for
**serving** (`query.Parse`, `shards.DirectorySearcher`); index **builds** via a
child `zoekt-git-index` compiled from the same go.mod SHA (OOM isolation) ·
SurrealDB 3.0 as a **supervised local child** (`surrealkv://`, official Go SDK
over WS — no embedded Go engine, 2026-07-09 ADR) for state **and** job queues
(jittered polling — no Redis, no BullMQ; server mode only in the P6 fleet
profile) · huma v2 for the API (OpenAPI free) · exec `git` for clone/fetch into
bare repos · Vite + React + TS + CodeMirror 6 in `ui/`, embedded in the binary.

## Layout

`cmd/phebs/` · `internal/{config,store,sync,indexer,search,api}` · `ui/` ·
`docs/` · shards at `$DATA/index`, bare repos at `$DATA/repos/<host>/<path>.git`

## Conventions

- PR-sized, stacked changes; one ticket per PR; ACs in BACKLOG.md are the merge bar.
- Table-driven tests. Every epic ends demoable via `make dev` — an epic that
  can't be shown end-to-end is not done.
- golangci-lint clean. `context.Context` first param. Errors wrapped with `%w`,
  classified at boundaries (T3.3 taxonomy).
- HEAD-only indexing. Single-tenant posture; the per-user RepoSet hook in the
  search pre-pass stays reserved but unimplemented.

## Hard rules

- **Never open, copy, or paraphrase upstream Sourcebot source.** Behavior, docs,
  and API shapes are the only reference. Never read any path under `ee/` in the
  upstream repo under any circumstances. Upstream is FSL-1.1 + proprietary ee/;
  phebs is Apache-2.0 (confirmed, T0.2) and must stay uncontaminated.
- Depend on upstream `github.com/sourcegraph/zoekt`, not the sourcebot-dev fork.
- No employer code, credentials, hosts, or infrastructure. Personal project,
  personal hardware.

## Current state

**P1 complete (2026-07-09): Epics 0–5 shipped and demoed** — sync → index →
search → UI, single binary, all local. UI is Base Web (public open-source
`baseui`). **Epic 6 done 2026-07-10** (live T2.2 PAT verification, Vitest UI
harness). **Epic 7 done 2026-07-11** (GitLab + Gitea connectors — Gitea
verified live in Docker, GitHub App auth via stdlib JWT, HMAC webhook
reindex — verified live, re-sync cadence). No git remote yet; CI has never
run. **Epic 8 done 2026-07-11** (search contexts; MCP server at /api/mcp —
verified live from a headless Claude Code session). Next per BACKLOG:
Epic 9 (Wave 3) — users/sessions/API keys → OIDC → SCIP code nav → history.
