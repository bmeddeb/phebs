# T32.2 — Authorized whole-monorepo baseline

> **Retained engineering record.** This directory contains the source-free
> receipt boundary, private-run protocol, and completed public receipt. It is
> not current user guidance or a topology decision. Current sequencing remains
> in [`docs/ROADMAP.md`](../../docs/ROADMAP.md).

**Completed:** 2026-08-04 · **Status:** COMPLETE · **Production changes:** none

T32.2 measures the already-shipped whole-repository search, candidate,
extraction, recovery, and retention paths on one separately authorized target
monorepo. It does not inspect or publish service membership, select direct
shards/cohorts/P6, establish an SLO, validate extraction accuracy, or broaden
the one-RPC pilot.

## Two-record boundary

The run has two deliberately different records:

1. A **private authorized record**, stored outside this repository, contains
   the target identity and exact commit, source/mirror/config paths, query text,
   host identity and capacity, binary/config digests, prospective ceilings,
   authorization, stop conditions, raw logs, response bodies, and teardown
   custody.
2. The tracked [`results.json`](./results.json) contains only the closed
   scalar/enumerated shape emitted by `t322-receipt`: an opaque salted run
   commitment, phebs source commit, phase outcomes and timings, resource/count
   measurements, fixed failure classes, retention deltas, and teardown posture.

The private plan carries a fresh 32-byte random nonce before it is frozen. The
public commitment hashes the exact plan bytes and source-free observation bytes
under a domain separator, so a target commit or likely query cannot be tested
against the public value without the private nonce. The plan's separately
recorded SHA-256 is required by the emitter and detects a post-freeze edit.

Never place the private plan, raw observation worksheet, logs, response bodies,
query corpus, configuration, mirror, data directory, or target source beneath
this checkout. `.gitignore` entries under `spike/t322/private/` are only a final
accident guard, not the approved storage location.

## Completed result

The authorized run completed within every frozen private ceiling. The public
receipt records:

- measured clone/fetch in 177 ms;
- one visible 122,630,786-byte shard built in 1,686 ms with 529,367,040-byte
  peak RSS;
- an already-current restart with zero index-child runs and no shard byte or
  modification-time change;
- four cold and four warm queries completed with equal canonical result sets;
- a 329 ms candidate rebuild;
- ten successful serial extraction domains: six `published_nonempty` and four
  `published_empty`;
- candidate-phase recovery that preserved prior publication and eventually
  became current;
- all 52 retention components observed, with 16 changed and 36 unchanged; and
- the authorized source copy retained while all derived installation data was
  destroyed.

The run took 2,032,150 ms in total. The receipt is 14,669 bytes and has SHA-256
`d1ec7b658eef84d2974c50c66d6dca00160a412fd49154c1ad4e232baae695ad`.
Its negative claims are part of the record: it establishes no SLO or extraction
accuracy, selects no topology, and authorizes no multi-service release.

## Private preregistration

Copy [`private-plan.template.json`](./private-plan.template.json) to the
approved private workspace and replace every placeholder. The template is
deliberately invalid until completed. Before source ingestion or a target run,
the private custodian must freeze:

- authorization timestamps, approver, custodian, and expiry;
- one exact repository identity, immutable commit, and mirror location;
- the exact phebs source commit, binary SHA-256, private configuration SHA-256,
  and host identity/capacity;
- clone/fetch inclusion or exclusion;
- positive ceilings for total wall time, index wall/RSS/shard bytes, restart,
  cold/warm queries, candidate wall/spool, per-pack extraction, recovery,
  retention collection, and minimum remaining data-volume capacity;
- a 1–256-query battery containing at least broad, literal, path, and symbol
  classes;
- the exact ordered extraction domains enabled by each one-pack-at-a-time
  configuration;
- all six failure classes, every retained scalar family, the fixed phase
  order, stop conditions, and teardown owner/deadline/procedure; and
- `provisional_packs_off_for_index`, `one_extraction_pack_at_a_time`, and
  `raw_artifacts_outside_repository` set to `true`.

Record the exact plan digest in private custody:

```sh
shasum -a 256 /approved/private/t322/private-plan.json
```

The digest passed to the emitter is `sha256:<the printed lowercase digest>`.
Do not update the frozen file after recording it. A changed plan requires a new
nonce, authorization review, and freeze.

## Run order

Use a dedicated phebs data directory and private configuration. Do not reuse a
development or production installation. The exact operational commands, local
addresses, API key, target path, and interruption mechanism belong only in the
private record.

1. **Before snapshot.** Capture the administrator-only retention-status
   response and exact available data-volume metric. Record artifact/config
   digests and confirm the frozen stop/teardown authority.
2. **Clone/fetch.** Either measure the authorized mirror acquisition or record
   it as excluded exactly as preregistered. Do not silently mix acquisition
   cost into indexing. A failed measured acquisition has no per-phase outcome
   field: record it as a stopped run with a `clone_fetch`-phase failure entry,
   classed `physical_index` for acquisition/mirror faults or `environment` for
   authorization, host, or tool faults.
3. **Whole-repository index.** Omit `analysis_units`; disable every provisional
   evidence pack. Enable job diagnostics and, only in the private log,
   `indexing.verbose` when needed. Record child wall time and peak RSS, final
   apparent shard bytes/count, publication visibility, and any classified
   stop. Raw child output never enters the public observation.
4. **Restart/already-current.** Snapshot shard byte totals and modification
   times, restart unchanged, and prove that the index child did not run, shard
   bytes did not change, and the existing publication stayed visible.
5. **Cold/warm search.** Run the frozen query battery once cold and once warm
   through the authenticated API. Keep queries and response bodies private;
   record only completion/failure counts, total/max wall time, and whether
   canonical private response digests match between passes.
6. **Candidate planning.** Restart with exactly one preregistered provisional
   pack and `diagnostics.jobs`, `diagnostics.candidates`, and
   `diagnostics.extraction` enabled. Copy only the allowlisted candidate receipt
   scalars into the private observation: decision/outcome, queue/lock/phase
   milliseconds, logical peak spool, declared bytes, and plane record counts.
7. **Extraction packs.** Enable and execute one preregistered pack at a time in
   the frozen domain order. Record ordered domain outcome/reason, phase timing,
   candidate/opened source counts and bytes, facts, and unresolved count. Raw
   errors, paths, samples, job IDs, repository/commit/unit digests, and child
   output remain private.
8. **Recovery.** Under the dedicated installation, interrupt the frozen index,
   candidate, or extraction phase. Verify the prior complete authority remains
   readable and the restarted pipeline either becomes current inside the
   ceiling or records a classified stop. Never weaken publication or
   authorization checks to make the recovery pass.
9. **After snapshot.** Capture retention status again. Record all 52 components
   as changed or unchanged, preserving exact/lower-bound/unavailable values and
   keeping logical/canonical/apparent/physical byte kinds separate. Do not sum
   non-combinable byte kinds.
10. **Stop/teardown.** Apply the frozen private procedure. The public receipt
    may say only `destroyed` or `retained_under_authorization`; a failed teardown
    blocks emission.

Every failure is recorded without raw text as exactly one of:

| Class | Boundary |
|---|---|
| `physical_index` | mirror acquisition, mirror-to-shard build, RSS/disk/FD pressure, shard integrity, or physical publication |
| `candidate` | census, partition planning, spool/sort, candidate publication, or candidate recovery |
| `extraction` | strict-open, source reads, extractor/staging/outcome publication, or extraction recovery |
| `relationship` | resolver/caller work triggered by the selected pack |
| `store_retention` | SurrealDB transition, retention-status collection, retained-state growth, or capacity |
| `environment` | authorization expiry, host/tool/config mismatch, operator stop, or teardown environment |

## Source-free observation and receipt

Copy [`observation.template.json`](./observation.template.json) to the approved
private workspace and fill it only from the frozen private record. Its strings
are closed public vocabularies; unknown fields, domains, component IDs, byte
kinds, reasons, phases, or failure classes are rejected. A `completed` outcome
must satisfy every frozen ceiling and every required phase. A prospectively
stopped run is valid only with at least one fixed failure classification and a
successful teardown posture.

Emit to a new path; the command refuses to overwrite an existing file:

```sh
go run ./spike/t322/cmd/t322-receipt \
  -plan /approved/private/t322/private-plan.json \
  -plan-digest sha256:<frozen-plan-digest> \
  -observation /approved/private/t322/source-free-observation.json \
  -output spike/t322/results.json
```

Before review, inspect the emitted diff and verify that it contains no private
identifier, target commit, path, query, host/tool identity, frozen threshold,
raw error, response, credential, or unclosed infrastructure detail. The model tests prove
unknown-field rejection, frozen-plan binding, commitment sensitivity, the
closed 52-component registry, ceiling enforcement, fixed negative claims, and
absence of representative private values:

```sh
go test ./spike/t322/... -count=1
make docs-check
```

No checked-in receipt may claim more than one measured target/environment. It
is an engineering observation that informs T32.4; it is not a generally
applicable scale result.
