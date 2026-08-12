package candidate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
)

const (
	DomainResultPlanSchema     = "phebs-extraction-domain-result-plan-v1"
	PartitionResultSchema      = "phebs-extraction-partition-result-v1"
	DomainResultRootSchema     = "phebs-extraction-domain-result-root-v1"
	PartitionExpectationSchema = "phebs-extraction-partition-result-expectation-v1"

	PartitionResultSuccess                 = "success"
	PartitionResultEmpty                   = "empty"
	PartitionResultUnavailablePrerequisite = "unavailable_prerequisite"
	PartitionResultTerminalRefusal         = "terminal_refusal"
	PartitionResultRetryable               = "retryable"

	// T40.1's structural profile models 489 candidate-member partitions;
	// T40.8 may append one present typed-input partition. The semantic profile
	// freezes 49,152 facts, 98,304 staging rows, and 39,182,336 canonical
	// extractor bytes. The byte limits retain bounded headroom up to the next
	// established binary boundary; references cannot exceed the independently
	// frozen staging-row population.
	MaxDomainResultMemberPartitions = 489
	MaxDomainResultTypedPartitions  = 1
	MaxDomainResultPartitions       = MaxDomainResultMemberPartitions + MaxDomainResultTypedPartitions
	MaxDomainResultFacts            = 49_152
	MaxDomainResultRows             = 98_304
	MaxDomainResultReferences       = 98_304
	MaxDomainResultCanonicalBytes   = int64(64 << 20)
	MaxDomainResultEncodedBytes     = int64(64 << 20)
	MaxDomainResultMemberBytes      = int64(64 << 20)
	MaxDomainResultMembers          = MaxDomainResultMemberPartitions
	MaxDomainResultInventoryEntries = 2 * MaxDomainResultPartitions
	MaxPartitionResultBytes         = int64(8 << 10)
	MaxDomainResultInventoryBytes   = int64(MaxDomainResultInventoryEntries) * MaxPartitionResultBytes
	MaxDomainResultPlanBytes        = int64(4 << 20)
	MaxDomainResultRootBytes        = int64(4 << 20)
)

// PartitionResultUnavailablePrerequisite is a root-only state. T40.8 proves
// typed-input absence before scheduling, so an unavailable domain has no
// partition result that could truthfully carry this disposition.

var ErrInvalidDomainResult = errors.New("invalid extraction domain result")

// DomainResultAuthority supplies the two immutable upstream generations and
// the extractor policy that do not live in the T40.8 candidate partition.
// BuildDomainResultPlan fills every candidate identity from an already-opened
// sparse domain instead of accepting a second caller-authored copy.
type DomainResultAuthority struct {
	SourceGenerationDigest      string `json:"source_generation_digest"`
	ObservationGenerationDigest string `json:"observation_generation_digest"`
	ExtractorVersion            string `json:"extractor_version"`
	ExtractionPolicyDigest      string `json:"extraction_policy_digest"`
}

type DomainResultLimits struct {
	Partitions       int   `json:"partitions"`
	MemberPartitions int   `json:"member_partitions"`
	TypedPartitions  int   `json:"typed_partitions"`
	Facts            int64 `json:"facts"`
	Rows             int64 `json:"rows"`
	References       int64 `json:"references"`
	CanonicalBytes   int64 `json:"canonical_bytes"`
	EncodedBytes     int64 `json:"encoded_bytes"`
	MemberBytes      int64 `json:"member_bytes"`
	Members          int64 `json:"members"`
	InventoryEntries int   `json:"inventory_entries"`
	InventoryBytes   int64 `json:"inventory_bytes"`
	PlanBytes        int64 `json:"plan_bytes"`
	RootBytes        int64 `json:"root_bytes"`
}

func FrozenDomainResultLimits() DomainResultLimits {
	return DomainResultLimits{
		Partitions:       MaxDomainResultPartitions,
		MemberPartitions: MaxDomainResultMemberPartitions,
		TypedPartitions:  MaxDomainResultTypedPartitions,
		Facts:            MaxDomainResultFacts, Rows: MaxDomainResultRows,
		References:       MaxDomainResultReferences,
		CanonicalBytes:   MaxDomainResultCanonicalBytes,
		EncodedBytes:     MaxDomainResultEncodedBytes,
		MemberBytes:      MaxDomainResultMemberBytes,
		Members:          MaxDomainResultMembers,
		InventoryEntries: MaxDomainResultInventoryEntries,
		InventoryBytes:   MaxDomainResultInventoryBytes,
		PlanBytes:        MaxDomainResultPlanBytes,
		RootBytes:        MaxDomainResultRootBytes,
	}
}

// DomainResultTotals is used for both reservations and settled work. A plan
// reserves every scalar before a worker can own source bytes; a result may
// consume no more than that exact reservation.
type DomainResultTotals struct {
	Facts          int64 `json:"facts"`
	Rows           int64 `json:"rows"`
	References     int64 `json:"references"`
	CanonicalBytes int64 `json:"canonical_bytes"`
	EncodedBytes   int64 `json:"encoded_bytes"`
	MemberBytes    int64 `json:"member_bytes"`
	Members        int64 `json:"members"`
}

type PartitionResultExpectation struct {
	Schema          string                    `json:"schema"`
	Ordinal         int                       `json:"ordinal"`
	Kind            string                    `json:"kind"`
	PartitionDigest string                    `json:"partition_digest"`
	SourceStart     int                       `json:"source_start"`
	SourceEnd       int                       `json:"source_end"`
	Quotas          ExtractionPartitionQuotas `json:"quotas"`
	Reservation     DomainResultTotals        `json:"reservation"`
	Digest          string                    `json:"digest"`
}

// DomainResultPlan is the complete ordered expected partition set. It is a
// nonproduct authority input: no partition result or plan has a current
// pointer, store registration, recovery hook, or product reader in T40.9.
type DomainResultPlan struct {
	Schema                       string                       `json:"schema"`
	Repository                   string                       `json:"repository"`
	CandidateManifestDigest      string                       `json:"candidate_manifest_digest"`
	CandidateGenerationDigest    string                       `json:"candidate_generation_digest"`
	CandidatePartitionRootDigest string                       `json:"candidate_partition_root_digest"`
	CandidatePolicyDigest        string                       `json:"candidate_policy_digest"`
	SourceGenerationDigest       string                       `json:"source_generation_digest"`
	ObservationGenerationDigest  string                       `json:"observation_generation_digest"`
	Domain                       string                       `json:"domain"`
	Version                      string                       `json:"version"`
	Plane                        Plane                        `json:"plane"`
	ExtractorVersion             string                       `json:"extractor_version"`
	ExtractionPolicyDigest       string                       `json:"extraction_policy_digest"`
	DomainIndexDigest            string                       `json:"domain_index_digest"`
	DomainScheduleDigest         string                       `json:"domain_schedule_digest"`
	Availability                 string                       `json:"availability"`
	UnavailableReason            string                       `json:"unavailable_reason,omitempty"`
	Limits                       DomainResultLimits           `json:"limits"`
	Reserved                     DomainResultTotals           `json:"reserved"`
	Expected                     []PartitionResultExpectation `json:"expected"`
	Digest                       string                       `json:"digest"`
}

type PartitionResultSpec struct {
	Disposition   string             `json:"disposition"`
	Reason        string             `json:"reason,omitempty"`
	Totals        DomainResultTotals `json:"totals"`
	ContentDigest string             `json:"content_digest,omitempty"`
}

type PartitionResult struct {
	Schema                      string             `json:"schema"`
	PlanDigest                  string             `json:"plan_digest"`
	ExpectationDigest           string             `json:"expectation_digest"`
	Repository                  string             `json:"repository"`
	CandidateGenerationDigest   string             `json:"candidate_generation_digest"`
	SourceGenerationDigest      string             `json:"source_generation_digest"`
	ObservationGenerationDigest string             `json:"observation_generation_digest"`
	Domain                      string             `json:"domain"`
	Version                     string             `json:"version"`
	ExtractorVersion            string             `json:"extractor_version"`
	ExtractionPolicyDigest      string             `json:"extraction_policy_digest"`
	PartitionOrdinal            int                `json:"partition_ordinal"`
	PartitionDigest             string             `json:"partition_digest"`
	SourceStart                 int                `json:"source_start"`
	SourceEnd                   int                `json:"source_end"`
	Reserved                    DomainResultTotals `json:"reserved"`
	Disposition                 string             `json:"disposition"`
	Reason                      string             `json:"reason,omitempty"`
	Totals                      DomainResultTotals `json:"totals"`
	ContentDigest               string             `json:"content_digest"`
	Digest                      string             `json:"digest"`
	Identity                    string             `json:"identity"`
}

type DomainResultRoot struct {
	Schema          string             `json:"schema"`
	PlanDigest      string             `json:"plan_digest"`
	Repository      string             `json:"repository"`
	Domain          string             `json:"domain"`
	Version         string             `json:"version"`
	Disposition     string             `json:"disposition"`
	ExpectedResults int                `json:"expected_results"`
	ResultCount     int                `json:"result_count"`
	Totals          DomainResultTotals `json:"totals"`
	Results         []PartitionResult  `json:"results"`
	ResultSetDigest string             `json:"result_set_digest"`
	Digest          string             `json:"digest"`
}

// DownstreamDomainAuthority is the bounded exact plan/root identity retained
// by T40.11 component roots. It deliberately repeats the upstream policy and
// generation digests needed to reject a root detached from its admitted plan.
type DownstreamDomainAuthority struct {
	Domain                       string `json:"domain"`
	Version                      string `json:"version"`
	PlanDigest                   string `json:"plan_digest"`
	RootDigest                   string `json:"root_digest"`
	RunID                        string `json:"run_id"`
	Disposition                  string `json:"disposition"`
	CandidateManifestDigest      string `json:"candidate_manifest_digest"`
	CandidatePartitionRootDigest string `json:"candidate_partition_root_digest"`
	CandidatePolicyDigest        string `json:"candidate_policy_digest"`
	SourceGenerationDigest       string `json:"source_generation_digest"`
	ObservationGenerationDigest  string `json:"observation_generation_digest"`
	ExtractionPolicyDigest       string `json:"extraction_policy_digest"`
	DomainIndexDigest            string `json:"domain_index_digest"`
	DomainScheduleDigest         string `json:"domain_schedule_digest"`
}

func NewDownstreamDomainAuthority(
	plan DomainResultPlan, root DomainResultRoot, runID string,
) (DownstreamDomainAuthority, error) {
	if err := ValidateDomainResultRoot(root, plan); err != nil {
		return DownstreamDomainAuthority{}, err
	}
	if strings.TrimSpace(runID) != runID || runID == "" || len(runID) > 512 {
		return DownstreamDomainAuthority{}, domainResultInvalid("downstream run identity")
	}
	return DownstreamDomainAuthority{
		Domain: plan.Domain, Version: plan.Version, PlanDigest: plan.Digest,
		RootDigest: root.Digest, RunID: runID, Disposition: root.Disposition,
		CandidateManifestDigest:      plan.CandidateManifestDigest,
		CandidatePartitionRootDigest: plan.CandidatePartitionRootDigest,
		CandidatePolicyDigest:        plan.CandidatePolicyDigest,
		SourceGenerationDigest:       plan.SourceGenerationDigest,
		ObservationGenerationDigest:  plan.ObservationGenerationDigest,
		ExtractionPolicyDigest:       plan.ExtractionPolicyDigest,
		DomainIndexDigest:            plan.DomainIndexDigest,
		DomainScheduleDigest:         plan.DomainScheduleDigest,
	}, nil
}

func ValidateDownstreamDomainAuthority(value DownstreamDomainAuthority) error {
	if !validToken(value.Domain, 128) || !validToken(value.Version, 128) ||
		!validDigest(value.PlanDigest) || !validDigest(value.RootDigest) ||
		strings.TrimSpace(value.RunID) != value.RunID || value.RunID == "" || len(value.RunID) > 512 ||
		!validDomainResultRootDisposition(value.Disposition) ||
		!validDigest(value.CandidateManifestDigest) || !validDigest(value.CandidatePartitionRootDigest) ||
		!validDigest(value.CandidatePolicyDigest) || !validDigest(value.SourceGenerationDigest) ||
		!validDigest(value.ObservationGenerationDigest) || !validDigest(value.ExtractionPolicyDigest) ||
		!validDigest(value.DomainIndexDigest) || !validDigest(value.DomainScheduleDigest) {
		return domainResultInvalid("downstream domain authority")
	}
	return nil
}

func (value DownstreamDomainAuthority) Available() bool {
	return value.Disposition == PartitionResultSuccess || value.Disposition == PartitionResultEmpty ||
		value.Disposition == PartitionResultUnavailablePrerequisite
}

// BuildDomainResultPlan freezes the exact ordered partitions and reservations
// from a completely opened T40.8 domain. Every aggregate is checked before an
// expectation is appended.
func BuildDomainResultPlan(
	domain *SparseDomain,
	authority DomainResultAuthority,
	reservations []DomainResultTotals,
) (DomainResultPlan, error) {
	if domain == nil || domain.publication == nil || reservations == nil {
		return DomainResultPlan{}, domainResultInvalid("plan requires an opened sparse domain and explicit reservations")
	}
	index := domain.index
	descriptor := domain.descriptor
	publicationRoot := domain.publication.root
	if len(index.Partitions) > MaxDomainResultPartitions || len(reservations) != len(index.Partitions) {
		return DomainResultPlan{}, domainResultInvalid("expected partition or reservation inventory is invalid")
	}
	memberPartitions, typedPartitions, err := countDomainResultPartitionKinds(index.Partitions)
	if err != nil || memberPartitions > MaxDomainResultMemberPartitions ||
		typedPartitions > MaxDomainResultTypedPartitions {
		return DomainResultPlan{}, domainResultInvalid("member or typed partition inventory exceeds its frozen limit")
	}
	manifest := Manifest{
		Repository: index.Repository, Digest: index.CandidateManifestDigest,
		GenerationDigest: index.CandidateGenerationDigest,
	}
	if err := validateSparseDomain(index, descriptor, manifest, false); err != nil {
		return DomainResultPlan{}, domainResultInvalid("sparse domain: %v", err)
	}
	if publicationRoot.Repository != index.Repository ||
		publicationRoot.CandidateManifestDigest != index.CandidateManifestDigest ||
		publicationRoot.CandidateGenerationDigest != index.CandidateGenerationDigest ||
		!validDigest(publicationRoot.Digest) || !validDigest(publicationRoot.PolicyDigest) ||
		!validDigest(authority.SourceGenerationDigest) ||
		!validDigest(authority.ObservationGenerationDigest) ||
		!validToken(authority.ExtractorVersion, 256) ||
		!validDigest(authority.ExtractionPolicyDigest) {
		return DomainResultPlan{}, domainResultInvalid("plan authority is invalid")
	}
	limits := FrozenDomainResultLimits()
	plan := DomainResultPlan{
		Schema: DomainResultPlanSchema, Repository: index.Repository,
		CandidateManifestDigest:      index.CandidateManifestDigest,
		CandidateGenerationDigest:    index.CandidateGenerationDigest,
		CandidatePartitionRootDigest: publicationRoot.Digest,
		CandidatePolicyDigest:        publicationRoot.PolicyDigest,
		SourceGenerationDigest:       authority.SourceGenerationDigest,
		ObservationGenerationDigest:  authority.ObservationGenerationDigest,
		Domain:                       index.Domain, Version: index.Version, Plane: index.Plane,
		ExtractorVersion:       authority.ExtractorVersion,
		ExtractionPolicyDigest: authority.ExtractionPolicyDigest,
		DomainIndexDigest:      index.Digest, DomainScheduleDigest: index.ScheduleDigest,
		Availability: descriptor.Availability, UnavailableReason: descriptor.UnavailableReason,
		Limits:   limits,
		Expected: make([]PartitionResultExpectation, 0, len(index.Partitions)),
	}
	for ordinal, partition := range index.Partitions {
		reservation := reservations[ordinal]
		if err := validateDomainResultReservation(reservation, partition.Quotas, limits); err != nil {
			return DomainResultPlan{}, domainResultInvalid("partition %d reservation: %v", ordinal, err)
		}
		if err := addDomainResultTotals(&plan.Reserved, reservation, limits); err != nil {
			return DomainResultPlan{}, errors.Join(
				domainResultInvalid("partition %d reservation", ordinal), err,
			)
		}
		expectation := PartitionResultExpectation{
			Schema: PartitionExpectationSchema, Ordinal: ordinal, Kind: partition.Kind,
			PartitionDigest: partition.Digest,
			SourceStart:     partition.SourceStart, SourceEnd: partition.SourceEnd,
			Quotas: partition.Quotas, Reservation: reservation,
		}
		expectation.Digest = partitionExpectationDigest(expectation)
		plan.Expected = append(plan.Expected, expectation)
	}
	plan.Digest = domainResultPlanDigest(plan)
	if err := validateDomainResultPlan(plan); err != nil {
		return DomainResultPlan{}, err
	}
	return plan, nil
}

// DecodeDomainResultPlan enforces the raw control fence before allocating a
// decoded plan, then closes it against the already-opened sparse authority.
func DecodeDomainResultPlan(reader io.Reader, domain *SparseDomain) (DomainResultPlan, error) {
	var plan DomainResultPlan
	if err := strictDecode(reader, MaxDomainResultPlanBytes, &plan); err != nil {
		return DomainResultPlan{}, domainResultInvalid("decode plan: %v", err)
	}
	if err := ValidateDomainResultPlan(plan, domain); err != nil {
		return DomainResultPlan{}, err
	}
	return plan, nil
}

// DecodeDomainResultPlanControl enforces the raw control fence and validates
// the complete self-contained plan. Runtime owners use this only after the
// plan was originally closed against an opened sparse domain by
// BuildDomainResultPlan; consumers that still have that domain should prefer
// DecodeDomainResultPlan so the upstream controls are re-bound as well.
func DecodeDomainResultPlanControl(reader io.Reader) (DomainResultPlan, error) {
	var plan DomainResultPlan
	if err := strictDecode(reader, MaxDomainResultPlanBytes, &plan); err != nil {
		return DomainResultPlan{}, domainResultInvalid("decode plan control: %v", err)
	}
	if err := validateDomainResultPlan(plan); err != nil {
		return DomainResultPlan{}, err
	}
	return plan, nil
}

// ValidateDomainResultPlanControl validates the bounded self-contained plan
// without reopening candidate content. It is the restart boundary for the
// T40.10 runtime; initial construction still requires BuildDomainResultPlan.
func ValidateDomainResultPlanControl(plan DomainResultPlan) error {
	return validateDomainResultPlan(plan)
}

// ValidateDomainResultPlan closes the trusted-plan replay against the same
// sparse domain without reading candidate members.
func ValidateDomainResultPlan(plan DomainResultPlan, domain *SparseDomain) error {
	if err := validateDomainResultPlan(plan); err != nil {
		return err
	}
	if domain == nil || domain.publication == nil ||
		plan.Repository != domain.index.Repository ||
		plan.CandidateManifestDigest != domain.index.CandidateManifestDigest ||
		plan.CandidateGenerationDigest != domain.index.CandidateGenerationDigest ||
		plan.CandidatePartitionRootDigest != domain.publication.root.Digest ||
		plan.CandidatePolicyDigest != domain.publication.root.PolicyDigest ||
		plan.DomainIndexDigest != domain.index.Digest ||
		plan.DomainScheduleDigest != domain.index.ScheduleDigest ||
		plan.Availability != domain.descriptor.Availability ||
		plan.UnavailableReason != domain.descriptor.UnavailableReason ||
		len(plan.Expected) != len(domain.index.Partitions) {
		return domainResultInvalid("plan differs from the selected sparse domain")
	}
	for ordinal, partition := range domain.index.Partitions {
		expected := plan.Expected[ordinal]
		if expected.Ordinal != ordinal || expected.Kind != partition.Kind ||
			expected.PartitionDigest != partition.Digest ||
			expected.SourceStart != partition.SourceStart || expected.SourceEnd != partition.SourceEnd ||
			expected.Quotas != partition.Quotas {
			return domainResultInvalid("plan partition %d differs from the selected sparse domain", ordinal)
		}
	}
	return nil
}

func BuildPartitionResult(
	plan DomainResultPlan,
	ordinal int,
	spec PartitionResultSpec,
) (PartitionResult, error) {
	// A partition result is deliberately non-authoritative. Validate only the
	// bounded plan envelope and selected expectation here so N workers do not
	// each rescan the N-partition plan. BuildDomainResultRoot performs the one
	// complete plan and ordered-result validation before authority can exist.
	if err := validateDomainResultPlanEnvelope(plan); err != nil {
		return PartitionResult{}, err
	}
	if ordinal < 0 || ordinal >= len(plan.Expected) {
		return PartitionResult{}, domainResultInvalid("partition result ordinal is invalid")
	}
	expectation := plan.Expected[ordinal]
	if err := validatePartitionResultExpectation(expectation, ordinal); err != nil {
		return PartitionResult{}, err
	}
	if err := validatePartitionResultSpec(spec, expectation.Reservation); err != nil {
		return PartitionResult{}, err
	}
	if spec.ContentDigest == "" && spec.Disposition != PartitionResultSuccess {
		spec.ContentDigest = emptyPartitionResultContentDigest()
	}
	result := PartitionResult{
		Schema: PartitionResultSchema, PlanDigest: plan.Digest,
		ExpectationDigest: expectation.Digest, Repository: plan.Repository,
		CandidateGenerationDigest:   plan.CandidateGenerationDigest,
		SourceGenerationDigest:      plan.SourceGenerationDigest,
		ObservationGenerationDigest: plan.ObservationGenerationDigest,
		Domain:                      plan.Domain, Version: plan.Version,
		ExtractorVersion:       plan.ExtractorVersion,
		ExtractionPolicyDigest: plan.ExtractionPolicyDigest,
		PartitionOrdinal:       ordinal, PartitionDigest: expectation.PartitionDigest,
		SourceStart: expectation.SourceStart, SourceEnd: expectation.SourceEnd,
		Reserved:    expectation.Reservation,
		Disposition: spec.Disposition, Reason: spec.Reason,
		Totals: spec.Totals, ContentDigest: spec.ContentDigest,
	}
	result.Digest = partitionResultDigest(result)
	result.Identity = partitionResultIdentity(result)
	if err := validatePartitionResult(result, plan, expectation); err != nil {
		return PartitionResult{}, err
	}
	return result, nil
}

// BuildDomainResultRoot accepts exact byte-equal duplicate retry results, but
// unique results must remain in the plan's order and cover it exactly.
func BuildDomainResultRoot(plan DomainResultPlan, submitted []PartitionResult) (DomainResultRoot, error) {
	if err := validateDomainResultPlan(plan); err != nil {
		return DomainResultRoot{}, err
	}
	return buildDomainResultRootValidatedPlan(plan, submitted)
}

func buildDomainResultRootValidatedPlan(plan DomainResultPlan, submitted []PartitionResult) (DomainResultRoot, error) {
	if submitted == nil || len(submitted) > plan.Limits.InventoryEntries {
		return DomainResultRoot{}, domainResultInvalid("result inventory is nil or exceeds %d entries", plan.Limits.InventoryEntries)
	}
	results := make([]PartitionResult, 0, min(len(plan.Expected), len(submitted)))
	seen := make(map[int][]byte, min(len(plan.Expected), len(submitted)))
	resultBytes := int64(0)
	nextOrdinal := 0
	for _, result := range submitted {
		if result.PartitionOrdinal < 0 || result.PartitionOrdinal >= len(plan.Expected) {
			return DomainResultRoot{}, domainResultInvalid("result references an extra partition")
		}
		expectation := plan.Expected[result.PartitionOrdinal]
		if err := validatePartitionResult(result, plan, expectation); err != nil {
			return DomainResultRoot{}, err
		}
		raw, err := sparseCanonical(result)
		if err != nil || int64(len(raw)) > MaxPartitionResultBytes {
			return DomainResultRoot{}, domainResultInvalid("partition result control is oversized")
		}
		if int64(len(raw)) > plan.Limits.InventoryBytes-resultBytes {
			return DomainResultRoot{}, domainResultInvalid("result inventory exceeds %d bytes", plan.Limits.InventoryBytes)
		}
		resultBytes += int64(len(raw))
		if prior, duplicate := seen[result.PartitionOrdinal]; duplicate {
			if !bytes.Equal(prior, raw) {
				return DomainResultRoot{}, domainResultInvalid("duplicate partition result is not byte-equal")
			}
			continue
		}
		if result.PartitionOrdinal != nextOrdinal {
			return DomainResultRoot{}, domainResultInvalid("partition results are missing or reordered")
		}
		seen[result.PartitionOrdinal] = raw
		results = append(results, result)
		nextOrdinal++
	}
	if len(results) != len(plan.Expected) {
		return DomainResultRoot{}, domainResultInvalid("partition result set is incomplete")
	}
	root := DomainResultRoot{
		Schema: DomainResultRootSchema, PlanDigest: plan.Digest,
		Repository: plan.Repository, Domain: plan.Domain, Version: plan.Version,
		ExpectedResults: len(plan.Expected), ResultCount: len(results),
		Results: slices.Clone(results),
	}
	for _, result := range root.Results {
		if err := addDomainResultTotals(&root.Totals, result.Totals, plan.Limits); err != nil {
			return DomainResultRoot{}, err
		}
		root.Disposition = mergePartitionResultDisposition(root.Disposition, result.Disposition)
	}
	if len(root.Results) == 0 {
		if plan.Availability == "unavailable" {
			root.Disposition = PartitionResultUnavailablePrerequisite
		} else {
			root.Disposition = PartitionResultEmpty
		}
	}
	root.ResultSetDigest = domainResultSetDigest(root.Results)
	root.Digest = domainResultRootDigest(root)
	raw, err := sparseCanonical(root)
	if err != nil || int64(len(raw)) > plan.Limits.RootBytes {
		return DomainResultRoot{}, domainResultInvalid("domain result root exceeds %d bytes", plan.Limits.RootBytes)
	}
	return root, nil
}

func ValidateDomainResultRoot(root DomainResultRoot, plan DomainResultPlan) error {
	if err := validateDomainResultPlan(plan); err != nil {
		return err
	}
	if err := validateDomainResultRootEnvelope(root, plan); err != nil {
		return err
	}
	rebuilt, err := buildDomainResultRootValidatedPlan(plan, root.Results)
	if err != nil {
		return err
	}
	want, err := sparseCanonical(rebuilt)
	if err != nil {
		return err
	}
	got, err := sparseCanonical(root)
	if err != nil || !bytes.Equal(got, want) {
		return domainResultInvalid("domain result root is not the exact recomputed root")
	}
	return nil
}

// DecodeDomainResultRoot fences raw bytes before decoding and requires the
// exact recomputed plan/result authority before returning a control.
func DecodeDomainResultRoot(reader io.Reader, plan DomainResultPlan) (DomainResultRoot, error) {
	var root DomainResultRoot
	if err := strictDecode(reader, MaxDomainResultRootBytes, &root); err != nil {
		return DomainResultRoot{}, domainResultInvalid("decode root: %v", err)
	}
	if err := ValidateDomainResultRoot(root, plan); err != nil {
		return DomainResultRoot{}, err
	}
	return root, nil
}

func validateDomainResultPlan(plan DomainResultPlan) error {
	if err := validateDomainResultPlanEnvelope(plan); err != nil {
		return err
	}
	var reserved DomainResultTotals
	seenPartitions := make(map[string]struct{}, len(plan.Expected))
	wantSourceStart := 0
	typedPhase := false
	memberPartitions := 0
	typedPartitions := 0
	for ordinal, expectation := range plan.Expected {
		if err := validatePartitionResultExpectation(expectation, ordinal); err != nil {
			return err
		}
		if _, duplicate := seenPartitions[expectation.PartitionDigest]; duplicate {
			return domainResultInvalid("partition expectation %d repeats an identity", ordinal)
		}
		seenPartitions[expectation.PartitionDigest] = struct{}{}
		switch expectation.Kind {
		case PartitionKindCandidateMember:
			memberPartitions++
			if typedPhase || expectation.SourceStart != wantSourceStart ||
				expectation.SourceEnd <= expectation.SourceStart {
				return domainResultInvalid("partition expectation %d has an invalid source range", ordinal)
			}
			wantSourceStart = expectation.SourceEnd
		case PartitionKindTypedInput:
			typedPartitions++
			typedPhase = true
			if expectation.SourceStart != wantSourceStart || expectation.SourceEnd != wantSourceStart {
				return domainResultInvalid("typed expectation %d has an invalid source range", ordinal)
			}
		}
		if err := addDomainResultTotals(&reserved, expectation.Reservation, plan.Limits); err != nil {
			return err
		}
	}
	if reserved != plan.Reserved {
		return domainResultInvalid("plan reserved aggregate is not recomputed exactly")
	}
	if memberPartitions > plan.Limits.MemberPartitions || typedPartitions > plan.Limits.TypedPartitions {
		return domainResultInvalid("plan member or typed partition aggregate exceeds its frozen limit")
	}
	digestInput := plan
	digestInput.Digest = ""
	raw, err := json.Marshal(digestInput)
	if err != nil || int64(len(raw)+len(plan.Digest)+1) > plan.Limits.PlanBytes {
		return domainResultInvalid("domain result plan exceeds %d bytes", plan.Limits.PlanBytes)
	}
	if plan.Digest != digest("phebs-extraction-domain-result-plan-v1\x00", raw) {
		return domainResultInvalid("domain result plan digest is invalid")
	}
	return nil
}

func validateDomainResultPlanEnvelope(plan DomainResultPlan) error {
	limits := FrozenDomainResultLimits()
	if plan.Schema != DomainResultPlanSchema || !safeRepository(plan.Repository) ||
		!validDigest(plan.CandidateManifestDigest) ||
		!validDigest(plan.CandidateGenerationDigest) ||
		!validDigest(plan.CandidatePartitionRootDigest) ||
		!validDigest(plan.CandidatePolicyDigest) ||
		!validDigest(plan.SourceGenerationDigest) ||
		!validDigest(plan.ObservationGenerationDigest) ||
		!validToken(plan.Domain, 128) || !validToken(plan.Version, 128) ||
		(plan.Plane != PlaneRepository && plan.Plane != PlaneLocal && plan.Plane != PlaneCaller) ||
		!validToken(plan.ExtractorVersion, 256) ||
		!validDigest(plan.ExtractionPolicyDigest) ||
		!validDigest(plan.DomainIndexDigest) || !validDigest(plan.DomainScheduleDigest) ||
		!validSparseAvailability(plan.Availability, plan.UnavailableReason) ||
		plan.Limits != limits || plan.Expected == nil || len(plan.Expected) > limits.Partitions ||
		!validDigest(plan.Digest) || !validDomainResultTotals(plan.Reserved) ||
		!domainResultTotalsWithin(plan.Reserved, domainResultTotalsLimit(limits)) {
		return domainResultInvalid("domain result plan envelope is invalid")
	}
	if (plan.Availability == "unavailable" || plan.Availability == "empty") && len(plan.Expected) != 0 ||
		plan.Availability == "admitted" && len(plan.Expected) == 0 {
		return domainResultInvalid("plan availability disagrees with its expected set")
	}
	return nil
}

func validateDomainResultRootEnvelope(root DomainResultRoot, plan DomainResultPlan) error {
	if root.Schema != DomainResultRootSchema || root.PlanDigest != plan.Digest ||
		root.Repository != plan.Repository || root.Domain != plan.Domain || root.Version != plan.Version ||
		!validDomainResultRootDisposition(root.Disposition) ||
		root.ExpectedResults != len(plan.Expected) || root.ResultCount != len(root.Results) ||
		root.Results == nil || len(root.Results) > plan.Limits.Partitions ||
		!validDomainResultTotals(root.Totals) ||
		!domainResultTotalsWithin(root.Totals, domainResultTotalsLimit(plan.Limits)) ||
		!validDigest(root.ResultSetDigest) || !validDigest(root.Digest) {
		return domainResultInvalid("domain result root envelope is invalid")
	}
	if (root.Disposition == PartitionResultUnavailablePrerequisite) !=
		(plan.Availability == "unavailable" && len(root.Results) == 0) {
		return domainResultInvalid("unavailable prerequisite is not the exact zero-result root state")
	}
	return nil
}

func domainResultTotalsLimit(limits DomainResultLimits) DomainResultTotals {
	return DomainResultTotals{
		Facts: limits.Facts, Rows: limits.Rows, References: limits.References,
		CanonicalBytes: limits.CanonicalBytes, EncodedBytes: limits.EncodedBytes,
		MemberBytes: limits.MemberBytes, Members: limits.Members,
	}
}

func countDomainResultPartitionKinds(partitions []ExtractionPartition) (int, int, error) {
	members := 0
	typed := 0
	for _, partition := range partitions {
		switch partition.Kind {
		case PartitionKindCandidateMember:
			members++
		case PartitionKindTypedInput:
			typed++
		default:
			return 0, 0, domainResultInvalid("unknown extraction partition kind")
		}
	}
	return members, typed, nil
}

func validatePartitionResultExpectation(expectation PartitionResultExpectation, ordinal int) error {
	if expectation.Schema != PartitionExpectationSchema || expectation.Ordinal != ordinal ||
		(expectation.Kind != PartitionKindCandidateMember && expectation.Kind != PartitionKindTypedInput) ||
		!validDigest(expectation.PartitionDigest) || expectation.SourceStart < 0 ||
		expectation.SourceEnd < expectation.SourceStart || expectation.Quotas != FrozenExtractionPartitionQuotas() ||
		expectation.Digest != partitionExpectationDigest(expectation) {
		return domainResultInvalid("partition expectation %d is invalid", ordinal)
	}
	if err := validateDomainResultReservation(expectation.Reservation, expectation.Quotas, FrozenDomainResultLimits()); err != nil {
		return domainResultInvalid("partition expectation %d reservation: %v", ordinal, err)
	}
	return nil
}

func validateDomainResultReservation(
	reservation DomainResultTotals,
	quotas ExtractionPartitionQuotas,
	limits DomainResultLimits,
) error {
	if !validDomainResultTotals(reservation) || reservation.Facts > int64(quotas.Facts) ||
		reservation.Rows > int64(quotas.Rows) || reservation.References > int64(quotas.References) ||
		reservation.CanonicalBytes > limits.CanonicalBytes ||
		reservation.EncodedBytes > limits.EncodedBytes ||
		reservation.MemberBytes > limits.MemberBytes || reservation.Members > 1 {
		return errors.New("reservation exceeds its partition quota")
	}
	return nil
}

func validatePartitionResultSpec(spec PartitionResultSpec, reserved DomainResultTotals) error {
	if !validPartitionResultDisposition(spec.Disposition) ||
		!validDomainResultTotals(spec.Totals) || !domainResultTotalsWithin(spec.Totals, reserved) {
		return domainResultInvalid("partition result exceeds its reservation or has an invalid disposition")
	}
	zero := spec.Totals == (DomainResultTotals{})
	switch spec.Disposition {
	case PartitionResultSuccess:
		if zero || spec.Reason != "" || !validDigest(spec.ContentDigest) {
			return domainResultInvalid("successful partition result is empty or lacks content identity")
		}
	case PartitionResultEmpty:
		if !zero || spec.Reason != "" ||
			(spec.ContentDigest != "" && spec.ContentDigest != emptyPartitionResultContentDigest()) {
			return domainResultInvalid("empty partition result carries work or a reason")
		}
	case PartitionResultTerminalRefusal, PartitionResultRetryable:
		if !zero || !validToken(spec.Reason, 256) ||
			(spec.ContentDigest != "" && spec.ContentDigest != emptyPartitionResultContentDigest()) {
			return domainResultInvalid("failed partition result carries work or an invalid reason")
		}
	}
	return nil
}

func validatePartitionResult(
	result PartitionResult,
	plan DomainResultPlan,
	expectation PartitionResultExpectation,
) error {
	spec := PartitionResultSpec{
		Disposition: result.Disposition, Reason: result.Reason,
		Totals: result.Totals, ContentDigest: result.ContentDigest,
	}
	if err := validatePartitionResultSpec(spec, expectation.Reservation); err != nil {
		return err
	}
	if result.Schema != PartitionResultSchema || result.PlanDigest != plan.Digest ||
		result.ExpectationDigest != expectation.Digest || result.Repository != plan.Repository ||
		result.CandidateGenerationDigest != plan.CandidateGenerationDigest ||
		result.SourceGenerationDigest != plan.SourceGenerationDigest ||
		result.ObservationGenerationDigest != plan.ObservationGenerationDigest ||
		result.Domain != plan.Domain || result.Version != plan.Version ||
		result.ExtractorVersion != plan.ExtractorVersion ||
		result.ExtractionPolicyDigest != plan.ExtractionPolicyDigest ||
		result.PartitionOrdinal != expectation.Ordinal ||
		result.PartitionDigest != expectation.PartitionDigest ||
		result.SourceStart != expectation.SourceStart || result.SourceEnd != expectation.SourceEnd ||
		result.Reserved != expectation.Reservation || result.Digest != partitionResultDigest(result) ||
		result.Identity != partitionResultIdentity(result) {
		return domainResultInvalid("partition result %d crosses or corrupts its authority", result.PartitionOrdinal)
	}
	raw, err := sparseCanonical(result)
	if err != nil || int64(len(raw)) > MaxPartitionResultBytes {
		return domainResultInvalid("partition result control exceeds %d bytes", MaxPartitionResultBytes)
	}
	return nil
}

func addDomainResultTotals(current *DomainResultTotals, add DomainResultTotals, limits DomainResultLimits) error {
	type scalar struct {
		current   int64
		add       int64
		limit     int64
		dimension pipelinerefusal.Dimension
	}
	for _, value := range []scalar{
		{current.Facts, add.Facts, limits.Facts, pipelinerefusal.DimensionDomainResultFacts},
		{current.Rows, add.Rows, limits.Rows, pipelinerefusal.DimensionDomainResultRows},
		{current.References, add.References, limits.References, pipelinerefusal.DimensionDomainResultReferences},
		{current.CanonicalBytes, add.CanonicalBytes, limits.CanonicalBytes, pipelinerefusal.DimensionDomainCanonicalBytes},
		{current.EncodedBytes, add.EncodedBytes, limits.EncodedBytes, pipelinerefusal.DimensionDomainEncodedBytes},
		{current.MemberBytes, add.MemberBytes, limits.MemberBytes, pipelinerefusal.DimensionCandidateMemberBytes},
		{current.Members, add.Members, limits.Members, pipelinerefusal.DimensionCandidateMembers},
	} {
		if !domainResultScalarFits(value.current, value.add, value.limit) {
			return pipelinerefusal.Measure(
				domainResultInvalid("domain result aggregate exceeds its frozen limit"),
				value.dimension, value.current+value.add, value.limit,
			)
		}
	}
	current.Facts += add.Facts
	current.Rows += add.Rows
	current.References += add.References
	current.CanonicalBytes += add.CanonicalBytes
	current.EncodedBytes += add.EncodedBytes
	current.MemberBytes += add.MemberBytes
	current.Members += add.Members
	return nil
}

func validDomainResultTotals(value DomainResultTotals) bool {
	return value.Facts >= 0 && value.Rows >= 0 && value.References >= 0 &&
		value.CanonicalBytes >= 0 && value.EncodedBytes >= 0 &&
		value.MemberBytes >= 0 && value.Members >= 0
}

func domainResultTotalsWithin(value, limit DomainResultTotals) bool {
	return value.Facts <= limit.Facts && value.Rows <= limit.Rows &&
		value.References <= limit.References &&
		value.CanonicalBytes <= limit.CanonicalBytes &&
		value.EncodedBytes <= limit.EncodedBytes &&
		value.MemberBytes <= limit.MemberBytes && value.Members <= limit.Members
}

func domainResultScalarFits(current, add, limit int64) bool {
	return current >= 0 && add >= 0 && current <= limit && add <= limit-current
}

func validPartitionResultDisposition(value string) bool {
	switch value {
	case PartitionResultSuccess, PartitionResultEmpty,
		PartitionResultTerminalRefusal, PartitionResultRetryable:
		return true
	default:
		return false
	}
}

func validDomainResultRootDisposition(value string) bool {
	return value == PartitionResultUnavailablePrerequisite || validPartitionResultDisposition(value)
}

func mergePartitionResultDisposition(current, next string) string {
	priority := func(value string) int {
		switch value {
		case PartitionResultTerminalRefusal:
			return 5
		case PartitionResultRetryable:
			return 4
		case PartitionResultSuccess:
			return 2
		case PartitionResultEmpty:
			return 1
		default:
			return 0
		}
	}
	if priority(next) > priority(current) {
		return next
	}
	return current
}

func emptyPartitionResultContentDigest() string {
	return digest("phebs-extraction-partition-result-empty-v1\x00", nil)
}

func partitionExpectationDigest(value PartitionResultExpectation) string {
	copy := value
	copy.Digest = ""
	raw, _ := json.Marshal(copy)
	return digest("phebs-extraction-partition-result-expectation-v1\x00", raw)
}

func domainResultPlanDigest(value DomainResultPlan) string {
	copy := value
	copy.Digest = ""
	raw, _ := json.Marshal(copy)
	return digest("phebs-extraction-domain-result-plan-v1\x00", raw)
}

func partitionResultDigest(value PartitionResult) string {
	copy := value
	copy.Digest = ""
	copy.Identity = ""
	raw, _ := json.Marshal(copy)
	return digest("phebs-extraction-partition-result-v1\x00", raw)
}

func partitionResultIdentity(value PartitionResult) string {
	return partitionResultIdentityFromDigests(value.PartitionDigest, value.Digest)
}

func domainResultSetDigest(results []PartitionResult) string {
	payload := struct {
		Schema  string   `json:"schema"`
		Results []string `json:"results"`
	}{Schema: "phebs-extraction-domain-result-set-v1", Results: make([]string, 0, len(results))}
	for _, result := range results {
		payload.Results = append(payload.Results, result.Identity)
	}
	raw, _ := json.Marshal(payload)
	return digest("phebs-extraction-domain-result-set-v1\x00", raw)
}

func domainResultRootDigest(value DomainResultRoot) string {
	copy := value
	copy.Digest = ""
	raw, _ := json.Marshal(copy)
	return digest("phebs-extraction-domain-result-root-v1\x00", raw)
}

func domainResultInvalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidDomainResult, fmt.Sprintf(format, args...))
}
