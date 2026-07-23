package store

import (
	"testing"

	"github.com/bmeddeb/phebs/internal/dossier"
)

func TestDossierRedactionFailsClosedWithoutAuthorizedUnitIdentity(t *testing.T) {
	facts := []ConsumerEdgeFact{
		{FactID: "visible", UnitID: "repo:a"},
		{FactID: "hidden", UnitID: "repo:hidden"},
		{FactID: "legacy-without-unit"},
	}
	got := redactedDossierFacts(facts, []string{"repo:a"})
	if len(got) != 1 || got[0].FactID != "visible" {
		t.Fatalf("redacted facts = %+v", got)
	}
}

func TestDossierScopeResolverCannotAddUnits(t *testing.T) {
	if _, err := normalizeDossierScope(
		[]string{"repo:a"},
		DossierScopeProjection{
			VisibilityProjectionID: "visibility:1",
			VisibleUnitIDs:         []string{"repo:a", "repo:hidden"},
		},
	); err == nil {
		t.Fatal("scope resolver addition unexpectedly accepted")
	}
}

func TestDossierFindingMustMatchCurrentAuthorizedFact(t *testing.T) {
	fact := ConsumerEdgeFact{
		FactID:   "fact:visible",
		UnitID:   "repo:a",
		ObjectID: "operation:visible",
	}
	entry, err := dossier.NewEntry(
		"evidence/fact-visible.json",
		"application/json",
		fact,
	)
	if err != nil {
		t.Fatal(err)
	}
	evidence := dossier.EvidenceReference{
		FactID:           fact.FactID,
		FindingEntryPath: entry.Path,
	}
	entries := map[string][]byte{entry.Path: entry.Content}
	if !dossierFindingMatchesFact(entries, evidence, fact) {
		t.Fatal("matching finding and current fact rejected")
	}

	changed := fact
	changed.ObjectID = "operation:changed"
	if dossierFindingMatchesFact(entries, evidence, changed) {
		t.Fatal("finding inconsistent with current fact accepted")
	}
	if dossierFindingMatchesFact(nil, evidence, fact) {
		t.Fatal("missing finding entry accepted")
	}
}
