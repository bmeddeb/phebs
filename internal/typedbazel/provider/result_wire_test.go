package provider

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"math/rand"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/bmeddeb/phebs/internal/typedsandbox"
)

func gzipResultFixture(t *testing.T, logical []byte, declared int64) []byte {
	t.Helper()
	var compressed bytes.Buffer
	z := gzip.NewWriter(&compressed)
	if _, err := z.Write(logical); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	wire, err := json.Marshal(resultGzip{resultGzipSchema, declared, compressed.Bytes()})
	if err != nil {
		t.Fatal(err)
	}
	return append(wire, '\n')
}

func TestResultWireLegacyAndFallback(t *testing.T) {
	i, selection, raw := admittedResultFixture(t, false, false)
	ctx := context.Background()
	legacy, err := encodeResultWire(ctx, bytes.Clone(raw[:len(raw)-1]), int64(len(raw)))
	if err != nil || !bytes.Equal(legacy, raw) {
		t.Fatal("fitting legacy bytes changed", err)
	}
	remaining := int64(len(raw) - 1)
	wire, err := encodeResultWire(ctx, bytes.Clone(raw[:len(raw)-1]), remaining)
	if err != nil || int64(len(wire)) > remaining || bytes.Equal(wire, raw) {
		t.Fatal("bounded gzip fallback", err)
	}
	logical, err := DecodeResultWire(ctx, wire, remaining)
	if err != nil || !bytes.Equal(logical, raw) {
		t.Fatal("lossless logical result", err)
	}
	i.Allowance.WorkerBytesUsed = typedsandbox.OutputBytes - remaining
	decoded, err := DecodeResult(ctx, i, selection, wire)
	if err != nil || decoded.Plan().Digest() != i.Plan.Digest() {
		t.Fatal("host admitted exact logical result", err)
	}
	if _, err = decoded.Finalize(ctx); err != nil {
		t.Fatal("fallback lost ordinary complete authority", err)
	}
	for _, mutation := range []string{"request", "phase", "plan", "scip"} {
		t.Run(mutation, func(t *testing.T) {
			r, err := decode[Result](raw, typedsandbox.OutputBytes)
			if err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "request":
				r.RequestDigest = hash([]byte("other"))
			case "phase":
				r.Phase = typedindex.Plan
			case "plan":
				r.Plan = []byte(`{}`)
			case "scip":
				r.SCIP = []byte("wrong")
				r.SCIPSHA256 = hash(r.SCIP)
			}
			bad := resultWire(t, r)
			badWire := gzipResultFixture(t, bad, int64(len(bad)))
			i.Allowance.WorkerBytesUsed = typedsandbox.OutputBytes - int64(len(bad)-1)
			if _, err = DecodeResult(ctx, i, selection, badWire); err == nil {
				t.Fatal("wrapped mutation bypassed authority fence")
			}
		})
	}
}

func TestResultWireWrapperRefusals(t *testing.T) {
	logical := []byte(`{"value":"` + strings.Repeat("x", 4096) + `"}` + "\n")
	wire := gzipResultFixture(t, logical, int64(len(logical)))
	remaining := int64(len(logical) - 5)
	if got, err := DecodeResultWire(context.Background(), wire, remaining); err != nil || !bytes.Equal(got, logical) {
		t.Fatal(err)
	}
	for _, fault := range []string{"schema", "extra-key", "duplicate-key", "reordered", "outer-space", "outer-no-lf", "outer-trailing", "empty", "decoded-zero", "decoded-large", "decoded-short", "decoded-long", "crc", "truncated", "trailing-gzip", "multistream", "inner-no-lf", "inner-extra-lf", "inner-leading-space"} {
		t.Run(fault, func(t *testing.T) {
			var frame resultGzip
			if err := json.Unmarshal(wire, &frame); err != nil {
				t.Fatal(err)
			}
			switch fault {
			case "schema":
				frame.Schema = "other"
			case "empty":
				frame.Gzip = nil
			case "decoded-zero":
				frame.DecodedBytes = 0
			case "decoded-large":
				frame.DecodedBytes = typedsandbox.OutputBytes + 1
			case "decoded-short":
				frame.DecodedBytes--
			case "decoded-long":
				frame.DecodedBytes++
			case "crc":
				frame.Gzip[len(frame.Gzip)-8] ^= 1
			case "truncated":
				frame.Gzip = frame.Gzip[:len(frame.Gzip)-1]
			case "trailing-gzip":
				frame.Gzip = append(frame.Gzip, 0)
			case "multistream":
				frame.Gzip = append(frame.Gzip, frame.Gzip...)
			}
			bad, err := json.Marshal(frame)
			if err != nil {
				t.Fatal(err)
			}
			bad = append(bad, '\n')
			switch fault {
			case "extra-key":
				bad = []byte(strings.TrimSuffix(string(bad), "}\n") + `,"extra":true}` + "\n")
			case "duplicate-key":
				bad = []byte(strings.Replace(string(bad), `"decoded_bytes":`, `"schema":"`+resultGzipSchema+`","decoded_bytes":`, 1))
			case "reordered":
				bad = []byte(`{"decoded_bytes":1,"schema":"` + resultGzipSchema + `","gzip":""}` + "\n")
			case "outer-space":
				bad = append([]byte{' '}, bad...)
			case "outer-no-lf":
				bad = bad[:len(bad)-1]
			case "outer-trailing":
				bad = append(bad, []byte("{}")...)
			case "inner-no-lf":
				bad = gzipResultFixture(t, logical[:len(logical)-1], int64(len(logical)-1))
			case "inner-extra-lf":
				bad = gzipResultFixture(t, append(bytes.Clone(logical), '\n'), int64(len(logical)+1))
			case "inner-leading-space":
				bad = gzipResultFixture(t, append([]byte{' '}, logical...), int64(len(logical)+1))
			}
			got, _, err := decodeResultWire(context.Background(), bad, remaining)
			// Unknown/reordered schemas continue to the existing Result-v1 schema
			// decoder; they must not be mistaken for an unwrapped valid result.
			if fault == "schema" || fault == "reordered" {
				if _, err = decode[Result](got, typedsandbox.OutputBytes); err == nil {
					t.Fatal("unknown wrapper accepted as Result-v1")
				}
				return
			}
			if err == nil {
				t.Fatal("invalid gzip wrapper admitted")
			}
		})
	}
	for _, allowance := range []int64{0, int64(len(wire) - 1), typedsandbox.OutputBytes + 1} {
		if _, err := DecodeResultWire(context.Background(), wire, allowance); !errors.Is(err, typedindex.Capacity) {
			t.Fatal("physical budget not checked first", allowance, err)
		}
	}
	if _, err := DecodeResultWire(context.Background(), wire, int64(len(logical))); err == nil {
		t.Fatal("unnecessary fallback admitted")
	}
	if _, err := encodeResultWire(context.Background(), bytes.Repeat([]byte{'x'}, typedsandbox.OutputBytes), typedsandbox.OutputBytes); !errors.Is(err, typedindex.Capacity) {
		t.Fatal("logical output ceiling widened", err)
	}
}

func TestResultWireShapeCancellationAndFailure(t *testing.T) {
	i, selection, raw := admittedResultFixture(t, false, false)
	deep := []byte(strings.Repeat("[", 26) + "0" + strings.Repeat("]", 26))
	logical := append([]byte(`{"generated":`), deep...)
	logical = append(logical, []byte(`,"padding":"`+strings.Repeat("x", 4096)+`"}`+"\n")...)
	i.Allowance.WorkerBytesUsed = typedsandbox.OutputBytes - int64(len(logical)-1)
	if _, err := DecodeResult(context.Background(), i, selection, gzipResultFixture(t, logical, int64(len(logical)))); !errors.Is(err, typedindex.Capacity) {
		t.Fatal("decoded dimensions bypassed", err)
	}
	for _, action := range []string{"encode", "decode"} {
		t.Run(action, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			var err error
			if action == "encode" {
				_, err = encodeResultWire(ctx, raw[:len(raw)-1], int64(len(raw)-1))
			} else {
				_, err = DecodeResultWire(ctx, gzipResultFixture(t, raw, int64(len(raw))), int64(len(raw)-1))
			}
			if !errors.Is(err, context.Canceled) && !errors.Is(err, typedindex.Canceled) {
				t.Fatal("cancellation lost", err)
			}
		})
	}
	failure := Result{Schema: "phebs-bazel-worker-result-v1", RequestDigest: i.Execution.Digest(), Phase: typedindex.Execute, Failure: &Failure{Stage: "validation", Reason: typedindex.Invalid}}
	failureRaw := resultWire(t, failure)
	i.Allowance.WorkerBytesUsed = typedsandbox.OutputBytes - int64(len(failureRaw)-1)
	if _, err := DecodeResult(context.Background(), i, selection, gzipResultFixture(t, failureRaw, int64(len(failureRaw)))); err == nil {
		t.Fatal("failure evidence switched protocol")
	}
}

type resultWireCancelContext struct {
	context.Context
	checks int
}

func (c *resultWireCancelContext) Err() error {
	c.checks--
	if c.checks <= 0 {
		return context.Canceled
	}
	return nil
}

func TestResultWireStreamingCancellationAndBudget(t *testing.T) {
	logical := []byte(`{"value":"` + strings.Repeat("x", 512<<10) + `"}` + "\n")
	wire := gzipResultFixture(t, logical, int64(len(logical)))
	ctx := &resultWireCancelContext{Context: context.Background(), checks: 32}
	if _, err := DecodeResultWire(ctx, wire, int64(len(logical)-1)); !errors.Is(err, typedindex.Canceled) {
		t.Fatal("streaming inflate cancellation lost", err)
	}
	ctx = &resultWireCancelContext{Context: context.Background(), checks: 5}
	if _, err := encodeResultWire(ctx, logical[:len(logical)-1], int64(len(logical)-1)); !errors.Is(err, context.Canceled) {
		t.Fatal("streaming compression cancellation lost", err)
	}
	noise := make([]byte, 64<<10)
	if _, err := rand.New(rand.NewSource(1)).Read(noise); err != nil {
		t.Fatal(err)
	}
	if _, err := encodeResultWire(context.Background(), noise, 1024); !errors.Is(err, typedindex.Capacity) {
		t.Fatal("untransmittable gzip payload retained", err)
	}
}
