package sync_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/sync"
)

// readFixture mirrors a repo with nested dirs and a binary blob; returns
// (dataDir, repoName, files) where files maps path → exact content.
func readFixture(t testing.TB) (string, string, map[string][]byte) {
	t.Helper()
	files := map[string][]byte{
		"README.md":        []byte("# fixture\nplain text\n"),
		"src/main.go":      []byte("package main\n\nfunc main() {}\n"),
		"src/deep/util.go": []byte("package deep\n"),
		"blob.bin":         {0x00, 0xff, 0x10, 0x80, 0x01},
	}
	origin := t.TempDir()
	gitc(t, origin, "init", "-b", "main")
	for path, content := range files {
		full := filepath.Join(origin, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitc(t, origin, "add", ".")
	gitc(t, origin, "commit", "-m", "fixture")

	dataDir := t.TempDir()
	const name = "example.com/read-fixture"
	if err := sync.Mirror(context.Background(), "file://"+origin, sync.RepoDir(dataDir, name)); err != nil {
		t.Fatal(err)
	}
	return dataDir, name, files
}

// T4.4 AC: content matches the checkout byte-for-byte, binary included.
func TestCatFileByteForByte(t *testing.T) {
	dataDir, name, files := readFixture(t)
	ctx := context.Background()
	for path, want := range files {
		got, err := sync.CatFile(ctx, dataDir, name, "", path)
		if err != nil {
			t.Fatalf("CatFile(%s): %v", path, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("CatFile(%s) = %q, want %q", path, got, want)
		}
	}

	if _, err := sync.CatFile(ctx, dataDir, name, "", "no/such/file.go"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("missing path err = %v, want ErrNotFound", err)
	}
	if _, err := sync.CatFile(ctx, dataDir, name, "deadbeef", "README.md"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("bad ref err = %v, want ErrNotFound", err)
	}
}

func TestFolderContentsAndTree(t *testing.T) {
	dataDir, name, _ := readFixture(t)
	ctx := context.Background()

	root, err := sync.FolderContents(ctx, dataDir, name, "", "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, e := range root {
		got[e.Name] = e.Type
	}
	want := map[string]string{"README.md": "file", "blob.bin": "file", "src": "dir"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("root entries = %v, want %v", got, want)
	}

	sub, err := sync.FolderContents(ctx, dataDir, name, "", "src")
	if err != nil {
		t.Fatal(err)
	}
	if len(sub) != 2 || sub[0].Name != "deep" || sub[0].Type != "dir" || sub[1].Name != "main.go" {
		t.Errorf("src entries = %+v", sub)
	}
	if sub[1].Size == 0 {
		t.Error("file entry missing size")
	}

	paths, err := sync.TreePaths(ctx, dataDir, name, "")
	if err != nil {
		t.Fatal(err)
	}
	wantPaths := []string{"README.md", "blob.bin", "src/deep/util.go", "src/main.go"}
	if !reflect.DeepEqual(paths, wantPaths) {
		t.Errorf("TreePaths = %v, want %v", paths, wantPaths)
	}
}

func TestEvilInputsRejected(t *testing.T) {
	dataDir, name, _ := readFixture(t)
	ctx := context.Background()
	evil := []struct{ ref, path string }{
		{"", "../../../../etc/passwd"},
		{"", "/etc/passwd"},
		{"", "--upload-pack=/bin/sh"},
		{"--exec=/bin/sh", "README.md"},
		{"", "a\x00b"},
		{"HEAD --", "README.md"},
		{"..", "README.md"},
	}
	for _, e := range evil {
		if _, err := sync.CatFile(ctx, dataDir, name, e.ref, e.path); !errors.Is(err, sync.ErrBadInput) {
			t.Errorf("CatFile(ref=%q, path=%q) err = %v, want ErrBadInput", e.ref, e.path, err)
		}
	}
}

// T4.4 AC: path traversal fuzz — any path either errors or returns content
// of a file that actually lives in the repo. cat-file resolves in the object
// database, so nothing outside the tree is reachable; the fuzz enforces it.
func FuzzCatFilePath(f *testing.F) {
	dataDir, name, files := readFixture(f)
	valid := map[string]bool{}
	for path := range files {
		valid[path] = true
	}
	contents := map[string]bool{}
	for _, c := range files {
		contents[string(c)] = true
	}

	f.Add("README.md")
	f.Add("../../../../etc/passwd")
	f.Add("--flag")
	f.Add("src/../README.md")
	f.Add("/abs/path")
	f.Fuzz(func(t *testing.T, path string) {
		got, err := sync.CatFile(context.Background(), dataDir, name, "", path)
		if err != nil {
			return // rejected or not found: fine
		}
		if !valid[path] {
			t.Fatalf("path %q not in the repo but returned %d bytes", path, len(got))
		}
		if !contents[string(got)] {
			t.Fatalf("path %q returned content not belonging to the repo", path)
		}
	})
}
