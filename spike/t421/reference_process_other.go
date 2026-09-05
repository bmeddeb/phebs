//go:build !darwin && !linux

package t421

import (
	"errors"
	"os/exec"
)

func prepareReferenceCommand(*exec.Cmd) error {
	return errors.New("reference build child-group control is unsupported on this host")
}
