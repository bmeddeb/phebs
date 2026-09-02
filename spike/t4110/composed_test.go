package t4110

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestT4110PassedGoTestsRequiresExactNamedPasses(t *testing.T) {
	data := strings.Join([]string{
		`{"Action":"pass","Test":"TestAlpha"}`,
		`{"Action":"skip","Test":"TestBeta"}`,
		`{"Action":"pass","Package":"example.invalid/package"}`,
	}, "\n") + "\n"
	passed, err := passedGoTests([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if !passed["TestAlpha"] || passed["TestBeta"] || len(passed) != 1 {
		t.Fatalf("passed tests = %v", passed)
	}
}

func TestT4110PassedVitestRequiresExactAllPassedReport(t *testing.T) {
	report := func(status string, pending int) []byte {
		return []byte(fmt.Sprintf(`{"numTotalTests":1,"numPassedTests":%d,"numFailedTests":0,"numPendingTests":%d,"numTodoTests":0,"success":true,"testResults":[{"status":"passed","assertionResults":[{"fullName":"named case","title":"named case","status":%q}]}]}`+"\n",
			1-pending, pending, status))
	}
	passed, err := passedVitest(report("passed", 0))
	if err != nil || passed["named case"] != 1 {
		t.Fatalf("exact report = %v, %v", passed, err)
	}
	if _, err := passedVitest(report("skipped", 1)); err == nil {
		t.Fatal("skipped named output was accepted")
	}
	if _, err := passedVitest([]byte("named case\n")); err == nil {
		t.Fatal("name-only output was accepted")
	}
}

func TestT4110PassedGoTestsRejectsNonJSONOutput(t *testing.T) {
	if _, err := passedGoTests([]byte("not-json\n")); err == nil {
		t.Fatal("non-JSON go test output was accepted")
	}
}

func TestExtractGitBlobsAcceptsExactBatchAndRejectsDrift(t *testing.T) {
	content := []byte("exact\n")
	object := strings.Repeat("a", 40)
	valid := bytes.NewBufferString(fmt.Sprintf("%s blob %d\n", object, len(content)))
	valid.Write(content)
	valid.WriteByte('\n')
	destination := t.TempDir()
	entry := gitTreeEntry{object: object, path: "directory/file.txt", mode: fs.FileMode(0o644)}
	if err := extractGitBlobs(destination, []gitTreeEntry{entry}, valid); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(destination, "directory", "file.txt"))
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("extracted file = %q, %v", got, err)
	}

	drift := bytes.NewBufferString(fmt.Sprintf("%s blob %d\n", strings.Repeat("b", 40), len(content)))
	drift.Write(content)
	drift.WriteByte('\n')
	if err := extractGitBlobs(t.TempDir(), []gitTreeEntry{entry}, drift); err == nil {
		t.Fatal("drifted exact HEAD blob was accepted")
	}
}

func TestVerifyCheckoutMatchesExactHEADExport(t *testing.T) {
	root := t.TempDir()
	for _, arguments := range [][]string{
		{"init", "--quiet"},
		{"config", "user.name", "Neutral Gate"},
		{"config", "user.email", "gate@neutral.invalid"},
	} {
		if _, err := runCommand(t.Context(), root, "git", arguments...); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(root, "tracked.txt")
	if err := os.WriteFile(path, []byte("exact\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runCommand(t.Context(), root, "git", "add", "tracked.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := runCommand(t.Context(), root, "git", "commit", "--quiet", "-m", "exact"); err != nil {
		t.Fatal(err)
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	git, err := admitExecutable(gitPath)
	if err != nil {
		t.Fatal(err)
	}
	exported, err := exportComposedTree(t.Context(), root, git)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = removeComposedTree(exported) })
	if err := verifyCheckoutMatchesExport(t.Context(), root, exported, git); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("drift\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyCheckoutMatchesExport(t.Context(), root, exported, git); err == nil {
		t.Fatal("tracked byte drift matched the exact HEAD export")
	}
	if err := os.WriteFile(path, []byte("exact\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := verifyCheckoutMatchesExport(t.Context(), root, exported, git); err == nil {
		t.Fatal("tracked executable-mode drift matched the exact HEAD export")
	}
}

func TestBindComposedTreeGitKeepsExactExportClean(t *testing.T) {
	root := t.TempDir()
	for _, arguments := range [][]string{
		{"init", "--quiet"},
		{"config", "user.name", "Neutral Gate"},
		{"config", "user.email", "gate@neutral.invalid"},
	} {
		if _, err := runCommand(t.Context(), root, "git", arguments...); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("ignored.go\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tracked.go"), []byte("package ancestor\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runCommand(t.Context(), root, "git", "add", ".gitignore", "tracked.go"); err != nil {
		t.Fatal(err)
	}
	if _, err := runCommand(t.Context(), root, "git", "commit", "--quiet", "-m", "ancestor"); err != nil {
		t.Fatal(err)
	}
	ancestor, err := verifyCleanCommitWithGit(t.Context(), root, "git")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tracked.go"), []byte("package exact\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runCommand(t.Context(), root, "git", "add", "tracked.go"); err != nil {
		t.Fatal(err)
	}
	if _, err := runCommand(t.Context(), root, "git", "commit", "--quiet", "-m", "exact"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ignored.go"), []byte("package ignored\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	git, err := admitExecutable(gitPath)
	if err != nil {
		t.Fatal(err)
	}
	commit, err := verifyCleanCommitWithGit(t.Context(), root, git.path)
	if err != nil {
		t.Fatal(err)
	}
	exported, err := exportComposedTree(t.Context(), root, git)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = removeComposedTree(exported) })
	if err := bindComposedTreeGit(t.Context(), root, exported, commit, git); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(exported, "ignored.go")); !os.IsNotExist(err) {
		t.Fatalf("ignored source entered exact export: %v", err)
	}
	bound, err := verifyCleanCommitWithGit(t.Context(), exported, git.path)
	if err != nil || bound != commit {
		t.Fatalf("bound exact export commit = %q, %v", bound, err)
	}
	shallow, err := runCommand(t.Context(), exported, git.path, "rev-parse", "--is-shallow-repository")
	if err != nil || strings.TrimSpace(string(shallow)) != "true" {
		t.Fatalf("bound exact export shallow = %q, %v", shallow, err)
	}
	if _, err := runCommand(t.Context(), exported, git.path, "cat-file", "-e", ancestor+"^{commit}"); err == nil {
		t.Fatal("ancestor entered the shallow exact export")
	}
}
