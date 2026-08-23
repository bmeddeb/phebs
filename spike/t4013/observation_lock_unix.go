//go:build darwin || linux

package t4013

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// lockObservationOutput serializes V25 execution and resume without leaving a
// stale lock after process death. ponytail: this locks the evidence directory;
// use a dedicated lock inode only if one directory must host concurrent runs.
func lockObservationOutput(path string) (*os.File, error) {
	finalPath, err := canonicalNewOutputPath(path)
	if err != nil {
		return nil, err
	}
	directory := filepath.Dir(finalPath)
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.Join(err, errors.New("T40.13 observation lock directory is invalid"))
	}
	file, err := os.Open(directory)
	if err != nil {
		return nil, fmt.Errorf("open T40.13 observation lock directory: %w", err)
	}
	opened, statErr := file.Stat()
	if statErr != nil || !opened.IsDir() || !os.SameFile(info, opened) {
		return nil, errors.Join(
			errors.New("T40.13 observation lock directory changed during open"),
			statErr, file.Close(),
		)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, errors.Join(
			errors.New("T40.13 observation execution or resume is already active"),
			err, file.Close(),
		)
	}
	return file, nil
}

func unlockObservationOutput(file *os.File) error {
	if file == nil {
		return nil
	}
	return errors.Join(
		syscall.Flock(int(file.Fd()), syscall.LOCK_UN),
		file.Close(),
	)
}
