//go:build linux && t45_native_cost_helper

package main

import (
	"os"
	"runtime"
	"syscall"

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
	// SIGSTOP stops the whole worker thread group before dispatch. The root
	// acceptance owner must authenticate this stopped lifetime and arm the
	// private-namespace sampler before resuming; the absolute allowance still runs.
	if err := syscall.Kill(os.Getpid(), syscall.SIGSTOP); err != nil {
		return 125
	}
	handled, code := runTypedCommand()
	if !handled || code != 0 {
		return 125
	}
	// Successful production dispatch already joined every tool and verified
	// final quiescence/workspace custody. No new writer or child follows it.
	cache, err := t451b.ObserveManagedPrivateCache()
	if err != nil {
		return 125
	}
	raw, err := t451b.EncodeManagedCache(t451b.ManagedCache{Schema: t451b.ManagedCacheSchema, Cache: cache})
	if err != nil {
		return 125
	}
	if n, err := os.Stderr.Write(raw); err != nil || n != len(raw) {
		return 125
	}
	// Keep this same worker alive for the root owner's final coherent sample.
	// A cache frame alone cannot authenticate successful native completion.
	if err := syscall.Kill(os.Getpid(), syscall.SIGSTOP); err != nil {
		return 125
	}
	return 0
}
