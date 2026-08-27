//go:build !darwin && !linux

package t4013

import (
	"errors"
)

const (
	runRootLockName = ".t4013-operation.lock"
	runRootLockEnv  = "T4013_RUN_LOCK_FD"
)

type runRootLock struct{}

func lockRunRoot(string) (*runRootLock, error) {
	return nil, errors.New("T40.13 V25 run-root locking requires Linux or macOS")
}

func (lock *runRootLock) Close() error {
	return nil
}

func (lock *runRootLock) inheritedDescriptorValue() (string, error) {
	return "", errors.New("T40.13 V25 run-root locking requires Linux or macOS")
}

func ValidateInheritedRunRootLock(string) error {
	return errors.New("T40.13 V25 run-root locking requires Linux or macOS")
}

func ExecRunRootLocked(string, string, []string) error {
	return errors.New("T40.13 V25 run-root locking requires Linux or macOS")
}
