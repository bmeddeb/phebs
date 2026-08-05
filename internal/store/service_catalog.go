package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"

	"github.com/bmeddeb/phebs/internal/servicecatalog"
)

var ErrInvalidServiceCatalogPublication = errors.New(
	"invalid service catalog publication",
)

// ServiceCatalogPublicationStore owns precious immutable catalog generations
// and their one atomic repository-local current pointer. It is deliberately
// separate from Store so existing pipeline fakes do not gain a T33.2 surface.
type ServiceCatalogPublicationStore interface {
	PublishServiceCatalog(context.Context, servicecatalog.Publication) error
	GetServiceCatalog(context.Context, string) (*servicecatalog.Publication, error)
}

var _ ServiceCatalogPublicationStore = (*Surreal)(nil)

type serviceCatalogGenerationRec struct {
	Schema                   string           `json:"schema"`
	Repository               string           `json:"repository"`
	SourceKind               string           `json:"source_kind"`
	SourcePath               string           `json:"source_path"`
	SourceCommit             string           `json:"source_commit"`
	SourceCensusDigest       string           `json:"source_census_digest"`
	SourceFileCount          int              `json:"source_file_count"`
	AcceptedFileCount        int              `json:"accepted_file_count"`
	UnownedFileCount         int              `json:"unowned_file_count"`
	AuthorityKind            string           `json:"authority_kind"`
	AuthorityID              string           `json:"authority_id"`
	AuthorityVersion         string           `json:"authority_version"`
	OverrideID               string           `json:"override_id"`
	OverrideVersion          string           `json:"override_version"`
	CatalogDigest            string           `json:"catalog_digest"`
	LegacyAnalysisUnitDigest string           `json:"legacy_analysis_unit_digest"`
	GenerationDigest         string           `json:"generation_digest"`
	CatalogJSON              string           `json:"catalog_json"`
	RecordedAt               time.Time        `json:"recorded_at"`
	RecID                    *models.RecordID `json:"id"`
}

type serviceCatalogCurrentRec struct {
	Repository       string           `json:"repository"`
	GenerationDigest string           `json:"generation_digest"`
	ControlRevision  uint64           `json:"control_revision"`
	PublishedAt      time.Time        `json:"published_at"`
	RecID            *models.RecordID `json:"id"`
}

type serviceCatalogAuthorityVersionRec struct {
	Repository       string           `json:"repository"`
	AuthorityKind    string           `json:"authority_kind"`
	AuthorityID      string           `json:"authority_id"`
	AuthorityVersion string           `json:"authority_version"`
	OverrideID       string           `json:"override_id"`
	OverrideVersion  string           `json:"override_version"`
	CatalogDigest    string           `json:"catalog_digest"`
	RecordedAt       time.Time        `json:"recorded_at"`
	RecID            *models.RecordID `json:"id"`
}

func serviceCatalogGenerationID(digest string) models.RecordID {
	return models.NewRecordID(
		"service_catalog_generation",
		digest[len("sha256:"):],
	)
}

func serviceCatalogCurrentID(repository string) models.RecordID {
	return models.NewRecordID("service_catalog_current", repository)
}

func serviceCatalogAuthorityVersionID(
	publication servicecatalog.Publication,
) models.RecordID {
	hash := sha256.New()
	_, _ = hash.Write([]byte("phebs-service-catalog-authority-version-v1\x00"))
	for _, value := range []string{
		publication.Repository,
		publication.Authority.Kind,
		publication.Authority.ID,
		publication.Authority.Version,
	} {
		_, _ = hash.Write([]byte(value))
		_, _ = hash.Write([]byte{0})
	}
	if publication.Override != nil {
		_, _ = hash.Write([]byte(publication.Override.ID))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(publication.Override.Version))
	}
	return models.NewRecordID(
		"service_catalog_authority_version", fmt.Sprintf("%x", hash.Sum(nil)),
	)
}

const publishServiceCatalogSQL = `
BEGIN;
LET $repo = (SELECT indexed_commit_hash, indexed_analysis_unit, deleting
	FROM $repo_rid)[0];
LET $current = (SELECT * FROM $current_rid)[0];
LET $generation = (SELECT * FROM $generation_rid)[0];
LET $version = (SELECT * FROM $version_rid)[0];
LET $repo_ok = $repo != NONE
	AND ($repo.deleting = NONE OR $repo.deleting = false)
	AND $repo.indexed_commit_hash = $source_commit
	AND ($legacy_digest = ''
		OR ($repo.indexed_analysis_unit.digest ?? '') = $legacy_digest);
LET $version_matches = $version != NONE
	AND $version.repository = $repository
	AND $version.authority_kind = $authority_kind
	AND $version.authority_id = $authority_id
	AND $version.authority_version = $authority_version
	AND $version.override_id = $override_id
	AND $version.override_version = $override_version
	AND $version.catalog_digest = $catalog_digest;
LET $generation_matches = $generation != NONE
	AND $generation.schema = $schema
	AND $generation.repository = $repository
	AND $generation.source_kind = $source_kind
	AND $generation.source_path = $source_path
	AND $generation.source_commit = $source_commit
	AND $generation.source_census_digest = $source_census_digest
	AND $generation.source_file_count = $source_file_count
	AND $generation.accepted_file_count = $accepted_file_count
	AND $generation.unowned_file_count = $unowned_file_count
	AND $generation.authority_kind = $authority_kind
	AND $generation.authority_id = $authority_id
	AND $generation.authority_version = $authority_version
	AND $generation.override_id = $override_id
	AND $generation.override_version = $override_version
	AND $generation.catalog_digest = $catalog_digest
	AND $generation.legacy_analysis_unit_digest = $legacy_digest
	AND $generation.generation_digest = $generation_digest
	AND $generation.catalog_json = $catalog_json;
LET $stored_generation = IF $repo_ok = false
		OR ($version != NONE AND $version_matches = false) THEN []
	ELSE IF $generation = NONE THEN
		(CREATE $generation_rid CONTENT {
			schema: $schema,
			repository: $repository,
			source_kind: $source_kind,
			source_path: $source_path,
			source_commit: $source_commit,
			source_census_digest: $source_census_digest,
			source_file_count: $source_file_count,
			accepted_file_count: $accepted_file_count,
			unowned_file_count: $unowned_file_count,
			authority_kind: $authority_kind,
			authority_id: $authority_id,
			authority_version: $authority_version,
			override_id: $override_id,
			override_version: $override_version,
			catalog_digest: $catalog_digest,
			legacy_analysis_unit_digest: $legacy_digest,
			generation_digest: $generation_digest,
			catalog_json: $catalog_json,
			recorded_at: $recorded_at
		} RETURN AFTER)
	ELSE IF $generation_matches THEN [$generation]
	ELSE [] END;
LET $stored_version = IF array::len($stored_generation) != 1 THEN []
	ELSE IF $version = NONE THEN
		(CREATE $version_rid CONTENT {
			repository: $repository,
			authority_kind: $authority_kind,
			authority_id: $authority_id,
			authority_version: $authority_version,
			override_id: $override_id,
			override_version: $override_version,
			catalog_digest: $catalog_digest,
			recorded_at: $recorded_at
		} RETURN AFTER)
	ELSE [$version] END;
LET $same_current = $current != NONE
	AND $current.repository = $repository
	AND $current.generation_digest = $generation_digest;
LET $next_revision = IF $current = NONE THEN 1
	ELSE ($current.control_revision ?? 0) + 1 END;
LET $published = IF array::len($stored_generation) != 1
		OR array::len($stored_version) != 1 THEN []
	ELSE IF $same_current THEN [$current]
	ELSE
		(UPSERT $current_rid SET
			repository = $repository,
			generation_digest = $generation_digest,
			control_revision = $next_revision,
			published_at = $published_at
			RETURN AFTER)
	END;
RETURN $published;
COMMIT;`

// PublishServiceCatalog atomically stores a new immutable generation and
// advances its current pointer only after repository, legacy, version-reuse,
// and exact-generation fences pass. A refusal cannot change the prior pointer.
func (s *Surreal) PublishServiceCatalog(
	ctx context.Context,
	publication servicecatalog.Publication,
) error {
	publication.ControlRevision = 0
	publication.PublishedAt = time.Time{}
	if err := servicecatalog.ValidatePublication(publication, false); err != nil {
		return fmt.Errorf("publish service catalog: %w", err)
	}
	overrideID, overrideVersion := "", ""
	if publication.Override != nil {
		overrideID = publication.Override.ID
		overrideVersion = publication.Override.Version
	}
	now := storeTimestamp(time.Now())
	vars := map[string]any{
		"repo_rid":             repoID(publication.Repository),
		"current_rid":          serviceCatalogCurrentID(publication.Repository),
		"generation_rid":       serviceCatalogGenerationID(publication.GenerationDigest),
		"version_rid":          serviceCatalogAuthorityVersionID(publication),
		"schema":               publication.Schema,
		"repository":           publication.Repository,
		"source_kind":          publication.SourceKind,
		"source_path":          publication.SourcePath,
		"source_commit":        publication.SourceCommit,
		"source_census_digest": publication.SourceCensusDigest,
		"source_file_count":    publication.SourceFileCount,
		"accepted_file_count":  publication.AcceptedFileCount,
		"unowned_file_count":   publication.UnownedFileCount,
		"authority_kind":       publication.Authority.Kind,
		"authority_id":         publication.Authority.ID,
		"authority_version":    publication.Authority.Version,
		"override_id":          overrideID,
		"override_version":     overrideVersion,
		"catalog_digest":       publication.CatalogDigest,
		"legacy_digest":        publication.LegacyAnalysisUnitDigest,
		"generation_digest":    publication.GenerationDigest,
		"catalog_json":         string(publication.Canonical),
		"recorded_at":          now,
		"published_at":         now,
	}
	for attempt := 0; ; attempt++ {
		results, err := surrealdb.Query[[]serviceCatalogCurrentRec](
			ctx, s.db, publishServiceCatalogSQL, vars,
		)
		if err != nil {
			if isRetryableEnqueue(err) && ctx.Err() == nil && attempt+1 < maxQueueRetries {
				continue
			}
			return fmt.Errorf("publish service catalog: %w", err)
		}
		rows := firstDomainRows(results)
		if len(rows) != 1 {
			return fmt.Errorf(
				"publish service catalog: stale repository, reused authority version, or immutable generation conflict: %w",
				ErrConflict,
			)
		}
		stored, err := s.GetServiceCatalog(ctx, publication.Repository)
		if err != nil {
			return fmt.Errorf("publish service catalog: strict-open result: %w", err)
		}
		if stored.GenerationDigest != publication.GenerationDigest {
			return fmt.Errorf("publish service catalog: current generation mismatch: %w", ErrConflict)
		}
		return nil
	}
}

// GetServiceCatalog strict-opens the current pointer and immutable generation;
// malformed restored or externally mutated rows are never returned as authority.
func (s *Surreal) GetServiceCatalog(
	ctx context.Context,
	repository string,
) (*servicecatalog.Publication, error) {
	if err := validateCandidateRepository(repository); err != nil {
		return nil, fmt.Errorf("get service catalog: repository: %w", err)
	}
	currentResults, err := surrealdb.Query[[]serviceCatalogCurrentRec](
		ctx, s.db, "SELECT * FROM $rid",
		map[string]any{"rid": serviceCatalogCurrentID(repository)},
	)
	if err != nil {
		return nil, fmt.Errorf("get service catalog: current pointer: %w", err)
	}
	currentRows := firstDomainRows(currentResults)
	if len(currentRows) == 0 {
		return nil, fmt.Errorf("service catalog for %q: %w", repository, ErrNotFound)
	}
	current := currentRows[0]
	if current.Repository != repository || !validSHA256Digest(current.GenerationDigest) ||
		current.ControlRevision == 0 || current.PublishedAt.IsZero() {
		return nil, fmt.Errorf(
			"get service catalog: %w: malformed current pointer",
			ErrInvalidServiceCatalogPublication,
		)
	}
	generationResults, err := surrealdb.Query[[]serviceCatalogGenerationRec](
		ctx, s.db, "SELECT * FROM $rid",
		map[string]any{"rid": serviceCatalogGenerationID(current.GenerationDigest)},
	)
	if err != nil {
		return nil, fmt.Errorf("get service catalog: generation: %w", err)
	}
	generationRows := firstDomainRows(generationResults)
	if len(generationRows) != 1 {
		return nil, fmt.Errorf(
			"get service catalog: %w: current generation is absent",
			ErrInvalidServiceCatalogPublication,
		)
	}
	generation := generationRows[0]
	publication := servicecatalog.Publication{
		Schema: generation.Schema, Repository: generation.Repository,
		SourceKind: generation.SourceKind, SourcePath: generation.SourcePath,
		SourceCommit:       generation.SourceCommit,
		SourceCensusDigest: generation.SourceCensusDigest,
		SourceFileCount:    generation.SourceFileCount,
		AcceptedFileCount:  generation.AcceptedFileCount,
		UnownedFileCount:   generation.UnownedFileCount,
		Authority: servicecatalog.Authority{
			Kind: generation.AuthorityKind, ID: generation.AuthorityID,
			Version: generation.AuthorityVersion,
		},
		CatalogDigest:            generation.CatalogDigest,
		LegacyAnalysisUnitDigest: generation.LegacyAnalysisUnitDigest,
		GenerationDigest:         generation.GenerationDigest,
		Canonical:                []byte(generation.CatalogJSON),
		ControlRevision:          current.ControlRevision,
		PublishedAt:              current.PublishedAt.UTC(),
	}
	if generation.OverrideID != "" || generation.OverrideVersion != "" {
		publication.Override = &servicecatalog.OperatorOverride{
			ID: generation.OverrideID, Version: generation.OverrideVersion,
		}
	}
	if publication.Repository != repository ||
		publication.GenerationDigest != current.GenerationDigest {
		return nil, fmt.Errorf(
			"get service catalog: %w: pointer and generation disagree",
			ErrInvalidServiceCatalogPublication,
		)
	}
	if err := servicecatalog.ValidatePublication(publication, true); err != nil {
		return nil, fmt.Errorf(
			"get service catalog: %w: %v",
			ErrInvalidServiceCatalogPublication, err,
		)
	}
	versionResults, err := surrealdb.Query[[]serviceCatalogAuthorityVersionRec](
		ctx, s.db, "SELECT * FROM $rid",
		map[string]any{"rid": serviceCatalogAuthorityVersionID(publication)},
	)
	if err != nil {
		return nil, fmt.Errorf("get service catalog: authority version: %w", err)
	}
	versionRows := firstDomainRows(versionResults)
	if len(versionRows) != 1 {
		return nil, fmt.Errorf(
			"get service catalog: %w: authority-version claim is absent",
			ErrInvalidServiceCatalogPublication,
		)
	}
	version := versionRows[0]
	overrideID, overrideVersion := "", ""
	if publication.Override != nil {
		overrideID = publication.Override.ID
		overrideVersion = publication.Override.Version
	}
	if version.Repository != publication.Repository ||
		version.AuthorityKind != publication.Authority.Kind ||
		version.AuthorityID != publication.Authority.ID ||
		version.AuthorityVersion != publication.Authority.Version ||
		version.OverrideID != overrideID || version.OverrideVersion != overrideVersion ||
		version.CatalogDigest != publication.CatalogDigest || version.RecordedAt.IsZero() {
		return nil, fmt.Errorf(
			"get service catalog: %w: authority-version claim is inconsistent",
			ErrInvalidServiceCatalogPublication,
		)
	}
	return &publication, nil
}
