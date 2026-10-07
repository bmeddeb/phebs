package t4013

import (
	"context"
	"errors"
)

// ObserveProcessExecutablePath returns one bounded native process-image path.
// It is an observation, not custody; callers must independently hold and hash
// the canonical image when identity matters.
func ObserveProcessExecutablePath(ctx context.Context, pid int) (string, error) {
	paths, err := ObserveProcessExecutablePaths(ctx, []int{pid})
	if err != nil {
		return "", err
	}
	if len(paths) != 1 {
		return "", errors.New("native process executable observation is incomplete")
	}
	return paths[0], nil
}

// ObserveProcessExecutablePaths performs bounded native path acquisitions for
// at most two exact PIDs. Darwin bounds kernel arguments; Linux bounds readlink
// and brackets it with process-lifetime observations. Neither holds image custody.
func ObserveProcessExecutablePaths(ctx context.Context, pids []int) ([]string, error) {
	if ctx == nil || len(pids) == 0 || len(pids) > 2 || ctx.Err() != nil {
		return nil, errors.New("native process executable observation is unavailable")
	}
	for index, pid := range pids {
		if pid <= 0 || index > 0 && pid == pids[index-1] {
			return nil, errors.New("native process executable observation is unavailable")
		}
	}
	paths, err := processExecutablePaths(pids)
	if err != nil {
		return nil, err
	}
	if len(paths) != len(pids) {
		return nil, errors.New("native process executable observation is incomplete")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return paths, nil
}
