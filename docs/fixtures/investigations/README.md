# Synthetic Investigation fixtures

Eight canonical-valid envelope instances modeling the states that phebs UI,
API, MCP, authorization, and conformance tests must handle. They target the
[MCP envelope](../MCP_ENVELOPE.md) contract v0.2 with
`envelope_version: "1.0"` and the Investigation domain contract v0.2.

These fixtures are **entirely synthetic**. The `acme` domain, pack, source
coordinates, identifiers, counts, decisions, and timestamps are invented. No
benchmark repository, benchmark fixture, coordinate, label, answer key, or
sealed validation material was used or referenced (GATE2-V2 disclosure
rules).

## Fixture index and required assertions


| Fixture                              | State modeled                                    | Required assertions                                                                                                                                                     |
| ------------------------------------ | ------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `01-complete-with-findings.json`     | Complete analysis with evidenced facts           | `outcome=ok`; two normative facts and proof references; absence inapplicable; result set complete and page exhausted                                                    |
| `02-complete-zero-findings.json`     | Eligible absence                                 | released/applicable validation; complete reconciled scope; zero facts; `eligible=true`; no blockers; authoritative negative wording                                     |
| `03-partial-failed-processing.json`  | Partial and failed processing                    | processing and exclusion counts reconcile to 120; `outcome=partial`; result set incomplete; `UNITS_FAILED` and `UNITS_PARTIAL`; triage only                             |
| `04-unresolved-attribution.json`     | Evidenced fact with unresolved service and owner | fact remains evidenced; per-hop attribution and coverage reconcile; `outcome=partial`; conclusion unknown; absence inapplicable without eligibility blockers            |
| `05-stale-pack-validation.json`      | Suspended pack with expired validation           | claim-bearing tool is refused; no payload or coverage; `PACK_WORKFLOW_NOT_ELIGIBLE`; workflow eligibility is `refused`                                                  |
| `06-inaccessible-scope-refusal.json` | Unknown-or-unauthorized sensitive identity       | minimal `NOT_AVAILABLE` refusal; unknown and unauthorized cases are byte-shape indistinguishable; no scope, counts, result window, pack, validation, or provenance      |
| `07-non-comparable-revisions.json`   | Comparison without compatible semantics          | `outcome=partial`; no ordinary fact delta; per-side coverage only; no analysis conclusion; explicit comparability rule and reasons                                      |
| `08-truncated-result.json`           | Irreversible hard truncation                     | complete processing but incomplete result set; `truncated=true`; no continuation token; applicable but ineligible absence; `RESULT_TRUNCATED` and authoritative wording |


All files in this directory are intended to be valid examples. Deliberately
invalid payloads belong in a separate `invalid/` directory and must declare
the exact schema assertion they violate.

## Contract and registry dependencies

The fixtures depend on these schema families when generated:

```text
schemas/mcp-envelope-v1.0.json
schemas/mcp-payload-contract-edges-v1.0.json
schemas/mcp-payload-snapshot-comparison-v1.0.json
```

They also depend on a synthetic in-tree registry for:

- pack `phebs.synthetic.grpc` version `0.0.1`;
- decision, coverage-gate, comparability, freshness, and qualification rules
whose IDs begin with `syn-`;
- namespaced exclusion and attribution reason codes beginning with
`PHEBS_SYNTHETIC_GRPC.`; and
- the qualification templates `syn-q-neg`, `syn-q-incomplete`, and
`syn-q-truncated`.

The synthetic pack is `released` only inside the fixture universe. This is a
test state, not a claim about the production Go/gRPC extractor or its Gate-2
validation status.

## Validation

Until the generated JSON Schemas and synthetic registry are checked in, the
portable syntax check is:

```sh
for file in docs/fixtures/*.json; do jq -e . "$file" >/dev/null; done
```

Once the schemas exist, CI must validate every fixture against the declared
envelope and discriminated payload schema, then run semantic assertions for:

- processing and exclusion reconciliation;
- per-hop attribution reconciliation;
- absence applicability and eligibility;
- pack lifecycle and workflow eligibility;
- result-set completeness, page exhaustion, and truncation;
- fact/proof-reference requirements;
- comparison behavior; and
- refusal non-disclosure.

The generator is not part of this fixture bundle. Its repository path,
regeneration command, deterministic ordering rule, and drift check must be
recorded here before generated-fixture CI becomes authoritative. Until then,
changes are reviewed as contract fixtures and JSON is kept deterministically
formatted with two-space indentation and a trailing newline.