package store_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/mcp"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	authzOwner    = "user:owner"
	authzReader   = "user:reader"
	authzStranger = "user:stranger"
)

type authzDomain struct {
	investigation *store.Investigation
	revision      *store.Revision
	run           *store.Run
	artifact      *store.RunArtifact
	decision      *store.Decision
	disposition   *store.Disposition
	baseline      *store.BaselineDesignation
	watch         *store.Watch
	watchRevision *store.WatchRevision
}

func seedAuthzDomain(t *testing.T, s *store.Surreal) authzDomain {
	t.Helper()
	ctx := context.Background()
	investigation, revision := seedInvestigation(t, s)
	run, err := s.CreateRun(ctx, testRunRequest(revision.ID))
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	advancePublishedRun(t, s, run.ID)
	artifact := putPublishedArtifact(t, s, run.ID)
	decision, err := s.PutDecision(ctx, store.Decision{
		InvestigationID: investigation.ID, ClaimScope: "consumer-census",
		Conclusion: "no_decision", RevisionID: revision.ID, RunArtifactID: artifact.ID,
		EligibilityResult: "eligible:false:blocker", VisibleScope: "repo:example/consumer",
		Authority: authzOwner, AuthoritySource: "investigation owner", Rationale: "needs review",
	})
	if err != nil {
		t.Fatalf("put decision: %v", err)
	}
	disposition, err := s.PutDisposition(ctx, store.Disposition{
		InvestigationID: investigation.ID, Category: "will_migrate",
		SubjectKind: "consumer", SubjectID: "consumer:one", Actor: authzOwner,
		AuthorityBasis: "subject owner", Rationale: "scheduled",
	})
	if err != nil {
		t.Fatalf("put disposition: %v", err)
	}
	baseline, err := s.PutBaselineDesignation(ctx, store.BaselineDesignation{
		InvestigationID: investigation.ID, ClaimScope: "consumer-census",
		WorkflowScope: "migration", RevisionID: revision.ID, RunArtifactID: artifact.ID,
		Authority: authzOwner, Rationale: "accepted inventory",
	})
	if err != nil {
		t.Fatalf("put baseline: %v", err)
	}
	watch, err := s.CreateWatch(ctx, store.Watch{Owner: authzOwner, Enabled: true})
	if err != nil {
		t.Fatalf("create watch: %v", err)
	}
	watchRevision, err := s.PutWatchRevision(ctx, store.WatchRevision{
		WatchID: watch.ID, Seq: 1, InvestigationID: investigation.ID, RevisionID: revision.ID,
		TypedQuery: "claim:consumer-census", TriggerPolicy: "on-publish",
		Coalescing: "one-per-run", NoiseControls: "material-only", Creator: authzOwner,
	})
	if err != nil {
		t.Fatalf("put watch revision: %v", err)
	}
	return authzDomain{
		investigation: investigation, revision: revision, run: run, artifact: artifact,
		decision: decision, disposition: disposition, baseline: baseline,
		watch: watch, watchRevision: watchRevision,
	}
}

// TestInvestigationAuthzReadProjectionMatrix is the T16.3 negative matrix:
// every domain read is principal-projected, grants extend object reads (but
// never Watches), and unknown and unauthorized inputs both classify as the
// identical ErrNotFound.
func TestInvestigationAuthzReadProjectionMatrix(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	domain := seedAuthzDomain(t, s)
	if _, err := s.GrantInvestigationAccess(ctx, authzOwner, domain.investigation.ID,
		authzReader, store.InvestigationRoleReader); err != nil {
		t.Fatalf("grant reader: %v", err)
	}

	const unknownULID = "01JYZZZZZZZZZZZZZZZZZZZZZZ"
	rows := []struct {
		name        string
		read        func(principal, id string) error
		id          string
		unknownID   string
		grantExtend bool
	}{
		{"investigation", func(p, id string) error {
			_, err := s.GetInvestigationAs(ctx, p, id)
			return err
		}, domain.investigation.ID, unknownULID, true},
		{"revision", func(p, id string) error {
			_, err := s.GetRevisionAs(ctx, p, id)
			return err
		}, domain.revision.ID, "ivr_deadbeef", true},
		{"run", func(p, id string) error {
			_, err := s.GetRunAs(ctx, p, id)
			return err
		}, domain.run.ID, "iru_deadbeef", true},
		{"run events", func(p, id string) error {
			_, err := s.ListRunEventsAs(ctx, p, id)
			return err
		}, domain.run.ID, "iru_deadbeef", true},
		{"run artifact", func(p, id string) error {
			_, err := s.GetRunArtifactAs(ctx, p, id)
			return err
		}, domain.artifact.ID, "ira_deadbeef", true},
		{"decision", func(p, id string) error {
			_, err := s.GetDecisionAs(ctx, p, id)
			return err
		}, domain.decision.ID, unknownULID, true},
		{"disposition", func(p, id string) error {
			_, err := s.GetDispositionAs(ctx, p, id)
			return err
		}, domain.disposition.ID, unknownULID, true},
		{"baseline", func(p, id string) error {
			_, err := s.GetBaselineDesignationAs(ctx, p, id)
			return err
		}, domain.baseline.ID, unknownULID, true},
		{"watch", func(p, id string) error {
			_, err := s.GetWatchAs(ctx, p, id)
			return err
		}, domain.watch.ID, unknownULID, false},
		{"watch revision", func(p, id string) error {
			_, err := s.GetWatchRevisionAs(ctx, p, id)
			return err
		}, domain.watchRevision.ID, "iwr_deadbeef", false},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			if err := row.read(authzOwner, row.id); err != nil {
				t.Fatalf("owner read: %v", err)
			}
			readerErr := row.read(authzReader, row.id)
			if row.grantExtend {
				if readerErr != nil {
					t.Fatalf("granted reader read: %v", readerErr)
				}
			} else if !errors.Is(readerErr, store.ErrNotFound) {
				t.Fatalf("watch read by non-owner = %v, want ErrNotFound", readerErr)
			}
			unauthorized := row.read(authzStranger, row.id)
			unknown := row.read(authzOwner, row.unknownID)
			if !errors.Is(unauthorized, store.ErrNotFound) {
				t.Fatalf("unauthorized read = %v, want ErrNotFound", unauthorized)
			}
			if !errors.Is(unknown, store.ErrNotFound) {
				t.Fatalf("unknown read = %v, want ErrNotFound", unknown)
			}
			if unauthorized.Error() != unknown.Error() {
				t.Fatalf("unauthorized (%q) and unknown (%q) errors are distinguishable",
					unauthorized, unknown)
			}
		})
	}
}

// TestInvestigationAuthzRefusalBytesMatchFixture is the fixture-06 AC: an
// unknown-identity input and an unauthorized-identity input classify to the
// same ErrNotFound and render byte-identical canonical refusals, each equal
// to the golden fixture.
func TestInvestigationAuthzRefusalBytesMatchFixture(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	investigation, _ := seedInvestigation(t, s)

	_, unauthorizedErr := s.GetInvestigationAs(ctx, authzStranger, investigation.ID)
	_, unknownErr := s.GetInvestigationAs(ctx, authzOwner, "01JYZZZZZZZZZZZZZZZZZZZZZZ")

	golden, err := os.ReadFile(filepath.Join("..", "..", "docs", "fixtures",
		"investigations", "06-inaccessible-scope-refusal.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	generatedAt := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	var rendered [][]byte
	for name, readErr := range map[string]error{
		"unauthorized": unauthorizedErr, "unknown": unknownErr,
	} {
		if !errors.Is(readErr, store.ErrNotFound) {
			t.Fatalf("%s read = %v, want ErrNotFound", name, readErr)
		}
		envelope, renderErr := mcp.NotAvailableRefusal(
			"find_contract_edges", "1.0", "01SYNTHETIC", generatedAt)
		if renderErr != nil {
			t.Fatalf("%s refusal render: %v", name, renderErr)
		}
		if !bytes.Equal(envelope, golden) {
			t.Fatalf("%s refusal differs from golden fixture:\n%s", name, envelope)
		}
		rendered = append(rendered, envelope)
	}
	if !bytes.Equal(rendered[0], rendered[1]) {
		t.Fatal("unknown and unauthorized refusal bytes are distinguishable")
	}
}

func TestInvestigationAuthzSharingTransferAndCursors(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	investigation, _ := seedInvestigation(t, s)
	const nextOwner = "user:next-owner"

	// Non-owners cannot grant, revoke, or transfer, and learn nothing.
	if _, err := s.GrantInvestigationAccess(ctx, authzStranger, investigation.ID,
		authzReader, store.InvestigationRoleReader); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("stranger grant = %v, want ErrNotFound", err)
	}
	if err := s.RevokeInvestigationAccess(ctx, authzStranger, investigation.ID, authzReader); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("stranger revoke = %v, want ErrNotFound", err)
	}
	if _, err := s.TransferInvestigationOwnership(ctx, authzStranger, investigation.ID, authzStranger); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("stranger transfer = %v, want ErrNotFound", err)
	}

	// Sharing is re-authorized at query time: denied before, allowed after,
	// denied again after revocation.
	if _, err := s.GetInvestigationAs(ctx, authzReader, investigation.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("pre-grant read = %v, want ErrNotFound", err)
	}
	grant, err := s.GrantInvestigationAccess(ctx, authzOwner, investigation.ID,
		authzReader, store.InvestigationRoleReader)
	if err != nil || grant.Principal != authzReader {
		t.Fatalf("grant = %+v, %v", grant, err)
	}
	if _, err := s.GrantInvestigationAccess(ctx, authzOwner, investigation.ID,
		authzReader, store.InvestigationRoleReader); err != nil {
		t.Fatalf("idempotent regrant: %v", err)
	}
	if _, err := s.GrantInvestigationAccess(ctx, authzReader, investigation.ID,
		authzStranger, store.InvestigationRoleReader); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("grantee delegating access = %v, want ErrNotFound", err)
	}
	if _, err := s.GetInvestigationAs(ctx, authzReader, investigation.ID); err != nil {
		t.Fatalf("granted read: %v", err)
	}
	if _, err := s.PutInvestigationCursor(ctx, authzReader, investigation.ID, "cursor:reader:1"); err != nil {
		t.Fatalf("reader cursor: %v", err)
	}
	if err := s.RevokeInvestigationAccess(ctx, authzOwner, investigation.ID, authzReader); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := s.GetInvestigationAs(ctx, authzReader, investigation.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("post-revoke read = %v, want ErrNotFound", err)
	}
	if _, err := s.GetInvestigationCursor(ctx, authzReader, investigation.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("post-revoke cursor = %v, want ErrNotFound", err)
	}

	// Ownership changes only through the transfer path.
	changed := *investigation
	changed.Owner = nextOwner
	if _, err := s.UpdateInvestigation(ctx, changed); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("owner edit through update = %v, want ErrConflict", err)
	}

	// Cursors void on ownership transfer without erasing stored state.
	if _, err := s.PutInvestigationCursor(ctx, authzOwner, investigation.ID, "cursor:owner:1"); err != nil {
		t.Fatalf("owner cursor: %v", err)
	}
	if _, err := s.GetInvestigationCursor(ctx, authzOwner, investigation.ID); err != nil {
		t.Fatalf("owner cursor read: %v", err)
	}
	transferred, err := s.TransferInvestigationOwnership(ctx, authzOwner, investigation.ID, nextOwner)
	if err != nil || transferred.Owner != nextOwner {
		t.Fatalf("transfer = %+v, %v", transferred, err)
	}
	if _, err := s.GetInvestigationAs(ctx, authzOwner, investigation.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("previous owner read = %v, want ErrNotFound", err)
	}
	if _, err := s.TransferInvestigationOwnership(ctx, authzOwner, investigation.ID, authzOwner); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("previous owner transfer-back = %v, want ErrNotFound", err)
	}
	if _, err := s.GetInvestigationAs(ctx, nextOwner, investigation.ID); err != nil {
		t.Fatalf("new owner read: %v", err)
	}

	// Re-grant the previous owner: object reads return, but the cursor stored
	// under the earlier authorization revision stays void until re-established.
	if _, err := s.GrantInvestigationAccess(ctx, nextOwner, investigation.ID,
		authzOwner, store.InvestigationRoleReader); err != nil {
		t.Fatalf("regrant previous owner: %v", err)
	}
	if _, err := s.GetInvestigationAs(ctx, authzOwner, investigation.ID); err != nil {
		t.Fatalf("regranted read: %v", err)
	}
	if _, err := s.GetInvestigationCursor(ctx, authzOwner, investigation.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("voided cursor read = %v, want ErrNotFound", err)
	}
	refreshed, err := s.PutInvestigationCursor(ctx, authzOwner, investigation.ID, "cursor:owner:2")
	if err != nil || refreshed.AuthzRevision != 1 {
		t.Fatalf("re-established cursor = %+v, %v", refreshed, err)
	}
	if _, err := s.GetInvestigationCursor(ctx, authzOwner, investigation.ID); err != nil {
		t.Fatalf("re-established cursor read: %v", err)
	}
}
