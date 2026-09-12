package t421

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// Supplied terminal rows exercise the exact failed-run classification seam;
// they establish no native traversal or physical-phase outcome.
func TestEpochEarlyWorkspaceLaterFailureKeepsCompletedPrefix(t *testing.T) {
	for _, mode := range []string{"completed", "missing_warm", "failed_sample", "excess"} {
		t.Run(mode, func(t *testing.T) {
			samples := ExecutionEarlyFinishSamples{}
			for index := range samples.Phases {
				samples.Phases[index].Attempts, samples.Phases[index].Completed = 1, 1
				samples.Phases[index].Maximum.LogicalBytes = uint64(index + 1)
				samples.Phases[index].Maximum.AllocatedBytes = uint64(index + 2)
			}
			switch mode {
			case "missing_warm":
				samples.Phases[1] = ExecutionWorkspaceBytePhase{}
			case "failed_sample":
				samples.Unavailable = true
			case "excess":
				samples.LimitExceeded = true
			}
			prior := samples
			samples.failIncomplete()
			if samples.Phases != prior.Phases || samples.LimitExceeded != prior.LimitExceeded ||
				samples.Unavailable != (mode == "missing_warm" || mode == "failed_sample") {
				t.Fatal("later failure changed completed evidence", prior, samples)
			}
		})
	}
}

// Supplied wire totals prove framing and closed fixed counts, not traversal.
func TestEpochEarlyWorkspaceFinishPrefix(t *testing.T) {
	plan := accountingTestPlan(t)
	input := "sha256:01" + strings.Repeat("00", 31)
	for _, mode := range []string{"valid", "missing", "gap", "duplicate", "wrong_phase", "changed_http", "changed_http_allocated", "changed_start_logical", "changed_start_allocated", "failed_http"} {
		t.Run(mode, func(t *testing.T) {
			raw := workspaceTestBinding(2) + workspaceTestPair(2, 2, 1, 11, 22)
			switch mode {
			case "missing":
			case "gap":
				raw += workspaceTestPair(2, 3, 3, 33, 44)
			case "duplicate":
				raw += workspaceTestPair(2, 2, 2, 33, 44)
			case "wrong_phase":
				raw += workspaceTestPair(2, 4, 2, 33, 44)
			default:
				raw += workspaceTestPair(2, 3, 2, 55, 12) + workspaceTestPair(2, 3, 3, 33, 44)
			}
			var out ExecutionWorkspaceByteObservation
			for _, line := range strings.SplitAfter(raw, "\n") {
				if line == "" {
					continue
				}
				if _, err := observeWorkspaceByteEvent([]byte(line), plan, 2, input, &out); err != nil {
					break
				}
			}
			out.finish()
			var samples ExecutionEarlyFinishSamples
			for i, values := range [][2]uint64{{11, 22}, {33, 44}} {
				samples.Phases[i].Attempts, samples.Phases[i].Completed = 1, 1
				samples.Phases[i].Maximum.LogicalBytes, samples.Phases[i].Maximum.AllocatedBytes = values[0], values[1]
			}
			samples.WarmStart = ExecutionWorkspaceBytePhase{Attempts: 1, Completed: 1}
			samples.WarmStart.Maximum.LogicalBytes, samples.WarmStart.Maximum.AllocatedBytes = 55, 12
			switch mode {
			case "changed_http":
				samples.Phases[1].Maximum.LogicalBytes++ // Still below actual start55: max equality must not hide it.
			case "changed_http_allocated":
				samples.Phases[1].Maximum.AllocatedBytes++
			case "changed_start_logical":
				samples.WarmStart.Maximum.LogicalBytes++
			case "changed_start_allocated":
				samples.WarmStart.Maximum.AllocatedBytes++ // Still below actual finish44.
			}
			if mode == "failed_http" {
				samples.Unavailable = true
			}
			if earlyWorkspaceFinishPrefix(out, samples) != (mode == "valid") {
				t.Fatal(mode, out, samples)
			}
		})
	}
	if workspaceCheckpointMaximum(2, 2) != 1 || workspaceCheckpointMaximum(2, 3) != 2 ||
		workspaceCheckpointMaximum(2, 4) != 2 || 79+3*(26+60) != 337 {
		t.Fatal("two finishes, fixed warm start, or separate physical slots changed")
	}
}

// Actual bounded HTTP requests with supplied reply/authority state, not native
// engine quiescence or authenticated byte provenance.
func TestEpochEarlyWorkspaceFinishHTTP(t *testing.T) {
	for _, mode := range []string{"valid", "missing_final", "missing_author", "conflict", "over_limit", "repeated"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			reader := epochTestHTTPReader(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodPost || r.URL.Path != "/api/t422/lifecycle/sample-workspace" ||
					r.Header.Get("X-Phebs-T422-Workspace-Point") != "finish" || r.Header.Get("X-Phebs-T421-Exact-Read-Ordinal") != "" {
					t.Error("fixed request changed")
				}
				if mode == "conflict" {
					w.WriteHeader(http.StatusConflict)
				}
				_, _ = fmt.Fprint(w, `{"logical_bytes":50,"allocated_bytes":100}`)
			}))
			reader.plan = accountingTestPlan(t)
			reader.run.epoch.Epoch, reader.run.physicalAllowed = 1, true
			reader.run.flow = &ExecutionEpochOne{workspace: &productionRoot{}, authorBytePoint: 2}
			reader.projection.Phase, reader.selectorCleanupPhase, reader.finalUsed = "cold", "cold", true
			switch mode {
			case "missing_final":
				reader.finalUsed = false
			case "missing_author":
				reader.run.flow.authorBytePoint = 0
			case "over_limit":
				reader.plan.SafetyEnvelope.MaximumDataAllocatedBytes = 99
			case "repeated":
				reader.earlyFinishSamples.Phases[0].Attempts = 1
			}
			err := reader.sampleEarlyFinish(t.Context())
			if (err == nil) != (mode == "valid") {
				t.Fatal(mode, err)
			}
			wantCalls := 0
			if mode == "valid" || mode == "conflict" || mode == "over_limit" {
				wantCalls = 1
			}
			if calls != wantCalls || reader.earlyFinishSamples.LimitExceeded != (mode == "over_limit") {
				t.Fatal(calls, reader.earlyFinishSamples)
			}
			if mode == "valid" {
				reader.projection.Phase = "warm_noop"
				if reader.sampleEarlyFinish(t.Context()) != nil || calls != 2 {
					t.Fatal("warm finish did not reuse fixed HTTP transport")
				}
			}
		})
	}
}
