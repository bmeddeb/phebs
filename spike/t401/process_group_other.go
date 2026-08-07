//go:build !darwin && !linux

package t401

import (
	"errors"
	"os/exec"
)

func isolateProcessGroup(*exec.Cmd) error {
	return errors.New("T40.1 cancellation process groups require Linux or macOS")
}

func stopProcessGroup(int) error {
	return errors.New("T40.1 cancellation process groups require Linux or macOS")
}

func continueProcessGroup(int) error {
	return errors.New("T40.1 cancellation process groups require Linux or macOS")
}

func resumeProcesses([]int) error {
	return errors.New("T40.1 cancellation process groups require Linux or macOS")
}
