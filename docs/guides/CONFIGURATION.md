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

### Config file permissions

The config file can hold API keys, OIDC client secrets, webhook secrets, and
connection tokens, so phebs refuses to start when the config file grants group or others any read, write,
or execute access — any group/other permission bit set (`mode & 0077 != 0`). The
check
applies to `serve`, `backup`, and `restore` whenever `-config` names a real
file; embedded bytes and defaults skip it. Keep the file owner-only:

```sh
chmod 600 phebs.yaml
```

Operators who knowingly run with looser permissions (e.g. a shared config
mounted into a container) can downgrade the refusal to a logged warning with
`--allow-insecure-config-perms`.

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
| `server.security_headers`                   | `true`           | hardening response headers (X-Content-Type-Options, X-Frame-Options, Referrer-Policy, CSP); set `false` only when a reverse proxy manages these headers instead    |
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
does not disable 90% hard-watermark admission, live roots, proof pins, active
leases, tombstones, or the independent
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
registry; T40.7 added `evidence_chunk` and T40.10 added
`extraction_domain_root`. T46.1 retired all Investigation and change-planning
components, the Investigation run-job history component, and the Investigation
artifact pin namespace. The current registry has 11 owners and 28 components:
21 core and seven derived. The unconditional
`unbounded_historical_publication_retention` warning is reported in
`X-Phebs-Warning-Code` on every endpoint response, including
authorization and internal errors, while successful bodies also carry
`warning_code`. The core collector populates 21 SurrealDB components with
bounded aggregate per-table or per-pin-namespace row totals.
T30.6r completes the remaining seven derived components and, where the
operating system supplies the supported descriptor-bound filesystem-capacity
primitive, both installation data-volume metrics
under the fixed budgets described below. Unsupported platforms retain typed
unavailable capacity with a localized cause. Per-component physical-database
attribution stays explicitly unavailable.
The populated byte metrics remain logical encoded outcome-receipt bytes,
canonical proof-content bytes, and canonical caller-receipt bytes. Ordered `logical_encoded`,
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
The fixed 4,096-report/4,124-scan aggregate allocation and
64-KiB response ceiling are implementation safety contracts, not configuration
keys. Registry indices 0–7 receive 147 report slots plus one sentinel; later
indices receive 146 plus one. The core collector therefore receives 3,074
report and at most 3,095 scan identities; the seven derived components receive
1,022/1,029. The store accepts report allocations from 1 through 147 only
with exactly one private scan sentinel and enforces the same core aggregate
ceiling. The core collector uses at most five readiness/catalog checks and 22
bounded row-range queries; four derived authority selections use at most nine
more client calls, for a 36-call retention-status ceiling per authorized
request. Failures
remain localized as unavailable metrics and emit at most one `not_ready` or
`query_error` log event per failed component. The retention collector reuses
the `evidence_chunk` and `extraction_domain_root` schema and adds no inventory
index, backfill, writer-generation bump, sync-tick work, writer work, or
retention lifecycle change. T30.6r populates the final seven derived components and,
where the operating system supports the descriptor-bound filesystem-capacity
primitive, the installation total/available metrics. Its four authority
selections use at most nine store client calls; the one batched caller fence
performs at most 584 server-internal point reads—four for each of at most 146
authorities—plus its marker check. Incremental filesystem work is fixed at 163,840 entry
observations, 4,096 charged stats, 64 MiB of manifest metadata, 256 queued
caller directories, and five simultaneous structural descriptors:
at most three collector-retained handles plus up to two Go/platform directory
iterator duplicates or rooted traversal internals. Every returned raw name
consumes the observation budget. Names are otherwise names-only; only
recognized names receive explicit descriptor-rooted `Lstat` checks. These
limits and the 256-name directory batch are implementation safety contracts,
not configuration keys. T30.6r localizes at most nine diagnostics,
bringing the complete status-path event ceiling to 30; neither is configurable.
The stat ceiling includes explicit descriptor-rooted `Lstat` checks,
conservative open-time `fstat` charges, and one conservative slot per name-batch
(`Readdirnames`) call for the Windows error-classification `File.Stat` fallback.
The 147-report/148-scan maximum slots allocate the response envelope rather than promise
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
contract, field, topic, consumer, attribution, and implementation
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
individually revocable and their last-use time is recorded. Key creation
accepts an optional `expires_at` RFC3339 timestamp, which must be in the
future; an expired key fails authentication. Omitting `expires_at` (or sending
`null`) creates a key that never expires, and existing keys without an expiry
keep working unchanged. Key listing,
creation, and revocation require a CSRF-protected browser session; bearer keys
cannot mint replacements or revoke sibling credentials.

Named API keys are read-only. The `capabilities` JSON field remains for wire
compatibility and is always an empty array in current key metadata. Omit it
or send `[]` when creating a key; a nonempty selection is rejected. Existing
retired capability values are cleared on upgrade. Keys still inherit only
their owner's permitted read scope, never administrator or repository access
on their own. Browser-session mutations remain governed by their existing
CSRF, authorization, and ownership checks. Token secrets and hashes never
appear in Settings or `GET /api/auth/keys`.

Existing `auth.api_key` deployments continue to work during migration. At
startup phebs imports only that key's hash as `Legacy config key`. Create a
named key for each client, deploy those tokens, then remove `auth.api_key`;
the next startup deletes the legacy key row. The legacy principal has no user
identity, has an empty capability set, and cannot manage named keys or perform
product mutations itself. An administrator can revoke the legacy key
through the API with `DELETE /api/auth/keys/legacy-config`; anyone else
receives the same not-found response as for any out-of-scope key id. The
revocation survives restarts: re-syncing an unchanged `auth.api_key` does not
resurrect a revoked key. Rotating the configured key clears the revocation,
and removing `auth.api_key` deletes the legacy row at the next startup.
Once this version has opened the store, a generation-named database event also
keeps that boundary fail-closed across an older binary reopen. The previous
legacy writer cannot replace the reserved row identity or recreate a deleted
legacy row, so its configured legacy bearer remains unavailable; current
binaries can still rotate, remove, and explicitly recreate the credential as
described above.
Existing named keys retain their tokens, hashes, identity, expiry,
revocation, and read behavior while retired capability values are cleared.
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

## Managed typed-index contract (T45.2)

Managed indexing remains unavailable in ordinary runtime: this ticket defines
an internal contract, not a configuration switch, worker or HTTP/MCP endpoint.
The accepted initial profile is Bazel/rules_go/scip-go. Its sealed reduced
configuration stays `linux/arm64`, with skip-tests/skip-implementations and
generated documents omitted. Execution admits the live Linux host, `arm64` or
`amd64`. Existing
committed-SCIP navigation keeps its behavior. T45.3 must supply the generated
lane and canonical ordering before generated coverage can advance; T45.4 owns
the executor and T45.5 the routed reader.

A managed build executes repository-controlled build logic. It is not a pure
source extractor, and ordinary repository visibility never grants execution.
Future API adapters must pass authenticated administrator authority separately
from request JSON and load authoritative HEAD, repository incarnation, source
generation and current operator profile epoch from trusted server state.
No browser input can provide executable paths, shell commands, environment,
rc content, cache locations, credentials or tool identities. Profiles are
operator-owned; every tool/image/config/resource/prehydration identity is
bound. Profile replacement requires an atomic durable epoch increment even
when values repeat (A→B→A); request retries retain their exact idempotency key.
The executor must recheck authority at transitions and before publication.

Profile and request JSON use exact schema field spelling/order and explicit
fields, including empty optional values; whitespace is accepted. Unknown,
duplicate, omitted, case-aliased or malformed fields refuse. Profile metadata
is bounded to 16 KiB, requests to 8 KiB and source-free progress to 512 bytes.
Do not put raw tool errors, source, credentials or paths in progress reasons.
Reported elapsed time can exceed the 300-second limit when describing a
watchdog refusal; this never increases the execution budget.

The measured execution envelope stays fixed:

| Resource | Limit |
|---|---:|
| Memory | 4,533,092,352 bytes |
| Scratch | 4,573,403,136 bytes; 262,144 inodes |
| Tasks / descriptors | 294 tasks; 128 descriptors per process |
| CPU | 200,000 / 100,000 microseconds (2 CPUs) |
| Wall | 300 seconds |
| Worker output / SCIP | 16,777,216 / 2,285,819 bytes |

Scratch requires ext4 with verified direct I/O and request-private custody;
network egress and remote/shared caches stay denied. Ambient system, home and
workspace bazelrc discovery is disabled. The sole optional copied rc is the
exact resolved line `build --compilation_mode=fastbuild` plus newline, with a
bound SHA-256; imports, extra configuration, command substitution and overrides
refuse. Tool and launcher paths and argv recipes are Phebs-owned.

Planning checks tool-process quiescence before reclaiming the private Gazelle
compiler cache. A graph that never creates that cache has nothing to reclaim;
absence is accepted only beneath a canonical existing directory ancestor.
Aliases, dangling links and unexpected entries still refuse. An absent cache
is not created, and an existing cache retains its inventory and deletion limits.
Neutral preparation post-run sealing requires the host completion receipt bound
to the exact returned result and configured run/executable/image identities.
Container removal is a host observation; a worker result cannot attest it.

The immutable prehydration manifest retains the reviewed importer ceilings:
16 MiB of metadata, 50,000 files, 20,000 directories, 256 MiB per file and
2 GiB total. It is digest-bound and rejects unsafe/duplicate/file-directory
colliding paths. These are validation bounds, not evidence that every shape
fits execution capacity. Before either Bazel planning or member execution, T45.4 must reserve capacity, copy
and verify every declared file and mode into new private immutable inputs,
reject undeclared files/links, verify tools/image/rc and actual policy, and
produce trusted preparation evidence. `ValidatePreparation` checks that bound
evidence; a caller-supplied boolean is not proof of filesystem or containment
facts. This contract performs no copy or sandbox launch itself.

Every workspace, input copy, cache, server/worker state and output path derives
from the exact request digest beneath `typed-index/attempts/`. Lifecycle must
inventory that custody on success, refusal, cancellation and restart. A planned
successor binds its exact original request and sealed package-load map; changing
any source, profile, tool, universe or idempotency field invalidates that link.
Disabled contract admission returns before decoding, hashing or inventory;
there is no default poller, startup scan, child or publication work.

The prospective executor's bundle also binds its fixed executable
`phebs-typed-worker` entry and non-executable `typed-host-tools.json` metadata
(at most 1 KiB). The latter uses schema `phebs-typed-host-tools-v1` and a
`mkfs_sha256` digest. It cannot select a host path or command; native preparation
still verifies the compiled formatter path. These local contracts do not enable
an executor or replace verification of the private input copy.

For neutral native preparation, the offline input check binds the test binary
to both executable bundle entry paths before staging, and remote dispatch
rehashes both staged helpers before spending its one shot. The final cmd helper
remains a separately verified postrun substitution.

If neutral native preparation stops with supervisor exit 125 and
`site=allow_live`, the supervisor rejected the live allowance before reading
controls or starting a worker. The next diagnostic distinguishes a boot-ID
read or mismatch, time-namespace stat or mismatch, BOOTTIME read, and
allowance-window refusal with fixed, value-free tokens. A token identifies the
failed check, not the underlying host cause. Preserve the spent stage and
its records; another native attempt requires a fresh ID and scoped decision.
The fresh attempt's own supervisor live check is the clock-domain admission
gate. A host check or a separate container cannot prove the next container's
clock identity.

In the prep-12 neutral attempt, `site=allow_nsdiff` meant that the supervisor
read the expected boot ID but its container time-namespace device or inode
differed from the sealed host identity. It stopped before controls or worker
admission and produced no result or receipt. Docker 29.5 enables private
container time namespaces by default on supported kernels; the observed VM
ran Docker 29.5.2 without a `time-namespaces` override. This is the likely
host cause, not a reason to weaken the required common clock domain. Docker's
daemon-wide `features.time-namespaces=false` is a candidate host setting; it
requires a separate host change and verification before another native
attempt. The spent stage was later freed by exact guarded cleanup; its sealed
STOP evidence remains retained.

The dedicated neutral VM now has that exact feature set to `false`. Its
replacement config passed `dockerd --validate`, one Docker restart succeeded,
and the daemon reports the override with no containers present. The spent
prep-12 attempt was not rerun; a fresh attempt must still pass its own
supervisor clock gate.

Native acceptance and preparation no longer read a Colima profile. Set
`PHEBS_TYPED_NATIVE_TRANSPORT=ssh` and `PHEBS_TYPED_NATIVE_SSH_TARGET` to the
dedicated user and host, with that host key already in `known_hosts`. On the
execution host itself, set `PHEBS_TYPED_NATIVE_TRANSPORT=direct`. Either mode runs
`sudo -n python3` (or `python3` when direct and already root). The host is the
live Linux machine: `x86_64` seals `amd64` and `aarch64` seals `arm64`. CPU
count is the live processor count and must be at least 2. MemTotal must be at
least 4,533,092,352 bytes. Docker listens on `/var/run/docker.sock` at API
v1.47, and `features.time-namespaces` stays `false`. Preflight requires
seccomp's builtin profile. AppArmor remains mandatory when the daemon
advertises it. A daemon that advertises seccomp and `name=cgroupns` is
admitted. `observe` writes
`deployment.json` from that live host. An exact `Content-Length` body stays
exact. A chunked body is accepted under the same 1 MiB cap, and both
encodings together are refused. The worker cgroup quota stays 2 CPUs
(`200000/100000`). The historical reduced profile remains
`phebs-typed-profile-v1` with `GOARCH=arm64`. An amd64 host seals
`phebs-typed-profile-amd64-v1` instead and refuses the arm64 profile.
Tool builds use `GOAMD64=v1` on amd64 and `GOARM64=v8.0` on arm64. Scratch,
wall, output, task, and descriptor limits are
unchanged. Keep stage directories and collected receipts on durable storage;
a failed attempt stays retained.

Prep-13 then reached a valid supervisor report but stopped at the later
`inspect_stopped` check with exit 125 and no fd2 site token. Its exact report
reason was not retained. For future stopped-inspection refusals, the error
names the first failed predicate and, on a nonzero supervisor exit, reports
only an allowlisted stop reason and optional sampling stage. Unknown strings
become `unknown`; the check remains fail closed. Prep-13 remains spent, with
no result or receipt; its stage was later freed by exact guarded cleanup and
its sealed STOP evidence remains retained.

Prep-14 then stopped at the reported `worker_start` reason, with no result or
receipt. Its exact child-start syscall was not retained. The staged bundle's
root-owned `0700` directories and `0500` helper could not be traversed or
executed by the sandbox worker's UID 65534. Future native preparation stages
make only the read-only bundle view worker-visible (`0555` directories and
executables, `0444` other files) before sealing the stage; the private stage
root and host package stay owner-only. The spent prep-14 stage was later
freed by exact guarded cleanup; its sealed STOP evidence remains retained.

Prep-15 passed those access checks and reached the worker, but the worker
returned 125 and the supervisor reported `worker_failed`. No result or receipt
exists; the exact worker step was not retained. For future attempts, a terminal
error includes `worker_site=<fixed token>` only when the valid supervisor report
contains one exact worker refusal frame. Missing or malformed frames report
`unknown` without copying worker stderr. The spent prep-15 stage was later
freed by exact guarded cleanup; its sealed STOP records remain retained.
Another attempt needs a fresh ID and scoped decision.

Native acceptance checks the fixed system formatter separately from staged
inputs: the standard `mkfs.ext4` alias may resolve to its canonical, root-owned
`mke2fs` file, whose bounded bytes must match the sealed digest. Staged input
aliases still refuse. Workspace and host scratch may use distinct existing
filesystems only when each independently admits its unchanged complete budget;
neutral preparation alone does not establish controller acceptance.

### Immutable bundle contract (T45.3)

The managed contract defines a Phebs bundle over bounded SCIP members, exact
package-load coverage and document/symbol routing controls. It is not a new
configuration switch or an enabled provider. Incomplete attempts retain a
terminal manifest but have no publishable current root. The executor must
recheck current source, profile and tool authority in its durable publication
transaction; the contract's pointer proposal alone does not publish anything.

Generated bytes have a reserved `.phebs-generated/<package-unit-hash>/` path,
exact digest and size, and source/request/profile/tool/plan/action provenance.
Raw Bazel output paths are not source authority. The bundle refuses missing,
corrupt, stale or substituted generated bytes and repository namespace
collisions. Until bounded custody and routed reading are connected, the reduced
runtime posture continues to omit generated documents and charge their payload.

The existing `phebs-typed-profile-v1` contract fixes generated documents to
`omit` and rejects generated plans and bundles. The prospective
`phebs-typed-profile-v2` contract explicitly selects `sealed`; it retains every
skip policy and measured resource cap. This contract distinction does not
install a profile or enable generation.

Member canonicalization preserves symbol identity, ranges, roles and ordered
text while sorting unordered records. It refuses conflicting metadata. The
retained proto and fan-out public SCIP files contain conflicting metadata for
blank-identifier symbols, so the new provider's compatibility gate remains open;
committed-SCIP navigation and the earlier accepted REDUCE decision are unchanged.

The neutral post-run role seals `typed-host-tools.json` into the FINAL bundle
using the preparation config's verified fixed formatter digest. An already
present selection or host-tool metadata entry refuses. The default profile
install expects epoch 0 and proves successor 1. A reviewed reseal can explicitly
set `-typed-preparation-profile-epoch=N`; the store CAS requires predecessor
`N-1`. Before CAS the role verifies the predecessor profile and full seed
predicates, refusing desire, cancellation, restore markers or other controls.
The role independently checks successor `N`, exact source and all
seed predicates before export. Preserve prior receipts and use a fresh native
acceptance ID with the new seed, inventory and profile hashes.

Future native acceptance failures use `native_acceptance_failed/<fixed site>`
for the called boundary. Only fixed local engine seed-import errors refine that
site; library errors keep the generic engine boundary. These tokens do not carry
raw errors or response bodies. Preserve spent receipts without retrospectively
assigning a cause, inspect owned custody separately, and use a fresh ID.

A fresh neutral acceptance engine creates only the compiled `t454` namespace
and `neutral` database before importing its authenticated seed. The bootstrap
requires exactly two OK rows within its five-second,16KiB response bounds;
full source/profile/epoch/history/growth checks still run after import.

`OPTION IMPORT` suppresses result rows, so HTTP200 with `[]` is accepted.
Null, malformed/non-array responses, ERR rows and response overflow still
refuse. HTTP success alone never replaces the full imported seed predicates.

Native scratch preparation writes actual zeros across the fixed preallocated
backing image before formatting, retaining the same capacity, physical-allocation
and time bounds. A fresh image adds 4,573,401,088 host writes before the existing sync while
the existing preparation lock is held; a complete cold plan/execute attempt adds
two such passes. Ready empty reuse adds none. The fixed 64KiB buffer does not
bound kernel dirty-page memory or write/sync latency. Cancellation checks occur
between writes. Unsupported allocation realizations still refuse execution.

Exact scratch retirement checks ownership and geometry independently from the
execution allocation ceiling, so an overallocated owned image can be retired
through the normal locked loop/mount/daemon checks. Ambiguous formatting or
foreign/replaced custody still refuses. Cleanup does not turn a failed native
receipt into a pass; record its outcome separately and use fresh run IDs.

An overallocated image still makes native execution fail even when exact cleanup
removes it successfully: cleanup returns `ErrCustody` after retiring that image.
Record resource absence separately from the failed execution result. Initialization
and successful retirement do not waive post-worker allocation checks.

Native observation and recovery use a pinned inspection loader for the exact
input's private main container journal and optional pending journal. Startup
checks their canonical metadata and exact retained identity before stale-job
reaping. Unknown, malformed, mismatched or pending-only custody remains held;
publication and workspace deletion keep their strict journal refusal until
native cleanup proves absence. No daemon request occurs during this metadata
census. Native acceptance receipts additionally record closed observation-site
tokens and `emergency=completed` or `held`, independently of the primary refusal.
These fields do not replace the required native-absence and workspace-drain
proofs, and spent receipts retain their original fields.

The native acceptance child also retains a closed `failure_site` for its
post-publication schedule, duplicate-coordinator, replay and growth checks.
A successful child has no failure site; a stopped child names only the fixed
check, never its private error. A later settled database snapshot does not
convert an earlier STOP into a pass. Fresh acceptance identifiers and all four
native cases remain required after a harness change.

A duplicate typed coordinator acknowledges its exact current settled schedule,
including a failed terminal outcome, without reactivating work. It rechecks
source, intent, live-root and current-pointer authority and validates the full
schedule. This acknowledgement does not turn a failed execution into a success
or authorize publication; generic, superseded, noncurrent and malformed
schedules still refuse.

The neutral production-helper acceptance suite passed success, cancellation,
shared absolute-wall expiry and hard-death recovery with fresh preparation27
and profile epoch5. Source-free original receipts are retained in
`spike/t454/neutral_acceptance_7.json`; all four cases require native absence,
owned workspace drainage, engine join, growth release and no replay. This
neutral acceptance proof does not enable or register a provider.

The executor, scheduler/lifecycle and generated-navigation adapter now have an
internal runtime composition. Ordinary `serve` keeps it disabled; there is no
new configuration switch or managed-indexing endpoint. Neutral native canary
and dry-run have passed without publication and with exact reuse and joined
cleanup; their source-free originals live at `spike/t454/neutral_checked_8.json`.
Fresh frozen target-corpus correctness/cost validation still gates Bazel
registration. Additional managed providers follow that closure.

The shared worker-output limit remains16 MiB. A successful result that exceeds
the remaining output allowance can use a bounded lossless gzip envelope;
fitting results and failure frames retain their original format. Both the
physical shared output and each decoded result stay bounded. Compression does
not change the shared wall limit or bypass plan, source, tool, caller or SCIP
validation.

T45.7 adds internal Go module/`go.work` and existing-artifact inputs behind the
same managed publication contract. Ordinary startup still installs no managed
provider: there is no new configuration switch or functioning generation control.
Bazel remains first for detected/configured Bazel repositories. The2026-10-03
sequencing waiver permits this work before the remaining Bazel corpus gate; it
does not turn that gate into a pass or enable any provider.

The module input requires an explicit `go.mod` or `go.work`, at most four exact
module roots and512 explicit packages, pinned Go1.25.0/scip-go0.2.7, and offline
immutable inputs. Recursive/wildcard selection, replacement directives, ambient
Go flags and dependency downloads are refused. Package files and internal imports
must match the sealed plan. Existing-artifact import copies authenticated SCIP
bytes into managed immutable custody, checks declared producer and source commit,
and requires exact document coverage and explicit root mappings when normalizing
paths. Global definitions must match the source commit even when symbol metadata
is absent; dependency references may retain external versions. A declared producer
is provenance, not proof that its executable ran. Both inputs retain raw artifact
hashes in the attempt receipt and use the existing publication, canary/dry-run,
restart and exact-reuse contracts. No generic command provider is supported.

For the pinned module tool, sibling workspace references using local version `.`
are normalized only when an admitted module's exact commit-bound definition is
present in the returned members. Missing definitions refuse publication. Import
artifacts and external dependency versions are not rewritten by this adapter.

Local neutral single-module, two-module workspace and artifact-import rehearsals
pass publish, canary/dry-run, exact lease reuse and restart checks. Their original
binary identities and scoped cleanup are recorded in
`spike/t457/native_inputs_1.json`; these results do not enable a provider or
establish a target-corpus performance envelope.

Module and import profiles target Linux `arm64` or `amd64`. The architecture is
sealed in the profile configuration and must match the worker host; module
commands use `GOARM64=v8.0` or `GOAMD64=v1` accordingly. Each architecture has
its own pinned Go 1.25.0 and scip-go 0.2.7 images, recorded in
`spike/t457/native_tool_pins_amd64.json`, and a profile carrying the other
architecture's tools is refused. Bazel tool pins are also per architecture, but
its C toolchain sysroot is sealed per architecture. `amd64` uses the host
gcc-13 archive recorded in `spike/t457/amd64_sysroot_1.json`. A C compile
through that wrapper passed on this x86_64 host. The admitted tool inventory
is `spike/t457/amd64_tool_inventory.json`. Single, workspace, and import
native rehearsals passed on this x86_64 host; the receipt is
`spike/t457/amd64_native_rehearsal_1.json`. The arm64-locked Bazel cohort
stops before launch; the receipt is `spike/t457/amd64_bazel_rehearsal_1.json`.
A separate `native-linux-amd64-rules-go-059-v1` profile admits the measured
amd64 pins. Its receipt is `spike/t457/amd64_native_profile_1.json`. The
sealed sysroot can be reassembled from the parts in
`spike/t457/amd64_sysroot_parts.json`. Offline rules_cc admission stopped
because those releases still require Bazel Central Registry modules; the
receipt is `spike/t457/amd64_bazel_next_1.json`. Vendoring the resolved
closure stopped at a 112,257,936-byte repository cache; the receipt is
`spike/t457/amd64_rules_cc_vendor_1.json`. The sealed offline set for the
amd64 profile is rules_cc 0.2.14 with protobuf 33.4 at
`/var/lib/phebs-typed-sysroot/rules-cc-0.2.14`. Its receipt is
`spike/t457/amd64_rules_cc_offline_1.json`. The frozen 249,496-byte public
archive was absent at the earlier search, when `RunNativeCompatibility`
required a Linux arm64 worker with uid 65534. That search receipt is
`spike/t457/amd64_public_archive_stop_1.json`. The same archive is now
placed at `tools/corpus/remote-apis-sdks.tar.gz` from an external host
attachment. Its receipt is `spike/t457/amd64_public_archive_found_1.json`.
At placement, `RunNativeCompatibility` required a Linux arm64 worker with
uid 65534; this host did not match, and that receipt is
`spike/t457/amd64_native_compat_worker_stop_1.json`. The function now
admits Linux arm64 and amd64 workers with uid 65534. This host's ordinary
uid 1000 does not match that sandbox uid. That receipt is
`spike/t457/amd64_native_compat_arch_1.json`. A uid 65534 invocation
on this host then stopped because `/scratch/workspace/lib/lib.go` is
absent. That receipt is `spike/t457/amd64_native_compat_uid_1.json`.
Compatibility plan, source and SDK checks now admit both Linux modes and
require the selected plan architecture to match the worker. Typed responses
carry that selected architecture; this validation does not establish a
successful cohort run.

## Code navigation indexing Settings (T45.8b)

Administrators can open **Settings → Code navigation indexing**, select a
repository and read its exact indexed HEAD. File's unavailable precise
navigation panel links administrators directly to that repository's Settings
section. Ordinary users see the unavailable navigation message without an
indexing action.

Providers appear Bazel first, followed by Go module/workspace and existing
artifact. Availability requires an installed managed runtime and that
repository's admitted profile; the ordinary build still has neither a managed
runtime switch nor provider registration. Its cards remain **Unavailable**.
These endpoints supersede the earlier T45.4 statement that no managed-indexing
endpoint exists; they do not change that disabled startup boundary.

When available, target, configuration and resource profiles are server-owned
names shown read-only. Select **Generate navigation**, **Canary** or **Dry run**,
then **Review indexing plan** to review the exact commit and request authority.
This is a request preview; native package planning starts after the separate
explicit enqueue action. Canary and dry run validate without publication.
Only a complete successful generation replaces current precise navigation.
There are no raw command, flag, path, environment, credential or resource
controls.

The status surface distinguishes absent, current, stale, planning, indexing,
validating, publishing, failed and canceled. Active work refreshes the selected
repository every five seconds, including queued work after coordinator completion.
A read failure retains the last confirmed state and continues automatic checks
while that state is active; refresh remains available. Early queued-work failure
is terminal. Missing collected job history remains unavailable. A
stale/restored source never displays old attempt progress as work on new HEAD.
An unconfirmed enqueue offers **Retry exact request**, reusing its original
request digest and idempotency key. Source/profile changes reject that request;
refresh and review a new request before enqueueing again. Raw worker output and
private paths are withheld.

A failed, canceled, completed or superseded purpose can run again on the same
commit and profile. **Review indexing plan** previews the next run of that
purpose: a new request digest that starts a fresh attempt once you enqueue it,
while earlier runs keep their records. While a request of the same purpose is
still planning, indexing, validating or publishing, review shows that request
instead, so a second click never starts a duplicate. **Retry exact request**
after an unconfirmed network response still resends the original digest and
never starts another run. The existing limit of 64 retained requests per
repository still applies; when it is full, a new run is refused.

The administrator HTTP contract is under `/api/code-navigation-indexing`:
`GET /providers`, `GET /status?repository=…`, `POST /plan` and `POST /enqueue`.
Both POSTs use the existing authenticated CSRF boundary and audit target.
Enqueue binds the preview's expected revision, request digest and idempotency
key to server-owned exact source/profile/universe authority. A preview does not
queue work. Status reads do not initialize source identity or inspect native
files. The T45.6 remaining Bazel corpus gate stays waived, not passed; this
workflow establishes no supported target scale or release posture.
