package t421

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"slices"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/spike/t4013"
)

// ExecutionAuthorResult retains the actual validated child response beside
// mechanical facts and its committed accounting prefix. A nonnil Response
// alone is not success: checkpoint, natural Close, join, session and continuity
// may still fail. Completed denotes this one author operation, not a ceremony.
type ExecutionAuthorResult struct {
	Revision     string
	Response     *ExecutionCorpusAuthorResponse
	RootStarted  bool
	RootJoined   bool
	SessionEmpty bool
	Completed    bool
	Accounting   dispatchadmission.Snapshot
}

func authorCustodyConfig(index int, binding [32]byte) (dispatchadmission.Config, dispatchadmission.PhaseControlConfig) {
	attempts := uint64(3)
	if index == 0 {
		attempts = 4
	}
	// Each lifetime has one local accounting phase: two pairs per Git attempt,
	// one checkpoint, one close, and one unused receive/EOF reservation. PC01
	// has Pause + Checkpoint and its receiver's one unused EOF reservation.
	config := dispatchadmission.Config{
		Limits: dispatchadmission.Limits{Producers: 1, Sites: 1, Roles: 1, Phases: 1, ActivePerProducer: 1,
			Attempts: attempts, WireBytes: (2*attempts + 3) * 2 * dispatchadmission.FrameBytes, AckTimeout: 5 * time.Second},
		Producers: []dispatchadmission.Producer{{ID: 1, Binding: binding, Sites: dispatchadmission.AuthorSites()}},
		Phases:    []dispatchadmission.Phase{{ID: 1, Roles: []dispatchadmission.RoleBudget{{Role: dispatchadmission.RoleGit, Attempts: attempts}}}},
	}
	control := dispatchadmission.PhaseControlConfig{Phases: []uint32{1}, InitialPhase: 1, MaximumPhases: 1,
		MaximumWireBytes: 3 * 2 * dispatchadmission.FrameBytes, Timeout: 30 * time.Second}
	return config, control
}

// AuthorNext selects only A, then B, then A-return, through three actual CLI
// processes. Each call uses the unchanged selected plan-phase deadline and the
// caller's earlier deadline, including bootstrap, author decoding and census.
// Timeout stops the owned process; it cannot make contextless child decoding
// cooperative. Cleanup is separate failed work and never extends valid success.
// No retry or future revision is authored after a failed operation.
func (custody *ExecutionAuthorCustody) AuthorNext(ctx context.Context) (result ExecutionAuthorResult, retErr error) {
	if custody == nil || ctx == nil || ctx.Err() != nil {
		return result, ErrExecutionAuthorCustody
	}
	custody.mu.Lock()
	if custody.active || custody.closed || custody.err != nil || custody.next >= len(custody.expected) {
		custody.mu.Unlock()
		return result, ErrExecutionAuthorCustody
	}
	index := custody.next
	ctx, cancel := context.WithTimeout(ctx, custody.deadlines[index])
	defer cancel()
	if custody.check(ctx) != nil || custody.checkSource(ctx, custody.previous) != nil {
		custody.err = ErrExecutionAuthorCustody
		custody.mu.Unlock()
		return result, ErrExecutionAuthorCustody
	}
	custody.active = true
	request := ExecutionCorpusAuthorRequest{Schema: ExecutionCorpusAuthorRequestSchema,
		PlanPath: custody.planPath, PlanSHA256: custody.planSHA256, SourcePath: custody.roots[1].path,
		SourceIdentity: custody.identity, Revision: custody.expected[index].Name, Previous: custody.previous}
	custody.mu.Unlock()
	result.Revision = request.Revision
	defer func() {
		custody.mu.Lock()
		defer custody.mu.Unlock()
		custody.active = result.RootStarted && (!result.RootJoined || !result.SessionEmpty)
		if retErr != nil {
			custody.err = ErrExecutionAuthorCustody
			retErr = ErrExecutionAuthorCustody
		} else {
			// Copy the actual validated response, never a caller-provided previous.
			previous := *result.Response
			custody.previous = &previous
			custody.next++
			result.Completed = true
		}
		custody.results = append(custody.results, cloneAuthorCustodyResult(result))
	}()
	raw, err := corpusAuthorCanonical(request, MaxExecutionCorpusAuthorRequestBytes)
	if err != nil {
		return result, ErrExecutionAuthorCustody
	}
	return custody.runAuthor(ctx, cancel, index, raw, result)
}

func (custody *ExecutionAuthorCustody) runAuthor(ctx context.Context, cancel context.CancelFunc, index int, raw []byte, result ExecutionAuthorResult) (_ ExecutionAuthorResult, retErr error) {
	var binding [32]byte
	if _, err := rand.Read(binding[:]); err != nil || binding == ([32]byte{}) {
		return result, ErrExecutionAuthorCustody
	}
	config, controlConfig := authorCustodyConfig(index, binding)
	lifetime, release := context.WithCancel(context.Background())
	defer release()
	controller, err := dispatchadmission.New(lifetime, config)
	if err != nil {
		return result, ErrExecutionAuthorCustody
	}
	stopController := context.AfterFunc(controller.Context(), cancel)
	defer stopController()
	// Exactly four owned socket pairs: DA01, PC01, stdin, stdout. Pollable
	// parent endpoints replace their originals before Start; FileConn briefly
	// adds one descriptor per adoption. Child stdio copies close after Start.
	var owned [8]*os.File
	defer func() {
		for _, file := range owned {
			if file != nil {
				if err := file.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
					retErr = ErrExecutionAuthorCustody
				}
			}
		}
	}()
	for index := 0; index < len(owned); index += 2 {
		owned[index], owned[index+1], err = dispatchadmission.NewPipe()
		if err != nil {
			return result, ErrExecutionAuthorCustody
		}
	}
	input, err := adoptAuthorCustodySocket(owned[4])
	owned[4] = nil // Adoption closes the original even on refusal.
	if err != nil {
		return result, ErrExecutionAuthorCustody
	}
	defer func() {
		if input.Close() != nil {
			retErr = ErrExecutionAuthorCustody
		}
	}()
	output, err := adoptAuthorCustodySocket(owned[6])
	owned[6] = nil
	if err != nil {
		return result, ErrExecutionAuthorCustody
	}
	defer func() {
		if output.Close() != nil {
			retErr = ErrExecutionAuthorCustody
		}
	}()
	command := exec.Command(custody.authorPath)
	command.Dir, command.Env = custody.parent, slices.Clone(custody.environment)
	command.ExtraFiles = []*os.File{owned[1], owned[3]}
	command.Stdin, command.Stdout = owned[5], owned[7]
	command.Stderr = &checkoutCommandOutput{remaining: 8 << 10, cancel: cancel}
	command.WaitDelay = 5 * time.Second
	prepareProductionSession(command)
	if ctx.Err() != nil || command.Start() != nil {
		return result, ErrExecutionAuthorCustody
	}
	result.RootStarted = true
	waited := make(chan error, 1)
	go func() { waited <- command.Wait() }() // The sole native Wait.
	var control *dispatchadmission.PhaseControl
	var served <-chan error
	canceled := make(chan struct{})
	stopCancel := context.AfterFunc(ctx, func() {
		defer close(canceled)
		_ = input.SetDeadline(time.Now())
		_ = output.SetDeadline(time.Now())
		_ = signalProductionStop(command.Process)
	})
	defer func() {
		if !stopCancel() {
			<-canceled
		}
	}()
	// Every post-Start path reaches finishAuthor and preserves its prefix.
	for _, index := range []int{1, 3, 5, 7} {
		if owned[index].Close() != nil {
			retErr = ErrExecutionAuthorCustody
		}
		owned[index] = nil
	}
	bootstrap := dispatchadmission.ProductionBootstrap{Program: dispatchadmission.ProgramCorpusAuthor,
		InputSHA256: sha256.Sum256(raw), Producer: config.Producers[0], Phase: 1,
		Limits: config.Limits, Control: controlConfig, Tools: custody.tools}
	if retErr == nil && dispatchadmission.SendProductionBootstrap(ctx, owned[0], owned[2], bootstrap) != nil {
		retErr = ErrExecutionAuthorCustody
	}
	if retErr == nil {
		control, err = dispatchadmission.NewPhaseControl(lifetime, owned[2], binding, controlConfig)
		owned[2] = nil // This API adopts even on refusal.
		if err != nil {
			retErr = ErrExecutionAuthorCustody
		} else {
			completion := make(chan error, 1)
			serverFile := owned[0]
			owned[0] = nil
			go func() {
				completion <- controller.ServeChecked(lifetime, 1, command.Process.Pid, serverFile, func(ctx context.Context, site dispatchadmission.Site) error {
					custody.mu.Lock()
					defer custody.mu.Unlock()
					if site.ID != dispatchadmission.SiteCorpusAuthorGit || site.Role != dispatchadmission.RoleGit || site.Persistent || custody.check(ctx) != nil {
						custody.err = ErrExecutionAuthorCustody
						return custody.err
					}
					return nil
				})
			}()
			served = completion
		}
	}
	reader := bufio.NewReaderSize(output, MaxExecutionCorpusAuthorResponseBytes+1)
	if retErr == nil {
		if input.SetWriteDeadline(time.Now().Add(dispatchadmission.ProductionBootstrapTimeout)) != nil {
			retErr = ErrExecutionAuthorCustody
		} else if count, err := input.Write(raw); err != nil || count != len(raw) {
			retErr = ErrExecutionAuthorCustody
		}
		if input.CloseWrite() != nil {
			retErr = ErrExecutionAuthorCustody
		}
	}
	if retErr == nil {
		responseRaw, err := reader.ReadSlice('\n')
		response, responseErr := authorCustodyCanonicalResponse(responseRaw, custody.expected[index])
		if err != nil || responseErr != nil || ctx.Err() != nil ||
			custody.previous != nil && response.ConfigSHA256 != custody.previous.ConfigSHA256 {
			retErr = ErrExecutionAuthorCustody
		} else {
			result.Response = &response
			if control.Pause(ctx) != nil || controller.Fence() != nil || control.Checkpoint(ctx) != nil {
				retErr = ErrExecutionAuthorCustody
			}
		}
	}
	result, retErr = finishAuthorCustody(command, controller, control, waited, served, release, result, retErr)
	if retErr == nil {
		// Check the exact EOF after natural child close; a second response or
		// any trailing byte refuses. A leaked non-session writer cannot hang it.
		if output.SetReadDeadline(time.Now().Add(time.Second)) != nil {
			retErr = ErrExecutionAuthorCustody
		} else if _, err := reader.ReadByte(); !errors.Is(err, io.EOF) {
			retErr = ErrExecutionAuthorCustody
		}
		custody.mu.Lock()
		if custody.check(ctx) != nil || custody.checkSource(ctx, result.Response) != nil ||
			result.Accounting.Attempts != config.Limits.Attempts {
			retErr = ErrExecutionAuthorCustody
		}
		custody.mu.Unlock()
	}
	return result, retErr
}

// The passed endpoint is owned, unlike the child's borrowed fd0/fd1. Replace
// it with one pollable close-on-exec duplicate, closing the original on every
// path. There is no file/anonymous-pipe fallback without real deadlines.
func adoptAuthorCustodySocket(file *os.File) (*net.UnixConn, error) {
	if file == nil {
		return nil, ErrExecutionAuthorCustody
	}
	connection, err := net.FileConn(file)
	closeErr := file.Close()
	if err != nil {
		return nil, ErrExecutionAuthorCustody
	}
	socket, ok := connection.(*net.UnixConn)
	if !ok || closeErr != nil {
		_ = connection.Close()
		return nil, ErrExecutionAuthorCustody
	}
	return socket, nil
}

func finishAuthorCustody(command *exec.Cmd, controller *dispatchadmission.Controller, control *dispatchadmission.PhaseControl,
	waited, served <-chan error, release context.CancelFunc, result ExecutionAuthorResult, failure error,
) (ExecutionAuthorResult, error) {
	if failure != nil {
		_ = controller.Fence()
		_ = signalProductionStop(command.Process)
	}
	if served == nil {
		_ = controller.CancelUnused(1)
	}
	joinTimer := time.NewTimer(30 * time.Second)
	select {
	case err := <-waited:
		result.RootJoined = true
		if err != nil {
			failure = ErrExecutionAuthorCustody
		}
	case <-joinTimer.C:
		failure = ErrExecutionAuthorCustody
		_ = command.Process.Kill()
		killTimer := time.NewTimer(6 * time.Second)
		select {
		case <-waited:
			result.RootJoined = true
		case <-killTimer.C:
		}
		killTimer.Stop()
	}
	joinTimer.Stop()
	if served != nil {
		timer := time.NewTimer(5 * time.Second)
		select {
		case err := <-served:
			if err != nil {
				failure = ErrExecutionAuthorCustody
			}
		case <-timer.C:
			failure = ErrExecutionAuthorCustody
			release()
			// Transport cancellation cannot interrupt a synchronous native
			// metadata check. Join cooperatively without releasing source custody.
			<-served
		}
		timer.Stop()
	}
	if control != nil && control.Close() != nil {
		failure = ErrExecutionAuthorCustody
	}
	if result.RootJoined {
		members, err := t4013.PrivateProcessSessionMembers(command.Process.Pid)
		result.SessionEmpty = err == nil && members == 0
	}
	var snapshotErr error
	result.Accounting, snapshotErr = controller.Snapshot()
	if !result.RootJoined || !result.SessionEmpty || snapshotErr != nil || !result.Accounting.Complete {
		failure = ErrExecutionAuthorCustody
	}
	return result, failure
}

func cloneAuthorCustodyResult(result ExecutionAuthorResult) ExecutionAuthorResult {
	if result.Response != nil {
		response := *result.Response
		result.Response = &response
	}
	result.Accounting.Phases = slices.Clone(result.Accounting.Phases)
	for index := range result.Accounting.Phases {
		result.Accounting.Phases[index].Roles = slices.Clone(result.Accounting.Phases[index].Roles)
	}
	result.Accounting.Producers = slices.Clone(result.Accounting.Producers)
	return result
}

// Results returns a defensive source-free copy of completed/stopped operations.
// Failed prefixes are never replaced by an all-zero success or discarded retry.
func (custody *ExecutionAuthorCustody) Results() []ExecutionAuthorResult {
	if custody == nil {
		return nil
	}
	custody.mu.Lock()
	defer custody.mu.Unlock()
	result := make([]ExecutionAuthorResult, len(custody.results))
	for index, row := range custody.results {
		result[index] = cloneAuthorCustodyResult(row)
	}
	return result
}
