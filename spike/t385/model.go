// Package t385 retains Epic 38's deterministic source-free product closure
// receipt. It composes earlier retained receipts and named production/UI
// gates; it does not create a new evidence or release authority.
package t385

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

const Schema = "t385-microservice-product-closure-v1"

type Receipt struct {
	Schema   string   `json:"schema"`
	Outcome  string   `json:"outcome"`
	Inputs   []Input  `json:"inputs"`
	Topology Topology `json:"topology"`
	Stages   []string `json:"stages"`
	Stories  []Story  `json:"stories"`
	Browser  Browser  `json:"browser"`
	Cases    []Case   `json:"cases"`
	Claims   Claims   `json:"claims"`
}

type Input struct {
	Ticket string `json:"ticket"`
	Schema string `json:"schema"`
	Digest string `json:"digest"`
}

type Topology struct {
	RelationshipRepositories int `json:"relationship_repositories"`
	RelationshipServices     int `json:"relationship_services"`
	SearchServices           int `json:"search_services"`
	WorkflowServices         int `json:"workflow_services"`
	WorkflowAccepted         int `json:"workflow_accepted_services"`
	WorkflowProposal         int `json:"workflow_proposal_services"`
	WorkflowConflict         int `json:"workflow_conflict_services"`
	WorkflowRemoved          int `json:"workflow_removed_services"`
}

type Story struct {
	Name                string   `json:"name"`
	Services            []string `json:"services"`
	WorkbenchSelections []string `json:"workbench_selections"`
	ExpectedStates      []string `json:"expected_states"`
}

type Browser struct {
	DesktopViewport string   `json:"desktop_viewport"`
	MobileViewport  string   `json:"mobile_viewport"`
	Checks          []string `json:"checks"`
}

type Case struct {
	Name       string   `json:"name"`
	Assertions []string `json:"assertions"`
	Gates      []string `json:"gates"`
}

type Claims struct {
	SourceFree           bool `json:"source_free"`
	NoAccuracy           bool `json:"no_accuracy"`
	NoCompleteness       bool `json:"no_completeness"`
	NoRuntimeTopology    bool `json:"no_runtime_topology"`
	NoTaskOrDecision     bool `json:"no_task_or_decision"`
	NoScale              bool `json:"no_scale"`
	NoSLO                bool `json:"no_slo"`
	NoMigrationSafety    bool `json:"no_migration_safety"`
	NoDecommissionSafety bool `json:"no_decommission_safety"`
	NoReleaseAuthority   bool `json:"no_release_authority"`
}

func Build() Receipt {
	return Receipt{
		Schema:  Schema,
		Outcome: "completed",
		Inputs: []Input{
			{Ticket: "T37.5", Schema: "t375-relationship-reader-demo-v1", Digest: "sha256:7f2aa20cedf98c1dbaa2da00102470ba00eaea4dc7a8e9c6e3de1069da3f7ec0"},
			{Ticket: "T38.1", Schema: "t381-service-overview-demo-v1", Digest: "sha256:65351feda11fb284da15a87ebc578b9aab54b02c6c91bda77aa60b7417e991b2"},
			{Ticket: "T38.2", Schema: "t382-cross-service-explorer-demo-v1", Digest: "sha256:ff4eadb4fd923695750585c4f75f1baca3ccd6e623896d182a78ac0040f5eb0b"},
			{Ticket: "T38.3", Schema: "t383-service-aware-workbench-demo-v1", Digest: "sha256:662dac9b997259a174529610257ca7e9474f129fdc4fc89a1fa507bfafe20a8e"},
			{Ticket: "T38.4", Schema: "t384-mcp-microservice-parity-v1", Digest: "sha256:a3e192d592e489c4bc834ae33b4b8ee90bd05d0040509dc174ea7e68c518e26b"},
		},
		Topology: Topology{RelationshipRepositories: 3, RelationshipServices: 4, SearchServices: 5, WorkflowServices: 5, WorkflowAccepted: 2, WorkflowProposal: 1, WorkflowConflict: 1, WorkflowRemoved: 1},
		Stages:   []string{"all_code_discovery", "service_overview", "dependency_evidence", "comparison", "workbench", "proof", "mcp"},
		Stories: []Story{
			{Name: "add", Services: []string{"orders-api", "returns-proposal"}, WorkbenchSelections: []string{"analogous", "proposal"}, ExpectedStates: []string{"exact", "unaccepted", "unowned", "gap"}},
			{Name: "modify", Services: []string{"orders-api", "orders-events"}, WorkbenchSelections: []string{"current"}, ExpectedStates: []string{"exact", "shared", "unresolved", "partial"}},
			{Name: "migrate", Services: []string{"legacy-orders", "orders-api"}, WorkbenchSelections: []string{"current", "replacement"}, ExpectedStates: []string{"added", "removed", "unchanged", "unavailable"}},
			{Name: "retire", Services: []string{"legacy-orders", "orders-api", "orders-events"}, WorkbenchSelections: []string{"current"}, ExpectedStates: []string{"exact", "stale", "gap", "unresolved"}},
		},
		Browser: Browser{
			DesktopViewport: "1440x1000",
			MobileViewport:  "390x844",
			Checks:          []string{"page_identity", "nonblank", "no_framework_overlay", "console_health", "screenshot_evidence", "keyboard_and_interaction", "no_document_overflow"},
		},
		Cases: []Case{
			{Name: "discovery_and_service_scope", Assertions: []string{"all_code_identity", "service_scope_receipt", "catalog_detail_authority"}, Gates: []string{"TestSharedSearchScopeReceiptDistinguishesAllCodeAndService", "service deep link preserves query while switching scope and renders receipt posture", "TestServiceDirectoryAuthorizationCursorAndDetail"}},
			{Name: "relationships_comparison_citations", Assertions: []string{"source_first_rows", "exact_two_generation_comparison", "immutable_citation", "gap_not_empty"}, Gates: []string{"uses broad authorized fallback and shares deterministic ids between exact rows and the optional diagram", "renders two exact generations, four classifications, and range-only citations", "reauthorizes and validates an immutable citation before showing source content"}},
			{Name: "four_workbench_stories", Assertions: []string{"add", "modify", "migrate", "retire", "service_authority_bound", "no_task_or_decision"}, Gates: []string{"TestWorkbenchImpactScenarioComposition", "TestT2114ScenarioFailureAccessibilityAndImplementationClosure", "TestWorkbenchChecklistCompletionHasNoTaskOrDecisionSurface"}},
			{Name: "proof_and_mcp", Assertions: []string{"coverage_gaps_explicit", "shared_http_mcp_types", "agent_evidence_only"}, Gates: []string{"TestRelationshipServiceAuthorizationGapAndRetainedProofShape", "TestMicroserviceMCPImpactUsesExactSharedContract", "TestMicroserviceMCPToolsAreStrictBoundedAndCapabilityGated"}},
			{Name: "failure_and_restart", Assertions: []string{"bounded_retry", "invalid_v2_no_fallback", "complete_recovery_before_pointer", "restart_noop", "root_change_refused"}, Gates: []string{"shows bounded errors and retries the same exact route", "TestServiceSearchRefusesInvalidV2WithoutLegacyFallback", "TestRecoveryValidatesCompleteGenerationBeforePointerSwap", "TestRuntimeSchedulesPublishesAndRestartNoOps", "TestWorkbenchImpactRejectsServiceRootChangeAtFinalFence"}},
			{Name: "responsive_accessibility_browser", Assertions: []string{"desktop", "390px", "named_controls", "focus_return", "route_reproducible", "console_clean"}, Gates: []string{"mobile repository drawer exposes an explicit collapsed lifecycle", "real hash navigation reaches How at a mobile viewport", "endpoint discovery has named controls, Escape focus return, and outside-click dismissal"}},
		},
		Claims: Claims{SourceFree: true, NoAccuracy: true, NoCompleteness: true, NoRuntimeTopology: true, NoTaskOrDecision: true, NoScale: true, NoSLO: true, NoMigrationSafety: true, NoDecommissionSafety: true, NoReleaseAuthority: true},
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
