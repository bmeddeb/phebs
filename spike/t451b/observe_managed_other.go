//go:build !linux

package t451b

import (
	"context"
	"errors"
	"io/fs"
)

func StartManagedObservations(context.Context) (func() (Observations, error), error) {
	return nil, errors.New("managed observations require Linux procfs")
}

func StartRootManagedObservations(context.Context, fs.FS, uint32) (func() (Observations, error), error) {
	return nil, errors.New("root managed observations require Linux procfs")
}

func ObserveManagedPrivateCache() (PrivateCacheObservation, error) {
	return PrivateCacheObservation{}, errors.New("managed cache observations require Linux")
}
