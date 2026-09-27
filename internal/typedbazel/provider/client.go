package provider

import (
	"context"
	"time"

	"github.com/bmeddeb/phebs/internal/typedbazel/launcher"
	"github.com/bmeddeb/phebs/internal/typedbazel/planner"
	"github.com/bmeddeb/phebs/internal/typedindex"
)

type LegEvidence struct {
	Slot            string       `json:"slot"`
	ClientArgv      []string     `json:"client_argv"`
	Environment     []string     `json:"environment"`
	WallNanoseconds int64        `json:"wall_nanoseconds"`
	Stdout          []byte       `json:"stdout"`
	Stderr          []byte       `json:"stderr"`
	Call            CallEvidence `json:"call"`
}

func scipArguments(s Selection, patterns []string) []string {
	return append([]string{"index", "--module-root=" + launcher.Workspace, "--module-path=" + s.Module, "--module-version=" + s.Source.Commit, "--repository-remote=" + s.Remote, "--go-version=go1.25.0", "--skip-tests", "--skip-implementations", "--output=/scratch/t451b-native-index.scip"}, patterns...)
}
func runLeg(ctx context.Context, plan planner.Plan, roots []planner.Configured, matches launcher.NativeGoFiles, s Selection, slot string) (LegEvidence, error) {
	var leg LegEvidence
	p, e := launcher.PrepareNativeCompatibility(plan, roots, slot, matches)
	if e != nil {
		return leg, e
	}
	data, e := nativeControlBytes(plan, roots, matches, s, slot)
	if e != nil || len(data) > planner.MaxProtoBytes {
		return leg, typedindex.Capacity
	}
	planPath, tracePath, e := nativeSlotPaths(slot)
	if e != nil {
		return leg, e
	}
	env := callerEnvironment(p, slot, hash(data))
	if len(callerRequest(env)) > maxRequestBytes {
		return leg, typedindex.Capacity
	}
	if e = closedFile(planPath, data); e != nil {
		return leg, e
	}
	exe, args := NativeProbePath, p.Invocation().Arguments
	if slot == "scip" {
		exe, args = SCIPPath, scipArguments(s, args)
	}
	started := time.Now()
	stdout, stderr, clientErr := runClient(ctx, exe, args, env)
	raw, e := readBounded(tracePath, maxTraceBytes)
	if e != nil {
		return leg, e
	}
	call, e := decode[CallEvidence](raw, maxTraceBytes)
	if e != nil {
		return leg, e
	}
	if e = verifyNativeCall(plan, roots, matches, s, slot, call); e != nil {
		return leg, e
	}
	if e = launcher.VerifyNativeCompatibilityFiles(ctx, plan, roots, slot, matches, call.Result.Exports); e != nil {
		return leg, e
	}
	if clientErr != nil {
		return leg, clientErr
	}
	if slot == "load" {
		if e = verifyNativeProbe(stdout, call.Result.Response); e != nil {
			return leg, e
		}
	}
	return LegEvidence{slot, append([]string{exe}, args...), env, time.Since(started).Nanoseconds(), stdout, stderr, call}, nil
}
