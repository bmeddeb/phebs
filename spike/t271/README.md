# T27.1 document-schema contract spike

This directory preregisters the neutral `phebs-document-schema-v1` reader
experiment. The input contract, fixtures, expected declarations/refusals,
public derivation, and decision table were authored before reader
implementation or execution. At this checkpoint **no reader exists and no
reader run has occurred**. Independent lead review and an immutable Git
checkpoint must precede implementation. Later outcomes belong in a separate
results record; they must not rewrite this preregistration.

The spike is isolated: production packages must not import it. It changes no
production schema, pack, API, UI, release claim, or validation label. Its
measurements can establish bounded fixture conformance and determinism only.
Neither the synthetic labels nor this author's public derivation establish
independent extraction accuracy, completeness, live-schema fidelity, store
usage, deployment, migration safety, or a Cassandra pack.

## Frozen inputs

- [JSON Schema](phebs-document-schema-v1.schema.json) closes the structural
  vocabulary. [the frozen decision table](#frozen-decision-table) supplies byte, text, duplicate,
  reference, graph, identity, ordering, and atomic-publication rules that
  structural JSON Schema alone cannot enforce.
- [bounds.json](bounds.json) fixes all parser and output bounds before code.
- [cases.json](cases.json) fixes accepted, refused, and missing-input cases.
  Each accepted case points at its complete declaration and byte-span labels
  in `labels/`; each refused case fixes a typed refusal and zero publication.
- [comparison-labels.json](comparison-labels.json) fixes the three expected
  explicit-snapshot field differences and unchanged field identity.
- [public/receipt.json](public/receipt.json) pins the permitted external
  reference; [public/derivation.json](public/derivation.json) accounts for all
  61 declarations/edges in the hand-derived neutral manifest.
- `preregistration.json` inventories the frozen paths, bytes, and SHA-256
  values. It excludes itself to avoid recursive hashing. Its checkpoint
  commit is supplied by the lead, not invented by an input author.

`author_fixtures.py` is an input authoring tool, not the reader. It constructs
explicit fixture objects and a declaration ledger, and a JSON encoder records
value offsets while writing them. It does not decode a manifest, parse CQL,
or import a future reader. Its CQL span lookup locates hand-selected literal
strings in hand-selected statement ranges. It refuses regeneration after the
preregistration index exists. Future tests must read the frozen artifacts,
never derive expected labels from reader results or regenerate them.

## Public source and complete derivation

The original 2,218-byte Apache-2.0 source is
[`resources/reservation.cql` at
`bed58e7e51768f9f30dd435e74f31e5b3b2649a4`](https://github.com/jeffreyscarpenter/cassandra-guide/blob/bed58e7e51768f9f30dd435e74f31e5b3b2649a4/resources/reservation.cql).
Its original copyright/license header remains in
[public/reservation.cql](public/reservation.cql), accompanied by the
[canonical Apache license](public/LICENSE-2.0.txt). The receipt pins its Git
blob, SHA-256, size, source URL, and license digest.

The derivation has three tables, 29 table fields (including eight explicit
collection/UDT expansions), six table primary-key entries, four table
partition-key entries, one view, six own view outputs, six source edges,
four view primary-key entries, one view partition-key entry, and one schema
set: 61 declarations/edges. Every one has an exact JSON value span and one
or more literal CQL spans. The UDT's five members cite both definition and
map-value use. Wildcard view outputs cite both `SELECT *` and the source
column. Comments, replication settings, and view null filters are explicitly
accounted for as outside neutral structural semantics.

Set and list elements share the neutral `array_element` segment without an
ordering claim; maps expand values only. Type spellings remain opaque. The
reference's omitted clustering directions use the neutral default `asc`,
consistent with the public
[Cassandra CQL documentation](https://cassandra.apache.org/doc/3.11/cassandra/cql/ddl.html#clustering-order);
no partition-key sort or store-vendor limits are inferred. The CQL file is
an external derivation oracle and a deliberately refused raw input. This
experiment admits JSON only and implements no CQL parser or vendor adapter.

## Preregistered execution and measurement

After independent review/checkpoint, implement a bounded, serial reader over
one supplied blob and explicit repository/path/commit context. Run every case
twice from fresh result state. Record byte-identical canonical facts,
citations, catalog projections, refusal census, missing-artifact state, and
explicit two-snapshot comparison. Round-trip every accepted citation to the
frozen input's exact UTF-8 bytes and independently authored offsets/digest;
round-trip every public derivation span to the pinned CQL bytes. Verify every
refusal and missing state publishes zero facts, citations, and catalog rows.

Record actual input/output byte counts, declaration/citation counts,
per-case and total elapsed time, bounded allocation observations if measured,
panic count, tool/host identity, and whether independent labels existed. Do
not turn wall-time observations into a latency or scale guarantee. Exercise
catalog lookup and exact nested-field citation, primary/partition membership,
declared association/view edges, the missing `request-schema-export` state,
and the three fixed comparison differences. A human-facing source-free
example may render these frozen results; it must label authored/captured and
explicit snapshot provenance.

This preregistration claims no implementation PASS. Results, limitations,
and any contract correction discovered during implementation must remain
explicit. A correction to a frozen input requires a separately reviewed
version/checkpoint; it must not silently rewrite labels to match output.

## Frozen decision table

Frozen on 2026-10-18 before reader implementation/execution. This table is
required retained T27.1 experiment evidence; the lead records its adoption
in the owning same-PR PLAN ADR.

| Subject | Frozen decision |
| --- | --- |
| Admission | One strict UTF-8 JSON blob plus explicit repository, clean repository-relative manifest path, and exact indexed commit. Only `phebs-document-schema-v1` is admitted. No service/store/registry connection, upload, converter execution, CQL parser, client-code inference, or private dialect. |
| Root vocabulary | Exactly `format`, mandatory `source_class`, `tables`, and `views`. All objects have closed properties and required structural members. Explicit empty tables/views is an accepted empty declaration set; absent input is a different workflow state. |
| Schema-set lineage | The exact `(repository, manifest_path, format)` tuple. Repository is a nonblank exact label, never fetched. Path is clean relative POSIX without absolute form, empty/dot/parent components, backslashes, or controls. Commit is 40 lowercase hexadecimal characters. Repository/path each have a 256 UTF-8-byte bound. |
| Provenance versus identity | Commit, full blob digest, exact citation, and source class accompany facts but do not establish stable schema/table/field identity. Explicit comparison requires equal lineage and source class; it does not silently merge classes or call either input live/current. |
| Owner identity | Typed `table` or `view` plus exact owner name within the lineage. They are distinct namespaces: a table and a view may have the same spelling. Duplicate names within either namespace refuse the entire manifest. Names do not establish cluster, database, environment, service, deployment, ownership, or runtime identity. |
| Field identity | Table identity plus the complete typed path array; view identity plus its exact own output name. Identity encoding uses structured canonical JSON components and a digest, never delimiter splitting, case folding, NFC normalization, display labels, or commit/digest provenance. Composed/decomposed Unicode and literal `.`, `/`, or `[]` remain distinct valid field names. |
| Paths | A nonempty array, at most eight segments, starting with `{kind:"field",name:...}`. Later segments are fields, `{kind:"array_element"}`, or `{kind:"map_value"}`. Traversals carry no name or other keys. A path is metadata, not a filesystem path. Literal `[]` is a field, never a traversal. Flattened paths require no implicit parent declaration, and the reader never infers missing parents or interprets type strings. |
| Text | Names/kinds/prefix units are nonblank strings of at most 128 UTF-8 bytes; types at most 256. Empty, whitespace-only, ASCII/C1 control text, invalid UTF-8, and unpaired JSON surrogate escapes refuse. Other exact Unicode survives. Structural JSON Schema character bounds are only an outer vocabulary check; the reader enforces byte/text rules. |
| Opaque metadata | Type, index kind, and prefix unit spellings remain exact and citable, without vendor parsing, equivalence, conversions, or vendor-limit validation. Prefix length is an optional positive signed-64-bit integer numeral (no fraction/exponent), at most 9223372036854775807, and requires an opaque `prefix_unit`; a unit without length refuses. This is a representability cap, not a store cap, and the number never determines work/allocation. |
| Primary keys | Every table/view has one nonempty ordered list of exact own field paths, each resolving in that owner. Entry direction is `asc` or `desc`, default `asc`. Duplicate paths refuse even if directions differ. A view's key path has exactly one field segment naming an own output. |
| Partition keys | Optional, direction-free nonempty path list that must equal a prefix of primary-key paths ignoring primary directions. Absent means `undeclared`, never an assumed default. Duplicate entries refuse before prefix validation. |
| Indexes | Ordered owner-local declarations with distinct exact names, opaque kinds, and nonempty ordered exact own columns. Column directions default `asc`; optional prefix metadata is preserved. Duplicate column paths within one index refuse. View indexes resolve only in the own output namespace. Index order and column position are semantic. |
| Associations | Table-owned named logical edges to another exact table, each with a nonempty list of exact source/target field-path pairs. Names and pairs must be distinct within their respective owner/association. Distinct associations between the same table pair are allowed. Self edges and association cycles are allowed and are never recursively traversed; no foreign-key or referential-integrity claim. |
| Views | Own ordered output names and opaque types, each with zero or more exact typed sources `{kind:"table"|"view",name,path}`. The explicit view source kind makes view dependency acyclicity testable without guessing whether a name denotes a table. A view source path is exactly one field segment naming an output. Duplicate source tuples per output refuse; a self or multi-view cycle refuses. Sources are declared edges, not proof of deployment/freshness/population/use. |
| Source class | `authored` records committed repository intent; `captured` records only the supplied export's committed structure. Both are declarations, neither the live store. No capture timestamp, adapter label, registry state, usage, or inferred current-shape merge enters the contract. |
| Payload prohibition | The object property names `record`, `records`, `sample`, `samples`, `default`, `defaults`, `example`, `examples`, `value`, `values`, `data`, and `documents` refuse at every depth. A field literally named `value` remains a valid string value. Closed vocabulary rejects other unexpected metadata. No sample/default values or full source snippets are emitted. |
| Publication | Validate the entire bounded input and all references/cycles/counts before publishing any fact, citation, or catalog row. A refusal or missing artifact yields zero of each. No partial success, truncated catalog, or fallback type inference. |
| Citation | Every emitted object/edge cites its complete exact JSON value: schema set=root object; owner=owner object; field=field object; primary entry=entry object; partition entry=path array; index=index object; index column=column object; association=association object; pair=pair object; view source=source object. Span is zero-based `[start,end)` UTF-8 bytes, excludes surrounding separators/whitespace, carries full blob and slice SHA-256, and joins exact repository/path/commit. No decoded-character offset or substring search substitutes for parser positions. |
| Canonical facts | One row per labelled declaration/edge. Meaning contains own attributes, not recursively duplicated child lists, so adding one field does not mark an unchanged owner/schema as changed. Default direction is materialized, and partition declared/undeclared state is explicit. Rows are sorted by stable structured identity. Citations are sorted by that same fact identity. JSON object keys are canonical, with no maps whose iteration order reaches encoded output. |
| Ordering and IDs | Primary/partition and index-column entries have stable own path identity with semantic position; indexes have own name identity with semantic owner position; view outputs have own name identity with semantic position. Table/field/association order does not establish identity. Association-pair and view-source identity is the exact structured edge, not its source array ordinal; source order is not semantic. Citations retain original pointers/positions but those do not participate in comparison. |
| Catalog projection | Declaration-only schema set → typed table/view → exact fields. Preserve own primary/partition/index metadata and explicit association/source edges without recursive traversal. It does not register relationship-only Workbench resources, operation→usage bindings, numeric protocol fields, or Contract Atlas service identities. |
| Missing artifact | Nil/absent input produces state `document-schema-artifact-missing` plus checklist `request-schema-export`, with no evidence rows. A zero-byte supplied artifact is malformed JSON. Request/adapter/capture workflow prose is not schema evidence. |
| Comparison | Two independently admitted explicit snapshots, same lineage and source class. Compare stable identity and semantic meaning only, excluding provenance/citations. Output additions/removals/changes in canonical identity order; preserve before/after exact provenance. The frozen example adds `new β`, removes `old`, changes `stable` type, and keeps `id` identity. No live-schema or cross-manifest/current-shape claim. |
| Public derivation | Pinned Apache CQL remains an external oracle; the admitted input is its separately hand-derived captured neutral JSON. All 61 facts/edges map to exact source spans. Set/list use `array_element` without ordering inference, map values expand the address definition/use, and wildcard view output expansion is explicit. Replication/comment/filter semantics are excluded, not guessed. |

### Refusal precedence and vocabulary

Refusal codes are bounded identifiers, not raw parser/source error strings.
An input has one deterministic first refusal; all diagnostics are source-free
apart from bounded JSON pointer, limit name, or exact declared owner metadata.
No decoder stack or source contents enter output.

1. Absent input yields the missing workflow state. Supplied bytes first check
   byte cap, context cap/shape, UTF-8 validity, then strict JSON tokenization:
   nesting depth, paired surrogate escapes, duplicate object keys, syntax,
   and a second/trailing non-whitespace value. Token-time errors follow byte
   order. Object-property payload prohibition is checked before unknown-key
   structural validation.
2. After complete strict tokenization, validate root shape and format,
   closed/member shapes, per-array and text/path bounds. Traversal follows
   source array order, object properties in a fixed schema order. Check
   aggregate declaration count before reference work. No type coercion.
3. Validate duplicate declarations/paths/edges, then exact references,
   partition-prefix semantics, and view DAG. Check the emitted-citation cap
   before publishing. Single-defect fixtures fix the typed outcome; cases
   with duplicate partition entries intentionally choose duplicate before
   prefix mismatch.

| Code | Meaning |
| --- | --- |
| `input_context_invalid` | Missing/blank/control context, invalid commit or unnormalized manifest path. |
| `invalid_utf8` | Supplied bytes are not valid UTF-8. |
| `invalid_unicode_scalar` | A JSON string has an unpaired surrogate escape. |
| `invalid_json` | Invalid JSON syntax or a non-JSON/CQL input. |
| `trailing_bytes` | Additional non-whitespace input after one JSON value. |
| `duplicate_key` | Repeated exact object key, before map decoding can discard it. |
| `data_payload_forbidden` | A forbidden payload property occurs at any object depth. |
| `unknown_key` | A closed object contains an unexpected property. |
| `unsupported_format` | Exact root format version is unsupported. |
| `invalid_shape` | Missing/wrong/null member, invalid enum/segment, malformed direction/prefix pair or numeric representation. |
| `invalid_text` | Empty/blank/control declared text. |
| `duplicate_declaration` | Repeated owner, field, key/column, index, association/pair, view output/source. |
| `unresolved_reference` | An exact referenced table/view/field/output is absent in this same manifest. |
| `invalid_partition_key` | Empty or non-prefix declared partition key. |
| `view_dependency_cycle` | A view self edge or directed view dependency cycle. |
| `limit_exceeded` | A named frozen parser/count/text/output bound is exceeded; no partial rows. |
| `comparison_lineage_mismatch` | Explicit snapshots do not share repository/path/format. |
| `comparison_source_class_mismatch` | Explicit snapshots have different mandatory source classes. |

### Bounds and cost budget

The authoritative numeric values are [bounds.json](bounds.json). Limits are
inclusive and belong to phebs's experiment, not a vendor/deployment.

| Resource | Bound |
| --- | --- |
| Supplied manifest bytes / JSON container depth | 65,536 / 32 |
| Tables / fields per table / typed path segments | 16 / 128 / 8 |
| UTF-8 bytes per name-kind-unit / type | 128 / 256 |
| Primary/partition width / columns per index / pairs per association | 8 / 8 / 8 |
| Indexes per owner / associations across all tables | 16 / 64 |
| Views / outputs per view / source refs per output | 16 / 128 / 16 |
| Aggregate declarations/edges during validation | 256 |
| Published facts and citations | 192 of each, one-to-one |
| Repository / manifest-path UTF-8 bytes | 256 / 256 |
| Opaque prefix numeric metadata | Positive signed-64-bit integer numeral |

Aggregate count includes the schema set, owners, fields/outputs, key entries,
indexes/columns, associations/pairs, and view sources. The 256 preflight cap
can refuse before reference validation; the stricter 192 publication cap
bounds the final complete output. A count-limited input is never truncated.
The 65,536-byte input bound also bounds token count and temporary AST storage.
Validation uses bounded maps/sets and a view DAG of at most 16 nodes; reference
lookups do not rescan the whole manifest for each edge. Graph traversal never
follows association cycles. No recursion exceeds the frozen input depth.

The spike performs one full bounded decode/validation plus canonical sort
per requested read. It holds that input, token/AST representation, at most
256 validation declarations, and at most 192 complete fact/citation pairs.
Citations store coordinates/digests, not duplicated source slices. All
strings/paths are independently bounded, so encoded fact/catalog/comparison
outputs are finite; the result record must measure actual encoded sizes and
allocation observations rather than claim an unmeasured heap/RSS ceiling.
Snapshot comparison holds two admitted results and at most their combined
384 identity entries; no live store or additional source is read. A demo
reads only the frozen results.

There are no shared locks, caches/invalidation, retry scheduler, sync tick,
publication transition, database, network request, child process, or runtime
worker. The spike runs cases serially and reinitializes result state on each
of two passes. Production request/startup/retry/no-op/publication costs are
unchanged because no production path imports this code. Measurements must
separate fixture-input/retained-output bytes and transient reader allocation
from any unsupported production latency, scale, or vendor claim.
