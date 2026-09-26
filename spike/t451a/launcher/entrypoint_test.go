package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/spike/t451a/planner"
)

func TestEntrypointRefusesUnsealedInputsBeforeExecution(t *testing.T) {
	plan, roots := fixture()
	p, err := Prepare(plan, roots)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(entrypointPlan{"phebs-t451a-driver-plan-v1", plan, roots})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inspectEntrypoint(data, hash(data), p.patterns, append(p.request, '\n')); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		data    []byte
		digest  string
		args    []string
		request string
	}{
		{"replaced plan", data, strings.Repeat("0", 64), p.patterns, string(p.request)},
		{"extra pattern", data, hash(data), append(append([]string{}, p.patterns...), "unselected/branch"), string(p.request)},
		{"missing pattern", data, hash(data), nil, string(p.request)},
		{"arbitrary argv", data, hash(data), []string{"--bazelrc=/outside"}, string(p.request)},
		{"request env", data, hash(data), p.patterns, `{"mode":31,"env":["GOTAGS=hostile"],"build_flags":[],"tests":false,"overlay":{}}`},
		{"build flags", data, hash(data), p.patterns, `{"mode":31,"env":[],"build_flags":["-tags=hostile"],"tests":false,"overlay":{}}`},
		{"overlay", data, hash(data), p.patterns, `{"mode":31,"env":[],"build_flags":[],"tests":false,"overlay":{"source.go":""}}`},
		{"tests", data, hash(data), p.patterns, strings.Replace(string(p.request), `"tests":false`, `"tests":true`, 1)},
		{"mode", data, hash(data), p.patterns, strings.Replace(string(p.request), `"mode":31`, `"mode":1023`, 1)},
		{"case alias", data, hash(data), p.patterns, strings.Replace(string(p.request), `"mode":31`, `"mode":31,"Mode":31`, 1)},
		{"trailing object", data, hash(data), p.patterns, string(p.request) + `{}`},
		{"oversized request", data, hash(data), p.patterns, string(p.request) + strings.Repeat(" ", maxRequestBytes)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := inspectEntrypoint(tc.data, tc.digest, tc.args, []byte(tc.request)); err == nil {
				t.Fatal("accepted an unclosed endpoint input")
			}
		})
	}
	for _, changed := range [][]byte{
		append(append([]byte{}, data...), '\n'),
		[]byte(strings.Replace(string(data), `"version":`, `"Version":`, 1)),
		[]byte(strings.Replace(string(data), `"version":`, `"extra":true,"version":`, 1)),
	} {
		if _, err := inspectEntrypoint(changed, hash(changed), p.patterns, p.request); err == nil {
			t.Fatal("accepted malformed/noncanonical plan despite matching digest")
		}
	}
}

func TestEntrypointLossyConfigurationRefusalPrecedesHostAndFileAccess(t *testing.T) {
	plan, roots := fixture()
	u := plan.Units[0]
	u.ID = "second"
	u.Owner.Configuration = strings.Repeat("b", 64)
	plan.Units = append(plan.Units, u)
	plan.Targets = append(plan.Targets, planner.Target{Configured: u.Owner, Kind: "go_library", Units: []string{u.ID}})
	roots = append(roots, u.Owner)
	seal(&plan)
	if _, err := RunThroughEntrypoint(context.Background(), plan, roots); !errors.Is(err, ErrUnrepresentable) {
		t.Fatalf("did not refuse before endpoint execution: %v", err)
	}
}
