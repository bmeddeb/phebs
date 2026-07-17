# phebs MCP envelope schema

*Normative MCP product contract, v0.2 · every evidence-sensitive MCP tool
returns this envelope through a versioned output schema. Semantics derive from
[INVESTIGATION_DOMAIN_CONTRACT.md](./INVESTIGATION_DOMAIN_CONTRACT.md) v0.2;
on conflict the domain contract wins. Design only—no change to the sealed
extractor or validation ceremony.*

## 1. Principles

1. **Never collapse the axes.** Evidence basis, semantic resolution,
   processing coverage, attribution state, pack conclusion, human Decision,
   and absence eligibility remain separate. There is no aggregate confidence
   score.
2. **Empty facts are not an absence conclusion.** An absence-shaped statement
   requires `absence_eligibility.applicable: true` and `eligible: true`.
3. **Completeness has separate meanings.** Analysis success, authorized result
   set completeness, current-page exhaustion, and truncation are distinct.
4. **Every response is a current authorization projection.** It never reveals
   inaccessible facts, counts, identifiers, prior scope, or that another
   principal sees a different universe.
5. **Refusal, failure, and evidentiary uncertainty are different.** Eligibility
   blockers never double as authorization or transport errors.
6. **The server owns authoritative qualification wording.** It returns the
   template identity, parameters, and rendered text; agents do not recreate
   negative wording.
7. **UI, API, and MCP share semantics.** A tool-specific payload may change
   shape, but coverage, authorization, evidence, and eligibility never do.

## 2. MCP transport mapping

- Each tool declares a versioned `inputSchema` with explicit typed claim and
  scope fields, plus a JSON Schema `outputSchema` whose root is the common
  envelope and whose `payload` is that tool's discriminated payload schema.
- The envelope is returned in MCP `structuredContent`.
- Optional textual `content` is rendered by phebs from the same authorized
  envelope. It is presentation, never the sole source of facts or
  qualification.
- `outcome: ok | partial | refused` is a successful tool execution and uses
  `isError: false`. A refusal means the server successfully enforced policy.
- `outcome: error` uses `isError: true` and a safe structured error. It never
  returns stack traces, internal paths, hidden identifiers, or partial facts
  whose publication was not authorized.
- For identities the principal may not know exist, refusal uses the same
  generic `NOT_AVAILABLE` response as an unknown identity and omits sensitive
  scope, pack, and validation fields.

Before implementation, this document produces machine-checked schemas:

```text
schemas/mcp-envelope-v1.0.json
schemas/mcp-payload-contract-edges-v1.0.json
schemas/mcp-payload-contract-evidence-v1.0.json
schemas/mcp-payload-attribution-trace-v1.0.json
schemas/mcp-payload-coverage-v1.0.json
schemas/mcp-payload-snapshot-comparison-v1.0.json
schemas/mcp-payload-new-consumers-v1.0.json
schemas/mcp-payload-proof-verification-v1.0.json
schemas/mcp-payload-review-requirements-v1.0.json
```

The annotated example below does not replace those schemas.

## 3. Versioning and compatibility

`envelope_version` is `MAJOR.MINOR`.

- Adding an optional field or enum member increments MINOR.
- Removing a field, making an optional field required, changing meaning or
  structure, or narrowing an accepted value increments MAJOR.
- Clients ignore unknown fields within a supported MAJOR version.
- Clients preserve unknown fields when transparently relaying an envelope
  when their transport permits it; preservation is not relied on for safety.
- An unknown enum value is treated as `unknown` or causes the affected feature
  to be withheld. It is never silently mapped to a known value.
- A client that does not support the MAJOR version must refuse semantic
  interpretation rather than guessing.

## 4. Common envelope — annotated, internally valid example

This example represents a fully returned authorized result set whose analysis
contains two failed units. Therefore it is partial and cannot support an
absence conclusion.

```json
{
  "envelope_version": "1.0",
  "request_id": "01J...",
  "generated_at": "2026-07-17T20:00:00Z",
  "tool": {
    "name": "find_contract_edges",
    "semantic_version": "1.0"
  },
  "outcome": "partial",
  "scope": {
    "claim": {
      "claim_family": "contract_relationship",
      "predicate": "CALLS_OPERATION",
      "subject": {"kind": "grpc_operation", "id": "<opaque-authorized-id>"},
      "object": null,
      "filters": {"target_role": ["production"]},
      "decision_sought": "enumerate_service_consumer_candidates"
    },
    "normalized_question": "<authoritative normalized wording>",
    "normalization_assumptions": [],
    "investigation_id": "<opaque-ulid-or-null>",
    "revision_id": "<opaque-authorized-id-or-null>",
    "run_artifact_id": "<opaque-authorized-id>",
    "snapshot_id": "<opaque-authorized-id>",
    "build_configuration_id": "<opaque-authorized-id>",
    "visibility_projection_id": "<opaque-principal-scoped-id>"
  },
  "payload": {
    "kind": "contract_edges",
    "data": {
      "facts": []
    }
  },
  "coverage": {
    "visibility": "available",
    "eligible_units": 1000,
    "processing": {
      "analyzed": 998,
      "excluded": 0,
      "partial": 0,
      "failed": 2
    },
    "processing_reconciled": true,
    "exclusions_by_reason": {},
    "outcome_gates": {
      "status": "failed",
      "rule_id": "<pack-rule-id>",
      "rule_version": "<version>",
      "reason_codes": ["UNITS_FAILED"]
    },
    "attribution": {
      "build_target": {"denominator": 0, "resolved": 0, "ambiguous": 0, "unresolved": 0, "not_applicable": 0},
      "deployable": {"denominator": 0, "resolved": 0, "ambiguous": 0, "unresolved": 0, "not_applicable": 0},
      "service": {"denominator": 0, "resolved": 0, "ambiguous": 0, "unresolved": 0, "not_applicable": 0},
      "owner": {"denominator": 0, "resolved": 0, "ambiguous": 0, "unresolved": 0, "not_applicable": 0}
    }
  },
  "analysis_conclusion": {
    "value": "unknown",
    "evidence_basis": "derived",
    "rule_id": "<decision-rule-id>",
    "rule_version": "<version>",
    "reason_codes": ["UNITS_FAILED"],
    "input_references": ["<coverage-ref>"],
    "eligible_workflows": ["triage"]
  },
  "absence_eligibility": {
    "applicable": true,
    "eligible": false,
    "rule_version": "<eligibility-rule-version>",
    "blocker_codes": ["UNITS_FAILED"],
    "qualification": {
      "template_id": "<pack-template-id>",
      "template_version": "<version>",
      "parameters": {"analyzed": 998, "failed": 2},
      "authoritative_text": "Analysis is incomplete. No supported facts were found among analyzed units; failed scope is listed separately."
    }
  },
  "validation": {
    "pack_id": "<stable.pack.id>",
    "pack_version": "<semver>",
    "pack_status": "released",
    "workflow_eligibility": "eligible",
    "validation_reference": "<opaque-authorized-id>",
    "applies": true,
    "applicability_blockers": [],
    "measured_claim_id": "<claim-id>",
    "expires_at": "2026-12-31T23:59:59Z",
    "expiry_trigger": null
  },
  "provenance": {
    "query_digest": "<scoped-digest>",
    "input_manifest_id": "<opaque-authorized-id>",
    "pack_manifest_id": "<opaque-authorized-id>",
    "extractor_version": "<version>",
    "adapter_versions": {"source": "<version>", "build": "<version>", "catalog": "<version>"},
    "rule_versions": {"extraction": "<version>", "attribution": "<version>", "decision": "<version>"},
    "schema_version": "<version>",
    "phebs_source_commit": "<digest>",
    "phebs_binary_digest": "<digest>",
    "toolchain_digest": "<digest>"
  },
  "freshness": {
    "analysis": {
      "status": "current",
      "evaluated_at": "2026-07-17T20:00:00Z",
      "rule_id": "<freshness-rule-id>",
      "rule_version": "<version>",
      "expires_at": "2026-07-18T20:00:00Z",
      "reason_codes": []
    },
    "inputs": [
      {
        "kind": "source",
        "required": true,
        "authority": "git",
        "snapshot_id": "<opaque-authorized-id>",
        "as_of": "2026-07-17T19:45:00Z",
        "freshness_rule_id": "<rule-id>",
        "freshness_rule_version": "<version>",
        "evaluated_at": "2026-07-17T20:00:00Z",
        "expires_at": "2026-07-18T20:00:00Z",
        "status": "current",
        "reason_codes": []
      }
    ]
  },
  "result_window": {
    "result_set_complete": false,
    "page_exhausted": true,
    "truncated": false,
    "truncated_reason": null,
    "returned": 0,
    "next_page_token": null,
    "ordering": {"key": "logical_fact_id", "direction": "ascending"}
  },
  "refusal": null,
  "errors": []
}
```

## 5. Top-level outcome and refusal rules

| Outcome | Meaning | Payload behavior | MCP `isError` |
|---|---|---|---:|
| `ok` | requested authorized computation completed without response-level limitations | full authorized payload | false |
| `partial` | safe payload exists, but analysis, coverage, freshness, comparability, or result completeness limits a requested conclusion | payload plus exact limitation/eligibility codes | false |
| `refused` | policy, authorization, unsupported claim, or pack workflow status prohibits the response | empty/minimal payload; safe refusal only | false |
| `error` | malformed input after tool invocation, internal execution failure, or unavailable dependency prevents a valid response | no unpublished facts; safe errors | true |

`refusal` contains a stable code and optional safe retry guidance. Core codes:

```text
NOT_AVAILABLE
UNSUPPORTED_CLAIM
PACK_WORKFLOW_NOT_ELIGIBLE
ACTION_NOT_PERMITTED
RATE_LIMITED
```

The server uses `NOT_AVAILABLE` for unauthorized and unknown sensitive
identities when distinguishing them would reveal existence. Eligibility
blockers never appear in `refusal`, and refusal codes never appear in
`absence_eligibility.blocker_codes`.

Each `errors[]` entry contains a stable code, `retryable` Boolean, optional
safe message, and optional `retry_after_seconds`. Core codes are
`INVALID_ARGUMENT`, `DEPENDENCY_UNAVAILABLE`, `EXECUTION_FAILED`, and
`INTERNAL_ERROR`. Raw dependency responses and implementation details are
never serialized.

## 6. Scope and normalization rules

- Claim fields are typed and namespace-qualified. Free-form user wording is
  not the claim identity.
- The server echoes the authoritative normalized claim and all safe
  assumptions. It never silently broadens repository, target-role, snapshot,
  predicate, or relationship scope.
- Any material normalization ambiguity is represented in semantic resolution
  or refused as `UNSUPPORTED_CLAIM`; the LLM does not choose silently.
- Investigation, Revision, RunArtifact, snapshot, build-configuration, and
  visibility-projection identifiers are opaque authorized references. Raw
  content or visibility digests are not cross-scope correlation keys.
- Stateless tools set Investigation and Revision identifiers to `null` and
  normally return `SCOPE_NOT_ENUMERATED` for absence eligibility unless an
  independently enumerated authorized universe is otherwise available.

## 7. Fact and proof-reference contract

An evidenced or derived fact uses this shape inside an appropriate payload:

```json
{
  "fact_id": "<opaque-authorized-id>",
  "logical_relationship_id": "<opaque-authorized-id>",
  "predicate": "CALLS_OPERATION",
  "subject": {"kind": "source_occurrence", "id": "<opaque-authorized-id>"},
  "object": {"kind": "grpc_operation", "id": "<opaque-authorized-id>"},
  "qualifiers": {
    "target_role": "production",
    "source_origin": "first_party",
    "behavioral_role": "client_call",
    "reachability": "direct"
  },
  "evidence_basis": "evidenced",
  "semantic_resolution": {
    "state": "resolved",
    "reason_codes": [],
    "authorized_alternatives": []
  },
  "attribution": {
    "build_target": {"state": "resolved", "identities": ["<opaque-id>"], "reason_codes": [], "derivation_reference": "<opaque-ref>"},
    "deployable": {"state": "resolved", "identities": ["<opaque-id>"], "reason_codes": [], "derivation_reference": "<opaque-ref>"},
    "service": {"state": "resolved", "identities": ["<opaque-id>"], "reason_codes": [], "derivation_reference": "<opaque-ref>"},
    "owner": {"state": "resolved", "identities": ["<opaque-id>"], "reason_codes": [], "derivation_reference": "<opaque-ref>"}
  },
  "proof_references": [
    {
      "proof_id": "<opaque-authorized-id>",
      "occurrence_id": "<opaque-authorized-id>",
      "snapshot_id": "<opaque-authorized-id>",
      "verification_tool": "verify_proof_reference"
    }
  ],
  "derivation_reference": null
}
```

- `evidenced` facts require at least one authorized proof reference.
- `derived` facts require a derivation reference and authorized input-fact or
  input-record references; they must not masquerade as direct source proof.
- Ambiguous alternatives and attribution identities are filtered before
  counting and serialization.
- Proof IDs are opaque and resolve only through current occurrence
  authorization. Unknown and unauthorized proof IDs receive the same
  `NOT_AVAILABLE` refusal.
- Evidence retrieval returns bounded bytes or excerpts plus blob digest,
  byte-span convention, encoding, redaction state, and snapshot binding. A
  “bytes reference” is never left ambiguous about whether bytes are embedded.

## 8. Coverage contract

- `eligible_units` and all counts are non-negative JSON integers within the
  declared schema range.
- Processing counts sum exactly to `eligible_units` when
  `processing_reconciled: true`.
- Exclusions are grouped by versioned reason code; reason counts sum to the
  processing `excluded` count.
- Every attribution hop declares its own denominator and the counts
  `resolved`, `ambiguous`, `unresolved`, and `not_applicable`; those counts sum
  to that hop's denominator.
- Attribution denominators follow the pack card and never silently exclude
  failed processing or unsuccessful mappings.
- All counts are computed only over the principal's current authorized
  projection. Omitted or redacted counts are represented by a safe schema
  state, never a smaller number that looks complete.
- `coverage.visibility` is `available | withheld`. When `withheld`, counts are
  `null`, no hidden total is implied, and absence eligibility is false.
- `processing_reconciled` is accounting only. It does not imply successful
  analysis or absence eligibility.

## 9. Analysis conclusion and human Decisions

`analysis_conclusion` is the deterministic, pack-defined conclusion. It is not
the Investigation domain's human `Decision`.

It contains conclusion value, evidence basis, decision-rule identity/version,
reason codes, complete input references, and eligible workflows/actions. If no
pack rule applies, it is `null`; clients do not infer one.

Human Decisions are returned only by tools explicitly authorized to read them,
as opaque Decision references or a dedicated payload. They never occupy
`analysis_conclusion` and never alter facts, coverage, validation, or absence
eligibility.

## 10. Absence eligibility and qualification

- `applicable: false` means an absence conclusion is not relevant—for example,
  because authorized facts are present or the requested claim is not
  absence-shaped. `eligible` must also be false and qualification may be null.
- `applicable: true, eligible: false` requires one or more domain-contract or
  namespaced pack blocker codes and the authoritative incomplete/unknown
  qualification when disclosure is allowed.
- `eligible: true` requires zero blockers, released/applicable validation,
  complete eligible-scope analysis, a complete authorized result set, final
  page exhaustion for the evaluated negative result, and no truncation.
- Qualification includes template ID, template version, typed parameters, and
  server-rendered authoritative text. Clients may present that text but must
  not reconstruct or strengthen it.

## 11. Validation and workflow eligibility

`pack_status` accepts the pack card's complete lifecycle:

```text
design | experimental-dark | shadow | released | suspended | retired
```

- `released` does not itself prove applicability; `applies` must be true for
  the exact claim, artifact versions, constructs, population assumptions, and
  intended workflow.
- `design` and `experimental-dark` are unavailable to ordinary claim tools.
- `shadow` may be exposed only to authorized evaluation workflows and is
  explicitly non-decision-eligible.
- `suspended` and `retired` refuse new claim-bearing workflows except
  authorized diagnostics or historical reproduction.
- `workflow_eligibility` is `eligible | advisory | refused` and is derived
  from the pack manifest, validation applicability, and tool workflow.
- Expiry is represented as `expires_at`, an `expiry_trigger`, or both—not an
  untyped date-or-rule string.

## 12. Freshness contract

Analysis and each required input report authority, version/snapshot, required
status, as-of time, freshness-rule identity/version, evaluation time,
expiry/review time, status, and reason codes. Core statuses are
`current | stale | unavailable | unknown`.

A stale required input produces `INPUT_STALE`; stale analysis produces
`STALE_ANALYSIS`. An unavailable input may also change `outcome` to `partial`
or `error` according to whether a safe published payload remains.

## 13. Result-window and page-token rules

- `result_set_complete` describes whether phebs computed the complete
  authorized match set supported by the eligible-scope analysis, without
  relevant processing failure, caps, shard failure, or hidden truncation.
- `page_exhausted` describes whether the current response is the final page.
- `truncated` means the complete authorized set cannot be obtained through the
  supplied cursor sequence. It requires a safe reason code and blocks absence.
- `next_page_token` is opaque, expiring, and bound to principal, tool semantic
  version, normalized query digest, visibility projection, snapshot/RunArtifact,
  ordering, and authorization epoch.
- Authorization or semantic-version change invalidates outstanding tokens.
- Cursor pagination is snapshot-consistent and cannot skip or duplicate facts.
- Hidden totals are not returned. A `returned` count describes this page only.

## 14. Tool payloads

| Tool | `payload.kind` | Contract |
|---|---|---|
| `find_contract_edges` | `contract_edges` | authorized facts matching the typed claim |
| `get_contract_evidence` | `contract_evidence` | one fact plus explicitly embedded bounded bytes/excerpt or an authorized evidence locator |
| `explain_attribution` | `attribution_trace` | one fact plus per-hop deterministic derivation, conflicts, and unresolved reasons |
| `get_analysis_coverage` | `coverage` | coverage ledger; no synthetic empty facts array |
| `compare_contract_snapshots` | `snapshot_comparison` | cause-classified delta only when comparable under domain-contract §8; otherwise a comparison report |
| `list_new_consumers` | `new_consumers` | `relationship_added_traced` plus positively traced reintroductions since an authorized named Baseline |
| `verify_proof_reference` | `proof_verification` | integrity and current authorization result for one proof reference; unknown and unauthorized remain indistinguishable |
| `generate_review_checklist` | `review_requirements` | read-only pack-derived review requirements; does not create or impersonate ReviewItems |

Each payload schema declares required fields, nullable fields, enum behavior,
maximum collection/excerpt sizes, pagination support, and authorization-safe
empty states. Tool-specific fields never appear as undocumented top-level
extensions.

## 15. Human-reserved actions

Creating or superseding Decisions, approving external exceptions, changing
ownership, concluding an Investigation, and publishing dossiers are absent
from the initial MCP tool surface and refused server-side by permission and
authority rules. Tool omission is not a security boundary.