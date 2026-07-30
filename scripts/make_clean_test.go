package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var makeCleanOwnedSentinels = []string{
	"phebs",
	"coverage.out",
	"bin/zoekt-git-index",
	"bin/phebs-focused-index",
	"bin/buf",
	"ui/dist/assets/app.js",
	"dist/.build-v0.2.1-dev-test/staged",
	"dist/phebs-v0.2.1-dev-test/release-manifest.json",
	"dist/.phebs-v0.2.1-dev-test.tmp-123/staged",
	"dist/.build-personal/keep",
	"dist/phebs-personal/keep",
	"dist/.phebs-personal.tmp-123/keep",
}

var makeCleanPreservedSentinels = []string{
	"bin/custom-tool",
	"dist/custom/keep",
	"dist/release/phebs-v0.2.1-test.tar.gz",
	"dist/release/phebs-v0.2.1-test.tar.gz.sha256",
	"data/repos/keep",
	"uber-phebs.yaml",
	"ui/node_modules/dependency/keep",
	".claude/keep",
	".idea/keep",
	".git",
	"docs/fixtures/keep",
	"internal/example/testdata/keep",
	"spike/example/out/keep",
	"unrelated.ignored",
}

var makeCleanExternalSentinels = []string{
	"keep",
	".build-v0.2.1-dev-test/keep",
	"phebs-v0.2.1-dev-test/keep",
	".phebs-v0.2.1-dev-test.tmp-123/keep",
}

func TestMakeCleanRemovesOnlyOwnedOutputs(t *testing.T) {
	workspace := makeCleanWorkspace(t)
	externalRoot := filepath.Join(t.TempDir(), "external release root")
	writeMakeCleanSentinels(t, workspace, makeCleanOwnedSentinels)
	writeMakeCleanSentinels(t, workspace, makeCleanPreservedSentinels)
	writeMakeCleanSentinels(t, externalRoot, makeCleanExternalSentinels)

	for range 2 {
		runMakeClean(t, workspace, "Makefile", externalRoot, true)
		assertMakeCleanSentinelsAbsent(t, workspace, makeCleanOwnedSentinels)
		assertMakeCleanSentinelsPresent(t, workspace, makeCleanPreservedSentinels)
		assertMakeCleanSentinelsPresent(t, externalRoot, makeCleanExternalSentinels)
	}
}

func TestMakeCleanRefusesWrongWorkingDirectory(t *testing.T) {
	workspace := makeCleanWorkspace(t)
	victim := filepath.Join(t.TempDir(), "unrelated working directory")
	makeCleanParseDirs(t, victim)
	externalRoot := filepath.Join(t.TempDir(), "external release root")
	writeMakeCleanSentinels(t, workspace, makeCleanOwnedSentinels)
	writeMakeCleanSentinels(t, victim, makeCleanOwnedSentinels)

	runMakeClean(t, victim, filepath.Join(workspace, "Makefile"), externalRoot, false)

	assertMakeCleanSentinelsPresent(t, workspace, makeCleanOwnedSentinels)
	assertMakeCleanSentinelsPresent(t, victim, makeCleanOwnedSentinels)
}

func TestMakeCleanUsesVerifiedWorkingCheckoutWithForeignMakefile(t *testing.T) {
	selected := makeCleanWorkspace(t)
	working := makeCleanWorkspace(t)
	externalRoot := filepath.Join(t.TempDir(), "external release root")
	writeMakeCleanSentinels(t, selected, makeCleanOwnedSentinels)
	writeMakeCleanSentinels(t, working, makeCleanOwnedSentinels)

	runMakeClean(t, working, filepath.Join(selected, "Makefile"), externalRoot, true)

	assertMakeCleanSentinelsPresent(t, selected, makeCleanOwnedSentinels)
	assertMakeCleanSentinelsAbsent(t, working, makeCleanOwnedSentinels)
}

func TestMakeCleanRefusesSymlinkedMakefileRoot(t *testing.T) {
	workspace := makeCleanWorkspace(t)
	victim := filepath.Join(t.TempDir(), "unrelated working directory")
	makeCleanRepositoryMarkers(t, victim)
	writeMakeCleanSentinels(t, victim, makeCleanOwnedSentinels)
	if err := os.Symlink(filepath.Join(workspace, "Makefile"), filepath.Join(victim, "Makefile")); err != nil {
		t.Fatal(err)
	}

	runMakeClean(t, victim, "Makefile", filepath.Join(t.TempDir(), "release"), false)

	assertMakeCleanSentinelsPresent(t, victim, makeCleanOwnedSentinels)
}

func TestMakeCleanRefusesUnexpectedModuleBeforeDeletion(t *testing.T) {
	workspace := makeCleanWorkspace(t)
	writeMakeCleanSentinels(t, workspace, makeCleanOwnedSentinels)
	if err := os.WriteFile(
		filepath.Join(workspace, "go.mod"),
		[]byte("module example.com/not-phebs\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	runMakeClean(t, workspace, "Makefile", filepath.Join(t.TempDir(), "release"), false)

	assertMakeCleanSentinelsPresent(t, workspace, makeCleanOwnedSentinels)
}

func TestMakeCleanTreatsMetacharacterCheckoutPathAsData(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(
		base,
		"checkout-$PHEBS_CLEAN_ALIAS-'quoted'-`touch PHEBS_CLEAN_INJECTED`",
	)
	makeCleanWorkspaceAt(t, workspace)
	redirected := filepath.Join(base, "checkout-redirected-quoted-")
	makeCleanWorkspaceAt(t, redirected)
	externalRoot := filepath.Join(t.TempDir(), "external release root")
	writeMakeCleanSentinels(t, workspace, makeCleanOwnedSentinels)
	writeMakeCleanSentinels(t, redirected, makeCleanOwnedSentinels)

	runMakeClean(t, workspace, "Makefile", externalRoot, true)

	assertMakeCleanSentinelsAbsent(t, workspace, makeCleanOwnedSentinels)
	assertMakeCleanSentinelsPresent(t, redirected, makeCleanOwnedSentinels)
	for _, path := range []string{
		filepath.Join(workspace, "PHEBS_CLEAN_INJECTED"),
		filepath.Join(base, "PHEBS_CLEAN_INJECTED"),
	} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("checkout path was evaluated as shell syntax: %s (err=%v)", path, err)
		}
	}
}

func TestMakeCleanRefusesSymlinkedParentsBeforeDeletion(t *testing.T) {
	for _, name := range []string{"bin", "dist", "ui"} {
		t.Run(name, func(t *testing.T) {
			workspace := makeCleanWorkspace(t)
			externalRoot := filepath.Join(t.TempDir(), "external")
			writeMakeCleanSentinels(t, workspace, makeCleanOwnedSentinels)
			if err := os.RemoveAll(filepath.Join(workspace, name)); err != nil {
				t.Fatal(err)
			}
			writeMakeCleanSentinel(t, externalRoot, "keep")
			if name == "ui" {
				writeMakeCleanSentinel(t, externalRoot, "package.json")
			}
			if err := os.Symlink(externalRoot, filepath.Join(workspace, name)); err != nil {
				t.Fatal(err)
			}

			runMakeClean(t, workspace, "Makefile", filepath.Join(t.TempDir(), "release"), false)

			assertMakeCleanSentinelsPresent(
				t,
				workspace,
				makeCleanSentinelsOutsideParent(makeCleanOwnedSentinels, name),
			)
			assertMakeCleanSentinelsPresent(t, externalRoot, []string{"keep"})
			info, err := os.Lstat(filepath.Join(workspace, name))
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("%s symlink was changed", name)
			}
		})
	}
}

func TestMakeCleanUnlinksOwnedSymlinksWithoutFollowing(t *testing.T) {
	for _, name := range []string{
		"phebs",
		"ui/dist",
		"dist/.build-personal",
	} {
		t.Run(name, func(t *testing.T) {
			workspace := makeCleanWorkspace(t)
			writeMakeCleanSentinels(t, workspace, makeCleanOwnedSentinels)
			path := filepath.Join(workspace, filepath.FromSlash(name))
			if err := os.RemoveAll(path); err != nil {
				t.Fatal(err)
			}
			external := filepath.Join(t.TempDir(), "external")
			writeMakeCleanSentinel(t, external, "keep")
			if err := os.Symlink(external, path); err != nil {
				t.Fatal(err)
			}

			runMakeClean(t, workspace, "Makefile", filepath.Join(t.TempDir(), "release"), true)

			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatalf("%s was not unlinked (err=%v)", name, err)
			}
			assertMakeCleanSentinelsPresent(t, external, []string{"keep"})
		})
	}
}

func TestMakeCleanRefusesUnexpectedFileOutputBeforeDeletion(t *testing.T) {
	for _, name := range []string{
		"phebs",
		"coverage.out",
		"bin/zoekt-git-index",
		"bin/phebs-focused-index",
		"bin/buf",
	} {
		t.Run(name, func(t *testing.T) {
			workspace := makeCleanWorkspace(t)
			writeMakeCleanSentinels(t, workspace, makeCleanOwnedSentinels)
			path := filepath.Join(workspace, filepath.FromSlash(name))
			if err := os.RemoveAll(path); err != nil {
				t.Fatal(err)
			}
			writeMakeCleanSentinel(t, workspace, name+"/keep")

			runMakeClean(t, workspace, "Makefile", filepath.Join(t.TempDir(), "release"), false)

			assertMakeCleanSentinelsPresent(
				t,
				workspace,
				makeCleanSentinelsExcept(makeCleanOwnedSentinels, name),
			)
			assertMakeCleanSentinelsPresent(t, workspace, []string{name + "/keep"})
		})
	}
}

func makeCleanWorkspace(t *testing.T) string {
	t.Helper()

	workspace := filepath.Join(t.TempDir(), "checkout with spaces")
	makeCleanWorkspaceAt(t, workspace)
	return workspace
}

func makeCleanWorkspaceAt(t *testing.T, workspace string) {
	t.Helper()

	root := filepath.Clean("..")
	makefile, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "Makefile"), makefile, 0o600); err != nil {
		t.Fatal(err)
	}
	makeCleanRepositoryMarkers(t, workspace)
}

func makeCleanRepositoryMarkers(t *testing.T, root string) {
	t.Helper()

	makeCleanParseDirs(t, root)
	if err := os.MkdirAll(filepath.Join(root, "cmd", "phebs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "ui"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"go.mod":          "module github.com/bmeddeb/phebs\n",
		"ui/package.json": "{}\n",
		".git":            "keep\n",
	} {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func makeCleanParseDirs(t *testing.T, root string) {
	t.Helper()

	for _, name := range []string{"cmd/phebs-focused-index", "internal"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(name)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{
		".go-version",
		".node-version",
		".golangci-lint-version",
		".surrealdb-version",
	} {
		writeMakeCleanSentinel(t, root, name)
	}
}

func runMakeClean(t *testing.T, invocationDir, makefile, externalRoot string, wantSuccess bool) {
	t.Helper()

	cmd := exec.Command(
		"make",
		"-f",
		makefile,
		"clean",
		"VERSION=../../hostile",
		"GO_VERSION=test",
		"NODE_VERSION=test",
		"GOLANGCI_LINT_VERSION=test",
		"SURREALDB_VERSION=test",
		"TARGET_GOOS=test",
		"TARGET_GOARCH=test",
		"RELEASE_COMMIT=test",
		"RELEASE_ROOT="+externalRoot,
		"PHEBS_MAKE_ROOT="+externalRoot,
		"MAKEFILE_LIST="+filepath.Join(externalRoot, "not-selected.mk"),
	)
	cmd.Dir = invocationDir
	cmd.Env = append(os.Environ(), "PHEBS_CLEAN_ALIAS=redirected")
	output, err := cmd.CombinedOutput()
	if wantSuccess {
		if err != nil {
			t.Fatalf("make clean failed: %v\n%s", err, output)
		}
		return
	}
	if err == nil {
		t.Fatalf("make clean unexpectedly succeeded:\n%s", output)
	}
	if !strings.Contains(string(output), "make clean: refusing ") {
		t.Fatalf("make clean failed without its refusal contract: %v\n%s", err, output)
	}
}

func writeMakeCleanSentinels(t *testing.T, root string, names []string) {
	t.Helper()

	for _, name := range names {
		writeMakeCleanSentinel(t, root, name)
	}
}

func writeMakeCleanSentinel(t *testing.T, root, name string) {
	t.Helper()

	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertMakeCleanSentinelsPresent(t *testing.T, root string, names []string) {
	t.Helper()

	for _, name := range names {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("%s was not preserved: %v", name, err)
		}
		if string(content) != "keep\n" {
			t.Fatalf("%s changed to %q", name, content)
		}
	}
}

func assertMakeCleanSentinelsAbsent(t *testing.T, root string, names []string) {
	t.Helper()

	for _, name := range names {
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(name))); !os.IsNotExist(err) {
			t.Errorf("%s still exists after make clean (err=%v)", name, err)
		}
	}
}

func makeCleanSentinelsOutsideParent(names []string, parent string) []string {
	var result []string
	prefix := parent + "/"
	for _, name := range names {
		if !strings.HasPrefix(name, prefix) {
			result = append(result, name)
		}
	}
	return result
}

func makeCleanSentinelsExcept(names []string, excluded string) []string {
	var result []string
	for _, name := range names {
		if name != excluded {
			result = append(result, name)
		}
	}
	return result
}
