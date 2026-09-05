package t421

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/executableidentity"
)

type executionCheckoutFixture struct {
	root        string
	git         string
	plan        string
	integration string
	source      string
}

func TestInspectExecutionCheckoutBindsRealGitLineage(t *testing.T) {
	fixture := newExecutionCheckoutFixture(t)
	got, err := fixture.inspect(t)
	if err != nil {
		t.Fatal(err)
	}
	want := ExecutionCommits{
		IntegratedMainCommit:                 fixture.integration,
		IntegratedMainTree:                   fixture.command(t, "rev-parse", fixture.integration+"^{tree}"),
		T422SourceCommit:                     fixture.source,
		T422SourceTree:                       fixture.command(t, "rev-parse", fixture.source+"^{tree}"),
		CleanTree:                            true,
		IntegratedMainDescendsFromPlanSource: true,
		SourceDescendsFromIntegratedMain:     true,
	}
	if got != want {
		t.Fatalf("checkout admission = %#v, want %#v", got, want)
	}
	if status := fixture.command(t, "status", "--porcelain=v1", "--untracked-files=all"); status != "" {
		t.Fatalf("read-only admission changed checkout: %q", status)
	}
}

func TestInspectExecutionCheckoutRejectsWrongAuthority(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, *executionCheckoutFixture)
	}{
		{"missing plan", func(_ *testing.T, f *executionCheckoutFixture) { f.plan = strings.Repeat("0", 40) }},
		{"missing integration", func(_ *testing.T, f *executionCheckoutFixture) { f.integration = strings.Repeat("0", 40) }},
		{"missing source", func(_ *testing.T, f *executionCheckoutFixture) { f.source = strings.Repeat("0", 40) }},
		{"abbreviated plan", func(_ *testing.T, f *executionCheckoutFixture) { f.plan = f.plan[:12] }},
		{"abbreviated integration", func(_ *testing.T, f *executionCheckoutFixture) { f.integration = f.integration[:12] }},
		{"abbreviated source", func(_ *testing.T, f *executionCheckoutFixture) { f.source = f.source[:12] }},
		{"revision expression", func(_ *testing.T, f *executionCheckoutFixture) { f.source = "HEAD" }},
		{"wrong HEAD", func(t *testing.T, f *executionCheckoutFixture) {
			f.command(t, "checkout", "--quiet", "--detach", f.integration)
		}},
		{"nested root", func(_ *testing.T, f *executionCheckoutFixture) { f.root = filepath.Join(f.root, "nested") }},
		{"plan after integration", func(_ *testing.T, f *executionCheckoutFixture) { f.plan = f.source }},
		{"unrelated integration", func(t *testing.T, f *executionCheckoutFixture) {
			f.integration = f.command(t, "commit-tree", f.command(t, "rev-parse", "HEAD^{tree}"), "-m", "unrelated")
		}},
		{"unrelated plan", func(t *testing.T, f *executionCheckoutFixture) {
			f.plan = f.command(t, "commit-tree", f.command(t, "rev-parse", "HEAD^{tree}"), "-m", "unrelated")
		}},
		{"tree instead of commit", func(t *testing.T, f *executionCheckoutFixture) {
			f.integration = f.command(t, "rev-parse", f.integration+"^{tree}")
		}},
		{"relative Git executable", func(_ *testing.T, f *executionCheckoutFixture) { f.git = "git" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newExecutionCheckoutFixture(t)
			test.mutate(t, &fixture)
			if _, err := fixture.inspect(t); err == nil {
				t.Fatal("invalid external checkout authority was admitted")
			}
		})
	}
}

func TestInspectExecutionCheckoutRejectsNonExactInputs(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, *executionCheckoutFixture)
	}{
		{"unstaged", func(t *testing.T, f *executionCheckoutFixture) { f.write(t, "tracked.txt", "change\n") }},
		{"staged", func(t *testing.T, f *executionCheckoutFixture) {
			f.write(t, "tracked.txt", "change\n")
			f.command(t, "add", "tracked.txt")
		}},
		{"same size and mtime", func(t *testing.T, f *executionCheckoutFixture) {
			path := filepath.Join(f.root, "tracked.txt")
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			f.command(t, "config", "core.trustctime", "false")
			f.write(t, "tracked.txt", "change\n")
			if err := os.Chtimes(path, before.ModTime(), before.ModTime()); err != nil {
				t.Fatal(err)
			}
		}},
		{"untracked", func(t *testing.T, f *executionCheckoutFixture) { f.write(t, "extra.go", "package extra\n") }},
		{"ignored file", func(t *testing.T, f *executionCheckoutFixture) { f.write(t, "ignored_test.go", "package ignored\n") }},
		{"ignored ancestor", func(t *testing.T, f *executionCheckoutFixture) {
			f.write(t, "ignored/nested/extra.go", "package ignored\n")
		}},
		{"assume unchanged", func(t *testing.T, f *executionCheckoutFixture) {
			f.command(t, "update-index", "--assume-unchanged", "tracked.txt")
		}},
		{"skip worktree", func(t *testing.T, f *executionCheckoutFixture) {
			f.command(t, "update-index", "--skip-worktree", "tracked.txt")
		}},
		{"executable mode", func(t *testing.T, f *executionCheckoutFixture) {
			f.command(t, "config", "core.fileMode", "false")
			if err := os.Chmod(filepath.Join(f.root, "tracked.txt"), 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{"tracked symlink", func(t *testing.T, f *executionCheckoutFixture) {
			if err := os.Symlink("tracked.txt", filepath.Join(f.root, "tracked-link")); err != nil {
				t.Fatal(err)
			}
			f.command(t, "add", "tracked-link")
			f.source = f.commit(t, "tracked symlink")
		}},
		{"symlink ancestor", func(t *testing.T, f *executionCheckoutFixture) {
			old := filepath.Join(f.root, "nested")
			destination := filepath.Join(t.TempDir(), "moved")
			if err := os.Rename(old, destination); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(destination, old); err != nil {
				t.Fatal(err)
			}
		}},
		{"gitlink", func(t *testing.T, f *executionCheckoutFixture) {
			f.command(t, "update-index", "--add", "--cacheinfo", "160000,"+f.source+",linked")
			f.source = f.commit(t, "gitlink")
		}},
		{"unmerged index", func(t *testing.T, f *executionCheckoutFixture) {
			object := f.command(t, "rev-parse", "HEAD:tracked.txt")
			input := "0 " + strings.Repeat("0", 40) + "\ttracked.txt\n" +
				"100644 " + object + " 1\ttracked.txt\n" +
				"100644 " + object + " 2\ttracked.txt\n"
			f.commandInput(t, input, "update-index", "--index-info")
		}},
		{"grafts", func(t *testing.T, f *executionCheckoutFixture) { f.write(t, ".git/info/grafts", f.source+"\n") }},
		{"shallow", func(t *testing.T, f *executionCheckoutFixture) { f.write(t, ".git/shallow", f.source+"\n") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newExecutionCheckoutFixture(t)
			test.mutate(t, &fixture)
			if _, err := fixture.inspect(t); err == nil {
				t.Fatal("non-exact checkout inputs were admitted")
			}
		})
	}
}

func TestInspectExecutionCheckoutScrubsAmbientGitControls(t *testing.T) {
	fixture := newExecutionCheckoutFixture(t)
	for _, name := range []string{
		"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY",
		"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM",
		"GIT_REPLACE_REF_BASE", "GIT_TRACE", "GIT_EXTERNAL_DIFF",
	} {
		t.Setenv(name, filepath.Join(t.TempDir(), "must-not-be-used"))
	}
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.bare")
	t.Setenv("GIT_CONFIG_VALUE_0", "true")
	if _, err := fixture.inspect(t); err != nil {
		t.Fatalf("ambient Git controls influenced exact checkout admission: %v", err)
	}
}

func TestInspectExecutionCheckoutDoesNotLaunchLocalHelpers(t *testing.T) {
	for _, dirty := range []bool{false, true} {
		t.Run(fmt.Sprintf("dirty=%t", dirty), func(t *testing.T) {
			fixture := newExecutionCheckoutFixture(t)
			fixture.write(t, ".gitattributes", "tracked.txt filter=admission-probe\n")
			fixture.command(t, "add", ".gitattributes")
			fixture.source = fixture.commit(t, "filter attribute")
			helperRoot := t.TempDir()
			marker := filepath.Join(helperRoot, "called")
			helper := filepath.Join(helperRoot, "local-helper")
			if err := os.WriteFile(helper, []byte(fmt.Sprintf(
				"#!/bin/sh\nprintf called > %q\nprintf 'source\\n'\n", marker,
			)), 0o700); err != nil {
				t.Fatal(err)
			}
			fixture.command(t, "config", "filter.admission-probe.clean", helper)
			fixture.command(t, "config", "filter.admission-probe.required", "true")
			fixture.command(t, "config", "core.fsmonitor", helper)
			if dirty {
				fixture.write(t, "tracked.txt", "change\n")
			}
			_, err := fixture.inspect(t)
			if dirty && err == nil {
				t.Fatal("clean filter hid modified raw build bytes")
			}
			if !dirty && err != nil {
				t.Fatalf("unused local helper configuration refused clean raw bytes: %v", err)
			}
			if _, statErr := os.Lstat(marker); !os.IsNotExist(statErr) {
				t.Fatalf("checkout admission launched a local filter or fsmonitor: %v", statErr)
			}
		})
	}
}

func TestInspectExecutionCheckoutRejectsCanceledContext(t *testing.T) {
	fixture := newExecutionCheckoutFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := InspectExecutionCheckout(ctx, fixture.root, fixture.git,
		fixture.plan, fixture.integration, fixture.source); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled admission = %v", err)
	}
}

func TestExecutionCheckoutInspectorBoundsRealGitOutputAndIdentity(t *testing.T) {
	fixture := newExecutionCheckoutFixture(t)
	digest, err := executableidentity.Digest(fixture.git)
	if err != nil {
		t.Fatal(err)
	}
	inspection := executionCheckoutInspector{root: fixture.root, git: fixture.git, digest: digest}
	if output, err := inspection.run(t.Context(), 1, "--version"); err == nil || len(output) != 0 {
		t.Fatalf("overflowing real Git output = %q, %v", output, err)
	}
	inspection.digest = "sha256:" + strings.Repeat("0", 64)
	if output, err := inspection.run(t.Context(), 4096, "--version"); err == nil || len(output) != 0 {
		t.Fatalf("changed admitted Git digest = %q, %v", output, err)
	}
}

func TestInspectExecutionCheckoutRenamedRealGitScrubsEnvironment(t *testing.T) {
	fixture := newExecutionCheckoutFixture(t)
	raw, err := os.ReadFile(fixture.git)
	if err != nil {
		t.Fatal(err)
	}
	fixture.git = filepath.Join(t.TempDir(), "admitted-checkout-tool")
	if err := os.WriteFile(fixture.git, raw, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "wrong-repository"))
	t.Setenv("GIT_INDEX_FILE", filepath.Join(t.TempDir(), "wrong-index"))
	if _, err := fixture.inspect(t); err != nil {
		t.Fatalf("renamed real Git inherited ambient controls: %v", err)
	}
}

func TestExecutionCheckoutEntriesRejectsMalformedInventory(t *testing.T) {
	object := strings.Repeat("a", 40)
	valid := "100644 blob " + object + "\ttracked.txt\x00"
	if entries, err := executionCheckoutEntries([]byte(valid)); err != nil || len(entries) != 1 ||
		entries[0] != (executionCheckoutEntry{path: "tracked.txt", object: object, mode: "100644"}) {
		t.Fatalf("exact bounded inventory = %#v, %v", entries, err)
	}
	for _, test := range []struct{ name, raw string }{
		{"empty", ""},
		{"truncated", strings.TrimSuffix(valid, "\x00")},
		{"too many entries", strings.Repeat("\x00", maxCheckoutEntries+1)},
		{"missing path", "100644 blob " + object + "\x00"},
		{"malformed header", "100644  blob " + object + "\ttracked.txt\x00"},
		{"abbreviated object", "100644 blob " + object[:12] + "\ttracked.txt\x00"},
		{"symlink", strings.Replace(valid, "100644", "120000", 1)},
		{"gitlink", "160000 commit " + object + "\ttracked.txt\x00"},
		{"tree", "040000 tree " + object + "\ttracked.txt\x00"},
		{"duplicate", valid + valid},
		{"unsorted", valid + strings.Replace(valid, "tracked.txt", "a.txt", 1)},
		{"parent escape", strings.Replace(valid, "tracked.txt", "../tracked.txt", 1)},
		{"Git metadata", strings.Replace(valid, "tracked.txt", "nested/.GIT/config", 1)},
		{"backslash", strings.Replace(valid, "tracked.txt", "nested\\tracked.txt", 1)},
		{"newline", strings.Replace(valid, "tracked.txt", "tracked\n.txt", 1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := executionCheckoutEntries([]byte(test.raw)); err == nil {
				t.Fatal("malformed checkout tree inventory was admitted")
			}
		})
	}
}

func TestInspectCheckoutFileRejectsFileAndWholeTreeByteOverflow(t *testing.T) {
	path := t.TempDir()
	file, err := os.OpenFile(filepath.Join(path, "large"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxCheckoutFileBytes + 1); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	})
	large := executionCheckoutEntry{path: "large", object: strings.Repeat("a", 40), mode: "100644"}
	if size, err := inspectCheckoutFile(root, large, maxCheckoutBytes); err == nil || size != 0 {
		t.Fatalf("oversized sparse file = %d, %v", size, err)
	}
	raw := []byte("exact\n")
	if err := os.WriteFile(filepath.Join(path, "small"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	small := executionCheckoutEntry{path: "small", object: gitSHA1ObjectID("blob", raw), mode: "100644"}
	if size, err := inspectCheckoutFile(root, small, int64(len(raw))-1); err == nil || size != 0 {
		t.Fatalf("whole-tree remaining byte overflow = %d, %v", size, err)
	}
	if size, err := inspectCheckoutFile(root, small, int64(len(raw))); err != nil || size != int64(len(raw)) {
		t.Fatalf("exact remaining byte allowance = %d, %v", size, err)
	}
}

func newExecutionCheckoutFixture(t *testing.T) executionCheckoutFixture {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "darwin" && git == "/usr/bin/git" {
		// /usr/bin/git is an argv0-sensitive developer-tool shim on macOS.
		// Admit and copy the real selected Git image in the renamed-image test.
		command := exec.CommandContext(t.Context(), "/usr/bin/xcrun", "--find", "git")
		command.Env = []string{"PATH=/usr/bin:/bin", "LC_ALL=C"}
		output, err := command.Output()
		if err != nil {
			t.Fatal(err)
		}
		git = strings.TrimSpace(string(output))
	}
	git, err = filepath.EvalSymlinks(git)
	if err != nil {
		t.Fatal(err)
	}
	git, err = filepath.Abs(git)
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture := executionCheckoutFixture{root: root, git: git}
	fixture.command(t, "init", "--quiet", "--object-format=sha1", "--template=")
	fixture.command(t, "config", "user.name", "Neutral Checkout")
	fixture.command(t, "config", "user.email", "checkout@neutral.invalid")
	fixture.write(t, ".gitignore", "/ignored/\n/ignored_test.go\n")
	fixture.write(t, "tracked.txt", "plan\n")
	fixture.write(t, "nested/tracked.txt", "nested\n")
	fixture.command(t, "add", ".gitignore", "tracked.txt", "nested/tracked.txt")
	fixture.plan = fixture.commit(t, "plan source")
	fixture.write(t, "tracked.txt", "integration\n")
	fixture.command(t, "add", "tracked.txt")
	fixture.integration = fixture.commit(t, "integrated main")
	fixture.write(t, "tracked.txt", "source\n")
	fixture.command(t, "add", "tracked.txt")
	fixture.source = fixture.commit(t, "execution source")
	return fixture
}

func (fixture executionCheckoutFixture) inspect(t *testing.T) (ExecutionCommits, error) {
	t.Helper()
	return InspectExecutionCheckout(t.Context(), fixture.root, fixture.git, fixture.plan, fixture.integration, fixture.source)
}

func (fixture executionCheckoutFixture) write(t *testing.T, relative, content string) {
	t.Helper()
	path := filepath.Join(fixture.root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (fixture executionCheckoutFixture) commit(t *testing.T, message string) string {
	t.Helper()
	fixture.command(t, "commit", "--quiet", "--no-gpg-sign", "-m", message)
	return fixture.command(t, "rev-parse", "HEAD")
}

func (fixture executionCheckoutFixture) command(t *testing.T, arguments ...string) string {
	t.Helper()
	return fixture.commandInput(t, "", arguments...)
}

func (fixture executionCheckoutFixture) commandInput(t *testing.T, input string, arguments ...string) string {
	t.Helper()
	command := exec.CommandContext(t.Context(), fixture.git,
		append([]string{"-c", "core.hooksPath=" + os.DevNull, "-c", "core.fsmonitor=false", "-C", fixture.root}, arguments...)...)
	command.Env = []string{
		"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C", "TZ=UTC",
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_CONFIG_SYSTEM=" + os.DevNull, "GIT_TERMINAL_PROMPT=0",
		"GIT_NO_REPLACE_OBJECTS=1", "GIT_NO_LAZY_FETCH=1",
	}
	command.Stdin = strings.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("fixture Git %q: %v\n%s", arguments, err, output)
	}
	return strings.TrimSpace(string(output))
}
