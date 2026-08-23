//go:build !darwin && !linux

package t4013

import (
	"errors"
	"os"
)

func openNoFollowRegular(string) (*os.File, error) {
	return nil, errors.New("T40.13 bounded exact inspection requires Linux or macOS")
}

func openNoFollowDirectory(string) (*os.File, error) {
	return nil, errors.New("T40.13 bounded exact inspection requires Linux or macOS")
}
