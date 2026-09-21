//go:build darwin

package t421

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"
)

// executionInnerPreparation owns the complete pre-AuthorA object graph. Its
// live handoff and signer objects are intentionally private and nonserializable.
type executionInnerPreparation struct {
	mu sync.Mutex

	operational            productionRoot
	volume                 *executionPressureVolume
	ballast                *executionPressureBallast
	signer                 *ExecutionSystemToolCustody
	git                    *ExecutionGitCustody
	builds                 *ExecutionGoBuildCustody
	candidates             *executionReferenceCandidates
	tools                  [5]*ExecutionToolCustody
	surreal                *ExecutionToolCustody
	planInput              *ExecutionInputCustody
	author                 *ExecutionAuthorCustody
	epochs                 *ExecutionEpochConfigCustody
	flow                   *ExecutionEpochOne
	profile                ExecutionProfile
	admission              ExecutionProfileAdmissionBinding
	handoff                *executionOperationalHandoffCapability
	projection             executionAuthorizationHandoffProjection
	claim                  *executionSignerCeremonyClaimCustody
	key                    *executionSignerKeyCustody
	candidate              executionFreezeCandidatePreparation
	seal                   *executionSignerSealCustody
	authorization          *executionAuthorizationWait
	sessionBinding         executionAuthorizationSessionBinding
	authorizationHandoff   executionAuthorizationHandoff
	authorizationPeer      executionAuthorizationPeer
	ordinals               *executionEventOrdinals
	selection              executionSelectionV1
	parent                 *executionParentLiveness
	outerDeadline          time.Time
	finalAdmissionDeadline time.Time
	handoffUsed            bool
	preclaimStage          executionPreclaimStage

	closed         bool
	abortAttempted bool
}

// prepareExecutionInnerPreparation performs only the bounded non-operational
// preparation admitted before AuthorA. The returned owner is nonnil after any
// created custody so failure evidence is not silently removed.
func prepareExecutionInnerPreparation(
	ctx context.Context,
	selection executionSelectionV1,
	parent *executionParentLiveness,
	outerDeadline time.Time,
) (*executionInnerPreparation, error) {
	prepared := &executionInnerPreparation{
		selection: selection, parent: parent, outerDeadline: outerDeadline,
		ordinals: newExecutionEventOrdinals(), preclaimStage: executionPreclaimStagePreflight,
	}
	refuse := func() (*executionInnerPreparation, error) { return prepared, ErrExecutionLauncher }
	if ctx == nil || ctx.Err() != nil || !validExecutionSelection(selection) || parent == nil || parent.alive == nil ||
		parent.alive.Err() != nil || !executionSessionIsolated(os.Getppid()) || !time.Now().Before(outerDeadline) {
		return refuse()
	}

	var err error
	prepared.preclaimStage = executionPreclaimStageOperationalRoot
	prepared.operational, err = createExecutionOperationalRoot(selection)
	if err != nil {
		return refuse()
	}
	prepared.preclaimStage = executionPreclaimStagePressureVolume
	prepared.volume, err = prepareExecutionPressureVolume(ctx, prepared.operational.path)
	if err != nil {
		return refuse()
	}
	prepared.preclaimStage = executionPreclaimStageWorkspace
	ctx, workspace, err := prepared.volume.borrowWorkspace(ctx)
	if err != nil {
		return refuse()
	}
	prepared.preclaimStage = executionPreclaimStageSignerCustody
	prepared.signer, err = HoldExecutionSystemTool(ctx, "ssh-keygen")
	if err != nil {
		return refuse()
	}
	prepared.preclaimStage = executionPreclaimStageGitCustody
	prepared.git, err = ProtectExecutionGit(ctx, workspace, selection.GitBinary)
	if err != nil {
		return refuse()
	}
	prepared.preclaimStage = executionPreclaimStageGoBuildInputs
	prepared.builds, err = ProtectExecutionGoBuildInputs(ctx, workspace, ExecutionGoBuildRequest{
		Git: prepared.git, RepositoryRoot: selection.RepositoryRoot,
		PlanSourceCommit: selection.PlanSourceCommit, IntegratedMainCommit: selection.IntegratedMainCommit,
		SourceCommit: selection.SourceCommit, GoRoot: selection.GoRoot, ModuleCache: selection.ModuleCache,
	})
	if err != nil {
		return refuse()
	}
	prepared.preclaimStage = executionPreclaimStageReferenceCandidates
	prepared.candidates, err = prepareExecutionReferenceCandidatesV3(ctx, prepared.builds, workspace)
	if err != nil {
		return refuse()
	}
	roles := executionReferenceCandidateRoles()
	prepared.preclaimStage = executionPreclaimStageReferenceTools
	for index, role := range roles {
		path, pathErr := prepared.candidates.Path(ctx, role)
		if pathErr != nil {
			return refuse()
		}
		prepared.tools[index], err = prepared.builds.protectReferenceTool(ctx, workspace, role, path, PlanV4Schema)
		if err != nil {
			return refuse()
		}
	}
	if err := prepared.candidates.Close(); err != nil {
		return refuse()
	}
	prepared.candidates = nil
	prepared.preclaimStage = executionPreclaimStageSurrealCustody
	prepared.surreal, err = ProtectExecutionExternalTool(ctx, workspace, "surreal", selection.SurrealBinary)
	if err != nil {
		return refuse()
	}

	prepared.preclaimStage = executionPreclaimStagePlanConstruction
	plan, err := BuildPlanV4(selection.PlanSourceCommit)
	if err != nil {
		return refuse()
	}
	raw, err := MarshalCanonical(plan)
	if err != nil {
		return refuse()
	}
	planPath := filepath.Join(workspace, "unsealed-plan-input.json")
	if err := writeExecutionInnerPlan(prepared.volume.workspace, planPath, raw); err != nil {
		return refuse()
	}
	prepared.preclaimStage = executionPreclaimStagePlanInputCustody
	prepared.planInput, err = ProtectExecutionInputs(ctx, workspace, []ExecutionInputCopy{{Name: "plan", Path: planPath, SHA256: SHA256(raw)}})
	if err != nil {
		return refuse()
	}
	prepared.preclaimStage = executionPreclaimStageAuthorCustody
	prepared.author, err = PrepareExecutionAuthor(ctx, workspace, ExecutionAuthorRequest{
		Git: prepared.git, Builds: prepared.builds, Author: prepared.tools[0], Plan: prepared.planInput,
	})
	if err != nil {
		return refuse()
	}
	prepared.preclaimStage = executionPreclaimStageEpochConfigs
	prepared.epochs, err = PrepareExecutionEpochConfigs(ctx, prepared.author)
	if err != nil {
		return refuse()
	}
	prepared.preclaimStage = executionPreclaimStageEpochOne
	prepared.flow, err = PrepareExecutionEpochOne(ctx, prepared.epochs, prepared.tools[1], prepared.tools[2], prepared.surreal)
	if err != nil {
		return refuse()
	}
	prepared.preclaimStage = executionPreclaimStageProfileTools
	if prepared.flow.bindProfileTools(ctx, prepared.tools[3], prepared.tools[4]) != nil {
		return refuse()
	}
	prepared.preclaimStage = executionPreclaimStageProfileSigner
	if prepared.flow.prepareProfileSigner(ctx, prepared.signer) != nil {
		return refuse()
	}
	prepared.preclaimStage = executionPreclaimStageProfileNamespace
	if prepared.flow.bindProfileSignerNamespace(ctx, selection) != nil {
		return refuse()
	}
	prepared.preclaimStage = executionPreclaimStageProfileExecutor
	if prepared.flow.bindProfileExecutor(ctx, parent) != nil {
		return refuse()
	}
	prepared.preclaimStage = executionPreclaimStagePressureBallast
	prepared.ballast, err = prepareExecutionPressureBallast(ctx, prepared.volume)
	if err != nil {
		return refuse()
	}
	prepared.preclaimStage = executionPreclaimStagePressureSample
	if _, err := prepared.volume.samplePreparation(ctx); err != nil {
		return refuse()
	}
	prepared.preclaimStage = executionPreclaimStageRehearsalBinding
	if prepared.volume.bindRehearsal(ctx, prepared.flow) != nil {
		return refuse()
	}
	prepared.preclaimStage = executionPreclaimStageProfileHost
	if prepared.volume.observeProfileHost(ctx, prepared.flow) != nil {
		return refuse()
	}
	prepared.preclaimStage = executionPreclaimStageProfileEnvironment
	if prepared.flow.prepareProfileEnvironment(ctx) != nil {
		return refuse()
	}
	prepared.preclaimStage = executionPreclaimStageProfileRuntime
	if prepared.flow.prepareProfileRuntime(ctx) != nil {
		return refuse()
	}
	prepared.preclaimStage = executionPreclaimStageProfileIssue
	prepared.profile, prepared.admission, prepared.handoff, err = prepared.flow.issueExecutionProfile(ctx)
	if err != nil {
		return refuse()
	}
	prepared.preclaimStage = executionPreclaimStageParentImage
	executePath, executeDigest, err := parent.image.observe(ctx)
	if err != nil {
		return refuse()
	}
	prepared.preclaimStage = executionPreclaimStageHandoffProjection
	prepared.projection, err = projectExecutionAuthorizationHandoff(executePath,
		filepath.Join(prepared.operational.path, executionAuthorizationSocketName), executeDigest,
		outerDeadline.UnixNano())
	if err != nil {
		return refuse()
	}
	prepared.preclaimStage = executionPreclaimStageSignerNamespace
	namespace, err := prepared.flow.profileSignerNamespace.check(ctx)
	if err != nil {
		return refuse()
	}
	prepared.preclaimStage = executionPreclaimStageCeremonyClaim
	prepared.claim, err = claimExecutionSignerCeremony(ctx, namespace, selection.CeremonyID, prepared.operational.path)
	if err != nil {
		return refuse()
	}
	prepared.key, err = prepareExecutionSignerKey(ctx, prepared.claim, prepared.signer)
	if err != nil {
		return refuse()
	}
	prepared.candidate, err = prepared.handoff.prepareFreezeCandidate(ctx, prepared.key.fingerprint)
	if err != nil {
		return refuse()
	}
	return prepared, nil
}

func (prepared *executionInnerPreparation) preclaimFailure(cleanupErr error) (executionPreclaimFailureV1, bool) {
	if prepared == nil {
		return executionPreclaimFailureV1{}, false
	}
	prepared.mu.Lock()
	defer prepared.mu.Unlock()
	if prepared.claim != nil || !validExecutionPreclaimStage(prepared.preclaimStage) {
		return executionPreclaimFailureV1{}, false
	}
	cleanup := "retained_or_unavailable"
	if cleanupErr == nil && prepared.closed {
		cleanup = "clean"
	}
	return executionPreclaimFailureV1{
		Schema: executionPreclaimFailureSchema, Stage: prepared.preclaimStage, Cleanup: cleanup,
	}, true
}

// authorizeAndAuthorA performs the sole live signed handoff. It emits one
// bounded operator command, spends the first connection regardless of its
// validity, and transfers only an exact post-authorization binding to AuthorA.
func (prepared *executionInnerPreparation) authorizeAndAuthorA(ctx context.Context, output *executionAuthorizationOutput) (result ExecutionAuthorResult, retErr error) {
	if prepared == nil {
		return ExecutionAuthorResult{}, ErrExecutionLauncher
	}
	prepared.mu.Lock()
	defer prepared.mu.Unlock()
	if prepared.closed || prepared.handoffUsed || ctx == nil || ctx.Err() != nil || output == nil ||
		prepared.flow == nil || prepared.handoff == nil || prepared.key == nil || prepared.parent == nil || prepared.ordinals == nil ||
		prepared.candidate.raw == nil || !time.Now().Before(prepared.outerDeadline) {
		return ExecutionAuthorResult{}, ErrExecutionLauncher
	}
	prepared.handoffUsed = true
	outerCtx, cancelOuter := context.WithDeadline(ctx, prepared.outerDeadline)
	defer cancelOuter()

	var err error
	prepared.seal, err = sealExecutionFreezeCandidate(outerCtx, prepared.key, prepared.flow.plan, prepared.candidate)
	if err != nil || prepared.seal == nil || prepared.seal.firstVerifiedAt.UnixNano() <= 0 ||
		prepared.seal.firstVerifiedAt.After(time.Now()) {
		return ExecutionAuthorResult{}, ErrExecutionLauncher
	}
	prepared.finalAdmissionDeadline, err = executionFinalAdmissionDeadline(prepared.seal.firstVerifiedAt, prepared.outerDeadline,
		prepared.flow.plan.SafetyEnvelope.RevalidationDeadlineMS)
	if err != nil || !time.Now().Before(prepared.finalAdmissionDeadline) {
		return ExecutionAuthorResult{}, ErrExecutionLauncher
	}
	finalCtx, cancelFinal := context.WithDeadline(outerCtx, prepared.finalAdmissionDeadline)
	defer cancelFinal()

	prepared.authorization, err = prepareExecutionAuthorization(finalCtx, prepared.operational, prepared.finalAdmissionDeadline)
	if err != nil {
		return ExecutionAuthorResult{}, ErrExecutionLauncher
	}
	defer func() { retErr = errors.Join(retErr, prepared.authorization.close()) }()
	freezeSHA256, err := receiptSHA256(prepared.seal.freeze)
	freezeSHA256 = strings.TrimPrefix(freezeSHA256, "sha256:")
	if err != nil || !validExecutionHexSHA256(freezeSHA256) {
		return ExecutionAuthorResult{}, ErrExecutionLauncher
	}
	prepared.sessionBinding, err = observeExecutionAuthorizationSessionBinding(finalCtx, prepared.selection.CeremonyID,
		freezeSHA256, prepared.parent, prepared.authorization, prepared.outerDeadline, prepared.finalAdmissionDeadline)
	if err != nil {
		return ExecutionAuthorResult{}, ErrExecutionLauncher
	}
	executePath, executeDigest, err := prepared.parent.image.observe(finalCtx)
	if err != nil || executeDigest != prepared.sessionBinding.preimage.T422ExecuteImageSHA256 ||
		executionAuthorizationSHA256([]byte(executePath)) != prepared.sessionBinding.preimage.T422ExecuteCanonicalPathSHA256 {
		return ExecutionAuthorResult{}, ErrExecutionLauncher
	}
	prepared.authorizationHandoff, err = buildExecutionAuthorizationHandoff(executePath, prepared.authorization.path, executeDigest,
		prepared.outerDeadline.UnixNano(), prepared.finalAdmissionDeadline.UnixNano(), freezeSHA256,
		prepared.sessionBinding.sha256, prepared.projection)
	if err != nil || forwardExecutionAuthorizationHandoff(finalCtx, output, prepared.authorizationHandoff.frame) != nil {
		return ExecutionAuthorResult{}, ErrExecutionLauncher
	}
	expected := executionAuthorizationV1{
		Schema: executionAuthorizationSchema, FreezeSHA256: freezeSHA256, SessionBindingSHA256: prepared.sessionBinding.sha256,
	}
	prepared.authorizationPeer, err = prepared.authorization.consume(finalCtx, expected)
	if err != nil || finalCtx.Err() != nil || !time.Now().Before(prepared.finalAdmissionDeadline) {
		return ExecutionAuthorResult{}, ErrExecutionLauncher
	}
	checkedNamespace, err := prepared.candidate.namespace.recheck(finalCtx)
	if err != nil || checkedNamespace.digest != prepared.key.namespace.digest {
		return ExecutionAuthorResult{}, ErrExecutionLauncher
	}
	gitIdentity, gitPath, err := prepared.git.Check(finalCtx)
	if err != nil {
		return ExecutionAuthorResult{}, ErrExecutionLauncher
	}
	checkedCommits, err := InspectExecutionCheckout(finalCtx, prepared.selection.RepositoryRoot, gitPath,
		prepared.selection.PlanSourceCommit, prepared.selection.IntegratedMainCommit, prepared.selection.SourceCommit)
	afterGitIdentity, afterGitPath, afterGitErr := prepared.git.Check(finalCtx)
	if err != nil || afterGitErr != nil || checkedCommits != prepared.candidate.commits ||
		!reflect.DeepEqual(afterGitIdentity, gitIdentity) || afterGitPath != gitPath {
		return ExecutionAuthorResult{}, ErrExecutionLauncher
	}
	checkedCandidate, err := prepared.handoff.prepareFreezeCandidate(finalCtx, prepared.key.fingerprint)
	if err != nil || !reflect.DeepEqual(checkedCandidate, prepared.candidate) {
		return ExecutionAuthorResult{}, ErrExecutionLauncher
	}
	prepared.candidate = checkedCandidate
	freezeAdmission, err := prepared.seal.verifyAndIssueAdmission(finalCtx, prepared.flow.plan)
	if err != nil || finalCtx.Err() != nil || !time.Now().Before(prepared.finalAdmissionDeadline) {
		return ExecutionAuthorResult{}, ErrExecutionLauncher
	}
	profile, profileAdmission, err := prepared.handoff.consumeProfile(finalCtx)
	if err != nil || finalCtx.Err() != nil || !time.Now().Before(prepared.finalAdmissionDeadline) {
		return ExecutionAuthorResult{}, ErrExecutionLauncher
	}
	binding, err := BindExecutionFreezeForReceipt(prepared.seal.freeze, prepared.flow.plan, prepared.candidate.commits,
		prepared.key.fingerprint, prepared.candidate.checkout, profileAdmission, freezeAdmission)
	if err != nil || !reflect.DeepEqual(profile, prepared.candidate.profile) || finalCtx.Err() != nil ||
		!time.Now().Before(prepared.finalAdmissionDeadline) {
		return ExecutionAuthorResult{}, ErrExecutionLauncher
	}
	// The admitted sampler and epoch sequence outlive this method. Bind them to
	// the caller-owned inner lifetime, not outerCtx whose local defer cancels.
	return prepared.flow.authorAAdmitted(ctx, prepared.finalAdmissionDeadline, binding, prepared.ordinals, executePath)
}

func createExecutionOperationalRoot(selection executionSelectionV1) (productionRoot, error) {
	path, err := os.MkdirTemp("/private/tmp", "phebs-t422-")
	if err != nil {
		return productionRoot{}, ErrExecutionLauncher
	}
	remove := true
	defer func() {
		if remove {
			_ = os.Remove(path)
		}
	}()
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil || canonical != path || len(filepath.Join(path, executionAuthorizationSocketName)) > maxExecutionAuthSocketPathBytes {
		return productionRoot{}, ErrExecutionLauncher
	}
	selected := []string{selection.RepositoryRoot, selection.GoRoot, selection.ModuleCache, selection.GitBinary, selection.SurrealBinary, selection.SignerControlRoot}
	for _, other := range selected {
		if executionPathContains(path, other) || executionPathContains(other, path) {
			return productionRoot{}, ErrExecutionLauncher
		}
	}
	root, err := openProductionRoot(path)
	if err != nil || preflightExecutionAuthSocket(path) != nil {
		_ = closeExecutionOperationalRoot(root)
		return productionRoot{}, ErrExecutionLauncher
	}
	remove = false
	return root, nil
}

func writeExecutionInnerPlan(root productionRoot, path string, raw []byte) (retErr error) {
	if len(raw) == 0 || filepath.Dir(path) != root.path || pressureRootsUnchanged(root) != nil {
		return ErrExecutionLauncher
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return ErrExecutionLauncher
	}
	defer func() { retErr = errors.Join(retErr, file.Close()) }()
	if written, err := file.Write(raw); err != nil || written != len(raw) || file.Sync() != nil || root.file.Sync() != nil {
		return ErrExecutionLauncher
	}
	return nil
}

// Close releases only a preparation whose operational volume has already
// completed its existing ceremony teardown. Refusal keeps the full graph live
// for exact retained-custody diagnosis.
func (prepared *executionInnerPreparation) Close() error {
	if prepared == nil {
		return nil
	}
	prepared.mu.Lock()
	defer prepared.mu.Unlock()
	if prepared.closed {
		return nil
	}
	if prepared.volume != nil {
		prepared.volume.mu.Lock()
		released := prepared.volume.removed && !prepared.volume.borrowed
		prepared.volume.mu.Unlock()
		if !released {
			return errPressureVolume
		}
	}
	if err := prepared.closeInputOwnersLocked(); err != nil {
		return err
	}
	if prepared.volume != nil {
		if err := prepared.volume.removeOwnedOperationLock(prepared.operational); err != nil {
			return err
		}
	}
	if err := prepared.volume.Close(); err != nil {
		return err
	}
	if err := closeExecutionOperationalRoot(prepared.operational); err != nil {
		return err
	}
	prepared.closed = true
	return nil
}

func closeExecutionOperationalRoot(root productionRoot) error {
	if root.file == nil && root.path == "" {
		return nil
	}
	if root.file == nil || root.path == "" || pressureRootsUnchanged(root) != nil || !pressureDirectoryEmpty(root.path) {
		return ErrExecutionLauncher
	}
	if root.file.Close() != nil || os.Remove(root.path) != nil {
		return ErrExecutionLauncher
	}
	return nil
}

// closeInputOwnersLocked releases actual holders; any refused join or close
// remains an error and forbids discarding the mounted workspace.
func (prepared *executionInnerPreparation) closeInputOwnersLocked() error {
	var result error
	result = errors.Join(result, prepared.authorization.close(), prepared.seal.Close(), prepared.key.Close(), prepared.claim.Close(), prepared.flow.Close(), prepared.epochs.Close(), prepared.author.Close())
	if prepared.planInput != nil {
		result = errors.Join(result, prepared.planInput.Close())
	}
	if prepared.surreal != nil {
		result = errors.Join(result, prepared.surreal.Close())
	}
	for index := len(prepared.tools) - 1; index >= 0; index-- {
		if prepared.tools[index] != nil {
			result = errors.Join(result, prepared.tools[index].Close())
		}
	}
	result = errors.Join(result, prepared.candidates.Close(), prepared.builds.Close(), prepared.git.Close(), prepared.signer.Close())
	return result
}
