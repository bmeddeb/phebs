//go:build !linux

package t421

import (
	"context"
	"time"
)

func runLinuxPressureBorrowCommand(context.Context, time.Time, []string, []string) error {
	return ErrExecutionLauncher
}

func runLinuxPressureAuthorCommand(context.Context, time.Time, []string, []string) error {
	return ErrExecutionLauncher
}
