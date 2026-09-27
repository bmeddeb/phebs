package store

import (
	"bytes"
	"errors"
	"testing"

	"github.com/bmeddeb/phebs/internal/typedindex"
)

func TestTypedIndexManagedPurpose(t *testing.T) {
	s := newRunnerStore(t)
	f := newTypedFixture(t, s, "managed-purpose")
	var good typedindex.PublicationPointer
	var goodRaw string
	for _, purpose := range []typedindex.Purpose{typedindex.Publish, typedindex.Canary, typedindex.DryRun} {
		t.Run(string(purpose), func(t *testing.T) {
			ctx := t.Context()
			source, e := s.GetTypedSource(ctx, f.repo)
			if e != nil {
				t.Fatal(e)
			}
			r := typedindex.NewManagedRequest(source, f.profile, uint64(f.intent.ProfileEpoch), f.intent.UniverseDigest, purpose)
			raw := typedTestJSON(t, r)
			first, e := s.EnqueueTypedIndex(ctx, f.repo, raw)
			if e != nil {
				t.Fatal(e)
			}
			second, e := s.EnqueueTypedIndex(ctx, f.repo, typedTestJSON(t, typedindex.NewManagedRequest(source, f.profile, uint64(f.intent.ProfileEpoch), f.intent.UniverseDigest, purpose)))
			if e != nil || !bytes.Equal(typedTestJSON(t, first), typedTestJSON(t, second)) {
				t.Fatal("managed coalescing", first, second, e)
			}
			bad := r
			bad.IdempotencyKey = "other-browser-token"
			if _, e = s.EnqueueTypedIndex(ctx, f.repo, typedTestJSON(t, bad)); e == nil {
				t.Fatal("caller key accepted")
			}
			chunk := f.claim(t)
			if _, e = s.BeginTypedIndex(ctx, chunk); e != nil {
				t.Fatal(e)
			}
			expected, e := s.ReadExpectedTypedIndexCurrent(ctx, chunk)
			if e != nil {
				t.Fatal(e)
			}
			a, p := f.seal(t, chunk)
			b := f.bundle(t, a, p)
			if a.Purpose() != purpose {
				t.Fatal("successor changed purpose")
			}
			// Replay of original planning request must retain the exact sealed successor.
			if _, e = s.EnqueueTypedIndex(ctx, f.repo, raw); e != nil {
				t.Fatal(e)
			}
			work, e := s.BeginTypedIndex(ctx, chunk)
			if e != nil || work.Admission.Digest() != a.Digest() {
				t.Fatal("planning replay reset desired", e)
			}
			f.advancePublication(t, chunk, b)
			checkManagedRelations(t, s, f, work, a)
			if purpose == typedindex.Publish {
				pointer, e := s.PublishTypedIndexReplacement(ctx, chunk, expected, b)
				if e != nil {
					t.Fatal("publish positive", e)
				}
				current, e := s.ResolveTypedIndexCurrent(ctx, f.repo)
				if e != nil || current != pointer {
					t.Fatal("current positive", e)
				}
				good = pointer
				goodRaw, e = s.typedRead(ctx, "typed_index_current", f.repo)
				if e != nil {
					t.Fatal(e)
				}
			} else {
				before, e := s.typedReadControl(ctx, TypedIndexAttempts, work.AttemptDigest)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = s.PublishTypedIndex(ctx, chunk, good, b); !errors.Is(e, typedindex.Invalid) {
					t.Fatal("nonpublish accepted", e)
				}
				if _, e = s.PublishTypedIndexReplacement(ctx, chunk, expected, b); !errors.Is(e, typedindex.Invalid) {
					t.Fatal("nonpublish replacement accepted", e)
				}
				after, e := s.typedReadControl(ctx, TypedIndexAttempts, work.AttemptDigest)
				if e != nil || before != after {
					t.Fatal("refusal mutated attempt", e)
				}
				current, e := s.typedRead(ctx, "typed_index_current", f.repo)
				if e != nil || current != goodRaw {
					t.Fatal("refusal changed prior current", e)
				}
				// An otherwise coherent forged complete publication cannot turn a check
				// into current authority. This directly tests the resolver's own guard.
				var attempt typedIndexAttempt
				if e = typedDecode(before, maxTypedControlBytes, &attempt); e != nil {
					t.Fatal(e)
				}
				attempt.Stage = TypedComplete
				attempt.States[4] = "complete"
				if !validTypedAttempt(attempt) {
					t.Fatal("invalid negative fixture")
				}
				attemptRaw, _ := typedEncode(attempt, maxTypedControlBytes)
				var root typedindex.BundleRoot
				if e = typedDecode(string(b.RootBytes()), typedindex.MaxRootBytes, &root); e != nil {
					t.Fatal(e)
				}
				pointer := typedindex.PublicationPointer{Epoch: good.Epoch + 1, Binding: root.Binding, RootDigest: b.RootDigest()}
				currentRaw, _ := typedEncode(typedIndexCurrent{Pointer: pointer, AttemptDigest: work.AttemptDigest}, maxTypedControlBytes)
				if e = s.typedWrite(ctx, `UPDATE $attempt SET body=$body; UPDATE $current SET body=$current_body;`, map[string]any{"attempt": typedID("typed_index_attempt", work.AttemptDigest), "body": attemptRaw, "current": typedID("typed_index_current", f.repo), "repository": f.repo, "current_body": currentRaw}, 2); e != nil {
					t.Fatal(e)
				}
				if _, e = s.ResolveTypedIndexCurrentCustody(ctx, f.repo); !errors.Is(e, typedindex.Invalid) {
					t.Fatal("forged nonpublish current resolved", e)
				}
				if e = s.InspectTypedIndexControlRelations(ctx, TypedIndexCurrents, f.repo); !errors.Is(e, typedindex.Invalid) {
					t.Fatal("census accepted nonpublish current", e)
				}
				if e = s.typedWrite(ctx, `UPDATE $current SET body=$current_body; UPDATE $attempt SET body=$body;`, map[string]any{"current": typedID("typed_index_current", f.repo), "current_body": goodRaw, "attempt": typedID("typed_index_attempt", work.AttemptDigest), "body": before}, 2); e != nil {
					t.Fatal(e)
				}
			}
			if current, e := s.ResolveTypedIndexCurrent(ctx, f.repo); e != nil || current != good {
				t.Fatal("prior publication lost", current, e)
			}
			// Settle/release synthetic native custody so the next case can acquire the
			// existing singleton. Tests assert no actual filesystem/native readiness.
			if e = s.CompleteGenerationChunk(ctx, chunk); e != nil {
				t.Fatal(e)
			}
			release, e := s.InspectTypedIndexGrowthRelease(ctx, work.AttemptDigest)
			if e != nil {
				t.Fatal(e)
			}
			if e = s.ReleaseTypedIndexGrowth(ctx, release); e != nil {
				t.Fatal(e)
			}
		})
	}
}

func TestTypedIndexStoredPurposeShape(t *testing.T) {
	h := typedDigest([]byte("identity"))
	r := typedindex.Request{Schema: typedindex.RequestSchema, Action: typedindex.Plan, Source: typedindex.Source{Repository: "example.test/repo", Incarnation: "one", Generation: h, Commit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, Provider: typedindex.ProviderID, ProfileName: "one", ProfileEpoch: 1, ProfileDigest: h, ConfigDigest: h, ToolsDigest: h, UniverseDigest: h, BundleDigest: h, PolicyDigest: h, IdempotencyKey: "legacy"}
	if !typedStoredRequest(r) {
		t.Fatal("legacy shape")
	}
	r.Purpose = typedindex.Publish
	if typedStoredRequest(r) {
		t.Fatal("legacy purpose")
	}
	r.Schema = typedindex.ManagedRequestSchema
	if typedStoredRequest(r) {
		t.Fatal("arbitrary managed key")
	}
}

func checkManagedRelations(t *testing.T, s *Surreal, f *typedFixture, work TypedIndexWork, a typedindex.Admission) {
	t.Helper()
	ctx := t.Context()
	check := func() {
		t.Helper()
		for _, tc := range []struct {
			kind TypedIndexControlKind
			key  string
		}{{TypedIndexIntents, f.repo}, {TypedIndexStates, f.repo}, {TypedIndexRequests, work.RootDigest}, {TypedIndexRequests, a.Digest()}, {TypedIndexPlans, work.RootDigest}, {TypedIndexAttempts, work.AttemptDigest}, {TypedIndexCurrents, f.repo}} {
			if e := s.InspectTypedIndexControlRelations(ctx, tc.kind, tc.key); e != nil {
				t.Fatal("managed relations", tc.kind, e)
			}
		}
		for _, kind := range []TypedIndexControlKind{TypedIndexIntents, TypedIndexStates, TypedIndexRequests, TypedIndexPlans, TypedIndexAttempts, TypedIndexCurrents} {
			if _, e := s.ScanTypedIndexControls(ctx, kind, "", 64); e != nil {
				t.Fatal("managed census", kind, e)
			}
		}
	}
	// No current exists until the first publishing case completes.
	// The exact relation inspector correctly treats a missing row as not found.
	current, e := s.typedRead(ctx, "typed_index_current", f.repo)
	if e != nil {
		t.Fatal(e)
	}
	if current == "" {
		for _, kind := range []TypedIndexControlKind{TypedIndexIntents, TypedIndexStates, TypedIndexRequests, TypedIndexPlans, TypedIndexAttempts, TypedIndexCurrents} {
			if _, e = s.ScanTypedIndexControls(ctx, kind, "", 64); e != nil {
				t.Fatal(e)
			}
		}
		if e = s.InspectTypedIndexControlRelations(ctx, TypedIndexIntents, f.repo); e != nil {
			t.Fatal(e)
		}
	} else {
		check()
	}
	before, e := s.typedReadControl(ctx, TypedIndexRequests, work.RootDigest)
	if e != nil {
		t.Fatal(e)
	}
	var stored typedIndexRequest
	if e = typedDecode(before, typedindex.MaxRequestBytes+1024, &stored); e != nil {
		t.Fatal(e)
	}
	for _, downgrade := range []bool{false, true} {
		bad := work.Parent.Request()
		if downgrade {
			bad.Schema = typedindex.RequestSchema
			bad.Purpose = ""
		} else if bad.Purpose == typedindex.Publish {
			bad.Purpose = typedindex.Canary
		} else {
			bad.Purpose = typedindex.Publish
		}
		changed := stored
		changed.Raw = string(typedTestJSON(t, bad))
		body, _ := typedEncode(changed, typedindex.MaxRequestBytes+1024)
		write := func(v string) {
			t.Helper()
			if e := s.typedWrite(ctx, `UPDATE $rid SET body=$body;`, map[string]any{"rid": typedID("typed_index_request", work.RootDigest), "body": v}, 1); e != nil {
				t.Fatal(e)
			}
		}
		write(body)
		if e = s.InspectTypedIndexControlRelations(ctx, TypedIndexIntents, f.repo); e == nil {
			t.Fatal("purpose/downgrade borrowed intent")
		}
		write(before)
		if e = s.InspectTypedIndexControlRelations(ctx, TypedIndexIntents, f.repo); e != nil {
			t.Fatal("restored managed intent", e)
		}
	}
}
