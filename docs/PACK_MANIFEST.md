# phebs executable pack manifest

*Normative contract, v0.2 · the machine-readable counterpart of the
[evidence-pack card](./EVIDENCE_PACK_CARD.md). Semantics derive from the
[domain contract](./INVESTIGATION_DOMAIN_CONTRACT.md) v0.2 and the
[MCP envelope](./MCP_ENVELOPE.md) v0.2. On conflict, the domain contract
wins. Design only.*

## 1. Purpose and authority

An evidence pack is released as four separately versioned artifacts:

| Artifact | Authority |
|---|---|
| **Pack card** | Human-approved claim, scope, non-claims, validation, limitations, workflow eligibility, and operating envelope |
| **Pack manifest** | Machine-readable product module, schema/rule bindings, query presentation, enforcement hooks, and platform safety ceilings |
| **Referenced artifacts** | Executable identity, extraction, decision, comparability, projection, authorization, freshness, template, and other rules plus their schemas |
| **Pack release** | Signed immutable record binding the exact approved card, manifest, implementation, referenced artifacts, and validation result |

The card and manifest identify the same `pack_id` and `pack_claim_version`,
but **do not embed one another's final digest**. Mutual digest fields would
create a circular hash dependency. A `PackRelease` record binds their digests
after both artifacts are canonicalized and approved.

The manifest may operationalize the card but may never widen its claim,
construct support, measured threshold, negative wording, workflow class,
authorization behavior, or operating envelope. A platform safety ceiling may
be stricter than the card; it may never be looser.

## 2. Loader and publication principles

1. **Fail closed.** The loader rejects an unsupported schema major, unknown
   field, unknown enum, duplicate object key, malformed digest, unsatisfied
   reference, invalid signature, non-canonical artifact, or inconsistent
   release binding. Loading is enforcement, not client-side display.
2. **Fixed in-tree modules only.** Manifests and executable rule
   implementations ship with phebs. There is no third-party loading,
   marketplace, runtime registration, or manifest-selected arbitrary code.
3. **Digest-bound behavior.** Every referenced schema, rule, template,
   implementation, validation result, and measured-envelope artifact is
   identified by stable ID, semantic version, and content digest. A stable ID
   alone is insufficient.
4. **Current authorization.** Object access, evidence access, aggregation,
   comparison, Review, proof resolution, export, pagination, and cache reuse
   are evaluated for the current principal at use time.
5. **No authored uncertainty.** Pack code returns typed states and parameters.
   Authoritative conclusion and qualification wording is rendered from sealed
   templates; pack code cannot pass through free-form decision language.
6. **Separate version axes.** Changes to measured claim semantics, manifest
   presentation, executable rules, schemas, implementation, validation, and
   release binding remain independently identifiable.

## 3. Canonical artifacts and formal schemas

Before implementation this contract produces, at minimum:

```text
schemas/pack-manifest-v1.0.json
schemas/pack-release-v1.0.json
schemas/pack-rule-reference-v1.0.json
schemas/pack-operating-profile-v1.0.json
```

Every schema has a stable `$id`, explicit `required` fields, bounded strings
and arrays, exact formats and enums, and `additionalProperties: false` at
every object boundary unless a field is explicitly defined as an extension
map. Extension maps use namespaced keys and schema-bound values.

JSON is UTF-8 and canonicalized with the platform's frozen canonical-JSON
algorithm before hashing or signing. The release record names that algorithm
and version. The parser rejects duplicate keys before canonicalization.
Digests cover canonical bytes; signatures cover the canonical `PackRelease`
payload excluding the signature value itself.

`manifest_schema_version` is `MAJOR.MINOR`:

- an unsupported MAJOR is rejected;
- adding an optional field or enum member increments MINOR;
- removing a field, making an optional field required, narrowing a value, or
  changing meaning increments MAJOR; and
- the loader uses the exact in-tree schema for the declared version and never
  guesses at an unknown field or enum.

## 4. Pack release binding

The release record, not a self-asserted manifest or card field, determines
what may load and in which mode. Annotated shape:

```json
{
  "release_schema_version": "1.0",
  "release_id": "<opaque-id>",
  "release_version": "1.0.0",
  "pack_id": "phebs.grpc.client-calls",
  "pack_claim_version": "1.0.0",
  "card": {
    "artifact_id": "<opaque-id>",
    "digest": "sha256:<digest>"
  },
  "manifest": {
    "artifact_id": "<opaque-id>",
    "digest": "sha256:<digest>"
  },
  "implementation": {
    "phebs_source_commit": "<digest>",
    "phebs_binary_digest": "sha256:<digest>",
    "pack_implementation_digest": "sha256:<digest>",
    "toolchain_digest": "sha256:<digest>"
  },
  "referenced_artifacts_root_digest": "sha256:<digest>",
  "validation": {
    "artifact_id": "<opaque-id>",
    "digest": "sha256:<digest>",
    "applies": true,
    "expires_at": "2026-12-31T23:59:59Z",
    "expiry_trigger": null
  },
  "derived_status": "released",
  "approved_at": "2026-07-17T20:00:00Z",
  "approval_records": ["<opaque-id>"],
  "canonicalization": {
    "algorithm": "<frozen-algorithm-id>",
    "version": "<version>"
  },
  "signature": {
    "key_id": "<approved-key-id>",
    "algorithm": "<approved-signature-algorithm>",
    "value": "<signature>"
  }
}
```

`derived_status` is computed from the card's lifecycle gates, applicable
validation, expiry, suspension records, approvals, and artifact consistency.
It is verified rather than trusted as an owner-supplied assertion.

## 5. Manifest schema — annotated shape

The example is explanatory. The generated JSON Schema is normative for
syntax; this document and the referenced contracts are normative for
semantics.

```json
{
  "manifest_schema_version": "1.0",
  "manifest_version": "1.1.0",
  "pack_id": "phebs.grpc.client-calls",
  "pack_claim_version": "1.0.0",

  "claim_contract": {
    "card_artifact_id": "<stable-card-id>",
    "claim_ids": ["grpc-client-call-enumeration"],
    "non_claim_ids": ["runtime-traffic-observation", "dynamic-target-resolution"],
    "workflow_classes": ["batch", "interactive"],
    "supported_languages": [
      {"language": "go", "version_rule": {"id": "...", "version": "...", "digest": "sha256:..."}}
    ],
    "supported_frameworks": [
      {"framework": "grpc-go", "version_rule": {"id": "...", "version": "...", "digest": "sha256:..."}}
    ]
  },

  "implementation": {
    "module_id": "phebs/internal/packs/grpc_client_calls",
    "implementation_schema": {"id": "...", "version": "...", "digest": "sha256:..."},
    "extractor": {"id": "...", "version": "...", "digest": "sha256:..."},
    "adapters": [
      {"kind": "source", "id": "...", "version": "...", "digest": "sha256:..."},
      {"kind": "build", "id": "...", "version": "...", "digest": "sha256:..."}
    ]
  },

  "predicates": [
    {
      "predicate_id": "CALLS_OPERATION",
      "predicate_version": "1.0.0",
      "subject_schema": {"id": "source-occurrence", "version": "1.0", "digest": "sha256:..."},
      "object_schema": {"id": "grpc-operation", "version": "1.0", "digest": "sha256:..."},
      "fact_schema": {"id": "grpc-client-call-fact", "version": "1.0", "digest": "sha256:..."},
      "occurrence_identity_rule": {"id": "...", "version": "...", "digest": "sha256:..."},
      "relationship_identity_rule": {"id": "...", "version": "...", "digest": "sha256:..."},
      "qualifier_schema": {"id": "grpc-client-call-qualifiers", "version": "1.0", "digest": "sha256:..."},
      "evidence_schema": {"id": "source-span-evidence", "version": "1.0", "digest": "sha256:..."},
      "cardinality": "many_subjects_to_one_object",
      "derivation_rule": {"id": "...", "version": "...", "digest": "sha256:..."}
    }
  ],

  "constructs": [
    {
      "construct_id": "direct_generated_client_call",
      "support": "full",
      "detection_rule": {"id": "...", "version": "...", "digest": "sha256:..."},
      "presence_eligible": true,
      "absence_eligible": true,
      "coverage_effect": "analyzed"
    },
    {
      "construct_id": "wrapper_alias",
      "support": "partial",
      "detection_rule": {"id": "...", "version": "...", "digest": "sha256:..."},
      "presence_eligible": true,
      "absence_eligible": false,
      "coverage_effect": "partial",
      "blocker_code": "SEMANTIC_UNRESOLVED",
      "qualification_template_id": "grpc.wrapper-alias-partial"
    }
  ],

  "inputs": [
    {
      "kind": "source",
      "required": true,
      "authority": "git",
      "manifest_schema": {"id": "...", "version": "...", "digest": "sha256:..."},
      "freshness_rule": {"id": "...", "version": "...", "digest": "sha256:..."},
      "on_missing": "refuse_claim_workflow",
      "on_stale": "block_absence_and_decision_workflows"
    }
  ],

  "query": {
    "facets": ["target_role", "source_origin", "reachability", "service", "owner"],
    "census_columns": [
      {"id": "service", "source": "attribution.service", "sortable": true},
      {"id": "owner", "source": "attribution.owner", "sortable": true},
      {"id": "reachability", "source": "qualifiers.reachability", "sortable": false}
    ],
    "ordering_rule": {"id": "logical-fact-order", "version": "1.0", "digest": "sha256:..."}
  },

  "evidence_renderer": {
    "kind": "source_span",
    "renderer_rule": {"id": "...", "version": "...", "digest": "sha256:..."},
    "evidence_authorization_rule": {"id": "...", "version": "...", "digest": "sha256:..."},
    "redaction_rule": {"id": "authorized-occurrence-only", "version": "...", "digest": "sha256:..."},
    "platform_safety_ceiling_ref": "source-excerpt-default"
  },

  "coverage": {
    "denominator_rule": {"id": "independent-target-enumeration", "version": "1.0", "digest": "sha256:..."},
    "ledger_schema": {"id": "coverage-ledger", "version": "1.0", "digest": "sha256:..."},
    "outcome_gate_rule": {"id": "...", "version": "...", "digest": "sha256:..."},
    "absence_eligibility_rule": {"id": "...", "version": "...", "digest": "sha256:..."},
    "core_blocker_schema": {"id": "investigation-core-blockers", "version": "0.2", "digest": "sha256:..."},
    "pack_blocker_extensions": [
      {
        "code": "PHEBS_GRPC_CLIENT_CALLS.EXCLUSION_RATE_EXCEEDED",
        "schema": {"id": "...", "version": "...", "digest": "sha256:..."}
      }
    ],
    "qualification_templates": [
      {
        "template_id": "grpc.analysis-incomplete",
        "template_version": "1.0",
        "template_artifact_digest": "sha256:...",
        "parameter_schema": {"id": "...", "version": "...", "digest": "sha256:..."},
        "allowed_blocker_codes": ["UNITS_FAILED", "UNITS_PARTIAL"],
        "locale": "en-US",
        "escaping_rule": {"id": "...", "version": "...", "digest": "sha256:..."}
      }
    ]
  },

  "decision_rules": [
    {
      "rule": {"id": "grpc-consumer-conclusion", "version": "1.0", "digest": "sha256:..."},
      "input_schema": {"id": "...", "version": "...", "digest": "sha256:..."},
      "output_schema": {"id": "...", "version": "...", "digest": "sha256:..."},
      "conclusions": ["evidenced_conforming", "evidenced_nonconforming", "unknown"],
      "eligible_workflows_by_conclusion": {
        "evidenced_conforming": ["enumerate", "review", "decision_support"],
        "evidenced_nonconforming": ["enumerate", "review", "decision_support"],
        "unknown": ["triage"]
      },
      "unknown_behavior": "typed_unknown"
    }
  ],

  "diff": {
    "comparability_rule": {"id": "...", "version": "...", "digest": "sha256:..."},
    "cause_classifier": {"id": "...", "version": "...", "digest": "sha256:..."},
    "cause_schema": {"id": "investigation-delta-causes", "version": "0.2", "digest": "sha256:..."},
    "removal_eligibility_rule": {"id": "...", "version": "...", "digest": "sha256:..."},
    "removal_permitted_causes": ["relationship_removed_traced"],
    "noncomparable_behavior": "comparison_report_without_fact_delta",
    "unknown_cause_behavior": "withhold_removal_claim"
  },

  "review_projections": [
    {
      "projection_id": "new-consumers",
      "projection_version": "1.0",
      "trigger": {"type": "delta_cause_in", "values": ["relationship_added_traced"]},
      "requires_baseline": true,
      "subject_identity_rule": {"id": "...", "version": "...", "digest": "sha256:..."},
      "lifecycle_rule": {"id": "...", "version": "...", "digest": "sha256:..."},
      "authorization_rule": {"id": "...", "version": "...", "digest": "sha256:..."}
    },
    {
      "projection_id": "coverage-regression",
      "projection_version": "1.0",
      "trigger": {"type": "coverage_gate_transition", "from": ["passed", "warning"], "to": ["failed"]},
      "requires_baseline": true,
      "subject_identity_rule": {"id": "...", "version": "...", "digest": "sha256:..."},
      "lifecycle_rule": {"id": "...", "version": "...", "digest": "sha256:..."},
      "authorization_rule": {"id": "...", "version": "...", "digest": "sha256:..."}
    }
  ],

  "mcp_actions": [
    {
      "tool_id": "find_contract_edges",
      "tool_version": "1.0",
      "input_schema": {"id": "mcp-input-find-contract-edges", "version": "1.0", "digest": "sha256:..."},
      "output_schema": {"id": "mcp-output-find-contract-edges", "version": "1.0", "digest": "sha256:..."},
      "payload_kind": "contract_edges",
      "payload_version": "1.0",
      "workflow_class": "interactive",
      "permission_rule": {"id": "...", "version": "...", "digest": "sha256:..."},
      "limits_profile_id": "interactive-default",
      "refusal_rule": {"id": "...", "version": "...", "digest": "sha256:..."}
    },
    {
      "tool_id": "get_contract_evidence",
      "tool_version": "1.0",
      "input_schema": {"id": "mcp-input-get-contract-evidence", "version": "1.0", "digest": "sha256:..."},
      "output_schema": {"id": "mcp-output-get-contract-evidence", "version": "1.0", "digest": "sha256:..."},
      "payload_kind": "contract_evidence",
      "payload_version": "1.0",
      "workflow_class": "interactive",
      "permission_rule": {"id": "...", "version": "...", "digest": "sha256:..."},
      "limits_profile_id": "interactive-default",
      "refusal_rule": {"id": "...", "version": "...", "digest": "sha256:..."}
    },
    {
      "tool_id": "explain_attribution",
      "tool_version": "1.0",
      "input_schema": {"id": "mcp-input-explain-attribution", "version": "1.0", "digest": "sha256:..."},
      "output_schema": {"id": "mcp-output-explain-attribution", "version": "1.0", "digest": "sha256:..."},
      "payload_kind": "attribution_trace",
      "payload_version": "1.0",
      "workflow_class": "interactive",
      "permission_rule": {"id": "...", "version": "...", "digest": "sha256:..."},
      "limits_profile_id": "interactive-default",
      "refusal_rule": {"id": "...", "version": "...", "digest": "sha256:..."}
    },
    {
      "tool_id": "get_analysis_coverage",
      "tool_version": "1.0",
      "input_schema": {"id": "mcp-input-get-analysis-coverage", "version": "1.0", "digest": "sha256:..."},
      "output_schema": {"id": "mcp-output-get-analysis-coverage", "version": "1.0", "digest": "sha256:..."},
      "payload_kind": "coverage",
      "payload_version": "1.0",
      "workflow_class": "interactive",
      "permission_rule": {"id": "...", "version": "...", "digest": "sha256:..."},
      "limits_profile_id": "interactive-default",
      "refusal_rule": {"id": "...", "version": "...", "digest": "sha256:..."}
    },
    {
      "tool_id": "compare_contract_snapshots",
      "tool_version": "1.0",
      "input_schema": {"id": "mcp-input-compare-contract-snapshots", "version": "1.0", "digest": "sha256:..."},
      "output_schema": {"id": "mcp-output-compare-contract-snapshots", "version": "1.0", "digest": "sha256:..."},
      "payload_kind": "snapshot_comparison",
      "payload_version": "1.0",
      "workflow_class": "interactive",
      "permission_rule": {"id": "...", "version": "...", "digest": "sha256:..."},
      "limits_profile_id": "interactive-default",
      "refusal_rule": {"id": "...", "version": "...", "digest": "sha256:..."}
    },
    {
      "tool_id": "list_new_consumers",
      "tool_version": "1.0",
      "input_schema": {"id": "mcp-input-list-new-consumers", "version": "1.0", "digest": "sha256:..."},
      "output_schema": {"id": "mcp-output-list-new-consumers", "version": "1.0", "digest": "sha256:..."},
      "payload_kind": "new_consumers",
      "payload_version": "1.0",
      "workflow_class": "interactive",
      "permission_rule": {"id": "...", "version": "...", "digest": "sha256:..."},
      "limits_profile_id": "interactive-default",
      "refusal_rule": {"id": "...", "version": "...", "digest": "sha256:..."}
    },
    {
      "tool_id": "verify_proof_reference",
      "tool_version": "1.0",
      "input_schema": {"id": "mcp-input-verify-proof-reference", "version": "1.0", "digest": "sha256:..."},
      "output_schema": {"id": "mcp-output-verify-proof-reference", "version": "1.0", "digest": "sha256:..."},
      "payload_kind": "proof_verification",
      "payload_version": "1.0",
      "workflow_class": "interactive",
      "permission_rule": {"id": "...", "version": "...", "digest": "sha256:..."},
      "limits_profile_id": "interactive-default",
      "refusal_rule": {"id": "...", "version": "...", "digest": "sha256:..."}
    },
    {
      "tool_id": "generate_review_checklist",
      "tool_version": "1.0",
      "input_schema": {"id": "mcp-input-generate-review-checklist", "version": "1.0", "digest": "sha256:..."},
      "output_schema": {"id": "mcp-output-generate-review-checklist", "version": "1.0", "digest": "sha256:..."},
      "payload_kind": "review_requirements",
      "payload_version": "1.0",
      "workflow_class": "interactive",
      "permission_rule": {"id": "...", "version": "...", "digest": "sha256:..."},
      "limits_profile_id": "interactive-default",
      "refusal_rule": {"id": "...", "version": "...", "digest": "sha256:..."}
    }
  ],

  "authorization": {
    "object_access_rule": {"id": "...", "version": "...", "digest": "sha256:..."},
    "evidence_access_rule": {"id": "...", "version": "...", "digest": "sha256:..."},
    "aggregate_disclosure_rule": {"id": "...", "version": "...", "digest": "sha256:..."},
    "comparison_disclosure_rule": {"id": "...", "version": "...", "digest": "sha256:..."},
    "review_disclosure_rule": {"id": "...", "version": "...", "digest": "sha256:..."},
    "proof_resolution_rule": {"id": "...", "version": "...", "digest": "sha256:..."},
    "unknown_or_unauthorized_rule": {"id": "not-available-indistinguishable", "version": "...", "digest": "sha256:..."},
    "pagination_token_rule": {"id": "principal-scope-bound", "version": "...", "digest": "sha256:..."},
    "export_rule": {"id": "...", "version": "...", "digest": "sha256:..."},
    "revocation_and_cache_rule": {"id": "...", "version": "...", "digest": "sha256:..."}
  },

  "operating_profiles": [
    {
      "profile_id": "interactive-default",
      "workflow_class": "interactive",
      "workload_class": "grpc-consumer-query",
      "environment_artifact_id": "<opaque-id>",
      "measurement_artifact": {"id": "...", "digest": "sha256:..."},
      "measured_at": "2026-07-17T20:00:00Z",
      "tested_range": {"measure": "eligible_universe_units", "minimum": 1, "maximum": 300000, "unit": "units"},
      "supported_limit": {"value": 250000, "unit": "units"},
      "safety_margin": {"kind": "absolute", "value": 50000, "unit": "units"},
      "on_exceed": "refuse_with_safe_operating_limit_error",
      "slo_artifact": {"id": "...", "digest": "sha256:..."}
    }
  ],

  "platform_safety_ceilings": [
    {"ceiling_id": "facts-per-page", "measure": "facts", "value": 200, "on_exceed": "paginate"},
    {"ceiling_id": "source-excerpt-default", "measure": "bytes", "value": 4096, "on_exceed": "truncate_and_mark"},
    {"ceiling_id": "source-context-lines", "measure": "lines", "value": 3, "on_exceed": "truncate_and_mark"}
  ],

  "lifecycle": {
    "status_source": "verified_pack_release",
    "load_modes_by_status": {
      "design": [],
      "experimental-dark": ["internal_test"],
      "shadow": ["authorized_evaluation"],
      "released": ["ordinary_claim", "decision_support", "historical_reproduction"],
      "suspended": ["historical_reproduction", "diagnostic"],
      "retired": ["historical_reproduction"]
    },
    "suspension_rule": {"id": "...", "version": "...", "digest": "sha256:..."},
    "historical_reproduction_rule": {"id": "...", "version": "...", "digest": "sha256:..."}
  }
}
```

### Construct-support semantics

`support` is one of `full`, `partial`, or `unsupported`:

- `full` requires the construct to be inside the validated applicability
  population for the stated workflow before it may support an absence claim;
- `partial` requires explicit detection behavior, coverage effect, blocker or
  qualification, and separate presence/absence eligibility; and
- `unsupported` is never silently ignored. It is accounted for as excluded,
  partial, failed, or refused according to the bound coverage rule.

Language, framework, generated-code, vendored-code, test/mock, build-tag,
reflection, wrapper, alias, dynamic-dispatch, and unresolved-target boundaries
must resolve through card-declared construct IDs. The manifest cannot invent a
broader interpretation.

### Blocker vocabulary

Core blocker codes are inherited by schema reference from domain contract
v0.2 and are not copied into each pack manifest. This prevents a pack-local
list from becoming stale. Pack additions must be stable, versioned, prefixed
with the pack namespace, declared by the card, and associated with an approved
qualification template when user-facing wording is required.

### Diff and Review semantics

The comparability rule must evaluate claim and logical identity, schema, rule,
extractor, adapter, universe, enumeration, snapshot policy, build
configuration, external-input semantics, and current principal visibility.
Same Revision is not sufficient by itself.

Only `relationship_removed_traced` may support a relationship-removal claim.
All other domain causes—including authorization, scope, rule, schema,
identity, build, enumeration, metadata, attribution, failure, staleness, and
unknown cause—either explain a non-removal transition or withhold the removal
claim. Noncomparable runs produce a comparison report without an ordinary
fact delta.

Review triggers are typed objects, never free-form expression strings. A
projection binds its version, baseline requirement, subject-identity rule,
lifecycle and supersession rule, and authorization behavior. ReviewItem
identity additionally incorporates the Investigation, comparison or baseline,
authorized subject, delta, and cause as required by the domain contract.

## 6. Release-time validation

Release is blocked unless all checks pass:

1. The signed `PackRelease` verifies and binds the canonical card, manifest,
   implementation, referenced-artifact root, validation artifact, approvals,
   and exact phebs binary/toolchain identities.
2. `pack_id` and `pack_claim_version` agree across the release, card, and
   manifest. No artifact contains an unresolved placeholder.
3. Every manifest claim, predicate, qualifier, evidence type, construct,
   input, non-claim, workflow, and operating profile is present in the card
   with identical or narrower semantics.
4. Every reference resolves in the in-tree registry to the declared type,
   semantic version, digest, and executable implementation. References cannot
   select arbitrary code or files.
5. Construct support is no stronger than the card and validation
   applicability. Partial and unsupported constructs have explicit coverage,
   blocker, qualification, and refusal behavior.
6. The core blocker-schema reference matches the governing domain contract.
   Pack extensions are namespaced, declared by the card, schema-valid, and do
   not shadow core codes.
7. Every qualification template has sealed content, locale, parameter schema,
   allowed blocker mapping, digest, and escaping rule. Every parameter emitted
   by pack code validates against that schema.
8. Decision rules have exhaustive typed outputs and never grant workflows
   beyond the card. Unknown or unmapped inputs yield typed `unknown` or a safe
   refusal, never an inferred conclusion.
9. Diff and projection rules satisfy the domain comparability, causal,
   stable-identity, tombstone, authorization, lifecycle, and baseline
   requirements. Only `relationship_removed_traced` can assert removal.
10. Every MCP action matches an MCP-envelope registry entry by tool name and
    semantic version, input/output schema identity and digest, payload kind and
    version, permission rule, refusal rule, workflow class, and limits profile.
11. Authorization rules cover objects, evidence, aggregates, comparisons,
    Review, proof resolution, identifiers, page tokens, exports, revocation,
    and caches without revealing inaccessible existence, counts, or prior
    visibility.
12. Each supported operating limit is inside a passing measured range after
    its stated safety margin. Batch and interactive classes have separate
    workload/environment evidence where their behavior differs. Platform
    ceilings are equal to or stricter than card limits.
13. Required input authorities, snapshot identities, freshness rules, missing
    and stale behavior, and analysis freshness reconcile with the card and
    validation applicability.
14. The derived lifecycle status permits the requested load mode. Only an
    unexpired, applicable `released` pack may serve ordinary claim-bearing or
    decision-support workflows.

## 7. Runtime obligations

- A loaded manifest and every bound artifact are immutable. Runtime behavior
  resolves only through the verified `PackRelease` and exact in-tree registry.
- The platform reauthorizes each read and enforces authorization before
  materializing facts, counts, denominators, identifiers, diffs, ReviewItems,
  proof results, exports, or page tokens.
- Publication is atomic across facts, coverage, provenance, eligibility, and
  artifact identity. Failed or canceled attempts cannot publish a complete
  fact set.
- Limits are enforced at enumeration, analysis, rendering, pagination, export,
  and MCP boundaries. Exceeding a measured supported limit refuses or safely
  degrades exactly as declared; it never silently changes the claim.
- Envelope fields—including validation, analysis conclusion, absence
  eligibility, qualification, blocker codes, provenance, freshness, and
  result-window state—come only from bound typed rules and schemas.
- Empty facts never become an absence statement without an applicable and
  eligible claim-specific computation over a reconciled, authorized, complete,
  cursor-exhausted result set.
- Suspended or retired packs cannot perform new ordinary claim-bearing work.
  Historical reproduction remains subject to current authorization, retained
  artifacts, and the bound historical-reproduction rule.

## 8. Change classification and revalidation

Every byte change creates a new manifest digest and therefore a new
`PackRelease`; it does **not** automatically imply a new measured claim.
The following minimum impact rules apply:

| Change | Required action |
|---|---|
| Editorial card wording with no semantic effect | New card digest and release binding; recorded no-semantic-change review |
| Query column, ordering, or stricter platform safety ceiling | New manifest version and release binding; product/security review as applicable |
| Looser limit or new workflow/load mode | Card review plus applicable operating and authorization validation before release |
| Predicate, construct, qualifier, evidence, identity, decision, comparability, projection, or negative-wording semantics | New `pack_claim_version`; suspend prior release when applicability changes; revalidate affected claims |
| Extractor, adapter, schema, rule implementation, compiler, or toolchain behavior | New artifact identities and release binding; apply the card's change-impact matrix and revalidate affected measured properties |
| Authorization, redaction, aggregate, proof, export, or token behavior | Security review and authorization regression validation; suspend if non-disclosure could be affected |
| Validation expiry, failed drift check, provenance break, or unresolved release inconsistency | Automatic suspension; no ordinary claim-bearing load |

The card's change-impact matrix may require stricter action. It may not weaken
these minimums.