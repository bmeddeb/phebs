package main

import (
	"context"
	"debug/buildinfo"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func releaseBuildGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func releaseBuildFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod":             "module example.invalid/releasefixture\n\ngo 1.26\n",
		"cmd/phebs/main.go":  "package main\nimport \"fmt\"\nvar version string\nfunc main() { fmt.Print(version) }\n",
		"history.txt":        "original history\n",
		".gitignore":         "ui/dist/\n",
		"ui/dist/index.html": "fixture UI",
	}
	for name, contents := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	releaseBuildGit(t, root, "init", "--quiet")
	releaseBuildGit(t, root, "add", "go.mod", "cmd/phebs/main.go", "history.txt", ".gitignore")
	releaseBuildGit(t, root, "-c", "user.name=Integration", "-c", "user.email=integration@example.invalid",
		"-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null", "commit", "--quiet", "-m", "fixture")
	return root
}

func runReleaseBuild(t *testing.T, root, output string, extraEnv ...string) (string, error) {
	t.Helper()
	script, err := filepath.Abs("build-release-binary.sh")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", script, output, "v0.2.1")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), extraEnv...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestReleaseBinaryBuildStampsCleanCheckoutAndWorktree(t *testing.T) {
	for _, worktree := range []bool{false, true} {
		name := "standalone"
		if worktree {
			name = "worktree"
		}
		t.Run(name, func(t *testing.T) {
			root := releaseBuildFixture(t)
			commit := releaseBuildGit(t, root, "rev-parse", "HEAD")
			if worktree {
				selected := filepath.Join(t.TempDir(), "selected")
				releaseBuildGit(t, root, "worktree", "add", "--quiet", "--detach", selected, commit)
				root = selected
				if err := os.MkdirAll(filepath.Join(root, "ui/dist"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "ui/dist/index.html"), []byte("fixture UI"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			binary := filepath.Join(t.TempDir(), "phebs")
			out, err := runReleaseBuild(t, root, binary)
			if err != nil {
				t.Fatalf("release build: %v\n%s", err, out)
			}
			info, err := buildinfo.ReadFile(binary)
			if err != nil {
				t.Fatal(err)
			}
			settings := make(map[string]string)
			for _, setting := range info.Settings {
				settings[setting.Key] = setting.Value
			}
			if settings["vcs"] != "git" || settings["vcs.revision"] != commit || settings["vcs.modified"] != "false" {
				t.Fatalf("release build metadata = %v, want clean Git commit %s", settings, commit)
			}
			if status := releaseBuildGit(t, root, "status", "--porcelain"); status != "" {
				t.Fatalf("source checkout changed: %s", status)
			}
			if worktree {
				info, err := os.Stat(filepath.Join(root, ".git"))
				if err != nil || !info.Mode().IsRegular() {
					t.Fatalf("source worktree Git file was replaced: %v", err)
				}
			}
		})
	}
}

func TestReleaseBinaryBuildPreservesDirtyHistoryAndExistingOutput(t *testing.T) {
	root := releaseBuildFixture(t)
	history := []byte("original history\nuser's uncommitted changes\n")
	if err := os.WriteFile(filepath.Join(root, "history.txt"), history, 0o600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "phebs")
	if err := os.WriteFile(binary, []byte("existing output"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runReleaseBuild(t, root, binary)
	if err == nil || !strings.Contains(out, "requires a clean checkout") {
		t.Fatalf("dirty checkout result = %v, %s", err, out)
	}
	for path, want := range map[string]string{filepath.Join(root, "history.txt"): string(history), binary: "existing output"} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("%s changed: %q, %v", path, got, err)
		}
	}
}

func TestReleaseBinaryBuildRejectsMissingMetadata(t *testing.T) {
	root := releaseBuildFixture(t)
	realGo, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	shim := t.TempDir()
	// Simulate a misconfigured compiler invocation with a real unstamped image.
	// The metadata reader remains the real Go command.
	program := "#!/bin/sh\nif [ \"$1\" = build ]; then shift; shift; exec \"$REAL_GO\" build -buildvcs=false \"$@\"; fi\nexec \"$REAL_GO\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(shim, "go"), []byte(program), 0o700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "phebs")
	out, err := runReleaseBuild(t, root, binary, "PATH="+shim+string(os.PathListSeparator)+os.Getenv("PATH"), "REAL_GO="+realGo)
	if err == nil || !strings.Contains(out, "lacks exact clean Git build metadata") {
		t.Fatalf("unstamped image result = %v, %s", err, out)
	}
	if _, err := os.Stat(binary); !os.IsNotExist(err) {
		t.Fatalf("unverified image was published: %v", err)
	}
}
