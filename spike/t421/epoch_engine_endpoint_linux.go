//go:build linux

package t421

import (
	"context"
	"net"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

// Published by the joined endpoint only. A completed protocol exchange is not
// a successful workspace sample; checkpoint issuance checks both separately.
type linuxEpochEngineCompletion struct {
	phases [7]uint32
	count  uint32
	err    error
}

func (completion *linuxEpochEngineCompletion) record(phase uint32) error {
	if completion.count >= uint32(len(completion.phases)) {
		return ErrExecutionEpochOne
	}
	completion.phases[completion.count] = phase
	completion.count++
	return nil
}
func (completion *linuxEpochEngineCompletion) completed(run *ExecutionEpochOneRun, epoch uint64) bool {
	if completion == nil || run == nil || run.engineMeasurementFinished == nil {
		return false
	}
	select {
	case <-run.engineMeasurementFinished:
	default:
		return false
	}
	return completion.err == nil && completion.completeSequence(epoch)
}

func (completion *linuxEpochEngineCompletion) completeSequence(epoch uint64) bool {
	expected := [7]uint32{6, 6, 7, 7, 7, 8, 8}
	maximum := uint32(7)
	if epoch == 4 {
		expected, maximum = [7]uint32{8}, 1
	} else if epoch != 3 {
		return false
	}
	return completion.count == maximum && completion.phases == expected
}

// Child EOF/PC acknowledgement precedes parent endpoint publication. Join the
// publication under the already-owned operation, phase and stop deadlines;
// the shared finish path remains the sole receiver of engineMeasurementDone.
func (server *linuxEpochServer) waitEngineCompletion(ctx context.Context, run *ExecutionEpochOneRun, epoch uint64, stopDeadline time.Time) error {
	if server == nil || ctx == nil || ctx.Err() != nil || run == nil || run.engineMeasurementFinished == nil || stopDeadline.IsZero() {
		return ErrExecutionEpochOne
	}
	if _, bounded := ctx.Deadline(); !bounded {
		return ErrExecutionEpochOne
	}
	run.mu.Lock()
	deadline := run.phaseDeadline
	run.mu.Unlock()
	if deadline.IsZero() {
		return ErrExecutionEpochOne
	}
	if stopDeadline.Before(deadline) {
		deadline = stopDeadline
	}
	operation, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	select {
	case <-run.engineMeasurementFinished:
	case <-operation.Done():
		return ErrExecutionEpochOne
	}
	if operation.Err() != nil || !time.Now().Before(deadline) || !server.measurements.completed(run, epoch) {
		return ErrExecutionEpochOne
	}
	return nil
}

func (adapter *linuxEpochCoordinator) engineMeasurements(epoch uint64) (string, uint32) {
	if !adapter.issued() {
		return "", 0
	}
	return adapter.recipe.recipe.engine(epoch)
}

func (adapter *linuxEpochCoordinator) serveEngineMeasurement(ctx context.Context, run *ExecutionEpochOneRun, conn *net.UnixConn, binding dispatchadmission.EngineMeasurementBinding, maximum uint32) <-chan error {
	done := make(chan error, 1)
	server := adapter.held.servers[run]
	go func() {
		defer close(run.engineMeasurementFinished)
		defer func() { _ = conn.Close() }()
		recipe, selectedMaximum := adapter.engineMeasurements(run.epoch.Epoch)
		if server == nil || server.trace == nil || selectedMaximum != maximum || binding.Recipe != recipe || binding.ProducerID != uint32(run.epoch.Epoch+1) {
			done <- ErrExecutionEpochOne
			return
		}
		err := dispatchadmission.ServeEngineMeasurement(ctx, conn, binding, maximum, func(operation context.Context, phase uint32, measure func(context.Context, dispatchadmission.EngineMeasurementIdentity) error) error {
			actual, err := adapter.core.controller.EngineMeasurementPhaseForRecipe(binding.ProducerID, binding.Recipe)
			if err != nil || actual != phase {
				return ErrExecutionEpochOne
			}
			run.mu.Lock()
			deadline := run.phaseDeadline
			run.mu.Unlock()
			if deadline.IsZero() {
				return ErrExecutionEpochOne
			}
			phaseCtx, cancel := context.WithDeadline(operation, deadline)
			defer cancel()
			if err := server.trace.withEngineMeasurement(phaseCtx, measure); err != nil {
				return err
			}
			if adapter.recipe.recipe == linuxEpochCheckpointV13 && run.epoch.Epoch >= 3 {
				return server.measurements.record(phase)
			}
			return nil
		})
		if err == nil && adapter.recipe.recipe == linuxEpochCheckpointV13 && run.epoch.Epoch >= 3 && !server.measurements.completeSequence(run.epoch.Epoch) {
			err = ErrExecutionEpochOne
		}
		server.measurements.err = err
		if err != nil {
			run.stopOnce.Do(func() { close(run.stop) })
		}
		done <- err
	}()
	return done
}
