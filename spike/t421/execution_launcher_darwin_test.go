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
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 4 && os.Args[2] == "--selection-base64url" && os.Args[1] == executionInnerMode {
		selected, _ := executionSelection(os.Args[3])
		if selected.CeremonyID == "t422-handoff-test" || selected.CeremonyID == "t422-handoff-extra-test" || selected.CeremonyID == "t422-waitdelay-test" {
			liveness, livenessErr := executionLiveness(os.Environ())
			frame, err := executionLauncherTestHandoff(os.Args[0], liveness.ExecuteImageSHA256, liveness.OuterDeadlineUnixNano)
			if livenessErr != nil || err != nil {
				os.Exit(62)
			}
			written, writeErr := os.Stdout.Write(frame)
			if writeErr != nil || written != len(frame) {
				os.Exit(63)
			}
			if selected.CeremonyID == "t422-waitdelay-test" {
				child := exec.Command(os.Args[0], "t422-session-row-test")
				child.Stderr = os.Stderr // Hold only exec's stderr copy pipe.
				if child.Start() != nil {
					os.Exit(67)
				}
				if os.WriteFile(selected.RepositoryRoot, []byte(strconv.Itoa(child.Process.Pid)), 0o600) != nil {
					os.Exit(68)
				}
				raw := []byte("modeled package; native cleanup precedes authentication")
				frame, err := frameExecutionReturnedPackage(raw, returnedTransportTestBinding(raw))
				if err != nil {
					os.Exit(69)
				}
				if _, err := os.Stdout.Write(frame); err != nil {
					os.Exit(70)
				}
			}
			if selected.CeremonyID == "t422-handoff-extra-test" {
				_, _ = os.Stdout.Write([]byte("x"))
			}
			os.Exit(0)
		}
		if selected.CeremonyID == "t422-package-test" || selected.CeremonyID == "t422-package-wrong-exit-test" {
			// A real signed fixture exercises native outer delivery; it models
			// inner execution and does not establish a full launcher rehearsal.
			liveness, err := executionLiveness(os.Environ())
			raw, readErr := os.ReadFile(selected.RepositoryRoot)
			files, inspectErr := inspectExecutionReturnedPackage(raw, Plan{SealPolicy: frozenSealPolicy()})
			if err != nil || readErr != nil || inspectErr != nil {
				os.Exit(73)
			}
			socket := "/tmp/t422/auth.sock"
			projection, err := projectExecutionAuthorizationHandoff(os.Args[0], socket, liveness.ExecuteImageSHA256, liveness.OuterDeadlineUnixNano)
			if err != nil {
				os.Exit(74)
			}
			handoff, err := buildExecutionAuthorizationHandoff(os.Args[0], socket, liveness.ExecuteImageSHA256, liveness.OuterDeadlineUnixNano,
				liveness.OuterDeadlineUnixNano-1, strings.TrimPrefix(SHA256(files["execution-freeze.json"]), "sha256:"), strings.Repeat("b", 64), projection)
			if err != nil || emitExecutionAuthorizationHandoff(os.Stdout, handoff) != nil {
				os.Exit(75)
			}
			frame, err := frameExecutionReturnedPackage(raw, returnedTransportTestBinding(raw))
			if err != nil {
				os.Exit(76)
			}
			if _, err := os.Stdout.Write(frame); err != nil {
				os.Exit(77)
			}
			if selected.CeremonyID == "t422-package-wrong-exit-test" {
				os.Exit(78)
			}
			os.Exit(0)
		}
		if selected.CeremonyID == "t422-no-handoff-test" {
			os.Exit(43)
		}
		if selected.CeremonyID == "t422-preclaim-diagnostic-test" || selected.CeremonyID == "t422-preclaim-diagnostic-extra-test" ||
			selected.CeremonyID == "t422-preclaim-diagnostic-zero-test" {
			raw, err := canonicalExecutionPreclaimFailure(executionPreclaimFailureV1{
				Schema: executionPreclaimFailureSchema, Stage: executionPreclaimStageProfileRuntime, Cleanup: "clean",
			})
			if err != nil {
				os.Exit(87)
			}
			if _, err := os.Stdout.Write(raw); err != nil {
				os.Exit(88)
			}
			if selected.CeremonyID == "t422-preclaim-diagnostic-extra-test" {
				_, _ = os.Stdout.Write([]byte("x"))
			}
			if selected.CeremonyID == "t422-preclaim-diagnostic-zero-test" {
				os.Exit(0)
			}
			os.Exit(1)
		}
		if selected.CeremonyID == "t422-cancel-test" {
			liveness, err := executionLiveness(os.Environ())
			if err != nil {
				os.Exit(55)
			}
			entered := time.Now()
			parent, innerCtx, adoptErr := adoptExecutionParentLiveness(context.Background(), entered, os.Args[0], liveness, 3)
			if adoptErr != nil {
				os.Exit(55)
			}
			signal.Ignore(syscall.SIGTERM)
			marker, markerErr := os.OpenFile(selected.RepositoryRoot, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if markerErr != nil || marker.Close() != nil {
				_ = parent.Close()
				os.Exit(55)
			}
			select {
			case <-innerCtx.Done():
				// The outer must preserve the abort owner's existing one-minute
				// allowance rather than killing it at the ordinary five-second join.
				time.Sleep(6 * time.Second)
				if closeErr := parent.Close(); !errors.Is(closeErr, ErrExecutionLauncher) {
					os.Exit(56)
				}
				joined, joinedErr := os.OpenFile(selected.RepositoryRoot+".post-eof", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
				if joinedErr != nil || joined.Close() != nil {
					os.Exit(57)
				}
				os.Exit(58)
			case <-time.After(15 * time.Second):
				_ = parent.Close()
				os.Exit(59)
			}
		}
		_ = RunExecutionCommand(context.Background(), os.Args, os.Environ())
		os.Exit(44)
	}
	if len(os.Args) == 4 && os.Args[2] == "--selection-base64url" && os.Args[1] == executionOuterMode {
		ctx := context.Background()
		selected, _ := executionSelection(os.Args[3])
		var cancel context.CancelFunc
		adopted := make(chan bool, 1)
		if selected.CeremonyID == "t422-cancel-test" {
			ctx, cancel = context.WithCancel(ctx)
			defer cancel()
			go func() {
				deadline := time.Now().Add(10 * time.Second)
				for time.Now().Before(deadline) {
					if info, err := os.Lstat(selected.RepositoryRoot); err == nil && info.Mode().IsRegular() {
						adopted <- true
						cancel()
						return
					} else if err != nil && !errors.Is(err, os.ErrNotExist) {
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
				adopted <- false
				cancel()
			}()
		}
		err := RunExecutionCommand(ctx, os.Args, os.Environ())
		if selected.CeremonyID == "t422-package-test" || selected.CeremonyID == "t422-package-wrong-exit-test" {
			if err == nil {
				os.Exit(0)
			}
			os.Exit(79)
		}
		if selected.CeremonyID == "t422-handoff-test" {
			if err == nil {
				os.Exit(0)
			}
			os.Exit(64)
		}
		if selected.CeremonyID == "t422-handoff-extra-test" {
			if errors.Is(err, ErrExecutionLauncher) {
				os.Exit(65)
			}
			os.Exit(66)
		}
		if selected.CeremonyID == "t422-waitdelay-test" {
			data, readErr := os.ReadFile(selected.RepositoryRoot)
			pid, parseErr := strconv.Atoi(string(data))
			if !errors.Is(err, ErrExecutionLauncher) {
				os.Exit(72)
			}
			if readErr != nil {
				os.Exit(80)
			}
			if parseErr != nil {
				os.Exit(81)
			}
			session, sessionErr := unix.Getsid(pid)
			if errors.Is(sessionErr, unix.ESRCH) {
				os.Exit(71)
			}
			if sessionErr != nil {
				os.Exit(82)
			}
			members, membersErr := t4013.PrivateProcessSessionMembers(session)
			if membersErr != nil {
				os.Exit(83)
			}
			if members != 0 {
				os.Exit(84)
			}
			os.Exit(71)
		}
		if selected.CeremonyID == "t422-cancel-test" {
			rows, observeErr := t4013.ObserveProcessTreeRecords(context.Background(), os.Getpid())
			var exit *exec.ExitError
			post, postErr := os.Lstat(selected.RepositoryRoot + ".post-eof")
			if errors.Is(err, ErrExecutionLauncher) && errors.As(err, &exit) && exit.ExitCode() == 58 && <-adopted &&
				postErr == nil && post.Mode().IsRegular() && observeErr == nil && len(rows) == 1 {
				os.Exit(51)
			}
			os.Exit(53)
		}
		if selected.CeremonyID == "t422-preclaim-diagnostic-test" || selected.CeremonyID == "t422-preclaim-diagnostic-extra-test" ||
			selected.CeremonyID == "t422-preclaim-diagnostic-zero-test" {
			reported := ExecutionCommandFailureReported(err)
			if errors.Is(err, ErrExecutionLauncher) &&
				(selected.CeremonyID == "t422-preclaim-diagnostic-test" && reported ||
					selected.CeremonyID != "t422-preclaim-diagnostic-test" && !reported) {
				os.Exit(85)
			}
			os.Exit(86)
		}
		var exit *exec.ExitError
		if errors.Is(err, ErrExecutionLauncher) && errors.As(err, &exit) && exit.ExitCode() == 43 {
			os.Exit(45)
		}
		os.Exit(46)
	}
	if len(os.Args) == 4 && os.Args[1] == "t422-wrong-parent-test" {
		var exit *exec.ExitError
		if errors.As(runExecutionParentTest(os.Args[2], os.Args[3], true), &exit) && exit.ExitCode() == 44 {
			os.Exit(47)
		}
		os.Exit(48)
	}
	if len(os.Args) == 4 && os.Args[1] == "t422-nonisolated-parent-test" {
		var exit *exec.ExitError
		if errors.As(runExecutionParentTest(os.Args[2], os.Args[3], false), &exit) && exit.ExitCode() == 44 {
			os.Exit(49)
		}
		os.Exit(50)
	}
	if len(os.Args) == 2 && os.Args[1] == "t422-session-row-test" {
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
	if len(os.Args) == 2 && os.Args[1] == "t422-exit-one-test" {
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func TestExecutionOuterCancellationCleansPrivateSession(t *testing.T) {
	selection, _ := testExecutionSelection(t)
	selection.CeremonyID = "t422-cancel-test"
	selection.RepositoryRoot = filepath.Join(t.TempDir(), "inner-adopted")
	executable := protectedExecutionTestImage(t)
	command := exec.Command(executable, executionOuterMode, "--selection-base64url", encodeExecutionSelection(t, selection))
	command.Env = []string{"AMBIENT_IGNORED=1"}
	_, runErr := runExecutionLauncherWithOutput(t, command)
	if code := exitCode(t, runErr); code != 51 {
		t.Fatalf("canceled outer exit = %d", code)
	}
	if info, err := os.Lstat(selection.RepositoryRoot); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("inner did not prove FD3 adoption: %v", err)
	}
	if info, err := os.Lstat(selection.RepositoryRoot + ".post-eof"); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("inner did not prove post-EOF watcher join: %v", err)
	}
}

func TestExecutionAbortDeadlinePreservesCleanupAllowance(t *testing.T) {
	started := time.Now()
	if got := executionAbortDeadline(started.Add(time.Hour)); got.Before(started.Add(79*time.Second)) || got.After(time.Now().Add(80*time.Second)) {
		t.Fatal("abort deadline lost the cleanup allowance", got)
	}
	outer := started.Add(time.Second)
	if got := executionAbortDeadline(outer); !got.Equal(outer) {
		t.Fatal("abort deadline renewed the outer lifetime", got)
	}
}

func TestExecutionPreclaimStopPreservesConsumedWait(t *testing.T) {
	command := exec.Command(os.Args[0], "t422-exit-one-test")
	prepareProductionSession(command)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	waited := make(chan error, 1)
	go func() { waited <- command.Wait() }()
	waitErr := <-waited
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	started := time.Now()
	err = stopExecutionPreclaimFailure(command, waited, writer, true, waitErr, started.Add(5*time.Second))
	if !errors.Is(err, ErrExecutionLauncher) || time.Since(started) >= time.Second {
		t.Fatalf("consumed Wait was repeated or refusal lost: %v, elapsed %s", err, time.Since(started))
	}
}

func TestExecutionStartedInnerRequiresExactParentAndSession(t *testing.T) {
	command := exec.Command(os.Args[0], "t422-session-row-test")
	prepareProductionSession(command)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_ = command.Wait()
	})
	if row, err := executionStartedInner(t.Context(), command.Process.Pid, os.Getpid()); err != nil || row.PID != command.Process.Pid || row.StartIdentity == "" {
		t.Fatal("started private-session child was not observed")
	}
	if _, err := executionStartedInner(t.Context(), command.Process.Pid, os.Getppid()); err == nil {
		t.Fatal("started child admitted a wrong parent")
	}
}

func TestExecutionOuterRefusesInnerWithoutHandoff(t *testing.T) {
	selection, _ := testExecutionSelection(t)
	selection.CeremonyID = "t422-no-handoff-test"
	authorityRoot := t.TempDir()
	selection.RepositoryRoot = filepath.Join(authorityRoot, "repository")
	selection.GoRoot = filepath.Join(authorityRoot, "goroot")
	selection.ModuleCache = filepath.Join(authorityRoot, "module-cache")
	selection.GitBinary = filepath.Join(authorityRoot, "git")
	selection.SurrealBinary = filepath.Join(authorityRoot, "surreal")
	selection.SignerControlRoot = filepath.Join(authorityRoot, "signer")
	encoded := encodeExecutionSelection(t, selection)
	executable := protectedExecutionTestImage(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, executionOuterMode, "--selection-base64url", encoded)
	command.Env = []string{"AMBIENT_IGNORED=1"}
	_, err := runExecutionLauncherWithOutput(t, command)
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 45 {
		t.Fatalf("outer did not reach protected inner authority boundary: %v", err)
	}
	for _, path := range []string{selection.RepositoryRoot, selection.GoRoot, selection.ModuleCache, selection.GitBinary, selection.SurrealBinary, selection.SignerControlRoot} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("pending launcher started selected authority at %q: %v", path, err)
		}
	}
}

func TestExecutionOuterRetainsOnlyExactPreclaimDiagnostic(t *testing.T) {
	for _, test := range []struct {
		name       string
		ceremonyID string
		wantRecord bool
	}{
		{name: "exact", ceremonyID: "t422-preclaim-diagnostic-test", wantRecord: true},
		{name: "extra byte", ceremonyID: "t422-preclaim-diagnostic-extra-test"},
		{name: "zero exit", ceremonyID: "t422-preclaim-diagnostic-zero-test"},
	} {
		t.Run(test.name, func(t *testing.T) {
			selection, _ := testExecutionSelection(t)
			selection.CeremonyID = test.ceremonyID
			executable := protectedExecutionTestImage(t)
			command := exec.Command(executable, executionOuterMode, "--selection-base64url", encodeExecutionSelection(t, selection))
			command.Env = []string{"AMBIENT_IGNORED=1"}
			var stderr bytes.Buffer
			command.Stderr = &stderr
			output, err := runExecutionLauncherWithOutput(t, command)
			if code := exitCode(t, err); code != 85 || len(output) != 0 {
				t.Fatalf("preclaim bridge exit/output = %d/%d", code, len(output))
			}
			want, encodeErr := canonicalExecutionPreclaimFailure(executionPreclaimFailureV1{
				Schema: executionPreclaimFailureSchema, Stage: executionPreclaimStageProfileRuntime, Cleanup: "clean",
			})
			if encodeErr != nil {
				t.Fatal(encodeErr)
			}
			if test.wantRecord && !bytes.Equal(stderr.Bytes(), want) {
				t.Fatalf("retained diagnostic = %q, want %q", stderr.Bytes(), want)
			}
			if !test.wantRecord && stderr.Len() != 0 {
				t.Fatalf("invalid diagnostic escaped: %q", stderr.Bytes())
			}
		})
	}
}

func TestExecutionPreclaimFailureIsClosedAndCanonical(t *testing.T) {
	stages := []executionPreclaimStage{
		executionPreclaimStagePreflight,
		executionPreclaimStageOperationalRoot,
		executionPreclaimStagePressureVolume,
		executionPreclaimStageWorkspace,
		executionPreclaimStageSignerCustody,
		executionPreclaimStageGitCustody,
		executionPreclaimStageGoBuildInputs,
		executionPreclaimStageReferenceCandidates,
		executionPreclaimStageReferenceTools,
		executionPreclaimStageSurrealCustody,
		executionPreclaimStagePlanConstruction,
		executionPreclaimStagePlanInputCustody,
		executionPreclaimStageAuthorCustody,
		executionPreclaimStageEpochConfigs,
		executionPreclaimStageEpochOne,
		executionPreclaimStageProfileTools,
		executionPreclaimStageProfileSigner,
		executionPreclaimStageProfileNamespace,
		executionPreclaimStageProfileExecutor,
		executionPreclaimStagePressureBallast,
		executionPreclaimStagePressureSample,
		executionPreclaimStageRehearsalBinding,
		executionPreclaimStageProfileHost,
		executionPreclaimStageProfileEnvironment,
		executionPreclaimStageProfileRuntime,
		executionPreclaimStageProfileIssue,
		executionPreclaimStageParentImage,
		executionPreclaimStageHandoffProjection,
		executionPreclaimStageSignerNamespace,
		executionPreclaimStageCeremonyClaim,
	}
	for _, stage := range stages {
		for _, cleanup := range []string{"clean", "retained_or_unavailable"} {
			value := executionPreclaimFailureV1{Schema: executionPreclaimFailureSchema, Stage: stage, Cleanup: cleanup}
			raw, err := canonicalExecutionPreclaimFailure(value)
			decoded, decodeErr := decodeExecutionPreclaimFailure(raw)
			if err != nil || decodeErr != nil || decoded != value || raw[len(raw)-1] != '\n' || len(raw) > maxExecutionPreclaimFailureBytes {
				t.Fatalf("stage %q cleanup %q did not round trip: %v, %v", stage, cleanup, err, decodeErr)
			}
		}
	}
	for _, raw := range [][]byte{
		[]byte(`{"schema":"t422-source-free-preclaim-failure-v1","stage":"unknown","cleanup":"clean"}` + "\n"),
		[]byte(`{"schema":"t422-source-free-preclaim-failure-v1","stage":"preflight","cleanup":"unknown"}` + "\n"),
		[]byte(`{"stage":"preflight","schema":"t422-source-free-preclaim-failure-v1","cleanup":"clean"}` + "\n"),
		[]byte(`{"schema":"t422-source-free-preclaim-failure-v1","stage":"preflight","cleanup":"clean","extra":true}` + "\n"),
		[]byte(`{"schema":"t422-source-free-preclaim-failure-v1","stage":"preflight","cleanup":"clean"}`),
	} {
		if _, err := decodeExecutionPreclaimFailure(raw); err == nil {
			t.Fatalf("noncanonical preclaim diagnostic admitted: %q", raw)
		}
	}
}

func TestExecutionPreclaimFailureSuppressesClaimedCeremony(t *testing.T) {
	prepared := &executionInnerPreparation{preclaimStage: executionPreclaimStageProfileIssue, closed: true}
	if value, ok := prepared.preclaimFailure(nil); !ok || value.Cleanup != "clean" || value.Stage != executionPreclaimStageProfileIssue {
		t.Fatal("clean preclaim failure was not classified")
	}
	prepared.claim = &executionSignerCeremonyClaimCustody{}
	if _, ok := prepared.preclaimFailure(nil); ok {
		t.Fatal("claimed ceremony emitted a preclaim diagnostic")
	}
}

func TestExecutionOuterForwardsHandoffButRefusesMissingPackage(t *testing.T) {
	selection, _ := testExecutionSelection(t)
	selection.CeremonyID = "t422-handoff-test"
	executable := protectedExecutionTestImage(t)
	command := exec.Command(executable, executionOuterMode, "--selection-base64url", encodeExecutionSelection(t, selection))
	command.Env = []string{"AMBIENT_IGNORED=1"}
	output, err := runExecutionLauncherWithOutput(t, command)
	value, decodeErr := decodeExecutionAuthorizationHandoff(output)
	digest, digestErr := t4013.DigestHostExecutable(t.Context(), executable)
	if exitCode(t, err) != 64 || decodeErr != nil || digestErr != nil || value.clientArgvPath() != executable || value.T422ExecuteImageSHA256 != digest {
		t.Fatalf("outer handoff = %d bytes, %v; decode %v, digest %v", len(output), err, decodeErr, digestErr)
	}
}

func TestExecutionOuterRefusesBytesAfterInheritedHandoff(t *testing.T) {
	selection, _ := testExecutionSelection(t)
	selection.CeremonyID = "t422-handoff-extra-test"
	executable := protectedExecutionTestImage(t)
	command := exec.Command(executable, executionOuterMode, "--selection-base64url", encodeExecutionSelection(t, selection))
	command.Env = []string{"AMBIENT_IGNORED=1"}
	output, err := runExecutionLauncherWithOutput(t, command)
	value, decodeErr := decodeExecutionAuthorizationHandoff(output)
	if code := exitCode(t, err); code != 65 || decodeErr != nil || value.clientArgvPath() != executable {
		t.Fatalf("extra-byte refusal = code %d, %d output bytes, decode %v", code, len(output), decodeErr)
	}
}

func executionLauncherTestHandoff(executePath, image string, outerDeadline int64) ([]byte, error) {
	socketPath := "/tmp/t422/auth.sock"
	projection, err := projectExecutionAuthorizationHandoff(executePath, socketPath, image, outerDeadline)
	if err != nil {
		return nil, err
	}
	handoff, err := buildExecutionAuthorizationHandoff(executePath, socketPath, image, outerDeadline, outerDeadline-1,
		strings.Repeat("a", 64), strings.Repeat("b", 64), projection)
	if err != nil {
		return nil, err
	}
	return handoff.frame, nil
}

func TestExecutionInnerRefusesWrongParentAndDirectInvocation(t *testing.T) {
	_, selection := testExecutionSelection(t)
	executable := protectedExecutionTestImage(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	direct := exec.CommandContext(ctx, executable, executionInnerMode, "--selection-base64url", selection)
	direct.Env = []string{executionLivenessEnvironment + "=invalid"}
	if exitCode(t, direct.Run()) != 44 {
		t.Fatal("direct inner invocation did not refuse")
	}
	wrongParent := protectedExecutionTestImage(t)
	wrongImage := exec.CommandContext(ctx, wrongParent, "t422-wrong-parent-test", executable, selection)
	wrongImage.Env = []string{"IGNORED_TEST_ENV=1"}
	if exitCode(t, wrongImage.Run()) != 47 {
		t.Fatal("inner admitted a parent running a different executable image")
	}
	nonisolated := exec.CommandContext(ctx, executable, "t422-nonisolated-parent-test", executable, selection)
	nonisolated.Env = []string{"IGNORED_TEST_ENV=1"}
	if exitCode(t, nonisolated.Run()) != 49 {
		t.Fatal("inner admitted a process that was not its session and group leader")
	}
}

func runExecutionParentTest(executable, selection string, isolated bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	reader, writer, err := os.Pipe()
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close(); _ = writer.Close() }()
	readRow, readErr := executionPipeRow(reader, unix.O_RDONLY)
	writeRow, writeErr := executionPipeRow(writer, unix.O_WRONLY)
	rows, observeErr := t4013.ObserveProcessTreeRecords(ctx, os.Getpid())
	started := time.Now()
	image, imageErr := holdExecutionImage(ctx, os.Args[0], 0, 0)
	if readErr != nil || writeErr != nil || observeErr != nil || imageErr != nil || len(rows) == 0 {
		return errors.Join(readErr, writeErr, observeErr, ErrExecutionLauncher)
	}
	defer func() { _ = image.Close() }()
	binding := executionParentLivenessV1{
		Schema: executionParentLivenessSchema, OuterPID: os.Getpid(), OuterStartToken: rows[0].StartIdentity,
		OuterStartedUnixNano: started.UnixNano(), OuterDeadlineUnixNano: started.Add(executionMaximumWall).UnixNano(), ReadFD: 3,
		ReadDevice: readRow.device, ReadInode: readRow.inode, ReadMode: readRow.mode,
		WriteDevice: writeRow.device, WriteInode: writeRow.inode, WriteMode: writeRow.mode,
		ExecutePathSHA256: image.pathSHA256, ExecuteDevice: image.device, ExecuteInode: image.inode,
		ExecuteMode: image.mode, ExecuteSize: image.size, ExecuteCTimeUnixNano: image.ctimeUnixNano,
		ExecuteImageSHA256: image.digest,
	}
	raw, err := json.Marshal(binding)
	if err != nil {
		return err
	}
	wrongParent := exec.CommandContext(ctx, executable, executionInnerMode, "--selection-base64url", selection)
	wrongParent.Env = []string{executionLivenessEnvironment + "=" + base64.RawURLEncoding.EncodeToString(raw)}
	wrongParent.Stdin, wrongParent.Stdout, wrongParent.Stderr = nil, io.Discard, io.Discard
	wrongParent.ExtraFiles = []*os.File{reader}
	if isolated {
		prepareProductionSession(wrongParent)
	}
	return wrongParent.Run()
}

func TestExecutionOuterRefusesReplacedExecutablePath(t *testing.T) {
	_, selection := testExecutionSelection(t)
	protected := protectedExecutionTestImage(t)
	err := RunExecutionCommand(t.Context(), []string{protected, executionOuterMode, "--selection-base64url", selection}, nil)
	var exit *exec.ExitError
	if err != ErrExecutionLauncher || errors.As(err, &exit) {
		t.Fatalf("non-current executable path was admitted: %v", err)
	}
}

func protectedExecutionTestImage(t *testing.T) string {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(base, "parent")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := t4013.DigestHostExecutable(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	input := ExecutionInputCopy{Name: "t422-execute", Path: source, SHA256: digest, Executable: true}
	custody, err := ProtectExecutionInputs(t.Context(), parent, []ExecutionInputCopy{input})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { inputCustodyTestCleanup(t, custody, []ExecutionInputCopy{input}) })
	path, err := custody.Check(t.Context(), input.Name)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func exitCode(t *testing.T, err error) int {
	t.Helper()
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatal("child did not exit with a refusal", err)
	}
	return exit.ExitCode()
}

func TestExecutionStartTokenIsCanonicalAndOverflowSafe(t *testing.T) {
	for _, value := range []string{"", "0:0", "+1:0", "01:0", "1:+0", "1:00", "1:1000000", "9223372036854775807:0", "1:-1", "1"} {
		if _, err := parseExecutionStartToken(value); err != ErrExecutionLauncher {
			t.Fatalf("invalid token admitted: %q, %v", value, err)
		}
	}
	if value, err := parseExecutionStartToken("1:0"); err != nil || value != 1_000_000_000 {
		t.Fatalf("valid token = %d, %v", value, err)
	}
}

func TestExecutionLivenessRejectsHeldImageFieldMutations(t *testing.T) {
	baseline := executionParentLivenessV1{
		Schema: executionParentLivenessSchema, OuterPID: 1, OuterStartToken: "1:0",
		OuterStartedUnixNano: 1, OuterDeadlineUnixNano: 2, ReadFD: 3,
		ReadMode: unix.S_IFIFO | 0o600, WriteMode: unix.S_IFIFO | 0o600,
		ExecutePathSHA256: strings.Repeat("a", 64), ExecuteDevice: 1, ExecuteInode: 2,
		ExecuteMode: unix.S_IFREG | 0o500, ExecuteSize: 3, ExecuteCTimeUnixNano: 4,
		ExecuteImageSHA256: "sha256:" + strings.Repeat("b", 64),
	}
	encode := func(value executionParentLivenessV1) string {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	if _, err := decodeExecutionLivenessDarwin(encode(baseline)); err != nil {
		t.Fatal(err)
	}
	mutations := []func(*executionParentLivenessV1){
		func(value *executionParentLivenessV1) { value.ExecutePathSHA256 = "sha256:" + strings.Repeat("a", 64) },
		func(value *executionParentLivenessV1) { value.ExecutePathSHA256 = strings.Repeat("A", 64) },
		func(value *executionParentLivenessV1) { value.ExecuteDevice = -1 },
		func(value *executionParentLivenessV1) { value.ExecuteInode = 0 },
		func(value *executionParentLivenessV1) { value.ExecuteMode = unix.S_IFDIR | 0o500 },
		func(value *executionParentLivenessV1) { value.ExecuteMode = unix.S_IFREG | 0o700 },
		func(value *executionParentLivenessV1) { value.ExecuteSize = 0 },
		func(value *executionParentLivenessV1) { value.ExecuteCTimeUnixNano = 0 },
		func(value *executionParentLivenessV1) { value.ExecuteImageSHA256 = strings.Repeat("b", 64) },
		func(value *executionParentLivenessV1) { value.ExecuteImageSHA256 = "sha256:" + strings.Repeat("B", 64) },
	}
	for index, mutate := range mutations {
		candidate := baseline
		mutate(&candidate)
		if _, err := decodeExecutionLivenessDarwin(encode(candidate)); err != ErrExecutionLauncher {
			t.Fatalf("held-image mutation %d admitted: %v", index, err)
		}
	}
}

func TestExecutionHeldImageMatchesEveryLivenessField(t *testing.T) {
	image := &executionHeldImage{
		pathSHA256: strings.Repeat("a", 64), device: 1, inode: 2, mode: unix.S_IFREG | 0o500,
		size: 3, ctimeUnixNano: 4, digest: "sha256:" + strings.Repeat("b", 64),
	}
	baseline := executionParentLivenessV1{
		ExecutePathSHA256: image.pathSHA256, ExecuteDevice: image.device, ExecuteInode: image.inode,
		ExecuteMode: image.mode, ExecuteSize: image.size, ExecuteCTimeUnixNano: image.ctimeUnixNano,
		ExecuteImageSHA256: image.digest,
	}
	if image.matchesBinding(baseline) {
		t.Fatal("unheld image binding accepted")
	}
	// The identity fields are modeled; this comparator also requires a held
	// descriptor. Opening this test file does not issue native image custody.
	file, err := os.CreateTemp(t.TempDir(), "image")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	image.file = file
	if !image.matchesBinding(baseline) {
		t.Fatal("exact image binding refused")
	}
	mutations := []func(*executionParentLivenessV1){
		func(value *executionParentLivenessV1) { value.ExecutePathSHA256 = strings.Repeat("c", 64) },
		func(value *executionParentLivenessV1) { value.ExecuteDevice++ },
		func(value *executionParentLivenessV1) { value.ExecuteInode++ },
		func(value *executionParentLivenessV1) { value.ExecuteMode++ },
		func(value *executionParentLivenessV1) { value.ExecuteSize++ },
		func(value *executionParentLivenessV1) { value.ExecuteCTimeUnixNano++ },
		func(value *executionParentLivenessV1) { value.ExecuteImageSHA256 = "sha256:" + strings.Repeat("d", 64) },
	}
	for index, mutate := range mutations {
		candidate := baseline
		mutate(&candidate)
		if image.matchesBinding(candidate) {
			t.Fatalf("image-binding mutation %d admitted", index)
		}
	}
	if err := image.Close(); err != nil {
		t.Fatal(err)
	}
	if image.matchesBinding(baseline) {
		t.Fatal("released image binding accepted")
	}
}

func TestExecutionParentLivenessWatcherRejectsByteEOFAndDeadline(t *testing.T) {
	for _, test := range []struct {
		name string
		act  func(*os.File, *os.File)
	}{
		{"byte", func(_ *os.File, writer *os.File) { _, _ = writer.Write([]byte{1}) }},
		{"eof", func(_ *os.File, writer *os.File) { _ = writer.Close() }},
		{"deadline", func(reader *os.File, _ *os.File) { _ = reader.SetReadDeadline(time.Now().Add(20 * time.Millisecond)) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = reader.Close() }()
			defer func() { _ = writer.Close() }()
			ctx, cancel := context.WithCancel(t.Context())
			liveness := &executionParentLiveness{file: reader, cancel: cancel, done: make(chan error, 1)}
			test.act(reader, writer)
			go liveness.watch()
			select {
			case err := <-liveness.done:
				if err == nil {
					t.Fatal("liveness refusal was nil")
				}
			case <-time.After(time.Second):
				t.Fatal("liveness watcher did not finish")
			}
			if ctx.Err() == nil {
				t.Fatal("liveness refusal did not cancel context")
			}
		})
	}
}

func TestExecutionImageCTimeStrictlyPrecedesParent(t *testing.T) {
	for _, pair := range [][2]int64{{0, 2}, {2, 2}, {3, 2}} {
		if validExecutionImageCTime(pair[0], pair[1]) {
			t.Fatalf("ctime %d admitted for parent %d", pair[0], pair[1])
		}
	}
	if !validExecutionImageCTime(1, 2) || !validExecutionImageCTime(1, 0) {
		t.Fatal("valid ctime ordering refused")
	}
}

func TestExecutionOuterWaitDelayStillCleansDescendants(t *testing.T) {
	selection, _ := testExecutionSelection(t)
	selection.CeremonyID = "t422-waitdelay-test"
	selection.RepositoryRoot = filepath.Join(t.TempDir(), "child-pid")
	executable := protectedExecutionTestImage(t)
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, executionOuterMode, "--selection-base64url", encodeExecutionSelection(t, selection))
	command.Env = []string{}
	_, err := runExecutionLauncherWithOutput(t, command)
	if code := exitCode(t, err); code != 71 {
		t.Fatalf("WaitDelay cleanup exit = %d", code)
	}
}

func executionOuterSignedFixture(t *testing.T, selection executionSelectionV1, raw []byte) {
	t.Helper()
	selection.RepositoryRoot = filepath.Join(t.TempDir(), "signed-package")
	if err := os.WriteFile(selection.RepositoryRoot, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	executable := protectedExecutionTestImage(t)
	for _, mode := range []string{"t422-package-test", "t422-package-wrong-exit-test"} {
		selection.CeremonyID = mode
		// Independent full-plan verification also runs under race instrumentation.
		started := time.Now()
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
		defer cancel()
		deadline, _ := ctx.Deadline()
		command := exec.CommandContext(ctx, executable, executionOuterMode, "--selection-base64url", encodeExecutionSelection(t, selection))
		command.Env = []string{}
		output, err := runExecutionLauncherWithOutput(t, command)
		finished, contextErr := time.Now(), ctx.Err()
		cancel()
		t.Logf("signed outer mode=%s elapsed=%s allowance=%s context=%v exit=%v", mode, finished.Sub(started), deadline.Sub(started), contextErr, err)
		if contextErr != nil || !finished.Before(deadline) {
			t.Fatal("native outer signed delivery exceeded its test deadline", contextErr)
		}
		frames, packages := make(chan executionAuthorizationHandoffFrame, 1), make(chan executionReturnedOutput, 1)
		captureExecutionReturnedOutput(bytes.NewReader(output), frames, packages)
		if captured := <-frames; captured.err != nil {
			t.Fatal("missing outer handoff", captured.err)
		}
		returned := <-packages
		if mode == "t422-package-test" {
			if err != nil || returned.err != nil || !bytes.Equal(returned.raw, raw) {
				t.Fatal("native outer signed delivery failed", err, returned.err)
			}
		} else {
			exit, ok := err.(*exec.ExitError)
			if !ok || exit.ProcessState == nil || !exit.Exited() || exit.ExitCode() != 79 || returned.err == nil {
				t.Fatal("outer accepted passed package with failed native exit", err)
			}
		}
	}
}
