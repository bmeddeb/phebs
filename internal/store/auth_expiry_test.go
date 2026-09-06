package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/surrealdb/surrealdb.go/pkg/models"
)

func TestAuthExpiryEmptyGuard(t *testing.T) {
	for _, test := range []struct {
		name                   string
		rows                   any
		cancel, fail, positive bool
	}{
		{name: "empty", rows: []models.RecordID{}},
		{name: "expired", rows: []models.RecordID{authSessionID("expired")}, positive: true},
		{name: "null", rows: nil, fail: true},
		{name: "extra", rows: []models.RecordID{authSessionID("a"), authSessionID("b")}, fail: true},
		{name: "wrong_table", rows: []models.RecordID{models.NewRecordID("repo", "a")}, fail: true},
		{name: "read_error", rows: []models.RecordID{}, fail: true},
		{name: "cancel_empty", rows: []models.RecordID{}, cancel: true, fail: true},
		{name: "cancel_positive", rows: []models.RecordID{authSessionID("a")}, cancel: true, fail: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			steps := []restoreClearStep{{contains: "SELECT VALUE id FROM auth_session WHERE expiry <= $now LIMIT 1", rows: test.rows, cancel: test.cancel}}
			if test.name == "read_error" {
				steps[0].err = errors.New("read unavailable")
			}
			want := 0
			if test.positive {
				steps = append(steps, restoreClearStep{contains: "DELETE auth_session WHERE expiry <= $now RETURN BEFORE", rows: []authSessionRec{{}, {}}})
				want = 2 // Return the deletion's count, not the one-row probe count.
			}
			s, conn := restoreClearScript(t, steps, cancel)
			got, err := s.DeleteExpiredAuthSessions(ctx, time.Now())
			if (err != nil) != test.fail || got != want || conn.calls != len(steps) {
				t.Fatalf("expiry count=%d calls=%d error=%v; want%d/%d failure=%v", got, conn.calls, err, want, len(steps), test.fail)
			}
			if test.cancel && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation=%v", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := (&Surreal{}).DeleteExpiredAuthSessions(ctx, time.Now()); !errors.Is(err, context.Canceled) {
		t.Fatalf("precanceled expiry reached SDK: %v", err)
	}
}

func TestAuthExpiryNative(t *testing.T) {
	deadline := time.Now().Add(time.Minute)
	if outer, ok := t.Deadline(); ok && outer.Add(-time.Minute).Before(deadline) {
		deadline = outer.Add(-time.Minute)
	}
	ctx, cancel := context.WithDeadline(t.Context(), deadline)
	t.Cleanup(cancel)
	if err := ctx.Err(); err != nil {
		t.Fatal(err)
	}
	s, err := OpenLocalMemory(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		if err := s.Close(cleanup); err != nil {
			t.Error(err)
		}
	})
	now := time.Now().UTC().Truncate(time.Millisecond)
	if n, err := s.DeleteExpiredAuthSessions(ctx, now); err != nil || n != 0 {
		t.Fatalf("empty=%d,%v", n, err)
	}
	for name, expiry := range map[string]time.Time{"expired": now.Add(-time.Second), "boundary": now, "live": now.Add(time.Hour)} {
		if err := s.CommitAuthSession(ctx, name, []byte(name), expiry); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := s.DeleteExpiredAuthSessions(ctx, now); err != nil || n != 2 {
		t.Fatalf("expired=%d,%v", n, err)
	}
	if n, err := s.DeleteExpiredAuthSessions(ctx, now); err != nil || n != 0 {
		t.Fatalf("settled=%d,%v", n, err)
	}
	if body, present, err := s.FindAuthSession(ctx, "live", now); err != nil || !present || string(body) != "live" {
		t.Fatalf("live session changed: %q,%v,%v", body, present, err)
	}
}
