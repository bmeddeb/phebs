//go:build darwin

package t421

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
)

func TestExecutionGitCustodyProtectedAuthorAndLocalTransport(t *testing.T) {
	requireExternalToolFrozenHost(t)
	fixture := newExecutionCheckoutFixture(t)
	parent, _ := inputCustodyTestFixture(t)
	before := inputCustodyTestStat(t, fixture.git)
	alias := filepath.Join(filepath.Dir(parent), "selected-git")
	if err := os.Symlink(fixture.git, alias); err != nil {
		t.Fatal(err)
	}
	git := gitCustodyTestProtect(t, t.Context(), parent, alias)
	identity, path, err := git.Check(t.Context())
	if err != nil || identity.Role != "git" || path != filepath.Join(git.Directory(), "git") {
		t.Fatalf("actual protected Git: %#v, %q, %v", identity, path, err)
	}
	if !inputCustodySame(before, inputCustodyTestStat(t, fixture.git)) {
		t.Fatal("admission changed the installed Git image")
	}
	entries, err := os.ReadDir(git.Directory())
	names := executionGitImageNames()
	if err != nil || len(entries) != len(names) {
		t.Fatalf("protected helper inventory: %d, %v", len(entries), err)
	}
	for _, name := range names {
		protected := filepath.Join(git.Directory(), name)
		info := inputCustodyTestStat(t, protected)
		digest, err := t4013.DigestHostExecutable(t.Context(), protected)
		if err != nil || digest != identity.SHA256 || !inputCustodyProtected(info) || os.SameFile(before, info) {
			t.Fatalf("helper %s is not an independently protected matching regular image: %v", name, err)
		}
	}
	inputCustodyTestReadOnlyDescriptors(t, git.input)
	for name, value := range map[string]string{
		"GIT_EXEC_PATH": "/private/not-admitted-core", "GIT_DIR": "/private/not-admitted-repository",
		"GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "core.hooksPath", "GIT_CONFIG_VALUE_0": "/private/not-admitted-hooks",
		"GIT_SSH_COMMAND": "/private/not-admitted-transport", "GIT_TEMPLATE_DIR": "/private/not-admitted-templates",
	} {
		t.Setenv(name, value)
	}
	environment, err := git.Environment(t.Context(), parent, parent)
	if err != nil {
		t.Fatal(err)
	}
	values := make(map[string]string, len(environment))
	for _, entry := range environment {
		name, value, ok := strings.Cut(entry, "=")
		if _, duplicate := values[name]; !ok || duplicate {
			t.Fatal("Git environment contains a malformed or duplicate binding")
		}
		values[name] = value
	}
	if values["PATH"] != git.Directory() || values["GIT_EXEC_PATH"] != git.Directory() ||
		values["GIT_DIR"] != "" || values["GIT_SSH_COMMAND"] != "" || values["GIT_ALLOW_PROTOCOL"] != "file" {
		t.Fatal("Git environment retained ambient routing or lost protected routing")
	}
	environment[0] = "caller-changed-value"
	again, err := git.Environment(t.Context(), parent, parent)
	if err != nil || len(again) == 0 || again[0] == environment[0] {
		t.Fatal("returned environment aliases mutable custody state")
	}
	if output := gitCustodyTestRun(t, git, parent, nil, "--exec-path"); string(output) != git.Directory()+"\n" {
		t.Fatalf("protected exec directory: %q", output)
	}
	source := filepath.Join(parent, "source.git")
	gitCustodyTestRun(t, git, parent, nil, "init", "--bare", "--initial-branch=main", source)
	gitCustodyTestRun(t, git, parent, strings.NewReader(gitCustodyTestImport("first\n", "")),
		"-C", source, "fast-import", "--quiet", "--date-format=raw")
	first := gitCustodyTestRevision(t, git, parent, source)
	fileURL := (&url.URL{Scheme: "file", Path: source}).String()
	mirror := filepath.Join(parent, "mirror.git")
	gitCustodyTestRun(t, git, parent, nil, "clone", "--mirror", fileURL, mirror)
	if got := gitCustodyTestRevision(t, git, parent, mirror); got != first {
		t.Fatal("protected local clone changed author commit/tree authority")
	}
	gitCustodyTestObjectPosture(t, git, parent, mirror)
	gitCustodyTestRun(t, git, parent, strings.NewReader(gitCustodyTestImport("second\n", first[0])),
		"-C", source, "fast-import", "--quiet", "--date-format=raw")
	second := gitCustodyTestRevision(t, git, parent, source)
	if second[0] == first[0] || second[1] == first[1] {
		t.Fatal("second author revision did not change exact commit/tree authority")
	}
	gitCustodyTestRun(t, git, parent, nil, "-C", mirror, "remote", "set-url", "origin", fileURL)
	gitCustodyTestRun(t, git, parent, nil, "-C", mirror, "fetch", "--prune", "origin")
	if got := gitCustodyTestRevision(t, git, parent, mirror); got != second {
		t.Fatal("protected local fetch changed author commit/tree authority")
	}
	gitCustodyTestObjectPosture(t, git, parent, mirror)
	if output := gitCustodyTestRun(t, git, parent, nil, "-C", mirror, "cat-file", "blob", "HEAD:file.txt"); string(output) != "second\n" {
		t.Fatalf("protected reader returned unexpected content: %q", output)
	}
	// No GC setting or fetch flag was changed to make this tiny rehearsal pass.
	for _, key := range []string{"gc.auto", "maintenance.auto"} {
		output := gitCustodyTestRun(t, git, parent, nil, "-C", mirror, "config", "--default", "not-configured", "--get", key)
		if string(output) != "not-configured\n" {
			t.Fatalf("rehearsal changed %s: %q", key, output)
		}
	}
}

func TestExecutionGitCustodyAliasProofRequiresEveryActualImage(t *testing.T) {
	requireExternalToolFrozenHost(t)
	fixture := newExecutionCheckoutFixture(t)
	digest, err := t4013.DigestHostExecutable(t.Context(), fixture.git)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"matching aliases", "missing alias", "directory alias", "unrelated image", "canceled"} {
		t.Run(name, func(t *testing.T) {
			core, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			for _, alias := range executionGitImageNames() {
				if err := os.Symlink(fixture.git, filepath.Join(core, alias)); err != nil {
					t.Fatal(err)
				}
			}
			selected := filepath.Join(core, "git-pack-objects")
			ctx := t.Context()
			switch name {
			case "missing alias", "directory alias", "unrelated image":
				if err := os.Remove(selected); err != nil {
					t.Fatal(err)
				}
				switch name {
				case "directory alias":
					if err := os.Mkdir(selected, 0o700); err != nil {
						t.Fatal(err)
					}
				case "unrelated image":
					if err := os.Symlink("/usr/bin/true", selected); err != nil {
						t.Fatal(err)
					}
				}
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			selections, err := executionGitSelections(ctx, core, digest)
			if name != "matching aliases" {
				if err != ErrExecutionGitCustody || selections != nil {
					t.Fatalf("invalid alias set admitted: %#v, %v", selections, err)
				}
				return
			}
			if err != nil || len(selections) != 7 {
				t.Fatalf("actual matching aliases refused: %v", err)
			}
			for index, alias := range executionGitImageNames() {
				if selections[index] != (ExecutionInputCopy{Name: alias, Path: fixture.git, SHA256: digest, Executable: true}) {
					t.Fatalf("alias %s not bound to its actual resolved image", alias)
				}
			}
		})
	}
}

func TestExecutionGitCustodyRefusesAndRetainsFailedCopies(t *testing.T) {
	requireExternalToolFrozenHost(t)
	fixture := newExecutionCheckoutFixture(t)
	for _, name := range []string{"helper drift", "closed", "canceled check", "invalid environment root", "copy cancellation"} {
		t.Run(name, func(t *testing.T) {
			parent, _ := inputCustodyTestFixture(t)
			if name == "copy cancellation" {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				observed := gitCustodyTestCancelAfterCopy(ctx, parent, cancel)
				git, err := ProtectExecutionGit(ctx, parent, fixture.git)
				gitCustodyTestCleanup(t, git)
				if !<-observed {
					t.Fatal("copy publication cancellation boundary was not observed")
				}
				if err != ErrExecutionGitCustody || git == nil || git.identity != (ExecutionToolIdentity{}) {
					t.Fatalf("canceled copy lost retained custody or exposed identity: %#v, %v", git, err)
				}
				inputCustodyTestClosed(t, git.input)
				identity, path, err := git.Check(t.Context())
				gitCustodyTestRefusal(t, identity, path, err)
				return
			}
			git := gitCustodyTestProtect(t, t.Context(), parent, fixture.git)
			ctx := t.Context()
			switch name {
			case "helper drift":
				if err := inputCustodyFlag(git.input.inputs["git-upload-pack"].file, false); err != nil {
					t.Fatal(err)
				}
			case "closed":
				if err := git.Close(); err != nil {
					t.Fatal(err)
				}
				inputCustodyTestClosed(t, git.input)
			case "canceled check":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "invalid environment root":
				if environment, err := git.Environment(ctx, "relative", parent); err != ErrExecutionGitCustody || environment != nil {
					t.Fatalf("unadmitted mutable environment root accepted: %#v, %v", environment, err)
				}
			}
			identity, path, err := git.Check(ctx)
			gitCustodyTestRefusal(t, identity, path, err)
			if name == "helper drift" {
				if err := inputCustodyFlag(git.input.inputs["git-upload-pack"].file, true); err != nil {
					t.Fatal(err)
				}
			}
			identity, path, err = git.Check(t.Context())
			gitCustodyTestRefusal(t, identity, path, err)
			if err := git.Close(); err != ErrExecutionGitCustody {
				t.Fatalf("failed custody did not remain sticky: %v", err)
			}
			inputCustodyTestClosed(t, git.input)
			if !inputCustodyProtected(inputCustodyTestStat(t, git.Directory())) {
				t.Fatal("failure thawed or removed protected directory")
			}
		})
	}
}

func TestExecutionGitCustodyCommandCancellationDrainsOwnedGit(t *testing.T) {
	requireExternalToolFrozenHost(t)
	fixture := newExecutionCheckoutFixture(t)
	parent, _ := inputCustodyTestFixture(t)
	git := gitCustodyTestProtect(t, t.Context(), parent, fixture.git)
	source := filepath.Join(parent, "source.git")
	gitCustodyTestRun(t, git, parent, nil, "init", "--bare", "--initial-branch=main", source)
	input, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = input.Close(); _ = writer.Close() })
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	command := gitCustodyTestCommand(t, git, ctx, parent, input, "-C", source, "fast-import", "--quiet", "--date-format=raw")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	// An open stdin keeps real fast-import owned and waiting; no sleep or mock
	// child is needed to exercise cooperative process-group cancellation.
	cancel()
	if err := command.Wait(); err == nil || !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("canceled real Git unexpectedly completed: %v", err)
	}
	if command.ProcessState == nil {
		t.Fatal("canceled real Git was not reaped")
	}
	if _, _, err := git.Check(t.Context()); err != nil {
		t.Fatalf("command cancellation changed immutable resources: %v", err)
	}
}

func TestExecutionGitCustodyPreflightAndZeroValueRefusal(t *testing.T) {
	requireExternalToolFrozenHost(t)
	parent, _ := inputCustodyTestFixture(t)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, test := range []struct {
		name, parent, binary string
		ctx                  context.Context
	}{
		{"nil context", parent, "/usr/bin/git", nil},
		{"canceled", parent, "/usr/bin/git", canceled},
		{"relative parent", "parent", "/usr/bin/git", t.Context()},
		{"unclean parent", parent + "/.", "/usr/bin/git", t.Context()},
		{"relative image", parent, "git", t.Context()},
		{"wrong native tool", parent, "/usr/bin/true", t.Context()},
		{"missing image", parent, filepath.Join(parent, "absent"), t.Context()},
	} {
		t.Run(test.name, func(t *testing.T) {
			git, err := ProtectExecutionGit(test.ctx, test.parent, test.binary)
			gitCustodyTestCleanup(t, git)
			if git != nil || err != ErrExecutionGitCustody {
				t.Fatalf("invalid request created custody: %#v, %v", git, err)
			}
			if entries, err := os.ReadDir(parent); err != nil || len(entries) != 0 {
				t.Fatalf("invalid request mutated parent: %v", err)
			}
		})
	}
	for _, git := range []*ExecutionGitCustody{nil, {}} {
		identity, path, err := git.Check(t.Context())
		gitCustodyTestRefusal(t, identity, path, err)
		if git.Directory() != "" || git.Close() != nil {
			t.Fatal("zero custody invented ownership")
		}
		if environment, err := git.Environment(t.Context(), parent, parent); environment != nil || err != ErrExecutionGitCustody {
			t.Fatal("zero custody invented a protected environment")
		}
	}
}

func TestExecutionGitRequiredBuiltinInventory(t *testing.T) {
	valid := "upload-pack\npack-objects\nindex-pack\nunpack-objects\nrev-list\nmaintenance\ninit\nfast-import\nrev-parse\nls-tree\nclone\nfetch"
	for _, test := range []struct {
		name, raw string
		valid     bool
	}{
		{"exact required subset", valid, true},
		{"missing unpack alternative", strings.ReplaceAll(valid, "unpack-objects\n", ""), false},
		{"missing author builtin", strings.ReplaceAll(valid, "fast-import\n", ""), false},
		{"duplicate", valid + "\nfetch", false},
		{"malformed", valid + "\nnot a builtin", false},
		{"overcount", strings.Repeat("x\n", 513), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := executionGitRequiredBuiltins(test.raw); got != test.valid {
				t.Fatalf("builtin inventory acceptance=%t, want %t", got, test.valid)
			}
		})
	}
}

func gitCustodyTestProtect(t *testing.T, ctx context.Context, parent, binary string) *ExecutionGitCustody {
	t.Helper()
	git, err := ProtectExecutionGit(ctx, parent, binary)
	gitCustodyTestCleanup(t, git)
	if err != nil || git == nil {
		t.Fatalf("actual Git resource admission: %v", err)
	}
	return git
}

func gitCustodyTestCancelAfterCopy(ctx context.Context, parent string, cancel context.CancelFunc) <-chan bool {
	observed := make(chan bool, 1)
	go func() {
		defer close(observed)
		tick, deadline := time.NewTicker(time.Millisecond), time.NewTimer(5*time.Second)
		defer tick.Stop()
		defer deadline.Stop()
		for {
			select {
			case <-ctx.Done():
				observed <- false
				return
			case <-deadline.C:
				cancel()
				observed <- false
				return
			case <-tick.C:
				entries, err := os.ReadDir(parent)
				if err != nil || len(entries) != 1 {
					continue
				}
				info, err := os.Lstat(filepath.Join(parent, entries[0].Name(), "git"))
				if err == nil && info.Size() > 0 {
					cancel()
					observed <- true
					return
				}
			}
		}
	}()
	return observed
}

func gitCustodyTestCleanup(t *testing.T, git *ExecutionGitCustody) {
	t.Helper()
	if git == nil || git.input == nil {
		return
	}
	var inputs []ExecutionInputCopy
	for _, name := range executionGitImageNames() {
		inputs = append(inputs, ExecutionInputCopy{Name: name})
	}
	inputCustodyTestCleanup(t, git.input, inputs)
}

func gitCustodyTestRefusal(t *testing.T, identity ExecutionToolIdentity, path string, err error) {
	t.Helper()
	if identity != (ExecutionToolIdentity{}) || path != "" || err != ErrExecutionGitCustody {
		t.Fatalf("refusal exposed identity/path/nonclosed error: %#v, %q, %v", identity, path, err)
	}
}

func gitCustodyTestCommand(t *testing.T, git *ExecutionGitCustody, ctx context.Context, root string, input io.Reader, args ...string) *exec.Cmd {
	t.Helper()
	_, binary, err := git.Check(ctx)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := git.Environment(ctx, root, root)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, binary, args...)
	command.Dir, command.Env, command.Stdin = root, environment, input
	command.WaitDelay = time.Second
	if err := prepareReferenceCommand(command); err != nil {
		t.Fatal(err)
	}
	return command
}

func gitCustodyTestRun(t *testing.T, git *ExecutionGitCustody, root string, input io.Reader, args ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	command := gitCustodyTestCommand(t, git, ctx, root, input, args...)
	output, diagnostic := checkoutCommandOutput{remaining: 16 << 10, cancel: cancel}, checkoutCommandOutput{remaining: 16 << 10, cancel: cancel}
	command.Stdout, command.Stderr = &output, &diagnostic
	if err := command.Run(); err != nil {
		t.Fatalf("protected neutral Git command failed: %v; %s", err, diagnostic.buffer.String())
	}
	return bytes.Clone(output.buffer.Bytes())
}

func gitCustodyTestImport(content, parent string) string {
	from := ""
	if parent != "" {
		from = "from " + parent + "\n"
	}
	return fmt.Sprintf("blob\nmark :1\ndata %d\n%s\ncommit refs/heads/main\ncommitter Neutral <neutral@example.invalid> 1700000000 +0000\ndata 7\nneutral\n%sM 100644 :1 file.txt\n\ndone\n", len(content), content, from)
}

func gitCustodyTestRevision(t *testing.T, git *ExecutionGitCustody, root, repository string) [2]string {
	t.Helper()
	output := gitCustodyTestRun(t, git, root, nil, "-C", repository, "rev-parse", "HEAD", "HEAD^{tree}")
	fields := strings.Fields(string(output))
	if len(fields) != 2 || !validCommit(fields[0]) || !validCommit(fields[1]) {
		t.Fatalf("actual author revision identity: %q", output)
	}
	inventory := gitCustodyTestRun(t, git, root, nil, "-C", repository, "ls-tree", "-rz", "--full-tree", "HEAD")
	if !bytes.HasSuffix(inventory, []byte("\tfile.txt\x00")) || bytes.Count(inventory, []byte{0}) != 1 {
		t.Fatalf("actual author inventory: %q", inventory)
	}
	return [2]string{fields[0], fields[1]}
}

func gitCustodyTestObjectPosture(t *testing.T, git *ExecutionGitCustody, root, repository string) {
	t.Helper()
	output := gitCustodyTestRun(t, git, root, nil, "-C", repository, "count-objects", "-v")
	counts := make(map[string]uint64)
	for line := range strings.SplitSeq(strings.TrimSpace(string(output)), "\n") {
		key, value, found := strings.Cut(line, ": ")
		count, err := strconv.ParseUint(value, 10, 64)
		if !found || err != nil {
			t.Fatalf("object posture unavailable: %q", output)
		}
		counts[key] = count
	}
	if _, ok := counts["count"]; !ok {
		t.Fatal("loose object posture missing")
	}
	if _, ok := counts["in-pack"]; !ok || counts["count"]+counts["in-pack"] == 0 {
		t.Fatal("packed object posture missing or repository empty")
	}
	t.Logf("actual object posture: loose=%d packed=%d packs=%d", counts["count"], counts["in-pack"], counts["packs"])
}
