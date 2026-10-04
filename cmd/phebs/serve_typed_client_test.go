package main

import (
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/store"
)

var typedSettingsUI = flag.String("typed-settings-ui", "", "absolute existing UI directory for the unchanged Settings HTTP-client composition")

// This composes the installed Settings HTTP client with a real handler/store.
// It generates no native SCIP and mounts no rendered Settings component.
func TestTypedSettingsHTTPClient(t *testing.T) {
	if *typedSettingsUI == "" {
		t.Skip("explicit -typed-settings-ui directory required")
	}
	if !filepath.IsAbs(*typedSettingsUI) || filepath.Clean(*typedSettingsUI) != *typedSettingsUI {
		t.Fatal("typed Settings UI directory must be absolute and canonical")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("explicit Settings client check requires installed Node", err)
	}
	s, err := store.OpenLocalMemory(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	repo := "example.invalid/settings-client"
	if err := s.UpsertRepo(t.Context(), store.Repo{Name: repo}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoIndexed(t.Context(), repo, strings.Repeat("a", 40), time.Now()); err != nil {
		t.Fatal(err)
	}
	profile := typedNavigationTestProfile(t, typedNavigationHash("settings-client-bundle"))
	if _, err := s.InstallTypedProfile(t.Context(), repo, profile, typedNavigationHash("settings-client-universe"), 0); err != nil {
		t.Fatal(err)
	}
	var invalidCSRF atomic.Bool
	server := func(available, admin bool) *httptest.Server {
		handler := api.New(api.Options{Store: s, TypedIndexAvailable: available, IsAdmin: func(context.Context) bool { return admin }})
		out := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && r.Header.Get("X-CSRF-Token") != "neutral-settings-client" {
				invalidCSRF.Store(true)
			}
			handler.ServeHTTP(w, r)
		}))
		t.Cleanup(out.Close)
		return out
	}
	installed, dark, denied := server(true, true), server(false, true), server(true, false)
	probe := filepath.Join("..", "..", "spike", "t459", "settings_client.cjs")
	run := func(phase string, preview []byte) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, node, probe, *typedSettingsUI, installed.URL, repo, phase, dark.URL, denied.URL)
		cmd.Env = append(os.Environ(), "TMPDIR="+t.TempDir())
		cmd.Stdin = strings.NewReader(string(preview))
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("unchanged Settings client %s: %v: %s", phase, err, out)
		}
		return out
	}
	preview := run("plan", nil)
	var decoded api.TypedIndexPreview
	if err := json.Unmarshal(preview, &decoded); err != nil || decoded.Selection.Repository != repo {
		t.Fatalf("client preview: %v: %s", err, preview)
	}
	status, err := s.GetTypedIndexStatus(t.Context(), repo)
	if err != nil || status.Desired != "" {
		t.Fatal("pure preview changed desired authority", status, err)
	}
	jobs, err := s.ListJobsPage(t.Context(), store.JobPageQuery{Kind: store.JobTypedIndex, Limit: 2})
	if err != nil || len(jobs.Jobs) != 0 || jobs.Next != nil {
		t.Fatal("pure preview created a coordinator", jobs, err)
	}
	run("enqueue", preview)
	jobs, err = s.ListJobsPage(t.Context(), store.JobPageQuery{Kind: store.JobTypedIndex, Limit: 2})
	if err != nil || len(jobs.Jobs) != 1 || jobs.Next != nil || jobs.Jobs[0].Status != store.StatusPending {
		t.Fatal("exact retry allocated another coordinator", jobs, err)
	}
	if err := s.SetRepoIndexed(t.Context(), repo, strings.Repeat("b", 40), time.Now()); err != nil {
		t.Fatal(err)
	}
	run("stale", preview)
	if err := s.ClearTypedIndexForRestore(t.Context()); err != nil {
		t.Fatal(err)
	}
	run("restore", preview)
	if invalidCSRF.Load() {
		t.Fatal("Settings client omitted CSRF header")
	}
	t.Log("unchanged Settings HTTP client + real API/store passed; rendered Settings, native generation and production registration are unestablished")
}
