package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/spike/t451a/planner"
)

func seal(plan *planner.Plan) {
	universe := append([]planner.Target{}, plan.Targets...)
	for i := range universe {
		universe[i].Units = nil
	}
	plan.UniverseSHA256 = jsonHash(universe)
	plan.MappingSHA256 = jsonHash(struct {
		Targets []planner.Target
		Units   []planner.Unit
		SDKs    []planner.SDKPlan
	}{plan.Targets, plan.Units, plan.SDKs})
	plan.DocumentsSHA256 = jsonHash(struct {
		Documents []planner.Document
		SDKs      []planner.SDKPlan
	}{plan.Documents, plan.SDKs})
}

func fixture() (planner.Plan, []planner.Configured) {
	config := strings.Repeat("a", 64)
	owner := planner.Configured{Label: "@@//lib:lib", Configuration: config}
	dep := planner.Configured{Label: "@@external+//pkg:pkg", Configuration: config}
	alias := planner.Configured{Label: "@@//lib:alias", Configuration: config}
	main := planner.Unit{ID: "main", Owner: owner, ArchiveLabel: owner.Label, Variant: "lib|bazel-out/cfg/bin/lib/lib.x", ImportPath: "example.test/lib", PackageName: "lib", GoFiles: []string{"source", "generated"}, CompiledGoFiles: []string{"source", "generated"}, Imports: []planner.UnitImport{{Path: "example.test/external", Unit: "external"}}, SourceImports: []string{"example.test/external", "unsafe"}, SDK: "sdk"}
	external := planner.Unit{ID: "external", Owner: dep, ArchiveLabel: dep.Label, Variant: "pkg|bazel-out/cfg/bin/external/external+/pkg/pkg.x", ImportPath: "example.test/external", PackageName: "pkg", GoFiles: []string{"external"}, CompiledGoFiles: []string{"external"}}
	plan := planner.Plan{Version: "phebs-t451a-plan-v1", Targets: []planner.Target{{Configured: owner, Kind: "go_library", Units: []string{main.ID}}, {Configured: dep, Kind: "go_library", Units: []string{external.ID}}, {Configured: alias, Kind: "alias", Units: []string{main.ID}}}, Units: []planner.Unit{main, external}, Documents: []planner.Document{
		{ID: "source", Kind: "source", Path: "lib/lib.go", ExecPath: "lib/lib.go", SHA256: hash([]byte("package lib\n")), Bytes: 12},
		{ID: "generated", Kind: "generated", Path: "lib/generated.go", ExecPath: "bazel-out/cfg/bin/lib/generated.go", Producer: owner, SHA256: config, Bytes: 12},
		{ID: "external", Kind: "external", Repository: "external+", Path: "pkg/pkg.go", ExecPath: "external/external+/pkg/pkg.go", SHA256: config, Bytes: 12},
	}, SDKs: []planner.SDKPlan{{ID: "sdk", Owner: planner.Configured{Label: "@@rules_go+//:stdlib", Configuration: config}, SDKProjection: planner.SDKProjection{Version: "1.25.0", Root: "external/go_sdk+", Prefix: "@@rules_go+//stdlib:", Packages: []planner.SDKPackage{{ID: "@@rules_go+//stdlib:unsafe", Name: "unsafe", ImportPath: "unsafe", GoFiles: []string{"external/go_sdk+/src/unsafe/unsafe.go"}, CompiledGoFiles: []string{"external/go_sdk+/src/unsafe/unsafe.go"}}}, Documents: []planner.SDKDocument{{Path: "src/unsafe/unsafe.go", ExecPath: "external/go_sdk+/src/unsafe/unsafe.go", SHA256: config, Bytes: 12}}}}}}
	seal(&plan)
	for i := range plan.Units {
		plan.Units[i].Mode = planner.GoMode{GOOS: "linux", GOARCH: "arm64", Tags: []string{}}
	}
	for i := range plan.SDKs {
		plan.SDKs[i].Mode = planner.GoMode{GOOS: "linux", GOARCH: "arm64", Tags: []string{}}
	}
	seal(&plan)
	return plan, []planner.Configured{alias, owner}
}

func exactResponse(p Prepared) response {
	r := response{Roots: append([]string{}, p.roots...)}
	for _, pkg := range p.packages {
		r.Packages = append(r.Packages, pkg)
	}
	return r
}

func TestClosedResponseEquality(t *testing.T) {
	plan, roots := fixture()
	p, err := Prepare(plan, roots)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.roots) != 1 || len(p.packages) != 3 || len(p.files) != 4 {
		t.Fatal("lost many-target/package or SDK document mapping")
	}
	good, _ := json.Marshal(exactResponse(p))
	if err := Reconcile(p, good); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*response)
	}{
		{"fallback", func(r *response) { r.NotHandled = true }},
		{"missing root", func(r *response) { r.Roots = nil }},
		{"extra root", func(r *response) { r.Roots = append(r.Roots, "@@//extra:lib") }},
		{"duplicate root", func(r *response) { r.Roots = append(r.Roots, r.Roots[0]) }},
		{"missing package", func(r *response) { r.Packages = r.Packages[1:] }},
		{"extra package", func(r *response) { r.Packages = append(r.Packages, flatPackage{ID: "extra"}) }},
		{"duplicate package", func(r *response) { r.Packages[0] = r.Packages[1] }},
		{"package error", func(r *response) { r.Packages[0].Errors = []json.RawMessage{json.RawMessage(`{"Msg":"failed"}`)} }},
		{"package name", func(r *response) { r.Packages[0].Name = "wrong" }},
		{"missing source", func(r *response) { r.Packages[0].GoFiles = nil }},
		{"extra compiled source", func(r *response) {
			r.Packages[0].CompiledGoFiles = append(r.Packages[0].CompiledGoFiles, "/outside.go")
		}},
		{"wrong generated path", func(r *response) {
			for i := range r.Packages {
				if r.Packages[i].ID == "@@//lib:lib" {
					r.Packages[i].CompiledGoFiles = []string{Workspace + "/lib/generated.go", Workspace + "/lib/lib.go"}
				}
			}
		}},
		{"wrong import", func(r *response) { r.Packages[0].Imports = map[string]string{"outside": "outside"} }},
		{"missing SDK import", func(r *response) {
			for i := range r.Packages {
				if r.Packages[i].ID == "@@//lib:lib" {
					r.Packages[i].Imports = map[string]string{"example.test/external": "@@external+//pkg:pkg"}
				}
			}
		}},
		{"extra non-Go files", func(r *response) { r.Packages[0].OtherFiles = []string{"/outside.c"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var r response
			if err := json.Unmarshal(good, &r); err != nil {
				t.Fatal(err)
			}
			tc.mutate(&r)
			data, _ := json.Marshal(r)
			if err := Reconcile(p, data); err == nil {
				t.Fatal("accepted driver mismatch")
			}
		})
	}
	for _, data := range [][]byte{nil, []byte(`{}`), append(append([]byte{}, good...), []byte(`{}`)...), append([]byte(`{"NotHandled":false,`), good[1:]...), append([]byte(`{"notHandled":false,`), good[1:]...), []byte(`{"unknown":true}`)} {
		if err := Reconcile(p, data); err == nil {
			t.Fatal("accepted invalid driver JSON")
		}
	}
}

func TestRefuseLossyDriverBeforeExecution(t *testing.T) {
	plan, roots := fixture()
	u := plan.Units[0]
	u.ID = "second"
	u.Owner.Configuration = strings.Repeat("b", 64)
	plan.Units = append(plan.Units, u)
	plan.Targets = append(plan.Targets, planner.Target{Configured: u.Owner, Kind: "go_library", Units: []string{u.ID}})
	roots = append(roots, u.Owner)
	seal(&plan)
	if _, err := Run(context.Background(), plan, roots, strings.Repeat("0", 64)); !errors.Is(err, ErrUnrepresentable) {
		t.Fatalf("did not stop before platform/process boundary: %v", err)
	}
}

func TestClosedInvocationAndSDKAuthority(t *testing.T) {
	t.Setenv("GOPACKAGESDRIVER_BAZEL", "/tmp/hostile")
	t.Setenv("GOPACKAGESDRIVER_BAZEL_FLAGS", "--bazelrc=/tmp/hostile")
	t.Setenv("GOTAGS", "hostile")
	plan, roots := fixture()
	p, err := Prepare(plan, roots)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(p.environment, "\n"), "hostile") || strings.Contains(string(p.request), "hostile") || len(p.patterns) != 1 || p.patterns[0] != "example.test/lib" {
		t.Fatal("ambient configuration reached driver")
	}
	if string(p.request) != `{"mode":31,"env":[],"build_flags":[],"tests":false,"overlay":{}}` {
		t.Fatal("driver request is open")
	}
	plan.SDKs = nil
	seal(&plan)
	if _, err := Prepare(plan, roots); !errors.Is(err, ErrUnrepresentable) {
		t.Fatalf("driver could invent SDK authority: %v", err)
	}
	plan, roots = fixture()
	plan.Documents[0].ExecPath = "../escape.go"
	if _, err := Prepare(plan, roots); err == nil {
		t.Fatal("accepted changed sealed plan")
	}
}

func TestByteCancellationAndSourceMutationBoundary(t *testing.T) {
	cancelled := false
	budget := outputBudget{remaining: 3, cancel: func() { cancelled = true }}
	w := outputWriter{budget: &budget}
	if _, err := w.Write([]byte("four")); err == nil || !cancelled || w.data.Len() != 0 {
		t.Fatal("output overflow did not cancel before buffering")
	}
	name := filepath.Join(t.TempDir(), "source.go")
	data := []byte("package p\n")
	if err := os.WriteFile(name, data, 0600); err != nil {
		t.Fatal(err)
	}
	files := map[string]fileIdentity{name: {hash(data), len(data)}}
	if err := verifyFiles(context.Background(), files); err != nil {
		t.Fatal(err)
	}
	p := Prepared{digest: strings.Repeat("a", 64), roots: []string{"one"}, packages: map[string]flatPackage{"one": {ID: "one", Name: "p", PkgPath: "one", GoFiles: []string{name}, CompiledGoFiles: []string{name}}}, files: files}
	responseBytes, _ := json.Marshal(exactResponse(p))
	if err := reconcileAndVerifyFiles(context.Background(), p, responseBytes); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte("package q\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifyFiles(context.Background(), files); err == nil {
		t.Fatal("accepted changed declared source")
	}
	if err := Reconcile(p, responseBytes); err != nil {
		t.Fatal("fixture should retain identical response paths")
	}
	if err := reconcileAndVerifyFiles(context.Background(), p, responseBytes); err == nil {
		t.Fatal("accepted source mutation during child despite exact response paths")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := verifyFiles(ctx, files); !errors.Is(err, context.Canceled) {
		t.Fatal("source validation ignored cancellation")
	}
}

func TestBazelShimRefusesScopeExpansionBeforeExecution(t *testing.T) {
	plan, roots := fixture()
	p, err := Prepare(plan, roots)
	if err != nil {
		t.Fatal(err)
	}
	var s scope
	if err := json.Unmarshal(p.scope, &s); err != nil {
		t.Fatal(err)
	}
	query := queryArgs(s)
	got, argv, err := inspectBazel(s, query)
	if err != nil || argv != nil || string(got) != "@@//lib:lib\n" {
		t.Fatalf("query changed sealed roots: %q %v", got, err)
	}
	info, argv, err := inspectBazel(s, commandPrefix("info"))
	if err != nil || argv != nil || !strings.Contains(string(info), "release: release 9.2.0\n") {
		t.Fatal("closed info profile failed")
	}
	build := buildArgs(s, "/scratch/tmp/gopackagesdriver_bep_1234")
	if out, argv, err := inspectBazel(s, build); err != nil || out != nil || len(argv) == 0 {
		t.Fatal("valid exact build shape refused")
	}
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"unselected select branch", append(append([]string{}, build...), "@@//unselected:branch")},
		{"repo rc", append(append([]string{}, build...), "--bazelrc=/scratch/workspace/.bazelrc")},
		{"overlay/environment flag", append(append([]string{}, build...), "--action_env=GOPACKAGESDRIVER=/scratch/workspace/driver")},
		{"BEP escape", buildArgs(s, "/scratch/tmp/../outside")},
		{"arbitrary query", append(commandPrefix("query"), "//...")},
		{"unknown info key", append(commandPrefix("info"), "server_pid")},
		{"missing selected root", build[:len(build)-1]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := inspectBazel(s, tc.args); err == nil {
				t.Fatal("accepted unsealed Bazel call")
			}
		})
	}
	invocation := p.Invocation()
	invocation.Environment[0] = "PATH=/hostile"
	invocation.Arguments[0] = "./..."
	if strings.Contains(strings.Join(p.environment, "\n"), "hostile") || p.patterns[0] != "example.test/lib" {
		t.Fatal("invocation report exposes mutable launch state")
	}
}
