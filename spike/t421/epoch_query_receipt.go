package t421

import (
	"context"
	"crypto/sha256"
	"slices"
	"strings"
)

// Called only by QueryRestored after its real final sample, request fence and
// selector acceptance. It converts completed private observations, not rows
// supplied by an executor caller. No authority is created on a failed phase.
func (run *ExecutionEpochOneRun) recordProductQueryEvidence(ctx context.Context) error {
	if run == nil || ctx == nil || ctx.Err() != nil || run.flow == nil || run.inspection == nil || run.control == nil {
		return ErrExecutionEpochOne
	}
	reader := run.inspection
	reader.mu.Lock()
	defer reader.mu.Unlock()
	run.mu.Lock()
	defer run.mu.Unlock()
	if run.stopping || run.err != nil || run.epoch.Epoch != 5 || !run.healthy || !run.warm || !run.productExecutionUsed ||
		run.restoredExecutionDone == nil || reader.run != run || reader.err != nil || reader.productQueryEvidence != nil ||
		reader.plan.Schema != PlanV3Schema || run.flow.plan.Schema != PlanV3Schema || reader.plan.Correction == nil ||
		!strings.HasSuffix(reader.plan.Correction.ReadAccountingPolicy, ";"+queryResultUnitsV3) ||
		run.control.Context().Err() != nil || run.control.RequestToken() != "" {
		return ErrExecutionEpochOne
	}
	select {
	case <-run.stop:
		return ErrExecutionEpochOne
	default:
	}
	// The sole operation remains active until this capture has returned.
	select {
	case <-run.restoredExecutionDone:
		return ErrExecutionEpochOne
	default:
	}
	if reader.projection.Phase != "product_queries" || !reader.finalUsed || !reader.productQueriesComplete ||
		!epochRestoredClosedEvidence(ExecutionEpochOneResult{
			Inspection: reader.evidence.rows, RestoredSamples: reader.restoredSamples,
			ProductFinals: reader.productFinalCalls, ProductQueries: reader.productQueries,
			ProductFirstFinalOrdinal: reader.productFirstFinalOrdinal,
		}, 14) || reader.productBaseline == nil ||
		reader.productFinalDigests[0] == ([sha256.Size]byte{}) ||
		reader.productFinalDigests[0] != reader.productFinalDigests[1] ||
		reader.productFinalDigests[0] != *reader.productBaseline ||
		!reader.productQueryAuthority.valid() {
		return ErrExecutionEpochOne
	}
	last := reader.evidence.rows[len(reader.evidence.rows)-1]
	if last.Final.Authority != reader.productAuthority.AuthorityState || reader.productAuthority.Phase != "product_queries" ||
		reader.productAuthority.Outcome != "passed" || last.NextOrdinal != reader.next {
		return ErrExecutionEpochOne
	}
	// Receipt hashes intentionally use its actual AuthorityPhaseResult wire
	// projection. The separate raw F digests above also cover detailed roots
	// and query_authority, which are not fields of that receipt hash.
	authorityHash, err := authorityResultSHA256([]AuthorityPhaseResult{reader.productAuthority}, "product_queries")
	if err != nil {
		return err
	}
	value := QueryEvidence{Phase: "product_queries", Outcome: "passed", Results: make([]QueryResult, len(reader.productQueries)/2)}
	for index, row := range reader.productQueries {
		transport, err := completedProductTransport(row, reader.plan.ReceiptContract.QueryTransportSchema, authorityHash)
		if err != nil {
			return err
		}
		slot := index % len(value.Results)
		if row.Transport == "http" {
			value.Results[slot] = QueryResult{Name: row.Name, HTTP: transport}
		} else {
			value.Results[slot].MCP = transport
		}
	}
	controls, err := checkedInspectionReadSum(last.Reads.ControlFileReads, last.Reads.StoreReadAttempts)
	if err != nil {
		return err
	}
	// This is the actual phase inspection prefix, not fabricated whole-phase
	// metrics. Final receipt assembly must still validate the whole phase.
	if err := validateQueryEvidence(value, "passed", authorityHash,
		ReceiptMetrics{ControlReads: CountMetric(controls), MemberReads: CountMetric(last.Reads.MemberVisits)}, reader.plan); err != nil {
		return err
	}
	if ctx.Err() != nil || run.control.Context().Err() != nil || run.control.RequestToken() != "" {
		return ErrExecutionEpochOne
	}
	reader.productQueryEvidence = &value
	return nil
}

// Pure conversion behind the owned completion guard. Every page already passed
// fresh native auth, typed status/body validation and exact cursor closure.
// Thus Code is an observed consistent logical verdict, never plan.Authorization.
func completedProductTransport(row ExecutionProductQuery, schema, authorityHash string) (QueryTransportResult, error) {
	controls, err := checkedInspectionReadSum(row.ControlFileReads, row.StoreReadAttempts)
	if err != nil || schema == "" || !validDigest(authorityHash) || row.Pages == 0 || !validDigest(row.ProjectionSHA256) {
		return QueryTransportResult{}, ErrExecutionEpochOne
	}
	decision, repositories := "", uint64(0)
	switch {
	case row.Transport == "http" && row.Code == "200", row.Transport == "mcp" && row.Code == "ok":
		decision = "t421-authorized-visible-v1"
		if row.Name == "all_code_structural_marker" {
			if !row.VisibleRepositoriesObserved || row.VisibleRepositories != 1 {
				return QueryTransportResult{}, ErrExecutionEpochOne
			}
			repositories = row.VisibleRepositories
		} else {
			if row.VisibleRepositoriesObserved || row.VisibleRepositories != 0 {
				return QueryTransportResult{}, ErrExecutionEpochOne
			}
			// Complete named-repository envelopes (including empty results)
			// were validated against the actual repository/F by the projector.
			repositories = 1
		}
	case row.Transport == "http" && row.Code == "404", row.Transport == "mcp" && row.Code == "unknown_repository":
		if row.VisibleRepositoriesObserved || row.VisibleRepositories != 0 {
			return QueryTransportResult{}, ErrExecutionEpochOne
		}
		decision = "t421-authorized-hidden-v1"
	default:
		return QueryTransportResult{}, ErrExecutionEpochOne
	}
	return QueryTransportResult{
		Schema: schema, Code: row.Code, Pages: row.Pages, Records: row.Records, Paths: row.Paths,
		ControlReads: controls, MemberReads: row.MemberVisits, ProjectionSHA256: row.ProjectionSHA256,
		PaginationClosedExactly: true, AuthorizationDecisions: 1, AuthorizationDecision: decision,
		AuthorizedRepositories: repositories, AuthoritySnapshots: 1,
		AuthorityBeforeSHA256: authorityHash, AuthorityAfterSHA256: authorityHash,
	}, nil
}

func cloneProductQueryEvidence(value *QueryEvidence) *QueryEvidence {
	if value == nil {
		return nil
	}
	copy := *value
	copy.Results = slices.Clone(value.Results) // Nested transport rows contain scalars/strings only.
	return &copy
}
