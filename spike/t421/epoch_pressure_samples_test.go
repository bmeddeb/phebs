package t421

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/custodybytes"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

func TestExecutionPressureSampleV3ByteCeilings(t *testing.T) {
	plan := accountingTestPlan(t)
	if plan.Schema != PlanV3Schema || plan.WorkEnvelope.MaximumDataLogicalBytes != 128<<30 || plan.SafetyEnvelope.MaximumDataAllocatedBytes != 128<<30 {
		t.Fatal("prospective V3 byte ceilings changed")
	}
	for _, test := range []struct {
		name   string
		sample custodybytes.Sample
		refuse bool
	}{
		{"above_historical_allocated", custodybytes.Sample{LogicalBytes: 64 << 30, AllocatedBytes: 96<<30 + 1}, false},
		{"both_equal", custodybytes.Sample{LogicalBytes: 128 << 30, AllocatedBytes: 128 << 30}, false},
		{"allocated_one_over", custodybytes.Sample{LogicalBytes: 64 << 30, AllocatedBytes: 128<<30 + 1}, true},
		{"logical_one_over", custodybytes.Sample{LogicalBytes: 128<<30 + 1, AllocatedBytes: 64 << 30}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			reader := epochTestHTTPReader(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				_, _ = fmt.Fprintf(w, `{"logical_bytes":%d,"allocated_bytes":%d}`, test.sample.LogicalBytes, test.sample.AllocatedBytes)
			}))
			reader.plan = plan
			reader.run.epoch.Epoch, reader.run.pressureAllowed = 4, true
			reader.run.flow = &ExecutionEpochOne{workspace: &productionRoot{}}
			reader.projection.Phase, reader.pressure.step = "pressure_80", 1
			reader.pressure.samples.Phases[0].Maximum = custodybytes.Sample{LogicalBytes: 4, AllocatedBytes: 8}
			value, err := reader.pressureSample(t.Context(), "start")
			row := reader.pressure.samples.Phases[0]
			if (err != nil) != test.refuse || value != test.sample || row.Attempts != 1 || row.Completed != 1 || row.Maximum != test.sample || calls != 1 ||
				reader.pressure.samples.LimitExceeded != test.refuse || reader.pressure.samples.Unavailable {
				t.Fatal("completed sample or limit classification changed", value, row, err)
			}
			if test.refuse {
				if !errors.Is(err, errEpochInspection) || reader.err == nil || reader.pressure.sampleOrdinal != 0 {
					t.Fatal("overshoot did not latch", err)
				}
				if _, err := reader.pressureSample(t.Context(), "start"); err == nil || calls != 1 || reader.pressure.samples.Phases[0] != row {
					t.Fatal("retry changed the retained completed overshoot", err)
				}
			}
		})
	}
}

func TestExecutionPressureSampleSequence(t *testing.T) {
	points := []string{"start", "normalized", "ballast", "finish", "start", "ballast", "finish", "start", "ballast", "removed", "finish"}
	phases := []string{"pressure_80", "pressure_80", "pressure_80", "pressure_80", "pressure_90", "pressure_90", "pressure_90", "pressure_75", "pressure_75", "pressure_75", "pressure_75"}
	steps := []uint8{1, 3, 3, 4, 4, 4, 5, 5, 5, 6, 9}
	for i, point := range points {
		if !pressureSampleExpected(phases[i], steps[i], uint8(i), point) {
			t.Fatal("valid position", i)
		}
		for _, other := range []string{"start", "normalized", "ballast", "removed", "finish", "unknown"} {
			if pressureSampleExpected(phases[i], steps[i], uint8(i), other) != (other == point) {
				t.Fatal("point mismatch", i, other)
			}
		}
		if pressureSampleExpected("cold", steps[i], uint8(i), point) || pressureSampleExpected(phases[i], steps[i]+1, uint8(i), point) {
			t.Fatal("foreign phase/step")
		}
	}
	if pressureSampleExpected("pressure_75", 9, 11, "finish") {
		t.Fatal("repeated final sample")
	}
}

func TestExecutionPressureSampleHTTP(t *testing.T) {
	for _, mode := range []string{"complete", "zero", "overflow", "duplicate", "missing", "unknown", "truncated", "oversize", "trailer", "header", "redirect", "conflict", "wrong-point", "post-final", "no-workspace", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			reader := epochTestHTTPReader(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodPost || r.URL.Path != "/api/t422/lifecycle/sample-workspace" || r.Header.Get("Authorization") != "Bearer private-key" ||
					r.Header.Get(dispatchadmission.ProductionRequestHeader) == "" || r.Header.Get("X-Phebs-T422-Workspace-Point") != "start" ||
					r.Header.Get("X-Phebs-T421-Exact-Read-Ordinal") != "" {
					t.Error("request binding")
				}
				body := `{"logical_bytes":50,"allocated_bytes":100}`
				switch mode {
				case "zero":
					body = `{"logical_bytes":0,"allocated_bytes":0}`
				case "overflow":
					body = `{"logical_bytes":50,"allocated_bytes":101}`
				case "duplicate":
					body = `{"logical_bytes":50,"logical_bytes":50,"allocated_bytes":100}`
				case "missing":
					body = `{"logical_bytes":50}`
				case "unknown":
					body = `{"logical_bytes":50,"allocated_bytes":100,"extra":0}`
				case "truncated":
					body = `{"logical_bytes":50`
				case "oversize":
					body = strings.Repeat(" ", 257)
				case "trailer":
					w.Header().Set("Trailer", "Other")
				case "header":
					w.Header().Set(epochReadTrailer, "invented")
				case "redirect":
					w.Header().Set("Location", "/unexpected")
					w.WriteHeader(http.StatusFound)
				case "conflict":
					w.WriteHeader(http.StatusConflict)
				}
				_, _ = w.Write([]byte(body))
				if mode == "trailer" {
					w.Header().Set("Other", "value")
				}
			}))
			reader.run.epoch.Epoch, reader.run.pressureAllowed = 4, true
			reader.run.flow = &ExecutionEpochOne{workspace: &productionRoot{}}
			reader.projection.Phase, reader.pressure.step = "pressure_80", 1
			reader.plan.WorkEnvelope.MaximumDataLogicalBytes, reader.plan.SafetyEnvelope.MaximumDataAllocatedBytes = 100, 100
			reader.pressure.samples.Phases[0].Maximum = custodybytes.Sample{LogicalBytes: 4, AllocatedBytes: 8}
			point, ctx := "start", t.Context()
			if mode == "wrong-point" {
				point = "ballast"
			}
			if mode == "post-final" {
				reader.finalUsed = true
			}
			if mode == "no-workspace" {
				reader.run.flow.workspace = nil
			}
			if mode == "canceled" {
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			}
			value, err := reader.pressureSample(ctx, point)
			good := mode == "complete" || mode == "zero"
			if (err == nil) != good || reader.next != 1 || reader.reports != 0 {
				t.Fatal(value, err, reader.next, reader.reports)
			}
			row := reader.pressure.samples.Phases[0]
			if good {
				if row.Completed != 1 || reader.pressure.sampleOrdinal != 1 || reader.pressure.step != 1 {
					t.Fatal("sample changed R sequence or failed to commit")
				}
			} else {
				if reader.err == nil || reader.pressure.samples.Complete {
					t.Fatal("refusal did not latch")
				}
				if mode == "overflow" {
					if row.Completed != 1 || row.Maximum.AllocatedBytes != 101 || !reader.pressure.samples.LimitExceeded || reader.pressure.samples.Unavailable {
						t.Fatal("excess not retained", row)
					}
				} else if row.Completed != 0 || row.Maximum.AllocatedBytes != 8 || !reader.pressure.samples.Unavailable {
					t.Fatal("invalid suffix changed prefix", row)
				}
			}
			if (mode == "wrong-point" || mode == "post-final" || mode == "no-workspace" || mode == "canceled") && calls != 0 {
				t.Fatal("refused before HTTP")
			}
		})
	}
}
