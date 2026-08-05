# phebs user guide

phebs is self-hosted code search and static change-impact analysis in one Go
binary. This page routes each task to its owning guide so setup, product
workflows, and operations do not compete in one long document.

## Choose a task

| Task | Guide |
|---|---|
| Install prerequisites, build, and complete first-administrator setup | [Getting started](./guides/GETTING_STARTED.md) |
| Configure authentication, connectors, synchronization, and repository discovery | [Configuration and connections](./guides/CONFIGURATION.md) |
| Search, browse, use SCIP/history, call HTTP/MCP, or evaluate the experimental workflows | [Product workflows](./guides/WORKFLOWS.md) |
| Back up, restore, secure, monitor, troubleshoot, or develop phebs | [Operations and development](./guides/OPERATIONS.md) |
| Review every available option | [Annotated configuration](./config.example.yaml) |

Behavior changes update the guide that owns the affected task in the same
change. Architecture and rationale belong in [PLAN.md](../PLAN.md); active work
and acceptance criteria belong in [BACKLOG.md](./BACKLOG.md).

## Product at a glance


phebs mirrors git repositories to local disk, builds
[zoekt](https://github.com/sourcegraph/zoekt) trigram indexes over them, and
serves fast regex-capable code search through a web UI and an OpenAPI HTTP
API — all from a single process with zero external services.

One process supervises a SurrealDB state/queue child, sync and index workers,
an optional Buf compatibility child, and an in-process searcher over the shard
directory; the [project README](../README.md#architecture) and
[PLAN.md](../PLAN.md) own the architecture and its decisions.

Indexing is **HEAD-only by default**: the default branch of each repo (or, for
watched local repos, whatever branch is checked out). An explicit per-repo
allowlist can add up to seven branch/tag revisions, selected with `rev:`.
For a repository with an `analysis_units` entry, search shards contain only
the configured primary and supporting paths at each admitted revision; status
reports `focused`. Repositories without an entry retain whole-repository
search and extraction. A focused repository may explicitly designate one
supporting SCIP artifact as `unit-bound`; without that designation it never
falls back to repository-root `index.scip`. The candidate planner gates
experimental extraction on one current streamed HEAD manifest. Local evidence,
coverage, source citations, and Workbench implementation views consume only
the unit records and are keyed by the exact indexed commit plus unit digest.
Repository-wide caller discovery remains a separately labeled overlay owned
by the T30.6 caller-overlay sequence. T30.6a emits one bounded,
non-authoritative extraction-operation report per repository job; T30.6b now
persists exact-generation per-domain outcomes so settled unavailable and
terminal generations do not blind-retry across restart; and T30.6c bounds the
complete post-lock job and cumulative mirror hold while scheduling
never-attempted domains ahead of oldest retryables. Candidate source-lane
classification is now consumed by focused local evidence: ordinary `go_test`
rows are excluded before blob open and exact `_test.go` SCIP documents are
removed after complete typed-artifact safety accounting. Empty-unit extraction
and focused search retain shipped behavior. The bounded resolver worker now
materializes ordered gRPC/Thrift catalogs from exact candidate and published
declaration generations using only committed Go module, layout-snapshot, and
generated-attribution inputs; ambiguous or unsupported authority stays
explicit. Its direct-caller projection additionally reads each mapped,
candidate-declared generated `base`-lane Go blob once during materialization;
leaf execution never reopens that generated source. Direct caller-leaf
execution now processes one exact `base`-lane domain/leaf pair at a time, opens
no source outside that leaf, and durably retains results or per-record
abstentions without making an incomplete generation visible. Atomic
complete-generation publication, authorized Caller Map reads and comparison,
and Workbench composition now consume that overlay; the shared scope panel
labels it `repository-overlay` beside focused Search and local evidence.

An optional `service_catalogs` entry can now ingest one explicit normalized
committed or operator multi-service authority for a repository. Publication is
fenced to the exact indexed HEAD and a complete regular-file census; invalid
replacement input preserves the previous authority. Without a v2 selection,
an indexed `analysis-unit-v1` scope imports as one digest-identical service
with its primary/supporting/typed roles and explicit unowned complement. These
catalog generations now reconcile independent desired/active/status rows,
monotonic incarnations, and retained removed tombstones. Catalog and lifecycle
state are durable and backup-safe but intentionally have no HTTP, MCP, search,
or UI registration yet; T33.4–T33.5 own those surfaces.

## Evidence boundary

Search, browsing, authentication, SCIP, and history are shipped product
surfaces. Contract Atlas, Caller Map, Impact, Kafka evidence, and Change
Workbench remain experimental/default-dark or fixture-bound as described in
the [workflow guide](./guides/WORKFLOWS.md). Static evidence does not establish
runtime use, complete coverage, compatibility, migration completion,
decommission safety, or extraction accuracy.

For the complete documentation inventory and authority map, see
[docs/README.md](./README.md).
