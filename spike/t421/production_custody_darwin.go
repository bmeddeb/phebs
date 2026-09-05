//go:build darwin

package t421

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

func acquireProductionSourceLease(parent string) (*os.File, error) {
	path := filepath.Join(parent, productionSourceLeaseName)
	created, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err == nil {
		if created.Close() != nil {
			return nil, ErrExecutionProductionCustody
		}
	} else if !errors.Is(err, os.ErrExist) {
		return nil, ErrExecutionProductionCustody
	}
	file, err := t4013.OpenHostImage(path)
	if err != nil {
		return nil, ErrExecutionProductionCustody
	}
	info, err := file.Stat()
	current, pathErr := os.Lstat(path)
	if err != nil || pathErr != nil || !inputCustodyOwned(info) || !info.Mode().IsRegular() || info.Size() != 0 ||
		info.Mode().Perm() != 0o600 || !inputCustodySame(info, current) || unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB) != nil {
		_ = file.Close()
		return nil, ErrExecutionProductionCustody
	}
	return file, nil
}

func prepareProductionSession(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func signalProductionStop(process *os.Process) error { return process.Signal(syscall.SIGTERM) }
