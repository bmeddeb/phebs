package store

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/typedindex"
)

func typedTestGrowthSpec() TypedIndexGrowthSpec {
	d := TypedIndexGrowthDomain{Device: 1, BaseInode: 2, BlockBytes: 4096, TotalBytes: 1 << 30, AvailableBytes: 1 << 29, TotalInodes: 1 << 20, FreeInodes: 1 << 19, FutureBytes: 1 << 20, FutureInodes: 100}
	return TypedIndexGrowthSpec{Workspace: d, Host: d}
}

// These store-only fixtures create no filesystem owner. Their explicit release
// stands for the trusted controller's separately required zero-growth proof.
func (f *typedFixture) acquireGrowth(t *testing.T, c GenerationChunk) {
	t.Helper()
	if _, err := f.s.AcquireTypedIndexGrowth(t.Context(), c, typedTestGrowthSpec()); err == nil {
		return
	} else if !errors.Is(err, typedindex.Capacity) {
		t.Fatal(err)
	}
	held, err := f.s.GetTypedIndexGrowth(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	selected, err := f.s.InspectTypedIndexGrowthRelease(t.Context(), held.AttemptDigest)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.s.ReleaseTypedIndexGrowth(t.Context(), selected); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.AcquireTypedIndexGrowth(t.Context(), c, typedTestGrowthSpec()); err != nil {
		t.Fatal(err)
	}
}
func TestTypedIndexGrowthLeaseAndRelease(t *testing.T) {
	s := newRunnerStore(t)
	ctx := t.Context()
	f := newTypedFixture(t, s, "growth")
	f.enqueue(t, "one")
	c := f.claim(t)
	if _, err := s.AcquireTypedIndexGrowth(ctx, c, typedTestGrowthSpec()); err == nil {
		t.Fatal("acquired without Begin")
	}
	w, err := s.BeginTypedIndex(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.AcquireTypedIndexGrowth(ctx, c, typedTestGrowthSpec())
	if err != nil {
		t.Fatal(err)
	}
	if got.AttemptDigest != w.AttemptDigest || got.PlanningDigest != c.Generation || got.ChunkID != c.ID {
		t.Fatalf("identity: %+v", got)
	}
	again, err := s.AcquireTypedIndexGrowth(ctx, c, typedTestGrowthSpec())
	if err != nil || again != got {
		t.Fatalf("replay: %+v %v", again, err)
	}
	bad := typedTestGrowthSpec()
	bad.Workspace.BaseInode++
	if _, err = s.AcquireTypedIndexGrowth(ctx, c, bad); !errors.Is(err, typedindex.Stale) {
		t.Fatalf("changed spec: %v", err)
	}
	if _, err = s.InspectTypedIndexGrowthRelease(ctx, got.AttemptDigest); !errors.Is(err, typedindex.Stale) {
		t.Fatalf("running release: %v", err)
	}
	if _, err = s.RetryGenerationChunk(ctx, c, "retry", time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	selected, err := s.InspectTypedIndexGrowthRelease(ctx, got.AttemptDigest)
	if err != nil {
		t.Fatal(err)
	}
	next, err := s.ClaimGenerationChunk(ctx, GenerationResourceTypedIndex, "new-worker")
	if err != nil || next == nil {
		t.Fatalf("claim %v %v", next, err)
	}
	if _, err = s.BeginTypedIndex(ctx, *next); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AcquireTypedIndexGrowth(ctx, *next, typedTestGrowthSpec()); !errors.Is(err, typedindex.Capacity) {
		t.Fatalf("retained holder: %v", err)
	}
	originalStatus := selected.chunks[0].Status
	if err = s.typedWrite(ctx, `UPDATE $rid SET status='running' RETURN NONE;`, map[string]any{"rid": generationChunkRecordID(c)}, 1); err != nil {
		t.Fatal(err)
	}
	if err = s.ReleaseTypedIndexGrowth(ctx, selected); !errors.Is(err, typedindex.Stale) {
		t.Fatalf("selection-to-release lease race: %v", err)
	}
	if err = s.typedWrite(ctx, `UPDATE $rid SET status=$status RETURN NONE;`, map[string]any{"rid": generationChunkRecordID(c), "status": originalStatus}, 1); err != nil {
		t.Fatal(err)
	}
	if err = s.ReleaseTypedIndexGrowth(ctx, selected); err != nil {
		t.Fatal(err)
	}
	if err = s.ReleaseTypedIndexGrowth(ctx, selected); !errors.Is(err, typedindex.Stale) {
		t.Fatalf("repeat release: %v", err)
	}
	if _, err = s.GetTypedIndexGrowth(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("released: %v", err)
	}
	if _, err = s.AcquireTypedIndexGrowth(ctx, c, typedTestGrowthSpec()); err == nil {
		t.Fatal("old attempt reacquired")
	}
	if _, err = s.AcquireTypedIndexGrowth(ctx, *next, typedTestGrowthSpec()); err != nil {
		t.Fatal(err)
	}
	requireRetentionExplain(t, ctx, s, "SELECT "+typedAdmissionProjection+" FROM typed_index_attempt WHERE growth_key=$key LIMIT $scan_limit EXPLAIN FULL", map[string]any{"key": typedGrowthKey, "scan_limit": 2}, "IndexScan", "typed_index_attempt_growth")
}
func TestTypedIndexGrowthSpecBounds(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*TypedIndexGrowthSpec)
	}{
		{"zero device", func(s *TypedIndexGrowthSpec) { s.Host.Device = 0 }},
		{"geometry", func(s *TypedIndexGrowthSpec) { s.Host.TotalBytes *= 2 }},
		{"bytes", func(s *TypedIndexGrowthSpec) { s.Workspace.FutureBytes = s.Workspace.AvailableBytes }},
		{"inodes", func(s *TypedIndexGrowthSpec) { s.Workspace.FutureInodes = s.Workspace.FreeInodes }},
		{"block", func(s *TypedIndexGrowthSpec) { s.Host.BlockBytes = 3 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := typedTestGrowthSpec()
			tc.change(&s)
			if validTypedGrowthSpec(s) {
				t.Fatal("invalid accepted")
			}
		})
	}
	if !validTypedGrowthSpec(typedTestGrowthSpec()) {
		t.Fatal("valid refused")
	}
}

func TestTypedIndexGrowthUniqueHolderAndProjection(t *testing.T) {
	s := newRunnerStore(t)
	ctx := t.Context()
	a := newTypedFixture(t, s, "growth-a")
	b := newTypedFixture(t, s, "growth-b")
	a.enqueue(t, "one")
	b.enqueue(t, "one")
	chunks := []GenerationChunk{a.claim(t), b.claim(t)}
	for _, c := range chunks {
		if _, err := s.BeginTypedIndex(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range chunks {
		wg.Go(func() { _, errs[i] = s.AcquireTypedIndexGrowth(ctx, chunks[i], typedTestGrowthSpec()) })
	}
	wg.Wait()
	winner := -1
	for i, e := range errs {
		if e == nil {
			if winner >= 0 {
				t.Fatal("two holders")
			}
			winner = i
		} else if !errors.Is(e, typedindex.Capacity) && !errors.Is(e, typedindex.Stale) {
			t.Fatalf("unclassified race: %v", e)
		}
	}
	if winner < 0 {
		t.Fatalf("no holder: %v", errs)
	}
	g, err := s.GetTypedIndexGrowth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CancelTypedIndex(ctx, chunks[winner].Repository, chunks[winner].Generation); err != nil {
		t.Fatal(err)
	}
	if retained, e := s.GetTypedIndexGrowth(ctx); e != nil || retained != g {
		t.Fatalf("cancel lost holder %v", e)
	}
	if err = s.SetRepoIndexed(ctx, chunks[winner].Repository, strings.Repeat("b", 40), time.Now()); err != nil {
		t.Fatal(err)
	}
	fixture := a
	if winner == 1 {
		fixture = b
	}
	if _, err = s.InstallTypedProfile(ctx, fixture.repo, fixture.profile, fixture.intent.UniverseDigest, fixture.intent.ProfileEpoch); err != nil {
		t.Fatal(err)
	}
	if retained, e := s.GetTypedIndexGrowth(ctx); e != nil || retained != g {
		t.Fatalf("source/profile lost holder: %v", e)
	}
	if err = s.DeleteRepo(ctx, fixture.repo); err != nil {
		t.Fatal(err)
	}
	if err = s.UpsertRepo(ctx, Repo{Name: fixture.repo}); err != nil {
		t.Fatal(err)
	}
	if retained, e := s.GetTypedIndexGrowth(ctx); e != nil || retained != g {
		t.Fatalf("delete/readd lost holder: %v", e)
	}
	// Corrupting the optional index projection cannot manufacture a valid body.
	if err = s.typedWrite(ctx, `UPDATE $rid SET growth_key=NONE RETURN NONE;`, map[string]any{"rid": typedID("typed_index_attempt", g.AttemptDigest)}, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ScanTypedIndexControls(ctx, TypedIndexAttempts, "", 64); !errors.Is(err, typedindex.Invalid) {
		t.Fatalf("hidden held projection %v", err)
	}
	if err = s.typedWrite(ctx, `UPDATE $rid SET growth_key='typed-index' RETURN NONE;`, map[string]any{"rid": typedID("typed_index_attempt", g.AttemptDigest)}, 1); err != nil {
		t.Fatal(err)
	}
	// A canceled durable chunk is settled, but the caller still supplies the
	// independent physical-cleanup proof; these fixtures allocated no owner.
	if err = s.typedWrite(ctx, `UPDATE $rid SET status='canceled' RETURN NONE;`, map[string]any{"rid": generationChunkRecordID(chunks[winner])}, 1); err != nil {
		t.Fatal(err)
	}
	selection, err := s.InspectTypedIndexGrowthRelease(ctx, g.AttemptDigest)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ReleaseTypedIndexGrowth(ctx, selection); err != nil {
		t.Fatal(err)
	}
	if err = s.typedWrite(ctx, `UPDATE $rid SET growth_key='typed-index' RETURN NONE;`, map[string]any{"rid": typedID("typed_index_attempt", g.AttemptDigest)}, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetTypedIndexGrowth(ctx); !errors.Is(err, typedindex.Invalid) {
		t.Fatalf("released indexed holder %v", err)
	}
}
