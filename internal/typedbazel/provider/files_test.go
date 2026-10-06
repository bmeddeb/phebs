package provider

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCompilerIdentityStaysArchSpecific(t *testing.T) {
	arm, ok := compilerScriptFor("arm64")
	amd, ok64 := compilerScriptFor("amd64")
	if !ok || !ok64 || arm == amd {
		t.Fatal("compiler scripts")
	}
	if !bytes.Contains([]byte(arm), []byte("aarch64-linux-gnu-gcc-12")) || bytes.Contains([]byte(amd), []byte("aarch64")) {
		t.Fatal("arm64 script changed or leaked into amd64")
	}
	if !bytes.Contains([]byte(amd), []byte("x86_64-linux-gnu-gcc-13")) {
		t.Fatal("amd64 script")
	}
	digest, ok := compilerDigestFor("amd64")
	if !ok || digest != compilerDigestAmd64 || compilerDigestArm64 == compilerDigestAmd64 {
		t.Fatal("compiler digests")
	}
	if _, err := compilerZIP([]byte("not-the-sealed-sysroot")); err == nil {
		t.Fatal("unsealed sysroot accepted")
	}
	if runtime.GOARCH == "amd64" {
		raw, err := os.ReadFile("/tmp/cc-sysroot-amd64.zip")
		if err != nil {
			t.Skip("local sealed archive is not on this machine")
		}
		if hash(raw) != compilerDigestAmd64 {
			t.Fatal("sealed amd64 archive digest changed")
		}
		if _, err = compilerZIP(raw); err != nil {
			t.Fatal(err)
		}
	}
}

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
				script, ok := compilerScriptFor(runtime.GOARCH)
				if e != nil || !ok || string(cc) != script {
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
