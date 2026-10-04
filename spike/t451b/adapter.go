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

const slotEnv = "PHEBS_T451B_SLOT"
const planEnv = "PHEBS_T451B_PLAN_SHA256"

type control struct {
	Version string               `json:"version"`
	Slot    string               `json:"slot"`
	Plan    planner.Plan         `json:"plan"`
	Roots   []planner.Configured `json:"roots"`
}

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

func slotPaths(slot string) (string, string, error) {
	if slot != "load" && slot != "scip" {
		return "", "", errors.New("unknown compatibility slot")
	}
	return "/scratch/t451b-" + slot + "-plan.json", "/scratch/t451b-" + slot + "-trace.json", nil
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
	return append(env, "GOPACKAGESDRIVER="+AdapterPath, slotEnv+"="+slot, planEnv+"="+hash, "PWD="+launcher.Workspace)
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

func inspectCall(data []byte, hash string, argv, env []string, cwd string, request []byte) (control, launcher.Prepared, error) {
	var p launcher.Prepared
	c, err := decode[control](data, planner.MaxProtoBytes, false)
	if err != nil {
		return c, p, err
	}
	if c.Version != "phebs-t451b-client-plan-v1" || t451a.Digest(data) != hash {
		return c, p, errors.New("compatibility plan identity refused")
	}
	if _, _, err = slotPaths(c.Slot); err != nil {
		return c, p, err
	}
	p, err = launcher.PrepareCompatibility(c.Plan, c.Roots, c.Slot)
	if err != nil {
		return c, p, err
	}
	expectedEnv := callerEnvironment(p, c.Slot, hash)
	expectedArgv := append([]string{AdapterPath}, p.Invocation().Arguments...)
	if cwd != launcher.Workspace || !slices.Equal(argv, expectedArgv) || !slices.Equal(env, expectedEnv) || len(request) > maxRequestBytes || !bytes.Equal(request, callerRequest(expectedEnv)) {
		return c, p, errors.New("caller argv/environment/request differs from exact compatibility profile")
	}
	return c, p, nil
}

// RunAdapter is selected only by the exact compiled executable pathname.
// No client field is forwarded into the launcher after this complete comparison.
func RunAdapter(ctx context.Context) error {
	if !typedindex.AdmittedNativeWorker() || os.Getuid() != 65534 || os.Getpid() == 1 {
		return errors.New("compatibility adapter requires an admitted Linux worker")
	}
	planPath, tracePath, err := slotPaths(os.Getenv(slotEnv))
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
	c, p, err := inspectCall(data, os.Getenv(planEnv), os.Args, os.Environ(), cwd, wire)
	if err != nil {
		return err
	}
	// The trace reservation makes a second or concurrent discovery fail before
	// launching a second driver, even if the first client leg eventually fails.
	trace, err := os.OpenFile(tracePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0400)
	if err != nil {
		return err
	}
	defer func() { _ = trace.Close() }()
	result, err := launcher.RunCompatibility(ctx, c.Plan, c.Roots, c.Slot)
	if err != nil {
		return err
	}
	evidence := CallEvidence{Slot: c.Slot, PlanSHA256: t451a.Digest(data), Argv: os.Args, Environment: os.Environ(), Directory: cwd, Request: wire, RequestSHA256: t451a.Digest(wire), Launcher: p.Invocation(), LauncherSHA256: p.Digest(), Result: result, DriverResponseSHA256: t451a.Digest(result.DriverResponse), ResponseSHA256: t451a.Digest(result.Response)}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		return err
	}
	if len(encoded)+1 > maxTraceBytes {
		return errors.New("compatibility trace byte limit")
	}
	if _, err = trace.Write(append(encoded, '\n')); err != nil {
		return err
	}
	_, err = os.Stdout.Write(result.Response)
	return err
}

func closedFile(name string, data []byte) error {
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0400)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	return errors.Join(writeErr, f.Close())
}
