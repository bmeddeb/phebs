package extract

import (
	"reflect"
	"strings"
	"testing"
)

func TestConsumerUnitProjectionPreservesLegacySemantics(t *testing.T) {
	content := `{"version":"t20-unit-snapshot-v1","mappings":[
	{"source_path":"src/a.go","build_targets":["go:base"]},
	{"source_path":"src/a.go","start_line":5,"build_targets":["go:line"]},
	{"source_path":"src/a.go","start_line":5,"owners":["owner"]},
	{"source_path":"src/none.go","state":"unavailable","source":"unit-snapshot-v1"}]}`
	projection, err := DecodeConsumerUnitSnapshot(t.Context(), content)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		path          string
		line          int
		state, reason string
		count         int
	}{
		{"src/a.go", 1, "resolved", "", 1}, {"src/a.go", 5, "ambiguous", "multiple_unit_candidates", 2},
		{"src/none.go", 2, "unavailable", "mapping_has_no_attribution", 1}, {"src/absent.go", 1, "unavailable", "no_unit_mapping", 0},
		{"../escape", 1, "unavailable", "invalid_source_location", 0},
	} {
		t.Run(test.path+test.reason, func(t *testing.T) {
			got := projection.ConsumerUnits(test.path, test.line)
			if got.State != test.state || got.Reason != test.reason || len(got.Candidates) != test.count {
				t.Fatalf("attribution=%+v", got)
			}
		})
	}
	rows := projection.Mappings()
	reopened, err := OpenConsumerUnitProjection(t.Context(), rows)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reopened.ConsumerUnits("src/a.go", 5), projection.ConsumerUnits("src/a.go", 5)) {
		t.Fatal("catalog roundtrip changed attribution")
	}
	rows[0].Candidate.BuildTargets[0] = "changed"
	if projection.ConsumerUnits("src/a.go", 1).Candidates[0].BuildTargets[0] != "go:base" {
		t.Fatal("returned metadata aliases projection")
	}
}

func TestConsumerUnitProjectionRejectsMalformedAndNoncanonical(t *testing.T) {
	for _, content := range []string{
		`{"version":"t20-unit-snapshot-v1","mappings":[],"unknown":1}`,
		`{"version":"t20-unit-snapshot-v1","version":"t20-unit-snapshot-v1","mappings":[]}`,
		`{"version":"future","mappings":[]}`,
		`{"version":"t20-unit-snapshot-v1","mappings":[{"source_path":"a.go","build_targets":["x","x"]}]}`,
		`{"version":"t20-unit-snapshot-v1","mappings":[{"source_path":"a.go"},{"source_path":"a.go"}]}`,
		`{"version":"t20-unit-snapshot-v1","mappings":[{"source_path":"../escape.go"}]}`,
		strings.Repeat(" ", MaxConsumerUnitSnapshotBytes+1),
	} {
		if _, err := DecodeConsumerUnitSnapshot(t.Context(), content); err == nil {
			t.Fatal("malformed snapshot accepted")
		}
	}
	projection, err := DecodeConsumerUnitSnapshot(t.Context(), `{"version":"t20-unit-snapshot-v1","mappings":[{"source_path":"a.go","build_targets":["x"]}]}`)
	if err != nil {
		t.Fatal(err)
	}
	rows := projection.Mappings()
	rows[0].Candidate.ID = "au_" + strings.Repeat("0", 64)
	if _, err := OpenConsumerUnitProjection(t.Context(), rows); err == nil {
		t.Fatal("changed normalized ID accepted")
	}
}

func TestConsumerUnitProjectionStopsBeforeCollectionOverflowDecode(t *testing.T) {
	content := `{"version":"t20-unit-snapshot-v1","mappings":[` + strings.Repeat(`{"source_path":"a.go"},`, MaxConsumerUnitMappings) + `{"source_path":`
	if _, err := DecodeConsumerUnitSnapshot(t.Context(), content); err == nil || !strings.Contains(err.Error(), "collection limit") {
		t.Fatalf("mapping overflow decoded remaining invalid tail: %v", err)
	}
	content = `{"version":"t20-unit-snapshot-v1","mappings":[{"source_path":"a.go","owners":[` + strings.Repeat(`"x",`, maxAttributionValues) + `{invalid`
	if _, err := DecodeConsumerUnitSnapshot(t.Context(), content); err == nil || !strings.Contains(err.Error(), "collection limit") {
		t.Fatalf("value overflow decoded remaining invalid tail: %v", err)
	}
}
