//go:build !darwin && !linux

package ownedpipe

import (
	"errors"
	"os"
)

func New() (parent, child *os.File, err error) {
	return nil, nil, errors.New("owned socket pairs unavailable")
}
