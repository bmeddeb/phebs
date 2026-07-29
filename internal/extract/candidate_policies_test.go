package extract

import (
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extract/scipsource"
)

func TestCandidatePoliciesFreezeEnumerationAndRequiredSeparation(t *testing.T) {
	tests := []struct {
		domain string
		plane  candidate.Plane
		yes    []string
		no     []string
	}{
		{
			domain: "proto-contract", plane: candidate.PlaneLocal,
			yes: []string{"api.proto"}, no: []string{"api.go"},
		},
		{
			domain: "grpc-consumer", plane: candidate.PlaneLocal,
			yes: []string{"api.go"}, no: []string{"api.proto"},
		},
		{
			domain: "thrift-contract", plane: candidate.PlaneLocal,
			yes: []string{"api.thrift"}, no: []string{"api.go"},
		},
		{
			domain: "thrift-consumer", plane: candidate.PlaneLocal,
			yes: []string{"api.go"}, no: []string{"api.thrift"},
		},
		{
			domain: "scip-proto-field", plane: candidate.PlaneLocal,
			yes: []string{
				"index.scip", "api.go", "api.proto", "nested/buf.yaml",
			},
			no: []string{"buf.yaml.lock", "api.thrift"},
		},
		{
			domain: "scip-thrift-field", plane: candidate.PlaneLocal,
			yes: []string{"index.scip", "api.go"},
			no:  []string{"api.proto", "api.thrift"},
		},
		{
			domain: "grpc-caller", plane: candidate.PlaneCaller,
			yes: []string{
				"api.go", "go.mod", "nested/go.mod", "index.scip",
				layoutSnapshotPath, unitSnapshotPath,
				generatedFromSnapshotPath,
			},
			no: []string{"nested/index.scip", "api.proto"},
		},
		{
			domain: "thrift-caller", plane: candidate.PlaneCaller,
			yes: []string{
				"api.go", "go.mod", "nested/go.mod", "index.scip",
				layoutSnapshotPath, unitSnapshotPath,
				generatedFromSnapshotPath,
			},
			no: []string{"nested/index.scip", "api.thrift"},
		},
		{
			domain: "kafka-producer", plane: candidate.PlaneLocal,
			yes: []string{"api.go"}, no: []string{"api.proto"},
		},
		{
			domain: "kafka-consumer", plane: candidate.PlaneLocal,
			yes: []string{"api.go"}, no: []string{"api.proto"},
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.domain, func(t *testing.T) {
			extractor := unitExtractor{
				domain: testCase.domain, version: "test-v1",
				candidate: func(filePath string) bool {
					return filePath == "index.scip"
				},
			}
			policies, err := CandidatePolicies([]Extractor{extractor})
			if err != nil {
				t.Fatal(err)
			}
			if len(policies) != 1 ||
				policies[0].Plane != testCase.plane ||
				!strings.HasSuffix(policies[0].EnumerationPolicy, "-v1") ||
				policies[0].SymlinkPolicy != fixedRootSymlinkPolicy ||
				policies[0].RejectSymlink == nil {
				t.Fatalf("policy = %+v", policies)
			}
			for _, filePath := range testCase.yes {
				if !policies[0].Enumerate(filePath) {
					t.Errorf("Enumerate(%q) = false", filePath)
				}
			}
			for _, filePath := range testCase.no {
				if policies[0].Enumerate(filePath) {
					t.Errorf("Enumerate(%q) = true", filePath)
				}
			}
			if !policies[0].Required("index.scip") ||
				policies[0].Required("not-required.go") {
				t.Fatal("planner widened or narrowed extractor Candidate")
			}
			if !policies[0].RejectSymlink("index.scip") ||
				!policies[0].RejectSymlink(layoutSnapshotPath) ||
				policies[0].RejectSymlink("api.go") {
				t.Fatal("planner changed fixed-root symlink posture")
			}
		})
	}
}

func TestCandidatePoliciesRejectUnknownOrDuplicateDomains(t *testing.T) {
	unknown := unitExtractor{domain: "future-domain", version: "1"}
	if _, err := CandidatePolicies([]Extractor{unknown}); err == nil ||
		!strings.Contains(err.Error(), "no candidate enumeration policy") {
		t.Fatalf("unknown domain error = %v", err)
	}
	duplicate := unitExtractor{domain: "proto-contract", version: "1"}
	if _, err := CandidatePolicies([]Extractor{duplicate, duplicate}); err == nil ||
		!strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate domain error = %v", err)
	}
}

func TestCandidatePoliciesMatchSCIPSourceEligibility(t *testing.T) {
	t.Parallel()
	tests := []struct {
		domain string
		policy scipsource.Policy
	}{
		{domain: "scip-proto-field", policy: scipsource.ProtoField},
		{domain: "scip-thrift-field", policy: scipsource.Go},
		{domain: "grpc-caller", policy: scipsource.Go},
		{domain: "thrift-caller", policy: scipsource.Go},
	}
	sourcePaths := []string{
		"api/service.proto",
		"consumer/use.go",
		"consumer/Use.java",
		"consumer/use.ts",
		"consumer/use.go.bak",
	}
	for _, testCase := range tests {
		t.Run(testCase.domain, func(t *testing.T) {
			t.Parallel()
			policies, err := CandidatePolicies([]Extractor{unitExtractor{
				domain: testCase.domain, version: "test-v1",
				candidate: func(filePath string) bool {
					return filePath == scipIndexPath
				},
			}})
			if err != nil {
				t.Fatal(err)
			}
			for _, filePath := range sourcePaths {
				want := scipsource.Eligible(testCase.policy, filePath)
				if got := policies[0].Enumerate(filePath); got != want {
					t.Errorf(
						"Enumerate(%q) = %t, source eligibility = %t",
						filePath, got, want,
					)
				}
			}
		})
	}

	protoPolicies, err := CandidatePolicies([]Extractor{unitExtractor{
		domain: "scip-proto-field", version: "test-v1",
		candidate: func(filePath string) bool {
			return filePath == scipIndexPath
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, ancillary := range []string{scipIndexPath, "nested/buf.yaml"} {
		if !protoPolicies[0].Enumerate(ancillary) ||
			scipsource.Eligible(scipsource.ProtoField, ancillary) {
			t.Errorf("ancillary path %q changed source eligibility", ancillary)
		}
	}
}
