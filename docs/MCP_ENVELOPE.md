# phebs MCP envelope schema

*Design contract, v0.1 · every MCP tool result shares this envelope.
Semantics derive from [INVESTIGATION_DOMAIN_CONTRACT.md](./INVESTIGATION_DOMAIN_CONTRACT.md)
v0.2; on conflict the domain contract wins. Design only — no change to the
sealed extractor or ceremony.*

## Principles

1. **Never collapse the axes.** Evidence basis, semantic resolution,
   processing coverage, attribution state, decision conclusion, and
   absence eligibility are separate fields; no aggregate "confidence".
2. **An empty `facts` array is not an absence conclusion.** Only
   `absence_eligibility.eligible: true` permits absence-shaped wording,
   and the wording itself is the referenced qualification template —
   agents render the template, never their own summary of it.
3. **Truncation is first-class.** A truncated or paginated result sets
   `complete: false` and blocks absence eligibility (`RESULT_TRUNCATED`).
4. **The envelope is an authorization projection** for the requesting
   principal at call time. It never discloses inaccessible existence,
   counts, or that any other principal sees a different universe.
5. **Refusals use the envelope too**: a tool that cannot answer returns
   the envelope with empty payload and blocker codes — never prose.
6. **Additive versioning.** `envelope_version` gates semantics; fields are
   only ever added. Unknown fields must be ignored, never stripped, by
   intermediaries.

## Envelope (annotated)

```json
{
  "envelope_version": "1",
  "tool": "find_contract_edges",
  "scope": {
    "normalized_question": "<canonical claim text>",
    "investigation_id": "<ULID | null>",
    "revision": "<(investigation, seq) | null>",
    "run_artifact": "<scoped content digest | null>",
    "snapshot": "<monorepo commit/tree digest>",
    "build_configuration": "<digest>",
    "visibility_scope_id": "<authorized-scope digest>"
  },
  "facts": [
    {
      "predicate": "<PREDICATE>",
      "subject": "<identity>",
      "object": "<identity>",
      "qualifiers": {"target_role": "…", "source_origin": "…", "behavioral_role": "…", "reachability": "…"},
      "evidence_basis": "evidenced | derived",
      "semantic_resolution": "resolved | ambiguous | unresolved",
      "attribution_states": {
        "build_target": "resolved | ambiguous | unresolved | not_applicable",
        "deployable": "…", "service": "…", "owner": "…"
      },
      "proof_reference": "<evidence_id + occurrence locator>"
    }
  ],
  "coverage": {
    "eligible_units": "<count within visibility projection>",
    "processing": {"analyzed": 0, "excluded": 0, "partial": 0, "failed": 0},
    "attribution_coverage": {"build_target": {}, "deployable": {}, "service": {}, "owner": {}},
    "reconciled": true
  },
  "decision_conclusion": "<pack-specific | null>",
  "absence_eligibility": {
    "eligible": false,
    "blocker_codes": ["UNITS_FAILED", "RESULT_TRUNCATED"],
    "qualification_template_id": "<pack-card template id>"
  },
  "validation": {
    "pack_id": "<stable.pack.id>",
    "pack_version": "<semver>",
    "pack_status": "shadow | released | suspended",
    "validation_identity": "<benchmark/result artifact digest>",
    "validation_expires": "<date | event rule>"
  },
  "provenance": {
    "extractor_version": "…", "adapter_version": "…",
    "rule_versions": "…", "schema_version": "…",
    "phebs_binary_digest": "…"
  },
  "freshness": {
    "snapshot_committed": "<RFC3339>",
    "published": "<RFC3339>",
    "external_inputs": [{"input": "service_catalog", "as_of": "<RFC3339>", "stale": false}]
  },
  "result_window": {
    "complete": true,
    "returned": 0,
    "next_page_token": null,
    "truncated_reason": null
  }
}
```

## Field rules

| Field | Rule |
|---|---|
| `scope` | always present; stateless tools set investigation fields null |
| `facts[].proof_reference` | resolvable via `verify_proof_reference`; digest exposure follows the contract's existence-oracle rule |
| `coverage` | denominators are the principal's visibility projection, never the global universe |
| `decision_conclusion` | present only when a versioned pack decision rule applies; never inferred |
| `absence_eligibility` | computed per the contract §6; `eligible: true` requires `result_window.complete: true` |
| `validation.pack_status` | anything other than `released` obliges the client to label results advisory |
| `freshness.external_inputs[].stale` | any `true` implies blocker `INPUT_STALE` |
| `result_window` | `complete: false` ⇒ `RESULT_TRUNCATED` present |

## Tool payloads over the shared envelope

| Tool | Payload delta |
|---|---|
| `find_contract_edges` | `facts` = matching edges |
| `get_contract_evidence` | `facts` = one edge + embedded evidence bytes reference |
| `explain_attribution` | `facts` = one edge; adds per-hop derivation trace |
| `get_analysis_coverage` | empty `facts`; full `coverage` |
| `compare_contract_snapshots` | adds `diff` block: cause-classified deltas or a comparison report when runs are non-comparable (contract §7); prohibited causes never render as removals |
| `list_new_consumers` | `diff` restricted to `source_added` and positively traced reintroductions since a named Baseline |
| `verify_proof_reference` | integrity result for one proof reference |
| `generate_review_checklist` | derived ReviewItems projection; read-only |

Human-reserved verbs (approve, decide, create exceptions, change
ownership, conclude) are absent from the tool surface **and** refused
server-side by principal role.
