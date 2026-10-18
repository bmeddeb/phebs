# T28.1 Redis evidence spike — source preparation

This is a complete proposed source-only input freeze awaiting independent lead
review and an immutable checkpoint. No Go parser, Redis recognizer, candidate
output or score exists. The all-ticket request schedules this previously
unscheduled spike, and Ben routes its backend work to the Mac.

[license-receipt.json](license-receipt.json) verifies permissive licenses before
any application or command-metadata source read. The exact notices remain in
`licenses/`. Only Redis 7.2.15 commit
`316753259b4db132cf494292a1b3a702d9e9ddb2` command metadata is admitted:
its BSD-3-Clause COPYING blob and 392 JSON files match the BACKLOG tree pin.
Three public ACL fixture files at that same pin are separate safety inputs.
No newer Redis source or module implementation/metadata was fetched.

[source-scope.json](source-scope.json) records whole-file selection before any
recognizer: all committed Go/module/sum files outside vendor for the pinned
MIT Asynq and Apache-2.0 Eino example repositories, plus the exact Redis
metadata/ACL paths. [source-receipt.json](source-receipt.json) binds every one
of the 793 files / 3,600,148 source bytes to its Git blob, byte length and
SHA-256. Three compressed corpus archives retain those exact permitted bytes;
the reader must validate bounded member paths/sizes/hashes before use and
never execute application source or connect to Redis.

Asynq's root module declares go-redis/v9 v9.14.1; Eino's root declares v9.17.2.
Their retained nested tools/x/quickstart modules also contain v9.7.0. Those
bytes remain in the corpus census; the proposed two-version adapter does not
inherit a parent module's version. Unsupported nested-module client versions
must retain explicit unknown-client-adapter outcomes rather than being
silently dropped or claimed supported.

The owning BACKLOG ticket is the acceptance bar. Documentation-derived module
subsets remain separate from pinned core metadata. All counts below are
independent source expectations, not observed candidate success, coverage,
precision, production registration, runtime existence, accuracy or scale.

Official semantic references reviewed for this freeze:
[key specifications](https://redis.io/docs/latest/develop/reference/key-specs/),
[FT.CREATE](https://redis.io/docs/latest/commands/ft.create/),
[TS.CREATE](https://redis.io/docs/latest/commands/ts.create/),
[TS.CREATERULE](https://redis.io/docs/latest/commands/ts.createrule/) and
[ACL SETUSER](https://redis.io/docs/latest/commands/acl-setuser/).
These reference pages are not vendored module code or metadata.

Cost is bounded one-shot input download/materialization/hash verification only.
No production request, startup, sync, retry/no-op, publication, lock, cache,
worker, schema, disk state or child-process behavior changes.

## Frozen populations and source oracle

`sampling.json` v2 fixes a purposive seven-complete-file usage sample:
Asynq's `internal/rdb/rdb.go` and `internal/rdb/inspect.go`, and all five Eino
files importing go-redis/v9. They total 136,115 bytes. The original random draft
is retained under `preparation/` as non-authoritative history: its Eino files
mostly lack Redis use. Selection precedes candidate output and makes no
probability/population-accuracy claim. Every other scoped file still enters
source-class, bounds and panic-safety traversal; it has no inferred quality
denominator.

The complete known native declaration census is separate from that sample.
`source-literal-census.json` retains the 380-file Go literal/frontier search and all 13 explicitly reviewed occurrences. Whole-scope literal/name/raw-call source inspection identifies two FT.CREATE
vectors and no TS.CREATE, TS.CREATERULE or XGROUP CREATE spelling. Both vector
files were reviewed completely. Both belong to nested v9.7.0, not root v9.17.2.
One index name is a same-file constant format; the other name/prefix is runtime
configuration. Both vectors contain runtime dimension values and fail atomic
full-schema shape proof independently of the version gate. The six Redis Do
sites are those two CREATE calls and four INFO calls; five other Do occurrences
are synchronization helpers. There is no unidentified dynamic raw Do vector in
this finite scope. Lua strings and unproved application/script wrappers are
excluded from declaration discovery, not claimed declaration-free.

| Separate denominator | Source expectation |
|---|---:|
| Known native declaration sites |2 indexes;0 series/rules/groups |
| Statically named / runtime-named native resources |1 /1 |
| Atomically complete native vectors / supported exact-version sites |0 /0 |
| Public ACL files / username occurrences / username-pattern pairs |3 /7 /7 |
| Selected command/frontier sites |99 |
| Core API / opaque script wrapper / unsupported-version / extension wrapper sites |37 /52 /8 /2 |
| Known key-argument expressions |30 |
| Provably resolved key spellings/structures |0 |
| Position-specific access-mode opportunities / expected resolved modes |30 /30 |
| Expected-kind opportunities / known / generic unknown |30 /27 /3 |
| Explicit script-key pass-through opportunities |0, unmeasured;52 wrapper cardinalities remain unmeasured |
| Uniquely bindable spelling/family opportunities |0, unmeasured |

Report every row separately by population and frozen shape class. Unknown
script-key cardinality is never zero keys. A command/mode/kind observation may
identify a source argument position while its bytes remain unresolved; it is
not a concrete key usage/resource fact. In particular, imported constants and
helpers stay outside same-file constant evaluation. No script body contributes
read/write/kind or hidden native declaration facts. Sixty separate
authored source controls plus two input-role/missing-artifact admission controls exercise positive literal/format/native/script/ACL cases and
every refusal class; never blend them with public populations.

`source-review-notes.json` retains the earlier non-authoritative source-preparation notes; the final sample and oracle below supersede its pending-work statements.

`labels/` retains complete safe ACL/native labels and all selected usage-site
choices. Authors explicitly chose each path, line, method and argument; the
`author_*` and `label_literals.py` helpers only locate/check those literals and
serialize them. No source grammar or candidate produced the labels. `audit_inputs.py` verifies original Git/SHA bytes, nearest modules and every chosen token range without a recognizer. Exact
source token ranges/hashes, source commits and nearest-module receipts remain
independent of the later evaluator Git/binary identity.

## Frozen decision table

| Question | Decision or explicit limit |
|---|---|
| Pure reader | Committed Go/ACL bytes and pinned core metadata only. No Redis, miniredis, application, client, script, sqlc or server execution; no connections or data captures. |
| Command vectors | Literal direct Do arguments and same-file immutable ordered arrays; fixed client API mappings below. Reject reassigned/mutated arrays, unknown commands or arbitrary application wrappers. A fixed vector may contain opaque non-key operands only while all emitted key positions and selected flags remain proved; unknown option branches retain gaps. Command/data operands remain distinct. |
| Receiver and version | The closed lexical/import/module/struct-field contract in adapter-contract.json resolves exact declaration objects, not spellings. Shadowed, reassigned, duplicate/ambiguous fields/types/imports or unproved aliases abstain as unknown-client-adapter. Only exact nearest-module v9.14.1/v9.17.2; one bounded metadata pass and indexed lookups, no per-call corpus scan or new SSA. |
| Adapter contract | `adapter-contract.json` retains 56 exact-method source witnesses plus 8 command-helper and 10 receiver type/constructor witnesses at the two immutable Go client commits. BSD2 licenses precede source reads. No Go client code is imported/linked/executed and no Redis module implementation/metadata is copied. No CamelCase guessing or other-major claim. |
| API branches | Set never adds GET; expiry options do not shift its key. SetNX is admitted only for literal expiration 0, which maps to SETNX. Other expiry branches abstain. ZAdd/ZAddXX retain fixed key position through reviewed exact helper chains. |
| Script wrappers | Script.Run may fall back EVALSHA→EVAL; it and application runScript helpers remain opaque-helper. Their argument-list cardinality is unmeasured. |
| Key specs | Use only 392 pinned JSONs. Retain full container+subcommand vector indices. Support bounded index/keyword begin_search and range/keynum find_keys; literal counts and branch keywords must prove positions. Negative offsets refer to the admitted full vector. `not_key` never becomes a data-key fact. |
| Incomplete specs | Fixed proven positions/flags may survive; omitted/unknown positions remain incomplete-key-specification. SORT's known primary key does not prove BY/GET/STORE-derived keys. Never claim an empty key list from an unknown spec. |
| Access modes | Preserve pinned RO/RW/OW/RM plus exact uppercase ACCESS/UPDATE/INSERT/DELETE flags, not generic read/write. SET GET resolves RW/ACCESS/UPDATE; proven absence of GET resolves OW/UPDATE. Other unproved variable_flags abstain as variable-access-mode, retaining independent key/kind evidence. |
| Expected kind | `expected-kinds.json` is separately hand-authored from official command docs, never inferred from key specs or runtime. Generic/missing/argument-dependent entries yield unknown-expected-value-kind. SET expresses intended new string value, not prior type. |
| Script keys | Only explicit Eval/EvalSha/FCall/raw EVAL/FCALL fixed key lists emit PASSES_REDIS_KEY_TO_SCRIPT. Do not inherit generic conservative metadata access flags or value kind, and never analyze script/function bodies. |
| Key bytes/identity | Exact binary-safe bytes (base64 in JSON) and structured format segments; never Unicode/global lowercase. Identity is repository+nearest module/client lane+key bytes/structure; commit/blob/citations are separate provenance. No runtime key existence or type. |
| Formats | At most 8 simple typed parameters, 32 segments and 256 format bytes; only %s/%d/%%, same-file immutable constants and bounded literal concatenation. Dynamic concatenation, arbitrary helper calls and unknown format specifiers abstain. A structure is not a concrete key. |
| Hash tags | Inspect bytes: first `{`, then first following `}`; use interior only if nonempty, otherwise whole key. Do not skip an empty first pair. A format parameter can contain braces, so grouping may remain argument-dependent. No cluster or slot-existence claim. |
| Native declarations | FT.CREATE names an index and indexed projection only. TS.CREATE names a series; TS.CREATERULE names an explicit resource relation. XGROUP CREATE names a group on a stream. None declares complete values/entry fields. |
| Native atomicity | All identity, prefix, schema/options/count and referenced-resource components must be proved within the admitted subset. Any unknown/malformed component emits zero declaration/schema/reference facts for that vector. Separately proven core usage can survive under its own fences. |
| Named module subset | native-subset.json closes literal arity/order/counts, duplicate/refusal rules and numeric spelling/ranges before recognition. FT has 1..8 prefixes and 1..32 fields; VECTOR is FLAT/FLOAT32 only, exactly6 attribute operands for unique TYPE/DIM/DISTANCE_METRIC, DIM1..4096, metric L2/IP/COSINE; HNSW remains unsupported here. TS retains only explicit bounded retention/labels and avg/sum rules with positive bounded buckets. Unknown/trailing/duplicate/outside-subset literal shapes refuse atomically as unsupported-module-command. All numeric maxima are engineering admission, not vendor limits. No module metadata or code. |
| ACL semantics | Authorization intent only. Emit exact username and ~/%R~/%W~/%RW~ token bytes/spans; no authentication/deployment/key existence, effective grants or complete user-state claim. Selector structure may be recorded without retaining command grants. |
| ACL safety | Token-streaming discard of credentials/grants, no line context or decoder errors containing source. Facts/schema cannot hold arbitrary raw ACL text. Zero credential-origin citations. Scan all candidate outputs/diagnostics/logs for full credential tokens; distinct-secret controls additionally scan payloads. |
| ACL collision | Two public credential payloads equal permitted usernames. A literal payload-substring ban would prohibit those allowed username facts. Permit only their exact username-token provenance; full credential tokens and credential spans/context remain forbidden. No credential token or credential hash enters labels. |
| Bindings | Concrete spellings bind by exact bytes; known format structures stay structured. Native prefix projections may supply candidate families only, never a universal value schema. Multiple matching candidates refuse ambiguous-key-family; no precedence guessing. |
| Object mapping | No model-class reader. The two Eino extension constructors are opaque wrappers, not interpreted ORM declarations. Go-only scope does not measure other-language model classes; feasibility verdict is defer/no reader, without claiming those artifacts do not exist elsewhere. |
| Structural failure | The semantic unresolved vocabulary stays exact in bounds.json. Malformed-input, resource-limit, data-bearing-input and artifact-missing are separately frozen boundary refusals; zero blob facts/catalog changes, no source-bearing diagnostics. Process stop/panic is execution failure, not a quality gap. |
| Neutral manifest | No phebs-redis-keyspace-v1 schema/adapter/fixture is created here. Only a later separately owned requested-artifact design may follow the criterion below. No production pack or registration. |

## Prospective source-review correction before recognition

The earlier source-only proposal is retained exactly in
`preparation/source-draft-contracts-85c3ac8a.tar.gz`, including its original
preregistration digest `85c3ac8a8e6b1e2b963f97cf7b77b3ef45d27e404d1017fa925561cf714fc770`.
It had no parser/recognizer output. This correction closes the lexical receiver
and named native option grammar contracts before any implementation outcome;
it changes no archived public byte, public label or public denominator.

`receiver-source-review.json` retains explicit source re-review of all37 API
sites:34 use the unique same-package RDB.client UniversalClient field,1 uses an
immutable Pipeline() local, and2 use RedisStore.cli *redis.Client. The nearest
module/import and selected lexical root remain unambiguous, without a write or
shadow at those sites. No public oracle mislabel was found. Exact client
UniversalClient/Cmdable/Pipeliner/NewClient/Pipeline signatures are independently
hashed source witnesses, not inferred constructor aliases.

The bounded metadata key is nearest-module+directory+package+named-type+field,
with per-file import objects and per-block lexical declaration identities.
All retained metadata shares16,384 records/8MiB; each lookup probes at most64
lexical/ancestor entries. Reassigning a selected binding anywhere in its
function conservatively invalidates it, including earlier uses; no SSA or
cross-function alias/runtime inference is added. Client-specific replace or
conflicting require metadata abstains. This static committed-module recipe
makes no claim about external workspace/vendor/environment resolution.

`native-subset.json` fixes the complete tiny FT/TS literal subset and atomic
failure priorities. The additional17 inert controls cover shadowed imports and
locals, reassignment, ambiguous fields, unproved copies, one exact constructor,
closed FLAT vector enums/counts/duplicates/dimensions, unsupported HNSW and
TS aggregator/bucket/labels/alignment/retention refusals. The original43 fixture
bytes and expectations remain exact; additions never increase public quality
populations. The known2 public FT vectors retain unknown-client-adapter and
zero admitted/atomic facts; runtime schema uncertainty remains separate.

## Manifest decision criterion frozen before results

The headline is complete atomic/native-identity declarations versus the full
known native-site denominator, with resource-name, structural and version gaps
reported on separate axes. A **GO only to a later requested-artifact design**
requires at least one independently labelled public structural declaration gap
that would persist with a correctly admitted client adapter; unsupported
versions, unimplemented recognizers, opaque helpers or zero usage samples alone
do not justify a new manifest. Both public FT vectors have source-observed
runtime schema arguments, so the structural expectation is explicit before
measurement; candidate recognition and atomically emitted facts must still be
reported honestly. No GO designs or registers a manifest in this spike. If the
remaining evidence is solely adapter/recognizer limits, record NO-GO/inconclusive.

## Bounds, cost and evaluation contract

`bounds.json` freezes all input/token/depth/AST/vector/key/format/ACL/metadata/
fact/output/identity limits and all refusal classes. Archive members must match
the exact bounded path/size/Git/SHA receipts; no extract-to-disk traversal or
symlink. Preflight source bounds precede Go AST parsing. Credential material is
never put in an AST/citation cache. One bounded blob is active at a time; retain
only bounded type/constant/identity metadata between passes, not raw source or
AST. No persistent cache, worker, lock or publication behavior changes.

After independent immutable input review, a separate evaluator supplies only
stripped source/module/control metadata to the candidate. It scores complete
emissions against these labels, counting all missing and unexpected witnesses,
all separate denominators, refusal/shape classes and unmeasured cardinalities.
Every token span must round-trip to its original immutable bytes. ACL output
schema/provenance and byte scans are mandatory, including stderr/logs, and must
never print offending tokens on failure. Freeze output schemas before execution.

Run twice from fresh state in a joined child under 30 seconds per complete pass,
with a 1 GiB observed-RSS stop sampled every 20 ms; this is not a hard heap bound and
can overshoot. The 4 MiB per-blob encoded cap is checked after encoding and is not
an allocation guarantee. Retained batch output is bounded separately at 32 MiB;
do not silently keep arbitrary complete results. Record build/binary/dependency
cost, wall, exact/observed RSS, output bytes, tool/evaluator source identities and
joined teardown independently of sorted deterministic output. Preserve first
stops and both bytes. No database, application, native renderer, production
dependency or long operating workload is needed for this isolated spike.

Official docs were reviewed on 2026-10-18. The source of core flags remains the
exact pinned BSD metadata, not the latest website. The named module rules are
small phebs-authored documentation interpretations, without copying website
metadata or module source. Hash-tag semantics follow the
[cluster specification](https://redis.io/docs/latest/operate/oss_and_stack/reference/cluster-spec/#hash-tags).
Expected kind references remain separately linked in `expected-kinds.json`.
