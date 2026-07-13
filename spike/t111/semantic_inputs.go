package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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
		validationKey := strings.Join([]string{
			module.Path, module.Version, replacement, effective.Path, effective.Version, effective.Dir,
		}, "\x00")
		if _, alreadyValidated := validatedModules[validationKey]; alreadyValidated {
			return
		}
		validatedModules[validationKey] = struct{}{}
		if effective.Dir == "" {
			validationErr = fmt.Errorf("module %s@%s has no resolved directory", module.Path, module.Version)
			return
		}
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
		if effective.Path == "" || effective.Version == "" || expectedSum == "" {
			validationErr = fmt.Errorf("external module %s@%s lacks a versioned content sum", effective.Path, effective.Version)
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
			effective.Path, effective.Version, expectedSum,
		}, "\x00")] = struct{}{}
	})
	if validationErr != nil {
		return "", validationErr
	}
	if len(descriptors) == 0 {
		return "", fmt.Errorf("go package graph has no bound module inputs")
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
			if len(fields) != 3 || strings.HasSuffix(fields[1], "/go.mod") {
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
