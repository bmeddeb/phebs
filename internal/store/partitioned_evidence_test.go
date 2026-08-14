package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/candidateid"
	surrealdb "github.com/surrealdb/surrealdb.go"
)

func setStagedFactCount(
	t *testing.T,
	s *Surreal,
	ctx context.Context,
	runID string,
	facts int64,
) {
	t.Helper()
	results, err := surrealdb.Query[[]extractionRunRec](ctx, s.db,
		"UPDATE $rid SET staged_fact_count = $facts RETURN AFTER",
		map[string]any{"rid": extractionRunID(runID), "facts": facts})
	if err != nil || len(firstExtractionRows(results)) != 1 {
		t.Fatalf("seed staged fact count = %v, %v", results, err)
	}
}

func TestPartitionedEvidenceRunStagesBeyondLegacyFactCap(t *testing.T) {
	s := newRetentionTestStore(t)
	ctx := t.Context()
	const (
		repository = "synthetic.invalid/t4010-aggregate"
		commit     = "cccccccccccccccccccccccccccccccccccccccc"
		domain     = "proto-contract"
	)
	planDigest := "sha256:" + strings.Repeat("8", 64)
	run, err := s.BeginPartitionedExtractionRun(ctx, ExtractionScope{
		Repository: repository, Commit: commit, Domain: domain,
	}, "t40.10-aggregate-v1", planDigest, "sha256:"+strings.Repeat("7", 64), "",
		PartitionedExtractionRunLimits{
			Facts:      maxEvidenceFactsPerRun + 1,
			Rows:       maxEvidenceRowsPerRun + 2,
			References: maxEvidenceReferenceEdges + 1,
		})
	if err != nil {
		t.Fatal(err)
	}
	setStagedFactCount(t, s, ctx, run.ID, maxEvidenceFactsPerRun)
	atoms, associations, assertions := t407Batch(repository, commit, 0)
	chunk := "sha256:" + strings.Repeat("9", 64)
	if err := s.AddEvidenceChunk(ctx, run.ID, chunk, 1, atoms, associations, assertions); err != nil {
		t.Fatalf("partition aggregate append above legacy fact cap: %v", err)
	}
	receipt, err := s.GetEvidenceChunkAccounting(ctx, run.ID, chunk)
	if err != nil || receipt.FactCount != 1 {
		t.Fatalf("aggregate chunk receipt = %+v, %v", receipt, err)
	}
}

func TestAbortedPartitionedEvidenceRunReleasesLifecyclePin(t *testing.T) {
	s := newRetentionTestStore(t)
	ctx := t.Context()
	run, err := s.BeginPartitionedExtractionRun(ctx, ExtractionScope{
		Repository: "synthetic.invalid/t4010-abort",
		Commit:     "dddddddddddddddddddddddddddddddddddddddd",
		Domain:     "proto-contract",
	}, "t40.10-abort-v1", "sha256:"+strings.Repeat("a", 64),
		"sha256:"+strings.Repeat("b", 64), "", PartitionedExtractionRunLimits{
			Facts: 1, Rows: 2, References: 1,
		})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AbortExtractionRun(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	aborted, err := s.getRun(ctx, run.ID)
	if err != nil || aborted.PartitionActive {
		t.Fatalf("aborted partition run = %+v, %v", aborted, err)
	}
	progress, err := s.SweepEvidence(ctx, time.Now().UTC().Add(48*time.Hour), 24*time.Hour)
	if err != nil || progress.RunsMarkedDeleting != 1 {
		t.Fatalf("aborted partition run sweep = %+v, %v", progress, err)
	}
}

func TestCandidateTransitionAbortsOnlyActivePartitionRun(t *testing.T) {
	s := newRetentionTestStore(t)
	ctx := t.Context()
	const (
		repository = "synthetic.invalid/t4010-candidate-transition"
		commit     = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
		domain     = "proto-contract"
	)
	if err := s.UpsertRepo(ctx, Repo{
		Name: repository, CloneURL: "https://example.invalid/t4010-transition.git",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoIndexed(ctx, repository, commit, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	candidateA := CandidateManifestPublication{
		Repository: repository, HeadCommit: commit,
		PolicyDigest:     "sha256:" + strings.Repeat("1", 64),
		ManifestDigest:   "sha256:" + strings.Repeat("2", 64),
		GenerationDigest: "sha256:" + strings.Repeat("3", 64),
		ManifestPath:     candidateid.ManifestName(repository),
	}
	if err := s.PublishCandidateManifest(ctx, candidateA); err != nil {
		t.Fatal(err)
	}
	run, err := s.BeginPartitionedExtractionRun(ctx, ExtractionScope{
		Repository: repository, Commit: commit, Domain: domain,
	}, "t40.10-transition-v1", "sha256:"+strings.Repeat("4", 64),
		candidateA.ManifestDigest, "", PartitionedExtractionRunLimits{Facts: 1, Rows: 2, References: 1})
	if err != nil {
		t.Fatal(err)
	}
	candidateB := candidateA
	candidateB.PolicyDigest = "sha256:" + strings.Repeat("5", 64)
	candidateB.ManifestDigest = "sha256:" + strings.Repeat("6", 64)
	candidateB.GenerationDigest = "sha256:" + strings.Repeat("7", 64)
	if err := s.PublishCandidateManifest(ctx, candidateB); err != nil {
		t.Fatal(err)
	}
	aborted, err := s.getRun(ctx, run.ID)
	if err != nil || aborted.Status != "aborted" || aborted.PartitionActive || aborted.PartitionSealed {
		t.Fatalf("candidate-invalidated partition run = %+v, %v", aborted, err)
	}
	progress, err := s.SweepEvidence(ctx, time.Now().UTC().Add(48*time.Hour), 24*time.Hour)
	if err != nil || progress.RunsMarkedDeleting != 1 {
		t.Fatalf("candidate-invalidated partition sweep = %+v, %v", progress, err)
	}
}

func TestPartitionedEvidencePublicationSealsExactAccountedRun(t *testing.T) {
	s := newRetentionTestStore(t)
	ctx := t.Context()
	const (
		repository = "synthetic.invalid/t4010-publication"
		commit     = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		domain     = "proto-contract"
		chunk      = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	)
	const candidateDigest = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	const planDigest = "sha256:3333333333333333333333333333333333333333333333333333333333333333"
	if err := s.UpsertRepo(ctx, Repo{Name: repository, CloneURL: "https://example.invalid/t4010.git"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoIndexed(ctx, repository, commit, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	candidatePublication := CandidateManifestPublication{
		Repository: repository, HeadCommit: commit,
		PolicyDigest:     "sha256:" + strings.Repeat("a", 64),
		ManifestDigest:   candidateDigest,
		GenerationDigest: "sha256:" + strings.Repeat("b", 64),
		ManifestPath:     candidateid.ManifestName(repository),
	}
	if err := s.PublishCandidateManifest(ctx, candidatePublication); err != nil {
		t.Fatal(err)
	}
	run, err := s.BeginPartitionedExtractionRun(ctx, ExtractionScope{
		Repository: repository, Commit: commit, Domain: domain,
	}, "t40.7-test-v1", planDigest, candidateDigest, "", PartitionedExtractionRunLimits{
		Facts: MaxPartitionedFacts, Rows: MaxPartitionedRows,
		References: MaxPartitionedReferences,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !run.PartitionActive || run.PartitionSealed || run.PartitionPlanDigest != planDigest ||
		run.PartitionCandidateDigest != candidateDigest ||
		run.PartitionFactLimit != MaxPartitionedFacts || run.PartitionRowLimit != MaxPartitionedRows ||
		run.PartitionReferenceLimit != MaxPartitionedReferences {
		t.Fatalf("partition run authority = %+v", run)
	}
	progress, err := s.SweepEvidence(ctx, time.Now().UTC().Add(48*time.Hour), 24*time.Hour)
	if err != nil || progress.RunsMarkedDeleting != 0 {
		t.Fatalf("active partition run sweep = %+v, %v", progress, err)
	}
	atoms, associations, assertions := t407Batch(repository, commit, 0)
	if err := s.AddEvidenceChunk(ctx, run.ID, chunk, 1, atoms, associations, assertions); err != nil {
		t.Fatal(err)
	}
	receipt, err := s.GetEvidenceChunkAccounting(ctx, run.ID, chunk)
	if err != nil || receipt.FactCount != 1 || receipt.RowDelta != 2 || receipt.ReferenceDelta != 1 {
		t.Fatalf("chunk accounting = %+v, %v", receipt, err)
	}
	publication := PartitionedExtractionDomain{
		Schema:     PartitionedExtractionDomainSchema,
		Repository: repository, Domain: domain, RunID: run.ID,
		PlanDigest:        planDigest,
		RootDigest:        "sha256:" + strings.Repeat("4", 64),
		CandidateDigest:   candidateDigest,
		SourceDigest:      "sha256:" + strings.Repeat("5", 64),
		ObservationDigest: "sha256:" + strings.Repeat("6", 64),
		Facts:             1, Rows: 2, References: 1, Plan: `{}`, Root: `{}`,
	}
	if err := s.PublishPartitionedExtractionDomain(ctx, publication); err != nil {
		t.Fatal(err)
	}
	sealed, err := s.getRun(ctx, run.ID)
	if err != nil || sealed.PartitionActive || !sealed.PartitionSealed {
		t.Fatalf("sealed partition run = %+v, %v", sealed, err)
	}
	if err := s.PublishPartitionedExtractionDomain(ctx, publication); err != nil {
		t.Fatalf("exact publication replay: %v", err)
	}
	stored, err := s.GetPartitionedExtractionDomain(ctx, repository, domain)
	if err != nil || !samePartitionedDomain(*stored, publication) {
		t.Fatalf("stored publication = %+v, %v", stored, err)
	}
	secondChunk := "sha256:" + strings.Repeat("7", 64)
	if err := s.AddEvidenceChunk(ctx, run.ID, secondChunk, 1, atoms, associations, assertions); !errors.Is(err, ErrConflict) {
		t.Fatalf("append after partition seal = %v, want conflict", err)
	}
	planDigestB := "sha256:" + strings.Repeat("c", 64)
	runB, err := s.BeginPartitionedExtractionRun(ctx, ExtractionScope{
		Repository: repository, Commit: commit, Domain: domain,
	}, "t40.7-test-v1", planDigestB, candidateDigest, "", PartitionedExtractionRunLimits{
		Facts: MaxPartitionedFacts, Rows: MaxPartitionedRows,
		References: MaxPartitionedReferences,
	})
	if err != nil {
		t.Fatal(err)
	}
	atomsB, associationsB, assertionsB := t407Batch(repository, commit, 1)
	chunkB := "sha256:" + strings.Repeat("8", 64)
	if err := s.AddEvidenceChunk(ctx, runB.ID, chunkB, 1, atomsB, associationsB, assertionsB); err != nil {
		t.Fatal(err)
	}
	publicationB := publication
	publicationB.RunID = runB.ID
	publicationB.PlanDigest = planDigestB
	publicationB.RootDigest = "sha256:" + strings.Repeat("d", 64)
	publicationB.Plan = `{"generation":"b"}`
	publicationB.Root = `{"generation":"b"}`
	if err := s.PublishPartitionedExtractionDomain(ctx, publicationB); err != nil {
		t.Fatalf("publish B: %v", err)
	}
	storedB, err := s.GetPartitionedExtractionDomain(ctx, repository, domain)
	if err != nil || storedB.PriorRunID != run.ID ||
		storedB.PriorPlanDigest != publication.PlanDigest || storedB.PriorRootDigest != publication.RootDigest {
		t.Fatalf("B rollback floor = %+v, %v", storedB, err)
	}
	if err := s.PublishPartitionedExtractionDomain(ctx, publication); err != nil {
		t.Fatalf("reactivate A: %v", err)
	}
	reactivated, err := s.GetPartitionedExtractionDomain(ctx, repository, domain)
	if err != nil || reactivated.RunID != run.ID || reactivated.RootDigest != publication.RootDigest ||
		reactivated.PriorRunID != runB.ID || reactivated.PriorPlanDigest != publicationB.PlanDigest ||
		reactivated.PriorRootDigest != publicationB.RootDigest {
		t.Fatalf("reactivated A = %+v, %v", reactivated, err)
	}
	owner := "relationship:sha256:" + strings.Repeat("f", 64)
	if err := s.PinPartitionedExtractionRun(ctx, runB.ID, owner); err != nil {
		t.Fatalf("pin rollback run B: %v", err)
	}
	if err := s.ReconcilePartitionedExtractionOwners(ctx, []string{owner}); err != nil {
		t.Fatalf("retain relationship pin owner: %v", err)
	}

	replacement := candidatePublication
	replacement.PolicyDigest = "sha256:" + strings.Repeat("e", 64)
	replacement.ManifestDigest = "sha256:" + strings.Repeat("9", 64)
	replacement.GenerationDigest = "sha256:" + strings.Repeat("0", 64)
	if err := s.PublishCandidateManifest(ctx, replacement); err != nil {
		t.Fatal(err)
	}
	sealedAfterCandidateTransition, err := s.getRun(ctx, run.ID)
	if err != nil || sealedAfterCandidateTransition.Status != "staged" ||
		sealedAfterCandidateTransition.PartitionActive || !sealedAfterCandidateTransition.PartitionSealed {
		t.Fatalf("sealed partition run after candidate transition = %+v, %v", sealedAfterCandidateTransition, err)
	}
	if err := s.PublishPartitionedExtractionDomain(ctx, publication); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale candidate publication = %v, want conflict", err)
	}
	stillCurrent, err := s.GetPartitionedExtractionDomain(ctx, repository, domain)
	if err != nil || stillCurrent.RootDigest != publication.RootDigest || stillCurrent.RunID != publication.RunID {
		t.Fatalf("current after stale replacement = %+v, %v", stillCurrent, err)
	}

	planDigestC := "sha256:" + strings.Repeat("1", 64)
	runC, err := s.BeginPartitionedExtractionRun(ctx, ExtractionScope{
		Repository: repository, Commit: commit, Domain: domain,
	}, "t40.11-lifecycle-v1", planDigestC, replacement.ManifestDigest,
		"", PartitionedExtractionRunLimits{})
	if err != nil {
		t.Fatal(err)
	}
	publicationC := publication
	publicationC.RunID = runC.ID
	publicationC.PlanDigest = planDigestC
	publicationC.RootDigest = "sha256:" + strings.Repeat("2", 64)
	publicationC.CandidateDigest = replacement.ManifestDigest
	publicationC.Facts, publicationC.Rows, publicationC.References = 0, 0, 0
	publicationC.Plan = `{"generation":"c"}`
	publicationC.Root = `{"generation":"c"}`
	if err := s.PublishPartitionedExtractionDomain(ctx, publicationC); err != nil {
		t.Fatalf("publish C: %v", err)
	}
	storedC, err := s.GetPartitionedExtractionDomain(ctx, repository, domain)
	if err != nil || storedC.PriorRunID != run.ID {
		t.Fatalf("C rollback floor = %+v, %v", storedC, err)
	}
	if released, err := s.ReleaseOneUnrootedPartitionRun(ctx); err != nil || released {
		t.Fatalf("pinned unrooted B release = %t, %v", released, err)
	}
	if err := s.ReconcilePartitionedExtractionOwners(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if released, err := s.ReleaseOneUnrootedPartitionRun(ctx); err != nil || !released {
		t.Fatalf("unpinned unrooted B release = %t, %v", released, err)
	}
	releasedB, err := s.getRun(ctx, runB.ID)
	if err != nil || releasedB.PartitionSealed {
		t.Fatalf("released B = %+v, %v", releasedB, err)
	}
}

func TestPartitionedEvidencePublicationAcceptsExactZeroRun(t *testing.T) {
	s := newRetentionTestStore(t)
	ctx := t.Context()
	const (
		repository = "synthetic.invalid/t4010-empty"
		commit     = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		domain     = "proto-contract"
	)
	candidateDigest := "sha256:" + strings.Repeat("1", 64)
	if err := s.UpsertRepo(ctx, Repo{
		Name: repository, CloneURL: "https://example.invalid/t4010-empty.git",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoIndexed(ctx, repository, commit, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishCandidateManifest(ctx, CandidateManifestPublication{
		Repository: repository, HeadCommit: commit,
		PolicyDigest:     "sha256:" + strings.Repeat("2", 64),
		ManifestDigest:   candidateDigest,
		GenerationDigest: "sha256:" + strings.Repeat("3", 64),
		ManifestPath:     candidateid.ManifestName(repository),
	}); err != nil {
		t.Fatal(err)
	}
	planDigest := "sha256:" + strings.Repeat("4", 64)
	run, err := s.BeginPartitionedExtractionRun(ctx, ExtractionScope{
		Repository: repository, Commit: commit, Domain: domain,
	}, "t40.10-empty-v1", planDigest, candidateDigest, "", PartitionedExtractionRunLimits{})
	if err != nil {
		t.Fatal(err)
	}
	publication := PartitionedExtractionDomain{
		Schema:     PartitionedExtractionDomainSchema,
		Repository: repository, Domain: domain, RunID: run.ID,
		PlanDigest:        planDigest,
		RootDigest:        "sha256:" + strings.Repeat("5", 64),
		CandidateDigest:   candidateDigest,
		SourceDigest:      "sha256:" + strings.Repeat("6", 64),
		ObservationDigest: "sha256:" + strings.Repeat("7", 64),
		Plan:              `{}`, Root: `{}`,
	}
	if err := s.PublishPartitionedExtractionDomain(ctx, publication); err != nil {
		t.Fatal(err)
	}
	stored, err := s.GetPartitionedExtractionDomain(ctx, repository, domain)
	if err != nil || !samePartitionedDomain(*stored, publication) {
		t.Fatalf("empty publication = %+v, %v", stored, err)
	}
}

// TestPartitionedRunLimitsContractBindingDispatch is a pure unit guard on the
// store envelope's (domain, plan-schema) dispatch: the measured T40.R1
// kafka-producer v3 aggregate is admitted only with its exact binding, every
// other pair — including a v3 schema on another domain and a v3-sized
// reservation on a historical pair — keeps the v1/v2 maxima, and even the
// exact binding never exceeds the v3 ceilings.
func TestPartitionedRunLimitsContractBindingDispatch(t *testing.T) {
	v3 := PartitionedExtractionRunLimits{
		Facts: MaxPartitionedFactsV3, Rows: MaxPartitionedRowsV3, References: MaxPartitionedReferencesV3,
	}
	if err := v3.validate(PartitionedV3Domain, PartitionedPlanSchemaV3); err != nil {
		t.Fatalf("v3 reservation with exact binding rejected: %v", err)
	}
	historical := PartitionedExtractionRunLimits{
		Facts: MaxPartitionedFacts, Rows: MaxPartitionedRows, References: MaxPartitionedReferences,
	}
	if err := historical.validate("proto-contract", ""); err != nil {
		t.Fatalf("historical v1/v2 reservation rejected: %v", err)
	}
	cases := []struct {
		name           string
		domain, schema string
		limits         PartitionedExtractionRunLimits
	}{
		{"v3-sized without schema", PartitionedV3Domain, "", v3},
		{"v3-sized with historical schema", PartitionedV3Domain, "phebs-extraction-domain-result-plan-v2", v3},
		{"v3 binding on another domain", "proto-contract", PartitionedPlanSchemaV3, v3},
		{"one fact above historical", "proto-contract", "",
			PartitionedExtractionRunLimits{Facts: MaxPartitionedFacts + 1, Rows: MaxPartitionedRows, References: MaxPartitionedReferences}},
		{"one fact above v3", PartitionedV3Domain, PartitionedPlanSchemaV3,
			PartitionedExtractionRunLimits{Facts: MaxPartitionedFactsV3 + 1, Rows: MaxPartitionedRowsV3, References: MaxPartitionedReferencesV3}},
		{"one row above v3", PartitionedV3Domain, PartitionedPlanSchemaV3,
			PartitionedExtractionRunLimits{Facts: MaxPartitionedFactsV3, Rows: MaxPartitionedRowsV3 + 1, References: MaxPartitionedReferencesV3}},
		{"one reference above v3", PartitionedV3Domain, PartitionedPlanSchemaV3,
			PartitionedExtractionRunLimits{Facts: MaxPartitionedFactsV3, Rows: MaxPartitionedRowsV3, References: MaxPartitionedReferencesV3 + 1}},
	}
	for _, test := range cases {
		if err := test.limits.validate(test.domain, test.schema); err == nil {
			t.Fatalf("%s: invalid reservation accepted", test.name)
		}
	}
}

// TestPartitionedPublicationEnvelopeRequiresExactV3Binding pins the published
// domain envelope: totals above the historical v1/v2 maxima are admitted only
// when the retained canonical plan bytes themselves carry the exact
// kafka-producer v3 schema. Persisted historical controls within the v1/v2
// maxima never pay or need that proof.
func TestPartitionedPublicationEnvelopeRequiresExactV3Binding(t *testing.T) {
	base := PartitionedExtractionDomain{
		Schema:            PartitionedExtractionDomainSchema,
		Repository:        "synthetic.invalid/t40r1-binding",
		Domain:            PartitionedV3Domain,
		RunID:             "run-binding",
		PlanDigest:        "sha256:" + strings.Repeat("1", 64),
		RootDigest:        "sha256:" + strings.Repeat("2", 64),
		CandidateDigest:   "sha256:" + strings.Repeat("3", 64),
		SourceDigest:      "sha256:" + strings.Repeat("4", 64),
		ObservationDigest: "sha256:" + strings.Repeat("5", 64),
		Facts:             MaxPartitionedFactsV3,
		Rows:              MaxPartitionedRowsV3,
		References:        MaxPartitionedReferencesV3,
		Plan:              `{"schema":"` + PartitionedPlanSchemaV3 + `","limits":{}}`,
		Root:              `{}`,
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("v3 publication with exact plan-byte binding rejected: %v", err)
	}
	historical := base
	historical.Domain = "proto-contract"
	historical.Facts, historical.Rows, historical.References =
		MaxPartitionedFacts, MaxPartitionedRows, MaxPartitionedReferences
	historical.Plan = `{}`
	if err := historical.Validate(); err != nil {
		t.Fatalf("historical v1/v2-sized control rejected: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*PartitionedExtractionDomain)
	}{
		{"v3 totals on another domain", func(p *PartitionedExtractionDomain) {
			p.Domain = "proto-contract"
		}},
		{"v3 totals with historical plan schema bytes", func(p *PartitionedExtractionDomain) {
			p.Plan = `{"schema":"phebs-extraction-domain-result-plan-v2","limits":{}}`
		}},
		{"v3 totals with schema-less plan bytes", func(p *PartitionedExtractionDomain) {
			p.Plan = `{}`
		}},
		{"one fact above v3 with exact binding", func(p *PartitionedExtractionDomain) {
			p.Facts = MaxPartitionedFactsV3 + 1
		}},
		{"one row above v3 with exact binding", func(p *PartitionedExtractionDomain) {
			p.Rows = MaxPartitionedRowsV3 + 1
		}},
		{"one reference above v3 with exact binding", func(p *PartitionedExtractionDomain) {
			p.References = MaxPartitionedReferencesV3 + 1
		}},
	}
	for _, test := range cases {
		mutated := base
		test.mutate(&mutated)
		if err := mutated.Validate(); err == nil {
			t.Fatalf("%s: invalid publication accepted", test.name)
		}
	}
}

// TestPartitionedV3BoundRunStagesThroughExactBinding proves the whole run
// path on the real store: a v3-sized reservation begins only with the exact
// (kafka-producer, v3) pair, the persisted schema round-trips, and staged
// chunks pass the second-line authority check under the raised ceilings.
func TestPartitionedV3BoundRunStagesThroughExactBinding(t *testing.T) {
	s := newRetentionTestStore(t)
	ctx := t.Context()
	const (
		repository = "synthetic.invalid/t40r1-v3-binding"
		commit     = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	v3Limits := PartitionedExtractionRunLimits{
		Facts: MaxPartitionedFactsV3, Rows: MaxPartitionedRowsV3, References: MaxPartitionedReferencesV3,
	}
	if _, err := s.BeginPartitionedExtractionRun(ctx, ExtractionScope{
		Repository: repository, Commit: commit, Domain: PartitionedV3Domain,
	}, "t40.r1-v3-v1", "sha256:"+strings.Repeat("1", 64),
		"sha256:"+strings.Repeat("2", 64), "", v3Limits); err == nil {
		t.Fatal("v3-sized reservation without the plan schema was admitted")
	}
	if _, err := s.BeginPartitionedExtractionRun(ctx, ExtractionScope{
		Repository: repository, Commit: commit, Domain: "proto-contract",
	}, "t40.r1-v3-v1", "sha256:"+strings.Repeat("1", 64),
		"sha256:"+strings.Repeat("2", 64), PartitionedPlanSchemaV3, v3Limits); err == nil {
		t.Fatal("v3-sized reservation on another domain was admitted")
	}
	run, err := s.BeginPartitionedExtractionRun(ctx, ExtractionScope{
		Repository: repository, Commit: commit, Domain: PartitionedV3Domain,
	}, "t40.r1-v3-v1", "sha256:"+strings.Repeat("1", 64),
		"sha256:"+strings.Repeat("2", 64), PartitionedPlanSchemaV3, v3Limits)
	if err != nil {
		t.Fatal(err)
	}
	if run.PartitionPlanSchema != PartitionedPlanSchemaV3 ||
		run.PartitionFactLimit != MaxPartitionedFactsV3 ||
		run.PartitionRowLimit != MaxPartitionedRowsV3 ||
		run.PartitionReferenceLimit != MaxPartitionedReferencesV3 {
		t.Fatalf("v3 partition run authority = %+v", run)
	}
	reread, err := s.getRun(ctx, run.ID)
	if err != nil || reread.PartitionPlanSchema != PartitionedPlanSchemaV3 {
		t.Fatalf("persisted v3 schema = %+v, %v", reread, err)
	}
	atoms, associations, assertions := t407Batch(repository, commit, 0)
	chunk := "sha256:" + strings.Repeat("9", 64)
	if err := s.AddEvidenceChunk(ctx, run.ID, chunk, 1, atoms, associations, assertions); err != nil {
		t.Fatalf("v3-bound run staging: %v", err)
	}
	receipt, err := s.GetEvidenceChunkAccounting(ctx, run.ID, chunk)
	if err != nil || receipt.FactCount != 1 || receipt.RowDelta != 2 || receipt.ReferenceDelta != 1 {
		t.Fatalf("v3-bound chunk accounting = %+v, %v", receipt, err)
	}
}
