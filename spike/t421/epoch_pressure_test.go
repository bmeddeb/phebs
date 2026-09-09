package t421

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/readaccounting"
)

func TestExecutionEpochPressureBounds(t *testing.T) {
	plan := Plan{Schema: PlanV3Schema, PhaseDeadlines: frozenPhaseDeadlines(), SafetyEnvelope: frozenSafetyEnvelope()}
	bounds, err := checkpointPressureEpochBounds(plan)
	if err != nil || bounds.lifetime != 5*time.Hour || bounds.controlPairs != 24 || bounds.health != 15*time.Minute || bounds.outputBytes != 64<<20 {
		t.Fatal(bounds, err)
	}
	for _, index := range []int{7, 8, 9, 10} {
		plan.PhaseDeadlines[index].DeadlineMS++
		if _, err := checkpointPressureEpochBounds(plan); err == nil {
			t.Fatal("changed phase admitted", index)
		}
		plan.PhaseDeadlines[index].DeadlineMS--
	}
	for _, run := range []*ExecutionEpochOneRun{nil, {}, {epoch: ExecutionEpochConfig{Epoch: 3}}} {
		if _, err := run.CheckpointRestartPressure(context.Background()); err == nil {
			t.Fatal("unavailable pressure restart")
		}
	}
}

func TestExecutionEpochPressureReaderBoundary(t *testing.T) {
	plan := accountingTestPlan(t)
	projection, err := expectedStateProjectionForPhase(plan, "pressure_80")
	if err != nil {
		t.Fatal(err)
	}
	rows, _, err := correctedInspectionInventory(plan.Profile)
	if err != nil {
		t.Fatal(err)
	}
	prefix := readaccounting.Counts{ControlFileReads: 2, StoreReadAttempts: 3, MemberVisits: 4}
	for _, phase := range []uint32{9, 10, 11} {
		for _, mode := range []string{"complete", "missing-final", "missing-baseline", "wrong-step", "wrong-prior", "unselected", "wrong-epoch", "wrong-catalog", "short-plan", "latched", "out-of-range"} {
			t.Run(strconv.Itoa(int(phase))+"/"+mode, func(t *testing.T) {
				reader := &executionEpochInspection{plan: plan, finalUsed: true, next: 77, reports: 5, totals: prefix,
					pressureBaseline: &[32]byte{1}, lifecycleCalls: 7,
					progressCalls: 6, tailCalls: 7, progressReady: true, tail: epochTailReadiness{Status: "complete"},
					run: &ExecutionEpochOneRun{pressureAllowed: true, epoch: ExecutionEpochConfig{Epoch: 4, CatalogSHA256: projection.CatalogSource.SHA256}}}
				reader.projection.Phase = []string{"process_restart", "pressure_80", "pressure_90"}[phase-9]
				reader.pressure.step = []uint8{1, 4, 5}[phase-9]
				target := phase
				switch mode {
				case "missing-final":
					reader.finalUsed = false
				case "missing-baseline":
					reader.pressureBaseline = nil
				case "wrong-step":
					reader.pressure.step++
				case "wrong-prior":
					reader.projection.Phase = "cold"
				case "unselected":
					reader.run.pressureAllowed = false
				case "wrong-epoch":
					reader.run.epoch.Epoch = 3
				case "wrong-catalog":
					reader.run.epoch.CatalogSHA256 = "wrong"
				case "short-plan":
					reader.plan.PhaseOrder = reader.plan.PhaseOrder[:10]
				case "latched":
					reader.err = errEpochInspection
				case "out-of-range":
					target = 12
				}
				prior, step, final := reader.projection.Phase, reader.pressure.step, reader.finalUsed
				if err := reader.beginPressure(target); (err == nil) != (mode == "complete") {
					t.Fatal(err)
				}
				if reader.next != 77 || reader.reports != 5 || reader.totals != prefix || reader.pressure.step != step {
					t.Fatal("epoch prefix or lifecycle step changed")
				}
				if mode == "complete" {
					if reader.projection.Phase != plan.PhaseOrder[phase-1] || !reflect.DeepEqual(reader.bounds, rows[phase-1]) || reader.finalUsed || reader.progressCalls != 0 || reader.tailCalls != 0 || reader.lifecycleCalls != 0 || reader.progressReady || reader.tail != (epochTailReadiness{}) {
						t.Fatal("phase-local boundary not reset")
					}
					if reader.beginPressure(phase) == nil {
						t.Fatal("repeated boundary admitted")
					}
				} else if reader.projection.Phase != prior || reader.finalUsed != final || reader.progressCalls != 6 || reader.tailCalls != 7 || !reader.progressReady || reader.tail.Status != "complete" {
					t.Fatal("refused boundary mutated phase state")
				}
			})
		}
	}
}

func pressureTestCycle() lifecycle.CycleObservation {
	fence := time.Unix(100, 0).UTC()
	const used = int64(16 << 30)
	c := lifecycle.CycleObservation{Schema: lifecycle.CycleObservationSchema, FenceAt: fence, OwnerTurns: 16,
		Capacity: lifecycle.TransitionCapacityObservation{Completeness: lifecycle.Exact, Pressure: lifecycle.PressureNormal, TotalBytes: 96 << 30,
			UsedBytes: used, ProjectedBytes: used, AvailableBytes: (96 << 30) - used, UsedPercent: 17, ObservedAt: fence.Add(time.Second)}}
	for _, name := range correctedLifecycleOwners() {
		completeness := lifecycle.Exact
		if name == lifecycle.JobOwner {
			completeness = lifecycle.LowerBound
		}
		c.Owners = append(c.Owners, lifecycle.CycleOwnerObservation{Name: name, State: "ok", Completeness: completeness, AttemptedAt: fence})
	}
	return c
}

func TestExecutionEpochPressureNativeReportChecks(t *testing.T) {
	for _, mode := range []string{"valid", "total", "projected", "percent", "job-exact", "job-backlog", "unknown-owner", "future-owner", "owner-limit", "aggregate-under", "aggregate-over", "byte-under"} {
		t.Run(mode, func(t *testing.T) {
			c := pressureTestCycle()
			switch mode {
			case "total":
				c.Capacity.TotalBytes--
			case "projected":
				c.Capacity.ProjectedBytes = 0
			case "percent":
				c.Capacity.UsedPercent = 16
			case "job-exact":
				for i := range c.Owners {
					if c.Owners[i].Name == lifecycle.JobOwner {
						c.Owners[i].Completeness = lifecycle.Exact
					}
				}
			case "job-backlog":
				for i := range c.Owners {
					if c.Owners[i].Name == lifecycle.JobOwner {
						c.Owners[i].Backlog = true
					}
				}
			case "unknown-owner":
				c.Owners[0].Name = "unknown"
			case "future-owner":
				c.Owners[0].AttemptedAt = c.Capacity.ObservedAt.Add(time.Second)
			case "owner-limit":
				c.Owners[0].Scanned = 65
			case "aggregate-under":
				c.Owners[0].Scanned = 1
			case "aggregate-over":
				c.Scanned = 16*64 + 1
			case "byte-under":
				c.Owners[0].RootBytes = 1
			}
			if pressureCycleValid(c, false) != (mode == "valid") {
				t.Fatal(mode)
			}
			if mode == "job-backlog" && !pressureCycleValid(c, true) {
				t.Fatal("truthful recovery durable-job backlog refused")
			}
		})
	}
}

func TestExecutionEpochPressureSequence(t *testing.T) {
	ops := []string{"park", "drive-normal", "normal-cycle", "pressure-80", "pressure-90", "pressure-75", "drive-recovery", "recovery-cycle", "recovered-normal"}
	phases := []string{"process_restart", "pressure_80", "pressure_80", "pressure_80", "pressure_90", "pressure_75", "pressure_75", "pressure_75", "pressure_75"}
	for i, op := range ops {
		for j, candidate := range ops {
			if pressureStep(phases[i], uint8(i), candidate) != (i == j) {
				t.Fatal(i, j, op)
			}
		}
		if pressureStep("cold", uint8(i), op) {
			t.Fatal("wrong phase", op)
		}
	}
	if pressureStep("pressure_75", 9, "recovered-normal") {
		t.Fatal("repeated terminal report")
	}
}

func TestExecutionEpochPressureCapacitySequence(t *testing.T) {
	normal := pressureTestCycle()
	fence80 := normal.Capacity.ObservedAt.Add(time.Second)
	capacity := func(percent int, pressure lifecycle.Pressure, fence time.Time) lifecycle.TransitionCapacityObservation {
		used := int64(percentOf(96<<30, uint64(percent-1)) + 4096)
		return lifecycle.TransitionCapacityObservation{Completeness: lifecycle.Exact, Pressure: pressure, TotalBytes: 96 << 30,
			UsedBytes: used, ProjectedBytes: used, AvailableBytes: (96 << 30) - used, UsedPercent: percent, ObservedAt: fence.Add(time.Second)}
	}
	p := epochPressureObservations{normal: normal}
	p.collect = lifecycle.Pressure80Observation{Schema: lifecycle.Pressure80ObservationSchema, BallastFenceAt: fence80,
		PriorCapacityObservedAt: normal.Capacity.ObservedAt, Capacity: capacity(80, lifecycle.PressureCollect, fence80)}
	fence90 := p.collect.Capacity.ObservedAt.Add(time.Second)
	p.refuse = lifecycle.Pressure90Observation{Schema: lifecycle.Pressure90ObservationSchema, BallastFenceAt: fence90,
		PriorCapacityObservedAt: p.collect.Capacity.ObservedAt, Capacity: capacity(90, lifecycle.PressureRefuse, fence90)}
	fence75 := p.refuse.Capacity.ObservedAt.Add(time.Second)
	p.latched = lifecycle.Pressure75Observation{Schema: lifecycle.Pressure75ObservationSchema, BallastFenceAt: fence75,
		PriorCapacityObservedAt: p.refuse.Capacity.ObservedAt, Capacity: capacity(75, lifecycle.PressureRefuse, fence75)}
	for _, tc := range []struct {
		operation string
		fence     time.Time
	}{{"pressure-80", fence80}, {"pressure-90", fence90}, {"pressure-75", fence75}} {
		if !p.valid(tc.operation, tc.fence) {
			t.Fatal("valid sequence refused", tc.operation)
		}
		if p.valid(tc.operation, tc.fence.Add(time.Nanosecond)) {
			t.Fatal("wrong native fence accepted", tc.operation)
		}
	}
	changed := p
	changed.latched.Capacity.Pressure = lifecycle.PressureNormal
	if changed.valid("pressure-75", fence75) {
		t.Fatal("unlatched 75 accepted")
	}
	changed = p
	changed.refuse.PriorCapacityObservedAt = normal.Capacity.ObservedAt
	if changed.valid("pressure-90", fence90) {
		t.Fatal("noncontiguous capacity accepted")
	}
}

func TestExecutionEpochPressureReadTransport(t *testing.T) {
	for _, mode := range []string{"complete", "nonzero-report", "bad-cycle", "duplicate-body", "missing-trailer"} {
		t.Run(mode, func(t *testing.T) {
			reader := epochTestHTTPReader(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/api/t422/lifecycle/normal-cycle" || r.Header.Get("X-Phebs-T422-Ballast-Unix-Nano") != "" {
					t.Error("unexpected request")
				}
				ordinal, _ := strconv.ParseUint(r.Header.Get("X-Phebs-T421-Exact-Read-Ordinal"), 10, 64)
				report := epochInspectionReport{Schema: "t421-source-free-read-accounting-v1", RequestOrdinal: ordinal, Status: "complete"}
				if mode == "nonzero-report" {
					report.StoreReadAttempts = 1
				}
				encoded, _ := json.Marshal(report)
				if mode != "missing-trailer" {
					w.Header().Set("Trailer", epochReadTrailer)
				}
				cycle := pressureTestCycle()
				if mode == "bad-cycle" {
					cycle.Capacity.ProjectedBytes = 0
				}
				raw, _ := json.Marshal(cycle)
				if mode == "duplicate-body" {
					raw = append(raw, raw...)
				}
				_, _ = w.Write(raw)
				if mode != "missing-trailer" {
					w.Header().Set(epochReadTrailer, base64.RawURLEncoding.EncodeToString(encoded))
				}
			}))
			reader.run.epoch.Epoch, reader.run.pressureAllowed = 4, true
			reader.projection.Phase, reader.pressure.step = "pressure_80", 2
			err := reader.pressureRead(t.Context(), "normal-cycle", time.Time{})
			if (err == nil) != (mode == "complete") || reader.next != 2 {
				t.Fatal(err, reader.next)
			}
			if err == nil && (reader.pressure.step != 3 || reader.reports != 1 || reader.pressure.normal.OwnerTurns != 16) {
				t.Fatal(reader.pressure, reader.reports)
			}
			if err != nil && reader.err == nil {
				t.Fatal("failure did not latch")
			}
		})
	}
}

func TestExecutionEpochPressureParkTransport(t *testing.T) {
	for _, mode := range []string{"complete", "wrong-body", "extra-trailer", "already-parked"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			reader := epochTestHTTPReader(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodPost || r.URL.Path != "/api/t422/lifecycle/park" || r.Header.Get("X-Phebs-T421-Exact-Read-Ordinal") != "" {
					t.Error("unexpected request")
				}
				if mode == "extra-trailer" {
					w.Header().Set("Trailer", "Other")
				}
				body := `{"status":"complete"}`
				if mode == "wrong-body" {
					body = `{"status":"pending"}`
				}
				_, _ = w.Write([]byte(body))
				if mode == "extra-trailer" {
					w.Header().Set("Other", "value")
				}
			}))
			reader.run.epoch.Epoch, reader.run.pressureAllowed = 4, true
			reader.projection.Phase = "process_restart"
			if mode == "already-parked" {
				reader.pressure.step = 1
			}
			err := reader.pressureCommand(t.Context(), "park", time.Time{})
			if (err == nil) != (mode == "complete") || reader.next != 1 || reader.reports != 0 {
				t.Fatal(err, reader.next, reader.reports)
			}
			if mode == "already-parked" && calls != 0 {
				t.Fatal("repeated park forwarded")
			}
		})
	}
}
