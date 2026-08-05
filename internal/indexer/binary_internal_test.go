package indexer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecutablePathResolvesConfiguredPathBeforeChildChdir(t *testing.T) {
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
	got, err := executablePath(filepath.Join("bin", "zoekt-git-index"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(bin)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("executablePath() = %q, want %q", got, want)
	}
}

func TestEmbeddedZoektPinMatchesModuleFiles(t *testing.T) {
	root := filepath.Join("..", "..")
	goMod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	goSum, err := os.ReadFile(filepath.Join(root, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	moduleLine := "github.com/sourcegraph/zoekt " + zoektModuleVersion
	checksumLine := moduleLine + " " + zoektModuleSum
	if !strings.Contains(string(goMod), moduleLine) ||
		!strings.Contains(string(goSum), checksumLine) {
		t.Fatalf(
			"embedded zoekt pin %s is absent from go.mod/go.sum", checksumLine,
		)
	}
}

func TestFindBinaryRejectsExecutableWithoutPinnedBuildIdentity(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "zoekt-git-index")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PHEBS_ZOEKT_GIT_INDEX", bin)
	if _, err := FindBinary(); err == nil {
		t.Fatal("FindBinary accepted an executable without Go module identity")
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
