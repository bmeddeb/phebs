//go:build linux

package t421

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

func runLinuxPressureBorrowCommand(ctx context.Context, entered time.Time, args, environment []string) error {
	if runtime.GOARCH != "amd64" || ctx == nil || ctx.Err() != nil || len(args) != 4 || args[2] != "--selection-base64url" ||
		args[1] != executionLinuxPressureBrokerMode && args[1] != executionLinuxPressureChildMode {
		return ErrLinuxPressure
	}
	for _, value := range environment {
		if strings.HasPrefix(value, executionLivenessEnvironment+"=") {
			return ErrLinuxPressure
		}
	}
	value, err := decodeLinuxPressureBorrow(args[3])
	if err != nil {
		return err
	}
	broker := args[1] == executionLinuxPressureBrokerMode
	selected := value.Target
	if broker {
		selected = value.Broker
		if os.Getuid() != 0 || os.Geteuid() != 0 || os.Getgid() != 0 || os.Getegid() != 0 || !linuxPressureBorrowBounding() || !validLinuxPressureBrokerSelection(value, entered) {
			return ErrLinuxPressure
		}
	} else if !validLinuxExecutorPrework(selected, entered) || !validLinuxPressureRequest(value.Pressure) ||
		selected.ExpectedInput != value.Broker.ExpectedInput || selected.ExpectedGoSHA256 != value.Broker.ExpectedGoSHA256 ||
		selected.SourceCommit != value.Broker.SourceCommit || selected.PlanSourceCommit != value.Broker.PlanSourceCommit || selected.IntegratedMainCommit != value.Broker.IntegratedMainCommit {
		return ErrLinuxPressure
	}
	ctx, cancel := context.WithDeadline(ctx, time.Unix(0, selected.DeadlineUnixNano))
	defer cancel()
	observation := &executionPreworkObservation{}
	failureAttempted := false
	for fd := 3; fd <= 5; fd++ {
		unix.CloseOnExec(fd)
	}
	if !broker {
		unix.CloseOnExec(6)
	}
	if unix.SetNonblock(4, true) != nil {
		return ErrLinuxPressure
	}
	image, read := os.NewFile(3, "pressure-executor-image"), os.NewFile(4, "pressure-parent-liveness")
	if !broker {
		return runLinuxPressureChild(ctx, value, image, read, observation)
	}
	namespace := &linuxPressureBorrowNamespace{file: os.NewFile(5, "pressure-broker-namespace"), device: value.NamespaceDev, ino: value.NamespaceIno, observation: observation}
	parent, err := adoptLinuxExecutorParentObserved(ctx, selected, image, read, false, observation)
	if err != nil || namespace.check() != nil {
		if parent != nil {
			_ = parent.Close()
		} else {
			_ = observation.close(read.Close())
		}
		_ = observation.close(image.Close())
		_ = namespace.Close()
		_ = writeLinuxExecutorPreworkFailureOnce(&failureAttempted, selected, observation, nil)
		if !observation.settle(context.Background()) || parent != nil && !parent.retired {
			for {
				time.Sleep(time.Second)
			}
		}
		// No source-owned child/volume was launched. Actual inherited aliases
		// and watcher have retired; this is a known failed unstarted prefix.
		_, _ = os.Stdout.WriteString(linuxPressureBorrowReceipt(selected.Attempt, false))
		return ErrLinuxPressure
	}
	retired, retained, workErr := runLinuxPressureBroker(parent.alive, value, parent, image, namespace, &failureAttempted)
	if recordErr := writeLinuxExecutorPreworkFailureOnce(&failureAttempted, selected, observation, nil); recordErr != nil {
		workErr = errors.Join(workErr, recordErr)
	}
	watchErr := parent.Close()
	imageErr, namespaceErr := observation.close(image.Close()), namespace.Close()
	if recordErr := writeLinuxExecutorPreworkFailureOnce(&failureAttempted, selected, observation, nil); recordErr != nil {
		workErr = errors.Join(workErr, recordErr)
	}
	if !retired || !parent.retired || imageErr != nil || namespaceErr != nil || !observation.settle(context.Background()) {
		for {
			time.Sleep(time.Second)
		}
	}
	failed := workErr != nil || watchErr != nil || ctx.Err() != nil || !time.Now().Before(time.Unix(0, selected.DeadlineUnixNano))
	if failed && !retained {
		// This is an actually retired pre-volume failure, distinguishable from
		// a preserved pressure prefix in the private caller outcome.
		_, _ = os.Stdout.WriteString(linuxPressureBorrowReceipt(selected.Attempt, false))
		return ErrLinuxPressure
	}
	_, err = os.Stdout.WriteString(linuxPressureBorrowReceipt(selected.Attempt, retained))
	if failed || retained {
		return ErrLinuxPressure
	}
	return err
}

func runLinuxPressureChild(ctx context.Context, value linuxPressureBorrowSelection, image, read *os.File, observation *executionPreworkObservation) error {
	leaf := &linuxPressureBorrowLeaf{file: os.NewFile(5, "pressure-target-leaf"), uid: value.UID, gid: value.GID,
		fsid: value.LeafFSID, mount: value.LeafMount, observation: observation}
	control := os.NewFile(6, "pressure-target-control")
	policy := linuxPressureBorrowPolicy{value.UID, value.GID}
	inner := &linuxExecutorInnerPrework{observation: observation}
	failureAttempted := false
	parent, parentErr := adoptLinuxPressureParent(ctx, value.Target, image, read, observation)
	workErr := parentErr
	var wire linuxPressureBorrowSocket
	if self, selfErr := linuxPressureBorrowRecord(ctx, os.Getpid()); selfErr == nil {
		birth, birthErr := strconv.ParseUint(self.StartIdentity, 10, 64)
		attempt, attemptErr := hex.DecodeString(value.Target.Attempt)
		if birthErr == nil && birth != 0 && attemptErr == nil && len(attempt) == 16 {
			wire = linuxPressureBorrowSocket{fd: 6, peer: unix.Ucred{Pid: int32(value.Target.ParentPID), Uid: 0, Gid: 0}, birth: birth, observation: observation}
			copy(wire.attempt[:], attempt)
		}
	}
	if workErr == nil {
		if unix.Fstat(5, &leaf.stat) != nil || uint64(leaf.stat.Dev) != value.LeafDevice || leaf.stat.Ino != value.LeafInode ||
			leaf.check(ctx) != nil || policy.constrainChild() != nil || checkLinuxPressureParent(ctx, parent) != nil {
			workErr = ErrLinuxPressure
		} else {
			namespace, err := pinLinuxPressureBorrowNamespace(observation)
			if err != nil || namespace.device != value.NamespaceDev || namespace.ino != value.NamespaceIno {
				workErr = ErrLinuxPressure
			}
			if namespace != nil {
				workErr = errors.Join(workErr, namespace.Close())
			}
		}
	}
	if workErr == nil {
		birth, birthErr := strconv.ParseUint(parent.self.StartIdentity, 10, 64)
		attempt, decodeErr := hex.DecodeString(value.Target.Attempt)
		wire = linuxPressureBorrowSocket{fd: 6, peer: unix.Ucred{Pid: int32(parent.parent.PID), Uid: 0, Gid: 0}, birth: birth, observation: observation}
		copy(wire.attempt[:], attempt)
		if birthErr != nil || decodeErr != nil || len(attempt) != len(wire.attempt) || birth == 0 ||
			linuxPressureBorrowHandshake(ctx, parent, &wire) != nil {
			workErr = ErrLinuxPressure
		}
	}
	if workErr == nil {
		workErr = prepareLinuxPressureReference(parent.alive, value.Target, image, inner)
	}
	if workErr == nil && (checkLinuxPressureParent(ctx, parent) != nil || policy.checkChild() != nil) {
		workErr = ErrLinuxPressure
	}
	if workErr == nil {
		workErr = leaf.work(parent.alive)
	}
	if recordErr := writeLinuxExecutorPreworkFailureOnce(&failureAttempted, value.Target, observation, nil); recordErr != nil {
		workErr = errors.Join(workErr, recordErr)
	}
	closeErr := inner.Close(context.Background())
	if recordErr := writeLinuxExecutorPreworkFailureOnce(&failureAttempted, value.Target, observation, nil); recordErr != nil {
		workErr = errors.Join(workErr, recordErr)
	}
	for !inner.retired() || !observation.settle(context.Background()) {
		time.Sleep(time.Second)
		if err := inner.Close(context.Background()); err != nil && closeErr == nil {
			closeErr = err
		}
		if recordErr := writeLinuxExecutorPreworkFailureOnce(&failureAttempted, value.Target, observation, nil); recordErr != nil {
			workErr = errors.Join(workErr, recordErr)
		}
	}
	if parent != nil {
		workErr = errors.Join(workErr, checkLinuxPressureParent(ctx, parent))
		closeErr = errors.Join(closeErr, parent.Close())
	} else {
		closeErr = errors.Join(closeErr, observation.close(read.Close()))
	}
	closeErr = errors.Join(closeErr, leaf.Close(), observation.close(image.Close()))
	if recordErr := writeLinuxExecutorPreworkFailureOnce(&failureAttempted, value.Target, observation, nil); recordErr != nil {
		workErr = errors.Join(workErr, recordErr)
	}
	if observation.nativeCloseFailed.Load() || parent != nil && !parent.retired {
		for {
			time.Sleep(time.Second)
		}
	}
	failed := workErr != nil || closeErr != nil || ctx.Err() != nil || !time.Now().Before(time.Unix(0, value.Target.DeadlineUnixNano))
	phase := linuxPressureBorrowSuccess
	if failed {
		phase = linuxPressureBorrowFailure
	}
	settlement, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if wire.peer.Pid <= 0 || wire.send(settlement, phase) != nil || observation.close(control.Close()) != nil {
		_ = writeLinuxExecutorPreworkFailureOnce(&failureAttempted, value.Target, observation, nil)
		for {
			time.Sleep(time.Second)
		}
	}
	if failed {
		return ErrLinuxPressure
	}
	return nil
}

func linuxPressureBorrowHandshake(ctx context.Context, parent *linuxExecutorParent, wire *linuxPressureBorrowSocket) error {
	if ctx == nil || ctx.Err() != nil || parent == nil || parent.alive == nil || wire == nil ||
		wire.send(parent.alive, linuxPressureBorrowHello) != nil || wire.receive(parent.alive, linuxPressureBorrowProceed) != nil || ctx.Err() != nil {
		return ErrLinuxPressure
	}
	return nil
}
