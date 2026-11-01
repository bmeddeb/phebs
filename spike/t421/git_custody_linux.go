//go:build linux

package t421

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

const linuxGitCore = "/proc/self/fd/3/core"

// LinuxExecutionGitCustody owns seven matching regular Git/core images in one
// H2k read-only tree. It is distinct from the Darwin immutable-path owner.
// This grants no command/repository admission, shell/library/helper closure,
// executor descendant-drain proof or ceremony permission.
type LinuxExecutionGitCustody struct {
	mu          sync.Mutex // Lock order: Git owner, then tree; entire joined use.
	tree        *LinuxExecutionInputTree
	sealed      *LinuxExecutionInputCustody // Retained until all construction probes drain.
	identity    ExecutionToolIdentity
	directory   string
	stage       *os.Root
	stageInfo   os.FileInfo
	children    map[string]os.FileInfo
	aliases     map[string]os.FileInfo
	probe       *linuxExecutionToolProbe // At most one uncertain joined-probe prefix.
	closing     bool
	released    bool
	err         error
	closeErr    error // Native close failure, separate from sticky operation refusal.
	observation *executionPreworkObservation
}

// ProtectLinuxExecutionGit verifies the selected actual core and six matching
// installed aliases, seals one core, materializes seven bounded regular copies,
// and protects them using the independently verified helper. It hashes every
// protected alias and probes the protected version, exec-path and builtins
// before publishing identity. Trusted source ownership excludes concurrent
// writers during observation/copying. No installed file is changed.
//
// Construction is serial, cooperatively bounded to thirty minutes. Errors after
// scratch creation return retained cleanup custody, never a usable identity.
// Close must complete before the caller disposes any retained private prefix.
func ProtectLinuxExecutionGit(ctx context.Context, parent, binary string, helper *LinuxInputTreeHelper) (*LinuxExecutionGitCustody, error) {
	return protectLinuxExecutionGitObserved(ctx, parent, binary, helper, nil)
}

func protectLinuxExecutionGitObserved(ctx context.Context, parent, binary string, helper *LinuxInputTreeHelper, observation *executionPreworkObservation) (*LinuxExecutionGitCustody, error) {
	if ctx == nil || ctx.Err() != nil || helper == nil || helper.tool == nil ||
		!linuxGitPrivateDirectory(parent) || !executionGitAbsolutePath(binary) {
		return nil, ErrExecutionGitCustody
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	binary, digest, err := admitExternalDelegationImage(ctx, binary)
	if err != nil {
		return nil, ErrExecutionGitCustody
	}
	sealed, err := protectLinuxExecutionInputsObserved(ctx, []ExecutionInputCopy{{Name: "git", Path: binary, SHA256: digest, Executable: true}}, observation)
	if err != nil {
		return nil, ErrExecutionGitCustody
	}
	directory, err := os.MkdirTemp(parent, "t42-linux-git-")
	if err != nil {
		_ = sealed.Close() // No child has started.
		return nil, ErrExecutionGitCustody
	}
	git := &LinuxExecutionGitCustody{sealed: sealed, directory: directory, aliases: make(map[string]os.FileInfo), children: make(map[string]os.FileInfo), observation: observation}
	fail := func() (*LinuxExecutionGitCustody, error) { git.err = ErrExecutionGitCustody; return git, git.err }
	git.stage, err = os.OpenRoot(directory)
	if err == nil {
		git.stageInfo, err = git.stage.Stat(".")
	}
	if err != nil {
		return fail()
	}
	for _, name := range []string{"core", "scratch"} {
		if git.stage.Mkdir(name, 0o700) != nil {
			return fail()
		}
		git.children[name], err = git.stage.Stat(name)
		if err != nil {
			return fail()
		}
	}
	var version, core string
	err = sealed.WithInput(ctx, "git", func(image *os.File) error {
		var err error
		environment := externalToolEnvironment(directory)
		version, err = git.probeGit(ctx, image, "/proc/self/fd/3", environment, "--version")
		if err != nil || !validPublicToolVersion(version) || !strings.HasPrefix(version, "git version ") || len(strings.TrimPrefix(version, "git version ")) == 0 {
			return ErrExecutionGitCustody
		}
		output, err := git.probeGit(ctx, image, "/proc/self/fd/3", environment, "--exec-path")
		if err != nil {
			return ErrExecutionGitCustody
		}
		core, err = admitExternalDelegationDirectory(output)
		return err
	})
	if err != nil {
		return fail()
	}
	identity := ExecutionToolIdentity{Role: "git", SHA256: digest, FileType: regularFileType, Version: version}
	selections, err := executionGitSelections(ctx, core, digest)
	if err != nil {
		return fail()
	}
	for _, name := range executionGitImageNames() {
		if sealed.WithInput(ctx, "git", func(image *os.File) error { return git.copyAlias(ctx, name, image) }) != nil {
			return fail()
		}
	}
	git.tree, err = protectLinuxExecutionInputTreeObserved(ctx, filepath.Join(directory, "scratch"),
		[]LinuxInputTreeSource{{Name: "core", Path: filepath.Join(directory, "core")}}, helper, observation)
	if err != nil {
		return fail()
	}
	err = git.tree.WithRoot(ctx, func(root *os.File) error {
		for _, name := range executionGitImageNames() {
			if verifyLinuxGitAliasObserved(ctx, root, name, identity.SHA256, observation) != nil {
				return ErrExecutionGitCustody
			}
		}
		environment := executionGitEnvironment(linuxGitCore, directory, directory)
		for _, probe := range []struct{ argument, expected string }{{"--version", identity.Version}, {"--exec-path", linuxGitCore}, {"--list-cmds=builtins", ""}} {
			output, err := git.probeGit(ctx, root, linuxGitCore+"/git", environment, probe.argument)
			if err != nil || probe.expected != "" && output != probe.expected || probe.expected == "" && !executionGitRequiredBuiltins(output) {
				return ErrExecutionGitCustody
			}
		}
		return nil
	})
	if err != nil {
		return fail()
	}
	actual, err := t4013.DigestHostExecutable(ctx, binary)
	if err != nil || actual != digest {
		return fail()
	}
	after, err := executionGitSelections(ctx, core, identity.SHA256)
	if err != nil || !slices.Equal(selections, after) || git.removeAliases() != nil || sealed.Close() != nil || ctx.Err() != nil {
		return fail()
	}
	git.sealed = nil
	identity.Provenance = "external-readonly-git-tree-linux-amd64-v1"
	git.identity = identity
	return git, nil
}

func linuxGitPrivateDirectory(path string) bool {
	if !executionGitAbsolutePath(path) || strings.ContainsAny(path, " \t\\") {
		return false
	}
	canonical, err := filepath.EvalSymlinks(path)
	info, statErr := os.Lstat(path)
	return err == nil && canonical == path && statErr == nil && info.IsDir() && info.Mode().Perm() == 0o700 && inputCustodyCopyOwned(info)
}

func (git *LinuxExecutionGitCustody) copyAlias(ctx context.Context, name string, image *os.File) error {
	path := "core/" + name
	file, err := git.stage.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return ErrExecutionGitCustody
	}
	source, statErr := image.Stat()
	written, copyErr := io.Copy(file, executionInputReader{ctx, io.LimitReader(image, maxInputCustodyFileBytes+1)})
	chmodErr, syncErr := file.Chmod(0o500), file.Sync()
	git.aliases[name], err = file.Stat()
	closeErr := git.observation.close(file.Close())
	if statErr != nil || copyErr != nil || err != nil || closeErr != nil || chmodErr != nil || syncErr != nil ||
		written <= 0 || written > maxInputCustodyFileBytes || source == nil || written != source.Size() || ctx.Err() != nil {
		return ErrExecutionGitCustody
	}
	return nil
}

func verifyLinuxGitAliasObserved(ctx context.Context, root *os.File, name, digest string, observation *executionPreworkObservation) error {
	fd, err := unix.Openat(int(root.Fd()), "core/"+name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return ErrExecutionGitCustody
	}
	file := os.NewFile(uintptr(fd), "protected-git-alias")
	info, statErr := file.Stat()
	hash := sha256.New()
	written, readErr := io.Copy(hash, executionInputReader{ctx, io.LimitReader(file, maxInputCustodyFileBytes+1)})
	closeErr := observation.close(file.Close())
	if statErr != nil || info == nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o500 ||
		written <= 0 || written > maxInputCustodyFileBytes || written != info.Size() || readErr != nil || closeErr != nil ||
		"sha256:"+hex.EncodeToString(hash.Sum(nil)) != digest || ctx.Err() != nil {
		return ErrExecutionGitCustody
	}
	return nil
}

func (git *LinuxExecutionGitCustody) probeGit(ctx context.Context, root *os.File, executable string, environment []string, argument string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	stdout, stderr := checkoutCommandOutput{remaining: 4 << 10, cancel: cancel}, checkoutCommandOutput{remaining: 4 << 10, cancel: cancel}
	command := exec.CommandContext(ctx, executable, argument)
	command.ExtraFiles, command.Dir, command.Env = []*os.File{root}, git.directory, environment
	command.Stdout, command.Stderr, command.WaitDelay = &stdout, &stderr, time.Second
	runErr := runReferenceCommand(ctx, command)
	if command.Process != nil {
		git.probe = &linuxExecutionToolProbe{pid: command.Process.Pid, joined: command.ProcessState != nil}
	}
	if runErr != nil || ctx.Err() != nil || stdout.err != nil || stderr.err != nil || stderr.buffer.Len() != 0 {
		return "", ErrExecutionGitCustody
	}
	git.probe = nil // The joined runner already observed the exact session empty.
	return strings.TrimSuffix(stdout.buffer.String(), "\n"), nil
}

// Check observes only the tree's kernel object, not payloads or original paths.
func (git *LinuxExecutionGitCustody) Check(ctx context.Context) (ExecutionToolIdentity, error) {
	if git == nil {
		return ExecutionToolIdentity{}, ErrExecutionGitCustody
	}
	git.mu.Lock()
	defer git.mu.Unlock()
	if git.err != nil || git.closing || git.identity.Role != "git" || git.tree == nil {
		git.err = ErrExecutionGitCustody
		return ExecutionToolIdentity{}, git.err
	}
	if _, err := git.tree.Check(ctx); err != nil {
		git.err = ErrExecutionGitCustody
		return ExecutionToolIdentity{}, git.err
	}
	return git.identity, nil
}

// WithGit supplies one root loan and a fresh closed environment. Attach the root
// as the FIRST ExtraFile (child FD3) and execute /proc/self/fd/3/core/git. Every
// child and descendant must join before returning. Do not escape/duplicate the
// descriptor or reenter this owner or its tree. Home/temp are mutable trusted
// workspace selections, not protected inputs. No command permission is issued.
func (git *LinuxExecutionGitCustody) WithGit(ctx context.Context, home, temporary string, use func(ExecutionToolIdentity, *os.File, []string) error) error {
	if git == nil {
		return ErrExecutionGitCustody
	}
	git.mu.Lock()
	defer git.mu.Unlock()
	if git.err != nil || git.closing || git.identity.Role != "git" || git.tree == nil || use == nil ||
		!linuxGitPrivateDirectory(home) || !linuxGitPrivateDirectory(temporary) {
		git.err = ErrExecutionGitCustody
		return git.err
	}
	err := git.tree.WithRoot(ctx, func(root *os.File) error {
		return use(git.identity, root, executionGitEnvironment(linuxGitCore, home, temporary))
	})
	if _, checkErr := git.tree.Check(ctx); checkErr != nil {
		git.err = ErrExecutionGitCustody
		return git.err
	}
	return err
}

// PrivateDirectory identifies only retained exact cleanup custody.
func (git *LinuxExecutionGitCustody) PrivateDirectory() string {
	if git == nil {
		return ""
	}
	return git.directory
}

func (git *LinuxExecutionGitCustody) checkStage() error {
	if git.stage == nil || git.stageInfo == nil || !linuxGitPrivateDirectory(git.directory) {
		return ErrExecutionGitCustody
	}
	held, err := git.stage.Stat(".")
	current, pathErr := os.Lstat(git.directory)
	if err != nil || pathErr != nil || !os.SameFile(git.stageInfo, held) || !os.SameFile(held, current) {
		return ErrExecutionGitCustody
	}
	return nil
}

func (git *LinuxExecutionGitCustody) checkChild(name string) error {
	before, exists := git.children[name]
	if !exists || before == nil {
		return ErrExecutionGitCustody
	}
	current, err := git.stage.Lstat(name)
	if err != nil || !current.IsDir() || current.Mode().Perm() != 0o700 ||
		!os.SameFile(before, current) || !inputCustodyCopyOwned(current) {
		return ErrExecutionGitCustody
	}
	return nil
}

func (git *LinuxExecutionGitCustody) removeAliases() (retErr error) {
	if git.checkStage() != nil {
		return ErrExecutionGitCustody
	}
	if len(git.aliases) == 0 {
		return nil
	}
	if git.checkChild("core") != nil {
		return ErrExecutionGitCustody
	}
	core, err := git.stage.OpenRoot("core")
	if err != nil {
		return ErrExecutionGitCustody
	}
	defer func() {
		if git.observation.close(core.Close()) != nil {
			retErr = ErrExecutionGitCustody
		}
	}()
	held, err := core.Stat(".")
	if err != nil || !os.SameFile(git.children["core"], held) {
		return ErrExecutionGitCustody
	}
	for _, name := range executionGitImageNames() {
		before, exists := git.aliases[name]
		if !exists {
			continue
		}
		current, err := core.Lstat(name)
		if err != nil || before == nil || !inputCustodySame(before, current) || !inputCustodyCopyOwned(current) ||
			git.checkStage() != nil || git.checkChild("core") != nil || core.Remove(name) != nil {
			return ErrExecutionGitCustody
		}
		delete(git.aliases, name)
	}
	return nil
}

// Close joins the constructor owner before releasing its tree or exact scratch.
// It removes only recorded, unchanged aliases and then empty directories; any
// unknown/replaced entry or uncertain helper keeps cleanup custody. Sticky
// refusal can remain after successful release; released records that fact.
func (git *LinuxExecutionGitCustody) Close(ctx context.Context) error {
	if git == nil || ctx == nil {
		return ErrExecutionGitCustody
	}
	git.mu.Lock()
	defer git.mu.Unlock()
	git.closing = true
	if git.released {
		return git.err
	}
	if git.probe != nil {
		deadline := time.Now().Add(5 * time.Second)
		if bound, ok := ctx.Deadline(); ok && bound.Before(deadline) {
			deadline = bound
		}
		if !git.probe.joined || ctx.Err() != nil || t4013.WaitPrivateProcessSession(git.probe.pid, deadline) != nil {
			git.err = ErrExecutionGitCustody
			return git.err
		}
		git.probe = nil
	}
	if git.tree != nil {
		if git.tree.Close(ctx) != nil {
			git.err = ErrExecutionGitCustody
		}
		git.tree.mu.Lock()
		if git.tree.closeErr != nil {
			git.closeErr = ErrExecutionGitCustody
		}
		git.tree.mu.Unlock()
		if !git.tree.closed {
			return ErrExecutionGitCustody
		}
	}
	if git.sealed != nil {
		if git.sealed.Close() != nil {
			git.err = ErrExecutionGitCustody
		}
		git.sealed.mu.Lock()
		if git.sealed.closeErr != nil {
			git.closeErr = ErrExecutionGitCustody
		}
		git.sealed.mu.Unlock()
		git.sealed = nil
	}
	if ctx.Err() != nil || git.removeAliases() != nil {
		git.err = ErrExecutionGitCustody
		return git.err
	}
	for _, name := range []string{"core", "scratch"} {
		before, exists := git.children[name]
		if !exists {
			continue
		}
		if before == nil || git.checkChild(name) != nil || git.stage.Remove(name) != nil {
			git.err = ErrExecutionGitCustody
			return git.err
		}
		delete(git.children, name)
	}
	if git.checkStage() != nil || os.Remove(git.directory) != nil {
		git.err = ErrExecutionGitCustody
		return git.err
	}
	if git.observation.close(git.stage.Close()) != nil {
		git.err = ErrExecutionGitCustody
	}
	git.released = true
	return git.err
}
