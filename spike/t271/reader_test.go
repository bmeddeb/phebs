package t271

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

type fixtureCase struct {
	ID                string   `json:"id"`
	Input             *string  `json:"input"`
	Context           Source   `json:"context"`
	Expect            string   `json:"expect"`
	Labels            string   `json:"labels"`
	Declarations      int      `json:"declarations"`
	Refusal           *Refusal `json:"refusal"`
	State             string   `json:"state"`
	Checklist         string   `json:"checklist"`
	FixtureSHA256     string   `json:"fixture_sha256"`
	ExpectedFacts     int      `json:"expected_facts"`
	ExpectedCitations int      `json:"expected_citations"`
}

func readFile(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func decodeJSON(t *testing.T, raw []byte, target any) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		t.Fatal(err)
	}
}

func encodeJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func canonicalJSON(t *testing.T, raw []byte) []byte {
	t.Helper()
	var value any
	decodeJSON(t, raw, &value)
	return encodeJSON(t, value)
}

func fixtureCases(t *testing.T) []fixtureCase {
	t.Helper()
	var index struct {
		Cases []fixtureCase `json:"cases"`
	}
	decodeJSON(t, readFile(t, "cases.json"), &index)
	if len(index.Cases) != 119 {
		t.Fatalf("frozen case count %d", len(index.Cases))
	}
	return index.Cases
}

func TestPreregistrationInventory(t *testing.T) {
	raw := readFile(t, "preregistration.json")
	if digest(raw) != "7ef0df8be38bc51494e63a57173c120c5ed2e98b832822251bdb9f6ca9555b3b" {
		t.Fatal("reviewed preregistration was changed")
	}
	var index struct {
		Files []struct {
			Path   string `json:"path"`
			Bytes  int    `json:"bytes"`
			SHA256 string `json:"sha256"`
		} `json:"files"`
	}
	decodeJSON(t, raw, &index)
	if len(index.Files) != 142 {
		t.Fatalf("frozen inventory count %d", len(index.Files))
	}
	for _, file := range index.Files {
		data := readFile(t, file.Path)
		if len(data) != file.Bytes || digest(data) != file.SHA256 {
			t.Errorf("frozen input changed: %s", file.Path)
		}
	}
}

type declarationLabel struct {
	Kind    string          `json:"kind"`
	Pointer string          `json:"pointer"`
	Owner   *Owner          `json:"owner"`
	Meaning json.RawMessage `json:"meaning"`
	Span    struct {
		Start  int    `json:"start"`
		End    int    `json:"end"`
		SHA256 string `json:"sha256"`
	} `json:"span"`
}

func assertAccepted(t *testing.T, fixture fixtureCase, raw []byte, result Result) {
	t.Helper()
	var labels struct {
		FixtureSHA256 string             `json:"fixture_sha256"`
		Declarations  []declarationLabel `json:"declarations"`
	}
	decodeJSON(t, readFile(t, fixture.Labels), &labels)
	if digest(raw) != labels.FixtureSHA256 || result.BlobSHA256 != labels.FixtureSHA256 {
		t.Fatal("fixture/blob digest mismatch")
	}
	if result.State != "accepted" || result.Refusal != nil || result.Source != fixture.Context ||
		len(result.Facts) != fixture.Declarations || len(result.Citations) != fixture.Declarations ||
		len(labels.Declarations) != fixture.Declarations || result.Catalog.SchemaID == "" {
		t.Fatalf("accepted shape/count mismatch: state=%s refusal=%v facts=%d citations=%d expected=%d", result.State, result.Refusal, len(result.Facts), len(result.Citations), fixture.Declarations)
	}
	expected := make(map[string]declarationLabel, len(labels.Declarations))
	for _, label := range labels.Declarations {
		expected[label.Kind+"\x00"+label.Pointer] = label
	}
	ids := make(map[string]bool, len(result.Facts))
	for index, fact := range result.Facts {
		citation := result.Citations[index]
		label, exists := expected[fact.Kind+"\x00"+citation.Pointer]
		if !exists {
			t.Fatalf("unlabelled declaration %s at %s", fact.Kind, citation.Pointer)
		}
		delete(expected, fact.Kind+"\x00"+citation.Pointer)
		if ids[fact.ID] || len(fact.ID) != 64 || citation.FactID != fact.ID ||
			index > 0 && result.Facts[index-1].ID >= fact.ID {
			t.Fatal("unstable, duplicate or unsorted identity")
		}
		ids[fact.ID] = true
		if !reflect.DeepEqual(fact.Owner, label.Owner) ||
			!bytes.Equal(canonicalJSON(t, fact.Meaning), canonicalJSON(t, label.Meaning)) {
			t.Fatalf("declaration differs from frozen label: %s %s\nactual %s\nlabel %s", fact.Kind, citation.Pointer, fact.Meaning, label.Meaning)
		}
		if citation.Source != fixture.Context || citation.BlobSHA256 != result.BlobSHA256 ||
			citation.Start != label.Span.Start || citation.End != label.Span.End || citation.SHA256 != label.Span.SHA256 ||
			citation.Start < 0 || citation.End <= citation.Start || citation.End > len(raw) {
			t.Fatalf("citation differs from independently frozen encoder offsets: %s", citation.Pointer)
		}
		span := raw[citation.Start:citation.End]
		if !json.Valid(span) || digest(span) != citation.SHA256 {
			t.Fatalf("citation round-trip failed: %s", citation.Pointer)
		}
	}
	if len(expected) != 0 {
		t.Fatal("frozen declarations were omitted")
	}
	ownerCount, fieldCount := 0, 0
	for _, collection := range [][]CatalogOwner{result.Catalog.Tables, result.Catalog.Views} {
		for index, owner := range collection {
			if owner.ID == "" || !ids[owner.ID] || index > 0 && collection[index-1].Name >= owner.Name {
				t.Fatal("catalog owner identity/order mismatch")
			}
			ownerCount++
			for _, field := range owner.Fields {
				if !ids[field.ID] {
					t.Fatal("catalog invented an uncited field")
				}
				fieldCount++
			}
			for _, declaration := range owner.Declarations {
				if !ids[declaration.ID] {
					t.Fatal("catalog invented an uncited edge")
				}
			}
		}
	}
	var wantOwners, wantFields int
	for _, fact := range result.Facts {
		switch fact.Kind {
		case "DECLARES_TABLE", "DECLARES_VIEW":
			wantOwners++
		case "DECLARES_FIELD", "DECLARES_VIEW_FIELD":
			wantFields++
		}
	}
	if ownerCount != wantOwners || fieldCount != wantFields {
		t.Fatal("catalog lost a declaration")
	}
}

func assertEmptyPublication(t *testing.T, result Result) {
	t.Helper()
	if len(result.Facts) != 0 || len(result.Citations) != 0 || result.Catalog.SchemaID != "" ||
		len(result.Catalog.Tables) != 0 || len(result.Catalog.Views) != 0 {
		t.Fatal("refusal/missing state published partial evidence")
	}
}

type passMeasurement struct {
	Pass              int            `json:"pass"`
	Cases             int            `json:"cases"`
	Accepted          int            `json:"accepted"`
	Refused           int            `json:"refused"`
	Missing           int            `json:"missing"`
	Declarations      int            `json:"declarations"`
	Citations         int            `json:"citations"`
	InputBytes        int            `json:"input_bytes"`
	EncodedBytes      int            `json:"encoded_bytes"`
	MaximumOutput     int            `json:"maximum_result_bytes"`
	AllocatedBytes    uint64         `json:"observed_read_allocation_bytes"`
	MaximumAllocation uint64         `json:"maximum_observed_read_allocation_bytes"`
	ReaderNanoseconds int64          `json:"reader_nanoseconds"`
	CanonicalSHA256   string         `json:"canonical_sha256"`
	RefusalCensus     map[string]int `json:"refusal_census"`
}

func TestFrozenCasesTwice(t *testing.T) {
	cases := fixtureCases(t)
	prior := make(map[string][]byte, len(cases))
	measurements := make([]passMeasurement, 0, 2)
	var comparisonBytes []byte
	for pass := 1; pass <= 2; pass++ {
		measurement := passMeasurement{Pass: pass, RefusalCensus: make(map[string]int)}
		results := make(map[string]Result, len(cases))
		var canonical bytes.Buffer
		for _, fixture := range cases {
			t.Run(strconvPass(pass)+"/"+fixture.ID, func(t *testing.T) {
				var raw []byte
				if fixture.Input != nil {
					raw = readFile(t, *fixture.Input)
					if len(raw) == 0 {
						raw = []byte{}
					}
					if fixture.FixtureSHA256 != "" && digest(raw) != fixture.FixtureSHA256 {
						t.Fatal("refusal fixture digest changed")
					}
				}
				var memoryBefore, memoryAfter runtime.MemStats
				runtime.ReadMemStats(&memoryBefore)
				began := time.Now()
				result := Read(fixture.Context, raw)
				measurement.ReaderNanoseconds += time.Since(began).Nanoseconds()
				runtime.ReadMemStats(&memoryAfter)
				allocated := memoryAfter.TotalAlloc - memoryBefore.TotalAlloc
				measurement.AllocatedBytes += allocated
				measurement.MaximumAllocation = max(measurement.MaximumAllocation, allocated)
				measurement.Cases++
				measurement.InputBytes += len(raw)
				switch fixture.Expect {
				case "accepted":
					assertAccepted(t, fixture, raw, result)
					measurement.Accepted++
				case "refused":
					assertEmptyPublication(t, result)
					if result.State != "refused" || !reflect.DeepEqual(result.Refusal, fixture.Refusal) {
						t.Fatalf("refusal actual=%+v expected=%+v", result.Refusal, fixture.Refusal)
					}
					measurement.Refused++
					measurement.RefusalCensus[result.Refusal.Code+":"+result.Refusal.Limit]++
				case "missing":
					assertEmptyPublication(t, result)
					if result.State != fixture.State || result.Checklist != fixture.Checklist || result.Refusal != nil {
						t.Fatal("missing-artifact workflow mismatch")
					}
					measurement.Missing++
				default:
					t.Fatal("unknown frozen expectation")
				}
				encoded := encodeJSON(t, result)
				if pass == 1 {
					prior[fixture.ID] = encoded
				} else if !bytes.Equal(prior[fixture.ID], encoded) {
					t.Fatal("fresh-state passes differ")
				}
				canonical.WriteString(fixture.ID)
				canonical.WriteByte('\n')
				canonical.Write(encoded)
				canonical.WriteByte('\n')
				measurement.EncodedBytes += len(encoded)
				measurement.MaximumOutput = max(measurement.MaximumOutput, len(encoded))
				measurement.Declarations += len(result.Facts)
				measurement.Citations += len(result.Citations)
				results[fixture.ID] = result
			})
		}
		if t.Failed() {
			return
		}
		comparison := assertFrozenComparison(t, results)
		encodedComparison := encodeJSON(t, comparison)
		if pass == 1 {
			comparisonBytes = encodedComparison
		} else if !bytes.Equal(comparisonBytes, encodedComparison) {
			t.Fatal("comparison fresh-state passes differ")
		}
		measurement.CanonicalSHA256 = digest(canonical.Bytes())
		measurements = append(measurements, measurement)
		if output := os.Getenv("PHEBS_T271_RESULTS"); output != "" && pass == 2 {
			demo := map[string]any{"qualification": "Frozen neutral declarations and explicit snapshots; no live-store, usage, independent accuracy or vendor claim.", "public_captured_catalog": results["public-captured"], "authored_associations_and_views": results["authored-rich"], "missing_artifact": results["missing-artifact"], "explicit_comparison": comparison}
			if err := os.WriteFile(filepath.Join(filepath.Dir(output), "demo.json"), append(encodeJSON(t, demo), '\n'), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if measurements[0].CanonicalSHA256 != measurements[1].CanonicalSHA256 ||
		!reflect.DeepEqual(measurements[0].RefusalCensus, measurements[1].RefusalCensus) {
		t.Fatal("canonical output/refusal census differs")
	}
	t.Logf("passes=%s", encodeJSON(t, measurements))
	if output := os.Getenv("PHEBS_T271_RESULTS"); output != "" {
		record := map[string]any{"schema": "phebs-t271-bounded-reader-results-v1", "qualification": "Fixture conformance and determinism only; no independent accuracy, vendor, live-store or scale claim.", "allocation_qualification": "Per-call process TotalAlloc deltas in a serial test, including possible runtime activity; not retained heap or peak RSS.", "preregistration_commit": "e7be4d27", "go": runtime.Version(), "platform": runtime.GOOS + "/" + runtime.GOARCH, "passes": measurements, "comparison_sha256": digest(comparisonBytes), "comparison": json.RawMessage(comparisonBytes), "core_reader_invocations": 238, "comparison_provenance_reader_invocations": 2, "frozen_inputs_modified": false}
		if err := os.WriteFile(output, append(encodeJSON(t, record), '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func strconvPass(pass int) string {
	if pass == 1 {
		return "pass1"
	}
	return "pass2"
}

func assertFrozenComparison(t *testing.T, results map[string]Result) Comparison {
	t.Helper()
	before := results["comparison-before"]
	context := before.Source
	context.Commit = strings.Repeat("2", 40)
	after := Read(context, readFile(t, "fixtures/positive/comparison-after.json"))
	if after.State != "accepted" {
		t.Fatal("comparison after snapshot refused")
	}
	comparison := Compare(before, after)
	if comparison.State != "compared" || len(comparison.Changes) != 3 || comparison.BeforeSource.Commit == comparison.AfterSource.Commit {
		t.Fatal("comparison shape/provenance mismatch")
	}
	var labels struct {
		Expected []struct {
			Change     string    `json:"change"`
			Kind       string    `json:"kind"`
			Table      string    `json:"table"`
			Path       fieldPath `json:"path"`
			BeforeType string    `json:"before_type"`
			AfterType  string    `json:"after_type"`
		} `json:"expected"`
		SourceClassMismatch string `json:"source_class_mismatch"`
		DifferentLineage    string `json:"different_lineage"`
	}
	decodeJSON(t, readFile(t, "comparison-labels.json"), &labels)
	for _, label := range labels.Expected {
		found := false
		for _, change := range comparison.Changes {
			fact := change.After
			if fact == nil {
				fact = change.Before
			}
			var meaning struct {
				Path fieldPath `json:"path"`
				Type string    `json:"type"`
			}
			decodeJSON(t, fact.Meaning, &meaning)
			if change.Change != label.Change || fact.Kind != label.Kind || fact.Owner.Name != label.Table || !reflect.DeepEqual(meaning.Path, label.Path) {
				continue
			}
			found = true
			if change.Change == "changed" {
				var old struct {
					Type string `json:"type"`
				}
				decodeJSON(t, change.Before.Meaning, &old)
				if old.Type != label.BeforeType || meaning.Type != label.AfterType || change.Before.ID != change.After.ID {
					t.Fatal("changed field did not retain identity or exact types")
				}
			}
		}
		if !found {
			t.Fatalf("frozen comparison label missing: %+v", label)
		}
	}
	// The unchanged id field must survive a new commit/blob, without a change row.
	var oldID, newID string
	for _, snapshot := range []Result{before, after} {
		for _, fact := range snapshot.Facts {
			if fact.Kind != "DECLARES_FIELD" {
				continue
			}
			var meaning struct {
				Path fieldPath `json:"path"`
			}
			decodeJSON(t, fact.Meaning, &meaning)
			if pathKey(meaning.Path) == pathKey(fieldPath{{Kind: "field", Name: "id"}}) {
				if snapshot.Source.Commit == before.Source.Commit {
					oldID = fact.ID
				} else {
					newID = fact.ID
				}
			}
		}
	}
	if oldID == "" || oldID != newID {
		t.Fatal("unchanged field identity depends on provenance")
	}
	for _, change := range comparison.Changes {
		if change.ID == oldID {
			t.Fatal("unchanged field became a difference")
		}
	}
	if got := Compare(before, results["captured-minimal"]); got.Refusal == nil || got.Refusal.Code != labels.SourceClassMismatch || len(got.Changes) != 0 {
		t.Fatal("source classes silently merged")
	}
	other := after
	other.Source.ManifestPath = "another/document.json"
	if got := Compare(before, other); got.Refusal == nil || got.Refusal.Code != labels.DifferentLineage || len(got.Changes) != 0 {
		t.Fatal("different lineages silently merged")
	}
	return comparison
}

func TestPublicDerivationRoundTrips(t *testing.T) {
	cql := readFile(t, "public/reservation.cql")
	if len(cql) != 2218 || digest(cql) != "38992bd1f0882ebefe7e16570d05886ade150bf2036ea6119ab08fc11b3bb39e" {
		t.Fatal("public oracle changed")
	}
	var derivation struct {
		Rows []struct {
			Kind        string                   `json:"kind"`
			JSONPointer string                   `json:"json_pointer"`
			JSONSpan    struct{ Start, End int } `json:"json_span"`
			CQLSpans    []struct {
				Start int    `json:"start"`
				End   int    `json:"end"`
				Text  string `json:"text"`
			} `json:"cql_spans"`
		} `json:"rows"`
	}
	decodeJSON(t, readFile(t, "public/derivation.json"), &derivation)
	var labels struct {
		Declarations []declarationLabel `json:"declarations"`
	}
	decodeJSON(t, readFile(t, "labels/public-captured.json"), &labels)
	if len(derivation.Rows) != 61 || len(labels.Declarations) != 61 {
		t.Fatal("incomplete public derivation")
	}
	seen := make(map[string]bool)
	for _, row := range derivation.Rows {
		key := row.Kind + "\x00" + row.JSONPointer
		if seen[key] || len(row.CQLSpans) == 0 {
			t.Fatal("duplicate or ungrounded public derivation")
		}
		seen[key] = true
		for _, span := range row.CQLSpans {
			if span.Start < 0 || span.End > len(cql) || span.Start >= span.End || string(cql[span.Start:span.End]) != span.Text {
				t.Fatal("public CQL span round-trip failed")
			}
		}
		found := false
		for _, label := range labels.Declarations {
			if label.Kind == row.Kind && label.Pointer == row.JSONPointer {
				found = label.Span.Start == row.JSONSpan.Start && label.Span.End == row.JSONSpan.End
			}
		}
		if !found {
			t.Fatal("public neutral declaration is not completely mapped")
		}
	}
}

func TestCanonicalProjectionIgnoresObjectAndOwnerOrder(t *testing.T) {
	fixture := fixtureCases(t)[0]
	raw := readFile(t, *fixture.Input)
	before := Read(fixture.Context, raw)
	var value map[string]any
	decodeJSON(t, raw, &value)
	tables, ok := value["tables"].([]any)
	if !ok {
		t.Fatal("fixture tables shape")
	}
	slices.Reverse(tables)
	changed := encodeJSON(t, value)
	after := Read(fixture.Context, changed)
	if before.State != "accepted" || after.State != "accepted" || !bytes.Equal(encodeJSON(t, before.Facts), encodeJSON(t, after.Facts)) ||
		!bytes.Equal(encodeJSON(t, before.Catalog), encodeJSON(t, after.Catalog)) || len(Compare(before, after).Changes) != 0 {
		t.Fatal("owner/object order changed canonical semantic output")
	}
	for index := range raw {
		raw[index] = ' '
	}
	if !bytes.Equal(encodeJSON(t, before.Facts), encodeJSON(t, after.Facts)) {
		t.Fatal("published data aliases mutable input bytes")
	}
}

// Supplemental review regression, outside the immutable quality fixture/label
// inventory. JSON permits exactly SP/HT/CR/LF after its one admitted value.
func TestJSONTailWhitespace(t *testing.T) {
	var fixture fixtureCase
	for _, candidate := range fixtureCases(t) {
		if candidate.ID == "captured-minimal" {
			fixture = candidate
		}
	}
	if fixture.Input == nil {
		t.Fatal("frozen baseline fixture missing")
	}
	base := readFile(t, *fixture.Input)
	for _, row := range []struct {
		name, tail string
		accepted   bool
	}{
		{name: "empty", accepted: true},
		{name: "JSON whitespace", tail: " \t\r\n", accepted: true},
		{name: "vertical tab", tail: "\v"},
		{name: "form feed", tail: "\f"},
		{name: "nonbreaking space", tail: "\u00a0"},
		{name: "em space", tail: "\u2003"},
		{name: "next line", tail: "\u0085"},
		{name: "line separator", tail: "\u2028"},
	} {
		t.Run(row.name, func(t *testing.T) {
			result := Read(fixture.Context, append(slices.Clone(base), row.tail...))
			if row.accepted {
				if result.State != "accepted" {
					t.Fatalf("valid JSON whitespace refused: %+v", result.Refusal)
				}
				return
			}
			assertEmptyPublication(t, result)
			if result.State != "refused" || result.Refusal == nil || result.Refusal.Code != "trailing_bytes" {
				t.Fatalf("non-JSON whitespace admitted/misclassified: %+v", result)
			}
		})
	}
}

func BenchmarkReadPublicCaptured(b *testing.B) {
	raw, err := os.ReadFile(filepath.Join("fixtures", "positive", "public-captured.json"))
	if err != nil {
		b.Fatal(err)
	}
	source := Source{Repository: "example.invalid/neutral/schema", ManifestPath: "schema/document.json", Commit: strings.Repeat("1", 40)}
	b.ReportAllocs()
	b.SetBytes(int64(len(raw)))
	b.ResetTimer()
	for b.Loop() {
		if result := Read(source, raw); result.State != "accepted" || len(result.Facts) != 61 {
			b.Fatal("public fixture refused")
		}
	}
}
