package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/api"
)

// TestCallerMapActivationDoesNotPromoteComparison pins the T47.3 admission
// split: Caller Map's admission (dark or released, expressed at serve as
// CallerMapEnabled) never promotes caller comparison. Comparison needs its own
// explicit CallerComparisonEnabled admission, so a released Caller Map recipe
// cannot silently re-expose comparison, Impact, or retired
// Investigation/Workbench capability through the shared engine.
func TestCallerMapActivationDoesNotPromoteComparison(t *testing.T) {
	st := callerMapStore(t)
	opts := callerMapOptions(st, "user:member", nil)
	// callerMapOptions already sets CallerMapEnabled and binds the legacy
	// Caller Map service; comparison admission stays off.
	if api.NewLegacyCallerComparisonService(opts) != nil {
		t.Fatal("legacy comparison admitted without CallerComparisonEnabled")
	}
	if api.NewCallerComparisonService(opts) != nil {
		t.Fatal("exact comparison admitted without CallerComparisonEnabled")
	}
	handler := api.New(opts)
	_, version := catalogHTTP(t, handler, "/api/version", nil)
	if !strings.Contains(version, `"contract-caller-map"`) {
		t.Fatalf("caller-map capability absent: %s", version)
	}
	if strings.Contains(version, "contract-caller-comparison") {
		t.Fatalf("comparison capability leaked from caller-map activation: %s", version)
	}
	code, body := catalogHTTP(t, handler, callerComparisonTarget(""), nil)
	if code != http.StatusNotFound {
		t.Fatalf("comparison route = %d %s, want 404 without CallerComparisonEnabled", code, body)
	}

	// The explicit flag admits comparison and only then does its capability
	// appear; Caller Map's own admission is unchanged by it.
	opts.CallerComparisonEnabled = true
	opts.CallerComparison = api.NewLegacyCallerComparisonService(opts)
	if opts.CallerComparison == nil {
		t.Fatal("legacy comparison refused despite CallerComparisonEnabled")
	}
	handler = api.New(opts)
	_, version = catalogHTTP(t, handler, "/api/version", nil)
	if !strings.Contains(version, `"contract-caller-comparison"`) {
		t.Fatalf("comparison capability absent: %s", version)
	}
	if !strings.Contains(version, `"contract-caller-map"`) {
		t.Fatalf("caller-map capability absent under comparison admission: %s", version)
	}
}
