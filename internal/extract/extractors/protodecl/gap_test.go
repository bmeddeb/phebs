package protodecl_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/extract/extractors/protodecl"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
)

// PHEBS-40: an extension group or a conflicting repeated declaration makes its
// file one explicit gap; the rest of the corpus still publishes.

const gapGoodFile = `syntax = "proto3";
package demo;
service Greeter { rpc Hello(Req) returns (Res); }
message Req { string name = 1; }
message Res { string text = 1; }
`

func gapsOf(facts []sdk.Fact, path string) (gaps, others []sdk.Fact) {
	for _, fact := range facts {
		if fact.Path != path {
			continue
		}
		if fact.Assertion.Predicate == "PROTO_EXTRACTION_GAP" {
			gaps = append(gaps, fact)
		} else {
			others = append(others, fact)
		}
	}
	return gaps, others
}

func TestFileGapsKeepTheRestOfTheRun(t *testing.T) {
	tests := []struct {
		name, content, reason string
		line                  int
		cited                 string
	}{
		{
			name: "extension group",
			content: `syntax = "proto2"; package demo;
message Base { extensions 100 to max; }
extend Base {
  optional group Grp = 100 { optional string x = 1; }
}`,
			reason: "extension_group", line: 4, cited: "optional group Grp = 100",
		},
		{
			name: "duplicate field number",
			content: `syntax = "proto2"; package demo;
message Foo {
  optional string a = 1;
  optional int32 b = 1;
}`,
			reason: "duplicate_field_number", line: 4, cited: "optional int32 b = 1;",
		},
		{
			name: "operation declared twice differently",
			content: `syntax = "proto3"; package demo;
message A {} message B {}
service S {
  rpc Get(A) returns (A);
  rpc Get(B) returns (B);
}`,
			reason: "conflicting_declaration", line: 5, cited: "rpc Get(B) returns (B);",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			facts, coverage, err := extractFacts(t, memoryCorpus{
				repo: "r", commit: "c", files: map[string]string{
					"good.proto": gapGoodFile,
					"bad.proto":  test.content,
				},
			})
			if err != nil {
				t.Fatalf("Extract refused the run: %v", err)
			}
			if len(coverage.Protocols) == 0 || coverage.UnresolvedCount != 1 {
				t.Fatalf("coverage = %+v, want protocols and one unresolved gap", coverage)
			}
			gaps, others := gapsOf(facts, "bad.proto")
			if len(gaps) != 1 || len(others) != 0 {
				t.Fatalf("bad.proto facts: %d gaps, %d others; want exactly one gap", len(gaps), len(others))
			}
			gap := gaps[0]
			a := gap.Assertion
			if a.Object != test.reason || a.Subject != "bad.proto" || a.Tier != "unresolved" ||
				a.Lineage != "" || gap.Atom.RuleID != "proto-file-gap-v1" ||
				gap.Atom.FactFingerprint != "PROTO_EXTRACTION_GAP|"+test.reason {
				t.Fatalf("gap = %+v / %+v", a, gap.Atom)
			}
			var detail struct{ Schema, Reason string }
			if err := json.Unmarshal([]byte(a.Detail), &detail); err != nil ||
				detail.Schema != "proto-gap-detail-v1" || detail.Reason != test.reason {
				t.Fatalf("gap detail = %s (%v)", a.Detail, err)
			}
			if gap.StartLine != test.line {
				t.Fatalf("gap cited at line %d, want %d", gap.StartLine, test.line)
			}
			if cited := test.content[gap.Atom.StartByte:gap.Atom.EndByte]; !strings.HasPrefix(cited, test.cited) {
				t.Fatalf("gap cites %q, want prefix %q", cited, test.cited)
			}
			_, goodFacts := gapsOf(facts, "good.proto")
			if len(goodFacts) == 0 {
				t.Fatal("the clean file published nothing")
			}
		})
	}
}

func TestCleanFilesAreUnchangedByTheGapPath(t *testing.T) {
	// The same clean file alone and next to a gapped file yields identical facts.
	alone, _, err := extractFacts(t, memoryCorpus{
		repo: "r", commit: "c", files: map[string]string{"good.proto": gapGoodFile},
	})
	if err != nil {
		t.Fatal(err)
	}
	mixed, _, err := extractFacts(t, memoryCorpus{
		repo: "r", commit: "c", files: map[string]string{
			"good.proto": gapGoodFile,
			"bad.proto":  "syntax = \"proto3\"; message M { string a = 1; int32 b = 1; }",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, mixedGood := gapsOf(mixed, "good.proto")
	want, _ := json.Marshal(alone)
	got, _ := json.Marshal(mixedGood)
	if string(want) != string(got) {
		t.Fatalf("clean file facts changed next to a gapped file:\nalone %s\nmixed %s", want, got)
	}
}

func TestIdenticalRepeatedDeclarationsAreStillDeclared(t *testing.T) {
	// Two identical messages repeat one identity with identical attributes;
	// the store merges them, so the file is declared, not gapped.
	facts, _, err := extractFacts(t, memoryCorpus{
		repo: "r", commit: "c", files: map[string]string{
			"dup.proto": `syntax = "proto3"; package demo;
message Same { string a = 1; }
message Same { string a = 1; }`,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	gaps, others := gapsOf(facts, "dup.proto")
	if len(gaps) != 0 || len(others) == 0 {
		t.Fatalf("identical repeats: %d gaps, %d declarations", len(gaps), len(others))
	}
}

func TestParseAndMalformedInputsStillRefuseTheRun(t *testing.T) {
	// The narrow PHEBS-40 posture: only conflicts and extension groups gap.
	for name, content := range map[string]string{
		"parse error": `syntax = "proto3"; message`,
		"map field in extension body": `syntax = "proto2"; package demo;
message Base { extensions 100 to max; }
extend Base { map<string, string> m = 100; }`,
	} {
		t.Run(name, func(t *testing.T) {
			_, coverage, err := extractFacts(t, memoryCorpus{
				repo: "r", commit: "c", files: map[string]string{
					"good.proto": gapGoodFile, "bad.proto": content,
				},
			})
			if err == nil || len(coverage.Protocols) != 0 {
				t.Fatalf("Extract = %v, coverage %+v; want a whole-run refusal", err, coverage)
			}
		})
	}
}

func TestProtoDeclImplementationRevision(t *testing.T) {
	revisioner, ok := protodecl.New().(sdk.ImplementationRevisioner)
	if !ok || revisioner.ImplementationRevision() != 1 {
		t.Fatal("protodecl must declare implementation revision 1 (PHEBS-40)")
	}
	if protodecl.New().Version() != "4.0.0" {
		t.Fatal("the signed recipe binds proto-contract@4.0.0; the version must not move")
	}
}
