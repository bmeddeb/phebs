package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/compat"
)

// TestWireCompatibilityServesOnlyTheVerdict pins T53.2's standalone surface:
// with no store, evidence or proof-bundle store at all it returns the checker's
// digest-bound verdict unchanged, maps refusals like the proof-enriched check,
// and stays undiscoverable when not admitted.
func TestWireCompatibilityServesOnlyTheVerdict(t *testing.T) {
	request := compat.Request{
		Lineage: "contract_wire_test",
		Before:  []compat.File{{Path: "shop/v1/shop.proto", Content: "syntax = \"proto3\";\n"}},
		After:   []compat.File{{Path: "shop/v1/shop.proto", Content: "syntax = \"proto3\";\n"}},
	}
	verdict := compat.CompatibilityResult{
		Compatible: false,
		Before:     compat.InputSnapshot{Digest: "sha256:before"},
		After:      compat.InputSnapshot{Digest: "sha256:after"},
		Violations: []compat.Violation{{Snapshot: "after", Path: "shop/v1/shop.proto", Rule: "FIELD_WIRE_COMPATIBLE_TYPE"}},
		AffectedFields: []compat.FieldIdentity{{
			Lineage: request.Lineage, Message: "shop.v1.M", Number: 1,
		}},
		Run: compat.Run{Engine: "buf", Version: compat.Version, Policy: compat.Policy},
	}
	post := func(handler http.Handler) *httptest.ResponseRecorder {
		encoded, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		recorder := httptest.NewRecorder()
		httpRequest := httptest.NewRequest(http.MethodPost, "/api/check_wire_compatibility", strings.NewReader(string(encoded)))
		httpRequest.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(recorder, httpRequest)
		return recorder
	}

	checker := &fixedCompatibility{result: verdict}
	recorder := post(api.New(api.Options{Version: "test", WireCompatibility: checker}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body)
	}
	var got compat.CompatibilityResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if want, _ := json.Marshal(verdict); string(mustJSON(t, got)) != string(want) || checker.request.Lineage != request.Lineage {
		t.Fatalf("verdict = %s, want %s", mustJSON(t, got), want)
	}

	for _, refusal := range []struct {
		err  error
		code int
	}{
		{fmt.Errorf("bad path: %w", compat.ErrInvalidInput), http.StatusBadRequest},
		{fmt.Errorf("too many files: %w", compat.ErrLimit), http.StatusUnprocessableEntity},
		{fmt.Errorf("sandbox: %w", compat.ErrUnavailable), http.StatusServiceUnavailable},
	} {
		if code := post(api.New(api.Options{Version: "test", WireCompatibility: &fixedCompatibility{err: refusal.err}})).Code; code != refusal.code {
			t.Fatalf("%v: status = %d, want %d", refusal.err, code, refusal.code)
		}
	}

	if code := post(api.New(api.Options{Version: "test"})).Code; code != http.StatusNotFound && code != http.StatusMethodNotAllowed {
		t.Fatalf("unadmitted wire compatibility answered %d", code)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
