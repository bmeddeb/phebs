package store

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestCanonicalWorkbenchSuggestionDeterministicAndClosed(t *testing.T) {
	const (
		investigationID = "01J00000000000000000000219"
		revisionID      = "ivr_t219"
		snapshotDigest  = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	)
	evidence := []WorkbenchSuggestionEvidence{
		{
			Plane:  "impact",
			Kind:   "gap",
			ID:     "gap-two",
			Digest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		},
		{
			Plane:      "implementation",
			Kind:       "reference",
			ID:         "row-one",
			Digest:     "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
			Repository: "github.com/acme/cart",
			Commit:     strings.Repeat("d", 40),
			Path:       "internal/cart/handler.go",
			StartLine:  4,
			EndLine:    4,
		},
	}
	input := WorkbenchSuggestion{
		InvestigationID:        investigationID,
		RevisionID:             revisionID,
		Kind:                   "review_related_source",
		Summary:                "Review production reference at github.com/acme/cart:internal/cart/handler.go.",
		SelectionRule:          "t21.9:test",
		EvidenceSnapshotDigest: snapshotDigest,
		Evidence:               evidence,
	}
	first, err := CanonicalWorkbenchSuggestion(input)
	if err != nil {
		t.Fatalf("canonical suggestion: %v", err)
	}
	input.Evidence = slices.Clone(evidence)
	slices.Reverse(input.Evidence)
	second, err := CanonicalWorkbenchSuggestion(input)
	if err != nil {
		t.Fatalf("reordered suggestion: %v", err)
	}
	if !reflect.DeepEqual(first, second) ||
		!strings.HasPrefix(first.ID, "wbs_") ||
		!validSHA256Digest(first.ContentDigest) {
		t.Fatalf("canonical suggestion drifted: first=%+v second=%+v", first, second)
	}

	rows := []struct {
		name string
		edit func(*WorkbenchSuggestion)
	}{
		{
			name: "custom kind",
			edit: func(value *WorkbenchSuggestion) {
				value.Kind = "assigned_task"
			},
		},
		{
			name: "mutable commit",
			edit: func(value *WorkbenchSuggestion) {
				value.Evidence[1].Commit = "HEAD"
			},
		},
		{
			name: "changed id",
			edit: func(value *WorkbenchSuggestion) {
				value.ID = "wbs_" + strings.Repeat("f", 64)
			},
		},
		{
			name: "conflicting evidence identity",
			edit: func(value *WorkbenchSuggestion) {
				conflict := value.Evidence[0]
				conflict.Digest = "sha256:" + strings.Repeat("e", 64)
				value.Evidence = append(value.Evidence, conflict)
			},
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			changed := input
			changed.Evidence = slices.Clone(input.Evidence)
			row.edit(&changed)
			if _, err := CanonicalWorkbenchSuggestion(changed); err == nil {
				t.Fatal("invalid suggestion was accepted")
			}
		})
	}
}

func TestWorkbenchDispositionVocabularyAndRationale(t *testing.T) {
	for _, category := range []string{
		"accepted",
		"rejected",
		"completed",
		"reopened",
		"waived",
	} {
		if _, ok := workbenchDispositionCategories[category]; !ok {
			t.Errorf("missing fixed category %q", category)
		}
	}
	for _, category := range []string{
		"rejected",
		"reopened",
		"waived",
	} {
		if !workbenchDispositionRationaleRequired(category) {
			t.Errorf("%q should require rationale", category)
		}
	}
	for _, category := range []string{"accepted", "completed"} {
		if workbenchDispositionRationaleRequired(category) {
			t.Errorf("%q should not require rationale", category)
		}
	}
	if _, ok := workbenchDispositionCategories["assigned"]; ok {
		t.Fatal("custom task state entered the fixed category vocabulary")
	}
}
