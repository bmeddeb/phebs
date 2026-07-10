# phebs · Sourcebot port map

Analyzed: `sourcebot-dev/sourcebot@2afc265a` (2026-07-07, HEAD of main).
Sync your clone to match: `git fetch origin && git checkout 2afc265a9c35a7af1173cf9a9cb2be3b675e1f59`
Their zoekt fork: `sourcebot-dev/zoekt` @ 2026-07-01 — light tracking fork of
`sourcegraph/zoekt` (module path unchanged, `go.mod:1`; latest commit is a dep CVE bump).

---

## 1. License verdict — gates everything (`LICENSE.md`, `ee/LICENSE`)

**Core is FSL-1.1-ALv2** (Functional Source License, Sentry-style), **not** MIT.
**Everything under any `ee/` folder is proprietary** — requires a paid enterprise
agreement; modifications are assigned back to Taqla Inc. (`ee/LICENSE`).

What FSL means for a port (from the license text, not legal advice):

- **Permitted**: internal use, self-hosting, modification, redistribution — for any
  purpose *except* a "Competing Use": a commercial product/service that substitutes
  for Sourcebot or offers substantially similar functionality.
- **Derivatives inherit the terms**: "The Terms and Conditions apply to all copies,
  modifications and derivatives." A translated port that reuses their code/UI/assets
  is a derivative → phebs would carry the FSL non-compete and could never be
  commercialized as a code-search product.
- **Per-version Apache-2.0 conversion, 2 years after release.** First release
  v1.0.0 shipped 2024-10-01 (`CHANGELOG.md:1568`) → converts 2026-10-01. The HEAD
  you'd actually want converts **July 2028**. Nothing is Apache today.

**Disposition: reference-only discipline.**

1. Never copy code, React components, CSS, or schema files from the repo.
2. Never open anything under `ee/` paths (list in §7) — different, stricter license.
3. Implement from observed behavior + docs + API shapes. Formats and interfaces
   aren't copyrightable expression; their implementation is.
4. Depend on **upstream `sourcegraph/zoekt` (Apache-2.0)** directly, not their fork.
5. Result: phebs licenses freely (MIT/Apache-2.0), no non-compete attached.

This is cheap discipline here: PLAN.md v2.2 already replaces their DB (SurrealDB vs
Postgres+Prisma), queue (polling vs Redis/BullMQ), API layer (huma vs Next.js), and
zoekt integration (in-process vs gRPC+subprocess). The only surfaces where copying
would even be tempting are the React UI and the JSON config schemas — both handled
below (§7, §8). If phebs ever heads commercial, get a real legal read.

---

## 2. Upstream topology (as-is)

Three supervised processes in one container (`supervisord.conf`) plus two databases
(`docker-compose.yml`):

- **zoekt-webserver** `-index $DATA_CACHE_DIR/index -rpc` — search over shard files
- **web** — Next.js 16.2 / React 19.2 / NextAuth v5-beta (`packages/web/package.json`)
- **backend** — sync + index worker (Node)
- **postgres:16** — ~30 Prisma models (`packages/db/prisma/schema.prisma`)
- **redis:8** — BullMQ job queues

Data flow:

- Search: web → zoekt-webserver **gRPC `StreamSearch`**
  (`packages/web/src/features/search/zoektSearcher.ts:150-204`), query parsed by a
  Lezer grammar → IR → zoekt `Q` proto
  (`packages/web/src/features/search/README.md`).
- Indexing: backend → **`execFile('zoekt-git-index', …)`**
  (`packages/backend/src/zoekt.ts:31`).
- Jobs: BullMQ `connection-sync-queue` (`connectionManager.ts:18`) and
  `repo-index-queue` (`repoIndexManager.ts:21`).

## 3. Package inventory & disposition

| Package | LOC | What it is | phebs disposition |
|---|---|---|---|
| `backend` | 10,979 | Connection sync, repo compile, index manager, host adapters (github, gitlab, gitea, gerrit, bitbucket, azuredevops, generic git) | **Re-implement core in Go.** P1: github + generic git adapters only |
| `web` | 100,667* | Next.js app: search UI, browse, chat, agents, settings, onboarding | **Rebuild minimal UI** (Vite+React+CM6). *Search core is only 1,522 LOC* (`features/search/`), codeNav 248 LOC — the rest is chrome, chat, EE, and generated protos |
| `queryLanguage` | 855 | Lezer grammar → zoekt `Q` IR | **Don't port.** Use zoekt's own `query.Parse` (Go); add a thin filter layer (§9) |
| `schemas` | 24,613 | Generated TS from `schemas/v3/*.json` (15 files: per-host connection configs, app settings, searchContext) | **Don't copy.** Write own config schema; optional importer (§8) |
| `db` | — | Prisma schema, ~30 models | **Re-model in SurrealDB**, P1 subset in §5 |
| `shared` | 2,176 | Types, entitlements, utils | Absorb what P1 needs into Go types |
| `setupWizard` | 2,859 | Config CLI wizard | Skip for P1 |

\* includes generated gRPC proto TS under `web/src/proto/`.

## 4. The collapse — boundaries removed

| Upstream mechanism | phebs mechanism (all in-process, Apache-2.0 deps) |
|---|---|
| Lezer parse → IR → gRPC `Q` proto | `zoekt/query.Parse()` → `query.Q` directly |
| gRPC `StreamSearch` to zoekt-webserver | `zoekt.Searcher.StreamSearch()` over `shards.NewDirectorySearcher` |
| `zoekt-git-index` subprocess (separate install) | same-SHA `zoekt-git-index` child built from our go.mod (OOM isolation); search stays in-process |
| Redis + BullMQ queues | SurrealDB job tables + jittered polling (PLAN.md) |
| Postgres + Prisma | SurrealDB 3.0, **embedded** (`surrealkv://`) single-node; server mode in fleet |
| Next.js API routes | huma (OpenAPI free — upstream also exposes `/api/openapi.json`, parity is natural) |
| 5 runtime components | **1** single-node (embedded DB in the phebs binary); 2 only in fleet/server mode |

Fleet note: upstream zoekt already ships the `webserver/v1` gRPC protos phebs' web
tier consumes nothing of — but they're a candidate **peer protocol for fleet
scatter-gather**: battle-tested, typed, and already model zoekt result semantics.
Decide vs custom proto in PLAN.md.

## 5. Data model — P1 subset (of ~30 upstream models)

| Upstream (Prisma) | phebs (SurrealDB) | Notes |
|---|---|---|
| `Repo` | `repo` | Port fields nearly 1:1: name, displayName, cloneUrl, webUrl, defaultBranch, isFork/isArchived/isPublic, metadata, indexedAt, indexedCommitHash, latestIndexingJobStatus, pushedAt, external_{id,codeHostType,codeHostUrl} (`schema.prisma` model Repo) |
| `Connection`, `ConnectionSyncJob` | `connection`, `connection_sync_job` | Job tables double as the polling queue |
| `RepoToConnection` | graph edge or `repo_connection` table | SurrealDB record links fit |
| `RepoIndexingJob` | `indexing_job` | status enum ports directly |
| `SearchContext` | defer to P2 | `context:` filter depends on it |
| `User`, `ApiKey` | minimal `user`, `api_key` | single-user + token auth for P1 |
| **Drop for P1** | — | Chat/Attachment/ChatAccess (AI chat), OAuth* (they're an OAuth provider for MCP), ScimToken, Audit, License, ServicePingEvent, McpServer*, AccountPermissionSync* (EE), Invite/AccountRequest/Org multi-tenancy |

## 6. HTTP surface for functional parity (`web/src/app/api/(server)/`)

Core (P1): `search`, `stream_search`, `source`, `repos`, `repo-status`, `tree`,
`folder_contents`, `files`, `health`, `version`, `openapi.json`.
P2: `find_definitions`, `find_references`, `blame`, `commit`, `commits`, `diff`,
`webhook`. Never (or independent later): everything under `(server)/ee/`.

Streaming: upstream `stream_search` wraps the gRPC stream into a web
`ReadableStream` (`zoektSearcher.ts:150`); phebs equivalent is SSE straight off
`Searcher.StreamSearch` callbacks.

## 7. EE exclusion list — never open, never copy

`ee/` · `packages/backend/src/ee/` (account/repo permission syncers) ·
`packages/web/src/ee/` (analytics, audit, chat, codeNav-advanced, lighthouse, mcp,
membership, oauth, scim, sso) · `packages/web/src/app/api/(server)/ee/` ·
`packages/web/src/app/(app)/chat/components/shareChatPopover/ee/`.

Note the split: basic find-refs/defs lives in core (`features/codeNav`, 248 LOC);
the advanced variant is EE. Permission-aware search filtering is EE — phebs P1
skips permission filtering entirely (self-hosted, trusted-user posture).

## 8. Config compatibility decision

Their config is 15 JSON Schema files (`schemas/v3/`): per-host connection specs +
app settings + searchContext. Recommendation: **define phebs' own schema**
(YAML/JSON, huma-validated), then ship an optional `phebs import sourcebot-config`
that maps their documented fields. Compatibility of *format* by independent
implementation is fine; copying their schema files (with descriptions) is not.

## 9. Query language decision

Adopt **zoekt native syntax** (`repo:` `file:` `lang:` `case:` `sym:` `branch:`
`-negation` etc.) via `query.Parse` — zero grammar code. Add a pre-pass for
Sourcebot-isms resolved against the DB before handing to zoekt:
`context:X` → repo set expansion (P2), `archived:`/`fork:`/`visibility:` → repo
metadata filters compiled to `query.RepoSet`. Their Lezer grammar exists mainly
because TypeScript has no zoekt parser — Go gets it free.

## 10. P1 slice proposal (maps to README roadmap)

1. **Skeleton**: huma server, SurrealDB schema (§5), config loader (own schema).
2. **Sync**: github + generic-git adapters → `repo` rows + clone/fetch to disk;
   `connection_sync_job` polled with jitter.
3. **Index**: `indexing_job` consumer → `gitindex` in-process → shard dir;
   HEAD-only per PLAN.md.
4. **Search**: `query.Parse` + repo-metadata pre-pass → `shards.DirectorySearcher`
   → SSE `stream_search` + JSON `search`.
5. **UI**: single search page + file viewer (CM6 read-only, match decorations),
   `repos` status page.

Defer: gitlab/gitea/gerrit/bitbucket/ADO adapters, contexts, codeNav, blame/diff,
fleet mode, any auth beyond one API token.

## 11. Open decisions

- Fleet peer protocol: reuse zoekt `webserver/v1` gRPC protos vs custom proto.
- Config: importer scope (connections only vs settings too).
- StreamSearch backpressure semantics under SSE (chunk flush cadence).
- SurrealDB job-claim statement shape (optimistic `UPDATE … WHERE status` vs
  record-lock pattern) — benchmark under the jittered-polling design.
- Whether `metadata Json` on repo stays schemaless or gets typed per host.

## 12. EE feature counter-plan (build-our-own map)

Canonical EE surface from FSL-licensed entitlement constants
(`packages/shared/src/entitlements.ts:34-47`) + license-gated docs pages.
Implementations under `ee/` stay unread; everything below is specced from
observed behavior and public docs — that discipline is what keeps independent
reimplementation clean.

| Entitlement | What it is | phebs move |
|---|---|---|
| `search-contexts` | Named repo groupings, `context:` filter | **P2.** `search_context` table + RepoSet pre-pass (§9). It's a WHERE clause |
| `mcp` | MCP server (paywalled upstream) | **P2, OSS.** search / read-file / list-repos tools off the same internals |
| `github-app` | GitHub App auth (installation tokens, rate limits, webhooks) | **P2.** `ghinstallation` + `/webhook` route → event-driven reindex; beats polling |
| `code-nav` | Refs/defs beyond the search-based core | **P3.** Base: zoekt `sym:`. Exceed: SCIP ingestion (Apache-2.0) for precise nav |
| `sso` | OIDC/SAML login | **P3.** `coreos/go-oidc`, no seat gating. SAML only on demand |
| `audit` | Admin/user action log | **P3.** Append-only SurrealDB table + huma middleware; near-free |
| `analytics` | Usage dashboards | **P3.** Events table + aggregations, one minimal page |
| `oauth` | OAuth provider (for remote MCP clients) | Skip until remote MCP demands it (`ory/fosite` if ever) |
| `permission-syncing` | Mirror code-host ACLs into search visibility | Skip; **reserve the per-user RepoSet hook now** so it's a feature later, not a rearchitecture |
| `org-management` | Multi-org, roles, seats, invites | Never — single-tenant by design |
| `scim` | IdP user provisioning | Never until paid demand |
| `ask` | LLM chat over the codebase | Don't clone the chat app. MCP-first: agents bring their own chat |

Also gated upstream: anonymous access is an offline-license flag
(`entitlements.ts:15`) — in phebs it's a config bool. "Lighthouse" under `ee/`
is their license-validation backend (see warning at `entitlements.ts:30-32`),
not a user feature: phebs deletes the entire licensing/entitlement/banner code
class outright.

---

## 12. EE feature matrix — hand-roll targets

Canonical entitlement list: `packages/shared/src/entitlements.ts:34-47` (FSL core).
EE-gated docs pages confirm scope (`docs/**` including `license-key-required` snippet).
Clean-room rule: feature *behavior* below is derived from entitlement names, docs
pages, and file names only — never open `packages/backend/src/ee/` or
`packages/web/src/ee/` implementations. Build from public specs and libraries.

| Entitlement | Function | phebs implementation | Priority |
|---|---|---|---|
| `search-contexts` | Named repo groups, `context:` filter | Table + `query.RepoSet` pre-pass (§9) | P2, trivial |
| `code-nav` | Refs/defs/go-to-def | zoekt's built-in universal-ctags symbols + `sym:` queries | P1-adjacent, near-free |
| `permission-syncing` | Mirror code-host ACLs, per-user filtering | Host-API ACL sync → repo↔user edges → compile into `RepoSet` at query time (no post-filter) | P2/P3, the hard one; design into schema now |
| `github-app` | GitHub App auth vs PATs | `ghinstallation` | P2 |
| `sso` | OIDC/SAML | `coreos/go-oidc`, `crewjam/saml` | P2 |
| `scim` | IdP user provisioning | RFC 7643/7644, `elimity-com/scim` | P3 |
| `audit` | Action log + retention | Append-only SurrealDB table + huma middleware | P2, trivial |
| `analytics` | Usage dashboards | Local aggregation; **zero telemetry** (upstream core ships `posthog.ts` phone-home — deliberate phebs divergence) | P2 |
| `org-management` | Multi-org RBAC | Skip — single-org posture (§7) | — |
| `oauth` | OAuth 2.0 provider (for their MCP auth) | Skip — API tokens | — |
| `mcp` | MCP server over search | First-class in binary: search/source/tree/symbols tools, token auth | P2, differentiator |
| `ask` | LLM chat over code | Anthropic API + search-as-retrieval | P3 |

Strategic read: two entitlements are paywalled WHERE clauses, one paywalls
functionality zoekt ships natively (ctags symbols); the durable moat is
permission-aware search — which is the §5 schema's job to make native, not bolted-on.
