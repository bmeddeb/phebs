package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/surrealdb/surrealdb.go/pkg/models"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/callerpublicationid"
	"github.com/bmeddeb/phebs/internal/candidateid"
	"github.com/bmeddeb/phebs/internal/resolvercatalogid"
)

func derivedRetentionDigest(label string) string {
	sum := sha256.Sum256([]byte(label))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func derivedRetentionRepository(index int) string {
	return fmt.Sprintf("example.com/derived/repository-%03d", index)
}

func derivedRetentionCandidateFixture(
	repository string,
) CandidateManifestPublication {
	return CandidateManifestPublication{
		Repository:       repository,
		HeadCommit:       strings.Repeat("1", 40),
		PolicyDigest:     derivedRetentionDigest("candidate-policy"),
		ManifestDigest:   derivedRetentionDigest("candidate-manifest"),
		GenerationDigest: derivedRetentionDigest("candidate-generation"),
		ManifestPath:     candidateid.ManifestName(repository),
		ControlRevision:  1,
		PublishedAt:      time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC),
	}
}

func derivedRetentionResolverFixture(
	repository string,
) ResolverCatalogPublication {
	declarations := []ResolverCatalogDeclarationPublication{}
	declarationDigest, err := resolverCatalogDeclarationSetDigest(declarations)
	if err != nil {
		panic(err)
	}
	packs := []ResolverCatalogPack{}
	packDigest, err := resolverCatalogPackSetDigest(packs)
	if err != nil {
		panic(err)
	}
	return ResolverCatalogPublication{
		Repository:              repository,
		HeadCommit:              strings.Repeat("1", 40),
		Declarations:            declarations,
		DeclarationSetDigest:    declarationDigest,
		CandidateManifestDigest: derivedRetentionDigest("candidate-manifest"),
		SourceLanePolicy:        resolverCatalogSourceLanePolicy,
		ResolverPacks:           packs,
		ResolverPackSetDigest:   packDigest,
		CatalogPolicyDigest:     derivedRetentionDigest("catalog-policy"),
		GenerationDigest:        derivedRetentionDigest("resolver-generation"),
		ManifestDigest:          derivedRetentionDigest("resolver-manifest"),
		AuthorityDigest:         derivedRetentionDigest("resolver-authority"),
		ManifestPath:            resolvercatalogid.ManifestName(repository),
		ControlRevision:         1,
		WriterSchema:            resolverCatalogWriterSchema,
		PublishedAt:             time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC),
	}
}

func derivedRetentionFocusedState(
	repository string,
) (*analysisunit.State, error) {
	return (analysisunit.Scope{
		Repository: repository,
		Name:       "service",
		Primary:    []string{"cmd/service"},
		Supporting: []string{"internal/shared"},
	}).State()
}

func seedDerivedCandidate(
	t *testing.T,
	store *Surreal,
	index int,
) CandidateManifestPublication {
	t.Helper()
	publication := derivedRetentionCandidateFixture(derivedRetentionRepository(index))
	requireRetentionStatement(t, t.Context(), store,
		"CREATE $rid CONTENT $content RETURN NONE",
		map[string]any{
			"rid":     candidateManifestPublicationID(publication.Repository),
			"content": publication,
		},
	)
	return publication
}

func seedDerivedResolver(
	t *testing.T,
	store *Surreal,
	index int,
) ResolverCatalogPublication {
	t.Helper()
	publication := derivedRetentionResolverFixture(derivedRetentionRepository(index))
	requireRetentionStatement(t, t.Context(), store,
		"CREATE $rid CONTENT $content RETURN NONE",
		map[string]any{
			"rid":     resolverCatalogPublicationID(publication.Repository),
			"content": publication,
		},
	)
	return publication
}

func seedDerivedFocused(
	t *testing.T,
	store *Surreal,
	index int,
	focused bool,
) {
	t.Helper()
	repository := derivedRetentionRepository(index)
	commit := strings.Repeat("1", 40)
	var unit *analysisunit.State
	if focused {
		var err error
		unit, err = derivedRetentionFocusedState(repository)
		if err != nil {
			t.Fatal(err)
		}
	}
	content := map[string]any{
		"name":                repository,
		"indexed_commit_hash": commit,
		"indexed_revisions": []IndexedRevision{{
			Selector: "HEAD", Branch: "HEAD", Commit: commit,
		}},
	}
	if unit != nil {
		content["indexed_analysis_unit"] = unit
	}
	requireRetentionStatement(t, t.Context(), store,
		"CREATE $rid CONTENT $content RETURN NONE",
		map[string]any{
			"rid": repoID(repository), "content": content,
		},
	)
}

func seedDerivedCaller(
	t *testing.T,
	store *Surreal,
	index int,
) CallerGenerationPublicationSummary {
	t.Helper()
	repository := derivedRetentionRepository(index)
	candidate := derivedRetentionCandidateFixture(repository)
	resolver := derivedRetentionResolverFixture(repository)
	generation, err := prepareCallerGeneration(CallerGenerationIdentity{
		Repository:               repository,
		HeadCommit:               candidate.HeadCommit,
		DeclarationSetDigest:     resolver.DeclarationSetDigest,
		CandidateManifestDigest:  candidate.ManifestDigest,
		CandidatePolicyDigest:    candidate.PolicyDigest,
		CandidateControlRevision: candidate.ControlRevision,
		ResolverGenerationDigest: resolver.GenerationDigest,
		ResolverManifestDigest:   resolver.AuthorityDigest,
		ResolverControlRevision:  resolver.ControlRevision,
		ResolverWriterSchema:     resolver.WriterSchema,
		SourceLanePolicy:         CallerBaseSourceLanePolicy,
		CallerPolicyDigest:       derivedRetentionDigest("caller-policy"),
		ExtractorSetDigest:       derivedRetentionDigest("extractor-set"),
	})
	if err != nil {
		t.Fatal(err)
	}
	manifestDigest := derivedRetentionDigest("caller-manifest")
	summary := CallerGenerationPublicationSummary{
		Generation:             generation,
		PairPayloadDigest:      derivedRetentionDigest("caller-pair-payload"),
		PairSetDigest:          derivedRetentionDigest("caller-pair-set"),
		PairCount:              0,
		ArtifactCount:          0,
		ResultCount:            0,
		AbstentionCount:        0,
		CanonicalBytes:         0,
		StagingBytes:           0,
		PeakOpenFiles:          maxCallerPeakOpenFiles,
		ManifestDigest:         manifestDigest,
		ManifestPath:           callerpublicationid.ManifestName(generation.Digest, manifestDigest),
		PublicationRevision:    1,
		PublicationIncarnation: derivedRetentionDigest("caller-incarnation-" + repository),
		WriterSchema:           CallerGenerationPublicationWriterSchema,
		PublishedAt:            time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC),
	}
	requireRetentionStatement(t, t.Context(), store, `
CREATE $repo_rid CONTENT {
	name: $repository,
	indexed_commit_hash: $head_commit,
	indexed_revisions: [{ selector: 'HEAD', branch: 'HEAD', commit: $head_commit }],
	indexed_analysis_unit: NONE,
	caller_publication_revision: 1,
	deleting: false
} RETURN NONE;
CREATE $candidate_rid CONTENT $candidate RETURN NONE;
CREATE $resolver_rid CONTENT $resolver RETURN NONE;
CREATE $caller_rid CONTENT {
	repository: $repository,
	generation: $generation,
	generation_digest: $generation.digest,
	pairs: [],
	pair_payload_digest: $summary.pair_payload_digest,
	pair_set_digest: $summary.pair_set_digest,
	pair_count: $summary.pair_count,
	artifact_count: $summary.artifact_count,
	result_count: $summary.result_count,
	abstention_count: $summary.abstention_count,
	canonical_bytes: $summary.canonical_bytes,
	staging_bytes: $summary.staging_bytes,
	peak_open_files: $summary.peak_open_files,
	manifest_digest: $summary.manifest_digest,
	manifest_path: $summary.manifest_path,
	publication_revision: $summary.publication_revision,
	publication_incarnation: $summary.publication_incarnation,
	writer_schema: $summary.writer_schema,
	published_at: $summary.published_at
} RETURN NONE;`, map[string]any{
		"repo_rid":      repoID(repository),
		"candidate_rid": candidateManifestPublicationID(repository),
		"resolver_rid":  resolverCatalogPublicationID(repository),
		"caller_rid":    callerGenerationPublicationID(repository),
		"repository":    repository,
		"head_commit":   generation.HeadCommit,
		"candidate":     candidate,
		"resolver":      resolver,
		"generation":    generation,
		"summary":       summary,
	})
	return summary
}

func derivedRetentionRequest(
	component RetentionComponent,
	reported int,
) RetentionComponentRequest {
	return RetentionComponentRequest{
		Component: component, ReportedIdentities: reported,
		ScanIdentities: reported + 1,
	}
}

func TestDerivedRetentionEmptyReadyStoreIsExactZero(t *testing.T) {
	store := newRetentionTestStore(t)
	requests := []RetentionComponentRequest{
		derivedRetentionRequest(RetentionCandidatePublication, 78),
		derivedRetentionRequest(RetentionCallerArtifacts, 78),
		derivedRetentionRequest(RetentionFocusedRepositoryState, 78),
		derivedRetentionRequest(RetentionResolverPublication, 78),
	}
	collection, err := store.CollectDerivedRetention(t.Context(), requests)
	if err != nil {
		t.Fatal(err)
	}
	wantComponents := []RetentionComponent{
		RetentionCandidatePublication,
		RetentionFocusedRepositoryState,
		RetentionResolverPublication,
	}
	if len(collection.Results) != len(wantComponents) {
		t.Fatalf("results = %d, want %d", len(collection.Results), len(wantComponents))
	}
	for index, result := range collection.Results {
		if result.Component != wantComponents[index] || result.Err != nil ||
			result.Summary != (RetentionComponentSummary{}) {
			t.Fatalf("result %d = %+v", index, result)
		}
	}
	if len(collection.CandidateAuthorities) != 0 ||
		len(collection.FocusedAuthorities) != 0 ||
		len(collection.ResolverAuthorities) != 0 ||
		len(collection.CallerAuthorities) != 0 ||
		collection.FocusedPartial || collection.CallerScannedIdentities != 0 ||
		collection.CallerTruncated || collection.CallerErr != nil {
		t.Fatalf("empty collection = %+v", collection)
	}
}

func TestDerivedRetentionUnderCapExactCapAndSentinel(t *testing.T) {
	tests := []struct {
		name      string
		component RetentionComponent
		seed      func(*testing.T, *Surreal, int)
		assert    func(*testing.T, DerivedRetentionCollection, int)
	}{
		{
			name: "candidate", component: RetentionCandidatePublication,
			seed: func(t *testing.T, store *Surreal, index int) {
				seedDerivedCandidate(t, store, index)
			},
			assert: func(t *testing.T, got DerivedRetentionCollection, count int) {
				if len(got.CandidateAuthorities) != count {
					t.Fatalf("candidate authorities = %d, want %d", len(got.CandidateAuthorities), count)
				}
			},
		},
		{
			name: "resolver", component: RetentionResolverPublication,
			seed: func(t *testing.T, store *Surreal, index int) {
				seedDerivedResolver(t, store, index)
			},
			assert: func(t *testing.T, got DerivedRetentionCollection, count int) {
				if len(got.ResolverAuthorities) != count {
					t.Fatalf("resolver authorities = %d, want %d", len(got.ResolverAuthorities), count)
				}
			},
		},
		{
			name: "caller", component: RetentionCallerArtifacts,
			seed: func(t *testing.T, store *Surreal, index int) {
				seedDerivedCaller(t, store, index)
			},
			assert: func(t *testing.T, got DerivedRetentionCollection, count int) {
				if got.CallerErr != nil || len(got.CallerAuthorities) != count {
					t.Fatalf("caller authorities/error = %d/%v, want %d/nil", len(got.CallerAuthorities), got.CallerErr, count)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newRetentionTestStore(t)
			request := []RetentionComponentRequest{
				derivedRetentionRequest(test.component, 2),
			}
			for count := 1; count <= 3; count++ {
				test.seed(t, store, count)
				collection, err := store.CollectDerivedRetention(t.Context(), request)
				if err != nil {
					t.Fatal(err)
				}
				selected := count
				if selected > 2 {
					selected = 2
				}
				test.assert(t, collection, selected)
				if test.component == RetentionCallerArtifacts {
					if collection.CallerScannedIdentities != count ||
						collection.CallerTruncated != (count == 3) {
						t.Fatalf("caller count %d scan/truncated = %d/%t", count, collection.CallerScannedIdentities, collection.CallerTruncated)
					}
					continue
				}
				if len(collection.Results) != 1 {
					t.Fatalf("count %d results = %d", count, len(collection.Results))
				}
				summary := collection.Results[0].Summary
				if summary.ScannedIdentities != count ||
					summary.ReportedIdentities != int64(selected) ||
					summary.Truncated != (count == 3) || summary.Bytes != nil {
					t.Fatalf("count %d summary = %+v", count, summary)
				}
			}
		})
	}
}

func TestDerivedRetentionFullCandidateAllocationExcludesSentinel(t *testing.T) {
	store := newRetentionTestStore(t)
	for index := 1; index <= 79; index++ {
		seedDerivedCandidate(t, store, index)
	}
	collection, err := store.CollectDerivedRetention(t.Context(), []RetentionComponentRequest{
		derivedRetentionRequest(RetentionCandidatePublication, 78),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(collection.Results) != 1 || collection.Results[0].Err != nil ||
		collection.Results[0].Summary.ScannedIdentities != 79 ||
		collection.Results[0].Summary.ReportedIdentities != 78 ||
		!collection.Results[0].Summary.Truncated ||
		len(collection.CandidateAuthorities) != 78 {
		t.Fatalf("full candidate allocation = %+v", collection)
	}
	sentinel := derivedRetentionRepository(79)
	for _, authority := range collection.CandidateAuthorities {
		if authority.Repository == sentinel {
			t.Fatalf("cap-plus-one sentinel %q crossed the authority boundary", sentinel)
		}
	}
}

func TestDerivedRetentionCallerFenceBatchesMoreThanTwoAuthorities(t *testing.T) {
	store := newRetentionTestStore(t)
	for index := 1; index <= 3; index++ {
		seedDerivedCaller(t, store, index)
	}
	collection, err := store.CollectDerivedRetention(t.Context(), []RetentionComponentRequest{
		derivedRetentionRequest(RetentionCallerArtifacts, 3),
	})
	if err != nil || collection.CallerErr != nil ||
		collection.CallerScannedIdentities != 3 || collection.CallerTruncated ||
		len(collection.CallerAuthorities) != 3 {
		t.Fatalf("three-authority caller fence = %+v, %v", collection, err)
	}
}

func TestDerivedRetentionFocusedPredicateAndBoundedPartial(t *testing.T) {
	t.Run("exact predicate", func(t *testing.T) {
		store := newRetentionTestStore(t)
		seedDerivedFocused(t, store, 1, false)
		seedDerivedFocused(t, store, 2, true)
		collection, err := store.CollectDerivedRetention(t.Context(), []RetentionComponentRequest{
			derivedRetentionRequest(RetentionFocusedRepositoryState, 2),
		})
		if err != nil {
			t.Fatal(err)
		}
		if collection.FocusedPartial || len(collection.FocusedAuthorities) != 1 ||
			collection.Results[0].Summary.ScannedIdentities != 1 ||
			collection.Results[0].Summary.ReportedIdentities != 1 ||
			collection.Results[0].Summary.Truncated {
			t.Fatalf("focused exact collection = %+v", collection)
		}
	})

	t.Run("nonempty partial lower bound", func(t *testing.T) {
		store := newRetentionTestStore(t)
		seedDerivedFocused(t, store, 1, false)
		seedDerivedFocused(t, store, 2, true)
		seedDerivedFocused(t, store, 3, true)
		collection, err := store.CollectDerivedRetention(t.Context(), []RetentionComponentRequest{
			derivedRetentionRequest(RetentionFocusedRepositoryState, 2),
		})
		if err != nil {
			t.Fatal(err)
		}
		if !collection.FocusedPartial || len(collection.FocusedAuthorities) != 2 ||
			collection.Results[0].Summary.ScannedIdentities != 2 ||
			collection.Results[0].Summary.ReportedIdentities != 2 ||
			collection.Results[0].Summary.Truncated {
			t.Fatalf("focused partial collection = %+v", collection)
		}
	})

	t.Run("empty partial is unavailable", func(t *testing.T) {
		store := newRetentionTestStore(t)
		seedDerivedFocused(t, store, 1, false)
		seedDerivedFocused(t, store, 2, false)
		seedDerivedFocused(t, store, 3, false)
		collection, err := store.CollectDerivedRetention(t.Context(), []RetentionComponentRequest{
			derivedRetentionRequest(RetentionFocusedRepositoryState, 2),
		})
		if err != nil {
			t.Fatal(err)
		}
		if !collection.FocusedPartial || len(collection.Results) != 1 ||
			!errors.Is(collection.Results[0].Err, ErrRetentionComponentUnavailable) ||
			len(collection.FocusedAuthorities) != 0 {
			t.Fatalf("focused empty partial collection = %+v", collection)
		}
	})

	t.Run("qualifying raw sentinel is retained as lower bound", func(t *testing.T) {
		store := newRetentionTestStore(t)
		seedDerivedFocused(t, store, 1, false)
		seedDerivedFocused(t, store, 2, false)
		seedDerivedFocused(t, store, 3, true)
		collection, err := store.CollectDerivedRetention(t.Context(), []RetentionComponentRequest{
			derivedRetentionRequest(RetentionFocusedRepositoryState, 2),
		})
		if err != nil {
			t.Fatal(err)
		}
		if !collection.FocusedPartial || len(collection.FocusedAuthorities) != 1 ||
			collection.Results[0].Summary.ScannedIdentities != 1 ||
			collection.Results[0].Summary.ReportedIdentities != 1 ||
			collection.Results[0].Summary.Truncated {
			t.Fatalf("focused raw-sentinel collection = %+v", collection)
		}
	})
}

func TestDerivedRetentionReturnsClonedBoundedProjections(t *testing.T) {
	store := newRetentionTestStore(t)
	seedDerivedResolver(t, store, 1)
	seedDerivedFocused(t, store, 2, true)
	requests := []RetentionComponentRequest{
		derivedRetentionRequest(RetentionResolverPublication, 2),
		derivedRetentionRequest(RetentionFocusedRepositoryState, 2),
	}
	first, err := store.CollectDerivedRetention(t.Context(), requests)
	if err != nil {
		t.Fatal(err)
	}
	first.ResolverAuthorities[0].Declarations = append(
		first.ResolverAuthorities[0].Declarations,
		ResolverCatalogDeclarationPublication{Domain: "mutated"},
	)
	first.FocusedAuthorities[0].IndexedAnalysisUnit.PrimaryPaths[0] = "mutated"

	second, err := store.CollectDerivedRetention(t.Context(), requests)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.ResolverAuthorities[0].Declarations) != 0 ||
		second.FocusedAuthorities[0].IndexedAnalysisUnit.PrimaryPaths[0] != "cmd/service" {
		t.Fatalf("second projections retained caller mutation: %+v", second)
	}
	if strings.Contains(derivedRetentionCandidateSQL, "SELECT *") ||
		strings.Contains(derivedRetentionFocusedSQL, "SELECT *") ||
		strings.Contains(derivedRetentionResolverSQL, "SELECT *") ||
		strings.Contains(derivedRetentionCallerSQL, "SELECT *") ||
		strings.Contains(derivedRetentionCallerSQL, "pairs,") {
		t.Fatal("derived retention query widened to an unbounded projection")
	}
}

func TestDerivedRetentionReadinessAndCatalogFailuresLocalize(t *testing.T) {
	tests := []struct {
		name      string
		component RetentionComponent
		mutate    func(*testing.T, *Surreal)
	}{
		{
			name: "candidate marker absent", component: RetentionCandidatePublication,
			mutate: func(t *testing.T, store *Surreal) {
				requireRetentionStatement(t, t.Context(), store,
					"DELETE $rid RETURN NONE",
					map[string]any{"rid": candidateControlRevisionMigrationID()},
				)
			},
		},
		{
			name: "resolver marker future", component: RetentionResolverPublication,
			mutate: func(t *testing.T, store *Surreal) {
				requireRetentionStatement(t, t.Context(), store,
					"UPDATE $rid SET version = 'future' RETURN NONE",
					map[string]any{"rid": resolverCatalogMigrationID()},
				)
			},
		},
		{
			name: "caller marker absent", component: RetentionCallerArtifacts,
			mutate: func(t *testing.T, store *Surreal) {
				requireRetentionStatement(t, t.Context(), store,
					"DELETE $rid RETURN NONE",
					map[string]any{"rid": callerGenerationPublicationMigrationID()},
				)
			},
		},
		{
			name: "candidate table absent", component: RetentionCandidatePublication,
			mutate: func(t *testing.T, store *Surreal) {
				requireRetentionStatement(t, t.Context(), store,
					"REMOVE TABLE candidate_manifest_publication", nil,
				)
			},
		},
		{
			name: "focused table absent", component: RetentionFocusedRepositoryState,
			mutate: func(t *testing.T, store *Surreal) {
				requireRetentionStatement(t, t.Context(), store,
					"REMOVE TABLE repo", nil,
				)
			},
		},
		{
			name: "resolver table absent", component: RetentionResolverPublication,
			mutate: func(t *testing.T, store *Surreal) {
				requireRetentionStatement(t, t.Context(), store,
					"REMOVE TABLE resolver_catalog_publication", nil,
				)
			},
		},
		{
			name: "caller table absent", component: RetentionCallerArtifacts,
			mutate: func(t *testing.T, store *Surreal) {
				requireRetentionStatement(t, t.Context(), store,
					"REMOVE TABLE caller_generation_publication", nil,
				)
			},
		},
	}
	requests := []RetentionComponentRequest{
		derivedRetentionRequest(RetentionCandidatePublication, 2),
		derivedRetentionRequest(RetentionFocusedRepositoryState, 2),
		derivedRetentionRequest(RetentionResolverPublication, 2),
		derivedRetentionRequest(RetentionCallerArtifacts, 2),
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newRetentionTestStore(t)
			test.mutate(t, store)
			collection, err := store.CollectDerivedRetention(t.Context(), requests)
			if err != nil {
				t.Fatal(err)
			}
			failed := 0
			for _, result := range collection.Results {
				if result.Err == nil {
					continue
				}
				failed++
				if result.Component != test.component ||
					!errors.Is(result.Err, ErrRetentionComponentUnavailable) {
					t.Fatalf("unexpected localized result = %+v", result)
				}
			}
			if collection.CallerErr != nil {
				failed++
				if test.component != RetentionCallerArtifacts ||
					!errors.Is(collection.CallerErr, ErrRetentionComponentUnavailable) {
					t.Fatalf("unexpected caller error = %v", collection.CallerErr)
				}
			}
			if failed != 1 {
				t.Fatalf("localized failures = %d, want 1: %+v", failed, collection)
			}
		})
	}
}

func TestDerivedRetentionCallerFenceRejectsStaleAuthority(t *testing.T) {
	store := newRetentionTestStore(t)
	summary := seedDerivedCaller(t, store, 1)
	request := []RetentionComponentRequest{
		derivedRetentionRequest(RetentionCallerArtifacts, 2),
	}
	collection, err := store.CollectDerivedRetention(t.Context(), request)
	if err != nil || collection.CallerErr != nil ||
		len(collection.CallerAuthorities) != 1 {
		t.Fatalf("initial caller collection = %+v, %v", collection, err)
	}
	requireRetentionStatement(t, t.Context(), store,
		"UPDATE $rid SET control_revision = control_revision + 1 RETURN NONE",
		map[string]any{
			"rid": candidateManifestPublicationID(summary.Generation.Repository),
		},
	)
	collection, err = store.CollectDerivedRetention(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(collection.CallerErr, ErrRetentionComponentUnavailable) ||
		len(collection.CallerAuthorities) != 0 {
		t.Fatalf("stale caller collection = %+v", collection)
	}
}

func TestDerivedRetentionMalformedProjectionFailureLocalizes(t *testing.T) {
	store := newRetentionTestStore(t)
	requireRetentionStatement(t, t.Context(), store, `
REMOVE TABLE candidate_manifest_publication;
DEFINE TABLE candidate_manifest_publication SCHEMALESS;
CREATE candidate_manifest_publication:malformed CONTENT {
	repository: ['not', 'a', 'string']
} RETURN NONE;`, nil)
	collection, err := store.CollectDerivedRetention(t.Context(), []RetentionComponentRequest{
		derivedRetentionRequest(RetentionCandidatePublication, 2),
		derivedRetentionRequest(RetentionFocusedRepositoryState, 2),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(collection.Results) != 2 || collection.Results[0].Err == nil ||
		collection.Results[1].Err != nil {
		t.Fatalf("malformed projection localization = %+v", collection.Results)
	}
}

func TestDerivedRetentionCatalogProjectionIsFixed(t *testing.T) {
	store := newRetentionTestStore(t)
	tables, err := store.derivedRetentionCatalogTables(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]struct{}{
		"candidate_manifest_publication": {},
		"repo":                           {},
		"resolver_catalog_publication":   {},
		"caller_generation_publication":  {},
	}
	if !reflect.DeepEqual(tables, want) {
		t.Fatalf("catalog projection = %+v, want %+v", tables, want)
	}
	if strings.Contains(derivedRetentionCatalogSQL, "caller_leaf_outcome") ||
		strings.Contains(derivedRetentionCatalogSQL, "extraction_run") {
		t.Fatal("derived catalog projection contains an unrelated table")
	}
}

func TestDerivedRetentionPlansPushPhysicalLimitsWithoutSorts(t *testing.T) {
	store := newRetentionTestStore(t)
	for _, test := range []struct {
		name      string
		statement string
		table     string
	}{
		{name: "candidate", statement: derivedRetentionCandidateSQL, table: "candidate_manifest_publication"},
		{name: "focused", statement: derivedRetentionFocusedSQL, table: "repo"},
		{name: "resolver", statement: derivedRetentionResolverSQL, table: "resolver_catalog_publication"},
		{name: "caller", statement: derivedRetentionCallerSQL, table: "caller_generation_publication"},
	} {
		t.Run(test.name, func(t *testing.T) {
			requireRetentionExplain(
				t, t.Context(), store, test.statement+" EXPLAIN FULL",
				map[string]any{"scan_limit": 3}, "TableScan", test.table,
			)
		})
	}
}

func TestDerivedRetentionRequestValidation(t *testing.T) {
	valid := []RetentionComponentRequest{
		derivedRetentionRequest(RetentionCandidatePublication, 78),
		derivedRetentionRequest(RetentionFocusedRepositoryState, 78),
		derivedRetentionRequest(RetentionResolverPublication, 78),
		derivedRetentionRequest(RetentionCallerArtifacts, 78),
	}
	if err := validateDerivedRetentionRequests(valid); err != nil {
		t.Fatalf("exact max allocation: %v", err)
	}
	if gotReported := 4 * 78; gotReported != maxDerivedRetentionReportedIdentities {
		t.Fatalf("reported allocation = %d, want %d", gotReported, maxDerivedRetentionReportedIdentities)
	}
	if gotScan := 4 * 79; gotScan != maxDerivedRetentionScanIdentities {
		t.Fatalf("scan allocation = %d, want %d", gotScan, maxDerivedRetentionScanIdentities)
	}

	tests := []struct {
		name     string
		requests []RetentionComponentRequest
	}{
		{name: "too many", requests: append(append([]RetentionComponentRequest{}, valid...), valid[0])},
		{name: "duplicate", requests: []RetentionComponentRequest{valid[0], valid[0]}},
		{name: "unknown", requests: []RetentionComponentRequest{derivedRetentionRequest("unknown", 1)}},
		{name: "candidate files", requests: []RetentionComponentRequest{derivedRetentionRequest(RetentionCandidateFiles, 1)}},
		{name: "focused files", requests: []RetentionComponentRequest{derivedRetentionRequest(RetentionFocusedFiles, 1)}},
		{name: "resolver files", requests: []RetentionComponentRequest{derivedRetentionRequest(RetentionResolverFiles, 1)}},
		{name: "zero", requests: []RetentionComponentRequest{{Component: RetentionCandidatePublication, ScanIdentities: 1}}},
		{name: "above max", requests: []RetentionComponentRequest{derivedRetentionRequest(RetentionCandidatePublication, 79)}},
		{name: "missing sentinel", requests: []RetentionComponentRequest{{Component: RetentionCandidatePublication, ReportedIdentities: 1, ScanIdentities: 1}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateDerivedRetentionRequests(test.requests); err == nil {
				t.Fatal("invalid derived retention request was accepted")
			}
		})
	}
}

func TestDerivedRetentionComponentIDsAreExact(t *testing.T) {
	want := map[RetentionComponent]string{
		RetentionCandidatePublication:   "candidate_manifest_publication",
		RetentionCandidateFiles:         "$DATA/candidates managed publication files",
		RetentionFocusedRepositoryState: "repo indexed analysis-unit/revision state",
		RetentionFocusedFiles:           "$DATA/index focused publication files",
		RetentionResolverPublication:    "resolver_catalog_publication",
		RetentionResolverFiles:          "$DATA/resolver-catalogs package-owned files",
		RetentionCallerArtifacts:        "$DATA/caller-leaves managed manifests and leaf artifacts",
	}
	if len(want) != 7 {
		t.Fatalf("derived component identities = %d, want 7", len(want))
	}
	for component, value := range want {
		if string(component) != value {
			t.Fatalf("component = %q, want %q", component, value)
		}
	}
}

func TestDerivedRetentionCancellationIsTopLevel(t *testing.T) {
	store := newRetentionTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := store.CollectDerivedRetention(ctx, []RetentionComponentRequest{
		derivedRetentionRequest(RetentionCandidatePublication, 1),
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled collection = %v, want context.Canceled", err)
	}
}

func TestDerivedRetentionRecordIDValidation(t *testing.T) {
	rid := models.NewRecordID("repo", "example.com/derived/repository")
	if !validDerivedRetentionRecordID(&rid, "repo", "example.com/derived/repository") ||
		validDerivedRetentionRecordID(&rid, "repo", "example.com/derived/other") ||
		validDerivedRetentionRecordID(nil, "repo", "example.com/derived/repository") {
		t.Fatal("derived retention record ID validation is not exact")
	}
}
