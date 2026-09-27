package planner

import (
	"reflect"
	"slices"
	"testing"
)

func TestNativeRoots(t *testing.T) {
	for _, roots := range [][]string{nil, {"//lib:alias", "//lib:alias"}, {"//lib:all"}, {"//lib:*"}, {"//..."}, {"//lib:alias union //other:x"}, {"//a/../b:x"}, {"@repo//lib:lib"}, {"//lib:lib)"}} {
		if _, err := NativeCommands(roots); err == nil {
			t.Errorf("accepted roots %q", roots)
		}
		if _, err := AssembleRoots(nil, nil, nil, roots); err == nil {
			t.Errorf("assembled invalid roots %q", roots)
		}
	}
	cq, aq, projections := syntheticPlan(t)
	want, err := Assemble(cq, aq, projections)
	if err != nil {
		t.Fatal(err)
	}
	got, err := AssembleRoots(cq, aq, projections, Roots())
	if err != nil || !reflect.DeepEqual(want, got) {
		t.Fatalf("explicit roots changed original plan: %v", err)
	}
	if _, err := AssembleRoots(cq, aq, projections, Roots()[:1]); err == nil {
		t.Fatal("accepted configured nodes outside reduced requested closure")
	}
	old := Commands()
	native, err := NativeCommands(Roots())
	if err != nil || len(native) != len(old) {
		t.Fatal("native command shape", err)
	}
	for i, command := range native {
		if len(command) != len(old[i])+1 || !slices.Equal(command[:len(command)-1], old[i]) || command[len(command)-1] != "--experimental_proto_descriptor_sets_include_source_info" {
			t.Fatal("native command widened original fixed flags")
		}
	}
}
