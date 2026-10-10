package packrelease

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// Artifact-directory bounds. They keep the one startup census bounded by
// construction rather than by a deadline, and they are defensive ceilings
// rather than capacity targets: a referenced artifact is a card, manifest or
// validation record, so it belongs in the same size class as a release record.
const (
	maxArtifactEntries    = 1024
	maxArtifactBytes      = MaxReleaseBytes
	maxArtifactTotalBytes = 64 << 20
)

// ArtifactDirectory binds artifact identifiers to the digests of the bytes
// actually present in one flat operator directory. It is the ArtifactResolver
// VerifyForLoad requires, and its RootDigest is the referenced-artifacts root a
// release must name, so both bindings are derived from present content rather
// than asserted by the record or by configuration.
//
// The shape is deliberately strict: one directory level, regular files only,
// and each file name is exactly one artifact identifier. An identifier that is
// not a single path segment is reported absent rather than mapped through an
// invented layout, and a namespaced identifier scheme would need its own
// resolver. The zero value resolves nothing.
type ArtifactDirectory struct {
	digests    map[string]string
	rootDigest string
}

// OpenArtifactDirectory censuses dir exactly once and derives both the
// per-artifact digests and the directory root digest from the bytes present.
// It is fail closed: an unreadable directory, more entries than the bound, a
// non-regular entry, an oversized artifact, or a directory whose total size
// exceeds the bound each refuse rather than yielding a partial description of
// an unadmitted shape.
//
// This is startup work at the admitted boundary, never per-query work: after
// the census Resolve is a map lookup and reads nothing. The cost is one
// bounded directory read plus one full read and hash per regular file, bounded by
// maxArtifactEntries files and maxArtifactTotalBytes total bytes.
func OpenArtifactDirectory(dir string) (*ArtifactDirectory, error) {
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("read referenced artifacts directory %q: %w", dir, err)
	}
	directory := os.NewFile(uintptr(fd), dir)
	defer func() { _ = directory.Close() }()
	entries, err := directory.ReadDir(maxArtifactEntries + 1)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("read referenced artifacts directory %q: %w", dir, err)
	}
	if len(entries) > maxArtifactEntries {
		return nil, fmt.Errorf("referenced artifacts directory %q exceeds the %d bound", dir, maxArtifactEntries)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !validArtifactName(entry.Name()) {
			return nil, fmt.Errorf("referenced artifacts directory %q: refused entry name %q", dir, entry.Name())
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	digests := make(map[string]string, len(names))
	rows := make([]byte, 0, len(names)*96)
	var total int64
	for _, name := range names {
		digest, size, err := hashArtifactFile(directory, name, maxArtifactTotalBytes-total)
		if err != nil {
			return nil, err
		}
		total += size
		digests[name] = digest
		rows = append(rows, name...)
		rows = append(rows, ' ')
		rows = append(rows, digest...)
		rows = append(rows, '\n')
	}

	sum := sha256.Sum256(rows)
	return &ArtifactDirectory{
		digests:    digests,
		rootDigest: "sha256:" + hex.EncodeToString(sum[:]),
	}, nil
}

// Resolve implements ArtifactResolver. It reports the digest of the artifact
// bytes censused at OpenArtifactDirectory and whether that identifier is
// present, and it performs no I/O: the census already read every artifact once.
// It never returns an error, so a resolver failure cannot be confused with an
// absent artifact.
func (d *ArtifactDirectory) Resolve(_ context.Context, artifactID string) (string, bool, error) {
	if d == nil || !validArtifactName(artifactID) {
		return "", false, nil
	}
	digest, found := d.digests[artifactID]
	return digest, found, nil
}

// RootDigest returns the digest derived from the artifacts actually present. A
// release whose referenced_artifacts_root_digest differs names another artifact
// set and is refused, which is what stops a signed record from being replayed
// against a directory it was never validated against.
func (d *ArtifactDirectory) RootDigest() string {
	if d == nil {
		return ""
	}
	return d.rootDigest
}

// Count returns how many artifacts the census admitted, for bounded diagnostics.
func (d *ArtifactDirectory) Count() int {
	if d == nil {
		return 0
	}
	return len(d.digests)
}

// validArtifactName accepts the bounded release identifier grammar restricted
// to one path segment. Spaces and newlines are excluded, so the root's row
// separators cannot occur in a name and two artifact sets cannot share rows.
func validArtifactName(name string) bool {
	return name != "." && name != ".." && idRE.MatchString(name) && !containsSeparator(name)
}

func containsSeparator(name string) bool {
	for _, char := range name {
		if char == '/' || char == '\\' {
			return true
		}
	}
	return false
}

func hashArtifactFile(directory *os.File, name string, remaining int64) (string, int64, error) {
	fd, err := unix.Openat(int(directory.Fd()), name,
		unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", 0, fmt.Errorf("open referenced artifact %q: %w", name, err)
	}
	file := os.NewFile(uintptr(fd), filepath.Join(directory.Name(), name))
	defer func() { _ = file.Close() }()
	before, err := file.Stat()
	if err != nil {
		return "", 0, fmt.Errorf("stat referenced artifact %q: %w", name, err)
	}
	digest, size, err := hashOpenedArtifact(file, before, remaining)
	if err != nil {
		return "", 0, fmt.Errorf("referenced artifact %q: %w", name, err)
	}
	// Verify that the directory entry still names the descriptor just hashed.
	// The second open cannot follow a symlink or block on a substituted FIFO.
	checkFD, err := unix.Openat(int(directory.Fd()), name,
		unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", 0, fmt.Errorf("recheck referenced artifact %q: %w", name, err)
	}
	check := os.NewFile(uintptr(checkFD), name)
	after, statErr := check.Stat()
	closeErr := check.Close()
	if statErr != nil || closeErr != nil {
		return "", 0, fmt.Errorf("recheck referenced artifact %q: %w", name, errors.Join(statErr, closeErr))
	}
	if !sameArtifact(before, after) {
		return "", 0, fmt.Errorf("referenced artifact %q changed during census", name)
	}
	return digest, size, nil
}

func hashOpenedArtifact(file *os.File, before os.FileInfo, remaining int64) (string, int64, error) {
	if !before.Mode().IsRegular() {
		return "", 0, fmt.Errorf("is %s, want a regular file", before.Mode().Type())
	}
	if before.Size() > maxArtifactBytes {
		return "", 0, fmt.Errorf("is %d bytes, exceeds the %d bound", before.Size(), maxArtifactBytes)
	}
	if before.Size() > remaining {
		return "", 0, fmt.Errorf("exceeds the %d total-byte bound", maxArtifactTotalBytes)
	}
	limit := min(int64(maxArtifactBytes), remaining)
	hasher := sha256.New()
	read, err := io.Copy(hasher, io.LimitReader(file, limit+1))
	if err != nil {
		return "", read, fmt.Errorf("read: %w", err)
	}
	if read > limit {
		return "", read, fmt.Errorf("grew beyond the %d byte read bound", limit)
	}
	after, err := file.Stat()
	if err != nil {
		return "", read, fmt.Errorf("stat after read: %w", err)
	}
	if read != before.Size() || !sameArtifact(before, after) {
		return "", read, fmt.Errorf("changed during census")
	}
	return "sha256:" + hex.EncodeToString(hasher.Sum(nil)), read, nil
}

func sameArtifact(before, after os.FileInfo) bool {
	return os.SameFile(before, after) && before.Mode() == after.Mode() &&
		before.Size() == after.Size() && before.ModTime().Equal(after.ModTime()) &&
		artifactChangeTimeEqual(before, after)
}
