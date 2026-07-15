package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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

func inspectOracleReaderCache(module, moduleCache string) error {
	if info, err := os.Lstat(filepath.Join(module, "vendor", "modules.txt")); err == nil && info.Mode().IsRegular() {
		return nil
	}
	return inspectOracleModuleCache(moduleCache)
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
