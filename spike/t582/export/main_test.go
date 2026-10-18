package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/extract/sdk"
)

func TestPlanStrictTailAndCatalogAdmission(t *testing.T) {
	p := plan{Schema: "t582-plan-v1", PreregistrationSHA256: preregSHA}
	for i := range 27 {
		p.Units = append(p.Units, unit{ID: string(rune('A' + i)), Repository: "authored", Commit: "fixed",
			Files: []inputFile{{Path: "consumer.go", Origin: "inert.go", SHA256: digest(nil)}}})
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, tail := range []string{"", " \r\n\t", "\v", "\f", "\u00a0", "\u2003", "{}"} {
		_, err := decodePlan(strings.NewReader(string(raw) + tail))
		wantValid := tail == "" || tail == " \r\n\t"
		if (err == nil) != wantValid {
			t.Errorf("tail %q: valid=%v, want %v", tail, err == nil, wantValid)
		}
	}
	p.Units[1].ID = p.Units[0].ID
	raw, err = json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodePlan(strings.NewReader(string(raw))); err == nil {
		t.Fatal("duplicate unit admitted")
	}
}

func TestFactRequiresMostRecentExactSource(t *testing.T) {
	content := []byte("f()")
	c, err := newCorpus(unit{ID: "inert", Repository: "authored", Commit: "fixed",
		Files: []inputFile{{Path: "consumer.go", Origin: "inert.go", Content: content, SHA256: digest(content)}}})
	if err != nil {
		t.Fatal(err)
	}
	f := sdk.Fact{Path: "consumer.go", StartLine: 1, EndLine: 1,
		Atom: sdk.AtomInput{SchemaVersion: "t19-v1", BlobDigest: "sha256:" + digest(content),
			EndByte: len(content), RuleID: "inert", AdapterConfigDigest: "sha256:" + digest(nil), FactFingerprint: "inert"}}
	if _, err := validateFact(c, f); err == nil {
		t.Fatal("fact before source read admitted")
	}
	if _, err := c.Read(context.Background(), "consumer.go"); err != nil {
		t.Fatal(err)
	}
	if _, err := validateFact(c, f); err != nil {
		t.Fatal(err)
	}
	f.Atom.BlobDigest = "sha256:wrong"
	if _, err := validateFact(c, f); err == nil {
		t.Fatal("wrong immutable blob admitted")
	}
}
