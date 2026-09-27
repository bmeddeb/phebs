package store

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/bmeddeb/phebs/internal/typedindex"
)

func TestTypedIndexRetainedRootBoundary(t *testing.T) {
	s := newRunnerStore(t)
	ctx := t.Context()
	f := newTypedFixture(t, s, "root-cap")
	source, err := s.GetTypedSource(ctx, f.repo)
	if err != nil {
		t.Fatal(err)
	}
	rawFor := func(key string) []byte {
		return typedTestJSON(t, typedindex.NewRequest(source, f.profile, uint64(f.intent.ProfileEpoch), f.intent.UniverseDigest, key))
	}
	for n := range 63 {
		raw := rawFor(fmt.Sprint(n))
		id := typedDigest(raw)
		body, _ := typedEncode(typedIndexRequest{Raw: string(raw), Root: id, SourceEpoch: 1}, typedindex.MaxRequestBytes+1024)
		if err = s.typedWrite(ctx, `CREATE ONLY $rid SET repository=$repo,request_root=$root,control_key=$root,is_parent=true,custody_state='collecting',body=$body RETURN NONE;`, map[string]any{"rid": typedID("typed_index_request", id), "repo": f.repo, "root": id, "body": body}, 1); err != nil {
			t.Fatal(err)
		}
	}
	requireRetentionExplain(t, ctx, s, typedRootAdmissionSQL+" EXPLAIN FULL", map[string]any{"repository": f.repo, "scan_limit": 129}, "IndexScan", "typed_index_request_repository")
	raws := [][]byte{rawFor("boundary-a"), rawFor("boundary-b")}
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range raws {
		wg.Go(func() { _, errs[i] = s.EnqueueTypedIndex(ctx, f.repo, raws[i]) })
	}
	wg.Wait()
	successes := 0
	var winner []byte
	for i, e := range errs {
		if e == nil {
			successes++
			winner = raws[i]
		}
	}
	if successes != 1 {
		t.Fatalf("boundary results: %v", errs)
	}
	if _, err = s.EnqueueTypedIndex(ctx, f.repo, winner); err != nil {
		t.Fatalf("cap no-op: %v", err)
	}
	if _, err = s.EnqueueTypedIndex(ctx, f.repo, rawFor("overflow")); !errors.Is(err, typedindex.Capacity) {
		t.Fatalf("overflow: %v", err)
	}
	// A parent with a missing discriminator cannot disappear from the census.
	badID := typedDigest(rawFor("0"))
	if err = s.typedWrite(ctx, `UPDATE $rid SET is_parent=NONE RETURN NONE;`, map[string]any{"rid": typedID("typed_index_request", badID)}, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = s.EnqueueTypedIndex(ctx, f.repo, rawFor("malformed")); !errors.Is(err, typedindex.Invalid) {
		t.Fatalf("missing parent projection: %v", err)
	}
	if err = s.typedWrite(ctx, `UPDATE $rid SET is_parent=true RETURN NONE;`, map[string]any{"rid": typedID("typed_index_request", badID)}, 1); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteRepo(ctx, f.repo); err != nil {
		t.Fatal(err)
	}
	replacement := newTypedFixture(t, s, "root-cap")
	replacementSource, err := s.GetTypedSource(ctx, replacement.repo)
	if err != nil {
		t.Fatal(err)
	}
	replacementRaw := typedTestJSON(t, typedindex.NewRequest(replacementSource, replacement.profile, uint64(replacement.intent.ProfileEpoch), replacement.intent.UniverseDigest, "new-incarnation"))
	if _, err = s.EnqueueTypedIndex(ctx, replacement.repo, replacementRaw); !errors.Is(err, typedindex.Capacity) {
		t.Fatalf("incarnation bypassed retained cap: %v", err)
	}

}
func TestTypedIndexRetainedAttemptBoundary(t *testing.T) {
	s := newRunnerStore(t)
	ctx := t.Context()
	f := newTypedFixture(t, s, "attempt-cap")
	f.enqueue(t, "one")
	chunk := f.claim(t)
	// 63 other lease attempts consume the same immutable root's retained budget.
	for n := range 63 {
		id := typedDigest([]byte(fmt.Sprint(n)))
		a := typedIndexAttempt{ChunkIdentity: typedDigest([]byte("chunk")), Root: chunk.Generation, Request: chunk.Generation, Lease: typedDigest([]byte("lease")), Stage: TypedPreflight, States: [5]string{"running", "pending", "pending", "pending", "pending"}}
		body, _ := typedEncode(a, maxTypedControlBytes)
		if err := s.typedWrite(ctx, `CREATE ONLY $rid SET repository=$repo,request_root=$root,control_key=$key,body=$body RETURN NONE;`, map[string]any{"rid": typedID("typed_index_attempt", id), "repo": f.repo, "root": chunk.Generation, "key": id, "body": body}, 1); err != nil {
			t.Fatal(err)
		}
	}
	requireRetentionExplain(t, ctx, s, typedAttemptAdmissionSQL+" EXPLAIN FULL", map[string]any{"root": chunk.Generation, "scan_limit": 65}, "IndexScan", "typed_index_attempt_root")
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Go(func() { _, errs[i] = s.BeginTypedIndex(ctx, chunk) })
	}
	wg.Wait()
	if errs[0] != nil && errs[1] != nil {
		t.Fatalf("boundary: %v", errs)
	}
	if _, err := s.BeginTypedIndex(ctx, chunk); err != nil {
		t.Fatalf("cap no-op: %v", err)
	}
	if err := s.ReleaseGenerationChunk(ctx, chunk, "test lease restart"); err != nil {
		t.Fatal(err)
	}
	next, err := s.ClaimGenerationChunk(ctx, GenerationResourceTypedIndex, "new-lease")
	if err != nil || next == nil {
		t.Fatalf("new lease: %v %v", next, err)
	}
	if _, err = s.BeginTypedIndex(ctx, *next); !errors.Is(err, typedindex.Capacity) {
		t.Fatalf("overflow: %v", err)
	}
	id := typedDigest([]byte("0"))
	if err = s.typedWrite(ctx, `UPDATE $rid SET control_key=NONE RETURN NONE;`, map[string]any{"rid": typedID("typed_index_attempt", id)}, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = s.BeginTypedIndex(ctx, *next); !errors.Is(err, typedindex.Invalid) {
		t.Fatalf("missing key projection: %v", err)
	}
}
