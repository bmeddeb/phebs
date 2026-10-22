//go:build !darwin

package t421

import (
	"context"
	"os/exec"
)

func killExecutionProcessSession(context.Context, *exec.Cmd, <-chan error) (executionProcessDeath, error) {
	return executionProcessDeath{}, ErrExecutionProductionCustody
}
