# phebs — Port Plan (Sourcebot → Go)

**Shape:** single Go binary · zoekt as a library · embedded SurrealDB · clean-room TypeScript SPA

**Version:** v2.3 (2026-07-09).
**Changelog:** v2.2 (2026-07-07) resolved huma/branch-scope/queue-wakeup at Uber-scale
posture and added the fleet profile. v2.3 re-baselines the doc for the phebs repo:
license verdict + reference-only discipline, own config schema (supersedes v2.2
config row), naming, and the BACKLOG.md epic mapping.
**Provenance:** v2.2 baseline reconstructed 2026-07-09 from the 2026-07-07 design
session; P1/P2 phase text re-derived from the §8 decision log (originals not
retrieved verbatim). Everything else carries the session's substance.

---

## 1. Decisions locked

| Area | Decision | Escape hatch |
|---|---|---|
| Language | Go ≥1.24 (build on latest stable, 1.26 line) | — |
| Search engine | zoekt imported as a library (`shards`, `query`) for serving; **index builds via child `zoekt-git-index` built from the same go.mod SHA** (OOM isolation — zoekt itself isolates builds) | none needed — battle-tested |
| zoekt source | **Upstream `sourcegraph/zoekt` (Apache-2.0), one version for reader and writer.** Reader/writer shard skew structurally impossible; shards are derived data, rebuildable from bare repos. sourcebot-dev fork verified as a light tracker — not used | temporary `-replace` only to browse legacy shards |
| Database | SurrealDB ≥3.0 as a **supervised local child** (`surreal start surrealkv://…`, loopback-only) for single-node, official Go SDK over WS — no embedded Go engine exists (2026-07-09 ADR); **server mode in the fleet profile** | store interfaces → Postgres impl |
| Job queue | Lease table on SurrealDB; claim conflict behavior characterized via invariant checker + Elle (P2 single-node, P6 distributed) | asynq + Redis |
| Queue wakeups | **Jittered polling.** LIVE SELECT wakeups are a thundering herd under fleet concurrency (every insert wakes every claimer; N−1 abort) | — |
| Live updates | LIVE SELECT → SSE **for UI status fan-out only** (confirmed working through the embedded engine); never for queue wakeups | — |
| API | **huma v2** — OpenAPI contract for multiple consumer classes (SPA, MCP, agents, other teams); validation + docs + client codegen | — |
| Frontend | **Clean-room TypeScript SPA** (Vite + React + CodeMirror 6), zero upstream component reuse; types via huma OpenAPI → `openapi-typescript` codegen; Go→JS/WASM rejected (CM6 keeps TS in the stack regardless) | templ + htmx |
| Branch scope | **HEAD-only by default** + explicit per-repo allowlist capped ≈8; feature branches never indexed; monorepo freshness via debounced cadence, zoekt delta builds as the lever | — |
| Auth | scs sessions + goth (OAuth) + hashed API keys; go-oidc SSO in P3 | — |
| MCP | official `modelcontextprotocol/go-sdk`; **MCP-first product layer** (v2.3) — agents bring their own chat; Ask-style loop optional later | — |
| Config | **Own schema** (YAML, huma-validated) + optional `phebs import sourcebot-config` (v2.3 — supersedes v2.2 "adopt upstream v3 config.json": license discipline forbids copying their schema files) | — |
| Query language | zoekt native syntax via `query.Parse` + DB pre-pass (`context:` `archived:` `fork:` → `query.RepoSet`); no grammar port (v2.3) | — |
| File serving | git plumbing (`cat-file`, `ls-tree`) from bare repos, not zoekt content tricks (v2.3) | — |
| Symbols | universal-ctags binary in image (invoked by zoekt at index time) | — |
| License posture | Upstream verified **FSL-1.1-ALv2 core + proprietary `ee/`** (nothing Apache-converted before 2026-10-01, HEAD converts 2028-07). **Reference-only discipline**: no code/UI/CSS/schema copying; `ee/` paths never opened. phebs is Apache-2.0 (confirmed 2026-07-09, T0.2) | — |

### 1.1 Why upstream-only dissolves shard compatibility

The port owns both sides of the shard boundary: our binary reads shards, and our
container builds the writer from the *same module version*
(`go build github.com/sourcegraph/zoekt/cmd/zoekt-git-index`). Compatibility with
fork-written shards stops mattering — reindex at cutover.

### 1.2 Fleet profile (P6)

N homogeneous replicas of the same binary. Each owns a rendezvous-hashed repo
subset: indexes it, serves it from local NVMe shards. gRPC scatter-gather across
replicas for queries; SurrealDB cut over embedded → server mode (export/import);
owner-scoped job claims; admission control with **separate interactive and agent
traffic budgets**. Standalone remains the dev/small default; fleet lands last so
single-node correctness is proved first.

## 2. Phases

- **P0 — Spike. DONE (2026-07-07).** ~200-line Go binary serving `/search` over an
  existing zoekt shard directory via library import. Proved the zoekt-as-library
  thesis.
- **P1 — Single-node core** *(reconstructed)*. Config, store on embedded Surreal,
  sync (github + generic git → bare repos), index pipeline (same-SHA child
  builder, HEAD-only), search API on huma. ≈ BACKLOG Epics 0–4.
- **P2 — Queue correctness + liveness** *(reconstructed)*. Claim-transaction
  invariant checker in CI; Elle probe single-node; LIVE SELECT → SSE status
  wiring. ≈ BACKLOG T1.3 (first slice) + T2.3 + status surfaces.
- **P3 — Clean-room SPA + SSO (go-oidc).** *Exit:* end-to-end search UI, zero
  Node processes, zero FSL-derived frontend code. ~2.5–3 wk. ≈ BACKLOG Epic 5.
- **P4 — Product layer, MCP-first.** MCP server (`search_code`, `read_file`,
  `list_repos`); code nav (`sym:` defs/refs + hover); optional Ask-style tool
  loop with SSE + persistence. *Exit:* cited agent answers; MCP usable from
  Claude Code. ~2–4 wk.
- **P5 — Hardening.** Prometheus metrics; `SURREAL EXPORT` backups of precious
  tables + rebuild-from-config runbook; graceful shutdown releases leases; image
  with git + ctags; concurrency soak reusing the probe. Ongoing.
- **P6 — Fleet profile.** Rendezvous placement + membership; owner-scoped claims;
  gRPC scatter-gather searcher; Surreal embedded → server cutover; admission
  control + per-client quotas (agent traffic modeled explicitly); **capacity
  spike on the real corpus** (index multiplier, RAM working set, monorepo build
  time → delta-build decision); load test with agent-shaped traffic; rerun Elle
  against TiKV-backed Surreal. *Exit:* replica kill → rehash + reindex without
  operator action; p95 held under load. ~3–5 wk.
- **Deferred:** remaining connectors, permission syncing (edges reserved), audit
  logs, billing, review agent. Re-sequencing options post-P1 live in
  PORT_MAP §12.

## 3. Risk register

| Risk | Mitigation |
|---|---|
| `surreal` child binary must be present on host/image | Same posture as git + ctags children: install step in CI and dev docs; bundled in the P5 image |
| Reader/writer shard skew | Structurally eliminated: library + `zoekt-git-index` from one module version |
| Queue double-claims under concurrency | Idempotent handlers; P2 invariant checker in CI; Elle characterization single-node (P2) and distributed (P6) before trusting exactly-once anywhere |
| Thundering herd on job inserts | Designed out: jittered polling, owner-scoped claims, per-kind concurrency caps |
| Monorepo index build time vs freshness SLO | Debounced cadence; delta builds as the lever; measured in P6 capacity spike |
| Fan-out tail latency (fleet) | Per-replica timeouts + partial-result degradation; hedging only if measurements demand it |
| Agent traffic amplification | Admission control + separate interactive/agent budgets at the gateway (P6) |
| surrealkv / Surreal-server operational youth | Nightly export of precious tables; derived state rebuildable; store interface keeps Postgres exit open |
| OpenAPI/type-codegen drift | `go generate` in CI; fail on dirty diff |
| FSL-1.1 upstream | Reference-only discipline (§1 license row); clean-room frontend; zoekt permissively licensed; product layer original, not translated |

## 4. Open questions

1. **P6 gate:** measured index-size and RAM multipliers on the real corpus — no
   hardware commitments before the capacity spike.
2. **P6:** do full monorepo builds meet the freshness SLO, or do delta builds
   graduate from "lever" to "required"?
3. **P6:** timing of the TiKV-backed Surreal cutover (when distributed Elle
   work starts).
4. **P6 (v2.3):** fleet peer protocol — reuse upstream zoekt `webserver/v1`
   gRPC protos vs custom proto.
5. **P1 (v2.3):** SSE flush cadence for `stream_search`; `repo.metadata` typed
   per host vs schemaless. Decide inline in T4.3 / T2.2.

## 5. Decision log

| Date | Question | Resolution |
|---|---|---|
| 2026-07-07 | Fork pin vs upstream | Upstream-only; reader/writer version-locked from one go.mod; legacy shards disposable |
| 2026-07-07 | LIVE SELECT through embedded engine | Confirmed → LIVE SELECT → SSE for status; not used for queue wakeups |
| 2026-07-07 | Elle on claim conflicts | In scope, P2 (single-node) + P6 rerun (TiKV-backed) |
| 2026-07-07 | Frontend reuse vs clean | Clean-room TS SPA; Go→JS/WASM rejected |
| 2026-07-07 | huma vs tygo | huma — OpenAPI contract for multiple consumer classes at org scale |
| 2026-07-07 | Branch indexing scope | HEAD default + explicit allowlist, cap ≈8; feature branches never; monorepo on debounced cadence |
| 2026-07-07 | Queue wakeups | Jittered poll; LIVE SELECT reserved for UI fan-out |
| 2026-07-07 | Scale posture | Heavy use at Uber scale → fleet profile §1.2 + P6; standalone remains dev/small default |
| 2026-07-09 | Name / brand | **phebs** (lowercase, "febz"), phebs.com; README epigraph locked |
| 2026-07-09 | Upstream license | Verified FSL-1.1-ALv2 + proprietary `ee/`; reference-only discipline; `ee/` never opened; deps upstream-zoekt-only |
| 2026-07-09 | phebs license | Apache-2.0 recommended; commit in T0.2 |
| 2026-07-09 | Config schema | Own schema + optional importer — supersedes v2.2 "adopt upstream v3 config.json" |
| 2026-07-09 | Query language | zoekt native + DB pre-pass; no grammar port |
| 2026-07-09 | File serving | git plumbing from bare repos |
| 2026-07-09 | Product layer ordering | MCP-first; upstream chat app not cloned (PORT_MAP §12) |
| 2026-07-09 | Execution granularity | BACKLOG.md epics/tickets are the work units; ADRs land here in the same PR as the change |
| 2026-07-09 | T0.1 repo home / module path | **`github.com/bmeddeb/phebs`** — under personal user; org names deferred until there's a reason |
| 2026-07-09 | T0.2 license | **Apache-2.0 confirmed**; LICENSE + copyright line land in t0-bootstrap |
| 2026-07-09 | T2.5 watch mode (local repos) | Live search over local working repos, built on the mirror pipeline: **HEAD-hash polling** (~3s exec-git per repo; HEAD-only indexing makes HEAD the exact signal — no fsnotify dep, no kqueue/recursive-watch trouble; revisit if watched-repo counts grow) → deduped sync job → existing sync→index chain. **Watched mirrors follow the source's checked-out branch** (that's the point of watching a working repo); detached HEAD keeps last good state. Plain absolute paths accepted for local clones (hardlink mirrors ≈ free). `sync.poll_interval` config makes the chain feel instant. Measured: commit → searchable in ~1.4s at 1s cadence |
| 2026-07-09 | T4.2 search latency budget | **Budget: p50 < 50ms** single-node (enforced in CI via `TestSearchLatencyP50`). Measured on the fixture corpus: p50 18µs, p95 31µs — in-process `zoekt/search.NewDirectorySearcher` (upstream renamed `shards`), no RPC hop. Realistic corpus numbers land with the P6 capacity spike |
| 2026-07-09 | T1.3 job-claim statement | **Optimistic conditional update wins** (select oldest pending LIMIT 1, then `UPDATE $cand SET status='claimed' … WHERE status='pending'`, no explicit txn). Spike, 200 jobs × 8 pollers on local surrealkv: both candidates **0 double-claims**; optimistic: 236 wasted attempts, p50 12.1ms, p95 16.1ms; explicit txn: 510 aborts, p50 15.9ms, p95 20.0ms. Lost race costs one empty read vs a server-side abort. Shipped as `Store.ClaimJob`; spike harness kept for P2 invariant/Elle reruns |
| 2026-07-09 | Embedded SurrealDB reality check (T1.2) | Official Go SDK is WS/HTTP only; `surrealdb.c.go` is v0.1.0 CGo requiring a manual Rust lib build. Pivot: **supervised local `surreal` child** on `surrealkv://`, loopback root creds, official SDK over WS. Single-command dev kept; CGo cross-compile risk deleted; LIVE SELECT unaffected; fleet cutover = same SDK, different URL. Revisit c.go if it matures |
| 2026-07-10 | Post-P1 roadmap: Sourcebot parity | Codified BACKLOG Epics 6–10 (Waves 0–4) from a public-sources feature comparison (free vs paid/EE, no upstream source/`ee/` read). Ordering by value-over-effort + dependency: quick wins → connectors/freshness → MCP+contexts differentiators → auth/code-nav → enterprise. Multi-branch `rev:` flagged architectural (HEAD-only is a P1 core assumption), sequenced last on demand. Non-goals reaffirmed: SCIM, multi-org RBAC, cloned Ask chat (MCP-first, single-tenant) |
| 2026-07-10 | T6.3 live PAT verification | GitHub adapter run end-to-end against a real PAT (200-repo account: `users:` 2-page pagination, `orgs:`, `repos:`, ~195 exclude globs → exactly the 5 kept repos synced/indexed/searchable, 4 of them private). **Finding fixed: `/users/{name}/repos` never returns private repos, even authenticated** — a `users:` entry naming the token's own login now resolves `GET /user` once and additionally lists `/user/repos`, unioned with the public listing (not replacing it — `/user/repos` alone omits public repos under a select-repositories fine-grained PAT, and a shrunken listing + `cleanup_orphans` would delete mirrors/shards; case-insensitive login match; regression test with a fake API). Verified live: private repo reached the index *through the users listing*; token absent from mirror config, data dir, and API responses. Rate-limit wait not trippable live (5k/hr headroom) — stays covered by unit tests |
| 2026-07-11 | T7.1 GitLab connector | Same adapter shape as GitHub with the REST plumbing extracted into a shared **`hostClient`** (Link-header pagination via generic `listPages[T]`, Retry-After/rate-limit waits, per-host Accept + Authorization values) — GitHub, GitLab, and Gitea all speak RFC 5988 Link + Retry-After, so one client serves Epic 7. GitLab specifics: `groups:` list with `include_subgroups=true`, user listing is requester-scoped (no GitHub-style public/private union needed, T6.3), clone auth = HTTP basic as the `oauth2` pseudo-user via the same redacted extraheader path, `url:` doubles as self-hosted base. Names are `<host>/<full/project/path>` (nesting is native); `safeName` guards server-supplied paths against `$DATA` escape. Fake-API e2e test; live gitlab.com run pending a real token |
| 2026-07-10 | T6.4 UI test harness | **Vitest + jsdom + Testing Library**, configured as a `test` block in the existing vite.config.ts — no separate config, no jest-dom/user-event deps. Components render real styletron+baseui; `lang`/`highlight` are mocked so tests stay pure-DOM and fast (23 cases, <1s). Keyboard tests drive `window` keydown (what the handlers bind); router asserted via `location.hash` (jsdom-native). `npm test` / `make ui-test`; CI wiring deferred to the first CI pipeline |
