//go:build linux

package store

import (
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"

	"golang.org/x/sys/unix"
)

const localEngineMaximumThreads = 1024

type localEngineIdentity struct {
	pid, parent, session, tracer            int
	birth                                   uint64
	namespaceDevice                         uint64
	namespaceInode                          uint64
	userNamespaceDevice, userNamespaceInode uint64
	imageDevice, imageInode                 uint64
}

type localEngineGroupState uint8

const (
	localEngineTransition localEngineGroupState = iota
	localEngineRunning
	localEnginePaused
)

func readLocalEngineProc(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.Join(errLocalEngineQuiescence, err)
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, 64<<10+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(raw) > 64<<10 {
		return nil, errors.Join(errLocalEngineQuiescence, readErr, closeErr)
	}
	return raw, nil
}

func localEngineTask(ctx context.Context, path string, pid, group int) (localEngineIdentity, byte, error) {
	var identity localEngineIdentity
	if ctx == nil || ctx.Err() != nil {
		return identity, 0, errors.Join(errLocalEngineQuiescence, localEngineContextError(ctx))
	}
	raw, err := readLocalEngineProc(path + "/stat")
	if err != nil {
		return identity, 0, err
	}
	begin, end := strings.IndexByte(string(raw), '('), strings.LastIndex(string(raw), ") ")
	if begin < 1 || end <= begin || strings.TrimSpace(string(raw[:begin])) != strconv.Itoa(pid) {
		return identity, 0, errLocalEngineQuiescence
	}
	statPrefix := string(raw[:end+2])
	fields := strings.Fields(string(raw[end+2:]))
	if len(fields) < 22 || len(fields[0]) != 1 || !strings.ContainsRune("RSDITt", rune(fields[0][0])) {
		return identity, 0, errLocalEngineQuiescence
	}
	identity.pid = pid
	identity.parent, err = strconv.Atoi(fields[1])
	if err != nil || identity.parent != os.Getpid() {
		return identity, 0, errLocalEngineQuiescence
	}
	identity.session, err = strconv.Atoi(fields[3])
	if err != nil || identity.session <= 0 {
		return identity, 0, errLocalEngineQuiescence
	}
	identity.birth, err = strconv.ParseUint(fields[19], 10, 64)
	if err != nil || identity.birth == 0 {
		return identity, 0, errLocalEngineQuiescence
	}
	raw, err = readLocalEngineProc(path + "/status")
	if err != nil {
		return identity, 0, err
	}
	found := 0
	for _, line := range strings.Split(string(raw), "\n") {
		values := strings.Fields(line)
		if len(values) == 2 && values[0] == "Tgid:" {
			if values[1] != strconv.Itoa(group) {
				return identity, 0, errLocalEngineQuiescence
			}
			found++
		}
		if len(values) == 5 && values[0] == "Uid:" {
			if os.Getuid() != os.Geteuid() {
				return identity, 0, errLocalEngineQuiescence
			}
			for _, value := range values[1:] {
				if value != strconv.Itoa(os.Getuid()) {
					return identity, 0, errLocalEngineQuiescence
				}
			}
			found++
		}
		if len(values) == 2 && values[0] == "TracerPid:" {
			identity.tracer, err = strconv.Atoi(values[1])
			if err != nil || identity.tracer < 0 {
				return identity, 0, errLocalEngineQuiescence
			}
			found++
		}
	}
	if found != 3 {
		return identity, 0, errLocalEngineQuiescence
	}
	var namespace, ownNamespace unix.Stat_t
	if unix.Stat(path+"/ns/pid", &namespace) != nil || unix.Stat("/proc/self/ns/pid", &ownNamespace) != nil || namespace.Dev != ownNamespace.Dev || namespace.Ino != ownNamespace.Ino {
		return identity, 0, errLocalEngineQuiescence
	}
	identity.namespaceDevice, identity.namespaceInode = uint64(namespace.Dev), namespace.Ino
	if unix.Stat(path+"/ns/user", &namespace) != nil || unix.Stat("/proc/self/ns/user", &ownNamespace) != nil || namespace.Dev != ownNamespace.Dev || namespace.Ino != ownNamespace.Ino {
		return identity, 0, errLocalEngineQuiescence
	}
	identity.userNamespaceDevice, identity.userNamespaceInode = uint64(namespace.Dev), namespace.Ino
	var image unix.Stat_t
	if unix.Stat(path+"/exe", &image) != nil || image.Mode&unix.S_IFMT != unix.S_IFREG {
		return identity, 0, errLocalEngineQuiescence
	}
	identity.imageDevice, identity.imageInode = uint64(image.Dev), image.Ino
	// Bracket status, namespaces and image with the same native lifetime/state.
	after, err := readLocalEngineProc(path + "/stat")
	if err != nil || string(after) == "" {
		return identity, 0, errors.Join(errLocalEngineQuiescence, err)
	}
	endAfter := strings.LastIndex(string(after), ") ")
	if endAfter <= 0 {
		return identity, 0, errLocalEngineQuiescence
	}
	fieldsAfter := strings.Fields(string(after[endAfter+2:]))
	if len(fieldsAfter) < 22 || string(after[:endAfter+2]) != statPrefix {
		return identity, 0, errLocalEngineQuiescence
	}
	if len(fieldsAfter[0]) != 1 || !strings.ContainsRune("RSDITt", rune(fieldsAfter[0][0])) || fieldsAfter[1] != fields[1] || fieldsAfter[3] != fields[3] || fieldsAfter[19] != fields[19] {
		return identity, 0, errLocalEngineQuiescence
	}
	if (fieldsAfter[0] == "T" || fieldsAfter[0] == "t") != (fields[0] == "T" || fields[0] == "t") {
		// Ordinary stop/resume progress is not identity drift. A zero state
		// keeps the complete group transitional until the next bounded pass.
		return identity, 0, ctx.Err()
	}
	if ctx.Err() != nil {
		return identity, 0, ctx.Err()
	}
	return identity, fields[0][0], nil
}

// The sole Wait owner holds engine.mu throughout these observations.
func inspectLocalEngineGroupContext(ctx context.Context, process *os.Process) (localEngineIdentity, localEngineGroupState, error) {
	if ctx == nil || ctx.Err() != nil || process == nil || process.Pid <= 1 {
		return localEngineIdentity{}, localEngineTransition, errors.Join(errLocalEngineQuiescence, localEngineContextError(ctx))
	}
	path := "/proc/" + strconv.Itoa(process.Pid)
	before, beforeState, err := localEngineTask(ctx, path, process.Pid, process.Pid)
	if err != nil {
		return before, localEngineTransition, err
	}
	directory, err := os.Open(path + "/task")
	if err != nil {
		return before, localEngineTransition, errors.Join(errLocalEngineQuiescence, err)
	}
	names, readErr := directory.Readdirnames(localEngineMaximumThreads + 1)
	closeErr := directory.Close()
	if readErr != nil && readErr != io.EOF || closeErr != nil || len(names) == 0 || len(names) > localEngineMaximumThreads {
		return before, localEngineTransition, errors.Join(errLocalEngineQuiescence, readErr, closeErr)
	}
	paused, running := true, true
	leader := false
	for _, name := range names {
		tid, parseErr := strconv.Atoi(name)
		if parseErr != nil || tid <= 0 || strconv.Itoa(tid) != name {
			return before, localEngineTransition, errLocalEngineQuiescence
		}
		current, state, taskErr := localEngineTask(ctx, path+"/task/"+name, tid, process.Pid)
		if taskErr != nil || current.imageDevice != before.imageDevice || current.imageInode != before.imageInode || current.userNamespaceDevice != before.userNamespaceDevice || current.userNamespaceInode != before.userNamespaceInode || current.parent != before.parent || current.session != before.session || current.tracer != before.tracer || current.namespaceDevice != before.namespaceDevice || current.namespaceInode != before.namespaceInode {
			return before, localEngineTransition, errors.Join(errLocalEngineQuiescence, taskErr)
		}
		if tid == process.Pid {
			if current != before {
				return before, localEngineTransition, errLocalEngineQuiescence
			}
			leader = true
		}
		stopped := state == 'T' || state == 't'
		paused, running = paused && stopped, running && state != 0 && !stopped
	}
	after, afterState, err := localEngineTask(ctx, path, process.Pid, process.Pid)
	if err != nil || !leader || after != before {
		return before, localEngineTransition, errors.Join(errLocalEngineQuiescence, err)
	}
	if ctx.Err() != nil {
		return before, localEngineTransition, ctx.Err()
	}
	beforePaused, afterPaused := beforeState == 'T' || beforeState == 't', afterState == 'T' || afterState == 't'
	paused = paused && beforePaused && afterPaused
	running = running && beforeState != 0 && afterState != 0 && !beforePaused && !afterPaused
	if paused {
		// Every member is stopped: none can create another thread. Require the
		// same complete bounded inventory after the individual observations.
		dir, e := os.Open(path + "/task")
		if e != nil {
			return before, localEngineTransition, e
		}
		check, re := dir.Readdirnames(localEngineMaximumThreads + 1)
		ce := dir.Close()
		if re != nil && re != io.EOF || ce != nil || len(check) != len(names) {
			return before, localEngineTransition, errors.Join(errLocalEngineQuiescence, re, ce)
		}
		slices.Sort(names)
		slices.Sort(check)
		if !slices.Equal(names, check) || ctx.Err() != nil {
			return before, localEngineTransition, errors.Join(errLocalEngineQuiescence, ctx.Err())
		}
		return before, localEnginePaused, nil
	}
	if running {
		return before, localEngineRunning, nil
	}
	return before, localEngineTransition, nil
}

func localEngineContextError(ctx context.Context) error {
	if ctx == nil {
		return errLocalEngineQuiescence
	}
	return ctx.Err()
}

func inspectLocalEngineGroup(process *os.Process) (localEngineIdentity, localEngineGroupState, error) {
	return inspectLocalEngineGroupContext(context.Background(), process)
}

func inspectLocalEngine(process *os.Process) (localEngineIdentity, bool, error) {
	identity, state, err := inspectLocalEngineGroup(process)
	return identity, state == localEnginePaused, err
}

func awaitLocalEngine(ctx context.Context, process *os.Process, identity localEngineIdentity, paused bool) error {
	if ctx == nil {
		return errLocalEngineQuiescence
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		current, state, err := inspectLocalEngineGroupContext(ctx, process)
		if err != nil || current != identity {
			return errors.Join(errLocalEngineQuiescence, err)
		}
		if paused && state == localEnginePaused || !paused && state == localEngineRunning {
			return ctx.Err()
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

// A readiness-free pidfd is required before signalling or accepting liveness.
func localEngineLive(ctx context.Context, fd int) error {
	for {
		if ctx == nil || ctx.Err() != nil || fd < 0 {
			return errors.Join(errLocalEngineQuiescence, localEngineContextError(ctx))
		}
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		n, err := unix.Poll(fds, 0)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil || n != 0 || fds[0].Revents != 0 {
			return errors.Join(errLocalEngineQuiescence, err)
		}
		return nil
	}
}

func (engine *localEngine) measureStopped(ctx context.Context, measure func(context.Context) error) (retErr error) {
	if engine == nil || engine.process == nil || ctx == nil || ctx.Err() != nil || measure == nil {
		return errors.Join(errLocalEngineQuiescence, localEngineContextError(ctx))
	}
	// engine.mu excludes the sole Wait, reserving this original PID while pinning.
	fd, err := unix.PidfdOpen(engine.process.Pid, 0)
	if err != nil {
		return errors.Join(errLocalEngineQuiescence, err)
	}
	defer func() { retErr = errors.Join(retErr, unix.Close(fd)) }()
	identity, state, err := inspectLocalEngineGroupContext(ctx, engine.process)
	if err != nil || identity.tracer == 0 && state != localEngineRunning {
		return errors.Join(errLocalEngineQuiescence, err, ctx.Err())
	}
	if err := localEngineLive(ctx, fd); err != nil {
		return err
	}
	// Traced execution uses only its separately authenticated parent lease.
	// The parent chooses the group; the child compares its already-owned identity.
	if identity.tracer != 0 {
		invoked := 0
		selected, err := dispatchadmission.ProductionEngineMeasurement(ctx, dispatchadmission.EngineMeasurementIdentity{
			PID: uint32(identity.pid), Birth: identity.birth, Tracer: uint32(identity.tracer),
		}, func(operation context.Context) error {
			invoked++
			engine.paused = true
			if invoked != 1 {
				return errLocalEngineQuiescence
			}
			current, stopped, err := inspectLocalEngineGroupContext(operation, engine.process)
			if err != nil || current != identity || stopped != localEnginePaused {
				return errors.Join(errLocalEngineQuiescence, err)
			}
			if err := localEngineLive(operation, fd); err != nil {
				return err
			}
			if err := measure(operation); err != nil {
				return err
			}
			current, stopped, err = inspectLocalEngineGroupContext(operation, engine.process)
			if err != nil || current != identity || stopped != localEnginePaused {
				return errors.Join(errLocalEngineQuiescence, err)
			}
			return errors.Join(operation.Err(), localEngineLive(operation, fd))
		})
		if !selected || err != nil || invoked != 1 {
			return errors.Join(errLocalEngineQuiescence, err)
		}
		// Success includes the parent's matching RELEASE ACK after actual resume.
		if err := awaitLocalEngine(ctx, engine.process, identity, false); err != nil {
			return errors.Join(errLocalEngineQuiescence, err)
		}
		if err := localEngineLive(ctx, fd); err != nil {
			return err
		}
		engine.paused = false
		return ctx.Err()
	}
	engine.paused = true
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), localEngineStopTimeout)
		defer cancel()
		current, _, inspectErr := inspectLocalEngineGroupContext(cleanup, engine.process)
		resumeErr := errors.Join(inspectErr, localEngineLive(cleanup, fd))
		if resumeErr == nil && current != identity {
			resumeErr = errLocalEngineQuiescence
		}
		if resumeErr == nil {
			resumeErr = unix.PidfdSendSignal(fd, unix.SIGCONT, nil, 0)
		}
		if resumeErr == nil {
			resumeErr = awaitLocalEngine(cleanup, engine.process, identity, false)
		}
		if resumeErr == nil {
			engine.paused = false
		}
		retErr = errors.Join(retErr, resumeErr)
	}()
	if err := unix.PidfdSendSignal(fd, unix.SIGSTOP, nil, 0); err != nil {
		return err
	}
	if err := awaitLocalEngine(ctx, engine.process, identity, true); err != nil {
		return err
	}
	if err := localEngineLive(ctx, fd); err != nil {
		return err
	}
	if err := measure(ctx); err != nil {
		return err
	}
	current, after, err := inspectLocalEngineGroupContext(ctx, engine.process)
	if err != nil || current != identity || after != localEnginePaused {
		return errors.Join(errLocalEngineQuiescence, err)
	}
	return errors.Join(ctx.Err(), localEngineLive(ctx, fd))
}
