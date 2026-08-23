//go:build !darwin && !linux

package t4013

import (
	"errors"
	"os"
)

func lockObservationOutput(string) (*os.File, error) {
	return nil, errors.New("T40.13 observation locking requires Linux or macOS")
}

func unlockObservationOutput(file *os.File) error {
	if file == nil {
		return nil
	}
	return file.Close()
}
