// Package t384 retains T38.4's deterministic source-free MCP microservice
// parity receipt. Production MCP and API tests own the shared authority,
// authorization, pagination, cancellation, and encoded-output fences.
package t384

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

const Schema = "t384-mcp-microservice-parity-v1"

type Receipt struct {
	Schema  string `json:"schema"`
	Outcome string `json:"outcome"`
	Tools   Tools  `json:"tools"`
	Bounds  Bounds `json:"bounds"`
	Cases   []Case `json:"cases"`
	Claims  Claims `json:"claims"`
}

type Tools struct {
	ParityToolCount       int      `json:"parity_tool_count"`
	CompleteReadToolCount int      `json:"complete_read_tool_count"`
	Names                 []string `json:"names"`
}

type Bounds struct {
	ServiceResponseBytes      int `json:"service_response_bytes"`
	RelationshipResponseBytes int `json:"relationship_response_bytes"`
	CitationBlobBytes         int `json:"citation_blob_bytes"`
	ImpactResponseBytes       int `json:"impact_response_bytes"`
	MaximumPageRows           int `json:"maximum_page_rows"`
	MaximumRepositories       int `json:"maximum_relationship_repositories"`
}

type Case struct {
	Name       string   `json:"name"`
	Assertions []string `json:"assertions"`
	Gates      []string `json:"gates"`
}

type Claims struct {
	SourceFree           bool `json:"source_free"`
	NoWrite              bool `json:"no_write"`
	NoTaskCompletion     bool `json:"no_task_completion"`
	NoDecisionAuthority  bool `json:"no_decision_authority"`
	NoAccuracy           bool `json:"no_accuracy"`
	NoCompleteness       bool `json:"no_completeness"`
	NoRuntimeTopology    bool `json:"no_runtime_topology"`
	NoMigrationSafety    bool `json:"no_migration_safety"`
	NoDecommissionSafety bool `json:"no_decommission_safety"`
	NoReleaseAuthority   bool `json:"no_release_authority"`
}

func Build() Receipt {
	return Receipt{
		Schema: Schema, Outcome: "completed",
		Tools: Tools{
			ParityToolCount: 6, CompleteReadToolCount: 18,
			Names: []string{
				"compare_service_relationships",
				"get_change_workbench_impact",
				"get_service",
				"list_service_relationships",
				"list_services",
				"read_service_relationship_citation",
			},
		},
		Bounds: Bounds{
			ServiceResponseBytes: 1 << 20, RelationshipResponseBytes: 2 << 20,
			CitationBlobBytes: 4 << 20, ImpactResponseBytes: 8 << 20,
			MaximumPageRows: 100, MaximumRepositories: 32,
		},
		Cases: []Case{
			{Name: "shared_contract", Assertions: []string{"exact_request_projection", "exact_page_projection", "authority_and_gaps_preserved"}, Gates: []string{"TestMicroserviceMCPImpactUsesExactSharedContract"}},
			{Name: "strict_bounded_darkness", Assertions: []string{"unknown_nested_field_refused", "output_ceiling_shared", "incomplete_capability_dark", "authorization_non_disclosing"}, Gates: []string{"TestMicroserviceMCPToolsAreStrictBoundedAndCapabilityGated", "TestWorkbenchImpactResponseCeilingIsEncodedAndClosed"}},
			{Name: "cancellation", Assertions: []string{"request_context_forwarded", "shared_read_canceled"}, Gates: []string{"TestMicroserviceMCPImpactCancellationReachesSharedService"}},
			{Name: "tool_identity", Assertions: []string{"six_parity_tools", "eighteen_complete_read_tools", "strict_schema_digests_pinned", "read_only_annotations", "production_wiring"}, Gates: []string{"TestMicroserviceMCPToolCountAndSchemaDigests", "TestHTTPHandlerAuthenticationBoundaries"}},
			{Name: "existing_shared_readers", Assertions: []string{"directory_shared", "relationships_shared", "citations_shared"}, Gates: []string{"TestServiceDirectoryToolsShareResponsesAndSchemas", "TestRelationshipMCPToolsUseSharedProjectionAndStayDefaultDark"}},
		},
		Claims: Claims{SourceFree: true, NoWrite: true, NoTaskCompletion: true, NoDecisionAuthority: true, NoAccuracy: true, NoCompleteness: true, NoRuntimeTopology: true, NoMigrationSafety: true, NoDecommissionSafety: true, NoReleaseAuthority: true},
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
