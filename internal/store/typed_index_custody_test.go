package store

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/typedindex"
)

// These fixtures are trusted synthetic filesystem facts for store-only tests.
// They prove no actual copy, inode custody, quiescence, or filesystem readiness.
func (f *typedFixture) custodyInputs(t *testing.T, chunk GenerationChunk) TypedIndexCustody {
	t.Helper()
	work, err := f.s.BeginTypedIndex(t.Context(), chunk)
	if err != nil {
		t.Fatal(err)
	}
	f.acquireGrowth(t, chunk)
	c := TypedIndexCustody{PlanningDigest: work.RootDigest, AttemptDigest: work.AttemptDigest, ManifestDigest: typedDigest([]byte(work.AttemptDigest + "owner-1")), Revision: 1, DirectoryDevice: 1, DirectoryInode: 42}
	if err = f.s.SaveTypedIndexCustody(t.Context(), chunk, "", c); err != nil {
		t.Fatal(err)
	}
	before := c.ManifestDigest
	c.Revision = 2
	c.ManifestDigest = typedDigest([]byte(work.AttemptDigest + "owner-2"))
	c.InputReceiptDigest = typedDigest([]byte("input-receipt"))
	if err = f.s.SaveTypedIndexCustody(t.Context(), chunk, before, c); err != nil {
		t.Fatal(err)
	}
	return c
}
func (f *typedFixture) custodyPublication(t *testing.T, chunk GenerationChunk, bundle typedindex.Bundle) TypedIndexCustody {
	t.Helper()
	work, err := f.s.BeginTypedIndex(t.Context(), chunk)
	if err != nil || work.Custody == nil {
		t.Fatalf("work: %+v %v", work, err)
	}
	c := *work.Custody
	before := c.ManifestDigest
	c.Revision = 3
	c.ManifestDigest = typedDigest([]byte(work.AttemptDigest + "owner-3"))
	c.PublicationReceiptDigest = typedDigest([]byte("publication-receipt"))
	c.PublicationRequestDigest = work.Admission.Digest()
	c.PublicationPlanDigest = work.PlanDigest
	c.PublicationRootDigest = bundle.RootDigest()
	if err = f.s.SaveTypedIndexCustody(t.Context(), chunk, before, c); err != nil {
		t.Fatal(err)
	}
	return c
}
func (f *typedFixture) advancePublication(t *testing.T, chunk GenerationChunk, bundle typedindex.Bundle) {
	t.Helper()
	if err := f.s.AdvanceTypedIndex(t.Context(), chunk, TypedExecution); err != nil {
		t.Fatal(err)
	}
	f.custodyPublication(t, chunk, bundle)
	if err := f.s.AdvanceTypedIndex(t.Context(), chunk, TypedValidation); err != nil {
		t.Fatal(err)
	}
}

func TestTypedIndexCustodyCASAndStageFences(t *testing.T) {
	s := newRunnerStore(t)
	ctx := t.Context()
	f := newTypedFixture(t, s, "custody-cas")
	f.enqueue(t, "one")
	chunk := f.claim(t)
	w, err := s.BeginTypedIndex(ctx, chunk)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AdvanceTypedIndex(ctx, chunk, TypedPreflight); !errors.Is(err, typedindex.Unprepared) {
		t.Fatalf("missing inputs: %v", err)
	}
	f.acquireGrowth(t, chunk)
	c := TypedIndexCustody{PlanningDigest: w.RootDigest, AttemptDigest: w.AttemptDigest, ManifestDigest: typedDigest([]byte("manifest-1")), Revision: 1, DirectoryDevice: 1, DirectoryInode: 42}
	for _, tc := range []struct {
		name   string
		mutate func(*TypedIndexCustody)
	}{
		{"wrong owner", func(c *TypedIndexCustody) { c.AttemptDigest = typedDigest([]byte("other")) }},
		{"wrong planning", func(c *TypedIndexCustody) { c.PlanningDigest = typedDigest([]byte("other")) }},
		{"revision jump", func(c *TypedIndexCustody) { c.Revision = 2; c.InputReceiptDigest = typedDigest([]byte("inputs")) }},
		{"premature input", func(c *TypedIndexCustody) { c.InputReceiptDigest = typedDigest([]byte("inputs")) }},
		{"invalid inode", func(c *TypedIndexCustody) { c.DirectoryInode = 0 }},
		{"invalid digest", func(c *TypedIndexCustody) { c.ManifestDigest = "bad" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := c
			tc.mutate(&bad)
			if err := s.SaveTypedIndexCustody(ctx, chunk, "", bad); err == nil {
				t.Fatal("accepted malformed first reference")
			}
		})
	}
	forged := chunk
	forged.Identity = typedDigest([]byte("forged"))
	if err = s.SaveTypedIndexCustody(ctx, forged, "", c); err == nil {
		t.Fatal("forged lease accepted")
	}
	if err = s.SaveTypedIndexCustody(ctx, chunk, "", c); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveTypedIndexCustody(ctx, chunk, "", c); err == nil {
		t.Fatal("stale CAS accepted")
	}
	if err = s.SaveTypedIndexCustody(ctx, chunk, c.ManifestDigest, c); err != nil {
		t.Fatalf("exact replay: %v", err)
	}
	next := c
	next.Revision = 2
	next.ManifestDigest = typedDigest([]byte("manifest-2"))
	next.InputReceiptDigest = typedDigest([]byte("inputs"))
	changed := next
	changed.DirectoryInode++
	if err = s.SaveTypedIndexCustody(ctx, chunk, c.ManifestDigest, changed); err == nil {
		t.Fatal("directory substitution accepted")
	}
	if err = s.SaveTypedIndexCustody(ctx, chunk, c.ManifestDigest, next); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveTypedIndexCustody(ctx, chunk, c.ManifestDigest, c); err == nil {
		t.Fatal("rollback accepted")
	}
	if err = s.AdvanceTypedIndex(ctx, chunk, TypedPreflight); err != nil {
		t.Fatal(err)
	}
	plan := f.plan(t, w.Parent)
	a, err := s.SealTypedIndexPlan(ctx, chunk, plan)
	if err != nil {
		t.Fatal(err)
	}
	bundle := f.bundle(t, a, plan)
	publication := next
	publication.Revision = 3
	publication.ManifestDigest = typedDigest([]byte("manifest-3"))
	publication.PublicationReceiptDigest = typedDigest([]byte("publication"))
	publication.PublicationRequestDigest = a.Digest()
	publication.PublicationPlanDigest = plan.Digest()
	publication.PublicationRootDigest = bundle.RootDigest()
	if err = s.SaveTypedIndexCustody(ctx, chunk, next.ManifestDigest, publication); err == nil {
		t.Fatal("publication before validation accepted")
	}
	if err = s.AdvanceTypedIndex(ctx, chunk, TypedExecution); err != nil {
		t.Fatal(err)
	}
	if err = s.AdvanceTypedIndex(ctx, chunk, TypedValidation); !errors.Is(err, typedindex.Unprepared) {
		t.Fatalf("missing publication: %v", err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*TypedIndexCustody)
	}{
		{"input substitution", func(c *TypedIndexCustody) { c.InputReceiptDigest = typedDigest([]byte("changed")) }},
		{"request substitution", func(c *TypedIndexCustody) { c.PublicationRequestDigest = w.RootDigest }},
		{"plan substitution", func(c *TypedIndexCustody) { c.PublicationPlanDigest = typedDigest([]byte("other")) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := publication
			tc.mutate(&bad)
			if err := s.SaveTypedIndexCustody(ctx, chunk, next.ManifestDigest, bad); err == nil {
				t.Fatal("accepted substituted publication reference")
			}
		})
	}
	// A trusted but wrong root reference can be persisted, but cannot publish a
	// different bundle. The store does not claim it has opened a filesystem receipt.
	wrong := publication
	wrong.PublicationRootDigest = typedDigest([]byte("wrong root"))
	if err = s.SaveTypedIndexCustody(ctx, chunk, next.ManifestDigest, wrong); err != nil {
		t.Fatal(err)
	}
	if err = s.AdvanceTypedIndex(ctx, chunk, TypedValidation); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PublishTypedIndex(ctx, chunk, typedindex.PublicationPointer{}, bundle); !errors.Is(err, typedindex.Unprepared) {
		t.Fatalf("wrong bundle published: %v", err)
	}
	if _, err = s.ResolveTypedIndexCurrentCustody(ctx, f.repo); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed publish installed current: %v", err)
	}
}

func TestTypedIndexCustodyRetryCannotBorrowOwner(t *testing.T) {
	s := newRunnerStore(t)
	ctx := t.Context()
	f := newTypedFixture(t, s, "custody-retry")
	f.enqueue(t, "one")
	chunk := f.claim(t)
	old := f.custodyInputs(t, chunk)
	if _, err := s.RetryGenerationChunk(ctx, chunk, "retry", time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	next, err := s.ClaimGenerationChunk(ctx, GenerationResourceTypedIndex, "next-owner")
	if err != nil {
		t.Fatal(err)
	}
	work, err := s.BeginTypedIndex(ctx, *next)
	if err != nil {
		t.Fatal(err)
	}
	old.Revision = 1
	old.InputReceiptDigest = ""
	if err = s.SaveTypedIndexCustody(ctx, *next, "", old); err == nil {
		t.Fatal("new lease borrowed old owner")
	}
	if work.Custody != nil {
		t.Fatal("new lease inherited owner")
	}
	f.custodyInputs(t, *next)
}

func TestTypedIndexCustodyControlByteBound(t *testing.T) {
	hash := "sha256:" + strings.Repeat("f", 64)
	c := TypedIndexCustody{PlanningDigest: hash, AttemptDigest: hash, ManifestDigest: hash, Revision: 3, DirectoryDevice: ^uint64(0), DirectoryInode: ^uint64(0), InputReceiptDigest: hash, PublicationReceiptDigest: hash, PublicationRequestDigest: hash, PublicationPlanDigest: hash, PublicationRootDigest: hash}
	attempt := typedIndexAttempt{ChunkIdentity: hash, Root: hash, Request: hash, Lease: hash, Stage: TypedComplete, States: [5]string{"complete", "complete", "complete", "complete", "complete"}, Custody: &c}
	d := TypedIndexGrowthDomain{Device: ^uint64(0), BaseInode: ^uint64(0), BlockBytes: 4096, TotalBytes: ((1 << 63) - 1) &^ 4095, AvailableBytes: ((1 << 63) - 1) &^ 4095, TotalInodes: ^uint64(0), FreeInodes: ^uint64(0), FutureBytes: 1 << 62, FutureInodes: 1 << 63}
	other := d
	other.Device--
	attempt.Growth = &TypedIndexGrowth{PlanningDigest: hash, AttemptDigest: hash, ChunkID: strings.Repeat("z", 128), ChunkIdentity: hash, LeaseDigest: hash, State: "released", Spec: TypedIndexGrowthSpec{Workspace: d, Host: other}}
	raw, err := typedEncode(attempt, maxTypedControlBytes)
	if err != nil || !validTypedAttempt(attempt) || len(raw) > maxTypedControlBytes {
		t.Fatalf("attempt bytes=%d error=%v", len(raw), err)
	}
	t.Logf("max-shaped attempt bytes=%d of %d", len(raw), maxTypedControlBytes)
}

func TestTypedIndexCustodyCurrentExactOwner(t *testing.T) {
	s := newRunnerStore(t)
	ctx := t.Context()
	f := newTypedFixture(t, s, "custody-current")
	f.enqueue(t, "one")
	chunk := f.claim(t)
	a, p := f.seal(t, chunk)
	bundle := f.bundle(t, a, p)
	f.advancePublication(t, chunk, bundle)
	// Fault-inject an absent reference at the publication boundary: even a
	// syntactically complete bundle must not bypass persisted custody.
	attemptID := typedDigest([]byte(chunk.Identity + "\x00" + chunk.LeaseToken))
	beforePublication, err := s.typedReadControl(ctx, TypedIndexAttempts, attemptID)
	if err != nil {
		t.Fatal(err)
	}
	var absent typedIndexAttempt
	if err = typedDecode(beforePublication, maxTypedControlBytes, &absent); err != nil {
		t.Fatal(err)
	}
	absent.Custody = nil
	absentRaw, _ := typedEncode(absent, maxTypedControlBytes)
	if err = s.typedWrite(ctx, `UPDATE $rid SET body=$body RETURN NONE;`, map[string]any{"rid": typedID("typed_index_attempt", attemptID), "body": absentRaw}, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PublishTypedIndex(ctx, chunk, typedindex.PublicationPointer{}, bundle); err == nil {
		t.Fatal("publication without custody accepted")
	}
	if err = s.typedWrite(ctx, `UPDATE $rid SET body=$body RETURN NONE;`, map[string]any{"rid": typedID("typed_index_attempt", attemptID), "body": beforePublication}, 1); err != nil {
		t.Fatal(err)
	}
	pointer, err := s.PublishTypedIndex(ctx, chunk, typedindex.PublicationPointer{}, bundle)
	if err != nil {
		t.Fatal(err)
	}
	current, err := s.ResolveTypedIndexCurrentCustody(ctx, f.repo)
	if err != nil {
		t.Fatal(err)
	}
	if current.Pointer != pointer || current.ChunkIdentity != chunk.Identity || current.LeaseDigest != GenerationLeaseTokenDigest(chunk.LeaseToken) || current.PlanningDigest != chunk.Generation || current.AttemptDigest != typedDigest([]byte(chunk.Identity+"\x00"+chunk.LeaseToken)) || current.Parent.Digest() != chunk.Generation || current.Admission.Digest() != a.Digest() || current.Custody.AttemptDigest != current.AttemptDigest {
		t.Fatalf("current ownership: %+v", current)
	}
	rawCurrent, err := s.typedRead(ctx, "typed_index_current", f.repo)
	if err != nil {
		t.Fatal(err)
	}
	rawAttempt, err := s.typedReadControl(ctx, TypedIndexAttempts, current.AttemptDigest)
	if err != nil {
		t.Fatal(err)
	}
	write := func(table, key, body string) {
		t.Helper()
		if e := s.typedWrite(ctx, `UPDATE $rid SET body=$body RETURN NONE;`, map[string]any{"rid": typedID(table, key), "body": body}, 1); e != nil {
			t.Fatal(e)
		}
	}
	for _, tc := range []struct {
		name string
		body func() string
	}{
		{"missing owner", func() string {
			raw, _ := typedEncode(typedIndexCurrent{Pointer: pointer, AttemptDigest: typedDigest([]byte("missing"))}, maxTypedControlBytes)
			return raw
		}},
		{"legacy pointer only", func() string { raw, _ := typedEncode(pointer, maxTypedControlBytes); return raw }},
		{"pointer bundle mismatch", func() string {
			wrong := pointer
			wrong.RootDigest = typedDigest([]byte("different"))
			raw, _ := typedEncode(typedIndexCurrent{Pointer: wrong, AttemptDigest: current.AttemptDigest}, maxTypedControlBytes)
			return raw
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			write("typed_index_current", f.repo, tc.body())
			if _, e := s.ResolveTypedIndexCurrentCustody(ctx, f.repo); e == nil {
				t.Fatal("invalid current resolved")
			}
			write("typed_index_current", f.repo, rawCurrent)
		})
	}
	var attempt typedIndexAttempt
	if err = typedDecode(rawAttempt, maxTypedControlBytes, &attempt); err != nil {
		t.Fatal(err)
	}
	attempt.Stage = TypedValidation
	attempt.States[3] = "running"
	attempt.States[4] = "pending"
	incomplete, _ := typedEncode(attempt, maxTypedControlBytes)
	write("typed_index_attempt", current.AttemptDigest, incomplete)
	if _, err = s.ResolveTypedIndexCurrentCustody(ctx, f.repo); err == nil {
		t.Fatal("non-complete owner resolved")
	}
	write("typed_index_attempt", current.AttemptDigest, rawAttempt)
	if err = s.CompleteGenerationChunk(ctx, chunk); err != nil {
		t.Fatal(err)
	}
	held, err := s.GetTypedIndexGrowth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	released, err := s.InspectTypedIndexGrowthRelease(ctx, held.AttemptDigest)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ReleaseTypedIndexGrowth(ctx, released); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ResolveTypedIndexCurrentCustody(ctx, f.repo); err != nil {
		t.Fatalf("released completed current: %v", err)
	}

	f.enqueue(t, "replacement")
	replacement := f.claim(t)
	if _, err = s.BeginTypedIndex(ctx, replacement); err != nil {
		t.Fatal(err)
	}
	wrongOwner, _ := typedEncode(typedIndexCurrent{Pointer: pointer, AttemptDigest: typedDigest([]byte(replacement.Identity + "\x00" + replacement.LeaseToken))}, maxTypedControlBytes)
	write("typed_index_current", f.repo, wrongOwner)
	if _, err = s.ResolveTypedIndexCurrentCustody(ctx, f.repo); err == nil {
		t.Fatal("replacement owner adopted old pointer")
	}
	write("typed_index_current", f.repo, rawCurrent)
	// The active state now names the replacement, but the current remains pinned
	// to the completed owner's original immutable reference and reconstructed parent.
	after, err := s.ResolveTypedIndexCurrentCustody(ctx, f.repo)
	if err != nil || after.AttemptDigest != current.AttemptDigest || after.Custody != current.Custody {
		t.Fatalf("replacement stole current: %+v %v", after, err)
	}
	if err = s.FailTypedIndex(ctx, replacement, typedindex.ExecutionFailed); err != nil {
		t.Fatal(err)
	}
	if err = s.CancelTypedIndex(ctx, f.repo, replacement.Generation); err != nil {
		t.Fatal(err)
	}
	after, err = s.ResolveTypedIndexCurrentCustody(ctx, f.repo)
	if err != nil || after.AttemptDigest != current.AttemptDigest || after.Custody != current.Custody {
		t.Fatalf("failed/canceled replacement retired current: %+v %v", after, err)
	}
	if err = s.ClearTypedIndexForRestore(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ResolveTypedIndexCurrentCustody(ctx, f.repo); err == nil {
		t.Fatal("restored custody remained readable")
	}
}

func TestTypedIndexCustodyRetirementCurrentAssociation(t *testing.T) {
	s := newRunnerStore(t)
	ctx := t.Context()
	f := newTypedFixture(t, s, "custody-retirement")
	publish := func(key string, expected typedindex.PublicationPointer) (GenerationChunk, typedindex.PublicationPointer) {
		t.Helper()
		f.enqueue(t, key)
		chunk := f.claim(t)
		a, p := f.seal(t, chunk)
		bundle := f.bundle(t, a, p)
		f.advancePublication(t, chunk, bundle)
		pointer, err := s.PublishTypedIndex(ctx, chunk, expected, bundle)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.CompleteGenerationChunk(ctx, chunk); err != nil {
			t.Fatal(err)
		}
		return chunk, pointer
	}
	old, first := publish("first", typedindex.PublicationPointer{})
	owner, current := publish("second", first)
	ownerID := typedDigest([]byte(owner.Identity + "\x00" + owner.LeaseToken))
	oldID := typedDigest([]byte(old.Identity + "\x00" + old.LeaseToken))
	currentRaw, err := s.typedRead(ctx, "typed_index_current", f.repo)
	if err != nil {
		t.Fatal(err)
	}
	ownerRaw, err := s.typedReadControl(ctx, TypedIndexAttempts, ownerID)
	if err != nil {
		t.Fatal(err)
	}
	mismatched, _ := typedEncode(typedIndexCurrent{Pointer: current, AttemptDigest: oldID}, maxTypedControlBytes)
	if err = s.typedWrite(ctx, `UPDATE $rid SET body=$body RETURN NONE;`, map[string]any{"rid": typedID("typed_index_current", f.repo), "body": mismatched}, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = s.InspectTypedIndexRetirement(ctx, old.Generation); !errors.Is(err, typedindex.Invalid) {
		t.Fatalf("mismatched owner admitted retirement: %v", err)
	}
	if err = s.typedWrite(ctx, `UPDATE $rid SET body=$body RETURN NONE;`, map[string]any{"rid": typedID("typed_index_current", f.repo), "body": currentRaw}, 1); err != nil {
		t.Fatal(err)
	}
	selected, err := s.InspectTypedIndexRetirement(ctx, old.Generation)
	if err != nil || selected.Protected() {
		t.Fatalf("old root: %+v %v", selected, err)
	}
	var changed typedIndexAttempt
	if err = typedDecode(ownerRaw, maxTypedControlBytes, &changed); err != nil {
		t.Fatal(err)
	}
	changed.Custody.ManifestDigest = typedDigest([]byte("changed-reference"))
	changedRaw, _ := typedEncode(changed, maxTypedControlBytes)
	if err = s.typedWrite(ctx, `UPDATE $rid SET body=$body RETURN NONE;`, map[string]any{"rid": typedID("typed_index_attempt", ownerID), "body": changedRaw}, 1); err != nil {
		t.Fatal(err)
	}
	if err = s.BeginTypedIndexRetirement(ctx, selected); !errors.Is(err, typedindex.Stale) {
		t.Fatalf("changed current-owner body accepted: %v", err)
	}
	if err = s.typedWrite(ctx, `UPDATE $rid SET body=$body RETURN NONE;`, map[string]any{"rid": typedID("typed_index_attempt", ownerID), "body": ownerRaw}, 1); err != nil {
		t.Fatal(err)
	}
	selected, err = s.InspectTypedIndexRetirement(ctx, old.Generation)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.typedWrite(ctx, `UPDATE $rid SET control_key=$key RETURN NONE;`, map[string]any{"rid": typedID("typed_index_attempt", ownerID), "key": oldID}, 1); err != nil {
		t.Fatal(err)
	}
	if err = s.BeginTypedIndexRetirement(ctx, selected); !errors.Is(err, typedindex.Stale) {
		t.Fatalf("changed current-owner projection accepted: %v", err)
	}
	if _, err = s.ResolveTypedIndexCurrentCustody(ctx, f.repo); err == nil {
		t.Fatal("malformed current-owner projection resolved")
	}
	if err = s.typedWrite(ctx, `UPDATE $rid SET control_key=$key RETURN NONE;`, map[string]any{"rid": typedID("typed_index_attempt", ownerID), "key": ownerID}, 1); err != nil {
		t.Fatal(err)
	}
	selected, err = s.InspectTypedIndexRetirement(ctx, old.Generation)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.BeginTypedIndexRetirement(ctx, selected); err != nil {
		t.Fatal(err)
	}
	resolved, err := s.ResolveTypedIndexCurrentCustody(ctx, f.repo)
	if err != nil || resolved.Pointer != current || resolved.AttemptDigest != ownerID {
		t.Fatalf("retirement affected actual current: %+v %v", resolved, err)
	}
}
