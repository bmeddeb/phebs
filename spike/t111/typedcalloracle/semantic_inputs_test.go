package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/mod/sumdb/dirhash"
	"golang.org/x/tools/go/packages"
)

func TestSemanticInputsVerifyExternalModuleAndDetectTampering(t *testing.T) {
	root := t.TempDir()
	moduleCache := filepath.Join(t.TempDir(), "module-cache")
	external := filepath.Join(moduleCache, "gomodcache", "example.test", "dep@v1.2.3")
	if err := os.MkdirAll(external, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(external, "dep.go")
	if err := os.WriteFile(source, []byte("package dep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	goModContent := []byte("module example.test/dep\n\ngo 1.26\n")
	if err := os.WriteFile(filepath.Join(external, "go.mod"), goModContent, 0o644); err != nil {
		t.Fatal(err)
	}
	wantSum, err := dirhash.HashDir(external, "example.test/dep@v1.2.3", dirhash.Hash1)
	if err != nil {
		t.Fatal(err)
	}
	wantModSum, err := dirhash.Hash1([]string{"go.mod"}, func(string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(goModContent)), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	cachedGoMod := filepath.Join(moduleCache, "gomodcache", "cache", "download", "example.test", "dep", "@v", "v1.2.3.mod")
	if err := os.MkdirAll(filepath.Dir(cachedGoMod), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachedGoMod, goModContent, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.sum"), []byte(
		"example.test/dep v1.2.3 "+wantSum+"\nexample.test/dep v1.2.3/go.mod "+wantModSum+"\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	sealOracleTestCache(t, moduleCache)
	pkgs := []*packages.Package{{Module: &packages.Module{
		Path: "example.test/dep", Version: "v1.2.3", Dir: external, GoMod: cachedGoMod,
	}}}
	first, err := verifyPackageSemanticInputs(root, strings.Repeat("a", 40), moduleCache, pkgs)
	if err != nil {
		t.Fatalf("verified external module rejected: %v", err)
	}
	second, err := verifyPackageSemanticInputs(root, strings.Repeat("a", 40), moduleCache, pkgs)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || !validSemanticDigest(first) {
		t.Fatalf("semantic digest is not deterministic SHA-256: first=%q second=%q", first, second)
	}
	if err := os.Chmod(cachedGoMod, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachedGoMod, append(append([]byte(nil), goModContent...), []byte("// tampered\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cachedGoMod, 0o444); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyPackageSemanticInputs(root, strings.Repeat("a", 40), moduleCache, pkgs); err == nil || !strings.Contains(err.Error(), "go.mod content mismatch") {
		t.Fatalf("tampered cached go.mod was not rejected: %v", err)
	}
	if err := os.Chmod(cachedGoMod, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachedGoMod, goModContent, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cachedGoMod, 0o444); err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(source, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("package dep\n// tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(source, 0o444); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyPackageSemanticInputs(root, strings.Repeat("a", 40), moduleCache, pkgs); err == nil || !strings.Contains(err.Error(), "content mismatch") {
		t.Fatalf("tampered external module was not rejected: %v", err)
	}
}

func TestSemanticInputsBindVendoredModule(t *testing.T) {
	root := t.TempDir()
	vendorDir := filepath.Join(root, "vendor", "example.test", "dep", "sub")
	if err := os.MkdirAll(vendorDir, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(vendorDir, "dep.go")
	if err := os.WriteFile(source, []byte("package sub\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pkg := &packages.Package{
		GoFiles:         []string{source},
		CompiledGoFiles: []string{source},
		Module:          &packages.Module{Path: "example.test/dep", Version: "v1.2.3"},
	}
	digest, err := verifyPackageSemanticInputs(root, strings.Repeat("b", 40), "", []*packages.Package{pkg})
	if err != nil {
		t.Fatalf("vendored module rejected: %v", err)
	}
	if !validSemanticDigest(digest) {
		t.Fatalf("vendored module digest = %q", digest)
	}
}

func sealOracleTestCache(t *testing.T, root string) {
	t.Helper()
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		mode := os.FileMode(0o444)
		if entry.IsDir() {
			mode = 0o555
		}
		return os.Chmod(path, mode)
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err == nil {
				if entry.IsDir() {
					_ = os.Chmod(path, 0o755)
				} else {
					_ = os.Chmod(path, 0o644)
				}
			}
			return nil
		})
	})
}

func TestValidateLocalReplacesRejectsCheckoutEscape(t *testing.T) {
	root := t.TempDir()
	module := filepath.Join(root, "app")
	shared := filepath.Join(root, "shared")
	outside := t.TempDir()
	for _, dir := range []string{module, shared} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	goMod := filepath.Join(module, "go.mod")
	if err := os.WriteFile(goMod, []byte("module example.test/app\n\ngo 1.26\n\nreplace example.test/shared => ../shared\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateLocalReplaces(root, module); err != nil {
		t.Fatalf("in-checkout local replace rejected: %v", err)
	}
	escape, err := filepath.Rel(module, outside)
	if err != nil {
		t.Fatal(err)
	}
	content := "module example.test/app\n\ngo 1.26\n\nreplace example.test/shared => " + filepath.ToSlash(escape) + "\n"
	if err := os.WriteFile(goMod, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateLocalReplaces(root, module); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("escaping local replace was not rejected: %v", err)
	}
}

func TestPackageEnvIsHermeticAndModuleAware(t *testing.T) {
	module := t.TempDir()
	t.Setenv("PATH", "/test/tool/path")
	t.Setenv("HOME", "/test/home")
	t.Setenv("GOCACHE", "/test/go-build")
	t.Setenv("GOMODCACHE", "/test/go-mod")
	t.Setenv("GOENV", "/host/go/env")
	t.Setenv("GOFLAGS", "-mod=mod")
	t.Setenv("ORACLE_UNRELATED", "must-not-leak")

	cache := filepath.Join(t.TempDir(), "dedicated")
	runRoot := filepath.Join(t.TempDir(), "run")
	readonly := semanticTestEnvMap(packageEnv(module, cache, runRoot))
	want := map[string]string{
		"CGO_ENABLED": "0",
		"GO111MODULE": "on",
		"GOENV":       "off",
		"GOFLAGS":     "-mod=readonly -buildvcs=false",
		"GOPROXY":     "off",
		"GOSUMDB":     "off",
		"GOTOOLCHAIN": "local",
		"GOWORK":      "off",
		"PATH":        "/test/tool/path",
		"HOME":        filepath.Join(runRoot, "home"),
		"GOCACHE":     filepath.Join(runRoot, "build-cache"),
		"GOMODCACHE":  filepath.Join(cache, "gomodcache"),
		"GOPATH":      filepath.Join(runRoot, "gopath"),
		"GOTMPDIR":    filepath.Join(runRoot, "tmp"),
		"TMPDIR":      filepath.Join(runRoot, "tmp"),
	}
	for key, value := range want {
		if got := readonly[key]; got != value {
			t.Errorf("%s = %q, want %q", key, got, value)
		}
	}
	if _, ok := readonly["ORACLE_UNRELATED"]; ok {
		t.Fatal("unrelated ambient environment leaked into package loader")
	}

	vendor := filepath.Join(module, "vendor")
	if err := os.Mkdir(vendor, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vendor, "modules.txt"), []byte("# pinned\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := semanticTestEnvMap(packageEnv(module, cache, runRoot))["GOFLAGS"]; got != "-mod=vendor -buildvcs=false" {
		t.Fatalf("vendor GOFLAGS = %q", got)
	}
}

func TestExecuteReportsSemanticInputsDigest(t *testing.T) {
	root, commit := makeFixture(t, map[string]string{
		"go.mod": "module example.test/oracle\n\ngo 1.23\n",
		"app.go": "package oracle\n",
	})
	var stdout, stderr bytes.Buffer
	includeTests := false
	if err := execute(options{
		root: root, commit: commit, goos: "linux", goarch: "arm64", includeTests: &includeTests,
	}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	var diag diagnostics
	if err := json.Unmarshal(bytes.TrimSpace(stderr.Bytes()), &diag); err != nil {
		t.Fatalf("decode diagnostics: %v: %s", err, stderr.Bytes())
	}
	if !validSemanticDigest(diag.SemanticInputsDigest) {
		t.Fatalf("diagnostics semantic input digest = %q", diag.SemanticInputsDigest)
	}
	if diag.AnalysisGOOS != "linux" || diag.AnalysisGOARCH != "arm64" || diag.IncludeTests {
		t.Fatalf("diagnostics did not bind the requested analysis policy: %+v", diag)
	}
}

func TestAnalysisContextRejectsPartialOrUnknownTarget(t *testing.T) {
	if _, err := analysisContextForOptions(options{goos: "linux"}); err == nil {
		t.Fatal("partial Go analysis target accepted")
	}
	if _, err := analysisContextForOptions(options{goos: "unknown", goarch: "arm64"}); err == nil {
		t.Fatal("unknown Go analysis target accepted")
	}
}

func semanticTestEnvMap(env []string) map[string]string {
	result := make(map[string]string, len(env))
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			result[key] = value
		}
	}
	return result
}

func validSemanticDigest(digest string) bool {
	if !strings.HasPrefix(digest, "sha256:") {
		return false
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(digest, "sha256:"))
	return err == nil && len(decoded) == 32
}
