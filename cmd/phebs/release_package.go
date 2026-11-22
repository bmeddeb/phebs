package main

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"

	"github.com/bmeddeb/phebs/internal/config"
)

// T48.6 default-on released selection. A published release package carries
// its signed records and artifacts beside the runtime:
//
//	<package>/runtime/phebs
//	<package>/packs/selection/*.json
//	<package>/packs/artifacts/*
//
// When the operator configures no release_selection.path, startup looks for
// exactly that layout next to the running executable and verifies it against
// the in-tree release trust anchor. A build without the layout (a development
// checkout, `make build`, an unpackaged binary) finds nothing and stays dark,
// exactly as before. Every record still binds this exact executable and the
// complete artifact census, so a package directory can only admit packs that
// were signed for the binary actually running.

// releasePackageSelectionDir and releasePackageArtifactsDir are the package
// layout, relative to the directory that holds runtime/.
const (
	releasePackageSelectionDir = "packs/selection"
	releasePackageArtifactsDir = "packs/artifacts"
)

// releaseAnchors is the in-tree release trust anchor used only for a package
// default selection. It holds public keys alone; the private keys never enter
// the repository. An explicitly configured release_selection.path keeps using
// only the operator's configured keys. It stays a variable only so tests can
// substitute a fixture key.
var releaseAnchors = []config.ReleaseKey{
	// Signed the v0.2.1-experimental.20261020 records (published anchor
	// spike/t494/release/public-key.txt); valid until those records expire.
	{ID: "ben-phebs-release-2026-10", PublicKey: "S1sOhc+R4AZVKlmSMWfSYmZWhPcvD9KcTUfrGzf1sqE="},
	// Generated on dave on 2026-11-22 for default-on packages (T48.6).
	{ID: "phebs-release-2026-11", PublicKey: "+qagVV2KRsM5KB/KtFCP8nYy69yA8b2Fag6kDc9rKXo="},
}

// releasePackageExecutable resolves the running executable for package
// discovery. It stays a variable only so tests can place a fixture package.
var releasePackageExecutable = os.Executable

// releaseSelectionSource is the effective selection startup admits from.
type releaseSelectionSource struct {
	path          string
	artifactsPath string
	keys          []config.ReleaseKey
	revoked       []string
	fromPackage   bool
}

// resolveReleaseSelection chooses the effective release selection once at the
// admitted startup boundary:
//
//   - A configured release_selection.path wins, with the configured keys.
//   - Otherwise, unless package_default is off, the release package layout next
//     to the running executable is used with the in-tree trust anchor.
//   - Otherwise nothing is selected and startup stays dark.
//
// ok is false when no selection applies. A package whose selection directory
// exists but is malformed refuses startup rather than silently going dark.
func resolveReleaseSelection(cfg *config.Config) (releaseSelectionSource, bool, error) {
	selection := cfg.ReleaseSelection
	if selection.Path != "" {
		return releaseSelectionSource{
			path:          selection.Path,
			artifactsPath: selection.ArtifactsPath,
			keys:          selection.Keys,
			revoked:       selection.Revoked,
		}, true, nil
	}
	if selection.PackageDefault == config.ReleasePackageDefaultOff {
		return releaseSelectionSource{}, false, nil
	}
	root, ok := releasePackageRoot()
	if !ok {
		return releaseSelectionSource{}, false, nil
	}
	selectionDir := filepath.Join(root, releasePackageSelectionDir)
	present, err := releasePackageDir(selectionDir)
	if err != nil || !present {
		return releaseSelectionSource{}, false, err
	}
	artifactsDir := filepath.Join(root, releasePackageArtifactsDir)
	present, err = releasePackageDir(artifactsDir)
	if err != nil {
		return releaseSelectionSource{}, false, err
	}
	if !present {
		return releaseSelectionSource{}, false, fmt.Errorf(
			"release package %s has %s but no %s, so no load could be bound",
			root, releasePackageSelectionDir, releasePackageArtifactsDir)
	}
	return releaseSelectionSource{
		path:          selectionDir,
		artifactsPath: artifactsDir,
		keys:          releaseAnchors,
		revoked:       selection.Revoked,
		fromPackage:   true,
	}, true, nil
}

// releasePackageRoot returns the directory that would hold a release package's
// packs/ for the running executable: the parent of the executable's directory,
// after resolving symlinks so an installed link to <package>/runtime/phebs
// still finds its package. An unresolvable executable is not a package.
func releasePackageRoot() (string, bool) {
	executable, err := releasePackageExecutable()
	if err != nil || executable == "" {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return "", false
	}
	return filepath.Dir(filepath.Dir(resolved)), true
}

// releasePackageDir reports whether dir is present as a real directory. An
// absent path is not an error; a symlink or a non-directory in the package
// layout refuses startup, matching the configured path's stability rule.
func releasePackageDir(dir string) (bool, error) {
	info, err := os.Lstat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("release package %s: %w", dir, err)
	case !info.IsDir():
		return false, fmt.Errorf("release package %s must be a real directory", dir)
	}
	return true, nil
}

// logPackageSelection names the package default in one bounded startup line so
// an operator can see why packs are on without a release_selection block.
func logPackageSelection(source releaseSelectionSource) {
	if source.fromPackage {
		log.Printf("pack release selection: package default %s (set release_selection.package_default: off to stay dark)", source.path)
	}
}
