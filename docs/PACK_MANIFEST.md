# phebs executable pack manifest

*Normative contract, v0.2 · machine counterpart of the
[evidence-pack card](./EVIDENCE_PACK_CARD.md), bound by a signed
PackRelease. Derives from the [domain contract](./INVESTIGATION_DOMAIN_CONTRACT.md)
v0.2 and [MCP envelope](./MCP_ENVELOPE.md) v0.2. Design only.*

## Artifact model

| Artifact | Responsibility |
|---|---|
| Pack Card | human-readable claim, scope, limitations, measured evidence |
| Pack Manifest | machine-readable module behavior and references |
| Rule artifacts | identity, decisions, comparability, projections, authorization — versioned, digested, typed |
| **PackRelease** | signed, immutable binding of all approved digests |

## Release binding (replaces mutual digests)

Card and manifest identify only `pack_id` and their own versions; **neither
embeds the other's digest** (that was circular). The binding is a signed,
append-only record:

```json
{
  "pack_id": "phebs.grpc.client-calls",
  "release_version": "1.0.0",
  "card_digest": "sha256:…",
  "manifest_digest": "sha256:…",
  "implementation_digest": "sha256:…",
  "phebs_binary_digest": "sha256:…",
  "validation_artifact_digest": "sha256:…",
  "rule_artifact_digests": {"identity": "…", "decision": "…", "comparability": "…", "authorization": "…"},
  "approved_at": "…", "approved_by": ["…"], "signature": "…"
}
```

Pack **status is derived** from PackRelease, validation, suspension, and
expiry records — never from a status field inside card or manifest. Release
binds the executable implementation and phebs binary, so behavior cannot
change under an unchanged manifest.

## Formal schema requirements

The manifest ships as JSON validated by a published JSON Schema:
`$id` + schema version; complete `required` lists;
`additionalProperties: false` throughout; string length, numeric bound, and
array limits; exact enums; duplicate-key rejection at parse; canonical
serialization (UTF-8, sorted keys, `,`/`:` separators, LF, single trailing
newline) so digests are reproducible; loaders reject unsupported MAJOR
versions. The loader is fail-closed: unknown fields, unknown enum values,
or unresolved references reject the manifest.

## Version axes and change impact

Distinct axes: `pack_claim_version` · `manifest_version` · extractor and
adapter versions · identity-rule version · decision-rule version ·
release-binding version.

| Change | Consequence |
|---|---|
| claim, predicate semantics, thresholds, identity rules | card review + remeasurement |
| extractor/adapter/implementation bytes | remeasurement or explicit bridging per card; suspension until bound |
| decision/comparability rule version | card review; remeasurement if measured claim touched |
| display config (columns, facets, excerpt size within ceilings) | new manifest_version + new release binding only |
| any digest in the binding | new PackRelease; old release remains valid history |

## Blocker vocabulary

Core blockers are **inherited from the domain contract** (incl.
`UNITS_FAILED`, `UNITS_PARTIAL`, `UNITS_UNRECONCILED`,
`EXCLUSIONS_NOT_PERMITTED`, `OUTCOME_GATE_FAILED`,
`ATTRIBUTION_UNRESOLVED`, `SEMANTIC_UNRESOLVED`, `RESULT_TRUNCATED`,
`SCOPE_NOT_ENUMERATED`, `STALE_ANALYSIS`, `INPUT_STALE`,
`PACK_NOT_RELEASED`, `PACK_VALIDATION_EXPIRED`,
`VALIDATION_NOT_APPLICABLE`, `VISIBILITY_SCOPE_NOT_RECONCILED`); the
manifest may not redefine them. Pack-specific blockers are namespaced
(`go_grpc.EXCLUSION_RATE_EXCEEDED`) and declared in the card, which lists
its complete pack-namespaced additions.

## Predicates as versioned artifacts

Each predicate entry references, by id **and digest**: subject and object
schemas; fact schema; identity rule (source-occurrence identity and
attributed service-operation identity are **separate rules** — attribution
may change while the occurrence does not); evidence schema; qualifier
schemas; cardinality; and derivation/proof requirements for `derived`
facts. No bare field lists like `logical_identity_fields`.

## Rules are artifacts, not expressions

Free-form conditions (`"status == failed"`) are prohibited. Every decision,
comparability, projection, and gate rule is a reference:
`{rule_id, version, digest, input_schema, output_mapping (exhaustive),
unknown_input_behavior}`. Rule artifacts are sealed and signed into the
PackRelease. `support: "partial"` for a construct must state its coverage
consequence, its blocker or qualification mapping, and whether it can
participate in negative claims.

## Comparability and diff

Comparability preconditions (minimum, from the domain contract): same fact
schema, identity rules, extractor/adapter versions, declared universe,
snapshot policy, build configuration, external-input contract, and
principal visibility projection — not merely revision and pack version.
Cause vocabulary is the domain contract's; prohibited-removal causes are a
superset of its prohibited set.

## MCP action registry

Complete action records — no parallel arrays:

```json
{
  "tool_id": "generate_review_checklist",
  "tool_version": "1.0",
  "input_schema": {"id": "…", "digest": "…"},
  "output_schema": {"id": "…", "digest": "…"},
  "payload_kind": "review_requirements",
  "payload_version": "1.0",
  "workflow_class": "decision",
  "permission_rule": "review.checklist",
  "refusal_behavior": "NOT_AVAILABLE",
  "limits": {"max_facts_per_page": 200, "max_excerpt_bytes": 4096}
}
```

Tool names are the envelope registry's (`generate_review_checklist` is the
tool; `review_requirements` is its payload kind).

## Authorization policy references

The manifest binds versioned, digested policy artifacts for: result-time
authorization; evidence/proof resolution; aggregate and count disclosure;
diff and review-item visibility; opaque identifier issuance; pagination
token binding; exports; revocation and cache invalidation; and
unknown-versus-unauthorized indistinguishability. Renderer redaction alone
is insufficient; without these references the loader refuses release mode.

## Qualification templates

Sealed template artifacts: content digest, typed parameter schema,
escaping rules, allowed blocker mappings, authoritative text. The envelope
serves the server-rendered text from these artifacts only.

## Operating limits — classified

Every limit is either a **mirrored measured limit** (from the card's
operating envelope: workload, environment, measurement date, tested range,
safety margin — with the measurement artifact referenced) or a **platform
safety ceiling** (declared as such, enforceable without measurement).
`max_eligible_universe_units`, excerpt bytes, context lines, and page
limits carry this classification explicitly, plus stop/refusal behavior.
Expiry is `expires_at` + `expiry_trigger`.

## Machine-readable card mirrors

The manifest also carries, for enforcement: non-claims; required and
optional inputs with stale-input behavior; unsupported constructs and
blind spots (detectability, effect, representation); validation
applicability conditions; and suspension triggers — each machine-checked
against the card at release.

## Release-time validation

Blocked unless all pass: PackRelease signature and every digest verify;
predicates/constructs/blockers/templates/rules/limits each reconcile with
the card as subset-or-equal; MCP actions ⊆ envelope registry within
authorization-safe bounds; comparability preconditions ⊇ domain-contract
minimum; status derivation yields `released`; canonical serialization
round-trips byte-identically.
