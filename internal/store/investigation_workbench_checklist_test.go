package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/store"
)

func checklistStoreSuggestion(
	t *testing.T,
	investigationID, revisionID string,
) store.WorkbenchSuggestion {
	t.Helper()
	value, err := store.CanonicalWorkbenchSuggestion(
		store.WorkbenchSuggestion{
			InvestigationID: investigationID,
			RevisionID:      revisionID,
			Kind:            "review_related_source",
			Summary: "Review production implementation at " +
				"github.com/acme/cart:internal/cart/handler.go.",
			SelectionRule: "t21.9:store-test",
			EvidenceSnapshotDigest: "sha256:" +
				strings.Repeat("a", 64),
			Evidence: []store.WorkbenchSuggestionEvidence{{
				Plane:      "implementation",
				Kind:       "implementation",
				ID:         "wri_store_test",
				Digest:     "sha256:" + strings.Repeat("b", 64),
				Repository: "github.com/acme/cart",
				Commit:     strings.Repeat("c", 40),
				Path:       "internal/cart/handler.go",
				StartLine:  2,
				EndLine:    2,
			}},
		},
	)
	if err != nil {
		t.Fatalf("canonical suggestion: %v", err)
	}
	return value
}

func TestWorkbenchDispositionOwnerCASIdempotencyAndSupersession(
	t *testing.T,
) {
	s := newTestStore(t)
	ctx := context.Background()
	investigation, revision := seedInvestigation(t, s)
	suggestion := checklistStoreSuggestion(
		t,
		investigation.ID,
		revision.ID,
	)
	request := store.WorkbenchDispositionRequest{
		ExpectedRevisionID: revision.ID,
		IdempotencyKey:     "t21.9-accept",
		Suggestion:         suggestion,
		Category:           "accepted",
	}
	first, err := s.AppendWorkbenchDisposition(
		ctx,
		authzOwner,
		request,
	)
	if err != nil {
		t.Fatalf("append disposition: %v", err)
	}
	retry, err := s.AppendWorkbenchDisposition(
		ctx,
		authzOwner,
		request,
	)
	if err != nil || retry.ID != first.ID ||
		retry.ContentDigest != first.ContentDigest {
		t.Fatalf("idempotent retry = %+v, %v; want %+v", retry, err, first)
	}
	receipt, err := s.GetWorkbenchDispositionMutationAs(
		ctx,
		authzOwner,
		investigation.ID,
		revision.ID,
		request.IdempotencyKey,
	)
	if err != nil || receipt.ID != first.ID {
		t.Fatalf("mutation receipt = %+v, %v", receipt, err)
	}
	changed := request
	changed.Category = "completed"
	if _, err := s.AppendWorkbenchDisposition(
		ctx,
		authzOwner,
		changed,
	); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("changed idempotency request = %v, want conflict", err)
	}

	if _, err := s.GrantInvestigationAccess(
		ctx,
		authzOwner,
		investigation.ID,
		authzReader,
		store.InvestigationRoleReader,
	); err != nil {
		t.Fatalf("grant reader: %v", err)
	}
	readerValues, err := s.ListWorkbenchDispositionsAs(
		ctx,
		authzReader,
		investigation.ID,
	)
	if err != nil || len(readerValues) != 1 {
		t.Fatalf("reader list = %+v, %v", readerValues, err)
	}
	if _, err := s.AppendWorkbenchDisposition(
		ctx,
		authzReader,
		store.WorkbenchDispositionRequest{
			ExpectedRevisionID: revision.ID,
			IdempotencyKey:     "reader-write",
			Suggestion:         suggestion,
			Category:           "completed",
		},
	); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("reader append = %v, want not found", err)
	}

	reopened, err := s.AppendWorkbenchDisposition(
		ctx,
		authzOwner,
		store.WorkbenchDispositionRequest{
			ExpectedRevisionID: revision.ID,
			IdempotencyKey:     "t21.9-reopen",
			Suggestion:         suggestion,
			Category:           "reopened",
			Rationale:          "Implementation evidence needs another pass.",
			Supersedes:         first.ID,
		},
	)
	if err != nil {
		t.Fatalf("reopen disposition: %v", err)
	}
	if reopened.Sequence != 2 ||
		reopened.Supersedes != first.ID ||
		reopened.Suggestion.ContentDigest !=
			first.Suggestion.ContentDigest {
		t.Fatalf("reopened disposition lost its chain: %+v", reopened)
	}
	if _, err := s.AppendWorkbenchDisposition(
		ctx,
		authzOwner,
		store.WorkbenchDispositionRequest{
			ExpectedRevisionID: revision.ID,
			IdempotencyKey:     "t21.9-fork",
			Suggestion:         suggestion,
			Category:           "completed",
			Supersedes:         first.ID,
		},
	); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("forked supersession = %v, want conflict", err)
	}
	values, err := s.ListWorkbenchDispositionsAs(
		ctx,
		authzOwner,
		investigation.ID,
	)
	if err != nil || len(values) != 2 {
		t.Fatalf("owner list = %+v, %v", values, err)
	}
	if values[0].Category != "accepted" ||
		values[0].ContentDigest != first.ContentDigest ||
		values[1].Category != "reopened" {
		t.Fatalf("immutable history changed: %+v", values)
	}

	secondSuggestion := suggestion
	secondSuggestion.ID = ""
	secondSuggestion.ContentDigest = ""
	secondSuggestion.Summary =
		"Review production implementation at github.com/acme/cart:internal/cart/other.go."
	secondSuggestion.Evidence = append(
		[]store.WorkbenchSuggestionEvidence(nil),
		suggestion.Evidence...,
	)
	secondSuggestion.Evidence[0].ID = "wri_store_test_other"
	secondSuggestion.Evidence[0].Digest = "sha256:" +
		strings.Repeat("d", 64)
	secondSuggestion.Evidence[0].Path = "internal/cart/other.go"
	secondSuggestion, err = store.CanonicalWorkbenchSuggestion(
		secondSuggestion,
	)
	if err != nil {
		t.Fatalf("canonical second suggestion: %v", err)
	}
	secondRoot, err := s.AppendWorkbenchDisposition(
		ctx,
		authzOwner,
		store.WorkbenchDispositionRequest{
			ExpectedRevisionID: revision.ID,
			IdempotencyKey:     "t21.9-complete-other",
			Suggestion:         secondSuggestion,
			Category:           "completed",
		},
	)
	if err != nil {
		t.Fatalf("append independent suggestion root: %v", err)
	}
	if secondRoot.Sequence != 1 || secondRoot.Supersedes != "" {
		t.Fatalf("independent suggestion root = %+v", secondRoot)
	}

	events, err := s.ListAuditEvents(ctx, 0, 100)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	appends := 0
	for _, event := range events {
		if event.Action ==
			"investigation.workbench.disposition.append" {
			appends++
			if event.ActorID != authzOwner {
				t.Fatalf("disposition audit actor = %q", event.ActorID)
			}
		}
	}
	if appends != 3 {
		t.Fatalf("disposition audit count = %d, want 3", appends)
	}
}

func TestWorkbenchDispositionExpectedRevisionAndRationaleGuards(
	t *testing.T,
) {
	s := newTestStore(t)
	ctx := context.Background()
	investigation, revision := seedInvestigation(t, s)
	suggestion := checklistStoreSuggestion(
		t,
		investigation.ID,
		revision.ID,
	)
	for _, row := range []struct {
		name    string
		request store.WorkbenchDispositionRequest
		want    error
	}{
		{
			name: "stale expected revision",
			request: store.WorkbenchDispositionRequest{
				ExpectedRevisionID: "ivr_stale",
				IdempotencyKey:     "stale",
				Suggestion:         suggestion,
				Category:           "accepted",
			},
			want: store.ErrInvalidWorkbenchDisposition,
		},
		{
			name: "unknown category",
			request: store.WorkbenchDispositionRequest{
				ExpectedRevisionID: revision.ID,
				IdempotencyKey:     "custom-state",
				Suggestion:         suggestion,
				Category:           "assigned",
			},
			want: store.ErrInvalidWorkbenchDisposition,
		},
		{
			name: "waived without rationale",
			request: store.WorkbenchDispositionRequest{
				ExpectedRevisionID: revision.ID,
				IdempotencyKey:     "waived",
				Suggestion:         suggestion,
				Category:           "waived",
			},
			want: store.ErrInvalidWorkbenchDisposition,
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			if _, err := s.AppendWorkbenchDisposition(
				ctx,
				authzOwner,
				row.request,
			); !errors.Is(err, row.want) {
				t.Fatalf("append error = %v, want %v", err, row.want)
			}
		})
	}
}
