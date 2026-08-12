package store_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/analysisunit"
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
	t.Run("candidate first-publication fanout", func(t *testing.T) {
		testResolverCatalogCandidateFirstPublicationFanout(t, s)
	})
	t.Run("declaration outcome fanout", func(t *testing.T) {
		testResolverCatalogDeclarationOutcomeFanout(t, s)
	})
	t.Run("candidate and repo retirement", func(t *testing.T) {
		testResolverCatalogRetiredByCandidateAndRepoTransitions(t, s)
	})
	t.Run("exact declarations", func(t *testing.T) {
		testResolverCatalogAcceptsExactPublishedDeclarationSet(t, s)
	})
	t.Run("partitioned declarations", func(t *testing.T) {
		testResolverCatalogAcceptsExactPartitionedDeclarationSet(t, s)
	})
	t.Run("restore clear and queue kind", func(t *testing.T) {
		testResolverCatalogRestoreClearAndQueueKind(t, s)
	})
}

func testResolverCatalogAcceptsExactPartitionedDeclarationSet(
	t *testing.T, s *store.Surreal,
) {
	ctx := context.Background()
	repository := "github.com/acme/resolver-partitioned-declaration"
	commit := candidateCommit('9')
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
	if _, err := s.CancelPendingJobs(
		ctx, store.JobResolverCatalog, repository,
	); err != nil {
		t.Fatal(err)
	}
	const domain = "proto-contract"
	planDigest := candidateDigest('a')
	rootDigest := candidateDigest('b')
	run, err := s.BeginPartitionedExtractionRun(
		ctx, store.ExtractionScope{
			Repository: repository, Commit: commit, Domain: domain,
		},
		"3.0.0", planDigest, candidate.ManifestDigest,
		store.PartitionedExtractionRunLimits{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PublishPartitionedExtractionDomain(
		ctx, store.PartitionedExtractionDomain{
			Schema:     store.PartitionedExtractionDomainSchema,
			Repository: repository, Domain: domain, RunID: run.ID,
			PlanDigest: planDigest, RootDigest: rootDigest,
			CandidateDigest: candidate.ManifestDigest,
			SourceDigest:    candidateDigest('c'), ObservationDigest: candidateDigest('d'),
			Plan: `{}`, Root: `{}`,
		},
	); err != nil {
		t.Fatal(err)
	}
	declaration := resolvercatalog.DeclarationPublication{
		Domain: domain, RunID: run.ID, GenerationDigest: rootDigest,
		AuthoritySchema: store.PartitionedExtractionDomainSchema,
		PlanDigest:      planDigest, RootDigest: rootDigest,
	}
	identity, err := resolvercatalog.NewIdentity(
		repository, commit, "", candidate.ManifestDigest,
		[]resolvercatalog.DeclarationPublication{declaration}, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	publication := resolverPublication(repository, commit, candidate.ManifestDigest)
	publication.Declarations = []store.ResolverCatalogDeclarationPublication{{
		Domain: declaration.Domain, RunID: declaration.RunID,
		GenerationDigest: declaration.GenerationDigest,
		AuthoritySchema:  declaration.AuthoritySchema,
		PlanDigest:       declaration.PlanDigest, RootDigest: declaration.RootDigest,
	}}
	publication.DeclarationSetDigest = identity.DeclarationSetDigest
	publication.GenerationDigest = identity.GenerationDigest
	if err := s.PublishResolverCatalog(ctx, publication); err != nil {
		t.Fatal(err)
	}
	current, err := s.GetResolverCatalogPublication(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.ResolverCatalogPublicationCurrent(ctx, *current); err != nil || !ok {
		t.Fatalf("partitioned declaration current = %t, %v", ok, err)
	}

	changed := *current
	changed.Declarations = append(
		[]store.ResolverCatalogDeclarationPublication(nil), current.Declarations...,
	)
	changed.Declarations[0].RootDigest = candidateDigest('e')
	changed.Declarations[0].GenerationDigest = changed.Declarations[0].RootDigest
	changedIdentity, err := resolvercatalog.NewIdentity(
		repository, commit, "", candidate.ManifestDigest,
		[]resolvercatalog.DeclarationPublication{{
			Domain: domain, RunID: run.ID,
			GenerationDigest: changed.Declarations[0].GenerationDigest,
			AuthoritySchema:  store.PartitionedExtractionDomainSchema,
			PlanDigest:       planDigest, RootDigest: changed.Declarations[0].RootDigest,
		}}, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	changed.DeclarationSetDigest = changedIdentity.DeclarationSetDigest
	changed.GenerationDigest = changedIdentity.GenerationDigest
	changed.ManifestDigest = candidateDigest('f')
	if err := s.PublishResolverCatalog(ctx, changed); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("mismatched partitioned declaration = %v, want conflict", err)
	}
}

func testResolverCatalogCandidateFirstPublicationFanout(
	t *testing.T, s *store.Surreal,
) {
	ctx := context.Background()
	repository := "github.com/acme/resolver-first-candidate"
	commit := candidateCommit('1')
	if err := s.UpsertRepo(ctx, store.Repo{
		Name: repository, CloneURL: "https://" + repository + ".git",
	}); err != nil {
		t.Fatal(err)
	}
	setCandidateIndexedState(t, ctx, s, repository, commit, nil, nil)
	publication := candidatePublication(repository, commit, "")
	if err := s.PublishCandidateManifest(ctx, publication); err != nil {
		t.Fatal(err)
	}
	assertPendingResolverSuccessor(t, ctx, s, repository, true)

	// An exact retry repairs but never duplicates the durable successor.
	if err := s.PublishCandidateManifest(ctx, publication); err != nil {
		t.Fatalf("exact candidate retry: %v", err)
	}
	assertPendingResolverSuccessor(t, ctx, s, repository, true)
	if canceled, err := s.CancelPendingJobs(
		ctx, store.JobResolverCatalog, repository,
	); err != nil || canceled != 1 {
		t.Fatalf("cancel first successor = %d, %v", canceled, err)
	}

	// A rejected publication cannot leak a resolver job out of its transaction.
	stale := publication
	stale.HeadCommit = candidateCommit('2')
	if err := s.PublishCandidateManifest(ctx, stale); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale candidate publication = %v, want ErrConflict", err)
	}
	assertPendingResolverSuccessor(t, ctx, s, repository, false)

	if err := s.PublishCandidateManifest(ctx, publication); err != nil {
		t.Fatalf("repair missing candidate successor: %v", err)
	}
	assertPendingResolverSuccessorForce(t, ctx, s, repository, false)
}

func testResolverCatalogDeclarationOutcomeFanout(
	t *testing.T, s *store.Surreal,
) {
	type outcomeCase struct {
		name        string
		domain      string
		disposition store.DomainOutcomeDisposition
		wantJob     bool
		wantCatalog bool
	}
	cases := []outcomeCase{
		{
			name: "published proto", domain: "proto-contract",
			disposition: store.DomainOutcomePublished,
			wantJob:     true, wantCatalog: false,
		},
		{
			name: "published thrift", domain: "thrift-contract",
			disposition: store.DomainOutcomePublished,
			wantJob:     true, wantCatalog: false,
		},
		{
			name: "terminal proto", domain: "proto-contract",
			disposition: store.DomainOutcomeTerminalGenerationRefusal,
			wantCatalog: true,
		},
		{
			name: "terminal thrift", domain: "thrift-contract",
			disposition: store.DomainOutcomeTerminalGenerationRefusal,
			wantCatalog: true,
		},
		{
			name: "retryable proto", domain: "proto-contract",
			disposition: store.DomainOutcomeRetryableFailure,
			wantCatalog: true,
		},
		{
			name: "retryable thrift", domain: "thrift-contract",
			disposition: store.DomainOutcomeRetryableFailure,
			wantCatalog: true,
		},
		{
			name: "unavailable proto", domain: "proto-contract",
			disposition: store.DomainOutcomeUnavailablePrerequisite,
			wantCatalog: true,
		},
		{
			name: "unavailable thrift", domain: "thrift-contract",
			disposition: store.DomainOutcomeUnavailablePrerequisite,
			wantCatalog: true,
		},
		{
			name: "unrelated terminal", domain: "grpc-go",
			disposition: store.DomainOutcomeTerminalGenerationRefusal,
			wantCatalog: true,
		},
	}
	for index, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			repository := fmt.Sprintf(
				"github.com/acme/resolver-outcome-fanout-%d", index,
			)
			commit := candidateCommit("3456789ab"[index])
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
			if canceled, err := s.CancelPendingJobs(
				ctx, store.JobResolverCatalog, repository,
			); err != nil || canceled != 1 {
				t.Fatalf("cancel candidate successor = %d, %v", canceled, err)
			}
			pointer, err := s.GetCandidateManifestPublication(ctx, repository)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.PublishResolverCatalog(
				ctx, resolverPublication(
					repository, commit, pointer.ManifestDigest,
				),
			); err != nil {
				t.Fatalf("publish empty catalog: %v", err)
			}
			scope := store.ExtractionScope{
				Repository: repository, Commit: commit, Domain: test.domain,
			}
			outcome := durableOutcome(scope, test.disposition, "1.0.0")
			outcome.Generation.CandidateManifestDigest = pointer.ManifestDigest
			outcome.Generation.CandidatePolicyDigest = pointer.PolicyDigest
			outcome.Generation.CandidateControlRevision = pointer.ControlRevision
			outcome.Generation.InventoryPolicy =
				"candidate-manifest-v4-" + strings.Repeat("b", 64)
			outcome.Generation.Digest =
				store.ComputeExtractionGenerationDigest(outcome.Generation)

			if test.disposition == store.DomainOutcomePublished {
				run, runErr := s.BeginExtractionRun(ctx, scope, outcome.Generation.Extractor)
				if runErr != nil {
					t.Fatal(runErr)
				}
				outcome.RunID = run.ID
				if err := s.PublishExtractionRunWithOutcome(
					ctx, run.ID, candidateCoverage(pointer.ManifestDigest), outcome,
				); err != nil {
					t.Fatalf("publish declaration outcome: %v", err)
				}
			} else if err := s.RecordExtractionDomainOutcome(ctx, outcome); err != nil {
				t.Fatalf("record declaration outcome: %v", err)
			}
			assertPendingResolverSuccessor(
				t, ctx, s, repository, test.wantJob,
			)
			_, catalogErr := s.GetResolverCatalogPublication(ctx, repository)
			if test.wantCatalog && catalogErr != nil {
				t.Fatalf("catalog was retired: %v", catalogErr)
			}
			if !test.wantCatalog && !errors.Is(catalogErr, store.ErrNotFound) {
				t.Fatalf("catalog after publication = %v, want not found", catalogErr)
			}
		})
	}

	t.Run("nonpublished dependent retirement", func(t *testing.T) {
		cases := []struct {
			name               string
			domain             string
			disposition        store.DomainOutcomeDisposition
			preexistingPending bool
		}{
			{
				name: "proto creates forced successor", domain: "proto-contract",
				disposition: store.DomainOutcomeTerminalGenerationRefusal,
			},
			{
				name: "thrift upgrades pending successor", domain: "thrift-contract",
				disposition:        store.DomainOutcomeRetryableFailure,
				preexistingPending: true,
			},
		}
		for index, test := range cases {
			t.Run(test.name, func(t *testing.T) {
				ctx := context.Background()
				repository := fmt.Sprintf(
					"github.com/acme/resolver-dependent-outcome-%d", index,
				)
				commit := candidateCommit("cd"[index])
				if err := s.UpsertRepo(ctx, store.Repo{
					Name: repository, CloneURL: "https://" + repository + ".git",
				}); err != nil {
					t.Fatal(err)
				}
				setCandidateIndexedState(t, ctx, s, repository, commit, nil, nil)
				if err := s.PublishCandidateManifest(
					ctx, candidatePublication(repository, commit, ""),
				); err != nil {
					t.Fatal(err)
				}
				if canceled, err := s.CancelPendingJobs(
					ctx, store.JobResolverCatalog, repository,
				); err != nil || canceled != 1 {
					t.Fatalf("cancel candidate successor = %d, %v", canceled, err)
				}
				pointer, err := s.GetCandidateManifestPublication(ctx, repository)
				if err != nil {
					t.Fatal(err)
				}
				scope := store.ExtractionScope{
					Repository: repository, Commit: commit, Domain: test.domain,
				}
				published := durableOutcome(
					scope, store.DomainOutcomePublished, "1.0.0",
				)
				published.Generation.CandidateManifestDigest = pointer.ManifestDigest
				published.Generation.CandidatePolicyDigest = pointer.PolicyDigest
				published.Generation.CandidateControlRevision = pointer.ControlRevision
				published.Generation.InventoryPolicy =
					"candidate-manifest-v4-" + strings.Repeat("b", 64)
				published.Generation.Digest =
					store.ComputeExtractionGenerationDigest(published.Generation)
				run, err := s.BeginExtractionRun(
					ctx, scope, published.Generation.Extractor,
				)
				if err != nil {
					t.Fatal(err)
				}
				published.RunID = run.ID
				if err := s.PublishExtractionRunWithOutcome(
					ctx, run.ID, candidateCoverage(pointer.ManifestDigest), published,
				); err != nil {
					t.Fatalf("publish declaration outcome: %v", err)
				}
				if canceled, err := s.CancelPendingJobs(
					ctx, store.JobResolverCatalog, repository,
				); err != nil || canceled != 1 {
					t.Fatalf("cancel declaration successor = %d, %v", canceled, err)
				}

				declaration := resolvercatalog.DeclarationPublication{
					Domain: test.domain, RunID: run.ID,
					GenerationDigest: published.Generation.Digest,
				}
				identity, err := resolvercatalog.NewIdentity(
					repository, commit, "", pointer.ManifestDigest,
					[]resolvercatalog.DeclarationPublication{declaration}, nil,
				)
				if err != nil {
					t.Fatal(err)
				}
				catalog := resolverPublication(
					repository, commit, pointer.ManifestDigest,
				)
				catalog.Declarations = []store.ResolverCatalogDeclarationPublication{{
					Domain: test.domain, RunID: run.ID,
					GenerationDigest: published.Generation.Digest,
				}}
				catalog.DeclarationSetDigest = identity.DeclarationSetDigest
				catalog.GenerationDigest = identity.GenerationDigest
				catalog.ManifestDigest = candidateDigest("de"[index])
				if err := s.PublishResolverCatalog(ctx, catalog); err != nil {
					t.Fatalf("publish dependent catalog: %v", err)
				}
				if test.preexistingPending {
					if _, err := s.EnqueuePending(
						ctx, store.JobResolverCatalog, repository, false,
					); err != nil {
						t.Fatal(err)
					}
					assertPendingResolverSuccessorForce(
						t, ctx, s, repository, false,
					)
				} else {
					assertPendingResolverSuccessor(t, ctx, s, repository, false)
				}

				replacement := durableOutcome(
					scope, test.disposition, "2.0.0",
				)
				replacement.Generation.CandidateManifestDigest = pointer.ManifestDigest
				replacement.Generation.CandidatePolicyDigest = pointer.PolicyDigest
				replacement.Generation.CandidateControlRevision = pointer.ControlRevision
				replacement.Generation.InventoryPolicy =
					"candidate-manifest-v4-" + strings.Repeat("b", 64)
				replacement.Generation.Digest =
					store.ComputeExtractionGenerationDigest(replacement.Generation)
				if err := s.RecordExtractionDomainOutcome(ctx, replacement); err != nil {
					t.Fatalf("record dependent nonpublished outcome: %v", err)
				}
				if _, err := s.GetResolverCatalogPublication(
					ctx, repository,
				); !errors.Is(err, store.ErrNotFound) {
					t.Fatalf("catalog after dependent outcome = %v, want not found", err)
				}
				assertPendingResolverSuccessor(t, ctx, s, repository, true)
			})
		}
	})

	t.Run("upgrades existing pending successor", func(t *testing.T) {
		ctx := context.Background()
		repository := "github.com/acme/resolver-outcome-upgrade"
		commit := candidateCommit('9')
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
		if canceled, err := s.CancelPendingJobs(
			ctx, store.JobResolverCatalog, repository,
		); err != nil || canceled != 1 {
			t.Fatalf("cancel candidate successor = %d, %v", canceled, err)
		}
		if _, err := s.EnqueuePending(
			ctx, store.JobResolverCatalog, repository, false,
		); err != nil {
			t.Fatal(err)
		}
		pointer, err := s.GetCandidateManifestPublication(ctx, repository)
		if err != nil {
			t.Fatal(err)
		}
		scope := store.ExtractionScope{
			Repository: repository, Commit: commit, Domain: "proto-contract",
		}
		outcome := durableOutcome(scope, store.DomainOutcomePublished, "1.0.0")
		outcome.Generation.CandidateManifestDigest = pointer.ManifestDigest
		outcome.Generation.CandidatePolicyDigest = pointer.PolicyDigest
		outcome.Generation.CandidateControlRevision = pointer.ControlRevision
		outcome.Generation.InventoryPolicy =
			"candidate-manifest-v4-" + strings.Repeat("b", 64)
		outcome.Generation.Digest =
			store.ComputeExtractionGenerationDigest(outcome.Generation)
		run, err := s.BeginExtractionRun(ctx, scope, outcome.Generation.Extractor)
		if err != nil {
			t.Fatal(err)
		}
		outcome.RunID = run.ID
		if err := s.PublishExtractionRunWithOutcome(
			ctx, run.ID, candidateCoverage(pointer.ManifestDigest), outcome,
		); err != nil {
			t.Fatal(err)
		}
		assertPendingResolverSuccessor(t, ctx, s, repository, true)
	})
}

func candidateCoverage(candidateManifestDigest string) store.CoverageManifest {
	emptyDigest := "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	return store.CoverageManifest{
		SourceScopeDigest:       emptyDigest,
		CandidateManifestDigest: candidateManifestDigest,
		ScopePosture:            "whole-repository",
		CandidatePlane:          "local",
		ScopeCorpusDigest:       emptyDigest,
		PlannedScopeDigest:      emptyDigest,
	}
}

func assertPendingResolverSuccessor(
	t *testing.T,
	ctx context.Context,
	s *store.Surreal,
	repository string,
	want bool,
) {
	t.Helper()
	jobs, err := s.ListJobs(ctx, store.JobResolverCatalog, store.StatusPending)
	if err != nil {
		t.Fatal(err)
	}
	jobs = resolverJobsForTarget(jobs, repository)
	if !want {
		if len(jobs) != 0 {
			t.Fatalf("resolver successor = %+v, want none", jobs)
		}
		return
	}
	if len(jobs) != 1 || !jobs[0].Force {
		t.Fatalf("resolver successor = %+v, want one forced job", jobs)
	}
}

func assertPendingResolverSuccessorForce(
	t *testing.T,
	ctx context.Context,
	s *store.Surreal,
	repository string,
	wantForce bool,
) {
	t.Helper()
	jobs, err := s.ListJobs(ctx, store.JobResolverCatalog, store.StatusPending)
	if err != nil {
		t.Fatal(err)
	}
	jobs = resolverJobsForTarget(jobs, repository)
	if len(jobs) != 1 || jobs[0].Force != wantForce {
		t.Fatalf(
			"resolver successor = %+v, want one job with force=%v",
			jobs, wantForce,
		)
	}
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
	if _, err := s.GetResolverCatalogPublication(
		ctx, repository,
	); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf(
			"catalog after byte-identical candidate control transition = %v, want not found",
			err,
		)
	}
	if err := s.ClearCandidateManifestPublication(ctx, repository); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetResolverCatalogPublication(ctx, repository); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("catalog after candidate clear = %v, want not found", err)
	}
	jobs, err := s.ListJobs(ctx, store.JobResolverCatalog, store.StatusPending)
	jobs = resolverJobsForTarget(jobs, repository)
	if err != nil || len(jobs) != 1 || !jobs[0].Force {
		t.Fatalf("candidate-clear catalog successor = %+v, %v", jobs, err)
	}
	if canceled, err := s.CancelPendingJobs(
		ctx, store.JobResolverCatalog, repository,
	); err != nil || canceled != 1 {
		t.Fatalf("cancel candidate-clear successor = %d, %v", canceled, err)
	}
	candidate.ControlRevision = 0
	if err := s.PublishCandidateManifest(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishResolverCatalog(ctx, catalog); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnqueuePending(
		ctx, store.JobResolverCatalog, repository, false,
	); err != nil {
		t.Fatal(err)
	}
	nextCommit := candidateCommit('2')
	setCandidateIndexedState(t, ctx, s, repository, nextCommit, nil, nil)
	if _, err := s.GetResolverCatalogPublication(ctx, repository); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("catalog after repo transition = %v, want not found", err)
	}
	jobs, err = s.ListJobs(ctx, store.JobResolverCatalog, store.StatusPending)
	jobs = resolverJobsForTarget(jobs, repository)
	if err != nil || len(jobs) != 1 || !jobs[0].Force {
		t.Fatalf("repo-transition catalog successor = %+v, %v", jobs, err)
	}
	setCandidateIndexedState(t, ctx, s, repository, nextCommit, nil, nil)
	jobs, err = s.ListJobs(ctx, store.JobResolverCatalog, store.StatusPending)
	jobs = resolverJobsForTarget(jobs, repository)
	if err != nil || len(jobs) != 1 || !jobs[0].Force {
		t.Fatalf("exact repo retry duplicated catalog successor = %+v, %v", jobs, err)
	}

	scopedRepository := "github.com/acme/resolver-retire-scoped"
	unit, err := (analysisunit.Scope{
		Repository: scopedRepository,
		Name:       "service",
		Primary:    []string{"service"},
	}).State()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertRepo(ctx, store.Repo{
		Name: scopedRepository, CloneURL: "https://" + scopedRepository + ".git",
	}); err != nil {
		t.Fatal(err)
	}
	setCandidateIndexedState(t, ctx, s, scopedRepository, commit, unit, nil)
	scopedCandidate := candidatePublication(scopedRepository, commit, unit.Digest)
	if err := s.PublishCandidateManifest(ctx, scopedCandidate); err != nil {
		t.Fatal(err)
	}
	scopedIdentity, err := resolvercatalog.NewIdentity(
		scopedRepository, commit, unit.Digest, scopedCandidate.ManifestDigest,
		nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	scopedCatalog := resolverPublication(
		scopedRepository, commit, scopedCandidate.ManifestDigest,
	)
	scopedCatalog.UnitDigest = unit.Digest
	scopedCatalog.GenerationDigest = scopedIdentity.GenerationDigest
	if err := s.PublishResolverCatalog(ctx, scopedCatalog); err != nil {
		t.Fatal(err)
	}
	setCandidateIndexedState(
		t, ctx, s, scopedRepository, nextCommit, unit, nil,
	)
	if _, err := s.GetResolverCatalogPublication(
		ctx, scopedRepository,
	); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("scoped catalog after repo transition = %v, want not found", err)
	}
	jobs, err = s.ListJobs(ctx, store.JobResolverCatalog, store.StatusPending)
	jobs = resolverJobsForTarget(jobs, scopedRepository)
	if err != nil || len(jobs) != 1 || !jobs[0].Force {
		t.Fatalf("scoped repo-transition catalog successor = %+v, %v", jobs, err)
	}

	clearRepository := "github.com/acme/resolver-clear-index"
	if err := s.UpsertRepo(ctx, store.Repo{
		Name: clearRepository, CloneURL: "https://" + clearRepository + ".git",
	}); err != nil {
		t.Fatal(err)
	}
	setCandidateIndexedState(t, ctx, s, clearRepository, commit, nil, nil)
	clearCandidate := candidatePublication(clearRepository, commit, "")
	if err := s.PublishCandidateManifest(ctx, clearCandidate); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishResolverCatalog(
		ctx,
		resolverPublication(clearRepository, commit, clearCandidate.ManifestDigest),
	); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearRepoIndexState(ctx, clearRepository); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetResolverCatalogPublication(
		ctx, clearRepository,
	); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("catalog after index clear = %v, want not found", err)
	}
	jobs, err = s.ListJobs(ctx, store.JobResolverCatalog, store.StatusPending)
	jobs = resolverJobsForTarget(jobs, clearRepository)
	if err != nil || len(jobs) != 1 || !jobs[0].Force {
		t.Fatalf("index-clear catalog successor = %+v, %v", jobs, err)
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
	candidate.ControlRevision = candidatePointer.ControlRevision + 1
	if err := s.PublishCandidateManifest(ctx, candidate); err != nil {
		t.Fatalf("advance candidate control revision: %v", err)
	}
	if _, err := s.GetResolverCatalogPublication(
		ctx, repository,
	); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("catalog after candidate control repair = %v, want not found", err)
	}
	if err := s.PublishResolverCatalog(ctx, publication); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("republish catalog with pre-repair declaration = %v, want conflict", err)
	}
	candidatePointer, err = s.GetCandidateManifestPublication(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	nextRun, err := s.BeginExtractionRun(ctx, scope, "1.1.0")
	if err != nil {
		t.Fatal(err)
	}
	nextGeneration := generation
	nextGeneration.CandidatePolicyDigest = candidatePointer.PolicyDigest
	nextGeneration.CandidateControlRevision = candidatePointer.ControlRevision
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
