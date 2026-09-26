package launcher

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"runtime"
	"slices"

	"github.com/bmeddeb/phebs/spike/t451a/planner"
)

const (
	EntrypointPath        = "/inputs/t451a"
	EntrypointCommand     = "__gopackagesdriver"
	EntrypointWrapperPath = "/scratch/phebs-gopackagesdriver"
	EntrypointWrapper     = "#!/bin/sh\nexec /inputs/t451a __gopackagesdriver \"$@\"\n"
	EntrypointPlanPath    = "/scratch/driver-plan.json"
	EntrypointDigestEnv   = "PHEBS_DRIVER_PLAN_SHA256"
	maxRequestBytes       = 4096
)

type entrypointPlan struct {
	Version string               `json:"version"`
	Plan    planner.Plan         `json:"plan"`
	Roots   []planner.Configured `json:"roots"`
}

// RunThroughEntrypoint exercises the actual owned GOPACKAGESDRIVER endpoint.
// The imported runner image and its fixed wrapper supply executable identity;
// the request and patterns come only from Prepare's sealed configured roots.
func RunThroughEntrypoint(ctx context.Context, plan planner.Plan, roots []planner.Configured) ([]byte, error) {
	p, err := Prepare(plan, roots)
	if err != nil {
		return nil, err
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "arm64" || os.Getuid() != 65534 {
		return nil, errors.New("driver endpoint requires admitted Linux arm64 worker")
	}
	data, err := json.Marshal(entrypointPlan{"phebs-t451a-driver-plan-v1", plan, roots})
	if err != nil {
		return nil, err
	}
	if len(data) > planner.MaxProtoBytes {
		return nil, errors.New("driver endpoint plan byte bound")
	}
	if err := writeClosedFile(EntrypointPlanPath, data, 0400); err != nil {
		return nil, err
	}
	if err := writeClosedFile(EntrypointWrapperPath, []byte(EntrypointWrapper), 0500); err != nil {
		return nil, err
	}
	environment := append(BazelEnvironment(), EntrypointDigestEnv+"="+hash(data), "GOPACKAGESDRIVER="+EntrypointWrapperPath)
	output, err := runClosedChild(ctx, EntrypointPath, append([]string{EntrypointCommand}, p.patterns...), environment, p.request)
	if err != nil {
		return nil, err
	}
	// Run checks documents before and after the real driver. The outer caller
	// additionally verifies response equality against its own sealed plan.
	if err := Reconcile(p, output); err != nil {
		return nil, err
	}
	return output, nil
}

func inspectEntrypoint(data []byte, want string, args []string, request []byte) (entrypointPlan, error) {
	var payload entrypointPlan
	if len(data) == 0 || len(data) > planner.MaxProtoBytes || len(want) != 64 || hash(data) != want {
		return payload, errors.New("driver endpoint plan byte/digest mismatch")
	}
	if err := uniqueJSON(data); err != nil {
		return payload, err
	}
	if err := planner.ValidateJSONFields(data, &payload); err != nil {
		return payload, err
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return payload, err
	}
	canonical, err := json.Marshal(payload)
	if err != nil {
		return payload, err
	}
	if !bytes.Equal(data, canonical) || payload.Version != "phebs-t451a-driver-plan-v1" {
		return payload, errors.New("noncanonical driver endpoint plan")
	}
	p, err := Prepare(payload.Plan, payload.Roots)
	if err != nil {
		return payload, err
	}
	if !slices.Equal(args, p.patterns) || len(request) > maxRequestBytes || !bytes.Equal(bytes.TrimSpace(request), p.request) {
		return payload, errors.New("driver endpoint argv/request differs from closed profile")
	}
	return payload, nil
}

// RunEntrypoint handles only the fixed runner's __gopackagesdriver command.
// It never accepts a plan path, executable, environment or selector override.
func RunEntrypoint(ctx context.Context, args []string) error {
	ctx, cancel := context.WithTimeout(ctx, MaxWall)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = os.Stdin.Close() })
	defer stop()
	data, err := readFile(EntrypointPlanPath, planner.MaxProtoBytes)
	if err != nil {
		return err
	}
	request, err := io.ReadAll(io.LimitReader(os.Stdin, maxRequestBytes+1))
	if err != nil {
		return err
	}
	payload, err := inspectEntrypoint(data, os.Getenv(EntrypointDigestEnv), args, request)
	if err != nil {
		return err
	}
	output, err := Run(ctx, payload.Plan, payload.Roots, DriverSHA256)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(output)
	return err
}
