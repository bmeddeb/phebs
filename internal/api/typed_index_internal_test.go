package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/typedindex"
)

type typedAPIStore struct {
	store.Store
	snapshot                 store.TypedIndexOperator
	reads, lookups, enqueues int
	fault                    error
}

func TestTypedIndexAPIInstalledProvidersAndSourceFence(t *testing.T) {
	s, opts := typedAPIFixture(t)
	opts.TypedIndexProviderAvailable = func(id string) bool { return id == typedindex.ModuleProviderID }
	opts.TypedIndexAdmitted = func(store.TypedIndexOperator) bool { return false }
	handler := New(opts)
	providers := typedAPIRequest(t, handler, "/providers", nil)
	var result TypedIndexProviders
	if providers.Code != http.StatusOK || json.Unmarshal(providers.Body.Bytes(), &result) != nil || len(result.Providers) != 3 {
		t.Fatal(providers.Code, providers.Body.String())
	}
	for _, provider := range result.Providers {
		if provider.Available != (provider.ID == typedindex.ModuleProviderID) {
			t.Fatal("uninstalled provider advertised", provider)
		}
	}
	status := typedAPIRequest(t, handler, "/status?repository="+s.snapshot.Source.Repository, nil)
	var view TypedIndexView
	if status.Code != http.StatusOK || json.Unmarshal(status.Body.Bytes(), &view) != nil || view.Available {
		t.Fatal("uninstalled source advertised", status.Body.String())
	}
	selection := TypedIndexSelection{Repository: s.snapshot.Source.Repository, Provider: s.snapshot.Profile.Provider(), Profile: s.snapshot.Profile.Definition().Name, Purpose: typedindex.Publish}
	plan := typedAPIRequest(t, handler, "/plan", selection)
	if plan.Code != http.StatusServiceUnavailable || s.enqueues != 0 {
		t.Fatal("uninstalled source planned", plan.Code, plan.Body.String())
	}
}

func (s *typedAPIStore) GetRepo(_ context.Context, name string) (*store.Repo, error) {
	s.lookups++
	if name != s.snapshot.Source.Repository {
		return nil, store.ErrNotFound
	}
	return &store.Repo{Name: name, IndexedCommitHash: s.snapshot.Source.Commit}, nil
}
func (s *typedAPIStore) ReadTypedIndexOperator(context.Context, string) (store.TypedIndexOperator, error) {
	s.reads++
	return s.snapshot, s.fault
}
func (s *typedAPIStore) EnqueueTypedIndexExpected(_ context.Context, _ string, revision string, raw []byte) (store.TypedIndexStatus, error) {
	if revision != s.snapshot.Revision {
		return store.TypedIndexStatus{}, typedindex.Stale
	}
	var request typedindex.Request
	if json.Unmarshal(raw, &request) != nil || request.ValidatePurpose() != nil {
		return store.TypedIndexStatus{}, typedindex.Invalid
	}
	s.enqueues++
	return store.TypedIndexStatus{}, nil
}
func typedAPIFixture(t *testing.T) (*typedAPIStore, Options) {
	t.Helper()
	h := "sha256:" + strings.Repeat("a", 64)
	tool := typedindex.Tool{Version: "0.2.7", Digest: h}
	raw, _ := json.Marshal(typedindex.ProfileDefinition{Schema: typedindex.ProfileSchema, Provider: typedindex.ProviderID, Name: "reduced", Tools: typedindex.Tools{Bazel: tool, RulesGo: tool, Go: tool, Driver: tool, Indexer: tool, Planner: tool, Launcher: tool}, Config: typedindex.ReducedConfig(), Policy: typedindex.MeasuredPolicy(), ImageDigest: h, BundleDigest: h})
	profile, err := typedindex.DecodeProfile(t.Context(), raw)
	if err != nil {
		t.Fatal(err)
	}
	s := &typedAPIStore{snapshot: store.TypedIndexOperator{Source: typedindex.Source{Repository: "example.test/repo", Incarnation: "one", Generation: h, Commit: strings.Repeat("a", 40)}, Profile: profile, ProfileEpoch: 1, UniverseDigest: h, Revision: h}}
	return s, Options{Store: s, IsAdmin: func(context.Context) bool { return true }, TypedIndexAvailable: true}
}
func typedAPIRequest(t *testing.T, handler http.Handler, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	method := http.MethodGet
	var raw []byte
	if body != nil {
		method = http.MethodPost
		raw, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, TypedIndexPath+path, strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	out := httptest.NewRecorder()
	handler.ServeHTTP(out, req)
	return out
}
func TestTypedIndexAPIAuthorizationAndDarkness(t *testing.T) {
	for _, mode := range []string{"nil-admin", "ordinary", "hidden", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			s, opts := typedAPIFixture(t)
			switch mode {
			case "nil-admin":
				opts.IsAdmin = nil
			case "ordinary":
				opts.IsAdmin = func(context.Context) bool { return false }
			case "hidden":
				opts.Visible = func(context.Context) func(store.Repo) bool { return func(store.Repo) bool { return false } }
			case "disabled":
				opts.TypedIndexAvailable = false
			}
			handler := New(opts)
			status := typedAPIRequest(t, handler, "/status?repository=example.test/repo", nil)
			want := 403
			if mode == "hidden" {
				want = 404
			}
			if mode == "disabled" {
				want = 200
			}
			if status.Code != want || s.reads != 0 || s.enqueues != 0 {
				t.Fatal(status.Code, status.Body.String(), s.reads)
			}
			if mode == "disabled" && !strings.Contains(status.Body.String(), `"available":false`) {
				t.Fatal(status.Body.String())
			}
			if mode == "ordinary" || mode == "nil-admin" {
				if s.lookups != 0 {
					t.Fatal("denied lookup")
				}
			}
		})
	}
}
func TestTypedIndexAPIPreviewEnqueueAndFences(t *testing.T) {
	for _, purpose := range []typedindex.Purpose{typedindex.Publish, typedindex.Canary, typedindex.DryRun} {
		t.Run(string(purpose), func(t *testing.T) {
			s, opts := typedAPIFixture(t)
			var events []store.AuditEvent
			opts.AuditRecord = func(_ context.Context, event store.AuditEvent) { events = append(events, event) }
			handler := New(opts)
			selection := TypedIndexSelection{Repository: s.snapshot.Source.Repository, ExpectedRevision: s.snapshot.Revision, Provider: s.snapshot.Profile.Provider(), Profile: s.snapshot.Profile.Definition().Name, Purpose: purpose}
			planned := typedAPIRequest(t, handler, "/plan", selection)
			if planned.Code != 200 || s.enqueues != 0 {
				t.Fatal(planned.Code, planned.Body.String())
			}
			var preview TypedIndexPreview
			if json.Unmarshal(planned.Body.Bytes(), &preview) != nil || preview.Commit != s.snapshot.Source.Commit || preview.RequestDigest == "" || preview.IdempotencyKey == "" {
				t.Fatal("preview")
			}
			enqueue := TypedIndexEnqueue{TypedIndexSelection: selection, RequestDigest: preview.RequestDigest, IdempotencyKey: preview.IdempotencyKey}
			for _, mutation := range []string{"revision", "digest", "key", "provider", "profile"} {
				bad := enqueue
				switch mutation {
				case "revision":
					bad.ExpectedRevision = "sha256:" + strings.Repeat("b", 64)
				case "digest":
					bad.RequestDigest = "sha256:" + strings.Repeat("b", 64)
				case "key":
					bad.IdempotencyKey = strings.Repeat("b", 64)
				case "provider":
					bad.Provider = typedindex.ImportProviderID
				case "profile":
					bad.Profile = "other"
				}
				if response := typedAPIRequest(t, handler, "/enqueue", bad); response.Code != 409 || s.enqueues != 0 {
					t.Fatal(mutation, response.Code, response.Body.String())
				}
			}
			for range 2 {
				if response := typedAPIRequest(t, handler, "/enqueue", enqueue); response.Code != 200 {
					t.Fatal(response.Code, response.Body.String())
				}
			}
			if s.enqueues != 2 || events[len(events)-1].Target != selection.Repository || events[len(events)-1].Action != "enqueue-code-navigation-indexing" {
				t.Fatal("missing exact mutation/audit")
			}
		})
	}
}
func TestTypedIndexAPIClosedStatesAndErrors(t *testing.T) {
	s, opts := typedAPIFixture(t)
	for _, tc := range []struct {
		status store.TypedIndexStatus
		job    store.JobStatus
		state  string
	}{
		{store.TypedIndexStatus{}, "", "absent"},
		{store.TypedIndexStatus{Desired: "sha256:" + strings.Repeat("a", 64)}, "", "absent"},
		{store.TypedIndexStatus{Desired: "sha256:" + strings.Repeat("a", 64), Current: &typedindex.PublicationPointer{}}, store.StatusPending, "planning"},
		{store.TypedIndexStatus{Stale: true, Stage: store.TypedExecution}, store.StatusRunning, "stale"},
		{store.TypedIndexStatus{RestoreRequired: true, Stage: store.TypedPlanning}, store.StatusRunning, "stale"},
		{store.TypedIndexStatus{Stage: store.TypedPlanning}, "", "planning"},
		{store.TypedIndexStatus{Stage: store.TypedExecution}, "", "indexing"},
		{store.TypedIndexStatus{Stage: store.TypedValidation}, "", "validating"},
		{store.TypedIndexStatus{Stage: store.TypedPublication}, "", "publishing"},
		{store.TypedIndexStatus{Stale: true}, "", "stale"},
		{store.TypedIndexStatus{}, store.StatusFailed, "failed"},
		{store.TypedIndexStatus{Canceled: true}, "", "canceled"},
	} {
		s.snapshot.Status, s.snapshot.Coordinator = tc.status, tc.job
		view, _, err := typedIndexRead(t.Context(), opts, s.snapshot.Source.Repository)
		if err != nil || view.State != tc.state {
			t.Fatal(tc.state, view.State, err)
		}
	}
	s.fault = errors.New("private /operator/source secret worker output")
	response := typedAPIRequest(t, New(opts), "/status?repository=example.test/repo", nil)
	if response.Code != 503 || strings.Contains(response.Body.String(), "private") || strings.Contains(response.Body.String(), "secret") {
		t.Fatal(response.Body.String())
	}
}

func typedAPIDigest(t *testing.T, s *typedAPIStore, r typedindex.Request) string {
	t.Helper()
	raw, _ := json.Marshal(r)
	a, err := typedindex.Admit(t.Context(), typedindex.Authority{Enabled: true, Administrator: true, Source: s.snapshot.Source, Profile: typedindex.Epoch{Number: s.snapshot.ProfileEpoch, Digest: s.snapshot.Profile.Digest()}, UniverseDigest: s.snapshot.UniverseDigest}, s.snapshot.Profile, raw)
	if err != nil {
		t.Fatal(err)
	}
	return a.Digest()
}
func TestTypedIndexAPIReruns(t *testing.T) {
	s, opts := typedAPIFixture(t)
	handler := New(opts)
	src := s.snapshot
	desired := "sha256:" + strings.Repeat("c", 64)
	canary := typedindex.NewManagedRequest(src.Source, src.Profile, src.ProfileEpoch, src.UniverseDigest, typedindex.Canary)
	dryRun := typedindex.NewManagedRequest(src.Source, src.Profile, src.ProfileEpoch, src.UniverseDigest, typedindex.DryRun)
	selection := TypedIndexSelection{Repository: src.Source.Repository, ExpectedRevision: src.Revision, Provider: src.Profile.Provider(), Profile: src.Profile.Definition().Name, Purpose: typedindex.Canary}
	for _, tc := range []struct {
		name    string
		desired typedindex.Request
		status  store.TypedIndexStatus
		want    uint64
		accept  []uint64
	}{
		{"first", typedindex.Request{}, store.TypedIndexStatus{}, 0, []uint64{0}},
		{"failed retry", canary.ManagedRun(2), store.TypedIndexStatus{Desired: desired, Reason: typedindex.ExecutionFailed}, 3, []uint64{3, 2}},
		{"switch back", dryRun.ManagedRun(4), store.TypedIndexStatus{Desired: desired}, 5, []uint64{5}},
		{"in flight", canary.ManagedRun(1), store.TypedIndexStatus{Desired: desired, Stage: store.TypedExecution}, 1, []uint64{1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s.snapshot.Desired, s.snapshot.DesiredFresh = tc.desired, tc.desired.Schema != ""
			s.snapshot.Status, s.snapshot.Coordinator = tc.status, store.StatusDone
			planned := typedAPIRequest(t, handler, "/plan", selection)
			var preview TypedIndexPreview
			if planned.Code != 200 || json.Unmarshal(planned.Body.Bytes(), &preview) != nil || preview.IdempotencyKey != canary.ManagedRun(tc.want).IdempotencyKey {
				t.Fatal(planned.Code, planned.Body.String())
			}
			for run := range uint64(7) {
				r := canary.ManagedRun(run)
				before := s.enqueues
				response := typedAPIRequest(t, handler, "/enqueue", TypedIndexEnqueue{TypedIndexSelection: selection, RequestDigest: typedAPIDigest(t, s, r), IdempotencyKey: r.IdempotencyKey})
				if accepted := slices.Contains(tc.accept, run); accepted != (response.Code == 200) || accepted != (s.enqueues == before+1) {
					t.Fatal("run", run, response.Code, response.Body.String())
				}
			}
		})
	}
}
func TestTypedIndexAPIScheduleGapAndEarlyFailure(t *testing.T) {
	s, opts := typedAPIFixture(t)
	s.snapshot.Coordinator = store.StatusDone
	s.snapshot.DesiredFresh = true
	s.snapshot.Status.Stale = true // Prior publication belongs to the previous HEAD.
	s.snapshot.Status.Desired = "sha256:" + strings.Repeat("a", 64)
	for _, current := range []*typedindex.PublicationPointer{nil, {}} {
		s.snapshot.Status.Current = current
		s.snapshot.Schedule = &store.GenerationSchedule{Status: store.GenerationScheduleActive, TotalItems: 1}
		view, _, err := typedIndexRead(t.Context(), opts, s.snapshot.Source.Repository)
		if err != nil || view.State != "planning" {
			t.Fatal(view, err)
		}
		s.snapshot.Schedule = &store.GenerationSchedule{Status: store.GenerationScheduleSettled, Failed: 1}
		view, _, err = typedIndexRead(t.Context(), opts, s.snapshot.Source.Repository)
		if err != nil || view.State != "failed" {
			t.Fatal(view, err)
		}
		s.snapshot.DesiredFresh = false
		view, _, err = typedIndexRead(t.Context(), opts, s.snapshot.Source.Repository)
		if err != nil || view.State != "stale" {
			t.Fatal("stale desired hidden", view, err)
		}
		s.snapshot.DesiredFresh = true
	}
}

func TestTypedIndexAPIResourceProfileMatchesInstalledArchitecture(t *testing.T) {
	for _, arch := range []string{"arm64", "amd64"} {
		t.Run(arch, func(t *testing.T) {
			s, opts := typedAPIFixture(t)
			if arch == "amd64" {
				h := s.snapshot.UniverseDigest
				tool := typedindex.Tool{Version: "0.2.7", Digest: h}
				raw, err := json.Marshal(typedindex.ProfileDefinition{Schema: typedindex.Amd64ProfileSchema, Provider: typedindex.ProviderID, Name: "reduced", Tools: typedindex.Tools{Bazel: tool, RulesGo: tool, Go: tool, Driver: tool, Indexer: tool, Planner: tool, Launcher: tool}, Config: typedindex.Amd64ReducedConfig(), Policy: typedindex.MeasuredPolicy(), ImageDigest: h, BundleDigest: h})
				if err != nil {
					t.Fatal(err)
				}
				profile, err := typedindex.DecodeProfile(t.Context(), raw)
				if err != nil {
					t.Fatal(err)
				}
				s.snapshot.Profile = profile
			}
			handler := New(opts)
			status := typedAPIRequest(t, handler, "/status?repository="+s.snapshot.Source.Repository, nil)
			var view TypedIndexView
			if status.Code != http.StatusOK || json.Unmarshal(status.Body.Bytes(), &view) != nil || !view.Available || view.ResourceProfile != "native-"+arch+"-bounded-v1" {
				t.Fatal(status.Code, status.Body.String())
			}
			selection := TypedIndexSelection{Repository: s.snapshot.Source.Repository, ExpectedRevision: s.snapshot.Revision, Provider: s.snapshot.Profile.Provider(), Profile: s.snapshot.Profile.Definition().Name, Purpose: typedindex.Publish}
			planned := typedAPIRequest(t, handler, "/plan", selection)
			var preview TypedIndexPreview
			if planned.Code != http.StatusOK || json.Unmarshal(planned.Body.Bytes(), &preview) != nil || preview.ResourceProfile != view.ResourceProfile {
				t.Fatal(planned.Code, planned.Body.String())
			}
		})
	}
}
