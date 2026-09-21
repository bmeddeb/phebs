//go:build darwin

package t421

import (
	"bytes"

	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

func runExecutionOuter(ctx context.Context, started time.Time, executable, selection string, _ []string) (retErr error) {
	startedNano := started.UnixNano()
	maximumWall := executionMaximumWall.Nanoseconds()
	if startedNano <= 0 || startedNano > math.MaxInt64-maximumWall || ctx.Err() != nil {
		return ErrExecutionLauncher
	}
	deadlineNano := startedNano + maximumWall
	deadline := time.Unix(0, deadlineNano)
	if selected, exists := ctx.Deadline(); exists && selected.Before(deadline) {
		deadline = selected
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	selected, err := executionSelection(selection)
	if err != nil || !time.Now().Before(time.Unix(0, deadlineNano)) || !validExecutionLauncherPath(executable) {
		return ErrExecutionLauncher
	}
	image, err := holdExecutionImage(ctx, executable, 0, 0)
	if err != nil {
		return ErrExecutionLauncher
	}
	defer func() { retErr = errors.Join(retErr, image.Close()) }()
	output, err := prepareExecutionAuthorizationOutput(ctx, os.Stdout)
	if err != nil {
		return ErrExecutionLauncher
	}
	defer func() { retErr = errors.Join(retErr, output.file.Close()) }()
	rows, err := t4013.ObserveProcessTreeRecords(ctx, os.Getpid())
	if err != nil || len(rows) == 0 || rows[0].PID != os.Getpid() || rows[0].StartIdentity == "" {
		return ErrExecutionLauncher
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return ErrExecutionLauncher
	}
	defer func() {
		retErr = errors.Join(retErr, closeExecutionFile(reader), closeExecutionFile(writer))
	}()
	readRow, err := executionPipeRow(reader, unix.O_RDONLY)
	if err != nil {
		return ErrExecutionLauncher
	}
	writeRow, err := executionPipeRow(writer, unix.O_WRONLY)
	if err != nil || reader.Fd() == writer.Fd() {
		return ErrExecutionLauncher
	}
	handoffReader, handoffWriter, err := os.Pipe()
	if err != nil {
		return ErrExecutionLauncher
	}
	defer func() {
		retErr = errors.Join(retErr, closeExecutionFile(handoffReader), closeExecutionFile(handoffWriter))
	}()
	if _, err := executionPipeRow(handoffReader, unix.O_RDONLY); err != nil {
		return ErrExecutionLauncher
	}
	if _, err := executionPipeRow(handoffWriter, unix.O_WRONLY); err != nil || handoffReader.Fd() == handoffWriter.Fd() {
		return ErrExecutionLauncher
	}
	binding := executionParentLivenessV1{
		Schema: executionParentLivenessSchema, OuterPID: os.Getpid(), OuterStartToken: rows[0].StartIdentity,
		OuterStartedUnixNano: startedNano, OuterDeadlineUnixNano: deadlineNano, ReadFD: 3,
		ReadDevice: readRow.device, ReadInode: readRow.inode, ReadMode: readRow.mode,
		WriteDevice: writeRow.device, WriteInode: writeRow.inode, WriteMode: writeRow.mode,
		ExecutePathSHA256: image.pathSHA256, ExecuteDevice: image.device, ExecuteInode: image.inode,
		ExecuteMode: image.mode, ExecuteSize: image.size, ExecuteCTimeUnixNano: image.ctimeUnixNano,
		ExecuteImageSHA256: image.digest,
	}
	raw, err := json.Marshal(binding)
	if err != nil || len(raw) == 0 || len(raw) > maxExecutionLivenessBytes {
		return ErrExecutionLauncher
	}
	liveness := base64.RawURLEncoding.EncodeToString(raw)
	input, err := os.Open(os.DevNull)
	if err != nil {
		return ErrExecutionLauncher
	}
	defer func() { retErr = errors.Join(retErr, closeExecutionFile(input)) }()
	command := exec.Command(executable, executionInnerMode, "--selection-base64url", selection)
	command.Env = []string{executionLivenessEnvironment + "=" + liveness}
	command.Stdin, command.Stdout, command.Stderr = input, handoffWriter, io.Discard
	command.ExtraFiles = []*os.File{reader}
	command.WaitDelay = 5 * time.Second
	prepareProductionSession(command)
	if image.Check(ctx) != nil || !time.Now().Before(time.Unix(0, deadlineNano)) || command.Start() != nil {
		return ErrExecutionLauncher
	}
	pid := command.Process.Pid
	waited := make(chan error, 1)
	go func() { waited <- command.Wait() }()
	frames := make(chan executionAuthorizationHandoffFrame, 1)
	packages := make(chan executionReturnedOutput, 1)
	captureDone := make(chan struct{})
	go func() {
		defer close(captureDone)
		captureExecutionReturnedOutput(handoffReader, frames, packages)
	}()
	defer func() {
		_ = closeExecutionFile(handoffReader)
		handoffReader = nil
		<-captureDone
	}()
	if reader.Close() != nil {
		reader = nil
		stoppedWriter := writer
		writer = nil
		return stopExecutionInner(command, waited, stoppedWriter, time.Unix(0, deadlineNano))
	}
	reader = nil
	if handoffWriter.Close() != nil {
		handoffWriter = nil
		stoppedWriter := writer
		writer = nil
		return stopExecutionInner(command, waited, stoppedWriter, time.Unix(0, deadlineNano))
	}
	handoffWriter = nil
	if current, err := executionPipeRow(writer, unix.O_WRONLY); err != nil || current != writeRow {
		stoppedWriter := writer
		writer = nil
		return stopExecutionInner(command, waited, stoppedWriter, time.Unix(0, deadlineNano))
	}
	startedInner, startErr := executionStartedInner(ctx, pid, os.Getpid())
	if image.Check(ctx) != nil || startErr != nil {
		stoppedWriter := writer
		writer = nil
		return stopExecutionInner(command, waited, stoppedWriter, time.Unix(0, deadlineNano))
	}
	var handoffFrame []byte
	var freezeSHA256 string
	select {
	case captured := <-frames:
		if captured.err != nil || len(captured.raw) == 0 {
			stoppedWriter := writer
			writer = nil
			return stopExecutionInner(command, waited, stoppedWriter, time.Unix(0, deadlineNano))
		}
		if captured.preclaim.Schema != "" {
			ownedWriter := writer
			writer = nil
			return finishExecutionPreclaimFailure(ctx, command, waited, packages, ownedWriter, writeRow, image, startedInner, captured, time.Unix(0, deadlineNano))
		}
		if captured.value.clientArgvPath() != executable ||
			captured.value.T422ExecuteImageSHA256 != image.digest || captured.value.OuterDeadlineUnixNano != deadlineNano {
			stoppedWriter := writer
			writer = nil
			return stopExecutionInner(command, waited, stoppedWriter, time.Unix(0, deadlineNano))
		}
		handoffFrame = captured.raw
		freezeSHA256 = captured.value.FreezeSHA256
	case <-ctx.Done():
		stoppedWriter := writer
		writer = nil
		return stopExecutionInner(command, waited, stoppedWriter, time.Unix(0, deadlineNano))
	}
	if forwardExecutionAuthorizationHandoff(ctx, output, handoffFrame) != nil {
		stoppedWriter := writer
		writer = nil
		return stopExecutionInner(command, waited, stoppedWriter, time.Unix(0, deadlineNano))
	}
	var waitErr error
	joined := false
	tailJoined := false
	var tailErr error
	var returned executionReturnedOutput
	waitChannel, tailChannel := (<-chan error)(waited), (<-chan executionReturnedOutput)(packages)
	done := ctx.Done()
	for !joined || !tailJoined {
		select {
		case waitErr = <-waitChannel:
			joined = true
			waitChannel = nil
		case returned = <-tailChannel:
			tailErr = returned.err
			tailJoined = true
			tailChannel = nil
			if tailErr != nil && !joined {
				stoppedWriter := writer
				writer = nil
				return stopExecutionInner(command, waited, stoppedWriter, time.Unix(0, deadlineNano))
			}
		case <-done:
			if !joined {
				stoppedWriter := writer
				writer = nil
				return stopExecutionInner(command, waited, stoppedWriter, time.Unix(0, deadlineNano))
			}
			tailErr = errors.Join(tailErr, ctx.Err())
			_ = closeExecutionFile(handoffReader)
			handoffReader = nil
			done = nil
		}
	}
	// A typed nonzero native exit can accompany an authenticated stopped
	// receipt. Reader/WaitDelay failures cannot masquerade as that outcome.
	var nativeExit *exec.ExitError
	invalidExit := waitErr != nil && !errors.As(waitErr, &nativeExit)
	stopDeadline := executionFinishDeadline(time.Unix(0, deadlineNano))
	joined, empty, finishErr := finishExecutionProcessSession(pid, waited, joined, nil, stopDeadline)
	if invalidExit || !joined || !tailJoined || tailErr != nil || !empty || finishErr != nil || ctx.Err() != nil {
		return errors.Join(ErrExecutionLauncher, tailErr, finishErr, ctx.Err())
	}
	if startedInner.PID != pid || startedInner.ParentPID != os.Getpid() || startedInner.StartIdentity == "" {
		return ErrExecutionLauncher
	}
	if current, err := executionPipeRow(writer, unix.O_WRONLY); err != nil || current != writeRow {
		return ErrExecutionLauncher
	}
	if ctx.Err() != nil || !time.Now().Before(time.Unix(0, deadlineNano)) {
		return ErrExecutionLauncher
	}
	verified, err := verifyExecutionReturnedPackage(ctx, returned.raw, selected, freezeSHA256)
	if err != nil {
		return ErrExecutionLauncher
	}
	passed := verified.receipt.Decision.Outcome == "passed" && verified.receipt.Teardown.Outcome == "clean"
	if passed && waitErr != nil || !passed && waitErr == nil {
		return ErrExecutionLauncher
	}
	if emitExecutionVerifiedReturnedPackage(ctx, output, verified) != nil {
		return ErrExecutionLauncher
	}
	if !passed {
		return ErrExecutionLauncher
	}
	return nil
}

func finishExecutionPreclaimFailure(
	ctx context.Context,
	command *exec.Cmd,
	waited <-chan error,
	packages <-chan executionReturnedOutput,
	writer *os.File,
	writeRow executionPipeIdentity,
	image *executionHeldImage,
	startedInner t4013.NativeProcessRecord,
	captured executionAuthorizationHandoffFrame,
	outerDeadline time.Time,
) error {
	if command == nil || command.Process == nil || writer == nil || image == nil {
		return errors.Join(ErrExecutionLauncher, closeExecutionFile(writer))
	}
	var waitErr error
	var tail executionReturnedOutput
	waitChannel, tailChannel := waited, packages
	for waitChannel != nil || tailChannel != nil {
		select {
		case waitErr = <-waitChannel:
			waitChannel = nil
		case tail = <-tailChannel:
			tailChannel = nil
			if tail.err != nil {
				return stopExecutionPreclaimFailure(command, waited, writer, waitChannel == nil, waitErr, outerDeadline)
			}
		case <-ctx.Done():
			return stopExecutionPreclaimFailure(command, waited, writer, waitChannel == nil, waitErr, outerDeadline)
		}
	}
	var exit *exec.ExitError
	joined, empty, finishErr := finishExecutionProcessSession(command.Process.Pid, waited, true, nil, executionFinishDeadline(outerDeadline))
	current, rowErr := executionPipeRow(writer, unix.O_WRONLY)
	closeErr := closeExecutionFile(writer)
	raw, encodeErr := canonicalExecutionPreclaimFailure(captured.preclaim)
	if !errors.As(waitErr, &exit) || exit.ExitCode() != 1 || !joined || !empty || finishErr != nil ||
		tail.err != nil || len(tail.raw) != 0 || ctx.Err() != nil || rowErr != nil || current != writeRow || closeErr != nil ||
		image.Check(ctx) != nil || startedInner.PID != command.Process.Pid || startedInner.ParentPID != os.Getpid() ||
		startedInner.StartIdentity == "" || encodeErr != nil || !bytes.Equal(raw, captured.raw) {
		return ErrExecutionLauncher
	}
	written, err := os.Stderr.Write(raw)
	if err != nil || written != len(raw) {
		return ErrExecutionLauncher
	}
	return errors.Join(ErrExecutionLauncher, errExecutionPreclaimFailureReported)
}

func stopExecutionPreclaimFailure(command *exec.Cmd, waited <-chan error, writer *os.File, joined bool, waitErr error, outerDeadline time.Time) error {
	if command == nil || command.Process == nil {
		return errors.Join(ErrExecutionLauncher, closeExecutionFile(writer))
	}
	closeErr := closeExecutionFile(writer)
	signalErr := signalProductionStop(command.Process)
	_, _, finishErr := finishExecutionProcessSession(command.Process.Pid, waited, joined, waitErr, executionAbortDeadline(outerDeadline))
	return errors.Join(ErrExecutionLauncher, closeErr, signalErr, finishErr)
}

func stopExecutionInner(command *exec.Cmd, waited <-chan error, writer *os.File, outerDeadline time.Time) error {
	if command == nil || command.Process == nil {
		return ErrExecutionLauncher
	}
	closeErr := closeExecutionFile(writer)
	signalErr := signalProductionStop(command.Process)
	_, _, err := finishExecutionProcessSession(command.Process.Pid, waited, false, nil, executionAbortDeadline(outerDeadline))
	return errors.Join(ErrExecutionLauncher, closeErr, signalErr, err)
}

func executionAbortDeadline(outerDeadline time.Time) time.Time {
	// The inner may spend its full one-minute abort context and five-second
	// command WaitDelay plus six-second forced-session unwind before returning;
	// keep a scheduling margin outside those existing bounds.
	grace := time.Now().Add(time.Minute + 20*time.Second)
	if outerDeadline.Before(grace) {
		return outerDeadline
	}
	return grace
}

func executionFinishDeadline(outerDeadline time.Time) time.Time {
	grace := time.Now().Add(5 * time.Second)
	if outerDeadline.Before(grace) {
		return outerDeadline
	}
	return grace
}

func executionStartedInner(ctx context.Context, pid, parentPID int) (t4013.NativeProcessRecord, error) {
	rows, err := t4013.ObserveProcessTreeRecords(ctx, pid)
	if err != nil || len(rows) == 0 || rows[0].PID != pid || rows[0].ParentPID != parentPID || rows[0].StartIdentity == "" {
		return t4013.NativeProcessRecord{}, ErrExecutionLauncher
	}
	session, sessionErr := unix.Getsid(pid)
	group, groupErr := syscall.Getpgid(pid)
	if sessionErr != nil || groupErr != nil || session != pid || group != pid {
		return t4013.NativeProcessRecord{}, ErrExecutionLauncher
	}
	return rows[0], nil
}

func runExecutionInner(ctx context.Context, entered time.Time, executable, selection string, environment []string) (retErr error) {
	liveness, livenessErr := executionLiveness(environment)
	if livenessErr != nil || entered.UnixNano() <= 0 || os.Getppid() != liveness.OuterPID {
		return ErrExecutionLauncher
	}
	outerDeadline := time.Unix(0, liveness.OuterDeadlineUnixNano)
	deadlineCtx, cancel := context.WithDeadline(ctx, outerDeadline)
	defer cancel()
	if deadlineCtx.Err() != nil || !time.Now().Before(outerDeadline) {
		return ErrExecutionLauncher
	}
	parent, innerCtx, err := adoptExecutionParentLiveness(deadlineCtx, entered, executable, liveness, 3)
	if err != nil {
		return ErrExecutionLauncher
	}
	defer func() { retErr = errors.Join(retErr, parent.Close()) }()
	if innerCtx.Err() != nil {
		return ErrExecutionLauncher
	}
	selected, err := executionSelection(selection)
	if err != nil {
		return ErrExecutionLauncher
	}
	if err := os.Unsetenv(executionLivenessEnvironment); err != nil {
		return ErrExecutionLauncher
	}
	if innerCtx.Err() != nil || !time.Now().Before(outerDeadline) {
		return ErrExecutionLauncher
	}
	prepared, err := prepareExecutionInnerPreparation(innerCtx, selected, parent, outerDeadline)
	if err != nil || prepared == nil {
		if prepared == nil {
			return ErrExecutionLauncher
		}
		cleanupErr := prepared.abortBeforeAdmission(innerCtx)
		failure, ok := prepared.preclaimFailure(cleanupErr)
		retErr = errors.Join(ErrExecutionLauncher, cleanupErr)
		if !ok || innerCtx.Err() != nil {
			return retErr
		}
		output, outputErr := prepareExecutionInnerOutput(innerCtx, parent, os.Stdout)
		if outputErr != nil {
			return errors.Join(retErr, outputErr)
		}
		emitErr := emitExecutionPreclaimFailure(innerCtx, output, failure)
		return errors.Join(retErr, emitErr, output.file.Close())
	}
	abortPreparation := true
	defer func() {
		if abortPreparation && prepared != nil {
			retErr = errors.Join(retErr, prepared.abortBeforeAdmission(innerCtx))
		}
	}()
	output, err := prepareExecutionInnerOutput(innerCtx, parent, os.Stdout)
	if err != nil {
		return ErrExecutionLauncher
	}
	defer func() { retErr = errors.Join(retErr, output.file.Close()) }()
	_, executionErr := prepared.authorizeAndAuthorA(innerCtx, output)
	flow := prepared.flow
	if flow == nil || flow.executionFreezeBinding == nil || flow.executionWholeResources == nil {
		return ErrExecutionLauncher
	}
	abortPreparation = false // Admitted work uses its existing observed phase-15 teardown.
	sequence := &executionEpochSequenceResult{}
	if executionErr == nil {
		sequence, executionErr = runExecutionEpochSequence(innerCtx, flow, prepared.volume)
	}
	if sequence == nil {
		sequence = &executionEpochSequenceResult{}
	}
	phaseEvents, eventErr := flow.executionPhaseEventEvidence()
	if eventErr != nil {
		executionErr = errors.Join(executionErr, sequence.observeStoppedTeardown(innerCtx, flow, prepared.volume))
	} else if len(phaseEvents) == 15 && phaseEvents[14].StartEventOrdinal == 0 {
		executionErr = errors.Join(executionErr, sequence.stopAndObserve(innerCtx, flow, prepared.volume))
	}
	resources, resourceErr := flow.executionWholeResources.close()
	sequence.resources = resources
	executionErr = errors.Join(executionErr, eventErr, resourceErr)
	binding := *flow.executionFreezeBinding
	diagnosticStage := "receipt_composition"
	defer func() {
		if retErr != nil && resources.Joined {
			flow.mu.Lock()
			recorder := flow.executionPhaseEvents
			flow.mu.Unlock()
			var processRefusal string
			if meter := flow.executionWholeResources.process; meter != nil && meter.gauge != nil {
				processRefusal = meter.gauge.privateRefusal()
			}
			resultErr := err
			if resultErr == nil {
				resultErr = retErr
			}
			retErr = errors.Join(retErr, retainExecutionFailureDiagnostic(prepared.operational, diagnosticStage, recorder, flow, executionErr, resultErr, processRefusal, sequence.current, sequence.runs[3]))
		}
	}()
	receipt, err := composeExecutionSequenceReceipt(flow.plan, binding, sequence, flow, resources)
	if err != nil {
		return errors.Join(ErrExecutionLauncher, err)
	}
	passed := receipt.Decision.Outcome == "passed" && receipt.Teardown.Outcome == "clean"
	if passed && executionErr != nil {
		return ErrExecutionLauncher
	}
	diagnosticStage = "package_construction"
	raw, packageBinding, err := buildExecutionReturnedPackage(innerCtx, flow.plan, receipt, binding, prepared.seal)
	if err != nil {
		return ErrExecutionLauncher
	}
	// Keep the signer, namespace and live admission graph until signing is
	// complete. Only successful volume teardown permits their existing release.
	diagnosticStage = "owner_close"
	if passed {
		if err = prepared.Close(); err != nil {
			return ErrExecutionLauncher
		}
	}
	diagnosticStage = "package_emit"
	if err = emitExecutionReturnedPackage(innerCtx, output, raw, packageBinding); err != nil {
		return ErrExecutionLauncher
	}
	if !passed {
		diagnosticStage = "execution_stopped"
		return ErrExecutionLauncher
	}
	return nil
}

func validExecutionLauncherPath(path string) bool {
	return len(path) > 0 && len(path) <= 1_023 && filepath.IsAbs(path) && filepath.Clean(path) == path && !strings.ContainsRune(path, 0)
}

type executionPipeIdentity struct {
	device int64
	inode  uint64
	mode   uint32
}

func executionPipeRow(file *os.File, access int) (executionPipeIdentity, error) {
	if file == nil {
		return executionPipeIdentity{}, ErrExecutionLauncher
	}
	fd := int(file.Fd())
	syscall.CloseOnExec(fd)
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	descriptorFlags, descriptorErr := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
	var stat unix.Stat_t
	statErr := unix.Fstat(fd, &stat)
	if err != nil || descriptorErr != nil || statErr != nil || flags&unix.O_ACCMODE != access || descriptorFlags&unix.FD_CLOEXEC == 0 ||
		stat.Uid != uint32(os.Getuid()) || stat.Mode&unix.S_IFMT != unix.S_IFIFO {
		return executionPipeIdentity{}, ErrExecutionLauncher
	}
	return executionPipeIdentity{device: int64(stat.Dev), inode: stat.Ino, mode: uint32(stat.Mode)}, nil
}

func executionLiveness(environment []string) (executionParentLivenessV1, error) {
	if len(environment) != 1 {
		return executionParentLivenessV1{}, ErrExecutionLauncher
	}
	var encoded string
	found := 0
	for _, entry := range environment {
		name, value, ok := strings.Cut(entry, "=")
		if ok && name == executionLivenessEnvironment {
			found++
			encoded = value
		}
	}
	if found != 1 {
		return executionParentLivenessV1{}, ErrExecutionLauncher
	}
	return decodeExecutionLivenessDarwin(encoded)
}

func decodeExecutionLivenessDarwin(encoded string) (executionParentLivenessV1, error) {
	value, err := decodeExecutionLiveness(encoded)
	if err != nil || value.Schema != executionParentLivenessSchema || value.OuterPID <= 0 || value.OuterStartToken == "" ||
		value.OuterStartedUnixNano <= 0 || value.OuterDeadlineUnixNano <= 0 || value.ReadFD != 3 ||
		value.ReadMode&unix.S_IFMT != unix.S_IFIFO || value.WriteMode&unix.S_IFMT != unix.S_IFIFO ||
		!validExecutionHexSHA256(value.ExecutePathSHA256) || value.ExecuteDevice < 0 || value.ExecuteInode == 0 ||
		value.ExecuteMode&unix.S_IFMT != unix.S_IFREG || value.ExecuteMode&0o7777 != 0o500 || value.ExecuteSize <= 0 ||
		value.ExecuteCTimeUnixNano <= 0 || !validExecutionSHA256(value.ExecuteImageSHA256) {
		return executionParentLivenessV1{}, ErrExecutionLauncher
	}
	return value, nil
}

func adoptExecutionParentLiveness(ctx context.Context, entered time.Time, executable string, binding executionParentLivenessV1, fd int) (*executionParentLiveness, context.Context, error) {
	if ctx == nil || ctx.Err() != nil || fd != binding.ReadFD || os.Getppid() != binding.OuterPID ||
		entered.UnixNano() <= 0 || entered.UnixNano() >= binding.OuterDeadlineUnixNano {
		return nil, nil, ErrExecutionLauncher
	}
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err != nil || flags&unix.O_ACCMODE != unix.O_RDONLY {
		return nil, nil, ErrExecutionLauncher
	}
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_SETFL, flags|unix.O_NONBLOCK); err != nil {
		return nil, nil, ErrExecutionLauncher
	}
	syscall.CloseOnExec(fd)
	flags, flagsErr := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	descriptorFlags, descriptorErr := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
	var stat unix.Stat_t
	statErr := unix.Fstat(fd, &stat)
	if flagsErr != nil || descriptorErr != nil || statErr != nil || flags&unix.O_ACCMODE != unix.O_RDONLY || flags&unix.O_NONBLOCK == 0 ||
		descriptorFlags&unix.FD_CLOEXEC == 0 || !binding.matchesRead(stat) {
		_ = unix.Close(fd)
		return nil, nil, ErrExecutionLauncher
	}
	file := os.NewFile(uintptr(fd), "t422-parent-liveness")
	if file == nil {
		_ = unix.Close(fd)
		return nil, nil, ErrExecutionLauncher
	}
	if err := unix.Fstat(fd, &stat); err != nil || !binding.matchesRead(stat) {
		_ = file.Close()
		return nil, nil, ErrExecutionLauncher
	}
	rows, err := t4013.ObserveProcessTreeRecords(ctx, binding.OuterPID)
	parentStarted, parseErr := parseExecutionStartToken(binding.OuterStartToken)
	maximumWall := executionMaximumWall.Nanoseconds()
	selfPID := os.Getpid()
	if err != nil || parseErr != nil || len(rows) != 2 || rows[0].PID != binding.OuterPID || rows[0].StartIdentity != binding.OuterStartToken ||
		rows[1].PID != selfPID || rows[1].ParentPID != binding.OuterPID || rows[1].StartIdentity == "" ||
		parentStarted > binding.OuterStartedUnixNano || binding.OuterStartedUnixNano > entered.UnixNano() ||
		binding.OuterStartedUnixNano > math.MaxInt64-maximumWall || binding.OuterDeadlineUnixNano != binding.OuterStartedUnixNano+maximumWall ||
		!time.Now().Before(time.Unix(0, binding.OuterDeadlineUnixNano)) {
		_ = file.Close()
		return nil, nil, ErrExecutionLauncher
	}
	if !executionSessionIsolated(binding.OuterPID) {
		_ = file.Close()
		return nil, nil, ErrExecutionLauncher
	}
	image, err := holdExecutionImage(ctx, executable, binding.OuterPID, parentStarted)
	if err != nil || !image.matchesBinding(binding) {
		if image != nil {
			_ = image.Close()
		}
		_ = file.Close()
		return nil, nil, ErrExecutionLauncher
	}
	watchCtx, cancel := context.WithCancel(ctx)
	liveness := &executionParentLiveness{file: file, image: image, outer: rows[0], inner: rows[1], alive: watchCtx, cancel: cancel, done: make(chan error, 1)}
	if file.SetReadDeadline(time.Unix(0, binding.OuterDeadlineUnixNano)) != nil {
		_ = file.Close()
		_ = image.Close()
		cancel()
		return nil, nil, ErrExecutionLauncher
	}
	go liveness.watch()
	return liveness, watchCtx, nil
}

func (value executionParentLivenessV1) matchesRead(stat unix.Stat_t) bool {
	return stat.Uid == uint32(os.Getuid()) && int64(stat.Dev) == value.ReadDevice && stat.Ino == value.ReadInode && uint32(stat.Mode) == value.ReadMode &&
		stat.Mode&unix.S_IFMT == unix.S_IFIFO
}

type executionParentLiveness struct {
	file   *os.File
	image  *executionHeldImage
	outer  t4013.NativeProcessRecord
	inner  t4013.NativeProcessRecord
	alive  context.Context
	cancel context.CancelFunc
	done   chan error
	once   sync.Once
}

func (value *executionParentLiveness) watch() {
	var one [1]byte
	count, err := value.file.Read(one[:])
	if count != 0 || err == nil {
		err = ErrExecutionLauncher
	} else if errors.Is(err, io.EOF) {
		err = ErrExecutionLauncher
	}
	value.cancel()
	value.done <- err
}

func (value *executionParentLiveness) Close() error {
	if value == nil || value.file == nil || value.cancel == nil || value.done == nil {
		return ErrExecutionLauncher
	}
	var closeErr error
	value.once.Do(func() {
		closeErr = value.file.Close()
		value.cancel()
	})
	watchErr := <-value.done
	if errors.Is(watchErr, os.ErrClosed) {
		watchErr = nil
	}
	return errors.Join(closeErr, watchErr, value.image.Close())
}

type executionHeldImage struct {
	mu            sync.Mutex
	file          *os.File
	info          os.FileInfo
	path          string
	pathSHA256    string
	device        int64
	inode         uint64
	mode          uint32
	size          int64
	ctimeUnixNano int64
	digest        string
}

func holdExecutionImage(ctx context.Context, expected string, parentPID int, parentStarted int64) (*executionHeldImage, error) {
	canonical, err := filepath.EvalSymlinks(expected)
	pids := []int{os.Getpid()}
	if parentPID > 0 {
		pids = []int{parentPID, os.Getpid()}
	}
	observedPaths, observedErr := t4013.ObserveProcessExecutablePaths(ctx, pids)
	actual, actualErr := os.Executable()
	actualPath, actualPathErr := filepath.EvalSymlinks(actual)
	if err != nil || observedErr != nil || actualErr != nil || actualPathErr != nil || canonical != expected ||
		len(observedPaths) != len(pids) || observedPaths[len(observedPaths)-1] != canonical || actualPath != canonical {
		return nil, ErrExecutionLauncher
	}
	if parentPID > 0 {
		if observedPaths[0] != canonical {
			return nil, ErrExecutionLauncher
		}
	}
	file, err := t4013.OpenHostImage(canonical)
	if err != nil {
		return nil, ErrExecutionLauncher
	}
	info, infoErr := file.Stat()
	pathInfo, pathErr := os.Lstat(canonical)
	parentInfo, parentErr := os.Lstat(filepath.Dir(canonical))
	stat, statOK := info.Sys().(*syscall.Stat_t)
	if infoErr != nil || pathErr != nil || parentErr != nil || !statOK || stat == nil ||
		!inputCustodyProtected(info) || info.Mode().Perm() != 0o500 || !inputCustodySame(info, pathInfo) ||
		!inputCustodyProtected(parentInfo) || parentInfo.Mode().Perm() != 0o700 {
		_ = file.Close()
		return nil, ErrExecutionLauncher
	}
	changed, changedOK := executionTimespecNano(stat.Ctimespec)
	if !changedOK || !validExecutionImageCTime(changed, parentStarted) {
		_ = file.Close()
		return nil, ErrExecutionLauncher
	}
	digest, err := t4013.DigestHostExecutable(ctx, canonical)
	image := &executionHeldImage{file: file, info: info, path: canonical,
		pathSHA256: strings.TrimPrefix(SHA256([]byte(canonical)), "sha256:"), device: int64(stat.Dev), inode: stat.Ino,
		mode: uint32(stat.Mode), size: stat.Size, ctimeUnixNano: changed, digest: digest}
	if err != nil || !validExecutionSHA256(digest) || image.Check(ctx) != nil {
		_ = file.Close()
		return nil, ErrExecutionLauncher
	}
	return image, nil
}

func (image *executionHeldImage) Check(ctx context.Context) error {
	if image == nil {
		return ErrExecutionLauncher
	}
	image.mu.Lock()
	defer image.mu.Unlock()
	return image.checkLocked(ctx)
}

func (image *executionHeldImage) checkLocked(ctx context.Context) error {
	if image == nil || image.file == nil || image.info == nil || ctx == nil || ctx.Err() != nil {
		return ErrExecutionLauncher
	}
	pathInfo, pathErr := os.Lstat(image.path)
	heldInfo, heldErr := image.file.Stat()
	if pathErr != nil || heldErr != nil || !validExecutionHexSHA256(image.pathSHA256) || !validExecutionSHA256(image.digest) ||
		!inputCustodyProtected(pathInfo) || !inputCustodySame(image.info, pathInfo) || !inputCustodySame(pathInfo, heldInfo) {
		return ErrExecutionLauncher
	}
	return nil
}

func (image *executionHeldImage) observe(ctx context.Context) (string, string, error) {
	if image == nil {
		return "", "", ErrExecutionLauncher
	}
	image.mu.Lock()
	defer image.mu.Unlock()
	if image.checkLocked(ctx) != nil {
		return "", "", ErrExecutionLauncher
	}
	return image.path, image.digest, nil
}

func (image *executionHeldImage) matchesBinding(value executionParentLivenessV1) bool {
	if image == nil {
		return false
	}
	image.mu.Lock()
	defer image.mu.Unlock()
	return image.file != nil && image.pathSHA256 == value.ExecutePathSHA256 && image.device == value.ExecuteDevice &&
		image.inode == value.ExecuteInode && image.mode == value.ExecuteMode && image.size == value.ExecuteSize &&
		image.ctimeUnixNano == value.ExecuteCTimeUnixNano && image.digest == value.ExecuteImageSHA256
}

func (image *executionHeldImage) Close() error {
	if image == nil {
		return ErrExecutionLauncher
	}
	image.mu.Lock()
	defer image.mu.Unlock()
	if image.file == nil {
		return ErrExecutionLauncher
	}
	err := image.file.Close()
	image.file = nil
	return err
}

func executionSessionIsolated(parent int) bool {
	pid := os.Getpid()
	session, sessionErr := unix.Getsid(pid)
	group, groupErr := syscall.Getpgid(pid)
	parentSession, parentSessionErr := unix.Getsid(parent)
	parentGroup, parentGroupErr := syscall.Getpgid(parent)
	return sessionErr == nil && groupErr == nil && parentSessionErr == nil && parentGroupErr == nil &&
		session == pid && group == pid && parentSession != session && parentGroup != group
}

func executionTimespecNano(value syscall.Timespec) (int64, bool) {
	if value.Sec < 0 || value.Nsec < 0 || value.Nsec >= 1_000_000_000 || value.Sec > (math.MaxInt64-value.Nsec)/1_000_000_000 {
		return 0, false
	}
	return value.Sec*1_000_000_000 + value.Nsec, true
}

func validExecutionImageCTime(changed, parentStarted int64) bool {
	return changed > 0 && (parentStarted <= 0 || changed < parentStarted)
}

func parseExecutionStartToken(value string) (int64, error) {
	secondsRaw, microsRaw, ok := strings.Cut(value, ":")
	if !ok || !executionDecimal(secondsRaw) || !executionDecimal(microsRaw) || len(secondsRaw) > 19 || len(microsRaw) > 6 ||
		len(secondsRaw) > 1 && secondsRaw[0] == '0' || len(microsRaw) > 1 && microsRaw[0] == '0' {
		return 0, ErrExecutionLauncher
	}
	seconds, err := strconv.ParseInt(secondsRaw, 10, 64)
	micros, microsErr := strconv.ParseInt(microsRaw, 10, 64)
	if err != nil || microsErr != nil || seconds <= 0 || micros < 0 || micros >= 1_000_000 || seconds > (math.MaxInt64-micros*1_000)/1_000_000_000 {
		return 0, ErrExecutionLauncher
	}
	return seconds*1_000_000_000 + micros*1_000, nil
}

func executionDecimal(value string) bool {
	if value == "" {
		return false
	}
	for _, digit := range []byte(value) {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}

func closeExecutionFile(file *os.File) error {
	if file == nil {
		return nil
	}
	return file.Close()
}

type executionParentLivenessV1 struct {
	Schema                string `json:"schema"`
	OuterPID              int    `json:"outer_pid"`
	OuterStartToken       string `json:"outer_start_token"`
	OuterStartedUnixNano  int64  `json:"outer_started_unix_nano"`
	OuterDeadlineUnixNano int64  `json:"outer_deadline_unix_nano"`
	ReadFD                int    `json:"read_fd"`
	ReadDevice            int64  `json:"read_st_dev"`
	ReadInode             uint64 `json:"read_st_ino"`
	ReadMode              uint32 `json:"read_st_mode"`
	WriteDevice           int64  `json:"write_st_dev"`
	WriteInode            uint64 `json:"write_st_ino"`
	WriteMode             uint32 `json:"write_st_mode"`
	ExecutePathSHA256     string `json:"t422_execute_canonical_path_sha256"`
	ExecuteDevice         int64  `json:"t422_execute_st_dev"`
	ExecuteInode          uint64 `json:"t422_execute_st_ino"`
	ExecuteMode           uint32 `json:"t422_execute_st_mode"`
	ExecuteSize           int64  `json:"t422_execute_size"`
	ExecuteCTimeUnixNano  int64  `json:"t422_execute_ctime_unix_nano"`
	ExecuteImageSHA256    string `json:"t422_execute_image_sha256"`
}

func decodeExecutionLiveness(encoded string) (executionParentLivenessV1, error) {
	if len(encoded) == 0 || len(encoded) > base64.RawURLEncoding.EncodedLen(maxExecutionLivenessBytes) {
		return executionParentLivenessV1{}, ErrExecutionLauncher
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || len(raw) == 0 || len(raw) > maxExecutionLivenessBytes {
		return executionParentLivenessV1{}, ErrExecutionLauncher
	}
	var value executionParentLivenessV1
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil || !executionJSONEOF(decoder) {
		return executionParentLivenessV1{}, ErrExecutionLauncher
	}
	canonical, err := json.Marshal(value)
	if err != nil || !bytes.Equal(raw, canonical) || base64.RawURLEncoding.EncodeToString(canonical) != encoded {
		return executionParentLivenessV1{}, ErrExecutionLauncher
	}
	return value, nil
}
