package store_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/store"
)

func TestProofBundleImmutablePersistencePinsRuns(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	repo, commit, domain := "github.com/proof/visible", "aaaaaaaa", "grpc-consumer"
	seedEvidenceRepo(t, s, repo, commit)
	run, err := beginExtractionRun(s, ctx, repo, commit, domain, "grpc-consumer@1")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PublishExtractionRun(ctx, run.ID, testCoverage(0, 0)); err != nil {
		t.Fatal(err)
	}

	content := `{"schema_version":"proof-bundle-v1","value":"immutable"}`
	record := store.ProofBundleRecord{
		ID: store.ComputeProofBundleID(content), Content: content,
		Repositories: []string{repo}, RunIDs: []string{run.ID},
		RetainedAt: time.Now().UTC().Truncate(time.Second),
	}
	if err := s.PutProofBundle(ctx, record); err != nil {
		t.Fatalf("put: %v", err)
	}
	refreshed := record
	refreshed.RetainedAt = record.RetainedAt.Add(time.Minute)
	if err := s.PutProofBundle(ctx, refreshed); err != nil {
		t.Fatalf("idempotent refresh: %v", err)
	}
	got, err := s.GetProofBundle(ctx, refreshed.ID, nil)
	if err != nil || !reflect.DeepEqual(got, &refreshed) {
		t.Fatalf("stored bundle = %+v, %v", got, err)
	}

	conflict := record
	conflict.Repositories = []string{"github.com/proof/other"}
	if err := s.PutProofBundle(ctx, conflict); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("immutable metadata conflict = %v", err)
	}

	replacement, err := beginExtractionRun(s, ctx, repo, commit, domain, "grpc-consumer@2")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PublishExtractionRun(ctx, replacement.ID, testCoverage(0, 0)); err != nil {
		t.Fatal(err)
	}
	if n, err := sweepEvidenceRun(ctx, s, time.Now().UTC(), 0); err != nil || n != 0 {
		t.Fatalf("proof-pinned superseded run swept = %d, %v", n, err)
	}
	if err := s.PinRun(ctx, run.ID, "proof-bundle-check"); err != nil {
		t.Fatalf("proof-pinned run unavailable: %v", err)
	}
}

func TestProofBundleRejectsInvalidOrUnavailableContent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	content := `{"schema_version":"proof-bundle-v1"}`
	validID := store.ComputeProofBundleID(content)
	now := time.Now().UTC()
	rows := []struct {
		name   string
		record store.ProofBundleRecord
	}{
		{name: "wrong digest", record: store.ProofBundleRecord{ID: "pb_" + string(make([]byte, 64)), Content: content, RetainedAt: now}},
		{name: "invalid json", record: store.ProofBundleRecord{ID: store.ComputeProofBundleID("{"), Content: "{", RetainedAt: now}},
		{name: "unsorted repos", record: store.ProofBundleRecord{ID: validID, Content: content, Repositories: []string{"z", "a"}, RetainedAt: now}},
		{name: "duplicate runs", record: store.ProofBundleRecord{ID: validID, Content: content, RunIDs: []string{"run", "run"}, RetainedAt: now}},
		{name: "unavailable run", record: store.ProofBundleRecord{ID: validID, Content: content, RunIDs: []string{"missing"}, RetainedAt: now}},
		{name: "missing retention metadata", record: store.ProofBundleRecord{ID: validID, Content: content}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			if err := s.PutProofBundle(ctx, row.record); err == nil {
				t.Fatal("invalid bundle persisted")
			}
		})
	}
	if _, err := s.GetProofBundle(ctx, "not-a-bundle", nil); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("invalid lookup = %v", err)
	}
}

func TestProofBundleExpiryReleasesOnlyOwnedPins(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	repo, commit, domain := "github.com/proof/retention", "bbbbbbbb", "grpc-consumer"
	seedEvidenceRepo(t, s, repo, commit)
	oldRun, err := beginExtractionRun(s, ctx, repo, commit, domain, "grpc-consumer@1")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PublishExtractionRun(ctx, oldRun.ID, testCoverage(0, 0)); err != nil {
		t.Fatal(err)
	}
	currentRun, err := beginExtractionRun(s, ctx, repo, commit, domain, "grpc-consumer@2")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PublishExtractionRun(ctx, currentRun.ID, testCoverage(0, 0)); err != nil {
		t.Fatal(err)
	}

	base := time.Now().UTC().Truncate(time.Microsecond)
	bundle := func(value string, retainedAt time.Time, runID string) store.ProofBundleRecord {
		content := `{"schema_version":"proof-bundle-v1","value":"` + value + `"}`
		return store.ProofBundleRecord{
			ID: store.ComputeProofBundleID(content), Content: content,
			Repositories: []string{repo}, RunIDs: []string{runID}, RetainedAt: retainedAt,
		}
	}
	first := bundle("first-old-pin", base.Add(-3*time.Hour), oldRun.ID)
	second := bundle("second-old-pin", base.Add(-2*time.Hour), oldRun.ID)
	live := bundle("unexpired", base, currentRun.ID)
	for _, record := range []store.ProofBundleRecord{first, second, live} {
		if err := s.PutProofBundle(ctx, record); err != nil {
			t.Fatalf("put %s: %v", record.ID, err)
		}
	}
	if err := s.PinRun(ctx, currentRun.ID, "migration-checkpoint:retention-test"); err != nil {
		t.Fatalf("pin checkpoint: %v", err)
	}

	firstCutoff := base.Add(-150 * time.Minute)
	if _, err := s.GetProofBundle(ctx, first.ID, &firstCutoff); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expired bundle read = %v, want not found", err)
	}
	if n, err := s.SweepProofBundles(ctx, firstCutoff); err != nil || n != 1 {
		t.Fatalf("sweep first bundle = %d, %v", n, err)
	}
	if n, err := sweepEvidenceRun(ctx, s, base, 0); err != nil || n != 0 {
		t.Fatalf("shared run swept while second bundle pins it = %d, %v", n, err)
	}

	secondCutoff := base.Add(-time.Hour)
	if n, err := s.SweepProofBundles(ctx, secondCutoff); err != nil || n != 1 {
		t.Fatalf("sweep second bundle = %d, %v", n, err)
	}
	if n, err := sweepEvidenceRun(ctx, s, base, 0); err != nil || n != 1 {
		t.Fatalf("newly unpinned superseded run sweep = %d, %v", n, err)
	}
	got, err := s.GetProofBundle(ctx, live.ID, &secondCutoff)
	if err != nil {
		t.Fatalf("read unexpired bundle: %v", err)
	}
	if got.Content != live.Content || got.ID != live.ID || store.ComputeProofBundleID(got.Content) != live.ID {
		t.Fatalf("unexpired bundle changed: got %+v", got)
	}
	if n, err := s.SweepProofBundles(ctx, secondCutoff); err != nil || n != 0 {
		t.Fatalf("unexpired bundle swept = %d, %v", n, err)
	}

	// Expiring the live bundle later releases only its bundle pin. The
	// independently named checkpoint must continue to retain the run after a
	// replacement makes it sweep-eligible.
	finalCutoff := base.Add(time.Minute)
	if n, err := s.SweepProofBundles(ctx, finalCutoff); err != nil || n != 1 {
		t.Fatalf("sweep live bundle = %d, %v", n, err)
	}
	thirdRun, err := beginExtractionRun(s, ctx, repo, commit, domain, "grpc-consumer@3")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PublishExtractionRun(ctx, thirdRun.ID, testCoverage(0, 0)); err != nil {
		t.Fatal(err)
	}
	if n, err := sweepEvidenceRun(ctx, s, base.Add(time.Hour), 0); err != nil || n != 0 {
		t.Fatalf("checkpoint pin removed with bundle pin = %d, %v", n, err)
	}
}
