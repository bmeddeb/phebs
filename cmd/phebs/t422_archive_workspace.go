package main

import (
	"context"

	"github.com/bmeddeb/phebs/internal/custodybytes"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

// Offline archive work borrows the same authenticated root as its parent.
// The existing serial archive hooks run before transient deletion; only their
// actual engine scope supplies a guard. Backup instead holds the retired
// server's owned engine through the parent's closed measurement transport.
func bindT422ArchiveWorkspace(ctx context.Context, cancel context.CancelFunc) (context.Context, error) {
	maximum, err := dispatchadmission.ProductionArchiveMeasurements()
	if err != nil {
		return nil, err
	}
	if maximum == 0 {
		return ctx, nil
	}
	initial, err := dispatchadmission.ProductionWorkState()
	if err != nil || cancel == nil || initial.Mode != "" || initial.Phase != 12 || initial.ProducerID != 10 && initial.ProducerID != 11 {
		return nil, errT422LifecycleControl
	}
	file, path, info, volume, err := dispatchadmission.ProductionWorkspace()
	if err != nil {
		return nil, err
	}
	observer := custodybytes.NewBorrowed(file, path, info, volume)
	reports, err := newT422WorkspaceReports(initial)
	if err != nil {
		return nil, err
	}
	return custodybytes.WithCheckpoint(ctx, func(operation context.Context) error {
		fail := func() error {
			_ = observer.Fail()
			_ = reports.failed()
			cancel()
			return errT422LifecycleControl
		}
		current, stateErr := dispatchadmission.ProductionWorkState()
		if stateErr != nil || reports.begin(current) != nil {
			return fail()
		}
		guard := custodybytes.CheckpointGuard(operation)
		if initial.ProducerID == 10 {
			guard = dispatchadmission.ProductionArchiveMeasurement
		}
		value, sampleErr := observer.SampleGuarded(operation, 12, guard, func() bool {
			state, err := dispatchadmission.ProductionWorkState()
			if err != nil {
				return false
			}
			_, err = t422SourceRecord(state, initial)
			return err == nil
		})
		// Confirmation, maxima publication and reporting all follow guard
		// release, including the remote server's actual resume acknowledgment.
		if sampleErr != nil || reports.complete(value) != nil {
			return fail()
		}
		return nil
	}), nil
}
