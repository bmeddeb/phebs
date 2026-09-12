package t421

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

// Supplied observations intentionally differ from today's configured values.
// A codec observation must not manufacture an expected profile comparison.
func modeledExecutionRuntimeFacts() executionConfiguredRuntimeFacts {
	return executionConfiguredRuntimeFacts{
		Schema: "t422-runtime-facts-v1", StoreRunnerDefaultMaxAttempts: 4,
		ObservationIOConcurrency: 2, ObservationCPUConcurrency: 3, RelationshipConcurrency: 2, ExtractionConcurrency: 3,
		ObservationPlanning:              executionScheduleFacts{MaxAttempts: 6, RepositoryTokens: 2},
		ObservationInventory:             executionScheduleFacts{MaxAttempts: 7, RepositoryTokens: 3},
		ObservationExecution:             executionScheduleFacts{MaxAttempts: 8, RepositoryTokens: 4},
		Relationship:                     executionScheduleFacts{MaxAttempts: 9, RepositoryTokens: 2},
		Extraction:                       executionScheduleFacts{MaxAttempts: 10, RepositoryTokens: 3},
		NativeMaximumAggregatePartitions: 131073, StoreGenerationMaxAttempts: 9,
		SelectedJobAcceptedAttempts: 4, SelectedChunkAcceptedAttempts: 6, MaximumStoreRowsPerTransaction: 513,
	}
}

func runtimeFactsTestJSON(t *testing.T, facts executionConfiguredRuntimeFacts) []byte {
	t.Helper()
	raw, err := json.Marshal(facts)
	if err != nil {
		t.Fatal(err)
	}
	return append(raw, '\n')
}

func TestExecutionRuntimeFactsObservedWire(t *testing.T) {
	facts := modeledExecutionRuntimeFacts()
	raw := runtimeFactsTestJSON(t, facts)
	observed, err := decodeExecutionRuntimeFacts(raw)
	if err != nil || observed != facts {
		t.Fatal("supplied facts became frozen expected values", observed, err)
	}
	observed.ObservationPlanning.MaxAttempts++
	if facts.ObservationPlanning.MaxAttempts != 6 {
		t.Fatal("observation aliases caller")
	}
	for _, test := range []struct {
		name string
		raw  string
	}{
		{name: "empty"},
		{name: "no_lf", raw: strings.TrimSuffix(string(raw), "\n")},
		{name: "two_lf", raw: string(raw) + "\n"},
		{name: "trailing", raw: string(raw) + "{}"},
		{name: "unknown", raw: strings.Replace(string(raw), "{", "{\"unknown\":1,", 1)},
		{name: "duplicate", raw: strings.Replace(string(raw), "{", "{\"schema\":\"t422-runtime-facts-v1\",", 1)},
		{name: "wrong_schema", raw: strings.Replace(string(raw), "t422-runtime-facts-v1", "t422-runtime-facts-v2", 1)},
		{name: "missing", raw: strings.Replace(string(raw), "\"store_runner_default_max_attempts\":4,", "", 1)},
		{name: "zero", raw: strings.Replace(string(raw), "\"store_generation_max_attempts\":9", "\"store_generation_max_attempts\":0", 1)},
		{name: "negative", raw: strings.Replace(string(raw), "\"store_generation_max_attempts\":9", "\"store_generation_max_attempts\":-1", 1)},
		{name: "nested_zero", raw: strings.Replace(string(raw), "\"repository_tokens\":2", "\"repository_tokens\":0", 1)},
		{name: "fraction", raw: strings.Replace(string(raw), "\"store_generation_max_attempts\":9", "\"store_generation_max_attempts\":9.0", 1)},
		{name: "null", raw: strings.Replace(string(raw), "\"max_attempts\":6", "\"max_attempts\":null", 1)},
		{name: "overflow", raw: strings.Replace(string(raw), "\"store_generation_max_attempts\":9", "\"store_generation_max_attempts\":18446744073709551616", 1)},
		{name: "too_large", raw: strings.Repeat(" ", 4<<10+1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := decodeExecutionRuntimeFacts([]byte(test.raw)); err == nil {
				t.Fatal("noncanonical or incomplete facts accepted")
			}
		})
	}
}

// Only prework state is modeled. The owned-check failure must stop at real
// metadata validation; these placeholders can never create a positive identity.
func modeledRuntimeProfileFlow() *ExecutionEpochOne {
	builds := &ExecutionGoBuildCustody{}
	return &ExecutionEpochOne{
		plan: Plan{Schema: PlanV3Schema}, release: func() {},
		epochs:             &ExecutionEpochConfigCustody{author: &ExecutionAuthorCustody{request: ExecutionAuthorRequest{Builds: builds}}},
		phebs:              &ExecutionToolCustody{referenceInputs: builds},
		profileEnvironment: &executionRuntimeEnvironmentObservation{}, profileEnvironmentUsed: true,
	}
}

func TestExecutionProfileRuntimePreworkRefusals(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	for _, mode := range []string{
		"nil", "no_deadline", "canceled", "v1", "v2", "closed", "used", "authored", "clock",
		"author_closed", "author_active", "author_next", "epoch_closed", "epoch_active", "released",
		"missing_environment", "wrong_reference", "repeat", "owned_check_failure",
	} {
		t.Run(mode, func(t *testing.T) {
			flow, callCtx := modeledRuntimeProfileFlow(), ctx
			switch mode {
			case "nil":
				flow = nil
			case "no_deadline":
				callCtx = context.Background()
			case "canceled":
				var stop context.CancelFunc
				callCtx, stop = context.WithCancel(ctx)
				stop()
			case "v1":
				flow.plan.Schema = PlanSchema
			case "v2":
				flow.plan.Schema = PlanV2Schema
			case "closed":
				flow.closed = true
			case "used":
				flow.used = true
			case "authored":
				flow.authored = true
			case "clock":
				flow.authorStarted = time.Now()
			case "author_closed":
				flow.epochs.author.closed = true
			case "author_active":
				flow.epochs.author.active = true
			case "author_next":
				flow.epochs.author.next = 1
			case "epoch_closed":
				flow.epochs.closed = true
			case "epoch_active":
				flow.epochs.active = true
			case "released":
				flow.epochs.released = 1
			case "missing_environment":
				flow.profileEnvironment = nil
			case "wrong_reference":
				flow.phebs.referenceInputs = &ExecutionGoBuildCustody{}
			case "repeat":
				flow.profileRuntime = &executionRuntimeObservation{err: ErrExecutionEpochOne}
			}
			if err := flow.prepareProfileRuntime(callCtx); err == nil {
				t.Fatal("invalid prework accepted")
			}
			if flow == nil {
				return
			}
			consumed := mode == "repeat" || mode == "owned_check_failure"
			if (flow.profileRuntime != nil) != consumed ||
				flow.profileRuntime != nil && (flow.profileRuntime.RootStarted || flow.profileRuntime.Complete || flow.profileRuntime.Observed) {
				t.Fatal("refusal consumed an unowned attempt or fabricated child/facts")
			}
			if mode == "owned_check_failure" && flow.prepareProfileRuntime(ctx) == nil {
				t.Fatal("failed one-shot prework retried")
			}
		})
	}
}

func TestExecutionRuntimeProbeRetainedCustody(t *testing.T) {
	// Supplied join facts test the real Close/AuthorA guard, not OS cleanup or
	// genuine executable admission. No fake operational active flag is set.
	for _, mode := range []string{"root_pending", "session_pending", "failed_joined"} {
		t.Run(mode, func(t *testing.T) {
			flow := modeledRuntimeProfileFlow()
			flow.controller, flow.parent = &dispatchadmission.Controller{}, &dispatchadmission.LocalProducer{}
			flow.profileRuntime = &executionRuntimeObservation{
				RootStarted: true, RootJoined: mode != "root_pending", SessionEmpty: mode == "failed_joined",
				err: ErrExecutionEpochOne,
			}
			if result, err := flow.AuthorA(t.Context()); err == nil || result.Completed || !flow.authorStarted.IsZero() {
				t.Fatal("failed prework reached author clock/work", result, err)
			}
			flow.controller, flow.parent = nil, nil
			if err := flow.Close(); !errors.Is(err, ErrExecutionEpochOne) {
				t.Fatal("failed prework lost error", err)
			}
			if flow.closed != (mode == "failed_joined") || flow.profileRuntime.releasable() != (mode == "failed_joined") {
				t.Fatal("unjoined prework released custody or joined failure became success")
			}
		})
	}
	flow := modeledRuntimeProfileFlow()
	waited := make(chan error, 1)
	flow.profileRuntime = &executionRuntimeObservation{RootStarted: true, err: ErrExecutionEpochOne, waited: waited}
	waited <- nil // A later root exit alone cannot invent session closure.
	if flow.profileRuntime.releasable() || flow.Close() == nil || flow.closed {
		t.Fatal("late Wait notification silently repaired incomplete custody")
	}
	flow = modeledRuntimeProfileFlow()
	if err := flow.Close(); err != nil || !flow.closed {
		t.Fatal("omitted observation changed existing unused flow close", err)
	}
}
