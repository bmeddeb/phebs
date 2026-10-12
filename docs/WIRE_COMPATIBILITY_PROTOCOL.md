# T53.3 Buf WIRE compatibility quality protocol

Status: **unsealed draft** (2026-10-12, Claude lead). The thresholds in §6
are proposed and Ben freezes them. Nothing in §5 runs before the seal in §7.

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

One reviewer adjudicates by reading both sides' sources. They judge every
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

## 6. Thresholds (proposed; Ben freezes)

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
