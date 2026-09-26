package t451a

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestCompilerArchiveBoundary(t *testing.T) {
	for _, tc := range []struct {
		name    string
		names   []string
		symlink bool
		pass    bool
	}{
		{"case distinct", []string{"usr/include/NAME.h", "usr/include/name.h"}, false, true},
		{"parent escape", []string{"../escape"}, false, false},
		{"absolute", []string{"/escape"}, false, false},
		{"duplicate", []string{"same", "same"}, false, false},
		{"link", []string{"link"}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			archive := filepath.Join(root, "compiler.zip")
			file, err := os.Create(archive)
			if err != nil {
				t.Fatal(err)
			}
			writer := zip.NewWriter(file)
			for _, name := range tc.names {
				header := zip.FileHeader{Name: name, Method: zip.Store}
				header.SetMode(0644)
				if tc.symlink {
					header.SetMode(os.ModeSymlink | 0777)
				}
				entry, err := writer.CreateHeader(&header)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := entry.Write([]byte("neutral")); err != nil {
					t.Fatal(err)
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			err = unpackCompiler(archive, filepath.Join(root, "out"))
			// The case-distinct input must refuse on a case-folding host and
			// pass on Linux; refusing is preferable to silently replacing bytes.
			if tc.pass && err != nil {
				if _, statErr := os.Stat(filepath.Join(root, "out/usr/include/NAME.h")); statErr == nil && os.IsExist(err) {
					return
				}
			}
			if (err == nil) != tc.pass {
				t.Fatalf("archive admission mismatch: %v", err)
			}
		})
	}
}

func TestCommandOutputSharesOneLimit(t *testing.T) {
	cancelled := false
	budget := commandBudget{remaining: 4, cancel: func() { cancelled = true }}
	stdout, stderr := commandOutput{budget: &budget}, commandOutput{budget: &budget}
	if _, err := stdout.Write([]byte("123")); err != nil {
		t.Fatal(err)
	}
	if _, err := stderr.Write([]byte("45")); err == nil {
		t.Fatal("combined output exceeded its bound")
	}
	if stdout.buffer.String() != "123" || stderr.buffer.Len() != 0 {
		t.Fatal("overflow bytes retained")
	}
	if !cancelled {
		t.Fatal("overflow did not cancel the child")
	}
}
