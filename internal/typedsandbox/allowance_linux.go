//go:build linux

package typedsandbox

import (
	"context"
	"encoding/binary"
	"math"
	"os"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

func bootNow() (int64, error) {
	var ts unix.Timespec
	if unix.ClockGettime(unix.CLOCK_BOOTTIME, &ts) != nil || ts.Sec < 0 || ts.Sec > math.MaxInt64/1000000000 || ts.Nsec < 0 || ts.Nsec >= 1000000000 {
		return 0, ErrRefused
	}
	n := ts.Sec * 1000000000
	if ts.Nsec > math.MaxInt64-n {
		return 0, ErrRefused
	}
	return n + ts.Nsec, nil
}
func checkedBootID(raw []byte, readErr error) (string, refusalSite, error) {
	if readErr != nil || len(raw) != 37 || raw[36] != '\n' || !bootUUID(string(raw[:36])) {
		return "", SiteAllowanceBootRead, ErrRefused
	}
	return strings.TrimSuffix(string(raw), "\n"), 0, nil
}

func checkedTimeNamespace(st unix.Stat_t, statErr error) (uint64, uint64, refusalSite, error) {
	if statErr != nil || st.Dev == 0 || st.Ino == 0 {
		return 0, 0, SiteAllowanceTimeStat, ErrRefused
	}
	return uint64(st.Dev), st.Ino, 0, nil
}

func clockIdentity() (string, uint64, uint64, refusalSite, error) {
	raw, err := readSmall("/proc/sys/kernel/random/boot_id", 37)
	boot, site, err := checkedBootID(raw, err)
	if err != nil {
		return "", 0, 0, site, err
	}
	var st unix.Stat_t
	statErr := unix.Stat("/proc/self/ns/time", &st)
	device, inode, site, err := checkedTimeNamespace(st, statErr)
	if err != nil {
		return "", 0, 0, site, err
	}
	return boot, device, inode, 0, nil
}
func BeginAllowance(ctx context.Context, planning, attempt string) (Allowance, error) {
	if ctx == nil || !hostDigest(planning) || !hostDigest(attempt) {
		return Allowance{}, ErrRefused
	}
	if err := ctx.Err(); err != nil {
		return Allowance{}, err
	}
	boot, device, inode, _, err := clockIdentity()
	if err != nil {
		return Allowance{}, err
	}
	now, err := bootNow()
	if err != nil || now <= 0 || now > math.MaxInt64-int64(WallLimit) {
		return Allowance{}, ErrRefused
	}
	a := Allowance{Schema: allowanceSchema, PlanningDigest: planning, AttemptDigest: attempt, BootID: boot, TimeDevice: device, TimeInode: inode, Start: now, Deadline: now + int64(WallLimit)}
	if a.Validate() != nil {
		return Allowance{}, ErrRefused
	}
	return a, ctx.Err()
}
func (a Allowance) checkLiveWindow(now int64, clockErr error) (refusalSite, error) {
	if clockErr != nil {
		return SiteAllowanceNowRead, clockErr
	}
	if _, err := a.remaining(now); err != nil {
		return SiteAllowanceWindow, err
	}
	return 0, nil
}

// checkLiveSite is the shared host/supervisor live check. The site is diagnostic
// only; CheckLive preserves the original public error surface. Each kernel fact
// is sampled once, in the same order as before this split.
func (a Allowance) checkLiveSite(ctx context.Context) (refusalSite, error) {
	if ctx == nil || a.Validate() != nil {
		return SiteAllowanceLive, ErrRefused
	}
	if err := ctx.Err(); err != nil {
		return SiteAllowanceLive, err
	}
	boot, device, inode, site, err := clockIdentity()
	if err != nil {
		return site, err
	}
	if boot != a.BootID {
		return SiteAllowanceBootMismatch, ErrRefused
	}
	if device != a.TimeDevice || inode != a.TimeInode {
		return SiteAllowanceTimeMismatch, ErrRefused
	}
	now, err := bootNow()
	if site, err = a.checkLiveWindow(now, err); err != nil {
		return site, err
	}
	if err := ctx.Err(); err != nil {
		return SiteAllowanceLive, err
	}
	return 0, nil
}

func (a Allowance) CheckLive(ctx context.Context) error {
	_, err := a.checkLiveSite(ctx)
	return err
}

// armAllowance performs only bounded scalar/clock/timer syscalls before arming.
// Common host/container BOOTTIME namespace is a mandatory native deployment
// invariant; checking that identity later does not repair an offset namespace.
// Poll's bounded cancellation interval never delays an already-readable timer.
func armAllowance(a Allowance, expire func(error)) (func(), error) {
	now, err := bootNow()
	if err != nil {
		return nil, err
	}
	if _, err = a.remaining(now); err != nil {
		return nil, err
	}
	fd, err := unix.TimerfdCreate(unix.CLOCK_BOOTTIME, unix.TFD_CLOEXEC|unix.TFD_NONBLOCK)
	if err != nil {
		return nil, ErrRefused
	}
	timer := unix.ItimerSpec{Value: unix.NsecToTimespec(a.Deadline)}
	if unix.TimerfdSettime(fd, unix.TFD_TIMER_ABSTIME, &timer, nil) != nil {
		_ = unix.Close(fd)
		return nil, ErrRefused
	}
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		defer func() { _ = unix.Close(fd) }()
		p := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		var ticks [8]byte
		for {
			select {
			case <-stop:
				return
			default:
			}
			n, e := unix.Poll(p, 50)
			if e == unix.EINTR {
				continue
			}
			if e != nil {
				expire(ErrRefused)
				return
			}
			if n > 0 {
				if p[0].Revents != unix.POLLIN {
					expire(ErrRefused)
					return
				}
				count, readErr := unix.Read(fd, ticks[:])
				if readErr == unix.EINTR || readErr == unix.EAGAIN {
					continue
				}
				if readErr != nil || count != len(ticks) || binary.NativeEndian.Uint64(ticks[:]) == 0 {
					expire(ErrRefused)
					return
				}
				expire(nil)
				return
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(stop); <-done }) }, nil
}

// AllowanceContext is for the one trusted native controller turn. It includes
// prepare, both runs and interphase work; it cannot mint or refresh an allowance.
func AllowanceContext(ctx context.Context, a Allowance) (context.Context, context.CancelFunc, error) {
	if ctx == nil {
		return nil, nil, ErrRefused
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	child, cancel := context.WithCancel(ctx)
	stop, err := armAllowance(a, func(error) { cancel() })
	if err != nil {
		cancel()
		return nil, nil, err
	}
	if err = a.CheckLive(child); err != nil {
		stop()
		cancel()
		return nil, nil, err
	}
	return child, func() { cancel(); stop() }, nil
}

func bootstrapSupervisor(args []string, expire func(error)) (Allowance, string, string, func(), error) {
	a, phase, request, err := parseSupervisorArgs(args)
	if err != nil {
		return Allowance{}, "", "", nil, err
	}
	stop, err := armAllowance(a, expire)
	if err != nil {
		return Allowance{}, "", "", nil, err
	}
	return a, phase, request, stop, nil
}

func readSupervisorAllowance(a Allowance, phase, request, seal string) (refusalSite, error) {
	if site, err := a.checkLiveSite(context.Background()); err != nil {
		return site, ErrRefused
	}
	root, err := openControlDirectory("/controls")
	if err != nil {
		return SiteAllowanceControls, ErrRefused
	}
	defer func() { _ = root.Close() }()
	raw, err := readControlFile(root, ControlSealFile, MaxControlSealBytes)
	if err != nil {
		return SiteAllowanceSeal, ErrRefused
	}
	if controlDigest(raw) != seal {
		return SiteAllowanceDigest, ErrRefused
	}
	if checkSealAllowance(raw, a, phase, request) != nil {
		return SiteAllowanceBinding, ErrRefused
	}
	return 0, nil
}

// Used after the absolute timer has been armed; no filesystem is touched here.
func supervisorExpired() { os.Exit(124) }
