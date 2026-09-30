//go:build linux || darwin

package typedsandbox

import "golang.org/x/sys/unix"

// writeRefusalSite performs at most one raw write to descriptor 2 and can never
// block. A terminal refusal must not hang on an attach stream or a pipe whose
// reader is itself waiting for this process to exit, so the descriptor is put in
// non-blocking mode first and a refused or short write is dropped rather than
// retried: missing evidence stays missing, exactly as watchdogExit treats its own
// fd-2 frame. The flag is deliberately not restored, because the supervisor sets
// descriptor 2 non-blocking for its whole lifetime and clearing it here would make
// the watchdog's later frame write blockable.
func writeRefusalSite(frame []byte) {
	if unix.SetNonblock(2, true) != nil {
		return
	}
	_, _ = unix.Write(2, frame)
}
