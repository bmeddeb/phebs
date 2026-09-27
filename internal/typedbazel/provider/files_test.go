package provider

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCompilerExpansionPreflight(t *testing.T) {
	for _, kind := range []string{"exact", "traversal", "symlink", "duplicate", "unsorted"} {
		t.Run(kind, func(t *testing.T) {
			var b bytes.Buffer
			w := zip.NewWriter(&b)
			names := []string{"usr/bin/compiler", "usr/include/header"}
			switch kind {
			case "traversal":
				names[1] = "../escape"
			case "duplicate":
				names[1] = names[0]
			case "unsorted":
				names[0], names[1] = names[1], names[0]
			}
			for j, name := range names {
				h := &zip.FileHeader{Name: name, Method: zip.Store}
				h.SetMode(0755)
				if kind == "symlink" && j == 1 {
					h.SetMode(os.ModeSymlink | 0755)
				}
				f, e := w.CreateHeader(h)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = f.Write([]byte("fixed bytes")); e != nil {
					t.Fatal(e)
				}
			}
			if e := w.Close(); e != nil {
				t.Fatal(e)
			}
			r, e := zip.NewReader(bytes.NewReader(b.Bytes()), int64(b.Len()))
			if e != nil {
				t.Fatal(e)
			}
			root := t.TempDir()
			e = unpackCompiler(context.Background(), r, root)
			if (e == nil) != (kind == "exact") {
				t.Fatal(kind, e)
			}
			if kind == "exact" {
				st, e := os.Stat(filepath.Join(root, names[0]))
				if e != nil || st.Mode().Perm() != 0500 {
					t.Fatal("mode", e)
				}
				cc, e := os.ReadFile(filepath.Join(root, "cc"))
				if e != nil || string(cc) != compilerScript {
					t.Fatal("compiler wrapper", e)
				}
			} else {
				entries, e := os.ReadDir(root)
				if e != nil || len(entries) != 0 {
					t.Fatal("wrote before complete admission", e)
				}
			}
		})
	}
}
