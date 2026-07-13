package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEvidenceViewFiltersBeforeAggregation(t *testing.T) {
	facts := []Fact{
		{System: "visible-repo", Predicate: "CALLS_OPERATION", Object: "public.Service/Get"},
		{System: "hidden-repo", Predicate: "CALLS_OPERATION", Object: "secret.Service/DeleteEverything"},
		{System: "hidden-repo", Predicate: "DECLARES_FIELD", Object: "secret.Message#1:token"},
	}

	view := buildEvidenceView(facts, visibilityContext{
		Principal:      "member@example.test",
		VisibleSystems: map[string]bool{"visible-repo": true},
	})
	if len(view.Facts) != 1 || view.Coverage.FactCount != 1 {
		t.Fatalf("visible view = %d facts, coverage=%d; want 1/1", len(view.Facts), view.Coverage.FactCount)
	}
	if len(view.Coverage.Systems) != 1 || view.Coverage.Systems[0] != "visible-repo" {
		t.Fatalf("coverage systems = %v", view.Coverage.Systems)
	}
	if got := view.Coverage.Predicates["CALLS_OPERATION"]; got != 1 {
		t.Fatalf("visible CALLS_OPERATION count = %d, want 1", got)
	}
	if _, leakedCount := view.Coverage.Predicates["DECLARES_FIELD"]; leakedCount {
		t.Fatal("hidden predicate count leaked into coverage")
	}

	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"hidden-repo", "secret.Service", "secret.Message"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("filtered result leaks %q: %s", secret, encoded)
		}
	}
}

func TestEvidenceViewEmptyVisibilityLeaksNoCorpusShape(t *testing.T) {
	view := buildEvidenceView([]Fact{
		{System: "hidden-a", Predicate: "P"},
		{System: "hidden-b", Predicate: "P"},
	}, visibilityContext{Principal: "nobody", VisibleSystems: map[string]bool{}})

	if len(view.Facts) != 0 || view.Coverage.FactCount != 0 || len(view.Coverage.Systems) != 0 || len(view.Coverage.Predicates) != 0 {
		t.Fatalf("empty visibility leaked corpus shape: %+v", view)
	}
}
