//go:build linux

package t421

import (
	"errors"
	"time"

	"golang.org/x/sys/unix"
)

// This path has a stronger finite shape than an operational root: no CONT has
// ever occurred, so the initial exec-stop cannot have made application children.
// Capture refusal remains sticky. Only the original pinned child may be killed;
// only actual terminal CLD and native HUP permit genuine Wait/retirement.
func (root *linuxEpochTraceRoot) cleanupInitial() {
	if !root.gateOwned || root.continued || root.pidfd < 0 || root.command.Process == nil {
		root.refuse("initial-cleanup-unavailable")
		for {
			time.Sleep(linuxEpochTraceFallback)
		}
	}
	if err := unix.PidfdSendSignal(root.pidfd, unix.SIGKILL, nil, 0); err != nil && !errors.Is(err, unix.ESRCH) {
		root.refuse("initial-owned-kill")
		for {
			time.Sleep(linuxEpochTraceFallback)
		}
	}
	ticker := time.NewTicker(linuxEpochTraceFallback)
	defer ticker.Stop()
	pid := root.command.Process.Pid
	for {
		stop, err := linuxEpochTraceWait(pid, unix.WSTOPPED)
		if err == nil && stop.signo != 0 {
			if stop.code != 4 || stop.pid != int32(pid) || stop.uid != uint32(unix.Getuid()) {
				root.refuse("initial-cleanup-stop")
				for {
					<-ticker.C
				}
			}
			// Only source-created initial/exit stops are continued after original-fd
			// SIGKILL. This never resumes application work or admits an image.
			if err = unix.PtraceCont(pid, 0); err != nil && !errors.Is(err, unix.ESRCH) {
				root.refuse("initial-cleanup-continue")
				for {
					<-ticker.C
				}
			}
		} else if err != nil && !errors.Is(err, unix.EINTR) && !errors.Is(err, unix.ECHILD) {
			root.refuse("initial-cleanup-selector")
			for {
				<-ticker.C
			}
		}
		terminal, err := linuxEpochTraceWait(pid, unix.WEXITED|unix.WNOWAIT)
		if err == nil && terminal.terminal(pid) {
			mask, e := linuxEpochTracePoll(root.pidfd)
			if e == nil && mask&unix.POLLIN != 0 && mask&unix.POLLHUP == 0 {
				if !root.exitSeen {
					root.exitSeen = true
					close(root.exited)
				}
				root.waitRoot(true)
				return
			}
			root.refuse("initial-cleanup-pidfd")
			for {
				<-ticker.C
			}
		}
		if err != nil && !errors.Is(err, unix.EINTR) {
			root.refuse("initial-cleanup-terminal")
			for {
				<-ticker.C
			}
		}
		select {
		case <-root.wake:
		case <-ticker.C:
		}
	}
}
