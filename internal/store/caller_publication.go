package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"

	"github.com/bmeddeb/phebs/internal/callerpublicationid"
	"github.com/bmeddeb/phebs/internal/reponame"
)

const (
	CallerGenerationPublicationWriterSchema     = callerpublicationid.WriterSchema
	callerGenerationPublicationMigrationVersion = "t30.6i-caller-generation-publication-writer-v1"
	// MaxCallerPublicationRepositories bounds the one startup pointer-summary
	// query. Pair receipts are deliberately excluded from that query.
	MaxCallerPublicationRepositories = callerpublicationid.InstallationPublicationRepositories
	// MaxCallerPublicationRepositoryPage bounds each keyset page used to find
	// indexed, non-deleting repositories that may need marker repair.
	MaxCallerPublicationRepositoryPage = 512
	maxCallerPublicationReferences     = callerpublicationid.InstallationPublicationRefs
	maxCallerPublicationCanonical      = callerpublicationid.InstallationCanonicalBytes
)

// CallerGenerationPairPublication is the exact ordered artifact receipt for
// one expected domain/leaf pair. RecordCount is derived from the immutable
// result and abstention counts; callers cannot supply a different value.
type CallerGenerationPairPublication struct {
	Pair        CallerLeafPair            `json:"pair"`
	Receipt     CallerLeafArtifactReceipt `json:"receipt"`
	RecordCount int                       `json:"record_count"`
}

// CallerGenerationPublication is the sole product-visible authority over a
// complete caller generation. Pair and aggregate fields are recomputed from
// the admitted immutable outcome set. PublicationRevision, WriterSchema, and
// PublishedAt belong exclusively to the store.
type CallerGenerationPublication struct {
	Generation CallerGenerationIdentity          `json:"generation"`
	Pairs      []CallerGenerationPairPublication `json:"pairs"`
	// PairPayloadDigest is store-owned integrity metadata over Pairs. Callers
	// carry it between exact reads and current checks but never compute it.
	PairPayloadDigest   string    `json:"pair_payload_digest"`
	PairSetDigest       string    `json:"pair_set_digest"`
	PairCount           int       `json:"pair_count"`
	ArtifactCount       int       `json:"artifact_count"`
	ResultCount         int       `json:"result_count"`
	AbstentionCount     int       `json:"abstention_count"`
	CanonicalBytes      int64     `json:"canonical_bytes"`
	StagingBytes        int64     `json:"staging_bytes"`
	PeakOpenFiles       int       `json:"peak_open_files"`
	ManifestDigest      string    `json:"manifest_digest"`
	ManifestPath        string    `json:"manifest_path"`
	PublicationRevision uint64    `json:"publication_revision"`
	WriterSchema        string    `json:"writer_schema"`
	PublishedAt         time.Time `json:"published_at"`
}

// CallerGenerationPublicationSummary is the bounded startup and warm-current
// projection of one pointer. It deliberately excludes the potentially
// 16,384-element pair list; the manifest digest and aggregate fields bind the
// cold-validated filesystem receipt while product reads remain free to request
// the full pointer.
type CallerGenerationPublicationSummary struct {
	Generation          CallerGenerationIdentity `json:"generation"`
	PairPayloadDigest   string                   `json:"pair_payload_digest"`
	PairSetDigest       string                   `json:"pair_set_digest"`
	PairCount           int                      `json:"pair_count"`
	ArtifactCount       int                      `json:"artifact_count"`
	ResultCount         int                      `json:"result_count"`
	AbstentionCount     int                      `json:"abstention_count"`
	CanonicalBytes      int64                    `json:"canonical_bytes"`
	StagingBytes        int64                    `json:"staging_bytes"`
	PeakOpenFiles       int                      `json:"peak_open_files"`
	ManifestDigest      string                   `json:"manifest_digest"`
	ManifestPath        string                   `json:"manifest_path"`
	PublicationRevision uint64                   `json:"publication_revision"`
	WriterSchema        string                   `json:"writer_schema"`
	PublishedAt         time.Time                `json:"published_at"`
}

type CallerGenerationPublicationStore interface {
	PublishCallerGeneration(context.Context, Job, CallerGenerationPublication) error
	GetCallerGenerationPublication(context.Context, string) (*CallerGenerationPublication, error)
	GetCallerGenerationPublicationSummary(context.Context, string) (*CallerGenerationPublicationSummary, error)
	ListCallerGenerationPublicationSummaries(context.Context) ([]CallerGenerationPublicationSummary, error)
	CallerGenerationPublicationCurrent(context.Context, CallerGenerationPublication) (bool, error)
	CallerGenerationPublicationSummaryAuthorityCurrent(context.Context, CallerGenerationPublicationSummary) (bool, error)
	CallerGenerationPublicationSummaryCurrent(context.Context, CallerGenerationPublicationSummary) (bool, error)
	ClearCallerGenerationPublication(context.Context, string) error
	ClearAllCallerPublicationStateForRestore(context.Context) error
}

var _ CallerGenerationPublicationStore = (*Surreal)(nil)

type callerGenerationPublicationRec struct {
	CallerGenerationPublication
	Repository       string           `json:"repository"`
	GenerationDigest string           `json:"generation_digest"`
	PairPayloadValid bool             `json:"pair_payload_valid"`
	RecID            *models.RecordID `json:"id"`
}

type callerGenerationPublicationSummaryRec struct {
	CallerGenerationPublicationSummary
	Repository       string           `json:"repository"`
	GenerationDigest string           `json:"generation_digest"`
	PairPayloadCount int              `json:"pair_payload_count"`
	RecID            *models.RecordID `json:"id"`
}

type callerGenerationCurrentResult struct {
	Current            bool `json:"current"`
	PairPayloadInvalid bool `json:"pair_payload_invalid"`
}

type callerPublicationRepositoryEligibleResult struct {
	Eligible bool `json:"eligible"`
}

type callerPublicationRepositoryRec struct {
	Name  string           `json:"name"`
	RecID *models.RecordID `json:"id"`
}

type callerPublicationOutcomeProjection struct {
	Pair        CallerLeafPair            `json:"pair"`
	Receipt     CallerLeafArtifactReceipt `json:"receipt"`
	Disposition CallerLeafDisposition     `json:"disposition"`
	Domain      string                    `json:"domain"`
	LeafPrefix  string                    `json:"leaf_prefix"`
}

// pair_payload_digest is an internal corruption commitment over Surreal's
// canonical string representation of the schema-full pair array. It is not a
// product identity. Get/List summary calls transfer only this scalar plus the
// array length; they never hash or transfer pairs. Exact authority checks hash
// only after the actual length equals the already validated bounded pair_count.
const callerGenerationPairPayloadDigestSQL = `'sha256:' + crypto::sha256(type::string(pairs))`
const callerGenerationBoundPairPayloadDigestSQL = `'sha256:' + crypto::sha256(type::string($pairs))`

const callerGenerationPublicationSummaryProjection = `id, repository,
	generation, generation_digest, pair_set_digest, pair_count, artifact_count,
	result_count, abstention_count, canonical_bytes, staging_bytes,
	peak_open_files, manifest_digest, manifest_path, publication_revision,
	writer_schema, published_at, pair_payload_digest,
	array::len(pairs) AS pair_payload_count`

func callerGenerationPublicationID(repository string) models.RecordID {
	return models.NewRecordID("caller_generation_publication", repository)
}

func callerGenerationPublicationMigrationID() models.RecordID {
	return models.NewRecordID(
		"store_migration", "caller_generation_publication_writer",
	)
}

func validCallerGenerationPublicationRecordID(
	row callerGenerationPublicationRec,
) bool {
	if row.RecID == nil || row.RecID.Table != "caller_generation_publication" {
		return false
	}
	identifier, ok := row.RecID.ID.(string)
	return ok && identifier == row.Repository
}

func validCallerGenerationPublicationSummaryRecordID(
	row callerGenerationPublicationSummaryRec,
) bool {
	if row.RecID == nil || row.RecID.Table != "caller_generation_publication" {
		return false
	}
	identifier, ok := row.RecID.ID.(string)
	return ok && identifier == row.Repository
}

func validateCallerGenerationPublicationSummary(
	row callerGenerationPublicationSummaryRec,
) (CallerGenerationPublicationSummary, error) {
	summary := row.CallerGenerationPublicationSummary
	originalDigest := summary.Generation.Digest
	generation, err := prepareCallerGeneration(summary.Generation)
	if err != nil || originalDigest != generation.Digest ||
		row.GenerationDigest != generation.Digest ||
		row.Repository != generation.Repository ||
		!validCallerGenerationPublicationSummaryRecordID(row) ||
		!validSHA256Digest(summary.PairPayloadDigest) ||
		!validSHA256Digest(summary.PairSetDigest) ||
		summary.PairCount < 0 || summary.PairCount > maxCallerGenerationPairs ||
		summary.ArtifactCount != summary.PairCount ||
		summary.ResultCount < 0 || summary.ResultCount > maxCallerGenerationResults ||
		summary.AbstentionCount < 0 ||
		summary.AbstentionCount > maxCallerGenerationAbstentions ||
		summary.CanonicalBytes < 0 ||
		summary.CanonicalBytes > maxCallerGenerationCanonical ||
		summary.StagingBytes != summary.CanonicalBytes ||
		summary.PeakOpenFiles != maxCallerPeakOpenFiles ||
		!validSHA256Digest(summary.ManifestDigest) ||
		summary.ManifestPath != callerpublicationid.ManifestName(
			generation.Digest, summary.ManifestDigest,
		) || summary.PublicationRevision == 0 ||
		summary.WriterSchema != CallerGenerationPublicationWriterSchema ||
		summary.PublishedAt.IsZero() {
		return CallerGenerationPublicationSummary{},
			ErrInvalidCallerGenerationPublication
	}
	summary.Generation = generation
	summary.PublishedAt = summary.PublishedAt.UTC()
	return summary, nil
}

func validatePersistedCallerGenerationPublicationSummary(
	row callerGenerationPublicationSummaryRec,
) (CallerGenerationPublicationSummary, error) {
	summary, err := validateCallerGenerationPublicationSummary(row)
	if err != nil || row.PairPayloadCount != summary.PairCount {
		return CallerGenerationPublicationSummary{},
			ErrInvalidCallerGenerationPublication
	}
	return summary, nil
}

// migrateCallerGenerationPublications installs both the immutable writer
// generation and the retained repository-local revision counter. There was no
// supported pre-T30.6i pointer, so all prototype rows are retired on the first
// install instead of being promoted into visibility.
func (s *Surreal) migrateCallerGenerationPublications(ctx context.Context) error {
	results, err := surrealdb.Query[any](ctx, s.db, `
BEGIN;
LET $current = (SELECT version FROM $marker LIMIT 1)[0].version;
IF $current != NONE AND $current != $wanted {
	THROW 'phebs-permanent: unsupported caller-generation publication writer generation'
};
UPDATE repo SET caller_publication_revision = 0
	WHERE $current = NONE
		AND (caller_publication_revision = NONE OR caller_publication_revision < 0)
	RETURN NONE;
LET $retired_repositories = SELECT VALUE record::id(id)
	FROM caller_generation_publication
	WHERE $current = NONE;
UPDATE repo SET caller_publication_revision =
	(caller_publication_revision ?? 0) + 1
	WHERE name IN $retired_repositories RETURN NONE;
DELETE caller_generation_publication
	WHERE $current = NONE RETURN NONE;
UPSERT $marker SET
	version = IF $current = NONE THEN $wanted ELSE $current END,
	completed_at = IF $current = NONE THEN time::now() ELSE completed_at END
	RETURN NONE;
COMMIT;`, map[string]any{
		"marker": callerGenerationPublicationMigrationID(),
		"wanted": callerGenerationPublicationMigrationVersion,
	})
	if err != nil {
		return fmt.Errorf("migrate caller-generation publications: %w", err)
	}
	for index, result := range *results {
		if result.Error != nil {
			return fmt.Errorf(
				"migrate caller-generation publications statement %d: %s",
				index, result.Error.Message,
			)
		}
	}
	check, err := surrealdb.Query[[]struct {
		Version string `json:"version"`
	}](ctx, s.db, "SELECT version FROM $marker", map[string]any{
		"marker": callerGenerationPublicationMigrationID(),
	})
	if err != nil {
		return fmt.Errorf("migrate caller-generation publications: verify: %w", err)
	}
	rows := firstDomainRows(check)
	if len(rows) == 1 && rows[0].Version == callerGenerationPublicationMigrationVersion {
		return nil
	}
	if len(rows) == 1 && rows[0].Version != "" {
		return fmt.Errorf(
			"migrate caller-generation publications: unsupported marker %q",
			rows[0].Version,
		)
	}
	return errors.New(
		"migrate caller-generation publications: completion marker missing",
	)
}

func prepareCallerGenerationPublication(
	publication CallerGenerationPublication,
	admission CallerGenerationAdmission,
	outcomes []CallerLeafOutcome,
) (CallerGenerationPublication, []callerPublicationOutcomeProjection, error) {
	preparedGeneration, err := prepareCallerGeneration(publication.Generation)
	if err != nil {
		return publication, nil, fmt.Errorf("generation: %w", err)
	}
	if publication.Pairs == nil || len(publication.Pairs) > maxCallerGenerationPairs {
		return publication, nil, errors.New("pairs must be an explicit bounded set")
	}
	if len(publication.Pairs) != len(outcomes) {
		return publication, nil, errors.New(
			"every expected pair must have one immutable outcome",
		)
	}
	pairs := make([]CallerLeafPair, len(publication.Pairs))
	canonicalPairs := make([]CallerGenerationPairPublication, len(publication.Pairs))
	projections := make([]callerPublicationOutcomeProjection, len(publication.Pairs))
	for index := range publication.Pairs {
		input := publication.Pairs[index]
		preparedOutcome, prepareErr := prepareCallerLeafOutcome(CallerLeafOutcome{
			Generation:  preparedGeneration,
			Pair:        input.Pair,
			Disposition: CallerLeafSucceeded,
			Receipt:     &input.Receipt,
		})
		if prepareErr != nil || preparedOutcome.Receipt == nil {
			return publication, nil, fmt.Errorf("pair %d: %w", index, ErrInvalidCallerGenerationPublication)
		}
		if index > 0 && !callerPairLess(pairs[index-1], preparedOutcome.Pair) {
			return publication, nil, fmt.Errorf("pair %d is unordered", index)
		}
		if outcomes[index].Generation != preparedGeneration ||
			outcomes[index].Pair != preparedOutcome.Pair ||
			outcomes[index].Disposition != CallerLeafSucceeded ||
			outcomes[index].Receipt == nil ||
			*outcomes[index].Receipt != *preparedOutcome.Receipt {
			return publication, nil, fmt.Errorf(
				"pair %d does not match its admitted immutable outcome", index,
			)
		}
		recordCount := preparedOutcome.Receipt.ResultCount
		if err := addCallerCount(
			&recordCount, preparedOutcome.Receipt.AbstentionCount,
			maxCallerPairResults+maxCallerPairAbstentions, "pair record",
		); err != nil {
			return publication, nil, err
		}
		pairs[index] = preparedOutcome.Pair
		canonicalPairs[index] = CallerGenerationPairPublication{
			Pair: preparedOutcome.Pair, Receipt: *preparedOutcome.Receipt,
			RecordCount: recordCount,
		}
		if input.RecordCount != 0 && input.RecordCount != recordCount {
			return publication, nil, fmt.Errorf("pair %d record_count is not canonical", index)
		}
		projections[index] = callerPublicationOutcomeProjection{
			Pair: preparedOutcome.Pair, Receipt: *preparedOutcome.Receipt,
			Disposition: CallerLeafSucceeded,
			Domain:      preparedOutcome.Pair.Domain, LeafPrefix: preparedOutcome.Pair.LeafPrefix,
		}
	}
	if admission.Generation != preparedGeneration ||
		admission.Disposition != CallerGenerationAdmitted ||
		ValidateCallerGenerationAdmission(admission, pairs, outcomes) != nil {
		return publication, nil, errors.New(
			"caller generation does not have an exact admitted outcome set",
		)
	}
	if !validSHA256Digest(publication.ManifestDigest) {
		return publication, nil, errors.New("manifest_digest must be canonical sha256")
	}
	if publication.ManifestPath != callerpublicationid.ManifestName(
		preparedGeneration.Digest, publication.ManifestDigest,
	) {
		return publication, nil, errors.New(
			"manifest_path does not match the generation and manifest digests",
		)
	}
	publication.Generation = preparedGeneration
	publication.Pairs = canonicalPairs
	publication.PairPayloadDigest = ""
	publication.PairSetDigest = admission.PairSetDigest
	publication.PairCount = admission.PairCount
	publication.ArtifactCount = admission.ArtifactCount
	publication.ResultCount = admission.ResultCount
	publication.AbstentionCount = admission.AbstentionCount
	publication.CanonicalBytes = admission.CanonicalBytes
	publication.StagingBytes = admission.StagingBytes
	publication.PeakOpenFiles = admission.PeakOpenFiles
	publication.PublicationRevision = 0
	publication.WriterSchema = CallerGenerationPublicationWriterSchema
	publication.PublishedAt = time.Time{}
	return publication, projections, nil
}

func validateCallerGenerationPublication(
	row callerGenerationPublicationRec,
) (CallerGenerationPublication, error) {
	publication := row.CallerGenerationPublication
	if publication.Pairs == nil {
		return CallerGenerationPublication{}, ErrInvalidCallerGenerationPublication
	}
	outcomes := make([]CallerLeafOutcome, len(publication.Pairs))
	pairs := make([]CallerLeafPair, len(publication.Pairs))
	for index := range publication.Pairs {
		receipt := publication.Pairs[index].Receipt
		outcomes[index] = CallerLeafOutcome{
			Generation:  publication.Generation,
			Pair:        publication.Pairs[index].Pair,
			Disposition: CallerLeafSucceeded,
			Receipt:     &receipt,
		}
		prepared, err := prepareCallerLeafOutcome(outcomes[index])
		if err != nil {
			return CallerGenerationPublication{}, ErrInvalidCallerGenerationPublication
		}
		outcomes[index] = prepared
		pairs[index] = prepared.Pair
	}
	admission := CallerGenerationAdmission{
		Generation:      publication.Generation,
		Disposition:     CallerGenerationAdmitted,
		PairSetDigest:   publication.PairSetDigest,
		PairCount:       publication.PairCount,
		ArtifactCount:   publication.ArtifactCount,
		ResultCount:     publication.ResultCount,
		AbstentionCount: publication.AbstentionCount,
		CanonicalBytes:  publication.CanonicalBytes,
		StagingBytes:    publication.StagingBytes,
		PeakOpenFiles:   publication.PeakOpenFiles,
		WriterSchema:    CallerLeafWriterSchema,
	}
	input := publication
	prepared, _, err := prepareCallerGenerationPublication(input, admission, outcomes)
	if err != nil || !slices.Equal(prepared.Pairs, publication.Pairs) ||
		prepared.Generation != publication.Generation ||
		prepared.PairSetDigest != publication.PairSetDigest ||
		prepared.PairCount != publication.PairCount ||
		prepared.ArtifactCount != publication.ArtifactCount ||
		prepared.ResultCount != publication.ResultCount ||
		prepared.AbstentionCount != publication.AbstentionCount ||
		prepared.CanonicalBytes != publication.CanonicalBytes ||
		prepared.StagingBytes != publication.StagingBytes ||
		prepared.PeakOpenFiles != publication.PeakOpenFiles ||
		row.Repository != publication.Generation.Repository ||
		row.GenerationDigest != publication.Generation.Digest ||
		!validSHA256Digest(publication.PairPayloadDigest) ||
		publication.PublicationRevision == 0 ||
		publication.WriterSchema != CallerGenerationPublicationWriterSchema ||
		publication.PublishedAt.IsZero() {
		return CallerGenerationPublication{}, ErrInvalidCallerGenerationPublication
	}
	publication.PublishedAt = publication.PublishedAt.UTC()
	return publication, nil
}

func validatePersistedCallerGenerationPublication(
	row callerGenerationPublicationRec,
) (CallerGenerationPublication, error) {
	publication, err := validateCallerGenerationPublication(row)
	if err != nil || !row.PairPayloadValid {
		return CallerGenerationPublication{},
			ErrInvalidCallerGenerationPublication
	}
	return publication, nil
}

// callerPublicationInstallationFenceSQL is kept as one reusable SurrealQL
// projection so the live writer and its small-limit store regression exercise
// the same arithmetic. The current repository row is excluded before the
// proposed pointer is added; an exact retry bypasses the capacity decision
// because it creates no new visibility edge and changes no retained bytes.
const callerPublicationInstallationFenceSQL = `
LET $installation = IF $same_publication THEN {
		repositories: 0, pair_rows: 0, canonical_rows: 0,
		pairs: 0, canonical_bytes: 0
	} ELSE (SELECT
			count() AS repositories,
			count(pair_count != NONE) AS pair_rows,
			count(canonical_bytes != NONE) AS canonical_rows,
			math::sum(pair_count) AS pairs,
			math::sum(canonical_bytes) AS canonical_bytes
		FROM caller_generation_publication
		WHERE id != $publication_rid
		GROUP ALL)[0]
	END;
LET $installation_repositories = $installation.repositories ?? 0;
LET $installation_pair_rows = $installation.pair_rows ?? 0;
LET $installation_canonical_rows = $installation.canonical_rows ?? 0;
LET $installation_pairs = $installation.pairs ?? 0;
LET $installation_canonical_bytes = $installation.canonical_bytes ?? 0;
LET $installation_shape_ok = $installation_repositories = $installation_pair_rows
	AND $installation_repositories = $installation_canonical_rows
	AND $installation_pairs >= 0
	AND $installation_canonical_bytes >= 0;
LET $installation_ok = $same_publication OR ($installation_shape_ok
	AND $installation_repositories < $max_publication_repositories
	AND $installation_repositories <= $max_publication_references
	AND $installation_pairs <=
		$max_publication_references - $installation_repositories
	AND $pair_count + 1 <= $max_publication_references -
		$installation_repositories - $installation_pairs
	AND $installation_canonical_bytes <= $max_publication_canonical
	AND $canonical_bytes <=
		$max_publication_canonical - $installation_canonical_bytes);
`

const publishCallerGenerationSQL = `
BEGIN;
LET $writer_ok = array::len(SELECT id FROM $migration_rid
	WHERE version = $migration_version LIMIT 1) = 1;
LET $leaf_writer_ok = array::len(SELECT id FROM $leaf_migration_rid
	WHERE version = $leaf_migration_version LIMIT 1) = 1;
LET $repo = (SELECT indexed_commit_hash, indexed_analysis_unit, deleting,
	caller_publication_revision FROM $repo_rid)[0];
LET $candidate = (SELECT head_commit, unit_digest, manifest_digest,
	policy_digest, control_revision FROM $candidate_rid)[0];
LET $resolver = (SELECT head_commit, unit_digest, candidate_manifest_digest,
	declaration_set_digest, generation_digest, manifest_digest,
	control_revision, writer_schema FROM $resolver_rid)[0];
LET $owned = (SELECT id FROM type::record($job_id)
	WHERE target = $repository AND status IN ['claimed', 'running']
		AND lease_token = $lease AND claimed_by = $claimed_by LIMIT 1)[0].id;
LET $admission = (SELECT * FROM $admission_rid)[0];
LET $outcomes = (SELECT pair, receipt, disposition, domain, leaf_prefix
	FROM caller_leaf_outcome
	WHERE repository = $repository AND generation_digest = $generation_digest
	ORDER BY domain, leaf_prefix);
LET $pair_payload_digest = (` + callerGenerationBoundPairPayloadDigestSQL + `);
LET $current = (SELECT * FROM $publication_rid)[0];
LET $current_pair_count = IF $current != NONE THEN
	array::len($current.pairs) ELSE 0 END;
LET $current_pair_payload_valid = IF $current != NONE
	AND $current_pair_count <= $max_pair_payload
	AND $current_pair_count = $current.pair_count THEN
		($current.pair_payload_digest = ('sha256:' +
			crypto::sha256(type::string($current.pairs)))) = true
	ELSE false END;
LET $authority_ok = $writer_ok AND $leaf_writer_ok AND $owned != NONE
	AND $repo != NONE AND ($repo.deleting = NONE OR $repo.deleting = false)
	AND $repo.indexed_commit_hash = $head_commit
	AND ($repo.indexed_analysis_unit.digest ?? '') = $unit_digest
	AND $candidate != NONE
	AND $candidate.head_commit = $head_commit
	AND $candidate.unit_digest = $unit_digest
	AND $candidate.manifest_digest = $candidate_manifest_digest
	AND $candidate.policy_digest = $candidate_policy_digest
	AND $candidate.control_revision = $candidate_control_revision
	AND $resolver != NONE
	AND $resolver.head_commit = $head_commit
	AND $resolver.unit_digest = $unit_digest
	AND $resolver.candidate_manifest_digest = $candidate_manifest_digest
	AND $resolver.declaration_set_digest = $declaration_set_digest
	AND $resolver.generation_digest = $resolver_generation_digest
	AND $resolver.manifest_digest = $resolver_manifest_digest
	AND $resolver.control_revision = $resolver_control_revision
	AND $resolver.writer_schema = $resolver_writer_schema
		AND $admission != NONE
		AND $admission.repository = $repository
		AND $admission.generation_digest = $generation_digest
	AND $admission.disposition = 'admitted'
	AND $admission.pair_set_digest = $pair_set_digest
	AND $admission.pair_count = $pair_count
	AND $admission.artifact_count = $artifact_count
	AND $admission.result_count = $result_count
	AND $admission.abstention_count = $abstention_count
	AND $admission.canonical_bytes = $canonical_bytes
	AND $admission.staging_bytes = $staging_bytes
	AND $admission.peak_open_files = $peak_open_files
	AND $admission.writer_schema = $leaf_writer_schema
	AND $outcomes = $expected_outcomes;
LET $same_generation = $current != NONE
	AND $current.repository = $repository
	AND $current.generation_digest = $generation_digest;
LET $same_publication = $same_generation
	AND $current.generation = $generation
	AND $current.pairs = $pairs
	AND $current.pair_payload_digest = $pair_payload_digest
	AND $current_pair_payload_valid
	AND $current.pair_set_digest = $pair_set_digest
	AND $current.pair_count = $pair_count
	AND $current.artifact_count = $artifact_count
	AND $current.result_count = $result_count
	AND $current.abstention_count = $abstention_count
	AND $current.canonical_bytes = $canonical_bytes
	AND $current.staging_bytes = $staging_bytes
	AND $current.peak_open_files = $peak_open_files
	AND $current.manifest_digest = $manifest_digest
	AND $current.manifest_path = $manifest_path
	AND $current.writer_schema = $writer_schema
	AND $current.publication_revision = ($repo.caller_publication_revision ?? 0);
` + callerPublicationInstallationFenceSQL + `
LET $acceptable = $authority_ok
	AND ($same_generation = false OR $same_publication = true
		OR $current_pair_payload_valid = false)
	AND $installation_ok;
LET $advanced = IF $acceptable AND $same_publication = false THEN
	(UPDATE $repo_rid SET caller_publication_revision =
		(caller_publication_revision ?? 0) + 1 RETURN AFTER)
	ELSE [] END;
LET $published = IF $acceptable = false THEN []
	ELSE IF $same_publication THEN [$current]
	ELSE IF array::len($advanced) = 1 THEN
		(UPSERT $publication_rid SET
			repository = $repository,
			generation = $generation,
			generation_digest = $generation_digest,
			pairs = $pairs,
			pair_payload_digest = $pair_payload_digest,
			pair_set_digest = $pair_set_digest,
			pair_count = $pair_count,
			artifact_count = $artifact_count,
			result_count = $result_count,
			abstention_count = $abstention_count,
			canonical_bytes = $canonical_bytes,
			staging_bytes = $staging_bytes,
			peak_open_files = $peak_open_files,
			manifest_digest = $manifest_digest,
			manifest_path = $manifest_path,
			publication_revision = $advanced[0].caller_publication_revision,
			writer_schema = $writer_schema,
			published_at = time::now()
			RETURN AFTER)
	ELSE [] END;
LET $verified = (SELECT *, (` + callerGenerationPairPayloadDigestSQL + `) =
	pair_payload_digest AS pair_payload_valid
	FROM caller_generation_publication
	WHERE id = $publication_rid LIMIT 1)[0];
RETURN IF array::len($published) = 1 AND $verified != NONE THEN
	[$verified] ELSE [] END;
COMMIT;`

func (s *Surreal) PublishCallerGeneration(
	ctx context.Context,
	job Job,
	publication CallerGenerationPublication,
) error {
	preparedGeneration, err := prepareCallerGeneration(publication.Generation)
	if err != nil {
		return fmt.Errorf("publish caller generation: %w", err)
	}
	if err := validateCallerJob(job, preparedGeneration.Repository); err != nil {
		return fmt.Errorf("publish caller generation: %w", err)
	}
	admission, err := s.GetCallerGenerationAdmission(ctx, preparedGeneration)
	if err != nil {
		return fmt.Errorf("publish caller generation: admission: %w", err)
	}
	outcomes, err := s.ListCallerLeafOutcomes(ctx, preparedGeneration)
	if err != nil {
		return fmt.Errorf("publish caller generation: outcomes: %w", err)
	}
	prepared, projections, err := prepareCallerGenerationPublication(
		publication, *admission, outcomes,
	)
	if err != nil {
		return fmt.Errorf("publish caller generation: %w: %v", ErrInvalidCallerGenerationPublication, err)
	}
	vars := map[string]any{
		"migration_rid":          callerGenerationPublicationMigrationID(),
		"migration_version":      callerGenerationPublicationMigrationVersion,
		"leaf_migration_rid":     callerLeafMigrationID(),
		"leaf_migration_version": callerLeafWriterMigrationVersion,
		"repo_rid":               repoID(prepared.Generation.Repository),
		"candidate_rid":          candidateManifestPublicationID(prepared.Generation.Repository),
		"resolver_rid":           resolverCatalogPublicationID(prepared.Generation.Repository),
		"admission_rid":          callerGenerationAdmissionID(prepared.Generation),
		"publication_rid":        callerGenerationPublicationID(prepared.Generation.Repository),
		"job_id":                 job.ID, "lease": job.LeaseToken, "claimed_by": job.ClaimedBy,
		"repository":                   prepared.Generation.Repository,
		"head_commit":                  prepared.Generation.HeadCommit,
		"unit_digest":                  prepared.Generation.UnitDigest,
		"declaration_set_digest":       prepared.Generation.DeclarationSetDigest,
		"candidate_manifest_digest":    prepared.Generation.CandidateManifestDigest,
		"candidate_policy_digest":      prepared.Generation.CandidatePolicyDigest,
		"candidate_control_revision":   prepared.Generation.CandidateControlRevision,
		"resolver_generation_digest":   prepared.Generation.ResolverGenerationDigest,
		"resolver_manifest_digest":     prepared.Generation.ResolverManifestDigest,
		"resolver_control_revision":    prepared.Generation.ResolverControlRevision,
		"resolver_writer_schema":       prepared.Generation.ResolverWriterSchema,
		"generation":                   prepared.Generation,
		"generation_digest":            prepared.Generation.Digest,
		"pairs":                        prepared.Pairs,
		"expected_outcomes":            projections,
		"pair_set_digest":              prepared.PairSetDigest,
		"pair_count":                   prepared.PairCount,
		"artifact_count":               prepared.ArtifactCount,
		"result_count":                 prepared.ResultCount,
		"abstention_count":             prepared.AbstentionCount,
		"canonical_bytes":              prepared.CanonicalBytes,
		"staging_bytes":                prepared.StagingBytes,
		"peak_open_files":              prepared.PeakOpenFiles,
		"manifest_digest":              prepared.ManifestDigest,
		"manifest_path":                prepared.ManifestPath,
		"leaf_writer_schema":           CallerLeafWriterSchema,
		"writer_schema":                CallerGenerationPublicationWriterSchema,
		"max_publication_repositories": MaxCallerPublicationRepositories,
		"max_publication_references":   maxCallerPublicationReferences,
		"max_publication_canonical":    maxCallerPublicationCanonical,
		"max_pair_payload":             maxCallerGenerationPairs,
	}
	for attempt := 0; ; attempt++ {
		results, queryErr := surrealdb.Query[[]callerGenerationPublicationRec](
			ctx, s.db, publishCallerGenerationSQL, vars,
		)
		if queryErr != nil {
			if isRetryableEnqueue(queryErr) && ctx.Err() == nil && attempt+1 < maxQueueRetries {
				continue
			}
			return fmt.Errorf("publish caller generation: %w", queryErr)
		}
		rows := firstDomainRows(results)
		if len(rows) != 1 {
			return fmt.Errorf("publish caller generation: stale or conflicting authority: %w", ErrConflict)
		}
		if !validCallerGenerationPublicationRecordID(rows[0]) {
			return fmt.Errorf("publish caller generation: persisted pointer: %w", ErrInvalidCallerGenerationPublication)
		}
		if _, validateErr := validatePersistedCallerGenerationPublication(rows[0]); validateErr != nil {
			return fmt.Errorf("publish caller generation: persisted pointer: %w", validateErr)
		}
		return nil
	}
}

func (s *Surreal) GetCallerGenerationPublication(
	ctx context.Context,
	repository string,
) (*CallerGenerationPublication, error) {
	if err := reponame.Validate(repository); err != nil {
		return nil, fmt.Errorf("get caller generation: %w", err)
	}
	results, err := surrealdb.Query[[]callerGenerationPublicationRec](
		ctx, s.db, "SELECT *, ("+callerGenerationPairPayloadDigestSQL+") = "+
			"pair_payload_digest AS pair_payload_valid FROM $rid", map[string]any{
			"rid": callerGenerationPublicationID(repository),
		},
	)
	if err != nil {
		return nil, fmt.Errorf("get caller generation: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) == 0 {
		return nil, fmt.Errorf("caller generation for %q: %w", repository, ErrNotFound)
	}
	publication, err := validatePersistedCallerGenerationPublication(rows[0])
	if err != nil || !validCallerGenerationPublicationRecordID(rows[0]) ||
		publication.Generation.Repository != repository {
		return nil, fmt.Errorf("get caller generation: %w", ErrInvalidCallerGenerationPublication)
	}
	return &publication, nil
}

// GetCallerGenerationPublicationSummary reads an untrusted scalar projection
// without materializing or hashing the bounded-but-large pair receipt array.
// Lifecycle readers must pass it to CallerGenerationPublicationSummaryCurrent
// before treating it as authority. Product readers that need pair rows use the
// full-pointer method, which validates the server-side payload commitment.
func (s *Surreal) GetCallerGenerationPublicationSummary(
	ctx context.Context,
	repository string,
) (*CallerGenerationPublicationSummary, error) {
	if err := reponame.Validate(repository); err != nil {
		return nil, fmt.Errorf("get caller generation summary: %w", err)
	}
	results, err := surrealdb.Query[[]callerGenerationPublicationSummaryRec](
		ctx, s.db,
		"SELECT "+callerGenerationPublicationSummaryProjection+" FROM $rid",
		map[string]any{"rid": callerGenerationPublicationID(repository)},
	)
	if err != nil {
		return nil, fmt.Errorf("get caller generation summary: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) == 0 {
		return nil, fmt.Errorf(
			"caller generation summary for %q: %w", repository, ErrNotFound,
		)
	}
	summary, err := validatePersistedCallerGenerationPublicationSummary(rows[0])
	if err != nil || summary.Generation.Repository != repository {
		return nil, fmt.Errorf(
			"get caller generation summary: %w",
			ErrInvalidCallerGenerationPublication,
		)
	}
	return &summary, nil
}

// ListCallerGenerationPublicationSummaries is the matching bounded startup
// inventory. Its rows are untrusted until each one passes the exact current
// check; listing never hashes or transfers pair payloads.
func (s *Surreal) ListCallerGenerationPublicationSummaries(
	ctx context.Context,
) ([]CallerGenerationPublicationSummary, error) {
	results, err := surrealdb.Query[[]callerGenerationPublicationSummaryRec](
		ctx, s.db, "SELECT "+callerGenerationPublicationSummaryProjection+`
		FROM caller_generation_publication ORDER BY repository LIMIT $limit`,
		map[string]any{"limit": MaxCallerPublicationRepositories + 1},
	)
	if err != nil {
		return nil, fmt.Errorf("list caller generation summaries: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) > MaxCallerPublicationRepositories {
		return nil, fmt.Errorf(
			"list caller generation summaries exceeds %d: %w",
			MaxCallerPublicationRepositories,
			ErrInvalidCallerGenerationPublication,
		)
	}
	publications := make([]CallerGenerationPublicationSummary, len(rows))
	for index := range rows {
		publications[index], err =
			validatePersistedCallerGenerationPublicationSummary(rows[index])
		if err != nil || index > 0 &&
			publications[index-1].Generation.Repository >= publications[index].Generation.Repository {
			return nil, fmt.Errorf(
				"list caller generation summaries row %d: %w",
				index, ErrInvalidCallerGenerationPublication,
			)
		}
	}
	return publications, nil
}

// ListCallerPublicationRepositoriesPage keyset-pages the startup marker-repair
// scope without imposing a total indexed-repository ceiling. Each response is
// a fixed bounded projection; the caller advances with the last returned name.
func (s *Surreal) ListCallerPublicationRepositoriesPage(
	ctx context.Context,
	after string,
	limit int,
) ([]string, error) {
	if after != "" {
		if err := reponame.Validate(after); err != nil {
			return nil, fmt.Errorf(
				"list caller publication repository page: after: %w", err,
			)
		}
	}
	if limit < 1 || limit > MaxCallerPublicationRepositoryPage {
		return nil, fmt.Errorf(
			"list caller publication repository page: limit must be from 1 through %d",
			MaxCallerPublicationRepositoryPage,
		)
	}
	results, err := surrealdb.Query[[]callerPublicationRepositoryRec](
		ctx, s.db, `SELECT id, name FROM repo
			WHERE name > $after
				AND (indexed_commit_hash ?? '') != ''
				AND (deleting = NONE OR deleting = false)
			ORDER BY name LIMIT $limit`, map[string]any{
			"after": after, "limit": limit,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("list caller publication repository page: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) > limit {
		return nil, fmt.Errorf(
			"list caller publication repository page exceeded %d: %w",
			limit, ErrInvalidCallerGenerationPublication,
		)
	}
	repositories := make([]string, len(rows))
	previous := after
	for index, row := range rows {
		identifier := ""
		if row.RecID != nil && row.RecID.Table == "repo" {
			identifier, _ = row.RecID.ID.(string)
		}
		if reponame.Validate(row.Name) != nil || identifier != row.Name ||
			row.Name <= previous {
			return nil, fmt.Errorf(
				"list caller publication repository page row %d: %w",
				index, ErrInvalidCallerGenerationPublication,
			)
		}
		repositories[index] = row.Name
		previous = row.Name
	}
	return repositories, nil
}

// CallerPublicationRepositoryEligible is the per-directory startup repair
// fence. Reconciliation discovers a bounded package-owned physical scope and
// probes only those exact repositories, so installations with many unrelated
// indexed repositories do not create an unbounded caller startup query.
func (s *Surreal) CallerPublicationRepositoryEligible(
	ctx context.Context,
	repository string,
) (bool, error) {
	if err := reponame.Validate(repository); err != nil {
		return false, fmt.Errorf("check caller publication repository: %w", err)
	}
	results, err := surrealdb.Query[[]callerPublicationRepositoryEligibleResult](
		ctx, s.db, `
		LET $repository = (SELECT name, indexed_commit_hash, deleting
			FROM $rid LIMIT 1)[0];
		RETURN [{
			eligible: $repository != NONE
				AND $repository.name = $name
				AND ($repository.indexed_commit_hash ?? '') != ''
				AND ($repository.deleting = NONE OR $repository.deleting = false)
		}];`, map[string]any{
			"rid": repoID(repository), "name": repository,
		},
	)
	if err != nil {
		return false, fmt.Errorf("check caller publication repository: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 {
		return false, errors.New(
			"check caller publication repository: empty result",
		)
	}
	return rows[0].Eligible, nil
}

const callerGenerationPublicationSummaryCurrentSQL = `
BEGIN;
LET $writer_ok = array::len(SELECT id FROM $migration_rid
	WHERE version = $migration_version LIMIT 1) = 1;
LET $repo = (SELECT indexed_commit_hash, indexed_analysis_unit, deleting,
	caller_publication_revision FROM $repo_rid)[0];
LET $candidate = (SELECT head_commit, unit_digest, manifest_digest,
	policy_digest, control_revision FROM $candidate_rid)[0];
LET $resolver = (SELECT head_commit, unit_digest, candidate_manifest_digest,
	declaration_set_digest, generation_digest, manifest_digest,
	control_revision, writer_schema FROM $resolver_rid)[0];
LET $current = (SELECT ` + callerGenerationPublicationSummaryProjection + `
	FROM $publication_rid)[0];
LET $scalar_current = $writer_ok AND $repo != NONE
		AND ($repo.deleting = NONE OR $repo.deleting = false)
		AND $repo.indexed_commit_hash = $head_commit
		AND ($repo.indexed_analysis_unit.digest ?? '') = $unit_digest
		AND $repo.caller_publication_revision = $publication_revision
		AND $candidate != NONE
		AND $candidate.head_commit = $head_commit
		AND $candidate.unit_digest = $unit_digest
		AND $candidate.manifest_digest = $candidate_manifest_digest
		AND $candidate.policy_digest = $candidate_policy_digest
		AND $candidate.control_revision = $candidate_control_revision
		AND $resolver != NONE
		AND $resolver.head_commit = $head_commit
		AND $resolver.unit_digest = $unit_digest
		AND $resolver.candidate_manifest_digest = $candidate_manifest_digest
		AND $resolver.declaration_set_digest = $declaration_set_digest
		AND $resolver.generation_digest = $resolver_generation_digest
		AND $resolver.manifest_digest = $resolver_manifest_digest
		AND $resolver.control_revision = $resolver_control_revision
		AND $resolver.writer_schema = $resolver_writer_schema
		AND $current != NONE
		AND $current.repository = $repository
		AND $current.generation = $generation
		AND $current.generation_digest = $generation_digest
		AND $current.pair_set_digest = $pair_set_digest
		AND $current.pair_count = $pair_count
		AND $current.artifact_count = $artifact_count
		AND $current.result_count = $result_count
		AND $current.abstention_count = $abstention_count
		AND $current.canonical_bytes = $canonical_bytes
		AND $current.staging_bytes = $staging_bytes
		AND $current.peak_open_files = $peak_open_files
		AND $current.manifest_digest = $manifest_digest
		AND $current.manifest_path = $manifest_path
		AND $current.publication_revision = $publication_revision
		AND $current.writer_schema = $writer_schema;
LET $pair_payload_identity = $scalar_current
	AND $current.pair_payload_count = $pair_count
	AND $current.pair_payload_digest = $pair_payload_digest;
LET $pair_payload_valid = IF $verify_pair_payload
	AND $pair_payload_identity THEN
		((SELECT VALUE (` + callerGenerationPairPayloadDigestSQL + `) =
			pair_payload_digest
			FROM caller_generation_publication
			WHERE id = $publication_rid LIMIT 1)[0] ?? false)
	ELSE $pair_payload_identity END;
RETURN [{
	current: $scalar_current AND $pair_payload_valid,
	pair_payload_invalid: $scalar_current AND $pair_payload_valid = false
}];
COMMIT;`

// CallerGenerationPublicationSummaryCurrent rechecks every mutable authority
// and the complete pointer's digest-bound summary without transferring its
// pair receipt array to Go. Surreal hashes that schema-bounded array once and
// compares it with the writer-owned pair payload commitment; the manifest and
// pair-set digests then bind the cold-validated filesystem publication used by
// a previously admitted cache entry.
func (s *Surreal) CallerGenerationPublicationSummaryCurrent(
	ctx context.Context,
	summary CallerGenerationPublicationSummary,
) (bool, error) {
	return s.callerGenerationPublicationSummaryCurrent(ctx, summary, true)
}

// CallerGenerationPublicationSummaryAuthorityCurrent is the provisional warm
// path fence. It checks every mutable authority/scalar, actual pair-array
// length, and the carried writer-owned payload digest, but deliberately does
// not hash pairs. A reader must still call the exact SummaryCurrent after its
// filesystem admission and before exposing visibility.
func (s *Surreal) CallerGenerationPublicationSummaryAuthorityCurrent(
	ctx context.Context,
	summary CallerGenerationPublicationSummary,
) (bool, error) {
	return s.callerGenerationPublicationSummaryCurrent(ctx, summary, false)
}

func (s *Surreal) callerGenerationPublicationSummaryCurrent(
	ctx context.Context,
	summary CallerGenerationPublicationSummary,
	verifyPairPayload bool,
) (bool, error) {
	recordID := callerGenerationPublicationID(summary.Generation.Repository)
	validated, err := validateCallerGenerationPublicationSummary(
		callerGenerationPublicationSummaryRec{
			CallerGenerationPublicationSummary: summary,
			Repository:                         summary.Generation.Repository,
			GenerationDigest:                   summary.Generation.Digest,
			RecID:                              &recordID,
		},
	)
	if err != nil {
		return false, fmt.Errorf(
			"check caller generation summary authority: %w", err,
		)
	}
	results, err := surrealdb.Query[[]callerGenerationCurrentResult](
		ctx, s.db, callerGenerationPublicationSummaryCurrentSQL,
		map[string]any{
			"migration_rid":              callerGenerationPublicationMigrationID(),
			"migration_version":          callerGenerationPublicationMigrationVersion,
			"repo_rid":                   repoID(validated.Generation.Repository),
			"candidate_rid":              candidateManifestPublicationID(validated.Generation.Repository),
			"resolver_rid":               resolverCatalogPublicationID(validated.Generation.Repository),
			"publication_rid":            callerGenerationPublicationID(validated.Generation.Repository),
			"repository":                 validated.Generation.Repository,
			"head_commit":                validated.Generation.HeadCommit,
			"unit_digest":                validated.Generation.UnitDigest,
			"declaration_set_digest":     validated.Generation.DeclarationSetDigest,
			"candidate_manifest_digest":  validated.Generation.CandidateManifestDigest,
			"candidate_policy_digest":    validated.Generation.CandidatePolicyDigest,
			"candidate_control_revision": validated.Generation.CandidateControlRevision,
			"resolver_generation_digest": validated.Generation.ResolverGenerationDigest,
			"resolver_manifest_digest":   validated.Generation.ResolverManifestDigest,
			"resolver_control_revision":  validated.Generation.ResolverControlRevision,
			"resolver_writer_schema":     validated.Generation.ResolverWriterSchema,
			"generation":                 validated.Generation,
			"generation_digest":          validated.Generation.Digest,
			"pair_payload_digest":        validated.PairPayloadDigest,
			"pair_set_digest":            validated.PairSetDigest,
			"pair_count":                 validated.PairCount,
			"artifact_count":             validated.ArtifactCount,
			"result_count":               validated.ResultCount,
			"abstention_count":           validated.AbstentionCount,
			"canonical_bytes":            validated.CanonicalBytes,
			"staging_bytes":              validated.StagingBytes,
			"peak_open_files":            validated.PeakOpenFiles,
			"manifest_digest":            validated.ManifestDigest,
			"manifest_path":              validated.ManifestPath,
			"publication_revision":       validated.PublicationRevision,
			"writer_schema":              CallerGenerationPublicationWriterSchema,
			"verify_pair_payload":        verifyPairPayload,
		},
	)
	if err != nil {
		return false, fmt.Errorf(
			"check caller generation summary authority: %w", err,
		)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 {
		return false, errors.New(
			"check caller generation summary authority: empty result",
		)
	}
	if rows[0].PairPayloadInvalid {
		return false, fmt.Errorf(
			"check caller generation summary authority: pair payload: %w",
			ErrInvalidCallerGenerationPublication,
		)
	}
	return rows[0].Current, nil
}

const callerGenerationPublicationCurrentSQL = `
LET $writer_ok = array::len(SELECT id FROM $migration_rid
	WHERE version = $migration_version LIMIT 1) = 1;
LET $repo = (SELECT indexed_commit_hash, indexed_analysis_unit, deleting,
	caller_publication_revision FROM $repo_rid)[0];
LET $candidate = (SELECT head_commit, unit_digest, manifest_digest,
	policy_digest, control_revision FROM $candidate_rid)[0];
LET $resolver = (SELECT head_commit, unit_digest, candidate_manifest_digest,
	declaration_set_digest, generation_digest, manifest_digest,
	control_revision, writer_schema FROM $resolver_rid)[0];
LET $expected_pair_payload_digest = (` +
	callerGenerationBoundPairPayloadDigestSQL + `);
LET $current = (SELECT *, (` + callerGenerationPairPayloadDigestSQL + `) =
	pair_payload_digest AS pair_payload_valid FROM $publication_rid)[0];
RETURN [{
	current: $writer_ok AND $repo != NONE
		AND ($repo.deleting = NONE OR $repo.deleting = false)
		AND $repo.indexed_commit_hash = $head_commit
		AND ($repo.indexed_analysis_unit.digest ?? '') = $unit_digest
		AND $repo.caller_publication_revision = $publication_revision
		AND $candidate != NONE
		AND $candidate.head_commit = $head_commit
		AND $candidate.unit_digest = $unit_digest
		AND $candidate.manifest_digest = $candidate_manifest_digest
		AND $candidate.policy_digest = $candidate_policy_digest
		AND $candidate.control_revision = $candidate_control_revision
		AND $resolver != NONE
		AND $resolver.head_commit = $head_commit
		AND $resolver.unit_digest = $unit_digest
		AND $resolver.candidate_manifest_digest = $candidate_manifest_digest
		AND $resolver.declaration_set_digest = $declaration_set_digest
		AND $resolver.generation_digest = $resolver_generation_digest
		AND $resolver.manifest_digest = $resolver_manifest_digest
		AND $resolver.control_revision = $resolver_control_revision
		AND $resolver.writer_schema = $resolver_writer_schema
		AND $current != NONE
		AND $current.repository = $repository
		AND $current.generation = $generation
		AND $current.generation_digest = $generation_digest
		AND $current.pairs = $pairs
		AND $expected_pair_payload_digest = $pair_payload_digest
		AND $current.pair_payload_digest = $expected_pair_payload_digest
		AND $current.pair_payload_valid = true
		AND $current.pair_set_digest = $pair_set_digest
		AND $current.pair_count = $pair_count
		AND $current.artifact_count = $artifact_count
		AND $current.result_count = $result_count
		AND $current.abstention_count = $abstention_count
		AND $current.canonical_bytes = $canonical_bytes
		AND $current.staging_bytes = $staging_bytes
		AND $current.peak_open_files = $peak_open_files
		AND $current.manifest_digest = $manifest_digest
		AND $current.manifest_path = $manifest_path
		AND $current.publication_revision = $publication_revision
		AND $current.writer_schema = $writer_schema
}];`

func (s *Surreal) CallerGenerationPublicationCurrent(
	ctx context.Context,
	publication CallerGenerationPublication,
) (bool, error) {
	validated, err := validateCallerGenerationPublication(
		callerGenerationPublicationRec{
			CallerGenerationPublication: publication,
			Repository:                  publication.Generation.Repository,
			GenerationDigest:            publication.Generation.Digest,
		},
	)
	if err != nil {
		return false, fmt.Errorf("check caller generation authority: %w", err)
	}
	results, err := surrealdb.Query[[]callerGenerationCurrentResult](ctx, s.db,
		callerGenerationPublicationCurrentSQL, map[string]any{
			"migration_rid":              callerGenerationPublicationMigrationID(),
			"migration_version":          callerGenerationPublicationMigrationVersion,
			"repo_rid":                   repoID(validated.Generation.Repository),
			"candidate_rid":              candidateManifestPublicationID(validated.Generation.Repository),
			"resolver_rid":               resolverCatalogPublicationID(validated.Generation.Repository),
			"publication_rid":            callerGenerationPublicationID(validated.Generation.Repository),
			"repository":                 validated.Generation.Repository,
			"head_commit":                validated.Generation.HeadCommit,
			"unit_digest":                validated.Generation.UnitDigest,
			"declaration_set_digest":     validated.Generation.DeclarationSetDigest,
			"candidate_manifest_digest":  validated.Generation.CandidateManifestDigest,
			"candidate_policy_digest":    validated.Generation.CandidatePolicyDigest,
			"candidate_control_revision": validated.Generation.CandidateControlRevision,
			"resolver_generation_digest": validated.Generation.ResolverGenerationDigest,
			"resolver_manifest_digest":   validated.Generation.ResolverManifestDigest,
			"resolver_control_revision":  validated.Generation.ResolverControlRevision,
			"resolver_writer_schema":     validated.Generation.ResolverWriterSchema,
			"generation":                 validated.Generation,
			"generation_digest":          validated.Generation.Digest,
			"pairs":                      validated.Pairs,
			"pair_payload_digest":        validated.PairPayloadDigest,
			"pair_set_digest":            validated.PairSetDigest,
			"pair_count":                 validated.PairCount,
			"artifact_count":             validated.ArtifactCount,
			"result_count":               validated.ResultCount,
			"abstention_count":           validated.AbstentionCount,
			"canonical_bytes":            validated.CanonicalBytes,
			"staging_bytes":              validated.StagingBytes,
			"peak_open_files":            validated.PeakOpenFiles,
			"manifest_digest":            validated.ManifestDigest,
			"manifest_path":              validated.ManifestPath,
			"publication_revision":       validated.PublicationRevision,
			"writer_schema":              CallerGenerationPublicationWriterSchema,
		})
	if err != nil {
		return false, fmt.Errorf("check caller generation authority: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 {
		return false, errors.New("check caller generation authority: empty result")
	}
	return rows[0].Current, nil
}

func (s *Surreal) ClearCallerGenerationPublication(
	ctx context.Context,
	repository string,
) error {
	if err := reponame.Validate(repository); err != nil {
		return fmt.Errorf("clear caller generation: %w", err)
	}
	for attempt := 0; ; attempt++ {
		results, err := surrealdb.Query[any](ctx, s.db, `
BEGIN;
LET $writer_ok = array::len(SELECT id FROM $migration_rid
	WHERE version = $migration_version LIMIT 1) = 1;
IF $writer_ok = false {
	THROW 'phebs-permanent: caller-generation publication writer is not active'
};
LET $current = DELETE $rid RETURN BEFORE;
IF array::len($current) = 1 {
	UPDATE $repo_rid SET caller_publication_revision =
		(caller_publication_revision ?? 0) + 1 RETURN NONE
};
COMMIT;`, map[string]any{
			"migration_rid":     callerGenerationPublicationMigrationID(),
			"migration_version": callerGenerationPublicationMigrationVersion,
			"rid":               callerGenerationPublicationID(repository),
			"repo_rid":          repoID(repository),
		})
		if err != nil {
			if isRetryable(err) && ctx.Err() == nil && attempt+1 < maxQueueRetries {
				continue
			}
			return fmt.Errorf("clear caller generation: %w", err)
		}
		for index, result := range *results {
			if result.Error != nil {
				return fmt.Errorf("clear caller generation statement %d: %s", index, result.Error.Message)
			}
		}
		return nil
	}
}

// ClearAllCallerPublicationStateForRestore raw-clears every imported caller
// pointer, leaf outcome, and admission without decoding them. Every repository
// that had a pointer advances exactly once, retaining an unavailable
// transition even when the imported row itself was malformed.
func (s *Surreal) ClearAllCallerPublicationStateForRestore(
	ctx context.Context,
) error {
	for attempt := 0; ; attempt++ {
		results, err := surrealdb.Query[any](ctx, s.db, `
BEGIN;
LET $writer_ok = array::len(SELECT id FROM $migration_rid
	WHERE version = $migration_version LIMIT 1) = 1;
LET $leaf_writer_ok = array::len(SELECT id FROM $leaf_migration_rid
	WHERE version = $leaf_migration_version LIMIT 1) = 1;
IF $writer_ok = false OR $leaf_writer_ok = false {
	THROW 'phebs-permanent: caller publication writer generation is not active'
};
LET $repositories = SELECT VALUE record::id(id)
	FROM caller_generation_publication;
UPDATE repo SET caller_publication_revision =
	(caller_publication_revision ?? 0) + 1
	WHERE name IN $repositories RETURN NONE;
DELETE caller_generation_publication RETURN NONE;
DELETE caller_leaf_outcome RETURN NONE;
DELETE caller_generation_admission RETURN NONE;
COMMIT;`, map[string]any{
			"migration_rid":          callerGenerationPublicationMigrationID(),
			"migration_version":      callerGenerationPublicationMigrationVersion,
			"leaf_migration_rid":     callerLeafMigrationID(),
			"leaf_migration_version": callerLeafWriterMigrationVersion,
		})
		if err != nil {
			if isRetryable(err) && ctx.Err() == nil && attempt+1 < maxQueueRetries {
				continue
			}
			return fmt.Errorf("clear caller publication state for restore: %w", err)
		}
		for index, result := range *results {
			if result.Error != nil {
				return fmt.Errorf(
					"clear caller publication state for restore statement %d: %s",
					index, result.Error.Message,
				)
			}
		}
		return nil
	}
}
