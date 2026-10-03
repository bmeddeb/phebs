//go:build linux && t45_native_cost_helper

package main

import (
	"context"
	"os"
	"runtime"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/typedsandbox"
	"github.com/bmeddeb/phebs/spike/t451b"
)

// This explicitly tagged validation helper retains the genuine cmd/phebs build
// main and the production dispatcher. Untagged builds contain none of this
// hook, observer import or cost work. All non-worker roles continue to main.
func init() {
	if len(os.Args) < 2 || os.Args[0] != typedsandbox.HelperPath || os.Args[1] != typedsandbox.WorkerCommand || runtime.GOARCH != "arm64" || os.Getuid() != 65534 || os.Getgid() != 65534 || os.Getpid() == 1 {
		return
	}
	if _, selected := os.LookupEnv(dispatchadmission.ProductionEnvironment); selected {
		return
	}
	os.Exit(runMeasuredTypedWorker())
}

func runMeasuredTypedWorker() int {
	stop, err := t451b.StartManagedObservations(context.Background())
	if err != nil {
		return measuredTypedStop("observer_start", nil)
	}
	handled, code := runTypedCommand()
	observations, err := stop()
	if err != nil {
		return measuredTypedStop("observations", &observations)
	}
	if !handled || code != 0 {
		return measuredTypedStop("worker", &observations)
	}
	// Successful production dispatch already joined every tool and verified
	// final quiescence/workspace custody. No new writer or child follows it.
	cache, err := t451b.ObserveManagedPrivateCache()
	if err != nil {
		return measuredTypedStop("cache", &observations)
	}
	raw, err := t451b.EncodeManagedCost(t451b.ManagedCost{Schema: t451b.ManagedCostSchema, Observations: observations, Cache: cache})
	if err != nil {
		return measuredTypedStop("cost_encode", &observations)
	}
	if n, err := os.Stderr.Write(raw); err != nil || n != len(raw) {
		return 125
	}
	return 0
}

func measuredTypedStop(stage string, observations *t451b.Observations) int {
	raw, err := t451b.EncodeManagedCostStop(t451b.ManagedCostStop{Schema: t451b.ManagedCostStopSchema, Stage: stage, Observations: observations})
	if err == nil {
		_, _ = os.Stderr.Write(raw)
	}
	return 125
}
