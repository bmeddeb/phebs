package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/bmeddeb/phebs/internal/typedbazel/launcher"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/bmeddeb/phebs/internal/typedsandbox"
)

// DecodedResult owns its decoded bytes. It proves the returned protocol, not
// sandbox success, quiescence, current authority or permission to publish.
type DecodedResult struct {
	adapted    []byte
	receipt    typedindex.SCIPGoAdapterReceipt
	invocation Invocation
	result     Result
	plan       typedindex.PackagePlan
	valid      bool
}

func (r DecodedResult) Plan() typedindex.PackagePlan { return r.plan }
func (r DecodedResult) Failure() *Failure {
	if r.result.Failure == nil {
		return nil
	}
	f := *r.result.Failure
	if f.PlanningScratch != nil {
		v := *f.PlanningScratch
		f.PlanningScratch = &v
	}
	if f.PlanningProcess != nil {
		v := *f.PlanningProcess
		f.PlanningProcess = &v
	}
	if f.FinalProcess != nil {
		v := *f.FinalProcess
		f.FinalProcess = &v
	}
	return &f
}

// DecodeResult is host-only pure evidence admission. selection must be the
// exact authenticated inventory member; worker JSON cannot choose this input.
// A valid failure returns bounded diagnostic custody and a nonnil closed error.
func DecodeResult(ctx context.Context, i Invocation, selection, raw []byte) (DecodedResult, error) {
	r, err := decodeResult(ctx, i, selection, raw)
	return r, classify(err)
}
func decodeResult(ctx context.Context, i Invocation, selection, raw []byte) (DecodedResult, error) {
	var empty DecodedResult
	if err := i.validate(ctx); err != nil {
		return empty, err
	}
	if len(raw) == 0 || int64(len(raw)) > int64(typedsandbox.OutputBytes)-i.Allowance.WorkerBytesUsed {
		return empty, typedindex.Capacity
	}
	if err := resultDimensions(ctx, raw); err != nil {
		return empty, err
	}
	s, err := bindSelection(ctx, i, selection)
	if err != nil {
		return empty, err
	}
	r, err := decode[Result](raw, typedsandbox.OutputBytes)
	if err != nil {
		return empty, err
	}
	request := i.Parent.Digest()
	if i.Phase == typedindex.Execute {
		request = i.Execution.Digest()
	}
	if r.Schema != "phebs-bazel-worker-result-v1" || r.RequestDigest != request || r.Phase != i.Phase {
		return empty, typedindex.Stale
	}
	if r.Failure != nil {
		if err = validateFailure(r.Failure); err != nil {
			return empty, err
		}
		want := Result{Schema: r.Schema, RequestDigest: r.RequestDigest, Phase: r.Phase, Failure: r.Failure, Plan: json.RawMessage("null")}
		if !reflect.DeepEqual(r, want) {
			return empty, typedindex.Invalid
		}
		return DecodedResult{result: r}, r.Failure.Reason
	}
	roots, err := configuredRoots(r.RawPlan, s)
	if err != nil {
		return empty, err
	}
	for _, sdk := range r.RawPlan.SDKs {
		if sdk.Root != nativeSDKRoot || sdk.Version != "1.25.0" {
			return empty, typedindex.Unsupported
		}
	}
	if _, err = launcher.PrepareNativeCompatibility(r.RawPlan, roots, "load", r.GoFiles); err != nil {
		return empty, err
	}
	p, docs, units, err := mapPlan(ctx, i, s, r.RawPlan, roots)
	if err != nil {
		return empty, err
	}
	if !bytes.Equal(p.Bytes(), r.Plan) || !reflect.DeepEqual(docs, r.Documents) || !reflect.DeepEqual(units, r.Units) {
		return empty, typedindex.Stale
	}
	rawPaths := make(map[string]bool, len(docs))
	for _, d := range docs {
		if rawPaths[d.RawPath] {
			return empty, typedindex.Invalid
		}
		rawPaths[d.RawPath] = true
	}
	var adapted []byte
	var receipt typedindex.SCIPGoAdapterReceipt
	if i.Phase == typedindex.Plan {
		if len(r.Legs) != 0 || len(r.SCIP) != 0 || r.SCIPSHA256 != "" || len(r.Generated) != 0 {
			return empty, typedindex.Invalid
		}
	} else {
		if !samePlan(i.Plan, p) || len(r.Legs) != 2 {
			return empty, typedindex.Stale
		}
		var legWall int64
		for j, slot := range []string{"load", "scip"} {
			leg := r.Legs[j]
			prepared, e := launcher.PrepareNativeCompatibility(r.RawPlan, roots, slot, r.GoFiles)
			if e != nil {
				return empty, e
			}
			control, e := nativeControlBytes(r.RawPlan, roots, r.GoFiles, s, slot)
			if e != nil {
				return empty, e
			}
			env := callerEnvironment(prepared, slot, hash(control))
			argv := append([]string{NativeProbePath}, prepared.Invocation().Arguments...)
			if slot == "scip" {
				argv = append([]string{SCIPPath}, scipArguments(s, prepared.Invocation().Arguments)...)
			}
			if leg.Slot != slot || !slices.Equal(leg.ClientArgv, argv) || !slices.Equal(leg.Environment, env) || leg.WallNanoseconds < 0 || leg.WallNanoseconds > int64(typedsandbox.WallLimit)-legWall || len(leg.Stdout) > maxClientBytes || len(leg.Stderr) > maxClientBytes-len(leg.Stdout) {
				return empty, typedindex.Invalid
			}
			legWall += leg.WallNanoseconds
			callBytes, e := json.Marshal(leg.Call)
			if e != nil || len(callBytes)+1 > maxTraceBytes || len(leg.Call.Request) > maxRequestBytes {
				return empty, typedindex.Capacity
			}
			for _, protocol := range [][]byte{leg.Call.Result.DriverResponse, leg.Call.Result.Response} {
				if e = resultDimensions(ctx, protocol); e != nil {
					return empty, e
				}
			}
			if slot == "load" {
				if e = resultDimensions(ctx, leg.Stdout); e != nil {
					return empty, e
				}
			}
			if e = verifyNativeCall(r.RawPlan, roots, r.GoFiles, s, slot, leg.Call); e != nil {
				return empty, e
			}
			if slot == "load" {
				if e = verifyNativeProbe(leg.Stdout, leg.Call.Result.Response); e != nil {
					return empty, e
				}
			}
		}
		adaptedMembers, audit, adaptErr := typedindex.AdaptSCIPGoBlanks(ctx, i.Profile, [][]byte{r.SCIP})
		if adaptErr != nil {
			return empty, adaptErr
		}
		adapted, receipt = adaptedMembers[0], audit
		if r.SCIPSHA256 != hash(r.SCIP) {
			return empty, typedindex.Stale
		}
		if err = verifySCIP(r.SCIP, docs, s, r.Legs[1].Call.Launcher.Arguments); err != nil {
			return empty, err
		}
		for _, b := range r.Generated {
			if b == nil {
				return empty, typedindex.Invalid
			}
		}
		byDocument := make(map[string]string, len(docs))
		for _, d := range docs {
			byDocument[d.Document] = d.Path
		}
		payloads := make(map[string][]byte, len(r.Generated))
		for _, d := range r.RawPlan.Documents {
			if p := byDocument[d.ID]; p != "" {
				payloads[launcher.ExecRoot+"/"+d.ExecPath] = r.Generated[p]
			}
		}
		generated, err := generatedBytes(ctx, docs, r.RawPlan, func(name string, limit int64) ([]byte, error) { return payloads[name], nil })
		if err != nil || !reflect.DeepEqual(generated, r.Generated) {
			return empty, typedindex.Stale
		}
	}
	return DecodedResult{invocation: i, result: r, plan: p, valid: true, adapted: adapted, receipt: receipt}, nil
}

func validateFailure(f *Failure) error {
	switch f.Stage {
	case "profile", "tool-verification", "materialization", "compiler-setup", "planning", "workspace-verification", "go-file-sealing", "package-load/typecheck", "indexer-unlocalized", "validation":
	default:
		return typedindex.Invalid
	}
	switch f.Reason {
	case typedindex.Invalid, typedindex.Unsupported, typedindex.Stale, typedindex.Capacity, typedindex.Unprepared, typedindex.WallLimit, typedindex.ExecutionFailed, typedindex.Containment, typedindex.Canceled:
	default:
		return typedindex.Invalid
	}
	if f.PlanningScratch != nil && (!f.PlanningScratch.Available && (f.PlanningScratch.FreeBlocks != 0 || f.PlanningScratch.FreeInodes != 0) || f.Stage != "planning") {
		return typedindex.Invalid
	}
	if f.FinalProcess != nil && !f.FinalQuiescenceFailed {
		return typedindex.Invalid
	}
	for _, d := range []*ProcessDiagnostic{f.PlanningProcess, f.FinalProcess} {
		if d == nil {
			continue
		}
		if len(d.Comm) == 0 || len(d.Comm) > 64 || !utf8.ValidString(d.Comm) || strings.ContainsRune(d.Comm, 0) || len(d.State) != 1 || !strings.ContainsAny(d.State, "RSDZTtXxKWPI") || d.PPID > 1<<31-1 || d.PGID > 1<<31-1 || d.SID > 1<<31-1 || len(d.CapPrm) != 16 || strings.Trim(d.CapPrm, "0123456789abcdef") != "" {
			return typedindex.Invalid
		}
	}
	return nil
}
