// Package t421 freezes the deterministic combined physical/logical corpus and
// independent oracle consumed by the later T42.2 execution gate. Production
// packages must not import spike packages.
package t421

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	PlanSchema      = "t421-combined-gate-plan-v1"
	PlanV2Schema    = "t421-combined-gate-plan-v2"
	PlanV3Schema    = "t421-combined-gate-plan-v3"
	OracleSchema    = "t421-independent-combined-oracle-v1"
	ReceiptSchema   = "t422-combined-convergence-receipt-v1"
	MaxPlanBytes    = 256 << 10
	MaxReceiptBytes = 512 << 10
)

type Plan struct {
	Schema            string                     `json:"schema"`
	FrozenOn          string                     `json:"frozen_on"`
	SourceCommit      string                     `json:"source_commit"`
	Inputs            []InputBinding             `json:"inputs"`
	Profile           CombinedProfile            `json:"profile"`
	Oracle            Oracle                     `json:"oracle"`
	Revisions         RevisionHistory            `json:"revision_history"`
	PhaseStates       []PhaseState               `json:"phase_states"`
	PhaseOrder        []string                   `json:"phase_order"`
	PhaseDeadlines    []PhaseDeadline            `json:"phase_deadlines"`
	FailurePoints     []FailurePoint             `json:"failure_points"`
	ReaderProbe       ReaderProbeProfile         `json:"reader_probe"`
	SafetyEnvelope    SafetyEnvelope             `json:"safety_envelope"`
	WorkEnvelope      WorkEnvelope               `json:"work_envelope"`
	MeterPolicy       MeterPolicy                `json:"meter_policy"`
	ToolPolicy        ToolPolicy                 `json:"tool_policy"`
	SealPolicy        SealPolicy                 `json:"seal_policy"`
	StopRules         []StopRule                 `json:"stop_rules"`
	Teardown          TeardownRule               `json:"teardown_rule"`
	ReceiptContract   ReceiptContract            `json:"receipt_contract"`
	Claims            Claims                     `json:"claims"`
	Correction        *ContractCorrection        `json:"correction,omitempty"`
	ProcessAccounting *ProcessAccountingContract `json:"process_accounting,omitempty"`
}

type InputBinding struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Identity string `json:"identity"`
}

type CombinedProfile struct {
	Schema           string                  `json:"schema"`
	Name             string                  `json:"name"`
	Seed             string                  `json:"seed"`
	Physical         PhysicalProfile         `json:"physical"`
	Logical          LogicalProfile          `json:"logical"`
	Overlay          OverlayProfile          `json:"overlay"`
	GeneratedMapping GeneratedMappingProfile `json:"generated_mapping"`
	TypedIndex       TypedIndexProfile       `json:"typed_index"`
	Pipeline         PipelineProfile         `json:"pipeline"`
	Bytes            ByteAccounting          `json:"bytes"`
}

type CatalogSourceProfile struct {
	Schema  string `json:"schema"`
	Records uint64 `json:"records"`
	Bytes   uint64 `json:"bytes"`
	SHA256  string `json:"sha256"`
}

type PhysicalProfile struct {
	StructuralPhysicalOwners    uint64 `json:"structural_physical_owners"`
	StructuralRegularFiles      uint64 `json:"structural_regular_files"`
	StructuralEligibleGoFiles   uint64 `json:"structural_eligible_go_files"`
	StructuralNonCandidateFiles uint64 `json:"structural_non_candidate_files"`
	StructuralControlFiles      uint64 `json:"structural_control_files"`
	UniqueStructuralBlobs       uint64 `json:"unique_structural_blobs"`
	StructuralModeledPartitions uint64 `json:"structural_modeled_partitions"`
	CombinedModeledPartitions   uint64 `json:"combined_modeled_partitions"`
	CombinedPhysicalOwners      uint64 `json:"combined_physical_owners"`
	CombinedRegularFiles        uint64 `json:"combined_regular_files"`
	CombinedEligibleGoFiles     uint64 `json:"combined_eligible_go_files"`
	CombinedControlFiles        uint64 `json:"combined_control_files"`
	CombinedUniqueContentsA     uint64 `json:"combined_unique_contents_a"`
	CombinedUniqueContentsB     uint64 `json:"combined_unique_contents_b"`
	CombinedUniqueContentsAR    uint64 `json:"combined_unique_contents_a_return"`
	StructuralUnownedFiles      uint64 `json:"structural_unowned_files"`
}

type LogicalProfile struct {
	AcceptedFloor                   uint64  `json:"accepted_floor"`
	AcceptedServices                uint64  `json:"accepted_services"`
	TotalServiceRecords             uint64  `json:"total_service_records"`
	Memberships                     uint64  `json:"memberships"`
	DistinctPaths                   uint64  `json:"distinct_paths"`
	UnownedPaths                    uint64  `json:"unowned_paths"`
	CatalogDistinctSelectors        uint64  `json:"catalog_distinct_selectors"`
	CatalogUnownedEntries           uint64  `json:"catalog_unowned_entries"`
	StructuralUnownedPrefixes       uint64  `json:"structural_unowned_prefixes"`
	AcceptedPhysicalFiles           uint64  `json:"accepted_physical_files"`
	UnownedPhysicalFiles            uint64  `json:"unowned_physical_files"`
	DistinctServicePathClaims       uint64  `json:"distinct_service_path_claims"`
	DuplicateRoleMemberships        uint64  `json:"duplicate_role_memberships"`
	InheritedPlacementClaimCopies   uint64  `json:"inherited_placement_claim_copies"`
	MaxRolesPerServicePathClaim     uint64  `json:"max_roles_per_service_path_claim"`
	MaxAcceptedPathFanout           uint64  `json:"max_accepted_path_fanout"`
	RoleMemberships                 []Count `json:"role_memberships"`
	PotentialCartesianOwnerPairs    uint64  `json:"potential_cartesian_owner_pairs"`
	MaterializedCartesianOwnerPairs uint64  `json:"materialized_cartesian_owner_pairs"`
}

type Count struct {
	Name  string `json:"name"`
	Count uint64 `json:"count"`
}

type OverlayProfile struct {
	Algorithm          string      `json:"algorithm"`
	RegularFiles       uint64      `json:"regular_files"`
	GoFiles            uint64      `json:"go_files"`
	IDLFiles           uint64      `json:"idl_files"`
	DistinctContents   uint64      `json:"distinct_contents"`
	RelationshipFiles  uint64      `json:"relationship_files"`
	PathModeContentSet SetIdentity `json:"path_mode_content_set"`
}

type GeneratedMappingProfile struct {
	Schema        string `json:"schema"`
	Records       uint64 `json:"records"`
	RegularFiles  uint64 `json:"regular_files"`
	ContentBytes  uint64 `json:"content_bytes"`
	ContentSHA256 string `json:"content_sha256"`
}

type TypedIndexProfile struct {
	Kind          string `json:"kind"`
	Path          string `json:"path"`
	RegularFiles  uint64 `json:"regular_files"`
	ContentBytes  uint64 `json:"content_bytes"`
	ContentSHA256 string `json:"content_sha256"`
}

type PipelineProfile struct {
	SupportedGoFiles               uint64                    `json:"supported_go_files"`
	SupportedIDLFiles              uint64                    `json:"supported_idl_files"`
	UnsupportedSourceFiles         uint64                    `json:"unsupported_source_files"`
	ProtoMessages                  uint64                    `json:"proto_messages"`
	ProtoServices                  uint64                    `json:"proto_services"`
	ProtoOperations                uint64                    `json:"proto_operations"`
	ResolverDeclarationRecords     uint64                    `json:"resolver_declaration_records"`
	ResolverDeclarationLimit       uint64                    `json:"resolver_declaration_limit"`
	ResolverDeclarationHeadroom    uint64                    `json:"resolver_declaration_headroom"`
	GeneratedMappings              uint64                    `json:"generated_mappings"`
	GeneratedMappingLimit          uint64                    `json:"generated_mapping_limit"`
	GeneratedMappingHeadroom       uint64                    `json:"generated_mapping_headroom"`
	GeneratedDescriptors           uint64                    `json:"generated_descriptors"`
	RPCCallPostings                uint64                    `json:"rpc_call_postings"`
	KafkaProducerPostings          uint64                    `json:"kafka_producer_postings"`
	KafkaConsumerPostings          uint64                    `json:"kafka_consumer_postings"`
	RelationshipProjections        uint64                    `json:"relationship_projections"`
	ServiceReferences              uint64                    `json:"service_references"`
	ResolverFixedReadsPerBuild     uint64                    `json:"resolver_fixed_reads_per_build"`
	ResolverModuleReadsPerBuild    uint64                    `json:"resolver_module_reads_per_build"`
	ResolverGeneratedReadsPerBuild uint64                    `json:"resolver_generated_reads_per_build"`
	ResolverBlobReadsPerBuild      uint64                    `json:"resolver_blob_reads_per_build"`
	ResolverBlobBytesPerBuild      uint64                    `json:"resolver_blob_bytes_per_build"`
	CandidateRepositoryMembers     uint64                    `json:"candidate_repository_members"`
	CandidateCallerLeaves          uint64                    `json:"candidate_caller_leaves"`
	MaximumCallerLeafRecords       uint64                    `json:"maximum_caller_leaf_records"`
	ExtractionDomains              []ExtractionDomainProfile `json:"extraction_domains"`
}

type ExtractionDomainProfile struct {
	Domain                     string                       `json:"domain"`
	ResultPlanSchema           string                       `json:"result_plan_schema"`
	Availability               string                       `json:"availability"`
	CandidateRecords           uint64                       `json:"candidate_records"`
	MaximumRecordsPerPartition uint64                       `json:"maximum_records_per_partition"`
	MemberPartitions           uint64                       `json:"member_partitions"`
	TypedPartitions            uint64                       `json:"typed_partitions"`
	TypedScopeRecords          uint64                       `json:"typed_scope_records"`
	TypedScopePathBytes        uint64                       `json:"typed_scope_path_bytes"`
	TypedScopeEncodedBytes     uint64                       `json:"typed_scope_encoded_bytes"`
	TypedScopeRevisions        []TypedScopeRevision         `json:"typed_scope_revisions"`
	ApplicablePartitions       uint64                       `json:"applicable_partitions"`
	Reserved                   ResultTotals                 `json:"reserved"`
	Expected                   ResultTotals                 `json:"expected"`
	PartitionShape             SetIdentity                  `json:"partition_shape"`
	Partitions                 []ExtractionPartitionProfile `json:"partitions"`
}

type TypedScopeRevision struct {
	PhysicalRevision        string `json:"physical_revision"`
	SHA256                  string `json:"sha256"`
	DescriptorContentSHA256 string `json:"descriptor_content_sha256"`
}

type ResultTotals struct {
	Facts          int64 `json:"facts"`
	Rows           int64 `json:"rows"`
	References     int64 `json:"references"`
	CanonicalBytes int64 `json:"canonical_bytes"`
	EncodedBytes   int64 `json:"encoded_bytes"`
	MemberBytes    int64 `json:"member_bytes"`
	Members        int64 `json:"members"`
}

type ExtractionPartitionProfile struct {
	Ordinal           uint64       `json:"ordinal"`
	Kind              string       `json:"kind"`
	MemberOrdinal     int64        `json:"member_ordinal"`
	CallerPrefix      string       `json:"caller_prefix,omitempty"`
	SourceStart       uint64       `json:"source_start"`
	SourceEnd         uint64       `json:"source_end"`
	MemberRecordStart uint64       `json:"member_record_start"`
	MemberRecordEnd   uint64       `json:"member_record_end"`
	AdmittedRecords   uint64       `json:"admitted_records"`
	Reservation       ResultTotals `json:"reservation"`
	Expected          ResultTotals `json:"expected"`
}

type ByteAccounting struct {
	StructuralDeclaredGoBytes      uint64 `json:"structural_declared_go_bytes"`
	StructuralLogicalSourceBytes   uint64 `json:"structural_logical_source_bytes"`
	StructuralUniqueContentBytes   uint64 `json:"structural_unique_content_bytes"`
	StructuralNonCandidateBytes    uint64 `json:"structural_non_candidate_bytes"`
	CombinedObservationInputBytes  uint64 `json:"combined_observation_input_bytes"`
	CombinedNonObservationBytes    uint64 `json:"combined_non_observation_bytes"`
	OverlayGoBytes                 uint64 `json:"overlay_go_bytes"`
	OverlayGeneratedGoBytes        uint64 `json:"overlay_generated_go_bytes"`
	OverlayServiceGoBytes          uint64 `json:"overlay_service_go_bytes"`
	OverlayNeutralGoBytes          uint64 `json:"overlay_neutral_go_bytes"`
	OverlayIDLBytes                uint64 `json:"overlay_idl_bytes"`
	OverlayLogicalSourceBytes      uint64 `json:"overlay_logical_source_bytes"`
	GeneratedMappingControlBytes   uint64 `json:"generated_mapping_control_bytes"`
	TypedInputRegularFiles         uint64 `json:"typed_input_regular_files"`
	TypedInputLogicalBytes         uint64 `json:"typed_input_logical_bytes"`
	TypedInputUniqueContentBytes   uint64 `json:"typed_input_unique_content_bytes"`
	CombinedLogicalSourceBytes     uint64 `json:"combined_logical_source_bytes"`
	CombinedUniqueContentBytesA    uint64 `json:"combined_unique_content_bytes_a"`
	CombinedUniqueContentBytesB    uint64 `json:"combined_unique_content_bytes_b"`
	CombinedUniqueContentBytesAR   uint64 `json:"combined_unique_content_bytes_a_return"`
	InputFixtureContentBytes       uint64 `json:"input_fixture_content_bytes"`
	CatalogLogicalBytes            uint64 `json:"catalog_logical_bytes"`
	CatalogMeasurementRootBytes    uint64 `json:"catalog_measurement_root_bytes"`
	CatalogMeasurementPathBytes    uint64 `json:"catalog_measurement_path_bytes"`
	CatalogServiceMemberBytes      uint64 `json:"catalog_service_member_bytes"`
	CatalogPlacementMemberBytes    uint64 `json:"catalog_placement_member_bytes"`
	CatalogInheritedClaimBytes     uint64 `json:"catalog_inherited_claim_bytes"`
	CatalogMemberBytes             uint64 `json:"catalog_member_bytes"`
	CatalogMeasurementEncodedBytes uint64 `json:"catalog_measurement_encoded_bytes"`
	AddedCartesianSourceBytes      uint64 `json:"added_cartesian_source_bytes"`
	AllocatedBytesMeasured         bool   `json:"allocated_bytes_measured"`
}

type SetIdentity struct {
	Records     uint64 `json:"records"`
	FramedBytes uint64 `json:"framed_bytes"`
	SHA256      string `json:"sha256"`
}

type Oracle struct {
	Schema               string               `json:"schema"`
	Independent          bool                 `json:"independent"`
	ConsumesPhebsResults bool                 `json:"consumes_phebs_results"`
	Catalog              SetIdentity          `json:"catalog"`
	Memberships          SetIdentity          `json:"memberships"`
	Placements           SetIdentity          `json:"placements"`
	UnownedPrefixes      SetIdentity          `json:"unowned_prefixes"`
	ServiceQueries       SetIdentity          `json:"service_queries"`
	QueryCases           []QueryCase          `json:"query_cases"`
	Relationships        []RelationshipFamily `json:"relationship_families"`
	ProductRelationships ProductRelationships `json:"product_relationships"`
}

type QueryCase struct {
	Name             string           `json:"name"`
	Surface          string           `json:"surface"`
	Revision         string           `json:"revision"`
	Authorization    string           `json:"authorization"`
	HTTP             RequestSpec      `json:"http"`
	MCPTool          string           `json:"mcp_tool,omitempty"`
	Parameters       []QueryParameter `json:"parameters"`
	PageSize         uint64           `json:"page_size,omitempty"`
	CursorRule       string           `json:"cursor_rule"`
	ExpectedStatus   uint64           `json:"expected_status"`
	ExpectedMCPCode  string           `json:"expected_mcp_code"`
	ExpectedRecords  uint64           `json:"expected_records"`
	ExpectedPaths    uint64           `json:"expected_paths"`
	AuthorityFence   string           `json:"authority_fence"`
	Canonicalization string           `json:"canonicalization"`
	ProjectionSHA256 string           `json:"projection_sha256"`
}

type RequestSpec struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

type QueryParameter struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type RelationshipFamily struct {
	Name              string      `json:"name"`
	Seed              string      `json:"seed"`
	Protocols         []string    `json:"protocols"`
	SemanticPairEdges uint64      `json:"semantic_pair_edges"`
	MaxInDegree       uint64      `json:"max_in_degree"`
	MaxOutDegree      uint64      `json:"max_out_degree"`
	Acyclic           bool        `json:"acyclic"`
	ExpectedEdges     SetIdentity `json:"expected_edges"`
}

type ProductRelationships struct {
	RPCProjections           uint64              `json:"rpc_projections"`
	KafkaProducerProjections uint64              `json:"kafka_producer_projections"`
	KafkaConsumerProjections uint64              `json:"kafka_consumer_projections"`
	TotalProjections         uint64              `json:"total_projections"`
	ServiceReferences        uint64              `json:"service_references"`
	KafkaPairOraclePosture   string              `json:"kafka_pair_oracle_posture"`
	Canonicalization         string              `json:"canonicalization"`
	GlobalCallerPolicy       string              `json:"global_caller_policy"`
	CallerCandidateRecords   uint64              `json:"caller_candidate_records"`
	CallerLeaves             []CallerLeafProfile `json:"caller_leaves"`
	ExpectedRPCProjections   SetIdentity         `json:"expected_rpc_projections"`
	ExpectedProjections      SetIdentity         `json:"expected_projections"`
}

type CallerLeafProfile struct {
	Prefix           string `json:"prefix"`
	CandidateRecords uint64 `json:"candidate_records"`
	ResolvedPostings uint64 `json:"resolved_postings"`
	Abstentions      uint64 `json:"abstentions"`
	Records          uint64 `json:"records"`
	CanonicalBytes   uint64 `json:"canonical_bytes"`
	EncodedBytes     uint64 `json:"encoded_bytes"`
}

type RevisionHistory struct {
	SourceRecipe SourceRecipe       `json:"source_recipe"`
	Physical     []PhysicalRevision `json:"physical"`
	Logical      []LogicalRevision  `json:"logical"`
}

type SourceRecipe struct {
	Schema                        string `json:"schema"`
	Composition                   string `json:"composition"`
	GitObjectFormat               string `json:"git_object_format"`
	PathOrder                     string `json:"path_order"`
	FileMode                      string `json:"file_mode"`
	AuthorName                    string `json:"author_name"`
	AuthorEmail                   string `json:"author_email"`
	CommitterName                 string `json:"committer_name"`
	CommitterEmail                string `json:"committer_email"`
	Timestamp                     int64  `json:"timestamp"`
	Timezone                      string `json:"timezone"`
	ObjectHeaderPolicy            string `json:"object_header_policy"`
	CommitMessagePrefix           string `json:"commit_message_prefix"`
	StructuralNonCandidateRecords uint64 `json:"structural_non_candidate_records"`
	StructuralNonCandidateRule    string `json:"structural_non_candidate_rule"`
	OverlayRecords                uint64 `json:"overlay_records"`
	OverlayFramedBytes            uint64 `json:"overlay_framed_bytes"`
	OverlaySHA256                 string `json:"overlay_sha256"`
	GeneratedMappingRecords       uint64 `json:"generated_mapping_records"`
	GeneratedMappingPath          string `json:"generated_mapping_path"`
	GeneratedMappingMode          string `json:"generated_mapping_mode"`
	GeneratedMappingBytes         uint64 `json:"generated_mapping_bytes"`
	GeneratedMappingSHA256        string `json:"generated_mapping_sha256"`
	TypedInputRecords             uint64 `json:"typed_input_records"`
	TypedInputKind                string `json:"typed_input_kind"`
	TypedInputPath                string `json:"typed_input_path"`
	TypedInputMode                string `json:"typed_input_mode"`
	TypedInputBytes               uint64 `json:"typed_input_bytes"`
	TypedInputSHA256              string `json:"typed_input_sha256"`
	TypedInputBlobOID             string `json:"typed_input_blob_oid"`
	CatalogSourcePolicy           string `json:"catalog_source_policy"`
	TreeEntryEncoding             string `json:"tree_entry_encoding"`
	CommitHeaderOrder             string `json:"commit_header_order"`
	IdentityEncoding              string `json:"identity_encoding"`
	CommitBodyTerminator          string `json:"commit_body_terminator"`
	ParentPolicy                  string `json:"parent_policy"`
	AuthoredManifestSchema        string `json:"authored_manifest_schema"`
	TreeInventoryCanonicalization string `json:"tree_inventory_canonicalization"`
}

type PhysicalRevision struct {
	Name                              string               `json:"name"`
	Parent                            string               `json:"parent,omitempty"`
	Posture                           string               `json:"posture"`
	ChangedPhysicalFiles              uint64               `json:"changed_physical_files"`
	BaseCommit                        string               `json:"base_commit"`
	BaseTree                          string               `json:"base_tree"`
	CommitMessage                     string               `json:"commit_message"`
	SourceTreeRecipeSHA256            string               `json:"source_tree_recipe_sha256"`
	SourceCommitRecipeSHA256          string               `json:"source_commit_recipe_sha256"`
	ExpectedTreeInventory             SetIdentity          `json:"expected_tree_inventory"`
	ExpectedObservationInputInventory SetIdentity          `json:"expected_observation_input_inventory"`
	ExpectedCandidateInventories      []CandidateInventory `json:"expected_candidate_inventories"`
	ExpectedTree                      string               `json:"expected_tree"`
	ExpectedCommit                    string               `json:"expected_commit"`
}

type CandidateInventory struct {
	Domain     string      `json:"domain"`
	Candidates SetIdentity `json:"candidates"`
}

type LogicalRevision struct {
	Name                   string               `json:"name"`
	Parent                 string               `json:"parent,omitempty"`
	Posture                string               `json:"posture"`
	ChangedLogicalServices uint64               `json:"changed_logical_services"`
	CatalogLogicalSHA256   string               `json:"catalog_logical_sha256"`
	SemanticSHA256         string               `json:"semantic_sha256"`
	CatalogSource          CatalogSourceProfile `json:"catalog_source"`
	Catalog                SetIdentity          `json:"catalog"`
	Memberships            SetIdentity          `json:"memberships"`
	Placements             SetIdentity          `json:"placements"`
	UnownedPrefixes        SetIdentity          `json:"unowned_prefixes"`
	ServiceQueries         SetIdentity          `json:"service_queries"`
}

type PhaseState struct {
	Phase                    string `json:"phase"`
	PhysicalRevision         string `json:"physical_revision,omitempty"`
	LogicalRevision          string `json:"logical_revision,omitempty"`
	SourceAction             string `json:"source_action"`
	SearchAction             string `json:"search_action"`
	ObservationAction        string `json:"observation_action"`
	CatalogAction            string `json:"catalog_action"`
	RelationshipAction       string `json:"relationship_action"`
	ExpectedCurrentAuthority string `json:"expected_current_authority"`
	RecoveryPreparation      string `json:"recovery_preparation,omitempty"`
}

type FailurePoint struct {
	Name                   string `json:"name"`
	Phase                  string `json:"phase"`
	Boundary               string `json:"boundary"`
	Occurrence             uint64 `json:"occurrence"`
	Trigger                string `json:"trigger"`
	TargetDomain           string `json:"target_domain"`
	TargetKind             string `json:"target_kind"`
	TargetOrdinal          uint64 `json:"target_ordinal"`
	TargetServiceOrdinal   uint64 `json:"target_service_ordinal,omitempty"`
	TargetServiceKeySHA256 string `json:"target_service_key_sha256,omitempty"`
	TargetCallerPrefix     string `json:"target_caller_prefix,omitempty"`
	TargetSourceStart      uint64 `json:"target_source_start"`
	TargetSourceEnd        uint64 `json:"target_source_end"`
	TargetMemberOrdinal    int64  `json:"target_member_ordinal"`
	TargetMemberStart      uint64 `json:"target_member_record_start"`
	TargetMemberEnd        uint64 `json:"target_member_record_end"`
	TargetBindingRecipe    string `json:"target_binding_recipe"`
	PartialResidue         string `json:"partial_residue"`
	PriorAuthority         string `json:"prior_authority"`
	FinalAuthority         string `json:"final_authority"`
	RecoveryAction         string `json:"recovery_action"`
	RecoveryDeadlineMS     uint64 `json:"recovery_deadline_ms"`
	LeavesTarget           bool   `json:"leaves_target"`
}

type PhaseDeadline struct {
	Phase      string `json:"phase"`
	DeadlineMS uint64 `json:"deadline_ms"`
}

type ReaderProbeProfile struct {
	Schema                  string `json:"schema"`
	Reader                  string `json:"reader"`
	QuerySHA256             string `json:"query_sha256"`
	PathSHA256              string `json:"path_sha256"`
	OldProjectionSHA256     string `json:"old_projection_sha256"`
	NewProjectionSHA256     string `json:"new_projection_sha256"`
	ExpectedRecords         uint64 `json:"expected_records"`
	PostDeleteOutcome       string `json:"post_delete_outcome,omitempty"`
	OldRoleAfterReplacement string `json:"old_role_after_replacement,omitempty"`
	NewRoleAfterReplacement string `json:"new_role_after_replacement,omitempty"`
	PostReleaseOutcome      string `json:"post_release_outcome,omitempty"`
}

type SafetyEnvelope struct {
	MinimumMemoryBytes          uint64   `json:"minimum_memory_bytes"`
	MinimumAvailableDiskBytes   uint64   `json:"minimum_available_disk_bytes"`
	PressureVolumeBytes         uint64   `json:"pressure_volume_bytes"`
	MaximumTotalWallMS          uint64   `json:"maximum_total_wall_ms"`
	MaximumPeakRSSBytes         uint64   `json:"maximum_peak_rss_bytes"`
	MaximumDataAllocatedBytes   uint64   `json:"maximum_data_allocated_bytes"`
	MaximumRetriesPerUnit       uint64   `json:"maximum_retries_per_unit"`
	ServerHealthDeadlineMS      uint64   `json:"server_health_deadline_ms"`
	FullConvergenceDeadlineMS   uint64   `json:"full_convergence_deadline_ms"`
	RevalidationDeadlineMS      uint64   `json:"revalidation_deadline_ms"`
	PressureUsedPercents        []uint64 `json:"pressure_used_percents"`
	MaximumPressureBallastBytes uint64   `json:"maximum_pressure_ballast_bytes"`
	MinimumPrePressureUsedBytes uint64   `json:"minimum_pre_pressure_used_bytes"`
	MaximumPrePressureUsedBytes uint64   `json:"maximum_pre_pressure_used_bytes"`
	MinimumPrePressureBytes     uint64   `json:"minimum_pre_pressure_allocated_bytes"`
	MaximumPrePressureBytes     uint64   `json:"maximum_pre_pressure_allocated_bytes"`
	PressureSameVolumeRequired  bool     `json:"pressure_same_volume_required"`
	PressureBallastFormula      string   `json:"pressure_ballast_formula"`
	MinimumPressureMarginBytes  uint64   `json:"minimum_pressure_margin_bytes"`
}

type ToolPolicy struct {
	DigestAlgorithm             string   `json:"digest_algorithm"`
	ExecutionFreezeSchema       string   `json:"execution_freeze_schema"`
	ExecutionProfileSchema      string   `json:"execution_profile_schema"`
	MaximumExecutionFreezeBytes uint64   `json:"maximum_execution_freeze_bytes"`
	RequireCleanCommit          bool     `json:"require_clean_commit"`
	FreezeBeforeExecution       bool     `json:"freeze_before_execution"`
	PressureVolumePreparation   string   `json:"pressure_volume_preparation"`
	BufModulePath               string   `json:"buf_module_path"`
	BufModuleVersion            string   `json:"buf_module_version"`
	BufModuleSum                string   `json:"buf_module_sum"`
	BufBuildRecipe              string   `json:"buf_build_recipe"`
	ZoektModulePath             string   `json:"zoekt_module_path"`
	ZoektModuleVersion          string   `json:"zoekt_module_version"`
	ZoektModuleSum              string   `json:"zoekt_module_sum"`
	ZoektBuildRecipe            string   `json:"zoekt_build_recipe"`
	RequiredTools               []string `json:"required_tools"`
	RequiredHostFields          []string `json:"required_host_fields"`
}

type WorkEnvelope struct {
	Schema                                    string            `json:"schema"`
	MaximumRetriesPerUnit                     uint64            `json:"maximum_retries_per_unit"`
	MaximumStoreRowsPerTransaction            uint64            `json:"maximum_store_rows_per_transaction"`
	MaximumAggregatePartitions                uint64            `json:"maximum_aggregate_partitions"`
	MaximumLifecycleDeletesPerTurn            uint64            `json:"maximum_lifecycle_deletes_per_turn"`
	MaximumDataLogicalBytes                   uint64            `json:"maximum_data_logical_bytes"`
	MaximumChildProcessesPerPhase             uint64            `json:"maximum_child_processes_per_phase"`
	ChildProcessRoles                         []string          `json:"child_process_roles"`
	MaximumControlledDispatchAttemptsPerPhase uint64            `json:"maximum_controlled_dispatch_attempts_per_phase,omitempty"`
	ControlledDispatchRoles                   []string          `json:"controlled_dispatch_roles,omitempty"`
	LifecycleOwners                           []string          `json:"lifecycle_owners"`
	Phases                                    []PhaseWorkBounds `json:"phases"`
}

type CounterBound struct {
	Minimum uint64 `json:"minimum"`
	Maximum uint64 `json:"maximum"`
}

type PhaseWorkBounds struct {
	Phase                     string       `json:"phase"`
	ChildProcessRoles         []RoleBound  `json:"child_process_roles"`
	ControlledDispatchRoles   []RoleBound  `json:"controlled_dispatch_roles,omitempty"`
	PhysicalCorpusPasses      CounterBound `json:"physical_corpus_passes"`
	ChangedPhysicalFiles      CounterBound `json:"changed_physical_files"`
	ChangedLogicalServices    CounterBound `json:"changed_logical_services"`
	GitReads                  CounterBound `json:"git_reads"`
	CensusChildren            CounterBound `json:"census_children,omitzero"`
	CensusRecords             CounterBound `json:"census_records,omitzero"`
	IndexFiles                CounterBound `json:"index_files"`
	ObservationParses         CounterBound `json:"observation_parses"`
	SourceLogicalBytes        CounterBound `json:"source_logical_bytes"`
	SourceUniqueBytes         CounterBound `json:"source_unique_bytes"`
	ApplicablePartitions      CounterBound `json:"applicable_partitions"`
	PublishedDomains          CounterBound `json:"published_domains"`
	ControlReads              CounterBound `json:"control_reads"`
	MemberReads               CounterBound `json:"member_reads"`
	JobAttempts               CounterBound `json:"job_attempts"`
	StoreTransactions         CounterBound `json:"store_transactions"`
	StoreRows                 CounterBound `json:"store_rows"`
	CacheRootReads            CounterBound `json:"cache_root_reads"`
	CacheMemberReads          CounterBound `json:"cache_member_reads"`
	CacheLookups              CounterBound `json:"cache_lookups"`
	PublicationWrites         CounterBound `json:"publication_writes"`
	RelationshipBuildAttempts CounterBound `json:"relationship_build_attempts"`
	LifecycleOwnerTurns       CounterBound `json:"lifecycle_owner_turns"`
	LifecycleDeleted          CounterBound `json:"lifecycle_deleted"`
	CacheHits                 CounterBound `json:"cache_hits"`
	CacheMisses               CounterBound `json:"cache_misses"`
	CombinedPhysicalOwners    CounterBound `json:"combined_physical_owners"`
	LogicalMemberships        CounterBound `json:"logical_memberships"`
	RelationshipProjections   CounterBound `json:"relationship_projections"`
	ResolverBlobBytes         CounterBound `json:"resolver_blob_bytes"`
	ResolverBlobReads         CounterBound `json:"resolver_blob_reads"`
	ReuseDecisions            CounterBound `json:"reuse_decisions"`
	ServiceReferences         CounterBound `json:"service_references"`
	ServiceRows               CounterBound `json:"service_rows"`
	UnsupportedSourceFiles    CounterBound `json:"unsupported_source_files"`
}

type RoleBound struct {
	Name    string `json:"name"`
	Minimum uint64 `json:"minimum"`
	Maximum uint64 `json:"maximum"`
}

type MeterPolicy struct {
	Schema                string `json:"schema"`
	Authority             string `json:"authority"`
	PhaseReset            string `json:"phase_reset"`
	CounterAggregation    string `json:"counter_aggregation"`
	ByteGaugeSemantics    string `json:"byte_gauge_semantics"`
	ProcessGaugeSemantics string `json:"process_gauge_semantics"`
	CacheSemantics        string `json:"cache_semantics"`
	StoreSemantics        string `json:"store_semantics"`
	LifecycleSemantics    string `json:"lifecycle_semantics"`
	RequiredMetricsSHA256 string `json:"required_metrics_sha256"`
}

type SealPolicy struct {
	Schema                               string   `json:"schema"`
	KeyAlgorithm                         string   `json:"key_algorithm"`
	SignerIdentity                       string   `json:"signer_identity"`
	FreezeSignatureNamespace             string   `json:"freeze_signature_namespace"`
	SourceVerificationSignatureNamespace string   `json:"source_verification_signature_namespace"`
	ReturnedSignatureNamespace           string   `json:"returned_signature_namespace"`
	TrustRoot                            string   `json:"trust_root"`
	PackageDigestAlgorithm               string   `json:"package_digest_algorithm"`
	SignatureInputPolicy                 string   `json:"signature_input_policy"`
	SignerFingerprintPolicy              string   `json:"signer_fingerprint_policy"`
	ArchiveFormat                        string   `json:"archive_format"`
	InventoryOrder                       string   `json:"inventory_order"`
	EntryTypePolicy                      string   `json:"entry_type_policy"`
	ManifestSchema                       string   `json:"manifest_schema"`
	ManifestRequiredFields               []string `json:"manifest_required_fields"`
	SourceVerificationSchema             string   `json:"source_verification_schema"`
	SourceVerificationCanonicalization   string   `json:"source_verification_canonicalization"`
	SourceVerificationRequiredFields     []string `json:"source_verification_required_fields"`
	SourceVerificationRevisionFields     []string `json:"source_verification_revision_fields"`
	SourceVerificationChecks             []string `json:"source_verification_checks"`
	ChecksumLineFormat                   string   `json:"checksum_line_format"`
	ChecksumCoverage                     []string `json:"checksum_coverage"`
	ChecksumExclusions                   []string `json:"checksum_exclusions"`
	RequireUniqueSigner                  bool     `json:"require_unique_signer"`
	MaximumPackageBytes                  uint64   `json:"maximum_package_bytes"`
	MaximumExpandedBytes                 uint64   `json:"maximum_expanded_bytes"`
	ExactInventory                       []string `json:"exact_inventory"`
}

type StopRule struct {
	Priority uint64 `json:"priority"`
	Decision string `json:"decision"`
	Trigger  string `json:"trigger"`
}

type TeardownRule struct {
	Scope                string `json:"scope,omitempty"`
	StopDescendants      bool   `json:"stop_descendants"`
	CloseStore           bool   `json:"close_store"`
	RemoveDerivedCustody bool   `json:"remove_derived_custody"`
	RemoveScratchSource  bool   `json:"remove_scratch_source"`
	RequireZeroChildren  bool   `json:"require_zero_children"`
	RetainSourceFreeOnly bool   `json:"retain_source_free_only"`
}

type ReceiptContract struct {
	Schema                          string   `json:"schema"`
	MaximumBytes                    uint64   `json:"maximum_bytes"`
	PhaseOutcomes                   []string `json:"phase_outcomes"`
	RequiredSections                []string `json:"required_sections"`
	RequiredMetrics                 []string `json:"required_metrics"`
	StoppedSuffixNotRun             bool     `json:"stopped_suffix_not_run"`
	TeardownAlwaysRuns              bool     `json:"teardown_always_runs"`
	TeardownPhaseOutcome            string   `json:"teardown_phase_outcome"`
	TeardownOutcomes                []string `json:"teardown_outcomes"`
	TeardownFailureSchema           string   `json:"teardown_failure_schema"`
	ExecutionAdmissionSchema        string   `json:"execution_admission_schema"`
	ExecutionAdmissionOrder         string   `json:"execution_admission_order"`
	TypedFailureOnly                bool     `json:"typed_failure_only"`
	CanonicalSourceFree             bool     `json:"canonical_source_free"`
	FailureObservationSchema        string   `json:"failure_observation_schema"`
	StateObservationSchema          string   `json:"state_observation_schema"`
	StateProjectionSchema           string   `json:"state_projection_schema"`
	StateProjectionCanonicalization string   `json:"state_projection_canonicalization"`
	TransitionSchema                string   `json:"transition_schema"`
	QueryTransportSchema            string   `json:"query_transport_schema"`
	MaterializedOwnerPairPhases     []string `json:"materialized_owner_pair_phases"`
	ExtractionDomains               []string `json:"extraction_domains"`
}

type Claims struct {
	Neutral                          bool   `json:"neutral"`
	SourceFree                       bool   `json:"source_free"`
	IndependentOracle                bool   `json:"independent_oracle"`
	ChangesProductionBehavior        bool   `json:"changes_production_behavior"`
	RaisesProductionCap              bool   `json:"raises_production_cap"`
	AuthorizesExecution              bool   `json:"authorizes_execution"`
	AuthorizesPrivateReplay          bool   `json:"authorizes_private_replay"`
	EstablishesTargetSLO             bool   `json:"establishes_target_slo"`
	QualifiesSupportedScale          bool   `json:"qualifies_supported_scale"`
	EstablishesAccuracyCompleteness  bool   `json:"establishes_accuracy_completeness"`
	EstablishesCommitCadence         bool   `json:"establishes_commit_cadence"`
	EstablishesQueueCatchup          bool   `json:"establishes_queue_catchup"`
	EstablishesFreshnessUnderCadence bool   `json:"establishes_freshness_under_cadence"`
	SelectsTopology                  bool   `json:"selects_topology"`
	EstablishesMigrationDecommission bool   `json:"establishes_migration_decommission"`
	AuthorizesRelease                bool   `json:"authorizes_release"`
	Gate2V2                          string `json:"gate2_v2"`
	ReleasePosture                   string `json:"release_posture"`
}

func MarshalCanonical(value any) ([]byte, error) {
	compact := false
	switch typed := value.(type) {
	case Plan:
		compact = typed.Schema == PlanV3Schema
	case *Plan:
		compact = typed != nil && typed.Schema == PlanV3Schema
	case ExecutionFreeze:
		compact = typed.Schema == ExecutionFreezeV3Schema
	case *ExecutionFreeze:
		compact = typed != nil && typed.Schema == ExecutionFreezeV3Schema
	case Receipt:
		compact = typed.Schema == ReceiptV3Schema
	case *Receipt:
		compact = typed != nil && typed.Schema == ReceiptV3Schema
	}
	if compact {
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		return append(raw, '\n'), nil
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

func DecodePlan(raw []byte) (Plan, error) {
	if len(raw) > MaxPlanBytes {
		return Plan{}, errors.New("T42.1 plan exceeds its frozen byte bound")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var plan Plan
	if err := decoder.Decode(&plan); err != nil {
		return Plan{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Plan{}, errors.New("T42.1 plan contains trailing data")
	}
	want, err := MarshalCanonical(plan)
	if err != nil || !bytes.Equal(raw, want) {
		return Plan{}, errors.New("T42.1 plan is not canonical")
	}
	if err := ValidateFrozenPlan(plan); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

func SHA256(raw []byte) string {
	digest := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func validDigest(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func validCommit(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, current := range value {
		if current < '0' || current > '9' && current < 'a' || current > 'f' {
			return false
		}
	}
	return true
}

func rejectSourceBearingPlan(raw []byte) error {
	raw = bytes.ToLower(raw)
	for _, fragment := range []string{
		".go\"", ".proto\"", "svc.load-", "services/", "structural/",
		"example.invalid/", "package neutral", "func t421",
	} {
		if bytes.Contains(raw, []byte(strings.ToLower(fragment))) {
			return fmt.Errorf("T42.1 source-free plan contains forbidden fragment %q", fragment)
		}
	}
	return nil
}
