//go:build linux

package typedexecutor

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/typedbazel/launcher"
	"github.com/bmeddeb/phebs/internal/typedbazel/planner"
	"github.com/bmeddeb/phebs/internal/typedbazel/provider"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/bmeddeb/phebs/internal/typedsandbox"
	"github.com/bmeddeb/phebs/internal/typedworkspace"
	"github.com/scip-code/scip/bindings/go/scip"
	"golang.org/x/sys/unix"
	"google.golang.org/protobuf/proto"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func hash(b []byte) string  { return testHash(b) }
func identity(v any) string { raw, _ := json.Marshal(v); return hash(raw) }
func rawHash(v any) string  { return strings.TrimPrefix(identity(v), "sha256:") }
func sealRaw(p *planner.Plan) {
	targets := slices.Clone(p.Targets)
	for j := range targets {
		targets[j].Units = nil
	}
	p.UniverseSHA256 = rawHash(targets)
	p.MappingSHA256 = rawHash(struct {
		Targets []planner.Target
		Units   []planner.Unit
		SDKs    []planner.SDKPlan
	}{p.Targets, p.Units, p.SDKs})
	p.DocumentsSHA256 = rawHash(struct {
		Documents []planner.Document
		SDKs      []planner.SDKPlan
	}{p.Documents, p.SDKs})
}
func callerEnvironment(p launcher.Prepared, slot, hash string) []string {
	env := launcher.BazelEnvironment()
	env[0] = "PATH=/inputs/tools/go/bin:/inputs/tools/bin"
	// Only these two sealed mode values cross from the driver profile to callers.
	for _, item := range p.Invocation().Environment {
		if strings.HasPrefix(item, "CGO_ENABLED=") || strings.HasPrefix(item, "GOTAGS=") {
			env = append(env, item)
		}
	}
	return append(env, "GOPACKAGESDRIVER="+provider.NativeAdapterPath, nativeSlotEnv+"="+slot, nativePlanEnv+"="+hash, "PWD="+launcher.Workspace)
}

func callerRequest(env []string) []byte {
	data, _ := json.Marshal(struct {
		Mode       int               `json:"mode"`
		Env        []string          `json:"env"`
		BuildFlags []string          `json:"build_flags"`
		Tests      bool              `json:"tests"`
		Overlay    map[string][]byte `json:"overlay"`
	}{Mode: launcher.CompatibilityMode, Env: env})
	return data
}

const nativeSlotEnv = "PHEBS_T451B_NATIVE_SLOT"
const nativePlanEnv = "PHEBS_T451B_NATIVE_PLAN_SHA256"

type nativeControl struct {
	Version   string                 `json:"version"`
	Slot      string                 `json:"slot"`
	Selection provider.Selection     `json:"selection"`
	Plan      planner.Plan           `json:"plan"`
	Roots     []planner.Configured   `json:"roots"`
	GoFiles   launcher.NativeGoFiles `json:"go_files"`
}

func nativeControlBytes(plan planner.Plan, roots []planner.Configured, matches launcher.NativeGoFiles, selection provider.Selection, slot string) ([]byte, error) {
	b, err := json.Marshal(nativeControl{"phebs-bazel-client-plan-v1", slot, selection, plan, roots, matches})
	return append(b, '\n'), err
}

type loadFact struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	Types    bool   `json:"types"`
	Syntax   int    `json:"syntax"`
	TypeInfo bool   `json:"type_info"`
	Errors   int    `json:"errors"`
	IllTyped bool   `json:"ill_typed"`
}
type loadReport struct {
	Mode     int        `json:"mode"`
	Roots    []loadFact `json:"roots"`
	Packages []loadFact `json:"packages"`
}

func scipArguments(s provider.Selection, patterns []string) []string {
	return append([]string{"index", "--module-root=" + launcher.Workspace, "--module-path=" + s.Module, "--module-version=" + s.Source.Commit, "--repository-remote=" + s.Remote, "--go-version=go1.25.0", "--skip-tests", "--skip-implementations", "--output=/scratch/t451b-native-index.scip"}, patterns...)
}
func verifiedNeutralLeg(t *testing.T, p planner.Plan, roots []planner.Configured, matches launcher.NativeGoFiles, s provider.Selection, slot string) provider.LegEvidence {
	t.Helper()
	prepared, e := launcher.PrepareNativeCompatibility(p, roots, slot, matches)
	if e != nil {
		t.Fatal(e)
	}
	control, e := nativeControlBytes(p, roots, matches, s, slot)
	if e != nil {
		t.Fatal(e)
	}
	rootNames := []string{}
	packages := []map[string]any{}
	exports := []launcher.ExportArtifact{}
	probe := loadReport{Mode: launcher.CompatibilityMode, Roots: []loadFact{}, Packages: []loadFact{}}
	for _, u := range p.Units {
		files := []string{}
		for _, id := range u.CompiledGoFiles {
			for _, d := range p.Documents {
				if d.ID == id {
					base := launcher.Workspace
					if d.Kind == "generated" {
						base = launcher.ExecRoot
					}
					files = append(files, base+"/"+d.ExecPath)
				}
			}
		}
		slices.Sort(files)
		_, archive, _ := strings.Cut(u.Variant, "|")
		export := launcher.ExecRoot + "/" + archive
		packages = append(packages, map[string]any{"ID": u.ArchiveLabel, "Name": u.PackageName, "PkgPath": u.ImportPath, "GoFiles": files, "CompiledGoFiles": files, "OtherFiles": []string{}, "ExportFile": export, "Imports": map[string]string{}})
		exports = append(exports, launcher.ExportArtifact{Path: export, Bytes: 1, SHA256: strings.TrimPrefix(hash([]byte("x")), "sha256:")})
		fact := loadFact{ID: u.ArchiveLabel, Path: u.ImportPath, Types: true, Syntax: len(files), TypeInfo: true}
		probe.Packages = append(probe.Packages, fact)
		if slices.Contains(roots, u.Owner) {
			rootNames = append(rootNames, u.ArchiveLabel)
			probe.Roots = append(probe.Roots, fact)
		}
	}
	raw, e := json.Marshal(map[string]any{"NotHandled": false, "Roots": rootNames, "Packages": packages})
	if e != nil {
		t.Fatal(e)
	}
	var adapted map[string]json.RawMessage
	if e = json.Unmarshal(raw, &adapted); e != nil {
		t.Fatal(e)
	}
	adapted["Compiler"], adapted["Arch"], adapted["GoVersion"] = json.RawMessage(`"gc"`), json.RawMessage(strconv.Quote(runtime.GOARCH)), json.RawMessage(`25`)
	response, e := json.Marshal(adapted)
	if e != nil {
		t.Fatal(e)
	}
	env := callerEnvironment(prepared, slot, hash(control))
	request := callerRequest(env)
	call := provider.CallEvidence{Slot: slot, PlanSHA256: hash(control), Argv: append([]string{provider.NativeAdapterPath}, prepared.Invocation().Arguments...), Environment: env, Directory: launcher.Workspace, Request: request, RequestSHA256: hash(request), Launcher: prepared.Invocation(), LauncherSHA256: prepared.Digest(), Result: launcher.CompatibilityResult{DriverResponse: raw, Response: response, Exports: exports}, DriverResponseSHA256: hash(raw), ResponseSHA256: hash(response)}
	argv := append([]string{provider.NativeProbePath}, prepared.Invocation().Arguments...)
	stdout := []byte{}
	if slot == "scip" {
		argv = append([]string{provider.SCIPPath}, scipArguments(s, prepared.Invocation().Arguments)...)
	} else {
		stdout, e = json.Marshal(probe)
		if e != nil {
			t.Fatal(e)
		}
	}
	return provider.LegEvidence{Slot: slot, ClientArgv: argv, Environment: env, WallNanoseconds: 1, Stdout: stdout, Stderr: []byte{}, Call: call}
}

// This neutral wire builder deliberately exercises the production returned-
// evidence decoder/finalizer. It does not claim any Bazel/driver/native run.
type wireFixture struct {
	raw       planner.Plan
	selection provider.Selection
	matches   launcher.NativeGoFiles
	unit      typedindex.PackageUnitID
	profile   typedindex.Profile
}

func completeFixture(t *testing.T, endpoint, database string) (fixture, wireFixture) {
	t.Helper()
	var w wireFixture
	f := preparationFixture(t, endpoint, database, func(s *store.Surreal, repo, dir string) (typedindex.Profile, []byte, string) {
		if e := os.Remove(filepath.Join(dir, "data")); e != nil {
			t.Fatal(e)
		}
		source, e := s.GetTypedSource(t.Context(), repo)
		if e != nil {
			t.Fatal(e)
		}
		uid := strings.Repeat("a", 64)
		owner := planner.Configured{Label: "@@//lib:lib", Configuration: strings.Repeat("b", 64)}
		mode := planner.GoMode{GOOS: "linux", GOARCH: "arm64"}
		w.raw = planner.Plan{Version: planner.PlanV2, Targets: []planner.Target{{Configured: owner, Kind: "go_library", Inputs: []planner.Configured{}, Units: []string{uid}}}, Units: []planner.Unit{{ID: uid, Owner: owner, ArchiveLabel: owner.Label, Variant: "lib|bazel-out/cfg/bin/lib/lib.x", ImportPath: "example.test/lib", PackageName: "lib", GoFiles: []string{"doc"}, CompiledGoFiles: []string{"doc"}, Imports: []planner.UnitImport{}, SDK: "sdk", Mode: mode}}, Documents: []planner.Document{{ID: "doc", Kind: "source", Path: "lib/lib.go", ExecPath: "lib/lib.go", SHA256: strings.TrimPrefix(hash([]byte("package lib\n")), "sha256:"), Bytes: 12}}, SDKs: []planner.SDKPlan{{ID: "sdk", Owner: owner, SDKProjection: planner.SDKProjection{Version: "1.25.0", Root: "external/rules_go++go_sdk+go_default_sdk", Mode: mode}}}}
		sealRaw(&w.raw)
		w.unit, _ = typedindex.NewPackageUnitID("sha256:" + uid)
		w.selection = provider.Selection{Schema: provider.SelectionSchema, Source: source, Roots: []string{"//lib:lib"}, Targets: []typedindex.PlannedTarget{{ID: identity(owner), Dependencies: []string{}, Units: []typedindex.PackageUnitID{w.unit}}}, Module: "example.test/lib", Remote: "https://example.test/repo"}
		files := map[string][]byte{"source/lib/lib.go": []byte("package lib\n"), provider.SelectionFile: append(encode(t, w.selection), '\n'), typedindex.ManagedHelperFile: []byte("helper"), typedindex.HostToolsFile: encode(t, typedindex.HostToolsDefinition{Schema: typedindex.HostToolsSchema, MkfsDigest: hash([]byte("formatter"))})}
		inventory := typedindex.InventoryDefinition{Schema: typedindex.InventorySchema}
		names := []string{}
		for name := range files {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			b := files[name]
			if e = os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0700); e != nil {
				t.Fatal(e)
			}
			mode := os.FileMode(0600)
			executable := name == typedindex.ManagedHelperFile
			if executable {
				mode = 0700
			}
			if e = os.WriteFile(filepath.Join(dir, name), b, mode); e != nil {
				t.Fatal(e)
			}
			inventory.Files = append(inventory.Files, typedindex.BundleFile{Path: name, Bytes: int64(len(b)), Digest: hash(b), Executable: executable})
		}
		raw := append([]byte(" "), append(encode(t, inventory), '\n')...)
		helper := typedindex.Tool{Version: "owned", Digest: hash([]byte("helper"))}
		tools := typedindex.Tools{Bazel: typedindex.Tool{Version: "9.0.0", Digest: provider.BazelDigest}, RulesGo: typedindex.Tool{Version: "0.59.0", Digest: "sha256:68af54cb97fbdee5e5e8fe8d210d15a518f9d62abfd71620c3eaff3b26a5ff86"}, Go: typedindex.Tool{Version: "1.25.0", Digest: provider.GoDigest}, Driver: typedindex.Tool{Version: "0.59.0", Digest: "sha256:" + launcher.NativeDriverSHA256}, Indexer: typedindex.Tool{Version: "0.2.7", Digest: provider.SCIPDigest}, Planner: helper, Launcher: helper}
		w.profile, e = typedindex.DecodeProfile(t.Context(), encode(t, typedindex.ProfileDefinition{Schema: typedindex.ProfileSchema, Name: "neutral", Provider: typedindex.ProviderID, Tools: tools, Config: typedindex.ReducedConfig(), Policy: typedindex.MeasuredPolicy(), BundleDigest: hash(raw), ImageDigest: hash([]byte("image"))}))
		if e != nil {
			t.Fatal(e)
		}
		w.matches = launcher.NativeGoFiles{Version: "phebs-t451b-native-go-files-v1", MappingSHA256: w.raw.MappingSHA256, DocumentsSHA256: w.raw.DocumentsSHA256, Packages: []launcher.NativeGoFilesPackage{{ID: owner.Label, GoFiles: []string{launcher.Workspace + "/lib/lib.go"}}}}
		return w.profile, raw, identity(w.selection.Targets)
	})
	if e := f.c.Startup(t.Context()); e != nil {
		t.Fatal(e)
	}
	f.c.config.Socket = "/run/docker.sock"
	f.c.config.Image = w.profile.Definition().ImageDigest
	return f, w
}
func (w wireFixture) result(t *testing.T, f fixture, o typedsandbox.Options) []byte {
	t.Helper()
	work, e := f.s.BeginTypedIndex(t.Context(), f.chunk)
	if e != nil {
		t.Fatal(e)
	}
	plan, e := typedindex.SealPackagePlan(t.Context(), work.Parent, typedindex.PackagePlanDefinition{Schema: typedindex.PackagePlanSchema, ParentRequestDigest: work.Parent.Digest(), Targets: w.selection.Targets, Units: []typedindex.PlannedUnit{{ID: w.unit, Imports: []typedindex.PackageUnitID{}, Documents: []string{"lib/lib.go"}}}, Documents: []typedindex.PlannedDocument{{Member: "main", Path: "lib/lib.go", Unit: w.unit, Bytes: 12, Digest: hash([]byte("package lib\n"))}}})
	if e != nil {
		t.Fatal(e)
	}
	r := provider.Result{Schema: "phebs-bazel-worker-result-v1", RequestDigest: o.Control.RequestDigest, Phase: typedindex.Action(o.Control.Phase), Plan: plan.Bytes(), RawPlan: w.raw, GoFiles: w.matches, Documents: []provider.DocumentOutcome{{Document: "doc", Unit: w.unit, RawPath: "lib/lib.go", Path: "lib/lib.go", State: "included"}}, Units: []typedindex.UnitOutcome{{Unit: w.unit, State: typedindex.UnitComplete}}}
	if r.Phase == typedindex.Execute {
		for _, slot := range []string{"load", "scip"} {
			r.Legs = append(r.Legs, verifiedNeutralLeg(t, w.raw, []planner.Configured{w.raw.Targets[0].Configured}, w.matches, w.selection, slot))
		}
		r.SCIP, e = proto.Marshal(&scip.Index{Metadata: &scip.Metadata{ProjectRoot: "file:///scratch/workspace", TextDocumentEncoding: scip.TextEncoding_UTF8, ToolInfo: &scip.ToolInfo{Name: "scip-go", Version: "0.2.7", Arguments: scipArguments(w.selection, []string{"example.test/lib"})}}, Documents: []*scip.Document{{Language: "go", RelativePath: "lib/lib.go"}}})
		if e != nil {
			t.Fatal(e)
		}
		r.SCIPSHA256 = hash(r.SCIP)
		r.Generated = map[string][]byte{}
	}
	return append(encode(t, r), '\n')
}

// Only native execution is substituted. Store, custody, control install/open,
// common-clock allowance, returned evidence, bundle verification and publication
// remain production paths. Fake completion is confined to this private seam.
func installNeutralNative(t *testing.T, f fixture, w wireFixture, fail string) (*[]string, *int) {
	t.Helper()
	events := []string{}
	begins := 0
	var receipt *typedsandbox.HostScratchReceipt
	var original typedsandbox.Allowance
	actualObserve := f.c.observeHost
	f.c.observeHost = func(ctx context.Context, name string) (typedsandbox.HostObservation, error) {
		o, e := actualObserve(ctx, name)
		if receipt != nil {
			root, _ := typedsandbox.HostScratchRootName(receipt.Options.RequestDigest, receipt.Options.AttemptDigest)
			o.Names = []string{root}
			if name == root {
				o.Selected = &typedsandbox.HostJournalObservation{Options: receipt.Options}
			}
		}
		return o, e
	}
	add := func(s string) error {
		events = append(events, s)
		if fail == s {
			return errors.New("neutral failure: " + s)
		}
		return nil
	}
	f.c.native.begin = func(ctx context.Context, p, a string) (typedsandbox.Allowance, error) {
		begins++
		return typedsandbox.BeginAllowance(ctx, p, a)
	}
	f.c.native.prepare = func(ctx context.Context, o typedsandbox.HostScratchOptions, _ *lifecycle.Gate) (typedsandbox.HostScratchReceipt, error) {
		if e := ctx.Err(); e != nil {
			return typedsandbox.HostScratchReceipt{}, e
		}
		if receipt != nil {
			t.Fatal("scratch reused")
		}
		if e := add("prepare"); e != nil {
			return typedsandbox.HostScratchReceipt{}, e
		}
		root, _ := typedsandbox.HostScratchRootName(o.RequestDigest, o.AttemptDigest)
		r := typedsandbox.HostScratchReceipt{Schema: "phebs-typed-host-scratch-v3", Options: o, ObservedDirectIO: true, Authority: typedsandbox.ScratchAuthority{Source: typedsandbox.HostScratchBase + "/" + root + "/scratch", DeviceMajor: 7, DeviceMinor: 3, BlockSize: 4096, Blocks: 100, Inodes: typedsandbox.ScratchInodes, ImageBytes: typedsandbox.ScratchBytes / 4096 * 4096}}
		receipt = &r
		return r, nil
	}
	f.c.native.verify = func(context.Context, typedsandbox.HostScratchOptions) (typedsandbox.HostScratchReceipt, error) {
		return *receipt, add("verify")
	}
	cleanups := 0
	f.c.native.cleanup = func(context.Context, typedsandbox.HostScratchOptions) error {
		cleanups++
		// Overage remains a refusal even after the exact-owned image is gone.
		receipt = nil
		if e := add("cleanup"); e != nil {
			return errors.Join(typedsandbox.ErrCustody, e)
		}
		if cleanups == 2 && fail == "cleanup-execute" {
			return typedsandbox.ErrCustody
		}
		return nil
	}
	f.c.native.quiescent = func(context.Context, typedsandbox.RecoveryOptions) error { return add("quiescent") }
	f.c.native.run = func(ctx context.Context, o typedsandbox.Options, _ typedsandbox.ScratchAuthority) (typedsandbox.Result, error) {
		// The global lifecycle guard is free while the exact attempt pin is held.
		release, e := f.c.config.Acquire(ctx)
		if e != nil {
			t.Fatal(e)
		}
		release()
		pinCtx, cancel := context.WithTimeout(ctx, 15*time.Millisecond)
		defer cancel()
		release, e = typedworkspace.AcquirePublicationMutation(pinCtx, filepath.Dir(o.Controls))
		if e == nil {
			release()
			t.Fatal("attempt unpinned")
		}
		if o.Control.Phase == typedsandbox.ControlPlan {
			original = o.Allowance
		} else if o.Allowance.Start != original.Start || o.Allowance.Deadline != original.Deadline {
			t.Fatal("wall refreshed")
		}
		if e = add("run-" + o.Control.Phase); e != nil {
			return typedsandbox.Result{ExitCode: 124, Removed: true, StopReason: "wall_limit"}, e
		}
		return typedsandbox.Result{Stdout: w.result(t, f, o), Removed: true}, nil
	}
	f.c.native.complete = func(_ typedsandbox.Allowance, c typedsandbox.ControlIdentity, _ typedsandbox.Result) error {
		return add("complete-" + c.Phase)
	}
	f.c.native.advance = func(a typedsandbox.Allowance, r typedsandbox.Result) (typedsandbox.Allowance, error) {
		a.WorkerBytesUsed = int64(len(r.Stdout))
		a.WireBytesUsed = a.WorkerBytesUsed
		return a, add("advance")
	}
	return &events, &begins
}

func TestTypedExecutorCompleteTurn(t *testing.T) {
	endpoint := testServer(t)
	for n, fail := range []string{"", "prepare", "verify", "run-plan", "complete-plan", "advance", "run-execute", "complete-execute", "cleanup", "cleanup-execute", "quiescent"} {
		t.Run("prefix-"+fail, func(t *testing.T) {
			f, w := completeFixture(t, endpoint, "turn"+string(rune('a'+n)))
			events, begins := installNeutralNative(t, f, w, fail)
			out, e := f.c.Execute(t.Context(), f.chunk, f.source, f.raw)
			if fail != "" {
				if e == nil || out.Pointer.Epoch != 0 {
					t.Fatal("failure published", out, e)
				}
				if *begins != 1 {
					t.Fatal("allowance count", *begins)
				}
				if fail == "cleanup" || fail == "cleanup-execute" {
					if !errors.Is(e, typedsandbox.ErrCustody) {
						t.Fatal("allocation refusal lost", e)
					}
					if fail == "cleanup" && slices.Contains(*events, "advance") {
						t.Fatal("plan cleanup advanced")
					}
					if fail == "cleanup-execute" && !slices.Contains(*events, "complete-execute") {
						t.Fatal("execute cleanup not exercised")
					}
				}
				inspected, inspectErr := f.s.InspectTypedIndexAttempt(t.Context(), out.AttemptDigest)
				wantReason := typedindex.Containment
				if fail == "run-plan" || fail == "run-execute" {
					wantReason = typedindex.WallLimit
				}
				if inspectErr != nil || inspected.Reason != wantReason {
					t.Fatal("live failure misclassified", inspected.Reason, inspectErr)
				}

				if fail == "run-plan" && out.Reports[0].StopReason != "wall_limit" {
					t.Fatal("diagnostic lost")
				}
				_, _ = f.c.Execute(t.Context(), f.chunk, f.source, f.raw)
				if *begins != 1 {
					t.Fatal("interrupted attempt replayed")
				}
				return
			}
			if e != nil || out.Pointer.Epoch != 1 {
				t.Fatal("complete turn", e, *events)
			}
			want := []string{"prepare", "verify", "run-plan", "complete-plan", "quiescent", "cleanup", "advance", "prepare", "verify", "run-execute", "complete-execute", "quiescent", "cleanup"}
			if !slices.Equal(*events, want) {
				t.Fatal(*events)
			}
			if e = f.c.AfterSettlement(t.Context(), out.AttemptDigest); e == nil {
				t.Fatal("running lease released")
			}
			if e = f.s.CompleteGenerationChunk(t.Context(), f.chunk); e != nil {
				t.Fatal(e)
			}
			if e = f.c.AfterSettlement(t.Context(), out.AttemptDigest); e != nil {
				t.Fatal("settled release", e)
			}
			if _, e = f.s.GetTypedIndexGrowth(t.Context()); !errors.Is(e, store.ErrNotFound) {
				t.Fatal("promise retained", e)
			}
			if e = f.c.Startup(t.Context()); e != nil {
				t.Fatal("restart", e)
			}
			if *begins != 1 {
				t.Fatal("recovery minted allowance")
			}
		})
	}
}

func TestTypedExecutorRecoveryWithoutReplay(t *testing.T) {
	endpoint := testServer(t)
	for n, kind := range []string{"planning-crash", "source-changed", "profile-changed", "deleted", "re-added", "unknown-owner", "missing-owner", "live-pin", "native-held", "base-changed"} {
		t.Run(kind, func(t *testing.T) {
			f, w := completeFixture(t, endpoint, "recover"+string(rune('a'+n)))
			events, begins := installNeutralNative(t, f, w, "")
			prepared, e := f.c.Prepare(t.Context(), f.chunk, f.source, f.raw)
			if e != nil {
				t.Fatal(e)
			}
			if e = f.s.AdvanceTypedIndex(t.Context(), f.chunk, store.TypedPreflight); e != nil {
				t.Fatal(e)
			}
			if e = f.s.FailGenerationChunk(t.Context(), f.chunk, "interrupted fixture"); e != nil {
				t.Fatal(e)
			}
			path := filepath.Join(f.c.config.Workspace, prepared.Manifest.Identity.RelativeName())
			restore := func() {}
			held := false
			switch kind {
			case "source-changed":
				e = f.s.SetRepoIndexed(t.Context(), f.chunk.Repository, strings.Repeat("e", 40), time.Now())
			case "profile-changed":
				def := w.profile.Definition()
				def.Name = "new-profile"
				p, pe := typedindex.DecodeProfile(t.Context(), encode(t, def))
				if pe != nil {
					t.Fatal(pe)
				}
				_, e = f.s.InstallTypedProfile(t.Context(), f.chunk.Repository, p, identity(w.selection.Targets), 1)
			case "deleted", "re-added":
				e = f.s.DeleteRepo(t.Context(), f.chunk.Repository)
				if e == nil && kind == "re-added" {
					e = f.s.UpsertRepo(t.Context(), store.Repo{Name: f.chunk.Repository})
					if e == nil {
						e = f.s.SetRepoIndexed(t.Context(), f.chunk.Repository, strings.Repeat("d", 40), time.Now())
					}
				}
			case "unknown-owner":
				held = true
				unknown := filepath.Join(f.c.config.Workspace, strings.Repeat("f", 64))
				e = os.Mkdir(unknown, 0700)
				restore = func() {
					if e := os.Remove(unknown); e != nil {
						t.Fatal(e)
					}
				}
			case "missing-owner":
				held = true
				saved := filepath.Join(t.TempDir(), "held")
				e = os.Rename(path, saved)
				restore = func() {
					if e := os.Rename(saved, path); e != nil {
						t.Fatal(e)
					}
				}
			case "live-pin":
				held = true
				lock, le := os.Open(filepath.Join(path, ".phebs-index-publication.lock"))
				if le != nil {
					t.Fatal(le)
				}
				e = unix.Flock(int(lock.Fd()), unix.LOCK_SH)
				restore = func() { _ = unix.Flock(int(lock.Fd()), unix.LOCK_UN); _ = lock.Close() }
			case "native-held":
				held = true
				original := f.c.native.quiescent
				f.c.native.quiescent = func(context.Context, typedsandbox.RecoveryOptions) error { return ErrHeld }
				restore = func() { f.c.native.quiescent = original }
			case "base-changed":
				held = true
				original := f.c.workspace
				f.c.workspace.Inode++
				restore = func() { f.c.workspace = original }
			}
			if e != nil {
				t.Fatal(e)
			}
			if kind == "planning-crash" {
				fresh, ne := New(f.c.config)
				if ne != nil {
					t.Fatal(ne)
				}
				fresh.observeHost = f.c.observeHost
				fresh.native = f.c.native
				f.c = fresh
			}
			e = f.c.Recover(t.Context())
			if held {
				if e == nil {
					t.Fatal("ambiguous recovery accepted")
				}
				if _, e = f.s.GetTypedIndexGrowth(t.Context()); e != nil {
					t.Fatal("held promise lost", e)
				}
				restore()
				e = f.c.Recover(t.Context())
			}
			if e != nil {
				t.Fatal("recovery", e, *events)
			}
			if *begins != 0 {
				t.Fatal("restart minted allowance")
			}
			if _, e = f.s.GetTypedIndexGrowth(t.Context()); !errors.Is(e, store.ErrNotFound) {
				t.Fatal("settled promise retained", e)
			}
			if e = f.c.Startup(t.Context()); e != nil {
				t.Fatal("restart ready", e)
			}
			if _, e = f.c.Execute(t.Context(), f.chunk, f.source, f.raw); e == nil {
				t.Fatal("old lease replayed")
			}
			if *begins != 0 {
				t.Fatal("old wall reset")
			}
		})
	}
}

func TestTypedExecutorCleanupNeedsGuard(t *testing.T) {
	endpoint := testServer(t)
	f, w := completeFixture(t, endpoint, "guard")
	events, _ := installNeutralNative(t, f, w, "")
	acquire := f.c.config.Acquire
	run := f.c.native.run
	denied := false
	f.c.config.Acquire = func(ctx context.Context) (func(), error) {
		if denied {
			return nil, ErrHeld
		}
		return acquire(ctx)
	}
	f.c.native.run = func(ctx context.Context, o typedsandbox.Options, a typedsandbox.ScratchAuthority) (typedsandbox.Result, error) {
		r, e := run(ctx, o, a)
		denied = true
		return r, e
	}
	out, e := f.c.Execute(t.Context(), f.chunk, f.source, f.raw)
	if e == nil || out.Pointer.Epoch != 0 {
		t.Fatal("lost guard accepted")
	}
	if slices.Contains(*events, "quiescent") || slices.Contains(*events, "cleanup") {
		t.Fatal("mutation without guard", *events)
	}
	attempt, e := f.s.InspectTypedIndexAttempt(t.Context(), out.AttemptDigest)
	if e != nil || attempt.Reason != "" {
		t.Fatal("failure state mutated without guard", e)
	}
	denied = false
	if e = f.s.FailGenerationChunk(t.Context(), f.chunk, "guard loss"); e != nil {
		t.Fatal(e)
	}
	if e = f.c.Recover(t.Context()); e != nil {
		t.Fatal("restored guard recovery", e)
	}
}

func TestTypedExecutorAllowanceCoversPreparation(t *testing.T) {
	endpoint := testServer(t)
	for n, external := range []bool{false, true} {
		t.Run([]string{"wall", "caller"}[n], func(t *testing.T) {
			f, w := completeFixture(t, endpoint, "clock"+string(rune('a'+n)))
			_, begins := installNeutralNative(t, f, w, "")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			begin := f.c.native.begin
			f.c.native.begin = func(ctx context.Context, p, a string) (typedsandbox.Allowance, error) {
				v, e := begin(ctx, p, a)
				if !external {
					v.Start -= int64(typedsandbox.WallLimit - 100*time.Millisecond)
					v.Deadline -= int64(typedsandbox.WallLimit - 100*time.Millisecond)
				}
				return v, e
			}
			f.c.native.prepare = func(ctx context.Context, _ typedsandbox.HostScratchOptions, _ *lifecycle.Gate) (typedsandbox.HostScratchReceipt, error) {
				if external {
					cancel()
				}
				<-ctx.Done()
				return typedsandbox.HostScratchReceipt{}, ctx.Err()
			}
			out, e := f.c.Execute(ctx, f.chunk, f.source, f.raw)
			if e == nil || *begins != 1 {
				t.Fatal("expired work continued", e)
			}
			a, e := f.s.InspectTypedIndexAttempt(t.Context(), out.AttemptDigest)
			want := typedindex.WallLimit
			if external {
				want = typedindex.Canceled
			}
			if e != nil || a.Reason != want {
				t.Fatal("classification", a.Reason, e)
			}
		})
	}
}

func TestTypedExecutorEarlyCrashCustodyHeld(t *testing.T) {
	endpoint := testServer(t)
	for n, kind := range []string{"holder-before-owner", "owner-before-inputs"} {
		t.Run(kind, func(t *testing.T) {
			f, w := completeFixture(t, endpoint, "early"+string(rune('a'+n)))
			events, begins := installNeutralNative(t, f, w, "")
			if kind == "owner-before-inputs" {
				if _, e := f.c.Prepare(t.Context(), f.chunk, "/missing-neutral-source", f.raw); e == nil {
					t.Fatal("missing source accepted")
				}
			} else {
				if _, e := f.s.BeginTypedIndex(t.Context(), f.chunk); e != nil {
					t.Fatal(e)
				}
				inv, e := typedindex.DecodeInventory(t.Context(), f.raw, hash(f.raw))
				if e != nil {
					t.Fatal(e)
				}
				wb, e := typedworkspace.DeriveOwnerBudget(t.Context(), inv, f.c.workspace.BlockSize)
				if e != nil {
					t.Fatal(e)
				}
				hb, e := typedsandbox.DeriveHostScratchBudget(f.c.host.BlockSize)
				if e != nil {
					t.Fatal(e)
				}
				spec, e := f.c.admit(t.Context(), f.c.workspace, f.c.host, wb, hb)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = f.s.AcquireTypedIndexGrowth(t.Context(), f.chunk, spec); e != nil {
					t.Fatal(e)
				}
			}
			if e := f.s.FailGenerationChunk(t.Context(), f.chunk, "incomplete custody"); e != nil {
				t.Fatal(e)
			}
			if e := f.c.Recover(t.Context()); e == nil {
				t.Fatal("unproven physical absence released")
			}
			if _, e := f.s.GetTypedIndexGrowth(t.Context()); e != nil {
				t.Fatal("promise lost", e)
			}
			if *begins != 0 || len(*events) != 0 {
				t.Fatal("early custody started native work", *events)
			}
		})
	}
}

// checkedFixture retains a real prior publication, then selects an exact managed
// nonpublishing successor. Only native operations use the existing private seam.
func checkedFixture(t *testing.T, endpoint, database string, purpose typedindex.Purpose) (fixture, wireFixture, typedindex.PublicationPointer) {
	t.Helper()
	ctx := t.Context()
	f, w := completeFixture(t, endpoint, database)
	installNeutralNative(t, f, w, "")
	old, e := f.c.Execute(ctx, f.chunk, f.source, f.raw)
	if e != nil {
		t.Fatal("prior publish", e)
	}
	if e = f.s.CompleteGenerationChunk(ctx, f.chunk); e != nil {
		t.Fatal(e)
	}
	if e = f.c.AfterSettlement(ctx, old.AttemptDigest); e != nil {
		t.Fatal(e)
	}
	intent, e := f.s.GetTypedIndexIntent(ctx, f.chunk.Repository)
	if e != nil {
		t.Fatal(e)
	}
	source, e := f.s.GetTypedSource(ctx, f.chunk.Repository)
	if e != nil {
		t.Fatal(e)
	}
	request := typedindex.NewManagedRequest(source, w.profile, uint64(intent.ProfileEpoch), intent.UniverseDigest, purpose)
	if _, e = f.s.EnqueueTypedIndex(ctx, f.chunk.Repository, encode(t, request)); e != nil {
		t.Fatal(e)
	}
	spec, e := f.s.TypedIndexSchedule(ctx, f.chunk.Repository)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.EnqueueGenerationSchedule(ctx, spec); e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.ExpandGenerationSchedule(ctx, spec.Repository, spec.Stage, spec.Generation); e != nil {
		t.Fatal(e)
	}
	chunk, e := f.s.ClaimGenerationChunk(ctx, store.GenerationResourceTypedIndex, "check")
	if e != nil || chunk == nil {
		t.Fatal(e)
	}
	f.chunk = *chunk
	return f, w, old.Pointer
}

func TestTypedExecutorCheckedTurn(t *testing.T) {
	endpoint := testServer(t)
	for _, purpose := range []typedindex.Purpose{typedindex.Canary, typedindex.DryRun} {
		t.Run(string(purpose), func(t *testing.T) {
			ctx := t.Context()
			f, w, prior := checkedFixture(t, endpoint, "checked_"+strings.ReplaceAll(string(purpose), "-", "_"), purpose)
			events, begins := installNeutralNative(t, f, w, "")
			out, e := f.c.Execute(ctx, f.chunk, f.source, f.raw)
			if e != nil || out.Check == nil || out.Check.Purpose != purpose || out.Pointer.Epoch != 0 || *begins != 1 {
				t.Fatal("checked turn", out, e, *events)
			}
			want := []string{"prepare", "verify", "run-plan", "complete-plan", "quiescent", "cleanup", "advance", "prepare", "verify", "run-execute", "complete-execute", "quiescent", "cleanup"}
			if !slices.Equal(*events, want) {
				t.Fatal("changed two-phase execution", *events)
			}
			work, e := f.s.BeginTypedIndex(ctx, f.chunk)
			if e != nil {
				t.Fatal(e)
			}
			id, e := typedworkspace.NewOwnerIdentity(work.Parent, f.chunk.Identity, f.chunk.LeaseToken)
			if e != nil {
				t.Fatal(e)
			}
			manifest, e := typedworkspace.LoadOwner(ctx, f.c.config.Workspace, id)
			if e != nil || manifest.Revision != 2 || manifest.Publication != nil || manifest.PublicationName != "" {
				t.Fatal("published custody", manifest, e)
			}
			entries, e := os.ReadDir(filepath.Join(f.c.config.Workspace, id.RelativeName()))
			if e != nil {
				t.Fatal(e)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), "bundle-") || entry.Name() == "publication-receipt.json" {
					t.Fatal("check installed publication", entry.Name())
				}
			}
			current, e := f.s.ResolveTypedIndexCurrent(ctx, f.chunk.Repository)
			if e != nil || current != prior {
				t.Fatal("prior current lost", e)
			}
			if e = f.c.AfterSettlement(ctx, out.AttemptDigest); e == nil {
				t.Fatal("released before scheduler settlement")
			}
			if e = f.s.CompleteGenerationChunk(ctx, f.chunk); e != nil {
				t.Fatal(e)
			}
			if e = f.c.AfterSettlement(ctx, out.AttemptDigest); e != nil {
				t.Fatal(e)
			}
			if _, e = f.s.GetTypedIndexGrowth(ctx); !errors.Is(e, store.ErrNotFound) {
				t.Fatal("check holder stranded", e)
			}
			if e = f.c.Startup(ctx); e != nil {
				t.Fatal("checked restart census", e)
			}
		})
	}
}

func TestTypedExecutorCheckedFailure(t *testing.T) {
	endpoint := testServer(t)
	for n, purpose := range []typedindex.Purpose{typedindex.Canary, typedindex.DryRun} {
		t.Run(string(purpose), func(t *testing.T) {
			f, w, prior := checkedFixture(t, endpoint, "checkedfailure"+string(rune('a'+n)), purpose)
			_, begins := installNeutralNative(t, f, w, "complete-execute")
			out, e := f.c.Execute(t.Context(), f.chunk, f.source, f.raw)
			if e == nil || out.Check != nil || out.Pointer.Epoch != 0 {
				t.Fatal("failed check succeeded", out, e)
			}
			status, e := f.s.GetTypedIndexStatus(t.Context(), f.chunk.Repository)
			if e != nil || status.Check != nil || status.Stage == store.TypedChecked {
				t.Fatal("failed status", status, e)
			}
			if current, e := f.s.ResolveTypedIndexCurrent(t.Context(), f.chunk.Repository); e != nil || current != prior {
				t.Fatal("prior current changed", e)
			}
			if _, e = f.c.Execute(t.Context(), f.chunk, f.source, f.raw); e == nil || *begins != 1 {
				t.Fatal("failed check replayed", e, *begins)
			}
			if e = f.s.FailGenerationChunk(t.Context(), f.chunk, "checked fixture failure"); e != nil {
				t.Fatal(e)
			}
			if e = f.c.AfterSettlement(t.Context(), out.AttemptDigest); e != nil {
				t.Fatal("failure cleanup", e)
			}
			if e = f.c.Startup(t.Context()); e != nil {
				t.Fatal("failed check recovery", e)
			}
		})
	}
}
