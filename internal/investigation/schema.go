package investigation

import "fmt"

const schemaBaseID = "https://phebs.local/schemas/"

var schemaFilenames = []string{
	"mcp-envelope-v1.0.json",
	"mcp-payload-contract-edges-v1.0.json",
	"mcp-payload-contract-evidence-v1.0.json",
	"mcp-payload-attribution-trace-v1.0.json",
	"mcp-payload-coverage-v1.0.json",
	"mcp-payload-snapshot-comparison-v1.0.json",
	"mcp-payload-new-consumers-v1.0.json",
	"mcp-payload-proof-verification-v1.0.json",
	"mcp-payload-review-requirements-v1.0.json",
}

// SchemaFilenames returns the complete deterministic T16.6 schema set.
func SchemaFilenames() []string { return append([]string(nil), schemaFilenames...) }

// SchemaDocuments returns fresh JSON-Schema documents. The generator and MCP
// registration both call this function, so checked-in schemas and advertised
// outputSchema definitions have one source of truth.
func SchemaDocuments() map[string]map[string]any {
	return map[string]map[string]any{
		"mcp-envelope-v1.0.json":                    envelopeSchema(),
		"mcp-payload-contract-edges-v1.0.json":      contractEdgesPayloadSchema(),
		"mcp-payload-contract-evidence-v1.0.json":   contractEvidencePayloadSchema(),
		"mcp-payload-attribution-trace-v1.0.json":   attributionTracePayloadSchema(),
		"mcp-payload-coverage-v1.0.json":            coveragePayloadSchema(),
		"mcp-payload-snapshot-comparison-v1.0.json": snapshotComparisonPayloadSchema(),
		"mcp-payload-new-consumers-v1.0.json":       newConsumersPayloadSchema(),
		"mcp-payload-proof-verification-v1.0.json":  proofVerificationPayloadSchema(),
		"mcp-payload-review-requirements-v1.0.json": reviewRequirementsPayloadSchema(),
	}
}

// EnvelopeOutputSchema returns the common envelope with one discriminated
// payload schema installed. Payload remains optional because a refusal may
// successfully return only its safe refusal section.
func EnvelopeOutputSchema(payloadFilename string) (map[string]any, error) {
	documents := SchemaDocuments()
	envelope := documents["mcp-envelope-v1.0.json"]
	payload, ok := documents[payloadFilename]
	if !ok || payloadFilename == "mcp-envelope-v1.0.json" {
		return nil, fmt.Errorf("unknown MCP payload schema %q", payloadFilename)
	}
	delete(payload, "$schema")
	delete(payload, "$id")
	properties := envelope["properties"].(map[string]any)
	properties["payload"] = payload
	return envelope, nil
}

func envelopeSchema() map[string]any {
	identity := objectSchema(
		[]string{"kind", "id"},
		map[string]any{"kind": boundedString(128), "id": boundedString(65536)},
	)
	claim := objectSchema(
		[]string{"claim_family", "predicate", "subject", "object", "filters", "decision_sought"},
		map[string]any{
			"claim_family": boundedString(256),
			"predicate":    boundedString(256),
			"subject":      identity,
			"object":       nullable(identity),
			"filters": map[string]any{
				"type": "object", "maxProperties": 128,
				"additionalProperties": arraySchema(boundedString(4096), 1024),
			},
			"decision_sought": boundedString(256),
		},
	)
	scope := objectSchema(
		[]string{
			"claim", "normalized_question", "normalization_assumptions",
			"investigation_id", "revision_id", "run_artifact_id", "snapshot_id",
			"build_configuration_id", "visibility_projection_id",
		},
		map[string]any{
			"claim":                     claim,
			"normalized_question":       boundedString(65536),
			"normalization_assumptions": arraySchema(boundedString(4096), 128),
			"investigation_id":          nullable(boundedString(65536)),
			"revision_id":               nullable(boundedString(65536)),
			"run_artifact_id":           nullable(boundedString(65536)),
			"snapshot_id":               boundedString(65536),
			"build_configuration_id":    boundedString(65536),
			"visibility_projection_id":  boundedString(65536),
		},
	)
	processing := objectSchema(
		[]string{"analyzed", "excluded", "partial", "failed"},
		map[string]any{
			"analyzed": nonNegativeInteger(), "excluded": nonNegativeInteger(),
			"partial": nonNegativeInteger(), "failed": nonNegativeInteger(),
		},
	)
	attributionCount := objectSchema(
		[]string{"denominator", "resolved", "ambiguous", "unresolved", "not_applicable"},
		map[string]any{
			"denominator": nonNegativeInteger(), "resolved": nonNegativeInteger(),
			"ambiguous": nonNegativeInteger(), "unresolved": nonNegativeInteger(),
			"not_applicable": nonNegativeInteger(),
		},
	)
	outcomeGate := objectSchema(
		[]string{"status", "rule_id", "rule_version", "reason_codes"},
		map[string]any{
			"status":  enumString("passed", "failed", "unknown"),
			"rule_id": boundedString(65536), "rule_version": boundedString(256),
			"reason_codes": arraySchema(boundedString(65536), 1024),
		},
	)
	coverage := objectSchema(
		[]string{
			"visibility", "eligible_units", "processing", "processing_reconciled",
			"exclusions_by_reason", "outcome_gates", "attribution",
		},
		map[string]any{
			"visibility":            enumString("available", "withheld"),
			"eligible_units":        nullable(nonNegativeInteger()),
			"processing":            nullable(processing),
			"processing_reconciled": map[string]any{"type": "boolean"},
			"exclusions_by_reason": map[string]any{
				"type": "object", "maxProperties": 4096,
				"additionalProperties": nonNegativeInteger(),
			},
			"outcome_gates": nullable(outcomeGate),
			"attribution": map[string]any{
				"type": "object", "maxProperties": 64,
				"additionalProperties": attributionCount,
			},
		},
	)
	analysisConclusion := objectSchema(
		[]string{
			"value", "evidence_basis", "rule_id", "rule_version", "reason_codes",
			"input_references", "eligible_workflows",
		},
		map[string]any{
			"value": boundedString(256), "evidence_basis": enumString("evidenced", "derived", "unknown"),
			"rule_id": boundedString(65536), "rule_version": boundedString(256),
			"reason_codes":       arraySchema(boundedString(65536), 1024),
			"input_references":   arraySchema(boundedString(65536), 4096),
			"eligible_workflows": arraySchema(boundedString(256), 256),
		},
	)
	qualification := objectSchema(
		[]string{"template_id", "template_version", "parameters", "authoritative_text"},
		map[string]any{
			"template_id": boundedString(65536), "template_version": boundedString(256),
			"parameters": map[string]any{
				"type": "object", "maxProperties": 256,
				"additionalProperties": true,
			},
			"authoritative_text": boundedString(65536),
		},
	)
	absence := objectSchema(
		[]string{"applicable", "eligible", "rule_version", "blocker_codes", "qualification"},
		map[string]any{
			"applicable":    map[string]any{"type": "boolean"},
			"eligible":      map[string]any{"type": "boolean"},
			"rule_version":  boundedString(256),
			"blocker_codes": arraySchema(boundedString(65536), 1024),
			"qualification": nullable(qualification),
		},
	)
	validation := objectSchema(
		[]string{
			"pack_id", "pack_version", "pack_status", "workflow_eligibility",
			"validation_reference", "applies", "applicability_blockers",
			"measured_claim_id", "expires_at", "expiry_trigger",
		},
		map[string]any{
			"pack_id": boundedString(65536), "pack_version": boundedString(256),
			"pack_status":            enumString("design", "experimental-dark", "shadow", "released", "suspended", "retired"),
			"workflow_eligibility":   enumString("eligible", "advisory", "refused"),
			"validation_reference":   boundedString(65536),
			"applies":                map[string]any{"type": "boolean"},
			"applicability_blockers": arraySchema(boundedString(65536), 1024),
			"measured_claim_id":      boundedString(65536),
			"expires_at":             boundedString(256),
			"expiry_trigger":         nullable(boundedString(65536)),
		},
	)
	provenance := objectSchema(
		[]string{
			"query_digest", "input_manifest_id", "pack_manifest_id",
			"extractor_version", "adapter_versions", "rule_versions",
			"schema_version", "phebs_source_commit", "phebs_binary_digest",
			"toolchain_digest",
		},
		map[string]any{
			"query_digest": boundedString(65536), "input_manifest_id": boundedString(65536),
			"pack_manifest_id": boundedString(65536), "extractor_version": boundedString(65536),
			"adapter_versions": stringMapSchema(256), "rule_versions": stringMapSchema(256),
			"schema_version": boundedString(256), "phebs_source_commit": boundedString(65536),
			"phebs_binary_digest": boundedString(65536), "toolchain_digest": boundedString(65536),
		},
	)
	freshnessAnalysis := objectSchema(
		[]string{"status"},
		map[string]any{
			"status":    enumString("current", "stale", "unavailable", "unknown"),
			"published": boundedString(256), "snapshot_committed": boundedString(256),
			"evaluated_at": boundedString(256), "rule_id": boundedString(65536),
			"rule_version": boundedString(256), "expires_at": boundedString(256),
			"reason_codes": arraySchema(boundedString(65536), 1024),
		},
	)
	freshnessInput := objectSchema(
		[]string{
			"kind", "required", "authority", "snapshot_id", "as_of",
			"freshness_rule_id", "freshness_rule_version", "evaluated_at",
			"expires_at", "status", "reason_codes",
		},
		map[string]any{
			"kind": boundedString(256), "required": map[string]any{"type": "boolean"},
			"authority": boundedString(256), "snapshot_id": boundedString(65536),
			"as_of": boundedString(256), "freshness_rule_id": boundedString(65536),
			"freshness_rule_version": boundedString(256), "evaluated_at": boundedString(256),
			"expires_at":   boundedString(256),
			"status":       enumString("current", "stale", "unavailable", "unknown"),
			"reason_codes": arraySchema(boundedString(65536), 1024),
		},
	)
	freshness := objectSchema(
		[]string{"analysis", "inputs"},
		map[string]any{
			"analysis": freshnessAnalysis,
			"inputs":   arraySchema(freshnessInput, 4096),
		},
	)
	orderingObject := objectSchema(
		[]string{"key", "direction"},
		map[string]any{
			"key": boundedString(256), "direction": enumString("ascending", "descending"),
		},
	)
	resultWindow := objectSchema(
		[]string{
			"result_set_complete", "page_exhausted", "truncated",
			"truncated_reason", "returned", "next_page_token", "ordering",
		},
		map[string]any{
			"result_set_complete": map[string]any{"type": "boolean"},
			"page_exhausted":      map[string]any{"type": "boolean"},
			"truncated":           map[string]any{"type": "boolean"},
			"truncated_reason":    nullable(boundedString(65536)),
			"returned":            nonNegativeInteger(),
			"next_page_token":     nullable(boundedString(65536)),
			"ordering": map[string]any{
				"oneOf": []any{boundedString(256), orderingObject},
			},
		},
	)
	refusal := objectSchema(
		[]string{"code", "authoritative_text"},
		map[string]any{
			"code": enumString(
				"NOT_AVAILABLE", "UNSUPPORTED_CLAIM", "PACK_WORKFLOW_NOT_ELIGIBLE",
				"ACTION_NOT_PERMITTED", "RATE_LIMITED",
			),
			"authoritative_text": boundedString(65536),
		},
	)
	envelopeError := objectSchema(
		[]string{"code", "retryable"},
		map[string]any{
			"code":                enumString("INVALID_ARGUMENT", "DEPENDENCY_UNAVAILABLE", "EXECUTION_FAILED", "INTERNAL_ERROR"),
			"retryable":           map[string]any{"type": "boolean"},
			"message":             boundedString(65536),
			"retry_after_seconds": nullable(nonNegativeInteger()),
		},
	)
	payload := objectSchema(
		[]string{"kind", "data"},
		map[string]any{
			"kind": enumString(
				"contract_edges", "contract_evidence", "attribution_trace", "coverage",
				"snapshot_comparison", "new_consumers", "proof_verification", "review_requirements",
			),
			"data": map[string]any{"type": "object"},
		},
	)
	schema := objectSchema(
		[]string{"envelope_version", "request_id", "generated_at", "tool", "outcome", "errors"},
		map[string]any{
			"envelope_version": map[string]any{"const": EnvelopeVersion, "type": "string"},
			"request_id":       boundedString(65536),
			"generated_at":     boundedString(256),
			"tool": objectSchema(
				[]string{"name", "semantic_version"},
				map[string]any{"name": boundedString(256), "semantic_version": boundedString(256)},
			),
			"outcome": enumString("ok", "partial", "refused", "error"),
			"scope":   nullable(scope), "payload": nullable(payload), "coverage": nullable(coverage),
			"analysis_conclusion": nullable(analysisConclusion),
			"absence_eligibility": nullable(absence), "validation": nullable(validation),
			"provenance": nullable(provenance), "freshness": nullable(freshness),
			"result_window": nullable(resultWindow), "refusal": nullable(refusal),
			"errors": arraySchema(envelopeError, 128),
		},
	)
	schema["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	schema["$id"] = schemaBaseID + "mcp-envelope-v1.0.json"
	schema["title"] = "phebs MCP envelope v1.0"
	return schema
}

func contractEdgesPayloadSchema() map[string]any {
	return payloadDocument(
		"mcp-payload-contract-edges-v1.0.json", "contract_edges",
		objectSchema(
			[]string{"facts"},
			map[string]any{"facts": arraySchema(contractFactSchema(), 5000)},
		),
	)
}

func contractFactSchema() map[string]any {
	identity := objectSchema(
		[]string{"kind", "id"},
		map[string]any{"kind": boundedString(128), "id": boundedString(65536)},
	)
	state := objectSchema(
		[]string{"state", "reason_codes"},
		map[string]any{
			"state":        enumString("resolved", "ambiguous", "unresolved", "not_applicable"),
			"reason_codes": arraySchema(boundedString(65536), 1024),
		},
	)
	proof := objectSchema(
		[]string{"occurrence_id", "proof_id", "snapshot_id", "verification_action"},
		map[string]any{
			"occurrence_id": boundedString(65536), "proof_id": boundedString(65536),
			"snapshot_id": boundedString(65536), "verification_action": boundedString(256),
		},
	)
	fact := objectSchema(
		[]string{
			"fact_id", "logical_relationship_id", "predicate", "subject", "object",
			"qualifiers", "evidence_basis", "semantic_resolution",
			"attribution_states", "proof_references", "verification_tool",
		},
		map[string]any{
			"fact_id": boundedString(65536), "logical_relationship_id": boundedString(65536),
			"predicate": boundedString(256), "subject": identity, "object": identity,
			"qualifiers":     stringMapSchema(256),
			"evidence_basis": enumString("evidenced", "derived"),
			"semantic_resolution": objectSchema(
				[]string{"value", "reason_codes"},
				map[string]any{
					"value":        enumString("resolved", "ambiguous", "unresolved", "not_applicable"),
					"reason_codes": arraySchema(boundedString(65536), 1024),
				},
			),
			"attribution_states": map[string]any{
				"type": "object", "maxProperties": 64, "additionalProperties": state,
			},
			"proof_references":  arraySchema(proof, 4096),
			"verification_tool": boundedString(256),
		},
	)
	return fact
}

func snapshotComparisonPayloadSchema() map[string]any {
	rule := objectSchema(
		[]string{"id", "version", "digest"},
		map[string]any{
			"id": boundedString(65536), "version": boundedString(256), "digest": boundedString(65536),
		},
	)
	delta := objectSchema(
		[]string{
			"logical_relationship_id", "change", "cause",
			"before_fact_id", "after_fact_id",
		},
		map[string]any{
			"logical_relationship_id": boundedString(65536),
			"change": enumString(
				"added", "removed", "reintroduced", "modified",
			),
			"cause": enumString(
				"relationship_added_traced",
				"relationship_removed_traced",
				"source_modified",
			),
			"before_fact_id": nullable(boundedString(65536)),
			"after_fact_id":  nullable(boundedString(65536)),
		},
	)
	report := objectSchema(
		[]string{"left_run", "right_run", "per_side_coverage_only", "deltas"},
		map[string]any{
			"left_run": boundedString(65536), "right_run": boundedString(65536),
			"per_side_coverage_only": map[string]any{"type": "boolean"},
			"deltas":                 arraySchema(delta, 5000),
		},
	)
	return payloadDocument(
		"mcp-payload-snapshot-comparison-v1.0.json", "snapshot_comparison",
		objectSchema(
			[]string{"comparable", "comparability_rule", "non_comparability_reasons", "comparison_report"},
			map[string]any{
				"comparable":                map[string]any{"type": "boolean"},
				"comparability_rule":        rule,
				"non_comparability_reasons": arraySchema(boundedString(65536), 1024),
				"comparison_report":         report,
			},
		),
	)
}

func contractEvidencePayloadSchema() map[string]any {
	evidence := objectSchema(
		[]string{"mode", "blob_digest", "encoding", "redaction_state", "snapshot_id"},
		map[string]any{
			"mode": enumString("embedded", "locator"), "blob_digest": boundedString(65536),
			"encoding": enumString("utf-8", "binary"), "redaction_state": boundedString(256),
			"snapshot_id": boundedString(65536), "excerpt": boundedString(1 << 20),
			"locator":    nullable(boundedString(65536)),
			"start_byte": nonNegativeInteger(), "end_byte": nonNegativeInteger(),
		},
	)
	return payloadDocument(
		"mcp-payload-contract-evidence-v1.0.json", "contract_evidence",
		objectSchema(
			[]string{"fact", "evidence"},
			map[string]any{
				"fact":     contractFactSchema(),
				"evidence": evidence,
			},
		),
	)
}

func attributionTracePayloadSchema() map[string]any {
	hop := objectSchema(
		[]string{"hop", "state", "identities", "reason_codes", "derivation_reference"},
		map[string]any{
			"hop":                  boundedString(256),
			"state":                enumString("resolved", "ambiguous", "unresolved", "not_applicable"),
			"identities":           arraySchema(boundedString(65536), 1024),
			"reason_codes":         arraySchema(boundedString(65536), 1024),
			"derivation_reference": nullable(boundedString(65536)),
		},
	)
	return payloadDocument(
		"mcp-payload-attribution-trace-v1.0.json", "attribution_trace",
		objectSchema(
			[]string{"fact", "hops"},
			map[string]any{
				"fact": contractFactSchema(),
				"hops": arraySchema(hop, 64),
			},
		),
	)
}

func coveragePayloadSchema() map[string]any {
	return payloadDocument(
		"mcp-payload-coverage-v1.0.json", "coverage",
		objectSchema(
			[]string{"certificate"},
			map[string]any{
				"certificate": map[string]any{
					"type":     "object",
					"required": []string{"schema_version", "domains", "repository_count", "repositories", "digest"},
					"properties": map[string]any{
						"schema_version":   boundedString(256),
						"domains":          arraySchema(boundedString(256), 64),
						"repository_count": nonNegativeInteger(),
						"repositories": arraySchema(
							map[string]any{"type": "object", "additionalProperties": true},
							10000,
						),
						"digest": boundedString(65536),
					},
					"additionalProperties": false,
				},
			},
		),
	)
}

func newConsumersPayloadSchema() map[string]any {
	return payloadDocument(
		"mcp-payload-new-consumers-v1.0.json", "new_consumers",
		objectSchema(
			[]string{"baseline_id", "facts"},
			map[string]any{
				"baseline_id": boundedString(65536),
				"facts":       arraySchema(contractFactSchema(), 5000),
			},
		),
	)
}

func proofVerificationPayloadSchema() map[string]any {
	return payloadDocument(
		"mcp-payload-proof-verification-v1.0.json", "proof_verification",
		objectSchema(
			[]string{"proof_id", "authorized", "integrity"},
			map[string]any{
				"proof_id":      boundedString(65536),
				"authorized":    map[string]any{"type": "boolean"},
				"integrity":     enumString("verified", "failed", "unavailable"),
				"occurrence_id": boundedString(65536), "snapshot_id": boundedString(65536),
				"evidence": nullable(map[string]any{"type": "object", "additionalProperties": true}),
			},
		),
	)
}

func reviewRequirementsPayloadSchema() map[string]any {
	requirement := objectSchema(
		[]string{"id", "text", "authority", "required"},
		map[string]any{
			"id": boundedString(65536), "text": boundedString(65536),
			"authority": boundedString(256), "required": map[string]any{"type": "boolean"},
		},
	)
	return payloadDocument(
		"mcp-payload-review-requirements-v1.0.json", "review_requirements",
		objectSchema(
			[]string{"requirements"},
			map[string]any{"requirements": arraySchema(requirement, 1024)},
		),
	)
}

func payloadDocument(filename, kind string, data map[string]any) map[string]any {
	schema := objectSchema(
		[]string{"kind", "data"},
		map[string]any{
			"kind": map[string]any{"type": "string", "const": kind},
			"data": data,
		},
	)
	schema["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	schema["$id"] = schemaBaseID + filename
	schema["title"] = "phebs MCP " + kind + " payload v1.0"
	return schema
}

func objectSchema(required []string, properties map[string]any) map[string]any {
	return map[string]any{
		"type":                 "object",
		"required":             required,
		"properties":           properties,
		"additionalProperties": false,
	}
}

func arraySchema(items any, max int) map[string]any {
	return map[string]any{
		"type": "array", "items": items, "maxItems": max,
	}
}

func boundedString(max int) map[string]any {
	return map[string]any{"type": "string", "minLength": 1, "maxLength": max}
}

func enumString(values ...string) map[string]any {
	enum := make([]any, len(values))
	for i, value := range values {
		enum[i] = value
	}
	return map[string]any{"type": "string", "enum": enum}
}

func nonNegativeInteger() map[string]any {
	return map[string]any{"type": "integer", "minimum": 0}
}

func nullable(schema map[string]any) map[string]any {
	return map[string]any{"oneOf": []any{schema, map[string]any{"type": "null"}}}
}

func stringMapSchema(max int) map[string]any {
	return map[string]any{
		"type": "object", "maxProperties": max,
		"additionalProperties": boundedString(65536),
	}
}
