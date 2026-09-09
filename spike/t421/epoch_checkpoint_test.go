package t421

import (
	"bytes"
	"context"
	"encoding/json"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

func TestExecutionEpochCheckpointBounds(t *testing.T) {
	for _, mode := range []string{"valid", "v2", "missing", "phase6", "phase7", "phase8", "health"} {
		t.Run(mode, func(t *testing.T) {
			plan := Plan{Schema: PlanV3Schema, PhaseDeadlines: frozenPhaseDeadlines(), SafetyEnvelope: frozenSafetyEnvelope()}
			switch mode {
			case "v2":
				plan.Schema = PlanV2Schema
			case "missing":
				plan.PhaseDeadlines = nil
			case "phase6":
				plan.PhaseDeadlines[5].DeadlineMS++
			case "phase7":
				plan.PhaseDeadlines[6].DeadlineMS++
			case "phase8":
				plan.PhaseDeadlines[7].DeadlineMS++
			case "health":
				plan.SafetyEnvelope.ServerHealthDeadlineMS++
			}
			bounds, err := returnCheckpointEpochBounds(plan)
			if (err == nil) != (mode == "valid") {
				t.Fatal("changed frozen bound accepted", err)
			}
			if err == nil && (bounds.lifetime != 12*time.Hour || bounds.health != 15*time.Minute || bounds.controlPairs*128 != 2688 || bounds.outputBytes != 64<<20) {
				t.Fatal(bounds)
			}
		})
	}
}

func epochCheckpointTestPreparation(t *testing.T) (*executionEpochInspection, epochFinalResponse, extractionpublication.CheckpointRestartTransition) {
	t.Helper()
	reader, final, _ := epochStaleTestPreparation(t)
	reader.staleAuthority = reader.returnAuthority
	reader.staleAuthority.Phase = "stale_lease"
	reader.finalUsed = true
	if reader.beginCheckpoint() != nil {
		t.Fatal("checkpoint fixture phase")
	}
	var offset int
	var selected ExtractionRootResult
	for _, root := range reader.staleAuthority.ExtractionRoots {
		if root.Domain == "proto-contract" {
			selected = root
			break
		}
		offset += int(root.ApplicablePartitions)
	}
	part := selected.PartitionResults[2]
	prepared := epochStalePreparation{Schema: "t422-checkpoint-preparation-observation-v1", Authority: final.Authority, TargetGeneration: selected.GenerationSHA256,
		PriorSchedule: testDigest("checkpoint-prior"), Domain: selected.Domain, Ordinal: 2, Offset: offset + 2, PlanDigest: selected.PlanSHA256,
		ResultIdentity: part.ResultIdentitySHA256, ControlFileReads: 117, StoreReadAttempts: 24, StoreWriteAttempts: 1}
	prepared.RecoveryGeneration = SHA256([]byte("phebs-extraction-recovery-schedule-v1\x00" + prepared.TargetGeneration + "\x00" + prepared.PriorSchedule))
	var err error
	prepared.RecoverySchedule, err = store.GenerationScheduleDigest(store.GenerationScheduleSpec{Repository: reader.run.epoch.Repository,
		Stage: extractionpublication.ScheduleStage, Generation: prepared.RecoveryGeneration, ResourceClass: store.GenerationResourceExtraction,
		TotalItems: 56, ChunkItems: extractionpublication.ScheduleChunkItems, MaxAttempts: extractionpublication.ScheduleMaxAttempts, RepositoryTokens: extractionpublication.ScheduleRepositoryTokens})
	if err != nil || reader.validateRecoveryPreparation(prepared, true) != nil {
		t.Fatal("checkpoint preparation", err)
	}
	chunk, err := store.GenerationChunkIdentity(prepared.RecoverySchedule, int64(prepared.Offset), 0)
	if err != nil {
		t.Fatal(err)
	}
	hit := extractionpublication.CheckpointRestartTransition{Point: store.GenerationStaleLeaseTransitionCheckpointHit,
		TargetGeneration: prepared.TargetGeneration, ScheduleGeneration: prepared.RecoveryGeneration, PriorScheduleDigest: prepared.PriorSchedule,
		ScheduleDigest: prepared.RecoverySchedule, ChunkIdentity: chunk, Domain: prepared.Domain, Ordinal: prepared.Ordinal, PlanDigest: prepared.PlanDigest,
		ResultIdentity: prepared.ResultIdentity, ResultDigest: part.ResultDigestSHA256, ExpectationDigest: part.ExpectationSHA256, PartitionDigest: part.PartitionSHA256,
		CandidateGenerationDigest: selected.CandidateGenerationSHA256, SourceGenerationDigest: selected.SourceGenerationSHA256, ObservationGenerationDigest: selected.ObservationGenerationSHA256,
		ExtractorVersion: "native-test-version", ExtractionPolicyDigest: testDigest("native-test-policy"), ScheduleStatus: store.GenerationScheduleActive,
		Priority: store.GenerationPriorityNeverRun, ChunkStatus: store.GenerationChunkRunning, Leased: true, CanonicalResultExists: true, CompletionFileExists: true}
	reader.checkpointPreparation, reader.checkpointPrepared = prepared, true
	return reader, final, hit
}

func TestExecutionEpochCheckpointIdentityAndFinal(t *testing.T) {
	reader, final, hit := epochCheckpointTestPreparation(t)
	if reader.validateCheckpoint(hit, false) != nil {
		t.Fatal("actual-shaped hit refused")
	}
	reader.checkpointHit = hit
	recovered := hit
	recovered.Point, recovered.Priority, recovered.ChunkStatus, recovered.Leased = store.GenerationStaleLeaseTransitionRecovered, store.GenerationPriorityStale, store.GenerationChunkDone, false
	recovered.ScheduleStatus = store.GenerationScheduleSettled
	recovered.CompletionBitSet, recovered.RootExists, recovered.Current = true, true, true
	for _, root := range reader.staleAuthority.ExtractionRoots {
		if root.Domain == hit.Domain {
			recovered.RootDigest = root.RootSHA256
		}
	}
	if reader.validateCheckpoint(recovered, true) != nil {
		t.Fatal("actual-shaped recovery refused")
	}
	for _, change := range []func(*extractionpublication.CheckpointRestartTransition){
		func(v *extractionpublication.CheckpointRestartTransition) { v.ChunkIdentity = testDigest("wrong") },
		func(v *extractionpublication.CheckpointRestartTransition) { v.ResultDigest = testDigest("wrong") },
		func(v *extractionpublication.CheckpointRestartTransition) { v.Attempt++ },
		func(v *extractionpublication.CheckpointRestartTransition) {
			v.PrivateLeaseTokenDigest = testDigest("private")
		},
		func(v *extractionpublication.CheckpointRestartTransition) { v.CompletionBitSet = true },
	} {
		value := hit
		change(&value)
		if reader.validateCheckpoint(value, false) == nil {
			t.Fatal("changed hit accepted")
		}
	}
	for _, change := range []func(*extractionpublication.CheckpointRestartTransition){
		func(v *extractionpublication.CheckpointRestartTransition) { v.RootDigest = testDigest("wrong") },
		func(v *extractionpublication.CheckpointRestartTransition) { v.ResultDigest = testDigest("wrong") },
		func(v *extractionpublication.CheckpointRestartTransition) {
			v.ExtractionPolicyDigest = testDigest("wrong")
		},
		func(v *extractionpublication.CheckpointRestartTransition) {
			v.ScheduleStatus = store.GenerationScheduleActive
		},
		func(v *extractionpublication.CheckpointRestartTransition) { v.Leased = true },
	} {
		value := recovered
		change(&value)
		if reader.validateCheckpoint(value, true) == nil {
			t.Fatal("changed recovery accepted")
		}
	}
	reader.checkpointRecovered = recovered
	reader.tail = epochTailReadiness{Status: "ready", RelationshipGenerationSHA256: final.Authority.RelationshipGenerationSHA256,
		RelationshipRootSHA256: final.Authority.RelationshipRootSHA256, CallerGenerationSHA256: final.Authority.CallerGenerationSHA256, CallerRootSHA256: final.Authority.CallerRootSHA256}
	projection, _ := json.Marshal(reader.projection)
	if json.Unmarshal(projection, &final.Projection) != nil {
		t.Fatal("projection fixture")
	}
	final.Projection.Schema = "t421-final-state-projection-source-free-v1"
	if _, _, err := reader.decodeFinal(epochTestJSON(t, final, true)); err != nil {
		t.Fatal("full unchanged F", err)
	}
	final.ExtractionRoots[0].PartitionResults[0].ResultDigestSHA256 = testDigest("changed-native-result")
	if _, _, err := reader.decodeFinal(epochTestJSON(t, final, true)); err == nil {
		t.Fatal("changed detailed result accepted")
	}
}

func TestExecutionEpochCheckpointSemanticInput(t *testing.T) {
	reader, _, hit := epochCheckpointTestPreparation(t)
	reader.checkpointHit = hit
	handoff, err := reader.checkpointHandoff()
	if err != nil || handoff.Offset != reader.checkpointPreparation.Offset || handoff.Hit != hit {
		t.Fatal("actual handoff", err)
	}
	epoch := ExecutionEpochConfig{Epoch: 4, Repository: "example.com/mono", ConfigSHA256: testDigest("config")}
	raw, err := epochSemanticInput(testDigest("plan"), epoch, handoff)
	if err != nil || len(raw) > 16<<10 || !bytes.Contains(raw, []byte(`"checkpoint_recovery":{"prior":`)) || bytes.Contains(raw, []byte("private_lease")) {
		t.Fatal("canonical handoff", err, len(raw))
	}
	for _, number := range []uint64{1, 2} {
		epoch.Epoch = number
		ordinary, err := epochSemanticInput(testDigest("plan"), epoch, nil)
		want := struct {
			Schema       string `json:"schema"`
			Recipe       string `json:"recipe"`
			PlanSHA256   string `json:"plan_sha256"`
			ConfigSHA256 string `json:"config_sha256"`
			ServerEpoch  uint64 `json:"server_epoch"`
			Repository   string `json:"repository"`
		}{"t422-semantic-launch-v3", "t422-fixed-phase-control-v3", testDigest("plan"), epoch.ConfigSHA256, number, epoch.Repository}
		if err != nil || !bytes.Equal(ordinary, epochTestJSON(t, want, false)) {
			t.Fatal("old semantic bytes changed")
		}
		if _, err := epochSemanticInput(testDigest("plan"), epoch, handoff); err == nil {
			t.Fatal("wrong-epoch handoff")
		}
	}
	epoch.Epoch = 3
	if _, err := epochSemanticInput(testDigest("plan"), epoch, nil); err == nil {
		t.Fatal("return epoch lacks actual authored source")
	}
	epoch.ReturnSourceCommit = strings.Repeat("a", 40)
	if raw, err := epochSemanticInput(testDigest("plan"), epoch, nil); err != nil ||
		!bytes.Contains(raw, []byte(`"return_source_commit":"`+epoch.ReturnSourceCommit+`"`)) {
		t.Fatal("return source binding", err)
	}
	for _, invalid := range []string{strings.Repeat("A", 40), strings.Repeat("a", 64), "HEAD"} {
		epoch.ReturnSourceCommit = invalid
		if _, err := epochSemanticInput(testDigest("plan"), epoch, nil); err == nil {
			t.Fatal("invalid return source")
		}
	}
	epoch.Epoch = 4
	epoch.ReturnSourceCommit = strings.Repeat("a", 40)
	if _, err := epochSemanticInput(testDigest("plan"), epoch, handoff); err == nil {
		t.Fatal("return binding crossed epoch")
	}
	epoch.ReturnSourceCommit = ""
	if _, err := epochSemanticInput(testDigest("plan"), epoch, nil); err == nil {
		t.Fatal("unbound recovery")
	}
	epoch.Repository = strings.Repeat("x", 16<<10)
	if _, err := epochSemanticInput(testDigest("plan"), epoch, handoff); err == nil {
		t.Fatal("oversized input")
	}
}

func TestExecutionEpochCheckpointClosedPrefixes(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		result := ExecutionEpochOneResult{RootStarted: true, RootJoined: true, SessionEmpty: true}
		opened, ordinal := 4, uint64(7)
		if terminal {
			opened, ordinal = 3, 6
		}
		result.Store.Opened, result.Store.TerminalEOF = opened, opened
		result.Store.Store.Phase = 8
		for _, id := range []uint32{1, 2, 3, 4, 5, 7, 8, 9} {
			if terminal && id == 5 {
				continue
			}
			p := dispatchadmission.ProducerCount{Producer: id, Attached: true, Closed: true}
			if id == 1 {
				p.Closed, p.Ordinal = !terminal, ordinal
			}
			if id >= 7 {
				p.Ordinal = authorCustodyAttempts(int(id - 7))
			}
			if id == 4 {
				p.Checkpoint = 8
			}
			result.Accounting.Producers = append(result.Accounting.Producers, p)
		}
		for id := uint32(2); id <= uint32(opened+1); id++ {
			p := storeaccounting.ProducerCount{Producer: id, Attached: true, Closed: true}
			if id == 4 {
				p.Closed, p.TerminalFencedEOF, p.TerminalPhase = false, true, 8
				p.Checkpoint = 8
			}
			result.Store.Store.Producers = append(result.Store.Store.Producers, p)
		}
		if !epochCheckpointClosedPrefix(t.Context(), result, terminal) {
			t.Fatal("exact partial closure refused")
		}
		for _, change := range []func(*ExecutionEpochOneResult){
			func(v *ExecutionEpochOneResult) { v.SessionEmpty = false },
			func(v *ExecutionEpochOneResult) { v.Store.Store.Producers[2].Closed = true },
			func(v *ExecutionEpochOneResult) { v.Store.Store.Producers[2].TerminalFencedEOF = false },
			func(v *ExecutionEpochOneResult) { v.Accounting.Producers[0].Ordinal++ },
		} {
			copy := result
			copy.Store.Store.Producers = append([]storeaccounting.ProducerCount(nil), result.Store.Store.Producers...)
			copy.Accounting.Producers = append([]dispatchadmission.ProducerCount(nil), result.Accounting.Producers...)
			change(&copy)
			if epochCheckpointClosedPrefix(t.Context(), copy, terminal) {
				t.Fatal("invented closure accepted")
			}
		}
	}
}

func TestExecutionEpochCheckpointInitialDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		done := make(chan struct{})
		close(done)
		run := &ExecutionEpochOneRun{flow: &ExecutionEpochOne{plan: Plan{Schema: PlanV3Schema, PhaseDeadlines: frozenPhaseDeadlines(), SafetyEnvelope: frozenSafetyEnvelope()}},
			epoch: ExecutionEpochConfig{Epoch: 2}, stop: make(chan struct{}), done: make(chan struct{}), logicalUsed: true, logicalDone: done, inspection: &executionEpochInspection{}, phaseDeadline: time.Now().Add(20 * time.Hour)}
		started := time.Now()
		if next, err := run.StartReturnACheckpoint(t.Context()); next != nil || err == nil || time.Since(started) != 4*time.Hour {
			t.Fatal("phase6 borrowed later phase time", err, time.Since(started))
		}
	})
}

// Inspect actual call sites, not a detached copied context recipe. Runtime
// tests above cover the initial deadline; this binds successor/sole-Wait use.
func TestExecutionEpochCheckpointSourceBinding(t *testing.T) {
	fset := token.NewFileSet()
	render := func(node ast.Node) string {
		var b bytes.Buffer
		if format.Node(&b, fset, node) != nil {
			t.Fatal("format")
		}
		return strings.Join(strings.Fields(b.String()), " ")
	}
	file, err := parser.ParseFile(fset, "epoch_checkpoint.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var body *ast.BlockStmt
	for _, d := range file.Decls {
		if f, ok := d.(*ast.FuncDecl); ok && f.Name.Name == "checkpointRestart" {
			body = f.Body
		}
	}
	if body == nil {
		t.Fatal("missing actual method")
	}
	want := []string{"lifetime, cancel := context.WithDeadline(ctx, lifetimeDeadline)", "operation, finishOperation := context.WithDeadline(lifetime, deadline)", "run.setPhaseDeadlineLocked(deadline)", "close(operationDone)", "result, err := flow.launchEpoch(lifetime, operation, cancel, next, bounds, 4)"}
	index := 0
	ast.Inspect(body, func(n ast.Node) bool {
		if _, ok := n.(ast.Stmt); ok && index < len(want) && render(n) == want[index] {
			index++
		}
		return true
	})
	if index != len(want) {
		t.Fatalf("actual lifetime binding reordered at %s", want[index])
	}
	calls := map[string]int{}
	ast.Inspect(body, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			calls[render(c.Fun)]++
		}
		return true
	})
	if calls["run.Wait"] != 1 || calls["flow.parent.Checkpoint"] != 1 || calls["flow.store.ReopenAfterTerminalEOF"] != 1 || calls["flow.parent.ReopenAfterHardDeath"] != 1 || calls["flow.controller.Advance"] != 0 || calls["flow.epochs.author.authorNext"] != 0 {
		t.Fatal("same-phase successor changed native ordering", calls)
	}
	checkpointFile := file
	file, err = parser.ParseFile(fset, "epoch_launch.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range file.Decls {
		if f, ok := d.(*ast.FuncDecl); ok && f.Name.Name == "finish" {
			body = f.Body
		}
	}
	calls = map[string]int{}
	ast.Inspect(body, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			calls[render(c.Fun)]++
		}
		return true
	})
	if !reflect.DeepEqual([]int{calls["killExecutionProcessSession"], calls["run.flow.controller.CloseHardDeath"]}, []int{1, 1}) {
		t.Fatal("actual sole-Wait terminal branch missing", calls)
	}
	for name, want := range map[string]string{"CheckpointRestart": "return run.checkpointRestart(ctx, false)", "CheckpointRestartPressure": "return run.checkpointRestart(ctx, true)"} {
		found := false
		for _, d := range checkpointFile.Decls {
			if f, ok := d.(*ast.FuncDecl); ok && f.Name.Name == name {
				found = len(f.Body.List) == 1 && render(f.Body.List[0]) == want
			}
		}
		if !found {
			t.Fatal("checkpoint selection changed", name)
		}
	}
}

func TestExecutionEpochCheckpointRefusesUnavailable(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, run := range []*ExecutionEpochOneRun{nil, {}, {epoch: ExecutionEpochConfig{Epoch: 3}}} {
		if _, err := run.CheckpointRestart(ctx); err == nil {
			t.Fatal("unavailable restart")
		}
		if run.RecoverCheckpoint(ctx) == nil {
			t.Fatal("unavailable recovery")
		}
	}
}

func TestExecutionEpochCheckpointPhaseEightDeadlineAndRefusal(t *testing.T) {
	for _, mode := range []string{"unselected", "unfinished", "used", "canceled", "stopped", "expired", "closed_control"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				parent, child, err := dispatchadmission.NewPipe()
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = parent.Close(); _ = child.Close() }()
				control, err := dispatchadmission.NewPhaseControl(t.Context(), parent, [32]byte{1}, dispatchadmission.PhaseControlConfig{
					OwnerControl: true, TerminalPhase: 8, Phases: []uint32{6, 7, 8}, InitialPhase: 6, MaximumPhases: 3, MaximumWireBytes: 2688, Timeout: time.Second})
				if err != nil {
					t.Fatal(err)
				}
				_ = control.Close()
				reader, _, _ := epochCheckpointTestPreparation(t)
				reader.projection.Phase, reader.finalUsed = "stale_lease", true
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				run := &ExecutionEpochOneRun{flow: &ExecutionEpochOne{plan: reader.plan}, control: control, epoch: ExecutionEpochConfig{Epoch: 3}, stop: make(chan struct{}),
					checkpointAllowed: true, staleUsed: true, staleDone: make(chan struct{}), inspection: reader, lifetimeDeadline: time.Now().Add(3 * time.Second), cancelRun: func() {}}
				reader.run = run
				if mode != "unfinished" {
					close(run.staleDone)
				}
				run.setPhaseDeadlineLocked(time.Now().Add(time.Second))
				defer run.stopPhaseDeadline()
				switch mode {
				case "unselected":
					run.checkpointAllowed = false
				case "used":
					run.checkpointUsed = true
				case "canceled":
					cancel()
				case "stopped":
					run.stopOnce.Do(func() { close(run.stop) })
				case "expired":
					time.Sleep(2 * time.Second)
				}
				if next, err := run.CheckpointRestart(ctx); next != nil || err == nil {
					t.Fatal("invalid checkpoint start")
				}
				if mode == "closed_control" {
					if !run.checkpointUsed || run.err == nil || run.phaseDeadline != run.lifetimeDeadline || run.terminalEntered || run.returnStarting {
						t.Fatal("deadline before handoff/refusal join", run.err)
					}
					select {
					case <-run.checkpointDone:
					default:
						t.Fatal("operation not joined")
					}
					select {
					case <-run.returnStartDone:
					default:
						t.Fatal("outer handoff not joined")
					}
				} else if mode != "used" && run.checkpointUsed {
					t.Fatal("admission refusal consumed operation")
				}
			})
		})
	}
}
