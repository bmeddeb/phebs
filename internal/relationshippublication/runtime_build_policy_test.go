package relationshippublication

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/kafkatopicposting"
	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	"github.com/bmeddeb/phebs/internal/resolvernamespace"
	"github.com/bmeddeb/phebs/internal/rpccallerposting"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/store"
)

// This is the exact canonical payload used by runtimeBuildPolicyDigest before
// T42.1r4 (a57a4bd9), not an invented different policy identity. Only the domain
// changes in the selected-v2 path; every serialized artifact policy is retained.
func testRuntimeBuildPolicyDigest(t *testing.T, domain string) string {
	t.Helper()
	digest, err := digestValue(struct {
		Domain           string                   `json:"domain"`
		Resolver         resolvernamespace.Policy `json:"resolver"`
		RPC              rpccallerposting.Policy  `json:"rpc"`
		Kafka            kafkatopicposting.Policy `json:"kafka"`
		Relationship     Policy                   `json:"relationship"`
		ResolverResident int64                    `json:"resolver_resident_bytes"`
		RPCResident      int64                    `json:"rpc_resident_bytes"`
		KafkaResident    int64                    `json:"kafka_resident_bytes"`
	}{
		Domain: domain, Resolver: resolvernamespace.FrozenPolicy(),
		RPC: rpccallerposting.FrozenPolicy(), Kafka: kafkatopicposting.FrozenPolicy(),
		Relationship: FrozenPolicy(), ResolverResident: ResolverResidentLimit,
		RPCResident: RPCResidentLimit, KafkaResident: KafkaResidentLimit,
	})
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func TestRuntimeBuildPolicyRevisionUsesRetainedPolicies(t *testing.T) {
	old := testRuntimeBuildPolicyDigest(t, "phebs-relationship-build-policy-v1")
	current, err := runtimeBuildPolicyDigest()
	if err != nil || current == old || current != testRuntimeBuildPolicyDigest(t, "phebs-relationship-build-policy-v2") {
		t.Fatalf("runtime build-policy revision: old=%q current=%q err=%v", old, current, err)
	}
	oldDirect, err := digestValue(FrozenPolicyV3())
	if err != nil {
		t.Fatal(err)
	}
	want, err := digestValue(struct {
		Domain       string   `json:"domain"`
		Components   string   `json:"components"`
		Relationship PolicyV3 `json:"relationship"`
	}{
		Domain:     "phebs-relationship-v3-runtime-build-policy-v1",
		Components: current, Relationship: FrozenPolicyV3(),
	})
	if err != nil {
		t.Fatal(err)
	}
	direct, err := runtimeBuildPolicyDigestV3()
	if err != nil || direct == oldDirect || direct != want {
		t.Fatalf("direct build-policy revision: old=%q current=%q err=%v", oldDirect, direct, err)
	}
}

type runtimePolicyStore struct {
	*runtimeHandleCleanupStore
	failure      *store.GenerationScheduleFailure
	enqueues     int
	replacements int
	failureReads int
}

func (state *runtimePolicyStore) GetServiceStateSummary(_ context.Context, repository string) (*servicecatalog.RepositoryState, error) {
	if repository != state.summary.Repository {
		return nil, store.ErrNotFound
	}
	value := state.summary
	return &value, nil
}

func (state *runtimePolicyStore) GetGenerationScheduleFailure(_ context.Context, repository, stage, digest string) (*store.GenerationScheduleFailure, error) {
	state.failureReads++
	if state.failure == nil || repository != state.schedule.Repository || stage != state.schedule.Stage || digest != state.failure.ScheduleDigest {
		return nil, store.ErrNotFound
	}
	value := *state.failure
	return &value, nil
}

func (state *runtimePolicyStore) EnqueueGenerationSchedule(_ context.Context, spec store.GenerationScheduleSpec) (*store.GenerationSchedule, error) {
	state.enqueues++
	value, err := testPolicySchedule(spec)
	if err != nil {
		return nil, err
	}
	if state.schedule.Digest != value.Digest {
		state.replacements++
		state.schedule = value
	}
	result := state.schedule
	return &result, nil
}

// The queue double records control transitions; all generation and schedule
// identities come from the production derivations. It is not a live queue test.
func testPolicySchedule(spec store.GenerationScheduleSpec) (store.GenerationSchedule, error) {
	digest, err := store.GenerationScheduleDigest(spec)
	return store.GenerationSchedule{
		Repository: spec.Repository, Stage: spec.Stage, Generation: spec.Generation,
		Digest: digest, ResourceClass: spec.ResourceClass, TotalItems: spec.TotalItems,
		ChunkItems: spec.ChunkItems, TotalChunks: 1, MaxAttempts: spec.MaxAttempts,
		RepositoryTokens: spec.RepositoryTokens, Status: store.GenerationScheduleActive,
	}, err
}

func testPolicyScheduleFor(t *testing.T, repository, stage, generation string) store.GenerationSchedule {
	t.Helper()
	value, err := testPolicySchedule(store.GenerationScheduleSpec{
		Repository: repository, Stage: stage, Generation: generation,
		ResourceClass: store.GenerationResourceMemory, TotalItems: 1, ChunkItems: 1,
		MaxAttempts: ScheduleMaxAttempts, RepositoryTokens: ScheduleRepositoryTokens,
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func testNamespaceTerminal(t *testing.T, schedule store.GenerationSchedule) *store.GenerationScheduleFailure {
	t.Helper()
	refusal, ok := pipelinerefusal.From(relationshipBuildFailure(
		rpccallerposting.ErrLimit, "build relationship RPC postings", rpccallerposting.ErrLimit,
		pipelinerefusal.StageRelationshipRPCPostings,
	))
	if !ok || pipelinerefusal.Validate(refusal) != nil {
		t.Fatal("namespace limit did not produce its native closed refusal")
	}
	return &store.GenerationScheduleFailure{ScheduleDigest: schedule.Digest, Generation: schedule.Generation, Refusal: &refusal}
}

func TestRuntimeNamespacePolicyUpgradeV2(t *testing.T) {
	for _, test := range []struct {
		name, policy string
		terminal     bool
		published    bool
		wantReplace  bool
	}{
		{"old_terminal", "phebs-relationship-build-policy-v1", true, false, true},
		{"current_terminal", "phebs-relationship-build-policy-v2", true, false, false},
		{"old_active", "phebs-relationship-build-policy-v1", false, false, true},
		{"current_active", "phebs-relationship-build-policy-v2", false, false, false},
		{"old_completed_publication_existing_v2_replacement", "phebs-relationship-build-policy-v1", false, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRuntimeHandleCleanupFixture(t)
			state := &runtimePolicyStore{runtimeHandleCleanupStore: fixture.store}
			fixture.runtime.Store = state
			binding, err := fixture.runtime.readBinding(fixture.chunk.Repository, fixture.chunk.Generation)
			if err != nil {
				t.Fatal(err)
			}
			binding.BuildPolicyDigest = testRuntimeBuildPolicyDigest(t, test.policy)
			binding.TargetGeneration, err = runtimeTargetV3(
				binding.Repository, binding.Upstream.Digest, binding.CatalogGeneration, binding.ServiceStateSet,
				binding.ResolverGeneration, binding.ServiceStateSummary, binding.ServiceStateRevision, binding.BuildPolicyDigest,
			)
			if err != nil {
				t.Fatal(err)
			}
			binding.ScheduleGeneration = binding.TargetGeneration
			if err := setBindingDigest(&binding); err != nil {
				t.Fatal(err)
			}
			if err := fixture.runtime.writeBinding(binding); err != nil {
				t.Fatal(err)
			}
			state.schedule = testPolicyScheduleFor(t, binding.Repository, ScheduleStage, binding.ScheduleGeneration)
			fixture.chunk.Generation = binding.ScheduleGeneration
			if test.terminal {
				state.schedule.Status, state.schedule.Failed = store.GenerationScheduleSettled, 1
				state.failure = testNamespaceTerminal(t, state.schedule)
			}
			var published Root
			if test.published {
				if err := fixture.runtime.Handle(t.Context(), fixture.chunk); err != nil {
					t.Fatal(err)
				}
				value, err := OpenCurrent(t.Context(), fixture.runtime.relationshipRoot(), binding.Repository)
				if err != nil {
					t.Fatal(err)
				}
				published = value.Root()
				// This existing selected-v2 comparator compares namespace identity
				// with catalog identity. Do not fake the root to claim a no-op;
				// that separate inefficiency is not repaired by this policy change.
				if published.Authority.ResolverGenerationDigest == state.resolver.GenerationDigest ||
					matchesCurrentV2Authority(published, fixture.upstream, state.summary, state.resolver.GenerationDigest) {
					t.Fatal("selected-v2 fixture no longer witnesses its existing identity distinction")
				}
			}
			priorDigest := state.schedule.Digest
			for range 2 {
				if err := fixture.runtime.Reconcile(t.Context(), binding.Repository); err != nil {
					t.Fatal(err)
				}
			}
			want := 0
			if test.wantReplace {
				want = 1
			}
			if state.replacements != want {
				t.Fatalf("schedule replacements=%d want=%d", state.replacements, want)
			}
			if test.wantReplace {
				current, err := fixture.runtime.readBinding(binding.Repository, state.schedule.Generation)
				policy, policyErr := runtimeBuildPolicyDigest()
				if err != nil || policyErr != nil || current.PriorScheduleDigest != priorDigest || current.BuildPolicyDigest != policy || current.TargetGeneration == binding.TargetGeneration {
					t.Fatalf("replacement lost predecessor/new policy: %+v, %v, %v", current, err, policyErr)
				}
			}
			if test.terminal && !test.wantReplace && state.enqueues != 0 {
				t.Fatal("retained terminal enqueued work")
			}
			if test.published {
				current, err := OpenCurrent(t.Context(), fixture.runtime.relationshipRoot(), binding.Repository)
				if err != nil || current.Root().Digest != published.Digest {
					t.Fatalf("operational upgrade changed current artifact: %v", err)
				}
			}
		})
	}
}

type runtimePolicyStoreV3 struct {
	*runtimeTestStoreV3
	failure      *store.GenerationScheduleFailure
	replacements int
	failureReads int
}

func (state *runtimePolicyStoreV3) GetGenerationScheduleFailure(_ context.Context, repository, stage, digest string) (*store.GenerationScheduleFailure, error) {
	state.failureReads++
	if state.failure == nil || state.schedule == nil || repository != state.schedule.Repository || stage != ScheduleStageV3 || digest != state.failure.ScheduleDigest {
		return nil, store.ErrNotFound
	}
	value := *state.failure
	return &value, nil
}

func (state *runtimePolicyStoreV3) EnqueueGenerationSchedule(_ context.Context, spec store.GenerationScheduleSpec) (*store.GenerationSchedule, error) {
	state.enqueues = append(state.enqueues, spec)
	value, err := testPolicySchedule(spec)
	if err != nil {
		return nil, err
	}
	if state.schedule == nil || state.schedule.Digest != value.Digest {
		state.replacements++
		state.schedule = &value
	}
	result := *state.schedule
	return &result, nil
}

func TestRuntimeNamespacePolicyUpgradeDirectV3(t *testing.T) {
	for _, test := range []struct {
		name        string
		oldPolicy   bool
		terminal    bool
		published   bool
		wantReplace bool
	}{
		{"old_terminal", true, true, false, true},
		{"current_terminal", false, true, false, false},
		{"old_active", true, false, false, true},
		{"current_active", false, false, false, false},
		{"old_completed_publication", true, false, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRuntimeHandleCleanupFixture(t)
			catalog, generation := relationshipCatalogV3Test(t, fixture.chunk.Repository, 2)
			states, summary := relationshipStatesV3Test(t, generation.Root, catalog)
			state := &runtimePolicyStoreV3{runtimeTestStoreV3: &runtimeTestStoreV3{
				runtimeHandleCleanupStore: fixture.store,
				candidate: &store.ServiceCatalogV3Candidate{Generation: generation,
					ControlRevision: summary.CatalogControlRevision, PublishedAt: time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)},
				summary: summary, states: states,
			}}
			fixture.runtime.Store = state
			policy, err := runtimeBuildPolicyDigestV3()
			if test.oldPolicy {
				// This was the complete direct-V3 schedule policy before T42.1r4.
				policy, err = digestValue(FrozenPolicyV3())
			}
			if err != nil {
				t.Fatal(err)
			}
			binding := runtimeBindingV3{
				Schema: runtimeBindingSchemaV3Direct, Repository: fixture.chunk.Repository,
				Upstream: &fixture.upstream, ResolverGeneration: fixture.store.resolver.GenerationDigest,
				CatalogRoot: generation.Root.Digest, CatalogRevision: summary.CatalogControlRevision,
				StateSummary: summary.SummaryDigest, StateRevision: summary.ControlRevision, PolicyDigest: policy,
			}
			binding.TargetGeneration, err = runtimeTargetDirectV3(binding.Repository, binding.Upstream.Digest,
				binding.Upstream.ProvenanceDigest, binding.ResolverGeneration, binding.CatalogRoot, binding.CatalogRevision,
				binding.StateSummary, binding.StateRevision, binding.PolicyDigest)
			if err != nil {
				t.Fatal(err)
			}
			binding.ScheduleGeneration = binding.TargetGeneration
			if err := setRuntimeBindingDigestV3(&binding); err != nil {
				t.Fatal(err)
			}
			if err := fixture.runtime.writeRuntimeBindingV3(binding); err != nil {
				t.Fatal(err)
			}
			schedule := testPolicyScheduleFor(t, binding.Repository, ScheduleStageV3, binding.ScheduleGeneration)
			state.schedule = &schedule
			if test.terminal {
				state.schedule.Status, state.schedule.Failed = store.GenerationScheduleSettled, 1
				state.failure = testNamespaceTerminal(t, *state.schedule)
			}
			var published RootV3
			if test.published {
				if err := fixture.runtime.HandleV3(t.Context(), store.GenerationChunk{Repository: binding.Repository,
					Stage: ScheduleStageV3, Generation: binding.ScheduleGeneration, Offset: 0, Length: 1}); err != nil {
					t.Fatal(err)
				}
				value, err := OpenCurrentV3(t.Context(), fixture.runtime.relationshipRoot(), binding.Repository)
				if err != nil {
					t.Fatal(err)
				}
				published = value.Root()
				fixture.runtime.Admit = func(context.Context) error {
					return errors.New("exact current publication must not request build admission")
				}
			}
			priorDigest := state.schedule.Digest
			for range 2 {
				current, err := fixture.runtime.ReconcileV3(t.Context(), binding.Repository)
				if err != nil || current != test.published {
					t.Fatalf("reconcile current=%t err=%v", current, err)
				}
			}
			want := 0
			if test.wantReplace {
				want = 1
			}
			if state.replacements != want {
				t.Fatalf("schedule replacements=%d want=%d", state.replacements, want)
			}
			if test.wantReplace {
				current, err := fixture.runtime.readRuntimeBindingV3(binding.Repository, state.schedule.Generation)
				policy, policyErr := runtimeBuildPolicyDigestV3()
				if err != nil || policyErr != nil || current.PriorScheduleDigest != priorDigest || current.PolicyDigest != policy || current.TargetGeneration == binding.TargetGeneration {
					t.Fatalf("replacement lost predecessor/new policy: %+v, %v, %v", current, err, policyErr)
				}
			}
			if (test.terminal && !test.wantReplace || test.published) && len(state.enqueues) != 0 {
				t.Fatal("retained terminal/current publication enqueued work")
			}
			if test.published {
				value, err := OpenGenerationV3(t.Context(), fixture.runtime.relationshipRoot(), binding.Repository,
					published.GenerationDigest, published.Digest)
				if err != nil || value.Root().Digest != published.Digest {
					t.Fatalf("operational upgrade invalidated old artifact: %v", err)
				}
			}
		})
	}
}
