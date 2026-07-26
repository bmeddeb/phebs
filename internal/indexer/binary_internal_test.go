package indexer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindBinaryResolvesConfiguredPathBeforeChildChdir(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	if err := os.Mkdir(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(binDir, "zoekt-git-index")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	t.Setenv("PHEBS_ZOEKT_GIT_INDEX", filepath.Join("bin", "zoekt-git-index"))

	got, err := FindBinary()
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(bin)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("FindBinary() = %q, want %q", got, want)
	}
}

func TestFindBinaryRejectsNonExecutableOverride(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "zoekt-git-index")
	if err := os.WriteFile(bin, []byte("not executable"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PHEBS_ZOEKT_GIT_INDEX", bin)

	if _, err := FindBinary(); err == nil {
		t.Fatal("FindBinary accepted a non-executable override")
	}
}
