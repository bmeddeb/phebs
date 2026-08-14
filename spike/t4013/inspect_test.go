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
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
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

func TestObservationProgressTerminalClassificationIsImmediateAndClosed(t *testing.T) {
	limit := pipelinerefusal.Receipt{
		Schema:         pipelinerefusal.Schema,
		Stage:          pipelinerefusal.StageObservationPublication,
		GenerationKind: pipelinerefusal.GenerationObservation,
		Classification: pipelinerefusal.ClassificationLimit,
		Dimension:      pipelinerefusal.DimensionGenerationRecords,
		Observed:       262_144, Limit: 250_000,
	}
	tests := []struct {
		name     string
		progress observationpublication.Progress
		want     error
	}{
		{name: "building", progress: observationpublication.Progress{
			State: "building", Planning: &observationpublication.PlanningProgress{State: "active"},
		}},
		{name: "closed limit", progress: observationpublication.Progress{
			State: "failed", Planning: &observationpublication.PlanningProgress{State: "failed", Refusal: &limit},
		}, want: errObservationBoundRefusal},
		{name: "unrelated limit remains unclassified", progress: observationpublication.Progress{
			State: "failed", Planning: &observationpublication.PlanningProgress{State: "failed", Refusal: &pipelinerefusal.Receipt{
				Schema: pipelinerefusal.Schema, Stage: pipelinerefusal.StageObservationPublication,
				GenerationKind: pipelinerefusal.GenerationObservation,
				Classification: pipelinerefusal.ClassificationLimit,
				Dimension:      pipelinerefusal.DimensionGenerationEncodedBytes,
				Observed:       2, Limit: 1,
			}},
		}, want: errObservationTerminal},
		{name: "wrong frozen limit remains unclassified", progress: observationpublication.Progress{
			State: "failed", Planning: &observationpublication.PlanningProgress{State: "failed", Refusal: &pipelinerefusal.Receipt{
				Schema: pipelinerefusal.Schema, Stage: pipelinerefusal.StageObservationPublication,
				GenerationKind: pipelinerefusal.GenerationObservation,
				Classification: pipelinerefusal.ClassificationLimit,
				Dimension:      pipelinerefusal.DimensionGenerationRecords,
				Observed:       262_144, Limit: 249_999,
			}},
		}, want: errObservationTerminal},
		{name: "terminal without limit", progress: observationpublication.Progress{
			State: "failed", Planning: &observationpublication.PlanningProgress{State: "failed"},
		}, want: errObservationTerminal},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := observationTerminal(test.progress)
			if !errors.Is(err, test.want) {
				t.Fatalf("terminal = %v, want %v", err, test.want)
			}
			wantClass := "complete"
			if test.want != nil {
				wantClass = "terminal"
			}
			if got := classifyConvergenceInspection(err).class; got != wantClass {
				t.Fatalf("class = %q, want %q", got, wantClass)
			}
		})
	}
}

type humaResponseStore struct {
	store.Store
	repositories []store.Repo
	statuses     []store.RepoStatus
}

func (value humaResponseStore) ListRepos(context.Context) ([]store.Repo, error) {
	return append([]store.Repo(nil), value.repositories...), nil
}

func (value humaResponseStore) RepoStatuses(context.Context) ([]store.RepoStatus, error) {
	return append([]store.RepoStatus(nil), value.statuses...), nil
}

func TestProfileInspectorConsumesRealHumaObjectAndArrayResponses(t *testing.T) {
	handler := apiresponse.New(apiresponse.Options{
		Version: "test", APIKey: "private-test-token",
		Store: humaResponseStore{
			repositories: []store.Repo{{Name: "example/repository"}},
			statuses: []store.RepoStatus{{
				Repo:              store.Repo{Name: "example/repository"},
				LastIndexJobState: store.JobProjectionExact,
				LastIndexJob:      &store.Job{Status: store.StatusRunning, Attempts: 2},
			}},
		},
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
	status, err := inspector.currentRepositoryStatus(t.Context(), PreparedProfile{
		Address: address, RepositoryName: "example/repository",
	})
	if err != nil || status.LastIndexJobState != store.JobProjectionExact ||
		status.LastIndexJob == nil || status.LastIndexJob.Status != store.StatusRunning ||
		status.LastIndexJob.Attempts != 2 {
		t.Fatalf("real Huma repository status = %+v, %v", status, err)
	}
}

func TestRepositoryIndexProbeUsesClosedLatestJobProjection(t *testing.T) {
	expected := strings.Repeat("a", 40)
	tests := []struct {
		name       string
		status     store.RepoStatus
		wantClass  string
		wantClosed bool
	}{
		{name: "projection unavailable", status: store.RepoStatus{
			Repo: store.Repo{}, LastIndexJobState: store.JobProjectionUnavailable,
		}, wantClass: "pending"},
		{name: "pending", status: projectedIndexStatus(store.StatusPending, 0), wantClass: "pending"},
		{name: "claimed", status: projectedIndexStatus(store.StatusClaimed, 1), wantClass: "pending"},
		{name: "running retry", status: projectedIndexStatus(store.StatusRunning, 2), wantClass: "pending"},
		{name: "done before expected publication", status: projectedIndexStatus(store.StatusDone, 1), wantClass: "pending"},
		{name: "failed", status: projectedIndexStatus(store.StatusFailed, 3), wantClass: "terminal", wantClosed: true},
		{name: "canceled", status: projectedIndexStatus(store.StatusCanceled, 1), wantClass: "terminal", wantClosed: true},
		{name: "exact projection without job", status: store.RepoStatus{
			LastIndexJobState: store.JobProjectionExact,
		}, wantClass: "control"},
		{name: "unknown projection state", status: store.RepoStatus{
			LastIndexJobState: store.JobProjectionState("unknown"),
		}, wantClass: "control"},
		{name: "published commit wins", status: func() store.RepoStatus {
			value := projectedIndexStatus(store.StatusFailed, 3)
			value.IndexedCommitHash = expected
			return value
		}(), wantClass: "complete"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			probe, err := repositoryIndexProbe(test.status, expected)
			if probe.Stage != "repository_index" || !digestIdentity(probe.SHA256) {
				t.Fatalf("probe = %+v", probe)
			}
			if got := classifyConvergenceInspection(err).class; got != test.wantClass {
				t.Fatalf("class = %q, want %q; err=%v", got, test.wantClass, err)
			}
			if errors.Is(err, errRepositoryIndexTerminal) != test.wantClosed {
				t.Fatalf("terminal = %t, want %t; err=%v",
					errors.Is(err, errRepositoryIndexTerminal), test.wantClosed, err)
			}
		})
	}

	first := projectedIndexStatus(store.StatusFailed, 3)
	first.LastIndexJob.Error = "private first failure"
	second := projectedIndexStatus(store.StatusFailed, 3)
	second.LastIndexJob.Error = "different private failure"
	firstProbe, _ := repositoryIndexProbe(first, expected)
	secondProbe, _ := repositoryIndexProbe(second, expected)
	if firstProbe.SHA256 != secondProbe.SHA256 {
		t.Fatal("repository-index probe retained a raw job error")
	}
	first.LastIndexJob.Error = "heartbeat: context deadline exceeded"
	second.LastIndexJob.Error = "heartbeat: private store detail changed"
	firstProbe, _ = repositoryIndexProbe(first, expected)
	secondProbe, _ = repositoryIndexProbe(second, expected)
	if firstProbe.RepositoryIndexFailureClass != "lease_heartbeat" ||
		secondProbe.RepositoryIndexFailureClass != "lease_heartbeat" ||
		firstProbe.SHA256 != secondProbe.SHA256 {
		t.Fatalf("closed heartbeat classes = %+v / %+v", firstProbe, secondProbe)
	}
	second.LastIndexJob.Error = "different private child failure"
	secondProbe, _ = repositoryIndexProbe(second, expected)
	if secondProbe.RepositoryIndexFailureClass != "other" || firstProbe.SHA256 == secondProbe.SHA256 {
		t.Fatalf("closed other class = %+v", secondProbe)
	}
}

func projectedIndexStatus(status store.JobStatus, attempts int) store.RepoStatus {
	return store.RepoStatus{
		LastIndexJobState: store.JobProjectionExact,
		LastIndexJob:      &store.Job{Status: status, Attempts: attempts},
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
		{name: "extraction progress", target: func() any { return &extractionpublication.Progress{} }},
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

func TestExtractionConvergenceProbeClosesTerminalRefusal(t *testing.T) {
	progress := extractionpublication.Progress{
		State: "unavailable",
	}
	refusal := pipelinerefusal.Receipt{
		Schema:         pipelinerefusal.Schema,
		Stage:          pipelinerefusal.StageDomainInventory,
		GenerationKind: pipelinerefusal.GenerationExtractionDomain,
		Classification: pipelinerefusal.ClassificationLimit,
		Dimension:      pipelinerefusal.DimensionCandidateMemberBytes,
		Observed:       792_000_000, Limit: 67_108_864,
	}
	repository := store.RepoStatus{
		LastExtractionJobState: store.JobProjectionExact,
		LastExtractionJob: &store.ExtractionJobProjection{
			Status: store.StatusFailed, Attempts: 1, Refusal: &refusal,
		},
	}
	probe, err := extractionConvergenceProbe(progress, repository)
	if !errors.Is(err, errExtractionBoundRefusal) || probe.ExtractionProgress == nil ||
		probe.ExtractionProgress.RefusalDimension != "candidate_member_bytes" ||
		probe.ExtractionProgress.RefusalObserved != 792_000_000 ||
		probe.ExtractionProgress.RefusalLimit != 67_108_864 ||
		classifyConvergenceInspection(err).class != "terminal" {
		t.Fatalf("probe = %+v, error=%v", probe, err)
	}
	repository.LastExtractionJob.Refusal = nil
	_, err = extractionConvergenceProbe(progress, repository)
	if !errors.Is(err, errExtractionJobTerminal) {
		t.Fatalf("ordinary terminal error = %v", err)
	}
	repository.LastExtractionJob.Refusal = &pipelinerefusal.Receipt{
		Schema: pipelinerefusal.Schema, Stage: pipelinerefusal.StageObservationPublication,
		GenerationKind: pipelinerefusal.GenerationObservation,
		Classification: pipelinerefusal.ClassificationLimit,
		Dimension:      pipelinerefusal.DimensionGenerationRecords, Observed: 2, Limit: 1,
	}
	_, err = extractionConvergenceProbe(progress, repository)
	if !errors.Is(err, errExtractionJobTerminal) {
		t.Fatalf("unrelated limit error = %v", err)
	}
}

func TestExtractionConvergenceProbeRetainsTypedPendingProjection(t *testing.T) {
	progress := extractionpublication.Progress{
		State: "active", Total: 489, Materialized: 489,
		Pending: 460, Running: 4, Succeeded: 25, Domains: 4,
	}
	repository := store.RepoStatus{LastExtractionJobState: store.JobProjectionUnavailable}
	probe, err := extractionConvergenceProbe(progress, repository)
	if err == nil || classifyConvergenceInspection(err).class != "pending" ||
		probe.ExtractionProgress == nil || probe.ExtractionProgress.Succeeded != 25 ||
		probe.ExtractionProgress.Running != 4 || probe.ExtractionProgress.Total != 489 {
		t.Fatalf("probe = %+v, error=%v", probe, err)
	}
}

func TestExtractionConvergenceProbeStopsOnlySettledFailedSchedule(t *testing.T) {
	repository := store.RepoStatus{
		LastExtractionJobState: store.JobProjectionExact,
		LastExtractionJob: &store.ExtractionJobProjection{
			Status: store.StatusDone, Attempts: 1,
		},
	}
	settled := extractionpublication.Progress{
		State: string(store.GenerationScheduleSettled), Total: 32, Materialized: 32,
		Succeeded: 0, Failed: 32, Domains: 4,
	}
	probe, err := extractionConvergenceProbe(settled, repository)
	if !errors.Is(err, errExtractionScheduleTerminal) ||
		classifyConvergenceInspection(err).class != "terminal" ||
		probe.ExtractionProgress == nil || probe.ExtractionProgress.Failed != 32 {
		t.Fatalf("settled probe = %+v, error=%v", probe, err)
	}
	active := settled
	active.State, active.Pending = "active", 1
	active.Failed, active.Total = 31, 32
	if _, err := extractionConvergenceProbe(active, repository); errors.Is(err, errExtractionScheduleTerminal) {
		t.Fatalf("active schedule stopped terminally: %v", err)
	}
	repository.LastExtractionJob.Status = store.StatusFailed
	if _, err := extractionConvergenceProbe(settled, repository); !errors.Is(err, errExtractionJobTerminal) {
		t.Fatalf("job terminal did not retain precedence: %v", err)
	}
	repository.LastExtractionJobState, repository.LastExtractionJob = store.JobProjectionUnavailable, nil
	probe, err = extractionConvergenceProbe(settled, repository)
	if !errors.Is(err, errExtractionScheduleTerminal) || probe.ExtractionProgress.JobState != "" ||
		validateExtractionProgress(*probe.ExtractionProgress) != nil {
		t.Fatalf("collected-job settled probe = %+v, error=%v", probe, err)
	}
}

func TestExtractionConvergenceProbeAcceptsCurrentAuthorityAfterJobCollection(t *testing.T) {
	progress := extractionpublication.Progress{
		State: "current", Total: 489, Materialized: 489,
		Succeeded: 489, Domains: 4, CurrentDomains: 4,
	}
	repository := store.RepoStatus{LastExtractionJobState: store.JobProjectionUnavailable}
	probe, err := extractionConvergenceProbe(progress, repository)
	if err != nil || probe.ExtractionProgress == nil ||
		probe.ExtractionProgress.State != "current" ||
		probe.ExtractionProgress.Succeeded != 489 {
		t.Fatalf("probe = %+v, error=%v", probe, err)
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
