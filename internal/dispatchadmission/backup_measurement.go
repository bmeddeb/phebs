package dispatchadmission

import (
	"context"
	"errors"
	"io"
	"math"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// BindRetiredBackupMeasurement binds the application's actual owned-engine
// guard once, before workers start. It grants no SDK, dispatch or phase work.
// Unselected lifetimes keep their ordinary path without retaining a callback.
func BindRetiredBackupMeasurement(guard func(context.Context, func(context.Context) error) error) error {
	lifetime := productionRuntime.Load()
	if lifetime == nil {
		return nil
	}
	return lifetime.bindRetiredBackupMeasurement(guard)
}

func (lifetime *ProductionLifetime) bindRetiredBackupMeasurement(guard func(context.Context, func(context.Context) error) error) error {
	lifetime.backupMeasurementMu.Lock()
	defer lifetime.backupMeasurementMu.Unlock()
	if lifetime.backupMeasurementMaximum == 0 {
		return nil
	}
	client := lifetime.client
	valid := client != nil && guard != nil && lifetime.backupMeasurementGuard == nil && lifetime.program == ProgramPhebs &&
		lifetime.semanticMode == ProductionSemanticV3 && lifetime.producerID == 5 && lifetime.inputSHA256 != ([32]byte{})
	if valid {
		client.mu.Lock()
		valid = client.backupEndpointCarry && client.phase == 8 && !client.backupRetiring && !client.closed && client.err == nil && client.ctx.Err() == nil
		client.mu.Unlock()
		lifetime.storeMu.Lock()
		valid = valid && lifetime.storeTaken && !lifetime.storeClosed && !lifetime.storeRetired && lifetime.storeOwner != nil
		lifetime.storeMu.Unlock()
		lifetime.workspaceMu.Lock()
		valid = valid && lifetime.workspace != nil && lifetime.workspace.file != nil
		lifetime.workspaceMu.Unlock()
	}
	if !valid {
		if client != nil {
			return client.fail(ErrProductionBootstrap)
		}
		return ErrProductionBootstrap
	}
	lifetime.backupMeasurementGuard = guard
	return nil
}

// WithRetiredBackupMeasurement keeps the existing serialized PC endpoint for
// HOLD, one synchronous parent-owned measurement, and RELEASE. The server ACKs
// HOLD only while its actual engine is stopped, and RELEASE only after resume
// and unlock. No ordinary control/SDK admission is resumed. The authenticated
// original phase deadline is pinned across holds, never renewed by this call.
func (control *PhaseControl) WithRetiredBackupMeasurement(ctx context.Context, measure func(context.Context) error) (retErr error) {
	if control == nil {
		return ErrConfig
	}
	acquired := false
	defer func() {
		if recover() != nil {
			retErr = ErrPanic
		}
		if retErr != nil {
			retErr = control.fail(retErr) // Closing the socket releases any remote hold.
		}
		if acquired {
			<-control.gate
		}
	}()
	if ctx == nil || measure == nil {
		return ErrCanceled
	}
	deadline, bounded := ctx.Deadline()
	if !bounded || ctx.Err() != nil || deadline.UnixNano() <= 0 {
		return ErrCanceled
	}
	select {
	case control.gate <- struct{}{}:
		acquired = true
	case <-ctx.Done():
		return ErrCanceled
	case <-control.ctx.Done():
		return ErrCanceled
	}
	control.mu.Lock()
	valid := control.err == nil && !control.closed && control.ctx.Err() == nil && ctx.Err() == nil &&
		control.config.BackupEndpointCarry && control.config.BackupMeasurementMaximum != 0 &&
		control.state == phasePause && control.index == len(control.config.Phases)-1 && control.config.Phases[control.index] == 11 &&
		control.backupMeasurements < control.config.BackupMeasurementMaximum && control.sequence <= math.MaxUint64-2 &&
		control.config.MaximumWireBytes-control.wireBytes >= 4*FrameBytes &&
		(control.backupMeasurementDeadline == 0 || control.backupMeasurementDeadline == deadline.UnixNano())
	if !valid {
		control.mu.Unlock()
		return ErrProtocol
	}
	control.backupMeasurements++
	control.backupMeasurementDeadline = deadline.UnixNano()
	control.wireBytes += 4 * FrameBytes
	control.sequence += 2
	hold := phaseControlFrame{op: phaseBackupMeasurementHold, phase: 12, sequence: control.sequence - 1, binding: control.binding, deadlineUnixNano: deadline.UnixNano()}
	control.mu.Unlock()
	if err := control.conn.SetDeadline(deadline); err != nil {
		return ErrTransport
	}
	join := archiveMeasurementCancellation(ctx, control.conn)
	defer join()
	if err := backupMeasurementExchange(control.conn, hold); err != nil {
		return err
	}
	measurementErr := measure(ctx)
	release := hold
	release.op, release.sequence = phaseBackupMeasurementRelease, hold.sequence+1
	if err := backupMeasurementExchange(control.conn, release); err != nil {
		return errors.Join(measurementErr, err)
	}
	if ctx.Err() != nil || control.ctx.Err() != nil {
		return errors.Join(measurementErr, ErrCanceled)
	}
	return measurementErr
}

func backupMeasurementExchange(conn *net.UnixConn, frame phaseControlFrame) error {
	raw := frame.encode()
	if n, err := conn.Write(raw[:]); err != nil || n != len(raw) {
		return ErrTransport
	}
	var ack [FrameBytes]byte
	if _, err := io.ReadFull(conn, ack[:]); err != nil {
		return ErrTransport
	}
	if ack != raw {
		return ErrProtocol
	}
	return nil
}

func (client *Client) retiredBackupGuard() (func(context.Context, func(context.Context) error) error, error) {
	client.mu.Lock()
	lifetime := client.storeLifetime
	valid := lifetime != nil && client.backupEndpointCarry && client.backupRetiring && client.phase == 11 && client.paused &&
		client.checkpoint && client.ownersRequired && !client.ownerRequestsOpen && !client.closed && client.err == nil && client.ctx.Err() == nil
	if owners := client.owners; valid && owners != nil {
		owners.mu.Lock()
		valid = owners.paused && owners.pausedReady && owners.requestsFenced && owners.requestsReady && owners.active == 0 && owners.requests == 0 && owners.err == nil && owners.ctx.Err() == nil
		owners.mu.Unlock()
	} else {
		valid = false
	}
	client.mu.Unlock()
	if !valid {
		return nil, ErrProtocol
	}
	lifetime.storeMu.Lock()
	valid = lifetime.program == ProgramPhebs && lifetime.semanticMode == ProductionSemanticV3 && lifetime.producerID == 5 &&
		lifetime.storeTaken && lifetime.storeClosed && lifetime.storeRetired && lifetime.storeOwner != nil
	lifetime.storeMu.Unlock()
	lifetime.backupMeasurementMu.Lock()
	guard := lifetime.backupMeasurementGuard
	valid = valid && lifetime.backupMeasurementMaximum != 0 && guard != nil
	lifetime.backupMeasurementMu.Unlock()
	if !valid {
		return nil, ErrProtocol
	}
	return guard, nil
}

func serveRetiredBackupMeasurement(ctx context.Context, conn *net.UnixConn, client *Client, hold phaseControlFrame) error {
	deadline := time.Unix(0, hold.deadlineUnixNano)
	if hold.deadlineUnixNano <= 0 || !time.Now().Before(deadline) {
		return ErrCanceled
	}
	if outer, ok := ctx.Deadline(); ok && deadline.After(outer) {
		return ErrProtocol
	}
	guard, err := client.retiredBackupGuard()
	if err != nil {
		return err
	}
	operation, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	if err := conn.SetDeadline(deadline); err != nil {
		return ErrTransport
	}
	join := archiveMeasurementCancellation(operation, conn)
	defer join()
	release := hold
	release.op, release.sequence = phaseBackupMeasurementRelease, hold.sequence+1
	rawHold, rawRelease := hold.encode(), release.encode()
	var callbackMu sync.Mutex
	var callbackErr error
	var completed atomic.Bool
	invoked, closed := 0, false
	valid := false
	func() {
		defer func() {
			if recover() != nil {
				err = ErrPanic
			}
			completedBeforeReturn := completed.Load()
			if !completedBeforeReturn {
				_ = conn.Close() // Unblock an escaped callback, including guard panic.
			}
			callbackMu.Lock()
			closed = true
			err = errors.Join(err, callbackErr)
			valid = invoked == 1 && completedBeforeReturn
			callbackMu.Unlock()
		}()
		err = guard(operation, func(context.Context) (retErr error) {
			callbackMu.Lock()
			defer callbackMu.Unlock()
			defer func() { callbackErr = errors.Join(callbackErr, retErr) }()
			if closed || invoked != 0 {
				return ErrProtocol
			}
			invoked++
			defer completed.Store(true)
			if n, err := conn.Write(rawHold[:]); err != nil || n != len(rawHold) {
				return ErrTransport
			}
			var raw [FrameBytes]byte
			if _, err := io.ReadFull(conn, raw[:]); err != nil {
				return ErrTransport
			}
			if raw != rawRelease {
				return ErrProtocol
			}
			return operation.Err()
		})
	}()
	// All state, SDK and engine guard locks have been released before this ACK.
	if err != nil || !valid || operation.Err() != nil {
		return errors.Join(ErrProtocol, err, operation.Err())
	}
	if n, err := conn.Write(rawRelease[:]); err != nil || n != len(rawRelease) {
		return ErrTransport
	}
	return nil
}
