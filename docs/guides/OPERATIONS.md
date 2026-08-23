# Operations and development

[← User guide](../MANUAL.md)

This guide owns data layout, backup and restore, security, permissions, audit,
jobs, experimental extraction operations, metrics, shutdown, troubleshooting,
and contributor commands.

## Operations



### Data layout

```
$DATA/                     # server.data_dir, default ~/.phebs
├── db/                    # SurrealDB — users, API keys, sessions, repo/jobs
├── .surreal-runtime.json  # private, process-owned live-backup rendezvous
├── repos/<host>/<path>.git  # bare mirrors
├── candidates/            # derived candidate manifests and NDJSON members
├── observation-plans/     # restartable derived Go partition planning state
├── observations/          # immutable current/historical Go observations
├── resolver-catalogs/     # derived immutable resolver catalog publications
├── caller-leaves/         # derived caller pair artifacts and complete manifests
└── index/                 # whole/focused zoekt shards; focused manifests and sidecars
```

Mirrors, candidate publications, shards, repo rows, and jobs are rebuildable
from config and upstream Git. **Authentication state is not derived:**
`$DATA/db` now contains users,
OIDC links, API-key hashes, and sessions (see *Backup & restore*). Deleting
the whole data directory is an intentional auth reset as well as a reindex;
the next start requires first-user enrollment.

### Backup & restore

Precious state is `$DATA/db` plus the exact config file — the users, OIDC
links, API-key hashes, sessions, permission edges, audit/analytics history,
evidence, extraction outcomes, and proof pins that cannot be rebuilt (repo
rows and job state ride along but are derivable). Mirrors and
whole-repository shards are derived.
Focused shards are also derived semantically, but online backup preserves
a validated marker-free physical publication byte-exactly without claiming
that filesystem discovery proved the live store pointer; builder
timestamps/identity make rebuild output an unsuitable restore-equality test,
and restore re-fences authority.
Candidate manifests and their partition members are derived planning state:
they are excluded and rebuilt from the restored indexed commit and mirror.
Resolver catalogs are also derived, but valid immutable publications are
preserved byte-exactly so a matching current generation can be validated and
reused without repeating its declaration/candidate input work.
Direct caller-leaf pair artifacts remain derived and are not independently
visible. Backup preserves only an exact, marker-free complete-generation
manifest and the precise immutable leaf artifacts it references; incomplete,
ambiguous, invalid, marker-covered, or unreferenced caller state is omitted for
reconstruction from candidate and resolver authority.
Go source observations are likewise derived. Backup preserves every fully
validated current immutable v1 observation generation plus each joint v2
current/rollback source-and-observation generation byte-exactly, but excludes
restartable `observation-plans/`, incomplete stages, and unrooted generations.
Restore strict-opens every staged v1/v2 root before installing it and grants no
authority to an invalid or omitted generation.

The selected historical-publication posture is unbounded in the live
installation, but backup is not a live retention pin and does not promise the
same history for every owner. The database export retains all evidence runs,
shared atoms, associations, assertions, exact-scope attempts, latest domain
outcomes, durable evidence pins, immutable proof bundles, all eight job tables,
and all 24 Investigation/Workbench tables. Restore retains those rows; it
selectively clears candidate-control outcomes with candidate authority. The
export also contains caller rows, but restore intentionally clears caller
pointers, admissions, and outcomes. Filesystem-only archive
discovery may include a validated, unambiguous, marker-free complete caller
publication without proving that the store currently points to it;
historical coverage is not promised, incomplete-generation residue is omitted,
and restore grants no authority to those bytes. Taking a backup therefore
neither pins live state nor turns omitted caller residue into recoverable
history.

For an online backup, keep the local phebs server running and use the same
phebs executable/configuration generation as that server:

```sh
phebs backup -config /etc/phebs/phebs.yaml -output /restricted/phebs-backup-20260722
```

The output path must not exist. The command discovers only the supervised
loopback SurrealDB child through `$DATA/.surreal-runtime.json`, verifies the
exact child executable and the raw-config digest that started the live server,
acquires a crash-released cross-process snapshot lock that lets in-flight
focused-index publication/reconciliation commits finish and pauses new ones,
and runs that executable's live `export`. Candidate, resolver-catalog, and
caller-generation transitions are not covered by that focused-index lock;
their derived archives independently admit only exact marker-free immutable
publications, and restore/startup re-fences them against imported store
authority. Service-catalog generations differ: their canonical JSON, exact
source-census binding, and current revision are precious SurrealDB authority,
so database export/import retains them byte-for-byte and restore does not
clear them with derived candidate/resolver/caller pointers. Independent service
desired/active rows, incarnations, tombstones, row revisions, and their bounded
repository summary are precious state too and restore exactly. Startup
strict-opens and reconciles that authority against the restored repository
state. A catalog pointer newer than its service summary is a fail-closed
interrupted transition; the exact retry repairs it without a Git census or
historical-generation scan. A different config that only points at the same
`$DATA` is refused. The command publishes a private directory
containing `database.surql`, `focused-index.tar`, `resolver-catalog.tar`,
`caller-publication.tar`, `observation-publication.tar`,
`relationship-publication.tar`, and `manifest.json`. The
focused archive contains only complete, revalidated focused manifests,
sidecars, and shard members; it never includes whole-repository shards. A
stale marker is omitted but cannot hide an otherwise complete valid
publication. Invalid, incomplete, or orphan focused artifacts are rebuildable
damage, not a reason to discard the already completed precious-state database
export: backup omits them. Creation copies the already validated focused files
from stable descriptors, rehashes each copy against its frozen digest, and
proves the exact canonical tar inventory in one bounded streaming pass; it
does not extract the archive into a second temporary tree. Restore's
pre-import verification and final installation still perform the complete
structural extraction and semantic publication validation. The
`phebs-backup-manifest-v8` manifest
binds all six artifacts' sizes and SHA-256 digests, the exact raw config digest,
phebs version/binary digest, SurrealDB version/binary digest, database
identity, store-writer/evidence/migration versions, and the derived-state
exclusions, including `$DATA/candidates` and invalid or incomplete caller
publication state. Its required
`phebs-focused-archive-report-v1` receipt records archived publications,
omitted publications/artifacts, and stale markers; verification independently
recovers and compares the archived publication count. It contains no host
binary path or database password. Preserve the exact config separately; the
backup contains its digest, not its bytes.

`observation-publication.tar` contains the strict current v1 generation and
the joint v2 current plus rollback-floor generations for each repository.
Joint v2 generations retain both the source super-root tree and its bound
observation inventory tree. Discovery and creation completely validate every
canonical member and distinct observation; the completed tar is then restored
into a private verification tree through the same validator before backup
succeeds. Corrupt, in-flight, or unrooted derived publications are counted and
omitted rather than blocking the precious database export. The archive admits
at most 10,000,000 regular entries and 1 TiB of declared bytes. Restore accepts
only safe regular paths, caps those same dimensions, retains a deterministic
private stage across interruption, reuses only byte-identical completed
entries, rejects every staged path absent from the selected tar, validates
every restored publication, and installs the private tree by one rename. The
top-level restore performs this extraction first through a sibling
`<data>.observation-restore` stage; until the final observation rename the data
directory remains empty, so a subsequent invocation reaches and resumes an
interrupted extraction before database import. The
`phebs-observation-archive-report-v2` manifest section
records total and separate v1/v2 publications, files, logical bytes, omitted
publications/artifacts, and stale markers. Verification independently compares
the archived publication, file, and byte totals. The completed tar is an
external snapshot and never remains a live-generation pin.

`resolver-catalog.tar` contains every and only strict, marker-free immutable
catalog publication. Catalog manifests and canonical NDJSON members retain
their exact bytes. Invalid, incomplete, symlinked, descriptor-swapped,
unreferenced, or marker-covered catalog state is omitted as rebuildable
derived damage. The required
`phebs-resolver-catalog-archive-report-v1` receipt keeps exact publication,
omission, artifact, stale-marker, and truncated-detail counts; at most 64
generic omission details are retained. Backup logs one source-free bounded
summary when any count is nonzero. If the live resolver-catalog root does not
exist, backup writes an empty catalog archive without creating that root.
Catalog restore pins one unchanged regular archive descriptor and completes a
structural preflight before creating its target. It accepts only canonical
regular USTAR/PAX entries; PAX may contain only exact `path` and decimal
`size`, while GNU, sparse, and unknown metadata are rejected. The archive is
bounded to 32,768 entries, 512-byte basenames, 64 MiB per member (1 MiB per
manifest), and 1 TiB for both physical size and aggregate declared logical
bytes; archive creation enforces those same entry, logical-byte, and final
physical-byte ceilings. Sparse metadata therefore cannot amplify a small input
into a large write, and backup cannot emit an archive its own restore rejects.

`caller-publication.tar` contains every and only strict, marker-free,
unambiguous complete caller publication. Each canonical tar name has exactly
two components: the cryptographic repository directory and one complete
manifest or referenced caller-leaf basename. The required
`phebs-caller-publication-archive-report-v1` receipt records exact publication,
omitted-publication, omitted-artifact, stale-marker, and truncated-detail
counts, retaining at most 64 generic details. More than one immutable complete
manifest in a repository is ambiguous exported authority, so backup omits all
of that repository's manifests and leaves instead of choosing one. Offline
verification and restore preflight stream one complete publication at a time,
cold-validating every canonical manifest and referenced leaf with bounded
memory, context cancellation, and no extracted verification tree. Restore pins
one unchanged regular archive descriptor, completes that semantic preflight
before creating its private target, then extracts and cold-validates the staged
set before one rename installs it. Only canonical
regular USTAR/PAX entries are accepted; PAX may contain only exact `path` and
decimal `size`, while symlinks, hard links, devices, sparse/GNU metadata,
unknown metadata, trailing bytes, duplicate paths, and unreferenced entries are
rejected. Creation and restore both enforce 65,536 total entries, 512-byte tar
paths, 64 MiB per leaf, 32 MiB per complete manifest, and 4 TiB for both
physical size and aggregate declared logical bytes. The archive envelope is
deliberately larger than the live 1 TiB leaf-canonical admission ceiling so
valid manifests, tar headers, and padding cannot make a live installation
impossible to back up.
Creation intentionally reads each exported leaf twice: strict discovery hashes
it once for admission, then the descriptor-rooted tar copy streams and hashes
it again while writing. Restore additionally streams and hashes the archive
preflight, cold-validates the private extracted tree before installation, and
revalidates the installed descriptor-rooted tree after the rename; these
repeated reads are integrity fences, not heap copies or scratch-tree copies.

The restored caller bytes deliberately carry no visibility authority. Before
candidate or resolver restore clearing can observe the imported caller edge,
restore raw-clears every imported complete pointer, caller outcome, and
admission and advances that repository's caller-publication revision exactly
once. Valid pointerless bytes for an indexed, non-deleting repository remain
available for startup reconciliation, which force-queues reconstruction;
absent, unindexed, and deleting repositories have their exact validated
pointerless caller residue reclaimed. Exported store state is never promoted
as a current complete caller generation.

Candidate publications remain derived and are deliberately absent from the
archive, including candidate-v4 members and the stable candidate manifest.
Restore clears imported candidate pointers and candidate-control outcomes;
ordinary startup backfill then reconstructs the current v4 generation from
the restored repository, HEAD, unit, and current policies. Never copy a v3 or
v4 `$DATA/candidates` directory beside a restore: those bytes have no authority
without the installation-local reconstructed pointer.

When any omission or stale-marker count is nonzero, backup emits one bounded
focused-derived-state summary:

```text
backup focused derived state: archived=N omitted_publications=N omitted_artifacts=N stale_markers=N
```

Any nonzero omission count means the precious database export is still valid,
but that class of derived focused state was not copied and normal startup will
reconcile its committed claim.

The export is unencrypted credential-bearing state. Move or encrypt the whole
directory only under the approved retention and key-custody procedure. Do not
edit any file: restore rejects extra, missing, renamed, symlinked, special,
oversized, digest-mismatched, malformed, partial, stale, or undeclared focused
entries.

Restore uses the manifest-bound phebs, SurrealDB, and config identities and an
absent or completely empty configured `$DATA`:

```sh
phebs restore -config /etc/phebs/phebs.yaml -backup /restricted/phebs-backup-20260722
phebs serve   -config /etc/phebs/phebs.yaml
```

Recovery config validation deliberately leaves `${SECRET}` references
unexpanded, so verification/import can happen in an isolated environment
without live source or OIDC credentials. Restore verifies the complete backup
and every compatible binary/store identity before it creates `$DATA`. Phebs
performs a structural tar pass on the same open file descriptor. Only canonical
regular USTAR/PAX entries are accepted; PAX may carry only an exact `path` and
exact decimal `size`. Every GNU, sparse, or unknown record is rejected before
the index directory, stage, or output is created. The archive is limited to
100,000 entries, 255-byte basenames, 16 GiB per focused entry, and 64 GiB for
both the physical archive and aggregate declared logical bytes; a small sparse
input cannot expand into a large filesystem write. A newly created archive is
self-verified before backup returns. Restore then imports through an isolated
SurrealDB child, restores focused shard/sidecar bytes before their manifests,
and opens the store once to apply and validate the supported schema/migrations.
That validation open clears every imported candidate-publication pointer
because `$DATA/candidates` is deliberately absent from the backup. Durable
domain outcomes are present in the imported database, but they are ineligible
while the candidate pointer and bytes are absent. Normal candidate rebuilding
must first re-establish the exact pointer; only an outcome whose complete
generation still matches may then short-circuit. A changed rebuild leaves the
old latest row ineligible, so no outcome can authorize missing derived bytes.
Restore validates and installs exact catalog filesystem bytes but always
clears every imported resolver-catalog pointer. Startup never promotes that
exported authority: a pointerless retained publication is classified as an
orphan and a forced `resolver_catalog_job` replacement is durably enqueued.
Restore also clears all imported caller-leaf outcomes and aggregate admissions
without decoding their repository or artifact fields, because caller-leaf
bytes are deliberately absent. Startup backfill or the next resolver
publication recreates only current work; imported rows cannot authorize a
missing artifact.
When at least one shipped gRPC or Thrift caller adapter is enabled, the
resolver worker drains that queue through the bounded materialization path
described below. With neither adapter enabled, no resolver worker or startup
backfill is registered.
Each process startup cold-validates every store-authorized catalog and every
marked crash-recovery candidate whose resolver-pack set matches current
policy. It streams each such catalog's member bytes at most once: at most
512 MiB per repository, with two lifecycle-owned descriptors and no repository
corpus or Git/blob walk. A known resolver-pack mismatch is rejected from the
pointer or manifest before any member is opened. Pointerless, unmarked restored
or orphaned bytes receive bounded manifest validation only; they are never
admitted and are force-requeued. A marked pointerless publication that cannot
be recovered is removed only after that queue write is durable, so it cannot
permanently fence the successor. After a successful cold open, reconciliation
sweeps undeclared files only from that repository's current v1 member
namespace while preserving foreign namespaces. The manifest remains the sole
visibility authority, which permits safe cleanup after a crash between store
commit and retired-file cleanup. Startup performs up to three serial bounded
inventories of the dedicated catalog directory (stage cleanup, discovery, and
one batched repository cleanup), retains the store pointer list, and performs
one bounded authority check per pointer; it holds no mirror lock and starts no
child process. Each top-level inventory is capped at 32,768 entries; abandoned
stages are removed only when they retain the flat lifecycle-produced shape of
at most 257 entries, never by an unbounded recursive walk. Each successful
ordinary publication transition performs one additional serial bounded
`O(D)` inventory of the catalog directory to retire old current-v1 members; it
does no member rehash, repository walk, Git/blob read, or child work. A
process-cached no-op checks only the marker and at most 257 captured
manifest/member path identities; it opens or hashes no member content.

#### Resolver catalog materialization

The enabled extraction registry determines one ordered resolver generation.
The current resolver set contains `go-module` plus the enabled
`grpc-generated-attribution` and `thrift-generated-attribution` packs, all at
version `1.1.0`. A build is eligible only when its repository, indexed HEAD,
committed analysis unit, candidate-manifest-v4 digest/policy/control revision,
and current
published protobuf/Thrift declaration runs and generation digests agree.
Candidate generation/control transitions and every successful protobuf/Thrift
declaration publication atomically ensure one forced pending
`resolver_catalog_job`, including when a prior catalog did not yet consume that
domain. A non-published declaration outcome retires authority only when the
current catalog actually declares that domain; retryable, unavailable, and
terminal outcomes do not rebuild an unrelated or empty catalog. An
exact unchanged candidate retry repairs a missing non-forced successor without
downgrading an existing force; startup also
backfills one deduplicated pending job for every indexed, non-deleting
repository when an adapter is enabled.

Upgrading from resolver packs 1.0.0 to 1.1.0 is a deliberate derived-state
cutover. Startup rejects each old pack set before opening member content,
force-queues its replacement, and clears the retired pointer; every indexed,
non-deleting repository with an enabled caller adapter then rematerializes its
catalog (including mapped generated-Go reads), and the accepted replacement
triggers that repository's first complete caller-leaf execution.

Materialization replays immutable candidate rows and pages the exact published
declaration assertions. It opens candidate-declared regular `go.mod`,
`layout-snapshot.json`, and `generated-from-snapshot.json` blobs, plus each
mapped generated `base`-lane Go source needed by resolver pack 1.1.0. The
layout snapshot is an optional validation fence; unmapped ordinary candidate
source and declaration blobs are not opened. The worker never invokes a build, `go list`,
dependency query, generator, corpus program, mutable checkout, or network
request. Missing or malformed committed content and conflicting mapping or
generator authority are retained as explicit `unavailable`, `ambiguous`, or
`unsupported` records. The catalog intentionally supports only a single-line
`module path` directive with a standard-double-quoted or unquoted token that
passes the pinned Go import-path validator. A legal factored `module (...)`
block and malformed quote, punctuation, comment, or control tokens are recorded
as `unsupported`, never as resolved literal identities such as `"("` or
`'example.invalid/root'`. This strict-token parser surface is committed in
member policy and does not change the shipped extraction compatibility path. A
special, forged, or stale input envelope fails closed.
The worker never chooses a target by tie-breaker.

One repository work lock serializes prior-marker reconciliation, current
authority checks, materialization, and publication. The worker reconciles a
prior-process marker before creating a stage and never overwrites an existing
publisher's marker. Operational filesystem failures while inspecting or
reading a marker, manifest, or member are retryable: the worker and startup
reconciler preserve the pointer, marker, and catalog bytes rather than
misclassifying a transient open/read failure as corruption. A
same-generation/different-manifest conflict is terminal nondeterminism. It
also preserves both the current pointer and the competing marker so the two
immutable receipts remain available for operator diagnosis; startup fails
closed until that conflict is resolved. Do not delete either side while phebs
is running: stop the process, retain the logs and `$DATA/resolver-catalogs`
bytes, and use a witnessed recovery rather than choosing a manifest by hand.

Before an ordinary transition creates its marker, the worker durably ensures
an independent non-forced resolver job owned by the exact active lease. A
store-commit, marker-clear, cleanup, or process failure therefore has a durable
recovery turn. A failed successor response is treated as possibly committed;
the provenance-aware transition safely falls back to the active claim when no
tagged row exists. Retryable live failures merge their attempt, error, and backoff
into that successor; a final live failure or terminal publication conflict may
exhaust it only while the lease provenance remains exclusive. Any ordinary
candidate, declaration, or backfill enqueue clears that ownership and survives
as fresher work. A final stale reap also preserves the independent successor
because process death cannot reveal whether the marker boundary was crossed.
After a successful publication, the successor takes the bounded exact-current
path and performs no candidate/input blob read or content hash. An already
pending forced successor is never downgraded.

A deterministically missing, malformed, or tampered marked publication—or a
malformed catalog store pointer—follows a different path. The worker first
durably queues an independent forced
successor, then clears that repository's pointer and derived catalog bytes and
finishes the current claim without rebuilding in place. The successor owns the
rebuild, so even a final-attempt claim cannot be reaped after clearing authority
with no pending work. Startup uses the same queue-before-clear ordering when it
finds deterministic invalid authority. The post-lock work context is capped
at five minutes; each `go.mod` is capped at 4 MiB, each fixed layout/generated
snapshot at 10 MiB, and source work in aggregate at 100,000 blob reads and
512 MiB of blob bytes. All four thresholds are committed in member policy.
Snapshot structure is capped at 128 layout roots, 25,000 generated
mappings, and 128 generator invocations. Generated selectors retain at most
1,024 candidates and 128 KiB of candidate identity each, with 25,000 expansion
attempts and 16 MiB of candidate identity across one materialization; cap+1
refuses before building a cross-product. Declaration retention is capped at
25,000 records and 16 MiB of canonical paths. Those four aggregate ceilings
are shared across every enabled protocol adapter in the build and never reset
at a catalog-member boundary. T30.6f's member, catalog,
descriptor, staging-disk, and publication bounds still apply. The materializer
decodes snapshot collections as a bounded stream and rejects unknown,
case-variant, or duplicate keys; this does not change the shipped extraction
parser. A snapshot beyond a structural root, mapping, or invocation bound
publishes an explicit `unsupported` record. Aggregate input-work,
declaration-retention, and lifecycle-output limit refusals are terminal for
that job generation and never publish a partial replacement; an input
transition may already have retired a stale prior generation.

Pack 1.1.0 also parses each mapped generated base-lane Go source once during
materialization and retains its exact package/import, client, constructor,
method, wire-operation, declaration, object, and content identities. It does
not execute generated code or reopen those sources in caller work. This
projection is capped at 25,000 generated source files, 4 MiB per source,
100,000 symbol descriptors, and one 32 MiB catalog-wide identity budget shared
by unique source path/object pairs and descriptors under the same
100,000-read/512 MiB aggregate source budget. A protobuf mapping with no
generator-relative path is an input wildcard; retained descriptors always
carry the concrete parsed `.proto` selector, and overlap with an equivalent
explicit mapping is deduplicated. A declared source above 4 MiB is not read:
its validated path/object envelope is retained as explicit
`unsupported: generated_source_too_large`, and valid protocol siblings still
materialize. Other invalid or unparseable generated content stays explicit
`unsupported`/`unavailable` authority; it never becomes a guessed symbol.

For a matching non-forced job, store authority plus the process-cached
manifest/member fingerprints provides the warm no-op: no candidate manifest
is opened, no candidate or input blob is read, and no catalog or input content
is hashed. A matching cold publication is streamed and hashed once to seed
that cache. Startup reconciliation separately validates every store-authorized
publication, so the first queued reuse after a process restart may perform a
second bounded catalog validation before subsequent jobs become metadata-only.
Stale work performs one strict candidate open, two bounded replays
of one caller projection, and declaration paging once per catalog generation;
caller partitions consume the resulting compiled projection and never rerun
discovery independently.

#### Direct caller-leaf execution

When at least one shipped caller adapter is enabled, every accepted resolver
catalog publication atomically ensures a repository-keyed `caller_leaf_job`;
startup also backfills indexed, non-deleting repositories with a current
catalog. Each turn selects canonical missing pairs one at a time from the
Cartesian product of every configured caller domain and every candidate-v4
caller leaf. Since the 2026-08-02 scheduling repair, a turn completes as soon
as one replayed pair's outcome is durable: the already-ensured pending
successor claims the remaining pairs, and because a successfully completed
turn's successor starts at attempt zero, bounded per-pair progress is never
reported as job failure and only a single pair that cannot finish inside one
five-minute worker deadline can exhaust the three-attempt budget. Terminal
refusals recorded without reading source still drain in one turn, and
admission plus complete publication run in their own fresh turn after the
last pair. The pair remains expected when that leaf contains no record for
the domain and publishes a canonical empty artifact.

A claimed resolver or caller job whose upstream has not converged — candidate
publication absent/publishing/stale, declarations unsettled, the resolver
catalog not yet current, observation absent, or a required partition domain
absent/stale for the new generation — returns a typed dependency deferral. The
runner atomically returns that exact lease to pending with its attempt count
unchanged and a `not_before` fence at the base poll interval. It continues
claiming ready siblings until the exact committed fence returned by the store
expires; once a sibling returns after that deadline, it exits to the
jitter/reap loop. Lifecycle diagnostics retain that exact fence, or omit it
when a separate immediately claimable freshness event wins. The same fenced
transaction moves the deferred row to the pending queue tail, so a fence that
expires during store response latency still cannot outrank siblings that were
already ready.
Extraction settlement enqueues the resolver and
caller jobs together, so on a delta the caller job can legitimately be claimed
before the resolver republishes. A publication arriving during the deferral
wins as an immediately claimable freshness event; otherwise the same job
re-checks on an eligible later poll and terminates when upstream converges.
The deferral mechanism adds no work to a ready turn. A blocked repository pays
one claim and one bounded readiness check per eligible poll, without a tight
drain loop, attempt consumption, or one completed history row per check.
Exact-current caller turns separately pay the relationship-reconcile tail cost
documented below. Before this repair a deferral reported success without
preserving work, and downstream authority could stay stale until restart or an
unrelated later event.

The five-minute caller execution deadline starts only after the repository
mirror lock is acquired. Pointer/control preflight has its own at-most-five-
minute context; waiting behind fetch, indexing, candidate planning, or
aggregate extraction uses the runner's lease-heartbeated parent context and
does not consume a caller attempt. Cancellation or lease loss interrupts that
wait before the candidate caller plan is opened or caller state is mutated.
This changes no timeout, attempt, admission, or retry limit: a pair that cannot
finish within the unchanged post-acquisition deadline remains an ordinary
retry and can still exhaust honestly.

Startup stage cleanup performs one bounded sorted read of the caller root
(at most 65,536 entries) and one bounded sorted read of each package-shaped
repository directory (at most 65,536 entries each), for `O(R + ΣE_r)` names;
it opens or hashes no artifact content and runs even after caller adapters are
disabled so prior-process stages remain reclaimable. No new caller-wide
repository backfill runs at startup. The existing candidate startup backfill
re-drives live repositories, candidate publication transactionally fans out
resolver work, and each exact resolver publication enqueues the caller job
when the caller adapter is enabled. If partition authority is not
yet current, that caller turn performs the bounded repository, candidate,
resolver, observation, and configured-domain control reads before deferring;
it does not read Git source or artifact members.

Every exact-current caller turn invokes relationship reconciliation after the
caller publication is confirmed, including new publication, recovery, cached
republish, and warm-current paths. The v2 no-op reads the current observation,
the bounded configured-domain authorities, the service summary, resolver
current authority (at most 16 declaration rows), and the local relationship
root. A stale relationship target may additionally load the bounded service
snapshot, validate policy/target/schedule controls, and write one binding or
enqueue one schedule. It performs no source, Git, or relationship-member read
on the no-op path. Callback failure remains an ordinary retryable caller-job
error, so a published caller generation cannot silently lose this tail step.

A blocked resolver turn acquires the repository mirror lock before checking
repository lifecycle, candidate publication, and the bounded declaration
authorities; it therefore may repeat that bounded lock wait and control work
once per eligible deferral poll. Caller readiness normally defers during its
preflight before taking the lock. Only a candidate/resolver/partition change
while the caller waits for the mirror lock causes the second authority fence
to defer after lock acquisition.

One cold turn allocates the `D × L` expected pair/outcome envelopes and scans
the caller artifact-name directory once, capped at 65,536 entries. Candidate
member validation remains per pair: with `D` caller domains it reads bounded
caller-member content `D` times, while each admitted base Go record launches at
most one serial immutable-blob read capped by that record's exact declared
size. A newly installed artifact is then descriptor/content-opened and hashed
once before its success row commits. There is no repository tree/corpus walk
or all-leaf content materialization.

The worker takes the repository work lock, opens only the canonical candidate
manifest and leaf envelopes, and descriptor-stably streams the selected leaf.
It excludes `go_test` rows before a blob open. Each base-lane Go row is read by
its validated immutable OID and declared size and scanned against the catalog's
precompiled syntax index; non-Go, oversized, invalid-UTF-8, unresolved, and
neutral inputs retain explicit abstentions. An exact generated path/object
owned by the resolver projection receives a no-read
`resolver_generated_input` abstention, so caller execution never reopens
generated source merely because it also appears in a candidate leaf. The
capability cannot enumerate a
tree, open another candidate leaf, read generated source, consult SCIP, run a
build, or use the network. Its receipt must report zero out-of-leaf reads.

Current V3 caller execution keeps descriptor-present source work and its
receipt accounting unchanged, but compacts a completed pair when it emitted no
caller facts. During the scan it retains only constant-size counters for the
bounded abstention reasons. After exact candidate accounting succeeds, it
truncates the temporary per-candidate abstention stream and writes one
`zero_caller_facts` coverage record that preserves candidate partitions,
source-read count, source bytes, and excluded-`go_test` count. The V3 receipt
also names that reason; artifact verification requires its source bytes to
equal the embedded coverage value, while `no_resolver_descriptors` requires
zero reads and bytes at both the filesystem and durable-store boundaries. Any
unrecognized future fact-free abstention reason disables compaction and keeps
the already valid materialized artifact instead of failing the pair. Any result or
fact-bearing unresolved occurrence disables compaction for that pair. A crash
before install exposes no artifact, and a V2 current publication is replaced
through the ordinary queue-before-clear generation transition; retained V1/V2
artifacts and receipts remain readable under their historical identities.

Pair output is capped at 12,500 results, 4,096 abstentions, 1 MiB per record,
64 MiB canonical content, 65 MiB stage, one serial 4 MiB source blob, 64 MiB
of source bytes per pair, five structurally owned descriptors/pipes, and five
minutes. The source-byte budget is generation identity and is reserved before
each exact declared-size blob read; it is independent of the output cap.
Aggregate admission is
capped at 16 caller domains, 16,384 artifacts, 100,000 results, 100,000
abstentions, 512 MiB canonical content, and 520 MiB staged content; the worker
has a 128 MiB design-memory budget. Exact cap output admits. The first crossing
success may retain at most one additional pair beneath its pair limits; the
worker then records terminal outcomes for every remaining expected pair
without resolver, candidate-member, source-blob, or artifact work. That
bounded cap-plus-one envelope records a terminal generation admission and
cannot replace any older complete generation. Local directory capacity is
mutable installation state, not immutable generation output: when the
65,536-entry bound has only its rename-before-stage-removal crash slot free, a
new pair receives a retryable capacity refusal before content work and records
no terminal outcome or admission. An exact same-pair orphan may use that last
slot only to validate/reuse or reject the existing sibling. Progress for a new
pair requires supported repository cleanup or a future package-owned retention
action to free an entry. T30.6m explicitly leaves historical and incomplete
caller residue unbounded; directory capacity is a refusal ceiling, not an
automatic retention policy.
An expected Cartesian pair set above 16,384 is not representable as an exact
admission: it refuses at typed job preflight before pair content work and
creates no partial outcome/admission row. “Cap-plus-one terminal admission”
refers to aggregate output crossed by one otherwise valid pair, not to this
unrepresentable pair-set preflight.

Artifacts install before their lease/repository/candidate/resolver-fenced
multi-generation outcome. An independent successor exists before that install,
so a crash with bytes but no row resumes from a byte-identical orphan. Another
content artifact for the same immutable pair is terminal nondeterminism.
Terminal outcomes preserve successful sibling pairs but prevent aggregate
admission. On the first worker reuse after restart, current successes are
descriptor/content-validated;
deterministic state/file mismatch queues forced repair before clearing only
identity-proven package bytes, while operational I/O preserves authority.
Logical-record framing damage—including a same-size missing newline or a line
over the 1 MiB record limit—is deterministic artifact corruption and follows
that queue-before-clear repair path. A later install, outcome, admission,
remove, or clear failure is still returned to the runner. The handler-created
successor carries provenance for the exact active lease. A failed ensure
response is treated as commit-ambiguous because the row may already exist;
the same final transition is safe when it does not. Ordinary retries
merge their attempt, error, and `not_before` backoff into that row, and a final
live failure may exhaust it only while that provenance remains exclusive. Any
ordinary candidate/resolver/backfill enqueue clears the provenance, so a
coalesced freshness event survives and receives its own turn. A final-attempt
stale reap likewise preserves the fresh independent successor because the
process may have died after installing bytes; the stale-claim interval paces
that crash path. Persistent live filesystem/store faults therefore back off
and exhaust instead of spinning as successful zero-delay turns, without
discarding fresher work.
The validation and compiled-resolver caches intentionally retain only one
generation process-wide. While that generation remains most recently
validated, a settled warm turn uses pointer/outcome/admission metadata only:
no mirror lock, content open/hash, corpus or tree walk, source blob, or child.
Work for another generation evicts it; the next exact job reacquires the mirror
lock, reopens the candidate plan, and hashes that generation's successful
artifacts once before restoring the metadata-only path. Pair artifacts and
admissions are not independently API/search/evidence-visible. Only the exact
complete manifest and matching store pointer establish caller-generation
authority; T30.6j owns authorized product reads over that authority.

Complete publication continues only from an exact `admitted` generation. It
recomputes one ordered receipt for every successful pair, writes and syncs a
bounded stage, links `complete-v1.publishing`, renames the immutable
generation-and-manifest-derived `complete-v1-*.manifest.json` last, commits the
pointer under the active caller-job lease, and then clears only the matching
marker. The pointer carries a repository-local monotonic publication revision.
An exact pointer retry preserves both revision and timestamp; every real
publish or invalidation advances it, including publish A, clear to unavailable,
and republish the byte-identical A. Candidate/control, resolver, relevant
declaration, indexed HEAD/unit, deleting, caller-state repair, restore, and
repository deletion transitions retire authority atomically. A failed or
terminal proposed generation cannot erase an otherwise-current prior pointer
by itself. If deletion cleanup is rolled back or a repository is otherwise
reactivated, the `deleting: true → false` transition atomically coalesces one
forced caller job; an ordinary refresh of an already-active repository does
not enqueue caller work.

Startup runs complete-publication reconciliation after resolver reconciliation
and before caller workers. A valid manifest-before-store marker is force-queued
and retained for a claimed worker; an exact store-before-marker-clear state is
cold-validated and has its marker cleared without bypassing the job fence.
An invalid or incomplete marker for an eligible repository is force-queued
before cleanup. An absent, unindexed, or deleting repository cannot accept
that work: startup descriptor-removes its canonical marker plus package-owned
manifest/stage residue, and reclaims leaf bytes only when an exact complete
manifest supplies their identities. Unrelated or receipt-less leaf files are
left to the bounded directory lifecycle rather than inferred from corrupt
bytes. Exact ineligible cleanup removes leaves, then the manifest receipt, and
the marker last, making every intervening crash shape resumable. A
deterministically incomplete deletion tombstone likewise removes only
the canonical tombstone; a valid tombstone still completes deletion or is
cleared when store state proves a same-name recreation.
Operational I/O stops startup or retries the job without deleting authority.
Current publications hash each referenced leaf at most once during startup;
the subsequent orphan inventory reuses those admissions while still reading
the canonical manifest and checking directory identity. The store query
projects summaries rather than pair arrays. It transfers the writer-owned pair
payload commitment and actual array length without hashing the array, then
refuses more than 65,536 current publication rows or 65,536 cumulative
manifest-plus-leaf references before exact current checks hash valid persisted
pair metadata once per pointer. Indexed, non-deleting repositories used for
marker repair are keyset-paged in fixed pages of at most 512 and have no additional
total-count ceiling. Before content opens, startup also refuses more than
1 TiB of declared canonical caller bytes across current publications. The
caller root admits a new repository directory only below 65,536 total root entries, and each repository
directory admits at most
65,536 entries. A hostile invalid inventory can therefore require up to the
product of those name bounds across startup's bounded passes; valid admitted
references remain subject to the much smaller installation-wide 65,536-
reference ceiling. Startup performs no Git or child work.

The shared in-process complete-publication registry caches at most eight
parsed publications and 16,384 aggregate pair references; inactive entries
are evicted and store-authoritative states cold-open again on demand. Eviction
retains no full store state or per-repository transition slot. It retains at
most 65,536 compact cleanup-authority tokens, matching the durable publication
installation ceiling: one fixed 85-byte cryptographic repository-directory key
and one fixed 155-byte exact manifest basename per token. That is at most
15 MiB of raw identity payload plus bounded Go map/string overhead. A new
authority above the cap refuses retryably; an existing repository's exact
replacement does not consume another slot. Retirement reconstructs a transient
serialization slot, descriptor-opens that exact manifest, verifies its
canonical digest and decoded repository, removes referenced leaves before the
manifest, and clears the token only after successful cleanup. If every
admission that could be evicted is lease-pinned, another admission returns a
retryable capacity refusal without removing its durable bytes. Acquisition
first rechecks a pair-free store summary with a scalar authority, revision,
actual-length, and stored-commitment fence, then uses captured directory,
marker, manifest, and leaf file identities; all leaf identities are checked
beneath one repository descriptor. Its final store fence hashes the persisted
pair metadata once server-side; marker/conflict recovery may repeat that exact
fence before changing marker or registry state. A warm caller job performs `O(P)` file stats
but no leaf-content read or hash, allocates no `P`-element Go pair copy, and
takes no mirror lock. The first cold acquisition reads one
at-most-32-MiB manifest and each exact referenced leaf once under the
16,384-pair and 512-MiB canonical aggregate ceilings. Readers lease an
immutable descriptor snapshot. On replacement, old process/store visibility
retires immediately, but its manifest remains as the durable cleanup receipt
and its unshared leaves remain until the last lease releases;
same-state descriptor replacement refreshes future leases without deleting
identically named bytes. Final release may synchronously unlink that retired
publication's at-most-16,384 pair references and sync its repository
directory. A repository deletion with an active lease first writes a canonical
deletion tombstone; final release removes the bounded package directory, while
startup completes a valid crash-interrupted deletion or clears only that
tombstone when store eligibility proves a same-name recreation. An incomplete
tombstone never authorizes directory removal. Publication transitions
scan at most 65,536 directory names and
reclaim residue in resumable batches of at most 32 manifests, 65,536 pair
references, and 1 GiB of manifest content; later startup or publication
transitions resume residue left by that batch. Closing the process registry
blocks new acquisition only and never removes store-authoritative files.

Every real caller publication transaction recomputes the installation fence by
aggregate-scanning at most 65,536 current caller pointer rows before it accepts
the proposed row. This is `O(N)` store work per real publish. An exact retry
skips that installation-wide scan after comparing its own `O(P)` persisted and
proposed pair arrays; the warm no-publication job path does not execute the
writer.

The private pair-payload commitment is store integrity metadata, not product
identity. A same-length raw pair mutation therefore fails the final current
fence even when every projected aggregate remains plausible. Startup queues a
forced successor before clearing that malformed pointer. During a live claimed
job, the exact admitted writer may replace only a commitment-invalid current
payload and advances caller-publication revision; a different payload whose
commitment validates remains a nondeterministic same-generation conflict.

#### Authorized exact Caller Map reads

When a shipped gRPC or Thrift caller adapter is enabled, `serve` constructs the
T30.6j publication reader over the same adapter registry and complete-
publication lease registry used by the caller worker. The public Caller Map is
registered only when that reader is available. Reader index and request-binding caches
start empty: startup does not build a reverse index, scan caller records, read
Git source, take a mirror lock, or start a child for the read surface. The
T30.6i startup reconciliation and publication admission described above remain
the separate derived-state lifecycle. The T30.6j store upgrade advances the
caller-publication migration marker from v1 to v2 and derives a store-owned
`publication_incarnation` for each existing pointer in place. Process-local
bindings have already been lost at startup, so this upgrade does not require a
global caller rebuild; later publications derive the incarnation from the
exact owned writer claim plus a fresh store nonce, including when the same job
survives a delete/recreate transition.

Every list, continuation, and citation request first authenticates and checks
permission for the one endpoint repository. It also requires that repository
to exist and not be deleting before it reads a caller pointer, opens a caller
directory, or consults a repository-specific read cache. Unknown, hidden, and
deleting repositories therefore share the same `404` response and cannot
affect rows, totals, gap detail, cursors, or caller-publication/index work
shape. The final store/filesystem authority sweep runs before authorization is
rechecked as the last result fence after response construction. A permission or authority transition
during the request fails closed instead of serializing rows from the former
snapshot. After authorization, at most eight exact list, continuation, or
citation requests perform store, publication, and index work concurrently;
excess work receives an immediate retryable service-unavailable response. The
gate ends before HTTP/MCP transport serialization, so it bounds active service
work, not the lifetime of a slow client's encoded response.

For an authorized repository, the generation state has these meanings:

| State | Product behavior |
|---|---|
| `current` | One exact complete generation is leased and may supply rows. |
| `missing` | No current complete generation is authoritative; rows and totals are unavailable. |
| `failed` | Current derived state is deterministically invalid, or the expected generation has a terminal admission; rows and totals are unavailable. |
| `stale` | The pointer, resolver, filesystem publication, or cursor binding no longer agrees with current authority; rows and totals are unavailable. |

Only `current` carries a request-scoped immutable lease. A first-page
`missing`, `failed`, or `stale` response contains
no partial rows and sets `matching_rows_state: unavailable`; it omits the
numeric total rather than serializing zero callers. Operational store or
filesystem failures remain request errors rather than being collapsed into a
gap. Pointerless gaps repeat the scalar authority/admission selection at the
result fence. T30.7 also repeats the exact-generation leaf-outcome aggregate:
the gap may disclose durable settled/succeeded/refused partition counts, with
an admitted total when known, but still emits no caller rows or numeric caller
total. An admitted generation whose leaves have not settled yet honestly
reports `partial` progress at zero of its admitted total; that shape is valid
in the signed gap authority rather than a conflict. Current generations derive complete partition progress and an explicit
base/`go_test` record census from the already validated publication payload.
The response carries the active focused/whole search scope separately from
the repository-overlay caller generation; its signed authority commits only a
fixed digest of that bounded scope, and final authorization recomputes it.
A deterministic cold-filesystem refusal is retained under its
exact generation/revision in an eight-entry reader cache, so its result fence
and later no-op requests recheck scalar authority without hashing the complete
publication again. A transition detected while continuing a cursor or reading a citation is
a conflict and requires a new first-page read.

The first page reads the exact complete pointer, including its at-most-16,384
pair receipts, binds the matching resolver publication, acquires the shared
lease, and applies a final exact store/filesystem fence. On a reverse-index
cache miss, it then streams every canonical caller record in the complete
generation, bounded by 100,000 results plus 100,000 abstentions and 512 MiB of
canonical leaf content. It retains at most 200,000 projected records and
128 MiB of explicitly counted record identity in that index; the count is an
accounting bound, not a Go-heap measurement. The process retains at most eight
such indexes, so the maximum counted identity across the separate index cache
is 1 GiB plus Go object/map overhead. Cold index construction is serialized
through one process-wide slot, so at most one additional 128 MiB counted index
is under construction (a 1.125 GiB transient counted ceiling plus overhead).
Deterministic semantic-projection and index-identity-limit refusals are retained
as tiny negative entries under the same exact key and eight-slot bound; a
stable retry does not rescan 128–512 MiB. Transition and operational failures
are never negative-cached. Cache replacement drops the oldest index with no active request or request
binding; if all eight are busy, the newly built request receives retryable
service-unavailable rather than evicting live state. Cold reader admission is
single-flight per exact key and admits at most 64 distinct active keys; a 65th
key receives the same immediate retryable response.
The 128 MiB ceiling is an independent consumer-admission bound, not a value
derived from the caller writer's 200,000-record and 512 MiB publication
ceilings. A writer-valid maximum-count generation can therefore exceed the
reader budget; it receives deterministic `422` with no rows or numeric total,
and retrying the same exact generation reuses the negative entry. The fixed
reference and derived-record-ID charge is 357 bytes per record before semantic
payload. Exact-cap admits and cap-plus-one refuses. Raising this bound would
multiply every retained index; requiring every writer-valid generation to be
readable instead needs a future frozen aggregate projection-identity writer
policy and rebuild.
Exact semantic projection revalidates canonical operations, declaration
lineages, source coordinates within the 4 MiB direct-source envelope, direct-
result heuristic confidence, nonempty code roles, unit states/reasons, and
result-versus-abstention kind/predicate pairing. One record may carry at most 25,000
canonically ordered unit candidates; each candidate has at most 64 ordered
values per attribution category and each value is at most 4 KiB. Counted index
identity includes deterministic charges for candidate structs and retained
string headers as well as payload bytes. Endpoint lookup keys retain only
string headers into that already charged operation payload, rather than a
second copy of each operation.

The shared publication registry may independently cold-admit at most two
repositories concurrently before that scan. Each admission streams one
at-most-32-MiB manifest and its at-most-512-MiB complete canonical generation;
these are I/O/work ceilings, not claims that all bytes are resident in Go heap.
A first authorized read after restart can therefore perform one bounded cold
publication validation and one bounded index-construction pass, while another
repository may be undergoing its own cold validation.

Filtering retains integer positions, not another copy of records. At most
eight live request bindings share at most 200,000 positions and survive for up
to five minutes after their first page. Every non-empty first page retains one
so both continuations and citations can use compact references. Each live
binding pins its reverse index. Before crossing either capacity, admission
preflights and retires enough of the oldest idle bindings; their cursors and
citations then return conflict and must be relisted. Pressure made solely of
active or retired-in-flight bindings receives a retryable service-unavailable
response. An expired/retired binding that is still in use remains fully counted
and keeps its index pinned until the active request releases it. Expiry of a decoded live-process binding makes continuation conflict;
a ninth cold index similarly prefers an inactive unbound victim, then an
inactive index whose complete pin set is idle; it pressure-retires that set
before eviction. Active and retired-in-flight pins are never reclaimed.
a process restart rotates the HMAC secret, so an old token is invalid input and
the client starts again from a new first page. Each HMAC cursor binds its normalized query
and page size, authorization projection, generation digest, manifest digest,
pair-set digest, monotonic publication revision, store-owned publication
incarnation, and next offset. The claim-plus-fresh-nonce incarnation cannot
repeat across same-name repository delete/recreate even if revision and second-precision
publication time repeat, so a real transition—including `A → B → A`—cannot
resume the old cursor. Exact index and deterministic-failure cache keys carry
the same incarnation.

A warm continuation reopens the pair-free scalar binding, reacquires a lease,
and reads and validates only its at-most-100 referenced canonical records;
each record is capped at 1 MiB and no read scans or hashes the remainder of its
leaf. Reopen and final current fences still perform complete-publication
identity checks, so one warm page may make roughly three `O(P)` stat sweeps
over the at-most-16,384 leaf references without rereading or hashing their
content. It never rematerializes the reverse index: if that index was evicted, the
page fails defensively with conflict, while a live unexpired binding normally
prevents that eviction. Leases deliberately do not survive between requests,
so pressure that evicts the same publication from the separate shared registry
may cause its existing bounded cold manifest/leaf validation before a later
page can reopen. Thus warm admitted paging has no intrinsic repeated full hash,
while restart or cache pressure can reintroduce one bounded cold-admission pass.
Every request releases its lease after the final authorization and authority
checks; a retired generation's bytes remain only until the final active request
releases them. A maximum 100-row page may retain close to 100 MiB of canonical
record/response data before encoding; this finite page bound is separate from
the eight-request service-work gate and may outlive that gate during transport
serialization.

Each row carries `repository-overlay` provenance: repository, indexed commit,
canonical path, Git object ID, SHA-256 blob digest, byte and line range, exact
generation and publication identity, and an opaque signed citation. A citation
token contains only its repository authorization key, random process-local
binding ID, bounded index position, and exact record ID; maximum-shaped path,
policy, extractor, and visibility fields remain in the capped binding rather
than being duplicated into the token. The signed repository is authorized
before the binding cache is touched. Tokens share the process-local HMAC secret and 16 KiB envelope
with cursors and survive with their up-to-five-minute binding; after expiry,
idle pressure retirement, or process restart, list the row again rather than
retaining the old token.
HTTP
`GET /api/contract_callers/citation?citation=...` and MCP
`read_operation_caller_citation` share one citation reader. It reauthorizes,
reopens and fences the same complete generation/revision, rereads only the
named canonical caller record, and rechecks its pair, operation, lineage,
source coordinates, object ID, and digest. It then runs bounded immutable Git
resolution (three small `rev-parse`/`cat-file` metadata children) and one
`cat-file blob` child, reads the complete cited blob under the existing 4 MiB
direct-source limit to verify SHA-256, and returns only the cited byte range.
At most two citation Git/blob phases run concurrently, bounding active child
and blob-read work; excess citations receive immediate retryable service-
unavailable. The phase gate also ends before response serialization.
It does not enumerate a tree or directory and the opaque citation grants no
unrelated path, whole-file, focused-search, or local-evidence access.

The exact reader exposes direct-syntax results and retained abstentions for one
complete repository-overlay generation. It does not prove runtime use,
completeness, extraction accuracy, migration completion, decommission safety,
or a retention bound. Caller comparison now uses the same exact engine under
the additional two-sided fences below. Workbench Impact now uses that shared
single- or two-generation exact authority under the composition fences below.

#### Authorized exact caller comparison

T30.6k registers caller comparison only beside the exact Caller Map service;
it reuses that service's publication reader, leases, reverse indexes, HMAC
secret, binding registry, service gates, and exact-range citation reader. It
does not start a second reader or cache and does not reconstruct a comparison
from legacy evidence rows, coverage, or attribution digests.

Every first page and continuation authenticates and authorizes both the old
and replacement repositories before either repository's caller pointer,
publication directory, or cache key is accessed. Unknown, hidden, and deleting
repositories all return the same `404`. For a current pair, the service
acquires both request-scoped complete-publication leases. After the page is
built, the store checks both bounded summaries in one transaction, the reader
checks both final publication descriptors together, and both authorizations run as the last
result fence. A permission or publication transition on either endpoint
therefore fails the entire read rather than exposing rows from the other
endpoint.

Both endpoint generation panels use `current`, `missing`, `failed`, and
`stale`. Only two current generations may be compared. If either side is
unavailable, each side repeats its applicable bounded scalar authority and
admission selection before the response becomes one whole-page typed gap with
no rows,
classifications, cursor, or numeric total. An operational store or filesystem
failure remains a request error. In particular, the service never converts an
unavailable old side into `new_only_evidence`, an unavailable replacement into
`old_only_evidence`, or either gap into a zero-caller claim.

For a current pair, the first page uses both exact reverse indexes and inspects
at most 50,000 protocol/operation-bucket positions across them, charged before
declaration-lineage and optional filters. It retains one compact two-
index binding of integer positions and bounded comparison metadata beneath the
same process-wide limit of eight live bindings, 200,000 aggregate retained
positions, and an up-to-five-minute lifetime. It neither copies complete
caller records into a second cache nor adds an unbounded comparison registry.
Idle pressure retirement, expiry, or restart requires a new first page; active
and retired-in-flight bindings remain fully counted. One request may hold at
most two parsed complete publications and 32,768 pair references total. A cold
first page can stream each missing reverse index once under the existing
one-index-build and 128-MiB counted-identity limits; it performs no corpus
tree walk, mirror lock, or source Git read.

The HMAC cursor binds the complete normalized comparison query and page size,
both authorization projections, both generation/manifest/pair-set digests,
both caller-publication revisions and non-repeating incarnations, the retained
comparison binding, and next offset. Either side can transition independently;
permission loss, binding expiry, pressure retirement, restart, or any
generation/revision/incarnation change conflicts and requires a restart from
the first page. The `A → B → A` fence applies independently to both sides.

A page defaults to 50 and admits at most 100 classified rows. Each side of one
row exposes its exact occurrence count and at most four source citations with
an explicit truncation bit, while the entire page hydrates at most 100 exact
canonical records. A resolved unit's potentially large attribution appears
once on the comparison row and is not duplicated in its citation samples. A
warm continuation never rematerializes either reverse
index: it reacquires both leases, rereads only those selected records, and
performs both publications' bounded identity/final-authority sweeps. It adds no
caller-content hash, store mutation, mirror lock, source Git read, or child
process. Restart or shared-registry eviction can still cause the separately
bounded cold publication validation. Shared binding/index bookkeeping locks
are not held while publications stream, records hydrate, final store and
authorization fences run, citations read Git, or transports encode responses.

Comparison citations are ordinary compact `caller-map-citation-v1` tokens.
Open them only through `GET /api/contract_callers/citation` or MCP
`read_operation_caller_citation`; those routes reauthorize the cited side,
reopen the exact generation, verify the immutable commit, object ID, complete
blob digest, record, and range, and return only the cited bytes. Comparison
does not grant tree, directory, unrelated-path, whole-file, focused-search, or
local-evidence access.

Startup creates no comparison binding, reverse scan, caller-content hash, Git
read, mirror lock, or child work. The shared eight-active-read and two-citation-
phase gates remain in force, and their slots end before transport encoding.
Static comparison rows and the literal old-only/both/new-only/unresolved
classifications establish no runtime use, completeness, extraction accuracy,
migration completion, decommission safety, production validation, or
bounded historical-retention claim.

#### Exact Workbench caller composition

T30.6l registers `workbench-impact-inventory-v2` only with the already shared
exact Caller Map and comparison services. Production construction creates
those services once and passes the same Caller Map instance into comparison
and Workbench. Do not create a second exact reader for Workbench: a second
instance would split the HMAC secret, reverse-index cache, request-binding
registry, publication leases, citation reader, and concurrency gates and would
invalidate the intended process bounds.

Every Impact request first reads and validates one current authorized
Investigation Revision and immutable Change Brief. Modify and retire call the
single-generation exact reader; migrate calls the jointly fenced two-generation
comparison; add has no caller stream. Each subordinate service keeps its own
authorization-first repository access and final store/filesystem/permission
fences. After Atlas, caller, comparison, compatibility, fields, and resource
planes are assembled, Workbench rereads the Investigation as the last result
fence. A revoked, hidden, or replaced current Revision returns the
same non-disclosing not-found posture. A changed digest for the selected
Revision/brief conflicts instead of serializing a mixed composition.

Caller and comparison output carries its exact `repository-overlay`
generation and `matching_rows_state`. `current` may expose rows and a numeric
total. `missing`, `failed`, and `stale` are typed unavailable gaps and expose
no partial rows, groups, classifications, subordinate cursor, or numeric zero.
The generation gap is added to Analysis scope as a caller capability/gap. It
is not added to the Coverage list: that list remains authority for
focused-local extraction publications, not complete repository-overlay caller
generations.

The HMAC-authenticated v2 outer cursor retains each subordinate stream's
semantic snapshot. For a
caller or comparison stream that completed before the outer page, it also
retains a signed transport-hidden authority token. The token uses the shared
HMAC and at-most-16-KiB exact-token envelope and binds normalized query,
visibility projection, repository revision, generation/manifest/pair-set and
caller-publication revision, snapshot state, and the non-repeating publication
incarnation for every bound publication across one or both sides. It is never
serialized in the public Impact
page as a distinct field; it exists only inside the opaque composite cursor
and is not a source citation or subordinate pagination cursor.
The outer signature covers every stream's complete/next state, so a client
cannot skip remaining rows by rewriting an unfinished stream as complete.

On a later outer page the exact service confirms that token directly. It
reauthorizes, reopens, and applies its current/final publication fences, then
returns only the bounded generation-state confirmation. It does not list page
one, allocate or pressure-retire a request binding, rebuild a reverse index,
hydrate caller rows, mint citations, or encode a duplicate caller response.
Thus a long field-reference stream cannot exhaust the shared eight-binding
limit merely by repeatedly confirming an already completed caller stream.
Restart, permission change, token invalidity, or any generation,
publication-revision, incarnation, or `A → B → A` transition conflicts and
requires a fresh first Workbench page. If the shared publication registry was
evicted, confirmation may still pay its already bounded cold manifest/leaf
validation; it makes no stronger zero-hash promise across that boundary.

Composition bounds are cumulative but finite:

- the public request accepts 1–100 rows and the v2 composite cursor is at most
  64 KiB in both encoded and decoded form;
- the UI requests 25 rows, mounts one server page, and retains at most 500
  cursor entries independently for Impact, implementation, and checklist. At
  the 64 KiB token ceiling one history is below 32 MiB of encoded cursor text;
  the How step can hold its implementation and checklist histories together
  below 64 MiB before JavaScript string/array overhead, while the Where and
  How steps unmount rather than retaining all three histories together;
- exact caller/comparison work remains under the shared eight active reads,
  two citation Git/blob phases, eight bindings, 200,000 retained positions,
  eight indexes, 128 MiB counted identity per index, and documented cold
  publication/index admissions;
- a maximum exact caller response may still approach 100 MiB. The 100-row cap
  is a finite transport bound, not a small-memory claim, and service-work slots
  still end before response encoding;
- checklist derivation reads no more than five 100-row Impact pages and five
  100-row implementation pages, processes each Impact page before requesting
  the next, and uses a deterministic top-1,000 accumulator rather than
  retaining every raw candidate. Final suggestions retain at most 32 evidence
  references each, and the response returns at most 100 entries behind a
  64 KiB cursor. Its mutation body remains capped at 512 KiB.

Checklist identity deliberately excludes transport authority. Before hashing
an Impact page or caller evidence, the checklist projection removes the outer
cursor, every subordinate cursor, and every signed caller citation. It retains
page digests and compact suggestion evidence rather than five complete exact-
caller pages. Rotating a binding, citation, or cursor for an unchanged exact
publication therefore cannot change suggestion IDs or make an existing human
Disposition stale. The five-page ceiling still emits an explicit truncation
suggestion.

Workbench caller source access is only the shared exact-range citation route.
It reauthorizes and verifies immutable commit, path, Git object ID, complete
blob digest, record, and byte range before returning those bytes. There is no
whole-file fallback and no tree, directory, unrelated-path, focused-search, or
focused-local evidence authority.

Startup constructs empty shared exact caches and the Workbench composition
only when its experimental capability is enabled. It adds no eager caller
scan, publication open, reverse-index build, Git read, content hash, store
write, mirror lock, or child process. Warm active streams inherit the exact
readers' selected-record and bounded identity-sweep costs; completed streams
use only the confirmation path above. Registry locks are not held across
publication validation, authorization, Git citation work, final Investigation
confirmation, or transport serialization.

No row, exact empty page, typed gap, completed cursor, or fully dispositioned
checklist establishes runtime use, completeness, extraction accuracy,
compatibility, migration completion, decommission safety, production
validation, or historical-retention safety.

If import begins and then fails, the partial target is retained and every
later restore refuses it; quarantine or remove it under the witnessed
recovery procedure rather than retrying over it.

The subsequent normal `serve` start revalidates restored focused publications
against committed unit/revision state. It keeps exact valid focused bytes,
clears and force-requeues any claim whose focused publication was invalid or
omitted, rebuilds excluded whole-repository shards, rebuilds the cleared
candidate publications before extraction, reconciles resolver-catalog crash
markers, queues any required catalog replacement, and starts its worker when a
shipped caller adapter is enabled. It also reconciles complete caller
publication markers and valid pointerless restored bytes for indexed,
non-deleting repositories, reclaims only complete-manifest-authorized residue
for ineligible repositories, repairs receipt-less markers without guessing
leaf identities, force-queues any required complete-generation replacement,
and starts the caller worker; boot
sync re-clones missing mirrors.
Restored API keys and sessions remain
live — rotate them if the backup's custody was ever in doubt.

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
- Named bearer keys are read-only for Investigation mutations by default.
`investigation:write` is an immutable, explicitly selected credential gate,
not an authorization grant: all repository visibility, ownership, principal,
Revision, preview, snapshot, and idempotency checks still apply. Use one
least-privilege capable key per agent, replace rather than edit capabilities,
and revoke immediately on suspected leakage.
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
setup, logout, API-key creation and its reviewed capability selection,
revocation, and each mutating API
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

Sync, fetch, index, candidate-manifest, extraction, and resolver-catalog work
runs through queues in SurrealDB,
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

#### Verbose index diagnostics

Indexing is intentionally quiet by default. To expose parent phase transitions
and the OOM-isolated builder's stdout/stderr, enable the startup-bound switch
and restart phebs:

```yaml
indexing:
  verbose: true
```

Each forwarded child line is prefixed with the repository. Parent messages
cover repository-census start/summary, child start, successful completion with
duration and total shard bytes, state commit, and already-current skips. Child lines longer than 64 KiB are
split into bounded continued records and invalid UTF-8 is replaced before
logging. Independent of this switch, a failed child carries only its newest
1 MiB of output into failure classification and the job error; successful
child output is discarded. Verbose mode does not change the indexed corpus,
timeouts, retries, process isolation, or shard publication. Disable it again
after diagnosis when indexing a noisy large repository.

#### Pipeline diagnostics

Candidate planning, extraction, resolver work, and caller-leaf turns use
separate bounded diagnostics from the zoekt child. Enable only the components
needed for a diagnosis and restart phebs:

```yaml
diagnostics:
  jobs: true
  candidates: true
  extraction: true
  extractor_details: true
```

`jobs` emits source-free JSON transitions for every durable queue with job
identity, kind, target, attempt, eligibility-relative queue wait, handler
duration, generic outcome, and the exact next retry gate when applicable.
`candidates` logs the successful index handoff and one operation receipt per
candidate job. The receipt distinguishes warm no-op, cold reuse, marker
recovery, repair, rebuild, and not-ready decisions; separates mirror wait,
tree walk, spooling/external sort, filesystem publication, control
fingerprint, database transition, and marker cleanup; and reports only
aggregate plane records/members/canonical bytes, declared source bytes,
typed-input posture, and logical peak spool bytes. A warm no-op deliberately
sets `manifest_summary_present=false` rather than reopening control or member
bytes merely for logging.

`extraction` emits pointer and strict-open preflight (including the observed
settled-domain prefix and an explicit flag saying whether that count is
complete), the ordered durable
scheduler posture, exact deferral triggers, durable outcome transitions, and
the existing capped operation receipt. Domain receipts include corpus and
candidate files, open attempts/files/bytes, facts, unresolved rows, staged
rows, phase timings, and the frozen limits that governed the turn. Enabling
`extractor_details` adds at most 32 fixed nonnegative counters per domain;
current gRPC/Thrift/Kafka packs report generated-input/pass/call/import/topic
and abstention aggregates suitable for diagnosing a published-empty result.

Every receipt is advisory and JSON-bounded. Reporting failure cannot change a
job, retry, evidence publication, or candidate publication. Source paths,
source samples, blob content, handler error text, credentials, and child
stdout/stderr are excluded. Candidate spool peak accounting is maintained
inline as files grow and retire; it performs no directory scan. Disable the
switches after diagnosis to remove their formatting and counter overhead.
Extraction phase, scheduler, completion, and outcome lines use the synchronous
process logger while the repository mirror lock is held, so a slow log
destination can extend that diagnostic turn. Keep the destination draining
and enable these switches only for a bounded investigation.

#### Closed pipeline refusals

Derived-pipeline failures that reach the source-partition, observation,
candidate, or extraction boundaries now expose the source-free
`phebs-pipeline-refusal-v1` shape. The rendered and durable form contains only
`schema`, `stage`, `generation_kind`, `classification`, `dimension`,
`observed`, and `limit`. Repository names, paths, object IDs, digests, child
output, and raw errors are not part of this projection.

The closed stage/generation pairs are:

| Stage | Generation kind |
| --- | --- |
| `source_partition_planning` | `source_partition` |
| `observation_publication` | `observation` |
| `candidate_strict_open` | `candidate` |
| `domain_inventory` | `extraction_domain` |
| `extractor_execution` | `extraction_domain` |
| `evidence_staging` | `extraction_domain` |
| `final_publication` | `extraction_domain` |

A `limit` refusal names the owning dimension and records the exact nonnegative
`observed` and `limit` scalars with `observed > limit`. An `invalid` or
`unknown` refusal uses `dimension: unknown` with both scalars zero; phebs does
not infer a measurement it did not obtain. Crossed stage/generation or
stage/dimension authority is invalid and is resealed as a closed `unknown`
failure rather than rendered with private context.

The original in-process error remains available to `errors.Is`/`errors.As`,
but durable queue text is selected structurally from the closed refusal even
when another error wraps it. Extraction outcome receipts carry the same
optional projection. When it is absent, the retained v1 outcome bytes remain
unchanged. This classification changes neither retry policy nor a previously
settled generation.

Limit scalars reuse counters already required by the owning boundary. On a
successful request, sync tick, startup/restart, retry/no-op, or publication
transition, this mechanism adds no read, hash, store operation, child process,
or formatting work; only a failure allocates and encodes the small projection.
The observation-progress response keeps its existing JSON `schema` field; the
Go-only field rename merely prevents Huma from producing a duplicate-`Schema`
warning during OpenAPI generation.

#### Analysis-unit state and rebuilds

At startup, phebs always emits one source-free analysis-unit posture receipt
per configured entry, or one whole-repository receipt when none is configured.
It contains the unit name and stable digest, primary/supporting path **counts**,
the exact search and typed-index postures, enabled extractor domains, and a
configuration recommendation. Selected path strings are intentionally not
logged. The configuration guide owns the
[strict schema and limits](./CONFIGURATION.md#analysis-units).

The committed projection is available on authenticated
`GET /api/repo-status` as `analysis_unit`. It contains:

- `schema`, `name`, and the canonical `digest`;
- sorted `primary_paths` and `supporting_paths`, with entry counts; and
- `search_index_posture` and `typed_index_posture`.

The general `GET /api/repos` shape remains unchanged and never exposes the
internal committed field. Repository visibility continues to authorize both
the status row and its path metadata; an analysis unit grants no additional
repository access.

Startup compares desired configuration with the unit state atomically stored
beside the indexed HEAD and complete revision list. A name/path change or
removal queues one index job even when commits are unchanged. An already
indexed repository uses a forced replacement so the child cannot take its
incremental short-circuit. The prior complete state remains authoritative
until the replacement child succeeds and the new revision/unit state commits
in one database update. A failed or canceled job therefore leaves the previous
state available while the child builds. Publication then creates a stable
marker that removes the repository from search, replaces the old shards from a
synced private stage, renames the complete manifest last, commits the matching
database state, and removes the marker. A child failure occurs before this
swap and leaves the prior publication available. An interrupted publication
or failed state commit fails closed and is cleared/requeued rather than
serving mismatched bytes.

Private `.phebs-build-*` and `.phebs-restore-*` workspaces, plus temporary
publication markers, carry a process token. Runtime reconciliation preserves
work owned by the current process and removes only residue owned by a prior
process after a crash. The startup `artifact reconciliation` summary includes
the reclaimed count as `lifecycle=N`; these paths are derived and are never
backup content.

Legacy rows reopen with no unit state and are not rewritten or rebuilt when
`analysis_units` is absent. Configured repositories use the focused child and
report `search_index_posture: focused`; unconfigured repositories retain the
whole-repository child. A repository-root `index.scip` remains
`repository-root-unbound`.

Every focused shard carries the exact ordered revision set, unit digest,
builder policy, and generation digest. Member sidecars bind ordinal/count plus
content and decoded-metadata digests; one stable manifest declares the exact
set. Search and reconciliation reject a publication marker, missing/extra
member, mixed/stale digest, sidecar disagreement, branch/commit mismatch, or
undeclared repository-owned shard. Search validation is repository-local and
cached only while committed state and every already-bound
manifest/member/sidecar identity agree. Warm queries inspect only those known
repository-local files; undeclared added files cannot enter the static reader
and remain cold-admission/reconciliation errors. Each query opens only that
exact validated member set in a static no-watcher composite, so a
shared-directory watcher delay cannot serve a retired same-commit scope and a
transient shard belonging to another repository cannot remove this one from
the query. One 10-second wall budget covers query compilation, starter-owned
cold validation/materialization, execution, and result-time identity checks.
At most two cache-owned fills run at once. The query that starts a fill and
same-generation concurrent queries share that fill within their own budgets.
Saturated cold work queues behind the two slots. Deadline expiry fails the
query rather than returning a knowingly partial RepoSet. A timed-out fill may
continue for up to 10 minutes. A later query reuses its completed exact
binding, and shutdown cancels and joins those loaders. Stable negative
validation entries retry with exponential backoff from 250 ms to 30 s; an
artifact-fingerprint change retries immediately. JSON fan-out uses at most
eight workers and incrementally retains only its global top K. Progressive SSE
batches retain arrival-order delivery under one shared display ceiling. A
whole repository that commits focused posture while a query is running is
rejected at the result gate with an explicit generation-change error; the
query cannot succeed with fewer files or retired whole content. Deleted,
unindexed, or whole-posture cache entries close after their active query
leases release.
`phebs_focused_index_opened_blobs` and
`phebs_focused_index_opened_blob_bytes` measure successful Git reads at the
trusted scope-checking boundary; any attempted out-of-unit read fails the
child and is never published. Focused builder policy v2 passes selected
zoekt-admissible text through 64 MiB with the same explicit size ceiling and
preflights the pinned content classifier. A larger blob, binary content,
nonempty content shorter than one trigram, or text above 20,000 distinct
trigrams refuses the complete replacement rather than disappearing silently
from content search. The child does not preload the corpus: the pinned builder
retains one 64 MiB current-shard batch, with at most one admitted-document
overshoot, and flushes synchronously. It refuses a result or manifest before
it would exceed the 1 MiB control-file reader envelope.

#### Whole-repository publication handoff

Unconfigured repositories retain whole-repository indexing, but their
searchability now uses the same explicit filesystem/store handoff as focused
indexes. Before publication, phebs renames every builder shard into a
`phebs-whole-<repository-sha256>_v<format>.<ordinal>.zoekt` namespace and writes
one canonical manifest containing the exact ordered revision set and each
member's ordinal/count, byte length, content digest, and decoded-metadata
digest. Shards are synced first; the manifest is renamed last while the stable
repository publication marker remains present. The matching indexed row
commits before the marker is removed. The manifest digest is a byte-publication
receipt, not semantic identity: a builder timestamp can change it across
equivalent rebuilds.

New whole-repository publications also carry two side-by-side v2-derived
receipts. `phebs-source-<repository-sha256>.manifest.json` binds the exact
ordered indexed revision set and immutable JSONL census members. Equal
path/mode/type/object/size identities across revisions have one physical-owner
record with multiple revision ordinals; validation rejects two owners for one
path in one revision. Symlinks and gitlinks are recorded as explicit
boundaries, and the zoekt child is invoked with submodule recursion disabled.
`phebs-search-<repository-sha256>.manifest.json` binds that source digest to
the direct topology and the complete existing whole-shard root and member
identities. All code can still read an older v1 whole publication only when
this v2 search root is absent. A service search requires the complete v2 root;
if a v2 root is present but invalid, phebs reports the scope unavailable and
never falls back to or relabels legacy results.

The source census is bounded to eight indexed revisions, 10,000,000 physical
owners, 80,000,000 revision placements, 4,096 records and 64 MiB per member,
16,384 members, 4 GiB of encoded members, 4,096-byte paths, and 8 MiB control
files. It starts at most eight `git ls-tree` children and retains one current
record per child plus the at-most-eight owners for the current path; it does
not load the repository inventory or blob bytes into memory. Publication
strictly rereads each staged census member once before binding its root. The whole zoekt
command explicitly fixes a 2-MiB file limit, 100-MiB shard-corpus split, 20,000
trigrams per document, and nonrecursive gitlinks. A configured or discovered
`zoekt-git-index` is refused unless its Go build metadata names the expected
main package and exact zoekt module version/checksum linked by this source
line.

Source members and root move first under the ordinary publication marker,
then shard members and the whole root, and the search root moves last. The
repository indexed row still commits before marker removal. Repository cleanup
owns the hashed source/search names and removes them with shards after a failed
state commit or deletion. Complete v2 generations are derived but included
byte-for-byte in the existing search-index backup archive. Invalid or
incomplete generations are omitted with explicit backup counts; their precious
catalog and service state still restore, and the service remains unavailable
until a later whole index rebuilds the derived generation. An already-current
index retry validates only the bounded control roots and returns before Git,
source-member or shard hashing, rewriting, or activation work.

T34.2 adds the side-by-side service-query compiler contract but does not
register it with the live reader. A prepared scope accepts one authorized
repository-local service in `current` or explicit `stale` state, verifies its
current catalog/state fences and exact active catalog against the T34.1 direct
search root, then places repository, exact branch, and all distinct authorized
path prefixes inside the zoekt query before ranking. Unowned paths never enter
a service scope. Unavailable, conflict, removed, mismatched-source, and
multi-repository scopes refuse. The compiler caps expressions at 16 KiB and
inherits 128 distinct paths/64 KiB of path bytes, producing at most 128 path
atoms and 132,608 conservative quote-expanded predicate bytes. Reusing one
opaque prepared scope for another expression or indexed selector does not
reopen a catalog, source member, or shard. The internal service reader now
strict-opens that prepared scope against an exact immutable whole-reader lease
and repeats its repository, state, catalog, source/search control, and lease
fences after the query. Concurrent index, catalog, restore, rollback, or
deletion transitions discard the response. Startup and completed whole-index
reconciliation fully validate a generation before atomically activating
accepted services; proposal, conflict, and removed services remain independent.
Activation records the exact search-generation digest, so even a same-source
shard replacement must pass validation and advance the service-state fence.
Reader replacement retires the prior generation only after its last active
lease releases. T34.4 owns public HTTP/MCP/UI registration and the shared All
code/service product surface.

Searcher startup records manifest/member identities before the synchronous
shared-directory open, captures the loaded repository inventory once, and
rechecks store/filesystem identity afterward. It does not hash the whole fleet
at startup. The first query for an unchanged startup generation lazily performs
one complete content/metadata validation. A repository published or changed
after process start never trusts the asynchronous watcher: queries bind a
static reader to exactly the manifest members for the rest of that process.
That cold exact bind deliberately reads every member twice: once for strict
publication validation, then again from the descriptor-stable mmap before it
can serve. This closes mixed-member, replacement-race, and watcher-delay false
negatives without a sleep; budget the bounded two-pass cold cost for a newly
published large repository.
At most two whole-repository fills run concurrently, and stable validation
failures retry with fingerprint-keyed exponential backoff from 250 ms to 30 s.
Member identity change retries immediately.

Warm query compilation checks the complete known file identity without
rehashing shard contents. JSON performs one final batched store/loaded-inventory
barrier before returning. SSE validates only repositories represented by
surviving file matches against their committed row, publication marker, and
manifest identity before emitting that event, then performs one complete final
barrier for no-match and out-of-band member changes. A stale, marker-covered,
mixed, missing, or invalid generation is an explicit search error, never a
successful empty result. The failed binding is retired so a repaired
publication can bind exactly on the next attempt.

The safety tradeoff is bounded overlap: a repository republished during this
process can have its exact static mapping open alongside the shared directory
reader's mapping. There is at most one cache-owned current exact generation per
repository, so steady state is roughly twice the current whole-shard mappings
and file descriptors. A rapid sequence of publications can transiently add
retired exact generations still held by active queries; those leases are
limited by the 10-second query budget and close on release. Shutdown cancels
and joins fills. Warm queries perform file-identity checks, not full-shard
SHA-256.

Missing pre-receipt publications are a one-time derived-state upgrade:
reconciliation clears their committed index claim and queues a forced
replacement. A prior-process marker is removed only when full receipt
validation proves that the store already committed those bytes. Reconciliation
also checks each declared member's decoded repository/revision metadata and
metadata digest locally; unreadable or mismatched metadata clears and
force-requeues only that repository. Search-time same-size content corruption
or another strict-binding failure requests a deduplicated forced index
replacement. The managed repository-hash basename—not decoded metadata—is the
sole ownership authority for current shards; metadata ownership applies only
to legacy noncanonical names. Cleanup can therefore remove an unreadable or
mixed-metadata target shard without selecting another repository's managed
path.
If a query reports `whole-repository generation changed` or cannot bind the
committed publication, inspect the forced `index_job`, let it complete, and
retry. Do not clear the repository row or rename shards manually. Whole shards
and receipts remain derived and excluded from backup.

#### Candidate planning and extraction admission

Every successful index now schedules `candidate_manifest_job` before
`extraction_job`. Under the repository work lock, the planner re-reads the
authoritative indexed HEAD and committed analysis-unit state, then consumes
one NUL-delimited `git ls-tree` stream. It records complete regular-file,
gitlink, and symlink counts/digests and versioned per-domain repository/unit
counts/digests, but writes rows only for candidates selected by the current
enumeration policies. A noncandidate path therefore changes corpus coverage
and manifest identity without consuming a retained row or becoming readable
to an extractor. Before that census, the planner rejects an external Git
object alternate and proves that the requested object is a commit. Every tree
leaf must have an explicitly supported Git mode; gitlinks remain census-only
repository boundaries even when their paths resemble an extractor candidate.
Each regular-file, gitlink, and symlink census dimension is capped at
10,000,000 entries on both build and open, so the planner cannot publish a
pointer that production extraction is structurally unable to admit.
The configured unit, focused-index descendant walk, planner, and extraction
reader also share the same 4,096-byte canonical repository-path validator, so
a focused publication cannot hand the planner an in-unit path it cannot
represent.

Repository rows are packed into canonical NDJSON members. For a focused
publication, candidate manifest v4 commits one explicitly
addressed projection for every local domain. Each projection contains exactly
that domain's in-unit repository records in canonical repository order; an
empty projection is represented by an explicit empty member list. Projection
members are named by the canonical policy ordinal and are limited in aggregate
to 16,384 artifacts and 4 GiB of canonical content. Caller rows are assigned by
`SHA-256("phebs-caller-path-v1\0" || UTF8(repository-relative-path))`.
Every ordinary repository, local-projection, and caller record also carries a
strictly path-derived `source_lane`: exact lowercase `_test.go` suffix is
`go_test`, even below generated, mock, fixture, or `testdata` paths; all other
ordinary candidates are `base`. Strict admission recomputes the lane and
rejects a missing or forged value before the record enters cross-plane
validation. Lane is a pure function of canonical path, so equal paths
necessarily agree and the external-merge projection does not carry a
redundant lane field.
Planning starts at two hash-prefix bits and recursively splits an over-limit
bucket by the next bit. Every member or nonempty leaf is limited to 4,096
records and 64 MiB of declared blob bytes; a larger singleton or a bucket that
cannot split at 256 bits fails the job. Blob OID and declared size contribute
to manifest identity but never move an unchanged path. The stable manifest
names every generation-digest-prefixed member, binds its exact content digest,
and commits the repository, HEAD, unit, policy, generation, corpus, domain,
and partition identities. Admission recomputes cross-plane identities with a
bounded external merge rather than retaining the path set in memory. Its
package-owned validation scratch is context-cancellable, removed before exact
publication membership is checked, and cleaned after a crash at startup.

The 4,096-record value remains the repository, caller, typed-input, and default
local maximum. A focused-local policy may commit a smaller bound as part of its
policy and generation identity; strict replay then enforces the same packing.
The provisional scale correction uses 2,048 only for `proto-contract` and
`thrift-contract`. Historical identities omit the optional field and retain
their exact 4,096-record packing.

The retained
[T30.4 prospective measurement](../../spike/t304/README.md) streamed 200,008
regular files into five repository rows and six caller rows. It produced three
two-bit caller leaves (`00:1`, `10:3`, `11:2`). The T30.6e policy refresh
retains the manifest-v4 focused-local projections; each run staged 12 files
totaling 24,967 bytes. Twice the final caller content bounds planner spool and
split scratch at 4,386 bytes; external-validation scratch is bounded at 3,514
bytes. Adding the larger phase bound to the final stage gives 29,353 bytes of
conservative peak candidate disk. The refresh runs took 3.61 s and 3.55 s,
peaked at 61,554,688 and 61,456,384 bytes RSS, and
reproduced byte-identical
output. The
frozen local planner gates are at most 10 s wall time, 256 MiB peak RSS, and 16
MiB peak candidate disk including publication plus the higher planner or
validation scratch phase; exceeding one refuses the prospective measurement
rather than relaxing the production partition contract. The 16 MiB value is
the neutral fixture's prospective disk gate, not the production schema's
independent 16,384-artifact/4 GiB focused-projection ceiling.

Publication stages and syncs the new bytes in `$DATA/candidates`, creates the
stable repository `.publishing` marker, replaces prior members, and renames
`phebs-candidate-<sha256(repository)>.manifest.json` last. One guarded
database transaction advances the matching pointer and ensures one pending
extraction successor; only then is the marker removed. A retry reuses the
identical generation and repairs missing fan-out. A second result for the same
HEAD, unit, and policy is an integrity error, not an alternate publication.
Filesystem bytes are reusable only when the exact valid database pointer names
their opened state or when the stable marker proves an interrupted
filesystem-before-database transition. Unmarked bytes with no valid pointer
are re-censused and replaced even if their manifest is internally consistent;
an orphan or forged generation cannot bootstrap its own authority.
On a candidate-v4 upgrade, startup compares each indexed, non-deleting
repository's pointer with the current complete policy digest before candidate
runners begin. A v3 pointer is cleared and its deduplicated pending candidate
job is upgraded to `force`; failure to clear or enqueue aborts startup. An
unindexed repository has no candidate work to reconcile, and a deleting
repository remains owned by its deletion flow; either repository self-heals
through that next transition rather than this startup pass. The replacement
re-censuses the same authoritative HEAD/unit and normal publication cleanup
removes the retired v3 members. Do not preserve a v3 pointer by renaming its
artifacts or disabling the backfill.
This reconciliation adds one indexed candidate-pointer point read per live
indexed repository after the existing repository list; it reads or hashes no
candidate artifact.

At steady state, the exact persisted pointer, an absent marker, and its present
regular manifest control file can prove that no publication bytes need to be
consumed. The candidate job uses that identity-only path to repair the guarded
extraction fan-out without rehashing or externally sorting every member. The
extraction job resolves the pointer's manifest, policy, and control identity
before taking the repository mirror lock and returns when every enabled domain
has a settled exact-generation outcome. A forced or stale domain takes the
lock, reloads current state, then strictly opens the manifest and members once
before any run begins. Marker recovery keeps the marker installed throughout
strict validation and removes it only after the matching database transition;
a second crash therefore remains fail-closed. A missing or malformed pointer
and post-restore reconstruction always take the strict validation/rebuild
path. This shortcut grants no artifact access: actual candidate replay still
revalidates the bound regular descriptor, record limits, exact digest, and
marker absence.

When extraction must strictly open a focused candidate publication, it scans
the repository members exactly once while reconstructing the expected local
projection envelopes, then independently validates each declared projection.
If `B_repository` is the canonical repository-member content, `C_caller` is
the unchanged caller-leaf content, and `P_d` is one local domain's projection,
strict-open I/O is `B_repository + C_caller + ΣP`; subsequent stale
local-domain replay reads only `P_d`. Adding local domains therefore does not
multiply repository-wide reads. Repository and caller planes retain their
existing views, and target-bound caller overlay remains owned by T30.6.

After a cold retry, the candidate worker also keeps a process-local control
fingerprint over the persisted manifest digest and each manifest/member inode,
mode, size, and modification time. Warm retries compare only those identities.
Observed drift forces one strict publication open and rebuilds on failure, so
ordinary unmarked runtime damage re-enters the repair loop without restoring
per-tick member hashing. This fingerprint is not cryptographic validation:
preexisting content damage that preserves every observed metadata field is
still refused when strict extraction consumption checks the member digest.
The persisted pointer also carries a nonzero control revision. An exact
ordinary retry preserves it. A typed descriptor/integrity refusal records a
terminal control outcome and force-enqueues candidate repair in the same
transaction; a successful strict repair advances the control revision even
when rebuilt bytes produce the same semantic manifest digest, clears only the
matching old control outcome, and ensures exactly one extraction successor.
The repair lookup matches repository, commit, unit, manifest, policy, and
revision, so an outcome from a retired scope cannot trigger repair of the
current pointer.

Before creating an extraction attempt, the worker refuses a publication
marker, stale database pointer, HEAD/unit/policy disagreement, malformed or
noncanonical JSON/NDJSON, partial/extra/duplicate member, digest mismatch,
overlapping or unordered caller leaf, or special filesystem entry. Local
contract, field, topic, consumer, attribution, and Workbench implementation
domains consume only records whose manifest membership is inside the committed
unit. Caller-plane domains remain visibly `repository-overlay` input. Their
independently complete T30.6 caller generations may cite outside-unit callers,
but those rows do not become focused Search or local implementation evidence.

The candidate manifest carries a designated typed-input envelope separately
from ordinary source rows. For a focused repository, `typed_index.kind: scip`
binds the exact configured supporting path, blob identity, size, consuming
domains, commit, unit, and candidate generation. Readers cite that real path:
they never invent `index.scip`, fall back to a root artifact, or admit a SCIP
document outside the unit. A focused unit with no designation reports a typed
input gap. Whole-repository repositories retain the legacy root lookup.

For a committed non-empty unit, local evidence consumes only candidate records
whose source lane is `base`; `go_test` records are rejected before an ordinary
blob open. Focused SCIP field readers still stream and globally safety-account
the complete typed artifact once, but discard exact `_test.go` documents and
all of their definitions, anchors/ranges, occurrences, and joins before
ordinary source reads or fact emission. Empty-unit whole-repository extraction
ignores the lane and retains shipped behavior. Search is independent: every
test file admitted by the focused unit remains searchable.

##### Extraction operation receipts

Every repository extraction job emits one JSON log entry prefixed
`extraction operation:` with schema `phebs-extraction-operation-v1`. Treat it
as a bounded operational diagnostic, not durable evidence. The job envelope
names the canonical repository, indexed HEAD, committed unit digest,
candidate-manifest and candidate-policy digests, and queue attempt. It records
queue wait from the attempt's eligibility time (the later of creation or
`not_before`), repository-lock wait, pointer-only preflight, and strict
candidate-publication open once per job instead of copying those shared costs
into each domain.

Nested domain entries contain only durations for inventory, opened source,
extractor, staging, publication, abort, and cleanup; aggregate counts and
bytes already observed by those operations; the existing corpus/read/blob/
typed-input/fact limits; and one generic reason:
`already_current`, `not_ready`, `stale`, `no_candidates`,
`typed_input_absent`, `limit_refusal`, `published_empty`,
`published_nonempty`, `aggregate_budget`, `domain_budget`, `canceled`, or
`failed`. Scheduler fields additionally freeze the aggregate, mirror,
per-domain, abort, and outcome-persistence time bounds; maximum serial domains
and retained scheduling identity; aggregate/domain staged-row limits; and the
exact association-plus-assertion rows staged by that domain. They never contain a source
path, source content, path sample, or raw extractor diagnostic. Use the
ordinary domain log immediately preceding the receipt when a raw local
diagnostic is required for troubleshooting.

For focused local evidence, the counts also disclose ordinary source files
excluded by lane and their aggregate declared bytes. SCIP field domains add
excluded typed-document, definition, and occurrence counts. The same scalar
fields enter the bounded durable domain receipt and authoritative coverage;
they are accounting, not a second evidence lane.

Phase durations are not additive. `extractor_ms` is the inclusive wall time of
the extractor call, so it contains `opened_source_ms` and any `staging_ms`
incurred by reads and emitted fact chunks during that call. Use each duration
to locate where time was spent; do not sum sibling fields to reconstruct
`total_ms`.

The complete report is capped at 64 KiB inclusive. An over-cap report becomes
one deterministic identity-only envelope with `truncated: true` and
`domain_count`; it carries no nested domains. Encoding failure, log/sink
failure, or sink panic cannot change extraction, publication, abort, or retry
disposition. Timings are never a freshness, cursor, proof, publication, or
evidence identity. Receipt collection observes existing transitions and
in-memory counters only: it performs no extra corpus pass, candidate/member
hash, publication open, or blob read.

Prometheus exposes `phebs_extraction_operations_total` and
`phebs_extraction_operation_duration_seconds` without labels.
The duration histogram's exponential finite buckets span 100 ms through about
27.3 minutes, covering the 15-minute extraction budget.
`phebs_extraction_operation_domains_total` has only the frozen generic
`reason` label. Repository, extractor, commit, unit, manifest, and policy are
deliberately absent from metric labels; use the bounded log receipt for those
identities.

##### Durable extraction outcomes and retry

The operation log above is not retry authority. The database stores one
latest-only outcome per repository/domain for the exact current generation.
The frozen dispositions are `published`, `unavailable_prerequisite`,
`terminal_generation_refusal`, and `retryable_failure`. Their
`phebs-extraction-domain-outcome-v1` receipt is separate from the job log,
source-free, capped at 8 KiB, and limited to work known before the store
transition: domain phases, bounded counts/bytes/limits, disposition, and
generic reason.

The exact generation binds repository, indexed HEAD, committed unit digest,
candidate manifest/policy/control revision, extractor generation, inventory
policy, typed-input identity and presence, and dependent scope inputs.
`published` commits in the same transaction that publishes and supersedes
evidence. Other outcomes never retire an older publication, but that older
publication remains readable only while the existing exact freshness fences
still match. A current settled generation short-circuits after restart.
Ordinary or forced work does not rerun the same unavailable or terminal
generation; force may deliberately rerun a published generation. Retryable or
missing outcomes run again.

For a focused unit, an applicable but absent designated SCIP input records
`unavailable_prerequisite` before an extraction run is staged. The same
generation remains settled until the typed-input identity or another
generation input changes. Whole-repository missing-SCIP extraction retains its
legacy empty-publication behavior.

If extraction keeps retrying, inspect the ordinary classified error rather
than parsing either receipt: temporary store, lease, cancellation, and
untyped extractor failures are retryable. Stable typed limit and candidate
descriptor/integrity refusals are terminal for their exact generation.
Transient candidate-manifest filesystem failures are job-level retryable
errors: they do not overwrite each domain's settled ledger. A recorded
terminal refusal fails the queue job immediately rather than consuming the
ordinary retry allowance or being reported as a successful job.
Changing commit, unit, candidate publication, extractor/inventory policy,
typed input, dependency identity, or control revision makes the old row
ineligible automatically; do not delete outcome rows by hand. Candidate
control damage should converge through the forced strict repair described
above. Repository deletion and committed scope cleanup remove its bounded
latest rows.

At steady state, extraction adds one indexed latest-row lookup per configured
domain. A settled no-op performs no corpus walk, candidate/member hash,
publication open, blob read, or evidence write. Outcome storage is bounded to
one row per repository/domain and one receipt of at most 8 KiB.

##### Aggregate domain scheduling

The extraction budget begins only after the repository mirror is locked.
Every job has one absolute 15-minute post-lock deadline and releases the mirror
by one absolute 14-minute-50-second deadline. Domains remain serial and each
receives at most five minutes, clipped to the time left in those enclosing
bounds. A retry or later domain never receives a replacement aggregate
deadline. The scheduler reserves five seconds for detached abort, five seconds
for a durable outcome before mirror expiry, and ten seconds after mirror
release for durable deferral outcomes. Work needs at least one second of
remaining domain-work time to start.

One job admits at most 16 configured domains, retains at most 64 KiB of
scheduling identity, stages at most 100,000 association-plus-assertion rows in
aggregate, and permits at most 25,000 such rows in one domain. These are fixed
production ceilings, not configuration knobs. The effective domain row cap is
also clipped to the aggregate allowance left when it starts. At the aggregate
maximum, the same fact chunks carry at most 50,000 content-keyed atom upsert
inputs. Do not raise the existing
corpus, path, blob, read, fact, or scheduler limits to mask a deterministic
refusal.

After strict admission, the worker resolves every configured domain's exact
outcome. A current settled generation is skipped. Never-attempted generations
run first in registry order; current retryable generations then run by oldest
persisted attempt and registry order. Consequently, a slow retryable domain
cannot start twice while a configured peer remains untried. A successor job
runs only remaining retryables after its peers settle. Work that cannot start
records `retryable_failure` with reason `aggregate_budget` after the mirror is
released; if it previously started, the outcome preserves that run identity
and therefore its ordering age. A started domain that exhausts its own time or
staged-row allowance records `domain_budget`. Prior published evidence and
terminal or unavailable peer outcomes are not erased.

When a bounded job durably settles a domain or creates a new attempt identity
and defers a never-attempted peer, the queue releases that job for immediate
continuation without consuming its failure-attempt allowance. This continues
only until every configured current generation has an attempt identity. A
zero-progress execution, a retry before run creation, subsequent retryable
failures, and deterministic scheduler-admission refusals such as an oversized
retained identity use the ordinary retry cap.

Candidate inventory remains the pre-run admission gate: malformed manifest
membership creates no staged run. After admission, the run attempt marker
precedes extractor execution. A failed run is aborted with a detached bounded
context; its rows remain invisible, and an interrupted abort leaves them to
the existing stale-run sweeper.

##### Completed T30.6 operating sequence

The accepted large-monorepo review now has its bounded operational, outcome,
scheduler, source-lane, resolver, caller-leaf, publication, exact-consumer,
and retention/status seams through T30.6r. An admitted `*_test.go` file remains
searchable and participates in candidate planning when an enabled domain
policy enumerates it, while direct
caller execution excludes its `go_test` lane. The public Caller Map now reads
only the exact complete repository-overlay authority; caller comparison now
uses that exact authority in one jointly fenced two-endpoint read, and
Workbench Impact now composes the same exact single- or two-generation
authority through its current Revision and final Investigation fence.
Operators should not raise the global file, path-byte, read-byte, fact, or
single-run deadline limits to work around that refusal. Configure the smallest
truthful analysis unit and, when required, its exact typed input; preserve the
failure diagnostic and wait for the target-bound caller generation.

T30.6a supplies the bounded job report described above, T30.6b makes exact
terminal and retryable outcomes durable, and T30.6c schedules them beneath
independent per-domain and aggregate job/lock bounds. T30.6d advances
candidate identity with `source_lane: base|go_test`; T30.6e now consumes
`base` only for focused local evidence while safety-accounting the complete
typed SCIP artifact before removing exact test documents' definitions,
anchors, occurrences, and joins. Coverage and bounded receipts expose the
excluded counts and declared bytes. Empty-unit repositories retain shipped
whole-repository extraction behavior, and focused search remains unchanged.
T30.6f supplies the catalog lifecycle, T30.6g supplies the ordered bounded
gRPC/Thrift materialization, T30.6h supplies the independently durable direct
caller-leaf artifacts, and T30.6i supplies the atomic complete publication and
recovery lifecycle described above. T30.6j supplies the authorized exact Caller
Map reader, revision-bound cursor, and exact-range citation path described
above. T30.6k supplies the exact two-sided comparison described above. T30.6l
supplies the exact Workbench composition, completed-stream confirmation, typed
gaps, citation-only caller source access, and deterministic checklist identity
described above. T30.6m explicitly selects unbounded historical-publication
retention without changing cleanup. T30.6n bounds job-history reads and
repairs startup migration without deleting history. T30.6o now supplies the
authorization-first retention-status shell, fixed registry, budgets, and
unconditional capacity warning. The core collector now populates 23 SurrealDB
components, T30.6q now populates the 24 Investigation/Workbench table
components, and T30.6r now adds the final seven derived filesystem/store
collectors plus installation capacity. T40.7 adds `evidence_chunk` and T40.10
adds `extraction_domain_root` beneath the evidence-publication owner, expanding
the declared surface to 54 components.

##### Scope-aware result diagnostics

T30.7 makes the existing authority visible next to product results. Search,
Contracts, Topics, Caller Map, Impact, and Workbench now identify the active
unit by name and disclose its exact primary and supporting paths. A focused
repository has two deliberately different planes:

- `focused` Search and `focused-local` declaration, topic, field, and
  implementation evidence read only the selected unit;
- `repository-overlay` Caller Map rows read the independently complete caller
  generation and may therefore cite a caller outside the focused shard.

Search may reuse the current `/api/repo-status` analysis unit only when the
result set names one revision equal to that status row's
`indexed_commit_hash`. Explicit historical or mixed revisions, a missing
result/indexed revision, and index/status transition skew produce a typed
scope gap; the UI never relabels those results with the current unit digest or
paths.

Do not interpret an outside-unit caller as a Search leak or a widening of
local evidence. The panel labels the plane before its rows. Exact paths are
configuration identities, not source content; the UI mounts at most 24
repository summaries and only one expanded path set at a time. MCP
`list_repos` returns the same committed `analysis_unit` projection as
`/api/repo-status`, while the MCP proof and Caller Map tools reuse the same
coverage/page schemas as HTTP.

Coverage certificates now use `coverage-certificate-v3`. Each domain row may
include the latest durable disposition and either a validated full bounded
receipt or `receipt_state: schema_only`. The latter is the writer's deliberate
8-KiB fallback and means counts/timings are unavailable; it is not a zero-work
receipt. A full receipt exposes generic reason, scalar phase times, counts,
bytes, and limits only. Focused-local candidate scope also shows exact
base-source and excluded `go_test` counts plus typed-input posture. In a v3
certificate both lane fields are required, including explicit zeroes, exactly
for `focused-local` evidence over a `local` candidate plane; any other
posture/plane must omit both. Retained v1/v2 proof bundles keep their original
version-aware canonical shape. Whole-repository and repository-overlay
coverage omit those two lane counts because their retained coverage does not
separate admitted test records. A stale publication,
unavailable prerequisite, retryable failure, or terminal refusal remains
visibly distinct from an exact fresh publication.

The disposition, not the receipt reason, is authoritative for publication.
When extraction and staging finish but publication fails transiently, a
`retryable_failure` may retain `published_nonempty`, `published_empty`,
`no_candidates`, or `typed_input_absent` as the bounded description of work
already completed. A terminal refusal reached through a mid-run budget stop
joined with an abort failure keeps its `aggregate_budget` or `domain_budget`
reason. A receipt recorded from T30.6c until the excluded-source/SCIP
counters existed keeps its exact recorded bytes and is disclosed as
`legacy_exclusion_shape` instead of being re-encoded or rejected (the
one-commit T30.6b receipt shape, which also predates nine limits fields,
remains out of scope), and a
candidate-manifest census is valid up to the candidate corpus ceiling even
above the walked per-run corpus limit frozen in the receipt's own limits
block; candidate plus excluded records remain bounded by that walked limit.
Retained v1 proof bundles predate the candidate-scope object, while retained
v2 bundles keep their original canonical candidate-scope JSON, which omitted
the six v3 candidate/exclusion counters. When a new v3 certificate emits
`candidate_scope`, that object includes all six counters, including exact
zeroes. Direct-corpus coverage may omit the candidate-scope object entirely.

A current complete caller generation reports its validated record counts and
`N/N` succeeded partitions without another database query. Before publication,
the product may report a server-side scalar count over durable leaf outcomes.
When aggregate admission has fixed the denominator it reports `N/N`; before
that it reports `N/?`. It never serializes partial caller rows or turns an
unknown total into zero. The initial pointerless read and final result fence
each execute one indexed aggregate over at most 16,384 outcome rows and return
one scalar row; a progress or admission transition produces `409`. Exact unit
paths are not copied into signed authorities: tokens carry a fixed scope
digest and the final authorization read recomputes it.

`GET /api/caller-generation-progress?repository=...` exposes the same exact
generation control without selecting a contract endpoint. Use it for
operational convergence checks when a repository legitimately has no matching
operation declaration; the declaration-bound Caller Map continues to return
404 for that endpoint. The progress response is capped at 32 KiB and carries
only generation identity/state, aggregate scalars, bounded partition
progress/refusals, and digest/count analysis-scope authority; it never repeats
the focused unit's selected paths. It authorizes before publication I/O,
shares Caller Map's eight-read semaphore, and rechecks current or unavailable
publication state plus repository visibility, revision, and scope before
returning. Compared with the replaced missing-declaration probe, a poll keeps
the same bounded publication open and avoids declaration assertion/resolution
lookup, while deliberately adding the final `CallerReader.Current` check and
repository visibility, revision, and scope reauthorization that the early 404
never reached. It performs no write, queue operation, corpus or
Git read, child process, cache fill, or startup/sync work.

The relationship worker admits one current Kafka topic-posting member with at
most 65,536 postings and charges at most 160 MiB while retaining Kafka
postings. The historical 50,000-posting policy remains a valid exact read
policy; the root's embedded policy and authority digest must agree, so the
larger current policy does not rewrite or relabel older generations. The
current component envelopes total 992 MiB within the unchanged one-GiB
relationship worker class: 128 MiB resolver, 192 MiB RPC, 160 MiB Kafka, and
512 MiB final relationship projection. A resident-limit overrun in the
resolver, RPC, or Kafka component build is durably classified under its owning
stage (`relationship_resolver_namespaces`, `relationship_rpc_postings`, or
`relationship_kafka_projection`) with `relationship` / `resident_bytes`
scalars; another deterministic component bound remains a closed unknown
refusal at the same stage. All are terminal schedule results and do not
consume the remaining five-attempt allowance. New partitioned relationship
schedule bindings include
one digest over the resolver, RPC, Kafka, and final relationship policies plus
their three resident fences. A repeated reconcile for the same authority and
same build policy retains a closed terminal schedule only under a v3 binding,
whose target embeds the build-policy digest; older bindings keep the ordinary
recovery path so a raised bound can still rebuild them. Changing any bound or
frozen builder policy changes the v3 target and creates the ordinary recovery
schedule. Historical v2 bindings remain valid, an active pre-upgrade v2
schedule is allowed to finish rather than being superseded mid-flight, and a
v3 binding recorded under a prior build policy stays readable — recovery flows
through the target mismatch instead of invalidating in-flight work.
The terminal no-op performs two current-schedule reads, one binding-file read,
and one final source-free failure-row read only when a publication-triggered reconcile
revisits that exact failed target; it adds no periodic scan or member read.
A noncurrent relationship-plan transition and each v3 binding validation hash
one bounded in-memory object containing four frozen policies and three resident
scalars. A current-root no-op returns before that hash. The operation performs
no source, member, shard, or store scan and retains no cache.

When the ceremony sees no current relationship root, it consults only the
current `service-relationship` schedule through a read-only connection to the
already supervised local store. No schedule or an active schedule remains
pending. A settled failed schedule becomes an immediate typed terminal result.
A settled successful schedule triggers one immediate confirming root open: a
now-readable root is the harmless publish/settle read race and stays pending
for the next full inspection, while a still-missing root is terminal rather
than polling to the ceremony deadline. The recorded refusal's own
observed/limit scalars substantiate a bound classification even when the
compiled-in fence has since changed. The projection excludes repository identity, stage,
timestamps, worker/lease state, and raw error text; a closed final refusal is
the only failure detail. Each missing-root poll opens and closes one local
connection, reads one current schedule, and confirms the runtime. Every settled
schedule adds one current-schedule recheck; a failed settled schedule also
reads one bounded final failure. A present relationship root performs none of
this schedule work.

The complete publication repeats every candidate leaf once per enabled caller
domain. Product record counts therefore census each immutable leaf ordinal
once, verify that the repeated leaf envelope and candidate/excluded-`go_test`
counts agree across domains, and reject an inconsistent publication instead
of multiplying its counts. Comparison uses the same census. This remains one
in-memory pass over at most 16,384 already-loaded pair rows and retains at most
one bounded census entry per leaf; it adds no database query.

Queue attempts, claimants, leases, mirror-lock timing, and per-job scheduling
remain operational log/metrics diagnostics. They are intentionally absent
from these result panels. Use the extraction-operation log and ordinary
classified worker error when a product state needs queue-level diagnosis.
In steady state the certificate adds one indexed latest-outcome lookup per
visible repository/domain each time it is built; consumers that already
perform a final certificate rebuild repeat that lookup. If publication or a
split failure transition lands between the run, outcome, and attempt reads,
the builder retries an identity mismatch at most twice more and otherwise
returns one real pre/post-transition state rather than a mixed receipt. Search
reuses its existing status request. No T30.7 result path adds a corpus/shard scan, Git/blob read,
content hash, mirror/owner lock, child process, startup scan, sync-tick work,
write, or retained cache.

This sequence does not authorize a physical Go-test search overlay, optional
test evidence, test-to-source association, build-system discovery, SCIP
generation, pack-specific recognizer expansion, or per-file parser degradation.
Neither the private evaluation report nor any identifier, path, measurement,
or code copied from it is committed or used as merge-bar evidence. Production
T30.6a receipts contain the canonical repository identity already required by
store state but retain no source path, source content, path sample, or raw
extractor diagnostic. Later T30.6 merge-bar fixtures and new measurements
remain neutral and generated.

#### Focused evidence publication and recovery

Every extraction attempt, run, and current-publication pointer is keyed by the
exact `(repository, indexed HEAD commit, unit digest, domain)` tuple. The unit
digest is empty only for whole-repository posture. Publication rechecks both
the repository's indexed commit and committed unit digest in the same guarded
transaction that makes the run visible. A same-HEAD unit edit, stale candidate
job, failed replacement, or rollback therefore cannot publish into or read
another scope. Re-running the identical tuple replaces only that tuple; other
commit/unit publications remain available for exact rollback and retained
proof references.

Exact older commit/unit/domain publications remain `published`, so the current
evidence sweep does not collect them. T30.6m deliberately retains that
historical posture without a count, age, or byte eviction policy. Database use
can therefore grow with historical focused publications and exact-scope
attempts. Do not delete those rows by hand: pinned proof may reference them,
shared atoms may serve another run, and physical database allocation is not
the sum of logical evidence rows.

#### Historical-publication retention

The selected historical-publication policy is **unbounded**. It is an explicit
compatibility and proof-preservation choice, not a claim that growth is
harmless, and T30.6m changes no cleanup behavior. No neutral measurement
establishes a safe destructive rollback depth, and keeping a fixed number of
generations would not establish a physical byte bound across variable-size
evidence, durable pins, shared atoms, caller leases, and the separate
database/filesystem owners.

That decision also preserves the modeled candidate and caller publication
residue without adding cleanup, but it must not be read as approving every
other append-only or unreclaimed table. Durable terminal job history and the
Investigation/Workbench domain are incidental operational/history owners whose
growth was missing from the first T30.6m inventory. Immutable proof bundles are
a separate configured lifecycle that defaults disabled. Adding those three
groups to the inventory authorizes no deletion and does not weaken the
historical-publication decision.

Use these terms when diagnosing storage:

- a historical evidence scope is a published exact
  `(repository, commit, unit digest, domain)` scope that is not the complete
  live current scope;
- current means equality with all live repository, unit, policy, candidate,
  pointer, and publication fences, not newest time or `status=published`;
- a durable pin is any store-accepted `evidence_pin` retention owner—including
  proof, checkpoint, and Investigation state—and is restored with the database;
  proof pins are indefinite by default but have the separate configured
  lifecycle described below, while other pins follow their own owner lifecycle;
- an active lease is a process-local caller-publication file-lifetime guard,
  not a durable history pin;
- a backup is an external immutable snapshot and never a pin on the live
  installation.

The owner behavior is intentionally asymmetric:

| Owner | Live retention | Backup/restore behavior |
| --- | --- | --- |
| evidence publications | historical published and pinned-superseded runs remain; quarantined runs require administrator resolution; sweep-eligible and in-progress backlog remains until bounded maintenance drains it; the v1 status reports bounded aggregate physical-row totals for runs, associations, assertions, and distinct shared atoms without computing a lifecycle partition | restore retains the graph and every pin |
| extraction attempts | exact-scope attempts accumulate | restore retains attempts |
| extraction outcomes | latest-only per repository/domain; each logical diagnostic receipt is capped at 8 KiB | restore imports outcomes, then selectively clears candidate-control outcomes with candidate authority |
| evidence pins | pin rows and the superseded evidence they protect accumulate according to their owner lifecycle; proof pins are indefinite when `proof_bundles.retention` is omitted or `"0"` | restore retains every pin kind; only its owning lifecycle may release it |
| proof bundles | content-derived `proof_bundle` rows have no count or aggregate-byte ceiling; each canonical content value is capped at 64 MiB and each repository/run-id list at 10,000 entries; omission or `"0"` retention keeps the row and exact proof pins indefinitely, while a positive lifetime makes them sweep-eligible by age from latest materialization | database export and restore retain bundle rows and pins; configured maintenance resumes after startup |
| durable job history (8 tables) | `connection_sync_job`, `indexing_job`, `repo_fetch_job`, `extraction_job`, `candidate_manifest_job`, `resolver_catalog_job`, `caller_leaf_job`, and `investigation_run_job` retain terminal rows without a deletion path; pending-key coalescing bounds pending work only | database export and restore retain job history; repository removal does not delete terminal rows |
| Investigation/Workbench domain (24 tables) | append-only or lifecycle-owned core, Revision/Brief, Workbench, Run/artifact, retention-owner, decision/disposition, access, review, Dossier, and Watch rows have no wired domain-wide sweep | database export and restore retain these rows and their evidence pins |
| candidate artifacts | authority is current-only, but failed partial generation files can accumulate without a root entry/byte cap until a later successful cleanup or repository removal | excluded and rebuilt |
| focused indexes | current and active-reader transition only | filesystem discovery may archive validated marker-free physical publications; restore re-fences authority, and whole indexes otherwise rebuild |
| resolver catalogs | current and bounded replacement transition plus package-owned stages/residue; top-level installation-root inventory refuses after its enforced 32,768-entry operational scan threshold, but that is not a storage ceiling; the 1,034 MiB clean-replacement figure is only a design model that excludes prior-process stages and undeclared residue | filesystem discovery may archive validated marker-free physical publications, then authority is cleared and re-fenced |
| caller rows | current generation-publication pointers plus generation admissions and pair outcomes are retained in the live database; admissions and outcomes accumulate across generations | exported pointers, admissions, and outcomes are deliberately cleared on restore before reconstruction |
| caller artifacts | current complete bytes and successful incomplete-generation residue accumulate; active leases delay current transition cleanup | validated unambiguous marker-free complete bytes may be archived, but historical coverage is not promised; incomplete residue is omitted and restore clears authority |

The retained job-growth receipt isolates the default resync rate without
inventing a failure horizon. With one healthy, continuously draining remote
connection and the default one-hour interval, 100 repositories produce
876,000 `indexing_job` rows per 365-day common year, plus 8,760
`connection_sync_job` rows. Pending coalescing may reduce that rate when work
does not drain; downstream candidate, extraction, resolver, caller, and
Investigation job rates are intentionally not summed. No measurement
establishes a days-to-weeks degradation threshold.

The latest failed-replacement diagnostic is owner-specific. Evidence retains
the live exact-scope attempt and latest-only domain outcome without displacing
the prior publication. Caller terminal admissions/outcomes remain derived live
state; restore clears them and rebuilds current work rather than claiming they
are archived history. Restore imports and migrates precious evidence and pins
before normal server maintenance starts, then re-fences the derived candidate,
resolver, and caller planes. T30.6m adds no restore-time sweep.

For exact component accounting, the 24-table Investigation/Workbench group is
`investigation`, `investigation_revision`, `investigation_change_brief`,
`investigation_workbench_mutation`, `investigation_workbench_disposition`,
`investigation_run`, `investigation_run_event`,
`investigation_run_artifact`, `investigation_artifact_owner`,
`investigation_artifact_owner_release`,
`investigation_artifact_retention_override`, `investigation_decision`,
`investigation_disposition`, `investigation_baseline_designation`,
`investigation_grant`, `investigation_cursor`, `investigation_creation`,
`investigation_consumer_snapshot`, `investigation_consumer_edge_ledger`,
`investigation_review_projection`, `investigation_review_item`,
`investigation_dossier`, `investigation_watch`, and
`investigation_watch_revision`. Grouping these rows in operator output must not
permit a component to disappear silently.

T30.6n bounds both job-history consumers without deleting or compacting a job
row. Internal history reads are record-ID-keyset pages: one call returns and
materializes at most 257 physical-ID-ordered rows and at most 100 rows matching
its optional status. Continuations use an exclusive record-range seek, not an
ID predicate that filters again from the first table key.
Because status filtering happens after the fixed physical scan, a sparse
filter can return an empty page with a continuation cursor. Keep following that
cursor; an empty page does not mean that no later matching diagnostic exists.
Each returned row contains only the declared diagnostic fields. Target, error,
and claimant are capped at 1,024, 2,048, and 256 Unicode characters and carry
independent truncation flags; the lease token is authority, not a diagnostic,
and is omitted. The durable row is not rewritten. This shape avoids adding
eight historical compound indexes during upgrade. Pagination is weakly
consistent with concurrent random-ID inserts: it never rescans behind its
physical cursor, so use it as a bounded diagnostic traversal rather than a
frozen snapshot.

`/api/repo-status` no longer reads `indexing_job` history. It scans current
repository and membership rows and dereferences at most one prospective job
record link per current repository. Supported indexing-job writer transactions
install that link atomically when they create a row; a job created through
those writers after this upgrade is exact,
and later claim, heartbeat, retry, and terminal transitions remain visible
through the same link. A repository whose latest job predates the projection,
was deleted and recreated under the same name, or otherwise has no current
link reports `last_index_job_state: "unavailable"`; neither the API nor the UI
calls that state “never indexed,” counts it as idle, or enables its per-row
reindex action. Its next supported indexing-job writer transaction establishes
exact state for the current repository incarnation. No retrospective terminal-
history backfill runs, and no table event replays projection work while retained
job rows are restored.

The first supported upgraded store open reads only `pending`, `claimed`, and
`running` rows through each of the eight status indexes, with a 131,072-active-
row installation safety/refusal bound per table. That number is not a claimed
queue-cardinality ceiling or retention bound. It preserves oldest-pending
coalescing and active lease recovery, verifies the already-shipped pending-key
indexes, then records one versioned completion marker. An interrupted repair
safely resumes because every transition is status-fenced; later opens read
only that marker. A nonempty table missing its required pending-key index, or
an unsupported store above the safety bound, refuses instead of silently
scanning or indexing lifetime terminal history. Database export and restore
retain terminal rows and exact diagnostics; the ordinary raw database backup
also retains the job projection and completion fence without one projection
event per restored indexing-job row. Stale-job reaping reads
only `claimed` and `running` rows through the status index, performs no server-
side sort, and returns or mutates at most 256 stale rows per poll; later polls
drain further batches. Its index scan is `O(current active jobs)`, not
`O(terminal history)`, while Go allocation and mutation count stay fixed. This
establishes no job-retention bound and adds no TTL, deletion, or retention
configuration.

T30.6o adds administrator-only `GET /api/retention-status` as an
authorization-first shell. Authorization completes before any component store,
filesystem, or cache touch; denial consumes none of the component-scan budget.
The `phebs-retention-status-v1` shell exposes all twelve owner groups and all 53
declared components in their exact order. T40.7 added `evidence_chunk` to the
original 52-component registry because its durable receipts are a separately
retained and separately swept physical population. The aggregate fixed-work allocation,
at-most-4,096 reported identities after at most one 4,097th sentinel per
summary, a 64 KiB encoded response, and independent `exact`, `lower_bound`, or
`unavailable` labels for counts and typed byte metrics. Each component's
ordered `byte_metrics` array declares one or more of `logical_encoded`,
`canonical_content`, `canonical_receipt`, `apparent_file`, and
`physical_database`. Those kinds describe different accounting planes, may
coexist on one component, and must never be summed. Endpoint-wide
work is not 4,097 multiplied by 53: 4,096 report slots are split fairly, with
78 reserved for each of the first 15 components and 77 for each of the last 38,
plus one private sentinel each for a 4,149-scan aggregate. The zero-inventory
shell fixture is 20,256 bytes, and the maximum-shaped fixed envelope is
21,239 bytes. The earlier fixtures were not maximal after the additive
component: an
unavailable count with its full scan allocation encodes three additional bytes
per component. A live completed response is not fixed at the shell-fixture size:
its observed counts, typed byte metrics, and data-volume digit widths vary, and
the encoder independently enforces the 64-KiB ceiling. At the T30.6o boundary
every metric was `unavailable` and the shell performed zero store, filesystem,
or cache inventory scans. Registry presence therefore proves coverage of the
declared model, not that T30.6o alone inventories retained capacity.

For each component, an `exact` count must equal the identities scanned below
its report allocation. A non-truncated `lower_bound` must equal a nonempty
partial scan, while a full cap-plus-one scan is the sole shape that sets
`truncated: true` and reports the allocation ceiling. An unavailable count may
retain a partial scan counter but cannot claim truncation.

Owner metadata makes the one existing scoped control explicit. Only
`proof_bundles` carries a non-null `retention_control`:
`proof_bundles.retention`. `default_state` is `disabled` when its effective
lifetime is zero and `enabled` when positive; the owner `accumulating` flag is
the inverse. A positive lifetime deletes the expired bundle and exactly its
`proof-bundle:<bundle_id>` evidence pins but no extraction evidence; the
independent evidence sweep may later reclaim newly unpinned superseded
evidence when otherwise eligible. Every other owner encodes null. This is
disclosure of an existing lifecycle, not a new cross-owner retention control.

The core collector populates 22 SurrealDB components: five evidence-publication
graph components—including `evidence_chunk` receipts—extraction attempts,
extraction outcomes, three logical
evidence-pin namespaces, proof bundles, all eight durable job tables, and the
three caller-row tables. The v1 wire reports one aggregate bounded row count
per table or pin namespace; it does not expose or compute lifecycle or
job-status partitions. Every retained physical row contributes regardless of
its state. The evidence collector counts shared `evidence_atom` rows directly
instead of multiplying an atom by the number of associations that reference it.

Each component keeps its non-transferable shell allocation. Registry indices
0–14 receive 78 report slots plus one sentinel; every later component receives
77 plus one. A complete core request can therefore scan at most 1,731 component
identities. That 78/77 placement belongs to the API registry. The store accepts any report
allocation from 1 through 79 only when scan is exactly report plus one and
independently enforces the 1,709-report/1,731-scan aggregate ceilings.
The implementation produces 22 component summaries using at most 24 bounded
row-range queries: the `other` pin namespace is the complement of two reserved
prefixes and therefore uses as many as three disjoint index ranges. Collection
follows four cached writer/migration-marker point checks and one required
pin-index catalog check. Every one-statement readiness, catalog, and component
query must return exactly one SurrealDB result envelope; zero or multiple
envelopes are failures rather than empty tables. The three pin namespaces use
the existing kind index and fixed ranges without sharing or borrowing one
another's allocations. The existing schema batch defines `evidence_pin.kind`
as a scalar string, so an array-shaped value cannot undermine those ranges. A
failed readiness check or component query leaves only the affected readiness
group or component `unavailable`; successful later components remain visible.
The production reporter emits one log event per failed component under only the
`not_ready` or `query_error` class, so one request emits at most 22 such events.

Counts become `exact` only when the bounded query exhausts its table or
namespace. Consuming the private sentinel reports the component allocation as
a truncated `lower_bound`. T30.6p publishes logical encoded receipt bytes for
extraction outcomes, canonical content bytes for proof bundles, and canonical
receipt bytes for the three caller-row components. It reads only server-side
byte lengths or stored scalar totals, not proof content, caller pair arrays, or
job diagnostics. Per-component `physical_database` metrics remain
`unavailable`. At the T30.6p boundary, the other 31 components and both
installation data-volume metrics were unavailable. Those unavailable values
were not zeros.

The retention collector adds no index or row bootstrap. It reuses the
`evidence_chunk` table and indexes required by T40.7's accounting writer, with
no additional retained-row scan, backfill, sort, writer-generation bump, or
migration generation. It adds no sync-tick, retry, writer,
publication-transition, or maintenance work.

An authorized request performs at most five readiness queries and 24 bounded
row-range queries. The resulting summaries are weakly consistent diagnostics,
not one frozen cross-table snapshot; denial still precedes all of them.
Computing an exact proof-bundle byte metric may inspect up to 80 bounded 64-MiB
canonical content values, including the later-excluded sentinel, inside
SurrealDB (5.00 GiB worst case), while only scalar byte lengths cross the
WebSocket/API boundary. No request takes an owner lock, writes retained state,
starts a child, scans the filesystem or corpus, or hashes content.

T30.6q populates the exact 24 Investigation/Workbench component tables listed
above and retains one independent aggregate summary for every table. The fixed
v1 wire has no owner-lifecycle partition field, so it does not compute a hidden
partition: every physical row contributes regardless of owner, release,
override, access, review, or Watch state, and `investigation_run_job` remains
only in durable-job history. All 24 Investigation components receive 77 report
slots plus one sentinel. The q allocation is therefore 1,848 reported and at
most 1,872 scanned identities.

One `INFO FOR DB` catalog preflight must return exactly one result envelope and
proves which of the closed 24-table allowlist is present. The collector then
runs at most 24 direct record-ID-ordered table reads, each with physical limit
pushdown and the same exact-one-envelope rule. A missing catalog entry leaves
only that component unavailable; a row-query failure does the same, while a
catalog-query failure leaves all 24 unavailable rather than inventing zero.
Successful siblings remain visible and are weakly consistent rather than a
frozen owner snapshot. One q request performs at most 25 SurrealDB calls,
returns at most 1,872 identities, retains at most 78 selected IDs for the
active table plus 24 summaries, and receives at most the 24 fixed allowlisted
names from the server-side catalog intersection. It emits at most 24
`not_ready` or `query_error` events. It reads no row payload or byte content,
so every q `physical_database` byte metric remains unavailable.

T30.6q uses the existing table record order and schema catalog. It adds no
query index, schema backfill, migration marker, first-open reconstruction,
writer, or owner-lifecycle work. Together the core and T30.6q collectors populate 46
components within 3,557-report, 3,603-scan, 54-query, and 46-event ceilings.
The deterministic empty core-plus-Investigation response is 19,797 bytes under
the unchanged 64-KiB encoded-response cap.

These are per-request ceilings. The surface adds no retention-specific cache
or concurrency gate, so concurrent authorized requests independently multiply
the query, identity, and response-memory work; it supplies no additional
process-level bound.

T30.6r populates the final seven derived components: candidate store authority
and managed files, focused repository state and publication files, resolver
store authority and package-owned files, and managed caller artifacts. The
four authority selections accept at most 308 reported/312 scanned rows behind
one four-name catalog query, three existing readiness point checks, four
direct bounded row reads, and one batched caller current-authority fence—at
most nine store client calls, or 63 across the complete collector path. The
fence remains one client round trip but performs at most 308 bounded
server-internal point reads—four for each of at most 77 caller authorities—
plus its marker check. The caller authority is bounded support for artifact
reconciliation, not a second component. Focused state bounds the raw
repository-ID prefix before applying the schemaless analysis-unit predicate;
if that prefix reaches its limit, qualifying rows are lower-bound and no
qualifying row is unavailable rather than exact zero.

Filesystem inventory is incremental and metadata-only. Candidate and focused
components admit stable package publication files; resolver additionally
admits regular files in package-owned stage directories; caller admits valid
repository directories and stable complete-manifest or leaf filenames. Bounded
parsing of store-authorized manifests separately proves canonical receipt
coverage. Stable unmatched package residue contributes count and apparent bytes,
but unrecognized top-level controls, foreign entries, symlinks, special files,
and path-escaping entries do not become managed identities. Regular files in a
recognized package-owned resolver stage are the deliberate temporary-stage
exception. Resolver canonical-content and caller
canonical-receipt bytes require a matching bounded store-authorized manifest.
No candidate member, focused shard, resolver member, or caller leaf payload is
opened or hashed.

One authorized request reads directories in 256-name batches and observes at
most 32,768 candidate entries, 32,768 focused entries, 32,768 resolver entries,
and 65,536 caller entries—163,840 aggregate. It charges at most 4,096 stats,
reads at most 64 MiB of manifest metadata, queues at most 256 caller repository
directories, and uses at most five simultaneous
structural descriptors: at most three collector-retained handles plus up to two
Go/platform directory-iterator duplicates or rooted traversal internals.
The stat ceiling includes explicit descriptor-rooted `Lstat` checks,
conservative open-time `fstat` charges, and one conservative slot per name-batch
(`Readdirnames`) call for the Windows error-classification `File.Stat` fallback.
The 77-report/78-scan slots allocate the response envelope rather than promise
universal exactness. The 4,096-stat ceiling covers the regression-gated lean
maximum allocation; recognized residue, nested stages, or the independent
64-MiB metadata limit may still localize a lower-bound or unavailable metric.
Every returned raw name consumes the observation budget. Names are otherwise
names-only; only recognized names receive explicit descriptor-rooted `Lstat`
checks.
Foreign entries consume those physical budgets. Each file component still owns
only its 77-report/78-scan summary allocation. A private sentinel is a
truncated lower bound; a positive prefix stopped by the observation, stat, or
metadata budget is a non-truncated lower bound; zero observed before an
incomplete stop is unavailable. A missing managed subroot under a verified data
directory is exact zero. If the data root
is unreadable, symlinked, special, or otherwise invalid, all filesystem and
data-volume metrics remain unavailable while independent store summaries stay
visible. Check the service log for the localized cause; do not repair status by
hand-deleting artifacts. T30.6r emits at most nine localized diagnostics per
request; together with the core and q collectors, the complete event ceiling is 55.

The 64-MiB metadata allowance is aggregate I/O, not a Go-heap meter. Parsing is
serial: one caller manifest may retain up to 32 MiB of raw bytes while
allocating its separately bounded decoded pair structure; resolver raw
metadata is capped at 1 MiB. Concurrent administrator requests multiply that
one-at-a-time raw-plus-decoded heap work.

T30.6r completed collector coverage across the original 52-component registry;
T40.7's `evidence_chunk` and T40.10's `extraction_domain_root` collectors make
the current surface 54 components.
The current 4,096-report allocation gives registry indices 0–45 76 report
slots and the remaining eight 75, each with one private sentinel, for 4,150
aggregate scans. Core owns 1,745/1,768 report/scan identities across 23
components, Investigation owns 1,824/1,848 across 24, and derived owns 527/534
across seven. The deterministic empty and maximum-shaped envelopes are 20,565
and 21,564 bytes, respectively. The core collector performs at most 25 bounded
row-range queries; the additional query inventories only
`extraction_domain_root` record identities and adds no lifecycle mutation.
The derived collector populates filesystem total/available capacity for the
verified data directory.
Resolver/caller canonical byte metrics require the supported rooted nonblocking
regular-file opener. On a build without it, those canonical metrics remain
typed unavailable while physical component inventory continues.
Canonical manifest lookup follows host filesystem path semantics. On a
case-insensitive filesystem, a byte-case alias can validate canonical bytes
while exact-spelling physical inventory ignores that alias; the metric kinds
remain independent.
On a build whose operating system lacks the supported descriptor-bound
filesystem-capacity primitive, those two metrics remain explicitly unavailable
while component inventory continues.
It does not publish used bytes: operators may derive them only with appropriate
filesystem semantics, and must not combine them with logical, canonical,
apparent, or physical-database byte kinds. Every per-component
`physical_database` metric remains unavailable.

The unconditional static warning
`unbounded_historical_publication_retention` is logged before opening the
store, so a slow or failed store open/startup migration cannot hide it. Every
response from `/api/retention-status`, including authorization denial and
internal error, carries it in `X-Phebs-Warning-Code`; every successful body
also repeats it as `warning_code`, including for an empty installation.
T30.6p, T30.6q, and T30.6r query, readiness, catalog, or filesystem failures
are localized as `unavailable`/`lower_bound` summaries rather than converted
to exact zero or used to hide successful sibling components. Audit, analytics,
authentication, and other installation state retain their separately
documented lifecycles; this
endpoint is not a claim to
inventory every database table. After ordinary authentication, a denied
request performs only the administrator check. An authorized production
request runs all three bounded collector planes, then validates and encodes one
fixed `O(53)` structure with a sub-64-KiB body. Startup still adds only the
existing warning log line plus T30.6p's scalar field definition inside the
existing batched schema. T30.6r additionally allocates only its bounded
collector and four-entry policy map; T30.6q and T30.6r add no startup retention
I/O, query, scan, child, retained-row scan, backfill, sort, index installation,
or migration generation. Sync ticks,
retries, no-ops, and publication transitions add no work. The endpoint takes
no lock, starts no child, and adds no retained-owner deletion, configuration,
lifecycle mutation, corpus read, payload/member/shard/leaf content hash, or
mirror work. Canonical manifest-metadata validation may recompute its bounded
metadata digest.

Expanding or relocating `server.data_dir` is the only unconditional live-
capacity escape. Take a verified backup before supported repository removal;
removal reclaims derived files and makes non-quarantined unpinned evidence
sweepable, but pins and retention-quarantined evidence remain. Pin rows remain
until their owning lifecycle releases them. A positive
`proof_bundles.retention` is a supported, scoped way to expire proof-bundle
rows and their exact pins; it is not a historical-publication or cross-owner
capacity bound. Retention-quarantined evidence has no supported deletion
procedure in this release and requires a separately reviewed administrator
resolution. Never delete database rows, publication artifacts, manifests, or
pin records by hand. A future bounded posture requires a new ADR and owner-
separated implementation; there is no historical-publication retention
configuration key in this release.

Store upgrade preserves readable legacy whole-repository runs with an empty
unit digest and their original source commit. It does not copy the currently
configured unit onto historical evidence. Current focused consumers request
the complete tuple and never fall back to those rows. An older writer refuses
a store already claimed by this generation, while a newer unknown writer is
left untouched and unreadable rather than guessed compatible.

This release advances extractor and enumeration identities in the shared
policy digest. On first processing after upgrade, every indexed repository
therefore re-extracts every enabled domain once; settled pointer-only no-op
cost resumes after those replacement outcomes publish or settle.

Coverage records name the scope posture, unit digest, candidate-manifest
digest, candidate plane, exact selected candidate count/bytes/digest, and the
source paths actually read. Focused local coverage also reports excluded
ordinary source file/required counts and declared bytes; SCIP field coverage
adds excluded document/definition/occurrence counts. Coverage certificates additionally disclose the
unit name and canonical primary/supporting roots, typed-index posture and path,
and treat freshness as equality of both commit and unit digest. A failed or
staged replacement remains visible only as the latest attempt for that exact
tuple; it never displaces the prior complete publication. Treat an
`unpublished`, stale, typed-index-gap, or extraction-refusal state as a
coverage gap, never as evidence that no matching code exists.

The candidate-manifest receipt is part of current evidence visibility, not
only an extraction-time audit field. A typed designation change or a newly
accepted candidate manifest retires current-schema publications carrying a
different receipt even when commit and unit digest are unchanged. Reads
independently validate stored coverage bounds, source-path admission, gitlink
census, and that exact receipt; malformed or tampered coverage therefore fails
closed instead of satisfying the steady-state no-op or a consumer query.
Candidate-pointer create/replace/clear and evidence publication advance a
monotonic internal repository revision. Indexed commit and full canonical
analysis-state changes—including typed designation and search-index
posture—advance it too and retire current evidence when the exact tuple is
otherwise unchanged. Result-time consumers combine that revision with exact
publication receipts as their last authoritative read, so a clear-and-restore
or `A → B → A` rollback is detected even when the final visible tuple equals
the starting tuple.

Startup removes abandoned package-owned stages, audits orphan candidate bytes,
and reconciles every indexed repository into a candidate job; that job reuses
or replaces stale live publications before resuming the extraction handoff.
Orphan publication bytes follow the existing `sync.cleanup_orphans` deletion
gate. Repository deletion cancels candidate, extraction, and resolver-catalog
jobs and removes their database pointers and derived bytes. A malformed
derived database pointer is cleared
under the repository lock and rebuilt; database transport/query failures still
fail closed. A preexisting stable marker permits strict reuse of a complete
generation after a real publication crash; without that marker, clearing the
pointer forces a fresh Git census. If a crash marker or corrupt candidate
publication continues to refuse after automatic reconciliation, stop phebs,
retain logs, move `$DATA/candidates` aside for diagnosis, and restart: the
directory is derived and is rebuilt before extraction. The supported restore
path clears the imported database pointer automatically and discards only
candidate-control-failure outcomes before reconstruction. Ordinary durable
outcomes remain restored state but cannot become eligible until the exact
candidate generation exists again. Do not manually copy a pointer or
individual member into a new installation.

Committed focused state is deliberately fail-closed. A malformed or tampered
`indexed_analysis_unit` can therefore make repository listing, startup
reconciliation, search compilation, and repository status refuse together.
Recover by restoring a validated precious-state backup. If no usable backup
exists, keep phebs stopped and escalate to a witnessed database-row repair
that atomically clears only that repository's complete index claim
(`indexed_commit_hash`, `indexed_revisions`, `indexed_analysis_unit`, and
`indexed_at`). There is no supported online repair command; do not hand-edit
one digest or leave a partial claim. Restarting after the atomic repair queues
a forced focused replacement from configured scope.

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

When enabled, every successful index schedules a bounded read of the
repository's committed `.proto` contracts at the latest indexed full commit.
Services, messages, RPCs, and numbered fields become `DECLARES_*` assertions
backed by content-keyed evidence atoms bound to the repository, commit, path,
digest, byte span, and line span. Type links are intentionally file-local and
recorded only when exactly one same-file declaration proves them; unresolved
names keep separate reason codes and are never labeled external. Extraction
runs publish atomically: a read, parse, provenance, limit, cancellation, or
publication failure leaves the prior published facts intact.

A separate `experimental.provisional_thrift_extraction` opt-in enables the
Thrift declaration reader over committed `.thrift` IDL. Operation identity is
`scope.Service/method`, where scope is the last segment of an explicit
`namespace go`, then `namespace *`, then the file basename. Request and
response shapes are modeled wire-honestly as the implicit argument and result
structs Thrift serializes: field `0` is the success slot, `throws` clauses are
result fields, and `oneway` functions declare no result struct. Type links are
file-local exactly as for protobuf, and fields with implicit identifiers fail
closed rather than fabricating identity. Buf-based wire-compatibility checking
remains protobuf-only; no Thrift compatibility engine exists.

The same Thrift opt-in also enables the Go consumer reader. It recognizes the
repository's own committed Apache Thrift generated Go, binds wire method names
to their generated client methods, and emits registration (tier `derived`) and
call (tier `heuristic`) assertions only for unambiguous names; ambiguous call
names and constructors abstain as unresolved candidates. A repository that
imports its generated stubs from another module yields no consumer evidence —
an honest abstention, not an error — and consumer joins against declarations
stay name-bound exactly as for gRPC.

A separate `experimental.provisional_kafka_extraction` opt-in enables the
Kafka topic-evidence packs (`kafka-producer` and `kafka-consumer`) over
non-test Go files that import sarama (Shopify or IBM) or segmentio/kafka-go.
A topic binds only when it is a string literal or a same-file `const`
satisfying Kafka's own naming bounds; the object is `topic:<literal>` and
carries no cluster, environment, runtime, or completeness claim. Consumer
group ids are recorded as detail, never identity. Configuration selectors,
function results, variables, and cross-file names emit `UNRESOLVED_KAFKA_*`
assertions naming the frozen shape class. **Expect abstention to dominate**:
production Kafka topics are overwhelmingly configuration-driven, and the pack
presents that volume as the honest norm rather than a defect. There is no
topic declarations plane: topics appear only through their producers and
consumers, with no catalog or Atlas surface.

The proto opt-in also enables the Go/gRPC consumer reader. It indexes the
repository's own generated `*_grpc.pb.go` stubs, then emits registration
(tier `derived`) and client-call (tier `heuristic`) assertions only when a
name matches exactly one indexed service; ambiguous names, helper collisions,
and duplicate service FQNs emit exact-span unresolved diagnostics instead of
guesses, so successful abstention remains publishable. Every assertion carries
a `code_role` and cites its atom's exact byte and line span. Resolution is
syntactic — there is no type checking — so these facts carry reduced fidelity
by design and must not drive compatibility, migration, or negative-proof
conclusions.

Under the same protocol flags, the `grpc-caller` and `thrift-caller` domains
add declaration-proven typed caller evidence from a committed repository-root
`index.scip`. phebs never creates or downloads that index — regenerate and
commit it whenever source changes. A tier-`derived` `CALLS_OPERATION` row is
emitted only when the complete generated-client provenance chain agrees;
missing or conflicting mappings emit operation-keyed `UNRESOLVED_CALLER` rows,
and malformed SCIP produces bounded extraction gaps in the affected protocol
domain only. When a usable typed occurrence is absent, a bounded package-aware
fallback may emit `resolution=syntax`, tier-`heuristic` rows; dynamic flows
and ambiguous clients still abstain. Module identity comes from every
committed `go.mod`, root or nested, so a polyglot monorepo whose Go modules
live in subdirectories resolves callers against the nearest enclosing module;
files outside any module are still read for coverage but yield no facts. Each row snapshots the unit-attribution
state used at extraction time. These rows remain provisional and dark and
establish neither caller completeness nor measured accuracy.

The opt-in also reads that same committed root `index.scip` to emit
`REFERENCES_PROTO_FIELD` assertions. A symbol is eligible only when its exact
definition provably maps to a generated protobuf Go field and its committed
`.proto` declaration; local symbols, malformed ranges, and ambiguous joins
abstain, and a missing index is an explicit unavailable result. The generated
Go `// source: ...` marker is generator-relative: `scip-proto-field` 1.1.0
resolves it against the repository root and directories marked by committed
regular `buf.yaml` files. Exactly one matching `.proto` path is required;
duplicate module-relative matches abstain. phebs does not execute Buf or parse
generation configuration, and the resolved repository path remains the only
source declaration read and cited. Field identity is canonical across consumer
dependency versions —
`(contract_lineage_id, message_full_name, field_number)` — so a field rename
that keeps its number and message remains one identity. These are direct
field references, not claims that a response field was semantically read.

The independent `experimental.provisional_thrift_field_extraction` opt-in
registers the `scip-thrift-field` reader. thriftrw output whose embedded IDL
module digest, generated struct-field order, and wire-ID literals agree binds
tier-`exact` rows; Apache Thrift compiler output, which embeds no IDL bytes,
remains tier `derived`. Both families use objects `scope.Message#field-number`
(including field `0`) under the same package-based lineage family as protobuf.
Duplicate identities abstain, malformed generator candidates abort the staged
run, and a missing root index publishes explicit `scip-index-absent` coverage.
The pack admits bounded index, document, occurrence, and candidate sizes only,
reads only committed repository blobs, and carries no accuracy, completeness,
runtime-use, or absence claim. `find_proto_field_references` remains
protobuf-only and byte-stable; the protocol-neutral `find_field_references`
route, impact report, and MCP tool fan out across every registered
field-reference domain whose number rules admit the requested identity.

#### Provisional Workbench startup and smoke

The Change Workbench remains default-dark. To bind it to the same
store-derived Contract Atlas used by the instance, enable it alongside at
least one declaration lane:

```yaml
experimental:
  provisional_proto_extraction: true
  provisional_workbench: true
```

Startup refuses before serving when the declaration lane or shared evidence
services are missing (`workbench-evidence-prerequisite`), or when a synthetic
Workbench or Contract Atlas fixture would create a second catalog authority
(`workbench-authority-conflict`). `PHEBS_SYNTHETIC_WORKBENCH` accepts only an
empty value or exact `1`; malformed values retain that strict parsing error
instead of being classified as an authority conflict. Successful startup logs
one warning naming the provisional, non-production posture. The bound evidence
service reuses the instance's exact Caller Map and comparison objects; startup
does not populate their publication, index, binding, or citation caches merely
because Workbench is enabled.

For a bounded operator smoke over public, remote-HEAD evidence:

1. Use the isolated `phebs-everything.yaml` configuration and a fresh or
   disposable `~/.phebs-everything` data directory.
2. Run `make build`, then `./phebs serve -config phebs-everything.yaml`.
3. Wait for one configured public repository to finish sync, index, and
   protobuf or Thrift declaration extraction. Confirm a published declaration
   run in the extraction logs or Contract Atlas coverage. For a modify,
   migrate, or retire observation, also wait for the applicable complete caller
   generation, or deliberately retain its `missing`, `failed`, or `stale`
   state as the typed gap under test.
4. Sign in, open **Contract Atlas**, select one exact published operation, and
   choose **Start Workbench**. Confirm the resulting Workbench retains the
   repository, indexed HEAD commit, declaration lineage, and operation shown
   by Contract Atlas. In Where, confirm focused-local coverage is separate from
   the repository-overlay generation. A current generation may show exact rows
   and **Read exact cited bytes**; an unavailable generation must show no
   caller total or comparison classification.
5. Stop the instance and remove the disposable data directory if the
   observation does not need to be retained locally.

This is a manual availability check only. Upstream HEADs and the resulting
rows may drift; do not commit outputs, turn observations into an accuracy
number, or use them as deterministic merge-bar input. `make dev` and
`make dev-api` instead provide the retained neutral T30.7 focused-service
cohort and T33.5 companion catalog through the same ordinary store-derived
paths.

### Source-free service-directory walkthrough

`make dev` and `make dev-api` select the exact retained
`docs/fixtures/t33.5-service-directory/t335-service-catalog.json` for the
T30.7 repository. Startup refuses a renamed, nonregular, mismatched-authority,
or colliding selection. It then uses the normal bounded file reader, exact
source census, immutable catalog publication, and service-state reconciler;
it does not install UI/API response data. Ordinary `phebs serve` does not read
or select this catalog.

After the neutral repository reaches its indexed commit:

1. Open **Repos**, choose **Services**, and confirm authority
   `operator · t335-demo · v1` with five identities, seven accepted source
   files, and two unowned source files.
2. Inspect `orders-api` and `orders-events` for exact shared/supporting roles.
   The directory reads catalog metadata only, not the bytes at those paths.
3. Filter to conflict and proposal states. Enable removed identities to inspect
   `legacy-orders`; its successor is authority lineage, not a runtime edge.
4. Reload the exact detail route and exercise browser back/forward. A sparse
   filtered page with **Next** means the 500-row scan bound was reached, not
   that the remaining directory has no matches.

If startup or the page refuses:

- preserve the current catalog/state rows; do not delete them to force a
  replacement;
- inspect the bounded startup error or page problem, then correct the selected
  fixture/config or wait for the repository's exact index reconciliation;
- after a catalog/state transition, return through Repos or remove an old
  cursor from the hash route rather than treating cursor refusal as empty; and
- use **Retry** only for the current route. The page performs no polling and
  aborts superseded requests.

The retained counts and digests live in the
[T33.5 receipt](../fixtures/t33.5-service-directory/receipt.json). This is a
source-free authority/lifecycle demonstration, not evidence of runtime use,
relationships, completeness, extraction accuracy, supported scale, migration
completion, decommission safety, or release readiness.

### Source-free All code/service-search walkthrough

`make dev` and `make dev-api` also bind the retained T32.3 bundle and the exact
`docs/fixtures/t34.4-service-search/t344-service-catalog.json` as a separate
ordinary whole-repository cohort. Startup pins both file names and catalog
bytes, rejects a focused-unit or catalog collision, and leaves every
experimental evidence flag disabled. Ordinary `phebs serve` does not select
this cohort unless both `PHEBS_T344_SERVICE_SEARCH_REPO` and
`PHEBS_T344_SERVICE_SEARCH_CATALOG` name the exact clean absolute files.

After indexing and service-generation reconciliation settle, open the
repository's service directory and use **Search this service** for Orders API.
The selector, HTTP parameters, SSE scope event, and MCP `search_code` fields
all use the same All code/service contract. Shared paths are included for each
accepted service; explicit unowned paths remain visible only through All code.
A current-to-stale transition stays labeled and bound to the last complete
active authority. Unavailable scope refuses without widening.

The [T34.4 fixture receipt](../fixtures/t34.4-service-search/receipt.json)
pins the bundle, commit, catalog, and closed cardinalities. Scope receipts bind
emitted citations rather than corpus completeness. The walkthrough adds no
evidence pack, relationship/runtime-use claim, extraction accuracy, supported
scale, migration/decommission conclusion, or release authority.

### Generation scheduler boundary

T35.1 installs a reusable durable generation scheduler but registers no
production workload. Operators therefore do not configure a new worker or see
new product/API state yet. T36 and T37 may use the scheduler only by naming an
immutable repository/stage/generation plan and a closed CPU, IO, or memory
class; they may not enqueue one job per service.

One planner tick materializes at most 64 offset/length chunks. One worker holds
one chunk, one heartbeat, and its declared memory/descriptor budget. Repository
tokens cap cross-stage work even with multiple processes. A new generation
coalesces the current pointer without deleting history; an old worker is
allowed to finish computation but cannot commit through the final fence.
Retries create fresh attempt rows, cancellation consumes no attempt, and stale
leases return with lower deterministic priority than never-run and retryable
work. Attempt exhaustion affects only that chunk.

T40.R1 hardens this boundary after the neutral-21 wedge. Planner expansion,
stale-lease reaping, and worker claim/execution run as independent loops per
resource class, so one blocked store call cannot starve lease recovery.
Reap, claim, heartbeat, settlement, and exhaustion-callback store calls are
bounded by a per-call five-second context; the atomic expansion transaction
runs under the scheduler lifetime instead, so a slow fanout page is never
aborted and restarted from the same offset. An explicit SurrealDB conflict
abort replays the same fenced transaction at most 64 times and emits one
source-free operation/attempt diagnostic per replay. An ambiguous completion
or final-attempt retry response is reconciled against the durable chunk row
on a fresh bounded context: a chunk completed under the caller's own
claimant reports success, a chunk settled by another claimant stays fenced,
and a durably committed final-attempt failure reports exhaustion only for its
exact preserved claimant, so its terminal record is neither lost nor adopted
by a reaped worker. A transient heartbeat error is tolerated only until the
last durably confirmed heartbeat reaches the stale cutoff; only a definitive
lease fence cancels a running handler earlier.

The hard admission ceilings are 4,096 ordinals per chunk, 64 chunks per fan-out
page, 80,000,000 items and 1,000,000 logical chunks per schedule, eight active
stages and eight tokens per repository, and eight attempts. Process pools admit
at most 64 handlers, 8 GiB declared memory, and 4,096 declared descriptors;
each handler may declare at most 1 GiB and 256 descriptors. These are refusal
bounds, not capacity guidance.

#### Frozen lifecycle policy

T35.2 froze the policy and T35.3 now installs its bounded controller and
`lifecycle.enabled` switch. Do not manually delete scheduler rows, catalogs,
publications, pins, or partial stages to resolve pressure.

Protection is evaluated before eligibility. Current repository, catalog,
service, source/search, and scheduler pointers are live roots. A stale
service's exact active generations remain rooted. Proof and Investigation
ownership is transitive, active reader and worker leases protect their exact
generation, and current plus one prior complete generation forms the rollback
floor. A backup does not pin live data. No age, count, byte, or watermark rule
may override these protections.

| Owner | Default age | Default count | Independent byte/quantity metric |
| --- | ---: | ---: | --- |
| catalog generations | 30 days | 3 per repository | 64 MiB canonical JSON per repository |
| source generations | 14 days | 2 per repository | 8 GiB encoded members per repository |
| search generations | 14 days | 2 per repository | 50 GiB filesystem allocated bytes per repository |
| observation namespaces | 14 days | 2 per repository | 20 GiB encoded members per repository |
| joint v2 observation generations | 14 days | current + 1 rollback per repository | separate 20 GiB encoded-member and 20 GiB segment-charged observation-byte ceilings per generation |
| resolver namespaces | 14 days | 2 per repository | 10 GiB encoded members per repository |
| relationship namespaces | 14 days | 2 per repository | 20 GiB encoded members per repository |
| abandoned partial stages | 24 hours | 2 per repository/stage | charged to the owning artifact class |
| settled generation schedules/chunks | 7 days | 2 per repository/stage | row count only; no byte substitution |
| terminal rows in each existing durable job table | 30 days | 100,000 per table | row count only; no database-byte claim |
| current service tombstones | indefinite | disabled | precious incarnation/ABA fence |
| proof bundles and Investigation/Workbench state | indefinite | disabled | released only by the explicit owner lifecycle |
| retired readers | final lease release | current replacement only | no age/count/byte policy |

For an unrooted identity above the rollback floor, any enabled matching age,
count, or byte limit makes it eligible, oldest first. Canonical JSON bytes,
encoded member bytes, filesystem logical bytes, filesystem allocated bytes,
and database row counts are separate metric kinds. They must not be added or
used as substitutes. If one byte metric is unavailable, only that byte rule is
disabled and the status remains visibly unavailable.

The future default is `lifecycle.enabled: true`. Explicit false disables
automated age/count/byte and pressure collection, but not safety admission,
roots, pins, leases, tombstones, or the existing independent
`proof_bundles.retention` lifecycle. The filesystem containing
`server.data_dir` uses allocated-byte watermarks: 80% begins accelerated
bounded collection, 90% refuses new derived artifacts and partial stages, and
admission resumes only below 75%. With lifecycle disabled, the 90% condition
refuses rather than deletes. Unknown capacity is reported unavailable and
refuses only new pressure-dependent T35 workloads.

A removed service must first commit its durable tombstone; only then may its
prior generation leave the live-root set. Proof and Investigation pins and
active leases always win, even if disk remains above 90%. T35.3 rechecks all
roots immediately before collection and coordinates with backup.

The installed controller has fourteen closed owners and advances one owner per
turn through durable CAS cursors. A successful turn uses at most sixteen store
queries including four cursor operations, 64 candidate rows, sixteen deleted
rows, 256 filesystem stats, eight descriptors, and 1 MiB of bounded metadata.
Failure is local: the failed owner's cursor stays put while durable rotation
gives the next owner a turn. Backlog and 80% pressure use the five-second
cadence; a completed unpressured cycle returns to one hour. Destructive store
turns share the index mutation lock, so online backup observes either before or
after a sweep.

Catalog collection transactionally protects current, every service desired or
active catalog reference, and current plus two rollback generations. It scans
at most eleven candidates and deletes one immutable generation per turn;
authority-version claims remain. Generation-schedule collection protects the
current pointer and every running lease, then removes no more than fifteen
chunks plus an empty schedule. Its wrapped key cursor reconsiders a schedule
after a lease releases.

Job maintenance covers all eight durable job tables. It deletes at most
sixteen terminal rows older than 30 days, performs a restart-resumable 64-row
physical census, and trims at most sixteen oldest terminal rows when the
lower-bound census exceeds 100,000. Concurrent queue writes mean census status
is `lower_bound`; each deletion still rechecks terminal status and finish time.
Pending, claimed, and running jobs are never candidates.

Source/search and resolver publications currently expose no separate
historical namespace to this controller. The observation owner protects its
current pointer, publishing marker, active cache leases, and one prior complete
generation. It first renames one eligible excess generation out of authority,
then removes at most sixteen regular files/directories per turn; every resumed
turn rechecks those roots and the lease before continuing. The relationship
owner remains exact empty until that pipeline registers publications. Proofs,
Investigations, tombstones, readers, and crash-stage recovery retain their
explicit existing lifecycle and are never swept by analogy. A malformed owner
is `unavailable`, not permission to widen another collector.

The separate `observation-v2-generations` owner walks the closed `v2/`
namespace under the same backup/publication mutation lock. Its small root
protects current plus one rollback floor; a publishing marker protects its
exact stage and any sealed post-rename candidate. Exact cache leases and the
explicit proof/Investigation pin providers outrank age, count, pressure, and
collection. One eligible generation or abandoned worker stage is renamed to a
closed `collecting-*` name before deletion. Each turn removes at most sixteen
known regular entries and resumes that collecting name after interruption;
unknown names, symlinks, special files, changed controls, or pin-provider
errors refuse. Its status is the standard bounded source-free owner result—no
repository, generation, path, source, or retained content is exposed.
Publication admission counts both `.stage-*` and `collecting-stage-*` names and
reserves the new stage plus marker before either is created, keeping the
74-entry namespace readable by archive and lifecycle after any interrupted
begin/restart. A cache lease pins its expected repository/generation from the
moment a cold open starts, before full filesystem validation completes.

Capacity admission opens and identity-fences the real data-directory
descriptor; a symlink or changing root is unavailable. Exact no-op indexing
returns before any check. A real whole-search rebuild first performs a
zero-byte hard-watermark probe, streams its already-required source census,
then reserves
`min(48 GiB, 3 × regular declared bytes + source encoded-member bytes)` before
starting `zoekt-git-index`. The completed stage is measured again: one shard
may carry at most 512 MiB logical bytes, one immutable generation at most 256
shards, 16,644 authority files, 48 GiB logical bytes, and 48 GiB allocated
bytes. Current plus one rollback generation may carry at most 96 GiB of each
byte kind, evaluated independently. These are prospective validation bounds,
not a fixed reservation charged to every repository. An unavailable capacity
probe may pass only the initial compatibility probe; the measured replacement
reservation fails closed.

Whole-search children explicitly use the go-git blob reader. The parent
removes every inherited `ZOEKT_DISABLE_CATFILE_BATCH` value and installs
exactly `true`; local shell state cannot silently select batch mode. Each
immutable `phebs-search-generation-receipt-v1` records `go_git`, exact files
offered, zero batch reads, fallback reads equal to offered regular placements,
shard/file counts, and separate logical/allocated totals. Verbose indexing
logs the same source-free mode and counters.

Whole-search publication retains immutable bytes under
`index/search-generations/<repository-hash>/<search-digest>/`. The flat
`index/*.zoekt` view is a hard-linked compatibility view for the shared zoekt
reader; `phebs-search-generation-root-v1` is the current/rollback lifecycle
authority. A transition marker exists before flat replacement. An uncommitted
database transition restores the previous complete generation. If the store
response and its immediate reread both fail, both controls remain: the next
retry or startup selects only the candidate or previous revision set matching
the durable repository row before it may build or roll back. A legacy whole
publication remains eligible for exact committed validation when a newer
marker matches neither durable revision; other recovery errors remain errors.
A committed whole-to-focused posture removes the whole-search root before the
focused publication becomes visible, releasing its immutable generations to
pin-aware collection. Online backup's exclusive index mutation lock waits for
publication/collection, then snapshots current flat authority.
A completed archive is an external snapshot and never pins live generations.

Allocated bytes in a generation receipt are the creation-host observation.
Every deep validation remeasures and enforces the current filesystem's 48-GiB
ceiling; a byte-exact stopped relocation may change `st_blocks` without
changing content identity. Logical bytes, member counts, digests, and shard
limits remain exact identity and policy fences.

The `search-generations` lifecycle owner advances one durable fair repository
cursor per turn. It protects current, rollback, transition candidates, and
active query leases, then rechecks those controls immediately before renaming
one stale generation to `collecting-*`. It removes at most sixteen regular
entries per turn and resumes after restart. Admission reserves eight partial-
stage slots and one namespace-growth slot. A 73-entry bounded recovery prefix
lets an older over-budget namespace drain one prior-process stage or stale
generation per turn. Known `.DS_Store` and AppleDouble regular metadata is
identity-rechecked and removed within the same budget. Symlinks,
special/unknown entries, changed identities, root movement, and active pins
refuse; after a repository is selected, that error cursor advances so a bad
namespace cannot starve healthy repository siblings. Search queries lease the selected
immutable generation until their final fence, so the collector cannot unlink
its retired immutable names while a query still binds them. Underlying mmap
retirement retains zoekt's existing reader-lease behavior.

#### Lifecycle operator status and recovery demo

Administrators can open Settings or request `GET /api/lifecycle-status` to see
the fixed `phebs-lifecycle-status-v1` snapshot. Authorization runs before the
source. The response is capped at 16 KiB and reports only enabled state, fixed
turn/watermark limits, the latest allocated-capacity class, and one latest
source-free result for each of the thirteen owners, including the real
`search-generations` owner. It contains no cursor,
repository, generation, path, retained content, or raw error. A status request
does not run a turn, probe the filesystem, acquire the mutation lock, or read
the store; it copies the bounded in-memory monitor populated by normal
maintenance and index admission.

The retained source-free receipt at `spike/t354/results.json` binds T32.3's
synthetic 1,000/5,000-service profiles to production-path gates for catalog
churn, coalescing, interrupted stages, reader leases, proof-pin release,
80/90/75 pressure hysteresis, sweep/restart, and live backup/restore. It is a
mechanics receipt, not a supported-scale, SLO, completeness, or release claim.
The ordinary `make dev` cohort runs the default-on controller and exposes the
same Settings view without manufacturing old rows or bypassing root checks.

### Independent source-observation planning ownership

Whole-repository indexing commits the immutable repository source and search
controls before it invokes derived-pipeline callbacks. The selected
service-catalog, service-state, and service-search reconciliation remains first
at that callback seam. Go source-partition planning follows as an independently
owned `go-source-observation-plan` schedule. Once that one-item schedule is
durably enqueued, a later planning refusal cannot roll back, relabel, or rebuild
the already-current search generation. If enqueue itself fails before durable
ownership exists, the indexing callback remains retryable; the committed search
authority is still current, and a duplicate callback coalesces against any
ownership established by the earlier attempt.

Planning diagnostics expose only the following closed disposition, never a
repository, path, object ID, digest, child output, or raw error:

| Disposition | Operational meaning |
| --- | --- |
| `current` | The current observation publication already binds the committed source generation. |
| `active` | The exact planning target already has durable planning or compatible v1 execution ownership. |
| `failed` | The exact planning target settled with a closed terminal refusal or exhausted its bounded retries; no automatic same-generation census is created. |
| `enqueued` | A new one-item, generation-scoped planning schedule was durably committed. |

After an index callback, the process emits only
`observation planning: disposition=<value>`. Startup lists repository controls,
skips deleting, unindexed, and focused-analysis-unit repositories, and repeats
the same bounded enqueue check for each eligible whole repository. This repairs
a crash after source/search commit but before enqueue without opening source
members or Git objects. Startup emits only aggregate `current`, `active`,
`failed`, `enqueued`, and `unavailable` counts; `unavailable` means that the
bounded ownership check failed, without retaining or rendering its repository
or error. A nonzero `unavailable` count is the sole source-free startup signal
that those repositories require attention. Correct the underlying store/control
availability and restart or let the ordinary index callback retry; do not delete
a settled schedule or current publication.

The planning worker owns the expensive source-generation census through the
durable scheduler before it begins. It streams the existing bounded source
generation and writes the existing content-addressed source-partition v1 plan;
it does not start another zoekt child. The long census runs without the
repository mirror lock. For an A→B→A return, the worker fully validates and
pins the historical observation generation against lifecycle collection before
acquiring transition locks. A short final transition acquires the exclusive
index/lifecycle mutation fence and briefly probes the repository mirror lock.
If the mirror is busy, planning releases the exclusive fence and retries; this
cannot form a lock cycle with either index publication's repository-then-shared
order or lifecycle's shared-then-repository order. After both locks are held,
the worker renews the exact chunk lease, confirms the historical manifest
control when applicable, and rechecks both the exact committed source
generation and still-active planning schedule before it may stage observation
execution. A newer commit therefore supersedes stale work, and a stale worker
can neither change the prior complete observation pointer nor contribute a
failure to the newer schedule.

Cancellation releases the planning chunk for later work without consuming an
attempt. Heartbeat or lease loss cancels the handler and grants it no terminal
store write. Reaping binds release to the exact stale heartbeat selected, so a
later successful renewal wins rather than being revoked by old evidence; that
reaper fence and the final exact-lease renewal decide subsequent ownership.
Transient owned failures retain the scheduler's bounded retry behavior. A
closed `limit` or `invalid` pipeline refusal instead settles the exact chunk
terminally, creates no retry successor, and remains visible as `failed`; a
same-source callback returns that disposition rather than repeating the tree
census. Exhausting the bounded transient attempts has the same settled
same-source posture. A genuinely newer source generation receives its own
target. If A returns after A→B→A, the recovery schedule identity also binds
the prior schedule digest, so the re-current A work is distinguishable from
its settled pre-B history.

This ownership split changes no observation execution bytes. Existing
`phebs-source-partition-manifest-v1` plans and current
`go-source-observation` schedules remain strict-readable and continue through
the existing CPU handler. The new IO planning handler only produces or reuses
that v1 input and then hands work to the same execution/publication path. The
long census writes a unique hidden plan stage. Cancellation or a stale final
fence removes only that worker's stage; after the exact fence, the durable
publication marker precedes an atomic same-parent move into the
content-addressed plan path. A stale creator therefore cannot delete a
replacement worker's adopted plan. An interrupted observation stage moves into
the existing bounded lifecycle `collecting-` namespace; if its marker names
current authority, only the marker is cleared and the current generation stays
in place. Before that move, a process-local build fence blocks new v1 handlers
for the exact stage and waits, without the mirror or mutation lock, for every
already-admitted handler to return. Every v1 handler registers before opening
the canonical stage, so an admitted writer drains before rename and a later
writer cannot create a member beneath the absent canonical parent. The
lifecycle owner accepts multiple
bounded `collecting-` generations; closed ordinal suffixes distinguish repeated
pre-publication incarnations of one content generation. Because the rename has
already removed those paths from authority, lifecycle can drain them
deterministically from a newer marker even before the repository has its first
`current.json`, while existing reader pins still outrank deletion. Ordinary plus
collecting generations remain capped at 64. If neither pointer nor marker can
recover the repository identity, lifecycle counts the bounded collecting
inventory as inspected but retains it without advertising perpetual pending
work; a later marker restores pin-safe cleanup authority. A crash-shaped extra
collecting incarnation is drained before an ordinary-only over-limit state is
refused. Once v1 execution owns a plan, the existing settled-only cleanup
posture remains, and every owned planning run now also performs one bounded
schedule/lease-fenced collection pass over that repository's
`observation-plans/` namespace before its census. Collection removes at most
eight superseded plan directories, crash-orphaned staging directories, or
schedule/planning binding controls per pass after examining at most 512 names;
the namespace is closed and each removal advances the bounded scan, so repeated
passes cannot strand an artifact behind a fixed first window. It protects live
registered staging directories, both current planning and execution schedules,
the recovery identities a concurrent enqueue may mint, the current publication
pointer and marker, and any in-process build. The collector renews the exact
chunk lease and confirms all authority inside the same short namespace-mutation
fence used by binding publication and build/stage admission. Authority movement
or lease loss aborts before retirement. Eligible directories are renamed into
a closed collecting namespace under that short fence and recursively drained
after it is released; a crash leaves a bounded restart-visible drain name.
Any collector refusal keeps the owned planning turn retryable rather than
admitting more derived bytes behind an unproved namespace.

That writer fence adds two short process-local mutex operations to every v1
CPU-handler turn (registration and release), holds no mutex across the work,
and adds no per-chunk store or source-control read. Stale-stage quiescence polls
only the process-local admitted-handler count while existing writers drain.

The steady-state planning-ownership path reads bounded controls only: the
at-most-8-MiB source root, at-most-8-MiB observation pointer/manifest and
planning binding controls as applicable, plus generation-schedule point state.
It performs no source-member scan, Git tree census, Git-object read, parser
work, or child process. Startup adds one repository listing and that same
per-eligible-repository control envelope, retaining counters rather than raw
errors. Actual planning is one IO-class chunk with one repository token, at
most five attempts, process concurrency one, and a declared 256-MiB memory / 8
descriptor budget. Its source-partition v1 work remains capped at 16,384
members, 4,096 blobs and placements per member, 64 MiB per member, 4 GiB each
of declared unique-blob and encoded-member bytes, and 8 GiB of temporary spool
bytes. The T40.3 `phebs-source-partition-superroot-v2` contract (not yet the
production planning path) retains every one of those per-member bounds and
evolves only the generation ceiling: at most 16 independently v1-bounded
segments, 16,384 aggregate members, and 64 GiB each of aggregate declared and
encoded bytes, every limit checked before growth and refused under the closed
`aggregate_segments` or generation-byte dimensions. A v2 build may first
fully validate a prior super-root, canonically hash each new member without
materializing it, and hard-link an exact prefix/summary/digest match; explicit
reused/written member and byte counters prove physical reuse, and corrupt prior
authority refuses rather than becoming input. A current publication also invokes the unchanged resolver-enabled
relationship reconciliation seam: bounded catalog/service-state pages and
resolver/current-root controls, with at most one existing relationship-schedule
enqueue. No observation member or source blob is read on that callback path.
Those are refusal bounds, not a supported-scale or latency claim.

T40.4 adds a non-authoritative hierarchical observation stage for the next
migration step. Its small v2 root binds the exact source-partition super-root
and at most sixteen independently validated observation segments. Each segment
retains the existing canonical member, record, unsupported-reason, and
content-addressed observation formats and admits at most 250,000 records; the
root therefore admits at most 4,000,000 records while retaining the existing
20-GiB aggregate encoded-member and segment-charged observation-byte ceilings.
Object counts and bytes are charged per segment: identical objects hard-linked
into two segments remain two logical charges and are never mislabeled globally
unique.

Cold construction requires a completely validated source super-root, reads
each non-reused source member once, writes the root last, and performs a final
complete self-validation. A restart revalidates complete segments, reuses them,
and rebuilds only missing or invalid segments. A completely validated prior v2
stage may supply exact hard-linked member and observation bytes; the builder
reports read, parsed, reused-member, reused-observation, linked-byte, and
written-byte counters. One keyed lookup binary-selects a root prefix range and
opens one segment control, one bounded member, and at most one object. Complete
validation holds at most one segment's 250,000-name object inventory rather
than a generation-wide 4,000,000-name map. An exact-root cache performs the
cold validation once; subsequent leases perform no filesystem reread and pin
only that exact stage digest.

These v2 bytes are not current production authority. No worker, pointer,
startup recovery, backup/archive path, lifecycle collector, or product reader
selects them in T40.4. T40.6 owns those mutations after their recovery and
retention gates can understand both v1 and v2. The 4,000,000-record envelope is
a source-free admission bound selected from the frozen 262,144-blob semantic
profile, not a supported-scale, latency, or release claim.

T40.6 supplies that authority as a separate joint v2 plane without changing
the v1 product pointer. `BeginInventoryPublicationV2` writes a marker before
corpus work and returns closed `source/` and `inventory/` stage directories.
Completion fully validates the source super-root, observation inventory, and
their cross-root binding, reruns the durable worker fence, seals the candidate
in the marker, same-parent-renames the stage, advances current/rollback, and
clears the marker. A stale worker cannot publish. Startup holds the existing
exclusive mutation lock, completes only a fully validated staged or already
renamed candidate, and otherwise leaves the prior root unchanged. An exact
worker may retire an incomplete stage to `collecting-stage-*` and mint a clean
transition; the lifecycle owner drains the retired bytes later rather than
performing recursive deletion under the publication fence.
Republishing the exact current generation returns the unchanged root, including
its existing rollback floor; a same-generation reference mismatch refuses.

### Partitioned extraction execution

T40.10 registers sparse T40.8 partitions as ordinary durable generation work
under the dedicated `extraction` resource class. The process runs two class
workers, but every schedule reserves one repository token because the selected
Git-object reader holds that repository's exclusive content lock. A worker
heartbeats before waiting for the source lease; its unchanged five-minute
execution deadline begins only after that lease and lock are acquired. A lost
lease or cancellation installs no result. The final attempt may install only a
source-free exhausted result after the scheduler has atomically settled it.

Each domain owns one invisible partition run with its exact T40.9 plan digest
and candidate-manifest digest plus admitted aggregate fact, row, and reference
limits. Active runs are pinned against the legacy 24-hour staged-run sweep. A
candidate replacement aborts and unpins only an active run bound to the old
candidate; a sealed historical run stays `staged` and sealed so exact A→B→A
reactivation remains possible. Successful publication seals the run and swaps
one atomic `extraction_domain_root`; the prior domain root remains authoritative
after retryable, terminal, stale, cancelled, or incomplete work.
T40.11 owns selected/rollback lifecycle and archive policy for these roots and
runs, so administrators should not delete their private controls manually.

Ordinary completion writes one bit to a private, at-most-4-KiB durable bitmap.
Only the final bit causes a single ordered read of at most 490 bounded result
controls and the complete T40.9 root pass. On restart, exact active work or a
fully current settled generation is recognized from reread candidate and
observation references plus pointer-sized execution controls before full
candidate/source/observation validation. This hot reuse performs no Git,
extractor, candidate-member, sparse-index, or evidence append work. A changed
reference, missing pointer, or malformed binding falls closed into the strict
planning path; it never reuses approximate authority.

A present typed input carries one private, digest-bound source-scope control for
every admitted document. Each sorted entry contains the canonical path, full
SHA-256 path identity, immutable Git object ID, and declared size. The scope is
capped at 262,144 entries, 16 MiB of path bytes, 48 MiB per domain, and 256 MiB
per generation. Its
worker reads that control once, reads the selected SCIP index exactly once, and
uses logarithmic lookup to open only SCIP-requested admitted blobs without
reopening a candidate member. An unsafe, absent, out-of-scope, tampered, or
oversized document scope refuses. A deterministic extractor or staging refusal
is first installed as a closed zero-total `terminal_refusal` result and included
in the nonpublishing terminal root; only then does the worker return terminal
status to the scheduler. Private error detail never enters that result.

### Shared source-observation progress and neutral demo

Whole-repository indexing publishes one immutable Go source-observation
generation under `$DATA/observations/`. Each complete generation now binds a
`phebs-observation-operation-receipt-v1` containing only scalar mechanics:
unique admitted blobs, source reads incorporated into successfully published
members, distinct parsed and prior-generation-reused observations, observed
and unsupported blob counts, and a sorted closed unsupported-reason census.
For a complete receipt, `unsupported_reasons` is always an array; a generation
with zero unsupported blobs returns `[]`, never `null`.
The receipt contains no paths, object IDs, source samples, or raw errors.
Failed or interrupted attempts remain visible through scheduler counters and
are not mislabeled as successful-publication reads.

An authorized caller can inspect one exact repository with either transport:

- `GET /api/observation-progress?repository=<canonical-repository>`
- MCP `get_observation_progress` with the same repository input

Both invoke the same `phebs-observation-progress-v1` service and return the
same closed state: `current`, `building`, `failed`, `stale`, or `unavailable`.
The response includes exact source/publication/schedule generation digests,
bounded partition counters, publication totals, and the operation receipt.
A generation created before T36.4 can report `legacy_unavailable` receipt
state; it never becomes an invented zero. The encoded response is capped at
64 KiB.

Repository visibility and current indexed authority are resolved before the
progress reader touches any source, publication, schedule, or count. The
reader repeats source, observation pointer, publishing marker, durable
schedule, repository, and visibility fences before emission. A denied or
deleted repository is indistinguishable from absence and invokes no progress
read.

If an observation schedule exhausts its attempts, the prior complete
publication remains current. Reconciliation keeps already validated staged
members and creates a domain-separated recovery schedule identity bound to the
same target publication and the terminal schedule digest. It never reactivates
or resets the settled schedule row. Recovery workers retain the T36.3 exact
schedule and source-generation checks before currenting.

Warm current progress acquires the shared validated cache and performs two
repository point reads, two source-control reads, three pointer reads, two
marker reads, four schedule point reads (initial and final checks for both
planning and execution), bounded encoding, and short cache
bookkeeping. It opens no Git child, reads no source/member/observation bytes,
parses nothing, and takes neither the lifecycle mutation lock nor the
publication transition mutex. Cold cache fill performs one complete T36.3
validation pass. One provisional current-generation entry pins lifecycle
identity and shares that pass among concurrent readers. Cancellation or a
failed open removes the provisional entry so a later request can retry; it is
never cached as a valid publication. Atomic replacement of a mutable pointer
and a crossed pointer/cache snapshot return conflict/stale, while stable
malformed controls and immutable publication corruption remain fail-closed.
Authorized 500 responses expose only the fixed control, publication, planning,
schedule, or response projection stage; they never expose a raw cause, path,
repository/source identity, or publication content. A planning-owned read
additionally opens one bounded planning binding; a v1 building or failed read
additionally opens only the bounded
partition-plan manifest. A current reconcile performs one strict schedule point
read and removes the bounded derived plan and binding controls only after that
schedule is durably settled. Search, extraction, scheduler polling, backup, and
lifecycle costs otherwise retain their T36.3/T35 bounds.

The retained [T36.4 receipt](../../spike/t364/results.json) deterministically
parses one neutral Go content identity once and projects it independently to
gRPC caller, Thrift caller, Kafka producer, and Kafka consumer adapters. It
binds named production tests for publication, recovery, cache cost,
authorization, HTTP/MCP parity, backup/lifecycle, and adapter parity. It is a
mechanics demonstration only, not accuracy, completeness, supported scale,
SLO, migration, decommission, extractor-promotion, or release evidence.
`GATE2-V2` remains `NOT_ESTABLISHED`.

### Thrift field-zero development walkthrough

This retained specialized walkthrough is no longer part of `make dev` or
`make dev-api`. An explicit developer invocation may set
`PHEBS_THRIFT_FIELD_DEMO_REPO` to the committed
`docs/fixtures/thrift-field/t225-thrift-field-demo.bundle`. The server accepts
only that clean absolute bundle name, adds it as a generic Git source, and
enables `provisional_thrift_field_extraction` for that process. The bundle then
uses the ordinary sync → zoekt index → extraction path; it is not an HTTP or
UI proof-logic adapter. Ordinary `phebs serve`, operator configuration, and
release defaults remain unchanged and dark.

To exercise the path after starting that explicit invocation:

1. Use the logged first-run setup token if necessary, and wait for the
   `t225-thrift-field-demo.bundle` repository to show an indexed revision on
   **Repos**.
2. Open **Impact**, select **Field**, choose **Thrift**, and enter:
   `contract_scip_package_v1_5e8be5dc2df626800c5990885b6313c96246c7d7822864bb44be094edc1d7783`,
   `health.Meta_Health_Result`, and field number `0`.
3. Build the report. The resolved row is protocol `thrift`, domain
   `scip-thrift-field`, exact tier, and links to `consumer/use.go:6` at
   fixture commit `050c2c5204b24c1968f887310774eee57c46be57`.
   Analysis scope & gaps includes the complete visible
   `scip-thrift-field` coverage row; field 0 admits no other registered field
   domain.

The fixture's `receipt.json` pins its repository commit, bundle and index
digests, two-document/two-occurrence census, canonical identity, and citation.
Its `index.scip` is an authored rule needle whose asserted symbol shape is
separately checked against the T22.2 real-indexer fixture. This walkthrough
demonstrates routing, exact citation, and the digest-bound thriftrw join only;
it is not evidence of completeness, runtime use, compatibility, or extraction
accuracy.

Every query answer over this evidence cites a deterministic coverage
certificate (`coverage-certificate-v3`): the caller's visible repositories
with their indexed revisions, each domain's exact latest published run (run
id, extractor, commit, freshness, protocols, complete source-scope counters and
digest, candidate/exclusion accounting, unresolved/assertion/atom counts, and
gitlink boundary state), its latest extraction attempt (id, input revision,
extractor, status, and failure), latest durable outcome and bounded receipt,
and SCIP index availability. Immutable retained proof bundles keep their
original v1/v2 certificate bytes and remain readable through version-aware
canonical decoding.

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

The opt-in registers six read-only query endpoints when the Buf startup probe
succeeds (the first five remain available when compatibility is unavailable):

- `GET /api/find_operation_consumers?operation=/scope.Service/method`
  returns exact-object `CALLS_OPERATION` assertions from both registered
  consumer domains (`grpc-consumer` and `thrift-consumer`); a domain with no
  published run for a repository contributes an honest no-run coverage row,
  never an error.
- `GET /api/find_proto_field_references?lineage=<id>&message=<full-name>&field_number=<n>`
  resolves the canonical field identity in the `scip-proto-field` domain
  and remains protobuf-only.
- `GET /api/find_field_references?lineage=<id>&message=<full-name>&field_number=<n>`
  resolves the same stable identity across every registered field-reference
  domain whose protocol admits `n`. Field `0` selects Thrift; a common
  positive field can return separate protobuf and Thrift rows, each retaining
  its exact predicate, domain, run, and citation.
- `GET /api/find_kafka_topic_usage?topic=<literal>` returns exact-object
  `PRODUCES_TO_TOPIC` and `CONSUMES_FROM_TOPIC` assertions for one topic
  spelling (validated by Kafka's own naming bounds), and the bundle always
  carries `unresolved_census`: per-plane counts of supporting source sites on
  `UNRESOLVED_KAFKA_*` assertions for every frozen shape class, zeros included,
  plus independent producer/consumer published-run counts that say whether
  those zeros were measured. Counts are gathered through bounded
  authorization-scoped per-plane queries; clipped planes are explicit lower
  bounds. The census is topic-independent by construction — a non-literal
  topic cannot be matched to any literal — while whole-file extraction gaps
  remain in the coverage certificate. The endpoint never makes a completeness
  claim.
- `GET /api/get_extraction_coverage?domains=<comma-separated-domains>` returns
  coverage only; omitted domains select these ten domains: `grpc-caller`, `grpc-consumer`,
  `kafka-consumer`, `kafka-producer`, `proto-contract`, `scip-proto-field`,
  `scip-thrift-field`, `thrift-caller`, `thrift-consumer`, and
  `thrift-contract`.
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
the complete `lineage`, `message`, and explicitly present `field_number`
identity. An explicit `0` is a Thrift field; omission is not treated as zero.
Field reports use the same per-pack fan-out as `find_field_references`. `POST` accepts
the compatibility request above and is registered only when the Buf probe
succeeds; that state also advertises `contract-compatibility`. A successful
response is `contract-impact-report-v2`: the proof question,
`resolved_evidence`, `matching_call_evidence`, and `extractor_abstentions`
source rows, optional Buf
classification, every visible repository/domain coverage state, the complete
canonical coverage certificate, and the existing caveat. The conclusion names
the exact certificate digest and says only what was found within the stated
evidence scope. A bare operation query is deliberately a union across the
declaration-proven caller and legacy name-matching planes for both protocol
packs; every evidence row therefore carries its exact extractor domain,
protocol, classification, and lineage so equal protobuf and Thrift operation
spellings remain distinguishable. Empty evidence never establishes absence.

Authenticated `/api/version` responses advertise `contract-caller-map` when
the exact Caller Map service, page route, and exact-range citation route are
registered; anonymous version discovery omits it with every other capability.
The same authenticated
response advertises `contract-caller-comparison` when the shared comparison
service, HTTP route, UI sub-route, and MCP tool are available.

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
the complete `coverage-certificate-v3` and its digest. The server first
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
caller whose server advertises `contract-atlas`. Normal production and the
T30.7 `make dev` cohort obtain that capability only from the enabled
store-derived provisional evidence service. An explicit historical-fixture
invocation may instead set `PHEBS_CONTRACT_ATLAS_FIXTURE` to
`docs/fixtures/contracts/contract-atlas.json`. That validated adapter projects
one synthetic service only onto the unique currently visible repository whose
indexed commit equals the reviewed `repository_commit` in the fixture. Missing
or duplicate matches return no rows, so store order cannot attach a synthetic
claim to unrelated content. Its IDL source link opens at that exact indexed
commit. It does not seed,
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

Proof-bundle expiry resolves to `0` and is disabled by default. To age-bound
retained answers and their extraction-run pins, set a positive Go duration,
for example:

```yaml
proof_bundles:
  retention: 168h # seven days since the bundle was last materialized
```

Omission or `"0"` retains each `proof_bundle` row and its exact proof pins
indefinitely. Repeating an identical query refreshes only the store lifecycle
timestamp: the canonical JSON, content ID, and returned bytes do not change.
Existing bundles created before this option was available use their creation
time until next materialized, so enabling the policy also makes them eligible
for the same bounded sweeper.
Expired, missing, and unauthorized IDs all return the same `404` response.

When enabled, retention checks once at startup and hourly thereafter, draining
backlogs in bounded batches. Deleting a bundle and only its exact
`proof-bundle:<id>` pins is one transaction; pins owned by another bundle or a
release/migration checkpoint remain. Bundle expiry never deletes evidence.
The independent evidence sweeper decides whether a superseded run made
newly unpinned by that transaction is eligible for reclamation. A published
historical run is not an evidence-sweep candidate, so this setting never
directly or indirectly turns the selected historical-publication posture into
a bounded one.

Declaration and T13.1 operation-consumer lineage is deliberately machine-labeled
`provisional_repo_path_v1_<sha256>` and separates repository paths instead of
guessing descriptor identity. It prevents name-only cross-repository merges,
but a file move fragments lineage and an unrelated contract replacing the
same path can reuse it. The parser does not resolve imports, module roots, or
extensions; extension declarations fail closed. These facts must not drive
compatibility, migration, or negative-proof conclusions as though canonical
lineage had been established.

The candidate manifest permits a complete streamed census beyond the old
200,000-file/16 MiB retained-tree ceiling, but one extractor still admits at
most 200,000 planned repository-view paths and 16 MiB of aggregate path text.
Reads remain bounded to 10 MiB per source blob, a separate 64 MiB ceiling for
the fixed root `index.scip`, 512 MiB of distinct reads, 12,500 emitted facts,
and a cooperative 15-minute context deadline. A candidate
Go parser input is further limited to 4 MiB; a protobuf parser input is limited
to 4 MiB, 500,000 lexical tokens, and 128 structural levels. Neither in-process
parser can be preempted inside one parse call, so this is not yet a hard
CPU/memory/process isolation boundary. More than 100 placements of one content
atom also prevents publication. Symlinks are policy-gated during shared
candidate planning. A symlink selected only by a broad enumeration predicate
is skipped and cannot abort planning. An alias selected by a domain's required
predicate is resolved only from Git tree/blob objects at the pinned commit,
never through the host filesystem. Its relative chain is limited to 16 links
and must end at a regular path selected and required by the same domain; only
that independently enumerated final path is retained, read, counted, and
cited. Candidate alias paths consume no retained row.

Absolute or root-escaping targets, unsafe target bytes or paths, missing
targets, directories, gitlinks, cycles, oversized targets, and unsupported
modes are shared publication-integrity failures. Root `index.scip` and
attribution snapshot symlinks remain unconditional failures. Because every
configured domain consumes one commit/unit candidate publication, one such
refusal blocks all configured domains before any extraction run begins. This
is the deliberate shared-planner seam added by T30.4; once admission succeeds,
ordinary parser or domain publication failures retain T19.8's per-domain
isolation and retry behavior. Runs stamped with the prior
`gitlink-boundary-v1` policy are replaced on the next extraction so their
symlink semantics are never treated as current. Gitlinks are still recorded
as census-only repository boundaries and are not traversed, even when their
paths match a candidate suffix. A
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
`phebs_jobs_total` with `kind="candidate_manifest_job"` or
`kind="extraction_job"`.

Each domain logs `inventory started`, inventory file/candidate counts,
`extractor started`, emitted fact count, and either `published` or `aborted`
with the refusal. Contract Atlas, Impact, Topics, and Workbench surfaces remain
empty until their required domains have a published run; enabling their flags
does not bypass candidate-manifest or extraction admission. A repository view
beyond a planned-path, path-byte, candidate-read, aggregate-byte, fact, or
parser bound is unsupported as one extraction unit: the job retries and then
fails with that bound recorded rather than publishing a partial result.
Focused local domains consume their manifest-v4 unit projection; repository
and caller planes do not silently narrow.

Proof-aware retention checks at startup and hourly while idle, deleting
aborted, superseded, or stale unpinned staged runs in bounded transactional
steps; pinned proof/checkpoint runs and atoms still shared by another run are
retained. Rows migrated from the retracted pre-bound evidence schema, and any
ambiguous duplicate run identity, are quarantined from automatic cleanup for
explicit administrator resolution.

The store separates its exact writer generation from the stable published
evidence format, so a compatible writer upgrade cannot strand a pinned proof
bundle, and mixed-version or rollback writers fail closed. A reopen that
skipped intermediate writer generations retires their rows instead of
upgrading them in place: a skipped-generation published run becomes a
quarantined superseded row, freeing its publication slot so the next
extraction can replace it. Evidence migrations
require exclusive startup: never operate rolling mixed-version writers against
a remote endpoint — the supervised local deployment already provides the
intended single-writer lifecycle.

### Investigation storage and guided execution foundation

The Investigation, Revision, Run, Disposition, and Dossier storage plus the
guided-execution, consumer-ledger, ReviewItem, and export services are
implemented but production-unregistered: the normal binary registers no
Investigation workflow, core-view, or export route, and those surfaces return
404. Their immutability, authorization, and lifecycle guarantees live in
[PLAN.md](../../PLAN.md) and the Epic 16 tickets in
[BACKLOG_COMPLETED.md](../BACKLOG_COMPLETED.md).

For retained fixture tests or an explicit historical-fixture invocation,
`PHEBS_INVESTIGATION_FIXTURES` may name the five canonical synthetic files in
`docs/fixtures/investigations/`, which binds the read-only
`investigation-core-views` capability and the `#/investigations` page. T30.7
`make dev` does not bind them. These fixtures exercise presentation and
conformance states; they are not published evidence, a released pack executor,
a valid accuracy gate, or authority for external claims.

Verify an exported canonical dossier file without a running phebs service:

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

### Metrics


| Metric                         | Type      | Labels                                                             |
| ------------------------------ | --------- | ------------------------------------------------------------------ |
| `phebs_jobs_total`             | counter   | `kind`, `result` (`done`/`failed`/`requeued`/`released`/`reaped`)  |
| `phebs_job_errors_total`       | counter   | `kind`, `class` (`auth`/`oom`/`corrupt-shard`/`extract`/`generic`) |
| `phebs_index_duration_seconds` | histogram | —                                                                  |
| `phebs_index_shard_bytes`      | gauge     | —                                                                  |
| `phebs_focused_index_opened_blobs` | histogram | —                                                               |
| `phebs_focused_index_opened_blob_bytes` | histogram | —                                                          |


Plus standard Go process metrics. Scrape `/metrics`.

### Shutdown

SIGINT/SIGTERM drains gracefully: the HTTP server stops, claimed/running work
is released to `pending` without consuming an attempt, and the SurrealDB child
is stopped. Kill -9 remains covered by the stale-heartbeat reaper.

## Troubleshooting


| Symptom                                                           | Cause                                                                                                  | Fix                                                                                                   |
| ----------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------ | ----------------------------------------------------------------------------------------------------- |
| `start surreal child: exec: "surreal": executable file not found` | SurrealDB not installed                                                                                | see [prerequisites](./GETTING_STARTED.md#prerequisites)                                                                   |
| log: `zoekt-git-index not found — indexing disabled` or module-identity mismatch | binary absent, not built from the exact linked zoekt module pin, or built without Go metadata | `make build`; overrides must use this source line's exact `zoekt-git-index` build                      |
| log: `phebs-focused-index not found`                               | configured analysis units but focused child is absent                                                  | `make build`, or set `PHEBS_FOCUSED_INDEX=/path/to/phebs-focused-index`                               |
| log: contract compatibility disabled                              | Buf is missing/mismatched, or the OS sandbox cannot be enforced                                        | use `make build` or set `PHEBS_BUF` to the pinned v1.72.0 binary; install `bubblewrap` on Linux        |
| `listen tcp 127.0.0.1:3070: bind: address already in use`         | another phebs (or process) on the port                                                                 | stop it, or `-addr 127.0.0.1:3071`                                                                    |
| UI shows first-run setup                                          | no users and no OIDC provider                                                                          | copy the ephemeral setup token from the current process log; restarting generates a new token         |
| login succeeds but the UI immediately asks again                  | a `Secure` cookie was used over plain non-loopback HTTP                                                | serve HTTPS, or set `auth.cookie_secure: false` only for deliberate local development                 |
| API or MCP answers `401`                                          | no valid session/key, or a key was revoked/removed                                                     | create a named key in Settings and send `Authorization: Bearer <token>`                               |
| startup fails during OIDC discovery                               | issuer unavailable, wrong URL/private CA, or incomplete provider config                                | verify HTTPS reachability and discovery metadata; loopback HTTP is test-only                          |
| OIDC login says verified email is required                        | provider omitted `email_verified=true`                                                                 | configure the provider's email scope/claim mapping; phebs does not accept unverified email identities |
| code navigation says unavailable                                  | whole-repository posture has no root `index.scip`, or focused posture has no valid unit-bound designation | for focused scope, add an exact supporting SCIP path plus `typed_index`, then sync/reindex; never copy or relabel an unrelated root index |
| code navigation rejects an out-of-unit document                   | the designated SCIP artifact describes source outside the configured unit                              | regenerate the typed index for exactly the unit paths, or explicitly add the required source path to the reviewed unit and reindex |
| code-navigation/history link returns 404 after a repo update      | requested immutable commit is no longer present in the mirror or repo is unindexed/deleting            | use the current indexed commit from Repos, or restore/fetch the referenced object                     |
| GitHub sync reports a rate-limit wait                             | host requested a reset delay; phebs waits at most 1 minute and retries once, then uses the job backoff | use a PAT/App or reduce listing frequency                                                             |
| watch mode "doesn't see my edits"                                 | uncommitted changes are never indexed                                                                   | commit (or amend); the watcher reacts to HEAD and admitted-ref moves                                  |
| a repo temporarily disappears from search during repair           | its shard revision did not match committed DB state                                                    | wait for the forced index job; serving is intentionally fail-closed                                   |
| backup summary has a nonzero `omitted_*` count                    | invalid, incomplete, or orphan focused artifacts were excluded while precious state still succeeded   | retain the backup; restart or reindex so reconciliation replaces the omitted derived publication      |
| a focused repo rebuilds after restore                             | its focused generation was invalid/incomplete at backup time and was omitted as derived state          | let the forced replacement finish; the precious database export remains authoritative                 |
| startup logs `artifact reconciliation: … lifecycle=N`            | a prior process left private build/restore workspace or temporary-marker residue                       | no action if startup continues; phebs reclaimed only prior-process derived residue                     |
| extraction reports `candidate publication is incomplete`         | a stable candidate `.publishing` marker covers an interrupted or active publication, so even a no-op extraction refuses | let reconciliation finish; if the error repeats with phebs stopped, retain logs, move `$DATA/candidates` aside for diagnosis, and restart to rebuild derived state |
| focused evidence is unpublished after a same-HEAD scope or typed-designation edit | prior evidence belongs to the old unit digest or candidate receipt and exact lookup correctly refuses to reuse it | allow the forced index → candidate → extraction chain to finish; inspect the typed-input and candidate refusal rather than repairing evidence rows by hand |
| repository listing/startup reports `invalid committed analysis unit` | the stored focused claim is malformed or was tampered with, so repository reads fail closed instance-wide | restore a validated backup; without one, keep phebs stopped and escalate to the witnessed atomic row repair above |
| restore rejects a sparse tar member                               | the focused archive uses PAX/GNU sparse expansion, which phebs never accepts                            | recreate the backup with `phebs backup`; do not rewrite or manually extract the archive               |
| repo tagged `orphaned`                                            | no connection claims it anymore                                                                        | re-add the connection, or enable `sync.cleanup_orphans`                                               |
| sync fails with `auth: git …` and retries slowly                  | credential failure, classified `auth` (10 m backoff)                                                   | fix the token; reindex/restart to retry immediately                                                   |
| startup rejects a clone URL containing credentials/query data     | URL secrets are no longer persisted                                                                    | move HTTP credentials to `http_auth`; keep `url` credential-free                                      |

## Resolver namespace catalog contract

T37.1 adds the repository-shared declaration/resolver namespace contract.
T37.2 adds classified RPC caller postings and T37.3 adds Kafka
producer/consumer postings. T37.4 registers their bounded relationship
workload when resolver adapters are enabled, adds atomic repository/service
roots, migrates the relationship lifecycle owner, and adds exact composite
backup/restore. T37.5 still owns HTTP, MCP, UI, comparison, proof, and
Workbench consumers.

The catalog admits Go gRPC and Thrift resolver symbols from one exact prior
resolver generation. Identity is partitioned by language, protocol, and import
path and deliberately excludes service and analysis-unit ownership. Ambiguous,
unsupported, unavailable, and conflicting resolution remain explicit records;
operators must never repair these derived bytes or choose a conflict candidate
by editing a member.

Resolver-catalog v2 also separates extraction-run provenance from semantic
authority. Each declaration publication retains its validated `run_id`, and
the exact manifest integrity digest continues to cover that value. The
declaration-set, resolver-generation, and manifest-authority digests instead
canonicalize the same bounded declaration list with only `run_id` cleared.
Caller and relationship generations bind the manifest-authority digest; they
must not bind the exact provenance manifest digest. Repeating identical
extraction content under a fresh run therefore advances the resolver control
and exact manifest receipt without rekeying downstream semantic authority.
The partitioned downstream-upstream envelope follows the same v2 rule: its
serialized domains retain validated `run_id` provenance, while its semantic
digest clears only that field. Historical v1 envelopes continue to validate
with their original run-sensitive digest and are never rewritten as v2.

The supported resolver writer v1→v2 startup migration is destructive only to
derived current pointers: one transaction retires every v1 resolver pointer,
retires its current caller pointer, increments the affected caller revision,
and advances the writer marker. Existing candidate startup backfill then
drives candidate→resolver→caller→relationship rebuilding. Unknown marker or
mixed writer generations refuse startup permanently; do not edit the marker.
Historical v1 filesystem/archive bytes retain their v1 validation algorithm
and remain evidence, while normal lifecycle cleanup reclaims unreferenced v1
artifacts after v2 becomes current.

The cutover is O(indexed repositories) through the existing uncapped startup
backfill and can pay one bounded resolver materialization plus caller and
relationship recovery chain for every affected repository. At steady state,
v2 hashes at most 16 small declaration headers for each semantic identity and
performs one extra at-most-1-MiB manifest canonicalization when sealing. It
adds no source, Git, corpus, shard, or member reread and no new request, lock,
goroutine, child, or concurrency. Exact-current and bounded retention reads
project one additional fixed SHA-256 field. A run-ID-only replay may replace
the exact resolver provenance manifest and advance its control revision; that
can replay the existing bounded caller job and relationship callback, but the
resulting caller-generation and relationship semantic authorities remain
unchanged. Downstream v2 performs one exact provenance hash and one semantic
hash over at most 64 domain headers; only the latter clones and clears the
copied run strings.

When the registered relationship owner invokes the builder, unchanged namespace
members are revalidated and hard-linked, while only changed namespaces receive
new bytes. A complete validation pass precedes the atomic current-pointer swap.
Startup recovery may complete a marked generation only after repeating that
pass; invalid derived bytes leave the prior pointer authoritative and should be
rebuilt from the exact upstream resolver authority. Sparse keyed reads validate
the selected namespace and repeat the current pointer fence. A corrupt sibling
does not authorize fallback or guessing and does not relabel the selected
namespace complete.

An explicit T37.2 build joins one fully validated observation publication to
one exact resolver catalog. It walks observed source records once, reads each
referenced resolver namespace at most once per protocol, and publishes one
path-, revision-, span-, object-, and content-bound occurrence per placement.
Exact receiver authority yields `resolved`; a unique method-only fallback is
kept distinct as `name_match`; dynamic or unsupported receivers, generated
inputs, ambiguous provenance, and every resolver abstention remain classified
`unresolved`. The builder never parses source again, guesses through a
conflict, reads Git or the store, or starts a child process.

Posting members are split into 256 deterministic buckets per protocol. An
operation lookup validates only the selected member; unresolved inspection
uses the protocol's fixed sentinel bucket. Complete validation still runs
while staging and again after installation, and an identical prior generation
is reused only after that validation. These derived generations have no
independent product authority. T37.4 consumes them only through their exact
generation/root digests, so operators must not edit, promote, or repair them
by hand.

T37.3 adds the parallel unregistered Kafka topic-posting contract. Producer and
consumer evidence remains in separate planes. Literal and same-file-constant
topics retain their decoded source spelling; that spelling is not a broker,
cluster, environment, deployment, or runtime identity. Dynamic expressions,
cross-file or mutable identifiers, invalid literals, and unsupported shapes
remain unresolved records with no topic authority. Exact path, revision,
object/content, byte/line span, library/shape, and source-role citations remain
part of each posting identity.

An explicit Kafka build rereads the validated observation publication once,
writes deterministic topic buckets, and performs complete stage and
post-install validation. Keyed topic and unresolved reads validate only their
selected at-most-128-MiB member after the at-most-8-MiB root. The build starts
no parser, Git or store read, resolver lookup, network request, or child.
The T37.4 worker runs one durable memory-class item after exact observation,
resolver, or catalog publication. It holds the shared mutation/backup lock,
leases the observation generation, pages verified service states, and rechecks
all precious and derived controls before moving the relationship pointer.
The accepted-service desired/incarnation-set digest makes catalog A after
A→B→A distinct from an earlier incarnation even when catalog bytes recur.
Source and declaration-target membership may resolve to zero, one, or many
services. Shared, unowned, proposal, conflict, and rejected claims remain
explicit. One failed service partition does not block repository authority or
unrelated service members, and `all_services_complete` stays false until every
named accepted service is complete or explicitly empty against the same root.

The worker declares one GiB and applies pre-growth resident charges: 128 MiB
resolver, 192 MiB RPC, 128 MiB Kafka, and 512 MiB relationship data. It starts
no Git, network, or parser child. When enabled, its idle scheduler performs one
planner/reaper and one bounded claim probe per second. Ordinary search,
evidence, directory, HTTP, MCP, and UI requests perform no relationship work.

Lifecycle preserves current plus one rollback root and active reader leases,
renames an eligible root before bounded deletion, and deletes a component only
when no surviving relationship root or resolver control references it.
Backup-manifest v7 includes only fully validated current composite authority;
corrupt or in-flight relationship state is counted as omitted and rebuilt
after restore rather than blocking the precious database export. Operators
must continue to treat these roots as derived immutable authority and never
edit or promote them manually.

When resolver adapters are enabled, T37.5 exposes the shared exact reader over
HTTP and MCP. `GET /api/service-relationships` (MCP
`list_service_relationships`) accepts one service key, an optional explicit
repository set, the `all`, `dependencies`, `callers`, or `topics` view, and
optional kind/plane/lookup filters. Omitting repositories selects the complete
visible set and refuses when that set exceeds 32; selecting repositories
explicitly is the bounded remedy. Permission resolution completes before any
relationship pointer or member is opened. Responses distinguish nonempty,
empty, failed, unavailable, and truncated states and repeat exact root,
incarnation, placement, projection, posting, object, digest, and span
authority. Kafka topic values remain source spellings, never broker/runtime
identity.

`GET /api/service-relationship-comparison` (MCP
`compare_service_relationships`) requires the generation and root digest for
both sides. Its cursor pins both immutable generations for five minutes and
reports added, removed, and unchanged exact references. A stale, expired,
reauthorized, or incarnation-mismatched cursor refuses with a restartable
conflict; it never advances against replacement authority. Each response is
capped at 100 rows and 2 MiB. Initial list and comparison scans are explicitly
truncated at 20,000 and 40,000 retained reference identities respectively.

Rows carry an opaque citation accepted by
`GET /api/service-relationship-citation` and MCP
`read_service_relationship_citation`. A citation is valid only while its
reader lease remains in the bounded eight-binding/80,000-reference cache
(normally five minutes, possibly shorter under idle-binding pressure). It
reauthorizes the repository, rejects Git alternates, verifies the exact
immutable object and whole-content digest, and returns only the published byte
span. At most eight relationship requests and two citation blob reads execute
concurrently. Expiry is an expected bounded-state transition: rerun the list or
comparison to mint a current citation.

Proof bundles may carry an optional source-free relationship-root coverage
annex; retained bundles without it keep byte-identical v1 content. Workbench
Impact binds the same exact root-set digest into its continuation cursor. A
visible set above 32 produces an explicit relationship coverage gap rather
than a partial root sample. These annexes read root controls only and do not
walk members, source, shards, or Git objects. None of the reader, comparison,
proof, or Workbench surfaces establishes relationship completeness, runtime
topology, migration/decommission safety, an SLO, or release authority.

T38.4 projects that same Workbench Impact reader through MCP as
`get_change_workbench_impact`. The adapter performs no relationship join or
classification: it forwards the exact HTTP request semantics, preserves the
shared page, authority, gap and error types, and applies the same final 8 MiB
encoded-response fence. Cancellation reaches the shared read. All six
microservice MCP tools are read-only and closed-world; their evidence cannot
authorize an agent Decision, task completion, migration, decommission, or
release action.

Use [Microservice change workflow](./MICROSERVICE_WORKFLOW.md) for the complete
neutral add/modify/migrate/retire operator journey. Start its first run with a
fresh data directory so every authority is published through the ordinary
pipeline; repeat the same run against that directory to verify restart/no-op
behavior. A corrupt or interrupted derived generation remains unavailable
until normal recovery or rebuilding restores a completely validated current
generation. Do not delete control files, edit publication pointers, reuse an
expired citation, or bypass a failed generation fence to make the demo pass.
Epic 39 owns measured replay, operating gates, and any release decision.

## Developing phebs


| Target               | Does                                                                                                                                                    |
| -------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `make dev`           | build UI + pinned whole/focused zoekt and Buf children; bind the retained neutral `orders-service` bundle and T33.5 service catalog through ordinary indexing, evidence, catalog, default-on bounded lifecycle, caller-overlay, and store-derived Workbench paths; expose administrator lifecycle pressure/owner status in Settings without seeding historical rows; explicitly disable synthetic response fixtures; run with embedded UI |
| `make dev-api`       | backend-only loop over the same neutral focused-service/catalog cohort and real store-derived paths (placeholder UI page, fast)                                           |
| `make build`         | version-stamped `./phebs` plus same-module `bin/zoekt-git-index`, `bin/phebs-focused-index`, and `bin/buf`; pass `VERSION=vX.Y.Z` for a release                                    |
| `make clean`         | from a verified phebs checkout working directory, remove `phebs`, `coverage.out`, the three named `bin/` children, `ui/dist`, and reserved `dist/.build-*`, `dist/phebs-*`, and `dist/.phebs-*.tmp-*` outputs; preserve data, configuration, dependencies/caches, other `bin`/`dist` entries, and custom release roots outside those namespaces |
| `make release`       | assemble a new host-native `dist/phebs-<version>-<target>` directory and canonical digest manifest; requires v-prefixed `VERSION`                       |
| `make verify-release` | reject any manifest, payload, mode, symlink, missing-file, or extra-file drift in `RELEASE_BUNDLE`                                                      |
| `make smoke-release` | run the verified bundle from empty state through auth, sync, index, search, pinned browse, and default-dark Contract Atlas checks                       |
| `make test`          | verify generated glossary projections, then `go test ./... -timeout=25m`; store/sync/indexer tests need `surreal`, the timeout matches CI's integration-suite allowance, and child tests build pinned zoekt and Buf binaries |
| `make t20-closure`   | run the opt-in Epic 20 empty-data scale/failure journey and write its reference-machine receipt to `/private/tmp` by default                       |
| `make ui-test`       | Vitest UI tests (`cd ui && npm test`) — streaming, keyboard nav, facets, file tree                                                                      |
| `make lint`          | verify generated glossary projections, then run golangci-lint                                                                                          |
| `make ui`            | production UI build only                                                                                                                                |
| `make db-server`     | SurrealDB in server mode via docker compose (testing only)                                                                                              |
| `make verify-glossary` | regenerate the canonical Go, TypeScript, schema, MCP, and MANUAL glossary bytes in memory and reject checked-in drift                                |

`make clean` is not an index, candidate, evidence, backup, or database cleanup
command. It never reads configuration or follows `DATA`/`RELEASE_ROOT`, and no
other target invokes it implicitly. The three listed `dist` prefixes are
reserved build-output namespaces; `dist/release` packaged archives are
preserved. Run it after changing to the repository root. A `make -f` invocation
from a non-checkout directory refuses without mutation; from another verified
phebs checkout, that working checkout remains authoritative and only its owned
outputs are removed.

The hosted release gate runs four independently visible local-equivalent
targets: `make ci-static`, `make ci-go`, `make ci-race`, and `make ci-ui`.
`make ci` runs all four sequentially. The live Go targets require the exact
pinned `surreal` on `PATH`; hosted CI downloads the exact 3.2.0 Linux archive
and verifies its committed SHA-256 before any store test starts.

An annotated release tag may be created only when its exact `main` commit has
a successful **push** run of all five named jobs in
`.github/workflows/ci.yml`, including the release job; the tag commit and run
SHA must match byte-for-byte, and tags are never force-moved. Release notes
must link the run and checksum and state that Contract Atlas and proof
features are default-dark, provisional, and do not establish the closed
`NOT_ESTABLISHED` accuracy gate.

The canonical Change Workbench vocabulary is
`internal/glossary/glossary.json`. Run `go generate ./internal/glossary` after
an approved source change; do not edit its generated Go, TypeScript, schema,
MCP, or marked MANUAL projection directly. `make verify-glossary` is
network-free and is also part of the ordinary test, lint, and static CI gates.
The generated TypeScript and MCP description inputs are contracts for later
tickets; T21.4 does not render help or register tools.


Live UI development: run `make dev-api`, then `cd ui && npm run dev` — Vite
serves on :5173 and proxies `/api` to :3070.
### Extraction progress

`GET /api/extraction-progress?repository=<name>` returns a bounded operational
view of partitioned extraction for one authorized indexed repository. The
response reports `unavailable`, an active/settled/superseded schedule state, or
`current`, together with total, materialized, pending, running, succeeded,
failed, domain, and current-domain counts. Authorization denial is a 404 and
precedes extraction-control reads. A schedule or repository-authority change
during the read returns 409; malformed controls or an invalid projection return
500.

The endpoint is diagnostic, not historical authority. It reads two exact
schedule points, one small generation inventory, and at most one current
pointer per configured domain. It does not read candidate members, source or
observation content, partition results, domain roots, or evidence payloads.

The repository-status extraction job is also diagnostic orchestration state,
not extraction-generation authority: its projection intentionally contains no
candidate, source, observation, plan, schedule, or generation identity. Under
V15, if a generation-bound schedule is active, keep waiting — live scheduler
actors can still settle and publish without the job; if it is settled with
failed partitions and complete counters, use that schedule terminal; and if it
is fully `current`, validate the exact extraction and downstream
caller/relationship authorities instead of letting the unbound job row poison
them. In every other schedule state — `unavailable`, settled successful
awaiting promotion, superseded, or incomplete settled counters — no actor
remains to finish the pipeline, so the failed or canceled latest job is
conclusive and stops the ceremony with a typed terminal rather than pending to
the four-hour deadline. The job row's exact validated terminal refusal follows
the same rule: it substantiates a bound-refused stop only in those conclusive
states. A V15 job-plane stop additionally confirms on a second identical
five-second probe, so a poll that races the schedule enqueuer or the final
promotion write converges instead of sealing a spurious terminal. The job
status and attempt count remain in operational evidence. Fresh ceremony plans,
observations, and receipts use V15 for this rule; V14 retains its historical
job-first predicate and validation, and receipt coherence checks are
outcome-restricted, so waits stopped for other reasons (diagnostic limit,
deadline, server exit) retain a terminal-shaped final projection as evidence
without failing to seal.

The ordinary path adds no operation kind or work. On the exceptional V15 edge
where a failed/canceled job accompanies a current schedule, the inspector now
continues into existing bounded validation that the former terminal skipped:
exact extraction-authority validation that globs at most 64 retained generation
controls; each matching generation status opens up to 64 domain plans, checks
at most 490 expected partition-result records per domain, and reads domain-root
and current authority, stopping at the first complete generation (about
2,007,040 result checks at the full envelope). It then makes one caller-
generation progress request and the applicable relationship-root and service-
catalog authority checks. The full extraction scan is memoized on the
extraction probe digest for the life of one convergence wait, so while the
schedule and job projection are unchanged the five-second poll re-reads one
digest instead of repeating the scan; the scan re-runs only when the probe
digest changes. It adds no source/content, candidate-member, corpus, shard, or
Git read, and no write, transaction, child, goroutine, lock, retry, startup,
sync, no-op, or publication-transition work.

### Relationship convergence classification

T40.13 V16 and later plans use the V16 relationship observer contract. After
caller authority is current, the inspector no longer treats every
relationship-root error as an undifferentiated control failure. It records one
source-free boundary: `current_control`, `authority_incomplete`,
`authority_mismatch`, `successor_absent`, or
`successor_settled_without_current`. The class is part of the probe digest and
is retained in the convergence transition, observation, and receipt.

An absent or mismatching root is paired with the current bounded
`service-relationship` schedule. Active schedule authority stays pending. An
exact settled failure retains its typed refusal or terminal outcome. Settled
success without matching current authority, malformed schedule/control state,
or an absent successor is terminal. A non-refusal terminal is accepted only
after the same exact class/root/schedule probe repeats on the next five-second
poll; the V16 receipt records exactly two confirmations. This distinguishes a
real stranded pair from the enqueuer, current-pointer, and final-settlement
races without waiting for the four-hour deadline.

The exact extraction scan is cached on both the extraction probe digest and the
relationship generation against which it was checked. A newly appearing or
changed relationship root forces one fresh extraction/root parity scan; later
polls over the same pair reuse it. That scan has the existing worst-case
envelope: at most 64 retained generation controls, each with up to 64 domain
plans and 490 partition-result records per domain plus domain-root/current
authority, stopping at the first complete generation (about 2,007,040 result
checks at the full envelope). A missing/mismatching root adds one bounded
current-schedule projection per poll. A stable valid root adds no schedule read
and no repeated scan. There are no new production requests or writes, source,
candidate-member, corpus, shard, Git, or content reads, child processes,
goroutines, locks, retries, startup/sync/no-op transitions, or storage.

`t40r1-neutral-27` remains immutable V15 stopped evidence. Its source-free
package is
`sha256:291336d632150b1c0101da65ab2621f7c410d218a08ef53da1d065c5c2a1a758`.
Both cold profiles and structural warm-noop converged; structural delta B moved
from relationship `pending` at 3,250,113 ms to generic `control` 4,974 ms later
and retained that unclassified shape through the deadline. Its retained
extraction counters are the last extraction-stage projection, not proof that
the pipeline remained at extraction after inspection moved downstream. V16 is
a prospective classification/revalidation correction; it does not
retroactively pass or diagnose neutral-27's destroyed private state. V15 and
earlier receipt validation and serialized bytes remain unchanged.

### Derived-publication lock liveness

Extraction source leases include the repository work lock. For a newly
executed partition, that lease now ends immediately after the immutable result
file and its directory sync are durable. Domain assembly, the exclusive
derived-publication fence, current-pointer publication, and downstream
settlement therefore run without the repository lock. Replayed installed
results still open no source lease. This preserves exact result and authority
validation while removing the repository-lock-to-publication-fence wait edge.

Artifact reconciliation still holds the shared backup/mutation fence across
its audit, but a repository lock is now only a 250-ms probe. The first busy
repository stops the audit and releases the shared fence. Startup treats that
as a failed trust-boundary audit. A runtime sync job classifies the same busy
edge as a delayed, non-attempt-consuming deferral: the exact leased job returns
to pending behind its base interval and already-ready siblings retain the
runner's bounded drain opportunity. Reconciliation never mutates filesystem
artifacts for a repository whose lock was not acquired. If an orphan-deletion
branch reaches the busy lock, it has already performed its existing
mark-deleting write and six bounded pending-job cancellations; rollback
reactivates the row and may enqueue the existing forced recovery successor.
Uncontended audits perform the same inventory and mutations, with one
immediately canceled timeout context per repository-lock acquisition.

A late runtime cleanup collision occurs after the sync handler has already run
the existing connection sync/fetch, index-job enqueue, and connection-membership
write, as well as every audit scan and mutation preceding the busy repository.
Each eligible deferred execution can repeat all of that bounded work until the
lock clears; the deferral does not consume the three-attempt failure budget, so
the repetition count is controlled by lock lifetime and the configured sync
cadence rather than an attempt ceiling. This path adds no new operation kind,
but operators should not interpret the 250-ms probe as the total cost of a
deferred sync turn.

Relationship unavailable transitions and relationship build chunks acquire
their shared mutation fence through 25-ms probes with 10-ms retry delays and a
five-second total deadline. Immediate acquisition remains one lock call. Under
continuous contention the operation makes at most about 143 probes and returns
a typed deferral. An unavailable-authority reconcile returns that outcome to
whichever lease-owning runner invoked the publication callback: resolver and
caller callbacks defer their ordinary job, while observation and extraction
callbacks defer their generation chunk, all without consuming an attempt. A
direct startup reconcile remains a reported failure because no runner or
scheduler lease owns it. A relationship build returns the same outcome to the
generation scheduler. The scheduler moves the exact chunk from running to
delayed pending, decrements the repository-wide running token, and leaves its
attempt and materialized counters unchanged. The chunk is ineligible while `not_before` is
in the future, and another ready stage may claim the released token. The delay
is the scheduler store-call timeout plus one poll interval, leaving one full
poll interval after any successful response even when the store call consumed
its timeout envelope; its absolute fence is recomputed inside every Surreal
conflict retry. The token stays intentionally
repository-wide, and attempt/backoff, memory, descriptor, and concurrency
ceilings are unchanged.

Startup service-catalog reconciliation visits the installation repository
list serially and invokes relationship reconciliation for each current,
published, or legacy-imported selected repository. That existing list has no
installation-wide cardinality cap. A current matching relationship root still
returns before mutation-lock acquisition. An unavailable transition under a
persistent exclusive fence may now spend up to five seconds per eligible
repository before recording that repository's failure and continuing, so the
startup edge is O(repositories) and can add up to five seconds times the
eligible repository count. Startup retains no waiting goroutine or repository
token after each bounded failure.

The extraction path adds no read, write, hash, heap allocation, or child process
and shortens one held lock. Uncontended reconciliation adds no scan or write and
bounds its opposite-order wait; the busy orphan branch pays the bounded
control writes and rollback above. Relationship contention adds bounded lock
syscalls plus one same-row deferral transaction under a scheduler or ordinary
job owner. A deferred upstream publication callback may repeat that owning
handler's existing bounded work at its runner or scheduler cadence; direct
startup pays the serial bound above. No goroutine, lease heartbeat, retry
successor, failed attempt, or repository token remains pinned by build-lock
acquisition.
These are production liveness fences, not evidence that neutral-27 encountered
a particular private lock state.

New non-Kafka extraction plans use the version-2 domain-result contract: each
partition retains the 64-MiB candidate-member reservation ceiling while the
cumulative domain input is bounded independently at 1 GiB. New
`kafka-producer` plans use version 3 with measured aggregate reservations of
262,144 facts, 524,288 rows, 262,144 references, and 256 MiB each of canonical
and encoded output. Version 3 separately retains 64-MiB per-partition canonical
and encoded backstops. Existing version-1 and version-2 plans keep their exact
serialized identities and original validation during restart, recovery,
archive, and downstream reads. This transition requires no operator migration
or configuration change.

### T40.13 V17 interruption lifecycle evidence

The retained `t40r1-neutral-28` source-free package is immutable V16 stopped
evidence, not a gate pass. Its package digest is
`sha256:ba1a583b08494d932ee1e769161e1e4ee9343720b72d8fc30b26245f98597f5b`
and its exact source is `26ca6d7e0375eb82be8731a4a6779a88107b8d86`.
Cold, warm-noop, delta B, and return A converged; interruption stopped as
generic `operational_failure`. V16 did not retain an interruption substage, so
operators must not infer whether backup, restore, ephemeral-control discovery,
first stop, or restart failed.

Fresh T40.13 freezes use V17. After the live interruption backup is restored
and exact A is verified, the harness deliberately advances the local semantic
source A→B. It consumes generation lifecycle reports incrementally from the
measured start offset of that server log. The trigger is satisfied only by an
`extraction-partitions` start whose immutable generation binds exact revision B
and whose current local-store projection still matches identity, repository,
stage, generation, attempt, and `running` status. The stop is commanded at
that durable lease boundary; because the graceful drain may settle or release
the selected lease before process exit, the receipt does not assert an
interrupted lease. Instead, after the offline B→A return and restart, the
harness re-projects the trigger chunk and requires a recovered, non-running
fate within a bounded window, recording it as `trigger_recovered_state` —
a lease still running after restart fails the phase as a recovery regression.
The restart must also recover exact A authority, retain no partial derived
publication state (including the hashed `relationship-publications` layout),
and carry its `interruption-restart` startup observation: a substage at or
past restart convergence refuses to validate without it, closing the
neutral-28 missing-startup gap. A B-bound schedule that settles before any
lease is selectable stops the wait immediately with a typed unsatisfiable
result instead of idling to the trigger deadline, and one transient store or
authority read failure retries on the next poll rather than aborting the
ceremony.

The V17 observation and receipt record the last closed interruption substage
and the trigger's source-free stage, generation digest, chunk digest, explicit
attempt, phase-relative wall time, and recovered post-restart state. They
exclude repository paths, source
content, worker identity, lease tokens, store timestamps, raw errors, logs,
credentials, and responses. V17 reuses the V16 relationship inspection
contract. The pointer-backed diagnostic is absent from V1–V16, preserving
their validation and serialized bytes.

This is ceremony-only work. It removes the V16 once-per-second shallow
filesystem marker scan and adds one local semantic A→B update plus one offline
B→A update, each through one bounded `git update-ref` child. The existing
production pipeline runs only until the first exact
running B extraction lease. Log input is read in 64-KiB increments, with a
1-MiB partial-line and 400,000-report per-poll ceiling. Lifecycle stages are a
closed set; non-extraction reports are validated and discarded. Every 250-ms
poll examines only still-active extraction reports, revalidates each exact
generation/revision binding, and performs one bounded source-free lease read
per candidate until selection. Settled reports and authoritative non-running
states remove candidates. Independently of stale prior-revision lifecycle
entries, one current-schedule progress projection runs at most every five
seconds; an exact B-bound settled schedule seals
`interruption_trigger_unsatisfiable` immediately. After restart, recovery opens
one local source-free reader and performs at most one 30-second-bounded trigger
lease projection per second for five minutes before closing it. A successful
projection is followed by one bounded filesystem pass over the three derived
roots (at most one repository and 4,096 controls per directory, including the
observation-v2 directory) and the one-repository/4,096-control hashed
relationship-publication directory. The receipt adds one fixed-size diagnostic
object.

There is no new production request, sync tick, startup/restart owner, retry or
no-op path, publication transition, authority, API/store schema, cache, lock,
worker concurrency, admission or safety bound, child process, disk allocation,
or release claim. A failed V17 run should be classified from its retained last
substage; it must not be relabeled from the previous phase's last progress
snapshot. The substage vocabulary is one closed ordered list shared by the
recorder and the validator, so a mismatch can cost precision but never an
unsealable observation; `recovery_verification` covers the post-restart lease
and partial-state checks and `teardown` covers final metering, the last server
stop, and safety enforcement, so a post-convergence failure is never
misattributed to restart convergence. Lifecycle log validation is
version-gated: V16 execution keeps its frozen extraction-only fatal contract,
while V17 checks structure fatally and validates-then-discards vocabulary
drift, because the store lease projection — not the log — is the selection
authority. Neutral-28 remains `unclassified` and cannot pass retroactively.

### T40.13 V18 authoritative interruption trigger diagnostic

`t40r1-neutral-29` remains immutable V17 `unclassified` evidence. Its verified
source-free package is
`sha256:4ba484f9b22902edda41179d0b790cec018c2ecc12fa7baaed66049c8315fcd8`;
it binds source `5707856b2ce72404fff7eca34a384e69b9e1169b`, plan
`sha256:c18621ef92664c042103592dfbfd12918a7eb4e974fa0f6a9041168b60ba22b7`,
observation
`sha256:d44420f7e179c54d76bb125a1396b90aaf3ac3ce60a59b36279d8d544ce0c825`,
and receipt
`sha256:3049b7a452f1932bfcf21d734cb7afb8c4a61a4a9f3ab202aa26eb9af8cfdd51`.
The interruption stopped at `active_lease_wait` without trigger fields. That
record proves only that V17 did not select a lifecycle-discovered B extraction
lease within 90 minutes; it does not prove that such a schedule existed or
identify an upstream boundary.

Fresh plans use V18. Both local-store readers, the lifecycle cursor, and the
exact inspector are opened before the A→B source transition. The trigger reads
one running chunk directly from the
exact current `extraction-partitions` schedule, then rechecks the current
schedule, exact chunk identity, and local runtime before accepting it. The
generation must still bind revision B. Lifecycle logs remain incrementally
parsed for closed structural corroboration, but their start/settle timing is no
longer discovery authority. Alongside lease selection, the existing exact V16
inspector records the last source-free stage, closed class, probe digest,
phase-relative wall time, and digest-change count. A terminal projection seals
`interruption_progress_terminal`; a no-trigger deadline with retained progress
seals `interruption_trigger_deadline`; complete convergence or an exact
B-bound settled extraction schedule seals `interruption_trigger_unsatisfiable`.
No raw error, response, path, worker, token, timestamp, log, credential, or
source is retained. V1–V17 validation and bytes remain unchanged.

Lease selection is independent of the exact inspector: one inspector and one
schedule-progress projection may run on their own local-store connection while
the selector continues sampling. The goroutine is canceled and joined before
either connection closes. Stale-worker uses the same exact-current selector
instead of lifecycle timing. This is ceremony-only read/evidence work. The log keeps its 250-ms cadence and
64-KiB read, 1-MiB partial-line, and 400,000-report-per-poll bounds. The exact
current-running selector runs at most four times per second, with a two-second
call bound, and performs bounded
current-pointer, one-row, current-schedule, identity, and runtime-fence reads.
The exact inspector runs at most every five seconds with a 30-second call
bound. Before extraction it performs the existing bounded authority reads and
requests. At the extraction boundary its existing worst envelope is at most 64
generation controls × 64 domains × 490 partition-result slots, stopping at the
first complete generation and retaining the relationship-pair memo for the
life of the inspector. It adds no source/content/candidate-member/corpus/shard
or Git read and no write. Five fixed-size scalar/digest fields are added to the
V18 observation/receipt. Production request, sync, startup, retry/no-op,
publication, worker, authority, schema, API, cache, lock, concurrency, bound,
memory/disk ceiling, and release behavior are unchanged. Neutral-29 cannot
pass retroactively; a fresh freeze and execution require separate approval.

The opt-in production-binary readiness rehearsal uses distinct fresh semantic
repositories for interruption and stale-worker, then retains the structural
delta/return, live-backup/offline-restore, lifecycle, and authorized-query
checks. Its first run after this correction selected the lease but then exposed
a failed B extraction schedule during the return-to-A restart. That is a
production stale-authority retry blocker to correct on a stacked branch; do not
weaken the rehearsal or freeze while it fails. Pressure and scale remain
outside this small-corpus rehearsal and receive no claim from it.

### T40.13 V19 prior-gate recovery closure

Fresh freezes use V19 after the combined production-binary rehearsal closed
the earlier-gate defects. Extraction authority-fence contention is bounded to
five seconds using 25-ms probes and 10-ms waits; it returns the exact generation
chunk to delayed pending without consuming an attempt. Reconciliation replaces
a stale active extraction schedule even when exact prior domain authority is
already current. Whole-search return-to-A reactivates validated retained A
authority instead of rebuilding it, and a caller publication pointer for a
different generation enters normal repair rather than completing the job.

Restore treats schedules, current-schedule pointers, repository generation
tokens, and extraction-domain roots as restartable state. After database import
it clears those tables and lets ordinary recovery remint them; immutable
outcomes remain retained. Offline verification still proves the exact archived
source, search, observation, caller, and relationship bytes. Online validation
then compares product semantics while allowing caller and relationship control
identities to be reminted. Current extraction progress permits bounded retry
materialization above the logical partition total, but still requires zero
pending/running/failed, exact successes and current domains, and the frozen
attempt ceiling.

The ordinary uncontended/no-op path adds no query. Contention adds only the
bounded probes and one non-consuming deferral. Search reactivation performs one
retained-generation validation and pointer transition under the existing
mutation lock, with no Git/source read, zoekt child, or shard rebuild. Restore-
only cleanup performs bounded full deletes over four generation-schedule tables
and extraction-domain roots. The ceremony-only stale-worker diagnostic performs
one exact current-schedule supersession write. While the opt-in rehearsal is
pending, source-free schedule diagnostics read one bounded progress projection
at most every 30 seconds; asynchronous partial controls are allowed five minutes
to clear at 250-ms cadence before failure. The combined semantic, structural,
and stale-worker rehearsal passed in one process. This establishes readiness
mechanics only—not pressure, scale, SLO, accuracy, release, migration, or
decommissioning—and does not pass any prior ceremony retroactively.

### T40.13 neutral-31 caller publication repair and V20 terminal fence

Neutral-31 is immutable V19 stopped evidence. Its verified source-free package
is `sha256:7e6301197b505dfd07718e97caf2565ead7037d70fb65a5d3098f5b91ab72543`
at source `5434bb382182251f356040eee15ac8766e2292d2`. Structural cold settled
extraction, reached caller generation, and then retained one unchanged caller
projection until the four-hour convergence deadline. A synthetic
production-binary rehearsal localized the stop without retaining private
source: all eight caller pairs and admission were complete, but the publication
transaction rejected the canonical downstream-authority V2 envelope because
its store-side validator still understood only V1.

Caller publication now uses the shared canonical V1/V2 validator and requires
the decoded authority to be usable and to match the generation repository and
upstream digest exactly. A V2 publication therefore commits through the same
transaction and ownership fences as V1; malformed provenance, unknown schemas,
repository drift, digest drift, unusable roots, noncanonical bytes, and
envelopes above the shared 256-KiB ceiling still fail closed. Historical V1 authorities remain
valid. This is a validator compatibility repair, not a new authority or
migration.

Fresh ceremony schemas advance to V20. If caller progress is `missing` or
`stale` while its exact pair projection is complete, all settled pairs
succeeded, and none were refused, V20 treats the absent publication as a
terminal candidate. It seals `caller_generation_terminal` only after the same
source-free probe repeats five seconds later. A publisher that wins the race
changes the probe and convergence continues. V19 and earlier retain their
historical pending classification and receipt validation.

Publication attempts validate the at-most-256-KiB upstream envelope.
V2 performs a canonical decode/marshal plus one provenance and one semantic
SHA-256 over at most 64 bounded domain headers before the existing transaction;
V1 retains its semantic hash. There is no new query, lock, transaction, retry,
startup scan, sync tick, child, source/Git/content/candidate/corpus/shard read,
cache invalidation, or concurrency. Ordinary convergence polling is unchanged.
Only the complete/all-success-but-unpublished V20 edge adds one caller-progress
request after five seconds. The upstream ceiling is the one production bound
changed by the subsequent maximum-shape audit; all other safety ceilings remain
unchanged. Neutral-31 does not pass retroactively, and this correction alone
does not authorize integration, freeze, execution, release, or any scale/SLO
claim.

### T40.13 caller maximum-shape and restart-repair audit

The post-neutral-31 production audit found that the downstream-authority model
admits a canonical 64-domain envelope larger than the store's former 64-KiB
limit. The exact maximum-shaped model fixture is 138,832 bytes. Authority
validation, caller-generation construction, and caller publication now share a
256-KiB ceiling, so every structurally valid V1/V2 authority fits while an
unrelated opaque payload remains bounded. A real Surreal publication test
commits and reopens the 64-domain V2 envelope, and a historical V1 envelope
passes the same transaction and current-pointer path.

The same audit seeded the durable neutral-31-shaped restart state: one failed
caller job at its final attempt, complete upstream candidate and resolver
authority, and no caller pointer or publication marker. The existing candidate
startup backfill exact-republishes its current pointer, the candidate
transaction repairs the resolver successor, and the resolver current/no-op
callback creates a fresh zero-attempt caller successor while retaining the
failed history row. No second caller-wide startup scan is added. A caller
current-pointer callback failure remains retryable, and its next warm turn
replays relationship reconciliation before the caller job can finish.

V2 validation can decode and canonically marshal at most 256 KiB twice around
the existing store transaction and performs the existing provenance and
semantic SHA-256 passes; V1 performs its historical semantic pass. The raw
authority is retained in the caller pointer and scalar startup summary, so the
existing installation-wide pointer-summary query can carry up to the product
of its 65,536-pointer ceiling and this per-authority ceiling (16 GiB before
JSON/SDK overhead) in the theoretical all-maximum shape. That pre-existing
all-pointer startup design is now explicitly recorded as a follow-up capacity
boundary; the T40.13 ceremony uses one repository and does not establish that
installation envelope. Ordinary nine-domain publications remain far below the
new ceiling and add no query, lock, write, retry, worker, source/Git/content
read, or concurrency. No wire schema, digest, API/OpenAPI field, retry policy,
or ceremony claim changes.

### T40.R1 pre-ceremony readiness closure

The post-neutral-31 readiness review closed two production defects before the
next ceremony. First, the generic job runner (indexing, extraction,
caller-leaf, resolver, and sibling queues) no longer kills a healthy handler
on its first transient heartbeat error: a confirmed-beat lower bound tolerates
client-side store errors until the existing `StaleAfter` cutoff, exactly as
the generation scheduler has done since the ceremony-21 correction, while a
lease fence remains definitive. This removes the path where one slow
surrealkv beat consumed a retry attempt mid-turn and a short degradation
window chained into permanent job failure in a frozen corpus. The reaper's
durable cutoff is unchanged; a row reaped during a tolerated error window
still surfaces as definitive lease loss on the next successful beat.

Second, the repo row now carries a caller-leaf job projection beside the
indexing and extraction ones: job creation links `latest_caller_job` inside
the existing queue transactions, and `/api/repo-status` serves `last_caller_job`
(status, attempts) under the same unavailable/exact discipline. Evidence and
operators can now distinguish a dead caller pipeline (settled failed job, no
pending successor) from a live slow one without reading queue tables. The
projection adds one bounded repo-row link write per caller job creation and
one bounded sub-projection to the existing repo-status query; no new query,
lock, retry, or concurrency.


### V21 caller evidence freeze-readiness correction

The declaration-independent caller progress route now serves schema
`caller-generation-progress-v2`. After repository authorization, it reads one
repository-keyed caller-job projection and returns `caller_job_state` plus the
live linked job's status and attempts beside the existing bounded caller
generation progress. The ceremony consumes that one response; it does not add
an installation-wide `/api/repo-status` request to each caller-generation
poll. The separate repository-index inspection retains its older status read.

The targeted store read selects one repository and its linked live caller job.
It performs no repository inventory, connection expansion, job-history scan,
source/Git/content/candidate/corpus/shard read, or write. It is one bounded
query inside the already rate-limited caller-progress request and replaces the
former second HTTP request and its installation-wide status materialization.
The response remains capped at 32 KiB and retains at most 32 typed refusal
summaries.

Fresh V21 receipts record generation-digest validity, the complete pair
counters, those bounded refusal summaries, and the caller-job projection.
Receipt validation derives caller terminal and exact bound-refusal outcomes
from the same mutually exclusive predicates as the inspector. Active work
holds non-refusal terminals, current complete/all-success authority is not
terminal, missing/stale incomplete authority without a settled dead job stays
pending, and an exact admission refusal cannot be relabeled as a generic
terminal.

During upgrade, enqueue transactions also repair `latest_caller_job` when a
pre-cutover pending caller row is coalesced. That is one repo-row update inside
the existing enqueue transaction, with no new job, history scan, attempt, or
startup migration. Every generic enqueue/successor transaction evaluates one
constant-time caller-kind/existing-row conditional, and each domain caller
transaction evaluates one projection conditional; a non-caller enqueue or an
already-current projection reads or writes no additional row. No lock,
concurrency, cache, source read, child-process, memory/disk ceiling, or startup
scan changes. Finally, the custody execution marker is created with an
exclusive atomic write only after read-only preflight succeeds. A failed
prerequisite is retryable; successful preflight still permits exactly one
state-mutating execution.

### T40.R1 resolver job projection

The repo row now links its latest resolver-catalog job
(`latest_resolver_job`) beside the indexing, extraction, and caller-leaf
projections, written by every creation path including the seven domain
fan-out transactions; coalescing into an exact pre-cutover pending row
repairs the marker. `/api/repo-status` serves it as `last_resolver_job`, and
caller-generation progress v3 returns it beside the caller projection through
the same bounded single-repository request. Operators and evidence can now
distinguish a caller successor that will never be minted (settled dead
resolver job) from one that is merely late (active resolver job), without
reading queue tables. Each creation adds one bounded repo-row link write
inside its existing transaction; the progress request gains one single-record
query and no installation-wide scan, new lock, retry, or concurrency.
Historical
caller-generation-progress v2 responses remain valid.

### T40.R1 V22 interruption recovery and lifecycle retention

Neutral-32 stopped honestly at `interruption/recovery_verification`, but its
five-minute row-presence proof ran after roughly eighteen minutes of restart-A
convergence. The restarted server had already reaped the selected B extraction
lease. Two later extraction incarnations then placed that retired schedule
beyond the retained-two generation policy, and the enabled five-second
pressure lifecycle sweep deleted the schedule and chunks before the proof
opened its reader. Repeated `not found` therefore proved neither a running
lease nor failed recovery.

Fresh V22 execution records the selected chunk's exact schedule digest and
runs recovery verification immediately after the restart reaches HTTP ready,
before restart-A convergence. A present exact chunk must leave `running`.
When the chunk is absent, the reader fences the current schedule pointer around
an exact-digest lookup. Only two consecutive, one-second-separated projections
of a digest that is non-current beside a distinct current successor and either
absent or retained as settled/superseded seal the typed `collected` fate.
Missing-current or current/active selected
authority, pointer movement, digest/scope mismatch, store error, or a
still-running chunk keeps polling and fails at the fixed five-minute bound.
Restart-A convergence follows this proof; the existing partial-derived-state
clear oracle then runs under a separate `partial_verification` substage.

The V22 observation also records the generation-schedule lifecycle owner's
latest bounded status immediately after recovery and after A convergence:
state, completeness, at most 64 scanned candidates, at most 16 deletions, and
the backlog bit. These are point-in-time last-cycle counters, not cumulative
deletion totals. V17–V21 keep their historical substage order and validation.

Cost reuses the existing exact chunk selection at trigger capture and moves
the one-second recovery poll. Each chunk/current-schedule store read inherits
the existing at-most-64 retryable-query loop. Only after `not found`, one poll
adds current schedule, one direct exact-digest lookup, and current confirmation
(at most 129 physical store queries if both current reads exhaust their retry
allowance); the outer loop can begin at most 300 polls inside five minutes,
while 30-second per-call deadlines reduce that count under slow failures. Each
of the two `/api/lifecycle-status` requests serializes the monitor's at-most-32
in-memory owner summaries and retains one; it performs no store or disk read.
There are no
new production writes, lifecycle mutations, source/Git/content/candidate/
corpus/shard scans, locks, job attempts, child processes, authority changes,
or admission/concurrency limits. Neutral-32 remains stopped; use a separately
reviewed V22 freeze for any future execution.

## T40.R1 V23 ceremony-oracle operations

Fresh V23 ceremonies require one complete process-observed lifecycle owner
cycle before the runner may take its one-hour idle interval. If the durable
rotation cursor resumes in the middle, startup can run the suffix and then one
full cycle—fewer than 64 turns for the at-most-32-owner controller—at the
existing five-second backlog cadence. Healthy steady state and pressure-
accelerated cadence are unchanged; every owner keeps its existing scan,
delete, query, lock, and cursor bounds.

Execution preflight runs `du` twice over prepared custody (allocated and
apparent bytes) before writing the executed marker. This is a full filesystem
namespace/metadata scan with no file-content read. It ensures the 82% pressure
target can still be reached inside both the 80-GiB ballast limit and the
remaining 96-GiB total custody allocation. The ballast phase remains the sole
writer and requires lifecycle `collect`; an observed 81–83% is accepted for
shared-filesystem snapshot movement. A deadline while entering `collect` is a
typed production-pressure `reduce`; collection-cycle deadline is a typed
frozen collection-review `cohort_experiment`. Post-removal pressure recovery
is classified separately below.

After a stale-worker lifecycle `completion_failed`, the harness reads the exact
selected chunk every 250 ms until it is canceled by the reaper, is proven
collected under the exact retired schedule, or the existing 20-minute bound
expires (at most 4,800 outer reads). Any different settled state or unknown
settled vocabulary fails immediately. Normal `stale_fenced` completion adds no
store-read tail. Collection and authorized-query refresh
their stable semantic baselines after successful re-projection; this hashes
already-open bounded fields and adds no request.

Each authorized endpoint has at most three attempts. Transport failures and
HTTP 409 wait one second between attempts; the HTTP client keeps its existing
30-second request timeout. The complete two-profile suite has nine endpoint
checks and therefore at most 27 requests and 18 seconds of retry delay if each
check succeeds only on attempt three; the first terminal failure stops later
queries. Evidence retains only the frozen profile, query name, failure class,
numeric status when applicable, and attempts. It never retains URL, response,
credential, or raw error text. V1–V22 validation remains historical, and this
operational hardening changes no product API, authority, admission, or release
posture.

Post-review V23 corrections keep that evidence sealable under correlated
faults. A lifecycle owner may report `error/unavailable` while retaining the
bounded counters from a successful sweep that lost its final CAS; V23 records
those counters, while V22 keeps its zero-only contract. Authorized-query
diagnostics survive a simultaneous meter-finalization failure or review/total-
wall ceiling, but a local inspector/setup failure is not attributed to an
endpoint. The historical one-attempt unauthorized probe still treats every
non-401 response as its frozen exact oracle.

Before destructive teardown, a completed run validates its entire known
evidence shape against a synthetic successful teardown record. The real
teardown metrics are recorded and validated again afterward. Schema drift now
retains custody on both stopped and otherwise-successful paths.

Pressure entry and pressure recovery are distinct decisions. Failure to enter
production `collect` remains the frozen substantiated `reduce`; delayed disk
reclaim after ballast removal is the unsubstantiated
`pressure_recovery_deadline_expired`. The pressure restart snapshot uses the
same stable semantic authority as collection and authorized-query, so a valid
relationship re-mint does not become drift.

For fresh V23 interruption evidence, `pending` is reclaimable rather than
recovered: it waits beside `running` for a closed chunk fate or the existing
twice-corroborated collection proof. V22 preserves its historical pending
acceptance. stale-worker remains log-only unless it sees
`completion_failed`; only then does it poll the exact row at 250-ms cadence.
If the row has already been collected, one exact retention projection must
bind its selected schedule digest, prove a distinct current successor, and
show the selected schedule absent or settled/superseded before the harness
records the stale fence. Current, active, malformed, moving, or unreadable
authority fails closed. Each recovery store call is bounded by 30 seconds.

The two lifecycle evidence snapshots retry transient HTTP/status/shape errors
inside their existing 30-second windows: at most one five-second request per
one-second loop, so immediate failures start at most 30 requests per snapshot.
Interruption can use its existing 300 one-second outer polls when a selected
row remains pending. After `completion_failed`, stale-worker adds at most 4,800
outer store polls/20 minutes; a missing row adds its exact retention read.
These are harness-only reads and validation. They add no production work,
source/Git/content/corpus read, persistent schema, authority, lock, write,
attempt, allocation, or release authorization.

## T40.R1 V24 interruption requeue evidence

Neutral-33 passed cold, warm-noop, delta-B, and return-A, then stopped at the
immediate interruption recovery check. V23 could not pass that check on the
normal recovery path: before B-schedule supersession, release, deferral, or
reaping returns the interrupted current chunk to pending stale priority with
its lease cleared. V23 rejected every pending row, while done/failed needed the
row to run and canceled/collected needed later supersession.

Fresh V24 records `requeued` only after two consecutive one-second projections
bind the exact trigger identity, schedule, repository, stage, generation, and
attempt to `pending`, stale priority, and no lease. A never-run priority-zero
row, any leased row, `running`, identity drift, a single sample, or a read
failure cannot pass. Closed fates and the twice-corroborated collected proof
are unchanged. The projection exports priority and a lease-presence boolean;
it never exports the lease token, worker, timestamps, or errors.

The recovery loop keeps the same one row read per second, 300-start/five-minute
ceiling, and 30-second call deadline. Requeue adds one confirmation second and
two scalar copies, not another query. There is no production write, retry,
lock, worker, queue, authority, API/OpenAPI, persistent-schema, source/Git, or
corpus-read change. V22 still accepts its historical pending fate, V23 still
refuses it, and V1-V23 evidence remains frozen.

## T40.R1 V25 ceremony feasibility and evidence firewall

Fresh V25 freezes a twelve-hour total ceremony review ceiling. This value is
derived from the retained neutral-33 phase timings and applies only to the
source-free mechanics ceremony; it is not a production timeout or SLO. The
pressure preflight also projects a frozen 72-GiB pre-pressure custody peak onto
the current filesystem. The projected host must remain below the 80% lifecycle
soft watermark and retain enough of the existing 96-GiB custody envelope to
reach the ballast target. Refusal happens before the executed marker, so free
host space and rerun preflight rather than starting a doomed ceremony.

Extraction transitions from an existing schedule use a predecessor-bound
operational schedule identity while retaining the exact content generation as
the binding target. This makes A→B→A and settled-successor recovery legal under
immutable scheduler rows. Lifecycle owner errors retry at the existing
five-second backlog cadence. A stale-worker `heartbeat_failed` report waits for
the same exact canceled/retired store proof as `completion_failed`, and parent
cancellation remains cancellation rather than a fabricated trigger deadline.

Before custody deletion the executor now rebuilds and validates the complete
plan-bound receipt in memory. The driver also supports:

```sh
./spike/t4013/run-large-mac-ceremony.sh seal <ceremony-id>
```

Use `seal` only after execution produced `observation.json` and private custody
is absent. It rebuilds a missing receipt, resumes an incomplete derived seal,
verifies a complete seal/package, and never rewrites a complete invalid seal.

## T40.R1 neutral-34 relationship-stage cleanup

A relationship root build owns its `.stage-*` directory until publish closes
it. If the late schedule fence, extraction pin, or pre-commit publish step
fails, the runtime now removes and durably syncs that unpublished stage before
returning. Successful publication remains immutable; startup repair and the
relationship lifecycle sweep remain fallback cleanup rather than the normal
failure path. This adds no healthy-path filesystem work beyond one closed-state
check and changes no operator command, schema, authority, or gate deadline.

## T40.R1 V25 pre-freeze readiness protocol

Use a clean checkout at the exact reviewed commit and run:

```sh
./spike/t4013/run-large-mac-ceremony.sh preflight
```

This is the required cheap host/module/command gate before requesting a freeze.
It performs the prospective V25 host-bound freeze in temporary output,
including the exact ceremony filesystem's projected pressure and atomic
evidence operations, verifies a fresh custody-local module graph, and prebuilds
the exact V25 freeze, promotion, Prepare, Execute, Cleanup, lock, and receipt
commands. It removes the private module and build caches before returning. It
does not run the
process-launching production/harness packages, focused real-tool proof, or
semantic/stale-worker rehearsals inside ceremony preflight; those remain branch
gates on the exact reviewed commit. A capacity, module, command-build, or
filesystem-protocol refusal is a pre-freeze stop: do not create a ceremony ID,
signing key, plan, or custody, and do not weaken the envelope. Move the
ceremony root to an adequate local filesystem or free independently reviewed
space, correct the module/tool state, then rerun the same command.

Execution may leave `observation.json.teardown` or
`observation.json.teardown.tmp` only after it has durably recorded an
incomplete teardown transition. If custody still exists, stop: retain it for
the separately reviewed purge and do not run `seal`. If custody is absent,
`execute` and the explicit `seal` command both route through
`t4013-receipt`, which gives the checkpoint precedence over any provisional
observation, reconstructs a conservative stopped observation, and retires the
checkpoint before source-free sealing. Do not delete or hand-edit any of these
files. A persistent frozen-toolchain mismatch must be corrected before resume;
the harness will keep the checkpoint rather than publish unsound evidence.

V25 alone uses the closed Git/Go execution contract. V1-V24 keep their prior
archive and ambient execution behavior so retained evidence remains
reproducible. Private server shutdown is process-session bounded; any forced kill
or surviving descendant is an execution failure, not a successful phase.
The V25 total clock begins when the executor is entered, including its admission
preflight. Concurrent executor/resume publication is refused. A wrapper signal,
abnormal execution child with retained custody, refused cleanup, or incomplete
preparation leaves its operation lock and private state for review.

V25 now creates `<workspace>.t4013-supervision` before its first operation
child. Its controller lock is crash-released, while one descriptor-64-or-higher
lease is inherited by every supported descendant until the controller proves
drain. PID reuse, session escape, and intermediate process exit therefore do
not authorize deletion. If the controller dies in `live` or `finalizing`, the
state is live while any descendant holds the lease and indeterminate after the
last lease closes; it never becomes drained from lock freedom alone. Teardown
binds the exact checkpoint, covers finalizer children with the same lease,
deletes custody, reaches terminal, and retires supervision through recoverable
`.retiring`/`.retired` transitions while the external prepared/checkpoint
controls remain authoritative. It removes those controls only after exact
retirement is durably confirmed.

Do not remove, rename, or hand-edit the supervision directory, its
`.creating.<token>` stage, `.retiring` directory, or `.retired` state. Do not
substitute `ps`, `pgrep`, port freedom, or elapsed time for the kernel lease.
Keep the run lock, closed execution-control root, prepared/checkpoint controls,
logs, and any custody together when a command reports live, indeterminate,
mismatched, or malformed supervision. The supported V25 driver uses prebuilt
direct operation roots with no outer `go run`. Receipt resume may complete exact
drained/terminal authority; any supervision path surviving that recovery
refuses further publication and seal. Historical V1–V24 bytes and normal
supported CLI flow remain historical; direct legacy Destroy intentionally
tightens symlinked cleanup-root and stable/retiring/retired V25-supervision
refusal.

V25 uses the persistent regular file `<run-root>/.t4013-operation.lock` as the
one custody-mutation lock for direct Prepare, Cleanup, Destroy, Execute, Resume,
and the supported shell. Do not remove it as stale: file existence carries no
liveness claim, and the kernel lock is crash-released only after every inherited
descriptor closes. The shell re-executes V25 execute/seal under this lock and
passes the exact descriptor through its closed environment. A busy lock means
another operation or surviving descendant still owns the run; retain all state
and retry only after the kernel admits the lock.

Each mutator uses preliminary bounded authority bytes only to locate the run,
then acquires the lock before custody/output preconditions and revalidates the
same exact bytes under it. Limits are 64 KiB for a plan, 256 KiB for prepared
authority, 4 KiB for cleanup control, and 260 KiB for a teardown checkpoint.
Prepared output is invalid inside custody or the reviewed module checkout.
Do not bypass these boundaries or hand-edit a prepared/checkpoint identity.

V25 now binds source-free canonical-path digests for every frozen host tool,
retains exact Go, Git/core, and SurrealDB paths, and rehashes the relevant exact
executable immediately before each ceremony launch. The four custody-built
binaries are rehashed as one bounded snapshot before every serve, backup, or
restore start; Phebs also rechecks exact SurrealDB immediately before start and
exact zoekt, focused-index, and Buf immediately before each child launch. The
shell retains and rehashes all eight prebuilt V25 commands across its lock
re-exec, including the bounded returned-bundle inspector. A changed file,
symlink, or PATH selection is a hard refusal; do not replace a tool or hand-edit
its digest.
Full Go/Git tree hashing is limited to fixed admission and terminal snapshots.

V25 Prepare also writes `.t4013-execution-controls.json` inside custody and
binds its digest in `prepared.json`. Its HOME, XDG config/cache/data, temporary,
module, build, PATH, and Git exec selections are fixed; shell startup controls
and ambient Go/Git configuration are absent. Do not edit or replace that
manifest or its `.t4013-execution/{home,tmp}` directories. Execute reopens the
same bounded manifest before mutation and each serve, backup, or restore start.
A fresh private module cache is checksum-verified and hashed under the fixed
100,000-entry/2-GiB tree limit, compared once after offline private builds, and
then removed together with the build cache. Their absence before runtime is an
enforced state, not a cleanup suggestion.

The shell mirrors these controls beneath the ceremony root. It ignores ambient
`CLOSED_*` values unless adopting the live inherited operation-lock descriptor,
fixes system PATH after exact Go/Git/Surreal discovery, and revalidates its
small digest-bound manifest after lock re-exec. On a proved normal exit it
removes only that exact private root; an unproven child exit retains the whole
root with custody for review. Never point a `CLOSED_*` variable at a shared
host cache, and never delete retained controls independently.

Treat every transferred `*-source-free.tgz` as hostile until verification
passes. `verify-bundle` requires exactly one identity recorded through a
separate reviewed channel; the package's `.sha256` sidecar, `signer.pub`, and
`allowed_signers` files are transfer material, not authority:

```sh
REVIEWED_SIGNER_FINGERPRINT='SHA256:<reviewed-freeze-fingerprint>'
./spike/t4013/run-large-mac-ceremony.sh verify-bundle \
  /absolute/path/to/<ceremony-id>-source-free.tgz \
  --reviewed-signer-fingerprint "$REVIEWED_SIGNER_FINGERPRINT"

# Alternative when the exact package digest itself was reviewed out of band.
REVIEWED_PACKAGE_DIGEST='sha256:<64-lowercase-hex-digits>'
./spike/t4013/run-large-mac-ceremony.sh verify-bundle \
  /absolute/path/to/<ceremony-id>-source-free.tgz \
  --reviewed-package-digest "$REVIEWED_PACKAGE_DIGEST"
```

The verifier reads at most 4 MiB of package bytes and consumes at most 1 MiB
of expanded tar bytes. It accepts only one `evidence/` directory and exactly
one regular non-link entry for each fixed evidence basename. The existing
64-KiB plan and 256-KiB observation/receipt limits apply; signer material is
capped at 1 KiB and the remaining small envelopes, manifests, and signatures
at 4 KiB each. It creates no extraction output until every header, duplicate,
type, link, per-file, aggregate, and trailing-stream check passes. It then
authenticates the checksum manifest before hashing its exact eight listed files
and verifies the frozen plan with the same trust root. Its private temporary
root is removed on success, error, INT, TERM, or HUP. Do not inspect a rejected
temporary tree or retry with an identity copied from the rejected bundle.

Do not request a freeze yet. T40.13h and later prerequisites must close the
remaining medium/low gates. `go mod verify`, the exact
filesystem projection, bounded package/race/docs gates, focused real-tool
proof, and both real-binary rehearsals must pass from one clean commit in their
designated branch-gate or preflight boundary. Passing those gates is readiness
evidence only; freeze and execution each still require Ben's separate explicit
authorization and a fresh, unconsumed ceremony identifier.
