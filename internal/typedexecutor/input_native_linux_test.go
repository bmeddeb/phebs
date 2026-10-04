//go:build linux

package typedexecutor

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/generationscheduler"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/typedbazel/provider"
	"github.com/bmeddeb/phebs/internal/typedimport"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/bmeddeb/phebs/internal/typedmodule"
	"github.com/bmeddeb/phebs/internal/typedsandbox"
	"github.com/bmeddeb/phebs/internal/typedworkspace"
	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"
)

// Explicit privileged rehearsal only: ordinary tests never provision a sandbox.
// Tools contains the pinned public Go SDK/scip-go plus a current Linux Phebs
// helper. The production controller authenticates every byte and native limit.
var inputNativeTools = flag.String("typed-input-tools", "", "private neutral input tool directory")
var inputNativeImage = flag.String("typed-input-image", "", "exact locally present neutral image digest")
var inputNativeMode = flag.String("typed-input-mode", "", "one neutral rehearsal: single, workspace or import")

type inputNativeControls map[string][]byte

func (c inputNativeControls) Read(ctx context.Context, name string, limit int) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b, ok := c[name]
	if !ok || len(b) > limit {
		return nil, typedindex.Unprepared
	}
	return slices.Clone(b), nil
}

func nativeInputFixture(t *testing.T, endpoint, mode string) (fixture, typedindex.Profile) {
	t.Helper()
	var profile typedindex.Profile
	f := preparationFixture(t, endpoint, "input_"+mode, func(s *store.Surreal, repo, dir string) (typedindex.Profile, []byte, string) {
		if err := os.Remove(filepath.Join(dir, "data")); err != nil {
			t.Fatal(err)
		}
		sourceFiles := inputNativeControls{"go.mod": []byte("module example.test/root\ngo 1.25.0\n"), "lib.go": []byte("package root\nconst Answer = 42\n")}
		selectors := []string{"example.test/root"}
		entry := "go.mod"
		discoveryMode := typedmodule.ModeSingle
		kind := typedindex.ModuleProviderID
		if mode == "workspace" {
			sourceFiles = inputNativeControls{"go.work": []byte("go 1.25.0\nuse (\n ./a\n ./b\n)\n"), "a/go.mod": []byte("module example.test/a\ngo 1.25.0\n"), "b/go.mod": []byte("module example.test/b\ngo 1.25.0\n"), "a/a.go": []byte("package a\nimport \"example.test/b\"\nvar Answer = b.Answer\n"), "b/b.go": []byte("package b\nconst Answer = 42\n")}
			selectors = []string{"example.test/a", "example.test/b"}
			entry = "go.work"
			discoveryMode = typedmodule.ModeWorkspace
		}
		// A real neutral Git commit, never an inferred tool-output version.
		git := t.TempDir()
		for name, b := range sourceFiles {
			nativeInputWrite(t, filepath.Join(git, name), b, 0600)
			nativeInputWrite(t, filepath.Join(dir, "source", name), b, 0600)
		}
		for _, args := range [][]string{{"init", "-q"}, {"add", "--", "."}, {"-c", "user.name=neutral", "-c", "user.email=neutral@example.invalid", "commit", "-q", "-m", "neutral input"}} {
			if b, e := exec.CommandContext(t.Context(), "git", append([]string{"-C", git}, args...)...).CombinedOutput(); e != nil {
				t.Fatal(e, string(b))
			}
		}
		commit, e := exec.CommandContext(t.Context(), "git", "-C", git, "rev-parse", "HEAD").Output()
		if e != nil {
			t.Fatal(e)
		}
		if e = s.SetRepoIndexed(t.Context(), repo, strings.TrimSpace(string(commit)), time.Now()); e != nil {
			t.Fatal(e)
		}
		source, e := s.GetTypedSource(t.Context(), repo)
		if e != nil {
			t.Fatal(e)
		}
		selection := provider.InputSelection{Schema: provider.InputSelectionSchema, Source: source, Plan: typedindex.PackagePlanDefinition{Schema: typedindex.PackagePlanSchema}}
		discovered, e := typedmodule.Discover(t.Context(), sourceFiles, discoveryMode, entry, selectors)
		if e != nil {
			t.Fatal(e)
		}
		scope := discovered.Digest()
		selection.Module = &discovered
		if mode == "import" {
			kind = typedindex.ImportProviderID
			selection.Module = nil
			symbol := "scip-go gomod example.test/root " + source.Commit + " root/Answer."
			artifact, e := proto.Marshal(&scip.Index{Metadata: &scip.Metadata{ProjectRoot: "file:///capture", TextDocumentEncoding: scip.TextEncoding_UTF8, ToolInfo: &scip.ToolInfo{Name: "scip-go", Version: "0.2.7"}}, Documents: []*scip.Document{{Language: "go", RelativePath: "lib.go", Symbols: []*scip.SymbolInformation{{Symbol: symbol}}, Occurrences: []*scip.Occurrence{{Symbol: symbol, Range: []int32{1, 6, 12}, SymbolRoles: int32(scip.SymbolRole_Definition)}}}}})
			if e != nil {
				t.Fatal(e)
			}
			nativeInputWrite(t, filepath.Join(dir, "artifacts/index.scip"), artifact, 0600)
			selection.Import = &typedimport.ImportSelection{Schema: typedimport.SelectionSchema, Source: source, Producer: typedimport.Producer{Name: "scip-go", Version: "0.2.7", Digest: hash([]byte("declared-neutral-producer"))}, Artifacts: []typedimport.ImportArtifact{{Path: "artifacts/index.scip", Bytes: int64(len(artifact)), Digest: hash(artifact)}}, Roots: []typedimport.RootMapping{{Input: "capture", Repo: "."}}, Coverage: selectors, Excluded: []string{}, Provenance: "neutral-operator-attestation"}
			scope = selection.Import.Digest()
		}
		for n, selector := range selectors {
			target := provider.InputTargetID(kind, scope, selector)
			unit, _ := typedindex.NewPackageUnitID(target)
			doc := "lib.go"
			if mode == "workspace" {
				doc = []string{"a/a.go", "b/b.go"}[n]
			}
			imports := []typedindex.PackageUnitID{}
			deps := []string{}
			if mode == "workspace" && n == 0 {
				dep := provider.InputTargetID(kind, scope, selectors[1])
				u, _ := typedindex.NewPackageUnitID(dep)
				imports = append(imports, u)
				deps = append(deps, dep)
			}
			selection.Plan.Targets = append(selection.Plan.Targets, typedindex.PlannedTarget{ID: target, Units: []typedindex.PackageUnitID{unit}, Dependencies: deps})
			selection.Plan.Units = append(selection.Plan.Units, typedindex.PlannedUnit{ID: unit, Imports: imports, Documents: []string{doc}})
			selection.Plan.Documents = append(selection.Plan.Documents, typedindex.PlannedDocument{Path: doc, Unit: unit, Member: "input-" + string(rune('0'+n)), Bytes: int64(len(sourceFiles[doc])), Digest: hash(sourceFiles[doc])})
		}
		slices.SortFunc(selection.Plan.Targets, func(a, b typedindex.PlannedTarget) int { return strings.Compare(a.ID, b.ID) })
		slices.SortFunc(selection.Plan.Units, func(a, b typedindex.PlannedUnit) int { return strings.Compare(string(a.ID), string(b.ID)) })
		name, _ := typedindex.SelectionFile(kind)
		nativeInputWrite(t, filepath.Join(dir, name), encode(t, selection), 0600)
		toolBytes, e := os.ReadFile(filepath.Join(*inputNativeTools, "phebs"))
		if e != nil {
			t.Fatal(e)
		}
		nativeInputWrite(t, filepath.Join(dir, typedindex.ManagedHelperFile), toolBytes, 0700)
		helper := typedindex.Tool{Version: "neutral-t45.7", Digest: hash(toolBytes)}
		goSDK, scip, _, _ := provider.NativeToolDigests(runtime.GOARCH)
		tools := typedindex.Tools{Planner: helper, Launcher: helper, Indexer: typedindex.Tool{Version: "0.2.7", Digest: scip}}
		if mode == "import" {
			tools.Indexer.Digest = selection.Import.Producer.Digest
		} else {
			tools.Go = typedindex.Tool{Version: "1.25.0", Digest: goSDK}
			for _, prefix := range []string{"go", "bin"} {
				root := filepath.Join(*inputNativeTools, prefix)
				if e = filepath.WalkDir(root, func(name string, d fs.DirEntry, err error) error {
					if err != nil {
						return err
					}
					if d.IsDir() {
						return nil
					}
					info, err := d.Info()
					if err != nil {
						return err
					}
					if !info.Mode().IsRegular() {
						return typedindex.Invalid
					}
					rel, err := filepath.Rel(*inputNativeTools, name)
					if err != nil {
						return err
					}
					b, err := os.ReadFile(name)
					if err != nil {
						return err
					}
					mode := os.FileMode(0600)
					if info.Mode()&0111 != 0 {
						mode = 0700
					}
					nativeInputWrite(t, filepath.Join(dir, "tools", rel), b, mode)
					return nil
				}); e != nil {
					t.Fatal(e)
				}
			}
		}
		nativeInputWrite(t, filepath.Join(dir, "tools/modcache/.phebs-empty"), nil, 0600)
		formatter, e := os.ReadFile("/usr/sbin/mke2fs")
		if e != nil {
			t.Fatal(e)
		}
		nativeInputWrite(t, filepath.Join(dir, typedindex.HostToolsFile), encode(t, typedindex.HostToolsDefinition{Schema: typedindex.HostToolsSchema, MkfsDigest: hash(formatter)}), 0600)
		inventory := typedindex.InventoryDefinition{Schema: typedindex.InventorySchema, Files: []typedindex.BundleFile{}}
		if e = filepath.WalkDir(dir, func(name string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			b, err := os.ReadFile(name)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(dir, name)
			if err != nil {
				return err
			}
			inventory.Files = append(inventory.Files, typedindex.BundleFile{Path: filepath.ToSlash(rel), Bytes: int64(len(b)), Digest: hash(b), Executable: info.Mode()&0111 != 0})
			return nil
		}); e != nil {
			t.Fatal(e)
		}
		slices.SortFunc(inventory.Files, func(a, b typedindex.BundleFile) int { return strings.Compare(a.Path, b.Path) })
		raw := encode(t, inventory)
		config, _ := typedindex.InputConfig(kind, runtime.GOARCH)
		profile, e = typedindex.DecodeProfile(t.Context(), encode(t, typedindex.ProfileDefinition{Schema: typedindex.InputProfileSchema, Name: "neutral-input", Provider: kind, Tools: tools, Config: config, Policy: typedindex.MeasuredPolicy(), BundleDigest: hash(raw), ImageDigest: *inputNativeImage}))
		if e != nil {
			t.Fatal(e)
		}
		return profile, raw, identity(selection.Plan.Targets)
	})
	f.c.observeHost = typedsandbox.ObserveHostScratch
	f.c.config.Socket = "/var/run/docker.sock"
	f.c.config.Image = *inputNativeImage
	return f, profile
}
func nativeInputWrite(t *testing.T, name string, b []byte, mode os.FileMode) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(name), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(name, b, mode); e != nil {
		t.Fatal(e)
	}
}

var inputNativeCleanup = flag.String("typed-input-cleanup", "", "exact scratch name from a stopped neutral rehearsal")

func TestNativeInputScratchCleanup(t *testing.T) {
	if *inputNativeCleanup == "" {
		t.Skip("explicit stopped neutral rehearsal only")
	}
	if os.Geteuid() != 0 {
		t.Fatal("root required")
	}
	o, e := typedsandbox.ObserveHostScratch(t.Context(), *inputNativeCleanup)
	if e != nil || o.Selected == nil || len(o.Names) != 1 || o.Names[0] != *inputNativeCleanup {
		t.Fatal("exact stopped custody", e)
	}
	if e = typedsandbox.CleanupHostScratch(t.Context(), o.Selected.Options); e != nil {
		t.Fatal("authenticated scratch cleanup", e)
	}
	o, e = typedsandbox.ObserveHostScratch(t.Context(), "")
	if e != nil || len(o.Names) != 0 || o.Held {
		t.Fatal("scratch not drained", e)
	}
}

func TestNativeAdditionalInputs(t *testing.T) {
	if *inputNativeTools == "" && *inputNativeImage == "" && *inputNativeMode == "" {
		t.Skip("explicit privileged neutral rehearsal")
	}
	if !filepath.IsAbs(*inputNativeTools) || os.Geteuid() != 0 || (*inputNativeMode != "single" && *inputNativeMode != "workspace" && *inputNativeMode != "import") {
		t.Fatal("explicit root/tool/image/mode admission required")
	}
	endpoint := testServer(t)
	f, profile := nativeInputFixture(t, endpoint, *inputNativeMode)
	ctx := t.Context()
	if e := f.c.Startup(ctx); e != nil {
		t.Fatal("native startup", e)
	}
	lookups := 0
	runtime, e := NewRuntime(f.c, func(context.Context, typedindex.Admission) (string, []byte, error) {
		lookups++
		return f.source, f.raw, nil
	})
	if e != nil {
		t.Fatal(e)
	}
	nativeRun := f.c.native.run
	launches := 0
	f.c.native.run = func(ctx context.Context, o typedsandbox.Options, a typedsandbox.ScratchAuthority) (typedsandbox.Result, error) {
		launches++
		r, e := nativeRun(ctx, o, a)
		if e != nil {
			t.Logf("native refusal phase=%s exit=%d stdout_bytes=%d stderr_bytes=%d", o.Control.Phase, r.ExitCode, len(r.Stdout), len(r.Stderr))
		}
		return r, e
	}
	for _, purpose := range []typedindex.Purpose{typedindex.Publish, typedindex.Canary, typedindex.DryRun} {
		t.Run(string(purpose), func(t *testing.T) {
			chunk := f.chunk
			if purpose != typedindex.Publish {
				intent, e := f.s.GetTypedIndexIntent(ctx, chunk.Repository)
				if e != nil {
					t.Fatal(e)
				}
				source, e := f.s.GetTypedSource(ctx, chunk.Repository)
				if e != nil {
					t.Fatal(e)
				}
				request := typedindex.NewManagedRequest(source, profile, uint64(intent.ProfileEpoch), intent.UniverseDigest, purpose)
				if _, e = f.s.EnqueueTypedIndex(ctx, chunk.Repository, encode(t, request)); e != nil {
					t.Fatal(e)
				}
				schedule, e := f.s.TypedIndexSchedule(ctx, chunk.Repository)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = f.s.EnqueueGenerationSchedule(ctx, schedule); e != nil {
					t.Fatal(e)
				}
				if _, e = f.s.ExpandGenerationSchedule(ctx, chunk.Repository, schedule.Stage, schedule.Generation); e != nil {
					t.Fatal(e)
				}
				next, e := f.s.ClaimGenerationChunk(ctx, store.GenerationResourceTypedIndex, "neutral-input")
				if e != nil || next == nil {
					t.Fatal(e)
				}
				chunk = *next
			}
			inv, e := typedindex.DecodeInventory(ctx, f.raw, profile.Definition().BundleDigest)
			if e != nil {
				t.Fatal(e)
			}
			w, h, e := f.c.observe(ctx)
			if e != nil {
				t.Fatal(e)
			}
			wb, e := typedworkspace.DeriveOwnerBudget(ctx, inv, w.BlockSize)
			if e != nil {
				t.Fatal(e)
			}
			hb, e := typedsandbox.DeriveHostScratchBudget(h.BlockSize)
			if e != nil {
				t.Fatal(e)
			}
			t.Logf("admission total=%d available=%d workspace_budget=%d scratch_budget=%d input_bytes=%d", w.TotalBytes, w.AvailableBytes, wb.Bytes, hb.Bytes, inv.Bytes())
			out, e := f.c.Execute(ctx, chunk, f.source, f.raw)
			if e != nil {
				t.Fatal("native execution refused; worker output withheld")
			}
			if purpose == typedindex.Publish {
				if out.Pointer.Epoch != 1 || out.Check != nil {
					t.Fatal("publication", out)
				}
			} else if out.Pointer.Epoch != 0 || out.Check == nil || out.Check.Purpose != purpose {
				t.Fatal("check published", out)
			}
			// The already executed lease must reuse without input lookup or native launch.
			before := launches
			if e = runtime.Class().Handle(ctx, chunk, generationscheduler.TypedIndexBudget()); e != nil || launches != before || lookups != 0 {
				t.Fatal("reuse", e)
			}
			if e = f.s.CompleteGenerationChunk(ctx, chunk); e != nil {
				t.Fatal(e)
			}
			if e = f.c.AfterSettlement(ctx, out.AttemptDigest); e != nil {
				t.Fatal("settlement", e)
			}
			if _, e = f.s.GetTypedIndexGrowth(ctx); !errors.Is(e, store.ErrNotFound) {
				t.Fatal("growth retained", e)
			}
			if e = f.c.Startup(ctx); e != nil {
				t.Fatal("restart census", e)
			}
			current, e := f.s.ResolveTypedIndexCurrentCustody(ctx, chunk.Repository)
			if e != nil || current.Pointer.Epoch != 1 {
				t.Fatal("prior current", e)
			}
			id := typedworkspace.OwnerIdentity{PlanningDigest: current.PlanningDigest, AttemptDigest: current.AttemptDigest, ChunkIdentity: current.ChunkIdentity, LeaseDigest: current.LeaseDigest, Request: current.Parent.Request()}
			pin, e := typedworkspace.OpenOwnerPublication(ctx, f.c.config.Workspace, id, current.Parent, current.Admission, current.Pointer.Binding.PlanDigest, current.Pointer.RootDigest)
			if e != nil {
				t.Fatal(e)
			}
			defer func() {
				if e := pin.Close(); e != nil {
					t.Error(e)
				}
			}()
			root := pin.Root()
			raw, e := pin.ReadMember(ctx, root.Attempt.Name)
			if e != nil {
				t.Fatal(e)
			}
			var manifest typedindex.AttemptManifest
			if json.Unmarshal(raw, &manifest) != nil || manifest.Input == nil || manifest.Input.Provider != profile.Provider() {
				t.Fatal("input audit missing")
			}
			answerDefinitions, answerReferences := 0, 0
			definitions := map[string]bool{}
			references := []string{}
			for _, member := range manifest.Members {
				raw, e = pin.ReadMember(ctx, member.Name)
				if e != nil {
					t.Fatal(e)
				}
				var index scip.Index
				if proto.Unmarshal(raw, &index) != nil {
					t.Fatal("SCIP")
				}
				for _, doc := range index.Documents {
					for _, o := range doc.Occurrences {
						if strings.Contains(o.Symbol, "Answer.") {
							if o.SymbolRoles&int32(scip.SymbolRole_Definition) != 0 {
								answerDefinitions++
								definitions[o.Symbol] = true
							} else {
								answerReferences++
								references = append(references, o.Symbol)
							}
						}
					}
				}
			}
			for _, symbol := range references {
				if !definitions[symbol] {
					t.Fatal("cross-module symbol has no exact definition", symbol)
				}
			}
			if answerDefinitions == 0 || (*inputNativeMode == "workspace" && answerReferences == 0) {
				t.Fatal("symbol oracle", answerDefinitions, answerReferences)
			}
			t.Logf("native %s: provider=%s members=%d documents=%d definitions=%d references=%d launches=%d", purpose, profile.Provider(), len(manifest.Members), len(manifest.Documents), answerDefinitions, answerReferences, launches)
		})
		if t.Failed() {
			break
		}
	}
}

// This independent fresh fixture exercises queue coordination, scheduler lease
// execution and settlement; TestNativeAdditionalInputs retains the direct proof.
func TestNativeInputCoordinator(t *testing.T) {
	if *inputNativeTools == "" && *inputNativeImage == "" && *inputNativeMode == "" {
		t.Skip("explicit privileged neutral rehearsal")
	}
	if !filepath.IsAbs(*inputNativeTools) || os.Geteuid() != 0 || (*inputNativeMode != "single" && *inputNativeMode != "workspace" && *inputNativeMode != "import") {
		t.Fatal("explicit root/tool/image/mode admission required")
	}
	f, profile := nativeInputFixture(t, testServer(t), *inputNativeMode)
	ctx := t.Context()
	// preparationFixture claims its legacy seed request. Release that unused
	// lease before a distinct managed request goes through the real coordinator.
	if err := f.s.ReleaseGenerationChunk(ctx, f.chunk, "unused neutral fixture seed"); err != nil {
		t.Fatal(err)
	}
	intent, err := f.s.GetTypedIndexIntent(ctx, f.chunk.Repository)
	if err != nil {
		t.Fatal(err)
	}
	source, err := f.s.GetTypedSource(ctx, f.chunk.Repository)
	if err != nil {
		t.Fatal(err)
	}
	request := typedindex.NewManagedRequest(source, profile, uint64(intent.ProfileEpoch), intent.UniverseDigest, typedindex.Publish)
	queued, err := f.s.EnqueueTypedIndex(ctx, f.chunk.Repository, encode(t, request))
	if err != nil || queued.Desired == f.chunk.Generation {
		t.Fatal("fresh managed request", err)
	}
	lookups, launches := 0, 0
	r, err := NewRuntime(f.c, func(_ context.Context, parent typedindex.Admission) (string, []byte, error) {
		lookups++
		if parent.Digest() != queued.Desired {
			return "", nil, errors.New("wrong immutable bundle selection")
		}
		return f.source, f.raw, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Reconcile(ctx); err != nil {
		t.Fatal("native startup", err)
	}
	nativeRun := f.c.native.run
	f.c.native.run = func(ctx context.Context, o typedsandbox.Options, a typedsandbox.ScratchAuthority) (typedsandbox.Result, error) {
		launches++
		return nativeRun(ctx, o, a)
	}

	coordinatorCtx, stopCoordinator := context.WithTimeout(ctx, time.Minute)
	defer stopCoordinator()
	var coordinatorJob store.Job
	var coordinatorErr, reportErr error
	var reports [3]store.JobLifecycleReport
	reportCount := 0
	runner := store.Runner{Store: f.s, Kind: store.JobTypedIndex, Handle: func(ctx context.Context, job store.Job) error {
		coordinatorJob = job
		coordinatorErr = r.Coordinator(ctx, job)
		return coordinatorErr
	}}
	runner.LifecycleReports = func(raw []byte) error {
		if reportCount == len(reports) || json.Unmarshal(raw, &reports[reportCount]) != nil {
			return errors.New("unexpected coordinator lifecycle report")
		}
		reportCount++
		if reports[reportCount-1].Event == "done" {
			stopCoordinator()
		}
		return nil
	}
	runner.LifecycleReportFailure = func(err error) { reportErr = err; stopCoordinator() }
	// Run returns only after its handler and heartbeat join. Assertions stay on
	// this goroutine; the fixture retains all ordinary polling and lease defaults.
	runner.Run(coordinatorCtx)
	if coordinatorErr != nil || reportErr != nil || reportCount != len(reports) {
		t.Fatalf("coordinator: reports=%d handler_failed=%t report_failed=%t", reportCount, coordinatorErr != nil, reportErr != nil)
	}
	for i, event := range []string{"claimed", "started", "done"} {
		if reports[i].Schema != store.JobLifecycleSchema || reports[i].Event != event || reports[i].JobID != coordinatorJob.ID || reports[i].Kind != store.JobTypedIndex || reports[i].Target != f.chunk.Repository || reports[i].Attempt != 1 {
			t.Fatal("coordinator lifecycle identity")
		}
	}
	if reports[2].Outcome != "success" || lookups != 0 || launches != 0 {
		t.Fatal("coordinator executed native work")
	}
	jobs, err := f.s.ListJobsPage(ctx, store.JobPageQuery{Kind: store.JobTypedIndex, Limit: 1})
	if err != nil || jobs == nil || jobs.Next != nil || len(jobs.Jobs) != 1 || jobs.Jobs[0].ID != coordinatorJob.ID || jobs.Jobs[0].Status != store.StatusDone || jobs.Jobs[0].Attempts != 1 || jobs.Jobs[0].FinishedAt == nil {
		t.Fatal("coordinator durable completion", err)
	}
	planned, err := f.s.GetGenerationSchedule(ctx, f.chunk.Repository, store.TypedIndexScheduleStage)
	if err != nil || planned.Generation != queued.Desired || planned.Status != store.GenerationScheduleActive || planned.TotalChunks != 1 || planned.Succeeded != 0 || planned.Failed != 0 {
		t.Fatal("coordinator schedule", err)
	}

	scheduler, err := r.Scheduler(ctx)
	if err != nil {
		t.Fatal(err)
	}
	schedulerCtx, stopScheduler := context.WithTimeout(ctx, 15*time.Minute)
	defer stopScheduler()
	configuration := scheduler.Classes[store.GenerationResourceTypedIndex]
	handle, settle := configuration.Handle, configuration.AfterSettlement
	var selected store.GenerationChunk
	var outcome Outcome
	var executionErr, settlementErr error
	handled, executed, settled := 0, 0, 0
	reused := false
	r.Report = func(out Outcome, err error) { outcome = out; executionErr = err; executed++ }
	configuration.Handle = func(ctx context.Context, chunk store.GenerationChunk, budget generationscheduler.Budget) error {
		handled++
		selected = chunk
		if err := handle(ctx, chunk, budget); err != nil {
			return err
		}
		beforeLookups, beforeLaunches := lookups, launches
		if err := handle(ctx, chunk, budget); err != nil {
			return err
		}
		reused = lookups == beforeLookups && launches == beforeLaunches
		if !reused {
			return errors.New("same lease repeated native execution")
		}
		return nil
	}
	configuration.AfterSettlement = func(ctx context.Context, chunk store.GenerationChunk) error {
		settlementErr = settle(ctx, chunk)
		settled++
		stopScheduler()
		return settlementErr
	}
	scheduler.Classes[store.GenerationResourceTypedIndex] = configuration
	schedulerErrors := make(chan error, 1)
	scheduler.Report = func(err error) {
		select {
		case schedulerErrors <- err:
		default:
		}
	}
	// Scheduler.Run cancels and joins every planner, reaper, worker and heartbeat;
	// its nil return alone does not establish successful durable settlement.
	if err = scheduler.Run(schedulerCtx); err != nil {
		t.Fatal("scheduler", err)
	}
	select {
	case <-schedulerErrors:
		t.Fatal("scheduler reported failure")
	default:
	}
	if handled != 1 || executed != 1 || settled != 1 || executionErr != nil || settlementErr != nil || !reused || lookups != 1 || launches != 2 || selected.Generation != queued.Desired || selected.LeaseToken == f.chunk.LeaseToken || outcome.Pointer.Epoch != 1 || outcome.Check != nil {
		t.Fatalf("native scheduler: handled=%d executed=%d settled=%d lookups=%d launches=%d reused=%t execution_failed=%t settlement_failed=%t", handled, executed, settled, lookups, launches, reused, executionErr != nil, settlementErr != nil)
	}
	before, err := f.s.GetGenerationSchedule(ctx, f.chunk.Repository, store.TypedIndexScheduleStage)
	if err != nil || before.Status != store.GenerationScheduleSettled || before.Succeeded != 1 || before.Failed != 0 || before.Pending != 0 || before.Running != 0 {
		t.Fatal("native schedule not successfully settled", err)
	}
	status, err := f.s.GetTypedIndexStatus(ctx, f.chunk.Repository)
	if err != nil || status.Desired != outcome.Pointer.Binding.RequestDigest || status.Stage != store.TypedComplete || status.States != [5]string{"complete", "complete", "complete", "complete", "complete"} || status.Current == nil || *status.Current != outcome.Pointer || status.Stale || status.Canceled || status.RestoreRequired {
		t.Fatal("native publication status", err)
	}
	current, err := f.s.ResolveTypedIndexCurrentCustody(ctx, f.chunk.Repository)
	if err != nil || current.PlanningDigest != queued.Desired || current.Parent.Request() != request || current.Admission.Digest() != status.Desired || current.Pointer != outcome.Pointer {
		t.Fatal("native parent/execution continuity", err)
	}
	if _, err = f.s.GetTypedIndexGrowth(ctx); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("settlement stranded growth", err)
	}
	if err = r.Coordinator(ctx, coordinatorJob); err != nil {
		t.Fatal("published duplicate coordinator", err)
	}
	after, err := f.s.GetGenerationSchedule(ctx, f.chunk.Repository, store.TypedIndexScheduleStage)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("published duplicate changed schedule", err)
	}
	next, err := f.s.ClaimGenerationChunk(ctx, store.GenerationResourceTypedIndex, "native-published-warm")
	if err != nil && !errors.Is(err, store.ErrNotFound) || next != nil || lookups != 1 || launches != 2 {
		t.Fatal("published duplicate replay", err)
	}
	if err = r.Reconcile(ctx); err != nil {
		t.Fatal("published restart census", err)
	}
	host, err := typedsandbox.ObserveHostScratch(ctx, "")
	if err != nil || host.Held || len(host.Names) != 0 {
		t.Fatal("native scratch not drained", err)
	}
	t.Logf("native coordinator: provider=%s coordinator_jobs=1 succeeded_chunks=1 lookups=%d launches=%d same_lease_reused=%t", profile.Provider(), lookups, launches, reused)
	t.Logf("native identity: source_commit=%s profile=%s inventory=%s planning=%s execution=%s root=%s", source.Commit, profile.Digest(), profile.Definition().BundleDigest, custody.PlanningDigest, custody.Admission.Digest(), custody.Pointer.RootDigest)
}
