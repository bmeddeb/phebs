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
- an optional **Buf compatibility child**, pinned by the same Go module and
  sandboxed per request when experimental contract intelligence is enabled;
- an **in-process searcher** over the shard directory, streaming results;
- **DB-backed authentication** with browser sessions, revocable API keys, and
optional OpenID Connect;
- **SCIP code navigation and Git history** read at immutable commit IDs from
the same bare mirrors;
- the **web UI** (React + Base Web + CodeMirror), embedded in the binary.

Indexing is **HEAD-only by default**: the default branch of each repo (or, for
watched local repos, whatever branch is checked out). An explicit per-repo
allowlist can add up to seven branch/tag revisions, selected with `rev:`.

## 2. Install & first run



### Prerequisites


| Requirement                        | Why                                                                  | Install                                                                                |
| ---------------------------------- | -------------------------------------------------------------------- | -------------------------------------------------------------------------------------- |
| `git`                              | clone/fetch mirrors, serve file content                              | usually present                                                                        |
| `surreal` (SurrealDB ≥ 3.0)        | the state/queue database child                                       | `brew install surrealdb/tap/surreal` or `curl -sSf https://install.surrealdb.com | sh` |
| Go ≥ 1.26                          | build from source                                                    | go.dev/dl                                                                              |
| Node ≥ 24                          | build the web UI                                                     | nodejs.org                                                                             |
| `universal-ctags` *(optional)*     | symbol search (`sym:`) at index time                                 | `brew install universal-ctags`                                                         |
| language SCIP indexer *(optional)* | precise definitions/references/hover; commit its `index.scip` output | [scip-code.org](https://scip-code.org/)                                                |
| `bubblewrap` *(Linux, optional)*    | network/filesystem namespace for the experimental Buf compatibility child | distribution package `bubblewrap`; macOS uses built-in `sandbox-exec`                 |

Release verification uses the exact tool versions recorded in
`.go-version`, `.node-version`, `.golangci-lint-version`, and
`.surrealdb-version`. Ordinary development supports the broader prerequisite
ranges above; the `make ci-*` targets fail early when the release toolchain
does not match.



### Build and run

```bash
git clone <your-clone-of-phebs> && cd phebs
make build          # builds the UI, zoekt and Buf children, and ./phebs
./phebs version     # 0.1.0-dev for an ordinary source build
./phebs serve -config phebs.yaml
```

`make build VERSION=vX.Y.Z` creates a release-identified binary. The same
exact value is printed by `phebs version`, returned by `/api/version`, written
to backup manifests, and included in startup logs. Release builds refuse a
non-SemVer `VERSION`.

To assemble and exercise the distributable directory with the exact pinned
release toolchain:

```bash
make release verify-release smoke-release VERSION=v0.1.0
```

The result is `dist/phebs-v0.1.0-<goos>-<goarch>/` containing `phebs`,
same-module `bin/zoekt-git-index` and `bin/buf` children, `LICENSE`,
`README.md`, the ready-to-run `phebs-otel-demo.yaml`, and
`release-manifest.json`. The canonical manifest binds the version, source
commit, target, Go toolchain, stable installed modes, sizes, and SHA-256
digest of every payload. `verify-release` rejects missing, additional,
symlinked, mode-changed, or byte-modified payloads. The manifest is an
integrity inventory, not a signature or independent proof of who built it.

`smoke-release` requires `git` and the exact `.surrealdb-version` binary on
`PATH`. It verifies the bundle before starting anything, creates an empty
temporary data directory and local Git fixture, then proves bootstrap login,
sync → index → search, and immutable source/folder browsing. It also removes
all development fixture variables and requires the authenticated capability
list and `/api/contract_atlas` route to retain the default-dark posture. The
temporary repository and data directory are deleted after shutdown.

For `v0.1.0`, the hosted `Release bundle and fresh-data smoke` job is part of
the required `ci` workflow. From a clean checkout it performs two independent
Linux/amd64 builds, compares their manifests, runs the empty-data smoke, then
retains a deterministic `.tar.gz` and adjacent `.sha256` file. The release
archive is not accepted from a local workspace.

This single-maintainer repository uses a documented release gate in place of
branch protection: an annotated release tag may be created only when its exact
`main` commit has a successful **push** run of all five named jobs in
`.github/workflows/ci.yml`, including the release job. The tag commit and run
SHA must match byte-for-byte; tags are never force-moved. Release notes must
link the run and checksum and state that Contract Atlas and proof features are
default-dark, provisional, and do not establish the closed
`NOT_ESTABLISHED` accuracy gate.

The published
[`v0.1.0`](https://github.com/bmeddeb/phebs/releases/tag/v0.1.0) binary bundle
is Linux/amd64 only. Its archive SHA-256 is
`63103500a6b86aa3e4533fb1693065009585f6be509e48aab7b26373405daaf6`.
macOS users build the exact tag from source with the pinned Go and Node
versions; this first release does not provide a signed or notarized macOS
binary.

#### OpenTelemetry microservices evaluation

The repository and release bundle include `phebs-otel-demo.yaml` as the
canonical public microservices evaluation. From the release directory:

```bash
./phebs serve -config phebs-otel-demo.yaml
```

Open `http://127.0.0.1:3071`, complete first-administrator setup with the
one-time token printed in the server log, and allow the initial sync and index
to finish. The configuration clones the public
`github.com/open-telemetry/opentelemetry-demo` monorepo and keeps its state
isolated under `~/.phebs-otel-demo`. It deliberately enables
`experimental.provisional_proto_extraction`, so the authenticated UI can
expose Contracts / Contract Atlas after eligible evidence publishes. This is
an evaluation posture: the extracted protobuf/gRPC relationships remain
provisional, default deployments remain dark, unresolved relationships are
abstentions rather than guesses, and no empty result establishes runtime
absence.

#### Thrift protocol-pack evaluation

The repository includes `phebs-thrift-demo.yaml` as the Epic 19 Thrift
evaluation over the public Jaeger corpus. Run it without the synthetic Atlas
fixture that `make dev` injects (the fixture would override real catalog
evidence):

```bash
make ui bin/zoekt-git-index bin/buf
PHEBS_ZOEKT_GIT_INDEX="$(pwd)/bin/zoekt-git-index" \
PHEBS_BUF="$(pwd)/bin/buf" \
  go run -tags ui ./cmd/phebs serve -config phebs-thrift-demo.yaml
```

Open `http://127.0.0.1:3073`, complete first-administrator setup with the
one-time token printed in the server log, and allow sync, index, and
extraction to finish. State stays isolated under `~/.phebs-thrift-demo`. The
three connections exercise the pack's three postures deliberately:
`jaeger-idl` publishes `thrift-contract` declarations (open
`agent.Agent/emitBatch` in the Atlas for the `oneway` chip and the
wire-honest args/result shapes); the archived `jaeger-client-go` vendors
generated stubs and real call sites in one repository, so
`thrift-consumer` registrations and `/agent.Agent/emitBatch` caller evidence
join the declarations by name (never `proven` — provisional lineage);
and `jaeger` imports its stubs from another module, publishing an honest
empty consumer run. The protobuf packs are also enabled so the Atlas shows
both protocols side by side. All Contract Atlas caveats apply unchanged: no
empty result establishes runtime absence.

#### Evaluating a separated IDL/source monorepo

The current extractor walks the repository's complete regular-blob inventory;
directories have no built-in semantic meaning. A layout with declarations
under `idl/` and handwritten Go under `src/` is therefore eligible without
moving files or making declarations adjacent to callers. Generated Go stubs
must be committed somewhere in that same pinned repository for the current
syntactic gRPC/Thrift consumer readers to index them. For protobuf field
references, a repository-root `index.scip` must additionally describe that
same immutable revision. phebs never runs the repository's build, code
generator, plugin, or dependency downloader.

Epic 20's dark caller resolver can additionally consume three optional,
committed JSON snapshots from fixed repository-root paths:

- `layout-snapshot.json` version `t20-layout-snapshot-v1` classifies
  non-overlapping `idl`, `generated`, and `source` roots. IDL/generated roots
  name a protocol token; a source root does not.
- `unit-snapshot.json` version `t20-unit-snapshot-v1` maps one source path and
  optional exact `start_line` to separate arrays of `build_targets`,
  `deployables`, `logical_services`, and `owners`. Exact-line entries win over
  path-level entries.
- `generated-from-snapshot.json` version `t20-generated-from-v1` provides
  explicit `generated_path` → `declaration_path` mappings. Protobuf mappings
  may also bind a `generator_relative_path`, or an `invocations` entry may map
  a `generated_root` to a `generator_invocation_root`. Thrift requires direct
  mappings.

For example, the layout remains classification—not proof:

```json
{
  "version": "t20-layout-snapshot-v1",
  "roots": [
    {"kind": "idl", "path": "idl/proto", "protocol": "grpc"},
    {"kind": "generated", "path": "gen/proto", "protocol": "grpc"},
    {"kind": "source", "path": "src"}
  ]
}
```

The snapshots are ordinary repository blobs and inherit the 10 MiB per-file
and 512 MiB aggregate extraction budgets. Snapshot symlinks, unknown versions
or fields, unsafe/stale paths, dishonest state labels, and overlapping roots
fail closed. Referenced source/generated/declaration files must exist in the
same fully inventoried commit. Files outside every optional root remain in the
corpus inventory. A layout root alone never establishes generated-from:
protobuf still needs its agreeing generated markers plus an explicit direct or
invocation-root relation, and Thrift needs a direct relation.

One mapping is `resolved`; zero is `unavailable`; multiple mappings or a
multi-valued candidate are `ambiguous`. phebs retains every candidate rather
than choosing a build target, deployable, service, or owner. The trusted loader
returns exact repository/commit/path/blob-digest provenance and an attribution
digest; metadata changes move that digest without changing source assertion
identity. The later Caller Map cursor binds this digest. No snapshot causes
phebs to run a build, generator, plugin, binary, or catalog client, and the
current adapter performs no external lookup.

This support should not be confused with a service-aware migration inventory.
In the current release, the Atlas operation detail returns at most 200
relationship rows, the Impact operation form accepts a bare canonical
operation and builds a proof bundle capped at 5,000 assertions, and call sites
are not grouped into build targets, deployables, logical services, or owners.
The Go consumer readers are syntactic; common generated method names can
produce explicit abstentions. There is also a current vocabulary mismatch:
Contract Atlas qualifies a unique wire-name call as
`caller / unresolved_name_match`, while `contract-impact-report-v1` places
the same `CALLS_OPERATION` evidence under `known_consumers` for its bare
operation-string question. Neither label proves a caller-to-declaration
lineage. The default-dark Epic 20 Caller Map in `docs/BACKLOG.md` assigns the
experimental schema/vocabulary migration to T20.10 and specifies the typed,
paged, unit-attributed workflow needed for thousands of services. Until that
work lands, use the current pages as bounded provisional source evidence—not
as a complete migration roster. T20.1's neutral 10,010-call capacity/index
gate is complete; it changed no API, MCP, UI, or production extraction
behavior.

The current MCP annex has the same pre-Epic-20 limitation:
`find_operation_consumers` requires a caller-supplied bare canonical operation
and returns one bounded proof bundle. It does not discover exact declaration
identities, page a fleet-scale Caller Map, or compare old and replacement
endpoints. Planned T20.11 adds read-only discovery, exact-detail, and paged
caller tools over the same authorization-first services as HTTP; T20.13 adds
the comparison tool. Those tools will be bounded and cursor-driven, will not
silently broaden `find_operation_consumers`, and will not create a proof
bundle or Investigation during ordinary browsing.

T20.2 has removed the extraction worker's 5,000-fact bottleneck behind these
planned surfaces. The pure-reader SDK still emits one source-bound fact at a
time; the trusted worker groups accepted facts into deterministic,
content-addressed chunks of at most 256 and keeps the complete replacement
invisible until one guarded publication. Its independent limit is now 12,500
facts, and the frozen 10,010-call profile fits the 256 MiB worker-memory gate.
T20.3 now supplies the corresponding `t12-store-v5` 25,000-row production
admission and retains the store-derived atomic recount. Coverage certificates,
APIs, and serialized evidence remain unchanged. The retained target gate
published 20,020 stored rows in 145.348583 ms on the reference machine,
inside the frozen 2-second ceiling; this establishes capacity and atomic
integrity only, not extraction accuracy. The 25,000-row value is a frozen
ceiling for this target, not an open-ended admission increase: T20.5 remains
required before it can rise. `spike/t201/results.json` remains the explicitly
historical v4/pre-guard baseline. The opt-in T20.1 store harness now measures
the active writer and a complete 20,020-row target sweep using exact
production statements and limits. Its committed version-2 receipt is
`spike/t201/results-current-writer-v6.json`: v6 publication took 154 ms and
the complete target sweep took 1,130 ms on the reference machine, inside the
frozen 2-second gate. Its retained first-page field is the legacy
`ListAssertions` comparison probe; T20.4's exact reverse-page gate separately
returned 100 rows in 8.9935 ms after 1,616 composite-index candidates.

T20.5's separately reviewed v7 receipt is
`spike/t201/results-current-writer-v7.json`
(`sha256:f4b7e4e5…`). Resumable retention removed one 20,020-row target run in
42 fixed-size steps: 10,010 associations, 10,010 assertions, and zero shared
atoms. It took 1,897 ms with 265,093,120 bytes peak Surreal RSS, inside the
frozen 2-second / 512 MiB gates. These are reference-machine capacity and
integrity observations, not extraction-accuracy or universal performance
claims.

There is no Change Workbench in the current release. The available pieces are
separate: a human can browse a declaration in Contracts, carry its operation
to Impact, inspect cited matching/unresolved evidence and the coverage
certificate, then use Search, SCIP navigation, and History independently.
The rich Investigations page is currently a development fixture projection,
not a production ticket-intake or checklist workflow.

The current Impact labels also need qualification. `Known consumers` means
matching static `CALLS_OPERATION` or field-reference evidence; it is not a
declaration-lineage-proven logical-service roster. `Unresolved candidates`
means source evidence the extractor deliberately could not assign uniquely,
not a confirmed consumer or a failed run. `Coverage certificate` is the
deterministic receipt of which visible repository revisions and extractor
domains were covered, stale, failed, processing, unsupported, or bounded; it
is not an accuracy or completeness score. Proposed Epic 21 replaces those
primary presentation labels with mode-correct vocabulary, shows the
certificate beneath an **Analysis scope & gaps** summary, and adds accessible
hover/focus/tap help to every qualified section while retaining the exact
technical receipt.

Epic 21 is authorized for specifications, tests, synthetic demonstrations, and
production-unregistered/default-dark implementation only. Because it stores
its brief, snapshots, analysis artifacts, and human records under
Investigations, production Workbench creation, mutation, export, UI, and MCP
registration inherit Epic 16's still-unsatisfied `ESTABLISHED` validation plus
explicit pilot-continuation gate. Completing its implementation tickets cannot
clear that gate or support an external accuracy, migration-complete, or
safe-to-retire claim.

The planned Workbench checklist does not change T16.8 ReviewItems into tasks.
ReviewItems remain deterministic machine projections with no hand-creation or
mutation path. Workbench suggestions remain unaccepted; the human projection
uses immutable superseding Dispositions in five fixed categories. There is no
ChecklistItem/Task table, comment, assignment, due date, priority, or custom
state.

The current API-key model has no read/write capability distinction, so no
Workbench MCP mutation tool may ship on that model. T21.12 introduces an
explicit immutable `investigation:write` capability for newly created named
keys; existing named keys and the migration-only legacy key remain read-only
for these new mutations and must be replaced deliberately. The capability
never expands the owning user's authority. A leaked write-capable agent key
can attempt durable Investigation mutations as that user, so operators should
create one narrowly capable, individually revocable key per agent. Browser
session writes remain CSRF protected.

The existing `check_contract_compatibility` HTTP and MCP contracts continue to
return retained content-addressed proof bundles. Workbench compatibility is an
additive explicit Investigation analysis path and does not migrate, delete, or
reidentify those bundles. Proposed protobuf and Thrift preview files inherit
the production parser preflights: at most 4 MiB, 500,000 tokens, and 128
structural levels per file, in addition to aggregate/file-count and sandbox
limits.

Minimal `phebs.yaml`:

```yaml
server:
  addr: "127.0.0.1:3070"  # local quick start

auth:
  cookie_secure: false  # plain-HTTP localhost only; keep the default true under HTTPS

connections:
  - name: zoekt
    type: git
    url: https://github.com/sourcegraph/zoekt.git
```

On a fresh data directory, startup prints `first-run setup token: ...`. Open
[http://localhost:3070](http://localhost:3070), enter that token with an administrator email and a
password of at least 12 bytes, and the browser starts a persisted session.
The token exists only in process memory and stops working as soon as the
first user is created; treat the startup log as sensitive until then. The
repo syncs and indexes within one poll cycle
(≤ ~20 s by default); watch progress on the **Repos** page.

For unattended provisioning, configure `auth.bootstrap_user` instead. For
HTTPS deployments, omit `cookie_secure` (the secure default), keep phebs on a
private listener, and terminate TLS at a trusted reverse proxy.

`phebs serve` flags:


| Flag                   | Meaning                                                              |
| ---------------------- | -------------------------------------------------------------------- |
| `-config path`         | config file; omitted = defaults (no connections, data in `~/.phebs`) |
| `-addr 127.0.0.1:3070` | listen address, overrides `server.addr`                              |




## 3. Configuration reference

Config is a single YAML file, validated strictly at startup: unknown fields,
type mismatches, and semantic errors **fail fast with line numbers**. The
annotated example lives at [docs/config.example.yaml](./config.example.yaml).
`server.data_dir` must be a literal path without glob metacharacters.
Every referenced environment variable in a secret field must exist and be
non-empty; this applies to legacy API/webhook secrets, bootstrap passwords,
OIDC client secrets, PATs, inline App keys, and Git HTTP credentials. A
missing variable stops startup rather than silently weakening authentication.

```yaml
server:
  addr: "127.0.0.1:3070" # loopback listen address (default)
  data_dir: "~/.phebs"   # all state lives here (default)

auth:
  cookie_secure: true       # default; set false only for plain-HTTP local use
  session_lifetime: 12h     # absolute lifetime; sessions idle out after 30m
  # api_key: "${PHEBS_LEGACY_API_KEY}"  # migration only

sync:
  cleanup_orphans: false  # delete repos no connection claims (default off)
  poll_interval: 15s      # job-runner cadence; lower for snappier watch mode

connections:
  - name: my-conn         # required; unique; [a-z0-9-]+
    type: github | gitlab | gitea | git
    # ... see per-type fields below

# Optional: seven additional refs per repo; HEAD is implicit.
revisions:
  github.com/acme/api:
    release-1: refs/heads/release/1
    v1.4.0: refs/tags/v1.4.0
```


| Key                                         | Default          | Notes                                                                                                                                                             |
| ------------------------------------------- | ---------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `server.addr`                               | `127.0.0.1:3070` | loopback by default; explicitly configure a private proxy-facing address for deployment                                                                           |
| `server.data_dir`                           | `~/.phebs`       | `~` expands; created if missing                                                                                                                                   |
| `auth.api_key`                              | *(empty)*        | legacy migration key only; its SHA-256 hash is imported into the DB, and omission removes the legacy row; it does not make an empty configuration unauthenticated |
| `auth.cookie_secure`                        | `true`           | `Secure` session-cookie attribute; set `false` only for intentional plain-HTTP development                                                                        |
| `auth.session_lifetime`                     | `12h`            | absolute lifetime, Go duration from `15m` through `720h`; fixed idle timeout is 30 minutes                                                                        |
| `auth.trusted_proxies`                      | `[]`             | trusted reverse-proxy hop CIDRs, including the direct peer, allowed in `X-Forwarded-For` resolution for per-client auth throttling; never include client networks |
| `auth.bootstrap_user`                       | *(none)*         | optional one-time first local administrator; requires `email` and a password of at least 12 bytes                                                                 |
| `auth.oidc`                                 | *(none)*         | one OIDC provider; requires issuer/client/secret/redirect URL; HTTPS except loopback tests                                                                        |
| `sync.cleanup_orphans`                      | `false`          | see [orphans](#orphans-and-cleanup)                                                                                                                               |
| `sync.poll_interval`                        | `15s`            | Go duration; job pollers wake with ±50 % jitter around it                                                                                                         |
| `sync.resync_interval`                      | `1h`             | re-sync cadence for remote connections; `"0"` disables                                                                                                            |
| `webhook.secret`                            | *(empty)*        | enables `POST /api/webhook`; `${ENV}` expanded, fails closed on unset vars                                                                                        |
| `audit.retention`                           | `2160h`          | audit events older than this are pruned twice a day; `"0"` keeps them forever                                                                                     |
| `analytics.retention`                       | `8760h`          | local usage events older than this are pruned twice a day; `"0"` keeps them forever                                                                               |
| `proof_bundles.retention`                   | *(disabled)*     | positive Go duration expires proof bundles after their latest materialization; omission or `"0"` keeps them indefinitely                                         |
| `experimental.provisional_proto_extraction` | `false`          | development-only opt-in for the validation-gated readers described below; declarations/operation consumers retain provisional lineage                             |
| `experimental.provisional_thrift_extraction` | `false`         | development-only opt-in for the T19 Thrift declaration and Go-consumer readers described below; same provisional repo/path lineage posture                         |
| `experimental.provisional_thrift_field_extraction` | `false`   | independent development-only opt-in for T22's thriftrw and Apache Thrift field-reference reader over a committed root `index.scip`; no public query until T22.4   |
| `experimental.provisional_kafka_extraction` | `false`          | development-only opt-in for the T23 Kafka topic-evidence packs described below; abstention-dominant by design, same provisional repo/path lineage posture         |
| `permissions`                               | *(none)*         | presence enables permission-aware search (see [Permission-aware search](#permission-aware-search)); omit to keep every authenticated user seeing everything       |
| `revisions`                                 | `{}`             | repo name → `rev:` selector → full `refs/heads/*` or `refs/tags/*`; at most 7 additional refs per repo (8 including implicit HEAD)                              |




### Authentication

Authentication is always required for the UI, application API, and MCP. A
fresh installation has three supported enrollment paths:

1. **Interactive setup:** configure neither `bootstrap_user` nor OIDC. Copy
  the ephemeral setup token from the local startup log into the UI's
   first-run form. The first account is an administrator.
2. **Bootstrap user:** provision the first administrator from config:
  ```yaml
   auth:
     bootstrap_user:
       email: admin@example.com
       display_name: Phebs Admin
       password: "${PHEBS_BOOTSTRAP_PASSWORD}"
  ```
   The password is used only when the first user is created and is stored as
   an Argon2id hash. Remove the block afterward; changing it does not rotate
   the existing password. If users already exist and the configured email is
   absent, startup fails instead of creating a surprise administrator.
3. **OIDC:** configure one provider and use **Continue with SSO**. The first
  verified OIDC identity becomes administrator; later identities are regular
   users. The provider therefore owns enrollment policy for this single-tenant
   deployment.

Browser sessions live in SurrealDB and survive process restarts. The cookie is
`HttpOnly`, `SameSite=Lax`, `Secure` by default, and stores only a random
token whose SHA-256 hash is persisted. Unsafe cookie-authenticated requests
also require the per-session `X-CSRF-Token`; the UI supplies it. Login/setup
attempts reserve a per-client slot before password work (8 credential failures
per 5 minutes), and Argon2id work is globally capped at four concurrent hashes;
overload fails with `429` instead of growing memory without bound. By default
the client is the direct peer. Behind a reverse proxy, list every trusted proxy
hop CIDR, including the direct peer, under `auth.trusted_proxies`; forwarded
headers from all other peers are ignored, and trusted chains are walked from
the nearest proxy outward.

#### API keys and legacy migration

After signing in, open **Settings**, name a key, and copy the returned
`phebs_<id>.<secret>` token immediately; the secret is shown once and only its
SHA-256 hash is stored. Send it as `Authorization: Bearer <token>`. Keys are
individually revocable and their last-use time is recorded. Key listing,
creation, and revocation require a CSRF-protected browser session; bearer keys
cannot mint replacements or revoke sibling credentials.

Existing `auth.api_key` deployments continue to work during migration. At
startup phebs imports only that key's hash as `Legacy config key`. Create a
named key for each client, deploy those tokens, then remove `auth.api_key`;
the next startup deletes the legacy key row. The legacy principal has no user
identity and cannot manage named keys itself.

#### OpenID Connect

```yaml
auth:
  oidc:
    issuer_url: https://idp.example.com
    client_id: phebs
    client_secret: "${PHEBS_OIDC_CLIENT_SECRET}"
    redirect_url: https://phebs.example.com/api/auth/oidc/callback
    scopes: [groups]  # optional extras; openid/profile/email are automatic
```

Register the redirect URL exactly at the provider. Discovery happens during
startup and failure stops the server. The authorization-code flow uses PKCE,
state, and nonce, verifies the ID token and access-token hash when present,
and requires `email_verified=true`. Identities bind only to issuer + subject;
email equality never links an OIDC identity to an existing local or OIDC
account, and collisions fail closed. Anonymous authorization-flow
sessions expire after 10 minutes, starts are rate limited, and starting a new
flow never clears an already authenticated browser session.

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

Private HTTP(S) remotes use transient Basic auth:

```yaml
- name: private-git
  type: git
  url: https://git.example.com/team/repo.git
  http_auth:
    username: "${GIT_HTTP_USERNAME}"
    password: "${GIT_HTTP_PASSWORD}"
```

Both fields are required. Credentials are passed to each Git process and are
never written to the repo row, API, logs, or mirror config. HTTP URL userinfo,
query parameters, and fragments are rejected; migrate any
`https://user:password@host/repo.git` configuration to `http_auth`. SSH URLs
may retain a username such as `ssh://git@host/repo.git`, but not a password.

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

At boot, phebs ensures one pending sync job per configured connection. A sync
resolves the connection to repo rows, mirrors each
repo into `$DATA/repos/<host>/<path>.git`, and chains an indexing job per
synced repo. Re-syncs are incremental (`git fetch --prune`).

Beyond boot, syncs happen when:

- a **watched** local repo's HEAD moves (see below);
- the **re-sync cadence** fires (`sync.resync_interval`, default `1h`, `"0"`
disables): every remote connection is re-synced, collapsing overlap into
one pending successor — local repos are covered by boot and watch instead;
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

`watch: true` on a local git connection makes phebs poll the repo's HEAD and
each configured allowlisted ref (every ~3 s), then re-sync + re-index whenever
one moves. **HEAD commits, branch switches, and allowlisted branch/tag moves
trigger reindexing; uncommitted working-tree edits and non-allowlisted feature
branches do not.**

Watched mirrors **follow the branch you have checked out** — switch to
`feature`, commit, and search reflects `feature`. A detached HEAD (mid-rebase,
bisect) keeps the last good index until you land somewhere.

End-to-end latency is roughly `watch tick (≤3 s) + poll_interval + index time`. With `sync.poll_interval: 1s`, a commit is searchable in ~1–2 s.

### Orphans and cleanup

A repo no connection claims (you removed the connection or narrowed its
filters) is flagged **orphaned** on the Repos page and in `/api/repo-status`.
By default orphans are kept; set `sync.cleanup_orphans: true` to delete their
rows, mirrors, and index shards after each sync. Every startup audits repo
rows, mirror configs, and shard metadata even when deletion is disabled. It
scrubs legacy URL credentials, hides invalid/unsafe legacy rows, and repairs
DB/shard revision mismatches by forcing a new index. Any audit, quarantine, or
repair failure stops startup so unverified state is never served. Destructive cleanup remains gated by `cleanup_orphans` and only
touches validated, non-symlinked paths under the data directory.

## 5. Searching

phebs uses zoekt's native query language. Patterns are regular expressions;
plain text behaves like substring search. Filters and patterns combine with
implicit AND; prefix any atom with `-` to negate it.


| Syntax                       | Meaning                                                     |
| ---------------------------- | ----------------------------------------------------------- |
| `foo bar`                    | files containing `foo` AND `bar`                            |
| `"foo bar"`                  | the exact phrase                                            |
| `f[ou]+nc.*Parse`            | regular expression                                          |
| `case:yes Foo`               | case-sensitive (default: smart case)                        |
| `repo:zoekt`                 | repo name matches regex                                     |
| `file:\.go$` / `-file:_test` | file path matches / doesn't match                           |
| `lang:go`                    | language filter                                             |
| `sym:ParseQuery`             | symbol definitions (needs ctags at index time)              |
| `content:foo`                | match file content only (not paths)                         |
| `archived:yes|no`            | filter by repo archived state *(phebs, from repo metadata)* |
| `fork:yes|no`                | filter by fork state *(phebs)*                              |
| `public:yes|no`              | filter by visibility *(phebs)*                              |
| `context:backend`            | restrict to a named repo set *(phebs, see below)*           |
| `rev:release-1`              | select one allowlisted branch/tag revision *(phebs)*        |


Examples:

```
watchModeNeedle repo:my-project
"TODO(ben)" -file:vendor/ lang:go
sym:ClaimJob fork:no
case:yes Searcher file:internal/
ClaimJob context:backend
deprecatedCall rev:release-1 repo:acme/api
```


### Revision scopes

HEAD remains implicit and is the revision searched when a query has no `rev:`
atom. Additional revisions are admitted explicitly by full Git ref while the
left-hand key provides the query-facing selector:

```yaml
revisions:
  github.com/acme/api:
    release-1: refs/heads/release/1
    v1.4.0: refs/tags/v1.4.0
```

This indexes `HEAD` plus the two named refs. `deprecatedCall rev:release-1`
searches the release branch only in visible repositories that published that
selector; `rev:v1.4.0` selects the tag. Selectors and refs are case-sensitive.
Exactly one bare, ungrouped, non-negated `rev:` scope is accepted per query.
An unknown selector returns a bounded query error without naming repositories.

The ceiling is eight revisions per repository including HEAD, so the config
allows at most seven additions. Wildcards, commit IDs, short/ambiguous refs,
and duplicate mappings are rejected at startup: values must be canonical
`refs/heads/*` or `refs/tags/*`. A normal sync, webhook fetch, forced reindex,
or watched-ref move republishes the whole admitted set atomically. Changing the
allowlist takes effect on the next sync/index job. File links still use the
immutable selected commit, while extraction, SCIP defaults, coverage, and
proof bundles intentionally remain anchored to HEAD.



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
negated or grouped in parentheses — both forms, and an unknown name, are
rejected with an error. Inside a double-quoted string `context:` is plain
content, not a filter.

Result bounds: `max_matches` (default 50 files, cap 500) and `context_lines`
(default 0, cap 10) on the API; searches are capped at 10 s of wall time.
Each result file includes the immutable indexed commit in `ref`. Repositories
without a committed index state, deleting repositories, and shards without a
live repo row are excluded from every query. A shard whose exact embedded
branch/commit set does not equal the row's atomically committed revision set is
also discarded. Permission filtering runs before `rev:` resolution, and the
selected result commit is checked again before serialization.

### Precise code navigation (SCIP)

`sym:` search uses ctags. Precise go-to-definition, references, and hover use
a committed [SCIP](https://scip-code.org/) index instead. Run the appropriate
SCIP indexer for the repository's language, write its binary protobuf as
`index.scip` at the repository root, commit it with the source it describes,
and let phebs sync/reindex that commit. No separate upload or side database is
required.

The first lookup lazily reads `index.scip` from the exact indexed commit. An
absent index is a normal `available: false` result. Index blobs over 64 MiB,
source files over 10 MiB, more than 32 MiB of aggregate source conversion in
one lookup, malformed or semantically oversized indexes, symbolic/short
revisions, and unsafe paths fail explicitly. The LRU snapshot cache has a 512
MiB accounted budget. Results are deterministically selected; reference
responses stop at 500 locations and set `truncated`, and hover content is
capped at 64 KiB. The UI uses UTF-16 offsets (matching browser strings), while
the HTTP API can request UTF-8, UTF-16, or UTF-32 conversion.

The extraction reader uses the same root-only product boundary with its own
trusted corpus ledger. The root path is fixed—nested indexes and manifests are
not alternatives—and the blob must have appeared as a regular file in the
complete walk of the indexed commit. Mutable refs and Git replacement objects
cannot redirect it; lazy object fetching is disabled. The reader opens only
the recorded immutable blob, enforces its separate 64 MiB limit, and
recomputes SHA-256 before parsing. A root `index.scip` symlink is an explicit
extraction failure, not an “index absent” result. T20.1 selected this mode for
the frozen monorepo target; phebs has no sharded-index manifest or part-reader
surface.

### Git history

History reads the existing bare mirror; it does not enlarge the zoekt index.
From a file, choose **Blame** for line attribution or **History** for the
rename-following commit list, then open a commit to inspect parents, changed
files, binary markers, and its first-parent diff. Root commits compare against
the empty tree. Blame is capped at 50,000 lines and 10 MiB source blobs, commit
pages at 200 rows, aggregate metadata at 64 MiB, and patch text at 2 MiB with
an explicit `truncated` flag. Git producers are canceled when a hard output
limit is reached. NUL-bearing blobs are rejected as binary; other non-UTF-8
line content is returned with invalid byte sequences replaced for JSON display.
Diff context defaults to three lines when omitted, while an explicit
`context_lines=0` returns zero-context hunks. Every request validates the
repository/path and pins supplied branch names to immutable object IDs before
subsequent Git commands run.

## 6. Web UI

Served at `/` from the binary. After setup/login, the main views are
deep-linkable hash routes:

- **Search** (`#/search?q=…`) — the repository explorer always lists the
repositories visible to the signed-in user, even before a search returns
results. Select a repository, expand folders one level at a time, or open a
file directly at its immutable indexed revision; **Add repository filter**
preserves the current query and inserts one exact quoted `repo:` atom. Folder
contents are loaded lazily and cached for that repository/revision/path. On
mobile the explorer is a collapsed **Browse repositories** drawer. Search
results still stream in as shards respond, grouped repo → file, with match
counts and highlighted spans; line numbers link into the viewer.
- **File viewer** (`#/file?repo=…&path=…&ref=…&L=42`) — read-only CodeMirror with
syntax highlighting across ~30 languages (Go, JS/TS, Python, Rust, Java,
C/C++, C#, Ruby, PHP, SQL, HTML/CSS, YAML, shell, …), a file-tree navigation
column that auto-expands to the current file, and a highlighted, scrolled-to
anchor line. Search links carry their immutable commit; old links without
`ref` resolve the repo's recorded indexed commit before loading. Click a
source position to open precise SCIP hover/definition/reference results when
that revision contains `index.scip`; **Blame** and **History** open the Git
views for the same immutable revision.
- **History / blame / commit** (`#/history`, `#/blame`, `#/commit`) — follow a
file across renames, map lines to commits, and render commit metadata,
changed-file statistics, and bounded unified diffs.
- **Repos** (`#/repos`) — sync/index state per repo (polled every 3 s),
orphan flags, indexed commit, and administrator-only **Reindex** controls
(a forced rebuild defeats the incremental short-circuit).
- **Settings** (`#/settings`) — create, copy once, list, and revoke API keys.
- **Audit** (`#/audit`, administrators only) — the recorded action trail:
logins (including failures), setup, logout, API-key lifecycle, and every
mutating API operation, newest first with actor, target, status, and
source IP.
- **Analytics** (`#/analytics`, administrators only) — 30-day search volume,
searches per day, average duration, and the repositories appearing most in
results — computed entirely from local usage events.
- **Contracts** (`#/contracts`, experimental) — browse provisional protobuf
service declarations without first knowing an operation identifier. Exact
repository/package/protocol/lineage filters lead to a service → operation
index, then bounded request/response shapes, independently classified
implementation/caller/abstention evidence, exact coverage state, and immutable
source links. Duplicate service names remain separate by repository and
provisional lineage. The protocol filter defaults to all registered protocols
(protobuf and thrift); each row carries its protocol. Thrift operations show
a `oneway` chip in place of the gRPC streaming chips, argument/result shapes
render through the same message tree (result field `0` is the wire success
slot), and union/exception declarations are badged. **Analyze impact**
carries the selected canonical operation into the Impact form but does not
submit it.
- **Impact** (`#/impact`, experimental) — bounded contract-impact reports for
canonical RPC operations (gRPC and Thrift consumer evidence), stable protobuf
field identities, and proposed before/after contract inputs. Known and unresolved consumers cite immutable
source revisions; every conclusion renders its complete coverage certificate.
The navigation item appears only when the server advertises the capability,
and the contract-change tab additionally requires the pinned Buf startup probe.

The UI uses its DB-backed session cookie and automatically supplies CSRF
tokens on mutations. A `401` clears stale authenticated state and returns to
the login view.

## 7. HTTP API

The API is OpenAPI-described by itself: fetch `/api/openapi.json` or browse
the interactive docs at `/api/docs`.

**Auth:** application endpoints accept either the browser session cookie or
`Authorization: Bearer <named-or-legacy-key>`. Authentication is not disabled
by omitting `auth.api_key`. Always open: `/api/health`, `/api/version`,
`/api/openapi*`, `/api/docs*`, auth status/enrollment/login/OIDC routes, and
`/metrics`. `/api/webhook` uses its own HMAC trust boundary.


| Endpoint                                                            | Method          | Purpose                                                                                        |
| ------------------------------------------------------------------- | --------------- | ---------------------------------------------------------------------------------------------- |
| `/api/health`                                                       | GET             | liveness `{"status":"ok"}`                                                                     |
| `/api/version`                                                      | GET             | server version                                                                                 |
| `/api/auth/status`                                                  | GET             | authentication/setup/OIDC state and current user                                               |
| `/api/auth/setup`, `/api/auth/login`, `/api/auth/logout`            | POST            | first administrator, local login, and session logout                                           |
| `/api/auth/keys`                                                    | GET/POST        | list or create the browser-session user's API keys                                             |
| `/api/auth/keys/{id}`                                               | DELETE          | revoke one API key (browser session only)                                                      |
| `/api/auth/oidc/start`, `/api/auth/oidc/callback`                   | GET             | OIDC authorization-code flow                                                                   |
| `/api/search?q=&max_matches=&context_lines=`                        | GET             | search, JSON in one shot                                                                       |
| `/api/stream_search?q=…`                                            | GET             | search over SSE (below)                                                                        |
| `/api/repos`                                                        | GET             | repo rows                                                                                      |
| `/api/repo-status`                                                  | GET             | repos + connections + orphan flag + last index job                                             |
| `/api/reindex`                                                      | POST            | administrator only: `{"repo":"github.com/foo/bar","force":true}` → enqueue index job           |
| `/api/audit?offset=&limit=`                                         | GET             | administrator only: audit events, newest first, `has_more` paging                              |
| `/api/analytics?days=`                                              | GET             | administrator only: search volume, per-day counts, top repos over the window (default 30 days) |
| `/api/webhook`                                                      | POST            | code-host push/repository events, HMAC-authed (no bearer); 404 unless `webhook.secret` set     |
| `/api/mcp`                                                          | POST/GET/DELETE | MCP over Streamable HTTP; bearer-authed (see §8)                                               |
| `/api/find_operation_consumers?operation=`                          | GET             | experimental permission-scoped operation-consumer proof bundle                                 |
| `/api/find_proto_field_references?lineage=&message=&field_number=`  | GET             | experimental permission-scoped protobuf-field-reference proof bundle                           |
| `/api/find_kafka_topic_usage?topic=`                                | GET             | experimental permission-scoped Kafka topic-usage proof bundle with an always-present unresolved census |
| `/api/get_extraction_coverage?domains=`                             | GET             | experimental assertion-free extraction-coverage proof bundle                                   |
| `/api/check_contract_compatibility`                                 | POST            | experimental Buf WIRE verdict enriched with permission-scoped affected consumers                |
| `/api/proof_bundles/{id}`                                           | GET             | reauthorized immutable proof-bundle read; an ID is not a bearer credential                     |
| `/api/contract_impact_report?operation=`                            | GET             | experimental bounded operation-impact report                                                   |
| `/api/contract_impact_report?lineage=&message=&field_number=`       | GET             | experimental bounded stable-field impact report                                                |
| `/api/contract_impact_report`                                       | POST            | experimental proposed-change impact report over the compatibility request shape                 |
| `/api/contract_impact_reports/{id}`                                 | GET             | reauthorized deterministic report projection of one immutable proof bundle                      |
| `/api/contract_atlas?repository=&package=&protocol=&lineage=&page_size=&cursor=` | GET | experimental bounded service/operation catalog over exact published evidence                    |
| `/api/contract_atlas/operation?repository=&lineage=&operation=`     | GET             | experimental bounded operation, message-shape, implementation, and caller detail                |
| `/api/source?repo=&path=&ref=`                                      | GET             | file content (`ref` defaults HEAD); binary comes base64; blobs over 10 MiB return 413          |
| `/api/folder_contents?repo=&path=&ref=`                             | GET             | one directory level                                                                            |
| `/api/tree?repo=&ref=`                                              | GET             | all file paths, recursive                                                                      |
| `/api/find_definitions?repo=&path=&ref=&line=&character=&encoding=` | GET             | precise SCIP definition at a zero-based position                                               |
| `/api/find_references?repo=&path=&ref=&line=&character=&encoding=`  | GET             | precise SCIP references (maximum 500)                                                          |
| `/api/hover?repo=&path=&ref=&line=&character=&encoding=`            | GET             | SCIP signature/documentation at a position                                                     |
| `/api/blame?repo=&path=&ref=`                                       | GET             | line-to-commit attribution, rename-aware                                                       |
| `/api/commits?repo=&ref=&path=&limit=&offset=`                      | GET             | commit history; optional path follows renames                                                  |
| `/api/commit?repo=&ref=`                                            | GET             | commit metadata, parents, and changed files                                                    |
| `/api/diff?repo=&head=&base=&path=&context_lines=`                  | GET             | bounded unified diff and file statistics; context defaults to 3 and accepts explicit 0         |
| `/metrics`                                                          | GET             | Prometheus metrics                                                                             |


`stream_search` emits Server-Sent Events: one `results` event per shard batch
(same JSON shape as `/api/search`), then a final `done` event with aggregate
stats; errors arrive as an `error` event. Disconnecting cancels the search.

```bash
export PHEBS_TOKEN='phebs_...'
curl -H "Authorization: Bearer $PHEBS_TOKEN" \
  'localhost:3070/api/search?q=ClaimJob+lang:go' | jq .files[0]
curl -N -H "Authorization: Bearer $PHEBS_TOKEN" \
  'localhost:3070/api/stream_search?q=needle'
curl -X POST localhost:3070/api/reindex \
  -H "Authorization: Bearer $PHEBS_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"repo":"github.com/foo/bar","force":true}'
```

Code-navigation refs must be full 40- or 64-hex commit IDs; omission resolves
the repository's recorded indexed commit. `line` and `character` are
zero-based; `encoding` defaults to `utf16` and also accepts `utf8`/`utf32`.
History endpoints similarly default omitted refs/heads to the indexed commit,
then resolve mutable commit-ish values once before reading. Unindexed or
deleting repositories fail closed.

## 8. Agents (MCP)

phebs is an MCP server: agents search and read your code through the same
index the UI uses. The endpoint is `/api/mcp` (Streamable HTTP, official MCP
go-sdk), guarded by the same DB-backed authentication as the rest of the API.
Create a named key in **Settings** and use it as the bearer token; the legacy
config key remains accepted only while it is configured.

Ten core tools are always present. Enabling any provisional extraction pack
adds four evidence-query tools.
It adds a fifth annex tool, for fifteen total, when the pinned Buf binary and
host sandbox pass their startup probe; otherwise compatibility stays
undiscoverable and the other three remain available.


| Tool               | Purpose                                                                                                                                                                                                                                                     |
| ------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `search_code`      | full query syntax from §5, including `context:` sets; returns files with line-numbered chunks and match ranges                                                                                                                                              |
| `read_file`        | file content at the indexed revision; optional `start_line`/`end_line`; output over 200 KB is truncated (on a line boundary where one fits) with a `truncated` flag inviting a ranged re-read. Blobs over 10 MiB are rejected outright, like `/api/source`. |
| `list_repos`       | every indexed repo with branch/visibility/index-time metadata                                                                                                                                                                                               |
| `find_definitions` | precise SCIP definition for `{repo,path,line,character,ref?}`                                                                                                                                                                                               |
| `find_references`  | precise SCIP references for the same position; maximum 500 locations with `truncated`                                                                                                                                                                       |
| `hover`            | SCIP symbol, signature, documentation, and source range                                                                                                                                                                                                     |
| `blame`            | rename-aware line attribution for `{repo,path,ref?}`; maximum 50,000 lines                                                                                                                                                                                  |
| `list_commits`     | paged history for `{repo,ref?,path?,limit?,offset?}`; maximum 200 commits per page                                                                                                                                                                          |
| `get_commit`       | commit metadata, parents, and first-parent file changes                                                                                                                                                                                                     |
| `diff`             | structured file statistics plus a unified patch, capped at 2 MiB with `truncated`                                                                                                                                                                           |
| `find_operation_consumers` | Investigation envelope v1.0 for one canonical `/package.Service/Method`; facts retain exact authorized proof references while processing, semantic resolution, attribution, conclusion, and absence eligibility remain separate |
| `find_proto_field_references` | Investigation envelope v1.0 for `(lineage, message, field_number)`; field names remain versioned attributes rather than identity |
| `find_kafka_topic_usage` | Investigation envelope v1.0 for one Kafka topic spelling; facts are producer/consumer evidence rows, the persisted bundle carries the per-shape-class unresolved census, and the answer is never a completeness claim |
| `get_extraction_coverage` | envelope containing the assertion-free coverage certificate over requested extractor domains, or every provisional domain when omitted |
| `check_contract_compatibility` | envelope containing the pinned Buf `WIRE` conclusion plus stable affected-field identities, visible SCIP consumers, exact proof references, coverage, and invocation provenance |


Code-navigation tool positions and returned ranges are zero-based UTF-16 code
units. Omitted `ref`/`head` values resolve to the DB's immutable indexed
commit. NUL-bearing binary blame, unknown repos, deleting repos, and unindexed repos come
back as tool errors rather than drifting to mutable mirror HEAD.

The five experimental tools return `envelope_version: "1.0"` as MCP
structured content. Their advertised `outputSchema` is the same generated
draft-2020-12 schema checked in under `schemas/`. Stateless proof queries do
not enumerate a released evidence-pack universe, so their pack-defined
eligible/processing counts are `withheld`, their outcome is `partial`, and a
zero-result response is blocked by `SCOPE_NOT_ENUMERATED`; it is not evidence
of absence. Qualification and refusal prose is selected and rendered by the
server. Clients should display `authoritative_text` verbatim and must not
upgrade a partial, withheld, refused, or truncated result. A hard-truncated
result has no continuation token and remains permanently incomplete and
absence-ineligible.

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

Any MCP client speaking Streamable HTTP works the same way. The core flow was
verified live against Claude Code: a headless session listed repos, ran a
scoped search, and read the matching file end-to-end (T8.3). Epic 9's seven
navigation/history tools are covered through real in-memory MCP sessions over
a committed SCIP fixture and bare Git mirror, including an indexed revision
held stable while mirror HEAD advances. T14.2's proof tools are covered through
one stateless Streamable HTTP session using the official SDK: the agent asks
operation-, field-, coverage-, and compatibility questions and receives source
citations and coverage without hidden-repository access. Compatibility is not
advertised if Buf is missing, has the wrong version, or the host cannot enforce
the sandbox.

## 9. Operations



### Data layout

```
$DATA/                     # server.data_dir, default ~/.phebs
├── db/                    # SurrealDB — users, API keys, sessions, repo/jobs
├── .surreal-runtime.json  # private, process-owned live-backup rendezvous
├── repos/<host>/<path>.git  # bare mirrors
└── index/*.zoekt          # search shards
```

Mirrors, shards, repo rows, and jobs are rebuildable from config and upstream
Git. **Authentication state is not derived:** `$DATA/db` now contains users,
OIDC links, API-key hashes, and sessions (see *Backup & restore*). Deleting
the whole data directory is an intentional auth reset as well as a reindex;
the next start requires first-user enrollment.

### Backup & restore

Precious state is `$DATA/db` plus the exact config file — the users, OIDC
links, API-key hashes, sessions, permission edges, audit/analytics history,
evidence, and proof pins that cannot be rebuilt (repo rows and job state ride
along but are derivable). Everything else under `$DATA` is derived.

For an online backup, keep the local phebs server running and use the same
phebs executable/configuration generation as that server:

```sh
phebs backup -config /etc/phebs/phebs.yaml -output /restricted/phebs-backup-20260722
```

The output path must not exist. The command discovers only the supervised
loopback SurrealDB child through `$DATA/.surreal-runtime.json`, verifies the
exact child executable and the raw-config digest that started the live server,
and runs that executable's live `export`. A different config that only points
at the same `$DATA` is refused. The command publishes a private directory
containing `database.surql` and `manifest.json`. The manifest binds the export's
size and SHA-256 digest, the exact raw config
digest, phebs version/binary digest, SurrealDB version/binary digest, database
identity, store-writer/evidence/migration versions, and the derived-state
exclusions. It contains no host binary path or database password. Preserve the
exact config separately; the backup contains its digest, not its bytes.

The export is unencrypted credential-bearing state. Move or encrypt the whole
directory only under the approved retention and key-custody procedure. Do not
edit either file: restore rejects extra, missing, renamed, symlinked, special,
oversized, or digest-mismatched entries.

Restore uses the manifest-bound phebs, SurrealDB, and config identities and an
absent or completely empty configured `$DATA`:

```sh
phebs restore -config /etc/phebs/phebs.yaml -backup /restricted/phebs-backup-20260722
phebs serve   -config /etc/phebs/phebs.yaml
```

Recovery config validation deliberately leaves `${SECRET}` references
unexpanded, so verification/import can happen in an isolated environment
without live source or OIDC credentials. Restore verifies the complete backup
and every compatible binary/store identity before it creates `$DATA`, imports
through an isolated SurrealDB child, and opens the store once to apply and
validate the supported schema/migrations. If import begins and then fails, the
partial target is retained and every later restore refuses it; quarantine or
remove it under the witnessed recovery procedure rather than retrying over it.

The subsequent normal `serve` start automatically rebuilds derived state:
startup reconciliation clears any indexed revision whose excluded shard is
missing, boot sync re-clones missing mirrors, and the queued index worker
rebuilds shards without an operator reindex request. Restored API keys and
sessions remain live — rotate them if the backup's custody was ever in doubt.

The stop-first cold path remains available:

1. Stop phebs and wait for exit, so SurrealKV is quiescent — a plain
   filesystem copy of a live `db/` is not consistent.
2. Copy the config file and `$DATA/db` to restricted storage; this is
   credential-bearing state.
3. Restart.

For a cold restore, place only the copied `db/` into a fresh `$DATA`, point
phebs at the same config, and start; the same automatic backfill applies.

### Security boundary

- Use HTTPS outside loopback and keep `auth.cookie_secure: true`. When a
reverse proxy terminates TLS, restrict direct access to phebs and configure
every trusted proxy-hop CIDR in `auth.trusted_proxies` so clients receive
separate login buckets. Phebs ignores forwarded-IP headers unless the direct
peer is trusted.
- Health, version, OpenAPI/docs, auth status/enrollment/login/OIDC routes, and
`/metrics` are public. Search, repository content, code navigation, history,
and MCP require a session or API key. Reindexing additionally requires an
administrator principal.
- Browser sessions are ambient credentials, so unsafe requests require CSRF.
Bearer clients must not put tokens in URLs, logs, or browser-local storage,
and bearer credentials cannot access the API-key management endpoints.
- `/api/webhook` does not accept user auth; it verifies the configured HMAC
over the exact request bytes and is absent when no secret is configured.
- OIDC authorizes every verified identity admitted by the configured provider.
Apply membership/domain policy at that provider; phebs does not add a second
allowlist.



### Permission-aware search

Adding a `permissions:` block turns on per-user visibility (T10.3). While a
connection syncs, each **private** repo's collaborator list is mirrored from
the code host (GitHub collaborators with `affiliation=all`; GitLab
`members/all` at Reporter or above; Gitea collaborators plus the owner —
org-team grants are not expanded) into local `repo_permission` edges keyed
`<host>:<login>`. Public repos are visible to every authenticated user and
never cost an ACL call. A failed listing keeps the previous grants rather
than locking users out; the next successful sync corrects them.

```yaml
permissions:
  users:
    bmeddeb@asu.edu: ["github.com:bmeddeb", "gitea.example.com:ben"]
  always_visible: ["local/*"]
```

`users` maps a phebs account (by email) to its code-host identities — the
explicit, operator-controlled link. `always_visible` globs cover repos with
no ACL source (`type: git`, local watches), which are otherwise visible only
to administrators. Administrators always see everything.

Enforcement compiles the user's allowed set **into the search query itself**
(the pre-pass RepoSet — never post-filtered), and the same predicate gates
file/tree/source reads, history, code navigation, repo listings, and every
MCP tool. A repo the caller cannot see behaves exactly like one that does
not exist. Enable the block, then re-sync (or wait for the resync cadence)
so edges exist before relying on them. MCP sessions run stateless so each
request is evaluated as its own authenticated caller.

### Audit log

Every mutating action is appended to an audit trail in the local database
(T10.1): local and OIDC logins (including failed local attempts), first-run
setup, logout, API-key creation and revocation, and each mutating API
operation (recorded by operation ID, e.g. `post-api-reindex` with the repo as
target). Events carry the actor (user, or API key for bearer calls), the
resolved client IP (trusted-proxy aware on the auth surface), and the
response status. Recording is synchronous but non-fatal: a failed write is
logged and never fails the request. Read the trail at `#/audit` or
`GET /api/audit` (administrators only). `audit.retention` (default 90 days)
prunes old events at boot and twice a day; `"0"` disables pruning. Webhook
deliveries are not audited — they are machine traffic with no principal, and
their effects are visible as jobs.

### Analytics — zero telemetry

Every completed search (UI, API, SSE, and MCP alike — they share one search
path) records a local `usage_event`: who searched, how long it took, and the
repositories that appeared in results (capped at the 20 most relevant). The
query text is deliberately **not** stored. Events never leave the machine and
nothing phones home — a deliberate divergence from upstream's telemetry.
The `#/analytics` dashboard and `GET /api/analytics` aggregate them on demand;
`analytics.retention` (default 365 days, `"0"` forever) bounds growth.

### Job system

Sync, fetch, index, and extraction work runs through queues in SurrealDB,
drained by one poller per kind that wakes every `poll_interval` (±50 %
jitter). Job states:
`pending → claimed → running → done | failed | canceled`.

Each target has at most one pending slot. An event arriving while work is
running creates or upgrades one pending successor, so pushes and forced
reindexes are not lost. Claims carry random lease tokens; every heartbeat,
retry, completion, shutdown release, and stale reaper transition is fenced by
that lease and the observed heartbeat. Connection membership snapshots are
replaced transactionally, so a failed refresh preserves the last complete set.

- **Retries:** failed executions requeue with per-class backoff, up to 3
attempts, then land in `failed` with the error recorded (visible in
`/api/repo-status` and the UI).
- **Backoff by failure class:** generic `30s × 2ⁿ`; auth failures `10m × 2ⁿ`
(a bad token won't heal in seconds); OOM-killed index children `5m × 2ⁿ`;
corrupt shards retry after `1s` (rebuild usually fixes them); extraction
failures `2m × 2ⁿ` (usually deterministic parse issues). Capped at 1 h.
- **Crash recovery:** running jobs heartbeat; a reaper requeues jobs whose
worker died (stale heartbeat), or fails them once attempts are exhausted.
Kill phebs mid-index and the job recovers on next boot.



### Experimental contract-intelligence extraction

This reader is **disabled by default**. T11.1 is closed by a human-accepted
capacity stop, while GATE2-V2 remains `NOT_ESTABLISHED`; T12.3 still lacks the
trusted protobuf module/root identity needed for canonical descriptor lineage.
To exercise the reviewed storage and extraction mechanics on a development
corpus only, opt in explicitly:

```yaml
experimental:
  provisional_proto_extraction: true
```

When enabled, every successful index schedules a bounded read of declared
protobuf contracts for that repository. The worker binds the read to the
latest indexed full commit. Services, messages, RPCs, and numbered fields
become `DECLARES_SERVICE`, `DECLARES_MESSAGE`, `DECLARES_OPERATION`, and
`DECLARES_FIELD` assertions backed by content-keyed evidence atoms bound to
the repository, commit, path, digest, byte span, and line span. RPC details
retain raw request/response type names and client/server streaming flags;
field details retain scalar or named type, cardinality, map key/value shape,
and oneof membership. Empty services and messages are included.

Type links are intentionally file-local. Protobuf lexical lookup records a
same-file message or enum only when exactly one declaration proves the link.
Unlinked import context, missing names, duplicate declarations, and invalid
declaration kinds remain unresolved under separate reason codes. Import
context is sorted, digest-bound, and explicitly truncated after 64 paths or
4 KiB; unresolved names are never labeled external. Recursive declarations
record links but are not expanded by the extractor. A trusted inventory still
requires every `.proto` candidate to be read. Extraction runs publish
atomically: a read, parse, provenance, limit, cancellation, or publication
failure leaves the prior published facts intact.

A separate `experimental.provisional_thrift_extraction` opt-in enables the
T19.2 Thrift declaration reader (`thrift-contract` 1.0.0). Every successful
index then also schedules a bounded read of `.thrift` IDL files. Services,
functions, and struct/union/exception shapes become the same
`DECLARES_SERVICE`, `DECLARES_OPERATION`, `DECLARES_MESSAGE`, and
`DECLARES_FIELD` assertion families under `thrift-*` detail schemas. Operation
identity is `scope.Service/method`, where scope is the last segment of an
explicit `namespace go`, then `namespace *` (Thrift applies it to every target
language), then the file basename. Request and response
shapes are modeled wire-honestly as the implicit argument and result structs
Thrift serializes: synthetic same-file messages whose field `0` is the success
slot and whose `throws` clauses are result fields; `oneway` functions declare
no result struct. Type links are file-local exactly as for protobuf, with
same-file typedef chains chased to a bounded depth and include-qualified names
remaining unresolved with sorted, digest-bound include context. Fields with
implicit identifiers fail closed as one structured `THRIFT_EXTRACTION_GAP`
per file — Thrift assigns negative wire identifiers to such fields, and the
reader never fabricates identity. Locators cite the declaration-start line
with exact byte spans. Buf-based wire-compatibility checking remains
protobuf-only; no Thrift compatibility engine exists.

The same Thrift opt-in also enables the T19.3 Go consumer reader
(`thrift-consumer` 1.1.0). It recognizes the repository's own Apache Thrift
generated Go by compiler header (both the modern and legacy marker forms),
recovers each service's wire method universe from `processorMap` key literals,
and binds each wire name to the exact generated Go method whose client
`Call` expression contains that literal. It does not reproduce Apache's
version- and option-sensitive publicizing rules. It then scans non-generated
Go files: `New<Service>Processor` call sites become
`REGISTERS_THRIFT_SERVICE` assertions (tier `derived`), and selector calls
whose generated method name is unique across the repository's stub index
become `CALLS_OPERATION` assertions for `/scope.Service/method` (tier
`heuristic`). Ambiguous call names abstain as one
`UNRESOLVED_THRIFT_CALL` candidate per canonical `/scope.Service/wire-method`;
ambiguous constructors use `UNRESOLVED_THRIFT_REGISTRATION`. Oversized or
unparseable files record
`THRIFT_EXTRACTION_GAP`. A repository that imports generated stubs from
another module instead of vendoring them yields no consumer evidence — an
honest abstention, not an error. Client construction is not evidence, and
consumer lineage remains file-scoped provisional, so joins against
declarations stay name-bound exactly as for gRPC.

A separate `experimental.provisional_kafka_extraction` opt-in enables the
T23.2 Kafka topic-evidence packs: `kafka-producer` and `kafka-consumer`
(both 1.0.0), two planes sharing one recognizer validated by the T23.1
spike. The readers scan non-test Go files that import
`github.com/Shopify/sarama`, `github.com/IBM/sarama`, or
`github.com/segmentio/kafka-go` (qualified selectors only — dot-imports
carry no in-file library proof and are refused; a file importing both
sarama eras abstains rather than guessing). Recognized shapes:
`sarama.ProducerMessage{Topic:}`, segmentio `Writer`/`WriterConfig`
composites, `kafka.Message{Topic:}` passed directly to `WriteMessages`
(never `CommitMessages`), `ReaderConfig` `Topic`/`GroupTopics`, and the
receiver-untyped `Consume`/`ConsumePartition` call shapes (tier
`heuristic`; composites are `derived`). A topic binds only when it is a
string literal or a same-file `const` satisfying Kafka's own naming bounds
(1–249 characters of `[a-zA-Z0-9._-]`, excluding `.`/`..`); the object is
`topic:<literal>` and carries no cluster, environment, runtime, or
completeness claim. Consumer group ids are recorded as detail, never
identity. Everything else — configuration selectors, function results,
variables, invalid literals — emits an `UNRESOLVED_KAFKA_PRODUCER` /
`UNRESOLVED_KAFKA_CONSUMER` assertion whose object names the frozen shape
class. **Expect abstention to dominate**: production Kafka topics are
overwhelmingly configuration-driven (2 literal evidence rows vs 17
abstentions across the spike's pinned production corpora), and the pack
presents that volume as the honest norm rather than a defect. `_test.go`
fixture literals are excluded from recognition entirely. Oversized or
unparseable files record `KAFKA_EXTRACTION_GAP`. There is no topic
declarations plane in round one — no in-code topic declaration exists —
so topics appear only through their producers and consumers, with no
catalog or Atlas surface.

The proto opt-in also enables the T13.1 Go/gRPC consumer reader (dark scope,
2026-07-22 disposition). It indexes the repository's own generated
`*_grpc.pb.go` stubs, then emits `REGISTERS_GRPC_SERVICE` assertions for
`Register<Service>Server` call sites (tier `derived` — name-bound to a
same-repo stub) and `CALLS_OPERATION` assertions for client method calls
whose name matches exactly one indexed service (tier `heuristic`). Package-less
protobuf service names such as `Greeter` are valid and indexed. Ambiguous
method names, generated registration-helper collisions, and duplicate service
FQNs anchored by different repository paths are not guessed: each source
occurrence emits an exact-span `tier=unresolved` diagnostic assertion, while
coverage counts the distinct semantic gaps those atoms support. Unparseable or
over-limit non-empty Go candidates likewise emit source-backed unresolved gaps,
so successful abstention remains publishable through the trusted worker.
Every assertion carries a `code_role`
(production/test/mock/generated/vendor, vendor > mock > generated > test >
production precedence) and cites its atom's exact byte and line span.
Resolution is syntactic — there is no type checking — so these facts carry
reduced fidelity by design and, like all provisional facts, state no
measured accuracy and must not drive compatibility, migration, or
negative-proof conclusions.

The opt-in also reads a repository-root, committed `index.scip` to emit T13.2
`REFERENCES_PROTO_FIELD` assertions. phebs never runs or downloads a SCIP
indexer: the fixed root index must be a regular blob in the same immutable
commit. Nested indexes, symlinks, and shard manifests are not selected inputs.
A SCIP symbol is
eligible only when its exact definition range matches a generated protobuf Go
struct field or getter, the generated struct tag supplies the field number and
proto name, and the generated file's `// source:` declaration maps uniquely to
the committed `.proto` field. Each non-definition reference cites the exact
identifier span in its source document. Positions use a document's declared
encoding when present and otherwise the index metadata's UTF-8/UTF-16
encoding; neither declared fails closed. Missing indexes produce an empty,
explicitly unavailable result; local symbols, malformed ranges, missing source
declarations, and ambiguous symbol/field joins abstain rather than guessing.

Field identity is canonical across consumer dependency versions:
`(contract_lineage_id, message_full_name, field_number)`. The lineage digest
uses the global SCIP scheme, package manager, and package name, but excludes
the dependency version and generated field/getter name. A field rename that
keeps its protobuf number and message therefore remains one identity, while
its current name and dependency version remain in assertion detail. The
classification is derived from SCIP role bits with precedence `write > read >
test > generated > unknown`; `code_role` separately records repository
placement and SCIP test/generated roles. These are direct field references,
not claims that a response field was semantically read.

The independent `experimental.provisional_thrift_field_extraction` opt-in
registers the T22 `scip-thrift-field` reader. For thriftrw, an eligible
generated file must embed a
`thriftreflect.ThriftModule` whose `SHA1` matches its `Raw` IDL bytes; the Raw
IDL field identity, generated struct-field order, and `wire.Field{ID: ...}`
literals must agree before an exact SCIP definition can bind references.
These rows carry tier `exact` and `source_binding=module_digest`.

Apache Thrift compiler output is eligible only when the file starts with one
of the validated complete generator-header comment forms and contains a valid
`thrift:"name,ID[,flags]"` struct tag. Scope is the generated Go package,
message is the enclosing generated struct, and the exact tagged identifier
must be the SCIP definition under that same enclosing type. Because Apache
output embeds no IDL bytes, digest, or source path, these rows remain tier
`derived` with `source_binding=none`; phebs does not promote them by consulting
another repository.

Both families use objects `scope.Message#field-number`, including field `0`,
and the same package-based `contract_scip_package_v1` lineage family as
protobuf without claiming equality to the declaration pack's provisional
lineage. Duplicate definitions or duplicate `(message, field ID)` identities
abstain. Malformed generator candidates abort the staged run.
A missing root index publishes explicit `scip-index-absent` coverage; a
malformed index aborts the staged run.

T22.1 measured the generated-file ceiling but not the inherited protobuf
index-scale limits, so this pack deliberately admits at most a 32 MiB root
index, 50,000 documents, 500,000 occurrences, 8 KiB symbols, and 4 MiB per
generated candidate. It reads only committed repository blobs and never runs
or downloads an indexer. The pack is experimental-dark and carries no
accuracy, completeness, runtime-use, or absence claim. T22.2/T22.3 add
ingestion only: the existing `find_proto_field_references` route remains
protobuf-only, and the neutral Thrift-capable query/report/MCP surface is
deferred to T22.4.

Every query answer over this evidence cites a deterministic coverage
certificate (`coverage-certificate-v1`): the caller's visible repositories
with their indexed revisions, each domain's exact latest published run (run
id, extractor, commit, freshness, protocols, complete source-scope counters and
digest, unresolved/assertion/atom counts, and gitlink boundary state), its
latest extraction attempt (id, input revision, extractor, status, and
failure), and SCIP index availability.

Submodule pointers (gitlinks) are repository boundaries, not blobs of the
containing repository: the trusted corpus walker records each one — a count,
a domain-separated digest over every sorted `path`/object-id record, and a
bounded display-safe path sample — into the run's coverage manifest, and
never clones, traverses, searches, or attributes their content to the parent
repository. `corpus_file_count` remains the count of regular blobs. Runs
published before boundary accounting carry no `inventory_policy` marker;
their boundary status is **unknown**, never zero, and the worker replaces
them on the next extraction job even when commit and extractor version match.
The Atlas coverage certificate table shows each run's boundary state
(`N gitlinks` or `unknown`), its bounded path sample, and whether that sample
was truncated. Unknown inventory-policy versions fail closed as `unknown`.
The published failure list is retained in the shape for exactness but is empty
under the atomic publisher, which refuses partial failures. SCIP availability
is current only when the reporting run matches the indexed revision; stale
protocol coverage yields `unknown`. A failed replacement keeps the prior
publication query-visible but records the newer attempt as `aborted`, including
same-commit forced runs and extractor upgrades; killed staged attempts become
`aborted` when swept. The certificate contains no wall-clock field. It never
queries, names, or counts a repository the caller cannot see. The query API
embeds this complete certificate in every proof bundle.

The opt-in registers five read-only query endpoints when the Buf startup probe
succeeds (the first four remain available when compatibility is unavailable):

- `GET /api/find_operation_consumers?operation=/scope.Service/method`
  returns exact-object `CALLS_OPERATION` assertions from both registered
  consumer domains (`grpc-consumer` and `thrift-consumer`); a domain with no
  published run for a repository contributes an honest no-run coverage row,
  never an error.
- `GET /api/find_proto_field_references?lineage=<id>&message=<full-name>&field_number=<n>`
  resolves the canonical field identity in the `scip-proto-field` domain
  (protobuf-only; the dark Thrift field-reference pack has no public query
  surface until T22.4).
- `GET /api/find_kafka_topic_usage?topic=<literal>` returns exact-object
  `PRODUCES_TO_TOPIC` and `CONSUMES_FROM_TOPIC` assertions for one topic
  spelling (validated by Kafka's own naming bounds), and the bundle always
  carries `unresolved_census`: per-plane counts of distinct
  `UNRESOLVED_KAFKA_*` assertions for every frozen shape class, zeros
  included. The census is topic-independent by construction — a non-literal
  topic cannot be matched to any literal — and the endpoint never makes a
  completeness claim.
- `GET /api/get_extraction_coverage?domains=<comma-separated-domains>` returns
  coverage only; omitted domains select `grpc-consumer`, `kafka-consumer`,
  `kafka-producer`, `proto-contract`, `scip-proto-field`, `thrift-consumer`,
  and `thrift-contract`.
- `POST /api/check_contract_compatibility` accepts a canonical `lineage` and
  `before`/`after` arrays of `{path,content}` `.proto` files. It runs Buf's
  `WIRE` policy and joins affected field identities to visible
  `REFERENCES_PROTO_FIELD` evidence.

The same opt-in registers the read-only contract-impact report surface and
advertises `contract-impact-report` in `/api/version`'s `capabilities` array
to authenticated callers. `/api/version` remains always-open for deployment
and client compatibility checks, but its anonymous response omits the entire
capabilities array so experimental feature and sandbox state are not disclosed.
`GET /api/contract_impact_report` accepts either one canonical `operation`, or
the complete `lineage`, `message`, and `field_number` identity. `POST` accepts
the compatibility request above and is registered only when the Buf probe
succeeds; that state also advertises `contract-compatibility`. A successful
response is `contract-impact-report-v1`: the proof question, known consumer
source rows, separately labeled unresolved candidates, optional Buf
classification, every visible repository/domain coverage state, the complete
canonical coverage certificate, and the existing caveat. The conclusion names
the exact certificate digest and says only what was found within the stated
evidence scope. A bare operation query is deliberately a union across
registered consumer packs; every evidence row therefore carries its exact
extractor domain and protocol so equal protobuf and Thrift operation spellings
remain distinguishable. Empty evidence never establishes absence.

Each repository evidence row links to `#/file` with its pinned repository,
commit, path, and line. Coverage rows describe the limits of the search and
may have no source evidence to open. Buf violation spans describe the submitted
source sets, whose contents are digested but deliberately not retained, so the
report does not manufacture links for them. Proposed-change reports therefore
compare the bounded before/after source commitments described below and record
Buf's bundle-local compatibility run; they are not represented as two
repository extraction publications. `GET /api/contract_impact_reports/{id}`
reprojects a saved bundle only after the same current-permission check as
`/api/proof_bundles/{id}`. Expiry, deletion, or access revocation produces the
same fail-closed `404` behavior.

The compatibility request is deliberately source-set based: it can check a
proposed contract before that contract exists in an indexed repository. Paths
must be unique canonical relative slash paths ending in `.proto`; content must
be UTF-8. Each side is capped at 256 files, each file at 4 MiB, both sides at
32 MiB total, the JSON request body at 72 MiB, and the evidence join at 256
distinct affected fields. A larger result fails with `422` rather than
returning a partial consumer inventory. Results retain sorted path and content
digests, not source blobs. For example:

```json
{
  "lineage": "contract_scip_package_v1_...",
  "before": [{"path": "shop/cart.proto", "content": "syntax = \"proto3\"; package shop; message Cart { int32 count = 1; }"}],
  "after": [{"path": "shop/cart.proto", "content": "syntax = \"proto3\"; package shop; message Cart { string count = 1; }"}]
}
```

phebs builds Buf v1.72.0 from the go.mod tool pin and refuses a different
binary. The child can execute only the fixed `buf breaking` operation with
the `WIRE` policy, JSON findings, symlink traversal disabled, and relative
paths inside a fresh private temp tree. It never runs `buf generate`, protoc
plugins, repository scripts, repository binaries, or repository configuration.
Network access and writes outside the temp tree are denied. Wall time is 15
seconds (Buf receives 10), CPU time is 10 seconds, output is capped at 4 MiB
per stream, and memory at 512 MiB. Linux uses bubblewrap namespaces plus a
virtual-memory rlimit; macOS uses Seatbelt plus a process-group RSS watchdog.
Failure to enforce or validate these boundaries leaves the endpoint and MCP
tool unregistered.

The bundle's `compatibility` object contains the WIRE verdict, exact Buf rule,
message and one-based source span, affected `(lineage,message,field_number)`
keys, input commitments, and an `extraction_run` record with engine, pinned
version, exact relative arguments, exit code, and result. That run is local to
the immutable bundle rather than the repository extraction publication table:
caller-provided source sets have no indexed repository revision. A breaking
rule is a spec-level conclusion only for the committed inputs. The affected
consumer list still has the coverage and provisional-evidence limits stated
below; an empty list does not prove absence or migration safety.

Every successful response is a self-contained `proof-bundle-v1`: canonical
question, matching assertions, their resolved atoms and repository
occurrences, coverage certificate, extractor/run bindings, visibility context,
and the provisional-evidence caveat. The `pb_<sha256>` ID commits to the exact
JSON content. Repeating the same query against the same evidence and effective
permission state yields the same ID and bytes. Queries return `HTTP 422` with
an instruction to narrow the question rather than truncate beyond 5,000
assertions or 20,000 distinct evidence references; stored bundle content is
limited to 64 MiB.

`GET /api/proof_bundles/{id}` retrieves an immutable bundle, but the ID is not
a bearer credential. phebs rechecks the current caller's permission to every
repository in the bundle before returning it; removal, repository deletion, or
revoked access makes the old bundle unavailable with `404`. The visibility
context records the stable principal and authorization-provider generation,
plus sha256 digests for the effective permission snapshot and complete visible
repository set. Permission filtering occurs before assertions, counts, or
coverage are computed, so an invisible repository is neither queried nor
named. Bundle scope is the complete visible universe at construction, not only
repositories with matching assertions. Deletion or rename of any repository in
that universe therefore makes the bundle unavailable to everyone, including
its creator; caller-specific loss of access makes it unavailable to that
caller. This is deliberately fail-closed.

The opt-in also registers the authenticated-only `contract-atlas` capability.
`GET /api/contract_atlas` discovers declared services and operations without
requiring a caller to know the canonical operation name. It supports exact
repository, package, protocol (`protobuf` or `thrift`; unknown values reject
before any read), and provisional declaration-lineage filters plus
`page_size` (default 50, maximum 100) and an opaque continuation cursor.
Listing is protocol-major over the registered packs, then repository-major,
so pagination order is deterministic; each row carries its protocol. A row is
identified by repository, declaration lineage, service FQN, and optional
method; equal FQNs in different lineages or repositories remain separate.

`GET /api/contract_atlas/operation` requires the exact `repository`,
declaration `lineage`, and canonical `/scope.Service/method`; the owning
protocol pack is resolved by probing declaration domains, so the identity
needs no protocol parameter. It returns the declaration, request/response
names with typed pack flags (protobuf streaming, Thrift `oneway` presence),
cycle-aware same-file message/field shape including typed
struct/union/exception kind and synthetic status, name-matched registrations
and callers, and separate extractor abstentions. A relationship is called
`proven` only when its own evidence lineage equals the selected declaration
lineage. Other name matches remain `unresolved_name_match`; ambiguous
extractor output remains `extractor_abstention`. SCIP package lineage is not
used as an operation identity.

Every claim-bearing row carries repository, exact extraction commit, path,
byte and line spans, assertion id, run id, and atom id. Every response embeds
the complete `coverage-certificate-v1` and its digest. The server first
filters the visible, non-deleting repository universe, reads only the exact
published run ids in that certificate, and confirms the digest after
projection. A concurrent publication is retried or returns `409`; revisions
are never mixed. Unknown, hidden, and deleting repository scopes all return
the same `404` before evidence is read.

The catalog certificate covers every registered declaration and consumer
domain, even when a query filters to one protocol. Adding a dark pack therefore
adds honest no-run coverage rows and changes the certificate digest. Protobuf
fact-detail JSON remains stable because Thrift-only typed fields omit when
absent; whole-response byte identity is not promised across registry growth.

Catalog responses are read-only and ephemeral: they create no proof bundle and
pin no extraction run. Cursors are checksummed and bind the query, stable
principal, authorization-provider generation, permission snapshot, complete
visible-repository set, coverage digest, and assertion position; a changed
binding returns `409`. Fixed limits bound each page, assertion scan, source
locators, message depth (6), expanded nodes (256), fields per message (100),
and joined relationships (200). Responses expose `complete`, `truncated`, and
machine-readable reasons. Ordered list scans include a safe continuation;
bounded detail trees and relationship sets state truncation without implying
that omitted or absent rows do not exist. Like all provisional extraction
surfaces, the Atlas is source evidence, not runtime topology or a completeness,
compatibility, ownership, or accuracy conclusion.

The Contracts navigation item and route appear only for an authenticated
caller whose server advertises `contract-atlas`. Normal production obtains
that capability only from the enabled provisional evidence service. For local
UI demonstration, `make dev` and `make dev-api` explicitly set
`PHEBS_CONTRACT_ATLAS_FIXTURE` to
`docs/fixtures/contracts/contract-atlas.json`. That validated adapter projects
one synthetic service onto the first currently visible indexed repository so
the `go.mod` source link opens at its exact indexed commit. It does not seed,
publish, persist, pin, or claim to validate extraction evidence; every
response says it is synthetic. With neither the environment binding nor the
real evidence service, the capability, HTTP routes, OpenAPI operations, and
navigation item do not exist. Anonymous `/api/version` responses never reveal
the capability even when either source is bound.

Selecting an operation also renders a focused one-hop source-evidence map.
Registration providers appear on one side of the declared operation;
name-bound caller evidence and separately dashed extractor abstentions appear
on the other. Each node is a keyboard-focusable link to its immutable source.
The table immediately below is authoritative and contains the exact same edge
ids and labels; on mobile it replaces the wide diagram. This is not a global
dependency graph: it makes no additional request, adds no hidden data surface,
and does not infer producers, consumers, runtime traffic, ownership, or
deployment topology. An empty neighborhood repeats the visible-repository
count, coverage digest, and relationship completeness state and explicitly
does not claim runtime absence.

Proof-bundle expiry is disabled by default. To bound retained answers and
their extraction-run pins, set a positive Go duration, for example:

```yaml
proof_bundles:
  retention: 168h # seven days since the bundle was last materialized
```

Omission or `"0"` retains bundles indefinitely. Repeating an identical query
refreshes only the store lifecycle timestamp: the canonical JSON, content ID,
and returned bytes do not change. Existing bundles created before this option
was available use their creation time until next materialized, so enabling the
policy also bounds them. Expired, missing, and unauthorized IDs all return the
same `404` response.

When enabled, retention checks once at startup and hourly thereafter, draining
backlogs in bounded batches. Deleting a bundle and only its exact
`proof-bundle:<id>` pins is one transaction; pins owned by another bundle or a
release/migration checkpoint remain. Bundle expiry never deletes evidence.
The independent evidence sweeper decides whether a superseded run made
newly unpinned by that transaction is eligible for reclamation.

Declaration and T13.1 operation-consumer lineage is deliberately machine-labeled
`provisional_repo_path_v1_<sha256>` and separates repository paths instead of
guessing descriptor identity. It prevents name-only cross-repository merges,
but a file move fragments lineage and an unrelated contract replacing the
same path can reuse it. The parser does not resolve imports, module roots, or
extensions; extension declarations fail closed. These facts must not drive
compatibility, migration, or negative-proof conclusions as though canonical
lineage had been established.

The run is bounded to 200,000 regular inventory paths and 16 MiB of aggregate
path text, 10 MiB per source blob, a separate 64 MiB ceiling for the fixed
root `index.scip`, 512 MiB of distinct reads, 5,000
emitted facts, and a cooperative 15-minute context deadline. A candidate
Go parser input is further limited to 4 MiB; a protobuf parser input is limited
to 4 MiB, 500,000 lexical tokens, and 128 structural levels. Neither in-process
parser can be preempted inside one parse call, so this is not yet a hard
CPU/memory/process isolation boundary. A candidate `.proto` symlink or more
than 100 placements of one content atom also prevents publication. Gitlinks
under
`gitlink-boundary-v1` are recorded as repository boundaries and are not
traversed. Unrelated symlinks are skipped. A
non-candidate file whose name cannot be represented safely (control bytes, a
backslash, invalid UTF-8, or a
leading `-`) is included in the published coverage certificate's
`corpus_file_count` but is never readable by extractors; a candidate with
such a name fails the run closed. Re-indexing the same
commit/extractor version short-circuits. Like the rest of phebs's
HEAD-freshness queues, successive index events may coalesce before extraction;
only the latest indexed revision can pass the publication guard. Opt-in
startup backfills indexed repositories even when new indexing is unavailable.
The same opt-in exposes these three proof queries as MCP envelope structured
content; HTTP proof-bundle routes and MCP envelope projection call one shared
proof service. Operational state is also visible through the database and
`phebs_jobs_total{kind="extraction_job"}`.

Proof-aware retention checks at startup and hourly while idle. Every
compatible-format run is limited to 25,000 stored association/assertion rows
and 20,000 evidence references. Individual staging transactions remain capped
at 10,000 rows of one kind; the extraction worker normally sends no more than
256 facts per transaction. Retention first locks one eligible aborted,
superseded, or 24-hour-stale staged run, rechecks that it is unpinned, and
marks it internally `deleting`. It then resumes from a durable phase while
deleting at most 512 associations or assertions per transaction. The run is
physically removed only after both row kinds are empty; an atom is removed
only after its last association anywhere has disappeared. Maintenance performs
at most 64 of these fixed-size steps, yields for five seconds when that cap is
reached, and returns to the hourly idle interval when drained. Logs report
completed logical runs separately from association, assertion, and atom rows.
Pinned proof/checkpoint runs and atoms still shared by another run are retained. Rows
migrated from the retracted, pre-bound evidence schema are hidden and
quarantined from automatic cleanup; an administrator must inspect and remove
that legacy data directly if desired. If two run records claim the same
logical run identity, all proof under that identity is likewise hidden,
unwritable, unpinnable, and exempt from automatic retention until an
administrator resolves the ambiguity.

The exact store-writer generation is separate from the stable evidence-format
version. Staged evidence writes and publication require the exact writer
generation, while compatible published reads, proof resolution, pinning, and
retention use the format version. A later compatible writer bump therefore
cannot strand an existing pinned proof bundle; an unknown format remains
hidden and untouched.

The current identities are writer `t12-store-v7`, readable evidence
`t12-evidence-v1`, and migration `t12-evidence-migration-v5`. Startup
idempotently upgrades the immediately preceding compatible v6 run generation
in place; readable evidence bytes and content identities do not change.
Staged-run reads and all mutations require v7. Compatible published
`t12-evidence-v1` runs, including pinned proof written by a future compatible
writer, remain readable through the stable format boundary.

Evidence migrations still require exclusive startup against the store.
Database and transaction guards now make a mixed-version or rollback writer
fail closed: known retired v1–v6 run writes are rejected, and every current
begin/stage/publish/abort/retention step requires the active v5 migration
marker. A generation-named synchronous database event survives a v6 binary reapplying
its weaker field definition and cancels its retired-generation transaction.
If an older opener changes the migration marker, current writes stop too; shut
down every writer and restart the v7 binary exclusively to restore it. Do not operate
rolling mixed writers against a remote endpoint. The supervised local
deployment already provides the intended single-writer lifecycle.

The v6 store also has an internal, exact reverse-evidence page used by the
planned Caller Map. It requires one authorized repository, published run,
predicate, and operation object; optionally fixes declaration lineage; returns
50 rows by default and at most 100; and uses an explicit continuation rather
than placing a hidden sentinel in the rendered rows. The query is bound to the
generation-specific compound index and fails if that index is unavailable.
No HTTP, MCP, or UI Caller Map surface is registered by T20.4.

### Investigation storage and guided execution foundation

On startup, the normal idempotent schema pass
creates the Investigation, Revision, Run, RunEvent, RunArtifact, Decision,
Disposition, BaselineDesignation, Watch, and WatchRevision tables and indexes.
T16.4 additionally creates the immutable guided-creation idempotency mapping
and the `investigation_run_job` queue table.

Revisions, Run requests, RunEvents, RunArtifacts, Decisions, Dispositions,
BaselineDesignations, and WatchRevisions are immutable at the store boundary.
Correcting one creates a new Revision/WatchRevision or an explicitly
superseding human record; the original remains readable. Investigation display
metadata/lifecycle/current-Revision and Watch owner/enablement/current-Revision/
expiry/cursor are the only checked mutable projections in this slice. Both
projections update by compare-and-swap against the previously read lifecycle
and current-revision pointer, so a concurrent stale writer fails closed with a
conflict instead of committing an invalid transition or clobbering the
pointer.

Run rows contain no status field. Guided submission atomically freezes the
active Investigation and first Revision, appends the initial `queued` event,
and creates one pending queue slot. Every later state is reconstructed from
the contiguous, append-only event stream:
`queued → enumerating → analyzing → publishing → published`, with `failed` or
`canceled` allowed only before a terminal state. Reusing the same idempotency
key for the exact same Revision request returns the existing Run; changing any
request input under that key fails closed. A failed or canceled RunArtifact
cannot carry published fact references.

Before submission, the guided preview lists only the caller's currently
visible repository snapshots, exact indexed commits, selected pack versions,
an authorization result, a repository-snapshot work estimate, and the
1,000-repository platform ceiling. A requested repository that is missing,
deleting, or unauthorized has the same `SCOPE_REPOSITORY_NOT_AVAILABLE`
blocker. The preview digest covers the normalized plan and resolved commits;
submission repeats the preflight and returns a conflict if anything changed.

Worker attempts use an opaque publication lease separate from the generic job
lease. A retry closes the prior lease and appends a new `queued` attempt; the
platform permits at most three attempts and a pack may choose fewer. Owners
may cancel a nonterminal Run. Cancellation closes the worker lease in the same
transaction as the terminal event and audit row, so a late worker cannot
publish.

RunArtifact publication, its terminal RunEvent, extraction-run evidence pins,
active-Investigation retention owner, and audit row are one database
transaction guarded by the exact attempt lease. Every pin uses the artifact-specific
`investigation-artifact:<artifact-id>` namespace; if any referenced extraction
run is missing, quarantined, ambiguous, incompatible, or not published or
superseded, neither the artifact nor any of its pins is written. The
publication transaction locks each referenced extraction run, so a concurrent
evidence sweep can never reclaim a run in the same instant an artifact pins
it. A Baseline
designation atomically acquires its corresponding artifact owner. Failed
attempts may atomically retain a reconciled
`investigation-coverage-ledger-v1` plus bounded diagnostics, but that path
rejects facts and evidence pins.

Retention owners are immutable caller-authorized claims. Ending one appends a
release; it never edits the claim, and that semantic owner key cannot silently
reacquire after release. Normal artifact collection locks the artifact and
rechecks that no unreleased Investigation, Baseline, or Dossier owner exists.
An audited revocation, mandatory-deletion, legal-policy, or approved-retention
override may supersede owners and allow immediate collection. Collection
removes only the RunArtifact and pins in its exact namespace, while preserving
owner/release/override audit rows. It never deletes extraction evidence; the
existing proof-aware evidence sweep may reclaim a superseded run only after
its final independent pin is gone. This remains an internal store facility:
lifecycle wiring, sharing surfaces, and public creation/read surfaces land in
later Epic 16 tickets.

Every investigation-domain read has a principal-scoped variant that
authorizes at query time. An object that does not exist and an object the
principal is not authorized to read produce the identical not-found result:
no counts, existence signals, scope, or integrity state are disclosed to an
unauthorized principal. Owners may grant and revoke `reader` access; grants
are re-checked on every read, and a grantee cannot delegate. Watches are
personal and never extend through grants. Ownership changes only through the
audited transfer operation — a plain update rejects owner edits — and a
transfer immediately voids each principal's stored continuation cursor for
that investigation without deleting any other state; re-authorized principals
simply re-establish their cursors. Each read binds the current ownership
revision and reader-grant generation, then rechecks that exact epoch after
integrity reconstruction; a concurrent transfer, revoke, or revoke/regrant
therefore cannot return stale data or surface a newly unauthorized integrity
error. Regranting creates a new generation and never resurrects the revoked
grant's cursor. Promoting a reader to owner consumes the reader grant, so it
cannot silently restore access after a later transfer. Grant, revoke, transfer,
and cursor mutation serialize against each other and commit with their audit
event in one transaction. The canonical `NOT_AVAILABLE` refusal
envelope (fixture 06) is rendered server-side as a minimal fixed shape whose
bytes are identical for unknown and unauthorized requests.

The T16.4 HTTP adapter defines preview, create, Run-status, and cancel
operations, but the production binary does not register them yet. Registration
requires a non-nil Investigation workflow store, which stays absent until an
exact released evidence-pack executor can drain the queue. Consequently these
post-gate implementation routes are absent from the live OpenAPI document and
return 404 rather than exposing a workflow that cannot complete.

T16.5 adds the read-only core-view surface behind a separate, narrow,
principal-scoped source. When a source is bound, authenticated clients receive
the `investigation-core-views` capability and may read:

- `GET /api/investigation_views` for authorized view summaries; and
- `GET /api/investigation_views/{id}` for one already-authorized envelope.

The UI then exposes `#/investigations`. Overview shows four summary regions
(evidenced, unknown, changed, and action), followed by the server-derived
bounded-absence eligibility result and blocker codes. The eligibility region
is deliberately read-only: there is no control or request path that can set
eligibility. Census preserves supported facts while displaying service and
owner attribution as separate states. Coverage shows eligible/analyzed/
failed/partial/excluded units and all attribution hops. Evidence retains proof,
snapshot, occurrence, and verification-action identifiers.

Empty states are conclusions with different prerequisites, not cosmetic
variants of “no results.” A complete zero-finding response renders the
server's authoritative bounded-absence qualification; an incomplete response
says only that no supported facts were found among analyzed units and lists
processing gaps; unresolved attribution remains visible beside the supported
fact; and a pack refusal suppresses Census, Coverage, evidence counts, and all
other claim-bearing content.

The normal production binary binds no core-view source, so these routes,
capability, OpenAPI operations, and navigation entry remain absent. For local
demonstration only, `make dev` sets `PHEBS_INVESTIGATION_FIXTURES` to the five
canonical synthetic files in `docs/fixtures/investigations/`. Setting that
environment variable manually opts into the same development adapter. These
fixtures exercise presentation and conformance states; they are not published
evidence, a released pack executor, a valid accuracy gate, or authority for
external claims.

T16.6 makes this envelope a generated, reusable contract rather than a
fixture-only UI shape. `internal/investigation` owns typed cross-field
validation; `go generate ./internal/mcp` regenerates the nine checked-in
schemas; and MCP advertises those exact schemas. Structural validation is not
the whole contract: the server also enforces coverage reconciliation, proof
references for evidenced facts, bounded-absence prerequisites,
non-comparability behavior, minimal `NOT_AVAILABLE` disclosure, and
irreversible truncation semantics.

T16.7 retains a principal-scoped first/last-seen consumer ledger independently
of sweepable RunArtifacts. Each published consumer census freezes the
authorized visibility projection, declared repository/build-target universe,
enumeration method, claim and fact identities, pack/rule/extractor/adapter
versions, build and snapshot semantics, and input completeness/freshness.
Two censuses produce an ordinary delta only when every frozen dimension is
compatible. A visibility, scope, identity, rule, enumeration, build,
external-input, failure, or freshness change instead yields a comparison
report with per-side coverage only; missing facts are never presented as
removals in that state.

Under fully comparable semantics, additions and removals require a positively
traced relationship occurrence. Removed relationships remain as inactive
ledger tombstones with their first/last-seen coordinates, so later compatible
evidence is classified as a reintroduction. Authorization is checked at every
snapshot and ledger read, and each principal has an independent projection.
Sweeping the RunArtifact that originally supplied an edge does not erase its
ledger history; the retained row is history and identity metadata, not a
replacement for the artifact's independently governed proof.

T16.8 derives ReviewItems from those authorized snapshots; there is no
hand-creation operation. A versioned pack projection can emit only three
queues: traced new/reintroduced consumers, coverage or comparability
regressions, and unresolved attribution tied to a fact that exists in the
source snapshot. The deterministic item identity binds the principal,
Investigation, comparison, projection version, logical subject, delta and
cause, evidence reference, and relevant human-record-state digest. Re-running
the same projection therefore returns the same IDs, independent of when it is
requested.

A later source sequence supersedes the preceding projection, while the
projection's lifecycle rule expires items relative to the immutable snapshot
publication time. The resulting states are only `open`, `superseded`, and
`expired`. Acknowledgement and the last-viewed comparison live in the existing
per-principal authorization-epoch cursor; they do not edit ReviewItems, and an
ownership transfer or grant revocation voids stale cursor state under the same
rules as every other Investigation read. Review materialization and listing
remain internal store facilities in this ticket; no production API or UI route
is enabled.

T16.9 defines the immutable `phebs-investigation-dossier-v1` export. Each
object and embedded finding is canonical JSON with its own domain-separated
SHA-256 digest; the canonical manifest binds those entries, recipient scope,
authorized locators for source material that is not embedded, supported
claims, blockers, eligibility, freshness/validation state, review/expiry rule,
and any predecessor. A second domain-separated digest roots the manifest and
entry list, and the service signs that root with Ed25519 plus a named
verification key. Creating the Dossier and its primary-RunArtifact retention
owner is one store transaction.

Export always calls the current recipient-scope resolver. It intersects the
principal-scoped consumer snapshot with that result, omits hidden units and
facts, and also omits legacy facts that lack a unit identity rather than
guessing their scope. The snapshot and input manifests and eligibility in the
export are recipient projections, not copied creator-wide claims. The service
rechecks both the Investigation authorization epoch and the resolved unit
scope after sealing and persistence before it returns any bytes.

Verify an exported canonical file without a running phebs service:

```sh
go run ./scripts/verify-dossier.go \
  -trusted-key-id <key-id> \
  -trusted-public-key <base64-ed25519-public-key> \
  dossier.json
```

The trust flags are optional for digest/signature consistency checks, but use
an independently distributed key for authenticity. Successful verification
proves byte integrity and the signer only; it does not prove current
authorization, freshness, evidence availability, or continuing validity.
Opening the file through phebs is a separate operation: it verifies the sealed
bytes, then reauthorizes every included Investigation object, current unit,
and fact. Revocation therefore blocks reopen without making an already
exported offline file cryptographically unverifiable. This ticket exposes the
service/store and offline format only; production API/UI export registration
still requires an explicit key configuration and route decision.

### Metrics


| Metric                         | Type      | Labels                                                             |
| ------------------------------ | --------- | ------------------------------------------------------------------ |
| `phebs_jobs_total`             | counter   | `kind`, `result` (`done`/`failed`/`requeued`/`released`/`reaped`)  |
| `phebs_job_errors_total`       | counter   | `kind`, `class` (`auth`/`oom`/`corrupt-shard`/`extract`/`generic`) |
| `phebs_index_duration_seconds` | histogram | —                                                                  |
| `phebs_index_shard_bytes`      | gauge     | —                                                                  |


Plus standard Go process metrics. Scrape `/metrics`.

### Shutdown

SIGINT/SIGTERM drains gracefully: the HTTP server stops, claimed/running work
is released to `pending` without consuming an attempt, and the SurrealDB child
is stopped. Kill -9 remains covered by the stale-heartbeat reaper.

## 10. Troubleshooting


| Symptom                                                           | Cause                                                                                                  | Fix                                                                                                   |
| ----------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------ | ----------------------------------------------------------------------------------------------------- |
| `start surreal child: exec: "surreal": executable file not found` | SurrealDB not installed                                                                                | see [prerequisites](#prerequisites)                                                                   |
| log: `zoekt-git-index not found — indexing disabled`              | binary built without `make build`/`make dev`                                                           | `make build`, or set `PHEBS_ZOEKT_GIT_INDEX=/path/to/zoekt-git-index`                                 |
| log: contract compatibility disabled                              | Buf is missing/mismatched, or the OS sandbox cannot be enforced                                        | use `make build` or set `PHEBS_BUF` to the pinned v1.72.0 binary; install `bubblewrap` on Linux        |
| `listen tcp 127.0.0.1:3070: bind: address already in use`         | another phebs (or process) on the port                                                                 | stop it, or `-addr 127.0.0.1:3071`                                                                    |
| UI shows first-run setup                                          | no users and no OIDC provider                                                                          | copy the ephemeral setup token from the current process log; restarting generates a new token         |
| login succeeds but the UI immediately asks again                  | a `Secure` cookie was used over plain non-loopback HTTP                                                | serve HTTPS, or set `auth.cookie_secure: false` only for deliberate local development                 |
| API or MCP answers `401`                                          | no valid session/key, or a key was revoked/removed                                                     | create a named key in Settings and send `Authorization: Bearer <token>`                               |
| startup fails during OIDC discovery                               | issuer unavailable, wrong URL/private CA, or incomplete provider config                                | verify HTTPS reachability and discovery metadata; loopback HTTP is test-only                          |
| OIDC login says verified email is required                        | provider omitted `email_verified=true`                                                                 | configure the provider's email scope/claim mapping; phebs does not accept unverified email identities |
| code navigation says unavailable                                  | the indexed commit has no root `index.scip`                                                            | generate and commit a SCIP index, then sync/reindex that commit                                       |
| code-navigation/history link returns 404 after a repo update      | requested immutable commit is no longer present in the mirror or repo is unindexed/deleting            | use the current indexed commit from Repos, or restore/fetch the referenced object                     |
| GitHub sync reports a rate-limit wait                             | host requested a reset delay; phebs waits at most 1 minute and retries once, then uses the job backoff | use a PAT/App or reduce listing frequency                                                             |
| watch mode "doesn't see my edits"                                 | uncommitted changes are never indexed                                                                   | commit (or amend); the watcher reacts to HEAD and admitted-ref moves                                  |
| a repo temporarily disappears from search during repair           | its shard revision did not match committed DB state                                                    | wait for the forced index job; serving is intentionally fail-closed                                   |
| repo tagged `orphaned`                                            | no connection claims it anymore                                                                        | re-add the connection, or enable `sync.cleanup_orphans`                                               |
| sync fails with `auth: git …` and retries slowly                  | credential failure, classified `auth` (10 m backoff)                                                   | fix the token; reindex/restart to retry immediately                                                   |
| startup rejects a clone URL containing credentials/query data     | URL secrets are no longer persisted                                                                    | move HTTP credentials to `http_auth`; keep `url` credential-free                                      |




## 11. Developing phebs


| Target               | Does                                                                                                                                                    |
| -------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `make dev`           | build UI + pinned zoekt/Buf children, bind synthetic Investigation/Contract Atlas demo fixtures, run with embedded UI                                   |
| `make dev-api`       | backend-only loop with the same children and explicit demo fixtures (placeholder UI page, fast)                                                         |
| `make build`         | version-stamped `./phebs` plus same-module `bin/zoekt-git-index` and `bin/buf`; pass `VERSION=vX.Y.Z` for a release                                    |
| `make release`       | assemble a new host-native `dist/phebs-<version>-<target>` directory and canonical digest manifest; requires v-prefixed `VERSION`                       |
| `make verify-release` | reject any manifest, payload, mode, symlink, missing-file, or extra-file drift in `RELEASE_BUNDLE`                                                      |
| `make smoke-release` | run the verified bundle from empty state through auth, sync, index, search, pinned browse, and default-dark Contract Atlas checks                       |
| `make test`          | `go test ./... -timeout=25m` — store/sync/indexer tests need `surreal`; the timeout matches CI's integration-suite allowance; child-binary tests build pinned zoekt and Buf binaries |
| `make ui-test`       | Vitest UI tests (`cd ui && npm test`) — streaming, keyboard nav, facets, file tree                                                                      |
| `make lint`          | golangci-lint                                                                                                                                           |
| `make ui`            | production UI build only                                                                                                                                |
| `make db-server`     | SurrealDB in server mode via docker compose (testing only)                                                                                              |

The hosted release gate runs four independently visible local-equivalent
targets: `make ci-static`, `make ci-go`, `make ci-race`, and `make ci-ui`.
`make ci` runs all four sequentially. The live Go targets require the exact
pinned `surreal` on `PATH`; hosted CI downloads the exact 3.2.0 Linux archive
and verifies its committed SHA-256 before any store test starts.


Live UI development: run `make dev-api`, then `cd ui && npm run dev` — Vite
serves on :5173 and proxies `/api` to :3070.

phebs is an independent, reference-only reimplementation inspired by
[Sourcebot](https://github.com/sourcebot-dev/sourcebot) — no upstream code is
used. phebs is licensed Apache-2.0.
