// Package t383 retains T38.3's deterministic source-free service-aware
// Workbench receipt. Production API, checklist, and UI tests own exact
// authorization, root fences, row projections, and human Dispositions.
package t383

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

const Schema = "t383-service-aware-workbench-demo-v1"

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
	ServiceSides      int `json:"service_sides"`
	RootReceipts      int `json:"root_receipts"`
	AffectedRows      int `json:"affected_rows"`
	CandidateRows     int `json:"candidate_rows"`
	HumanDispositions int `json:"human_dispositions"`
}

type Reader struct {
	SnapshotsPerRead    int `json:"maximum_snapshots_per_read"`
	RowsPerSnapshot     int `json:"maximum_rows_per_snapshot"`
	RetainedBindings    int `json:"retained_relationship_bindings"`
	MaximumRepositories int `json:"maximum_repositories_per_snapshot"`
	FinalRootSetFences  int `json:"final_root_set_fences"`
	MaximumSuggestions  int `json:"maximum_checklist_suggestions"`
}

type Case struct {
	Name       string   `json:"name"`
	Assertions []string `json:"assertions"`
	Gates      []string `json:"gates"`
}

type Claims struct {
	SourceFree           bool `json:"source_free"`
	NoImplicitWrite      bool `json:"no_implicit_write"`
	NoTaskCompletion     bool `json:"no_task_completion"`
	NoAccuracy           bool `json:"no_accuracy"`
	NoCompleteness       bool `json:"no_completeness"`
	NoRuntimeTopology    bool `json:"no_runtime_topology"`
	NoScale              bool `json:"no_scale"`
	NoSLO                bool `json:"no_slo"`
	NoMigrationSafety    bool `json:"no_migration_safety"`
	NoDecommissionSafety bool `json:"no_decommission_safety"`
	NoReleaseAuthority   bool `json:"no_release_authority"`
}

func Build() Receipt {
	return Receipt{
		Schema: Schema, Outcome: "completed",
		Fixture: Fixture{ServiceSides: 2, RootReceipts: 2, AffectedRows: 4, CandidateRows: 2, HumanDispositions: 3},
		Reader:  Reader{SnapshotsPerRead: 2, RowsPerSnapshot: 50, RetainedBindings: 0, MaximumRepositories: 32, FinalRootSetFences: 1, MaximumSuggestions: 1_000},
		States:  []string{"ambiguous", "current", "empty", "exact", "gap", "partial", "stale", "unowned", "unaccepted"},
		Cases: []Case{
			{Name: "service_snapshot_composition", Assertions: []string{"source_and_target_exact", "contract_lookup_exact", "zero_retained_bindings"}, Gates: []string{"TestWorkbenchImpactComposesExactServiceSnapshotsWithoutRetainedBindings"}},
			{Name: "final_authority_fence", Assertions: []string{"revision_rechecked", "root_set_rechecked", "changed_root_refused"}, Gates: []string{"TestWorkbenchImpactRejectsServiceRootChangeAtFinalFence"}},
			{Name: "source_first_product", Assertions: []string{"affected_services_visible", "unowned_candidate_visible", "exact_explorer_handoff"}, Gates: []string{"Where applies exact service scope and renders affected and unowned evidence"}},
			{Name: "fail_closed_and_reproducible", Assertions: []string{"malformed_root_refused", "source_target_route_preserved", "service_change_invalidates"}, Gates: []string{"Where refuses a service row whose root differs from final authority", "exact service scope survives revision and step deep links"}},
			{Name: "human_disposition_only", Assertions: []string{"fixed_categories", "stale_evidence_visible", "no_task_or_decision_surface"}, Gates: []string{"TestWorkbenchChecklistDispositionSupersessionAndStaleEvidence", "TestWorkbenchChecklistCompletionHasNoTaskOrDecisionSurface"}},
		},
		Claims: Claims{SourceFree: true, NoImplicitWrite: true, NoTaskCompletion: true, NoAccuracy: true, NoCompleteness: true, NoRuntimeTopology: true, NoScale: true, NoSLO: true, NoMigrationSafety: true, NoDecommissionSafety: true, NoReleaseAuthority: true},
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
