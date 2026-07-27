package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bmeddeb/phebs/internal/compat"
	"github.com/bmeddeb/phebs/internal/investigation"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	mcpToolSemanticVersion = "1.0"
	statelessAbsenceRule   = "stateless-proof-scope-v1"
)

// FindOperationConsumersMCP projects the shared proof result into the
// Investigation envelope. Querying and authorization remain in the same core
// used by HTTP; this method only gives MCP the normative result contract.
func (s *ProofService) FindOperationConsumersMCP(ctx context.Context, operation string) (*investigation.Envelope, error) {
	bundle, err := s.FindOperationConsumers(ctx, operation)
	if err != nil {
		return nil, err
	}
	return proofEnvelope("find_operation_consumers", bundle)
}

// FindProtoFieldReferencesMCP projects the shared field-reference proof.
func (s *ProofService) FindProtoFieldReferencesMCP(
	ctx context.Context,
	lineage, message string,
	fieldNumber int,
) (*investigation.Envelope, error) {
	bundle, err := s.FindProtoFieldReferences(ctx, lineage, message, fieldNumber)
	if err != nil {
		return nil, err
	}
	return proofEnvelope("find_proto_field_references", bundle)
}

// FindFieldReferencesMCP projects the shared protocol-neutral field proof.
func (s *ProofService) FindFieldReferencesMCP(
	ctx context.Context,
	lineage, message string,
	fieldNumber int,
) (*investigation.Envelope, error) {
	bundle, err := s.FindFieldReferences(ctx, lineage, message, fieldNumber)
	if err != nil {
		return nil, err
	}
	return proofEnvelope("find_field_references", bundle)
}

// FindKafkaTopicUsageMCP projects the shared topic-usage proof. The
// envelope's facts are the producer/consumer evidence rows; the persisted
// bundle (by envelope provenance ID) additionally carries the
// always-present unresolved census, and the claim text keeps the
// no-completeness posture explicit.
func (s *ProofService) FindKafkaTopicUsageMCP(ctx context.Context, topic string) (*investigation.Envelope, error) {
	bundle, err := s.FindKafkaTopicUsage(ctx, topic)
	if err != nil {
		return nil, err
	}
	return proofEnvelope("find_kafka_topic_usage", bundle)
}

// GetExtractionCoverageMCP projects the shared permission-scoped certificate.
func (s *ProofService) GetExtractionCoverageMCP(ctx context.Context, domains []string) (*investigation.Envelope, error) {
	bundle, err := s.GetExtractionCoverage(ctx, domains)
	if err != nil {
		return nil, err
	}
	return proofEnvelope("get_extraction_coverage", bundle)
}

// CheckContractCompatibilityMCP projects the pinned Buf result and its
// evidence-derived consumers into the same envelope.
func (s *ProofService) CheckContractCompatibilityMCP(
	ctx context.Context,
	request compat.Request,
) (*investigation.Envelope, error) {
	bundle, err := s.CheckContractCompatibility(ctx, request)
	if err != nil {
		return nil, err
	}
	return proofEnvelope("check_contract_compatibility", bundle)
}

func proofEnvelope(toolName string, source *ProofBundleEnvelope) (*investigation.Envelope, error) {
	if source == nil {
		return nil, fmt.Errorf("project MCP envelope: proof bundle is nil")
	}
	switch source.Bundle.Query.Kind {
	case "find_proto_field_references", "find_field_references",
		"contract_impact_field":
		if source.Bundle.Query.FieldNumber == nil {
			return nil, fmt.Errorf(
				"project MCP envelope: field number is missing",
			)
		}
	}
	payload, facts, err := proofPayload(*source)
	if err != nil {
		return nil, err
	}
	queryDigest, err := canonicalDigest(source.Bundle.Query)
	if err != nil {
		return nil, fmt.Errorf("project MCP envelope: digest query: %w", err)
	}
	claim := proofClaim(source.Bundle.Query)
	absence := &investigation.AbsenceEligibility{
		Applicable:    payload.Kind == "contract_edges" && facts == 0,
		Eligible:      false,
		RuleVersion:   statelessAbsenceRule,
		BlockerCodes:  []string{},
		Qualification: nil,
	}
	if absence.Applicable {
		absence.BlockerCodes = []string{"SCOPE_NOT_ENUMERATED"}
		absence.Qualification = &investigation.Qualification{
			TemplateID:        "stateless-scope-not-enumerated",
			TemplateVersion:   "1.0",
			Parameters:        map[string]any{},
			AuthoritativeText: investigation.StatelessScopeQualificationText,
		}
	}
	extractors := make([]string, 0, len(source.Bundle.ExtractorVersions))
	for _, extractor := range source.Bundle.ExtractorVersions {
		extractors = append(extractors, extractor.Extractor)
	}
	sort.Strings(extractors)
	extractors = compactStrings(extractors)
	extractorVersion := strings.Join(extractors, ",")
	if extractorVersion == "" {
		extractorVersion = "unavailable"
	}
	envelope := &investigation.Envelope{
		EnvelopeVersion: investigation.EnvelopeVersion,
		RequestID:       source.ID,
		GeneratedAt:     time.Now().UTC().Truncate(time.Second).Format(time.RFC3339),
		Tool: investigation.Tool{
			Name: toolName, SemanticVersion: mcpToolSemanticVersion,
		},
		Outcome: "partial",
		Scope: &investigation.Scope{
			Claim:                    claim,
			NormalizedQuestion:       normalizedProofQuestion(source.Bundle.Query),
			NormalizationAssumptions: []string{},
			InvestigationID:          nil,
			RevisionID:               nil,
			RunArtifactID:            nil,
			SnapshotID:               source.Bundle.Coverage.Digest,
			BuildConfigurationID:     "published-extraction-runs",
			VisibilityProjectionID:   source.Bundle.VisibilityContext.VisibleRepositorySetDigest,
		},
		Payload: payload,
		Coverage: &investigation.Coverage{
			Visibility:           "withheld",
			EligibleUnits:        nil,
			Processing:           nil,
			ProcessingReconciled: false,
			ExclusionsByReason:   map[string]int{},
			OutcomeGates:         nil,
			Attribution:          map[string]investigation.AttributionCount{},
		},
		AnalysisConclusion: compatibilityConclusion(source.Bundle.Compatibility),
		AbsenceEligibility: absence,
		Validation:         nil,
		Provenance: &investigation.Provenance{
			QueryDigest:      queryDigest,
			InputManifestID:  source.Bundle.Coverage.Digest,
			PackManifestID:   "not-applicable:stateless-proof",
			ExtractorVersion: extractorVersion,
			AdapterVersions:  map[string]string{},
			RuleVersions: map[string]string{
				"absence": statelessAbsenceRule,
			},
			SchemaVersion:     investigation.EnvelopeVersion,
			PhebsSourceCommit: "unavailable",
			PhebsBinaryDigest: "unavailable",
			ToolchainDigest:   proofToolchainDigest(source.Bundle),
		},
		Freshness: &investigation.Freshness{
			Analysis: investigation.FreshnessAnalysis{
				Status:      proofFreshness(source.Bundle),
				ReasonCodes: []string{},
			},
			Inputs: []investigation.FreshnessInput{},
		},
		ResultWindow: &investigation.ResultWindow{
			ResultSetComplete: false,
			PageExhausted:     true,
			Truncated:         false,
			TruncatedReason:   nil,
			Returned:          facts,
			NextPageToken:     nil,
			Ordering:          investigation.StringOrdering("logical_relationship_id ascending"),
		},
		Refusal: nil,
		Errors:  []investigation.EnvelopeError{},
	}
	if err := envelope.ValidateSemantics(); err != nil {
		return nil, fmt.Errorf("project MCP envelope: %w", err)
	}
	return envelope, nil
}

func proofPayload(source ProofBundleEnvelope) (*investigation.Payload, int, error) {
	if source.Bundle.Query.Kind == "get_extraction_coverage" {
		payload, err := investigation.NewPayload("coverage", map[string]any{
			"certificate": source.Bundle.Coverage,
		})
		return payload, 0, err
	}
	facts := make([]investigation.Fact, 0, len(source.Bundle.Assertions))
	evidenceByAtom := make(map[string]BundleEvidence, len(source.Bundle.Evidence))
	for _, evidence := range source.Bundle.Evidence {
		evidenceByAtom[proofEvidenceKey(evidence.Repository, evidence.RunID, evidence.Atom.ID)] = evidence
	}
	// CALLS_OPERATION is shared across protocol packs, so its object kind is
	// derived from the emitting run's domain via the bundle certificate
	// (T19.5). Predicate-distinct facts keep their predicate-derived kinds.
	domainByRun := make(map[string]string)
	for _, repository := range source.Bundle.Coverage.Repositories {
		for _, run := range repository.Runs {
			if run.RunID != "" {
				domainByRun[run.RunID] = run.Domain
			}
		}
	}
	for _, assertion := range source.Bundle.Assertions {
		references := make([]investigation.ProofReference, 0)
		qualifiers := map[string]string{
			"tier": assertion.Tier, "repository": assertion.Repo,
		}
		if assertion.CodeRole != "" {
			qualifiers["code_role"] = assertion.CodeRole
		}
		if assertion.Lineage != "" {
			qualifiers["lineage"] = assertion.Lineage
		}
		subjectID := assertion.Subject
		for _, atomID := range assertion.Supporting {
			evidence, ok := evidenceByAtom[proofEvidenceKey(assertion.Repo, assertion.RunID, atomID)]
			if !ok {
				return nil, 0, fmt.Errorf("project MCP envelope: assertion %q lacks atom %q", assertion.ID, atomID)
			}
			for _, occurrence := range evidence.Occurrences {
				if len(references) == 0 {
					subjectID = occurrence.ID
					qualifiers["path"] = occurrence.Path
					qualifiers["start_line"] = strconv.Itoa(occurrence.StartLine)
					qualifiers["end_line"] = strconv.Itoa(occurrence.EndLine)
					qualifiers["commit"] = occurrence.Commit
				}
				references = append(references, investigation.ProofReference{
					OccurrenceID:       occurrence.ID,
					ProofID:            source.ID + "#" + evidence.Atom.ID,
					SnapshotID:         occurrence.Commit,
					VerificationAction: "GET /api/proof_bundles/" + source.ID,
				})
			}
		}
		semantic := investigation.SemanticResolution{Value: "resolved", ReasonCodes: []string{}}
		if assertion.Tier == store.TierUnresolved {
			semantic = investigation.SemanticResolution{
				Value: "unresolved", ReasonCodes: []string{"EXTRACTOR_UNRESOLVED"},
			}
		}
		objectKind := "contract_identity"
		objectID := assertion.Object
		switch assertion.Predicate {
		case "CALLS_OPERATION":
			objectKind = "grpc_operation"
			if domainByRun[assertion.RunID] == "thrift-consumer" {
				objectKind = "thrift_operation"
			}
		case "REFERENCES_PROTO_FIELD":
			objectKind = "proto_field"
			objectID = assertion.Lineage + ":" + assertion.Object
		case "REFERENCES_THRIFT_FIELD":
			objectKind = "thrift_field"
			objectID = assertion.Lineage + ":" + assertion.Object
		case "PRODUCES_TO_TOPIC", "CONSUMES_FROM_TOPIC":
			// A topic object is a source spelling with no cluster or
			// environment identity; lineage is per-file provisional and is
			// deliberately not part of the topic identity.
			objectKind = "kafka_topic"
		}
		attribution := make(map[string]investigation.AttributionState, 4)
		for _, hop := range []string{"build_target", "deployable", "owner", "service"} {
			attribution[hop] = investigation.AttributionState{
				State: "unresolved", ReasonCodes: []string{"ATTRIBUTION_NOT_COMPUTED"},
			}
		}
		facts = append(facts, investigation.Fact{
			FactID:                assertion.ID,
			LogicalRelationshipID: assertion.ID,
			Predicate:             assertion.Predicate,
			Subject:               investigation.Identity{Kind: "source_occurrence", ID: subjectID},
			Object:                investigation.Identity{Kind: objectKind, ID: objectID},
			Qualifiers:            qualifiers,
			EvidenceBasis:         "evidenced",
			SemanticResolution:    semantic,
			AttributionStates:     attribution,
			ProofReferences:       references,
			VerificationTool:      "proof_bundle_api",
		})
	}
	payload, err := investigation.NewPayload(
		"contract_edges",
		investigation.ContractEdgesData{Facts: facts},
	)
	return payload, len(facts), err
}

func proofEvidenceKey(repository, runID, atomID string) string {
	return repository + "\x00" + runID + "\x00" + atomID
}

func proofClaim(query ProofQuery) investigation.Claim {
	claim := investigation.Claim{
		ClaimFamily: "contract_relationship",
		Predicate:   "",
		Subject: investigation.Identity{
			Kind: "visible_repository_universe", ID: "current-principal",
		},
		Object:         nil,
		Filters:        map[string][]string{},
		DecisionSought: "enumerate_evidence",
	}
	switch query.Kind {
	case "find_operation_consumers":
		claim.Predicate = "CALLS_OPERATION"
		// The bare operation identity carries no protocol, so the query
		// subject is protocol-neutral; per-fact object kinds stay
		// domain-derived (T19.5).
		claim.Subject = investigation.Identity{Kind: "rpc_operation", ID: query.Operation}
		claim.DecisionSought = "enumerate_matching_call_evidence"
	case "find_proto_field_references":
		claim.Predicate = "REFERENCES_PROTO_FIELD"
		claim.Subject = investigation.Identity{
			Kind: "proto_field",
			ID:   query.Lineage + ":" + query.Message + "#" + strconv.Itoa(*query.FieldNumber),
		}
		claim.DecisionSought = "enumerate_proto_field_reference_candidates"
	case "find_field_references":
		claim.Predicate = "REFERENCES_FIELD"
		claim.Subject = investigation.Identity{
			Kind: "contract_field",
			ID:   query.Lineage + ":" + query.Message + "#" + strconv.Itoa(*query.FieldNumber),
		}
		claim.DecisionSought = "enumerate_field_reference_candidates"
	case "find_kafka_topic_usage":
		claim.Predicate = "USES_KAFKA_TOPIC"
		claim.Subject = investigation.Identity{Kind: "kafka_topic", ID: "topic:" + query.Topic}
		claim.DecisionSought = "enumerate_kafka_topic_usage_candidates"
	case "check_contract_compatibility":
		claim.ClaimFamily = "contract_compatibility"
		claim.Predicate = "WIRE_COMPATIBILITY"
		claim.Subject = investigation.Identity{Kind: "contract_lineage", ID: query.Lineage}
		claim.Filters["before_digest"] = []string{query.BeforeDigest}
		claim.Filters["after_digest"] = []string{query.AfterDigest}
		claim.DecisionSought = "assess_wire_compatibility"
	case "get_extraction_coverage":
		claim.ClaimFamily = "processing_coverage"
		claim.Predicate = "HAS_EXTRACTION_COVERAGE"
		claim.Filters["domains"] = append([]string(nil), query.Domains...)
		claim.DecisionSought = "inspect_extraction_coverage"
	}
	return claim
}

func normalizedProofQuestion(query ProofQuery) string {
	switch query.Kind {
	case "find_operation_consumers":
		return "Find matching static call evidence for " + query.Operation +
			" in the current visibility projection; this bare-operation query does not establish declaration identity."
	case "find_proto_field_references":
		return "Find evidence-derived references to " + query.Lineage + ":" +
			query.Message + "#" + strconv.Itoa(*query.FieldNumber) + " in the current visibility projection."
	case "find_field_references":
		return "Find evidence-derived protocol-qualified references to " +
			query.Lineage + ":" + query.Message + "#" +
			strconv.Itoa(*query.FieldNumber) +
			" in the current visibility projection."
	case "find_kafka_topic_usage":
		return "Find evidence-derived producers and consumers of topic:" + query.Topic +
			" in the current visibility projection; unresolved sites are counted in the bundle census and this list is never complete."
	case "check_contract_compatibility":
		return "Check Buf WIRE compatibility for " + query.Lineage +
			" and enumerate affected field-reference evidence."
	default:
		return "Describe extraction coverage for the current visibility projection."
	}
}

func compatibilityConclusion(result *compat.CompatibilityResult) *investigation.AnalysisConclusion {
	if result == nil {
		return nil
	}
	value := "compatible"
	if !result.Compatible {
		value = "breaking"
	}
	reasons := make([]string, 0, len(result.Violations))
	for _, violation := range result.Violations {
		reasons = append(reasons, violation.Rule)
	}
	sort.Strings(reasons)
	reasons = compactStrings(reasons)
	return &investigation.AnalysisConclusion{
		Value:         value,
		EvidenceBasis: "derived",
		RuleID:        "buf-" + strings.ToLower(result.Run.Policy),
		RuleVersion:   result.Run.Version,
		ReasonCodes:   reasons,
		InputReferences: []string{
			result.Before.Digest, result.After.Digest,
		},
		EligibleWorkflows: []string{},
	}
}

func proofFreshness(bundle ProofBundle) string {
	status := "current"
	for _, repository := range bundle.Coverage.Repositories {
		for _, run := range repository.Runs {
			if run.Status != "published" {
				return "unavailable"
			}
			if !run.Fresh || len(run.Failures) != 0 ||
				run.LatestAttempt != nil && run.LatestAttempt.Status == "aborted" {
				status = "stale"
			}
		}
	}
	return status
}

func proofToolchainDigest(bundle ProofBundle) string {
	if bundle.Compatibility != nil {
		return "buf:" + bundle.Compatibility.Run.Version + ":" + bundle.Compatibility.Run.Policy
	}
	versions := make([]string, 0, len(bundle.ExtractorVersions))
	for _, extractor := range bundle.ExtractorVersions {
		versions = append(versions, extractor.Extractor)
	}
	sort.Strings(versions)
	sum := sha256.Sum256([]byte(strings.Join(versions, "\x00")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func canonicalDigest(value any) (string, error) {
	content, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func compactStrings(values []string) []string {
	if len(values) == 0 {
		return values
	}
	result := values[:1]
	for _, value := range values[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}
