package typedsandbox

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func testAllowance() Allowance {
	c := testControlIdentity()
	return Allowance{Schema: allowanceSchema, PlanningDigest: c.PlanningDigest, AttemptDigest: c.AttemptDigest, BootID: "12345678-1234-1234-1234-123456789abc", TimeDevice: 1, TimeInode: 2, Start: 1, Deadline: 1 + int64(WallLimit)}
}
func testControlSeal(t *testing.T, a Allowance, c ControlIdentity) []byte {
	t.Helper()
	raw, err := json.Marshal(struct {
		Schema   string `json:"schema"`
		Identity any    `json:"identity"`
	}{"phebs-typed-worker-controls-v3", struct {
		Allowance      Allowance `json:"allowance"`
		Phase          string    `json:"phase"`
		PlanningDigest string    `json:"planning_digest"`
		AttemptDigest  string    `json:"attempt_digest"`
		RequestDigest  string    `json:"request_digest"`
	}{a, c.Phase, c.PlanningDigest, c.AttemptDigest, c.RequestDigest}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func runFake(ctx context.Context, o Options) (Result, error) {
	return runChecked(ctx, o, func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return o.Allowance.Validate()
	})
}

func TestAllowanceCanonicalArithmetic(t *testing.T) {
	a := testAllowance()
	raw, err := EncodeAllowance(a)
	if err != nil {
		t.Fatal(err)
	}
	if got, e := DecodeAllowance(raw); e != nil || got != a {
		t.Fatal(got, e)
	}
	for _, change := range []func(*Allowance){func(v *Allowance) { v.Start = 0 }, func(v *Allowance) { v.Start = math.MaxInt64 }, func(v *Allowance) { v.Deadline++ }, func(v *Allowance) { v.Deadline-- }, func(v *Allowance) { v.TimeInode = 0 }, func(v *Allowance) { v.BootID = strings.ToUpper(v.BootID) }, func(v *Allowance) { v.WorkerBytesUsed = -1 }, func(v *Allowance) { v.WorkerBytesUsed = OutputBytes + 1 }, func(v *Allowance) { v.WireBytesUsed = maxWireBytes + 1 }, func(v *Allowance) { v.PlanningDigest = "bad" }} {
		bad := a
		change(&bad)
		if bad.Validate() == nil {
			t.Fatal("invalid scalar accepted", bad)
		}
	}
	for _, bad := range [][]byte{append([]byte(" "), raw...), append(raw, '\n'), bytes.Replace(raw, []byte(`"schema":`), []byte(`"Schema":`), 1), append(append([]byte(nil), raw[:len(raw)-1]...), []byte(`,"schema":"phebs-typed-allowance-v1"}`)...)} {
		if _, err := DecodeAllowance(bad); err == nil {
			t.Fatal("noncanonical allowance")
		}
	}
	for _, now := range []int64{a.Start - 1, a.Deadline, a.Deadline + 1} {
		if _, err := a.remaining(now); err == nil {
			t.Fatal("bad time", now)
		}
	}
	for _, now := range []int64{a.Start, a.Deadline - 1} {
		n, e := a.remaining(now)
		if e != nil || n != a.Deadline-now {
			t.Fatal(n, e)
		}
	}
	for _, change := range []func(*Options){func(o *Options) { o.Control.Phase = "other" }, func(o *Options) { o.Control.RequestDigest = "bad" }, func(o *Options) { o.Allowance.Deadline++ }} {
		o := Options{Allowance: a, Control: testControlIdentity()}
		change(&o)
		if _, _, _, err := parseSupervisorArgs(supervisorArgs(o)); err == nil {
			t.Fatal("invalid argv")
		}
	}
}

func TestAllowanceJoinedToken(t *testing.T) {
	for _, fault := range []string{"", "cleanup error", "truncated stream", "incomplete report", "watchdog only", "wrong allowance report", "wrong phase report", "wrong seal report", "changed invocation", "expires in cleanup"} {
		t.Run(fault, func(t *testing.T) {
			fakeFault := fault
			if fault == "expires in cleanup" {
				fakeFault = ""
			}
			d, o := fakeDaemon(t, fakeFault)
			check := func(ctx context.Context) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				d.mu.Lock()
				defer d.mu.Unlock()
				if fault == "expires in cleanup" && d.removed {
					return context.DeadlineExceeded
				}
				return o.Allowance.Validate()
			}
			result, err := runChecked(t.Context(), o, check)
			next, e := AdvanceAllowance(o.Allowance, result)
			if fault != "" {
				if err == nil || e == nil {
					t.Fatal("uncertain/late result minted allowance", err, e)
				}
				return
			}
			if err != nil || e != nil || next.Deadline != o.Allowance.Deadline || next.Start != o.Allowance.Start || next.WorkerBytesUsed != int64(len(result.Stdout)+len(result.Stderr)) || next.WireBytesUsed <= next.WorkerBytesUsed {
				t.Fatal(next, err, e)
			}
			forged := Result{ExitCode: 0, Removed: true, Stdout: []byte("success"), Resources: Resources{LimitsVerified: true}}
			if _, e = AdvanceAllowance(o.Allowance, forged); e == nil {
				t.Fatal("public fields minted token")
			}
			result.Stdout = nil
			result.Removed = false
			result.ExitCode = 99
			if again, e := AdvanceAllowance(o.Allowance, result); e != nil || again != next {
				t.Fatal("public mutation changed trusted counts", again, e)
			}
			wrong := o.Allowance
			wrong.AttemptDigest = testImage
			if _, e = AdvanceAllowance(wrong, result); e == nil {
				t.Fatal("cross attempt completion")
			}
		})
	}
}

func TestAllowanceWireAggregateBoundaries(t *testing.T) {
	for _, used := range []int64{0, maxWireBytes - 3, maxWireBytes} {
		for _, delta := range []int64{-1, 0, 1} {
			n := int64(maxWireBytes) - used + delta
			if n < 0 {
				continue
			}
			var wire bytes.Buffer
			var header [8]byte
			header[0] = 1
			if n > 0 {
				binary.BigEndian.PutUint32(header[4:], uint32(n))
				wire.Write(header[:])
				wire.Write(bytes.Repeat([]byte("x"), int(n)))
			}
			got := readWireLimit(&wire, maxWireBytes-used)
			if (got.err == nil) != (delta <= 0) || got.err == nil && got.payloadBytes != n {
				t.Fatal(used, delta, got.err, got.payloadBytes)
			}
		}
	}
}

func TestAllowanceWatchdogBootClock(t *testing.T) {
	var elapsed int64
	var clockErr error
	s := newWatchdogClock(WallLimit, func() (int64, error) { return elapsed, clockErr })
	elapsed = int64(time.Second)
	s.resources(Resources{Samples: 1})
	if !s.progress(2, 1) {
		t.Fatal("first stage")
	}
	// BOOTTIME advances over suspend even though the worker's monotonic elapsed
	// barely moved. Every diagnostic timestamp must reflect the resumed clock.
	elapsed = int64(250 * time.Second)
	s.resources(Resources{Samples: 2})
	if !s.progress(3, 2) {
		t.Fatal("resumed stage")
	}
	got, err := decodeWatchdog(s.frame.Load().data, WallLimit)
	if err != nil || got.ResourceSampleNanoseconds != elapsed || got.WorkerStageStartedNanoseconds != elapsed || got.CompletedStages[0].Nanoseconds != int64(249*time.Second) {
		t.Fatal(got, err)
	}
	elapsed = int64(301 * time.Second)
	s.resources(Resources{Samples: 3})
	got, err = decodeWatchdog(s.frame.Load().data, WallLimit)
	if err != nil || got.SnapshotNanoseconds != int64(WallLimit) || got.ResourceAgeAtDeadlineNanoseconds != 0 {
		t.Fatal(got, err)
	}
	clockErr = ErrRefused
	s.resources(Resources{Samples: 4})
	if s.frame.Load() != nil || !s.failed.Load() {
		t.Fatal("clock failure retained a misleading frame")
	}
	clockErr = nil
	s.resources(Resources{Samples: 5})
	if s.frame.Load() != nil {
		t.Fatal("sticky clock failure recovered")
	}
	var now int64 = 10
	s = newWatchdogClock(WallLimit, func() (int64, error) { return now, nil })
	now = 9
	s.resources(Resources{})
	if s.frame.Load() != nil {
		t.Fatal("clock rollback accepted")
	}
}

func TestAllowanceCanceledWaitRetainsOnlyAuthenticatedPartial(t *testing.T) {
	for _, fault := range []string{"watchdog deadline", "watchdog deadline delayed", "watchdog deadline delayed malformed", "watchdog deadline delayed wrong invocation", "watchdog deadline delayed unavailable", "canceled normal report"} {
		t.Run(fault, func(t *testing.T) {
			d, o := fakeDaemon(t, fault)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			cancelDone := make(chan struct{})
			go func() {
				defer close(cancelDone)
				select {
				case <-d.start:
					time.Sleep(30 * time.Millisecond)
					cancel()
				case <-ctx.Done():
				}
			}()
			result, err := runChecked(ctx, o, func(ctx context.Context) error { return ctx.Err() })
			cancel()
			<-cancelDone
			if err == nil || !result.Removed {
				t.Fatal("cancellation lost failure/cleanup", result, err)
			}
			if _, e := AdvanceAllowance(o.Allowance, result); e == nil {
				t.Fatal("canceled result minted successor")
			}
			want := fault == "watchdog deadline" || fault == "watchdog deadline delayed"
			if (result.Watchdog != nil) != want {
				t.Fatal("partial availability", fault, result, err)
			}
			if want && (result.ExitCode != 124 || result.StopReason != "wall_limit" || result.Watchdog.WorkerStage != "planning") {
				t.Fatal("wrong partial", result)
			}
			if !want && result.StopReason == "wall_limit" {
				t.Fatal("unavailable frame invented timeout classification")
			}
		})
	}
}

func TestStopOnlyTransportDoesNotCancelBeforeCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	transport, start, stop := stopOnlyTransport(ctx)
	cancel()
	start()
	if transport.Err() != nil {
		t.Fatal("execution cancellation discarded teardown transport")
	}
	stop()
	stop()
	if transport.Err() == nil {
		t.Fatal("watcher/transport retained after stop")
	}
}
