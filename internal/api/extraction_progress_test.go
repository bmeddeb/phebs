package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/store"
)

type extractionProgressCapture struct {
	calls int
	value extractionpublication.Progress
}

func (capture *extractionProgressCapture) Progress(
	_ context.Context, _ string,
) (extractionpublication.Progress, error) {
	capture.calls++
	return capture.value, nil
}

func TestExtractionProgressAuthorizesBeforeReadingAndUsesClosedProjection(t *testing.T) {
	repository := "example.com/acme/mono"
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	state := &observationProgressRepoStore{repository: store.Repo{
		Name: repository, IndexedCommitHash: strings.Repeat("1", 40), IndexedAt: &now,
	}}
	capture := &extractionProgressCapture{value: extractionpublication.Progress{
		State: "active", Total: 8, Materialized: 8, Pending: 3,
		Running: 1, Succeeded: 4, Domains: 4,
	}}
	denied := NewExtractionProgressService(Options{
		Store: state, Visible: func(context.Context) func(store.Repo) bool {
			return func(store.Repo) bool { return false }
		},
	}, capture)
	if _, err := denied.Read(t.Context(), repository); humaStatus(err) != http.StatusNotFound || capture.calls != 0 {
		t.Fatalf("denied progress = calls %d error %v", capture.calls, err)
	}

	opts := Options{Version: "test", Store: state}
	opts.ExtractionProgress = NewExtractionProgressService(opts, capture)
	// Register both progress routes: their distinct package types must also
	// retain distinct Huma/OpenAPI component identities in the complete server.
	opts.ObservationProgress = NewObservationProgressService(opts, &observationProgressCapture{})
	handler := New(opts)
	request := httptest.NewRequest(http.MethodGet, ExtractionProgressPath+"?repository="+repository, nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var got extractionpublication.Progress
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil || got != capture.value || capture.calls != 1 {
		t.Fatalf("HTTP progress = %+v calls=%d error=%v", got, capture.calls, err)
	}
}
