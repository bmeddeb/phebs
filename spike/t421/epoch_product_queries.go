package t421

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"strconv"
)

// ExecutionProductQuery retains only completed, actually decoded transports.
// It is a private execution prefix, not signed QueryTransportResult evidence:
// authorization-decision and snapshot counts are not invented from page counts.
type ExecutionProductQuery struct {
	Name, Transport, Code, ProjectionSHA256           string
	Pages, Records, Paths, FirstOrdinal, LastOrdinal  uint64
	ControlFileReads, StoreReadAttempts, MemberVisits uint64
	VisibleRepositories                               uint64
	VisibleRepositoriesObserved                       bool
}

// An optional report field is admitted only for the two fixed all-code calls.
// Do not infer this enumeration result from the one returned search hit.
func validEpochQueryRepositories(report epochInspectionReport, required bool) bool {
	if !required {
		return report.VisibleRepositories == nil
	}
	return report.VisibleRepositories != nil && *report.VisibleRepositories == 1
}

// QueryRestored runs the closed HTTP-then-MCP corridor between two actual F
// fences. The selected stateless MCP server needs no initialization requests.
// No query reads the catalog file, reopens the store, or retries an ordinal.
func (run *ExecutionEpochOneRun) QueryRestored(ctx context.Context) (retErr error) {
	op, cancel, done, err := run.beginRestoredExecution(ctx, 14)
	if err != nil {
		return err
	}
	defer func() { run.finishRestoredExecution(cancel, done, retErr) }()
	reader := run.inspection
	if err := run.startRestoredPhaseDeadline(op, 14); err != nil {
		return err
	}
	run.mu.Lock()
	deadline := run.phaseDeadline
	run.mu.Unlock()
	op, phaseCancel := context.WithDeadline(op, deadline)
	defer phaseCancel()
	if run.advanceReturnPhase(op, 14) != nil || reader.beginProductInspection() != nil || run.control.OpenRequests(op) != nil {
		return ErrExecutionEpochOne
	}
	if _, err := reader.restoredSample(op, "start"); err != nil {
		return err
	}
	progress, _, err := reader.Progress(op)
	if err != nil || progress.Progress == nil || progress.Progress.State != "current" {
		return errEpochInspection
	}
	tail, _, err := reader.Tail(op)
	if err != nil || tail.Status != "ready" {
		return errEpochInspection
	}
	authority, projection, _, err := reader.Final(op)
	if err != nil {
		return err
	}
	queries, err := run.productQueryContext(op, authority, projection)
	if err != nil {
		return err
	}
	for _, transport := range []string{"http", "mcp"} {
		for _, query := range correctedQueryCases() {
			if err := reader.productQuery(op, queries, query, transport); err != nil {
				return err
			}
		}
	}
	reader.mu.Lock()
	valid := reader.err == nil && validExecutionProductQueries(reader.productQueries)
	reader.productQueriesComplete = valid
	reader.mu.Unlock()
	if !valid {
		return errEpochInspection
	}
	if _, _, _, err := reader.Final(op); err != nil {
		return err
	}
	if _, err := reader.restoredSample(op, "finish"); err != nil {
		return err
	}
	if err := run.acceptRestoredExecution(op, 14); err != nil {
		return err
	}
	return run.recordProductQueryEvidence(op)
}

func (run *ExecutionEpochOneRun) productQueryContext(ctx context.Context, authority AuthorityPhaseResult, projection PhaseStateProjection) (*epochQueryProjectionContext, error) {
	if run.flow.epochs == nil || run.flow.epochs.author == nil || authority.Phase != "product_queries" || projection.Phase != authority.Phase {
		return nil, ErrExecutionEpochOne
	}
	// Reconstitute only the already-validated actual F fields. Check its exact
	// native bytes before deriving catalog identities; never substitute the plan.
	value := epochFinalResponse{Schema: "t421-final-authority-source-free-v1", ExtractionRoots: authority.ExtractionRoots}
	raw, err := json.Marshal(authority.AuthorityState)
	if err != nil || json.Unmarshal(raw, &value.Authority) != nil {
		return nil, ErrExecutionEpochOne
	}
	raw, err = json.Marshal(projection)
	if err != nil || json.Unmarshal(raw, &value.Projection) != nil {
		return nil, ErrExecutionEpochOne
	}
	value.Projection.Schema = "t421-final-state-projection-source-free-v1"
	reader := run.inspection
	reader.mu.Lock()
	queryAuthority := reader.productQueryAuthority
	value.QueryAuthority = &queryAuthority
	raw, err = json.MarshalIndent(value, "", "  ")
	raw = append(raw, '\n')
	valid := err == nil && reader.productBaseline != nil && sha256.Sum256(raw) == *reader.productBaseline
	reader.mu.Unlock()
	if !valid {
		return nil, ErrExecutionEpochOne
	}
	epochs, author := run.flow.epochs, run.flow.epochs.author
	author.mu.Lock()
	epochs.mu.Lock()
	valid = !epochs.closed && epochs.err == nil && epochs.active && epochs.released == 5 && author.borrowedBy == run &&
		epochs.queryCatalog != nil && epochs.epochs[4].Repository == run.epoch.Repository && epochs.epochs[4].CatalogSHA256 == run.epoch.CatalogSHA256
	catalog := epochs.queryCatalog // Private immutable preparation value, never returned to callers.
	epochs.mu.Unlock()
	author.mu.Unlock()
	if !valid {
		return nil, ErrExecutionEpochOne
	}
	return newEpochQueryProjectionContext(ctx, run.epoch.Repository, value, *catalog)
}

func (reader *executionEpochInspection) productQuery(ctx context.Context, queries *epochQueryProjectionContext, query QueryCase, transport string) (retErr error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	defer func() { reader.fail(retErr) }()
	cases := correctedQueryCases()
	index := len(reader.productQueries)
	wantTransport := "http"
	if index >= len(cases) {
		wantTransport = "mcp"
	}
	if reader.err != nil || index >= 2*len(cases) || !epochQueryKnown(query) || query.Name != cases[index%len(cases)].Name || transport != wantTransport {
		return errEpochInspection
	}
	projection, err := queries.queryProjection(query)
	maximum, maximumErr := epochProductRemainingReads(reader.productQueries)
	controls, stores, readsErr := correctedProductQueryControlReads(query)
	if transport == "http" && query.Name == "first_service" {
		stores += 4 // The first accepted catalog route's existing cold miss.
	}
	if err != nil || maximumErr != nil || readsErr != nil {
		return errEpochInspection
	}
	result := ExecutionProductQuery{Name: query.Name, Transport: transport, FirstOrdinal: reader.next}
	cursor := ""
	for page := uint64(0); page < correctedProductQueryPages(query); page++ {
		id := strconv.FormatUint(reader.next, 10)
		_, path, payload, err := epochQueryRequest(query, transport, reader.run.epoch.Repository, cursor, id)
		limit, limitErr := epochQueryBodyMaximum(query, transport, reader.run.epoch.Listen, id)
		if err != nil || limitErr != nil {
			return errEpochInspection
		}
		raw, status, contentType, report, err := reader.readQueryRequest(ctx, path, payload, limit, maximum, query.Name == "all_code_structural_marker")
		if err != nil {
			return err
		}
		if report.VisibleRepositories != nil {
			result.VisibleRepositories, result.VisibleRepositoriesObserved = *report.VisibleRepositories, true
		}
		if transport == "http" {
			result.Code = strconv.Itoa(status)
			cursor, err = projection.addHTTP(status, raw)
		} else {
			var structured []byte
			if status != http.StatusOK {
				return errEpochInspection
			}
			structured, result.Code, err = decodeEpochQueryMCP(query, contentType, id, raw)
			if err == nil {
				cursor, err = projection.addMCP(result.Code, structured)
			}
		}
		if err != nil || (cursor == "") != (page+1 == correctedProductQueryPages(query)) {
			return errEpochInspection
		}
		// readQueryRequest admitted each report under this remaining bound;
		// subtraction makes every sum overflow-safe, including prior transports.
		maximum.ControlFileReads -= report.ControlFileReads
		maximum.StoreReadAttempts -= report.StoreReadAttempts
		maximum.MemberVisits -= report.MemberVisits
		result.ControlFileReads += report.ControlFileReads
		result.StoreReadAttempts += report.StoreReadAttempts
		result.MemberVisits += report.MemberVisits
		result.LastOrdinal = report.RequestOrdinal
	}
	actual, err := projection.finish()
	transportIndex := 0
	if transport == "mcp" {
		transportIndex = 1
	}
	if err != nil || actual.SHA256 != query.ProjectionSHA256 || actual.Pages != correctedProductQueryPages(query) ||
		actual.Records != query.ExpectedRecords || actual.Paths != query.ExpectedPaths ||
		result.ControlFileReads != controls || result.StoreReadAttempts != stores ||
		!validQueryMemberReads(PlanV3Schema, query, transportIndex, result.MemberVisits, result.MemberVisits) {
		return errEpochInspection
	}
	result.ProjectionSHA256, result.Pages, result.Records, result.Paths = actual.SHA256, actual.Pages, actual.Records, actual.Paths
	reader.productQueries = append(reader.productQueries, result)
	return nil
}

func epochProductRemainingReads(rows []ExecutionProductQuery) (epochInspectionReport, error) {
	controls, stores, err := correctedProductQueryNativeControlReads()
	members, memberErr := correctedProductQueryMemberReadMaximum(correctedQueryCases())
	if err != nil || memberErr != nil {
		return epochInspectionReport{}, errEpochInspection
	}
	for _, row := range rows {
		if row.ControlFileReads > controls || row.StoreReadAttempts > stores || row.MemberVisits > members {
			return epochInspectionReport{}, errEpochInspection
		}
		controls -= row.ControlFileReads
		stores -= row.StoreReadAttempts
		members -= row.MemberVisits
	}
	return epochInspectionReport{ControlFileReads: controls, StoreReadAttempts: stores, MemberVisits: members}, nil
}

func validExecutionProductQueries(rows []ExecutionProductQuery) bool {
	cases := correctedQueryCases()
	if len(rows) != 2*len(cases) {
		return false
	}
	for index, row := range rows {
		query := cases[index%len(cases)]
		transport, code := "http", strconv.Itoa(int(query.ExpectedStatus))
		if index >= len(cases) {
			transport, code = "mcp", query.ExpectedMCPCode
		}
		pages := correctedProductQueryPages(query)
		controls, stores, readsErr := correctedProductQueryControlReads(query)
		if transport == "http" && query.Name == "first_service" {
			stores += 4
		}
		if row.Name != query.Name || row.Transport != transport || row.Code != code || row.Pages != pages || row.Records != query.ExpectedRecords ||
			row.VisibleRepositoriesObserved != (query.Name == "all_code_structural_marker") || query.Name == "all_code_structural_marker" && row.VisibleRepositories != 1 || query.Name != "all_code_structural_marker" && row.VisibleRepositories != 0 ||
			readsErr != nil || row.ControlFileReads != controls || row.StoreReadAttempts != stores ||
			row.Paths != query.ExpectedPaths || row.ProjectionSHA256 != query.ProjectionSHA256 || row.FirstOrdinal == 0 || row.LastOrdinal < row.FirstOrdinal ||
			row.LastOrdinal-row.FirstOrdinal != pages-1 || index > 0 && (rows[index-1].LastOrdinal == ^uint64(0) || row.FirstOrdinal != rows[index-1].LastOrdinal+1) ||
			!validQueryMemberReads(PlanV3Schema, query, index/len(cases), row.MemberVisits, row.MemberVisits) {
			return false
		}
	}
	remaining, err := epochProductRemainingReads(rows)
	return err == nil && remaining.ControlFileReads == 0 && remaining.StoreReadAttempts == 0
}
