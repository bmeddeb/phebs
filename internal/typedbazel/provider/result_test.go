package provider

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/typedbazel/launcher"
	"github.com/bmeddeb/phebs/internal/typedbazel/planner"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/bmeddeb/phebs/internal/typedsandbox"
	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"
)

func resultWire(t *testing.T, r Result) []byte {
	t.Helper()
	b, e := json.Marshal(r)
	if e != nil {
		t.Fatal(e)
	}
	return append(b, '\n')
}

func verifiedNeutralLeg(t *testing.T, p planner.Plan, roots []planner.Configured, matches launcher.NativeGoFiles, s Selection, slot string) LegEvidence {
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
	call := CallEvidence{Slot: slot, PlanSHA256: hash(control), Argv: append([]string{NativeAdapterPath}, prepared.Invocation().Arguments...), Environment: env, Directory: launcher.Workspace, Request: request, RequestSHA256: hash(request), Launcher: prepared.Invocation(), LauncherSHA256: prepared.Digest(), Result: launcher.CompatibilityResult{DriverResponse: raw, Response: response, Exports: exports}, DriverResponseSHA256: hash(raw), ResponseSHA256: hash(response)}
	argv := append([]string{NativeProbePath}, prepared.Invocation().Arguments...)
	stdout := []byte{}
	if slot == "scip" {
		argv = append([]string{SCIPPath}, scipArguments(s, prepared.Invocation().Arguments)...)
	} else {
		stdout, e = json.Marshal(probe)
		if e != nil {
			t.Fatal(e)
		}
	}
	return LegEvidence{Slot: slot, ClientArgv: argv, Environment: env, WallNanoseconds: 1, Stdout: stdout, Stderr: []byte{}, Call: call}
}

func admittedResultFixture(t *testing.T, generated, reduced bool) (Invocation, []byte, []byte) {
	t.Helper()
	i, s, p, m, selection := fixture(t)
	var payload []byte
	if generated {
		i, s, p, m, selection, payload = generatedFixture(t, !reduced)
	}
	i = executing(t, i, s, p)
	var events []string
	o := neutralOperations(t, s, p, m, selection, &events)
	o.leg = func(_ context.Context, p planner.Plan, roots []planner.Configured, m launcher.NativeGoFiles, s Selection, slot string) (LegEvidence, error) {
		return verifiedNeutralLeg(t, p, roots, m, s, slot), nil
	}
	if generated {
		originalRead := o.read
		o.read = func(name string, n int64) ([]byte, error) {
			if name == launcher.ExecRoot+"/bazel-out/cfg/bin/lib/generated.go" {
				return payload, nil
			}
			b, e := originalRead(name, n)
			if e != nil {
				return nil, e
			}
			var index scip.Index
			if e = proto.Unmarshal(b, &index); e != nil {
				return nil, e
			}
			index.Documents = append(index.Documents, &scip.Document{Language: "go", RelativePath: "../bazel-output/execroot/_main/bazel-out/cfg/bin/lib/generated.go"})
			return proto.Marshal(&index)
		}
	}
	raw, e := run(context.Background(), i, o)
	if e != nil {
		t.Fatal(e)
	}
	return i, selection, raw
}

func TestResultAdmissionAndFinalization(t *testing.T) {
	for _, mode := range []string{"ordinary", "generated", "omit"} {
		t.Run(mode, func(t *testing.T) {
			i, selection, raw := admittedResultFixture(t, mode != "ordinary", mode == "omit")
			r, e := DecodeResult(context.Background(), i, selection, raw)
			if e != nil {
				t.Fatal(e)
			}
			if r.Plan().Digest() != i.Plan.Digest() {
				t.Fatal("plan changed")
			}
			b, e := r.Finalize(context.Background())
			if e != nil || b.RootDigest() == "" {
				t.Fatal(e)
			}
			contents := map[string][]byte{}
			for _, name := range b.Names() {
				contents[name] = b.Content(name)
			}
			if _, e = typedindex.VerifyBundle(context.Background(), i.Execution, i.Plan, b.AttemptBytes(), b.RootBytes(), contents); e != nil {
				t.Fatal(e)
			}
			var attempt typedindex.AttemptManifest
			if e = json.Unmarshal(b.AttemptBytes(), &attempt); e != nil {
				t.Fatal(e)
			}
			if attempt.Schema != typedindex.SCIPGoAttemptSchema || attempt.SCIPGo == nil || len(attempt.SCIPGo.Members) != 1 {
				t.Fatal("audit missing")
			}
			if (len(attempt.Generated) == 1) != (mode == "generated") {
				t.Fatal("generated coverage")
			}
			// Caller buffers and opaque plan accessors cannot mutate admitted bytes.
			for j := range raw {
				raw[j] = 0
			}
			for j := range selection {
				selection[j] = 0
			}
			copy(r.Plan().Bytes(), []byte("bad"))
			again, e := r.Finalize(context.Background())
			if e != nil || again.RootDigest() != b.RootDigest() {
				t.Fatal("mutable decoded custody", e)
			}
		})
	}
}

func TestResultMutationRefusals(t *testing.T) {
	i, selection, raw := admittedResultFixture(t, true, false)
	for _, kind := range []string{"request", "phase", "plan", "documents", "units", "gofiles", "sdk", "leg-order", "argv", "env", "call", "probe", "output", "time", "scip", "generated", "extra-generated", "missing-generated"} {
		t.Run(kind, func(t *testing.T) {
			r, e := decode[Result](raw, typedsandbox.OutputBytes)
			if e != nil {
				t.Fatal(e)
			}
			switch kind {
			case "request":
				r.RequestDigest = hash([]byte("other"))
			case "phase":
				r.Phase = typedindex.Plan
			case "plan":
				r.Plan = []byte(`{}`)
			case "documents":
				r.Documents[0].Path = "other.go"
			case "units":
				r.Units[0].State = typedindex.UnitFailed
			case "gofiles":
				r.GoFiles.Packages[0].GoFiles = append(r.GoFiles.Packages[0].GoFiles, "/wrong")
			case "sdk":
				r.RawPlan.SDKs[0].Version = "1.25.7"
				sealRaw(&r.RawPlan)
			case "leg-order":
				r.Legs[0], r.Legs[1] = r.Legs[1], r.Legs[0]
			case "argv":
				r.Legs[0].ClientArgv = append(r.Legs[0].ClientArgv, "other")
			case "env":
				r.Legs[0].Environment = append(r.Legs[0].Environment, "OTHER=1")
			case "call":
				r.Legs[0].Call.Request = []byte("{}")
			case "probe":
				r.Legs[0].Stdout = []byte("{}")
			case "output":
				r.Legs[0].Stderr = make([]byte, maxClientBytes)
			case "time":
				r.Legs[0].WallNanoseconds = int64(typedsandbox.WallLimit) + 1
			case "scip":
				r.SCIP = append(r.SCIP, 0)
				r.SCIPSHA256 = hash(r.SCIP)
			case "generated":
				for k := range r.Generated {
					r.Generated[k] = []byte("wrong")
				}
			case "extra-generated":
				r.Generated["unknown"] = []byte("wrong")
			case "missing-generated":
				r.Generated = nil
			}
			bad, e := DecodeResult(context.Background(), i, selection, resultWire(t, r))
			if e == nil {
				t.Fatal("mutation admitted")
			}
			if b, e := bad.Finalize(context.Background()); e == nil || b.RootDigest() != "" {
				t.Fatal("failed decode retained authority")
			}
			if _, e = DecodeResult(context.Background(), i, selection, raw); e != nil {
				t.Fatal("restored positive", e)
			}
		})
	}
	for _, bad := range [][]byte{append([]byte(`{"schema":"other",`), raw[1:]...), append([]byte(`{"unknown":0,`), raw[1:]...)} {
		if _, e := DecodeResult(context.Background(), i, selection, bad); e == nil {
			t.Fatal("duplicate/unknown key")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := DecodeResult(ctx, i, selection, raw); !errors.Is(e, typedindex.Canceled) {
		t.Fatal(e)
	}
}

func TestResultPredecodeBounds(t *testing.T) {
	for _, at := range []string{"legs", "raw_plan/targets", "raw_plan/units", "raw_plan/documents", "raw_plan/sdks", "raw_plan/sdks/*/packages", "go_files/packages", "legs/*/call/result/exports"} {
		limit := resultArrayLimit(at)
		payload := "[" + strings.Repeat("{},", limit) + "{}]"
		parts := strings.Split(at, "/")
		for j := len(parts) - 1; j >= 0; j-- {
			if parts[j] == "*" {
				payload = "[" + payload + "]"
			} else {
				payload = `{"` + parts[j] + `":` + payload + `}`
			}
		}
		if e := resultDimensions(context.Background(), []byte(payload)); !errors.Is(e, typedindex.Capacity) {
			t.Fatal(at, e)
		}
	}
}

func TestResultPredecodeAggregateEdges(t *testing.T) {
	half := strings.Repeat("{},", typedindex.MaxBundleEdges/2) + "{}"
	raw := []byte(`{"raw_plan":{"units":[{"imports":[` + half + `]},{"imports":[` + half + `]}]}}`)
	if e := resultDimensions(context.Background(), raw); !errors.Is(e, typedindex.Capacity) {
		t.Fatal(e)
	}
}

func TestResultPlanningAndFailure(t *testing.T) {
	ctx := context.Background()
	i, s, p, m, selection := fixture(t)
	var events []string
	o := neutralOperations(t, s, p, m, selection, &events)
	raw, e := run(ctx, i, o)
	if e != nil {
		t.Fatal(e)
	}
	planned, e := DecodeResult(ctx, i, selection, raw)
	if e != nil || planned.Plan().Digest() == "" {
		t.Fatal(e)
	}
	if _, e = planned.Finalize(ctx); !errors.Is(e, typedindex.Invalid) {
		t.Fatal("planning gained publication", e)
	}
	o.plan = func(context.Context, []string) (planner.Plan, error) {
		return planner.Plan{}, &PlanningCommandError{Cause: typedindex.ExecutionFailed, Scratch: PlanningScratch{Available: true, FreeBlocks: 9, FreeInodes: 3}}
	}
	raw, e = run(ctx, i, o)
	if e == nil {
		t.Fatal("expected planning failure")
	}
	failed, e := DecodeResult(ctx, i, selection, raw)
	if !errors.Is(e, typedindex.ExecutionFailed) || failed.Failure() == nil {
		t.Fatal(e)
	}
	failed.Failure().PlanningScratch.FreeBlocks = 123
	if failed.Failure().PlanningScratch.FreeBlocks != 9 {
		t.Fatal("failure alias")
	}
	if _, e = failed.Finalize(ctx); !errors.Is(e, typedindex.Invalid) {
		t.Fatal("failure gained publication", e)
	}
	f, e := decode[Result](raw, typedsandbox.OutputBytes)
	if e != nil {
		t.Fatal(e)
	}
	f.Failure.PlanningProcess = &ProcessDiagnostic{Comm: "name\nline", State: "S", CapPrm: "0000000000000000"}
	if decoded, err := DecodeResult(ctx, i, selection, resultWire(t, f)); !errors.Is(err, typedindex.ExecutionFailed) || decoded.Failure() == nil || decoded.Failure().PlanningProcess.Comm != "name\nline" {
		t.Fatal("kernel comm diagnostic lost", err)
	}
	f.SCIP = []byte("payload on failure")
	if _, e = DecodeResult(ctx, i, selection, resultWire(t, f)); e == nil {
		t.Fatal("failure hid payload")
	}
}

func TestResultBlankReferencesBeforeOmission(t *testing.T) {
	i, selection, raw := admittedResultFixture(t, true, true)
	for _, kind := range []string{"occurrence", "enclosing", "signature", "unreferenced"} {
		t.Run(kind, func(t *testing.T) {
			r, e := decode[Result](raw, typedsandbox.OutputBytes)
			if e != nil {
				t.Fatal(e)
			}
			var index scip.Index
			if e = proto.Unmarshal(r.SCIP, &index); e != nil {
				t.Fatal(e)
			}
			blank := "scip-go gomod example.test/lib v1 pkg/_."
			index.Documents[0].Symbols = []*scip.SymbolInformation{{Symbol: blank, Documentation: []string{"first"}}, {Symbol: blank, Documentation: []string{"second"}}}
			omitted := index.Documents[1]
			switch kind {
			case "occurrence":
				omitted.Occurrences = []*scip.Occurrence{{Range: []int32{0, 0, 1}, Symbol: blank}}
			case "enclosing":
				omitted.Symbols = []*scip.SymbolInformation{{Symbol: "local name", EnclosingSymbol: blank}}
			case "signature":
				omitted.Symbols = []*scip.SymbolInformation{{Symbol: "local name", SignatureDocumentation: &scip.Signature{Occurrences: []*scip.Occurrence{{Range: []int32{0, 0, 1}, Symbol: blank}}}}}
			}
			r.SCIP, e = proto.Marshal(&index)
			if e != nil {
				t.Fatal(e)
			}
			r.SCIPSHA256 = hash(r.SCIP)
			decoded, e := DecodeResult(context.Background(), i, selection, resultWire(t, r))
			if kind != "unreferenced" {
				if !errors.Is(e, typedindex.Unsupported) {
					t.Fatal("omitted reference erased", e)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			bundle, e := decoded.Finalize(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			var manifest typedindex.AttemptManifest
			if e = json.Unmarshal(bundle.AttemptBytes(), &manifest); e != nil {
				t.Fatal(e)
			}
			if len(manifest.SCIPGo.Members[0].Mappings) != 2 || len(manifest.Documents) != 1 {
				t.Fatal("lost metadata or omitted document retained")
			}
		})
	}
}

func TestResultZeroRetainedCoverage(t *testing.T) {
	i, selection, raw := admittedResultFixture(t, true, true)
	s, e := bindSelection(context.Background(), i, selection)
	if e != nil {
		t.Fatal(e)
	}
	r, e := decode[Result](raw, typedsandbox.OutputBytes)
	if e != nil {
		t.Fatal(e)
	}
	r.RawPlan.Units[0].GoFiles = []string{"generated"}
	r.RawPlan.Units[0].CompiledGoFiles = []string{"generated"}
	sealRaw(&r.RawPlan)
	r.GoFiles.MappingSHA256, r.GoFiles.DocumentsSHA256 = r.RawPlan.MappingSHA256, r.RawPlan.DocumentsSHA256
	r.GoFiles.Packages[0].GoFiles = []string{launcher.ExecRoot + "/bazel-out/cfg/bin/lib/generated.go"}
	i = executing(t, i, s, r.RawPlan)
	roots, e := configuredRoots(r.RawPlan, s)
	if e != nil {
		t.Fatal(e)
	}
	p, docs, units, e := mapPlan(context.Background(), i, s, r.RawPlan, roots)
	if e != nil {
		t.Fatal(e)
	}
	r.Plan, r.Documents, r.Units, r.RequestDigest = p.Bytes(), docs, units, i.Execution.Digest()
	r.Legs = []LegEvidence{verifiedNeutralLeg(t, r.RawPlan, roots, r.GoFiles, s, "load"), verifiedNeutralLeg(t, r.RawPlan, roots, r.GoFiles, s, "scip")}
	var index scip.Index
	if e = proto.Unmarshal(r.SCIP, &index); e != nil {
		t.Fatal(e)
	}
	index.Documents = index.Documents[1:]
	r.SCIP, e = proto.Marshal(&index)
	if e != nil {
		t.Fatal(e)
	}
	r.SCIPSHA256 = hash(r.SCIP)
	decoded, e := DecodeResult(context.Background(), i, selection, resultWire(t, r))
	if e != nil {
		t.Fatal(e)
	}
	if b, e := decoded.Finalize(context.Background()); !errors.Is(e, typedindex.Unsupported) || b.RootDigest() != "" {
		t.Fatal("empty coverage gained current evidence", e)
	}
}

func TestResultInclusiveLegTiming(t *testing.T) {
	i, selection, raw := admittedResultFixture(t, false, false)
	r, e := decode[Result](raw, typedsandbox.OutputBytes)
	if e != nil {
		t.Fatal(e)
	}
	// A leg includes trace/source verification after its <=120s child. The
	// overall300s bound remains unchanged and is shared by sequential legs.
	r.Legs[0].WallNanoseconds = int64(launcher.MaxWall) + 1
	if _, e = DecodeResult(context.Background(), i, selection, resultWire(t, r)); e != nil {
		t.Fatal("leg inclusive timing refused", e)
	}
	r.Legs[1].WallNanoseconds = int64(typedsandbox.WallLimit) - r.Legs[0].WallNanoseconds + 1
	if _, e = DecodeResult(context.Background(), i, selection, resultWire(t, r)); e == nil {
		t.Fatal("aggregate wall exceeded")
	}
}
