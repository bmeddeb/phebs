//go:build !linux

package store

import (
	"os/exec"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

// startBoundToParent has no parent-death binding outside Linux; Darwin keeps
// its existing supervision unchanged.
func startBoundToParent(_ *exec.Cmd, start func() (dispatchadmission.Handle, error)) (dispatchadmission.Handle, func(), error) {
	handle, err := start()
	return handle, func() {}, err
}
