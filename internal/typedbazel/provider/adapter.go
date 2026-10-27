package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/bmeddeb/phebs/internal/typedbazel/launcher"
	"github.com/bmeddeb/phebs/internal/typedbazel/planner"
	"github.com/bmeddeb/phebs/internal/typedindex"
)

type CallEvidence struct {
	Slot                 string                       `json:"slot"`
	PlanSHA256           string                       `json:"plan_sha256"`
	Argv                 []string                     `json:"argv"`
	Environment          []string                     `json:"environment"`
	Directory            string                       `json:"directory"`
	Request              []byte                       `json:"request"`
	RequestSHA256        string                       `json:"request_sha256"`
	Launcher             launcher.Invocation          `json:"launcher"`
	LauncherSHA256       string                       `json:"launcher_sha256"`
	Result               launcher.CompatibilityResult `json:"result"`
	DriverResponseSHA256 string                       `json:"driver_response_sha256"`
	ResponseSHA256       string                       `json:"response_sha256"`
}

func callerEnvironment(p launcher.Prepared, slot, hash string) []string {
	env := launcher.BazelEnvironment()
	env[0] = "PATH=/inputs/tools/go/bin:/inputs/tools/bin"
	// Only these two sealed mode values cross from the driver profile to callers.
	for _, item := range p.Invocation().Environment {
		if strings.HasPrefix(item, "CGO_ENABLED=") || strings.HasPrefix(item, "GOTAGS=") {
			env = append(env, item)
		}
	}
	return append(env, "GOPACKAGESDRIVER="+NativeAdapterPath, nativeSlotEnv+"="+slot, nativePlanEnv+"="+hash, "PWD="+launcher.Workspace)
}

func callerRequest(env []string) []byte {
	data, _ := json.Marshal(struct {
		Mode       int               `json:"mode"`
		Env        []string          `json:"env"`
		BuildFlags []string          `json:"build_flags"`
		Tests      bool              `json:"tests"`
		Overlay    map[string][]byte `json:"overlay"`
	}{Mode: launcher.CompatibilityMode, Env: env})
	return data
}

const nativeSlotEnv = "PHEBS_T451B_NATIVE_SLOT"
const nativePlanEnv = "PHEBS_T451B_NATIVE_PLAN_SHA256"

type nativeControl struct {
	Version   string                 `json:"version"`
	Slot      string                 `json:"slot"`
	Selection Selection              `json:"selection"`
	Plan      planner.Plan           `json:"plan"`
	Roots     []planner.Configured   `json:"roots"`
	GoFiles   launcher.NativeGoFiles `json:"go_files"`
}

func nativeSlotPaths(slot string) (string, string, error) {
	if slot != "load" && slot != "scip" {
		return "", "", errors.New("unknown native slot")
	}
	return "/scratch/t451b-native-" + slot + "-plan.json", "/scratch/t451b-native-" + slot + "-trace.json", nil
}

func nativeControlBytes(plan planner.Plan, roots []planner.Configured, matches launcher.NativeGoFiles, selection Selection, slot string) ([]byte, error) {
	b, err := json.Marshal(nativeControl{"phebs-bazel-client-plan-v1", slot, selection, plan, roots, matches})
	return append(b, '\n'), err
}

func inspectNativeCall(data []byte, expectedHash string, argv, env []string, cwd string, request []byte) (nativeControl, launcher.Prepared, error) {
	var p launcher.Prepared
	c, err := decode[nativeControl](data, planner.MaxProtoBytes)
	if err != nil {
		return c, p, err
	}
	if c.Version != "phebs-bazel-client-plan-v1" || hash(data) != expectedHash {
		return c, p, errors.New("native control identity")
	}
	roots, err := configuredRoots(c.Plan, c.Selection)
	if err != nil || !slices.Equal(roots, c.Roots) {
		return c, p, errors.New("native control roots")
	}
	p, err = launcher.PrepareNativeCompatibility(c.Plan, c.Roots, c.Slot, c.GoFiles)
	if err != nil {
		return c, p, err
	}
	expectedEnv := callerEnvironment(p, c.Slot, expectedHash)
	if cwd != launcher.Workspace || !slices.Equal(argv, append([]string{NativeAdapterPath}, p.Invocation().Arguments...)) || !slices.Equal(env, expectedEnv) || len(request) > maxRequestBytes || !bytes.Equal(request, callerRequest(expectedEnv)) {
		return c, p, errors.New("native caller argv/environment/request differs from exact profile")
	}
	return c, p, nil
}

func RunAdapter(ctx context.Context) error {
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
	e := CallEvidence{Slot: c.Slot, PlanSHA256: hash(data), Argv: os.Args, Environment: os.Environ(), Directory: cwd, Request: wire, RequestSHA256: hash(wire), Launcher: p.Invocation(), LauncherSHA256: p.Digest(), Result: result, DriverResponseSHA256: hash(result.DriverResponse), ResponseSHA256: hash(result.Response)}
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

func verifyNativeCall(plan planner.Plan, roots []planner.Configured, matches launcher.NativeGoFiles, selection Selection, slot string, call CallEvidence) error {
	b, err := nativeControlBytes(plan, roots, matches, selection, slot)
	if err != nil {
		return err
	}
	_, p, err := inspectNativeCall(b, call.PlanSHA256, call.Argv, call.Environment, call.Directory, call.Request)
	if err != nil {
		return err
	}
	return verifyPreparedCall(p, slot, call)
}
