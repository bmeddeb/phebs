package store

import (
	"reflect"
	"testing"
)

func TestMergeAtomRefsCanonicalizesArrays(t *testing.T) {
	cases := []struct {
		name   string
		left   []string
		right  []string
		want   []string
		nonNil bool
	}{
		{
			name: "nil inputs become an empty array",
			want: []string{}, nonNil: true,
		},
		{
			name: "inputs merge sorted and unique",
			left: []string{"ea_b", "ea_a"}, right: []string{"ea_b", "ea_c"},
			want: []string{"ea_a", "ea_b", "ea_c"}, nonNil: true,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := mergeAtomRefs(testCase.left, testCase.right)
			if !reflect.DeepEqual(got, testCase.want) {
				t.Fatalf("mergeAtomRefs(%v, %v) = %#v, want %#v",
					testCase.left, testCase.right, got, testCase.want)
			}
			if testCase.nonNil && got == nil {
				t.Fatal("mergeAtomRefs returned a nil slice")
			}
		})
	}
}
