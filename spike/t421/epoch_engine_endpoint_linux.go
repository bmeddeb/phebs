//go:build linux

package t421

import (
	"context"
	"net"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

func (*linuxEpochCoordinator) engineMeasurements(epoch uint64) uint32 {
	switch epoch {
	case 1:
		return 6
	case 2:
		return 1
	case 3:
		return 2
	default:
		return 0
	}
}

func (adapter *linuxEpochCoordinator) serveEngineMeasurement(ctx context.Context, run *ExecutionEpochOneRun, conn *net.UnixConn, binding dispatchadmission.EngineMeasurementBinding, maximum uint32) <-chan error {
	done := make(chan error, 1)
	server := adapter.held.servers[run]
	go func() {
		defer close(run.engineMeasurementFinished)
		defer func() { _ = conn.Close() }()
		if server == nil || server.trace == nil || adapter.engineMeasurements(run.epoch.Epoch) != maximum || binding.ProducerID != uint32(run.epoch.Epoch+1) {
			done <- ErrExecutionEpochOne
			return
		}
		err := dispatchadmission.ServeEngineMeasurement(ctx, conn, binding, maximum, func(operation context.Context, phase uint32, measure func(context.Context, dispatchadmission.EngineMeasurementIdentity) error) error {
			actual, err := adapter.core.controller.EngineMeasurementPhase(binding.ProducerID)
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
			return server.trace.withEngineMeasurement(phaseCtx, measure)
		})
		if err != nil {
			run.stopOnce.Do(func() { close(run.stop) })
		}
		done <- err
	}()
	return done
}
