//go:build darwin

package t421

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
)

// The capability owns the exact object graph and held descriptors admitted by
// bindRehearsal. It has no exported fields, serialization form or caller-facing constructor.
// A failed or canceled attempt spends it just as a successful attempt does.
type executionWorkspaceCustodyCapability struct {
	state *executionWorkspaceCustodyCapabilityState
}

type executionWorkspaceCustodyCapabilityState struct {
	mu    sync.Mutex
	proof *executionWorkspaceCustodyProof
}

// This wrapper can only retain the live parent-liveness/image owner created by
// the protected launcher. It has no path/digest constructor.
type executionProfileLauncherCustody struct {
	parent *executionParentLiveness
}

type executionProfileExecutorCustody struct {
	launcher *executionProfileLauncherCustody
	identity ExecutionToolIdentity
}

type executionWorkspaceCustodyProof struct {
	volume  *executionPressureVolume
	flow    *ExecutionEpochOne
	profile *executionWorkspaceCustodyCapability
}

// This complete observed-profile half alone authorizes no operation; T42.2m
// must consume it together with exact signed-freeze and checkout proofs.
type executionOperationalHandoffCapability struct {
	state *executionOperationalHandoffCapabilityState
}

type executionOperationalHandoffCapabilityState struct {
	mu        sync.Mutex
	proof     *executionWorkspaceCustodyProof
	preimages executionObservedProfilePreimages
	profile   ExecutionProfile
	admission ExecutionProfileAdmissionBinding
}

// bindProfileExecutor retains the independently reference-admitted executor
// only when it is the exact image held by the live protected launcher.
func (flow *ExecutionEpochOne) bindProfileExecutor(ctx context.Context, launcher *executionParentLiveness) error {
	if flow == nil || flow.epochs == nil || flow.epochs.author == nil || launcher == nil {
		return ErrExecutionEpochOne
	}
	flow.mu.Lock()
	defer flow.mu.Unlock()
	author, epochs := flow.epochs.author, flow.epochs
	author.mu.Lock()
	defer author.mu.Unlock()
	epochs.mu.Lock()
	defer epochs.mu.Unlock()
	live := &executionProfileLauncherCustody{parent: launcher}
	if !processAccountingPlanSemantics(flow.plan.Schema) || flow.closed || flow.used || flow.authored || !flow.authorStarted.IsZero() ||
		flow.workspace != nil || flow.profileExecutor != nil || author.request.Builds == nil ||
		author.closed || author.err != nil || author.active ||
		author.borrowedBy != nil || author.next != 0 || epochs.closed || epochs.err != nil || epochs.active || epochs.released != 0 {
		return ErrExecutionEpochOne
	}
	// Binding is a one-shot authority transition. Publish an invalid sentinel
	// before observing or rebuilding so cancellation, launcher loss, and every
	// verifier failure remain sticky and cannot be retried with changed state.
	custody := &executionProfileExecutorCustody{}
	flow.profileExecutor = custody
	if ctx == nil || ctx.Err() != nil || launcher.alive == nil || launcher.alive.Err() != nil {
		return ErrExecutionEpochOne
	}
	verifyCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(launcher.alive, cancel)
	defer func() {
		stop()
		cancel()
	}()
	if launcher.alive.Err() != nil || verifyCtx.Err() != nil {
		return ErrExecutionEpochOne
	}
	path, digest, err := live.observe(verifyCtx)
	if err != nil {
		return ErrExecutionEpochOne
	}
	identity, err := verifyExecutionProfileExecutor(verifyCtx, author.request.Builds, path, digest)
	custody.launcher, custody.identity = live, identity
	_, checkErr := custody.check(ctx)
	if err != nil || checkErr != nil {
		return ErrExecutionEpochOne
	}
	return nil
}

// verifyExecutionProfileExecutor is the shared reference-verification boundary.
// The caller supplies only the launcher-observed image path and digest; the
// returned identity and Go comparison are derived while custody is locked.
func verifyExecutionProfileExecutor(ctx context.Context, builds *ExecutionGoBuildCustody, path, digest string) (ExecutionToolIdentity, error) {
	if builds == nil || ctx == nil || ctx.Err() != nil || !validExecutionSHA256(digest) {
		return ExecutionToolIdentity{}, ErrExecutionEpochOne
	}
	builds.mu.Lock()
	defer builds.mu.Unlock()
	if builds.check(ctx) != nil {
		return ExecutionToolIdentity{}, ErrExecutionEpochOne
	}
	identity, goIdentity, err := builds.verifyReferenceTool(ctx, filepath.Dir(builds.directory), "t422-execute", path, activeExecutionPlanSchema)
	if err != nil || goIdentity != builds.goIdentity || identity.SHA256 != digest {
		return ExecutionToolIdentity{}, ErrExecutionEpochOne
	}
	return identity, nil
}

func (custody *executionProfileLauncherCustody) observe(ctx context.Context) (string, string, error) {
	if custody == nil || custody.parent == nil || custody.parent.file == nil || custody.parent.image == nil ||
		custody.parent.alive == nil || custody.parent.alive.Err() != nil || custody.parent.cancel == nil || custody.parent.done == nil ||
		custody.parent.inner.PID != os.Getpid() || custody.parent.outer.PID != os.Getppid() {
		return "", "", ErrExecutionEpochOne
	}
	path, digest, err := custody.parent.image.observe(ctx)
	if err != nil || custody.parent.alive.Err() != nil {
		return "", "", ErrExecutionEpochOne
	}
	return path, digest, nil
}

func (custody *executionProfileExecutorCustody) check(ctx context.Context) (ExecutionToolIdentity, error) {
	if custody == nil || custody.identity.Role != "t422-execute" {
		return ExecutionToolIdentity{}, ErrExecutionEpochOne
	}
	_, digest, err := custody.launcher.observe(ctx)
	if err != nil || custody.identity.SHA256 != digest {
		return ExecutionToolIdentity{}, ErrExecutionEpochOne
	}
	return custody.identity, nil
}

func newExecutionWorkspaceCustodyCapability(schema string, volume *executionPressureVolume, flow *ExecutionEpochOne) *executionWorkspaceCustodyCapability {
	if !processAccountingPlanSemantics(schema) || volume == nil || flow == nil {
		return nil
	}
	capability := &executionWorkspaceCustodyCapability{state: &executionWorkspaceCustodyCapabilityState{}}
	capability.state.proof = &executionWorkspaceCustodyProof{volume: volume, flow: flow, profile: capability}
	return capability
}

func (capability *executionWorkspaceCustodyCapability) ConsumePreimages(ctx context.Context) (executionObservedProfilePreimages, error) {
	if capability == nil || capability.state == nil {
		return executionObservedProfilePreimages{}, errPressureVolume
	}
	capability.state.mu.Lock()
	proof := capability.state.proof
	capability.state.proof = nil
	capability.state.mu.Unlock()
	if proof == nil {
		return executionObservedProfilePreimages{}, errPressureVolume
	}
	observed, err := proof.issueProfile(ctx)
	if err != nil {
		return executionObservedProfilePreimages{}, err
	}
	return observed, nil
}

// issueExecutionProfile is the sole complete issuer. It accepts no identity,
// digest, profile or verified boolean from its caller and spends workspace
// custody even when a later observed-field check refuses.
func (flow *ExecutionEpochOne) issueExecutionProfile(ctx context.Context) (ExecutionProfile, ExecutionProfileAdmissionBinding, *executionOperationalHandoffCapability, error) {
	if flow == nil {
		return ExecutionProfile{}, ExecutionProfileAdmissionBinding{}, nil, ErrExecutionEpochOne
	}
	flow.mu.Lock()
	capability := flow.profileWorkspace
	flow.mu.Unlock()
	if capability == nil || capability.state == nil {
		return ExecutionProfile{}, ExecutionProfileAdmissionBinding{}, nil, ErrExecutionEpochOne
	}
	capability.state.mu.Lock()
	proof := capability.state.proof
	capability.state.proof = nil
	capability.state.mu.Unlock()
	if proof == nil {
		return ExecutionProfile{}, ExecutionProfileAdmissionBinding{}, nil, ErrExecutionEpochOne
	}
	if ctx == nil || ctx.Err() != nil {
		return ExecutionProfile{}, ExecutionProfileAdmissionBinding{}, nil, ErrExecutionEpochOne
	}
	profile, admission, preimages, err := proof.issueExecutionProfile(ctx)
	if err != nil {
		return ExecutionProfile{}, ExecutionProfileAdmissionBinding{}, nil, err
	}
	handoff := &executionOperationalHandoffCapability{state: &executionOperationalHandoffCapabilityState{
		proof: proof, preimages: preimages, profile: cloneExecutionProfile(profile), admission: cloneExecutionProfileAdmission(admission),
	}}
	return cloneExecutionProfile(profile), cloneExecutionProfileAdmission(admission), handoff, nil
}

// consumeProfile is the complete workspace/tool/profile half of the later
// signed operational handoff. It revalidates from held owners and spends once.
func (capability *executionOperationalHandoffCapability) consumeProfile(ctx context.Context) (ExecutionProfile, ExecutionProfileAdmissionBinding, error) {
	if capability == nil || capability.state == nil {
		return ExecutionProfile{}, ExecutionProfileAdmissionBinding{}, errPressureVolume
	}
	capability.state.mu.Lock()
	proof, profile, admission := capability.state.proof, capability.state.profile, capability.state.admission
	capability.state.proof = nil
	capability.state.preimages = executionObservedProfilePreimages{}
	capability.state.profile = ExecutionProfile{}
	capability.state.admission = ExecutionProfileAdmissionBinding{}
	capability.state.mu.Unlock()
	if proof == nil || profile.Schema == "" || admission.schema == "" || proof.revalidateExecutionProfile(ctx, profile, admission) != nil {
		return ExecutionProfile{}, ExecutionProfileAdmissionBinding{}, errPressureVolume
	}
	return cloneExecutionProfile(profile), cloneExecutionProfileAdmission(admission), nil
}

// prepareFreezeCandidate re-observes the held profile authorities and issues
// the checkout/build binding needed by the signer. It does not consume the
// operational handoff; only the later verified-signature transition may do so.
func (capability *executionOperationalHandoffCapability) prepareFreezeCandidate(ctx context.Context, signerFingerprint string) (executionFreezeCandidatePreparation, error) {
	var prepared executionFreezeCandidatePreparation
	if capability == nil || capability.state == nil {
		return prepared, errPressureVolume
	}
	capability.state.mu.Lock()
	defer capability.state.mu.Unlock()
	proof, profile, admission := capability.state.proof, capability.state.profile, capability.state.admission
	if proof == nil || profile.Schema == "" || admission.schema == "" {
		return prepared, errPressureVolume
	}
	err := proof.withProfileLocks(ctx, func(v *executionPressureVolume, flow *ExecutionEpochOne) error {
		observed, err := profilePreimagesLocked(ctx, proof, v, flow)
		if err != nil {
			return err
		}
		current, currentAdmission, err := issueExecutionProfileLocked(ctx, v, flow, observed)
		if err != nil || !reflect.DeepEqual(current, profile) || !reflect.DeepEqual(currentAdmission, admission) {
			return errPressureVolume
		}
		tools, _, err := observedExecutionProfileToolsLocked(ctx, v, flow)
		if err != nil {
			return errPressureVolume
		}
		commits, checkout, err := flow.epochs.author.request.Builds.bindCheckout(ctx, flow.plan.ToolPolicy, tools)
		if err != nil {
			return errPressureVolume
		}
		namespace, err := flow.profileSignerNamespace.check(ctx)
		if err != nil {
			return errPressureVolume
		}
		raw, err := assembleExecutionFreezeCandidate(flow.plan, commits, tools, flow.profileHost.Host, signerFingerprint,
			namespace, current, currentAdmission)
		if err != nil {
			return errPressureVolume
		}
		prepared = executionFreezeCandidatePreparation{
			raw: slices.Clone(raw), commits: commits, checkout: checkout,
			profile: cloneExecutionProfile(current), profileAdmission: cloneExecutionProfileAdmission(currentAdmission), namespace: namespace,
		}
		return nil
	})
	if err != nil {
		return executionFreezeCandidatePreparation{}, err
	}
	return prepared, nil
}
func (proof *executionWorkspaceCustodyProof) issueProfile(ctx context.Context) (executionObservedProfilePreimages, error) {
	var zero executionObservedProfilePreimages
	if proof == nil || proof.volume == nil || proof.flow == nil || ctx == nil || ctx.Err() != nil {
		return zero, errPressureVolume
	}
	v, flow := proof.volume, proof.flow
	v.mu.Lock()
	defer v.mu.Unlock()
	flow.mu.Lock()
	defer flow.mu.Unlock()
	if flow.epochs == nil || flow.epochs.author == nil {
		return zero, errPressureVolume
	}
	author, epochs := flow.epochs.author, flow.epochs
	author.mu.Lock()
	defer author.mu.Unlock()
	epochs.mu.Lock()
	defer epochs.mu.Unlock()
	if proof.profile == nil || flow.profileWorkspace != proof.profile || !v.rehearsalWorkspaceValidLocked(ctx, flow, true) || !profilePreimageObservationSetComplete(flow) {
		return zero, errPressureVolume
	}
	pressure, roots, err := v.profilePreimagesLocked(ctx, flow)
	if err != nil || !v.rehearsalWorkspaceValidLocked(ctx, flow, true) {
		return zero, errPressureVolume
	}
	observed, err := issueObservedProfilePreimages(flow.profileCommands, pressure, roots)
	if err != nil || ctx.Err() != nil {
		return zero, errPressureVolume
	}
	return observed, nil
}

func (proof *executionWorkspaceCustodyProof) issueExecutionProfile(ctx context.Context) (ExecutionProfile, ExecutionProfileAdmissionBinding, executionObservedProfilePreimages, error) {
	var profile ExecutionProfile
	var admission ExecutionProfileAdmissionBinding
	var observed executionObservedProfilePreimages
	err := proof.withProfileLocks(ctx, func(v *executionPressureVolume, flow *ExecutionEpochOne) error {
		var err error
		observed, err = profilePreimagesLocked(ctx, proof, v, flow)
		if err != nil {
			return err
		}
		profile, admission, err = issueExecutionProfileLocked(ctx, v, flow, observed)
		return err
	})
	if err != nil {
		return ExecutionProfile{}, ExecutionProfileAdmissionBinding{}, executionObservedProfilePreimages{}, err
	}
	return profile, admission, observed, nil
}

func (proof *executionWorkspaceCustodyProof) revalidateExecutionProfile(ctx context.Context, profile ExecutionProfile, admission ExecutionProfileAdmissionBinding) error {
	return proof.withProfileLocks(ctx, func(v *executionPressureVolume, flow *ExecutionEpochOne) error {
		observed, err := profilePreimagesLocked(ctx, proof, v, flow)
		if err != nil {
			return err
		}
		current, currentAdmission, err := issueExecutionProfileLocked(ctx, v, flow, observed)
		if err != nil || !reflect.DeepEqual(current, profile) || !reflect.DeepEqual(currentAdmission, admission) {
			return errPressureVolume
		}
		return nil
	})
}

func (proof *executionWorkspaceCustodyProof) withProfileLocks(ctx context.Context, inspect func(*executionPressureVolume, *ExecutionEpochOne) error) error {
	if proof == nil || proof.volume == nil || proof.flow == nil || inspect == nil || ctx == nil || ctx.Err() != nil {
		return errPressureVolume
	}
	v, flow := proof.volume, proof.flow
	v.mu.Lock()
	defer v.mu.Unlock()
	flow.mu.Lock()
	defer flow.mu.Unlock()
	if flow.epochs == nil || flow.epochs.author == nil {
		return errPressureVolume
	}
	author, epochs := flow.epochs.author, flow.epochs
	author.mu.Lock()
	defer author.mu.Unlock()
	epochs.mu.Lock()
	defer epochs.mu.Unlock()
	return inspect(v, flow)
}

func profilePreimagesLocked(ctx context.Context, proof *executionWorkspaceCustodyProof, v *executionPressureVolume, flow *ExecutionEpochOne) (executionObservedProfilePreimages, error) {
	if proof.profile == nil || flow.profileWorkspace != proof.profile || !v.rehearsalWorkspaceValidLocked(ctx, flow, true) || !profileObservationSetComplete(flow) {
		return executionObservedProfilePreimages{}, errPressureVolume
	}
	pressure, roots, err := v.profilePreimagesLocked(ctx, flow)
	if err != nil || !v.rehearsalWorkspaceValidLocked(ctx, flow, true) {
		return executionObservedProfilePreimages{}, errPressureVolume
	}
	observed, err := issueObservedProfilePreimages(flow.profileCommands, pressure, roots)
	if err != nil || ctx.Err() != nil {
		return executionObservedProfilePreimages{}, errPressureVolume
	}
	return observed, nil
}

func issueExecutionProfileLocked(ctx context.Context, v *executionPressureVolume, flow *ExecutionEpochOne, observed executionObservedProfilePreimages) (ExecutionProfile, ExecutionProfileAdmissionBinding, error) {
	builds := flow.epochs.author.request.Builds
	if !authorCustodyBuildBinding(builds, flow.plan.SourceCommit) {
		return ExecutionProfile{}, ExecutionProfileAdmissionBinding{}, errPressureVolume
	}
	// The plan names its authoring source; tool provenance names the separately
	// selected execution source retained by genuine build custody. Release the
	// build lock before tool Check methods acquire it themselves.
	builds.mu.Lock()
	sourceCommit := builds.commits.T422SourceCommit
	builds.mu.Unlock()
	namespace, err := flow.profileSignerNamespace.check(ctx)
	if err != nil {
		return ExecutionProfile{}, ExecutionProfileAdmissionBinding{}, errPressureVolume
	}
	tools, phebsPath, err := observedExecutionProfileToolsLocked(ctx, v, flow)
	if err != nil || validateExecutionTools(tools, flow.plan.ToolPolicy, sourceCommit) != nil || flow.profileHost == nil || validateExecutionHost(flow.profileHost.Host, flow.plan) != nil {
		return ExecutionProfile{}, ExecutionProfileAdmissionBinding{}, errPressureVolume
	}
	configDigests, configDigest, err := observedExecutionProfileConfigsLocked(ctx, flow)
	if err != nil {
		return ExecutionProfile{}, ExecutionProfileAdmissionBinding{}, errPressureVolume
	}
	environment, commands, err := flow.observeProfileEnvironmentCommandsLocked(ctx)
	if err != nil || !reflect.DeepEqual(environment, *flow.profileEnvironment) || !reflect.DeepEqual(commands, flow.profileCommands) {
		return ExecutionProfile{}, ExecutionProfileAdmissionBinding{}, errPressureVolume
	}
	namespace, err = namespace.recheck(ctx)
	if err != nil {
		return ExecutionProfile{}, ExecutionProfileAdmissionBinding{}, errPressureVolume
	}
	return issueObservedExecutionProfile(flow.plan, sourceCommit, tools, flow.profileHost.Host, configDigests, configDigest, environment, commands,
		flow.profileRuntime, phebsPath, flow.epochs.epochs[0].Temporary, observed, namespace)
}

// issueObservedExecutionProfile is the pure end of the issuer. Its inputs are
// private observations collected under the live custody locks above; keeping
// assembly here makes every observed-field mutation independently testable.
func issueObservedExecutionProfile(
	plan Plan,
	sourceCommit string,
	tools []ExecutionToolIdentity,
	host ExecutionHost,
	configDigests []string,
	configDigest string,
	environment executionRuntimeEnvironmentObservation,
	commands []ExecutionCommandProfile,
	runtime *executionRuntimeObservation,
	phebsPath string,
	directory string,
	observed executionObservedProfilePreimages,
	namespace executionSignerNamespaceBinding,
) (ExecutionProfile, ExecutionProfileAdmissionBinding, error) {
	if !validCommit(sourceCommit) || validateExecutionTools(tools, plan.ToolPolicy, sourceCommit) != nil || validateExecutionHost(host, plan) != nil ||
		observed.commandsSHA256 != observed.harnessCommandSetSHA256 || !namespace.valid() {
		return ExecutionProfile{}, ExecutionProfileAdmissionBinding{}, errPressureVolume
	}
	accountingDigest, err := canonicalSHA256(plan.ProcessAccounting)
	if err != nil || plan.ProcessAccounting == nil {
		return ExecutionProfile{}, ExecutionProfileAdmissionBinding{}, errPressureVolume
	}
	derivedConfigDigest, err := canonicalSHA256(configDigests)
	if err != nil || derivedConfigDigest != configDigest {
		return ExecutionProfile{}, ExecutionProfileAdmissionBinding{}, errPressureVolume
	}
	admission := ExecutionProfileAdmissionBinding{
		schema:         plan.ToolPolicy.ExecutionProfileSchema,
		commandsSHA256: observed.commandsSHA256, harnessCommandSetSHA256: observed.harnessCommandSetSHA256,
		pressureCommandSetSHA256: observed.pressureCommandSetSHA256,
		configBytesSHA256:        configDigest, epochConfigBytesSHA256: slices.Clone(configDigests),
		recoveryEnvironmentSHA256: environment.RecoverySHA256, serverEnvironmentSHA256: environment.ServerSHA256,
		rootVolumeBindingsSHA256: observed.rootVolumeBindingsSHA256, closedEnvironment: true,
		processAccountingSHA256: accountingDigest, signerNamespaceSHA256: namespace.digest,
	}
	profile, commandsDigest, err := assembleExecutionProfile(plan, tools, host, admission)
	actualCommandsDigest, actualCommandsErr := canonicalSHA256(commands)
	if err != nil || actualCommandsErr != nil || commandsDigest != observed.commandsSHA256 || actualCommandsDigest != observed.commandsSHA256 ||
		!slices.Equal(environment.Recovery, profile.Environment.BaseVariables) ||
		!slices.Equal(environment.Server, executionProfileServerEnvironment(profile.Environment)) ||
		environment.RecoverySHA256 != executionEnvironmentSHA256(environment.Recovery) ||
		environment.ServerSHA256 != executionEnvironmentSHA256(environment.Server) ||
		validateObservedExecutionRuntime(runtime, plan, profile, tools, phebsPath, directory) != nil {
		return ExecutionProfile{}, ExecutionProfileAdmissionBinding{}, errPressureVolume
	}
	admission.configProjectionSHA256 = profile.Config.ProjectionSHA256
	admission.invocationSHA256 = profile.InvocationSHA256
	admission.profileSHA256, err = canonicalSHA256(profile)
	if err != nil {
		return ExecutionProfile{}, ExecutionProfileAdmissionBinding{}, errPressureVolume
	}
	admission.verifiedBeforeOperationalWork = true
	want, err := expectedExecutionProfile(plan, tools, host, admission)
	if err != nil || !reflect.DeepEqual(want, profile) {
		return ExecutionProfile{}, ExecutionProfileAdmissionBinding{}, errPressureVolume
	}
	return profile, admission, nil
}

func observedExecutionProfileToolsLocked(ctx context.Context, v *executionPressureVolume, flow *ExecutionEpochOne) ([]ExecutionToolIdentity, string, error) {
	author := flow.epochs.author
	tools := make([]ExecutionToolIdentity, 0, len(flow.plan.ToolPolicy.RequiredTools))
	var phebsPath string
	for _, role := range flow.plan.ToolPolicy.RequiredTools {
		var identity ExecutionToolIdentity
		var err error
		switch role {
		case "buf":
			identity, _, err = flow.profileTools[0].Check(ctx, role)
		case "git":
			identity, _, err = author.request.Git.Check(ctx)
		case "go":
			identity, _, err = author.request.Builds.CheckGo(ctx)
		case "hdiutil":
			identity, _, err = v.tool.Check(ctx, role)
		case "phebs":
			identity, phebsPath, err = flow.phebs.Check(ctx, role)
		case "phebs-focused-index":
			identity, _, err = flow.profileTools[1].Check(ctx, role)
		case "ssh-keygen":
			identity, _, err = flow.profileSigner.Check(ctx, role)
		case "surreal":
			identity, _, err = flow.surreal.Check(ctx, role)
		case "t422-author":
			identity, _, err = author.request.Author.Check(ctx, role)
		case "t422-execute":
			identity, err = flow.profileExecutor.check(ctx)
		case "zoekt-git-index":
			identity, _, err = flow.zoekt.Check(ctx, role)
		default:
			return nil, "", errPressureVolume
		}
		if err != nil || identity.Role != role {
			return nil, "", errPressureVolume
		}
		tools = append(tools, identity)
	}
	if phebsPath == "" {
		return nil, "", errPressureVolume
	}
	return tools, phebsPath, nil
}

func observedExecutionProfileConfigsLocked(ctx context.Context, flow *ExecutionEpochOne) ([]string, string, error) {
	epochs, author := flow.epochs, flow.epochs.author
	source := productionSourceURL(author.roots[1].path)
	digests := make([]string, len(epochs.epochs))
	for index, epoch := range epochs.epochs {
		if epochs.checkLocked(ctx, uint64(index+1)) != nil || epochs.parsedConfigs[index] == nil {
			return nil, "", errPressureVolume
		}
		raw, parsed, err := epochConfigBytesParsed(flow.plan, epoch, source)
		if err != nil || SHA256(raw) != epoch.ConfigSHA256 || !reflect.DeepEqual(parsed, epochs.parsedConfigs[index]) {
			return nil, "", errPressureVolume
		}
		digests[index] = epoch.ConfigSHA256
	}
	digest, err := canonicalSHA256(digests)
	if err != nil {
		return nil, "", errPressureVolume
	}
	return digests, digest, nil
}

func executionProfileServerEnvironment(profile ExecutionEnvironmentProfile) []string {
	values := append(slices.Clone(profile.BaseVariables), profile.ServerVariables...)
	slices.Sort(values)
	return values
}

func cloneExecutionProfileAdmission(value ExecutionProfileAdmissionBinding) ExecutionProfileAdmissionBinding {
	value.epochConfigBytesSHA256 = slices.Clone(value.epochConfigBytesSHA256)
	return value
}

func profileObservationSetComplete(flow *ExecutionEpochOne) bool {
	return profilePreimageObservationSetComplete(flow) &&
		flow.profileExecutor != nil && flow.profileSignerNamespace != nil
}

func profilePreimageObservationSetComplete(flow *ExecutionEpochOne) bool {
	return processAccountingPlanSemantics(flow.plan.Schema) && flow.profileEnvironmentUsed && flow.profileEnvironment != nil && len(flow.profileCommands) == 3 &&
		flow.profileHostUsed && flow.profileHost != nil && flow.profileSystemUsed &&
		flow.profileSigner != nil && flow.profileTools[0] != nil && flow.profileTools[1] != nil &&
		flow.profileRuntime != nil && flow.profileRuntime.Complete && flow.profileRuntime.err == nil && flow.profileRuntime.releasable()
}

// Caller holds volume, flow, author and epochs locks. Each named role is read
// from its production-held descriptor and current path; the config role checks
// both config and catalog roots before collapsing their common FSID.
func (v *executionPressureVolume) profilePreimagesLocked(ctx context.Context, flow *ExecutionEpochOne) (executionPressureCommandSetPreimageV1, executionRootVolumeBindingsPreimageV1, error) {
	var pressure executionPressureCommandSetPreimageV1
	var roots executionRootVolumeBindingsPreimageV1
	if ctx == nil || ctx.Err() != nil || v.pressureCommandCount != 2 || v.device == "" || v.device != v.attachDevice ||
		v.ballast == nil || v.ballast.file == nil || v.ballast.failed || v.ballast.removed || flow.profileHost == nil {
		return pressure, roots, errPressureVolume
	}
	signerIdentity, signerPath, err := flow.profileSigner.Check(ctx, "ssh-keygen")
	if err != nil || flow.profileSignerImage != (executionProfileSystemImage{Identity: signerIdentity, Path: signerPath}) {
		return pressure, roots, errPressureVolume
	}
	for index, role := range [2]string{"buf", "phebs-focused-index"} {
		if _, _, err := flow.profileTools[index].Check(ctx, role); err != nil {
			return pressure, roots, errPressureVolume
		}
	}
	identity, toolPath, err := v.tool.Check(ctx, "hdiutil")
	if err != nil {
		return pressure, roots, errPressureVolume
	}
	pressure, err = executionPressureCommandSetPreimage(identity, toolPath, v.root.path, v.attachDevice, v.pressureCommands)
	if err != nil {
		return executionPressureCommandSetPreimageV1{}, roots, errPressureVolume
	}
	author, epochs := flow.epochs.author, flow.epochs
	backing, err := heldProductionRootVolume(v.parent)
	if err != nil {
		return executionPressureCommandSetPreimageV1{}, roots, errPressureVolume
	}
	source, err := heldProductionRootVolume(author.roots[1])
	if err != nil {
		return executionPressureCommandSetPreimageV1{}, roots, errPressureVolume
	}
	data, err := heldProductionRootVolume(epochs.roots[0])
	if err != nil {
		return executionPressureCommandSetPreimageV1{}, roots, errPressureVolume
	}
	backup, err := heldProductionRootVolume(epochs.roots[3])
	if err != nil {
		return executionPressureCommandSetPreimageV1{}, roots, errPressureVolume
	}
	home, err := heldProductionRootVolume(epochs.roots[1])
	if err != nil {
		return executionPressureCommandSetPreimageV1{}, roots, errPressureVolume
	}
	temporary, err := heldProductionRootVolume(epochs.roots[2])
	if err != nil {
		return executionPressureCommandSetPreimageV1{}, roots, errPressureVolume
	}
	toolOutput, err := heldBuildDirectoryVolume(author.request.Builds)
	if err != nil {
		return executionPressureCommandSetPreimageV1{}, roots, errPressureVolume
	}
	if _, err := v.ballast.sample(); err != nil {
		return executionPressureCommandSetPreimageV1{}, roots, errPressureVolume
	}
	ballast := v.workspace.volume // sample read this value through the held ballast inode.
	inputs := []*ExecutionInputCustody{author.request.Plan, author.request.Git.input, epochs.catalogs, epochs.configs}
	// The already-running executor is launcher-owned outside this later mounted
	// workspace. Its live held-image/path/SHA check above is the independent
	// authority; it is intentionally not relabeled as a pressure-volume input.
	for _, tool := range profileMountedInputTools(flow) {
		inputs = append(inputs, tool.input)
	}
	inputVolumes := make([][2]int32, len(inputs))
	for index, input := range inputs {
		volume, inputErr := heldInputDirectoryVolume(input)
		if inputErr != nil || volume != data {
			return executionPressureCommandSetPreimageV1{}, roots, errPressureVolume
		}
		inputVolumes[index] = volume
	}
	config, catalog := inputVolumes[3], inputVolumes[2]
	if catalog != config {
		return executionPressureCommandSetPreimageV1{}, roots, errPressureVolume
	}
	roots, err = executionRootVolumeBindingsFromObservation(*flow.profileHost,
		[9][2]int32{backing, source, config, data, backup, home, temporary, toolOutput, ballast})
	if err != nil {
		return executionPressureCommandSetPreimageV1{}, executionRootVolumeBindingsPreimageV1{}, errPressureVolume
	}
	return pressure, roots, nil
}

func profileMountedInputTools(flow *ExecutionEpochOne) []*ExecutionToolCustody {
	return []*ExecutionToolCustody{
		flow.epochs.author.request.Author, flow.phebs, flow.zoekt, flow.surreal, flow.profileTools[0], flow.profileTools[1],
	}
}

func heldProductionRootVolume(root productionRoot) ([2]int32, error) {
	if pressureRootsUnchanged(root) != nil {
		return [2]int32{}, errPressureVolume
	}
	return root.volume, nil // pressureRootsUnchanged just re-read this FSID.
}

func heldInputDirectoryVolume(input *ExecutionInputCustody) ([2]int32, error) {
	if input == nil {
		return [2]int32{}, errPressureVolume
	}
	input.mu.Lock()
	defer input.mu.Unlock()
	if input.closed || input.err != nil || input.root == nil {
		return [2]int32{}, errPressureVolume
	}
	return heldDirectoryVolume(input.root, input.rootInfo, input.directory, input.volume)
}

func heldBuildDirectoryVolume(builds *ExecutionGoBuildCustody) ([2]int32, error) {
	if builds == nil {
		return [2]int32{}, errPressureVolume
	}
	builds.mu.Lock()
	defer builds.mu.Unlock()
	if builds.closed || builds.err != nil || builds.root == nil {
		return [2]int32{}, errPressureVolume
	}
	return heldDirectoryVolume(builds.root, nil, builds.directory, builds.volume)
}

func heldDirectoryVolume(file *os.File, original os.FileInfo, path string, expected [2]int32) ([2]int32, error) {
	if file == nil || !filepath.IsAbs(path) {
		return [2]int32{}, errPressureVolume
	}
	held, err := file.Stat()
	current, pathErr := os.Lstat(path)
	canonical, canonicalErr := filepath.EvalSymlinks(path)
	volume, volumeErr := inputCustodyVolume(file)
	if err != nil || pathErr != nil || canonicalErr != nil || volumeErr != nil || canonical != path ||
		!os.SameFile(held, current) || original != nil && !os.SameFile(original, held) || !inputCustodyOwned(current) ||
		!current.IsDir() || current.Mode().Perm() != 0o700 || volume != expected {
		return [2]int32{}, errPressureVolume
	}
	return volume, nil
}
