# Product workflows

[← User guide](../MANUAL.md)

This guide covers public-corpus evaluations, the neutral focused-service demo,
search, repository browsing, SCIP and Git history, HTTP, and MCP.
Experimental evidence remains subject to the
explicit coverage and validation caveats in each workflow.

## OpenTelemetry microservices evaluation

The repository and release bundle include `phebs-otel-demo.yaml` as the
canonical public microservices evaluation. From the release directory:

```bash
./phebs serve -config phebs-otel-demo.yaml
```

Open `http://127.0.0.1:3071`, complete first-administrator setup with the
one-time token printed in the server log, and allow the initial sync and index
to finish. The configuration clones the public
`github.com/open-telemetry/opentelemetry-demo` monorepo and keeps its state
isolated under `~/.phebs-otel-demo`. It deliberately enables
`experimental.provisional_proto_extraction`, so the authenticated UI can
expose Contracts / Contract Atlas after eligible evidence publishes. This is
an evaluation posture: the extracted protobuf/gRPC relationships remain
provisional, default deployments remain dark, unresolved relationships are
abstentions rather than guesses, and no empty result establishes runtime
absence.

## Thrift protocol-pack evaluation

The repository includes `phebs-thrift-demo.yaml` as the Epic 19 Thrift
evaluation over the public Jaeger corpus. Run it without a synthetic Atlas
fixture, which would override real catalog evidence:

```bash
make ui bin/zoekt-git-index bin/phebs-focused-index bin/buf
PHEBS_ZOEKT_GIT_INDEX="$(pwd)/bin/zoekt-git-index" \
PHEBS_FOCUSED_INDEX="$(pwd)/bin/phebs-focused-index" \
PHEBS_BUF="$(pwd)/bin/buf" \
  go run -tags ui ./cmd/phebs serve -config phebs-thrift-demo.yaml
```

Open `http://127.0.0.1:3073`, complete first-administrator setup with the
one-time token printed in the server log, and allow sync, index, and
extraction to finish. State stays isolated under `~/.phebs-thrift-demo`. The
three connections exercise the pack's three postures deliberately:
`jaeger-idl` publishes `thrift-contract` declarations (open
`agent.Agent/emitBatch` in the Atlas for the `oneway` chip and the
wire-honest args/result shapes); the archived `jaeger-client-go` vendors
generated stubs and real call sites in one repository, so
`thrift-consumer` registrations and `/agent.Agent/emitBatch` caller evidence
join the declarations by name (never `proven` — provisional lineage);
and `jaeger` imports its stubs from another module, publishing an honest
empty consumer run. The protobuf packs are also enabled so the Atlas shows
both protocols side by side. All Contract Atlas caveats apply unchanged: no
empty result establishes runtime absence.

## Kafka topic-evidence evaluation

The repository includes `phebs-kafka-demo.yaml` as the Epic 23 Kafka
evaluation over two public corpora. Run it the same way (no Atlas fixture —
the Kafka packs have no catalog surface at all):

```bash
make ui bin/zoekt-git-index bin/phebs-focused-index bin/buf
PHEBS_ZOEKT_GIT_INDEX="$(pwd)/bin/zoekt-git-index" \
PHEBS_FOCUSED_INDEX="$(pwd)/bin/phebs-focused-index" \
PHEBS_BUF="$(pwd)/bin/buf" \
  go run -tags ui ./cmd/phebs serve -config phebs-kafka-demo.yaml
```

Open `http://127.0.0.1:3074`, complete first-administrator setup, and allow
sync, index, and extraction to finish (state isolates under
`~/.phebs-kafka-demo`). Then open **Topics** and query `important` or
`access_log`: the sarama corpus's `examples/http_server` carries those
topics as qualified source literals, so `PRODUCES_TO_TOPIC` evidence rows
appear with exact citations. The unresolved census renders **above** the
evidence on every answer — the kafka-go corpus's examples are entirely
environment-driven, so its recognized sites all abstain, and the census
counts their supporting source sites per shape class with zeros listed
explicitly. Producer and consumer publication state is reported separately,
so a run from one plane never makes zeros in the other plane look measured;
whole-file extraction gaps stay visible in the coverage certificate rather
than being folded into the site census. Query any other topic spelling to see
the honest empty answer: no rows, the same topic-independent census, and no
completeness claim anywhere. Topics have no catalog or Atlas surface by design
(T23.1 KD8): a topic exists on this page only through its producers and
consumers.

## Evaluating a separated IDL/source monorepo

The current extractor walks the repository's complete regular-blob inventory;
directories have no built-in semantic meaning. A layout with declarations
under `idl/` and handwritten Go under `src/` is therefore eligible without
moving files or making declarations adjacent to callers. Generated Go stubs
must be committed somewhere in that same pinned repository for the current
syntactic gRPC/Thrift consumer readers to index them. For protobuf field
references, the current committed-artifact workflow additionally requires a
SCIP index for that same immutable revision: repository-root `index.scip` in
whole-repository mode, or the exact configured supporting path in focused
mode. Current pure extractors and committed-index readers never run the
repository's build, code generator, plugin, or dependency downloader. The
separately bounded, administrator-authorized managed provider planned below is
the only proposed build-execution boundary; it is not shipped.

Epic 20's dark caller resolver can additionally consume three optional,
committed JSON snapshots from fixed repository-root paths:

- `layout-snapshot.json` version `t20-layout-snapshot-v1` classifies
  non-overlapping `idl`, `generated`, and `source` roots. IDL/generated roots
  name a protocol token; a source root does not.
- `unit-snapshot.json` version `t20-unit-snapshot-v1` maps one source path and
  optional exact `start_line` to separate arrays of `build_targets`,
  `deployables`, `logical_services`, and `owners`. Exact-line entries win over
  path-level entries.
- `generated-from-snapshot.json` version `t20-generated-from-v1` provides
  explicit `generated_path` → `declaration_path` mappings. Protobuf mappings
  may also bind a `generator_relative_path`, or an `invocations` entry may map
  a `generated_root` to a `generator_invocation_root`. Thrift requires direct
  mappings.

For example, the layout remains classification—not proof:

```json
{
  "version": "t20-layout-snapshot-v1",
  "roots": [
    {"kind": "idl", "path": "idl/proto", "protocol": "grpc"},
    {"kind": "generated", "path": "gen/proto", "protocol": "grpc"},
    {"kind": "source", "path": "src"}
  ]
}
```

The snapshots are ordinary repository blobs and inherit the 10 MiB per-file
and 512 MiB aggregate extraction budgets. Snapshot symlinks, unknown versions
or fields, unsafe/stale paths, dishonest state labels, and overlapping roots
fail closed. Referenced source/generated/declaration files must exist in the
same fully inventoried commit. Files outside every optional root remain in the
corpus inventory. A layout root alone never establishes generated-from:
protobuf still needs its agreeing generated markers plus an explicit direct or
invocation-root relation, and Thrift needs a direct relation.

One mapping is `resolved`; zero is `unavailable`; multiple mappings or a
multi-valued candidate are `ambiguous`. phebs never chooses a build target,
deployable, service, or owner. Evidence retains the complete candidate set;
each Caller Map row serializes at most 64 candidates plus the exact
pre-truncation `candidate_total`, so omitted display candidates remain explicit
rather than silently disappearing. The resolver catalog retains exact
repository/commit/path/blob-digest attribution; its manifest is part of the
complete caller-generation identity and therefore part of every Caller Map
cursor fence. No snapshot causes
phebs to run a build, generator, plugin, binary, or catalog client, and the
current adapter performs no external lookup.

The default-dark Caller Map read service is now available at
`GET /api/contract_callers`. It requires the complete declaration identity:
`protocol`, declaration `repository`, declaration `lineage`, and canonical
`operation`. Optional `unit`, `owner`, `path_prefix`, `code_role`, `tier`,
`freshness`, and `resolution` filters narrow the result; `ordering=source`
(default) or `ordering=unit` chooses the stable ordering. Pages default to 50
rows and accept at most 100. After authorizing the endpoint repository, the
service reads only that repository's exact complete `repository-overlay`
generation. It does not union callers from other visible repositories and no
longer reconstructs public caller rows from legacy caller assertions; the
declaration header is still resolved from the exact declaration run named by
the generation-bound resolver catalog. Every row retains the indexed commit,
canonical path, Git object ID, SHA-256 blob
digest, byte and line range, exact record identity, and an opaque citation.
Unit state remains independent metadata: unavailable or ambiguous attribution
never hides a source occurrence.

The response's generation state is `current`, `missing`, `failed`, or `stale`.
Only `current` sets `matching_rows_state: exact` and can return rows from the
leased current generation. The other three states return no
partial rows and set `matching_rows_state: unavailable`; they omit the numeric
total rather than serializing a zero-caller claim. Unknown, hidden, and
deleting repositories all return `404` before
caller authority, filesystem bytes, or repository-specific caches are read.

A current V3 generation may also report `coverage_record_count` and
`covered_candidate_count`. When an exact protocol resolver has no descriptors,
one compact record accounts for a complete immutable candidate member instead
of materializing one `no_direct_caller` abstention per candidate. After a
descriptor-present pair has scanned its complete member, V3 also replaces a
fact-free artifact with one compact `zero_caller_facts` record. That record
partitions every candidate into no-direct inputs plus explicit catalog-owned,
domain-unselected, excluded-`go_test`, invalid-UTF-8, oversized-source, and
resolver-generated gaps and embeds the exact source-byte total. Its receipt
names the compact reason and must agree with the record's exact source-read
count and bytes; a `no_resolver_descriptors` receipt is valid only with zero
reads and bytes. A pair containing any result or fact-bearing unresolved occurrence
remains fully materialized. These counts explain why the generation contains
no matching rows without inventing per-source evidence. They do not establish
runtime non-use, extraction completeness/accuracy, or a semantic zero-caller
claim. Historical V1 generations omit both fields; historical V2 coverage is
accepted only with its original no-resolver, zero-read semantics.

When a `failed` generation has durably completed its caller partitions, its
partition progress may also include up to 32 source-free refusal summaries.
Each summary names the closed stage, caller generation kind, classification,
dimension, observed value, limit, and number of refused pair outcomes it
represents. An explicit `unknown` summary means the exact outcome receipts were
more diverse than the bounded projection or a compatibility caller submitted
a terminal outcome without a typed reason; it must not be interpreted as a
measured limit. Refusal
summaries explain unavailable caller authority only. They do not turn partial
rows into evidence, establish zero callers, or relax any production bound.

Caller Map cursors are opaque, HMAC-authenticated, process-local, and
snapshot-bound. They bind the normalized query and page size, authorization
projection, complete generation, manifest, pair set, and monotonic publication
revision plus the store-owned exact-writer-claim-and-nonce publication
incarnation. The
incarnation cannot repeat across same-name delete/recreate, so permission loss,
a generation transition, and `A → B → A` all invalidate continuation. A
non-empty first page retains one request binding
for its cursor and citations. It survives for up to five minutes after that
first page; process restart loses it, while a live binding pins its reverse
index. Capacity pressure retires the oldest idle binding only after it can
preflight enough slots and positions; active and retired-in-flight bindings
remain fully counted, and only unreclaimable pressure refuses retryably.
Restart from the first page after an expiry or pressure-retirement conflict; after process restart the
rotated HMAC makes the old cursor invalid input. After every page is built,
generation authority is swept first and authorization is checked last. A warm continuation rereads
only its selected canonical records and never rematerializes the reverse index,
but its reopen/final fences still perform bounded complete-publication file-
identity sweeps without content reads or hashes; a
restart or publication-registry cache miss can still perform the separately
bounded cold complete-publication validation. The list creates no proof bundle.

Each exact row's citation can be opened through
`GET /api/contract_callers/citation?citation=...`. The server reauthorizes the
repository, reopens the exact generation and publication revision, rereads the
named caller record, resolves its path at the generation's immutable commit,
and verifies the Git object ID and complete at-most-4-MiB blob digest. The
response contains only the cited byte range. A citation cannot list a tree or
directory, open an unrelated path, fetch the whole file, or widen focused
search/local evidence. Its compact signed token refers to that row through the
capped request binding instead of embedding maximum-shaped generation state.
It expires with the binding and is process-local like the cursor; after expiry
or server restart, list the row again to obtain a new token. T20.12's dedicated UI
exposes this as **Read exact cited bytes**; MCP exposes the same service as
`read_operation_caller_citation`.

Select an operation in Contract Atlas and choose **View callers** to open
`#/callers` with its protocol, declaration repository, lineage, and canonical
operation already fixed. The link and route exist only when the authenticated
`contract-caller-map` capability is enabled. Caller Map is part of the
Contract Impact workflow, so the existing **Impact** navigation item stays
active and no separate Callers item is added.

The page shows at most one 100-row server page. Filters cover unit, owner, path
prefix, code role, tier, freshness, resolution, and source/unit server
ordering. Source view places abstentions in **Needs review**; unit view groups
the same current-page occurrence identities without another request. Each row
retains its immutable source identity, exact object/digest identity, expandable
record byte identity, and exact-range citation action. Resolved singleton
attribution is inline; only one ambiguous
candidate list of at most 64 candidates is mounted at once, and its
pre-truncation total names any omitted remainder. Previous pages retain only
opaque cursors, not hidden rows, and a changed authorization or complete
generation requires **Restart from first page**.

The generation panel distinguishes exact rows from unavailable rows and shows
the publication state, revision, commit, and generation digest when present.
The progress line uses an exact total only for `matching_rows_state: exact`;
an unavailable generation displays no caller total. An exhausted exact empty
page means only that no retained direct-syntax result or abstention matched the
filters in that generation. It does not establish runtime use, completeness,
extraction accuracy, migration completion, or decommission safety.
The reverse index also has an independent 128 MiB identity ceiling below the
writer's maximum publication shape. A writer-valid generation that crosses it
returns deterministic `422`, not partial rows or a zero total; retrying that
unchanged generation cannot clear the refusal.

Choose **Compare replacement** from an exact Caller Map header to open the
default-dark `#/compare-callers` workflow. The route first uses the bounded
Contract Atlas catalog to select a second complete endpoint identity; it does
not ask for a typed operation string. The comparison is available only with
the authenticated `contract-caller-comparison` capability and remains under
the existing **Impact** navigation item.

T30.6k moves this route onto the same exact caller engine as Caller Map. Both
endpoint repositories are authorized before either caller pointer,
publication directory, or repository-specific cache is read. For a current
pair, after it builds the page, the service checks both complete-generation
summaries in one store transaction, both final publication descriptors, and
both permissions. This is one jointly fenced read, not two independently timed
Caller Map requests.

The old and replacement panels each show `current`, `missing`, `failed`, or
`stale`. Only two current generations produce comparison rows. If either side
is unavailable, the whole page says that matching rows are unavailable and
shows no rows, classifications, cursor, or numeric total. A missing old side
does not mean new-only, a missing replacement does not mean old-only, and a gap
never means zero callers. Restart from the first page after an independent
side transition or permission change.

The UI and `GET /api/compare_operation_callers` classify the union of both
endpoint populations at `level=occurrence` (default) or `level=unit`.
`old_only_evidence`, `both_evidence`, `new_only_evidence`, and `unresolved`
are literal evidence states, not migration verdicts. Occurrence keys are
immutable `repo@commit:path:start-end` sites. A unit key is used only when a
resolved source occurrence has exactly one consistent unit candidate;
ambiguous, unavailable, and extractor-abstention sites remain distinct
unresolved occurrences.

The comparison accepts the same `unit`, `owner`, `path_prefix`, `code_role`,
`tier`, `freshness`, `resolution`, and `ordering` filters as Caller Map, plus
`classification`. Pages default to 50 rows and accept at most 100. Each side
of one classified row exposes its exact occurrence count and at most four
source citations, with an explicit truncation flag when more exist; the whole
page hydrates at most 100 exact caller records. Resolved unit attribution is
shown once on the comparison row rather than repeated in citation samples.
Every citation opens through
the same exact-range route as Caller Map and returns only its immutable
commit/object/digest-verified bytes. The combined first-page traversal inspects
at most 50,000 protocol/operation-bucket positions before lineage and optional
filters. The page mounts only its current server page and
retains only bounded cursor history.

The cursor is opaque, HMAC-authenticated, and process-local. It binds the
complete comparison query and page size, both repository authorization
projections, and both full generation, manifest, pair-set, caller-publication-
revision, and non-repeating-incarnation identities. A compact two-index
binding survives for up to five minutes under the same shared eight-binding
and 200,000-retained-position limits as Caller Map. Expiry, idle pressure
retirement, process restart, or a transition on either side requires a new
first page; no cursor can continue against only the unchanged endpoint.

An exact empty current/current result means no retained direct-syntax result or
abstention matched the selected scope. Old-only evidence does not establish
that migration is incomplete. The read creates no proof bundle and establishes
no runtime use, completeness, extraction
accuracy, migration completion, decommissioning safety, or retention bound.

The vocabulary is now explicit. `contract-atlas-v2` calls only a
declaration-lineage-proven occurrence `resolved_caller`; a legacy name match
against an exact declaration is `unresolved_name_match`, and parser/resolver
abstention is `extractor_abstention`. `contract-impact-report-v2`, whose input
is still a bare operation rather than a declaration identity, separates
`resolved_evidence`, `matching_call_evidence`, and
`extractor_abstentions`. It does not present an operation-object match as a
known-caller roster. That legacy evidence reader remains at 1.2.0; public
Caller Map and caller comparison project
the separate direct-syntax complete caller generation. All surfaces remain
behind their existing provisional protocol flags.

The MCP Caller Map annex now supplies the missing exact-identity workflow.
`search_contract_operations` returns selectable protocol, repository,
declaration-lineage, and canonical-operation identities from the same bounded
Contract Atlas service as HTTP. `get_contract_operation` accepts exactly that
identity and returns its endpoint header, request/response shapes, immutable
declaration citation, related evidence, and coverage.
`list_operation_callers` pages the same exact Caller Map service and accepts
its unit, owner, path, code-role, tier, freshness, resolution, ordering,
page-size, and cursor controls. Its generation states, rows, ambiguity,
abstentions, exact total or unavailable-total state, revision-bound cursor, and
opaque citations are not reinterpreted by the MCP adapter.
`read_operation_caller_citation` accepts one opaque citation returned by that
list. It invokes the same reauthorization, complete-generation, record,
immutable-object, digest, and exact-range reader as the HTTP citation route and
returns only the cited bytes.

The older `find_operation_consumers` remains deliberately different: it
requires a caller-supplied bare canonical operation and persists one bounded
proof bundle of matching call evidence and extractor abstentions. It does not
establish declaration identity or become a known-caller roster. Ordinary
Caller Map discovery, detail, and paging persist no proof bundle.
`compare_operation_callers` projects the same exact two-sided
comparison service as HTTP, accepts both complete endpoint identities and the
shared filters, and returns bounded occurrence- or unit-level
classifications, both exact generation states, an exact total only for a
current/current pair, ordinary Caller Map citations, and one opaque two-
publication cursor. It performs no adapter-side classification or
summarization. A stale authorization, binding, or generation on either side
must be restarted rather than bypassed.

Epic 20's capacity, publication, paging, and closure gates are retained
engineering records rather than workflow instructions. Their receipts and
digests live under [`spike/t201/`](../../spike/t201/README.md) and are indexed
by [RETAINED_RECORDS.md](../RETAINED_RECORDS.md); the opt-in
`make t20-closure` acceptance journey is listed with the contributor targets
in [OPERATIONS.md](./OPERATIONS.md#developing-phebs). Passing those gates
changed neither the experimental-dark registration nor the external
`NOT_ESTABLISHED` accuracy posture.

Change Workbench is retired. A human can browse a declaration in Contracts,
carry its operation to Impact, inspect cited matching/unresolved evidence and
the coverage certificate, then use Search, SCIP navigation, and History
independently. PHEBS provides evidence; Cortana owns change organization and
human decisions. Historical Workbench fixture receipts remain archival only.

## Neutral focused-service development cohort

`make dev` and `make dev-api` bind one retained neutral Git bundle through the
ordinary sync, focused-index, candidate, extraction, resolver, caller-leaf,
and complete-publication paths. They also select the
companion T33.5 operator catalog through the ordinary catalog ingestion and
service-state reconciliation paths; there is no directory response fixture.
The repository's active `orders-service` analysis unit selects
`service/orders`, its protobuf/generated supporting files, snapshot, and
`go.mod`. It deliberately leaves an unrelated bulk needle and one real gRPC
caller outside the focused shard. The former is absent from Search; the latter
remains visible only through the separately labeled repository-overlay Caller
Map generation. One `_test.go` file is classified and reported as excluded
`go_test` evidence. Protobuf declarations, the in-unit registration, and Kafka
producer/consumer facts are extracted from committed source rather than
projected by a response fixture.

Search labels a result with that exact focused scope only when the result's
single revision equals the loaded repository status `indexed_commit_hash`.
An explicit historical `rev:`, mixed-revision response, missing revision, or
indexing transition instead shows `search_revision_scope_not_projectable` (or
`search_index_scope_unavailable` when no indexed revision is available) and
does not attach the current analysis-unit digest or paths to those results.

The recipes explicitly clear the Contract Atlas and Thrift-field fixture
bindings. Contracts uses the same store-derived evidence
and authorization fences as an ordinary provisional instance. Ordinary
`phebs serve` remains unchanged and default-dark. The exact source and evidence
demo steps are retained in the
[T30.7 cohort README](../fixtures/t30.7-neutral-service/README.md); the catalog
census, directory steps, receipt, and non-claims are retained in the
[T33.5 companion README](../fixtures/t33.5-service-directory/README.md).

## Service directory

When the authenticated server advertises `service-catalog-v2`, each visible
repository row offers **Services**. The link opens the source-free directory
with the repository fixed in the hash route. Authority, source/catalog/state
identities and revisions, lifecycle counts, accepted/unowned source counts,
and shared placements describe the exact catalog and service-state snapshot;
they do not read source bytes or infer a runtime topology.

The contextual-help control beside **Service directory** opens the canonical
**Service catalog authority** definition and its evidence and authority
boundaries. It is available both before a repository is selected and after the
directory loads. Opening it performs no request and does not promote catalog
metadata into ownership, deployment, runtime, or relationship authority.

Use lifecycle and disposition filters to isolate unavailable, stale, conflict,
proposal, or accepted identities. Removed identities are excluded by default
and require **Include removed identities**. Choosing a row adds its exact
`service_key` to the route and shows incarnation, desired/active identities,
reason or successor lineage, and its primary, supporting, shared, generated,
and typed path roles. A successor is declared catalog lineage, not proof that
one service calls, deploys with, or replaces another at runtime.

The route retains repository, filters, removed opt-in, cursor, and selected
service. Reload and browser back/forward therefore repeat the same authorized
request. Changing a filter deliberately clears the cursor and selected detail.
**Next** replaces the page with the server's opaque continuation; **First
page** clears that continuation exactly, while browser back/forward returns to
visited cursor routes. A filtered page may be empty while still offering Next:
the bounded server scan stopped at its continuation rather than proving there
are no later matches. A refused cursor or changed authority is an error, not an
empty directory. A missing selected service leaves the valid inventory usable
and reports the refusal only in the detail panel; either retry reloads the
current exact route.

The page mounts one 50-row inventory page and at most one detail. It does not
poll, accumulate prior inventory pages, cache authority across principals, or
turn catalog paths into evidence. **Search this service** uses the exact
service-search scope when the service is current or explicitly stale.

Directory continuations for v2 and v3 are HMAC-authenticated and process-local,
so a server restart requires a fresh first-page route. An explicit repository
runtime selector chooses exactly one backend; an absent selector preserves v2.
V3 additionally binds the exact immutable member range and keeps the verified
member lease until response validation and the final authorization/authority
fence complete. Runtime transitions keep the final idempotent state chunk
retryable until the next durable state or relationship schedule exists or the
exact selector commits; a temporary not-ready handoff never exposes a partial
backend. If a continuation refuses, the directory preserves its filters
and offers a first-page route. Concurrent
inventory/detail responses render detail only when repository, catalog
generation/revision, and state revision match; otherwise the inventory remains
available and the detail fails closed with retry and clear-selection actions.

When the server also advertises `service-relationships-v1`, the selected
detail adds an **Exact relationship overview**. Its three linked summaries are
the exact bounded reference counts for:

- **Contracts used & dependencies** — RPC source participation, with accepted
  dependency services where catalog placement proves one;
- **Contracts provided, callers & dependents** — RPC target participation,
  with accepted caller/dependent services where placement proves one; and
- **Topics** — Kafka producer/consumer source evidence, preserving source
  spelling, plane, and classification.

Each summary opens its exact 25-row table. Rows retain source and target paths,
all accepted/proposal/conflict/rejected role-and-origin claims, shared and
unowned posture, classification/reason, and accepted counterpart services.
**View citation** reauthorizes and reads only that row's immutable source span;
the panel repeats its relationship generation/root and source object/content
identities. It is evidence, not a runtime call, broker, deployment, or owner
claim.

The page creates three first-page relationship bindings once per selected
service and reuses them when switching summaries. **Next exact page** advances
only the selected server-bound cursor; **First page** returns to its retained
first page. Reloading an opaque cursor after its five-minute lease expires is
a visible refusal and requires restarting that summary. Mixed relationship
root identities, a different service incarnation/desired generation, failed
or unavailable roots, admitted truncation, missing capability, and exact-empty
results display as distinct failed, stale, unavailable, partial, unsupported,
and empty states. Gaps never become zero counts. The narrow layout stacks the
directory and detail and keeps the wide exact table inside its own labeled
scroll region.

### Explore relationships across repositories

From an exact service overview, choose **Explore across repositories** to open
`#/relationships` with that service and repository prefilled. The relationship
explorer requires one exact `service_key`; the repository is optional. Clearing
repository deliberately asks the existing authorization-first reader for the
same service key across all visible indexed repositories, up to its 32-root
bound. This is a broad fallback, not a claim that equal keys across repositories
share one deployment or owner.

The contextual-help control beside **Relationship explorer** opens the
canonical **Exact static relationship** definition and its evidence and
authority boundaries. It is available before and after a query, performs no
request, and does not turn source relationships into runtime traffic,
deployment, ownership, or completeness claims.

The remaining filters map directly to the exact reader rather than filtering a
larger client result:

- **Direction** selects all evidence, RPC uses/dependencies, RPC
  provided/callers, Kafka producers, or Kafka consumers;
- **Evidence** selects all, RPC, or Kafka source observations; and
- **Contract or topic** is an optional exact operation/topic lookup key, not
  fuzzy search.

**Apply filters** creates one new bounded reader binding and records every
filter in the hash route. Editing fields alone performs no read. **Next exact
page** advances the opaque cursor bound to that query and authorization
snapshot; **First exact page** starts the same filter set without the cursor.
An expired or crossed cursor is a visible refusal and requires restarting the
query.

Coverage above the results repeats authorized, complete, empty, failed,
unavailable, scanned, returned, and truncated authority. A failed or
unavailable root remains a gap, and admission truncation remains partial; the
screen never relabels either as exact empty. Desktop presents source path and
span first in the authoritative table. Mobile uses the same rows as a vertical
list so primary evidence does not require horizontal page scrolling. Every row
retains its exact repository, contract/topic, direction, service route,
catalog claims, classification/reason, and **View citation** action.

**Show page diagram** is optional and performs no server request. It renders
one straight route for each currently visible sorted row and shares that row's
`R-01` through `R-50` identifier with the table/list. It never loads hidden
pages, aggregates duplicate-looking evidence, adds a service, invents a
transitive edge, or becomes authority. The table remains authoritative. A
citation still reauthorizes and reads only its immutable source span while
repeating the exact relationship root and source object/content identities.

## Evidence terminology

The generated glossary below defines the nine terms that qualify PHEBS evidence.
Change intent and success criteria belong in Cortana.

<!-- BEGIN GENERATED PHEBS EVIDENCE GLOSSARY -->
#### Canonical PHEBS evidence glossary

The following help is generated from the reviewed `phebs-evidence-glossary-v2` source. Glossary digest: `sha256:33c75aa4176fb0ec3aa934177b5b33c99b715363e83b3777786b992de9daac06`.

##### Analysis scope & gaps

Shows what phebs examined, what evidence was available, and what remained unsupported or unresolved.

This summary binds visible repositories and revisions to evidence domains, freshness, failures, inventory boundaries, unresolved counts, and unsupported planes. It qualifies the adjacent result and is not a completeness score.

- Evidence boundary: It summarizes recorded processing and inventory state; it does not prove that unobserved callers, resources, or runtime uses do not exist.
- Authority boundary: Only the requesting principal's authorized repository universe contributes rows, counts, or capability state.
- Registered surfaces: `caller_map`, `impact`, `manual`, `mcp`
- Required capabilities (all): none
- Required capabilities (any): `contract-atlas`, `contract-impact-report`, `coverage-certificate`
- When unavailable: Analysis scope & gaps is unavailable because no supporting contract or coverage capability is enabled.

##### Could not resolve

A relevant source construct was observed, but the bounded resolver deliberately did not assign it to one identity.

This is an extractor abstention, not a confirmed caller and not a processing failure. The reason and cited source remain available for review when the evidence pack recorded them.

- Evidence boundary: The row proves an observed construct and a refusal reason only; it makes no claim about the construct's runtime target.
- Authority boundary: The label is derived from authorized published evidence and cannot be upgraded by presentation code.
- Registered surfaces: `caller_map`, `impact`, `manual`, `mcp`
- Required capabilities (all): none
- Required capabilities (any): `caller-map-exact-identity`, `contract-impact-report`
- When unavailable: Resolver abstentions are unavailable because no supporting caller or impact capability is enabled.

##### Coverage certificate

The deterministic audit receipt behind Analysis scope & gaps.

The certificate records the authorized repository universe, indexed revisions, published extraction runs, freshness, failures, counts, protocols, and inventory boundaries under one content digest.

- Evidence boundary: It proves change detection over recorded extraction state, not extraction correctness, business completeness, or runtime absence.
- Authority boundary: Invisible repositories are structurally unreachable to the builder and never appear in certificate bytes or counts.
- Registered surfaces: `atlas`, `impact`, `manual`, `mcp`
- Required capabilities (all): `coverage-certificate`
- Required capabilities (any): none
- When unavailable: The coverage certificate is unavailable because extraction coverage is not enabled for this surface.

##### Exact static relationship

A published RPC or Kafka source relationship bound to one exact service incarnation and generation.

Each row retains its repository, selected service key, incarnation, service generation, relationship root, evidence kind and plane, lookup key, attribution class, and immutable source citation. Missing or unavailable roots remain explicit gaps.

- Evidence boundary: Static source evidence does not prove runtime execution, traffic, ownership, or completeness; an empty result is exact only when every authorized root is complete or empty.
- Authority boundary: Rows are selected only from authorized exact-current relationship roots. Citation loading reauthorizes repository access and preserves the selected row's immutable relationship-root and source identities; presentation cannot promote ambiguous, shared, unowned, failed, or unavailable evidence into an exact runtime edge.
- Registered surfaces: `manual`, `mcp`, `relationship_explorer`
- Required capabilities (all): `service-relationships-v1`
- Required capabilities (any): none
- When unavailable: Exact static relationships are unavailable because the service relationship capability is not enabled for this surface.

##### Implementation evidence

Cited source or history that may inform how the change is implemented.

Search matches, definitions, references, tests, mocks, documentation, file content, blame, commits, changed-file metadata, and diffs are different evidence shapes. Evidence citations retain the immutable repository, revision, path, span, and selection-rule provenance returned by their route; Git routes retain only their requested route context and the bounded fields returned by the reviewed file, history, blame, commit, or diff reader.

- Evidence boundary: Similarity or proximity is not a correctness ranking and does not authorize an edit.
- Authority boundary: The developer reviews and decides whether evidence is relevant; phebs does not turn it into an instruction.
- Registered surfaces: `blame`, `commit`, `file`, `history`, `manual`, `mcp`
- Required capabilities (all): none
- Required capabilities (any): `code-navigation`, `history`, `source-search`
- When unavailable: Implementation evidence is unavailable because search, code navigation, and history capabilities are not available.

##### Matching static evidence

A source occurrence whose extracted object matches the question.

The occurrence keeps its immutable citation and extraction tier. A matching operation object may not be joined to one declaration lineage, generated client, logical service, deployable, or runtime use.

- Evidence boundary: This is source-level matching evidence, not a proven service roster or a resolved caller for one exact declaration.
- Authority boundary: Presentation code may qualify or group the row but cannot promote its evidence tier or lineage.
- Registered surfaces: `atlas`, `caller_map`, `impact`, `manual`, `mcp`
- Required capabilities (all): none
- Required capabilities (any): `contract-atlas`, `contract-impact-report`
- When unavailable: Matching static evidence is unavailable because contract evidence is not enabled.

##### Name match needing review

An operation-name match that is not proven to belong to the selected declaration.

The source citation and candidate operation remain reviewable, but missing or ambiguous generated-client and declaration provenance prevents exact caller attribution.

- Evidence boundary: A shared method name is not contract identity and cannot establish blast radius for one declaration.
- Authority boundary: Only a validated exact-identity join may promote the row to Resolved caller.
- Registered surfaces: `caller_map`, `manual`, `mcp`
- Required capabilities (all): `caller-map-exact-identity`
- Required capabilities (any): none
- When unavailable: Name-match review is unavailable until the exact-identity Caller Map capability is enabled.

##### Resolved caller

A source call occurrence joined through generated-client provenance to the exact selected declaration lineage.

The row retains the call-site citation, generated symbol, wire operation, declaration lineage, and any separate unit attribution. Missing or ambiguous attribution never removes the source occurrence.

- Evidence boundary: Static resolution does not prove runtime execution, traffic, ownership, or migration completion.
- Authority boundary: Only the exact-identity Caller Map service may emit this label; legacy matching evidence cannot be renamed into it.
- Registered surfaces: `caller_map`, `manual`, `mcp`
- Required capabilities (all): `caller-map-exact-identity`
- Required capabilities (any): none
- When unavailable: Resolved callers are unavailable until declaration-proven caller identity is enabled; matching static evidence remains separate.

##### Service catalog authority

The reviewed catalog metadata bound to a repository that defines one service identity and its lifecycle state.

The directory binds each service key to its repository, authority source, desired and active generations, incarnation, disposition, membership roles, and retained tombstone or successor state.

- Evidence boundary: Catalog acceptance and source-path attribution do not prove ownership, deployment, runtime traffic, or relationship completeness.
- Authority boundary: Only immutable accepted catalog and store-committed service state for repositories visible to the requesting principal may supply this label; presentation cannot promote desired, stale, conflict, unavailable, or removed state into current authority.
- Registered surfaces: `manual`, `mcp`, `service_directory`
- Required capabilities (all): `service-catalog-v2`
- Required capabilities (any): none
- When unavailable: Service catalog authority is unavailable because the service catalog capability is not enabled for this surface.

<!-- END GENERATED PHEBS EVIDENCE GLOSSARY -->

## Searching

phebs uses zoekt's native query language. Patterns are regular expressions;
plain text behaves like substring search. Filters and patterns combine with
implicit AND; prefix any atom with `-` to negate it.


| Syntax                       | Meaning                                                     |
| ---------------------------- | ----------------------------------------------------------- |
| `foo bar`                    | files containing `foo` AND `bar`                            |
| `"foo bar"`                  | the exact phrase                                            |
| `f[ou]+nc.*Parse`            | regular expression                                          |
| `case:yes Foo`               | case-sensitive (default: smart case)                        |
| `repo:zoekt`                 | repo name matches regex                                     |
| `file:\.go$` / `-file:_test` | file path matches / doesn't match                           |
| `lang:go`                    | language filter                                             |
| `sym:ParseQuery`             | symbol definitions (needs ctags at index time)              |
| `content:foo`                | match file content only (not paths)                         |
| `archived:yes|no`            | filter by repo archived state *(phebs, from repo metadata)* |
| `fork:yes|no`                | filter by fork state *(phebs)*                              |
| `public:yes|no`              | filter by visibility *(phebs)*                              |
| `context:backend`            | restrict to a named repo set *(phebs, see below)*           |
| `rev:release-1`              | select one allowlisted branch/tag revision *(phebs)*        |


Examples:

```
watchModeNeedle repo:my-project
"TODO(ben)" -file:vendor/ lang:go
sym:ClaimJob fork:no
case:yes Searcher file:internal/
ClaimJob context:backend
deprecatedCall rev:release-1 repo:acme/api
```

### All code and service scope

Search defaults to **All code**, preserving the existing visible indexed
repository scope. From a service detail page, **Search this service** opens the
same query surface with an exact repository and service key. The selector
preserves the query while switching scope; incomplete or malformed service
deep links fall back to All code rather than guessing an identity.

Service scope uses the accepted membership-role union for the service's exact
active generation. Shared paths are included, explicit unowned paths are
excluded, and proposal, conflict, removed, or unavailable identities cannot be
searched. A stale service may use only its labeled last-complete active
generation. It never falls back to All code or silently becomes current.

Completed HTTP, SSE, MCP, and UI searches expose a
`phebs-search-scope-v1` receipt. It binds the normalized scope policy, query
digest, exact service authority when applicable, sorted emitted
repository/commit/path citations, result counts, and receipt digest. The
receipt proves which authority and citations produced the response; existing
result limits mean it is not a claim that the citations exhaust the corpus.

HTTP clients pass `scope=service&repository=…&service_key=…` to
`/api/search` or `/api/stream_search`. MCP `search_code` accepts the same three
fields. Omitting `scope` selects `all_code`; service-only fields are rejected
on All code so a stale deep link cannot widen silently.
Malformed queries or selectors return HTTP 400 through typed request errors;
known service generation/state unavailability returns 409. Unexpected store,
reader, or runtime faults remain HTTP 500 rather than being relabeled as an
ordinary unavailable service.

The v3 service-state runtime preserves members of at most 512 services while
splitting a member into at most three native transactions. Each transaction
submits at most 512 records, including current-state updates, actually missing
selected-snapshot preimages, changed summaries and the plan record. A changed
preimage census refuses before mutation. Interrupted prefixes retain their
progress and continue under a valid lease without changing the member's
identity; strict reads remain unavailable until the final matching-summary
CAS. Services already activated from the same catalog
remain readable while later chunks settle. `service_catalogs.<repository>.
runtime: v3` selects the complete v3 catalog/state/search/relationship tuple
only after it validates; omission preserves v2, and neither path falls back to
the other after an explicit selector exists. Compatibility-mode v2 confirms
selector absence again at response emission, while explicit v2 and v3 confirm
the exact selector. V3 state chunks share the selector/backup mutation fence
and move a selected v3 reader to complete v2 authority before changing its
mutable state summary.

The query compiler consumes a verified v3 root/member lease without changing
the existing query authority receipt. Current service scope reads one member;
stale scope may open exactly one historical member, and the lease stays pinned
through the final repository/catalog/state/search and selector fence. HTTP,
streamed HTTP, and MCP share this selector-aware boundary. An ambiguous
selector-commit response keeps both possible filesystem targets pinned until
an exact follow-up read resolves the durable winner. Once any selector exists,
disabling both relationship-capable extraction packs refuses startup rather
than leaving a requested reverse transition permanently pending.


### Revision scopes

HEAD remains implicit and is the revision searched when a query has no `rev:`
atom. Additional revisions are admitted explicitly by full Git ref while the
left-hand key provides the query-facing selector:

```yaml
revisions:
  github.com/acme/api:
    release-1: refs/heads/release/1
    v1.4.0: refs/tags/v1.4.0
```

This indexes `HEAD` plus the two named refs. `deprecatedCall rev:release-1`
searches the release branch only in visible repositories that published that
selector; `rev:v1.4.0` selects the tag. Selectors and refs are case-sensitive.
Exactly one bare, ungrouped, non-negated `rev:` scope is accepted per query.
An unknown selector returns a bounded query error without naming repositories.

The ceiling is eight revisions per repository including HEAD, so the config
allows at most seven additions. Wildcards, commit IDs, short/ambiguous refs,
and duplicate mappings are rejected at startup: values must be canonical
`refs/heads/*` or `refs/tags/*`. A normal sync, webhook fetch, forced reindex,
or watched-ref move republishes the whole admitted set atomically. Changing the
allowlist takes effect on the next sync/index job. File links still use the
immutable selected commit, while extraction, SCIP defaults, coverage, and
proof bundles intentionally remain anchored to HEAD.



### Search contexts

Contexts are named repo sets defined in config — shorthand for scoping
queries to a slice of the index:

```yaml
contexts:
  backend:
    - "github.com/acme/api-*"
    - "gitlab.example.com/team/platform/*"
  docs:
    - "github.com/acme/handbook"
```

`context:backend needle` searches only repos whose full name matches one of
the set's glob patterns (`*` does not cross `/`; a pattern without wildcards
is an exact name). Multiple `context:` atoms union their sets. A context is
a scope, not a predicate: it applies to the whole query and can't be
negated or grouped in parentheses — both forms, and an unknown name, are
rejected with an error. Inside a double-quoted string `context:` is plain
content, not a filter.

Result bounds: `max_matches` (default 50 files, cap 500) and `context_lines`
(default 0, cap 10) on the API; searches are capped at 10 s of wall time.
Each result file includes the immutable indexed commit in `ref`. Repositories
without a committed index state, deleting repositories, and shards without a
live repo row are excluded from every query. A shard whose exact embedded
branch/commit set does not equal the row's atomically committed revision set is
also discarded. Permission filtering runs before `rev:` resolution, and the
selected result commit is checked again before serialization.

### Precise code navigation (SCIP)

#### Current committed-index compatibility paths

`sym:` search uses ctags. Precise go-to-definition, references, and hover use
a committed [SCIP](https://scip-code.org/) index instead. For an unconfigured
whole-repository scope, run the appropriate SCIP indexer, write its binary
protobuf as `index.scip` at the repository root, commit it with the source it
describes, and let phebs sync/reindex that commit. A focused repository instead
uses only its exact configured supporting SCIP path inside the committed unit;
it never falls back to root `index.scip`. No separate upload or side database
is required for either compatibility mode.

The first lookup lazily reads the selected committed SCIP blob from the exact
indexed commit. An absent index is a normal `available: false` result. Index
blobs over 64 MiB, source files over 10 MiB, more than 32 MiB of aggregate
source conversion in one lookup, malformed or semantically oversized indexes,
symbolic/short revisions, and unsafe paths fail explicitly. The LRU snapshot
cache has a 512 MiB accounted budget. Results are deterministically selected;
reference responses stop at 500 locations and set `truncated`, and hover
content is capped at 64 KiB. The UI uses UTF-16 offsets (matching browser
strings), while the HTTP API can request UTF-8, UTF-16, or UTF-32 conversion.

Position encoding is document-local. A document that leaves it unspecified or
uses an unknown encoding is omitted from the navigation snapshot instead of
making every valid document in the committed index unavailable. Its document,
occurrence, symbol, and relationship payload still consumes the configured
semantic limits, and queries into that omitted path return an available empty
result. phebs does not substitute the metadata text encoding, because that
field describes source-file bytes rather than SCIP range units. Other
index-wide structural and boundedness failures remain hard errors and are
negative-cached by immutable revision.

In whole-repository mode, the extraction reader uses the same root-only
boundary with its own trusted corpus ledger. The root path is fixed—nested
indexes and manifests are not alternatives—and the blob must have appeared as
a regular file in the complete walk of the indexed commit. Mutable refs and
Git replacement objects cannot redirect it; lazy object fetching is disabled.
The reader opens only the recorded immutable blob, enforces its separate 64
MiB limit, and recomputes SHA-256 before parsing. A root `index.scip` symlink
is an explicit extraction failure, not an “index absent” result. A focused
reader instead opens only the unit-bound supporting artifact, accounts the
whole blob, and rejects out-of-unit documents; it has no root fallback. T20.1
selected the root mode for its frozen gate. Both committed-artifact modes
remain shipped compatibility paths; their one-blob semantic limits are current
refusal boundaries, not the intended typed-index ceiling for the declared
massive-monorepo target. phebs does not yet ship a generated-index manifest or
part-reader surface.

SCIP-derived experimental extractors parse the complete bounded committed index
before applying their source-language projection, so every foreign-language
document and occurrence still consumes the global SCIP safety limits. The
protobuf field reader then considers only `.go` and `.proto` source documents;
the Thrift field and Go caller readers consider only `.go`. A Java, Python, or
other out-of-policy document in a polyglot index therefore cannot abort valid
Go/protobuf extraction merely because its source was not retained by that
domain's candidate policy. A missing eligible `.go` or `.proto` source remains
an integrity failure for the field readers and a typed
`stale_symbol_input` gap for the caller reader. This is a bounded
source-language posture, not a claim that cross-language references are
extracted.

#### Bazel-first managed generation target

The planned product-scale path is a managed indexing provider, not a dynamic
in-process plugin and not one virtual repository per service. Bazel is first:
the initial feasibility path uses
[`scip-go`](https://github.com/scip-code/scip-go#other-build-systems) through
the Go Packages Driver Protocol with the
[`rules_go` `gopackagesdriver`](https://github.com/bazel-contrib/rules_go/blob/master/docs/editors.md),
bound to the repository's current authoritative HEAD and its exact commit,
Bazel configuration, rules_go version, Go toolchain, and indexer identity.
This is the upstream-documented Bazel-aware package-loading path; it is neither
the configured-target completeness oracle nor a historical-commit selector,
and it still requires target-repository measurement rather than establishing
whole-monorepo scalability.

Before that path may run against an authorized target repository, T45.1a must
merge and independently pass. Its source-free harness freezes minimal spike
identities and one Bazel-native planner: bounded exact `cquery` target
enumeration plus a pinned Phebs-owned aspect/provider projection for the stable
target-to-Go-package-load-unit and canonical-document map. Configuration
transitions, aliases, tests, generated/proto/cgo sources, external repositories,
and many-target-to-one-package edges must be deterministic. `scip-go`,
`gopackagesdriver`, `aquery`, filesystem discovery, and partial package output
cannot create or amend those authority layers; an inexact planner stops the
feasibility program.

Managed generation must be partitionable and resumable without requiring one
whole-repository package/type graph or one monolithic in-memory navigation
snapshot. The selected design publishes a Phebs-managed bundle of conforming
SCIP [`Index`](https://github.com/scip-code/scip/blob/main/scip.proto) members
behind one exact complete manifest. Completeness has four distinct bound
layers: the profile's ordered configured Bazel-target universe; the sealed
target-to-Go-package-load-unit mapping returned by planning; each package
unit's canonical document set; and the deterministic document-to-SCIP-member
assignment. A **required unit** always means one stable Go package-load unit in
that sealed plan, never a service, Bazel target, repository, document, or
physical member. One package unit may satisfy several targets, and service
scope remains a separate catalog projection.

A durable attempt manifest binds every layer and every complete, failed,
predeclared-excluded, or unsupported outcome. Only an attempt whose requested
targets resolve completely, whose required package units all complete, and
whose expected documents, routes, and members all validate may publish a
current bundle. A failure, unsupported required unit, missing/extra mapping, or
document/member mismatch leaves the attempt non-current and preserves the
prior complete generation, or `available: false` when none exists. A profile
may publish exact qualified coverage only for target exclusions frozen before
execution; it cannot convert a runtime failure into an exclusion. Partial
members are progress, never current authority. SCIP permits field-wise index
emission and consumption, but it defines neither this bundle nor its routing,
retry, lifecycle, or atomic-publication semantics; those remain Phebs
contracts.

[Bazel `aquery`](https://bazel.build/query/aquery) may supply measured
diagnostic or planning evidence about configured actions and artifacts, but it
is not Go-package, service-identity, or completeness authority.

The initial provider has default-deny network egress and no remote cache. The
operator supplies one immutable dependency/toolchain bundle with a bounded
count/byte/digest manifest. Preflight verifies it and copies it into
request-private custody before repository code runs; missing, oversized, or
mismatched material refuses, and no mutable shared cache is mounted or reused.
Any later network or remote-cache capability requires a separate reviewed
decision with a closed destination allowlist, credential boundary,
repository-code exfiltration analysis, and new negative evidence.

Bazel also has ambient local control and state. The managed boundary disables
automatic system, workspace, and home
[`bazelrc`](https://bazel.build/run/bazelrc) discovery and admits at most one
operator-copied, fully resolved, digest-bound profile configuration. Every run
uses request-private, capacity-bounded
[`output_user_root` and `output_base`](https://bazel.build/remote/output-directories);
shared repository, disk, and action caches are disabled. Any admitted local
cache is request-private, identity-bound, byte-accounted, and lifecycle-owned.
`GOPACKAGESDRIVER` resolves only to the pinned Phebs-owned audited launcher and
closed argv; a repository-provided script, executable path, environment, or rc
file is rejected.
The Bazel server, persistent workers, and all descendants must stop before the
workspace can be sealed or removed. These controls follow Bazel's documented
server/output layout and its distinction between
[`--disk_cache` and remote caches](https://bazel.build/remote/caching); network
denial alone is not treated as isolation.

Generated bundles use regenerate-on-restore. Backup retains only bounded
request/profile intent, not a generated current pointer or member bytes.
Restore exposes generated navigation as unavailable, revalidates current HEAD,
profile, planner, launcher, and tool authority, and may then enqueue a distinct
successor. This recovery/lifecycle contract must pass before a generated reader
or provider registers; T45.9 validates it rather than choosing it.

The managed generation workflow is not shipped yet. Until its backlog gate
closes, installations must continue to provide the current committed artifact:
root `index.scip` for whole-repository mode or the configured unit-bound
supporting path for focused mode. Settings offers no Bazel execution control.
Administrators see one static **Code navigation indexing** boundary with
`01 · Bazel first` and `Unavailable`; it makes no request, infers no repository
support or index absence, and exposes no selector or action. Ordinary users do
not see that operational boundary. Its planned interactive replacement selects
a repository's current HEAD only and displays the resolved immutable commit
read-only.

### Git history

History reads the existing bare mirror; it does not enlarge the zoekt index.
From a file, choose **Blame** for line attribution or **History** for the
rename-following commit list, then open a commit to inspect parents, changed
files, binary markers, and its first-parent diff. Root commits compare against
the empty tree. Blame is capped at 50,000 lines and 10 MiB source blobs, commit
pages at 200 rows, aggregate metadata at 64 MiB, and patch text at 2 MiB with
an explicit `truncated` flag. Git producers are canceled when a hard output
limit is reached. NUL-bearing blobs are rejected as binary; other non-UTF-8
line content is returned with invalid byte sequences replaced for JSON display.
Diff context defaults to three lines when omitted, while an explicit
`context_lines=0` returns zero-context hunks. Every request validates the
repository/path and pins supplied branch names to immutable object IDs before
subsequent Git commands run.

File, History, Blame, and Commit each expose the same canonical
**Implementation evidence** contextual help. The control explains what source,
history, blame, commits, and diffs can support and preserves the distinction
between evidence and instruction or correctness. Opening it performs no Git or
HTTP request.

History failures use the same bounded presentation as catalog failures: one
synthetic leading `Error:` prefix is removed, displayed detail is capped at 512
UTF-16 code units, and unbroken metadata wraps within the viewport. An initial
failure is not reported as **No commits**; a later page failure leaves already
loaded commits visible and the existing **Load more** action remains the only
page-retry path. This display bound is not sensitive-data redaction.

The Commit page presents patch text as labelled file regions. File identity,
status, and statistics come from the structured diff response in its existing
order; the raw `diff --git` line starts a visual region but is not parsed into
path authority. A headerless patch uses file identity only when the response
contains exactly one file. Unmatched prelude or truncated metadata stays
visible as `Patch prelude`, `Patch`, or `File N`. Every patch line remains in
original order, and each file body owns its horizontal scroll. Offscreen file
regions use browser content containment to defer layout and paint; this does
not virtualize or remove the complete patch DOM.

## Web UI

Served at `/` from the binary. After setup/login, the main views are
deep-linkable hash routes:

Search, Contracts, Topics, Caller Map, and Impact share the
scope-aware **Analysis scope & gaps** panel. Expand one repository to inspect
its active unit name, exact primary/supporting paths, typed-index posture,
fresh/stale domain publications, durable disposition, and bounded receipt.
Only one repository's paths are mounted at a time. Focused Search and local
evidence are labeled separately from repository-overlay callers; an
outside-unit caller is expected overlay evidence, not a focused-search result.
Current caller generations show explicit base/excluded-`go_test` records and
complete partition progress. A pre-publication generation may show `N/?`
durably settled partitions, while caller rows and their total remain
unavailable. Queue/claim/lock detail stays in operational logs.

Empty and degraded states remain explicit rather than removing the panel. A
zero-repository certificate says that it contains zero repository rows;
The panel separately says when it has no capability rows, no focused-local
coverage, or no gaps in the bounded projection. A retained failure class stays
visible beside a newer durable outcome. Legal null/omitted supporting paths
render as an empty list, and an explicit gap remains neutral/amber unless it
is a failure or terminal refusal.

### Keyboard navigation

- **`/`** focuses the search input from anywhere (unless already typing).
- **`Cmd/Ctrl+K`** opens the global navigator: a modal go-to dialog listing
  every routed surface the instance advertises (destinations whose
  capability read is still loading or failed are listed as *capability
  unknown* rather than hidden — a failed read never establishes absence),
  the active scope's Search/Directory/Explorer jumps, and up to
  five recently visited scopes. Recents are stored locally per signed-in
  user, contain only scope identities (repository, service key,
  generation), and are recorded only after an authorized authority read
  succeeded. Arrow keys move the selection, Enter navigates, Escape
  closes. The navigator performs no reads: every entry is a plain
  navigation.
- **Search results**: `j`/`k` move the result cursor (focus follows for
  assistive technology), `Enter` opens the selected file at its first
  match, `y` copies the selected path, `o` collapses or expands the
  selected repository group.
- **Citation highlighting** (T44.1): relationship citations and exact caller
  citations in Caller Map and Caller Comparison render cited
  source bytes through the same best-effort line tokenizer search results
  use, in both themes. Their authorization, immutable identity, and failure
  envelopes remain separate; only presentation is shared. The bytes are
  never altered — only presentation spans are added. Highlighting is bounded:
  content over 65,536 UTF-16 units or 1,500 lines renders as exact plain text
  instead (evidence spans are normally tiny; the bound keeps adversarially
  large citations from costing main-thread seconds). The line bound is checked
  without allocating a line array. Unsupported paths remain one plain text
  node without loading the tokenizer, and any tokenizer failure falls back to
  the exact plain bytes. A citation transition shows its current plain bytes
  until its own path/theme/palette-bound tokens are ready; spans from a prior
  citation are never reused. Exact caller read state is also bound to its full
  source and capability identity: a refreshed row hides old bytes immediately,
  aborts the superseded read, and ignores a late superseded response.
- **Directory and explorer lists** (T43.11): the service list and the
  exact relationship rows are single tab stops. With the list focused,
  `↑`/`↓` move the active row, `PageUp`/`PageDown` move by a viewport,
  `Home`/`End` jump to the edges (group headers are skipped), and
  `Enter` opens the active service or pins the active relationship row's
  detail. The lists are windowed: only the visible rows exist in the
  DOM, while assistive technology still announces exact positions
  ("row 4,832 of 10,000").
- **Markdown preview** (T44.3): a Markdown | Preview control on the file
  viewer for `.md`/`.markdown` files. Source is the default and the view
  is URL-borne (`?view=preview`); a line deep-link (`?L=`) forces source,
  since line numbers are a source concept. Preview reuses the exact loaded
  repository/path/revision bytes; toggling the view or using Back/Forward does
  not reread the source. Documents over 131,072 UTF-16 units remain
  source-only. Smaller documents parse in a one-shot Worker with a 1-second
  deadline, then pass a
  bounded result through the sanitizer. Repository Markdown is untrusted:
  scripts, event handlers, styles, and unsafe attributes are stripped; only
  absolute `http`, `https`, and `mailto` links survive and open with
  `rel="noopener noreferrer nofollow"`; empty and relative links become
  ordinary prose; and images are never fetched, instead reading **Image
  unavailable:** plus their alt text. At most 20 top-level `mermaid` fences
  render as diagrams; excess fences remain code. Diagrams use ELK and the
  current design tokens under Mermaid strict mode. Resource-capable syntax,
  renderer configuration, C4 diagrams, and unsafe returned SVG fail closed to
  visible source. A parse/draw failure also keeps its source with a one-line
  reason. A successful diagram has a **Diagram source** disclosure for
  keyboard and assistive-technology access. The parser, sanitizer, Mermaid,
  and ELK assets remain preview/fence-lazy.
- **Code highlight palette** (T44.2): Settings · Appearance offers four
  curated syntax palettes — Phebs (default), Quiet (near-monochrome
  reading), Classic (traditional editor hues), and High contrast
  (maximal separation). The choice re-colors the file viewer, search
  result chunks, and citation source without reload, persists in this
  browser, and every palette meets the AA contrast floor against the page,
  citation band, search-row hover, selected-line, and search-match surfaces
  in both themes (high contrast holds ≥7:1 against the page). A live specimen
  previews the selection and falls back to uncolored source while a new
  palette or theme is being tokenized, so prior colors are never relabeled.
- **Row density**: the header's rows button (next to the theme toggle)
  switches every density-aware surface between comfortable and dense
  rows. Density changes spacing and row height, never information; the
  choice persists locally per browser.

- **Search** (`#/search?q=…`) — the repository explorer always lists the
repositories visible to the signed-in user, even before a search returns
results. Select a repository, expand folders one level at a time, or open a
file directly at its immutable indexed revision; **Add repository filter**
preserves the current query and inserts one exact quoted `repo:` atom. Folder
contents are loaded lazily and cached for that repository/revision/path. On
mobile the explorer is a collapsed **Browse repositories** drawer. Search
results still stream in as shards respond, grouped repo → file, with match
counts and highlighted spans; line numbers link into the viewer.
- **File viewer** (`#/file?repo=…&path=…&ref=…&L=42`) — read-only CodeMirror with
syntax highlighting across ~30 languages (Go, JS/TS, Python, Rust, Java,
C/C++, C#, Ruby, PHP, SQL, HTML/CSS, YAML, shell, and — as of T44.1 — the
contract surfaces Protobuf and Thrift), a file-tree navigation
column that auto-expands to the current file, and a highlighted, scrolled-to
anchor line. Search links carry their immutable commit; old links without
`ref` resolve the repo's recorded indexed commit before loading. Click a
source position to open precise SCIP hover/definition/reference results when
that revision has an admitted typed-index binding. A focused repository uses
only its configured unit-bound SCIP path and rejects an index containing an
out-of-unit document; it never falls back to root `index.scip`. **Blame** and
**History** open the Git views for the same immutable revision.
- **History / blame / commit** (`#/history`, `#/blame`, `#/commit`) — follow a
file across renames, map lines to commits, and render commit metadata,
changed-file statistics, and bounded unified diffs.
- **Repos** (`#/repos`) — sync/index state per repo (polled every 3 s),
orphan flags, indexed commit, and administrator-only **Reindex** controls
(a forced rebuild defeats the incremental short-circuit). The control names
the focused unit it replaces, or says **whole repository** when no unit is
committed. The backing
`/api/repo-status` response also reports any committed analysis-unit name,
digest, exact selected paths/counts, and search/typed-index postures.
Configured repositories report `focused`: search and local evidence are
physically limited to the selected primary/supporting paths. Typed posture is
`unit-bound` only when the configuration explicitly designates a supporting
SCIP artifact; otherwise it remains `repository-root-unbound` and focused code
navigation/extractors report the gap. Unconfigured repositories remain
whole-repository. Every experimental local-evidence publication and coverage
read uses the exact indexed commit plus unit digest, so a same-commit scope
change cannot reuse the previous unit's evidence.
- **Settings** (`#/settings`) — create, copy once, list, and revoke read-only
  API keys. Administrators also see the static, non-actionable Bazel-first
  managed-indexing boundary described above; it is not a generation control.
  The retained `capabilities` metadata field is empty.
- **Audit** (`#/audit`, administrators only) — the recorded action trail:
logins (including failures), setup, logout, API-key lifecycle, and every
mutating API operation, newest first with actor, target, status, and
source IP.
- **Analytics** (`#/analytics`, administrators only) — 30-day search volume,
searches per day, average duration, and the repositories appearing most in
results — computed entirely from local usage events.
- **Contracts** (`#/contracts`, experimental) — browse provisional protobuf
service declarations without first knowing an operation identifier. Exact
repository/package/protocol/lineage filters lead to a service → operation
index, then bounded request/response shapes, independently classified
implementation/caller/abstention evidence, exact coverage state, and immutable
source links. Duplicate service names remain separate by repository and
provisional lineage. The protocol filter defaults to all registered protocols
(protobuf and thrift); each row carries its protocol. Thrift operations show
a `oneway` chip in place of the gRPC streaming chips, argument/result shapes
render through the same message tree (result field `0` is the wire success
slot), and union/exception declarations are badged. **Analyze impact**
carries the selected canonical operation into the Impact form but does not
submit it.
- **Impact** (`#/impact`, experimental) — bounded contract-impact reports for
canonical RPC operations (gRPC and Thrift consumer evidence), stable protobuf
and Thrift field identities, and proposed before/after contract inputs. Field
mode defaults to protobuf's 1..536,870,911 rules (including refusal of reserved
19000..19999) and offers Thrift's 0..32,767 rules explicitly. That choice
validates the input; the report remains neutral and may render every registered
field-reference domain that admits the number. Known and unresolved consumers
cite immutable source revisions; every conclusion renders its complete coverage certificate.
For a focused repository, declarations and local consumers come only from the
unit manifest, and the coverage certificate names that unit's exact roots,
candidate scope, typed-input posture, and freshness. The navigation item
appears only when the server advertises the capability,
and the contract-change tab additionally requires the pinned Buf startup probe.
- **Topics** (`#/topics`, experimental) — topic-centered Kafka evidence:
query one topic spelling and see producers, consumers (group ids as detail),
and — rendered first, always — the unresolved census: per-shape-class counts
of supporting source sites that could not be resolved, with zeros listed
explicitly, `≥` marking bounded lower-bound counts, and distinct per-plane
published-run states so producer-only or consumer-only extraction never turns
the other plane's unmeasured zeros into affirmative zeros. Whole-file
extraction gaps are disclosed separately through the coverage certificate.
For a focused repository, producers and consumers are unit-local; a topic row
outside the unit cannot enter the answer merely because it shares the same
repository and commit.
The navigation item appears with the
`kafka-topic-usage` capability, which the server advertises whenever the
proof surfaces exist — including deployments where the Kafka packs
themselves are dark, in which case every answer honestly shows the no-run
state. Nothing on this page is a completeness claim.

The UI uses its DB-backed session cookie and automatically supplies CSRF
tokens on mutations. A `401` clears stale authenticated state and returns to
the login view.

## HTTP API

The API is OpenAPI-described by itself: fetch `/api/openapi.json` or browse
the interactive docs at `/api/docs`.

**Auth:** application endpoints accept either the browser session cookie or
`Authorization: Bearer <named-or-legacy-key>`. Authentication is not disabled
by omitting `auth.api_key`. Always open: `/api/health`, `/api/version`,
`/api/openapi*`, `/api/docs*`, and auth status/enrollment/login/OIDC routes.
`/metrics` requires authentication (a session cookie or a Bearer API key).
`/api/webhook` uses its own HMAC trust boundary.


| Endpoint                                                            | Method          | Purpose                                                                                        |
| ------------------------------------------------------------------- | --------------- | ---------------------------------------------------------------------------------------------- |
| `/api/health`                                                       | GET             | liveness `{"status":"ok"}`                                                                     |
| `/api/version`                                                      | GET             | server version                                                                                 |
| `/api/auth/status`                                                  | GET             | authentication/setup/OIDC state and current user                                               |
| `/api/auth/setup`, `/api/auth/login`, `/api/auth/logout`            | POST            | first administrator, local login, and session logout                                           |
| `/api/auth/keys`                                                    | GET/POST        | list or create the browser-session user's read-only keys; the optional `capabilities` array must be empty |
| `/api/auth/keys/{id}`                                               | DELETE          | revoke one API key (browser session only)                                                      |
| `/api/auth/oidc/start`, `/api/auth/oidc/callback`                   | GET             | OIDC authorization-code flow                                                                   |
| `/api/search?q=&max_matches=&context_lines=`                        | GET             | search, JSON in one shot                                                                       |
| `/api/stream_search?q=…`                                            | GET             | search over SSE (below)                                                                        |
| `/api/repos`                                                        | GET             | repo rows                                                                                      |
| `/api/repo-status`                                                  | GET             | repos + connections + orphan flag + exact/unavailable prospective last-index-job state + committed analysis-unit diagnostics |
| `/api/services?repository=&status=&disposition=&include_removed=&page_size=&cursor=` | GET | authorization-first bounded service inventory for one repository; list rows omit membership paths |
| `/api/service?repository=&service_key=`                             | GET             | one exact authorized service with lifecycle identities, successors, and bounded membership paths |
| `/api/reindex`                                                      | POST            | administrator only: `{"repo":"github.com/foo/bar","force":true}` → enqueue index job           |
| `/api/retention-status`                                             | GET             | administrator only: fixed eleven-owner/twenty-eight-component retained-capacity status shell   |
| `/api/lifecycle-status`                                             | GET             | administrator only: fixed 16-KiB source-free lifecycle owner/pressure snapshot, including exact dark catalog-v3 logical/root/member byte counters; no cursor, path, identity, content, raw error, or mutation |
| `/api/audit?offset=&limit=`                                         | GET             | administrator only: audit events, newest first, `has_more` paging                              |
| `/api/analytics?days=`                                              | GET             | administrator only: search volume, per-day counts, top repos over the window (default 30 days) |
| `/api/webhook`                                                      | POST            | code-host push/repository events, HMAC-authed (no bearer); 404 unless `webhook.secret` set     |
| `/api/mcp`                                                          | POST/GET/DELETE | MCP over Streamable HTTP; bearer-authed (see [Agents (MCP)](#agents-mcp))                                               |
| `/api/find_operation_consumers?operation=`                          | GET             | experimental permission-scoped bare-operation matching-call proof bundle                        |
| `/api/find_proto_field_references?lineage=&message=&field_number=`  | GET             | experimental permission-scoped protobuf-field-reference proof bundle                           |
| `/api/find_field_references?lineage=&message=&field_number=`        | GET             | experimental permission-scoped protocol-neutral field-reference proof bundle                    |
| `/api/find_kafka_topic_usage?topic=`                                | GET             | experimental permission-scoped Kafka topic-usage proof bundle with an always-present unresolved census |
| `/api/get_extraction_coverage?domains=`                             | GET             | experimental assertion-free extraction-coverage proof bundle                                   |
| `/api/check_contract_compatibility`                                 | POST            | experimental Buf WIRE verdict enriched with permission-scoped affected field references         |
| `/api/proof_bundles/{id}`                                           | GET             | reauthorized immutable proof-bundle read; an ID is not a bearer credential                     |
| `/api/contract_impact_report?operation=`                            | GET             | experimental bounded operation-impact report                                                   |
| `/api/contract_impact_report?lineage=&message=&field_number=`       | GET             | experimental bounded stable-field impact report                                                |
| `/api/contract_impact_report`                                       | POST            | experimental proposed-change impact report over the compatibility request shape                 |
| `/api/contract_impact_reports/{id}`                                 | GET             | reauthorized deterministic report projection of one immutable proof bundle                      |
| `/api/contract_atlas?repository=&package=&protocol=&lineage=&page_size=&cursor=` | GET | experimental bounded service/operation catalog over exact published evidence                    |
| `/api/contract_atlas/operation?repository=&lineage=&operation=`     | GET             | experimental bounded operation, message-shape, implementation, and caller detail                |
| `/api/contract_callers?protocol=&repository=&lineage=&operation=&page_size=&cursor=` | GET | experimental one-repository exact-generation Caller Map with source/unit ordering, typed gaps, and revision-bound pages |
| `/api/caller-generation-progress?repository=`                    | GET             | authorization-first declaration-independent exact caller-generation state, aggregate counts, bounded partition progress/refusals, and digest/count analysis-scope authority without selected paths |
| `/api/contract_callers/citation?citation=`                         | GET             | reauthorized exact caller citation; returns only the commit/object/digest-verified byte range                    |
| `/api/compare_operation_callers?old_protocol=&old_repository=&old_lineage=&old_operation=&replacement_protocol=&replacement_repository=&replacement_lineage=&replacement_operation=&level=&page_size=&cursor=` | GET | experimental authorization-first old/replacement comparison over one jointly fenced pair of exact complete caller generations, with typed whole-page gaps and two-publication cursor |
| `/api/source?repo=&path=&ref=`                                      | GET             | file content (`ref` defaults HEAD); binary comes base64; blobs over 10 MiB return 413          |
| `/api/folder_contents?repo=&path=&ref=`                             | GET             | one directory level                                                                            |
| `/api/tree?repo=&ref=`                                              | GET             | all file paths, recursive                                                                      |
| `/api/find_definitions?repo=&path=&ref=&line=&character=&encoding=` | GET             | precise SCIP definition at a zero-based position                                               |
| `/api/find_references?repo=&path=&ref=&line=&character=&encoding=`  | GET             | precise SCIP references (maximum 500)                                                          |
| `/api/hover?repo=&path=&ref=&line=&character=&encoding=`            | GET             | SCIP signature/documentation at a position                                                     |
| `/api/blame?repo=&path=&ref=`                                       | GET             | line-to-commit attribution, rename-aware                                                       |
| `/api/commits?repo=&ref=&path=&limit=&offset=`                      | GET             | commit history; optional path follows renames                                                  |
| `/api/commit?repo=&ref=`                                            | GET             | commit metadata, parents, and changed files                                                    |
| `/api/diff?repo=&head=&base=&path=&context_lines=`                  | GET             | bounded unified diff and file statistics; context defaults to 3 and accepts explicit 0         |
| `/metrics`                                                          | GET             | Prometheus metrics                                                                             |

Every response from the retention-status path, including authorization denial
and internal error, carries
`X-Phebs-Warning-Code: unbounded_historical_publication_retention`.
Successful administrator responses identify `phebs-retention-status-v1` and
list the current 28 components: 21 core and seven derived. T46.1 removed the
Investigation and change-planning product tables, job-history component, and
artifact-pin namespace from status.
Each component has a bounded aggregate row count; `exact`, `lower_bound`, and
`unavailable` are distinct states. Per-component `physical_database` bytes
remain unavailable. The different byte kinds must never be summed. The
`proof_bundles` owner alone reports its configured retention control.
Non-administrators are denied before inventory work. The fixed budgets,
platform caveats, and backup/restore boundaries are in
[Operations](./OPERATIONS.md#historical-publication-retention).

`stream_search` emits Server-Sent Events: one `results` event per shard batch
(same JSON shape as `/api/search`), then a final `done` event with aggregate
stats; errors arrive as an `error` event. Batches remain progressive and share
one global display ceiling, so their arrival order is not the globally ranked
top-K order returned by `/api/search`. Disconnecting cancels the search.

```bash
export PHEBS_TOKEN='phebs_...'
curl -H "Authorization: Bearer $PHEBS_TOKEN" \
  'localhost:3070/api/search?q=ClaimJob+lang:go' | jq .files[0]
curl -N -H "Authorization: Bearer $PHEBS_TOKEN" \
  'localhost:3070/api/stream_search?q=needle'
curl -X POST localhost:3070/api/reindex \
  -H "Authorization: Bearer $PHEBS_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"repo":"github.com/foo/bar","force":true}'
```

Code-navigation refs must be full 40- or 64-hex commit IDs; omission resolves
the repository's recorded indexed commit. `line` and `character` are
zero-based; `encoding` defaults to `utf16` and also accepts `utf8`/`utf32`.
History endpoints similarly default omitted refs/heads to the indexed commit,
then resolve mutable commit-ish values once before reading. Unindexed or
deleting repositories fail closed.

## Agents (MCP)

phebs is an MCP server: agents search and read your code through the same
index the UI uses. The endpoint is `/api/mcp` (Streamable HTTP, official MCP
go-sdk), guarded by the same DB-backed authentication as the rest of the API.
Create a named key in **Settings** and use it as the bearer token; the legacy
config key remains accepted only while it is configured.

PHEBS MCP exposes read-only evidence tools. It has no Change Workbench
preview, read, creation, or disposition tools. Named API keys are read-only.
The ten core tools are always present; the
complete service-directory/relationship configuration adds five read tools.
Observation progress can add one independently. Provisional proof packs add
five evidence-query tools; Contract Atlas/Caller Map adds four; and a pinned
Buf binary plus successful host-sandbox probe adds compatibility as one more.
With every read annex enabled, discovery contains 26 tools. Agents should
carry exact identities from `search_contract_operations` into the existing
Caller Map, comparison, proof, search, SCIP, and history reads.

| Tool               | Purpose                                                                                                                                                                                                                                                     |
| ------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `search_code`      | full query syntax from [Searching](#searching), including `context:` sets; returns files with line-numbered chunks and match ranges                                                                                                                                              |
| `read_file`        | file content at the indexed revision; optional `start_line`/`end_line`; output over 200 KB is truncated (on a line boundary where one fits) with a `truncated` flag inviting a ranged re-read. Blobs over 10 MiB are rejected outright, like `/api/source`. |
| `list_repos`       | every indexed repo with branch/visibility/index-time metadata plus the same active analysis-unit name, exact selected paths, search posture, and typed-index posture exposed by HTTP repository status                                                                                                                        |
| `list_services`    | one visible repository's exact service-key-ordered lifecycle page; supports bounded status/disposition/removed filters and a catalog/authorization/summary/incarnation-bound cursor, while returning counts but no paths |
| `get_service`      | one exact visible service with the same list-row type plus successors and bounded primary/supporting/shared/generated/typed membership paths |
| `list_service_relationships` | one exact service's authorization-scoped dependency, caller, or source-spelled topic evidence with root/incarnation authority, gaps, placement claims, bounded pagination, and opaque citations |
| `compare_service_relationships` | added, removed, and unchanged exact relationship evidence across two lease-pinned generations; unavailable and truncated states remain explicit |
| `read_service_relationship_citation` | reauthorize and read only the immutable source span named by one current relationship citation; repeats exact root, posting, object, digest, and span authority |
| `find_definitions` | precise SCIP definition for `{repo,path,line,character,ref?}`                                                                                                                                                                                               |
| `find_references`  | precise SCIP references for the same position; maximum 500 locations with `truncated`                                                                                                                                                                       |
| `hover`            | SCIP symbol, signature, documentation, and source range                                                                                                                                                                                                     |
| `blame`            | rename-aware line attribution for `{repo,path,ref?}`; maximum 50,000 lines                                                                                                                                                                                  |
| `list_commits`     | paged history for `{repo,ref?,path?,limit?,offset?}`; maximum 200 commits per page                                                                                                                                                                          |
| `get_commit`       | commit metadata, parents, and first-parent file changes                                                                                                                                                                                                     |
| `diff`             | structured file statistics plus a unified patch, capped at 2 MiB with `truncated`                                                                                                                                                                           |
| `find_operation_consumers` | MCP envelope v1.0 with matching static call evidence for one bare canonical `/package.Service/Method`; it does not establish declaration identity or a known-caller roster |
| `find_proto_field_references` | MCP envelope v1.0 for `(lineage, message, field_number)`; field names remain versioned attributes rather than identity |
| `find_field_references` | MCP envelope v1.0 for one protocol-neutral `(lineage, message, field_number)`; facts retain `proto_field` or `thrift_field` identity and exact citations, and field 0 is valid |
| `find_kafka_topic_usage` | MCP envelope v1.0 for one Kafka topic spelling; facts are producer/consumer evidence rows, the persisted bundle carries the per-shape-class unresolved census, and the answer is never a completeness claim |
| `get_extraction_coverage` | envelope containing the assertion-free coverage certificate over requested extractor domains, or every provisional domain when omitted |
| `check_contract_compatibility` | envelope containing the pinned Buf `WIRE` conclusion plus stable affected-field identities, visible field-reference evidence, exact proof references, coverage, and invocation provenance |
| `search_contract_operations` | bounded Contract Atlas discovery page with complete selectable protocol/repository/declaration-lineage/operation identities, coverage, and continuation cursor |
| `get_contract_operation` | one protocol-qualified exact operation with request/response shapes, immutable declaration citation, related evidence, and coverage |
| `list_operation_callers` | one authorized repository's exact complete-generation Caller Map page with active focused/whole scope, repository-overlay plane, durable partition progress, base/`go_test` record counts when current, typed unavailable states, source/unit ordering, direct-syntax rows and abstentions, exact totals, opaque citations, and revision-bound cursor |
| `read_operation_caller_citation` | reauthorize and return only one caller row's exact commit/object/digest-verified source byte range; grants no tree, directory, unrelated-path, or whole-file read |
| `compare_operation_callers` | exact occurrence- or unit-level comparison of two authorized complete caller generations with typed whole-page gaps, evidence-qualified classifications, immutable exact-range citations, and a cursor bound to both full publication identities |


Code-navigation tool positions and returned ranges are zero-based UTF-16 code
units. Omitted `ref`/`head` values resolve to the DB's immutable indexed
commit. NUL-bearing binary blame, unknown repos, deleting repos, and unindexed repos come
back as tool errors rather than drifting to mutable mirror HEAD.

The six proof/compatibility tools return `envelope_version: "1.0"` as MCP
structured content. Their advertised `outputSchema` is the same generated
draft-2020-12 schema checked in under `schemas/`. Stateless proof queries do
not enumerate a released evidence-pack universe, so their pack-defined
eligible/processing counts are `withheld`, their outcome is `partial`, and a
zero-result response is blocked by `SCOPE_NOT_ENUMERATED`; it is not evidence
of absence. Qualification and refusal prose is selected and rendered by the
server. Clients should display `authoritative_text` verbatim and must not
upgrade a partial, withheld, refused, or truncated result. A hard-truncated
result has no continuation token and remains permanently incomplete and
absence-ineligible.

### Claude Code

```bash
claude mcp add --transport http phebs http://localhost:3070/api/mcp \
  --header "Authorization: Bearer YOUR_API_KEY"
```

or the equivalent `.mcp.json`:

```json
{
  "mcpServers": {
    "phebs": {
      "type": "http",
      "url": "http://localhost:3070/api/mcp",
      "headers": { "Authorization": "Bearer YOUR_API_KEY" }
    }
  }
}
```

Any MCP client speaking Streamable HTTP works the same way.
