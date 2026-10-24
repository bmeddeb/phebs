//go:build linux

package t421

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
)

// This is a finite prework consumer, not a profile or operational capability.
// Failed/canceled attempts remain spent; no reference identity crosses a wire.
type linuxExecutorInnerPrework struct {
	attempted       atomic.Bool
	consumed        atomic.Bool
	inputHelper     *LinuxInputTreeHelper
	git             *LinuxExecutionGitCustody
	builds          *LinuxExecutionGoBuildCustody
	referenceHelper *LinuxGoReferenceHelper
	tool            *LinuxExecutionToolCustody
	cleanupErr      error
	observation     *executionPreworkObservation
}

func (inner *linuxExecutorInnerPrework) prepare(ctx context.Context, value linuxExecutorPreworkSelection, parent *linuxExecutorParent, image *os.File) error {
	if inner == nil || !inner.attempted.CompareAndSwap(false, true) {
		return ErrExecutionLauncher
	}
	if ctx == nil || ctx.Err() != nil || parent == nil || parent.check(ctx) != nil || !parent.inner || image == nil {
		return ErrExecutionLauncher
	}
	request := ReferenceToolRequest{RepositoryRoot: value.RepositoryRoot, GitBinary: value.GitBinary, GoRoot: value.GoRoot,
		ModuleCache: value.ModuleCache, PlanSourceCommit: value.PlanSourceCommit, IntegratedMainCommit: value.IntegratedMainCommit,
		SourceCommit: value.SourceCommit, Binary: value.InputHelperBinary}
	var err error
	inner.inputHelper, err = protectLinuxInputTreeHelperObserved(ctx, request, inner.observation)
	if err != nil {
		return ErrExecutionLauncher
	}
	inner.git, err = protectLinuxExecutionGitObserved(ctx, value.PreparationRoot, value.GitBinary, inner.inputHelper, inner.observation)
	if err != nil {
		return ErrExecutionLauncher
	}
	inner.builds, err = protectLinuxExecutionGoBuildInputsObserved(ctx, value.PreparationRoot, LinuxExecutionGoBuildRequest{Git: inner.git,
		RepositoryRoot: value.RepositoryRoot, PlanSourceCommit: value.PlanSourceCommit, IntegratedMainCommit: value.IntegratedMainCommit,
		SourceCommit: value.SourceCommit, GoRoot: value.GoRoot, ModuleCache: value.ModuleCache}, inner.inputHelper, inner.observation)
	if err != nil {
		return ErrExecutionLauncher
	}
	request.Binary = value.ReferenceHelperBinary
	inner.referenceHelper, err = protectLinuxGoReferenceHelperObserved(ctx, request, inner.observation)
	if err != nil {
		return ErrExecutionLauncher
	}
	inner.tool, err = inner.builds.protectExecutorReferenceFD(ctx, value.PreparationRoot, image, inner.referenceHelper)
	if err != nil {
		return ErrExecutionLauncher
	}
	if !inner.tool.executorClaim.bindInner(inner) {
		return ErrExecutionLauncher
	}
	return inner.consume(ctx, value, parent, image)
}

func (inner *linuxExecutorInnerPrework) consume(ctx context.Context, value linuxExecutorPreworkSelection, parent *linuxExecutorParent, image *os.File) error {
	if inner == nil || !inner.consumed.CompareAndSwap(false, true) {
		return ErrExecutionLauncher
	}
	if inner.tool == nil || !inner.tool.executorClaim.consumeInner(inner) {
		return ErrExecutionLauncher
	}
	if !inner.attempted.Load() || ctx == nil || ctx.Err() != nil || inner.builds == nil || inner.tool == nil ||
		inner.tool.referenceInputs != inner.builds || parent == nil || !parent.inner || parent.check(ctx) != nil {
		return ErrExecutionLauncher
	}
	identity, err := inner.tool.Check(ctx, "t422-execute")
	if err != nil || identity.BuildVCSRevision != value.SourceCommit || identity.Provenance != "go-build-info-vcs-v1" || inner.measuredInputs(ctx, value, identity) != nil {
		return ErrExecutionLauncher
	}
	return inner.tool.WithImage(ctx, "t422-execute", func(_ ExecutionToolIdentity, candidate *os.File) error {
		left, leftErr := image.Stat()
		right, rightErr := candidate.Stat()
		if leftErr != nil || rightErr != nil || !os.SameFile(left, right) ||
			t4013.MatchLinuxProcessExecutableImage(ctx, os.Getpid(), candidate) != nil ||
			t4013.MatchLinuxProcessExecutableImage(ctx, os.Getppid(), candidate) != nil || parent.check(ctx) != nil {
			return ErrExecutionLauncher
		}
		return nil
	})
}

func (inner *linuxExecutorInnerPrework) measuredInputs(ctx context.Context, value linuxExecutorPreworkSelection, identity ExecutionToolIdentity) error {
	builds := inner.builds
	builds.mu.Lock()
	defer builds.mu.Unlock()
	if builds.closing || builds.released || builds.err != nil || builds.active != nil || builds.tree == nil || !builds.last.joined || !builds.last.empty ||
		builds.last.pid <= 0 || builds.last.toolDigest != identity.SHA256 || builds.request.SourceCommit != value.SourceCommit ||
		builds.last.goDigest != value.ExpectedGoSHA256 || builds.goIdentity.SHA256 != value.ExpectedGoSHA256 {
		return ErrExecutionLauncher
	}
	input, err := builds.tree.Check(ctx)
	if err != nil || input != builds.last.input || linuxExecutorPreworkInputFacts(input) != value.ExpectedInput {
		return ErrExecutionLauncher
	}
	return nil
}

// Close returns only after the actual source-owned constructors/attempts have
// joined. A retained unknown prefix vetoes completion and keeps its borrowers.
func (inner *linuxExecutorInnerPrework) Close(ctx context.Context) error {
	if inner == nil || ctx == nil {
		return ErrExecutionLauncher
	}
	if inner.tool != nil {
		if inner.tool.Close() != nil {
			inner.cleanupErr = ErrExecutionLauncher
		}
		if !linuxExecutorToolReleased(inner.tool) {
			return ErrExecutionLauncher
		}
		inner.tool = nil
	}
	if inner.builds != nil {
		err := inner.builds.Close(ctx)
		if !linuxExecutorBuildReleased(inner.builds) {
			return ErrExecutionLauncher
		}
		inner.builds = nil
		if err != nil {
			inner.cleanupErr = ErrExecutionLauncher
		}
	}
	if inner.git != nil {
		err := inner.git.Close(ctx)
		if !linuxExecutorGitReleased(inner.git) {
			return ErrExecutionLauncher
		}
		inner.git = nil
		if err != nil {
			inner.cleanupErr = ErrExecutionLauncher
		}
	}
	if inner.referenceHelper != nil {
		if inner.referenceHelper.Close() != nil {
			inner.cleanupErr = ErrExecutionLauncher
		}
		if !linuxExecutorToolReleased(inner.referenceHelper.tool) {
			return ErrExecutionLauncher
		}
		inner.referenceHelper = nil
	}
	if inner.inputHelper != nil {
		if inner.inputHelper.Close() != nil {
			inner.cleanupErr = ErrExecutionLauncher
		}
		if !linuxExecutorToolReleased(inner.inputHelper.tool) {
			return ErrExecutionLauncher
		}
		inner.inputHelper = nil
	}
	return inner.cleanupErr
}

func linuxExecutorToolReleased(tool *LinuxExecutionToolCustody) bool {
	if tool == nil || tool.input == nil {
		return false
	}
	tool.mu.Lock()
	defer tool.mu.Unlock()
	tool.input.mu.Lock()
	defer tool.input.mu.Unlock()
	return tool.probe == nil && tool.input.closed && tool.input.closeErr == nil
}

func linuxExecutorTreeReleased(tree *LinuxExecutionInputTree) bool {
	if tree == nil {
		return true // No tree was issued before this constructor failed.
	}
	tree.mu.Lock()
	defer tree.mu.Unlock()
	return tree.closed && tree.closeErr == nil && (tree.pid == 0 || tree.joined && tree.empty)
}

func linuxExecutorBuildReleased(builds *LinuxExecutionGoBuildCustody) bool {
	builds.mu.Lock()
	defer builds.mu.Unlock()
	return builds.released && builds.closeErr == nil && linuxExecutorTreeReleased(builds.tree)
}

func linuxExecutorGitReleased(git *LinuxExecutionGitCustody) bool {
	git.mu.Lock()
	defer git.mu.Unlock()
	return git.released && git.closeErr == nil && linuxExecutorTreeReleased(git.tree)
}

func (inner *linuxExecutorInnerPrework) retired() bool {
	return inner.tool == nil && inner.builds == nil && inner.git == nil && inner.referenceHelper == nil && inner.inputHelper == nil
}

func runLinuxExecutorPreworkInner(ctx context.Context, value linuxExecutorPreworkSelection, parent *linuxExecutorParent, image *os.File) (bool, error) {
	inner := &linuxExecutorInnerPrework{observation: parent.observation}
	workErr := inner.prepare(ctx, value, parent, image)
	if recordErr := writeLinuxExecutorPreworkFailure(value, inner.observation); recordErr != nil {
		workErr = errors.Join(ErrExecutionLauncher, workErr, recordErr)
	}
	// Work remains bounded. Unknown original owners retain this process through
	// settlement without extending the work deadline or allowing later success.
	closeErr := inner.Close(context.Background())
	for !inner.retired() || !inner.observation.settle(context.Background()) {
		// Retire only the original recorded owners. Never restart preparation,
		// release unknown borrowers, or grant additional operational time.
		time.Sleep(time.Second)
		closeErr = errors.Join(ErrExecutionLauncher, inner.Close(context.Background()))
	}
	liveErr := parent.check(ctx)
	if workErr != nil || closeErr != nil || liveErr != nil {
		return true, errors.Join(ErrExecutionLauncher, workErr, closeErr, liveErr)
	}
	return true, nil
}
