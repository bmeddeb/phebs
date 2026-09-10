//go:build !darwin

package store

import (
	"context"
	"os"
)

func resumeLocalEngine(*os.Process) error { return errLocalEngineQuiescence }

func (*localEngine) measureStopped(context.Context, func(context.Context) error) error {
	return errLocalEngineQuiescence
}
