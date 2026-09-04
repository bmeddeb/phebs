package repositoryindex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestSourceMemberReadAccounting(t *testing.T) {
	repositoryDir := t.TempDir()
	git(t, repositoryDir, "init", "-b", "main")
	write(t, repositoryDir, "main.go", "package main\n", 0o644)
	git(t, repositoryDir, "add", ".")
	git(t, repositoryDir, "commit", "-m", "read accounting")
	stage := filepath.Join(t.TempDir(), "source")
	manifest, err := BuildSourceGeneration(
		t.Context(), repositoryDir, stage, "example.com/acme/read-accounting",
		[]store.IndexedRevision{{
			Selector: "HEAD", Branch: "HEAD",
			Commit: git(t, repositoryDir, "rev-parse", "HEAD"),
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.OwnerCount != 1 {
		t.Fatalf("fixture owners = %d, want 1", manifest.OwnerCount)
	}

	t.Run("valid_rereads_and_root_only", func(t *testing.T) {
		for range 2 {
			ctx, ledger := sourceReadScope(t, readaccounting.Counts{MemberVisits: 1})
			if err := ValidateSourceGeneration(ctx, stage, manifest); err != nil {
				t.Fatal(err)
			}
			if counts, err := ledger.Finish(); err != nil || counts != (readaccounting.Counts{MemberVisits: 1}) {
				t.Fatalf("source validation = %+v, %v", counts, err)
			}
		}
		_, ledger := sourceReadScope(t, readaccounting.Counts{})
		if _, err := ReadSourceManifest(stage, manifest.Repository); err != nil {
			t.Fatal(err)
		}
		if counts, err := ledger.Finish(); err != nil || counts != (readaccounting.Counts{}) {
			t.Fatalf("root-only read = %+v, %v", counts, err)
		}
	})

	t.Run("decoded_then_rejected", func(t *testing.T) {
		raw := []byte("{}\n")
		directory := t.TempDir()
		if err := os.WriteFile(filepath.Join(directory, "member.ndjson"), raw, 0o600); err != nil {
			t.Fatal(err)
		}
		ctx, ledger := sourceReadScope(t, readaccounting.Counts{MemberVisits: 1})
		err := validateSourceMembers(ctx, directory, SourceManifest{
			Revisions: []store.IndexedRevision{{}},
			Members:   []SourceMember{{Name: "member.ndjson", ContentBytes: int64(len(raw))}},
		}, nil)
		if !errors.Is(err, ErrInvalidGeneration) {
			t.Fatalf("invalid decoded record = %v", err)
		}
		if counts, err := ledger.Finish(); err != nil || counts != (readaccounting.Counts{MemberVisits: 1}) {
			t.Fatalf("rejected decoded record = %+v, %v", counts, err)
		}
	})

	t.Run("limit_before_delivery", func(t *testing.T) {
		ctx, ledger := sourceReadScope(t, readaccounting.Counts{
			ControlFileReads: 1, MemberVisits: 1,
		})
		delivered := 0
		_, err := WalkPublishedSource(ctx, stage, manifest.Repository, func(SourceRecord) error {
			delivered++
			return nil
		})
		if !errors.Is(err, readaccounting.ErrLimit) || delivered != 0 {
			t.Fatalf("bounded source walk = %v, delivered %d", err, delivered)
		}
		want := readaccounting.Counts{ControlFileReads: 1, MemberVisits: 2}
		if counts, err := ledger.Finish(); !errors.Is(err, readaccounting.ErrLimit) || counts != want {
			t.Fatalf("refused source walk = %+v, %v", counts, err)
		}
	})

	t.Run("manifest_control_limit_refuses_before_members", func(t *testing.T) {
		ctx, ledger := sourceReadScope(t, readaccounting.Counts{MemberVisits: 2})
		if _, err := WalkPublishedSource(ctx, stage, manifest.Repository, nil); !errors.Is(err, readaccounting.ErrLimit) {
			t.Fatalf("source manifest control limit = %v", err)
		}
		counts, err := ledger.Finish()
		want := readaccounting.Counts{ControlFileReads: 1}
		if !errors.Is(err, readaccounting.ErrLimit) || counts != want {
			t.Fatalf("source manifest refusal = %+v, %v", counts, err)
		}
	})
}

func sourceReadScope(t *testing.T, limits readaccounting.Counts) (context.Context, *readaccounting.Ledger) {
	t.Helper()
	ctx, ledger, err := readaccounting.Start(t.Context(), limits)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, ledger
}
