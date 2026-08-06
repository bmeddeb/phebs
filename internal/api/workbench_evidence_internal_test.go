package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/store"
)

const (
	workbenchEvidenceInvestigationID = "01J00000000000000000000000"
	workbenchEvidenceRevisionID      = "ivr_fixture"
)

type workbenchImpactAPIFake struct {
	principal string
	request   WorkbenchImpactRequest
	err       error
}

func (fake *workbenchImpactAPIFake) Read(
	_ context.Context,
	principal string,
	request WorkbenchImpactRequest,
) (*WorkbenchImpactPage, error) {
	fake.principal = principal
	fake.request = request
	if fake.err != nil {
		return nil, fake.err
	}
	return &WorkbenchImpactPage{
		SchemaVersion:   workbenchImpactSchemaVersion,
		InvestigationID: request.InvestigationID,
		RevisionID:      request.RevisionID,
		TicketKind:      store.ChangeBriefModify,
		Atlas:           []WorkbenchAtlasImpact{},
		Callers: []WorkbenchCallerImpact{{
			Selection: store.ChangeBriefContractSelection{
				Role:     store.ChangeBriefCurrent,
				Protocol: "protobuf", Repository: "github.com/acme/contracts",
				DeclarationLineage: "proto/cart.proto:shop.Cart",
				CanonicalOperation: "/shop.Cart/Get",
			},
			Query: CallerMapQuery{Endpoint: CallerMapEndpoint{
				Protocol: "protobuf", Repository: "github.com/acme/contracts",
				Lineage: "proto/cart.proto:shop.Cart", Operation: "/shop.Cart/Get",
			}},
			Generation: CallerMapGeneration{
				State: "current", Plane: exactCallerMapPlane,
				Repository: "github.com/acme/contracts",
			},
			MatchingRowsState:    "exact",
			ResolvedCallers:      []CallerMapRow{},
			ExtractorAbstentions: []CallerMapRow{},
			TotalMatchingRows:    callerMapTotal(0),
			Pagination:           CallerMapPagination{Complete: true},
		}},
		ResourcePlanes: []ResourcePlaneSnapshot{},
		AnalysisScope: WorkbenchAnalysisScope{
			Coverage:     []WorkbenchImpactCoverage{},
			Capabilities: []WorkbenchImpactCapability{},
			Gaps:         []WorkbenchImpactGap{},
		},
		Pagination: WorkbenchImpactPagination{Complete: true},
		Caveat:     workbenchImpactCaveat,
	}, nil
}

type workbenchImplementationAPIFake struct {
	principal string
	request   WorkbenchImplementationRequest
}

func (fake *workbenchImplementationAPIFake) Read(
	_ context.Context,
	principal string,
	request WorkbenchImplementationRequest,
) (*WorkbenchImplementationPage, error) {
	fake.principal = principal
	fake.request = request
	return &WorkbenchImplementationPage{
		SchemaVersion:   workbenchImplementationSchemaVersion,
		InvestigationID: request.InvestigationID,
		RevisionID:      request.RevisionID,
		TicketKind:      store.ChangeBriefModify,
		Rows:            []WorkbenchImplementationRow{},
		Capabilities:    []WorkbenchImplementationCapability{},
		Gaps:            []WorkbenchImplementationGap{},
		Pagination: WorkbenchImplementationPagination{
			Complete: true,
		},
		Caveat: workbenchImplementationCaveat,
	}, nil
}

type workbenchChecklistAPIFake struct {
	readPrincipal  string
	readRequest    WorkbenchChecklistRequest
	writePrincipal string
	writeMutation  WorkbenchDispositionMutation
	dispositionID  string
	writeErr       error
}

func (fake *workbenchChecklistAPIFake) Read(
	_ context.Context,
	principal string,
	request WorkbenchChecklistRequest,
) (*WorkbenchChecklistPage, error) {
	fake.readPrincipal = principal
	fake.readRequest = request
	return &WorkbenchChecklistPage{
		SchemaVersion:   workbenchChecklistSchemaVersion,
		InvestigationID: request.InvestigationID,
		RevisionID:      request.RevisionID,
		TicketKind:      store.ChangeBriefModify,
		Entries:         []WorkbenchChecklistEntry{},
		Pagination: WorkbenchChecklistPagination{
			Complete: true,
		},
		Caveat: workbenchChecklistCaveat,
	}, nil
}

func (fake *workbenchChecklistAPIFake) RecordDisposition(
	_ context.Context,
	principal string,
	mutation WorkbenchDispositionMutation,
) (*store.WorkbenchDisposition, error) {
	fake.writePrincipal = principal
	fake.writeMutation = mutation
	if fake.writeErr != nil {
		return nil, fake.writeErr
	}
	return &store.WorkbenchDisposition{
		SchemaVersion:   store.WorkbenchDispositionSchemaVersion,
		ID:              fake.dispositionID,
		InvestigationID: mutation.InvestigationID,
		RevisionID:      mutation.ExpectedRevisionID,
		Suggestion:      mutation.Suggestion,
		Category:        mutation.Category,
		Actor:           principal,
		Authority:       "human",
		Sequence:        1,
	}, nil
}

func TestWorkbenchEvidenceHumaIsThinExactAndDefaultDark(t *testing.T) {
	impact := &workbenchImpactAPIFake{}
	implementation := &workbenchImplementationAPIFake{}
	checklist := &workbenchChecklistAPIFake{
		dispositionID: "iwd_fixture",
	}
	handler := New(Options{
		Version: "test",
		Store:   &investigationRepoStore{},
		Principal: func(context.Context) string {
			return "user:workbench-evidence-api"
		},
		InvestigationMutation:   func(context.Context) bool { return true },
		Workbench:               &workbenchAPIFake{},
		WorkbenchImpact:         impact,
		WorkbenchImplementation: implementation,
		WorkbenchChecklist:      checklist,
	})
	base := "/api/workbenches/" + workbenchEvidenceInvestigationID +
		"/revisions/" + workbenchEvidenceRevisionID

	get := func(target string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, target, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	filters := url.QueryEscape(`{"unit":"checkout","ordering":"unit"}`)
	impactResponse := get(
		base + "/impact?filters=" + filters +
			"&compatibility_run_id=run-1&page_size=25&cursor=impact-next",
	)
	if impactResponse.Code != http.StatusOK ||
		impact.principal != "user:workbench-evidence-api" ||
		impact.request.InvestigationID != workbenchEvidenceInvestigationID ||
		impact.request.RevisionID != workbenchEvidenceRevisionID ||
		impact.request.Filters.Unit != "checkout" ||
		impact.request.Filters.Ordering != "unit" ||
		impact.request.CompatibilityRun != "run-1" ||
		impact.request.PageSize != 25 ||
		impact.request.Cursor != "impact-next" {
		t.Fatalf(
			"impact transport = %d %s; fake=%+v",
			impactResponse.Code,
			impactResponse.Body,
			impact,
		)
	}
	if body := impactResponse.Body.String(); !strings.Contains(body, `"schema_version":"workbench-impact-inventory-v3"`) ||
		!strings.Contains(body, `"matching_rows_state":"exact"`) ||
		!strings.Contains(body, `"plane":"repository-overlay"`) ||
		strings.Contains(body, `"attribution_digest"`) {
		t.Fatalf("impact exact transport contract = %s", body)
	}

	anchors := url.QueryEscape(`[{"repository":"example/client","commit":"` +
		strings.Repeat("a", 40) +
		`","path":"client.go","line":7,"character":3,"encoding":"utf-8"}]`)
	implementationResponse := get(
		base + "/implementation?anchors=" + anchors +
			"&page_size=20&cursor=implementation-next",
	)
	if implementationResponse.Code != http.StatusOK ||
		implementation.principal != "user:workbench-evidence-api" ||
		len(implementation.request.Anchors) != 1 ||
		implementation.request.Anchors[0].Path != "client.go" ||
		implementation.request.Cursor != "implementation-next" {
		t.Fatalf(
			"implementation transport = %d %s; fake=%+v",
			implementationResponse.Code,
			implementationResponse.Body,
			implementation,
		)
	}

	evidence := url.QueryEscape(
		`{"impact_filters":{"unit":"checkout"},"anchors":[]}`,
	)
	checklistResponse := get(
		base + "/checklist?evidence=" + evidence +
			"&page_size=10&cursor=checklist-next",
	)
	if checklistResponse.Code != http.StatusOK ||
		checklist.readPrincipal != "user:workbench-evidence-api" ||
		checklist.readRequest.Evidence.ImpactFilters.Unit != "checkout" ||
		checklist.readRequest.Cursor != "checklist-next" {
		t.Fatalf(
			"checklist transport = %d %s; fake=%+v",
			checklistResponse.Code,
			checklistResponse.Body,
			checklist,
		)
	}

	suggestion := store.WorkbenchSuggestion{
		SchemaVersion:          store.WorkbenchSuggestionSchemaVersion,
		ID:                     "iws_fixture",
		InvestigationID:        workbenchEvidenceInvestigationID,
		RevisionID:             workbenchEvidenceRevisionID,
		Kind:                   "resolve_analysis_gap",
		Summary:                "Resolve the explicit gap.",
		SelectionRule:          "one gap yields one suggestion",
		EvidenceSnapshotDigest: "sha256:" + strings.Repeat("b", 64),
		ContentDigest:          "sha256:" + strings.Repeat("c", 64),
	}
	mutation := WorkbenchDispositionMutation{
		InvestigationID:    workbenchEvidenceInvestigationID,
		ExpectedRevisionID: workbenchEvidenceRevisionID,
		IdempotencyKey:     "ui-disposition",
		Evidence: WorkbenchChecklistEvidenceInput{
			ImpactFilters: WorkbenchImpactFilters{Unit: "checkout"},
			Anchors:       []WorkbenchImplementationAnchor{},
		},
		Suggestion: suggestion,
		Category:   "accepted",
	}
	dispositionResponse := postInvestigationJSON(
		t,
		handler,
		base+"/dispositions",
		mutation,
	)
	if dispositionResponse.Code != http.StatusOK ||
		checklist.writePrincipal != "user:workbench-evidence-api" ||
		checklist.writeMutation.IdempotencyKey != "ui-disposition" {
		t.Fatalf(
			"disposition transport = %d %s; fake=%+v",
			dispositionResponse.Code,
			dispositionResponse.Body,
			checklist,
		)
	}

	invalid := get(base + "/impact?filters=" +
		url.QueryEscape(`{"unknown":"value"}`))
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("unknown filter = %d %s", invalid.Code, invalid.Body)
	}
	mutation.ExpectedRevisionID = "ivr_other"
	mismatch := postInvestigationJSON(
		t,
		handler,
		base+"/dispositions",
		mutation,
	)
	if mismatch.Code != http.StatusBadRequest {
		t.Fatalf("disposition path mismatch = %d %s", mismatch.Code, mismatch.Body)
	}
	impact.err = fmt.Errorf("hidden repository: %w", store.ErrNotFound)
	unavailable := get(base + "/impact")
	if unavailable.Code != http.StatusNotFound ||
		strings.Contains(unavailable.Body.String(), "hidden repository") {
		t.Fatalf(
			"non-disclosing unavailable = %d %s",
			unavailable.Code,
			unavailable.Body,
		)
	}
	impact.err = nil

	openAPI := get("/api/openapi.json")
	for _, expected := range []string{
		`"/api/workbenches/{investigation_id}/revisions/{revision_id}/impact"`,
		`"/api/workbenches/{investigation_id}/revisions/{revision_id}/implementation"`,
		`"/api/workbenches/{investigation_id}/revisions/{revision_id}/checklist"`,
		`"/api/workbenches/{investigation_id}/revisions/{revision_id}/dispositions"`,
	} {
		if !strings.Contains(openAPI.Body.String(), expected) {
			t.Fatalf("enabled OpenAPI missing %s", expected)
		}
	}
	version := get("/api/version")
	if !strings.Contains(
		version.Body.String(),
		`"change-workbench-evidence"`,
	) {
		t.Fatalf(
			"enabled version omitted evidence capability: %s",
			version.Body,
		)
	}

	dark := New(Options{
		Version: "test",
		Store:   &investigationRepoStore{},
		Principal: func(context.Context) string {
			return "user:workbench-evidence-api"
		},
		Workbench: &workbenchAPIFake{},
	})
	for _, target := range []string{
		base + "/impact",
		base + "/implementation",
		base + "/checklist",
	} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		response := httptest.NewRecorder()
		dark.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("dark %s = %d %s", target, response.Code, response.Body)
		}
	}
	darkOpenAPI := httptest.NewRecorder()
	dark.ServeHTTP(
		darkOpenAPI,
		httptest.NewRequest(http.MethodGet, "/api/openapi.json", nil),
	)
	if strings.Contains(
		darkOpenAPI.Body.String(),
		"read-workbench-impact",
	) {
		t.Fatal("dark OpenAPI exposed Workbench evidence operations")
	}
	darkVersion := httptest.NewRecorder()
	dark.ServeHTTP(
		darkVersion,
		httptest.NewRequest(http.MethodGet, "/api/version", nil),
	)
	if strings.Contains(
		darkVersion.Body.String(),
		`"change-workbench-evidence"`,
	) {
		t.Fatal("dark version exposed Workbench evidence capability")
	}
}

func TestWorkbenchDispositionCredentialGateIsAdditionalAndNonDisclosing(
	t *testing.T,
) {
	allowed := false
	checklist := &workbenchChecklistAPIFake{
		dispositionID: "iwd_capability",
	}
	handler := New(Options{
		Version: "test",
		Store:   &investigationRepoStore{},
		Principal: func(context.Context) string {
			return "user:workbench-owner"
		},
		InvestigationMutation: func(context.Context) bool {
			return allowed
		},
		Workbench:               &workbenchAPIFake{},
		WorkbenchImpact:         &workbenchImpactAPIFake{},
		WorkbenchImplementation: &workbenchImplementationAPIFake{},
		WorkbenchChecklist:      checklist,
	})
	base := "/api/workbenches/" + workbenchEvidenceInvestigationID +
		"/revisions/" + workbenchEvidenceRevisionID + "/dispositions"
	mutation := WorkbenchDispositionMutation{
		InvestigationID:    workbenchEvidenceInvestigationID,
		ExpectedRevisionID: workbenchEvidenceRevisionID,
		IdempotencyKey:     "capability-gate",
		Evidence: WorkbenchChecklistEvidenceInput{
			ImpactFilters: WorkbenchImpactFilters{},
			Anchors:       []WorkbenchImplementationAnchor{},
		},
		Suggestion: store.WorkbenchSuggestion{
			SchemaVersion:          store.WorkbenchSuggestionSchemaVersion,
			ID:                     "iws_capability",
			InvestigationID:        workbenchEvidenceInvestigationID,
			RevisionID:             workbenchEvidenceRevisionID,
			Kind:                   "resolve_analysis_gap",
			Summary:                "Resolve the explicit gap.",
			SelectionRule:          "one gap yields one suggestion",
			EvidenceSnapshotDigest: "sha256:" + strings.Repeat("b", 64),
			ContentDigest:          "sha256:" + strings.Repeat("c", 64),
		},
		Category: "accepted",
	}
	denied := postInvestigationJSON(t, handler, base, mutation)
	if denied.Code != http.StatusNotFound ||
		!strings.Contains(denied.Body.String(), "workbench resource not found") ||
		checklist.writePrincipal != "" {
		t.Fatalf(
			"read-only owner disposition = %d %s; fake=%+v",
			denied.Code,
			denied.Body,
			checklist,
		)
	}

	allowed = true
	checklist.writeErr = store.ErrNotFound
	nonOwner := postInvestigationJSON(t, handler, base, mutation)
	if nonOwner.Code != http.StatusNotFound ||
		!strings.Contains(nonOwner.Body.String(), "workbench resource not found") {
		t.Fatalf(
			"non-owner disposition = %d %s",
			nonOwner.Code,
			nonOwner.Body,
		)
	}
	checklist.writeErr = nil
	reached := postInvestigationJSON(t, handler, base, mutation)
	if reached.Code != http.StatusOK ||
		checklist.writePrincipal != "user:workbench-owner" {
		t.Fatalf(
			"write-capable owner disposition = %d %s; fake=%+v",
			reached.Code,
			reached.Body,
			checklist,
		)
	}
}
