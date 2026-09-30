//go:build unix

package typedsandbox

import "golang.org/x/sys/unix"

// writeRefusalSite performs at most one raw write to descriptor 2 and can never
// block. A terminal refusal must not hang on an attach stream whose reader is
// itself waiting for this process to exit, so a refused or short write is dropped
// rather than retried: missing evidence stays missing, exactly as watchdogExit
// treats its own fd-2 frame.
//
// The kind check mirrors nonblockingReportFD. O_NONBLOCK is a property of the open
// file description, not of this process's descriptor table, so setting it on a
// descriptor inherited from a parent shell would leak the flag out of this process
// and leave the parent's stderr non-blocking after exit. A regular file cannot
// block on write and is written with its flags untouched; a pipe or socket — the
// container attach transport — is made non-blocking first and the flag is
// deliberately not restored, because the supervisor already sets descriptor 2
// non-blocking for its whole lifetime and clearing it here would make the
// watchdog's later frame write blockable. Anything else, a terminal above all, is
// left alone and the token is dropped rather than risk hanging a terminal exit.
func writeRefusalSite(frame []byte) {
	if len(frame) == 0 {
		return
	}
	var st unix.Stat_t
	if unix.Fstat(2, &st) != nil {
		return
	}
	switch st.Mode & unix.S_IFMT {
	case unix.S_IFREG:
	case unix.S_IFIFO, unix.S_IFSOCK:
		if unix.SetNonblock(2, true) != nil {
			return
		}
	default:
		return
	}
	_, _ = unix.Write(2, frame)
}
