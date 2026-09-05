package t421

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
)

// ErrExecutionGitCustody exposes no selected path, repository or child output.
var ErrExecutionGitCustody = errors.New("execution Git resources unavailable or changed")

// These are actual Git-core aliases, not invented process counts. Local
// transport uses upload-pack and pack-objects, chooses index-pack or
// unpack-objects, checks connectivity with rev-list, and may run maintenance.
// Both unpacking alternatives must be available; no GC/fetch policy is changed.
func executionGitImageNames() [7]string {
	return [7]string{"git", "git-upload-pack", "git-pack-objects", "git-index-pack",
		"git-unpack-objects", "git-rev-list", "git-maintenance"}
}

// ExecutionGitCustody owns protected copies of the actual native Git image and
// six independently resolved matching builtin aliases. It is not a full tool,
// checkout or command admission. In particular, /bin/sh and platform libraries
// remain separately admitted trusted-system resources; repository config and
// argv still need their source-owned command/input checks. This is no sandbox
// or proof that arbitrary Git commands cannot delegate to another image.
type ExecutionGitCustody struct {
	input    *ExecutionInputCustody
	identity ExecutionToolIdentity
}

// ProtectExecutionGit observes the explicitly selected actual Git/core image,
// verifies all six installed aliases, protects seven fresh regular copies, then
// probes the protected Git with its protected exec directory. No installed file
// is flagged, no caller identity is trusted, and no mutable helper path is
// returned. A failure after copying retains closed cleanup custody without a
// usable identity. The two-minute ceiling is cooperative, not a hard I/O bound.
func ProtectExecutionGit(ctx context.Context, parent, binary string) (*ExecutionGitCustody, error) {
	if ctx == nil || ctx.Err() != nil || runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" ||
		!executionGitPrivateDirectory(parent) || !executionGitAbsolutePath(binary) {
		return nil, ErrExecutionGitCustody
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	binary, err := filepath.EvalSymlinks(binary)
	if err != nil || !executionGitAbsolutePath(binary) {
		return nil, ErrExecutionGitCustody
	}
	identity, err := ObserveExecutionExternalTool(ctx, "git", binary)
	if err != nil {
		return nil, ErrExecutionGitCustody
	}
	core, err := executionGitCoreDirectory(ctx, parent, binary, identity.SHA256)
	if err != nil {
		return nil, ErrExecutionGitCustody
	}
	selections, err := executionGitSelections(ctx, core, identity.SHA256)
	if err != nil {
		return nil, ErrExecutionGitCustody
	}
	input, err := ProtectExecutionInputs(ctx, parent, selections)
	if input == nil {
		return nil, ErrExecutionGitCustody
	}
	git := &ExecutionGitCustody{input: input}
	fail := func() (*ExecutionGitCustody, error) {
		_ = git.refuse()
		_ = git.Close()
		return git, ErrExecutionGitCustody
	}
	if err != nil {
		return fail()
	}
	path, err := git.checkImages(ctx)
	if err != nil {
		return fail()
	}
	environment := executionGitEnvironment(git.Directory(), parent, parent)
	version, err := runExternalToolProbe(ctx, parent, path, environment, "--version")
	if err != nil || version != identity.Version {
		return fail()
	}
	protectedCore, err := runExternalToolProbe(ctx, parent, path, environment, "--exec-path")
	if err != nil || protectedCore != git.Directory() {
		return fail()
	}
	builtins, err := runExternalToolProbe(ctx, parent, path, environment, "--list-cmds=builtins")
	if err != nil || !executionGitRequiredBuiltins(builtins) {
		return fail()
	}
	// Original aliases may be symlinks or hardlinks. Re-resolve every name and
	// remeasure its actual regular image; equality of the selected main alone
	// cannot bless an unrelated mutable helper resource.
	afterCore, err := executionGitCoreDirectory(ctx, parent, binary, identity.SHA256)
	if err != nil || afterCore != core {
		return fail()
	}
	after, err := executionGitSelections(ctx, core, identity.SHA256)
	if err != nil {
		return fail()
	}
	for index := range selections {
		if selections[index] != after[index] {
			return fail()
		}
	}
	if digest, err := t4013.DigestHostExecutable(ctx, binary); err != nil || digest != identity.SHA256 {
		return fail()
	}
	if _, err := git.checkImages(ctx); err != nil {
		return fail()
	}
	git.identity = identity
	return git, nil
}

func executionGitCoreDirectory(ctx context.Context, root, binary, digest string) (string, error) {
	actual, err := t4013.DigestHostExecutable(ctx, binary)
	if err != nil || actual != digest {
		return "", ErrExecutionGitCustody
	}
	core, err := runExternalToolProbe(ctx, root, binary, externalToolEnvironment(root), "--exec-path")
	if err != nil || !executionGitAbsolutePath(core) {
		return "", ErrExecutionGitCustody
	}
	actual, err = t4013.DigestHostExecutable(ctx, binary)
	if err != nil || actual != digest {
		return "", ErrExecutionGitCustody
	}
	core, err = filepath.EvalSymlinks(core)
	if err != nil || !executionGitAbsolutePath(core) {
		return "", ErrExecutionGitCustody
	}
	return core, nil
}

func executionGitSelections(ctx context.Context, core, digest string) ([]ExecutionInputCopy, error) {
	names := executionGitImageNames()
	selections := make([]ExecutionInputCopy, 0, len(names))
	for _, name := range names {
		if ctx.Err() != nil {
			return nil, ErrExecutionGitCustody
		}
		path, err := filepath.EvalSymlinks(filepath.Join(core, name))
		if err != nil || !executionGitAbsolutePath(path) {
			return nil, ErrExecutionGitCustody
		}
		actual, err := t4013.DigestHostExecutable(ctx, path)
		if err != nil || actual != digest {
			return nil, ErrExecutionGitCustody
		}
		selections = append(selections, ExecutionInputCopy{Name: name, Path: path, SHA256: actual, Executable: true})
	}
	return selections, nil
}

func executionGitRequiredBuiltins(raw string) bool {
	lines := strings.SplitN(raw, "\n", 513)
	if len(lines) > 512 {
		return false
	}
	available := make(map[string]bool, len(lines))
	for _, name := range lines {
		if !validSanitizedToken(strings.ReplaceAll(name, "-", ""), 64) || available[name] {
			return false
		}
		available[name] = true
	}
	names := executionGitImageNames()
	for _, name := range names[1:] {
		if !available[strings.TrimPrefix(name, "git-")] {
			return false
		}
	}
	for _, name := range []string{"init", "fast-import", "rev-parse", "ls-tree", "clone", "fetch"} {
		if !available[name] {
			return false
		}
	}
	return true
}

// Check rechecks all seven protected images without hashing or probing, then
// returns the observed main identity by value and its private path. It confers
// no launch permission or repository/input authority.
func (git *ExecutionGitCustody) Check(ctx context.Context) (ExecutionToolIdentity, string, error) {
	if git == nil || git.identity.Role != "git" {
		return ExecutionToolIdentity{}, "", git.refuse()
	}
	path, err := git.checkImages(ctx)
	if err != nil {
		return ExecutionToolIdentity{}, "", ErrExecutionGitCustody
	}
	return git.identity, path, nil
}

// Environment returns a fresh, exact Git-child environment. GIT_EXEC_PATH is
// not inserted into the frozen server/recovery parent environment. The caller
// still owns admission of mutable home/temp/repository roots and command argv.
// File-only transport, absent ambient config and disabled template/hook imports
// do not alter fetch flags, GC or maintenance policy.
func (git *ExecutionGitCustody) Environment(ctx context.Context, home, temporary string) ([]string, error) {
	if _, _, err := git.Check(ctx); err != nil || !executionGitPrivateDirectory(home) || !executionGitPrivateDirectory(temporary) {
		return nil, git.refuse()
	}
	return executionGitEnvironment(git.Directory(), home, temporary), nil
}

func executionGitEnvironment(core, home, temporary string) []string {
	environment := externalToolEnvironment(temporary)
	for index, value := range environment {
		if strings.HasPrefix(value, "HOME=") {
			environment[index] = "HOME=" + home
		} else if strings.HasPrefix(value, "PATH=") {
			environment[index] = "PATH=" + core
		}
	}
	return append(environment, "GIT_EXEC_PATH="+core, "GIT_ALLOW_PROTOCOL=file", "GIT_TEMPLATE_DIR="+os.DevNull,
		"GIT_CONFIG_COUNT=3", "GIT_CONFIG_KEY_0=core.fsmonitor", "GIT_CONFIG_VALUE_0=false",
		"GIT_CONFIG_KEY_1=core.untrackedCache", "GIT_CONFIG_VALUE_1=false",
		"GIT_CONFIG_KEY_2=core.hooksPath", "GIT_CONFIG_VALUE_2="+os.DevNull)
}

func (git *ExecutionGitCustody) checkImages(ctx context.Context) (string, error) {
	if git == nil || git.input == nil {
		return "", git.refuse()
	}
	var path string
	for _, name := range executionGitImageNames() {
		current, err := git.input.Check(ctx, name)
		if err != nil {
			return "", git.refuse()
		}
		if name == "git" {
			path = current
		}
	}
	return path, nil
}

func (git *ExecutionGitCustody) refuse() error {
	if git != nil && git.input != nil {
		git.input.mu.Lock()
		git.input.err = ErrExecutionInputCustody
		git.input.mu.Unlock()
	}
	return ErrExecutionGitCustody
}

func executionGitAbsolutePath(path string) bool {
	return len(path) <= maxInputCustodyPathBytes && filepath.IsAbs(path) && filepath.Clean(path) == path &&
		path != string(filepath.Separator) && !strings.ContainsAny(path, "\x00\r\n")
}

func executionGitPrivateDirectory(path string) bool {
	if !executionGitAbsolutePath(path) {
		return false
	}
	canonical, err := filepath.EvalSymlinks(path)
	info, statErr := os.Lstat(path)
	return err == nil && canonical == path && statErr == nil && info.IsDir() && info.Mode().Perm() == 0o700 && inputCustodyOwned(info)
}

// Directory exposes retained private cleanup custody, including failed copies.
func (git *ExecutionGitCustody) Directory() string {
	if git == nil || git.input == nil {
		return ""
	}
	return git.input.Directory()
}

// Close releases descriptors; it never thaws/removes protected copies or proves
// child/session teardown. Failed admission remains sticky after close.
func (git *ExecutionGitCustody) Close() error {
	if git != nil && git.input != nil && git.input.Close() != nil {
		return ErrExecutionGitCustody
	}
	return nil
}
