package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"

	"github.com/bmeddeb/phebs/internal/reponame"
	"github.com/bmeddeb/phebs/internal/resolvercatalogid"
)

const (
	resolverCatalogWriterSchema           = resolvercatalogid.WriterSchema
	resolverCatalogPriorWriterSchema      = resolvercatalogid.WriterSchemaV1
	resolverCatalogSourceLanePolicy       = resolvercatalogid.SourceLanePolicy
	resolverCatalogPriorMigration         = "t30.6f-resolver-catalog-writer-v1"
	resolverCatalogWriterMigrationVersion = "t40r1-resolver-catalog-writer-v2"
)

const maxResolverCatalogDeclarations = 16

type ResolverCatalogDeclarationPublication struct {
	Domain           string `json:"domain"`
	RunID            string `json:"run_id"`
	GenerationDigest string `json:"generation_digest"`
	AuthoritySchema  string `json:"authority_schema,omitempty"`
	PlanDigest       string `json:"plan_digest,omitempty"`
	RootDigest       string `json:"root_digest,omitempty"`
}

type ResolverCatalogPack struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type ResolverCatalogPublication struct {
	Repository              string                                  `json:"repository"`
	HeadCommit              string                                  `json:"head_commit"`
	UnitDigest              string                                  `json:"unit_digest"`
	Declarations            []ResolverCatalogDeclarationPublication `json:"declarations"`
	DeclarationSetDigest    string                                  `json:"declaration_set_digest"`
	CandidateManifestDigest string                                  `json:"candidate_manifest_digest"`
	SourceLanePolicy        string                                  `json:"source_lane_policy"`
	ResolverPacks           []ResolverCatalogPack                   `json:"resolver_packs"`
	ResolverPackSetDigest   string                                  `json:"resolver_pack_set_digest"`
	CatalogPolicyDigest     string                                  `json:"catalog_policy_digest"`
	GenerationDigest        string                                  `json:"generation_digest"`
	ManifestDigest          string                                  `json:"manifest_digest"`
	AuthorityDigest         string                                  `json:"authority_digest"`
	ManifestPath            string                                  `json:"manifest_path"`
	ControlRevision         uint64                                  `json:"control_revision"`
	WriterSchema            string                                  `json:"writer_schema"`
	PublishedAt             time.Time                               `json:"published_at"`
}

type ResolverCatalogPublicationStore interface {
	GetResolverCatalogPublication(context.Context, string) (*ResolverCatalogPublication, error)
	PublishResolverCatalog(context.Context, ResolverCatalogPublication) error
	ClearResolverCatalogPublication(context.Context, string) error
	ListResolverCatalogPublications(context.Context) ([]ResolverCatalogPublication, error)
	ResolverCatalogPublicationCurrent(context.Context, ResolverCatalogPublication) (bool, error)
}

var _ ResolverCatalogPublicationStore = (*Surreal)(nil)

type resolverCatalogCurrentResult struct {
	Current bool `json:"current"`
}

type resolverCatalogPublicationRec struct {
	ResolverCatalogPublication
	RecID *models.RecordID `json:"id"`
}

func resolverCatalogPublicationID(repository string) models.RecordID {
	return models.NewRecordID("resolver_catalog_publication", repository)
}

func resolverCatalogMigrationID() models.RecordID {
	return models.NewRecordID("store_migration", "resolver_catalog_writer")
}

func (s *Surreal) migrateResolverCatalogWriter(ctx context.Context) error {
	marker := resolverCatalogMigrationID()
	results, err := surrealdb.Query[any](ctx, s.db, `
BEGIN;
LET $current = (SELECT version FROM $marker LIMIT 1)[0].version;
IF $current != NONE AND $current != $wanted AND $current != $prior {
	THROW 'phebs-permanent: unsupported resolver catalog writer generation'
};
LET $prior_rows = SELECT id FROM resolver_catalog_publication
	WHERE writer_schema = $prior_writer_schema LIMIT 1;
LET $wanted_rows = SELECT id FROM resolver_catalog_publication
	WHERE writer_schema = $writer_schema LIMIT 1;
LET $unsupported = SELECT id FROM resolver_catalog_publication
	WHERE writer_schema NOT IN [$prior_writer_schema, $writer_schema] LIMIT 1;
IF array::len($unsupported) != 0 {
	THROW 'phebs-permanent: unsupported resolver catalog publication generation'
};
IF ($current = $prior AND array::len($wanted_rows) != 0)
	OR ($current = $wanted AND array::len($prior_rows) != 0)
	OR ($current = NONE AND array::len($prior_rows) != 0
		AND array::len($wanted_rows) != 0) {
	THROW 'phebs-permanent: mixed resolver catalog publication generations'
};
LET $retired_repositories = SELECT VALUE repository
	FROM resolver_catalog_publication
	WHERE ($current = $prior OR $current = NONE)
		AND writer_schema = $prior_writer_schema;
DELETE caller_generation_publication
	WHERE repository IN $retired_repositories RETURN NONE;
UPDATE repo SET caller_publication_revision =
	(caller_publication_revision ?? 0) + 1
	WHERE name IN $retired_repositories RETURN NONE;
DELETE resolver_catalog_publication
	WHERE ($current = NONE OR $current = $prior)
		AND writer_schema = $prior_writer_schema
	RETURN NONE;
UPSERT $marker SET
	version = IF $current = $wanted THEN $current ELSE $wanted END,
	completed_at = IF $current = $wanted THEN completed_at ELSE time::now() END
	RETURN NONE;
COMMIT;`, map[string]any{
		"marker": marker, "prior": resolverCatalogPriorMigration,
		"wanted":              resolverCatalogWriterMigrationVersion,
		"writer_schema":       resolverCatalogWriterSchema,
		"prior_writer_schema": resolverCatalogPriorWriterSchema,
	})
	if err != nil {
		return fmt.Errorf("migrate resolver catalog writer: %w", err)
	}
	for index, result := range *results {
		if result.Error != nil {
			return fmt.Errorf(
				"migrate resolver catalog writer statement %d: %s",
				index, result.Error.Message,
			)
		}
	}
	return nil
}

func validateResolverCatalogPublication(
	publication ResolverCatalogPublication,
	requireStoreFields bool,
) error {
	if err := reponame.Validate(publication.Repository); err != nil {
		return fmt.Errorf("repository: %w", err)
	}
	if !validGitObjectID(publication.HeadCommit) {
		return errors.New("head_commit must be a canonical object ID")
	}
	if publication.UnitDigest != "" && !validSHA256Digest(publication.UnitDigest) {
		return errors.New("unit_digest must be empty or canonical sha256")
	}
	if publication.Declarations == nil ||
		len(publication.Declarations) > maxResolverCatalogDeclarations {
		return errors.New("declarations must be an explicit bounded set")
	}
	for index, declaration := range publication.Declarations {
		if !validResolverCatalogToken(declaration.Domain, 128) ||
			!validResolverCatalogToken(declaration.RunID, 256) ||
			index > 0 &&
				publication.Declarations[index-1].Domain >= declaration.Domain {
			return fmt.Errorf("declaration %d is invalid or unordered", index)
		}
		partitioned := declaration.AuthoritySchema != "" ||
			declaration.PlanDigest != "" || declaration.RootDigest != ""
		if partitioned {
			if declaration.AuthoritySchema != PartitionedExtractionDomainSchema ||
				!validSHA256Digest(declaration.PlanDigest) ||
				!validSHA256Digest(declaration.RootDigest) ||
				declaration.GenerationDigest != declaration.RootDigest {
				return fmt.Errorf("declaration %d has invalid partitioned authority", index)
			}
		} else if !validResolverCatalogGenerationDigest(
			declaration.GenerationDigest,
		) {
			return fmt.Errorf("declaration %d has invalid legacy authority", index)
		}
	}
	declarationDigest, err := resolverCatalogDeclarationSetDigest(
		publication.Declarations,
	)
	if err != nil {
		return fmt.Errorf("encode declarations: %w", err)
	}
	if publication.DeclarationSetDigest != declarationDigest {
		return errors.New("declaration_set_digest does not match declarations")
	}
	for name, digest := range map[string]string{
		"declaration_set_digest":    publication.DeclarationSetDigest,
		"candidate_manifest_digest": publication.CandidateManifestDigest,
		"resolver_pack_set_digest":  publication.ResolverPackSetDigest,
		"catalog_policy_digest":     publication.CatalogPolicyDigest,
		"generation_digest":         publication.GenerationDigest,
		"manifest_digest":           publication.ManifestDigest,
		"authority_digest":          publication.AuthorityDigest,
	} {
		if !validSHA256Digest(digest) {
			return fmt.Errorf("%s must be canonical sha256", name)
		}
	}
	if publication.SourceLanePolicy != resolverCatalogSourceLanePolicy {
		return errors.New("unsupported source_lane_policy")
	}
	if publication.ResolverPacks == nil ||
		len(publication.ResolverPacks) > maxResolverCatalogDeclarations {
		return errors.New("resolver_packs must be an explicit bounded set")
	}
	for index, pack := range publication.ResolverPacks {
		if !validResolverCatalogToken(pack.Name, 128) ||
			!validResolverCatalogToken(pack.Version, 64) ||
			index > 0 && publication.ResolverPacks[index-1].Name >= pack.Name {
			return fmt.Errorf("resolver pack %d is invalid or unordered", index)
		}
	}
	packDigest, err := resolverCatalogPackSetDigest(publication.ResolverPacks)
	if err != nil {
		return fmt.Errorf("encode resolver packs: %w", err)
	}
	if publication.ResolverPackSetDigest != packDigest {
		return errors.New("resolver_pack_set_digest does not match resolver_packs")
	}
	if publication.ManifestPath != resolvercatalogid.ManifestName(publication.Repository) {
		return errors.New("manifest_path must be the stable repository manifest name")
	}
	if requireStoreFields {
		if publication.ControlRevision == 0 {
			return errors.New("control_revision is required")
		}
		if publication.WriterSchema != resolverCatalogWriterSchema {
			return errors.New("writer_schema is incompatible")
		}
		if publication.PublishedAt.IsZero() {
			return errors.New("published_at is required")
		}
	}
	return nil
}

func resolverCatalogDeclarationSetDigest(
	declarations []ResolverCatalogDeclarationPublication,
) (string, error) {
	semantic := append([]ResolverCatalogDeclarationPublication{}, declarations...)
	for index := range semantic {
		semantic[index].RunID = ""
	}
	raw, err := json.Marshal(semantic)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func resolverCatalogPackSetDigest(
	packs []ResolverCatalogPack,
) (string, error) {
	raw, err := json.Marshal(packs)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func validResolverCatalogToken(value string, maxBytes int) bool {
	if value == "" || len(value) > maxBytes || !utf8.ValidString(value) ||
		strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if r <= 0x20 || r == 0x7f || r == '/' || r == '\\' {
			return false
		}
	}
	return true
}

func validResolverCatalogGenerationDigest(value string) bool {
	const prefix = "extraction_generation_v1_"
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+64 {
		return false
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(value, prefix))
	return err == nil &&
		hex.EncodeToString(decoded) == strings.TrimPrefix(value, prefix)
}

const publishResolverCatalogSQL = `
BEGIN;
LET $writer_ok = array::len(SELECT id FROM $migration_rid
	WHERE version = $migration_version LIMIT 1) = 1;
LET $caller_writer_ok = array::len(SELECT id FROM $caller_migration_rid
	WHERE version = $caller_migration_version LIMIT 1) = 1;
LET $evidence_writer_ok = array::len(SELECT id FROM $evidence_migration_rid
	WHERE version = $evidence_migration_version LIMIT 1) = 1;
LET $repo_state = (SELECT indexed_commit_hash, indexed_analysis_unit, deleting
	FROM $repo_rid)[0];
LET $candidate = (SELECT manifest_digest, policy_digest, control_revision
	FROM $candidate_rid)[0];
LET $current = (SELECT * FROM $publication_rid)[0];
LET $repo_ok = $repo_state != NONE
	AND ($repo_state.deleting = NONE OR $repo_state.deleting = false)
	AND $repo_state.indexed_commit_hash = $head_commit
	AND ($repo_state.indexed_analysis_unit.digest ?? '') = $unit_digest;
LET $candidate_ok = $candidate != NONE
	AND $candidate.manifest_digest = $candidate_manifest_digest;
LET $declaration_domains = $declarations.map(|$declaration| $declaration.domain);
LET $current_legacy_declarations = (SELECT domain, run_id,
	generation.digest AS generation_digest
	FROM extraction_domain_outcome
	WHERE repo = $repository
		AND commit = $head_commit
		AND unit_digest = $unit_digest
		AND domain IN $declaration_domains
		AND disposition = 'published'
		AND run_id != NONE
		AND generation.candidate_manifest_digest = $candidate_manifest_digest
		AND generation.candidate_policy_digest = $candidate.policy_digest
		AND generation.candidate_control_revision = $candidate.control_revision
		AND store_schema_version = $evidence_store_schema
		AND evidence_migration_version = $evidence_migration_version
	ORDER BY domain);
LET $current_partitioned_declarations = (SELECT domain, run_id,
	root_digest AS generation_digest,
	$partitioned_schema AS authority_schema, plan_digest, root_digest
	FROM extraction_domain_root
	WHERE repository = $repository
		AND domain IN $declaration_domains
		AND candidate_digest = $candidate_manifest_digest
	ORDER BY domain);
LET $uses_partitioned = array::len($declarations) > 0
	AND $declarations[0].authority_schema = $partitioned_schema;
LET $current_declarations = IF $uses_partitioned
	THEN $current_partitioned_declarations ELSE $current_legacy_declarations END;
LET $declarations_ok = $current_declarations = $declarations;
LET $same_generation = $current != NONE
	AND $current.repository = $repository
	AND $current.head_commit = $head_commit
	AND $current.unit_digest = $unit_digest
	AND $current.declarations = $declarations
	AND $current.declaration_set_digest = $declaration_set_digest
	AND $current.candidate_manifest_digest = $candidate_manifest_digest
	AND $current.source_lane_policy = $source_lane_policy
	AND $current.resolver_packs = $resolver_packs
	AND $current.resolver_pack_set_digest = $resolver_pack_set_digest
	AND $current.catalog_policy_digest = $catalog_policy_digest
	AND $current.generation_digest = $generation_digest;
LET $same_publication = $same_generation
	AND $current.manifest_digest = $manifest_digest
	AND $current.authority_digest = $authority_digest
	AND $current.manifest_path = $manifest_path
	AND $current.writer_schema = $writer_schema;
LET $current_revision = $current.control_revision ?? 0;
LET $control_ok = IF $current = NONE THEN $requested_revision IN [0, 1]
	ELSE IF $same_publication THEN
		$requested_revision IN [0, $current_revision]
	ELSE
		$requested_revision IN [0, $current_revision + 1]
	END;
LET $acceptable = $writer_ok AND $caller_writer_ok AND $evidence_writer_ok
	AND $repo_ok AND $candidate_ok AND $declarations_ok
	AND ($same_generation = false OR $same_publication)
	AND $control_ok;
LET $next_revision = IF $current = NONE THEN 1
	ELSE IF $same_publication THEN $current_revision
	ELSE $current_revision + 1 END;
LET $published = IF $acceptable = false THEN []
	ELSE IF $same_publication THEN [$current]
	ELSE (UPSERT $publication_rid SET
		repository = $repository,
		head_commit = $head_commit,
		unit_digest = $unit_digest,
		declarations = $declarations,
		declaration_set_digest = $declaration_set_digest,
		candidate_manifest_digest = $candidate_manifest_digest,
		source_lane_policy = $source_lane_policy,
		resolver_packs = $resolver_packs,
		resolver_pack_set_digest = $resolver_pack_set_digest,
		catalog_policy_digest = $catalog_policy_digest,
		generation_digest = $generation_digest,
		manifest_digest = $manifest_digest,
		authority_digest = $authority_digest,
		manifest_path = $manifest_path,
		control_revision = $next_revision,
		writer_schema = $writer_schema,
		published_at = time::now()
		RETURN AFTER)
	END;
LET $retired_caller = IF array::len($published) = 1
		AND $same_publication = false THEN
	(DELETE caller_generation_publication
		WHERE repository = $repository RETURN BEFORE)
	ELSE [] END;
LET $caller_revision = IF array::len($retired_caller) = 1 THEN
	(UPDATE $repo_rid SET caller_publication_revision =
		(caller_publication_revision ?? 0) + 1 RETURN AFTER)
	ELSE [] END;
LET $caller_force = $same_publication = false;
LET $pending_caller = IF array::len($published) = 1 THEN
	(SELECT id, created_at FROM caller_leaf_job
		WHERE pending_key = $repository AND status = 'pending'
		ORDER BY created_at LIMIT 1)[0].id
	ELSE NONE END;
LET $caller_fanout = IF array::len($published) != 1 THEN []
	ELSE IF $pending_caller != NONE THEN
		(UPDATE $pending_caller SET
			force = IF $caller_force THEN true ELSE force END,
			recovery_lease = NONE
			RETURN AFTER)
	ELSE (CREATE caller_leaf_job CONTENT {
		target: $repository,
		status: 'pending',
		attempts: 0,
		created_at: time::now(),
		pending_key: $repository,
		force: $caller_force
	} RETURN AFTER) END;
LET $caller_created = IF array::len($published) = 1 AND $pending_caller = NONE
	THEN $caller_fanout[0] ELSE NONE END;` + projectCreatedCallerJobSQL + `
RETURN IF array::len($caller_fanout) = 1
	AND (array::len($retired_caller) = 0
		OR array::len($caller_revision) = 1)
	THEN $published ELSE [] END;
COMMIT;`

// PublishResolverCatalog also atomically ensures the repository-keyed
// caller-leaf successor. A new semantic catalog generation forces execution;
// an exact accepted retry repairs a missing non-forced successor without ever
// downgrading an existing forced request.
func (s *Surreal) PublishResolverCatalog(
	ctx context.Context,
	publication ResolverCatalogPublication,
) error {
	publication.PublishedAt = time.Time{}
	publication.WriterSchema = resolverCatalogWriterSchema
	if err := validateResolverCatalogPublication(publication, false); err != nil {
		return fmt.Errorf("publish resolver catalog: %w", err)
	}
	results, err := surrealdb.Query[[]resolverCatalogPublicationRec](
		ctx, s.db, publishResolverCatalogSQL, map[string]any{
			"migration_rid":              resolverCatalogMigrationID(),
			"migration_version":          resolverCatalogWriterMigrationVersion,
			"caller_migration_rid":       callerGenerationPublicationMigrationID(),
			"caller_migration_version":   callerGenerationPublicationMigrationVersion,
			"evidence_migration_rid":     evidenceMigrationStateID(),
			"evidence_migration_version": evidenceMigrationVersion,
			"evidence_store_schema":      evidenceStoreSchemaVersion,
			"repo_rid":                   repoID(publication.Repository),
			"candidate_rid":              candidateManifestPublicationID(publication.Repository),
			"publication_rid":            resolverCatalogPublicationID(publication.Repository),
			"repository":                 publication.Repository,
			"head_commit":                publication.HeadCommit,
			"unit_digest":                publication.UnitDigest,
			"declarations":               publication.Declarations,
			"declaration_set_digest":     publication.DeclarationSetDigest,
			"candidate_manifest_digest":  publication.CandidateManifestDigest,
			"source_lane_policy":         publication.SourceLanePolicy,
			"resolver_packs":             publication.ResolverPacks,
			"resolver_pack_set_digest":   publication.ResolverPackSetDigest,
			"catalog_policy_digest":      publication.CatalogPolicyDigest,
			"generation_digest":          publication.GenerationDigest,
			"manifest_digest":            publication.ManifestDigest,
			"authority_digest":           publication.AuthorityDigest,
			"manifest_path":              publication.ManifestPath,
			"requested_revision":         publication.ControlRevision,
			"writer_schema":              resolverCatalogWriterSchema,
			"partitioned_schema":         PartitionedExtractionDomainSchema,
		},
	)
	if err != nil {
		return fmt.Errorf("publish resolver catalog: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 {
		return fmt.Errorf("publish resolver catalog: stale or conflicting authority: %w", ErrConflict)
	}
	if err := validateResolverCatalogPublication(
		rows[0].ResolverCatalogPublication, true,
	); err != nil {
		return fmt.Errorf("publish resolver catalog: persisted pointer: %w", err)
	}
	return nil
}

func (s *Surreal) GetResolverCatalogPublication(
	ctx context.Context,
	repository string,
) (*ResolverCatalogPublication, error) {
	if err := reponame.Validate(repository); err != nil {
		return nil, fmt.Errorf("get resolver catalog: %w", err)
	}
	results, err := surrealdb.Query[[]resolverCatalogPublicationRec](
		ctx, s.db, "SELECT * FROM $rid",
		map[string]any{"rid": resolverCatalogPublicationID(repository)},
	)
	if err != nil {
		return nil, fmt.Errorf("get resolver catalog: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) == 0 {
		return nil, fmt.Errorf("resolver catalog for %q: %w", repository, ErrNotFound)
	}
	publication := rows[0].ResolverCatalogPublication
	if publication.Repository != repository {
		return nil, fmt.Errorf("get resolver catalog: %w", ErrInvalidResolverCatalogPublication)
	}
	if err := validateResolverCatalogPublication(publication, true); err != nil {
		return nil, fmt.Errorf(
			"get resolver catalog: %w: %v",
			ErrInvalidResolverCatalogPublication, err,
		)
	}
	publication.PublishedAt = publication.PublishedAt.UTC()
	return &publication, nil
}

func (s *Surreal) ListResolverCatalogPublications(
	ctx context.Context,
) ([]ResolverCatalogPublication, error) {
	results, err := surrealdb.Query[[]resolverCatalogPublicationRec](
		ctx, s.db,
		"SELECT * FROM resolver_catalog_publication ORDER BY repository", nil,
	)
	if err != nil {
		return nil, fmt.Errorf("list resolver catalogs: %w", err)
	}
	rows := firstDomainRows(results)
	publications := make([]ResolverCatalogPublication, len(rows))
	for index := range rows {
		publication := rows[index].ResolverCatalogPublication
		if err := validateResolverCatalogPublication(publication, true); err != nil {
			return nil, fmt.Errorf(
				"list resolver catalogs: row %d: %w: %v",
				index, ErrInvalidResolverCatalogPublication, err,
			)
		}
		publication.PublishedAt = publication.PublishedAt.UTC()
		publications[index] = publication
	}
	return publications, nil
}

// ResolverCatalogPublicationCurrent rechecks the mutable store authorities
// that are deliberately absent from filesystem validation. Reconciliation
// calls it before accepting a warm pointer, including after an older binary or
// raw restore may have advanced repository/candidate state.
func (s *Surreal) ResolverCatalogPublicationCurrent(
	ctx context.Context,
	publication ResolverCatalogPublication,
) (bool, error) {
	if err := validateResolverCatalogPublication(publication, true); err != nil {
		return false, fmt.Errorf("check resolver catalog authority: %w", err)
	}
	results, err := surrealdb.Query[[]resolverCatalogCurrentResult](
		ctx, s.db, `
LET $repo = (SELECT indexed_commit_hash, indexed_analysis_unit, deleting
	FROM $repo_rid)[0];
LET $candidate = (SELECT manifest_digest, policy_digest, control_revision
	FROM $candidate_rid)[0];
LET $catalog = (SELECT manifest_digest, control_revision, writer_schema
	FROM $catalog_rid)[0];
LET $declaration_domains = $declarations.map(|$declaration| $declaration.domain);
LET $current_legacy_declarations = (SELECT domain, run_id,
	generation.digest AS generation_digest
	FROM extraction_domain_outcome
	WHERE repo = $repository
		AND commit = $head_commit
		AND unit_digest = $unit_digest
		AND domain IN $declaration_domains
		AND disposition = 'published'
		AND run_id != NONE
		AND generation.candidate_manifest_digest = $candidate_manifest_digest
		AND generation.candidate_policy_digest = $candidate.policy_digest
		AND generation.candidate_control_revision = $candidate.control_revision
		AND store_schema_version = $evidence_store_schema
		AND evidence_migration_version = $evidence_migration_version
	ORDER BY domain);
LET $current_partitioned_declarations = (SELECT domain, run_id,
	root_digest AS generation_digest,
	$partitioned_schema AS authority_schema, plan_digest, root_digest
	FROM extraction_domain_root
	WHERE repository = $repository
		AND domain IN $declaration_domains
		AND candidate_digest = $candidate_manifest_digest
	ORDER BY domain);
LET $uses_partitioned = array::len($declarations) > 0
	AND $declarations[0].authority_schema = $partitioned_schema;
LET $current_declarations = IF $uses_partitioned
	THEN $current_partitioned_declarations ELSE $current_legacy_declarations END;
RETURN [{
	current: $repo != NONE
		AND ($repo.deleting = NONE OR $repo.deleting = false)
		AND $repo.indexed_commit_hash = $head_commit
		AND ($repo.indexed_analysis_unit.digest ?? '') = $unit_digest
		AND $candidate != NONE
		AND $candidate.manifest_digest = $candidate_manifest_digest
		AND $catalog != NONE
		AND $catalog.manifest_digest = $manifest_digest
		AND $catalog.control_revision = $control_revision
		AND $catalog.writer_schema = $writer_schema
		AND $current_declarations = $declarations
}];`, map[string]any{
			"repo_rid":                   repoID(publication.Repository),
			"candidate_rid":              candidateManifestPublicationID(publication.Repository),
			"catalog_rid":                resolverCatalogPublicationID(publication.Repository),
			"head_commit":                publication.HeadCommit,
			"unit_digest":                publication.UnitDigest,
			"candidate_manifest_digest":  publication.CandidateManifestDigest,
			"manifest_digest":            publication.ManifestDigest,
			"control_revision":           publication.ControlRevision,
			"writer_schema":              resolverCatalogWriterSchema,
			"repository":                 publication.Repository,
			"declarations":               publication.Declarations,
			"evidence_store_schema":      evidenceStoreSchemaVersion,
			"evidence_migration_version": evidenceMigrationVersion,
			"partitioned_schema":         PartitionedExtractionDomainSchema,
		},
	)
	if err != nil {
		return false, fmt.Errorf("check resolver catalog authority: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 {
		return false, errors.New("check resolver catalog authority: empty result")
	}
	return rows[0].Current, nil
}

func (s *Surreal) ClearResolverCatalogPublication(
	ctx context.Context,
	repository string,
) error {
	if err := reponame.Validate(repository); err != nil {
		return fmt.Errorf("clear resolver catalog: %w", err)
	}
	results, err := surrealdb.Query[any](ctx, s.db, `
BEGIN;
LET $writer_ok = array::len(SELECT id FROM $migration_rid
	WHERE version = $migration_version LIMIT 1) = 1;
LET $caller_writer_ok = array::len(SELECT id FROM $caller_migration_rid
	WHERE version = $caller_migration_version LIMIT 1) = 1;
IF $writer_ok = false OR $caller_writer_ok = false {
	THROW 'phebs-permanent: resolver catalog writer generation is not active'
};
DELETE $rid RETURN NONE;
LET $retired_caller = DELETE caller_generation_publication
	WHERE repository = $repository RETURN BEFORE;
IF array::len($retired_caller) = 1 {
	UPDATE $repo_rid SET caller_publication_revision =
		(caller_publication_revision ?? 0) + 1 RETURN NONE;
};
COMMIT;`,
		map[string]any{
			"rid":                      resolverCatalogPublicationID(repository),
			"migration_rid":            resolverCatalogMigrationID(),
			"migration_version":        resolverCatalogWriterMigrationVersion,
			"repository":               repository,
			"repo_rid":                 repoID(repository),
			"caller_migration_rid":     callerGenerationPublicationMigrationID(),
			"caller_migration_version": callerGenerationPublicationMigrationVersion,
		},
	)
	if err != nil {
		return fmt.Errorf("clear resolver catalog: %w", err)
	}
	for index, result := range *results {
		if result.Error != nil {
			return fmt.Errorf(
				"clear resolver catalog statement %d: %s",
				index, result.Error.Message,
			)
		}
	}
	return nil
}

// ClearAllResolverCatalogPublications discards imported derived authority
// without decoding rows, including malformed rows.
func (s *Surreal) ClearAllResolverCatalogPublications(ctx context.Context) error {
	results, err := surrealdb.Query[any](ctx, s.db, `
BEGIN;
LET $writer_ok = array::len(SELECT id FROM $migration_rid
	WHERE version = $migration_version LIMIT 1) = 1;
LET $caller_writer_ok = array::len(SELECT id FROM $caller_migration_rid
	WHERE version = $caller_migration_version LIMIT 1) = 1;
IF $writer_ok = false OR $caller_writer_ok = false {
	THROW 'phebs-permanent: resolver catalog writer generation is not active'
};
LET $caller_repositories = SELECT VALUE record::id(id)
	FROM caller_generation_publication;
UPDATE repo SET caller_publication_revision =
	(caller_publication_revision ?? 0) + 1
	WHERE name IN $caller_repositories RETURN NONE;
DELETE resolver_catalog_publication RETURN NONE;
DELETE caller_generation_publication RETURN NONE;
COMMIT;`, map[string]any{
		"migration_rid":            resolverCatalogMigrationID(),
		"migration_version":        resolverCatalogWriterMigrationVersion,
		"caller_migration_rid":     callerGenerationPublicationMigrationID(),
		"caller_migration_version": callerGenerationPublicationMigrationVersion,
	})
	if err != nil {
		return fmt.Errorf("clear all resolver catalogs: %w", err)
	}
	for index, result := range *results {
		if result.Error != nil {
			return fmt.Errorf(
				"clear all resolver catalogs statement %d: %s",
				index, result.Error.Message,
			)
		}
	}
	return nil
}
