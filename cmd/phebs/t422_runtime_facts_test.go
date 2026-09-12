package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/generationscheduler"
	"github.com/bmeddeb/phebs/internal/store"
)

// These are native-free construction/codec checks, not admission or proof
// that the actual protected Phebs image ran or registered workers.
func TestT422RuntimeFacts(t *testing.T) {
	var output bytes.Buffer
	if err := writeT422RuntimeFacts(t.Context(), nil, &output, nil); err != nil {
		t.Fatal(err)
	}
	var facts t422RuntimeFacts
	decoder := json.NewDecoder(bytes.NewReader(output.Bytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&facts); err != nil {
		t.Fatal(err)
	}
	canonical, err := json.Marshal(facts)
	if err != nil || !bytes.Equal(output.Bytes(), append(canonical, '\n')) {
		t.Fatal("noncanonical runtime facts", err)
	}
	if facts.Schema != t422RuntimeFactsSchema || facts.StoreRunnerDefaultMaxAttempts != 3 ||
		facts.ObservationIOConcurrency != 1 || facts.ObservationCPUConcurrency != 2 ||
		facts.RelationshipConcurrency != 1 || facts.ExtractionConcurrency != 2 ||
		facts.NativeMaximumAggregatePartitions != 131072 || facts.StoreGenerationMaxAttempts != 8 ||
		facts.SelectedJobAcceptedAttempts != 3 || facts.SelectedChunkAcceptedAttempts != 5 ||
		facts.MaximumStoreRowsPerTransaction != 512 {
		t.Fatal("native configuration changed", facts)
	}
	for name, schedule := range map[string]t422ScheduleFacts{
		"planning": facts.ObservationPlanning, "inventory": facts.ObservationInventory,
		"execution": facts.ObservationExecution, "relationship": facts.Relationship, "extraction": facts.Extraction,
	} {
		tokens := 1
		if name == "execution" {
			tokens = 2
		}
		if schedule.MaxAttempts != 5 || schedule.RepositoryTokens != tokens {
			t.Fatal("native schedule configuration changed", name, schedule)
		}
	}
	// The existing admission probe transport accepts at most 4 KiB; this is
	// a fit assertion, not a new transport or permission to launch that probe.
	if output.Len() != 688 || output.Len() > 4<<10 || strings.Contains(output.String(), "target") ||
		strings.Contains(output.String(), "registered") || strings.Contains(output.String(), "verified") {
		t.Fatal("unobserved authority or oversized facts", output.String())
	}
}

type t422RuntimeFactsWriter func([]byte) (int, error)

func (write t422RuntimeFactsWriter) Write(raw []byte) (int, error) { return write(raw) }

func TestT422RuntimeFactsRefusals(t *testing.T) {
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, test := range []struct {
		name     string
		ctx      context.Context
		args     []string
		lifetime *dispatchadmission.ProductionLifetime
	}{
		{name: "nil context"},
		{name: "canceled", ctx: canceled},
		{name: "argument", ctx: t.Context(), args: []string{"--config"}},
		// Inert nonnil owner only tests the refusal guard; it is not a
		// fabricated successful inherited bootstrap or native closure.
		{name: "operational lifetime", ctx: t.Context(), lifetime: &dispatchadmission.ProductionLifetime{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			if err := writeT422RuntimeFacts(test.ctx, test.args, &output, test.lifetime); err == nil || output.Len() != 0 {
				t.Fatal("invalid no-work command emitted facts", err, output.String())
			}
		})
	}
	if err := writeT422RuntimeFacts(t.Context(), nil, nil, nil); err == nil {
		t.Fatal("nil writer accepted")
	}
	sentinel := errors.New("sink refused")
	for _, test := range []struct {
		name  string
		write t422RuntimeFactsWriter
		want  error
	}{
		{name: "short", write: func(raw []byte) (int, error) { return len(raw) - 1, nil }, want: io.ErrShortWrite},
		{name: "sink", write: func([]byte) (int, error) { return 0, sentinel }, want: sentinel},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := writeT422RuntimeFacts(t.Context(), nil, test.write, nil); !errors.Is(err, test.want) {
				t.Fatal(err)
			}
		})
	}
	ctx, stop := context.WithCancel(t.Context())
	defer stop()
	var retained bytes.Buffer
	writer := t422RuntimeFactsWriter(func(raw []byte) (int, error) {
		n, err := retained.Write(raw)
		stop()
		return n, err
	})
	if err := writeT422RuntimeFacts(ctx, nil, writer, nil); !errors.Is(err, context.Canceled) || retained.Len() == 0 {
		t.Fatal("late cancellation discarded emitted prefix or became success", err)
	}
}

func TestT422RuntimeFactsSelectedAttempts(t *testing.T) {
	facts := configuredT422RuntimeFacts()
	for _, test := range []struct {
		name           string
		attempt        int
		event, outcome string
		accepted       bool
	}{
		{name: "last job start", attempt: facts.SelectedJobAcceptedAttempts, event: "started", outcome: "running", accepted: true},
		{name: "next job start", attempt: facts.SelectedJobAcceptedAttempts + 1, event: "started", outcome: "running"},
		{name: "last job retry", attempt: facts.SelectedJobAcceptedAttempts - 1, event: "requeued", outcome: "retryable", accepted: true},
		{name: "refused job retry", attempt: facts.SelectedJobAcceptedAttempts, event: "requeued", outcome: "retryable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			report := store.JobLifecycleReport{
				Schema: store.JobLifecycleSchema, Event: test.event, JobID: "native-parser-test",
				Kind: store.JobSync, Target: "test-repository", Attempt: test.attempt, Outcome: test.outcome,
			}
			raw, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = t422NativeAttempt("job", raw)
			if (err == nil) != test.accepted {
				t.Fatal("selected native job report limit", err)
			}
		})
	}
	// Actual selected report parser, including service-state stages whose
	// store recipe allows eight attempts. No claim/store mutation is executed.
	for _, stage := range []string{extractionpublication.ScheduleStage, store.ServiceStateV3ReconcileStage, store.ServiceStateV3ActivateStage} {
		for _, test := range []struct {
			name           string
			attempt        int
			event, outcome string
			accepted       bool
		}{
			{name: "last accepted start", attempt: facts.SelectedChunkAcceptedAttempts - 1, event: "started", outcome: "running", accepted: true},
			{name: "native store capacity is not selected admission", attempt: facts.SelectedChunkAcceptedAttempts, event: "started", outcome: "running"},
			{name: "last allowed retry", attempt: facts.SelectedChunkAcceptedAttempts - 2, event: "settled", outcome: "retried", accepted: true},
			{name: "refused retry", attempt: facts.SelectedChunkAcceptedAttempts - 1, event: "settled", outcome: "retried"},
		} {
			t.Run(stage+"/"+test.name, func(t *testing.T) {
				report := generationscheduler.ChunkLifecycleReport{
					Schema:   generationscheduler.ChunkLifecycleSchema,
					Identity: "sha256:" + strings.Repeat("a", 64), Generation: "sha256:" + strings.Repeat("b", 64),
					Stage: stage, Attempt: test.attempt, Event: test.event, Outcome: test.outcome,
				}
				raw, err := json.Marshal(report)
				if err != nil {
					t.Fatal(err)
				}
				_, _, err = t422NativeAttempt("chunk", raw)
				if (err == nil) != test.accepted {
					t.Fatal("selected native report limit", err)
				}
			})
		}
	}
}

// Source binding supplements the codec checks: the actual serve constructors
// must consume these same named values, not a separate probe-only copy.
func TestT422RuntimeFactsSchedulerConstruction(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"observationIOConcurrency": 1, "observationCPUConcurrency": 1, "relationshipConcurrency": 1}
	ast.Inspect(file, func(node ast.Node) bool {
		field, ok := node.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		key, ok := field.Key.(*ast.Ident)
		if !ok || key.Name != "Concurrency" {
			return true
		}
		value, ok := field.Value.(*ast.Ident)
		if ok {
			if _, present := want[value.Name]; present {
				want[value.Name]--
			}
		}
		return true
	})
	for name, remaining := range want {
		if remaining != 0 {
			t.Fatal("serve does not construct the observed class once", name, remaining)
		}
	}
}
