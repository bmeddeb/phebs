# phebs · active backlog

T31.1 bounded pipeline diagnostics and Epic 32's complete microservice v2
contract/validation gate are retained in the completed backlog. T32.5 recorded
a conditional implementation GO on 2026-08-04 without authorizing runtime
registration or release. T33.1's canonical service-catalog contract, T33.2's
catalog ingestion/v1 migration, T33.3 independent service state, T33.4
authorized catalog reads, and T33.5's service directory/neutral demo are
retained in the completed backlog, closing Epic 33. T34.1's immutable
repository source/search generation, T34.2's exact service-query compiler,
T34.3's publication migration/recovery, and T34.4's shared search product/demo
are also complete, closing Epic 34. T35.1's generation-scoped scheduler and
T35.2's pin-aware lifecycle decision and T35.3's bounded sweep/capacity
control and T35.4's lifecycle recovery demo are complete, closing Epic 35.
T36.1's bounded immutable Git reader and source-partition contract is complete.
T36.2's shared Go source-observation contract, T36.3's content-addressed
observation publication, and T36.4's authorized progress/neutral multi-pack
demo are complete, closing Epic 36. T37.1's namespace-sharded declaration and
resolver catalog, T37.2's RPC caller postings, T37.3's Kafka topic postings,
and T37.4's service projections and atomic relationship roots are complete;
T37.5's exact readers, comparison, proof/Workbench integration, and neutral
demo are also complete, closing Epic 37. T38.1's exact service overview,
T38.2's cross-service explorer, T38.3's service-aware Impact/Workbench,
T38.4's strict MCP parity, and T38.5's neutral product closure are complete,
closing Epic 38. T39.1's neutral correctness, scale-admission, and recovery
gate and T39.2's honestly stopped authorized target run are retained in the
completed backlog. T39.3's security/lifecycle gate is also retained there.
T39.4's evidence/workflow gate stopped before unsealing and T39.5 retained the
no-release decision, closing Epic 39. T39.R1's mirror-lock contention closure
is also complete without authorizing or superseding a target rerun. A
source-free diagnostic from a later unfrozen very-large-monorepo run is
retained as engineering evidence, not as a scale pass. Epics 40–42 are now the
explicit scale-convergence program: T40.1–T40.11 are complete and T40.12 is next,
while Epic 41 separately targets
at least 8,000 accepted services and measures 10,000 accepted logical services,
and Epic 42 composes the physical-repository and service-cardinality envelopes.
Epic 43 runs in parallel as the charter-governed presentation track: it
applies [DESIGN_CHARTER.md](./DESIGN_CHARTER.md) to every product surface,
starts at T43.1, and may not touch a scale plane, authority, or claim.
Epics 25–28 remain drafted and unscheduled; none is an implicit next ticket.
Epic 30's service-scoped
monorepo program completed on 2026-08-02, including the scope-aware UI,
operations guidance, and neutral ordinary-worker demo in T30.7. Its retained
completion receipt also records the post-review compatibility closure:
immutable v1/v2 proof bytes remain readable and the production exact Caller
Map envelope is validated through the strict MCP boundary. Completed
Epics 0–24, Epics 29–39, and P5 hardening are retained in the
[completed backlog](./BACKLOG_COMPLETED.md). Current posture and decision
points are summarized in [ROADMAP.md](./ROADMAP.md).

New work starts here only after its product boundary, dependencies, acceptance
criteria, and dated [PLAN.md](../PLAN.md) decision are reviewed. Tickets remain
PR-sized and dependency-ordered for a stacked workflow.

## Epic 40 · Very-large-monorepo derived-pipeline convergence *(in progress · T40.1–T40.11 complete · T40.12 next)*

Make the source-observation, candidate, extraction, and downstream generation
pipeline converge under a neutral repository shape with at least two million
regular-file owners. Search already demonstrated that it can publish a shared
physical generation beyond 1.6 million owners in one unfrozen environment;
that observation does not establish comfortable headroom, a supported scale,
or derived-pipeline completion. This epic closes the semantic and bounded-work
gaps that allowed committed search authority to coexist with opaque
observation-plan refusal, repeated repository-wide manifest validation, and
nonpublishing extraction work.

### Boundary

- Search publication and derived evidence are independent authority planes. A
  committed, validated search generation remains current when observation,
  extraction, resolver, or relationship work is unavailable or fails. The
  exact derived generation remains visibly pending, failed, or unavailable.
- The initial neutral envelope has at least 2,000,000 regular-file physical
  owners, at least 1,000,000 repository-lane placements and separately at
  least 1,000,000 caller-lane placements, more than 8 GiB of declared
  repository-lane Go bytes, and at least 32,768 IDL candidate inputs. T40.1
  separately
  freezes each plane's placements, unique blobs, declared bytes, and encoded
  bytes; overlapping repository/caller planes are never summed as unique
  source. Exact structural and semantic profiles precede any production
  aggregate cap change.
- Existing per-blob, per-member, per-child, per-request, per-evidence-chunk,
  and per-domain safety limits remain fixed unless the owning ticket measures
  that exact dimension and records a reduce-first decision. Increasing a
  timeout, retry count, or monolithic buffer is not an implementation.
- Generation controls stay small; corpus-sized records live in ordered,
  digest-bound members. Cold validation may stream a complete generation once;
  hot point reads, no-ops, retries, and status reads may not reopen or hash the
  complete corpus.
- A file-local unsupported syntax becomes an explicit classified gap when the
  pack contract permits per-file independence. It may not silently disappear
  or fail unrelated files. A corrupt control, inconsistent member, unknown
  authority, or aggregate-bound violation still refuses the complete domain.
- This epic does not multiply work by service count, raise the 4,000-service
  catalog cap, select cohorts or P6, authorize a private rerun, or change the
  T39 `DO_NOT_RELEASE` decision. Epic 41 owns service cardinality; Epic 42 owns
  the combined gate.

**T40.12 · Authorized and product-consumer compatibility replay** — migrate or
adapt exact relationship readers, comparison, proof, Workbench, HTTP, MCP, and
UI only where their generation seams changed, then replay their existing
acceptance gates. AC: authorization precedes identity/control/filesystem work;
root, cursor, citation, revision, and concurrent-publication fences remain
exact; sparse point/page reads do not reopen a complete observation/extraction
generation; partial/failed/unsupported states remain visible rather than
empty; v1/v2 retained evidence and proof bytes remain readable; this ticket
adds no new product claim or query shape; full API/MCP/UI and merge bars.

**T40.13 · Neutral two-million-owner convergence gate** — run the frozen T40.1
profiles through ordinary sync/index, candidate, observation, extraction,
resolver, relationship, recovery, and lifecycle workers with a deliberately
small at-most-100-accepted-service correctness control whose exact membership
and unowned-prefix counts stay within the existing v2 12,000-path cap. AC: at
least 2,000,000 regular-file physical owners publish one shared search
generation; the receipt names T40.5's effective blob-reader mode and proves no
silent batch disablement or go-git fallback; all applicable
derived partitions settle and publish, with deliberately absent inputs and
unsupported syntax remaining explicit; cold, warm no-op, one-partition delta,
A→B→A, interruption, stale worker, pressure, archive/restore, collection, and
authorized query cases pass exact oracles; the receipt reports wall/RSS,
allocated/logical bytes, children, reads, writes, transactions, retries, and
reuse by phase using closed source-free scalars; predetermined stop rules
choose continue, reduce, cohort experiment, or P6 investigation without
silently raising a bound. The result is mechanics evidence only—no target SLO,
service-cardinality, accuracy/completeness, release, private-rerun, migration,
or decommission claim. Epic 40 closes only on an independently reviewed receipt
and demo.

## Epic 41 · Ten-thousand-service authority and sparse consumers *(scheduled after Epic 40)*

Raise logical-service capacity through segmented authority and bounded state/
publication design, not a constants-only change. The required floor is 8,000
accepted services; the planned accepted neutral target is 10,000, and an
accepted-only 12,500-service shape is the ceiling comparator. Total service
records across every disposition are a separate admitted dimension. The
large-monorepo diagnostic selected no service catalog and provides no evidence
for this epic.

### Boundary

- T41.1 creates a new schema/package/receipt and leaves the digest-bound T32.3
  1,000/5,000-service v1 generator and artifacts byte-identical. Accepted-only
  fan-out-20 profiles freeze candidate
  services/memberships/distinct-path tuples of 8,000/48,000/25,280,
  10,000/60,000/31,600, and 12,500/75,000/39,500. A separate small semantic
  profile owns proposal/conflict/rejected, successor, tombstone, and re-add
  cases so those records never distort the accepted-cardinality arithmetic.
- Service key, incarnation, desired/active identity, dispositions, role
  vocabulary, per-service path envelope, and accepted fan-out 20 remain exact.
  Aggregate total records, memberships, distinct paths, successor edges,
  logical bytes, and encoded publication bytes are independent caps. Total
  claims per placement remains separately bounded even when accepted fan-out
  is small.
- `phebs-service-catalog-v3` uses a small immutable root, service-key-range
  members, and placement-path-range members containing both accepted/
  nonaccepted claims and unowned state. The two member views must agree exactly.
  v1/v2 bytes remain readable; migration is side-by-side and lazy.
- Point service and path reads are member-local; pages read only intersecting
  members; full reconcile/relationship work streams each view once. No query
  repeatedly decodes a 10,000-service catalog and no state transition places
  every service in one database transaction.
- v3 ingestion stays runtime-dark until state, sparse backend, transport, and
  relationship consumers all exist. Partial reconcile is never authority for
  service reads; later activation may make individual services current under
  exact summary CAS fences. A successor catalog cannot skip an unsettled
  reconcile generation.
- Epic 41's closure uses a deliberately small at-most-50,000-regular-file
  physical corpus. Passing it is not large-repository evidence, a supported
  service count, target SLO, accuracy, release, or P6 claim. Epic 42 composes
  both dimensions.

**T41.1 · Production-aligned 10,000-service profiles and cap decision** — add
new `t411-service-load-profile-v1` accepted-only 8,000-, 10,000-, and
12,500-service profiles using typed-requires-supporting and fan-out 20, plus a
separate small transition/authority profile. Reuse the deterministic identity
discipline of T32.3 without changing or copying its retained bytes; service
authority is explicit and never inferred from generated directories or import
edges. Shared/generated fixture paths are assigned to deterministic groups of
at most 20 accepted services rather than one global shared membership. Freeze
accepted and total service
records independently; exact memberships, paths, roles, successor graphs,
canonical bytes, encoded publication bytes, fixture content, total
claims-per-placement, and empty/mixed/dense relationship distributions. AC:
10,000 accepted services is the pass target and anything below 8,000 cannot
close the program; the 12,500 accepted-only comparator selects a measured hard
total-record ceiling or exact pre-growth refusal. Draft reduce-only caps are
12,500 total service records, 75,000 memberships, 40,000 distinct paths,
12,500 aggregate successor edges, 512 successors for one service, 16 MiB
logical canonical bytes, and 32 MiB encoded publication members; accepted
fan-out 20, total claims per placement 4,000, and existing per-service/path
limits do not increase. One maximum service combines maximum path and successor
bytes, and one maximum path carries the total-claim bound; either must fit its
2-MiB catalog member. The same maximum path must also fit the existing 1-MiB
relationship-projection wire; otherwise T41.1 reduces the total-claim cap or
selects an explicit bucketed claim representation. Two builds are byte-identical;
old T32.3 artifacts/digests are unchanged;
the receipt records RSS, wall, allocation, serialization, projection,
store-transaction, filesystem, and lifecycle estimates by byte kind; no
runtime registration or production cap change; dated PLAN decision and full
merge bar.

**T41.2 · Segmented service-catalog v3 contract** — implement the pure root and
dual member views while retaining v2 record semantics. Draft reduce-only
bounds are 512 services/2 MiB per service-range member, 2,048 paths/2 MiB per
placement-range member, 64 total members, a 256-KiB root, and T41.1's separate
logical/encoded aggregates. AC: the root binds repository/source/base/override
authority, aggregate disposition/role/path/successor/claim counts, ordered
nonoverlapping service and path ranges, all member digests/bytes, one logical
catalog digest, and policy digest; validation proves internal membership/path
cross-view equality, unowned exclusivity, overlap/fan-out/total-claim bounds,
successor liveness/cycles, ordering, and exact member inventory. Source-file
liveness and census complement remain T41.3. Placement members are
prefix-routed subtrees: each contains a bounded, digest-accounted inherited
ancestor-claim prelude, so cross-service nested prefixes are never missed and
one file-path lookup reads root plus one matching placement member. Complete
validation proves every inherited claim against the service view and rejects
missing/extra duplication; T41.3 merge-streams the ordered placement view once
against the source census rather than performing one lookup per file. Service
lookup reads root plus one service member; full iteration streams in canonical order; every
bound is pre-growth; every v2-admitted catalog maps to identical v3 logical
semantics and a mapped digest, while expanded v3 refuses v2 downgrade; maximum
combined service paths/successors and maximum path claims each fit one member;
no runtime import; full merge bar.

**T41.3 · Dark catalog-v3 ingestion and immutable store authority** — ingest
operator or committed selections into T41.2, stream the source census to prove
placement liveness and exact complement, and store precious immutable root/
member rows instead of one monolithic JSON string. Keep ordinary v3 runtime
selection unregistered through T41.9. AC: complete validation precedes a dark
candidate pointer; every identity collision is byte-equal or refusal;
same-authority-version/different-bytes refuses; committed still means declared
version unless a separate provenance decision changes it; restart no-op uses
bounded metadata and selected file reads; v1/v2 current/historical authority
remains unchanged; startup schema uses an explicit idempotent migration rather
than `IF NOT EXISTS` assertion drift; malicious, partial, concurrent,
crash-gap, census, and live maximum-shape SurrealDB tests; full merge bar.

**T41.4 · Catalog-v3 recovery, archive, and lifecycle owner** — own v3 root/
member durability before state consumes it. AC: startup repairs only complete
strict-valid candidates and removes bounded orphans without changing the v2
pointer; backup/restore includes every referenced precious root/member byte
and revalidates exact inventory; the lifecycle owner rechecks current, dark
candidate, future desired/active state references, rollback floor, and active
state roots inside the destructive transaction while holding the shared
mutation lock across it; completed backups never pin live rows. One atomic
`collecting`/tombstone transition removes the generation from historical
authority before the first member drain; roots and members collect in bounded restartable order with exact
logical/member byte accounting, fair cursors, malformed-owner isolation, and
pressure/status integration; maximum-member, interrupted collection/restore,
orphan, malformed-row, current/prior, and one-over tests; full recovery/merge
bars.

**T41.5 · Resumable service-state reconcile and activation** — replace
all-service transactions with durable at-most-512-row chunks, reducible by
T41.1 measurement, and explicitly separate the two protocols. Reconcile keeps
the repository summary catalog-mismatched and all service reads unavailable
until one final summary CAS; activation starts only after that match and updates
each chunk of rows plus the already-matching summary atomically so independently
current services remain readable. All v3 rows, summaries, plans, chunks, and
pointers occupy a distinct versioned shadow namespace until T41.9; preactivation
tests require existing v1/v2 rows, revisions, summaries, and pointers to remain
byte-identical. AC: a store fence refuses publication of a
successor catalog while reconcile is unsettled; every chunk fences catalog,
plan, prior row revision/digest, and tombstone counters; restart reuses settled
chunks; removed→re-added increments incarnation once; terminal failure permits
bounded repair/continue only—rollback requires a future preimage/staged-row
contract; stale/concurrent chunks roll back; active/desired generation
references have an index or bounded aggregate pin; 10,000-service cold/no-op/
small-delta/A→B→A/live-store tests bound rows, bytes, locks, reads, and wall
time with no O(N²) projection; reconcile/activation plans and chunks reuse the
generation-scheduler stage/lease identity and its named lifecycle coverage for
settled, superseded, interrupted, and restored rows; explicit schema migration
and full merge bar.

**T41.6 · Sparse catalog/state/search backend** — migrate verified store point/
page state reads and service-query compile/runtime to root/member authority,
without adding HTTP/MCP/UI changes. AC: a service detail/search reads one root,
one service member, bounded state rows, and at most one historical member for a
stale service; inventory pages read only intersecting members; one verified
generation/member cache has bounded entries and lease-delayed retirement;
batch relationship consumers stream the catalog/state once instead of opening
it per 101-row page; malformed current v3 never falls back to v2; selected
512-successor service state remains within response/backend bounds; cold,
warm, revocation seam, concurrent-publication, and exact read/hash/query count
tests at 10,000 services; full focused and merge bars.

**T41.7 · Authorized HTTP/MCP/UI v3 parity** — project T41.6 through service
inventory/detail, status, search scope, HTTP, MCP, and the existing directory
without changing authority semantics. AC: authorization precedes repository/
service lookup; cursors bind visible repositories, catalog generation, member
range, principal, and page state; one page reads only intersecting members;
the selected 512-successor posture is returned under the existing bounded
detail response rather than re-decided here; stale/partial/unavailable/conflict/
removed states remain explicit; forged cursor, revocation, hidden-service,
deep-link, pagination, concurrent publication, browser, and 10,000-service
paged-UI bounds pass; v3 selection remains runtime-dark; full merge bar.

**T41.8 · Dark bucketed relationship publication** — replace
one-file-per-service relationship storage and the monolithic hot root with a
small control root plus bounded buckets in the v3 shadow namespace. Preserve
accepted-placement and nonaccepted-claim evidence. Keep the existing
20-million total-reference,
one-million per-service, 512-MiB resident, and 20-GiB generation ceilings unless
T41.1 reduces them. AC: T41.1's exact empty/mixed/dense reference and
claims-per-placement distributions drive maximum tests; repository/service
reads touch required buckets only; full build streams catalog/state once;
service-local failure remains visible; root identity binds incarnation/
desired set and every upstream generation; complete validation/recovery is
before pointer swap; lifecycle drains bounded buckets rather than one file per
service; pins, leases, rollback floor, archive/restore, pressure, and corrupt
derived omission remain exact; v1/v2 relationship authority remains
byte-identical and selected; full merge bar.

**T41.9 · Atomic v3 runtime registration and reverse transition** — add an
explicit operator/config opt-in only after dark catalog, state, search, and
relationship authority is fully reconciled and strict-current. A valid dark
candidate never auto-promotes. AC: one CAS-controlled runtime selector is the
linearization point across all v3 consumers; the prior v1/v2 authority remains
readable until that selector changes; crash immediately before/after activation
recovers to exactly old or new authority; startup verifies every selected root
before serving; older binaries refuse the selector safely; disablement builds
and reconciles the complete reverse target before a selector CAS—no pointer-only
downgrade or mixed summary; authorization, no-fallback, concurrent catalog/
state/relationship transition, rollback-floor, and backup/restore tests; the
registration changes no evidence release posture; full merge bar.

**T41.10 · Neutral 10,000-service closure gate** — exercise the T41.1 target on
the deliberately small physical corpus through ingestion, state reconcile/
activation, shared search, relationships, authorized HTTP/MCP/UI reads,
archive/restore, and lifecycle. AC: 10,000 accepted services publish and become
independently queryable; the floor of 8,000 accepted services is explicit; the
separate transition profile passes; exact-bound and one-over behavior match
T41.1; cold, no-op, point/page, one-service and 1% deltas, removal/re-add,
A→B→A, partial activation,
crash, stale worker, authorization, pressure, backup/restore, and collection
pass; receipt records root/member/cache/transaction/query/read/write/RSS/disk
cost without source identity; no operation performs service-count × repository
bytes work. The result establishes no large-repository envelope, target SLO,
supported customer limit, accuracy/completeness, release, migration, or
decommission claim.

## Epic 42 · Combined scale gate and topology decision *(scheduled after Epics 40–41)*

Compose the independently proven physical and logical dimensions. A system
that handles two million files with no service catalog, or 10,000 services over
a tiny fixture, has not met the product target. Epic 42 proves that one shared
repository generation can support both without multiplying source work by
service count and makes the next topology decision from prospectively frozen
evidence.

### Boundary

- The gate uses only deterministic neutral/public inputs and source-free
  receipts. A private target replay remains separately authorized and cannot be
  smuggled into these tickets.
- The minimum combined pass shape is at least 2,000,000 regular-file physical
  owners and 10,000 accepted services, with explicit shared/generated/typed/unowned
  placements and bounded RPC/Kafka relationships. Logical memberships may not
  multiply physical indexing, Git reads, observation parses, or stored source
  bytes.
- Exact correctness, authorization, publication, recovery, lifecycle, and
  nonclaim gates outrank elapsed time. The plan freezes stop rules and host/tool
  identity before measurement; a failure selects a named follow-up rather than
  an in-run threshold change.
- This gate freezes source transitions and proves eventual bounded convergence;
  it does not generate or claim sustained commit-velocity, queue catch-up, or
  freshness-under-cadence evidence. That operating gate remains owned by a
  separately authorized successor to the stopped T39.2 operating gate.

**T42.1 · Combined gate freeze and deterministic corpus** — construct the
minimal deterministic corpus/profile adapter that composes the exact T40 and
T41 authorities without materializing service-count × file-count bytes. Freeze
revision history, ownership/membership oracle, query and relationship oracle,
small-delta/A→B→A transitions, failure injection points, host/tool identities,
resource ceilings, stop/teardown rules, and receipt schema before execution.
Neutral chain, layered-DAG, bounded-fan-out, and hotspot families use fixed
seeds and closed expected edges. External runtime traces may inform aggregate
shape only after license review; their rows and opaque identities are not
vendored and cannot become source, service, placement, relationship, or result
authority.
AC: independent oracle generation does not consume phebs results; two builds
are byte-identical; physical owners, unique content, catalog membership and
unowned prefixes, inherited placement-claim duplications, logical memberships,
accepted fan-out, and every byte kind are separately reported; the exact main
commit and upstream receipts are digest-bound; no production behavior, cap, or
release state changes; independent plan review and full merge bar.

**T42.2 · Combined convergence, recovery, and pressure execution** — run the
frozen corpus through ordinary production workers and retain a closed receipt.
AC: one physical source/search/observation generation with at least 2,000,000
regular-file owners serves all 10,000 services; applicable extraction and
relationship roots become current; exact All code/service/relationship queries
match the oracle; cold, warm, small
physical delta, small logical delta, A→B→A, partial service failure,
interrupted publication, stale lease, process restart, backup/restore, reader
lease, lifecycle collection, and 80/90/75 pressure cases settle under bounded
work; measurements enumerate per-phase wall/RSS, children, physical/logical/
allocated bytes, store rows/transactions, member reads, cache behavior, and
reuse; any stop remains a stopped receipt with later phases `not_run`; no SLO
or release inference; full recovery and merge bars.

**T42.3 · Scale posture decision and neutral product closure** — independently
review the T42.2 receipt, replay representative All code → service →
relationship → Workbench → proof → MCP flows, and record one decision:
single-node envelope retained, bounded cohort experiment required, P6
investigation required, or program stopped. AC: UI/API/MCP authority receipts
remain exact across pagination and concurrent publication; operations status
shows source-free progress/refusal/pressure; every unsupported/partial/stale
state remains visible; topology selection follows the frozen rule and cannot
raise an admission bound; docs distinguish mechanics, neutral scale evidence,
target operating evidence, validation, and release. Epic 42 closes with a
source-free digest-pinned demo receipt, but `GATE2-V2` and `DO_NOT_RELEASE`
remain unchanged absent a separate sealed validation and release program. The
closure cannot be cited as commit-cadence, catch-up, or freshness evidence.

## Epic 43 · Charter-governed product experience *(completed 2026-08-08 → [BACKLOG_COMPLETED.md](./BACKLOG_COMPLETED.md))*

Twelve charter-gated presentation tickets, T43.1–T43.12, closed by the
T43.12 motion pass and charter closure. The closure record
(`spike/t431/CLOSURE.md`) binds all 38 T43.1 ledger findings to their
resolutions — zero blockers open — and re-scores the T43.1 instrument at
37/40 against the 23/40 baseline. Receipts: 118 baselines (29 routed
surfaces × 2 themes × 2 densities + the themed sign-in pair), cardinality
derived from the manifest by test. Post-closure residue, PR-sized and
unscheduled:

**T43R.1 · Type-floor adjudication (F14)** — adjudicate charter §3's 11px
floor against the shipped 9–10.5px evidence-metadata dialect (138 sites):
enforce the floor as written, or amend the charter with a named metadata
exception; then enforce the ruling through the kit.

**T43R.2 · Commit diff density (F34)** — per-file grouping and
`contentVisibility` for the Commit page's flat div-per-line diff.

**T43R.3 · House confirm dialog (S1)** — replace the Workbench's native
`window.confirm` guard with the kit's dialog semantics.

**T43R.4 · Bounded errors everywhere (S2)** — History and Analytics render
raw `String(cause)`; route them through the bounded-error form the catalog
surfaces use.

**T43R.5 · SectionHelp deployment (S3)** — extend the glossary help to the
catalog (directory, explorer) and git (file, history, blame, commit)
surfaces; it already reaches six surfaces through the analysis scope panel.

## Epic 25 · Embedded documentation browser *(drafted 2026-07-27 · unscheduled nice-to-have)*

Serve the repository's markdown documentation, rendered, from the phebs binary
itself. The tracked `docs/` tree stays the single source of truth: plain
markdown, still rendered identically by GitHub, with no external docs
toolchain, static-site generator, or content fork.

### Boundary

- One new pure-Go dependency (`goldmark` plus its GFM extension); no new
  runtime children and no build-pipeline stage.
- Served behind the existing session/API-key authentication like the UI; no
  anonymous surface and no new capability.
- `docs/fixtures/` and `docs/design_handoff_phebs_brand_and_ui/` are excluded
  from the embedded set; retained records remain repository-only.
- No docs versioning, search, or navigation chrome: `docs/README.md` and
  `docs/MANUAL.md` are the navigation, exactly as on GitHub.

**T25.1 · Rendered docs at their markdown URLs** — embed the tracked docs
markdown, docs SVGs, and `config.example.yaml`, plus root `README.md` and
`PLAN.md`, via `go:embed` (same build-tag pattern as `ui/`); render GFM to
HTML with goldmark once at startup; serve each page inside one branded HTML
shell at its repo-relative path under an authenticated docs route, so every
tracked relative link works unrewritten (`.md` URLs return HTML, SVG and YAML
pass through). AC: every local link the T24.1 contract test validates also
resolves in the served site; excluded fixture and design-handoff bytes are
absent from the binary; the route requires authentication; `make dev` demo —
open the served `docs/README.md`, follow a link into a task guide and the
architecture SVG; dated PLAN ADR bullet in the same PR; full merge bar.

## Epic 26 · SQL schema-set evidence *(drafted 2026-07-27 · unscheduled — spike first)*

Outline relational models from committed repository bytes: declaration-first,
usage-second, joined only through committed binding artifacts. Round one
scopes PostgreSQL and MySQL current-schema snapshots, including a requested
schema-only dump, plus schemas and authored queries bound by a committed sqlc
manifest. A dump alone is sufficient for a citable catalog when its committed
header establishes the dialect; the manifest is the stronger artifact that
deterministically ties an engine, schema files, and authored query files
together without inference.

### Boundary

- Pure reader over committed blobs at the indexed commit. No database
  connection, no `information_schema` introspection, no migration execution,
  no runtime naming resolution.
- PostgreSQL and MySQL are separate dialect lanes. A sqlc `engine` value or a
  recognized dump-generator header establishes the dialect; grammar sniffing
  never does. Headerless standalone schema files require a committed binding
  manifest. No assertion or measurement merges identities across dialects.
- Three evidence planes, no more:
  - `sql-schema` — current-schema declarations from a committed schema-only
    dump or from inputs explicitly listed by a committed sqlc manifest; the
    only source of `DECLARES_TABLE` / `DECLARES_COLUMN` / key, index, and
    FK-edge facts. Evidence records whether the source is an authored schema
    input or a captured dump; either proves only what the committed bytes say,
    never the live database. A dump without a query-binding manifest produces
    the catalog but no usage join.
  - `sql-query` — authored `.sql` query definitions producing read/write
    relation references. Generated `.sql.go` is never a primary usage source;
    it duplicates the authored query and joins later via generated-from
    evidence.
  - `sql-migration-event` — per-statement migration history
    (`MIGRATION_CREATES_TABLE`, `MIGRATION_ADDS_COLUMN`,
    `MIGRATION_DROPS_TABLE`, …) with **no current-shape claim**. No fold in
    round one: a folded "current schema" has no single citable blob under the
    extractor's streaming contract, and repeated alterations of one column
    need occurrence-distinct identities because assertion identity excludes
    detail.
- Shared join lineage `sql_schema_set_v1(repo, manifest-or-migration-root,
  dialect)`. An unqualified table literal stays a named-table reference until
  exactly one declaration set selects it; multiple candidate roots refuse as
  `ambiguous-schema-set`, never a repo-wide guess.
- Identifier canonicalization is dialect-specific. PostgreSQL quoted and
  unquoted identifiers retain their distinct rules. MySQL source spelling and
  quoting are preserved; absent committed server configuration, phebs never
  assumes a `lower_case_table_names` value or joins case variants.
- A repository with no admitted schema artifact reports
  `schema-artifact-missing` and may produce a checklist action to request a
  schema-only dump. The request and any human-supplied capture metadata are
  workflow state, not schema evidence; facts begin only when immutable dump
  bytes are available to the reader.
- Column references get their own eventual surface
  (`find_sql_column_references` over `(schema lineage, relation, column)`);
  the existing numeric wire-field surface stays byte-stable and untouched.
- Out of scope, stated as decisions: Redis (Epic 28 scopes deterministic
  declaration islands and provable key usage; a universal keyspace model
  stays out); raw document-store dialects (Epic 27
  instead proposes one employer-neutral normalized manifest); Cassandra/CQL
  and explicit-only ORM packs (separate later spikes with their own decision
  tables); any ER visualization (rendering is commodity once facts exist);
  the Workbench SQL resource plane (stays `unsupported` until a proven
  operation → query → table join exists).
- Same posture as every pack: experimental-dark, provisional lineage, honest
  abstention, no accuracy/completeness/runtime claim; production registration
  sits behind the documented validation and pilot-continuation gate.

### Safety boundary

- Spike artifacts live under `spike/t261/` as retained records; production
  packages must not import spike packages.
- A requested dump enters the pure-reader boundary only after it is committed
  to an indexed repository, including a dedicated evidence repository if the
  application source repository cannot own it. Ad hoc uploads and mutable
  filesystem paths are outside round one.
- Dump admission is schema-only and fails closed on data-bearing `COPY`,
  `INSERT`, `REPLACE`, or `LOAD DATA` sections. PostgreSQL dump wrappers and
  MySQL versioned comments, delimiters, engine/charset/collation clauses,
  prefix indexes, and generated-column syntax are parsed or reported by
  frozen abstention classes, never silently discarded. Repository placement
  and an optional human-asserted capture time do not establish database,
  cluster, environment, or runtime identity.
- Public corpus only; no employer names, code, schemas, or infrastructure.

**T26.1 · Pinned-corpus SQL evidence spike** — pin the public Hatchet
repository at `559b5021e418f12ded175f950b709b7fa66be5a5` (214 SQL migrations,
5 sqlc schema inputs, 36 authored query files, matching generated Go; FKs,
composite keys, CTEs, PL/pgSQL, triggers, partitions, and dynamic `EXECUTE`
for credible positives *and* honest abstentions). Pin the public
`ntppool/monitor` repository at
`e03c40a06ae8f9bd4906001c2ede0c7296ec8e96` for the MySQL lane: its committed
MySQL-compatible dump, sqlc `engine: mysql` manifest, authored query file, and
generated Go exercise FKs, composite keys, views, backticks, versioned
comments, engine/charset/collation clauses, joins, index hints, and
`ON DUPLICATE KEY UPDATE`. Add one version-pinned PostgreSQL-generated
schema-only dump of a committed public fixture, retaining the exact generator
command, version, input digest, and output digest; together the real MySQL
dump and generated PostgreSQL dump validate the requested-dump path without
executing corpus SQL in production. Hand-label both dumps and all manifest
schema inputs completely, plus a preregistered stratified random sample of
migration and query files; freeze the sampling rule and all four measurement
denominators in the spike README **before** the first parser run. Evaluate at
least one imported parser candidate and one bounded subset grammar for each
dialect against identical gates (parser bounds, panic safety on the full
corpus, binary-size and build-time impact, byte-exact source positions,
deterministic double-run output). Freeze a decision table covering:
authored-schema versus captured-dump source classification; dialect admission
from committed metadata; schema-only admission and data-section refusal;
missing artifact/request-dump state; PostgreSQL and MySQL identifier
canonicalization (never generic lowercasing or an assumed
`lower_case_table_names` value); schema-set lineage and ambiguous-root
refusal; the exact `DECLARES_*` versus `MIGRATION_*` predicate split; FK
identity including composite-column order and multiple FKs between the same
table pair; common read/write semantics for `SELECT`, `INSERT … SELECT`,
`UPDATE`, and CTEs plus PostgreSQL `DELETE … USING` and MySQL multi-table
delete / `ON DUPLICATE KEY UPDATE`; generated-source deduplication;
occurrence-distinct migration-event identity; single-pass versus bounded
two-pass reads under the SDK's one-blob citation contract; and a frozen
per-dialect unresolved vocabulary (unsupported statement, dynamic identifier,
ambiguous schema set, unknown declaration, procedural SQL, generated
duplicate, parser gap). AC: locked corpus pins, generated-dump receipt, labels,
and measurements committed under `spike/t261/`; the decision table answers
every frozen question or records an explicit deferral; the measurement report
states four separate denominators for each dialect — declaration objects
parsed vs. hand labels, migration statements recognized vs. gaps by syntax
class, query table sites recognized vs. unresolved, and uniquely bound joins
vs. name-only/missing/ambiguous — with no cross-dialect blended percentage;
double-run bytes identical in both lanes; no production code paths changed
and no pack registered.

## Epic 27 · Document schema-manifest evidence *(drafted 2026-07-27 · unscheduled — spike first)*

Make schema-on-write document-store structure useful during everyday ticket
work without teaching phebs a private vendor or employer dialect. Round one
admits one project-owned, strict JSON interchange artifact,
`phebs-document-schema-v1`, produced by a repository author or an out-of-tree
adapter and committed at the indexed revision. It supplies an Atlas-shaped
catalog — schema set → table → nested field — plus ordered key, index,
association, and materialized-view declarations with exact citations.

The first workflow is deliberately declaration-only: locate the table a ticket
mentions, inspect its nested fields and exact type spellings, verify primary
and partition-key membership and order, follow explicitly declared
associations/views, and cite the committed snapshot. If the artifact is absent,
phebs reports a request-schema-export checklist state. It does not infer a
model from client code.

### Boundary

- Pure reader over one committed manifest blob at the indexed commit. No
  service connection, administrative API, runtime introspection, schema
  registry call, generator execution, or mutable upload.
- The manifest has a closed, versioned vocabulary for tables, flattened nested
  field paths, exact source type spellings, ordered primary and partition
  keys, ordered indexes, associations, and materialized views. A field path
  is a JSON array of typed segment objects — a field segment carrying one
  exact name, or a traversal segment from the closed set {array element, map
  value} — never a delimiter-split or escaped string, so every exact field
  name, including `[]`, remains representable. Primary-key, view-key, and
  index entries may carry an explicit `asc`/`desc` direction (default
  ascending); partition keys are path-only and carry no direction. An index
  column may carry an optional positive prefix length whose unit is defined
  by the declaring store — phebs cites it and never interprets or converts
  it. An index names an opaque, citable `kind` spelling and is owned by
  either a table or a view. A view declares its own output namespace:
  ordered output fields with exact names, each carrying zero or more source
  `(table, field-path)` references; view keys and view-owned indexes
  reference output paths only, and duplicate output names fail closed. Type
  and kind spellings are citable opaque declarations in round one; phebs
  does not invent cross-store equivalence.
- Lineage is
  `document_schema_set_v1(repo, manifest_path, format_version)`. Exact table
  spelling and exact field-path segments remain part of identity. Display
  labels, capture timestamps, source revision strings, repository placement,
  and adapter names never establish service, cluster, database, environment,
  ownership, deployment, or runtime identity.
- Every table declares a non-empty ordered primary key. An optional partition
  key must be a non-empty ordered path-prefix of that primary key, with
  direction excluded from the prefix comparison; an absent
  partition key means undeclared, never an assumed default. Associations are
  optional — some stores declare none — and the association graph may
  contain cycles, including self-associations: associations are declared
  edges phebs never traverses. View source references must form an acyclic
  dependency graph; a view-dependency cycle fails closed. Every referenced
  field path must resolve exactly within the same manifest; duplicate tables,
  fields, key entries, indexes, associations, or view identities fail closed.
- Associations are explicit logical edges, not foreign keys and not proof of
  referential integrity. A materialized view is a declared derived resource,
  not proof that it is deployed, fresh, populated, or read by code.
- An `authored` manifest records repository intent. A `captured` manifest
  records only the structure present in the supplied export. Both are
  committed declarations; neither is evidence of the live store. The source
  class is mandatory and the two classes are never silently merged.
- A repository with no admitted manifest reports
  `document-schema-artifact-missing` and may produce a checklist action to
  request a schema-only export in this neutral format. The request, adapter
  instructions, and human capture metadata are workflow state rather than
  schema evidence.
- The eventual catalog gets its own table and field identity surfaces; the
  numeric protocol field reader and Contract Atlas service identities remain
  unchanged. The Workbench document-store resource plane stays `unsupported`
  until a separate public contract can prove operation → usage → table
  relationships. Declaration-only catalog rows cannot populate that
  relationship registry.
- Raw proprietary schema files, private client APIs, private adapters,
  inferred client-code usage, query semantics, live-schema comparison, and
  cross-manifest current-shape folding are outside round one. An organization
  may maintain an adapter elsewhere, but only its normalized committed output
  crosses the phebs evidence boundary.
- Same posture as every pack: experimental-dark, provisional lineage, honest
  abstention, no accuracy/completeness/runtime claim; production registration
  sits behind the documented validation and pilot-continuation gate.

### Safety boundary

- Spike artifacts live under `spike/t271/` as retained records; production
  packages must not import spike packages.
- Strict decoding rejects unknown and duplicate object keys, invalid UTF-8,
  control text, invalid or unnormalized paths, unresolved references, cycles
  where the contract forbids them, unsupported format versions, trailing
  bytes, and any record/sample/default/example/value payload. The manifest is
  structural metadata only.
- Freeze limits before implementation for manifest bytes, tables, fields per
  table, path depth and segment bytes, type bytes, key width, indexes,
  associations, views, aggregate declarations, and emitted citations.
  Limit failures are typed abstentions; no partial catalog is emitted. phebs
  validates only these frozen bounds — store-vendor and deployment limits are
  never encoded or enforced.
- A captured export enters the reader only after immutable placement in an
  indexed repository, including a dedicated evidence repository if the
  application source repository cannot own it. No employer artifacts, names,
  code, credentials, hosts, or infrastructure enter phebs fixtures or retained
  records.

**T27.1 · Neutral document-schema contract spike** — freeze the
`phebs-document-schema-v1` JSON Schema, a neutral synthetic fixture family,
and one independently authored public-reference derivation before
implementing the reader. The positive synthetic fixtures cover nested
objects/lists/maps traversed through typed array-element and map-value
segments, a literal field exactly named `[]`, opaque scalar type spellings,
composite primary keys including a descending key column, path-only
partition-key prefixes, an undeclared partition key, multiple ordered
indexes including a store-defined prefix-length column, distinct opaque
index kinds, and a view-owned index over output paths, two associations
between the same table pair, a self-association, a table with no
associations, a materialized view with multi-source output fields, a
zero-source declared output field, and its own ordered keys over output
names, authored and captured source classes, and exact non-ASCII names. The
negative synthetic fixtures cover every strict-decoder, reference-integrity,
view-dependency-cycle, key-order, malformed-segment-object, duplicate
view-output-name, direction-on-partition-key, invalid direction or
prefix-length, data-payload, and size/depth refusal.

Pin the Apache-2.0 public `jeffreyscarpenter/cassandra-guide` repository at
commit `bed58e7e51768f9f30dd435e74f31e5b3b2649a4` and hand-derive one captured
neutral manifest from `resources/reservation.cql` (Git blob
`2c1c098ff706f63f28be772514e7b7a664878774`, 2,218 bytes, SHA-256
`38992bd1f0882ebefe7e16570d05886ade150bf2036ea6119ab08fc11b3bb39e`).
The source independently exercises a nested user-defined type, set/list/map
collections, a map value containing the nested type, scalar and composite
partition/clustering keys, and a materialized view. Retain the source pin,
license and digest receipt, plus a complete declaration-by-declaration
CQL-source → neutral-manifest derivation table with exact source spans and
explicit decisions for collection traversal, path-only partition prefixes,
default key direction, and view output fields. Freeze that derivation before
the reader's first run. The CQL file is an external oracle only: phebs neither
parses nor admits CQL in Epic 27, and this fixture makes no Cassandra-pack
claim. Synthetic fixtures remain authoritative for neutral-contract features
the public source does not contain.

Hand-label every synthetic declaration and refusal and every declaration in
the derived public manifest. Freeze a decision table covering
schema-set/table/field identity; source-class semantics; exact-name handling;
typed field-path segment representation; type, index-kind, and
prefix-length-unit opacity; key, index, association, and view semantics —
including per-column direction, path-only partition prefixes, view-owned
indexes, view output namespaces and their source references,
association-cycle legality versus view-dependency acyclicity, and
undeclared-partition-key meaning; source positions for every emitted object
and edge; missing-artifact/request-export state; single-snapshot versus
explicit two-snapshot comparison; canonical ordering; typed unresolved
vocabulary; and all parser/output bounds — phebs's own, with no vendor-limit
validation. Run a bounded reader twice over every fixture and compare
byte-identical facts, citations, catalog projection, refusal census, and
snapshot comparison. AC: schema, synthetic and public-derived fixtures,
source/digest/license receipt, complete derivation table, labels, decision
table, bounds, and measurements committed under `spike/t271/`; byte-exact
UTF-8 source spans round-trip to the cited JSON; the derived manifest is fully
accounted for against the pinned CQL without a parser; malformed, cyclic,
data-bearing, and oversized inputs fail without partial facts or panics; the
comparison distinguishes added/removed/changed declarations without calling
either snapshot live or current; the retained README demonstrates the
locate-table → inspect-key/field → copy-citation ticket workflow and the
missing-artifact checklist; no private dialect, adapter, CQL parser,
production code path, pack registration, or runtime claim is added.

## Epic 28 · Redis keyspace evidence *(drafted 2026-07-27 · unscheduled — spike first)*

A general Redis keyspace model is out of scope; deterministic Redis
declaration islands and provable key usage are in scope. Redis repositories
divide by the artifacts they commit, so round one is two spike lanes —
native declaration islands and key-specification-driven usage binding — and
the neutral keyspace manifest is explicitly deferred behind the spike's gap
measurement: it is drafted only if native declarations leave the expected
gap, entering as the same request-artifact workflow as the SQL dump and the
document-schema manifest.

### Boundary

- Pure reader over committed blobs at the indexed commit. No server
  connection, no `TYPE`/`SCAN` probing, no RDB/AOF inputs, no runtime state,
  and no claim that any cluster, database, or key exists.
- Lane one, native declaration islands: committed ACL key-pattern files and
  recognizable literal command vectors that declare indexes (`FT.CREATE`),
  time series (`TS.CREATE`, `TS.CREATERULE`), and stream groups
  (`XGROUP CREATE`). Core-command semantics come from the pinned command
  metadata; the named module subset uses bounded phebs-authored recognizers
  derived from public command documentation, not copied module source or
  metadata. An index schema is only the indexed projection over declared key
  prefixes, never a complete value model; a series or group declaration names
  a resource, not its entry fields. Most such declarations execute from
  application startup code, so this lane shares the command-vector machinery
  and its recognizer limits — the spike's headline number is what fraction of
  these declarations exist as recognizable committed literals at all.
- Redis object-mapping model classes are the strongest committed declaration
  artifact but sit across a language frontier; round one measures feasibility
  only and commits to no model-class reader.
- Lane two, usage binding over the Redis 7.2.15 command key specifications
  pinned below. Only provable command vectors bind: literal raw commands or
  arrays; one `github.com/redis/go-redis/v9` major-version adapter whose
  method-to-command table is validated against each pinned corpus's exact
  dependency version; literal or same-file-constant key arguments; and
  bounded format expressions whose segment structure is statically known.
  Other client majors and unproved wrapper mappings abstain.
- Explicit `FCALL`/`EVAL` key lists can emit
  `PASSES_REDIS_KEY_TO_SCRIPT`: they prove only which keys the caller supplies
  under the command contract. They never inherit the script's read, write, or
  expected-value-kind semantics, and round one does not analyze function or
  script bodies.
- Access modes adopt the key-specification vocabulary verbatim — `RO`, `RW`,
  `OW`, `RM` with access/update/insert/delete detail — never flattened to
  read/write. `variable_flags` are resolved only when the admitted argument
  vector proves the applicable branch; otherwise access mode abstains as
  `variable-access-mode`. An incomplete specification may support a fact only
  when the exact key position and applicable flags are fixed for that admitted
  vector; every omitted or unknown position remains an
  `incomplete-key-specification` gap.
- Expected value kind is not present in the key specifications. It comes from
  a separate, frozen command-family table hand-labeled from public command
  documentation. A table entry may assert only what the code expects — for
  example, a hash command expects a hash — never runtime existence or type.
  Missing or argument-dependent entries yield
  `unknown-expected-value-kind` without discarding separately provable
  key/access evidence.
- ACL key patterns are a separate authorization-intent evidence class; they
  never declare key existence or value structure and are never merged with
  declaration or usage rows. An ACL fact may identify the exact username token
  as its authorization principal, but it proves neither authentication nor
  deployment. Only username-token and `%R~`/`%W~`/`%RW~`/`~` pattern-token
  spans may be emitted or cited. Cleartext-password tokens, password-hash
  tokens, command grants, and line-context snippets are neither retained nor
  rendered; diagnostics name only structural refusal classes.
- Frozen unresolved vocabulary: `dynamic-concatenation`, `opaque-helper`,
  `runtime-namespace`, `unknown-client-adapter`,
  `unsupported-core-command-after-pin`, `unsupported-module-command`,
  `incomplete-key-specification`, `variable-access-mode`,
  `unknown-expected-value-kind`, and `ambiguous-key-family`.
- The deferred `phebs-redis-keyspace-v1` manifest, if the gap measurement
  justifies it, declares key families as ordered structured binary-safe
  segments (literal, parameter, hash-tag grouping — typed segments, never
  format strings), with intended value kind, optional field intent, expiry
  intent, associated index/group/series resources, and authored/captured
  source class. Hash-tag validation uses the exact documented byte semantics
  without claiming a cluster exists. A concrete key or statically known
  format binds only when exactly one declared family matches; overlaps
  refuse as `ambiguous-key-family`, never precedence guessing.
- Out of scope as decisions: universal keyspace modeling; runtime
  introspection; RDB/AOF or any data-bearing input; the Workbench Redis
  resource plane (stays `unsupported`).
- Same posture as every pack: experimental-dark, provisional lineage, honest
  abstention, no accuracy/completeness/runtime claim; production
  registration sits behind the documented validation and pilot-continuation
  gate.

### Safety boundary

- Spike artifacts live under `spike/t281/` as retained records; production
  packages must not import spike packages.
- The only admitted upstream command-metadata bytes are Redis 7.2.15 commit
  `316753259b4db132cf494292a1b3a702d9e9ddb2`: BSD-3-Clause `COPYING` blob
  `a381681a1c2524ed586c6a87dfeb9ccdf1e86ded` and the 392 JSON files under
  `src/commands/` tree `59da020b9c7d8847fa0f7012b1fa2b3a09f47297`.
  T28.1 records per-file SHA-256 digests and a license receipt before use.
  Core commands added after that pin are unsupported.
- The named declaration subset (`FT.CREATE`, `TS.CREATE`,
  `TS.CREATERULE`) uses bounded phebs-authored recognizers derived from public
  command documentation. No Redis module implementation or metadata bytes
  enter the repository; every other module command is
  `unsupported-module-command`. Phebs parses committed source artifacts and
  neither links, embeds, starts, nor connects to Redis.
- ACL readers are token-streaming and output-safe by construction. Retained
  fixtures and test snapshots must prove that credentials adjacent to a
  pattern cannot enter facts, citations, diagnostics, logs, or rendered
  context.
- Public corpus only; no employer names, code, schemas, credentials, hosts, or
  infrastructure.

**T28.1 · Two-lane Redis evidence spike** — preregister these immutable public
inputs and their exact file scopes before the first recognizer run:
MIT-licensed `hibiken/asynq` commit
`d135f1439bee74e989b7f9b41ecd542cc87f024a` with
`github.com/redis/go-redis/v9` v9.14.1 for the broad usage census;
Apache-2.0 `cloudwego/eino-examples` commit
`6dc0d214c0eb392babf2d001e9be85f57ac10952` with go-redis v9.17.2 for
literal/constant raw-command and `FT.CREATE` declaration shapes; and the
Redis 7.2.15 pin above for command metadata plus the public ACL safety
fixtures. Record the hand-label sampling rule, source/license receipts, and
all denominators before execution. Measure separately, with no blended
percentage: declaration coverage (index/series/group sites with exactly
bindable identities versus dynamic construction — the manifest go/no-go
number); ACL username/pattern pairs parsed and credential-token non-retention;
recognized command sites versus unresolved by frozen shape class; resolved
key arguments; resolved access modes; expected-kind coverage; script-key
pass-through rows; and uniquely bound key spellings or families. Freeze a
decision table covering command-vector recognition and declaration
partial-fact atomicity; the go-redis method-to-command rule at both exact
versions with no cross-major claim; format-expression bounds;
incomplete-specification and `variable_flags` handling; exact access
vocabulary; the separately sourced expected-kind table;
`PASSES_REDIS_KEY_TO_SCRIPT`; ACL token-only citations; hash-tag byte
semantics; the clean-room named-module boundary; the object-mapping
feasibility verdict; and the explicit manifest go/no-go criterion. AC: pins,
receipts, labels, decision table, and measurements committed under
`spike/t281/`; double-run facts, citations, censuses, and retained outputs are
byte-identical; every refusal lands in the frozen vocabulary; an output scan
proves ACL credential tokens absent; no production code path changed and no
pack registered.

## Deliberate non-goals *(per historical PORT_MAP §7/§12)*

SCIM provisioning, multi-org RBAC / seats, and a cloned "Ask" chat app —
phebs stays **MCP-first** (agents bring their own chat) and **single-tenant**.
Kubernetes/Helm waits for the P6 fleet profile. Anonymous-access and
entitlement gating are deleted outright (config bool, no license backend).

---

## Standing rules

- Decisions land as dated ADR bullets in PLAN.md, same PR as the change.
- Every epic ends with a `make dev` demo state — no epic is "done" if it
  can't be shown end-to-end.
- Upstream repo is behavior reference only; `ee/` paths never opened.
- Personal hardware, personal time, no employer code or credentials.
