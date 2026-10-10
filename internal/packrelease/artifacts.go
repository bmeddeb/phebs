package packrelease

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
// ReadDir plus one full read and hash per regular file, bounded by
// maxArtifactEntries files and maxArtifactTotalBytes total bytes.
func OpenArtifactDirectory(dir string) (*ArtifactDirectory, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read referenced artifacts directory %q: %w", dir, err)
	}
	if len(entries) > maxArtifactEntries {
		return nil, fmt.Errorf(
			"referenced artifacts directory %q holds %d entries, exceeds the %d bound",
			dir, len(entries), maxArtifactEntries,
		)
	}

	names := make([]string, 0, len(entries))
	var total int64
	for _, entry := range entries {
		name := entry.Name()
		if !validArtifactName(name) {
			return nil, fmt.Errorf("referenced artifacts directory %q: refused entry name %q", dir, name)
		}
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("stat referenced artifact %q: %w", name, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf(
				"referenced artifact %q is %s, want a regular file", name, info.Mode().Type(),
			)
		}
		if info.Size() > maxArtifactBytes {
			return nil, fmt.Errorf(
				"referenced artifact %q is %d bytes, exceeds the %d bound", name, info.Size(), maxArtifactBytes,
			)
		}
		total += info.Size()
		if total > maxArtifactTotalBytes {
			return nil, fmt.Errorf(
				"referenced artifacts directory %q exceeds the %d total-byte bound", dir, maxArtifactTotalBytes,
			)
		}
		names = append(names, name)
	}

	// Sorting is load-bearing: the root digest is derived from the row order,
	// and ReadDir returns entries in directory order rather than a sorted
	// sequence, so an unsorted census would make the digest host-dependent.
	sort.Strings(names)

	digests := make(map[string]string, len(names))
	rows := make([]byte, 0, len(names)*96)
	for _, name := range names {
		digest, err := hashArtifactFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
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

// validArtifactName reports whether name is usable as exactly one artifact
// identifier inside the flat directory. It refuses the empty name, the two
// relative markers, any name carrying a path separator or a NUL, and any name
// that is not trimmed of surrounding space, so no identifier can address
// anything but one entry of the censused directory.
func validArtifactName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	if strings.ContainsRune(name, '/') || strings.ContainsRune(name, '\\') ||
		strings.ContainsRune(name, 0) {
		return false
	}
	return strings.TrimSpace(name) == name
}

func hashArtifactFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open referenced artifact %q: %w", filepath.Base(path), err)
	}
	// Read-only, so a Close failure carries no lost write and cannot change the
	// digest already derived from the bytes read.
	defer func() { _ = file.Close() }()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", fmt.Errorf("read referenced artifact %q: %w", filepath.Base(path), err)
	}
	return "sha256:" + hex.EncodeToString(hasher.Sum(nil)), nil
}
