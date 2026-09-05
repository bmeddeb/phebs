package t421

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/mod/module"
	"golang.org/x/mod/sumdb/dirhash"
)

type referenceModulesFixture struct {
	source, cache, modFile, directory, sum, modSum string
	main, dependency                               referenceGraphModule
}

// Opt-in evidence over the existing repository/cache, not a fixture or a tool
// build. The module cache must already be populated; the Go child is offline.
func TestVerifyExecutionModuleGraphRepositoryReplay(t *testing.T) {
	if os.Getenv("PHEBS_T422_REFERENCE_GRAPH_REPLAY") != "1" {
		t.Skip("set PHEBS_T422_REFERENCE_GRAPH_REPLAY=1 for the existing-cache repository replay")
	}
	moduleCache := os.Getenv("PHEBS_T422_REFERENCE_MODULE_CACHE")
	if moduleCache == "" {
		t.Skip("set PHEBS_T422_REFERENCE_MODULE_CACHE to an existing absolute module cache")
	}
	if !filepath.IsAbs(moduleCache) {
		t.Fatal("repository replay module cache must be absolute")
	}
	moduleCache, err := filepath.EvalSymlinks(moduleCache)
	if err != nil {
		t.Fatal(err)
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	source, err := filepath.EvalSymlinks(filepath.Join(workingDirectory, "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	sumPath := filepath.Join(source, "go.sum")
	before, err := os.ReadFile(sumPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		after, err := os.ReadFile(sumPath)
		if err != nil || !bytes.Equal(before, after) {
			t.Errorf("offline repository graph replay changed source go.sum: %v", err)
		}
	})
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"home", "cache", "tmp", "bin"} {
		if err := os.Mkdir(filepath.Join(workspace, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	goRootCommand := exec.CommandContext(ctx, goBinary, "env", "GOROOT")
	goRootCommand.Dir = workspace
	goRootCommand.Env = []string{
		"GOENV=off", "GOWORK=off", "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off",
		"PATH=/usr/bin:/bin", "LC_ALL=C", "HOME=" + filepath.Join(workspace, "home"),
	}
	goRootRaw, err := goRootCommand.Output()
	if err != nil {
		t.Fatal(err)
	}
	goRoot, err := filepath.EvalSymlinks(strings.TrimSpace(string(goRootRaw)))
	if err != nil {
		t.Fatal(err)
	}
	request := ReferenceToolRequest{RepositoryRoot: source, GoRoot: goRoot, ModuleCache: moduleCache}
	raw, err := runReferenceGo(ctx, source, filepath.Join(goRoot, "bin", "go"),
		referenceBuildEnvironment(request, workspace), maxReferenceModuleGraphBytes, "list", "-m", "-json", "all")
	if err != nil {
		t.Fatal(err)
	}
	verified, err := verifyExecutionModuleGraph(ctx, source, moduleCache, raw)
	if err != nil {
		t.Fatal(err)
	}
	policy := frozenToolPolicy()
	for key, want := range map[string]string{
		policy.BufModulePath + "@" + policy.BufModuleVersion:     policy.BufModuleSum,
		policy.ZoektModulePath + "@" + policy.ZoektModuleVersion: policy.ZoektModuleSum,
	} {
		if actual := verified[key]; actual != want {
			t.Fatalf("repository replay module %q checksum = %q, want %q", key, actual, want)
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	rows := 0
	for {
		var value json.RawMessage
		if err := decoder.Decode(&value); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		rows++
	}
	t.Logf("repository graph verified: %d rows, %d downloaded directories; frozen Buf and Zoekt sums exact", rows, len(verified))
}

func newReferenceModulesFixture(t *testing.T) referenceModulesFixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture := referenceModulesFixture{source: filepath.Join(root, "source"), cache: filepath.Join(root, "cache")}
	path, version := "example.invalid/Fixture", "v1.0.0"
	escaped, err := module.EscapePath(path)
	if err != nil {
		t.Fatal(err)
	}
	fixture.directory = filepath.Join(fixture.cache, filepath.FromSlash(escaped)+"@"+version)
	fixture.modFile = filepath.Join(fixture.cache, "cache", "download", filepath.FromSlash(escaped), "@v", version+".mod")
	modRaw := []byte("module " + path + "\n\ngo 1.26\n")
	writeReferenceModuleTestFile(t, filepath.Join(fixture.source, "go.mod"), []byte("module github.com/bmeddeb/phebs\n\ngo 1.26\n"))
	writeReferenceModuleTestFile(t, fixture.modFile, modRaw)
	writeReferenceModuleTestFile(t, filepath.Join(fixture.directory, "go.mod"), modRaw)
	writeReferenceModuleTestFile(t, filepath.Join(fixture.directory, "nested", "fixture.go"), []byte("package fixture\nconst Value = 1\n"))
	fixture.modSum, err = dirhash.Hash1([]string{"go.mod"}, func(string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(modRaw)), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.sum, err = dirhash.HashDir(fixture.directory, path+"@"+version, dirhash.Hash1)
	if err != nil {
		t.Fatal(err)
	}
	writeReferenceModuleTestFile(t, filepath.Join(fixture.source, "go.sum"), []byte(
		path+" "+version+" "+fixture.sum+"\n"+path+" "+version+"/go.mod "+fixture.modSum+"\n",
	))
	fixture.main = referenceGraphModule{
		Path: "github.com/bmeddeb/phebs", Main: true, Dir: fixture.source, GoMod: filepath.Join(fixture.source, "go.mod"),
	}
	fixture.dependency = referenceGraphModule{
		Path: path, Version: version, Dir: fixture.directory, GoMod: fixture.modFile, Sum: fixture.sum, GoModSum: fixture.modSum,
	}
	return fixture
}

func writeReferenceModuleTestFile(t *testing.T, path string, raw []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (fixture referenceModulesFixture) raw(t *testing.T) []byte {
	t.Helper()
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	for _, value := range []referenceGraphModule{fixture.main, fixture.dependency} {
		if err := encoder.Encode(value); err != nil {
			t.Fatal(err)
		}
	}
	return output.Bytes()
}

func TestVerifyExecutionModuleGraphBindsActualCacheBytes(t *testing.T) {
	fixture := newReferenceModulesFixture(t)
	got, err := verifyExecutionModuleGraph(t.Context(), fixture.source, fixture.cache, fixture.raw(t))
	key := fixture.dependency.Path + "@" + fixture.dependency.Version
	if err != nil || len(got) != 1 || got[key] != fixture.sum {
		t.Fatalf("verified module graph = %#v, %v", got, err)
	}
	// Missing advisory sums do not replace independent source-checksum proof.
	fixture.dependency.Sum, fixture.dependency.GoModSum = "", ""
	if got, err := verifyExecutionModuleGraph(t.Context(), fixture.source, fixture.cache, fixture.raw(t)); err != nil || got[key] != fixture.sum {
		t.Fatalf("independently measured sums = %#v, %v", got, err)
	}
	fixture.dependency.Dir = ""
	if got, err := verifyExecutionModuleGraph(t.Context(), fixture.source, fixture.cache, fixture.raw(t)); err != nil || len(got) != 0 {
		t.Fatalf("unneeded graph descriptor = %#v, %v", got, err)
	}
}

func TestVerifyExecutionModuleGraphRefusesCacheAndAuthorityDrift(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, *referenceModulesFixture)
	}{
		{"cached mod", func(t *testing.T, f *referenceModulesFixture) {
			writeReferenceModuleTestFile(t, f.modFile, []byte("module changed.invalid/module\n"))
		}},
		{"source bytes and forged ziphash", func(t *testing.T, f *referenceModulesFixture) {
			writeReferenceModuleTestFile(t, filepath.Join(f.directory, "nested", "fixture.go"), []byte("package fixture\nconst Value = 2\n"))
			forged, err := dirhash.HashDir(f.directory, f.dependency.Path+"@"+f.dependency.Version, dirhash.Hash1)
			if err != nil {
				t.Fatal(err)
			}
			writeReferenceModuleTestFile(t, strings.TrimSuffix(f.modFile, ".mod")+".ziphash", []byte(forged))
			f.dependency.Sum = forged
		}},
		{"forged cache bytes with original claimed sum", func(t *testing.T, f *referenceModulesFixture) {
			writeReferenceModuleTestFile(t, filepath.Join(f.directory, "nested", "fixture.go"), []byte("package fixture\nconst Value = 2\n"))
		}},
		{"missing descriptor", func(_ *testing.T, f *referenceModulesFixture) { f.dependency.GoMod = "" }},
		{"descriptor path escape", func(_ *testing.T, f *referenceModulesFixture) { f.dependency.GoMod = filepath.Join(f.source, "go.mod") }},
		{"directory path escape", func(_ *testing.T, f *referenceModulesFixture) { f.dependency.Dir = f.source }},
		{"module path escape", func(_ *testing.T, f *referenceModulesFixture) { f.dependency.Path = "../escape" }},
		{"module version escape", func(_ *testing.T, f *referenceModulesFixture) { f.dependency.Version = "../../escape" }},
		{"dependency replacement", func(_ *testing.T, f *referenceModulesFixture) {
			f.dependency.Replace = json.RawMessage(`{"Path":"replacement.invalid/module"}`)
		}},
		{"main replacement", func(_ *testing.T, f *referenceModulesFixture) {
			f.main.Replace = json.RawMessage(`{"Path":"replacement.invalid/module"}`)
		}},
		{"dependency error", func(_ *testing.T, f *referenceModulesFixture) {
			f.dependency.Error = json.RawMessage(`{"Err":"unavailable"}`)
		}},
		{"wrong main", func(_ *testing.T, f *referenceModulesFixture) { f.main.Path = "wrong.invalid/main" }},
		{"main flag absent", func(_ *testing.T, f *referenceModulesFixture) { f.main.Main = false }},
		{"second main", func(_ *testing.T, f *referenceModulesFixture) { f.dependency.Main = true }},
		{"wrong main directory", func(_ *testing.T, f *referenceModulesFixture) { f.main.Dir = f.cache }},
		{"wrong reported mod checksum", func(_ *testing.T, f *referenceModulesFixture) { f.dependency.GoModSum = f.sum }},
		{"missing root directory checksum", func(t *testing.T, f *referenceModulesFixture) {
			writeReferenceModuleTestFile(t, filepath.Join(f.source, "go.sum"), []byte(
				f.dependency.Path+" "+f.dependency.Version+"/go.mod "+f.modSum+"\n",
			))
		}},
		{"missing root descriptor checksum", func(t *testing.T, f *referenceModulesFixture) {
			writeReferenceModuleTestFile(t, filepath.Join(f.source, "go.sum"), []byte(
				f.dependency.Path+" "+f.dependency.Version+" "+f.sum+"\n",
			))
		}},
		{"symlinked file", func(t *testing.T, f *referenceModulesFixture) {
			if err := os.Symlink(f.modFile, filepath.Join(f.directory, "linked")); err != nil {
				t.Fatal(err)
			}
		}},
		{"symlinked descriptor ancestor", func(t *testing.T, f *referenceModulesFixture) {
			parent := filepath.Dir(f.modFile)
			moved := filepath.Join(t.TempDir(), "moved")
			if err := os.Rename(parent, moved); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(moved, parent); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newReferenceModulesFixture(t)
			test.mutate(t, &fixture)
			if _, err := verifyExecutionModuleGraph(t.Context(), fixture.source, fixture.cache, fixture.raw(t)); err == nil {
				t.Fatal("non-exact module cache or graph was accepted")
			}
		})
	}
}

func TestVerifyExecutionModuleGraphRejectsBoundsAndCancellation(t *testing.T) {
	fixture := newReferenceModulesFixture(t)
	for _, raw := range [][]byte{nil, bytes.Repeat([]byte{' '}, maxReferenceModuleGraphBytes+1), []byte(`{"Unknown":true}`)} {
		if _, err := verifyExecutionModuleGraph(t.Context(), fixture.source, fixture.cache, raw); err == nil {
			t.Fatal("empty, oversized, or malformed module graph was accepted")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := verifyExecutionModuleGraph(ctx, fixture.source, fixture.cache, fixture.raw(t)); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled module graph = %v", err)
	}
	writeReferenceModuleTestFile(t, filepath.Join(fixture.source, "go.sum"), bytes.Repeat([]byte{' '}, maxReferenceGoSumBytes+1))
	if _, err := verifyExecutionModuleGraph(t.Context(), fixture.source, fixture.cache, fixture.raw(t)); err == nil {
		t.Fatal("oversized source go.sum was accepted")
	}
}

func TestReferenceModuleBudgetAndReaderRefuseOverflowAndMutation(t *testing.T) {
	fixture := newReferenceModulesFixture(t)
	info, err := os.Lstat(fixture.modFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, budget := range []referenceModuleBudget{
		{entries: maxCheckoutEntries}, {bytes: maxCheckoutBytes - info.Size() + 1},
	} {
		if err := budget.reserve(info); err == nil {
			t.Fatal("exhausted module entry or byte budget was accepted")
		}
	}
	file, err := os.OpenFile(filepath.Join(fixture.source, "oversized"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxCheckoutFileBytes + 1); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	large, statErr := file.Stat()
	closeErr := file.Close()
	if statErr != nil || closeErr != nil {
		t.Fatal(errors.Join(statErr, closeErr))
	}
	if err := (&referenceModuleBudget{}).reserve(large); err == nil {
		t.Fatal("oversized module file was accepted")
	}
	root, err := os.OpenRoot(fixture.directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	before, err := root.Lstat("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	reader, err := openReferenceModuleFile(t.Context(), root, "go.mod", before)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(reader); err != nil {
		_ = reader.Close()
		t.Fatal(err)
	}
	writeReferenceModuleTestFile(t, filepath.Join(fixture.directory, "go.mod"), []byte("module changed.invalid/module\n"))
	if err := reader.Close(); err == nil {
		t.Fatal("module file changed after reading was accepted")
	}
}

func TestReferenceModuleGraphModuleCountBound(t *testing.T) {
	fixture := newReferenceModulesFixture(t)
	var raw, sums bytes.Buffer
	encoder := json.NewEncoder(&raw)
	if err := encoder.Encode(fixture.main); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < maxReferenceModules; index++ {
		path := fmt.Sprintf("example.invalid/dep%d", index)
		version := "v1.0.0"
		modPath := filepath.Join(fixture.cache, "cache", "download", filepath.FromSlash(path), "@v", version+".mod")
		modRaw := []byte("module " + path + "\n")
		writeReferenceModuleTestFile(t, modPath, modRaw)
		sum, err := dirhash.Hash1([]string{"go.mod"}, func(string) (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(modRaw)), nil
		})
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&sums, "%s %s/go.mod %s\n", path, version, sum)
		if err := encoder.Encode(referenceGraphModule{Path: path, Version: version, GoMod: modPath}); err != nil {
			t.Fatal(err)
		}
	}
	writeReferenceModuleTestFile(t, filepath.Join(fixture.source, "go.sum"), sums.Bytes())
	if _, err := verifyExecutionModuleGraph(t.Context(), fixture.source, fixture.cache, raw.Bytes()); err == nil {
		t.Fatal("graph exceeding the module count cap was accepted")
	}
}
