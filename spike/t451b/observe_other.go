//go:build !linux

package t451b

import (
	"context"
	"errors"
)

func StartObservations(context.Context) (func() (Observations, error), error) {
	return nil, errors.New("target observations require Linux procfs")
}
