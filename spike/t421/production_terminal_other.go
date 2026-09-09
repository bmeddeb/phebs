//go:build !darwin

package t421

import (
	"context"
	"os"
	"os/exec"
)

func killExecutionProcessSession(context.Context, *exec.Cmd, <-chan error) (executionProcessDeath, error) {
	return executionProcessDeath{}, ErrExecutionProductionCustody
}

func executionProcessSIGKILL(error, *os.ProcessState, int) bool { return false }
