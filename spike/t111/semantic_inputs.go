package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/sumdb/dirhash"
	"golang.org/x/tools/go/packages"
)

// verifyPackageSemanticInputs proves that every external module used by the
// Go type checker still has the content committed by its module sum. Local
// modules and replaces must live in the exact Git snapshot and are bound by
// commit plus repository-relative directory. The returned digest is recorded
// on every type-derived fact, so a shared module cache is never an unrecorded
// semantic input.
func verifyPackageSemanticInputs(snapshotRoot, commit string, pkgs []*packages.Package) (string, error) {
	rootReal, err := filepath.EvalSymlinks(snapshotRoot)
	if err != nil {
		return "", fmt.Errorf("resolve snapshot root: %w", err)
	}
	descriptors := map[string]struct{}{}
	validatedModules := map[string]struct{}{}
	sums, err := snapshotModuleSums(rootReal)
	if err != nil {
		return "", err
	}
	var validationErr error
	cacheReal := ""
	packages.Visit(pkgs, nil, func(pkg *packages.Package) {
		if validationErr != nil || pkg == nil || pkg.Module == nil {
			return
		}
		module := pkg.Module
		effective := module
		replacement := ""
		if module.Replace != nil {
			effective = module.Replace
			replacement = module.Replace.Path + "@" + module.Replace.Version
		}
		// In -mod=vendor mode go/packages reports the module identity but
		// leaves Module.Dir empty. Bind that dependency to the exact vendored
		// directory in the pinned snapshot; never mistake an arbitrary source
		// path for vendored content.
		if effective.Dir == "" {
			// Go's vendor tree is keyed by the required/import module path,
			// even when modules.txt records a replacement implementation.
			vendorRel, vendorErr := vendoredModuleRoot(rootReal, module.Path, pkg)
			if vendorErr != nil {
				validationErr = fmt.Errorf("module %s@%s has no resolved directory: %w", module.Path, module.Version, vendorErr)
				return
			}
			validationKey := strings.Join([]string{
				module.Path, module.Version, replacement, effective.Path, effective.Version, vendorRel,
			}, "\x00")
			if _, alreadyValidated := validatedModules[validationKey]; alreadyValidated {
				return
			}
			validatedModules[validationKey] = struct{}{}
			descriptors[strings.Join([]string{
				"snapshot-vendor", module.Path, module.Version, replacement,
				effective.Path, effective.Version, commit, vendorRel,
			}, "\x00")] = struct{}{}
			return
		}
		validationKey := strings.Join([]string{
			module.Path, module.Version, replacement, effective.Path, effective.Version, effective.Dir,
		}, "\x00")
		if _, alreadyValidated := validatedModules[validationKey]; alreadyValidated {
			return
		}
		validatedModules[validationKey] = struct{}{}
		dirReal, resolveErr := filepath.EvalSymlinks(effective.Dir)
		if resolveErr != nil {
			validationErr = fmt.Errorf("resolve module %s@%s directory: %w", module.Path, module.Version, resolveErr)
			return
		}
		if rel, inside := relativeWithin(rootReal, dirReal); inside {
			descriptors[strings.Join([]string{
				"snapshot", module.Path, module.Version, replacement, commit, filepath.ToSlash(rel),
			}, "\x00")] = struct{}{}
			return
		}
		expectedSum := sums[effective.Path+"@"+effective.Version]
		expectedModSum := sums[effective.Path+"@"+effective.Version+"/go.mod"]
		if effective.Path == "" || effective.Version == "" || expectedSum == "" || expectedModSum == "" {
			validationErr = fmt.Errorf("external module %s@%s lacks a versioned content sum", effective.Path, effective.Version)
			return
		}
		if cacheReal == "" {
			paths := dedicatedModuleCache()
			if cacheErr := inspectSharedModuleCache(paths.root, true); cacheErr != nil {
				validationErr = fmt.Errorf("validate sealed module cache: %w", cacheErr)
				return
			}
			cacheReal, resolveErr = filepath.EvalSymlinks(paths.gomodcache)
			if resolveErr != nil {
				validationErr = fmt.Errorf("resolve sealed module cache: %w", resolveErr)
				return
			}
		}
		if _, inside := relativeWithin(cacheReal, dirReal); !inside {
			validationErr = fmt.Errorf("external module %s@%s source is outside the sealed module cache", effective.Path, effective.Version)
			return
		}
		if effective.GoMod == "" {
			validationErr = fmt.Errorf("external module %s@%s has no resolved go.mod", effective.Path, effective.Version)
			return
		}
		goModReal, resolveErr := filepath.EvalSymlinks(effective.GoMod)
		if resolveErr != nil {
			validationErr = fmt.Errorf("resolve external module %s@%s go.mod: %w", effective.Path, effective.Version, resolveErr)
			return
		}
		if _, inside := relativeWithin(cacheReal, goModReal); !inside {
			validationErr = fmt.Errorf("external module %s@%s go.mod is outside the sealed module cache", effective.Path, effective.Version)
			return
		}
		goModContent, readErr := os.ReadFile(goModReal)
		if readErr != nil {
			validationErr = fmt.Errorf("read external module %s@%s go.mod: %w", effective.Path, effective.Version, readErr)
			return
		}
		actualModSum, modHashErr := dirhash.Hash1([]string{"go.mod"}, func(string) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(string(goModContent))), nil
		})
		if modHashErr != nil {
			validationErr = fmt.Errorf("hash external module %s@%s go.mod: %w", effective.Path, effective.Version, modHashErr)
			return
		}
		if actualModSum != expectedModSum {
			validationErr = fmt.Errorf("external module %s@%s go.mod content mismatch: got %s, want %s", effective.Path, effective.Version, actualModSum, expectedModSum)
			return
		}
		actual, hashErr := dirhash.HashDir(dirReal, effective.Path+"@"+effective.Version, dirhash.Hash1)
		if hashErr != nil {
			validationErr = fmt.Errorf("hash external module %s@%s: %w", effective.Path, effective.Version, hashErr)
			return
		}
		if actual != expectedSum {
			validationErr = fmt.Errorf("external module %s@%s content mismatch: got %s, want %s", effective.Path, effective.Version, actual, expectedSum)
			return
		}
		descriptors[strings.Join([]string{
			"module", module.Path, module.Version, replacement,
			effective.Path, effective.Version, expectedSum, expectedModSum,
		}, "\x00")] = struct{}{}
	})
	if validationErr != nil {
		return "", validationErr
	}
	if len(descriptors) == 0 {
		return "", fmt.Errorf("go package graph has no bound module inputs")
	}
	if cacheReal != "" {
		if err := inspectSharedModuleCache(dedicatedModuleCache().root, true); err != nil {
			return "", fmt.Errorf("revalidate sealed module cache: %w", err)
		}
	}
	ordered := make([]string, 0, len(descriptors))
	for descriptor := range descriptors {
		ordered = append(ordered, descriptor)
	}
	sort.Strings(ordered)
	h := sha256.New()
	for _, descriptor := range ordered {
		_, _ = fmt.Fprintf(h, "%d:%s;", len(descriptor), descriptor)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func vendoredModuleRoot(snapshotRoot, modulePath string, pkg *packages.Package) (string, error) {
	if modulePath == "" {
		return "", fmt.Errorf("missing module path")
	}
	marker := "vendor/" + modulePath
	roots := map[string]bool{}
	files := append(append([]string(nil), pkg.GoFiles...), pkg.CompiledGoFiles...)
	for _, source := range files {
		if source == "" {
			continue
		}
		real, err := filepath.EvalSymlinks(source)
		if err != nil {
			return "", fmt.Errorf("resolve vendored source %s: %w", source, err)
		}
		rel, inside := relativeWithin(snapshotRoot, real)
		if !inside {
			return "", fmt.Errorf("source %s is outside the pinned snapshot", source)
		}
		rel = filepath.ToSlash(rel)
		start := -1
		if strings.HasPrefix(rel, marker+"/") || rel == marker {
			start = 0
		} else if index := strings.Index(rel, "/"+marker+"/"); index >= 0 {
			start = index + 1
		} else if strings.HasSuffix(rel, "/"+marker) {
			start = len(rel) - len(marker)
		}
		if start < 0 {
			return "", fmt.Errorf("source %s is not below %s", rel, marker)
		}
		root := rel[:start+len(marker)]
		roots[root] = true
	}
	if len(roots) != 1 {
		ordered := make([]string, 0, len(roots))
		for root := range roots {
			ordered = append(ordered, root)
		}
		sort.Strings(ordered)
		return "", fmt.Errorf("vendored sources resolve to %d module roots: %v", len(roots), ordered)
	}
	for root := range roots {
		info, err := os.Stat(filepath.Join(snapshotRoot, filepath.FromSlash(root)))
		if err != nil || !info.IsDir() {
			return "", fmt.Errorf("vendored module root %s is not a snapshot directory", root)
		}
		return root, nil
	}
	return "", fmt.Errorf("vendored package has no source files")
}

func snapshotModuleSums(root string) (map[string]string, error) {
	sums := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "node_modules" || entry.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Name() != "go.sum" {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("open %s: %w", path, err)
		}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) != 3 {
				continue
			}
			key := fields[0] + "@" + fields[1]
			if previous := sums[key]; previous != "" && previous != fields[2] {
				_ = file.Close()
				return fmt.Errorf("conflicting module sums for %s", key)
			}
			sums[key] = fields[2]
		}
		scanErr := scanner.Err()
		closeErr := file.Close()
		if scanErr != nil {
			return fmt.Errorf("scan %s: %w", path, scanErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close %s: %w", path, closeErr)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("load snapshot module sums: %w", err)
	}
	return sums, nil
}

func relativeWithin(root, candidate string) (string, bool) {
	rel, err := filepath.Rel(root, candidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}

func bindSemanticInputs(facts []Fact, digest string) []Fact {
	for index := range facts {
		fact := &facts[index]
		fact.SemanticInputsDigest = digest
		fact.FactFingerprint = factFingerprintWithInputs(
			fact.Predicate, fact.Subject, fact.Object, fact.Detail, digest,
		)
		fact.AtomID = fact.atom().ID()
	}
	return facts
}
