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

func runLinuxPressureAuthorCommand(ctx context.Context, entered time.Time, args, environment []string) error {
	if ctx == nil || ctx.Err() != nil || runtime.GOARCH != "amd64" || len(args) != 4 || args[2] != "--selection-base64url" {
		return ErrLinuxPressure
	}
	for _, entry := range environment {
		if strings.HasPrefix(entry, executionLivenessEnvironment+"=") {
			return ErrLinuxPressure
		}
	}
	value, err := decodeLinuxPressureAuthor(args[3])
	if err != nil {
		return err
	}
	selected := value.Borrow.Target
	if args[1] == executionLinuxPressureAuthorBrokerMode {
		selected = value.Borrow.Broker
		if os.Getuid() != 0 || os.Geteuid() != 0 || !linuxPressureBorrowBounding() || !validLinuxPressureAuthorSelection(value, entered) {
			return ErrLinuxPressure
		}
	} else if args[1] != executionLinuxPressureAuthorBootstrapMode && args[1] != executionLinuxPressureAuthorTargetMode {
		return ErrLinuxPressure
	}
	selectionValid := validLinuxExecutorPrework(selected, entered)
	if args[1] == executionLinuxPressureAuthorBootstrapMode {
		selectionValid = validLinuxPressureAuthorSelection(value, entered)
	}
	if !selectionValid || !linuxPressureAuthorOriginalPaths(selected) {
		return ErrLinuxPressure
	}
	ctx, cancel := context.WithDeadline(ctx, time.Unix(0, selected.DeadlineUnixNano))
	defer cancel()
	observation := &executionPreworkObservation{}
	if args[1] == executionLinuxPressureAuthorBootstrapMode {
		err = runLinuxPressureAuthorBootstrap(ctx, value, observation)
		// Before independent target work there are no separate helper children.
		// The failed root bootstrap still retires every original control alias.
		_ = observation.close(unix.Close(4))
		if observation.nativeCloseFailed.Load() {
			linuxPressureAuthorRetain()
		}
		settlement, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		wire, wireErr := linuxPressureAuthorWire(settlement, value, 7, 0, observation)
		if wireErr != nil || wire.send(settlement, linuxPressureAuthorFailure) != nil || observation.close(unix.Close(7)) != nil {
			linuxPressureAuthorRetain()
		}
		return err
	}
	for _, fd := range []int{3, 4, 5, 7} {
		unix.CloseOnExec(fd)
	}
	if unix.SetNonblock(4, true) != nil {
		return ErrLinuxPressure
	}
	image, read := os.NewFile(3, "pressure-author-image"), os.NewFile(4, "pressure-author-parent")
	if args[1] == executionLinuxPressureAuthorTargetMode {
		return runLinuxPressureAuthorTarget(ctx, value, image, read, observation)
	}
	namespace := &linuxPressureBorrowNamespace{file: os.NewFile(5, "pressure-author-original-namespace"), device: value.Borrow.NamespaceDev, ino: value.Borrow.NamespaceIno, observation: observation}
	parent, parentErr := adoptLinuxExecutorParentObserved(ctx, selected, image, read, false, observation)
	failureAttempted := false
	var retired, retained bool
	retired = true
	workErr := parentErr
	if parentErr == nil && namespace.check() != nil {
		workErr = ErrLinuxPressure
	}
	if workErr == nil {
		retired, retained, workErr = runLinuxPressureBrokerRecipe(parent.alive, value.Borrow, parent, image, namespace, &failureAttempted, &value)
	}
	if recordErr := writeLinuxExecutorPreworkFailureOnce(&failureAttempted, selected, observation, nil); recordErr != nil {
		workErr = errors.Join(workErr, recordErr)
	}
	var closeErr error
	if parent != nil {
		closeErr = parent.Close()
	} else {
		closeErr = observation.close(read.Close())
		retired = true
	}
	closeErr = errors.Join(closeErr, observation.close(image.Close()), namespace.Close())
	if recordErr := writeLinuxExecutorPreworkFailureOnce(&failureAttempted, selected, observation, nil); recordErr != nil {
		workErr = errors.Join(workErr, recordErr)
	}
	if !retired || parent != nil && !parent.retired || !observation.settle(context.Background()) {
		linuxPressureAuthorRetain()
	}
	failed := workErr != nil || closeErr != nil || ctx.Err() != nil || !time.Now().Before(time.Unix(0, selected.DeadlineUnixNano))
	_, writeErr := os.Stdout.WriteString(linuxPressureAuthorReceipt(selected.Attempt, retained))
	if failed || retained {
		return ErrLinuxPressure
	}
	return writeErr
}
func linuxPressureAuthorRetain() {
	for {
		time.Sleep(time.Second)
	}
}
func linuxPressureAuthorWire(ctx context.Context, value linuxPressureAuthorSelection, fd int, peerUID uint32, observation *executionPreworkObservation) (*linuxPressureBorrowSocket, error) {
	self, err := linuxPressureBorrowRecord(ctx, os.Getpid())
	if err != nil {
		return nil, err
	}
	birth, err := strconv.ParseUint(self.StartIdentity, 10, 64)
	attempt, decodeErr := hex.DecodeString(value.Borrow.Target.Attempt)
	if err != nil || birth == 0 || decodeErr != nil || len(attempt) != 16 {
		return nil, ErrLinuxPressure
	}
	wire := &linuxPressureBorrowSocket{fd: fd, author: true, peer: unix.Ucred{Pid: int32(value.Borrow.Target.ParentPID), Uid: peerUID, Gid: peerUID}, birth: birth, observation: observation}
	wire.peer.Gid = 0
	copy(wire.attempt[:], attempt)
	return wire, nil
}
func runLinuxPressureAuthorTarget(ctx context.Context, value linuxPressureAuthorSelection, image, read *os.File, observation *executionPreworkObservation) error {
	original := os.NewFile(5, "pressure-author-original-leaf")
	control := os.NewFile(7, "pressure-author-control")
	parent, parentErr := adoptLinuxPressureParent(ctx, value.Borrow.Target, image, read, observation)
	wire, wireErr := linuxPressureAuthorWire(ctx, value, 7, 0, observation)
	workErr := errors.Join(parentErr, wireErr)
	var view *linuxPressureAuthorView
	var graph *linuxPressureAuthorWorkspace
	if workErr == nil {
		view, workErr = adoptLinuxPressureAuthorView(ctx, original, value, observation)
		if workErr == nil && (checkLinuxPressureAuthorThreads(linuxPressureBorrowPolicy{value.Borrow.UID, value.Borrow.GID}) != nil || checkLinuxPressureParent(ctx, parent) != nil || wire.send(parent.alive, linuxPressureAuthorTargetHello) != nil || wire.receive(parent.alive, linuxPressureAuthorTargetProceed) != nil) {
			workErr = ErrLinuxPressure
		}
	}
	if workErr == nil {
		graph, workErr = newLinuxPressureAuthorWorkspace(parent.alive, value, view, image, observation)
	}
	if workErr == nil {
		_, workErr = graph.AuthorA(parent.alive)
	}
	// First facts before any potentially blocked original owner Close.
	if workErr != nil && graph != nil {
		observation.retainWorkspace(graph.path)
	}
	failureAttempted := false
	failureSelection := value.Borrow.Target
	if graph != nil {
		failureSelection.PreparationRoot = graph.path
	}
	if err := writeLinuxExecutorPreworkFailureOnce(&failureAttempted, failureSelection, observation, nil); err != nil {
		workErr = errors.Join(workErr, err)
	}
	var closeErr error
	if graph != nil {
		closeErr = graph.Close(context.Background())
		for !graph.closed || !observation.settle(context.Background()) {
			if recordErr := writeLinuxExecutorPreworkFailureOnce(&failureAttempted, failureSelection, observation, nil); recordErr != nil {
				workErr = errors.Join(workErr, recordErr)
			}
			time.Sleep(time.Second)
			if err := graph.Close(context.Background()); err != nil && closeErr == nil {
				closeErr = err
			}
		}
		if workErr == nil && closeErr == nil {
			workErr = graph.retire(ctx, value.ImageBytes)
		}
	}
	if graph != nil && graph.root != nil {
		if graph.retirement != nil && graph.retirement.journal != nil && (graph.retirement.journal.closeFailed || graph.retirement.journal.retainedFDCount != 0) {
			linuxPressureAuthorRetain()
		}
		if !graph.rootClosed {
			if err := observation.close(graph.root.Close()); err != nil {
				closeErr = errors.Join(closeErr, err)
			}
			graph.rootClosed = true
		}
	}
	if parent != nil {
		if err := checkLinuxPressureParent(ctx, parent); err != nil {
			workErr = errors.Join(workErr, err)
		}
		closeErr = errors.Join(closeErr, parent.Close())
	} else {
		closeErr = errors.Join(closeErr, observation.close(read.Close()))
	}
	if view != nil {
		closeErr = errors.Join(closeErr, view.Close())
	}
	closeErr = errors.Join(closeErr, observation.close(original.Close()), observation.close(image.Close()))
	if workErr != nil && graph != nil {
		observation.retainWorkspace(graph.path)
	}
	if recordErr := writeLinuxExecutorPreworkFailureOnce(&failureAttempted, failureSelection, observation, nil); recordErr != nil {
		workErr = errors.Join(workErr, recordErr)
	}
	if !observation.settle(context.Background()) || parent != nil && !parent.retired {
		linuxPressureAuthorRetain()
	}
	phase := byte(linuxPressureAuthorSuccess)
	if workErr != nil || closeErr != nil || ctx.Err() != nil || !time.Now().Before(time.Unix(0, value.Borrow.Target.DeadlineUnixNano)) {
		phase = linuxPressureAuthorFailure
	}
	settlement, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if wire == nil || wire.send(settlement, phase) != nil || observation.close(control.Close()) != nil {
		linuxPressureAuthorRetain()
	}
	if phase == linuxPressureAuthorFailure {
		return ErrLinuxPressure
	}
	return nil
}
