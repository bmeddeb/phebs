//go:build !linux

package t451b

import (
	"context"
	"errors"
)

func StartManagedObservations(context.Context) (func() (Observations, error), error) {
	return nil, errors.New("managed observations require Linux procfs")
}

func ObserveManagedPrivateCache() (PrivateCacheObservation, error) {
	return PrivateCacheObservation{}, errors.New("managed cache observations require Linux")
}
