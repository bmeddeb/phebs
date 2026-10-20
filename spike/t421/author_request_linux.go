//go:build linux

package t421

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"golang.org/x/sys/unix"
)

const linuxCorpusAuthorRequestSchema = "t422-author-request-linux-v1"

// LinuxExecutionCorpusSourceIdentity is private mutable-source custody. Native
// device, inode and mount values retain their full kernel widths.
type LinuxExecutionCorpusSourceIdentity struct {
	Device          uint64   `json:"device"`
	Inode           uint64   `json:"inode"`
	Mount           uint64   `json:"mount"`
	Filesystem      int64    `json:"filesystem"`
	FSID            [2]int32 `json:"fsid"`
	NamespaceDevice uint64   `json:"namespace_device"`
	NamespaceInode  uint64   `json:"namespace_inode"`
}

type linuxCorpusAuthorRequest struct {
	Schema         string                             `json:"schema"`
	PlanSHA256     string                             `json:"plan_sha256"`
	AuthorSHA256   string                             `json:"author_sha256"`
	SourcePath     string                             `json:"source_path"`
	SourceIdentity LinuxExecutionCorpusSourceIdentity `json:"source_identity"`
	Controller     int                                `json:"controller"`
	GitFD          int                                `json:"git_fd"`
	Revision       string                             `json:"revision"`
	Previous       *ExecutionCorpusAuthorResponse     `json:"previous"`
}

func decodeLinuxCorpusAuthorRequest(raw []byte, expected [32]byte, parent int) (linuxCorpusAuthorRequest, error) {
	var request linuxCorpusAuthorRequest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if len(raw) == 0 || len(raw) > MaxExecutionCorpusAuthorRequestBytes || expected == ([32]byte{}) || sha256.Sum256(raw) != expected || decoder.Decode(&request) != nil || !corpusAuthorJSONEOF(decoder) {
		return request, ErrExecutionCorpusAuthor
	}
	canonical, err := corpusAuthorCanonical(request, MaxExecutionCorpusAuthorRequestBytes)
	if err != nil || !bytes.Equal(canonical, raw) || request.Schema != linuxCorpusAuthorRequestSchema || !validExecutionSHA256(request.PlanSHA256) || !validExecutionSHA256(request.AuthorSHA256) ||
		!executionGitAbsolutePath(request.SourcePath) || request.Controller <= 1 || request.Controller != parent || request.GitFD < 3 || request.GitFD > 1<<20 || request.SourceIdentity.Inode == 0 || request.SourceIdentity.Mount == 0 || request.SourceIdentity.NamespaceInode == 0 ||
		(request.Revision != "a" && request.Revision != "b" && request.Revision != "a-return") || (request.Revision == "a") != (request.Previous == nil) || request.Previous != nil && !validChangedPhysicalFiles(request.Previous.ChangedPhysicalFiles, true) {
		return linuxCorpusAuthorRequest{}, ErrExecutionCorpusAuthor
	}
	return request, nil
}

// LinuxExecutionAuthorInputs captures fixed inherited descriptors before
// bootstrap adopts sockets and allocates descriptors. These are selected input
// bytes, not provenance or authority. Close releases only these owned FDs.
type LinuxExecutionAuthorInputs struct {
	image, plan           *os.File
	imageEntry, planEntry linuxExecutionInput
	used                  bool
}

// CaptureLinuxExecutionAuthorInputs adopts only proven sealed image/plan FDs
// 5/6 before bootstrap. Unproven descriptors are never wrapped or closed.
func CaptureLinuxExecutionAuthorInputs() (*LinuxExecutionAuthorInputs, error) {
	inputs := &LinuxExecutionAuthorInputs{}
	for _, selection := range []struct {
		fd         int
		executable bool
		target     **os.File
		entry      *linuxExecutionInput
	}{{5, true, &inputs.image, &inputs.imageEntry}, {6, false, &inputs.plan, &inputs.planEntry}} {
		var stat unix.Stat_t
		mode := uint32(unix.S_IFREG | 0o400)
		if selection.executable {
			mode = unix.S_IFREG | 0o500
		}
		seals, se := unix.FcntlInt(uintptr(selection.fd), unix.F_GET_SEALS, 0)
		flags, fe := unix.FcntlInt(uintptr(selection.fd), unix.F_GETFL, 0)
		if unix.Fstat(selection.fd, &stat) != nil || stat.Mode != mode || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 0 || stat.Size <= 0 || stat.Size > maxInputCustodyFileBytes || !selection.executable && stat.Size > MaxPlanBytes || se != nil || seals&linuxInputSeals != linuxInputSeals || fe != nil || flags&unix.O_ACCMODE != unix.O_RDONLY {
			_ = inputs.Close()
			return nil, ErrExecutionCorpusAuthor
		}
		// Adopt only after native checks prove this is a matching sealed input.
		unix.CloseOnExec(selection.fd)
		file := os.NewFile(uintptr(selection.fd), "linux-author-input")
		*selection.target = file
		*selection.entry = linuxExecutionInput{file: file, stat: stat, identity: LinuxExecutionInputIdentity{Executable: selection.executable, Bytes: stat.Size}}
		if !linuxInputProtected(file, *selection.entry) {
			_ = inputs.Close()
			return nil, ErrExecutionCorpusAuthor
		}
	}
	return inputs, nil
}
func (inputs *LinuxExecutionAuthorInputs) Close() error {
	if inputs == nil {
		return nil
	}
	var err error
	for _, f := range []*os.File{inputs.image, inputs.plan} {
		if f != nil {
			if e := f.Close(); e != nil && !errors.Is(e, os.ErrClosed) {
				err = ErrExecutionCorpusAuthor
			}
		}
	}
	inputs.image, inputs.plan = nil, nil
	return err
}

func observeLinuxAuthorRoot(file *os.File) (LinuxExecutionCorpusSourceIdentity, error) {
	var id LinuxExecutionCorpusSourceIdentity
	var stat, ns unix.Stat_t
	var fs unix.Statfs_t
	if file == nil || unix.Fstat(int(file.Fd()), &stat) != nil || stat.Mode != unix.S_IFDIR|0o700 || stat.Uid != uint32(os.Geteuid()) || stat.Nlink == 0 || unix.Fstatfs(int(file.Fd()), &fs) != nil || fs.Flags&unix.ST_RDONLY != 0 || unix.Stat("/proc/self/ns/mnt", &ns) != nil {
		return id, ErrExecutionCorpusAuthor
	}
	mount, err := inputTreeMountID(int(file.Fd()))
	flags, flagErr := unix.FcntlInt(file.Fd(), unix.F_GETFD, 0)
	if err != nil || flagErr != nil || flags&unix.FD_CLOEXEC == 0 {
		return id, ErrExecutionCorpusAuthor
	}
	return LinuxExecutionCorpusSourceIdentity{Device: stat.Dev, Inode: stat.Ino, Mount: mount, Filesystem: int64(fs.Type), FSID: fs.Fsid.Val, NamespaceDevice: ns.Dev, NamespaceInode: ns.Ino}, nil
}

type linuxCorpusAuthorState struct {
	identity LinuxExecutionCorpusSourceIdentity
	inputs   *LinuxExecutionAuthorInputs
	pending  *linuxCorpusAuthorCommand
}

func (state *linuxCorpusAuthorState) owned(info os.FileInfo) bool {
	if info == nil || info.Mode()&(os.ModeSymlink|os.ModeSetuid|os.ModeSetgid) != 0 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid()) && stat.Nlink > 0 && info.Mode().Perm()&0o022 == 0 && (info.IsDir() || info.Mode().IsRegular() && stat.Nlink == 1)
}
func (state *linuxCorpusAuthorState) check(ctx context.Context, author *ExecutionCorpusAuthor) error {
	if ctx == nil || ctx.Err() != nil || author.closed || author.err != nil || author.root == nil || author.rootInfo == nil {
		return ErrExecutionCorpusAuthor
	}
	canonical, err := filepath.EvalSymlinks(author.directory)
	current, pathErr := os.Lstat(author.directory)
	held, heldErr := author.root.Stat()
	id, idErr := observeLinuxAuthorRoot(author.root)
	if err != nil || pathErr != nil || heldErr != nil || idErr != nil || canonical != author.directory || id != state.identity || !os.SameFile(author.rootInfo, current) || !os.SameFile(held, current) || !state.owned(current) || state.checkPlan(ctx, author) != nil {
		return ErrExecutionCorpusAuthor
	}
	return nil
}
func (state *linuxCorpusAuthorState) checkPlan(ctx context.Context, _ *ExecutionCorpusAuthor) error {
	if ctx == nil || ctx.Err() != nil {
		return ErrExecutionCorpusAuthor
	}
	if state.inputs == nil {
		return nil
	}
	if !linuxInputProtected(state.inputs.plan, state.inputs.planEntry) || !linuxInputProtected(state.inputs.image, state.inputs.imageEntry) {
		return ErrExecutionCorpusAuthor
	}
	return nil
}

func openLinuxAuthorRoot(ctx context.Context, path string, identity LinuxExecutionCorpusSourceIdentity) (*ExecutionCorpusAuthor, error) {
	if !linuxGitPrivateDirectory(path) {
		return nil, ErrExecutionCorpusAuthor
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, ErrExecutionCorpusAuthor
	}
	author := &ExecutionCorpusAuthor{lifetime: ctx, directory: path, root: file, platform: &linuxCorpusAuthorState{identity: identity}}
	author.rootInfo, err = file.Stat()
	if err != nil || author.checkRoot(ctx) != nil {
		_ = author.Close()
		return nil, ErrExecutionCorpusAuthor
	}
	return author, nil
}

// OpenLinuxExecutionCorpusAuthorRequest authenticates the distinct closed wire
// before opening source paths. The fixed plan/image FDs were captured before
// bootstrap; neither a path alias nor caller-provided identity grants custody.
func OpenLinuxExecutionCorpusAuthorRequest(ctx context.Context, raw []byte, inputs *LinuxExecutionAuthorInputs) (*ExecutionCorpusAuthor, error) {
	if ctx == nil || ctx.Err() != nil || inputs == nil || inputs.used {
		return nil, ErrExecutionCorpusAuthor
	}
	expected, err := dispatchadmission.AuthorInputSHA256()
	if err != nil {
		return nil, ErrExecutionCorpusAuthor
	}
	request, err := decodeLinuxCorpusAuthorRequest(raw, expected, os.Getppid())
	if err != nil {
		return nil, err
	}
	if !linuxInputProtected(inputs.plan, inputs.planEntry) || !linuxInputProtected(inputs.image, inputs.imageEntry) {
		return nil, ErrExecutionCorpusAuthor
	}
	planRaw, err := io.ReadAll(executionInputReader{ctx, io.LimitReader(inputs.plan, MaxPlanBytes+1)})
	digest, digestErr := digestExecutionReferenceFD(ctx, inputs.image)
	if err != nil || digestErr != nil || SHA256(planRaw) != request.PlanSHA256 || digest != request.AuthorSHA256 || len(planRaw) > MaxPlanBytes {
		return nil, ErrExecutionCorpusAuthor
	}
	plan, err := DecodePlan(planRaw)
	if err != nil || !processAccountingPlanSemantics(plan.Schema) || ctx.Err() != nil {
		return nil, ErrExecutionCorpusAuthor
	}
	source, err := newCorpusAuthorSource(ctx, plan)
	if err != nil {
		return nil, err
	}
	author, err := openLinuxAuthorRoot(ctx, request.SourcePath, request.SourceIdentity)
	if err != nil {
		return nil, err
	}
	core := linuxAuthorGitCore(request.Controller, request.GitFD)
	if dispatchadmission.ProductionTool("git") != core+"/git" {
		_ = author.Close()
		return nil, ErrExecutionCorpusAuthor
	}
	inputs.used = true
	author.platform.(*linuxCorpusAuthorState).inputs = inputs
	author, err = resumeCorpusAuthorState(ctx, author, source, request.Revision, request.Previous, true, core+"/git", dispatchadmission.StartAuthor)
	if err != nil {
		return nil, err
	}
	author.lifetime = dispatchadmission.ProcessContext()
	return author, nil
}

// Kept lexical: resolving this descriptor route would select a covered host placeholder.
func linuxAuthorGitCore(controller, fd int) string {
	return "/proc/" + strconv.Itoa(controller) + "/fd/" + strconv.Itoa(fd) + "/core"
}

// One fixed serial Git command is pinned while unreaped before Handle.Wait can
// settle/reap it. Explicit cancellation joins before its owned pidfd closes;
// these commands have no exec.CommandContext watcher or numeric-PID fallback.
type linuxCorpusAuthorCommand struct {
	command  *exec.Cmd
	ctx      context.Context
	fd       int
	pinErr   error
	stop     func() bool
	canceled chan struct{}
}
type linuxCorpusAuthorWait struct {
	handle  dispatchadmission.Handle
	command *linuxCorpusAuthorCommand
}

func (wait *linuxCorpusAuthorWait) Wait() error {
	err := wait.handle.Wait()
	if wait.command.stop != nil && !wait.command.stop() {
		<-wait.command.canceled
	}
	if wait.command.fd >= 0 {
		if unix.Close(wait.command.fd) != nil {
			err = ErrExecutionCorpusAuthor
		}
		wait.command.fd = -1
	}
	if wait.command.pinErr != nil {
		err = ErrExecutionCorpusAuthor
	}
	return err
}
func (state *linuxCorpusAuthorState) prepare(ctx context.Context, command *exec.Cmd) error {
	state.pending = &linuxCorpusAuthorCommand{command: command, ctx: ctx, fd: -1}
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return nil
}
func (state *linuxCorpusAuthorState) start(ctx context.Context, command *exec.Cmd, start func(context.Context, *exec.Cmd) (dispatchadmission.Handle, error)) (corpusAuthorWait, error) {
	pending := state.pending
	if pending == nil || pending.command != command || command.Cancel != nil {
		return nil, ErrExecutionCorpusAuthor
	}
	state.pending = nil
	handle, err := start(ctx, command)
	if err != nil {
		return nil, err
	}
	pending.fd, pending.pinErr = unix.PidfdOpen(command.Process.Pid, 0)
	if pending.pinErr == nil {
		pending.canceled = make(chan struct{})
		pending.stop = context.AfterFunc(pending.ctx, func() { defer close(pending.canceled); _ = unix.PidfdSendSignal(pending.fd, unix.SIGKILL, nil, 0) })
	}
	return &linuxCorpusAuthorWait{handle: handle, command: pending}, nil
}
