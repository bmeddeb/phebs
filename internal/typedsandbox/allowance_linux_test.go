//go:build linux

package typedsandbox

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestAllowanceLinuxClockAndContext(t *testing.T) {
	c := testControlIdentity()
	a, err := BeginAllowance(t.Context(), c.PlanningDigest, c.AttemptDigest)
	if err != nil {
		t.Fatal(err)
	}
	if a.CheckLive(t.Context()) != nil {
		t.Fatal("own clock refused")
	}
	for _, change := range []func(*Allowance){func(v *Allowance) { v.BootID = "ffffffff-ffff-ffff-ffff-ffffffffffff" }, func(v *Allowance) { v.TimeInode++ }, func(v *Allowance) { v.TimeDevice++ }, func(v *Allowance) { v.Start += int64(time.Hour); v.Deadline += int64(time.Hour) }} {
		bad := a
		change(&bad)
		if bad.CheckLive(t.Context()) == nil {
			t.Fatal("changed clock accepted")
		}
	}
	now, err := bootNow()
	if err != nil {
		t.Fatal(err)
	}
	a.Start = now - int64(WallLimit) + int64(100*time.Millisecond)
	a.Deadline = a.Start + int64(WallLimit)
	ctx, stop, err := AllowanceContext(t.Context(), a)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("absolute context did not expire")
	}
	if a.CheckLive(t.Context()) == nil {
		t.Fatal("expired allowance admitted")
	}
}

func TestSupervisorAllowanceNamesLiveRefusal(t *testing.T) {
	c := testControlIdentity()
	a, err := BeginAllowance(t.Context(), c.PlanningDigest, c.AttemptDigest)
	if err != nil {
		t.Fatal(err)
	}
	now, err := bootNow()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*Allowance)
		want   refusalSite
	}{
		{"boot_id_mismatch", func(a *Allowance) {
			if a.BootID[0] == '0' {
				a.BootID = "1" + a.BootID[1:]
			} else {
				a.BootID = "0" + a.BootID[1:]
			}
		}, SiteAllowanceBootMismatch},
		{"time_device_mismatch", func(a *Allowance) { a.TimeDevice++ }, SiteAllowanceTimeMismatch},
		{"time_inode_mismatch", func(a *Allowance) { a.TimeInode++ }, SiteAllowanceTimeMismatch},
		{"future_window", func(a *Allowance) { a.Start = now + int64(time.Minute); a.Deadline = a.Start + int64(WallLimit) }, SiteAllowanceWindow},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := a
			tc.change(&bad)
			if site, err := readSupervisorAllowance(bad, ControlPlan, bad.PlanningDigest, c.SealDigest); site != tc.want || err != ErrRefused {
				t.Fatalf("site=%v err=%v, want site=%v ErrRefused", site, err, tc.want)
			}
			if err := bad.CheckLive(t.Context()); err != ErrRefused {
				t.Fatalf("public CheckLive error=%v, want ErrRefused", err)
			}
		})
	}
}

func TestAllowanceLiveReadAndFallbackSites(t *testing.T) {
	validBoot := []byte("00000000-0000-0000-0000-000000000001\n")
	for _, tc := range []struct {
		name string
		raw  []byte
		err  error
	}{
		{"boot_read_error", nil, os.ErrNotExist},
		{"boot_malformed", []byte("not-a-boot-id"), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, site, err := checkedBootID(tc.raw, tc.err); site != SiteAllowanceBootRead || err != ErrRefused {
				t.Fatalf("site=%v err=%v, want boot read refusal", site, err)
			}
		})
	}
	if boot, site, err := checkedBootID(validBoot, nil); boot != string(validBoot[:36]) || site != 0 || err != nil {
		t.Fatalf("valid boot = %q, site=%v err=%v", boot, site, err)
	}
	for _, tc := range []struct {
		name string
		stat unix.Stat_t
		err  error
	}{
		{"namespace_stat_error", unix.Stat_t{}, os.ErrNotExist},
		{"namespace_zero_device", unix.Stat_t{Ino: 2}, nil},
		{"namespace_zero_inode", unix.Stat_t{Dev: 1}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, site, err := checkedTimeNamespace(tc.stat, tc.err); site != SiteAllowanceTimeStat || err != ErrRefused {
				t.Fatalf("site=%v err=%v, want namespace stat refusal", site, err)
			}
		})
	}
	if device, inode, site, err := checkedTimeNamespace(unix.Stat_t{Dev: 1, Ino: 2}, nil); device != 1 || inode != 2 || site != 0 || err != nil {
		t.Fatalf("valid namespace = %d:%d, site=%v err=%v", device, inode, site, err)
	}
	c := testControlIdentity()
	a, err := BeginAllowance(t.Context(), c.PlanningDigest, c.AttemptDigest)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		now  int64
		err  error
		want refusalSite
	}{
		{"boottime_read_error", 0, ErrRefused, SiteAllowanceNowRead},
		{"before_start", a.Start - 1, nil, SiteAllowanceWindow},
		{"at_deadline", a.Deadline, nil, SiteAllowanceWindow},
		{"live", a.Start, nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			site, err := a.checkLiveWindow(tc.now, tc.err)
			if site != tc.want || (err != nil) != (tc.want != 0) {
				t.Fatalf("site=%v err=%v, want site=%v", site, err, tc.want)
			}
		})
	}
	if site, err := a.checkLiveSite(nil); site != SiteAllowanceLive || err != ErrRefused { //nolint:staticcheck // Deliberately exercise fail-closed nil-context refusal.
		t.Fatalf("nil context site=%v err=%v, want generic refusal", site, err)
	}
	bad := a
	bad.Schema = "invalid"
	if site, err := bad.checkLiveSite(t.Context()); site != SiteAllowanceLive || err != ErrRefused {
		t.Fatalf("invalid allowance site=%v err=%v, want generic refusal", site, err)
	}
	if site, err := readSupervisorAllowance(bad, ControlPlan, bad.PlanningDigest, c.SealDigest); site != SiteAllowanceLive || err != ErrRefused {
		t.Fatalf("supervisor invalid allowance site=%v err=%v, want generic refusal", site, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if site, err := a.checkLiveSite(ctx); site != SiteAllowanceLive || err != context.Canceled {
		t.Fatalf("canceled context site=%v err=%v, want generic canceled", site, err)
	}
}

type allowanceProbe struct {
	PID, Parent int
	Now         int64
	Event       string
}

func TestAllowanceProcessHelper(t *testing.T) {
	mode := os.Getenv("PHEBS_ALLOWANCE_TEST")
	if mode == "" {
		return
	}
	if mode == "owner" {
		child := exec.Command(os.Args[0], "-test.run=^TestAllowanceProcessHelper$")
		child.Env = append(os.Environ(), "PHEBS_ALLOWANCE_TEST=orphan")
		child.ExtraFiles = []*os.File{os.NewFile(3, "probe")}
		if child.Start() != nil {
			os.Exit(90)
		}
		_ = child.Wait()
		os.Exit(91)
	}
	if mode != "orphan" {
		os.Exit(92)
	}
	raw := os.Getenv("PHEBS_ALLOWANCE_RAW")
	a, err := DecodeAllowance([]byte(raw))
	if err != nil {
		os.Exit(93)
	}
	probe := os.NewFile(3, "probe")
	args := supervisorArgs(Options{Allowance: a, Control: ControlIdentity{Phase: ControlExecute, RequestDigest: testImage, SealDigest: testImage}})
	_, _, _, stop, err := bootstrapSupervisor(args, func(timerErr error) {
		if timerErr != nil {
			os.Exit(96)
		}
		now, _ := bootNow()
		_ = json.NewEncoder(probe).Encode(allowanceProbe{os.Getpid(), os.Getppid(), now, "expired"})
		os.Exit(124)
	})
	if err != nil {
		os.Exit(94)
	}
	defer stop()
	now, _ := bootNow()
	_ = json.NewEncoder(probe).Encode(allowanceProbe{os.Getpid(), os.Getppid(), now, "armed"})
	// A real blocking filesystem validation after the production bootstrap. No
	// writer ever opens this FIFO; only the absolute watchdog can end this process.
	_, _ = unix.Open(os.Getenv("PHEBS_ALLOWANCE_FIFO"), unix.O_RDONLY, 0)
	os.Exit(95)
}

func TestAllowanceOrphanedBlockedSecondSupervisor(t *testing.T) {
	c := testControlIdentity()
	a, err := BeginAllowance(t.Context(), "sha256:"+fmt.Sprintf("%064x", 3), c.AttemptDigest)
	if err != nil {
		t.Fatal(err)
	}
	now, err := bootNow()
	if err != nil {
		t.Fatal(err)
	}
	a.Start = now - int64(WallLimit) + int64(time.Second)
	a.Deadline = a.Start + int64(WallLimit)
	a.WorkerBytesUsed = 123
	a.WireBytesUsed = 456
	raw, err := EncodeAllowance(a)
	if err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(t.TempDir(), "validation")
	if err = unix.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	defer func() { _ = writer.Close() }()
	// Interphase work consumes part of the already fixed deadline.
	time.Sleep(100 * time.Millisecond)
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	owner := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestAllowanceProcessHelper$")
	owner.Env = append(os.Environ(), "PHEBS_ALLOWANCE_TEST=owner", "PHEBS_ALLOWANCE_RAW="+string(raw), "PHEBS_ALLOWANCE_FIFO="+fifo)
	owner.ExtraFiles = []*os.File{writer}
	if err = owner.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = owner.Process.Kill(); _ = owner.Wait() }()
	_ = writer.Close()
	events := make(chan allowanceProbe, 2)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		scan := bufio.NewScanner(reader)
		for scan.Scan() {
			var p allowanceProbe
			if json.Unmarshal(scan.Bytes(), &p) == nil {
				events <- p
			}
		}
	}()
	defer func() { _ = reader.Close(); <-readDone }()
	var armed allowanceProbe
	select {
	case armed = <-events:
	case <-ctx.Done():
		t.Fatal("supervisor did not arm")
	}
	if armed.Event != "armed" || armed.Parent != owner.Process.Pid {
		t.Fatal(armed)
	}
	orphan, err := os.FindProcess(armed.PID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = orphan.Kill(); _ = orphan.Release() }()
	if err = owner.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = owner.Wait()
	select {
	case expired := <-events:
		if expired.Event != "expired" || expired.PID != armed.PID || expired.Parent == armed.Parent || expired.Now < a.Deadline || expired.Now > a.Deadline+int64(time.Second) {
			t.Fatal("deadline refreshed or owner survived", armed, expired, a.Deadline)
		}
	case <-ctx.Done():
		t.Fatal("orphan gained another interval")
	}
	// If this test is namespace PID1, reap its newly adopted exact test child.
	var status unix.WaitStatus
	_, _ = unix.Wait4(armed.PID, &status, 0, nil)
}

func TestAllowanceWorkerAggregateBoundaries(t *testing.T) {
	for _, used := range []int64{0, 3, OutputBytes - 3, OutputBytes} {
		for _, delta := range []int64{-1, 0, 1} {
			n := int64(OutputBytes) - used + delta
			if n < 0 {
				continue
			}
			t.Run(strconv.FormatInt(used, 10)+"/"+strconv.FormatInt(delta, 10), func(t *testing.T) {
				canceled := false
				o := &childOutput{limit: OutputBytes - used, cancel: func() { canceled = true }}
				out, stderr := childStream{o, false}, childStream{o, true}
				first := min(n, 2)
				if _, err := out.Write(bytes.Repeat([]byte("o"), int(first))); err != nil && delta <= 0 {
					t.Fatal(err)
				}
				_, err := stderr.Write(bytes.Repeat([]byte("e"), int(n-first)))
				if delta <= 0 {
					if err != nil || canceled || int64(o.stdout.Len()+o.stderr.Len()) != n {
						t.Fatal(err, canceled)
					}
				} else if !canceled || !o.overflow {
					t.Fatal("aggregate overflow accepted")
				}
			})
		}
	}
}
