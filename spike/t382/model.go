// Package t382 retains T38.2's deterministic source-free cross-service
// explorer receipt. Production reader and UI tests own exact filters,
// authorization, paging, citations, and table/diagram identity.
package t382

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

const Schema = "t382-cross-service-explorer-demo-v1"

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
	Repositories    int `json:"repositories"`
	RootReceipts    int `json:"root_receipts"`
	VisibleRows     int `json:"visible_rows"`
	DirectionModes  int `json:"direction_modes"`
	EvidenceKinds   int `json:"evidence_kinds"`
	VisualizationID int `json:"visualization_ids"`
}

type Reader struct {
	Bindings            int `json:"bindings_per_query"`
	PageRows            int `json:"page_rows"`
	MaximumPageRows     int `json:"maximum_page_rows"`
	MaximumRepositories int `json:"maximum_repositories"`
	DiagramRows         int `json:"maximum_diagram_rows"`
	CitationReads       int `json:"citation_reads_per_action"`
}

type Case struct {
	Name       string   `json:"name"`
	Assertions []string `json:"assertions"`
	Gates      []string `json:"gates"`
}

type Claims struct {
	SourceFree         bool `json:"source_free"`
	NoInventedEdges    bool `json:"no_invented_edges"`
	NoTransitivity     bool `json:"no_transitivity"`
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
		Fixture: Fixture{Repositories: 2, RootReceipts: 2, VisibleRows: 4, DirectionModes: 5, EvidenceKinds: 3, VisualizationID: 4},
		Reader:  Reader{Bindings: 1, PageRows: 50, MaximumPageRows: 100, MaximumRepositories: 32, DiagramRows: 50, CitationReads: 1},
		States:  []string{"ambiguous", "empty", "exact", "gap", "partial", "shared", "unavailable", "unowned"},
		Cases: []Case{
			{Name: "source_first_filters", Assertions: []string{"service_required", "repository_optional", "direction_kind_plane_lookup_exact", "cursor_query_bound"}, Gates: []string{"maps repository, direction, evidence, and exact contract filters into one bounded reader query"}},
			{Name: "broad_fallback_and_coverage", Assertions: []string{"authorization_first_visible_repository_fallback", "root_states_and_truncation_visible", "source_rows_primary"}, Gates: []string{"uses broad authorized fallback and shares deterministic ids between exact rows and the optional diagram", "TestRelationshipServiceAuthorizationGapAndRetainedProofShape"}},
			{Name: "deterministic_page_diagram", Assertions: []string{"table_and_diagram_ids_equal", "current_page_only", "no_transitive_or_invented_edges"}, Gates: []string{"uses broad authorized fallback and shares deterministic ids between exact rows and the optional diagram"}},
			{Name: "citation_authority", Assertions: []string{"one_action_one_read", "root_projection_object_content_span_fenced", "malformed_response_refused"}, Gates: []string{"reauthorizes and validates an immutable citation before showing source content", "TestRelationshipMCPToolsUseSharedProjectionAndStayDefaultDark"}},
			{Name: "state_honesty", Assertions: []string{"malformed_authority_refused", "gap_not_empty", "shared_unowned_ambiguous_visible"}, Gates: []string{"refuses malformed response authority and keeps a root gap distinct from exact empty"}},
		},
		Claims: Claims{SourceFree: true, NoInventedEdges: true, NoTransitivity: true, NoAccuracy: true, NoCompleteness: true, NoRuntimeTopology: true, NoScale: true, NoSLO: true, NoMigrationSafety: true, NoReleaseAuthority: true},
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
