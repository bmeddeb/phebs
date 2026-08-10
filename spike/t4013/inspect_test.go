package t4013

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apiresponse "github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/search"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestProfileInspectorClassifiesClosedObservationProgressStatuses(t *testing.T) {
	tests := []struct {
		name   string
		status int
		detail string
		reason string
	}{
		{name: "stale", status: http.StatusConflict, detail: apiresponse.ObservationProgressDetailStale, reason: httpReason409Stale},
		{name: "control absent", status: http.StatusConflict, detail: apiresponse.ObservationProgressDetailControlAbsent, reason: httpReason409ControlAbsent},
		{name: "store", status: http.StatusInternalServerError, detail: apiresponse.ObservationProgressDetailStore, reason: httpReason500Store},
		{name: "projection response", status: http.StatusInternalServerError, detail: apiresponse.ObservationProgressDetailProjection, reason: httpReason500Response},
		{name: "projection control", status: http.StatusInternalServerError, detail: apiresponse.ObservationProgressDetailControl, reason: httpReason500Control},
		{name: "projection publication", status: http.StatusInternalServerError, detail: apiresponse.ObservationProgressDetailPublication, reason: httpReason500Publication},
		{name: "projection planning", status: http.StatusInternalServerError, detail: apiresponse.ObservationProgressDetailPlanning, reason: httpReason500Planning},
		{name: "projection schedule", status: http.StatusInternalServerError, detail: apiresponse.ObservationProgressDetailSchedule, reason: httpReason500Schedule},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				response.Header().Set("Content-Type", "application/problem+json")
				response.WriteHeader(test.status)
				_, _ = fmt.Fprintf(response,
					`{"$schema":"http://%s/schemas/ErrorModel.json","title":"closed","status":%d,"detail":%q}`,
					request.Host, test.status, test.detail,
				)
			}))
			defer server.Close()
			inspector := &profileInspector{client: server.Client(), credential: "private-test-token"}
			profile := PreparedProfile{Address: strings.TrimPrefix(server.URL, "http://")}
			var target struct{}
			err := inspector.get(t.Context(), profile, "/api/observation-progress", &target)
			var statusErr *privateHTTPStatusError
			if !errors.As(err, &statusErr) || statusErr.Status != test.status || statusErr.Reason != test.reason {
				t.Fatalf("status error = %+v, %v", statusErr, err)
			}
			diagnostic := classifyConvergenceInspection(err)
			if diagnostic.class != "status" || diagnostic.httpStatus != test.status ||
				diagnostic.httpReason != test.reason {
				t.Fatalf("diagnostic = %+v", diagnostic)
			}
		})
	}
}

type humaResponseStore struct {
	store.Store
	repositories []store.Repo
}

func (value humaResponseStore) ListRepos(context.Context) ([]store.Repo, error) {
	return append([]store.Repo(nil), value.repositories...), nil
}

func TestProfileInspectorConsumesRealHumaObjectAndArrayResponses(t *testing.T) {
	handler := apiresponse.New(apiresponse.Options{
		Version: "test", APIKey: "private-test-token",
		Store: humaResponseStore{repositories: []store.Repo{{Name: "example/repository"}}},
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	address := strings.TrimPrefix(server.URL, "http://")
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/api/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer private-test-token")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	raw, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		t.Fatal(readErr, closeErr)
	}
	var directHealth struct {
		Status string `json:"status"`
	}
	if err := decodeHumaResponse(raw, address, &directHealth); err != nil {
		t.Fatalf("decode real Huma health %q: %v", raw, err)
	}
	inspector := &profileInspector{client: server.Client(), credential: "private-test-token"}
	profile := PreparedProfile{Address: address}
	class, err := inspector.healthClass(t.Context(), profile)
	if err != nil || class != "ok" {
		t.Fatalf("real Huma health = %q, %v", class, err)
	}
	var repositories []store.Repo
	if err := inspector.get(t.Context(), profile, "/api/repos", &repositories); err != nil {
		t.Fatal(err)
	}
	if len(repositories) != 1 || repositories[0].Name != "example/repository" {
		t.Fatalf("real Huma repositories = %+v", repositories)
	}
}

func TestHumaResponseDecoderCoversEveryCeremonyObjectTarget(t *testing.T) {
	const address = "127.0.0.1:41731"
	tests := []struct {
		name   string
		target func() any
	}{
		{name: "health", target: func() any {
			return &struct {
				Status string `json:"status"`
			}{}
		}},
		{name: "observation progress", target: func() any { return &observationpublication.Progress{} }},
		{name: "lifecycle status", target: func() any { return &lifecycle.Status{} }},
		{name: "search", target: func() any { return &search.Result{} }},
		{name: "service inventory", target: func() any { return &apiresponse.ServiceInventory{} }},
		{name: "relationships", target: func() any { return &apiresponse.RelationshipPage{} }},
		{name: "citation", target: func() any { return &apiresponse.RelationshipCitation{} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := []byte(`{"$schema":"http://127.0.0.1:41731/schemas/CeremonyResponse.json"}`)
			if err := decodeHumaResponse(raw, address, test.target()); err != nil {
				t.Fatal(err)
			}
		})
	}
	var repositories []store.Repo
	if err := decodeHumaResponse([]byte(`[]`), address, &repositories); err != nil {
		t.Fatalf("top-level repository array: %v", err)
	}
}

func TestHumaResponseDecoderKeepsSchemaAndApplicationFieldsFailClosed(t *testing.T) {
	const address = "127.0.0.1:41731"
	validSchema := `"$schema":"http://127.0.0.1:41731/schemas/HealthOutBody.json"`
	tests := []struct {
		name string
		raw  string
	}{
		{name: "missing schema", raw: `{"status":"ok"}`},
		{name: "duplicate schema", raw: `{` + validSchema + `,` + validSchema + `,"status":"ok"}`},
		{name: "wrong host", raw: `{"$schema":"http://127.0.0.1:41732/schemas/HealthOutBody.json","status":"ok"}`},
		{name: "wrong scheme", raw: `{"$schema":"https://127.0.0.1:41731/schemas/HealthOutBody.json","status":"ok"}`},
		{name: "nested schema path", raw: `{"$schema":"http://127.0.0.1:41731/schemas/private/Health.json","status":"ok"}`},
		{name: "unknown application field", raw: `{` + validSchema + `,"status":"ok","extra":true}`},
		{name: "duplicate application field", raw: `{` + validSchema + `,"status":"ok","status":"ok"}`},
		{name: "trailing value", raw: `{` + validSchema + `,"status":"ok"} {}`},
		{name: "primitive", raw: `true`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var target struct {
				Status string `json:"status"`
			}
			if err := decodeHumaResponse([]byte(test.raw), address, &target); err == nil {
				t.Fatal("invalid Huma response passed")
			}
		})
	}
}
