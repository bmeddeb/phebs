package t421

import (
	"context"
	"reflect"
	"slices"

	"github.com/bmeddeb/phebs/internal/servicecatalog"
)

// This is observed content change within the closed prepared display-name
// recipe, not state-row writes, arbitrary catalog diffs or complete phase metrics.
// Complete distinguishes a genuinely accepted zero from missing evidence.
type ExecutionLogicalChangeObservation struct {
	ChangedAcceptedServices uint64
	Prior, Current          CatalogSourceProfile
	Complete                bool
}

type epochPreparedLogicalChanges struct {
	Sources [3]CatalogSourceProfile
	Counts  [2]uint64
}

type epochAcceptedCatalog struct {
	Phase     string
	Epoch     uint64
	Source    CatalogSourceProfile
	Authority AuthorityState
}

// Compare the actual typed values serialized by epochCatalogInputsObserved.
// The frozen recipe changes accepted display names only. Memberships, complement,
// dispositions, successors, origin and authority selection must remain equal;
// unknown changes refuse rather than silently acquire general delta semantics.
func countEpochAcceptedDisplayChanges(ctx context.Context, prior, current servicecatalog.Catalog) (uint64, error) {
	if ctx == nil || ctx.Err() != nil || prior.Schema != servicecatalog.Schema ||
		prior.Authority.Kind != servicecatalog.AuthorityOperator || len(prior.Services) == 0 || len(prior.Services) != len(current.Services) {
		return 0, ErrExecutionEpochConfigs
	}
	a, b := prior, current
	a.Authority.Version, b.Authority.Version = "", ""
	a.Services, b.Services = nil, nil
	if !reflect.DeepEqual(a, b) {
		return 0, ErrExecutionEpochConfigs
	}
	var count uint64
	for i, left := range prior.Services {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		right := current.Services[i]
		if left.Key == "" || left.Key != right.Key || i > 0 && prior.Services[i-1].Key >= left.Key {
			return 0, ErrExecutionEpochConfigs
		}
		changed := left.DisplayName != right.DisplayName
		left.DisplayName = right.DisplayName
		if !reflect.DeepEqual(left, right) || changed && right.Disposition != servicecatalog.DispositionAccepted {
			return 0, ErrExecutionEpochConfigs
		}
		// Bounded by the actual service slice length, not a plan count.
		if changed {
			count++
		}
	}
	return count, ctx.Err()
}

// Called only after the predecessor's real Wait, before taking the flow lock.
// Copy only fixed source/authority values; no old reader/config lock survives.
func (reader *executionEpochInspection) acceptedCatalogPrefix(ctx context.Context, phase string) (*epochAcceptedCatalog, error) {
	if reader == nil || ctx == nil || ctx.Err() != nil {
		return nil, errEpochInspection
	}
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if ctx.Err() != nil || reader.err != nil || len(reader.evidence.rows) == 0 {
		return nil, errEpochInspection
	}
	row := reader.evidence.rows[len(reader.evidence.rows)-1]
	if row.Phase != phase || !row.SelectorAccepted || row.Final == nil || row.Final.Ordinal == 0 || row.Final.Projection.Phase != phase || !row.Final.Authority.Current {
		return nil, errEpochInspection
	}
	return &epochAcceptedCatalog{Phase: row.Phase, Epoch: row.ServerEpoch, Source: row.Final.Projection.CatalogSource, Authority: row.Final.Authority}, nil
}

// Called under the existing reader/run acceptance locks. Configuration facts
// were fixed before any launch and are never mutated or cleared by Close;
// borrow/run acceptance owns their lifetime. No configuration lock or I/O here.
func (reader *executionEpochInspection) acceptedLogicalChanges(row ExecutionPhaseInspection) (ExecutionLogicalChangeObservation, error) {
	var out ExecutionLogicalChangeObservation
	index := slices.Index([]string{"physical_delta_b", "logical_delta_b", "return_a"}, row.Phase)
	if index < 0 {
		return out, nil
	}
	run := reader.run
	if reader.plan.Schema != PlanV3Schema || run == nil || run.flow == nil || run.flow.epochs == nil ||
		run.flow.epochs.logicalChanges == nil || row.ServerEpoch != uint64(index+1) || run.epoch.Epoch != row.ServerEpoch ||
		!reader.finalUsed || !reader.progressReady || reader.tail.Status != "ready" || row.Final == nil || row.Final.Ordinal == 0 || row.Final.Projection.Phase != row.Phase || !row.Final.Authority.Current {
		return out, errEpochInspection
	}
	prepared := run.flow.epochs.logicalChanges
	var prior *epochAcceptedCatalog
	var expectedPrior, actualCurrent AuthorityState
	switch index {
	case 0:
		if len(reader.evidence.rows) < 2 {
			return out, errEpochInspection
		}
		before := reader.evidence.rows[len(reader.evidence.rows)-2]
		if !before.SelectorAccepted || before.Final == nil || before.Final.Ordinal == 0 {
			return out, errEpochInspection
		}
		prior = &epochAcceptedCatalog{Phase: before.Phase, Epoch: before.ServerEpoch, Source: before.Final.Projection.CatalogSource, Authority: before.Final.Authority}
		expectedPrior, actualCurrent = reader.warmAuthority.AuthorityState, reader.physicalAuthority.AuthorityState
	case 1:
		if run.priorPhysical == nil {
			return out, errEpochInspection
		}
		prior = run.priorPhysical.catalog
		expectedPrior, actualCurrent = reader.physicalAuthority.AuthorityState, reader.logicalAuthority.AuthorityState
	case 2:
		if run.priorLogical == nil {
			return out, errEpochInspection
		}
		prior = run.priorLogical.catalog
		expectedPrior, actualCurrent = reader.logicalAuthority.AuthorityState, reader.returnAuthority.AuthorityState
	}
	beforeIndex := 0
	if index == 2 {
		beforeIndex = 1
	}
	expectedPhase := []string{"warm_noop", "physical_delta_b", "logical_delta_b"}[index]
	expectedEpoch := []uint64{1, 1, 2}[index]
	if prior == nil || prior.Phase != expectedPhase || prior.Epoch != expectedEpoch || !prior.Authority.Current ||
		prior.Authority != expectedPrior || row.Final.Authority != actualCurrent || prior.Source != prepared.Sources[beforeIndex] ||
		row.Final.Projection.CatalogSource != prepared.Sources[index] || run.epoch.CatalogSHA256 != prepared.Sources[index].SHA256 ||
		!validDigest(prior.Source.SHA256) || !validDigest(row.Final.Projection.CatalogSource.SHA256) {
		return out, errEpochInspection
	}
	out.Prior, out.Current = prior.Source, row.Final.Projection.CatalogSource
	if index > 0 {
		out.ChangedAcceptedServices = prepared.Counts[index-1]
	}
	// For physical B both actual protected/F source identities are identical,
	// so zero follows from content equality rather than the expected phase bound.
	// F already validated native activation/marker continuity. Only the actual
	// request-fenced acceptInspectionPhase caller may commit this derived fact.
	out.Complete = true
	return out, nil
}
