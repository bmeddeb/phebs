package t421

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/storeaccounting"
	"github.com/bmeddeb/phebs/spike/t4013"
)

var ErrExecutionEpochOne = errors.New("execution epoch-one launch unavailable or incomplete")

// ExecutionEpochOne owns the genuine shared reducers for one author/start/stop
// slice. Later producer slots remain unused: snapshots are prefixes, never a
// fifteen-phase completion or a host/profile/ceremony admission.
type ExecutionEpochOne struct {
	mu                     sync.Mutex
	epochs                 *ExecutionEpochConfigCustody
	phebs, zoekt, surreal  *ExecutionToolCustody
	controller             *dispatchadmission.Controller
	parent                 *dispatchadmission.LocalProducer
	store                  *storeaccounting.Transport
	release                context.CancelFunc
	used, authored, closed bool
	closeErr               error
}

// PrepareExecutionEpochOne starts no child. It rechecks the author's admitted
// protected plan, derives both unchanged V3 accounting ceilings, and advances
// only the empty phase-one accounting boundary. It does not repeat DecodePlan's
// physical regeneration or infer that preflight has passed.
func PrepareExecutionEpochOne(ctx context.Context, epochs *ExecutionEpochConfigCustody, phebs, zoekt, surreal *ExecutionToolCustody) (_ *ExecutionEpochOne, retErr error) {
	if ctx == nil || ctx.Err() != nil || epochs == nil || epochs.author == nil || phebs == nil || zoekt == nil || surreal == nil {
		return nil, ErrExecutionEpochOne
	}
	author := epochs.author
	author.mu.Lock()
	defer author.mu.Unlock()
	epochs.mu.Lock()
	defer epochs.mu.Unlock()
	if author.active || epochs.active || author.next != 0 || epochs.checkLocked(ctx, 1) != nil ||
		phebs.referenceInputs != author.request.Builds || zoekt.referenceInputs != author.request.Builds {
		return nil, ErrExecutionEpochOne
	}
	_, raw, err := readAuthorCustodyPlan(ctx, author.request.Plan)
	var plan Plan
	if err != nil || SHA256(raw) != author.planSHA256 || json.Unmarshal(raw, &plan) != nil {
		return nil, ErrExecutionEpochOne
	}
	var bindings [executionProducerCount][32]byte
	for index := range bindings {
		if _, err := rand.Read(bindings[index][:]); err != nil {
			return nil, ErrExecutionEpochOne
		}
	}
	da, err := executionDispatchConfig(plan, bindings)
	sa, wire, storeErr := executionStoreConfig(plan, bindings)
	if err != nil || storeErr != nil {
		return nil, ErrExecutionEpochOne
	}
	lifetime, release := context.WithCancel(context.Background())
	flow := &ExecutionEpochOne{epochs: epochs, phebs: phebs, zoekt: zoekt, surreal: surreal, release: release}
	defer func() {
		if retErr != nil {
			_ = flow.Close()
		}
	}()
	flow.controller, err = dispatchadmission.New(lifetime, da)
	if err != nil {
		return nil, ErrExecutionEpochOne
	}
	flow.parent, err = flow.controller.NewLocalProducer(lifetime, executionRootProducer)
	if err != nil {
		return nil, ErrExecutionEpochOne
	}
	store, err := storeaccounting.New(lifetime, sa)
	if err != nil {
		return nil, ErrExecutionEpochOne
	}
	flow.store, err = storeaccounting.NewTransport(lifetime, store, wire)
	if err != nil {
		return nil, ErrExecutionEpochOne
	}
	if flow.parent.Pause(ctx) != nil || flow.controller.Fence() != nil || flow.parent.Checkpoint(ctx) != nil ||
		flow.store.Fence() != nil || flow.store.Advance() != nil || flow.controller.Advance() != nil || flow.parent.Resume(2) != nil {
		return nil, ErrExecutionEpochOne
	}
	return flow, nil
}

func (flow *ExecutionEpochOne) AuthorA(ctx context.Context) (ExecutionAuthorResult, error) {
	if flow == nil || flow.epochs == nil || flow.controller == nil || flow.parent == nil {
		return ExecutionAuthorResult{}, ErrExecutionEpochOne
	}
	flow.mu.Lock()
	defer flow.mu.Unlock()
	if flow.closed || flow.used || flow.authored {
		return ExecutionAuthorResult{}, ErrExecutionEpochOne
	}
	result, err := flow.epochs.author.AuthorNextOn(ctx, flow.controller, flow.parent, 7)
	flow.authored = err == nil && result.Completed
	return result, err
}

// Close cancels unused future lifetimes, rather than manufacturing their
// terminal EOFs. A live/unjoined server retains the borrowed input owners.
func (flow *ExecutionEpochOne) Close() error {
	if flow == nil {
		return nil
	}
	flow.mu.Lock()
	defer flow.mu.Unlock()
	if flow.closed {
		return flow.closeErr
	}
	// Constructor failures already hold the input locks; only a used flow can
	// own a launched child and require this check.
	if flow.used {
		flow.epochs.mu.Lock()
		active := flow.epochs.active
		flow.epochs.mu.Unlock()
		if active {
			return ErrExecutionEpochOne
		}
	}
	flow.closed = true
	if flow.parent != nil {
		if flow.parent.Close(context.Background()) != nil {
			flow.closeErr = ErrExecutionEpochOne
		}
	}
	if flow.store != nil {
		// Future unopened lifetimes deliberately make Close incomplete. Only
		// that exact expected refusal is harmless after every opened receiver
		// has supplied terminal EOF and the pre-close prefix is unlatched.
		prefix, err := flow.store.Snapshot()
		closeErr := flow.store.Close()
		if err != nil || prefix.Opened != prefix.TerminalEOF || closeErr != nil && closeErr != storeaccounting.ErrIncomplete {
			flow.closeErr = ErrExecutionEpochOne
		}
	}
	if flow.release != nil {
		flow.release()
	} else {
		flow.closeErr = ErrExecutionEpochOne
	}
	return flow.closeErr
}

type ExecutionEpochOneResult struct {
	RootStarted, RootJoined, SessionEmpty bool
	Accounting                            dispatchadmission.Snapshot
	Store                                 storeaccounting.WireSnapshot
}

type ExecutionEpochOneRun struct {
	mu           sync.Mutex
	flow         *ExecutionEpochOne
	epoch        ExecutionEpochConfig
	control      *dispatchadmission.PhaseControl
	command      *exec.Cmd
	output       *checkoutCommandOutput // Read only after native Wait joins stdout/stderr copies.
	stop, done   chan struct{}
	stopOnce     sync.Once
	healthUsed   bool
	healthCancel context.CancelFunc
	healthDone   chan struct{}
	stopping     bool
	result       ExecutionEpochOneResult
	err          error
}

func (flow *ExecutionEpochOne) checkTools(ctx context.Context) (string, []dispatchadmission.ProductionToolBinding, []string, error) {
	author := flow.epochs.author
	epoch := flow.epochs.epochs[0]
	phebs, path, err := flow.phebs.Check(ctx, "phebs")
	zoekt, zoektPath, zoektErr := flow.zoekt.Check(ctx, "zoekt-git-index")
	surreal, surrealPath, surrealErr := flow.surreal.Check(ctx, "surreal")
	gitEnv, gitErr := author.request.Git.Environment(ctx, epoch.Home, epoch.Temporary)
	if err != nil || zoektErr != nil || surrealErr != nil || gitErr != nil || phebs.BuildVCSRevision != author.request.Builds.reference.source {
		return "", nil, nil, ErrExecutionEpochOne
	}
	environment := externalToolEnvironment(epoch.Temporary)
	for index, value := range environment {
		if strings.HasPrefix(value, "HOME=") {
			environment[index] = "HOME=" + epoch.Home
		}
		if strings.HasPrefix(value, "PATH=") {
			environment[index] = "PATH=" + author.request.Git.Directory()
		}
	}
	tools := []dispatchadmission.ProductionToolBinding{
		{Role: "git", Path: author.gitPath, Environment: gitEnv},
		{Role: "surreal", Path: surrealPath, Environment: environment},
		{Role: "zoekt-git-index", Path: zoektPath, Environment: gitEnv},
	}
	environment = append(environment, dispatchadmission.ProductionEnvironment+"="+dispatchadmission.ProductionStoreSelector,
		"PHEBS_SURREAL="+surrealPath, "PHEBS_SURREAL_SHA256="+surreal.SHA256,
		"PHEBS_ZOEKT_GIT_INDEX="+zoektPath, "PHEBS_ZOEKT_GIT_INDEX_SHA256="+zoekt.SHA256,
		"PHEBS_T421_EXACT_READS=source-free-v1", "PHEBS_T4013_EXACT_REPORTS=source-free-v1")
	return path, tools, environment, nil
}

// Start launches exactly producer two in phase two. The twenty-minute and
// one-MiB output refusal ceilings are inherited from the small native serve
// rehearsal, not frozen full-cold budgets. PC01 reserves DrainOwners, Pause,
// and one terminal EOF pair; no phase transition is implemented by this slice.
func (flow *ExecutionEpochOne) Start(ctx context.Context) (_ *ExecutionEpochOneRun, retErr error) {
	if flow == nil || flow.epochs == nil || flow.controller == nil || flow.parent == nil || flow.store == nil || ctx == nil || ctx.Err() != nil {
		return nil, ErrExecutionEpochOne
	}
	flow.mu.Lock()
	defer flow.mu.Unlock()
	if flow.closed || flow.used || !flow.authored {
		return nil, ErrExecutionEpochOne
	}
	flow.used = true
	author, epochs := flow.epochs.author, flow.epochs
	author.mu.Lock()
	epochs.mu.Lock()
	if author.active || epochs.active || author.next != 1 || epochs.checkLocked(ctx, 1) != nil || author.checkSource(ctx, author.previous) != nil {
		epochs.mu.Unlock()
		author.mu.Unlock()
		return nil, ErrExecutionEpochOne
	}
	path, tools, environment, err := flow.checkTools(ctx)
	epoch := epochs.epochs[0]
	if err == nil && (epochs.released != 0 || epochs.listeners[0].Close() != nil) {
		err = ErrExecutionEpochOne
	}
	if err == nil {
		epochs.listeners[0] = nil
		epochs.released = 1
		epochs.active = true
		author.active = true
	}
	epochs.mu.Unlock()
	author.mu.Unlock()
	if err != nil {
		return nil, ErrExecutionEpochOne
	}
	started := false
	defer func() {
		if !started {
			author.mu.Lock()
			epochs.mu.Lock()
			author.active, epochs.active = false, false
			epochs.mu.Unlock()
			author.mu.Unlock()
		}
	}()
	view, err := flow.controller.ProducerLaunch(2)
	if err != nil || view.Phase != 2 {
		return nil, ErrExecutionEpochOne
	}
	raw, err := json.Marshal(struct {
		Schema       string `json:"schema"`
		Recipe       string `json:"recipe"`
		PlanSHA256   string `json:"plan_sha256"`
		ConfigSHA256 string `json:"config_sha256"`
		ServerEpoch  uint64 `json:"server_epoch"`
		Repository   string `json:"repository"`
	}{"t422-semantic-launch-v3", "t422-fixed-phase-control-v3", author.planSHA256, epoch.ConfigSHA256, 1, epoch.Repository})
	if err != nil {
		return nil, ErrExecutionEpochOne
	}
	raw = append(raw, '\n')
	var files [6]*os.File
	defer func() {
		for _, file := range files {
			if file != nil {
				_ = file.Close()
			}
		}
	}()
	for index := 0; index < len(files); index += 2 {
		files[index], files[index+1], err = dispatchadmission.NewPipe()
		if err != nil {
			return nil, ErrExecutionEpochOne
		}
	}
	input, err := adoptAuthorCustodySocket(files[4])
	files[4] = nil
	if err != nil {
		return nil, ErrExecutionEpochOne
	}
	defer func() { _ = input.Close() }() // Explicit success-path close is checked below.
	storeFile, storeConfig, err := flow.store.Open(2)
	if err != nil {
		return nil, ErrExecutionEpochOne
	}
	defer func() { _ = storeFile.Close() }() // Explicit post-Start close is checked below.
	runCtx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	run := &ExecutionEpochOneRun{flow: flow, epoch: epoch, stop: make(chan struct{}), done: make(chan struct{})}
	output := &checkoutCommandOutput{remaining: 1 << 20, cancel: cancel}
	run.output = output
	command := exec.Command(path, "serve", "--config", epoch.ConfigPath)
	command.Dir, command.Env = author.parent, environment
	command.Stdin, command.Stdout, command.Stderr = files[5], output, output
	command.ExtraFiles = []*os.File{files[1], files[3], storeFile}
	command.WaitDelay = 5 * time.Second
	prepareProductionSession(command)
	run.command = command
	handle, err := flow.parent.StartInPhase(runCtx, 2, dispatchadmission.Site{ID: executionSiteServe, Role: executionRolePhebs, Persistent: true}, command)
	if err != nil {
		cancel()
		return nil, ErrExecutionEpochOne
	}
	started, run.result.RootStarted = true, true
	waited := make(chan error, 1)
	go func() { waited <- handle.Wait() }()
	for _, index := range []int{1, 3, 5} {
		if files[index].Close() != nil {
			retErr = ErrExecutionEpochOne
		}
		files[index] = nil
	}
	if storeFile.Close() != nil {
		retErr = ErrExecutionEpochOne
	}
	controlConfig := dispatchadmission.PhaseControlConfig{OwnerControl: true, Phases: []uint32{2, 3, 4}, InitialPhase: 2, MaximumPhases: 3, MaximumWireBytes: 3 * 2 * dispatchadmission.FrameBytes, Timeout: 30 * time.Second}
	bootstrap := dispatchadmission.ProductionBootstrap{Program: dispatchadmission.ProgramPhebs, SemanticMode: dispatchadmission.ProductionSemanticV3,
		InputSHA256: sha256.Sum256(raw), Producer: view.Producer, Phase: 2, Limits: view.Limits, Control: controlConfig, Tools: tools, Store: &storeConfig}
	var served <-chan error
	if retErr == nil && dispatchadmission.SendProductionBootstrap(runCtx, files[0], files[2], bootstrap) != nil {
		retErr = ErrExecutionEpochOne
	}
	if retErr == nil {
		run.control, err = dispatchadmission.NewPhaseControl(flow.controller.Context(), files[2], view.Producer.Binding, controlConfig)
		files[2] = nil
		if err != nil {
			retErr = ErrExecutionEpochOne
		} else {
			completion := make(chan error, 1)
			file := files[0]
			files[0] = nil
			go func() {
				completion <- flow.controller.ServeChecked(flow.controller.Context(), 2, command.Process.Pid, file, func(checkCtx context.Context, _ dispatchadmission.Site) error {
					author.mu.Lock()
					defer author.mu.Unlock()
					epochs.mu.Lock()
					defer epochs.mu.Unlock()
					if epochs.checkLocked(checkCtx, 1) != nil {
						return ErrExecutionEpochOne
					}
					_, _, _, err := flow.checkTools(checkCtx)
					return err
				})
			}()
			served = completion
		}
	}
	if retErr == nil && writeAuthorCustodyRequest(runCtx, input, raw) != nil {
		retErr = ErrExecutionEpochOne
	}
	// Bind parent stdin release to the same terminal result before handing
	// cleanup to finish. The deferred Close remains only a refusal-path guard.
	if input.Close() != nil {
		retErr = ErrExecutionEpochOne
	}
	go run.finish(runCtx, cancel, waited, served, retErr)
	return run, retErr
}

// Health waits only for TCP readiness (no repeated HTTP requests), then makes
// one authenticated, parent-token-bound health request. It proves no index or
// pipeline convergence. Any failure ends this one-shot run; no automatic retry.
func (run *ExecutionEpochOneRun) Health(ctx context.Context) (retErr error) {
	if run == nil || ctx == nil || run.stop == nil || run.done == nil {
		return ErrExecutionEpochOne
	}
	run.mu.Lock()
	if run.healthUsed || run.control == nil || run.stopping {
		run.mu.Unlock()
		return ErrExecutionEpochOne
	}
	select {
	case <-run.stop:
		run.mu.Unlock()
		return ErrExecutionEpochOne
	default:
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	run.healthCancel, run.healthDone = cancel, make(chan struct{})
	run.healthUsed = true
	run.mu.Unlock()
	defer func() {
		defer close(run.healthDone)
		cancel()
		if retErr != nil {
			run.mu.Lock()
			run.err = ErrExecutionEpochOne
			run.mu.Unlock()
			run.stopOnce.Do(func() { close(run.stop) })
		}
	}()
	for {
		conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp4", run.epoch.Listen)
		if err == nil {
			_ = conn.Close()
			break
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			run.stopOnce.Do(func() { close(run.stop) })
			return ErrExecutionEpochOne
		case <-run.done:
			timer.Stop()
			return ErrExecutionEpochOne
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+run.epoch.Listen+"/api/health", nil)
	if err != nil {
		return ErrExecutionEpochOne
	}
	request.Header.Set("Authorization", "Bearer "+run.epoch.APIKey)
	request.Header.Set(dispatchadmission.ProductionRequestHeader, run.control.RequestToken())
	transport := &http.Transport{DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		run.stopOnce.Do(func() { close(run.stop) })
		return ErrExecutionEpochOne
	}
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, 4097))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil || ctx.Err() != nil || len(raw) > 4096 || response.StatusCode != http.StatusOK {
		run.stopOnce.Do(func() { close(run.stop) })
		return ErrExecutionEpochOne
	}
	return nil
}

func (run *ExecutionEpochOneRun) finish(ctx context.Context, cancel context.CancelFunc, waited, served <-chan error, failure error) {
	defer close(run.done)
	defer cancel()
	joined := false
	var waitErr error
	if failure == nil {
		select {
		case <-run.stop:
		case <-ctx.Done():
			failure = ErrExecutionEpochOne
		case <-run.flow.controller.Context().Done():
			failure = ErrExecutionEpochOne
		case waitErr = <-waited:
			joined = true
			failure = ErrExecutionEpochOne
		}
	}
	// Stop and the lifetime deadline may both be ready. Cleanup still owns the
	// process, but whichever select arm won cannot turn expiry into success.
	if ctx.Err() != nil {
		failure = ErrExecutionEpochOne
	}
	run.mu.Lock()
	run.stopping = true
	healthCancel, healthDone := run.healthCancel, run.healthDone
	run.mu.Unlock()
	if healthCancel != nil {
		healthCancel()
		<-healthDone
	}
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer stopCancel()
	if !joined && failure == nil && (run.control.DrainOwners(stopCtx) != nil || run.control.Pause(stopCtx) != nil) {
		failure = ErrExecutionEpochOne
	}
	if run.flow.parent.Pause(stopCtx) != nil || run.flow.controller.Fence() != nil {
		failure = ErrExecutionEpochOne
	}
	if !joined {
		if signalProductionStop(run.command.Process) != nil {
			failure = ErrExecutionEpochOne
		}
		select {
		case waitErr = <-waited:
			joined = true
		case <-stopCtx.Done():
			failure = ErrExecutionEpochOne
			_ = run.command.Process.Kill()
			timer := time.NewTimer(6 * time.Second)
			select {
			case waitErr = <-waited:
				joined = true
			case <-timer.C:
			}
			timer.Stop()
		}
	}
	if !joined || waitErr != nil {
		failure = ErrExecutionEpochOne
	}
	if served != nil {
		joinCtx, joinCancel := context.WithTimeout(context.Background(), 5*time.Second)
		select {
		case err := <-served:
			if err != nil {
				failure = ErrExecutionEpochOne
			}
		case <-joinCtx.Done():
			failure = ErrExecutionEpochOne
			run.flow.release()
			<-served
		}
		joinCancel()
	}
	joinCtx, joinCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if run.flow.store.Wait(joinCtx, 2) != nil {
		failure = ErrExecutionEpochOne
	}
	joinCancel()
	if run.control != nil && run.control.Close() != nil {
		failure = ErrExecutionEpochOne
	}
	if run.flow.parent.Close(context.Background()) != nil {
		failure = ErrExecutionEpochOne
	}
	result := ExecutionEpochOneResult{RootStarted: true, RootJoined: joined}
	if joined {
		members, err := t4013.PrivateProcessSessionMembers(run.command.Process.Pid)
		result.SessionEmpty = err == nil && members == 0
	}
	if !result.SessionEmpty {
		failure = ErrExecutionEpochOne
	}
	var err error
	result.Accounting, err = run.flow.controller.Snapshot()
	if err != nil {
		failure = ErrExecutionEpochOne
	}
	result.Store, err = run.flow.store.Snapshot()
	if err != nil {
		failure = ErrExecutionEpochOne
	}
	author, epochs := run.flow.epochs.author, run.flow.epochs
	author.mu.Lock()
	epochs.mu.Lock()
	author.active, epochs.active = !joined || !result.SessionEmpty, !joined || !result.SessionEmpty
	epochs.mu.Unlock()
	author.mu.Unlock()
	run.mu.Lock()
	if run.err != nil || !epochOneClosedPrefix(ctx, result) {
		failure = ErrExecutionEpochOne
	}
	run.result, run.err = result, failure
	run.mu.Unlock()
}

func (run *ExecutionEpochOneRun) Stop(ctx context.Context) (ExecutionEpochOneResult, error) {
	if run == nil || run.stop == nil || run.done == nil || run.flow == nil || ctx == nil {
		return ExecutionEpochOneResult{}, ErrExecutionEpochOne
	}
	run.stopOnce.Do(func() { close(run.stop) })
	return run.Wait(ctx)
}

func (run *ExecutionEpochOneRun) Wait(ctx context.Context) (ExecutionEpochOneResult, error) {
	if run == nil || ctx == nil || run.done == nil || run.flow == nil {
		return ExecutionEpochOneResult{}, ErrExecutionEpochOne
	}
	select {
	case <-run.done:
		run.mu.Lock()
		defer run.mu.Unlock()
		result := run.result
		result.Accounting.Phases = slices.Clone(result.Accounting.Phases)
		for index := range result.Accounting.Phases {
			result.Accounting.Phases[index].Roles = slices.Clone(result.Accounting.Phases[index].Roles)
		}
		result.Accounting.Producers = slices.Clone(result.Accounting.Producers)
		result.Store.Store.Phases = slices.Clone(result.Store.Store.Phases)
		result.Store.Store.Producers = slices.Clone(result.Store.Store.Producers)
		return result, run.err
	case <-ctx.Done():
		return ExecutionEpochOneResult{RootStarted: true}, ErrExecutionEpochOne
	}
}

func epochOneClosedPrefix(ctx context.Context, result ExecutionEpochOneResult) bool {
	if ctx == nil || ctx.Err() != nil || !result.RootStarted || !result.RootJoined || !result.SessionEmpty || result.Store.Opened != 1 || result.Store.TerminalEOF != 1 {
		return false
	}
	root, server, store := false, false, false
	for _, producer := range result.Accounting.Producers {
		if producer.Producer == executionRootProducer {
			root = producer.Attached && producer.Closed && producer.Active == 0 && producer.Ordinal == 2
		}
		if producer.Producer == 2 {
			server = producer.Attached && producer.Closed && producer.Active == 0
		}
	}
	for _, producer := range result.Store.Store.Producers {
		if producer.Producer == 2 {
			store = producer.Attached && producer.Closed && producer.Calls == 0 && producer.Transactions == 0
		}
	}
	return root && server && store
}
