package releasebundle

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// T48.6 slice 2: a default-on release package wraps a verified bundle as
// runtime/ and signs every pack template for that exact runtime/phebs into a
// sibling packs/selection, beside a complete copy of the artifacts the records
// reference:
//
//	<package>/runtime/...            the verified bundle, byte for byte
//	<package>/packs/artifacts/*      the flat artifact census
//	<package>/packs/selection/*.json one signed record per template
//
// This is the layout the runtime's package default (cmd/phebs
// release_package.go) admits from an empty configuration.

const (
	PackageRuntimeDir   = "runtime"
	PackageArtifactsDir = "packs/artifacts"
	PackageSelectionDir = "packs/selection"

	maxPackageTemplates = 64
	maxPackageArtifacts = 1024
	maxPackageFileBytes = 1 << 20
)

// SignFunc signs one template for the runtime executable phebs, against the
// artifact directory, into out. Production runs `phebs release-sign`, so every
// binding is derived by the binary being released and self-verified through
// the startup loader before success.
type SignFunc func(ctx context.Context, phebs, template, artifacts, out string) error

// PackageOptions name the inputs of one release package.
type PackageOptions struct {
	BundleDir    string
	OutputDir    string
	TemplatesDir string
	ArtifactsDir string
	Sign         SignFunc
}

// Package assembles a signed default-on release package. The output must not
// exist; the package is assembled in a sibling temporary directory and renamed
// into place only after every record is signed and the runtime re-verifies.
func Package(ctx context.Context, opts PackageOptions) error {
	if opts.BundleDir == "" || opts.OutputDir == "" || opts.TemplatesDir == "" || opts.ArtifactsDir == "" || opts.Sign == nil {
		return errors.New("package requires a bundle, output, templates, artifacts and signer")
	}
	if _, err := Verify(opts.BundleDir); err != nil {
		return fmt.Errorf("verify bundle before packaging: %w", err)
	}
	if _, err := os.Lstat(opts.OutputDir); err == nil {
		return fmt.Errorf("package output %s already exists", opts.OutputDir)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	templates, err := flatRegularFiles(opts.TemplatesDir, maxPackageTemplates, ".json")
	if err != nil {
		return fmt.Errorf("pack templates: %w", err)
	}
	if len(templates) == 0 {
		return fmt.Errorf("pack templates: %s holds no *.json template", opts.TemplatesDir)
	}
	artifacts, err := flatRegularFiles(opts.ArtifactsDir, maxPackageArtifacts, "")
	if err != nil {
		return fmt.Errorf("pack artifacts: %w", err)
	}

	parent := filepath.Dir(opts.OutputDir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(parent, "."+filepath.Base(opts.OutputDir)+".tmp-")
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(staging)
		}
	}()
	if err := os.Chmod(staging, 0o755); err != nil {
		return err
	}

	runtimeDir := filepath.Join(staging, PackageRuntimeDir)
	if err := copyTree(opts.BundleDir, runtimeDir); err != nil {
		return fmt.Errorf("copy bundle into runtime/: %w", err)
	}
	artifactsDir := filepath.Join(staging, PackageArtifactsDir)
	selectionDir := filepath.Join(staging, PackageSelectionDir)
	for _, dir := range []string{artifactsDir, selectionDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	for _, name := range artifacts {
		if err := copyRegular(filepath.Join(opts.ArtifactsDir, name), filepath.Join(artifactsDir, name), 0o644); err != nil {
			return fmt.Errorf("copy artifact %s: %w", name, err)
		}
	}
	phebs := filepath.Join(runtimeDir, "phebs")
	for _, name := range templates {
		out := filepath.Join(selectionDir, name)
		if err := opts.Sign(ctx, phebs, filepath.Join(opts.TemplatesDir, name), artifactsDir, out); err != nil {
			return fmt.Errorf("sign %s: %w", name, err)
		}
		if info, err := os.Lstat(out); err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("sign %s: no signed record was written", name)
		}
	}
	if _, err := Verify(runtimeDir); err != nil {
		return fmt.Errorf("runtime changed while packaging: %w", err)
	}
	if err := os.Rename(staging, opts.OutputDir); err != nil {
		return err
	}
	committed = true
	return nil
}

// flatRegularFiles lists dir's entries, which must all be regular files (no
// subdirectories or symlinks) with an optional suffix, at most limit of them,
// each at most maxPackageFileBytes. Names are returned sorted.
func flatRegularFiles(dir string, limit int, suffix string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	if len(entries) > limit {
		return nil, fmt.Errorf("%s holds %d entries, more than %d", dir, len(entries), limit)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%s/%s is not a regular file", dir, entry.Name())
		}
		if info.Size() > maxPackageFileBytes {
			return nil, fmt.Errorf("%s/%s exceeds %d bytes", dir, entry.Name(), maxPackageFileBytes)
		}
		if suffix != "" && !strings.HasSuffix(entry.Name(), suffix) {
			return nil, fmt.Errorf("%s/%s is not a %s file", dir, entry.Name(), suffix)
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names, nil
}

// copyTree copies a verified bundle directory, preserving each regular file's
// permission bits. Anything other than directories and regular files refuses.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case entry.IsDir():
			return os.MkdirAll(target, 0o755)
		case info.Mode().IsRegular():
			return copyRegular(path, target, info.Mode().Perm())
		default:
			return fmt.Errorf("%s is neither a directory nor a regular file", path)
		}
	})
}

func copyRegular(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, mode)
}
