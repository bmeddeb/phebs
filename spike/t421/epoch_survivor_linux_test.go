//go:build linux

package t421

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

// A separate adopter keeps an actual orphan outside the measured controller's
// ancestry. Only its one child-reported candidate may be waited; no wait(-1).
func runLinuxEpochSurvivorAdopter(t *testing.T, selector, environment string) {
	t.Helper()
	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	report, output, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = report.Close() }()
	defer func() { _ = output.Close() }()
	command := exec.Command(os.Args[0], "-test.run="+selector, "-test.timeout=18s", "-test.v")
	command.Env = []string{environment + "=controller", "GOMAXPROCS=2", "GORACE=atexit_sleep_ms=0"}
	command.ExtraFiles = []*os.File{output}
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	command.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	// This sole genuine controller Wait always stays owned, including refusal.
	defer func() {
		if err := command.Wait(); err != nil {
			t.Error("isolated controller genuine Wait", err)
		}
	}()
	if err = output.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(io.LimitReader(report, 33))
	pid, parseErr := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || parseErr != nil || len(raw) > 32 || pid <= 0 || pid == command.Process.Pid || string(raw) != strconv.Itoa(pid)+"\n" {
		t.Fatal("private actual-creation candidate", string(raw), err, parseErr)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	for ctx.Err() == nil {
		var status unix.WaitStatus
		reaped, err := unix.Wait4(pid, &status, unix.WNOHANG, nil)
		if err != nil && !errors.Is(err, unix.ECHILD) && !errors.Is(err, unix.EINTR) {
			t.Fatal("targeted adopter Wait", err)
		}
		if reaped == pid {
			if !status.Signaled() || status.Signal() != unix.SIGKILL {
				t.Fatal("actual adopted child status", status)
			}
			t.Log("separate external adopter actually reaped its SIGKILL child")
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("separate adopter reap deadline", ctx.Err())
}

func assertLinuxEpochSurvivorAccounting(t *testing.T, ctx context.Context, root *linuxEpochTraceRoot, child *linuxEpochTraceLeader, shell *os.File) {
	t.Helper()
	ordinary, err := t4013.ObserveProcessTreeRecords(ctx, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range ordinary {
		if row.PID == child.original.pid {
			t.Fatal("fixture did not leave the controller's actual ancestry")
		}
	}
	rows, proofs, err := root.owner.observeWholeCohort(ctx, os.Getpid())
	if err != nil || len(rows) != len(ordinary)+1 {
		t.Fatal("source-owned union omitted or duplicated its external survivor", rows, err)
	}
	var survivor t4013.NativeProcessRecord
	var rss uint64
	for _, row := range rows {
		rss += uint64(row.RSSBytes)
		if row.PID == child.original.pid {
			survivor = row
		}
	}
	if survivor.RSSBytes <= 0 || survivor.ParentPID != os.Getppid() || survivor.ParentPID == child.original.parent || !proofs[survivor] {
		t.Fatal("cohort replaced current parent/RSS or lacked source proof", survivor, proofs)
	}
	// The mechanical fixture owns exact kernel/event facts, not production
	// resource issuance. Exercise the same held-image classifier/gauge below it.
	held := root.owner.held
	image, err := os.Open("/proc/self/exe")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = image.Close() })
	unused, err := os.CreateTemp(t.TempDir(), "unexecuted-roles-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unused.Close() })
	held.controllerImage, held.shellImage = image, shell
	for i := range held.images {
		held.images[i] = unused
	}
	for i := range held.gitImages {
		held.gitImages[i] = unused
	}
	held.resources = &linuxEpochResourceAdmission{held: held, trace: root.owner}
	gauge, _, err := held.gauge(ctx, os.Getpid(), held.controllerStart)
	if err != nil {
		t.Fatal(err)
	}
	gauge.probe = func(context.Context, int) ([]t4013.NativeProcessRecord, error) { return rows, nil }
	gauge.cohortRow = func(row t4013.NativeProcessRecord) bool { return proofs[row] }
	observed, err := gauge.Sample(ctx)
	if err != nil || !observed.Available || observed.ObservedRSSBytes != rss || observed.ObservedDescendants != uint64(len(rows)-1) || survivor.ParentPID != os.Getppid() {
		t.Fatal("whole gauge failed full raw union charge", observed, err)
	}
	delete(root.owner.leaders, child.original.pid)
	_, _, refused := root.owner.observeWholeCohort(ctx, os.Getpid())
	root.owner.leaders[child.original.pid] = child
	if refused == nil {
		t.Fatal("current SID candidate manufactured source ownership")
	}
	t.Logf("external_survivor_RSS=%d whole_union_RSS=%d observed_owned_rows=%d current_parent_preserved=true unknown_session_candidate_refused=true", survivor.RSSBytes, observed.ObservedRSSBytes, len(rows))
}

func TestProcessObservationCohortProofIsExactAndPrivate(t *testing.T) {
	for _, kind := range []string{"absent", "exact", "birth", "RSS", "parent", "pid"} {
		t.Run(kind, func(t *testing.T) {
			gauge, rows := observationFixture(t)
			rows[1].ParentPID = 1
			proof := rows[1]
			if kind != "absent" {
				gauge.cohortRow = func(row t4013.NativeProcessRecord) bool { return row == proof }
			}
			switch kind {
			case "birth":
				rows[1].StartIdentity += "x"
			case "RSS":
				rows[1].RSSBytes++
			case "parent":
				rows[1].ParentPID++
			case "pid":
				rows[1].PID++
			}
			gauge.probe = func(context.Context, int) ([]t4013.NativeProcessRecord, error) { return rows, nil }
			value, err := gauge.Sample(t.Context())
			if (err == nil) != (kind == "exact") || err == nil && (value.ObservedRSSBytes != 12288 || value.ObservedDescendants != 1 || rows[1].ParentPID != 1) {
				t.Fatal("exact raw-row proof scope", kind, value, err)
			}
		})
	}
}
