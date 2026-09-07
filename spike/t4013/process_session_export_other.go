//go:build !darwin && !linux

package t4013

import (
	"errors"
	"time"
)

func PrivateProcessSessionMembers(int) (int, error) {
	return 0, errors.New("private process sessions require Linux or macOS")
}

func WaitPrivateProcessSession(int, time.Time) error {
	return errors.New("private process sessions require Linux or macOS")
}

func KillPrivateProcessSession(int) error {
	return errors.New("private process sessions require Linux or macOS")
}
