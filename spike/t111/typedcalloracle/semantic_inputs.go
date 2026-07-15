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

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/sumdb/dirhash"
	"golang.org/x/tools/go/packages"
)

// verifyPackageSemanticInputs proves that every external module used by the
// Go type checker still has the content committed by a go.sum in the checkout.
// Local modules and replacements are instead bound to the exact commit and
// repository-relative directory. Vendored dependencies are bound to their
// commit-backed vendor roots because go/packages omits Module.Dir for them.
func verifyPackageSemanticInputs(snapshotRoot, commit, moduleCache string, pkgs []*packages.Package) (string, error) {
	rootReal, err := filepath.EvalSymlinks(snapshotRoot)
	if err != nil {
		return "", fmt.Errorf("resolve snapshot root: %w", err)
	}
	sums, err := snapshotModuleSums(rootReal)
	if err != nil {
		return "", err
	}

	descriptors := make(map[string]struct{})
	validatedModules := make(map[string]struct{})
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

		// In vendor mode, go/packages reports the required module identity but
		// leaves Module.Dir empty. The import path remains rooted below the
		// required module path even for a replaced implementation.
		if effective.Dir == "" {
			vendorRel, vendorErr := semanticVendoredModuleRoot(rootReal, module.Path, pkg)
			if vendorErr != nil {
				validationErr = fmt.Errorf("module %s@%s has no resolved directory: %w", module.Path, module.Version, vendorErr)
				return
			}
			validationKey := strings.Join([]string{
				module.Path, module.Version, replacement, effective.Path, effective.Version, vendorRel,
			}, "\x00")
			if _, ok := validatedModules[validationKey]; ok {
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
		if _, ok := validatedModules[validationKey]; ok {
			return
		}
		validatedModules[validationKey] = struct{}{}

		dirReal, resolveErr := filepath.EvalSymlinks(effective.Dir)
		if resolveErr != nil {
			validationErr = fmt.Errorf("resolve module %s@%s directory: %w", module.Path, module.Version, resolveErr)
			return
		}
		if rel, inside := semanticRelativeWithin(rootReal, dirReal); inside {
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
			if cacheErr := inspectOracleModuleCache(moduleCache); cacheErr != nil {
				validationErr = fmt.Errorf("validate sealed module cache: %w", cacheErr)
				return
			}
			cacheReal, resolveErr = filepath.EvalSymlinks(filepath.Join(moduleCache, "gomodcache"))
			if resolveErr != nil {
				validationErr = fmt.Errorf("resolve sealed module cache: %w", resolveErr)
				return
			}
		}
		if _, inside := semanticRelativeWithin(cacheReal, dirReal); !inside {
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
		if _, inside := semanticRelativeWithin(cacheReal, goModReal); !inside {
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
		if err := inspectOracleModuleCache(moduleCache); err != nil {
			return "", fmt.Errorf("revalidate sealed module cache: %w", err)
		}
	}
	return digestSemanticDescriptors(descriptors), nil
}

func semanticVendoredModuleRoot(snapshotRoot, modulePath string, pkg *packages.Package) (string, error) {
	if modulePath == "" {
		return "", fmt.Errorf("missing module path")
	}
	marker := "vendor/" + modulePath
	roots := make(map[string]struct{})
	files := append(append([]string(nil), pkg.GoFiles...), pkg.CompiledGoFiles...)
	for _, source := range files {
		if source == "" {
			continue
		}
		real, err := filepath.EvalSymlinks(source)
		if err != nil {
			return "", fmt.Errorf("resolve vendored source %s: %w", source, err)
		}
		rel, inside := semanticRelativeWithin(snapshotRoot, real)
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
		roots[rel[:start+len(marker)]] = struct{}{}
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
	sums := make(map[string]string)
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

// validateLocalReplaces rejects module sources whose content is selected by a
// host path rather than by the pinned checkout or a versioned module sum.
func validateLocalReplaces(snapshotRoot, modDir string) error {
	content, err := os.ReadFile(filepath.Join(modDir, "go.mod"))
	if err != nil {
		return fmt.Errorf("read go.mod: %w", err)
	}
	parsed, err := modfile.Parse("go.mod", content, nil)
	if err != nil {
		return fmt.Errorf("parse go.mod: %w", err)
	}
	rootReal, err := filepath.EvalSymlinks(snapshotRoot)
	if err != nil {
		return fmt.Errorf("resolve snapshot root: %w", err)
	}
	for _, replacement := range parsed.Replace {
		if replacement.New.Version != "" {
			continue
		}
		if filepath.IsAbs(replacement.New.Path) {
			return fmt.Errorf("local replace %s => %s is absolute", replacement.Old.Path, replacement.New.Path)
		}
		target := filepath.Clean(filepath.Join(modDir, filepath.FromSlash(replacement.New.Path)))
		targetReal, err := filepath.EvalSymlinks(target)
		if err != nil {
			return fmt.Errorf("resolve local replace %s => %s: %w", replacement.Old.Path, replacement.New.Path, err)
		}
		if _, inside := semanticRelativeWithin(rootReal, targetReal); !inside {
			return fmt.Errorf("local replace %s => %s escapes the immutable snapshot", replacement.Old.Path, replacement.New.Path)
		}
		info, err := os.Stat(targetReal)
		if err != nil || !info.IsDir() {
			return fmt.Errorf("local replace %s => %s is not an in-snapshot directory", replacement.Old.Path, replacement.New.Path)
		}
	}
	return nil
}

func combineSemanticInputDigests(byModule map[string]string) (string, error) {
	if len(byModule) == 0 {
		return "", fmt.Errorf("no module semantic input digests")
	}
	descriptors := make(map[string]struct{}, len(byModule))
	for module, digest := range byModule {
		if module == "" || digest == "" {
			return "", fmt.Errorf("incomplete module semantic input digest")
		}
		descriptors[strings.Join([]string{"module-scan", module, digest}, "\x00")] = struct{}{}
	}
	return digestSemanticDescriptors(descriptors), nil
}

func digestSemanticDescriptors(descriptors map[string]struct{}) string {
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

func semanticRelativeWithin(root, candidate string) (string, bool) {
	rel, err := filepath.Rel(root, candidate)
	if err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}
