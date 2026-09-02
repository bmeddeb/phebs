# Configuration and repository connections

[← User guide](../MANUAL.md)

This guide owns accepted configuration, authentication, connector, sync,
webhook, watch, and orphan-cleanup behavior. The exhaustive commented schema is
[config.example.yaml](../config.example.yaml).

## Configuration reference

Config is a single YAML file, validated strictly at startup: unknown fields,
type mismatches, and semantic errors **fail fast with line numbers**. The
annotated example lives at [config.example.yaml](../config.example.yaml).
`server.data_dir` must be a literal path without glob metacharacters.
Every referenced environment variable in a secret field must exist and be
non-empty; this applies to legacy API/webhook secrets, bootstrap passwords,
OIDC client secrets, PATs, inline App keys, and Git HTTP credentials. A
missing variable stops startup rather than silently weakening authentication.

```yaml
server:
  addr: "127.0.0.1:3070" # loopback listen address (default)
  data_dir: "~/.phebs"   # all state lives here (default)

auth:
  cookie_secure: true       # default; set false only for plain-HTTP local use
  session_lifetime: 12h     # absolute lifetime; sessions idle out after 30m
  # api_key: "${PHEBS_LEGACY_API_KEY}"  # migration only

sync:
  cleanup_orphans: false  # delete repos no connection claims (default off)
  poll_interval: 15s      # job-runner cadence; lower for snappier watch mode

indexing:
  verbose: false          # opt-in parent/child index progress logs

diagnostics:
  jobs: false             # bounded queue lifecycle receipts for every worker
  candidates: false       # index handoff and candidate-operation receipts
  extraction: false       # preflight, scheduler, outcome, and operation receipts
  extractor_details: false # fixed pack counters; requires extraction: true

connections:
  - name: my-conn         # required; unique; [a-z0-9-]+
    type: github | gitlab | gitea | git
    # ... see per-type fields below

# Optional: seven additional refs per repo; HEAD is implicit.
revisions:
  github.com/acme/api:
    release-1: refs/heads/release/1
    v1.4.0: refs/tags/v1.4.0

# Optional: one exact service scope per repository.
analysis_units:
  github.com/acme/monorepo:
    name: payments
    primary: [services/payments/src]
    supporting: [contracts/payment.proto, services/payments/go.mod]

# Optional: one explicit normalized multi-service authority per repository.
service_catalogs:
  github.com/acme/monorepo:
    kind: operator
    id: platform-catalog
    version: "2026-08-04.1"
    path: /etc/phebs/service-catalog.json
```


| Key                                         | Default          | Notes                                                                                                                                                             |
| ------------------------------------------- | ---------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `server.addr`                               | `127.0.0.1:3070` | loopback by default; explicitly configure a private proxy-facing address for deployment                                                                           |
| `server.data_dir`                           | `~/.phebs`       | `~` expands; created if missing                                                                                                                                   |
| `auth.api_key`                              | *(empty)*        | legacy migration key only; its SHA-256 hash is imported into the DB, and omission removes the legacy row; it does not make an empty configuration unauthenticated |
| `auth.cookie_secure`                        | `true`           | `Secure` session-cookie attribute; set `false` only for intentional plain-HTTP development                                                                        |
| `auth.session_lifetime`                     | `12h`            | absolute lifetime, Go duration from `15m` through `720h`; fixed idle timeout is 30 minutes                                                                        |
| `auth.trusted_proxies`                      | `[]`             | trusted reverse-proxy hop CIDRs, including the direct peer, allowed in `X-Forwarded-For` resolution for per-client auth throttling; never include client networks |
| `auth.bootstrap_user`                       | *(none)*         | optional one-time first local administrator; requires `email` and a password of at least 12 bytes                                                                 |
| `auth.oidc`                                 | *(none)*         | one OIDC provider; requires issuer/client/secret/redirect URL; HTTPS except loopback tests                                                                        |
| `sync.cleanup_orphans`                      | `false`          | see [orphans](#orphans-and-cleanup)                                                                                                                               |
| `sync.poll_interval`                        | `15s`            | Go duration; job pollers wake with ±50 % jitter around it                                                                                                         |
| `sync.resync_interval`                      | `1h`             | re-sync cadence for remote connections; `"0"` disables                                                                                                            |
| `indexing.verbose`                          | `false`          | restart-bound opt-in for repository-prefixed index phases, explicit whole-search `go_git` mode/offered/batch/fallback counters, and `zoekt-git-index` stdout/stderr; child lines are split after 64 KiB and failure diagnostics retain only the newest 1 MiB |
| `diagnostics.jobs`                          | `false`          | restart-bound bounded JSON lifecycle receipts (`claimed`, `started`, `done`, `yielded`, `requeued`, `failed`, or `released`) for every durable worker queue |
| `diagnostics.candidates`                    | `false`          | restart-bound index-to-candidate handoff plus one bounded candidate-operation receipt with decision, phase timing, plane counts/bytes, typed-input posture, and logical spool peak |
| `diagnostics.extraction`                    | `false`          | restart-bound extraction pointer/strict-open preflight, ordered scheduler/deferral, durable-outcome transition, phase, and final bounded operation receipts |
| `diagnostics.extractor_details`             | `false`          | adds only fixed aggregate gRPC, Thrift, and Kafka counters to extraction-operation domains; requires `diagnostics.extraction: true` |
| `webhook.secret`                            | *(empty)*        | enables `POST /api/webhook`; `${ENV}` expanded, fails closed on unset vars                                                                                        |
| `audit.retention`                           | `2160h`          | audit events older than this are pruned twice a day; `"0"` keeps them forever                                                                                     |
| `analytics.retention`                       | `8760h`          | local usage events older than this are pruned twice a day; `"0"` keeps them forever                                                                               |
| `proof_bundles.retention`                   | *(disabled; effective `0`)* | positive Go duration expires proof bundles after their latest materialization, deleting the bundle and exactly its `proof-bundle:<bundle_id>` evidence pins but no extraction evidence; the independent evidence sweep may later reclaim newly unpinned superseded evidence when otherwise eligible; omission or `"0"` keeps bundles and pins indefinitely |
| `lifecycle.enabled`                         | `true`           | runs bounded owner-separated v1/v2 catalog, dark catalog-v3, search-generation, generation-schedule, and terminal-job maintenance and reports its source-free state through the administrator status; `false` disables automated collection but keeps hard-watermark admission and every root/pin/lease/tombstone fence |
| `experimental.provisional_proto_extraction` | `false`          | development-only opt-in for the validation-gated readers described below; declarations/operation consumers retain provisional lineage                             |
| `experimental.provisional_thrift_extraction` | `false`         | development-only opt-in for the T19 Thrift declaration and Go-consumer readers described below; same provisional repo/path lineage posture                         |
| `experimental.provisional_thrift_field_extraction` | `false`   | independent development-only opt-in for T22's thriftrw and Apache Thrift field-reference reader over a committed root `index.scip`; neutral proof/report/MCP/UI surfaces remain experimental-dark |
| `experimental.provisional_kafka_extraction` | `false`          | development-only opt-in for the T23 Kafka topic-evidence packs described below; abstention-dominant by design, same provisional repo/path lineage posture         |
| `experimental.provisional_workbench`       | `false`          | development-only binding of the existing Change Workbench to store-derived Contract Atlas evidence; requires protobuf or Thrift declaration extraction and conflicts with synthetic catalog authorities |
| `permissions`                               | *(none)*         | presence enables permission-aware search (see [Permission-aware search](./OPERATIONS.md#permission-aware-search)); omit to keep every authenticated user seeing everything       |
| `connections[].url`                         | *(required by type)* | generic Git accepts remote clone URLs, absolute local paths, `file://`, or a quoted exact `~/...` path; local wildcards are never expanded                      |
| `revisions`                                 | `{}`             | repo name → `rev:` selector → full `refs/heads/*` or `refs/tags/*`; at most 7 additional refs per repo (8 including implicit HEAD)                              |
| `analysis_units`                            | `{}`             | repo name → one strict service scope; omitted repositories keep whole-repository behavior; restart after changing it                                           |
| `service_catalogs`                          | `{}`             | repo name → one explicit normalized `committed` or `operator` catalog file; exact replacement is reconciled at startup and after indexing; see [Service catalogs](#service-catalogs) |

### Historical publication retention

T35.2 supersedes T30.6m with fixed owner-specific policy, and T35.3 implements
the first bounded collectors behind one boolean rather than operator-tunable
age/count/byte values. Strict config validation still rejects guessed keys such
as `publication_retention` or `retention.historical_publications`.
`lifecycle.enabled` defaults true; false disables automated collection but
does not disable 90% hard-watermark admission, live roots, proof/Investigation
pins, active leases, tombstones, or the independent
`proof_bundles.retention` lifecycle. The latter remains narrower: a positive
lifetime removes an expired immutable bundle and
its exact `proof-bundle:<id>` pins. Bundle expiry deletes no extraction
evidence; the independent evidence sweeper may later reclaim a newly unpinned
superseded run only when that run is otherwise eligible. This key does not
change the T35 catalog/schedule/job collectors.
Administrators can inspect the fixed 16-KiB-bounded
`GET /api/lifecycle-status` or Settings projection; it copies in-memory
aggregate state only and exposes no cursor, repository, generation, path,
retained content, or raw error.
The `catalog-v3-generations` owner is independently listed and reports
only source-free scan/delete/backlog state plus exact retired logical bytes and
physically deleted root/member bytes for its latest turn. These counters are
zero for owners that do not publish those metric kinds. There is no v3
retention configuration key and no valid candidate auto-promotes. T41.9
registers v3 state workers and selector-aware directory/search adapters. V3
work runs for an explicit `service_catalogs.<repository>.runtime: v3`
transition and, after the irreversible selector floor exists, to maintain the
complete holding target needed by a safe v2 transition. The candidate remains
non-authoritative until the complete selector CAS.

T30.6n bounds job-history reads and repairs startup migration without deleting
job history, and it adds no configuration key. The 100-row response cap,
257-row physical scan window, 1,024/2,048/256-character target/error/claimant
caps, 256-row stale-reap batch, and active-row migration refusal are frozen
safety contracts rather than operator-tunable retention controls. T30.6o
shipped the authorization-first status shell and its original 52-component
registry; T40.7 adds `evidence_chunk` and T40.10 adds
`extraction_domain_root` beneath `evidence_publications`, making the current
registry 54 components. The unconditional
`unbounded_historical_publication_retention` warning is reported in
`X-Phebs-Warning-Code` on every endpoint response, including
authorization and internal errors, while successful bodies also carry
`warning_code`. The core collector now populates 23 SurrealDB components with
bounded aggregate per-table or per-pin-namespace row totals. T30.6q now populates one
aggregate physical-row total for each of the exact 24
Investigation/Workbench tables. T30.6r completes the remaining seven derived
components and, where the operating system supplies the supported
descriptor-bound filesystem-capacity primitive, both installation data-volume metrics
under the fixed budgets described below. Unsupported platforms retain typed
unavailable capacity with a localized cause. Per-component physical-database
attribution stays explicitly unavailable.
The populated byte metrics remain logical encoded outcome-receipt bytes,
canonical proof-content bytes, and canonical caller-receipt bytes; T30.6q
adds counts but does not infer physical database bytes. Ordered `logical_encoded`,
`canonical_content`, `canonical_receipt`, `apparent_file`, and
`physical_database` byte kinds are non-combinable accounting contracts, not
selectable configuration. The `proof_bundles` owner exposes the existing
`proof_bundles.retention` control, while `lifecycle.enabled` controls only the
T35 automated collectors as a group.
Its `default_state` and `accumulating` posture follow the effective configured
lifetime: zero reports disabled/accumulating and a positive duration reports
enabled/nonaccumulating. A positive lifetime deletes the expired bundle and
exactly its `proof-bundle:<bundle_id>` evidence pins but no extraction evidence;
the independent evidence sweep may later reclaim newly unpinned superseded
evidence when otherwise eligible. Owner-specific T35 ages, counts, byte kinds,
and watermarks are fixed decisions rather than additional configuration keys.
The fixed 4,096-report/4,150-scan aggregate allocation and
64-KiB response ceiling are implementation safety contracts, not configuration
keys. Registry indices 0–45 receive 76 report slots plus one sentinel and all
later indices receive 75 plus one. The core collector therefore receives
1,745 report and at most 1,768 scan identities. That placement is an API-shell
choice: the store accepts report allocations from 1 through 79 only when scan
is exactly report plus one and separately enforces the 1,745/1,768
aggregate ceilings. It produces 23 component summaries using at most 25
bounded row-range queries after four cached writer/migration-marker point
checks and one pin-index catalog check. Each one-statement query must return
exactly one result envelope. Failures remain localized as unavailable metrics
and emit one log event from the closed
operational class set—`not_ready` or `query_error`—per failed component, at most
23 per request. These limits are not configuration keys. The retention
collector reuses the `evidence_chunk` table and indexes required by T40.7's
accounting writer and includes T40.10's `extraction_domain_root`; inventory
adds no further schema, backfill,
writer-generation bump, index, sync-tick work, writer work, or retention
lifecycle change. T30.6q preserves the shell's
non-transferable allocation: all 24 Investigation components receive 76 report
slots plus one sentinel, for 1,824 reported and at most 1,848 scanned
identities. Its store-side safety fence still accepts the earlier larger
1,848/1,872 aggregate, while the current API never requests it. One
`INFO FOR DB` catalog preflight plus at most 24 direct
record-ID-ordered table scans produces those summaries with at most 25 calls
and 77 selected IDs retained for the active table. The
server-side catalog intersection returns at most the 24 fixed allowlisted table
names.
Missing tables and failed reads remain unavailable and emit at most 24
additional events classified as `not_ready` or `query_error`. Together the
current core and T30.6q collectors stay within
3,569-report, 3,616-scan, 55-call, and 47-event ceilings. The collector adds no
query index, schema backfill, startup reconstruction, configuration, writer,
or lifecycle work. T30.6r now populates the final seven derived components and,
where the operating system supports the descriptor-bound filesystem-capacity
primitive, the installation total/available metrics. Its four authority
selections use at most nine store client calls; the one batched caller fence
performs at most 304
server-internal point reads—four for each of at most 76 authorities—plus its
marker check. Incremental filesystem work is fixed at 163,840 entry
observations, 4,096 charged stats, 64 MiB of manifest metadata, 256 queued
caller directories, and five simultaneous structural descriptors:
at most three collector-retained handles plus up to two Go/platform directory
iterator duplicates or rooted traversal internals. Every returned raw name
consumes the observation budget. Names are otherwise names-only; only
recognized names receive explicit descriptor-rooted `Lstat` checks. These
limits and the 256-name directory batch are implementation safety contracts,
not configuration keys. T30.6r localizes at most nine diagnostics,
bringing the complete status-path event ceiling to 56; neither is configurable.
The stat ceiling includes explicit descriptor-rooted `Lstat` checks,
conservative open-time `fstat` charges, and one conservative slot per name-batch
(`Readdirnames`) call for the Windows error-classification `File.Stat` fallback.
The 76-report/77-scan maximum slots allocate the response envelope rather than promise
universal exactness. The 4,096-stat ceiling covers the regression-gated lean
maximum allocation; recognized residue, nested stages, or the independent
64-MiB metadata limit may still localize a lower-bound or unavailable metric.
The metadata allowance is aggregate I/O, not a heap meter: one caller manifest
at a time may retain up to 32 MiB of raw bytes beside its bounded decoded pair
structure.
`server.data_dir` selects the directory whose managed
subroots and filesystem capacity are observed; it does not change component
allocations or turn unreadable/partial inventory into exact zero. These are
per-request ceilings; concurrent authorized requests multiply them because
the surface adds no retention-specific cache or concurrency gate. T30.6n–T30.6r
add no deletion, change no owner lifecycle, and add no retention configuration.
On an operating system without the supported filesystem-capacity primitive,
total and available bytes remain explicitly unavailable while component
inventory continues.
Resolver/caller canonical byte metrics have a separate platform fence: they
require the supported rooted nonblocking regular-file opener and remain typed
unavailable where it is absent, while physical component inventory continues.
A future bounded historical-publication policy requires a new ADR and
configuration contract; omission and numeric zero are not reserved as
destructive/default aliases for such a future key.


### Analysis units

`analysis_units` names at most one service scope for an exact repository. It
does not discover services, run a build, follow dependencies, or widen the
scope automatically:

```yaml
analysis_units:
  github.com/acme/monorepo:
    name: payments
    primary:
      - services/payments/src
    supporting:
      - contracts/payment.proto
      - services/payments/go.mod
      - services/payments/index.scip
    typed_index:
      kind: scip
      path: services/payments/index.scip
```

`primary` requires at least one exact file or directory. `supporting` may be
empty and is reserved for explicit declarations, generated sources,
module/workspace metadata, attribution inputs, and typed-index artifacts. Both
lists use complete repository-relative Git paths. They are sorted
independently for identity, so YAML order does not change the digest.

Names are non-empty tokens of at most 128 bytes using letters, digits, `.`,
`_`, and `-`. A scope admits at most 128 combined path entries and 64 KiB of
combined path bytes. Repository keys are at most 1,024 bytes and must be valid
mirror names. Paths must be non-empty, clean UTF-8, slash-separated, relative,
and free of control characters. Empty or `.` paths, absolute paths, `..`,
backslashes, duplicates, and ancestor/descendant overlaps across either list
fail startup. A directory selection includes its regular-file descendants at
every indexed revision; no unlisted sibling is implied. A selected path that
is missing or resolves to a symlink, gitlink, or other special entry in HEAD
or any allowlisted revision refuses the complete replacement.

The stable `analysis-unit-v1` digest is SHA-256 over a domain separator plus
canonical JSON containing the schema, repository, unit name, sorted primary
paths, and sorted supporting paths. Source commits and revision selectors do
not enter that stable unit digest. On a successful index, phebs atomically
stores the unit state beside the exact indexed HEAD and allowlisted revision
set. A name or path change therefore queues a replacement even when HEAD is
unchanged; removing the entry queues a replacement that returns the repository
to unscoped state.

For a configured repository, `phebs-focused-index` receives only these
selected immutable blobs and status reports
`search_index_posture: focused`. The same exact scope must exist at HEAD and
every configured `rev:` lane; the unit digest remains stable while the
generation digest changes with the ordered revision set. Phebs never falls
back to whole-repository input when a focused build refuses.

Existing repository-root `index.scip` input is not relabeled as scoped; status
continues to report `typed_index_posture: repository-root-unbound` unless
`typed_index` explicitly names it. The only supported kind is `scip`, and its
path must be an exact `supporting` entry; selecting a parent directory does not
implicitly designate a typed index. The designation does not change the stable
unit digest because the artifact path is already part of semantic scope, but
it does change candidate-generation identity. A missing, special, stale, or
out-of-unit designated artifact refuses the focused typed-input publication.
Every SCIP document must also resolve inside the unit; phebs never falls back
to a repository-root `index.scip` for a focused repository.

Changing or removing `typed_index` keeps the semantic unit digest stable but
invalidates the previous candidate pointer and any current evidence carrying
the old candidate receipt. Code navigation and typed evidence remain
unavailable until the index → candidate → extraction chain publishes the new
designation; phebs never serves the old typed artifact during that interval.

Candidate planning records the committed unit membership of every planned
input and refuses stale or mismatched scope before extraction starts. Local
contract, field, topic, consumer, attribution, and Workbench implementation
readers replay only unit records and publish under the exact repository,
indexed HEAD commit, unit digest, and evidence domain. Changing the unit at the
same commit therefore cannot reuse or supersede evidence from the prior unit.
Repository-overlay caller candidates remain separately labeled planning input
for T30.6; they do not widen focused search or local evidence.

There is currently no production/test search split or Go-test overlay setting.
An exact `*_test.go` path admitted by the configured unit remains in focused
search. Candidate v4 stamps ordinary records with
`source_lane: base|go_test`; an exact `_test.go` suffix wins even under a
generated, mock, fixture, or `testdata` path, and every other ordinary
candidate is `base`. For repositories with a committed non-empty analysis unit,
focused local evidence now consumes only `base`; coverage and bounded receipts
report the excluded source-file count and declared bytes. Focused SCIP field
readers still validate the complete designated typed artifact, then remove
exact `_test.go` documents before source reads or joins and report their
excluded document/definition/occurrence counts. Repositories with an empty unit
digest record the lane but retain shipped whole-repository extraction
behavior. T30.6h will consume the retained lane classification for caller-leaf
planning. The source lane is not semantic unit scope, does not change the
stable unit digest, and is not a search configuration surface. There is no
setting that overrides the path-derived classification.

Repositories absent from `analysis_units` retain whole-repository indexing and
extraction. Their exact evidence scope has an empty unit digest, and their
legacy root `index.scip` behavior remains available. Migrated historical
whole-repository publications remain readable by their original commit and
empty-unit identity, but never satisfy a focused lookup.


### Service catalogs

`service_catalogs` selects at most one normalized
`phebs-service-catalog-v2` JSON authority for an exact repository. This is an
explicit ingestion boundary, not discovery: phebs does not scan directories,
run a build, read deployment configuration, or invoke an authority adapter to
invent services. The JSON file contains the complete accepted, proposal,
conflict, rejected, membership, optional override, and unowned projection.
One map entry therefore cannot express two competing base authorities or an
implicit precedence order.

Both source kinds use an absolute, clean path to a non-symlink regular local
file. The path is not a secret field and does not expand `${ENV}` or `~`.
The explicit `id` must equal `authority.id` in the JSON:

```yaml
service_catalogs:
  github.com/acme/monorepo:
    kind: committed
    id: build-catalog
    path: /srv/phebs-catalogs/acme-monorepo.json

  github.com/acme/other-repo:
    kind: operator
    id: platform-catalog
    version: "2026-08-04.1"
    path: /srv/phebs-catalogs/other-repo.json
    runtime: v3
```

`runtime` is closed to `v2` and `v3`; omission means `v2`. The `v3` value is
the explicit operator opt-in for the segmented catalog/state/search/
relationship runtime and requires provisional protobuf or Thrift extraction;
without one of those relationship-capable packs, configuration validation
refuses a requested v3 transition because no complete relationship target can
be built. After the first selector commit, startup also refuses if all
relationship-capable packs are disabled, including when YAML requests v2:
reversal and later mutation still require the complete holding authority. A
complete v3 candidate alone never changes product reads. Phebs keeps serving
the selected v2 authority while it builds and verifies every v3 target, then
changes all service-aware consumers at one durable selector CAS. Removing
`runtime: v3` performs the inverse operation:
v3 remains selected until a complete v2 target is rebuilt and verified, so a
missing or stale reverse target refuses instead of falling back or moving only
one pointer. Once the compatibility floor exists, an explicit v2 target is
accepted only when its immutable catalog can also reconstruct the holding v3
authority needed for a later safe mutation; a v2-valid shape outside the v3
envelope refuses before the selector changes.

Each selected runtime records a monotonic repository-local revision and the
exact catalog, state summary, search generation, and relationship root. A
restart accepts only a selector whose complete target still validates; a
corrupt or incomplete selected target stops startup before HTTP or MCP serves.
In-flight reads final-confirm the same selector revision after their ordinary
authorization and authority fences; compatibility-mode v2 reads final-confirm
that the selector is still absent. The store keeps layered irreversible
compatibility floors. Writing the first selector raises the T41.9 floor so an
older binary cannot ignore the selection. Opening the data directory with
T41.10 or later also raises the source-generation floor once that migration
commits, even when no selector exists and even if later startup work fails, so
the immediately preceding binary cannot misread the versioned v3 source
identity. Backup and restore carry and
revalidate the same selector and floors; neither operation changes the
evidence release posture.

A `committed` catalog's JSON uses the repository's exact indexed HEAD commit
as `authority.version`; configuration omits `version` because the indexed
commit supplies it. The normalized JSON is a selected projection of that
commit, not a catalog file recursively required to contain its own Git commit
ID. The selected bytes are still operator-supplied: T33.2 verifies the declared
HEAD fence and canonical byte immutability, not that the bytes exist in or
equal a blob from that commit. Reading an in-repository blob remains a separate
automatic-authority-adapter decision. An `operator` catalog uses the explicit
configured opaque `version`, which must equal its JSON authority version. A
selected catalog may carry the one optional versioned operator override defined
by the JSON contract. Reusing an authority/override version with different
canonical catalog bytes is refused.

Before publication, phebs streams the exact indexed commit's regular-file Git
tree once. Every catalog membership and unowned placement must resolve to at
least one regular file. Each regular file must be covered by an accepted
membership or an explicit unowned placement, never both. Proposal, conflict,
and rejected memberships retain provenance and must resolve, but they do not
become accepted authority; a proposal-only file must therefore also remain
explicitly unowned. File mode, blob object ID, and path enter the census
digest. Symlinks and gitlinks are not regular census members and cannot
satisfy a selected placement.

The catalog, source commit/census, and provenance form one immutable generation
stored in SurrealDB. A separate monotonic current revision records each actual
pointer transition. Invalid JSON, admission-limit refusal, a census gap, stale
HEAD, missing input, same-version byte change, or store failure leaves the
prior complete authority unchanged. Repositories reconcile independently at
startup, so one refusal is logged without preventing unrelated repositories
from publishing. A completed index run retries its repository's catalog after
the existing candidate handoff. An exact v2 retry rereads only the bounded
selected JSON and strict store rows; it does not repeat the Git census.

When no `service_catalogs` entry exists, an already indexed
`analysis-unit-v1` state imports deterministically as one accepted service.
Its existing name becomes the service key/display name, the exact v1 digest is
preserved, primary/supporting paths and an exact typed designation become v2
roles, and every other regular file becomes an exact unowned record. The
legacy repository/index state remains side by side and readable; a failed v2
replacement cannot relabel it. The unchanged 12,000 distinct-path admission
cap applies, so a legacy import with too many exact unowned files refuses while
the existing v1 pipeline remains authoritative. If both authorities exist,
removing the repository's `service_catalogs` entry makes the next reconcile
publish the deterministic v1 import as a real new current-pointer transition;
the prior v2 generation remains immutable but is no longer current.

Each current catalog also reconciles one independently fenced lifecycle row per
service key. A service-local desired digest binds the key's incarnation and
changes for its exact source or own record/memberships, not for a sibling-only
catalog edit. Accepted services begin
`unavailable` until an exact active generation is published; later exact
transitions may be `current` or `stale`, conflicts stay explicit, and rejected
or omitted prior keys retain removed tombstones. Re-adding a removed key mints
the next incarnation and never inherits its prior active identity. Catalog and
state publication are consecutive transactions: a crash between them makes
state reads unavailable until the exact startup/index retry repairs the point
summary; it never serves a mixed catalog/state view.

T33.4 registers authorization-first read surfaces over this state. HTTP
`GET /api/services?repository=...` returns a service-key-ordered page and
`GET /api/service?repository=...&service_key=...` returns exact detail; MCP
provides the identical `list_services` and `get_service` projections. List
pages default to 50 and cap at 100. Removed tombstones are excluded unless
`include_removed=true`; optional `status` and `disposition` filters are closed
to the catalog/lifecycle enums. List rows expose membership, role, and distinct
path counts but no paths. Exact detail alone returns successors and membership
triples, bounded by 128 distinct paths, 64 KiB of distinct path bytes, 640 role
records, and the existing 4,000 aggregate-successor ceiling. Both transports
cap responses at 1 MiB and cursors at 16 KiB.

Each inventory request scans and verifies at most 500 service-key-ordered rows
through the existing repository/key seek index, applies the optional filters
in memory, and returns at most 100 services. A sparse filter may therefore
return an empty page with a nonempty continuation. Follow the cursor until it
is empty; an empty page does not prove that no later service matches. This
bounded scan avoids both a retained-tombstone-wide filter query and new
write-time status/disposition indexes.

Repository authorization runs before filters, cursors, catalog/state counts,
or memberships. Missing, deleting, and hidden repositories therefore share one
not-found result. A cursor binds the permission projection, query/order/filter,
catalog generation/revision, summary digest/revision, and last service
key/incarnation; any authority, lifecycle, permission, or removal/re-add
transition refuses continuation. Building a page or detail strict-decodes the
admitted catalog once; the final response fence rereads only the catalog
pointer and state summary. Neither surface reads Git, blobs, shards, or source
content. The capability-gated repository → Services directory now consumes
these same bounded projections, retains its exact request in the hash route,
and labels paths and successors as source-free catalog metadata. It adds no
configuration key and never makes a runtime-relationship claim. T34.3 owns
real active physical generation transitions, and T35 owns retained-generation
GC.


### Provisional Change Workbench

`experimental.provisional_workbench` is default-dark and does not independently
enable evidence. It binds the existing Change Workbench HTTP, UI, and MCP
surfaces only when `experimental.provisional_proto_extraction` or
`experimental.provisional_thrift_extraction` already supplies the instance's
store-derived Contract Atlas. It adds no route or capability identifier and
does not change the production-registration gate.

When that complete shared Workbench evidence set is available, MCP also
registers the read-only `get_change_workbench_impact` projection. It accepts
the same exact Investigation revision, service filters, page size, and cursor
as HTTP and returns the same relationship authority and typed gaps. Both
transports reject an encoded impact page above 8 MiB. The tool stays
undiscoverable when the shared impact service, Workbench/checklist annex, or
principal projection is absent; it adds no configuration switch of its own.

`PHEBS_SYNTHETIC_WORKBENCH` is parsed strictly before the registration matrix:
only an empty value or exact `1` is accepted. The two stable typed startup
refusal classes are:

- `workbench-evidence-prerequisite`: the flag lacks a protobuf/Thrift
  declaration lane or one of the shared evidence/catalog/workbench services is
  unavailable.
- `workbench-authority-conflict`: provisional binding is combined with
  `PHEBS_SYNTHETIC_WORKBENCH=1` or a non-empty
  `PHEBS_CONTRACT_ATLAS_FIXTURE`.

Investigation fixtures and `PHEBS_WORKBENCH_CLOSURE_REPO` do not replace
catalog authority and may coexist with the provisional binding.

| `provisional_workbench` | Protobuf/Thrift declaration lane | Synthetic Workbench | Contract Atlas fixture | Result |
|---|---|---|---|---|
| `false` | any | empty | any | No Workbench registration |
| `false` | any | `1` | present, with Investigation fixtures | Existing fixture-backed synthetic Workbench |
| `false` | any | `1` | absent, or Investigation fixtures absent | Existing synthetic missing-fixture refusal |
| `true` | present | empty | absent | Existing Workbench surfaces over store-derived published evidence |
| `true` | absent | empty | absent | `workbench-evidence-prerequisite` |
| `true` | any | `1` | any | `workbench-authority-conflict` |
| `true` | any | empty | present | `workbench-authority-conflict` |

Workbench rows inherit the provisional evidence posture. They establish no
runtime use, completeness, compatibility, migration completion,
decommission safety, or extraction accuracy.


### Authentication

Authentication is always required for the UI, application API, and MCP. A
fresh installation has three supported enrollment paths:

1. **Interactive setup:** configure neither `bootstrap_user` nor OIDC. Copy
  the ephemeral setup token from the local startup log into the UI's
   first-run form. The first account is an administrator.
2. **Bootstrap user:** provision the first administrator from config:
  ```yaml
   auth:
     bootstrap_user:
       email: admin@example.com
       display_name: Phebs Admin
       password: "${PHEBS_BOOTSTRAP_PASSWORD}"
  ```
   The password is used only when the first user is created and is stored as
   an Argon2id hash. Remove the block afterward; changing it does not rotate
   the existing password. If users already exist and the configured email is
   absent, startup fails instead of creating a surprise administrator.
3. **OIDC:** configure one provider and use **Continue with SSO**. The first
  verified OIDC identity becomes administrator; later identities are regular
   users. The provider therefore owns enrollment policy for this single-tenant
   deployment.

Browser sessions live in SurrealDB and survive process restarts. The cookie is
`HttpOnly`, `SameSite=Lax`, `Secure` by default, and stores only a random
token whose SHA-256 hash is persisted. Unsafe cookie-authenticated requests
also require the per-session `X-CSRF-Token`; the UI supplies it. Login/setup
attempts reserve a per-client slot before password work (8 credential failures
per 5 minutes), and Argon2id work is globally capped at four concurrent hashes;
overload fails with `429` instead of growing memory without bound. By default
the client is the direct peer. Behind a reverse proxy, list every trusted proxy
hop CIDR, including the direct peer, under `auth.trusted_proxies`; forwarded
headers from all other peers are ignored, and trusted chains are walked from
the nearest proxy outward.

#### API keys and legacy migration

After signing in, open **Settings**, name a key, and copy the returned
`phebs_<id>.<secret>` token immediately; the secret is shown once and only its
SHA-256 hash is stored. Send it as `Authorization: Bearer <token>`. Keys are
individually revocable and their last-use time is recorded. Key listing,
creation, and revocation require a CSRF-protected browser session; bearer keys
cannot mint replacements or revoke sibling credentials.

Named keys have an immutable, closed capability set. Omitting
`capabilities`, sending `[]`, or leaving **Allow Investigation writes**
unchecked creates a read-only key. The only reviewed value is
`investigation:write`; unknown, duplicate, malformed, and future values are
rejected. Selecting it explicitly allows the key to attempt Workbench
preview-binding, create/revise, retained compatibility actions when a future
adapter exposes them, and Disposition writes. It does not grant repository
visibility, Investigation access, ownership, administrator status, or a way
around current-Revision, preview, snapshot, or idempotency checks. Capability
names appear in Settings and `GET /api/auth/keys`; token secrets and hashes
never do.

Capabilities cannot be edited after creation. To change authority, create a
replacement, deploy it, and revoke the old key. Treat
`investigation:write` as increased authority: use least privilege, issue one
narrowly capable key per agent so it can be revoked independently, and revoke
it immediately if leakage is suspected. A leaked capable key can attempt
durable Investigation mutations as its owning user until it is revoked,
expires, or the user is disabled. Browser sessions remain governed by their
CSRF-protected session boundary and need no capability selection.

Existing `auth.api_key` deployments continue to work during migration. At
startup phebs imports only that key's hash as `Legacy config key`. Create a
named key for each client, deploy those tokens, then remove `auth.api_key`;
the next startup deletes the legacy key row. The legacy principal has no user
identity, has an empty capability set, and cannot manage named keys or perform
Investigation mutations itself. Existing named keys likewise migrate with an
empty set; their tokens, hashes, identity, expiry, revocation, and existing
read behavior do not change.
The startup migration records its exact generation and skips the key-table
backfill after completion. An older binary that encounters a later or unknown
capability-migration generation fails closed without overwriting that marker.

#### OpenID Connect

```yaml
auth:
  oidc:
    issuer_url: https://idp.example.com
    client_id: phebs
    client_secret: "${PHEBS_OIDC_CLIENT_SECRET}"
    redirect_url: https://phebs.example.com/api/auth/oidc/callback
    scopes: [groups]  # optional extras; openid/profile/email are automatic
```

Register the redirect URL exactly at the provider. Discovery happens during
startup and failure stops the server. The authorization-code flow uses PKCE,
state, and nonce, verifies the ID token and access-token hash when present,
and requires `email_verified=true`. Identities bind only to issuer + subject;
email equality never links an OIDC identity to an existing local or OIDC
account, and collisions fail closed. Anonymous authorization-flow
sessions expire after 10 minutes, starts are rate limited, and starting a new
flow never clears an already authenticated browser session.

### `type: github` connections

```yaml
- name: github-personal
  type: github
  token: "${GITHUB_TOKEN}"   # PAT; omit for public repos only
  orgs:  [my-org]            # all repos of each org
  users: [bmeddeb]           # all repos owned by each user
  repos: [owner/name]        # explicit repos
  exclude:
    archived: true
    forks: true
    repos: ["*/*-mirror"]    # glob on owner/name
```

At least one of `orgs`/`users`/`repos` is required. The token is sent as a
bearer to api.github.com and injected into git fetches per-invocation — it is
never written into mirror config or the database. Rate limits are honored
automatically (the sync waits out `Retry-After` / `X-RateLimit-Reset`).

A `users:` entry naming the token's own account includes that account's
private repos: GitHub's public user listing omits them, so phebs additionally
lists the token owner via the authenticated endpoint and unions the two (a
fine-grained PAT restricted to select repositories still gets all public
repos). Other users list public repos only; private repos elsewhere are
reachable via `orgs:` or explicit `repos:` entries.

#### GitHub App auth

Instead of a PAT, a github connection can authenticate as an App
installation (higher rate limits, per-install scoping):

```yaml
- name: gh-app
  type: github
  app:
    id: 12345                  # the App's ID
    installation_id: 67890     # the installation on your org/account
    private_key_path: /etc/phebs/app.pem   # or private_key: "${APP_KEY_PEM}"
  orgs: [my-org]               # optional — omit selectors to sync every
                               # repo the installation was granted
```

`app` and `token` are mutually exclusive. Each sync run exchanges the App's
key for a fresh ~1-hour installation token (RS256 JWT, no cached state), so
tokens never go stale. Installation tokens have no user identity: `users:`
entries list public repos only under App auth. Without any selectors the
connection syncs exactly the installation's granted repositories.

### `type: gitlab` connections

```yaml
- name: gitlab-work
  type: gitlab
  url: https://git.example.com  # self-hosted base URL; omit for gitlab.com
  token: "${GITLAB_TOKEN}"      # PAT; omit for public projects only
  groups: [team/platform]       # all projects of each group, subgroups included
  users:  [dev]                 # all projects owned by each user
  repos:  [solo/tool]           # explicit projects by full path
  exclude:
    archived: true
    forks: true
    repos: ["*/*/sandbox-*"]    # glob on the full project path
```

At least one of `groups`/`users`/`repos` is required. Unlike GitHub, GitLab's
user listing is requester-scoped, so a token's own private projects appear
without special-casing. The token authenticates the API (bearer) and git
fetches (HTTP basic as the `oauth2` pseudo-user, injected per-invocation) —
it is never written into mirror config or the database. Rate limits are
honored automatically (429 `Retry-After`). Repos are named
`<host>/<full/project/path>`.

### `type: gitea` connections

```yaml
- name: gitea-forge
  type: gitea
  url: https://gitea.example.com  # required: base URL of the instance
  token: "${GITEA_TOKEN}"         # PAT; omit for public repos only
  orgs:  [acme]                   # all repos of each org
  users: [dev]                    # all repos owned by each user
  repos: [owner/name]             # explicit repos
  exclude:
    archived: true
    forks: true
    repos: ["*/*-mirror"]
```

`url` is required (there is no canonical hosted Gitea); at least one of
`orgs`/`users`/`repos` too. Listings are requester-scoped, so a token sees
its accessible private repos. The token authenticates the API
(`Authorization: token …`) and git fetches (HTTP basic, token as username,
injected per-invocation) — never persisted. Repos are named
`<host>/<owner>/<name>`.

### `type: git` connections

```yaml
- name: any-git
  type: git
  url: https://example.com/repo.git    # any clone URL: https, ssh, scp-like
```

Private HTTP(S) remotes use transient Basic auth:

```yaml
- name: private-git
  type: git
  url: https://git.example.com/team/repo.git
  http_auth:
    username: "${GIT_HTTP_USERNAME}"
    password: "${GIT_HTTP_PASSWORD}"
```

Both fields are required. Credentials are passed to each Git process and are
never written to the repo row, API, logs, or mirror config. HTTP URL userinfo,
query parameters, and fragments are rejected; migrate any
`https://user:password@host/repo.git` configuration to `http_auth`. SSH URLs
may retain a username such as `ssh://git@host/repo.git`, but not a password.

Local repositories use a quoted home-relative path, a plain absolute path, or
a `file://` URL. A home-relative path is portable across workstations:

```yaml
- name: my-project
  type: git
  url: "~/src/my-project"
  watch: true            # see [Connecting repositories](#connecting-repositories), watch mode
```

Phebs resolves that path through the account running the server and gives it
the stable repository identity `local/src/my-project`; Git and persisted clone
metadata receive the absolute path. Only exact `~/...` paths are supported.
There is no shell expansion or filesystem discovery: `~other/repo`,
`file://~/repo`, `~/../repo`, and paths containing `*`, `?`, `[`, or `\` fail
configuration admission. Use one connection per exact local repository.

Existing absolute and `file://` paths retain their historical full-path
identities, such as `local/Users/ben/src/my-project`. Changing an existing
connection from an absolute path to `~/src/my-project` intentionally creates
the portable identity; the previous row then follows the normal orphan and
`sync.cleanup_orphans` policy.



## Connecting repositories



### Sync lifecycle

At boot, phebs ensures one pending sync job per configured connection. A sync
resolves the connection to repo rows, mirrors each
repo into `$DATA/repos/<host>/<path>.git`, and chains an indexing job per
synced repo. Re-syncs are incremental (`git fetch --prune`).

Beyond boot, syncs happen when:

- a **watched** local repo's HEAD moves (see below);
- the **re-sync cadence** fires (`sync.resync_interval`, default `1h`, `"0"`
disables): every remote connection is re-synced, collapsing overlap into
one pending successor — local repos are covered by boot and watch instead;
- a **push webhook** arrives (see below);
- you press **Reindex** in the UI or call `POST /api/reindex` (re-index only);
- phebs restarts.



### Push webhooks

`POST /api/webhook` turns code-host push events into targeted fetches — the
changed repo is fetched and reindexed without waiting for a poll, and without
re-listing the host:

```yaml
webhook:
  secret: "${WEBHOOK_SECRET}"   # required to enable the endpoint
```

Point a GitHub (or Gitea — it sends GitHub-compatible headers, verified live)
webhook at `https://your-phebs/api/webhook` with content type
`application/json` and the same secret. Payload signatures
(`X-Hub-Signature-256`) are verified in constant time; the endpoint does not
exist unless a secret is configured, and it ignores pushes for repos phebs
doesn't know. `repository` and `installation_repositories` events (repo
created/deleted/renamed, App grants changed) re-sync the remote connections
so membership catches up. GitLab webhooks use a different scheme and are not
yet supported — the re-sync cadence covers those.

### Watch mode (local repos)

`watch: true` on a local git connection—absolute, `file://`, or quoted
`~/...`—makes phebs poll the resolved repo's HEAD and each configured
allowlisted ref (every ~3 s), then re-sync + re-index whenever one moves.
**HEAD commits, branch switches, and allowlisted branch/tag moves trigger
reindexing; uncommitted working-tree edits and non-allowlisted feature branches
do not.**

Watched mirrors **follow the branch you have checked out** — switch to
`feature`, commit, and search reflects `feature`. A detached HEAD (mid-rebase,
bisect) keeps the last good index until you land somewhere.

End-to-end latency is roughly `watch tick (≤3 s) + poll_interval + index time`. With `sync.poll_interval: 1s`, a commit is searchable in ~1–2 s.

### Orphans and cleanup

A repo no connection claims (you removed the connection or narrowed its
filters) is flagged **orphaned** on the Repos page and in `/api/repo-status`.
By default orphans are kept; set `sync.cleanup_orphans: true` to delete their
rows, mirrors, and index shards after each sync. Every startup audits repo
rows, mirror configs, and shard metadata even when deletion is disabled. It
scrubs legacy URL credentials, hides invalid/unsafe legacy rows, and repairs
DB/shard revision mismatches by forcing a new index. Any audit, quarantine, or
repair failure stops startup so unverified state is never served. Destructive cleanup remains gated by `cleanup_orphans` and only
touches validated, non-symlinked paths under the data directory.
