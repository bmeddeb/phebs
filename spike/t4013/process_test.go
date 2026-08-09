package t4013

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractFrozenSourceAcceptsCanonicalGitDirectoryHeaders(t *testing.T) {
	archive := frozenSourceArchive(t,
		archiveEntry{name: ".claude/", kind: tar.TypeDir, mode: 0o775},
		archiveEntry{name: ".claude/launch.json", kind: tar.TypeReg, mode: 0o664, content: "frozen\n"},
		archiveEntry{name: "script.sh", kind: tar.TypeReg, mode: 0o775, content: "#!/bin/sh\n"},
	)
	output := filepath.Join(t.TempDir(), "source")
	if err := os.Mkdir(output, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := extractFrozenSource(bytes.NewReader(archive), output); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(output, ".claude", "launch.json"))
	if err != nil || string(content) != "frozen\n" {
		t.Fatalf("extracted content = %q, %v", content, err)
	}
	info, err := os.Stat(filepath.Join(output, "script.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("extracted executable mode = %v", info.Mode().Perm())
	}
}

func TestInspectStartupLogRetainsOnlyClosedStageAndDigest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	raw := []byte("private startup detail stays in custody\n" +
		"2026/08/08 00:00:00 T40.13 startup lifecycle: {\"schema\":\"t4013-source-free-startup-v1\",\"stage\":\"store_opened\"}\n" +
		"more private detail stays in custody\n")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	bytes, digest, stage, err := inspectStartupLog(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if bytes != int64(len(raw)) || digest != "sha256:"+hex.EncodeToString(sum[:]) || stage != "store_opened" {
		t.Fatalf("startup log = %d, %q, %q", bytes, digest, stage)
	}
	if digest == string(raw) {
		t.Fatal("startup diagnostic retained raw log content")
	}
}

func TestInspectStartupLogRejectsUnknownStage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	if err := os.WriteFile(path, []byte(
		"T40.13 startup lifecycle: {\"schema\":\"t4013-source-free-startup-v1\",\"stage\":\"private_path\"}\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := inspectStartupLog(path); err == nil {
		t.Fatal("unknown startup stage passed")
	}
}

func TestExtractFrozenSourceRejectsNoncanonicalAndEscapingEntries(t *testing.T) {
	tests := []struct {
		name  string
		entry archiveEntry
	}{
		{name: "absolute", entry: archiveEntry{name: "/escape", kind: tar.TypeReg}},
		{name: "parent", entry: archiveEntry{name: "../escape", kind: tar.TypeReg}},
		{name: "embedded parent", entry: archiveEntry{name: "inside/../../escape", kind: tar.TypeReg}},
		{name: "repeated separator", entry: archiveEntry{name: "inside//file", kind: tar.TypeReg}},
		{name: "backslash", entry: archiveEntry{name: `inside\file`, kind: tar.TypeReg}},
		{name: "root directory", entry: archiveEntry{name: "./", kind: tar.TypeDir}},
		{name: "symlink", entry: archiveEntry{name: "link", kind: tar.TypeSymlink}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			archive := frozenSourceArchive(t, test.entry)
			output := filepath.Join(t.TempDir(), "source")
			if err := os.Mkdir(output, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := extractFrozenSource(bytes.NewReader(archive), output); err == nil {
				t.Fatal("invalid frozen source entry passed")
			}
			entries, err := os.ReadDir(output)
			if err != nil || len(entries) != 0 {
				t.Fatalf("rejected archive wrote entries: %v, %v", entries, err)
			}
		})
	}
	if _, err := frozenArchiveName(&tar.Header{Name: "file/", Typeflag: tar.TypeReg}); err == nil {
		t.Fatal("non-directory trailing slash passed archive-name validation")
	}
}

type archiveEntry struct {
	name    string
	kind    byte
	mode    int64
	content string
}

func frozenSourceArchive(t *testing.T, entries ...archiveEntry) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := tar.NewWriter(&output)
	for _, entry := range entries {
		mode := entry.mode
		if mode == 0 {
			mode = 0o600
		}
		header := &tar.Header{
			Name: entry.name, Typeflag: entry.kind, Mode: mode,
			Size: int64(len(entry.content)),
		}
		if entry.kind == tar.TypeDir {
			header.Size = 0
		}
		if entry.kind == tar.TypeSymlink {
			header.Linkname = "target"
			header.Size = 0
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Size > 0 {
			if _, err := writer.Write([]byte(entry.content)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
