package t451b

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/bmeddeb/phebs/spike/t451a"
	"github.com/bmeddeb/phebs/spike/t451a/launcher"
	"github.com/bmeddeb/phebs/spike/t451a/planner"
	"github.com/bmeddeb/phebs/spike/t451a/sandbox"
)

func selectedSDKProfile() toolProfile {
	return toolProfile{launcher.ExecRoot + "/" + nativeSDKRoot + "/bin/go", GoDigest, "cmd/go", false}
}

func validateSelectedSDK(plan planner.Plan, tool ToolIdentity) error {
	if len(plan.SDKs) == 0 {
		return errors.New("native selected SDK absent")
	}
	for _, sdk := range plan.SDKs {
		if sdk.Root != nativeSDKRoot || sdk.Version != "1.25.0" {
			return errors.New("native SDK provider differs")
		}
	}
	return validateToolProfiles([]toolProfile{selectedSDKProfile()}, []ToolIdentity{tool})
}

// NativeWorker is called only after the existing sandbox worker validation.
// Every normal error writes partial typed STOP evidence before returning it.
func NativeWorker(ctx context.Context, r NativeRequest) (err error) {
	e := NativeEvidence{Version: "phebs-t451b-native-evidence-v1", Request: r, Decision: "STOP", Stage: "profile"}
	started := time.Now()
	stageStarted := started
	setStage := func(stage string) {
		now := time.Now()
		e.Timings = append(e.Timings, NativeTiming{e.Stage, now.Sub(stageStarted).Nanoseconds()})
		stageStarted = now
		e.Stage = stage
	}
	var stop func() (Observations, error)
	var originals map[string][]byte
	defer func() {
		setStage(e.Stage)
		measurementStarted := time.Now()
		var planningRefusal *t451a.QuiescenceError
		if errors.As(err, &planningRefusal) {
			e.PlanningQuiescenceProcess = planningRefusal.Process
		}
		if stop != nil {
			var observationErr error
			e.Observations, observationErr = stop()
			recordNativeFailure(&e, &err, "containment/measurement", observationErr)
		}
		// Reuse the bounded private process census before exact cache accounting.
		cacheErr := t451a.EnsureQuiescentWorker()
		var finalRefusal *t451a.QuiescenceError
		if errors.As(cacheErr, &finalRefusal) {
			e.FinalQuiescenceProcess = finalRefusal.Process
		}
		if cacheErr == nil {
			if originals != nil {
				var finalPlan planner.Plan
				if e.Plan != nil {
					finalPlan = *e.Plan
				}
				sourceErr := verifyNativeWorkspace(finalPlan, originals)
				e.Materialization.OriginalsUnchanged = sourceErr == nil
				e.Materialization.ExcludedControlAbsent = sourceErr == nil && e.Plan != nil
				recordNativeFailure(&e, &err, "validation", sourceErr)
			}
			e.Cache, cacheErr = ObservePrivateCache()
		}
		recordNativeFailure(&e, &err, "containment/measurement", cacheErr)
		if err == nil {
			e.Stage = "complete"
			e.Decision = "COHORT_OBSERVED"
		}
		e.Timings = append(e.Timings, NativeTiming{"containment/measurement", time.Since(measurementStarted).Nanoseconds()})
		e.WallNanoseconds = time.Since(started).Nanoseconds()
		data, encodeErr := json.Marshal(e)
		if encodeErr == nil && len(data)+1 > sandbox.OutputBytes {
			recordNativeFailure(&e, &err, "containment/measurement", errors.New("native evidence aggregate output limit"))
			e.OmittedEvidenceBytes, e.OmittedEvidenceSHA256 = len(data), t451a.Digest(data)
			e.Decision = "STOP"
			e.Plan, e.GoFiles, e.SelectedSDK, e.Legs, e.SCIP, e.SCIPSHA256, e.Oracle = nil, nil, nil, nil, nil, "", nil
			if e.FailedLeg != nil {
				e.FailedLeg.Call = nil
				e.FailedLeg.Protocol = "unproven"
			}
			data, encodeErr = json.Marshal(e)
			if len(data)+1 > sandbox.OutputBytes {
				encodeErr = errors.New("native reduced evidence output limit")
			}
		}
		if encodeErr == nil {
			_, encodeErr = os.Stdout.Write(append(data, '\n'))
		}
		err = errors.Join(err, encodeErr)
	}()
	if _, err = DecodeNativeRequest(nativeRequestBytes(r)); err != nil {
		return err
	}
	stop, err = StartObservations(ctx)
	if err != nil {
		return err
	}
	e.Tools, err = readToolProfiles(nativeToolProfiles(r))
	if err != nil {
		return err
	}
	e.Materialization, originals, err = materializeNative(r)
	if err != nil {
		return err
	}
	if err = t451a.SetupCompiler(); err != nil {
		return err
	}
	setStage("planning")
	labels, _ := cohortRoots(r.Cohort)
	plan, evicted, err := t451a.BuildNativePlan(ctx, labels, r.Cohort == "neutral")
	if err != nil {
		return err
	}
	e.Plan = &plan
	e.CompilerCacheEviction = evicted
	setStage("profile")
	if r.Cohort == "neutral" {
		lock, readErr := readBounded(launcher.Workspace+"/MODULE.bazel.lock", planner.MaxFileBytes)
		if readErr != nil {
			return readErr
		}
		originals["MODULE.bazel.lock"] = lock
		e.Materialization.PlanningLockSHA256 = t451a.Digest(lock)
	}
	if err = verifyNativeWorkspace(plan, originals); err != nil {
		return err
	}
	identities, err := readToolProfiles([]toolProfile{selectedSDKProfile()})
	if err != nil {
		return err
	}
	e.SelectedSDK = &identities[0]
	if err = validateSelectedSDK(plan, *e.SelectedSDK); err != nil {
		return err
	}
	roots, err := nativeRoots(plan, r.Cohort)
	if err != nil {
		return err
	}
	matches, err := launcher.SealNativeGoFiles(ctx, plan, roots)
	if err != nil {
		return err
	}
	e.GoFiles = &matches
	if r.Cohort == "neutral" {
		if err = validateNativeNeutral(plan, matches); err != nil {
			return err
		}
	}
	for _, slot := range []string{"load", "scip"} {
		stage := "package-load/typecheck"
		if slot == "scip" {
			stage = "indexer-unlocalized"
		}
		setStage(stage)
		leg, failed, err := runNativeLeg(ctx, plan, roots, matches, r.Cohort, slot)
		if err != nil {
			e.FailedLeg = failed
			return err
		}
		e.Legs = append(e.Legs, leg)
	}
	setStage("validation")
	e.SCIP, err = readBounded("/scratch/t451b-native-index.scip", maxClientBytes)
	if err != nil {
		return err
	}
	e.SCIPSHA256 = t451a.Digest(e.SCIP)
	facts, err := verifyNativeSCIP(e.SCIP, plan, e.Legs[1].Call.Result.Response, r.Cohort, e.Legs[1].Call.Launcher.Arguments)
	if err != nil {
		return err
	}
	e.Oracle = &facts
	return nil
}

func validateNativeNeutral(plan planner.Plan, matches launcher.NativeGoFiles) error {
	var inactive string
	for _, d := range plan.Documents {
		if d.Kind == "source" && d.Path == "lib/inactive_windows.go" {
			if inactive != "" {
				return errors.New("ambiguous native inactive document")
			}
			inactive = d.ID
		}
	}
	if inactive == "" {
		return errors.New("native inactive declared document absent")
	}
	units := map[string]planner.Unit{}
	for _, u := range plan.Units {
		units[u.ID] = u
	}
	docs := map[string]planner.Document{}
	for _, d := range plan.Documents {
		docs[d.ID] = d
	}
	rows := map[string][]string{}
	for _, row := range matches.Packages {
		rows[row.ID] = row.GoFiles
	}
	for _, t := range plan.Targets {
		if t.Label != "@@//lib:alias" && t.Label != "@@//proto:message_go" && t.Label != "@@//cgo:cgo" {
			continue
		}
		if len(t.Units) != 1 {
			return errors.New("native proof root units")
		}
		u := units[t.Units[0]]
		switch t.Label {
		case "@@//lib:alias":
			if !slices.Contains(u.GoFiles, inactive) || slices.Contains(u.CompiledGoFiles, inactive) || slices.Contains(rows[u.ArchiveLabel], launcher.Workspace+"/lib/inactive_windows.go") {
				return errors.New("native inactive source was lost or became active")
			}
		case "@@//proto:message_go", "@@//cgo:cgo":
			generated := false
			for _, id := range u.CompiledGoFiles {
				generated = generated || docs[id].Kind == "generated"
			}
			if !generated || t.Label == "@@//cgo:cgo" && !u.Mode.Cgo {
				return errors.New("native proto/cgo proof missing configured generated source")
			}
		}
	}
	return nil
}

func runNativeLeg(ctx context.Context, plan planner.Plan, roots []planner.Configured, matches launcher.NativeGoFiles, cohort, slot string) (LegEvidence, *NativeFailedLeg, error) {
	var leg LegEvidence
	p, err := launcher.PrepareNativeCompatibility(plan, roots, slot, matches)
	if err != nil {
		return leg, nil, err
	}
	data, err := nativeControlBytes(plan, roots, matches, cohort, slot)
	if err != nil {
		return leg, nil, err
	}
	if len(data) > planner.MaxProtoBytes {
		return leg, nil, errors.New("native client control byte bound")
	}
	planPath, tracePath, err := nativeSlotPaths(slot)
	if err != nil {
		return leg, nil, err
	}
	env := nativeCallerEnvironment(p, slot, t451a.Digest(data))
	if len(callerRequest(env)) > maxRequestBytes {
		return leg, nil, errors.New("native client request ceiling")
	}
	if err = closedFile(planPath, data); err != nil {
		return leg, nil, err
	}
	executable, args := NativeProbePath, p.Invocation().Arguments
	if slot == "scip" {
		executable, args = SCIPPath, nativeSCIPArguments(cohort, args)
	}
	started := time.Now()
	stdout, stderr, clientErr := runClient(ctx, executable, args, env)
	// Even a failed client may have completed its own distinct driver call.
	// Missing, partial or invalid trace bytes prove no protocol or target cause.
	raw, readErr := readBounded(tracePath, maxTraceBytes)
	call, traceErr := nativeCallFromTrace(plan, roots, matches, cohort, slot, raw, readErr)
	if clientErr != nil || traceErr != nil {
		point := "protocol"
		if clientErr != nil {
			point = "client"
		}
		failed := nativeLegFailure(slot, point, clientErr != nil, started, stdout, stderr, call)
		return leg, failed, fmt.Errorf("native %s leg refused: %w: %.4096s", slot, errors.Join(clientErr, traceErr), stderr)
	}
	if err = launcher.VerifyNativeCompatibilityFiles(ctx, plan, roots, slot, matches, call.Result.Exports); err != nil {
		return leg, nativeLegFailure(slot, "files", false, started, stdout, stderr, call), err
	}
	if slot == "load" {
		if err = verifyNativeProbe(stdout, call.Result.Response); err != nil {
			return leg, nativeLegFailure(slot, "probe", false, started, stdout, stderr, call), err
		}
	}
	return LegEvidence{slot, append([]string{executable}, args...), env, time.Since(started).Nanoseconds(), stdout, stderr, *call}, nil, nil
}

func nativeCallFromTrace(plan planner.Plan, roots []planner.Configured, matches launcher.NativeGoFiles, cohort, slot string, raw []byte, readErr error) (*CallEvidence, error) {
	if readErr != nil {
		return nil, readErr
	}
	call, err := decode[CallEvidence](raw, maxTraceBytes, false)
	if err != nil {
		return nil, err
	}
	if err = verifyNativeCall(plan, roots, matches, cohort, slot, call); err != nil {
		return nil, err
	}
	return &call, nil
}

// Only failure paths hash client output; success keeps its existing raw evidence.
func nativeLegFailure(slot, point string, clientError bool, started time.Time, stdout, stderr []byte, call *CallEvidence) *NativeFailedLeg {
	protocol := "unproven"
	if call != nil {
		protocol = "completed"
	}
	return &NativeFailedLeg{Slot: slot, Point: point, ClientError: clientError, Protocol: protocol, WallNanoseconds: time.Since(started).Nanoseconds(), StdoutBytes: len(stdout), StdoutSHA256: t451a.Digest(stdout), StderrBytes: len(stderr), StderrSHA256: t451a.Digest(stderr), Call: call}
}

// Preserve the first substantiated failing phase; accounting still retains its
// own unavailable/incomplete facts when a second failure follows.
func recordNativeFailure(e *NativeEvidence, prior *error, stage string, next error) {
	if next == nil {
		return
	}
	if *prior == nil {
		e.Stage = stage
	}
	*prior = errors.Join(*prior, next)
}
