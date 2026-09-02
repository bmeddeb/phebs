//go:build !darwin

package t4110

import (
	"context"
	"errors"
)

type phaseProcessSampler struct{}

func startPhaseProcessSampler(context.Context) (*phaseProcessSampler, error) {
	return nil, errors.New("T41.10 live measurement requires a Darwin host")
}

func (*phaseProcessSampler) stop() (int64, error) {
	return 0, errors.New("T41.10 live measurement requires a Darwin host")
}

func remainingProcessChildren(context.Context) (int, error) {
	return 0, errors.New("T41.10 live measurement requires a Darwin host")
}

func diskUsage(string) (int64, int64, error) {
	return 0, 0, errors.New("T41.10 live measurement requires a Darwin host")
}
