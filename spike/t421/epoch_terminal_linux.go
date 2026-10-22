//go:build linux

package t421

import (
	"context"
	"errors"
	"time"
)

// The source-bound finish adapter alone issues runtime entry. Mechanical tests
// exercise this actor layer without manufacturing checkpoint/resource admission.
func (root *linuxEpochTraceRoot) finishHardDeath(operation context.Context, deadline time.Time) (executionProcessDeath, error) {
	if root == nil || operation == nil || operation.Err() != nil || root.command == nil || root.command.Process == nil || root.done == nil {
		return executionProcessDeath{}, ErrExecutionEpochOne
	}
	bound, bounded := operation.Deadline()
	if !bounded || deadline.IsZero() || !time.Now().Before(deadline) {
		return executionProcessDeath{}, ErrExecutionEpochOne
	}
	if bound.Before(deadline) {
		deadline = bound
	}
	if maximum := time.Now().Add(30 * time.Second); maximum.Before(deadline) {
		deadline = maximum
	}
	ctx, cancel := context.WithDeadline(operation, deadline)
	defer cancel()
	if err := root.requestHardDeath(ctx); err != nil {
		return executionProcessDeath{}, errors.Join(ErrExecutionEpochOne, err)
	}
	completion, joined := waitExecutionProcessRoot(root.done, deadline)
	late := !joined
	if !joined {
		// Deadline failure remains failure; real cleanup and sole Wait stay owned.
		root.requestStop()
		completion = <-root.done
	}
	death := root.death // The actual done receive publishes the native/cleanup facts.
	if late || ctx.Err() != nil || !time.Now().Before(deadline) || !root.hardDeathIssued || root.cleanupErr != nil || !root.waitReturned || !death.RootJoined || !death.SessionEmpty || death.ProcessState != root.command.ProcessState || !executionProcessSIGKILL(death.WaitErr, death.ProcessState, root.command.Process.Pid) {
		return death, errors.Join(ErrExecutionEpochOne, completion, root.cleanupErr, ctx.Err())
	}
	return death, nil
}
