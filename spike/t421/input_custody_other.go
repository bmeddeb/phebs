//go:build !darwin

package t421

import (
	"errors"
	"os"
)

func inputCustodyOwned(os.FileInfo) bool             { return false }
func inputCustodyProtected(os.FileInfo) bool         { return false }
func inputCustodySame(os.FileInfo, os.FileInfo) bool { return false }

func inputCustodyFlag(*os.File, bool) error {
	return errors.New("input custody requires Darwin file protection")
}

func inputCustodyVolume(*os.File) ([2]int32, error) {
	return [2]int32{}, errors.New("input custody requires Darwin file protection")
}
