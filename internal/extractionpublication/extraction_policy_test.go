package extractionpublication

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/store"
)

// The general fixture reuses a domain-only run ID; model distinct durable
// Begin calls here so the test can detect accidental old-run reuse.
type policyRunStore struct{ testExtractionRunStore }

func (state *policyRunStore) BeginPartitionedExtractionRun(
	ctx context.Context, scope store.ExtractionScope, extractor, plan, candidateDigest, schema string,
	limits store.PartitionedExtractionRunLimits,
) (*store.ExtractionRun, error) {
	run, err := state.testExtractionRunStore.BeginPartitionedExtractionRun(ctx, scope, extractor, plan, candidateDigest, schema, limits)
	if err == nil {
		run.ID = plan
	}
	return run, err
}

func TestReconcilerExtractionPolicyIsolatesDurableRuns(t *testing.T) {
	repository := "example.invalid/grouping"
	fixture := buildCandidateFixtureAt(t, repository, filepath.Join(t.TempDir(), "repository"), t.TempDir())
	source := "sha256:" + strings.Repeat("1", 64)
	observation := "sha256:" + strings.Repeat("2", 64)
	runtime, _, _, _, _, _, _ := newRuntimeFixture(t, buildTestPlan(t, source, true))
	runtime.Root = t.TempDir()
	evidence := &policyRunStore{}
	reconciler := &Reconciler{
		Root: runtime.Root, CandidateRoot: fixture.candidateDirectory, Runtime: runtime, Evidence: evidence,
		OpenCandidate:      func(context.Context, string) (*candidate.Publication, error) { return fixture.publication, nil },
		CandidateReference: func(context.Context, string) (candidate.State, error) { return fixture.publication.State(), nil },
		Authority:          func(context.Context, string) (string, string, error) { return source, observation, nil },
		AuthorityReference: func(context.Context, string) (string, string, error) { return source, observation, nil },
	}
	var generations [2]string
	var domains [2]DomainPlan
	for index, selected := range []bool{false, true} {
		reconciler.StoreAccounting = selected
		generation, err := reconciler.Reconcile(t.Context(), repository)
		if err != nil {
			t.Fatal(err)
		}
		generations[index] = generation
		directory := runtime.generationDirectory(repository, generation)
		control, err := runtime.openGeneration(directory, repository, generation)
		if err != nil {
			t.Fatal(err)
		}
		domains[index], err = runtime.openDomainPlan(directory, control.Domains[0])
		if err != nil {
			t.Fatal(err)
		}
		authority, err := authorityForPlans(repository, []DomainPlan{domains[index]})
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := canonical(authority)
		if bytes.Contains(raw, []byte("extraction_policy_digest")) != selected {
			t.Fatalf("policy omission does not preserve ordinary bytes: %s", raw)
		}
		if err := reconciler.confirmRecoveryReferences(t.Context(), authority); err != nil {
			t.Fatal(err)
		}
		reconciler.StoreAccounting = !selected
		if err := reconciler.confirmRecoveryReferences(t.Context(), authority); !errors.Is(err, ErrStale) {
			t.Fatalf("cross-policy recovery = %v", err)
		}
		reconciler.StoreAccounting = selected
		again, err := reconciler.Reconcile(t.Context(), repository)
		if err != nil || again != generation {
			t.Fatalf("same-policy replay = %s, %v", again, err)
		}
		if begins, _ := evidence.counts(); begins != index+1 {
			t.Fatalf("run begins = %d", begins)
		}
	}
	if generations[0] == generations[1] || domains[0].RunID == domains[1].RunID ||
		domains[0].Plan.Digest == domains[1].Plan.Digest ||
		domains[0].Plan.CandidateManifestDigest != domains[1].Plan.CandidateManifestDigest {
		t.Fatalf("policies share durable authority: %+v", domains)
	}
	if _, err := authorityForPlans(repository, domains[:]); err == nil {
		t.Fatal("mixed extraction policies admitted as one authority")
	}
	// Both still-incomplete execution generations retain their own run on mode switches.
	for index, selected := range []bool{false, true} {
		reconciler.StoreAccounting = selected
		again, err := reconciler.Reconcile(t.Context(), repository)
		if err != nil || again != generations[index] {
			t.Fatalf("partial reuse = %s, %v", again, err)
		}
	}
	if begins, _ := evidence.counts(); begins != 2 {
		t.Fatalf("partial replay created %d runs", begins)
	}
}
