package main

import (
	"bufio"
	"bytes"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"io"
	"io/fs"
	"os"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

const diagnosticsSchema = "t111-typed-call-oracle-diagnostics-v3"

var (
	fullCommitRE    = regexp.MustCompile(`^[0-9a-f]{40}$`)
	buildTagRE      = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)
	generatedHeader = regexp.MustCompile(`(?m)^// Code generated .* DO NOT EDIT\.$`)
)

type options struct {
	root   string
	commit string
	tags   []string
	output string
}

type record struct {
	Path       string `json:"path"`
	Line       int    `json:"line"`
	Column     int    `json:"column"`
	ByteOffset int    `json:"byte_offset"`
	Method     string `json:"method"`
	Stratum    string `json:"stratum"`
}

// diagnostics deliberately contains only coverage counts and a digest binding
// the package loader's semantic inputs. It contains no labels, client
// identities, operation identities, or source coordinates.
type diagnostics struct {
	Schema                     string `json:"schema"`
	RuntimeVersion             string `json:"runtime_version"`
	GOOS                       string `json:"goos"`
	GOARCH                     string `json:"goarch"`
	SemanticInputsDigest       string `json:"semantic_inputs_digest"`
	Modules                    int    `json:"modules"`
	LoadedPackages             int    `json:"loaded_packages"`
	RootSourceFiles            int    `json:"root_source_files"`
	ExternalSourceFiles        int    `json:"external_source_files"`
	GeneratedClientInterfaces  int    `json:"generated_client_interfaces"`
	StructuralClientInterfaces int    `json:"structural_client_interfaces"`
	SelectorCallSites          int    `json:"selector_call_sites"`
	MatchedSites               int    `json:"matched_sites"`
	AmbiguousSites             int    `json:"ambiguous_sites"`
	DeduplicatedMatches        int    `json:"deduplicated_matches"`
}

type checkout struct {
	root    string
	tracked map[string]treeEntry
}

type treeEntry struct {
	mode string
	kind string
	oid  string
}

type clientInterface struct {
	identity  string
	named     *types.Named
	iface     *types.Interface
	methods   map[string]struct{}
	generated bool
}

type clientMatch struct {
	identity string
	stratum  string
}

const (
	stratumGeneratedClient  = "generated_client_interface"
	stratumConcreteClient   = "concrete_implements_generated_client"
	stratumEmbeddedClient   = "interface_embeds_generated_client"
	stratumStructuralClient = "structural_grpc_client_interface"
	stratumAmbiguousClient  = "ambiguous_multiple_generated_clients"
)

type scanState struct {
	checkout   checkout
	commit     string
	records    map[string]record
	ambig      map[string]struct{}
	selectors  map[string]struct{}
	outcomes   map[string]string
	rootFiles  map[string]struct{}
	extFiles   map[string]struct{}
	packages   map[string]struct{}
	clients    map[string]struct{}
	structural map[string]struct{}
	semantic   map[string]string
	rawMatches int
}

func splitTags(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			// Preserve an empty item so validation reports malformed input.
			result = append(result, part)
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		result = append(result, part)
	}
	sort.Strings(result)
	return result
}

func execute(opts options, stdout, stderr io.Writer) error {
	co, err := verifyCheckout(opts)
	if err != nil {
		return err
	}
	modules, err := findModules(co)
	if err != nil {
		return err
	}
	if len(modules) == 0 {
		return errors.New("no non-vendor go.mod modules found")
	}

	state := scanState{
		checkout:   co,
		commit:     opts.commit,
		records:    make(map[string]record),
		ambig:      make(map[string]struct{}),
		selectors:  make(map[string]struct{}),
		outcomes:   make(map[string]string),
		rootFiles:  make(map[string]struct{}),
		extFiles:   make(map[string]struct{}),
		packages:   make(map[string]struct{}),
		clients:    make(map[string]struct{}),
		structural: make(map[string]struct{}),
		semantic:   make(map[string]string),
	}
	for _, module := range modules {
		if err := state.scanModule(module, opts.tags); err != nil {
			return fmt.Errorf("module %s: %w", displayModule(co.root, module), err)
		}
	}
	semanticInputs, err := combineSemanticInputDigests(state.semantic)
	if err != nil {
		return fmt.Errorf("combine Go semantic inputs: %w", err)
	}
	if _, err := verifyCheckout(opts); err != nil {
		return fmt.Errorf("reverify checkout after oracle scan: %w", err)
	}

	records := make([]record, 0, len(state.records))
	for _, item := range state.records {
		records = append(records, item)
	}
	sort.Slice(records, func(i, j int) bool {
		a, b := records[i], records[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		if a.ByteOffset != b.ByteOffset {
			return a.ByteOffset < b.ByteOffset
		}
		if a.Method != b.Method {
			return a.Method < b.Method
		}
		return a.Stratum < b.Stratum
	})

	if opts.output == "" {
		if err := writeJSONL(stdout, records); err != nil {
			return fmt.Errorf("write stdout: %w", err)
		}
	} else if err := atomicWriteJSONL(opts.output, records); err != nil {
		return err
	}

	diag := diagnostics{
		Schema:                     diagnosticsSchema,
		RuntimeVersion:             runtime.Version(),
		GOOS:                       runtime.GOOS,
		GOARCH:                     runtime.GOARCH,
		SemanticInputsDigest:       semanticInputs,
		Modules:                    len(modules),
		LoadedPackages:             len(state.packages),
		RootSourceFiles:            len(state.rootFiles),
		ExternalSourceFiles:        len(state.extFiles),
		GeneratedClientInterfaces:  len(state.clients),
		StructuralClientInterfaces: len(state.structural),
		SelectorCallSites:          len(state.selectors),
		MatchedSites:               len(records),
		AmbiguousSites:             len(state.ambig),
		DeduplicatedMatches:        state.rawMatches - len(records),
	}
	b, err := json.Marshal(diag)
	if err != nil {
		return fmt.Errorf("encode diagnostics: %w", err)
	}
	if _, err := fmt.Fprintf(stderr, "%s\n", b); err != nil {
		return fmt.Errorf("write diagnostics: %w", err)
	}
	return nil
}

func verifyCheckout(opts options) (checkout, error) {
	if opts.root == "" {
		return checkout{}, errors.New("-root is required")
	}
	if !fullCommitRE.MatchString(opts.commit) {
		return checkout{}, errors.New("-commit must be a full 40-character lowercase SHA-1")
	}
	for _, tag := range opts.tags {
		if !buildTagRE.MatchString(tag) {
			return checkout{}, fmt.Errorf("invalid Go build tag %q", tag)
		}
	}

	root, err := filepath.Abs(opts.root)
	if err != nil {
		return checkout{}, fmt.Errorf("resolve root: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return checkout{}, fmt.Errorf("resolve root symlinks: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return checkout{}, fmt.Errorf("stat root: %w", err)
	}
	if !info.IsDir() {
		return checkout{}, errors.New("-root is not a directory")
	}

	top, err := gitOutput(root, "rev-parse", "--show-toplevel")
	if err != nil {
		return checkout{}, err
	}
	top = strings.TrimSpace(top)
	top, err = filepath.EvalSymlinks(top)
	if err != nil {
		return checkout{}, fmt.Errorf("resolve Git top-level: %w", err)
	}
	if top != root {
		return checkout{}, fmt.Errorf("-root must be the Git top-level (got %s)", top)
	}

	head, err := gitOutput(root, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return checkout{}, err
	}
	if strings.TrimSpace(head) != opts.commit {
		return checkout{}, fmt.Errorf("HEAD %s does not equal requested commit %s", strings.TrimSpace(head), opts.commit)
	}
	status, err := gitBytes(root, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return checkout{}, err
	}
	if len(status) != 0 {
		return checkout{}, errors.New("checkout is dirty (tracked, staged, or untracked files present)")
	}

	tree, err := readTree(root, opts.commit)
	if err != nil {
		return checkout{}, err
	}
	if err := verifyWorktreeTree(root, tree); err != nil {
		return checkout{}, err
	}
	return checkout{root: root, tracked: tree}, nil
}

func gitOutput(root string, args ...string) (string, error) {
	b, err := gitBytes(root, args...)
	return string(b), err
}

func gitBytes(root string, args ...string) ([]byte, error) {
	full := append([]string{"-C", root}, args...)
	cmd := exec.Command("git", full...)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
	)
	b, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(b)))
	}
	return b, nil
}

func readTree(root, commit string) (map[string]treeEntry, error) {
	b, err := gitBytes(root, "ls-tree", "-r", "--full-tree", "-z", commit)
	if err != nil {
		return nil, err
	}
	result := make(map[string]treeEntry)
	for _, raw := range bytes.Split(b, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		header, path, ok := bytes.Cut(raw, []byte{'\t'})
		if !ok {
			return nil, errors.New("malformed git ls-tree record")
		}
		fields := bytes.Fields(header)
		if len(fields) != 3 {
			return nil, errors.New("malformed git ls-tree header")
		}
		rel := filepath.ToSlash(string(path))
		if !safeRelative(rel) {
			return nil, fmt.Errorf("unsafe path in Git tree: %q", rel)
		}
		result[rel] = treeEntry{
			mode: string(fields[0]), kind: string(fields[1]), oid: string(fields[2]),
		}
	}
	return result, nil
}

// verifyWorktreeTree closes the assume-unchanged/skip-worktree hole in
// porcelain cleanliness checks. go/packages necessarily reads the worktree,
// so every tracked blob it could observe must first (and, via execute's final
// verifyCheckout, afterwards) hash to the exact requested Git tree object.
func verifyWorktreeTree(root string, tracked map[string]treeEntry) error {
	paths := make([]string, 0, len(tracked))
	for rel := range tracked {
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	for _, rel := range paths {
		entry := tracked[rel]
		if entry.kind == "commit" && entry.mode == "160000" {
			continue
		}
		if entry.kind != "blob" {
			return fmt.Errorf("unsupported Git tree object %s %s at %s", entry.mode, entry.kind, rel)
		}
		if !semanticWorktreeInput(rel) {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(rel))
		var content []byte
		var err error
		if entry.mode == "120000" {
			var target string
			target, err = os.Readlink(path)
			content = []byte(target)
		} else {
			var info fs.FileInfo
			info, err = os.Lstat(path)
			if err == nil && !info.Mode().IsRegular() {
				return fmt.Errorf("tracked worktree path is not a regular file: %s", rel)
			}
			if err == nil {
				content, err = os.ReadFile(path)
			}
		}
		if err != nil {
			return fmt.Errorf("read tracked worktree blob %s: %w", rel, err)
		}
		if got := gitBlobOID(content); got != entry.oid {
			return fmt.Errorf("worktree content does not match pinned Git blob at %s: got %s, want %s", rel, got, entry.oid)
		}
		if entry.mode == "120000" {
			if _, _, err := resolveTrackedSource(root, tracked, rel); err != nil {
				return fmt.Errorf("verify tracked semantic-input alias %s: %w", rel, err)
			}
		}
	}
	return nil
}

func semanticWorktreeInput(rel string) bool {
	base := filepath.Base(filepath.FromSlash(rel))
	return strings.HasSuffix(rel, ".go") || base == "go.mod" || base == "go.sum" ||
		(filepath.Base(filepath.Dir(filepath.FromSlash(rel))) == "vendor" && base == "modules.txt") ||
		strings.HasSuffix(rel, "/vendor/modules.txt")
}

func gitBlobOID(content []byte) string {
	h := sha1.New()
	_, _ = fmt.Fprintf(h, "blob %d%c", len(content), byte(0))
	_, _ = h.Write(content)
	return fmt.Sprintf("%x", h.Sum(nil))
}

// resolveTrackedSource accepts a Go source symlink only when the already
// verified worktree link chain stays inside the pinned tree and terminates at
// a regular commit-backed blob. The caller keeps the alias path as the source
// occurrence; the resolved path is provenance, not a replacement coordinate.
func resolveTrackedSource(root string, tracked map[string]treeEntry, rel string) (string, treeEntry, error) {
	current := filepath.ToSlash(rel)
	seen := make(map[string]struct{})
	traversedLink := false
	for {
		if _, duplicate := seen[current]; duplicate {
			return "", treeEntry{}, fmt.Errorf("tracked source symlink cycle at %s", rel)
		}
		seen[current] = struct{}{}
		entry, ok := tracked[current]
		if !ok {
			return "", treeEntry{}, fmt.Errorf("tracked source symlink %s resolves to absent path %s", rel, current)
		}
		if entry.kind != "blob" {
			return "", treeEntry{}, fmt.Errorf("tracked source %s resolves to unsupported %s %s at %s", rel, entry.mode, entry.kind, current)
		}
		switch entry.mode {
		case "100644", "100755":
			if traversedLink {
				path := filepath.Join(root, filepath.FromSlash(current))
				info, err := os.Lstat(path)
				if err != nil {
					return "", treeEntry{}, fmt.Errorf("stat tracked source target %s: %w", current, err)
				}
				if !info.Mode().IsRegular() {
					return "", treeEntry{}, fmt.Errorf("tracked source target is not a regular file: %s", current)
				}
				content, err := os.ReadFile(path)
				if err != nil {
					return "", treeEntry{}, fmt.Errorf("read tracked source target %s: %w", current, err)
				}
				if got := gitBlobOID(content); got != entry.oid {
					return "", treeEntry{}, fmt.Errorf("tracked source target %s differs from pinned blob: got %s, want %s", current, got, entry.oid)
				}
			}
			return current, entry, nil
		case "120000":
			target, err := os.Readlink(filepath.Join(root, filepath.FromSlash(current)))
			if err != nil {
				return "", treeEntry{}, fmt.Errorf("read tracked source symlink %s: %w", current, err)
			}
			target = filepath.ToSlash(target)
			if got := gitBlobOID([]byte(target)); got != entry.oid {
				return "", treeEntry{}, fmt.Errorf("tracked source symlink %s differs from pinned blob: got %s, want %s", current, got, entry.oid)
			}
			if target == "" || pathpkg.IsAbs(target) {
				return "", treeEntry{}, fmt.Errorf("tracked source symlink %s has empty or absolute target %q", current, target)
			}
			next := pathpkg.Clean(pathpkg.Join(pathpkg.Dir(current), target))
			if !safeRelative(next) {
				return "", treeEntry{}, fmt.Errorf("tracked source symlink %s escapes the pinned tree via %q", current, target)
			}
			current = next
			traversedLink = true
		default:
			return "", treeEntry{}, fmt.Errorf("tracked source %s resolves to unsupported blob mode %s at %s", rel, entry.mode, current)
		}
	}
}

func findModules(co checkout) ([]string, error) {
	var modules []string
	err := filepath.WalkDir(co.root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == co.root {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			rel, ok := relativeWithin(co.root, path)
			if !ok {
				return fmt.Errorf("directory escapes root: %s", path)
			}
			if entry, exists := co.tracked[rel]; exists && entry.mode == "160000" && entry.kind == "commit" {
				return filepath.SkipDir
			}
			switch d.Name() {
			case ".git", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "go.mod" {
			return nil
		}
		rel, ok := relativeWithin(co.root, path)
		if !ok {
			return fmt.Errorf("module path escapes root: %s", path)
		}
		entry, ok := co.tracked[rel]
		if !ok || entry.kind != "blob" || entry.mode == "120000" {
			return fmt.Errorf("module file is not a regular commit-backed blob: %s", rel)
		}
		modules = append(modules, filepath.Dir(path))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("find modules: %w", err)
	}
	sort.Strings(modules)
	return modules, nil
}

func (s *scanState) scanModule(module string, tags []string) error {
	if err := validateLocalReplaces(s.checkout.root, module); err != nil {
		return err
	}
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedImports | packages.NeedDeps | packages.NeedTypes |
			packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedModule |
			packages.NeedForTest,
		Dir:        module,
		Tests:      true,
		Env:        packageEnv(module),
		BuildFlags: buildFlags(tags),
	}
	roots, err := packages.Load(cfg, "./...")
	if err != nil {
		return fmt.Errorf("packages.Load: %w", err)
	}
	if len(roots) == 0 {
		semanticInputs, err := s.emptyModuleSemanticInputs(module)
		if err != nil {
			return err
		}
		moduleKey := displayModule(s.checkout.root, module)
		if previous, ok := s.semantic[moduleKey]; ok && previous != semanticInputs {
			return fmt.Errorf("module %s produced inconsistent semantic input digests", moduleKey)
		}
		s.semantic[moduleKey] = semanticInputs
		return nil
	}
	graph := packageClosure(roots)
	if err := validatePackages(graph); err != nil {
		return err
	}
	semanticInputs, err := verifyPackageSemanticInputs(s.checkout.root, s.commit, graph)
	if err != nil {
		return fmt.Errorf("verify Go semantic inputs: %w", err)
	}
	for _, p := range graph {
		s.packages[displayModule(s.checkout.root, module)+"\x00"+p.ID] = struct{}{}
	}

	clients, err := s.indexClients(graph)
	if err != nil {
		return err
	}
	if err := s.scanCalls(graph, clients); err != nil {
		return err
	}
	verifiedAgain, err := verifyPackageSemanticInputs(s.checkout.root, s.commit, graph)
	if err != nil {
		return fmt.Errorf("reverify Go semantic inputs: %w", err)
	}
	if verifiedAgain != semanticInputs {
		return errors.New("Go semantic inputs changed during oracle scan")
	}
	moduleKey := displayModule(s.checkout.root, module)
	if previous, ok := s.semantic[moduleKey]; ok && previous != semanticInputs {
		return fmt.Errorf("module %s produced inconsistent semantic input digests", moduleKey)
	}
	s.semantic[moduleKey] = semanticInputs
	return nil
}

// emptyModuleSemanticInputs admits a tracked go.mod-only module while keeping
// the package census fail closed. A direct .go blob that go/packages did not
// load is an unexplained coverage gap and must not be silently classified as
// an empty module. Sources below a nested tracked module are accounted for by
// that module's own scan.
func (s *scanState) emptyModuleSemanticInputs(module string) (string, error) {
	moduleKey := displayModule(s.checkout.root, module)
	prefix := ""
	if moduleKey != "." {
		prefix = moduleKey + "/"
	}
	nested := make([]string, 0)
	for path, entry := range s.checkout.tracked {
		if entry.kind != "blob" || entry.mode == "120000" || path == prefix+"go.mod" {
			continue
		}
		if strings.HasPrefix(path, prefix) && strings.HasSuffix(path, "/go.mod") {
			nested = append(nested, strings.TrimSuffix(path, "go.mod"))
		}
	}
	sort.Strings(nested)
	for path, entry := range s.checkout.tracked {
		if entry.kind != "blob" || !strings.HasSuffix(path, ".go") || !strings.HasPrefix(path, prefix) {
			continue
		}
		if _, _, err := resolveTrackedSource(s.checkout.root, s.checkout.tracked, path); err != nil {
			return "", err
		}
		insideNested := false
		for _, nestedPrefix := range nested {
			if strings.HasPrefix(path, nestedPrefix) {
				insideNested = true
				break
			}
		}
		if !insideNested {
			return "", fmt.Errorf("packages.Load returned no packages but tracked Go source exists: %s", path)
		}
	}
	return digestSemanticDescriptors(map[string]struct{}{
		strings.Join([]string{"snapshot-empty-module", s.commit, moduleKey}, "\x00"): {},
	}), nil
}

func packageEnv(module string) []string {
	moduleMode := "readonly"
	if info, err := os.Lstat(filepath.Join(module, "vendor", "modules.txt")); err == nil && info.Mode().IsRegular() {
		moduleMode = "vendor"
	}
	env := map[string]string{
		"CGO_ENABLED":         "0",
		"GIT_CONFIG_NOSYSTEM": "1",
		"GIT_OPTIONAL_LOCKS":  "0",
		"GIT_TERMINAL_PROMPT": "0",
		"GO111MODULE":         "on",
		"GOENV":               "off",
		"GOFLAGS":             "-mod=" + moduleMode + " -buildvcs=false",
		"GOPROXY":             "off",
		"GOSUMDB":             "off",
		"GOTOOLCHAIN":         "local",
		"GOWORK":              "off",
	}
	for _, key := range []string{"PATH", "HOME", "GOCACHE", "GOMODCACHE", "GOPATH", "GOTMPDIR", "TMPDIR"} {
		if value := os.Getenv(key); value != "" {
			env[key] = value
		}
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+env[key])
	}
	return result
}

func buildFlags(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	return []string{"-tags=" + strings.Join(tags, ",")}
}

func packageClosure(roots []*packages.Package) []*packages.Package {
	seen := make(map[string]*packages.Package)
	var visit func(*packages.Package)
	visit = func(p *packages.Package) {
		if p == nil {
			return
		}
		key := p.ID
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = p
		for _, path := range sortedImportPaths(p.Imports) {
			visit(p.Imports[path])
		}
	}
	for _, p := range roots {
		visit(p)
	}
	result := make([]*packages.Package, 0, len(seen))
	for _, p := range seen {
		result = append(result, p)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func sortedImportPaths(imports map[string]*packages.Package) []string {
	paths := make([]string, 0, len(imports))
	for path := range imports {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func validatePackages(graph []*packages.Package) error {
	for _, p := range graph {
		if len(p.Errors) != 0 {
			errs := append([]packages.Error(nil), p.Errors...)
			sort.Slice(errs, func(i, j int) bool { return errs[i].Error() < errs[j].Error() })
			return fmt.Errorf("package %s load/type error: %s", p.ID, errs[0].Error())
		}
		if p.IllTyped {
			return fmt.Errorf("package %s is ill-typed", p.ID)
		}
		if p.Types == nil || p.TypesInfo == nil || p.Fset == nil {
			return fmt.Errorf("package %s has incomplete type information", p.ID)
		}
		if len(p.CompiledGoFiles) != len(p.Syntax) {
			return fmt.Errorf("package %s source/type gap: %d compiled files, %d syntax files", p.ID, len(p.CompiledGoFiles), len(p.Syntax))
		}
	}
	return nil
}

func (s *scanState) indexClients(graph []*packages.Package) ([]clientInterface, error) {
	var result []clientInterface
	for _, p := range graph {
		for i, file := range p.Syntax {
			filename := p.CompiledGoFiles[i]
			content, err := os.ReadFile(filename)
			if err != nil {
				return nil, fmt.Errorf("read package source %s: %w", filename, err)
			}
			generatedFile := generatedHeader.Match(headerPrefix(content))
			for _, decl := range file.Decls {
				gen, ok := decl.(*ast.GenDecl)
				if !ok || gen.Tok != token.TYPE {
					continue
				}
				for _, spec := range gen.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok || !strings.HasSuffix(ts.Name.Name, "Client") {
						continue
					}
					if _, ok := ts.Type.(*ast.InterfaceType); !ok {
						continue
					}
					obj, ok := p.TypesInfo.Defs[ts.Name].(*types.TypeName)
					if !ok || obj == nil {
						return nil, fmt.Errorf("generated interface %s in package %s has no type object", ts.Name.Name, p.ID)
					}
					named, ok := obj.Type().(*types.Named)
					if !ok {
						return nil, fmt.Errorf("generated type %s in package %s is not named", ts.Name.Name, p.ID)
					}
					iface, ok := named.Underlying().(*types.Interface)
					if !ok {
						return nil, fmt.Errorf("generated type %s in package %s is not an interface", ts.Name.Name, p.ID)
					}
					iface.Complete()
					if !grpcClientInterfaceShape(iface) {
						continue
					}
					methods := make(map[string]struct{}, iface.NumMethods())
					for j := 0; j < iface.NumMethods(); j++ {
						methods[iface.Method(j).Name()] = struct{}{}
					}
					identity := packagePath(obj) + "." + obj.Name()
					result = append(result, clientInterface{
						identity: identity, named: named, iface: iface,
						methods: methods, generated: generatedFile,
					})
					if generatedFile {
						s.clients[identity] = struct{}{}
					} else {
						s.structural[identity] = struct{}{}
					}
				}
			}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].identity < result[j].identity })
	return result, nil
}

func packagePath(obj *types.TypeName) string {
	if obj.Pkg() == nil {
		return "<builtin>"
	}
	return obj.Pkg().Path()
}

// grpcClientInterfaceShape independently recognizes the stable unary and
// streaming protoc-gen-go-grpc method envelope: context.Context first and
// variadic grpc.CallOption last. Requiring every method to have this shape
// rejects unrelated generated APIs that merely use a *Client suffix.
func grpcClientInterfaceShape(iface *types.Interface) bool {
	if iface == nil || iface.NumMethods() == 0 {
		return false
	}
	for i := 0; i < iface.NumMethods(); i++ {
		sig, ok := iface.Method(i).Type().(*types.Signature)
		if !ok || sig.Params().Len() < 2 || !sig.Variadic() {
			return false
		}
		if !namedType(sig.Params().At(0).Type(), "context", "Context") {
			return false
		}
		last, ok := sig.Params().At(sig.Params().Len() - 1).Type().(*types.Slice)
		if !ok || !namedType(last.Elem(), "google.golang.org/grpc", "CallOption") {
			return false
		}
	}
	return true
}

func namedType(value types.Type, packagePath, name string) bool {
	named, ok := value.(*types.Named)
	if !ok || named.Obj() == nil || named.Obj().Pkg() == nil {
		return false
	}
	return named.Obj().Pkg().Path() == packagePath && named.Obj().Name() == name
}

func (s *scanState) scanCalls(graph []*packages.Package, clients []clientInterface) error {
	clientsByMethod := make(map[string][]clientInterface)
	for _, client := range clients {
		for method := range client.methods {
			clientsByMethod[method] = append(clientsByMethod[method], client)
		}
	}
	for method := range clientsByMethod {
		sort.Slice(clientsByMethod[method], func(i, j int) bool {
			return clientsByMethod[method][i].identity < clientsByMethod[method][j].identity
		})
	}
	for _, p := range graph {
		for i, file := range p.Syntax {
			filename := p.CompiledGoFiles[i]
			rel, inside := lexicalRelativeWithin(s.checkout.root, filename)
			if !inside {
				s.extFiles[filepath.Clean(filename)] = struct{}{}
				continue
			}
			if _, _, err := resolveTrackedSource(s.checkout.root, s.checkout.tracked, rel); err != nil {
				return fmt.Errorf("root source is not a safe commit-backed blob alias: %s: %w", rel, err)
			}
			if _, err := os.ReadFile(filename); err != nil {
				return fmt.Errorf("read root source %s: %w", rel, err)
			}
			s.rootFiles[rel] = struct{}{}
			var scanErr error
			ast.Inspect(file, func(node ast.Node) bool {
				if scanErr != nil {
					return false
				}
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pos := p.Fset.PositionFor(sel.Sel.Pos(), false)
				if pos.Filename == "" || pos.Offset < 0 {
					return true
				}
				siteKey := fmt.Sprintf("%s\x00%d\x00%s", rel, pos.Offset, sel.Sel.Name)
				s.selectors[siteKey] = struct{}{}
				selection, ok := p.TypesInfo.Selections[sel]
				if !ok || selection.Kind() != types.MethodVal {
					scanErr = s.recordCallOutcome(siteKey, "not-method-value")
					return true
				}
				matches := matchingClients(
					selection.Recv(), sel.Sel.Name, clientsByMethod[sel.Sel.Name],
				)
				if len(matches) == 0 {
					scanErr = s.recordCallOutcome(siteKey, "unmatched")
					return true
				}
				stratum := matches[0].stratum
				identities := make([]string, 0, len(matches))
				for _, match := range matches {
					identities = append(identities, match.identity)
				}
				if len(matches) > 1 {
					// This is still a valid recall candidate: the receiver satisfies
					// more than one independently discovered generated-client
					// interface. Preserve it for blind adjudication instead of
					// silently biasing the recall frame.
					s.ambig[siteKey] = struct{}{}
					stratum = stratumAmbiguousClient
				}
				outcome := "matched\x00" + stratum + "\x00" + strings.Join(identities, "\x00")
				if err := s.recordCallOutcome(siteKey, outcome); err != nil {
					scanErr = err
					return false
				}
				rec := record{
					Path:       rel,
					Line:       pos.Line,
					Column:     pos.Column,
					ByteOffset: pos.Offset,
					Method:     sel.Sel.Name,
					Stratum:    stratum,
				}
				s.rawMatches++
				if prior, exists := s.records[siteKey]; exists && prior != rec {
					scanErr = fmt.Errorf("package contexts disagree on matched source site %s", siteKey)
					return false
				}
				s.records[siteKey] = rec
				return true
			})
			if scanErr != nil {
				return scanErr
			}
		}
	}
	return nil
}

func (s *scanState) recordCallOutcome(siteKey, outcome string) error {
	if prior, exists := s.outcomes[siteKey]; exists && prior != outcome {
		return fmt.Errorf("package contexts disagree on source call site %s", siteKey)
	}
	s.outcomes[siteKey] = outcome
	return nil
}

func matchingClients(recv types.Type, method string, clients []clientInterface) []clientMatch {
	matched := make(map[string]clientMatch)
	exact := make(map[string]clientMatch)
	for _, client := range clients {
		if _, ok := client.methods[method]; !ok {
			continue
		}
		if receiverImplements(recv, client.iface) {
			stratum := stratumStructuralClient
			if client.generated {
				stratum = stratumConcreteClient
				if types.Identical(recv, client.named) {
					stratum = stratumGeneratedClient
				} else if _, ok := recv.Underlying().(*types.Interface); ok {
					stratum = stratumEmbeddedClient
				}
			}
			match := clientMatch{
				identity: client.identity,
				stratum:  stratum,
			}
			matched[client.identity] = match
			if types.Identical(recv, client.named) {
				exact[client.identity] = match
			}
		}
	}
	// A statically named generated-client interface is authoritative. It can
	// also structurally implement another same-shaped generated interface;
	// treating that coincidence as ambiguous drops known direct-client calls.
	if len(exact) != 0 {
		matched = exact
	}
	result := make([]clientMatch, 0, len(matched))
	for _, match := range matched {
		result = append(result, match)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].identity < result[j].identity })
	return result
}

func receiverImplements(recv types.Type, iface *types.Interface) bool {
	if recv == nil {
		return false
	}
	if types.Implements(recv, iface) {
		return true
	}
	// A selector on an addressable concrete value may resolve a pointer-receiver
	// method even though Selection.Recv reports the value type.
	if named, ok := recv.(*types.Named); ok {
		if _, isInterface := named.Underlying().(*types.Interface); !isInterface {
			return types.Implements(types.NewPointer(named), iface)
		}
	}
	return false
}

func headerPrefix(content []byte) []byte {
	if len(content) > 4096 {
		return content[:4096]
	}
	return content
}

func relativeWithin(root, path string) (string, bool) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	return rel, safeRelative(rel)
}

// lexicalRelativeWithin preserves an in-tree symlink's logical tracked path.
// Safety comes from resolveTrackedSource, which verifies the already hash-bound
// link chain before any source coordinate is admitted.
func lexicalRelativeWithin(root, candidate string) (string, bool) {
	abs, err := filepath.Abs(candidate)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	return rel, safeRelative(rel)
}

func safeRelative(path string) bool {
	if path == "" || filepath.IsAbs(filepath.FromSlash(path)) || strings.ContainsRune(path, '\x00') {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	return clean == path && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}

func displayModule(root, module string) string {
	rel, ok := relativeWithin(root, module)
	if !ok || rel == "" {
		return "."
	}
	return rel
}

func writeJSONL(w io.Writer, records []record) error {
	bw := bufio.NewWriter(w)
	enc := json.NewEncoder(bw)
	enc.SetEscapeHTML(false)
	for _, item := range records {
		if err := enc.Encode(item); err != nil {
			return err
		}
	}
	return bw.Flush()
}

func atomicWriteJSONL(path string, records []record) (err error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve output: %w", err)
	}
	dir := filepath.Dir(abs)
	if info, statErr := os.Stat(dir); statErr != nil || !info.IsDir() {
		if statErr != nil {
			return fmt.Errorf("output directory: %w", statErr)
		}
		return errors.New("output parent is not a directory")
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(abs)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create output temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()
	if err := tmp.Chmod(0o644); err != nil {
		return fmt.Errorf("chmod output temp file: %w", err)
	}
	if err := writeJSONL(tmp, records); err != nil {
		return fmt.Errorf("write output temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync output temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close output temp file: %w", err)
	}
	if err := os.Rename(tmpName, abs); err != nil {
		return fmt.Errorf("publish output: %w", err)
	}
	dirHandle, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open output directory: %w", err)
	}
	defer dirHandle.Close()
	if err := dirHandle.Sync(); err != nil {
		return fmt.Errorf("sync output directory: %w", err)
	}
	return nil
}
