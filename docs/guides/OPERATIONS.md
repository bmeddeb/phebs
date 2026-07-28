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
└── index/*.zoekt          # search shards
```

Mirrors, shards, repo rows, and jobs are rebuildable from config and upstream
Git. **Authentication state is not derived:** `$DATA/db` now contains users,
OIDC links, API-key hashes, and sessions (see *Backup & restore*). Deleting
the whole data directory is an intentional auth reset as well as a reindex;
the next start requires first-user enrollment.

### Backup & restore

Precious state is `$DATA/db` plus the exact config file — the users, OIDC
links, API-key hashes, sessions, permission edges, audit/analytics history,
evidence, and proof pins that cannot be rebuilt (repo rows and job state ride
along but are derivable). Everything else under `$DATA` is derived.

For an online backup, keep the local phebs server running and use the same
phebs executable/configuration generation as that server:

```sh
phebs backup -config /etc/phebs/phebs.yaml -output /restricted/phebs-backup-20260722
```

The output path must not exist. The command discovers only the supervised
loopback SurrealDB child through `$DATA/.surreal-runtime.json`, verifies the
exact child executable and the raw-config digest that started the live server,
and runs that executable's live `export`. A different config that only points
at the same `$DATA` is refused. The command publishes a private directory
containing `database.surql` and `manifest.json`. The manifest binds the export's
size and SHA-256 digest, the exact raw config
digest, phebs version/binary digest, SurrealDB version/binary digest, database
identity, store-writer/evidence/migration versions, and the derived-state
exclusions. It contains no host binary path or database password. Preserve the
exact config separately; the backup contains its digest, not its bytes.

The export is unencrypted credential-bearing state. Move or encrypt the whole
directory only under the approved retention and key-custody procedure. Do not
edit either file: restore rejects extra, missing, renamed, symlinked, special,
oversized, or digest-mismatched entries.

Restore uses the manifest-bound phebs, SurrealDB, and config identities and an
absent or completely empty configured `$DATA`:

```sh
phebs restore -config /etc/phebs/phebs.yaml -backup /restricted/phebs-backup-20260722
phebs serve   -config /etc/phebs/phebs.yaml
```

Recovery config validation deliberately leaves `${SECRET}` references
unexpanded, so verification/import can happen in an isolated environment
without live source or OIDC credentials. Restore verifies the complete backup
and every compatible binary/store identity before it creates `$DATA`, imports
through an isolated SurrealDB child, and opens the store once to apply and
validate the supported schema/migrations. If import begins and then fails, the
partial target is retained and every later restore refuses it; quarantine or
remove it under the witnessed recovery procedure rather than retrying over it.

The subsequent normal `serve` start automatically rebuilds derived state:
startup reconciliation clears any indexed revision whose excluded shard is
missing, boot sync re-clones missing mirrors, and the queued index worker
rebuilds shards without an operator reindex request. Restored API keys and
sessions remain live — rotate them if the backup's custody was ever in doubt.

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

Sync, fetch, index, and extraction work runs through queues in SurrealDB,
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

When enabled, every successful index schedules a bounded read of declared
protobuf contracts for that repository. The worker binds the read to the
latest indexed full commit. Services, messages, RPCs, and numbered fields
become `DECLARES_SERVICE`, `DECLARES_MESSAGE`, `DECLARES_OPERATION`, and
`DECLARES_FIELD` assertions backed by content-keyed evidence atoms bound to
the repository, commit, path, digest, byte span, and line span. RPC details
retain raw request/response type names and client/server streaming flags;
field details retain scalar or named type, cardinality, map key/value shape,
and oneof membership. Empty services and messages are included.

Type links are intentionally file-local. Protobuf lexical lookup records a
same-file message or enum only when exactly one declaration proves the link.
Unlinked import context, missing names, duplicate declarations, and invalid
declaration kinds remain unresolved under separate reason codes. Import
context is sorted, digest-bound, and explicitly truncated after 64 paths or
4 KiB; unresolved names are never labeled external. Recursive declarations
record links but are not expanded by the extractor. A trusted inventory still
requires every `.proto` candidate to be read. Extraction runs publish
atomically: a read, parse, provenance, limit, cancellation, or publication
failure leaves the prior published facts intact.

A separate `experimental.provisional_thrift_extraction` opt-in enables the
T19.2 Thrift declaration reader (`thrift-contract` 1.0.0). Every successful
index then also schedules a bounded read of `.thrift` IDL files. Services,
functions, and struct/union/exception shapes become the same
`DECLARES_SERVICE`, `DECLARES_OPERATION`, `DECLARES_MESSAGE`, and
`DECLARES_FIELD` assertion families under `thrift-*` detail schemas. Operation
identity is `scope.Service/method`, where scope is the last segment of an
explicit `namespace go`, then `namespace *` (Thrift applies it to every target
language), then the file basename. Request and response
shapes are modeled wire-honestly as the implicit argument and result structs
Thrift serializes: synthetic same-file messages whose field `0` is the success
slot and whose `throws` clauses are result fields; `oneway` functions declare
no result struct. Type links are file-local exactly as for protobuf, with
same-file typedef chains chased to a bounded depth and include-qualified names
remaining unresolved with sorted, digest-bound include context. Fields with
implicit identifiers fail closed as one structured `THRIFT_EXTRACTION_GAP`
per file — Thrift assigns negative wire identifiers to such fields, and the
reader never fabricates identity. Locators cite the declaration-start line
with exact byte spans. Buf-based wire-compatibility checking remains
protobuf-only; no Thrift compatibility engine exists.

The same Thrift opt-in also enables the T19.3 Go consumer reader
(`thrift-consumer` 1.1.0). It recognizes the repository's own Apache Thrift
generated Go by compiler header (both the modern and legacy marker forms),
recovers each service's wire method universe from `processorMap` key literals,
and binds each wire name to the exact generated Go method whose client
`Call` expression contains that literal. It does not reproduce Apache's
version- and option-sensitive publicizing rules. It then scans non-generated
Go files: `New<Service>Processor` call sites become
`REGISTERS_THRIFT_SERVICE` assertions (tier `derived`), and selector calls
whose generated method name is unique across the repository's stub index
become `CALLS_OPERATION` assertions for `/scope.Service/method` (tier
`heuristic`). Ambiguous call names abstain as one
`UNRESOLVED_THRIFT_CALL` candidate per canonical `/scope.Service/wire-method`;
ambiguous constructors use `UNRESOLVED_THRIFT_REGISTRATION`. Oversized or
unparseable files record
`THRIFT_EXTRACTION_GAP`. A repository that imports generated stubs from
another module instead of vendoring them yields no consumer evidence — an
honest abstention, not an error. Client construction is not evidence, and
consumer lineage remains file-scoped provisional, so joins against
declarations stay name-bound exactly as for gRPC.

A separate `experimental.provisional_kafka_extraction` opt-in enables the
T23.2 Kafka topic-evidence packs: `kafka-producer` and `kafka-consumer`
(both 1.1.0), two planes sharing one recognizer validated by the T23.1
spike. The readers scan non-test Go files that import
`github.com/Shopify/sarama`, `github.com/IBM/sarama`, or
`github.com/segmentio/kafka-go` (qualified selectors only — dot-imports
carry no in-file library proof and are refused; a file importing both
sarama eras abstains rather than guessing). Recognized shapes:
`sarama.ProducerMessage{Topic:}`, segmentio `Writer`/`WriterConfig`
composites, `kafka.Message{Topic:}` passed directly to `WriteMessages`
(never `CommitMessages`), `ReaderConfig` `Topic`/`GroupTopics`, and the
receiver-untyped `Consume`/`ConsumePartition` call shapes (tier
`heuristic`; composites are `derived`). A topic binds only when it is a
string literal or a same-file `const` satisfying Kafka's own naming bounds
(1–249 characters of `[a-zA-Z0-9._-]`, excluding `.`/`..`); the object is
`topic:<literal>` and carries no cluster, environment, runtime, or
completeness claim. The constant may be package- or function-local, but must
be an explicit string literal declaration lexically resolved within that
file; vars, expressions, and cross-file names still abstain. Consumer group
ids are recorded as detail, never
identity. Everything else — configuration selectors, function results,
variables, invalid literals — emits an `UNRESOLVED_KAFKA_PRODUCER` /
`UNRESOLVED_KAFKA_CONSUMER` assertion whose object names the frozen shape
class. **Expect abstention to dominate**: production Kafka topics are
overwhelmingly configuration-driven (2 literal evidence rows vs 19
abstentions across the spike's pinned corpora), and the pack
presents that volume as the honest norm rather than a defect. `_test.go`
fixture literals are excluded from recognition entirely. Oversized or
unparseable files record `KAFKA_EXTRACTION_GAP`. There is no topic
declarations plane in round one — no in-code topic declaration exists —
so topics appear only through their producers and consumers, with no
catalog or Atlas surface.

The proto opt-in also enables the T13.1 Go/gRPC consumer reader (dark scope,
2026-07-22 disposition). It indexes the repository's own generated
`*_grpc.pb.go` stubs, then emits `REGISTERS_GRPC_SERVICE` assertions for
`Register<Service>Server` call sites (tier `derived` — name-bound to a
same-repo stub) and `CALLS_OPERATION` assertions for client method calls
whose name matches exactly one indexed service (tier `heuristic`). Package-less
protobuf service names such as `Greeter` are valid and indexed. Ambiguous
method names, generated registration-helper collisions, and duplicate service
FQNs anchored by different repository paths are not guessed: each source
occurrence emits an exact-span `tier=unresolved` diagnostic assertion, while
coverage counts the distinct semantic gaps those atoms support. Unparseable or
over-limit non-empty Go candidates likewise emit source-backed unresolved gaps,
so successful abstention remains publishable through the trusted worker.
Every assertion carries a `code_role`
(production/test/mock/generated/vendor, vendor > mock > generated > test >
production precedence) and cites its atom's exact byte and line span.
Resolution is syntactic — there is no type checking — so these facts carry
reduced fidelity by design and, like all provisional facts, state no
measured accuracy and must not drive compatibility, migration, or
negative-proof conclusions.

T20.8 adds declaration-proven typed Go caller evidence without changing that
legacy reader or its proof results. Under the same protocol flags, the current
`grpc-caller` and `thrift-caller` 1.2.0 domains read a committed root
`index.scip`; phebs still never creates or downloads that index. Each source
call must carry the exact SCIP symbol of a checked-in generated client method.
For gRPC, the generated definition must also agree with one `// source:`
marker, one full-method literal, and the service descriptor name before the
generator-relative `.proto` path may select one immutable T20.7
generated-from mapping. For Apache Thrift, the generated client method must
carry an admitted compiler header and exactly one client `Call` wire literal,
then select one direct immutable generated-from mapping. Only that complete
chain emits a tier-`derived` `CALLS_OPERATION` row whose lineage is the
declaration evidence identity.

Missing or conflicting mappings emit source-granular, operation-keyed
`UNRESOLVED_CALLER` rows instead of guesses. Malformed SCIP or a document path
absent from the repository produces a bounded `CALLER_EXTRACTION_GAP` in only
the affected protocol domain; an absent or zero-byte index is reported as
unavailable and emits no typed callers. SCIP documents normally do not embed a
source-content digest, so a committed same-path index is not proof that its
ranges describe the current file bytes; regenerate and commit `index.scip`
whenever source changes. Each row snapshots the
immutable unit-attribution state and attribution digest used at extraction
time, including unattributed and ambiguous results, so later pages never
silently reclassify old evidence. T20.10's authenticated Caller Map is their
first read surface. They remain provisional and dark, and establish neither
caller completeness nor measured accuracy.

When a usable typed occurrence is absent, version 1.2.0 may use the bounded
package-aware fallback. It requires a valid repository-root `go.mod`, an
explicit import of one indexed generated package, and one of five local
provenance shapes: imported client parameter, imported type alias, generated
constructor assignment, named client field, or embedded client. Such rows use
`resolution=syntax` and tier `heuristic`. A SCIP occurrence always wins and is
never duplicated. Dynamic/interface flows, dot imports, shadowing whose new
value has no admitted client provenance, or multiple candidate clients remain
operation-keyed `UNRESOLVED_CALLER` rows; the reader does not
perform general assignment propagation, reflection, type checking, builds, or
module resolution. The fallback can still operate when SCIP is absent or
malformed, but that independent coverage gap remains visible.
Generated and caller Go documents above the reader's 4 MiB per-file bound, or
with invalid UTF-8, are outside this v1 reader; the coverage manifest still
binds the trusted corpus/candidate/read scope, but no caller row or per-file
gap row is claimed for those documents.

The opt-in also reads a repository-root, committed `index.scip` to emit T13.2
`REFERENCES_PROTO_FIELD` assertions. phebs never runs or downloads a SCIP
indexer: the fixed root index must be a regular blob in the same immutable
commit. Nested indexes, symlinks, and shard manifests are not selected inputs.
A SCIP symbol is
eligible only when its exact definition range matches a generated protobuf Go
struct field or getter, the generated struct tag supplies the field number and
proto name, and the generated file's `// source:` declaration maps uniquely to
the committed `.proto` field. Each non-definition reference cites the exact
identifier span in its source document. Positions use a document's declared
encoding when present and otherwise the index metadata's UTF-8/UTF-16
encoding; neither declared fails closed. Missing indexes produce an empty,
explicitly unavailable result; local symbols, malformed ranges, missing source
declarations, and ambiguous symbol/field joins abstain rather than guessing.

Field identity is canonical across consumer dependency versions:
`(contract_lineage_id, message_full_name, field_number)`. The lineage digest
uses the global SCIP scheme, package manager, and package name, but excludes
the dependency version and generated field/getter name. A field rename that
keeps its protobuf number and message therefore remains one identity, while
its current name and dependency version remain in assertion detail. The
classification is derived from SCIP role bits with precedence `write > read >
test > generated > unknown`; `code_role` separately records repository
placement and SCIP test/generated roles. These are direct field references,
not claims that a response field was semantically read.

The independent `experimental.provisional_thrift_field_extraction` opt-in
registers the T22 `scip-thrift-field` reader. For thriftrw, an eligible
generated file must embed a
`thriftreflect.ThriftModule` whose `SHA1` matches its `Raw` IDL bytes; the Raw
IDL field identity, generated struct-field order, and `wire.Field{ID: ...}`
literals must agree before an exact SCIP definition can bind references.
These rows carry tier `exact` and `source_binding=module_digest`.

Apache Thrift compiler output is eligible only when the file starts with one
of the validated complete generator-header comment forms and contains a valid
`thrift:"name,ID[,flags]"` struct tag. Scope is the generated Go package,
message is the enclosing generated struct, and the exact tagged identifier
must be the SCIP definition under that same enclosing type. Because Apache
output embeds no IDL bytes, digest, or source path, these rows remain tier
`derived` with `source_binding=none`; phebs does not promote them by consulting
another repository.

Both families use objects `scope.Message#field-number`, including field `0`,
and the same package-based `contract_scip_package_v1` lineage family as
protobuf without claiming equality to the declaration pack's provisional
lineage. Duplicate definitions or duplicate `(message, field ID)` identities
abstain. Malformed generator candidates abort the staged run.
A missing root index publishes explicit `scip-index-absent` coverage; a
malformed index aborts the staged run.

T22.1 measured the generated-file ceiling but not the inherited protobuf
index-scale limits, so this pack deliberately admits at most a 32 MiB root
index, 50,000 documents, 500,000 occurrences, 8 KiB symbols, and 4 MiB per
generated candidate. It reads only committed repository blobs and never runs
or downloads an indexer. The pack is experimental-dark and carries no
accuracy, completeness, runtime-use, or absence claim. The existing
`find_proto_field_references` route remains protobuf-only and byte-stable.
The separate `find_field_references` route, impact report, and MCP tool fan
out across the registered field-reference domains whose number rules admit
the requested identity. Impact field mode uses the same neutral report:
selecting a protocol applies only that protocol's field-number validation and
does not filter an otherwise admitted domain from the answer.

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

The run is bounded to 200,000 regular inventory paths and 16 MiB of aggregate
path text, 10 MiB per source blob, a separate 64 MiB ceiling for the fixed
root `index.scip`, 512 MiB of distinct reads, 5,000
emitted facts, and a cooperative 15-minute context deadline. A candidate
Go parser input is further limited to 4 MiB; a protobuf parser input is limited
to 4 MiB, 500,000 lexical tokens, and 128 structural levels. Neither in-process
parser can be preempted inside one parse call, so this is not yet a hard
CPU/memory/process isolation boundary. A candidate `.proto` symlink or more
than 100 placements of one content atom also prevents publication. Gitlinks
under
`gitlink-boundary-v1` are recorded as repository boundaries and are not
traversed. Unrelated symlinks are skipped. A
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
`phebs_jobs_total{kind="extraction_job"}`.

Proof-aware retention checks at startup and hourly while idle. Every
compatible-format run is limited to 25,000 stored association/assertion rows
and 20,000 evidence references. Individual staging transactions remain capped
at 10,000 rows of one kind; the extraction worker normally sends no more than
256 facts per transaction. Retention first locks one eligible aborted,
superseded, or 24-hour-stale staged run, rechecks that it is unpinned, and
marks it internally `deleting`. It then resumes from a durable phase while
deleting at most 512 associations or assertions per transaction. The run is
physically removed only after both row kinds are empty; an atom is removed
only after its last association anywhere has disappeared. Maintenance performs
at most 64 of these fixed-size steps, yields for five seconds when that cap is
reached, and returns to the hourly idle interval when drained. Logs report
completed logical runs separately from association, assertion, and atom rows.
Pinned proof/checkpoint runs and atoms still shared by another run are retained. Rows
migrated from the retracted, pre-bound evidence schema are hidden and
quarantined from automatic cleanup; an administrator must inspect and remove
that legacy data directly if desired. If two run records claim the same
logical run identity, all proof under that identity is likewise hidden,
unwritable, unpinnable, and exempt from automatic retention until an
administrator resolves the ambiguity.

The exact store-writer generation is separate from the stable evidence-format
version. Staged evidence writes and publication require the exact writer
generation, while compatible published reads, proof resolution, pinning, and
retention use the format version. A later compatible writer bump therefore
cannot strand an existing pinned proof bundle; an unknown format remains
hidden and untouched.

The current identities are writer `t12-store-v7`, readable evidence
`t12-evidence-v1`, and migration `t12-evidence-migration-v5`. Startup
idempotently upgrades the immediately preceding compatible v6 run generation
in place; readable evidence bytes and content identities do not change.
Staged-run reads and all mutations require v7. Compatible published
`t12-evidence-v1` runs, including pinned proof written by a future compatible
writer, remain readable through the stable format boundary.

Evidence migrations still require exclusive startup against the store.
Database and transaction guards now make a mixed-version or rollback writer
fail closed: known retired v1–v6 run writes are rejected, and every current
begin/stage/publish/abort/retention step requires the active v5 migration
marker. A generation-named synchronous database event survives a v6 binary reapplying
its weaker field definition and cancels its retired-generation transaction.
If an older opener changes the migration marker, current writes stop too; shut
down every writer and restart the v7 binary exclusively to restore it. Do not operate
rolling mixed writers against a remote endpoint. The supervised local
deployment already provides the intended single-writer lifecycle.

The v6 store also has an internal, exact reverse-evidence page used by the
planned Caller Map. It requires one authorized repository, published run,
predicate, and operation object; optionally fixes declaration lineage; returns
50 rows by default and at most 100; and uses an explicit continuation rather
than placing a hidden sentinel in the rendered rows. The query is bound to the
generation-specific compound index and fails if that index is unavailable.
No HTTP, MCP, or UI Caller Map surface is registered by T20.4.

### Investigation storage and guided execution foundation

On startup, the normal idempotent schema pass
creates the Investigation, Revision, Run, RunEvent, RunArtifact, Decision,
Disposition, BaselineDesignation, Watch, and WatchRevision tables and indexes.
T16.4 additionally creates the immutable guided-creation idempotency mapping
and the `investigation_run_job` queue table.

Revisions, Run requests, RunEvents, RunArtifacts, Decisions, Dispositions,
BaselineDesignations, and WatchRevisions are immutable at the store boundary.
Correcting one creates a new Revision/WatchRevision or an explicitly
superseding human record; the original remains readable. Investigation display
metadata/lifecycle/current-Revision and Watch owner/enablement/current-Revision/
expiry/cursor are the only checked mutable projections in this slice. Both
projections update by compare-and-swap against the previously read lifecycle
and current-revision pointer, so a concurrent stale writer fails closed with a
conflict instead of committing an invalid transition or clobbering the
pointer.

Run rows contain no status field. Guided submission atomically freezes the
active Investigation and first Revision, appends the initial `queued` event,
and creates one pending queue slot. Every later state is reconstructed from
the contiguous, append-only event stream:
`queued → enumerating → analyzing → publishing → published`, with `failed` or
`canceled` allowed only before a terminal state. Reusing the same idempotency
key for the exact same Revision request returns the existing Run; changing any
request input under that key fails closed. A failed or canceled RunArtifact
cannot carry published fact references.

Before submission, the guided preview lists only the caller's currently
visible repository snapshots, exact indexed commits, selected pack versions,
an authorization result, a repository-snapshot work estimate, and the
1,000-repository platform ceiling. A requested repository that is missing,
deleting, or unauthorized has the same `SCOPE_REPOSITORY_NOT_AVAILABLE`
blocker. The preview digest covers the normalized plan and resolved commits;
submission repeats the preflight and returns a conflict if anything changed.

Worker attempts use an opaque publication lease separate from the generic job
lease. A retry closes the prior lease and appends a new `queued` attempt; the
platform permits at most three attempts and a pack may choose fewer. Owners
may cancel a nonterminal Run. Cancellation closes the worker lease in the same
transaction as the terminal event and audit row, so a late worker cannot
publish.

RunArtifact publication, its terminal RunEvent, extraction-run evidence pins,
active-Investigation retention owner, and audit row are one database
transaction guarded by the exact attempt lease. Every pin uses the artifact-specific
`investigation-artifact:<artifact-id>` namespace; if any referenced extraction
run is missing, quarantined, ambiguous, incompatible, or not published or
superseded, neither the artifact nor any of its pins is written. The
publication transaction locks each referenced extraction run, so a concurrent
evidence sweep can never reclaim a run in the same instant an artifact pins
it. A Baseline
designation atomically acquires its corresponding artifact owner. Failed
attempts may atomically retain a reconciled
`investigation-coverage-ledger-v1` plus bounded diagnostics, but that path
rejects facts and evidence pins.

Retention owners are immutable caller-authorized claims. Ending one appends a
release; it never edits the claim, and that semantic owner key cannot silently
reacquire after release. Normal artifact collection locks the artifact and
rechecks that no unreleased Investigation, Baseline, or Dossier owner exists.
An audited revocation, mandatory-deletion, legal-policy, or approved-retention
override may supersede owners and allow immediate collection. Collection
removes only the RunArtifact and pins in its exact namespace, while preserving
owner/release/override audit rows. It never deletes extraction evidence; the
existing proof-aware evidence sweep may reclaim a superseded run only after
its final independent pin is gone. This remains an internal store facility:
lifecycle wiring, sharing surfaces, and public creation/read surfaces land in
later Epic 16 tickets.

Every investigation-domain read has a principal-scoped variant that
authorizes at query time. An object that does not exist and an object the
principal is not authorized to read produce the identical not-found result:
no counts, existence signals, scope, or integrity state are disclosed to an
unauthorized principal. Owners may grant and revoke `reader` access; grants
are re-checked on every read, and a grantee cannot delegate. Watches are
personal and never extend through grants. Ownership changes only through the
audited transfer operation — a plain update rejects owner edits — and a
transfer immediately voids each principal's stored continuation cursor for
that investigation without deleting any other state; re-authorized principals
simply re-establish their cursors. Each read binds the current ownership
revision and reader-grant generation, then rechecks that exact epoch after
integrity reconstruction; a concurrent transfer, revoke, or revoke/regrant
therefore cannot return stale data or surface a newly unauthorized integrity
error. Regranting creates a new generation and never resurrects the revoked
grant's cursor. Promoting a reader to owner consumes the reader grant, so it
cannot silently restore access after a later transfer. Grant, revoke, transfer,
and cursor mutation serialize against each other and commit with their audit
event in one transaction. The canonical `NOT_AVAILABLE` refusal
envelope (fixture 06) is rendered server-side as a minimal fixed shape whose
bytes are identical for unknown and unauthorized requests.

The T16.4 HTTP adapter defines preview, create, Run-status, and cancel
operations, but the production binary does not register them yet. Registration
requires a non-nil Investigation workflow store, which stays absent until an
exact released evidence-pack executor can drain the queue. Consequently these
post-gate implementation routes are absent from the live OpenAPI document and
return 404 rather than exposing a workflow that cannot complete.

T16.5 adds the read-only core-view surface behind a separate, narrow,
principal-scoped source. When a source is bound, authenticated clients receive
the `investigation-core-views` capability and may read:

- `GET /api/investigation_views` for authorized view summaries; and
- `GET /api/investigation_views/{id}` for one already-authorized envelope.

The UI then exposes `#/investigations`. Overview shows four summary regions
(evidenced, unknown, changed, and action), followed by the server-derived
bounded-absence eligibility result and blocker codes. The eligibility region
is deliberately read-only: there is no control or request path that can set
eligibility. Census preserves supported facts while displaying service and
owner attribution as separate states. Coverage shows eligible/analyzed/
failed/partial/excluded units and all attribution hops. Evidence retains proof,
snapshot, occurrence, and verification-action identifiers.

Empty states are conclusions with different prerequisites, not cosmetic
variants of “no results.” A complete zero-finding response renders the
server's authoritative bounded-absence qualification; an incomplete response
says only that no supported facts were found among analyzed units and lists
processing gaps; unresolved attribution remains visible beside the supported
fact; and a pack refusal suppresses Census, Coverage, evidence counts, and all
other claim-bearing content.

The normal production binary binds no core-view source, so these routes,
capability, OpenAPI operations, and navigation entry remain absent. For local
demonstration only, `make dev` sets `PHEBS_INVESTIGATION_FIXTURES` to the five
canonical synthetic files in `docs/fixtures/investigations/`. Setting that
environment variable manually opts into the same development adapter. These
fixtures exercise presentation and conformance states; they are not published
evidence, a released pack executor, a valid accuracy gate, or authority for
external claims.

T16.6 makes this envelope a generated, reusable contract rather than a
fixture-only UI shape. `internal/investigation` owns typed cross-field
validation; `go generate ./internal/mcp` regenerates the nine checked-in
schemas; and MCP advertises those exact schemas. Structural validation is not
the whole contract: the server also enforces coverage reconciliation, proof
references for evidenced facts, bounded-absence prerequisites,
non-comparability behavior, minimal `NOT_AVAILABLE` disclosure, and
irreversible truncation semantics.

T16.7 retains a principal-scoped first/last-seen consumer ledger independently
of sweepable RunArtifacts. Each published consumer census freezes the
authorized visibility projection, declared repository/build-target universe,
enumeration method, claim and fact identities, pack/rule/extractor/adapter
versions, build and snapshot semantics, and input completeness/freshness.
Two censuses produce an ordinary delta only when every frozen dimension is
compatible. A visibility, scope, identity, rule, enumeration, build,
external-input, failure, or freshness change instead yields a comparison
report with per-side coverage only; missing facts are never presented as
removals in that state.

Under fully comparable semantics, additions and removals require a positively
traced relationship occurrence. Removed relationships remain as inactive
ledger tombstones with their first/last-seen coordinates, so later compatible
evidence is classified as a reintroduction. Authorization is checked at every
snapshot and ledger read, and each principal has an independent projection.
Sweeping the RunArtifact that originally supplied an edge does not erase its
ledger history; the retained row is history and identity metadata, not a
replacement for the artifact's independently governed proof.

T16.8 derives ReviewItems from those authorized snapshots; there is no
hand-creation operation. A versioned pack projection can emit only three
queues: traced new/reintroduced consumers, coverage or comparability
regressions, and unresolved attribution tied to a fact that exists in the
source snapshot. The deterministic item identity binds the principal,
Investigation, comparison, projection version, logical subject, delta and
cause, evidence reference, and relevant human-record-state digest. Re-running
the same projection therefore returns the same IDs, independent of when it is
requested.

A later source sequence supersedes the preceding projection, while the
projection's lifecycle rule expires items relative to the immutable snapshot
publication time. The resulting states are only `open`, `superseded`, and
`expired`. Acknowledgement and the last-viewed comparison live in the existing
per-principal authorization-epoch cursor; they do not edit ReviewItems, and an
ownership transfer or grant revocation voids stale cursor state under the same
rules as every other Investigation read. Review materialization and listing
remain internal store facilities in this ticket; no production API or UI route
is enabled.

T16.9 defines the immutable `phebs-investigation-dossier-v1` export. Each
object and embedded finding is canonical JSON with its own domain-separated
SHA-256 digest; the canonical manifest binds those entries, recipient scope,
authorized locators for source material that is not embedded, supported
claims, blockers, eligibility, freshness/validation state, review/expiry rule,
and any predecessor. A second domain-separated digest roots the manifest and
entry list, and the service signs that root with Ed25519 plus a named
verification key. Creating the Dossier and its primary-RunArtifact retention
owner is one store transaction.

Export always calls the current recipient-scope resolver. It intersects the
principal-scoped consumer snapshot with that result, omits hidden units and
facts, and also omits legacy facts that lack a unit identity rather than
guessing their scope. The snapshot and input manifests and eligibility in the
export are recipient projections, not copied creator-wide claims. The service
rechecks both the Investigation authorization epoch and the resolved unit
scope after sealing and persistence before it returns any bytes.

Verify an exported canonical file without a running phebs service:

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
Opening the file through phebs is a separate operation: it verifies the sealed
bytes, then reauthorizes every included Investigation object, current unit,
and fact. Revocation therefore blocks reopen without making an already
exported offline file cryptographically unverifiable. This ticket exposes the
service/store and offline format only; production API/UI export registration
still requires an explicit key configuration and route decision.

### Metrics


| Metric                         | Type      | Labels                                                             |
| ------------------------------ | --------- | ------------------------------------------------------------------ |
| `phebs_jobs_total`             | counter   | `kind`, `result` (`done`/`failed`/`requeued`/`released`/`reaped`)  |
| `phebs_job_errors_total`       | counter   | `kind`, `class` (`auth`/`oom`/`corrupt-shard`/`extract`/`generic`) |
| `phebs_index_duration_seconds` | histogram | —                                                                  |
| `phebs_index_shard_bytes`      | gauge     | —                                                                  |


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
| log: contract compatibility disabled                              | Buf is missing/mismatched, or the OS sandbox cannot be enforced                                        | use `make build` or set `PHEBS_BUF` to the pinned v1.72.0 binary; install `bubblewrap` on Linux        |
| `listen tcp 127.0.0.1:3070: bind: address already in use`         | another phebs (or process) on the port                                                                 | stop it, or `-addr 127.0.0.1:3071`                                                                    |
| UI shows first-run setup                                          | no users and no OIDC provider                                                                          | copy the ephemeral setup token from the current process log; restarting generates a new token         |
| login succeeds but the UI immediately asks again                  | a `Secure` cookie was used over plain non-loopback HTTP                                                | serve HTTPS, or set `auth.cookie_secure: false` only for deliberate local development                 |
| API or MCP answers `401`                                          | no valid session/key, or a key was revoked/removed                                                     | create a named key in Settings and send `Authorization: Bearer <token>`                               |
| startup fails during OIDC discovery                               | issuer unavailable, wrong URL/private CA, or incomplete provider config                                | verify HTTPS reachability and discovery metadata; loopback HTTP is test-only                          |
| OIDC login says verified email is required                        | provider omitted `email_verified=true`                                                                 | configure the provider's email scope/claim mapping; phebs does not accept unverified email identities |
| code navigation says unavailable                                  | the indexed commit has no root `index.scip`                                                            | generate and commit a SCIP index, then sync/reindex that commit                                       |
| code-navigation/history link returns 404 after a repo update      | requested immutable commit is no longer present in the mirror or repo is unindexed/deleting            | use the current indexed commit from Repos, or restore/fetch the referenced object                     |
| GitHub sync reports a rate-limit wait                             | host requested a reset delay; phebs waits at most 1 minute and retries once, then uses the job backoff | use a PAT/App or reduce listing frequency                                                             |
| watch mode "doesn't see my edits"                                 | uncommitted changes are never indexed                                                                   | commit (or amend); the watcher reacts to HEAD and admitted-ref moves                                  |
| a repo temporarily disappears from search during repair           | its shard revision did not match committed DB state                                                    | wait for the forced index job; serving is intentionally fail-closed                                   |
| repo tagged `orphaned`                                            | no connection claims it anymore                                                                        | re-add the connection, or enable `sync.cleanup_orphans`                                               |
| sync fails with `auth: git …` and retries slowly                  | credential failure, classified `auth` (10 m backoff)                                                   | fix the token; reindex/restart to retry immediately                                                   |
| startup rejects a clone URL containing credentials/query data     | URL secrets are no longer persisted                                                                    | move HTTP credentials to `http_auth`; keep `url` credential-free                                      |




## Developing phebs


| Target               | Does                                                                                                                                                    |
| -------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `make dev`           | build UI + pinned zoekt/Buf children, bind synthetic Investigation/Contract Atlas fixtures, the retained neutral Change Workbench closure repo, the fixture-coupled Workbench, and the committed Thrift field-zero repo through normal sync/index/extraction; run with embedded UI |
| `make dev-api`       | backend-only loop with the same children, explicit UI/Workbench fixtures, and Thrift field-zero repository (placeholder UI page, fast)                                           |
| `make build`         | version-stamped `./phebs` plus same-module `bin/zoekt-git-index` and `bin/buf`; pass `VERSION=vX.Y.Z` for a release                                    |
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

The hosted release gate runs four independently visible local-equivalent
targets: `make ci-static`, `make ci-go`, `make ci-race`, and `make ci-ui`.
`make ci` runs all four sequentially. The live Go targets require the exact
pinned `surreal` on `PATH`; hosted CI downloads the exact 3.2.0 Linux archive
and verifies its committed SHA-256 before any store test starts.

The canonical Change Workbench vocabulary is
`internal/glossary/glossary.json`. Run `go generate ./internal/glossary` after
an approved source change; do not edit its generated Go, TypeScript, schema,
MCP, or marked MANUAL projection directly. `make verify-glossary` is
network-free and is also part of the ordinary test, lint, and static CI gates.
The generated TypeScript and MCP description inputs are contracts for later
tickets; T21.4 does not render help or register tools.


Live UI development: run `make dev-api`, then `cd ui && npm run dev` — Vite
serves on :5173 and proxies `/api` to :3070.

phebs is an independent, reference-only reimplementation inspired by
[Sourcebot](https://github.com/sourcebot-dev/sourcebot) — no upstream code is
used. phebs is licensed Apache-2.0.
