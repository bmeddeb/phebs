package main

import (
	"archive/zip"
	"bytes"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/mod/sumdb/dirhash"
)

func TestHydrateThenOfflineExtractionUsesOnlyDedicatedCache(t *testing.T) {
	t.Setenv("HOME", "/poison/home")
	t.Setenv("GOPATH", "/poison/gopath")
	t.Setenv("GOMODCACHE", "/poison/modcache")
	proxy, goSum := localModuleProxy(t)
	repo := initTestRepository(t)
	goMod := []byte("module example.test/app\n\ngo 1.26\n\nrequire (\n\texample.test/dep v1.0.0\n\texample.test/unused v1.0.0\n)\n")
	source := []byte("package app\n\nimport _ \"example.test/dep\"\n")
	for name, content := range map[string][]byte{
		"go.mod": goMod, "go.sum": []byte(goSum), "app.go": source,
	} {
		if err := os.WriteFile(filepath.Join(repo, name), content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitTestOutput(t, repo, "add", "go.mod", "go.sum", "app.go")
	gitTestOutput(t, repo, "commit", "-q", "-m", "module fixture")
	commit := gitTestOutput(t, repo, "rev-parse", "HEAD")
	entry := CorpusEntry{
		Name: "fixture", GitURL: "https://example.invalid/fixture", Commit: commit,
		GoOS: runtime.GOOS, GoArch: runtime.GOARCH, GoTests: goTestsInclude,
	}

	empty := filepath.Join(t.TempDir(), "empty-cache")
	if _, err := prepareModuleCache(empty); err != nil {
		t.Fatal(err)
	}
	oldCache := harnessModuleCacheRoot
	harnessModuleCacheRoot = empty
	t.Cleanup(func() { harnessModuleCacheRoot = oldCache })
	if facts, err := extractModule(entry.Name, commit, repo, repo, nil); err == nil && !hasLoadDiagnostic(facts) {
		t.Fatal("empty dedicated cache did not fail closed while offline")
	}

	cache := filepath.Join(t.TempDir(), "hydrated-cache")
	t.Cleanup(func() { makeCacheWritable(cache) })
	beforeMod, _ := os.ReadFile(filepath.Join(repo, "go.mod"))
	beforeSum, _ := os.ReadFile(filepath.Join(repo, "go.sum"))
	beforeSource, _ := os.ReadFile(filepath.Join(repo, "app.go"))
	downloaded, vendored, err := hydrateCorpusModules(entry, repo, cache, proxy, "off")
	if err != nil {
		t.Fatalf("hydrate from local proxy: %v", err)
	}
	if downloaded != 1 || vendored != 0 {
		t.Fatalf("hydration counts = downloaded %d vendored %d", downloaded, vendored)
	}
	if _, err := os.Lstat(filepath.Join(cache, "gomodcache", "example.test", "unused@v1.0.0")); !os.IsNotExist(err) {
		t.Fatalf("unused required module was unexpectedly hydrated: %v", err)
	}
	if err := inspectSharedModuleCache(cache, true); err != nil {
		t.Fatalf("hydration did not seal the shared cache: %v", err)
	}
	for name, want := range map[string][]byte{"go.mod": beforeMod, "go.sum": beforeSum, "app.go": beforeSource} {
		got, err := os.ReadFile(filepath.Join(repo, name))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("hydration changed %s", name)
		}
	}
	if err := verifyCorpus(repo, commit); err != nil {
		t.Fatalf("hydration changed pinned corpus: %v", err)
	}

	harnessModuleCacheRoot = cache
	facts, err := extractModule(entry.Name, commit, repo, repo, nil)
	if err != nil {
		t.Fatalf("offline extraction after hydration: %v", err)
	}
	if hasLoadDiagnostic(facts) {
		t.Fatalf("offline extraction emitted a load diagnostic: %+v", facts)
	}
	env := envMap(goPackageEnv(repo))
	if env["GOMODCACHE"] != filepath.Join(cache, "gomodcache") ||
		env["GOPROXY"] != "off" || env["GOSUMDB"] != "off" || env["GOWORK"] != "off" {
		t.Fatalf("offline environment escaped the dedicated cache: %#v", env)
	}
	for _, key := range []string{"HOME", "GOPATH", "GOCACHE", "GOTMPDIR", "GOTELEMETRYDIR"} {
		if env[key] == "" || strings.HasPrefix(env[key], cache+string(filepath.Separator)) || strings.HasPrefix(env[key], "/poison/") {
			t.Fatalf("%s is not isolated per-run state: %q", key, env[key])
		}
	}
	if env["GOTELEMETRY"] != "off" {
		t.Fatalf("Go telemetry is not forced off: %#v", env)
	}

	depSource := filepath.Join(cache, "gomodcache", "example.test", "dep@v1.0.0", "dep.go")
	if err := os.Chmod(depSource, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(depSource, []byte("package dep\n// tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := extractModule(entry.Name, commit, repo, repo, nil); err == nil ||
		(!strings.Contains(err.Error(), "content mismatch") && !strings.Contains(err.Error(), "not sealed read-only")) {
		t.Fatalf("tampered dedicated cache was not rejected: %v", err)
	}
	if err := os.WriteFile(depSource, []byte("package dep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := sealSharedModuleCache(cache); err != nil {
		t.Fatal(err)
	}
	cachedGoMod := filepath.Join(cache, "gomodcache", "cache", "download", "example.test", "dep", "@v", "v1.0.0.mod")
	if err := os.Chmod(cachedGoMod, 0o644); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(cachedGoMod)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachedGoMod, append(content, []byte("// tampered\n")...), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cachedGoMod, 0o444); err != nil {
		t.Fatal(err)
	}
	if _, err := extractModule(entry.Name, commit, repo, repo, nil); err == nil ||
		(!strings.Contains(err.Error(), "go.mod content mismatch") && !strings.Contains(err.Error(), "checksum mismatch")) {
		t.Fatalf("tampered cached go.mod was not directly h1-rejected: %v", err)
	}
}

func TestExplicitGoAnalysisPolicyControlsTargetAndTestPopulation(t *testing.T) {
	repo := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":      "module example.test/policy\n\ngo 1.26\n",
		"app.go":      "package policy\n",
		"app_test.go": "package policy\n\nvar _ MissingTestOnlySymbol\n",
	} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cache := filepath.Join(t.TempDir(), "cache")
	if _, err := prepareModuleCache(cache); err != nil {
		t.Fatal(err)
	}
	if err := sealSharedModuleCache(cache); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { makeCacheWritable(cache) })
	oldCache := harnessModuleCacheRoot
	harnessModuleCacheRoot = cache
	t.Cleanup(func() { harnessModuleCacheRoot = oldCache })

	withTests := runtimeBuildContext(nil)
	facts, err := extractModuleWithContext("fixture", strings.Repeat("a", 40), repo, repo, withTests)
	if err != nil {
		t.Fatalf("test-inclusive extraction returned structural error: %v", err)
	}
	if !hasLoadDiagnostic(facts) {
		t.Fatal("test-only type failure was not preserved as a diagnostic")
	}

	productionOnly := withTests
	productionOnly.GOOS = "linux"
	productionOnly.GOARCH = "arm64"
	productionOnly.IncludeTests = false
	facts, err = extractModuleWithContext("fixture", strings.Repeat("a", 40), repo, repo, productionOnly)
	if err != nil {
		t.Fatalf("explicit production-only target failed: %v", err)
	}
	if hasLoadDiagnostic(facts) {
		t.Fatalf("excluded test variant leaked into production-only analysis: %+v", facts)
	}
	env := envMap(goPackageEnvForContext(repo, productionOnly))
	if env["GOOS"] != "linux" || env["GOARCH"] != "arm64" || env["CGO_ENABLED"] != "0" {
		t.Fatalf("analysis target did not reach package environment: %#v", env)
	}
}

func TestHydrationNetworkPolicyRejectsFallbackAndAlternateTrust(t *testing.T) {
	validFile := (&url.URL{Scheme: "file", Path: filepath.ToSlash(t.TempDir())}).String()
	for _, tc := range []struct {
		proxy string
		sumdb string
	}{
		{"https://proxy.golang.org,direct", "sum.golang.org"},
		{"https://proxy.golang.org|direct", "sum.golang.org"},
		{"https://user@proxy.golang.org", "sum.golang.org"},
		{"https://proxy.golang.org?fallback=direct", "sum.golang.org"},
		{"https://proxy.golang.org#fragment", "sum.golang.org"},
		{"file://localhost/private/tmp/proxy", "off"},
		{"file:relative", "off"},
		{"https://proxy.golang.org", "sum.golang.org https://evil.invalid"},
		{"https://proxy.golang.org", "evil.invalid"},
	} {
		if err := validateHydrationNetwork(tc.proxy, tc.sumdb); err == nil {
			t.Fatalf("accepted hydration network policy proxy=%q sumdb=%q", tc.proxy, tc.sumdb)
		}
	}
	for _, tc := range []struct {
		proxy string
		sumdb string
	}{{"https://proxy.golang.org", "sum.golang.org"}, {validFile, "off"}} {
		if err := validateHydrationNetwork(tc.proxy, tc.sumdb); err != nil {
			t.Fatalf("rejected canonical hydration policy proxy=%q sumdb=%q: %v", tc.proxy, tc.sumdb, err)
		}
	}
}

func TestSharedModuleCacheRejectsNestedSymlinkAndReseals(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache")
	paths, err := prepareModuleCache(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := makeSharedModuleCacheWritable(root); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(paths.gomodcache, "cache", "download")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "record"), []byte("pinned\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := sealSharedModuleCache(root); err != nil {
		t.Fatal(err)
	}
	if err := inspectSharedModuleCache(root, true); err != nil {
		t.Fatalf("sealed cache rejected: %v", err)
	}
	if info, err := os.Stat(filepath.Join(nested, "record")); err != nil || info.Mode().Perm()&0o222 != 0 {
		t.Fatalf("cache file was not resealed: info=%v err=%v", info, err)
	}
	makeCacheWritable(root)
	if err := os.Symlink(t.TempDir(), filepath.Join(nested, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := inspectSharedModuleCache(root, false); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("nested cache symlink was accepted: %v", err)
	}
}

func TestBoundBinaryTreeRejectsIntermediateSymlinkAndWritableArtifact(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache")
	t.Cleanup(func() { makeCacheWritable(root) })
	paths, err := prepareModuleCache(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := sealSharedModuleCache(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), paths.bin); err != nil {
		t.Fatal(err)
	}
	if err := inspectBoundBinaryTree(root); err == nil || !strings.Contains(err.Error(), "real directory") {
		t.Fatalf("intermediate bound-binary symlink was accepted: %v", err)
	}
	if err := os.Remove(paths.bin); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(paths.bin, 0o755); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(paths.bin, "t111")
	if err := os.WriteFile(artifact, []byte("bound\n"), 0o555); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(paths.bin, 0o555); err != nil {
		t.Fatal(err)
	}
	if err := inspectBoundBinaryTree(root); err != nil {
		t.Fatalf("sealed bound-binary tree rejected: %v", err)
	}
	if err := os.Chmod(artifact, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := inspectBoundBinaryTree(root); err == nil || !strings.Contains(err.Error(), "writable") {
		t.Fatalf("writable bound artifact was accepted: %v", err)
	}
}

func TestHydrateSkipsVendoredModulesWithoutNetwork(t *testing.T) {
	repo := initTestRepository(t)
	for name, content := range map[string]string{
		"go.mod":             "module example.test/vendorapp\n\ngo 1.26\n",
		"app.go":             "package vendorapp\n",
		"vendor/modules.txt": "# immutable vendor fixture\n",
	} {
		path := filepath.Join(repo, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitTestOutput(t, repo, "add", "go.mod", "app.go", "vendor/modules.txt")
	gitTestOutput(t, repo, "commit", "-q", "-m", "vendor fixture")
	commit := gitTestOutput(t, repo, "rev-parse", "HEAD")
	entry := CorpusEntry{
		Name: "vendor", GitURL: "https://example.invalid/vendor", Commit: commit,
		GoOS: runtime.GOOS, GoArch: runtime.GOARCH, GoTests: goTestsInclude,
	}
	downloaded, vendored, err := hydrateCorpusModules(
		entry, repo, filepath.Join(t.TempDir(), "cache"), "https://127.0.0.1:1", "off",
	)
	if err != nil {
		t.Fatalf("vendor-only hydration attempted network or failed: %v", err)
	}
	if downloaded != 0 || vendored != 1 {
		t.Fatalf("vendor hydration counts = downloaded %d vendored %d", downloaded, vendored)
	}
}

func hasLoadDiagnostic(facts []Fact) bool {
	for _, fact := range facts {
		if fact.Predicate == "LOAD_ERRORS" || fact.Predicate == "EXTRACTION_FAILURE" {
			return true
		}
	}
	return false
}

func makeCacheWritable(root string) {
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			_ = os.Chmod(path, 0o755)
		} else {
			_ = os.Chmod(path, 0o644)
		}
		return nil
	})
}

func localModuleProxy(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	versionDir := filepath.Join(root, "example.test", "dep", "@v")
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mod := []byte("module example.test/dep\n\ngo 1.26\n")
	if err := os.WriteFile(filepath.Join(versionDir, "v1.0.0.mod"), mod, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(versionDir, "v1.0.0.info"), []byte("{\"Version\":\"v1.0.0\",\"Time\":\"2020-01-01T00:00:00Z\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(versionDir, "list"), []byte("v1.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	zipPath := filepath.Join(versionDir, "v1.0.0.zip")
	file, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(file)
	for name, content := range map[string][]byte{
		"example.test/dep@v1.0.0/go.mod": mod,
		"example.test/dep@v1.0.0/dep.go": []byte("package dep\n"),
	} {
		writer, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	moduleSum, err := dirhash.HashZip(zipPath, dirhash.Hash1)
	if err != nil {
		t.Fatal(err)
	}
	modSum, err := dirhash.Hash1([]string{"go.mod"}, func(string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(mod)), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	proxy := (&url.URL{Scheme: "file", Path: filepath.ToSlash(root)}).String()
	return proxy, "example.test/dep v1.0.0 " + moduleSum + "\n" +
		"example.test/dep v1.0.0/go.mod " + modSum + "\n"
}
