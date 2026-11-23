package thriftdecl_test

import (
	"os"
	"testing"

	"github.com/bmeddeb/phebs/internal/extract/extractors/thriftdecl"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
)

// TestConflictingDeclarationsGapTheFile pins PHEBS-39: a file that declares
// one identity twice with different attributes is outside the claim (the
// reference compiler rejects it) and becomes one structured gap, instead of
// two conflicting assertions that make the evidence store refuse the
// repository's whole run.
func TestConflictingDeclarationsGapTheFile(t *testing.T) {
	for _, test := range []struct {
		name, source, reason, line string
		lineNumber                 int
	}{
		{
			name: "duplicate struct field id",
			source: `struct Ambiguous {
  1: optional string left
  1: optional string right
}
service S { void f(1: i32 x) }
`,
			reason: "duplicate_field_id", lineNumber: 3, line: "1: optional string right",
		},
		{
			name: "duplicate parameter id",
			source: `service S {
  void f(1: i32 x, 1: string y)
}
`,
			reason: "duplicate_field_id", lineNumber: 2, line: "1: string y)",
		},
		{
			name: "same name declared as struct and exception",
			source: `struct Clash { 1: i32 a }
exception Clash { 1: i32 a }
`,
			reason: "conflicting_declaration", lineNumber: 2, line: "exception Clash { 1: i32 a }",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			facts, coverage := mustExtract(t, singleFileCorpus(test.source))
			if len(facts) != 1 || coverage.UnresolvedCount != 1 {
				t.Fatalf("facts = %d, unresolved = %d; want a single gap fact", len(facts), coverage.UnresolvedCount)
			}
			gap := facts[0]
			if gap.Assertion.Predicate != "THRIFT_EXTRACTION_GAP" || gap.Assertion.Object != test.reason ||
				gap.Assertion.Tier != "unresolved" || gap.Assertion.Lineage != "" {
				t.Fatalf("gap fact = %+v", gap.Assertion)
			}
			assertLineSpan(t, test.source, gap, test.lineNumber, test.line)
		})
	}
}

// Identical repeats stay as they were: both declarations are emitted and the
// store merges their evidence, so a file that staged before keeps its facts.
func TestIdenticalRepeatedDeclarationsAreKept(t *testing.T) {
	source := `struct Same { 1: i32 a }
struct Same { 1: i32 a }
`
	facts, coverage := mustExtract(t, singleFileCorpus(source))
	if coverage.UnresolvedCount != 0 {
		t.Fatalf("unresolved = %d, want 0", coverage.UnresolvedCount)
	}
	counts := map[string]int{}
	for _, fact := range facts {
		if fact.Assertion.Predicate == "THRIFT_EXTRACTION_GAP" {
			t.Fatalf("identical repeat gapped the file: %+v", fact.Assertion)
		}
		counts[fact.Assertion.Predicate+" "+fact.Assertion.Object]++
	}
	if counts["DECLARES_MESSAGE svc.Same"] != 2 || counts["DECLARES_FIELD svc.Same#1"] != 2 {
		t.Fatalf("declarations = %v, want both repeats", counts)
	}
}

// The committed fixture that refused github.com/bmeddeb/phebs on dave: its
// conflicting file gaps, and the rest of the corpus still declares.
func TestPHEBS39FixtureGapsOnlyTheConflictingFile(t *testing.T) {
	read := func(path string) string {
		t.Helper()
		content, err := os.ReadFile("../../../../" + path)
		if err != nil {
			t.Fatal(err)
		}
		return string(content)
	}
	corpus := memoryCorpus{
		repo: "github.com/bmeddeb/phebs", commit: "c0ffee",
		files: map[string]string{
			"spike/t522/testdata/neutral/idl/packet.thrift":  read("spike/t522/testdata/neutral/idl/packet.thrift"),
			"spike/t522/testdata/neutral/idl/renamed.thrift": read("spike/t522/testdata/neutral/idl/renamed.thrift"),
		},
	}
	facts, coverage := mustExtract(t, corpus)
	if coverage.UnresolvedCount != 1 {
		t.Fatalf("unresolved = %d, want 1", coverage.UnresolvedCount)
	}
	var packet, renamed int
	for _, fact := range facts {
		switch fact.Path {
		case "spike/t522/testdata/neutral/idl/packet.thrift":
			packet++
			if fact.Assertion.Object != "duplicate_field_id" || fact.StartLine != 10 {
				t.Fatalf("packet fact = %+v line %d", fact.Assertion, fact.StartLine)
			}
		case "spike/t522/testdata/neutral/idl/renamed.thrift":
			renamed++
		}
	}
	if packet != 1 || renamed == 0 {
		t.Fatalf("packet facts = %d (want 1 gap), renamed facts = %d (want declarations)", packet, renamed)
	}
}

func TestThriftDeclImplementationRevision(t *testing.T) {
	revisioned, ok := thriftdecl.New().(sdk.ImplementationRevisioner)
	if !ok || revisioned.ImplementationRevision() != 1 {
		t.Fatalf("thriftdecl implementation revision = %v, %v; want 1", revisioned, ok)
	}
}
