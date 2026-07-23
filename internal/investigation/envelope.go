// Package investigation defines the transport-neutral Investigation result
// contract shared by API projections, MCP tools, and fixture validation.
package investigation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

const (
	EnvelopeVersion = "1.0"

	// StatelessScopeQualificationText is server-owned wording for provisional
	// proof queries that have no independently enumerated pack universe.
	StatelessScopeQualificationText = "The stateless proof query does not enumerate a pack-defined eligible universe; no absence conclusion is supported."
)

type Tool struct {
	Name            string `json:"name"`
	SemanticVersion string `json:"semantic_version"`
}

type Identity struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type Claim struct {
	ClaimFamily    string              `json:"claim_family"`
	Predicate      string              `json:"predicate"`
	Subject        Identity            `json:"subject"`
	Object         *Identity           `json:"object"`
	Filters        map[string][]string `json:"filters"`
	DecisionSought string              `json:"decision_sought"`
}

type Scope struct {
	Claim                    Claim    `json:"claim"`
	NormalizedQuestion       string   `json:"normalized_question"`
	NormalizationAssumptions []string `json:"normalization_assumptions"`
	InvestigationID          *string  `json:"investigation_id"`
	RevisionID               *string  `json:"revision_id"`
	RunArtifactID            *string  `json:"run_artifact_id"`
	SnapshotID               string   `json:"snapshot_id"`
	BuildConfigurationID     string   `json:"build_configuration_id"`
	VisibilityProjectionID   string   `json:"visibility_projection_id"`
}

// Payload holds one of the discriminated payload schemas. Data stays raw so a
// relay can preserve unknown fields within a supported envelope major version.
type Payload struct {
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data"`
}

func NewPayload(kind string, data any) (*Payload, error) {
	if strings.TrimSpace(kind) == "" {
		return nil, errors.New("payload kind is required")
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("marshal %s payload: %w", kind, err)
	}
	return &Payload{Kind: kind, Data: raw}, nil
}

type Processing struct {
	Analyzed int `json:"analyzed"`
	Excluded int `json:"excluded"`
	Partial  int `json:"partial"`
	Failed   int `json:"failed"`
}

type AttributionCount struct {
	Denominator   int `json:"denominator"`
	Resolved      int `json:"resolved"`
	Ambiguous     int `json:"ambiguous"`
	Unresolved    int `json:"unresolved"`
	NotApplicable int `json:"not_applicable"`
}

type OutcomeGate struct {
	Status      string   `json:"status"`
	RuleID      string   `json:"rule_id"`
	RuleVersion string   `json:"rule_version"`
	ReasonCodes []string `json:"reason_codes"`
}

type Coverage struct {
	Visibility           string                      `json:"visibility"`
	EligibleUnits        *int                        `json:"eligible_units"`
	Processing           *Processing                 `json:"processing"`
	ProcessingReconciled bool                        `json:"processing_reconciled"`
	ExclusionsByReason   map[string]int              `json:"exclusions_by_reason"`
	OutcomeGates         *OutcomeGate                `json:"outcome_gates"`
	Attribution          map[string]AttributionCount `json:"attribution"`
}

type AnalysisConclusion struct {
	Value             string   `json:"value"`
	EvidenceBasis     string   `json:"evidence_basis"`
	RuleID            string   `json:"rule_id"`
	RuleVersion       string   `json:"rule_version"`
	ReasonCodes       []string `json:"reason_codes"`
	InputReferences   []string `json:"input_references"`
	EligibleWorkflows []string `json:"eligible_workflows"`
}

type Qualification struct {
	TemplateID        string         `json:"template_id"`
	TemplateVersion   string         `json:"template_version"`
	Parameters        map[string]any `json:"parameters"`
	AuthoritativeText string         `json:"authoritative_text"`
}

type AbsenceEligibility struct {
	Applicable    bool           `json:"applicable"`
	Eligible      bool           `json:"eligible"`
	RuleVersion   string         `json:"rule_version"`
	BlockerCodes  []string       `json:"blocker_codes"`
	Qualification *Qualification `json:"qualification"`
}

type Validation struct {
	PackID                string   `json:"pack_id"`
	PackVersion           string   `json:"pack_version"`
	PackStatus            string   `json:"pack_status"`
	WorkflowEligibility   string   `json:"workflow_eligibility"`
	ValidationReference   string   `json:"validation_reference"`
	Applies               bool     `json:"applies"`
	ApplicabilityBlockers []string `json:"applicability_blockers"`
	MeasuredClaimID       string   `json:"measured_claim_id"`
	ExpiresAt             string   `json:"expires_at"`
	ExpiryTrigger         *string  `json:"expiry_trigger"`
}

type Provenance struct {
	QueryDigest       string            `json:"query_digest"`
	InputManifestID   string            `json:"input_manifest_id"`
	PackManifestID    string            `json:"pack_manifest_id"`
	ExtractorVersion  string            `json:"extractor_version"`
	AdapterVersions   map[string]string `json:"adapter_versions"`
	RuleVersions      map[string]string `json:"rule_versions"`
	SchemaVersion     string            `json:"schema_version"`
	PhebsSourceCommit string            `json:"phebs_source_commit"`
	PhebsBinaryDigest string            `json:"phebs_binary_digest"`
	ToolchainDigest   string            `json:"toolchain_digest"`
}

type FreshnessAnalysis struct {
	Status            string   `json:"status"`
	Published         string   `json:"published,omitempty"`
	SnapshotCommitted string   `json:"snapshot_committed,omitempty"`
	EvaluatedAt       string   `json:"evaluated_at,omitempty"`
	RuleID            string   `json:"rule_id,omitempty"`
	RuleVersion       string   `json:"rule_version,omitempty"`
	ExpiresAt         string   `json:"expires_at,omitempty"`
	ReasonCodes       []string `json:"reason_codes,omitempty"`
}

type FreshnessInput struct {
	Kind                 string   `json:"kind"`
	Required             bool     `json:"required"`
	Authority            string   `json:"authority"`
	SnapshotID           string   `json:"snapshot_id"`
	AsOf                 string   `json:"as_of"`
	FreshnessRuleID      string   `json:"freshness_rule_id"`
	FreshnessRuleVersion string   `json:"freshness_rule_version"`
	EvaluatedAt          string   `json:"evaluated_at"`
	ExpiresAt            string   `json:"expires_at"`
	Status               string   `json:"status"`
	ReasonCodes          []string `json:"reason_codes"`
}

type Freshness struct {
	Analysis FreshnessAnalysis `json:"analysis"`
	Inputs   []FreshnessInput  `json:"inputs"`
}

type Ordering struct {
	Key       string `json:"key"`
	Direction string `json:"direction"`
}

type ResultWindow struct {
	ResultSetComplete bool            `json:"result_set_complete"`
	PageExhausted     bool            `json:"page_exhausted"`
	Truncated         bool            `json:"truncated"`
	TruncatedReason   *string         `json:"truncated_reason"`
	Returned          int             `json:"returned"`
	NextPageToken     *string         `json:"next_page_token"`
	Ordering          json.RawMessage `json:"ordering"`
}

func StringOrdering(value string) json.RawMessage {
	raw, _ := json.Marshal(value)
	return raw
}

type Refusal struct {
	Code              string `json:"code"`
	AuthoritativeText string `json:"authoritative_text"`
}

type EnvelopeError struct {
	Code              string `json:"code"`
	Retryable         bool   `json:"retryable"`
	Message           string `json:"message,omitempty"`
	RetryAfterSeconds *int   `json:"retry_after_seconds,omitempty"`
}

type Envelope struct {
	EnvelopeVersion    string              `json:"envelope_version"`
	RequestID          string              `json:"request_id"`
	GeneratedAt        string              `json:"generated_at"`
	Tool               Tool                `json:"tool"`
	Outcome            string              `json:"outcome"`
	Scope              *Scope              `json:"scope,omitempty"`
	Payload            *Payload            `json:"payload,omitempty"`
	Coverage           *Coverage           `json:"coverage,omitempty"`
	AnalysisConclusion *AnalysisConclusion `json:"analysis_conclusion,omitempty"`
	AbsenceEligibility *AbsenceEligibility `json:"absence_eligibility,omitempty"`
	Validation         *Validation         `json:"validation,omitempty"`
	Provenance         *Provenance         `json:"provenance,omitempty"`
	Freshness          *Freshness          `json:"freshness,omitempty"`
	ResultWindow       *ResultWindow       `json:"result_window,omitempty"`
	Refusal            *Refusal            `json:"refusal,omitempty"`
	Errors             []EnvelopeError     `json:"errors"`
}

type AttributionState struct {
	State       string   `json:"state"`
	ReasonCodes []string `json:"reason_codes"`
}

type SemanticResolution struct {
	Value       string   `json:"value"`
	ReasonCodes []string `json:"reason_codes"`
}

type ProofReference struct {
	OccurrenceID       string `json:"occurrence_id"`
	ProofID            string `json:"proof_id"`
	SnapshotID         string `json:"snapshot_id"`
	VerificationAction string `json:"verification_action"`
}

type Fact struct {
	FactID                string                      `json:"fact_id"`
	LogicalRelationshipID string                      `json:"logical_relationship_id"`
	Predicate             string                      `json:"predicate"`
	Subject               Identity                    `json:"subject"`
	Object                Identity                    `json:"object"`
	Qualifiers            map[string]string           `json:"qualifiers"`
	EvidenceBasis         string                      `json:"evidence_basis"`
	SemanticResolution    SemanticResolution          `json:"semantic_resolution"`
	AttributionStates     map[string]AttributionState `json:"attribution_states"`
	ProofReferences       []ProofReference            `json:"proof_references"`
	VerificationTool      string                      `json:"verification_tool"`
}

type ContractEdgesData struct {
	Facts []Fact `json:"facts"`
}

type ComparabilityRule struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

type ComparisonReport struct {
	LeftRun             string            `json:"left_run"`
	RightRun            string            `json:"right_run"`
	PerSideCoverageOnly bool              `json:"per_side_coverage_only"`
	Deltas              []ComparisonDelta `json:"deltas"`
}

type ComparisonDelta struct {
	LogicalRelationshipID string  `json:"logical_relationship_id"`
	Change                string  `json:"change"`
	Cause                 string  `json:"cause"`
	BeforeFactID          *string `json:"before_fact_id"`
	AfterFactID           *string `json:"after_fact_id"`
}

type SnapshotComparisonData struct {
	Comparable              bool              `json:"comparable"`
	ComparabilityRule       ComparabilityRule `json:"comparability_rule"`
	NonComparabilityReasons []string          `json:"non_comparability_reasons"`
	ComparisonReport        ComparisonReport  `json:"comparison_report"`
}

// ValidateSemantics enforces cross-field rules that JSON Schema cannot
// express. Structural validation against the generated schemas happens first
// at the MCP boundary and in the conformance suite.
func (e Envelope) ValidateSemantics() error {
	if e.EnvelopeVersion != EnvelopeVersion || e.RequestID == "" ||
		e.GeneratedAt == "" || e.Tool.Name == "" || e.Tool.SemanticVersion == "" {
		return errors.New("envelope identity is incomplete")
	}
	if !slices.Contains([]string{"ok", "partial", "refused", "error"}, e.Outcome) {
		return fmt.Errorf("unknown outcome %q", e.Outcome)
	}
	if e.Errors == nil {
		return errors.New("errors must be an array")
	}
	if e.Outcome == "refused" {
		if e.Refusal == nil || e.Refusal.Code == "" || e.Refusal.AuthoritativeText == "" {
			return errors.New("refused outcome requires a server-rendered refusal")
		}
	} else if e.Refusal != nil {
		return errors.New("non-refused outcome cannot carry a refusal")
	}
	if e.Outcome != "error" && len(e.Errors) != 0 {
		return errors.New("successful envelope cannot carry execution errors")
	}
	if e.Refusal != nil && e.Refusal.Code == "NOT_AVAILABLE" {
		if e.Scope != nil || e.Payload != nil || e.Coverage != nil ||
			e.AnalysisConclusion != nil || e.AbsenceEligibility != nil ||
			e.Validation != nil || e.Provenance != nil || e.Freshness != nil ||
			e.ResultWindow != nil {
			return errors.New("NOT_AVAILABLE refusal discloses authorized-only sections")
		}
		return nil
	}
	if e.Outcome == "error" {
		if len(e.Errors) == 0 {
			return errors.New("error outcome requires a safe structured error")
		}
		if e.Payload != nil || e.AnalysisConclusion != nil {
			return errors.New("error outcome carries unpublished claim content")
		}
		return nil
	}
	if e.Outcome == "refused" && e.Payload != nil {
		return errors.New("refused envelope carries a claim payload")
	}
	if e.Scope == nil || e.AbsenceEligibility == nil || e.ResultWindow == nil {
		return errors.New("authorized envelope is missing common semantic sections")
	}
	if err := validateCoverage(e.Coverage); err != nil {
		return err
	}
	factCount, err := validatePayload(e.Payload)
	if err != nil {
		return err
	}
	if e.Payload != nil && e.Payload.Kind == "contract_edges" &&
		e.ResultWindow.Returned != factCount {
		return errors.New("contract-edge returned count does not match facts")
	}
	if e.ResultWindow.Returned < 0 {
		return errors.New("result-window returned count is negative")
	}
	if err := validateAbsence(*e.AbsenceEligibility, e); err != nil {
		return err
	}
	if e.ResultWindow.Truncated {
		if e.Outcome != "partial" || e.ResultWindow.ResultSetComplete ||
			!e.ResultWindow.PageExhausted || e.ResultWindow.NextPageToken != nil ||
			e.ResultWindow.TruncatedReason == nil || *e.ResultWindow.TruncatedReason == "" ||
			e.AbsenceEligibility.Eligible ||
			!slices.Contains(e.AbsenceEligibility.BlockerCodes, "RESULT_TRUNCATED") ||
			e.AbsenceEligibility.Qualification == nil {
			return errors.New("truncation is not represented as irreversible and absence-blocking")
		}
	} else if e.ResultWindow.TruncatedReason != nil {
		return errors.New("non-truncated result carries a truncation reason")
	}
	return nil
}

func validateCoverage(coverage *Coverage) error {
	if coverage == nil {
		return nil
	}
	if coverage.Visibility != "available" && coverage.Visibility != "withheld" {
		return errors.New("coverage visibility is invalid")
	}
	if coverage.Visibility == "withheld" {
		if coverage.EligibleUnits != nil || coverage.Processing != nil ||
			coverage.ProcessingReconciled || len(coverage.ExclusionsByReason) != 0 ||
			coverage.OutcomeGates != nil || len(coverage.Attribution) != 0 {
			return errors.New("withheld coverage discloses counts")
		}
		return nil
	}
	if coverage.EligibleUnits == nil || coverage.Processing == nil {
		return errors.New("available coverage omits counts")
	}
	values := []int{
		*coverage.EligibleUnits, coverage.Processing.Analyzed,
		coverage.Processing.Excluded, coverage.Processing.Partial,
		coverage.Processing.Failed,
	}
	for _, value := range values {
		if value < 0 {
			return errors.New("coverage count is negative")
		}
	}
	if coverage.ProcessingReconciled &&
		coverage.Processing.Analyzed+coverage.Processing.Excluded+
			coverage.Processing.Partial+coverage.Processing.Failed != *coverage.EligibleUnits {
		return errors.New("processing coverage does not reconcile")
	}
	excluded := 0
	for _, count := range coverage.ExclusionsByReason {
		if count < 0 {
			return errors.New("exclusion count is negative")
		}
		excluded += count
	}
	if excluded != coverage.Processing.Excluded {
		return errors.New("exclusion reasons do not reconcile")
	}
	for hop, count := range coverage.Attribution {
		if count.Denominator < 0 || count.Resolved < 0 || count.Ambiguous < 0 ||
			count.Unresolved < 0 || count.NotApplicable < 0 ||
			count.Resolved+count.Ambiguous+count.Unresolved+count.NotApplicable != count.Denominator {
			return fmt.Errorf("attribution hop %q does not reconcile", hop)
		}
	}
	return nil
}

func validatePayload(payload *Payload) (int, error) {
	if payload == nil {
		return 0, nil
	}
	switch payload.Kind {
	case "contract_edges":
		var data ContractEdgesData
		if err := strictPayload(payload.Data, &data); err != nil {
			return 0, fmt.Errorf("contract edges payload: %w", err)
		}
		for _, fact := range data.Facts {
			if fact.FactID == "" || fact.LogicalRelationshipID == "" ||
				fact.Predicate == "" || fact.Subject.ID == "" || fact.Object.ID == "" {
				return 0, errors.New("fact identity is incomplete")
			}
			if fact.EvidenceBasis == "evidenced" && len(fact.ProofReferences) == 0 {
				return 0, fmt.Errorf("evidenced fact %q has no proof reference", fact.FactID)
			}
			for _, proof := range fact.ProofReferences {
				if proof.ProofID == "" || proof.OccurrenceID == "" ||
					proof.SnapshotID == "" || proof.VerificationAction == "" {
					return 0, fmt.Errorf("fact %q has an incomplete proof reference", fact.FactID)
				}
			}
		}
		return len(data.Facts), nil
	case "snapshot_comparison":
		var data SnapshotComparisonData
		if err := strictPayload(payload.Data, &data); err != nil {
			return 0, fmt.Errorf("snapshot comparison payload: %w", err)
		}
		if !data.Comparable && (len(data.NonComparabilityReasons) == 0 ||
			!data.ComparisonReport.PerSideCoverageOnly ||
			len(data.ComparisonReport.Deltas) != 0) {
			return 0, errors.New("non-comparable result presents a semantic delta")
		}
		if data.Comparable && len(data.NonComparabilityReasons) != 0 {
			return 0, errors.New("comparable result carries non-comparability reasons")
		}
		if data.Comparable && data.ComparisonReport.PerSideCoverageOnly {
			return 0, errors.New("comparable result is limited to per-side coverage")
		}
		if data.Comparable {
			for _, delta := range data.ComparisonReport.Deltas {
				switch delta.Change {
				case "added", "reintroduced":
					if delta.Cause != "relationship_added_traced" ||
						delta.BeforeFactID != nil || delta.AfterFactID == nil {
						return 0, errors.New("comparison contains an untraced addition")
					}
				case "removed":
					if delta.Cause != "relationship_removed_traced" ||
						delta.BeforeFactID == nil || delta.AfterFactID != nil {
						return 0, errors.New("comparison contains an untraced removal")
					}
				case "modified":
					if delta.Cause != "source_modified" ||
						delta.BeforeFactID == nil || delta.AfterFactID == nil {
						return 0, errors.New("comparison contains an untraced modification")
					}
				default:
					return 0, fmt.Errorf("comparison contains unknown change %q", delta.Change)
				}
			}
		}
		return 0, nil
	default:
		if len(payload.Data) == 0 || bytes.Equal(payload.Data, []byte("null")) {
			return 0, fmt.Errorf("payload %q has no data", payload.Kind)
		}
		return 0, nil
	}
}

func validateAbsence(a AbsenceEligibility, e Envelope) error {
	if a.RuleVersion == "" || a.BlockerCodes == nil {
		return errors.New("absence eligibility is incomplete")
	}
	if !a.Applicable {
		if a.Eligible {
			return errors.New("inapplicable absence result is eligible")
		}
		return nil
	}
	if a.Eligible {
		if len(a.BlockerCodes) != 0 || a.Qualification == nil ||
			e.Validation == nil || e.Validation.PackStatus != "released" ||
			!e.Validation.Applies || e.Validation.WorkflowEligibility != "eligible" ||
			e.ResultWindow == nil || !e.ResultWindow.ResultSetComplete ||
			!e.ResultWindow.PageExhausted || e.ResultWindow.Truncated {
			return errors.New("eligible absence result lacks its prerequisites")
		}
		if count, err := validatePayload(e.Payload); err != nil || count != 0 {
			return errors.New("eligible absence result contains facts")
		}
		return nil
	}
	if len(a.BlockerCodes) == 0 || a.Qualification == nil {
		return errors.New("blocked absence result lacks blockers or qualification")
	}
	if a.Qualification.TemplateID == "" || a.Qualification.TemplateVersion == "" ||
		a.Qualification.AuthoritativeText == "" || a.Qualification.Parameters == nil {
		return errors.New("qualification is not server-renderable")
	}
	return nil
}

func strictPayload(raw json.RawMessage, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("payload contains trailing data")
		}
		return fmt.Errorf("read payload trailer: %w", err)
	}
	return nil
}
