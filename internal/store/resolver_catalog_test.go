package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/resolvercatalog"
	"github.com/bmeddeb/phebs/internal/resolvercatalogid"
	"github.com/bmeddeb/phebs/internal/store"
)

func resolverPublication(
	repository, commit, candidateManifestDigest string,
) store.ResolverCatalogPublication {
	identity, err := resolvercatalog.NewIdentity(
		repository, commit, "", candidateManifestDigest, nil, nil,
	)
	if err != nil {
		panic(err)
	}
	return store.ResolverCatalogPublication{
		Repository: repository, HeadCommit: commit,
		Declarations:            []store.ResolverCatalogDeclarationPublication{},
		DeclarationSetDigest:    identity.DeclarationSetDigest,
		CandidateManifestDigest: candidateManifestDigest,
		SourceLanePolicy:        resolvercatalog.SourceLanePolicy,
		ResolverPacks:           []store.ResolverCatalogPack{},
		ResolverPackSetDigest:   identity.ResolverPackSetDigest,
		CatalogPolicyDigest:     identity.CatalogPolicyDigest,
		GenerationDigest:        identity.GenerationDigest,
		ManifestDigest:          candidateDigest('2'),
		ManifestPath:            resolvercatalogid.ManifestName(repository),
	}
}

func testResolverCatalogStoreLifecycle(t *testing.T, s *store.Surreal) {
	t.Helper()
	t.Run("guarded publication", func(t *testing.T) {
		testResolverCatalogGuardedPublicationLifecycle(t, s)
	})
	t.Run("candidate and repo retirement", func(t *testing.T) {
		testResolverCatalogRetiredByCandidateAndRepoTransitions(t, s)
	})
	t.Run("exact declarations", func(t *testing.T) {
		testResolverCatalogAcceptsExactPublishedDeclarationSet(t, s)
	})
	t.Run("restore clear and queue kind", func(t *testing.T) {
		testResolverCatalogRestoreClearAndQueueKind(t, s)
	})
}

func testResolverCatalogGuardedPublicationLifecycle(
	t *testing.T, s *store.Surreal,
) {
	ctx := context.Background()
	repository := "github.com/acme/resolver-store"
	commit := candidateCommit('1')
	candidate := candidatePublication(repository, commit, "")
	catalog := resolverPublication(repository, commit, candidate.ManifestDigest)

	if err := s.PublishResolverCatalog(ctx, catalog); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("publish without repo = %v, want ErrConflict", err)
	}
	if err := s.UpsertRepo(ctx, store.Repo{
		Name: repository, CloneURL: "https://" + repository + ".git",
	}); err != nil {
		t.Fatal(err)
	}
	setCandidateIndexedState(t, ctx, s, repository, commit, nil, nil)
	if err := s.PublishResolverCatalog(ctx, catalog); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("publish without candidate = %v, want ErrConflict", err)
	}
	if err := s.PublishCandidateManifest(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	missingDeclaration := resolvercatalog.DeclarationPublication{
		Domain: "proto-contract", RunID: "missing-run",
		GenerationDigest: "extraction_generation_v1_" + strings.Repeat("7", 64),
	}
	missingIdentity, err := resolvercatalog.NewIdentity(
		repository, commit, "", candidate.ManifestDigest,
		[]resolvercatalog.DeclarationPublication{missingDeclaration}, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	missing := catalog
	missing.Declarations = []store.ResolverCatalogDeclarationPublication{{
		Domain: missingDeclaration.Domain, RunID: missingDeclaration.RunID,
		GenerationDigest: missingDeclaration.GenerationDigest,
	}}
	missing.DeclarationSetDigest = missingIdentity.DeclarationSetDigest
	missing.GenerationDigest = missingIdentity.GenerationDigest
	missing.ManifestDigest = candidateDigest('8')
	if err := s.PublishResolverCatalog(ctx, missing); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("publish missing declaration authority = %v, want ErrConflict", err)
	}
	if err := s.PublishResolverCatalog(ctx, catalog); err != nil {
		t.Fatal(err)
	}
	first, err := s.GetResolverCatalogPublication(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	if first.ControlRevision != 1 ||
		first.WriterSchema != "phebs-resolver-catalog-store-v1" ||
		first.PublishedAt.IsZero() {
		t.Fatalf("published pointer = %+v", first)
	}
	if current, err := s.ResolverCatalogPublicationCurrent(
		ctx, *first,
	); err != nil || !current {
		t.Fatalf("current authority = %v, %v; want true", current, err)
	}

	catalog.PublishedAt = time.Unix(1, 0)
	if err := s.PublishResolverCatalog(ctx, catalog); err != nil {
		t.Fatalf("exact retry: %v", err)
	}
	exact, err := s.GetResolverCatalogPublication(ctx, repository)
	if err != nil || exact.ControlRevision != 1 ||
		!exact.PublishedAt.Equal(first.PublishedAt) {
		t.Fatalf("exact retry = %+v, %v; want unchanged", exact, err)
	}

	conflict := catalog
	conflict.ManifestDigest = candidateDigest('3')
	if err := s.PublishResolverCatalog(ctx, conflict); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("same-generation different manifest = %v, want ErrConflict", err)
	}
	next := catalog
	nextIdentity, err := resolvercatalog.NewIdentity(
		repository, commit, "", candidate.ManifestDigest, nil,
		[]resolvercatalog.ResolverPack{{Name: "neutral", Version: "1.0.0"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	next.ResolverPacks = []store.ResolverCatalogPack{{
		Name: "neutral", Version: "1.0.0",
	}}
	next.ResolverPackSetDigest = nextIdentity.ResolverPackSetDigest
	next.GenerationDigest = nextIdentity.GenerationDigest
	next.ManifestDigest = candidateDigest('6')
	if err := s.PublishResolverCatalog(ctx, next); err != nil {
		t.Fatalf("next generation: %v", err)
	}
	replaced, err := s.GetResolverCatalogPublication(ctx, repository)
	if err != nil || replaced.ControlRevision != 2 {
		t.Fatalf("replacement = %+v, %v; want revision 2", replaced, err)
	}
}

func testResolverCatalogRetiredByCandidateAndRepoTransitions(
	t *testing.T, s *store.Surreal,
) {
	ctx := context.Background()
	repository := "github.com/acme/resolver-retire"
	commit := candidateCommit('1')
	if err := s.UpsertRepo(ctx, store.Repo{
		Name: repository, CloneURL: "https://" + repository + ".git",
	}); err != nil {
		t.Fatal(err)
	}
	setCandidateIndexedState(t, ctx, s, repository, commit, nil, nil)
	candidate := candidatePublication(repository, commit, "")
	if err := s.PublishCandidateManifest(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	catalog := resolverPublication(repository, commit, candidate.ManifestDigest)
	if err := s.PublishResolverCatalog(ctx, catalog); err != nil {
		t.Fatal(err)
	}
	candidate.ControlRevision = 2
	if err := s.PublishCandidateManifest(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetResolverCatalogPublication(ctx, repository); err != nil {
		t.Fatalf("catalog after byte-identical candidate control transition: %v", err)
	}
	if err := s.ClearCandidateManifestPublication(ctx, repository); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetResolverCatalogPublication(ctx, repository); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("catalog after candidate clear = %v, want not found", err)
	}
	candidate.ControlRevision = 0
	if err := s.PublishCandidateManifest(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishResolverCatalog(ctx, catalog); err != nil {
		t.Fatal(err)
	}
	nextCommit := candidateCommit('2')
	setCandidateIndexedState(t, ctx, s, repository, nextCommit, nil, nil)
	if _, err := s.GetResolverCatalogPublication(ctx, repository); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("catalog after repo transition = %v, want not found", err)
	}
}

func testResolverCatalogAcceptsExactPublishedDeclarationSet(
	t *testing.T, s *store.Surreal,
) {
	ctx := context.Background()
	repository := "github.com/acme/resolver-declarations"
	commit := candidateCommit('1')
	if err := s.UpsertRepo(ctx, store.Repo{
		Name: repository, CloneURL: "https://" + repository + ".git",
	}); err != nil {
		t.Fatal(err)
	}
	setCandidateIndexedState(t, ctx, s, repository, commit, nil, nil)
	candidate := candidatePublication(repository, commit, "")
	if err := s.PublishCandidateManifest(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	candidatePointer, err := s.GetCandidateManifestPublication(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	scope := store.ExtractionScope{
		Repository: repository, Commit: commit, Domain: "proto-contract",
	}
	run, err := s.BeginExtractionRun(ctx, scope, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	generation := store.ExtractionGenerationIdentity{
		CandidateManifestDigest:  candidatePointer.ManifestDigest,
		CandidatePolicyDigest:    candidatePointer.PolicyDigest,
		CandidateControlRevision: candidatePointer.ControlRevision,
		Extractor:                run.Extractor,
		InventoryPolicy:          "candidate-manifest-v4-neutral",
		DependencyDigest:         candidateDigest('9'),
	}
	generation.Digest = store.ComputeExtractionGenerationDigest(generation)
	outcome := store.ExtractionDomainOutcome{
		Scope: scope, Disposition: store.DomainOutcomePublished,
		Generation: generation, RunID: run.ID,
		ReceiptSchema: store.ExtractionOutcomeReceiptSchema,
		Receipt:       `{"schema":"` + store.ExtractionOutcomeReceiptSchema + `"}`,
	}
	emptyDigest := "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	coverage := store.CoverageManifest{
		SourceScopeDigest:       emptyDigest,
		CandidateManifestDigest: candidatePointer.ManifestDigest,
		ScopePosture:            "whole-repository",
		CandidatePlane:          "local",
		ScopeCorpusDigest:       emptyDigest,
		PlannedScopeDigest:      emptyDigest,
	}
	if err := s.PublishExtractionRunWithOutcome(
		ctx, run.ID, coverage, outcome,
	); err != nil {
		t.Fatalf("publish declaration: %v", err)
	}
	declaration := resolvercatalog.DeclarationPublication{
		Domain: scope.Domain, RunID: run.ID,
		GenerationDigest: generation.Digest,
	}
	identity, err := resolvercatalog.NewIdentity(
		repository, commit, "", candidatePointer.ManifestDigest,
		[]resolvercatalog.DeclarationPublication{declaration}, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	publication := resolverPublication(
		repository, commit, candidatePointer.ManifestDigest,
	)
	publication.Declarations = []store.ResolverCatalogDeclarationPublication{{
		Domain: declaration.Domain, RunID: declaration.RunID,
		GenerationDigest: declaration.GenerationDigest,
	}}
	publication.DeclarationSetDigest = identity.DeclarationSetDigest
	publication.GenerationDigest = identity.GenerationDigest
	publication.ManifestDigest = candidateDigest('8')
	if err := s.PublishResolverCatalog(ctx, publication); err != nil {
		t.Fatalf("publish catalog with exact declarations: %v", err)
	}
	nextRun, err := s.BeginExtractionRun(ctx, scope, "1.1.0")
	if err != nil {
		t.Fatal(err)
	}
	nextGeneration := generation
	nextGeneration.Extractor = nextRun.Extractor
	nextGeneration.DependencyDigest = candidateDigest('8')
	nextGeneration.Digest = store.ComputeExtractionGenerationDigest(nextGeneration)
	nextOutcome := outcome
	nextOutcome.RunID = nextRun.ID
	nextOutcome.Generation = nextGeneration
	if err := s.PublishExtractionRunWithOutcome(
		ctx, nextRun.ID, coverage, nextOutcome,
	); err != nil {
		t.Fatalf("replace declaration: %v", err)
	}
	if _, err := s.GetResolverCatalogPublication(
		ctx, repository,
	); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("catalog after declaration replacement = %v, want not found", err)
	}
	jobs, err := s.ListJobs(
		ctx, store.JobResolverCatalog, store.StatusPending,
	)
	jobs = resolverJobsForTarget(jobs, repository)
	if err != nil || len(jobs) != 1 || !jobs[0].Force {
		t.Fatalf("catalog replacement jobs = %+v, %v", jobs, err)
	}

	nextDeclaration := resolvercatalog.DeclarationPublication{
		Domain: scope.Domain, RunID: nextRun.ID,
		GenerationDigest: nextGeneration.Digest,
	}
	nextIdentity, err := resolvercatalog.NewIdentity(
		repository, commit, "", candidatePointer.ManifestDigest,
		[]resolvercatalog.DeclarationPublication{nextDeclaration}, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	publication.Declarations = []store.ResolverCatalogDeclarationPublication{{
		Domain: nextDeclaration.Domain, RunID: nextDeclaration.RunID,
		GenerationDigest: nextDeclaration.GenerationDigest,
	}}
	publication.DeclarationSetDigest = nextIdentity.DeclarationSetDigest
	publication.GenerationDigest = nextIdentity.GenerationDigest
	publication.ManifestDigest = candidateDigest('7')
	if err := s.PublishResolverCatalog(ctx, publication); err != nil {
		t.Fatalf("republish catalog for current declaration: %v", err)
	}

	terminalGeneration := nextGeneration
	terminalGeneration.DependencyDigest = candidateDigest('6')
	terminalGeneration.Digest = store.ComputeExtractionGenerationDigest(
		terminalGeneration,
	)
	terminal := nextOutcome
	terminal.Disposition = store.DomainOutcomeTerminalGenerationRefusal
	terminal.Generation = terminalGeneration
	terminal.RunID = ""
	if err := s.RecordExtractionDomainOutcome(ctx, terminal); err != nil {
		t.Fatalf("replace declaration with terminal outcome: %v", err)
	}
	if _, err := s.GetResolverCatalogPublication(
		ctx, repository,
	); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("catalog after terminal declaration replacement = %v, want not found", err)
	}
	jobs, err = s.ListJobs(
		ctx, store.JobResolverCatalog, store.StatusPending,
	)
	jobs = resolverJobsForTarget(jobs, repository)
	if err != nil || len(jobs) != 1 || !jobs[0].Force {
		t.Fatalf("terminal replacement jobs = %+v, %v", jobs, err)
	}
}

func testResolverCatalogRestoreClearAndQueueKind(
	t *testing.T, s *store.Surreal,
) {
	ctx := context.Background()
	repository := "github.com/acme/resolver-clear"
	commit := candidateCommit('1')
	if err := s.UpsertRepo(ctx, store.Repo{
		Name: repository, CloneURL: "https://" + repository + ".git",
	}); err != nil {
		t.Fatal(err)
	}
	setCandidateIndexedState(t, ctx, s, repository, commit, nil, nil)
	candidate := candidatePublication(repository, commit, "")
	if err := s.PublishCandidateManifest(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishResolverCatalog(
		ctx, resolverPublication(repository, commit, candidate.ManifestDigest),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnqueuePending(
		ctx, store.JobResolverCatalog, repository, true,
	); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.ListJobs(ctx, store.JobResolverCatalog, store.StatusPending)
	jobs = resolverJobsForTarget(jobs, repository)
	if err != nil || len(jobs) != 1 || !jobs[0].Force {
		t.Fatalf("resolver jobs = %+v, %v", jobs, err)
	}
	if err := s.ClearAllResolverCatalogPublications(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetResolverCatalogPublication(ctx, repository); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("catalog after clear all = %v, want not found", err)
	}
}

func resolverJobsForTarget(jobs []store.Job, target string) []store.Job {
	filtered := make([]store.Job, 0, len(jobs))
	for _, job := range jobs {
		if job.Target == target {
			filtered = append(filtered, job)
		}
	}
	return filtered
}
