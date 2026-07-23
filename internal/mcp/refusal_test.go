package mcp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const refusalFixtureDir = "../../docs/fixtures/investigations"

func fixture06Time() time.Time {
	return time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
}

func TestNotAvailableRefusalMatchesGoldenFixture(t *testing.T) {
	golden, err := os.ReadFile(filepath.Join(refusalFixtureDir, "06-inaccessible-scope-refusal.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got, err := NotAvailableRefusal("find_contract_edges", "1.0", "01SYNTHETIC", fixture06Time())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, golden) {
		t.Fatalf("canonical refusal bytes differ from fixture 06:\ngot:\n%s\nwant:\n%s", got, golden)
	}
}

// The NOT_AVAILABLE refusal must be minimal: none of the disclosure-bearing
// top-level sections that appear on an authorized refusal (fixture 05's
// scope, validation, provenance, freshness, absence eligibility, and result
// window) may exist in its shape.
func TestNotAvailableRefusalDisclosesNoAuthorizedSections(t *testing.T) {
	rendered, err := NotAvailableRefusal("find_contract_edges", "1.0", "01SYNTHETIC", fixture06Time())
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(rendered, &got); err != nil {
		t.Fatal(err)
	}
	fixture05, err := os.ReadFile(filepath.Join(refusalFixtureDir, "05-stale-pack-validation.json"))
	if err != nil {
		t.Fatalf("read fixture 05: %v", err)
	}
	var authorized map[string]json.RawMessage
	if err := json.Unmarshal(fixture05, &authorized); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{
		"envelope_version": true, "errors": true, "generated_at": true,
		"outcome": true, "refusal": true, "request_id": true, "tool": true,
	}
	for key := range got {
		if !allowed[key] {
			t.Errorf("NOT_AVAILABLE refusal contains unexpected top-level key %q", key)
		}
	}
	for key := range allowed {
		if _, ok := got[key]; !ok {
			t.Errorf("NOT_AVAILABLE refusal is missing required key %q", key)
		}
	}
	for key := range authorized {
		if allowed[key] {
			continue
		}
		if _, ok := got[key]; ok {
			t.Errorf("NOT_AVAILABLE refusal discloses authorized-only section %q", key)
		}
	}
}
