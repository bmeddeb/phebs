package t421

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
)

type executionFreezeTestKey struct {
	PlanSHA256 string
	Commits    ExecutionCommits
}

// This cache contains only public-admitted test bindings. Its host, signer,
// tool, checkout, configuration and signature fixtures are deterministic from
// the exact plan and supplied commits; they are not external ceremony evidence.
// A miss deliberately pays both BuildExecutionFreeze's validation and public
// binding admission. Hits hash the plan and clone the small owned freeze only.
type executionFreezeTestCache struct {
	mu       sync.Mutex
	bindings map[executionFreezeTestKey]ExecutionFreezeBinding
}

var executionFreezeAdmissionCache executionFreezeTestCache

func admittedExecutionFreezeTestBinding(t *testing.T, plan Plan, commits ExecutionCommits) ExecutionFreezeBinding {
	t.Helper()
	binding, err := executionFreezeAdmissionCache.binding(t, plan, commits)
	if err != nil {
		t.Fatal(err)
	}
	return binding
}

func (cache *executionFreezeTestCache) binding(t *testing.T, plan Plan, commits ExecutionCommits) (ExecutionFreezeBinding, error) {
	t.Helper()
	planSHA256, err := receiptSHA256(plan)
	if err != nil {
		return ExecutionFreezeBinding{}, fmt.Errorf("hash fixture plan: %w", err)
	}
	key := executionFreezeTestKey{PlanSHA256: planSHA256, Commits: commits}
	// ponytail: Serialize hits and full-profile admission misses with one mutex.
	// Parallel admissions would need per-key coordination only if test throughput
	// warrants the extra machinery. No live worker runs here.
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if binding, ok := cache.bindings[key]; ok {
		binding.freeze = cloneExecutionFreeze(t, binding.freeze)
		return binding, nil
	}
	tools, host := executionFreezeTestTools(plan, commits), executionFreezeTestHost()
	checkout := executionFreezeTestCheckout(t, commits, tools)
	profile := executionProfileTestAdmission(t, plan, tools, host)
	signer := executionFreezeTestSigner()
	freeze, err := BuildExecutionFreeze(plan, commits, tools, host, signer, checkout, profile)
	if err != nil {
		return ExecutionFreezeBinding{}, fmt.Errorf("build fixture freeze: %w", err)
	}
	binding, err := BindExecutionFreezeForReceipt(
		freeze, plan, commits, signer, checkout, profile,
		executionFreezeTestAdmission(t, plan, freeze),
	)
	if err != nil {
		return ExecutionFreezeBinding{}, fmt.Errorf("admit fixture freeze: %w", err)
	}
	if cache.bindings == nil {
		cache.bindings = make(map[executionFreezeTestKey]ExecutionFreezeBinding)
	}
	cache.bindings[key] = binding
	binding.freeze = cloneExecutionFreeze(t, binding.freeze)
	return binding, nil
}

func TestFixtureAdmissionCachePreservesVersionOrder(t *testing.T) {
	plans := []Plan{frozenTestPlan(t), correctedTestPlan(t)}
	commits := executionFreezeTestCommits()
	for _, test := range []struct {
		name  string
		order []int
	}{
		{name: "v1_then_v2", order: []int{0, 1, 0, 1}},
		{name: "v2_then_v1", order: []int{1, 0, 1, 0}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var cache executionFreezeTestCache
			seen := make(map[int]ExecutionFreezeBinding, len(plans))
			for _, index := range test.order {
				plan := plans[index]
				binding, err := cache.binding(t, plan, commits)
				if err != nil {
					t.Fatal(err)
				}
				assertFixtureAdmissionIdentity(t, binding, plan, commits)
				wantEpoch, wantEpochs := uint64(2), 3
				if plan.Schema == PlanV2Schema {
					wantEpoch, wantEpochs = 4, 5
				}
				epoch, launch, ok := expectedPhaseRuntime(binding.freeze.Profile.Epochs, "pressure_80")
				if !ok || epoch != wantEpoch || launch != "process_restart" || len(binding.freeze.Profile.Epochs) != wantEpochs ||
					binding.freeze.Profile.RuntimeBindingSchema != phaseRuntimeBindingSchema(plan) {
					t.Fatalf("%s reused the other plan's runtime: %+v", plan.Schema, binding.freeze.Profile.Epochs)
				}
				if previous, ok := seen[index]; ok && !reflect.DeepEqual(previous, binding) {
					t.Fatal("same exact inputs changed after visiting another schema")
				}
				seen[index] = binding
				if len(cache.bindings) != len(seen) {
					t.Fatal("cache did not retain one admitted binding per exact input")
				}
			}
		})
	}
}

func TestFixtureAdmissionCacheBindsExactPlanAndCommits(t *testing.T) {
	plan := frozenTestPlan(t)
	commits := executionFreezeTestCommits()
	sourceCommits := commits
	sourceCommits.T422SourceCommit, sourceCommits.T422SourceTree = strings.Repeat("d", 40), strings.Repeat("3", 40)
	integratedCommits := commits
	integratedCommits.IntegratedMainCommit, integratedCommits.IntegratedMainTree = strings.Repeat("e", 40), strings.Repeat("4", 40)
	otherPlan := clonePlan(t, plan)
	otherPlan.SourceCommit = strings.Repeat("f", 40)
	// The other source commit is admitted by the real frozen-plan generator;
	// these are not schema-only or unchecked synthetic cache values.
	seen := make(map[string]struct{}, 4)
	for _, test := range []struct {
		name    string
		plan    Plan
		commits ExecutionCommits
	}{
		{name: "baseline", plan: plan, commits: commits},
		{name: "source_commit_and_tree", plan: plan, commits: sourceCommits},
		{name: "integrated_commit_and_tree", plan: plan, commits: integratedCommits},
		{name: "same_schema_other_plan", plan: otherPlan, commits: commits},
	} {
		t.Run(test.name, func(t *testing.T) {
			binding := admittedExecutionFreezeTestBinding(t, test.plan, test.commits)
			assertFixtureAdmissionIdentity(t, binding, test.plan, test.commits)
			if _, duplicate := seen[binding.freezeSHA256]; duplicate {
				t.Fatal("different exact input reused a prior freeze")
			}
			seen[binding.freezeSHA256] = struct{}{}
			for _, tool := range binding.freeze.Tools {
				if tool.Provenance == "go-build-info-vcs-v1" && tool.BuildVCSRevision != test.commits.T422SourceCommit {
					t.Fatal("repository tool provenance did not follow supplied source commit")
				}
			}
			if again := admittedExecutionFreezeTestBinding(t, test.plan, test.commits); !reflect.DeepEqual(binding, again) {
				t.Fatal("repeated exact input did not return the same admitted value")
			}
		})
	}
}

func TestFixtureAdmissionCacheReturnsOwnedBindings(t *testing.T) {
	plans := []Plan{frozenTestPlan(t), correctedTestPlan(t)}
	commits := executionFreezeTestCommits()
	for _, plan := range plans {
		t.Run(plan.Schema, func(t *testing.T) {
			want := admittedExecutionFreezeTestBinding(t, plan, commits)
			// Keep the oracle independent even if a regression removes the
			// cache's defensive copy and makes every returned slice alias.
			want.freeze = cloneExecutionFreeze(t, want.freeze)
			changed := admittedExecutionFreezeTestBinding(t, plan, commits)
			changed.freeze.Tools[0].Version = "changed"
			changed.freeze.Profile.Commands[0].NormalizedArgv[0] = "changed"
			changed.freeze.Profile.Epochs[0].Phases[0] = "changed"
			changed.freeze.Profile.Config.EnabledExtractorDomains[0] = "changed"
			changed.freeze.Pressure.Targets[0].ExpectedDisposition = "changed"
			changed.expectedCommits.CleanTree = false
			changed.planSHA256 = "changed"
			if again := admittedExecutionFreezeTestBinding(t, plan, commits); !reflect.DeepEqual(want, again) {
				t.Fatal("returned binding shares mutable cache data")
			}
			freeze := mustExecutionFreeze(t, plan, commits)
			if !reflect.DeepEqual(want.freeze, freeze) {
				t.Fatal("freeze wrapper did not use the admitted exact binding")
			}
			freeze.Profile.Epochs[0].Phases[0] = "changed-through-freeze-wrapper"
			if again := mustExecutionFreeze(t, plan, commits); !reflect.DeepEqual(want.freeze, again) {
				t.Fatal("freeze wrapper exposed mutable cache data")
			}
		})
	}
}

func TestFixtureAdmissionCacheRejectsUncleanKey(t *testing.T) {
	plans := []Plan{frozenTestPlan(t), correctedTestPlan(t)}
	commits := executionFreezeTestCommits()
	for _, plan := range plans {
		t.Run(plan.Schema, func(t *testing.T) {
			want := admittedExecutionFreezeTestBinding(t, plan, commits)
			unclean := commits
			unclean.CleanTree = false
			if _, err := executionFreezeAdmissionCache.binding(t, plan, unclean); err == nil {
				t.Fatal("unclean input reused an admitted clean-tree cache entry")
			}
			key := executionFreezeTestKey{PlanSHA256: mustReceiptSHA256(t, plan), Commits: unclean}
			if _, retained := executionFreezeAdmissionCache.bindings[key]; retained {
				t.Fatal("failed admission was retained as a usable binding")
			}
			if again := admittedExecutionFreezeTestBinding(t, plan, commits); !reflect.DeepEqual(want, again) {
				t.Fatal("failed admission changed the previously admitted binding")
			}
		})
	}
}

func TestFixtureAdmissionPublicBoundaryRejectsReadmittedDrift(t *testing.T) {
	plans := []Plan{frozenTestPlan(t), correctedTestPlan(t)}
	commits := executionFreezeTestCommits()
	for _, plan := range plans {
		t.Run(plan.Schema, func(t *testing.T) {
			binding := admittedExecutionFreezeTestBinding(t, plan, commits)
			for _, test := range []string{"wrong_epoch_profile", "unverified_checkout", "unverified_signature"} {
				t.Run(test, func(t *testing.T) {
					freeze := cloneExecutionFreeze(t, binding.freeze)
					checkout := executionFreezeTestCheckout(t, commits, freeze.Tools)
					profile := executionProfileTestAdmission(t, plan, freeze.Tools, freeze.Host)
					if test == "wrong_epoch_profile" {
						freeze.Profile.Epochs[1].ServerEpoch++
					}
					// Recompute the signature fixture over the changed freeze. The
					// public profile/checkout checks must remain independent of it.
					admission := executionFreezeTestAdmission(t, plan, freeze)
					if test == "unverified_checkout" {
						checkout.verified = false
					}
					if test == "unverified_signature" {
						admission.signatureVerified = false
					}
					if _, err := BindExecutionFreezeForReceipt(
						freeze, plan, commits, executionFreezeTestSigner(), checkout, profile, admission,
					); err == nil {
						t.Fatal("public admission accepted invalid fixture evidence")
					}
				})
			}
		})
	}
}

func assertFixtureAdmissionIdentity(t *testing.T, binding ExecutionFreezeBinding, plan Plan, commits ExecutionCommits) {
	t.Helper()
	planSHA256 := mustReceiptSHA256(t, plan)
	if binding.planSHA256 != planSHA256 || binding.freeze.PlanSHA256 != planSHA256 ||
		binding.expectedCommits != commits || binding.freeze.Commits != commits ||
		binding.freezeSHA256 != mustReceiptSHA256(t, binding.freeze) ||
		binding.expectedSignerFingerprint != executionFreezeTestSigner() ||
		binding.freeze.SignerFingerprint != executionFreezeTestSigner() ||
		binding.admissionEventOrdinal != 1 || !validDigest(binding.admissionEventSHA256) {
		t.Fatal("fixture binding does not belong to the exact supplied inputs")
	}
}
