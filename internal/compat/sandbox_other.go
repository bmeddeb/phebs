//go:build !darwin && !linux

package compat

import (
	"context"
	"fmt"
)

type commandOutput struct {
	stdout   string
	stderr   string
	exitCode int
	overflow bool
}

func sandboxSupported() error {
	return fmt.Errorf("%w: no supported sandbox backend on this operating system", ErrUnavailable)
}

func runSandboxed(context.Context, string, string, []string) (commandOutput, error) {
	return commandOutput{}, fmt.Errorf("%w: no supported sandbox backend on this operating system", ErrUnavailable)
}
