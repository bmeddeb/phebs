//go:build !darwin

package t421

import (
	"os"
	"os/exec"
)

func acquireProductionSourceLease(string) (*os.File, error) {
	return nil, ErrExecutionProductionCustody
}
func prepareProductionSession(*exec.Cmd)     {}
func signalProductionStop(*os.Process) error { return ErrExecutionProductionCustody }
