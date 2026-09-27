package typedsandbox

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
)

func TestWorkerInvocationArgvAndReportRoot(t *testing.T) {
	o := Options{Allowance: testAllowance(), Control: testControlIdentity()}
	args := supervisorArgs(o)
	expected := append([]string{WorkerCommand}, args[1:]...)
	if got := workerArgs(args); !slices.Equal(got, expected) {
		t.Fatal("exact argv", got, expected)
	}
	a, phase, request, seal, err := parseWorkerArgs(expected)
	if err != nil || a != o.Allowance || phase != o.Control.Phase || request != o.Control.RequestDigest || seal != o.Control.SealDigest {
		t.Fatal("round trip", err)
	}
	raw, _ := json.Marshal(args)
	if invocationDigest(a, phase, request, seal) != controlDigest(raw) {
		t.Fatal("invocation did not bind exact argv")
	}
	for _, bad := range [][]string{nil, args, expected[:4], append(append([]string(nil), expected...), "extra"), {WorkerCommand, phase, request, args[3], "bad"}} {
		if _, _, _, _, err := parseWorkerArgs(bad); err == nil {
			t.Fatal("malformed worker argv", bad)
		}
	}
	changed := o
	changed.Control.SealDigest = controlDigest([]byte("changed-seal"))
	if invocationDigest(a, phase, request, seal) == invocationDigest(a, phase, request, changed.Control.SealDigest) {
		t.Fatal("seal not report-bound")
	}
	for _, token := range []WorkerInvocation{{}, {allowance: a, phase: phase, request: request, seal: seal}} {
		if _, _, _, _, err = token.Binding(t.Context()); err == nil {
			t.Fatal("unminted invocation")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ReadWorkerInvocation(ctx); err == nil {
		t.Fatal("canceled entry")
	}
	if _, err := ReadWorkerInvocation(t.Context()); err == nil {
		t.Fatal("ordinary executable minted authority")
	}
}
