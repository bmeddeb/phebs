# T30.6m historical-publication retention decision

This retained neutral model inventories the publication owners and adjacent
persisted owners exposed by the T30.6m review. It records the decision to leave
historical evidence and caller/candidate publication residue explicitly
**unbounded** and changes no cleanup behavior. Structured `decision_relation`
values prevent that T30.6m choice from being used to justify unrelated job or
Investigation/Workbench growth, or to override the adjacent proof-bundle
owner's existing configured lifecycle.

The twelve owner groups cover T30.6 publication and adjacent persisted domains;
they are not an inventory of every database table. Audit, analytics,
authentication, and other installation state retain their separately
documented lifecycles.

The choice is conservative, not a claim that growth is safe. Existing
installations were promised exact historical evidence pending this decision,
and no neutral receipt establishes that two—or any other number—of old scopes
is a safe destructive default. A count would also fail to bound physical
storage: evidence runs vary up to 25,000 admitted association-plus-assertion
rows, durable proof/checkpoint/Investigation pins follow independently owned
lifecycles and can accumulate,
evidence atoms may be shared, caller publications retain process-local leases,
and SurrealDB allocation/compaction is not attributable to one logical owner.

The corrected model has twelve grouped owners, nine of which can grow across
transitions or product activity:

- historical published, pinned-superseded, and retention-quarantined evidence
  graphs in SurrealDB; sweep-eligible/in-progress backlog is also visible until
  bounded maintenance drains it;
- exact-scope extraction attempts;
- durable evidence-pin rows and the superseded runs they protect;
- failed partial candidate-generation residue on disk;
- caller generation admissions/outcomes in SurrealDB;
- successful caller-leaf residue from incomplete generations on disk;
- immutable proof bundles while their existing retention control is disabled;
- terminal durable job history across all eight queue tables; and
- Investigation/Workbench domain rows across 24 non-job tables.

Latest extraction outcomes are a separate, latest-only owner whose logical
diagnostic receipts are capped at 8 KiB. They are not an additional unbounded
growth class, but hiding their rows or receipt bytes would make operator status
incomplete.

Proof bundles have an existing lifecycle rather than a newly selected T30.6m
policy. `proof_bundles.retention` defaults to disabled: omission or `"0"`
keeps bundles indefinitely. A positive duration enables the existing sweeper,
which expires from the latest successful materialization and atomically removes
one expired `proof_bundle` plus exactly its
`proof-bundle:<bundle_id>` evidence pins. It deletes no extraction evidence;
the independent evidence sweep may later reclaim newly unpinned superseded
evidence when otherwise eligible.

The durable-job owner contains exactly `connection_sync_job`, `indexing_job`,
`repo_fetch_job`, `candidate_manifest_job`, `extraction_job`,
`resolver_catalog_job`, `caller_leaf_job`, and `investigation_run_job`.
Pending-key coalescing bounds only a pending target slot. Claiming releases
that slot, and terminal rows have no pruning path. This is incidental existing
growth, not a consequence or justification of the historical-publication
decision.

The neutral durable-job receipt isolates only the directly derivable default
resync traffic. Under a 365-day common year, the default one-hour interval, and
one healthy continuously draining remote connection, 1, 10, and 100
repositories produce 8,760, 87,600, and 876,000 `indexing_job` rows per year;
the connection produces 8,760 `connection_sync_job` rows per year in each
case, whether or not repository content changes. This assumes pending work
drains before the next tick. It does not estimate time to degradation or the
aggregate rate of downstream fetch, candidate, extraction, resolver, caller,
or Investigation jobs.

The Investigation/Workbench owner contains the exact 24 non-job tables listed
in `results.json`; `investigation_run_job` appears only in the job owner. No
aggregate or production-wired sweep bounds those rows. The package-local
single-artifact sweep has no production caller and would not bound the other
23 component tables. This growth likewise requires a separate decision.

Candidate publication authority remains current-only, but repeated failed
transitions can leave unbounded generation-named files until a later successful
cleanup or repository removal; the candidate root has no entry/byte ceiling.
Focused indexes retain current authority plus bounded reader transitions.
Resolver catalogs additionally own stages/residue. Their top-level
installation-root inventory refuses after an enforced 32,768-entry operational
scan threshold, which is not a storage ceiling; their 1,034 MiB clean-
replacement figure is also a design model, not a hard disk ceiling. Status must
therefore count all package-owned entries and apparent bytes. None gains a new
sweeper here.

`results.json` demonstrates the linear logical envelope without pretending it
is a disk-usage measurement. At 1, 10, and 100 maximum-admission historical
transitions, one evidence domain contributes 25,000, 250,000, and 2,500,000
association-plus-assertion admission rows; successful leaf content retained by
incomplete cap-plus-one caller generations contributes up to 576 MiB,
5.625 GiB, and 56.25 GiB. The same inputs model 1, 10, and 100 failed candidate
residue sets without assigning them a false byte bound. Shared atoms, database
overhead, manifests, stages, and filesystem allocation are intentionally not
summed into those figures.

## Selected follow-up

The corrected inventory requires five implementation PRs in dependency order:

1. **T30.6n — bounded durable-job reads and startup migration repair.** Replace
   `/api/repo-status`'s lifetime `indexing_job` materialization with bounded
   latest-per-live-repository work, and prevent both the first upgraded store
   startup and steady-state startup from loading and sorting terminal history
   across all eight job tables. Preserve pending, active, retry, successor, and
   repository-removal semantics. Schema or latest-projection work needed for
   bounded reads is allowed only with bounded, resumable reconstruction; legacy
   latest state may be explicitly partial or unavailable until it completes.
   A first-open full-history index build or backfill is forbidden. This ticket
   deletes no job row and selects no job-retention policy.
2. **T30.6o — authorization-first status shell and warning.** Add
   administrator-only `GET /api/retention-status`, the fixed twelve-owner and
   52-component registry, fixed response/scan budgets, independent
   `exact`/`lower_bound`/`unavailable` labels, and unconditional
   `unbounded_historical_publication_retention` warnings before `store.Open`
   and in every status response through `X-Phebs-Warning-Code`; successful
   bodies repeat it as `warning_code`. Authorization finishes before any
   inventory or cache touch. Ordered typed byte metrics remain non-combinable,
   and the proof-bundle owner alone discloses its effective configured lifecycle:
   zero reports disabled/accumulating and a positive duration reports
   enabled/nonaccumulating. Every collector initially reports `unavailable` and performs zero
   store or filesystem scans.
3. **T30.6p — core SurrealDB collectors.** Fill the evidence graph, attempt,
   outcome, pin, proof-bundle, durable-job, and caller-row summaries using one
   bespoke evidence classifier and generic bounded table summaries. Any needed
   query-index install/backfill is bounded, resumable, and nonblocking; no
   first-open full-history index build is allowed.
4. **T30.6q — Investigation/Workbench collector.** Summarize its exact 24
   component tables and owner lifecycle state. The same bounded, resumable,
   nonblocking index/backfill rule applies, with no first-open full-history
   build.
5. **T30.6r — derived-publication collectors.** Reconcile candidate, focused,
   resolver, and caller store authority with bounded filesystem inventories,
   replacing the final four `unavailable` owner summaries. Every installation-
   root and repository-directory scan has an explicit cap-plus-one budget.

Each ticket has its own retained size proof: T30.6n stays within one queue
subsystem and two consumers; T30.6o ships only the auth/warning/status shell,
registry, and budget model; T30.6p covers seven owners and 21 components through
one bespoke classifier plus one generic table path; T30.6q covers one owner and
24 exact tables; and T30.6r covers four owners and seven components through
four fixed filesystem adapters. Any additional unmodeled non-generic behavior
beyond T30.6p's retained evidence classifier and T30.6q's retained owner-
lifecycle classifier—or any unbounded bootstrap—must split again before
implementation. The five tickets do not fit one combined PR, and T30.7 depends
on completion through T30.6r.

The neutral status sample reports at most 4,096 identities after observing one
4,097th sentinel. That is a per-summary model, not a fabricated per-component
implementation bound. T30.6o selects and gates a fixed-work allocation that
covers every declared component without allowing growth in an earlier table to
hide a later table; T30.6p through T30.6r populate it incrementally. The
completed surface covers all exact component tables and filesystem components,
and partitions `evidence_pin` into
`proof-bundle:<bundle_id>`, `investigation-artifact:<artifact_id>`, and other
exact store-accepted kind namespaces. It must not list identities, disclose
repository names, combine unlike logical/canonical/apparent/physical byte
kinds, or mislabel logical rows/canonical bytes as physical SurrealDB bytes.

Data-volume total/available space remains a separate filesystem metric. None of
the five follow-ups adds cleanup, deletion, backup/restore mutation, a retention
configuration alias, or retained-owner lifecycle/data mutations. T30.6p and
T30.6q may add separately justified bounded-query indexes only with bounded,
resumable, nonblocking install/backfill and never a first-open full-history
build. T30.6r forbids unbounded directory walks or filesystem inventories.

The unconditional operational escape hatch is to monitor and expand or
relocate `server.data_dir`. Take a verified backup before supported repository
removal; removal reclaims derived files and makes non-quarantined unpinned
evidence sweepable, but pins and retention-quarantined evidence remain. Pin
rows remain until their owning lifecycle releases them. Retention-quarantined
evidence has no supported deletion procedure in this release and requires a
separately reviewed administrator resolution. Operators must not delete
database rows or publication artifacts by hand. A future bounded posture needs
a new ADR and separately reviewable evidence/pin, proof-bundle, durable-job,
Investigation/Workbench, caller-row, caller-filesystem/lease, and restore-
enablement tickets.

Run the retained gate with:

```sh
go test ./spike/t306m -count=1
```

This is a neutral capacity and ownership decision record. It establishes no
public-corpus accuracy, completeness, runtime-use, migration-completion,
decommission-safety, physical-database-byte, or bounded-retention claim.
