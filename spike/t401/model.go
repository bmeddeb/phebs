// Package t401 freezes the neutral scale envelope and explicit authoring seams
// used by T40.1. Production packages must not import spike packages.
package t401

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

const (
	EnvelopeSchema        = "t401-neutral-scale-envelope-v1"
	ProfileSchema         = "t401-neutral-scale-profile-v1"
	OracleSchema          = "t401-independent-scale-oracle-v1"
	ManifestSchema        = "t401-neutral-git-manifest-v1"
	ReceiptSchema         = "t401-neutral-author-receipt-v1"
	ReproducibilitySchema = "t401-neutral-author-reproducibility-v1"
	ComparisonSchema      = "t401-zoekt-blob-reader-comparison-v1"

	StructuralProfileName              = "structural-2m-v1"
	SemanticProfileName                = "semantic-262144-v1"
	RepositoryName                     = "example.invalid/t401-neutral-scale"
	structuralProjectionTree           = "a162006919d1379be2335ed158cefa66cba90525"
	structuralProjectionManifestDigest = "sha256:e3b2aa714d7d8c5b3fc255e2a900c271bcb369e7f0feaf51f46a78ee09551ce2"
)

var (
	ErrDimensionExceeded = errors.New("t401 profile dimension exceeded")
	ErrInjected          = errors.New("t401 author failure injected")
)

type Envelope struct {
	Schema               string               `json:"schema"`
	Profiles             []Profile            `json:"profiles"`
	Oracles              []Oracle             `json:"oracles"`
	FailurePoints        []FailurePoint       `json:"failure_points"`
	BlobReaderTrial      BlobReaderTrial      `json:"blob_reader_trial"`
	HistoricalDiagnostic HistoricalDiagnostic `json:"historical_diagnostic"`
	Claims               EnvelopeClaims       `json:"claims"`
}

type EnvelopeClaims struct {
	Synthetic                 bool `json:"synthetic"`
	GiantAuthoringExplicit    bool `json:"giant_authoring_explicit"`
	GeneratesGiantInTests     bool `json:"generates_giant_in_tests"`
	EstablishesTargetSLO      bool `json:"establishes_target_slo"`
	EstablishesAccuracy       bool `json:"establishes_accuracy"`
	QualifiesSupportedScale   bool `json:"qualifies_supported_scale"`
	SelectsBlobReader         bool `json:"selects_blob_reader"`
	ChangesProductionBehavior bool `json:"changes_production_behavior"`
	AuthorizesRelease         bool `json:"authorizes_release"`
}

type Profile struct {
	Schema              string               `json:"schema"`
	Name                string               `json:"name"`
	Kind                string               `json:"kind"`
	Scale               string               `json:"scale"`
	ParentProfileDigest string               `json:"parent_profile_digest,omitempty"`
	Seed                string               `json:"seed"`
	Generator           GeneratorIdentity    `json:"generator"`
	Shape               ProfileShape         `json:"shape"`
	Families            []TemplateFamily     `json:"template_families"`
	Adapters            []AdapterExpectation `json:"adapter_expectations"`
	Lanes               []LaneProjection     `json:"lane_projections"`
	Aggregate           Aggregate            `json:"aggregate"`
	Dimensions          []Dimension          `json:"dimensions"`
	ExpectedOutcomes    []StageExpectation   `json:"expected_outcomes"`
	Transitions         []Transition         `json:"transitions"`
	Claims              ProfileClaims        `json:"claims"`
}

type LaneProjection struct {
	Name                      string `json:"name"`
	Selection                 string `json:"selection"`
	Unit                      string `json:"unit"`
	Placements                uint64 `json:"placements"`
	UniqueGoBlobs             uint64 `json:"unique_go_blobs"`
	DeclaredBytes             uint64 `json:"declared_bytes"`
	LogicalBytes              uint64 `json:"logical_bytes"`
	ProductionCandidateParity bool   `json:"production_candidate_parity"`
}

type AdapterExpectation struct {
	Family     string `json:"family"`
	Adapter    string `json:"adapter"`
	Protocol   string `json:"protocol,omitempty"`
	ImportPath string `json:"import_path,omitempty"`
	ClientType string `json:"client_type,omitempty"`
	Method     string `json:"method,omitempty"`
	Operation  string `json:"operation,omitempty"`
	Plane      string `json:"plane,omitempty"`
	State      string `json:"state"`
	Reason     string `json:"reason,omitempty"`
}

type GeneratorIdentity struct {
	Name            string `json:"name"`
	Version         string `json:"version"`
	GitObjectFormat string `json:"git_object_format"`
	PathOrder       string `json:"path_order"`
	PathLayout      string `json:"path_layout"`
	TemplatePolicy  string `json:"template_policy"`
	BlobReusePolicy string `json:"blob_reuse_policy"`
	Timestamp       int64  `json:"timestamp"`
	AuthorName      string `json:"author_name"`
	AuthorEmail     string `json:"author_email"`
}

type ProfileShape struct {
	Cells                  uint64 `json:"cells"`
	EligibleGoPathsPerCell uint64 `json:"eligible_go_paths_per_cell"`
	CallerPathsPerCell     uint64 `json:"caller_paths_per_cell"`
	GoFileBytes            uint64 `json:"go_file_bytes"`
	GoBlobReuseClasses     uint64 `json:"go_blob_reuse_classes"`
	IDLFileBytes           uint64 `json:"idl_file_bytes"`
	ControlFiles           uint64 `json:"control_files"`
}

type TemplateFamily struct {
	Name                   string `json:"name"`
	Plane                  string `json:"plane"`
	Suffix                 string `json:"suffix"`
	Outcome                string `json:"outcome"`
	Reason                 string `json:"reason,omitempty"`
	Inputs                 uint64 `json:"inputs"`
	RecordsPerInput        uint64 `json:"records_per_input"`
	GapsPerInput           uint64 `json:"gaps_per_input"`
	StagingRowsPerInput    uint64 `json:"staging_rows_per_input"`
	CanonicalBytesPerInput uint64 `json:"canonical_bytes_per_input"`
}

type Aggregate struct {
	PhysicalOwners            uint64 `json:"physical_owners"`
	RegularFiles              uint64 `json:"regular_files"`
	EligibleGoFiles           uint64 `json:"eligible_go_files"`
	IDLCandidates             uint64 `json:"idl_candidates"`
	ControlFiles              uint64 `json:"control_files"`
	RepositoryLanePlacements  uint64 `json:"repository_lane_placements"`
	CallerLanePlacements      uint64 `json:"caller_lane_placements"`
	UniqueGoBlobs             uint64 `json:"unique_go_blobs"`
	UniqueIDLBlobs            uint64 `json:"unique_idl_blobs"`
	DeclaredRepositoryGoBytes uint64 `json:"declared_repository_go_bytes"`
	LogicalSourceBytes        uint64 `json:"logical_source_bytes"`
	ControlBytes              uint64 `json:"control_bytes"`
	ModeledDomainPartitions   uint64 `json:"modeled_domain_partitions"`
	ModeledDomainMembers      uint64 `json:"modeled_domain_members"`
	DomainPartitionModel      string `json:"domain_partition_model"`
	ObservationRecords        uint64 `json:"observation_records"`
	ObservationGaps           uint64 `json:"observation_gap_classifications"`
	ExtractorFacts            uint64 `json:"extractor_facts"`
	ExtractorGapFacts         uint64 `json:"extractor_gap_facts"`
	ExtractorStagingRows      uint64 `json:"extractor_staging_rows"`
	CanonicalObservationBytes uint64 `json:"canonical_observation_bytes"`
	CanonicalExtractorBytes   uint64 `json:"canonical_extractor_bytes"`
	CanonicalResultByteModel  string `json:"canonical_result_byte_model"`
}

type Dimension struct {
	Name     string `json:"name"`
	Baseline uint64 `json:"baseline"`
	Admitted uint64 `json:"admitted"`
}

type StageExpectation struct {
	Stage     string `json:"stage"`
	Outcome   string `json:"outcome"` // pass | refused | not_run
	Class     string `json:"class"`
	Dimension string `json:"dimension"`
	Observed  uint64 `json:"observed"`
	Limit     uint64 `json:"limit"`
}

type HistoricalDiagnostic struct {
	Artifact string `json:"artifact"`
	SHA256   string `json:"sha256"`
	Outcome  string `json:"outcome"`
	Scope    string `json:"scope"`
}

type Transition struct {
	Revision            string `json:"revision"`
	Parent              string `json:"parent,omitempty"`
	Posture             string `json:"posture"`
	ChangedRegularFiles uint64 `json:"changed_regular_files"`
	TreeRelation        string `json:"tree_relation"`
}

type ProfileClaims struct {
	Synthetic               bool `json:"synthetic"`
	ExplicitAuthoringOnly   bool `json:"explicit_authoring_only"`
	EstablishesTargetSLO    bool `json:"establishes_target_slo"`
	EstablishesAccuracy     bool `json:"establishes_accuracy"`
	QualifiesSupportedScale bool `json:"qualifies_supported_scale"`
	AuthorizesRelease       bool `json:"authorizes_release"`
}

type FailurePoint struct {
	Name         string `json:"name"`
	Boundary     string `json:"boundary"`
	LeavesTarget bool   `json:"leaves_target"`
}

type BlobReaderTrial struct {
	Schema             string       `json:"schema"`
	BinaryAuthority    string       `json:"binary_authority"`
	Fixture            string       `json:"fixture"`
	Modes              []ReaderMode `json:"modes"`
	Queries            []string     `json:"queries"`
	Checks             []string     `json:"checks"`
	GiantMeasurement   string       `json:"giant_measurement"`
	ProductionSelected bool         `json:"production_selected"`
}

type ReaderMode struct {
	Name            string `json:"name"`
	EnvironmentName string `json:"environment_name"`
	Environment     string `json:"environment_value"`
	Production      bool   `json:"production"`
}

type Oracle struct {
	Schema           string             `json:"schema"`
	Profile          string             `json:"profile"`
	ProfileDigest    string             `json:"profile_digest"`
	Aggregate        Aggregate          `json:"aggregate"`
	Dimensions       []Dimension        `json:"dimensions"`
	ExpectedOutcomes []StageExpectation `json:"expected_outcomes"`
	Families         []OracleFamily     `json:"families"`
	Revisions        []OracleRevision   `json:"revisions"`
	Failures         []OracleFailure    `json:"failures"`
}

type OracleFamily struct {
	Name           string `json:"name"`
	Inputs         uint64 `json:"inputs"`
	Outcome        string `json:"outcome"`
	Facts          uint64 `json:"facts"`
	Gaps           uint64 `json:"gaps"`
	StagingRows    uint64 `json:"staging_rows"`
	CanonicalBytes uint64 `json:"canonical_bytes"`
	Reason         string `json:"reason,omitempty"`
}

type OracleRevision struct {
	Revision      string `json:"revision"`
	RegularFiles  uint64 `json:"regular_files"`
	UniqueGoBlobs uint64 `json:"unique_go_blobs"`
	ChangedFiles  uint64 `json:"changed_files"`
	ExpectedTree  string `json:"expected_tree"`
}

type OracleFailure struct {
	Point   string `json:"point"`
	Class   string `json:"class"`
	Cleanup string `json:"cleanup"`
}

type Manifest struct {
	Schema        string             `json:"schema"`
	Profile       string             `json:"profile"`
	ProfileDigest string             `json:"profile_digest"`
	Repository    string             `json:"repository"`
	Projection    bool               `json:"projection"`
	Aggregate     Aggregate          `json:"aggregate"`
	Revisions     []RevisionIdentity `json:"revisions"`
	ByteMetrics   []ByteMetric       `json:"byte_metrics"`
	FailurePoints []string           `json:"failure_points"`
}

type RevisionIdentity struct {
	Revision      string `json:"revision"`
	Commit        string `json:"commit"`
	Tree          string `json:"tree"`
	RegularFiles  uint64 `json:"regular_files"`
	UniqueGoBlobs uint64 `json:"unique_go_blobs"`
	ChangedFiles  uint64 `json:"changed_files"`
}

type ByteMetric struct {
	Kind        string `json:"kind"`
	Measurement string `json:"measurement"`
	Bytes       uint64 `json:"bytes"`
}

type Receipt struct {
	Schema               string               `json:"schema"`
	Outcome              string               `json:"outcome"`
	Profile              string               `json:"profile"`
	ProfileDigest        string               `json:"profile_digest"`
	Artifacts            []ArtifactIdentity   `json:"artifacts"`
	Revisions            []RevisionIdentity   `json:"revisions"`
	Git                  ToolIdentity         `json:"git"`
	ByteMetrics          []ByteMetric         `json:"byte_metrics"`
	HistoricalDiagnostic HistoricalDiagnostic `json:"historical_diagnostic"`
	Claims               ProfileClaims        `json:"claims"`
}

type ArtifactIdentity struct {
	Name   string `json:"name"`
	Bytes  uint64 `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type ReproducibilityReceipt struct {
	Schema   string                   `json:"schema"`
	Profiles []ReproducibilityProfile `json:"profiles"`
	Claims   ReproducibilityClaims    `json:"claims"`
}

type ReproducibilityProfile struct {
	Profile               string                   `json:"profile"`
	Kind                  string                   `json:"kind"`
	Scale                 string                   `json:"scale"`
	ProfileDigest         string                   `json:"profile_digest"`
	Builds                []AuthorBuildObservation `json:"builds"`
	ProfileBytesEqual     bool                     `json:"profile_bytes_equal"`
	OracleBytesEqual      bool                     `json:"oracle_bytes_equal"`
	ManifestBytesEqual    bool                     `json:"manifest_bytes_equal"`
	RevisionIdentityEqual bool                     `json:"revision_identity_equal"`
}

type AuthorBuildObservation struct {
	Ordinal               uint64             `json:"ordinal"`
	Artifacts             []ArtifactIdentity `json:"artifacts"`
	Revisions             []RevisionIdentity `json:"revisions"`
	Git                   ToolIdentity       `json:"git"`
	BareGitLogicalBytes   uint64             `json:"bare_git_logical_bytes"`
	BareGitAllocatedBytes uint64             `json:"bare_git_allocated_bytes"`
}

type ReproducibilityClaims struct {
	Synthetic                 bool `json:"synthetic"`
	SourceFree                bool `json:"source_free"`
	SeparateAuthorInvocations bool `json:"separate_author_invocations"`
	EstablishesTargetSLO      bool `json:"establishes_target_slo"`
	QualifiesSupportedScale   bool `json:"qualifies_supported_scale"`
	AuthorizesRelease         bool `json:"authorizes_release"`
}

type ToolIdentity struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	SHA256  string `json:"sha256,omitempty"`
}

type ComparisonReceipt struct {
	Schema                string            `json:"schema"`
	Binary                ToolIdentity      `json:"binary"`
	FixtureProfileDigest  string            `json:"fixture_profile_digest"`
	FixtureManifestDigest string            `json:"fixture_manifest_digest"`
	FixtureTree           string            `json:"fixture_tree"`
	Queries               []string          `json:"queries"`
	QuerySetDigest        string            `json:"query_set_digest"`
	Modes                 []ModeMeasurement `json:"modes"`
	IndexBytesEqual       bool              `json:"index_bytes_equal"`
	IndexedContentEqual   bool              `json:"indexed_content_equal"`
	QueryResultsEqual     bool              `json:"query_results_equal"`
	BoundaryProbes        []BoundaryProbe   `json:"boundary_probes"`
	Claims                ComparisonClaims  `json:"claims"`
}

type ModeMeasurement struct {
	Name          string              `json:"name"`
	Environment   string              `json:"environment_value"`
	IndexSHA256   string              `json:"index_sha256"`
	IndexBytes    uint64              `json:"index_bytes"`
	ShardCount    uint64              `json:"shard_count"`
	ContentSHA256 string              `json:"indexed_content_sha256"`
	QuerySHA256   string              `json:"query_results_sha256"`
	FilesSeen     uint64              `json:"files_seen"`
	Resources     ResourceObservation `json:"resources"`
}

type ResourceObservation struct {
	RSSState              string `json:"rss_state"`
	SampledMaxRSSBytes    uint64 `json:"sampled_max_rss_bytes"`
	RSSMethod             string `json:"rss_method"`
	DescriptorState       string `json:"descriptor_state"`
	SampledMaxDescriptors uint64 `json:"sampled_max_descriptors"`
	DescriptorMethod      string `json:"descriptor_method"`
	IncludesDescendants   bool   `json:"includes_descendants"`
}

type BoundaryProbe struct {
	Mode               string `json:"mode"`
	Kind               string `json:"kind"`
	Boundary           string `json:"boundary"`
	Outcome            string `json:"outcome"`
	Class              string `json:"class"`
	ParentReaped       bool   `json:"parent_reaped"`
	GitDescendantState string `json:"git_descendant_state"`
}

type ComparisonClaims struct {
	SameBinary               bool `json:"same_binary"`
	SmallNeutralFixture      bool `json:"small_neutral_fixture"`
	SelectsProductionReader  bool `json:"selects_production_reader"`
	EstablishesGiantCost     bool `json:"establishes_giant_cost"`
	ChangesProductionPosture bool `json:"changes_production_posture"`
}

type DimensionError struct {
	Dimension string
	Observed  uint64
	Admitted  uint64
}

func (err *DimensionError) Error() string {
	return fmt.Sprintf("%s: %s observed=%d admitted=%d", ErrDimensionExceeded, err.Dimension, err.Observed, err.Admitted)
}

func (err *DimensionError) Unwrap() error { return ErrDimensionExceeded }

func CheckDimension(name string, observed, admitted uint64) error {
	if name == "" || admitted == 0 {
		return errors.New("invalid T40.1 dimension fence")
	}
	if observed > admitted {
		return &DimensionError{Dimension: name, Observed: observed, Admitted: admitted}
	}
	return nil
}

func MarshalCanonical(value any) ([]byte, error) {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func DecodeStrict[T any](data []byte) (T, error) {
	var value T
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return value, err
	}
	return value, nil
}

func SHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func validDigest(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func validObjectID(value string) bool {
	if len(value) != 40 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func ProfileDigest(profile Profile) (string, error) {
	encoded, err := MarshalCanonical(profile)
	if err != nil {
		return "", err
	}
	return SHA256(encoded), nil
}

func ValidateEnvelope(envelope Envelope) error {
	if envelope.Schema != EnvelopeSchema || len(envelope.Profiles) != 2 || len(envelope.Oracles) != 2 ||
		!envelope.Claims.Synthetic || !envelope.Claims.GiantAuthoringExplicit ||
		envelope.Claims.GeneratesGiantInTests || envelope.Claims.EstablishesTargetSLO ||
		envelope.Claims.EstablishesAccuracy || envelope.Claims.QualifiesSupportedScale ||
		envelope.Claims.SelectsBlobReader || envelope.Claims.ChangesProductionBehavior ||
		envelope.Claims.AuthorizesRelease {
		return errors.New("T40.1 envelope or claims are invalid")
	}
	if !slices.Equal(envelope.FailurePoints, FrozenFailurePoints()) ||
		!equalBlobReaderTrial(envelope.BlobReaderTrial, FrozenBlobReaderTrial()) ||
		envelope.HistoricalDiagnostic != FrozenHistoricalDiagnostic() {
		return errors.New("T40.1 envelope seams differ from the frozen set")
	}
	for index, profile := range envelope.Profiles {
		if err := ValidateProfile(profile); err != nil {
			return fmt.Errorf("profile %q: %w", profile.Name, err)
		}
		if err := ValidateOracle(envelope.Oracles[index], profile); err != nil {
			return fmt.Errorf("oracle %q: %w", profile.Name, err)
		}
	}
	return nil
}

func ValidateManifest(manifest Manifest, profile Profile, oracle Oracle) error {
	digest, err := ProfileDigest(profile)
	if err != nil {
		return err
	}
	if manifest.Schema != ManifestSchema || manifest.Profile != profile.Name ||
		manifest.ProfileDigest != digest || manifest.Repository != RepositoryName ||
		manifest.Projection != (profile.Scale == "projection") || manifest.Aggregate != oracle.Aggregate ||
		len(manifest.Revisions) != 3 || !slices.Equal(manifest.FailurePoints, failurePointNames()) {
		return errors.New("T40.1 manifest envelope is invalid")
	}
	for index, revision := range manifest.Revisions {
		expected := oracle.Revisions[index]
		if revision.Revision != expected.Revision || !validObjectID(revision.Commit) ||
			!validObjectID(revision.Tree) || revision.RegularFiles != expected.RegularFiles ||
			revision.UniqueGoBlobs != expected.UniqueGoBlobs || revision.ChangedFiles != expected.ChangedFiles {
			return fmt.Errorf("T40.1 revision %q is invalid", revision.Revision)
		}
	}
	if manifest.Revisions[0].Tree == manifest.Revisions[1].Tree ||
		manifest.Revisions[0].Tree != manifest.Revisions[2].Tree ||
		manifest.Revisions[0].Commit == manifest.Revisions[2].Commit {
		return errors.New("T40.1 A/B/A-content-return tree relation is invalid")
	}
	return validateByteMetrics(manifest.ByteMetrics, []string{
		"declared_repository_go", "logical_source",
	})
}

func ValidateReceipt(receipt Receipt, profile Profile, manifest Manifest) error {
	digest, err := ProfileDigest(profile)
	if err != nil {
		return err
	}
	oracle, err := DeriveOracle(profile)
	if err != nil {
		return err
	}
	profileBytes, err := MarshalCanonical(profile)
	if err != nil {
		return err
	}
	oracleBytes, err := MarshalCanonical(oracle)
	if err != nil {
		return err
	}
	manifestBytes, err := MarshalCanonical(manifest)
	if err != nil {
		return err
	}
	wantArtifacts := []ArtifactIdentity{
		{Name: "profile.json", Bytes: uint64(len(profileBytes)), SHA256: SHA256(profileBytes)},
		{Name: "oracle.json", Bytes: uint64(len(oracleBytes)), SHA256: SHA256(oracleBytes)},
		{Name: "manifest.json", Bytes: uint64(len(manifestBytes)), SHA256: SHA256(manifestBytes)},
	}
	if receipt.Schema != ReceiptSchema || receipt.Outcome != "authored" || receipt.Profile != profile.Name ||
		receipt.ProfileDigest != digest || !slices.Equal(receipt.Revisions, manifest.Revisions) ||
		!slices.Equal(receipt.Artifacts, wantArtifacts) || receipt.Git.Name != "git" || receipt.Git.Version == "" ||
		receipt.HistoricalDiagnostic != FrozenHistoricalDiagnostic() ||
		!receipt.Claims.Synthetic || receipt.Claims.EstablishesTargetSLO || receipt.Claims.EstablishesAccuracy ||
		receipt.Claims.QualifiesSupportedScale || receipt.Claims.AuthorizesRelease ||
		receipt.Claims.ExplicitAuthoringOnly != (profile.Scale == "frozen") {
		return errors.New("T40.1 author receipt is invalid")
	}
	if err := validateByteMetrics(receipt.ByteMetrics, []string{
		"profile_canonical", "oracle_canonical", "manifest_canonical",
		"declared_repository_go", "logical_source", "bare_git_logical", "bare_git_allocated",
	}); err != nil {
		return err
	}
	wantBytes := []uint64{
		uint64(len(profileBytes)), uint64(len(oracleBytes)), uint64(len(manifestBytes)),
		oracle.Aggregate.DeclaredRepositoryGoBytes, oracle.Aggregate.LogicalSourceBytes,
	}
	for index, want := range wantBytes {
		if receipt.ByteMetrics[index].Bytes != want {
			return fmt.Errorf("T40.1 byte metric %q differs from its artifact", receipt.ByteMetrics[index].Kind)
		}
	}
	return nil
}

func ValidateComparison(receipt ComparisonReceipt) error {
	profile, profileErr := ProjectionProfile("structural")
	wantProfileDigest, digestErr := ProfileDigest(profile)
	wantQueryDigest, queryErr := QuerySetDigest(FrozenBlobReaderTrial().Queries)
	if receipt.Schema != ComparisonSchema || receipt.Binary.Name != "zoekt-git-index" ||
		receipt.Binary.Version == "" || !validDigest(receipt.Binary.SHA256) || len(receipt.Modes) != 2 ||
		profileErr != nil || digestErr != nil || queryErr != nil ||
		receipt.FixtureProfileDigest != wantProfileDigest ||
		receipt.FixtureManifestDigest != structuralProjectionManifestDigest ||
		receipt.FixtureTree != structuralProjectionTree ||
		!slices.Equal(receipt.Queries, FrozenBlobReaderTrial().Queries) ||
		receipt.QuerySetDigest != wantQueryDigest ||
		!receipt.IndexedContentEqual || !receipt.QueryResultsEqual ||
		len(receipt.BoundaryProbes) != 6 || !receipt.Claims.SameBinary ||
		!receipt.Claims.SmallNeutralFixture || receipt.Claims.SelectsProductionReader ||
		receipt.Claims.EstablishesGiantCost || receipt.Claims.ChangesProductionPosture {
		return errors.New("T40.1 blob-reader comparison envelope is invalid")
	}
	wantModes := FrozenBlobReaderTrial().Modes
	for index, measurement := range receipt.Modes {
		if measurement.Name != wantModes[index].Name || measurement.Environment != wantModes[index].Environment ||
			!validDigest(measurement.IndexSHA256) || measurement.IndexBytes == 0 || measurement.ShardCount == 0 ||
			!validDigest(measurement.ContentSHA256) || !validDigest(measurement.QuerySHA256) ||
			measurement.FilesSeen == 0 || validateResourceObservation(measurement.Resources) != nil {
			return fmt.Errorf("T40.1 blob-reader mode %q is invalid", measurement.Name)
		}
	}
	if receipt.IndexBytesEqual != (receipt.Modes[0].IndexSHA256 == receipt.Modes[1].IndexSHA256 &&
		receipt.Modes[0].IndexBytes == receipt.Modes[1].IndexBytes) ||
		receipt.Modes[0].ContentSHA256 != receipt.Modes[1].ContentSHA256 ||
		receipt.Modes[0].QuerySHA256 != receipt.Modes[1].QuerySHA256 ||
		receipt.Modes[0].FilesSeen != receipt.Modes[1].FilesSeen {
		return errors.New("T40.1 blob-reader modes differ semantically")
	}
	wantKinds := []string{"cancellation", "cancellation", "missing_object", "missing_object", "corrupt_object", "corrupt_object"}
	for index, probe := range receipt.BoundaryProbes {
		wantMode := wantModes[index%2].Name
		if probe.Mode != wantMode || probe.Kind != wantKinds[index] || probe.Boundary == "" || !probe.ParentReaped {
			return fmt.Errorf("T40.1 boundary probe %d is invalid", index)
		}
		if probe.Kind == "cancellation" {
			wantBoundary := "zoekt_child_started"
			if probe.Mode == "cat_file_batch_candidate" {
				wantBoundary = "git_descendant_observed"
			}
			wantGitState := "not_observed"
			if probe.Mode == "cat_file_batch_candidate" {
				wantGitState = "observed_reaped"
			}
			if probe.Outcome != "refused" || probe.Class != "cancellation" || probe.Boundary != wantBoundary ||
				probe.GitDescendantState != wantGitState {
				return fmt.Errorf("T40.1 cancellation probe %d is invalid", index)
			}
			continue
		}
		if probe.Outcome != "refused" && probe.Outcome != "completed_with_gap" || probe.Class != probe.Kind ||
			probe.Boundary != "object_read" || probe.GitDescendantState != "not_measured" {
			return fmt.Errorf("T40.1 object probe %d is invalid", index)
		}
	}
	return nil
}

func validateResourceObservation(observation ResourceObservation) error {
	if observation.RSSState != "sampled" && observation.RSSState != "unavailable" ||
		observation.DescriptorState != "sampled" && observation.DescriptorState != "unavailable" ||
		observation.RSSState == "sampled" && (observation.SampledMaxRSSBytes == 0 || observation.RSSMethod == "") ||
		observation.RSSState == "unavailable" && (observation.SampledMaxRSSBytes != 0 || observation.RSSMethod != "") ||
		observation.DescriptorState == "sampled" && (observation.SampledMaxDescriptors == 0 || observation.DescriptorMethod == "") ||
		observation.DescriptorState == "unavailable" && (observation.SampledMaxDescriptors != 0 || observation.DescriptorMethod != "") ||
		(observation.RSSState == "sampled" || observation.DescriptorState == "sampled") && !observation.IncludesDescendants {
		return errors.New("T40.1 resource observation is invalid")
	}
	return nil
}

func QuerySetDigest(queries []string) (string, error) {
	encoded, err := MarshalCanonical(queries)
	if err != nil {
		return "", err
	}
	return SHA256(encoded), nil
}

func validateByteMetrics(metrics []ByteMetric, names []string) error {
	if len(metrics) != len(names) {
		return errors.New("T40.1 byte metric inventory is incomplete")
	}
	for index, metric := range metrics {
		if metric.Kind != names[index] || metric.Measurement != "exact" ||
			(metric.Bytes == 0 && metric.Kind != "bare_git_allocated") {
			return fmt.Errorf("T40.1 byte metric %q is invalid", metric.Kind)
		}
	}
	return nil
}
