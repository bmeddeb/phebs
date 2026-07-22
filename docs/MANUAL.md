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

Indexing is **HEAD-only**: the default branch of each repo (or, for watched
local repos, whatever branch is checked out).

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




### Build and run

```bash
git clone <your-clone-of-phebs> && cd phebs
make build          # builds the UI, zoekt and Buf children, and ./phebs
./phebs serve -config phebs.yaml
```

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
| `experimental.provisional_proto_extraction` | `false`          | development-only opt-in for the validation-gated readers described below; declarations/operation consumers retain provisional lineage                             |
| `permissions`                               | *(none)*         | presence enables permission-aware search (see [Permission-aware search](#permission-aware-search)); omit to keep every authenticated user seeing everything       |




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

`watch: true` on a local git connection makes phebs poll the repo's HEAD
(every ~3 s) and re-sync + re-index whenever it moves. Because indexing is
HEAD-only, the HEAD hash is the exact change signal: **commits and branch
switches trigger reindexing; uncommitted working-tree edits do not.**

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
negated or grouped in parentheses — both forms, and an unknown name, are
rejected with an error. Inside a double-quoted string `context:` is plain
content, not a filter.

Result bounds: `max_matches` (default 50 files, cap 500) and `context_lines`
(default 0, cap 10) on the API; searches are capped at 10 s of wall time.
Each result file includes the immutable indexed commit in `ref`. Repositories
without a committed index state, deleting repositories, and shards without a
live repo row are excluded from every query. A shard whose embedded revision
does not equal the row's committed revision is also discarded.

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

- **Search** (`#/search?q=…`) — results stream in as shards respond, grouped
repo → file, with match counts and highlighted spans. Line numbers link
into the viewer.
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
| `/api/get_extraction_coverage?domains=`                             | GET             | experimental assertion-free extraction-coverage proof bundle                                   |
| `/api/check_contract_compatibility`                                 | POST            | experimental Buf WIRE verdict enriched with permission-scoped affected consumers                |
| `/api/proof_bundles/{id}`                                           | GET             | reauthorized immutable proof-bundle read; an ID is not a bearer credential                     |
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

Ten core tools are always present. Enabling
`experimental.provisional_proto_extraction` adds three evidence-query tools.
It adds a fourth annex tool, for fourteen total, when the pinned Buf binary and
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
| `find_operation_consumers` | immutable permission-scoped `proof-bundle-v1` for one canonical `/package.Service/Method`; includes matching assertions, exact source occurrences, coverage, extractor versions, and the provisional-evidence caveat |
| `find_proto_field_references` | immutable proof bundle for `(lineage, message, field_number)`; field names remain versioned attributes rather than identity |
| `get_extraction_coverage` | assertion-free proof bundle over requested extractor domains, or all three provisional domains when omitted |
| `check_contract_compatibility` | pinned Buf `WIRE` verdict over bounded before/after `.proto` files, enriched with stable affected-field identities, visible SCIP consumers, exact citations, coverage, and invocation provenance |


Code-navigation tool positions and returned ranges are zero-based UTF-16 code
units. Omitted `ref`/`head` values resolve to the DB's immutable indexed
commit. NUL-bearing binary blame, unknown repos, deleting repos, and unindexed repos come
back as tool errors rather than drifting to mutable mirror HEAD.

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
├── repos/<host>/<path>.git  # bare mirrors
└── index/*.zoekt          # search shards
```

Mirrors, shards, repo rows, and jobs are rebuildable from config and upstream
Git. **Authentication state is not derived:** `$DATA/db` now contains users,
OIDC links, API-key hashes, and sessions (see *Backup & restore*). Deleting
the whole data directory is an intentional auth reset as well as a reindex;
the next start requires first-user enrollment.

### Backup & restore

Precious state is `$DATA/db` plus the config file — the users, OIDC links,
API-key hashes, sessions, permission edges, and audit/analytics history that
cannot be rebuilt (repo rows and job state ride along but are derivable).
Everything else under `$DATA` is derived. Cold backup:

1. Stop phebs and wait for exit, so SurrealKV is quiescent — a plain
   filesystem copy of a live `db/` is not consistent.
2. Copy the config file and `$DATA/db` to restricted storage; this is
   credential-bearing state.
3. Restart.

To restore, place the copied `db/` into a fresh `$DATA`, point phebs at the
same config, and start. Backfill of derived state is automatic: sync
re-clones any mirror whose `HEAD` is missing, and the startup reconcile
audit re-enqueues indexing for every repo whose recorded indexed revision
has no matching shard. No operator action is needed beyond time and
bandwidth. Restored API keys and sessions remain live — rotate them if the
backup's custody was ever in doubt.

There is no online backup yet; `phebs backup` / `phebs restore` subcommands
are tracked as ticket T-P5.1 in the backlog.

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
latest indexed full commit. Each RPC, fully qualified by its service, becomes
a `DECLARES_OPERATION` assertion and each message field a `DECLARES_FIELD`
assertion, backed by a content-keyed evidence atom bound to the repository,
commit, path, digest, byte span, and line span. A trusted inventory requires
every `.proto` candidate to be read. Extraction runs publish atomically: a
read, parse, provenance, limit, cancellation, or publication failure leaves
the prior published facts intact.

The same opt-in also enables the T13.1 Go/gRPC consumer reader (dark scope,
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
indexer: the index must describe the same immutable commit. A SCIP symbol is
eligible only when its exact definition range matches a generated protobuf Go
struct field or getter, the generated struct tag supplies the field number and
proto name, and the generated file's `// source:` declaration maps uniquely to
the committed `.proto` field. Each non-definition reference cites the exact
identifier span in its source document. Missing indexes produce an empty,
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

Every query answer over this evidence cites a deterministic coverage
certificate (`coverage-certificate-v1`): the caller's visible repositories
with their indexed revisions, each domain's exact latest published run (run
id, extractor, commit, freshness, protocols, complete source-scope counters and
digest, unresolved/assertion/atom counts), its latest extraction attempt (id,
input revision, extractor, status, and failure), and SCIP index availability.
The published failure list is retained in the shape for exactness but is empty
under the atomic publisher, which refuses partial failures. SCIP availability
is current only when the reporting run matches the indexed revision; stale
protocol coverage yields `unknown`. A failed replacement keeps the prior
publication query-visible but records the newer attempt as `aborted`, including
same-commit forced runs and extractor upgrades; killed staged attempts become
`aborted` when swept. The certificate contains no wall-clock field. It never
queries, names, or counts a repository the caller cannot see. The query API
embeds this complete certificate in every proof bundle.

The opt-in registers four read-only query endpoints when the Buf startup probe
succeeds (the first three remain available when compatibility is unavailable):

- `GET /api/find_operation_consumers?operation=/fully.qualified.Service/Method`
  returns exact-object `CALLS_OPERATION` assertions from the `grpc-consumer`
  domain.
- `GET /api/find_proto_field_references?lineage=<id>&message=<full-name>&field_number=<n>`
  resolves the canonical field identity in the `scip-proto-field` domain.
- `GET /api/get_extraction_coverage?domains=<comma-separated-domains>` returns
  coverage only; omitted domains select `grpc-consumer`, `proto-contract`, and
  `scip-proto-field`.
- `POST /api/check_contract_compatibility` accepts a canonical `lineage` and
  `before`/`after` arrays of `{path,content}` `.proto` files. It runs Buf's
  `WIRE` policy and joins affected field identities to visible
  `REFERENCES_PROTO_FIELD` evidence.

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

There is currently no automatic bundle expiry: stored bundles and their
referenced extraction-run pins are retained indefinitely. T14.4 adds an
operator-configured lifetime with atomic bundle/pin removal and is required
before pilot exposure.

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
CPU/memory/process isolation boundary. A candidate `.proto` symlink, any
gitlink (whose subtree coverage is unknown), or more than 100 placements of one
content atom also prevents publication; unrelated symlinks are skipped. A
non-candidate file whose name cannot be represented safely (control bytes, a
backslash, invalid UTF-8, or a
leading `-`) is included in the published coverage certificate's
`corpus_file_count` but is never readable by extractors; a candidate with
such a name fails the run closed. Re-indexing the same
commit/extractor version short-circuits. Like the rest of phebs's
HEAD-freshness queues, successive index events may coalesce before extraction;
only the latest indexed revision can pass the publication guard. Opt-in
startup backfills indexed repositories even when new indexing is unavailable.
The same opt-in exposes these three proof queries as MCP structured content;
HTTP and MCP call one shared proof service. Operational state is also visible
through the database and
`phebs_jobs_total{kind="extraction_job"}`.

Proof-aware retention checks at startup and hourly while idle. Every
compatible-format run is limited to 10,000 stored association/assertion rows
and 20,000 evidence references. One eligible aborted, superseded, or
24-hour-stale staged run is reclaimed per transaction, with at most eight
transactions in a pass. A full pass yields for five seconds before another
bounded pass; a drained pass returns to the hourly idle interval. Pinned
proof/checkpoint runs and atoms still shared by another run are retained. Rows
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

Evidence migrations assume exclusive startup against the store. Mixed-version
rolling writers, or rolling an older writer back onto the same remote
endpoint, are not supported; the supervised local deployment already provides
that single-writer boundary.

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
| watch mode "doesn't see my edits"                                 | uncommitted changes — indexing is HEAD-only                                                            | commit (or amend); the watcher reacts to HEAD moves                                                   |
| a repo temporarily disappears from search during repair           | its shard revision did not match committed DB state                                                    | wait for the forced index job; serving is intentionally fail-closed                                   |
| repo tagged `orphaned`                                            | no connection claims it anymore                                                                        | re-add the connection, or enable `sync.cleanup_orphans`                                               |
| sync fails with `auth: git …` and retries slowly                  | credential failure, classified `auth` (10 m backoff)                                                   | fix the token; reindex/restart to retry immediately                                                   |
| startup rejects a clone URL containing credentials/query data     | URL secrets are no longer persisted                                                                    | move HTTP credentials to `http_auth`; keep `url` credential-free                                      |




## 11. Developing phebs


| Target           | Does                                                                                                                                                    |
| ---------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `make dev`       | build UI + pinned zoekt/Buf children, run with embedded UI                                                                                               |
| `make dev-api`   | backend-only loop with pinned zoekt/Buf children (placeholder UI page, fast)                                                                             |
| `make build`     | release binary `./phebs`, `bin/zoekt-git-index`, and `bin/buf`                                                                                          |
| `make test`      | `go test ./...` — store/sync/indexer tests need `surreal`; child-binary integration tests build pinned zoekt and Buf binaries                            |
| `make ui-test`   | Vitest UI tests (`cd ui && npm test`) — streaming, keyboard nav, facets, file tree                                                                      |
| `make lint`      | golangci-lint                                                                                                                                           |
| `make ui`        | production UI build only                                                                                                                                |
| `make db-server` | SurrealDB in server mode via docker compose (testing only)                                                                                              |


Live UI development: run `make dev-api`, then `cd ui && npm run dev` — Vite
serves on :5173 and proxies `/api` to :3070.

phebs is an independent, reference-only reimplementation inspired by
[Sourcebot](https://github.com/sourcebot-dev/sourcebot) — no upstream code is
used. phebs is licensed Apache-2.0.
