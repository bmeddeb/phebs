// Package t4110 defines the source-free retained evidence contract for the
// neutral T41.10 10,000-service closure gate. The live runner is deliberately
// separate: this package owns only the frozen receipt model, validation, and
// create-only authoring seam.
package t4110

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/spike/t411"
)

const (
	ReceiptSchema   = "t4110-neutral-service-closure-receipt-v1"
	MaxReceiptBytes = 128 << 10

	recoveryArtifactCount                = 6
	maxRecoveryArtifactBytes             = uint64(9 << 40)
	targetRelationshipObservationMembers = 16
	targetRelationshipRecords            = t411.AcceptedServiceTarget
	targetRelationshipCandidateMembers   = 3
	targetRelationshipDeclaredBytes      = 580_000

	OutcomePassed = "passed"
	StepPassed    = "passed"

	RSSMethodProcessTree = "native-descendant-rss-50ms-sampled-peak-bytes"
	DiskMethodWalk       = "lstat-logical-and-allocated-bytes"
)

var measuredPhaseNames = []string{
	"cold_publish_activate",
	"warm_noop",
	"point_page_queries",
	"one_service_delta",
	"percent_delta",
	"removal_readd",
	"a_b_a",
	"transition_profile",
	"archive_restore",
	"collection",
	"product_reader_queries",
}

var composedGateNames = []string{
	"t411_input_and_bound_contract",
	"service_runtime_and_recovery",
	"authorized_product_parity",
	"archive_restore_and_lifecycle",
	"cost_and_source_free_evidence",
}

var composedGateTests = map[string][]string{
	"t411_input_and_bound_contract": {
		"go:spike/t4110#TestLiveTargetCatalogUsesExpandedV3Input",
		"go:spike/t4110#TestMappedTransitionRetainsExpandedV3Target",
		"go:spike/t4110#TestMappedTransitionIncarnationOracleTracksOmissionAndExplicitRejection",
		"go:spike/t4110#TestFrozenTargetRelationshipInputShape",
		"go:spike/t411#TestFrozenEnvelopeIsDeterministicAndExact",
		"go:spike/t411#TestRetainedArtifactsMatchFrozenEnvelope",
		"go:spike/t411#TestEveryAggregateLimitAcceptsExactAndRefusesOneOver",
		"go:spike/t411#TestTransitionProfileUsesRealV2Semantics",
		"go:internal/servicecatalogv3#TestT411ExactAggregateAndByteBoundaries",
		"go:internal/servicecatalogv3#TestDecodeCatalogExpandedBoundsAndRootRoundTrip",
		"go:internal/servicecatalogv3#TestV2SuccessorBoundaryAndPersistedConversion",
		"go:internal/servicecatalogv3#TestSourceGenerationDigestV2UsesStableSourceTuple",
		"go:internal/servicecatalogv3#TestLegacyRootCompatibilityAndRebuild",
		"go:internal/servicecatalogv3#TestSuccessorAggregateBoundary",
		"go:internal/servicecatalogv3#TestDecodeCanonicalPreflightsCollections",
		"go:internal/servicecatalogv3#TestExpandedRulesAndDowngradeRefusal",
		"go:internal/servicecatalogv3#TestMaximumServiceAndPlacementFitOneMember",
		"go:internal/relationshippublication#TestProjectionBucketsV3PreserveMaximumAlignedClaims",
		"go:internal/relationshippublication#TestT411MaximumProfileEmptyAndMixedBuildPublishV3",
		"go:internal/relationshippublication#TestT411MaximumProfileDenseBuildPublishV3",
	},
	"service_runtime_and_recovery": {
		"go:spike/t4110#TestTargetRelationshipScheduleExpandsBeforeClaim",
		"go:spike/t4110#TestSelectedServiceComparisonUsesSemanticSlices",
		"go:spike/t4110#TestSuccessorCatalogRebindsFrozenSourceComplement",
		"go:internal/servicecatalogv3#TestReadCacheReturnsNotExistForServiceRangeHole",
		"go:internal/servicecatalogingest#TestV3ReconcilerCommittedCensusNoopAndVersionRefusal",
		"go:internal/relationshippublication#TestRuntimeV3DirectBuildDoesNotRequireV2Relationship",
		"go:internal/store#TestServiceStateV3TenThousandBoundedColdNoopDeltaAndActivation",
		"go:internal/store#TestServiceCatalogV3SourceGenerationCompatibilityMigration",
		"go:internal/store#TestServiceRuntimeSelectorCompatibilityLatchIsIrreversible",
		"go:internal/store#TestServiceRuntimeSelectorCASAdvancesSupportedCompatibilityMarkers",
		"go:internal/store#TestServiceStateV3SparseSuccessorKeepsUnchangedCatalogProvenance",
		"go:internal/store#TestServiceStateV3RemovalReaddAndABA",
		"go:internal/store#TestServiceStateV3PartialActivationKeepsMatchingSummaryReadable",
		"go:internal/store#TestSelectedV3SnapshotSurvivesSparseSuccessor",
		"go:internal/store#TestServiceStateV3CrashReplay",
		"go:internal/store#TestServiceStateV3CatalogSuccessorSupersedesActivationLease",
		"go:internal/store#TestGenerationScheduleRetryCoalescingAndStaleFence",
		"go:internal/store#TestServiceRuntimeSelectorCASAndReverseAreMonotonic",
	},
	"authorized_product_parity": {
		"go:internal/search#TestServiceSearchV3StreamMatchesScopedReceipt",
		"go:internal/search#TestRuntimeScopedSearchKeepsSelectedV3AcrossDarkCandidateAdvance",
		"go:internal/search#TestRuntimeScopedSearchKeepsSelectedV3AfterFlatCurrentAdvances",
		"go:internal/search#TestRuntimeScopedSearchV3RefusesFinalSelectorDrift",
		"go:internal/api#TestServiceDirectoryV3AuthorizationPrecedesAuthorityReads",
		"go:internal/api#TestRuntimeServiceDirectoryKeepsSelectedV3AcrossDarkCandidateAdvance",
		"go:internal/api#TestRuntimeServiceDirectoryV3RefusesFinalSelectorDrift",
		"go:internal/api#TestServiceDirectoryV3TenThousandServicePagination",
		"go:internal/api#TestV3ScopedSearchSharesHTTPMCPAuthorityAndSSEBoundary",
		"vitest:ServiceDirectoryPage#renders exact authority, page summaries, lifecycle states, roles, and source-free detail",
		"vitest:ServiceDirectoryPage#offers a filter-preserving first-page route when a cursor is invalid",
	},
	"archive_restore_and_lifecycle": {
		"go:internal/observationpublication#TestObservationArchiveRoundTripsUnsupportedOnlyV1AndV2",
		"go:internal/relationshippublication#TestArchiveV3IndependentCompositeRoundTrip",
		"go:internal/store#TestServiceCatalogV3LifecycleRetainsCandidateAndTwoPrior",
		"go:internal/store#TestServiceCatalogV3RepublishReadmitsInterruptedCollectingRoot",
		"go:internal/store#TestServiceCatalogV3LifecyclePinsRestartAndMalformedIsolation",
		"go:internal/store#TestServiceCatalogV3RelationshipReferenceReconcileAndLifecycleFence",
		"go:internal/store#TestServiceCatalogV3RelationshipReferenceDrainAndFinalizeFences",
		"go:internal/lifecycle#TestRunnerCompletesFreshCycleAfterPressureRecovery",
		"go:internal/lifecycle#TestRunnerPressureRecoveryDrivesCatalogV3Owner",
		"go:internal/store#TestClearAllGenerationScheduleStateForRestore",
	},
	"cost_and_source_free_evidence": {
		"go:spike/t4013#TestCustodyCommandCancellationTerminatesProcessSession",
		"go:spike/t411#TestReusableTargetAndTransitionCorporaMatchFrozenEnvelope",
		"go:spike/t4110#TestT4110ReceiptRoundTripIsCanonicalAndSourceFree",
		"go:spike/t4110#TestT4110AuthorIsCreateOnlyAndAtomic",
	},
}

var checkNames = []string{
	"accepted_target_10000",
	"accepted_floor_8000_explicit",
	"independent_queryability",
	"transition_profile",
	"exact_bound_and_one_over",
	"cold",
	"warm_noop",
	"point_and_page",
	"one_service_delta",
	"percent_delta",
	"removal_readd",
	"a_b_a",
	"partial_activation",
	"crash_recovery",
	"stale_worker",
	"authorization",
	"pressure",
	"backup_restore",
	"collection",
	"authorized_http_mcp_ui",
	"no_service_count_times_repository_bytes",
	"source_free_receipt",
	"clean_teardown",
}

type checkEvidenceBinding struct {
	Phases        []string
	Tests         []string
	ReceiptOracle bool
}

var checkEvidence = map[string]checkEvidenceBinding{
	"accepted_target_10000": {
		Phases: []string{"cold_publish_activate", "point_page_queries"}, ReceiptOracle: true,
	},
	"accepted_floor_8000_explicit": {ReceiptOracle: true},
	"independent_queryability": {
		Phases: []string{"point_page_queries"}, ReceiptOracle: true,
	},
	"transition_profile": {
		Phases: []string{"transition_profile"},
		Tests: []string{
			"go:spike/t411#TestTransitionProfileUsesRealV2Semantics",
			"go:spike/t4110#TestMappedTransitionIncarnationOracleTracksOmissionAndExplicitRejection",
		},
	},
	"exact_bound_and_one_over": {
		Tests: []string{
			"go:spike/t411#TestEveryAggregateLimitAcceptsExactAndRefusesOneOver",
			"go:internal/servicecatalogv3#TestT411ExactAggregateAndByteBoundaries",
			"go:internal/servicecatalogv3#TestDecodeCatalogExpandedBoundsAndRootRoundTrip",
			"go:internal/servicecatalogv3#TestV2SuccessorBoundaryAndPersistedConversion",
			"go:internal/servicecatalogv3#TestSuccessorAggregateBoundary",
			"go:internal/servicecatalogv3#TestDecodeCanonicalPreflightsCollections",
			"go:internal/servicecatalogv3#TestExpandedRulesAndDowngradeRefusal",
			"go:internal/servicecatalogv3#TestMaximumServiceAndPlacementFitOneMember",
			"go:internal/relationshippublication#TestProjectionBucketsV3PreserveMaximumAlignedClaims",
		},
	},
	"cold":              {Phases: []string{"cold_publish_activate"}},
	"warm_noop":         {Phases: []string{"warm_noop"}},
	"point_and_page":    {Phases: []string{"point_page_queries"}},
	"one_service_delta": {Phases: []string{"one_service_delta"}},
	"percent_delta":     {Phases: []string{"percent_delta"}},
	"removal_readd":     {Phases: []string{"removal_readd"}},
	"a_b_a":             {Phases: []string{"a_b_a"}},
	"partial_activation": {
		Phases: []string{"one_service_delta"},
		Tests: []string{
			"go:internal/store#TestServiceStateV3PartialActivationKeepsMatchingSummaryReadable",
		},
	},
	"crash_recovery": {
		Tests: []string{"go:internal/store#TestServiceStateV3CrashReplay"},
	},
	"stale_worker": {
		Tests: []string{
			"go:internal/store#TestServiceStateV3CatalogSuccessorSupersedesActivationLease",
			"go:internal/store#TestGenerationScheduleRetryCoalescingAndStaleFence",
		},
	},
	"authorization": {
		Tests: []string{"go:internal/api#TestServiceDirectoryV3AuthorizationPrecedesAuthorityReads"},
	},
	"pressure": {
		Phases: []string{"collection"},
		Tests:  []string{"go:internal/lifecycle#TestRunnerPressureRecoveryDrivesCatalogV3Owner"},
	},
	"backup_restore": {
		Phases: []string{"archive_restore"},
		Tests: []string{
			"go:internal/observationpublication#TestObservationArchiveRoundTripsUnsupportedOnlyV1AndV2",
			"go:internal/relationshippublication#TestArchiveV3IndependentCompositeRoundTrip",
		},
	},
	"collection": {
		Phases: []string{"collection"},
		Tests:  []string{"go:internal/store#TestServiceCatalogV3LifecycleRetainsCandidateAndTwoPrior"},
	},
	"authorized_http_mcp_ui": {
		Phases: []string{"product_reader_queries"},
		Tests: []string{
			"go:internal/search#TestServiceSearchV3StreamMatchesScopedReceipt",
			"go:internal/search#TestRuntimeScopedSearchKeepsSelectedV3AcrossDarkCandidateAdvance",
			"go:internal/search#TestRuntimeScopedSearchKeepsSelectedV3AfterFlatCurrentAdvances",
			"go:internal/search#TestRuntimeScopedSearchV3RefusesFinalSelectorDrift",
			"go:internal/api#TestRuntimeServiceDirectoryKeepsSelectedV3AcrossDarkCandidateAdvance",
			"go:internal/api#TestRuntimeServiceDirectoryV3RefusesFinalSelectorDrift",
			"go:internal/api#TestServiceDirectoryV3TenThousandServicePagination",
			"go:internal/api#TestV3ScopedSearchSharesHTTPMCPAuthorityAndSSEBoundary",
			"vitest:ServiceDirectoryPage#renders exact authority, page summaries, lifecycle states, roles, and source-free detail",
			"vitest:ServiceDirectoryPage#offers a filter-preserving first-page route when a cursor is invalid",
		},
	},
	"no_service_count_times_repository_bytes": {
		Phases: []string{"cold_publish_activate"}, ReceiptOracle: true,
	},
	"source_free_receipt": {
		Tests: []string{"go:spike/t4110#TestT4110ReceiptRoundTripIsCanonicalAndSourceFree"},
	},
	"clean_teardown": {ReceiptOracle: true},
}

var forbiddenReceiptFragments = []string{
	"/users/",
	"/private/",
	"file://",
	"repository_name",
	"repository_url",
	"source_path",
	"source_commit",
	"service_key",
	"query_text",
	"result_rows",
	"raw_error",
	"clone_url",
	"password",
	"credential",
	"hostname",
	"username",
	"object_id",
}

type Receipt struct {
	Schema         string             `json:"schema"`
	Outcome        string             `json:"outcome"`
	MeasuredOn     string             `json:"measured_on"`
	Implementation Implementation     `json:"implementation"`
	Inputs         InputBindings      `json:"inputs"`
	Environment    Environment        `json:"environment"`
	Population     Population         `json:"population"`
	Queryability   QueryabilityOracle `json:"queryability"`
	PhysicalWork   PhysicalWorkOracle `json:"physical_work"`
	MeasuredPhases []MeasuredPhase    `json:"measured_phases"`
	ComposedGates  []ComposedGate     `json:"composed_gates"`
	Checks         []Check            `json:"checks"`
	Teardown       Teardown           `json:"teardown"`
	SourceFree     bool               `json:"source_free"`
	NonClaims      NonClaims          `json:"nonclaims"`
}

type Implementation struct {
	Commit                  string `json:"commit"`
	CleanTree               bool   `json:"clean_tree"`
	AuthorExecutableSHA256  string `json:"author_executable_sha256"`
	PhebsExecutableSHA256   string `json:"phebs_executable_sha256"`
	ZoektExecutableSHA256   string `json:"zoekt_executable_sha256"`
	GitExecutableSHA256     string `json:"git_executable_sha256"`
	SurrealExecutableSHA256 string `json:"surreal_executable_sha256"`
	GoExecutableSHA256      string `json:"go_executable_sha256"`
	NodeExecutableSHA256    string `json:"node_executable_sha256"`
	NPMExecutableSHA256     string `json:"npm_executable_sha256"`
	BrowserExecutableSHA256 string `json:"browser_executable_sha256"`
}

type InputBindings struct {
	T411EnvelopeSHA256      string `json:"t411_envelope_sha256"`
	T411ReceiptSHA256       string `json:"t411_receipt_sha256"`
	TargetProfileSHA256     string `json:"target_profile_sha256"`
	TransitionProfileSHA256 string `json:"transition_profile_sha256"`
}

type Environment struct {
	GOOS        string `json:"goos"`
	GOARCH      string `json:"goarch"`
	GoVersion   string `json:"go_version"`
	LogicalCPUs int    `json:"logical_cpus"`
	GOMAXPROCS  int    `json:"gomaxprocs"`
	RSSMethod   string `json:"rss_method"`
	DiskMethod  string `json:"disk_method"`
}

type Population struct {
	AcceptedFloor       int   `json:"accepted_floor"`
	AcceptedTarget      int   `json:"accepted_target"`
	AcceptedServices    int   `json:"accepted_services"`
	TotalServiceRecords int   `json:"total_service_records"`
	Memberships         int   `json:"memberships"`
	DistinctPaths       int   `json:"distinct_paths"`
	RegularFiles        int   `json:"regular_files"`
	FixtureContentBytes int64 `json:"fixture_content_bytes"`
}

type QueryabilityOracle struct {
	PublishedAcceptedServices int `json:"published_accepted_services"`
	CurrentAcceptedServices   int `json:"current_accepted_services"`
	IndependentQueries        int `json:"independent_queries"`
	IndependentMatches        int `json:"independent_matches"`
	MissingServices           int `json:"missing_services"`
	UnexpectedServices        int `json:"unexpected_services"`
}

type PhysicalWorkOracle struct {
	CorpusPasses uint64 `json:"corpus_passes"`
}

type MeasuredPhase struct {
	Name    string    `json:"name"`
	Outcome string    `json:"outcome"`
	Cost    PhaseCost `json:"cost"`
}

type PhaseCost struct {
	WallMilliseconds                     int64  `json:"wall_ms"`
	PeakRSSBytes                         int64  `json:"peak_rss_bytes"`
	DataLogicalBytes                     int64  `json:"data_logical_bytes"`
	DataAllocatedBytes                   int64  `json:"data_allocated_bytes"`
	SelectedStateRootReads               uint64 `json:"selected_state_root_reads"`
	SelectedStateMemberReads             uint64 `json:"selected_state_member_reads"`
	SelectedStateRootValidations         uint64 `json:"selected_state_root_validations"`
	SelectedStateMemberValidations       uint64 `json:"selected_state_member_validations"`
	StateChunkTransactions               uint64 `json:"state_chunk_transactions"`
	StateRowsRead                        uint64 `json:"state_rows_read"`
	StateRowsApplied                     uint64 `json:"state_rows_applied"`
	SearchFilesOffered                   uint64 `json:"search_files_offered"`
	SearchContentReads                   uint64 `json:"search_content_reads"`
	SearchDeclaredBytes                  uint64 `json:"search_declared_bytes"`
	SourceCensusRecords                  uint64 `json:"source_census_records"`
	SourceCensusMembers                  uint64 `json:"source_census_members"`
	SourceCensusPlacements               uint64 `json:"source_census_placements"`
	SourceCensusDeclaredBytes            uint64 `json:"source_census_declared_bytes"`
	ObservationInputMembers              uint64 `json:"observation_input_members"`
	ObservationMembers                   uint64 `json:"observation_members"`
	ObservationRecords                   uint64 `json:"observation_records"`
	ObservationObservedRecords           uint64 `json:"observation_observed_records"`
	ObservationSourceBlobReads           uint64 `json:"observation_source_blob_reads"`
	CandidateInputReadsUnavailable       bool   `json:"candidate_input_reads_unavailable"`
	CandidateResultMembers               uint64 `json:"candidate_result_members"`
	CandidateResultRecords               uint64 `json:"candidate_result_records"`
	CandidateDeclaredBytes               uint64 `json:"candidate_declared_bytes"`
	RelationshipScheduleChunks           uint64 `json:"relationship_schedule_chunks"`
	RelationshipComponentPublishes       uint64 `json:"relationship_component_publishes"`
	RelationshipComponentMembers         uint64 `json:"relationship_component_members"`
	RelationshipComponentRecords         uint64 `json:"relationship_component_records"`
	RelationshipPublishes                uint64 `json:"relationship_publishes"`
	RelationshipServiceMembers           uint64 `json:"relationship_service_members"`
	RelationshipServiceRecords           uint64 `json:"relationship_service_records"`
	RelationshipProjectionMembers        uint64 `json:"relationship_projection_members"`
	RelationshipProjectionRecords        uint64 `json:"relationship_projection_records"`
	LifecycleRecordsDeleted              uint64 `json:"lifecycle_records_deleted"`
	LifecycleRetiredLogicalBytes         uint64 `json:"lifecycle_retired_logical_bytes"`
	LifecycleDeletedRootBytes            uint64 `json:"lifecycle_deleted_root_bytes"`
	LifecycleDeletedMemberBytes          uint64 `json:"lifecycle_deleted_member_bytes"`
	LifecycleOwnerTurns                  uint64 `json:"lifecycle_owner_turns"`
	LifecyclePressureCollectObservations uint64 `json:"lifecycle_pressure_collect_observations"`
	LifecyclePressureNormalObservations  uint64 `json:"lifecycle_pressure_normal_observations"`
	ArchiveArtifactCount                 uint64 `json:"archive_artifact_count"`
	ArchiveArtifactBytes                 uint64 `json:"archive_artifact_bytes"`
	RestoredArtifactCount                uint64 `json:"restored_artifact_count"`
	RestoredArtifactBytes                uint64 `json:"restored_artifact_bytes"`
	ProductQueries                       uint64 `json:"product_queries"`
	BrowserProductReads                  uint64 `json:"browser_product_reads"`
	ChangedRows                          uint64 `json:"changed_rows"`
	PreimageRowsWritten                  uint64 `json:"preimage_rows_written"`
	PreimageSummariesWritten             uint64 `json:"preimage_summaries_written"`
	PreimageRowsCollected                uint64 `json:"preimage_rows_collected"`
	PreimageSummariesCollected           uint64 `json:"preimage_summaries_collected"`
}

type ComposedGate struct {
	Name    string   `json:"name"`
	Outcome string   `json:"outcome"`
	Tests   []string `json:"tests"`
}

type Check struct {
	Name    string `json:"name"`
	Outcome string `json:"outcome"`
}

type Teardown struct {
	StoreClosed             bool `json:"store_closed"`
	TemporaryCustodyRemoved bool `json:"temporary_custody_removed"`
	ChildrenRemaining       int  `json:"children_remaining"`
}

// NonClaims is deliberately all-positive claim language. Every field must be
// false; adding a claim requires a schema change and independent review.
type NonClaims struct {
	LargeRepositoryEnvelope bool `json:"large_repository_envelope"`
	TargetSLO               bool `json:"target_slo"`
	SupportedCustomerLimit  bool `json:"supported_customer_limit"`
	AccuracyOrCompleteness  bool `json:"accuracy_or_completeness"`
	Release                 bool `json:"release"`
	Migration               bool `json:"migration"`
	Decommission            bool `json:"decommission"`
	P6OrTopology            bool `json:"p6_or_topology"`
}

type ArtifactIdentity struct {
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// newDraft constructs the fixed T41.10 inventories and T41.1 bindings. The
// returned value is intentionally not yet valid: the live runner must fill
// outcomes, measurements, oracles, and teardown before authoring it.
func newDraft(implementationCommit, measuredOn string, environment Environment) (Receipt, error) {
	inputs, population := frozenBindings()
	return Receipt{
		Schema: ReceiptSchema, MeasuredOn: measuredOn,
		Implementation: Implementation{Commit: implementationCommit, CleanTree: true},
		Inputs:         inputs, Environment: environment, Population: population,
		MeasuredPhases: phasesWithNames(), ComposedGates: gatesWithNames(),
		Checks: checksWithNames(), SourceFree: true,
	}, nil
}

// MeasuredPhaseNames returns a copy of the closed live-measurement inventory.
func MeasuredPhaseNames() []string { return slices.Clone(measuredPhaseNames) }

// ComposedGateNames returns a copy of the closed composed-regression inventory.
func ComposedGateNames() []string { return slices.Clone(composedGateNames) }

// CheckNames returns a copy of the closed T41.10 acceptance inventory.
func CheckNames() []string { return slices.Clone(checkNames) }

// ValidateReceipt applies the complete passing receipt contract.
func ValidateReceipt(receipt Receipt) error {
	inputs, population := frozenBindings()
	if receipt.Schema != ReceiptSchema || receipt.Outcome != OutcomePassed ||
		!validDate(receipt.MeasuredOn) || !validCommit(receipt.Implementation.Commit) ||
		!receipt.Implementation.CleanTree || !validImplementation(receipt.Implementation) ||
		receipt.Inputs != inputs ||
		receipt.Population != population || !validEnvironment(receipt.Environment) ||
		!receipt.SourceFree || receipt.NonClaims != (NonClaims{}) {
		return errors.New("T41.10 receipt envelope is invalid")
	}
	if err := validateQueryability(receipt.Queryability, population); err != nil {
		return err
	}
	if err := validatePhysicalWork(receipt.PhysicalWork); err != nil {
		return err
	}
	if len(receipt.MeasuredPhases) != len(measuredPhaseNames) {
		return errors.New("T41.10 measured phase inventory is incomplete")
	}
	for index, name := range measuredPhaseNames {
		phase := receipt.MeasuredPhases[index]
		if phase.Name != name || phase.Outcome != StepPassed ||
			phase.Cost.WallMilliseconds < 1 || phase.Cost.PeakRSSBytes < 1 ||
			phase.Cost.DataLogicalBytes < 1 || phase.Cost.DataAllocatedBytes < 1 {
			return fmt.Errorf("T41.10 measured phase %q is invalid", name)
		}
		if err := validatePhaseCostShape(name, phase.Cost, population); err != nil {
			return err
		}
		if index != 0 && (phase.Cost.SearchFilesOffered != 0 ||
			phase.Cost.SearchContentReads != 0 || phase.Cost.SearchDeclaredBytes != 0 ||
			phase.Cost.SourceCensusRecords != 0 || phase.Cost.SourceCensusMembers != 0 ||
			phase.Cost.SourceCensusPlacements != 0 ||
			phase.Cost.SourceCensusDeclaredBytes != 0 ||
			phase.Cost.ObservationInputMembers != 0 || phase.Cost.ObservationMembers != 0 ||
			phase.Cost.ObservationRecords != 0 || phase.Cost.ObservationObservedRecords != 0 ||
			phase.Cost.ObservationSourceBlobReads != 0 ||
			phase.Cost.CandidateInputReadsUnavailable ||
			phase.Cost.CandidateResultMembers != 0 || phase.Cost.CandidateResultRecords != 0 ||
			phase.Cost.CandidateDeclaredBytes != 0) {
			return fmt.Errorf("T41.10 measured phase %q repeated physical source work", name)
		}
		if index != 0 && (phase.Cost.RelationshipComponentPublishes != 0 ||
			phase.Cost.RelationshipComponentMembers != 0 ||
			phase.Cost.RelationshipComponentRecords != 0) {
			return fmt.Errorf("T41.10 measured phase %q rebuilt relationship components", name)
		}
		if phase.Cost.RelationshipProjectionMembers != 0 ||
			phase.Cost.RelationshipProjectionRecords != 0 {
			return fmt.Errorf("T41.10 measured phase %q invented target relationships", name)
		}
		if phase.Cost.LifecycleOwnerTurns > 4_096 ||
			phase.Cost.LifecyclePressureCollectObservations > phase.Cost.LifecycleOwnerTurns ||
			phase.Cost.LifecyclePressureNormalObservations > phase.Cost.LifecycleOwnerTurns {
			return fmt.Errorf("T41.10 measured phase %q lifecycle pressure cost is out of bounds", name)
		}
		if name != "collection" && (phase.Cost.LifecycleOwnerTurns != 0 ||
			phase.Cost.LifecyclePressureCollectObservations != 0 ||
			phase.Cost.LifecyclePressureNormalObservations != 0) {
			return fmt.Errorf("T41.10 measured phase %q invented lifecycle pressure work", name)
		}
		if name != "archive_restore" && (phase.Cost.ArchiveArtifactCount != 0 ||
			phase.Cost.ArchiveArtifactBytes != 0 || phase.Cost.RestoredArtifactCount != 0 ||
			phase.Cost.RestoredArtifactBytes != 0) {
			return fmt.Errorf("T41.10 measured phase %q invented archive/restore work", name)
		}
		if name != "product_reader_queries" && phase.Cost.BrowserProductReads != 0 {
			return fmt.Errorf("T41.10 measured phase %q invented browser product reads", name)
		}
		if phase.Cost.SelectedStateRootReads != phase.Cost.SelectedStateRootValidations ||
			phase.Cost.SelectedStateMemberReads != phase.Cost.SelectedStateMemberValidations {
			return fmt.Errorf("T41.10 measured phase %q cache cost is incoherent", name)
		}
		if phase.Cost.SelectedStateRootValidations == 0 ||
			phase.Cost.SelectedStateMemberValidations == 0 {
			return fmt.Errorf("T41.10 measured phase %q cache cost is absent", name)
		}
	}
	if err := validateMeasuredPhaseOracles(receipt); err != nil {
		return err
	}
	cold := receipt.MeasuredPhases[0].Cost
	maxFiles := uint64(population.RegularFiles) * receipt.PhysicalWork.CorpusPasses
	maxBytes := uint64(population.FixtureContentBytes) * receipt.PhysicalWork.CorpusPasses
	if cold.SearchFilesOffered > maxFiles || cold.SearchContentReads > maxFiles ||
		cold.SearchDeclaredBytes > maxBytes {
		return errors.New("T41.10 cold physical source work exceeds the bounded corpus passes")
	}
	if len(receipt.ComposedGates) != len(composedGateNames) {
		return errors.New("T41.10 composed gate inventory is incomplete")
	}
	for index, name := range composedGateNames {
		gate := receipt.ComposedGates[index]
		if gate.Name != name || gate.Outcome != StepPassed ||
			!slices.Equal(gate.Tests, composedGateTests[name]) {
			return fmt.Errorf("T41.10 composed gate %q is invalid", name)
		}
	}
	if len(receipt.Checks) != len(checkNames) {
		return errors.New("T41.10 check inventory is incomplete")
	}
	if err := validateCheckEvidence(); err != nil {
		return err
	}
	for index, name := range checkNames {
		check := receipt.Checks[index]
		if check.Name != name || check.Outcome != StepPassed {
			return fmt.Errorf("T41.10 check %q is invalid", name)
		}
	}
	if !receipt.Teardown.StoreClosed || !receipt.Teardown.TemporaryCustodyRemoved ||
		receipt.Teardown.ChildrenRemaining != 0 {
		return errors.New("T41.10 teardown is incomplete")
	}
	return nil
}

func validatePhaseCostShape(name string, cost PhaseCost, population Population) error {
	const (
		maxLifecycleRecords = uint64(4 * 4_096 * lifecycle.MaxDeletesPerTick)
		maxRetiredLogical   = uint64(256 << 30)
		maxDeletedRoots     = uint64(4 << 30)
		maxDeletedMembers   = uint64(32 << 30)
		maxPhaseWall        = int64((24 * time.Hour) / time.Millisecond)
	)
	if cost.WallMilliseconds > maxPhaseWall ||
		cost.PeakRSSBytes > int64(maxRecoveryArtifactBytes) ||
		cost.DataLogicalBytes > int64(maxRecoveryArtifactBytes) ||
		cost.DataAllocatedBytes > int64(maxRecoveryArtifactBytes) {
		return fmt.Errorf("T41.10 measured phase %q resource cost exceeds its bound", name)
	}
	cacheReadLimit := uint64(population.AcceptedTarget + 1)
	if cost.SelectedStateRootReads > cacheReadLimit ||
		cost.SelectedStateMemberReads > cacheReadLimit ||
		cost.SelectedStateRootValidations > cacheReadLimit ||
		cost.SelectedStateMemberValidations > cacheReadLimit {
		return fmt.Errorf("T41.10 measured phase %q cache cost exceeds its bound", name)
	}
	var stateTransactionLimit uint64
	switch name {
	case "cold_publish_activate":
		stateTransactionLimit = 150
	case "one_service_delta", "percent_delta":
		stateTransactionLimit = 170
	case "removal_readd", "a_b_a":
		stateTransactionLimit = 340
	case "transition_profile":
		stateTransactionLimit = 680
	}
	if cost.StateChunkTransactions > stateTransactionLimit ||
		cost.StateRowsRead > cost.StateChunkTransactions*store.MaxServiceStateV3ChunkRows ||
		cost.StateRowsApplied > cost.StateRowsRead || cost.ChangedRows > cost.StateRowsApplied {
		return fmt.Errorf("T41.10 measured phase %q state cost is incoherent", name)
	}
	if cost.SearchContentReads > cost.SearchFilesOffered ||
		cost.ObservationObservedRecords > cost.ObservationRecords ||
		cost.BrowserProductReads > cost.ProductQueries ||
		cost.PreimageSummariesWritten > cost.PreimageRowsWritten ||
		cost.PreimageSummariesCollected > cost.PreimageRowsCollected ||
		cost.PreimageRowsCollected > cost.LifecycleRecordsDeleted ||
		cost.PreimageSummariesCollected >
			cost.LifecycleRecordsDeleted-cost.PreimageRowsCollected {
		return fmt.Errorf("T41.10 measured phase %q additive cost is incoherent", name)
	}
	if cost.SourceCensusMembers > uint64(population.RegularFiles) ||
		cost.RelationshipPublishes > 4 ||
		cost.RelationshipScheduleChunks != cost.RelationshipPublishes ||
		cost.RelationshipServiceRecords >
			cost.RelationshipPublishes*uint64(population.AcceptedTarget) ||
		cost.RelationshipServiceMembers > cost.RelationshipServiceRecords {
		return fmt.Errorf("T41.10 measured phase %q relationship cost exceeds its bound", name)
	}
	if cost.LifecycleRecordsDeleted > maxLifecycleRecords ||
		cost.LifecycleRetiredLogicalBytes > maxRetiredLogical ||
		cost.LifecycleDeletedRootBytes > maxDeletedRoots ||
		cost.LifecycleDeletedMemberBytes > maxDeletedMembers ||
		cost.LifecycleRecordsDeleted == 0 && (cost.LifecycleRetiredLogicalBytes != 0 ||
			cost.LifecycleDeletedRootBytes != 0 || cost.LifecycleDeletedMemberBytes != 0) {
		return fmt.Errorf("T41.10 measured phase %q lifecycle cost exceeds its bound", name)
	}
	if name == "collection" &&
		cost.LifecycleRecordsDeleted >
			cost.LifecycleOwnerTurns*lifecycle.MaxDeletesPerTick {
		return errors.New("T41.10 collection exceeded its owner-turn deletion bound")
	}

	residue := cost
	residue.WallMilliseconds, residue.PeakRSSBytes = 0, 0
	residue.DataLogicalBytes, residue.DataAllocatedBytes = 0, 0
	residue.SelectedStateRootReads, residue.SelectedStateMemberReads = 0, 0
	residue.SelectedStateRootValidations, residue.SelectedStateMemberValidations = 0, 0
	state := func() {
		residue.StateChunkTransactions, residue.StateRowsRead, residue.StateRowsApplied = 0, 0, 0
	}
	source := func() {
		residue.SearchFilesOffered, residue.SearchContentReads, residue.SearchDeclaredBytes = 0, 0, 0
		residue.SourceCensusRecords, residue.SourceCensusMembers = 0, 0
		residue.SourceCensusPlacements, residue.SourceCensusDeclaredBytes = 0, 0
		residue.ObservationInputMembers, residue.ObservationMembers = 0, 0
		residue.ObservationRecords, residue.ObservationObservedRecords = 0, 0
		residue.ObservationSourceBlobReads = 0
		residue.CandidateInputReadsUnavailable = false
		residue.CandidateResultMembers, residue.CandidateResultRecords = 0, 0
		residue.CandidateDeclaredBytes = 0
	}
	components := func() {
		residue.RelationshipComponentPublishes = 0
		residue.RelationshipComponentMembers, residue.RelationshipComponentRecords = 0, 0
	}
	relationship := func() {
		residue.RelationshipScheduleChunks, residue.RelationshipPublishes = 0, 0
		residue.RelationshipServiceMembers, residue.RelationshipServiceRecords = 0, 0
	}
	lifecycleCost := func() {
		residue.LifecycleRecordsDeleted, residue.LifecycleRetiredLogicalBytes = 0, 0
		residue.LifecycleDeletedRootBytes, residue.LifecycleDeletedMemberBytes = 0, 0
	}
	pressure := func() {
		residue.LifecycleOwnerTurns = 0
		residue.LifecyclePressureCollectObservations = 0
		residue.LifecyclePressureNormalObservations = 0
	}
	archive := func() {
		residue.ArchiveArtifactCount, residue.ArchiveArtifactBytes = 0, 0
		residue.RestoredArtifactCount, residue.RestoredArtifactBytes = 0, 0
	}
	delta := func() {
		residue.ChangedRows = 0
		residue.PreimageRowsWritten, residue.PreimageSummariesWritten = 0, 0
	}
	collected := func() {
		residue.PreimageRowsCollected, residue.PreimageSummariesCollected = 0, 0
	}
	product := func() { residue.ProductQueries = 0 }

	switch name {
	case "cold_publish_activate":
		state()
		source()
		components()
		relationship()
		residue.ChangedRows = 0
	case "warm_noop", "point_page_queries":
		product()
	case "one_service_delta":
		state()
		relationship()
		product()
		delta()
	case "percent_delta", "removal_readd", "a_b_a", "transition_profile":
		state()
		relationship()
		lifecycleCost()
		product()
		delta()
		collected()
	case "archive_restore":
		residue.LifecycleRecordsDeleted = 0
		archive()
		product()
	case "collection":
		lifecycleCost()
		pressure()
		product()
		collected()
	case "product_reader_queries":
		product()
		residue.BrowserProductReads = 0
	}
	if residue != (PhaseCost{}) {
		return fmt.Errorf("T41.10 measured phase %q contains work outside its closed shape", name)
	}
	return nil
}

func validImplementation(value Implementation) bool {
	return validDigest(value.AuthorExecutableSHA256) &&
		validDigest(value.PhebsExecutableSHA256) &&
		validDigest(value.ZoektExecutableSHA256) &&
		validDigest(value.GitExecutableSHA256) &&
		validDigest(value.SurrealExecutableSHA256) &&
		validDigest(value.GoExecutableSHA256) &&
		validDigest(value.NodeExecutableSHA256) &&
		validDigest(value.NPMExecutableSHA256) &&
		validDigest(value.BrowserExecutableSHA256)
}

func validateCheckEvidence() error {
	if len(checkEvidence) != len(checkNames) {
		return errors.New("T41.10 check evidence inventory is incomplete")
	}
	phases := make(map[string]bool, len(measuredPhaseNames))
	for _, name := range measuredPhaseNames {
		phases[name] = true
	}
	tests := make(map[string]bool)
	for _, gateTests := range composedGateTests {
		for _, identity := range gateTests {
			tests[identity] = true
		}
	}
	for _, name := range checkNames {
		binding, ok := checkEvidence[name]
		if !ok || len(binding.Phases)+len(binding.Tests) == 0 && !binding.ReceiptOracle {
			return fmt.Errorf("T41.10 check %q has no closed evidence", name)
		}
		for _, phase := range binding.Phases {
			if !phases[phase] {
				return fmt.Errorf("T41.10 check %q names unknown phase %q", name, phase)
			}
		}
		for _, identity := range binding.Tests {
			if !tests[identity] {
				return fmt.Errorf("T41.10 check %q names unknown test %q", name, identity)
			}
		}
	}
	return nil
}

func validateMeasuredPhaseOracles(receipt Receipt) error {
	phases := make(map[string]PhaseCost, len(receipt.MeasuredPhases))
	for _, phase := range receipt.MeasuredPhases {
		phases[phase.Name] = phase.Cost
	}
	cold := phases["cold_publish_activate"]
	wantFiles := uint64(receipt.Population.RegularFiles) * receipt.PhysicalWork.CorpusPasses
	wantBytes := uint64(receipt.Population.FixtureContentBytes) * receipt.PhysicalWork.CorpusPasses
	if cold.SearchFilesOffered != wantFiles || cold.SearchContentReads != wantFiles ||
		cold.SearchDeclaredBytes != wantBytes ||
		cold.SourceCensusRecords != uint64(receipt.Population.RegularFiles) ||
		cold.SourceCensusMembers == 0 ||
		cold.SourceCensusPlacements != uint64(receipt.Population.RegularFiles) ||
		cold.SourceCensusDeclaredBytes != uint64(receipt.Population.FixtureContentBytes) ||
		cold.ObservationInputMembers != targetRelationshipObservationMembers ||
		cold.ObservationMembers != targetRelationshipObservationMembers ||
		cold.ObservationRecords != targetRelationshipRecords ||
		cold.ObservationObservedRecords != 0 ||
		cold.ObservationSourceBlobReads != targetRelationshipRecords ||
		!cold.CandidateInputReadsUnavailable ||
		cold.CandidateResultMembers != targetRelationshipCandidateMembers ||
		cold.CandidateResultRecords != targetRelationshipRecords ||
		cold.CandidateDeclaredBytes != uint64(receipt.Population.FixtureContentBytes) ||
		cold.RelationshipScheduleChunks != 1 ||
		cold.RelationshipComponentPublishes != 3 ||
		cold.RelationshipComponentMembers != 0 || cold.RelationshipComponentRecords != 0 ||
		cold.RelationshipPublishes != 1 || cold.RelationshipServiceMembers == 0 ||
		cold.RelationshipServiceRecords != uint64(receipt.Population.AcceptedTarget) ||
		cold.ProductQueries != 0 ||
		cold.ChangedRows != uint64(receipt.Population.AcceptedTarget) ||
		cold.StateChunkTransactions == 0 ||
		cold.StateRowsApplied < uint64(receipt.Population.AcceptedTarget)*2 ||
		cold.PreimageRowsWritten != 0 || cold.PreimageSummariesWritten != 0 {
		return errors.New("T41.10 cold phase oracle is incomplete")
	}
	warm := phases["warm_noop"]
	if warm.ProductQueries != 1 || warm.StateRowsApplied != 0 || warm.ChangedRows != 0 ||
		warm.StateChunkTransactions != 0 ||
		warm.RelationshipScheduleChunks != 0 || warm.RelationshipPublishes != 0 ||
		warm.RelationshipServiceMembers != 0 || warm.RelationshipServiceRecords != 0 ||
		warm.PreimageRowsWritten != 0 || warm.PreimageSummariesWritten != 0 {
		return errors.New("T41.10 warm no-op oracle is incomplete")
	}
	points := phases["point_page_queries"]
	if points.ProductQueries != uint64(receipt.Population.AcceptedTarget+1) ||
		points.SelectedStateRootReads == 0 || points.SelectedStateMemberReads == 0 ||
		points.StateRowsApplied != 0 || points.RelationshipScheduleChunks != 0 ||
		points.RelationshipPublishes != 0 || points.RelationshipServiceMembers != 0 ||
		points.RelationshipServiceRecords != 0 {
		return errors.New("T41.10 point/page oracle is incomplete")
	}
	type deltaOracle struct {
		name      string
		changed   uint64
		rows      uint64
		summaries uint64
		collected uint64
		retired   uint64
		queries   uint64
		records   uint64
	}
	for _, want := range []deltaOracle{
		{name: "one_service_delta", changed: 1, rows: 1, summaries: 1, queries: 4, records: 10_000},
		{name: "percent_delta", changed: 100, rows: 100, summaries: 1, collected: 1, retired: 1, queries: 103, records: 10_000},
		{name: "removal_readd", changed: 2, rows: 2, summaries: 2, collected: 101, retired: 2, queries: 8, records: 19_999},
		{name: "a_b_a", changed: 2, rows: 2, summaries: 2, collected: 2, retired: 2, queries: 8, records: 20_000},
		{name: "transition_profile", changed: 10, rows: 10, summaries: 4, collected: 8, retired: 4, queries: 32, records: 39_991},
	} {
		cost := phases[want.name]
		if cost.ChangedRows != want.changed || cost.PreimageRowsWritten != want.rows ||
			cost.PreimageSummariesWritten != want.summaries ||
			cost.PreimageRowsCollected != want.collected ||
			cost.PreimageSummariesCollected != want.retired ||
			cost.RelationshipScheduleChunks != want.summaries ||
			cost.RelationshipPublishes != want.summaries ||
			cost.RelationshipServiceMembers == 0 ||
			cost.RelationshipServiceRecords != want.records ||
			cost.ProductQueries != want.queries || cost.StateChunkTransactions == 0 ||
			cost.StateRowsApplied < want.changed {
			return fmt.Errorf("T41.10 phase %q sparse oracle is incomplete", want.name)
		}
	}
	archive := phases["archive_restore"]
	if archive.ProductQueries != 2 || archive.SelectedStateRootReads == 0 ||
		archive.SelectedStateMemberReads == 0 || archive.StateRowsApplied != 0 ||
		archive.ArchiveArtifactCount != recoveryArtifactCount ||
		archive.RestoredArtifactCount != recoveryArtifactCount ||
		archive.ArchiveArtifactBytes < recoveryArtifactCount ||
		archive.ArchiveArtifactBytes > maxRecoveryArtifactBytes ||
		archive.RestoredArtifactBytes != archive.ArchiveArtifactBytes ||
		archive.LifecycleRecordsDeleted == 0 || archive.RelationshipScheduleChunks != 0 ||
		archive.RelationshipPublishes != 0 || archive.RelationshipServiceMembers != 0 ||
		archive.RelationshipServiceRecords != 0 ||
		archive.PreimageRowsWritten != 0 || archive.PreimageSummariesWritten != 0 {
		return errors.New("T41.10 archive/restore oracle is incomplete")
	}
	collection := phases["collection"]
	if collection.ProductQueries != 1 || collection.LifecycleRecordsDeleted == 0 ||
		collection.PreimageRowsCollected != 3 ||
		collection.PreimageSummariesCollected != 1 ||
		collection.LifecycleRetiredLogicalBytes == 0 ||
		collection.LifecycleDeletedRootBytes == 0 ||
		collection.LifecycleDeletedMemberBytes == 0 ||
		collection.LifecycleOwnerTurns == 0 ||
		collection.LifecycleOwnerTurns != collection.LifecyclePressureCollectObservations+
			collection.LifecyclePressureNormalObservations ||
		collection.LifecyclePressureCollectObservations != 1 ||
		collection.LifecyclePressureNormalObservations < 2 ||
		collection.RelationshipScheduleChunks != 0 || collection.RelationshipPublishes != 0 ||
		collection.RelationshipServiceMembers != 0 || collection.RelationshipServiceRecords != 0 ||
		collection.PreimageRowsWritten != 0 || collection.PreimageSummariesWritten != 0 {
		return errors.New("T41.10 collection oracle is incomplete")
	}
	product := phases["product_reader_queries"]
	if product.ProductQueries != 9 || product.BrowserProductReads != 3 ||
		product.SelectedStateRootReads == 0 ||
		product.SelectedStateMemberReads == 0 || product.StateRowsApplied != 0 ||
		product.RelationshipScheduleChunks != 0 || product.RelationshipPublishes != 0 ||
		product.RelationshipServiceMembers != 0 || product.RelationshipServiceRecords != 0 {
		return errors.New("T41.10 product-reader oracle is incomplete")
	}
	return nil
}

func validateQueryability(value QueryabilityOracle, population Population) error {
	if value.PublishedAcceptedServices != population.AcceptedTarget ||
		value.CurrentAcceptedServices != population.AcceptedTarget ||
		value.IndependentQueries != population.AcceptedTarget ||
		value.IndependentMatches != value.IndependentQueries ||
		value.MissingServices != 0 || value.UnexpectedServices != 0 {
		return errors.New("T41.10 independent queryability oracle is incomplete")
	}
	return nil
}

func validatePhysicalWork(value PhysicalWorkOracle) error {
	if value.CorpusPasses != 1 {
		return errors.New("T41.10 physical-work oracle is invalid")
	}
	return nil
}

// MarshalCanonical validates and emits the only accepted receipt encoding.
func MarshalCanonical(receipt Receipt) ([]byte, error) {
	if err := ValidateReceipt(receipt); err != nil {
		return nil, err
	}
	encoded, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return nil, err
	}
	encoded = append(encoded, '\n')
	if len(encoded) > MaxReceiptBytes {
		return nil, errors.New("T41.10 receipt exceeds 128 KiB")
	}
	if err := rejectForbiddenReceiptBytes(encoded); err != nil {
		return nil, err
	}
	return encoded, nil
}

// Decode rejects oversized, noncanonical, unknown-field, multi-value, and
// source-bearing JSON before returning a validated receipt.
func Decode(data []byte) (Receipt, error) {
	if len(data) == 0 || len(data) > MaxReceiptBytes {
		return Receipt{}, errors.New("T41.10 receipt size is invalid")
	}
	if err := rejectForbiddenReceiptBytes(data); err != nil {
		return Receipt{}, err
	}
	var receipt Receipt
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		return Receipt{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return Receipt{}, err
	}
	if err := ValidateReceipt(receipt); err != nil {
		return Receipt{}, err
	}
	canonical, err := MarshalCanonical(receipt)
	if err != nil {
		return Receipt{}, err
	}
	if !bytes.Equal(data, canonical) {
		return Receipt{}, errors.New("T41.10 receipt is not canonical")
	}
	return receipt, nil
}

// author creates destination atomically without replacing any existing path.
func author(destination string, receipt Receipt) (ArtifactIdentity, error) {
	encoded, err := MarshalCanonical(receipt)
	if err != nil {
		return ArtifactIdentity{}, err
	}
	directory := filepath.Dir(destination)
	base := filepath.Base(destination)
	if base == "." || base == string(filepath.Separator) || base == "" {
		return ArtifactIdentity{}, errors.New("T41.10 receipt destination is invalid")
	}
	temporary, err := os.CreateTemp(directory, "."+base+".tmp-")
	if err != nil {
		return ArtifactIdentity{}, err
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return ArtifactIdentity{}, err
	}
	if _, err := temporary.Write(encoded); err != nil {
		_ = temporary.Close()
		return ArtifactIdentity{}, err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return ArtifactIdentity{}, err
	}
	if err := temporary.Close(); err != nil {
		return ArtifactIdentity{}, err
	}
	if err := os.Link(temporaryName, destination); err != nil {
		if errors.Is(err, os.ErrExist) {
			return ArtifactIdentity{}, errors.New("T41.10 receipt destination already exists")
		}
		return ArtifactIdentity{}, err
	}
	if err := syncLinkedReceipt(
		directory,
		destination,
		func(path string) (receiptDirectory, error) { return os.Open(path) },
		func(path string) error { return removeLinkedReceipt(temporaryName, path) },
	); err != nil {
		return ArtifactIdentity{}, err
	}
	return ArtifactIdentity{Bytes: len(encoded), SHA256: digest(encoded)}, nil
}

type receiptDirectory interface {
	Sync() error
	Close() error
}

func syncLinkedReceipt(
	directory, destination string,
	openDirectory func(string) (receiptDirectory, error),
	removeDestination func(string) error,
) error {
	directoryHandle, err := openDirectory(directory)
	if err != nil {
		return errors.Join(
			fmt.Errorf("open receipt directory: %w", err),
			removeDestination(destination),
		)
	}
	if err := directoryHandle.Sync(); err != nil {
		return errors.Join(
			fmt.Errorf("sync receipt directory: %w", err),
			directoryHandle.Close(),
			removeDestination(destination),
		)
	}
	if err := directoryHandle.Close(); err != nil {
		return errors.Join(
			fmt.Errorf("close receipt directory: %w", err),
			removeDestination(destination),
		)
	}
	return nil
}

func removeLinkedReceipt(temporaryName, destination string) error {
	temporaryInfo, err := os.Lstat(temporaryName)
	if err != nil {
		return fmt.Errorf("inspect temporary receipt link: %w", err)
	}
	destinationInfo, err := os.Lstat(destination)
	if err != nil {
		return fmt.Errorf("inspect linked receipt destination: %w", err)
	}
	if !temporaryInfo.Mode().IsRegular() || !destinationInfo.Mode().IsRegular() ||
		!os.SameFile(temporaryInfo, destinationInfo) {
		return errors.New("linked receipt destination changed before cleanup")
	}
	if err := os.Remove(destination); err != nil {
		return fmt.Errorf("remove linked receipt destination: %w", err)
	}
	return nil
}

func frozenBindings() (InputBindings, Population) {
	target := t411.TargetProfileIdentity()
	return InputBindings{
			T411EnvelopeSHA256:      t411.RetainedEnvelopeSHA256,
			T411ReceiptSHA256:       t411.RetainedReceiptSHA256,
			TargetProfileSHA256:     target.SHA256,
			TransitionProfileSHA256: t411.RetainedTransitionProfileSHA256,
		}, Population{
			AcceptedFloor:       t411.AcceptedServiceFloor,
			AcceptedTarget:      t411.AcceptedServiceTarget,
			AcceptedServices:    target.AcceptedServices,
			TotalServiceRecords: target.TotalServiceRecords,
			Memberships:         target.Memberships,
			DistinctPaths:       target.DistinctPaths,
			RegularFiles:        target.RegularFiles,
			FixtureContentBytes: target.FixtureContentBytes,
		}
}

func phasesWithNames() []MeasuredPhase {
	result := make([]MeasuredPhase, len(measuredPhaseNames))
	for index, name := range measuredPhaseNames {
		result[index].Name = name
	}
	return result
}

func gatesWithNames() []ComposedGate {
	result := make([]ComposedGate, len(composedGateNames))
	for index, name := range composedGateNames {
		result[index] = ComposedGate{Name: name, Tests: slices.Clone(composedGateTests[name])}
	}
	return result
}

func checksWithNames() []Check {
	result := make([]Check, len(checkNames))
	for index, name := range checkNames {
		result[index].Name = name
	}
	return result
}

func validEnvironment(value Environment) bool {
	return safeLabel(value.GOOS, 32) && safeLabel(value.GOARCH, 32) &&
		safeLabel(value.GoVersion, 64) && value.LogicalCPUs > 0 && value.LogicalCPUs <= 4_096 &&
		value.GOMAXPROCS > 0 && value.GOMAXPROCS <= 4_096 &&
		value.RSSMethod == RSSMethodProcessTree && value.DiskMethod == DiskMethodWalk
}

func safeLabel(value string, maximum int) bool {
	if value == "" || len(value) > maximum {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune("._+-", character) {
			continue
		}
		return false
	}
	return true
}

func validDate(value string) bool {
	parsed, err := time.Parse("2006-01-02", value)
	return err == nil && parsed.Format("2006-01-02") == value
}

func validCommit(value string) bool {
	if len(value) != 40 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 20 && hex.EncodeToString(decoded) == value
}

func validDigest(value string) bool {
	const prefix = "sha256:"
	if len(value) != len(prefix)+sha256.Size*2 || !strings.HasPrefix(value, prefix) {
		return false
	}
	decoded, err := hex.DecodeString(value[len(prefix):])
	return err == nil && len(decoded) == sha256.Size && prefix+hex.EncodeToString(decoded) == value
}

func rejectForbiddenReceiptBytes(data []byte) error {
	lower := strings.ToLower(string(data))
	for _, fragment := range forbiddenReceiptFragments {
		if strings.Contains(lower, fragment) {
			return fmt.Errorf("T41.10 receipt contains forbidden source-bearing fragment %q", fragment)
		}
	}
	return nil
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
