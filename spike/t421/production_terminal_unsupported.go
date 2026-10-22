//go:build !darwin && !linux

package t421

import "os"

func executionProcessSIGKILL(error, *os.ProcessState, int) bool { return false }
