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
}

// Compact native identities only: detailed extraction partition results remain
// in the existing authority owners, not duplicated in every result snapshot.
type ExecutionInspectionFinal struct {
	Ordinal    uint64
	Authority  AuthorityState
	Projection PhaseStateProjection
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
	reader.run.mu.Lock()
	defer reader.run.mu.Unlock()
	if reader.run.stopping || reader.run.err != nil {
		return errEpochInspection
	}
	changes, err := reader.acceptedLogicalChanges(*row)
	if err != nil {
		return err
	}
	row.LogicalChanges = changes
	row.SelectorAccepted = true
	return nil
}

func cloneInspectionEvidence(rows []ExecutionPhaseInspection) []ExecutionPhaseInspection {
	result := slices.Clone(rows)
	for i := range result {
		if result[i].Final != nil {
			result[i].Final = cloneInspectionFinal(*result[i].Final)
		}
	}
	return result
}

func cloneInspectionFinal(value ExecutionInspectionFinal) *ExecutionInspectionFinal {
	value.Projection.ExtractionRoots = slices.Clone(value.Projection.ExtractionRoots)
	value.Projection.RelationshipResults = slices.Clone(value.Projection.RelationshipResults)
	return &value
}
