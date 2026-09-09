//go:build !darwin && !linux

package t421

import (
	"context"
	"errors"
	"os/exec"
)

func runReferenceCommand(context.Context, *exec.Cmd) error {
	return errors.New("reference preparation session control is unsupported on this host")
}

func prepareReferenceCommand(*exec.Cmd) error {
	return errors.New("reference build child-group control is unsupported on this host")
}
