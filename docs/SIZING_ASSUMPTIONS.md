# phebs pilot sizing assumptions and worksheet

*Draft v0.1 · pilot prerequisite item 4 · design phase only*

This dependency-preview draft defines the inputs, formulas, safety margins,
and stop rules needed to size the bounded pilot without ingesting source.
Threat-model item 1 is not assigned or accepted, and the authorized workload
assumptions do not yet exist. Therefore every environment-specific value
remains `TBD`; this document does not authorize capacity checks, source access,
hardware procurement, Gate 1, Gate 2, or Epic 16.

## Record status

| Field | Value |
|---|---|
| Owner | `TBD` |
| Reviewer | `TBD` |
| State | `blocked_unassigned` |
| Depends on | accepted threat model plus declared workload assumptions |
| Measurement authority | none; verification item 7 requires separate pilot-environment authorization |
| Required acceptance evidence | artifact digest, reviewer identity, timestamp, decision, unresolved findings |

## 1. Sizing principles

1. Metadata-only estimates precede retained source. Source-derived
   measurements begin only at the charter's authorized scale checkpoint.
2. Averages never size a hard limit. Peak concurrent memory, temporary disk,
   largest eligible unit, output caps, and failure recovery are modeled
   independently.
3. Precious state and derived state are separated. Mirrors and indexes may be
   rebuilt; authentication, permission, audit, evidence, decision, pin, and
   retention state require an approved backup/restore posture.
4. Indexing, extraction, search, MCP, database, backup, and restore have
   separate resource envelopes. One workload cannot borrow a security reserve
   silently from another.
5. The pilot stops rather than relaxes authorization, provenance, publication,
   output, timeout, retention, or reviewer-custody controls under pressure.

## 2. Declared workload assumptions

The owner fills this table using approved non-source metadata and cites the
authority, collection time, and uncertainty for every value.

| Symbol | Assumption | Value | Authority / as-of | Uncertainty treatment |
|---|---|---|---|---|
| `W_REPOS` | repositories in the authorized universe | `TBD` | `TBD` | upper bound, not average |
| `W_GIT_BYTES` | packed Git bytes to retain | `TBD` | `TBD` | include growth and object overhead |
| `W_TREE_FILES` | regular files in frozen trees | `TBD` | `TBD` | include generated/vendor populations separately |
| `W_SOURCE_BYTES` | readable source bytes | `TBD` | `TBD` | separate eligible and excluded bytes |
| `W_GO_FILES` | eligible first-party Go files | `TBD` | `TBD` | enumerate per frozen build policy |
| `W_PROTO_FILES` | eligible protobuf files | `TBD` | `TBD` | record import/root uncertainty |
| `W_TARGETS` | eligible build targets/units | `TBD` | `TBD` | independent enumeration required |
| `W_FACTS` | projected extracted facts and occurrence associations | `TBD` | `TBD` | low/base/high cases |
| `W_SNAPSHOTS` | retained immutable snapshots during pilot | `TBD` | `TBD` | include superseded/pinned artifacts |
| `W_CHANGE_RATE` | commits or changed eligible bytes per interval | `TBD` | `TBD` | p50/p95/burst |
| `W_USERS` | named pilot principals | `TBD` | `TBD` | role and visibility distribution |
| `W_SEARCH_QPS` | interactive search request rate | `TBD` | `TBD` | p50/p95/burst and concurrency |
| `W_MCP_QPS` | agent-shaped tool request rate | `TBD` | `TBD` | tool mix, output sizes, amplification |
| `W_EXPORTS` | dossier/proof exports retained concurrently | `TBD` | `TBD` | recipient scopes and maximum size |
| `W_RETENTION` | retention periods by artifact class | `TBD` | `TBD` | legal/deletion overrides |

The workload declaration also freezes supported language/protocol constructs,
build tags, generated input treatment, excluded paths, metadata sources,
query mix, result bounds, freshness target, backup frequency, and permitted
maintenance window. A missing dimension blocks acceptance.

## 3. Resource model

All terms use observed or independently justified **upper bounds**. Units and
decimal/binary conventions are recorded alongside values.

### Disk

```text
D_steady = D_git + D_index + D_db + D_evidence + D_logs + D_cache
D_publish_peak = D_steady + D_index_build_tmp + D_extract_stage_tmp
D_backup_peak = D_publish_peak + D_backup_staging + D_restore_scratch
D_required = max(D_publish_peak, D_backup_peak) * F_disk + D_emergency_reserve
```

`F_disk` is a preregistered uncertainty/growth factor, not a value selected
after measurement. `D_emergency_reserve` remains unavailable to routine work
and must be sufficient to seal failure records, release leases, and stop
cleanly. The disk watermark and cleanup policy cannot delete pinned or
mandatory-retention evidence.

### Memory

```text
M_steady = M_server + M_surreal + M_search_working_set + M_caches
M_index_peak = M_steady + M_zoekt_child_peak
M_extract_peak = M_steady + M_extract_worker_peak * C_extract
M_query_peak = M_steady + M_query_peak_each * C_interactive
                         + M_mcp_peak_each * C_agent
M_required = max(M_index_peak, M_extract_peak, M_query_peak) * F_memory
```

Index and extraction concurrency are not assumed simultaneous unless the
operating policy structurally prevents it. Cache budgets count retained object
overhead as well as payload bytes. Cooperative in-process parser limits are
not treated as hard memory isolation.

### CPU, time, and throughput

```text
T_cold = T_clone + T_index + T_extract + T_publish + T_audit
T_incremental = T_fetch + T_delta_or_full_index + T_extract + T_publish
Q_capacity = min(Q_search_budget, Q_db_budget, Q_output_budget)
```

The worksheet records cold and incremental p50/p95/worst observed times,
separate interactive and MCP concurrency, queue delay, cancellation latency,
shutdown drain, stale-worker recovery, backup time, restore time, and teardown
time. Partial results and retries are counted as cost, not discarded.

## 4. Product limits to preserve

The accepted worksheet records the exact implementation version and verifies
its effective limits. Current design expectations include:

- immutable revision binding for every search/evidence read;
- explicit per-file, aggregate-source, parser, fact, association, output,
  pagination, timeout, and cache limits;
- bounded worker concurrency and retry/backoff;
- isolated high-memory index children;
- atomic staged extraction publication;
- separate interactive and agent-shaped traffic measurements;
- disk-space stop behavior that preserves current publications and precious
  state.

An implementation limit is a safety ceiling, not proof that the selected
pilot workload fits beneath it.

## 5. Non-source feasibility worksheet

Before Gate 1, fill only from approved metadata or synthetic measurements:

| Check | Input | Calculation / test | Result | Confidence / blocker |
|---|---|---|---|---|
| source and target population envelope | `W_*` declarations | low/base/high bounds | `TBD` | `TBD` |
| steady and peak disk | §3 disk model | all publication/backup phases | `TBD` | `TBD` |
| peak memory | §3 memory model | index/extract/query concurrency | `TBD` | `TBD` |
| cold-time feasibility | metadata size + synthetic calibration | `T_cold` bound | `TBD` | `TBD` |
| incremental freshness | change-rate envelope | `T_incremental` bound | `TBD` | `TBD` |
| query/MCP capacity | frozen tool/query mix | p95 plus output bounds | `TBD` | `TBD` |
| backup/restore window | precious bytes + procedure | time and scratch space | `TBD` | `TBD` |
| teardown window | complete artifact inventory | destruction and witness time | `TBD` | `TBD` |

An unknown or low-confidence upper bound remains a blocker; it is not replaced
with a convenient average.

## 6. Authorized capacity-check sequence

Verification item 7 runs only after item 4 acceptance and separate
environment authorization:

1. Record clean-host resources, hard ceilings, stop mechanism, exact binary,
   toolchain, configuration, and workload manifest.
2. Measure a representative authorized slice without using it for a
   completeness claim.
3. Compare measured multipliers with the preregistered bounds. If any hard
   ceiling, stop behavior, or uncertainty allowance fails, stop before scale.
4. Measure the declared scale: cold mirror/index/extract/publish, incremental
   update, interactive query mix, agent-shaped MCP mix, backup, and isolated
   restore.
5. Repeat peak phases after restart and failure recovery; retain failed and
   partial costs.
6. Publish observed values, distributions, configuration, resource graphs,
   artifact digests, deviations, and reviewer decision.

## 7. Stop conditions

The capacity check stops without tuning the frozen claim when:

- disk reaches the preregistered watermark or emergency reserve is consumed;
- memory, CPU, process, output, timeout, or concurrency ceiling is exceeded;
- cancellation, shutdown, or stale-worker recovery fails to bound work;
- a partial/stale publication appears complete;
- authorization, logging, credential, or egress controls are bypassed;
- source scale falls outside the declared workload envelope;
- backup, restore, or teardown cannot fit the approved window or inventory;
- a required measurement is missing, irreproducible, or collected after an
  uncontrolled configuration change.

## 8. Acceptance boundary

Item 4 can be accepted as a design only after item 1 acceptance, an authorized
owner supplies every workload assumption and its provenance, all formulas and
safety factors are frozen, the reviewer records a decision, and no `TBD`
affecting feasibility remains. Acceptance authorizes neither item 7 nor source
ingestion.
