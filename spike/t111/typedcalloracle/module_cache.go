package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/mod/sumdb/dirhash"
)

func prepareOracleModuleCache(configured string) (string, func(), error) {
	if configured == "" {
		root, err := os.MkdirTemp("", "t111-empty-module-cache-*")
		if err != nil {
			return "", func() {}, err
		}
		if err := os.Mkdir(filepath.Join(root, "gomodcache"), 0o555); err != nil {
			_ = os.RemoveAll(root)
			return "", func() {}, err
		}
		return root, func() { _ = os.RemoveAll(root) }, nil
	}
	absolute, err := filepath.Abs(configured)
	if err != nil {
		return "", func() {}, fmt.Errorf("resolve dedicated module cache: %w", err)
	}
	if err := inspectOracleModuleCache(absolute); err != nil {
		return "", func() {}, err
	}
	return absolute, func() {}, nil
}

func createOracleRunState(root string) error {
	for _, name := range []string{"home", "gopath", "build-cache", "tmp", "telemetry"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o700); err != nil {
			return fmt.Errorf("create isolated Go %s directory: %w", name, err)
		}
	}
	return nil
}

func inspectOracleModuleCache(root string) error {
	root = filepath.Clean(root)
	gomodcache := filepath.Join(root, "gomodcache")
	for _, path := range []string{root, gomodcache} {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("shared module cache path is not a real directory: %s", path)
		}
	}
	return filepath.WalkDir(gomodcache, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(gomodcache, path)
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
			return fmt.Errorf("module cache contains non-regular entry: %s", path)
		}
		if info.Mode().Perm()&0o222 != 0 {
			return fmt.Errorf("module cache entry is not sealed read-only: %s", path)
		}
		return nil
	})
}

func oracleExecutableDigest() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve typed oracle executable: %w", err)
	}
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

type oracleListedModule struct {
	Path    string              `json:"Path"`
	Version string              `json:"Version"`
	Dir     string              `json:"Dir"`
	GoMod   string              `json:"GoMod"`
	Main    bool                `json:"Main"`
	Replace *oracleListedModule `json:"Replace"`
}

func verifyResolvedModuleGraph(snapshotRoot, commit, modDir, moduleCache, runRoot string) (string, error) {
	if info, err := os.Lstat(filepath.Join(modDir, "vendor", "modules.txt")); err == nil && info.Mode().IsRegular() {
		return digestSemanticDescriptors(map[string]struct{}{
			strings.Join([]string{"snapshot-vendor-module", commit, displayModule(snapshotRoot, modDir)}, "\x00"): {},
		}), nil
	}
	if err := inspectOracleModuleCache(moduleCache); err != nil {
		return "", err
	}
	cacheReal, err := filepath.EvalSymlinks(filepath.Join(moduleCache, "gomodcache"))
	if err != nil {
		return "", err
	}
	cmd := exec.Command("go", "list", "-m", "-json", "all")
	cmd.Dir = modDir
	cmd.Env = packageEnv(modDir, moduleCache, runRoot)
	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", fmt.Errorf("resolve module graph: %w: %s", err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", fmt.Errorf("resolve module graph: %w", err)
	}
	rootReal, err := filepath.EvalSymlinks(snapshotRoot)
	if err != nil {
		return "", err
	}
	sums, err := oracleModuleSums(modDir)
	if err != nil {
		return "", err
	}
	descriptors := make(map[string]struct{})
	decoder := json.NewDecoder(strings.NewReader(string(output)))
	for {
		var module oracleListedModule
		if err := decoder.Decode(&module); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return "", fmt.Errorf("decode module graph: %w", err)
		}
		effective := &module
		if module.Replace != nil {
			effective = module.Replace
		}
		dirReal := ""
		if effective.Dir != "" {
			dirReal, err = filepath.EvalSymlinks(effective.Dir)
			if err != nil {
				return "", err
			}
			if module.Main && filepath.Clean(dirReal) == filepath.Clean(rootReal) {
				descriptors[strings.Join([]string{"snapshot", module.Path, module.Version, commit, "."}, "\x00")] = struct{}{}
				continue
			}
			if rel, inside := relativeWithin(rootReal, dirReal); inside {
				descriptors[strings.Join([]string{"snapshot", module.Path, module.Version, commit, filepath.ToSlash(rel)}, "\x00")] = struct{}{}
				continue
			}
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
			return "", err
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
		if dirReal == "" {
			descriptors[strings.Join([]string{"module-metadata", module.Path, module.Version, effective.Path, effective.Version, expectedMod}, "\x00")] = struct{}{}
			continue
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
		return "", errors.New("resolved module graph is empty")
	}
	if err := inspectOracleModuleCache(moduleCache); err != nil {
		return "", err
	}
	return digestSemanticDescriptors(descriptors), nil
}

func oracleModuleSums(modDir string) (map[string]string, error) {
	result := make(map[string]string)
	content, err := os.ReadFile(filepath.Join(modDir, "go.sum"))
	if os.IsNotExist(err) {
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(string(content), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}
		key := fields[0] + "@" + fields[1]
		if previous := result[key]; previous != "" && previous != fields[2] {
			return nil, fmt.Errorf("conflicting module sums for %s", key)
		}
		result[key] = fields[2]
	}
	return result, nil
}
