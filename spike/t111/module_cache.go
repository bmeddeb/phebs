package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"golang.org/x/mod/sumdb/dirhash"
)

const (
	defaultModuleCacheRoot = "spike/t111/.module-cache"
	boundManifestSchema    = "t111-bound-harness-v1"
)

type moduleCachePaths struct {
	root       string
	gomodcache string
	bin        string
	manifest   string
}

func moduleCacheAt(root string) moduleCachePaths {
	root, _ = filepath.Abs(root)
	return moduleCachePaths{
		root:       root,
		gomodcache: filepath.Join(root, "gomodcache"),
		bin:        filepath.Join(root, "bin"),
		manifest:   filepath.Join(root, "manifest.json"),
	}
}

func dedicatedModuleCache() moduleCachePaths {
	root := harnessModuleCacheRoot
	if root == "" {
		// Unit tests call extraction helpers directly, without the CLI's bound
		// tool/bootstrap configuration. Give those processes an empty, sealed
		// cache instead of ever falling back to the user's ambient module cache.
		root = filepath.Join(os.TempDir(), fmt.Sprintf("t111-empty-module-cache-%d", os.Getpid()))
		paths := moduleCacheAt(root)
		if _, err := os.Lstat(paths.gomodcache); os.IsNotExist(err) {
			if prepared, prepareErr := prepareModuleCache(root); prepareErr == nil {
				_ = sealSharedModuleCache(prepared.root)
			}
		}
	}
	return moduleCacheAt(root)
}

// prepareModuleCache creates only the persistent shared module download cache.
// HOME, GOPATH, GOCACHE, temporary files, and telemetry are process-scoped
// state under the operating-system temporary directory, never shared here.
func prepareModuleCache(root string) (moduleCachePaths, error) {
	paths := moduleCacheAt(root)
	if info, err := os.Lstat(paths.root); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return moduleCachePaths{}, fmt.Errorf("dedicated module cache root is a symlink: %s", paths.root)
	} else if err != nil && !os.IsNotExist(err) {
		return moduleCachePaths{}, fmt.Errorf("inspect dedicated module cache %s: %w", paths.root, err)
	}
	if err := os.MkdirAll(paths.root, 0o755); err != nil {
		return moduleCachePaths{}, fmt.Errorf("create dedicated module cache %s: %w", paths.root, err)
	}
	realRoot, err := filepath.EvalSymlinks(paths.root)
	if err != nil {
		return moduleCachePaths{}, fmt.Errorf("resolve dedicated module cache %s: %w", paths.root, err)
	}
	paths = moduleCacheAt(realRoot)
	for _, path := range []string{paths.root, paths.gomodcache} {
		if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return moduleCachePaths{}, fmt.Errorf("dedicated module cache path is a symlink: %s", path)
		} else if err != nil && !os.IsNotExist(err) {
			return moduleCachePaths{}, fmt.Errorf("inspect dedicated module cache %s: %w", path, err)
		}
		if err := os.MkdirAll(path, 0o755); err != nil {
			return moduleCachePaths{}, fmt.Errorf("create dedicated module cache %s: %w", path, err)
		}
		real, err := filepath.EvalSymlinks(path)
		if err != nil {
			return moduleCachePaths{}, fmt.Errorf("resolve dedicated module cache %s: %w", path, err)
		}
		if filepath.Clean(real) != filepath.Clean(path) {
			return moduleCachePaths{}, fmt.Errorf("dedicated module cache path resolves outside itself: %s -> %s", path, real)
		}
	}
	return paths, nil
}

// inspectSharedModuleCache rejects every indirection and special file before
// Go is allowed to traverse the shared cache. A read-phase inspection also
// proves that hydration resealed every entry against accidental mutation.
func inspectSharedModuleCache(root string, requireReadOnly bool) error {
	paths := moduleCacheAt(root)
	for _, path := range []string{paths.root, paths.gomodcache} {
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("inspect shared module cache %s: %w", path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("shared module cache path is not a real directory: %s", path)
		}
	}
	return filepath.WalkDir(paths.gomodcache, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(paths.gomodcache, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return fmt.Errorf("module cache entry escapes root: %s", path)
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("module cache contains symlink: %s", path)
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("module cache contains non-regular entry: %s (%s)", path, info.Mode())
		}
		if requireReadOnly && info.Mode().Perm()&0o222 != 0 {
			return fmt.Errorf("module cache entry is not sealed read-only: %s (%#o)", path, info.Mode().Perm())
		}
		return nil
	})
}

func makeSharedModuleCacheWritable(root string) error {
	if err := inspectSharedModuleCache(root, false); err != nil {
		return err
	}
	return filepath.WalkDir(moduleCacheAt(root).gomodcache, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		mode := os.FileMode(0o644)
		if entry.IsDir() {
			mode = 0o755
		}
		if err := os.Chmod(path, mode); err != nil {
			return fmt.Errorf("unseal module cache entry %s: %w", path, err)
		}
		return nil
	})
}

func sealSharedModuleCache(root string) error {
	if err := inspectSharedModuleCache(root, false); err != nil {
		return err
	}
	var paths []string
	if err := filepath.WalkDir(moduleCacheAt(root).gomodcache, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		paths = append(paths, path)
		return nil
	}); err != nil {
		return err
	}
	for i := len(paths) - 1; i >= 0; i-- {
		info, err := os.Lstat(paths[i])
		if err != nil {
			return err
		}
		mode := os.FileMode(0o444)
		if info.IsDir() {
			mode = 0o555
		}
		if err := os.Chmod(paths[i], mode); err != nil {
			return fmt.Errorf("seal module cache entry %s: %w", paths[i], err)
		}
	}
	return inspectSharedModuleCache(root, true)
}

func validateHydrationNetwork(proxy, sumdb string) error {
	if proxy == "" || strings.TrimSpace(proxy) != proxy || strings.ContainsAny(proxy, ",|\r\n\x00") {
		return fmt.Errorf("module proxy must be one canonical URL, got %q", proxy)
	}
	parsed, err := url.Parse(proxy)
	if err != nil || parsed.User != nil || parsed.Opaque != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.ForceQuery {
		return fmt.Errorf("invalid module proxy %q", proxy)
	}
	if parsed.String() != proxy {
		return fmt.Errorf("module proxy URL is not canonical: %q", proxy)
	}
	switch parsed.Scheme {
	case "https":
		if parsed.Host == "" || parsed.Hostname() == "" {
			return fmt.Errorf("module proxy must name an HTTPS host")
		}
	case "file":
		if parsed.Host != "" {
			return fmt.Errorf("file module proxy must not name a host")
		}
		if !filepath.IsAbs(filepath.FromSlash(parsed.Path)) {
			return fmt.Errorf("file module proxy must use an absolute path")
		}
	default:
		return fmt.Errorf("module proxy must be one explicit https:// or file:// URL")
	}
	if sumdb != "sum.golang.org" && sumdb != "off" {
		return fmt.Errorf("module checksum database must be exactly sum.golang.org or off, got %q", sumdb)
	}
	return nil
}

func processGoState() (home, gopath, gocache, tmp, telemetry string) {
	if harnessGoHome != "" && harnessGoPath != "" && harnessGoCache != "" && harnessGoTmp != "" && harnessTelemetry != "" {
		return harnessGoHome, harnessGoPath, harnessGoCache, harnessGoTmp, harnessTelemetry
	}
	// Direct package tests do not call configureHarnessTools. Keep their Go
	// state process-local and outside the shared module cache as well.
	root := filepath.Join(os.TempDir(), fmt.Sprintf("t111-go-state-%d", os.Getpid()))
	for _, name := range []string{"home", "gopath", "build-cache", "tmp", "telemetry"} {
		_ = os.MkdirAll(filepath.Join(root, name), 0o700)
	}
	return filepath.Join(root, "home"), filepath.Join(root, "gopath"), filepath.Join(root, "build-cache"), filepath.Join(root, "tmp"), filepath.Join(root, "telemetry")
}

func moduleEnv(paths moduleCachePaths, moduleMode, proxy, sumdb string, contexts ...goBuildContext) []string {
	home, gopath, gocache, tmp, telemetry := processGoState()
	context := runtimeBuildContext(nil)
	if len(contexts) == 1 {
		context = contexts[0]
	}
	env := map[string]string{
		"CGO_ENABLED":     "0",
		"GO111MODULE":     "on",
		"GOARCH":          context.GOARCH,
		"GOCACHE":         gocache,
		"GOENV":           "off",
		"GOFLAGS":         "-mod=" + moduleMode + " -buildvcs=false",
		"GOMODCACHE":      paths.gomodcache,
		"GOOS":            context.GOOS,
		"GOPATH":          gopath,
		"GOPROXY":         proxy,
		"GOSUMDB":         sumdb,
		"GOTELEMETRY":     "off",
		"GOTELEMETRYDIR":  telemetry,
		"GOTOOLCHAIN":     "local",
		"GOTMPDIR":        tmp,
		"GOWORK":          "off",
		"HOME":            home,
		"PATH":            restrictedToolPath,
		"TMPDIR":          tmp,
		"XDG_CACHE_HOME":  filepath.Join(home, ".cache"),
		"XDG_CONFIG_HOME": filepath.Join(home, ".config"),
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

// goPackageEnv deliberately has no ambient HOME/GOPATH/GOMODCACHE fallback.
// Hydration is the only networked phase; extraction is always offline.
func goPackageEnv(modDir string) []string {
	return goPackageEnvForContext(modDir, runtimeBuildContext(nil))
}

func goPackageEnvForContext(modDir string, context goBuildContext) []string {
	paths := dedicatedModuleCache()
	moduleMode := "readonly"
	if isVendoredModule(modDir) {
		moduleMode = "vendor"
	}
	return moduleEnv(paths, moduleMode, "off", "off", context)
}

func isVendoredModule(modDir string) bool {
	info, err := os.Lstat(filepath.Join(modDir, "vendor", "modules.txt"))
	return err == nil && info.Mode().IsRegular()
}

func validateReaderModuleCache(modDir string) error {
	if isVendoredModule(modDir) {
		return nil
	}
	paths := dedicatedModuleCache()
	return inspectSharedModuleCache(paths.root, true)
}

func findHydrationModules(root string) ([]string, error) {
	var modules []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			if entry.Name() == "go.mod" {
				return fmt.Errorf("module file may not be a symlink: %s", path)
			}
			return nil
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Name() == "go.mod" {
			info, err := entry.Info()
			if err != nil || !info.Mode().IsRegular() {
				return fmt.Errorf("module file is not regular: %s", path)
			}
			modules = append(modules, filepath.Dir(path))
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("find modules to hydrate: %w", err)
	}
	sort.Strings(modules)
	return modules, nil
}

func moduleFileState(root string) (map[string]string, error) {
	state := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path != root && entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		if entry.IsDir() || (entry.Name() != "go.mod" && entry.Name() != "go.sum") {
			return nil
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("module input is not a regular file: %s", path)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		state[filepath.ToSlash(rel)] = blobDigest(content)
		return nil
	})
	return state, err
}

func sameStringMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}

func hydrateCorpusModules(entry CorpusEntry, corpusRoot, cacheRoot, proxy, sumdb string) (downloaded, vendored int, resultErr error) {
	if err := verifyCorpus(corpusRoot, entry.Commit); err != nil {
		return 0, 0, err
	}
	snapshot, cleanup, err := snapshotCorpus(corpusRoot, entry)
	if err != nil {
		return 0, 0, err
	}
	defer cleanup()
	before, err := moduleFileState(snapshot)
	if err != nil {
		return 0, 0, err
	}
	defer func() {
		after, err := moduleFileState(snapshot)
		if err == nil && !sameStringMap(before, after) {
			err = fmt.Errorf("go.mod or go.sum changed during hydration")
		}
		if corpusErr := verifyCorpus(corpusRoot, entry.Commit); corpusErr != nil {
			err = errors.Join(err, fmt.Errorf("corpus changed during hydration: %w", corpusErr))
		}
		if err != nil {
			resultErr = errors.Join(resultErr, err)
		}
	}()
	modules, err := findHydrationModules(snapshot)
	if err != nil {
		return 0, 0, err
	}
	for _, module := range modules {
		if err := validateLocalReplaces(snapshot, module); err != nil {
			return 0, 0, err
		}
	}
	context, err := corpusBuildContext(entry)
	if err != nil {
		return 0, 0, fmt.Errorf("Go analysis policy: %w", err)
	}
	targets := make([]moduleClosureTarget, 0, len(modules))
	for _, module := range modules {
		targets = append(targets, moduleClosureTarget{
			dir:          module,
			patterns:     []string{"./..."},
			buildContext: context,
			buildTags:    context.BuildTags,
			includeTests: context.IncludeTests,
		})
	}
	downloaded, vendored, err = hydrateTargetClosuresUnchecked(targets, cacheRoot, proxy, sumdb)
	if err != nil {
		return downloaded, vendored, err
	}
	return downloaded, vendored, nil
}

type moduleClosureTarget struct {
	dir          string
	patterns     []string
	buildContext goBuildContext
	buildTags    []string
	includeTests bool
}

func hydrateTargetClosuresUnchecked(targets []moduleClosureTarget, cacheRoot, proxy, sumdb string) (downloaded, vendored int, resultErr error) {
	if err := validateHydrationNetwork(proxy, sumdb); err != nil {
		return 0, 0, err
	}
	paths, err := prepareModuleCache(cacheRoot)
	if err != nil {
		return 0, 0, err
	}
	if err := makeSharedModuleCacheWritable(paths.root); err != nil {
		return 0, 0, fmt.Errorf("unseal shared module cache: %w", err)
	}
	defer func() {
		if err := sealSharedModuleCache(paths.root); err != nil {
			sealErr := fmt.Errorf("reseal shared module cache: %w", err)
			if resultErr == nil {
				resultErr = sealErr
			} else {
				resultErr = errors.Join(resultErr, sealErr)
			}
		}
	}()
	for _, target := range targets {
		if isVendoredModule(target.dir) {
			vendored++
			continue
		}
		args := []string{"list", "-deps", "-json"}
		if target.includeTests {
			args = append(args, "-test")
		}
		if len(target.buildTags) > 0 {
			tags := append([]string(nil), target.buildTags...)
			sort.Strings(tags)
			args = append(args, "-tags="+strings.Join(tags, ","))
		}
		args = append(args, target.patterns...)
		cmd := exec.Command(goExecutable, args...)
		cmd.Dir = target.dir
		context := target.buildContext
		if context.GOOS == "" || context.GOARCH == "" {
			context = runtimeBuildContext(target.buildTags)
		}
		cmd.Env = moduleEnv(paths, "readonly", proxy, sumdb, context)
		cmd.Stdout = io.Discard
		var diagnostics strings.Builder
		cmd.Stderr = &diagnostics
		err := cmd.Run()
		if err != nil {
			return downloaded, vendored, fmt.Errorf("hydrate package closure %s %s: %w: %s", target.dir, strings.Join(target.patterns, " "), err, strings.TrimSpace(diagnostics.String()))
		}
		downloaded++
	}
	return downloaded, vendored, nil
}

func hydrateHarnessModule(repoRoot, cacheRoot, proxy, sumdb string) (resultErr error) {
	before, err := rootModuleFileState(repoRoot)
	if err != nil {
		return err
	}
	defer func() {
		after, err := rootModuleFileState(repoRoot)
		if err == nil && !sameStringMap(before, after) {
			err = fmt.Errorf("harness go.mod or go.sum changed during hydration")
		}
		if err != nil {
			resultErr = errors.Join(resultErr, err)
		}
	}()
	_, _, err = hydrateTargetClosuresUnchecked([]moduleClosureTarget{{
		dir:      repoRoot,
		patterns: harnessClosurePatterns(),
	}}, cacheRoot, proxy, sumdb)
	if err != nil {
		return fmt.Errorf("hydrate T11.1 oracle dependencies: %w", err)
	}
	return nil
}

func harnessClosurePatterns() []string {
	return []string{"./spike/t111", "./spike/t111/typedcalloracle"}
}

func rootModuleFileState(root string) (map[string]string, error) {
	state := map[string]string{}
	for _, name := range []string{"go.mod", "go.sum"} {
		path := filepath.Join(root, name)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) && name == "go.sum" {
			continue
		}
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("harness module input is not a regular file: %s", path)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		state[name] = blobDigest(content)
	}
	return state, nil
}

type listedModule struct {
	Path    string        `json:"Path"`
	Version string        `json:"Version"`
	Dir     string        `json:"Dir"`
	GoMod   string        `json:"GoMod"`
	Main    bool          `json:"Main"`
	Replace *listedModule `json:"Replace"`
}

type listedPackage struct {
	ImportPath string        `json:"ImportPath"`
	Standard   bool          `json:"Standard"`
	Module     *listedModule `json:"Module"`
}

// verifyHarnessDependencyClosure directly hashes only the external modules
// used to compile the two bound harness binaries. It intentionally avoids the
// full module graph: unused historical go.sum entries are not build inputs.
func verifyHarnessDependencyClosure(snapshotRoot, commit, modDir string, patterns []string) (string, error) {
	paths := dedicatedModuleCache()
	if err := inspectSharedModuleCache(paths.root, true); err != nil {
		return "", fmt.Errorf("validate shared module cache: %w", err)
	}
	cacheReal, err := filepath.EvalSymlinks(paths.gomodcache)
	if err != nil {
		return "", fmt.Errorf("resolve shared module cache: %w", err)
	}

	args := append([]string{"list", "-deps", "-json"}, patterns...)
	cmd := exec.Command(goExecutable, args...)
	cmd.Dir = modDir
	cmd.Env = goPackageEnv(modDir)
	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", fmt.Errorf("resolve harness dependency closure: %w: %s", err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", fmt.Errorf("resolve harness dependency closure: %w", err)
	}
	rootReal, err := filepath.EvalSymlinks(snapshotRoot)
	if err != nil {
		return "", fmt.Errorf("resolve snapshot root: %w", err)
	}
	sums, err := moduleSumsForModule(modDir)
	if err != nil {
		return "", err
	}
	dec := json.NewDecoder(strings.NewReader(string(output)))
	descriptors := make(map[string]struct{})
	validated := make(map[string]struct{})
	for {
		var pkg listedPackage
		if err := dec.Decode(&pkg); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return "", fmt.Errorf("decode harness dependency closure: %w", err)
		}
		if pkg.Standard || pkg.Module == nil {
			continue
		}
		module := *pkg.Module
		effective := &module
		if module.Replace != nil {
			effective = module.Replace
		}
		key := strings.Join([]string{module.Path, module.Version, effective.Path, effective.Version, effective.Dir, effective.GoMod}, "\x00")
		if _, ok := validated[key]; ok {
			continue
		}
		validated[key] = struct{}{}
		if effective.Dir == "" {
			return "", fmt.Errorf("used module %s@%s has no source directory", effective.Path, effective.Version)
		}
		dirReal, err := filepath.EvalSymlinks(effective.Dir)
		if err != nil {
			return "", fmt.Errorf("resolve module %s@%s: %w", effective.Path, effective.Version, err)
		}
		if rel, inside := relativeWithin(rootReal, dirReal); inside {
			descriptors[strings.Join([]string{"snapshot", module.Path, module.Version, commit, filepath.ToSlash(rel)}, "\x00")] = struct{}{}
			continue
		}
		if module.Main {
			return "", fmt.Errorf("main module source is outside the pinned checkout: %s", effective.Dir)
		}
		expectedMod := sums[effective.Path+"@"+effective.Version+"/go.mod"]
		if effective.Path == "" || effective.Version == "" || expectedMod == "" {
			return "", fmt.Errorf("external module %s@%s lacks a versioned go.mod sum", effective.Path, effective.Version)
		}
		if effective.GoMod == "" {
			return "", fmt.Errorf("external module %s@%s has no resolved go.mod", effective.Path, effective.Version)
		}
		goModReal, err := filepath.EvalSymlinks(effective.GoMod)
		if err != nil {
			return "", fmt.Errorf("resolve external module %s@%s go.mod: %w", effective.Path, effective.Version, err)
		}
		if _, inside := relativeWithin(cacheReal, goModReal); !inside {
			return "", fmt.Errorf("external module %s@%s go.mod is outside the sealed cache", effective.Path, effective.Version)
		}
		goModContent, err := os.ReadFile(goModReal)
		if err != nil {
			return "", err
		}
		actualMod, err := dirhash.Hash1([]string{"go.mod"}, func(string) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(string(goModContent))), nil
		})
		if err != nil || actualMod != expectedMod {
			return "", fmt.Errorf("external module %s@%s go.mod content mismatch: got %s, want %s", effective.Path, effective.Version, actualMod, expectedMod)
		}
		expected := sums[effective.Path+"@"+effective.Version]
		if expected == "" {
			return "", fmt.Errorf("external module %s@%s has source without a versioned content sum", effective.Path, effective.Version)
		}
		if _, inside := relativeWithin(cacheReal, dirReal); !inside {
			return "", fmt.Errorf("external module %s@%s source is outside the sealed cache", effective.Path, effective.Version)
		}
		actual, err := dirhash.HashDir(dirReal, effective.Path+"@"+effective.Version, dirhash.Hash1)
		if err != nil {
			return "", fmt.Errorf("hash external module %s@%s: %w", effective.Path, effective.Version, err)
		}
		if actual != expected {
			return "", fmt.Errorf("external module %s@%s content mismatch: got %s, want %s", effective.Path, effective.Version, actual, expected)
		}
		descriptors[strings.Join([]string{"module", module.Path, module.Version, effective.Path, effective.Version, expected}, "\x00")] = struct{}{}
	}
	if len(descriptors) == 0 {
		return "", fmt.Errorf("harness dependency closure is empty")
	}
	if err := inspectSharedModuleCache(paths.root, true); err != nil {
		return "", fmt.Errorf("revalidate shared module cache: %w", err)
	}
	return digestResolvedModuleDescriptors(descriptors), nil
}

func digestResolvedModuleDescriptors(descriptors map[string]struct{}) string {
	ordered := make([]string, 0, len(descriptors))
	for descriptor := range descriptors {
		ordered = append(ordered, descriptor)
	}
	sort.Strings(ordered)
	h := sha256.New()
	for _, descriptor := range ordered {
		_, _ = fmt.Fprintf(h, "%d:%s;", len(descriptor), descriptor)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

func moduleSumsForModule(modDir string) (map[string]string, error) {
	sums := make(map[string]string)
	content, err := os.ReadFile(filepath.Join(modDir, "go.sum"))
	if os.IsNotExist(err) {
		return sums, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read module go.sum: %w", err)
	}
	for _, line := range strings.Split(string(content), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}
		key := fields[0] + "@" + fields[1]
		if prior := sums[key]; prior != "" && prior != fields[2] {
			return nil, fmt.Errorf("conflicting module sums for %s", key)
		}
		sums[key] = fields[2]
	}
	return sums, nil
}

type boundArtifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type boundHarnessManifest struct {
	Schema      string                   `json:"schema"`
	CreatedAt   string                   `json:"created_at"`
	SourceHead  string                   `json:"source_head"`
	SourceClean bool                     `json:"source_clean"`
	GoVersion   string                   `json:"go_version"`
	GoSHA256    string                   `json:"go_sha256"`
	GOOS        string                   `json:"goos"`
	GOARCH      string                   `json:"goarch"`
	Sources     map[string]string        `json:"sources"`
	Artifacts   map[string]boundArtifact `json:"artifacts"`
}

func boundSourceState(repoRoot string, requireHeadClean bool) (map[string]string, string, error) {
	var files []string
	rootFiles, err := filepath.Glob(filepath.Join(repoRoot, "spike", "t111", "*.go"))
	if err != nil {
		return nil, "", err
	}
	oracleFiles, err := filepath.Glob(filepath.Join(repoRoot, "spike", "t111", "typedcalloracle", "*.go"))
	if err != nil {
		return nil, "", err
	}
	files = append(files, filepath.Join(repoRoot, "go.mod"), filepath.Join(repoRoot, "go.sum"))
	files = append(files, rootFiles...)
	files = append(files, oracleFiles...)
	filtered := files[:0]
	for _, path := range files {
		if !strings.HasSuffix(path, "_test.go") {
			filtered = append(filtered, path)
		}
	}
	files = filtered
	sort.Strings(files)
	head, err := gitOut(repoRoot, "rev-parse", "HEAD")
	if err != nil {
		return nil, "", fmt.Errorf("resolve harness source HEAD: %w", err)
	}
	state := make(map[string]string, len(files))
	for _, path := range files {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return nil, "", fmt.Errorf("bound harness source is not a regular file: %s", path)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, "", err
		}
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return nil, "", err
		}
		rel = filepath.ToSlash(rel)
		state[rel] = blobDigest(content)
		if requireHeadClean {
			committed, err := gitBytes(repoRoot, "show", "HEAD:"+rel)
			if err != nil {
				return nil, "", fmt.Errorf("bound source %s is not committed at HEAD: %w", rel, err)
			}
			if blobDigest(committed) != state[rel] {
				return nil, "", fmt.Errorf("bound source %s differs from HEAD", rel)
			}
		}
	}
	return state, strings.TrimSpace(head), nil
}

func exactGoIdentity() (version, digest string, err error) {
	path := goExecutable
	if path == "" {
		path = "go"
	}
	resolved, err := exec.LookPath(path)
	if err != nil {
		return "", "", fmt.Errorf("resolve exact Go executable: %w", err)
	}
	resolved, err = filepath.EvalSymlinks(resolved)
	if err != nil {
		return "", "", err
	}
	content, err := os.ReadFile(resolved)
	if err != nil {
		return "", "", err
	}
	cmd := exec.Command(resolved, "version")
	cmd.Env = []string{"GOENV=off", "GOTOOLCHAIN=local", "PATH=" + filepath.Dir(resolved)}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("identify exact Go executable: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), blobDigest(content), nil
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func runOfflineGo(repoRoot string, args ...string) error {
	paths := dedicatedModuleCache()
	cmd := exec.Command(goExecutable, args...)
	cmd.Dir = repoRoot
	cmd.Env = moduleEnv(paths, "readonly", "off", "off")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("go %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func installBoundFile(source, destination string, mode os.FileMode) error {
	info, err := os.Lstat(destination)
	if err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return fmt.Errorf("bound artifact destination is not a regular file: %s", destination)
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		if err := os.Chmod(destination, 0o600); err != nil {
			return err
		}
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(destination), ".bound-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := io.Copy(tmp, in); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, destination); err != nil {
		return err
	}
	ok = true
	return nil
}

func writeBoundManifest(path string, manifest boundHarnessManifest) error {
	content, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".manifest-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(name)
		}
	}()
	if _, err := tmp.Write(content); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Chmod(0o444); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("bound manifest destination is not regular: %s", path)
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	ok = true
	return nil
}

// buildBoundHarnesses compiles the only binaries permitted to produce facts
// or the independent typed-call census. The manifest binds them to clean HEAD
// sources, the exact Go executable, and the directly h1-verified hydrated
// package closure. Verification deliberately has the same scope as hydration.
func buildBoundHarnesses(repoRoot, cacheRoot string) error {
	paths := moduleCacheAt(cacheRoot)
	if err := inspectSharedModuleCache(paths.root, true); err != nil {
		return err
	}
	sources, head, err := boundSourceState(repoRoot, true)
	if err != nil {
		return err
	}
	version, goDigest, err := exactGoIdentity()
	if err != nil {
		return err
	}
	harnessPatterns := harnessClosurePatterns()
	graphBefore, err := verifyHarnessDependencyClosure(repoRoot, head, repoRoot, harnessPatterns)
	if err != nil {
		return fmt.Errorf("directly verify harness module graph: %w", err)
	}
	buildDir, err := os.MkdirTemp("", "t111-bound-build-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(buildDir)
	targets := map[string]string{
		"t111":            "./spike/t111",
		"typedcalloracle": "./spike/t111/typedcalloracle",
	}
	names := []string{"t111", "typedcalloracle"}
	for _, name := range names {
		output := filepath.Join(buildDir, name)
		if err := runOfflineGo(repoRoot, "build", "-trimpath", "-ldflags=-buildid=", "-o", output, targets[name]); err != nil {
			return fmt.Errorf("build bound %s: %w", name, err)
		}
	}
	graphAfter, err := verifyHarnessDependencyClosure(repoRoot, head, repoRoot, harnessPatterns)
	if err != nil {
		return fmt.Errorf("reverify harness module graph: %w", err)
	}
	if graphAfter != graphBefore {
		return fmt.Errorf("harness module graph changed during bound builds")
	}
	if err := inspectSharedModuleCache(paths.root, true); err != nil {
		return fmt.Errorf("revalidate shared module cache after bound builds: %w", err)
	}
	if info, err := os.Lstat(paths.bin); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("bound binary path is not a real directory: %s", paths.bin)
		}
		if err := os.Chmod(paths.bin, 0o755); err != nil {
			return err
		}
	} else if os.IsNotExist(err) {
		if err := os.Mkdir(paths.bin, 0o755); err != nil {
			return err
		}
	} else {
		return err
	}
	artifacts := make(map[string]boundArtifact, len(names))
	for _, name := range names {
		destination := filepath.Join(paths.bin, name)
		if err := installBoundFile(filepath.Join(buildDir, name), destination, 0o555); err != nil {
			return fmt.Errorf("install bound %s: %w", name, err)
		}
		digest, err := hashFile(destination)
		if err != nil {
			return err
		}
		artifacts[name] = boundArtifact{Path: filepath.ToSlash(filepath.Join("bin", name)), SHA256: digest}
	}
	manifest := boundHarnessManifest{
		Schema:      boundManifestSchema,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		SourceHead:  head,
		SourceClean: true,
		GoVersion:   version,
		GoSHA256:    goDigest,
		GOOS:        runtime.GOOS,
		GOARCH:      runtime.GOARCH,
		Sources:     sources,
		Artifacts:   artifacts,
	}
	if err := writeBoundManifest(paths.manifest, manifest); err != nil {
		return fmt.Errorf("write bound harness manifest: %w", err)
	}
	if err := os.Chmod(paths.bin, 0o555); err != nil {
		return err
	}
	_, err = verifyBoundArtifact(repoRoot, cacheRoot, "t111", "")
	return err
}

func loadBoundManifest(path string) (boundHarnessManifest, error) {
	var manifest boundHarnessManifest
	info, err := os.Lstat(path)
	if err != nil {
		return manifest, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o222 != 0 {
		return manifest, fmt.Errorf("bound manifest is not a sealed regular file: %s", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return manifest, err
	}
	defer file.Close()
	dec := json.NewDecoder(io.LimitReader(file, 4<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&manifest); err != nil {
		return manifest, fmt.Errorf("decode bound manifest: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		return manifest, fmt.Errorf("bound manifest has trailing data")
	}
	return manifest, nil
}

func inspectBoundBinaryTree(root string) error {
	paths := moduleCacheAt(root)
	for _, path := range []string{paths.root, paths.bin} {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("bound binary path is not a real directory: %s", path)
		}
		if path == paths.bin && info.Mode().Perm()&0o222 != 0 {
			return fmt.Errorf("bound binary directory is writable: %s", path)
		}
	}
	return filepath.WalkDir(paths.bin, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return fmt.Errorf("bound binary tree contains unsafe entry: %s", path)
		}
		if info.Mode().Perm()&0o222 != 0 {
			return fmt.Errorf("bound binary tree contains writable entry: %s", path)
		}
		return nil
	})
}

// verifyBoundArtifact returns the exact executable path after independently
// checking its source, toolchain, manifest, digest, permissions, and cache.
// runningDigest, when non-empty, must equal the artifact (used by t111 main to
// reject go run and stale/unbound producer binaries).
func verifyBoundArtifact(repoRoot, cacheRoot, artifactName, runningDigest string) (string, error) {
	paths := moduleCacheAt(cacheRoot)
	if err := inspectSharedModuleCache(paths.root, true); err != nil {
		return "", err
	}
	if err := inspectBoundBinaryTree(paths.root); err != nil {
		return "", err
	}
	manifest, err := loadBoundManifest(paths.manifest)
	if err != nil {
		return "", err
	}
	if manifest.Schema != boundManifestSchema || !manifest.SourceClean || len(manifest.Artifacts) != 2 {
		return "", fmt.Errorf("bound harness manifest contract mismatch")
	}
	sources, head, err := boundSourceState(repoRoot, true)
	if err != nil {
		return "", err
	}
	if !sameStringMap(sources, manifest.Sources) {
		return "", fmt.Errorf("bound harness sources differ from the manifest")
	}
	_ = head // HEAD may advance through receipt/docs-only commits after hydration.
	version, goDigest, err := exactGoIdentity()
	if err != nil {
		return "", err
	}
	if version != manifest.GoVersion || goDigest != manifest.GoSHA256 || manifest.GOOS != runtime.GOOS || manifest.GOARCH != runtime.GOARCH {
		return "", fmt.Errorf("exact Go toolchain differs from bound manifest")
	}
	artifact, ok := manifest.Artifacts[artifactName]
	if !ok || artifact.Path != filepath.ToSlash(filepath.Join("bin", artifactName)) {
		return "", fmt.Errorf("bound artifact %q is absent or mislocated", artifactName)
	}
	path := filepath.Join(paths.root, filepath.FromSlash(artifact.Path))
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o222 != 0 {
		return "", fmt.Errorf("bound artifact is not a sealed regular file: %s", path)
	}
	digest, err := hashFile(path)
	if err != nil {
		return "", err
	}
	if digest != artifact.SHA256 || (runningDigest != "" && runningDigest != digest) {
		return "", fmt.Errorf("bound artifact %s digest mismatch", artifactName)
	}
	if err := inspectSharedModuleCache(paths.root, true); err != nil {
		return "", err
	}
	return path, nil
}

func verifyRunningBoundHarness(repoRoot, cacheRoot string) error {
	running, err := os.Executable()
	if err != nil {
		return err
	}
	runningDigest, err := hashFile(running)
	if err != nil {
		return err
	}
	_, err = verifyBoundArtifact(repoRoot, cacheRoot, "t111", runningDigest)
	if err != nil {
		return fmt.Errorf("producer must run as %s/bin/t111: %w", cacheRoot, err)
	}
	return nil
}
