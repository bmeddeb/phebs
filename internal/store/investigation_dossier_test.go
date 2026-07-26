package store_test

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
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
	if sealed.Manifest.ChangeBrief != nil {
		t.Fatal("legacy Investigation revision unexpectedly exported a change brief")
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
	if strings.Contains(string(content), `"change_brief"`) {
		t.Fatal("legacy dossier bytes gained an empty change-brief field")
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

func TestInvestigationDossierIncludesRevisionChangeBrief(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	investigation, err := s.CreateInvestigation(ctx, store.Investigation{
		Referent:    "grpc:/shop.v1.Checkout/Submit",
		ClaimFamily: "change-workbench",
		Title:       "Checkout change",
		Owner:       "user:owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	appended, err := s.AppendChangeBriefRevision(
		ctx,
		store.AppendChangeBriefRevisionRequest{
			Principal: "user:owner",
			Revision: store.Revision{
				InvestigationID:    investigation.ID,
				Seq:                1,
				NormalizedQuestion: "what changes are required for checkout?",
				DecisionSought:     "approve the contract change",
				DeclaredUniverse:   "repo:a",
				SnapshotPolicy:     "exact commit",
				BuildConfiguration: "linux/amd64",
				PackSelection:      "protobuf-contract@1",
				EnumerationMethod:  "exact contract selection",
			},
			Brief: store.ChangeBrief{
				TicketKind:     store.ChangeBriefModify,
				Problem:        "Checkout lacks a stable receipt identifier.",
				DesiredOutcome: "Checkout returns a stable receipt identifier.",
				SuccessCriteria: []string{
					"The response contains the new identifier.",
				},
				What: store.ChangeBriefWhat{
					Selections: []store.ChangeBriefContractSelection{{
						Role:               store.ChangeBriefCurrent,
						Protocol:           "protobuf",
						Repository:         "example/contracts",
						DeclarationLineage: "proto/shop.proto:shop.v1.Checkout",
						CanonicalOperation: "/shop.v1.Checkout/Submit",
					}},
				},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	investigation, err = s.GetInvestigation(ctx, investigation.ID)
	if err != nil {
		t.Fatal(err)
	}
	investigation.Lifecycle = "active"
	investigation, err = s.UpdateInvestigation(ctx, *investigation)
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateRun(ctx, testRunRequest(appended.Revision.ID))
	if err != nil {
		t.Fatal(err)
	}
	artifact := putPublishedArtifactWithFacts(t, s, run.ID, []string{"fact:visible"})
	fact := consumerFact("fact:visible", "edge:visible")
	fact.UnitID = "repo:a"
	snapshot, err := s.RecordConsumerSnapshot(
		ctx,
		ledgerSnapshot(investigation, &appended.Revision, artifact, fact),
	)
	if err != nil {
		t.Fatal(err)
	}
	privateKey := ed25519.NewKeyFromSeed([]byte(
		"abcdef0123456789abcdef0123456789",
	))
	service := store.InvestigationDossierService{
		Store: s,
		ResolveScope: func(
			_ context.Context,
			_ string,
			_ []string,
		) (store.DossierScopeProjection, error) {
			return store.DossierScopeProjection{
				VisibilityProjectionID: "visibility:owner:brief",
				VisibleUnitIDs:         []string{"repo:a"},
			}, nil
		},
		Signer: store.DossierSigner{
			KeyID: "test-change-brief-key", PrivateKey: privateKey,
		},
	}
	sealed, err := service.Export(ctx, store.DossierExportRequest{
		Principal: "user:owner", SourceSnapshotID: snapshot.ID,
		SupportedClaims:        []string{"consumer-census"},
		EligibilityResult:      "eligible:true",
		FreshnessState:         "current",
		ValidationState:        "released",
		ReviewDueRule:          "on-material-change",
		HandlingClassification: "internal",
	})
	if err != nil {
		t.Fatal(err)
	}
	if sealed.Manifest.ChangeBrief == nil ||
		sealed.Manifest.ChangeBrief.ID != appended.Brief.ID ||
		sealed.Manifest.ChangeBrief.EntryPath != "objects/change-brief.json" {
		t.Fatalf("change brief reference = %+v", sealed.Manifest.ChangeBrief)
	}
	liveBrief, err := s.GetChangeBriefAs(ctx, "user:owner", appended.Brief.ID)
	if err != nil {
		t.Fatalf("read exported change brief: %v", err)
	}
	_, canonicalBrief, err := store.CanonicalChangeBrief(*liveBrief)
	if err != nil {
		t.Fatal(err)
	}
	var canonicalBriefValue any
	if err := json.Unmarshal(canonicalBrief, &canonicalBriefValue); err != nil {
		t.Fatal(err)
	}
	expectedBriefEntry, err := dossier.NewEntry(
		sealed.Manifest.ChangeBrief.EntryPath,
		"application/json",
		canonicalBriefValue,
	)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range sealed.Entries {
		if entry.Path == sealed.Manifest.ChangeBrief.EntryPath {
			found = string(entry.Content) == string(expectedBriefEntry.Content)
		}
	}
	if !found {
		t.Fatal("dossier does not contain the exact canonical change brief entry")
	}
	reopened, err := service.Reopen(ctx, "user:owner", sealed.DossierID)
	if err != nil || reopened.Manifest.ChangeBrief == nil ||
		reopened.Manifest.ChangeBrief.ID != appended.Brief.ID {
		t.Fatalf("reopen change-brief dossier = %+v, %v", reopened, err)
	}
}
