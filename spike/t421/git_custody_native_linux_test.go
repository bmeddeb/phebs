//go:build linux && phebs_t42_native

package t421

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLinuxGitCustodyRequiredNativeLocalTransport(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Fatal("required unprivileged Git consumer proof; no skip")
	}
	helper := requireLinuxInputTreeHelper(t)
	base, err := os.MkdirTemp("", "phebs-linux-git-native-")
	if err != nil {
		t.Fatal(err)
	}
	parent, work := filepath.Join(base, "parent"), filepath.Join(base, "work")
	for _, path := range []string{parent, work} {
		if os.Mkdir(path, 0o700) != nil {
			t.Fatal("native fixture directory; retained", base)
		}
	}
	// Remove only a private test-owned source after protection, never installed
	// Git. The fixed child PATH/exec-path and actual local transport below must
	// continue resolving aliases through the inherited protected descriptor.
	source := filepath.Join(base, "selected-git")
	installed, err := os.Open(requireLinuxExternalTool(t, "git"))
	if err != nil {
		t.Fatal(err)
	}
	writer, err := os.OpenFile(source, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		_ = installed.Close()
		t.Fatal(err)
	}
	_, copyErr := io.Copy(writer, installed)
	writerErr, sourceErr := writer.Close(), installed.Close()
	if copyErr != nil || writerErr != nil || sourceErr != nil {
		t.Fatal("native source copy failed", base)
	}
	operationalSafe := true
	git, err := ProtectLinuxExecutionGit(t.Context(), parent, source, helper)
	if git != nil {
		t.Cleanup(func() {
			if !operationalSafe {
				t.Errorf("uncertain operational session; retain Git owner and fixture %s", base)
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = git.Close(ctx)
			if !git.released {
				t.Errorf("uncertain Git custody retained; do not remove %s", base)
				return
			}
			if os.RemoveAll(base) != nil {
				t.Error("native fixture removal failed")
			}
		})
	}
	if err != nil {
		t.Fatal("required protected Git construction refused; retained", base, err)
	}
	identity, err := git.Check(t.Context())
	if err != nil || identity.Role != "git" || identity.Provenance != "external-readonly-git-tree-linux-amd64-v1" {
		t.Fatal("protected identity", identity, err)
	}
	treeIdentity, err := git.tree.Check(t.Context())
	if err != nil || treeIdentity.Files != 7 || treeIdentity.Entries != 8 || treeIdentity.Bytes == 0 || len(git.aliases) != 0 {
		t.Fatal("protected alias inventory or mutable staging release", treeIdentity, err)
	}
	if os.Remove(source) != nil {
		t.Fatal("private source removal")
	}
	t.Setenv("GIT_EXEC_PATH", "/not-admitted-git")
	t.Setenv("GIT_DIR", "/not-admitted-repo")
	t.Setenv("GIT_SSH_COMMAND", "/not-admitted-transport")
	var trace strings.Builder
	run := func(input string, args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		var result string
		err := git.WithGit(ctx, work, work, func(actual ExecutionToolIdentity, root *os.File, environment []string) error {
			if actual != identity {
				return errors.New("loan identity drift")
			}
			values := make(map[string]string)
			for _, binding := range environment {
				key, value, ok := strings.Cut(binding, "=")
				if _, exists := values[key]; !ok || exists {
					return errors.New("duplicate/malformed environment")
				}
				values[key] = value
			}
			if values["PATH"] != linuxGitCore || values["GIT_EXEC_PATH"] != linuxGitCore || values["GIT_ALLOW_PROTOCOL"] != "file" ||
				values["GIT_DIR"] != "" || values["GIT_SSH_COMMAND"] != "" {
				return errors.New("ambient routing or lost protected routing")
			}
			command := exec.CommandContext(ctx, linuxGitCore+"/git", args...)
			command.ExtraFiles, command.Dir = []*os.File{root}, work
			// Test-only bounded Git trace observes real helper routing; the
			// production loan still supplies the exact closed environment.
			command.Env = append(environment, "GIT_TRACE=1")
			command.Stdin = strings.NewReader(input)
			stdout, stderr := checkoutCommandOutput{remaining: 16 << 10, cancel: cancel}, checkoutCommandOutput{remaining: 16 << 10, cancel: cancel}
			command.Stdout, command.Stderr, command.WaitDelay = &stdout, &stderr, time.Second
			operationalSafe = false
			err := runReferenceCommand(ctx, command) // Includes actual Wait and session drain.
			if err == nil {
				operationalSafe = true
			} else if command.Process != nil {
				t.Logf("retained operational session pid=%d root_joined=%t", command.Process.Pid, command.ProcessState != nil)
			}
			trace.WriteString(stderr.buffer.String())
			if err != nil || stdout.err != nil || stderr.err != nil {
				return fmt.Errorf("neutral Git command: %w; %s", errors.Join(err, stdout.err, stderr.err), stderr.buffer.String())
			}
			result = stdout.buffer.String()
			return nil
		})
		if err != nil {
			t.Fatal("protected neutral Git failed", err)
		}
		return result
	}
	bare, mirror := filepath.Join(work, "source.git"), filepath.Join(work, "mirror.git")
	run("", "init", "--bare", "--initial-branch=main", bare)
	importCommit := func(content, from string) {
		parent := ""
		if from != "" {
			parent = "from " + from + "\n"
		}
		stream := fmt.Sprintf("blob\nmark :1\ndata %d\n%s\ncommit refs/heads/main\ncommitter Neutral <neutral@example.invalid> 1700000000 +0000\ndata 7\nneutral\n%sM 100644 :1 file.txt\n\ndone\n", len(content), content, parent)
		run(stream, "-C", bare, "fast-import", "--quiet", "--date-format=raw")
	}
	importCommit("first\n", "")
	first := strings.TrimSpace(run("", "-C", bare, "rev-parse", "HEAD"))
	if !validCommit(first) {
		t.Fatal("first commit oracle", first)
	}
	fileURL := (&url.URL{Scheme: "file", Path: bare}).String()
	run("", "clone", "--mirror", fileURL, mirror)
	if got := strings.TrimSpace(run("", "-C", mirror, "rev-parse", "HEAD")); got != first {
		t.Fatal("clone changed commit", got, first)
	}
	importCommit("second\n", first)
	second := strings.TrimSpace(run("", "-C", bare, "rev-parse", "HEAD"))
	if !validCommit(second) || second == first {
		t.Fatal("second commit oracle", second)
	}
	run("", "-C", mirror, "fetch", "--prune", "origin")
	if got := strings.TrimSpace(run("", "-C", mirror, "rev-parse", "HEAD")); got != second {
		t.Fatal("fetch changed commit", got, second)
	}
	if got := run("", "-C", mirror, "cat-file", "blob", "HEAD:file.txt"); got != "second\n" {
		t.Fatal("protected content", got)
	}
	if !strings.Contains(trace.String(), linuxGitCore+"/git-upload-pack") || !strings.Contains(trace.String(), "pack-objects") {
		t.Fatal("actual local transport/pack routing was not observed", trace.String())
	}
	for _, key := range []string{"gc.auto", "maintenance.auto"} {
		if got := run("", "-C", mirror, "config", "--default", "not-configured", "--get", key); got != "not-configured\n" {
			t.Fatal("test altered Git policy", key, got)
		}
	}
	callbackErr := errors.New("neutral callback failure")
	if err := git.WithGit(t.Context(), work, work, func(ExecutionToolIdentity, *os.File, []string) error { return callbackErr }); err != callbackErr {
		t.Fatal("callback error changed", err)
	}
	if _, err := git.Check(t.Context()); err != nil {
		t.Fatal("callback error poisoned owner", err)
	}
	if git.WithGit(t.Context(), work, work, func(_ ExecutionToolIdentity, root *os.File, _ []string) error { return root.Close() }) != ErrExecutionGitCustody {
		t.Fatal("closed loan stayed usable")
	}
	if _, err := git.Check(t.Context()); err != ErrExecutionGitCustody {
		t.Fatal("unsafe loan refusal was not sticky", err)
	}
	if git.Close(t.Context()) != ErrExecutionGitCustody || !git.released {
		t.Fatal("sticky error concealed completed cleanup")
	}
	if _, err := os.Lstat(git.PrivateDirectory()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("owned scratch survived exact cleanup", err)
	}
	t.Logf("native protected Git local clone/fetch passed: files=%d bytes=%d; closed-loan sticky refusal and exact cleanup passed", treeIdentity.Files, treeIdentity.Bytes)
}
