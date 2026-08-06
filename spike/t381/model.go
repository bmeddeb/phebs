// Package t381 retains T38.1's deterministic source-free service-overview
// receipt. Production API and UI tests own the authorization, exact-reader,
// paging, citation, state-honesty, and accessibility mechanics.
package t381

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

const Schema = "t381-service-overview-demo-v1"

type Receipt struct {
	Schema  string   `json:"schema"`
	Outcome string   `json:"outcome"`
	Fixture Fixture  `json:"fixture"`
	Reader  Reader   `json:"reader"`
	States  []string `json:"states"`
	Cases   []Case   `json:"cases"`
	Claims  Claims   `json:"claims"`
}

type Fixture struct {
	Repositories      int `json:"repositories"`
	Services          int `json:"services"`
	CatalogRoles      int `json:"catalog_roles"`
	RelationshipViews int `json:"relationship_views"`
	RPCReferences     int `json:"rpc_references"`
	KafkaReferences   int `json:"kafka_references"`
}

type Reader struct {
	FirstPageBindings int `json:"first_page_bindings"`
	PageRows          int `json:"page_rows"`
	MaximumPageRows   int `json:"maximum_page_rows"`
	CitationReads     int `json:"citation_reads_per_action"`
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
	NoScale            bool `json:"no_scale"`
	NoSLO              bool `json:"no_slo"`
	NoMigrationSafety  bool `json:"no_migration_safety"`
	NoReleaseAuthority bool `json:"no_release_authority"`
}

func Build() Receipt {
	return Receipt{
		Schema: Schema, Outcome: "completed",
		Fixture: Fixture{Repositories: 1, Services: 3, CatalogRoles: 2, RelationshipViews: 3, RPCReferences: 5, KafkaReferences: 1},
		Reader:  Reader{FirstPageBindings: 3, PageRows: 25, MaximumPageRows: 100, CitationReads: 1},
		States:  []string{"ambiguous", "current", "empty", "failed", "partial", "shared", "stale", "unavailable", "unowned", "unsupported"},
		Cases: []Case{
			{Name: "catalog_authority", Assertions: []string{"authorization_first", "catalog_and_state_snapshot_fenced", "source_roles_exact"}, Gates: []string{"TestServiceDirectoryAuthorizationCursorAndDetail", "TestServiceDirectoryCursorBindsSnapshotAndIncarnation"}},
			{Name: "exact_overview", Assertions: []string{"counts_link_exact_rows", "contracts_callers_topics_and_counterparts_visible", "immutable_citation_span"}, Gates: []string{"links exact relationship counts to fenced rows and reads immutable citations"}},
			{Name: "state_honesty", Assertions: []string{"unsupported_failed_empty_and_mixed_distinct", "shared_unowned_ambiguous_visible", "no_empty_inference_from_gap"}, Gates: []string{"keeps unsupported, failed, empty, and mixed-generation relationship states distinct"}},
			{Name: "bounded_navigation", Assertions: []string{"three_first_page_bindings_once", "tab_reuse", "selected_cursor_only"}, Gates: []string{"reuses three first-page bindings across tabs and advances only the selected cursor"}},
			{Name: "reader_authority", Assertions: []string{"permission_before_aggregation", "root_and_incarnation_fenced", "citation_lease_pinned"}, Gates: []string{"TestRelationshipServiceAuthorizationGapAndRetainedProofShape", "TestWorkbenchRelationshipCoverageBindsExactRootDigest"}},
		},
		Claims: Claims{SourceFree: true, NoAccuracy: true, NoCompleteness: true, NoRuntimeTopology: true, NoScale: true, NoSLO: true, NoMigrationSafety: true, NoReleaseAuthority: true},
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
