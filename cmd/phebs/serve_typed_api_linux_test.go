//go:build linux

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/codenav"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/typedindex"
)

func typedNavigationHTTP(ctx context.Context, t *testing.T, handler http.Handler, path string, body, out any, status int) *httptest.ResponseRecorder {
	t.Helper()
	method, raw := http.MethodGet, []byte(nil)
	if body != nil {
		method, raw = http.MethodPost, typedNavigationJSON(t, body)
	}
	request := httptest.NewRequestWithContext(ctx, method, path, strings.NewReader(string(raw)))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != status {
		t.Fatalf("%s %s: status %d, want %d: %s", method, path, response.Code, status, response.Body)
	}
	if out != nil {
		if e := json.Unmarshal(response.Body.Bytes(), out); e != nil {
			t.Fatal("response", e, response.Body)
		}
	}
	return response
}

// The HTTP handler, store, production resolver, sealed publication, and kernel
// pins are real. Publication bytes are fixture-authored: no native generation,
// authenticated browser/CSRF session, installed Settings, or target run is proven.
func TestTypedNavigationAPICompositionLinux(t *testing.T) {
	f := typedNavigationFixtureFor(t, typedNavigationServer(t))
	ctx := t.Context()
	check := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	options := api.Options{Store: f.state, TypedIndexAvailable: true, IsAdmin: func(context.Context) bool { return true }}
	handler := api.New(options)
	statusPath := api.TypedIndexPath + "/status?repository=" + url.QueryEscape(f.repo)
	var providers api.TypedIndexProviders
	typedNavigationHTTP(ctx, t, handler, api.TypedIndexPath+"/providers", nil, &providers, http.StatusOK)
	order := typedindex.ProviderOrder()
	if len(providers.Providers) != len(order) {
		t.Fatal("provider order", providers)
	}
	for i, provider := range providers.Providers {
		if provider.ID != order[i] || !provider.Available {
			t.Fatal("prepared provider", provider)
		}
	}
	var view api.TypedIndexView
	typedNavigationHTTP(ctx, t, handler, statusPath, nil, &view, http.StatusOK)
	if !view.Available || view.State != "absent" || view.Revision == "" {
		t.Fatal("initial status", view)
	}
	selection := api.TypedIndexSelection{Repository: f.repo, ExpectedRevision: view.Revision, Provider: view.Provider, Profile: view.Profile, Purpose: typedindex.Publish}
	before, e := f.state.ReadTypedIndexOperator(ctx, f.repo)
	check(e)
	readctx, ledger, e := readaccounting.Start(ctx, readaccounting.Counts{StoreReadAttempts: 100})
	check(e)
	var preview api.TypedIndexPreview
	typedNavigationHTTP(readctx, t, handler, api.TypedIndexPath+"/plan", selection, &preview, http.StatusOK)
	counts, e := ledger.Finish()
	check(e)
	if counts.StoreReadAttempts == 0 || counts.StoreWriteAttempts != 0 || preview.RequestDigest == "" || preview.IdempotencyKey == "" || preview.Commit != view.Commit {
		t.Fatal("pure plan", counts, preview)
	}
	after, e := f.state.ReadTypedIndexOperator(ctx, f.repo)
	check(e)
	if string(typedNavigationJSON(t, before)) != string(typedNavigationJSON(t, after)) {
		t.Fatal("preview changed operator state", before, after)
	}
	jobs, e := f.state.ListJobsPage(ctx, store.JobPageQuery{Kind: store.JobTypedIndex, Limit: 2})
	check(e)
	if len(jobs.Jobs) != 0 || jobs.Next != nil {
		t.Fatal("preview created a job", jobs)
	}
	enqueue := api.TypedIndexEnqueue{TypedIndexSelection: selection, RequestDigest: preview.RequestDigest, IdempotencyKey: preview.IdempotencyKey}
	for _, mutation := range []string{"revision", "digest", "key"} {
		bad := enqueue
		switch mutation {
		case "revision":
			bad.ExpectedRevision = typedNavigationHash("stale")
		case "digest":
			bad.RequestDigest = typedNavigationHash("tampered")
		case "key":
			bad.IdempotencyKey = strings.Repeat("b", 64)
		}
		typedNavigationHTTP(ctx, t, handler, api.TypedIndexPath+"/enqueue", bad, nil, http.StatusConflict)
	}
	for range 2 {
		typedNavigationHTTP(ctx, t, handler, api.TypedIndexPath+"/enqueue", enqueue, &view, http.StatusOK)
		if view.State != "planning" || view.RequestDigest != preview.RequestDigest || view.JobState != store.StatusPending {
			t.Fatal("queued request", view)
		}
	}
	jobs, e = f.state.ListJobsPage(ctx, store.JobPageQuery{Kind: store.JobTypedIndex, Limit: 2})
	check(e)
	if len(jobs.Jobs) != 1 || jobs.Next != nil || jobs.Jobs[0].Status != store.StatusPending || jobs.Jobs[0].Attempts != 0 {
		t.Fatal("retry duplicated or executed job", jobs)
	}
	typedNavigationHTTP(ctx, t, handler, api.TypedIndexPath+"/plan", selection, nil, http.StatusUnprocessableEntity)
	publication := f.publishQueued(t, true)
	if publication.parent.Digest() != preview.RequestDigest {
		t.Fatal("HTTP request was not the published request", publication.parent.Digest(), preview.RequestDigest)
	}
	jobs, e = f.state.ListJobsPage(ctx, store.JobPageQuery{Kind: store.JobTypedIndex, Limit: 2})
	check(e)
	if len(jobs.Jobs) != 1 || jobs.Next != nil || jobs.Jobs[0].Status != store.StatusDone {
		t.Fatal("completed coordinator", jobs)
	}
	completed := jobs.Jobs[0]
	reference, e := typedindex.GeneratedPath(f.unit, "reference.go")
	check(e)
	position := func(path string, line, character string) string {
		return "?" + url.Values{"repo": {f.repo}, "path": {path}, "line": {line}, "character": {character}, "encoding": {"utf16"}}.Encode()
	}
	// Recreate the adapter and service twice: the current publication must reopen
	// from sealed custody. Repeated queries may reuse immutable routing metadata;
	// member/source validation and final authority fencing remain required.
	for range 2 {
		resolver, e := newTypedCodeNavigationResolver(f.state, f.base)
		check(e)
		options.CodeNav = codenav.New(codenav.Options{DataDir: filepath.Join(f.base, "no-git"), RoutedResolver: resolver})
		handler = api.New(options)
		typedNavigationHTTP(ctx, t, handler, statusPath, nil, &view, http.StatusOK)
		if view.State != "current" || view.CurrentCommit != preview.Commit || view.RequestDigest != publication.execution.Digest() {
			t.Fatal("published status", view)
		}
		for range 2 {
			readctx, ledger, e := readaccounting.Start(ctx, readaccounting.Counts{StoreReadAttempts: 100, ControlFileReads: 100, MemberVisits: 1000})
			check(e)
			var definition codenav.DefinitionResult
			typedNavigationHTTP(readctx, t, handler, "/api/find_definitions"+position(reference, "1", "0"), nil, &definition, http.StatusOK)
			counts, e := ledger.Finish()
			check(e)
			if !definition.Available || definition.Location == nil || definition.Location.Path != f.document || definition.Location.Range.Start.Character != 2 || counts.StoreReadAttempts == 0 || counts.StoreWriteAttempts != 0 {
				t.Fatal("cross-member generated definition", definition, counts)
			}
			var references codenav.ReferencesResult
			typedNavigationHTTP(ctx, t, handler, "/api/find_references"+position(f.document, "0", "2"), nil, &references, http.StatusOK)
			if !references.Available || len(references.Locations) != 1 || references.Locations[0].Path != reference || references.Locations[0].Range.Start.Line != 1 {
				t.Fatal("cross-member reference", references)
			}
			var hover codenav.HoverResult
			typedNavigationHTTP(ctx, t, handler, "/api/hover"+position(reference, "1", "0"), nil, &hover, http.StatusOK)
			if !hover.Available || hover.Hover == nil || len(hover.Hover.Documentation) != 1 || hover.Hover.Documentation[0] != "generated" {
				t.Fatal("cross-member hover", hover)
			}
		}
	}
	// The same exact transport retry confirms completed work without re-queueing.
	typedNavigationHTTP(ctx, t, handler, api.TypedIndexPath+"/enqueue", enqueue, &view, http.StatusOK)
	if view.State != "current" || view.JobState != store.StatusDone {
		t.Fatal("completed retry", view)
	}
	jobs, e = f.state.ListJobsPage(ctx, store.JobPageQuery{Kind: store.JobTypedIndex, Limit: 2})
	check(e)
	if len(jobs.Jobs) != 1 || jobs.Next != nil || jobs.Jobs[0].Status != store.StatusDone || jobs.Jobs[0].ID != completed.ID || jobs.Jobs[0].Attempts != completed.Attempts {
		t.Fatal("completed retry requeued or duplicated job", jobs)
	}
	typedNavigationHTTP(ctx, t, handler, api.TypedIndexPath+"/plan", selection, nil, http.StatusUnprocessableEntity)
	// Fail a real coordinator before execution: the API must expose failure
	// without erasing the prior current publication or leaking its private error.
	canary := selection
	canary.Purpose = typedindex.Canary
	var canaryPreview api.TypedIndexPreview
	typedNavigationHTTP(ctx, t, handler, api.TypedIndexPath+"/plan", canary, &canaryPreview, http.StatusOK)
	typedNavigationHTTP(ctx, t, handler, api.TypedIndexPath+"/enqueue", api.TypedIndexEnqueue{TypedIndexSelection: canary, RequestDigest: canaryPreview.RequestDigest, IdempotencyKey: canaryPreview.IdempotencyKey}, &view, http.StatusOK)
	coordinator, e := f.state.ClaimJob(ctx, store.JobTypedIndex, "failed-coordinator-fixture")
	check(e)
	if coordinator == nil {
		t.Fatal("no failure coordinator")
	}
	check(f.state.SetJobStatus(ctx, *coordinator, store.StatusRunning, ""))
	check(f.state.SetJobStatus(ctx, *coordinator, store.StatusFailed, "private /unretained/driver-output"))
	response := typedNavigationHTTP(ctx, t, handler, statusPath, nil, &view, http.StatusOK)
	if view.State != "failed" || view.JobState != store.StatusFailed || view.CurrentCommit != preview.Commit || strings.Contains(response.Body.String(), "private") || strings.Contains(response.Body.String(), "/unretained") {
		t.Fatal("coordinator failure visibility", view, response.Body)
	}
	var retained codenav.DefinitionResult
	typedNavigationHTTP(ctx, t, handler, "/api/find_definitions"+position(reference, "1", "0"), nil, &retained, http.StatusOK)
	if !retained.Available || retained.Location == nil || retained.Location.Path != f.document {
		t.Fatal("failed canary erased current navigation", retained)
	}
	check(f.state.SetRepoIndexed(ctx, f.repo, strings.Repeat("b", 40), time.Now()))
	typedNavigationHTTP(ctx, t, handler, api.TypedIndexPath+"/enqueue", enqueue, nil, http.StatusConflict)
	typedNavigationHTTP(ctx, t, handler, statusPath, nil, &view, http.StatusOK)
	if view.State != "stale" || view.Commit != strings.Repeat("b", 40) || view.Revision == selection.ExpectedRevision {
		t.Fatal("source fence", view)
	}
	var definition codenav.DefinitionResult
	typedNavigationHTTP(ctx, t, handler, "/api/find_definitions"+position(reference, "1", "0"), nil, &definition, http.StatusOK)
	if definition.Available || definition.Location != nil {
		t.Fatal("stale publication escaped", definition)
	}
	check(f.state.ClearTypedIndexForRestore(ctx))
	typedNavigationHTTP(ctx, t, handler, statusPath, nil, &view, http.StatusOK)
	if view.Available || view.State != "stale" || view.RequestDigest != "" {
		t.Fatal("restored publication advertised", view)
	}
	typedNavigationHTTP(ctx, t, handler, "/api/find_definitions"+position(reference, "1", "0"), nil, &definition, http.StatusOK)
	if definition.Available || definition.Location != nil {
		t.Fatal("restored publication escaped", definition)
	}
	typedNavigationHTTP(ctx, t, handler, api.TypedIndexPath+"/plan", selection, nil, http.StatusServiceUnavailable)
	options.TypedIndexAvailable = false
	handler = api.New(options)
	typedNavigationHTTP(ctx, t, handler, api.TypedIndexPath+"/providers", nil, &providers, http.StatusOK)
	for _, provider := range providers.Providers {
		if provider.Available {
			t.Fatal("ordinary provider advertised", provider)
		}
	}
	typedNavigationHTTP(ctx, t, handler, statusPath, nil, &view, http.StatusOK)
	if view.Available || view.State != "absent" {
		t.Fatal("ordinary managed status", view)
	}
}
