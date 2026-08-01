package store

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"

	"github.com/bmeddeb/phebs/internal/callerleafid"
	"github.com/bmeddeb/phebs/internal/callerpublicationid"
)

type callerPublicationAggregateSeed struct {
	repository    string
	pairs         int
	canonicalByte int64
}

type callerPublicationFenceResult struct {
	OK bool `json:"ok"`
}

func callerPublicationFenceCurrent(
	t *testing.T,
	s *Surreal,
	currentRepository string,
	proposedPairs int,
	proposedCanonical int64,
	exactRetry bool,
	maxRepositories int,
	maxReferences int,
	maxCanonical int64,
) bool {
	t.Helper()
	results, err := surrealdb.Query[[]callerPublicationFenceResult](
		t.Context(), s.db,
		callerPublicationInstallationFenceSQL+"RETURN [{ ok: $installation_ok }];",
		map[string]any{
			"publication_rid":              callerGenerationPublicationID(currentRepository),
			"pair_count":                   proposedPairs,
			"canonical_bytes":              proposedCanonical,
			"same_publication":             exactRetry,
			"max_publication_repositories": maxRepositories,
			"max_publication_references":   maxReferences,
			"max_publication_canonical":    maxCanonical,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 {
		t.Fatalf("caller publication fence rows = %+v", rows)
	}
	return rows[0].OK
}

func seedCallerPublicationAggregate(
	t *testing.T,
	s *Surreal,
	rows ...callerPublicationAggregateSeed,
) {
	t.Helper()
	requireCandidateRawQuery(
		t, t.Context(), s,
		"DELETE caller_generation_publication RETURN NONE", nil,
	)
	for _, row := range rows {
		requireCandidateRawQuery(t, t.Context(), s, `
			CREATE $rid CONTENT {
				repository: $repository,
				generation: {
					repository: $repository,
					head_commit: 'seed', unit_digest: '',
					declaration_set_digest: 'seed',
					candidate_manifest_digest: 'seed',
					candidate_policy_digest: 'seed',
					candidate_control_revision: 1,
					resolver_generation_digest: 'seed',
					resolver_manifest_digest: 'seed',
					resolver_control_revision: 1,
					resolver_writer_schema: 'seed',
					source_lane_policy: 'seed', caller_policy_digest: 'seed',
					extractor_set_digest: 'seed', digest: 'seed'
				},
				generation_digest: 'seed', pairs: [], pair_set_digest: 'seed',
				pair_payload_digest: 'sha256:0000000000000000000000000000000000000000000000000000000000000000',
				pair_count: $pair_count,
				artifact_count: 0, result_count: 0, abstention_count: 0,
				canonical_bytes: $canonical_bytes,
				staging_bytes: 0, peak_open_files: 5,
				manifest_digest: 'seed', manifest_path: 'seed',
				publication_revision: 1,
				publication_incarnation: $publication_incarnation,
				writer_schema: $writer_schema, published_at: time::now()
			} RETURN NONE;
		`, map[string]any{
			"rid":                     callerGenerationPublicationID(row.repository),
			"repository":              row.repository,
			"pair_count":              row.pairs,
			"canonical_bytes":         row.canonicalByte,
			"publication_incarnation": internalCallerDigest('0'),
			"writer_schema":           CallerGenerationPublicationWriterSchema,
		})
	}
}

func TestCallerPublicationInstallationFenceUsesReplacementProjection(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	s, err := OpenLocal(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })

	tests := []struct {
		name              string
		rows              []callerPublicationAggregateSeed
		currentRepository string
		proposedPairs     int
		proposedCanonical int64
		exactRetry        bool
		maxRepositories   int
		maxReferences     int
		maxCanonical      int64
		want              bool
	}{
		{
			name: "exact aggregate caps admit",
			rows: []callerPublicationAggregateSeed{
				{repository: "github.com/acme/other", pairs: 1, canonicalByte: 6},
			},
			currentRepository: "github.com/acme/proposed",
			proposedPairs:     0, proposedCanonical: 4,
			maxRepositories: 2, maxReferences: 3, maxCanonical: 10,
			want: true,
		},
		{
			name: "repository cap plus one refuses",
			rows: []callerPublicationAggregateSeed{
				{repository: "github.com/acme/other", pairs: 0, canonicalByte: 0},
			},
			currentRepository: "github.com/acme/proposed",
			maxRepositories:   1, maxReferences: 10, maxCanonical: 10,
		},
		{
			name: "manifest and pair reference cap plus one refuses",
			rows: []callerPublicationAggregateSeed{
				{repository: "github.com/acme/other", pairs: 1, canonicalByte: 0},
			},
			currentRepository: "github.com/acme/proposed",
			maxRepositories:   2, maxReferences: 2, maxCanonical: 10,
		},
		{
			name: "canonical byte cap plus one refuses",
			rows: []callerPublicationAggregateSeed{
				{repository: "github.com/acme/other", pairs: 0, canonicalByte: 6},
			},
			currentRepository: "github.com/acme/proposed",
			proposedCanonical: 5,
			maxRepositories:   2, maxReferences: 2, maxCanonical: 10,
		},
		{
			name: "replacement excludes its current contribution",
			rows: []callerPublicationAggregateSeed{
				{repository: "github.com/acme/replaced", pairs: 2, canonicalByte: 9},
			},
			currentRepository: "github.com/acme/replaced",
			proposedPairs:     1, proposedCanonical: 5,
			maxRepositories: 1, maxReferences: 2, maxCanonical: 5,
			want: true,
		},
		{
			name: "exact retry remains a no-op above a later lowered fence",
			rows: []callerPublicationAggregateSeed{
				{repository: "github.com/acme/retried", pairs: 2, canonicalByte: 7},
				{repository: "github.com/acme/other", pairs: 2, canonicalByte: 7},
			},
			currentRepository: "github.com/acme/retried",
			proposedPairs:     2, proposedCanonical: 7, exactRetry: true,
			maxRepositories: 1, maxReferences: 1, maxCanonical: 1,
			want: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			seedCallerPublicationAggregate(t, s, test.rows...)
			got := callerPublicationFenceCurrent(
				t, s, test.currentRepository,
				test.proposedPairs, test.proposedCanonical, test.exactRetry,
				test.maxRepositories, test.maxReferences, test.maxCanonical,
			)
			if got != test.want {
				t.Fatalf("installation fence = %t, want %t", got, test.want)
			}
		})
	}
}

func seedCallerPublicationPayloadIntegrity(
	t *testing.T,
	s *Surreal,
	repository string,
) (CallerGenerationPublication, Job) {
	t.Helper()
	ctx := t.Context()
	generationInput := internalCallerGeneration()
	generationInput.Repository = repository
	generationInput.Digest = ""
	generation, err := prepareCallerGeneration(generationInput)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := prepareCallerLeafPair(generation, CallerLeafPair{
		Domain: "grpc-caller", ExtractorVersion: "1.0.0",
		LeafAdapterVersion: "direct-syntax-base-v1",
		LeafOrdinal:        0, LeafPrefix: "00", LeafPrefixBits: 2,
		CandidateMemberName: "candidate.ndjson", CandidateRecordCount: 2,
		CandidateDeclaredBytes: 2, CandidateContentBytes: 2,
		CandidateContentDigest: internalCallerDigest('8'),
	})
	if err != nil {
		t.Fatal(err)
	}
	contentDigest := internalCallerDigest('9')
	receipt := CallerLeafArtifactReceipt{
		ArtifactName:  callerleafid.ArtifactName(pair.PairDigest, contentDigest),
		ArtifactCount: 1, ResultCount: 1, CanonicalBytes: 1, StagingBytes: 1,
		ContentDigest: contentDigest, MetadataDigest: internalCallerDigest('a'),
	}
	outcome, err := prepareCallerLeafOutcome(CallerLeafOutcome{
		Generation: generation, Pair: pair, Disposition: CallerLeafSucceeded,
		Receipt: &receipt,
	})
	if err != nil {
		t.Fatal(err)
	}
	admission, _, err := prepareCallerGenerationAdmission(
		CallerGenerationAdmission{
			Generation: generation, Disposition: CallerGenerationAdmitted,
			PeakOpenFiles: maxCallerPeakOpenFiles,
		},
		[]CallerLeafPair{pair}, []CallerLeafOutcome{outcome},
	)
	if err != nil {
		t.Fatal(err)
	}
	manifestDigest := internalCallerDigest('b')
	publication, _, err := prepareCallerGenerationPublication(
		CallerGenerationPublication{
			Generation: generation,
			Pairs: []CallerGenerationPairPublication{{
				Pair: pair, Receipt: receipt,
			}},
			ManifestDigest: manifestDigest,
			ManifestPath: callerpublicationid.ManifestName(
				generation.Digest, manifestDigest,
			),
		},
		admission, []CallerLeafOutcome{outcome},
	)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.UpsertRepo(ctx, Repo{
		Name: repository, CloneURL: "https://" + repository + ".git",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoIndexed(
		ctx, repository, generation.HeadCommit, time.Now().UTC(),
	); err != nil {
		t.Fatal(err)
	}
	requireCandidateRawQuery(t, ctx, s, `
		CREATE $candidate_rid CONTENT {
			repository: $repository, head_commit: $head_commit,
			unit_digest: $unit_digest, policy_digest: $candidate_policy_digest,
			manifest_digest: $candidate_manifest_digest,
			generation_digest: $candidate_manifest_digest,
			manifest_path: 'candidate.json', control_revision: 1,
			published_at: time::now()
		} RETURN NONE;
		CREATE $resolver_rid CONTENT {
			repository: $repository, head_commit: $head_commit,
			unit_digest: $unit_digest, declarations: [],
			declaration_set_digest: $declaration_set_digest,
			candidate_manifest_digest: $candidate_manifest_digest,
			source_lane_policy: $source_lane_policy, resolver_packs: [],
			resolver_pack_set_digest: $resolver_generation_digest,
			catalog_policy_digest: $resolver_generation_digest,
			generation_digest: $resolver_generation_digest,
			manifest_digest: $resolver_manifest_digest,
			manifest_path: 'resolver.json', control_revision: 1,
			writer_schema: $resolver_writer_schema,
			published_at: time::now()
		} RETURN NONE;
	`, map[string]any{
		"candidate_rid": candidateManifestPublicationID(repository),
		"resolver_rid":  resolverCatalogPublicationID(repository),
		"repository":    repository, "head_commit": generation.HeadCommit,
		"unit_digest":                generation.UnitDigest,
		"declaration_set_digest":     generation.DeclarationSetDigest,
		"candidate_manifest_digest":  generation.CandidateManifestDigest,
		"candidate_policy_digest":    generation.CandidatePolicyDigest,
		"source_lane_policy":         generation.SourceLanePolicy,
		"resolver_generation_digest": generation.ResolverGenerationDigest,
		"resolver_manifest_digest":   generation.ResolverManifestDigest,
		"resolver_writer_schema":     generation.ResolverWriterSchema,
	})
	if _, err := s.EnqueuePending(
		ctx, JobCallerLeaf, repository, false,
	); err != nil {
		t.Fatal(err)
	}
	job, err := s.ClaimJob(ctx, JobCallerLeaf, "pair-payload-test")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetJobStatus(ctx, *job, StatusRunning, ""); err != nil {
		t.Fatal(err)
	}
	job.Status = StatusRunning
	if err := s.RecordCallerLeafOutcome(ctx, *job, outcome); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordCallerGenerationAdmission(
		ctx, *job, admission, []CallerLeafPair{pair},
	); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishCallerGeneration(ctx, *job, publication); err != nil {
		t.Fatal(err)
	}
	persisted, err := s.GetCallerGenerationPublication(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	return *persisted, *job
}

func TestCallerPublicationSummaryCurrentRejectsRawPairPayloadMutation(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	s, err := OpenLocal(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	repository := "github.com/acme/caller-pair-payload-integrity"
	publication, job := seedCallerPublicationPayloadIntegrity(t, s, repository)

	summary, err := s.GetCallerGenerationPublicationSummary(
		t.Context(), repository,
	)
	if err != nil {
		t.Fatal(err)
	}
	current, err := s.CallerGenerationPublicationSummaryCurrent(
		t.Context(), *summary,
	)
	if err != nil || !current {
		t.Fatalf("initial summary current = %t, %v", current, err)
	}
	current, err = s.CallerGenerationPublicationSummaryAuthorityCurrent(
		t.Context(), *summary,
	)
	if err != nil || !current {
		t.Fatalf("initial scalar authority current = %t, %v", current, err)
	}
	// Model the authority shape produced by delete/recreate/identical republish:
	// repository-local revision and second-precision time may repeat, while the
	// newly owned writer job produces a different incarnation. The old binding
	// must fail both scalar and exact fences.
	oldIncarnation := summary.PublicationIncarnation
	newIncarnation := internalCallerDigest('f')
	if newIncarnation == oldIncarnation {
		newIncarnation = internalCallerDigest('e')
	}
	requireCandidateRawQuery(t, t.Context(), s,
		"UPDATE $rid SET publication_incarnation = $incarnation RETURN NONE",
		map[string]any{
			"rid":         callerGenerationPublicationID(repository),
			"incarnation": newIncarnation,
		})
	current, err = s.CallerGenerationPublicationSummaryAuthorityCurrent(
		t.Context(), *summary,
	)
	if err != nil || current {
		t.Fatalf("recreated scalar authority current = %t, %v", current, err)
	}
	current, err = s.CallerGenerationPublicationSummaryCurrent(
		t.Context(), *summary,
	)
	if err != nil || current {
		t.Fatalf("recreated exact authority current = %t, %v", current, err)
	}
	requireCandidateRawQuery(t, t.Context(), s,
		"UPDATE $rid SET publication_incarnation = $incarnation RETURN NONE",
		map[string]any{
			"rid":         callerGenerationPublicationID(repository),
			"incarnation": oldIncarnation,
		})

	// SourceBlobReads is a valid, non-aggregate receipt field. Mutating it
	// leaves every scalar summary and every independently validated pair field
	// plausible, so only the writer-owned payload commitment detects the drift.
	tampered := append([]CallerGenerationPairPublication(nil), publication.Pairs...)
	tampered[0].Receipt.SourceBlobReads = 1
	requireCandidateRawQuery(t, t.Context(), s,
		"UPDATE $rid SET pairs = $pairs RETURN NONE", map[string]any{
			"rid": callerGenerationPublicationID(repository), "pairs": tampered,
		})

	// Scalar summary reads are intentionally pair-free and untrusted. The exact
	// current fence performs the bounded server-side payload hash and fails shut.
	if _, err := s.GetCallerGenerationPublicationSummary(
		t.Context(), repository,
	); err != nil {
		t.Fatalf("pair-free summary projection: %v", err)
	}
	summaries, err := s.ListCallerGenerationPublicationSummaries(t.Context())
	if err != nil || len(summaries) != 1 {
		t.Fatalf("pair-free summary inventory = %+v, %v", summaries, err)
	}
	current, err = s.CallerGenerationPublicationSummaryAuthorityCurrent(
		t.Context(), *summary,
	)
	if err != nil || !current {
		t.Fatalf("tampered scalar authority current = %t, %v", current, err)
	}
	current, err = s.CallerGenerationPublicationSummaryCurrent(
		t.Context(), *summary,
	)
	if !errors.Is(err, ErrInvalidCallerGenerationPublication) || current {
		t.Fatalf("tampered exact summary current = %t, %v", current, err)
	}
	if _, err := s.GetCallerGenerationPublication(
		t.Context(), repository,
	); !errors.Is(err, ErrInvalidCallerGenerationPublication) {
		t.Fatalf("tampered full pointer = %v, want invalid publication", err)
	}

	// A malformed current payload is corruption, not a second deterministic
	// result for the same generation. The exact admitted writer may replace it
	// and must advance the repository-local visibility revision.
	if err := s.PublishCallerGeneration(
		t.Context(), job, publication,
	); err != nil {
		t.Fatalf("repair pair payload: %v", err)
	}
	repaired, err := s.GetCallerGenerationPublication(t.Context(), repository)
	if err != nil {
		t.Fatal(err)
	}
	if repaired.PublicationRevision != publication.PublicationRevision+1 ||
		repaired.Pairs[0].Receipt.SourceBlobReads != 0 {
		t.Fatalf("repaired publication = %+v", repaired)
	}
	repairedSummary, err := s.GetCallerGenerationPublicationSummary(
		t.Context(), repository,
	)
	if err != nil {
		t.Fatal(err)
	}
	current, err = s.CallerGenerationPublicationSummaryCurrent(
		t.Context(), *repairedSummary,
	)
	if err != nil || !current {
		t.Fatalf("repaired summary current = %t, %v", current, err)
	}

	// Model a pre-hardening row whose prefix/length are plausible but payload
	// is not canonical lowercase hex. The exact writer must treat it as
	// repairable corruption instead of taking the same-publication no-op branch.
	requireCandidateRawQuery(t, t.Context(), s, `
		REMOVE FIELD publication_incarnation ON caller_generation_publication;
		DEFINE FIELD publication_incarnation ON caller_generation_publication TYPE string
			ASSERT string::len($value) = 71
				AND string::starts_with($value, 'sha256:');
		UPDATE $rid SET publication_incarnation = $invalid RETURN NONE;
		DEFINE FIELD OVERWRITE publication_incarnation ON caller_generation_publication TYPE string
			ASSERT string::len($value) = 71
				AND string::starts_with($value, 'sha256:')
				AND string::is_hexadecimal(string::slice($value, 7))
				AND string::lowercase($value) = $value;
	`, map[string]any{
		"rid":     callerGenerationPublicationID(repository),
		"invalid": "sha256:" + strings.Repeat("z", 64),
	})
	if _, err := s.GetCallerGenerationPublication(
		t.Context(), repository,
	); !errors.Is(err, ErrInvalidCallerGenerationPublication) {
		t.Fatalf("nonhex incarnation = %v, want invalid publication", err)
	}
	if err := s.PublishCallerGeneration(
		t.Context(), job, publication,
	); err != nil {
		t.Fatalf("repair nonhex incarnation: %v", err)
	}
	incarnationRepaired, err := s.GetCallerGenerationPublication(
		t.Context(), repository,
	)
	if err != nil || incarnationRepaired.PublicationRevision !=
		repaired.PublicationRevision+1 ||
		!validSHA256Digest(incarnationRepaired.PublicationIncarnation) {
		t.Fatalf("incarnation-repaired publication = %+v, %v", incarnationRepaired, err)
	}
}

func TestCallerGenerationPublicationMigrationAndWriterGuard(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx := context.Background()
	s, err := OpenLocal(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	repository := "github.com/acme/caller-publication-migration"
	if err := s.UpsertRepo(ctx, Repo{
		Name: repository, CloneURL: "https://" + repository + ".git",
	}); err != nil {
		t.Fatal(err)
	}
	requireCandidateRawQuery(t, ctx, s, `
		DELETE $marker RETURN NONE;
		UPDATE $repo SET caller_publication_revision = NONE RETURN NONE;
	`, map[string]any{
		"marker": callerGenerationPublicationMigrationID(),
		"repo":   repoID(repository),
	})
	if err := s.migrateCallerGenerationPublications(ctx); err != nil {
		t.Fatalf("migrate missing revision: %v", err)
	}
	repo, err := s.GetRepo(ctx, repository)
	if err != nil || repo.CallerPublicationRevision != 0 {
		t.Fatalf("migrated repo = %+v, %v", repo, err)
	}
	marker, err := surrealdb.Query[[]struct {
		Version string `json:"version"`
	}](ctx, s.db, "SELECT version FROM $rid", map[string]any{
		"rid": callerGenerationPublicationMigrationID(),
	})
	if err != nil || len(firstDomainRows(marker)) != 1 ||
		firstDomainRows(marker)[0].Version != callerGenerationPublicationMigrationVersion {
		t.Fatalf("migration marker = %+v, %v", marker, err)
	}
	// The supported v1 upgrade preserves current pointer visibility in place
	// while adding a repository-specific incarnation. Process restart has
	// already invalidated every process-local T30.6j request binding.
	requireCandidateRawQuery(t, ctx, s, `
		UPDATE $marker SET version = $prior RETURN NONE;
		REMOVE FIELD publication_incarnation ON caller_generation_publication;
		CREATE $publication CONTENT {
			repository: $repository,
			generation: {
				repository: $repository,
				head_commit: 'seed', unit_digest: '',
				declaration_set_digest: 'seed',
				candidate_manifest_digest: 'seed',
				candidate_policy_digest: 'seed',
				candidate_control_revision: 1,
				resolver_generation_digest: 'seed',
				resolver_manifest_digest: 'seed',
				resolver_control_revision: 1,
				resolver_writer_schema: 'seed',
				source_lane_policy: 'seed', caller_policy_digest: 'seed',
				extractor_set_digest: 'seed', digest: 'seed'
			},
			generation_digest: 'seed', pairs: [],
			pair_payload_digest: $pair_payload_digest,
			pair_set_digest: 'seed', pair_count: 0,
			artifact_count: 0, result_count: 0, abstention_count: 0,
			canonical_bytes: 0, staging_bytes: 0, peak_open_files: 5,
			manifest_digest: 'seed', manifest_path: 'seed',
			publication_revision: 1,
			writer_schema: $writer_schema,
			published_at: time::now()
		} RETURN NONE;
		DEFINE FIELD publication_incarnation ON caller_generation_publication TYPE string
			ASSERT string::len($value) = 71
				AND string::starts_with($value, 'sha256:')
				AND string::is_hexadecimal(string::slice($value, 7))
				AND string::lowercase($value) = $value;
	`, map[string]any{
		"marker":              callerGenerationPublicationMigrationID(),
		"prior":               callerGenerationPublicationPriorMigration,
		"publication":         callerGenerationPublicationID(repository),
		"repository":          repository,
		"pair_payload_digest": internalCallerDigest('0'),
		"writer_schema":       CallerGenerationPublicationWriterSchema,
	})
	if err := s.migrateCallerGenerationPublications(ctx); err != nil {
		t.Fatalf("upgrade v1 caller publication marker: %v", err)
	}
	upgraded, err := surrealdb.Query[[]struct {
		Incarnation string `json:"publication_incarnation"`
	}](ctx, s.db, "SELECT publication_incarnation FROM $rid", map[string]any{
		"rid": callerGenerationPublicationID(repository),
	})
	if err != nil || len(firstDomainRows(upgraded)) != 1 ||
		!validSHA256Digest(firstDomainRows(upgraded)[0].Incarnation) {
		t.Fatalf("upgraded publication incarnation = %+v, %v", upgraded, err)
	}
	guarded, queryErr := surrealdb.Query[any](ctx, s.db, `
		CREATE caller_generation_publication CONTENT {
			writer_schema: 'phebs-caller-generation-publication-store-retired'
		};`, nil)
	failed := queryErr != nil
	if guarded != nil {
		for _, result := range *guarded {
			failed = failed || result.Error != nil
		}
	}
	if !failed {
		t.Fatal("retired caller publication writer bypassed generation guard")
	}
	identity := CurrentStoreIdentity()
	if identity.CallerPublicationWriter != CallerGenerationPublicationWriterSchema ||
		identity.CallerPublicationMigration != callerGenerationPublicationMigrationVersion {
		t.Fatalf("store identity = %+v", identity)
	}
	requireCandidateRawQuery(t, ctx, s,
		"UPDATE $marker SET version = 'future-writer' RETURN NONE",
		map[string]any{"marker": callerGenerationPublicationMigrationID()})
	if err := s.migrateCallerGenerationPublications(ctx); err == nil ||
		!strings.Contains(err.Error(), "unsupported caller-generation publication writer") {
		t.Fatalf("future writer marker = %v", err)
	}
}

func TestCallerPublicationRestoreClearUsesRecordIdentityForMalformedPointer(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx := context.Background()
	s, err := OpenLocal(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	withPointer := "github.com/acme/caller-restore-pointer"
	withoutPointer := "github.com/acme/caller-restore-none"
	for _, repository := range []string{withPointer, withoutPointer} {
		if err := s.UpsertRepo(ctx, Repo{
			Name: repository, CloneURL: "https://" + repository + ".git",
		}); err != nil {
			t.Fatal(err)
		}
	}
	requireCandidateRawQuery(t, ctx, s, `
		CREATE $pointer CONTENT {
			repository: $stored_repository,
			generation: {
				repository: $repository, head_commit: 'malformed', unit_digest: '',
				declaration_set_digest: 'malformed',
				candidate_manifest_digest: 'malformed',
				candidate_policy_digest: 'malformed', candidate_control_revision: 1,
				resolver_generation_digest: 'malformed',
				resolver_manifest_digest: 'malformed', resolver_control_revision: 1,
				resolver_writer_schema: 'malformed', source_lane_policy: 'malformed',
				caller_policy_digest: 'malformed', extractor_set_digest: 'malformed',
				digest: 'malformed'
			},
			generation_digest: 'malformed', pairs: [],
			pair_payload_digest: 'malformed', pair_set_digest: 'malformed',
			pair_count: 0, artifact_count: 0, result_count: 0,
			abstention_count: 0, canonical_bytes: 0, staging_bytes: 0,
			peak_open_files: 0, manifest_digest: 'malformed',
			manifest_path: 'malformed', publication_revision: 1,
			publication_incarnation: $publication_incarnation,
			writer_schema: $writer_schema, published_at: time::now()
		};
		CREATE caller_leaf_outcome CONTENT {
			repository: $repository,
			generation: {}, generation_digest: 'malformed', pair: {},
			domain: 'grpc-caller', leaf_prefix: '00',
			disposition: 'succeeded', receipt: NONE,
			writer_schema: $leaf_writer_schema, recorded_at: time::now()
		};
		CREATE caller_generation_admission CONTENT {
			repository: $repository,
			generation: {}, generation_digest: 'malformed',
			disposition: 'admitted', pair_set_digest: 'malformed',
			pair_count: 0, artifact_count: 0, result_count: 0,
			abstention_count: 0, canonical_bytes: 0, staging_bytes: 0,
			peak_open_files: 0, writer_schema: $leaf_writer_schema,
			recorded_at: time::now()
		};
	`, map[string]any{
		"pointer":                 callerGenerationPublicationID(withPointer),
		"repository":              withPointer,
		"stored_repository":       withoutPointer,
		"publication_incarnation": internalCallerDigest('0'),
		"writer_schema":           CallerGenerationPublicationWriterSchema,
		"leaf_writer_schema":      CallerLeafWriterSchema,
	})
	if _, err := s.GetCallerGenerationPublication(
		ctx, withPointer,
	); !errors.Is(err, ErrInvalidCallerGenerationPublication) {
		t.Fatalf("malformed pointer = %v, want invalid publication", err)
	}
	if err := s.ClearAllCallerPublicationStateForRestore(ctx); err != nil {
		t.Fatalf("restore clear: %v", err)
	}
	if _, err := s.GetCallerGenerationPublication(
		ctx, withPointer,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pointer after restore clear = %v", err)
	}
	for _, table := range []string{
		"caller_generation_publication", "caller_leaf_outcome",
		"caller_generation_admission",
	} {
		rows, queryErr := surrealdb.Query[[]struct {
			ID any `json:"id"`
		}](ctx, s.db, "SELECT id FROM "+table, nil)
		if queryErr != nil || len(firstDomainRows(rows)) != 0 {
			t.Fatalf("%s after restore clear = %+v, %v", table, rows, queryErr)
		}
	}
	affected, err := s.GetRepo(ctx, withPointer)
	if err != nil || affected.CallerPublicationRevision != 1 {
		t.Fatalf("affected restore revision = %+v, %v", affected, err)
	}
	unaffected, err := s.GetRepo(ctx, withoutPointer)
	if err != nil || unaffected.CallerPublicationRevision != 0 {
		t.Fatalf("unaffected restore revision = %+v, %v", unaffected, err)
	}
}
