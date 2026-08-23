//go:build !darwin && !linux

package t4013

import (
	"errors"
	"os"
	"os/exec"
)

func isolatePrivateServerSession(*exec.Cmd) error {
	return errors.New("T40.13 private process sessions require Linux or macOS")
}

func interruptPrivateServerRoot(*os.Process) (bool, error) {
	return false, errors.New("T40.13 private process sessions require Linux or macOS")
}

func killPrivateServerSession(int) error {
	return errors.New("T40.13 private process sessions require Linux or macOS")
}

func privateServerSessionAlive(int) (bool, error) {
	return false, errors.New("T40.13 private process sessions require Linux or macOS")
}

func expectedPrivateServerInterrupt(error) bool { return false }
