package scipsource

import "testing"

func TestEligible(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		policy Policy
		path   string
		want   bool
	}{
		{name: "Go source", policy: Go, path: "src/use.go", want: true},
		{name: "Go rejects proto", policy: Go, path: "api/service.proto"},
		{name: "protobuf field Go source", policy: ProtoField, path: "src/use.go", want: true},
		{name: "protobuf field declaration", policy: ProtoField, path: "api/service.proto", want: true},
		{name: "foreign Java", policy: ProtoField, path: "src/Use.java"},
		{name: "suffix is exact", policy: Go, path: "src/use.go.bak"},
		{name: "root index is ancillary", policy: ProtoField, path: "index.scip"},
		{name: "Buf module is ancillary", policy: ProtoField, path: "api/buf.yaml"},
		{name: "unknown policy", path: "src/use.go"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if got := Eligible(testCase.policy, testCase.path); got != testCase.want {
				t.Fatalf(
					"Eligible(%d, %q) = %t, want %t",
					testCase.policy, testCase.path, got, testCase.want,
				)
			}
		})
	}
}
