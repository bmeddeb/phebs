# T27.1 bounded reader outcome and ticket walkthrough

The isolated reader conforms to the frozen neutral fixture contract. This
supports proceeding with a separately specified production ticket; it adds
no production reader, schema, pack, route, registry, live-store access, or
release claim. The original [preregistration README](../README.md), decision
table, 119 cases and labels remain byte-for-byte unchanged at the independently
reviewed `e7be4d27` input checkpoint. These later outcome files are outside
that frozen input inventory.

[measurements.json](measurements.json) records two fresh-state passes over
every frozen case. Each accepted 14, refused 104 without partial rows, and
returned the one missing-artifact checklist state. All 534 facts and 534
citations in each pass matched preregistered fixture-author
meanings, UTF-8 offsets and slice hashes. The complete 61-row hand-derived
public mapping round-tripped to the pinned CQL and neutral JSON. Both passes'
canonical facts/citations/catalog/refusal bytes have SHA-256
`904465c775546e1c208fbdd4dc727dabfa5560ae7a9bc88c7022526096926577`.
Their explicit comparison bytes also match. No frozen label was changed to
match the implementation, and no panic occurred.

Independent implementation review subsequently found that the original
trailing-space check used Unicode whitespace and could admit non-JSON tails.
The corrected reader accepts only JSON's space, tab, carriage return and
line feed there. Eight supplemental controls (two accepted, six atomic
refusals) cover that correction outside the frozen input/label inventory.
[review-correction.json](review-correction.json) records the finding, exact
corrected source hashes and focused checks. The original measurements,
demo and first verification record remain preserved; the 119 frozen cases'
canonical output hash remains unchanged on the corrected reader.

These are fixture-conformance and determinism results. The fixture author
also implemented the reader; the lead independently reviewed the frozen
contract/public derivation. No blind human labels, independent extraction
accuracy, live/vendor schema validation, usage, deployment, migration, or
scale result is claimed. The reader trusts supplied source context; Git,
authorization, exact publication fencing and production integration remain
future work. Example repository/path/commit values are synthetic fixture
provenance, not a claim that a public repository contains this neutral JSON.

## Locate a table, inspect a field/key, copy its exact citation

Open [demo.json](demo.json). Its `public_captured_catalog` explicitly says
`source_class: captured`, meaning only the supplied export's committed
declarations. `catalog.tables` locates `reservation.guests`; its `fields`
contain the own typed path:

```json
[
  {"kind":"field","name":"addresses"},
  {"kind":"map_value"},
  {"kind":"field","name":"street"}
]
```

Its exact opaque type is `text`. This path traverses a declared map value;
literal field names are never split on punctuation. In the same table,
the `DECLARES_PRIMARY_KEY` and `DECLARES_PARTITION_KEY` declarations both
name `guest_id` at position 0; primary direction is the explicit neutral
default `asc`, and partition entries carry no direction. For the composite
example, `reservation.reservations_by_hotel_date` orders primary paths as
`hotel_id`, `start_date`, `room_number`, and declares the first two as its
partition prefix. This is committed structural metadata, not a runtime
partition or ordering guarantee.

The nested street field's fact ID is
`c00d1f58f089e00cf60732a67a081d057ba3c45904a04b4f94ccbe29b54feed6`.
Join that exact ID to `public_captured_catalog.citations` to copy:

```text
repository: example.invalid/neutral/schema
manifest_path: schema/document.json
commit: 1111111111111111111111111111111111111111
blob_sha256: b3b71a23e40c87603616e746d8245d7579f65c6d7d28b95fe2946ba43946cd57
pointer: /tables/2/fields/11
UTF-8 byte span: [6016, 6332)
slice_sha256: 548f167bc4f845f17576cb252b43662a7a5e0a3e4db0271056c7bd19b9d84ba6
```

For this neutral experiment that logical manifest is
[fixtures/positive/public-captured.json](../fixtures/positive/public-captured.json).
The exact bytes at that span form the full field object. The corresponding
row in [public/derivation.json](../public/derivation.json) cites both the
public `street text` UDT definition and the `addresses map<...>` use. The
reader consumes the neutral JSON only; it never parses or executes CQL.

The `authored_associations_and_views` example has `source_class: authored`.
Its `Orders` table declares distinct `purchaser` and `billing` associations
to `客户`, plus a self edge, and the reverse table edge forms an allowed
cycle. They remain displayed declared edges, not recursively traversed
foreign keys. Its `Summary` view owns ordered outputs and source edges;
an output named `[]` has zero sources, while `customer` has two explicit
table sources. View keys/indexes bind those own outputs. No view freshness
or deployment is implied.

## Missing artifact and explicit snapshot difference

`demo.json.missing_artifact` shows
`state: document-schema-artifact-missing`,
`checklist: request-schema-export`, and zero facts, citations and catalog
rows. The ticket action is to request a schema-only export in the frozen
neutral format and place it immutably in an indexed repository. Human
capture/adapter instructions are workflow prose, not schema evidence. A
missing export does not trigger a service connection or client-code inference.

`demo.json.explicit_comparison` retains two explicit source commits and blob
digests. Stable identity excludes that provenance. Its three differences are:

| Declaration in table `Queue` | Difference |
| --- | --- |
| Field `new β` | Added |
| Field `old` | Removed |
| Field `stable` | Opaque type changed from `opaque:string` to `opaque:bytes` |

Field `id` keeps its stable identity and has no difference row. The comparison
refuses a different lineage or mandatory source class; neither snapshot is
called live or current. Keys/columns/output positions remain semantic, while
object property order and table array order do not invent differences.

## Bounded checks and cost

Run the isolated package only:

```sh
go test ./spike/t271 -count=1 -timeout=30s
go test -race ./spike/t271 -count=1 -timeout=30s
go vet ./spike/t271
go test ./spike/t271 -run '^$' -bench '^BenchmarkReadPublicCaptured$' \
  -benchtime=10x -count=1 -timeout=30s
```

For an explicitly requested fresh measurement file, run
`PHEBS_T271_RESULTS=results/measurements.json go test ./spike/t271 -run '^TestFrozenCasesTwice$' -count=1 -timeout=30s`.
It also writes `results/demo.json`; it never regenerates frozen inputs or
labels. Default tests write neither result file.

On Go 1.26.5 / darwin arm64 / Apple M4, the measured two-pass run consumed
473,393 fixture bytes per pass and encoded 488,162 result bytes per pass;
the largest individual result was 164,567 bytes. The serial per-call
process-allocation observations totalled 6,831,528 and 6,796,464 bytes per
pass, with a largest single observed delta of 942,056 bytes. These include
possible Go runtime activity and are neither retained heap nor peak RSS.
Ten public-fixture benchmark iterations observed 302,967 ns/op,
305,212 B/op and 5,707 allocs/op. These mechanics samples establish no
production latency, heap or scale guarantee.

Each requested read checks the 65,536-byte cap before decoding; builds one
bounded AST (depth 32), owner/path lookup maps and at most 256 pending
declarations; validates all shapes, references and the at-most-16-node view
DAG; then publishes at most 192 fact/citation pairs. Association edges are
never traversed. Canonical sorting is over at most 192 fixed-length IDs;
catalog construction makes one pass over those facts and sorts at most 16
owners in each namespace. There is no repeated full-manifest reference scan.

An accepted read hashes the full input once for provenance and the exact
cited value for each fact. Citable values nest at most four deep (schema,
owner, declaration, edge), so slice hashing processes at most four input
lengths in aggregate, in addition to that first full-input hash. Structured
stable-ID encoding/hashing is bounded by the declaration and string/path
caps. Citations retain coordinates/digests, not raw source slices. The
result holds owned rendered meanings and cloned bounded source-context
strings; it retains no AST or raw input buffer. There are no caches or
invalidation, held shared locks, workers, retries, sync ticks, database,
network calls or child processes.

The frozen counts/text/path bounds also give a conservative serialization
ceiling without a new admission rule: one fact fits within 32 KiB (two paths
of at most eight 128-byte names, worst JSON escaping, opaque attributes and
bounded owner metadata), and one citation fits within 4 KiB. Facts occur
once in the fact list and at most once more in the catalog. At most 192 of
each, 32 bounded owner headers and fixed context/punctuation fit below 16 MiB
encoded per complete result. Explicit comparison has at most 384 combined
before/after fact rows and also fits below 16 MiB encoded. This is an
analytical encoding budget, not a measured memory/RSS bound or a vendor
limit. Temporary AST/decoder/maps and encoder allocations are additional.

Comparison holds two admitted results, builds bounded identity maps and
compares semantic meanings; it performs no source fetch/hash and preserves
both snapshots' provenance. The test harness retains its frozen-case
results while comparing two passes; that test-only aggregate cost is
separate from one reader request. Production startup/request/sync/no-op/
retry/publication costs remain unchanged because production imports none of
this experiment. No schema or persisted state was added.
