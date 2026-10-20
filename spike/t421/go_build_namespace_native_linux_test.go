//go:build linux && phebs_t42_native

package t421

import (
	"bytes"
	"context"
	"crypto/rand"
	"debug/buildinfo"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

// This is a fixed neutral layout/lifetime qualification, not a public Go owner,
// protected reference-tool provenance, aggregate admission or ceremony entry.
func TestLinuxGoBuildRequiredNativeNamespaceComposition(t *testing.T) {
	helper := requireLinuxGoBuildHelper(t)
	var attempt *LinuxExecutionInputTree
	t.Cleanup(func() {
		if attempt != nil && !attempt.closed {
			t.Error("unjoined attempt retains build-helper image keeper")
			return
		}
		if helper.Close() != nil {
			t.Error("build-helper image cleanup refused")
		}
	})
	oldHelper := requireLinuxInputTreeHelper(t)
	hostRoot, err := goBuildImportHostRoot()
	if err != nil {
		t.Fatal("host mount baseline", err)
	}
	t.Cleanup(func() {
		after, err := goBuildImportHostRoot()
		if err != nil || after != hostRoot {
			t.Error("host-root mount propagation changed", err)
		}
	})
	base, err := os.MkdirTemp("", "phebs-linux-go-namespace-")
	if err != nil {
		t.Fatal(err)
	}
	parent, work, payload, mount := filepath.Join(base, "parent"), filepath.Join(base, "work"), filepath.Join(base, "mutable"), filepath.Join(base, "mount")
	for _, name := range []string{parent, work, payload, mount, filepath.Join(work, "home"), filepath.Join(work, "tmp"), filepath.Join(work, "cache"), filepath.Join(payload, "modules"), filepath.Join(payload, "source"), filepath.Join(payload, "git"), filepath.Join(payload, "git", "core")} {
		if os.Mkdir(name, 0o700) != nil {
			t.Fatal("private fixture directory; retain", base)
		}
	}
	var git *LinuxExecutionGitCustody
	preparationSafe := true
	t.Cleanup(func() {
		if !preparationSafe {
			t.Errorf("uncertain preparation; retain fixture and helpers %s", base)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if attempt != nil {
			_ = attempt.Close(ctx)
			if !attempt.closed {
				t.Errorf("unjoined namespace attempt; retain helper, inputs and %s", base)
				return
			}
		}
		if git != nil {
			_ = git.Close(ctx)
			if !git.released {
				t.Errorf("unjoined Git custody; retain %s", base)
				return
			}
		}
		if os.RemoveAll(base) != nil {
			t.Error("joined fixture cleanup refused")
		}
	})
	git, err = ProtectLinuxExecutionGit(t.Context(), parent, requireLinuxExternalTool(t, "git"), oldHelper)
	if err != nil {
		t.Fatal("protected Git prerequisite", err)
	}
	source := filepath.Join(payload, "source")
	gitRun := func(input string, args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		var result string
		err := git.WithGit(ctx, work, work, func(_ ExecutionToolIdentity, root *os.File, environment []string) error {
			command := exec.CommandContext(ctx, linuxGitCore+"/git", args...)
			command.Dir, command.Env, command.ExtraFiles, command.Stdin = work, environment, []*os.File{root}, strings.NewReader(input)
			stdout, stderr := checkoutCommandOutput{remaining: 64 << 10, cancel: cancel}, checkoutCommandOutput{remaining: 64 << 10, cancel: cancel}
			command.Stdout, command.Stderr, command.WaitDelay = &stdout, &stderr, time.Second
			preparationSafe = false
			runErr := runReferenceCommand(ctx, command)
			if command.Process == nil {
				preparationSafe = true
			} else if command.ProcessState != nil {
				members, observeErr := t4013.PrivateProcessSessionMembers(command.Process.Pid)
				preparationSafe = observeErr == nil && members == 0
				t.Logf("source preparation pid=%d root_joined=true session_members=%d observation_error=%v", command.Process.Pid, members, observeErr)
			}
			result = stdout.buffer.String()
			return errors.Join(runErr, stdout.err, stderr.err)
		})
		if err != nil {
			t.Fatal("neutral source preparation", err)
		}
		return result
	}
	files := []struct{ name, content string }{{"go.mod", linuxGoBuildModule}, {"main.go", linuxGoBuildSource}}
	var stream strings.Builder
	for i, file := range files {
		if os.WriteFile(filepath.Join(source, file.name), []byte(file.content), 0o600) != nil {
			t.Fatal("neutral source file")
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
	revision := strings.TrimSpace(gitRun("", "-C", source, "rev-parse", "HEAD"))
	if !validCommit(revision) || os.WriteFile(filepath.Join(source, ".git", "config"), []byte(linuxGoBuildGitConfig), 0o600) != nil {
		t.Fatal("neutral source controls")
	}
	// Copy only the selected SDK into this private fixture. The installed SDK
	// remains available; no vendor/toolchain or ambient-library closure is issued.
	gitTree, err := git.tree.Check(t.Context())
	if err != nil || gitTree.Bytes > maxGoBuildBytes-(8<<20) {
		t.Fatal("Git/source staging reservation", err)
	}
	copyNativeGoSDK(t, os.Getenv("PHEBS_T42_GO_SDK"), payload, int64(gitTree.Bytes)+(8<<20))
	err = git.WithGit(t.Context(), work, work, func(identity ExecutionToolIdentity, root *os.File, _ []string) error {
		for _, name := range executionGitImageNames() {
			reader, err := os.Open(fmt.Sprintf("/proc/self/fd/%d/core/%s", root.Fd(), name))
			if err != nil {
				return err
			}
			writer, err := os.OpenFile(filepath.Join(payload, "git", "core", name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o500)
			if err != nil {
				_ = reader.Close()
				return err
			}
			_, copyErr := io.Copy(writer, io.LimitReader(reader, maxInputCustodyFileBytes+1))
			readerErr, writerErr := reader.Close(), writer.Close()
			if copyErr != nil || readerErr != nil || writerErr != nil {
				return ErrExecutionGitCustody
			}
			copied, err := os.Open(filepath.Join(payload, "git", "core", name))
			if err != nil {
				return err
			}
			digest, digestErr := digestExecutionReferenceFD(t.Context(), copied)
			closeErr := copied.Close()
			if digestErr != nil || closeErr != nil || digest != identity.SHA256 {
				return ErrExecutionGitCustody
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal("protected Git copy", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	attemptCtx, attemptCancel := context.WithCancel(ctx)
	defer attemptCancel()
	stage, err := os.Open(mount)
	if err != nil {
		t.Fatal(err)
	}
	stageInfo, err := stage.Stat()
	if err != nil {
		t.Fatal(err)
	}
	attempt = &LinuxExecutionInputTree{stagePath: mount, stage: stage, stageInfo: stageInfo, done: make(chan struct{}), cancel: attemptCancel}
	// The tracked worker keeps the image loan and launching thread through its
	// actual native Wait even when the caller's context expires first.
	go runNativeGoNamespaceAttempt(attemptCtx, attempt, helper, work, payload, revision)
	select {
	case <-attempt.done:
		if attempt.err != nil || !attempt.joined || !attempt.empty {
			t.Fatal("required protected namespace compile/VCS refused; retain diagnostics", attempt.err)
		}
	case <-ctx.Done():
		t.Fatal("namespace attempt deadline; retained worker owns unfinished cleanup")
	}
	t.Logf("namespace helper pid=%d actual_root_joined=%t session_empty=%t inputs_entries=%d inputs_bytes=%d", attempt.pid, attempt.joined, attempt.empty, attempt.identity.Entries, attempt.identity.Bytes)
	outputCtx, outputCancel := context.WithTimeout(ctx, 10*time.Second)
	defer outputCancel()
	command := exec.CommandContext(outputCtx, filepath.Join(work, "output"))
	command.Env, command.Dir = []string{"LANG=C", "LC_ALL=C"}, work
	stdout, stderr := checkoutCommandOutput{remaining: 32 << 10, cancel: outputCancel}, checkoutCommandOutput{remaining: 32 << 10, cancel: outputCancel}
	command.Stdout, command.Stderr, command.WaitDelay = &stdout, &stderr, time.Second
	preparationSafe = false
	runErr := runReferenceCommand(outputCtx, command)
	if command.Process == nil {
		preparationSafe = true
	} else if command.ProcessState != nil {
		members, observeErr := t4013.PrivateProcessSessionMembers(command.Process.Pid)
		preparationSafe = observeErr == nil && members == 0
		t.Logf("compiled output pid=%d actual_root_joined=true session_members=%d observation_error=%v", command.Process.Pid, members, observeErr)
	}
	if runErr != nil || !preparationSafe || stdout.err != nil || stderr.err != nil || stdout.buffer.String() != "neutral protected build\n" {
		t.Fatal("neutral compiled output", runErr, stdout.buffer.String(), stderr.buffer.String())
	}
}

func requireLinuxGoBuildHelper(t *testing.T) *LinuxExecutionToolCustody {
	t.Helper()
	if os.Geteuid() == 0 || !executionGitAbsolutePath(os.Getenv("PHEBS_T42_GO_SDK")) {
		t.Fatal("unprivileged consumer and explicit selected SDK required; no skip")
	}
	sdk := os.Getenv("PHEBS_T42_GO_SDK")
	if canonical, err := filepath.EvalSymlinks(sdk); err != nil || canonical != sdk {
		t.Fatal("canonical SDK required", err)
	}
	requestFile, err := os.Open(os.Getenv("PHEBS_T42_GO_BUILD_REQUEST"))
	if err != nil {
		t.Fatal("independently rebuildable Go-build helper request required; no skip", err)
	}
	raw, readErr := io.ReadAll(io.LimitReader(requestFile, 16<<10))
	closeErr := requestFile.Close()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var request ReferenceToolRequest
	if readErr != nil || closeErr != nil || len(raw) == 16<<10 || decoder.Decode(&request) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		t.Fatal("closed helper request required")
	}
	request.Role, request.PlanSchema = linuxGoBuildRole, linuxGoBuildRecipe
	if request.GoRoot != sdk {
		t.Fatal("selected fixture SDK differs from exact helper reference SDK")
	}
	helper, err := ProtectLinuxExecutionReferenceTool(t.Context(), request)
	if err != nil {
		if helper != nil {
			_ = helper.Close()
		}
		t.Fatal("exact-source sealed build helper refused", err)
	}
	return helper
}

func copyNativeGoSDK(t *testing.T, sdk, destination string, reserved int64) {
	t.Helper()
	input, err := os.OpenRoot(sdk)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = input.Close() }()
	output, err := os.OpenRoot(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = output.Close() }()
	var bytes int64
	entries, pathBytes := 256, 32<<10 // Fresh neutral Git/source controls and roots.
	err = walkGoBuildTree(t.Context(), input, ".", func(name string, info os.FileInfo) error {
		target := filepath.Join("sdk", name)
		entries++
		pathBytes += len("inputs/") + len(target)
		if entries > maxGoBuildEntries || pathBytes > maxGoBuildPathBytes {
			return ErrExecutionGoBuildCustody
		}
		if info.IsDir() {
			return output.Mkdir(target, 0o700)
		}
		if info.Size() > maxGoBuildBytes-reserved-bytes {
			return ErrExecutionGoBuildCustody
		}
		file, copied, err := copyExecutionInput(t.Context(), output, ExecutionInputCopy{Name: target, Path: filepath.Join(sdk, name), Executable: info.Mode().Perm()&0o111 != 0}, info.Size())
		bytes += copied
		if err != nil {
			return err
		}
		return file.Close()
	})
	if err != nil {
		t.Fatal("bounded private SDK copy", err)
	}
}

func runNativeGoNamespaceAttempt(ctx context.Context, tree *LinuxExecutionInputTree, helper *LinuxExecutionToolCustody, workspace, payload, revision string) {
	defer completeInputTreeConstruction(tree.done, tree.cancel)
	roots, rows, err := gatherInputTreeSources(ctx, tree.stagePath, []LinuxInputTreeSource{{Name: "inputs", Path: payload}})
	if err != nil {
		tree.err = ErrLinuxInputTree
		return
	}
	defer func() {
		if closeInputTreeSources(roots) != nil {
			tree.err = ErrLinuxInputTree
		}
	}()
	err = helper.WithImage(ctx, linuxGoBuildRole, func(_ ExecutionToolIdentity, image *os.File) (retErr error) {
		pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, 0)
		if err != nil {
			return ErrLinuxInputTree
		}
		parent, child := os.NewFile(uintptr(pair[0]), "go-build-controller"), os.NewFile(uintptr(pair[1]), "go-build-helper")
		work, err := os.Open(workspace)
		if err != nil {
			_ = parent.Close()
			_ = child.Close()
			return err
		}
		defer func() {
			parentErr, workErr := parent.Close(), work.Close()
			if parentErr != nil || workErr != nil {
				retErr = ErrLinuxInputTree
			}
			if child != nil && child.Close() != nil {
				retErr = ErrLinuxInputTree
			}
		}()
		if unix.SetsockoptInt(pair[0], unix.SOL_SOCKET, unix.SO_PASSCRED, 1) != nil {
			return ErrLinuxInputTree
		}
		command := exec.Command("/proc/self/fd/3", linuxGoBuildRecipe)
		command.ExtraFiles, command.Dir, command.Env = []*os.File{image, child, tree.stage, work}, tree.stagePath, []string{"GOMAXPROCS=2"}
		command.SysProcAttr = &syscall.SysProcAttr{Cloneflags: unix.CLONE_NEWUSER | unix.CLONE_NEWNS | unix.CLONE_NEWPID, Setsid: true, Pdeathsig: syscall.SIGKILL,
			UidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Geteuid(), Size: 1}}, GidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getegid(), Size: 1}}, GidMappingsEnableSetgroups: false}
		stderr := checkoutCommandOutput{remaining: 1 << 20, cancel: tree.cancel}
		command.Stderr, command.WaitDelay = &stderr, time.Second
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if err := command.Start(); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "namespace fixture start refused: %v\n", err)
			return ErrLinuxInputTree
		}
		tree.pid = command.Process.Pid
		waited := make(chan error, 1)
		go func() { waited <- command.Wait() }()
		pidfd, pidfdErr := unix.PidfdOpen(tree.pid, 0)
		if pidfdErr == nil {
			defer func() { _ = unix.Close(pidfd) }()
		}
		useErr := func() error {
			closeErr := child.Close()
			child = nil
			namespaces, nsErr := observeInputTreeNamespaces(tree.pid)
			var selfPIDNS, childPIDNS unix.Stat_t
			if closeErr != nil || pidfdErr != nil || nsErr != nil || t4013.MatchLinuxProcessExecutableImage(ctx, tree.pid, image) != nil ||
				unix.Stat("/proc/self/ns/pid", &selfPIDNS) != nil || unix.Stat("/proc/"+strconv.Itoa(tree.pid)+"/ns/pid", &childPIDNS) != nil ||
				selfPIDNS.Ino == childPIDNS.Ino && selfPIDNS.Dev == childPIDNS.Dev {
				return ErrLinuxInputTree
			}
			wire := &inputTreeSocket{fd: pair[0], peer: &unix.Ucred{Pid: int32(tree.pid), Uid: uint32(os.Geteuid()), Gid: uint32(os.Getegid())}}
			initial := make([]byte, 48)
			deadline, _ := ctx.Deadline()
			binary.BigEndian.PutUint64(initial, uint64(deadline.UnixNano()))
			binary.BigEndian.PutUint32(initial[8:], uint32(os.Geteuid()))
			binary.BigEndian.PutUint32(initial[12:], uint32(os.Getegid()))
			if _, err := rand.Read(initial[16:]); err != nil || wire.send(ctx, treeWireInit, initial, nil) != nil {
				return ErrLinuxInputTree
			}
			census, err := sendInputTreeSources(ctx, wire, roots, rows)
			if err != nil || wire.send(ctx, treeWireEnd, inputTreeManifest(census), nil) != nil {
				return ErrLinuxInputTree
			}
			ready, view, err := wire.receive(ctx, treeWireReady, true)
			tree.root = view // Own every delivered descriptor before later checks.
			expected := append(bytes.Clone(initial[16:]), inputTreeManifest(census)...)
			readyNS, nsErr := observeInputTreeNamespaces(tree.pid)
			if err != nil || !bytes.Equal(ready, expected) || nsErr != nil || namespaces != readyNS || tree.checkStage() != nil ||
				observeInputTreeDroppedPrivileges(tree.pid) != nil || t4013.MatchLinuxProcessExecutableImage(ctx, tree.pid, image) != nil {
				return ErrLinuxInputTree
			}
			tree.kernel, err = observeInputTreeRoot(tree.root)
			if err != nil || observeInputTreeHelperMount(tree.pid, tree.stagePath, tree.kernel.mount) != nil {
				return ErrLinuxInputTree
			}
			if closeInputTreeSources(roots) != nil {
				return ErrLinuxInputTree
			}
			roots = nil
			if os.RemoveAll(payload) != nil {
				return ErrLinuxInputTree
			}
			if _, err := os.Lstat(payload); !errors.Is(err, os.ErrNotExist) || wire.send(ctx, treeWireAck, initial[16:], nil) != nil {
				return ErrLinuxInputTree
			}
			result, _, err := wire.receive(ctx, treeWireEnd, false)
			if err != nil || len(result) != 128 || !bytes.Equal(result[:96], expected) {
				return ErrLinuxInputTree
			}
			output := filepath.Join(workspace, "output")
			info, infoErr := buildinfo.ReadFile(output)
			file, openErr := os.Open(output)
			if infoErr != nil || openErr != nil {
				if file != nil {
					_ = file.Close()
				}
				return ErrLinuxInputTree
			}
			digest, digestErr := digestExecutionReferenceFD(ctx, file)
			closeErr = file.Close()
			if digestErr != nil || closeErr != nil || digest != "sha256:"+hex.EncodeToString(result[96:]) || !neutralGoBuildInfo(info, revision) ||
				wire.send(ctx, treeWireAck, initial[16:], nil) != nil {
				return ErrLinuxInputTree
			}
			tree.identity = LinuxInputTreeIdentity{Entries: census.inventory.entries, Files: census.inventory.files, Bytes: census.inventory.bytes, PathBytes: census.inventory.pathBytes}
			return nil
		}()
		deadline, _ := ctx.Deadline()
		if useErr != nil || ctx.Err() != nil {
			if pidfdErr == nil {
				_ = unix.PidfdSendSignal(pidfd, unix.SIGKILL, nil, 0)
			}
			deadline = time.Now().Add(5 * time.Second)
		}
		waitErr, joined := waitExecutionProcessRoot(waited, deadline)
		if !joined {
			if pidfdErr == nil {
				_ = unix.PidfdSendSignal(pidfd, unix.SIGKILL, nil, 0)
			}
			waitErr, joined = <-waited, true // Retained worker; no invented join.
			useErr = ErrLinuxInputTree
		}
		tree.joined, tree.empty = joined, t4013.WaitPrivateProcessSession(tree.pid, time.Now().Add(5*time.Second)) == nil
		if useErr != nil || waitErr != nil || !tree.empty || ctx.Err() != nil || stderr.err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "namespace fixture pid=%d joined=%t empty=%t protocol_error=%v wait_error=%v stderr=%q\n", tree.pid, joined, tree.empty, useErr, waitErr, stderr.buffer.String())
			return ErrLinuxInputTree
		}
		return nil
	})
	if err != nil || tree.root == nil || !tree.joined || !tree.empty || tree.checkRoot() != nil {
		tree.err = ErrLinuxInputTree
	}
}
