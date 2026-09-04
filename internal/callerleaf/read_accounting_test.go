package callerleaf

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/bmeddeb/phebs/internal/readaccounting"
)

func TestCallerMemberReadAccounting(t *testing.T) {
	root := filepath.Join(t.TempDir(), "caller-leaves")
	generation, pair := testIdentity(t)
	stage, err := NewStage(root, generation, pair)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stage.Discard() })
	if err := stage.Add(testResultRecord()); err != nil {
		t.Fatal(err)
	}
	prepared, err := stage.Seal()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prepared.Discard() })
	publication, err := prepared.Install(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	receipt := publication.Receipt()

	t.Run("valid_reread_and_warm_hit", func(t *testing.T) {
		ctx, ledger := callerReadScope(t, readaccounting.Counts{MemberVisits: 1})
		seen := 0
		if _, err := Open(ctx, root, generation, pair, receipt, func(Record) error {
			seen++
			return nil
		}); err != nil || seen != 1 {
			t.Fatalf("cold open = %v, seen %d", err, seen)
		}
		if counts, err := ledger.Finish(); err != nil || counts != (readaccounting.Counts{MemberVisits: 1}) {
			t.Fatalf("cold open accounting = %+v, %v", counts, err)
		}

		_, warm := callerReadScope(t, readaccounting.Counts{})
		if !publication.Current() || !publication.Matches(root, generation.Repository, receipt) {
			t.Fatal("warm publication identity did not match")
		}
		if counts, err := warm.Finish(); err != nil || counts != (readaccounting.Counts{}) {
			t.Fatalf("warm identity hit = %+v, %v", counts, err)
		}

		ctx, replay := callerReadScope(t, readaccounting.Counts{MemberVisits: 1})
		var reference RecordReference
		if err := publication.ScanRecords(ctx, generation, pair, func(current RecordReference, _ Record) error {
			reference = current
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if counts, err := replay.Finish(); err != nil || counts != (readaccounting.Counts{MemberVisits: 1}) {
			t.Fatalf("full replay = %+v, %v", counts, err)
		}

		ctx, reread := callerReadScope(t, readaccounting.Counts{MemberVisits: 1})
		if _, err := publication.ReadRecord(ctx, reference); err != nil {
			t.Fatal(err)
		}
		if counts, err := reread.Finish(); err != nil || counts != (readaccounting.Counts{MemberVisits: 1}) {
			t.Fatalf("record reread = %+v, %v", counts, err)
		}
	})

	t.Run("decoded_then_rejected", func(t *testing.T) {
		raw, err := json.Marshal(Record{})
		if err != nil {
			t.Fatal(err)
		}
		raw = append(raw, '\n')
		ctx, ledger := callerReadScope(t, readaccounting.Counts{MemberVisits: 1})
		err = VerifyReader(ctx, bytes.NewReader(raw), generation, pair, receipt, nil)
		if !errors.Is(err, ErrInvalidArtifact) {
			t.Fatalf("invalid decoded caller record = %v", err)
		}
		if counts, err := ledger.Finish(); err != nil || counts != (readaccounting.Counts{MemberVisits: 1}) {
			t.Fatalf("rejected decoded record = %+v, %v", counts, err)
		}
	})

	t.Run("limit_before_delivery", func(t *testing.T) {
		ctx, ledger := callerReadScope(t, readaccounting.Counts{})
		delivered := 0
		_, err := Open(ctx, root, generation, pair, receipt, func(Record) error {
			delivered++
			return nil
		})
		if !errors.Is(err, readaccounting.ErrLimit) || delivered != 0 {
			t.Fatalf("bounded caller open = %v, delivered %d", err, delivered)
		}
		if counts, err := ledger.Finish(); !errors.Is(err, readaccounting.ErrLimit) || counts != (readaccounting.Counts{MemberVisits: 1}) {
			t.Fatalf("refused caller open = %+v, %v", counts, err)
		}
	})

	t.Run("closed_scope_refuses_without_reclassification", func(t *testing.T) {
		ctx, ledger := callerReadScope(t, readaccounting.Counts{MemberVisits: 1})
		if _, err := ledger.Finish(); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(ctx, root, generation, pair, receipt, nil); !errors.Is(err, readaccounting.ErrClosed) {
			t.Fatalf("closed-scope caller open = %v", err)
		}
	})
}

func callerReadScope(t *testing.T, limits readaccounting.Counts) (context.Context, *readaccounting.Ledger) {
	t.Helper()
	ctx, ledger, err := readaccounting.Start(t.Context(), limits)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, ledger
}
