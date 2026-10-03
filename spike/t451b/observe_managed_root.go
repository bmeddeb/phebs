package t451b

import (
	"context"
	"errors"
	"io/fs"

	"github.com/bmeddeb/phebs/internal/typedsandbox"
)

func startRootManagedObservations(ctx context.Context, proc fs.FS, workerPID uint32, euid int) (func() (Observations, error), error) {
	if ctx == nil || proc == nil || workerPID <= 1 || euid != 0 {
		return nil, errors.New("root managed observer identity unavailable")
	}
	return startObservationsBounded(ctx, proc, workerPID, 65534, typedsandbox.TaskLimit)
}
