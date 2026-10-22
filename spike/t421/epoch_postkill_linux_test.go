//go:build linux

package t421

import (
	"context"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func assertLinuxEpochPostKillEvents(t *testing.T, ctx context.Context, root *linuxEpochTraceRoot, survivor *linuxEpochTraceLeader, shell *os.File, mode string) {
	t.Helper()
	if err := root.hardDeathAnchor(ctx); err != nil || root.command.ProcessState != nil {
		t.Fatal("original root must already be terminal and genuinely unreaped", err)
	}
	held := root.owner.held
	image, err := os.Open("/proc/self/exe")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = image.Close() })
	unused, err := os.CreateTemp(t.TempDir(), "unexecuted-images-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unused.Close() })
	sleep, err := os.Open("/usr/bin/sleep")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sleep.Close() })
	held.controllerImage, held.shellImage = image, shell
	for i := range held.images {
		held.images[i] = unused
	}
	for i := range held.gitImages {
		held.gitImages[i] = unused
	}
	held.images[3] = sleep // Test-only exact held image, not production tool admission.
	root.owner.tids = len(root.tasks)
	if err := unix.PtraceCont(survivor.original.pid, 0); err != nil {
		t.Fatal(err)
	}
	executed := survivor
	if mode == "postkill-fork-exec" {
		stop := rootFirstKernelStop(t, ctx, survivor.original.pid)
		event := int(stop.status) >> 8
		if event != unix.PTRACE_EVENT_FORK && event != unix.PTRACE_EVENT_VFORK && event != unix.PTRACE_EVENT_CLONE {
			t.Fatal("post-death source creation event missing", stop)
		}
		child, err := unix.PtraceGetEventMsg(survivor.original.pid)
		if err != nil || child == 0 {
			t.Fatal("actual post-death creation message", child, err)
		}
		if err := root.stopped(ctx, survivor.original.pid, root.tasks[survivor.original.pid], stop); err != nil {
			t.Fatal("actual production post-death creation dispatcher", err)
		}
		executed = root.owner.leaders[int(child)]
		if executed == nil || executed.original.parent != survivor.original.pid || executed.parentBirth != survivor.original.birth || !executed.verified || root.tasks[int(child)] == nil || !root.tasks[int(child)].initial {
			t.Fatal("post-death creation lacked original source identity/image", executed)
		}
		if err := root.allowedImage(ctx, int(child), shell); err != nil {
			t.Fatal("actual inherited shell image before post-death child continuation", err)
		}
		stop = rootFirstKernelStop(t, ctx, int(child))
		if stop.status != int32(unix.SIGSTOP) {
			t.Fatal("actual registered initial child stop", stop)
		}
		if err := root.stopped(ctx, int(child), root.tasks[int(child)], stop); err != nil {
			t.Fatal("actual inherited-image continuation", err)
		}
	}
	stop := rootFirstKernelStop(t, ctx, executed.original.pid)
	if int(stop.status)>>8 != unix.PTRACE_EVENT_EXEC {
		t.Fatal("actual post-death EXEC stop missing", stop)
	}
	if err := root.stopped(ctx, executed.original.pid, root.tasks[executed.original.pid], stop); err != nil || !executed.verified {
		t.Fatal("actual production post-death held EXEC dispatcher", err)
	}
	if err := root.allowedImage(ctx, executed.original.pid, sleep); err != nil {
		t.Fatal("post-death exact executed image", err)
	}
	rows, proofs, err := root.owner.observeWholeCohort(ctx, os.Getpid())
	if err != nil {
		t.Fatal("post-death source cohort", err)
	}
	seen := make(map[int]bool)
	var rss int64
	expectedParent := survivor.original.pid
	if executed == survivor {
		expectedParent = os.Getpid()
	}
	for _, row := range rows {
		if seen[row.PID] {
			t.Fatal("post-death cohort charged duplicate PID", row.PID)
		}
		seen[row.PID] = true
		if row.PID == executed.original.pid {
			if !proofs[row] || row.ParentPID != expectedParent {
				t.Fatal("post-death current parent/source proof changed", row)
			}
			rss = row.RSSBytes
		}
	}
	if rss <= 0 || !seen[survivor.original.pid] {
		t.Fatal("post-death live child RSS missing", rss, seen)
	}
	delete(root.owner.leaders, executed.original.pid)
	_, _, refused := root.owner.observeWholeCohort(ctx, os.Getpid())
	root.owner.leaders[executed.original.pid] = executed
	if refused == nil {
		t.Fatal("post-death current SID candidate manufactured creation ownership")
	}
	if err := root.hardDeathAnchor(ctx); err != nil || root.command.ProcessState != nil {
		t.Fatal("post-death proofs lost original unreaped root anchor", err)
	}
	t.Logf("postkill_source_creation=%t actual_EXEC=true current_image_exact=true executed_RSS=%d cohort_rows_unique=true root_still_unreaped=true", executed != survivor, rss)
	if executed == survivor {
		return // Existing outer control owns its exact kill, genuine reap and HUP.
	}
	if err := unix.PidfdSendSignal(executed.fd, unix.SIGKILL, nil, 0); err != nil {
		t.Fatal(err)
	}
	rootFirstKernelTerminal(t, ctx, executed.original.pid)
	terminal, err := linuxEpochTraceWait(executed.original.pid, unix.WEXITED)
	if err != nil || !terminal.terminal(executed.original.pid) || terminal.status != int32(unix.SIGKILL) {
		t.Fatal("post-death descendant tracer terminal copy", terminal, err)
	}
	// The real surviving shell must receive SIGCHLD and perform wait itself.
	// A tracer's terminal copy is not the original parent's reap or pidfd HUP.
	sigchldForwarded := false
	for {
		mask, err := linuxEpochTracePoll(ctx, executed.fd)
		if err != nil || ctx.Err() != nil {
			t.Fatal("post-death original-parent reap deadline", mask, err, ctx.Err())
		}
		if mask&unix.POLLHUP != 0 && sigchldForwarded {
			break
		}
		stop, err := linuxEpochTraceWait(survivor.original.pid, unix.WSTOPPED)
		if err != nil {
			t.Fatal("targeted original parent delivery", err)
		}
		if stop.signo != 0 {
			if stop.code != 4 || stop.pid != int32(survivor.original.pid) || stop.uid != uint32(os.Getuid()) {
				t.Fatal("original parent stop identity", stop)
			}
			if err := root.stopped(ctx, survivor.original.pid, root.tasks[survivor.original.pid], stop); err != nil {
				t.Fatal("actual original parent delivery", err)
			}
			if int(stop.status)>>8 == 0 && int(stop.status)&255 == int(unix.SIGCHLD) {
				sigchldForwarded = true
			}
		}
		time.Sleep(time.Millisecond)
	}
	if err := executed.closeFD(); err != nil {
		t.Fatal(err)
	}
	root.removeTask(executed.original.pid)
	delete(root.owner.leaders, executed.original.pid)
	if root.owner.tids != 2 || len(root.tasks) != 2 || len(root.owner.leaders) != 2 {
		t.Fatal("post-death descendant slot or pidfd ownership retained")
	}
	t.Log("postkill descendant actual parent reap and original pidfd HUP joined before owned-slot retirement SIGCHLD_forwarded=true")
}
