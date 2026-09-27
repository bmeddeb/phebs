package store

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/typedindex"
)

func checkSummaryChecksum(t *testing.T, c typedindex.CheckedSummary) string {
	t.Helper()
	c.Checksum = ""
	return typedDigest(append([]byte("phebs-typed-checked-summary-v1\x00"), typedTestJSON(t, c)...))
}
func settleCheckFixture(t *testing.T, f *typedFixture, chunk GenerationChunk, attempt string) {
	t.Helper()
	ctx := t.Context()
	if e := f.s.CompleteGenerationChunk(ctx, chunk); e != nil {
		t.Fatal(e)
	}
	snapshot, e := f.s.InspectTypedIndexGrowthRelease(ctx, attempt)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.s.ReleaseTypedIndexGrowth(ctx, snapshot); e != nil {
		t.Fatal(e)
	}
}
func TestTypedIndexCheckedWorkflow(t *testing.T) {
	s := newRunnerStore(t)
	ctx := t.Context()
	f := newTypedFixture(t, s, "checked")
	f.enqueue(t, "published")
	old := f.claim(t)
	a, p := f.seal(t, old)
	b := f.bundle(t, a, p)
	f.advancePublication(t, old, b)
	pointer, e := s.PublishTypedIndex(ctx, old, typedindex.PublicationPointer{}, b)
	if e != nil {
		t.Fatal(e)
	}
	settleCheckFixture(t, f, old, typedDigest([]byte(old.Identity+"\x00"+old.LeaseToken)))
	for _, purpose := range []typedindex.Purpose{typedindex.Canary, typedindex.DryRun} {
		t.Run(string(purpose), func(t *testing.T) {
			source, e := s.GetTypedSource(ctx, f.repo)
			if e != nil {
				t.Fatal(e)
			}
			raw := typedTestJSON(t, typedindex.NewManagedRequest(source, f.profile, uint64(f.intent.ProfileEpoch), f.intent.UniverseDigest, purpose))
			if _, e = s.EnqueueTypedIndex(ctx, f.repo, raw); e != nil {
				t.Fatal(e)
			}
			chunk := f.claim(t)
			a, p := f.seal(t, chunk)
			b := f.bundle(t, a, p)
			if _, e = s.CompleteTypedIndexCheck(ctx, chunk, b); e == nil {
				t.Fatal("check before validation")
			}
			if e = s.AdvanceTypedIndex(ctx, chunk, TypedExecution); e != nil {
				t.Fatal(e)
			}
			attempt := typedDigest([]byte(chunk.Identity + "\x00" + chunk.LeaseToken))
			before, e := s.typedReadControl(ctx, TypedIndexAttempts, attempt)
			if e != nil {
				t.Fatal(e)
			}
			// Before the terminal CAS, an otherwise valid finished bundle is not durable success.
			d, e := s.InspectTypedIndexDisposition(ctx, chunk)
			if e != nil || d.State() != TypedIndexInterrupted {
				t.Fatal("pre-CAS", d.State(), e)
			}
			forged := chunk
			forged.LeaseToken = "other"
			if _, e = s.CompleteTypedIndexCheck(ctx, forged, b); e == nil {
				t.Fatal("stale lease")
			}
			if _, e = s.CompleteTypedIndexCheck(ctx, chunk, typedindex.Bundle{}); e == nil {
				t.Fatal("zero bundle")
			}
			summary, e := s.CompleteTypedIndexCheck(ctx, chunk, b)
			if e != nil {
				t.Fatal(e)
			}
			again, e := s.CompleteTypedIndexCheck(ctx, chunk, b)
			if e != nil || again != summary {
				t.Fatal("ambiguous-CAS replay", e)
			}
			stored, e := s.typedReadControl(ctx, TypedIndexAttempts, attempt)
			if e != nil || stored == before {
				t.Fatal("terminal not saved", e)
			}
			status, e := s.GetTypedIndexStatus(ctx, f.repo)
			if e != nil || status.Stage != TypedChecked || status.Check == nil || *status.Check != summary || status.States != [5]string{"complete", "complete", "complete", "complete", "not_requested"} || status.Current == nil || *status.Current != pointer {
				t.Fatal("checked status", status, e)
			}
			inspected, e := s.InspectTypedIndexAttempt(ctx, attempt)
			if e != nil || inspected.Check == nil || *inspected.Check != summary || inspected.Custody.Revision != 2 {
				t.Fatal("checked inspection", inspected, e)
			}
			d, e = s.InspectTypedIndexDisposition(ctx, chunk)
			if e != nil || d.State() != TypedIndexAlreadyChecked {
				t.Fatal("checked disposition", d.State(), e)
			}
			for _, tc := range []struct {
				kind TypedIndexControlKind
				key  string
			}{{TypedIndexAttempts, attempt}, {TypedIndexStates, f.repo}, {TypedIndexIntents, f.repo}, {TypedIndexRequests, a.Digest()}, {TypedIndexPlans, chunk.Generation}} {
				if e = s.InspectTypedIndexControlRelations(ctx, tc.kind, tc.key); e != nil {
					t.Fatal(tc.kind, e)
				}
			}
			var control typedIndexAttempt
			if e = typedDecode(stored, maxTypedControlBytes, &control); e != nil {
				t.Fatal(e)
			}
			mutations := []func(*typedindex.CheckedSummary){func(c *typedindex.CheckedSummary) { c.Purpose = typedindex.Publish }, func(c *typedindex.CheckedSummary) { c.ParentDigest = typedDigest(nil) }, func(c *typedindex.CheckedSummary) { c.RequestDigest = typedDigest(nil) }, func(c *typedindex.CheckedSummary) { c.PlanDigest = typedDigest(nil) }, func(c *typedindex.CheckedSummary) { c.RootDigest = typedDigest(nil) }, func(c *typedindex.CheckedSummary) { c.Units++ }, func(c *typedindex.CheckedSummary) { c.Targets++ }, func(c *typedindex.CheckedSummary) { c.Members++ }, func(c *typedindex.CheckedSummary) { c.Documents++ }, func(c *typedindex.CheckedSummary) { c.Generated++ }, func(c *typedindex.CheckedSummary) { c.Checksum = typedDigest(nil) }}
			write := func(raw string) {
				t.Helper()
				if e := s.typedWrite(ctx, `UPDATE $rid SET body=$body;`, map[string]any{"rid": typedID("typed_index_attempt", attempt), "body": raw}, 1); e != nil {
					t.Fatal(e)
				}
			}
			for _, mutate := range mutations {
				bad := summary
				mutate(&bad)
				changed := control
				changed.Check = &bad
				body, _ := typedEncode(changed, maxTypedControlBytes)
				write(body)
				if _, e = s.InspectTypedIndexDisposition(ctx, chunk); e == nil {
					t.Fatal("corrupt summary reused")
				}
				if _, e = s.InspectTypedIndexAttempt(ctx, attempt); e == nil {
					t.Fatal("corrupt summary inspected")
				}
				if _, e = s.GetTypedIndexStatus(ctx, f.repo); e == nil {
					t.Fatal("corrupt summary status")
				}
				write(stored)
				if _, e = s.InspectTypedIndexDisposition(ctx, chunk); e != nil {
					t.Fatal("restored check", e)
				}
			}
			// A structurally rechecksummed plan/purpose swap still cannot borrow the
			// immutable execution/parent relation. Counts alone cannot be rederived later.

			for _, mutate := range []func(*typedindex.CheckedSummary){func(c *typedindex.CheckedSummary) { c.PlanDigest = typedDigest([]byte("foreign-plan")) }, func(c *typedindex.CheckedSummary) { c.ParentDigest = typedDigest([]byte("foreign-parent")) }, func(c *typedindex.CheckedSummary) {
				if c.Purpose == typedindex.Canary {
					c.Purpose = typedindex.DryRun
				} else {
					c.Purpose = typedindex.Canary
				}
			}} {
				bad := summary
				mutate(&bad)
				bad.Checksum = checkSummaryChecksum(t, bad)
				changed := control
				changed.Check = &bad
				body, _ := typedEncode(changed, maxTypedControlBytes)
				write(body)
				if _, e = s.InspectTypedIndexAttempt(ctx, attempt); e == nil {
					t.Fatal("foreign checked relation")
				}
				if _, e = s.GetTypedIndexStatus(ctx, f.repo); e == nil {
					t.Fatal("foreign checked status")
				}
				write(stored)
				if _, e = s.GetTypedIndexStatus(ctx, f.repo); e != nil {
					t.Fatal("restored status", e)
				}
			}

			if _, e = s.PublishTypedIndex(ctx, chunk, pointer, b); e == nil {
				t.Fatal("checked became publication")
			}
			// Lose the scheduler reply and acquire another lease. History, not last-state
			// or caller memory, proves success without creating an attempt.
			if e = s.ReleaseGenerationChunk(ctx, chunk, "lost reply"); e != nil {
				t.Fatal(e)
			}
			next, e := s.ClaimGenerationChunk(ctx, GenerationResourceTypedIndex, "checked-retry")
			if e != nil || next == nil {
				t.Fatal(e)
			}
			d, e = s.InspectTypedIndexDisposition(ctx, *next)
			if e != nil || d.State() != TypedIndexAlreadyChecked {
				t.Fatal("new lease check", d.State(), e)
			}
			newID := typedDigest([]byte(next.Identity + "\x00" + next.LeaseToken))
			newRaw, e := s.typedReadControl(ctx, TypedIndexAttempts, newID)
			if e != nil || newRaw != "" {
				t.Fatal("reuse created attempt", e)
			}
			if e = s.CompleteGenerationChunk(ctx, *next); e != nil {
				t.Fatal(e)
			}
			release, e := s.InspectTypedIndexGrowthRelease(ctx, attempt)
			if e != nil {
				t.Fatal(e)
			}
			if e = s.ReleaseTypedIndexGrowth(ctx, release); e != nil {
				t.Fatal(e)
			}
			retirement, e := s.InspectTypedIndexRetirement(ctx, chunk.Generation)
			if e != nil {
				t.Fatal(e)
			}
			if e = s.BeginTypedIndexRetirement(ctx, retirement); e == nil {
				t.Fatal("desired checked retired")
			}
			if e = s.CancelTypedIndex(ctx, f.repo, a.Digest()); e != nil {
				t.Fatal(e)
			}
			retirement, e = s.InspectTypedIndexRetirement(ctx, chunk.Generation)
			if e != nil {
				t.Fatal(e)
			}
			if e = s.BeginTypedIndexRetirement(ctx, retirement); e != nil {
				t.Fatal(e)
			}
			if _, e = s.InspectTypedIndexAttempt(ctx, attempt); e != nil {
				t.Fatal("obsolete checked owner", e)
			}
			current, e := s.ResolveTypedIndexCurrent(ctx, f.repo)
			if e != nil || current != pointer {
				t.Fatal("prior current", e)
			}
		})
	}
}

func TestTypedIndexCheckedControlBound(t *testing.T) {
	h := "sha256:" + strings.Repeat("f", 64)
	c := TypedIndexCustody{PlanningDigest: h, AttemptDigest: h, ManifestDigest: h, Revision: 2, DirectoryDevice: ^uint64(0), DirectoryInode: ^uint64(0), InputReceiptDigest: h}
	summary := typedindex.CheckedSummary{Purpose: typedindex.DryRun, ParentDigest: h, RequestDigest: h, PlanDigest: h, RootDigest: h, Units: typedindex.MaxBundleUnits, Targets: typedindex.MaxBundleTargets, Members: typedindex.MaxSCIPMembers, Documents: typedindex.MaxBundleDocuments, Generated: typedindex.MaxBundleDocuments}
	summary.Checksum = checkSummaryChecksum(t, summary)
	d := TypedIndexGrowthDomain{Device: ^uint64(0), BaseInode: ^uint64(0), BlockBytes: 4096, TotalBytes: ((1 << 63) - 1) &^ 4095, AvailableBytes: ((1 << 63) - 1) &^ 4095, TotalInodes: ^uint64(0), FreeInodes: ^uint64(0), FutureBytes: 1 << 62, FutureInodes: 1 << 63}
	other := d
	other.Device--
	a := typedIndexAttempt{ChunkIdentity: h, Root: h, Request: h, Lease: h, Stage: TypedChecked, States: [5]string{"complete", "complete", "complete", "complete", "not_requested"}, Custody: &c, Check: &summary, Growth: &TypedIndexGrowth{PlanningDigest: h, AttemptDigest: h, ChunkID: strings.Repeat("z", 128), ChunkIdentity: h, LeaseDigest: h, State: "released", Spec: TypedIndexGrowthSpec{Workspace: d, Host: other}}}
	raw, e := typedEncode(a, maxTypedControlBytes)
	if e != nil || !validTypedAttempt(a) {
		t.Fatal("max check", len(raw), e)
	}
	t.Logf("max checked bytes=%d/%d", len(raw), maxTypedControlBytes)
	a.Stage = TypedComplete
	if validTypedAttempt(a) {
		t.Fatal("check on complete")
	}
	a.Stage = TypedChecked
	a.Check = nil
	if validTypedAttempt(a) {
		t.Fatal("checked without summary")
	}
	if !errors.Is((typedindex.CheckedSummary{}).Validate(), typedindex.Invalid) {
		t.Fatal("zero summary")
	}
}

func TestTypedIndexCheckAuthorityFences(t *testing.T) {
	s := newRunnerStore(t)
	ctx := t.Context()
	for _, fault := range []string{"source", "profile", "cancel", "plan-snapshot"} {
		t.Run(fault, func(t *testing.T) {
			f := newTypedFixture(t, s, "check-fence-"+fault)
			source, e := s.GetTypedSource(ctx, f.repo)
			if e != nil {
				t.Fatal(e)
			}
			raw := typedTestJSON(t, typedindex.NewManagedRequest(source, f.profile, uint64(f.intent.ProfileEpoch), f.intent.UniverseDigest, typedindex.Canary))
			if _, e = s.EnqueueTypedIndex(ctx, f.repo, raw); e != nil {
				t.Fatal(e)
			}
			chunk := f.claim(t)
			a, p := f.seal(t, chunk)
			b := f.bundle(t, a, p)
			if e = s.AdvanceTypedIndex(ctx, chunk, TypedExecution); e != nil {
				t.Fatal(e)
			}
			attempt := typedDigest([]byte(chunk.Identity + "\x00" + chunk.LeaseToken))
			before, e := s.typedReadControl(ctx, TypedIndexAttempts, attempt)
			if e != nil {
				t.Fatal(e)
			}
			switch fault {
			case "source":
				e = s.SetRepoIndexed(ctx, f.repo, strings.Repeat("b", 40), time.Now())
			case "profile":
				_, e = s.InstallTypedProfile(ctx, f.repo, f.profile, f.intent.UniverseDigest, f.intent.ProfileEpoch)
			case "cancel":
				e = s.CancelTypedIndex(ctx, f.repo, a.Digest())
			case "plan-snapshot":
				x, err := s.typedExecution(ctx, chunk, true)
				if err != nil {
					t.Fatal(err)
				}
				summary, err := typedindex.CheckBundle(ctx, a, b)
				if err != nil {
					t.Fatal(err)
				}
				guard, err := typedCheckFence(x)
				if err != nil {
					t.Fatal(err)
				}
				oldPlan, err := s.typedReadControl(ctx, TypedIndexPlans, chunk.Generation)
				if err != nil {
					t.Fatal(err)
				}
				if err = s.typedWrite(ctx, `UPDATE $rid SET body=$body;`, map[string]any{"rid": typedID("typed_index_plan", chunk.Generation), "body": oldPlan + " "}, 1); err != nil {
					t.Fatal(err)
				}
				next := x.attempt
				next.Stage = TypedChecked
				next.Check = &summary
				next.States = [5]string{"complete", "complete", "complete", "complete", "not_requested"}
				if err = s.typedSaveAttempt(ctx, x, next, guard, 0); err == nil {
					t.Fatal("plan selection race committed")
				}
				if err = s.typedWrite(ctx, `UPDATE $rid SET body=$body;`, map[string]any{"rid": typedID("typed_index_plan", chunk.Generation), "body": oldPlan}, 1); err != nil {
					t.Fatal(err)
				}
				if after, err := s.typedReadControl(ctx, TypedIndexAttempts, attempt); err != nil || after != before {
					t.Fatal("failed transaction changed attempt", err)
				}
				if _, err = s.CompleteTypedIndexCheck(ctx, chunk, b); err != nil {
					t.Fatal("restored plan", err)
				}
			}
			if e != nil {
				t.Fatal(e)
			}
			if fault != "plan-snapshot" {
				if _, e = s.CompleteTypedIndexCheck(ctx, chunk, b); e == nil {
					t.Fatal("changed authority checked")
				}
				after, e := s.typedReadControl(ctx, TypedIndexAttempts, attempt)
				if e != nil || after != before {
					t.Fatal("refusal changed attempt", e)
				}
			}
			if e = s.FailGenerationChunk(ctx, chunk, "fixture settled"); e != nil && !errors.Is(e, ErrConflict) {
				t.Fatal(e)
			}
			// The selected source/profile change may stale the ordinary scheduler mutation;
			// use the existing stale release inspector only after a settled durable lease.
			if e = s.typedWrite(ctx, `UPDATE $rid SET status='failed' RETURN NONE;`, map[string]any{"rid": generationChunkRecordID(chunk)}, 1); e != nil {
				t.Fatal(e)
			}
			release, e := s.InspectTypedIndexGrowthRelease(ctx, attempt)
			if e != nil {
				t.Fatal(e)
			}
			if e = s.ReleaseTypedIndexGrowth(ctx, release); e != nil {
				t.Fatal(e)
			}
		})
	}
}

func TestTypedIndexCheckedStatusAuthority(t *testing.T) {
	s := newRunnerStore(t)
	ctx := t.Context()
	for _, fault := range []string{"source", "profile"} {
		t.Run(fault, func(t *testing.T) {
			f := newTypedFixture(t, s, "checked-status-"+fault)
			source, e := s.GetTypedSource(ctx, f.repo)
			if e != nil {
				t.Fatal(e)
			}
			raw := typedTestJSON(t, typedindex.NewManagedRequest(source, f.profile, uint64(f.intent.ProfileEpoch), f.intent.UniverseDigest, typedindex.DryRun))
			if _, e = s.EnqueueTypedIndex(ctx, f.repo, raw); e != nil {
				t.Fatal(e)
			}
			chunk := f.claim(t)
			a, p := f.seal(t, chunk)
			b := f.bundle(t, a, p)
			if e = s.AdvanceTypedIndex(ctx, chunk, TypedExecution); e != nil {
				t.Fatal(e)
			}
			if _, e = s.CompleteTypedIndexCheck(ctx, chunk, b); e != nil {
				t.Fatal(e)
			}
			attempt := typedDigest([]byte(chunk.Identity + "\x00" + chunk.LeaseToken))
			settleCheckFixture(t, f, chunk, attempt)
			before, e := s.GetTypedIndexStatus(ctx, f.repo)
			if e != nil || before.Check == nil || before.Current != nil || before.Stale {
				t.Fatal("positive status", before, e)
			}
			if fault == "source" {
				e = s.SetRepoIndexed(ctx, f.repo, strings.Repeat("b", 40), time.Now())
			} else {
				_, e = s.InstallTypedProfile(ctx, f.repo, f.profile, f.intent.UniverseDigest, f.intent.ProfileEpoch)
			}
			if e != nil {
				t.Fatal(e)
			}
			if _, e = s.InspectTypedIndexAttempt(ctx, attempt); e != nil {
				t.Fatal("historical check", e)
			}
			after, e := s.GetTypedIndexStatus(ctx, f.repo)
			if e != nil || after.Check != nil || after.Stage == TypedChecked || after.Current != nil {
				t.Fatal("stale success exposed", after, e)
			}
			if fault == "source" && !after.Stale {
				t.Fatal("source stale flag missing")
			}
		})
	}
}
