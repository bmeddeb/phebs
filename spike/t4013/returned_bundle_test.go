package t4013

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

type returnedArchiveTestEntry struct {
	name     string
	typeflag byte
	linkname string
	content  []byte
}

func TestExtractReturnedBundle(t *testing.T) {
	t.Parallel()
	valid := returnedArchiveTestEntries(t)
	for _, test := range []struct {
		name    string
		mutate  func([]returnedArchiveTestEntry) []returnedArchiveTestEntry
		tail    int
		wantErr string
	}{
		{
			name: "valid",
			mutate: func(entries []returnedArchiveTestEntry) []returnedArchiveTestEntry {
				return entries
			},
		},
		{
			name: "traversal",
			mutate: func(entries []returnedArchiveTestEntry) []returnedArchiveTestEntry {
				entries[1].name = "evidence/../outside"
				return entries
			},
			wantErr: "unexpected path",
		},
		{
			name: "symlink",
			mutate: func(entries []returnedArchiveTestEntry) []returnedArchiveTestEntry {
				entries[1].typeflag = tar.TypeSymlink
				entries[1].linkname = "../outside"
				return entries
			},
			wantErr: "link metadata",
		},
		{
			name: "hard link",
			mutate: func(entries []returnedArchiveTestEntry) []returnedArchiveTestEntry {
				entries[1].typeflag = tar.TypeLink
				entries[1].linkname = "evidence/plan.json"
				return entries
			},
			wantErr: "link metadata",
		},
		{
			name: "special file",
			mutate: func(entries []returnedArchiveTestEntry) []returnedArchiveTestEntry {
				entries[1].typeflag = tar.TypeChar
				return entries
			},
			wantErr: "not a regular file",
		},
		{
			name: "duplicate",
			mutate: func(entries []returnedArchiveTestEntry) []returnedArchiveTestEntry {
				return append(entries, entries[1])
			},
			wantErr: "duplicate path",
		},
		{
			name: "missing",
			mutate: func(entries []returnedArchiveTestEntry) []returnedArchiveTestEntry {
				return entries[:len(entries)-1]
			},
			wantErr: "exactly one",
		},
		{
			name: "per-file expansion",
			mutate: func(entries []returnedArchiveTestEntry) []returnedArchiveTestEntry {
				entries[1].content = bytes.Repeat([]byte{'x'}, maxReturnedSignerBytes+1)
				return entries
			},
			wantErr: "fixed size bound",
		},
		{
			name: "aggregate expansion",
			mutate: func(entries []returnedArchiveTestEntry) []returnedArchiveTestEntry {
				return entries
			},
			tail:    maxReturnedArchiveExpandedBytes,
			wantErr: "aggregate expanded-byte bound",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			entries := append([]returnedArchiveTestEntry(nil), valid...)
			packageBytes := writeReturnedArchiveTestPackage(t, test.mutate(entries), test.tail)
			packagePath := filepath.Join(t.TempDir(), "returned.tgz")
			if err := os.WriteFile(packagePath, packageBytes, 0o600); err != nil {
				t.Fatal(err)
			}
			output := t.TempDir()
			digest := sha256.Sum256(packageBytes)
			err := ExtractReturnedBundle(
				packagePath,
				output,
				"",
				"sha256:"+hex.EncodeToString(digest[:]),
			)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("error = %v, want containing %q", err, test.wantErr)
				}
				if entries, readErr := os.ReadDir(output); readErr != nil || len(entries) != 0 {
					t.Fatalf("rejected package extracted paths: entries=%v err=%v", entries, readErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range valid[1:] {
				raw, readErr := os.ReadFile(filepath.Join(output, entry.name))
				if readErr != nil {
					t.Fatal(readErr)
				}
				if !bytes.Equal(raw, entry.content) {
					t.Fatalf("%s = %q, want %q", entry.name, raw, entry.content)
				}
			}
		})
	}
}

func TestExtractReturnedBundleRejectsUnreviewedPackageIdentity(t *testing.T) {
	t.Parallel()
	packageBytes := writeReturnedArchiveTestPackage(t, returnedArchiveTestEntries(t), 0)
	root := t.TempDir()
	packagePath := filepath.Join(root, "returned.tgz")
	if err := os.WriteFile(packagePath, packageBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "output")
	if err := os.Mkdir(output, 0o700); err != nil {
		t.Fatal(err)
	}
	err := ExtractReturnedBundle(packagePath, output, "", "sha256:"+strings.Repeat("0", 64))
	if err == nil || !strings.Contains(err.Error(), "reviewed digest") {
		t.Fatalf("error = %v, want reviewed digest refusal", err)
	}
	if entries, readErr := os.ReadDir(output); readErr != nil || len(entries) != 0 {
		t.Fatalf("digest-refused package extracted paths: entries=%v err=%v", entries, readErr)
	}
}

func TestExtractReturnedBundleAcceptsSystemTar(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("tar"); err != nil {
		t.Skip("tar is unavailable")
	}
	root := t.TempDir()
	evidence := filepath.Join(root, "evidence")
	if err := os.Mkdir(evidence, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, entry := range returnedArchiveTestEntries(t)[1:] {
		if err := os.WriteFile(filepath.Join(root, entry.name), entry.content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	packagePath := filepath.Join(root, "returned.tgz")
	command := exec.Command("tar", "-C", root, "-czf", packagePath, "evidence")
	command.Env = append(os.Environ(), "COPYFILE_DISABLE=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create system archive: %v: %s", err, output)
	}
	packageBytes, err := os.ReadFile(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(packageBytes)
	reviewedDigest := "sha256:" + hex.EncodeToString(digest[:])
	if err := ExtractReturnedBundle(packagePath, filepath.Join(root, "output"), "", reviewedDigest); err == nil ||
		!strings.Contains(err.Error(), "extraction root") {
		t.Fatalf("absent output error = %v", err)
	}
	output := filepath.Join(root, "extracted")
	if err := os.Mkdir(output, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := ExtractReturnedBundle(packagePath, output, "", reviewedDigest); err != nil {
		t.Fatal(err)
	}
}

func returnedArchiveTestEntries(t *testing.T) []returnedArchiveTestEntry {
	t.Helper()
	publicKey, err := ssh.NewPublicKey(ed25519.PublicKey(bytes.Repeat([]byte{1}, ed25519.PublicKeySize)))
	if err != nil {
		t.Fatal(err)
	}
	publicRaw := ssh.MarshalAuthorizedKey(publicKey)
	entries := make([]returnedArchiveTestEntry, 0, len(_returnedBundleEntries)+1)
	entries = append(entries, returnedArchiveTestEntry{name: "evidence/", typeflag: tar.TypeDir})
	for _, entry := range _returnedBundleEntries {
		entries = append(entries, returnedArchiveTestEntry{
			name:     "evidence/" + entry.name,
			typeflag: tar.TypeReg,
			content:  []byte(entry.name + "\n"),
		})
	}
	entries[1].content = []byte(returnedSignerIdentity + " " + strings.TrimSpace(string(publicRaw)) + "\n")
	entries[len(entries)-1].content = publicRaw
	return entries
}

func writeReturnedArchiveTestPackage(
	t *testing.T,
	entries []returnedArchiveTestEntry,
	tailBytes int,
) []byte {
	t.Helper()
	var archive bytes.Buffer
	tarWriter := tar.NewWriter(&archive)
	for _, entry := range entries {
		header := &tar.Header{
			Name:     entry.name,
			Mode:     0o600,
			Size:     int64(len(entry.content)),
			Typeflag: entry.typeflag,
			Linkname: entry.linkname,
		}
		if entry.typeflag == tar.TypeDir {
			header.Mode = 0o700
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if len(entry.content) > 0 && entry.typeflag == tar.TypeReg {
			if _, err := tarWriter.Write(entry.content); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	archive.Write(bytes.Repeat([]byte{0}, tailBytes))
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	if _, err := gzipWriter.Write(archive.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return compressed.Bytes()
}
