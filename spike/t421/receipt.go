package t421

import (
	"bytes"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"slices"

	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/recovery"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
	"github.com/bmeddeb/phebs/internal/store"
)

type Bytes uint64
type CountMetric uint64
type Milliseconds uint64

type Receipt struct {
	Schema              string                 `json:"schema"`
	Authority           ReceiptAuthority       `json:"authority"`
	Decision            ReceiptDecision        `json:"decision"`
	Environment         ReceiptEnvironment     `json:"environment"`
	ExecutionFreeze     ReceiptExecutionFreeze `json:"execution_freeze"`
	Implementation      ReceiptImplementation  `json:"implementation"`
	Inputs              []InputBinding         `json:"inputs"`
	Measurements        []PhaseMeasurement     `json:"measurements"`
	NonClaims           ReceiptNonClaims       `json:"nonclaims"`
	PhaseResults        []PhaseResult          `json:"phase_results"`
	StateResults        []ExactPhaseEvidence   `json:"state_results"`
	TransitionResults   []TransitionResult     `json:"transition_results"`
	QueryResults        QueryEvidence          `json:"query_results"`
	RelationshipResults RelationshipEvidence   `json:"relationship_results"`
	RevisionResults     []RevisionResult       `json:"revision_results"`
	Seal                ReceiptSeal            `json:"seal"`
	Teardown            ReceiptTeardown        `json:"teardown"`
	SourceFree          bool                   `json:"source_free"`
}

type ReceiptAuthority struct {
	PlanSchema               string                    `json:"plan_schema"`
	PlanSHA256               string                    `json:"plan_sha256"`
	SourceCommit             string                    `json:"source_commit"`
	ProfileSHA256            string                    `json:"profile_sha256"`
	OracleSHA256             string                    `json:"oracle_sha256"`
	RevisionHistorySHA256    string                    `json:"revision_history_sha256"`
	MeterPolicySHA256        string                    `json:"meter_policy_sha256"`
	SourceVerificationSHA256 string                    `json:"source_verification_sha256"`
	ExtractionRootSnapshots  []ExtractionRootSnapshot  `json:"extraction_root_snapshots"`
	Snapshots                []AuthoritySnapshot       `json:"snapshots"`
	Results                  []AuthorityPhaseReference `json:"results"`
}

type AuthorityPhaseReference struct {
	Phase          string `json:"phase"`
	Outcome        string `json:"outcome"`
	SnapshotSHA256 string `json:"snapshot_sha256,omitempty"`
}

type ReceiptDecision struct {
	Outcome        string `json:"outcome"`
	Selected       string `json:"selected"`
	RulePriority   uint64 `json:"rule_priority"`
	Reason         string `json:"reason"`
	Substantiated  bool   `json:"substantiated"`
	Gate2V2        string `json:"gate2_v2"`
	ReleasePosture string `json:"release_posture"`
}

type ReceiptEnvironment struct {
	Fields ExecutionHost `json:"fields"`
	SHA256 string        `json:"sha256"`
}

type ReceiptExecutionFreeze struct {
	Schema                string           `json:"schema"`
	SHA256                string           `json:"sha256"`
	SignerFingerprint     string           `json:"signer_fingerprint"`
	AdmissionEventSHA256  string           `json:"admission_event_sha256"`
	AdmissionEventOrdinal uint64           `json:"admission_event_ordinal"`
	Commits               ExecutionCommits `json:"commits"`
}

// ExecutionFreezeBinding is created only after the exact freeze is validated
// against externally selected commits. Its fields stay private so receipt
// validation cannot accidentally self-authorize the commits carried by a
// freeze.
type ExecutionFreezeBinding struct {
	freeze                    ExecutionFreeze
	expectedCommits           ExecutionCommits
	expectedSignerFingerprint string
	planSHA256                string
	freezeSHA256              string
	admissionEventSHA256      string
	admissionEventOrdinal     uint64
}

type ExecutionFreezeAdmissionBinding struct {
	schema                string
	freezeSHA256          string
	signatureNamespace    string
	signerFingerprint     string
	admissionEventSHA256  string
	admissionEventOrdinal uint64
	signatureVerified     bool
	verifiedBeforeWork    bool
}

// ReturnedPackageBinding is yielded only by the T42.2 outer package verifier.
// Private fields prevent results.json from authorizing its own archive or
// signature.
type ReturnedPackageBinding struct {
	signerFingerprint          string
	receiptSHA256              string
	packageSHA256              string
	inventorySHA256            string
	exactInventory             []string
	returnedSignatureVerified  bool
	sourceSignatureVerified    bool
	returnedSignatureNamespace string
	sourceSignatureNamespace   string
	sourceVerificationSHA256   string
	sourceVerificationSchema   string
	sourcePlanSHA256           string
	sourceFreezeSHA256         string
	sourceExactInventorySHA256 string
	revisionResultsSHA256      string
	sourceVerified             bool
}

type ReceiptImplementation struct {
	IntegratedMainCommit string                  `json:"integrated_main_commit"`
	T422SourceCommit     string                  `json:"t422_source_commit"`
	CleanTree            bool                    `json:"clean_tree"`
	DigestAlgorithm      string                  `json:"digest_algorithm"`
	Tools                []ExecutionToolIdentity `json:"tools"`
}

type PhaseResult struct {
	Name    string          `json:"name"`
	Outcome string          `json:"outcome"`
	Failure *ReceiptFailure `json:"failure,omitempty"`
}

type ReceiptFailure struct {
	Phase       string                     `json:"phase"`
	Class       string                     `json:"class"`
	Code        string                     `json:"code"`
	Observation FailureObservation         `json:"observation"`
	Evidence    *FailureEvidenceProjection `json:"evidence,omitempty"`
}

type FailureEvidenceProjection struct {
	Schema    string                    `json:"schema"`
	Kind      string                    `json:"kind"`
	Lifecycle *LifecycleFailureEvidence `json:"lifecycle,omitempty"`
	Internal  *InternalFailureEvidence  `json:"internal,omitempty"`
}

type LifecycleFailureEvidence struct {
	Phase                string               `json:"phase"`
	Owner                LifecycleOwnerResult `json:"owner"`
	CapacityCompleteness string               `json:"capacity_completeness"`
	CapacityPressure     string               `json:"capacity_pressure"`
	ErrorClass           string               `json:"error_class"`
	EventOrdinal         uint64               `json:"event_ordinal"`
}

type InternalFailureEvidence struct {
	Phase        string `json:"phase"`
	Stage        string `json:"stage"`
	ErrorClass   string `json:"error_class"`
	EventOrdinal uint64 `json:"event_ordinal"`
}

type FailureObservation struct {
	Schema             string   `json:"schema"`
	Kind               string   `json:"kind"`
	Metric             string   `json:"metric,omitempty"`
	UnavailableMetrics []string `json:"unavailable_metrics,omitempty"`
	Expected           uint64   `json:"expected,omitempty"`
	Observed           uint64   `json:"observed,omitempty"`
	Limit              uint64   `json:"limit,omitempty"`
	ExpectedSHA256     string   `json:"expected_sha256,omitempty"`
	ObservedSHA256     string   `json:"observed_sha256,omitempty"`
	EvidenceSHA256     string   `json:"evidence_sha256"`
}

type PhaseMeasurement struct {
	Phase              string                         `json:"phase"`
	StartEventOrdinal  uint64                         `json:"start_event_ordinal"`
	FinishEventOrdinal uint64                         `json:"finish_event_ordinal"`
	Metrics            ReceiptMetrics                 `json:"metrics"`
	ChildProcessRoles  []Count                        `json:"child_process_roles"`
	DispatchAccounting *DispatchAccountingMeasurement `json:"dispatch_accounting,omitempty"`
	NativeObservation  *ProcessObservation            `json:"native_observation,omitempty"`
}

type ReceiptMetrics struct {
	ApplicablePartitions             CountMetric  `json:"applicable_partitions"`
	AllocationMeasurementAvailable   bool         `json:"allocation_measurement_available"`
	AvailableDiskBytes               Bytes        `json:"available_disk_bytes"`
	BallastCeilingBytes              Bytes        `json:"ballast_ceiling_bytes"`
	CacheHits                        CountMetric  `json:"cache_hits"`
	CacheLookups                     CountMetric  `json:"cache_lookups"`
	CacheMemberReads                 CountMetric  `json:"cache_member_reads"`
	CacheMemberValidations           CountMetric  `json:"cache_member_validations"`
	CacheMisses                      CountMetric  `json:"cache_misses"`
	CacheRootReads                   CountMetric  `json:"cache_root_reads"`
	CacheRootValidations             CountMetric  `json:"cache_root_validations"`
	ChangedLogicalServices           CountMetric  `json:"changed_logical_services"`
	ChangedPhysicalFiles             CountMetric  `json:"changed_physical_files"`
	ChildProcesses                   CountMetric  `json:"child_processes"`
	ControlReads                     CountMetric  `json:"control_reads"`
	DataAllocatedBytes               Bytes        `json:"data_allocated_bytes"`
	DataLogicalBytes                 Bytes        `json:"data_logical_bytes"`
	DirectRecoveryLimits             CountMetric  `json:"direct_recovery_topology_limits"`
	GitReads                         CountMetric  `json:"git_reads"`
	CensusChildren                   CountMetric  `json:"census_children,omitempty"`
	CensusRecords                    CountMetric  `json:"census_records,omitempty"`
	IndexFiles                       CountMetric  `json:"index_files"`
	JobAttempts                      CountMetric  `json:"job_attempts"`
	LogicalMemberships               CountMetric  `json:"logical_memberships"`
	LifecycleDeleted                 CountMetric  `json:"lifecycle_deleted"`
	LifecycleOwnerTurns              CountMetric  `json:"lifecycle_owner_turns"`
	MaterializedOwnerPairs           CountMetric  `json:"materialized_cartesian_owner_pairs"`
	MaxLifecycleDeletesTurn          CountMetric  `json:"max_lifecycle_deletes_in_any_turn"`
	MaxRetriesUnit                   CountMetric  `json:"max_retries_on_any_unit"`
	MaxRowsTransaction               CountMetric  `json:"max_rows_in_any_transaction"`
	MemberReads                      CountMetric  `json:"member_reads"`
	ObservationParses                CountMetric  `json:"observation_parses"`
	PeakRSSBytes                     Bytes        `json:"peak_rss_bytes"`
	ProcessMeasurementAvailable      bool         `json:"process_measurement_available"`
	PhysicalCorpusPasses             CountMetric  `json:"physical_corpus_passes"`
	CombinedPhysicalOwners           CountMetric  `json:"combined_physical_owners"`
	PublicationWrites                CountMetric  `json:"publication_writes"`
	PublishedDomains                 CountMetric  `json:"published_domains"`
	PressureCustodyMarginBytes       Bytes        `json:"pressure_custody_margin_bytes"`
	PressureVolumeBytes              Bytes        `json:"pressure_volume_bytes"`
	RelationshipProjections          CountMetric  `json:"relationship_projections"`
	MinimumPrePressureUsedBytes      Bytes        `json:"minimum_pre_pressure_used_bytes"`
	MaximumPrePressureUsedBytes      Bytes        `json:"maximum_pre_pressure_used_bytes"`
	MinimumPrePressureAllocatedBytes Bytes        `json:"minimum_pre_pressure_allocated_bytes"`
	MaximumPrePressureAllocatedBytes Bytes        `json:"maximum_pre_pressure_allocated_bytes"`
	ResolverBlobBytes                Bytes        `json:"resolver_blob_bytes"`
	ResolverBlobReads                CountMetric  `json:"resolver_blob_reads"`
	RelationshipBuildAttempts        CountMetric  `json:"relationship_build_attempts"`
	Retries                          CountMetric  `json:"retries"`
	ReuseDecisions                   CountMetric  `json:"reuse_decisions"`
	SourceReuseDecisions             CountMetric  `json:"source_reuse_decisions"`
	SearchReuseDecisions             CountMetric  `json:"search_reuse_decisions"`
	ObservationReuseDecisions        CountMetric  `json:"observation_reuse_decisions"`
	CatalogReuseDecisions            CountMetric  `json:"catalog_reuse_decisions"`
	RelationshipReuseDecisions       CountMetric  `json:"relationship_reuse_decisions"`
	ServiceReferences                CountMetric  `json:"service_references"`
	ServiceRows                      CountMetric  `json:"service_rows"`
	SourceLogicalBytes               Bytes        `json:"source_logical_bytes"`
	SourceUniqueBytes                Bytes        `json:"source_unique_bytes"`
	StoreRows                        CountMetric  `json:"store_rows"`
	StoreTransactions                CountMetric  `json:"store_transactions"`
	TotalDiskBytes                   Bytes        `json:"total_disk_bytes"`
	UnsupportedSourceFiles           CountMetric  `json:"unsupported_source_files"`
	WallMS                           Milliseconds `json:"wall_ms"`
	ControlledDispatchAttempts       CountMetric  `json:"controlled_dispatch_attempts,omitempty"`
	DispatchMeasurementAvailable     bool         `json:"dispatch_measurement_available,omitempty"`
	ObservedRSSHighWaterBytes        Bytes        `json:"observed_rss_high_water_bytes,omitempty"`
	NativeMeasurementAvailable       bool         `json:"native_measurement_available,omitempty"`
}

type AuthorityPhaseResult struct {
	Phase   string `json:"phase"`
	Outcome string `json:"outcome"`
	AuthorityState
	ExtractionRoots []ExtractionRootResult `json:"-"`
}

type AuthoritySnapshot struct {
	SHA256 string `json:"sha256"`
	AuthorityState
}

type AuthorityState struct {
	PhysicalRevision                string      `json:"physical_revision"`
	LogicalRevision                 string      `json:"logical_revision"`
	PhysicalCommit                  string      `json:"physical_commit,omitempty"`
	PhysicalTree                    string      `json:"physical_tree,omitempty"`
	SourceGenerationSHA256          string      `json:"source_generation_sha256,omitempty"`
	SearchGenerationSHA256          string      `json:"search_generation_sha256,omitempty"`
	ObservationGenerationSHA256     string      `json:"observation_generation_sha256,omitempty"`
	CandidateGenerationSHA256       string      `json:"candidate_generation_sha256,omitempty"`
	CatalogRootSHA256               string      `json:"catalog_root_sha256,omitempty"`
	CatalogActivationPlanSHA256     string      `json:"catalog_activation_plan_sha256,omitempty"`
	CatalogActivationScheduleSHA256 string      `json:"catalog_activation_schedule_sha256,omitempty"`
	CatalogActivationUnitSHA256     string      `json:"catalog_activation_unit_sha256,omitempty"`
	ResolverCatalogGenerationSHA256 string      `json:"resolver_catalog_generation_sha256,omitempty"`
	ResolverCatalogRootSHA256       string      `json:"resolver_catalog_root_sha256,omitempty"`
	CallerGenerationSHA256          string      `json:"caller_generation_sha256,omitempty"`
	CallerRootSHA256                string      `json:"caller_root_sha256,omitempty"`
	RelationshipGenerationSHA256    string      `json:"relationship_generation_sha256,omitempty"`
	RelationshipRootSHA256          string      `json:"relationship_root_sha256,omitempty"`
	RelationshipProvenanceSHA256    string      `json:"relationship_provenance_sha256,omitempty"`
	SearchInventory                 SetIdentity `json:"search_inventory"`
	ObservationInputInventory       SetIdentity `json:"observation_input_inventory"`
	ExtractionRootsSHA256           string      `json:"extraction_roots_sha256,omitempty"`
	Current                         bool        `json:"current"`
}

type ExtractionRootSnapshot struct {
	SHA256 string                 `json:"sha256"`
	Roots  []ExtractionRootResult `json:"roots"`
}

func authoritySnapshotSHA256(value AuthorityState) (string, error) {
	return receiptSHA256(value)
}

func resolveAuthorityResults(
	rootSnapshots []ExtractionRootSnapshot,
	snapshots []AuthoritySnapshot,
	references []AuthorityPhaseReference,
	phases []string,
	outcomes map[string]string,
) ([]AuthorityPhaseResult, error) {
	if len(references) != len(phases) || len(snapshots) > len(phases) {
		return nil, errors.New("authority reference inventory is incomplete")
	}
	rootsByDigest := make(map[string][]ExtractionRootResult, len(rootSnapshots))
	for index, snapshot := range rootSnapshots {
		digest, err := receiptSHA256(snapshot.Roots)
		if err != nil || len(snapshot.Roots) == 0 || snapshot.SHA256 != digest || !validDigest(snapshot.SHA256) ||
			index > 0 && rootSnapshots[index-1].SHA256 >= snapshot.SHA256 {
			return nil, errors.New("extraction-root snapshot inventory is not canonical")
		}
		rootsByDigest[snapshot.SHA256] = snapshot.Roots
	}
	byDigest := make(map[string]AuthorityState, len(snapshots))
	for index, snapshot := range snapshots {
		digest, err := authoritySnapshotSHA256(snapshot.AuthorityState)
		if err != nil || snapshot.SHA256 != digest || !validDigest(snapshot.SHA256) ||
			index > 0 && snapshots[index-1].SHA256 >= snapshot.SHA256 {
			return nil, errors.New("authority snapshot inventory is not canonical")
		}
		byDigest[snapshot.SHA256] = snapshot.AuthorityState
	}
	used := make(map[string]struct{}, len(snapshots))
	usedRoots := make(map[string]struct{}, len(rootSnapshots))
	result := make([]AuthorityPhaseResult, len(phases))
	for index, phase := range phases {
		reference := references[index]
		if reference.Phase != phase || reference.Outcome != outcomes[phase] {
			return nil, fmt.Errorf("phase %q authority reference is invalid", phase)
		}
		if reference.Outcome == "not_run" {
			if reference.SnapshotSHA256 != "" {
				return nil, fmt.Errorf("phase %q not-run authority references a snapshot", phase)
			}
			result[index] = AuthorityPhaseResult{Phase: phase, Outcome: reference.Outcome}
			continue
		}
		state, ok := byDigest[reference.SnapshotSHA256]
		if !ok {
			return nil, fmt.Errorf("phase %q authority snapshot is absent", phase)
		}
		used[reference.SnapshotSHA256] = struct{}{}
		roots, rootsOK := rootsByDigest[state.ExtractionRootsSHA256]
		if state.ExtractionRootsSHA256 != "" && !rootsOK {
			return nil, fmt.Errorf("phase %q extraction-root snapshot is absent", phase)
		}
		if rootsOK {
			usedRoots[state.ExtractionRootsSHA256] = struct{}{}
		}
		result[index] = AuthorityPhaseResult{
			Phase: phase, Outcome: reference.Outcome, AuthorityState: state, ExtractionRoots: roots,
		}
	}
	if len(used) != len(snapshots) {
		return nil, errors.New("authority snapshot inventory contains an unreferenced value")
	}
	if len(usedRoots) != len(rootSnapshots) {
		return nil, errors.New("extraction-root snapshot inventory contains an unreferenced value")
	}
	return result, nil
}

type ExtractionRootResult struct {
	Domain                      string                      `json:"domain"`
	Current                     bool                        `json:"current"`
	GenerationSHA256            string                      `json:"generation_sha256"`
	RootSHA256                  string                      `json:"root_sha256"`
	CandidateGenerationSHA256   string                      `json:"candidate_generation_sha256"`
	SourceGenerationSHA256      string                      `json:"source_generation_sha256"`
	ObservationGenerationSHA256 string                      `json:"observation_generation_sha256"`
	PlanSHA256                  string                      `json:"plan_sha256"`
	ScheduleSHA256              string                      `json:"schedule_sha256"`
	ApplicablePartitions        uint64                      `json:"applicable_partitions"`
	MemberPartitions            uint64                      `json:"member_partitions"`
	TypedPartitions             uint64                      `json:"typed_partitions"`
	TypedScopeRecords           uint64                      `json:"typed_scope_records"`
	TypedScopePathBytes         uint64                      `json:"typed_scope_path_bytes"`
	TypedScopeEncodedBytes      uint64                      `json:"typed_scope_encoded_bytes"`
	TypedScopeSHA256            string                      `json:"typed_scope_sha256,omitempty"`
	TypedScopeContentSHA256     string                      `json:"typed_scope_descriptor_content_sha256,omitempty"`
	Candidates                  SetIdentity                 `json:"candidates"`
	Members                     SetIdentity                 `json:"members"`
	Reserved                    ResultTotals                `json:"reserved"`
	Totals                      ResultTotals                `json:"totals"`
	PartitionResultsSHA256      string                      `json:"partition_results_sha256"`
	PartitionResults            []ExtractionPartitionResult `json:"partition_results"`
}

type ExtractionPartitionResult struct {
	Ordinal              uint64       `json:"ordinal"`
	Kind                 string       `json:"kind"`
	MemberOrdinal        int64        `json:"member_ordinal"`
	CallerPrefix         string       `json:"caller_prefix,omitempty"`
	SourceStart          uint64       `json:"source_start"`
	SourceEnd            uint64       `json:"source_end"`
	MemberRecordStart    uint64       `json:"member_record_start"`
	MemberRecordEnd      uint64       `json:"member_record_end"`
	AdmittedRecords      uint64       `json:"admitted_records"`
	Reservation          ResultTotals `json:"reservation"`
	Disposition          string       `json:"disposition"`
	Totals               ResultTotals `json:"totals"`
	PartitionSHA256      string       `json:"partition_sha256"`
	ExpectationSHA256    string       `json:"expectation_sha256"`
	ResultDigestSHA256   string       `json:"result_digest_sha256"`
	ResultIdentitySHA256 string       `json:"result_identity_sha256"`
}

type ExactPhaseEvidence struct {
	Phase                    string               `json:"phase"`
	Outcome                  string               `json:"outcome"`
	ExpectedSHA256           string               `json:"expected_sha256"`
	ObservedProjectionSHA256 string               `json:"observed_projection_sha256,omitempty"`
	ObservedSHA256           string               `json:"observed_sha256,omitempty"`
	Observed                 *ObservedPhaseState  `json:"observed,omitempty"`
	RuntimeSHA256            string               `json:"runtime_sha256,omitempty"`
	Runtime                  *PhaseRuntimeBinding `json:"runtime,omitempty"`
}

type QueryEvidence struct {
	Phase   string        `json:"phase"`
	Outcome string        `json:"outcome"`
	Results []QueryResult `json:"results"`
}

type RelationshipEvidence struct {
	Phase                       string                     `json:"phase"`
	Outcome                     string                     `json:"outcome"`
	AuthorityBeforeSHA256       string                     `json:"authority_before_sha256,omitempty"`
	AuthorityAfterSHA256        string                     `json:"authority_after_sha256,omitempty"`
	RelationshipRootReads       uint64                     `json:"relationship_root_reads"`
	RelationshipGenerationReads uint64                     `json:"relationship_generation_reads"`
	Results                     []RelationshipResult       `json:"results"`
	Caller                      *CallerPublicationResult   `json:"caller,omitempty"`
	Product                     *ProductRelationshipResult `json:"product,omitempty"`
}

type CallerPublicationResult struct {
	Schema                          string                        `json:"schema"`
	ExecutionPolicy                 string                        `json:"execution_policy"`
	CandidateInventory              SetIdentity                   `json:"candidate_inventory"`
	ResolverCatalogGenerationSHA256 string                        `json:"resolver_catalog_generation_sha256"`
	ResolverCatalogRootSHA256       string                        `json:"resolver_catalog_root_sha256"`
	ResolverDeclarationRecords      uint64                        `json:"resolver_declaration_records"`
	GeneratedDescriptors            uint64                        `json:"generated_descriptors"`
	GenerationSHA256                string                        `json:"generation_sha256"`
	RootSHA256                      string                        `json:"root_sha256"`
	ManifestSHA256                  string                        `json:"manifest_sha256"`
	Current                         bool                          `json:"current"`
	LeavesSHA256                    string                        `json:"leaves_sha256"`
	Leaves                          []CallerPublicationLeafResult `json:"leaves"`
	ResolvedPostings                uint64                        `json:"resolved_postings"`
	Abstentions                     uint64                        `json:"abstentions"`
	Records                         uint64                        `json:"records"`
	CanonicalBytes                  uint64                        `json:"canonical_bytes"`
	EncodedBytes                    uint64                        `json:"encoded_bytes"`
	UnresolvedPostings              uint64                        `json:"unresolved_postings"`
	Projection                      SetIdentity                   `json:"projection"`
	RelationshipGenerationSHA256    string                        `json:"relationship_generation_sha256"`
	RelationshipRootSHA256          string                        `json:"relationship_root_sha256"`
	ComponentBindingSHA256          string                        `json:"component_binding_sha256"`
}

type CallerPublicationLeafResult struct {
	Prefix           string `json:"prefix"`
	CandidateRecords uint64 `json:"candidate_records"`
	Outcome          string `json:"outcome"`
	ResolvedPostings uint64 `json:"resolved_postings"`
	Abstentions      uint64 `json:"abstentions"`
	Records          uint64 `json:"records"`
	Unresolved       uint64 `json:"unresolved"`
	CanonicalBytes   uint64 `json:"canonical_bytes"`
	EncodedBytes     uint64 `json:"encoded_bytes"`
	ResultSHA256     string `json:"result_sha256"`
}

type QueryResult struct {
	Name string               `json:"name"`
	HTTP QueryTransportResult `json:"http"`
	MCP  QueryTransportResult `json:"mcp"`
}

type QueryTransportResult struct {
	Schema                  string `json:"schema"`
	Code                    string `json:"code"`
	Pages                   uint64 `json:"pages"`
	Records                 uint64 `json:"records"`
	Paths                   uint64 `json:"paths"`
	ControlReads            uint64 `json:"control_reads"`
	MemberReads             uint64 `json:"member_reads"`
	ProjectionSHA256        string `json:"projection_sha256"`
	PaginationClosedExactly bool   `json:"pagination_closed_exactly"`
	AuthorizationDecisions  uint64 `json:"authorization_decisions"`
	AuthorizationDecision   string `json:"authorization_decision"`
	AuthorizedRepositories  uint64 `json:"authorized_repositories"`
	AuthoritySnapshots      uint64 `json:"authority_snapshots"`
	AuthorityBeforeSHA256   string `json:"authority_before_sha256"`
	AuthorityAfterSHA256    string `json:"authority_after_sha256"`
}

type RelationshipResult struct {
	Name                     string `json:"name"`
	SemanticPairEdges        uint64 `json:"semantic_pair_edges"`
	MaxInDegree              uint64 `json:"max_in_degree"`
	MaxOutDegree             uint64 `json:"max_out_degree"`
	Acyclic                  bool   `json:"acyclic"`
	ObservedEdgesFramedBytes uint64 `json:"observed_edges_framed_bytes"`
	ObservedEdgesSHA256      string `json:"observed_edges_sha256"`
}

type ProductRelationshipResult struct {
	RPCProjections           uint64 `json:"rpc_projections"`
	KafkaProducerProjections uint64 `json:"kafka_producer_projections"`
	KafkaConsumerProjections uint64 `json:"kafka_consumer_projections"`
	KafkaPairRows            uint64 `json:"kafka_pair_rows"`
	TotalProjections         uint64 `json:"total_projections"`
	ServiceReferences        uint64 `json:"service_references"`
	Canonicalization         string `json:"canonicalization"`
	ProjectionRecords        uint64 `json:"projection_records"`
	ProjectionFramedBytes    uint64 `json:"projection_framed_bytes"`
	ProjectionSHA256         string `json:"projection_sha256"`
}

type RevisionResult struct {
	Name                       string                 `json:"name"`
	PhysicalPhase              string                 `json:"physical_phase"`
	PhysicalOutcome            string                 `json:"physical_outcome"`
	LogicalPhase               string                 `json:"logical_phase"`
	LogicalOutcome             string                 `json:"logical_outcome"`
	PhysicalTreeRecipeSHA256   string                 `json:"physical_tree_recipe_sha256"`
	PhysicalCommitRecipeSHA256 string                 `json:"physical_commit_recipe_sha256"`
	PhysicalCommit             string                 `json:"physical_commit,omitempty"`
	PhysicalTree               string                 `json:"physical_tree,omitempty"`
	PhysicalParentCommit       string                 `json:"physical_parent_commit,omitempty"`
	CatalogLogicalSHA256       string                 `json:"catalog_logical_sha256"`
	SemanticSHA256             string                 `json:"semantic_sha256"`
	CatalogSource              CatalogSourceProfile   `json:"catalog_source"`
	AuthoredManifest           AuthoredSourceManifest `json:"authored_manifest"`
}

type AuthoredSourceManifest struct {
	Schema                  string      `json:"schema"`
	BaseCommit              string      `json:"base_commit"`
	BaseTree                string      `json:"base_tree"`
	Overlay                 SetIdentity `json:"overlay"`
	GeneratedMappingRecords uint64      `json:"generated_mapping_records"`
	GeneratedMappingPath    string      `json:"generated_mapping_path"`
	GeneratedMappingMode    string      `json:"generated_mapping_mode"`
	GeneratedMappingBytes   uint64      `json:"generated_mapping_bytes"`
	GeneratedMappingSHA256  string      `json:"generated_mapping_sha256"`
	TypedInputRecords       uint64      `json:"typed_input_records"`
	TypedInputKind          string      `json:"typed_input_kind"`
	TypedInputPath          string      `json:"typed_input_path"`
	TypedInputMode          string      `json:"typed_input_mode"`
	TypedInputBytes         uint64      `json:"typed_input_bytes"`
	TypedInputSHA256        string      `json:"typed_input_sha256"`
	TypedInputBlobOID       string      `json:"typed_input_blob_oid"`
	AddedRegularFiles       uint64      `json:"added_regular_files"`
	RegularFiles            uint64      `json:"regular_files"`
	TreeInventory           SetIdentity `json:"tree_inventory"`
	TreeObjectID            string      `json:"tree_object_id"`
	CommitBytesSHA256       string      `json:"commit_bytes_sha256"`
}

type TransitionResult struct {
	Phase              string                       `json:"phase"`
	Outcome            string                       `json:"outcome"`
	StartEventOrdinal  uint64                       `json:"start_event_ordinal,omitempty"`
	FinishEventOrdinal uint64                       `json:"finish_event_ordinal,omitempty"`
	FailureProjection  *TransitionFailureProjection `json:"failure_projection,omitempty"`
	Injections         []InjectionTransition        `json:"injections,omitempty"`
	Pressure           *PressureTransition          `json:"pressure,omitempty"`
	Archive            *ArchiveTransition           `json:"archive,omitempty"`
	Reader             *ReaderTransition            `json:"reader,omitempty"`
	Lifecycle          *LifecycleTransition         `json:"lifecycle,omitempty"`
	ReadAccounting     *TransitionReadSubtotal      `json:"transition_read_accounting,omitempty"`
}

type TransitionReadSubtotal struct {
	Schema             string `json:"schema"`
	Class              string `json:"class"`
	ReportCalls        uint64 `json:"report_calls"`
	ControlFileReads   uint64 `json:"control_file_reads"`
	StoreReadAttempts  uint64 `json:"store_read_attempts"`
	MemberReads        uint64 `json:"member_reads"`
	StoreWriteAttempts uint64 `json:"store_write_attempts"`
}

type TransitionFailureProjection struct {
	Schema            string               `json:"schema"`
	Phase             string               `json:"phase"`
	FailurePoint      string               `json:"failure_point,omitempty"`
	Boundary          string               `json:"boundary"`
	LastCompletedStep string               `json:"last_completed_step"`
	EventOrdinal      uint64               `json:"event_ordinal"`
	Authority         AuthorityPhaseResult `json:"authority"`
}

type InjectionTransition struct {
	Schema                      string                     `json:"schema"`
	FailurePoint                string                     `json:"failure_point"`
	ArmCount                    uint64                     `json:"arm_count"`
	HitCount                    uint64                     `json:"hit_count"`
	RecoveryCount               uint64                     `json:"recovery_count"`
	ResidueBefore               uint64                     `json:"residue_before"`
	ResidueAtHit                uint64                     `json:"residue_at_hit"`
	ResidueAfter                uint64                     `json:"residue_after"`
	TargetSHA256                string                     `json:"target_sha256"`
	TargetIdentitySHA256        string                     `json:"target_identity_sha256"`
	StableTargetSHA256          string                     `json:"stable_target_sha256"`
	Target                      InjectionTargetProjection  `json:"target"`
	HitReportSHA256             string                     `json:"hit_report_sha256"`
	RecoveryProjectionSHA256    string                     `json:"recovery_projection_sha256"`
	ArmEventOrdinal             uint64                     `json:"arm_event_ordinal"`
	HitEventOrdinal             uint64                     `json:"hit_event_ordinal"`
	RecoveryEventOrdinal        uint64                     `json:"recovery_event_ordinal"`
	ClearEventOrdinal           uint64                     `json:"clear_event_ordinal"`
	TargetGenerationBefore      string                     `json:"target_generation_before,omitempty"`
	TargetGenerationAfter       string                     `json:"target_generation_after,omitempty"`
	RequeueCount                uint64                     `json:"requeue_count"`
	SuccessCount                uint64                     `json:"success_count"`
	AuthorityBeforeSHA256       string                     `json:"authority_before_sha256"`
	AuthorityAtHitSHA256        string                     `json:"authority_at_hit_sha256"`
	AuthorityAfterSHA256        string                     `json:"authority_after_sha256"`
	ObservedRecoveryBranch      string                     `json:"observed_recovery_branch"`
	RecoveredCandidates         uint64                     `json:"recovered_candidates"`
	CollectedCandidates         uint64                     `json:"collected_candidates"`
	ProcessEpochBefore          uint64                     `json:"process_epoch_before,omitempty"`
	ProcessEpochAfter           uint64                     `json:"process_epoch_after,omitempty"`
	ProcessIdentityBeforeSHA256 string                     `json:"process_identity_before_sha256,omitempty"`
	ProcessIdentityAfterSHA256  string                     `json:"process_identity_after_sha256,omitempty"`
	ProcessImageSHA256          string                     `json:"process_image_sha256,omitempty"`
	ProcessStopEventOrdinal     uint64                     `json:"process_stop_event_ordinal,omitempty"`
	ProcessStartEventOrdinal    uint64                     `json:"process_start_event_ordinal,omitempty"`
	ElapsedMS                   uint64                     `json:"elapsed_ms"`
	DeadlineMS                  uint64                     `json:"deadline_ms"`
	Checkpoint                  *CheckpointRecovery        `json:"checkpoint,omitempty"`
	Preparation                 *RecoveryPreparationResult `json:"preparation,omitempty"`
}

type InjectionTargetProjection struct {
	Schema            string `json:"schema"`
	Phase             string `json:"phase"`
	Domain            string `json:"domain"`
	Kind              string `json:"kind"`
	Ordinal           uint64 `json:"ordinal"`
	ServiceOrdinal    uint64 `json:"service_ordinal,omitempty"`
	ServiceKeySHA256  string `json:"service_key_sha256,omitempty"`
	CallerPrefix      string `json:"caller_prefix,omitempty"`
	SourceStart       uint64 `json:"source_start"`
	SourceEnd         uint64 `json:"source_end"`
	MemberOrdinal     int64  `json:"member_ordinal"`
	MemberRecordStart uint64 `json:"member_record_start"`
	MemberRecordEnd   uint64 `json:"member_record_end"`
	GenerationSHA256  string `json:"generation_sha256"`
	ScheduleSHA256    string `json:"schedule_sha256,omitempty"`
	PlanSHA256        string `json:"plan_sha256,omitempty"`
	UnitSHA256        string `json:"unit_sha256"`
	AuthoritySHA256   string `json:"authority_sha256"`
}

type CheckpointRecovery struct {
	ResultIdentitySHA256        string                         `json:"result_identity_sha256"`
	ResultDigestSHA256          string                         `json:"result_digest_sha256"`
	PlanSHA256                  string                         `json:"plan_sha256"`
	ExpectationSHA256           string                         `json:"expectation_sha256"`
	PartitionSHA256             string                         `json:"partition_sha256"`
	CandidateGenerationSHA256   string                         `json:"candidate_generation_sha256"`
	SourceGenerationSHA256      string                         `json:"source_generation_sha256"`
	ObservationGenerationSHA256 string                         `json:"observation_generation_sha256"`
	Domain                      string                         `json:"domain"`
	ExtractorVersion            string                         `json:"extractor_version"`
	ExtractionPolicySHA256      string                         `json:"extraction_policy_sha256"`
	ChunkIdentitySHA256         string                         `json:"chunk_identity_sha256,omitempty"`
	ScheduleStatusAtHit         store.GenerationScheduleStatus `json:"schedule_status_at_hit,omitempty"`
	ChunkStatusAtHit            store.GenerationChunkStatus    `json:"chunk_status_at_hit,omitempty"`
	LeasedAtHit                 bool                           `json:"leased_at_hit,omitempty"`
	CanonicalResultExistsAtHit  bool                           `json:"canonical_result_exists_at_hit"`
	ResultDirectorySyncedAtHit  bool                           `json:"result_directory_synced_at_hit"`
	CompletionAbsentAtHit       bool                           `json:"completion_absent_at_hit"`
	CompletionFileExistsAtHit   bool                           `json:"completion_file_exists_at_hit,omitempty"`
	CompletionBitClearAtHit     bool                           `json:"completion_bit_clear_at_hit,omitempty"`
	RootAbsentAtHit             bool                           `json:"root_absent_at_hit"`
	CurrentAbsentAtHit          bool                           `json:"current_absent_at_hit,omitempty"`
	SameResultBytesReused       bool                           `json:"same_result_bytes_reused"`
	CompletionExistsAfter       bool                           `json:"completion_exists_after"`
	RootExistsAfter             bool                           `json:"root_exists_after"`
	CurrentAfter                bool                           `json:"current_after"`
	ScheduleStatusAfter         store.GenerationScheduleStatus `json:"schedule_status_after,omitempty"`
	ChunkStatusAfter            store.GenerationChunkStatus    `json:"chunk_status_after,omitempty"`
	UnleasedAfter               bool                           `json:"unleased_after,omitempty"`
	StartCount                  uint64                         `json:"start_count"`
	CompletionCount             uint64                         `json:"completion_count"`
	RetrySuccessorCount         uint64                         `json:"retry_successor_count"`
	PriorityBefore              uint64                         `json:"priority_before"`
	PriorityAfter               uint64                         `json:"priority_after"`
	AttemptBefore               uint64                         `json:"attempt_before"`
	AttemptAfter                uint64                         `json:"attempt_after"`
	PrivateLeaseTokenChanged    bool                           `json:"private_lease_token_changed"`
	HardDeath                   bool                           `json:"hard_death"`
	CooperativeRelease          bool                           `json:"cooperative_release"`
}

type PressureTransition struct {
	Schema                        string                 `json:"schema"`
	TargetUsedPercent             uint64                 `json:"target_used_percent"`
	Action                        string                 `json:"action"`
	ExpectedDisposition           string                 `json:"expected_disposition"`
	ObservedDisposition           string                 `json:"observed_disposition"`
	GateOutcome                   string                 `json:"gate_outcome"`
	PriorGateSequenceSHA256       string                 `json:"prior_gate_sequence_sha256"`
	GateSequenceSHA256            string                 `json:"gate_sequence_sha256"`
	ServerEpoch                   uint64                 `json:"server_epoch"`
	RestartCount                  uint64                 `json:"restart_count"`
	VolumeAvailableBytesBefore    uint64                 `json:"volume_available_bytes_before"`
	VolumeAvailableBytesAfter     uint64                 `json:"volume_available_bytes_after"`
	VolumeUsedBytesBefore         uint64                 `json:"volume_used_bytes_before"`
	VolumeUsedBytesAfter          uint64                 `json:"volume_used_bytes_after"`
	ObservedUsedPercent           uint64                 `json:"observed_used_percent"`
	BallastAllocatedBytesBefore   uint64                 `json:"ballast_allocated_bytes_before"`
	BallastAllocatedBytesAfter    uint64                 `json:"ballast_allocated_bytes_after"`
	DataAllocatedBytesBefore      uint64                 `json:"data_allocated_bytes_before"`
	DataAllocatedBytesAtTarget    uint64                 `json:"data_allocated_bytes_at_target"`
	BallastMutationEventOrdinal   uint64                 `json:"ballast_mutation_event_ordinal"`
	GateEventOrdinal              uint64                 `json:"gate_event_ordinal"`
	PrePressureDeletedUnits       uint64                 `json:"pre_pressure_deleted_units,omitempty"`
	PrePressureAllocatedBytes     uint64                 `json:"pre_pressure_allocated_bytes,omitempty"`
	RecoveryUsedPercent           uint64                 `json:"recovery_used_percent,omitempty"`
	RecoveryUsedBytes             uint64                 `json:"recovery_used_bytes,omitempty"`
	RecoveryAvailableBytes        uint64                 `json:"recovery_available_bytes,omitempty"`
	RecoveryDataAllocatedBytes    uint64                 `json:"recovery_data_allocated_bytes,omitempty"`
	RecoveryDisposition           string                 `json:"recovery_disposition,omitempty"`
	RecoveryGateOutcome           string                 `json:"recovery_gate_outcome,omitempty"`
	RecoveryBallastAllocatedBytes uint64                 `json:"recovery_ballast_allocated_bytes,omitempty"`
	RecoveryBallastEventOrdinal   uint64                 `json:"recovery_ballast_event_ordinal,omitempty"`
	RecoveryGateEventOrdinal      uint64                 `json:"recovery_gate_event_ordinal,omitempty"`
	DataVolumeIdentity            string                 `json:"data_volume_identity"`
	BallastVolumeIdentity         string                 `json:"ballast_volume_identity"`
	AuthorityBeforeSHA256         string                 `json:"authority_before_sha256"`
	AuthorityAfterSHA256          string                 `json:"authority_after_sha256"`
	LifecycleFenceUnixMS          uint64                 `json:"lifecycle_fence_unix_ms,omitempty"`
	CapacityObservedUnixMS        uint64                 `json:"capacity_observed_unix_ms,omitempty"`
	LifecycleFenceEventOrdinal    uint64                 `json:"lifecycle_fence_event_ordinal,omitempty"`
	CapacityObservedEventOrdinal  uint64                 `json:"capacity_observed_event_ordinal,omitempty"`
	LifecycleScanned              uint64                 `json:"lifecycle_scanned,omitempty"`
	LifecycleDeleted              uint64                 `json:"lifecycle_deleted,omitempty"`
	LifecycleLogicalBytes         uint64                 `json:"lifecycle_logical_bytes,omitempty"`
	LifecycleRootBytes            uint64                 `json:"lifecycle_root_bytes,omitempty"`
	LifecycleMemberBytes          uint64                 `json:"lifecycle_member_bytes,omitempty"`
	LifecycleOwnerTurns           uint64                 `json:"lifecycle_owner_turns,omitempty"`
	LifecycleCycleSHA256          string                 `json:"lifecycle_cycle_sha256,omitempty"`
	Owners                        []LifecycleOwnerResult `json:"owners,omitempty"`
}

type ArchiveTransition struct {
	Schema                                 string                    `json:"schema"`
	ArchiveSHA256                          string                    `json:"archive_sha256"`
	ArchiveBytes                           uint64                    `json:"archive_bytes"`
	ArchiveBindingSHA256                   string                    `json:"archive_binding_sha256"`
	ManifestSchema                         string                    `json:"manifest_schema"`
	ManifestSHA256                         string                    `json:"manifest_sha256"`
	InventoryCanonicalization              string                    `json:"inventory_canonicalization"`
	ManifestInventory                      SetIdentity               `json:"manifest_inventory"`
	ReportInventory                        SetIdentity               `json:"report_inventory"`
	StateInventoryBefore                   SetIdentity               `json:"state_inventory_before"`
	StateInventoryArchived                 SetIdentity               `json:"state_inventory_archived"`
	StateInventoryAfter                    SetIdentity               `json:"state_inventory_after"`
	Components                             []ArchiveComponent        `json:"components"`
	Reports                                []ArchiveReportProjection `json:"reports"`
	PreRestoreStateSHA256                  string                    `json:"pre_restore_state_sha256"`
	RestoredStateSHA256                    string                    `json:"restored_state_sha256"`
	RelationshipSemanticSHA256             string                    `json:"relationship_semantic_sha256"`
	RelationshipGenerationBefore           string                    `json:"relationship_generation_before"`
	RelationshipGenerationAfter            string                    `json:"relationship_generation_after"`
	RelationshipRootBefore                 string                    `json:"relationship_root_before"`
	RelationshipRootAfter                  string                    `json:"relationship_root_after"`
	RelationshipRuntimeIdentityDisposition string                    `json:"relationship_runtime_identity_disposition"`
	AuthoritySnapshotBeforeSHA256          string                    `json:"authority_snapshot_before_sha256"`
	AuthoritySnapshotAfterSHA256           string                    `json:"authority_snapshot_after_sha256"`
	InstallationPathsAfterDestroy          uint64                    `json:"installation_paths_after_destroy"`
	RestoreTargetPathsBeforeRestore        uint64                    `json:"restore_target_paths_before_restore"`
	ScratchSourcePathsAfter                uint64                    `json:"scratch_source_paths_after"`
	ArchiveCreatedEventOrdinal             uint64                    `json:"archive_created_event_ordinal"`
	InstallationDestroyedEventOrdinal      uint64                    `json:"installation_destroyed_event_ordinal"`
	EmptyRestoreTargetEventOrdinal         uint64                    `json:"empty_restore_target_event_ordinal"`
	RestoreStartedEventOrdinal             uint64                    `json:"restore_started_event_ordinal"`
	ComparisonEventOrdinal                 uint64                    `json:"comparison_event_ordinal"`
}

type ArchiveComponent struct {
	Name           string `json:"name"`
	Classification string `json:"classification"`
	MediaType      string `json:"media_type"`
	Bytes          uint64 `json:"bytes"`
	SHA256         string `json:"sha256"`
}

type ArchiveReportProjection struct {
	Name                string `json:"name"`
	Schema              string `json:"schema"`
	Publications        uint64 `json:"publications"`
	V1Publications      uint64 `json:"v1_publications,omitempty"`
	V2Publications      uint64 `json:"v2_publications,omitempty"`
	Files               uint64 `json:"files,omitempty"`
	Bytes               uint64 `json:"bytes,omitempty"`
	Omitted             uint64 `json:"omitted"`
	OmittedPublications uint64 `json:"omitted_publications"`
	OmittedArtifacts    uint64 `json:"omitted_artifacts"`
	StaleMarkers        uint64 `json:"stale_markers"`
	TruncatedDetails    uint64 `json:"truncated_details"`
}

type ReaderTransition struct {
	Schema                         string  `json:"schema"`
	Reader                         string  `json:"reader"`
	QuerySHA256                    string  `json:"query_sha256"`
	OldSearchGenerationSHA256      string  `json:"old_search_generation_sha256"`
	NewSearchGenerationSHA256      string  `json:"new_search_generation_sha256"`
	OldHeldRecords                 uint64  `json:"old_held_records"`
	NewHeldRecords                 uint64  `json:"new_held_records"`
	OldHeldProjectionSHA256        string  `json:"old_held_projection_sha256"`
	NewHeldProjectionSHA256        string  `json:"new_held_projection_sha256"`
	PostDeleteOldGenerationOutcome string  `json:"post_delete_old_generation_outcome,omitempty"`
	LeaseAcquired                  uint64  `json:"lease_acquired"`
	OldVisibleWhileHeld            bool    `json:"old_visible_while_held"`
	NewCurrentWhileHeld            bool    `json:"new_current_while_held"`
	RetirementAttemptsWhileHeld    uint64  `json:"retirement_attempts_while_held,omitempty"`
	ProtectedWhileHeld             uint64  `json:"protected_while_held,omitempty"`
	LeaseReleased                  uint64  `json:"lease_released"`
	RetirementAttemptsAfterRelease uint64  `json:"retirement_attempts_after_release,omitempty"`
	DeletedAfterRelease            uint64  `json:"deleted_after_release,omitempty"`
	LeaseAcquireEventOrdinal       uint64  `json:"lease_acquire_event_ordinal"`
	NewCurrentEventOrdinal         uint64  `json:"new_current_event_ordinal"`
	HeldRetirementEventOrdinal     uint64  `json:"held_retirement_event_ordinal,omitempty"`
	OldHeldQueryEventOrdinal       uint64  `json:"old_held_query_event_ordinal"`
	NewHeldQueryEventOrdinal       uint64  `json:"new_held_query_event_ordinal"`
	LeaseReleaseEventOrdinal       uint64  `json:"lease_release_event_ordinal"`
	PostReleaseRetirementOrdinal   uint64  `json:"post_release_retirement_event_ordinal,omitempty"`
	DeleteEventOrdinal             uint64  `json:"delete_event_ordinal,omitempty"`
	PostDeleteProbeEventOrdinal    uint64  `json:"post_delete_probe_event_ordinal,omitempty"`
	OldRoleAfterReplacement        string  `json:"old_role_after_replacement,omitempty"`
	NewRoleAfterReplacement        string  `json:"new_role_after_replacement,omitempty"`
	LifecycleAttemptsWhileHeld     uint64  `json:"lifecycle_attempts_while_held,omitempty"`
	OldRootProtectedWhileHeld      uint64  `json:"old_root_protected_while_held,omitempty"`
	HeldLifecycleScanned           *uint64 `json:"held_lifecycle_scanned,omitempty"`
	HeldLifecycleOutcome           string  `json:"held_lifecycle_outcome,omitempty"`
	LifecycleAttemptsAfterRelease  uint64  `json:"lifecycle_attempts_after_release,omitempty"`
	OldRootProtectedAfterRelease   uint64  `json:"old_root_protected_after_release,omitempty"`
	PostReleaseLifecycleScanned    *uint64 `json:"post_release_lifecycle_scanned,omitempty"`
	PostReleaseLifecycleOutcome    string  `json:"post_release_lifecycle_outcome,omitempty"`
	PostReleaseOldRecords          uint64  `json:"post_release_old_records,omitempty"`
	PostReleaseOldProjectionSHA256 string  `json:"post_release_old_projection_sha256,omitempty"`
	PostReleaseOldOutcome          string  `json:"post_release_old_outcome,omitempty"`
	OldReaderHeldThroughReprobe    bool    `json:"old_reader_held_through_reprobe,omitempty"`
	HeldLifecycleEventOrdinal      uint64  `json:"held_lifecycle_event_ordinal,omitempty"`
	PostReleaseLifecycleOrdinal    uint64  `json:"post_release_lifecycle_event_ordinal,omitempty"`
	PostReleaseOldQueryOrdinal     uint64  `json:"post_release_old_query_event_ordinal,omitempty"`
	AuthorityBeforeSHA256          string  `json:"authority_before_sha256"`
	AuthorityAfterSHA256           string  `json:"authority_after_sha256"`
}

type LifecycleTransition struct {
	Schema                       string                 `json:"schema"`
	Scanned                      uint64                 `json:"scanned"`
	Deleted                      uint64                 `json:"deleted"`
	LogicalBytes                 uint64                 `json:"logical_bytes"`
	RootBytes                    uint64                 `json:"root_bytes"`
	MemberBytes                  uint64                 `json:"member_bytes"`
	OwnerTurns                   uint64                 `json:"owner_turns"`
	LifecycleFenceUnixMS         uint64                 `json:"lifecycle_fence_unix_ms"`
	CapacityObservedUnixMS       uint64                 `json:"capacity_observed_unix_ms"`
	LifecycleFenceEventOrdinal   uint64                 `json:"lifecycle_fence_event_ordinal"`
	CapacityObservedEventOrdinal uint64                 `json:"capacity_observed_event_ordinal"`
	AuthorityBeforeSHA256        string                 `json:"authority_before_sha256"`
	AuthorityAfterSHA256         string                 `json:"authority_after_sha256"`
	CycleSHA256                  string                 `json:"cycle_sha256"`
	Owners                       []LifecycleOwnerResult `json:"owners"`
}

type LifecycleOwnerResult struct {
	Name              string `json:"name"`
	State             string `json:"state"`
	Completeness      string `json:"completeness"`
	Scanned           uint64 `json:"scanned"`
	Deleted           uint64 `json:"deleted"`
	LogicalBytes      uint64 `json:"logical_bytes"`
	RootBytes         uint64 `json:"root_bytes"`
	MemberBytes       uint64 `json:"member_bytes"`
	Backlog           bool   `json:"backlog"`
	AttemptedAtUnixMS uint64 `json:"attempted_at_unix_ms"`
}

type ReceiptSeal struct {
	PolicySchema                         string `json:"policy_schema"`
	SignerFingerprint                    string `json:"signer_fingerprint"`
	FreezeSignatureNamespace             string `json:"freeze_signature_namespace"`
	SourceVerificationSignatureNamespace string `json:"source_verification_signature_namespace"`
	ReturnedSignatureNamespace           string `json:"returned_signature_namespace"`
	VerificationPosture                  string `json:"verification_posture"`
}

type ReceiptNonClaims struct {
	ChangesProductionBehavior        bool `json:"changes_production_behavior"`
	RaisesProductionCap              bool `json:"raises_production_cap"`
	AuthorizesExecution              bool `json:"authorizes_execution"`
	AuthorizesPrivateReplay          bool `json:"authorizes_private_replay"`
	EstablishesTargetSLO             bool `json:"establishes_target_slo"`
	QualifiesSupportedScale          bool `json:"qualifies_supported_scale"`
	EstablishesAccuracyCompleteness  bool `json:"establishes_accuracy_completeness"`
	EstablishesCommitCadence         bool `json:"establishes_commit_cadence"`
	EstablishesQueueCatchup          bool `json:"establishes_queue_catchup"`
	EstablishesFreshnessUnderCadence bool `json:"establishes_freshness_under_cadence"`
	SelectsTopology                  bool `json:"selects_topology"`
	EstablishesMigrationDecommission bool `json:"establishes_migration_decommission"`
	AuthorizesRelease                bool `json:"authorizes_release"`
}

type ReceiptTeardown struct {
	Scoped                     *ScopedTeardownEvidence `json:"scoped,omitempty"`
	Attempted                  bool                    `json:"attempted"`
	Completed                  bool                    `json:"completed"`
	Outcome                    string                  `json:"outcome"`
	Failure                    *TeardownFailure        `json:"failure,omitempty"`
	DescendantsStopped         bool                    `json:"descendants_stopped"`
	StoreClosed                bool                    `json:"store_closed"`
	DerivedCustodyPaths        uint64                  `json:"derived_custody_paths"`
	ScratchSourcePaths         uint64                  `json:"scratch_source_paths"`
	ChildrenRemaining          uint64                  `json:"children_remaining"`
	PressureBallastBytes       uint64                  `json:"pressure_ballast_bytes"`
	PressureVolumeDetached     bool                    `json:"pressure_volume_detached"`
	PressureImagePaths         uint64                  `json:"pressure_image_paths"`
	PressureImageRemoved       bool                    `json:"pressure_image_removed"`
	BackingDerivedCustodyPaths uint64                  `json:"backing_derived_custody_paths"`
	BackingVolumeIdentity      string                  `json:"backing_volume_identity"`
	RetainedSourceFreeOnly     bool                    `json:"retained_source_free_only"`
	DescendantStopErrors       uint64                  `json:"descendant_stop_errors"`
	StoreCloseErrors           uint64                  `json:"store_close_errors"`
	DerivedRemovalErrors       uint64                  `json:"derived_removal_errors"`
	ScratchRemovalErrors       uint64                  `json:"scratch_removal_errors"`
	BallastRemovalErrors       uint64                  `json:"ballast_removal_errors"`
	VolumeDetachErrors         uint64                  `json:"volume_detach_errors"`
	ImageRemovalErrors         uint64                  `json:"image_removal_errors"`
	MeasurementErrors          uint64                  `json:"measurement_errors"`
	MeasurementUnavailable     []string                `json:"measurement_unavailable"`
}

type TeardownFailure struct {
	Schema         string   `json:"schema"`
	Kind           string   `json:"kind"`
	FailedChecks   []string `json:"failed_checks"`
	EvidenceSHA256 string   `json:"evidence_sha256"`
}

var receiptMetricNames = []string{
	"allocation_measurement_available", "applicable_partitions", "available_disk_bytes", "cache_hits", "cache_lookups", "cache_member_reads",
	"cache_member_validations", "cache_misses", "cache_root_reads", "cache_root_validations",
	"changed_logical_services", "changed_physical_files", "child_processes", "control_reads",
	"data_allocated_bytes", "data_logical_bytes", "direct_recovery_topology_limits", "git_reads", "index_files",
	"job_attempts", "logical_memberships", "lifecycle_deleted", "lifecycle_owner_turns",
	"materialized_cartesian_owner_pairs", "max_lifecycle_deletes_in_any_turn",
	"max_retries_on_any_unit", "max_rows_in_any_transaction", "member_reads", "observation_parses",
	"peak_rss_bytes", "physical_corpus_passes", "combined_physical_owners", "process_measurement_available", "publication_writes",
	"published_domains", "pressure_custody_margin_bytes", "pressure_volume_bytes",
	"relationship_projections", "resolver_blob_bytes", "resolver_blob_reads",
	"relationship_build_attempts", "retries", "reuse_decisions", "source_reuse_decisions",
	"search_reuse_decisions", "observation_reuse_decisions", "catalog_reuse_decisions",
	"relationship_reuse_decisions", "service_references", "service_rows", "source_logical_bytes",
	"source_unique_bytes", "store_rows", "store_transactions", "unsupported_source_files",
	"wall_ms", "total_disk_bytes", "minimum_pre_pressure_used_bytes", "maximum_pre_pressure_used_bytes",
	"minimum_pre_pressure_allocated_bytes", "maximum_pre_pressure_allocated_bytes", "ballast_ceiling_bytes",
}

var unavailableMetricNames = []string{
	"available_disk_bytes", "data_allocated_bytes", "data_logical_bytes",
	"peak_rss_bytes", "total_disk_bytes", "wall_ms",
}

func validUnavailableMetrics(values []string) bool {
	if !slices.IsSorted(values) {
		return false
	}
	for index, value := range values {
		if !slices.Contains(unavailableMetricNames, value) || index > 0 && values[index-1] == value {
			return false
		}
	}
	return true
}

var receiptSectionNames = []string{
	"authority", "decision", "environment", "execution_freeze", "implementation", "inputs",
	"measurements", "nonclaims", "phase_results", "query_results", "relationship_results",
	"revision_results", "seal", "state_results", "teardown", "transition_results",
}

var receiptHostFieldNames = []string{
	"backing_available_disk_bytes", "backing_total_disk_bytes", "backing_volume_identity",
	"ballast_volume_identity", "data_volume_identity", "goarch", "goos", "logical_cpus",
	"memory_bytes", "os_build_version", "os_product_version", "pressure_available_disk_bytes", "pressure_total_disk_bytes",
	"pressure_allocation_unit_bytes", "volume_identity_method",
}

var transitionPhases = []string{
	"physical_delta_b", "logical_delta_b", "return_a", "stale_lease", "process_restart",
	"pressure_80", "pressure_90", "pressure_75", "archive_restore", "lifecycle_collection",
}

const archiveInventoryCanonicalization = "source-free artifact records sorted by path; each canonical JSON record is framed by an unsigned big-endian 64-bit byte length"

// BindExecutionFreezeForReceipt performs the one expensive admission check
// before any number of cheap receipt validations.
func BindExecutionFreezeForReceipt(
	freeze ExecutionFreeze,
	plan Plan,
	expectedCommits ExecutionCommits,
	expectedSignerFingerprint string,
	checkout CheckoutAdmissionBinding,
	profileAdmission ExecutionProfileAdmissionBinding,
	admission ExecutionFreezeAdmissionBinding,
) (ExecutionFreezeBinding, error) {
	freeze = cloneExecutionFreezeForBinding(freeze)
	if err := ValidateExecutionFreeze(
		freeze, plan, expectedCommits, expectedSignerFingerprint, checkout, profileAdmission,
	); err != nil {
		return ExecutionFreezeBinding{}, err
	}
	return bindExecutionFreezeForReceipt(
		freeze, plan, expectedCommits, expectedSignerFingerprint, admission,
	)
}

func bindExecutionFreezeForReceipt(
	freeze ExecutionFreeze,
	plan Plan,
	expectedCommits ExecutionCommits,
	expectedSignerFingerprint string,
	admission ExecutionFreezeAdmissionBinding,
) (ExecutionFreezeBinding, error) {
	planSHA256, err := receiptSHA256(plan)
	if err != nil {
		return ExecutionFreezeBinding{}, err
	}
	freezeSHA256, err := receiptSHA256(freeze)
	if err != nil {
		return ExecutionFreezeBinding{}, err
	}
	wantAdmissionSHA256, err := receiptSHA256(struct {
		Schema             string `json:"schema"`
		FreezeSHA256       string `json:"freeze_sha256"`
		SignatureNamespace string `json:"signature_namespace"`
		SignerFingerprint  string `json:"signer_fingerprint"`
		Order              string `json:"order"`
		EventOrdinal       uint64 `json:"event_ordinal"`
	}{
		Schema: plan.ReceiptContract.ExecutionAdmissionSchema, FreezeSHA256: freezeSHA256,
		SignatureNamespace: plan.SealPolicy.FreezeSignatureNamespace,
		SignerFingerprint:  expectedSignerFingerprint, Order: plan.ReceiptContract.ExecutionAdmissionOrder,
		EventOrdinal: 1,
	})
	if err != nil || admission.schema != plan.ReceiptContract.ExecutionAdmissionSchema ||
		admission.freezeSHA256 != freezeSHA256 || admission.signatureNamespace != plan.SealPolicy.FreezeSignatureNamespace ||
		admission.signerFingerprint != expectedSignerFingerprint || admission.admissionEventSHA256 != wantAdmissionSHA256 ||
		admission.admissionEventOrdinal != 1 || !admission.signatureVerified || !admission.verifiedBeforeWork {
		return ExecutionFreezeBinding{}, errors.New("T42.2 execution freeze lacks verified pre-work signature admission")
	}
	return ExecutionFreezeBinding{
		freeze: freeze, expectedCommits: expectedCommits,
		expectedSignerFingerprint: expectedSignerFingerprint,
		planSHA256:                planSHA256, freezeSHA256: freezeSHA256,
		admissionEventSHA256: admission.admissionEventSHA256, admissionEventOrdinal: admission.admissionEventOrdinal,
	}, nil
}

func cloneExecutionFreezeForBinding(freeze ExecutionFreeze) ExecutionFreeze {
	freeze.Tools = slices.Clone(freeze.Tools)
	freeze.Profile = cloneExecutionProfile(freeze.Profile)
	freeze.Pressure.Targets = slices.Clone(freeze.Pressure.Targets)
	return freeze
}

// DecodeReceipt accepts only canonical source-free evidence bound to the exact
// execution freeze previously admitted with externally selected commits.
func DecodeReceipt(
	raw []byte,
	plan Plan,
	binding ExecutionFreezeBinding,
	packageBinding ReturnedPackageBinding,
) (Receipt, error) {
	if len(raw) == 0 || len(raw) > MaxReceiptBytes || uint64(len(raw)) > plan.ReceiptContract.MaximumBytes {
		return Receipt{}, errors.New("T42.2 receipt size is invalid")
	}
	if err := rejectSourceBearingReceipt(raw); err != nil {
		return Receipt{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var receipt Receipt
	if err := decoder.Decode(&receipt); err != nil {
		return Receipt{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Receipt{}, errors.New("T42.2 receipt contains trailing data")
	}
	if err := ValidateReceipt(receipt, plan, binding, packageBinding); err != nil {
		return Receipt{}, err
	}
	want, err := MarshalCanonical(receipt)
	if err != nil || !bytes.Equal(raw, want) {
		return Receipt{}, errors.New("T42.2 receipt is not canonical")
	}
	return receipt, nil
}

// ValidateReceipt applies the prospective stopped-or-passed T42.2 contract.
func ValidateReceipt(
	receipt Receipt,
	plan Plan,
	binding ExecutionFreezeBinding,
	packageBinding ReturnedPackageBinding,
) error {
	if err := validateReceiptAccountingVersion(receipt, plan); err != nil {
		return err
	}
	planSHA256, err := receiptSHA256(plan)
	if err != nil {
		return err
	}
	freezeSHA256, err := receiptSHA256(binding.freeze)
	if err != nil {
		return err
	}
	if binding.planSHA256 != planSHA256 || binding.freeze.PlanSHA256 != planSHA256 ||
		binding.freeze.Commits != binding.expectedCommits ||
		binding.freeze.SignerFingerprint != binding.expectedSignerFingerprint ||
		binding.freezeSHA256 != freezeSHA256 || !validDigest(binding.freezeSHA256) {
		return errors.New("T42.2 execution freeze binding is invalid")
	}
	freeze := binding.freeze
	receiptDigest, err := receiptSHA256(receipt)
	if err != nil {
		return err
	}
	revisionResultsSHA256, err := receiptSHA256(receipt.RevisionResults)
	if err != nil {
		return err
	}
	exactInventorySHA256, err := receiptSHA256(plan.SealPolicy.ExactInventory)
	if err != nil {
		return err
	}
	if !packageBinding.returnedSignatureVerified || !packageBinding.sourceSignatureVerified || !packageBinding.sourceVerified ||
		packageBinding.signerFingerprint != binding.expectedSignerFingerprint ||
		packageBinding.returnedSignatureNamespace != plan.SealPolicy.ReturnedSignatureNamespace ||
		packageBinding.sourceSignatureNamespace != plan.SealPolicy.SourceVerificationSignatureNamespace ||
		packageBinding.receiptSHA256 != receiptDigest ||
		packageBinding.revisionResultsSHA256 != revisionResultsSHA256 ||
		packageBinding.sourceVerificationSchema != plan.SealPolicy.SourceVerificationSchema ||
		packageBinding.sourcePlanSHA256 != planSHA256 || packageBinding.sourceFreezeSHA256 != freezeSHA256 ||
		packageBinding.sourceExactInventorySHA256 != exactInventorySHA256 ||
		!validDigest(packageBinding.sourceVerificationSHA256) ||
		!validDigest(packageBinding.packageSHA256) || !validDigest(packageBinding.inventorySHA256) ||
		!slices.Equal(packageBinding.exactInventory, plan.SealPolicy.ExactInventory) {
		return errors.New("T42.2 receipt lacks an authenticated returned-package binding")
	}
	profileSHA256, err := receiptSHA256(plan.Profile)
	if err != nil {
		return err
	}
	oracleSHA256, err := receiptSHA256(plan.Oracle)
	if err != nil {
		return err
	}
	revisionsSHA256, err := receiptSHA256(plan.Revisions)
	if err != nil {
		return err
	}
	meterPolicySHA256, err := receiptSHA256(plan.MeterPolicy)
	if err != nil {
		return err
	}
	if receipt.Schema != plan.ReceiptContract.Schema || !receipt.SourceFree ||
		!slices.Equal(plan.ReceiptContract.RequiredSections, receiptSectionNames) ||
		!plan.ReceiptContract.StoppedSuffixNotRun || !plan.ReceiptContract.TypedFailureOnly ||
		!plan.ReceiptContract.CanonicalSourceFree ||
		receipt.Authority.PlanSchema != plan.Schema || receipt.Authority.PlanSHA256 != planSHA256 ||
		receipt.ExecutionFreeze.Schema != freeze.Schema ||
		receipt.ExecutionFreeze.SHA256 != binding.freezeSHA256 ||
		receipt.ExecutionFreeze.SignerFingerprint != binding.expectedSignerFingerprint ||
		receipt.ExecutionFreeze.AdmissionEventSHA256 != binding.admissionEventSHA256 ||
		receipt.ExecutionFreeze.AdmissionEventOrdinal != binding.admissionEventOrdinal ||
		receipt.ExecutionFreeze.Commits != freeze.Commits ||
		receipt.Authority.SourceCommit != plan.SourceCommit ||
		receipt.Authority.ProfileSHA256 != profileSHA256 ||
		receipt.Authority.OracleSHA256 != oracleSHA256 ||
		receipt.Authority.RevisionHistorySHA256 != revisionsSHA256 ||
		receipt.Authority.MeterPolicySHA256 != meterPolicySHA256 ||
		receipt.Authority.SourceVerificationSHA256 != packageBinding.sourceVerificationSHA256 ||
		!reflect.DeepEqual(receipt.Inputs, plan.Inputs) {
		return errors.New("T42.2 receipt authority binding is invalid")
	}
	if receipt.Seal != (ReceiptSeal{
		PolicySchema:                         plan.SealPolicy.Schema,
		SignerFingerprint:                    binding.expectedSignerFingerprint,
		FreezeSignatureNamespace:             plan.SealPolicy.FreezeSignatureNamespace,
		SourceVerificationSignatureNamespace: plan.SealPolicy.SourceVerificationSignatureNamespace,
		ReturnedSignatureNamespace:           plan.SealPolicy.ReturnedSignatureNamespace,
		VerificationPosture:                  "freeze_preflight_source_and_returned_signatures_verified_by_external_bindings",
	}) {
		return errors.New("T42.2 receipt seal posture differs from the frozen policy")
	}
	if err := validateReceiptImplementation(receipt.Implementation, plan, freeze); err != nil {
		return err
	}
	if err := validateReceiptEnvironment(receipt.Environment, plan, freeze); err != nil {
		return err
	}
	outcomes, stopped, err := validateReceiptPhases(receipt.PhaseResults, plan)
	if err != nil {
		return err
	}
	if err := validateReceiptMeasurements(
		receipt.Measurements, outcomes, stopped, receipt.Teardown, binding.admissionEventOrdinal, plan, freeze,
	); err != nil {
		return err
	}
	teardownClean, err := validateReceiptTeardown(receipt.Teardown, receipt.Measurements, plan, freeze, hasOwnedServerStart(receipt.StateResults))
	if err != nil {
		return err
	}
	if err := validateReceiptDecision(receipt.Decision, stopped, teardownClean, receipt.Measurements, plan); err != nil {
		return err
	}
	authorities, err := expectedAuthorityStates(plan)
	if err != nil {
		return err
	}
	operational := plan.PhaseOrder[1 : len(plan.PhaseOrder)-1]
	resolvedAuthority, err := resolveAuthorityResults(
		receipt.Authority.ExtractionRootSnapshots, receipt.Authority.Snapshots,
		receipt.Authority.Results, operational, outcomes,
	)
	if err != nil {
		return fmt.Errorf("T42.2 authority snapshots: %w", err)
	}
	if err := validateAuthorityResults(
		resolvedAuthority, operational, outcomes, authorities, receipt.RevisionResults, plan,
	); err != nil {
		return fmt.Errorf("T42.2 authority results: %w", err)
	}
	stateDigests, err := expectedStateProjectionDigests(plan)
	if err != nil {
		return err
	}
	if err := validateExactPhaseEvidence(
		receipt.StateResults, operational, outcomes, stateDigests, resolvedAuthority,
		receipt.Measurements, freeze, stopped, plan,
	); err != nil {
		return fmt.Errorf("T42.2 state results: %w", err)
	}
	if err := validateTransitionResults(receipt.TransitionResults, outcomes, stopped, resolvedAuthority, receipt.Measurements, plan, freeze); err != nil {
		return fmt.Errorf("T42.2 transition results: %w", err)
	}
	if err := validatePhaseRuntimeTransitionBindings(receipt.StateResults, receipt.TransitionResults, freeze.Profile.Epochs); err != nil {
		return fmt.Errorf("T42.2 runtime transition binding: %w", err)
	}
	productAuthoritySHA256 := ""
	productAuthority := AuthorityPhaseResult{}
	if outcomes["product_queries"] == "passed" {
		productAuthoritySHA256, err = authorityResultSHA256(resolvedAuthority, "product_queries")
		if err != nil {
			return err
		}
		index := slices.IndexFunc(resolvedAuthority, func(value AuthorityPhaseResult) bool {
			return value.Phase == "product_queries"
		})
		productAuthority = resolvedAuthority[index]
	}
	productMetrics, ok := namedPhaseMetrics(receipt.Measurements, "product_queries")
	if !ok {
		return errors.New("T42.2 product query measurement is absent")
	}
	if err := validateQueryEvidence(receipt.QueryResults, outcomes["product_queries"], productAuthoritySHA256, productMetrics, plan); err != nil {
		return err
	}
	if err := validateRelationshipEvidence(receipt.RelationshipResults, outcomes["product_queries"], productAuthoritySHA256, productAuthority, plan); err != nil {
		return err
	}
	if err := validateRevisionResults(receipt.RevisionResults, outcomes, plan); err != nil {
		return fmt.Errorf("T42.2 revision results: %w", err)
	}
	if receipt.NonClaims != receiptNonClaims(plan.Claims) {
		return errors.New("T42.2 receipt nonclaims differ from the frozen plan")
	}
	if err := validateStoppedFailureEvidence(receipt, stopped, resolvedAuthority, plan, freeze); err != nil {
		return err
	}
	raw, err := MarshalCanonical(receipt)
	if err != nil {
		return err
	}
	if uint64(len(raw)) > plan.ReceiptContract.MaximumBytes {
		return errors.New("T42.2 receipt exceeds its frozen byte bound")
	}
	return rejectSourceBearingReceipt(raw)
}

func validateReceiptImplementation(value ReceiptImplementation, plan Plan, freeze ExecutionFreeze) error {
	if value.IntegratedMainCommit != freeze.Commits.IntegratedMainCommit ||
		value.T422SourceCommit != freeze.Commits.T422SourceCommit ||
		value.CleanTree != freeze.Commits.CleanTree || !value.CleanTree ||
		value.DigestAlgorithm != plan.ToolPolicy.DigestAlgorithm ||
		!reflect.DeepEqual(value.Tools, freeze.Tools) {
		return errors.New("T42.2 implementation identity is invalid")
	}
	return nil
}

func validateReceiptEnvironment(value ReceiptEnvironment, plan Plan, freeze ExecutionFreeze) error {
	if !slices.Equal(plan.ToolPolicy.RequiredHostFields, receiptHostFieldNames) || value.Fields != freeze.Host {
		return errors.New("T42.2 host identity is invalid")
	}
	digest, err := receiptSHA256(value.Fields)
	if err != nil || value.SHA256 != digest {
		return errors.New("T42.2 host identity digest is invalid")
	}
	return nil
}

func validateReceiptPhases(values []PhaseResult, plan Plan) (map[string]string, *ReceiptFailure, error) {
	if len(values) != len(plan.PhaseOrder) || !plan.ReceiptContract.TeardownAlwaysRuns {
		return nil, nil, errors.New("T42.2 phase inventory is incomplete")
	}
	outcomes := make(map[string]string, len(values))
	var stopped *ReceiptFailure
	state := "passed"
	for index, name := range plan.PhaseOrder {
		value := values[index]
		if value.Name != name {
			return nil, nil, fmt.Errorf("T42.2 phase %q is invalid", name)
		}
		if name == "teardown" {
			if index != len(plan.PhaseOrder)-1 || value.Outcome != plan.ReceiptContract.TeardownPhaseOutcome || value.Failure != nil {
				return nil, nil, errors.New("T42.2 teardown is not the unconditional attempted terminal corridor")
			}
			outcomes[name] = value.Outcome
			continue
		}
		if !slices.Contains(plan.ReceiptContract.PhaseOutcomes, value.Outcome) {
			return nil, nil, fmt.Errorf("T42.2 phase %q outcome is invalid", name)
		}
		switch state {
		case "passed":
			switch value.Outcome {
			case "passed":
				if value.Failure != nil {
					return nil, nil, errors.New("T42.2 passed phase retained a failure")
				}
			case "stopped":
				if value.Failure == nil || stopped != nil || !validReceiptFailure(*value.Failure, name, plan) {
					return nil, nil, errors.New("T42.2 stopped phase lacks one typed failure")
				}
				failure := *value.Failure
				stopped = &failure
				state = "not_run"
			default:
				return nil, nil, errors.New("T42.2 phase sequence lacks a terminal outcome")
			}
		case "not_run":
			if value.Outcome != "not_run" || value.Failure != nil {
				return nil, nil, errors.New("T42.2 stopped phase suffix is not not_run")
			}
		}
		outcomes[name] = value.Outcome
	}
	return outcomes, stopped, nil
}

func validateReceiptDecision(
	value ReceiptDecision,
	stopped *ReceiptFailure,
	teardownClean bool,
	measurements []PhaseMeasurement,
	plan Plan,
) error {
	if value.Gate2V2 != plan.Claims.Gate2V2 || value.ReleasePosture != plan.Claims.ReleasePosture {
		return errors.New("T42.2 decision changed the frozen validation or release posture")
	}
	selected, priority := "", uint64(0)
	if stopped != nil {
		var err error
		selected, priority, err = expectedStoppedDecision(*stopped, measurements, plan)
		if err != nil {
			return err
		}
	}
	if !teardownClean {
		if value.Outcome != "stopped" || value.Selected != "reduce" ||
			value.RulePriority != 4 || value.Reason != "teardown_failed" || !value.Substantiated {
			return errors.New("T42.2 failed teardown decision is invalid")
		}
		return nil
	}
	if stopped == nil {
		if value.Outcome != "passed" || value.Selected != "continue" ||
			value.RulePriority != 3 || value.Reason != "all_exact_checks_passed" || !value.Substantiated {
			return errors.New("T42.2 completed decision is invalid")
		}
		return nil
	}
	if value.Outcome != "stopped" || value.Selected != selected || value.RulePriority != priority ||
		value.Reason != stopped.Code || !value.Substantiated {
		return errors.New("T42.2 stopped decision is invalid")
	}
	return nil
}

func expectedStoppedDecision(
	stopped ReceiptFailure,
	measurements []PhaseMeasurement,
	plan Plan,
) (string, uint64, error) {
	metrics, ok := namedPhaseMetrics(measurements, stopped.Phase)
	if !ok {
		return "", 0, errors.New("T42.2 stopped decision lacks phase metrics")
	}
	if metrics.MaterializedOwnerPairs > 0 {
		if !slices.Contains(plan.ReceiptContract.MaterializedOwnerPairPhases, stopped.Phase) ||
			stopped.Class != "topology" || stopped.Code != "materialized_cartesian_owner_pairs_nonzero" {
			return "", 0, errors.New("T42.2 materialized owner pairs lack the exact topology stop")
		}
		if !crossingObservationMatches(stopped.Observation, "materialized_cartesian_owner_pairs", 0, uint64(metrics.MaterializedOwnerPairs)) {
			return "", 0, errors.New("T42.2 topology stop observation is not exact")
		}
		return frozenDecision(plan, 1)
	}
	if metrics.DirectRecoveryLimits != 0 {
		if stopped.Phase != "process_restart" || metrics.DirectRecoveryLimits != 1 ||
			stopped.Class != "topology" || stopped.Code != "direct_recovery_topology_limit" {
			return "", 0, errors.New("T42.2 topology stop is not a frozen signal")
		}
		if !counterObservationMatches(stopped.Observation, "direct_recovery_topology_limits", 0, 1) {
			return "", 0, errors.New("T42.2 direct-recovery observation is not exact")
		}
		return frozenDecision(plan, 1)
	}
	if stopped.Class == "topology" {
		return "", 0, errors.New("T42.2 topology stop lacks a frozen measured signal")
	}
	if stopped.Code == "phase_work_limit" {
		if stopped.Class != "resource" {
			return "", 0, errors.New("T42.2 phase-work stop has the wrong class")
		}
		return frozenDecision(plan, 4)
	}
	peakMetric, peakBytes := receiptRSSMetric(metrics, plan.Schema)
	peakCrossed := peakBytes > plan.SafetyEnvelope.MaximumPeakRSSBytes
	dataCrossed := uint64(metrics.DataAllocatedBytes) > plan.SafetyEnvelope.MaximumDataAllocatedBytes
	logicalCrossed := uint64(metrics.DataLogicalBytes) > plan.WorkEnvelope.MaximumDataLogicalBytes
	totalWall, err := measuredWallThrough(measurements, stopped.Phase)
	if err != nil {
		return "", 0, err
	}
	totalCrossed := totalWall > plan.SafetyEnvelope.MaximumTotalWallMS
	deadlineIndex := slices.IndexFunc(plan.PhaseDeadlines, func(value PhaseDeadline) bool { return value.Phase == stopped.Phase })
	if deadlineIndex < 0 {
		return "", 0, errors.New("T42.2 stopped phase deadline is absent")
	}
	phaseCrossed := uint64(metrics.WallMS) > plan.PhaseDeadlines[deadlineIndex].DeadlineMS
	crossings := 0
	for _, crossed := range []bool{peakCrossed, dataCrossed, logicalCrossed, totalCrossed, phaseCrossed} {
		if crossed {
			crossings++
		}
	}
	resourceCodeMatches := stopped.Class == "resource" && (stopped.Code == receiptRSSStopCode(plan.Schema) && peakCrossed ||
		stopped.Code == "data_allocated_ceiling" && dataCrossed ||
		stopped.Code == "data_logical_ceiling" && logicalCrossed ||
		stopped.Code == "total_wall_ceiling" && totalCrossed ||
		stopped.Code == "phase_deadline" && phaseCrossed)
	if crossings != 0 || stopped.Class == "resource" {
		if crossings > 1 {
			if stopped.Class != "resource" || stopped.Code != "multiple_resource_ceilings" {
				return "", 0, errors.New("T42.2 multiple resource ceilings lack the exact fallback stop")
			}
			if !gaugeObservationMatches(stopped.Observation, "multiple_resource_ceilings", 0, 1) {
				return "", 0, errors.New("T42.2 multiple-ceiling observation is not exact")
			}
			return frozenDecision(plan, 4)
		}
		if crossings != 1 || !resourceCodeMatches {
			return "", 0, errors.New("T42.2 resource stop does not match its measured ceiling")
		}
		metric, limit, observed := "", uint64(0), uint64(0)
		switch stopped.Code {
		case "peak_rss_ceiling", "observed_rss_ceiling":
			metric, limit, observed = peakMetric, plan.SafetyEnvelope.MaximumPeakRSSBytes, peakBytes
		case "data_allocated_ceiling":
			metric, limit, observed = "data_allocated_bytes", plan.SafetyEnvelope.MaximumDataAllocatedBytes, uint64(metrics.DataAllocatedBytes)
		case "data_logical_ceiling":
			metric, limit, observed = "data_logical_bytes", plan.WorkEnvelope.MaximumDataLogicalBytes, uint64(metrics.DataLogicalBytes)
		case "total_wall_ceiling":
			metric, limit, observed = "total_wall_ms", plan.SafetyEnvelope.MaximumTotalWallMS, totalWall
		case "phase_deadline":
			metric, limit, observed = "phase_wall_ms", plan.PhaseDeadlines[deadlineIndex].DeadlineMS, uint64(metrics.WallMS)
		}
		if !gaugeObservationMatches(stopped.Observation, metric, limit, observed) {
			return "", 0, errors.New("T42.2 resource stop observation is not exact")
		}
		if plan.Schema == PlanV3Schema && (!metrics.NativeMeasurementAvailable || !metrics.DispatchMeasurementAvailable) {
			// A retained crossing plus an independent accounting failure is not
			// resource-only evidence for a topology/cohort recommendation.
			return frozenDecision(plan, 4)
		}
		if slices.Index(plan.PhaseOrder, stopped.Phase) <= 0 {
			return frozenDecision(plan, 4)
		}
		return frozenDecision(plan, 2)
	}
	return frozenDecision(plan, 4)
}

func validateStoppedFailureEvidence(
	receipt Receipt,
	stopped *ReceiptFailure,
	authorities []AuthorityPhaseResult,
	plan Plan,
	freeze ExecutionFreeze,
) error {
	if stopped == nil {
		return nil
	}
	metrics, ok := namedPhaseMetrics(receipt.Measurements, stopped.Phase)
	if !ok {
		return errors.New("T42.2 stopped failure lacks phase measurements")
	}
	observation := stopped.Observation
	if slices.Contains([]string{"lifecycle_error", "internal_error"}, stopped.Code) {
		if err := validateFailureEvidenceProjection(receipt, *stopped, plan); err != nil {
			return err
		}
	}
	switch stopped.Code {
	case "phase_work_limit":
		index := slices.IndexFunc(plan.WorkEnvelope.Phases, func(value PhaseWorkBounds) bool {
			return value.Phase == stopped.Phase
		})
		if index < 0 {
			return errors.New("T42.2 stopped work envelope is absent")
		}
		for _, value := range boundedPhaseMetricValues(metrics, plan.WorkEnvelope.Phases[index]) {
			if counterObservationMatches(observation, value.name, value.bound.Maximum, value.value) {
				return nil
			}
		}
		for _, value := range []struct {
			name     string
			limit    uint64
			observed uint64
		}{
			{name: "child_processes", limit: plan.WorkEnvelope.MaximumChildProcessesPerPhase, observed: uint64(metrics.ChildProcesses)},
			{name: "max_lifecycle_deletes_in_any_turn", limit: plan.WorkEnvelope.MaximumLifecycleDeletesPerTurn, observed: uint64(metrics.MaxLifecycleDeletesTurn)},
			{name: "max_retries_on_any_unit", limit: plan.WorkEnvelope.MaximumRetriesPerUnit, observed: uint64(metrics.MaxRetriesUnit)},
			{name: "max_rows_in_any_transaction", limit: plan.WorkEnvelope.MaximumStoreRowsPerTransaction, observed: uint64(metrics.MaxRowsTransaction)},
		} {
			if counterObservationMatches(observation, value.name, value.limit, value.observed) {
				return nil
			}
		}
		return errors.New("T42.2 phase-work stop does not match a measured crossing")
	case "phase_deadline":
		index := slices.IndexFunc(plan.PhaseDeadlines, func(value PhaseDeadline) bool { return value.Phase == stopped.Phase })
		if index < 0 || !gaugeObservationMatches(
			observation, "phase_wall_ms", plan.PhaseDeadlines[index].DeadlineMS, uint64(metrics.WallMS),
		) {
			return errors.New("T42.2 phase-deadline stop does not match its measured crossing")
		}
	case "exact_oracle_mismatch":
		index := slices.IndexFunc(receipt.StateResults, func(value ExactPhaseEvidence) bool {
			return value.Phase == stopped.Phase
		})
		if index < 0 || receipt.StateResults[index].Outcome != "stopped" ||
			receipt.StateResults[index].ExpectedSHA256 != observation.ExpectedSHA256 ||
			receipt.StateResults[index].ObservedProjectionSHA256 != observation.ObservedSHA256 ||
			observation.ExpectedSHA256 == observation.ObservedSHA256 {
			return errors.New("T42.2 exact-oracle stop does not match its state evidence")
		}
	case "transition_mismatch":
		expected, err := transitionExpectationSHA256(stopped.Phase, plan, freeze)
		transition, ok := namedTransition(receipt.TransitionResults, stopped.Phase)
		authorityIndex := slices.IndexFunc(authorities, func(value AuthorityPhaseResult) bool {
			return value.Phase == stopped.Phase
		})
		if err != nil || observation.ExpectedSHA256 != expected || !ok || transition.FailureProjection == nil || authorityIndex < 0 {
			return errors.New("T42.2 transition stop does not match its frozen expectation")
		}
		observed, err := validateTransitionFailureProjection(
			*transition.FailureProjection, stopped.Phase,
			transition.StartEventOrdinal, transition.FinishEventOrdinal,
			authorities[authorityIndex], plan,
		)
		if err != nil || observation.ObservedSHA256 != observed {
			return errors.New("T42.2 transition stop lacks its bounded observed projection")
		}
	case "lifecycle_error":
		if !slices.Contains([]string{"pressure_80", "pressure_75", "lifecycle_collection"}, stopped.Phase) {
			return errors.New("T42.2 lifecycle error occurred outside a lifecycle phase")
		}
	case "measurement_unavailable", "internal_error", "peak_rss_ceiling", "observed_rss_ceiling", "data_allocated_ceiling", "data_logical_ceiling",
		"total_wall_ceiling", "multiple_resource_ceilings", "materialized_cartesian_owner_pairs_nonzero",
		"direct_recovery_topology_limit":
		// Measurement sentinels and resource/topology crossings are matched by
		// measurement validation and expectedStoppedDecision.
	default:
		return errors.New("T42.2 stopped failure code is not exhaustively validated")
	}
	return nil
}

func validateFailureEvidenceProjection(receipt Receipt, stopped ReceiptFailure, plan Plan) error {
	projection := stopped.Evidence
	measurementIndex := slices.IndexFunc(receipt.Measurements, func(value PhaseMeasurement) bool {
		return value.Phase == stopped.Phase
	})
	if projection == nil || measurementIndex < 0 ||
		projection.Schema != plan.ReceiptContract.FailureObservationSchema+"/public-projection-v1" {
		return errors.New("T42.2 typed failure lacks its public projection")
	}
	wantSHA256, err := receiptSHA256(*projection)
	if err != nil || stopped.Observation.ObservedSHA256 != wantSHA256 {
		return errors.New("T42.2 typed failure projection digest is invalid")
	}
	measurement := receipt.Measurements[measurementIndex]
	payloads := boolCount(projection.Lifecycle != nil) + boolCount(projection.Internal != nil)
	if payloads != 1 {
		return errors.New("T42.2 typed failure projection is not a closed union")
	}
	within := func(event uint64) bool {
		return orderedEventsWithin(measurement.StartEventOrdinal, measurement.FinishEventOrdinal, event)
	}
	switch stopped.Code {
	case "lifecycle_error":
		value := projection.Lifecycle
		if projection.Kind != "lifecycle" || value == nil || value.Phase != stopped.Phase ||
			!within(value.EventOrdinal) || !safeToken(value.ErrorClass, 64) ||
			!lifecycleFailureMatches(*value, plan) {
			return errors.New("T42.2 lifecycle failure projection is invalid")
		}
	case "internal_error":
		value := projection.Internal
		if projection.Kind != "internal" || value == nil || value.Phase != stopped.Phase ||
			!within(value.EventOrdinal) || !safeToken(value.Stage, 64) || !safeToken(value.ErrorClass, 64) {
			return errors.New("T42.2 internal failure projection is invalid")
		}
	default:
		return errors.New("T42.2 failure code has no public projection contract")
	}
	return nil
}

func lifecycleFailureMatches(value LifecycleFailureEvidence, plan Plan) bool {
	capacityValid := value.CapacityCompleteness == "exact" &&
		slices.Contains([]string{"normal", "collect", "refuse"}, value.CapacityPressure) ||
		value.CapacityCompleteness == "unavailable" && value.CapacityPressure == "unavailable"
	ownerError := value.Owner.Name != "" && slices.Contains(plan.WorkEnvelope.LifecycleOwners, value.Owner.Name) &&
		value.Owner.State == "error" && value.Owner.Completeness == string(lifecycle.Unavailable) &&
		value.Owner.Scanned <= uint64(lifecycle.MaxCandidatesPerTick) &&
		value.Owner.Deleted <= uint64(lifecycle.MaxDeletesPerTick) && value.Owner.Deleted <= value.Owner.Scanned &&
		value.Owner.LogicalBytes <= uint64(servicecatalogv3.MaxLogicalBytes) &&
		value.Owner.RootBytes <= uint64(servicecatalogv3.MaxRootBytes) &&
		value.Owner.MemberBytes <= uint64(servicecatalogv3.MaxMemberBytes) &&
		value.Owner.AttemptedAtUnixMS != 0
	capacityError := value.Owner == (LifecycleOwnerResult{}) &&
		value.CapacityCompleteness == "unavailable" && value.CapacityPressure == "unavailable"
	return capacityValid && (ownerError || capacityError)
}

func transitionExpectationSHA256(phase string, plan Plan, freeze ExecutionFreeze) (string, error) {
	stateDigests, err := expectedStateProjectionDigests(plan)
	if err != nil {
		return "", err
	}
	var failurePoint *FailurePoint
	if index := slices.IndexFunc(plan.FailurePoints, func(value FailurePoint) bool { return value.Phase == phase }); index >= 0 {
		value := plan.FailurePoints[index]
		failurePoint = &value
	}
	var pressureTarget *PressureTargetGeometry
	if index := slices.Index([]string{"pressure_80", "pressure_90", "pressure_75"}, phase); index >= 0 {
		if index >= len(freeze.Pressure.Targets) {
			return "", errors.New("T42.2 pressure transition expectation is absent")
		}
		value := freeze.Pressure.Targets[index]
		pressureTarget = &value
	}
	return receiptSHA256(struct {
		Schema         string                  `json:"schema"`
		Phase          string                  `json:"phase"`
		StateSHA256    string                  `json:"state_sha256"`
		FailurePoint   *FailurePoint           `json:"failure_point,omitempty"`
		PressureTarget *PressureTargetGeometry `json:"pressure_target,omitempty"`
	}{
		Schema: plan.ReceiptContract.TransitionSchema, Phase: phase,
		StateSHA256: stateDigests[phase], FailurePoint: failurePoint, PressureTarget: pressureTarget,
	})
}

func counterObservationMatches(value FailureObservation, metric string, limit, observed uint64) bool {
	return value.Kind == "counter_limit" && value.Metric == metric &&
		value.Limit == limit && value.Observed == observed && limit < math.MaxUint64 && observed == limit+1
}

func crossingObservationMatches(value FailureObservation, metric string, limit, observed uint64) bool {
	return value.Kind == "counter_crossing" && value.Metric == metric &&
		value.Limit == limit && value.Observed == observed && observed > limit
}

func gaugeObservationMatches(value FailureObservation, metric string, limit, observed uint64) bool {
	return value.Kind == "gauge_limit" && value.Metric == metric &&
		value.Limit == limit && value.Observed == observed && observed > limit
}

func frozenDecision(plan Plan, priority uint64) (string, uint64, error) {
	index := slices.IndexFunc(plan.StopRules, func(value StopRule) bool { return value.Priority == priority })
	if index < 0 {
		return "", 0, errors.New("T42.2 frozen decision priority is absent")
	}
	return plan.StopRules[index].Decision, priority, nil
}

func namedPhaseMetrics(values []PhaseMeasurement, phase string) (ReceiptMetrics, bool) {
	index := slices.IndexFunc(values, func(value PhaseMeasurement) bool { return value.Phase == phase })
	if index < 0 {
		return ReceiptMetrics{}, false
	}
	return values[index].Metrics, true
}

func measuredWallThrough(values []PhaseMeasurement, phase string) (uint64, error) {
	var total uint64
	for _, value := range values {
		wall := uint64(value.Metrics.WallMS)
		if wall > math.MaxUint64-total {
			return 0, errors.New("T42.2 wall measurement overflowed")
		}
		total += wall
		if value.Phase == phase {
			return total, nil
		}
	}
	return 0, errors.New("T42.2 stopped phase is absent from measurements")
}

func validateReceiptMeasurements(
	values []PhaseMeasurement,
	outcomes map[string]string,
	stopped *ReceiptFailure,
	teardown ReceiptTeardown,
	admissionEventOrdinal uint64,
	plan Plan,
	freeze ExecutionFreeze,
) error {
	wantMetrics := receiptMetricNames
	if correctedPlanSemantics(plan.Schema) {
		wantMetrics = correctedReceiptMetricNames()
	}
	if plan.Schema == PlanV3Schema {
		wantMetrics = accountingReceiptMetricNames()
	}
	if !slices.Equal(plan.ReceiptContract.RequiredMetrics, wantMetrics) ||
		len(values) != len(plan.PhaseOrder) || len(plan.PhaseDeadlines) != len(plan.PhaseOrder) ||
		len(plan.WorkEnvelope.Phases) != len(plan.PhaseOrder) {
		return errors.New("T42.2 measurement inventory differs from the frozen contract")
	}
	var total, preTeardownWall uint64
	priorFinishOrdinal := admissionEventOrdinal
	for index, phase := range plan.PhaseOrder {
		value := values[index]
		if value.Phase != phase || plan.PhaseDeadlines[index].Phase != phase ||
			plan.WorkEnvelope.Phases[index].Phase != phase {
			return errors.New("T42.2 measurements are not in phase order")
		}
		if outcomes[phase] == "not_run" {
			if value.StartEventOrdinal != 0 || value.FinishEventOrdinal != 0 ||
				value.Metrics != (ReceiptMetrics{}) || value.ChildProcessRoles != nil ||
				value.DispatchAccounting != nil || value.NativeObservation != nil {
				return errors.New("T42.2 not-run phase retained measurements")
			}
			continue
		}
		if value.StartEventOrdinal <= priorFinishOrdinal || value.FinishEventOrdinal <= value.StartEventOrdinal {
			return fmt.Errorf("T42.2 phase %q meter event order is invalid", phase)
		}
		priorFinishOrdinal = value.FinishEventOrdinal
		var observation *FailureObservation
		if stopped != nil && stopped.Phase == phase {
			observation = &stopped.Observation
		}
		unavailable := func(metric string) bool {
			return observation != nil && stopped.Code == "measurement_unavailable" &&
				observation.Kind == "measurement_unavailable" && slices.Contains(observation.UnavailableMetrics, metric)
		}
		teardownUnavailable := func(metric string) bool {
			return phase == "teardown" && teardown.Outcome == "failed" &&
				slices.Contains(teardown.MeasurementUnavailable, metric)
		}
		wallUnavailable := unavailable("wall_ms") || teardownUnavailable("wall_ms")
		availableUnavailable := unavailable("available_disk_bytes") || teardownUnavailable("available_disk_bytes")
		totalDiskUnavailable := unavailable("total_disk_bytes") || teardownUnavailable("total_disk_bytes")
		processUnavailable := unavailable("peak_rss_bytes") || teardownUnavailable("peak_rss_bytes")
		processInvalid := value.Metrics.ProcessMeasurementAvailable == processUnavailable ||
			(value.Metrics.PeakRSSBytes == 0) != processUnavailable
		if plan.Schema == PlanV3Schema {
			processInvalid = false
			if err := validateAccountingMeasurement(value, outcomes[phase], stopped, teardown, plan); err != nil {
				return fmt.Errorf("T42.2 phase %q process accounting: %w", phase, err)
			}
		}
		_, rssBytes := receiptRSSMetric(value.Metrics, plan.Schema)
		allocationUnavailable := unavailable("data_allocated_bytes") || teardownUnavailable("data_allocated_bytes")
		logicalUnavailable := unavailable("data_logical_bytes") || teardownUnavailable("data_logical_bytes")
		if (value.Metrics.WallMS == 0) != wallUnavailable ||
			(value.Metrics.AvailableDiskBytes == 0) != availableUnavailable ||
			totalDiskUnavailable && value.Metrics.TotalDiskBytes != 0 ||
			!totalDiskUnavailable && value.Metrics.TotalDiskBytes != Bytes(freeze.Host.PressureTotalDiskBytes) ||
			!availableUnavailable && !totalDiskUnavailable &&
				value.Metrics.AvailableDiskBytes > value.Metrics.TotalDiskBytes ||
			processInvalid ||
			!value.Metrics.AllocationMeasurementAvailable && !allocationUnavailable ||
			value.Metrics.AllocationMeasurementAvailable && allocationUnavailable ||
			value.Metrics.DataAllocatedBytes != 0 && allocationUnavailable ||
			value.Metrics.DataLogicalBytes != 0 && logicalUnavailable ||
			value.Metrics.DataLogicalBytes > Bytes(plan.WorkEnvelope.MaximumDataLogicalBytes) &&
				(observation == nil || stopped.Code != "data_logical_ceiling" ||
					!gaugeObservationMatches(*observation, "data_logical_bytes", plan.WorkEnvelope.MaximumDataLogicalBytes, uint64(value.Metrics.DataLogicalBytes))) {
			return fmt.Errorf("T42.2 phase %q measurement is unavailable or invalid", phase)
		}
		coldIndex := slices.Index(plan.PhaseOrder, "cold")
		if index >= coldIndex && phase != "teardown" &&
			((value.Metrics.DataAllocatedBytes == 0) != allocationUnavailable ||
				(value.Metrics.DataLogicalBytes == 0) != logicalUnavailable) {
			return fmt.Errorf("T42.2 phase %q lacks live data byte gauges", phase)
		}
		if outcomes[phase] == "passed" &&
			(rssBytes > plan.SafetyEnvelope.MaximumPeakRSSBytes ||
				value.Metrics.DataAllocatedBytes > Bytes(plan.SafetyEnvelope.MaximumDataAllocatedBytes) ||
				value.Metrics.MaterializedOwnerPairs != 0 || value.Metrics.DirectRecoveryLimits != 0 ||
				uint64(value.Metrics.WallMS) > plan.PhaseDeadlines[index].DeadlineMS) {
			return fmt.Errorf("T42.2 phase %q measurement is unavailable or over ceiling", phase)
		}
		if phase != "preflight" &&
			(value.Metrics.BallastCeilingBytes != 0 || value.Metrics.PressureCustodyMarginBytes != 0 ||
				value.Metrics.MinimumPrePressureUsedBytes != 0 || value.Metrics.MaximumPrePressureUsedBytes != 0 ||
				value.Metrics.MinimumPrePressureAllocatedBytes != 0 ||
				value.Metrics.MaximumPrePressureAllocatedBytes != 0 || value.Metrics.PressureVolumeBytes != 0) {
			return fmt.Errorf("T42.2 phase %q retained preflight pressure geometry", phase)
		}
		workOutcome := outcomes[phase]
		if phase == "teardown" && teardown.Outcome == "clean" {
			workOutcome = "passed"
		}
		if workOutcome == "passed" &&
			(rssBytes > plan.SafetyEnvelope.MaximumPeakRSSBytes ||
				value.Metrics.DataAllocatedBytes > Bytes(plan.SafetyEnvelope.MaximumDataAllocatedBytes) ||
				value.Metrics.DataLogicalBytes > Bytes(plan.WorkEnvelope.MaximumDataLogicalBytes)) {
			return fmt.Errorf("T42.2 phase %q clean work crossed a frozen gauge ceiling", phase)
		}
		roles := value.ChildProcessRoles
		if plan.Schema == PlanV3Schema {
			roles = value.DispatchAccounting.Roles
		}
		if err := validatePhaseWorkMetrics(value.Metrics, roles, plan.WorkEnvelope.Phases[index], workOutcome, observation, plan.WorkEnvelope); err != nil {
			return fmt.Errorf("T42.2 phase %q work: %w", phase, err)
		}
		wall := uint64(value.Metrics.WallMS)
		if wall > math.MaxUint64-total {
			return errors.New("T42.2 wall measurement overflowed")
		}
		total += wall
		if phase != "teardown" {
			preTeardownWall = total
		}
	}
	if preTeardownWall > plan.SafetyEnvelope.MaximumTotalWallMS && stopped == nil {
		return errors.New("T42.2 pre-teardown wall measurement crossed its ceiling")
	}
	if total > plan.SafetyEnvelope.MaximumTotalWallMS && stopped == nil && teardown.Outcome != "failed" {
		return errors.New("T42.2 total wall measurement crossed its ceiling")
	}
	if err := validateFrozenMetricOracles(values, outcomes, plan); err != nil {
		return err
	}
	if outcomes[plan.PhaseOrder[0]] == "passed" {
		fields := values[0].Metrics
		pressure := freeze.Pressure
		if uint64(fields.AvailableDiskBytes) != freeze.Host.PressureAvailableDiskBytes ||
			fields.TotalDiskBytes != Bytes(freeze.Host.PressureTotalDiskBytes) ||
			fields.MinimumPrePressureUsedBytes != Bytes(pressure.MinimumPrePressureUsedBytes) ||
			fields.MaximumPrePressureUsedBytes != Bytes(pressure.MaximumPrePressureUsedBytes) ||
			fields.MinimumPrePressureAllocatedBytes != Bytes(pressure.MinimumPrePressureBytes) ||
			fields.MaximumPrePressureAllocatedBytes != Bytes(pressure.MaximumPrePressureBytes) ||
			len(pressure.Targets) != 3 ||
			fields.BallastCeilingBytes != Bytes(pressure.BallastCeilingBytes) ||
			fields.PressureVolumeBytes != Bytes(pressure.PressureVolumeBytes) ||
			fields.PressureCustodyMarginBytes != Bytes(pressure.CustodyMarginBytes) ||
			pressure.CustodyMarginBytes < plan.SafetyEnvelope.MinimumPressureMarginBytes ||
			!pressure.SameVolume || pressure.DataVolumeIdentity != freeze.Host.DataVolumeIdentity ||
			pressure.BallastVolumeIdentity != freeze.Host.BallastVolumeIdentity ||
			pressure.BackingVolumeIdentity != freeze.Host.BackingVolumeIdentity {
			return errors.New("T42.2 passed preflight differs from the frozen pressure geometry")
		}
	}
	return nil
}

func validatePhaseWorkMetrics(
	metrics ReceiptMetrics,
	children []Count,
	bounds PhaseWorkBounds,
	outcome string,
	observation *FailureObservation,
	envelope WorkEnvelope,
) error {
	values := boundedPhaseMetricValues(metrics, bounds)
	for _, value := range values {
		if outcome == "passed" && value.value < value.bound.Minimum {
			return fmt.Errorf("%s is below its frozen minimum", value.name)
		}
		if value.value > value.bound.Maximum {
			if outcome != "stopped" || observation == nil ||
				observation.Kind != "counter_limit" || observation.Metric != value.name ||
				observation.Limit != value.bound.Maximum || observation.Observed != value.value ||
				value.bound.Maximum == math.MaxUint64 || value.value != value.bound.Maximum+1 {
				return fmt.Errorf("%s crossed its frozen maximum", value.name)
			}
		}
	}
	if metrics.CacheRootReads != metrics.CacheRootValidations ||
		metrics.CacheMemberReads != metrics.CacheMemberValidations ||
		uint64(metrics.CacheRootReads) > math.MaxUint64-uint64(metrics.CacheMemberReads) ||
		uint64(metrics.CacheMisses) != uint64(metrics.CacheRootReads)+uint64(metrics.CacheMemberReads) ||
		uint64(metrics.CacheHits) > math.MaxUint64-uint64(metrics.CacheMisses) ||
		uint64(metrics.CacheLookups) != uint64(metrics.CacheHits)+uint64(metrics.CacheMisses) {
		return errors.New("cache lookup, load, or validation accounting is incoherent")
	}
	if envelope.Schema == WorkEnvelopeV3Schema {
		if err := validateControlledDispatchCounts(metrics, children, bounds, envelope); err != nil {
			return err
		}
	} else {
		if len(children) != len(envelope.ChildProcessRoles) || len(bounds.ChildProcessRoles) != len(envelope.ChildProcessRoles) {
			return errors.New("child-process role inventory is incomplete")
		}
		var childTotal uint64
		for index, role := range envelope.ChildProcessRoles {
			roleBound := bounds.ChildProcessRoles[index]
			if children[index].Name != role || roleBound.Name != role ||
				outcome == "passed" && children[index].Count < roleBound.Minimum ||
				children[index].Count > roleBound.Maximum ||
				children[index].Count > envelope.MaximumChildProcessesPerPhase+1 ||
				children[index].Count > math.MaxUint64-childTotal {
				return errors.New("child-process role inventory is invalid")
			}
			childTotal += children[index].Count
		}
		if childTotal != uint64(metrics.ChildProcesses) || childTotal > envelope.MaximumChildProcessesPerPhase &&
			(outcome != "stopped" || observation == nil ||
				!counterObservationMatches(*observation, "child_processes", envelope.MaximumChildProcessesPerPhase, childTotal)) {
			return errors.New("child-process count crossed its frozen phase bound")
		}
	}
	if err := validateMeasuredMaximum("max_retries_on_any_unit", uint64(metrics.MaxRetriesUnit),
		envelope.MaximumRetriesPerUnit, outcome, observation); err != nil {
		return err
	}
	if err := validateMeasuredMaximum("max_rows_in_any_transaction", uint64(metrics.MaxRowsTransaction),
		envelope.MaximumStoreRowsPerTransaction, outcome, observation); err != nil {
		return err
	}
	if err := validateMeasuredMaximum("max_lifecycle_deletes_in_any_turn", uint64(metrics.MaxLifecycleDeletesTurn),
		envelope.MaximumLifecycleDeletesPerTurn, outcome, observation); err != nil {
		return err
	}
	if err := validateTotalAgainstMeasuredMaximum(
		"store rows", uint64(metrics.StoreRows), uint64(metrics.StoreTransactions), uint64(metrics.MaxRowsTransaction),
	); err != nil {
		return err
	}
	if err := validateTotalAgainstMeasuredMaximum(
		"retries", uint64(metrics.Retries), uint64(metrics.JobAttempts), uint64(metrics.MaxRetriesUnit),
	); err != nil {
		return err
	}
	if err := validateTotalAgainstMeasuredMaximum(
		"lifecycle deletion", uint64(metrics.LifecycleDeleted), uint64(metrics.LifecycleOwnerTurns), uint64(metrics.MaxLifecycleDeletesTurn),
	); err != nil {
		return err
	}
	return nil
}

type boundedPhaseMetric struct {
	name  string
	value uint64
	bound CounterBound
}

func boundedPhaseMetricValues(metrics ReceiptMetrics, bounds PhaseWorkBounds) []boundedPhaseMetric {
	return []boundedPhaseMetric{
		{"physical_corpus_passes", uint64(metrics.PhysicalCorpusPasses), bounds.PhysicalCorpusPasses},
		{"changed_physical_files", uint64(metrics.ChangedPhysicalFiles), bounds.ChangedPhysicalFiles},
		{"changed_logical_services", uint64(metrics.ChangedLogicalServices), bounds.ChangedLogicalServices},
		{"git_reads", uint64(metrics.GitReads), bounds.GitReads},
		{"census_children", uint64(metrics.CensusChildren), bounds.CensusChildren},
		{"census_records", uint64(metrics.CensusRecords), bounds.CensusRecords},
		{"index_files", uint64(metrics.IndexFiles), bounds.IndexFiles},
		{"observation_parses", uint64(metrics.ObservationParses), bounds.ObservationParses},
		{"source_logical_bytes", uint64(metrics.SourceLogicalBytes), bounds.SourceLogicalBytes},
		{"source_unique_bytes", uint64(metrics.SourceUniqueBytes), bounds.SourceUniqueBytes},
		{"applicable_partitions", uint64(metrics.ApplicablePartitions), bounds.ApplicablePartitions},
		{"published_domains", uint64(metrics.PublishedDomains), bounds.PublishedDomains},
		{"control_reads", uint64(metrics.ControlReads), bounds.ControlReads},
		{"member_reads", uint64(metrics.MemberReads), bounds.MemberReads},
		{"job_attempts", uint64(metrics.JobAttempts), bounds.JobAttempts},
		{"store_transactions", uint64(metrics.StoreTransactions), bounds.StoreTransactions},
		{"store_rows", uint64(metrics.StoreRows), bounds.StoreRows},
		{"cache_root_reads", uint64(metrics.CacheRootReads), bounds.CacheRootReads},
		{"cache_member_reads", uint64(metrics.CacheMemberReads), bounds.CacheMemberReads},
		{"cache_lookups", uint64(metrics.CacheLookups), bounds.CacheLookups},
		{"publication_writes", uint64(metrics.PublicationWrites), bounds.PublicationWrites},
		{"relationship_build_attempts", uint64(metrics.RelationshipBuildAttempts), bounds.RelationshipBuildAttempts},
		{"lifecycle_owner_turns", uint64(metrics.LifecycleOwnerTurns), bounds.LifecycleOwnerTurns},
		{"lifecycle_deleted", uint64(metrics.LifecycleDeleted), bounds.LifecycleDeleted},
		{"cache_hits", uint64(metrics.CacheHits), bounds.CacheHits},
		{"cache_misses", uint64(metrics.CacheMisses), bounds.CacheMisses},
		{"combined_physical_owners", uint64(metrics.CombinedPhysicalOwners), bounds.CombinedPhysicalOwners},
		{"logical_memberships", uint64(metrics.LogicalMemberships), bounds.LogicalMemberships},
		{"relationship_projections", uint64(metrics.RelationshipProjections), bounds.RelationshipProjections},
		{"resolver_blob_bytes", uint64(metrics.ResolverBlobBytes), bounds.ResolverBlobBytes},
		{"resolver_blob_reads", uint64(metrics.ResolverBlobReads), bounds.ResolverBlobReads},
		{"reuse_decisions", uint64(metrics.ReuseDecisions), bounds.ReuseDecisions},
		{"service_references", uint64(metrics.ServiceReferences), bounds.ServiceReferences},
		{"service_rows", uint64(metrics.ServiceRows), bounds.ServiceRows},
		{"unsupported_source_files", uint64(metrics.UnsupportedSourceFiles), bounds.UnsupportedSourceFiles},
	}
}

func validateMeasuredMaximum(name string, observed, limit uint64, outcome string, failure *FailureObservation) error {
	if observed <= limit {
		return nil
	}
	if outcome == "stopped" && failure != nil && failure.Kind == "counter_limit" &&
		failure.Metric == name && failure.Limit == limit && failure.Observed == observed &&
		limit < math.MaxUint64 && observed == limit+1 {
		return nil
	}
	return fmt.Errorf("%s crossed its frozen ceiling", name)
}

func validateTotalAgainstMeasuredMaximum(name string, total, units, maximum uint64) error {
	if total == 0 {
		if maximum != 0 {
			return fmt.Errorf("%s retained a high-water mark without work", name)
		}
		return nil
	}
	if units == 0 || maximum == 0 || units > math.MaxUint64/maximum || total > units*maximum {
		return fmt.Errorf("%s exceeds its measured per-unit envelope", name)
	}
	return nil
}

func validateFrozenMetricOracles(
	values []PhaseMeasurement,
	outcomes map[string]string,
	plan Plan,
) error {
	byPhase := make(map[string]ReceiptMetrics, len(values))
	for _, value := range values {
		byPhase[value.Phase] = value.Metrics
	}
	if outcomes["cold"] == "passed" {
		cold := byPhase["cold"]
		profile := plan.Profile
		if cold.CombinedPhysicalOwners != CountMetric(profile.Physical.CombinedPhysicalOwners) ||
			cold.LogicalMemberships != CountMetric(profile.Logical.Memberships) ||
			cold.RelationshipProjections != CountMetric(profile.Pipeline.RelationshipProjections) ||
			cold.ResolverBlobBytes != Bytes(profile.Pipeline.ResolverBlobBytesPerBuild) ||
			cold.ResolverBlobReads != CountMetric(profile.Pipeline.ResolverBlobReadsPerBuild) ||
			cold.RelationshipBuildAttempts != 1 ||
			cold.ServiceReferences != CountMetric(profile.Pipeline.ServiceReferences) ||
			cold.ServiceRows != CountMetric(profile.Logical.TotalServiceRecords) ||
			cold.SourceLogicalBytes != Bytes(profile.Bytes.CombinedLogicalSourceBytes) ||
			cold.SourceUniqueBytes != Bytes(profile.Bytes.CombinedUniqueContentBytesA) ||
			cold.UnsupportedSourceFiles != CountMetric(profile.Pipeline.UnsupportedSourceFiles) {
			return errors.New("T42.2 cold metrics differ from the frozen population and pipeline")
		}
	}
	for _, state := range plan.PhaseStates {
		if outcomes[state.Phase] != "passed" {
			continue
		}
		metrics := byPhase[state.Phase]
		wantSource := CountMetric(boolCount(state.SourceAction == "reuse"))
		wantSearch := CountMetric(boolCount(state.SearchAction == "reuse"))
		wantObservation := CountMetric(boolCount(state.ObservationAction == "reuse"))
		wantCatalog := CountMetric(boolCount(state.CatalogAction == "reuse"))
		wantRelationship := CountMetric(boolCount(state.RelationshipAction == "reuse"))
		if metrics.SourceReuseDecisions != wantSource || metrics.SearchReuseDecisions != wantSearch ||
			metrics.ObservationReuseDecisions != wantObservation || metrics.CatalogReuseDecisions != wantCatalog ||
			metrics.RelationshipReuseDecisions != wantRelationship ||
			metrics.ReuseDecisions != wantSource+wantSearch+wantObservation+wantCatalog+wantRelationship {
			return fmt.Errorf("T42.2 phase %q reuse lane evidence is invalid", state.Phase)
		}
		if state.SourceAction == "reuse" &&
			(metrics.GitReads != 0 || metrics.SourceLogicalBytes != 0 || metrics.SourceUniqueBytes != 0) {
			return fmt.Errorf("T42.2 phase %q repeated source work", state.Phase)
		}
		if state.ObservationAction == "reuse" && metrics.ObservationParses != 0 {
			return fmt.Errorf("T42.2 phase %q repeated observation parsing", state.Phase)
		}
		if state.RelationshipAction == "reuse" &&
			(metrics.RelationshipProjections != 0 || metrics.ResolverBlobBytes != 0 ||
				metrics.ResolverBlobReads != 0 || metrics.RelationshipBuildAttempts != 0) {
			return fmt.Errorf("T42.2 phase %q repeated relationship work", state.Phase)
		}
		if state.SourceAction == "reuse" && state.SearchAction == "reuse" &&
			state.ObservationAction == "reuse" && state.CatalogAction == "reuse" &&
			state.RelationshipAction == "reuse" && state.RecoveryPreparation == "" && metrics.PublicationWrites != 0 {
			return fmt.Errorf("T42.2 phase %q published during a complete no-op", state.Phase)
		}
	}
	return nil
}

func boolCount(value bool) uint64 {
	if value {
		return 1
	}
	return 0
}

func validateExactPhaseEvidence(
	values []ExactPhaseEvidence,
	phases []string,
	outcomes map[string]string,
	expected map[string]string,
	authorities []AuthorityPhaseResult,
	measurements []PhaseMeasurement,
	freeze ExecutionFreeze,
	stopped *ReceiptFailure,
	plan Plan,
) error {
	if len(values) != len(phases) {
		return errors.New("inventory is incomplete")
	}
	for index, phase := range phases {
		value := values[index]
		if value.Phase != phase || value.Outcome != outcomes[phase] ||
			value.ExpectedSHA256 != expected[phase] {
			return fmt.Errorf("phase %q binding is invalid", phase)
		}
		switch value.Outcome {
		case "passed":
			if value.Observed == nil {
				return fmt.Errorf("phase %q lacks its observed state projection", phase)
			}
			observedSHA256, observedErr := receiptSHA256(*value.Observed)
			authorityIndex := slices.IndexFunc(authorities, func(item AuthorityPhaseResult) bool { return item.Phase == phase })
			if authorityIndex < 0 {
				return fmt.Errorf("phase %q lacks native authority evidence", phase)
			}
			authority := authorities[authorityIndex]
			if observedErr != nil || value.Observed.Projection != nil ||
				value.Observed.ProjectionSHA256 != value.ExpectedSHA256 ||
				value.ObservedProjectionSHA256 != value.Observed.ProjectionSHA256 ||
				value.ObservedSHA256 != observedSHA256 ||
				!observedPhaseStateMatchesAuthority(*value.Observed, authority, plan.ReceiptContract.StateObservationSchema) {
				return fmt.Errorf("phase %q is not exact", phase)
			}
		case "stopped":
			if value.Observed == nil {
				if stopped != nil && stopped.Phase == phase && stopped.Code == "exact_oracle_mismatch" {
					return fmt.Errorf("phase %q exact-oracle stop lacks its observed state", phase)
				}
				if value.ObservedProjectionSHA256 != "" || value.ObservedSHA256 != "" {
					return fmt.Errorf("phase %q stopped observation is invalid", phase)
				}
				break
			}
			if value.Observed.Projection == nil {
				return fmt.Errorf("phase %q exact-oracle stop lacks its observed projection", phase)
			}
			projectionSHA256, projectionErr := receiptSHA256(*value.Observed.Projection)
			observedSHA256, observedErr := receiptSHA256(*value.Observed)
			expectedProjection, expectedProjectionErr := expectedStateProjectionForPhase(plan, phase)
			authorityIndex := slices.IndexFunc(authorities, func(item AuthorityPhaseResult) bool { return item.Phase == phase })
			if projectionErr != nil || observedErr != nil || expectedProjectionErr != nil ||
				authorityIndex < 0 || stopped == nil || stopped.Phase != phase || stopped.Code != "exact_oracle_mismatch" ||
				value.Observed.ProjectionSHA256 != projectionSHA256 ||
				value.ObservedProjectionSHA256 != value.Observed.ProjectionSHA256 ||
				value.ObservedSHA256 != observedSHA256 ||
				!validDiagnosticProjection(*value.Observed.Projection, expectedProjection) ||
				!observedPhaseStateMatchesAuthority(*value.Observed, authorities[authorityIndex], plan.ReceiptContract.StateObservationSchema) {
				return fmt.Errorf("phase %q stopped observation is invalid", phase)
			}
		case "not_run":
			if value.ObservedProjectionSHA256 != "" || value.ObservedSHA256 != "" || value.Observed != nil {
				return fmt.Errorf("phase %q retained a not-run observation", phase)
			}
		default:
			return fmt.Errorf("phase %q outcome is invalid", phase)
		}
	}
	return validatePhaseRuntimeBindings(values, phases, outcomes, measurements, freeze)
}

func observedPhaseStateMatchesAuthority(
	value ObservedPhaseState,
	authority AuthorityPhaseResult,
	schema string,
) bool {
	semanticReader := semanticReaderForObservationSchema(schema)
	authoritySHA256, err := authoritySnapshotSHA256(authority.AuthorityState)
	if err != nil || semanticReader == "" || value.Schema != schema || !validDigest(value.ProjectionSHA256) ||
		!slices.Contains([]string{"passed", "stopped"}, authority.Outcome) || !authority.Current ||
		value.AuthoritySnapshotSHA256 != authoritySHA256 ||
		value.SourceAuthorityRecipe != "source-generation-and-authored-tree-recipe-v1" ||
		value.AuthorityReader != "current-root-then-generation-then-exact-member-inventory-v1" ||
		value.SemanticReader != semanticReader {
		return false
	}
	if value.Projection == nil {
		return true
	}
	return value.Projection.PhysicalRevision == authority.PhysicalRevision &&
		value.Projection.LogicalRevision == authority.LogicalRevision &&
		value.Projection.SearchInventory == authority.SearchInventory &&
		value.Projection.ObservationInputInventory == authority.ObservationInputInventory
}

func semanticReaderForObservationSchema(schema string) string {
	if schema == "t422-observed-phase-state-v4" {
		return "authorized-product-reader-canonical-projection-v1"
	}
	if schema == "t422-observed-phase-state-v5" || schema == "t422-observed-phase-state-v6" {
		return "private-exact-current-authorized-canonical-projection-v1"
	}
	return ""
}

func validDiagnosticProjection(value, frozen PhaseStateProjection) bool {
	if value.Schema != frozen.Schema || value.Phase != frozen.Phase ||
		value.PhysicalRevision != frozen.PhysicalRevision || value.LogicalRevision != frozen.LogicalRevision ||
		!optionalDigest(value.CatalogLogicalSHA256) || !optionalDigest(value.SemanticSHA256) ||
		value.CatalogSource.Schema != frozen.CatalogSource.Schema || !optionalDigest(value.CatalogSource.SHA256) {
		return false
	}
	sets := []SetIdentity{
		value.Catalog, value.MembershipSet, value.Placements, value.UnownedPrefixes,
		value.ServiceQueries, value.SearchInventory, value.ObservationInputInventory,
	}
	for _, set := range sets {
		if set != (SetIdentity{}) && !validPossiblyEmptySetIdentity(set) {
			return false
		}
	}
	if len(value.ExtractionRoots) != len(frozen.ExtractionRoots) ||
		len(value.RelationshipResults) != len(frozen.RelationshipResults) {
		return false
	}
	for index, root := range value.ExtractionRoots {
		want := frozen.ExtractionRoots[index]
		if root.Domain != want.Domain || root.Availability != want.Availability ||
			!optionalDigest(root.TypedScopeSHA256) || !optionalDigest(root.TypedScopeContentSHA256) ||
			root.Candidates != (SetIdentity{}) && !validPossiblyEmptySetIdentity(root.Candidates) ||
			root.PartitionShape != (SetIdentity{}) && !validPossiblyEmptySetIdentity(root.PartitionShape) {
			return false
		}
	}
	for index, relationship := range value.RelationshipResults {
		if relationship.Name != frozen.RelationshipResults[index].Name ||
			!optionalDigest(relationship.ObservedEdgesSHA256) {
			return false
		}
	}
	return value.ProductRelationship.Canonicalization == frozen.ProductRelationship.Canonicalization &&
		optionalDigest(value.ProductRelationship.ProjectionSHA256)
}

func validateTransitionResults(
	values []TransitionResult,
	outcomes map[string]string,
	stopped *ReceiptFailure,
	authorities []AuthorityPhaseResult,
	measurements []PhaseMeasurement,
	plan Plan,
	freeze ExecutionFreeze,
) error {
	if len(values) != len(transitionPhases) {
		return errors.New("inventory is incomplete")
	}
	authority := make(map[string]AuthorityPhaseResult, len(authorities))
	for _, value := range authorities {
		authority[value.Phase] = value
	}
	metrics := make(map[string]ReceiptMetrics, len(measurements))
	phaseMeasurements := make(map[string]PhaseMeasurement, len(measurements))
	for _, value := range measurements {
		metrics[value.Phase] = value.Metrics
		phaseMeasurements[value.Phase] = value
	}
	for index, phase := range transitionPhases {
		value := values[index]
		if value.Phase != phase || value.Outcome != outcomes[phase] {
			return fmt.Errorf("phase %q binding is invalid", phase)
		}
		measurement, ok := phaseMeasurements[phase]
		if !ok {
			return fmt.Errorf("phase %q measurement is absent", phase)
		}
		if value.Outcome == "not_run" {
			if value.StartEventOrdinal != 0 || value.FinishEventOrdinal != 0 {
				return fmt.Errorf("phase %q retained not-run transition events", phase)
			}
		} else if value.StartEventOrdinal != measurement.StartEventOrdinal ||
			value.FinishEventOrdinal != measurement.FinishEventOrdinal ||
			value.StartEventOrdinal >= value.FinishEventOrdinal {
			return fmt.Errorf("phase %q transition is outside its phase meter", phase)
		}
		payloads := boolCount(len(value.Injections) != 0) + boolCount(value.Pressure != nil) +
			boolCount(value.Archive != nil) + boolCount(value.Reader != nil) + boolCount(value.Lifecycle != nil)
		if value.Outcome != "passed" {
			if value.ReadAccounting != nil {
				return fmt.Errorf("phase %q retained incomplete transition read accounting", phase)
			}
			if payloads != 0 {
				return fmt.Errorf("phase %q retained an incomplete transition", phase)
			}
			needsProjection := value.Outcome == "stopped" && stopped != nil && stopped.Phase == phase && stopped.Code == "transition_mismatch"
			if needsProjection {
				if value.FailureProjection == nil {
					return fmt.Errorf("phase %q lacks its failed transition projection", phase)
				}
				observed, err := validateTransitionFailureProjection(
					*value.FailureProjection, phase, value.StartEventOrdinal, value.FinishEventOrdinal, authority[phase], plan,
				)
				if err != nil || observed != stopped.Observation.ObservedSHA256 {
					return fmt.Errorf("phase %q failed transition projection is invalid", phase)
				}
			} else if value.FailureProjection != nil {
				return fmt.Errorf("phase %q retained unrelated failed transition evidence", phase)
			}
			continue
		}
		if value.FailureProjection != nil {
			return fmt.Errorf("phase %q retained failed evidence after passing", phase)
		}
		if payloads != 1 {
			return fmt.Errorf("phase %q lacks one exact transition", phase)
		}
		if plan.Schema == PlanSchema {
			if value.ReadAccounting != nil {
				return fmt.Errorf("phase %q added transition read accounting to v1", phase)
			}
		} else if phase == "physical_delta_b" {
			if value.ReadAccounting == nil || validatePhysicalTransitionReadSubtotal(*value.ReadAccounting, plan) != nil {
				return errors.New("physical delta transition read accounting is invalid")
			}
		} else if phase == "logical_delta_b" {
			if value.ReadAccounting == nil || validateLogicalTransitionReadSubtotal(*value.ReadAccounting) != nil {
				return errors.New("logical delta transition read accounting is invalid")
			}
		} else if phase == "return_a" {
			if value.ReadAccounting == nil || validateReturnTransitionReadSubtotal(*value.ReadAccounting) != nil {
				return errors.New("return transition read accounting is invalid")
			}
		} else if phase == "stale_lease" {
			if value.ReadAccounting == nil || validateStaleLeaseTransitionReadSubtotal(*value.ReadAccounting) != nil {
				return errors.New("stale lease transition read accounting is invalid")
			}
		} else if phase == "process_restart" {
			if value.ReadAccounting == nil || validateCheckpointRestartReadSubtotal(*value.ReadAccounting) != nil {
				return errors.New("checkpoint restart transition read accounting is invalid")
			}
		} else if phase == "pressure_80" {
			if value.ReadAccounting == nil || validatePressure80TransitionReadSubtotal(*value.ReadAccounting) != nil {
				return errors.New("pressure 80 transition read accounting is invalid")
			}
		} else if phase == "pressure_90" {
			if value.ReadAccounting == nil || validatePressure90TransitionReadSubtotal(*value.ReadAccounting) != nil {
				return errors.New("pressure 90 transition read accounting is invalid")
			}
		} else if phase == "pressure_75" {
			if value.ReadAccounting == nil || validatePressure75TransitionReadSubtotal(*value.ReadAccounting) != nil {
				return errors.New("pressure 75 transition read accounting is invalid")
			}
		} else if phase == "archive_restore" {
			if value.ReadAccounting == nil || validateArchiveTransitionReadSubtotal(*value.ReadAccounting) != nil {
				return errors.New("archive transition read accounting is invalid")
			}
		} else if phase == "lifecycle_collection" {
			if value.ReadAccounting == nil || validateLifecycleTransitionReadSubtotal(*value.ReadAccounting) != nil {
				return errors.New("lifecycle transition read accounting is invalid")
			}
		} else if value.ReadAccounting != nil {
			return fmt.Errorf("phase %q claims unfinished transition read accounting", phase)
		}
		switch phase {
		case "physical_delta_b":
			if value.Reader == nil || validateReaderTransition(
				*value.Reader, value.StartEventOrdinal, value.FinishEventOrdinal, authority, metrics[phase], plan,
			) != nil {
				return errors.New("physical delta reader transition is invalid")
			}
		case "logical_delta_b", "return_a", "stale_lease", "process_restart":
			points := failurePointsForPhase(plan.FailurePoints, phase)
			if len(value.Injections) != len(points) {
				return fmt.Errorf("phase %q injection transition is invalid", phase)
			}
			for injectionIndex, point := range points {
				if err := validateInjectionTransition(
					point, value.Injections[injectionIndex], value.StartEventOrdinal, value.FinishEventOrdinal,
					authority, metrics[phase], measurement.ChildProcessRoles, plan, freeze,
				); err != nil {
					return fmt.Errorf("phase %q injection %q is invalid: %w", phase, point.Name, err)
				}
			}
		case "pressure_80", "pressure_90", "pressure_75":
			if value.Pressure == nil {
				return fmt.Errorf("phase %q pressure transition is absent", phase)
			}
		case "archive_restore":
			if value.Archive == nil || validateArchiveTransition(
				*value.Archive, value.StartEventOrdinal, value.FinishEventOrdinal, authority, plan,
			) != nil {
				return errors.New("archive/restore transition is invalid")
			}
		case "lifecycle_collection":
			if value.Lifecycle == nil || validateLifecycleTransition(
				*value.Lifecycle, value.StartEventOrdinal, value.FinishEventOrdinal, authority, metrics[phase], plan,
			) != nil {
				return errors.New("lifecycle transition is invalid")
			}
		}
	}
	seenInjectionTargets := make(map[string]struct{}, 3*len(plan.FailurePoints))
	seenFailurePoints := make(map[string]struct{}, len(plan.FailurePoints))
	for _, point := range plan.FailurePoints {
		transition, ok := namedTransition(values, point.Phase)
		if !ok || transition.Outcome != "passed" {
			continue
		}
		index := slices.IndexFunc(transition.Injections, func(value InjectionTransition) bool { return value.FailurePoint == point.Name })
		if index < 0 {
			return errors.New("failure injection result is absent")
		}
		injection := transition.Injections[index]
		if _, duplicate := seenFailurePoints[point.Name]; duplicate {
			return errors.New("failure injection result is duplicated")
		}
		seenFailurePoints[point.Name] = struct{}{}
		for _, target := range []string{injection.StableTargetSHA256, injection.TargetIdentitySHA256, injection.TargetSHA256} {
			if _, duplicate := seenInjectionTargets[target]; duplicate {
				return errors.New("failure injections reused a target identity or binding")
			}
			seenInjectionTargets[target] = struct{}{}
		}
	}
	if err := validateRecoveryPreparationLineage(values, authority, plan); err != nil {
		return err
	}
	if outcomes["pressure_80"] == "passed" || outcomes["pressure_90"] == "passed" || outcomes["pressure_75"] == "passed" {
		if err := validatePressureTransitions(values, outcomes, authority, metrics, plan, freeze); err != nil {
			return err
		}
	}
	if outcomes["lifecycle_collection"] == "passed" && outcomes["pressure_75"] == "passed" {
		pressure75, _ := namedTransition(values, "pressure_75")
		lifecycleValue, _ := namedTransition(values, "lifecycle_collection")
		if lifecycleValue.Lifecycle.LifecycleFenceUnixMS <= pressure75.Pressure.CapacityObservedUnixMS {
			return errors.New("final lifecycle cycle is not fresh after pressure recovery")
		}
	}
	return nil
}

func failurePointsForPhase(values []FailurePoint, phase string) []FailurePoint {
	result := make([]FailurePoint, 0, 2)
	for _, value := range values {
		if value.Phase == phase {
			result = append(result, value)
		}
	}
	return result
}

func validateTransitionFailureProjection(
	value TransitionFailureProjection,
	phase string,
	startEventOrdinal, finishEventOrdinal uint64,
	authority AuthorityPhaseResult,
	plan Plan,
) (string, error) {
	boundary := map[string]string{
		"physical_delta_b":     "reader_lease_replace_release",
		"pressure_80":          "pressure_gate_and_lifecycle_fence",
		"pressure_90":          "pressure_gate",
		"pressure_75":          "pressure_recovery_and_lifecycle_fence",
		"archive_restore":      "archive_restore_compare",
		"lifecycle_collection": "fresh_owner_cycle",
	}[phase]
	if correctedPlanSemantics(plan.Schema) && phase == "physical_delta_b" {
		boundary = "reader_lease_current_prior_release"
	}
	if value.FailurePoint != "" {
		index := slices.IndexFunc(plan.FailurePoints, func(point FailurePoint) bool {
			return point.Phase == phase && point.Name == value.FailurePoint
		})
		if index < 0 {
			return "", errors.New("failed transition names an unknown failure point")
		}
		boundary = plan.FailurePoints[index].Boundary
	}
	projectedAuthority := value.Authority
	projectedAuthority.ExtractionRoots = nil
	expectedAuthority := authority
	expectedAuthority.ExtractionRoots = nil
	if boundary == "" || value.Schema != plan.ReceiptContract.TransitionSchema+"/failed-projection-v1" ||
		value.Phase != phase || value.Boundary != boundary || !validTransitionStep(plan.Schema, phase, value.LastCompletedStep) ||
		!orderedEventsWithin(startEventOrdinal, finishEventOrdinal, value.EventOrdinal) ||
		!reflect.DeepEqual(projectedAuthority, expectedAuthority) {
		return "", errors.New("failed transition projection is invalid")
	}
	return receiptSHA256(value)
}

func validTransitionStep(planSchema, phase, step string) bool {
	steps := map[string][]string{
		"physical_delta_b":     {"lease_acquired", "new_current", "held_retirement", "lease_released", "post_release_retirement", "deleted"},
		"logical_delta_b":      {"armed", "hit", "recovered", "cleared"},
		"return_a":             {"publication_armed", "publication_hit", "publication_recovered", "publication_cleared"},
		"stale_lease":          {"armed", "hit", "requeued", "completed", "cleared"},
		"process_restart":      {"checkpoint_armed", "checkpoint_hit", "process_stopped", "process_started", "checkpoint_recovered", "checkpoint_cleared"},
		"pressure_80":          {"lifecycle_fenced", "capacity_observed", "ballast_mutated", "gate_observed"},
		"pressure_90":          {"ballast_mutated", "gate_observed"},
		"pressure_75":          {"ballast_mutated", "gate_observed", "recovery_ballast_removed", "lifecycle_fenced", "capacity_observed", "recovery_gate_observed"},
		"archive_restore":      {"archive_created", "installation_destroyed", "empty_restore_target_observed", "restore_started", "comparison_completed"},
		"lifecycle_collection": {"lifecycle_fenced", "capacity_observed"},
	}
	if correctedPlanSemantics(planSchema) {
		steps["physical_delta_b"] = []string{
			"lease_acquired", "new_current", "held_lifecycle", "old_queried", "new_queried",
			"lease_released", "post_release_lifecycle", "post_release_old_queried",
		}
	}
	return slices.Contains(steps[phase], step)
}

func orderedEventsWithin(start, finish uint64, events ...uint64) bool {
	previous := start
	for _, event := range events {
		if event <= previous || event >= finish {
			return false
		}
		previous = event
	}
	return true
}

func validateReaderTransition(
	value ReaderTransition,
	startEventOrdinal, finishEventOrdinal uint64,
	authority map[string]AuthorityPhaseResult,
	metrics ReceiptMetrics,
	plan Plan,
) error {
	before, beforeOK := authorityIdentitySHA256(authority["warm_noop"])
	after, afterOK := authorityIdentitySHA256(authority["physical_delta_b"])
	commonError := value.Reader != plan.ReaderProbe.Reader || value.QuerySHA256 != plan.ReaderProbe.QuerySHA256 ||
		!beforeOK || !afterOK || value.OldSearchGenerationSHA256 != authority["warm_noop"].SearchGenerationSHA256 ||
		value.NewSearchGenerationSHA256 != authority["physical_delta_b"].SearchGenerationSHA256 ||
		value.OldSearchGenerationSHA256 == value.NewSearchGenerationSHA256 ||
		value.OldHeldRecords != plan.ReaderProbe.ExpectedRecords || value.NewHeldRecords != plan.ReaderProbe.ExpectedRecords ||
		value.OldHeldProjectionSHA256 != plan.ReaderProbe.OldProjectionSHA256 ||
		value.NewHeldProjectionSHA256 != plan.ReaderProbe.NewProjectionSHA256 ||
		value.OldHeldProjectionSHA256 == value.NewHeldProjectionSHA256 ||
		value.LeaseAcquired != 1 || !value.OldVisibleWhileHeld || !value.NewCurrentWhileHeld || value.LeaseReleased != 1 ||
		value.AuthorityBeforeSHA256 != before || value.AuthorityAfterSHA256 != after
	if commonError {
		return errors.New("reader facts differ from the physical A-to-B replacement")
	}
	if plan.Schema == PlanSchema {
		returnError := value.Schema != plan.ReceiptContract.TransitionSchema+"/reader-v1" ||
			value.PostDeleteOldGenerationOutcome != plan.ReaderProbe.PostDeleteOutcome ||
			value.RetirementAttemptsWhileHeld != 1 || value.ProtectedWhileHeld != 1 || value.LeaseReleased != 1 ||
			value.RetirementAttemptsAfterRelease != 1 || value.DeletedAfterRelease != 1 ||
			!orderedEventsWithin(startEventOrdinal, finishEventOrdinal,
				value.LeaseAcquireEventOrdinal, value.NewCurrentEventOrdinal, value.HeldRetirementEventOrdinal,
				value.OldHeldQueryEventOrdinal, value.NewHeldQueryEventOrdinal, value.LeaseReleaseEventOrdinal,
				value.PostReleaseRetirementOrdinal, value.DeleteEventOrdinal, value.PostDeleteProbeEventOrdinal,
			) ||
			metrics.LifecycleOwnerTurns != 2 || metrics.LifecycleDeleted != 1 || metrics.MaxLifecycleDeletesTurn != 1 ||
			value.OldRoleAfterReplacement != "" || value.NewRoleAfterReplacement != "" ||
			value.LifecycleAttemptsWhileHeld != 0 || value.OldRootProtectedWhileHeld != 0 ||
			value.HeldLifecycleScanned != nil || value.HeldLifecycleOutcome != "" ||
			value.LifecycleAttemptsAfterRelease != 0 || value.OldRootProtectedAfterRelease != 0 ||
			value.PostReleaseLifecycleScanned != nil || value.PostReleaseLifecycleOutcome != "" ||
			value.PostReleaseOldRecords != 0 || value.PostReleaseOldProjectionSHA256 != "" ||
			value.PostReleaseOldOutcome != "" || value.OldReaderHeldThroughReprobe || value.HeldLifecycleEventOrdinal != 0 ||
			value.PostReleaseLifecycleOrdinal != 0 || value.PostReleaseOldQueryOrdinal != 0
		if returnError {
			return errors.New("reader lease facts differ from the physical A-to-B replacement")
		}
		return nil
	}
	returnError := value.Schema != plan.ReceiptContract.TransitionSchema+"/reader-v2" ||
		value.PostDeleteOldGenerationOutcome != "" || value.RetirementAttemptsWhileHeld != 0 ||
		value.ProtectedWhileHeld != 0 || value.RetirementAttemptsAfterRelease != 0 || value.DeletedAfterRelease != 0 ||
		value.HeldRetirementEventOrdinal != 0 || value.PostReleaseRetirementOrdinal != 0 ||
		value.DeleteEventOrdinal != 0 || value.PostDeleteProbeEventOrdinal != 0 ||
		value.OldRoleAfterReplacement != plan.ReaderProbe.OldRoleAfterReplacement ||
		value.NewRoleAfterReplacement != plan.ReaderProbe.NewRoleAfterReplacement ||
		value.LifecycleAttemptsWhileHeld != 1 || value.OldRootProtectedWhileHeld != 1 ||
		value.HeldLifecycleScanned == nil || *value.HeldLifecycleScanned != 0 || value.HeldLifecycleOutcome != "exact_drained" ||
		value.LifecycleAttemptsAfterRelease != 1 || value.OldRootProtectedAfterRelease != 1 ||
		value.PostReleaseLifecycleScanned == nil || *value.PostReleaseLifecycleScanned != 0 || value.PostReleaseLifecycleOutcome != "exact_drained" ||
		value.PostReleaseOldRecords != plan.ReaderProbe.ExpectedRecords ||
		value.PostReleaseOldProjectionSHA256 != plan.ReaderProbe.OldProjectionSHA256 ||
		value.PostReleaseOldOutcome != plan.ReaderProbe.PostReleaseOutcome || !value.OldReaderHeldThroughReprobe ||
		!orderedEventsWithin(startEventOrdinal, finishEventOrdinal,
			value.LeaseAcquireEventOrdinal, value.NewCurrentEventOrdinal, value.HeldLifecycleEventOrdinal,
			value.OldHeldQueryEventOrdinal, value.NewHeldQueryEventOrdinal, value.LeaseReleaseEventOrdinal,
			value.PostReleaseLifecycleOrdinal, value.PostReleaseOldQueryOrdinal,
		) ||
		metrics.LifecycleOwnerTurns != 2 || metrics.LifecycleDeleted != 0 || metrics.MaxLifecycleDeletesTurn != 0
	if returnError {
		return errors.New("reader current/prior facts differ from the physical A-to-B replacement")
	}
	return nil
}

func validatePhysicalTransitionReadSubtotal(value TransitionReadSubtotal, plan Plan) error {
	bound, err := correctedPhysicalTransitionReadBound(plan.Profile)
	if err != nil || value.Schema != "t422-transition-read-accounting-v1" ||
		value.Class != correctedPhysicalTransitionReadClass || value.ReportCalls != bound.Calls.Minimum ||
		value.ControlFileReads != bound.ControlFileReads.Minimum ||
		value.StoreReadAttempts != bound.StoreReadAttempts.Minimum ||
		value.MemberReads != bound.MemberReads.Minimum ||
		value.StoreWriteAttempts != bound.StoreWriteAttempts.Minimum ||
		value.StoreReadAttempts > math.MaxUint64-value.ControlFileReads {
		return errors.New("physical transition read subtotal differs from its derived bound")
	}
	return nil
}

func validateLogicalTransitionReadSubtotal(value TransitionReadSubtotal) error {
	bound, err := correctedLogicalTransitionReadBound()
	if err != nil || value.Schema != "t422-transition-read-accounting-v1" ||
		value.Class != correctedLogicalTransitionReadClass || value.ReportCalls != bound.Calls.Minimum ||
		value.ControlFileReads != bound.ControlFileReads.Minimum ||
		value.StoreReadAttempts != bound.StoreReadAttempts.Minimum ||
		value.MemberReads != bound.MemberReads.Minimum ||
		value.StoreWriteAttempts != bound.StoreWriteAttempts.Minimum ||
		value.StoreReadAttempts > math.MaxUint64-value.ControlFileReads {
		return errors.New("logical transition read subtotal differs from its derived bound")
	}
	return nil
}

func validateReturnTransitionReadSubtotal(value TransitionReadSubtotal) error {
	bound, err := correctedReturnTransitionReadBound()
	if err != nil || value.Schema != "t422-transition-read-accounting-v1" ||
		value.Class != correctedReturnTransitionReadClass || value.ReportCalls != bound.Calls.Minimum ||
		value.ControlFileReads != bound.ControlFileReads.Minimum ||
		value.StoreReadAttempts != bound.StoreReadAttempts.Minimum ||
		value.MemberReads != bound.MemberReads.Minimum ||
		value.StoreWriteAttempts != bound.StoreWriteAttempts.Minimum ||
		value.StoreReadAttempts > math.MaxUint64-value.ControlFileReads {
		return errors.New("return transition read subtotal differs from its derived bound")
	}
	return nil
}

func validateStaleLeaseTransitionReadSubtotal(value TransitionReadSubtotal) error {
	bound, err := correctedStaleLeaseTransitionReadBound()
	if err != nil || value.Schema != "t422-transition-read-accounting-v1" ||
		value.Class != correctedStaleLeaseTransitionReadClass || value.ReportCalls != bound.Calls.Minimum ||
		value.ControlFileReads != bound.ControlFileReads.Minimum ||
		value.StoreReadAttempts != bound.StoreReadAttempts.Minimum ||
		value.MemberReads != bound.MemberReads.Minimum ||
		value.StoreWriteAttempts != bound.StoreWriteAttempts.Minimum ||
		value.StoreReadAttempts > math.MaxUint64-value.ControlFileReads {
		return errors.New("stale lease transition read subtotal differs from its derived bound")
	}
	return nil
}

func validateCheckpointRestartReadSubtotal(value TransitionReadSubtotal) error {
	bound, err := correctedCheckpointRestartReadBound()
	if err != nil || value.Schema != "t422-transition-read-accounting-v1" ||
		value.Class != correctedCheckpointRestartReadClass || value.ReportCalls != bound.Calls.Minimum ||
		value.ControlFileReads != bound.ControlFileReads.Minimum ||
		value.StoreReadAttempts != bound.StoreReadAttempts.Minimum ||
		value.MemberReads != bound.MemberReads.Minimum ||
		value.StoreWriteAttempts != bound.StoreWriteAttempts.Minimum ||
		value.StoreReadAttempts > math.MaxUint64-value.ControlFileReads {
		return errors.New("checkpoint restart transition read subtotal differs from its derived bound")
	}
	return nil
}

func validatePressure80TransitionReadSubtotal(value TransitionReadSubtotal) error {
	bound := correctedPressure80TransitionReadBound()
	if value.Schema != "t422-transition-read-accounting-v1" ||
		value.Class != correctedPressure80TransitionReadClass || value.ReportCalls != bound.Calls.Minimum ||
		value.ControlFileReads != bound.ControlFileReads.Minimum ||
		value.StoreReadAttempts != bound.StoreReadAttempts.Minimum ||
		value.MemberReads != bound.MemberReads.Minimum ||
		value.StoreWriteAttempts != bound.StoreWriteAttempts.Minimum ||
		value.StoreReadAttempts > math.MaxUint64-value.ControlFileReads {
		return errors.New("pressure 80 transition read subtotal differs from its derived bound")
	}
	return nil
}

func validatePressure90TransitionReadSubtotal(value TransitionReadSubtotal) error {
	bound := correctedPressure90TransitionReadBound()
	if value.Schema != "t422-transition-read-accounting-v1" ||
		value.Class != correctedPressure90TransitionReadClass || value.ReportCalls != bound.Calls.Minimum ||
		value.ControlFileReads != bound.ControlFileReads.Minimum ||
		value.StoreReadAttempts != bound.StoreReadAttempts.Minimum ||
		value.MemberReads != bound.MemberReads.Minimum ||
		value.StoreWriteAttempts != bound.StoreWriteAttempts.Minimum ||
		value.StoreReadAttempts > math.MaxUint64-value.ControlFileReads {
		return errors.New("pressure 90 transition read subtotal differs from its derived bound")
	}
	return nil
}

func validatePressure75TransitionReadSubtotal(value TransitionReadSubtotal) error {
	bound := correctedPressure75TransitionReadBound()
	if value.Schema != "t422-transition-read-accounting-v1" ||
		value.Class != correctedPressure75TransitionReadClass || value.ReportCalls != bound.Calls.Minimum ||
		value.ControlFileReads != bound.ControlFileReads.Minimum ||
		value.StoreReadAttempts != bound.StoreReadAttempts.Minimum ||
		value.MemberReads != bound.MemberReads.Minimum ||
		value.StoreWriteAttempts != bound.StoreWriteAttempts.Minimum ||
		value.StoreReadAttempts > math.MaxUint64-value.ControlFileReads {
		return errors.New("pressure 75 transition read subtotal differs from its derived bound")
	}
	return nil
}

func validateArchiveTransitionReadSubtotal(value TransitionReadSubtotal) error {
	bound := correctedArchiveTransitionReadBound()
	if value.Schema != "t422-transition-read-accounting-v1" ||
		value.Class != correctedArchiveTransitionReadClass || value.ReportCalls != bound.Calls.Minimum ||
		value.ControlFileReads != bound.ControlFileReads.Minimum ||
		value.StoreReadAttempts != bound.StoreReadAttempts.Minimum ||
		value.MemberReads != bound.MemberReads.Minimum ||
		value.StoreWriteAttempts != bound.StoreWriteAttempts.Minimum {
		return errors.New("archive transition read subtotal differs from its derived bound")
	}
	return nil
}

func validateLifecycleTransitionReadSubtotal(value TransitionReadSubtotal) error {
	bound := correctedLifecycleTransitionReadBound()
	if value.Schema != "t422-transition-read-accounting-v1" ||
		value.Class != correctedLifecycleTransitionReadClass || value.ReportCalls != bound.Calls.Minimum ||
		value.ControlFileReads != bound.ControlFileReads.Minimum ||
		value.StoreReadAttempts != bound.StoreReadAttempts.Minimum ||
		value.MemberReads != bound.MemberReads.Minimum ||
		value.StoreWriteAttempts != bound.StoreWriteAttempts.Minimum {
		return errors.New("lifecycle transition read subtotal differs from its derived bound")
	}
	return nil
}

func validateInjectionTransition(
	point FailurePoint,
	value InjectionTransition,
	startEventOrdinal, finishEventOrdinal uint64,
	authority map[string]AuthorityPhaseResult,
	metrics ReceiptMetrics,
	children []Count,
	plan Plan,
	freeze ExecutionFreeze,
) error {
	phaseAuthority := authority[point.Phase]
	authorityBefore := phaseAuthority
	switch point.Name {
	case "partial_service_activation":
		authorityBefore = authority["physical_delta_b"]
	case "interrupted_publication":
		authorityBefore = authority["logical_delta_b"]
	case "stale_partition_lease":
		authorityBefore = authority["return_a"]
	case "checkpointed_hard_restart":
		authorityBefore = authority["stale_lease"]
	}
	authorityBeforeSHA256, beforeOK := authorityIdentitySHA256(authorityBefore)
	authorityAfterSHA256, afterOK := authorityIdentitySHA256(phaseAuthority)
	targetIdentitySHA256, targetErr := receiptSHA256(value.Target)
	selectorSHA256, selectorErr := injectionSelectorSHA256(value.Target)
	stableTargetSHA256 := recipeDigest(
		"t422-stable-injection-target-v2", selectorSHA256, value.Target.Domain,
		value.Target.GenerationSHA256, value.Target.ScheduleSHA256, value.Target.PlanSHA256,
		value.Target.UnitSHA256,
	)
	wantTarget := recipeDigest(
		"t422-injection-target-binding-v3", point.Phase, point.Name, point.Boundary,
		stableTargetSHA256, authorityBeforeSHA256, authorityAfterSHA256,
	)
	hitSHA256, hitErr := injectionHitReportSHA256(value, point)
	recoverySHA256, recoveryErr := injectionRecoveryProjectionSHA256(value, point)
	authorityAtHitOK := value.AuthorityAtHitSHA256 == authorityBeforeSHA256
	if point.Name == "stale_partition_lease" && correctedPlanSemantics(plan.Schema) {
		want, ok := staleLeaseAuthorityAtHitSHA256(authorityBefore, phaseAuthority)
		authorityAtHitOK = ok && value.AuthorityAtHitSHA256 == want
	}
	if point.Name == "interrupted_publication" {
		want, ok := interruptedPublicationAuthorityAtHitSHA256(authorityBefore, phaseAuthority, plan)
		authorityAtHitOK = ok && value.AuthorityAtHitSHA256 == want
	}
	if point.Name == "checkpointed_hard_restart" && !correctedPlanSemantics(plan.Schema) {
		authorityAtHitOK = validDigest(value.AuthorityAtHitSHA256) &&
			value.AuthorityAtHitSHA256 != authorityBeforeSHA256 && value.AuthorityAtHitSHA256 != authorityAfterSHA256
	}
	if !beforeOK || !afterOK || targetErr != nil || selectorErr != nil || hitErr != nil || recoveryErr != nil ||
		value.Schema != plan.ReceiptContract.TransitionSchema+"/injection-v2" ||
		value.FailurePoint != point.Name || value.ArmCount != 1 || value.HitCount != 1 ||
		value.RecoveryCount != 1 || value.ResidueBefore != 0 || value.ResidueAtHit != 1 || value.ResidueAfter != 0 ||
		value.Target.Schema != plan.ReceiptContract.TransitionSchema+"/injection-target-v2" ||
		value.Target.Phase != point.Phase || value.Target.Domain != point.TargetDomain ||
		!injectionSelectorMatches(value.Target, point) ||
		!validDigest(value.Target.GenerationSHA256) || !validDigest(value.Target.ScheduleSHA256) ||
		!validDigest(value.Target.UnitSHA256) || value.Target.AuthoritySHA256 != authorityAfterSHA256 ||
		value.TargetIdentitySHA256 != targetIdentitySHA256 || value.StableTargetSHA256 != stableTargetSHA256 ||
		value.TargetSHA256 != wantTarget || value.HitReportSHA256 != hitSHA256 ||
		value.RecoveryProjectionSHA256 != recoverySHA256 ||
		!orderedEventsWithin(startEventOrdinal, finishEventOrdinal,
			value.ArmEventOrdinal, value.HitEventOrdinal, value.RecoveryEventOrdinal, value.ClearEventOrdinal,
		) || value.AuthorityBeforeSHA256 != authorityBeforeSHA256 || !authorityAtHitOK ||
		value.AuthorityAfterSHA256 != authorityAfterSHA256 || value.ElapsedMS == 0 ||
		value.ElapsedMS > point.RecoveryDeadlineMS || value.ElapsedMS > uint64(metrics.WallMS) ||
		value.DeadlineMS != point.RecoveryDeadlineMS {
		return errors.New("injection lifecycle is not exact")
	}
	if err := validateRecoveryPreparation(value, phaseAuthority, metrics, startEventOrdinal, plan); err != nil {
		return err
	}
	switch point.Name {
	case "partial_service_activation":
		wantRequeues := uint64(0)
		if correctedPlanSemantics(plan.Schema) {
			wantRequeues = 1
		}
		if value.ObservedRecoveryBranch != "resume_activation_schedule" || value.RecoveredCandidates != 1 ||
			value.CollectedCandidates != 0 || value.TargetGenerationBefore != authorityBefore.CatalogRootSHA256 ||
			value.TargetGenerationAfter != phaseAuthority.CatalogRootSHA256 ||
			value.TargetGenerationBefore == value.TargetGenerationAfter ||
			value.Target.GenerationSHA256 != phaseAuthority.CatalogRootSHA256 ||
			value.Target.PlanSHA256 != phaseAuthority.CatalogActivationPlanSHA256 ||
			value.Target.ScheduleSHA256 != phaseAuthority.CatalogActivationScheduleSHA256 ||
			value.Target.UnitSHA256 != phaseAuthority.CatalogActivationUnitSHA256 ||
			value.RequeueCount != wantRequeues || value.SuccessCount != 1 || value.Checkpoint != nil ||
			hasProcessEvidence(value) {
			return errors.New("activation chunk recovery is invalid")
		}
	case "checkpointed_hard_restart":
		if err := validateCheckpointRecovery(value, phaseAuthority, children, plan, freeze); err != nil {
			return err
		}
	case "interrupted_publication":
		if value.ObservedRecoveryBranch != "recover_marker_owned" || value.RecoveredCandidates != 1 ||
			value.CollectedCandidates != 0 || value.Target.UnitSHA256 != phaseAuthority.RelationshipRootSHA256 ||
			value.Target.GenerationSHA256 != phaseAuthority.RelationshipGenerationSHA256 ||
			!validInterruptedPublicationTargetMapping(value.Target, plan) ||
			authorityBefore.RelationshipGenerationSHA256 == value.Target.GenerationSHA256 ||
			value.TargetGenerationBefore != authorityBefore.RelationshipGenerationSHA256 ||
			value.TargetGenerationAfter != value.Target.GenerationSHA256 ||
			value.TargetGenerationBefore == value.TargetGenerationAfter || value.RequeueCount != 0 ||
			value.SuccessCount != 1 || value.Checkpoint != nil || hasProcessEvidence(value) {
			return errors.New("marker-owned publication recovery is invalid")
		}
	case "stale_partition_lease":
		rootIndex := slices.IndexFunc(phaseAuthority.ExtractionRoots, func(root ExtractionRootResult) bool {
			return root.Domain == point.TargetDomain
		})
		if rootIndex < 0 || value.ObservedRecoveryBranch != "fence_stale_lease_requeue_then_complete" ||
			value.RecoveredCandidates != 1 || value.CollectedCandidates != 0 ||
			!injectionTargetMatchesPreparedExtraction(value, phaseAuthority.ExtractionRoots[rootIndex]) ||
			value.TargetGenerationBefore != value.Target.GenerationSHA256 ||
			value.TargetGenerationAfter != value.Target.GenerationSHA256 || value.RequeueCount != 1 ||
			value.SuccessCount != 1 || value.Checkpoint != nil || hasProcessEvidence(value) {
			return errors.New("stale partition lease recovery is invalid")
		}
	default:
		return errors.New("failure point has no frozen validation")
	}
	return nil
}

func validateCheckpointRecovery(
	value InjectionTransition,
	authority AuthorityPhaseResult,
	children []Count,
	plan Plan,
	freeze ExecutionFreeze,
) error {
	checkpoint := value.Checkpoint
	rootIndex := slices.IndexFunc(authority.ExtractionRoots, func(root ExtractionRootResult) bool {
		return root.Domain == value.Target.Domain
	})
	toolIndex := slices.IndexFunc(freeze.Tools, func(tool ExecutionToolIdentity) bool { return tool.Role == "phebs" })
	phebsChildren := slices.IndexFunc(children, func(value Count) bool { return value.Name == "phebs" })
	if checkpoint == nil || rootIndex < 0 || toolIndex < 0 ||
		plan.Schema != PlanV3Schema && phebsChildren < 0 ||
		value.Target.Ordinal >= uint64(len(authority.ExtractionRoots[rootIndex].PartitionResults)) {
		return errors.New("checkpoint restart evidence is absent")
	}
	root := authority.ExtractionRoots[rootIndex]
	partition := root.PartitionResults[value.Target.Ordinal]
	imageSHA256 := freeze.Tools[toolIndex].SHA256
	beforeIdentity := recipeDigest("t422-phebs-process-identity-v1", imageSHA256, fmt.Sprint(value.ProcessEpochBefore))
	afterIdentity := recipeDigest("t422-phebs-process-identity-v1", imageSHA256, fmt.Sprint(value.ProcessEpochAfter))
	processIdentityValid := value.ProcessIdentityBeforeSHA256 == beforeIdentity &&
		value.ProcessIdentityAfterSHA256 == afterIdentity && beforeIdentity != afterIdentity
	processStartsValid := phebsChildren >= 0 && children[phebsChildren].Count == 1
	if plan.Schema == PlanV3Schema {
		// V3's separately validated runtime bindings carry successful owned
		// starts. Admission permissions cannot substantiate a server birth.
		processStartsValid = children == nil
		processIdentityValid = validDigest(value.ProcessIdentityBeforeSHA256) &&
			validDigest(value.ProcessIdentityAfterSHA256) && value.ProcessIdentityBeforeSHA256 != value.ProcessIdentityAfterSHA256
	}
	beforeEpoch, _, beforeOK := expectedPhaseRuntime(freeze.Profile.Epochs, "stale_lease")
	afterEpoch, _, afterOK := expectedPhaseRuntime(freeze.Profile.Epochs, "process_restart")
	completionAtHit := checkpoint.CompletionAbsentAtHit && !checkpoint.CompletionFileExistsAtHit && !checkpoint.CompletionBitClearAtHit
	if value.Preparation != nil {
		completionAtHit = !checkpoint.CompletionAbsentAtHit && checkpoint.CompletionFileExistsAtHit && checkpoint.CompletionBitClearAtHit
	}
	digests := []string{
		checkpoint.ResultIdentitySHA256, checkpoint.ResultDigestSHA256, checkpoint.PlanSHA256,
		checkpoint.ExpectationSHA256, checkpoint.PartitionSHA256, checkpoint.CandidateGenerationSHA256,
		checkpoint.SourceGenerationSHA256, checkpoint.ObservationGenerationSHA256,
		checkpoint.ExtractionPolicySHA256,
	}
	for _, digest := range digests {
		if !validDigest(digest) {
			return errors.New("checkpoint identity is invalid")
		}
	}
	attemptsExact := checkpoint.AttemptBefore != 0 && checkpoint.AttemptAfter == checkpoint.AttemptBefore
	checkpointStateExact := checkpoint.ChunkIdentitySHA256 == "" &&
		checkpoint.ScheduleStatusAtHit == "" && checkpoint.ChunkStatusAtHit == "" && !checkpoint.LeasedAtHit &&
		!checkpoint.CurrentAbsentAtHit && checkpoint.ScheduleStatusAfter == "" &&
		checkpoint.ChunkStatusAfter == "" && !checkpoint.UnleasedAfter
	if correctedPlanSemantics(plan.Schema) {
		attemptsExact = checkpoint.AttemptBefore == 0 && checkpoint.AttemptAfter == 0
		globalOffset := value.Target.Ordinal
		for index := 0; index < rootIndex; index++ {
			count := uint64(len(authority.ExtractionRoots[index].PartitionResults))
			if globalOffset > uint64(math.MaxInt64) || count > uint64(math.MaxInt64)-globalOffset {
				return errors.New("checkpoint chunk offset overflows")
			}
			globalOffset += count
		}
		chunkIdentity, err := store.GenerationChunkIdentity(
			value.Target.ScheduleSHA256, int64(globalOffset), 0,
		)
		checkpointStateExact = err == nil && checkpoint.ChunkIdentitySHA256 == chunkIdentity &&
			checkpoint.ScheduleStatusAtHit == store.GenerationScheduleActive &&
			checkpoint.ChunkStatusAtHit == store.GenerationChunkRunning && checkpoint.LeasedAtHit &&
			checkpoint.CurrentAbsentAtHit &&
			checkpoint.ScheduleStatusAfter == store.GenerationScheduleSettled &&
			checkpoint.ChunkStatusAfter == store.GenerationChunkDone && checkpoint.UnleasedAfter
	}
	if checkpoint.Domain != value.Target.Domain || !safeToken(checkpoint.ExtractorVersion, 128) ||
		!injectionTargetMatchesPreparedExtraction(value, root) ||
		checkpoint.ResultIdentitySHA256 != value.Target.UnitSHA256 ||
		checkpoint.ResultDigestSHA256 != partition.ResultDigestSHA256 ||
		checkpoint.PlanSHA256 != root.PlanSHA256 || checkpoint.ExpectationSHA256 != partition.ExpectationSHA256 ||
		checkpoint.PartitionSHA256 != partition.PartitionSHA256 ||
		checkpoint.CandidateGenerationSHA256 != root.CandidateGenerationSHA256 ||
		checkpoint.SourceGenerationSHA256 != root.SourceGenerationSHA256 ||
		checkpoint.ObservationGenerationSHA256 != authority.ObservationGenerationSHA256 ||
		!checkpoint.CanonicalResultExistsAtHit || !checkpoint.ResultDirectorySyncedAtHit ||
		!completionAtHit || !checkpoint.RootAbsentAtHit ||
		!checkpoint.SameResultBytesReused || !checkpoint.CompletionExistsAfter ||
		!checkpoint.RootExistsAfter || !checkpoint.CurrentAfter || checkpoint.StartCount != 2 ||
		checkpoint.CompletionCount != 1 || checkpoint.RetrySuccessorCount != 0 ||
		checkpoint.PriorityBefore != 0 || checkpoint.PriorityAfter != 2 || !attemptsExact || !checkpointStateExact ||
		!checkpoint.PrivateLeaseTokenChanged ||
		!checkpoint.HardDeath || checkpoint.CooperativeRelease || !processStartsValid ||
		value.ObservedRecoveryBranch != "hard_restart_reap_and_reuse_checkpoint" ||
		value.RecoveredCandidates != 1 || value.CollectedCandidates != 0 ||
		!beforeOK || !afterOK || value.ProcessEpochBefore != beforeEpoch || value.ProcessEpochAfter != afterEpoch ||
		value.ProcessImageSHA256 != imageSHA256 || !processIdentityValid ||
		value.ProcessStopEventOrdinal <= value.HitEventOrdinal ||
		value.ProcessStartEventOrdinal <= value.ProcessStopEventOrdinal ||
		value.ProcessStartEventOrdinal >= value.RecoveryEventOrdinal ||
		value.TargetGenerationBefore != value.Target.GenerationSHA256 ||
		value.TargetGenerationAfter != value.Target.GenerationSHA256 || value.RequeueCount != 1 ||
		value.SuccessCount != 1 {
		return errors.New("checkpointed hard restart is invalid")
	}
	return nil
}

func injectionSelectorSHA256(value InjectionTargetProjection) (string, error) {
	return receiptSHA256(struct {
		Kind              string `json:"kind"`
		Ordinal           uint64 `json:"ordinal"`
		ServiceOrdinal    uint64 `json:"service_ordinal,omitempty"`
		ServiceKeySHA256  string `json:"service_key_sha256,omitempty"`
		CallerPrefix      string `json:"caller_prefix,omitempty"`
		SourceStart       uint64 `json:"source_start"`
		SourceEnd         uint64 `json:"source_end"`
		MemberOrdinal     int64  `json:"member_ordinal"`
		MemberRecordStart uint64 `json:"member_record_start"`
		MemberRecordEnd   uint64 `json:"member_record_end"`
	}{
		value.Kind, value.Ordinal, value.ServiceOrdinal, value.ServiceKeySHA256, value.CallerPrefix,
		value.SourceStart, value.SourceEnd, value.MemberOrdinal,
		value.MemberRecordStart, value.MemberRecordEnd,
	})
}

func injectionSelectorMatches(value InjectionTargetProjection, point FailurePoint) bool {
	return value.Kind == point.TargetKind && value.Ordinal == point.TargetOrdinal &&
		value.ServiceOrdinal == point.TargetServiceOrdinal &&
		value.ServiceKeySHA256 == point.TargetServiceKeySHA256 && value.CallerPrefix == point.TargetCallerPrefix &&
		value.SourceStart == point.TargetSourceStart && value.SourceEnd == point.TargetSourceEnd &&
		value.MemberOrdinal == point.TargetMemberOrdinal && value.MemberRecordStart == point.TargetMemberStart &&
		value.MemberRecordEnd == point.TargetMemberEnd
}

func injectionTargetMatchesExtraction(value InjectionTargetProjection, root ExtractionRootResult) bool {
	if value.Ordinal >= uint64(len(root.PartitionResults)) {
		return false
	}
	partition := root.PartitionResults[value.Ordinal]
	return value.GenerationSHA256 == root.GenerationSHA256 && value.PlanSHA256 == root.PlanSHA256 &&
		value.ScheduleSHA256 == root.ScheduleSHA256 && value.UnitSHA256 == partition.ResultIdentitySHA256 &&
		value.Kind == partition.Kind && value.Ordinal == partition.Ordinal &&
		value.CallerPrefix == partition.CallerPrefix && value.SourceStart == partition.SourceStart &&
		value.SourceEnd == partition.SourceEnd && value.MemberOrdinal == partition.MemberOrdinal &&
		value.MemberRecordStart == partition.MemberRecordStart && value.MemberRecordEnd == partition.MemberRecordEnd
}

func hasProcessEvidence(value InjectionTransition) bool {
	return value.ProcessEpochBefore != 0 || value.ProcessEpochAfter != 0 ||
		value.ProcessIdentityBeforeSHA256 != "" || value.ProcessIdentityAfterSHA256 != "" ||
		value.ProcessImageSHA256 != "" || value.ProcessStopEventOrdinal != 0 || value.ProcessStartEventOrdinal != 0
}

func injectionHitReportSHA256(value InjectionTransition, point FailurePoint) (string, error) {
	type checkpointHit struct {
		ResultIdentitySHA256, ResultDigestSHA256, PlanSHA256, ExpectationSHA256, PartitionSHA256 string
		CandidateGenerationSHA256, SourceGenerationSHA256, ObservationGenerationSHA256           string
		Domain, ExtractorVersion, ExtractionPolicySHA256                                         string
		CanonicalResultExistsAtHit, ResultDirectorySyncedAtHit                                   bool
		CompletionAbsentAtHit, RootAbsentAtHit, HardDeath, CooperativeRelease                    bool
		PriorityBefore, AttemptBefore                                                            uint64
	}
	var checkpoint *checkpointHit
	if value.Checkpoint != nil {
		checkpoint = &checkpointHit{
			value.Checkpoint.ResultIdentitySHA256, value.Checkpoint.ResultDigestSHA256,
			value.Checkpoint.PlanSHA256, value.Checkpoint.ExpectationSHA256, value.Checkpoint.PartitionSHA256,
			value.Checkpoint.CandidateGenerationSHA256, value.Checkpoint.SourceGenerationSHA256,
			value.Checkpoint.ObservationGenerationSHA256, value.Checkpoint.Domain,
			value.Checkpoint.ExtractorVersion, value.Checkpoint.ExtractionPolicySHA256,
			value.Checkpoint.CanonicalResultExistsAtHit, value.Checkpoint.ResultDirectorySyncedAtHit,
			value.Checkpoint.CompletionAbsentAtHit, value.Checkpoint.RootAbsentAtHit,
			value.Checkpoint.HardDeath, value.Checkpoint.CooperativeRelease,
			value.Checkpoint.PriorityBefore, value.Checkpoint.AttemptBefore,
		}
	}
	legacy, err := receiptSHA256(struct {
		Schema, Trigger, FailurePoint, StableTargetSHA256, AuthorityBeforeSHA256, AuthorityAtHitSHA256 string
		Target                                                                                         InjectionTargetProjection
		ArmCount, HitCount, ResidueBefore, ResidueAtHit, ArmEventOrdinal, HitEventOrdinal              uint64
		Checkpoint                                                                                     *checkpointHit
	}{
		"t422-exact-control-hit-report-v3", point.Trigger, point.Name, value.StableTargetSHA256,
		value.AuthorityBeforeSHA256, value.AuthorityAtHitSHA256, value.Target,
		value.ArmCount, value.HitCount, value.ResidueBefore, value.ResidueAtHit,
		value.ArmEventOrdinal, value.HitEventOrdinal, checkpoint,
	})
	if err != nil || value.Preparation == nil {
		return legacy, err
	}
	preparation := value.Preparation
	fileExists, bitClear := false, false
	if value.Checkpoint != nil {
		fileExists, bitClear = value.Checkpoint.CompletionFileExistsAtHit, value.Checkpoint.CompletionBitClearAtHit
	}
	prefix, err := receiptSHA256(struct {
		PrepareEventOrdinal                                                                                                         uint64
		AuthoritySHA256, RootsSHA256, TargetGenerationSHA256, PriorScheduleSHA256, RecoveryGenerationSHA256, RecoveryScheduleSHA256 string
		ScheduleWrites, CompletionWrites, Deletes                                                                                   uint64
		CompletionFileExists, CompletionBitClear                                                                                    bool
	}{preparation.PrepareEventOrdinal, preparation.AuthoritySHA256, preparation.PreservedRootsSHA256,
		preparation.TargetGenerationSHA256, preparation.PriorScheduleSHA256, preparation.RecoveryGenerationSHA256, preparation.RecoveryScheduleSHA256,
		preparation.ScheduleWrites, preparation.PreparationCompletionWrites, preparation.PreparationDeletes,
		fileExists, bitClear})
	if err != nil {
		return "", err
	}
	if value.Checkpoint == nil {
		return recipeDigest("t422-prepared-exact-control-hit-report-v1", legacy, prefix), nil
	}
	checkpointPrefix, err := receiptSHA256(struct {
		ChunkIdentitySHA256             string
		ScheduleStatusAtHit             store.GenerationScheduleStatus
		ChunkStatusAtHit                store.GenerationChunkStatus
		LeasedAtHit, CurrentAbsentAtHit bool
	}{
		value.Checkpoint.ChunkIdentitySHA256,
		value.Checkpoint.ScheduleStatusAtHit,
		value.Checkpoint.ChunkStatusAtHit,
		value.Checkpoint.LeasedAtHit,
		value.Checkpoint.CurrentAbsentAtHit,
	})
	if err != nil {
		return "", err
	}
	return recipeDigest(
		"t422-prepared-checkpoint-exact-control-hit-report-v1", legacy, prefix, checkpointPrefix,
	), nil
}

func injectionRecoveryProjectionSHA256(value InjectionTransition, point FailurePoint) (string, error) {
	legacy, err := receiptSHA256(struct {
		Schema, FailurePoint, StableTargetSHA256, AuthorityAfterSHA256, Branch   string
		RecoveryCount, ResidueAfter, RecoveryEventOrdinal, ClearEventOrdinal     uint64
		TargetGenerationBefore, TargetGenerationAfter                            string
		RequeueCount, SuccessCount, RecoveredCandidates, CollectedCandidates     uint64
		ProcessEpochBefore, ProcessEpochAfter                                    uint64
		ProcessIdentityBeforeSHA256, ProcessIdentityAfterSHA256                  string
		ProcessImageSHA256                                                       string
		ProcessStopEventOrdinal, ProcessStartEventOrdinal, ElapsedMS, DeadlineMS uint64
		Checkpoint                                                               *CheckpointRecovery
	}{
		"t422-exact-control-recovery-projection-v3", point.Name, value.StableTargetSHA256,
		value.AuthorityAfterSHA256, value.ObservedRecoveryBranch,
		value.RecoveryCount, value.ResidueAfter, value.RecoveryEventOrdinal, value.ClearEventOrdinal,
		value.TargetGenerationBefore, value.TargetGenerationAfter, value.RequeueCount, value.SuccessCount,
		value.RecoveredCandidates, value.CollectedCandidates, value.ProcessEpochBefore, value.ProcessEpochAfter,
		value.ProcessIdentityBeforeSHA256, value.ProcessIdentityAfterSHA256, value.ProcessImageSHA256,
		value.ProcessStopEventOrdinal, value.ProcessStartEventOrdinal, value.ElapsedMS, value.DeadlineMS,
		value.Checkpoint,
	})
	if err != nil || value.Preparation == nil {
		return legacy, err
	}
	preparation, err := receiptSHA256(value.Preparation)
	if err != nil {
		return "", err
	}
	return recipeDigest("t422-prepared-exact-control-recovery-projection-v1", legacy, preparation), nil
}

func validateArchiveTransition(
	value ArchiveTransition,
	startEventOrdinal, finishEventOrdinal uint64,
	authority map[string]AuthorityPhaseResult,
	plan Plan,
) error {
	before, beforeOK := authority["pressure_75"]
	after, afterOK := authority["archive_restore"]
	stateSHA256, err := expectedArchiveSemanticStateSHA256(plan)
	if err != nil {
		return err
	}
	relationshipSHA256, err := expectedRelationshipSemanticSHA256(plan)
	if err != nil {
		return err
	}
	disposition := relationshipRuntimeIdentityDisposition(before, after)
	beforeAuthoritySHA256, beforeAuthorityErr := authoritySnapshotSHA256(before.AuthorityState)
	afterAuthoritySHA256, afterAuthorityErr := authoritySnapshotSHA256(after.AuthorityState)
	manifestInventory, manifestErr := archiveManifestInventory(value.Components)
	reportInventory, reportErr := archiveReportInventory(value.Reports)
	bindingSHA256, bindingErr := archiveBindingSHA256(value)
	if !beforeOK || !afterOK || value.Schema != plan.ReceiptContract.TransitionSchema+"/archive-v1" ||
		!validDigest(value.ArchiveSHA256) || value.ArchiveBytes == 0 ||
		value.ManifestSchema != recovery.ManifestSchema || !validDigest(value.ManifestSHA256) ||
		value.InventoryCanonicalization != archiveInventoryCanonicalization ||
		manifestErr != nil || value.ManifestInventory != manifestInventory ||
		reportErr != nil || value.ReportInventory != reportInventory ||
		!validSetIdentity(value.StateInventoryBefore) ||
		value.StateInventoryBefore != value.StateInventoryArchived ||
		value.StateInventoryBefore != value.StateInventoryAfter ||
		bindingErr != nil || value.ArchiveBindingSHA256 != bindingSHA256 ||
		value.PreRestoreStateSHA256 != stateSHA256 || value.RestoredStateSHA256 != stateSHA256 ||
		value.RelationshipSemanticSHA256 != relationshipSHA256 ||
		value.RelationshipGenerationBefore != before.RelationshipGenerationSHA256 ||
		value.RelationshipGenerationAfter != after.RelationshipGenerationSHA256 ||
		value.RelationshipRootBefore != before.RelationshipRootSHA256 ||
		value.RelationshipRootAfter != after.RelationshipRootSHA256 ||
		beforeAuthorityErr != nil || value.AuthoritySnapshotBeforeSHA256 != beforeAuthoritySHA256 ||
		afterAuthorityErr != nil || value.AuthoritySnapshotAfterSHA256 != afterAuthoritySHA256 ||
		value.InstallationPathsAfterDestroy != 0 || value.RestoreTargetPathsBeforeRestore != 0 ||
		value.RelationshipRuntimeIdentityDisposition != disposition || value.ScratchSourcePathsAfter != 0 ||
		!orderedEventsWithin(startEventOrdinal, finishEventOrdinal,
			value.ArchiveCreatedEventOrdinal, value.InstallationDestroyedEventOrdinal,
			value.EmptyRestoreTargetEventOrdinal, value.RestoreStartedEventOrdinal, value.ComparisonEventOrdinal,
		) {
		return errors.New("archive restore facts are invalid")
	}
	return nil
}

func archiveManifestInventory(values []ArchiveComponent) (SetIdentity, error) {
	want := []struct{ name, classification, mediaType string }{
		{recovery.DatabaseName, "precious", "application/surrealql"},
		{recovery.FocusedIndexName, "derived-byte-exact", "application/x-tar"},
		{recovery.ResolverCatalogName, "derived-byte-exact", "application/x-tar"},
		{recovery.CallerPublicationName, "derived-byte-exact", "application/x-tar"},
		{recovery.ObservationPublicationName, "derived-byte-exact", "application/x-tar"},
		{recovery.RelationshipPublicationName, "derived-byte-exact", "application/x-tar"},
	}
	if len(values) != len(want) {
		return SetIdentity{}, errors.New("archive manifest component inventory is incomplete")
	}
	builder := newIdentityBuilder("t422-archive-manifest-components-v1")
	for index, expected := range want {
		value := values[index]
		if value.Name != expected.name || value.Classification != expected.classification ||
			value.MediaType != expected.mediaType || value.Bytes == 0 || !validDigest(value.SHA256) {
			return SetIdentity{}, errors.New("archive manifest component is invalid")
		}
		if err := builder.add(value); err != nil {
			return SetIdentity{}, err
		}
	}
	return builder.finish(), nil
}

func archiveReportInventory(values []ArchiveReportProjection) (SetIdentity, error) {
	want := []struct{ name, schema string }{
		{"focused_index", recovery.FocusedIndexArchiveReportSchema},
		{"resolver_catalog", recovery.ResolverCatalogArchiveReportSchema},
		{"caller_publication", recovery.CallerPublicationArchiveReportSchema},
		{"observation", recovery.ObservationArchiveReportSchema},
		{"relationship", recovery.RelationshipArchiveReportSchema},
	}
	if len(values) != len(want) {
		return SetIdentity{}, errors.New("archive report inventory is incomplete")
	}
	builder := newIdentityBuilder("t422-archive-reports-v1")
	for index, expected := range want {
		value := values[index]
		if value.Name != expected.name || value.Schema != expected.schema || value.Publications == 0 ||
			value.Omitted != 0 || value.OmittedPublications != 0 || value.OmittedArtifacts != 0 ||
			value.StaleMarkers != 0 || value.TruncatedDetails != 0 {
			return SetIdentity{}, errors.New("archive report is incomplete or records omissions")
		}
		switch value.Name {
		case "focused_index", "resolver_catalog", "caller_publication":
			if value.V1Publications != 0 || value.V2Publications != 0 || value.Files != 0 || value.Bytes != 0 {
				return SetIdentity{}, errors.New("archive report retained unrelated counters")
			}
		case "observation":
			if value.V1Publications > math.MaxUint64-value.V2Publications ||
				value.Publications != value.V1Publications+value.V2Publications || value.Files == 0 || value.Bytes == 0 {
				return SetIdentity{}, errors.New("observation archive report is invalid")
			}
		case "relationship":
			if value.V1Publications != 0 || value.V2Publications != 0 || value.Files == 0 || value.Bytes == 0 {
				return SetIdentity{}, errors.New("relationship archive report is invalid")
			}
		}
		if err := builder.add(value); err != nil {
			return SetIdentity{}, err
		}
	}
	return builder.finish(), nil
}

func archiveBindingSHA256(value ArchiveTransition) (string, error) {
	return receiptSHA256(struct {
		Schema, ArchiveSHA256, ManifestSchema, ManifestSHA256, Canonicalization      string
		AuthorityBeforeSHA256, AuthorityAfterSHA256                                  string
		ArchiveBytes, InstallationPathsAfterDestroy, RestoreTargetPathsBeforeRestore uint64
		ManifestInventory, ReportInventory                                           SetIdentity
		Before, Archived, After                                                      SetIdentity
		Components                                                                   []ArchiveComponent
		Reports                                                                      []ArchiveReportProjection
	}{
		value.Schema, value.ArchiveSHA256, value.ManifestSchema, value.ManifestSHA256,
		value.InventoryCanonicalization, value.AuthoritySnapshotBeforeSHA256,
		value.AuthoritySnapshotAfterSHA256, value.ArchiveBytes,
		value.InstallationPathsAfterDestroy, value.RestoreTargetPathsBeforeRestore,
		value.ManifestInventory, value.ReportInventory,
		value.StateInventoryBefore, value.StateInventoryArchived, value.StateInventoryAfter,
		value.Components, value.Reports,
	})
}

func relationshipRuntimeIdentityDisposition(before, after AuthorityPhaseResult) string {
	generationChanged := before.RelationshipGenerationSHA256 != after.RelationshipGenerationSHA256
	rootChanged := before.RelationshipRootSHA256 != after.RelationshipRootSHA256
	switch {
	case generationChanged && rootChanged:
		return "both_replaced"
	case generationChanged:
		return "generation_replaced"
	case rootChanged:
		return "root_replaced"
	default:
		return "preserved"
	}
}

func validateLifecycleTransition(
	value LifecycleTransition,
	startEventOrdinal, finishEventOrdinal uint64,
	authority map[string]AuthorityPhaseResult,
	metrics ReceiptMetrics,
	plan Plan,
) error {
	before, beforeOK := authorityIdentitySHA256(authority["archive_restore"])
	after, afterOK := authorityIdentitySHA256(authority["lifecycle_collection"])
	finalRows, err := validateLifecycleOwners(value.Owners, plan, value.LifecycleFenceUnixMS, value.CapacityObservedUnixMS)
	if err != nil {
		return err
	}
	aggregate := lifecycleAggregate{
		scanned: value.Scanned, deleted: value.Deleted, logicalBytes: value.LogicalBytes,
		rootBytes: value.RootBytes, memberBytes: value.MemberBytes,
	}
	if err := validateLifecycleTotals(
		aggregate, finalRows, value.OwnerTurns, uint64(len(plan.WorkEnvelope.LifecycleOwners)),
	); err != nil {
		return err
	}
	cycleSHA256, err := lifecycleCycleSHA256(value)
	if err != nil {
		return err
	}
	if value.Schema != plan.ReceiptContract.TransitionSchema+"/lifecycle-v1" || !beforeOK || !afterOK ||
		value.AuthorityBeforeSHA256 != before || value.AuthorityAfterSHA256 != after || before != after ||
		value.CycleSHA256 != cycleSHA256 || metrics.LifecycleOwnerTurns != CountMetric(value.OwnerTurns) ||
		metrics.LifecycleDeleted != CountMetric(value.Deleted) ||
		!orderedEventsWithin(startEventOrdinal, finishEventOrdinal,
			value.LifecycleFenceEventOrdinal, value.CapacityObservedEventOrdinal,
		) {
		return errors.New("lifecycle aggregate is invalid")
	}
	return nil
}

type lifecycleAggregate struct {
	scanned, deleted, logicalBytes, rootBytes, memberBytes uint64
}

func validateLifecycleOwners(
	values []LifecycleOwnerResult,
	plan Plan,
	fenceUnixMS, capacityObservedUnixMS uint64,
) (lifecycleAggregate, error) {
	if len(values) != len(plan.WorkEnvelope.LifecycleOwners) ||
		fenceUnixMS == 0 || capacityObservedUnixMS == 0 {
		return lifecycleAggregate{}, errors.New("lifecycle owner inventory is incomplete")
	}
	var result lifecycleAggregate
	latestAttempt := fenceUnixMS
	for index, name := range plan.WorkEnvelope.LifecycleOwners {
		value := values[index]
		if value.Name != name || value.State != "ok" ||
			lifecycleTimestampOutOfOrder(plan.Schema, value.AttemptedAtUnixMS, latestAttempt) ||
			value.Scanned > uint64(lifecycle.MaxCandidatesPerTick) ||
			value.Deleted > uint64(lifecycle.MaxDeletesPerTick) || value.Deleted > value.Scanned {
			return lifecycleAggregate{}, errors.New("lifecycle owner row is invalid")
		}
		latestAttempt = value.AttemptedAtUnixMS
		if name == lifecycle.JobOwner {
			if value.Completeness != string(lifecycle.LowerBound) {
				return lifecycleAggregate{}, errors.New("durable-job lifecycle completeness is invalid")
			}
		} else if value.Completeness != string(lifecycle.Exact) || value.Backlog {
			return lifecycleAggregate{}, errors.New("exact lifecycle owner is not drained")
		}
		for _, add := range []struct {
			destination *uint64
			value       uint64
		}{
			{&result.scanned, value.Scanned}, {&result.deleted, value.Deleted},
			{&result.logicalBytes, value.LogicalBytes}, {&result.rootBytes, value.RootBytes},
			{&result.memberBytes, value.MemberBytes},
		} {
			if add.value > math.MaxUint64-*add.destination {
				return lifecycleAggregate{}, errors.New("lifecycle aggregate overflowed")
			}
			*add.destination += add.value
		}
	}
	if lifecycleTimestampOutOfOrder(plan.Schema, capacityObservedUnixMS, latestAttempt) {
		return lifecycleAggregate{}, errors.New("lifecycle owner accounting or freshness is invalid")
	}
	return result, nil
}

func lifecycleTimestampOutOfOrder(planSchema string, current, previous uint64) bool {
	return current < previous || !correctedPlanSemantics(planSchema) && current == previous
}

func validateLifecycleTotals(total, finalRows lifecycleAggregate, ownerTurns, minimumTurns uint64) error {
	if ownerTurns < minimumTurns || ownerTurns > math.MaxUint64/uint64(lifecycle.MaxCandidatesPerTick) ||
		ownerTurns > math.MaxUint64/uint64(lifecycle.MaxDeletesPerTick) ||
		total.scanned > ownerTurns*uint64(lifecycle.MaxCandidatesPerTick) ||
		total.deleted > ownerTurns*uint64(lifecycle.MaxDeletesPerTick) || total.deleted > total.scanned ||
		total.scanned < finalRows.scanned || total.deleted < finalRows.deleted ||
		total.logicalBytes < finalRows.logicalBytes || total.rootBytes < finalRows.rootBytes ||
		total.memberBytes < finalRows.memberBytes {
		return errors.New("lifecycle cumulative accounting is invalid")
	}
	return nil
}

func lifecycleCycleSHA256(value LifecycleTransition) (string, error) {
	value.CycleSHA256 = ""
	return receiptSHA256(value)
}

func validatePressureTransitions(
	values []TransitionResult,
	outcomes map[string]string,
	authority map[string]AuthorityPhaseResult,
	metrics map[string]ReceiptMetrics,
	plan Plan,
	freeze ExecutionFreeze,
) error {
	start := SHA256([]byte("t422-pressure-sequence-start-v1"))
	priorSequence, priorBallast, priorAvailable, priorDataAllocated := start, uint64(0), uint64(0), uint64(0)
	baseAllocated := uint64(0)
	serverEpoch := uint64(0)
	if restart, ok := namedTransition(values, "process_restart"); ok && restart.Outcome == "passed" {
		index := slices.IndexFunc(restart.Injections, func(value InjectionTransition) bool {
			return value.FailurePoint == "checkpointed_hard_restart"
		})
		if index >= 0 {
			serverEpoch = restart.Injections[index].ProcessEpochAfter
		}
	}
	var pressure80Capacity, pressure75Capacity uint64
	for index, phase := range []string{"pressure_80", "pressure_90", "pressure_75"} {
		if outcomes[phase] != "passed" {
			break
		}
		transition, _ := namedTransition(values, phase)
		value := transition.Pressure
		target := freeze.Pressure.Targets[index]
		authoritySHA256, ok := authorityIdentitySHA256(authority[phase])
		if value == nil || !ok || value.Schema != plan.ReceiptContract.TransitionSchema+"/pressure-v1" ||
			value.TargetUsedPercent != target.TargetUsedPercent || value.Action != target.Action ||
			value.ExpectedDisposition != target.ExpectedDisposition || value.ObservedDisposition != target.ExpectedDisposition ||
			value.PriorGateSequenceSHA256 != priorSequence || value.ServerEpoch == 0 || value.ServerEpoch != serverEpoch ||
			value.RestartCount != 0 || value.DataVolumeIdentity != freeze.Host.DataVolumeIdentity ||
			value.BallastVolumeIdentity != freeze.Host.BallastVolumeIdentity ||
			value.AuthorityBeforeSHA256 != authoritySHA256 || value.AuthorityAfterSHA256 != authoritySHA256 ||
			value.VolumeAvailableBytesBefore > freeze.Pressure.PressureVolumeBytes ||
			value.VolumeAvailableBytesAfter > freeze.Pressure.PressureVolumeBytes ||
			value.VolumeUsedBytesBefore+value.VolumeAvailableBytesBefore != freeze.Pressure.PressureVolumeBytes ||
			value.VolumeUsedBytesAfter+value.VolumeAvailableBytesAfter != freeze.Pressure.PressureVolumeBytes ||
			value.VolumeUsedBytesAfter < target.MinimumUsedBytes || value.VolumeUsedBytesAfter > target.MaximumUsedBytes ||
			!withinTolerance(value.VolumeUsedBytesAfter, target.TargetUsedBytes, target.ToleranceBytes) ||
			!withinTolerance(value.VolumeAvailableBytesAfter, target.TargetAvailableBytes, target.ToleranceBytes) ||
			usedPercentCeiling(value.VolumeUsedBytesAfter, freeze.Pressure.PressureVolumeBytes) != value.ObservedUsedPercent ||
			value.ObservedUsedPercent != target.TargetUsedPercent ||
			value.BallastAllocatedBytesBefore != priorBallast ||
			value.BallastAllocatedBytesAfter > freeze.Pressure.BallastCeilingBytes ||
			value.DataAllocatedBytesAtTarget > plan.SafetyEnvelope.MaximumDataAllocatedBytes ||
			value.DataAllocatedBytesAtTarget < value.BallastAllocatedBytesAfter {
			return fmt.Errorf("phase %q pressure facts are invalid", phase)
		}
		if index > 0 && (value.VolumeAvailableBytesBefore != priorAvailable ||
			value.DataAllocatedBytesBefore != priorDataAllocated) {
			return fmt.Errorf("phase %q pressure capacity is not contiguous", phase)
		}
		if !pressureMutationMatches(
			target.Action,
			value.VolumeUsedBytesBefore, value.VolumeUsedBytesAfter,
			value.BallastAllocatedBytesBefore, value.BallastAllocatedBytesAfter,
			value.DataAllocatedBytesBefore, value.DataAllocatedBytesAtTarget,
			target.ToleranceBytes,
		) {
			return fmt.Errorf("phase %q ballast mutation is invalid", phase)
		}
		if index == 0 {
			if value.VolumeUsedBytesBefore < freeze.Pressure.MinimumPrePressureUsedBytes ||
				value.VolumeUsedBytesBefore > freeze.Pressure.MaximumPrePressureUsedBytes ||
				value.PrePressureAllocatedBytes < freeze.Pressure.MinimumPrePressureBytes ||
				value.PrePressureAllocatedBytes > freeze.Pressure.MaximumPrePressureBytes ||
				value.DataAllocatedBytesBefore != value.PrePressureAllocatedBytes ||
				value.GateOutcome != "success" {
				return errors.New("pressure 80 pre-pressure normalization is invalid")
			}
			baseAllocated = value.PrePressureAllocatedBytes
			if err := validatePressure80Lifecycle(*value, metrics[phase], plan); err != nil ||
				value.PrePressureDeletedUnits != value.LifecycleDeleted ||
				!orderedEventsWithin(transition.StartEventOrdinal, transition.FinishEventOrdinal,
					value.LifecycleFenceEventOrdinal, value.CapacityObservedEventOrdinal,
					value.BallastMutationEventOrdinal, value.GateEventOrdinal,
				) {
				return errors.New("pressure 80 collection cycle is invalid")
			}
			pressure80Capacity = value.CapacityObservedUnixMS
		} else if value.PrePressureDeletedUnits != 0 || value.PrePressureAllocatedBytes != 0 {
			return fmt.Errorf("phase %q retained pressure-80 normalization evidence", phase)
		} else if value.GateOutcome != "err_pressure_refusal" {
			return fmt.Errorf("phase %q lacks the typed production pressure refusal", phase)
		}
		if phase == "pressure_75" {
			if value.RecoveryBallastAllocatedBytes != 0 || value.RecoveryUsedPercent > freeze.Pressure.Recovery.MaximumUsedPercent ||
				value.RecoveryUsedBytes+value.RecoveryAvailableBytes != freeze.Pressure.PressureVolumeBytes ||
				usedPercentCeiling(value.RecoveryUsedBytes, freeze.Pressure.PressureVolumeBytes) != value.RecoveryUsedPercent ||
				value.RecoveryDisposition != freeze.Pressure.Recovery.ExpectedDisposition ||
				value.RecoveryGateOutcome != "success" || value.LifecycleFenceUnixMS <= pressure80Capacity ||
				value.RecoveryDataAllocatedBytes > baseAllocated ||
				value.RecoveryDataAllocatedBytes >= value.DataAllocatedBytesAtTarget ||
				!pressureMutationMatches(
					"remove", value.VolumeUsedBytesAfter, value.RecoveryUsedBytes,
					value.BallastAllocatedBytesAfter, value.RecoveryBallastAllocatedBytes,
					value.DataAllocatedBytesAtTarget, value.RecoveryDataAllocatedBytes,
					target.ToleranceBytes,
				) ||
				value.DataAllocatedBytesBefore != uint64(metrics[phase].DataAllocatedBytes) ||
				!orderedEventsWithin(transition.StartEventOrdinal, transition.FinishEventOrdinal,
					value.BallastMutationEventOrdinal, value.GateEventOrdinal, value.RecoveryBallastEventOrdinal,
					value.LifecycleFenceEventOrdinal, value.CapacityObservedEventOrdinal, value.RecoveryGateEventOrdinal,
				) {
				return errors.New("pressure recovery did not clear the production latch")
			}
			if err := validatePressureLifecycle(*value, metrics[phase], plan); err != nil {
				return errors.New("pressure recovery lifecycle cycle is invalid")
			}
			pressure75Capacity = value.CapacityObservedUnixMS
		} else if phase == "pressure_90" && (value.RecoveryUsedPercent != 0 || value.RecoveryUsedBytes != 0 ||
			value.RecoveryAvailableBytes != 0 || value.RecoveryDataAllocatedBytes != 0 ||
			value.RecoveryDisposition != "" || value.RecoveryGateOutcome != "" ||
			value.RecoveryBallastAllocatedBytes != 0 || value.RecoveryBallastEventOrdinal != 0 ||
			value.RecoveryGateEventOrdinal != 0 || value.LifecycleFenceUnixMS != 0 ||
			value.CapacityObservedUnixMS != 0 || value.LifecycleFenceEventOrdinal != 0 ||
			value.CapacityObservedEventOrdinal != 0 || value.LifecycleScanned != 0 ||
			value.LifecycleDeleted != 0 || value.LifecycleLogicalBytes != 0 || value.LifecycleRootBytes != 0 ||
			value.LifecycleMemberBytes != 0 || value.LifecycleOwnerTurns != 0 ||
			value.LifecycleCycleSHA256 != "" || value.Owners != nil ||
			!orderedEventsWithin(transition.StartEventOrdinal, transition.FinishEventOrdinal,
				value.BallastMutationEventOrdinal, value.GateEventOrdinal,
			)) {
			return fmt.Errorf("phase %q retained unrelated recovery evidence", phase)
		} else if phase == "pressure_80" && (value.RecoveryUsedPercent != 0 || value.RecoveryUsedBytes != 0 ||
			value.RecoveryAvailableBytes != 0 || value.RecoveryDataAllocatedBytes != 0 ||
			value.RecoveryDisposition != "" || value.RecoveryGateOutcome != "" ||
			value.RecoveryBallastAllocatedBytes != 0 || value.RecoveryBallastEventOrdinal != 0 ||
			value.RecoveryGateEventOrdinal != 0) {
			return errors.New("pressure 80 retained unrelated recovery evidence")
		} else if phase != "pressure_75" && value.DataAllocatedBytesAtTarget != uint64(metrics[phase].DataAllocatedBytes) {
			return fmt.Errorf("phase %q data allocation differs from its phase gauge", phase)
		}
		wantSequence, err := pressureSequenceSHA256(phase, *value)
		if err != nil || value.GateSequenceSHA256 != wantSequence {
			return fmt.Errorf("phase %q pressure sequence digest is invalid", phase)
		}
		priorSequence = value.GateSequenceSHA256
		priorBallast = value.BallastAllocatedBytesAfter
		priorAvailable = value.VolumeAvailableBytesAfter
		priorDataAllocated = value.DataAllocatedBytesAtTarget
	}
	if outcomes["pressure_75"] == "passed" && (pressure80Capacity == 0 || pressure75Capacity <= pressure80Capacity) {
		return errors.New("pressure recovery lifecycle cycle is not fresh")
	}
	return nil
}

func pressureMutationMatches(
	action string,
	usedBefore, usedAfter, ballastBefore, ballastAfter, dataBefore, dataAfter, tolerance uint64,
) bool {
	var ballastDelta, dataDelta, volumeDelta uint64
	switch action {
	case "add":
		if usedAfter <= usedBefore || ballastAfter <= ballastBefore || dataAfter <= dataBefore {
			return false
		}
		ballastDelta, dataDelta, volumeDelta = ballastAfter-ballastBefore, dataAfter-dataBefore, usedAfter-usedBefore
	case "remove":
		if usedAfter >= usedBefore || ballastAfter >= ballastBefore || dataAfter >= dataBefore {
			return false
		}
		ballastDelta, dataDelta, volumeDelta = ballastBefore-ballastAfter, dataBefore-dataAfter, usedBefore-usedAfter
	default:
		return false
	}
	return ballastDelta == dataDelta && withinTolerance(volumeDelta, ballastDelta, tolerance)
}

func withinTolerance(observed, expected, tolerance uint64) bool {
	if observed >= expected {
		return observed-expected <= tolerance
	}
	return expected-observed <= tolerance
}

func validatePressureLifecycle(value PressureTransition, metrics ReceiptMetrics, plan Plan) error {
	finalRows, err := validateLifecycleOwners(value.Owners, plan, value.LifecycleFenceUnixMS, value.CapacityObservedUnixMS)
	if err != nil {
		return err
	}
	total := lifecycleAggregate{
		scanned: value.LifecycleScanned, deleted: value.LifecycleDeleted,
		logicalBytes: value.LifecycleLogicalBytes, rootBytes: value.LifecycleRootBytes,
		memberBytes: value.LifecycleMemberBytes,
	}
	if err := validateLifecycleTotals(
		total, finalRows, value.LifecycleOwnerTurns, uint64(len(plan.WorkEnvelope.LifecycleOwners)),
	); err != nil {
		return err
	}
	want, err := pressureLifecycleCycleSHA256(value)
	if err != nil || value.LifecycleCycleSHA256 != want ||
		metrics.LifecycleOwnerTurns != CountMetric(value.LifecycleOwnerTurns) ||
		metrics.LifecycleDeleted != CountMetric(value.LifecycleDeleted) {
		return errors.New("pressure lifecycle cumulative evidence is invalid")
	}
	return nil
}

func validatePressure80Lifecycle(value PressureTransition, metrics ReceiptMetrics, plan Plan) error {
	if err := validatePressureLifecycle(value, metrics, plan); err != nil {
		return err
	}
	if !correctedPlanSemantics(plan.Schema) {
		return nil
	}
	index := slices.IndexFunc(value.Owners, func(owner LifecycleOwnerResult) bool {
		return owner.Name == lifecycle.JobOwner
	})
	if index < 0 || value.Owners[index].Backlog {
		return errors.New("pressure 80 durable-job lifecycle owner is not drained")
	}
	return nil
}

func pressureLifecycleCycleSHA256(value PressureTransition) (string, error) {
	return receiptSHA256(struct {
		Scanned, Deleted, LogicalBytes, RootBytes, MemberBytes, OwnerTurns   uint64
		FenceUnixMS, CapacityUnixMS, FenceEventOrdinal, CapacityEventOrdinal uint64
		Owners                                                               []LifecycleOwnerResult
	}{
		value.LifecycleScanned, value.LifecycleDeleted, value.LifecycleLogicalBytes,
		value.LifecycleRootBytes, value.LifecycleMemberBytes, value.LifecycleOwnerTurns,
		value.LifecycleFenceUnixMS, value.CapacityObservedUnixMS,
		value.LifecycleFenceEventOrdinal, value.CapacityObservedEventOrdinal, value.Owners,
	})
}

func pressureSequenceSHA256(phase string, value PressureTransition) (string, error) {
	value.GateSequenceSHA256 = ""
	return receiptSHA256(struct {
		Phase string             `json:"phase"`
		Value PressureTransition `json:"value"`
	}{Phase: phase, Value: value})
}

func namedTransition(values []TransitionResult, phase string) (TransitionResult, bool) {
	index := slices.IndexFunc(values, func(value TransitionResult) bool { return value.Phase == phase })
	if index < 0 {
		return TransitionResult{}, false
	}
	return values[index], true
}

func authorityIdentitySHA256(value AuthorityPhaseResult) (string, bool) {
	if value.Outcome != "passed" || !value.Current {
		return "", false
	}
	value.Phase, value.Outcome = "", ""
	digest, err := receiptSHA256(value)
	return digest, err == nil
}

func interruptedPublicationAuthorityAtHitSHA256(
	prior, final AuthorityPhaseResult,
	plan Plan,
) (string, bool) {
	if !correctedPlanSemantics(plan.Schema) {
		return authorityIdentitySHA256(prior)
	}
	hit := final
	hit.CallerGenerationSHA256 = prior.CallerGenerationSHA256
	hit.CallerRootSHA256 = prior.CallerRootSHA256
	hit.RelationshipGenerationSHA256 = prior.RelationshipGenerationSHA256
	hit.RelationshipRootSHA256 = prior.RelationshipRootSHA256
	hit.RelationshipProvenanceSHA256 = prior.RelationshipProvenanceSHA256
	return authorityIdentitySHA256(hit)
}

func staleLeaseAuthorityAtHitSHA256(prior, final AuthorityPhaseResult) (string, bool) {
	priorSHA256, priorOK := authorityIdentitySHA256(prior)
	finalSHA256, finalOK := authorityIdentitySHA256(final)
	return priorSHA256, priorOK && finalOK && priorSHA256 == finalSHA256
}

func validInterruptedPublicationTargetMapping(value InjectionTargetProjection, plan Plan) bool {
	if !correctedPlanSemantics(plan.Schema) {
		return value.PlanSHA256 == "" && value.ScheduleSHA256 == value.GenerationSHA256
	}
	return validDigest(value.PlanSHA256) &&
		value.PlanSHA256 != value.GenerationSHA256 &&
		value.ScheduleSHA256 != value.GenerationSHA256 &&
		value.ScheduleSHA256 != value.PlanSHA256
}

func expectedArchiveSemanticStateSHA256(plan Plan) (string, error) {
	logical, ok := namedLogicalRevision(plan.Revisions.Logical, "a-return")
	if !ok {
		return "", errors.New("a-return logical revision is absent")
	}
	return receiptSHA256(struct {
		Schema               string               `json:"schema"`
		PhysicalRevision     string               `json:"physical_revision"`
		LogicalRevision      string               `json:"logical_revision"`
		CatalogLogicalSHA256 string               `json:"catalog_logical_sha256"`
		SemanticSHA256       string               `json:"semantic_sha256"`
		CatalogSource        CatalogSourceProfile `json:"catalog_source"`
		Catalog              SetIdentity          `json:"catalog"`
		Memberships          SetIdentity          `json:"memberships"`
		Placements           SetIdentity          `json:"placements"`
		UnownedPrefixes      SetIdentity          `json:"unowned_prefixes"`
		Relationships        ProductRelationships `json:"relationships"`
	}{
		Schema: "t422-archive-semantic-state-v1", PhysicalRevision: "a-return", LogicalRevision: "a-return",
		CatalogLogicalSHA256: logical.CatalogLogicalSHA256, SemanticSHA256: logical.SemanticSHA256,
		CatalogSource: logical.CatalogSource, Catalog: logical.Catalog, Memberships: logical.Memberships,
		Placements: logical.Placements, UnownedPrefixes: logical.UnownedPrefixes,
		Relationships: plan.Oracle.ProductRelationships,
	})
}

func expectedRelationshipSemanticSHA256(plan Plan) (string, error) {
	return receiptSHA256(struct {
		Schema   string               `json:"schema"`
		Families []RelationshipFamily `json:"families"`
		Product  ProductRelationships `json:"product"`
	}{
		Schema:   "t422-relationship-semantic-state-v1",
		Families: plan.Oracle.Relationships,
		Product:  plan.Oracle.ProductRelationships,
	})
}

func validateQueryEvidence(value QueryEvidence, outcome, authoritySHA256 string, metrics ReceiptMetrics, plan Plan) error {
	if value.Phase != "product_queries" || value.Outcome != outcome ||
		len(value.Results) > len(plan.Oracle.QueryCases) {
		return errors.New("T42.2 query result inventory is invalid")
	}
	if outcome != "passed" {
		if value.Results != nil {
			return errors.New("T42.2 incomplete query phase retained results")
		}
		return nil
	}
	if len(value.Results) != len(plan.Oracle.QueryCases) {
		return errors.New("T42.2 passed query result inventory is incomplete")
	}
	workIndex := slices.IndexFunc(plan.WorkEnvelope.Phases, func(value PhaseWorkBounds) bool {
		return value.Phase == "product_queries"
	})
	if workIndex < 0 {
		return errors.New("T42.2 product query work bounds are absent")
	}
	workBounds := plan.WorkEnvelope.Phases[workIndex]
	var controlReads, memberReads uint64
	for index, result := range value.Results {
		if err := validateQueryResult(
			result, plan.Oracle.QueryCases[index], plan.ReceiptContract.QueryTransportSchema, authoritySHA256, workBounds, plan.Schema,
		); err != nil {
			return fmt.Errorf("T42.2 query result %q differs from the frozen oracle", result.Name)
		}
		for _, transport := range []QueryTransportResult{result.HTTP, result.MCP} {
			if transport.ControlReads > math.MaxUint64-controlReads || transport.MemberReads > math.MaxUint64-memberReads {
				return errors.New("T42.2 query read accounting overflowed")
			}
			controlReads += transport.ControlReads
			memberReads += transport.MemberReads
		}
	}
	if controlReads > uint64(metrics.ControlReads) || memberReads > uint64(metrics.MemberReads) {
		return errors.New("T42.2 request-local query reads exceed the product phase meter")
	}
	return nil
}

func validateRelationshipEvidence(
	value RelationshipEvidence,
	outcome, authoritySHA256 string,
	authority AuthorityPhaseResult,
	plan Plan,
) error {
	if value.Phase != "product_queries" || value.Outcome != outcome ||
		len(value.Results) > len(plan.Oracle.Relationships) {
		return errors.New("T42.2 relationship result inventory is invalid")
	}
	if outcome != "passed" {
		if value.Results != nil || value.Caller != nil || value.Product != nil || value.AuthorityBeforeSHA256 != "" ||
			value.AuthorityAfterSHA256 != "" || value.RelationshipRootReads != 0 ||
			value.RelationshipGenerationReads != 0 {
			return errors.New("T42.2 incomplete relationship phase retained results")
		}
		return nil
	}
	if len(value.Results) != len(plan.Oracle.Relationships) || value.Caller == nil || value.Product == nil ||
		validateCallerPublication(*value.Caller, authority, plan.Oracle.ProductRelationships) != nil ||
		*value.Product != expectedProductRelationshipResult(plan.Oracle.ProductRelationships) ||
		value.AuthorityBeforeSHA256 != authoritySHA256 || value.AuthorityAfterSHA256 != authoritySHA256 ||
		value.RelationshipRootReads != 1 || value.RelationshipGenerationReads != 1 {
		return errors.New("T42.2 passed relationship results differ from the frozen oracle")
	}
	for index, result := range value.Results {
		if result != expectedRelationshipResult(plan.Oracle.Relationships[index]) {
			return fmt.Errorf("T42.2 relationship result %q differs from the frozen oracle", result.Name)
		}
	}
	return nil
}

func validateCallerPublication(
	value CallerPublicationResult,
	authority AuthorityPhaseResult,
	expected ProductRelationships,
) error {
	rootIndex := slices.IndexFunc(authority.ExtractionRoots, func(root ExtractionRootResult) bool {
		return root.Domain == "grpc-caller"
	})
	leavesSHA256, leavesErr := receiptSHA256(value.Leaves)
	binding := value
	binding.ComponentBindingSHA256 = ""
	bindingSHA256, bindingErr := receiptSHA256(binding)
	if rootIndex < 0 || leavesErr != nil || bindingErr != nil ||
		value.Schema != "t422-global-caller-publication-v1" || value.ExecutionPolicy != expected.GlobalCallerPolicy ||
		value.CandidateInventory != authority.ExtractionRoots[rootIndex].Candidates ||
		value.ResolverCatalogGenerationSHA256 != authority.ResolverCatalogGenerationSHA256 ||
		value.ResolverCatalogRootSHA256 != authority.ResolverCatalogRootSHA256 ||
		value.ResolverDeclarationRecords != 10_100 || value.GeneratedDescriptors != 10_100 ||
		value.GenerationSHA256 != authority.CallerGenerationSHA256 || value.RootSHA256 != authority.CallerRootSHA256 ||
		!validDigest(value.ManifestSHA256) || !value.Current || value.LeavesSHA256 != leavesSHA256 ||
		len(value.Leaves) != len(expected.CallerLeaves) ||
		value.ResolvedPostings != expected.RPCProjections || value.UnresolvedPostings != 0 ||
		value.Abstentions != 11_603 || value.Records != 22_602 ||
		value.CanonicalBytes != 21_656_043 || value.EncodedBytes != 21_656_043 ||
		value.Projection != expected.ExpectedRPCProjections ||
		value.RelationshipGenerationSHA256 != authority.RelationshipGenerationSHA256 ||
		value.RelationshipRootSHA256 != authority.RelationshipRootSHA256 ||
		value.ComponentBindingSHA256 != bindingSHA256 {
		return errors.New("global caller publication is not bound to native product authority")
	}
	var candidates, resolved, abstentions, records, canonicalBytes, encodedBytes uint64
	for index, leaf := range value.Leaves {
		exact := expected.CallerLeaves[index]
		if leaf.Prefix != exact.Prefix || leaf.CandidateRecords != exact.CandidateRecords ||
			leaf.ResolvedPostings != exact.ResolvedPostings || leaf.Abstentions != exact.Abstentions ||
			leaf.Records != exact.Records || leaf.CanonicalBytes != exact.CanonicalBytes ||
			leaf.EncodedBytes != exact.EncodedBytes || leaf.Outcome != "success" || leaf.Unresolved != 0 ||
			!validDigest(leaf.ResultSHA256) || leaf.CandidateRecords > math.MaxUint64-candidates ||
			leaf.ResolvedPostings > math.MaxUint64-resolved || leaf.Abstentions > math.MaxUint64-abstentions ||
			leaf.Records > math.MaxUint64-records || leaf.CanonicalBytes > math.MaxUint64-canonicalBytes ||
			leaf.EncodedBytes > math.MaxUint64-encodedBytes {
			return errors.New("global caller publication leaf differs from the frozen ordinary-worker replay")
		}
		candidates += leaf.CandidateRecords
		resolved += leaf.ResolvedPostings
		abstentions += leaf.Abstentions
		records += leaf.Records
		canonicalBytes += leaf.CanonicalBytes
		encodedBytes += leaf.EncodedBytes
	}
	if candidates != expected.CallerCandidateRecords || resolved != value.ResolvedPostings ||
		abstentions != value.Abstentions || records != value.Records ||
		canonicalBytes != value.CanonicalBytes || encodedBytes != value.EncodedBytes {
		return errors.New("global caller publication aggregate differs from the frozen oracle")
	}
	return nil
}

func validateQueryResult(
	result QueryResult,
	value QueryCase,
	schema, authoritySHA256 string,
	workBounds PhaseWorkBounds,
	planSchema string,
) error {
	pages := uint64(1)
	if value.PageSize > 0 && value.ExpectedRecords > 0 {
		pages = (value.ExpectedRecords + value.PageSize - 1) / value.PageSize
	}
	if result.Name != value.Name || value.MCPTool == "" {
		return errors.New("query inventory is invalid")
	}
	wantCodes := []string{fmt.Sprint(value.ExpectedStatus), value.ExpectedMCPCode}
	transports := []QueryTransportResult{result.HTTP, result.MCP}
	for index, transport := range transports {
		denied := value.ExpectedStatus == 404
		deniedControlReads := uint64(0)
		if denied && value.Surface == "service_search" {
			deniedControlReads = 1
		}
		memberReadsValid := validQueryMemberReads(planSchema, value, index, transport.MemberReads, workBounds.MemberReads.Maximum)
		readsValid := denied && transport.ControlReads == deniedControlReads && transport.MemberReads == 0 ||
			!denied && transport.ControlReads >= 1 && transport.ControlReads <= workBounds.ControlReads.Maximum &&
				memberReadsValid
		if transport.Schema != schema || transport.Code != wantCodes[index] ||
			transport.Pages != pages || transport.Records != value.ExpectedRecords ||
			transport.Paths != value.ExpectedPaths || transport.ProjectionSHA256 != value.ProjectionSHA256 ||
			!readsValid ||
			!transport.PaginationClosedExactly || transport.AuthorizationDecisions != 1 ||
			transport.AuthorizationDecision != value.Authorization ||
			transport.AuthoritySnapshots != 1 ||
			transport.AuthorityBeforeSHA256 != authoritySHA256 ||
			transport.AuthorityAfterSHA256 != authoritySHA256 {
			return errors.New("query transport result is invalid")
		}
		if denied && transport.AuthorizedRepositories != 0 || !denied && transport.AuthorizedRepositories != 1 {
			return errors.New("query authorization cardinality is invalid")
		}
	}
	if result.HTTP.ProjectionSHA256 != result.MCP.ProjectionSHA256 ||
		result.HTTP.Records != result.MCP.Records || result.HTTP.Paths != result.MCP.Paths ||
		result.HTTP.AuthorityBeforeSHA256 != result.MCP.AuthorityBeforeSHA256 {
		return errors.New("HTTP and MCP query projections differ")
	}
	return nil
}

func validQueryMemberReads(planSchema string, query QueryCase, transportIndex int, reads, maximum uint64) bool {
	if reads > maximum {
		return false
	}
	if !correctedPlanSemantics(planSchema) {
		return reads >= 1
	}
	if query.Surface == "service_relationships" || query.Name == "first_service" && transportIndex == 0 {
		return reads >= 1
	}
	return reads == 0
}

func authorityResultSHA256(values []AuthorityPhaseResult, phase string) (string, error) {
	index := slices.IndexFunc(values, func(value AuthorityPhaseResult) bool { return value.Phase == phase })
	if index < 0 || values[index].Outcome != "passed" {
		return "", errors.New("T42.2 passed query phase authority is absent")
	}
	return receiptSHA256(values[index])
}

func expectedQueryResult(value QueryCase, schema, authoritySHA256, planSchema string) QueryResult {
	pages := uint64(1)
	if value.PageSize > 0 && value.ExpectedRecords > 0 {
		pages = (value.ExpectedRecords + value.PageSize - 1) / value.PageSize
	}
	transport := func(code string, index int) QueryTransportResult {
		result := QueryTransportResult{
			Schema: schema, Code: code, Pages: pages, Records: value.ExpectedRecords,
			Paths: value.ExpectedPaths, ProjectionSHA256: value.ProjectionSHA256,
			PaginationClosedExactly: true, AuthorizationDecisions: 1,
			AuthorizationDecision: value.Authorization,
			AuthoritySnapshots:    1,
			AuthorityBeforeSHA256: authoritySHA256, AuthorityAfterSHA256: authoritySHA256,
		}
		if value.ExpectedStatus == 404 && value.Surface == "service_search" {
			result.ControlReads = 1
		} else if value.ExpectedStatus != 404 {
			result.AuthorizedRepositories = 1
			result.ControlReads = 1
			if !correctedPlanSemantics(planSchema) || value.Surface == "service_relationships" || value.Name == "first_service" && index == 0 {
				result.MemberReads = 1
			}
		}
		return result
	}
	return QueryResult{
		Name: value.Name,
		HTTP: transport(fmt.Sprint(value.ExpectedStatus), 0),
		MCP:  transport(value.ExpectedMCPCode, 1),
	}
}

func expectedRelationshipResult(value RelationshipFamily) RelationshipResult {
	return RelationshipResult{
		Name: value.Name, SemanticPairEdges: value.SemanticPairEdges,
		MaxInDegree: value.MaxInDegree, MaxOutDegree: value.MaxOutDegree,
		Acyclic: value.Acyclic, ObservedEdgesFramedBytes: value.ExpectedEdges.FramedBytes,
		ObservedEdgesSHA256: value.ExpectedEdges.SHA256,
	}
}

func expectedProductRelationshipResult(value ProductRelationships) ProductRelationshipResult {
	return ProductRelationshipResult{
		RPCProjections:           value.RPCProjections,
		KafkaProducerProjections: value.KafkaProducerProjections,
		KafkaConsumerProjections: value.KafkaConsumerProjections,
		TotalProjections:         value.TotalProjections,
		ServiceReferences:        value.ServiceReferences,
		Canonicalization:         value.Canonicalization,
		ProjectionRecords:        value.ExpectedProjections.Records,
		ProjectionFramedBytes:    value.ExpectedProjections.FramedBytes,
		ProjectionSHA256:         value.ExpectedProjections.SHA256,
	}
}

func validateReceiptTeardown(
	value ReceiptTeardown,
	measurements []PhaseMeasurement,
	plan Plan,
	freeze ExecutionFreeze,
	ownedServerStarted bool,
) (bool, error) {
	contract, rule := plan.ReceiptContract, plan.Teardown
	if !value.Attempted || value.BackingVolumeIdentity != freeze.Host.BackingVolumeIdentity ||
		!slices.Contains(contract.TeardownOutcomes, value.Outcome) {
		return false, errors.New("T42.2 teardown authority is invalid")
	}
	if !validUnavailableMetricsForPlan(value.MeasurementUnavailable, plan.Schema) ||
		uint64(len(value.MeasurementUnavailable)) != value.MeasurementErrors {
		return false, errors.New("T42.2 teardown measurement-error inventory is invalid")
	}
	failed := make([]string, 0, 20)
	add := func(name string, condition bool) {
		if condition {
			failed = append(failed, name)
		}
	}
	add("teardown_incomplete", !value.Completed)
	add("descendants_not_stopped", rule.StopDescendants && (!value.DescendantsStopped || value.DescendantStopErrors != 0))
	add("store_not_closed", rule.CloseStore && (!value.StoreClosed || value.StoreCloseErrors != 0))
	add("derived_custody_not_removed", rule.RemoveDerivedCustody && (value.DerivedCustodyPaths != 0 || value.DerivedRemovalErrors != 0))
	add("scratch_source_not_removed", rule.RemoveScratchSource && (value.ScratchSourcePaths != 0 || value.ScratchRemovalErrors != 0))
	add("children_remain", rule.RequireZeroChildren && value.ChildrenRemaining != 0)
	if plan.Schema == PlanV3Schema {
		var err error
		failed, err = appendAccountingTeardownFailures(failed, value, measurements, ownedServerStarted)
		if err != nil {
			return false, err
		}
	}
	add("pressure_ballast_not_removed", value.PressureBallastBytes != 0 || value.BallastRemovalErrors != 0)
	add("pressure_volume_not_detached", !value.PressureVolumeDetached || value.VolumeDetachErrors != 0)
	add("pressure_image_not_removed", value.PressureImagePaths != 0 || !value.PressureImageRemoved || value.ImageRemovalErrors != 0)
	add("backing_custody_not_removed", value.BackingDerivedCustodyPaths != 0)
	add("source_free_custody_not_established", rule.RetainSourceFreeOnly && !value.RetainedSourceFreeOnly)
	for _, metric := range value.MeasurementUnavailable {
		failed = append(failed, "measurement_"+metric+"_unavailable")
	}
	teardownIndex := slices.IndexFunc(measurements, func(measurement PhaseMeasurement) bool {
		return measurement.Phase == "teardown"
	})
	deadlineIndex := slices.IndexFunc(plan.PhaseDeadlines, func(deadline PhaseDeadline) bool {
		return deadline.Phase == "teardown"
	})
	if teardownIndex < 0 || deadlineIndex < 0 {
		return false, errors.New("T42.2 teardown measurement or deadline is absent")
	}
	add("teardown_deadline_exceeded", uint64(measurements[teardownIndex].Metrics.WallMS) > plan.PhaseDeadlines[deadlineIndex].DeadlineMS)
	var preTeardownWall, totalWall uint64
	for index, measurement := range measurements {
		wall := uint64(measurement.Metrics.WallMS)
		if wall > math.MaxUint64-totalWall {
			return false, errors.New("T42.2 teardown wall accounting overflowed")
		}
		totalWall += wall
		if index < teardownIndex {
			preTeardownWall = totalWall
		}
	}
	add("teardown_total_wall_ceiling_exceeded",
		preTeardownWall <= plan.SafetyEnvelope.MaximumTotalWallMS && totalWall > plan.SafetyEnvelope.MaximumTotalWallMS)
	slices.Sort(failed)
	if len(failed) == 0 {
		if value.Outcome != "clean" || value.Failure != nil {
			return false, errors.New("T42.2 clean teardown outcome is invalid")
		}
		return true, nil
	}
	if value.Outcome != "failed" || value.Failure == nil {
		return false, errors.New("T42.2 failed teardown lacks exact failure evidence")
	}
	wantKind := "multiple"
	if len(failed) == 1 {
		wantKind = failed[0]
	}
	failure := *value.Failure
	evidence := failure
	evidence.EvidenceSHA256 = ""
	wantSHA256, err := receiptSHA256(evidence)
	if err != nil || failure.Schema != contract.TeardownFailureSchema || failure.Kind != wantKind ||
		!slices.Equal(failure.FailedChecks, failed) || failure.EvidenceSHA256 != wantSHA256 {
		return false, errors.New("T42.2 teardown failure evidence is invalid")
	}
	return false, nil
}

type authorityState struct {
	PhysicalRevision string
	LogicalRevision  string
}

func expectedAuthorityStates(plan Plan) (map[string]authorityState, error) {
	result := make(map[string]authorityState, len(plan.PhaseOrder)-2)
	for _, phase := range plan.PhaseOrder[1 : len(plan.PhaseOrder)-1] {
		index := slices.IndexFunc(plan.PhaseStates, func(value PhaseState) bool {
			return value.Phase == phase
		})
		if index < 0 || plan.PhaseStates[index].PhysicalRevision == "" ||
			plan.PhaseStates[index].LogicalRevision == "" {
			return nil, fmt.Errorf("T42.2 phase %q lacks an authority state", phase)
		}
		result[phase] = authorityState{
			PhysicalRevision: plan.PhaseStates[index].PhysicalRevision,
			LogicalRevision:  plan.PhaseStates[index].LogicalRevision,
		}
	}
	return result, nil
}

func validateAuthorityResults(
	values []AuthorityPhaseResult,
	phases []string,
	outcomes map[string]string,
	expected map[string]authorityState,
	revisions []RevisionResult,
	plan Plan,
) error {
	if len(values) != len(phases) {
		return errors.New("inventory is incomplete")
	}
	passed := make(map[string]AuthorityPhaseResult, len(values))
	for index, phase := range phases {
		value, state := values[index], expected[phase]
		if value.Phase != phase || value.Outcome != outcomes[phase] {
			return fmt.Errorf("phase %q binding is invalid", phase)
		}
		if plan.Schema == PlanSchema && value.RelationshipProvenanceSHA256 != "" {
			return fmt.Errorf("phase %q retained prospective provenance in a V1 receipt", phase)
		}
		if value.Outcome == "not_run" {
			if value.PhysicalRevision != "" || value.LogicalRevision != "" ||
				value.PhysicalCommit != "" || value.PhysicalTree != "" ||
				value.SourceGenerationSHA256 != "" || value.SearchGenerationSHA256 != "" ||
				value.ObservationGenerationSHA256 != "" ||
				value.CatalogRootSHA256 != "" || value.CatalogActivationPlanSHA256 != "" ||
				value.CatalogActivationScheduleSHA256 != "" || value.CatalogActivationUnitSHA256 != "" ||
				value.ResolverCatalogGenerationSHA256 != "" ||
				value.ResolverCatalogRootSHA256 != "" || value.CallerGenerationSHA256 != "" ||
				value.CallerRootSHA256 != "" || value.RelationshipGenerationSHA256 != "" ||
				value.RelationshipRootSHA256 != "" || value.RelationshipProvenanceSHA256 != "" || value.SearchInventory != (SetIdentity{}) ||
				value.ObservationInputInventory != (SetIdentity{}) || value.ExtractionRootsSHA256 != "" ||
				value.ExtractionRoots != nil || value.Current {
				return fmt.Errorf("phase %q retained not-run authority", phase)
			}
			continue
		}
		if value.PhysicalRevision != state.PhysicalRevision || value.LogicalRevision != state.LogicalRevision {
			return fmt.Errorf("phase %q revision binding is invalid", phase)
		}
		physical, physicalOK := namedRevisionResult(revisions, state.PhysicalRevision)
		logical, logicalOK := namedRevisionResult(revisions, state.LogicalRevision)
		physicalPlan, physicalPlanOK := namedPhysicalRevision(plan.Revisions.Physical, state.PhysicalRevision)
		if !physicalOK || !logicalOK || !physicalPlanOK {
			return fmt.Errorf("phase %q revision result is absent", phase)
		}
		switch value.Outcome {
		case "passed":
			if physical.PhysicalOutcome != "passed" || logical.LogicalOutcome != "passed" ||
				value.PhysicalCommit != physical.PhysicalCommit || value.PhysicalTree != physical.PhysicalTree ||
				!validDigest(value.SourceGenerationSHA256) || !validDigest(value.SearchGenerationSHA256) ||
				!validDigest(value.ObservationGenerationSHA256) || !validDigest(value.CandidateGenerationSHA256) ||
				!validDigest(value.CatalogRootSHA256) ||
				!validDigest(value.CatalogActivationPlanSHA256) ||
				!validDigest(value.CatalogActivationScheduleSHA256) || !validDigest(value.CatalogActivationUnitSHA256) ||
				!validDigest(value.ResolverCatalogGenerationSHA256) || !validDigest(value.ResolverCatalogRootSHA256) ||
				!validDigest(value.CallerGenerationSHA256) || !validDigest(value.CallerRootSHA256) ||
				!validDigest(value.RelationshipGenerationSHA256) ||
				correctedPlanSemantics(plan.Schema) && !validDigest(value.RelationshipProvenanceSHA256) ||
				!validDigest(value.RelationshipRootSHA256) || !value.Current ||
				validateAuthorityCoverage(value, physicalPlan, plan) != nil {
				return fmt.Errorf("phase %q authority is not exact", phase)
			}
			passed[phase] = value
		case "stopped":
			if !optionalCommit(value.PhysicalCommit) || !optionalCommit(value.PhysicalTree) ||
				!optionalDigest(value.SourceGenerationSHA256) || !optionalDigest(value.SearchGenerationSHA256) ||
				!optionalDigest(value.ObservationGenerationSHA256) || !optionalDigest(value.CandidateGenerationSHA256) ||
				!optionalDigest(value.CatalogRootSHA256) ||
				!optionalDigest(value.CatalogActivationPlanSHA256) ||
				!optionalDigest(value.CatalogActivationScheduleSHA256) || !optionalDigest(value.CatalogActivationUnitSHA256) ||
				!optionalDigest(value.ResolverCatalogGenerationSHA256) || !optionalDigest(value.ResolverCatalogRootSHA256) ||
				!optionalDigest(value.CallerGenerationSHA256) || !optionalDigest(value.CallerRootSHA256) ||
				!optionalDigest(value.RelationshipGenerationSHA256) ||
				!optionalDigest(value.RelationshipRootSHA256) || !optionalDigest(value.RelationshipProvenanceSHA256) {
				return fmt.Errorf("phase %q stopped authority is invalid", phase)
			}
			if physical.PhysicalOutcome == "passed" &&
				(value.PhysicalCommit != physical.PhysicalCommit || value.PhysicalTree != physical.PhysicalTree ||
					!optionalDigest(value.SearchGenerationSHA256) || !optionalDigest(value.ObservationGenerationSHA256)) {
				return fmt.Errorf("phase %q stopped physical authority is not exact", phase)
			}
			if value.Current {
				if err := validateAuthorityCoverage(value, physicalPlan, plan); err != nil {
					return fmt.Errorf("phase %q stopped current authority coverage is invalid", phase)
				}
			} else if value.CandidateGenerationSHA256 != "" || value.SearchInventory != (SetIdentity{}) ||
				value.ObservationInputInventory != (SetIdentity{}) ||
				value.ExtractionRootsSHA256 != "" || value.ExtractionRoots != nil {
				return fmt.Errorf("phase %q retained incomplete authority coverage", phase)
			}
		default:
			return fmt.Errorf("phase %q outcome is invalid", phase)
		}
	}
	if err := validateAuthorityContinuity(passed, plan); err != nil {
		return err
	}
	return nil
}

func validateAuthorityCoverage(value AuthorityPhaseResult, physical PhysicalRevision, plan Plan) error {
	if value.SearchInventory != physical.ExpectedTreeInventory ||
		value.ObservationInputInventory != physical.ExpectedObservationInputInventory ||
		len(value.ExtractionRoots) != len(plan.ReceiptContract.ExtractionDomains) {
		return errors.New("authority source, search, or observation inventory is incomplete")
	}
	var partitions uint64
	for index, domain := range plan.ReceiptContract.ExtractionDomains {
		root := value.ExtractionRoots[index]
		expected := plan.Profile.Pipeline.ExtractionDomains[index]
		candidates := physical.ExpectedCandidateInventories[index]
		typedRevision, typedRevisionOK := namedTypedScopeRevision(expected.TypedScopeRevisions, physical.Name)
		partitionResultsSHA256, partitionErr := receiptSHA256(root.PartitionResults)
		membersOK := validPossiblyEmptySetIdentity(root.Members)
		if correctedPlanSemantics(plan.Schema) {
			members, err := extractionResultMembers(root.PartitionResults)
			membersOK = err == nil && root.Members == members
		}
		scheduleOK := validDigest(root.ScheduleSHA256)
		if correctedPlanSemantics(plan.Schema) {
			scheduleOK = root.ScheduleSHA256 == ""
		}
		if root.Domain != domain || !root.Current || !validDigest(root.GenerationSHA256) ||
			!validDigest(root.RootSHA256) || root.CandidateGenerationSHA256 != value.CandidateGenerationSHA256 ||
			root.SourceGenerationSHA256 != value.SourceGenerationSHA256 ||
			root.ObservationGenerationSHA256 != value.ObservationGenerationSHA256 ||
			!validDigest(root.PlanSHA256) || !scheduleOK ||
			!membersOK ||
			expected.Domain != domain || candidates.Domain != domain || root.Candidates != candidates.Candidates ||
			root.MemberPartitions != expected.MemberPartitions || root.TypedPartitions != expected.TypedPartitions ||
			root.TypedScopeRecords != expected.TypedScopeRecords ||
			root.TypedScopePathBytes != expected.TypedScopePathBytes ||
			root.TypedScopeEncodedBytes != expected.TypedScopeEncodedBytes ||
			root.TypedPartitions > 0 && (!typedRevisionOK || root.TypedScopeSHA256 != typedRevision.SHA256 ||
				root.TypedScopeContentSHA256 != typedRevision.DescriptorContentSHA256) ||
			root.TypedPartitions == 0 && (typedRevisionOK || root.TypedScopeSHA256 != "" || root.TypedScopeContentSHA256 != "") ||
			root.Reserved != expected.Reserved || root.Totals != expected.Expected ||
			root.ApplicablePartitions != root.MemberPartitions+root.TypedPartitions ||
			root.Members.Records != root.ApplicablePartitions ||
			partitionErr != nil || root.PartitionResultsSHA256 != partitionResultsSHA256 ||
			!extractionPartitionResultsMatch(root.PartitionResults, expected.Partitions) ||
			root.ApplicablePartitions > math.MaxUint64-partitions {
			return errors.New("applicable extraction root is invalid")
		}
		partitions += root.ApplicablePartitions
	}
	rootsSHA256, err := receiptSHA256(value.ExtractionRoots)
	if err != nil || value.ExtractionRootsSHA256 != rootsSHA256 ||
		partitions != plan.Profile.Physical.CombinedModeledPartitions {
		return errors.New("applicable extraction root inventory is not exact")
	}
	return nil
}

func extractionPartitionResultsMatch(values []ExtractionPartitionResult, expected []ExtractionPartitionProfile) bool {
	if len(values) != len(expected) {
		return false
	}
	for index, profile := range expected {
		disposition := "success"
		if profile.Expected == (ResultTotals{}) {
			disposition = "empty"
		}
		value := values[index]
		if value.Ordinal != profile.Ordinal || value.Kind != profile.Kind || value.MemberOrdinal != profile.MemberOrdinal ||
			value.CallerPrefix != profile.CallerPrefix || value.SourceStart != profile.SourceStart ||
			value.SourceEnd != profile.SourceEnd || value.MemberRecordStart != profile.MemberRecordStart ||
			value.MemberRecordEnd != profile.MemberRecordEnd || value.AdmittedRecords != profile.AdmittedRecords ||
			value.Reservation != profile.Reservation || value.Disposition != disposition || value.Totals != profile.Expected ||
			!validDigest(value.PartitionSHA256) || !validDigest(value.ExpectationSHA256) ||
			!validDigest(value.ResultDigestSHA256) || !validDigest(value.ResultIdentitySHA256) {
			return false
		}
	}
	return true
}

func validPossiblyEmptySetIdentity(value SetIdentity) bool {
	return value.FramedBytes > 0 && validDigest(value.SHA256)
}

func validateAuthorityContinuity(values map[string]AuthorityPhaseResult, plan Plan) error {
	equal := func(left, right string) bool { return reflect.DeepEqual(values[left], withPhase(values[right], left)) }
	if _, ok := values["warm_noop"]; ok {
		if _, prior := values["cold"]; !prior || !equal("warm_noop", "cold") {
			return errors.New("warm no-op changed authority")
		}
	}
	if current, ok := values["physical_delta_b"]; ok {
		prior, priorOK := values["warm_noop"]
		if !priorOK || current.PhysicalCommit == prior.PhysicalCommit || current.PhysicalTree == prior.PhysicalTree ||
			correctedPlanSemantics(plan.Schema) && current.RelationshipProvenanceSHA256 == prior.RelationshipProvenanceSHA256 ||
			current.SourceGenerationSHA256 == prior.SourceGenerationSHA256 ||
			current.SearchGenerationSHA256 == prior.SearchGenerationSHA256 ||
			current.ObservationGenerationSHA256 == prior.ObservationGenerationSHA256 ||
			current.CandidateGenerationSHA256 == prior.CandidateGenerationSHA256 ||
			current.CatalogRootSHA256 == prior.CatalogRootSHA256 ||
			current.CatalogActivationPlanSHA256 == prior.CatalogActivationPlanSHA256 ||
			current.CatalogActivationScheduleSHA256 == prior.CatalogActivationScheduleSHA256 ||
			current.CatalogActivationUnitSHA256 == prior.CatalogActivationUnitSHA256 ||
			current.ResolverCatalogGenerationSHA256 == prior.ResolverCatalogGenerationSHA256 ||
			current.ResolverCatalogRootSHA256 == prior.ResolverCatalogRootSHA256 ||
			current.CallerGenerationSHA256 == prior.CallerGenerationSHA256 ||
			current.CallerRootSHA256 == prior.CallerRootSHA256 ||
			current.RelationshipGenerationSHA256 == prior.RelationshipGenerationSHA256 ||
			current.RelationshipRootSHA256 == prior.RelationshipRootSHA256 {
			return errors.New("physical delta did not replace every source-bound authority")
		}
	}
	if current, ok := values["logical_delta_b"]; ok {
		prior, priorOK := values["physical_delta_b"]
		// Repository-level identities exclude logical service catalog state.
		// V1 is retained exactly; the prospective V2 contract follows those
		// production inputs instead of requiring deterministic output changes.
		resolverCallerChanged := current.ResolverCatalogGenerationSHA256 != prior.ResolverCatalogGenerationSHA256 &&
			current.ResolverCatalogRootSHA256 != prior.ResolverCatalogRootSHA256 &&
			current.CallerGenerationSHA256 != prior.CallerGenerationSHA256 &&
			current.CallerRootSHA256 != prior.CallerRootSHA256
		resolverCallerValid := resolverCallerChanged
		if correctedPlanSemantics(plan.Schema) {
			resolverCallerValid = current.ResolverCatalogGenerationSHA256 == prior.ResolverCatalogGenerationSHA256 &&
				current.ResolverCatalogRootSHA256 == prior.ResolverCatalogRootSHA256 &&
				current.CallerGenerationSHA256 == prior.CallerGenerationSHA256 &&
				current.CallerRootSHA256 == prior.CallerRootSHA256
		}
		if !priorOK || current.PhysicalCommit != prior.PhysicalCommit || current.PhysicalTree != prior.PhysicalTree ||
			correctedPlanSemantics(plan.Schema) && current.RelationshipProvenanceSHA256 != prior.RelationshipProvenanceSHA256 ||
			current.SourceGenerationSHA256 != prior.SourceGenerationSHA256 ||
			current.SearchGenerationSHA256 != prior.SearchGenerationSHA256 ||
			current.ObservationGenerationSHA256 != prior.ObservationGenerationSHA256 ||
			current.CandidateGenerationSHA256 != prior.CandidateGenerationSHA256 ||
			current.ExtractionRootsSHA256 != prior.ExtractionRootsSHA256 ||
			!reflect.DeepEqual(current.ExtractionRoots, prior.ExtractionRoots) ||
			current.CatalogRootSHA256 == prior.CatalogRootSHA256 ||
			current.CatalogActivationPlanSHA256 == prior.CatalogActivationPlanSHA256 ||
			current.CatalogActivationScheduleSHA256 == prior.CatalogActivationScheduleSHA256 ||
			current.CatalogActivationUnitSHA256 == prior.CatalogActivationUnitSHA256 ||
			!resolverCallerValid ||
			current.RelationshipGenerationSHA256 == prior.RelationshipGenerationSHA256 ||
			current.RelationshipRootSHA256 == prior.RelationshipRootSHA256 {
			return errors.New("logical delta changed physical authority or reused logical authority")
		}
	}
	if current, ok := values["return_a"]; ok {
		prior, priorOK := values["logical_delta_b"]
		cold, coldOK := values["cold"]
		if !priorOK || !coldOK || current.PhysicalTree != cold.PhysicalTree ||
			current.PhysicalCommit == prior.PhysicalCommit ||
			correctedPlanSemantics(plan.Schema) && current.RelationshipProvenanceSHA256 == prior.RelationshipProvenanceSHA256 ||
			current.SourceGenerationSHA256 == prior.SourceGenerationSHA256 ||
			current.SearchGenerationSHA256 == prior.SearchGenerationSHA256 ||
			current.ObservationGenerationSHA256 == prior.ObservationGenerationSHA256 ||
			current.CandidateGenerationSHA256 == prior.CandidateGenerationSHA256 ||
			current.CatalogRootSHA256 == prior.CatalogRootSHA256 ||
			current.CatalogActivationPlanSHA256 == prior.CatalogActivationPlanSHA256 ||
			current.CatalogActivationScheduleSHA256 == prior.CatalogActivationScheduleSHA256 ||
			current.CatalogActivationUnitSHA256 == prior.CatalogActivationUnitSHA256 ||
			current.ResolverCatalogGenerationSHA256 == prior.ResolverCatalogGenerationSHA256 ||
			current.ResolverCatalogRootSHA256 == prior.ResolverCatalogRootSHA256 ||
			current.CallerGenerationSHA256 == prior.CallerGenerationSHA256 ||
			current.CallerRootSHA256 == prior.CallerRootSHA256 ||
			current.RelationshipGenerationSHA256 == prior.RelationshipGenerationSHA256 ||
			current.RelationshipRootSHA256 == prior.RelationshipRootSHA256 {
			return errors.New("a-return authority continuity is invalid")
		}
	}
	stable := []string{
		"return_a", "stale_lease",
		"process_restart", "pressure_80", "pressure_90", "pressure_75",
	}
	for index := 1; index < len(stable); index++ {
		if _, ok := values[stable[index]]; ok {
			if _, prior := values[stable[index-1]]; !prior || !equal(stable[index], stable[index-1]) {
				return fmt.Errorf("phase %q changed protected authority", stable[index])
			}
		}
	}
	if current, ok := values["archive_restore"]; ok {
		prior, priorOK := values["pressure_75"]
		if !priorOK || !sameAuthorityExceptRelationship(current, prior) {
			return errors.New("archive restore did not preserve semantic authority")
		}
		if correctedPlanSemantics(plan.Schema) &&
			(current.ResolverCatalogGenerationSHA256 != prior.ResolverCatalogGenerationSHA256 ||
				current.ResolverCatalogRootSHA256 != prior.ResolverCatalogRootSHA256 ||
				current.CallerGenerationSHA256 != prior.CallerGenerationSHA256 ||
				current.CallerRootSHA256 != prior.CallerRootSHA256) {
			return errors.New("archive restore changed immutable resolver or caller authority")
		}
		if correctedPlanSemantics(plan.Schema) {
			sameProvenance := current.RelationshipProvenanceSHA256 == prior.RelationshipProvenanceSHA256
			sameRelationship := current.RelationshipGenerationSHA256 == prior.RelationshipGenerationSHA256 &&
				current.RelationshipRootSHA256 == prior.RelationshipRootSHA256
			changedRelationship := current.RelationshipGenerationSHA256 != prior.RelationshipGenerationSHA256 &&
				current.RelationshipRootSHA256 != prior.RelationshipRootSHA256
			if sameProvenance && !sameRelationship || !sameProvenance && !changedRelationship {
				return errors.New("archive relationship identity does not follow extraction provenance")
			}
		}
	}
	for _, phase := range []string{"lifecycle_collection", "product_queries"} {
		if _, ok := values[phase]; ok {
			prior := "archive_restore"
			if phase == "product_queries" {
				prior = "lifecycle_collection"
			}
			if _, priorOK := values[prior]; !priorOK || !equal(phase, prior) {
				return fmt.Errorf("phase %q changed protected authority", phase)
			}
		}
	}
	return nil
}

func withPhase(value AuthorityPhaseResult, phase string) AuthorityPhaseResult {
	value.Phase = phase
	return value
}

func sameAuthorityExceptRelationship(left, right AuthorityPhaseResult) bool {
	left.Phase, right.Phase = "", ""
	left.RelationshipGenerationSHA256, right.RelationshipGenerationSHA256 = "", ""
	left.RelationshipRootSHA256, right.RelationshipRootSHA256 = "", ""
	left.RelationshipProvenanceSHA256, right.RelationshipProvenanceSHA256 = "", ""
	left.ResolverCatalogGenerationSHA256, right.ResolverCatalogGenerationSHA256 = "", ""
	left.ResolverCatalogRootSHA256, right.ResolverCatalogRootSHA256 = "", ""
	left.CallerGenerationSHA256, right.CallerGenerationSHA256 = "", ""
	left.CallerRootSHA256, right.CallerRootSHA256 = "", ""
	return reflect.DeepEqual(left, right)
}

func validateRevisionResults(values []RevisionResult, outcomes map[string]string, plan Plan) error {
	if len(values) != 3 || len(plan.Revisions.Physical) != 3 || len(plan.Revisions.Logical) != 3 {
		return errors.New("T42.2 revision result inventory is incomplete")
	}
	physicalPhases := []string{"cold", "physical_delta_b", "return_a"}
	logicalPhases := []string{"cold", "logical_delta_b", "return_a"}
	for index, name := range []string{"a", "b", "a-return"} {
		value := values[index]
		physical, physicalOK := namedPhysicalRevision(plan.Revisions.Physical, name)
		logical, logicalOK := namedLogicalRevision(plan.Revisions.Logical, name)
		if !physicalOK || !logicalOK || value.Name != name ||
			value.PhysicalPhase != physicalPhases[index] || value.LogicalPhase != logicalPhases[index] ||
			value.PhysicalOutcome != outcomes[value.PhysicalPhase] ||
			value.LogicalOutcome != outcomes[value.LogicalPhase] ||
			value.PhysicalTreeRecipeSHA256 != physical.SourceTreeRecipeSHA256 ||
			value.PhysicalCommitRecipeSHA256 != physical.SourceCommitRecipeSHA256 ||
			value.CatalogLogicalSHA256 != logical.CatalogLogicalSHA256 ||
			value.SemanticSHA256 != logical.SemanticSHA256 || value.CatalogSource != logical.CatalogSource {
			return fmt.Errorf("T42.2 revision %q binding is invalid", name)
		}
		if err := validatePhysicalRevisionResult(value, plan.Revisions.SourceRecipe.GitObjectFormat); err != nil {
			return fmt.Errorf("T42.2 revision %q: %w", name, err)
		}
		if err := validateAuthoredSourceManifest(value, physical, plan.Revisions.SourceRecipe, plan.Profile); err != nil {
			return fmt.Errorf("T42.2 revision %q authored source: %w", name, err)
		}
	}
	for index := 1; index < len(values); index++ {
		value, parent := values[index], values[index-1]
		if value.PhysicalOutcome == "passed" &&
			(parent.PhysicalOutcome != "passed" || value.PhysicalParentCommit != parent.PhysicalCommit) {
			return fmt.Errorf("T42.2 revision %q parent is invalid", value.Name)
		}
	}
	a, b, aReturn := values[0], values[1], values[2]
	if aReturn.PhysicalOutcome == "passed" {
		if a.PhysicalOutcome != "passed" || b.PhysicalOutcome != "passed" ||
			a.PhysicalTree != aReturn.PhysicalTree || b.PhysicalTree == a.PhysicalTree ||
			a.PhysicalCommit == b.PhysicalCommit || a.PhysicalCommit == aReturn.PhysicalCommit ||
			b.PhysicalCommit == aReturn.PhysicalCommit {
			return errors.New("T42.2 generated physical A-B-A continuity is invalid")
		}
	}
	if aReturn.PhysicalOutcome == "passed" &&
		(a.AuthoredManifest.TreeInventory != aReturn.AuthoredManifest.TreeInventory ||
			b.AuthoredManifest.TreeInventory == a.AuthoredManifest.TreeInventory) {
		return errors.New("T42.2 authored source inventories do not have the exact A-B-A shape")
	}
	return nil
}

func validateAuthoredSourceManifest(
	value RevisionResult,
	physical PhysicalRevision,
	recipe SourceRecipe,
	profile CombinedProfile,
) error {
	if value.PhysicalOutcome != "passed" {
		if value.AuthoredManifest != (AuthoredSourceManifest{}) {
			return errors.New("incomplete physical revision retained an authored manifest")
		}
		return nil
	}
	manifest := value.AuthoredManifest
	overlay := SetIdentity{
		Records: recipe.OverlayRecords, FramedBytes: recipe.OverlayFramedBytes, SHA256: recipe.OverlaySHA256,
	}
	if manifest.Schema != recipe.AuthoredManifestSchema ||
		manifest.BaseCommit != physical.BaseCommit || manifest.BaseTree != physical.BaseTree ||
		manifest.Overlay != overlay || manifest.GeneratedMappingRecords != recipe.GeneratedMappingRecords ||
		manifest.GeneratedMappingPath != recipe.GeneratedMappingPath ||
		manifest.GeneratedMappingMode != recipe.GeneratedMappingMode ||
		manifest.GeneratedMappingBytes != recipe.GeneratedMappingBytes ||
		manifest.GeneratedMappingSHA256 != recipe.GeneratedMappingSHA256 ||
		manifest.TypedInputRecords != recipe.TypedInputRecords ||
		manifest.TypedInputKind != recipe.TypedInputKind || manifest.TypedInputPath != recipe.TypedInputPath ||
		manifest.TypedInputMode != recipe.TypedInputMode || manifest.TypedInputBytes != recipe.TypedInputBytes ||
		manifest.TypedInputSHA256 != recipe.TypedInputSHA256 || manifest.TypedInputBlobOID != recipe.TypedInputBlobOID ||
		manifest.AddedRegularFiles != recipe.OverlayRecords+profile.GeneratedMapping.RegularFiles+profile.TypedIndex.RegularFiles ||
		manifest.RegularFiles != profile.Physical.CombinedRegularFiles ||
		manifest.TreeInventory != physical.ExpectedTreeInventory ||
		manifest.TreeObjectID != value.PhysicalTree || value.PhysicalTree != physical.ExpectedTree ||
		value.PhysicalCommit != physical.ExpectedCommit {
		return errors.New("manifest differs from the frozen source composition")
	}
	commitBytes, err := canonicalGitCommitBytes(value, physical, recipe)
	if err != nil || manifest.CommitBytesSHA256 != SHA256(commitBytes) ||
		value.PhysicalCommit != gitSHA1ObjectID("commit", commitBytes) {
		return errors.New("commit object does not match the frozen canonical recipe")
	}
	return nil
}

func canonicalGitCommitBytes(value RevisionResult, physical PhysicalRevision, recipe SourceRecipe) ([]byte, error) {
	if value.Name != physical.Name {
		return nil, errors.New("commit inputs are invalid")
	}
	return canonicalGitCommitBytesFor(value.PhysicalTree, value.PhysicalParentCommit, physical.CommitMessage, recipe)
}

func gitSHA1ObjectID(kind string, raw []byte) string {
	header := []byte(fmt.Sprintf("%s %d\x00", kind, len(raw)))
	//nolint:gosec // Git's frozen object format is SHA-1; this is identity compatibility, not security.
	digest := sha1.New()
	_, _ = digest.Write(header)
	_, _ = digest.Write(raw)
	return fmt.Sprintf("%x", digest.Sum(nil))
}

func validatePhysicalRevisionResult(value RevisionResult, objectFormat string) error {
	switch value.PhysicalOutcome {
	case "passed":
		if !validGitObjectID(value.PhysicalCommit, objectFormat) ||
			!validGitObjectID(value.PhysicalTree, objectFormat) ||
			value.Name == "a" && value.PhysicalParentCommit != "" {
			return errors.New("physical identity is invalid")
		}
	case "stopped":
		if !optionalGitObjectID(value.PhysicalCommit, objectFormat) ||
			!optionalGitObjectID(value.PhysicalTree, objectFormat) ||
			!optionalGitObjectID(value.PhysicalParentCommit, objectFormat) {
			return errors.New("stopped physical identity is invalid")
		}
	case "not_run":
		if value.PhysicalCommit != "" || value.PhysicalTree != "" || value.PhysicalParentCommit != "" {
			return errors.New("not-run physical identity is present")
		}
	default:
		return errors.New("physical outcome is invalid")
	}
	return nil
}

func namedRevisionResult(values []RevisionResult, name string) (RevisionResult, bool) {
	index := slices.IndexFunc(values, func(value RevisionResult) bool { return value.Name == name })
	if index < 0 {
		return RevisionResult{}, false
	}
	return values[index], true
}

func namedPhysicalRevision(values []PhysicalRevision, name string) (PhysicalRevision, bool) {
	index := slices.IndexFunc(values, func(value PhysicalRevision) bool { return value.Name == name })
	if index < 0 {
		return PhysicalRevision{}, false
	}
	return values[index], true
}

func namedLogicalRevision(values []LogicalRevision, name string) (LogicalRevision, bool) {
	index := slices.IndexFunc(values, func(value LogicalRevision) bool { return value.Name == name })
	if index < 0 {
		return LogicalRevision{}, false
	}
	return values[index], true
}

func namedTypedScopeRevision(values []TypedScopeRevision, name string) (TypedScopeRevision, bool) {
	index := slices.IndexFunc(values, func(value TypedScopeRevision) bool {
		return value.PhysicalRevision == name
	})
	if index < 0 {
		return TypedScopeRevision{}, false
	}
	return values[index], true
}

func validGitObjectID(value, objectFormat string) bool {
	if !validCommit(value) {
		return false
	}
	switch objectFormat {
	case "sha1":
		return len(value) == 40
	case "sha256":
		return len(value) == 64
	default:
		return false
	}
}

func optionalGitObjectID(value, objectFormat string) bool {
	return value == "" || validGitObjectID(value, objectFormat)
}

func optionalCommit(value string) bool { return value == "" || validCommit(value) }

func optionalDigest(value string) bool { return value == "" || validDigest(value) }

type PhaseStateProjection struct {
	Schema                    string                     `json:"schema"`
	Phase                     string                     `json:"phase"`
	PhysicalRevision          string                     `json:"physical_revision"`
	LogicalRevision           string                     `json:"logical_revision"`
	CatalogLogicalSHA256      string                     `json:"catalog_logical_sha256"`
	SemanticSHA256            string                     `json:"semantic_sha256"`
	CatalogSource             CatalogSourceProfile       `json:"catalog_source"`
	Catalog                   SetIdentity                `json:"catalog"`
	MembershipSet             SetIdentity                `json:"membership_set"`
	Placements                SetIdentity                `json:"placements"`
	UnownedPrefixes           SetIdentity                `json:"unowned_prefixes"`
	ServiceQueries            SetIdentity                `json:"service_queries"`
	SearchInventory           SetIdentity                `json:"search_inventory"`
	ObservationInputInventory SetIdentity                `json:"observation_input_inventory"`
	ExtractionRoots           []ExtractionRootProjection `json:"extraction_roots"`
	RelationshipResults       []RelationshipResult       `json:"relationship_results"`
	ProductRelationship       ProductRelationshipResult  `json:"product_relationship"`
}

type ExtractionRootProjection struct {
	Domain                  string       `json:"domain"`
	Availability            string       `json:"availability"`
	ApplicablePartitions    uint64       `json:"applicable_partitions"`
	MemberPartitions        uint64       `json:"member_partitions"`
	TypedPartitions         uint64       `json:"typed_partitions"`
	TypedScopeRecords       uint64       `json:"typed_scope_records"`
	TypedScopePathBytes     uint64       `json:"typed_scope_path_bytes"`
	TypedScopeEncodedBytes  uint64       `json:"typed_scope_encoded_bytes"`
	TypedScopeSHA256        string       `json:"typed_scope_sha256,omitempty"`
	TypedScopeContentSHA256 string       `json:"typed_scope_descriptor_content_sha256,omitempty"`
	Candidates              SetIdentity  `json:"candidates"`
	Reserved                ResultTotals `json:"reserved"`
	Expected                ResultTotals `json:"expected"`
	PartitionShape          SetIdentity  `json:"partition_shape"`
}

type ObservedPhaseState struct {
	Schema                  string                `json:"schema"`
	ProjectionSHA256        string                `json:"projection_sha256"`
	Projection              *PhaseStateProjection `json:"projection,omitempty"`
	AuthoritySnapshotSHA256 string                `json:"authority_snapshot_sha256"`
	SourceAuthorityRecipe   string                `json:"source_authority_recipe"`
	AuthorityReader         string                `json:"authority_reader"`
	SemanticReader          string                `json:"semantic_reader"`
}

func expectedStateProjectionDigests(plan Plan) (map[string]string, error) {
	result := make(map[string]string, len(plan.PhaseOrder)-2)
	for _, state := range plan.PhaseStates[1 : len(plan.PhaseStates)-1] {
		projection, err := expectedStateProjection(plan, state)
		if err != nil {
			return nil, err
		}
		digest, err := receiptSHA256(projection)
		if err != nil {
			return nil, err
		}
		result[state.Phase] = digest
	}
	return result, nil
}

func expectedStateProjectionForPhase(plan Plan, phase string) (PhaseStateProjection, error) {
	index := slices.IndexFunc(plan.PhaseStates, func(state PhaseState) bool { return state.Phase == phase })
	if index < 0 {
		return PhaseStateProjection{}, fmt.Errorf("T42.2 phase %q state is absent", phase)
	}
	return expectedStateProjection(plan, plan.PhaseStates[index])
}

func expectedStateProjection(plan Plan, state PhaseState) (PhaseStateProjection, error) {
	physical, physicalOK := namedPhysicalRevision(plan.Revisions.Physical, state.PhysicalRevision)
	logical, logicalOK := namedLogicalRevision(plan.Revisions.Logical, state.LogicalRevision)
	if !physicalOK || !logicalOK {
		return PhaseStateProjection{}, fmt.Errorf("T42.2 phase %q state revision is absent", state.Phase)
	}
	rootProjections := make([]ExtractionRootProjection, len(plan.Profile.Pipeline.ExtractionDomains))
	for index, domain := range plan.Profile.Pipeline.ExtractionDomains {
		typedRevision, _ := namedTypedScopeRevision(domain.TypedScopeRevisions, physical.Name)
		rootProjections[index] = ExtractionRootProjection{
			Domain: domain.Domain, Availability: domain.Availability,
			ApplicablePartitions: domain.ApplicablePartitions,
			MemberPartitions:     domain.MemberPartitions, TypedPartitions: domain.TypedPartitions,
			TypedScopeRecords: domain.TypedScopeRecords, TypedScopePathBytes: domain.TypedScopePathBytes,
			TypedScopeEncodedBytes: domain.TypedScopeEncodedBytes, TypedScopeSHA256: typedRevision.SHA256,
			TypedScopeContentSHA256: typedRevision.DescriptorContentSHA256,
			Candidates:              physical.ExpectedCandidateInventories[index].Candidates,
			Reserved:                domain.Reserved, Expected: domain.Expected, PartitionShape: domain.PartitionShape,
		}
	}
	relationships := make([]RelationshipResult, len(plan.Oracle.Relationships))
	for index, family := range plan.Oracle.Relationships {
		relationships[index] = expectedRelationshipResult(family)
	}
	return PhaseStateProjection{
		Schema: plan.ReceiptContract.StateProjectionSchema, Phase: state.Phase,
		PhysicalRevision: state.PhysicalRevision, LogicalRevision: state.LogicalRevision,
		CatalogLogicalSHA256: logical.CatalogLogicalSHA256, SemanticSHA256: logical.SemanticSHA256,
		CatalogSource: logical.CatalogSource,
		Catalog:       logical.Catalog, MembershipSet: logical.Memberships,
		Placements: logical.Placements, UnownedPrefixes: logical.UnownedPrefixes,
		ServiceQueries:            logical.ServiceQueries,
		SearchInventory:           physical.ExpectedTreeInventory,
		ObservationInputInventory: physical.ExpectedObservationInputInventory,
		ExtractionRoots:           rootProjections, RelationshipResults: relationships,
		ProductRelationship: expectedProductRelationshipResult(plan.Oracle.ProductRelationships),
	}, nil
}

func receiptSHA256(value any) (string, error) {
	raw, err := MarshalCanonical(value)
	if err != nil {
		return "", err
	}
	return SHA256(raw), nil
}

func receiptNonClaims(value Claims) ReceiptNonClaims {
	return ReceiptNonClaims{
		ChangesProductionBehavior:        value.ChangesProductionBehavior,
		RaisesProductionCap:              value.RaisesProductionCap,
		AuthorizesExecution:              value.AuthorizesExecution,
		AuthorizesPrivateReplay:          value.AuthorizesPrivateReplay,
		EstablishesTargetSLO:             value.EstablishesTargetSLO,
		QualifiesSupportedScale:          value.QualifiesSupportedScale,
		EstablishesAccuracyCompleteness:  value.EstablishesAccuracyCompleteness,
		EstablishesCommitCadence:         value.EstablishesCommitCadence,
		EstablishesQueueCatchup:          value.EstablishesQueueCatchup,
		EstablishesFreshnessUnderCadence: value.EstablishesFreshnessUnderCadence,
		SelectsTopology:                  value.SelectsTopology,
		EstablishesMigrationDecommission: value.EstablishesMigrationDecommission,
		AuthorizesRelease:                value.AuthorizesRelease,
	}
}

func validReceiptFailure(value ReceiptFailure, phase string, plan Plan) bool {
	if value.Phase != phase || value.Observation.Schema != plan.ReceiptContract.FailureObservationSchema ||
		!validDigest(value.Observation.EvidenceSHA256) {
		return false
	}
	observationWithoutDigest := value.Observation
	observationWithoutDigest.EvidenceSHA256 = ""
	evidenceSHA256, err := receiptSHA256(observationWithoutDigest)
	if err != nil || value.Observation.EvidenceSHA256 != evidenceSHA256 {
		return false
	}
	requiresProjection := slices.Contains([]string{
		"lifecycle_error", "internal_error",
	}, value.Code)
	if requiresProjection != (value.Evidence != nil) {
		return false
	}
	want := map[string]string{
		"exact_oracle_mismatch":                      "oracle/exact_mismatch",
		"transition_mismatch":                        "oracle/exact_mismatch",
		"peak_rss_ceiling":                           "resource/gauge_limit",
		"data_allocated_ceiling":                     "resource/gauge_limit",
		"data_logical_ceiling":                       "resource/gauge_limit",
		"total_wall_ceiling":                         "resource/gauge_limit",
		"multiple_resource_ceilings":                 "resource/gauge_limit",
		"phase_work_limit":                           "resource/counter_limit",
		"phase_deadline":                             "resource/gauge_limit",
		"materialized_cartesian_owner_pairs_nonzero": "topology/counter_crossing",
		"direct_recovery_topology_limit":             "topology/counter_limit",
		"measurement_unavailable":                    "internal/measurement_unavailable",
		"internal_error":                             "internal/typed_error",
		"lifecycle_error":                            "lifecycle/typed_error",
	}
	if plan.Schema == PlanV3Schema {
		delete(want, "peak_rss_ceiling")
		want["observed_rss_ceiling"] = "resource/gauge_limit"
	}
	if want[value.Code] != value.Class+"/"+value.Observation.Kind {
		return false
	}
	observation := value.Observation
	if observation.Kind != "measurement_unavailable" && observation.UnavailableMetrics != nil {
		return false
	}
	switch observation.Kind {
	case "exact_mismatch":
		wantMetric := map[string]string{
			"exact_oracle_mismatch": "state_projection",
			"transition_mismatch":   "transition",
		}[value.Code]
		return observation.Metric == wantMetric && validDigest(observation.ExpectedSHA256) &&
			validDigest(observation.ObservedSHA256) && observation.ExpectedSHA256 != observation.ObservedSHA256 &&
			observation.Expected == 0 && observation.Observed == 0 && observation.Limit == 0
	case "counter_limit":
		return safeToken(observation.Metric, 64) && observation.Limit < math.MaxUint64 &&
			observation.Observed == observation.Limit+1 && observation.ExpectedSHA256 == "" &&
			observation.ObservedSHA256 == ""
	case "counter_crossing":
		return observation.Metric == "materialized_cartesian_owner_pairs" &&
			observation.Observed > observation.Limit && observation.ExpectedSHA256 == "" &&
			observation.ObservedSHA256 == ""
	case "gauge_limit":
		return safeToken(observation.Metric, 64) && observation.Observed > observation.Limit &&
			observation.ExpectedSHA256 == "" && observation.ObservedSHA256 == ""
	case "measurement_unavailable":
		return observation.Metric == "" && len(observation.UnavailableMetrics) > 0 &&
			validUnavailableMetricsForPlan(observation.UnavailableMetrics, plan.Schema) && observation.Expected == 0 &&
			observation.Observed == 0 && observation.Limit == 0 &&
			observation.ExpectedSHA256 == "" && observation.ObservedSHA256 == ""
	case "typed_error":
		wantMetric := map[string]string{
			"internal_error":  "internal_error",
			"lifecycle_error": "lifecycle_error",
		}[value.Code]
		return observation.Metric == wantMetric && observation.Expected == 0 && observation.Observed == 0 &&
			observation.Limit == 0 && observation.ExpectedSHA256 == "" && validDigest(observation.ObservedSHA256)
	default:
		return false
	}
}

func safeToken(value string, maximum int) bool {
	if value == "" || len(value) > maximum {
		return false
	}
	for _, current := range value {
		if current < 'a' || current > 'z' {
			if current < '0' || current > '9' {
				if current != '_' && current != '-' && current != '.' {
					return false
				}
			}
		}
	}
	return true
}

func rejectSourceBearingReceipt(raw []byte) error {
	lower := bytes.ToLower(raw)
	for _, fragment := range []string{
		"/users/", "/private/", "/volumes/", "file://", "repository_name", "repository_url",
		"query_text", "result_rows", "raw_error", "clone_url",
		"password", "credential", "hostname", "username", ".go\"", ".proto\"",
		"services/", "structural/", "package ", "func ",
	} {
		if bytes.Contains(lower, []byte(fragment)) {
			return fmt.Errorf("T42.2 source-free receipt contains forbidden fragment %q", fragment)
		}
	}
	return nil
}
