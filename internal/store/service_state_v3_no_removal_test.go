package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"

	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
)

func TestServiceStateV3NoRemovalLiveCensus(t *testing.T) {
	const repository = "example.com/acme/no-removal-census"
	row := func(key, status string) serviceStateV3LiveRec {
		rid := serviceStateV3ID(repository, key)
		return serviceStateV3LiveRec{RecID: &rid, ServiceKey: key, Status: status,
			ControlRevision: 1, VisibleFrom: 1}
	}
	valid := []serviceStateV3LiveRec{
		row("alpha", servicecatalog.StatusCurrent), row("bravo", servicecatalog.StatusStale),
		row("charlie", servicecatalog.StatusUnavailable), row("delta", servicecatalog.StatusConflict),
	}
	tests := []struct {
		name   string
		mutate func([]serviceStateV3LiveRec) []serviceStateV3LiveRec
	}{
		{"duplicate", func(rows []serviceStateV3LiveRec) []serviceStateV3LiveRec { rows[1] = rows[0]; return rows }},
		{"unsorted", func(rows []serviceStateV3LiveRec) []serviceStateV3LiveRec {
			rows[0], rows[1] = rows[1], rows[0]
			return rows
		}},
		{"removed", func(rows []serviceStateV3LiveRec) []serviceStateV3LiveRec { rows[3].Removed = true; return rows }},
		{"status", func(rows []serviceStateV3LiveRec) []serviceStateV3LiveRec { rows[3].Status = "unknown"; return rows }},
		{"zero revision", func(rows []serviceStateV3LiveRec) []serviceStateV3LiveRec { rows[3].ControlRevision = 0; return rows }},
		{"zero visibility", func(rows []serviceStateV3LiveRec) []serviceStateV3LiveRec { rows[3].VisibleFrom = 0; return rows }},
		{"wrong native ID", func(rows []serviceStateV3LiveRec) []serviceStateV3LiveRec { rows[3].RecID = rows[0].RecID; return rows }},
		{"absent native ID", func(rows []serviceStateV3LiveRec) []serviceStateV3LiveRec { rows[3].RecID = nil; return rows }},
		{"empty key", func(rows []serviceStateV3LiveRec) []serviceStateV3LiveRec { rows[3].ServiceKey = ""; return rows }},
		{"punctuation prefix", func(rows []serviceStateV3LiveRec) []serviceStateV3LiveRec {
			rows[3] = row("_delta", servicecatalog.StatusCurrent)
			return rows[3:]
		}},
		{"non ASCII", func(rows []serviceStateV3LiveRec) []serviceStateV3LiveRec {
			rows[3] = row("délta", servicecatalog.StatusCurrent)
			return rows
		}},
		{"key cap", func(rows []serviceStateV3LiveRec) []serviceStateV3LiveRec {
			rows[3] = row(strings.Repeat("z", servicecatalog.MaxServiceKeyBytes+1), servicecatalog.StatusCurrent)
			return rows
		}},
		{"row cap", func([]serviceStateV3LiveRec) []serviceStateV3LiveRec {
			return make([]serviceStateV3LiveRec, servicecatalogv3.MaxTotalServices*2+1)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := serviceStateV3LiveCensus(t.Context(), repository, test.mutate(slices.Clone(valid))); !errors.Is(err, ErrInvalidServiceStateV3) {
				t.Fatalf("invalid census = %v", err)
			}
		})
	}
	keys, counts, err := serviceStateV3LiveCensus(t.Context(), repository, valid)
	if err != nil || !slices.Equal(keys, []string{"alpha", "bravo", "charlie", "delta"}) ||
		counts != (serviceStateV3Counts{Live: 4, Current: 1, Stale: 1, Unavailable: 1, Conflict: 1}) {
		t.Fatalf("valid census = %v, %+v, %v", keys, counts, err)
	}
	empty, counts, err := serviceStateV3LiveCensus(t.Context(), repository, nil)
	raw, encodeErr := json.Marshal(empty)
	if err != nil || encodeErr != nil || string(raw) != "[]" || counts != (serviceStateV3Counts{}) {
		t.Fatalf("empty census = %s, %+v, %v, %v", raw, counts, err, encodeErr)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := serviceStateV3LiveCensus(canceled, repository, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled empty census = %v", err)
	}
}

func TestServiceStateV3NoRemovalCandidateKeys(t *testing.T) {
	services := []servicecatalog.Service{
		{Key: "accepted", DisplayName: "Accepted", Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase},
		{Key: "conflict", DisplayName: "Conflict", Disposition: servicecatalog.DispositionConflict, Origin: servicecatalog.OriginBase, Reason: "neutral conflict"},
		{Key: "proposal", DisplayName: "Proposal", Disposition: servicecatalog.DispositionProposal, Origin: servicecatalog.OriginBase, Reason: "neutral proposal"},
		{Key: "rejected", DisplayName: "Rejected", Disposition: servicecatalog.DispositionRejected, Origin: servicecatalog.OriginBase, Reason: "neutral rejection"},
	}
	generation := serviceStateV3Generation(t, "example.com/acme/no-removal-keys", strings.Repeat("a", 40), "keys", services)
	keys, err := serviceStateV3CandidateKeys(t.Context(), generation)
	if err != nil || !slices.Equal(keys.all, []string{"accepted", "conflict", "proposal", "rejected"}) ||
		!slices.Equal(keys.live, []string{"accepted", "conflict", "proposal"}) {
		t.Fatalf("projected keys = %+v, %v", keys, err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*servicecatalogv3.Generation)
	}{
		{"missing", func(value *servicecatalogv3.Generation) { value.Members = value.Members[1:] }},
		{"wrong ordinal", func(value *servicecatalogv3.Generation) { value.Members[0].Ordinal++ }},
		{"changed bytes", func(value *servicecatalogv3.Generation) { value.Members[0].Content = []byte("{}") }},
		{"root count", func(value *servicecatalogv3.Generation) { value.Root.Services++ }},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := generation
			changed.Members = slices.Clone(changed.Members)
			test.mutate(&changed)
			if _, err := serviceStateV3CandidateKeys(t.Context(), changed); err == nil {
				t.Fatal("invalid candidate admitted")
			}
		})
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := serviceStateV3CandidateKeys(canceled, generation); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled candidate = %v", err)
	}
}

// This suite uses one real native store and tiny independent repositories. It
// proves store behavior, not a frozen whole-phase transaction-budget pass.
func TestServiceStateV3NoRemovalNative(t *testing.T) {
	ctx := noRemovalContext(t)
	s := newServiceCatalogV3InternalStoreContext(ctx, t)
	for _, test := range []struct {
		name         string
		services     []servicecatalog.Service
		wantRemovals bool
		wantLive     int
	}{
		{"unchanged", noRemovalServices("alpha", "bravo"), false, 2},
		{"added", noRemovalServices("alpha", "bravo", "charlie"), false, 3},
		{"missing", noRemovalServices("alpha"), true, 1},
		{"rejected", []servicecatalog.Service{
			{Key: "alpha", DisplayName: "Alpha", Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase},
			{Key: "bravo", DisplayName: "Bravo", Disposition: servicecatalog.DispositionRejected, Origin: servicecatalog.OriginBase, Reason: "neutral rejection"},
		}, false, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository, prior := noRemovalSeed(ctx, t, s, test.name)
			generation := serviceStateV3Generation(t, repository, strings.Repeat("a", 40), "next", test.services)
			if err := s.PublishServiceCatalogV3Candidate(ctx, generation); err != nil {
				t.Fatal(err)
			}
			begin, err := s.BeginServiceStateV3Reconcile(ctx, repository)
			if err != nil || begin.Plan == nil || (begin.Plan.RemovalChunks > 0) != test.wantRemovals {
				t.Fatalf("successor = %+v, %v", begin, err)
			}
			again, err := s.BeginServiceStateV3Reconcile(ctx, repository)
			if err != nil || !reflect.DeepEqual(again.Plan, begin.Plan) {
				t.Fatalf("active layout changed = %+v, %v", again, err)
			}
			runServiceStateV3PlanContext(ctx, t, s, begin)
			summary, err := s.GetServiceStateV3Summary(ctx, repository)
			if err != nil || summary.LiveServiceCount != test.wantLive || summary.ControlRevision <= prior.ControlRevision {
				t.Fatalf("final summary = %+v, %v", summary, err)
			}
		})
	}
	for _, mutation := range []string{"candidate", "summary", "prior progress", "live key", "prior absence"} {
		t.Run("create fence "+mutation, func(t *testing.T) {
			repository, summary := noRemovalSeed(ctx, t, s, "fence-"+strings.ReplaceAll(mutation, " ", "-"))
			prior, priorSchedule, err := s.currentServiceStateV3Plan(ctx, repository, serviceStateV3Reconcile)
			if err != nil || prior == nil || priorSchedule == nil {
				t.Fatalf("prior = %+v, %v", prior, err)
			}
			generation := serviceStateV3Generation(t, repository, strings.Repeat("a", 40), "next", noRemovalServices("alpha", "bravo"))
			if err := s.PublishServiceCatalogV3Candidate(ctx, generation); err != nil {
				t.Fatal(err)
			}
			candidate, err := s.GetServiceCatalogV3Candidate(ctx, repository)
			if err != nil {
				t.Fatal(err)
			}
			proof, err := s.proveServiceStateV3NoRemovals(ctx, candidate, summary)
			if err != nil || proof == nil {
				t.Fatalf("proof = %+v, %v", proof, err)
			}
			plan := noRemovalPreparedPlan(ctx, t, s, candidate, *summary, 0)
			switch mutation {
			case "candidate":
				noRemovalQuery(ctx, t, s, "UPDATE $rid SET control_revision += 1 RETURN NONE", serviceCatalogV3CandidateID(repository))
			case "summary":
				noRemovalQuery(ctx, t, s, "UPDATE $rid SET current_count += 1 RETURN NONE", serviceStateV3RepositoryID(repository))
			case "prior progress":
				noRemovalQuery(ctx, t, s, "UPDATE $rid SET rows_read += 1 RETURN NONE", serviceStateV3PlanID(prior.Digest))
			case "live key":
				noRemovalQuery(ctx, t, s, "UPDATE $rid SET removed = true RETURN NONE", serviceStateV3ID(repository, "alpha"))
			case "prior absence":
				noRemovalQuery(ctx, t, s, "DELETE $rid RETURN NONE", serviceStateV3PlanID(prior.Digest))
			}
			if err := s.createServiceStateV3Plan(ctx, plan, prior, proof); err == nil {
				t.Fatal("changed proof admitted")
			}
			if _, err := s.getServiceStateV3Plan(ctx, plan.Digest); !errors.Is(err, ErrNotFound) {
				t.Fatalf("failed proof created a plan: %v", err)
			}
		})
	}
	for _, mutation := range []string{"missing", "extra"} {
		t.Run("final drain "+mutation, func(t *testing.T) {
			repository, _ := noRemovalSeed(ctx, t, s, "drain-"+mutation)
			generation := serviceStateV3Generation(t, repository, strings.Repeat("a", 40), "next", noRemovalServices("alpha", "bravo"))
			if err := s.PublishServiceCatalogV3Candidate(ctx, generation); err != nil {
				t.Fatal(err)
			}
			begin, err := s.BeginServiceStateV3Reconcile(ctx, repository)
			if err != nil {
				t.Fatal(err)
			}
			expandServiceStateV3PlanContext(ctx, t, s, begin)
			member, err := s.ClaimGenerationChunk(ctx, GenerationResourceCPU, "no-removal-member")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.ProcessServiceStateV3Chunk(ctx, *member); err != nil {
				t.Fatal(err)
			}
			if err := s.CompleteGenerationChunk(ctx, *member); err != nil {
				t.Fatal(err)
			}
			if mutation == "missing" {
				noRemovalQuery(ctx, t, s, "DELETE $rid RETURN NONE", serviceStateV3ID(repository, "alpha"))
			} else {
				noRemovalQuery(ctx, t, s, "UPDATE $rid SET service_key = 'zulu' RETURN NONE", serviceStateV3ID(repository, "alpha"))
			}
			final, err := s.ClaimGenerationChunk(ctx, GenerationResourceCPU, "no-removal-final")
			if err != nil || final.Offset != int64(begin.Plan.TotalChunks-1) {
				t.Fatalf("final = %+v, %v", final, err)
			}
			if _, err := s.ProcessServiceStateV3Chunk(ctx, *final); err == nil {
				t.Fatal("invalid final live set admitted")
			}
		})
	}
}

func TestServiceStateV3NoRemovalPriorPrefixFence(t *testing.T) {
	ctx := noRemovalContext(t)
	s := newServiceCatalogV3InternalStoreContext(ctx, t)
	for _, changeStatus := range []bool{false, true} {
		t.Run(fmt.Sprintf("changed-status-%t", changeStatus), func(t *testing.T) {
			repository, summary := noRemovalSeed(ctx, t, s, fmt.Sprintf("prefix-%t", changeStatus))
			if changeStatus {
				activation, err := s.BeginServiceStateV3Activation(ctx, repository, selectorTestDigest("9"))
				if err != nil {
					t.Fatal(err)
				}
				runServiceStateV3PlanContext(ctx, t, s, activation)
				summary, err = s.GetServiceStateV3Summary(ctx, repository)
				if err != nil {
					t.Fatal(err)
				}
			}
			services := noRemovalServices("alpha", "bravo")
			if changeStatus {
				services[0].DisplayName = "Changed Alpha"
			}
			second := serviceStateV3Generation(t, repository, strings.Repeat("a", 40), "second", services)
			if err := s.PublishServiceCatalogV3Candidate(ctx, second); err != nil {
				t.Fatal(err)
			}
			begin, err := s.BeginServiceStateV3Reconcile(ctx, repository)
			if err != nil {
				t.Fatal(err)
			}
			capturedPrior := *begin.Plan
			candidate, err := s.GetServiceCatalogV3Candidate(ctx, repository)
			if err != nil {
				t.Fatal(err)
			}
			// Capture the actual candidate before a real prior prefix. The raw
			// keys stay equal, but the stale progress witness must still refuse.
			proof, err := s.proveServiceStateV3NoRemovals(ctx, candidate, summary)
			if err != nil || proof == nil {
				t.Fatalf("future census = %+v, %v", proof, err)
			}
			expandServiceStateV3PlanContext(ctx, t, s, begin)
			chunk, err := s.ClaimGenerationChunk(ctx, GenerationResourceCPU, "prefix-fence")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.ProcessServiceStateV3Chunk(ctx, *chunk); err != nil {
				t.Fatal(err)
			}
			if err := s.CompleteGenerationChunk(ctx, *chunk); err != nil {
				t.Fatal(err)
			}
			advanced, err := s.getServiceStateV3Plan(ctx, capturedPrior.Digest)
			if err != nil || advanced.NextChunk == capturedPrior.NextChunk || advanced.RowsRead == capturedPrior.RowsRead {
				t.Fatalf("real prefix did not advance = %+v, %v", advanced, err)
			}
			if (advanced.CurrentCount != capturedPrior.CurrentCount) != changeStatus {
				t.Fatalf("unexpected prefix status counts = %+v", advanced)
			}
			afterSummary, err := s.getRawServiceStateV3Summary(ctx, repository)
			if err != nil || !reflect.DeepEqual(afterSummary, summary) {
				t.Fatalf("prefix changed raw summary = %+v, %v", afterSummary, err)
			}
			third := serviceStateV3Generation(t, repository, strings.Repeat("a", 40), "third", services)
			if err := s.PublishServiceCatalogV3Candidate(ctx, third); !errors.Is(err, ErrConflict) {
				t.Fatalf("unsettled future candidate = %v", err)
			}
			// A private creation-boundary probe uses a new schedule digest for
			// the same actual target; public repair separately preserves layout.
			plan := noRemovalPreparedPlan(ctx, t, s, candidate, *summary, 1)
			if err := s.createServiceStateV3Plan(ctx, plan, &capturedPrior, proof); err == nil {
				t.Fatal("same-key prior progress admitted using the old census")
			}
		})
	}
}

func TestServiceStateV3NoRemovalRepairKeepsLayout(t *testing.T) {
	ctx := noRemovalContext(t)
	s := newServiceCatalogV3InternalStoreContext(ctx, t)
	repository, _ := noRemovalSeed(ctx, t, s, "repair")
	generation := serviceStateV3Generation(t, repository, strings.Repeat("a", 40), "removal", noRemovalServices("alpha"))
	if err := s.PublishServiceCatalogV3Candidate(ctx, generation); err != nil {
		t.Fatal(err)
	}
	begin, err := s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil || begin.Plan.RemovalChunks == 0 {
		t.Fatalf("conservative plan = %+v, %v", begin, err)
	}
	expandServiceStateV3PlanContext(ctx, t, s, begin)
	for ordinal := 0; ordinal < begin.Plan.TotalChunks-1; ordinal++ {
		chunk, err := s.ClaimGenerationChunk(ctx, GenerationResourceCPU, "repair-prefix")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.ProcessServiceStateV3Chunk(ctx, *chunk); err != nil {
			t.Fatal(err)
		}
		if err := s.CompleteGenerationChunk(ctx, *chunk); err != nil {
			t.Fatal(err)
		}
	}
	// The real removal has now completed, making a fresh key-only proof look
	// eligible. A repair must nevertheless retain the existing final ordinal.
	for attempt := 0; attempt < MaxGenerationAttempts; attempt++ {
		schedule, err := s.GetGenerationSchedule(ctx, repository, ServiceStateV3ReconcileStage)
		if err != nil {
			t.Fatal(err)
		}
		if schedule.Status == GenerationScheduleSettled {
			break
		}
		chunk, err := s.ClaimGenerationChunk(ctx, GenerationResourceCPU, "repair-final-failure")
		if err != nil {
			t.Fatal(err)
		}
		if err := s.FailGenerationChunk(ctx, *chunk, "neutral final failure"); err != nil {
			t.Fatal(err)
		}
	}
	repair, err := s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil || repair.Plan == nil || repair.Plan.Repair != begin.Plan.Repair+1 ||
		repair.Plan.RemovalChunks != begin.Plan.RemovalChunks || repair.Plan.TotalChunks != begin.Plan.TotalChunks ||
		repair.Plan.BaseChunk != begin.Plan.TotalChunks-1 {
		t.Fatalf("repair layout = %+v, %v", repair, err)
	}
	runServiceStateV3PlanContext(ctx, t, s, repair)
}

func TestServiceStateV3NoRemovalAfterRestore(t *testing.T) {
	ctx := noRemovalContext(t)
	fixture := newServiceRuntimeSelectorFixtureContext(ctx, t)
	s := fixture.store
	if _, err := s.SelectServiceRuntimeV3(ctx, ServiceRuntimeSelectionRequest{
		Repository: fixture.repository, Target: fixture.v3,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.RestoreSelectedServiceStateV3ForRestore(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearAllGenerationScheduleStateForRestore(ctx); err != nil {
		t.Fatal(err)
	}
	generation := serviceStateV3Generation(t, fixture.repository, strings.Repeat("7", 40), "restored-next", noRemovalServices("orders"))
	if err := s.PublishServiceCatalogV3Candidate(ctx, generation); err != nil {
		t.Fatal(err)
	}
	begin, err := s.BeginServiceStateV3Reconcile(ctx, fixture.repository)
	if err != nil || begin.Plan == nil || begin.Plan.RemovalChunks != 0 {
		t.Fatalf("restored new plan = %+v, %v", begin, err)
	}
	runServiceStateV3PlanContext(ctx, t, s, begin)
}

func noRemovalContext(t *testing.T) context.Context {
	t.Helper()
	deadline := time.Now().Add(4 * time.Minute)
	if outer, ok := t.Deadline(); ok && outer.Add(-time.Minute).Before(deadline) {
		deadline = outer.Add(-time.Minute)
	}
	ctx, cancel := context.WithDeadline(t.Context(), deadline)
	t.Cleanup(cancel)
	if err := ctx.Err(); err != nil {
		t.Fatal(err)
	}
	return ctx
}

func noRemovalServices(keys ...string) []servicecatalog.Service {
	services := make([]servicecatalog.Service, 0, len(keys))
	for _, key := range keys {
		services = append(services, servicecatalog.Service{Key: key, DisplayName: key,
			Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase})
	}
	return services
}

func noRemovalSeed(ctx context.Context, t *testing.T, s *Surreal, suffix string) (string, *servicecatalog.RepositoryState) {
	t.Helper()
	repository := "example.com/acme/no-removal-" + suffix
	commit := strings.Repeat("a", 40)
	seedServiceCatalogV3RepoContext(ctx, t, s, repository, commit)
	generation := serviceStateV3Generation(t, repository, commit, "first", noRemovalServices("alpha", "bravo"))
	if err := s.PublishServiceCatalogV3Candidate(ctx, generation); err != nil {
		t.Fatal(err)
	}
	begin, err := s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil || begin.Plan == nil || begin.Plan.RemovalChunks != 0 {
		t.Fatalf("cold proof = %+v, %v", begin, err)
	}
	runServiceStateV3PlanContext(ctx, t, s, begin)
	summary, err := s.GetServiceStateV3Summary(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	return repository, summary
}

func noRemovalPreparedPlan(ctx context.Context, t *testing.T, s *Surreal, candidate *ServiceCatalogV3Candidate, summary servicecatalog.RepositoryState, repair int) ServiceStateV3Plan {
	t.Helper()
	root := candidate.Generation.Root
	now := storeTimestamp(time.Now())
	plan := ServiceStateV3Plan{Schema: serviceStateV3PlanSchema, Repository: root.Binding.Repository,
		Phase: serviceStateV3Reconcile, CatalogRoot: root.Digest, CatalogControlRevision: candidate.ControlRevision,
		ServiceMemberChunks: len(root.ServiceMembers), TotalChunks: len(root.ServiceMembers) + 1,
		CatalogServiceCount: root.Services, State: serviceStateV3Running, Repair: repair,
		LiveServiceCount: summary.LiveServiceCount, CurrentCount: summary.CurrentCount,
		StaleCount: summary.StaleCount, UnavailableCount: summary.UnavailableCount,
		ConflictCount: summary.ConflictCount, TombstoneCount: summary.TombstoneCount,
		SummaryControlRevision: summary.ControlRevision, SummaryDigest: summary.SummaryDigest,
		CreatedAt: now, UpdatedAt: now}
	plan.Digest = serviceStateV3PlanDigest(plan.Repository, plan.Phase, plan.CatalogRoot, plan.CatalogControlRevision, "", repair)
	schedule, err := s.EnqueueGenerationSchedule(ctx, GenerationScheduleSpec{
		Repository: plan.Repository, Stage: serviceStateV3Stage(plan.Phase), Generation: plan.Digest,
		ResourceClass: GenerationResourceCPU, TotalItems: int64(plan.TotalChunks), ChunkItems: 1,
		MaxAttempts: MaxGenerationAttempts, RepositoryTokens: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	plan.ScheduleDigest = schedule.Digest
	return plan
}

func noRemovalQuery(ctx context.Context, t *testing.T, s *Surreal, query string, rid models.RecordID) {
	t.Helper()
	results, err := surrealdb.Query[any](ctx, s.db, query, map[string]any{"rid": rid})
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range *results {
		if result.Error != nil {
			t.Fatal(fmt.Errorf("native test mutation: %s", result.Error.Message))
		}
	}
}
