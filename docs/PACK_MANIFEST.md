# phebs executable pack manifest

*Normative contract, v0.1 · the machine-readable counterpart of the
[evidence-pack card](./EVIDENCE_PACK_CARD.md). Semantics derive from the
[domain contract](./INVESTIGATION_DOMAIN_CONTRACT.md) v0.2 and the
[MCP envelope](./MCP_ENVELOPE.md) v0.2. Design only.*

## Principles

1. **The card is the claim; the manifest is the machine.** The card is the
   human-approved contract of what a pack may assert; the manifest is what
   the platform loads to enforce it. The manifest can never widen a claim,
   threshold, construct set, or workflow class beyond the card.
2. **Bound both ways at release.** The card's header carries the manifest
   digest; the manifest carries the card digest. Release-time validation
   machine-checks the pair; any divergence blocks release (card release
   requirement). A manifest change without a new card review is a
   suspension trigger.
3. **Fail-closed loading.** Unlike envelope *clients* (which ignore
   unknown fields), the platform *loader* rejects a manifest containing
   unknown fields, unknown enum values, or unsatisfied references —
   loading is enforcement, not display.
4. **Internal, fixed modules only.** Manifests ship in-tree with phebs;
   no third-party loading, no marketplace, no runtime registration.

## Manifest schema (annotated)

```json
{
  "manifest_version": "1.0",
  "pack_id": "phebs.grpc.client-calls",
  "pack_version": "1.0.0",
  "card_digest": "sha256:…",

  "predicates": [
    {
      "name": "CALLS_OPERATION",
      "subject_kind": "source_occurrence",
      "object_kind": "grpc_operation",
      "qualifiers": ["target_role", "source_origin", "behavioral_role", "reachability"],
      "evidence_required": ["blob_digest", "byte_span", "rule_id"],
      "logical_identity_fields": ["object", "service_identity"]
    }
  ],
  "supported_constructs": [
    {"construct": "direct_generated_client_call", "support": "yes", "rule_id": "…"},
    {"construct": "wrapper_alias", "support": "partial", "rule_id": "…"}
  ],

  "query": {
    "facets": ["target_role", "source_origin", "reachability", "service", "owner"],
    "census_columns": [
      {"id": "service", "source": "attribution.service", "sortable": true},
      {"id": "owner", "source": "attribution.owner", "sortable": true},
      {"id": "reachability", "source": "qualifiers.reachability", "sortable": false}
    ]
  },

  "evidence_renderer": {
    "kind": "source_span",
    "excerpt_max_bytes": 4096,
    "context_lines": 3,
    "redaction": "authorized_occurrence_only"
  },

  "coverage": {
    "denominator_method_id": "independent_target_enumeration_v1",
    "outcome_gates": {"rule_id": "…", "rule_version": "…"},
    "blocker_vocabulary": [
      "UNITS_FAILED", "UNITS_PARTIAL", "EXCLUSION_RATE_EXCEEDED",
      "OUTCOME_GATE_FAILED", "ATTRIBUTION_UNRESOLVED", "SEMANTIC_UNRESOLVED",
      "PACK_VALIDATION_EXPIRED", "PACK_NOT_RELEASED", "SCOPE_NOT_ENUMERATED",
      "STALE_ANALYSIS", "INPUT_STALE", "AUTHORIZATION_NARROWED", "RESULT_TRUNCATED"
    ],
    "qualification_templates": [
      {"template_id": "…", "template_version": "…", "parameters": ["analyzed", "failed"]}
    ]
  },

  "decision_rules": [
    {
      "rule_id": "…", "rule_version": "…",
      "conclusions": ["evidenced_conforming", "evidenced_nonconforming", "unknown"],
      "eligible_workflows_by_conclusion": {"unknown": ["triage"]}
    }
  ],

  "diff": {
    "comparability_preconditions": ["same_revision", "same_pack_version", "compatible_coverage_class", "same_identity_semantics"],
    "cause_classifier_id": "…",
    "prohibited_removal_causes": ["analysis_failed", "authorization_changed", "scope_narrowed", "unresolved_shift", "unknown_cause"]
  },

  "review_projections": [
    {"queue_id": "new_consumers", "delta_causes": ["relationship_added_traced"], "requires_baseline": true},
    {"queue_id": "coverage_regression", "condition": "outcome_gates.status == failed"}
  ],

  "mcp": {
    "tools": ["find_contract_edges", "get_contract_evidence", "explain_attribution",
              "get_analysis_coverage", "compare_contract_snapshots", "list_new_consumers",
              "verify_proof_reference", "review_requirements"],
    "payload_kinds": ["contract_edges", "contract_evidence", "attribution_trace",
                      "coverage", "snapshot_comparison", "new_consumers",
                      "proof_verification", "review_requirements"],
    "limits": {"max_facts_per_page": 200, "max_excerpt_bytes": 4096, "pagination": true}
  },

  "operating": {
    "workflow_classes": ["batch", "interactive"],
    "max_eligible_universe_units": 250000,
    "measured_envelope_reference": "<card §12 result artifact id>"
  },

  "status_inputs": {
    "card_status_field": "released",
    "validation_reference": "<opaque id>",
    "expiry": "<date or event rule>"
  }
}
```

## Release-time validation (machine checks)

Release is blocked unless **all** pass:

1. `card_digest` matches the approved card bytes, and the card's
   `Pack manifest digest` field matches this manifest's bytes.
2. Every manifest predicate appears in card §3 with identical subject/
   object kinds, qualifiers, and evidence requirements; no extras.
3. `supported_constructs` ⊆ card §5, with support levels no stronger than
   the card's.
4. `blocker_vocabulary` equals the card's declared vocabulary exactly.
5. Every `qualification_template` exists in the card with matching
   version; the envelope's `qualification.template_id` values must resolve
   here.
6. Every decision rule's conclusions are exhaustive per card §8's
   deterministic mapping; `eligible_workflows_by_conclusion` never exceeds
   the card's permitted actions.
7. `diff.prohibited_removal_causes` is a superset of the domain contract's
   prohibited set; `comparability_preconditions` include the contract's
   minimum.
8. `mcp.tools`/`payload_kinds` ⊆ the envelope contract's registry; limits
   within the envelope's authorization-safe bounds.
9. `operating` values lie within card §12's measured envelope with its
   stated margin; workflow classes only those the card marks eligible.
10. `status_inputs` reconcile with the card's derived status; a
    non-`released` card blocks a release-mode manifest load.

## Runtime obligations

- A loaded manifest version is immutable; any byte change is a new
  `pack_version` and re-triggers card review per the card's suspension
  rules.
- The platform enforces manifest limits at query time (page size, excerpt
  bytes, universe caps) and refuses tools/payloads absent from the
  manifest.
- Envelope fields (`validation`, `analysis_conclusion`, `qualification`,
  blocker codes) are populated **only** from manifest-resolved identities;
  no free-text passthrough from pack code.
- The manifest contains no thresholds, sampling rules, or policy text of
  its own: those live in the card and referenced rule artifacts. A
  manifest field that would override a card value is a loader error.
