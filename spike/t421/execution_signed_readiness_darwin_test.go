//go:build darwin

package t421

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

// TestPrepareExecutionSignedReadinessExecutor is the sole opt-in producer for
// the immutable executor consumed by signed readiness and the later controller.
// Successful output deliberately survives the test; no ceremony ID is selected.
func TestPrepareExecutionSignedReadinessExecutor(t *testing.T) {
	output := os.Getenv("PHEBS_T422_PREPARE_EXECUTOR")
	if output == "" {
		t.Skip("requires an explicit executor preparation path")
	}
	parent := filepath.Dir(output)
	if !executionSignedReadinessPreparationPath(output) {
		t.Fatal("executor preparation path must be /private/tmp/<new-directory>/t422-execute")
	}
	if _, err := os.Lstat(parent); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("executor preparation parent must not exist", err)
	}
	t.Logf("executor preparation target; retain for disposition after any publication failure: %s", output)
	root, err := os.MkdirTemp("/private/tmp", "t422-executor-preparation-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error("executor preparation scratch cleanup", err)
		}
	})
	bootstrap := filepath.Join(root, "bootstrap")
	workspace := filepath.Join(root, "build")
	for _, path := range []string{bootstrap, workspace, filepath.Join(workspace, "home"), filepath.Join(workspace, "tmp"), filepath.Join(workspace, "cache")} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Minute)
	defer cancel()
	git, err := ProtectExecutionGit(ctx, bootstrap, os.Getenv("PHEBS_T422_PRODUCTION_GIT"))
	gitCustodyTestCleanup(t, git)
	if err != nil {
		t.Fatal("executor preparation Git custody", err)
	}
	inputs, err := ProtectExecutionGoBuildInputs(ctx, bootstrap, ExecutionGoBuildRequest{
		Git: git, RepositoryRoot: os.Getenv("PHEBS_T422_PRODUCTION_REPOSITORY"),
		PlanSourceCommit:     os.Getenv("PHEBS_T422_PLAN_SOURCE_COMMIT"),
		IntegratedMainCommit: os.Getenv("PHEBS_T422_INTEGRATED_MAIN_COMMIT"),
		SourceCommit:         os.Getenv("PHEBS_T422_PRODUCTION_COMMIT"),
		GoRoot:               os.Getenv("PHEBS_T422_PRODUCTION_GOROOT"), ModuleCache: os.Getenv("PHEBS_T422_PRODUCTION_MODULE_CACHE"),
	})
	goBuildTestCleanup(t, inputs)
	if err != nil {
		t.Fatal("executor preparation build-input custody", err)
	}
	candidate := productionRehearsalBuildSchema(t, ctx, inputs, workspace, "t422-execute", PlanV4Schema)
	if ctx.Err() != nil || inputs.Check(ctx) != nil {
		t.Fatal("executor preparation inputs changed or expired")
	}
	digest, err := publishExecutionSignedReadinessExecutor(ctx, candidate, output)
	if err != nil {
		t.Fatal("prepared executor publication retained for disposition", err)
	}
	t.Logf("prepared immutable executor: path=%s digest=%s", output, digest)
}

// This selector authorizes one rehearsal, including its ephemeral signature and
// its exact live authorization message. It does not select or resume the later
// formal T42.2o freeze. No selector is forwarded to the production process.
// The ordinary executable performs all preparation, fifteen phases, signing,
// custody checks and outer verification with its unchanged limits.
func TestExecutionSignedLauncherOptionalReadiness(t *testing.T) {
	mode := os.Getenv("PHEBS_T422_SIGNED_LAUNCHER_REHEARSAL")
	if mode == "" {
		t.Skip("requires explicit serial signed-launcher rehearsal selection")
	}
	if !executionSignedReadinessMode(mode) {
		t.Fatal("unknown signed rehearsal mode; no custody acquired")
	}
	requireExternalToolFrozenHost(t)
	if os.Getenv("PHEBS_T422_EXTERNAL_SURREAL") == "" {
		t.Fatal("selected signed rehearsal requires an explicit native SurrealDB image")
	}
	selection := executionSelectionV1{
		Schema:               executionSelectionSchema,
		RepositoryRoot:       os.Getenv("PHEBS_T422_PRODUCTION_REPOSITORY"),
		PlanSourceCommit:     os.Getenv("PHEBS_T422_PLAN_SOURCE_COMMIT"),
		IntegratedMainCommit: os.Getenv("PHEBS_T422_INTEGRATED_MAIN_COMMIT"),
		SourceCommit:         os.Getenv("PHEBS_T422_PRODUCTION_COMMIT"),
		GoRoot:               os.Getenv("PHEBS_T422_PRODUCTION_GOROOT"),
		ModuleCache:          os.Getenv("PHEBS_T422_PRODUCTION_MODULE_CACHE"),
		GitBinary:            os.Getenv("PHEBS_T422_PRODUCTION_GIT"),
		SurrealBinary:        toolCustodyExternalSurreal(t),
	}
	// Refuse input shape before even creating the test's private root. The two
	// temporary strings only stand in for paths/ID that the test will own below;
	// they are never passed to an issuer or to the launcher.
	selection.CeremonyID = "readiness"
	selection.SignerControlRoot = "/private/tmp/t422-readiness-selection-shape"
	if !validExecutionSelection(selection) {
		t.Fatal("explicit canonical repository, source/main commits and native tools required")
	}
	preparedExecutor := os.Getenv("PHEBS_T422_PRODUCTION_EXECUTOR")
	preparedInfo, preparedParentInfo, err := executionSignedReadinessPreparedExecutor(preparedExecutor)
	if err != nil {
		t.Fatal("exact immutable prepared executor required", err)
	}
	root, err := os.MkdirTemp("/private/tmp", "t422-signed-readiness-")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("private rehearsal evidence/ephemeral signer custody: %s", root)
	diagnosticRoot, err := openProductionRoot(root)
	if err != nil {
		t.Fatal("private rehearsal root custody", err)
	}
	t.Cleanup(func() {
		if err := diagnosticRoot.file.Close(); err != nil {
			t.Error("close private rehearsal root custody", err)
		}
	})
	// Never testing.TempDir: a failed real launcher can leave mounted custody.
	// Keep the namespace claims, private key and returned source-free evidence
	// for attribution. No private key is read or copied by the harness.
	selection.CeremonyID = filepath.Base(root)
	selection.SignerControlRoot = filepath.Join(root, "signer")
	bootstrap := filepath.Join(root, "bootstrap")
	for _, path := range []string{selection.SignerControlRoot, bootstrap} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if !validExecutionSelection(selection) {
		t.Fatal("owned rehearsal selections are not canonical")
	}
	namespace, err := holdExecutionSignerNamespace(t.Context(), selection.SignerControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = namespace.Close() }()
	// Bootstrap is outside the measured outer lifetime. The exact immutable
	// controller candidate is copied, then independently rebuilt from genuine
	// protected source/SDK/module custody. The command is not this TestMain.
	buildCtx, stopBuild := context.WithTimeout(t.Context(), 20*time.Minute)
	defer stopBuild()
	git, err := ProtectExecutionGit(buildCtx, bootstrap, selection.GitBinary)
	if err != nil {
		t.Fatal("retained bootstrap Git custody", err)
	}
	inputs, err := ProtectExecutionGoBuildInputs(buildCtx, bootstrap, ExecutionGoBuildRequest{
		Git: git, RepositoryRoot: selection.RepositoryRoot, PlanSourceCommit: selection.PlanSourceCommit,
		IntegratedMainCommit: selection.IntegratedMainCommit, SourceCommit: selection.SourceCommit,
		GoRoot: selection.GoRoot, ModuleCache: selection.ModuleCache,
	})
	if err != nil {
		t.Fatal("retained bootstrap build inputs", err)
	}
	executor, err := inputs.protectReferenceTool(buildCtx, bootstrap, "t422-execute", preparedExecutor, PlanV4Schema)
	if err != nil {
		t.Fatal("retained bootstrap executor", err)
	}
	identity, executable, err := executor.Check(buildCtx, "t422-execute")
	if err != nil {
		t.Fatal(err)
	}
	stopBuild()
	t.Logf("source=%s integrated-main=%s exact prepared/protected executor=%s", selection.SourceCommit, selection.IntegratedMainCommit, identity.SHA256)

	ctx, cancel := context.WithTimeout(t.Context(), executionMaximumWall)
	defer cancel()
	reader, writer := executionLauncherOutputPipe(t, true)
	command := exec.CommandContext(ctx, executable, executionOuterMode, "--selection-base64url", encodeExecutionSelection(t, selection))
	command.Env, command.Stdout, command.Stderr = []string{}, writer, io.Discard
	command.WaitDelay = 5 * time.Second
	command.Cancel = func() error { return command.Process.Signal(syscall.SIGTERM) }
	prepareProductionSession(command)
	frames := make(chan executionAuthorizationHandoffFrame, 1)
	packages := make(chan executionReturnedOutput, 1)
	captured := make(chan struct{})
	go func() {
		defer close(captured)
		captureExecutionReturnedOutput(reader, frames, packages)
	}()
	if err := command.Start(); err != nil {
		_ = writer.Close()
		_ = reader.Close()
		<-captured
		t.Fatal(err)
	}
	_ = writer.Close() // The child owns the sole remaining write descriptor.
	waited := make(chan error, 1)
	go func() { waited <- command.Wait() }()
	joined := false
	innerSession := 0
	defer func() {
		if !joined {
			_ = command.Process.Signal(syscall.SIGTERM)
		}
		// Inner owns a distinct session. Always account for both known scopes,
		// including a t.Fatal after the outer's native Wait already completed.
		var cleanupErr error
		var empty bool
		joined, empty, cleanupErr = finishExecutionProcessSession(command.Process.Pid, waited, joined, nil, time.Now().Add(5*time.Second))
		if executionSignedReadinessOrdinaryJoinedExit(joined, empty, cleanupErr) {
			// This is only a cleanup classification. The original premature
			// handoff/readiness failure remains recorded by the calling test.
			t.Log("rehearsal outer joined with ordinary failure and empty observed session; retain all named custody", cleanupErr)
		} else if cleanupErr != nil || !joined || !empty {
			t.Error("rehearsal outer/session cleanup unavailable or forced; retain all named custody", cleanupErr)
		}
		if innerSession > 0 {
			if err := t4013.WaitPrivateProcessSession(innerSession, time.Now().Add(5*time.Second)); err != nil {
				// Emergency harness cleanup is never accepted as a clean native
				// launcher result. Retain failure even if the forced sweep works.
				t.Error("captured inner session did not close; emergency cleanup required", err)
				// Observe member lifetimes and kernel command names before the
				// sweep; these are not executable-image identities.
				captureExecutionSessionMembership(t, diagnosticRoot, innerSession)
				killErr := t4013.KillPrivateProcessSession(innerSession)
				closeErr := t4013.WaitPrivateProcessSession(innerSession, time.Now().Add(6*time.Second))
				if killErr != nil || closeErr != nil {
					t.Error("captured inner session remains unavailable after bounded cleanup", killErr, closeErr)
				}
			}
		}
		_ = reader.Close()
		<-captured
	}()
	var frame executionAuthorizationHandoffFrame
	select {
	case frame = <-frames:
	case <-ctx.Done():
		t.Fatal("live handoff unavailable before effective harness/outer deadline", ctx.Err(), t.Context().Err())
	}
	if frame.err != nil || frame.value.T422ExecuteImageSHA256 != identity.SHA256 || frame.value.ClientArgv[0] != executable {
		t.Fatal("real launcher failed before a matching signed handoff", frame.err)
	}
	// At this quiescent boundary preparation children have joined, the client
	// has not started, and the operational sequence is still unauthorized.
	live, err := t4013.ObserveProcessTreeRecords(ctx, command.Process.Pid)
	if err == nil && len(live) > 1 && live[1].ParentPID == command.Process.Pid {
		if observed, observeErr := unix.Getsid(live[1].PID); observeErr == nil && observed == live[1].PID && observed != command.Process.Pid {
			innerSession = observed
		}
	}
	if err != nil || len(live) != 2 || live[0].PID != command.Process.Pid || live[1].ParentPID != command.Process.Pid {
		t.Fatal("live handoff did not expose exactly the native outer/inner pair", err)
	}
	if innerSession == 0 {
		t.Fatal("inner is not its own distinct native session")
	}
	if err := os.WriteFile(filepath.Join(root, "authorization-handoff.json"), frame.raw, 0o600); err != nil {
		t.Fatal(err)
	}
	operational := filepath.Dir(frame.value.SocketPath)
	operationalInfo, err := os.Lstat(operational)
	if err != nil || !operationalInfo.IsDir() || filepath.Dir(operational) != "/private/tmp" || !strings.HasPrefix(filepath.Base(operational), "phebs-t422-") {
		t.Fatal("unexpected live operational root")
	}
	t.Logf("live rehearsal operational custody: %s; admission expires %d", operational, frame.value.FinalAdmissionDeadlineUnixNano)
	// Anchor verification in the independently selected, actually held signer
	// namespace before authorizing. Returned signer.pub is never this anchor.
	names := executionSignerNames(selection.CeremonyID)
	public, err := executionSignedReadinessPublic(ctx, namespace, names.generatedPublic)
	if err != nil {
		t.Fatal("independent rehearsal public key unavailable", err)
	}
	if mode == "orphan-preparation" {
		joined = executionSignedReadinessOrphan(t, ctx, root, selection, frame, live[1], operationalInfo, command, waited, packages, captured)
		return // Actual refused custody remains; no bootstrap cleanup/pass claim.
	}
	clientArgs := append([]string(nil), frame.value.ClientArgv...)
	var signerBefore, replacementInfo os.FileInfo
	var signerClaimBefore []byte
	switch mode {
	case "reject-authorization":
		clientArgs, err = executionSignedReadinessWrongAuthorization(frame.value)
		if err != nil {
			t.Fatal(err)
		}
	case "replace-signer-namespace":
		// Rename only this test's independently created signer directory, after
		// all signer children joined at the live wait. Preserve both identities.
		signerBefore, err = os.Lstat(selection.SignerControlRoot)
		if err != nil {
			t.Fatal(err)
		}
		signerClaimBefore, err = executionSignedReadinessSignerBytes(ctx, namespace, names.claim)
		if err != nil || os.Rename(selection.SignerControlRoot, selection.SignerControlRoot+"-retained") != nil || os.Mkdir(selection.SignerControlRoot, 0o700) != nil {
			t.Fatal("could not install owned namespace replacement")
		}
		replacementInfo, err = os.Lstat(selection.SignerControlRoot)
		if err != nil || os.SameFile(signerBefore, replacementInfo) {
			t.Fatal("namespace replacement did not create a distinct owned identity")
		}
	case "cancel-wait":
		if err := command.Process.Signal(syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
	}
	if mode != "cancel-wait" {
		clientCtx, stopClient := context.WithDeadline(ctx, time.Unix(0, frame.value.FinalAdmissionDeadlineUnixNano))
		client := exec.CommandContext(clientCtx, clientArgs[0], clientArgs[1:]...)
		client.Env, client.Stdout, client.Stderr = []string{}, io.Discard, io.Discard
		client.WaitDelay = 5 * time.Second
		clientErr := client.Run()
		stopClient()
		if clientErr != nil {
			t.Fatal("exact live authorization client failed", clientErr)
		}
	}
	var waitErr error
	select {
	case waitErr = <-waited:
		joined = true
	case <-ctx.Done():
		t.Fatal("launcher did not join before effective harness/outer deadline", ctx.Err(), t.Context().Err())
	}
	finishedAt := time.Now()
	if err := t4013.WaitPrivateProcessSession(command.Process.Pid, time.Now().Add(5*time.Second)); err != nil {
		t.Fatal("outer private session did not close", err)
	}
	if err := t4013.WaitPrivateProcessSession(innerSession, time.Now().Add(5*time.Second)); err != nil {
		t.Fatal("inner private session did not close", err)
	}
	select {
	case <-captured:
	case <-ctx.Done():
		t.Fatal("launcher output remained open after Wait")
	}
	var returned executionReturnedOutput
	select {
	case returned = <-packages:
	default:
		t.Fatal("capture did not finish the returned output boundary")
	}
	if mode == "healthy" {
		if waitErr != nil || returned.err != nil {
			if len(returned.raw) > 0 {
				_ = os.WriteFile(filepath.Join(root, "stopped-package.bin"), returned.raw, 0o600)
			}
			t.Fatal("healthy signed launcher did not return success; preserve actual stopped evidence", waitErr, returned.err)
		}
		verified, err := verifyExecutionReturnedPackage(ctx, returned.raw, selection, frame.value.FreezeSHA256)
		if err != nil {
			t.Fatal("independent complete returned-package replay", err)
		}
		files, err := inspectExecutionReturnedPackage(returned.raw, Plan{SealPolicy: frozenSealPolicy()})
		if err != nil || !bytes.Equal(public, files["signer.pub"]) || verified.receipt.Decision.Outcome != "passed" || verified.receipt.Teardown.Outcome != "clean" || len(verified.receipt.PhaseResults) != 15 {
			t.Fatal("independent signer, success or complete phase evidence mismatch")
		}
		if err := os.WriteFile(filepath.Join(root, "returned-package.bin"), returned.raw, 0o600); err != nil {
			t.Fatal(err)
		}
		t.Logf("actual fifteen-phase signed launcher passed; returned package=%s bytes=%d", verified.digest, len(returned.raw))
	} else {
		var exit *exec.ExitError
		if !executionSignedReadinessTimelyRefusal(ctx.Err(), finishedAt, time.Unix(0, frame.value.FinalAdmissionDeadlineUnixNano)) ||
			!errors.As(waitErr, &exit) || waitErr != exit || exit.ProcessState == nil || !exit.Exited() || exit.ExitCode() != 1 || returned.err == nil || len(returned.raw) != 0 {
			t.Fatal("expected timely ordinary exit status 1 without operational receipt; timeout/signal is unavailable evidence", waitErr, returned.err, ctx.Err())
		}
		t.Logf("real signed-wait refusal %s: timely ordinary native status 1, no operational package; not an executed stopped-receipt test", mode)
	}
	if mode == "replace-signer-namespace" {
		retained, retainedErr := os.Lstat(selection.SignerControlRoot + "-retained")
		replacement, replacementErr := os.Lstat(selection.SignerControlRoot)
		if retainedErr != nil || replacementErr != nil || !os.SameFile(signerBefore, retained) || !os.SameFile(replacementInfo, replacement) {
			t.Fatal("refused namespace drift did not retain both exact external identities")
		}
		retainedNamespace, err := holdExecutionSignerNamespace(ctx, selection.SignerControlRoot+"-retained")
		if err != nil {
			t.Fatal("retained original namespace unavailable", err)
		}
		defer func() { _ = retainedNamespace.Close() }()
		retainedPublic, publicErr := executionSignedReadinessPublic(ctx, retainedNamespace, names.generatedPublic)
		retainedClaim, claimErr := executionSignedReadinessSignerBytes(ctx, retainedNamespace, names.claim)
		if publicErr != nil || claimErr != nil || !bytes.Equal(public, retainedPublic) || !bytes.Equal(signerClaimBefore, retainedClaim) {
			t.Fatal("refused namespace drift changed original public key or spent claim", publicErr, claimErr)
		}
	}
	if _, err := os.Lstat(operational); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("operational custody remains; this is retained failure, not clean readiness", err)
	}
	currentPreparedInfo, currentPreparedParentInfo, err := executionSignedReadinessPreparedExecutor(preparedExecutor)
	currentPreparedDigest, digestErr := t4013.DigestHostExecutable(ctx, preparedExecutor)
	if err != nil || digestErr != nil || currentPreparedDigest != identity.SHA256 ||
		!inputCustodySame(preparedInfo, currentPreparedInfo) || !inputCustodySame(preparedParentInfo, currentPreparedParentInfo) {
		t.Fatal("prepared controller executor changed during readiness", err, digestErr)
	}
	// Only a joined, verified, removed operational run permits bootstrap copy
	// cleanup. Namespace/key claims and source-free evidence remain deliberate.
	gitCustodyTestCleanup(t, git)
	goBuildTestCleanup(t, inputs)
	inputCustodyTestCleanup(t, executor.input, []ExecutionInputCopy{{Name: "t422-execute"}})
	t.Logf("rehearsal complete; retained ephemeral signer claims/evidence, with exact bootstrap copy cleanup registered: %s", root)
}

func executionSignedReadinessTimelyRefusal(ctxErr error, finished, deadline time.Time) bool {
	return ctxErr == nil && !finished.IsZero() && !deadline.IsZero() && finished.Before(deadline)
}

func executionSignedReadinessMode(mode string) bool {
	switch mode {
	case "healthy", "reject-authorization", "cancel-wait", "replace-signer-namespace", "orphan-preparation":
		return true
	default:
		return false
	}
}

func executionSignedReadinessPreparationPath(path string) bool {
	if len(path) > maxInputCustodyPathBytes || !filepath.IsAbs(path) || filepath.Clean(path) != path ||
		filepath.Base(path) != "t422-execute" {
		return false
	}
	return filepath.Dir(filepath.Dir(path)) == "/private/tmp"
}

// publishExecutionSignedReadinessExecutor is create-only. Once it creates the
// final parent, any failure retains that exact path for reviewed disposition.
func publishExecutionSignedReadinessExecutor(ctx context.Context, candidate, output string) (string, error) {
	if ctx == nil || ctx.Err() != nil || !executionSignedReadinessPreparationPath(output) {
		return "", ErrExecutionLauncher
	}
	parent := filepath.Dir(output)
	if _, err := os.Lstat(parent); !errors.Is(err, os.ErrNotExist) {
		return "", ErrExecutionLauncher
	}
	canonical, err := filepath.EvalSymlinks(candidate)
	before, statErr := os.Lstat(candidate)
	if err != nil || statErr != nil || canonical != candidate || !inputCustodyOwned(before) || !before.Mode().IsRegular() ||
		before.Size() < 1 || before.Size() > maxInputCustodyFileBytes || before.Mode().Perm()&0o111 == 0 || before.Mode().Perm()&0o022 != 0 {
		return "", ErrExecutionLauncher
	}
	if err := os.Chmod(candidate, 0o500); err != nil || ctx.Err() != nil {
		return "", ErrExecutionLauncher
	}
	ready, err := os.Lstat(candidate)
	if err != nil || !os.SameFile(before, ready) || ready.Mode().Perm() != 0o500 {
		return "", ErrExecutionLauncher
	}
	if err := os.Mkdir(parent, 0o700); err != nil {
		return "", ErrExecutionLauncher
	}
	parentFile, err := os.Open(parent)
	if err != nil {
		return "", ErrExecutionLauncher
	}
	parentInfo, parentStatErr := parentFile.Stat()
	if parentStatErr != nil || !inputCustodyOwned(parentInfo) || !parentInfo.IsDir() || parentInfo.Mode().Perm() != 0o700 ||
		os.Rename(candidate, output) != nil {
		_ = parentFile.Close()
		return "", ErrExecutionLauncher
	}
	file, err := t4013.OpenHostImage(output)
	if err != nil {
		_ = parentFile.Close()
		return "", ErrExecutionLauncher
	}
	current, currentErr := file.Stat()
	if currentErr != nil || !os.SameFile(ready, current) || ctx.Err() != nil || file.Sync() != nil || parentFile.Sync() != nil ||
		inputCustodyFlag(file, true) != nil || inputCustodyFlag(parentFile, true) != nil {
		_ = file.Close()
		_ = parentFile.Close()
		return "", ErrExecutionLauncher
	}
	if errors.Join(file.Close(), parentFile.Close()) != nil {
		return "", ErrExecutionLauncher
	}
	if _, _, err := executionSignedReadinessPreparedExecutor(output); err != nil {
		return "", ErrExecutionLauncher
	}
	digest, err := t4013.DigestHostExecutable(ctx, output)
	if err != nil {
		return "", ErrExecutionLauncher
	}
	return digest, nil
}

func executionSignedReadinessPreparedExecutor(path string) (os.FileInfo, os.FileInfo, error) {
	if len(path) > maxInputCustodyPathBytes || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, nil, ErrExecutionLauncher
	}
	canonical, err := filepath.EvalSymlinks(path)
	info, infoErr := os.Lstat(path)
	parentInfo, parentErr := os.Lstat(filepath.Dir(path))
	if err != nil || infoErr != nil || parentErr != nil || canonical != path || !info.Mode().IsRegular() ||
		info.Mode().Perm() != 0o500 || info.Size() < 1 || info.Size() > maxInputCustodyFileBytes || !inputCustodyProtected(info) ||
		!parentInfo.IsDir() || parentInfo.Mode().Perm() != 0o700 || !inputCustodyProtected(parentInfo) {
		return nil, nil, ErrExecutionLauncher
	}
	return info, parentInfo, nil
}

func executionSignedReadinessPublic(ctx context.Context, namespace *executionSignerNamespaceCustody, name string) ([]byte, error) {
	raw, err := executionSignedReadinessSignerBytes(ctx, namespace, name)
	if err != nil {
		return nil, err
	}
	canonical, err := executionSignerCanonicalFromGenerated(raw)
	if err != nil {
		return nil, err
	}
	public, _, _, err := deriveExecutionSignerPublic(canonical)
	return public, err
}

// executionSignerCanonicalFromGenerated recovers the canonical one-space public
// line (ssh-ed25519 <base64>\n) from the generated ssh-keygen form, which carries
// an empty trailing comment (ssh-ed25519 <base64> \n). The stably named held file
// in the namespace is the generated one, but the returned signer.pub is canonical,
// so the readiness anchor must be canonicalized before the canonical validator.
// The round trip through executionSignerGeneratedPublic keeps the recovery exact.
func executionSignerCanonicalFromGenerated(generated []byte) ([]byte, error) {
	n := len(generated)
	if n < 2 || generated[n-1] != '\n' || generated[n-2] != ' ' {
		return nil, ErrExecutionLauncher
	}
	canonical := make([]byte, 0, n-1)
	canonical = append(canonical, generated[:n-2]...)
	canonical = append(canonical, '\n')
	if !bytes.Equal(generated, executionSignerGeneratedPublic(canonical)) {
		return nil, ErrExecutionLauncher
	}
	return canonical, nil
}

func TestExecutionSignedReadinessGeneratedPublic(t *testing.T) {
	blob := append([]byte("\x00\x00\x00\x0bssh-ed25519\x00\x00\x00\x20"), make([]byte, 32)...)
	canonical := "ssh-ed25519 " + base64.StdEncoding.EncodeToString(blob) + "\n"
	for _, test := range []struct {
		name, raw string
		valid     bool
	}{
		{"generated empty comment", strings.TrimSuffix(canonical, "\n") + " \n", true},
		{"canonical is not generated", canonical, false},
		{"nonempty comment", strings.TrimSuffix(canonical, "\n") + " comment\n", false},
		{"extra space", strings.TrimSuffix(canonical, "\n") + "  \n", false},
		{"malformed key", "ssh-ed25519 !!! \n", false},
		{"empty", "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			public, err := executionSignerCanonicalFromGenerated([]byte(test.raw))
			if err == nil {
				public, _, _, err = deriveExecutionSignerPublic(public)
			}
			if (err == nil) != test.valid || test.valid && string(public) != canonical {
				t.Fatal("generated public anchor classification differs", err)
			}
		})
	}
}

func executionSignedReadinessSignerBytes(ctx context.Context, namespace *executionSignerNamespaceCustody, name string) ([]byte, error) {
	if _, err := namespace.check(ctx); err != nil || filepath.Base(name) != name {
		return nil, ErrExecutionLauncher
	}
	path := filepath.Join(namespace.path, name)
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, ErrExecutionLauncher
	}
	defer func() { _ = file.Close() }()
	held, heldErr := file.Stat()
	current, currentErr := os.Lstat(path)
	if heldErr != nil || currentErr != nil || !held.Mode().IsRegular() || held.Mode().Perm() != 0o600 || !os.SameFile(held, current) || held.Size() < 1 || held.Size() > maxExecutionSignerKeyBytes {
		return nil, ErrExecutionLauncher
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxExecutionSignerKeyBytes+1))
	if err != nil {
		return nil, err
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(held, after) || after.Size() != held.Size() || int64(len(raw)) != held.Size() {
		return nil, ErrExecutionLauncher
	}
	if _, err := namespace.check(ctx); err != nil {
		return nil, err
	}
	return raw, nil
}

func executionSignedReadinessWrongAuthorization(frame executionAuthorizationHandoffV1) ([]string, error) {
	value, err := decodeExecutionAuthorization([]byte(frame.AuthorizationJSON))
	if err != nil || len(frame.ClientArgv) != 6 {
		return nil, errExecutionAuthorization
	}
	if value.FreezeSHA256[0] == '0' {
		value.FreezeSHA256 = "1" + value.FreezeSHA256[1:]
	} else {
		value.FreezeSHA256 = "0" + value.FreezeSHA256[1:]
	}
	raw, err := canonicalExecutionAuthorization(value)
	if err != nil {
		return nil, err
	}
	args := append([]string(nil), frame.ClientArgv...)
	args[5] = base64.RawURLEncoding.EncodeToString(raw)
	return args, nil
}

func TestExecutionSignedReadinessSelectors(t *testing.T) {
	for _, test := range []struct {
		mode string
		want bool
	}{
		{"healthy", true}, {"reject-authorization", true}, {"cancel-wait", true}, {"replace-signer-namespace", true},
		{"orphan-preparation", true},
		{"", false}, {"1", false}, {"formal", false}, {"healthy,orphan", false},
	} {
		t.Run(test.mode, func(t *testing.T) {
			if executionSignedReadinessMode(test.mode) != test.want {
				t.Fatal("selector authority expanded")
			}
		})
	}
}

func TestExecutionSignedReadinessPreparedExecutor(t *testing.T) {
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "t422-execute")
	if err := os.WriteFile(path, []byte("prepared executor fixture"), 0o500); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executionSignedReadinessPreparedExecutor(path); err == nil {
		t.Fatal("mutable prepared executor admitted")
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	parentFile, err := os.Open(parent)
	if err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := inputCustodyFlag(file, false); err != nil {
			t.Error(err)
		}
		if err := inputCustodyFlag(parentFile, false); err != nil {
			t.Error(err)
		}
		_ = file.Close()
		_ = parentFile.Close()
	})
	if err := inputCustodyFlag(file, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executionSignedReadinessPreparedExecutor(path); err == nil {
		t.Fatal("prepared executor with mutable parent admitted")
	}
	if err := inputCustodyFlag(parentFile, true); err != nil {
		t.Fatal(err)
	}
	if err := inputCustodyFlag(file, false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executionSignedReadinessPreparedExecutor(path); err == nil {
		t.Fatal("mutable prepared executor with protected parent admitted")
	}
	if err := inputCustodyFlag(file, true); err != nil {
		t.Fatal(err)
	}
	info, parentInfo, err := executionSignedReadinessPreparedExecutor(path)
	if err != nil || !inputCustodyProtected(info) || !inputCustodyProtected(parentInfo) {
		t.Fatal("exact prepared executor refused", err)
	}
	for _, invalid := range []string{"", "relative/t422-execute", filepath.Join(filepath.Dir(parent), "missing-t422-execute")} {
		if _, _, err := executionSignedReadinessPreparedExecutor(invalid); err == nil {
			t.Fatalf("invalid prepared executor admitted: %q", invalid)
		}
	}
}

func TestPublishExecutionSignedReadinessExecutor(t *testing.T) {
	source, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	candidate := filepath.Join(source, "candidate")
	fixture := []byte("prepared executor fixture")
	if err := os.WriteFile(candidate, fixture, 0o700); err != nil {
		t.Fatal(err)
	}
	parent, err := os.MkdirTemp("/private/tmp", "t422-executor-publication-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(parent); err != nil {
		t.Fatal("reserve absent publication parent", err)
	}
	output := filepath.Join(parent, "t422-execute")
	t.Cleanup(func() {
		for _, path := range []string{output, parent} {
			file, err := os.Open(path)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				t.Error("open prepared-executor test custody", err)
				continue
			}
			if err := inputCustodyFlag(file, false); err != nil {
				t.Error("clear prepared-executor test custody", err)
			}
			if err := file.Close(); err != nil {
				t.Error("close prepared-executor test custody", err)
			}
		}
		for _, path := range []string{output, parent} {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Error("remove prepared-executor test custody", err)
			}
		}
	})
	digest, err := publishExecutionSignedReadinessExecutor(t.Context(), candidate, output)
	if err != nil || digest != SHA256(fixture) {
		t.Fatal("publish prepared executor", err)
	}
	if _, _, err := executionSignedReadinessPreparedExecutor(output); err != nil {
		t.Fatal("published executor is not protected", err)
	}
	refused := filepath.Join(source, "refused")
	if err := os.WriteFile(refused, []byte("refused fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := publishExecutionSignedReadinessExecutor(t.Context(), refused, output); err == nil {
		t.Fatal("existing publication parent admitted")
	}
	if info, err := os.Lstat(refused); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatal("refused candidate was mutated", err)
	}
	for _, invalid := range []string{"", "/private/tmp/t422-execute", "/private/tmp/a/b/t422-execute", filepath.Join(parent, "other")} {
		if executionSignedReadinessPreparationPath(invalid) {
			t.Fatalf("invalid preparation path admitted: %q", invalid)
		}
	}
}

func TestExecutionSignedReadinessRefusalDeadline(t *testing.T) {
	now := time.Unix(1, 0)
	for _, test := range []struct {
		name               string
		ctxErr             error
		finished, deadline time.Time
		want               bool
	}{
		{"timely", nil, now, now.Add(time.Second), true},
		{"deadline_equal", nil, now, now, false},
		{"deadline_elapsed", nil, now.Add(time.Second), now, false},
		{"outer_expired", context.DeadlineExceeded, now, now.Add(time.Second), false},
		{"harness_canceled", context.Canceled, now, now.Add(time.Second), false},
		{"unavailable_finish", nil, time.Time{}, now, false},
		{"unavailable_deadline", nil, now, time.Time{}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if executionSignedReadinessTimelyRefusal(test.ctxErr, test.finished, test.deadline) != test.want {
				t.Fatal("deadline/context uncertainty became refusal evidence")
			}
		})
	}
}

func TestExecutionSignedReadinessWrongAuthorization(t *testing.T) {
	value := executionAuthorizationV1{Schema: executionAuthorizationSchema, FreezeSHA256: strings.Repeat("a", 64), SessionBindingSHA256: strings.Repeat("b", 64)}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	frame := executionAuthorizationHandoffV1{AuthorizationJSON: string(raw), ClientArgv: []string{"/private/tmp/executor", executionAuthorizationMode, "--socket", "/private/tmp/auth.sock", "--payload-base64url", base64.RawURLEncoding.EncodeToString(raw)}}
	args, err := executionSignedReadinessWrongAuthorization(frame)
	if err != nil {
		t.Fatal(err)
	}
	changedRaw, err := base64.RawURLEncoding.DecodeString(args[5])
	if err != nil {
		t.Fatal(err)
	}
	changed, err := decodeExecutionAuthorization(changedRaw)
	if err != nil || changed.FreezeSHA256 == value.FreezeSHA256 || changed.SessionBindingSHA256 != value.SessionBindingSHA256 || args[3] != frame.ClientArgv[3] || frame.ClientArgv[5] != base64.RawURLEncoding.EncodeToString(raw) {
		t.Fatal("negative authorization must change only the copied freeze binding")
	}
}

// finishExecutionProcessSession returns the original Wait error on a clean
// join. Its forced path wraps that error with a custody failure sentinel.
func executionSignedReadinessOrdinaryJoinedExit(joined, empty bool, err error) bool {
	exit, ok := err.(*exec.ExitError)
	return joined && empty && ok && exit != nil && exit.ProcessState != nil && exit.Exited() && exit.ExitCode() > 0
}

// executionSessionMembershipCapture is the retained private record of the
// surviving inner-session members at a forced harness cleanup. It stays in the
// test's private root: kernel command names are never source-free evidence.
type executionSessionMembershipCapture struct {
	Schema           string                   `json:"schema"`
	Session          int                      `json:"session"`
	CapturedUnixNano int64                    `json:"captured_unix_nano"`
	CaptureError     string                   `json:"capture_error,omitempty"`
	Members          []executionSessionMember `json:"members"`
}

type executionSessionMember struct {
	PID           int    `json:"pid"`
	ParentPID     int    `json:"parent_pid"`
	RSSBytes      int64  `json:"rss_bytes"`
	StartIdentity string `json:"start_identity"`
	ObservedName  string `json:"observed_name"`
}

// captureExecutionSessionMembership records the surviving inner-session members
// with their kernel identities before the forced sweep, so a retained forced
// cleanup retains kernel command names, not executable-image identities. A capture
// failure is itself retained test-failure evidence; the sweep still proceeds.
func captureExecutionSessionMembership(t *testing.T, root productionRoot, session int) {
	t.Helper()
	capture := executionSessionMembershipCapture{
		Schema:           "t422-inner-session-membership-v1",
		Session:          session,
		CapturedUnixNano: time.Now().UnixNano(),
	}
	members, err := t4013.PrivateProcessSessionMembership(session)
	if err != nil {
		t.Error("inner session membership capture unavailable before forced cleanup", err)
		capture.CaptureError = err.Error()
	}
	for _, member := range members {
		capture.Members = append(capture.Members, executionSessionMember{
			PID: member.PID, ParentPID: member.ParentPID, RSSBytes: member.RSSBytes,
			StartIdentity: member.StartIdentity, ObservedName: member.ObservedName,
		})
		t.Logf("lingering inner session member: pid=%d parent=%d name=%q start=%s rss=%d",
			member.PID, member.ParentPID, member.ObservedName, member.StartIdentity, member.RSSBytes)
	}
	raw, marshalErr := json.Marshal(capture)
	var writeErr error
	if marshalErr == nil {
		writeErr = writeExecutionFailureLeaf(root, "inner-session-forced-cleanup.json", raw, 512<<10)
	}
	if marshalErr != nil || writeErr != nil {
		t.Error("inner session membership record was not retained", marshalErr, writeErr)
	}
}

func TestExecutionSignedReadinessCleanupClassification(t *testing.T) {
	// Two tiny real process states avoid fabricating os.ProcessState internals.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ordinary := exec.CommandContext(ctx, "/bin/sh", "-c", "exit 1").Run()
	signaled := exec.CommandContext(ctx, "/bin/sh", "-c", "kill -TERM $$").Run()
	ordinaryExit, ordinaryOK := ordinary.(*exec.ExitError)
	signalExit, signalOK := signaled.(*exec.ExitError)
	if ctx.Err() != nil || !ordinaryOK || ordinaryExit.ProcessState == nil || !ordinaryExit.Exited() || ordinaryExit.ExitCode() != 1 ||
		!signalOK || signalExit.ProcessState == nil || signalExit.Exited() {
		t.Fatal("native exit fixtures unavailable", ordinary, signaled, ctx.Err())
	}
	for _, test := range []struct {
		name          string
		joined, empty bool
		err           error
		want          bool
	}{
		{"ordinary joined failure", true, true, ordinary, true},
		{"unjoined", false, true, ordinary, false},
		{"session not empty", true, false, ordinary, false},
		{"forced custody failure", true, true, errors.Join(ErrExecutionProductionCustody, ordinary), false},
		{"wrapped ordinary exit", true, true, errors.Join(ordinary), false},
		{"signal", true, true, signaled, false},
		{"unavailable process state", true, true, &exec.ExitError{}, false},
		{"unavailable session", true, false, context.DeadlineExceeded, false},
		{"successful cleanup", true, true, nil, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if executionSignedReadinessOrdinaryJoinedExit(test.joined, test.empty, test.err) != test.want {
				t.Fatal("ordinary exit and unavailable/forced cleanup classification differ")
			}
		})
	}
}
