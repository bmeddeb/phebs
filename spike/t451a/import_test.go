package t451a

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func wire(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(data, '\n')
}

func TestBundleAdmission(t *testing.T) {
	content := []byte("neutral tool bytes\n")
	base := Bundle{Schema: "phebs-t451a-offline-v1", Files: []BundleFile{{Path: "tools/bin/tool", Bytes: int64(len(content)), SHA256: Digest(content), Executable: true}}}
	for _, tc := range []struct {
		name   string
		change func(*Bundle)
		edit   func(*testing.T, string)
		wire   func([]byte) []byte
		cancel bool
		pass   bool
	}{
		{name: "verified immutable copy", pass: true},
		{name: "wrong digest", change: func(b *Bundle) { b.Files[0].SHA256 = Digest(nil) }},
		{name: "truncated", change: func(b *Bundle) { b.Files[0].Bytes++ }},
		{name: "oversized", change: func(b *Bundle) { b.Files[0].Bytes = MaxFileBytes + 1 }},
		{name: "traversal", change: func(b *Bundle) { b.Files[0].Path = "../escape" }},
		{name: "duplicate path", change: func(b *Bundle) { b.Files = append(b.Files, b.Files[0]) }},
		{name: "unknown field", wire: func(b []byte) []byte { return []byte(strings.Replace(string(b), "{", "{\n  \"unknown\": true,", 1)) }},
		{name: "duplicate key", wire: func(b []byte) []byte {
			return []byte(strings.Replace(string(b), "{", "{\n  \"schema\": \"phebs-t451a-offline-v1\",", 1))
		}},
		{name: "trailing value", wire: func(b []byte) []byte { return append(b, []byte("{}")...) }},
		{name: "extra file", edit: func(t *testing.T, root string) { mustWrite(t, filepath.Join(root, "extra"), content) }},
		{name: "missing file", edit: func(t *testing.T, root string) {
			if err := os.Remove(filepath.Join(root, "tools/bin/tool")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "file symlink", edit: func(t *testing.T, root string) {
			name := filepath.Join(root, "tools/bin/tool")
			if err := os.Rename(name, name+"-original"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("tool-original", name); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "directory symlink", edit: func(t *testing.T, root string) {
			if err := os.Rename(filepath.Join(root, "tools"), filepath.Join(root, "original")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("original", filepath.Join(root, "tools")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "cancelled", cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, parent := t.TempDir(), t.TempDir()
			if err := os.MkdirAll(filepath.Join(source, "tools/bin"), 0700); err != nil {
				t.Fatal(err)
			}
			mustWrite(t, filepath.Join(source, "tools/bin/tool"), content)
			bundle := base
			bundle.Files = append([]BundleFile(nil), base.Files...)
			if tc.change != nil {
				tc.change(&bundle)
			}
			if tc.edit != nil {
				tc.edit(t, source)
			}
			manifest := wire(t, bundle)
			if tc.wire != nil {
				manifest = tc.wire(manifest)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			copied, err := ImportBundle(ctx, source, parent, manifest, Digest(manifest))
			if !tc.pass {
				if err == nil {
					t.Fatal("invalid bundle admitted")
				}
				entries, readErr := os.ReadDir(parent)
				if readErr != nil || len(entries) != 0 {
					t.Fatalf("partial custody retained: %v (%v)", entries, readErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			mustWrite(t, filepath.Join(source, "tools/bin/tool"), []byte("changed source"))
			data, err := os.ReadFile(filepath.Join(copied, "tools/bin/tool"))
			if err != nil || string(data) != string(content) {
				t.Fatalf("copy changed: %q, %v", data, err)
			}
			info, err := os.Stat(filepath.Join(copied, "tools/bin/tool"))
			if err != nil || info.Mode().Perm() != 0500 {
				t.Fatalf("bad private executable mode: %v %v", info, err)
			}
		})
	}
}

func mustWrite(t *testing.T, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(name, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestBundleDirectoryAmplificationRefusesBeforeCopy(t *testing.T) {
	bundle := Bundle{Schema: "phebs-t451a-offline-v1"}
	for index := range 1000 {
		bundle.Files = append(bundle.Files, BundleFile{Path: fmt.Sprintf("root%04d/", index) + strings.Repeat("d/", 32) + "file", SHA256: Digest(nil)})
	}
	manifest := wire(t, bundle)
	parent := t.TempDir()
	_, err := ImportBundle(context.Background(), t.TempDir(), parent, manifest, Digest(manifest))
	if err == nil || !strings.Contains(err.Error(), "directory count limit") {
		t.Fatalf("directory amplification was not refused: %v", err)
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatalf("import mutated custody before refusal: %v %v", entries, err)
	}
}
