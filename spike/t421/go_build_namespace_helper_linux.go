//go:build linux

package t421

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// LinuxGoBuildHelperMain owns a distinct fixed recipe. Unlike the input-tree
// constructor it remains PID1 through its one neutral Go command and orphan
// reap. It accepts no arbitrary command, path, source program or role selector.
// Its caller must separately admit SDK/Git images; copying grants no authority.
func LinuxGoBuildHelperMain() (status int) {
	status = 125
	stage := "entry"
	defer func() {
		if status != 0 {
			_, _ = fmt.Fprintf(os.Stderr, "go-build-helper refused stage=%s\n", stage)
		}
	}()
	if runtime.GOARCH != "amd64" || len(os.Args) != 2 || os.Args[1] != linuxGoBuildRecipe || os.Getpid() != 1 ||
		os.Getppid() != 0 || os.Getuid() != 0 || os.Geteuid() != 0 || os.Getgid() != 0 || os.Getegid() != 0 {
		return status
	}
	for _, fd := range []int{3, 4, 5, 6} {
		unix.CloseOnExec(fd)
	}
	stage = "exclusive-control"
	kind, kindErr := unix.GetsockoptInt(4, unix.SOL_SOCKET, unix.SO_TYPE)
	peer, peerErr := unix.GetsockoptUcred(4, unix.SOL_SOCKET, unix.SO_PEERCRED)
	// The parent lies outside this PID namespace. Its PID translates to zero;
	// exclusivity is the inherited socket plus mapped credentials and nonce.
	if kindErr != nil || peerErr != nil || kind != unix.SOCK_SEQPACKET || peer.Pid != 0 || peer.Uid != 0 || peer.Gid != 0 {
		return status
	}
	wire := &inputTreeSocket{fd: 4}
	startup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	raw, _, err := wire.receive(startup, treeWireInit, false)
	cancel()
	if err != nil {
		return status
	}
	initial := bytes.Clone(raw)
	ctx, cancel, err := inputTreeDeadline(initial)
	if err != nil {
		return status
	}
	defer cancel()
	stage = "mapping-and-private-mount"
	if !inputTreeHelperMappings(initial) || setupInputTreeMount() != nil {
		return status
	}
	stage = "pid-procfs"
	if unix.Mount("proc", "/proc", "proc", unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC, "") != nil {
		return status
	}
	self, selfErr := os.Readlink("/proc/self")
	if selfErr != nil || self != "1" {
		return status
	}
	stage = "reachable-workspace"
	workspace, err := goBuildHelperWorkspace()
	if err != nil {
		return status
	}
	stage = "protected-copy"
	protected, err := receiveProtectedInputTree(ctx, wire)
	if err != nil {
		return status
	}
	defer func() {
		if protected.close() != nil {
			status = 126
		}
	}()
	stage = "fixed-input-shape"
	if !neutralGoBuildInputShape(protected.census) || !neutralGoBuildSource(protected.root) {
		return status
	}
	revision, err := neutralGoBuildRevision(protected.root)
	if err != nil {
		return status
	}
	ready := append(bytes.Clone(initial[16:]), protected.manifest...)
	stage = "ready-ack"
	if wire.send(ctx, treeWireReady, ready, protected.view) != nil {
		return status
	}
	ack, _, err := wire.receive(ctx, treeWireAck, false)
	if err != nil || !bytes.Equal(ack, initial[16:]) {
		return status
	}
	stage = "neutral-compile"
	if _, err := os.Lstat(filepath.Join(filepath.Dir(workspace), "mutable")); !os.IsNotExist(err) {
		return status
	}
	if err := runNeutralGoNamespaceBuild(ctx, workspace, revision); err != nil {
		return status
	}
	stage = "output-identity"
	output, err := os.Open(filepath.Join(workspace, "output"))
	if err != nil {
		return status
	}
	digest, digestErr := digestExecutionReferenceFD(ctx, output)
	closeErr := output.Close()
	decoded, decodeErr := hex.DecodeString(strings.TrimPrefix(digest, "sha256:"))
	if digestErr != nil || closeErr != nil || decodeErr != nil || len(decoded) != sha256.Size {
		return status
	}
	result := append(bytes.Clone(ready), decoded...)
	stage = "result-ack"
	if wire.send(ctx, treeWireEnd, result, nil) != nil {
		return status
	}
	ack, _, err = wire.receive(ctx, treeWireAck, false)
	if err != nil || !bytes.Equal(ack, initial[16:]) {
		return status
	}
	return 0
}

func goBuildHelperWorkspace() (string, error) {
	name, err := os.Readlink("/proc/self/fd/6")
	root, rootErr := os.Getwd()
	var held, linked unix.Stat_t
	if err != nil || rootErr != nil || !executionGitAbsolutePath(name) || strings.ContainsAny(name, " \t\\") ||
		name != filepath.Join(filepath.Dir(root), "work") ||
		inputTreePathsOverlap(root, name) || unix.Fstat(6, &held) != nil || unix.Stat(name, &linked) != nil ||
		!sameLinuxInputStat(held, linked) || held.Mode != unix.S_IFDIR|0o700 || held.Uid != 0 || unix.Close(6) != nil {
		return "", ErrExecutionGoBuildCustody
	}
	for _, child := range []string{"home", "tmp", "cache"} {
		if !linuxGitPrivateDirectory(filepath.Join(name, child)) {
			return "", ErrExecutionGoBuildCustody
		}
	}
	return name, nil
}

func neutralGoBuildInputShape(census inputTreeCensus) bool {
	required := map[string]bool{"inputs": false, "inputs/sdk": false, "inputs/modules": false, "inputs/source": false, "inputs/source/.git": false, "inputs/git": false, "inputs/git/core": false}
	aliases := map[string]bool{}
	var aliasDigest [sha256.Size]byte
	aliasCount := 0
	for _, name := range executionGitImageNames() {
		aliases["inputs/git/core/"+name] = false
	}
	for _, row := range census.rows {
		if _, ok := required[row.name]; ok {
			if row.mode != 0o700 {
				return false
			}
			required[row.name] = true
			continue
		}
		if _, ok := aliases[row.name]; ok {
			if row.mode != 0o500 || aliasCount != 0 && row.digest != aliasDigest {
				return false
			}
			aliasCount++
			aliasDigest = row.digest
			aliases[row.name] = true
			continue
		}
		if strings.HasPrefix(row.name, "inputs/sdk/") || neutralGoBuildGitEntry(row) {
			continue
		}
		if (row.name != "inputs/source/go.mod" && row.name != "inputs/source/main.go") || row.mode != 0o400 {
			return false
		}
	}
	for _, seen := range required {
		if !seen {
			return false
		}
	}
	for _, seen := range aliases {
		if !seen {
			return false
		}
	}
	return true
}

func neutralGoBuildSource(root *os.Root) bool {
	for _, file := range []struct{ name, content string }{{"inputs/source/go.mod", linuxGoBuildModule}, {"inputs/source/main.go", linuxGoBuildSource}, {"inputs/source/.git/config", linuxGoBuildGitConfig}} {
		reader, err := root.Open(file.name)
		if err != nil {
			return false
		}
		raw, readErr := io.ReadAll(io.LimitReader(reader, int64(len(file.content)+1)))
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil || string(raw) != file.content {
			return false
		}
	}
	return true
}

func neutralGoBuildGitEntry(row inputTreeEntry) bool {
	name, ok := strings.CutPrefix(row.name, "inputs/source/.git/")
	if !ok {
		return false
	}
	if row.mode == 0o700 {
		return name == "objects" || name == "objects/info" || name == "objects/pack" || name == "refs" || name == "refs/heads" || name == "refs/tags" ||
			name == "logs" || name == "logs/refs" || name == "logs/refs/heads" || name == "info" ||
			(strings.HasPrefix(name, "objects/") && len(name) == len("objects/")+2 && onlyLowerHex(strings.TrimPrefix(name, "objects/")))
	}
	if row.mode != 0o400 {
		return false
	}
	if name == "config" || name == "HEAD" || name == "ORIG_HEAD" || name == "index" || name == "refs/heads/main" || name == "logs/HEAD" || name == "logs/refs/heads/main" {
		return true
	}
	if object, ok := strings.CutPrefix(name, "objects/"); ok {
		directory, leaf, hasSlash := strings.Cut(object, "/")
		if hasSlash && len(directory) == 2 && len(leaf) == 38 && onlyLowerHex(directory+leaf) {
			return true
		}
		if directory == "pack" {
			stem, suffix, found := strings.Cut(leaf, ".")
			return found && strings.HasPrefix(stem, "pack-") && validCommit(strings.TrimPrefix(stem, "pack-")) && (suffix == "pack" || suffix == "idx" || suffix == "rev")
		}
	}
	return false
}

func onlyLowerHex(value string) bool {
	return value != "" && strings.Trim(value, "0123456789abcdef") == ""
}

func neutralGoBuildRevision(root *os.Root) (string, error) {
	head, headErr := readReferenceControl(root, "inputs/source/.git/HEAD", 64)
	ref, refErr := readReferenceControl(root, "inputs/source/.git/refs/heads/main", 64)
	if headErr != nil || refErr != nil || string(head) != "ref: refs/heads/main\n" || len(ref) != 41 || ref[40] != '\n' || !validCommit(string(ref[:40])) {
		return "", ErrExecutionGoBuildCustody
	}
	return string(ref[:40]), nil
}

func neutralGoBuildInfo(info *debug.BuildInfo, revision string) bool {
	if !validCommit(revision) || info == nil || info.GoVersion != runtime.Version() || info.Path != "neutral.invalid/protected" ||
		info.Main.Path != "neutral.invalid/protected" || info.Main.Version != "(devel)" || info.Main.Sum != "" || info.Main.Replace != nil || len(info.Deps) != 0 {
		return false
	}
	settings := map[string]string{}
	for _, setting := range info.Settings {
		if _, exists := settings[setting.Key]; exists {
			return false
		}
		settings[setting.Key] = setting.Value
	}
	return settings["CGO_ENABLED"] == "0" && settings["-trimpath"] == "true" && settings["GOOS"] == "linux" && settings["GOARCH"] == "amd64" &&
		settings["vcs"] == "git" && settings["vcs.revision"] == revision && settings["vcs.modified"] == "false"
}

func runNeutralGoNamespaceBuild(ctx context.Context, workspace, revision string) error {
	root, err := os.Getwd()
	if err != nil {
		return ErrExecutionGoBuildCustody
	}
	inputs := filepath.Join(root, "inputs")
	request := ReferenceToolRequest{GoRoot: filepath.Join(inputs, "sdk"), ModuleCache: filepath.Join(inputs, "modules")}
	environment := referenceBuildEnvironment(request, workspace)
	git := filepath.Join(inputs, "git", "core")
	for i, binding := range environment {
		if strings.HasPrefix(binding, "PATH=") {
			environment[i] = "PATH=" + git
		}
	}
	environment = append(environment, "GIT_EXEC_PATH="+git, "GIT_ALLOW_PROTOCOL=file", "GIT_TEMPLATE_DIR="+os.DevNull)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, filepath.Join(request.GoRoot, "bin", "go"), "build", "-mod=readonly", "-trimpath", "-buildvcs=true", "-o", filepath.Join(workspace, "output"), ".")
	command.Dir, command.Env = filepath.Join(inputs, "source"), environment
	stdout, stderr := checkoutCommandOutput{remaining: 64 << 10, cancel: cancel}, checkoutCommandOutput{remaining: 64 << 10, cancel: cancel}
	command.Stdout, command.Stderr, command.WaitDelay = &stdout, &stderr, time.Second
	if prepareReferenceCommand(command) != nil { // Separate group; retain PID1's session.
		return ErrExecutionGoBuildCustody
	}
	if command.Start() != nil {
		return ErrExecutionGoBuildCustody
	}
	waitErr := command.Wait() // Sole direct-root Wait, including output pumps.
	orphanErr := reapGoBuildNamespaceOrphans(ctx)
	if waitErr != nil || orphanErr != nil || stdout.err != nil || stderr.err != nil || ctx.Err() != nil {
		_, _ = fmt.Fprintf(os.Stderr, "neutral compile refused stdout=%q stderr=%q\n", stdout.buffer.String(), stderr.buffer.String())
		return ErrExecutionGoBuildCustody
	}
	info, err := buildinfo.ReadFile(filepath.Join(workspace, "output"))
	if err != nil || !neutralGoBuildInfo(info, revision) {
		return ErrExecutionGoBuildCustody
	}
	return nil
}

// Only call after every direct Cmd.Wait. A live adopted child is unresolved,
// never converted into successful drain by letting PID1 exit and kill it.
func reapGoBuildNamespaceOrphans(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for ctx.Err() == nil {
		var status unix.WaitStatus
		pid, err := unix.Wait4(-1, &status, unix.WNOHANG, nil)
		if err == unix.ECHILD {
			return nil
		}
		if err == unix.EINTR {
			continue
		}
		if err != nil || pid > 0 && (!status.Exited() || status.ExitStatus() != 0) {
			return ErrExecutionGoBuildCustody
		}
		if pid == 0 {
			timer := time.NewTimer(20 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
			case <-timer.C:
			}
		}
	}
	return ErrExecutionGoBuildCustody
}
