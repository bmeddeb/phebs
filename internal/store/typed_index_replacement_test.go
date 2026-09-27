package store

import (
	"github.com/bmeddeb/phebs/internal/typedindex"
	"strings"
	"testing"
	"time"
)

func TestTypedIndexReplacementExactSnapshot(t *testing.T) {
	s := newRunnerStore(t)
	ctx := t.Context()
	f := newTypedFixture(t, s, "replacement-snapshot")
	f.enqueue(t, "first")
	first := f.claim(t)
	if _, e := s.BeginTypedIndex(ctx, first); e != nil {
		t.Fatal(e)
	}
	absent, e := s.ReadExpectedTypedIndexCurrent(ctx, first)
	if e != nil {
		t.Fatal(e)
	}
	a, p := f.seal(t, first)
	b := f.bundle(t, a, p)
	f.advancePublication(t, first, b)
	old, e := s.PublishTypedIndexReplacement(ctx, first, absent, b)
	if e != nil {
		t.Fatal("absent positive", e)
	}
	if e = s.CompleteGenerationChunk(ctx, first); e != nil {
		t.Fatal(e)
	}
	held, e := s.GetTypedIndexGrowth(ctx)
	if e != nil {
		t.Fatal(e)
	}
	released, e := s.InspectTypedIndexGrowthRelease(ctx, held.AttemptDigest)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ReleaseTypedIndexGrowth(ctx, released); e != nil {
		t.Fatal(e)
	}
	// Replacement mutation authority must preserve the old epoch even after the
	// source changes. Ordinary readers still refuse that stale publication.
	if e = s.SetRepoIndexed(ctx, f.repo, strings.Repeat("b", 40), time.Now()); e != nil {
		t.Fatal(e)
	}
	definition := f.profile.Definition()
	definition.Name = "replacement-profile"
	f.profile, e = typedindex.DecodeProfile(ctx, typedTestJSON(t, definition))
	if e != nil {
		t.Fatal(e)
	}
	f.intent, e = s.InstallTypedProfile(ctx, f.repo, f.profile, f.intent.UniverseDigest, f.intent.ProfileEpoch)
	if e != nil {
		t.Fatal(e)
	}
	f.enqueue(t, "next")
	next := f.claim(t)
	if _, e = s.BeginTypedIndex(ctx, next); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ResolveTypedIndexCurrent(ctx, f.repo); e == nil {
		t.Fatal("stale reader accepted")
	}
	snapshot, e := s.ReadExpectedTypedIndexCurrent(ctx, next)
	if e != nil || snapshot.pointer != old {
		t.Fatal("historical positive", snapshot.pointer, e)
	}
	a, p = f.seal(t, next)
	b = f.bundle(t, a, p)
	f.advancePublication(t, next, b)
	for _, kind := range []string{"current-body", "current-projection", "owner-body", "owner-projection", "forged", "wrong-lease"} {
		t.Run(kind, func(t *testing.T) {
			candidate := snapshot
			chunk := next
			table, key, field, value := "typed_index_current", f.repo, "body", snapshot.current[0].Body+" "
			switch kind {
			case "current-body":
				changed, e := decodeTypedIndexCurrent(snapshot.current[0].Body)
				if e != nil {
					t.Fatal(e)
				}
				changed.AttemptDigest = typedDigest([]byte("different-owner"))
				value = string(typedTestJSON(t, changed))
			case "current-projection":
				field, value = "repository", "example.invalid/other"
			case "owner-body":
				table, key, value = "typed_index_attempt", snapshot.owner[0].ID, snapshot.owner[0].Body+" "
			case "owner-projection":
				table, key, field, value = "typed_index_attempt", snapshot.owner[0].ID, "request_root", typedDigest([]byte("other"))
			case "forged":
				candidate = TypedIndexReplacement{}
			case "wrong-lease":
				chunk.LeaseToken = "other"
			}
			if kind != "forged" && kind != "wrong-lease" {
				if e := s.typedFence(ctx, "UPDATE $row SET "+field+"=$value;", map[string]any{"row": typedID(table, key), "value": value}); e != nil {
					t.Fatal(e)
				}
			}
			if _, e := s.PublishTypedIndexReplacement(ctx, chunk, candidate, b); e == nil {
				t.Fatal("changed snapshot accepted")
			}
			if kind != "forged" && kind != "wrong-lease" {
				original := snapshot.current[0]
				if table == "typed_index_attempt" {
					original = snapshot.owner[0]
				}
				v := original.Body
				if field == "repository" {
					v = original.Repository
				}
				if field == "request_root" {
					v = original.Root
				}
				if e := s.typedFence(ctx, "UPDATE $row SET "+field+"=$value;", map[string]any{"row": typedID(table, key), "value": v}); e != nil {
					t.Fatal(e)
				}
			}
		})
	}
	current, e := s.PublishTypedIndexReplacement(ctx, next, snapshot, b)
	if e != nil || current.Epoch != old.Epoch+1 {
		t.Fatal("restored existing positive", current, e)
	}
}

func TestTypedIndexReplacementReadRace(t *testing.T) {
	s := newRunnerStore(t)
	ctx := t.Context()
	f := newTypedFixture(t, s, "replacement-read-race")
	f.enqueue(t, "one")
	chunk := f.claim(t)
	if _, e := s.BeginTypedIndex(ctx, chunk); e != nil {
		t.Fatal(e)
	}
	_, e := s.readExpectedTypedIndexCurrent(ctx, chunk, func() {
		if e := s.CancelTypedIndex(ctx, f.repo, chunk.Generation); e != nil {
			t.Fatal(e)
		}
	})
	if e == nil {
		t.Fatal("racing cancellation accepted")
	}
	if _, e = s.PublishTypedIndexReplacement(ctx, chunk, TypedIndexReplacement{}, typedindex.Bundle{}); e == nil {
		t.Fatal("zero snapshot accepted")
	}
}
