package gitobj

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/readaccounting"
)

func TestSourceReadAttemptBoundaries(t *testing.T) {
	dir, oid, commit := fixtureRepo(t)
	calls := 0
	refusal := errors.New("source sink refused")
	refuse := false
	ctx, err := readaccounting.WithSourceObserver(t.Context(), func() error {
		calls++
		if refuse {
			return refusal
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ResolveBlob(ctx, dir, commit+":a.txt"); err != nil || calls != 0 {
		t.Fatal("metadata charged", calls, err)
	}
	if _, err := ReadBlob(ctx, dir, "invalid", 20); err == nil || calls != 0 {
		t.Fatal("invalid input charged")
	}
	for _, limit := range []int64{-1, math.MaxInt64} {
		if _, err := ReadBlob(ctx, dir, oid, limit); !errors.Is(err, ErrTooLarge) || calls != 0 {
			t.Fatal("invalid limit submitted", calls, err)
		}
	}
	for i := 0; i < 2; i++ {
		if raw, err := ReadBlob(ctx, dir, oid, 20); err != nil || string(raw) != "hello object" {
			t.Fatal(string(raw), err)
		}
	}
	if calls != 2 {
		t.Fatal(calls)
	}
	refuse = true
	if _, err := ReadBlob(ctx, dir+"/absent", oid, 20); !errors.Is(err, refusal) || calls != 3 {
		t.Fatal("native start preceded refusal", calls, err)
	}
	refuse = false
	reader, err := NewBatchBlobReader(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := reader.Close(); err != nil {
			t.Error(err)
		}
	}()
	for i := 0; i < 2; i++ {
		if raw, err := reader.ReadBlob(ctx, oid, 12); err != nil || string(raw) != "hello object" {
			t.Fatal(string(raw), err)
		}
	}
	refuse = true
	if _, err := reader.ReadBlob(ctx, strings.Repeat("f", 40), 0); !errors.Is(err, refusal) {
		t.Fatal(err)
	}
	refuse = false
	if _, err := reader.ReadBlob(ctx, oid, 12); err != nil {
		t.Fatal("refused batch corrupted protocol", err)
	}
	if calls != 7 {
		t.Fatal(calls)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := reader.ReadBlob(canceled, oid, 12); !errors.Is(err, context.Canceled) || calls != 7 {
		t.Fatal(calls, err)
	}
	if _, err := ReadBlob(canceled, dir, oid, 12); !errors.Is(err, context.Canceled) || calls != 7 {
		t.Fatal(calls, err)
	}
}
