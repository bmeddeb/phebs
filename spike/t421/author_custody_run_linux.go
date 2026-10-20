//go:build linux

package t421

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

// AuthorNext runs only the next fixed revision. Cancellation returns a failed
// prefix while a tracked worker retains every loan until actual Wait and SID
// drainage. A failed operation is sticky; there is no retry or caller response.
// Until done, returned mechanical fields are unavailable, not proof of no Start;
// Results publishes the actual prefix after the retained worker completes.
func (owner *LinuxExecutionAuthorCustody) AuthorNext(ctx context.Context) (ExecutionAuthorResult, error) {
	if owner == nil || ctx == nil || ctx.Err() != nil {
		return ExecutionAuthorResult{}, ErrExecutionAuthorCustody
	}
	owner.mu.Lock()
	if owner.closing || owner.released || owner.err != nil || owner.active != nil || owner.next >= 3 {
		owner.mu.Unlock()
		return ExecutionAuthorResult{}, ErrExecutionAuthorCustody
	}
	index := owner.next
	if owner.checkRoots(ctx) != nil || owner.checkSource(ctx, owner.previous) != nil {
		owner.err = ErrExecutionAuthorCustody
		owner.mu.Unlock()
		return ExecutionAuthorResult{}, owner.err
	}
	operation, cancel := context.WithTimeout(ctx, owner.projection.deadlines[index])
	attempt := &linuxAuthorAttempt{done: make(chan struct{}), cancel: cancel, result: ExecutionAuthorResult{Revision: owner.projection.expected[index].Name, ProducerID: 1}}
	owner.active = attempt
	owner.mu.Unlock()
	go owner.run(operation, attempt, index)
	select {
	case <-attempt.done:
		return cloneAuthorCustodyResult(attempt.result), attempt.err
	case <-operation.Done():
		owner.mu.Lock()
		select {
		case <-attempt.done:
			owner.mu.Unlock()
			return cloneAuthorCustodyResult(attempt.result), attempt.err
		default:
		}
		owner.err = ErrExecutionAuthorCustody
		owner.mu.Unlock()
		return ExecutionAuthorResult{Revision: owner.projection.expected[index].Name, ProducerID: 1}, ErrExecutionAuthorCustody
	}
}

func (owner *LinuxExecutionAuthorCustody) run(ctx context.Context, attempt *linuxAuthorAttempt, index int) {
	defer attempt.cancel()
	attempt.step = "loans"
	// This fixed order is the only cross-owner consumer loan. No hook reenters it.
	builds, git := owner.request.Builds, owner.request.Git
	builds.mu.Lock()
	if !linuxAuthorBuildBinding(owner.request, owner.authorIdentity, builds.request.PlanSourceCommit) {
		attempt.err = ErrExecutionAuthorCustody
	} else {
		git.mu.Lock()
		if git.closing || git.released || git.err != nil || git.tree == nil {
			attempt.err = ErrExecutionAuthorCustody
		} else {
			attempt.err = git.tree.WithRoot(context.Background(), func(root *os.File) error {
				return owner.request.Author.WithImage(context.Background(), "t422-author", func(identity ExecutionToolIdentity, image *os.File) error {
					if identity != owner.authorIdentity {
						return ErrExecutionAuthorCustody
					}
					return owner.request.Plan.WithInput(context.Background(), "plan", func(plan *os.File) error { return owner.runBorrowed(ctx, attempt, index, root, image, plan) })
				})
			})
		}
		git.mu.Unlock()
	}
	builds.mu.Unlock()
	owner.mu.Lock()
	if attempt.err != nil || owner.err != nil || owner.closing || ctx.Err() != nil {
		owner.err = ErrExecutionAuthorCustody
		attempt.err = ErrExecutionAuthorCustody
	} else {
		response := cloneCorpusAuthorResponse(*attempt.result.Response)
		owner.previous = &response
		owner.next++
		attempt.result.Completed = true
	}
	owner.results = append(owner.results, cloneAuthorCustodyResult(attempt.result))
	owner.last = attempt
	owner.active = nil
	// Publish fields only after the entire worker, callback and loan release.
	close(attempt.done)
	owner.mu.Unlock()
}

func (owner *LinuxExecutionAuthorCustody) runBorrowed(ctx context.Context, attempt *linuxAuthorAttempt, index int, gitRoot, image, plan *os.File) (retErr error) {
	attempt.step = "input-fence"
	if ctx.Err() != nil || owner.checkRoots(ctx) != nil {
		return ErrExecutionAuthorCustody
	}
	var imageStat, planStat unix.Stat_t
	if unix.Fstat(int(image.Fd()), &imageStat) != nil || unix.Fstat(int(plan.Fd()), &planStat) != nil {
		return ErrExecutionAuthorCustody
	}
	imageEntry := linuxExecutionInput{stat: imageStat, identity: LinuxExecutionInputIdentity{Executable: true, Bytes: imageStat.Size}}
	planEntry := linuxExecutionInput{stat: planStat, identity: LinuxExecutionInputIdentity{Bytes: planStat.Size}}
	gitKernel, err := observeInputTreeRoot(gitRoot)
	if err != nil {
		return ErrExecutionAuthorCustody
	}
	// All state below is borrowed under the fixed lock order; no public Check
	// method can recursively acquire those locks from the remote dispatch hook.
	check := func(checkCtx context.Context) error {
		after, e := observeInputTreeRoot(gitRoot)
		if e != nil || after.mount != gitKernel.mount || after.fsid != gitKernel.fsid || !sameLinuxInputStat(after.stat, gitKernel.stat) || !linuxInputProtected(image, imageEntry) || !linuxInputProtected(plan, planEntry) || owner.checkRoots(checkCtx) != nil || owner.request.Builds.tree.checkRoot() != nil || !linuxAuthorBuildBinding(owner.request, owner.authorIdentity, owner.request.Builds.request.PlanSourceCommit) {
			return ErrExecutionAuthorCustody
		}
		return nil
	}
	if check(ctx) != nil {
		return ErrExecutionAuthorCustody
	}
	request := linuxCorpusAuthorRequest{Schema: linuxCorpusAuthorRequestSchema, PlanSHA256: owner.planSHA256, AuthorSHA256: owner.authorIdentity.SHA256, SourcePath: owner.roots[1].path, SourceIdentity: owner.roots[1].identity, Controller: os.Getpid(), GitFD: int(gitRoot.Fd()), Revision: owner.projection.expected[index].Name}
	if owner.previous != nil {
		value := cloneCorpusAuthorResponse(*owner.previous)
		request.Previous = &value
	}
	raw, err := corpusAuthorCanonical(request, MaxExecutionCorpusAuthorRequestBytes)
	if err != nil {
		return ErrExecutionAuthorCustody
	}
	var binding [32]byte
	if _, err := rand.Read(binding[:]); err != nil || binding == ([32]byte{}) {
		return ErrExecutionAuthorCustody
	}
	config, controlConfig := linuxAuthorCustodyConfig(index, binding)
	lifetime, release := context.WithCancel(context.Background())
	defer release()
	controller, err := dispatchadmission.New(lifetime, config)
	if err != nil {
		return ErrExecutionAuthorCustody
	}
	var files [8]*os.File
	defer func() {
		for _, file := range files {
			if file != nil {
				if err := file.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
					retErr = ErrExecutionAuthorCustody
				}
			}
		}
	}()
	for i := 0; i < len(files); i += 2 {
		files[i], files[i+1], err = dispatchadmission.NewPipe()
		if err != nil {
			return ErrExecutionAuthorCustody
		}
	}
	input, err := adoptAuthorCustodySocket(files[4])
	files[4] = nil
	if err != nil {
		return ErrExecutionAuthorCustody
	}
	defer func() {
		if input.Close() != nil {
			retErr = ErrExecutionAuthorCustody
		}
	}()
	output, err := adoptAuthorCustodySocket(files[6])
	files[6] = nil
	if err != nil {
		return ErrExecutionAuthorCustody
	}
	defer func() {
		if output.Close() != nil {
			retErr = ErrExecutionAuthorCustody
		}
	}()
	core := linuxAuthorGitCore(os.Getpid(), int(gitRoot.Fd()))
	environment := executionGitEnvironment(core, owner.roots[2].path, owner.roots[3].path)
	command := exec.Command("/proc/self/fd/5")
	command.Dir, command.Env = owner.roots[0].path, append(append([]string(nil), environment...), dispatchadmission.ProductionEnvironment+"="+dispatchadmission.ProductionSelector)
	command.ExtraFiles = []*os.File{files[1], files[3], image, plan}
	command.Stdin, command.Stdout = files[5], files[7]
	operation, cancel := context.WithCancel(ctx)
	defer cancel()
	stderr := &checkoutCommandOutput{remaining: 8 << 10, cancel: cancel}
	command.Stderr = stderr
	command.WaitDelay = 0
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Pdeathsig: syscall.SIGKILL}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	attempt.step = "start"
	if command.Start() != nil {
		_ = controller.CancelUnused(1)
		return ErrExecutionAuthorCustody
	}
	attempt.pid = command.Process.Pid
	attempt.result.RootStarted = true
	pidfd, waited, pinErr := pinExecutionProcessRoot(command)
	if pinErr == nil {
		defer func() { _ = unix.Close(pidfd) }()
	}
	for _, i := range []int{1, 3, 5, 7} {
		if files[i].Close() != nil {
			retErr = ErrExecutionAuthorCustody
		}
		files[i] = nil
	}
	canceled := make(chan struct{})
	stop := context.AfterFunc(operation, func() {
		defer close(canceled)
		_ = input.SetDeadline(time.Now())
		_ = output.SetDeadline(time.Now())
		if pinErr == nil {
			_ = unix.PidfdSendSignal(pidfd, unix.SIGKILL, nil, 0)
		}
	})
	defer func() {
		if !stop() {
			<-canceled
		}
	}()
	var control *dispatchadmission.PhaseControl
	var served <-chan error
	if pinErr != nil {
		retErr = ErrExecutionAuthorCustody
	}
	bootstrap := dispatchadmission.ProductionBootstrap{Program: dispatchadmission.ProgramCorpusAuthor, InputSHA256: sha256.Sum256(raw), Producer: config.Producers[0], Phase: 1, Limits: config.Limits, Control: controlConfig, Tools: []dispatchadmission.ProductionToolBinding{{Role: "git", Path: core + "/git", Environment: environment}}}
	attempt.step = "bootstrap"
	if retErr == nil && dispatchadmission.SendProductionBootstrap(operation, files[0], files[2], bootstrap) != nil {
		retErr = ErrExecutionAuthorCustody
	}
	if retErr == nil {
		control, err = dispatchadmission.NewPhaseControl(lifetime, files[2], binding, controlConfig)
		files[2] = nil
		if err != nil {
			retErr = ErrExecutionAuthorCustody
		} else {
			completion := make(chan error, 1)
			server := files[0]
			files[0] = nil
			go func() {
				completion <- controller.ServeChecked(lifetime, 1, attempt.pid, server, func(hookCtx context.Context, site dispatchadmission.Site) error {
					if site.ID != dispatchadmission.SiteCorpusAuthorGit || site.Role != dispatchadmission.RoleGit || site.Persistent || check(hookCtx) != nil || t4013.MatchLinuxProcessExecutableImage(hookCtx, attempt.pid, image) != nil {
						return ErrExecutionAuthorCustody
					}
					return nil
				})
			}()
			served = completion
		}
	}
	attempt.step = "response"
	reader := bufio.NewReaderSize(output, MaxExecutionCorpusAuthorResponseBytes+1)
	if retErr == nil {
		retErr = writeAuthorCustodyRequest(operation, input, raw)
	}
	if retErr == nil {
		responseRaw, e := reader.ReadSlice('\n')
		response, responseErr := authorCustodyCanonicalResponseFor(responseRaw, owner.projection.expected[index], true)
		if e != nil || responseErr != nil || operation.Err() != nil || owner.previous != nil && response.ConfigSHA256 != owner.previous.ConfigSHA256 {
			retErr = ErrExecutionAuthorCustody
		} else {
			attempt.result.Response = &response
			// TerminalAuthor authenticates the Pause echo and the child's own DA Close.
			// No checkpoint substitutes for terminal completion.
			attempt.step = "terminal-pause"
			if control.Pause(operation) != nil {
				retErr = ErrExecutionAuthorCustody
			}
		}
	}
	if retErr != nil {
		_ = controller.Fence()
		cancel()
		if served == nil {
			_ = controller.CancelUnused(1)
		}
	}
	if served == nil {
		// Pin refusal cannot signal by numeric PID. Closing bootstrap endpoints lets
		// the still-owned child observe EOF; any uncertain child retains all loans.
		for _, i := range []int{0, 2} {
			if files[i] != nil {
				_ = files[i].Close()
				files[i] = nil
			}
		}
	}
	if retErr == nil {
		attempt.step = "actual-wait"
	}
	waitErr := <-waited
	attempt.result.RootJoined = true
	if retErr == nil {
		attempt.step = "session-drain"
	}
	for t4013.WaitPrivateProcessSession(attempt.pid, time.Now().Add(5*time.Second)) != nil {
		time.Sleep(5 * time.Second)
	}
	attempt.result.SessionEmpty = true
	if retErr == nil {
		attempt.step = "transport-close"
	}
	if served != nil {
		if e := <-served; e != nil {
			retErr = ErrExecutionAuthorCustody
		}
	}
	if control != nil && control.Close() != nil {
		retErr = ErrExecutionAuthorCustody
	}
	// Natural producer close precedes this final accounting fence.
	if controller.Fence() != nil {
		retErr = ErrExecutionAuthorCustody
	}
	attempt.result.Accounting, err = controller.Snapshot()
	attempt.stderr = append([]byte(nil), stderr.buffer.Bytes()...)
	if waitErr != nil || err != nil || stderr.err != nil || operation.Err() != nil || !attempt.result.Accounting.Complete || !authorCustodyProducerComplete(attempt.result.Accounting, 1, authorCustodyAttempts(index)) {
		retErr = ErrExecutionAuthorCustody
	}
	if retErr == nil {
		attempt.step = "continuity"
	}
	if retErr == nil && (readAuthorCustodyEOF(operation, output, reader) != nil || check(operation) != nil || owner.checkSource(operation, attempt.result.Response) != nil) {
		retErr = ErrExecutionAuthorCustody
	}
	if retErr == nil {
		attempt.step = "complete"
	}
	return retErr
}

func linuxAuthorCustodyConfig(index int, binding [32]byte) (dispatchadmission.Config, dispatchadmission.PhaseControlConfig) {
	config, control := authorCustodyConfig(index, binding)
	// One terminal Pause request/echo plus the receiver's EOF reservation.
	control.TerminalAuthor = true
	control.MaximumWireBytes = 4 * dispatchadmission.FrameBytes
	return config, control
}
