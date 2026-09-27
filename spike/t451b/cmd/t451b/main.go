package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/bmeddeb/phebs/spike/t451a/launcher"
	"github.com/bmeddeb/phebs/spike/t451a/planner"
	"github.com/bmeddeb/phebs/spike/t451a/sandbox"
	"github.com/bmeddeb/phebs/spike/t451b"
)

func main() {
	if os.Args[0] != t451b.AdapterPath && os.Args[0] != t451b.NativeAdapterPath && len(os.Args) == 2 {
		switch os.Args[1] {
		case "__supervisor":
			os.Exit(sandbox.Supervisor())
		case "__native_supervisor":
			os.Exit(sandbox.SupervisorNativeT451b())
		}
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	if os.Args[0] == t451b.NativeAdapterPath {
		return t451b.RunNativeAdapter(context.Background())
	}
	if os.Args[0] == t451b.AdapterPath {
		return t451b.RunAdapter(context.Background())
	}
	if len(os.Args) > 3 && os.Args[1] == "__native_bazel" {
		if os.Getuid() != 65534 || os.Getpid() == 1 {
			return errors.New("native Bazel requires isolated worker")
		}
		return launcher.RunNativeCompatibilityBazel(context.Background(), os.Args[2], os.Args[3:])
	}
	if len(os.Args) > 3 && os.Args[1] == "__compat_bazel" {
		if os.Getuid() != 65534 || os.Getpid() == 1 {
			return errors.New("compatibility Bazel requires isolated worker")
		}
		return launcher.RunCompatibilityBazel(context.Background(), os.Args[2], os.Args[3:])
	}
	if len(os.Args) == 4 && os.Args[1] == "__plan_helper" {
		if os.Getuid() != 65534 || os.Getpid() == 1 {
			return errors.New("planner helper requires isolated worker")
		}
		return planner.RunHelper(os.Args[2:])
	}
	if len(os.Args) == 2 && os.Args[1] == "__worker" {
		r, err := t451b.WorkerRequest()
		if err != nil {
			return err
		}
		return t451b.Worker(context.Background(), r)
	}
	if len(os.Args) == 2 && os.Args[1] == "__native_worker" {
		r, err := t451b.NativeWorkerRequest()
		if err != nil {
			return err
		}
		return t451b.NativeWorker(context.Background(), r)
	}
	if len(os.Args) == 3 && os.Args[1] == "native" {
		c, err := t451b.ReadNativeConfig(os.Args[2])
		if err != nil {
			return err
		}
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		_, err = t451b.RunNativeConfig(ctx, c)
		return err
	}
	if len(os.Args) != 3 || os.Args[1] != "compatibility" {
		return errors.New("usage: t451b compatibility /absolute/closed-config.json")
	}
	c, err := t451b.ReadConfig(os.Args[2])
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	_, err = t451b.RunConfig(ctx, c)
	return err
}
