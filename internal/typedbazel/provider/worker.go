package provider

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/bmeddeb/phebs/internal/typedbazel/launcher"
	"github.com/bmeddeb/phebs/internal/typedbazel/planner"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/bmeddeb/phebs/internal/typedsandbox"
)

// Run executes only inside the already verified worker namespace. The returned
// bytes are private evidence subject to the shared supervisor output allowance.
// Neither a successful run nor its raw SCIP authorizes publication.
func Run(ctx context.Context, i Invocation) ([]byte, error) {
	if e := i.validate(ctx); e != nil {
		return nil, classify(e)
	}
	if e := typedsandbox.ValidateWorker(); e != nil {
		return nil, typedindex.Containment
	}
	ctx, cancel, e := typedsandbox.AllowanceContext(ctx, i.Allowance)
	if e != nil {
		return nil, typedindex.WallLimit
	}
	defer cancel()
	if e = typedsandbox.InitializeWorkerProgress(); e != nil {
		return nil, typedindex.Containment
	}
	b, e := runValidated(ctx, i, nativeOperations())
	return b, classify(e)
}

// workerOperations is a private neutral-test seam. Every production entry is
// fixed here; no caller can supply command execution or filesystem behavior.
type workerOperations struct {
	readSelection func(typedindex.Inventory, string, int64) ([]byte, error)
	tools         func(context.Context, Invocation) error
	materialize   func(context.Context, Invocation) ([]original, error)
	compiler      func(context.Context, typedindex.Inventory) error
	plan          func(context.Context, []string) (planner.Plan, error)
	verify        func(context.Context, planner.Plan, []original) error
	sdk           func(planner.Plan) error
	rules         func(context.Context, typedindex.Inventory) error
	files         func(context.Context, planner.Plan, []planner.Configured) (launcher.NativeGoFiles, error)
	leg           func(context.Context, planner.Plan, []planner.Configured, launcher.NativeGoFiles, Selection, string) (LegEvidence, error)
	read          func(string, int64) ([]byte, error)
	quiesce       func() error
}

func nativeOperations() workerOperations {
	return workerOperations{readInventory, verifyTools, materialize, setupCompiler, nativePlan, verifyWorkspace, verifySDK, verifyRules, launcher.SealNativeGoFiles, runLeg, readBounded, ensureQuiescentWorker}
}
func run(ctx context.Context, i Invocation, o workerOperations) (raw []byte, err error) {
	if e := i.validate(ctx); e != nil {
		return nil, e
	}
	return runValidated(ctx, i, o)
}

func runValidated(ctx context.Context, i Invocation, o workerOperations) (raw []byte, err error) {
	started := time.Now()
	currentStage := "profile"
	failure := &Failure{}
	stage := func(s string) { currentStage = s; typedsandbox.WorkerStage(s, time.Since(started)) }
	defer func() {
		if err == nil {
			return
		}
		failure.Stage = currentStage
		failure.Reason = reason(err)
		var command *PlanningCommandError
		if errors.As(err, &command) {
			failure.PlanningScratch = &command.Scratch
		}
		var q *QuiescenceError
		if errors.As(err, &q) {
			failure.PlanningProcess = q.Process
		}
		r := Result{Schema: "phebs-bazel-worker-result-v1", RequestDigest: i.Parent.Digest(), Phase: i.Phase, Failure: failure}
		if i.Phase == typedindex.Execute {
			r.RequestDigest = i.Execution.Digest()
		}
		raw, _ = json.Marshal(r)
		if int64(len(raw))+1 > int64(typedsandbox.OutputBytes)-i.Allowance.WorkerBytesUsed {
			raw = nil
		} else {
			raw = append(raw, '\n')
		}
	}()
	stage("profile")
	b, e := o.readSelection(i.Inventory, SelectionFile, MaxSelectionBytes)
	if e != nil {
		return nil, e
	}
	selection, e := bindSelection(ctx, i, b)
	if e != nil {
		return nil, e
	}
	stage("tool-verification")
	if e = o.tools(ctx, i); e != nil {
		return nil, e
	}
	stage("materialization")
	originals, e := o.materialize(ctx, i)
	if e != nil {
		return nil, e
	}
	planned := false
	defer func() {
		if e := o.quiesce(); e != nil {
			var q *QuiescenceError
			if errors.As(e, &q) {
				failure.FinalProcess = q.Process
			}
			failure.FinalQuiescenceFailed = true
			if err == nil {
				err = typedindex.Containment
			}
			return
		}
		if planned {
			if e := o.rules(ctx, i.Inventory); e != nil {
				failure.FinalVerificationFailed = true
				if err == nil {
					err = e
				}
			}
		}
		if e := o.verify(ctx, planner.Plan{}, originals); e != nil {
			failure.FinalVerificationFailed = true
			if err == nil {
				err = e
			}
		}
	}()

	stage("compiler-setup")
	if e = o.compiler(ctx, i.Inventory); e != nil {
		return nil, e
	}
	stage("planning")
	plan, e := o.plan(ctx, selection.Roots)
	if e != nil {
		return nil, e
	}
	planned = true
	stage("workspace-verification")
	if e = o.rules(ctx, i.Inventory); e != nil {
		return nil, e
	}
	if e = o.verify(ctx, plan, originals); e != nil {
		return nil, e
	}
	if e = o.sdk(plan); e != nil {
		return nil, e
	}
	roots, e := configuredRoots(plan, selection)
	if e != nil {
		return nil, e
	}
	stage("go-file-sealing")
	matches, e := o.files(ctx, plan, roots)
	if e != nil {
		return nil, e
	}
	// Structural native preparation validates all raw seals and selected closures
	// even during planning; driver output cannot supply the final authority.
	if _, e = launcher.PrepareNativeCompatibility(plan, roots, "load", matches); e != nil {
		return nil, e
	}
	sealed, documents, units, e := mapPlan(ctx, i, selection, plan, roots)
	if e != nil {
		return nil, e
	}
	result := Result{Schema: "phebs-bazel-worker-result-v1", RequestDigest: i.Parent.Digest(), Phase: i.Phase, Plan: sealed.Bytes(), RawPlan: plan, GoFiles: matches, Documents: documents, Units: units, Legs: []LegEvidence{}, SCIP: []byte{}}
	if i.Phase == typedindex.Execute {
		if !samePlan(sealed, i.Plan) {
			return nil, typedindex.Stale
		}
		result.RequestDigest = i.Execution.Digest()
		for _, slot := range []string{"load", "scip"} {
			if e = ctx.Err(); e != nil {
				return nil, e
			}
			if slot == "load" {
				stage("package-load/typecheck")
			} else {
				stage("indexer-unlocalized")
			}
			leg, e := o.leg(ctx, plan, roots, matches, selection, slot)
			if e != nil {
				return nil, e
			}
			result.Legs = append(result.Legs, leg)
			if slot == "load" {
				if e = o.rules(ctx, i.Inventory); e != nil {
					return nil, e
				}
			}
		}
		stage("validation")
		result.SCIP, e = o.read("/scratch/t451b-native-index.scip", typedindex.MaxSCIPMemberBytes)
		if e != nil {
			return nil, e
		}
		if e = verifySCIP(result.SCIP, documents, selection, result.Legs[1].Call.Launcher.Arguments); e != nil {
			return nil, e
		}
		result.SCIPSHA256 = hash(result.SCIP)
		result.Generated, e = generatedBytes(ctx, documents, plan, o.read)
		if e != nil {
			return nil, e
		}
	}
	if e = ctx.Err(); e != nil {
		return nil, e
	}
	raw, e = json.Marshal(result)
	if e != nil {
		return nil, e
	}
	remaining := int64(typedsandbox.OutputBytes) - i.Allowance.WorkerBytesUsed
	return encodeResultWire(ctx, raw, remaining)
}
