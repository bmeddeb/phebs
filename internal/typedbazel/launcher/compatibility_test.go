package launcher

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/bmeddeb/phebs/internal/typedbazel/planner"
)

func compatibilityFixture() (planner.Plan, []planner.Configured) {
	plan, roots := fixture()
	plan.Units[1].SDK = "sdk"
	seal(&plan)
	return plan, roots
}

func compatibilityFileFixture(t *testing.T) (Prepared, []byte) {
	t.Helper()
	plan, roots := compatibilityFixture()
	p, err := PrepareCompatibility(plan, roots, "load")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "source.go")
	sourceBytes := []byte("package lib\n")
	if err := os.WriteFile(source, sourceBytes, 0600); err != nil {
		t.Fatal(err)
	}
	p.files = map[string]fileIdentity{source: {digest: hash(sourceBytes), bytes: len(sourceBytes)}}
	for id, pkg := range p.packages {
		pkg.GoFiles, pkg.CompiledGoFiles = []string{source}, []string{source}
		if pkg.ExportFile != "" {
			pkg.ExportFile = filepath.Join(dir, pkg.Name+".x")
			if err := os.WriteFile(pkg.ExportFile, []byte("sealed export for "+id), 0600); err != nil {
				t.Fatal(err)
			}
		}
		p.packages[id] = pkg
	}
	raw, err := json.Marshal(exactResponse(p))
	if err != nil {
		t.Fatal(err)
	}
	return p, append(append([]byte(" \n"), raw...), '\n')
}

func TestCompatibilityBoundary(t *testing.T) {
	t.Run("closed profile preserves original", func(t *testing.T) {
		t.Setenv("GOPACKAGESDRIVER_BAZEL", "/hostile")
		t.Setenv("GOFLAGS", "-tags=hostile")
		plan, roots := compatibilityFixture()
		old, err := Prepare(plan, roots)
		if err != nil {
			t.Fatal(err)
		}
		p, err := PrepareCompatibility(plan, roots, "load")
		if err != nil {
			t.Fatal(err)
		}
		other, err := PrepareCompatibility(plan, roots, "scip")
		if err != nil {
			t.Fatal(err)
		}
		if CompatibilityMode != 8681 || string(p.request) != `{"mode":8681,"env":[],"build_flags":null,"tests":false,"overlay":null}` {
			t.Fatal("typed wire changed")
		}
		if string(old.request) != `{"mode":31,"env":[],"build_flags":[],"tests":false,"overlay":{}}` || bytes.Equal(old.scope, p.scope) || p.Digest() == old.Digest() || p.Digest() == other.Digest() || bytes.Equal(p.scope, other.scope) {
			t.Fatal("legacy or per-slot identity changed/collapsed")
		}
		if !slices.Equal(old.patterns, p.patterns) || !slices.Equal(old.roots, p.roots) || strings.Contains(strings.Join(p.environment, "\n"), "hostile") {
			t.Fatal("compatibility changed scope or admitted ambient configuration")
		}
		if !slices.Contains(p.environment, "GOPACKAGESDRIVER_BAZEL=/scratch/t451b-load-bazel") || !slices.Contains(other.environment, "GOPACKAGESDRIVER_BAZEL=/scratch/t451b-scip-bazel") {
			t.Fatal("typed wrapper path is not bound to its slot")
		}
		for _, value := range p.environment {
			if strings.HasPrefix(value, "GOPACKAGESDRIVER_BAZEL=") || strings.HasPrefix(value, ScopeDigestEnv+"=") {
				continue
			}
			if !slices.Contains(old.environment, value) {
				t.Fatal("typed driver environment broadened")
			}
		}
		after, err := Prepare(plan, roots)
		if err != nil || after.Digest() != old.Digest() || !bytes.Equal(after.scope, old.scope) || !slices.Equal(after.environment, old.environment) {
			t.Fatal("compatibility mutated original prepared state")
		}
		for _, slot := range []string{"", "LOAD", "load/../scip", "load ", "other"} {
			if _, err := RunCompatibility(context.Background(), plan, roots, slot); err == nil || !strings.Contains(err.Error(), "slot") {
				t.Fatalf("slot %q reached execution: %v", slot, err)
			}
			if err := RunCompatibilityBazel(context.Background(), slot, nil); err == nil || !strings.Contains(err.Error(), "slot") {
				t.Fatalf("slot %q reached control file access: %v", slot, err)
			}
			if err := VerifyCompatibilityFiles(context.Background(), plan, roots, slot, nil); err == nil || !strings.Contains(err.Error(), "slot") {
				t.Fatalf("slot %q reached post-client file access: %v", slot, err)
			}
		}
	})

	t.Run("uniform SDK and sealed closure", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			change func(*planner.Plan)
		}{
			{"missing SDK without stdlib import", func(p *planner.Plan) { p.Units[1].SDK = "" }},
			{"wrong SDK release", func(p *planner.Plan) { p.SDKs[0].Version = "1.26.0" }},
			{"wrong SDK without stdlib import", func(p *planner.Plan) {
				other := p.SDKs[0]
				other.ID, other.Version = "other-sdk", "1.26.0"
				p.SDKs = append(p.SDKs, other)
				p.Units[1].SDK = other.ID
			}},
			{"mixed archive mode", func(p *planner.Plan) { p.Units[1].Mode.Cgo = true }},
			{"SDK mode mismatch", func(p *planner.Plan) { p.SDKs[0].Mode.Cgo = true }},
			{"mixed architecture", func(p *planner.Plan) { p.Units[1].Mode.GOARCH = "amd64" }},
			{"wrong platform", func(p *planner.Plan) {
				for i := range p.Units {
					p.Units[i].Mode.GOARCH = "386"
				}
			}},
			{"missing selected package", func(p *planner.Plan) { p.Units = p.Units[:1] }},
		} {
			t.Run(tc.name, func(t *testing.T) {
				plan, roots := compatibilityFixture()
				tc.change(&plan)
				seal(&plan)
				if _, err := PrepareCompatibility(plan, roots, "load"); err == nil {
					t.Fatal("accepted unsupported typed authority")
				}
			})
		}
		plan, roots := compatibilityFixture()
		plan.SDKs[0].Version = "1.26.0"
		if _, err := PrepareCompatibility(plan, roots, "load"); err == nil {
			t.Fatal("accepted changed plan seal")
		}
		plan, roots = compatibilityFixture()
		unselected := plan.Units[1]
		unselected.ID, unselected.SDK = "unselected", "missing"
		unselected.Owner.Configuration = strings.Repeat("b", 64)
		plan.Units = append(plan.Units, unselected)
		seal(&plan)
		if _, err := PrepareCompatibility(plan, roots, "load"); err != nil {
			t.Fatal("unselected same-label unit changed selected SDK authority:", err)
		}
	})

	t.Run("one client chunk", func(t *testing.T) {
		patterns := []string{strings.Repeat("a", 4096), strings.Repeat("b", 4096), strings.Repeat("c", 4096), strings.Repeat("d", 4091)}
		if err := compatibilityPatterns(patterns); err != nil {
			t.Fatal(err)
		}
		patterns[3] += "d"
		if err := compatibilityPatterns(patterns); err == nil {
			t.Fatal("accepted client chunking")
		}
		plan, _ := compatibilityFixture()
		plan.Targets, plan.Units = nil, nil
		var roots []planner.Configured
		for i, pattern := range patterns {
			owner := planner.Configured{Label: fmt.Sprintf("@@//root:r%d", i), Configuration: strings.Repeat("a", 64)}
			id := fmt.Sprint(i)
			plan.Targets = append(plan.Targets, planner.Target{Configured: owner, Units: []string{id}})
			plan.Units = append(plan.Units, planner.Unit{ID: id, Owner: owner, ArchiveLabel: owner.Label, Variant: "root|bazel-out/" + id + ".x", ImportPath: pattern, SDK: "sdk", Mode: plan.SDKs[0].Mode})
			roots = append(roots, owner)
		}
		seal(&plan)
		if _, err := Prepare(plan, roots); err != nil {
			t.Fatal("fixture exceeds an original bound:", err)
		}
		if _, err := PrepareCompatibility(plan, roots, "load"); err == nil {
			t.Fatal("PrepareCompatibility accepted multiple client chunks")
		}
	})

	t.Run("scope and exact build groups", testCompatibilityScope)
	t.Run("raw parity precedes metadata", testCompatibilityResponse)
	t.Run("post-client exact inventory", testCompatibilityFiles)
	t.Run("bounded export materialization", testCompatibilityExportBounds)
	t.Run("concurrent immutable invocation", func(t *testing.T) {
		plan, roots := compatibilityFixture()
		p, err := PrepareCompatibility(plan, roots, "load")
		if err != nil {
			t.Fatal(err)
		}
		want := p.Digest()
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				invocation := p.Invocation()
				invocation.Arguments[0] = "./..."
				invocation.Environment[0] = "PATH=/hostile"
				if p.Digest() != want || p.patterns[0] != "example.test/lib" || strings.Contains(p.environment[0], "hostile") {
					t.Error("report mutated prepared authority")
				}
			})
		}
		wg.Wait()
		name := filepath.Join(t.TempDir(), "scope.json")
		results := make(chan error, 8)
		for range 8 {
			wg.Go(func() { results <- writeClosedFile(name, p.scope, 0400) })
		}
		wg.Wait()
		close(results)
		accepted := 0
		for err := range results {
			if err == nil {
				accepted++
			}
		}
		if accepted != 1 {
			t.Fatalf("concurrent control writers accepted: %d", accepted)
		}
		if err := writeClosedFile(name, []byte("replacement"), 0400); err == nil {
			t.Fatal("reused immutable control")
		}
		data, err := os.ReadFile(name)
		if err != nil || !bytes.Equal(data, p.scope) {
			t.Fatal("refusal altered control bytes")
		}
	})
}

func testCompatibilityScope(t *testing.T) {
	plan, roots := compatibilityFixture()
	p, err := PrepareCompatibility(plan, roots, "load")
	if err != nil {
		t.Fatal(err)
	}
	s, err := inspectCompatibilityScope(p.scope, hash(p.scope), "load")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inspectCompatibilityScope(p.scope, hash(p.scope), "scip"); err == nil {
		t.Fatal("slot substitution")
	}
	if _, err := inspectCompatibilityScope(p.scope, strings.Repeat("0", 64), "load"); err == nil {
		t.Fatal("scope substitution")
	}
	oversized := bytes.Repeat([]byte(" "), (1<<20)+1)
	if _, err := inspectCompatibilityScope(oversized, hash(oversized), "load"); err == nil {
		t.Fatal("oversized scope")
	}
	for _, change := range []func(string) string{
		func(s string) string { return s + "\n" },
		func(s string) string {
			return strings.Replace(s, compatibilityScopeVersion, "phebs-t451a-driver-scope-v1", 1)
		},
		func(s string) string { return strings.Replace(s, `"slot":"load"`, `"slot":"load","slot":"load"`, 1) },
		func(s string) string { return strings.Replace(s, `"slot":`, `"Slot":`, 1) },
		func(s string) string { return strings.Replace(s, `"slot":`, `"unknown":true,"slot":`, 1) },
		func(s string) string { return strings.Replace(s, plan.MappingSHA256, strings.Repeat("g", 64), 1) },
		func(s string) string { return strings.Replace(s, `"roots":["@@//lib:lib"]`, `"roots":null`, 1) },
		func(s string) string { return strings.Replace(s, `"example.test/lib"`, `"./..."`, 1) },
	} {
		changed := []byte(change(string(p.scope)))
		if bytes.Equal(changed, p.scope) {
			t.Fatal("ineffective scope mutation")
		}
		if _, err := inspectCompatibilityScope(changed, hash(changed), "load"); err == nil {
			t.Fatal("accepted changed scope shape")
		}
	}
	old := buildArgs(s, "/scratch/tmp/gopackagesdriver_bep_1234")
	typed := slices.Clone(old)
	for i, arg := range typed {
		if strings.HasPrefix(arg, "--output_groups=") {
			typed[i] = compatibilityGroups
		}
	}
	if _, argv, err := inspectCompatibilityBazel(s, typed); err != nil || !slices.Equal(argv, typed) {
		t.Fatal("typed build refused:", err)
	}
	if _, _, err := inspectBazel(s, typed); err == nil {
		t.Fatal("legacy build accepts typed groups")
	}
	for _, args := range [][]string{
		old,
		append(slices.Clone(typed), "@@//unselected:branch"),
		append(slices.Clone(typed), "--bazelrc=/outside"),
		append(slices.Clone(typed), "--action_env=GOPACKAGESDRIVER=/outside"),
		append(slices.Clone(typed), compatibilityGroups),
		typed[:len(typed)-1],
		append(commandPrefix("query"), "//..."),
	} {
		if _, _, err := inspectCompatibilityBazel(s, args); err == nil {
			t.Fatal("accepted changed typed build")
		}
	}
	for _, command := range [][]string{commandPrefix("info"), queryArgs(s)} {
		if output, argv, err := inspectCompatibilityBazel(s, command); err != nil || argv != nil || len(output) == 0 {
			t.Fatal("closed synthetic call refused:", err)
		}
	}
}

func testCompatibilityResponse(t *testing.T) {
	p, raw := compatibilityFileFixture(t)
	result, err := finishCompatibility(context.Background(), p, raw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(result.DriverResponse, raw) {
		t.Fatal("raw response bytes changed")
	}
	var before map[string]json.RawMessage
	if err := json.Unmarshal(raw, &before); err != nil {
		t.Fatal(err)
	}
	before["Compiler"], before["Arch"], before["GoVersion"] = json.RawMessage(`"gc"`), json.RawMessage(strconv.Quote(runtime.GOARCH)), json.RawMessage(`25`)
	want, err := json.Marshal(before)
	if err != nil || !bytes.Equal(want, result.Response) {
		t.Fatal("adaptation changed more than uniform metadata")
	}
	if err := Reconcile(p, result.Response); err == nil {
		t.Fatal("raw oracle accepted adapted metadata")
	}
	for _, tc := range []struct {
		name   string
		change func(*response)
	}{
		{"compiler", func(r *response) { r.Compiler = "gc" }},
		{"architecture", func(r *response) { r.Arch = "arm64" }},
		{"version", func(r *response) { r.GoVersion = 25 }},
		{"fallback", func(r *response) { r.NotHandled = true }},
		{"missing graph member", func(r *response) { r.Packages = r.Packages[1:] }},
		{"extra graph member", func(r *response) { r.Packages = append(r.Packages, flatPackage{ID: "invented"}) }},
		{"missing export", func(r *response) {
			for i := range r.Packages {
				if r.Packages[i].ExportFile != "" {
					r.Packages[i].ExportFile = ""
					return
				}
			}
		}},
		{"extra export", func(r *response) { r.Packages[0].ExportFile = "/invented.x" }},
		{"import edge", func(r *response) { r.Packages[0].Imports = map[string]string{"invented": "invented"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var r response
			if err := json.Unmarshal(raw, &r); err != nil {
				t.Fatal(err)
			}
			tc.change(&r)
			changed, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := finishCompatibility(context.Background(), p, changed); err == nil {
				t.Fatal("adaptation hid raw mismatch")
			}
		})
	}
	for _, change := range []struct{ old, replacement string }{
		{`"mode":8681`, `"mode":31`},
		{`"env":[]`, `"env":["GOTAGS=hostile"]`},
		{`"build_flags":null`, `"build_flags":[]`},
		{`"overlay":null`, `"overlay":{}`},
		{`"tests":false`, `"tests":true`},
	} {
		p.request = []byte(strings.Replace(compatibilityRequest, change.old, change.replacement, 1))
		if _, err := finishCompatibility(context.Background(), p, raw); err == nil {
			t.Fatal("adapted unsupported typed request")
		}
	}
}

func testCompatibilityFiles(t *testing.T) {
	p, raw := compatibilityFileFixture(t)
	result, err := finishCompatibility(context.Background(), p, raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Exports) != 2 || result.Exports[0].Path >= result.Exports[1].Path {
		t.Fatal("export inventory not exact/sorted")
	}
	if err := verifyCompatibilityFiles(context.Background(), p, result.Exports); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func([]ExportArtifact) []ExportArtifact{
		func(e []ExportArtifact) []ExportArtifact { return e[:1] },
		func(e []ExportArtifact) []ExportArtifact { return append(e, e[0]) },
		func(e []ExportArtifact) []ExportArtifact { e[1] = e[0]; return e },
		func(e []ExportArtifact) []ExportArtifact { e[0], e[1] = e[1], e[0]; return e },
		func(e []ExportArtifact) []ExportArtifact { e[0].Path = "/outside.x"; return e },
		func(e []ExportArtifact) []ExportArtifact { e[0].SHA256 = "A" + strings.Repeat("0", 63); return e },
		func(e []ExportArtifact) []ExportArtifact { e[0].Bytes++; return e },
		func(e []ExportArtifact) []ExportArtifact { e[0].Bytes = 0; return e },
		func(e []ExportArtifact) []ExportArtifact { e[0].Bytes = planner.MaxFileBytes + 1; return e },
	} {
		if err := verifyCompatibilityFiles(context.Background(), p, change(slices.Clone(result.Exports))); err == nil {
			t.Fatal("accepted altered inventory")
		}
	}
	export := result.Exports[0]
	data, err := os.ReadFile(export.Path)
	if err != nil {
		t.Fatal(err)
	}
	changed := slices.Clone(data)
	changed[0] ^= 1
	if err := os.WriteFile(export.Path, changed, 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifyCompatibilityFiles(context.Background(), p, result.Exports); err == nil {
		t.Fatal("accepted post-client export mutation")
	}
	if err := os.WriteFile(export.Path, data, 0600); err != nil {
		t.Fatal(err)
	}
	for source := range p.files {
		if err := os.WriteFile(source, []byte("package bad\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := verifyCompatibilityFiles(context.Background(), p, result.Exports); err == nil {
		t.Fatal("accepted post-client source mutation")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := verifyCompatibilityFiles(ctx, p, result.Exports); !errors.Is(err, context.Canceled) {
		t.Fatal("ignored cancellation:", err)
	}
	if _, err := sealCompatibilityExports(ctx, p); !errors.Is(err, context.Canceled) {
		t.Fatal("ignored seal cancellation:", err)
	}
}

func testCompatibilityExportBounds(t *testing.T) {
	dir := t.TempDir()
	name := filepath.Join(dir, "export.x")
	p := Prepared{packages: map[string]flatPackage{"one": {ExportFile: name}}}
	if _, err := sealCompatibilityExports(context.Background(), p); err == nil {
		t.Fatal("accepted absent export")
	}
	if err := os.WriteFile(name, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := sealCompatibilityExports(context.Background(), p); err == nil {
		t.Fatal("accepted empty export")
	}
	if err := os.Truncate(name, planner.MaxFileBytes+1); err != nil {
		t.Fatal(err)
	}
	if _, err := sealCompatibilityExports(context.Background(), p); err == nil {
		t.Fatal("accepted oversized export")
	}
	if err := os.Truncate(name, planner.MaxFileBytes); err != nil {
		t.Fatal(err)
	}
	p.packages = make(map[string]flatPackage)
	var exports []ExportArtifact
	for i := range 9 {
		member := filepath.Join(dir, fmt.Sprintf("%d.x", i))
		if err := os.Link(name, member); err != nil {
			t.Fatal(err)
		}
		p.packages[fmt.Sprint(i)] = flatPackage{ExportFile: member}
		exports = append(exports, ExportArtifact{Path: member, Bytes: planner.MaxFileBytes, SHA256: strings.Repeat("a", 64)})
	}
	if _, err := sealCompatibilityExports(context.Background(), p); err == nil {
		t.Fatal("accepted aggregate export overflow")
	}
	if err := verifyCompatibilityFiles(context.Background(), p, exports); err == nil {
		t.Fatal("accepted aggregate inventory overflow")
	}
	delete(p.packages, "8")
	sealed, err := sealCompatibilityExports(context.Background(), p)
	if err != nil || len(sealed) != 8 {
		t.Fatal("refused exact aggregate bound:", err)
	}
	// Multiple packages may declare one archive; inventory each path once.
	p.packages["duplicate"] = p.packages["0"]
	if err := verifyCompatibilityFiles(context.Background(), p, sealed); err != nil {
		t.Fatal("shared archive changed inventory:", err)
	}
}
