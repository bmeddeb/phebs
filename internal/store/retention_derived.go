package store

import (
	"context"
	"errors"
	"fmt"
	"slices"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/reponame"
)

// RetentionFocusedRepositoryAuthority is the bounded store projection needed
// to inventory one focused-index filesystem authority. Schemaless repository
// metadata and unrelated mutable repository fields never cross this boundary.
type RetentionFocusedRepositoryAuthority struct {
	Repository          string              `json:"repository"`
	IndexedCommitHash   string              `json:"indexed_commit_hash"`
	IndexedAnalysisUnit *analysisunit.State `json:"indexed_analysis_unit"`
}

// DerivedRetentionCollection carries the three store-backed summaries and the
// four bounded authority selections needed by T30.6r's physical collectors.
// Results preserves the request order after omitting the caller-artifact
// support request, whose store side is an authority selection rather than a
// database-row component.
type DerivedRetentionCollection struct {
	Results                 []RetentionComponentResult
	CandidateAuthorities    []CandidateManifestPublication
	FocusedAuthorities      []RetentionFocusedRepositoryAuthority
	ResolverAuthorities     []ResolverCatalogPublication
	CallerAuthorities       []CallerGenerationPublicationSummary
	FocusedPartial          bool
	CallerScannedIdentities int
	CallerTruncated         bool
	CallerErr               error
}

// DerivedRetentionStore is the T30.6r bounded store-authority boundary. It is
// intentionally separate from the T30.6p/q logical-row collectors.
type DerivedRetentionStore interface {
	CollectDerivedRetention(context.Context, []RetentionComponentRequest) (DerivedRetentionCollection, error)
}

var _ DerivedRetentionStore = (*Surreal)(nil)

const (
	maxDerivedRetentionReportedPerComponent = 78
	maxDerivedRetentionRequests             = 4
	maxDerivedRetentionReportedIdentities   = 312
	maxDerivedRetentionScanIdentities       = 316
)

type derivedRetentionKind int

const (
	derivedRetentionCandidate derivedRetentionKind = iota + 1
	derivedRetentionFocused
	derivedRetentionResolver
	derivedRetentionCaller
)

type derivedRetentionPlan struct {
	table string
	kind  derivedRetentionKind
}

var derivedRetentionPlans = map[RetentionComponent]derivedRetentionPlan{
	RetentionCandidatePublication: {
		table: "candidate_manifest_publication", kind: derivedRetentionCandidate,
	},
	RetentionFocusedRepositoryState: {
		table: "repo", kind: derivedRetentionFocused,
	},
	RetentionResolverPublication: {
		table: "resolver_catalog_publication", kind: derivedRetentionResolver,
	},
	RetentionCallerArtifacts: {
		table: "caller_generation_publication", kind: derivedRetentionCaller,
	},
}

const derivedRetentionCatalogSQL = `
RETURN array::intersect(
	object::keys((INFO FOR DB).tables),
	[
		'candidate_manifest_publication',
		'repo',
		'resolver_catalog_publication',
		'caller_generation_publication'
	]
)`

const derivedRetentionCandidateSQL = `SELECT id, repository, head_commit,
	unit_digest, policy_digest, manifest_digest, generation_digest,
	manifest_path, control_revision, published_at
	FROM candidate_manifest_publication ORDER BY id LIMIT $scan_limit`

const derivedRetentionFocusedSQL = `SELECT id, name, indexed_commit_hash,
	indexed_analysis_unit
	FROM repo ORDER BY id LIMIT $scan_limit`

const derivedRetentionResolverSQL = `SELECT id, repository, head_commit,
	unit_digest, declarations, declaration_set_digest,
	candidate_manifest_digest, source_lane_policy, resolver_packs,
	resolver_pack_set_digest, catalog_policy_digest, generation_digest,
	manifest_digest, manifest_path, control_revision, writer_schema, published_at
	FROM resolver_catalog_publication ORDER BY id LIMIT $scan_limit`

const derivedRetentionCallerSQL = `SELECT ` + callerGenerationPublicationSummaryProjection + `
	FROM caller_generation_publication ORDER BY id LIMIT $scan_limit`

// A single Surreal statement supplies one exact query-result envelope and one
// database snapshot for every selected caller authority. It deliberately
// checks the stored pair-array length but never transfers or hashes pairs.
const derivedRetentionCallerAuthoritiesCurrentSQL = `
RETURN [{
	current: array::len(SELECT id FROM $migration_rid
		WHERE version = $migration_version LIMIT 1) = 1
		AND array::all(
			$bindings.map(|$binding| {
				expected: $binding.summary,
				repository: (SELECT indexed_commit_hash,
					indexed_analysis_unit, deleting,
					caller_publication_revision
					FROM $binding.repository_rid)[0],
				candidate: (SELECT head_commit, unit_digest,
					manifest_digest, policy_digest, control_revision
					FROM $binding.candidate_rid)[0],
				resolver: (SELECT head_commit, unit_digest,
					candidate_manifest_digest, declaration_set_digest,
					generation_digest, manifest_digest, control_revision,
					writer_schema FROM $binding.resolver_rid)[0],
				publication: (SELECT ` + callerGenerationPublicationSummaryProjection + `
					FROM $binding.publication_rid)[0]
			}).map(|$authority|
				$authority.repository != NONE
					AND ($authority.repository.deleting = NONE
						OR $authority.repository.deleting = false)
					AND $authority.repository.indexed_commit_hash =
						$authority.expected.generation.head_commit
					AND ($authority.repository.indexed_analysis_unit.digest ?? '') =
						$authority.expected.generation.unit_digest
					AND $authority.repository.caller_publication_revision =
						$authority.expected.publication_revision
					AND $authority.candidate != NONE
					AND $authority.candidate.head_commit =
						$authority.expected.generation.head_commit
					AND $authority.candidate.unit_digest =
						$authority.expected.generation.unit_digest
					AND $authority.candidate.manifest_digest =
						$authority.expected.generation.candidate_manifest_digest
					AND $authority.candidate.policy_digest =
						$authority.expected.generation.candidate_policy_digest
					AND $authority.candidate.control_revision =
						$authority.expected.generation.candidate_control_revision
					AND $authority.resolver != NONE
					AND $authority.resolver.head_commit =
						$authority.expected.generation.head_commit
					AND $authority.resolver.unit_digest =
						$authority.expected.generation.unit_digest
					AND $authority.resolver.candidate_manifest_digest =
						$authority.expected.generation.candidate_manifest_digest
					AND $authority.resolver.declaration_set_digest =
						$authority.expected.generation.declaration_set_digest
					AND $authority.resolver.generation_digest =
						$authority.expected.generation.resolver_generation_digest
					AND $authority.resolver.manifest_digest =
						$authority.expected.generation.resolver_manifest_digest
					AND $authority.resolver.control_revision =
						$authority.expected.generation.resolver_control_revision
					AND $authority.resolver.writer_schema =
						$authority.expected.generation.resolver_writer_schema
					AND $authority.publication != NONE
					AND $authority.publication.repository =
						$authority.expected.generation.repository
					AND $authority.publication.generation =
						$authority.expected.generation
					AND $authority.publication.generation_digest =
						$authority.expected.generation.digest
					AND $authority.publication.pair_payload_count =
						$authority.expected.pair_count
					AND $authority.publication.pair_payload_digest =
						$authority.expected.pair_payload_digest
					AND $authority.publication.pair_set_digest =
						$authority.expected.pair_set_digest
					AND $authority.publication.pair_count =
						$authority.expected.pair_count
					AND $authority.publication.artifact_count =
						$authority.expected.artifact_count
					AND $authority.publication.result_count =
						$authority.expected.result_count
					AND $authority.publication.abstention_count =
						$authority.expected.abstention_count
					AND $authority.publication.canonical_bytes =
						$authority.expected.canonical_bytes
					AND $authority.publication.staging_bytes =
						$authority.expected.staging_bytes
					AND $authority.publication.peak_open_files =
						$authority.expected.peak_open_files
					AND $authority.publication.manifest_digest =
						$authority.expected.manifest_digest
					AND $authority.publication.manifest_path =
						$authority.expected.manifest_path
					AND $authority.publication.publication_revision =
						$authority.expected.publication_revision
					AND $authority.publication.publication_incarnation =
						$authority.expected.publication_incarnation
					AND $authority.publication.writer_schema = $writer_schema
			)
		)
}]`

type derivedRetentionFocusedRow struct {
	Name                string              `json:"name"`
	IndexedCommitHash   string              `json:"indexed_commit_hash"`
	IndexedAnalysisUnit *analysisunit.State `json:"indexed_analysis_unit"`
	RecID               *models.RecordID    `json:"id"`
}

func validateDerivedRetentionRequests(requests []RetentionComponentRequest) error {
	if len(requests) > maxDerivedRetentionRequests {
		return fmt.Errorf(
			"collect derived retention: %d requests exceed %d",
			len(requests), maxDerivedRetentionRequests,
		)
	}
	seen := make(map[RetentionComponent]struct{}, len(requests))
	reportedTotal, scanTotal := 0, 0
	for _, request := range requests {
		if _, allowed := derivedRetentionPlans[request.Component]; !allowed {
			return fmt.Errorf(
				"collect derived retention: unsupported component %q",
				request.Component,
			)
		}
		if _, duplicate := seen[request.Component]; duplicate {
			return fmt.Errorf(
				"collect derived retention: duplicate component %q",
				request.Component,
			)
		}
		seen[request.Component] = struct{}{}
		if request.ReportedIdentities <= 0 ||
			request.ReportedIdentities > maxDerivedRetentionReportedPerComponent ||
			request.ScanIdentities != request.ReportedIdentities+1 {
			return fmt.Errorf(
				"collect derived retention: invalid allocation for %q",
				request.Component,
			)
		}
		reportedTotal += request.ReportedIdentities
		scanTotal += request.ScanIdentities
	}
	if reportedTotal > maxDerivedRetentionReportedIdentities ||
		scanTotal > maxDerivedRetentionScanIdentities {
		return fmt.Errorf(
			"collect derived retention: aggregate allocation %d/%d exceeds %d/%d",
			reportedTotal, scanTotal,
			maxDerivedRetentionReportedIdentities,
			maxDerivedRetentionScanIdentities,
		)
	}
	return nil
}

func emptyDerivedRetentionCollection(resultCapacity int) DerivedRetentionCollection {
	return DerivedRetentionCollection{
		Results:              make([]RetentionComponentResult, 0, resultCapacity),
		CandidateAuthorities: []CandidateManifestPublication{},
		FocusedAuthorities:   []RetentionFocusedRepositoryAuthority{},
		ResolverAuthorities:  []ResolverCatalogPublication{},
		CallerAuthorities:    []CallerGenerationPublicationSummary{},
	}
}

// CollectDerivedRetention performs one fixed catalog intersection, at most one
// direct id-ordered LIMIT query per accepted component, and one bounded caller
// authority fence when caller rows are selected. Every component failure is
// localized so one unavailable derived authority cannot erase an independent
// peer's status.
func (s *Surreal) CollectDerivedRetention(
	ctx context.Context,
	requests []RetentionComponentRequest,
) (DerivedRetentionCollection, error) {
	if err := validateDerivedRetentionRequests(requests); err != nil {
		return DerivedRetentionCollection{}, err
	}
	collection := emptyDerivedRetentionCollection(len(requests))
	if len(requests) == 0 {
		return collection, nil
	}

	tables, catalogErr := s.derivedRetentionCatalogTables(ctx)
	if err := ctx.Err(); err != nil {
		return DerivedRetentionCollection{}, err
	}
	readiness := make(map[derivedRetentionKind]retentionReadyResult, 3)
	for _, request := range requests {
		if err := ctx.Err(); err != nil {
			return DerivedRetentionCollection{}, err
		}
		plan := derivedRetentionPlans[request.Component]
		if catalogErr != nil {
			collection.recordDerivedRetentionError(request.Component, catalogErr)
			continue
		}
		if _, present := tables[plan.table]; !present {
			collection.recordDerivedRetentionError(request.Component, fmt.Errorf(
				"%w: table %s is absent from the database catalog",
				ErrRetentionComponentUnavailable, plan.table,
			))
			continue
		}
		if plan.kind != derivedRetentionFocused {
			ready, checked := readiness[plan.kind]
			if !checked {
				ready.ready, ready.err = s.derivedRetentionReadiness(ctx, plan.kind)
				readiness[plan.kind] = ready
			}
			if err := ctx.Err(); err != nil {
				return DerivedRetentionCollection{}, err
			}
			if ready.err != nil {
				collection.recordDerivedRetentionError(request.Component, ready.err)
				continue
			}
			if !ready.ready {
				collection.recordDerivedRetentionError(
					request.Component, ErrRetentionComponentUnavailable,
				)
				continue
			}
		}

		var err error
		switch plan.kind {
		case derivedRetentionCandidate:
			err = s.collectDerivedCandidate(ctx, request, &collection)
		case derivedRetentionFocused:
			err = s.collectDerivedFocused(ctx, request, &collection)
		case derivedRetentionResolver:
			err = s.collectDerivedResolver(ctx, request, &collection)
		case derivedRetentionCaller:
			err = s.collectDerivedCaller(ctx, request, &collection)
		default:
			err = errors.New("unknown derived retention component")
		}
		if err != nil {
			if ctx.Err() != nil {
				return DerivedRetentionCollection{}, ctx.Err()
			}
			collection.recordDerivedRetentionError(request.Component, err)
		}
	}
	return collection, nil
}

func (collection *DerivedRetentionCollection) recordDerivedRetentionError(
	component RetentionComponent,
	err error,
) {
	if component == RetentionCallerArtifacts {
		collection.CallerErr = err
		return
	}
	collection.Results = append(collection.Results, RetentionComponentResult{
		Component: component,
		Err:       err,
	})
}

func (s *Surreal) derivedRetentionCatalogTables(
	ctx context.Context,
) (map[string]struct{}, error) {
	results, err := surrealdb.Query[[]string](ctx, s.db, derivedRetentionCatalogSQL, nil)
	if err != nil {
		return nil, fmt.Errorf("inspect derived retention database catalog: %w", err)
	}
	if err := retentionQueryResultsError(results); err != nil {
		return nil, fmt.Errorf("inspect derived retention database catalog: %w", err)
	}

	names := (*results)[0].Result
	if len(names) > len(derivedRetentionPlans) {
		return nil, fmt.Errorf(
			"inspect derived retention database catalog: %d tables exceed fixed projection of %d",
			len(names), len(derivedRetentionPlans),
		)
	}
	allowed := make(map[string]struct{}, len(derivedRetentionPlans))
	for _, plan := range derivedRetentionPlans {
		allowed[plan.table] = struct{}{}
	}
	tables := make(map[string]struct{}, len(names))
	for _, name := range names {
		if _, ok := allowed[name]; !ok {
			return nil, fmt.Errorf(
				"inspect derived retention database catalog: unexpected table %q",
				name,
			)
		}
		if _, duplicate := tables[name]; duplicate {
			return nil, fmt.Errorf(
				"inspect derived retention database catalog: duplicate table %q",
				name,
			)
		}
		tables[name] = struct{}{}
	}
	return tables, nil
}

func (s *Surreal) derivedRetentionReadiness(
	ctx context.Context,
	kind derivedRetentionKind,
) (bool, error) {
	var rid models.RecordID
	var version string
	switch kind {
	case derivedRetentionCandidate:
		rid = candidateControlRevisionMigrationID()
		version = candidateControlRevisionMigrationVersion
	case derivedRetentionResolver:
		rid = resolverCatalogMigrationID()
		version = resolverCatalogWriterMigrationVersion
	case derivedRetentionCaller:
		rid = callerGenerationPublicationMigrationID()
		version = callerGenerationPublicationMigrationVersion
	default:
		return false, errors.New("unknown derived retention readiness fence")
	}
	rows, err := surrealdb.Query[[]retentionMarkerState](ctx, s.db,
		"SELECT version FROM $rid WHERE version = $version LIMIT 1",
		map[string]any{"rid": rid, "version": version})
	if err != nil {
		return false, fmt.Errorf("derived retention readiness %d: %w", kind, err)
	}
	if err := retentionQueryResultsError(rows); err != nil {
		return false, fmt.Errorf("derived retention readiness %d: %w", kind, err)
	}
	return len((*rows)[0].Result) == 1 &&
		(*rows)[0].Result[0].Version == version, nil
}

func derivedRetentionSummary(
	request RetentionComponentRequest,
	scanned int,
) (RetentionComponentSummary, int, error) {
	if scanned > request.ScanIdentities {
		return RetentionComponentSummary{}, 0, fmt.Errorf(
			"derived retention component %q returned %d rows above limit %d",
			request.Component, scanned, request.ScanIdentities,
		)
	}
	selected := scanned
	summary := RetentionComponentSummary{ScannedIdentities: scanned}
	if scanned == request.ScanIdentities {
		selected = request.ReportedIdentities
		summary.Truncated = true
	}
	summary.ReportedIdentities = int64(selected)
	return summary, selected, nil
}

func (s *Surreal) collectDerivedCandidate(
	ctx context.Context,
	request RetentionComponentRequest,
	collection *DerivedRetentionCollection,
) error {
	results, err := surrealdb.Query[[]candidateManifestPublicationRec](
		ctx, s.db, derivedRetentionCandidateSQL,
		map[string]any{"scan_limit": request.ScanIdentities},
	)
	if err != nil {
		return fmt.Errorf("derived retention candidate authorities: %w", err)
	}
	if err := retentionQueryResultsError(results); err != nil {
		return fmt.Errorf("derived retention candidate authorities: %w", err)
	}
	rows := (*results)[0].Result
	for index, row := range rows {
		if err := validateCandidateManifestPublication(
			row.CandidateManifestPublication, true,
		); err != nil || !validDerivedRetentionRecordID(
			row.RecID, "candidate_manifest_publication", row.Repository,
		) {
			return fmt.Errorf(
				"derived retention candidate authority row %d is invalid", index,
			)
		}
	}
	summary, selected, err := derivedRetentionSummary(request, len(rows))
	if err != nil {
		return err
	}
	collection.CandidateAuthorities = make(
		[]CandidateManifestPublication, selected,
	)
	for index := range selected {
		publication := rows[index].CandidateManifestPublication
		publication.PublishedAt = publication.PublishedAt.UTC()
		collection.CandidateAuthorities[index] = publication
	}
	collection.Results = append(collection.Results, RetentionComponentResult{
		Component: request.Component,
		Summary:   summary,
	})
	return nil
}

func (s *Surreal) collectDerivedFocused(
	ctx context.Context,
	request RetentionComponentRequest,
	collection *DerivedRetentionCollection,
) error {
	results, err := surrealdb.Query[[]derivedRetentionFocusedRow](
		ctx, s.db, derivedRetentionFocusedSQL,
		map[string]any{"scan_limit": request.ScanIdentities},
	)
	if err != nil {
		return fmt.Errorf("derived retention focused authorities: %w", err)
	}
	if err := retentionQueryResultsError(results); err != nil {
		return fmt.Errorf("derived retention focused authorities: %w", err)
	}
	rows := (*results)[0].Result
	if len(rows) > request.ScanIdentities {
		return fmt.Errorf(
			"derived retention focused authorities returned %d rows above limit %d",
			len(rows), request.ScanIdentities,
		)
	}
	collection.FocusedPartial = len(rows) == request.ScanIdentities
	authorities := make(
		[]RetentionFocusedRepositoryAuthority, 0,
		min(len(rows), request.ReportedIdentities),
	)
	for index := range rows {
		row := &rows[index]
		if row.IndexedAnalysisUnit == nil {
			continue
		}
		if err := validateDerivedRetentionFocusedRow(row); err != nil {
			return fmt.Errorf(
				"derived retention focused authority row %d: %w", index, err,
			)
		}
		if len(authorities) < request.ReportedIdentities {
			authorities = append(authorities, RetentionFocusedRepositoryAuthority{
				Repository:          row.Name,
				IndexedCommitHash:   row.IndexedCommitHash,
				IndexedAnalysisUnit: analysisunit.CloneState(row.IndexedAnalysisUnit),
			})
		}
	}
	if collection.FocusedPartial && len(authorities) == 0 {
		return fmt.Errorf(
			"%w: bounded repository prefix contains no focused authority",
			ErrRetentionComponentUnavailable,
		)
	}
	collection.FocusedAuthorities = authorities
	collection.Results = append(collection.Results, RetentionComponentResult{
		Component: request.Component,
		Summary: RetentionComponentSummary{
			ScannedIdentities:  len(authorities),
			ReportedIdentities: int64(len(authorities)),
		},
	})
	return nil
}

func (s *Surreal) collectDerivedResolver(
	ctx context.Context,
	request RetentionComponentRequest,
	collection *DerivedRetentionCollection,
) error {
	results, err := surrealdb.Query[[]resolverCatalogPublicationRec](
		ctx, s.db, derivedRetentionResolverSQL,
		map[string]any{"scan_limit": request.ScanIdentities},
	)
	if err != nil {
		return fmt.Errorf("derived retention resolver authorities: %w", err)
	}
	if err := retentionQueryResultsError(results); err != nil {
		return fmt.Errorf("derived retention resolver authorities: %w", err)
	}
	rows := (*results)[0].Result
	for index, row := range rows {
		if err := validateResolverCatalogPublication(
			row.ResolverCatalogPublication, true,
		); err != nil || !validDerivedRetentionRecordID(
			row.RecID, "resolver_catalog_publication", row.Repository,
		) {
			return fmt.Errorf(
				"derived retention resolver authority row %d is invalid", index,
			)
		}
	}
	summary, selected, err := derivedRetentionSummary(request, len(rows))
	if err != nil {
		return err
	}
	collection.ResolverAuthorities = make([]ResolverCatalogPublication, selected)
	for index := range selected {
		publication := rows[index].ResolverCatalogPublication
		publication.Declarations = slices.Clone(publication.Declarations)
		publication.ResolverPacks = slices.Clone(publication.ResolverPacks)
		publication.PublishedAt = publication.PublishedAt.UTC()
		collection.ResolverAuthorities[index] = publication
	}
	collection.Results = append(collection.Results, RetentionComponentResult{
		Component: request.Component,
		Summary:   summary,
	})
	return nil
}

func (s *Surreal) collectDerivedCaller(
	ctx context.Context,
	request RetentionComponentRequest,
	collection *DerivedRetentionCollection,
) error {
	results, err := surrealdb.Query[[]callerGenerationPublicationSummaryRec](
		ctx, s.db, derivedRetentionCallerSQL,
		map[string]any{"scan_limit": request.ScanIdentities},
	)
	if err != nil {
		return fmt.Errorf("derived retention caller authorities: %w", err)
	}
	if err := retentionQueryResultsError(results); err != nil {
		return fmt.Errorf("derived retention caller authorities: %w", err)
	}
	rows := (*results)[0].Result
	validated := make([]CallerGenerationPublicationSummary, len(rows))
	for index := range rows {
		validated[index], err = validatePersistedCallerGenerationPublicationSummary(rows[index])
		if err != nil {
			return fmt.Errorf(
				"derived retention caller authority row %d: %w", index, err,
			)
		}
	}
	summary, selected, err := derivedRetentionSummary(request, len(rows))
	if err != nil {
		return err
	}
	current, err := s.derivedRetentionCallerAuthoritiesCurrent(
		ctx, validated[:selected],
	)
	if err != nil {
		return err
	}
	if !current {
		return fmt.Errorf(
			"%w: one or more caller generation authorities are not current",
			ErrRetentionComponentUnavailable,
		)
	}
	collection.CallerScannedIdentities = summary.ScannedIdentities
	collection.CallerTruncated = summary.Truncated
	collection.CallerAuthorities = slices.Clone(validated[:selected])
	return nil
}

func (s *Surreal) derivedRetentionCallerAuthoritiesCurrent(
	ctx context.Context,
	summaries []CallerGenerationPublicationSummary,
) (bool, error) {
	if len(summaries) == 0 {
		return true, nil
	}
	bindings := make(
		[]callerGenerationSummaryAuthorityBinding, len(summaries),
	)
	for index, summary := range summaries {
		bindings[index] = callerGenerationSummaryAuthorityBinding{
			RepositoryRID:  repoID(summary.Generation.Repository),
			CandidateRID:   candidateManifestPublicationID(summary.Generation.Repository),
			ResolverRID:    resolverCatalogPublicationID(summary.Generation.Repository),
			PublicationRID: callerGenerationPublicationID(summary.Generation.Repository),
			Summary:        summary,
		}
	}
	results, err := surrealdb.Query[[]callerGenerationCurrentResult](
		ctx, s.db, derivedRetentionCallerAuthoritiesCurrentSQL,
		map[string]any{
			"migration_rid":     callerGenerationPublicationMigrationID(),
			"migration_version": callerGenerationPublicationMigrationVersion,
			"bindings":          bindings,
			"writer_schema":     CallerGenerationPublicationWriterSchema,
		},
	)
	if err != nil {
		return false, fmt.Errorf(
			"derived retention caller authority fence: %w", err,
		)
	}
	if err := retentionQueryResultsError(results); err != nil {
		return false, fmt.Errorf(
			"derived retention caller authority fence: %w", err,
		)
	}
	rows := (*results)[0].Result
	if len(rows) != 1 {
		return false, errors.New(
			"derived retention caller authority fence returned no result",
		)
	}
	return rows[0].Current, nil
}

func validDerivedRetentionRecordID(
	rid *models.RecordID,
	table, identifier string,
) bool {
	if rid == nil || rid.Table != table {
		return false
	}
	value, ok := rid.ID.(string)
	return ok && value == identifier
}

func validateDerivedRetentionFocusedRow(row *derivedRetentionFocusedRow) error {
	if row == nil {
		return errors.New("row is nil")
	}
	if err := reponame.Validate(row.Name); err != nil ||
		!validDerivedRetentionRecordID(row.RecID, "repo", row.Name) {
		return errors.New("repository identity is invalid")
	}
	if !validGitObjectID(row.IndexedCommitHash) {
		return errors.New("indexed_commit_hash is invalid")
	}
	if row.IndexedAnalysisUnit == nil {
		return errors.New("indexed_analysis_unit is absent")
	}
	if err := row.IndexedAnalysisUnit.Validate(row.Name); err != nil {
		return fmt.Errorf("indexed_analysis_unit: %w", err)
	}
	return nil
}
