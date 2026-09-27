//go:build !linux

package typedworkspace

import (
	"context"
	"github.com/bmeddeb/phebs/internal/typedsandbox"
	"os"
)

func workerReadOnly(*os.File) error { return ErrCustody }
func loadWorkerControls(context.Context, string, typedsandbox.Allowance, string, string, string, func(*os.File) error) (WorkerControls, error) {
	return WorkerControls{}, ErrCustody
}
