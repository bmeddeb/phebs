//go:build darwin

package store

import (
	"context"
	"errors"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// Darwin sys/proc.h: SRUN=2, SSLEEP=3, SSTOP=4. A signal returning nil is not
// the stopped-state proof; the actual owned birth must reach SSTOP first.
type localEngineIdentity struct{ seconds, micros int64 }

func inspectLocalEngine(process *os.Process) (localEngineIdentity, bool, error) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", process.Pid)
	if err != nil || info == nil {
		return localEngineIdentity{}, false, errors.Join(errLocalEngineQuiescence, err)
	}
	start := info.Proc.P_starttime
	if info.Proc.P_pid != int32(process.Pid) || info.Eproc.Ppid != int32(os.Getpid()) ||
		start.Sec <= 0 || start.Usec < 0 || start.Usec >= 1_000_000 ||
		(info.Proc.P_stat != 2 && info.Proc.P_stat != 3 && info.Proc.P_stat != 4) {
		return localEngineIdentity{}, false, errLocalEngineQuiescence
	}
	return localEngineIdentity{start.Sec, int64(start.Usec)}, info.Proc.P_stat == 4, nil
}

func awaitLocalEngine(ctx context.Context, process *os.Process, identity localEngineIdentity, stopped bool) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		observed, paused, err := inspectLocalEngine(process)
		if err != nil || observed != identity {
			return errors.Join(errLocalEngineQuiescence, err)
		}
		if paused == stopped {
			return nil
		}
		timer := time.NewTimer(5 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func resumeLocalEngine(process *os.Process) error { return process.Signal(unix.SIGCONT) }

func (engine *localEngine) measureStopped(ctx context.Context, measure func(context.Context) error) (retErr error) {
	identity, paused, err := inspectLocalEngine(engine.process)
	if err != nil || paused {
		return errors.Join(errLocalEngineQuiescence, err)
	}
	// Install cleanup before signalling: cancellation and callback panic must
	// still resume the unreaped owned child before shutdown can take its mutex.
	engine.paused = true
	defer func() {
		resumeErr := resumeLocalEngine(engine.process)
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), localEngineStopTimeout)
		defer cancel()
		if resumeErr == nil {
			resumeErr = awaitLocalEngine(cleanup, engine.process, identity, false)
		}
		if resumeErr == nil {
			engine.paused = false
		}
		retErr = errors.Join(retErr, resumeErr)
	}()
	if err := engine.process.Signal(unix.SIGSTOP); err != nil {
		return err
	}
	if err := awaitLocalEngine(ctx, engine.process, identity, true); err != nil {
		return err
	}
	if err := measure(ctx); err != nil {
		return err
	}
	return ctx.Err()
}
