package packrelease

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// The census is the machine-derived source of both bindings VerifyForLoad
// requires from the loading side, so these tests pin what the census admits,
// what it refuses, and that its root digest describes the bytes present rather
// than the order a host happened to store them in.

var _ ArtifactResolver = (*ArtifactDirectory)(nil)

// writeArtifact writes one artifact whose content is derived from its
// identifier, so an expected digest is testDigest(name+"\n").
func writeArtifact(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0o600); err != nil {
		t.Fatalf("write artifact %s: %v", name, err)
	}
}

// writeSparseArtifact creates a file whose apparent size is n bytes without
// writing them. The census judges size from directory metadata and refuses
// before reading, so a bound test needs the size and not the content.
func writeSparseArtifact(t *testing.T, dir, name string, n int64) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("create sparse artifact %s: %v", name, err)
	}
	if err := os.Truncate(path, n); err != nil {
		t.Fatalf("truncate sparse artifact %s: %v", name, err)
	}
}

func TestOpenArtifactDirectoryCensusesPresentBytes(t *testing.T) {
	const pack = "phebs.grpc.caller.go"
	names := []string{"card." + pack, "manifest." + pack, "validation." + pack}

	dir := t.TempDir()
	for _, name := range names {
		writeArtifact(t, dir, name)
	}

	opened, err := OpenArtifactDirectory(dir)
	if err != nil {
		t.Fatalf("OpenArtifactDirectory() error = %v", err)
	}
	if got := opened.Count(); got != len(names) {
		t.Fatalf("Count() = %d, want %d", got, len(names))
	}
	if !digestRE.MatchString(opened.RootDigest()) {
		t.Fatalf("RootDigest() = %q, want a sha256 digest", opened.RootDigest())
	}
	for _, name := range names {
		digest, found, err := opened.Resolve(context.Background(), name)
		if err != nil {
			t.Fatalf("Resolve(%s) error = %v", name, err)
		}
		if !found {
			t.Fatalf("Resolve(%s) found = false, want the censused artifact", name)
		}
		if want := testDigest(name + "\n"); digest != want {
			t.Fatalf("Resolve(%s) = %q, want %q", name, digest, want)
		}
	}

	digest, found, err := opened.Resolve(context.Background(), "card.phebs.absent")
	if err != nil || found || digest != "" {
		t.Fatalf("Resolve(absent) = (%q, %t, %v), want no digest and no error", digest, found, err)
	}
}

func TestOpenArtifactDirectoryRootDigestDescribesContentNotOrder(t *testing.T) {
	names := []string{"card.b", "card.a", "validation.a", "manifest.a", "card.c"}

	forward := t.TempDir()
	for _, name := range names {
		writeArtifact(t, forward, name)
	}
	reversed := t.TempDir()
	for i := len(names) - 1; i >= 0; i-- {
		writeArtifact(t, reversed, names[i])
	}

	first, err := OpenArtifactDirectory(forward)
	if err != nil {
		t.Fatalf("OpenArtifactDirectory(forward) error = %v", err)
	}
	second, err := OpenArtifactDirectory(reversed)
	if err != nil {
		t.Fatalf("OpenArtifactDirectory(reversed) error = %v", err)
	}
	if first.RootDigest() != second.RootDigest() {
		t.Fatalf("root digest depends on write order: %q != %q", first.RootDigest(), second.RootDigest())
	}

	// One changed byte is a different artifact set, so it must be a different
	// root. This is what stops a signed record from being replayed against a
	// directory whose contents drifted after the record was validated.
	if err := os.WriteFile(filepath.Join(forward, "card.a"), []byte("drifted\n"), 0o600); err != nil {
		t.Fatalf("rewrite card.a: %v", err)
	}
	drifted, err := OpenArtifactDirectory(forward)
	if err != nil {
		t.Fatalf("OpenArtifactDirectory(drifted) error = %v", err)
	}
	if drifted.RootDigest() == first.RootDigest() {
		t.Fatal("root digest did not change when an artifact's bytes changed")
	}
	if drifted.Count() != first.Count() {
		t.Fatalf("Count() = %d after a rewrite, want %d", drifted.Count(), first.Count())
	}

	writeArtifact(t, forward, "card.extra")
	grown, err := OpenArtifactDirectory(forward)
	if err != nil {
		t.Fatalf("OpenArtifactDirectory(grown) error = %v", err)
	}
	if grown.RootDigest() == drifted.RootDigest() {
		t.Fatal("root digest did not change when an artifact was added")
	}
}

func TestOpenArtifactDirectoryEmptyDerivesAnEmptySetRoot(t *testing.T) {
	opened, err := OpenArtifactDirectory(t.TempDir())
	if err != nil {
		t.Fatalf("OpenArtifactDirectory() error = %v", err)
	}
	if got := opened.Count(); got != 0 {
		t.Fatalf("Count() = %d, want 0", got)
	}
	// An empty directory is a described set, not an absence: the root is the
	// digest of zero rows, so a release naming any artifact cannot match it.
	if want := testDigest(""); opened.RootDigest() != want {
		t.Fatalf("RootDigest() = %q, want the empty-row digest %q", opened.RootDigest(), want)
	}
}

func TestOpenArtifactDirectoryRefusesUnadmittedShapes(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, dir string)
		want  string
	}{
		{
			name: "directory entry",
			setup: func(t *testing.T, dir string) {
				if err := os.Mkdir(filepath.Join(dir, "nested"), 0o700); err != nil {
					t.Fatalf("mkdir nested: %v", err)
				}
			},
			want: "want a regular file",
		},
		{
			name:  "untrimmed entry name",
			setup: func(t *testing.T, dir string) { writeArtifact(t, dir, " padded ") },
			want:  `refused entry name`,
		},
		{
			name: "oversized artifact",
			setup: func(t *testing.T, dir string) {
				writeSparseArtifact(t, dir, "card.huge", MaxReleaseBytes+1)
			},
			want: "exceeds the 1048576 bound",
		},
		{
			name: "oversized total",
			setup: func(t *testing.T, dir string) {
				for i := range maxArtifactTotalBytes/maxArtifactBytes + 1 {
					writeSparseArtifact(t, dir, fmt.Sprintf("card.part%03d", i), maxArtifactBytes)
				}
			},
			want: "total-byte bound",
		},
		{
			name: "too many entries",
			setup: func(t *testing.T, dir string) {
				for i := range maxArtifactEntries + 1 {
					writeSparseArtifact(t, dir, fmt.Sprintf("card.entry%04d", i), 0)
				}
			},
			want: "exceeds the 1024 bound",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.setup(t, dir)
			_, err := OpenArtifactDirectory(dir)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("OpenArtifactDirectory() error = %v, want an error naming %q", err, tc.want)
			}
		})
	}
}

func TestOpenArtifactDirectoryRefusesAbsentDirectory(t *testing.T) {
	_, err := OpenArtifactDirectory(filepath.Join(t.TempDir(), "absent"))
	if err == nil || !strings.Contains(err.Error(), "read referenced artifacts directory") {
		t.Fatalf("OpenArtifactDirectory() error = %v, want a read refusal", err)
	}
}

func TestArtifactDirectoryResolveReadsNothingAfterTheCensus(t *testing.T) {
	dir := t.TempDir()
	writeArtifact(t, dir, "card.phebs.kafka.producer")

	opened, err := OpenArtifactDirectory(dir)
	if err != nil {
		t.Fatalf("OpenArtifactDirectory() error = %v", err)
	}
	before := opened.RootDigest()

	// The census already read every artifact, so removing one afterwards cannot
	// change what the resolver reports or cost another read. This is the pin that
	// keeps artifact work at the admitted startup boundary and out of a query.
	if err := os.Remove(filepath.Join(dir, "card.phebs.kafka.producer")); err != nil {
		t.Fatalf("remove artifact: %v", err)
	}
	digest, found, err := opened.Resolve(context.Background(), "card.phebs.kafka.producer")
	if err != nil || !found || digest != testDigest("card.phebs.kafka.producer\n") {
		t.Fatalf("Resolve() after removal = (%q, %t, %v), want the censused digest", digest, found, err)
	}
	if opened.RootDigest() != before {
		t.Fatal("RootDigest() changed after the census")
	}
}

func TestArtifactDirectoryResolveRefusesNonSegmentIdentifiers(t *testing.T) {
	dir := t.TempDir()
	writeArtifact(t, dir, "card.phebs.thrift.contract")
	opened, err := OpenArtifactDirectory(dir)
	if err != nil {
		t.Fatalf("OpenArtifactDirectory() error = %v", err)
	}

	// An identifier is exactly one path segment of the censused directory. One
	// that addresses anything else is reported absent rather than mapped through
	// an invented layout or followed outside the directory.
	const present = "card.phebs.thrift.contract"
	nonSegments := []string{
		"", ".", "..", "../escape", "card/manifest", "card\\manifest",
		" " + present, present + " ", present + "\x00",
	}
	for _, id := range nonSegments {
		digest, found, err := opened.Resolve(context.Background(), id)
		if err != nil {
			t.Fatalf("Resolve(%q) error = %v, want none: an absent artifact is not a resolver failure", id, err)
		}
		if found || digest != "" {
			t.Fatalf("Resolve(%q) = (%q, %t), want an absent artifact", id, digest, found)
		}
	}
}

func TestArtifactDirectoryZeroValueIsInert(t *testing.T) {
	var opened *ArtifactDirectory

	digest, found, err := opened.Resolve(context.Background(), "card.phebs.grpc.caller.go")
	if err != nil || found || digest != "" {
		t.Fatalf("Resolve() on a nil directory = (%q, %t, %v), want an absent artifact", digest, found, err)
	}
	if got := opened.RootDigest(); got != "" {
		t.Fatalf("RootDigest() on a nil directory = %q, want empty", got)
	}
	if got := opened.Count(); got != 0 {
		t.Fatalf("Count() on a nil directory = %d, want 0", got)
	}
}

func TestArtifactRootRefusesDelimiterCollision(t *testing.T) {
	dir := t.TempDir()
	// These bytes formerly encoded the same two rows as files a and b.
	forged := "a " + testDigest("first") + "\nb"
	if err := os.WriteFile(filepath.Join(dir, forged), []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenArtifactDirectory(dir); err == nil {
		t.Fatal("a filename containing root row delimiters must refuse")
	}
}

func TestArtifactReadBoundsUseOpenedBytes(t *testing.T) {
	for _, remaining := range []int64{maxArtifactBytes, 7} {
		t.Run(fmt.Sprint(remaining), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "growing")
			writeSparseArtifact(t, filepath.Dir(path), filepath.Base(path), 0)
			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = file.Close() }()
			before, err := file.Stat()
			if err != nil {
				t.Fatal(err)
			}
			// Growth after descriptor metadata was sampled must consume only
			// the allowed bytes plus one overflow sentinel.
			if err := os.Truncate(path, 2*maxArtifactBytes); err != nil {
				t.Fatal(err)
			}
			_, read, err := hashOpenedArtifact(file, before, remaining)
			if err == nil || read != remaining+1 {
				t.Fatalf("read = %d, error = %v; want %d bytes and refusal", read, err, remaining+1)
			}
		})
	}
}

func TestArtifactOpenRefusesFIFOAndSymlink(t *testing.T) {
	for _, kind := range []string{"fifo", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "card")
			var err error
			if kind == "fifo" {
				err = unix.Mkfifo(path, 0o600)
			} else {
				outside := t.TempDir()
				writeArtifact(t, outside, "target")
				err = os.Symlink(filepath.Join(outside, "target"), path)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := OpenArtifactDirectory(dir); err == nil {
				t.Fatal("a FIFO or symlink must refuse without being read")
			}
		})
	}
}

func TestArtifactRefusesSameSizeRewriteWithRestoredMtime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifact")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	before, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := hashOpenedArtifact(file, before, maxArtifactTotalBytes); err == nil {
		t.Fatal("a same-size rewrite with restored mtime must still refuse")
	}
}
