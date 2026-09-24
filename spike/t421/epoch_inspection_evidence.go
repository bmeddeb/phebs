package t421

import (
	"context"
	"slices"

	"github.com/bmeddeb/phebs/internal/readaccounting"
)

// ExecutionPhaseInspection is an epoch-local exact-read prefix. A returned F
// and an accepted selector are distinct facts; neither is whole-phase metrics.
// Phase eight deliberately has separate predecessor and recovered-epoch rows.
type ExecutionPhaseInspection struct {
	ServerEpoch                                uint64
	Phase                                      string
	FirstOrdinal, NextOrdinal, AcceptedReports uint64
	Reads                                      readaccounting.Counts
	Final                                      *ExecutionInspectionFinal
	SelectorAccepted                           bool
	LogicalChanges                             ExecutionLogicalChangeObservation
	TransitionReads                            *TransitionReadSubtotal
	SelectorCleanup                            *SelectorCleanupEvidence // V5: exact successful HTTP observation, not whole-phase accounting.
}

// Compact native identities only: detailed extraction partition results remain
// in the existing authority owners, not duplicated in every result snapshot.
type ExecutionCatalogPopulation struct {
	AcceptedServices uint64 `json:"accepted_services"`
}

type ExecutionInspectionFinal struct {
	CatalogPopulation     *ExecutionCatalogPopulation
	Ordinal               uint64
	Authority             AuthorityState
	Projection            PhaseStateProjection
	RPCPostings           *ExecutionRPCPostingObservation
	ResolverCatalogCounts *readaccounting.ResolverCatalogCounts
	CallerPublication     *ExecutionCallerPublicationObservation
}

type epochInspectionLedger struct {
	rows        []ExecutionPhaseInspection
	baseReports uint64
	baseReads   readaccounting.Counts
}

// Called under reader.mu only when the existing reader consumes an ordinal.
func (reader *executionEpochInspection) beginInspectionEvidence() {
	ledger := &reader.evidence
	if len(ledger.rows) == 0 || ledger.rows[len(ledger.rows)-1].Phase != reader.projection.Phase {
		ledger.rows = append(ledger.rows, ExecutionPhaseInspection{ServerEpoch: reader.run.epoch.Epoch,
			Phase: reader.projection.Phase, FirstOrdinal: reader.next, NextOrdinal: reader.next})
		ledger.baseReports, ledger.baseReads = reader.reports, reader.totals
	}
}

func (reader *executionEpochInspection) finishInspectionEvidence() {
	ledger := &reader.evidence
	row := &ledger.rows[len(ledger.rows)-1]
	row.NextOrdinal, row.AcceptedReports = reader.next, reader.reports-ledger.baseReports
	row.Reads = readaccounting.Counts{
		ControlFileReads:   reader.totals.ControlFileReads - ledger.baseReads.ControlFileReads,
		StoreReadAttempts:  reader.totals.StoreReadAttempts - ledger.baseReads.StoreReadAttempts,
		MemberVisits:       reader.totals.MemberVisits - ledger.baseReads.MemberVisits,
		StoreWriteAttempts: reader.totals.StoreWriteAttempts - ledger.baseReads.StoreWriteAttempts,
	}
}

// The real selector calls this only after its cleanup, request fence and any
// immediate handoff succeed. finalUsed is intentionally not an acceptance flag.
func (reader *executionEpochInspection) acceptInspectionPhase(ctx context.Context) error {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if ctx == nil || ctx.Err() != nil || reader.err != nil || reader.run == nil || reader.run.control == nil ||
		reader.run.control.Context().Err() != nil || reader.run.control.RequestToken() != "" || len(reader.evidence.rows) == 0 {
		return errEpochInspection
	}
	row := &reader.evidence.rows[len(reader.evidence.rows)-1]
	if row.Phase != reader.projection.Phase || row.Final == nil || row.SelectorAccepted {
		return errEpochInspection
	}
	actual := reader.finalAuthority
	if actual.Phase != "" && (actual.Phase != row.Phase || actual.Outcome != "passed" || actual.AuthorityState != row.Final.Authority) {
		return errEpochInspection
	}
	reader.run.mu.Lock()
	if reader.run.stopping || reader.run.err != nil {
		reader.run.mu.Unlock()
		return errEpochInspection
	}
	changes, err := reader.acceptedLogicalChanges(*row)
	reader.run.mu.Unlock()
	if err != nil {
		return err
	}
	// Lower-level guard tests may model the compact F row directly. Production
	// Final always retains the detailed value; an absent value is never inferred
	// from the compact row and therefore cannot enter the result projection.
	if actual.Phase != "" && reader.run.flow.retainAcceptedAuthority(reader.run, actual) != nil {
		return errEpochInspection
	}
	row.LogicalChanges = changes
	row.SelectorAccepted = true
	return nil
}

// retainAcceptedAuthority records only the next actual F accepted by the
// production phase coordinator. The flow already owns the validated plan; this
// path performs no I/O, plan rebuild, or authority inference.
func (flow *ExecutionEpochOne) retainAcceptedAuthority(run *ExecutionEpochOneRun, value AuthorityPhaseResult) error {
	if flow == nil || run == nil || run.flow != flow {
		return errEpochInspection
	}
	flow.mu.Lock()
	defer flow.mu.Unlock()
	run.mu.Lock()
	defer run.mu.Unlock()
	phases := flow.plan.PhaseOrder
	if flow.closed || run.stopping || run.err != nil || !processAccountingPlanSemantics(flow.plan.Schema) ||
		!slices.Equal(phases, frozenPhaseOrder()) || len(flow.authorities) >= len(phases)-2 ||
		value.Phase != phases[len(flow.authorities)+1] || value.Outcome != "passed" || !value.Current ||
		!validDigest(value.ExtractionRootsSHA256) || len(value.ExtractionRoots) != len(flow.plan.ReceiptContract.ExtractionDomains) {
		return errEpochInspection
	}
	flow.authorities = append(flow.authorities, cloneExecutionAuthorityResult(value))
	return nil
}

func (flow *ExecutionEpochOne) acceptedAuthorityPrefix() []AuthorityPhaseResult {
	if flow == nil {
		return nil
	}
	flow.mu.Lock()
	defer flow.mu.Unlock()
	return cloneExecutionAuthorityResults(flow.authorities)
}

func cloneExecutionAuthorityResults(values []AuthorityPhaseResult) []AuthorityPhaseResult {
	result := slices.Clone(values)
	for index, value := range values {
		result[index] = cloneExecutionAuthorityResult(value)
	}
	return result
}

func cloneInspectionEvidence(rows []ExecutionPhaseInspection) []ExecutionPhaseInspection {
	result := slices.Clone(rows)
	for i := range result {
		if result[i].SelectorCleanup != nil {
			value := *result[i].SelectorCleanup
			result[i].SelectorCleanup = &value
		}
		if result[i].TransitionReads != nil {
			value := *result[i].TransitionReads
			result[i].TransitionReads = &value
		}
		if result[i].Final != nil {
			result[i].Final = cloneInspectionFinal(*result[i].Final)
		}
	}
	return result
}

func cloneInspectionFinal(value ExecutionInspectionFinal) *ExecutionInspectionFinal {
	if value.CatalogPopulation != nil {
		counts := *value.CatalogPopulation
		value.CatalogPopulation = &counts
	}
	if value.ResolverCatalogCounts != nil {
		counts := *value.ResolverCatalogCounts
		value.ResolverCatalogCounts = &counts
	}
	if value.CallerPublication != nil {
		caller := *value.CallerPublication
		caller.Leaves = slices.Clone(caller.Leaves)
		value.CallerPublication = &caller
	}
	if value.RPCPostings != nil {
		counts := *value.RPCPostings
		value.RPCPostings = &counts
	}
	value.Projection.ExtractionRoots = slices.Clone(value.Projection.ExtractionRoots)
	value.Projection.RelationshipResults = slices.Clone(value.Projection.RelationshipResults)
	return &value
}
