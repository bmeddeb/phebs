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
| Auth | **Always-on DB-backed auth:** SCS sessions, Argon2id local users, SHA-256 hashed/revocable API keys, optional `coreos/go-oidc`; no open application mode | — |
| MCP | official `modelcontextprotocol/go-sdk`; **MCP-first product layer** (v2.3) — ten search/read/SCIP/history tools, DB-backed bearer auth; agents bring their own chat | — |
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
- **P3 — Clean-room SPA + SSO. DONE (2026-07-11).** End-to-end React search
  UI plus local/session/API-key auth and `coreos/go-oidc`; zero FSL-derived
  frontend code. ≈ BACKLOG Epics 5 + 9.1–9.2.
- **P4 — Product layer, MCP-first. CORE DONE (2026-07-11).** Ten-tool MCP
  server plus committed SCIP definition/reference/hover and Git history;
  verified core agent flow from Claude Code. Ask-style chat remains optional,
  with agents expected to bring their own interface. ≈ BACKLOG 8 + 9.3–9.4.
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
| 2026-07-10 | T6.4 UI test harness | **Vitest + jsdom + Testing Library**, configured as a `test` block in the existing vite.config.ts — no separate config, no jest-dom/user-event deps. Components render real styletron+baseui; `lang`/`highlight` are mocked so tests stay pure-DOM and fast (23 cases, <1s). Keyboard tests drive `window` keydown (what the handlers bind); router asserted via `location.hash` (jsdom-native). `npm test` / `make ui-test`; CI wiring deferred to the first CI pipeline |
| 2026-07-11 | T7.1 GitLab connector | Same adapter shape as GitHub with the REST plumbing extracted into a shared **`hostClient`** (Link-header pagination via generic `listPages[T]`, Retry-After/rate-limit waits, per-host Accept + Authorization values) — GitHub, GitLab, and Gitea all speak RFC 5988 Link + Retry-After, so one client serves Epic 7. GitLab specifics: `groups:` list with `include_subgroups=true`, user listing is requester-scoped (no GitHub-style public/private union needed, T6.3), clone auth = HTTP basic as the `oauth2` pseudo-user via the same redacted extraheader path, `url:` doubles as self-hosted base. Names are `<host>/<full/project/path>` (nesting is native); `safeName` guards server-supplied paths against `$DATA` escape. Fake-API e2e test; live gitlab.com run pending a real token |
| 2026-07-11 | T7.2 Gitea connector | Third `hostClient` consumer; Gitea's API is a near-superset of GitHub's shape so the adapter is ~120 lines. `url:` required (no canonical hosted Gitea). API auth `Authorization: token …`; clone auth HTTP basic with the PAT as username + empty password (Gitea resolves basic usernames as tokens). **Verified live, not just fake-API**: throwaway `gitea/gitea:1.26` container, seeded private org/repo via API, phebs synced → indexed → searched it; token absent from the data dir. Container + scratch state destroyed after |
| 2026-07-11 | T7.3 GitHub App auth | **No `ghinstallation`/go-github dependency**: the whole exchange is a stdlib RS256 JWT (crypto/rsa, ~40 lines, PKCS#1+PKCS#8 keys) + one POST `/app/installations/{id}/access_tokens`, re-run per sync so tokens are always fresh — a cache would add staleness risk to save one HTTP call per run. App mode skips `/user` resolution (installation tokens have no user identity) and, with no selectors, lists `GET /installation/repositories` (wrapper-object pagination) = exactly the granted repos. Installation tokens ride the existing x-access-token git path. ponytail ceiling: a single sync outliving the ~1h token fails remaining fetches and heals next run |
| 2026-07-11 | T7.4+T7.5 webhooks & re-sync cadence | Push webhooks get their own **`repo_fetch_job`** kind (fetch one mirror + chain index) instead of re-running a connection sync — a push must not cost a full host listing. The webhook handler lives outside huma (HMAC over raw body bytes IS the auth; no bearer), 404s without a configured secret, names repos via `RepoName(payload.repository.clone_url)` (host-agnostic — **Gitea's GitHub-compat headers verified live**: container webhook → push → searchable with re-sync disabled). Clone credentials for the fetch path are rebuilt via a shared `cloneAuth` (also de-duplicates the three adapters' inline auth). T7.5 is a ticker enqueuing `JobSync` for remote connections (`sync.resync_interval`, default 1h, "0" off); `EnqueueUnlessInFlight` is the debounce; local repos stay watch/boot-owned. GitLab webhooks (different scheme: X-Gitlab-Token) deferred; cadence covers them |
| 2026-07-11 | T8.1 search contexts | Config-defined only — **no `search_context` table** (the BACKLOG's "CRUD or config-defined" choice): config is already the source of truth for connections, contexts are the same kind of operator intent, and a table + CRUD adds drift risk with no UI to drive it. `context:` is stripped **string-level before `query.Parse`** (zoekt has no such atom; quoted spans respected), resolved against `ListRepos` by `path.Match` glob, AND'd as one RepoSet; multiple atoms union; negation rejected (a context is a scope, not a predicate). Unknown name = hard error, not empty results |
| 2026-07-11 | T8.2+T8.3 MCP server | Official `modelcontextprotocol/go-sdk` (locked in §1), mounted at **`/api/mcp` on the existing mux** using Streamable HTTP and the API's DB-backed bearer/session authentication. Schemas are inferred from Go types. The original three tools (`search_code`, `read_file`, `list_repos`) remain; Epic 9 adds seven SCIP/history tools. `read_file` is ranged and caps output at 200 KB with `truncated`; binary files and blobs over 10 MiB are tool errors. **Core AC verified live**: headless Claude Code listed → searched → read a fixture needle; Epic 9 tools are protocol-tested over real committed SCIP/Git fixtures. |
| 2026-07-11 | Post-Epic-8 credential boundary | Clone URLs are identifiers, never secret containers. Generic private HTTP Git uses nested `http_auth.username/password`, injected per process. HTTP userinfo/query/fragment, opaque HTTP spellings, password-bearing SSH, and other-scheme userinfo fail config validation; every referenced secret env var must be present and non-empty. Startup scrubs all legacy origin/push URLs and DB values, and a failed credential audit stops service startup. Host API pagination and redirects remain same-origin, cycle/page bounded, and rate-limit waits are capped. |
| 2026-07-11 | Queue freshness and lease fencing | A unique `pending_key` provides one pending successor per kind/target; events during active work merge into it and `force` only upgrades. Claims get random lease tokens. All worker writes, shutdown release, and stale reaping are fenced by lease/owner; the reaper additionally matches the heartbeat it observed so it cannot steal a recovered worker. Legacy unfenced/duplicate rows migrate before the unique indexes are installed. Connection membership replacement is transactional. |
| 2026-07-11 | Artifact identity and cleanup | Search always intersects the query with DB rows that are indexed and not deleting, so stale/untracked shards fail closed. Startup audits repo rows, shard metadata/revisions, and persisted/mirror URLs. Credential quarantine failures abort startup; unreadable shard snapshots are preserved and make index-state repair non-destructive until a complete audit succeeds. Revision repair takes the same per-repo lock as publication and re-reads both DB and shards under it. Orphan deletion marks the row, cancels pending work, takes that lock, removes shards/mirror, then deletes the row; any post-mark failure rolls the row back to active with a bounded uncancelled context. Persisted names are canonicalized and checked for traversal, symlinks, case aliases, and legacy `.git` layout collisions. Locks are cancellable, reference-counted, idle-evicted, and keyed by the same canonical artifact identity. Shard enumeration uses literal directory reads, never glob-expanded data paths. |
| 2026-07-11 | Indexed revision contract | Search file results carry zoekt's immutable commit `ref`; UI source/tree links preserve it and no-ref routes first resolve `indexed_commit_hash` or fail closed. Search also verifies each zoekt result's embedded version equals the DB revision, closing the shard→DB publication window. MCP `read_file` defaults to the same stored commit. Unindexed rows are not searchable/listable. A failed state commit clears/removes the uncommitted index, and startup repairs shard/DB version mismatches with a forced reindex. |
| 2026-07-11 | DB-backed authentication boundary | Application UI/API/MCP authentication is always on; public health/discovery/metrics and HMAC webhooks are explicit separate boundaries. Surreal tables hold users, API-key hashes, and SCS sessions. Local passwords use atomically reserved, globally concurrency-bounded Argon2id; malformed request bodies release their reservation without charging the credential-failure quota. API keys and stored session tokens use SHA-256; unsafe cookie requests require per-session CSRF. Key management is browser-session only, and operational reindexing requires an administrator. Login/OIDC client buckets use the direct peer unless it belongs to an explicit `auth.trusted_proxies` CIDR, in which case `X-Forwarded-For` is walked right-to-left to the first untrusted address. First-admin enrollment is either an ephemeral in-memory setup token or one-time config bootstrap. `auth.api_key` is migration-only: import its hash as a legacy principal, replace it with named revocable keys, then omission deletes it. |
| 2026-07-11 | OIDC identity model | One `coreos/go-oidc` provider is discovered at startup (fail closed). Authorization code + PKCE S256, state, nonce, ID-token/access-token verification, and `email_verified=true` bridge into the same SCS session/user model. Identity binds only to issuer + subject; email equality never auto-links accounts, and any collision with a different local or OIDC identity fails closed. The first user is administrator, so provider-side enrollment is the single-tenant membership policy; SAML/SCIM remain out of scope. |
| 2026-07-11 | Committed immutable SCIP | Precise definition/reference/hover data is the repository's root `index.scip` blob at the exact full object ID recorded by indexing, not an uploaded mutable side index. The service lazily loads revision snapshots into a byte-budgeted LRU; missing data is a graceful unavailable state and malformed immutable indexes are negative-cached. Occurrence-local invalid ranges are skipped without discarding valid results, with bounded candidate oversampling; relationship expansion is direct, protocol-direction-aware, and non-transitive. Full-OID/path validation, a 64 MiB index cap, per-file/aggregate source budgets, semantic and hover limits, UTF-8/16/32 conversion, bounded deterministic top-500 references, and a 512 MiB accounted cache budget bound the trust and resource surface. |
| 2026-07-11 | Git history plumbing | Blame, rename-following commit lists, commit detail, and diffs read existing bare mirrors. Mutable commit-ish inputs resolve once to immutable commit/blob OIDs; paths are literal and share `SafeRepoDir` validation. Commit views use first-parent diffs (root vs empty tree), mark NUL-bearing binary files, normalize other invalid text bytes for JSON, cancel Git producers at hard output limits, and share aggregate metadata budgets; blame also rejects source blobs over 10 MiB. Diff context defaults to three only when omitted and honors explicit zero. API, UI, and MCP default omitted history refs to the DB's indexed revision so related reads cannot drift with mirror HEAD; UI pagination aborts and generation-checks in-flight pages across navigation. |
| 2026-07-11 | T10.1 audit log | Append-only `audit_event` table behind a narrow `AuditStore` interface (AuthStore precedent; append-only is enforced Go-side by exposing no update/delete). Two recording points because the auth surface bypasses huma: a huma middleware records every mutating operation by operation ID (auto-covers future endpoints; handlers annotate targets via an in-context container), and the `/api/auth` handlers call an injected recorder with precise actor/outcome (incl. failed local logins). One `main.go` closure feeds both, resolves the actor from the request principal, writes synchronously via `context.WithoutCancel` (a disconnect must not lose a completed action), and never fails the request. Source IP reuses the trusted-proxy resolver on the auth surface; direct peer elsewhere. Webhook deliveries deliberately unaudited (HMAC machine traffic, no principal, effects visible as jobs). `audit.retention` (default 90d, "0" forever) prunes at boot + 12h ticker. Admin-gated `GET /api/audit` (offset/has_more paging) + `#/audit` page. |
