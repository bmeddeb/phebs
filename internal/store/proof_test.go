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
	run, err := s.BeginExtractionRun(ctx, repo, commit, domain, "grpc-consumer@1")
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
	}
	for range 2 {
		if err := s.PutProofBundle(ctx, record); err != nil {
			t.Fatalf("idempotent put: %v", err)
		}
	}
	got, err := s.GetProofBundle(ctx, record.ID)
	if err != nil || !reflect.DeepEqual(got, &record) {
		t.Fatalf("stored bundle = %+v, %v", got, err)
	}

	conflict := record
	conflict.Repositories = []string{"github.com/proof/other"}
	if err := s.PutProofBundle(ctx, conflict); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("immutable metadata conflict = %v", err)
	}

	replacement, err := s.BeginExtractionRun(ctx, repo, commit, domain, "grpc-consumer@2")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PublishExtractionRun(ctx, replacement.ID, testCoverage(0, 0)); err != nil {
		t.Fatal(err)
	}
	if n, err := s.SweepEvidence(ctx, time.Now().UTC(), 0); err != nil || n != 0 {
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
	rows := []struct {
		name   string
		record store.ProofBundleRecord
	}{
		{name: "wrong digest", record: store.ProofBundleRecord{ID: "pb_" + string(make([]byte, 64)), Content: content}},
		{name: "invalid json", record: store.ProofBundleRecord{ID: store.ComputeProofBundleID("{"), Content: "{"}},
		{name: "unsorted repos", record: store.ProofBundleRecord{ID: validID, Content: content, Repositories: []string{"z", "a"}}},
		{name: "duplicate runs", record: store.ProofBundleRecord{ID: validID, Content: content, RunIDs: []string{"run", "run"}}},
		{name: "unavailable run", record: store.ProofBundleRecord{ID: validID, Content: content, RunIDs: []string{"missing"}}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			if err := s.PutProofBundle(ctx, row.record); err == nil {
				t.Fatal("invalid bundle persisted")
			}
		})
	}
	if _, err := s.GetProofBundle(ctx, "not-a-bundle"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("invalid lookup = %v", err)
	}
}
