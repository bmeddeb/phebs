package store

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

const (
	consumerComparisonRuleID      = "consumer-edge-comparability"
	consumerComparisonRuleVersion = "1.0"
)

// ConsumerEdgeFact is the stable relationship identity retained independently
// of an extraction run. FactID is the evidence publication occurrence;
// LogicalRelationshipID is the cross-snapshot identity used by the ledger.
type ConsumerEdgeFact struct {
	FactID                string `json:"fact_id"`
	LogicalRelationshipID string `json:"logical_relationship_id"`
	UnitID                string `json:"unit_id,omitempty"`
	Predicate             string `json:"predicate"`
	SubjectKind           string `json:"subject_kind"`
	SubjectID             string `json:"subject_id"`
	ObjectKind            string `json:"object_kind"`
	ObjectID              string `json:"object_id"`
}

// ConsumerSnapshotSemantics contains every frozen dimension required by
// contract §8 before an ordinary fact delta is permitted. DeclaredUniverse is
// the principal's independently authorized unit set, not a hidden global set.
type ConsumerSnapshotSemantics struct {
	ClaimIdentity          string   `json:"claim_identity"`
	FactSchema             string   `json:"fact_schema"`
	RuleIdentity           string   `json:"rule_identity"`
	ExtractorIdentity      string   `json:"extractor_identity"`
	AdapterIdentity        string   `json:"adapter_identity"`
	IdentitySemantics      string   `json:"identity_semantics"`
	DeclaredUniverse       []string `json:"declared_universe"`
	EnumerationMethod      string   `json:"enumeration_method"`
	SnapshotPolicy         string   `json:"snapshot_policy"`
	BuildConfiguration     string   `json:"build_configuration"`
	ExternalInputSemantics string   `json:"external_input_semantics"`
	VisibilityProjection   string   `json:"visibility_projection"`
	AnalysisComplete       bool     `json:"analysis_complete"`
	InputsFresh            bool     `json:"inputs_fresh"`
}

// ConsumerSnapshot is one principal-scoped published fact census.
type ConsumerSnapshot struct {
	Principal       string                    `json:"principal"`
	InvestigationID string                    `json:"investigation_id"`
	RevisionID      string                    `json:"revision_id"`
	ArtifactID      string                    `json:"artifact_id"`
	SourceSnapshot  string                    `json:"source_snapshot"`
	Semantics       ConsumerSnapshotSemantics `json:"semantics"`
	Facts           []ConsumerEdgeFact        `json:"facts"`
}

type ConsumerDelta struct {
	LogicalRelationshipID string            `json:"logical_relationship_id"`
	Change                string            `json:"change"`
	Cause                 string            `json:"cause"`
	Before                *ConsumerEdgeFact `json:"before"`
	After                 *ConsumerEdgeFact `json:"after"`
}

// ConsumerComparison is either an ordinary cause-classified delta or a
// comparison report with per-side coverage only. Non-comparable reports
// structurally carry no deltas.
type ConsumerComparison struct {
	ComparisonID           string          `json:"comparison_id"`
	LeftArtifactID         string          `json:"left_artifact_id,omitempty"`
	RightArtifactID        string          `json:"right_artifact_id"`
	Comparable             bool            `json:"comparable"`
	RuleID                 string          `json:"rule_id"`
	RuleVersion            string          `json:"rule_version"`
	RuleDigest             string          `json:"rule_digest"`
	NonComparabilityCauses []string        `json:"non_comparability_causes"`
	PerSideCoverageOnly    bool            `json:"per_side_coverage_only"`
	Deltas                 []ConsumerDelta `json:"deltas"`
}

func normalizeConsumerSnapshot(in ConsumerSnapshot) (ConsumerSnapshot, error) {
	for name, value := range map[string]string{
		"principal": in.Principal, "investigation id": in.InvestigationID,
		"revision id": in.RevisionID, "artifact id": in.ArtifactID,
		"source snapshot":          in.SourceSnapshot,
		"claim identity":           in.Semantics.ClaimIdentity,
		"fact schema":              in.Semantics.FactSchema,
		"rule identity":            in.Semantics.RuleIdentity,
		"extractor identity":       in.Semantics.ExtractorIdentity,
		"adapter identity":         in.Semantics.AdapterIdentity,
		"identity semantics":       in.Semantics.IdentitySemantics,
		"enumeration method":       in.Semantics.EnumerationMethod,
		"snapshot policy":          in.Semantics.SnapshotPolicy,
		"build configuration":      in.Semantics.BuildConfiguration,
		"external input semantics": in.Semantics.ExternalInputSemantics,
		"visibility projection":    in.Semantics.VisibilityProjection,
	} {
		if err := validateDomainString(name, value, true); err != nil {
			return ConsumerSnapshot{}, err
		}
	}
	if !validInvestigationPrincipal(in.Principal) || !validULID(in.InvestigationID) ||
		!strings.HasPrefix(in.ArtifactID, "ira_") {
		return ConsumerSnapshot{}, errors.New("consumer snapshot has invalid principal or parent identity")
	}
	var err error
	in.Semantics.DeclaredUniverse, err = canonicalStrings(
		"declared universe unit",
		in.Semantics.DeclaredUniverse,
	)
	if err != nil {
		return ConsumerSnapshot{}, err
	}
	if len(in.Facts) > 5000 {
		return ConsumerSnapshot{}, errors.New("consumer snapshot exceeds 5000 facts")
	}
	in.Facts = slices.Clone(in.Facts)
	for i := range in.Facts {
		for name, value := range map[string]string{
			"fact id":                 in.Facts[i].FactID,
			"logical relationship id": in.Facts[i].LogicalRelationshipID,
			"unit id":                 in.Facts[i].UnitID,
			"predicate":               in.Facts[i].Predicate,
			"subject kind":            in.Facts[i].SubjectKind,
			"subject id":              in.Facts[i].SubjectID,
			"object kind":             in.Facts[i].ObjectKind,
			"object id":               in.Facts[i].ObjectID,
		} {
			required := name != "unit id"
			if err := validateDomainString(name, value, required); err != nil {
				return ConsumerSnapshot{}, err
			}
		}
	}
	slices.SortFunc(in.Facts, func(a, b ConsumerEdgeFact) int {
		return strings.Compare(a.LogicalRelationshipID, b.LogicalRelationshipID)
	})
	for i := 1; i < len(in.Facts); i++ {
		if in.Facts[i-1].LogicalRelationshipID == in.Facts[i].LogicalRelationshipID {
			return ConsumerSnapshot{}, fmt.Errorf(
				"duplicate logical relationship %q",
				in.Facts[i].LogicalRelationshipID,
			)
		}
	}
	return in, nil
}

// CompareConsumerSnapshots applies the fail-closed contract §8 matrix.
// Missing facts become removals only when every frozen semantic dimension is
// compatible and both sides are complete and fresh.
func CompareConsumerSnapshots(left, right ConsumerSnapshot) (ConsumerComparison, error) {
	var err error
	left, err = normalizeConsumerSnapshot(left)
	if err != nil {
		return ConsumerComparison{}, fmt.Errorf("left consumer snapshot: %w", err)
	}
	right, err = normalizeConsumerSnapshot(right)
	if err != nil {
		return ConsumerComparison{}, fmt.Errorf("right consumer snapshot: %w", err)
	}
	if left.InvestigationID != right.InvestigationID {
		return ConsumerComparison{}, errors.New("consumer snapshots belong to different investigations")
	}
	causes := consumerNonComparabilityCauses(left, right)
	ruleDigest, _ := contentDigest(struct {
		ID, Version string
	}{consumerComparisonRuleID, consumerComparisonRuleVersion})
	comparison := ConsumerComparison{
		LeftArtifactID: left.ArtifactID, RightArtifactID: right.ArtifactID,
		Comparable: len(causes) == 0,
		RuleID:     consumerComparisonRuleID, RuleVersion: consumerComparisonRuleVersion,
		RuleDigest: ruleDigest, NonComparabilityCauses: causes,
		PerSideCoverageOnly: len(causes) != 0,
		Deltas:              []ConsumerDelta{},
	}
	comparison.ComparisonID = hashIdentity(
		"iccmp_",
		comparison.RuleID,
		comparison.RuleVersion,
		left.ArtifactID,
		right.ArtifactID,
		left.Semantics.VisibilityProjection,
		right.Semantics.VisibilityProjection,
	)
	if !comparison.Comparable {
		return comparison, nil
	}
	leftFacts := make(map[string]ConsumerEdgeFact, len(left.Facts))
	rightFacts := make(map[string]ConsumerEdgeFact, len(right.Facts))
	for _, fact := range left.Facts {
		leftFacts[fact.LogicalRelationshipID] = fact
	}
	for _, fact := range right.Facts {
		rightFacts[fact.LogicalRelationshipID] = fact
	}
	for _, fact := range left.Facts {
		if _, ok := rightFacts[fact.LogicalRelationshipID]; ok {
			continue
		}
		before := fact
		comparison.Deltas = append(comparison.Deltas, ConsumerDelta{
			LogicalRelationshipID: fact.LogicalRelationshipID,
			Change:                "removed", Cause: "relationship_removed_traced",
			Before: &before, After: nil,
		})
	}
	for _, fact := range right.Facts {
		if _, ok := leftFacts[fact.LogicalRelationshipID]; ok {
			continue
		}
		after := fact
		comparison.Deltas = append(comparison.Deltas, ConsumerDelta{
			LogicalRelationshipID: fact.LogicalRelationshipID,
			Change:                "added", Cause: "relationship_added_traced",
			Before: nil, After: &after,
		})
	}
	slices.SortFunc(comparison.Deltas, func(a, b ConsumerDelta) int {
		return strings.Compare(a.LogicalRelationshipID, b.LogicalRelationshipID)
	})
	return comparison, nil
}

func consumerNonComparabilityCauses(left, right ConsumerSnapshot) []string {
	causes := make([]string, 0, 8)
	add := func(cause string) {
		if !slices.Contains(causes, cause) {
			causes = append(causes, cause)
		}
	}
	if left.Principal != right.Principal ||
		left.Semantics.VisibilityProjection != right.Semantics.VisibilityProjection {
		add("authorization_changed")
	}
	if left.Semantics.ClaimIdentity != right.Semantics.ClaimIdentity ||
		left.Semantics.FactSchema != right.Semantics.FactSchema ||
		left.Semantics.IdentitySemantics != right.Semantics.IdentitySemantics {
		add("schema_or_identity_changed")
	}
	if left.Semantics.RuleIdentity != right.Semantics.RuleIdentity ||
		left.Semantics.ExtractorIdentity != right.Semantics.ExtractorIdentity ||
		left.Semantics.AdapterIdentity != right.Semantics.AdapterIdentity {
		add("pack_or_rule_changed")
	}
	if !slices.Equal(left.Semantics.DeclaredUniverse, right.Semantics.DeclaredUniverse) {
		switch {
		case isStringSubset(left.Semantics.DeclaredUniverse, right.Semantics.DeclaredUniverse):
			add("scope_expanded")
		case isStringSubset(right.Semantics.DeclaredUniverse, left.Semantics.DeclaredUniverse):
			add("scope_narrowed")
		default:
			add("unknown_cause")
		}
	}
	if left.Semantics.EnumerationMethod != right.Semantics.EnumerationMethod {
		add("enumeration_changed")
	}
	if left.Semantics.BuildConfiguration != right.Semantics.BuildConfiguration {
		add("build_configuration_changed")
	}
	if left.Semantics.SnapshotPolicy != right.Semantics.SnapshotPolicy ||
		left.Semantics.ExternalInputSemantics != right.Semantics.ExternalInputSemantics {
		add("external_metadata_changed")
	}
	if !left.Semantics.AnalysisComplete || !right.Semantics.AnalysisComplete {
		add("analysis_failed")
	}
	if !left.Semantics.InputsFresh || !right.Semantics.InputsFresh {
		add("input_stale")
	}
	slices.Sort(causes)
	return causes
}

func isStringSubset(left, right []string) bool {
	rightSet := make(map[string]struct{}, len(right))
	for _, value := range right {
		rightSet[value] = struct{}{}
	}
	for _, value := range left {
		if _, ok := rightSet[value]; !ok {
			return false
		}
	}
	return true
}

func consumerContinuityNamespace(snapshot ConsumerSnapshot) string {
	digest, _ := contentDigest(snapshot.Semantics)
	return digest
}
