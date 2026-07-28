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

## Evidence boundary

Search, browsing, authentication, SCIP, and history are shipped product
surfaces. Contract Atlas, Caller Map, Impact, Kafka evidence, and Change
Workbench remain experimental/default-dark or fixture-bound as described in
the [workflow guide](./guides/WORKFLOWS.md). Static evidence does not establish
runtime use, complete coverage, compatibility, migration completion,
decommission safety, or extraction accuracy.

For the complete documentation inventory and authority map, see
[docs/README.md](./README.md).
