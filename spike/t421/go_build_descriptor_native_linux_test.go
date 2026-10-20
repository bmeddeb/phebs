//go:build linux && phebs_t42_native

package t421

import (
	"context"
	"debug/buildinfo"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
)

// This is the first native feasibility boundary for the next Go owner, not a
// reference-tool admission or a completed SDK/module/source recipe. In
// particular, the original installed SDK remains available. A real compile
// with VCS stamping must work before a descriptor-only layout can be selected.
func TestLinuxGoBuildRequiredNativeDescriptorComposition(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Fatal("unprivileged protected build required; no skip")
	}
	selectedSDK := os.Getenv("PHEBS_T42_GO_SDK")
	if !executionGitAbsolutePath(selectedSDK) {
		t.Fatal("native prerequisite: explicit PHEBS_T42_GO_SDK required; no skip")
	}
	goRoot, err := filepath.EvalSymlinks(selectedSDK)
	if err != nil || goRoot != selectedSDK {
		t.Fatal("native prerequisite: canonical selected SDK required", err)
	}
	helper := requireLinuxInputTreeHelper(t)
	base, err := os.MkdirTemp("", "phebs-linux-go-descriptor-")
	if err != nil {
		t.Fatal(err)
	}
	parent, work, source, modules := filepath.Join(base, "parent"), filepath.Join(base, "work"), filepath.Join(base, "source"), filepath.Join(base, "modules")
	for _, path := range []string{parent, work, source, modules, filepath.Join(work, "home"), filepath.Join(work, "tmp"), filepath.Join(work, "cache")} {
		if os.Mkdir(path, 0o700) != nil {
			t.Fatal("private fixture directory; retain", base)
		}
	}
	git, err := ProtectLinuxExecutionGit(t.Context(), parent, requireLinuxExternalTool(t, "git"), helper)
	var inputs *LinuxExecutionInputTree
	operationalSafe := true
	t.Cleanup(func() {
		if !operationalSafe {
			t.Errorf("uncertain preparation session; retain all owners and %s", base)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if inputs != nil {
			_ = inputs.Close(ctx)
			if !inputs.closed {
				t.Errorf("uncertain input construction; retain %s", base)
				return
			}
		}
		if git != nil {
			_ = git.Close(ctx)
			if !git.released {
				t.Errorf("uncertain Git construction; retain %s", base)
				return
			}
		}
		if os.RemoveAll(base) != nil {
			t.Error("joined fixture cleanup failed")
		}
	})
	if err != nil {
		t.Fatal("protected Git prerequisite", err)
	}
	runJoined := func(command *exec.Cmd, ctx context.Context, cancel context.CancelFunc) (string, string, error) {
		stdout, stderr := checkoutCommandOutput{remaining: 64 << 10, cancel: cancel}, checkoutCommandOutput{remaining: 64 << 10, cancel: cancel}
		command.Stdout, command.Stderr, command.WaitDelay = &stdout, &stderr, time.Second
		operationalSafe = false
		runErr := runReferenceCommand(ctx, command)
		if command.Process == nil {
			operationalSafe = true // Start failed before any child existed.
		} else if command.ProcessState != nil {
			// An ordinary nonzero build exit is distinct from uncertain drain.
			// Observe the exact recorded session before releasing any owner.
			members, observeErr := t4013.PrivateProcessSessionMembers(command.Process.Pid)
			operationalSafe = observeErr == nil && members == 0
			t.Logf("preparation pid=%d root_joined=true session_members=%d observation_error=%v", command.Process.Pid, members, observeErr)
		}
		return stdout.buffer.String(), stderr.buffer.String(), errors.Join(runErr, stdout.err, stderr.err)
	}
	gitRun := func(input string, args ...string) string {
		t.Helper()
		var output, diagnostic string
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		err := git.WithGit(ctx, work, work, func(_ ExecutionToolIdentity, root *os.File, environment []string) error {
			command := exec.CommandContext(ctx, linuxGitCore+"/git", args...)
			command.ExtraFiles, command.Dir, command.Env = []*os.File{root}, work, environment
			command.Stdin = strings.NewReader(input)
			var err error
			output, diagnostic, err = runJoined(command, ctx, cancel)
			return err
		})
		if err != nil {
			t.Fatal("neutral source preparation", err, diagnostic)
		}
		return output
	}
	files := []struct{ name, content string }{
		{"go.mod", "module neutral.invalid/protected\n\ngo 1.26.5\n"},
		{"main.go", "package main\nimport \"fmt\"\nfunc main() { fmt.Println(\"neutral protected build\") }\n"},
	}
	var stream strings.Builder
	for i, file := range files {
		if os.WriteFile(filepath.Join(source, file.name), []byte(file.content), 0o600) != nil {
			t.Fatal("neutral source write")
		}
		fmt.Fprintf(&stream, "blob\nmark :%d\ndata %d\n%s\n", i+1, len(file.content), file.content)
	}
	stream.WriteString("commit refs/heads/main\ncommitter Neutral <neutral@example.invalid> 1700000000 +0000\ndata 7\nneutral\n")
	for i, file := range files {
		fmt.Fprintf(&stream, "M 100644 :%d %s\n", i+1, file.name)
	}
	stream.WriteString("\ndone\n")
	gitRun("", "init", "--quiet", "--initial-branch=main", source)
	gitRun(stream.String(), "-C", source, "fast-import", "--quiet", "--date-format=raw")
	gitRun("", "-C", source, "reset", "--mixed", "--quiet", "HEAD")
	commit := strings.TrimSpace(gitRun("", "-C", source, "rev-parse", "HEAD"))
	if !validCommit(commit) {
		t.Fatal("neutral commit identity", commit)
	}
	inputs, err = ProtectLinuxExecutionInputTree(t.Context(), parent, []LinuxInputTreeSource{
		{Name: "sdk", Path: goRoot}, {Name: "modules", Path: modules}, {Name: "source", Path: source},
	}, helper)
	if err != nil {
		t.Fatal("protected SDK/module/source tree prerequisite", err)
	}
	if os.RemoveAll(source) != nil || os.Remove(modules) != nil {
		t.Fatal("private original source/module hiding failed")
	}
	identity, err := inputs.Check(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("protected build inputs entries=%d files=%d bytes=%d helper=%s", identity.Entries, identity.Files, identity.Bytes, identity.HelperSHA256)
	output := filepath.Join(work, "neutral")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	err = git.WithGit(ctx, work, work, func(_ ExecutionToolIdentity, gitRoot *os.File, _ []string) error {
		return inputs.WithRoot(ctx, func(buildRoot *os.File) error {
			request := ReferenceToolRequest{GoRoot: "/proc/self/fd/4/sdk", ModuleCache: "/proc/self/fd/4/modules"}
			environment := referenceBuildEnvironment(request, work)
			for i, binding := range environment {
				if strings.HasPrefix(binding, "PATH=") {
					environment[i] = "PATH=" + linuxGitCore
				}
			}
			environment = append(environment, "GIT_EXEC_PATH="+linuxGitCore, "GIT_ALLOW_PROTOCOL=file", "GIT_TEMPLATE_DIR="+os.DevNull, "PWD=/proc/self/fd/4/source")
			command := exec.CommandContext(ctx, "/proc/self/fd/4/sdk/bin/go", "build", "-mod=readonly", "-trimpath", "-buildvcs=true", "-o", output, ".")
			command.ExtraFiles = []*os.File{gitRoot, buildRoot}
			// Chdir happens before descriptor remapping in the pinned Go launcher.
			// The inherited child recipe uses FD4 consistently after exec.
			command.Dir = "/proc/self/fd/" + strconv.FormatUint(uint64(buildRoot.Fd()), 10) + "/source"
			command.Env = environment
			stdout, diagnostic, err := runJoined(command, ctx, cancel)
			if err != nil {
				return fmt.Errorf("protected descriptor build: %w; stdout=%q stderr=%q", err, stdout, diagnostic)
			}
			return nil
		})
	})
	if err != nil {
		t.Fatal("required descriptor compile/VCS feasibility refused", err)
	}
	info, err := buildinfo.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	settings := make(map[string]string)
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	if info.GoVersion != runtime.Version() || settings["vcs.revision"] != commit || settings["vcs.modified"] != "false" {
		t.Fatal("protected build lost exact clean VCS binding", settings)
	}
	command := exec.CommandContext(ctx, output)
	command.Dir, command.Env = work, []string{"LANG=C", "LC_ALL=C"}
	result, diagnostic, err := runJoined(command, ctx, cancel)
	if err != nil || result != "neutral protected build\n" {
		t.Fatal("neutral compiled output", result, diagnostic, err)
	}
}
