//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/store"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
	"github.com/bmeddeb/phebs/internal/typedexecutor"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/bmeddeb/phebs/internal/typedsandbox"
	"github.com/bmeddeb/phebs/internal/typedworkspace"
)

var (
	typedSettingsNativeRoot   = flag.String("typed-settings-native-root", "", "exact fresh neutral installation for the opt-in native Settings bridge")
	typedSettingsNativeConfig = flag.String("typed-settings-native-config-sha256", "", "explicit SHA256 of that installation's private config.json")
)

// This digest-bound subset consumes the closed producer contract. The producer
// validates the full canonical configuration; no browser input installs tools.
type typedSettingsNativeConfigSubset struct {
	Schema          string            `json:"schema"`
	ID              string            `json:"id"`
	SourceCommit    string            `json:"source_commit"`
	Source          typedindex.Source `json:"source"`
	ProfileEpoch    int64             `json:"profile_epoch"`
	UniverseSHA256  string            `json:"universe_sha256"`
	ProfileSHA256   string            `json:"profile_sha256"`
	InventorySHA256 string            `json:"inventory_sha256"`
	SeedSHA256      string            `json:"seed_sha256"`
	ImageSHA256     string            `json:"image_sha256"`
	Policy          typedindex.Policy `json:"policy"`
}

// The public report's fixed-size successful phase/resource projection keeps a
// single frame below the protocol cap; no arbitrary watchdog/output is sent.
type typedSettingsNativePhase struct {
	Phase            typedindex.Action      `json:"phase"`
	ExitCode         int                    `json:"exit_code"`
	Removed          bool                   `json:"removed"`
	StopReason       string                 `json:"stop_reason"`
	Resources        typedsandbox.Resources `json:"resources"`
	FailureOperation string                 `json:"failure_operation,omitempty"`
	CleanupOperation string                 `json:"cleanup_operation,omitempty"`
}

var typedSettingsInstallationPattern = regexp.MustCompile(`^/var/lib/phebs-typed-acceptance/[a-z][a-z0-9-]{0,31}$`)
var typedSettingsDigestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

func typedSettingsNativeFlags(root, digest, ui string) error {
	if !typedSettingsInstallationPattern.MatchString(root) || !typedSettingsDigestPattern.MatchString(digest) || !filepath.IsAbs(ui) || filepath.Clean(ui) != ui {
		return errors.New("explicit closed installation, config digest and absolute UI directory required")
	}
	return nil
}

func TestTypedSettingsNativeFlagRefusals(t *testing.T) {
	for _, tc := range []struct {
		name, root, digest, ui string
		valid                  bool
	}{
		{"closed", "/var/lib/phebs-typed-acceptance/t459-settings-native", "sha256:" + strings.Repeat("a", 64), "/ui/dist", true},
		{"missing root", "", "sha256:" + strings.Repeat("a", 64), "/ui/dist", false},
		{"missing digest", "/var/lib/phebs-typed-acceptance/t459-settings-native", "", "/ui/dist", false},
		{"traversal", "/var/lib/phebs-typed-acceptance/../native", "sha256:" + strings.Repeat("a", 64), "/ui/dist", false},
		{"alien root", "/tmp/t459-native", "sha256:" + strings.Repeat("a", 64), "/ui/dist", false},
		{"missing UI", "/var/lib/phebs-typed-acceptance/t459-settings-native", "sha256:" + strings.Repeat("a", 64), "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if (typedSettingsNativeFlags(tc.root, tc.digest, tc.ui) == nil) != tc.valid {
				t.Fatal("flag gate differs")
			}
		})
	}
}

// Reads are capped before allocation. Private controls must be single-link,
// root-owned regular files; final-component symlinks are never followed.
func typedSettingsNativeRead(ctx context.Context, t *testing.T, path, expected string, limit int64) []byte {
	t.Helper()
	if err := ctx.Err(); err != nil {
		t.Fatal(err)
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal("private installation control open", err)
	}
	file := os.NewFile(uintptr(fd), path)
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		t.Fatal("private installation control stat", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || stat.Uid != 0 || stat.Nlink != 1 || info.Size() < 1 || info.Size() > limit {
		t.Fatal("private installation control identity or size")
	}
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(raw)) != info.Size() || typedNavigationBytes(raw) != expected {
		t.Fatal("private installation control digest or bounded read")
	}
	return raw
}

func typedSettingsNativeInstallation(ctx context.Context, t *testing.T) (typedSettingsNativeConfigSubset, typedindex.Profile, typedindex.Inventory, []byte, []byte) {
	t.Helper()
	root := *typedSettingsNativeRoot
	resolved, err := filepath.EvalSymlinks(root)
	info, statErr := os.Lstat(root)
	if err != nil || statErr != nil || resolved != root || !info.IsDir() || info.Mode().Perm() != 0700 {
		t.Fatal("installation root must be a resolved private directory")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 {
		t.Fatal("installation root is not owned by root")
	}
	raw := typedSettingsNativeRead(ctx, t, filepath.Join(root, "config.json"), *typedSettingsNativeConfig, 16<<10)
	var cfg typedSettingsNativeConfigSubset
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if decoder.Decode(&cfg) != nil || decoder.Decode(new(any)) != io.EOF || cfg.Schema != "phebs-typed-workspace-fault-acceptance-v1" || cfg.ID != filepath.Base(root) || cfg.Source.Validate() != nil || cfg.Source.Repository != "example.invalid/phebs-native-neutral" || cfg.ProfileEpoch != 1 || cfg.Policy != typedindex.MeasuredPolicy() || len(cfg.SourceCommit) != 40 {
		t.Fatal("installation differs from the closed neutral workspace contract")
	}
	for _, digest := range []string{cfg.UniverseSHA256, cfg.ProfileSHA256, cfg.InventorySHA256, cfg.SeedSHA256, cfg.ImageSHA256} {
		if !typedSettingsDigestPattern.MatchString(digest) {
			t.Fatal("installation digest shape")
		}
	}
	profileRaw := typedSettingsNativeRead(ctx, t, filepath.Join(root, "profile.json"), cfg.ProfileSHA256, typedindex.MaxProfileBytes)
	profile, err := typedindex.DecodeProfile(ctx, profileRaw)
	if err != nil || profile.Provider() != typedindex.ModuleProviderID || profile.Definition().Schema != typedindex.InputProfileSchema || profile.Definition().Policy != cfg.Policy || profile.Definition().BundleDigest != cfg.InventorySHA256 || profile.Definition().ImageDigest != cfg.ImageSHA256 {
		t.Fatal("installed profile relation", err)
	}
	inventoryRaw := typedSettingsNativeRead(ctx, t, filepath.Join(root, "inventory.json"), cfg.InventorySHA256, typedindex.MaxInventoryBytes)
	inventory, err := typedindex.DecodeInventory(ctx, inventoryRaw, cfg.InventorySHA256)
	if err != nil {
		t.Fatal("installed inventory", err)
	}
	mirror, err := phebssync.SafeRepoDir(root, cfg.Source.Repository)
	if err != nil {
		t.Fatal("retained source mirror", err)
	}
	head, err := gitobj.Output(ctx, mirror, 41, "rev-parse", "HEAD")
	if err != nil || strings.TrimSpace(string(head)) != cfg.Source.Commit {
		t.Fatal("retained mirror HEAD differs", err)
	}
	if err = gitobj.RejectAlternates(mirror); err != nil {
		t.Fatal("retained mirror uses alternates", err)
	}
	var total int64
	sources := map[string]bool{"source/go.work": true, "source/a/go.mod": true, "source/b/go.mod": true, "source/a/a.go": true, "source/b/b.go": true}
	for _, entry := range inventory.Files() {
		if !strings.HasPrefix(entry.Path, "source/") {
			continue
		}
		if !sources[entry.Path] || entry.Executable || entry.Bytes < 1 || entry.Bytes > 176 {
			t.Fatal("neutral source inventory differs")
		}
		delete(sources, entry.Path)
		blob, err := gitobj.Output(ctx, mirror, entry.Bytes, "show", cfg.Source.Commit+":"+strings.TrimPrefix(entry.Path, "source/"))
		if err != nil || int64(len(blob)) != entry.Bytes || typedNavigationBytes(blob) != entry.Digest {
			t.Fatal("retained source blob differs", err)
		}
		total += entry.Bytes
	}
	if len(sources) != 0 || total != 176 {
		t.Fatal("neutral five-file 176-byte source differs")
	}
	seed := typedSettingsNativeRead(ctx, t, filepath.Join(root, "seed.surql"), cfg.SeedSHA256, 4<<20)
	return cfg, profile, inventory, inventoryRaw, seed
}

func typedSettingsNativeImport(ctx context.Context, t *testing.T, endpoint string, seed []byte) {
	t.Helper()
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, control := range []struct {
		path    string
		raw     []byte
		limit   int64
		timeout time.Duration
	}{
		{"/sql", []byte("DEFINE NAMESPACE IF NOT EXISTS t454; DEFINE DATABASE IF NOT EXISTS neutral;"), 16 << 10, 5 * time.Second},
		{"/import", seed, 1 << 20, 30 * time.Second},
	} {
		requestCtx, cancel := context.WithTimeout(ctx, control.timeout)
		request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, "http://"+strings.TrimPrefix(endpoint, "ws://")+control.path, bytes.NewReader(control.raw))
		if err != nil {
			cancel()
			t.Fatal("seed import request", err)
		}
		request.SetBasicAuth("root", "fixture")
		request.Header.Set("Surreal-NS", "t454")
		request.Header.Set("Surreal-DB", "neutral")
		request.Header.Set("Accept", "application/json")
		response, err := client.Do(request)
		if err != nil {
			cancel()
			t.Fatal("seed import transport", err)
		}
		raw, readErr := io.ReadAll(io.LimitReader(response.Body, control.limit+1))
		closeErr := response.Body.Close()
		cancel()
		var rows []struct {
			Status string `json:"status"`
		}
		if readErr != nil || closeErr != nil || response.StatusCode != http.StatusOK || int64(len(raw)) > control.limit || json.Unmarshal(raw, &rows) != nil || rows == nil || control.path == "/sql" && len(rows) != 2 {
			t.Fatal("seed import response refused")
		}
		for _, row := range rows {
			if row.Status != "OK" {
				t.Fatal("seed import statement refused")
			}
		}
	}
}

func typedSettingsNativePristine(ctx context.Context, t *testing.T, state *store.Surreal, cfg typedSettingsNativeConfigSubset, profile typedindex.Profile) store.TypedIndexOperator {
	t.Helper()
	op, err := state.ReadTypedIndexOperator(ctx, cfg.Source.Repository)
	if err != nil || op.Source != cfg.Source || op.Profile.Digest() != profile.Digest() || op.ProfileEpoch != uint64(cfg.ProfileEpoch) || op.UniverseDigest != cfg.UniverseSHA256 || op.Status.Desired != "" || op.Status.Current != nil || op.Status.Canceled || op.Status.RestoreRequired || op.Schedule != nil {
		t.Fatal("imported source/profile/intent is not pristine", err)
	}
	typedSettingsJobs(ctx, t, state, 0)
	for _, kind := range []store.TypedIndexControlKind{store.TypedIndexRequests, store.TypedIndexAttempts, store.TypedIndexPlans, store.TypedIndexIntents, store.TypedIndexStates, store.TypedIndexCurrents} {
		page, err := state.ScanTypedIndexControls(ctx, kind, "", 2)
		want := 0
		if kind == store.TypedIndexIntents {
			want = 1
		}
		if err != nil || page.Next != "" || len(page.Rows) != want {
			t.Fatal("imported typed controls are not pristine", err)
		}
	}
	if _, err := state.GetTypedIndexGrowth(ctx); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("imported growth authority is not absent", err)
	}
	return op
}

// The unchanged browser admits one exact Publish. Production composition then
// owns Coordinator, Scheduler, native execution, settlement and routed readers.
// Reports are observation only; prior native receipts prove the private shared
// allowance clock, which this package cannot observe and does not recreate.
func TestTypedSettingsNativeLinux(t *testing.T) {
	if *typedSettingsNativeRoot == "" && *typedSettingsNativeConfig == "" {
		t.Skip("explicit native installation and config SHA256 required")
	}
	if err := typedSettingsNativeFlags(*typedSettingsNativeRoot, *typedSettingsNativeConfig, *typedSettingsBrowserUI); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() != 0 || !typedindex.AdmittedNativeWorker() {
		t.Fatal("native bridge requires the admitted root Linux worker")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Minute)
	defer cancel()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	cfg, profile, inventory, inventoryRaw, seed := typedSettingsNativeInstallation(ctx, t)
	dist := *typedSettingsBrowserUI
	resolved, err := filepath.EvalSymlinks(dist)
	check(err)
	index, err := os.Lstat(filepath.Join(dist, "index.html"))
	if err != nil || resolved != dist || !index.Mode().IsRegular() || index.Size() < 1 || index.Size() > 2<<20 {
		t.Fatal("built UI directory differs")
	}
	endpoint := typedNavigationServer(t)
	typedSettingsNativeImport(ctx, t, endpoint, seed)
	state, err := store.Open(ctx, endpoint, "root", "fixture", "t454", "neutral")
	check(err)
	t.Cleanup(func() {
		closeCtx, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		if err := state.Close(closeCtx); err != nil {
			t.Error("native fixture store failed to close")
		}
	})
	before := typedSettingsNativePristine(ctx, t, state, cfg, profile)
	// The operational driver owns this directory. Failed or unjoined native
	// custody is retained; testing.TempDir must not recursively remove it.
	temporary := filepath.Join(*typedSettingsNativeRoot, "tmp")
	resolved, err = filepath.EvalSymlinks(temporary)
	check(err)
	info, err := os.Lstat(temporary)
	check(err)
	stat, ok := info.Sys().(*syscall.Stat_t)
	if resolved != temporary || !info.IsDir() || info.Mode().Perm() != 0700 || !ok || stat.Uid != 0 {
		t.Fatal("native fixture temporary root is not private root custody")
	}
	owned, err := os.MkdirTemp(temporary, "settings-native-")
	check(err)
	workspace, indexDir := filepath.Join(owned, "typed-index"), filepath.Join(*typedSettingsNativeRoot, "index")
	check(os.Mkdir(workspace, 0700))
	check(os.Mkdir(indexDir, 0700))
	check(os.WriteFile(filepath.Join(workspace, ".phebs-index-publication.lock"), nil, 0600))
	runtimeCtx, stopRuntime := context.WithCancel(ctx)
	var workers sync.WaitGroup
	var stopOnce sync.Once
	var joinFailed atomic.Bool
	joinRuntime := func() {
		stopOnce.Do(func() {
			stopRuntime()
			done := make(chan struct{})
			go func() { workers.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(30 * time.Second):
				joinFailed.Store(true)
				t.Error("native runtime failed to join")
			}
		})
	}
	t.Cleanup(joinRuntime)
	var lookups, reports atomic.Int64
	var reportFailed atomic.Bool
	var planning string
	bundle := func(ctx context.Context, admitted typedindex.Admission) (string, []byte, error) {
		request := admitted.Request()
		if ctx.Err() != nil || lookups.Add(1) != 1 || admitted.Digest() != planning || request.Source != cfg.Source || request.ProfileEpoch != uint64(cfg.ProfileEpoch) || request.ProfileDigest != profile.Digest() || request.BundleDigest != cfg.InventorySHA256 || request.UniverseDigest != cfg.UniverseSHA256 || request.Purpose != typedindex.Publish || request.Action != typedindex.Plan {
			return "", nil, typedexecutor.ErrHeld
		}
		return filepath.Join(*typedSettingsNativeRoot, "bundle"), bytes.Clone(inventoryRaw), nil
	}
	deps := &serveDeps{ctx: runtimeCtx, cancel: stopRuntime, cfg: &config.Config{Server: config.Server{DataDir: *typedSettingsNativeRoot}}, st: state, startup: &serveOwners{}, exact: &serveExact{}, typedInstallation: &typedServeInstallation{Workspace: workspace, Socket: "/run/docker.sock", Image: cfg.ImageSHA256, Bundle: bundle}}
	deps.runBackground = func(run func()) { workers.Add(1); go func() { defer workers.Done(); run() }() }
	deps.stopBackground = joinRuntime
	check(wireServeLifecycle(deps))
	outcomes := make(chan typedSettingsNativeReport, 1)
	deps.typedRuntime.runtime.Report = func(outcome typedexecutor.Outcome, err error) {
		if reports.Add(1) != 1 {
			reportFailed.Store(true)
		}
		select {
		case outcomes <- typedSettingsNativeReport{outcome, err}:
		default:
			reportFailed.Store(true)
		}
	}
	authCtx, stopAuth := context.WithCancel(ctx)
	authentication := typedSettingsBrowserAuth(authCtx, t, state, stopAuth)
	deps.authService, deps.auditRecord = authentication.service, authentication.audit
	var closeSearcherOnce sync.Once
	closeSearcher := func() {
		joinRuntime()
		closeSearcherOnce.Do(func() {
			if deps.searcher != nil {
				deps.searcher.Close()
			}
		})
	}
	t.Cleanup(closeSearcher)
	newServeVisibility(deps)
	check(openServeSearcher(deps))
	// The separately verified assets keep the browser gate independent of the
	// test binary's placeholder UI while every HTTP/API option comes from serve.
	deps.dist = os.DirFS(dist)
	options, err := newServeAPIOptions(deps)
	check(err)
	handler, err := newServeHTTPHandlers(deps, options, t421ExactFinalAuthorityRead{}, t421ExactFinalAuthorityRead{})
	check(err)
	installed := typedSettingsTLSServer(ctx, t, handler)
	emit, expect := typedSettingsBrowserProtocol(ctx, t)
	port := func(server *httptest.Server) int { return server.Listener.Addr().(*net.TCPAddr).Port }
	emit(map[string]any{"event": "ready", "port": port(installed), "repo": cfg.Source.Repository, "commit": cfg.Source.Commit, "provider": profile.Provider(), "adminEmail": authentication.adminEmail, "ordinaryEmail": authentication.ordinaryEmail, "password": authentication.password, "httpComposition": "production-helpers", "lifecycleOwners": deps.lifecycleStatus.Snapshot().Policy.Owners})
	var coordinator string
	var outcome typedexecutor.Outcome
	var current store.TypedIndexCurrentCustody
	var published store.TypedIndexOperator
	var finishedJobs []store.Job
	for _, command := range []string{"preview", "queued", "queued", "publish", "warm", "stale", "finish"} {
		expect(command)
		event := map[string]any{"event": "done", "command": command}
		switch command {
		case "preview":
			after, err := state.ReadTypedIndexOperator(ctx, cfg.Source.Repository)
			check(err)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("browser preview or refusal mutated native authority")
			}
			typedSettingsJobs(ctx, t, state, 0)
			event["state"] = "absent"
		case "queued":
			op, err := state.ReadTypedIndexOperator(ctx, cfg.Source.Repository)
			check(err)
			jobs := typedSettingsJobs(ctx, t, state, 1)
			if op.Source != cfg.Source || op.Status.Desired == "" || !op.DesiredFresh || op.Desired.Purpose != typedindex.Publish || op.Desired.Action != typedindex.Plan || op.Status.Current != nil || op.Schedule != nil || jobs[0].Status != store.StatusPending || jobs[0].Attempts != 0 {
				t.Fatal("browser retry did not retain one pristine pending Publish")
			}
			if planning != "" && (planning != op.Status.Desired || coordinator != jobs[0].ID) {
				t.Fatal("browser retry changed planning request or coordinator")
			}
			planning, coordinator = op.Status.Desired, jobs[0].ID
			event["state"], event["requestDigest"] = "planning", planning
		case "publish":
			check(startServeTypedIndex(deps))
			outcome, published = typedSettingsNativeSettle(ctx, t, state, cfg.Source.Repository, outcomes, func() error {
				switch {
				case reportFailed.Load():
					return errors.New("native report bound failed")
				case deps.typedRuntime.pending.Load():
					// A failed settlement only recovers; fail now, not at the deadline.
					return errors.New("native settlement failed; recovery pending")
				}
				return nil
			})
			current, err = state.ResolveTypedIndexCurrentCustody(ctx, cfg.Source.Repository)
			check(err)
			finishedJobs = typedSettingsJobs(ctx, t, state, 1)
			if current.PlanningDigest != planning || current.Parent.Digest() != planning || current.Parent.Request().Source != cfg.Source || current.Parent.Purpose() != typedindex.Publish || current.Admission.Digest() != published.Status.Desired || !reflect.DeepEqual(current.Pointer, outcome.Pointer) || current.AttemptDigest != outcome.AttemptDigest || current.Pointer.Binding.RequestDigest != current.Admission.Digest() || current.Pointer.Epoch != 1 || finishedJobs[0].ID != coordinator || finishedJobs[0].Status != store.StatusDone || finishedJobs[0].Attempts != 0 || finishedJobs[0].FinishedAt == nil || published.Status.Stale || published.Status.Canceled || published.Status.RestoreRequired || published.Schedule.Generation != planning || published.Schedule.TotalChunks != 1 || deps.typedRuntime.pending.Load() || deps.typedRuntime.recovering.Load() || lookups.Load() != 1 || reports.Load() != 1 {
				t.Fatal("native current, request, durable settlement or counts differ")
			}
			for i, phase := range []typedindex.Action{typedindex.Plan, typedindex.Execute} {
				r := outcome.Reports[i]
				if r.Phase != phase || r.ExitCode != 0 || !r.Removed || r.StopReason != "" || !r.Resources.LimitsVerified || r.Failure != nil {
					t.Fatal("native phase report is not successful and contained")
				}
			}
			event["state"], event["parentDigest"], event["executionDigest"] = "current", planning, current.Admission.Digest()
			event["root"], event["epoch"], event["attemptDigest"], event["leaseDigest"], event["profileDigest"] = current.Pointer.RootDigest, current.Pointer.Epoch, current.AttemptDigest, current.LeaseDigest, profile.Digest()
			var phaseReports [2]typedSettingsNativePhase
			for i, phase := range outcome.Reports {
				phaseReports[i] = typedSettingsNativePhase{phase.Phase, phase.ExitCode, phase.Removed, phase.StopReason, phase.Resources, phase.FailureOperation, phase.CleanupOperation}
			}
			event["reports"], event["lookups"], event["reportCount"] = phaseReports, lookups.Load(), reports.Load()
			event["definitionPath"], event["referencePath"], event["sourceBytes"], event["sourceFiles"] = "b/b.go", "a/a.go", 176, 5
		case "warm":
			cadence := deps.cfg.Sync.Interval()
			if cadence != 15*time.Second {
				t.Fatal("ordinary polling cadence changed")
			}
			warmStart := time.Now()
			timer := time.NewTimer(2*cadence + time.Second)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				t.Fatal("warm observation deadline")
			}
			after, err := state.ReadTypedIndexOperator(ctx, cfg.Source.Repository)
			check(err)
			retained, err := state.ResolveTypedIndexCurrentCustody(ctx, cfg.Source.Repository)
			check(err)
			jobs := typedSettingsJobs(ctx, t, state, 1)
			if !reflect.DeepEqual(after, published) || !reflect.DeepEqual(retained, current) || !reflect.DeepEqual(jobs, finishedJobs) || lookups.Load() != 1 || reports.Load() != 1 || reportFailed.Load() || len(outcomes) != 0 || deps.typedRuntime.pending.Load() || deps.typedRuntime.recovering.Load() {
				t.Fatal("ordinary polling replayed work or changed publication")
			}
			if _, err := state.GetTypedIndexGrowth(ctx); !errors.Is(err, store.ErrNotFound) {
				t.Fatal("warm polling retained growth", err)
			}
			event["state"], event["lookups"], event["reportCount"], event["cadenceMS"], event["elapsedMS"] = "current", lookups.Load(), reports.Load(), cadence.Milliseconds(), time.Since(warmStart).Milliseconds()
			event["requiredWaitMS"] = (2*cadence + time.Second).Milliseconds()
		case "stale":
			next := strings.Repeat("b", 40)
			if next == cfg.Source.Commit {
				next = strings.Repeat("c", 40)
			}
			check(state.SetRepoIndexed(ctx, cfg.Source.Repository, next, time.Now()))
			op, err := state.ReadTypedIndexOperator(ctx, cfg.Source.Repository)
			check(err)
			if !op.Status.Stale || op.DesiredFresh || op.Revision == before.Revision {
				t.Fatal("source drift did not fence native current")
			}
			event["state"] = "stale"
		case "finish":
			joinRuntime()
			if ctx.Err() != nil || joinFailed.Load() || reportFailed.Load() || lookups.Load() != 1 || reports.Load() != 1 || authentication.failed.Load() {
				t.Fatal("native runtime or real audit failed")
			}
			auditEvents, err := state.ListAuditEvents(ctx, 0, 32)
			check(err)
			enqueues := 0
			for _, a := range auditEvents {
				if a.Action == "enqueue-code-navigation-indexing" && a.Status == http.StatusOK {
					if a.ActorID != authentication.admin.ID || a.AuthMethod != "session" || a.Target != cfg.Source.Repository {
						t.Fatal("native enqueue audit lost session actor")
					}
					enqueues++
				}
			}
			if enqueues != 3 {
				t.Fatal("native bridge did not retain exactly three successful enqueue audits", enqueues)
			}
			installed.Close()
			closeSearcher()
			turns, tombstones := typedSettingsNativeDrain(ctx, t, deps, cfg.Source.Repository, workspace, current)
			check(os.RemoveAll(owned))
			stopAuth()
			authentication.service.WaitCleanup()
			event["drainTurns"], event["retainedParentTombstones"], event["auditEnqueues"], event["lookups"], event["reportCount"] = turns, tombstones, enqueues, lookups.Load(), reports.Load()
			event["workspaceDrained"], event["hostScratchDrained"], event["growthAbsent"], event["runtimeJoined"] = true, true, true, true
			event["inputBytes"], event["inputFiles"], event["configDigest"], event["seedDigest"], event["inventoryDigest"] = inventory.Bytes(), len(inventory.Files()), *typedSettingsNativeConfig, cfg.SeedSHA256, cfg.InventorySHA256
		}
		emit(event)
	}
	t.Log("authenticated Settings native bridge complete; successor/restart/restore and full T45.9 closure remain unestablished")
}

type typedSettingsNativeReport struct {
	outcome typedexecutor.Outcome
	err     error
}

// Settlement completes only after one successful report and durable settled,
// growth-free operator state. failed reports runtime faults that end the wait.
func typedSettingsNativeSettle(ctx context.Context, t *testing.T, state *store.Surreal, repo string, outcomes <-chan typedSettingsNativeReport, failed func() error) (typedexecutor.Outcome, store.TypedIndexOperator) {
	t.Helper()
	waitCtx, stop := context.WithTimeout(ctx, 6*time.Minute)
	defer stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	var outcome typedexecutor.Outcome
	var received bool
	for {
		select {
		case r := <-outcomes:
			if received {
				t.Fatal("native report duplicated")
			}
			if r.err != nil {
				var phases [2]typedSettingsNativePhase
				for i, phase := range r.outcome.Reports {
					phases[i] = typedSettingsNativePhase{phase.Phase, phase.ExitCode, phase.Removed, phase.StopReason, phase.Resources, phase.FailureOperation, phase.CleanupOperation}
				}
				// Keep classification and bounded public measurements; never log
				// the private error, watchdog or worker failure envelope.
				diagnostic, err := json.Marshal(map[string]any{
					"reason": typedServeReason(r.err), "deadline": errors.Is(r.err, context.DeadlineExceeded),
					"canceled": errors.Is(r.err, context.Canceled), "phaseReports": phases,
				})
				if err != nil {
					t.Fatal("native refusal diagnostic unavailable")
				}
				t.Logf("native refusal %s", diagnostic)
				t.Fatal("native execution refused")
			}
			outcome, received = r.outcome, true
		case <-ticker.C:
		case <-waitCtx.Done():
			t.Fatal("production native publication did not settle")
		}
		if err := failed(); err != nil {
			t.Fatal(err)
		}
		if !received {
			continue
		}
		op, err := state.ReadTypedIndexOperator(waitCtx, repo)
		if err != nil {
			t.Fatal("publication observation refused", err)
		}
		if op.Coordinator != store.StatusDone || op.Status.Stage != store.TypedComplete || op.Schedule == nil || op.Schedule.Status != store.GenerationScheduleSettled || op.Schedule.Succeeded != 1 || op.Schedule.Failed != 0 || op.Schedule.Pending != 0 || op.Schedule.Running != 0 {
			continue
		}
		if _, err := state.GetTypedIndexGrowth(waitCtx); !errors.Is(err, store.ErrNotFound) {
			if err == nil {
				continue
			}
			t.Fatal("publication growth observation refused", err)
		}
		return outcome, op
	}
}

func typedSettingsNativeDrain(ctx context.Context, t *testing.T, deps *serveDeps, repo, workspace string, current store.TypedIndexCurrentCustody) (int, int) {
	t.Helper()
	release, err := deps.acquireLifecycleMutation(ctx)
	if err != nil {
		t.Fatal("native retirement guard", err)
	}
	identity := typedworkspace.OwnerIdentity{PlanningDigest: current.PlanningDigest, AttemptDigest: current.AttemptDigest, ChunkIdentity: current.ChunkIdentity, LeaseDigest: current.LeaseDigest, Request: current.Parent.Request()}
	manifest, err := typedworkspace.LoadOwner(ctx, workspace, identity)
	if err == nil && manifest.Digest() != current.Custody.ManifestDigest {
		err = typedworkspace.ErrCustody
	}
	if err == nil {
		err = typedsandbox.QuiescentAttempt(ctx, typedsandbox.RecoveryOptions{Socket: deps.typedInstallation.Socket, ImageID: deps.typedInstallation.Image, Inputs: filepath.Join(workspace, identity.RelativeName(), manifest.InputName), PlanningDigest: current.PlanningDigest, AttemptDigest: current.AttemptDigest})
	}
	if err == nil {
		err = deps.st.DeleteRepo(ctx, repo)
	}
	release()
	if err != nil {
		t.Fatal("native fixture repository retirement", err)
	}
	controller, err := lifecycle.NewController(deps.st, deps.typedRuntime)
	if err != nil {
		t.Fatal("native lifecycle controller", err)
	}
	for turn := 1; turn <= 12000; turn++ {
		result := controller.Tick(ctx)
		if result.Err != nil || result.Deleted < 0 || result.Deleted > 16 || result.Owner != lifecycle.TypedIndexOwner {
			t.Fatal("bounded native lifecycle drain", result.Err)
		}
		dir, err := os.Open(workspace)
		if err != nil {
			t.Fatal("native workspace census", err)
		}
		entries, readErr := dir.ReadDir(2)
		closeErr := dir.Close()
		if closeErr != nil || readErr != nil && readErr != io.EOF {
			t.Fatal("native workspace census read")
		}
		if len(entries) != 1 || entries[0].Name() != ".phebs-index-publication.lock" {
			continue
		}
		complete, tombstones := true, 0
		for _, kind := range []store.TypedIndexControlKind{store.TypedIndexRequests, store.TypedIndexPlans, store.TypedIndexAttempts, store.TypedIndexStates, store.TypedIndexCurrents, store.TypedIndexIntents} {
			page, err := deps.st.ScanTypedIndexControls(ctx, kind, "", 8)
			if err != nil || page.Next != "" {
				t.Fatal("native control drain census", err)
			}
			for _, row := range page.Rows {
				if kind == store.TypedIndexRequests && row.Parent && row.ID == row.Root && row.State == "collecting" && row.Repository == repo {
					tombstones++
				} else {
					complete = false
				}
			}
		}
		if tombstones > 1 {
			t.Fatal("native drain retained extra parent tombstones")
		}
		if !complete {
			continue
		}
		host, err := typedsandbox.ObserveHostScratch(ctx, "")
		if err != nil || host.Held || host.Overflow || len(host.Names) != 0 {
			t.Fatal("native host scratch not drained", err)
		}
		if _, err := deps.st.GetTypedIndexGrowth(ctx); !errors.Is(err, store.ErrNotFound) {
			t.Fatal("native lifecycle retained growth", err)
		}
		return turn, tombstones
	}
	t.Fatal("native lifecycle turn ceiling reached")
	return 0, 0
}
