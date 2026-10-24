//go:build !linux

package t421

import (
	"context"
	"time"
)

func runLinuxExecutionPreworkCommand(context.Context, time.Time, []string, []string) error {
	return ErrExecutionLauncher
}
