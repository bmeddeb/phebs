//go:build linux

package t421

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
)

// Go SDK location bounds. The measured GOROOT on this host holds 16 root entries
// — 8 directories and 8 regular files — a bin directory of exactly go and gofmt,
// and a pkg/tool/linux_amd64 directory of 8 native tools, while the whole SDK
// holds 15,026 files across 1,667 directories. These bounds therefore admit the
// real root, bin and tool shapes with headroom and never walk the SDK: the recipe
// reads a fixed marker set, one bounded level of three directories and at most
// 4,096 bytes of the VERSION marker. The reported location pair is bounded twice
// over — each side by maxInputCustodyPathBytes and the pair by the probe's
// existing 4 KiB output ceiling.
const (
	maxGoSDKRootEntries  = 64
	maxGoSDKBinEntries   = 8
	maxGoSDKToolEntries  = 64
	maxGoSDKVersionBytes = 4096
)

// ExecutionGoSDKLocation is a path-free structural description of the SDK that
// one explicitly selected Go image reports as its own GOROOT. It records the
// image digest, the toolchain release the image was required to report, bounded
// root and bin census shapes, a digest over the canonical sorted encoding of the
// root census, the tool-directory entry count and a digest over that directory's
// canonical encoding. It carries no absolute host path, no SDK entry name and no
// SDK file content beyond the fixed VERSION marker. It proves the SDK location
// was observed, structurally bound to the admitted image and bounded; it does not
// prove SDK immutability, a vendor signature, the absence of runtime delegation
// outside these markers, or any launch right.
type ExecutionGoSDKLocation struct {
	Role                 string
	ImageSHA256          string
	Version              string
	RootEntries          int
	RootDirectories      int
	RootSHA256           string
	BinEntries           int
	ToolDirectoryEntries int
	ToolDirectorySHA256  string
	Provenance           string
}

// ObserveExecutionGoSDKLocation measures the GOROOT and GOTOOLDIR that one
// explicitly selected Go image reports for itself, under a closed probe
// environment with no ambient GOROOT, GOENV, GOWORK or PATH and with
// GOTOOLCHAIN=local so the image cannot substitute a downloaded toolchain. The
// location is then bound structurally rather than nominally: GOROOT must be a
// fully resolved real directory, its bin/go must resolve to the admitted image
// and hash byte-equal to it, GOTOOLDIR must be exactly
// GOROOT/pkg/tool/<goos>_<goarch>, the VERSION marker's first line and the
// image's own go version must both equal the verifier toolchain release, and a
// fixed marker set must be present with the admitted classes. No SDK-wide walk or
// tree digest is performed, so a 15,026-file SDK never becomes a host scan. This
// issues no CheckoutAdmissionBinding, no launcher authority, no session teardown
// and no dispatch admission, and validateExecutionHost remains the separate
// freeze-platform fence.
func ObserveExecutionGoSDKLocation(ctx context.Context, binary string) (location ExecutionGoSDKLocation, retErr error) {
	if ctx == nil || runtime.GOARCH != "amd64" {
		return location, errors.New("external Go SDK location requires a context and the frozen Linux platform")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if ctx.Err() != nil {
		return location, errors.New("external Go SDK observation canceled")
	}
	resolved, digest, err := admitExternalDelegationImage(ctx, binary)
	if err != nil {
		return location, err
	}
	reported, err := runExternalDelegationProbe(ctx, resolved, digest, "env", "GOROOT", "GOTOOLDIR")
	if err != nil {
		return location, err
	}
	goroot, tooldir, err := admitExternalGoSDKDirectories(reported, resolved)
	if err != nil {
		return location, err
	}
	version, err := runExternalDelegationProbe(ctx, resolved, digest, "version")
	if err != nil {
		return location, err
	}
	if version != "go version "+runtime.Version()+" "+runtime.GOOS+"/"+runtime.GOARCH {
		return location, errors.New("external Go version differs from the verifier toolchain")
	}
	location, err = censusExternalGoSDKLocation(ctx, goroot, tooldir, digest)
	if err != nil {
		return ExecutionGoSDKLocation{}, err
	}
	if observed, hashErr := t4013.DigestHostExecutable(ctx, resolved); hashErr != nil || observed != digest || ctx.Err() != nil {
		return ExecutionGoSDKLocation{}, errors.New("external Go image changed or SDK observation expired")
	}
	return location, nil
}

// admitExternalGoSDKDirectories admits the two locations one image reports and
// binds them to that image. GOROOT must be a fully resolved real directory whose
// own bin/go resolves back to the admitted image, and GOTOOLDIR must be exactly
// the platform tool directory inside that GOROOT, so neither value can name an
// unrelated tree. Probe output here is a location rather than a version, so
// validPublicToolVersion deliberately does not apply.
func admitExternalGoSDKDirectories(output, resolved string) (goroot, tooldir string, err error) {
	fields := strings.Split(output, "\n")
	if len(fields) != 2 {
		return "", "", errors.New("external Go SDK environment did not report exactly two locations")
	}
	goroot, err = admitExternalDelegationDirectory(fields[0])
	if err != nil {
		return "", "", err
	}
	tooldir, err = admitExternalDelegationDirectory(fields[1])
	if err != nil {
		return "", "", err
	}
	if tooldir != filepath.Join(goroot, "pkg", "tool", runtime.GOOS+"_"+runtime.GOARCH) {
		return "", "", errors.New("external Go tool directory is not the platform directory inside its GOROOT")
	}
	bound, bindErr := filepath.EvalSymlinks(filepath.Join(goroot, "bin", "go"))
	if bindErr != nil || bound != resolved {
		return "", "", errors.New("external Go GOROOT does not hold the admitted image as its own bin/go")
	}
	return goroot, tooldir, nil
}

// censusExternalGoSDKLocation observes the fixed marker set and one bounded level
// of the root, bin and tool directories, then binds the SDK's own bin/go to the
// admitted digest. The recorded release is the one the VERSION marker was just
// required to name, so the value never reaches a caller through a path that
// skipped that check. Every intermediate accumulates in a local and reaches the
// caller only through the single success return, so a refusal yields the zero
// location rather than a partial description of an unadmitted SDK.
//
// The bin census and its admission run before that bin/go digest, so the row
// describing bin/go is bounded by maxExternalDelegationHelperBytes before any
// body of it is read. That is the same precedence the Git recipe keeps between
// its census and its core-image digest, and it means an oversized or otherwise
// unadmitted bin entry is refused on its metadata alone.
func censusExternalGoSDKLocation(ctx context.Context, goroot, tooldir, digest string) (location ExecutionGoSDKLocation, err error) {
	if err := admitExternalGoSDKVersionMarker(filepath.Join(goroot, "VERSION")); err != nil {
		return location, err
	}
	rootRows, err := censusExternalDelegationRoot(ctx, goroot, maxGoSDKRootEntries)
	if err != nil {
		return location, err
	}
	rootEntries, rootDirectories, rootDigest, err := admitExternalGoSDKRoot(rootRows)
	if err != nil {
		return location, err
	}
	binRows, err := censusExternalDelegationRoot(ctx, filepath.Join(goroot, "bin"), maxGoSDKBinEntries)
	if err != nil {
		return location, err
	}
	binEntries, err := admitExternalGoSDKBin(binRows)
	if err != nil {
		return location, err
	}
	if observed, digestErr := t4013.DigestHostExecutable(ctx, filepath.Join(goroot, "bin", "go")); digestErr != nil || observed != digest {
		return location, errors.New("external Go GOROOT bin/go differs from the admitted image")
	}
	toolRows, err := censusExternalDelegationRoot(ctx, tooldir, maxGoSDKToolEntries)
	if err != nil {
		return location, err
	}
	toolEntries, toolDigest, err := admitExternalGoSDKTools(toolRows)
	if err != nil {
		return location, err
	}
	for _, marker := range []string{"src/go", "src/runtime"} {
		if _, markerErr := admitExternalDelegationDirectory(filepath.Join(goroot, marker)); markerErr != nil {
			return location, errors.New("external Go SDK source marker is not a real resolved directory")
		}
	}
	return ExecutionGoSDKLocation{Role: "go", ImageSHA256: digest, Version: runtime.Version(),
		RootEntries: rootEntries, RootDirectories: rootDirectories, RootSHA256: rootDigest,
		BinEntries: binEntries, ToolDirectoryEntries: toolEntries, ToolDirectorySHA256: toolDigest,
		Provenance: "external-go-sdk-location-linux-amd64-v1"}, nil
}

// admitExternalGoSDKRoot admits the GOROOT root census as regular files and real
// directories only, requires the marker set the Go distribution actually ships,
// and digests the whole canonical root shape. A symlinked, irregular or reduced
// GOROOT root refuses instead of being recorded as a smaller SDK.
func admitExternalGoSDKRoot(entries []externalDelegationEntry) (rootEntries, rootDirectories int, digest string, err error) {
	named, err := externalDelegationNamed(entries)
	if err != nil {
		return 0, 0, "", errors.New("external Go SDK root entry is duplicated")
	}
	directories := 0
	for _, entry := range entries {
		switch entry.class {
		case externalDelegationDirectory:
			directories++
		case externalDelegationRegular:
		default:
			return 0, 0, "", errors.New("external Go SDK root holds an unadmitted entry class")
		}
	}
	for _, marker := range []string{"api", "bin", "lib", "pkg", "src"} {
		if entry, ok := named[marker]; !ok || entry.class != externalDelegationDirectory {
			return 0, 0, "", errors.New("external Go SDK root is missing an admitted directory marker")
		}
	}
	for _, marker := range []string{"VERSION", "go.env"} {
		if entry, ok := named[marker]; !ok || entry.class != externalDelegationRegular || entry.size <= 0 {
			return 0, 0, "", errors.New("external Go SDK root is missing an admitted file marker")
		}
	}
	return len(entries), directories, externalDelegationDigest(externalDelegationCanonical(entries)), nil
}

// admitExternalGoSDKBin requires exactly the two native executables the Go
// distribution ships in bin, so an extra or substituted launcher refuses.
func admitExternalGoSDKBin(entries []externalDelegationEntry) (int, error) {
	named, err := externalDelegationNamed(entries)
	if err != nil {
		return 0, errors.New("external Go SDK bin entry is duplicated")
	}
	if len(named) != 2 {
		return 0, errors.New("external Go SDK bin does not hold exactly its two admitted tools")
	}
	for _, marker := range []string{"go", "gofmt"} {
		entry, ok := named[marker]
		if !ok || entry.class != externalDelegationRegular || entry.helper != externalDelegationNative || !entry.executable {
			return 0, errors.New("external Go SDK bin is missing an admitted native tool")
		}
	}
	return len(entries), nil
}

// admitExternalGoSDKTools admits the platform tool directory as a non-empty flat
// collection of native executables and digests its canonical shape without
// reading a single tool body.
func admitExternalGoSDKTools(entries []externalDelegationEntry) (int, string, error) {
	if _, err := externalDelegationNamed(entries); err != nil {
		return 0, "", errors.New("external Go SDK tool entry is duplicated")
	}
	if len(entries) == 0 {
		return 0, "", errors.New("external Go SDK tool directory is empty")
	}
	for _, entry := range entries {
		if entry.class != externalDelegationRegular || entry.helper != externalDelegationNative ||
			!entry.executable || entry.size <= 0 {
			return 0, "", errors.New("external Go SDK tool directory holds an unadmitted entry")
		}
	}
	return len(entries), externalDelegationDigest(externalDelegationCanonical(entries)), nil
}

// admitExternalGoSDKVersionMarker reads at most 4,096 bytes of the SDK's own
// VERSION marker and requires its first line to name the verifier toolchain
// release. This is the one bounded content read the recipe performs inside the
// SDK, and it never walks the tree around it.
func admitExternalGoSDKVersionMarker(path string) error {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 ||
		before.Size() == 0 || before.Size() > maxGoSDKVersionBytes {
		return errors.New("external Go SDK version marker is not a bounded regular file")
	}
	file, err := t4013.OpenHostImage(path)
	if err != nil {
		return errors.New("external Go SDK version marker cannot be opened")
	}
	content := make([]byte, before.Size())
	readErr := func() error {
		opened, statErr := file.Stat()
		if statErr != nil || !inputCustodySame(before, opened) {
			return errors.New("external Go SDK version marker changed before its read")
		}
		if _, copyErr := io.ReadFull(file, content); copyErr != nil {
			return errors.New("external Go SDK version marker cannot be read")
		}
		release, _, _ := strings.Cut(strings.TrimSuffix(string(content), "\n"), "\n")
		if release != runtime.Version() {
			return errors.New("external Go SDK version marker does not name the verifier toolchain")
		}
		return nil
	}()
	after, statErr := file.Stat()
	current, pathErr := os.Lstat(path)
	closeErr := file.Close()
	if readErr != nil {
		return readErr
	}
	if closeErr != nil {
		return errors.New("external Go SDK version marker cannot be closed")
	}
	if statErr != nil || pathErr != nil || !inputCustodySame(before, after) || !inputCustodySame(before, current) {
		return errors.New("external Go SDK version marker changed during its read")
	}
	return nil
}
