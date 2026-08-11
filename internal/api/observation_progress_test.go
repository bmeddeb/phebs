package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"

	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestObservationProgressErrorsExposeOnlyClosedDiagnosticDetails(t *testing.T) {
	tests := []struct {
		name   string
		cause  error
		status int
		detail string
	}{
		{name: "stale", cause: observationpublication.ErrStale, status: http.StatusConflict, detail: ObservationProgressDetailStale},
		{name: "control absent", cause: os.ErrNotExist, status: http.StatusConflict, detail: ObservationProgressDetailControlAbsent},
		{name: "projection", cause: observationpublication.ErrInvalid, status: http.StatusInternalServerError, detail: ObservationProgressDetailProjection},
		{name: "control projection", cause: &observationpublication.ProgressReadError{Stage: observationpublication.ProgressReadStageControl, Err: observationpublication.ErrInvalid}, status: http.StatusInternalServerError, detail: ObservationProgressDetailControl},
		{name: "publication projection", cause: &observationpublication.ProgressReadError{Stage: observationpublication.ProgressReadStagePublication, Err: observationpublication.ErrInvalid}, status: http.StatusInternalServerError, detail: ObservationProgressDetailPublication},
		{name: "planning projection", cause: &observationpublication.ProgressReadError{Stage: observationpublication.ProgressReadStagePlanning, Err: observationpublication.ErrInvalid}, status: http.StatusInternalServerError, detail: ObservationProgressDetailPlanning},
		{name: "schedule projection", cause: &observationpublication.ProgressReadError{Stage: observationpublication.ProgressReadStageSchedule, Err: observationpublication.ErrInvalid}, status: http.StatusInternalServerError, detail: ObservationProgressDetailSchedule},
		{name: "crossed pointer is stale", cause: &observationpublication.ProgressReadError{Stage: observationpublication.ProgressReadStagePublication, Err: errors.Join(observationpublication.ErrInvalid, observationpublication.ErrStale)}, status: http.StatusConflict, detail: ObservationProgressDetailStale},
		{name: "store", cause: errors.New("private store failure"), status: http.StatusInternalServerError, detail: ObservationProgressDetailStore},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := observationProgressError(test.cause)
			if humaStatus(err) != test.status || err.Error() != test.detail ||
				strings.Contains(err.Error(), "private") {
				t.Fatalf("closed error = %d %q", humaStatus(err), err)
			}
		})
	}
}

type observationProgressRepoStore struct {
	store.Store
	repository store.Repo
	reads      int
	change     bool
}

func (state *observationProgressRepoStore) GetRepo(
	_ context.Context, name string,
) (*store.Repo, error) {
	state.reads++
	if name != state.repository.Name {
		return nil, store.ErrNotFound
	}
	value := state.repository
	if state.change && state.reads > 1 {
		value.EvidenceRevision++
	}
	return &value, nil
}

type observationProgressCapture struct {
	calls int
	value observationpublication.Progress
}

func (capture *observationProgressCapture) Read(
	_ context.Context, _ string,
) (observationpublication.Progress, error) {
	capture.calls++
	return capture.value, nil
}

func TestObservationProgressAuthorizesBeforeReadingCounts(t *testing.T) {
	repository := "example.com/acme/mono"
	now := time.Date(2026, 8, 5, 20, 0, 0, 0, time.UTC)
	state := &observationProgressRepoStore{repository: store.Repo{
		Name: repository, IndexedCommitHash: strings.Repeat("1", 40), IndexedAt: &now,
	}}
	capture := &observationProgressCapture{value: observationpublication.Progress{
		SchemaVersion: observationpublication.ProgressSchema, Repository: repository,
		State: "unavailable", SourceGenerationDigest: "sha256:" + strings.Repeat("a", 64),
	}}
	denied := NewObservationProgressService(Options{
		Store: state, Visible: func(context.Context) func(store.Repo) bool {
			return func(store.Repo) bool { return false }
		},
	}, capture)
	if _, err := denied.Read(t.Context(), repository); humaStatus(err) != http.StatusNotFound || capture.calls != 0 {
		t.Fatalf("denied progress = calls %d error %v", capture.calls, err)
	}

	state.reads = 0
	allowed := NewObservationProgressService(Options{Store: state}, capture)
	progress, err := allowed.Read(t.Context(), repository)
	if err != nil || progress.State != "unavailable" || capture.calls != 1 || state.reads != 2 {
		t.Fatalf("allowed progress = %+v calls=%d reads=%d error=%v", progress, capture.calls, state.reads, err)
	}

	state.reads = 0
	state.change = true
	if _, err := allowed.Read(t.Context(), repository); humaStatus(err) != http.StatusConflict {
		t.Fatalf("changing authority error = %v", err)
	}
}

func TestObservationProgressHTTPUsesSharedBoundedProjection(t *testing.T) {
	repository := "example.com/acme/mono"
	now := time.Date(2026, 8, 5, 20, 0, 0, 0, time.UTC)
	state := &observationProgressRepoStore{repository: store.Repo{
		Name: repository, IndexedCommitHash: strings.Repeat("1", 40), IndexedAt: &now,
	}}
	want := observationpublication.Progress{
		SchemaVersion: observationpublication.ProgressSchema, Repository: repository,
		State: "current", SourceGenerationDigest: "sha256:" + strings.Repeat("a", 64),
		Publication: &observationpublication.PublicationProgress{
			State: "current", GenerationDigest: "sha256:" + strings.Repeat("b", 64),
			SourceGenerationDigest: "sha256:" + strings.Repeat("a", 64),
			RecordCount:            1, ObservedCount: 1, ReceiptState: "complete",
			Receipt: &observationpublication.OperationReceipt{
				Schema: observationpublication.ReceiptSchema, InputBlobs: 1,
				SourceBlobReads: 1, ParsedObservations: 1, ObservedBlobs: 1,
				UnsupportedReasons: []observationpublication.ReasonCount{},
			},
		},
	}
	capture := &observationProgressCapture{value: want}
	opts := Options{Version: "test", Store: state}
	opts.ObservationProgress = NewObservationProgressService(opts, capture)
	handler := New(opts)
	request := httptest.NewRequest(http.MethodGet, ObservationProgressPath+"?repository="+repository, nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"unsupported_reasons":[]`) {
		t.Fatalf("HTTP zero-unsupported receipt = %s", recorder.Body.String())
	}
	var got observationpublication.Progress
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil ||
		got.SchemaVersion != observationpublication.ProgressSchema || got.Repository != want.Repository ||
		got.Publication == nil || got.Publication.Receipt == nil || got.Publication.Receipt.InputBlobs != 1 {
		t.Fatalf("HTTP progress = %+v, %v", got, err)
	}
}

func TestObservationProgressOpenAPISchemaGenerationIsWarningFree(t *testing.T) {
	config := huma.DefaultConfig("test", "test")
	_, testAPI := humatest.New(t, config)
	warning := captureObservationProgressSchemaStderr(t, func() {
		registerObservationProgressAPI(testAPI, Options{
			ObservationProgress: &ObservationProgressService{},
		})
	})
	if warning != "" {
		t.Fatalf("observation progress schema generation wrote stderr: %q", warning)
	}

	operation := testAPI.OpenAPI().Paths[ObservationProgressPath].Get
	if operation == nil {
		t.Fatal("observation progress OpenAPI operation is absent")
	}
	response := operation.Responses["200"]
	if response == nil || response.Content["application/json"] == nil {
		t.Fatal("observation progress OpenAPI response schema is absent")
	}
	ref := response.Content["application/json"].Schema.Ref
	schema := testAPI.OpenAPI().Components.Schemas.SchemaFromRef(ref)
	if ref == "" || schema == nil || schema.Properties["schema"] == nil ||
		schema.Properties["$schema"] == nil {
		t.Fatalf("observation progress OpenAPI schema = ref %q, schema %+v", ref, schema)
	}
}

func captureObservationProgressSchemaStderr(t *testing.T, action func()) string {
	t.Helper()
	original := os.Stderr
	capture, err := os.CreateTemp(t.TempDir(), "observation-progress-schema-stderr-*")
	if err != nil {
		t.Fatal(err)
	}
	func() {
		os.Stderr = capture
		defer func() { os.Stderr = original }()
		action()
	}()
	if err := capture.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(capture.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
