package provider

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"slices"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/typedbazel/launcher"
	"github.com/bmeddeb/phebs/internal/typedbazel/planner"
	"github.com/bmeddeb/phebs/internal/typedimport"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/bmeddeb/phebs/internal/typedmodule"
	"github.com/bmeddeb/phebs/internal/typedsandbox"
	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"
)

type inputFixture struct {
	i         Invocation
	selection InputSelection
	raw       []byte
	files     map[string][]byte
	packages  []loadedInputPackage
	scip      []byte
}

type fixtureControlSource map[string][]byte

func (s fixtureControlSource) Read(_ context.Context, name string, limit int) ([]byte, error) {
	raw, ok := s[name]
	if !ok || len(raw) > limit {
		return nil, typedindex.Unprepared
	}
	return slices.Clone(raw), nil
}

func inputFixtureFor(t *testing.T, kind string, mutate ...func(*scip.Index)) inputFixture {
	t.Helper()
	ctx := t.Context()
	source := typedindex.Source{Repository: "example.test/repo", Incarnation: "one", Generation: hash([]byte("source")), Commit: strings.Repeat("a", 40)}
	controls := fixtureControlSource{"go.mod": []byte("module example.test/root\ngo 1.25\n")}
	m, err := typedmodule.Discover(ctx, controls, typedmodule.ModeSingle, "go.mod", []string{"example.test/root/pkg"})
	if err != nil {
		t.Fatal(err)
	}
	f := inputFixture{selection: InputSelection{Schema: InputSelectionSchema, Source: source}, files: map[string][]byte{"source/go.mod": controls["go.mod"], "source/pkg/a.go": []byte("package pkg\nvar Foo int\n"), typedindex.ManagedHelperFile: []byte("helper")}}
	scope := m.Digest()
	if kind == typedindex.ModuleProviderID {
		f.selection.Module = &m
	} else {
		f.selection.Import = &typedimport.ImportSelection{Schema: typedimport.SelectionSchema, Source: source, Producer: typedimport.Producer{Name: "scip-go", Version: "0.2.7", Digest: hash([]byte("declared-producer"))}, Artifacts: []typedimport.ImportArtifact{{Path: "artifacts/index.scip"}}, Roots: []typedimport.RootMapping{{Input: "capture", Repo: "."}}, Coverage: []string{"example.test/root/pkg"}, Excluded: []string{}, Provenance: "operator-1"}
	}
	index := &scip.Index{Metadata: &scip.Metadata{ProjectRoot: "file:///capture", TextDocumentEncoding: scip.TextEncoding_UTF8, ToolInfo: &scip.ToolInfo{Name: "scip-go", Version: "0.2.7"}}, Documents: []*scip.Document{{RelativePath: "pkg/a.go", Language: "go", Occurrences: []*scip.Occurrence{{Range: []int32{1, 4, 7}, Symbol: "scip-go gomod example.test/root " + source.Commit + " Foo.", SymbolRoles: int32(scip.SymbolRole_Definition)}}, Symbols: []*scip.SymbolInformation{{Symbol: "scip-go gomod example.test/root " + source.Commit + " Foo."}}}}}
	if f.selection.Module != nil {
		index.Metadata.ProjectRoot = "file://" + launcher.Workspace
		index.Metadata.ToolInfo.Arguments = inputSCIPArguments(inputBinding{selection: f.selection, packages: []inputPackage{{importPath: "example.test/root/pkg", root: 0}}}, 0)
	}
	for _, change := range mutate {
		change(index)
	}
	f.scip, err = proto.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	if f.selection.Import != nil {
		f.files["artifacts/index.scip"] = f.scip
		f.selection.Import.Artifacts[0].Bytes, f.selection.Import.Artifacts[0].Digest = int64(len(f.scip)), hash(f.scip)
		scope = f.selection.Import.Digest()
	}
	unit := inputUnit(kind, scope, "example.test/root/pkg")
	f.selection.Plan = typedindex.PackagePlanDefinition{Schema: typedindex.PackagePlanSchema, Targets: []typedindex.PlannedTarget{{ID: InputTargetID(kind, scope, "example.test/root/pkg"), Dependencies: []string{}, Units: []typedindex.PackageUnitID{unit}}}, Units: []typedindex.PlannedUnit{{ID: unit, Imports: []typedindex.PackageUnitID{}, Documents: []string{"pkg/a.go"}}}, Documents: []typedindex.PlannedDocument{{Member: inputSlot(0), Path: "pkg/a.go", Unit: unit, Bytes: int64(len(f.files["source/pkg/a.go"])), Digest: hash(f.files["source/pkg/a.go"])}}}
	f.raw, err = json.Marshal(f.selection)
	if err != nil {
		t.Fatal(err)
	}
	name, _ := typedindex.SelectionFile(kind)
	f.files[name] = f.raw
	var rows []typedindex.BundleFile
	for name, raw := range f.files {
		rows = append(rows, typedindex.BundleFile{Path: name, Bytes: int64(len(raw)), Digest: hash(raw), Executable: name == typedindex.ManagedHelperFile})
	}
	slices.SortFunc(rows, func(a, b typedindex.BundleFile) int { return strings.Compare(a.Path, b.Path) })
	invRaw, _ := json.Marshal(typedindex.InventoryDefinition{Schema: typedindex.InventorySchema, Files: rows})
	inv, err := typedindex.DecodeInventory(ctx, invRaw, hash(invRaw))
	if err != nil {
		t.Fatal(err)
	}
	helper := typedindex.Tool{Version: "test-helper", Digest: hash(f.files[typedindex.ManagedHelperFile])}
	tools := typedindex.Tools{Planner: helper, Launcher: helper, Indexer: typedindex.Tool{Version: "0.2.7", Digest: SCIPDigest}}
	if f.selection.Module != nil {
		tools.Go = typedindex.Tool{Version: "1.25.0", Digest: GoDigest}
	} else {
		tools.Indexer.Digest = f.selection.Import.Producer.Digest
	}
	cfg, _ := typedindex.InputConfig(kind)
	pRaw, _ := json.Marshal(typedindex.ProfileDefinition{Schema: typedindex.InputProfileSchema, Name: "input", Provider: kind, Tools: tools, Config: cfg, Policy: typedindex.MeasuredPolicy(), BundleDigest: inv.Digest(), ImageDigest: hash([]byte("image"))})
	p, err := typedindex.DecodeProfile(ctx, pRaw)
	if err != nil {
		t.Fatal(err)
	}
	r := typedindex.NewManagedRequest(source, p, 1, identity(f.selection.Plan.Targets), typedindex.Publish)
	raw, _ := json.Marshal(r)
	a, err := typedindex.Admit(ctx, typedindex.Authority{Enabled: true, Administrator: true, Source: source, Profile: typedindex.Epoch{Number: 1, Digest: p.Digest()}, UniverseDigest: r.UniverseDigest}, p, raw)
	if err != nil {
		t.Fatal(err)
	}
	f.i = Invocation{Parent: a, Profile: p, Inventory: inv, Phase: typedindex.Plan, Allowance: typedsandbox.Allowance{Schema: "phebs-typed-allowance-v1", PlanningDigest: a.Digest(), AttemptDigest: hash([]byte("attempt")), BootID: "12345678-1234-1234-1234-123456789abc", TimeDevice: 1, TimeInode: 2, Start: 1, Deadline: 1 + int64(typedsandbox.WallLimit)}}
	f.packages = []loadedInputPackage{{Dir: path.Join(launcher.Workspace, "pkg"), ImportPath: "example.test/root/pkg", GoFiles: []string{"a.go"}, Imports: []string{}}}
	return f
}

func executeInputFixture(t *testing.T, f *inputFixture) {
	t.Helper()
	p, err := BindInputSelection(t.Context(), f.i, f.raw)
	if err != nil {
		t.Fatal(err)
	}
	r, err := typedindex.PlannedSuccessor(t.Context(), f.i.Parent, p.Digest())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(r)
	a, err := typedindex.Admit(t.Context(), typedindex.Authority{Enabled: true, Administrator: true, Source: r.Source, Profile: typedindex.Epoch{Number: r.ProfileEpoch, Digest: f.i.Profile.Digest()}, UniverseDigest: r.UniverseDigest, ParentRequestDigest: f.i.Parent.Digest(), PlanDigest: p.Digest()}, f.i.Profile, raw)
	if err != nil {
		t.Fatal(err)
	}
	f.i.Execution, f.i.Plan, f.i.Phase = a, p, typedindex.Execute
}

func inputFixtureOperations(t *testing.T, f inputFixture, commands *int, joined *bool) inputOperations {
	t.Helper()
	return inputOperations{
		read: func(_ typedindex.Inventory, name string, limit int64) ([]byte, error) {
			raw, ok := f.files[name]
			if !ok || int64(len(raw)) > limit {
				return nil, typedindex.Unprepared
			}
			return slices.Clone(raw), nil
		},
		tools:       func(context.Context, Invocation) error { return nil },
		materialize: func(context.Context, Invocation) ([]original, error) { return []original{}, nil },
		verify:      func(context.Context, planner.Plan, []original) error { return nil },
		command: func(_ context.Context, executable string, args, env []string, _ *outputBudget, directory string) ([]byte, []byte, error) {
			*commands++
			if !slices.Equal(env, inputEnvironment(*f.selection.Module)) {
				t.Fatal("unexpected environment")
			}
			if executable == "/inputs/tools/go/bin/go" {
				if !slices.Equal(args, []string{"list", "-json", "-mod=readonly", "example.test/root/pkg"}) || directory != launcher.Workspace {
					t.Fatal(args, directory)
				}
				raw, _ := json.Marshal(f.packages[0])
				return raw, nil, nil
			}
			b, err := bindInputSelection(t.Context(), f.i, f.raw)
			if err != nil {
				t.Fatal(err)
			}
			if executable != SCIPPath || !slices.Equal(args, inputSCIPArguments(b, 0)) {
				t.Fatal("unexpected indexer command")
			}
			return nil, nil, nil
		},
		output: func(name string, limit int64) ([]byte, error) {
			if name != inputOutput(0) || limit != typedindex.MaxSCIPMemberBytes {
				t.Fatal(name, limit)
			}
			return slices.Clone(f.scip), nil
		},
		quiesce: func() error { *joined = true; return nil },
	}
}

func TestAdditionalInputsPlanExecuteAndRouting(t *testing.T) {
	for _, kind := range []string{typedindex.ModuleProviderID, typedindex.ImportProviderID} {
		t.Run(kind, func(t *testing.T) {
			f := inputFixtureFor(t, kind)
			commands, joined := 0, false
			raw, err := runInput(t.Context(), f.i, inputFixtureOperations(t, f, &commands, &joined))
			if err != nil || !joined {
				t.Fatal(err, joined)
			}
			r, err := DecodeResult(t.Context(), f.i, f.raw, raw)
			if err != nil || r.Plan().Digest() == "" {
				t.Fatal(err)
			}
			if _, err = r.Finalize(t.Context()); err == nil {
				t.Fatal("planning authorized publication")
			}
			executeInputFixture(t, &f)
			joined = false
			raw, err = runInput(t.Context(), f.i, inputFixtureOperations(t, f, &commands, &joined))
			if err != nil || !joined {
				t.Fatal(err, joined)
			}
			r, err = DecodeResult(t.Context(), f.i, f.raw, raw)
			if err != nil {
				t.Fatal(err)
			}
			bundle, err := r.Finalize(t.Context())
			if err != nil || bundle.RootDigest() == "" {
				t.Fatal(err)
			}
			if kind == typedindex.ImportProviderID && commands != 0 {
				t.Fatal("import executed a command")
			}
			if kind == typedindex.ModuleProviderID && commands != 3 {
				t.Fatal("module recipe changed", commands)
			}
			root, err := typedindex.DecodeRoutingRoot(t.Context(), f.i.Execution, bundle.RootDigest(), bundle.RootBytes())
			if err != nil {
				t.Fatal(err)
			}
			routes, err := typedindex.DecodeRouting(t.Context(), f.i.Execution, bundle.RootDigest(), bundle.RootBytes(), bundle.AttemptBytes(), bundle.Content(root.Documents.Name), bundle.Content(root.Symbols.Name))
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := routes.Document("pkg/a.go"); !ok {
				t.Fatal("document route missing")
			}
			var attempt typedindex.AttemptManifest
			if json.Unmarshal(bundle.AttemptBytes(), &attempt) != nil || attempt.Input == nil || attempt.Input.SelectionDigest != hash(f.raw) || attempt.Input.Members[0].Digest != hash(f.scip) {
				t.Fatal("missing retained provenance")
			}
			if kind == typedindex.ImportProviderID && (attempt.Input.DeclaredProducer == nil || attempt.SCIPGo != nil) {
				t.Fatal("import attestation became execution/adaptation proof")
			}
		})
	}
}

func TestAdditionalInputFailuresSuppressSuccess(t *testing.T) {
	for _, kind := range []string{typedindex.ModuleProviderID, typedindex.ImportProviderID} {
		t.Run(kind, func(t *testing.T) {
			for _, mode := range []string{"changed-bytes", "quiescence", "canceled", "wrong-root", "extra-document"} {
				t.Run(mode, func(t *testing.T) {
					f := inputFixtureFor(t, kind)
					executeInputFixture(t, &f)
					commands, joined := 0, false
					o := inputFixtureOperations(t, f, &commands, &joined)
					switch mode {
					case "changed-bytes":
						f.scip = append(slices.Clone(f.scip), 0)
						if kind == typedindex.ImportProviderID {
							f.files["artifacts/index.scip"] = f.scip
						}
						o = inputFixtureOperations(t, f, &commands, &joined)
					case "quiescence":
						o.quiesce = func() error { joined = true; return typedindex.Containment }
					case "canceled":
						o.tools = func(context.Context, Invocation) error { return context.Canceled }
					default:
						var index scip.Index
						_ = proto.Unmarshal(f.scip, &index)
						if mode == "wrong-root" {
							index.Metadata.ProjectRoot = "file:///foreign"
						} else {
							index.Documents = append(index.Documents, &scip.Document{RelativePath: "extra.go", Language: "go"})
						}
						f.scip, _ = proto.Marshal(&index)
						if kind == typedindex.ImportProviderID {
							f.files["artifacts/index.scip"] = f.scip
						}
						o = inputFixtureOperations(t, f, &commands, &joined)
					}
					raw, err := runInput(t.Context(), f.i, o)
					if err == nil || len(raw) != 0 {
						t.Fatal("failed input reported success", mode, err)
					}
				})
			}
		})
	}
}

func TestAdditionalResultRefusesChangedAuthorityAndCoverage(t *testing.T) {
	f := inputFixtureFor(t, typedindex.ModuleProviderID)
	executeInputFixture(t, &f)
	commands, joined := 0, false
	raw, err := runInput(t.Context(), f.i, inputFixtureOperations(t, f, &commands, &joined))
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"request", "phase", "missing-member", "package-path", "package-document", "internal-import", "foreign-provider", "stale-symbol"} {
		t.Run(mode, func(t *testing.T) {
			var r inputResult
			if json.Unmarshal(raw, &r) != nil {
				t.Fatal("result")
			}
			i := f.i
			switch mode {
			case "request":
				r.RequestDigest = hash([]byte("other"))
			case "phase":
				r.Phase = typedindex.Plan
			case "missing-member":
				r.Members = nil
			case "package-path":
				r.Packages[0].Dir = "/outside"
			case "package-document":
				r.Packages[0].GoFiles = []string{"other.go"}
			case "internal-import":
				r.Packages[0].Imports = []string{"example.test/root/missing"}
			case "stale-symbol":
				var index scip.Index
				_ = proto.Unmarshal(r.Members[0].SCIP, &index)
				index.Documents[0].Symbols[0].Symbol = strings.ReplaceAll(index.Documents[0].Symbols[0].Symbol, f.selection.Source.Commit, strings.Repeat("b", 40))
				r.Members[0].SCIP, _ = proto.Marshal(&index)
			case "foreign-provider":
				i.Profile = typedindex.Profile{}
			}
			b, _ := json.Marshal(r)
			if _, err := DecodeResult(t.Context(), i, f.raw, b); err == nil {
				t.Fatal("changed result admitted", mode)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := DecodeResult(ctx, f.i, f.raw, raw); !errors.Is(err, typedindex.Canceled) {
		t.Fatal(err)
	}
}

func TestAdditionalWorkspacePackageResolution(t *testing.T) {
	s := typedmodule.ModuleSelection{Roots: []typedmodule.ModuleRoot{{Path: ".", Module: "example.test/root"}, {Path: "nested", Module: "example.test/root/nested"}}}
	for _, tc := range []struct {
		selector, directory, imported string
		root                          int
	}{
		{"./pkg", "pkg", "example.test/root/pkg", 0},
		{"./nested/pkg", "nested/pkg", "example.test/root/nested/pkg", 1},
		{"example.test/root/nested/pkg", "nested/pkg", "example.test/root/nested/pkg", 1},
		{"example.test/root", ".", "example.test/root", 0},
	} {
		t.Run(tc.selector, func(t *testing.T) {
			p, err := resolveInputPackage(s, tc.selector)
			if err != nil || p.directory != tc.directory || p.importPath != tc.imported || p.root != tc.root {
				t.Fatal(p, err)
			}
		})
	}
	s.Roots[1].Module = "example.test/other"
	if _, err := resolveInputPackage(s, "example.test/root/nested/pkg"); err == nil {
		t.Fatal("outer import alias crossed nested module")
	}
	if p, err := resolveInputPackage(s, "./nested/pkg"); err != nil || p.importPath != "example.test/other/pkg" {
		t.Fatal(p, err)
	}
}

func TestImportDefinitionOccurrencesBindCommit(t *testing.T) {
	for _, tc := range []struct {
		name, scheme, manager, version string
		wantError                      bool
	}{
		{"current", "scip-go", "gomod", strings.Repeat("a", 40), false},
		{"stale", "scip-go", "gomod", strings.Repeat("b", 40), true},
		{"scheme", "other", "gomod", strings.Repeat("a", 40), true},
		{"manager", "scip-go", "other", strings.Repeat("a", 40), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Mutate before sealing artifact bytes so identity, not digest mismatch,
			// must refuse an occurrence-only definition. External references retain
			// their own dependency versions.
			f := inputFixtureFor(t, typedindex.ImportProviderID, func(index *scip.Index) {
				d := index.Documents[0]
				d.Symbols = nil
				d.Occurrences[0].Symbol = tc.scheme + " " + tc.manager + " example.test/root " + tc.version + " Foo."
				d.Occurrences = append(d.Occurrences, &scip.Occurrence{Range: []int32{1, 8, 11}, Symbol: "scip-go gomod example.test/dependency v1.0.0 Bar."})
			})
			executeInputFixture(t, &f)
			commands, joined := 0, false
			raw, err := runInput(t.Context(), f.i, inputFixtureOperations(t, f, &commands, &joined))
			if !joined || commands != 0 {
				t.Fatal("import custody or execution changed")
			}
			if tc.wantError {
				if !errors.Is(err, typedindex.Stale) || len(raw) != 0 {
					t.Fatal("accepted mismatched definition", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = DecodeResult(t.Context(), f.i, f.raw, raw); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestModuleWorkspaceReferenceIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, module, version string
		missing, stale        bool
	}{
		{"sibling", "example.test/root", ".", false, false},
		{"external", "example.test/external", ".", false, false},
		{"versioned", "example.test/root", "v1.0.0", false, false},
		{"missing", "example.test/root", ".", true, false},
		{"stale", "example.test/root", ".", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := inputFixtureFor(t, typedindex.ModuleProviderID)
			var index scip.Index
			if err := proto.Unmarshal(f.scip, &index); err != nil {
				t.Fatal(err)
			}
			definition := index.Documents[0].Occurrences[0].Symbol
			if tc.missing {
				index.Documents[0].Occurrences = nil
			}
			if tc.stale {
				index.Documents[0].Occurrences[0].Symbol = strings.ReplaceAll(definition, f.selection.Source.Commit, strings.Repeat("b", 40))
			}
			reference := "scip-go gomod " + tc.module + " " + tc.version + " Foo."
			info := &scip.SymbolInformation{Symbol: reference, EnclosingSymbol: reference, Relationships: []*scip.Relationship{{Symbol: reference}}, SignatureDocumentation: &scip.Signature{Occurrences: []*scip.Occurrence{{Symbol: reference}}}}
			other := &scip.Index{Documents: []*scip.Document{{Occurrences: []*scip.Occurrence{{Symbol: reference}}}}, ExternalSymbols: []*scip.SymbolInformation{info}}
			err := normalizeInputReferences(inputBinding{selection: f.selection}, []*scip.Index{other, &index})
			if tc.missing || tc.stale {
				if !errors.Is(err, typedindex.Stale) {
					t.Fatal("unproved reference accepted", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := reference
			if tc.name == "sibling" {
				want = definition
			}
			for _, got := range []string{other.Documents[0].Occurrences[0].Symbol, info.Symbol, info.EnclosingSymbol, info.Relationships[0].Symbol, info.SignatureDocumentation.Occurrences[0].Symbol} {
				if got != want {
					t.Fatal("incorrect reference identity", got)
				}
			}
		})
	}
}
