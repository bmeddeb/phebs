package store

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"

	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
)

func TestServiceStateV3PayloadHeadroom(t *testing.T) {
	for _, test := range []struct {
		name       string
		phase      string
		changed    int
		wantWrites []int
	}{
		{"reconcile_noop", serviceStateV3Reconcile, 0, []int{0}},
		{"reconcile_511", serviceStateV3Reconcile, 511, []int{511}},
		{"reconcile_512", serviceStateV3Reconcile, 512, []int{511, 1}},
		{"activation_noop", serviceStateV3Activate, 0, []int{0}},
		{"activation_510", serviceStateV3Activate, 510, []int{510}},
		{"activation_511", serviceStateV3Activate, 511, []int{510, 1}},
		{"activation_512", serviceStateV3Activate, 512, []int{510, 2}},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan, changes, summary := serviceStateV3HeadroomFixture(t, test.phase, test.changed)
			originalPlan := plan
			remaining := changes
			written := 0
			for index, wantWrites := range test.wantWrites {
				write, err := nextServiceStateV3ChunkWrite(plan, remaining, 512, "last-key", summary)
				if err != nil {
					t.Fatal(err)
				}
				written += wantWrites
				payload := len(write.updates) + 1
				if write.summary != nil {
					payload++
				}
				final := index == len(test.wantWrites)-1
				wantNext, wantRead, wantCursor := originalPlan.NextChunk, int64(0), originalPlan.RemovalCursor
				if final {
					wantNext++
					wantRead, wantCursor = 512, "last-key"
				}
				if len(write.updates) != wantWrites || payload > 512 ||
					write.plan.NextChunk != wantNext || write.plan.RowsRead != wantRead ||
					write.plan.RowsWritten != int64(written) || write.plan.RemovalCursor != wantCursor ||
					write.plan.MaxChunkRows != int(wantRead) || validateServiceStateV3Plan(write.plan) != nil {
					t.Fatalf("prefix %d: payload=%d, plan=%+v, writes=%d", index, payload, write.plan, len(write.updates))
				}
				if test.phase == serviceStateV3Activate {
					if write.plan.CurrentCount != written || write.plan.UnavailableCount != test.changed-written {
						t.Fatalf("prefix contains final activation totals: %+v", write.plan)
					}
					if wantWrites == 0 {
						if write.summary != nil || write.plan.SummaryDigest != summary.SummaryDigest {
							t.Fatal("no-op rewrote the summary")
						}
					} else if write.summary == nil || write.summary.CurrentCount != written ||
						write.summary.ControlRevision != summary.ControlRevision+1 ||
						write.summary.SummaryDigest == summary.SummaryDigest ||
						write.plan.SummaryDigest != write.summary.SummaryDigest {
						t.Fatalf("prefix summary = %+v", write.summary)
					}
				} else if write.plan.LiveServiceCount != written || write.plan.UnavailableCount != written {
					t.Fatalf("prefix contains final reconcile totals: %+v", write.plan)
				}
				if write.plan.Digest != originalPlan.Digest ||
					write.plan.ScheduleDigest != originalPlan.ScheduleDigest ||
					write.plan.ServiceMemberChunks != originalPlan.ServiceMemberChunks {
					t.Fatal("prefix changed immutable member/plan identity")
				}
				remaining = remaining[len(write.updates):]
				plan = write.plan
				if write.summary != nil {
					summary = write.summary
				}
			}
			if len(remaining) != 0 || (test.changed != 0 && plan.BytesWritten == 0) {
				t.Fatalf("uncommitted changes or missing written bytes: remaining=%d, plan=%+v", len(remaining), plan)
			}
		})
	}
}

func TestServiceStateV3PayloadGuardAndTarget(t *testing.T) {
	for _, phase := range []string{serviceStateV3Reconcile, serviceStateV3Activate} {
		t.Run(phase, func(t *testing.T) {
			plan, changes, summary := serviceStateV3HeadroomFixture(t, phase, 512)
			write, err := nextServiceStateV3ChunkWrite(plan, changes, 512, "", summary)
			if err != nil {
				t.Fatal(err)
			}
			// The shared commit must refuse one extra payload record before it
			// attempts selector preflight, even if a caller bypasses the planner.
			oversized := append(slices.Clone(write.updates), changes[len(write.updates)].update)
			var absent *Surreal
			if err := absent.commitServiceStateV3Chunk(t.Context(), GenerationChunk{},
				plan, write.plan, oversized, summary, write.summary, false,
			); !errors.Is(err, ErrInvalidServiceStateV3) {
				t.Fatalf("oversized write reached the store: %v", err)
			}
		})
	}
	plan, changes, summary := serviceStateV3HeadroomFixture(t, serviceStateV3Activate, 1)
	plan.NextChunk = 9
	plan.ServiceMemberChunks = 10
	plan.TotalChunks = 11
	write, err := nextServiceStateV3ChunkWrite(plan, changes, 512, "", summary)
	if err != nil || len(write.updates) != 1 || write.summary == nil ||
		write.plan.NextChunk != 10 || write.plan.RowsRead != 512 || write.plan.RowsWritten != 1 {
		t.Fatalf("logical member9 must remain one three-record write: %+v, %v", write, err)
	}
}

func serviceStateV3HeadroomFixture(
	t *testing.T, phase string, count int,
) (ServiceStateV3Plan, []serviceStateV3Change, *servicecatalog.RepositoryState) {
	t.Helper()
	repository := "example.com/acme/headroom"
	services := serviceStateV3HeadroomServices(count)
	// A catalog requires a service even for a pure zero-change plan test.
	if len(services) == 0 {
		services = serviceStateV3HeadroomServices(1)
	}
	generation := serviceStateV3Generation(t, repository, strings.Repeat("7", 40), "headroom", services)
	var raw []byte
	for _, member := range generation.Members {
		if member.Kind == generation.Root.ServiceMembers[0].Kind && member.Ordinal == 0 {
			raw = member.Content
		}
	}
	projections, err := servicecatalogv3.ProjectServiceMember(generation.Root, generation.Root.ServiceMembers[0], raw)
	if err != nil {
		t.Fatal(err)
	}
	now := storeTimestamp(time.Now().Add(-time.Minute))
	plan := ServiceStateV3Plan{
		Schema: serviceStateV3PlanSchema, Repository: repository, Phase: phase,
		CatalogRoot: generation.Root.Digest, CatalogControlRevision: 1,
		ScheduleDigest: selectorTestDigest("a"), State: serviceStateV3Running,
		TotalChunks: 2, ServiceMemberChunks: 1, CatalogServiceCount: count,
		CreatedAt: now, UpdatedAt: now,
	}
	var summary *servicecatalog.RepositoryState
	if phase == serviceStateV3Activate {
		plan.SearchGeneration = selectorTestDigest("b")
		plan.LiveServiceCount, plan.UnavailableCount = count, count
		summary = &servicecatalog.RepositoryState{
			Schema: servicecatalogv3.RepositoryStateSchema, Repository: repository,
			CatalogGeneration: plan.CatalogRoot, CatalogControlRevision: 1,
			CatalogServiceCount: count, LiveServiceCount: count, UnavailableCount: count,
			ControlRevision: 1, UpdatedAt: now,
		}
		if err := servicecatalogv3.SetRepositoryStateDigest(summary); err != nil {
			t.Fatal(err)
		}
		plan.SummaryControlRevision, plan.SummaryDigest = summary.ControlRevision, summary.SummaryDigest
	}
	plan.Digest = serviceStateV3PlanDigest(repository, phase, plan.CatalogRoot, 1, plan.SearchGeneration, 0)
	changes := make([]serviceStateV3Change, 0, count)
	for _, projection := range projections[:count] {
		state, changed, err := projectServiceStateV3(projection, servicecatalog.ServiceState{}, false, now)
		if err != nil || !changed {
			t.Fatalf("project fixture: changed=%v, err=%v", changed, err)
		}
		change := serviceStateV3Change{update: serviceStateUpdate{State: state}}
		if phase == serviceStateV3Activate {
			change = serviceStateV3HeadroomActivation(t, state, plan.SearchGeneration)
		}
		changes = append(changes, change)
	}
	if err := validateServiceStateV3Plan(plan); err != nil {
		t.Fatal(err)
	}
	return plan, changes, summary
}

func TestServiceStateV3PayloadPrevalidatesEveryPrefix(t *testing.T) {
	for _, phase := range []string{serviceStateV3Reconcile, serviceStateV3Activate} {
		t.Run(phase, func(t *testing.T) {
			plan, changes, summary := serviceStateV3HeadroomFixture(t, phase, 512)
			changes[511].update.State.StateDigest = selectorTestDigest("f")
			var absent *Surreal
			if err := absent.commitServiceStateV3Changes(t.Context(), GenerationChunk{},
				plan, changes, 512, "", summary,
			); !errors.Is(err, ErrInvalidServiceStateV3) {
				t.Fatalf("invalid last state reached first store write: %v", err)
			}
		})
	}
	plan, changes, summary := serviceStateV3HeadroomFixture(t, serviceStateV3Activate, 512)
	summary.ControlRevision = math.MaxInt64 - 1
	if err := servicecatalogv3.SetRepositoryStateDigest(summary); err != nil {
		t.Fatal(err)
	}
	plan.SummaryControlRevision, plan.SummaryDigest = summary.ControlRevision, summary.SummaryDigest
	var absent *Surreal
	if err := absent.commitServiceStateV3Changes(t.Context(), GenerationChunk{},
		plan, changes, 512, "", summary,
	); !errors.Is(err, ErrInvalidServiceStateV3) {
		t.Fatalf("second-prefix summary ceiling reached first store write: %v", err)
	}
}

func TestServiceStateV3PayloadPrefixRestartAndRemovalRefill(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx := t.Context()
	dataDir := t.TempDir()
	s, err := OpenLocal(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if s != nil {
			_ = s.Close(context.Background())
		}
	})
	repository, commit := "example.com/acme/payload-prefix-replay", strings.Repeat("7", 40)
	seedServiceCatalogV3Repo(t, s, repository, commit)
	services := serviceStateV3HeadroomServices(512)
	generation := serviceStateV3Generation(t, repository, commit, "a", services)
	if err := s.PublishServiceCatalogV3Candidate(ctx, generation); err != nil {
		t.Fatal(err)
	}
	begin, err := s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	expandServiceStateV3Plan(t, s, begin)
	first := serviceStateV3HeadroomClaim(t, s, *begin.Plan, 0)
	changes, read, cursor, summary := serviceStateV3HeadroomChanges(t, s, *begin.Plan)
	prefix := serviceStateV3HeadroomCommitPrefix(t, s, first, *begin.Plan, changes, read, cursor, summary)
	if len(prefix.updates) != 511 || prefix.plan.RowsWritten != 511 ||
		prefix.plan.NextChunk != 0 || prefix.plan.RowsRead != 0 || prefix.plan.LiveServiceCount != 511 {
		t.Fatalf("first durable reconcile prefix = %+v", prefix.plan)
	}
	before, err := s.getServiceStateV3Plan(ctx, begin.Plan.Digest)
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.commitServiceStateV3Changes(canceled, first, prefix.plan, changes[511:], read, cursor, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled continuation = %v", err)
	}
	// The stale plan has current counts and ordinal but pre-prefix work counters.
	// In particular a zero-update writer cannot roll back durable prefix progress.
	stale := prefix.plan
	stale.RowsWritten, stale.BytesWritten = 0, 0
	staleWrite, err := nextServiceStateV3ChunkWrite(stale, nil, 512, cursor, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.commitServiceStateV3Chunk(ctx, first, stale, staleWrite.plan, nil, nil, nil, false); err == nil {
		t.Fatal("stale same-ordinal zero-update plan overwrote prefix counters")
	}
	serviceStateV3HeadroomUnchanged(t, s, before)

	reopen := func() {
		t.Helper()
		if err := s.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		s = nil
		s, err = OpenLocal(ctx, dataDir)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.RepairServiceCatalogV3Startup(ctx); err != nil {
			t.Fatalf("repair durable prefix: %v", err)
		}
	}
	reopen()
	serviceStateV3HeadroomReap(t, s, first)
	retry := serviceStateV3HeadroomClaim(t, s, prefix.plan, 0)
	if retry.ID != first.ID || retry.LeaseToken == first.LeaseToken {
		t.Fatal("prefix replay did not acquire a new lease for the same chunk")
	}
	result, err := s.ProcessServiceStateV3Chunk(ctx, retry)
	if err != nil || result.Read != 512 || result.Applied != 1 {
		t.Fatalf("reconcile retry = %+v, %v", result, err)
	}
	if err := s.CompleteGenerationChunk(ctx, retry); err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, s, begin)
	search := selectorTestDigest("8")
	activation, err := s.BeginServiceStateV3Activation(ctx, repository, search)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, s, activation)
	selected := serviceStateV3HeadroomSelect(t, s, repository, search)
	oldFirst := serviceStateV3Row(t, s, repository, services[0].Key)
	oldLast := serviceStateV3Row(t, s, repository, services[511].Key)
	assertSelected := func() {
		t.Helper()
		for _, old := range []servicecatalog.ServiceState{oldFirst, oldLast} {
			row, readErr := s.GetServiceStateV3PointSnapshot(ctx, repository, old.ServiceKey,
				selected.StateControlRevision, selected.StateSummaryDigest)
			if readErr != nil || row.StateDigest != old.StateDigest {
				t.Fatalf("selected preimage changed: %+v, %v", row, readErr)
			}
		}
		got, readErr := s.GetServiceStateV3SummarySnapshot(ctx, repository,
			selected.StateControlRevision, selected.StateSummaryDigest)
		if readErr != nil || got.SummaryDigest != selected.StateSummaryDigest {
			t.Fatalf("selected summary changed: %+v, %v", got, readErr)
		}
	}
	for index := range services {
		services[index].DisplayName += " B"
	}
	generation = serviceStateV3Generation(t, repository, commit, "b", services)
	if err := s.PublishServiceCatalogV3Candidate(ctx, generation); err != nil {
		t.Fatal(err)
	}
	begin, err = s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, s, begin)
	assertSelected()
	activation, err = s.BeginServiceStateV3Activation(ctx, repository, search)
	if err != nil {
		t.Fatal(err)
	}
	expandServiceStateV3Plan(t, s, activation)
	first = serviceStateV3HeadroomClaim(t, s, *activation.Plan, 0)
	changes, read, cursor, summary = serviceStateV3HeadroomChanges(t, s, *activation.Plan)
	prefix = serviceStateV3HeadroomCommitPrefix(t, s, first, *activation.Plan, changes, read, cursor, summary)
	if len(prefix.updates) != 510 || prefix.summary == nil || prefix.summary.CurrentCount != 510 ||
		prefix.summary.StaleCount != 2 || prefix.plan.NextChunk != 0 ||
		prefix.summary.ControlRevision != summary.ControlRevision+1 {
		t.Fatalf("first durable activation prefix = %+v, summary=%+v", prefix.plan, prefix.summary)
	}
	assertSelected()
	if err := s.ReleaseGenerationChunk(ctx, first, "prefix lease interruption"); err != nil {
		t.Fatal(err)
	}
	if err := s.commitServiceStateV3Changes(ctx, first, prefix.plan, changes[510:], read, cursor, prefix.summary); err == nil {
		t.Fatal("released worker committed the second activation prefix")
	}
	retry = serviceStateV3HeadroomClaim(t, s, prefix.plan, 0)
	result, err = s.ProcessServiceStateV3Chunk(ctx, retry)
	if err != nil || result.Read != 512 || result.Applied != 2 {
		t.Fatalf("activation retry = %+v, %v", result, err)
	}
	if err := s.CompleteGenerationChunk(ctx, retry); err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, s, activation)
	assertSelected()

	// All 512 old keys precede the new key, so the first removal page contains
	// only removals. After 511 durable removals, retry refills with that last old
	// key and the still-present new key; the final cursor must include both.
	remaining := serviceStateV3HeadroomServices(1)
	remaining[0].Key = "zz-remaining"
	generation = serviceStateV3Generation(t, repository, commit, "c", remaining)
	if err := s.PublishServiceCatalogV3Candidate(ctx, generation); err != nil {
		t.Fatal(err)
	}
	begin, err = s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	expandServiceStateV3Plan(t, s, begin)
	member := serviceStateV3HeadroomClaim(t, s, *begin.Plan, 0)
	if _, err := s.ProcessServiceStateV3Chunk(ctx, member); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteGenerationChunk(ctx, member); err != nil {
		t.Fatal(err)
	}
	removalPlan, err := s.getServiceStateV3Plan(ctx, begin.Plan.Digest)
	if err != nil {
		t.Fatal(err)
	}
	first = serviceStateV3HeadroomClaim(t, s, *removalPlan, 1)
	changes, read, cursor, summary = serviceStateV3HeadroomChanges(t, s, *removalPlan)
	if read != 512 || len(changes) != 512 {
		t.Fatalf("initial removal page read=%d changes=%d", read, len(changes))
	}
	prefix = serviceStateV3HeadroomCommitPrefix(t, s, first, *removalPlan, changes, read, cursor, summary)
	if len(prefix.updates) != 511 || prefix.plan.RemovalCursor != removalPlan.RemovalCursor ||
		prefix.plan.TombstoneCount != 511 || prefix.plan.NextChunk != 1 {
		t.Fatalf("first removal prefix = %+v", prefix.plan)
	}
	reopen()
	assertSelected()
	serviceStateV3HeadroomReap(t, s, first)
	retry = serviceStateV3HeadroomClaim(t, s, prefix.plan, 1)
	result, err = s.ProcessServiceStateV3Chunk(ctx, retry)
	if err != nil || result.Read != 2 || result.Applied != 1 {
		t.Fatalf("refilled removal retry = %+v, %v", result, err)
	}
	stored, err := s.getServiceStateV3Plan(ctx, begin.Plan.Digest)
	if err != nil || stored.RemovalCursor != "zz-remaining" || stored.NextChunk != 2 ||
		stored.TombstoneCount != 512 || stored.LiveServiceCount != 1 || stored.RowsWritten != 513 {
		t.Fatalf("refilled removal plan = %+v, %v", stored, err)
	}
	if err := s.CompleteGenerationChunk(ctx, retry); err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, s, begin)
	assertSelected()
	finalSummary, err := s.GetServiceStateV3Summary(ctx, repository)
	if err != nil || finalSummary.LiveServiceCount != 1 || finalSummary.TombstoneCount != 512 {
		t.Fatalf("final removal summary = %+v, %v", finalSummary, err)
	}
}

func serviceStateV3HeadroomChanges(
	t *testing.T, s *Surreal, plan ServiceStateV3Plan,
) ([]serviceStateV3Change, int, string, *servicecatalog.RepositoryState) {
	t.Helper()
	ctx := t.Context()
	opened, err := s.GetServiceCatalogV3CandidateRoot(ctx, plan.Repository)
	if err != nil {
		t.Fatal(err)
	}
	var changes []serviceStateV3Change
	if plan.NextChunk >= plan.ServiceMemberChunks {
		rows, err := s.nextServiceStateV3RemovalRows(ctx, plan)
		if err != nil {
			t.Fatal(err)
		}
		present, err := s.serviceStateV3PresentKeys(ctx, opened.Root, rows)
		if err != nil {
			t.Fatal(err)
		}
		cursor := plan.RemovalCursor
		for _, prior := range rows {
			cursor = prior.ServiceKey
			if present[prior.ServiceKey] {
				continue
			}
			next, err := implicitRemovedServiceStateV3(prior, storeTimestamp(time.Now()))
			if err != nil {
				t.Fatal(err)
			}
			changes = append(changes, serviceStateV3Change{
				update: serviceStateUpdate{State: next, ExpectedRevision: prior.ControlRevision, ExpectedDigest: prior.StateDigest},
				prior:  prior, existed: true,
			})
		}
		return changes, len(rows), cursor, nil
	}
	descriptor := opened.Root.ServiceMembers[plan.NextChunk]
	raw, err := s.serviceCatalogV3MemberContent(ctx, descriptor)
	if err != nil {
		t.Fatal(err)
	}
	projections, err := servicecatalogv3.ProjectServiceMember(opened.Root, descriptor, raw)
	if err != nil {
		t.Fatal(err)
	}
	existing, err := s.serviceStateV3RowsForProjections(ctx, plan.Repository, projections)
	if err != nil {
		t.Fatal(err)
	}
	for _, projection := range projections {
		prior, existed := existing[projection.Service.Key]
		if plan.Phase == serviceStateV3Activate {
			changes = append(changes, serviceStateV3HeadroomActivation(t, prior, plan.SearchGeneration))
			continue
		}
		next, changed, err := projectServiceStateV3(projection, prior, existed, storeTimestamp(time.Now()))
		if err != nil || !changed {
			t.Fatalf("fixture needs changed projection: changed=%v err=%v", changed, err)
		}
		changes = append(changes, serviceStateV3Change{
			update: serviceStateUpdate{State: next, ExpectedRevision: prior.ControlRevision, ExpectedDigest: prior.StateDigest},
			prior:  prior, existed: existed,
		})
	}
	var summary *servicecatalog.RepositoryState
	if plan.Phase == serviceStateV3Activate {
		summary, err = s.getRawServiceStateV3Summary(ctx, plan.Repository)
		if err != nil {
			t.Fatal(err)
		}
	}
	return changes, len(projections), plan.RemovalCursor, summary
}

func serviceStateV3HeadroomCommitPrefix(
	t *testing.T, s *Surreal, chunk GenerationChunk, plan ServiceStateV3Plan,
	changes []serviceStateV3Change, read int, cursor string, summary *servicecatalog.RepositoryState,
) serviceStateV3ChunkWrite {
	t.Helper()
	write, err := nextServiceStateV3ChunkWrite(plan, changes, read, cursor, summary)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.commitServiceStateV3Chunk(t.Context(), chunk, plan, write.plan,
		write.updates, summary, write.summary, false); err != nil {
		t.Fatal(err)
	}
	return write
}

func serviceStateV3HeadroomClaim(t *testing.T, s *Surreal, plan ServiceStateV3Plan, offset int64) GenerationChunk {
	t.Helper()
	ctx := t.Context()
	for range plan.TotalChunks {
		chunk, err := s.ClaimGenerationChunk(ctx, GenerationResourceCPU, "payload-prefix")
		if err != nil || chunk == nil || chunk.Generation != plan.Digest || chunk.Offset < offset {
			t.Fatalf("claim expected offset%d = %+v, %v", offset, chunk, err)
		}
		if chunk.Offset == offset {
			return *chunk
		}
		// Reaped/released chunks have priority2; untouched future chunks have
		// priority0. Return their genuine leases after the ordinary ordinal
		// refusal, without mutating scheduler priority or completing any work.
		before, err := s.getServiceStateV3Plan(ctx, plan.Digest)
		if err != nil {
			t.Fatal(err)
		}
		if result, err := s.ProcessServiceStateV3Chunk(ctx, *chunk); !errors.Is(err, ErrConflict) {
			t.Fatalf("future chunk crossed incomplete prefix: result=%+v err=%v", result, err)
		}
		serviceStateV3HeadroomUnchanged(t, s, before)
		if err := s.ReleaseGenerationChunk(ctx, *chunk, "waiting for incomplete prefix"); err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("incomplete prefix did not regain its bounded scheduler turn")
	return GenerationChunk{}
}

func serviceStateV3HeadroomUnchanged(t *testing.T, s *Surreal, before *ServiceStateV3Plan) {
	t.Helper()
	after, err := s.getServiceStateV3Plan(t.Context(), before.Digest)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("refused continuation changed plan: before=%+v after=%+v err=%v", before, after, err)
	}
}

func serviceStateV3HeadroomReap(t *testing.T, s *Surreal, chunk GenerationChunk) {
	t.Helper()
	ctx := t.Context()
	if _, err := surrealdb.Query[any](ctx, s.db,
		"UPDATE $chunk SET heartbeat_at = $old RETURN NONE", map[string]any{
			"chunk": generationChunkRecordID(chunk), "old": storeTimestamp(time.Now().Add(-time.Hour)),
		}); err != nil {
		t.Fatal(err)
	}
	if count, err := s.ReapStaleGenerationChunks(ctx, GenerationResourceCPU, time.Minute); err != nil || count != 1 {
		t.Fatalf("reap first-prefix lease = %d, %v", count, err)
	}
	if result, err := s.ProcessServiceStateV3Chunk(ctx, chunk); err == nil {
		t.Fatalf("old first-prefix lease continued: %+v", result)
	}
}

func serviceStateV3HeadroomSelect(t *testing.T, s *Surreal, repository, search string) ServiceRuntimeSelector {
	t.Helper()
	ctx := t.Context()
	pointer, err := s.GetServiceCatalogV3CandidatePointer(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := s.GetServiceStateV3SummaryPoint(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	reference := ServiceCatalogV3RelationshipReference{
		Repository: repository, RelationshipGenerationDigest: selectorTestDigest("9"),
		RelationshipRootDigest: selectorTestDigest("a"), CatalogRootDigest: pointer.RootDigest,
		CatalogControlRevision: pointer.ControlRevision, StateControlRevision: summary.ControlRevision,
		StateSummaryDigest: summary.SummaryDigest,
	}
	if err := s.PinServiceCatalogV3RelationshipReference(ctx, reference); err != nil {
		t.Fatal(err)
	}
	selected, err := s.SelectServiceRuntimeV3(ctx, ServiceRuntimeSelectionRequest{
		Repository: repository,
		Target: ServiceRuntimeTarget{
			CatalogRootDigest: pointer.RootDigest, CatalogControlRevision: pointer.ControlRevision,
			StateControlRevision: summary.ControlRevision, StateSummaryDigest: summary.SummaryDigest,
			SearchGenerationDigest: search, RelationshipGenerationDigest: reference.RelationshipGenerationDigest,
			RelationshipRootDigest: reference.RelationshipRootDigest,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return selected
}

func serviceStateV3HeadroomServices(count int) []servicecatalog.Service {
	services := make([]servicecatalog.Service, count)
	for index := range services {
		services[index] = servicecatalog.Service{
			Key: fmt.Sprintf("service-%04d", index), DisplayName: fmt.Sprintf("Service %d", index),
			Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase,
		}
	}
	return services
}

func serviceStateV3HeadroomActivation(
	t *testing.T, state servicecatalog.ServiceState, search string,
) serviceStateV3Change {
	t.Helper()
	next := state
	next.ActiveDesiredGeneration = state.DesiredGeneration
	next.ActiveSourceGeneration = state.DesiredSourceGeneration
	next.ActiveCatalogGeneration = state.DesiredCatalogGeneration
	next.ActiveSearchGeneration = search
	next.Status = servicecatalog.StatusCurrent
	next.ControlRevision++
	next.ChangedAt = storeTimestamp(time.Now())
	if err := servicecatalogv3.SetServiceStateDigest(&next); err != nil {
		t.Fatal(err)
	}
	return serviceStateV3Change{
		update: serviceStateUpdate{State: next, ExpectedRevision: state.ControlRevision, ExpectedDigest: state.StateDigest},
		prior:  state, existed: true,
	}
}
