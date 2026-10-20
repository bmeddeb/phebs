//go:build linux && phebs_t42_native

package t421

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

type authorGitRouteReport struct {
	Nonce, SHA256, Version, ExecPath, Commit, ContentSHA256 string
	Roots, TraceChildExits                                  []int
}

type authorGitRouteAttempt struct {
	done           chan struct{}
	pid            int
	joined, empty  bool
	err            error
	stdout, stderr []byte
}

// These occupied sockets are collision controls, not DA01/PC01 endpoints. The
// self-test image is diagnostic bytes, never reference-issued tool authority.
func TestLinuxAuthorGitRouteProbeChild(t *testing.T) {
	if os.Getenv("PHEBS_T42_CONTROLLER_GIT_ROUTE_CHILD") != authorGitRouteSchema {
		return
	}
	report, err := runAuthorGitRouteChild()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "controller route diagnostic refused: %v\n", err)
		os.Exit(125)
	}
	if json.NewEncoder(os.Stdout).Encode(report) != nil {
		os.Exit(126)
	}
	os.Exit(0)
}

func runAuthorGitRouteChild() (authorGitRouteReport, error) {
	var report authorGitRouteReport
	for _, fd := range []int{3, 4, 5} {
		unix.CloseOnExec(fd)
		flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
		if err != nil || flags&unix.FD_CLOEXEC == 0 {
			return report, errors.New("CLOEXEC collision control")
		}
		if fd != 5 {
			kind, err := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_TYPE)
			if err != nil || kind != unix.SOCK_SEQPACKET {
				return report, errors.New("occupied socket collision control")
			}
		}
	}
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, 4097))
	request, decodeErr := decodeAuthorGitRouteRequest(raw, os.Getppid())
	if err != nil || decodeErr != nil || request.Workspace != mustAuthorGitRouteCWD() || !linuxGitPrivateDirectory(request.Workspace) {
		return report, errors.New("closed child request")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	wire := &inputTreeSocket{fd: 3}
	if wire.send(ctx, treeWireReady, []byte(request.Nonce), nil) != nil {
		return report, errors.New("ready send")
	}
	ack, _, err := wire.receive(ctx, treeWireAck, false)
	if err != nil || string(ack) != request.Nonce {
		return report, errors.New("image observation acknowledgement")
	}
	core := request.core()
	for _, alias := range executionGitImageNames() {
		path := filepath.Join(core, alias)
		file, err := os.Open(path)
		if err != nil {
			return report, fmt.Errorf("routed alias open: %w", err)
		}
		digest, digestErr := digestExecutionReferenceFD(ctx, file)
		closeErr := file.Close()
		imageErr := validateExternalToolNativeImage(path)
		if digestErr != nil || closeErr != nil || digest != request.SHA256 || imageErr != nil {
			return report, fmt.Errorf("routed alias=%s actual_digest=%q digest_error=%v close_error=%v image_error=%v", alias, digest, digestErr, closeErr, imageErr)
		}
	}
	var trace bytes.Buffer
	run := func(input string, args ...string) (string, error) {
		commandCtx, commandCancel := context.WithTimeout(ctx, 10*time.Second)
		defer commandCancel()
		argv := append([]string{"--no-replace-objects", "-c", "core.commitGraph=false"}, args...)
		command := exec.CommandContext(commandCtx, core+"/git", argv...)
		command.Dir, command.Env = request.Workspace, executionGitEnvironment(core, filepath.Join(request.Workspace, "home"), filepath.Join(request.Workspace, "tmp"))
		command.Env = append(command.Env, "GIT_TRACE=1", "GIT_TRACE2_EVENT=1") // Bounded diagnostic trace only.
		command.Stdin = strings.NewReader(input)
		stdout, stderr := checkoutCommandOutput{remaining: 16 << 10, cancel: commandCancel}, checkoutCommandOutput{remaining: 64 << 10, cancel: commandCancel}
		command.Stdout, command.Stderr, command.WaitDelay = &stdout, &stderr, time.Second
		if len(command.ExtraFiles) != 0 || prepareReferenceCommand(command) != nil || command.Start() != nil {
			return "", errors.New("fixed nested Git start")
		}
		// Each Git gets a process group, retaining the diagnostic root's session.
		// Root Wait + that complete session's empty observation is parent-owned.
		report.Roots = append(report.Roots, command.Process.Pid)
		waitErr := command.Wait()
		if stderr.buffer.Len() > (128<<10)-trace.Len() {
			return "", errors.New("aggregate Git trace bound")
		}
		trace.Write(stderr.buffer.Bytes())
		if waitErr != nil || stdout.err != nil || stderr.err != nil || commandCtx.Err() != nil {
			return "", fmt.Errorf("fixed Git wait=%v stderr=%q", waitErr, stderr.buffer.Bytes())
		}
		return stdout.buffer.String(), nil
	}
	version, err := run("", "version")
	if err != nil || version != request.Version+"\n" {
		return report, fmt.Errorf("version fence actual=%q expected=%q command_error=%v", version, request.Version, err)
	}
	execPath, err := run("", "--exec-path")
	if err != nil || execPath != core+"\n" {
		return report, fmt.Errorf("exec-path fence actual=%q expected=%q command_error=%v", execPath, core, err)
	}
	bare, mirror := filepath.Join(request.Workspace, "source.git"), filepath.Join(request.Workspace, "mirror.git")
	if _, err := run("", "init", "--bare", "--initial-branch=main", bare); err != nil {
		return report, err
	}
	const content = "controller descriptor route\n"
	stream := fmt.Sprintf("blob\nmark :1\ndata %d\n%s\ncommit refs/heads/main\ncommitter Neutral <neutral@example.invalid> 1700000000 +0000\ndata 7\nneutral\nM 100644 :1 file.txt\n\ndone\n", len(content), content)
	if _, err := run(stream, "-C", bare, "fast-import", "--quiet", "--date-format=raw"); err != nil {
		return report, err
	}
	commit, err := run("", "-C", bare, "rev-parse", "HEAD")
	if err != nil || !validCommit(strings.TrimSuffix(commit, "\n")) {
		return report, fmt.Errorf("fixed source commit actual=%q command_error=%v", commit, err)
	}
	if _, err := run("", "clone", "--mirror", (&url.URL{Scheme: "file", Path: bare}).String(), mirror); err != nil {
		return report, err
	}
	cloned, err := run("", "-C", mirror, "rev-parse", "HEAD")
	if err != nil || cloned != commit {
		return report, fmt.Errorf("fixed cloned commit actual=%q expected=%q command_error=%v", cloned, commit, err)
	}
	actual, err := run("", "-C", mirror, "cat-file", "blob", "HEAD:file.txt")
	if err != nil || actual != content || !strings.Contains(trace.String(), "trace: built-in: git upload-pack ") ||
		!strings.Contains(trace.String(), "trace: start_command: "+core+"/git pack-objects ") {
		return report, fmt.Errorf("fixed clone/content/pack route: command_error=%v trace=%q", err, trace.Bytes())
	}
	for _, line := range bytes.Split(trace.Bytes(), []byte{'\n'}) {
		var event struct {
			Event     string
			Pid, Code int
		}
		if json.Unmarshal(line, &event) == nil && event.Event == "child_exit" {
			if event.Pid <= 0 || event.Code != 0 || len(report.TraceChildExits) >= 32 {
				return report, errors.New("bounded descendant trace exit")
			}
			report.TraceChildExits = append(report.TraceChildExits, event.Pid)
		}
	}
	if len(report.Roots) != 8 || len(report.TraceChildExits) < 2 {
		return report, errors.New("fixed root/observed descendant trace count")
	}
	report.Nonce, report.SHA256, report.Version, report.ExecPath = request.Nonce, request.SHA256, request.Version, core
	report.Commit, report.ContentSHA256 = strings.TrimSuffix(commit, "\n"), SHA256([]byte(actual))
	return report, nil
}

func mustAuthorGitRouteCWD() string { path, _ := os.Getwd(); return path }

func TestLinuxAuthorGitRouteRequiredNativeControllerDescriptor(t *testing.T) {
	if os.Getuid() == 0 || os.Geteuid() == 0 {
		t.Fatal("unprivileged required native diagnostic; no skip")
	}
	request := readNativeGoReferenceRequest(t, "PHEBS_T42_INPUT_TREE_REQUEST")
	original := requireLinuxExternalTool(t, "git")
	var before unix.Stat_t
	baseline, err := goBuildImportHostRoot()
	if err != nil || unix.Stat(original, &before) != nil {
		t.Fatal("host original/propagation baseline")
	}
	installed, err := os.Open(original)
	if err != nil {
		t.Fatal(err)
	}
	installedDigest, digestErr := digestExecutionReferenceFD(t.Context(), installed)
	closeErr := installed.Close()
	if digestErr != nil || closeErr != nil {
		t.Fatal("host Git digest")
	}
	t.Cleanup(func() {
		var after unix.Stat_t
		propagation, err := goBuildImportHostRoot()
		if err != nil || propagation != baseline || unix.Stat(original, &after) != nil || !sameLinuxInputStat(before, after) {
			t.Error("host original/propagation changed")
		}
		file, err := os.Open(original)
		if err != nil {
			t.Error("original host Git became unavailable")
			return
		}
		digest, digestErr := digestExecutionReferenceFD(context.Background(), file)
		closeErr := file.Close()
		if digestErr != nil || closeErr != nil || digest != installedDigest {
			t.Error("original host Git bytes changed")
		}
	})
	base, err := os.MkdirTemp("", "phebs-controller-git-route-")
	if err != nil {
		t.Fatal(err)
	}
	baseInfo, err := os.Lstat(base)
	if err != nil {
		t.Fatal("fixture identity; retain", base)
	}
	parent, work, selected := filepath.Join(base, "parent"), filepath.Join(base, "work"), filepath.Join(base, "selected-git")
	for _, path := range []string{parent, work, filepath.Join(work, "home"), filepath.Join(work, "tmp")} {
		if os.Mkdir(path, 0o700) != nil {
			t.Fatal("fixture directory; retain", base)
		}
	}
	var helper *LinuxInputTreeHelper
	var git *LinuxExecutionGitCustody
	var image *LinuxExecutionInputCustody
	var attempt *authorGitRouteAttempt
	success := false
	t.Cleanup(func() {
		if attempt != nil {
			select {
			case <-attempt.done:
			default:
				t.Error("unfinished worker retains image/Git/helper and fixture", base)
				return
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if git != nil {
			if git.Close(ctx) != nil {
				t.Error("Git Close returned sticky or cleanup refusal")
			}
			if !git.released {
				t.Error("Git retains exact fixture", base)
				return
			}
		}
		if image != nil {
			if image.Close() != nil {
				t.Error("diagnostic image Close returned sticky or cleanup refusal")
			}
			if !image.closed {
				t.Error("image retained", base)
				return
			}
		}
		if helper != nil && helper.Close() != nil {
			t.Error("helper retained", base)
			return
		}
		if !success || t.Failed() {
			t.Log("failed diagnostic exact fixture retained", base)
			return
		}
		current, err := os.Lstat(base)
		if err != nil || !os.SameFile(baseInfo, current) || os.RemoveAll(base) != nil {
			t.Error("exact fixture cleanup refused", base)
		}
	})
	helper, err = ProtectLinuxInputTreeHelper(t.Context(), request)
	if err != nil {
		t.Fatal("independently verified H2k helper refused")
	}
	source, err := os.Open(original)
	if err != nil {
		t.Fatal(err)
	}
	copy, err := os.OpenFile(selected, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		_ = source.Close()
		t.Fatal(err)
	}
	_, copyErr := io.Copy(copy, io.LimitReader(source, maxInputCustodyFileBytes+1))
	sourceErr, copyCloseErr := source.Close(), copy.Close()
	if copyErr != nil || sourceErr != nil || copyCloseErr != nil {
		t.Fatal("private Git copy")
	}
	git, err = ProtectLinuxExecutionGit(t.Context(), parent, selected, helper)
	if err != nil {
		t.Fatal("genuine H2p Git prerequisite")
	}
	identity, err := git.Check(t.Context())
	if err != nil || identity.SHA256 != installedDigest || os.Remove(selected) != nil {
		t.Fatal("Git identity/private original retirement")
	}
	tree, err := git.tree.Check(t.Context())
	if err != nil || tree.Files != 7 || tree.Entries != 8 || tree.Bytes == 0 {
		t.Fatal("genuine protected seven-alias census")
	}
	t.Logf("protected_git_helper=%s files=%d bytes=%d", tree.HelperSHA256, tree.Files, tree.Bytes)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(executable)
	if err != nil {
		t.Fatal(err)
	}
	imageDigest, digestErr := digestExecutionReferenceFD(t.Context(), file)
	closeErr = file.Close()
	if digestErr != nil || closeErr != nil {
		t.Fatal("diagnostic image digest")
	}
	image, err = ProtectLinuxExecutionInputs(t.Context(), []ExecutionInputCopy{{Name: "route-diagnostic", Path: executable, SHA256: imageDigest, Executable: true}})
	if err != nil {
		t.Fatal("sealed diagnostic image")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	attempt = &authorGitRouteAttempt{done: make(chan struct{})}
	go runAuthorGitRouteAttempt(ctx, attempt, git, image, work, identity)
	select {
	case <-attempt.done:
	case <-ctx.Done():
		t.Fatal("unknown route outcome retains worker and exact fixture", base)
	}
	t.Logf("route pid=%d actual_root_joined=%t session_empty=%t stderr=%q", attempt.pid, attempt.joined, attempt.empty, attempt.stderr)
	var report authorGitRouteReport
	if attempt.err != nil || !attempt.joined || !attempt.empty || json.Unmarshal(attempt.stdout, &report) != nil || report.SHA256 != identity.SHA256 || report.Version != identity.Version ||
		len(report.Roots) != 8 || len(report.TraceChildExits) < 2 || report.ContentSHA256 != SHA256([]byte("controller descriptor route\n")) {
		t.Fatal("required controller route refused; no fallback", attempt.err)
	}
	if _, err := os.Lstat(selected); !os.IsNotExist(err) {
		t.Fatal("private selected Git source reappeared")
	}
	t.Logf("diagnostic_image=%s git=%s version=%q direct_git_actual_wait_roots=%v trace_observed_child_exits=%v commit=%s exec_path=%s", imageDigest, identity.SHA256, identity.Version, report.Roots, report.TraceChildExits, report.Commit, report.ExecPath)
	success = true
}

func runAuthorGitRouteAttempt(ctx context.Context, attempt *authorGitRouteAttempt, git *LinuxExecutionGitCustody, image *LinuxExecutionInputCustody, work string, identity ExecutionToolIdentity) {
	defer close(attempt.done)
	// Test-local prospective routing avoids changing WithGit's runtime FD3
	// contract. No root duplication/escape: its loan stays inside this callback
	// through actual native Wait and complete session drain.
	attempt.err = image.WithInput(ctx, "route-diagnostic", func(image *os.File) error {
		git.mu.Lock()
		defer git.mu.Unlock()
		if git.closing || git.err != nil || git.identity != identity {
			return ErrExecutionGitCustody
		}
		return git.tree.WithRoot(ctx, func(root *os.File) error {
			var nonce [32]byte
			if _, err := rand.Read(nonce[:]); err != nil {
				return err
			}
			request := authorGitRouteRequest{Schema: authorGitRouteSchema, Nonce: hex.EncodeToString(nonce[:]), Workspace: work, SHA256: identity.SHA256,
				Version: identity.Version, Controller: os.Getpid(), LoanFD: int(root.Fd())}
			raw, err := json.Marshal(request)
			if err != nil {
				return err
			}
			var sockets [4]*os.File
			defer func() {
				for _, socket := range sockets {
					if socket != nil {
						_ = socket.Close()
					}
				}
			}()
			for i := 0; i < 4; i += 2 {
				pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_NONBLOCK|unix.SOCK_CLOEXEC, 0)
				if err != nil {
					return err
				}
				sockets[i], sockets[i+1] = os.NewFile(uintptr(pair[0]), "route-controller"), os.NewFile(uintptr(pair[1]), "route-child")
			}
			if unix.SetsockoptInt(int(sockets[0].Fd()), unix.SOL_SOCKET, unix.SO_PASSCRED, 1) != nil {
				return ErrExecutionGitCustody
			}
			command := exec.Command("/proc/self/fd/5", "-test.run=^TestLinuxAuthorGitRouteProbeChild$")
			command.Dir, command.Env = work, []string{"PHEBS_T42_CONTROLLER_GIT_ROUTE_CHILD=" + authorGitRouteSchema, "GOMAXPROCS=2"}
			command.ExtraFiles, command.Stdin = []*os.File{sockets[1], sockets[3], image}, bytes.NewReader(raw)
			command.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Pdeathsig: syscall.SIGKILL}
			commandCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			stdout, stderr := checkoutCommandOutput{remaining: 16 << 10, cancel: cancel}, checkoutCommandOutput{remaining: 512 << 10, cancel: cancel}
			command.Stdout, command.Stderr, command.WaitDelay = &stdout, &stderr, time.Second
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			if command.Start() != nil {
				return ErrExecutionGitCustody
			}
			attempt.pid = command.Process.Pid
			controlCloseErr, collisionCloseErr := sockets[1].Close(), sockets[3].Close()
			sockets[1], sockets[3] = nil, nil
			waited := make(chan error, 1)
			go func() { waited <- command.Wait() }()
			pidfd, pidfdErr := unix.PidfdOpen(attempt.pid, 0)
			if pidfdErr == nil {
				defer func() { _ = unix.Close(pidfd) }()
			}
			wire := &inputTreeSocket{fd: int(sockets[0].Fd()), peer: &unix.Ucred{Pid: int32(attempt.pid), Uid: uint32(os.Geteuid()), Gid: uint32(os.Getegid())}}
			observed := false
			if controlCloseErr == nil && collisionCloseErr == nil && pidfdErr == nil {
				ready, _, readyErr := wire.receive(commandCtx, treeWireReady, false)
				observed = readyErr == nil && string(ready) == request.Nonce && t4013.MatchLinuxProcessExecutableImage(commandCtx, attempt.pid, image) == nil
				if observed {
					observed = wire.send(commandCtx, treeWireAck, []byte(request.Nonce), nil) == nil
				}
			}
			if !observed || pidfdErr != nil {
				cancel()
			}
			stopDone := make(chan struct{})
			stop := context.AfterFunc(commandCtx, func() {
				defer close(stopDone)
				if pidfdErr == nil {
					_ = unix.PidfdSendSignal(pidfd, unix.SIGKILL, nil, 0)
				}
			})
			defer func() {
				if !stop() {
					<-stopDone
				}
			}() // Join cancellation before closing or reusing its owned pidfd.
			waitErr := <-waited // Context/timeout never substitutes for actual Wait.
			attempt.joined = true
			for t4013.WaitPrivateProcessSession(attempt.pid, time.Now().Add(5*time.Second)) != nil {
				time.Sleep(5 * time.Second)
			}
			attempt.empty, attempt.stdout, attempt.stderr = true, bytes.Clone(stdout.buffer.Bytes()), bytes.Clone(stderr.buffer.Bytes())
			var report authorGitRouteReport
			if !observed || pidfdErr != nil || waitErr != nil || commandCtx.Err() != nil || stdout.err != nil || stderr.err != nil || json.Unmarshal(attempt.stdout, &report) != nil || report.Nonce != request.Nonce || report.ExecPath != request.core() {
				return fmt.Errorf("route/image/actual-wait fence: child_ends_closed=%t observed=%t pidfd=%t wait=%v", controlCloseErr == nil && collisionCloseErr == nil, observed, pidfdErr == nil, waitErr)
			}
			return nil
		})
	})
}
