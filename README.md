<div align="center">

<img src="./docs/phebs-banner.svg" alt="phebs — Self-hosted code search in one Go binary." width="960">

*pronounced **"febz"***

</div>

---

> In 1899, William Pickering discovered Phoebe — the first moon ever found by
> photography — by comparing plates of Saturn's sky and spotting the single dot
> that moved among thousands of fixed stars.
>
> One moving signal in a massive static corpus. That is the whole job.

## What it is

**phebs** is a ground-up, reference-only Go reimplementation of the ideas in
[Sourcebot](https://github.com/sourcebot-dev/sourcebot): fast, precise code
search you host yourself, without running a constellation of services to get it.

- **One binary.** [zoekt](https://github.com/sourcegraph/zoekt) is linked
  in-process as a library for search; index builds run as an OOM-isolated
  child compiled from the same module version. The web UI is embedded.
- **One database, supervised.** [SurrealDB](https://surrealdb.com) holds auth,
  repo state, and job queues on local disk, started and stopped with phebs —
  zero external services.
- **Live search over your working repos.** Watch mode notices commits and
  branch switches in local repositories and reindexes within seconds.
- **Authentication included.** Persisted browser sessions, Argon2id local
  login, revocable API keys, and optional OIDC protect the UI, API, and MCP.
- **Code intelligence at the indexed commit.** Committed SCIP indexes provide
  definitions/references/hover; bare-mirror plumbing provides blame, history,
  commit detail, and bounded diffs.
- **Scales sideways (planned).** The fleet profile shards repos across
  replicas by rendezvous hashing with gRPC scatter-gather queries.

## Quick start

```bash
make build                          # UI + zoekt child + ./phebs
./phebs serve -config phebs.yaml    # open http://localhost:3070
```

```yaml
# phebs.yaml
server:
  addr: "127.0.0.1:3070"           # local quick start
auth:
  cookie_secure: false              # plain-HTTP localhost only
connections:
  - name: zoekt
    type: git
    url: https://github.com/sourcegraph/zoekt.git
  - name: my-project
    type: git
    url: /Users/you/src/my-project
    watch: true                     # commits searchable in seconds
```

On first start, copy the setup token printed in the local log into the browser
to create the administrator. Keep the default secure cookie setting under
HTTPS; see the manual for bootstrap-user and OIDC deployment.

**[→ Full user manual](./docs/MANUAL.md)** — authentication/OIDC, repository
connections, search and SCIP, Git history, HTTP/MCP APIs, operations, and
troubleshooting.

## Architecture

zoekt in-process for trigram search · SurrealDB 3.0 for state, auth, and queues ·
[huma](https://github.com/danielgtaylor/huma) for the OpenAPI surface ·
committed SCIP for precise navigation · Vite + React +
[Base Web](https://baseweb.design) + CodeMirror 6 in front ·
gRPC scatter-gather across a rendezvous-hashed fleet (planned, P6).

```mermaid
flowchart LR
    UI["Web UI<br/>React · Base Web · CodeMirror 6"] -->|"HTTP / OpenAPI"| API["phebs binary<br/>huma API"]
    API --> COORD["Searcher"]
    COORD -->|"in-process"| ZK["zoekt shards"]
    API --> SYNC["Repo syncer<br/>jittered polling · watch mode"]
    SYNC --> GIT[("Git hosts &<br/>local repos")]
    SYNC --> IDX["zoekt-git-index<br/>(same-SHA child)"]
    IDX --> ZK
    API <--> DB[("SurrealDB 3.0<br/>supervised child<br/>auth · state · jobs")]
    COORD -.->|"gRPC scatter-gather<br/>(fleet mode, planned)"| PEERS["Peer replicas"]
```

## Design decisions of record

Every decision lands as a dated ADR bullet in [PLAN.md](./PLAN.md). Highlights:

- **zoekt as a library, not a service.** Search runs in-process; the index
  builder is a child compiled from the same go.mod SHA, so reader/writer
  shard skew is structurally impossible.
- **HEAD by default, bounded revisions when requested.** HEAD remains the
  authoritative default; an optional per-repository allowlist adds up to seven
  branch/tag revisions selected explicitly with `rev:`.
- **Jittered polling, not LIVE SELECT.** Queue claims poll with jitter; an
  optimistic conditional UPDATE won the claim-semantics spike (zero
  double-claims under concurrency, cheapest lost-race cost).
- **Supervised child over embedded DB.** No embedded SurrealDB engine exists
  for Go; a loopback-only supervised `surreal` process keeps single-command
  dev and deletes the CGo risk.

## Status

**Single-node product complete through Epic 15** — sync (GitHub incl. App auth,
GitLab, Gitea, any git URL, local), fenced jobs with crash recovery, bounded
multi-revision indexing, JSON/SSE search, contexts, authentication/OIDC,
permission-aware API and stateless MCP, audit/analytics, committed SCIP, Git
history, live backup/restore, and the web UI are shipped. The default-dark
contract-intelligence annex adds exact consumer/field citations, coverage
certificates, immutable proof bundles, pinned-Buf compatibility, MCP tools, and
a read-only impact report. Its validation status remains explicitly bounded:
GATE2-V2 is `NOT_ESTABLISHED`, and Epic 16 is blocked pending an established
validation plus a pilot-continuation decision. See
[BACKLOG.md](./docs/BACKLOG.md) for the ticket record.

## Lineage & acknowledgements

- [Sourcebot](https://github.com/sourcebot-dev/sourcebot) — the reference for
  this port. phebs is an independent reimplementation from observed behavior
  and public docs; no upstream code, UI, or assets are used.
- [zoekt](https://github.com/sourcegraph/zoekt) (Apache-2.0) — the search
  core, by Han-Wen Nienhuys, maintained by Sourcegraph.
- [SurrealDB](https://surrealdb.com), [huma](https://github.com/danielgtaylor/huma),
  [Base Web](https://baseweb.design), [CodeMirror](https://codemirror.net).

## License

[Apache-2.0](./LICENSE).
