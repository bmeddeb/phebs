package store

import (
	"strings"
	"testing"
)

func TestValidateCandidateCoveragePinsExactScopeAndTypedReceipt(t *testing.T) {
	digest := func(value string) string {
		return "sha256:" + strings.Repeat(value, 64)
	}
	valid := CoverageManifest{
		CorpusFileCount:          3,
		CandidateFileCount:       2,
		CandidateManifestDigest:  digest("a"),
		ScopePosture:             "focused-local",
		CandidatePlane:           "local",
		ScopeCorpusFileCount:     3,
		ScopeCorpusDeclaredBytes: 30,
		ScopeCorpusDigest:        digest("b"),
		PlannedFileCount:         2,
		PlannedRequiredFileCount: 2,
		PlannedDeclaredBytes:     20,
		PlannedScopeDigest:       digest("c"),
		TypedInputKind:           "scip",
		TypedInputPath:           "service/support/unit.scip",
		TypedInputObjectID:       strings.Repeat("d", 40),
		TypedInputDeclaredBytes:  10,
		TypedInputDigest:         digest("e"),
		TypedInputPresent:        true,
	}
	focusedRun := &ExtractionRun{UnitDigest: digest("f")}

	tests := []struct {
		name   string
		mutate func(*CoverageManifest, *ExtractionRun)
	}{
		{name: "valid"},
		{name: "manifest digest", mutate: func(got *CoverageManifest, _ *ExtractionRun) {
			got.CandidateManifestDigest = "not-a-digest"
		}},
		{name: "focused posture", mutate: func(got *CoverageManifest, _ *ExtractionRun) {
			got.ScopePosture = "whole-repository"
		}},
		{name: "candidate plane", mutate: func(got *CoverageManifest, _ *ExtractionRun) {
			got.CandidatePlane = "unknown"
		}},
		{name: "scope corpus count", mutate: func(got *CoverageManifest, _ *ExtractionRun) {
			got.ScopeCorpusFileCount--
		}},
		{name: "planned required count", mutate: func(got *CoverageManifest, _ *ExtractionRun) {
			got.PlannedRequiredFileCount--
		}},
		{name: "planned bytes", mutate: func(got *CoverageManifest, _ *ExtractionRun) {
			got.PlannedDeclaredBytes = got.ScopeCorpusDeclaredBytes + 1
		}},
		{name: "typed kind", mutate: func(got *CoverageManifest, _ *ExtractionRun) {
			got.TypedInputKind = "other"
		}},
		{name: "typed path", mutate: func(got *CoverageManifest, _ *ExtractionRun) {
			got.TypedInputPath = "../unit.scip"
		}},
		{name: "typed object", mutate: func(got *CoverageManifest, _ *ExtractionRun) {
			got.TypedInputObjectID = strings.Repeat("g", 40)
		}},
		{name: "typed digest", mutate: func(got *CoverageManifest, _ *ExtractionRun) {
			got.TypedInputDigest = ""
		}},
		{name: "absent typed content identity", mutate: func(got *CoverageManifest, _ *ExtractionRun) {
			got.TypedInputPresent = false
		}},
		{name: "whole run cannot claim focused", mutate: func(_ *CoverageManifest, run *ExtractionRun) {
			run.UnitDigest = ""
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := valid
			run := *focusedRun
			if test.mutate != nil {
				test.mutate(&got, &run)
			}
			err := validateCandidateCoverage(got, &run)
			if test.mutate == nil {
				if err != nil {
					t.Fatalf("valid candidate coverage: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("invalid candidate coverage unexpectedly accepted")
			}
		})
	}
}

func TestValidateCandidateCoverageRejectsUnboundFields(t *testing.T) {
	run := &ExtractionRun{}
	if err := validateCandidateCoverage(CoverageManifest{
		ScopePosture: "whole-repository",
	}, run); err == nil {
		t.Fatal("candidate scope without manifest digest unexpectedly accepted")
	}

	if err := validateCandidateCoverage(CoverageManifest{}, run); err != nil {
		t.Fatalf("legacy coverage without candidate scope: %v", err)
	}

	run.UnitDigest = "sha256:" + strings.Repeat("f", 64)
	if err := validateCandidateCoverage(CoverageManifest{}, run); err == nil {
		t.Fatal("focused coverage without candidate scope unexpectedly accepted")
	}
}

func TestValidateCandidateCoverageBindsFocusedExclusions(t *testing.T) {
	digest := func(value string) string {
		return "sha256:" + strings.Repeat(value, 64)
	}
	base := CoverageManifest{
		CorpusFileCount:             4,
		CandidateFileCount:          1,
		CandidateManifestDigest:     digest("a"),
		ScopePosture:                "focused-local",
		CandidatePlane:              "local",
		ScopeCorpusFileCount:        4,
		ScopeCorpusDeclaredBytes:    40,
		ScopeCorpusDigest:           digest("b"),
		PlannedFileCount:            2,
		PlannedRequiredFileCount:    2,
		PlannedDeclaredBytes:        20,
		PlannedScopeDigest:          digest("c"),
		ExcludedSourceFileCount:     1,
		ExcludedSourceRequiredCount: 1,
		ExcludedSourceDeclaredBytes: 9,
	}
	run := &ExtractionRun{UnitDigest: digest("f")}
	if err := validateCandidateCoverage(base, run); err != nil {
		t.Fatalf("valid source exclusions: %v", err)
	}

	scip := base
	scip.CandidateFileCount = 0
	scip.PlannedRequiredFileCount = 1
	scip.ExcludedSCIPDocumentCount = 1
	scip.ExcludedSCIPDefinitionCount = 2
	scip.ExcludedSCIPOccurrenceCount = 3
	scip.TypedInputKind = "scip"
	scip.TypedInputPath = "service/index.scip"
	scip.TypedInputObjectID = strings.Repeat("d", 40)
	scip.TypedInputDeclaredBytes = 10
	scip.TypedInputDigest = digest("e")
	scip.TypedInputPresent = true
	if err := validateCandidateCoverage(scip, run); err != nil {
		t.Fatalf("valid SCIP exclusions: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*CoverageManifest)
	}{
		{
			name: "non-focused",
			mutate: func(got *CoverageManifest) {
				got.ScopePosture = "repository-overlay"
				got.CandidatePlane = "caller"
			},
		},
		{
			name: "too many excluded files",
			mutate: func(got *CoverageManifest) {
				got.ExcludedSourceFileCount = got.PlannedFileCount + 1
			},
		},
		{
			name: "excluded required exceeds files",
			mutate: func(got *CoverageManifest) {
				got.ExcludedSourceRequiredCount = 2
			},
		},
		{
			name: "included required mismatch",
			mutate: func(got *CoverageManifest) {
				got.CandidateFileCount++
			},
		},
		{
			name: "excluded bytes exceed plan",
			mutate: func(got *CoverageManifest) {
				got.ExcludedSourceDeclaredBytes =
					got.PlannedDeclaredBytes + 1
			},
		},
		{
			name: "excluded bytes without file",
			mutate: func(got *CoverageManifest) {
				got.ExcludedSourceFileCount = 0
				got.ExcludedSourceRequiredCount = 0
				got.ExcludedSourceDeclaredBytes = 1
				got.CandidateFileCount = got.PlannedRequiredFileCount
			},
		},
		{
			name: "definitions exceed occurrences",
			mutate: func(got *CoverageManifest) {
				got.ExcludedSCIPDefinitionCount =
					got.ExcludedSCIPOccurrenceCount + 1
			},
		},
		{
			name: "SCIP semantics without document",
			mutate: func(got *CoverageManifest) {
				got.ExcludedSCIPDocumentCount = 0
				got.ExcludedSCIPDefinitionCount = 0
				got.ExcludedSCIPOccurrenceCount = 1
			},
		},
		{
			name: "SCIP counts without typed input",
			mutate: func(got *CoverageManifest) {
				got.TypedInputKind = ""
				got.TypedInputPath = ""
				got.TypedInputObjectID = ""
				got.TypedInputDeclaredBytes = 0
				got.TypedInputDigest = ""
				got.TypedInputPresent = false
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := scip
			test.mutate(&got)
			if err := validateCandidateCoverage(got, run); err == nil {
				t.Fatal("invalid exclusion coverage unexpectedly accepted")
			}
		})
	}
}
