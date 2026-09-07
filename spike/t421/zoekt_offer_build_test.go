package t421

import (
	"context"
	"debug/buildinfo"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
	"time"
)

// A tiny actual Go-tool regression for replacement metadata/resolution only.
// This is not the pinned Zoekt image, its reproduction gate, or native offers.
func TestZoektOfferNativeReplacementMetadata(t *testing.T) {
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"home", "tmp", "cache", "bin", "modules", "zoekt", "zoekt/cmd", "zoekt/cmd/neutral"} {
		if err := os.Mkdir(filepath.Join(workspace, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	policy := frozenToolPolicy()
	base := "module github.com/bmeddeb/phebs\n\ngo 1.26.0\n\nrequire " + policy.ZoektModulePath + " " + policy.ZoektModuleVersion + "\n"
	files := map[string]string{
		"go.mod":                    base,
		"v3.mod":                    base + "\nreplace " + policy.ZoektModulePath + " " + policy.ZoektModuleVersion + " => ./zoekt\n",
		"zoekt/go.mod":              "module " + policy.ZoektModulePath + "\n\ngo 1.25.0\n",
		"zoekt/cmd/neutral/main.go": "package main\nfunc main() {}\n",
	}
	for name, value := range files {
		if err := os.WriteFile(filepath.Join(workspace, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	goRoot, _ := zoektOfferNativeGoRoots(t)
	request := ReferenceToolRequest{GoRoot: goRoot, ModuleCache: filepath.Join(workspace, "modules")}
	environment := referenceBuildEnvironment(request, workspace)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	output := filepath.Join(workspace, "neutral")
	raw, err := runReferenceGo(ctx, workspace, filepath.Join(request.GoRoot, "bin", "go"), environment, 64<<10,
		"build", "-trimpath", "-pgo=off", "-buildvcs=true", "-p=1", "-modfile=v3.mod", "-o", output, policy.ZoektModulePath+"/cmd/neutral")
	if err != nil {
		if writeErr := os.WriteFile(filepath.Join(workspace, "build-failure.log"), raw, 0o600); writeErr != nil {
			t.Fatal(writeErr)
		}
		t.Fatalf("neutral replacement build: %v", err)
	}
	info, err := buildinfo.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("native Go=%s main=%+v replacement=%+v", info.GoVersion, info.Main, info.Main.Replace)
	if info.Main.Path != policy.ZoektModulePath || info.Main.Version != policy.ZoektModuleVersion || info.Main.Sum != "" || info.Main.Replace == nil || info.Main.Replace.Path != "./zoekt" || info.Main.Replace.Version != "(devel)" || info.Main.Replace.Sum != "" || info.Main.Replace.Replace != nil {
		t.Fatal("actual replacement identity differs")
	}
}

func TestZoektOfferNativePinnedGraph(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	source, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	goRoot, cache := zoektOfferNativeGoRoots(t)
	driver := filepath.Join(goRoot, "bin", "go")
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"home", "tmp", "cache", "bin"} {
		if err := os.Mkdir(filepath.Join(workspace, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	root, overlay, check, err := prepareZoektOfferBuild(ctx, source, cache, workspace)
	if err != nil {
		t.Fatal(err)
	}
	environment := referenceBuildEnvironment(ReferenceToolRequest{GoRoot: goRoot, ModuleCache: cache}, workspace)
	baseline, err := runReferenceGo(ctx, source, driver, environment, maxReferenceModuleGraphBytes, "list", "-m", "-json", "all")
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"list", "-modfile=v3.mod", "-overlay=" + overlay, "-m", "-json", "all"}
	actual, err := runReferenceGo(ctx, root, driver, environment, maxReferenceModuleGraphBytes, args...)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyZoektOfferGraph(baseline, actual, source, root); err != nil {
		t.Fatal(err)
	}
	if err := check(); err != nil {
		t.Fatal(err)
	}
	after, err := runReferenceGo(ctx, root, driver, environment, maxReferenceModuleGraphBytes, args...)
	if err != nil || verifyZoektOfferGraph(baseline, after, source, root) != nil || string(after) != string(actual) {
		t.Fatal("actual private graph changed", err)
	}
	// Actual pinned package resolution traverses the overlay target without a
	// module-cache overlay. No compile/link of the full native image occurs.
	resolved, err := runReferenceGo(ctx, root, driver, environment, 64<<10, "list", "-modfile=v3.mod", "-overlay="+overlay,
		"-f", "{{.Dir}} {{.Module.GoVersion}}", frozenToolPolicy().ZoektModulePath+"/gitindex")
	if err != nil || string(resolved) != filepath.Join(root, "zoekt", "gitindex")+" 1.25.9\n" {
		t.Fatal(err)
	}
	changed := strings.Replace(string(actual), "./zoekt", "./other", 1)
	if verifyZoektOfferGraph(baseline, []byte(changed), source, root) == nil {
		t.Fatal("wrong local replacement admitted")
	}
	if err := os.WriteFile(filepath.Join(root, "zoekt", "unexpected"), []byte("new input"), 0o600); err != nil {
		t.Fatal(err)
	}
	if check() == nil {
		t.Fatal("extra module file admitted")
	}
	t.Log("actual pinned overlay Go-list resolution and exact graph comparison passed; no full image build")
}

func zoektOfferNativeGoRoots(t *testing.T) (string, string) {
	t.Helper()
	driver, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	raw, err := exec.CommandContext(ctx, driver, "env", "GOROOT", "GOMODCACHE").Output()
	roots := strings.Fields(string(raw))
	if err != nil || len(roots) != 2 {
		t.Fatal("native Go roots unavailable", err)
	}
	return roots[0], roots[1]
}

func TestZoektOfferPrivateBuildInfo(t *testing.T) {
	policy := frozenToolPolicy()
	info := &debug.BuildInfo{GoVersion: runtime.Version(), Path: policy.ZoektModulePath + "/cmd/zoekt-git-index",
		Main:     debug.Module{Path: policy.ZoektModulePath, Version: policy.ZoektModuleVersion, Replace: &debug.Module{Path: "./zoekt", Version: "(devel)"}},
		Settings: []debug.BuildSetting{{Key: "CGO_ENABLED", Value: "0"}, {Key: "-trimpath", Value: "true"}, {Key: "GOOS", Value: runtime.GOOS}, {Key: "GOARCH", Value: runtime.GOARCH}}}
	modules := map[string]string{policy.ZoektModulePath + "@" + policy.ZoektModuleVersion: policy.ZoektModuleSum}
	for _, name := range []string{"valid", "legacy", "wrong role", "absolute", "unversioned", "sum", "missing", "extra replacement", "wrong original version", "wrong original sum", "wrong graph"} {
		t.Run(name, func(t *testing.T) {
			candidate := *info
			main := info.Main
			replacement := *main.Replace
			main.Replace = &replacement
			candidate.Main = main
			role, schema := "zoekt-git-index", PlanV3Schema
			graph := modules
			switch name {
			case "legacy":
				schema = PlanV2Schema
			case "wrong role":
				role = "buf"
			case "absolute":
				replacement.Path = "/private/not-admitted"
			case "unversioned":
				replacement.Version = ""
			case "sum":
				replacement.Sum = "h1:invented"
			case "missing":
				candidate.Main.Replace = nil
			case "extra replacement":
				candidate.Deps = []*debug.Module{{Path: "extra", Version: "v1.0.0", Replace: &replacement}}
			case "wrong original version":
				candidate.Main.Version = "v0.0.0"
			case "wrong original sum":
				candidate.Main.Sum = policy.ZoektModuleSum
			case "wrong graph":
				graph = map[string]string{}
			}
			err := validateReferenceBuildInfoForSchema(&candidate, role, schema, info.Path, strings.Repeat("a", 40), policy.ZoektModulePath, policy.ZoektModuleVersion, policy.ZoektModuleSum, graph)
			if (err == nil) != (name == "valid") {
				t.Fatal(name, err)
			}
		})
	}
}

func TestZoektOfferGraphRefusals(t *testing.T) {
	policy := frozenToolPolicy()
	encode := func(values ...referenceGraphModule) []byte {
		t.Helper()
		var result []byte
		for _, value := range values {
			raw, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			// go list omits absent raw-valued metadata; struct marshaling
			// otherwise fabricates nulls absent from the native stream.
			for _, field := range []string{"Time", "Replace", "Error", "Update", "Origin"} {
				raw = []byte(strings.ReplaceAll(string(raw), `,"`+field+`":null`, ""))
			}
			result = append(result, raw...)
			result = append(result, '\n')
		}
		return result
	}
	main := referenceGraphModule{Path: "github.com/bmeddeb/phebs", Main: true, Dir: "/source", GoMod: "/source/go.mod", GoVersion: "1.26.0"}
	module := referenceGraphModule{Path: policy.ZoektModulePath, Version: policy.ZoektModuleVersion, Sum: policy.ZoektModuleSum,
		Dir: "/modules/zoekt", GoMod: "/modules/zoekt/go.mod", GoVersion: "1.25.9", GoModSum: "h1:baseline", Time: json.RawMessage(`"2026-07-09T06:41:01Z"`)}
	baseline := encode(main, module)
	for _, name := range []string{"valid", "main directory", "main descriptor", "module directory", "module version", "module sum", "module time", "language", "replace path", "replace version", "replace sum", "replace language", "replace unknown", "module unknown", "missing replace", "missing module", "extra module", "reorder", "truncated"} {
		t.Run(name, func(t *testing.T) {
			selectedMain, selected := main, module
			selectedMain.Dir, selectedMain.GoMod = "/build", "v3.mod"
			selected.Dir, selected.GoMod, selected.Sum, selected.GoModSum, selected.Time = "/build/zoekt", "/build/zoekt/go.mod", "", "", nil
			replacement := referenceGraphModule{Path: "./zoekt", Dir: selected.Dir, GoMod: selected.GoMod, GoVersion: "1.25.9"}
			switch name {
			case "main directory":
				selectedMain.Dir = "/other"
			case "main descriptor":
				selectedMain.GoMod = "/build/v3.mod"
			case "module directory":
				selected.Dir = "/other"
			case "module version":
				selected.Version = "v0.0.0"
			case "module sum":
				selected.Sum = policy.ZoektModuleSum
			case "module time":
				selected.Time = module.Time
			case "language":
				selected.GoVersion = "1.26.0"
			case "replace path":
				replacement.Path = "./other"
			case "replace version":
				replacement.Version = "(devel)"
			case "replace sum":
				replacement.Sum = policy.ZoektModuleSum
			case "replace language":
				replacement.GoVersion = "1.26.0"
			}
			selected.Replace = json.RawMessage(strings.TrimSpace(string(encode(replacement))))
			if name == "replace unknown" {
				selected.Replace = json.RawMessage(`{"Unexpected":true}`)
			}
			if name == "missing replace" {
				selected.Replace = nil
			}
			actual := encode(selectedMain, selected)
			switch name {
			case "module unknown":
				actual = append(actual, []byte(`{"Unexpected":true}`)...)
			case "missing module":
				actual = encode(selectedMain)
			case "extra module":
				actual = encode(selectedMain, selected, selected)
			case "reorder":
				actual = encode(selected, selectedMain)
			case "truncated":
				actual = actual[:len(actual)-2]
			}
			if err := verifyZoektOfferGraph(baseline, actual, "/source", "/build"); (err == nil) != (name == "valid") {
				t.Fatal(name, err)
			}
		})
	}
}
