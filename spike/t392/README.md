# T39.2 authorized target operating-envelope gate

T39.2 measures one approved target repository in one named private environment.
This directory contains only the public protocol, the source-free receipt
boundary, and its tests. Target identity, catalog contents, queries, local
paths, credentials, raw logs, child output, and response bodies remain in the
private workspace.

The ticket cannot reuse an expired authorization. Preparing a candidate plan
does not authorize source ingestion or measurement. After every private field
is complete, the approver must explicitly approve the exact candidate; only
then may the custodian set the approval/freeze timestamps, record the digest,
and start the run. Any post-freeze edit requires a new nonce and approval.

## Retained outcome

The approved 2026-08-06 run retained [`results.json`](./results.json) with
outcome `stopped`. Exact preflight and cold publication succeeded. During the
one-file incremental transition, source, search, observation, and checked
relationship generations changed and three unaffected services remained
exact, but the bounded failure census reached one terminal `pipeline` failure
against a frozen maximum of zero. The transition therefore remained
non-current and the STOP rule skipped no-op, query, recovery, restore, and
retention. Their zero counters are `not_run`, not passing zero-work evidence.
Derived data, raw run artifacts, and credentials were destroyed; the prepared
source copy remains only under separate renewed private authorization. Direct
stopped, and cohort/P6 work was not triggered because the run ended before a
topology decision. This outcome establishes no target pass, SLO, accuracy,
release, migration-complete, or decommission-safe claim.

## Frozen private plan

Copy [`private-plan.template.json`](./private-plan.template.json) to the private
workspace and replace every placeholder. The template is deliberately invalid.
Freeze all of the following before measurement:

- one target repository, a base commit for the cold build, and a descendant
  target commit for the incremental transition;
- the exact phebs source commit, binary/config SHA-256 values, four required
  tool versions and executable SHA-256 values, and host capacity;
- one human-confirmed service catalog with its exact canonical digest, version,
  service/membership/unowned/fan-out counts, and private path;
- positive cold, incremental, no-op, cold/warm query, recovery, restore,
  retention, total-wall, RSS, byte, availability, and remaining-capacity
  thresholds;
- a 3–64-query private battery covering broad, literal, path, and symbol
  classes and All-code, service, and relationship scopes;
- the fixed phase order and failure classes, explicit stop conditions, and
  teardown owner/deadline/procedure; and
- every safety fence set to `true`: dedicated data, raw artifacts outside the
  repository, no post-freeze mutation, direct topology only, and no automatic
  service authority.

The service catalog is operator authority. It must not be inferred from source,
organizational labels, generated paths, or historical product evidence. The
plan validator enforces the production T33.1 caps and the fixed 16 KiB lifecycle
status bound.

After explicit human authoring, validate and canonicalize it without overwrite:

```sh
go run ./spike/t392/cmd/t392-catalog-check \
  -input /approved/private/t392/catalog.authored.json \
  -output /approved/private/t392/service-catalog.json
```

This command only checks supplied authority; it never scans source or proposes
services, placements, roles, or dispositions.

Record the exact frozen digest in private custody:

```sh
shasum -a 256 /approved/private/t392/private-plan.json
```

Pass it to the emitter as `sha256:<lowercase digest>`.

## Run order and STOP rule

Use the exact frozen binary and configuration against a dedicated data
directory. Before every phase, confirm authorization is still valid and the
remaining capacity is above the frozen minimum. A frozen identity mismatch,
authority refusal, threshold breach, unavailable required capacity, or failed
safety fence stops forward measurement. Preserve the prior complete authority,
classify the failure without raw text, skip later phases, and execute teardown.
Never widen a threshold or mutate the plan to turn a stop into a pass.

1. **Preflight.** Recompute artifact, config, source, catalog, and tool
   identities. Confirm target ancestry, host capacity, catalog authority, and
   authorization expiry before opening source.
2. **Cold.** Start at the frozen base commit and measure wall time, peak RSS,
   physical shard allocation, derived allocation, availability latency, source
   members, observation records, relationship records, and current publication.
3. **Incremental.** Advance only to the frozen target commit. Measure changed
   files, catch-up/RSS, post-transition shard allocation, required source,
   search, and observation generation changes, explicit relationship-generation
   checking, unaffected-service equality, and current authority.
4. **No-op.** Reconcile the already-current target again. A successful no-op
   requires zero index children, Git children, new chunks, shard-byte delta,
   generation changes, and durable writes.
5. **Query.** Run the frozen battery once cold and once warm through the
   authorized API. Keep requests/results private. Record only bounded timing and
   completion counts, canonical result-set equality, authority receipts,
   authorization decisions, and post-query final fences.
6. **Recovery.** Interrupt exactly one frozen derived-publication phase. Verify
   that the prior complete publication remains authoritative, partial output is
   invisible, and retry reaches current within the threshold.
7. **Restore.** Back up precious authority with the production path, restore to
   the dedicated installation, record validated or visibly omitted derived
   generations, and prove eventual current authority.
8. **Retention.** Observe all 13 lifecycle owners through the bounded status
   surface, exercise bounded sweeping, record protected roots and deletions,
   and retain the exact remaining-capacity check.
9. **Teardown.** Destroy derived data and release source custody, or retain the
   source only under an explicit renewed authorization. Derived data is never
   retained by this receipt.

The no-op `durable_writes` counter is scoped to catalog/service controls and
source, search, observation, resolver, posting, and relationship publication
controls or generation bytes. The bounded job-history row that requests and
records the replay is orchestration evidence, not a publication write; its
presence cannot be relabeled as a zero-cost scheduler or store operation.

For the observation recovery case, stop the dedicated process before staging
the incomplete publication. The helper refuses to touch a missing or
noncanonical current pointer and requires an explicit confirmation phrase:

```sh
go run ./spike/t392/cmd/t392-observation-interrupt \
  -data /approved/private/t392/data \
  -repository approved/private/repository-name \
  -confirm interrupt-incomplete-observation-stage
```

Restart only the frozen production binary. Its ordinary startup recovery must
discard the marker-covered incomplete generation without moving the prior
pointer; the private transcript must prove the partial directory and marker are
gone and the exact prior generation remains current. The helper is a bounded
fault injector, not a production repair command, and must never run against a
live or shared installation.

Failures use only these public classes:

| Class | Boundary |
|---|---|
| `authority` | authorization, target, catalog, or immutable identity refusal |
| `physical_index` | Git census, shard build, physical publication, or shard integrity |
| `pipeline` | source partitions, observations, resolver/posting/relationship publication, or scheduling |
| `query` | authorization, scope compilation, search, authority receipt, equality, or final fence |
| `recovery_restore` | interruption recovery, backup, restore, or convergence |
| `lifecycle_capacity` | status, protected roots, bounded sweep, capacity, or admission |
| `environment` | host, tool, process, operator, or teardown environment |

## Source-free receipt

Copy [`observation.template.json`](./observation.template.json) to the private
workspace. Populate it only from the frozen private record. The checked-in
observation must contain no private strings; all strings that cross into the
receipt are closed public vocabularies or the public phebs commit.

Emit to a new path; the command refuses overwrite:

```sh
go run ./spike/t392/cmd/t392-receipt \
  -plan /approved/private/t392/private-plan.json \
  -plan-digest sha256:<frozen-plan-digest> \
  -observation /approved/private/t392/source-free-observation.json \
  -output spike/t392/results.json
```

A `completed` receipt requires every phase, every frozen ceiling, exact no-op
zeros, query equality/authority/fences, an empty failure census, a successful
direct decision, and teardown. A `stopped` receipt requires at least one closed
failure classification and still requires valid teardown.

The public run commitment is a domain-separated SHA-256 over the exact private
plan and source-free observation. It allows the custodian to prove which frozen
run produced the receipt without exposing the target. The receipt explicitly
establishes no general SLO, accuracy, release authority, migration completion,
or decommission safety. It describes one environment only; cohort and P6 work
remain untriggered unless a failed direct gate explicitly reopens them.

```sh
go test ./spike/t392/... -count=1
make docs-check
```
