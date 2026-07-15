package main

import (
	"bytes"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/mod/sumdb/dirhash"
	"golang.org/x/tools/go/packages"
)

func TestAssertionIDIsIndependentOfEvidenceOccurrence(t *testing.T) {
	a := newFact("CALLS_OPERATION", "internal/cart", "/shop.Cart/Get", roleProduction, tierExact,
		"repo", "commit-a", "a.go", 1, 8, 1, 1, "call-v1", "sha256:one", "iface=shop.CartClient")
	b := newFact("CALLS_OPERATION", "internal/cart", "/shop.Cart/Get", roleProduction, tierExact,
		"repo", "commit-b", "b.go", 20, 40, 3, 3, "call-v2", "sha256:two", "iface=shop.CartClient")
	if a.AtomID == b.AtomID {
		t.Fatal("different evidence occurrences shared an atom ID")
	}
	if a.AssertionID != b.AssertionID {
		t.Fatalf("same semantic assertion depended on evidence occurrence: %s != %s", a.AssertionID, b.AssertionID)
	}

	r1 := newFact("SERVICE_REACHABLE_FROM_BINARY", "cmd/server", "shop.Cart", roleProduction, tierDerived,
		"repo", "commit", "a.go", 1, 8, 1, 1, "reach-v1", "sha256:one", "support=ea_one;src-pkg=a")
	r2 := newFact("SERVICE_REACHABLE_FROM_BINARY", "cmd/server", "shop.Cart", roleProduction, tierDerived,
		"repo", "commit", "b.go", 3, 9, 1, 1, "reach-v1", "sha256:two", "support=ea_two;src-pkg=b")
	if r1.AssertionID != r2.AssertionID {
		t.Fatal("reachability assertion ID depended on supporting atom")
	}
	crossRepo := a
	crossRepo.System = "other-repo"
	crossRepo.AssertionID = assertionID(crossRepo)
	if crossRepo.AssertionID == a.AssertionID {
		t.Fatal("repository-local assertions collided across repositories")
	}
}

func TestValidateFactsChecksDiagnosticsAndLines(t *testing.T) {
	content := []byte("first\ncall()\n")
	entry := CorpusEntry{Name: "repo", Commit: "commit"}
	fact := newFact("CALLS_OPERATION", "pkg", "/shop.Cart/Get", roleProduction, tierExact,
		entry.Name, entry.Commit, "call.go", 6, 12, 2, 2, "call-v1", blobDigest(content), "iface=shop.CartClient")
	fact = bindSemanticInputs([]Fact{fact}, "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")[0]
	readBlob := func(string) ([]byte, error) { return content, nil }
	if err := validateFacts(entry, []Fact{fact}, readBlob); err != nil {
		t.Fatalf("valid fact rejected: %v", err)
	}
	badLine := fact
	badLine.EndLine = 1
	badLine.AssertionID = assertionID(badLine)
	if err := validateFacts(entry, []Fact{badLine}, readBlob); err == nil || !strings.Contains(err.Error(), "span-derived lines") {
		t.Fatalf("bad lines were not rejected: %v", err)
	}
	diagnostic := newFact("LOAD_ERRORS", "pkg", "one package error", "", tierUnresolved,
		entry.Name, entry.Commit, "go.mod", 0, 1, 1, 1, "go-load-v2", blobDigest(content), "stage=load")
	if err := validateFacts(entry, []Fact{diagnostic}, readBlob); err == nil || !strings.Contains(err.Error(), "extractor diagnostic") {
		t.Fatalf("diagnostic fact was not fail-closed: %v", err)
	}
}

func TestFailedAtomicPublishRestoresPriorArtifact(t *testing.T) {
	dir := realTempDir(t)
	path := filepath.Join(dir, "facts.jsonl")
	old := []byte("old artifact\n")
	if err := os.WriteFile(path, old, 0o644); err != nil {
		t.Fatal(err)
	}
	fact := newFact("CALLS_OPERATION", "pkg", "/shop.Cart/Get", roleProduction, tierExact,
		"repo", "commit", "call.go", 1, 2, 1, 1, "call-v1", "sha256:blob", "iface=shop.CartClient")
	injected := errors.New("injected post-rename failure")
	err := writeFactsAtomicWithHooks(path, []Fact{fact}, publishHooks{
		afterRename: func() error { return injected },
	})
	if !errors.Is(err, injected) {
		t.Fatalf("publish error = %v, want injected failure", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(old) {
		t.Fatalf("failed publish replaced prior artifact: %q", got)
	}
	matches, err := filepath.Glob(filepath.Join(dir, ".facts.jsonl.*-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("failed publish left transaction files: %v", matches)
	}
	if _, err := os.Lstat(path + ".lock"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed publish left lock: %v", err)
	}
}

func TestPublishRejectsSymlinkedOutputComponent(t *testing.T) {
	root := realTempDir(t)
	realDir := filepath.Join(root, "real")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(root, "link")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Fatal(err)
	}
	err := writeFactsAtomic(filepath.Join(linkDir, "facts.jsonl"), []Fact{{}}, nil)
	if err == nil {
		t.Fatalf("symlinked output path was not rejected: %v", err)
	}
}

func TestRecursiveTreeRejectsGitlink(t *testing.T) {
	repo := initTestRepository(t)
	commit := gitTestOutput(t, repo, "rev-parse", "HEAD")
	gitTestOutput(t, repo, "update-index", "--add", "--cacheinfo", "160000,"+commit+",nested")
	tree := gitTestOutput(t, repo, "write-tree")
	if _, err := recursiveTree(repo, tree, nil); err == nil || !strings.Contains(err.Error(), "gitlink") {
		t.Fatalf("gitlink was not rejected: %v", err)
	}
}

func TestPinnedFactReaderResolvesOnlySafeSourceAliases(t *testing.T) {
	repo := initTestRepository(t)
	target := filepath.Join(repo, "targets", "source.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("package fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(repo, "alias.go")
	if err := os.Symlink("targets/source.go", alias); err != nil {
		t.Fatal(err)
	}
	gitTestOutput(t, repo, "add", "-A")
	gitTestOutput(t, repo, "commit", "-q", "-m", "safe source alias")
	commit := gitTestOutput(t, repo, "rev-parse", "HEAD")
	entries, err := recursiveTree(repo, commit, nil)
	if err != nil {
		t.Fatal(err)
	}
	byPath := make(map[string]treeEntry, len(entries))
	for _, entry := range entries {
		byPath[entry.path] = entry
	}
	content, err := readPinnedSourceBlob(repo, "alias.go", byPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "package fixture\n" {
		t.Fatalf("alias content = %q", content)
	}

	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../outside.go", alias); err != nil {
		t.Fatal(err)
	}
	gitTestOutput(t, repo, "add", "alias.go")
	gitTestOutput(t, repo, "commit", "-q", "-m", "escaping source alias")
	commit = gitTestOutput(t, repo, "rev-parse", "HEAD")
	entries, err = recursiveTree(repo, commit, nil)
	if err != nil {
		t.Fatal(err)
	}
	byPath = make(map[string]treeEntry, len(entries))
	for _, entry := range entries {
		byPath[entry.path] = entry
	}
	if _, err := readPinnedSourceBlob(repo, "alias.go", byPath); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("escaping source alias was not rejected: %v", err)
	}
}

func TestRecursiveTreeAllowsOnlyExactReviewedGitlink(t *testing.T) {
	repo := initTestRepository(t)
	commit := gitTestOutput(t, repo, "rev-parse", "HEAD")
	gitTestOutput(t, repo, "update-index", "--add", "--cacheinfo", "160000,"+commit+",nested")
	tree := gitTestOutput(t, repo, "write-tree")
	exclusion := CorpusGitlinkExclusion{Path: "nested", ObjectID: commit, Reason: "reviewed non-code fixture"}
	entries, err := recursiveTree(repo, tree, []CorpusGitlinkExclusion{exclusion})
	if err != nil {
		t.Fatalf("exact reviewed gitlink rejected: %v", err)
	}
	for _, entry := range entries {
		if entry.path == exclusion.Path {
			t.Fatal("excluded gitlink was returned for materialization")
		}
	}
	wrong := exclusion
	wrong.ObjectID = strings.Repeat("0", 40)
	if _, err := recursiveTree(repo, tree, []CorpusGitlinkExclusion{wrong}); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("gitlink object mismatch was not rejected: %v", err)
	}
	missing := exclusion
	missing.Path = "elsewhere"
	if _, err := recursiveTree(repo, tree, []CorpusGitlinkExclusion{missing}); err == nil || !strings.Contains(err.Error(), "not explicitly excluded") {
		t.Fatalf("undeclared gitlink was not rejected before unused policy: %v", err)
	}
}

func TestCorpusPolicyValidation(t *testing.T) {
	valid := CorpusEntry{
		Name: "repo", GitURL: "https://example.test/repo", Commit: strings.Repeat("a", 40),
		ExcludedGitlinks: []CorpusGitlinkExclusion{{
			Path: "docs/theme", ObjectID: strings.Repeat("b", 40), Reason: "reviewed non-code tree",
		}},
		GoBuildTags: []string{"unit"},
	}
	if err := validateCorpusEntry(valid); err != nil {
		t.Fatalf("valid corpus policy rejected: %v", err)
	}
	bad := valid
	bad.GoBuildTags = []string{"unit,prod"}
	if err := validateCorpusEntry(bad); err == nil {
		t.Fatal("ambiguous Go build tags accepted")
	}
	bad = valid
	bad.ExcludedGitlinks[0].Reason = ""
	if err := validateCorpusEntry(bad); err == nil {
		t.Fatal("gitlink exclusion without rationale accepted")
	}
}

func TestResolveRegisteredServiceRequiresUniqueFullProtoName(t *testing.T) {
	dir := t.TempDir()
	one := filepath.Join(dir, "one_grpc.pb.go")
	if err := os.WriteFile(one, []byte(`package pb
var Cart_ServiceDesc = grpc.ServiceDesc{ServiceName: "shop.v1.Cart"}
var Other_ServiceDesc = grpc.ServiceDesc{ServiceName: "shop.v1.Other"}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := resolveRegisteredService([]string{one}, "Cart"); got != "shop.v1.Cart" {
		t.Fatalf("registered service = %q, want fully-qualified proto name", got)
	}
	two := filepath.Join(dir, "two_grpc.pb.go")
	if err := os.WriteFile(two, []byte(`package pb
var Cart2_ServiceDesc = grpc.ServiceDesc{ServiceName: "legacy.Cart"}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := resolveRegisteredService([]string{one, two}, "Cart"); got != "" {
		t.Fatalf("ambiguous registered service did not abstain: %q", got)
	}
}

func TestServiceNameLiteralRequiresStaticProtoIdentity(t *testing.T) {
	expression, err := parser.ParseExpr(`grpc.ServiceDesc{ServiceName: "shop.v1.Cart"}`)
	if err != nil {
		t.Fatal(err)
	}
	literal, ok := expression.(*ast.CompositeLit)
	if !ok {
		t.Fatalf("parsed expression = %T", expression)
	}
	if got := serviceNameLiteral(literal); got != "shop.v1.Cart" {
		t.Fatalf("service name = %q", got)
	}
	dynamic, err := parser.ParseExpr(`grpc.ServiceDesc{ServiceName: serviceName}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := serviceNameLiteral(dynamic.(*ast.CompositeLit)); got != "" {
		t.Fatalf("dynamic service name did not abstain: %q", got)
	}
	if validProtoServiceName("shop..Cart") || validProtoServiceName("9shop.Cart") {
		t.Fatal("invalid proto service identity accepted")
	}
}

func TestRegisterHelperForwarderIsNotAnImplementationSite(t *testing.T) {
	source := `package pb
func RegisterCartServer(s Registrar, srv CartServer) {
	s.RegisterService(&Cart_ServiceDesc, srv)
}

func TestPackageClosureAndFactOrderAreDeterministic(t *testing.T) {
	depA := &packages.Package{ID: "a", PkgPath: "example/a"}
	depZ := &packages.Package{ID: "z", PkgPath: "example/z"}
	root := &packages.Package{
		ID: "root", PkgPath: "example/root",
		Imports: map[string]*packages.Package{"example/z": depZ, "example/a": depA},
	}
	closure := packageClosure([]*packages.Package{root})
	gotIDs := make([]string, len(closure))
	for i, pkg := range closure {
		gotIDs[i] = pkg.ID
	}
	if got := strings.Join(gotIDs, ","); got != "a,root,z" {
		t.Fatalf("package closure order = %q", got)
	}
	facts := []Fact{
		{Path: "z.go", StartByte: 1, AtomID: "z"},
		{Path: "a.go", StartByte: 9, AtomID: "b"},
		{Path: "a.go", StartByte: 2, AtomID: "a"},
	}
	sortFacts(facts)
	if facts[0].AtomID != "a" || facts[1].AtomID != "b" || facts[2].AtomID != "z" {
		t.Fatalf("fact order = %+v", facts)
	}
}
func install(s Registrar, impl CartServer) {
	s.RegisterService(&Cart_ServiceDesc, impl)
}`
	file, err := parser.ParseFile(token.NewFileSet(), "cart.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	forwarders := registerHelperForwarders(file)
	var direct []token.Pos
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == "RegisterService" {
			direct = append(direct, call.Pos())
		}
		return true
	})
	if len(direct) != 2 || !forwarders[direct[0]] || forwarders[direct[1]] {
		t.Fatalf("forwarder classification = %v for calls %v", forwarders, direct)
	}
}

func TestLocalReplaceAndVendorMode(t *testing.T) {
	root := t.TempDir()
	modDir := filepath.Join(root, "app")
	shared := filepath.Join(root, "shared")
	if err := os.MkdirAll(filepath.Join(modDir, "vendor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modDir, "go.mod"), []byte("module example.test/app\n\ngo 1.26\n\nreplace example.test/shared => ../shared\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modDir, "vendor", "modules.txt"), []byte("# pinned\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateLocalReplaces(root, modDir); err != nil {
		t.Fatalf("in-snapshot replace rejected: %v", err)
	}
	env := envMap(goPackageEnv(modDir))
	if got := env["GOFLAGS"]; !strings.Contains(got, "-mod=vendor") {
		t.Fatalf("GOFLAGS = %q, want pinned vendor mode", got)
	}
	if err := os.WriteFile(filepath.Join(modDir, "go.mod"), []byte("module example.test/app\n\ngo 1.26\n\nreplace example.test/shared => ../../outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateLocalReplaces(root, modDir); err == nil {
		t.Fatal("escaping local replace was accepted")
	}
}

func TestSemanticInputsVerifyAndBindExternalModule(t *testing.T) {
	root := realTempDir(t)
	cacheRoot := filepath.Join(realTempDir(t), "module-cache")
	paths, err := prepareModuleCache(cacheRoot)
	if err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(paths.gomodcache, "example.test", "dep@v1.0.0")
	if err := os.MkdirAll(external, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(external, "dep.go"), []byte("package dep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	goModContent := []byte("module example.test/dep\n\ngo 1.26\n")
	if err := os.WriteFile(filepath.Join(external, "go.mod"), goModContent, 0o644); err != nil {
		t.Fatal(err)
	}
	sum, err := dirhash.HashDir(external, "example.test/dep@v1.0.0", dirhash.Hash1)
	if err != nil {
		t.Fatal(err)
	}
	modSum, err := dirhash.Hash1([]string{"go.mod"}, func(string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(goModContent)), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	cachedGoMod := filepath.Join(paths.gomodcache, "cache", "download", "example.test", "dep", "@v", "v1.0.0.mod")
	if err := os.MkdirAll(filepath.Dir(cachedGoMod), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachedGoMod, goModContent, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.sum"), []byte(
		"example.test/dep v1.0.0 "+sum+"\nexample.test/dep v1.0.0/go.mod "+modSum+"\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := sealSharedModuleCache(cacheRoot); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { makeCacheWritable(cacheRoot) })
	oldCache := harnessModuleCacheRoot
	harnessModuleCacheRoot = cacheRoot
	t.Cleanup(func() { harnessModuleCacheRoot = oldCache })
	pkgs := []*packages.Package{{Module: &packages.Module{
		Path: "example.test/dep", Version: "v1.0.0", Dir: external, GoMod: cachedGoMod,
	}}}
	digest, err := verifyPackageSemanticInputs(root, "0123456789abcdef", pkgs)
	if err != nil {
		t.Fatalf("verified module rejected: %v", err)
	}
	fact := newFact("CALLS_OPERATION", "pkg", "/dep.Service/Call", roleProduction, tierExact,
		"repo", "commit", "call.go", 0, 1, 1, 1, "call-v1", "sha256:blob", "iface=dep.Client")
	bound := bindSemanticInputs([]Fact{fact}, digest)[0]
	if bound.SemanticInputsDigest != digest || bound.AtomID == fact.AtomID {
		t.Fatal("semantic input digest was not bound into the evidence atom")
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
	if _, err := verifyPackageSemanticInputs(root, "0123456789abcdef", pkgs); err == nil || !strings.Contains(err.Error(), "go.mod content mismatch") {
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
	depSource := filepath.Join(external, "dep.go")
	if err := os.Chmod(depSource, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(depSource, []byte("package dep\n// tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(depSource, 0o444); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyPackageSemanticInputs(root, "0123456789abcdef", pkgs); err == nil || !strings.Contains(err.Error(), "content mismatch") {
		t.Fatalf("tampered module was not rejected: %v", err)
	}
}

func TestSemanticInputsBindVendoredModuleInsideSnapshot(t *testing.T) {
	root := realTempDir(t)
	vendorDir := filepath.Join(root, "vendor", "example.test", "dep", "sub")
	if err := os.MkdirAll(vendorDir, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(vendorDir, "dep.go")
	if err := os.WriteFile(source, []byte("package sub\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pkg := &packages.Package{
		PkgPath:         "example.test/dep/sub",
		GoFiles:         []string{source},
		CompiledGoFiles: []string{source},
		Module:          &packages.Module{Path: "example.test/dep", Version: "v1.2.3"},
	}
	digest, err := verifyPackageSemanticInputs(root, "0123456789abcdef", []*packages.Package{pkg})
	if err != nil {
		t.Fatalf("pinned vendored module rejected: %v", err)
	}
	if !strings.HasPrefix(digest, "sha256:") {
		t.Fatalf("vendored module digest = %q", digest)
	}
	ordinary := filepath.Join(root, "ordinary")
	if err := os.MkdirAll(ordinary, 0o755); err != nil {
		t.Fatal(err)
	}
	ordinarySource := filepath.Join(ordinary, "dep.go")
	if err := os.WriteFile(ordinarySource, []byte("package dep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pkg.GoFiles = []string{ordinarySource}
	pkg.CompiledGoFiles = nil
	if _, err := verifyPackageSemanticInputs(root, "0123456789abcdef", []*packages.Package{pkg}); err == nil || !strings.Contains(err.Error(), "not below vendor/") {
		t.Fatalf("unrooted module with empty Dir was accepted: %v", err)
	}
}

func TestTemplateValuesAlwaysAbstain(t *testing.T) {
	got, tier := resolveTemplateValue("{{ .Values.image.repository }}", map[string]any{
		"image": map[string]any{"repository": "registry.example/image"},
	})
	if got != "{{ .Values.image.repository }}" || tier != tierUnresolved {
		t.Fatalf("template resolution = %q/%s, want raw/unresolved", got, tier)
	}
}

func TestTemplateLiteralManifestAbstainsUnderControlFlow(t *testing.T) {
	content := []byte(`{{ if .Values.enabled }}
apiVersion: apps/v1
kind: Deployment
metadata:
  name: literal-name
spec:
  template:
    spec:
      containers:
        - image: registry.example/literal:1
{{ end }}
`)
	facts := scanTemplateManifest("repo", "commit", t.TempDir(), "templates/app.yaml", content)
	if len(facts) == 0 {
		t.Fatal("template fixture produced no diagnostic facts")
	}
	for _, fact := range facts {
		if fact.Tier != tierUnresolved {
			t.Fatalf("template control-flow fact %s was %s, want unresolved", fact.Predicate, fact.Tier)
		}
	}
}

func initTestRepository(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitTestOutput(t, repo, "init", "-q")
	gitTestOutput(t, repo, "config", "user.email", "test@example.test")
	gitTestOutput(t, repo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("tracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTestOutput(t, repo, "add", "tracked.txt")
	gitTestOutput(t, repo, "commit", "-q", "-m", "initial")
	return repo
}

func gitTestOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(gitExecutable, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func envMap(env []string) map[string]string {
	result := map[string]string{}
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			result[key] = value
		}
	}
	return result
}

func realTempDir(t *testing.T) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return real
}
