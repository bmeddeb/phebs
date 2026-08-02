package store

import (
	"context"
	"errors"
	"fmt"
	"math"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

// RetentionComponent identifies one T30.6p database-backed component. The
// values deliberately match the fixed API registry, but the store owns the
// query allowlist so a caller cannot select an arbitrary SurrealDB table or
// expression.
type RetentionComponent string

const (
	RetentionExtractionRun            RetentionComponent = "extraction_run"
	RetentionSnapshotEvidence         RetentionComponent = "snapshot_evidence"
	RetentionAssertion                RetentionComponent = "assertion"
	RetentionEvidenceAtom             RetentionComponent = "evidence_atom"
	RetentionExtractionAttempt        RetentionComponent = "extraction_attempt"
	RetentionExtractionOutcome        RetentionComponent = "extraction_domain_outcome"
	RetentionProofBundlePin           RetentionComponent = "evidence_pin[kind=proof-bundle:<bundle_id>]"
	RetentionInvestigationArtifactPin RetentionComponent = "evidence_pin[kind=investigation-artifact:<artifact_id>]"
	RetentionOtherEvidencePin         RetentionComponent = "evidence_pin[kind=<other exact store-accepted value>]"
	RetentionProofBundle              RetentionComponent = "proof_bundle"
	RetentionCallerPublication        RetentionComponent = "caller_generation_publication"
	RetentionCallerAdmission          RetentionComponent = "caller_generation_admission"
	RetentionCallerLeafOutcome        RetentionComponent = "caller_leaf_outcome"
)

// RetentionComponentRequest gives one component its non-transferable share of
// the endpoint-wide allocation. ScanIdentities must be exactly one larger than
// ReportedIdentities so a collector can distinguish exact-at-cap from a lower
// bound without reading another row.
type RetentionComponentRequest struct {
	Component          RetentionComponent
	ReportedIdentities int
	ScanIdentities     int
}

// RetentionComponentSummary is a bounded logical-row summary. Bytes is nil
// when the component has no store-attributable logical/canonical byte metric
// or a selected row cannot provide that metric. Physical SurrealDB allocation
// is never inferred from logical rows.
type RetentionComponentSummary struct {
	ScannedIdentities  int
	ReportedIdentities int64
	Bytes              *int64
	Truncated          bool
}

// RetentionComponentResult localizes one component failure. The API leaves a
// failed component explicitly unavailable while still returning independent
// successful summaries.
type RetentionComponentResult struct {
	Component RetentionComponent
	Summary   RetentionComponentSummary
	Err       error
}

// CoreRetentionStore is the T30.6p bounded database-inventory boundary.
type CoreRetentionStore interface {
	CollectCoreRetention(context.Context, []RetentionComponentRequest) ([]RetentionComponentResult, error)
}

var _ CoreRetentionStore = (*Surreal)(nil)

var ErrRetentionComponentUnavailable = errors.New("retention component unavailable")

type retentionReadiness int

const (
	retentionEvidenceReady retentionReadiness = iota
	retentionEvidencePinReady
	retentionJobsReady
	retentionCallerLeafReady
	retentionCallerPublicationReady
)

type retentionPinPartition int

const (
	retentionNoPinPartition retentionPinPartition = iota
	retentionProofPins
	retentionInvestigationPins
	retentionOtherPins
)

type retentionQueryPlan struct {
	table          string
	byteExpression string
	readiness      retentionReadiness
	pins           retentionPinPartition
}

const retentionNonnegativeScalarBytesSQL = `
IF type::is_int(canonical_bytes) THEN
	IF canonical_bytes >= 0 THEN canonical_bytes ELSE NONE END
ELSE NONE END`

const retentionCallerLeafBytesSQL = `
IF receipt = NONE THEN
	IF disposition = 'terminal_generation_refusal' THEN 0 ELSE NONE END
ELSE IF type::is_int(receipt.canonical_bytes) THEN
	IF receipt.canonical_bytes >= 0 THEN receipt.canonical_bytes ELSE NONE END
ELSE NONE END`

var coreRetentionPlans = map[RetentionComponent]retentionQueryPlan{
	RetentionExtractionRun: {
		table: "extraction_run", readiness: retentionEvidenceReady,
	},
	RetentionSnapshotEvidence:  {table: "snapshot_evidence", readiness: retentionEvidenceReady},
	RetentionAssertion:         {table: "assertion", readiness: retentionEvidenceReady},
	RetentionEvidenceAtom:      {table: "evidence_atom", readiness: retentionEvidenceReady},
	RetentionExtractionAttempt: {table: "extraction_attempt", readiness: retentionEvidenceReady},
	RetentionExtractionOutcome: {
		table: "extraction_domain_outcome", readiness: retentionEvidenceReady,
		byteExpression: "IF type::is_string(receipt) THEN bytes::len(<bytes>receipt) ELSE NONE END",
	},
	RetentionProofBundlePin: {
		table: "evidence_pin", readiness: retentionEvidencePinReady, pins: retentionProofPins,
	},
	RetentionInvestigationArtifactPin: {
		table: "evidence_pin", readiness: retentionEvidencePinReady, pins: retentionInvestigationPins,
	},
	RetentionOtherEvidencePin: {
		table: "evidence_pin", readiness: retentionEvidencePinReady, pins: retentionOtherPins,
	},
	RetentionProofBundle: {
		table: "proof_bundle", readiness: retentionEvidenceReady,
		byteExpression: "IF type::is_string(content) THEN bytes::len(<bytes>content) ELSE NONE END",
	},
	RetentionComponent(JobSync):            {table: string(JobSync), readiness: retentionJobsReady},
	RetentionComponent(JobIndex):           {table: string(JobIndex), readiness: retentionJobsReady},
	RetentionComponent(JobFetch):           {table: string(JobFetch), readiness: retentionJobsReady},
	RetentionComponent(JobCandidate):       {table: string(JobCandidate), readiness: retentionJobsReady},
	RetentionComponent(JobExtract):         {table: string(JobExtract), readiness: retentionJobsReady},
	RetentionComponent(JobResolverCatalog): {table: string(JobResolverCatalog), readiness: retentionJobsReady},
	RetentionComponent(JobCallerLeaf):      {table: string(JobCallerLeaf), readiness: retentionJobsReady},
	RetentionComponent(JobInvestigate):     {table: string(JobInvestigate), readiness: retentionJobsReady},
	RetentionCallerPublication: {
		table: "caller_generation_publication", readiness: retentionCallerPublicationReady,
		byteExpression: retentionNonnegativeScalarBytesSQL,
	},
	RetentionCallerAdmission: {
		table: "caller_generation_admission", readiness: retentionCallerLeafReady,
		byteExpression: retentionNonnegativeScalarBytesSQL,
	},
	RetentionCallerLeafOutcome: {
		table: "caller_leaf_outcome", readiness: retentionCallerLeafReady,
		byteExpression: retentionCallerLeafBytesSQL,
	},
}

const (
	maxCoreRetentionReportedPerComponent = 79
	maxCoreRetentionRequests             = 21
	maxCoreRetentionReportedIdentities   = 1_656
	maxCoreRetentionScanIdentities       = 1_677
)

type retentionRow struct {
	ID    *models.RecordID `json:"id"`
	Bytes *int64           `json:"metric_bytes"`
}

type retentionMarkerState struct {
	Version string `json:"version"`
}

type retentionReadyResult struct {
	ready bool
	err   error
}

func (s *Surreal) CollectCoreRetention(
	ctx context.Context,
	requests []RetentionComponentRequest,
) ([]RetentionComponentResult, error) {
	if len(requests) > maxCoreRetentionRequests {
		return nil, fmt.Errorf("collect core retention: %d requests exceed %d", len(requests), maxCoreRetentionRequests)
	}
	seen := make(map[RetentionComponent]struct{}, len(requests))
	reportedTotal, scanTotal := 0, 0
	for _, request := range requests {
		_, ok := coreRetentionPlans[request.Component]
		if !ok {
			return nil, fmt.Errorf("collect core retention: unsupported component %q", request.Component)
		}
		if _, duplicate := seen[request.Component]; duplicate {
			return nil, fmt.Errorf("collect core retention: duplicate component %q", request.Component)
		}
		seen[request.Component] = struct{}{}
		if request.ReportedIdentities <= 0 ||
			request.ReportedIdentities > maxCoreRetentionReportedPerComponent ||
			request.ScanIdentities != request.ReportedIdentities+1 {
			return nil, fmt.Errorf("collect core retention: invalid allocation for %q", request.Component)
		}
		reportedTotal += request.ReportedIdentities
		scanTotal += request.ScanIdentities
	}
	if reportedTotal > maxCoreRetentionReportedIdentities ||
		scanTotal > maxCoreRetentionScanIdentities {
		return nil, fmt.Errorf("collect core retention: aggregate allocation %d/%d exceeds %d/%d", reportedTotal, scanTotal, maxCoreRetentionReportedIdentities, maxCoreRetentionScanIdentities)
	}

	readiness := make(map[retentionReadiness]retentionReadyResult, 5)
	results := make([]RetentionComponentResult, 0, len(requests))
	for _, request := range requests {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		plan := coreRetentionPlans[request.Component]
		ready, exists := readiness[plan.readiness]
		if !exists {
			if plan.readiness == retentionEvidencePinReady {
				evidence, evidenceExists := readiness[retentionEvidenceReady]
				if !evidenceExists {
					evidence.ready, evidence.err = s.retentionReadiness(ctx, retentionEvidenceReady)
					readiness[retentionEvidenceReady] = evidence
				}
				switch {
				case evidence.err != nil || !evidence.ready:
					ready = evidence
				default:
					ready.ready = true
					ready.err = s.requireRetentionIndex(ctx, "evidence_pin_kind", "evidence_pin")
				}
			} else {
				ready.ready, ready.err = s.retentionReadiness(ctx, plan.readiness)
			}
			readiness[plan.readiness] = ready
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		result := RetentionComponentResult{Component: request.Component}
		if ready.err != nil {
			result.Err = ready.err
			results = append(results, result)
			continue
		}
		if !ready.ready {
			result.Err = ErrRetentionComponentUnavailable
			results = append(results, result)
			continue
		}
		rows, err := s.retentionRows(ctx, plan, request.ScanIdentities)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			result.Err = err
			results = append(results, result)
			continue
		}
		result.Summary = summarizeRetentionRows(plan, request, rows)
		results = append(results, result)
	}
	return results, nil
}

func (s *Surreal) retentionReadiness(
	ctx context.Context,
	readiness retentionReadiness,
) (bool, error) {
	var rid models.RecordID
	var version string
	switch readiness {
	case retentionEvidenceReady:
		rid, version = evidenceMigrationStateID(), evidenceMigrationVersion
	case retentionJobsReady:
		rid, version = jobActiveMigrationID(), jobActiveMigrationVersion
	case retentionCallerLeafReady:
		rid, version = callerLeafMigrationID(), callerLeafWriterMigrationVersion
	case retentionCallerPublicationReady:
		rid, version = callerGenerationPublicationMigrationID(), callerGenerationPublicationMigrationVersion
	default:
		return false, errors.New("unknown retention readiness fence")
	}
	rows, err := surrealdb.Query[[]retentionMarkerState](ctx, s.db,
		"SELECT version FROM $rid WHERE version = $version LIMIT 1",
		map[string]any{"rid": rid, "version": version})
	if err != nil {
		return false, fmt.Errorf("retention readiness %d: %w", readiness, err)
	}
	if err := retentionQueryResultsError(rows); err != nil {
		return false, fmt.Errorf("retention readiness %d: %w", readiness, err)
	}
	for _, result := range *rows {
		if len(result.Result) == 1 && result.Result[0].Version == version {
			return true, nil
		}
	}
	return false, nil
}

func (s *Surreal) requireRetentionIndex(
	ctx context.Context,
	index, table string,
) error {
	// Both identifiers come exclusively from the closed query-plan registry.
	results, err := surrealdb.Query[any](ctx, s.db,
		"INFO FOR INDEX "+index+" ON TABLE "+table, nil)
	if err != nil {
		return fmt.Errorf("inspect retention index %s: %w", index, err)
	}
	if err := retentionQueryResultsError(results); err != nil {
		return fmt.Errorf("inspect retention index %s: %w", index, err)
	}
	return nil
}

func (s *Surreal) retentionRows(
	ctx context.Context,
	plan retentionQueryPlan,
	scanLimit int,
) ([]retentionRow, error) {
	if plan.pins != retentionNoPinPartition {
		return s.retentionPinRows(ctx, plan.pins, scanLimit)
	}
	fields := "id"
	if plan.byteExpression != "" {
		fields += ", " + plan.byteExpression + " AS metric_bytes"
	}
	statement := "SELECT " + fields +
		" FROM type::table($table) ORDER BY id LIMIT $scan_limit"
	queryResults, err := surrealdb.Query[[]retentionRow](ctx, s.db, statement,
		map[string]any{"table": plan.table, "scan_limit": scanLimit})
	if err != nil {
		return nil, fmt.Errorf("retention component %s: %w", plan.table, err)
	}
	if err := retentionQueryResultsError(queryResults); err != nil {
		return nil, fmt.Errorf("retention component %s: %w", plan.table, err)
	}
	var rows []retentionRow
	for _, result := range *queryResults {
		rows = append(rows, result.Result...)
	}
	if len(rows) > scanLimit {
		return nil, fmt.Errorf("retention component %s returned %d rows above limit %d", plan.table, len(rows), scanLimit)
	}
	return rows, nil
}

func (s *Surreal) retentionPinRows(
	ctx context.Context,
	partition retentionPinPartition,
	scanLimit int,
) ([]retentionRow, error) {
	const (
		investigationStart = "investigation-artifact:"
		investigationEnd   = "investigation-artifact;"
		proofStart         = "proof-bundle:"
		proofEnd           = "proof-bundle;"
	)
	type kindRange struct {
		lower *string
		upper *string
	}
	value := func(value string) *string { return &value }
	var ranges []kindRange
	switch partition {
	case retentionProofPins:
		ranges = []kindRange{{lower: value(proofStart), upper: value(proofEnd)}}
	case retentionInvestigationPins:
		ranges = []kindRange{{lower: value(investigationStart), upper: value(investigationEnd)}}
	case retentionOtherPins:
		ranges = []kindRange{
			{upper: value(investigationStart)},
			{lower: value(investigationEnd), upper: value(proofStart)},
			{lower: value(proofEnd)},
		}
	default:
		return nil, errors.New("unknown retention pin partition")
	}

	rows := make([]retentionRow, 0, scanLimit)
	for _, scanRange := range ranges {
		remaining := scanLimit - len(rows)
		if remaining == 0 {
			break
		}
		where := ""
		vars := map[string]any{"scan_limit": remaining}
		switch {
		case scanRange.lower != nil && scanRange.upper != nil:
			where = "kind >= $lower AND kind < $upper"
			vars["lower"], vars["upper"] = *scanRange.lower, *scanRange.upper
		case scanRange.lower != nil:
			where = "kind >= $lower"
			vars["lower"] = *scanRange.lower
		case scanRange.upper != nil:
			where = "kind < $upper"
			vars["upper"] = *scanRange.upper
		default:
			return nil, errors.New("unbounded retention pin range")
		}
		queryResults, err := surrealdb.Query[[]retentionRow](ctx, s.db,
			"SELECT id FROM evidence_pin WITH INDEX evidence_pin_kind WHERE "+where+" LIMIT $scan_limit",
			vars)
		if err != nil {
			return nil, fmt.Errorf("retention evidence-pin partition %d: %w", partition, err)
		}
		if err := retentionQueryResultsError(queryResults); err != nil {
			return nil, fmt.Errorf("retention evidence-pin partition %d: %w", partition, err)
		}
		for _, result := range *queryResults {
			rows = append(rows, result.Result...)
		}
		if len(rows) > scanLimit {
			return nil, fmt.Errorf("retention evidence-pin partition %d exceeded scan limit", partition)
		}
	}
	return rows, nil
}

func summarizeRetentionRows(
	plan retentionQueryPlan,
	request RetentionComponentRequest,
	rows []retentionRow,
) RetentionComponentSummary {
	summary := RetentionComponentSummary{ScannedIdentities: len(rows)}
	reported := len(rows)
	if reported == request.ScanIdentities {
		reported = request.ReportedIdentities
		summary.Truncated = true
	}
	summary.ReportedIdentities = int64(reported)
	selected := rows[:reported]

	if plan.byteExpression != "" {
		var total int64
		for _, row := range selected {
			if row.Bytes == nil || *row.Bytes < 0 || *row.Bytes > math.MaxInt64-total {
				summary.Bytes = nil
				return summary
			}
			total += *row.Bytes
		}
		summary.Bytes = &total
	}
	return summary
}

func retentionQueryResultsError[T any](
	results *[]surrealdb.QueryResult[T],
) error {
	if results == nil {
		return errors.New("SurrealDB returned no query results")
	}
	if len(*results) != 1 {
		return fmt.Errorf("SurrealDB returned %d query result envelopes, want 1", len(*results))
	}
	for index, result := range *results {
		if result.Error != nil {
			return fmt.Errorf("statement %d: %s", index, result.Error.Message)
		}
	}
	return nil
}
