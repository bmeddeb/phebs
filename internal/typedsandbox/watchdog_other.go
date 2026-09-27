//go:build !linux && !darwin

package typedsandbox

import "time"

func InitializeWorkerProgress() error   { return ErrRefused }
func WorkerStage(string, time.Duration) {}
