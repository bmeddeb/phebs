//go:build linux

package typedexecutor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/codenav"
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/recovery"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/bmeddeb/phebs/internal/typedsandbox"
	"github.com/bmeddeb/phebs/internal/typedworkspace"
	"github.com/surrealdb/surrealdb.go"
)

type nativeRestorePhases struct {
	Begins     int                       `json:"begins"`
	Count      int                       `json:"launches"`
	Allowances [2]typedsandbox.Allowance `json:"allowances"`
}

func (p *nativeRestorePhases) track(c *Controller) {
	begin, run := c.native.begin, c.native.run
	c.native.begin = func(ctx context.Context, planning, attempt string) (typedsandbox.Allowance, error) {
		p.Begins++
		return begin(ctx, planning, attempt)
	}
	c.native.run = func(ctx context.Context, o typedsandbox.Options, a typedsandbox.ScratchAuthority) (typedsandbox.Result, error) {
		if p.Count >= len(p.Allowances) {
			return typedsandbox.Result{}, errors.New("extra restore rehearsal phase")
		}
		p.Allowances[p.Count] = o.Allowance
		p.Count++
		return run(ctx, o, a)
	}
}

func (p nativeRestorePhases) verify(t *testing.T, publication nativeInputPublication) {
	t.Helper()
	current := publication.current
	if p.Begins != 1 || p.Count != 2 || *publication.lookups != 1 || *publication.launches != 2 {
		t.Fatal("restore rehearsal native count")
	}
	for i, allowance := range p.Allowances {
		if allowance.Validate() != nil || allowance.PlanningDigest != current.PlanningDigest || allowance.AttemptDigest != current.AttemptDigest || allowance.Start != p.Allowances[0].Start || allowance.Deadline != p.Allowances[0].Deadline || allowance.Deadline-allowance.Start != int64(typedsandbox.WallLimit) {
			t.Fatal("restore rehearsal refreshed native allowance")
		}
		report := publication.outcome.Reports[i]
		if report.Phase != []typedindex.Action{typedindex.Plan, typedindex.Execute}[i] || report.ExitCode != 0 || !report.Removed || report.StopReason != "" || !report.Resources.LimitsVerified || report.Failure != nil {
			t.Fatal("restore rehearsal native phase not verified")
		}
	}
}

type nativeRestoreContent struct {
	Definition codenav.DefinitionResult
	References codenav.ReferencesResult
	Hover      codenav.HoverResult
}

func nativeRestoreRead(t *testing.T, ctx context.Context, service *codenav.Service, resolver *acceptanceCorpusResolver, query codenav.Query, unavailable bool) nativeRestoreContent {
	t.Helper()
	definition, de := service.Definition(ctx, query)
	references, re := service.References(ctx, query)
	hover, he := service.Hover(ctx, query)
	if unavailable {
		if !errors.Is(de, store.ErrNotFound) || !errors.Is(re, store.ErrNotFound) || !errors.Is(he, store.ErrNotFound) || definition.Available || definition.Symbol != "" || definition.Location != nil || references.Available || references.Symbol != "" || len(references.Locations) != 0 || references.Truncated || hover.Available || hover.Hover != nil {
			t.Fatal("warm cached navigation escaped missing restored authority")
		}
	} else {
		definitionPoint := acceptanceOraclePoint{Path: "b/b.go", Range: [3]int32{1, 6, 12}}
		referencePoint := acceptanceOraclePoint{Path: "a/a.go", Range: [3]int32{2, 15, 21}}
		if errors.Join(de, re, he) != nil || !definition.Available || definition.Symbol == "" || !acceptanceCorpusLocation(resolver.conf, definitionPoint, definition.Location) || !references.Available || references.Symbol != definition.Symbol || references.Truncated || len(references.Locations) != 1 || !acceptanceCorpusLocation(resolver.conf, referencePoint, &references.Locations[0]) || !hover.Available || hover.Hover == nil || hover.Hover.Symbol != definition.Symbol || hover.Hover.Range != acceptanceCorpusRange(referencePoint) || hover.Hover.Encoding != codenav.EncodingUTF8 || !strings.Contains(hover.Hover.Signature+"\n"+strings.Join(hover.Hover.Documentation, "\n"), "Answer") {
			t.Fatal("restore rehearsal routed workspace content", errors.Join(de, re, he))
		}
	}
	return nativeRestoreContent{definition, references, hover}
}

func nativeRestoreFixture(t *testing.T, s *store.Surreal, data, bundle string, raw []byte) fixture {
	t.Helper()
	workspace, index := filepath.Join(data, "typed-index"), filepath.Join(data, "index")
	for _, dir := range []string{workspace, index} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	nativeInputWrite(t, filepath.Join(workspace, ".phebs-index-publication.lock"), nil, 0600)
	c, err := New(Config{Store: s, Workspace: workspace, Acquire: func(ctx context.Context) (func(), error) { return focusedindex.AcquireMutationLock(ctx, index) }, Socket: acceptanceSocket, Image: *inputNativeImage})
	if err != nil {
		t.Fatal(err)
	}
	return fixture{c: c, s: s, source: bundle, raw: raw}
}

func nativeRestoreDrain(t *testing.T, ctx context.Context, f *fixture, repo string, publication nativeInputPublication, phases nativeRestorePhases) (int, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	release, err := f.c.enter(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = f.s.DeleteRepo(ctx, repo)
	release()
	if err != nil {
		t.Fatal("restore rehearsal repository retirement", err)
	}
	controller, err := lifecycle.NewController(f.s, LifecycleOwner{f.c})
	if err != nil {
		t.Fatal(err)
	}
	for turns := 1; turns <= 12000; turns++ {
		result := controller.Tick(ctx)
		if result.Err != nil || result.Deleted < 0 || result.Deleted > 16 {
			t.Fatal("restore rehearsal bounded lifecycle drain", result.Err)
		}
		entries, err := os.ReadDir(f.c.config.Workspace)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || entries[0].Name() != ".phebs-index-publication.lock" {
			continue
		}
		drained, tombstones, err := nativePressureControlsDrained(ctx, f.s, repo)
		if err != nil || tombstones > 1 {
			t.Fatal("restore rehearsal control drain", err)
		}
		if !drained {
			continue
		}
		host, err := typedsandbox.ObserveHostScratch(ctx, "")
		if err != nil || host.Held || host.Overflow || len(host.Names) != 0 {
			t.Fatal("restore rehearsal native scratch drain", err)
		}
		if _, err = f.s.GetTypedIndexGrowth(ctx); !errors.Is(err, store.ErrNotFound) {
			t.Fatal("restore rehearsal retained growth", err)
		}
		phases.verify(t, publication)
		return turns, tombstones
	}
	t.Fatal("restore rehearsal lifecycle ceiling reached")
	return 0, 0
}

func nativeRestoreRevalidate(t *testing.T, ctx context.Context, bundle, originalGit string, raw []byte, profile typedindex.Profile, parent typedindex.Admission) {
	t.Helper()
	inventory, err := typedindex.DecodeInventory(ctx, raw, profile.Definition().BundleDigest)
	if err != nil || !acceptanceWorkspaceSources(inventory) {
		t.Fatal("retained restore inventory", err)
	}
	for _, file := range inventory.Files() {
		if err := ctx.Err(); err != nil {
			t.Fatal(err)
		}
		name := filepath.Join(bundle, filepath.FromSlash(file.Path))
		opened, err := acceptanceOpen(name)
		if err != nil {
			t.Fatal("retained restore file ownership", err)
		}
		info, statErr := opened.Stat()
		closeErr := opened.Close()
		mode := os.FileMode(0600)
		if file.Executable {
			mode = 0700
		}
		if errors.Join(statErr, closeErr) != nil || info.Size() != file.Bytes || info.Mode().Perm() != mode {
			t.Fatal("retained restore file size or mode")
		}
		digest, err := acceptanceFileHash(name, file.Bytes)
		if err != nil || digest != file.Digest {
			t.Fatal("retained restore file identity", err)
		}
	}
	metadata, err := acceptanceRead(filepath.Join(bundle, typedindex.HostToolsFile), typedindex.MaxHostToolsBytes)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := typedindex.BindHostTools(ctx, parent, inventory, metadata)
	formatter, fe := acceptanceFormatterHash()
	helper, he := acceptanceFileHash(filepath.Join(*inputNativeTools, "phebs"), typedindex.MaxFileBytes)
	if errors.Join(err, fe, he) != nil || bound.MkfsDigest != formatter || bound.Helper.Digest != helper || helper != profile.Definition().Tools.Planner.Digest {
		t.Fatal("retained restore tools changed", errors.Join(err, fe, he))
	}
	head, err := gitobj.Output(ctx, originalGit, 128, "rev-parse", "HEAD")
	if err != nil || strings.TrimSpace(string(head)) != parent.Request().Source.Commit {
		t.Fatal("retained restore source HEAD changed", err)
	}
}

func nativeRestoreEmpty(t *testing.T, ctx context.Context, s *store.Surreal, data, repo string) {
	t.Helper()
	for _, kind := range []store.TypedIndexControlKind{store.TypedIndexRequests, store.TypedIndexPlans, store.TypedIndexAttempts, store.TypedIndexStates, store.TypedIndexCurrents, store.TypedIndexIntents} {
		page, err := s.ScanTypedIndexControls(ctx, kind, "", 1)
		want := 0
		if kind == store.TypedIndexIntents {
			want = 1
		}
		if err != nil || page.Next != "" || len(page.Rows) != want || want == 1 && page.Rows[0].Repository != repo {
			t.Fatal("restored control inventory", err)
		}
	}
	jobs, err := s.ListJobsPage(ctx, store.JobPageQuery{Kind: store.JobTypedIndex, Limit: 1})
	if err != nil || jobs == nil || jobs.Next != nil || len(jobs.Jobs) != 0 {
		t.Fatal("restored typed job inventory", err)
	}
	// The same real runtime credentials are used only in this private SDK session;
	// neither them nor the runtime descriptor are included in diagnostics/receipts.
	runtime, err := store.ReadLocalRuntime(data)
	if err != nil {
		t.Fatal("restored runtime unavailable")
	}
	db, err := surrealdb.FromEndpointURLString(ctx, runtime.Endpoint)
	if err != nil {
		t.Fatal("restored SDK session unavailable")
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if err := db.Close(closeCtx); err != nil {
			t.Error("restored SDK session close failed")
		}
	}()
	if _, err = db.SignIn(ctx, surrealdb.Auth{Username: "root", Password: runtime.Pass}); err != nil {
		t.Fatal("restored SDK session sign-in failed")
	}
	if err = db.Use(ctx, "phebs", "phebs"); err != nil {
		t.Fatal("restored SDK database selection failed")
	}
	for _, table := range []string{"generation_schedule", "generation_schedule_current", "generation_schedule_repository", "generation_schedule_chunk"} {
		results, err := surrealdb.Query[[]map[string]any](ctx, db, "SELECT id FROM "+table+" LIMIT 1;", nil)
		if err != nil || results == nil || len(*results) != 1 || (*results)[0].Status != "OK" || (*results)[0].Error != nil || len((*results)[0].Result) != 0 {
			t.Fatal("restored generation control inventory")
		}
	}
}

// Explicit neutral root/workspace rehearsal. The two publications use unchanged
// production native limits and are separated by the public six-artifact restore.
func TestNativeInputRestore(t *testing.T) {
	if *inputNativeTools == "" && *inputNativeImage == "" && *inputNativeMode == "" {
		t.Skip("explicit privileged neutral restore rehearsal")
	}
	if os.Geteuid() != 0 || !filepath.IsAbs(*inputNativeTools) || *inputNativeImage == "" || *inputNativeMode != "workspace" {
		t.Fatal("explicit root/workspace/tool/image admission required")
	}
	ctx := t.Context()
	root := t.TempDir()
	data, restoredData, backup, bundle := filepath.Join(root, "data-original"), filepath.Join(root, "data-restored"), filepath.Join(root, "backup"), filepath.Join(root, "bundle")
	config := []byte("neutral native workspace restore fixture")
	if err := os.Mkdir(data, 0700); err != nil {
		t.Fatal(err)
	}
	active, err := store.OpenLocalWithConfig(ctx, data, recovery.ConfigDigest(config))
	if err != nil {
		t.Fatal(err)
	}
	closeActive := func() {
		t.Helper()
		if active == nil {
			return
		}
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		err := active.Close(closeCtx)
		active = nil
		if err != nil {
			t.Error("native restore engine close", err)
		}
	}
	t.Cleanup(closeActive)
	const repo = "example.invalid/native-restore"
	if err = active.UpsertRepo(ctx, store.Repo{Name: repo}); err != nil {
		t.Fatal(err)
	}
	profile, raw, universe, originalGit := nativeInputBundle(t, active, repo, bundle, "workspace")
	intent, err := active.InstallTypedProfile(ctx, repo, profile, universe, 0)
	if err != nil {
		t.Fatal(err)
	}
	f := nativeRestoreFixture(t, active, data, bundle, raw)
	var originalPhases nativeRestorePhases
	originalPhases.track(f.c)
	original := nativeInputPublish(t, &f, repo, profile, nil)
	originalPhases.verify(t, original)
	source := original.current.Parent.Request().Source
	oldRequest := encode(t, original.current.Parent.Request())
	// Keep the same service and its warm metadata across both engine lifetimes.
	navData := filepath.Join(root, "navigation")
	mirror := filepath.Join(navData, "repos", filepath.FromSlash(repo)+".git")
	if err = os.MkdirAll(filepath.Dir(mirror), 0700); err != nil {
		t.Fatal(err)
	}
	cloneCtx, stopClone := context.WithTimeout(ctx, time.Minute)
	err = exec.CommandContext(cloneCtx, "git", "clone", "--bare", "--no-hardlinks", "--", originalGit, mirror).Run()
	stopClone()
	if err != nil {
		t.Fatal("neutral restore source mirror", err)
	}
	resolver := &acceptanceCorpusResolver{store: active, base: f.c.config.Workspace, conf: nativeAcceptanceConfig{Source: source}}
	service := codenav.New(codenav.Options{DataDir: navData, RoutedResolver: resolver})
	t.Cleanup(func() { _ = service.Remove(repo) })
	query := codenav.Query{Repo: repo, Revision: source.Commit, Path: "a/a.go", Line: 2, Character: 15, Encoding: codenav.EncodingUTF8}
	cold := nativeRestoreRead(t, ctx, service, resolver, query, false)
	if !reflect.DeepEqual(cold, nativeRestoreRead(t, ctx, service, resolver, query, false)) {
		t.Fatal("native restore original warm payload")
	}
	oldBinding, err := resolver.ResolveRoutedIndex(ctx, repo, source.Commit)
	if err != nil {
		t.Fatal(err)
	}
	reader, oldMetadata, err := resolver.OpenRoutedIndex(ctx, oldBinding, nil)
	if err != nil || reader == nil {
		t.Fatal("native restore cached publication", err)
	}
	if err = reader.Close(); err != nil {
		t.Fatal(err)
	}
	opts := recovery.Options{DataDir: data, Config: config, PhebsBinary: filepath.Join(*inputNativeTools, "phebs"), PhebsVersion: "neutral-native-restore"}
	manifest, err := recovery.Create(ctx, recovery.BackupOptions{Options: opts, Output: backup})
	if err != nil {
		t.Fatal("native current public backup", err)
	}
	const excluded = "typed_index_request,typed_index_plan,typed_index_attempt,typed_index_current,typed_index_state,typed_index_job,generation_schedule,generation_schedule_current,generation_schedule_repository,generation_schedule_chunk"
	wantExport := []string{"surreal", "export", "--endpoint", "<live-loopback-endpoint>", "--namespace", "phebs", "--database", "phebs", "--log", "none", "--tables-exclude", excluded, recovery.DatabaseName}
	paths := make([]string, 0, len(manifest.Inventory))
	for _, artifact := range manifest.Inventory {
		paths = append(paths, artifact.Path)
	}
	slices.Sort(paths)
	wantPaths := []string{recovery.CallerPublicationName, recovery.DatabaseName, recovery.FocusedIndexName, recovery.ObservationPublicationName, recovery.RelationshipPublicationName, recovery.ResolverCatalogName}
	slices.Sort(wantPaths)
	if manifest.Schema != recovery.ManifestSchema || manifest.Database != (recovery.DatabaseIdentity{Namespace: "phebs", Database: "phebs"}) || !reflect.DeepEqual(manifest.Store, store.CurrentStoreIdentity()) || !slices.Equal(paths, wantPaths) || !slices.Equal(manifest.ExportCommand, wantExport) || manifest.ConfigSHA256 != recovery.ConfigDigest(config) || manifest.Phebs.SHA256 != profile.Definition().Tools.Planner.Digest {
		t.Fatal("native backup manifest contract")
	}
	for _, declaration := range []string{
		"typed-index/ and host typed scratch (all generated bundles, workspace, caches, prehydration and worker custody)",
		"typed_index_request, typed_index_plan, typed_index_attempt, typed_index_current, typed_index_state, typed_index_job (regenerate-on-restore)",
		"generation_schedule, generation_schedule_current, generation_schedule_repository, generation_schedule_chunk (restartable execution controls)",
	} {
		if !slices.Contains(manifest.DerivedExclusions, declaration) {
			t.Fatal("native backup derived exclusion declaration")
		}
	}
	export, err := acceptanceRead(filepath.Join(backup, recovery.DatabaseName), 16<<20)
	if err != nil || !strings.Contains(string(export), "DEFINE TABLE typed_index_intent ") || !strings.Contains(string(export), "id: typed_index_intent:") || !strings.Contains(string(export), profile.Digest()) {
		t.Fatal("native backup precious profile intent", err)
	}
	for _, table := range strings.Split(excluded, ",") {
		for _, marker := range []string{"DEFINE TABLE " + table + " ", "TABLE DATA: " + table + "\n", "id: " + table + ":"} {
			if strings.Contains(string(export), marker) {
				t.Fatal("native backup transported derived controls")
			}
		}
	}
	originalTurns, originalTombstones := nativeRestoreDrain(t, ctx, &f, repo, original, originalPhases)
	closeActive()
	if t.Failed() {
		t.Fatal("original engine close failed")
	}
	if _, err = store.ReadLocalRuntime(data); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("original engine runtime survived close")
	}
	opts.DataDir = restoredData
	restoredManifest, err := recovery.Restore(ctx, recovery.RestoreOptions{Options: opts, Backup: backup})
	if err != nil || !reflect.DeepEqual(manifest, restoredManifest) {
		t.Fatal("native current public restore", err)
	}
	if _, err = os.Lstat(filepath.Join(restoredData, "typed-index")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("generated native workspace traveled through backup")
	}
	active, err = store.OpenLocalWithConfig(ctx, restoredData, recovery.ConfigDigest(config))
	if err != nil {
		t.Fatal(err)
	}
	restoredSource, err := active.GetTypedSource(ctx, repo)
	if err != nil || restoredSource != source {
		t.Fatal("restore changed exact source identity", err)
	}
	status, err := active.GetTypedIndexStatus(ctx, repo)
	if err != nil || !status.RestoreRequired || status.Desired != "" || status.Current != nil {
		t.Fatal("restored native authority available before revalidation", err)
	}
	if _, err = active.ReadTypedIndexCurrentCustody(ctx, repo); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("restored native current survived", err)
	}
	if _, err = active.EnqueueTypedIndex(ctx, repo, oldRequest); !errors.Is(err, typedindex.Disabled) {
		t.Fatal("restored old request replay", err)
	}
	nativeRestoreEmpty(t, ctx, active, restoredData, repo)
	resolver.store, resolver.base = active, filepath.Join(restoredData, "typed-index")
	nativeRestoreRead(t, ctx, service, resolver, query, true)
	reader, _, err = resolver.OpenRoutedIndex(ctx, oldBinding, oldMetadata)
	if reader != nil || !errors.Is(err, store.ErrNotFound) {
		if reader != nil {
			_ = reader.Close()
		}
		t.Fatal("saved binding reopened missing restored current", err)
	}
	nativeRestoreRevalidate(t, ctx, bundle, originalGit, raw, profile, original.current.Parent)
	restoredIntent, err := active.GetTypedIndexIntent(ctx, repo)
	if err != nil || restoredIntent.ProfileEpoch != intent.ProfileEpoch || restoredIntent.ProfileDigest != intent.ProfileDigest || restoredIntent.ProfileJSON != intent.ProfileJSON || restoredIntent.UniverseDigest != universe || !restoredIntent.RestoreRequired {
		t.Fatal("precious restore profile changed", err)
	}
	next, err := active.InstallTypedProfile(ctx, repo, profile, universe, restoredIntent.ProfileEpoch)
	if err != nil || next.ProfileEpoch != intent.ProfileEpoch+1 || next.RestoreRequired || next.Desired != "" {
		t.Fatal("trusted restored profile revalidation", err)
	}
	if _, err = active.EnqueueTypedIndex(ctx, repo, oldRequest); !errors.Is(err, typedindex.Stale) {
		t.Fatal("old request escaped restored profile epoch fence", err)
	}
	if _, err = active.ReadTypedIndexCurrentCustody(ctx, repo); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("profile revalidation invented current", err)
	}
	f = nativeRestoreFixture(t, active, restoredData, bundle, raw)
	var restoredPhases nativeRestorePhases
	restoredPhases.track(f.c)
	fresh := nativeInputPublish(t, &f, repo, profile, nil)
	restoredPhases.verify(t, fresh)
	if fresh.current.Parent.Request().Source != source || fresh.current.PlanningDigest == original.current.PlanningDigest || fresh.current.Admission.Digest() == original.current.Admission.Digest() || fresh.current.AttemptDigest == original.current.AttemptDigest || fresh.current.ChunkIdentity == original.current.ChunkIdentity || fresh.current.LeaseDigest == original.current.LeaseDigest || fresh.current.Pointer.RootDigest == original.current.Pointer.RootDigest || restoredPhases.Allowances[0].Start <= originalPhases.Allowances[0].Start || restoredPhases.Allowances[0].BootID != originalPhases.Allowances[0].BootID {
		t.Fatal("restore did not mint fresh exact native generation")
	}
	newBinding, err := resolver.ResolveRoutedIndex(ctx, repo, source.Commit)
	if err != nil || newBinding == oldBinding {
		t.Fatal("restored native binding identity", err)
	}
	for range 2 {
		if !reflect.DeepEqual(cold, nativeRestoreRead(t, ctx, service, resolver, query, false)) {
			t.Fatal("fresh restored native content differs at unchanged source")
		}
	}
	for _, check := range []struct {
		binding codenav.RoutedBinding
		want    error
	}{{oldBinding, codenav.ErrBindingChanged}, {newBinding, typedworkspace.ErrCustody}} {
		reader, _, err = resolver.OpenRoutedIndex(ctx, check.binding, oldMetadata)
		if reader != nil || !errors.Is(err, check.want) {
			if reader != nil {
				_ = reader.Close()
			}
			t.Fatal("old cached publication escaped restored identity fence", err)
		}
	}
	confirmed, err := active.ReadTypedIndexCurrentCustody(ctx, repo)
	if err != nil || !reflect.DeepEqual(fresh.current, confirmed) {
		t.Fatal("restore consumers changed current authority", err)
	}
	restoredTurns, restoredTombstones := nativeRestoreDrain(t, ctx, &f, repo, fresh, restoredPhases)
	closeActive()
	if t.Failed() {
		t.Fatal("restored engine close failed")
	}
	if _, err = store.ReadLocalRuntime(restoredData); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("restored engine runtime survived close")
	}
	record := map[string]any{"schema": "phebs-typed-neutral-native-restore-v1", "source": source, "mode": "workspace", "provider": profile.Provider(), "image_digest": *inputNativeImage, "tools": profile.Definition().Tools, "profile_digest": profile.Digest(), "inventory_digest": profile.Definition().BundleDigest, "profile_epochs": [2]int64{intent.ProfileEpoch, next.ProfileEpoch}, "planning_digests": [2]string{original.current.PlanningDigest, fresh.current.PlanningDigest}, "execution_digests": [2]string{original.current.Admission.Digest(), fresh.current.Admission.Digest()}, "attempt_digests": [2]string{original.current.AttemptDigest, fresh.current.AttemptDigest}, "chunk_identities": [2]string{original.current.ChunkIdentity, fresh.current.ChunkIdentity}, "lease_digests": [2]string{original.current.LeaseDigest, fresh.current.LeaseDigest}, "root_digests": [2]string{original.current.Pointer.RootDigest, fresh.current.Pointer.RootDigest}, "phases": [2]nativeRestorePhases{originalPhases, restoredPhases}, "phase_reports": [2][2]PhaseReport{original.outcome.Reports, fresh.outcome.Reports}, "native_launches": 4, "native_allowances": 2, "manifest_schema": manifest.Schema, "manifest_digest": manifest.ManifestSHA256, "artifacts": manifest.Inventory, "phebs": manifest.Phebs, "surreal": manifest.Surreal, "export_command": manifest.ExportCommand, "derived_exclusions": manifest.DerivedExclusions, "restored_unavailable": true, "old_request_disabled": true, "old_request_stale": true, "warm_cache_missing_refused": true, "old_binding_refused": true, "old_metadata_refused": true, "routed_content_equal": true, "same_lease_reused": true, "source_preserved": true, "retirement": "fixture_repository_deletion", "lifecycle_turns": [2]int{originalTurns, restoredTurns}, "retained_parent_tombstones": [2]int{originalTombstones, restoredTombstones}, "workspace_drained": true, "native_absent": true, "engines_joined": true}
	receipt := encode(t, record)
	if len(receipt) > acceptanceMaxReceipt {
		t.Fatal("native restore receipt bound")
	}
	t.Logf("native restore receipt: %s", receipt)
}
