//go:build darwin || linux

package t4013

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/sys/unix"
)

const (
	runRootLockName = ".t4013-operation.lock"
	runRootLockEnv  = "T4013_RUN_LOCK_FD"
)

type runRootLock struct {
	root string
	file *os.File
}

func lockRunRoot(root string) (*runRootLock, error) {
	root, err := canonicalRunRoot(root)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(root, runRootLockName)
	file, err := inheritedRunRootLock(path)
	if err != nil {
		return nil, err
	}
	if file == nil {
		file, err = openRunRootLock(path)
		if err != nil {
			return nil, err
		}
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return nil, errors.Join(
			errors.New("T40.13 V25 custody mutation is already active"), err, file.Close(),
		)
	}
	return &runRootLock{root: root, file: file}, nil
}

func canonicalRunRoot(root string) (string, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) == string(filepath.Separator) {
		return "", errors.New("T40.13 V25 run root must be absolute and non-root")
	}
	real, err := filepath.EvalSymlinks(root)
	if err != nil || real != filepath.Clean(root) {
		return "", errors.Join(err, errors.New("T40.13 V25 run root is not canonical"))
	}
	info, err := os.Lstat(real)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.Join(err, errors.New("T40.13 V25 run root is invalid"))
	}
	file, err := openNoFollowDirectory(real)
	if err != nil {
		return "", fmt.Errorf("open T40.13 V25 run root: %w", err)
	}
	opened, statErr := file.Stat()
	if statErr != nil || !opened.IsDir() || !os.SameFile(info, opened) {
		return "", errors.Join(
			errors.New("T40.13 V25 run root changed during open"), statErr, file.Close(),
		)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close T40.13 V25 run root: %w", err)
	}
	return real, nil
}

func inheritedRunRootLock(path string) (*os.File, error) {
	raw := os.Getenv(runRootLockEnv)
	if raw == "" {
		return nil, nil
	}
	fd, err := strconv.Atoi(raw)
	if err != nil || fd < 3 {
		return nil, errors.New("T40.13 inherited V25 run-root lock descriptor is invalid")
	}
	duplicateFD, err := unix.FcntlInt(uintptr(fd), unix.F_DUPFD_CLOEXEC, custodyMinimumFD)
	if err != nil {
		return nil, fmt.Errorf("duplicate inherited T40.13 V25 run-root lock: %w", err)
	}
	file := os.NewFile(uintptr(duplicateFD), path)
	if file == nil {
		return nil, errors.Join(
			errors.New("adopt inherited T40.13 V25 run-root lock"), unix.Close(duplicateFD),
		)
	}
	if err := validateRunRootLock(path, file); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return file, nil
}

func openRunRootLock(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		if syncErr := errors.Join(file.Sync(), syncDirectory(filepath.Dir(path))); syncErr != nil {
			return nil, errors.Join(syncErr, file.Close())
		}
	} else if errors.Is(err, os.ErrExist) {
		file, err = os.OpenFile(path, os.O_RDWR, 0)
	}
	if err != nil {
		return nil, fmt.Errorf("open T40.13 V25 run-root lock: %w", err)
	}
	if err := validateRunRootLock(path, file); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	reservedFD, err := unix.FcntlInt(file.Fd(), unix.F_DUPFD_CLOEXEC, custodyMinimumFD)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("reserve T40.13 V25 run-root lock: %w", err), file.Close())
	}
	reserved := os.NewFile(uintptr(reservedFD), path)
	if reserved == nil {
		return nil, errors.Join(
			errors.New("adopt reserved T40.13 V25 run-root lock"), unix.Close(reservedFD), file.Close(),
		)
	}
	if err := file.Close(); err != nil {
		return nil, errors.Join(err, reserved.Close())
	}
	return reserved, nil
}

func validateRunRootLock(path string, file *os.File) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Mode().Perm() != 0o600 || info.Size() != 0 {
		return errors.Join(err, errors.New("T40.13 V25 run-root lock is invalid"))
	}
	opened, statErr := file.Stat()
	if statErr != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return errors.Join(statErr, errors.New("T40.13 V25 run-root lock changed during open"))
	}
	return nil
}

func (lock *runRootLock) Close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	err := lock.file.Close()
	lock.file = nil
	return err
}

// ValidateInheritedRunRootLock adopts and locks the descriptor inherited by a
// parent shell. The lock remains held by that parent's shared descriptor after
// this process exits.
func ValidateInheritedRunRootLock(root string) error {
	if os.Getenv(runRootLockEnv) == "" {
		return errors.New("T40.13 inherited V25 run-root lock descriptor is absent")
	}
	lock, err := lockRunRoot(root)
	if err != nil {
		return err
	}
	return lock.Close()
}

// ExecRunRootLocked replaces the current process with command while retaining
// the exact run-root lock descriptor for the shell and every direct mutator.
func ExecRunRootLocked(root, command string, arguments []string) (retErr error) {
	if !filepath.IsAbs(command) {
		return errors.New("T40.13 V25 lock command must be absolute")
	}
	commandInfo, err := os.Lstat(command)
	if err != nil || !commandInfo.Mode().IsRegular() || commandInfo.Mode()&os.ModeSymlink != 0 ||
		commandInfo.Mode().Perm()&0o111 == 0 {
		return errors.Join(err, errors.New("T40.13 V25 lock command is invalid"))
	}
	lock, err := lockRunRoot(root)
	if err != nil {
		return err
	}
	defer func() {
		retErr = errors.Join(retErr, lock.Close())
	}()
	if err := setRunRootLockCloseOnExec(lock.file, false); err != nil {
		return err
	}
	environment := make([]string, 0, len(os.Environ())+1)
	prefix := runRootLockEnv + "="
	for _, value := range os.Environ() {
		if len(value) < len(prefix) || value[:len(prefix)] != prefix {
			environment = append(environment, value)
		}
	}
	environment = append(environment, prefix+strconv.FormatUint(uint64(lock.file.Fd()), 10))
	argv := make([]string, 1, len(arguments)+1)
	argv[0] = command
	argv = append(argv, arguments...)
	if err := unix.Exec(command, argv, environment); err != nil {
		_ = setRunRootLockCloseOnExec(lock.file, true)
		return fmt.Errorf("exec T40.13 V25 run-root command: %w", err)
	}
	return nil
}

func setRunRootLockCloseOnExec(file *os.File, enabled bool) error {
	flags, err := unix.FcntlInt(file.Fd(), unix.F_GETFD, 0)
	if err != nil {
		return fmt.Errorf("read T40.13 V25 run-root lock flags: %w", err)
	}
	if enabled {
		flags |= unix.FD_CLOEXEC
	} else {
		flags &^= unix.FD_CLOEXEC
	}
	if _, err := unix.FcntlInt(file.Fd(), unix.F_SETFD, flags); err != nil {
		return fmt.Errorf("write T40.13 V25 run-root lock flags: %w", err)
	}
	return nil
}
