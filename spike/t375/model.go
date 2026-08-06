// Package t375 retains Epic 37's deterministic source-free closure receipt.
// Production-path tests own all publication, reader, citation, lifecycle,
// proof, Workbench, and transport claims; this model records only the neutral
// topology shape and the named gates that establish those mechanics.
package t375

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

const Schema = "t375-relationship-reader-demo-v1"

type Receipt struct {
	Schema   string   `json:"schema"`
	Outcome  string   `json:"outcome"`
	Topology Topology `json:"topology"`
	Reader   Reader   `json:"reader"`
	Cases    []Case   `json:"cases"`
	Claims   Claims   `json:"claims"`
}

type Topology struct {
	AuthorizedRepositories int `json:"authorized_repositories"`
	Services               int `json:"services"`
	RPCDependencies        int `json:"rpc_dependencies"`
	RPCCallers             int `json:"rpc_callers"`
	KafkaProducers         int `json:"kafka_producers"`
	KafkaConsumers         int `json:"kafka_consumers"`
	EmptyStates            int `json:"empty_states"`
	GapStates              int `json:"gap_states"`
}

type Reader struct {
	MaximumRepositories         int `json:"maximum_repositories"`
	MaximumPageRows             int `json:"maximum_page_rows"`
	MaximumListReferences       int `json:"maximum_list_references"`
	MaximumComparisonReferences int `json:"maximum_comparison_references"`
	MaximumBindings             int `json:"maximum_bindings"`
	MaximumBindingReferences    int `json:"maximum_binding_references"`
	BindingLifetimeSeconds      int `json:"binding_lifetime_seconds"`
	MaximumConcurrentReads      int `json:"maximum_concurrent_reads"`
	MaximumConcurrentCitations  int `json:"maximum_concurrent_citations"`
	MaximumResponseBytes        int `json:"maximum_response_bytes"`
}

type Case struct {
	Name       string   `json:"name"`
	Assertions []string `json:"assertions"`
	Gates      []string `json:"gates"`
}
type Claims struct {
	SourceFree         bool `json:"source_free"`
	NoAccuracy         bool `json:"no_accuracy"`
	NoCompleteness     bool `json:"no_completeness"`
	NoRuntimeTopology  bool `json:"no_runtime_topology"`
	NoSLO              bool `json:"no_slo"`
	NoMigrationSafety  bool `json:"no_migration_safety"`
	NoReleaseAuthority bool `json:"no_release_authority"`
}

func Build() Receipt {
	return Receipt{
		Schema: Schema, Outcome: "completed",
		Topology: Topology{AuthorizedRepositories: 3, Services: 4, RPCDependencies: 2, RPCCallers: 2, KafkaProducers: 1, KafkaConsumers: 1, EmptyStates: 1, GapStates: 1},
		Reader:   Reader{MaximumRepositories: 32, MaximumPageRows: 100, MaximumListReferences: 20_000, MaximumComparisonReferences: 40_000, MaximumBindings: 8, MaximumBindingReferences: 80_000, BindingLifetimeSeconds: 300, MaximumConcurrentReads: 8, MaximumConcurrentCitations: 2, MaximumResponseBytes: 2 << 20},
		Cases: []Case{
			{Name: "atomic_projection", Assertions: []string{"all_placement_claims_retained", "service_failure_isolated", "incarnation_bound"}, Gates: []string{"TestBuildProjectsSharedUnownedAndTargetAuthorityAtomically", "TestServiceIncarnationChangesRootIdentity", "TestServiceLimitPublishesFailedPartitionWithoutBlockingSibling"}},
			{Name: "exact_readers", Assertions: []string{"bucket_batched_projection_reads", "keyed_posting_reads", "historical_generation_lease"}, Gates: []string{"TestBuildProjectsSharedUnownedAndTargetAuthorityAtomically", "TestBuildPublishesResolvedNameMatchUnresolvedAndOccurrenceIdentity", "TestBuildSeparatesPlanesSpellingClassificationsAndOccurrences"}},
			{Name: "authorization_and_gaps", Assertions: []string{"permission_before_aggregation", "empty_failed_unavailable_distinct", "retained_proof_shape_unchanged"}, Gates: []string{"TestAuthorizationPrecedesRepositoryValidationAndFilesystem", "TestRelationshipServiceAuthorizationGapAndRetainedProofShape"}},
			{Name: "proof_and_workbench", Assertions: []string{"optional_exact_root_coverage_annex", "workbench_cursor_binds_root_set", "legacy_bytes_unchanged"}, Gates: []string{"TestRelationshipServiceAuthorizationGapAndRetainedProofShape", "TestWorkbenchRelationshipCoverageBindsExactRootDigest"}},
			{Name: "transport_parity", Assertions: []string{"shared_http_mcp_types", "default_dark_registration", "bounded_citations"}, Gates: []string{"TestRelationshipMCPToolsUseSharedProjectionAndStayDefaultDark"}},
			{Name: "recovery_and_retention", Assertions: []string{"complete_recovery_before_pointer", "current_prior_and_reader_lease_preserved", "byte_exact_composite_archive"}, Gates: []string{"TestRecoveryValidatesCompleteGenerationBeforePointerSwap", "TestLifecyclePreservesCurrentRollbackFloorAndReaderLease", "TestArchiveRoundTripPreservesExactCompositeAuthority"}},
		},
		Claims: Claims{SourceFree: true, NoAccuracy: true, NoCompleteness: true, NoRuntimeTopology: true, NoSLO: true, NoMigrationSafety: true, NoReleaseAuthority: true},
	}
}

func Marshal(receipt Receipt) ([]byte, error) {
	raw, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}
func Digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}
