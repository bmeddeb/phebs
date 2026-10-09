//go:build linux

package t421

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/bmeddeb/phebs/spike/t4013"
)

// Shared external-delegation bounds. These cap the shape of one delegation
// location reported by an already admitted host image, not the host: a census is
// at most two directory levels deep, every helper contributes one Lstat plus at
// most a 64-byte header read, and only explicitly selected images are hashed in
// full. A location that exceeds any bound refuses instead of being truncated.
const (
	maxExternalDelegationHelperBytes  = 64 << 20
	maxExternalDelegationSymlinkBytes = 256
	maxExternalDelegationHeaderBytes  = 64
)

// Admitted structural classes. The vocabulary is closed: a census row that is
// none of these refuses rather than being recorded as an unknown shape.
const (
	externalDelegationRegular   = "regular"
	externalDelegationSymlink   = "symlink"
	externalDelegationDirectory = "directory"
	externalDelegationNative    = "native"
	externalDelegationScript    = "script"
	externalDelegationText      = "text"
)

// admitExternalDelegationImage repeats the observer's own admission for one
// explicitly selected direct image: absolute, clean, unpadded, resolved, bounded
// and a native ELF64 executable. It duplicates rather than shares that sequence
// so the admitted ObserveExecutionExternalTool hot path stays byte-identical, and
// it performs no PATH discovery.
func admitExternalDelegationImage(ctx context.Context, binary string) (resolved, digest string, err error) {
	if len(binary) == 0 || len(binary) > maxInputCustodyPathBytes || !filepath.IsAbs(binary) ||
		filepath.Clean(binary) != binary || strings.TrimSpace(binary) != binary || strings.ContainsAny(binary, "\x00\r\n") {
		return "", "", errors.New("external delegation image requires an explicit absolute path")
	}
	resolved, err = filepath.EvalSymlinks(binary)
	if err != nil || len(resolved) > maxInputCustodyPathBytes {
		return "", "", errors.New("external delegation image cannot be resolved")
	}
	digest, err = t4013.DigestHostExecutable(ctx, resolved)
	if err != nil || validateExternalToolNativeImage(resolved) != nil {
		return "", "", errors.New("external delegation image is not a bounded native executable")
	}
	return resolved, digest, nil
}

// runExternalDelegationProbe executes one closed-environment probe of an already
// admitted image and re-hashes that image on both sides, so a substituted image
// cannot supply the observed delegation location. The probe environment is the
// observer's: no ambient PATH, no GIT_EXEC_PATH, no GOROOT and no GOENV, with
// every temporary location pinned inside the private workspace. Scratch
// retention follows the observer exactly — a volume-owned preparation keeps even
// failed probe scratch until its owner's non-forced detach, and ordinary
// observation removes it.
func runExternalDelegationProbe(ctx context.Context, binary, digest string, arguments ...string) (output string, retErr error) {
	if ctx.Err() != nil {
		return "", errors.New("external delegation probe canceled")
	}
	verify := func(when string) error {
		observed, err := t4013.DigestHostExecutable(ctx, binary)
		if err != nil || observed != digest {
			return errors.New("external delegation image changed " + when + " its probe")
		}
		return nil
	}
	preparationParent := executionPreparationParent(ctx)
	workspace, err := os.MkdirTemp(preparationParent, "phebs-t422-delegation-")
	if err != nil {
		return "", errors.New("external delegation probe cannot create private directory")
	}
	defer func() {
		// A volume-owned preparation retains even failed probe scratch until the
		// owner's non-forced detach. Ordinary observation stays unchanged.
		if preparationParent == "" && os.RemoveAll(workspace) != nil {
			retErr = errors.Join(retErr, errors.New("external delegation probe cleanup failed"))
		}
		// Match the observer's own convention: a refusal returns explicit zeros, so
		// no partial probe output can reach a caller beside a non-nil error.
		if retErr != nil {
			output = ""
		}
	}()
	resolved, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return "", errors.New("external delegation probe cannot resolve private directory")
	}
	workspace = resolved
	if err := verify("before"); err != nil {
		return "", err
	}
	output, err = runExternalToolProbe(ctx, workspace, binary, externalToolEnvironment(workspace), arguments...)
	if err != nil {
		return "", err
	}
	if err := verify("after"); err != nil {
		return "", err
	}
	return output, nil
}

// admitExternalDelegationDirectory accepts only a fully resolved absolute real
// directory that an image reported for itself. Probe output here is a location
// rather than a version, so validPublicToolVersion deliberately does not apply.
func admitExternalDelegationDirectory(output string) (string, error) {
	if len(output) == 0 || len(output) > maxInputCustodyPathBytes || !filepath.IsAbs(output) ||
		filepath.Clean(output) != output || strings.TrimSpace(output) != output || strings.ContainsAny(output, "\x00\r\n") {
		return "", errors.New("external delegation directory is invalid")
	}
	resolved, err := filepath.EvalSymlinks(output)
	if err != nil || resolved != output {
		return "", errors.New("external delegation directory is not fully resolved")
	}
	info, err := os.Lstat(output)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("external delegation directory is not a real directory")
	}
	return output, nil
}

type externalDelegationRow struct {
	name   string
	info   fs.FileInfo
	target string
}

type externalDelegationEntry struct {
	name       string
	class      string
	helper     string
	size       int64
	perm       string
	target     string
	executable bool
}

// readExternalDelegationRows performs one bounded, refuse-don't-truncate census
// of a single directory inside an already opened root. The directory identity is
// snapshotted before the first read and re-checked after the last, so a
// concurrent replacement or resize cannot supply the observed rows. Directory
// order is a kernel convention rather than a sorted sequence, so rows are
// ordered explicitly here; every downstream digest depends on that ordering.
func readExternalDelegationRows(ctx context.Context, root *os.Root, path string, limit int) ([]externalDelegationRow, error) {
	if limit <= 0 || ctx.Err() != nil {
		return nil, errors.New("external delegation census is unavailable")
	}
	baseline, err := root.Lstat(path)
	if err != nil || !baseline.IsDir() || baseline.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("external delegation directory is not a real directory")
	}
	directory, err := root.Open(path)
	if err != nil {
		return nil, errors.New("external delegation directory cannot be opened")
	}
	rows := make([]externalDelegationRow, 0, 64)
	readErr := func() error {
		opened, statErr := directory.Stat()
		if statErr != nil || !inputCustodySame(baseline, opened) {
			return errors.New("external delegation directory changed before its census")
		}
		for {
			batch, batchErr := directory.ReadDir(128)
			if ctx.Err() != nil || (batchErr != nil && !errors.Is(batchErr, io.EOF)) {
				return errors.New("external delegation directory cannot be read")
			}
			for _, item := range batch {
				name := item.Name()
				if len(name) == 0 || name == "." || name == ".." || len(name) > maxInputCustodyPathBytes ||
					strings.ContainsAny(name, "\x00\r\n") {
					return errors.New("external delegation entry name is invalid")
				}
				info, lstatErr := root.Lstat(filepath.Join(path, name))
				if ctx.Err() != nil || lstatErr != nil {
					return errors.New("external delegation entry cannot be inspected")
				}
				target := ""
				if info.Mode()&os.ModeSymlink != 0 {
					link, linkErr := root.Readlink(filepath.Join(path, name))
					if linkErr != nil || len(link) == 0 || len(link) > maxExternalDelegationSymlinkBytes {
						return errors.New("external delegation symlink target is unavailable or unbounded")
					}
					target = link
				}
				rows = append(rows, externalDelegationRow{name: name, info: info, target: target})
			}
			if len(rows) > limit {
				return errors.New("external delegation directory exceeds its entry bound")
			}
			if errors.Is(batchErr, io.EOF) {
				break
			}
		}
		after, statErr := directory.Stat()
		current, pathErr := root.Lstat(path)
		if statErr != nil || pathErr != nil || !inputCustodySame(baseline, after) || !inputCustodySame(after, current) {
			return errors.New("external delegation directory changed during its census")
		}
		return nil
	}()
	closeErr := directory.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, errors.New("external delegation directory cannot be closed")
	}
	slices.SortFunc(rows, func(left, right externalDelegationRow) int { return strings.Compare(left.name, right.name) })
	return rows, nil
}

// classifyExternalDelegationRow turns one raw census row into an admitted
// structural class. The admitted vocabulary is regular, symlink and directory, so
// a socket, FIFO, device or other typed entry falls through to a refusal, and any
// setuid or setgid bit refuses outright: a delegation location carrying either is
// not the shape these recipes describe, and recording it would weaken the
// resulting manifest. Every refusal returns an explicit zero entry rather than the
// partially classified row, so a caller cannot carry a refused entry's name or
// symlink target past the error that rejected it.
func classifyExternalDelegationRow(ctx context.Context, directory string, row externalDelegationRow) (externalDelegationEntry, error) {
	info, mode := row.info, row.info.Mode()
	entry := externalDelegationEntry{name: row.name, target: row.target}
	if ctx.Err() != nil || mode&(os.ModeSetuid|os.ModeSetgid) != 0 {
		return externalDelegationEntry{}, errors.New("external delegation entry is not an admitted file class")
	}
	switch {
	case mode&os.ModeSymlink != 0:
		// A symlink's size and permission bits are kernel conventions rather than
		// delegated structure, so a manifest records only its target.
		entry.class = externalDelegationSymlink
	case mode.IsDir():
		entry.class, entry.perm = externalDelegationDirectory, mode.Perm().String()
	case mode.IsRegular():
		if info.Size() == 0 || info.Size() > maxExternalDelegationHelperBytes {
			return externalDelegationEntry{}, errors.New("external delegation helper is empty or exceeds its byte bound")
		}
		helper, err := externalDelegationHelperClass(filepath.Join(directory, row.name), info)
		if err != nil {
			return externalDelegationEntry{}, err
		}
		entry.class, entry.helper = externalDelegationRegular, helper
		entry.size, entry.perm = info.Size(), mode.Perm().String()
		entry.executable = mode.Perm()&0o111 != 0
	default:
		return externalDelegationEntry{}, errors.New("external delegation entry is not an admitted file class")
	}
	return entry, nil
}

// externalDelegationHelperClass reads at most 64 header bytes of one regular
// helper through the existing non-following host-image open, separating a native
// ELF image from an interpreted script and from sourced shell text. This is a
// shape observation: it never executes, loads, vendors-validates or reads the
// helper body.
func externalDelegationHelperClass(path string, info fs.FileInfo) (string, error) {
	file, err := t4013.OpenHostImage(path)
	if err != nil {
		return "", errors.New("external delegation helper cannot be opened")
	}
	header := make([]byte, maxExternalDelegationHeaderBytes)
	class := ""
	readErr := func() error {
		opened, statErr := file.Stat()
		if statErr != nil || !inputCustodySame(info, opened) {
			return errors.New("external delegation helper changed before classification")
		}
		read, copyErr := io.ReadFull(file, header)
		if copyErr != nil && !errors.Is(copyErr, io.EOF) && !errors.Is(copyErr, io.ErrUnexpectedEOF) {
			return errors.New("external delegation helper cannot be read")
		}
		if read == 0 {
			return errors.New("external delegation helper is empty")
		}
		header = header[:read]
		switch {
		case bytes.HasPrefix(header, []byte("\x7fELF")):
			class = externalDelegationNative
		case bytes.HasPrefix(header, []byte("#!")):
			class = externalDelegationScript
		default:
			class = externalDelegationText
		}
		return nil
	}()
	after, statErr := file.Stat()
	current, pathErr := os.Lstat(path)
	closeErr := file.Close()
	if readErr != nil {
		return "", readErr
	}
	if closeErr != nil {
		return "", errors.New("external delegation helper cannot be closed")
	}
	if statErr != nil || pathErr != nil || !inputCustodySame(info, after) || !inputCustodySame(info, current) {
		return "", errors.New("external delegation helper changed during classification")
	}
	return class, nil
}

// censusExternalDelegationDirectory reads and classifies one directory level
// inside an already opened root, using an absolute directory only to reach each
// entry's header screen.
func censusExternalDelegationDirectory(ctx context.Context, root *os.Root, directory, path string, limit int) ([]externalDelegationEntry, error) {
	rows, err := readExternalDelegationRows(ctx, root, path, limit)
	if err != nil {
		return nil, err
	}
	entries := make([]externalDelegationEntry, 0, len(rows))
	for _, row := range rows {
		entry, err := classifyExternalDelegationRow(ctx, directory, row)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// censusExternalDelegationRoot opens one directory as its own census root, reads
// its single level and closes it with a checked error.
func censusExternalDelegationRoot(ctx context.Context, directory string, limit int) ([]externalDelegationEntry, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, errors.New("external delegation directory cannot be opened as a census root")
	}
	entries, censusErr := censusExternalDelegationDirectory(ctx, root, directory, ".", limit)
	closeErr := root.Close()
	if censusErr != nil {
		return nil, censusErr
	}
	if closeErr != nil {
		return nil, errors.New("external delegation directory cannot be closed")
	}
	return entries, nil
}

// externalDelegationNamed indexes censused entries by name and refuses a
// duplicated name rather than silently keeping one of the two rows.
func externalDelegationNamed(entries []externalDelegationEntry) (map[string]externalDelegationEntry, error) {
	named := make(map[string]externalDelegationEntry, len(entries))
	for _, entry := range entries {
		if _, duplicate := named[entry.name]; duplicate {
			return nil, errors.New("external delegation entry is duplicated")
		}
		named[entry.name] = entry
	}
	return named, nil
}

// externalDelegationCanonical encodes already sorted census rows in a
// length-prefixed form, so a manifest digest is injective and reproducible
// without carrying a single entry name out of the observation.
func externalDelegationCanonical(entries []externalDelegationEntry) string {
	var builder strings.Builder
	for _, entry := range entries {
		builder.WriteString(entry.class)
		builder.WriteByte(' ')
		for _, field := range []string{entry.name, entry.helper, strconv.FormatInt(entry.size, 10), entry.perm, entry.target} {
			builder.WriteString(strconv.Itoa(len(field)))
			builder.WriteByte(':')
			builder.WriteString(field)
			builder.WriteByte(';')
		}
		builder.WriteByte('\n')
	}
	return builder.String()
}

func externalDelegationSubdirectoryHeader(name string) string {
	return "subdirectory " + strconv.Itoa(len(name)) + ":" + name + ";\n"
}

func externalDelegationDigest(canonical string) string {
	sum := sha256.Sum256([]byte(canonical))
	return "sha256:" + hex.EncodeToString(sum[:])
}
