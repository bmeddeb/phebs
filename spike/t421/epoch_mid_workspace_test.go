package t421

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Ownership-only fixtures: no joined process, SDK or native byte proof.
func midphaseParentModel(point uint8) *ExecutionEpochOneRun {
	epoch, next := uint64(2), 2
	if point == 0 {
		epoch = 1
	}
	if point == 2 {
		next = 3
	}
	flow := &ExecutionEpochOne{logicalUsed: true, returnUsed: true,
		epochs: &ExecutionEpochConfigCustody{active: true, released: epoch, author: &ExecutionAuthorCustody{next: next}}}
	run := &ExecutionEpochOneRun{flow: flow, epoch: ExecutionEpochConfig{Epoch: epoch}, returnStarting: point > 0,
		midphaseDeadline: time.Now().Add(time.Minute)}
	flow.retained, flow.epochs.author.borrowedBy = run, run
	run.result.RootStarted, run.result.RootJoined, run.result.SessionEmpty = true, true, true
	for i := uint8(0); i < point; i++ {
		run.result.ParentMidphaseSamples.Points[i].Attempts, run.result.ParentMidphaseSamples.Points[i].Completed = 1, 1
	}
	return run
}

func TestEpochMidphaseParentBoundary(t *testing.T) {
	for point := uint8(0); point < 3; point++ {
		for _, mode := range []string{"valid", "canceled", "unbounded", "renewed", "active_author", "borrow_lost", "successor", "not_joined", "duplicate", "unavailable", "missing_previous"} {
			t.Run(fmt.Sprint(point)+"/"+mode, func(t *testing.T) {
				run := midphaseParentModel(point)
				ctx, cancel := context.WithDeadline(t.Context(), run.midphaseDeadline)
				defer cancel()
				switch mode {
				case "canceled":
					cancel()
				case "unbounded":
					ctx = context.Background()
				case "renewed":
					run.midphaseDeadline = run.midphaseDeadline.Add(-time.Second)
				case "active_author":
					run.flow.epochs.author.active = true
				case "borrow_lost":
					run.flow.epochs.author.borrowedBy = nil
				case "successor":
					run.flow.epochs.released++
				case "not_joined":
					run.result.SessionEmpty = false
				case "duplicate":
					run.result.ParentMidphaseSamples.Points[point].Completed = 1
				case "unavailable":
					run.result.ParentMidphaseSamples.Unavailable = true
				case "missing_previous":
					if point == 0 {
						run.flow.logicalUsed = false
					} else {
						run.result.ParentMidphaseSamples.Points[point-1] = ExecutionWorkspaceBytePhase{}
					}
				}
				run.flow.mu.Lock()
				got := run.midphaseParentBoundaryLocked(ctx, point)
				run.flow.mu.Unlock()
				if got != (mode == "valid") {
					t.Fatal(mode, got)
				}
			})
		}
	}
}

// Actual bounded HTTP transport; bodies/prior authority are supplied fixtures,
// not authenticated native engine or physical-mutation proof.
func TestEpochMidphaseWorkspaceHTTP(t *testing.T) {
	for point := uint8(0); point < 4; point++ {
		for _, mode := range []string{"valid", "missing_final", "wrong_phase", "conflict", "excess", "duplicate"} {
			t.Run(fmt.Sprint(point)+"/"+mode, func(t *testing.T) {
				calls := 0
				reader := epochTestHTTPReader(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					name := "finish"
					if point == 0 {
						name = "start"
					}
					if r.Method != http.MethodPost || r.URL.Path != "/api/t422/lifecycle/sample-workspace" ||
						r.Header.Get("X-Phebs-T422-Workspace-Point") != name {
						t.Error("unexpected request")
					}
					if mode == "conflict" {
						w.WriteHeader(http.StatusConflict)
					}
					_, _ = fmt.Fprint(w, `{"logical_bytes":50,"allocated_bytes":100}`)
				}))
				reader.plan = accountingTestPlan(t)
				reader.run.flow = &ExecutionEpochOne{workspace: &productionRoot{}}
				reader.run.physicalAllowed, reader.run.checkpointAllowed = true, true
				reader.run.epoch.Epoch = []uint64{1, 1, 2, 3}[point]
				reader.projection.Phase = []string{"warm_noop", "physical_delta_b", "logical_delta_b", "return_a"}[point]
				reader.selectorCleanupPhase, reader.finalUsed, reader.retentionUsed = reader.projection.Phase, true, true
				reader.warmAuthority.Phase = "warm_noop"
				for i := range reader.run.result.ParentMidphaseSamples.Points {
					reader.run.result.ParentMidphaseSamples.Points[i].Attempts, reader.run.result.ParentMidphaseSamples.Points[i].Completed = 1, 1
				}
				reader.earlyFinishSamples.Phases[1].Completed = 1
				if point == 1 {
					reader.midphaseSamples.Points[0].Completed = 1
					reader.midphaseSamples.PostAuthor.Completed = 1
				}
				switch mode {
				case "missing_final":
					reader.finalUsed = false
				case "wrong_phase":
					reader.projection.Phase = "pressure_80"
				case "excess":
					reader.plan.SafetyEnvelope.MaximumDataAllocatedBytes = 99
				case "duplicate":
					reader.midphaseSamples.Points[point].Attempts = 1
				}
				err := reader.sampleMidphaseWorkspace(t.Context(), point)
				if (err == nil) != (mode == "valid") {
					t.Fatal(mode, err)
				}
				want := 0
				if mode == "valid" || mode == "conflict" || mode == "excess" {
					want = 1
				}
				if calls != want || reader.midphaseSamples.LimitExceeded != (mode == "excess") {
					t.Fatal(calls, reader.midphaseSamples)
				}
			})
		}
	}
}

// Real report parser over supplied completed values. Maxima deliberately occur
// at different endpoints, proving reconciliation never compares only finish.
func TestEpochMidphaseWorkspacePrefix(t *testing.T) {
	plan := accountingTestPlan(t)
	for _, producer := range []uint32{2, 3, 4} {
		for _, mode := range []string{"valid", "missing", "changed", "masked_start_allocated", "masked_finish_logical", "failed", "later_failure"} {
			var samples ExecutionMidphaseSamples
			first, end, phase := 0, 2, uint32(4)
			if producer == 3 {
				first, end, phase = 2, 3, 5
			}
			if producer == 4 {
				first, end, phase = 3, 4, 6
			}
			raw := workspaceTestBinding(producer)
			offset := uint64(0)
			if producer == 2 {
				raw += workspaceTestPair(2, 2, 1, 1, 1) + workspaceTestPair(2, 3, 2, 2, 2) + workspaceTestPair(2, 3, 3, 3, 3)
				offset = 3
			}
			for i := first; i < end; i++ {
				row := &samples.Points[i]
				row.Attempts, row.Completed = 1, 1
				row.Maximum.LogicalBytes, row.Maximum.AllocatedBytes = uint64(100-i), uint64(100+i)
				if mode != "missing" || i != end-1 {
					raw += workspaceTestPair(producer, phase, offset+uint64(i-first+1), row.Maximum.LogicalBytes, row.Maximum.AllocatedBytes)
				}
				if producer == 2 && i == 0 {
					samples.PostAuthor = ExecutionWorkspaceBytePhase{Attempts: 1, Completed: 1}
					samples.PostAuthor.Maximum.LogicalBytes, samples.PostAuthor.Maximum.AllocatedBytes = 75, 80
					raw += workspaceTestPair(2, 4, 5, 75, 80) + "WB1:2:4R:0000000000000005\n"
					offset++
				}
			}
			var stream ExecutionWorkspaceByteObservation
			for _, line := range strings.SplitAfter(raw, "\n") {
				if line == "" {
					continue
				}
				_, _ = observeWorkspaceByteEvent([]byte(line), plan, producer, "sha256:01"+strings.Repeat("00", 31), &stream)
			}
			stream.finish()
			switch mode {
			case "changed":
				samples.Points[first].Maximum.LogicalBytes++
			case "masked_start_allocated":
				samples.Points[first].Maximum.AllocatedBytes++
			case "masked_finish_logical":
				samples.Points[end-1].Maximum.LogicalBytes--
			case "failed":
				samples.Unavailable = true
			case "later_failure":
				samples.failIncomplete(producer)
			}
			if midphaseWorkspacePrefix(producer, stream, samples) != (mode == "valid" || mode == "later_failure") {
				t.Fatal(producer, mode, stream, samples)
			}
		}
	}
	if workspaceCheckpointMaximum(2, 4) != 3 || workspaceCheckpointMaximum(3, 5) != 1 || workspaceCheckpointMaximum(4, 6) != 1 ||
		5*(26+60)+26+2*79 != 614 {
		t.Fatal("fixed added sample/report envelope")
	}
}

// Supplied state exercises the actual owned snapshot seam used at both
// successor handoffs AND finish. No fabricated native join is claimed.
func TestEpochMidphaseParentPrefixTransfer(t *testing.T) {
	prior := midphaseParentModel(0)
	row := &prior.result.ParentMidphaseSamples.Points[0]
	row.Attempts, row.Completed, row.Maximum.LogicalBytes, row.Maximum.AllocatedBytes = 1, 1, 11, 22
	next := &ExecutionEpochOneRun{}
	next.result.ParentMidphaseSamples = prior.midphaseParentPrefix()
	terminal := ExecutionEpochOneResult{ParentMidphaseSamples: next.midphaseParentPrefix()}
	copy := next.midphaseParentPrefix()
	copy.Points[0].Maximum.LogicalBytes++
	if terminal.ParentMidphaseSamples.Points[0] != *row || next.midphaseParentPrefix() != terminal.ParentMidphaseSamples {
		t.Fatal("owned prefix lost or aliased")
	}
	// Prevent a fresh result literal from silently bypassing the tested seam.
	for _, check := range []struct{ file, method string }{{"epoch_launch.go", "finish"}, {"epoch_logical.go", "StartLogicalB"}, {"epoch_return.go", "startReturnA"}} {
		tree, err := parser.ParseFile(token.NewFileSet(), check.file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		calls := 0
		for _, declaration := range tree.Decls {
			method, ok := declaration.(*ast.FuncDecl)
			if !ok || method.Name.Name != check.method {
				continue
			}
			ast.Inspect(method.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if ok && selector.Sel.Name == "midphaseParentPrefix" {
					calls++
				}
				return true
			})
		}
		if calls != 1 {
			t.Fatal(check, "actual ownership seam missing", calls)
		}
	}
}

// Supplied stream values distinguish a later phase-four excess from the
// already completed warm-start value. The global stream still refuses.
func TestEpochMidphaseExcessDoesNotRelabelWarmStart(t *testing.T) {
	for _, mode := range []string{"later_logical", "later_allocated", "own_logical", "own_allocated"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			plan := accountingTestPlan(t)
			tap := newEpochWarmWorkspaceOutput(&checkoutCommandOutput{remaining: 64 << 20, cancel: cancel}, plan, [32]byte{1})
			if _, err := tap.Write([]byte(workspaceTestBinding(2) + workspaceTestPair(2, 2, 1, 11, 22))); err != nil || tap.arm() != nil {
				t.Fatal("cold prefix", err)
			}
			logical, allocated := uint64(33), uint64(44)
			switch mode {
			case "own_logical":
				logical = plan.WorkEnvelope.MaximumDataLogicalBytes + 1
			case "own_allocated":
				allocated = plan.SafetyEnvelope.MaximumDataAllocatedBytes + 1
			}
			_, err := tap.Write([]byte(workspaceTestPair(2, 3, 2, logical, allocated)))
			own := strings.HasPrefix(mode, "own_")
			if own != (err != nil) {
				t.Fatal("warm sample classification", err)
			}
			if !own {
				if _, err := tap.Write([]byte(workspaceTestPair(2, 3, 3, 55, 66))); err != nil {
					t.Fatal("warm finish", err)
				}
				laterLogical, laterAllocated := uint64(77), uint64(88)
				if mode == "later_logical" {
					laterLogical = plan.WorkEnvelope.MaximumDataLogicalBytes + 1
				} else {
					laterAllocated = plan.SafetyEnvelope.MaximumDataAllocatedBytes + 1
				}
				if _, err := tap.Write([]byte(workspaceTestPair(2, 4, 4, laterLogical, laterAllocated))); err == nil {
					t.Fatal("phase-four excess accepted")
				}
			}
			row, unavailable, exceeded := tap.snapshot()
			if row.Attempts != 1 || row.Completed != 1 || row.Maximum.LogicalBytes != logical || row.Maximum.AllocatedBytes != allocated ||
				unavailable || exceeded != own || !tap.observation.LimitExceeded || ctx.Err() == nil {
				t.Fatal("point-local excess lost or transferred", row, unavailable, exceeded)
			}
		})
	}
}
