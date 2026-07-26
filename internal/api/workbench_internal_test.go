package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/store"
)

type workbenchAPIFake struct {
	previewPrincipal string
	createPrincipal  string
	revisePrincipal  string
	readPrincipal    string
	previewPlan      store.WorkbenchPlan
	createRequest    store.WorkbenchMutationRequest
	reviseRequest    store.WorkbenchMutationRequest
	readID           string
	readErr          error
}

func (fake *workbenchAPIFake) Preview(
	_ context.Context,
	principal string,
	plan store.WorkbenchPlan,
) (*store.WorkbenchPreview, error) {
	fake.previewPrincipal = principal
	fake.previewPlan = plan
	return &store.WorkbenchPreview{
		SchemaVersion: store.WorkbenchPreviewSchemaVersion,
		Operation:     "create",
		Ready:         true,
		PreviewDigest: "sha256:" + strings.Repeat("a", 64),
	}, nil
}

func (fake *workbenchAPIFake) Create(
	_ context.Context,
	principal string,
	request store.WorkbenchMutationRequest,
) (*store.WorkbenchView, error) {
	fake.createPrincipal = principal
	fake.createRequest = request
	return workbenchAPIView(request.Plan), nil
}

func (fake *workbenchAPIFake) Revise(
	_ context.Context,
	principal string,
	request store.WorkbenchMutationRequest,
) (*store.WorkbenchView, error) {
	fake.revisePrincipal = principal
	fake.reviseRequest = request
	return workbenchAPIView(request.Plan), nil
}

func (fake *workbenchAPIFake) Read(
	_ context.Context,
	principal, investigationID string,
) (*store.WorkbenchView, error) {
	fake.readPrincipal = principal
	fake.readID = investigationID
	if fake.readErr != nil {
		return nil, fake.readErr
	}
	return workbenchAPIView(store.WorkbenchPlan{
		InvestigationID: investigationID,
	}), nil
}

func workbenchAPIView(plan store.WorkbenchPlan) *store.WorkbenchView {
	investigationID := plan.InvestigationID
	if investigationID == "" {
		investigationID = "01J00000000000000000000000"
	}
	return &store.WorkbenchView{
		Investigation: store.Investigation{
			ID:                investigationID,
			Owner:             "user:workbench-api",
			Lifecycle:         "active",
			CurrentRevisionID: "ivr_fixture",
		},
		Revision: store.Revision{
			ID:              "ivr_fixture",
			InvestigationID: investigationID,
			Seq:             1,
		},
		Brief: store.ChangeBrief{
			ID:              "icb_fixture",
			InvestigationID: investigationID,
			RevisionID:      "ivr_fixture",
		},
	}
}

func workbenchAPIPlan() store.WorkbenchPlan {
	return store.WorkbenchPlan{
		Referent:    "grpc:/shop.v1.Checkout/Submit",
		ClaimFamily: "change-workbench",
		Title:       "Checkout change",
		Revision: store.WorkbenchRevisionDraft{
			NormalizedQuestion: "what changes?",
			DecisionSought:     "approve change",
			SnapshotPolicy:     "exact commit",
			BuildConfiguration: "linux/amd64",
			EnumerationMethod:  "exact selection",
		},
		Brief: store.WorkbenchChangeBriefDraft{
			TicketKind:      store.ChangeBriefModify,
			Problem:         "Receipt identifiers are unstable.",
			DesiredOutcome:  "Receipt identifiers are stable.",
			SuccessCriteria: []string{"The identifier is returned."},
			What: store.WorkbenchWhatDraft{
				Selections: []store.ChangeBriefContractSelection{{
					Role:               store.ChangeBriefCurrent,
					Protocol:           "protobuf",
					Repository:         "example/contracts",
					DeclarationLineage: "proto/shop.proto:shop.Checkout",
					CanonicalOperation: "/shop.Checkout/Submit",
				}},
			},
		},
		Repositories: []string{"example/contracts"},
		Capabilities: []string{"contract-atlas"},
	}
}

func TestWorkbenchHumaIsThinBoundedAndDefaultDark(t *testing.T) {
	repoStore := &investigationRepoStore{}
	fake := &workbenchAPIFake{}
	handler := New(Options{
		Version: "test",
		Store:   repoStore,
		Principal: func(context.Context) string {
			return "user:workbench-api"
		},
		Workbench: fake,
	})
	plan := workbenchAPIPlan()
	preview := postInvestigationJSON(
		t,
		handler,
		"/api/workbench_previews",
		plan,
	)
	if preview.Code != http.StatusOK ||
		fake.previewPrincipal != "user:workbench-api" ||
		fake.previewPlan.Title != plan.Title {
		t.Fatalf("preview transport = %d %s; fake=%+v", preview.Code, preview.Body, fake)
	}
	request := store.WorkbenchMutationRequest{
		Plan:           plan,
		PreviewDigest:  "sha256:" + strings.Repeat("a", 64),
		IdempotencyKey: "api-create",
	}
	created := postInvestigationJSON(t, handler, "/api/workbenches", request)
	if created.Code != http.StatusCreated ||
		fake.createPrincipal != "user:workbench-api" ||
		fake.createRequest.IdempotencyKey != "api-create" {
		t.Fatalf("create transport = %d %s; fake=%+v", created.Code, created.Body, fake)
	}

	request.Plan.InvestigationID = "01J00000000000000000000000"
	request.Plan.ExpectedRevisionID = "ivr_fixture"
	request.IdempotencyKey = "api-revise"
	revised := postInvestigationJSON(
		t,
		handler,
		"/api/workbenches/01J00000000000000000000000/revisions",
		request,
	)
	if revised.Code != http.StatusOK ||
		fake.revisePrincipal != "user:workbench-api" ||
		fake.reviseRequest.IdempotencyKey != "api-revise" {
		t.Fatalf("revise transport = %d %s; fake=%+v", revised.Code, revised.Body, fake)
	}
	request.Plan.InvestigationID = "01J00000000000000000000001"
	mismatch := postInvestigationJSON(
		t,
		handler,
		"/api/workbenches/01J00000000000000000000000/revisions",
		request,
	)
	if mismatch.Code != http.StatusBadRequest {
		t.Fatalf("path mismatch = %d %s", mismatch.Code, mismatch.Body)
	}

	readRequest := httptest.NewRequest(
		http.MethodGet,
		"/api/workbenches/01J00000000000000000000000",
		nil,
	)
	read := httptest.NewRecorder()
	handler.ServeHTTP(read, readRequest)
	if read.Code != http.StatusOK ||
		fake.readPrincipal != "user:workbench-api" ||
		fake.readID != "01J00000000000000000000000" {
		t.Fatalf("read transport = %d %s; fake=%+v", read.Code, read.Body, fake)
	}

	openAPIRequest := httptest.NewRequest(http.MethodGet, "/api/openapi.json", nil)
	openAPI := httptest.NewRecorder()
	handler.ServeHTTP(openAPI, openAPIRequest)
	for _, want := range []string{
		`"/api/workbench_previews"`,
		`"maxLength":4194304`,
		`"maxItems":256`,
		`"maxItems":1000`,
	} {
		if !strings.Contains(openAPI.Body.String(), want) {
			t.Fatalf("enabled OpenAPI missing %s", want)
		}
	}
	versionRequest := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	version := httptest.NewRecorder()
	handler.ServeHTTP(version, versionRequest)
	if !strings.Contains(version.Body.String(), `"change-workbench"`) {
		t.Fatalf("enabled version omitted Workbench capability: %s", version.Body)
	}

	dark := New(Options{
		Version: "test",
		Store:   repoStore,
		Principal: func(context.Context) string {
			return "user:workbench-api"
		},
	})
	for _, target := range []string{
		"/api/workbench_previews",
		"/api/workbenches",
	} {
		response := postInvestigationJSON(t, dark, target, plan)
		if response.Code != http.StatusNotFound {
			t.Fatalf("dark %s = %d %s", target, response.Code, response.Body)
		}
	}
	darkOpenAPI := httptest.NewRecorder()
	dark.ServeHTTP(darkOpenAPI, openAPIRequest)
	if strings.Contains(darkOpenAPI.Body.String(), "workbench") {
		t.Fatal("dark OpenAPI exposed Workbench operations")
	}
	darkVersion := httptest.NewRecorder()
	dark.ServeHTTP(darkVersion, versionRequest)
	if strings.Contains(darkVersion.Body.String(), `"change-workbench"`) {
		t.Fatal("dark version exposed Workbench capability")
	}
}

func TestWorkbenchHumaUnknownAndUnauthorizedRefusalsMatch(t *testing.T) {
	fake := &workbenchAPIFake{readErr: store.ErrNotFound}
	handler := New(Options{
		Version: "test",
		Store:   &investigationRepoStore{},
		Principal: func(context.Context) string {
			return "user:workbench-api"
		},
		Workbench: fake,
	})
	responses := make([]*httptest.ResponseRecorder, 2)
	for index, id := range []string{
		"01J00000000000000000000000",
		"01J00000000000000000000001",
	} {
		request := httptest.NewRequest(
			http.MethodGet,
			"/api/workbenches/"+id,
			nil,
		)
		responses[index] = httptest.NewRecorder()
		handler.ServeHTTP(responses[index], request)
	}
	if responses[0].Code != http.StatusNotFound ||
		responses[1].Code != http.StatusNotFound ||
		responses[0].Body.String() != responses[1].Body.String() {
		t.Fatalf(
			"refusals differ: %d %s / %d %s",
			responses[0].Code,
			responses[0].Body,
			responses[1].Code,
			responses[1].Body,
		)
	}
}
