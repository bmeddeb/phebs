//go:build linux

package t421

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
)

// Git exec-path census bounds. The measured Ubuntu exec-path holds 172 root
// entries — 145 symlinks, 26 regular helpers and one mergetools directory of 24
// regular files totalling 31,571 bytes — so these admit the real shape with
// headroom while keeping the observation two directory levels deep and far below
// an unbounded host scan. Only the selected core image and the exec-path "git"
// entry are hashed in full; every other helper contributes one Lstat plus at
// most a 64-byte header read.
const (
	maxGitExecPathEntries             = 512
	maxGitExecPathSubdirectories      = 4
	maxGitExecPathSubdirectoryEntries = 128
	maxGitExecPathSubdirectoryBytes   = 64 << 20
	maxGitExecPathSymlinkTargets      = 8
)

// ExecutionGitExecPathManifest is a path-free structural description of the
// helper directory one explicitly selected Git core image actually delegates to.
// It records how many helpers exist, what class each one is, how many symlinks
// collapse onto the core image and how many distinct targets remain, plus a
// digest over the canonical sorted encoding of every censused row. It carries no
// absolute host path, no helper name and no helper content. It proves the
// exec-path shape was observed and bounded; it does not prove helper
// immutability, a vendor signature, the absence of runtime delegation outside
// this directory, or any launch right.
type ExecutionGitExecPathManifest struct {
	Role                string
	CoreSHA256          string
	ManifestSHA256      string
	Entries             int
	RegularFiles        int
	Symlinks            int
	Directories         int
	CoreImageSymlinks   int
	SymlinkTargets      int
	NativeHelpers       int
	ScriptHelpers       int
	TextHelpers         int
	SubdirectoryEntries int
	SubdirectoryBytes   int64
	Provenance          string
}

// ObserveExecutionGitExecPathManifest measures the exec-path that one explicitly
// selected Git core image reports, under a closed probe environment with no
// ambient PATH or GIT_EXEC_PATH. The observation is structural rather than
// nominal: each entry is classified by Lstat type, symlink target, size,
// permission bits and a 64-byte header screen, and the directory read refuses
// instead of truncating when it exceeds its bound. Only two paths are ever hashed
// in full — the selected image, at each of its verification points, and the
// exec-path "git" entry, which must be byte-equal to it — so a 172-entry
// exec-path never becomes a whole-tree digest and every other helper costs one
// Lstat plus at most a 64-byte header read. This issues no
// CheckoutAdmissionBinding, no launcher authority, no session teardown and no
// dispatch admission, and validateExecutionHost remains the separate
// freeze-platform fence.
func ObserveExecutionGitExecPathManifest(ctx context.Context, binary string) (manifest ExecutionGitExecPathManifest, retErr error) {
	if ctx == nil || runtime.GOARCH != "amd64" {
		return manifest, errors.New("external Git exec-path manifest requires a context and the frozen Linux platform")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if ctx.Err() != nil {
		return manifest, errors.New("external Git exec-path observation canceled")
	}
	resolved, digest, err := admitExternalDelegationImage(ctx, binary)
	if err != nil {
		return manifest, err
	}
	output, err := runExternalDelegationProbe(ctx, resolved, digest, "--exec-path")
	if err != nil {
		return manifest, err
	}
	directory, err := admitExternalDelegationDirectory(output)
	if err != nil {
		return manifest, err
	}
	manifest, err = censusExternalGitExecPath(ctx, directory, digest)
	if err != nil {
		return ExecutionGitExecPathManifest{}, err
	}
	if observed, hashErr := t4013.DigestHostExecutable(ctx, resolved); hashErr != nil || observed != digest || ctx.Err() != nil {
		return ExecutionGitExecPathManifest{}, errors.New("external Git image changed or exec-path observation expired")
	}
	return manifest, nil
}

// censusExternalGitExecPath binds the reported exec-path shape to the admitted
// core digest. It requires exactly one regular "git" entry that hashes equal to
// the selected image and at least one symlink delegating to it, so a directory of
// unrelated helpers or a lone copied core cannot satisfy the recipe. Counters
// accumulate in a local and reach the caller only through the single success
// return, so every refusal yields the zero manifest rather than a partial
// description of an unadmitted directory.
func censusExternalGitExecPath(ctx context.Context, directory, digest string) (manifest ExecutionGitExecPathManifest, retErr error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return manifest, errors.New("external Git exec-path directory cannot be opened as a census root")
	}
	defer func() {
		if root.Close() != nil {
			retErr = errors.Join(retErr, errors.New("external Git exec-path directory cannot be closed"))
			manifest = ExecutionGitExecPathManifest{}
		}
	}()
	entries, err := censusExternalDelegationDirectory(ctx, root, directory, ".", maxGitExecPathEntries)
	if err != nil {
		return manifest, err
	}
	named, err := externalDelegationNamed(entries)
	if err != nil {
		return manifest, errors.New("external Git exec-path entry is duplicated")
	}
	observed := ExecutionGitExecPathManifest{Role: "git", CoreSHA256: digest, Entries: len(entries),
		Provenance: "external-exec-path-manifest-linux-amd64-v1"}
	targets := make(map[string]int, maxGitExecPathSymlinkTargets+1)
	var subdirectories []string
	for _, entry := range entries {
		switch entry.class {
		case externalDelegationRegular:
			observed.RegularFiles++
			countExternalGitHelper(&observed, entry.helper)
		case externalDelegationSymlink:
			observed.Symlinks++
			if err := admitExternalGitExecPathSymlink(entry, named); err != nil {
				return manifest, err
			}
			targets[entry.target]++
		case externalDelegationDirectory:
			subdirectories = append(subdirectories, entry.name)
		default:
			return manifest, errors.New("external Git exec-path entry is not an admitted class")
		}
	}
	observed.Directories, observed.SymlinkTargets = len(subdirectories), len(targets)
	if observed.Directories > maxGitExecPathSubdirectories || observed.SymlinkTargets > maxGitExecPathSymlinkTargets {
		return manifest, errors.New("external Git exec-path exceeds its directory or symlink-target bound")
	}
	if err := admitExternalGitCoreEntry(ctx, directory, digest, named, targets); err != nil {
		return manifest, err
	}
	observed.CoreImageSymlinks = targets["git"]
	canonical := externalDelegationCanonical(entries)
	for _, name := range subdirectories {
		childCanonical, err := censusExternalGitExecPathSubdirectory(ctx, root, directory, name, &observed)
		if err != nil {
			return manifest, err
		}
		canonical += externalDelegationSubdirectoryHeader(name) + childCanonical
	}
	if observed.NativeHelpers+observed.ScriptHelpers+observed.TextHelpers != observed.RegularFiles+observed.SubdirectoryEntries {
		return manifest, errors.New("external Git exec-path census is not internally coherent")
	}
	observed.ManifestSHA256 = externalDelegationDigest(canonical)
	manifest = observed
	return manifest, nil
}

// admitExternalGitCoreEntry requires the exec-path to hold the selected core
// image under its canonical name, byte-equal, with delegation pointing at it.
func admitExternalGitCoreEntry(ctx context.Context, directory, digest string, named map[string]externalDelegationEntry, targets map[string]int) error {
	core, ok := named["git"]
	if !ok || core.class != externalDelegationRegular || core.helper != externalDelegationNative || targets["git"] == 0 {
		return errors.New("external Git exec-path does not delegate to a native core helper")
	}
	observed, err := t4013.DigestHostExecutable(ctx, filepath.Join(directory, "git"))
	if err != nil || observed != digest {
		return errors.New("external Git exec-path core helper differs from the selected image")
	}
	return nil
}

// censusExternalGitExecPathSubdirectory admits one exec-path subdirectory as a
// flat collection of regular helper files, accumulating its rows into the shared
// helper counters. Symlinks, nested directories and empty or irregular entries
// refuse, matching the measured mergetools shape of 24 sourced regular text files
// with no executable bit.
func censusExternalGitExecPathSubdirectory(ctx context.Context, root *os.Root, directory, name string, manifest *ExecutionGitExecPathManifest) (string, error) {
	entries, err := censusExternalDelegationDirectory(ctx, root, filepath.Join(directory, name), name, maxGitExecPathSubdirectoryEntries)
	if err != nil {
		return "", err
	}
	var total int64
	for _, entry := range entries {
		if ctx.Err() != nil || entry.class != externalDelegationRegular || entry.size <= 0 {
			return "", errors.New("external Git exec-path subdirectory holds an unadmitted entry")
		}
		total += entry.size
		countExternalGitHelper(manifest, entry.helper)
	}
	if len(entries) == 0 || total > maxGitExecPathSubdirectoryBytes {
		return "", errors.New("external Git exec-path subdirectory is empty or exceeds its byte bound")
	}
	manifest.SubdirectoryEntries += len(entries)
	manifest.SubdirectoryBytes += total
	return externalDelegationCanonical(entries), nil
}

func countExternalGitHelper(manifest *ExecutionGitExecPathManifest, helper string) {
	switch helper {
	case externalDelegationNative:
		manifest.NativeHelpers++
	case externalDelegationScript:
		manifest.ScriptHelpers++
	case externalDelegationText:
		manifest.TextHelpers++
	}
}

// admitExternalGitExecPathSymlink requires every symlink to name a bare sibling
// that is itself an admitted regular helper in the same census. A link that
// escapes the directory, points at a directory, chains to another link or names
// itself refuses, so the manifest can never describe an unresolved delegation.
func admitExternalGitExecPathSymlink(entry externalDelegationEntry, named map[string]externalDelegationEntry) error {
	if entry.target == "." || entry.target == ".." || entry.target == entry.name || filepath.Base(entry.target) != entry.target {
		return errors.New("external Git exec-path symlink does not name a bare sibling helper")
	}
	referenced, ok := named[entry.target]
	if !ok || referenced.class != externalDelegationRegular {
		return errors.New("external Git exec-path symlink does not resolve to a regular sibling helper")
	}
	return nil
}
