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
evidence, and proof pins that cannot be rebuilt (repo rows and job state ride
along but are derivable). Mirrors and whole-repository shards are derived.
Focused shards are also derived semantically, but online backup preserves
their currently published bytes exactly because builder timestamps/identity
make rebuild output an unsuitable restore-equality test.
Candidate manifests and their partition members are derived planning state:
they are excluded and rebuilt from the restored indexed commit and mirror.

For an online backup, keep the local phebs server running and use the same
phebs executable/configuration generation as that server:

```sh
phebs backup -config /etc/phebs/phebs.yaml -output /restricted/phebs-backup-20260722
```

The output path must not exist. The command discovers only the supervised
loopback SurrealDB child through `$DATA/.surreal-runtime.json`, verifies the
exact child executable and the raw-config digest that started the live server,
acquires a crash-released cross-process snapshot lock that lets in-flight
publication/state commits finish and pauses new ones, and runs that
executable's live `export`. A different config that only points at the same
`$DATA` is refused. The command publishes a private directory
containing `database.surql`, `focused-index.tar`, and `manifest.json`. The
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
`phebs-backup-manifest-v3` manifest
binds both artifacts' sizes and SHA-256 digests, the exact raw config digest,
phebs version/binary digest, SurrealDB version/binary digest, database
identity, store-writer/evidence/migration versions, and the derived-state
exclusions, including `$DATA/candidates`. Its required
`phebs-focused-archive-report-v1` receipt records archived publications,
omitted publications/artifacts, and stale markers; verification independently
recovers and compares the archived publication count. It contains no host
binary path or database password. Preserve the exact config separately; the
backup contains its digest, not its bytes.

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
because `$DATA/candidates` is deliberately absent from the backup.
If import begins and then fails, the partial target is retained and every
later restore refuses it; quarantine or remove it under the witnessed
recovery procedure rather than retrying over it.

The subsequent normal `serve` start revalidates restored focused publications
against committed unit/revision state. It keeps exact valid focused bytes,
clears and force-requeues any claim whose focused publication was invalid or
omitted, rebuilds excluded whole-repository shards, rebuilds the cleared
candidate publications before extraction, and
boot sync re-clones missing mirrors. Restored API keys and sessions remain
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

Sync, fetch, index, candidate-manifest, and extraction work runs through
queues in SurrealDB,
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
cover child start, successful completion with duration and total shard bytes,
state commit, and already-current skips. Child lines longer than 64 KiB are
split into bounded continued records and invalid UTF-8 is replaced before
logging. Independent of this switch, a failed child carries only its newest
1 MiB of output into failure classification and the job error; successful
child output is discarded. Verbose mode does not change the indexed corpus,
timeouts, retries, process isolation, or shard publication. Disable it again
after diagnosis when indexing a noisy large repository.

#### Analysis-unit state and rebuilds

At startup, each configured `analysis_units` entry produces one
repository-prefixed diagnostic containing the unit name, stable digest,
primary/supporting path lists, and the exact search and typed-index postures.
These are operator metadata only; no source, blob bytes, line excerpts, or
credentials are logged. The configuration guide owns the
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
publication, candidate manifest v3 additionally commits one explicitly
addressed projection for every local domain. Each projection contains exactly
that domain's in-unit repository records in canonical repository order; an
empty projection is represented by an explicit empty member list. Projection
members are named by the canonical policy ordinal and are limited in aggregate
to 16,384 artifacts and 4 GiB of canonical content. Caller rows are assigned by
`SHA-256("phebs-caller-path-v1\0" || UTF8(repository-relative-path))`.
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

The retained
[T30.4 prospective measurement](../../spike/t304/README.md) streamed 200,008
regular files into five repository rows and six caller rows. It produced three
two-bit caller leaves (`00:1`, `10:3`, `11:2`). The post-T30.5 manifest-v3
refresh added the focused-local projections; each run staged 12 files totaling
24,288 bytes. Twice the final caller content bounds planner spool and
split scratch at 4,134 bytes; external-validation scratch is bounded at 3,514
bytes. Adding the larger phase bound to the final stage gives 28,422 bytes of
conservative peak candidate disk. The refreshed runs took 3.80 s and 3.62 s,
peaked at 60,604,416 and 61,652,992 bytes RSS, and reproduced byte-identical
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

At steady state, the exact persisted pointer, an absent marker, and its present
regular manifest control file can prove that no publication bytes need to be
consumed. The candidate job uses that identity-only path to repair the guarded
extraction fan-out without rehashing or externally sorting every member. The
extraction job resolves the pointer's manifest digest before taking the
repository mirror lock and returns when every enabled domain already has a
current run carrying that digest. A forced or stale domain takes the lock,
reloads current state, then strictly opens the manifest and members once
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

Before creating an extraction attempt, the worker refuses a publication
marker, stale database pointer, HEAD/unit/policy disagreement, malformed or
noncanonical JSON/NDJSON, partial/extra/duplicate member, digest mismatch,
overlapping or unordered caller leaf, or special filesystem entry. Local
contract, field, topic, consumer, attribution, and Workbench implementation
domains consume only records whose manifest membership is inside the committed
unit. A caller-plane domain remains visibly `repository-overlay` input pending
T30.6's target-bound complete-generation replacement; its rows do not become
focused search or local implementation evidence.

The candidate manifest carries a designated typed-input envelope separately
from ordinary source rows. For a focused repository, `typed_index.kind: scip`
binds the exact configured supporting path, blob identity, size, consuming
domains, commit, unit, and candidate generation. Readers cite that real path:
they never invent `index.scip`, fall back to a root artifact, or admit a SCIP
document outside the unit. A focused unit with no designation reports a typed
input gap. Whole-repository repositories retain the legacy root lookup.

##### Extraction operation receipts

Every repository extraction job emits one JSON log entry prefixed
`extraction operation:` with schema `phebs-extraction-operation-v1`. Treat it
as a bounded operational diagnostic, not durable evidence. The job envelope
names the canonical repository, indexed HEAD, committed unit digest,
candidate-manifest and candidate-policy digests, and queue attempt. It records
queue wait, repository-lock wait, pointer-only preflight, and strict
candidate-publication open once per job instead of copying those shared costs
into each domain.

Nested domain entries contain only durations for inventory, opened source,
extractor, staging, publication, abort, and cleanup; aggregate counts and
bytes already observed by those operations; the existing corpus/read/blob/
typed-input/fact limits; and one generic reason:
`already_current`, `not_ready`, `stale`, `no_candidates`,
`typed_input_absent`, `limit_refusal`, `published_empty`,
`published_nonempty`, `canceled`, or `failed`. They never contain a source
path, source content, path sample, or raw extractor diagnostic. Use the
ordinary domain log immediately preceding the receipt when a raw local
diagnostic is required for troubleshooting.

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
`phebs_extraction_operation_domains_total` has only the frozen generic
`reason` label. Repository, extractor, commit, unit, manifest, and policy are
deliberately absent from metric labels; use the bounded log receipt for those
identities.

##### Scheduled T30.6 operating sequence

The accepted large-monorepo review changes sequencing, not current runtime
behavior. Today an admitted `*_test.go` file remains searchable and participates
in candidate planning when an enabled domain policy enumerates it, the
extraction runner may retry a deterministic domain failure to its attempt cap,
and caller domains still consume the temporary repository-overlay view that
can refuse its flattened aggregate inventory.
Operators should not raise the global file, path-byte, read-byte, fact, or
single-run deadline limits to work around that refusal. Configure the smallest
truthful analysis unit and, when required, its exact typed input; preserve the
failure diagnostic and wait for the target-bound caller generation.

T30.6a now supplies the bounded job report described above. T30.6b next makes
exact terminal and retryable outcomes durable; T30.6c schedules them beneath
independent per-domain and aggregate job/lock bounds. T30.6d advances
candidate identity with `source_lane: base|go_test`, and T30.6e consumes
`base` only for focused local evidence while safety-accounting the complete
typed SCIP artifact before removing exact test documents' definitions,
anchors, occurrences, and joins. Empty-unit repositories retain shipped
whole-repository extraction behavior, and focused search remains unchanged.
T30.6f–T30.6i separately deliver catalog lifecycle, resolver materialization,
leaf artifacts, and complete caller publication. T30.6j–T30.6l bind Caller Map,
comparison, and Workbench Impact as separate authorized consumers. T30.6m
selects the historical-retention posture and T30.6n implements only that
selected policy.

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
evidence sweep does not collect them. Database use can therefore grow with
historical focused publications. Do not delete those rows by hand: pinned proof
may reference them. The active backlog requires a separately reviewed bounded
unpinned-retention policy, or an explicit decision to retain this unbounded
posture, before Epic 30 closes.

Store upgrade preserves readable legacy whole-repository runs with an empty
unit digest and their original source commit. It does not copy the currently
configured unit onto historical evidence. Current focused consumers request
the complete tuple and never fall back to those rows. An older writer refuses
a store already claimed by this generation, while a newer unknown writer is
left untouched and unreadable rather than guessed compatible.

Coverage records name the scope posture, unit digest, candidate-manifest
digest, candidate plane, exact selected candidate count/bytes/digest, and the
source paths actually read. Coverage certificates additionally disclose the
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
gate. Repository deletion cancels both job kinds and removes the database
pointer and derived bytes. A malformed derived database pointer is cleared
under the repository lock and rebuilt; database transport/query failures still
fail closed. A preexisting stable marker permits strict reuse of a complete
generation after a real publication crash; without that marker, clearing the
pointer forces a fresh Git census. If a crash marker or corrupt candidate
publication continues to refuse after automatic reconciliation, stop phebs,
retain logs, move `$DATA/candidates` aside for diagnosis, and restart: the
directory is derived and is rebuilt before extraction. The supported restore
path clears the imported database pointer automatically; do not manually copy
a pointer or individual member into a new installation.

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
one warning naming the provisional, non-production posture.

For a bounded operator smoke over public, remote-HEAD evidence:

1. Use the isolated `phebs-everything.yaml` configuration and a fresh or
   disposable `~/.phebs-everything` data directory.
2. Run `make build`, then `./phebs serve -config phebs-everything.yaml`.
3. Wait for one configured public repository to finish sync, index, and
   protobuf or Thrift declaration extraction. Confirm a published declaration
   run in the extraction logs or Contract Atlas coverage.
4. Sign in, open **Contract Atlas**, select one exact published operation, and
   choose **Start Workbench**. Confirm the resulting Workbench retains the
   repository, indexed HEAD commit, declaration lineage, and operation shown
   by Contract Atlas.
5. Stop the instance and remove the disposable data directory if the
   observation does not need to be retained locally.

This is a manual availability check only. Upstream HEADs and the resulting
rows may drift; do not commit outputs, turn observations into an accuracy
number, or use them as deterministic merge-bar input. `make dev` and
`make dev-api` remain the fixture-backed deterministic demonstrations.

### Thrift field-zero development walkthrough

`make dev` and `make dev-api` explicitly set
`PHEBS_THRIFT_FIELD_DEMO_REPO` to the committed
`docs/fixtures/thrift-field/t225-thrift-field-demo.bundle`. The server accepts
only that clean absolute bundle name, adds it as a generic Git source, and
enables `provisional_thrift_field_extraction` for that process. The bundle then
uses the ordinary sync → zoekt index → extraction path; it is not an HTTP or
UI proof-logic adapter. Ordinary `phebs serve`, operator configuration, and
release defaults remain unchanged and dark.

To exercise the path:

1. Run `make dev`, use the logged first-run setup token if necessary, and wait
   for the `t225-thrift-field-demo.bundle` repository to show an indexed
   revision on **Repos**.
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
certificate (`coverage-certificate-v1`): the caller's visible repositories
with their indexed revisions, each domain's exact latest published run (run
id, extractor, commit, freshness, protocols, complete source-scope counters and
digest, unresolved/assertion/atom counts, and gitlink boundary state), its
latest extraction attempt (id, input revision, extractor, status, and
failure), and SCIP index availability.

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
the exact Caller Map service and HTTP route are registered; anonymous version
discovery omits it with every other capability. The same authenticated
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
the complete `coverage-certificate-v1` and its digest. The server first
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
caller whose server advertises `contract-atlas`. Normal production obtains
that capability only from the enabled provisional evidence service. For local
UI demonstration, `make dev` and `make dev-api` explicitly set
`PHEBS_CONTRACT_ATLAS_FIXTURE` to
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

Proof-bundle expiry is disabled by default. To bound retained answers and
their extraction-run pins, set a positive Go duration, for example:

```yaml
proof_bundles:
  retention: 168h # seven days since the bundle was last materialized
```

Omission or `"0"` retains bundles indefinitely. Repeating an identical query
refreshes only the store lifecycle timestamp: the canonical JSON, content ID,
and returned bytes do not change. Existing bundles created before this option
was available use their creation time until next materialized, so enabling the
policy also bounds them. Expired, missing, and unauthorized IDs all return the
same `404` response.

When enabled, retention checks once at startup and hourly thereafter, draining
backlogs in bounded batches. Deleting a bundle and only its exact
`proof-bundle:<id>` pins is one transaction; pins owned by another bundle or a
release/migration checkpoint remain. Bundle expiry never deletes evidence.
The independent evidence sweeper decides whether a superseded run made
newly unpinned by that transaction is eligible for reclamation.

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
Focused local domains consume their manifest-v3 unit projection; repository
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

For local demonstration only, `make dev` sets `PHEBS_INVESTIGATION_FIXTURES`
to the five canonical synthetic files in `docs/fixtures/investigations/`,
which binds the read-only `investigation-core-views` capability and the
`#/investigations` page. These fixtures exercise presentation and conformance
states; they are not published evidence, a released pack executor, a valid
accuracy gate, or authority for external claims.

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
| log: `zoekt-git-index not found — indexing disabled`              | binary built without `make build`/`make dev`                                                           | `make build`, or set `PHEBS_ZOEKT_GIT_INDEX=/path/to/zoekt-git-index`                                 |
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




## Developing phebs


| Target               | Does                                                                                                                                                    |
| -------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `make dev`           | build UI + pinned whole/focused zoekt and Buf children, bind synthetic Investigation/Contract Atlas fixtures, the retained neutral Change Workbench closure repo, the fixture-coupled Workbench, and the committed Thrift field-zero repo through normal sync/index/extraction; run with embedded UI |
| `make dev-api`       | backend-only loop with the same children, explicit UI/Workbench fixtures, and Thrift field-zero repository (placeholder UI page, fast)                                           |
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
