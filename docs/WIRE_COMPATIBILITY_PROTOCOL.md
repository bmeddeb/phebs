# T53.3 Buf WIRE compatibility quality protocol

Status: **sealed and scored: the round fails** (2026-10-12). Ben froze the
§6 thresholds as proposed and named Claude as the sole adjudicator. The
results are in §8.

## 1. Claim card (`phebs.protobuf.wire.buf`)

**Claim.** `check_wire_compatibility` (HTTP `POST
/api/check_wire_compatibility`, MCP tool of the same name) runs the pinned
Buf 1.72.0 `WIRE` breaking policy over the before/after `.proto` source sets
the caller supplies. It returns that verdict, its violations, affected field
identities, both input digests and run provenance. The verdict covers exactly
those bytes.

**Non-claims.**
- Runtime, deployment, application or semantic compatibility.
- JSON and text formats (`WIRE_JSON` is not run).
- Thrift.
- Imports the caller did not supply.
- Consumer impact: no field-reference evidence is joined.
- Completeness of any repository's contract history.

**Supported input.** A compilable closure within the checker's frozen
ceilings: 256 files per side, 4 MiB per file, 32 MiB per side. Plus the
preflight token and depth limits. A source that does not compile is
refused, never judged.

**Known limit (disclosed, pinned).** Buf reports every singular↔repeated
change as breaking. Protobuf documents string, bytes and message
singular↔repeated as wire-compatible. The error is conservative only.

## 2. Population

The population is real `.proto` history from the frozen T47.2 corpus
(containerd, etcd, grpc-go), at the derived commits in
`spike/t472/corpus.lock.json`.

- `spike/t533/enumerate_changes.py` lists every modified, non-vendored
  `.proto` file in each first-parent commit (1,015 units).
- Each side is the file plus its repository-local import closure. Every
  file is supplied under the exact name it is imported by.
- 571 units are `closed`. These are the judged population.
- The other 444 keep their state and carry no request, because the caller
  must supply a compilable closure:
  - 353 import a dependency absent from the repository (gogoproto,
    `google/api`, openapiv2);
  - 51 have an ambiguous import;
  - 40 moved the changed file's import name between sides.

The frozen files are `spike/t533/population/{units.jsonl.gz,population.json}`.
Only blob ids and content digests are recorded. The runner re-reads content
from full-history clones and refuses any blob whose digest differs.

## 3. Truth

`spike/t533/wire_oracle.py` is an independent reader and rule set. It uses
no Buf and no phebs code or output, only T49.1b's independent lexer.

- **Rules.** They come from protobuf's documented update rules (protobuf.dev
  "Updating A Message Type") and the purposes the T53.1 goldens recorded.
  The rules cover:
  - package changes;
  - unreserved field and enum-number deletion;
  - wire-type compatibility groups, with `sint*` as zigzag;
  - singular↔repeated by element kind and packing;
  - unsafe oneof moves;
  - proto2 defaults;
  - required-field sets;
  - dropped reserved numbers or names;
  - rpc request, response, streaming and idempotency changes.
- **Outcomes.** Each finding is `break` or `undetermined`. Undetermined
  covers what neither source fixes: a different message or enum type,
  same-wire-type value reinterpretation, presence or syntax changes,
  editions, changed extensions, and map↔repeated. A source the reader
  cannot read is `unreadable`, and its truth is that the product must
  refuse it.
- **Fences.** The oracle reproduces all 29 T53.1 golden truths.

Before the seal, the oracle was only ever run over the population to find
reader defects. Its pre-seal distribution over the 571 units is 486
compatible, 52 breaking, 24 undetermined and 9 unreadable. Buf has not seen
the population.

## 4. Adjudication

Claude, the sole adjudicator Ben named, adjudicates by reading both sides' sources. They judge every
`undetermined` unit, every unit where the product disagrees with the oracle
(outside the pinned class), and every product refusal of a readable unit.

The adjudication file maps `unit_id` to `{truth, note}`, where truth is
`compatible`, `breaking` or `unreadable`. An adjudicated truth replaces the
oracle's and decides the class outright. A permissive disagreement is never
scored on the oracle alone: it is adjudicated first.

## 5. Measurement

`run_round.py run` starts the real `phebs serve` with
`provisional_proto_extraction`, which is the only admission of the standalone
verdict until release. It requires `wire-compatibility` in `/api/version`.
It sends every closed unit to HTTP and to MCP, and records the outcome and
HTTP/MCP parity. HTTP's `$schema` link is the only field excluded from
parity. `run_round.py score` classifies each unit:

| Class | Meaning |
|---|---|
| `agree` | Same verdict, or a refusal of an unreadable source. |
| `pinned_conservative` | Buf breaks only by `FIELD_WIRE_COMPATIBLE_CARDINALITY`, and the oracle finds no break. Disclosed, not counted against agreement. |
| `conservative` | Adjudicated: the product says breaking, the truth says compatible. |
| `permissive` | Adjudicated: the product says compatible, the truth says breaking. |
| `disagree` | Adjudicated other mismatch: a refusal against a verdict, or the reverse. |
| `pending` | Not yet adjudicated. |

## 6. Thresholds (frozen by Ben, 2026-10-12)

- `permissive` = 0. One confirmed permissive verdict fails the round.
- `pending` = 0. Every undetermined truth and every disagreement is
  adjudicated.
- Agreement ≥ 99.0% over judged units, excluding `pinned_conservative`.
- HTTP/MCP parity holds on every unit.

## 7. Seal

The seal publishes, in a public gist created before `run`:

- the commit holding this protocol;
- the population digests (`population.json` `units_sha256`);
- the `sha256` of `enumerate_changes.py`, `wire_oracle.py` and `run_round.py`;
- the frozen thresholds.

There is no sampling, so no beacon draw is needed. The whole closed
population is scored.

## 8. Result (2026-10-12)

**Seal.** Commit `d88a6c97` was pushed to the public repository. The seal
values were published at
<https://gist.github.com/Blandt/74dda9b6e7d8a90df884fefb91d4813f> before the
run. The receipts are in `spike/t533/round/`.

**Run.** The real binary, built at the seal commit, judged all 571 closed
units in 2 m 1 s:

- 486 compatible, 73 breaking (HTTP 200) and 12 refused (HTTP 400);
- HTTP/MCP parity held on all 571 units;
- peak RSS was 118 MiB, and the server stopped cleanly.

**Scored (sealed binary): fails.**

| Class | Units |
|---|---|
| agree | 548 |
| pinned_conservative | 3 |
| conservative | 11 |
| disagree | 9 |
| permissive | 0 |
| pending | 0 |

Agreement is **96.48%** (548/568), against the 99.0% threshold. The zero
permissive, zero pending and parity thresholds pass.

1. **9 disagreements are a product defect.** Buf reports a source that fails
   to compile semantically (an unresolved type, or an unknown option
   extension) as a `COMPILE` annotation with its breaking exit status 100.
   The checker passed that through as a 200 "breaking" verdict on a source
   that does not compile. The claim forbids this: it must be refused.
   - The sources: 6 etcd `Documentation/rfc/v3api.proto` revisions
     (`storagepb.KeyValue` never imported), 2 containerd files using gogo
     options with no imports, and 1 containerd `shim.proto` whose rpc types
     were never imported.
   - The last was first scored `agree` by coincidence: the oracle does not
     validate rpc type names. It was adjudicated after scoring, once the fix
     exposed it.
2. **11 conservative errors are Buf comparing message types by name.**
   `FIELD_WIRE_COMPATIBLE_TYPE` fires when a field's message type moves to a
   differently named message with an identical wire structure, checked field
   by field and recursively.
   - 7 units: containerd `containerd.v1.types.*` → `containerd.types.*`, the
     containerd ttrpc `Envelope`, and grpc-go's `grpc.lb.v1.Timestamp` and
     `Duration` → the well-known types.
   - 4 units are a judgment call: containerd `map<string,string> options` →
     `google.protobuf.Any`, which is wire-equivalent field by field. Even
     adjudicated as breaking, agreement stays below 99% (98.77% after the
     fix).

Every adjudication note is in `round/adjudications.json`. The 13 adjudicated
agreements include real breaks: renumbered fields, `uint32` → map, and
string ↔ message exchanges.

**Fix and diagnostic (not a sealed result).** The checker now refuses any
`COMPILE` annotation as invalid input, and `TestGoldenUncompilableSourceIsRefusedNotJudged`
covers semantic failures on both sides. A diagnostic rerun of the fixed binary
over the same, now-seen population scores 557 agree, 11 conservative and 3
pinned: **98.06%**. It still fails, and only on the Buf type-name class.

**Consequence.** The standalone WIRE verdict does not meet the frozen bar, so
it stays experimental-dark. Its safety property held throughout: no unit was
called compatible when it breaks. Any new claim needs a new protocol and a
fresh population, because this population has now been seen.

## 9. Round 2: the narrower claim (sealed 2026-10-12; result in §10)

Ben chose the narrower claim and a fresh population after round 1 failed.
Claude remains the sole adjudicator.

**Claim card, revised (`phebs.protobuf.wire.buf`).** For a supported input,
`check_wire_compatibility` never reports a change that breaks protobuf wire
compatibility as compatible. A "breaking" verdict is conservative in two
disclosed classes; outside them, it agrees with the wire truth at least 99% of
the time:

1. string, bytes or message singular↔repeated
   (`FIELD_WIRE_COMPATIBLE_CARDINALITY`);
2. a field whose message type changes name while keeping an identical wire
   layout, compared field by field and recursively, with a map counting as
   its repeated entry message (`FIELD_WIRE_COMPATIBLE_TYPE`).

A source that does not compile is refused, never judged. The non-claims of
§1 stand.

**Population (fresh).** The population covers vitess and istio. Both are in
`spike/t472/corpus.lock.json`, pinned at their selection commits
(`0ee52554`, `d501e135`), and neither was seen in round 1. They are read from
full-history bare clones in `phebs-rehearsals/t533r2-history-20261012`.

- `enumerate_changes.py --pinned` yields 1,911 units, 1,525 of them closed:
  952 vitess and 573 istio.
- istio's vendored `common-protos/` joined the excluded third-party
  prefixes. Round 1's population is byte-identical under the change.

The frozen files are `spike/t533/population-r2/`.

**Truth (revised oracle).** The oracle closes round 1's three gaps:

- rpc request and response types must resolve;
- custom options must resolve to a declared extension;
- message type changes are decided by wire layout: equivalent is compatible
  with a `disclosed_type_equivalent` note, a conflicting shared number is
  breaking, and added or removed numbers are undetermined. string ↔ message
  is breaking, which was round 1's adjudication rule.

Singular↔repeated of string, bytes and message carries a
`disclosed_cardinality` note. The revised oracle passes these checks:

- it reproduces all 29 goldens;
- it matches round 1's Buf refusals exactly (12/12);
- it contradicts none of round 1's 33 adjudications.

Its pre-seal distribution over the fresh 1,525 units is 1,409 compatible, 100
breaking, 16 undetermined and 0 unreadable. Buf has not seen these units.

**Classes (`run_round.py`, schema v2).**

| Class | Meaning |
|---|---|
| `agree` | Same verdict, or a refusal of an unreadable source. |
| `disclosed_conservative` | The product says breaking and the truth says compatible; Buf's rules lie within the two disclosed rules and the truth carries a disclosed note. |
| `conservative` | Any other breaking-versus-compatible disagreement. |
| `refused_readable` | The product refused a compilable source. |
| `judged_unreadable` | The product judged a source that does not compile. |
| `permissive` | The product said compatible, but the change breaks. |
| `pending` | Not yet adjudicated. |

A disagreement the oracle decided alone, and every undetermined truth, is
adjudicated before it counts.

**Thresholds (frozen for the narrower claim).**

| Check | Bar |
|---|---|
| `permissive` | 0 |
| `judged_unreadable` | 0 |
| `pending` | 0 |
| (`conservative` + `refused_readable`) / judged units | ≤ 1.0% |
| breaking-truth units judged | ≥ 50 |
| HTTP/MCP parity | on every unit |

The score reports the rule-of-three 95% upper bound on the permissive rate,
3/n over the n real breaks. Disclosed conservative units are counted and
published, but they are not errors under this claim.

**Seal.** The round-2 commit, the `population-r2` digest, the three tool
digests and these thresholds go in a public gist before `run`.

## 10. Round 2 result (2026-10-12): passes

**Seal.** Commit `7484c0c4` was pushed to the public repository, and the seal
values were published at
<https://gist.github.com/Blandt/f2becbe05317959b48800dd8c07f2931> before the
run. The receipts are in `spike/t533/round2/`.

**Run.** The real binary, built at the seal commit, judged all 1,525 closed
units in 7 m 31 s:

- 1,362 compatible and 163 breaking, all HTTP 200, with no refusals;
- HTTP/MCP parity held on every unit;
- peak RSS was 121 MiB, and the server stopped cleanly.

**Adjudication.** Claude judged 18 units: 16 undetermined truths plus 2
oracle disagreements. Buf's violation locations were read for the units
mixing disclosed and undisclosed changes, from a diagnostic re-query of the
same sealed binary on those units only. Each note is in
`round2/adjudications.json`.

**Score.**

| Class | Units |
|---|---|
| agree | 1,466 |
| disclosed_conservative | 54 |
| conservative | 5 |
| permissive | 0 |
| judged_unreadable | 0 |
| pending | 0 |

- Real breaks judged: 104. With none called compatible, the rule-of-three
  95% upper bound on the permissive rate is 2.9%.
- Undisclosed error rate: **0.33%** (5/1,525), against ≤ 1.0%.
- Parity: every unit.
- **Every frozen check passes.**

**Post-seal scoring correction (disclosed).** As sealed, `score` still let
the oracle's `disclosed_cardinality` note count for an adjudicated unit.
Unit istio `2e445533…:workload.proto` was adjudicated as undisclosed (an
enum rename plus bytes↔message), but scored as disclosed. That contradicts
§4 and §9, where an adjudication decides the unit outright.

`run_round.py` now ignores oracle notes on adjudicated units, and a test pins
this. The sealed scorer's output is kept as `round2/score-as-sealed.json`:
1,466 / 55 / 4, 0.26%, also passing. The corrected score above is the
result. The pass does not depend on the correction.

**What the 5 undisclosed conservative errors are.** All 5 are Buf's
`FIELD_WIRE_COMPATIBLE_TYPE` on changes protobuf documents as compatible:

- two enum renames (vitess `vtrpc`);
- `bool` → enum (vitess `vtctldata`);
- an enum rename plus bytes→message (istio `workload`);
- an empty message → a message with added fields (istio `values_types`).

They remain below the bar and are published with the claim.

**Oracle limits seen in adjudication.** The oracle treats string/bytes as
symmetric, but bytes→string is breaking unless the bytes are valid UTF-8. It
also cannot compare layouts through nested enum renames. Adjudication
corrected both, on one unit each.

**Remaining for release (T53.3 → T48).** The common bar's other gates still
apply to this stateless surface:

- ordinary startup admission by a signed release record, which binds the
  claim, with Ben's key;
- authorization on the real binary;
- an operating envelope: one sandboxed Buf child per request. Measured so
  far: about 3.4 units per second here (1,525 in 451 s) and 4.7 in round 1
  (571 in 121 s), each unit being one HTTP plus one MCP check, at 121 MiB
  peak RSS;
- owner, reviewer, expiry and suspension.

Backup/restore and generation gates do not apply, because the surface stores
nothing.

## 11. Release 1.0.0 (2026-10-12): signed and proven on the real binary

Ben generated the release signing key on his instruction: an ed25519 key at
`~/phebs-release-ed25519.pem` (mode 0600). The public key is
`S1sOhc+R4AZVKlmSMWfSYmZWhPcvD9KcTUfrGzf1sqE=`, also in
`spike/t533/release/public-key.txt`, under key id
`ben-phebs-release-2026-10`.

**What the record binds.** The signed record is
`spike/t533/release/selection/release.json`, with release id
`rel-phebs.protobuf.wire.buf-1.0.0`.

- **Card, manifest and validation.** These are in
  `spike/t533/release/artifacts/`. The validation cites round 2 (§10), the
  operating envelope and authorization.
- **Release binary.** Built from commit `e485fc36` (`vcs.modified=false`),
  binary `sha256:837b0f0f…`. A second clean build reproduced it byte for
  byte.
- **Validation expiry.** 2027-01-10.
- **Approval.** `ben-meddeb-2026-10-12-signing-instruction`.

**Code added for the release.**

- **Claim registry.** `releasedClaims` in `cmd/phebs` binds the claim beside
  `packRecipes` and enters the implementation digest, so a verified record
  admits exactly this surface.
- **`phebs release-sign`.** It derives every binding from the running binary
  and an artifact census, discards template digests, and self-verifies
  through the startup loader. It removes a record the loader would not
  admit.
- **Concurrency bound.** The checker now runs at most 4 Buf children at
  once. A check waits up to 5 s for a slot, then gets a retryable 503.

**Findings on the way.**

1. Concurrent checks were unbounded: each request spawned one 512 MiB-capped
   Buf child. The first refuse-at-once gate rejected 220 of 235 requests at
   8 clients, so it now queues for up to 5 s instead.
2. A released claim with no admitted extractor never got a checker. Only the
   extraction pipeline built one, so the surface stayed dark. The
   production-assembly test had masked this with a stand-in checker. A real
   Buf test now pins it (`TestReleasedClaimAloneInitializesTheChecker`).
3. The first signing attempt carried a future `approved_at`. The signer's
   self-check refused it as `future_approval`, which led to the cleanup fix.

**Envelope (release binary, 235 real units, `receipts/envelope.json`).**
Every request returned 200.

| Clients | Checks/s | p95 | Peak server-tree RSS |
|---|---|---|---|
| 1 | 7.2 | 0.25 s | 318 MiB |
| 4 | 18.7 | 0.39 s | 559 MiB |
| 8 | 19.1 | 0.65 s | 536 MiB |

Anonymous and wrong-token requests get 401.

**Startup proof (`receipts/startup.json`).** The release binary ran with no
experimental switch, only the signed selection and the key.

- Capabilities are exactly `service-catalog-v2` and `wire-compatibility`.
  No Caller Map, Atlas, Impact, comparison or proof surface is served.
- A real breaking change returns HTTP 200 `compatible=false` with
  `FIELD_WIRE_COMPATIBLE_TYPE`, and the MCP result equals HTTP.
- Unauthenticated requests get 401.
- With the release id under `release_selection.revoked`, a restart serves no
  `wire-compatibility` (HTTP 404) and logs
  `phebs.protobuf.wire.buf=revoked`.

**Not established.** This release has not had an independent human review;
Claude was the sole adjudicator and reviewer Ben named. The envelope was
measured on this host only.

## 12. Re-bind 1.0.1 (2026-10-14): one binary serves every released claim

1.0.0 admits only binary `e485fc36`, so the T49.3 release binary `2467bd17`
served no WIRE verdict. Nothing the claim depends on changed between the two:
`internal/compat`, `cmd/phebs/serve_runners.go`, and the Buf and protocompile
pins are byte-identical. `rel-phebs.protobuf.wire.buf-1.0.1` therefore
re-binds the unchanged claim. It ships in `spike/t533r/release/`, a
five-release selection signed with `ben-phebs-release-2026-10` for
`2467bd17`.

That selection holds Thrift contract 1.0.1, producer 1.0.4, consumer 1.0.3,
Topics 1.0.2 and WIRE 1.0.1. The other four are re-signatures of the
unchanged T49.3 records, needed because a record binds the digest of its
whole artifacts directory.

**Envelope on `2467bd17`** (`receipts/wire-envelope.json`): 235 round-2 units
at each of 1, 4 and 8 concurrent clients, all answering 200.

| Clients | p95 | Throughput | Peak tree RSS |
|---|---|---|---|
| 1 | 224 ms | 8.2 checks/s | 297 MiB |
| 4 | 312 ms | 23.4 checks/s | 491 MiB |
| 8 | 509 ms | 24.0 checks/s | 501 MiB |

Anonymous and wrong-token requests get 401.

**Final proofs with Ben's key.**

- **WIRE** (`receipts/wire-final.json`): capabilities are exactly the five
  released surfaces, and a real breaking change returns
  `FIELD_WIRE_COMPATIBLE_TYPE` with MCP equal to HTTP. Revoking WIRE removes
  only `wire-compatibility`, and its route answers 404.
- **Thrift** (`receipts/thrift-final.json`): all five records leave Atlas
  equal to the oracle. Revoking Thrift removes only Atlas; revoking WIRE
  leaves Atlas exact.
- **Kafka** (`receipts/kafka-final.json`): 41 of 41 topics. Revoking Topics
  or the consumer withdraws exactly those surfaces.
