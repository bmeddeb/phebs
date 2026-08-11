//go:build !darwin && !linux

package t4013

import (
	"errors"
	"os"
)

func allocatePressureFile(*os.File, int64) error {
	return errors.New("T40.13 pressure ballast allocation is unsupported")
}
