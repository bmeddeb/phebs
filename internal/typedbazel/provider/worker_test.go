package provider

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/typedbazel/launcher"
	"github.com/bmeddeb/phebs/internal/typedbazel/planner"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/bmeddeb/phebs/internal/typedsandbox"
	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"
)

func rawHash(v any) string { return strings.TrimPrefix(identity(v), "sha256:") }
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
func fixture(t *testing.T) (Invocation, Selection, planner.Plan, launcher.NativeGoFiles, []byte) {
	t.Helper()
	ctx := context.Background()
	uid := strings.Repeat("a", 64)
	owner := planner.Configured{Label: "@@//lib:lib", Configuration: strings.Repeat("b", 64)}
	mode := planner.GoMode{GOOS: "linux", GOARCH: "arm64"}
	p := planner.Plan{Version: planner.PlanV2, Targets: []planner.Target{{Configured: owner, Kind: "go_library", Inputs: []planner.Configured{}, Units: []string{uid}}}, Units: []planner.Unit{{ID: uid, Owner: owner, ArchiveLabel: owner.Label, Variant: "lib|bazel-out/cfg/bin/lib/lib.x", ImportPath: "example.test/lib", PackageName: "lib", GoFiles: []string{"doc"}, CompiledGoFiles: []string{"doc"}, Imports: []planner.UnitImport{}, SDK: "sdk", Mode: mode}}, Documents: []planner.Document{{ID: "doc", Kind: "source", Path: "lib/lib.go", ExecPath: "lib/lib.go", SHA256: strings.TrimPrefix(hash([]byte("package lib\n")), "sha256:"), Bytes: 12}}, SDKs: []planner.SDKPlan{{ID: "sdk", Owner: owner, SDKProjection: planner.SDKProjection{Version: "1.25.0", Root: nativeSDKRoot, Mode: mode}}}}
	sealRaw(&p)
	unit, _ := unitID(uid)
	source := typedindex.Source{Repository: "example.test/repo", Incarnation: "incarnation", Generation: hash([]byte("source")), Commit: strings.Repeat("c", 40)}
	s := Selection{Schema: SelectionSchema, Source: source, Roots: []string{"//lib:lib"}, Targets: []typedindex.PlannedTarget{{ID: targetID(owner), Dependencies: []string{}, Units: []typedindex.PackageUnitID{unit}}}, Module: "example.test/lib", Remote: "https://example.test/repo"}
	raw := selectionBytes(s)
	invRaw, _ := json.Marshal(typedindex.InventoryDefinition{Schema: typedindex.InventorySchema, Files: []typedindex.BundleFile{{Path: "source/lib/lib.go", Bytes: 12, Digest: hash([]byte("package lib\n"))}, {Path: SelectionFile, Bytes: int64(len(raw)), Digest: hash(raw)}}})
	inv, e := typedindex.DecodeInventory(ctx, invRaw, hash(invRaw))
	if e != nil {
		t.Fatal(e)
	}
	tool := typedindex.Tool{Version: "owned", Digest: hash([]byte("helper"))}
	tools := typedindex.Tools{Bazel: typedindex.Tool{Version: "9.0.0", Digest: BazelDigest}, RulesGo: typedindex.Tool{Version: "0.59.0", Digest: "sha256:" + rulesArchive}, Go: typedindex.Tool{Version: "1.25.0", Digest: GoDigest}, Driver: typedindex.Tool{Version: "0.59.0", Digest: "sha256:" + launcher.NativeDriverSHA256}, Indexer: typedindex.Tool{Version: "0.2.7", Digest: SCIPDigest}, Planner: tool, Launcher: tool}
	def := typedindex.ProfileDefinition{Schema: typedindex.ProfileSchema, Name: "canary", Provider: typedindex.ProviderID, Tools: tools, Config: typedindex.ReducedConfig(), Policy: typedindex.MeasuredPolicy(), BundleDigest: inv.Digest(), ImageDigest: hash([]byte("image"))}
	profileRaw, _ := json.Marshal(def)
	profile, e := typedindex.DecodeProfile(ctx, profileRaw)
	if e != nil {
		t.Fatal(e)
	}
	request := typedindex.NewRequest(source, profile, 1, identity(s.Targets), "run")
	requestRaw, _ := json.Marshal(request)
	admit, e := typedindex.Admit(ctx, typedindex.Authority{Enabled: true, Administrator: true, Source: source, Profile: typedindex.Epoch{Number: 1, Digest: profile.Digest()}, UniverseDigest: identity(s.Targets)}, profile, requestRaw)
	if e != nil {
		t.Fatal(e)
	}
	a := typedsandbox.Allowance{Schema: "phebs-typed-allowance-v1", PlanningDigest: admit.Digest(), AttemptDigest: hash([]byte("attempt")), BootID: "12345678-1234-1234-1234-123456789abc", TimeDevice: 1, TimeInode: 2, Start: 1, Deadline: 1 + int64(typedsandbox.WallLimit)}
	i := Invocation{Parent: admit, Profile: profile, Inventory: inv, Allowance: a, Phase: typedindex.Plan}
	matches := launcher.NativeGoFiles{Version: "phebs-t451b-native-go-files-v1", MappingSHA256: p.MappingSHA256, DocumentsSHA256: p.DocumentsSHA256, Packages: []launcher.NativeGoFilesPackage{{ID: owner.Label, GoFiles: []string{launcher.Workspace + "/lib/lib.go"}}}}
	return i, s, p, matches, raw
}
func executing(t *testing.T, i Invocation, s Selection, p planner.Plan) Invocation {
	t.Helper()
	ctx := context.Background()
	roots, e := configuredRoots(p, s)
	if e != nil {
		t.Fatal(e)
	}
	plan, _, _, e := mapPlan(ctx, i, s, p, roots)
	if e != nil {
		t.Fatal(e)
	}
	request, e := typedindex.PlannedSuccessor(ctx, i.Parent, plan.Digest())
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(request)
	execution, e := typedindex.Admit(ctx, typedindex.Authority{Enabled: true, Administrator: true, Source: request.Source, Profile: typedindex.Epoch{Number: request.ProfileEpoch, Digest: i.Profile.Digest()}, UniverseDigest: request.UniverseDigest, ParentRequestDigest: i.Parent.Digest(), PlanDigest: plan.Digest()}, i.Profile, raw)
	if e != nil {
		t.Fatal(e)
	}
	i.Phase = typedindex.Execute
	i.Execution = execution
	i.Plan = plan
	return i
}
func neutralOperations(t *testing.T, s Selection, p planner.Plan, m launcher.NativeGoFiles, raw []byte, events *[]string) workerOperations {
	t.Helper()
	add := func(s string) { *events = append(*events, s) }
	return workerOperations{
		readSelection: func(_ typedindex.Inventory, name string, _ int64) ([]byte, error) {
			add("selection")
			if name != SelectionFile {
				t.Fatal(name)
			}
			return raw, nil
		},
		rules: func(context.Context, typedindex.Inventory) error { add("rules"); return nil },
		tools: func(context.Context, Invocation) error { add("tools"); return nil }, materialize: func(context.Context, Invocation) ([]original, error) {
			add("materialize")
			return []original{{Path: "lib/lib.go", Bytes: 12, Digest: hash([]byte("package lib\n"))}}, nil
		}, compiler: func(context.Context, typedindex.Inventory) error { add("compiler"); return nil }, plan: func(_ context.Context, roots []string) (planner.Plan, error) {
			add("plan")
			if !slices.Equal(roots, s.Roots) {
				t.Fatal(roots)
			}
			return p, nil
		}, verify: func(context.Context, planner.Plan, []original) error { add("verify"); return nil }, sdk: func(planner.Plan) error { add("sdk"); return nil }, files: func(context.Context, planner.Plan, []planner.Configured) (launcher.NativeGoFiles, error) {
			add("files")
			return m, nil
		},
		leg: func(_ context.Context, _ planner.Plan, _ []planner.Configured, _ launcher.NativeGoFiles, _ Selection, slot string) (LegEvidence, error) {
			add(slot)
			return LegEvidence{Slot: slot, Call: CallEvidence{Launcher: launcher.Invocation{Arguments: []string{"example.test/lib"}}}}, nil
		},
		read: func(_ string, _ int64) ([]byte, error) {
			add("scip-read")
			return proto.Marshal(&scip.Index{Metadata: &scip.Metadata{ProjectRoot: "file:///scratch/workspace", TextDocumentEncoding: scip.TextEncoding_UTF8, ToolInfo: &scip.ToolInfo{Name: "scip-go", Version: "0.2.7", Arguments: scipArguments(s, []string{"example.test/lib"})}}, Documents: []*scip.Document{{Language: "go", RelativePath: "lib/lib.go"}}})
		}, quiesce: func() error { add("quiesce"); return nil },
	}
}
func TestWorkerPlanExecute(t *testing.T) {
	i, s, p, m, raw := fixture(t)
	for _, phase := range []typedindex.Action{typedindex.Plan, typedindex.Execute} {
		t.Run(string(phase), func(t *testing.T) {
			work := i
			if phase == typedindex.Execute {
				work = executing(t, i, s, p)
			}
			var events []string
			o := neutralOperations(t, s, p, m, raw, &events)
			b, e := run(context.Background(), work, o)
			if e != nil {
				t.Fatal(e)
			}
			r, e := decode[Result](b, typedsandbox.OutputBytes)
			if e != nil {
				t.Fatal(e)
			}
			want := []string{"selection", "tools", "materialize", "compiler", "plan", "rules", "verify", "sdk", "files"}
			if phase == typedindex.Execute {
				want = append(want, "load", "rules", "scip", "scip-read")
				if len(r.SCIP) == 0 || r.SCIPSHA256 != hash(r.SCIP) {
					t.Fatal("SCIP evidence")
				}
			} else if len(r.Legs) != 0 || len(r.SCIP) != 0 {
				t.Fatal("planning executed client")
			}
			want = append(want, "quiesce", "rules", "verify")
			if !slices.Equal(events, want) {
				t.Fatal(events)
			}
			if len(r.Units) != 1 || r.Units[0].State != typedindex.UnitComplete || len(r.Documents) != 1 {
				t.Fatal("incomplete mapping")
			}
		})
	}
}
func TestWorkerRefusals(t *testing.T) {
	for _, name := range []string{"selection-mutation", "changed-plan", "source-mutation", "quiescence", "load-failure", "output-cap", "canceled"} {
		t.Run(name, func(t *testing.T) {
			i, s, p, m, raw := fixture(t)
			i = executing(t, i, s, p)
			var events []string
			o := neutralOperations(t, s, p, m, raw, &events)
			ctx := context.Background()
			switch name {
			case "selection-mutation":
				o.readSelection = func(typedindex.Inventory, string, int64) ([]byte, error) { return append(raw, ' '), nil }
			case "changed-plan":
				p.Documents[0].SHA256 = strings.Repeat("d", 64)
				sealRaw(&p)
				m.MappingSHA256 = p.MappingSHA256
				m.DocumentsSHA256 = p.DocumentsSHA256
				o.plan = func(context.Context, []string) (planner.Plan, error) { return p, nil }
				o.files = func(context.Context, planner.Plan, []planner.Configured) (launcher.NativeGoFiles, error) {
					return m, nil
				}
			case "source-mutation":
				o.verify = func(context.Context, planner.Plan, []original) error { return typedindex.Stale }
			case "quiescence":
				o.quiesce = func() error { return errors.New("private process remains") }
			case "load-failure":
				o.leg = func(context.Context, planner.Plan, []planner.Configured, launcher.NativeGoFiles, Selection, string) (LegEvidence, error) {
					return LegEvidence{}, errors.New("private child diagnostic")
				}
			case "output-cap":
				i.Allowance.WorkerBytesUsed = typedsandbox.OutputBytes - 1
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			b, e := run(ctx, i, o)
			if e == nil {
				t.Fatal("refusal lost", e)
			}
			if len(b) > 0 {
				r, decodeErr := decode[Result](b, typedsandbox.OutputBytes)
				if decodeErr != nil || r.Failure == nil || r.Failure.Reason != reason(e) || len(r.SCIP) != 0 {
					t.Fatal("invalid failure evidence", decodeErr)
				}
			}
			if name == "changed-plan" && slices.Contains(events, "load") {
				t.Fatal("stale plan reached client")
			}
			if strings.Contains(classify(e).Error(), "private") {
				t.Fatal("diagnostic escaped")
			}
		})
	}
}
func TestPublicWorkerHasNoPortableBypass(t *testing.T) {
	i, _, _, _, _ := fixture(t)
	if _, e := Run(context.Background(), i); !errors.Is(e, typedindex.Containment) {
		t.Fatal(e)
	}
}
