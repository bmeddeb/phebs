# Attribution-hop label sheet formats — dependency-preview draft

*Draft artifact for pilot prerequisite item 11 (design phase). Templates
only: no sheet defined here can seal until Gate 0 fixes the partner's
catalog shapes, and nothing here advances any prerequisite state or gate.
The charter controls scope; these sheets cover exactly the §8.2 hop labels
— build-target, deployable, service, and owner-record — plus the
end-to-end edge frame. Proto field-level lineage remains out of scope
per charter §3.4.*

## Shared discipline (inherited from the sealed t111 machinery)

Every hop sheet follows the same rules `label_protocol.py` enforces for
call-site labels, applied at the hop's own unit:

- exact population equality — a sheet must contain exactly the sealed
  sample's unit IDs, no more, no fewer; validation fails closed;
- decisions are `yes | no | unsure`; `unsure` is excluded from numerator
  and denominator and always reported;
- every row carries a nonempty `rationale` and `evidence`;
- sheets are labeled blind to phebs predictions, adjudicated, frozen as
  canonical JSONL, committed, and externally receipted **before**
  predictions are unsealed;
- one sheet version is one schema name; a different field set is a
  different schema and requires review before sealing.

## Unit identities

| Hop | Unit ID format | Source of the claim under test |
|---|---|---|
| Build-target | `site_id → target:<partner build-target coordinate>` | phebs occurrence joined to the partner build graph |
| Deployable | `target:<coordinate> → deployable:<partner deployable identity>` | build graph joined to deployment metadata |
| Service | `deployable:<identity> → service:<canonical catalog identity>` | deployment metadata joined to the service catalog |
| Owner-record | `service:<identity> → owner:<recorded owner identity>` | catalog ownership record |
| End-to-end edge | `(canonical service, /package.Service/Method)` | scored directly against its own frames, never composed |

`<partner …>` coordinate shapes are frozen at Gate 0 from the partner's
actual catalogs; a placeholder shape blocks sealing (charter Gate 0 rule).

## Sheet schema drafts

### `pilot-hop-build-target-v1-draft`

| Field | Semantics |
|---|---|
| `unit_id` | build-target hop unit ID above |
| `mapping` | `yes` — the sampled occurrence genuinely belongs to the named build target; `no`; `unsure` |
| `expected_target` | required iff `mapping=yes`: the reviewer's independently determined coordinate |
| `rationale`, `evidence` | nonempty; evidence cites the partner artifact consulted |

### `pilot-hop-deployable-v1-draft`

Same shape over the deployable hop: decision field `mapping`,
`expected_deployable` required iff `yes`.

### `pilot-hop-service-v1-draft`

Same shape over the service hop: decision field `mapping`,
`expected_service` required iff `yes` and must be the canonical catalog
identity, not a display name.

### `pilot-hop-owner-v1-draft`

Same shape over the owner hop: decision field `mapping`, `expected_owner`
required iff `yes`. Owner labels assert what the record states, not whether
the recorded owner is currently accountable (charter §3.3 boundary).

### `pilot-edge-v1-draft` (end-to-end)

| Field | Semantics |
|---|---|
| `unit_id` | `(canonical service, operation)` edge ID |
| `edge` | `yes` — the edge exists per independent evidence; `no`; `unsure` |
| `rationale`, `evidence` | nonempty; the evidence channel must be one the recall-frame method admits |

The end-to-end sheet labels its own independently constructed frames; it is
never derived from hop sheets, and hop results are never multiplied into it
(charter §9 operational definitions).

## Denominators and scoring

Each hop scores **accuracy** (against these labels, over sampled emitted
mappings) and **coverage** (over all supported analyzed occurrences
requiring that hop) as separate claims; unresolved rates use the declared
denominator. Scoring runs through `harness.score_claim`-equivalent logic
once the hop schemas are versioned into the harness — a Gate 0-gated code
change, deliberately not made while shapes remain placeholders.

## Open until Gate 0

1. Partner coordinate shapes for all four `<partner …>` placeholders.
2. Hop-sheet validator support in `pilot/validation/harness.py` (schema
   names above, same fail-closed rules) — implemented only after shapes
   freeze, so no speculative schema ships.
3. Reviewer assignment and overlap per sheet (protocol §7 applies to every
   hop sheet unchanged).
