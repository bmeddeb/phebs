// Package diagnostics contains the process-log boundary for advisory
// diagnostics. A broken log destination must never change pipeline state.
package diagnostics

import "log"

// Logf writes synchronously through the process logger and contains a panic
// from its destination. It deliberately has no queue, buffer, or retry path.
func Logf(format string, args ...any) {
	guard(func() { log.Printf(format, args...) })
}

func guard(write func()) {
	defer func() { _ = recover() }()
	write()
}
