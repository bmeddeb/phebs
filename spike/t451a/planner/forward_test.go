package planner

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func addTestProjection(t *testing.T, aq *[]byte, projections map[string][]byte, id, configuration uint64, value projection) {
	t.Helper()
	name := "forward-" + string(rune('0'+id)) + ".phebs-plan.json"
	*aq = append(*aq, pbBytes(3, concat(pbInt(1, id), pbString(2, value.Owner)))...)
	*aq = append(*aq, pbBytes(8, concat(pbInt(1, id+4), pbString(2, name), pbInt(3, 4)))...)
	*aq = append(*aq, pbBytes(1, concat(pbInt(1, id), pbInt(2, id+4)))...)
	*aq = append(*aq, pbBytes(2, concat(pbInt(1, id), pbInt(2, 1), pbString(4, "PhebsPlan"), pbInt(5, configuration), pbInt(9, id)))...)
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	projections["bazel-out/cfg/bin/common/"+name] = data
}

func resetPlan(t *testing.T) ([]byte, []byte, map[string][]byte) {
	t.Helper()
	cq, aq, projections := syntheticPlan(t)
	root := canonicalLabel(Roots()[0])
	cq = bytes.Replace(cq, cqTarget(root, "alias", "@@//common:lib"), cqTarget(root, "alias", "@@//wrapper:outer"), 1)
	cq = append(cq, cqTarget("@@//wrapper:outer", "go_reset_target", "@@//wrapper:inner")...)
	cq = append(cq, cqTarget("@@//wrapper:inner", "go_reset_target", "@@//common:lib")...)
	for i, name := range []string{"inner", "outer"} {
		addTestProjection(t, &aq, projections, uint64(i+2), 1, projection{
			Version: "phebs-t451a-projection-v1", Owner: "@@//wrapper:" + name,
			Forward: &ArchiveRef{Owner: "@@//common:lib", Export: "bazel-out/cfg/bin/common/lib.x"},
		})
	}
	return cq, aq, projections
}

func TestForwardedArchiveBindsExactConfiguredOwner(t *testing.T) {
	cq, aq, projections := resetPlan(t)
	plan, err := Assemble(cq, aq, projections)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Units) != 1 {
		t.Fatal("forwarding created a new archive unit")
	}
	for _, target := range plan.Targets {
		if target.Kind == "go_reset_target" || target.Label == canonicalLabel(Roots()[0]) {
			if len(target.Units) != 1 || target.Units[0] != plan.Units[0].ID {
				t.Fatal("lost nested reset/alias package edge")
			}
		}
	}
	for _, tc := range []struct {
		name   string
		mutate func(*[]byte, *[]byte, map[string][]byte)
	}{
		{"outside configured closure", func(cq, _ *[]byte, _ map[string][]byte) {
			*cq = bytes.Replace(*cq, cqTarget("@@//wrapper:inner", "go_reset_target", "@@//common:lib"), cqTarget("@@//wrapper:inner", "go_reset_target"), 1)
		}},
		{"missing export", func(_, _ *[]byte, p map[string][]byte) {
			name := "bazel-out/cfg/bin/common/forward-2.phebs-plan.json"
			p[name] = bytes.ReplaceAll(p[name], []byte("common/lib.x"), []byte("common/missing.x"))
		}},
		{"ambiguous configuration", func(cq, aq *[]byte, p map[string][]byte) {
			h := strings.Repeat("b", 64)
			*cq = append(*cq, frame(pbBytes(2, concat(pbInt(1, 2), pbString(4, h))))...)
			rule := concat(pbString(1, "@@//common:lib"), pbString(2, "go_library"), pbBytes(15, pbString(1, "@@//common:lib.go")))
			*cq = append(*cq, frame(pbBytes(1, concat(pbBytes(1, concat(pbInt(1, 1), pbBytes(2, rule))), pbInt(3, 2))))...)
			innerRule := concat(pbString(1, "@@//wrapper:inner"), pbString(2, "go_reset_target"),
				pbBytes(15, concat(pbString(1, "@@//common:lib"), pbString(2, testHash), pbInt(3, 1))),
				pbBytes(15, concat(pbString(1, "@@//common:lib"), pbString(2, h), pbInt(3, 2))))
			inner := frame(pbBytes(1, concat(pbBytes(1, concat(pbInt(1, 1), pbBytes(2, innerRule))), pbInt(3, 1))))
			*cq = bytes.Replace(*cq, cqTarget("@@//wrapper:inner", "go_reset_target", "@@//common:lib"), inner, 1)
			*aq = append(*aq, pbBytes(5, concat(pbInt(1, 2), pbString(4, h)))...)
			var second projection
			if err := json.Unmarshal(p["bazel-out/cfg/bin/common/lib.phebs-plan.json"], &second); err != nil {
				t.Fatal(err)
			}
			addTestProjection(t, aq, p, 4, 2, second)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cq, aq, p := resetPlan(t)
			tc.mutate(&cq, &aq, p)
			if _, err := Assemble(cq, aq, p); err == nil || !strings.Contains(err.Error(), "forwarded archive owner") {
				t.Fatalf("did not refuse exact forwarding join: %v", err)
			}
		})
	}
}

func TestForwardProjectionReadsNoSourceOrExportBytes(t *testing.T) {
	in := helperInput{Version: "phebs-t451a-projection-input-v1", Owner: "@@//wrapper:reset", Forward: &ArchiveRef{Owner: "@@//common:lib", Export: "bazel-out/cfg/bin/common/lib.x"}}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := project(data, func(string, int) ([]byte, error) { return nil, errors.New("unexpected artifact read") }); err != nil {
		t.Fatal(err)
	}
	in.Forward.Owner = in.Owner
	data, _ = json.Marshal(in)
	if _, err := project(data, nil); err == nil {
		t.Fatal("accepted self forwarding")
	}
}
