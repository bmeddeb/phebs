package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bmeddeb/phebs/spike/t451a"
	"github.com/bmeddeb/phebs/spike/t451a/launcher"
	"github.com/bmeddeb/phebs/spike/t451a/planner"
	"github.com/bmeddeb/phebs/spike/t451a/sandbox"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "__supervisor" {
		os.Exit(sandbox.Supervisor())
	}
	if len(os.Args) == 2 && os.Args[1] == "__probe_child" {
		if err := sandbox.ValidateWorker(); err != nil {
			os.Exit(1)
		}
		time.Sleep(2 * sandbox.WallLimit)
		os.Exit(1)
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) > 2 && os.Args[1] == "__gopackagesdriver" {
		if os.Getuid() != 65534 || os.Getpid() == 1 {
			return errors.New("package driver requires the isolated worker")
		}
		return launcher.RunEntrypoint(context.Background(), os.Args[2:])
	}
	if len(os.Args) > 2 && os.Args[1] == "__driver_bazel" {
		if os.Getuid() != 65534 || os.Getpid() == 1 {
			return errors.New("driver Bazel wrapper requires the isolated worker")
		}
		return launcher.RunBazel(context.Background(), os.Args[2:])
	}
	if len(os.Args) == 4 && os.Args[1] == "__plan_helper" {
		if os.Getuid() != 65534 || os.Getpid() == 1 {
			return errors.New("planner helper requires the isolated worker")
		}
		return planner.RunHelper(os.Args[2:])
	}
	if len(os.Args) == 2 && os.Args[1] == "__worker" {
		request, err := t451a.WorkerRequest()
		if err != nil {
			return err
		}
		if request.Mode == "probe" {
			return t451a.Probe(request.Probe)
		}
		return t451a.WorkerPlan(context.Background())
	}
	if len(os.Args) < 2 {
		return errors.New("usage: t451a plan|probe|recover [closed options]")
	}
	command := os.Args[1]
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	socket := flags.String("socket", "", "explicit local Docker Unix socket")
	image := flags.String("image", "", "immutable local Linux arm64 image ID")
	helper := flags.String("helper", "", "pinned Linux arm64 t451a executable")
	helperDigest := flags.String("helper-sha256", "", "exact helper digest")
	parent := flags.String("parent", "", "existing private run parent")
	probe := flags.String("probe", "access", "compiled-in neutral probe name")
	inputs := flags.String("inputs", "", "exact retained inputs directory for recovery")
	bundleRoot := flags.String("bundle", "", "offline tools tree (neutral plan only)")
	bundleManifest := flags.String("manifest", "", "canonical offline manifest file")
	bundleDigest := flags.String("bundle-sha256", "", "exact offline manifest digest")
	if err := flags.Parse(os.Args[2:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	switch command {
	case "plan", "probe":
		if *inputs != "" || *parent == "" {
			return errors.New("probe requires a private parent and no retained input")
		}
		request := t451a.Request{Schema: "phebs-t451a-request-v1", Profile: t451a.Profile, Mode: "probe", Probe: *probe,
			BundleSHA256: t451a.Digest(nil), HelperSHA256: *helperDigest, PlannerSHA256: *helperDigest, LauncherSHA256: *helperDigest, ImageID: *image}
		var manifest []byte
		if command == "plan" {
			var err error
			manifest, err = t451a.ReadManifest(*bundleManifest)
			if err != nil {
				return err
			}
			request.Mode, request.Probe, request.BundleSHA256 = "plan", "", *bundleDigest
		} else if *bundleRoot != "" || *bundleManifest != "" || *bundleDigest != "" {
			return errors.New("probes cannot import tools")
		}
		receipt, err := t451a.Run(ctx, t451a.Options{Socket: *socket, Parent: *parent, Helper: *helper, Request: request, BundleRoot: *bundleRoot, Manifest: manifest})
		return errors.Join(err, encoder.Encode(receipt))
	case "recover":
		result, err := sandbox.Recover(ctx, sandbox.Options{Socket: *socket, ImageID: *image, Inputs: *inputs})
		return errors.Join(err, encoder.Encode(struct {
			Removed bool `json:"removed"`
		}{result.Removed}))
	default:
		return errors.New("unknown command")
	}
}
