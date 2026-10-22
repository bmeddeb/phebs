//go:build linux

package t421

import (
	"context"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

type linuxEpochCoordinator struct {
	held *linuxEpochHeldFlow
	core *ExecutionEpochOne
}

func (adapter *linuxEpochCoordinator) issued() bool {
	return adapter != nil && adapter.held != nil && adapter.core != nil && adapter.held.flow != nil && adapter.held.flow.core == adapter.core && adapter.core.native == adapter && adapter.core.epochs != nil && adapter.configIssued() && adapter.core.epochs.author != nil && adapter.core.epochs.author.native == adapter
}
func (adapter *linuxEpochCoordinator) configIssued() bool {
	value, ok := adapter.core.epochs.native.(*linuxEpochConfigAdapter)
	return ok && value.coordinator == adapter
}
func (adapter *linuxEpochCoordinator) check(ctx context.Context) error {
	if !adapter.issued() || adapter.held.flow.lifetime == nil || adapter.held.flow.lifetime.Err() != nil || adapter.held.check(ctx) != nil {
		return ErrExecutionEpochOne
	}
	return nil
}
func (adapter *linuxEpochCoordinator) checkSource(ctx context.Context, response *ExecutionCorpusAuthorResponse) error {
	if adapter.check(ctx) != nil {
		return ErrExecutionAuthorCustody
	}
	owner := adapter.held.author
	owner.mu.Lock()
	defer owner.mu.Unlock()
	return owner.checkSource(ctx, response)
}
func (adapter *linuxEpochCoordinator) checkEpoch(ctx context.Context, epoch uint64) error {
	if adapter.check(ctx) != nil || epoch < 1 || epoch > 3 {
		return ErrExecutionEpochConfigs
	}
	owner := adapter.held.epochs
	owner.author.mu.Lock()
	defer owner.author.mu.Unlock()
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if owner.checkLocked(ctx, epoch) != nil || owner.epochs[epoch-1] != adapter.core.epochs.epochs[epoch-1] {
		return ErrExecutionEpochConfigs
	}
	return nil
}

// Different method names keep author and epoch checks closed and unambiguous.
type linuxEpochConfigAdapter struct{ coordinator *linuxEpochCoordinator }

func (adapter *linuxEpochConfigAdapter) check(ctx context.Context, epoch uint64) error {
	if adapter == nil || adapter.coordinator == nil {
		return ErrExecutionEpochConfigs
	}
	return adapter.coordinator.checkEpoch(ctx, epoch)
}
func (adapter *linuxEpochConfigAdapter) release(ctx context.Context, epoch uint64) error {
	if adapter == nil || adapter.coordinator == nil {
		return ErrExecutionEpochConfigs
	}
	return adapter.coordinator.release(ctx, epoch)
}
func (adapter *linuxEpochCoordinator) release(ctx context.Context, epoch uint64) error {
	if adapter.checkEpoch(ctx, epoch) != nil {
		return ErrExecutionEpochConfigs
	}
	owner := adapter.held.epochs
	owner.author.mu.Lock()
	defer owner.author.mu.Unlock()
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if epoch != owner.released+1 || epoch > 3 || owner.listeners[epoch-1] == nil || owner.listeners[epoch-1].Close() != nil {
		owner.err = ErrExecutionEpochConfigs
		return owner.err
	}
	owner.listeners[epoch-1] = nil
	owner.released++
	return nil
}
func (adapter *linuxEpochCoordinator) next(ctx context.Context, controller *dispatchadmission.Controller, parent *dispatchadmission.LocalProducer, producer uint32, borrower *ExecutionEpochOneRun) (result ExecutionAuthorResult, retErr error) {
	if adapter.check(ctx) != nil || controller != adapter.core.controller || parent != adapter.core.parent || adapter.held.resources == nil {
		return result, ErrExecutionAuthorCustody
	}
	view := adapter.core.epochs.author
	view.mu.Lock()
	index := view.next
	allowed := borrower == nil && index == 0 || borrower != nil && view.borrowedBy == borrower && (index == 1 && borrower.epoch.Epoch == 1 || index == 2 && borrower.epoch.Epoch == 2 && borrower.joinedEmpty())
	if view.active || view.closed || view.err != nil || !allowed || index < 0 || index >= 3 || producer != uint32(7+index) {
		view.mu.Unlock()
		return result, ErrExecutionAuthorCustody
	}
	view.active = true
	view.mu.Unlock()
	owner := adapter.held.author
	operation, cancel := context.WithTimeout(ctx, owner.projection.deadlines[index])
	abortDone := make(chan struct{})
	stopAbort := context.AfterFunc(adapter.held.flow.lifetime, func() { defer close(abortDone); cancel() })
	defer func() {
		if !stopAbort() {
			<-abortDone
		}
		cancel()
	}()
	attempt := &linuxAuthorAttempt{done: make(chan struct{}), cancel: cancel, result: ExecutionAuthorResult{Revision: owner.projection.expected[index].Name, ProducerID: producer}}
	launch := linuxAuthorLaunch{controller: controller, parent: parent, producer: producer, trace: adapter.held.resources.trace}
	owner.mu.Lock()
	if owner.borrowedBy != adapter.held.flow || owner.active != nil || owner.next != index || owner.bindLaunchLocked(index, &launch) != nil || owner.checkSource(operation, owner.previous) != nil {
		owner.mu.Unlock()
		view.mu.Lock()
		view.active = false
		view.err = ErrExecutionAuthorCustody
		view.mu.Unlock()
		return result, ErrExecutionAuthorCustody
	}
	attempt.launch = launch
	owner.active = attempt
	request := linuxCorpusAuthorRequest{Schema: linuxCorpusAuthorRequestSchema, PlanSHA256: owner.planSHA256, AuthorSHA256: owner.authorIdentity.SHA256, SourcePath: owner.roots[1].path, SourceIdentity: owner.roots[1].identity, Controller: adapter.held.controllerPID, GitFD: int(adapter.held.gitRoot.Fd()), Revision: owner.projection.expected[index].Name}
	if owner.previous != nil {
		value := cloneCorpusAuthorResponse(*owner.previous)
		request.Previous = &value
	}
	owner.mu.Unlock()
	check := func(checkCtx context.Context) error {
		if adapter.check(checkCtx) != nil || adapter.held.resources.Check(checkCtx) != nil {
			return ErrExecutionAuthorCustody
		}
		return nil
	}
	attempt.err = adapter.held.rewindAuthorPlan(operation)
	if attempt.err == nil {
		attempt.err = owner.runTransport(operation, attempt, index, adapter.held.images[0], adapter.held.plan, request, launch, check)
	}
	view.mu.Lock()
	owner.mu.Lock()
	if attempt.err != nil || operation.Err() != nil || owner.err != nil || attempt.result.Response == nil {
		owner.err = ErrExecutionAuthorCustody
		view.err = ErrExecutionAuthorCustody
		attempt.err = ErrExecutionAuthorCustody
	} else {
		response := cloneCorpusAuthorResponse(*attempt.result.Response)
		owner.previous = &response
		owner.next++
		attempt.result.Completed = true
		copy := cloneCorpusAuthorResponse(response)
		view.previous = &copy
		view.next = owner.next
	}
	owner.results = append(owner.results, cloneAuthorCustodyResult(attempt.result))
	owner.last = attempt
	owner.active = nil
	view.results = append(view.results, cloneAuthorCustodyResult(attempt.result))
	view.sessions[index] = attempt.pid
	view.active = false
	close(attempt.done)
	owner.mu.Unlock()
	view.mu.Unlock()
	return cloneAuthorCustodyResult(attempt.result), attempt.err
}
func (adapter *linuxEpochCoordinator) tools(ctx context.Context, epoch uint64) (string, []dispatchadmission.ProductionToolBinding, []string, error) {
	if adapter.checkEpoch(ctx, epoch) != nil || adapter.held.resources == nil || adapter.held.resources.Check(ctx) != nil {
		return "", nil, nil, ErrExecutionEpochOne
	}
	held := adapter.held
	selected := held.epochs.epochs[epoch-1]
	source := held.builds.reference.source
	if held.imageIdentities[1].BuildVCSRevision != source || held.imageIdentities[2].Provenance != zoektOfferProvenance || held.imageIdentities[2].BuildRecipeSHA256 != zoektOfferRecipe(adapter.core.plan.ToolPolicy, source) {
		return "", nil, nil, ErrExecutionEpochOne
	}
	core := linuxEpochFDRoute(held.controllerPID, held.gitRoot) + "/core"
	gitEnv := linuxEpochForegroundGitEnvironment(core, selected.Home, selected.Temporary)
	environment := externalToolEnvironment(selected.Temporary)
	for i, value := range environment {
		if strings.HasPrefix(value, "HOME=") {
			environment[i] = "HOME=" + selected.Home
		}
		if strings.HasPrefix(value, "PATH=") {
			environment[i] = "PATH=" + core
		}
	}
	phebs, zoekt, surreal := linuxEpochFDRoute(held.controllerPID, held.images[1]), linuxEpochFDRoute(held.controllerPID, held.images[2]), linuxEpochFDRoute(held.controllerPID, held.images[3])
	tools := []dispatchadmission.ProductionToolBinding{{Role: "git", Path: core + "/git", Environment: gitEnv}, {Role: "surreal", Path: surreal, Environment: environment}, {Role: "zoekt-git-index", Path: zoekt, Environment: append(slices.Clone(gitEnv), dispatchadmission.IndexOfferEnvironment+"=v1", "ZOEKT_DISABLE_CATFILE_BATCH=true", "GOGC=50")}}
	// The successful genuine resource Check above issues this fixed narrowing.
	tools[0].RuntimeRecipe = dispatchadmission.ProductionLinuxForegroundGitV2
	tools[2].RuntimeRecipe = dispatchadmission.ProductionLinuxForegroundGitV2
	binding := executionRuntimeEnvironmentBindings{Home: selected.Home, Temporary: selected.Temporary, GitDirectory: core, SurrealPath: surreal, SurrealSHA256: held.imageIdentities[3].SHA256, ZoektPath: zoekt, ZoektSHA256: held.imageIdentities[2].SHA256}
	return phebs, tools, executionPhebsEnvironment(environment, binding), nil
}

// Only this resource-checked Linux server recipe narrows background maintenance.
// Keep the historical Git owner and ordinary application policy byte-exact.
func linuxEpochForegroundGitEnvironment(core, home, temporary string) []string {
	environment := executionGitEnvironment(core, home, temporary)
	for i, entry := range environment {
		if entry == "GIT_CONFIG_COUNT=3" {
			environment[i] = "GIT_CONFIG_COUNT=5"
		}
	}
	return append(environment, "GIT_CONFIG_KEY_3=gc.autoDetach", "GIT_CONFIG_VALUE_3=false",
		"GIT_CONFIG_KEY_4=maintenance.autoDetach", "GIT_CONFIG_VALUE_4=false")
}
func (adapter *linuxEpochCoordinator) workspace() (*os.File, string, [2]int32) {
	root := adapter.held.workspace
	return root.file, root.path, root.identity.FSID
}

// The same whole-scope description is deliberately reused serially. Each
// actual author consumes FD6; only a joined predecessor permits this rewind.
func (held *linuxEpochHeldFlow) rewindAuthorPlan(ctx context.Context) error {
	if ctx == nil || ctx.Err() != nil || held.plan == nil || !linuxInputProtected(held.plan, held.planEntry) {
		return ErrExecutionAuthorCustody
	}
	offset, err := held.plan.Seek(0, io.SeekStart)
	if err != nil || offset != 0 {
		return ErrExecutionAuthorCustody
	}
	return nil
}
