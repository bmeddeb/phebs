package t451b

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/bmeddeb/phebs/spike/t451a"
	"github.com/bmeddeb/phebs/spike/t451a/launcher"
	"github.com/bmeddeb/phebs/spike/t451a/planner"
)

const nativeSlotEnv = "PHEBS_T451B_NATIVE_SLOT"
const nativePlanEnv = "PHEBS_T451B_NATIVE_PLAN_SHA256"

type nativeControl struct {
	Version string                 `json:"version"`
	Slot    string                 `json:"slot"`
	Cohort  string                 `json:"cohort"`
	Plan    planner.Plan           `json:"plan"`
	Roots   []planner.Configured   `json:"roots"`
	GoFiles launcher.NativeGoFiles `json:"go_files"`
}

func nativeRoots(plan planner.Plan, cohort string) ([]planner.Configured, error) {
	labels, err := cohortRoots(cohort)
	if err != nil {
		return nil, err
	}
	roots := make([]planner.Configured, 0, len(labels))
	for _, label := range labels {
		count := 0
		for _, target := range plan.Targets {
			if target.Label == "@@"+label {
				roots = append(roots, target.Configured)
				count++
			}
		}
		if count != 1 {
			return nil, errors.New("native configured root absent or ambiguous")
		}
	}
	return roots, nil
}

func nativeSlotPaths(slot string) (string, string, error) {
	if _, _, err := slotPaths(slot); err != nil {
		return "", "", err
	}
	return "/scratch/t451b-native-" + slot + "-plan.json", "/scratch/t451b-native-" + slot + "-trace.json", nil
}

func nativeCallerEnvironment(p launcher.Prepared, slot, hash string) []string {
	env := callerEnvironment(p, slot, hash)
	for i, v := range env {
		switch {
		case strings.HasPrefix(v, "GOPACKAGESDRIVER="):
			env[i] = "GOPACKAGESDRIVER=" + NativeAdapterPath
		case strings.HasPrefix(v, slotEnv+"="):
			env[i] = nativeSlotEnv + "=" + slot
		case strings.HasPrefix(v, planEnv+"="):
			env[i] = nativePlanEnv + "=" + hash
		}
	}
	return env
}

func nativeControlBytes(plan planner.Plan, roots []planner.Configured, matches launcher.NativeGoFiles, cohort, slot string) ([]byte, error) {
	b, err := json.Marshal(nativeControl{"phebs-t451b-native-client-plan-v1", slot, cohort, plan, roots, matches})
	return append(b, '\n'), err
}

func inspectNativeCall(data []byte, hash string, argv, env []string, cwd string, request []byte) (nativeControl, launcher.Prepared, error) {
	var p launcher.Prepared
	c, err := decode[nativeControl](data, planner.MaxProtoBytes, false)
	if err != nil {
		return c, p, err
	}
	if c.Version != "phebs-t451b-native-client-plan-v1" || t451a.Digest(data) != hash {
		return c, p, errors.New("native control identity")
	}
	roots, err := nativeRoots(c.Plan, c.Cohort)
	if err != nil || !slices.Equal(roots, c.Roots) {
		return c, p, errors.New("native control roots")
	}
	p, err = launcher.PrepareNativeCompatibility(c.Plan, c.Roots, c.Slot, c.GoFiles)
	if err != nil {
		return c, p, err
	}
	expectedEnv := nativeCallerEnvironment(p, c.Slot, hash)
	if cwd != launcher.Workspace || !slices.Equal(argv, append([]string{NativeAdapterPath}, p.Invocation().Arguments...)) || !slices.Equal(env, expectedEnv) || len(request) > maxRequestBytes || !bytes.Equal(request, callerRequest(expectedEnv)) {
		return c, p, errors.New("native caller argv/environment/request differs from exact profile")
	}
	return c, p, nil
}

func RunNativeAdapter(ctx context.Context) error {
	if !typedindex.AdmittedNativeWorker() || os.Getuid() != 65534 || os.Getpid() == 1 {
		return errors.New("native adapter requires an admitted Linux worker")
	}
	planPath, tracePath, err := nativeSlotPaths(os.Getenv(nativeSlotEnv))
	if err != nil {
		return err
	}
	data, err := readBounded(planPath, planner.MaxProtoBytes)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, launcher.MaxWall)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = os.Stdin.Close() })
	defer stop()
	wire, err := io.ReadAll(io.LimitReader(os.Stdin, maxRequestBytes+1))
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	c, p, err := inspectNativeCall(data, os.Getenv(nativePlanEnv), os.Args, os.Environ(), cwd, wire)
	if err != nil {
		return err
	}
	trace, err := os.OpenFile(tracePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0400)
	if err != nil {
		return err
	}
	defer func() { _ = trace.Close() }()
	result, err := launcher.RunNativeCompatibility(ctx, c.Plan, c.Roots, c.Slot, c.GoFiles)
	if err != nil {
		return err
	}
	e := CallEvidence{Slot: c.Slot, PlanSHA256: t451a.Digest(data), Argv: os.Args, Environment: os.Environ(), Directory: cwd, Request: wire, RequestSHA256: t451a.Digest(wire), Launcher: p.Invocation(), LauncherSHA256: p.Digest(), Result: result, DriverResponseSHA256: t451a.Digest(result.DriverResponse), ResponseSHA256: t451a.Digest(result.Response)}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if len(b)+1 > maxTraceBytes {
		return errors.New("native trace byte limit")
	}
	if _, err = trace.Write(append(b, '\n')); err != nil {
		return err
	}
	_, err = os.Stdout.Write(result.Response)
	return err
}

func verifyNativeCall(plan planner.Plan, roots []planner.Configured, matches launcher.NativeGoFiles, cohort, slot string, call CallEvidence) error {
	b, err := nativeControlBytes(plan, roots, matches, cohort, slot)
	if err != nil {
		return err
	}
	_, p, err := inspectNativeCall(b, call.PlanSHA256, call.Argv, call.Environment, call.Directory, call.Request)
	if err != nil {
		return err
	}
	return verifyPreparedCall(p, slot, call)
}
