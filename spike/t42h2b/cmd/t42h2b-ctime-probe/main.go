//go:build linux

// Command t42h2b-ctime-probe measures the kernel's coarse inode timestamp
// behaviour that bounds T42.H2b metadata-drift detection. It exists because
// TestLinuxInputCustodyKernelMutationRefusals originally required a
// chmod-and-restore to be detected through Ctim, and Linux does not guarantee
// that a restore inside one coarse clock tick changes any compared field. That
// oracle was unsound and made the test fail intermittently; this probe measures
// the boundary so the correction can be re-derived rather than trusted.
//
// Unlike t42h2b-seal-probe this command asserts no host invariant. Every
// figure it prints is a measurement, and a low observability rate is the
// finding rather than a failure, so it exits zero unless it cannot run. It
// creates no files and leaves no disk copy; each trial holds one descriptor.
//
// Run it on the target host:
//
//	GOOS=linux GOARCH=amd64 go run ./spike/t42h2b/cmd/t42h2b-ctime-probe
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime"
	"time"

	"golang.org/x/sys/unix"
)

// allSeals mirrors linuxInputSeals in spike/t421/input_custody_linux.go.
const allSeals = unix.F_SEAL_WRITE | unix.F_SEAL_GROW | unix.F_SEAL_SHRINK | unix.F_SEAL_EXEC | unix.F_SEAL_SEAL

const payload = "neutral sealed input\n"

func main() {
	trials := flag.Int("trials", 200, "restore and unrestored drift trials to run")
	window := flag.Duration("window", 40*time.Millisecond, "measurement window for effective timestamp granularity")
	flag.Parse()
	if *trials < 1 || *window < time.Millisecond {
		fmt.Fprintln(os.Stderr, "trials must be >= 1 and window >= 1ms")
		os.Exit(2)
	}

	var release unix.Utsname
	if err := unix.Uname(&release); err != nil {
		fmt.Fprintf(os.Stderr, "uname failed: %v\n", err)
		os.Exit(2)
	}
	fmt.Printf("kernel=%s goarch=%s trials=%d window=%s\n", chars(release.Release[:]), runtime.GOARCH, *trials, *window)

	measureRestoreObservability(*trials)
	measureGranularity(*window)
	measureUnrestoredDrift(*trials)
	measureExplicitDrift()
}

// measureRestoreObservability reproduces the exact sequence the original test
// used and counts how often it leaves Ctim identical to the construction
// snapshot. When it does, custody compares equal fields and correctly accepts,
// which is why the restored-chmod oracle could not be relied on.
func measureRestoreObservability(trials int) {
	unchanged, changed, setupFailures := 0, 0, 0
	var worst time.Duration
	for range trials {
		file, snapshot, err := sealed()
		if err != nil {
			setupFailures++
			continue
		}
		start := time.Now()
		chmodErr := unix.Fchmod(int(file.Fd()), 0o600)
		writable, openErr := unix.Open(fmt.Sprintf("/proc/self/fd/%d", file.Fd()), unix.O_RDWR|unix.O_CLOEXEC, 0)
		if openErr == nil {
			_ = unix.Close(writable)
		}
		restoreErr := unix.Fchmod(int(file.Fd()), 0o400)
		elapsed := time.Since(start)
		var after unix.Stat_t
		statErr := unix.Fstat(int(file.Fd()), &after)
		closeFile(file)
		if chmodErr != nil || openErr != nil || restoreErr != nil || statErr != nil {
			setupFailures++
			continue
		}
		if after.Ctim == snapshot.Ctim {
			unchanged++
			if elapsed > worst {
				worst = elapsed
			}
			continue
		}
		changed++
	}
	fmt.Printf("\n=== restore-drift Ctim observability (%d trials) ===\n", trials)
	fmt.Printf("  ctim CHANGED   (restored-chmod oracle would pass): %d\n", changed)
	fmt.Printf("  ctim UNCHANGED (restored-chmod oracle FAILS):      %d\n", unchanged)
	fmt.Printf("  slowest unobserved sequence:                       %v\n", worst)
	fmt.Printf("  setup failures:                                    %d\n", setupFailures)
	if unchanged > 0 {
		fmt.Println("  finding: a chmod restored inside one coarse tick is not observable,")
		fmt.Println("           so restored-mode drift cannot be a sound refusal oracle.")
	}
}

// measureGranularity derives the effective inode timestamp resolution by
// counting distinct Ctim values produced by continuous chmod within a fixed
// window.
func measureGranularity(window time.Duration) {
	file, _, err := sealed()
	if err != nil {
		fmt.Printf("\n=== coarse clock ===\n  unavailable: %v\n", err)
		return
	}
	defer closeFile(file)
	previous, err := current(file)
	if err != nil {
		fmt.Printf("\n=== coarse clock ===\n  unavailable: %v\n", err)
		return
	}
	ticks := 0
	start := time.Now()
	for time.Since(start) < window {
		if err := unix.Fchmod(int(file.Fd()), 0o400); err != nil {
			break
		}
		now, err := current(file)
		if err != nil {
			break
		}
		if now != previous {
			ticks++
			previous = now
		}
	}
	observed := time.Since(start)
	fmt.Printf("\n=== coarse clock ===\n")
	fmt.Printf("  distinct Ctim values in %v of continuous chmod: %d\n", observed, ticks)
	if ticks > 0 {
		fmt.Printf("  effective granularity: ~%v\n", observed/time.Duration(ticks))
	} else {
		fmt.Println("  effective granularity: no tick observed in the window")
	}
}

// measureUnrestoredDrift confirms the replacement oracle: leaving the mode
// drifted is detected every time, because a compared field differs by
// construction rather than by clock resolution.
func measureUnrestoredDrift(trials int) {
	detected, setupFailures := 0, 0
	for range trials {
		file, snapshot, err := sealed()
		if err != nil {
			setupFailures++
			continue
		}
		chmodErr := unix.Fchmod(int(file.Fd()), 0o600)
		var after unix.Stat_t
		statErr := unix.Fstat(int(file.Fd()), &after)
		closeFile(file)
		if chmodErr != nil || statErr != nil {
			setupFailures++
			continue
		}
		if after.Ctim != snapshot.Ctim || after.Mode&0o7777 != 0o400 {
			detected++
		}
	}
	fmt.Printf("\n=== unrestored drift detection (%d trials) ===\n", trials)
	fmt.Printf("  detected: %d  setup failures: %d\n", detected, setupFailures)
	if detected == trials-setupFailures && setupFailures == 0 {
		fmt.Println("  finding: unrestored mode drift is a deterministic refusal oracle.")
	}
}

// measureExplicitDrift confirms the second replacement oracle: an explicit
// utimensat moves Mtim on a fully sealed memfd, leaving the seal set intact.
func measureExplicitDrift() {
	file, before, err := sealed()
	if err != nil {
		fmt.Printf("\n=== explicit timestamp drift ===\n  unavailable: %v\n", err)
		return
	}
	defer closeFile(file)
	drifted := unix.NsecToTimespec(int64(before.Mtim.Sec)*int64(time.Second) + int64(before.Mtim.Nsec) - int64(72*time.Hour))
	proc := fmt.Sprintf("/proc/self/fd/%d", file.Fd())
	utimesErr := unix.UtimesNanoAt(unix.AT_FDCWD, proc, []unix.Timespec{drifted, drifted}, 0)
	after, statErr := current(file)
	seals, sealErr := unix.FcntlInt(file.Fd(), unix.F_GET_SEALS, 0)
	fmt.Printf("\n=== explicit timestamp drift ===\n")
	fmt.Printf("  utimensat on fully sealed memfd: %v\n", utimesErr)
	fmt.Printf("  mtim moved: %v (stat=%v)\n", statErr == nil && after.Mtim != before.Mtim, statErr)
	fmt.Printf("  seal set intact: %v (seals=0x%x err=%v)\n", sealErr == nil && seals&allSeals == allSeals, seals, sealErr)
	if utimesErr == nil && statErr == nil && after.Mtim != before.Mtim {
		fmt.Println("  finding: explicit timestamp drift is a deterministic refusal oracle.")
	}
}

// sealed builds one sealed nonexecutable copy and returns its read-only keeper
// with the construction snapshot custody would compare against.
func sealed() (*os.File, unix.Stat_t, error) {
	var snapshot unix.Stat_t
	fd, err := unix.MemfdCreate("phebs-ctime-probe", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING|unix.MFD_NOEXEC_SEAL)
	if err != nil {
		return nil, snapshot, err
	}
	writer := os.NewFile(uintptr(fd), "probe-writer")
	if _, err := writer.Write([]byte(payload)); err != nil {
		_ = writer.Close()
		return nil, snapshot, err
	}
	chmodErr := unix.Fchmod(fd, 0o400)
	_, sealErr := unix.FcntlInt(writer.Fd(), unix.F_ADD_SEALS, allSeals)
	statErr := unix.Fstat(fd, &snapshot)
	readerFd, reopenErr := unix.Open(fmt.Sprintf("/proc/self/fd/%d", fd), unix.O_RDONLY|unix.O_CLOEXEC, 0)
	closeErr := writer.Close()
	if chmodErr != nil || sealErr != nil || statErr != nil || reopenErr != nil || closeErr != nil {
		if reopenErr == nil {
			_ = unix.Close(readerFd)
		}
		return nil, snapshot, errors.Join(chmodErr, sealErr, statErr, reopenErr, closeErr)
	}
	return os.NewFile(uintptr(readerFd), "probe-reader"), snapshot, nil
}

func current(file *os.File) (unix.Stat_t, error) {
	var stat unix.Stat_t
	err := unix.Fstat(int(file.Fd()), &stat)
	return stat, err
}

func closeFile(file *os.File) {
	if file != nil {
		_ = file.Close()
	}
}

// chars renders a fixed-size utsname field, whose element type differs between
// linux architectures.
func chars[T int8 | uint8](values []T) string {
	out := make([]byte, 0, len(values))
	for _, value := range values {
		if value == 0 {
			break
		}
		out = append(out, byte(value))
	}
	return string(out)
}
