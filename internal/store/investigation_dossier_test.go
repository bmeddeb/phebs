package store_test

import (
	"context"
	"crypto/ed25519"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dossier"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestInvestigationDossierRedactionOfflineIntegrityAndReauthorization(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	investigation, revision := seedInvestigation(t, s)
	run, err := s.CreateRun(ctx, testRunRequest(revision.ID))
	if err != nil {
		t.Fatal(err)
	}
	artifact := putPublishedArtifactWithFacts(
		t,
		s,
		run.ID,
		[]string{"fact:visible", "fact:hidden"},
	)
	visibleFact := consumerFact("fact:visible", "edge:visible")
	visibleFact.UnitID = "repo:a"
	hiddenFact := consumerFact("fact:hidden", "edge:hidden")
	hiddenFact.UnitID = "repo:hidden"
	snapshotInput := ledgerSnapshot(
		investigation,
		revision,
		artifact,
		visibleFact,
		hiddenFact,
	)
	snapshotInput.Semantics.DeclaredUniverse = []string{"repo:a", "repo:hidden"}
	snapshot, err := s.RecordConsumerSnapshot(ctx, snapshotInput)
	if err != nil {
		t.Fatal(err)
	}

	currentVisible := []string{"repo:a"}
	privateKey := ed25519.NewKeyFromSeed([]byte(
		"0123456789abcdef0123456789abcdef",
	))
	service := store.InvestigationDossierService{
		Store: s,
		ResolveScope: func(
			_ context.Context,
			_ string,
			_ []string,
		) (store.DossierScopeProjection, error) {
			return store.DossierScopeProjection{
				VisibilityProjectionID: "visibility:owner:current",
				VisibleUnitIDs:         append([]string(nil), currentVisible...),
			}, nil
		},
		Signer: store.DossierSigner{
			KeyID: "test-dossier-key", PrivateKey: privateKey,
		},
	}
	sealed, err := service.Export(ctx, store.DossierExportRequest{
		Principal: "user:owner", SourceSnapshotID: snapshot.ID,
		PackCardIdentities: []string{"pack:grpc@1"},
		SupportedClaims:    []string{"consumer-census"},
		Blockers:           []string{"OWNER_UNKNOWN"},
		EligibilityResult:  "eligible:false:blocker",
		FreshnessState:     "current", ValidationState: "released",
		ReviewDueRule:          "on-material-change",
		HandlingClassification: "internal",
	})
	if err != nil {
		t.Fatalf("export dossier: %v", err)
	}
	if len(sealed.Manifest.Evidence) != 1 ||
		sealed.Manifest.Evidence[0].FactID != "fact:visible" ||
		len(sealed.Manifest.Authorization.VisibleUnitIDs) != 1 ||
		sealed.Manifest.Authorization.VisibleUnitIDs[0] != "repo:a" {
		t.Fatalf("redacted manifest = %+v", sealed.Manifest)
	}
	content, err := dossier.CanonicalJSON(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "fact:hidden") ||
		strings.Contains(string(content), "edge:hidden") ||
		strings.Contains(string(content), "repo:hidden") {
		t.Fatalf("recipient dossier contains hidden scope: %s", content)
	}
	if _, err := dossier.Verify(*sealed, dossier.VerifyOptions{
		TrustedKeyID:  "test-dossier-key",
		TrustedPublic: privateKey.Public().(ed25519.PublicKey),
	}); err != nil {
		t.Fatalf("offline verify: %v", err)
	}
	reopened, err := service.Reopen(ctx, "user:owner", sealed.DossierID)
	if err != nil || reopened.RootDigest != sealed.RootDigest {
		t.Fatalf("reopen = %+v, %v", reopened, err)
	}

	// The Dossier owner keeps its primary artifact retained even after the
	// active-Investigation owner is explicitly released.
	owner, err := s.PutRunArtifactRetentionOwner(ctx, store.RunArtifactRetentionOwner{
		ArtifactID: artifact.ID, Kind: store.RunArtifactOwnerInvestigation,
		OwnerID: investigation.ID, AuthorizedBy: "worker:one",
		Reason: "active investigation run artifact",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReleaseRunArtifactRetentionOwner(
		ctx,
		owner.Key,
		"user:owner",
		"dossier retention test",
	); err != nil {
		t.Fatal(err)
	}
	if swept, err := s.SweepRunArtifacts(ctx, time.Now().Add(time.Hour)); err != nil ||
		swept != 0 {
		t.Fatalf("dossier-pinned sweep = %d, %v", swept, err)
	}

	// Offline possession remains verifiable, but reopening through phebs
	// rechecks current evidence scope and fails closed after access loss.
	currentVisible = []string{}
	if _, err := service.Reopen(
		ctx,
		"user:owner",
		sealed.DossierID,
	); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("reopen after scope revocation = %v, want ErrNotFound", err)
	}
	if _, err := dossier.Verify(*sealed, dossier.VerifyOptions{
		TrustedKeyID:  "test-dossier-key",
		TrustedPublic: privateKey.Public().(ed25519.PublicKey),
	}); err != nil {
		t.Fatalf("offline verification changed after revocation: %v", err)
	}
}
