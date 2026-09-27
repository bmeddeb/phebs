package store

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/typedindex"
)

func TestTypedIndexLifecycle(t *testing.T) {
	s := newRunnerStore(t)
	ctx := t.Context()
	t.Run("bounded inventory and sentinel", func(t *testing.T) {
		f := newTypedFixture(t, s, "lifecycle-page")
		source, err := s.GetTypedSource(ctx, f.repo)
		if err != nil {
			t.Fatal(err)
		}
		authority, err := s.typedAuthority(ctx, f.repo)
		if err != nil {
			t.Fatal(err)
		}
		for n := 0; n < 65; n++ {
			request := typedindex.NewRequest(source, f.profile, uint64(f.intent.ProfileEpoch), f.intent.UniverseDigest, fmt.Sprintf("page-%d", n))
			raw := typedTestJSON(t, request)
			id := typedDigest(raw)
			body, _ := typedEncode(typedIndexRequest{Raw: string(raw), Root: id, SourceEpoch: authority.source.Epoch}, typedindex.MaxRequestBytes+1024)
			if err = s.typedWrite(ctx, `CREATE ONLY $rid SET repository=$repo,request_root=$root,control_key=$root,is_parent=true,custody_state='live',body=$body RETURN NONE;`, map[string]any{"rid": typedID("typed_index_request", id), "repo": f.repo, "root": id, "body": body}, 1); err != nil {
				t.Fatal(err)
			}
		}
		page, err := s.ScanTypedIndexRoots(ctx, "", 64)
		if err != nil || len(page.Rows) != 64 || page.Next == "" {
			t.Fatalf("first: %+v %v", page, err)
		}
		tail, err := s.ScanTypedIndexRoots(ctx, page.Next, 64)
		if err != nil || len(tail.Rows) != 1 || tail.Next != "" {
			t.Fatalf("tail: %+v %v", tail, err)
		}
		if _, err = s.ScanTypedIndexRoots(ctx, "", 65); err == nil {
			t.Fatal("accepted oversized page")
		}
		if _, err = s.ScanTypedIndexRootChildren(ctx, page.Rows[0].Root, TypedIndexControlKind("repo"), "", 1); err == nil {
			t.Fatal("accepted arbitrary table")
		}
	})
	t.Run("desired race collecting and replay", func(t *testing.T) {
		f := newTypedFixture(t, s, "lifecycle-retire")
		raw := f.enqueue(t, "first")
		root := typedDigest(raw)
		spec, err := s.TypedIndexSchedule(ctx, f.repo)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.EnqueueGenerationSchedule(ctx, spec); err != nil {
			t.Fatal(err)
		}
		before, err := s.InspectTypedIndexRetirement(ctx, root)
		if err != nil || !before.Protected() {
			t.Fatalf("desired: %+v %v", before, err)
		}
		if err = s.BeginTypedIndexRetirement(ctx, before); !errors.Is(err, typedindex.Stale) {
			t.Fatalf("retire desired: %v", err)
		}
		if err = s.CancelTypedIndex(ctx, f.repo, root); err != nil {
			t.Fatal(err)
		}
		selected, err := s.InspectTypedIndexRetirement(ctx, root)
		if err != nil || selected.Protected() {
			t.Fatalf("cancel: %+v %v", selected, err)
		}
		f.enqueue(t, "replacement")
		if err = s.BeginTypedIndexRetirement(ctx, selected); !errors.Is(err, typedindex.Stale) {
			t.Fatalf("changed intent accepted: %v", err)
		}
		selected, err = s.InspectTypedIndexRetirement(ctx, root)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.BeginTypedIndexRetirement(ctx, selected); err != nil {
			t.Fatal(err)
		}
		after, err := s.InspectTypedIndexRetirement(ctx, root)
		if err != nil || !after.Collecting() {
			t.Fatalf("collecting: %+v %v", after, err)
		}
		if err = s.BeginTypedIndexRetirement(ctx, after); err != nil {
			t.Fatalf("idempotent mark: %v", err)
		}
		if _, err = s.EnqueueTypedIndex(ctx, f.repo, raw); !errors.Is(err, typedindex.Stale) {
			t.Fatalf("replay: %v", err)
		}
		if _, err = s.EnqueueGenerationSchedule(ctx, spec); err == nil {
			t.Fatal("schedule collecting root")
		}
		if _, err = s.ExpandGenerationSchedule(ctx, f.repo, spec.Stage, root); err == nil {
			t.Fatal("expanded collecting root")
		}
		page, err := s.ScanTypedIndexRootChildren(ctx, root, TypedIndexRequests, "", 1)
		if err != nil || len(page.Rows) != 1 || page.Rows[0].State != "collecting" {
			t.Fatalf("tombstone: %+v %v", page, err)
		}
	})
	t.Run("running old lease protected then terminal", func(t *testing.T) {
		f := newTypedFixture(t, s, "lifecycle-running")
		raw := f.enqueue(t, "one")
		root := typedDigest(raw)
		chunk := f.claim(t)
		if _, err := s.BeginTypedIndex(ctx, chunk); err != nil {
			t.Fatal(err)
		}
		f.enqueue(t, "two")
		selected, err := s.InspectTypedIndexRetirement(ctx, root)
		if err != nil {
			t.Fatal(err)
		}
		current, desired, running := selected.Protection()
		if current || desired || !running {
			t.Fatalf("protection: %v %v %v", current, desired, running)
		}
		if err = s.BeginTypedIndexRetirement(ctx, selected); err == nil {
			t.Fatal("retired running")
		}
		if err = s.FailGenerationChunk(ctx, chunk, "finished"); err != nil {
			t.Fatal(err)
		}
		selected, err = s.InspectTypedIndexRetirement(ctx, root)
		if err != nil || selected.Protected() {
			t.Fatalf("terminal: %+v %v", selected, err)
		}
		if err = s.BeginTypedIndexRetirement(ctx, selected); err != nil {
			t.Fatal(err)
		}
		if _, err = s.BeginTypedIndex(ctx, chunk); err == nil {
			t.Fatal("began collected root")
		}
		if err = s.HeartbeatGenerationChunk(ctx, chunk); err == nil {
			t.Fatal("heartbeat collected root")
		}
		page, err := s.ScanTypedIndexRootChildren(ctx, root, TypedIndexAttempts, "", 64)
		if err != nil || len(page.Rows) != 1 {
			t.Fatalf("attempt inventory: %+v %v", page, err)
		}
	})
	t.Run("current survives canceled replacement", func(t *testing.T) {
		f := newTypedFixture(t, s, "lifecycle-current")
		raw := f.enqueue(t, "one")
		root := typedDigest(raw)
		chunk := f.claim(t)
		admission, plan := f.seal(t, chunk)
		bundle := f.bundle(t, admission, plan)
		f.advancePublication(t, chunk, bundle)
		if _, err := s.PublishTypedIndex(ctx, chunk, typedindex.PublicationPointer{}, bundle); err != nil {
			t.Fatal(err)
		}
		if err := s.CompleteGenerationChunk(ctx, chunk); err != nil {
			t.Fatal(err)
		}
		replacement := f.enqueue(t, "two")
		if err := s.CancelTypedIndex(ctx, f.repo, typedDigest(replacement)); err != nil {
			t.Fatal(err)
		}
		selected, err := s.InspectTypedIndexRetirement(ctx, root)
		if err != nil {
			t.Fatal(err)
		}
		current, desired, running := selected.Protection()
		if !current || desired || running {
			t.Fatalf("protection: %v %v %v", current, desired, running)
		}
		if err = s.BeginTypedIndexRetirement(ctx, selected); err == nil {
			t.Fatal("retired current")
		}
		plans, err := s.ScanTypedIndexRootChildren(ctx, root, TypedIndexPlans, "", 1)
		if err != nil || len(plans.Rows) != 1 {
			t.Fatalf("plans: %+v %v", plans, err)
		}
		requests, err := s.ScanTypedIndexRootChildren(ctx, root, TypedIndexRequests, "", 2)
		if err != nil || len(requests.Rows) != 2 {
			t.Fatalf("requests: %+v %v", requests, err)
		}
	})
	t.Run("running lease arrives after selection", func(t *testing.T) {
		f := newTypedFixture(t, s, "lifecycle-lease-race")
		raw := f.enqueue(t, "one")
		root := typedDigest(raw)
		spec, err := s.TypedIndexSchedule(ctx, f.repo)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.EnqueueGenerationSchedule(ctx, spec); err != nil {
			t.Fatal(err)
		}
		if _, err = s.ExpandGenerationSchedule(ctx, f.repo, spec.Stage, root); err != nil {
			t.Fatal(err)
		}
		if err = s.CancelTypedIndex(ctx, f.repo, root); err != nil {
			t.Fatal(err)
		}
		selected, err := s.InspectTypedIndexRetirement(ctx, root)
		if err != nil || selected.Protected() {
			t.Fatalf("before claim: %+v %v", selected, err)
		}
		chunk, err := s.ClaimGenerationChunk(ctx, GenerationResourceTypedIndex, "late-claim")
		if err != nil {
			t.Fatal(err)
		}
		if err = s.BeginTypedIndexRetirement(ctx, selected); !errors.Is(err, typedindex.Stale) {
			t.Fatalf("late lease: %v", err)
		}
		if err = s.FailGenerationChunk(ctx, *chunk, "done"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("collecting fences every execution writer", func(t *testing.T) {
		f := newTypedFixture(t, s, "lifecycle-write-fences")
		raw := f.enqueue(t, "one")
		root := typedDigest(raw)
		chunk := f.claim(t)
		admission, plan := f.seal(t, chunk)
		bundle := f.bundle(t, admission, plan)
		f.advancePublication(t, chunk, bundle)
		// Fault-inject the terminal fence beside an old exact lease: every writer
		// must independently refuse. Normal retirement never permits this pairing.
		if err := s.typedWrite(ctx, `UPDATE $rid SET custody_state='collecting' RETURN NONE;`, map[string]any{"rid": typedID("typed_index_request", root)}, 1); err != nil {
			t.Fatal(err)
		}
		checks := []struct {
			name string
			run  func() error
		}{
			{"begin", func() error { _, e := s.BeginTypedIndex(ctx, chunk); return e }},
			{"seal", func() error { _, e := s.SealTypedIndexPlan(ctx, chunk, plan); return e }},
			{"publish", func() error {
				_, e := s.PublishTypedIndex(ctx, chunk, typedindex.PublicationPointer{}, bundle)
				return e
			}},
			{"advance", func() error { return s.AdvanceTypedIndex(ctx, chunk, TypedValidation) }},
			{"typed fail", func() error { return s.FailTypedIndex(ctx, chunk, typedindex.ExecutionFailed) }},
			{"heartbeat", func() error { return s.HeartbeatGenerationChunk(ctx, chunk) }},
			{"complete", func() error { return s.CompleteGenerationChunk(ctx, chunk) }},
			{"fail", func() error { return s.FailGenerationChunk(ctx, chunk, "failed") }},
			{"retry", func() error {
				_, e := s.RetryGenerationChunk(ctx, chunk, "retry", time.Now().Add(time.Minute))
				return e
			}},
			{"release", func() error { return s.ReleaseGenerationChunk(ctx, chunk, "release") }},
			{"defer", func() error { return s.DeferGenerationChunk(ctx, chunk, "defer", time.Minute) }},
		}
		for _, check := range checks {
			if err := check.run(); err == nil {
				t.Fatalf("%s accepted collecting root", check.name)
			}
		}
		before, err := s.generationChunkByIdentity(ctx, chunk.Identity)
		if err != nil {
			t.Fatal(err)
		}
		chunk.Stage = "other"
		chunk.ResourceClass = GenerationResourceCPU
		for _, check := range checks[5:] {
			if err := check.run(); err == nil {
				t.Fatalf("relabeled %s accepted", check.name)
			}
		}
		after, err := s.generationChunkByIdentity(ctx, chunk.Identity)
		if err != nil {
			t.Fatal(err)
		}
		if string(typedTestJSON(t, before)) != string(typedTestJSON(t, after)) {
			t.Fatal("relabeling changed durable lease")
		}

	})
	t.Run("missing and conflicting projections refuse", func(t *testing.T) {
		f := newTypedFixture(t, s, "lifecycle-malformed")
		raw := f.enqueue(t, "one")
		root := typedDigest(raw)
		if err := s.typedWrite(ctx, `UPDATE $rid SET request_root=NONE RETURN NONE;`, map[string]any{"rid": typedID("typed_index_request", root)}, 1); err != nil {
			t.Fatal(err)
		}
		if _, err := s.InspectTypedIndexRetirement(ctx, root); !errors.Is(err, typedindex.Invalid) {
			t.Fatalf("missing projection: %v", err)
		}
		if _, err := s.EnqueueTypedIndex(ctx, f.repo, raw); err == nil {
			t.Fatal("missing projection replay")
		}
		if err := s.typedWrite(ctx, `UPDATE $rid SET request_root=$wrong RETURN NONE;`, map[string]any{"rid": typedID("typed_index_request", root), "wrong": "sha256:" + strings.Repeat("b", 64)}, 1); err != nil {
			t.Fatal(err)
		}
		if _, err := s.InspectTypedIndexRetirement(ctx, root); err == nil {
			t.Fatal("conflicting projection")
		}
		if _, err := s.ScanTypedIndexControls(ctx, TypedIndexControlKind("repo"), "", 1); err == nil {
			t.Fatal("unclosed global table")
		}
		// Restore test-owned row so later complete inventory is not poisoned.
		if err := s.typedWrite(ctx, `UPDATE $rid SET request_root=$root RETURN NONE;`, map[string]any{"rid": typedID("typed_index_request", root), "root": root}, 1); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("global attempt census detects missing projection", func(t *testing.T) {
		f := newTypedFixture(t, s, "lifecycle-attempt-projection")
		f.enqueue(t, "one")
		chunk := f.claim(t)
		work, err := s.BeginTypedIndex(ctx, chunk)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.typedWrite(ctx, `UPDATE $rid SET request_root=NONE RETURN NONE;`, map[string]any{"rid": typedID("typed_index_attempt", work.AttemptDigest)}, 1); err != nil {
			t.Fatal(err)
		}
		if _, err = s.ScanTypedIndexControls(ctx, TypedIndexAttempts, "", 64); !errors.Is(err, typedindex.Invalid) {
			t.Fatalf("missing attempt projection hidden: %v", err)
		}
		if _, err = s.BeginTypedIndex(ctx, chunk); !errors.Is(err, typedindex.Invalid) {
			t.Fatalf("resumed old projection: %v", err)
		}
	})

	t.Run("physical index bounds", func(t *testing.T) {
		for _, kind := range []TypedIndexControlKind{TypedIndexRequests, TypedIndexAttempts, TypedIndexPlans} {
			requireRetentionExplain(t, ctx, s, "SELECT "+typedControlProjection+" FROM "+string(kind)+" WHERE request_root=$root AND control_key>$after ORDER BY control_key LIMIT $scan_limit EXPLAIN FULL", map[string]any{"root": "sha256:" + strings.Repeat("a", 64), "after": "", "scan_limit": 65}, "IndexScan", string(kind)+"_root")
		}
		requireRetentionExplain(t, ctx, s, "SELECT id FROM generation_schedule_chunk WHERE generation=$root AND stage='typed-index' AND status='running' LIMIT $scan_limit EXPLAIN FULL", map[string]any{"root": "sha256:" + strings.Repeat("a", 64), "scan_limit": 1}, "IndexScan", "generation_chunk_typed_root")
	})

}

func TestTypedIndexChunkScopeLimits(t *testing.T) {
	const statement = "UPDATE $chunk SET heartbeat_at=time::now() WHERE status='running' AND lease_token = $lease AND claimed_by = $worker RETURN AFTER"
	for _, tc := range []struct {
		name, stage string
		class       GenerationResourceClass
		valid       bool
	}{
		{"ordinary", "other", GenerationResourceCPU, true},
		{"stage bound", strings.Repeat("a", MaxGenerationStageBytes), GenerationResourceCPU, true},
		{"stage overflow", strings.Repeat("a", MaxGenerationStageBytes+1), GenerationResourceCPU, false},
		{"stage syntax", "other' OR true", GenerationResourceCPU, false},
		{"unknown class", "other", GenerationResourceClass("cpu' OR true"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actual := typedChunkStatement(statement, GenerationChunk{Stage: tc.stage, ResourceClass: tc.class})
			if tc.valid {
				if !strings.Contains(actual, "AND stage=$lease_scope.stage") || !strings.Contains(actual, "AND resource_class=$lease_scope.resource_class") {
					t.Fatal("lease scope was not bound")
				}
			} else if strings.Contains(actual, "UPDATE") || len(actual) > 128 {
				t.Fatal("invalid scope retained executable mutation or unbounded input")
			}
		})
	}
}

func TestTypedIndexRetirementScheduleProgress(t *testing.T) {
	s := newRunnerStore(t)
	ctx := t.Context()
	old := newTypedFixture(t, s, "retire-schedule-old")
	root := typedDigest(old.enqueue(t, "one"))
	spec, err := s.TypedIndexSchedule(ctx, old.repo)
	if err != nil {
		t.Fatal(err)
	}
	scheduled, err := s.EnqueueGenerationSchedule(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CancelTypedIndex(ctx, old.repo, root); err != nil {
		t.Fatal(err)
	}
	selection, err := s.InspectTypedIndexRetirement(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.BeginTypedIndexRetirement(ctx, selection); err != nil {
		t.Fatal(err)
	}
	retired, err := s.generationScheduleByDigest(ctx, scheduled.Digest)
	if err != nil || retired.Status != GenerationScheduleSuperseded {
		t.Fatalf("retired: %+v %v", retired, err)
	}
	if _, err = s.GetGenerationSchedule(ctx, old.repo, TypedIndexScheduleStage); !errors.Is(err, ErrNotFound) {
		t.Fatalf("current survived: %v", err)
	}
	// An already expanded pending schedule is retired without walking chunks or
	// touching repository counters; it must likewise leave the claim candidate set.
	pending := newTypedFixture(t, s, "retire-schedule-pending")
	pendingRoot := typedDigest(pending.enqueue(t, "one"))
	pendingSpec, err := s.TypedIndexSchedule(ctx, pending.repo)
	if err != nil {
		t.Fatal(err)
	}
	pendingSchedule, err := s.EnqueueGenerationSchedule(ctx, pendingSpec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ExpandGenerationSchedule(ctx, pending.repo, TypedIndexScheduleStage, pendingRoot); err != nil {
		t.Fatal(err)
	}
	if err = s.CancelTypedIndex(ctx, pending.repo, pendingRoot); err != nil {
		t.Fatal(err)
	}
	pendingSelection, err := s.InspectTypedIndexRetirement(ctx, pendingRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.BeginTypedIndexRetirement(ctx, pendingSelection); err != nil {
		t.Fatal(err)
	}
	pendingSchedule, err = s.generationScheduleByDigest(ctx, pendingSchedule.Digest)
	if err != nil || pendingSchedule.Status != GenerationScheduleSuperseded || pendingSchedule.Pending != 1 || pendingSchedule.Running != 0 {
		t.Fatalf("pending retirement: %+v %v", pendingSchedule, err)
	}
	// The oldest unexpanded retired schedule must not block another repository.
	next := newTypedFixture(t, s, "retire-schedule-next")
	next.enqueue(t, "one")
	nextSpec, err := s.TypedIndexSchedule(ctx, next.repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.EnqueueGenerationSchedule(ctx, nextSpec); err != nil {
		t.Fatal(err)
	}
	expanded, err := s.ExpandNextGenerationSchedule(ctx, GenerationResourceTypedIndex)
	if err != nil || expanded.Repository != next.repo {
		t.Fatalf("next expansion: %+v %v", expanded, err)
	}
	claimed, err := s.ClaimGenerationChunk(ctx, GenerationResourceTypedIndex, "next-worker")
	if err != nil || claimed.Repository != next.repo {
		t.Fatalf("next claim: %+v %v", claimed, err)
	}
	if err = s.FailGenerationChunk(ctx, *claimed, "done"); err != nil {
		t.Fatal(err)
	}
	// Re-marking the old tombstone must preserve its repository's replacement.
	old.enqueue(t, "replacement")
	replacementSpec, err := s.TypedIndexSchedule(ctx, old.repo)
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := s.EnqueueGenerationSchedule(ctx, replacementSpec)
	if err != nil {
		t.Fatal(err)
	}
	selection, err = s.InspectTypedIndexRetirement(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.BeginTypedIndexRetirement(ctx, selection); err != nil {
		t.Fatal(err)
	}
	current, err := s.GetGenerationSchedule(ctx, old.repo, TypedIndexScheduleStage)
	if err != nil || current.Digest != replacement.Digest {
		t.Fatalf("replacement current lost: %+v %v", current, err)
	}
	expanded, err = s.ExpandNextGenerationSchedule(ctx, GenerationResourceTypedIndex)
	if err != nil || expanded.Digest != replacement.Digest {
		t.Fatalf("replacement expansion: %+v %v", expanded, err)
	}
	claimed, err = s.ClaimGenerationChunk(ctx, GenerationResourceTypedIndex, "replacement-worker")
	if err != nil || claimed.ScheduleDigest != replacement.Digest {
		t.Fatalf("replacement claim: %+v %v", claimed, err)
	}
}

func TestTypedIndexWorkerDescriptorBinding(t *testing.T) {
	s := newRunnerStore(t)
	ctx := t.Context()
	f := newTypedFixture(t, s, "descriptor-source")
	f.enqueue(t, "one")
	original := f.claim(t)
	other := newTypedFixture(t, s, "descriptor-other")
	other.enqueue(t, "one")
	otherChunk := other.claim(t)
	snapshot := func() string {
		t.Helper()
		var all []any
		for _, chunk := range []GenerationChunk{original, otherChunk} {
			durable, err := s.generationChunkByIdentity(ctx, chunk.Identity)
			if err != nil {
				t.Fatal(err)
			}
			schedule, err := s.generationScheduleByDigest(ctx, chunk.ScheduleDigest)
			if err != nil {
				t.Fatal(err)
			}
			all = append(all, durable, schedule)
		}
		return string(typedTestJSON(t, all))
	}
	before := snapshot()
	cases := []struct {
		name   string
		mutate func(*GenerationChunk)
	}{
		{"identity", func(c *GenerationChunk) { c.Identity = otherChunk.Identity }},
		{"schedule", func(c *GenerationChunk) { c.ScheduleDigest = otherChunk.ScheduleDigest }},
		{"repository", func(c *GenerationChunk) { c.Repository = otherChunk.Repository }},
		{"stage", func(c *GenerationChunk) { c.Stage = "other" }},
		{"class", func(c *GenerationChunk) { c.ResourceClass = GenerationResourceCPU }},
		{"generation", func(c *GenerationChunk) { c.Generation = otherChunk.Generation }},
		{"offset", func(c *GenerationChunk) { c.Offset++ }},
		{"length", func(c *GenerationChunk) { c.Length++ }},
		{"attempt", func(c *GenerationChunk) { c.Attempt++ }},
		{"labels", func(c *GenerationChunk) { c.Stage = "other"; c.ResourceClass = GenerationResourceCPU }},
	}
	// High-level Begin derives its attempt identity from this descriptor too.
	// Every forged descriptor must leave both the attempt table and state empty.
	for _, tc := range cases {
		forged := original
		tc.mutate(&forged)
		if _, err := s.BeginTypedIndex(ctx, forged); err == nil {
			t.Fatalf("Begin accepted forged %s", tc.name)
		}
	}
	page, err := s.ScanTypedIndexRootChildren(ctx, original.Generation, TypedIndexAttempts, "", 64)
	if err != nil || len(page.Rows) != 0 {
		t.Fatalf("forgery created attempts: %+v %v", page, err)
	}
	state, err := s.typedRead(ctx, "typed_index_state", original.Repository)
	if err != nil || state != "" {
		t.Fatalf("forgery changed state: %q %v", state, err)
	}
	if _, err = s.BeginTypedIndex(ctx, original); err != nil {
		t.Fatal(err)
	}
	page, err = s.ScanTypedIndexRootChildren(ctx, original.Generation, TypedIndexAttempts, "", 64)
	if err != nil || len(page.Rows) != 1 {
		t.Fatalf("legitimate Begin: %+v %v", page, err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := original
			tc.mutate(&c)
			checks := []struct {
				name string
				run  func() error
			}{
				{"heartbeat", func() error { return s.HeartbeatGenerationChunk(ctx, c) }},
				{"complete", func() error { return s.CompleteGenerationChunk(ctx, c) }},
				{"fail", func() error { return s.FailGenerationChunk(ctx, c, "failed") }},
				{"retry", func() error { _, e := s.RetryGenerationChunk(ctx, c, "retry", time.Now().Add(time.Minute)); return e }},
				{"release", func() error { return s.ReleaseGenerationChunk(ctx, c, "release") }},
				{"defer", func() error { return s.DeferGenerationChunk(ctx, c, "defer", time.Minute) }},
			}
			for _, check := range checks {
				if err := check.run(); err == nil {
					t.Fatalf("%s accepted forged descriptor", check.name)
				}
			}
			if after := snapshot(); after != before {
				t.Fatal("forgery mutated source or unrelated durable state")
			}
		})
	}
	// A correctly bound worker still settles normally after every refusal.
	if err := s.FailGenerationChunk(ctx, original, "done"); err != nil {
		t.Fatal(err)
	}
	if err := s.FailGenerationChunk(ctx, otherChunk, "done"); err != nil {
		t.Fatal(err)
	}
}
