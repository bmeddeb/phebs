package t421

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/bmeddeb/phebs/internal/servicecatalog"
)

func TestEpochPreparedLogicalChangesActualInputs(t *testing.T) {
	// Actual existing generator/serializer, not native activation or F proof.
	inputs, changes, err := epochCatalogInputsObserved(t.Context(), accountingTestPlan(t))
	if err != nil || len(inputs) != 3 {
		t.Fatal(err)
	}
	for i, input := range inputs {
		var catalog servicecatalog.Catalog
		if json.Unmarshal(input.raw, &catalog) != nil {
			t.Fatal("actual prepared bytes")
		}
		source := CatalogSourceProfile{Schema: catalogSourceSchema, Bytes: uint64(len(input.raw)), SHA256: SHA256(input.raw), Records: uint64(len(catalog.Services) + len(catalog.Memberships) + len(catalog.Unowned))}
		if changes.Sources[i] != source {
			t.Fatal("preparation identity not actual bytes", i, changes)
		}
	}
	if changes.Counts != [2]uint64{1, 1} {
		t.Fatal(changes)
	}
}

func logicalChangesTestCatalogs(t *testing.T) [3]servicecatalog.Catalog {
	t.Helper()
	base := servicecatalog.Catalog{Schema: servicecatalog.Schema, Authority: servicecatalog.Authority{Kind: servicecatalog.AuthorityOperator, ID: "fixture", Version: "fixture"},
		Services: []servicecatalog.Service{
			{Key: "a", DisplayName: "A", Disposition: servicecatalog.DispositionAccepted},
			{Key: "b", DisplayName: "B", Disposition: servicecatalog.DispositionAccepted},
			{Key: "c", DisplayName: "C", Disposition: servicecatalog.DispositionProposal},
		},
		Memberships: []servicecatalog.Membership{{ServiceKey: "a", Path: "a.go", Role: "primary"}, {ServiceKey: "b", Path: "b.go", Role: "primary"}},
	}
	var catalogs [3]servicecatalog.Catalog
	for i, name := range []string{"a", "b", "a-return"} {
		var err error
		catalogs[i], err = logicalCatalogForRevision(base, name)
		if err != nil {
			t.Fatal(err)
		}
	}
	return catalogs
}

func TestEpochLogicalChangesClosedRecipe(t *testing.T) {
	for _, mode := range []string{"physical_only", "b", "return", "changed_membership", "nonaccepted", "reason", "origin", "successors", "addition", "key", "authority", "unowned", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			catalogs := logicalChangesTestCatalogs(t)
			prior, current := catalogs[0], catalogs[1]
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			want := uint64(1)
			valid := false
			switch mode {
			case "physical_only":
				current = cloneCatalog(prior)
				current.Authority.Version = "physical-provenance-only"
				want = 0
				valid = true
			case "b":
				valid = true
			case "return":
				prior, current = catalogs[1], catalogs[2]
				valid = true
			case "changed_membership":
				current.Memberships[0].Path = "different.go"
			case "nonaccepted":
				current.Services[2].DisplayName = "changed"
			case "reason":
				current.Services[0].Reason = "changed"
			case "origin":
				current.Services[0].Origin = "override"
			case "successors":
				current.Services[0].Successors = []string{"b"}
			case "addition":
				current.Services = append(current.Services, servicecatalog.Service{Key: "d"})
			case "key":
				current.Services[0].Key = "different"
			case "authority":
				current.Authority.ID = "different"
			case "unowned":
				current.Unowned = []servicecatalog.UnownedPlacement{{Path: "other"}}
			case "canceled":
				cancel()
			}
			count, err := countEpochAcceptedDisplayChanges(ctx, prior, current)
			if (err == nil) != valid || valid && count != want {
				t.Fatal(count, err)
			}
		})
	}
}

// The F/phase/authority values below are supplied models. Only the reused
// request-fence socket exchanges are real; this is not native phase-4/5/6 proof.
func TestEpochLogicalChangesAcceptedBoundary(t *testing.T) {
	for _, phase := range []string{"physical_delta_b", "logical_delta_b", "return_a"} {
		for _, mode := range []string{"accepted", "missing_prepared", "missing_prior", "prior_unaccepted", "prior_source", "current_source", "current_authority", "wrong_epoch", "incomplete_final", "open_requests", "canceled", "failed_reader", "failed_run", "duplicate"} {
			t.Run(phase+"/"+mode, func(t *testing.T) {
				sources := [3]CatalogSourceProfile{
					{Schema: catalogSourceSchema, SHA256: testDigest("actual-model-a"), Bytes: 10, Records: 2},
					{Schema: catalogSourceSchema, SHA256: testDigest("actual-model-b"), Bytes: 11, Records: 2},
					{Schema: catalogSourceSchema, SHA256: testDigest("actual-model-return"), Bytes: 12, Records: 2},
				}
				prepared := &epochPreparedLogicalChanges{Sources: sources, Counts: [2]uint64{2, 3}}
				// Unequal supplied counts ensure acceptance does not stamp expected ones.
				index := 0
				if phase == "logical_delta_b" {
					index = 1
				}
				if phase == "return_a" {
					index = 2
				}
				beforeIndex := 0
				if index == 2 {
					beforeIndex = 1
				}
				priorPhase := []string{"warm_noop", "physical_delta_b", "logical_delta_b"}[index]
				priorEpoch := []uint64{1, 1, 2}[index]
				priorAuthority := AuthorityState{Current: true, CatalogRootSHA256: testDigest("prior")}
				currentAuthority := AuthorityState{Current: true, CatalogRootSHA256: testDigest("current")}
				before := ExecutionPhaseInspection{Phase: priorPhase, ServerEpoch: priorEpoch, SelectorAccepted: true, Final: &ExecutionInspectionFinal{Ordinal: 1,
					Authority: priorAuthority, Projection: PhaseStateProjection{Phase: priorPhase, CatalogSource: sources[beforeIndex]}}}
				old := &executionEpochInspection{evidence: epochInspectionLedger{rows: []ExecutionPhaseInspection{before}}}
				prior, err := old.acceptedCatalogPrefix(t.Context(), priorPhase)
				if err != nil {
					t.Fatal(err)
				}
				run := &ExecutionEpochOneRun{flow: &ExecutionEpochOne{epochs: &ExecutionEpochConfigCustody{logicalChanges: prepared}},
					epoch:         ExecutionEpochConfig{Epoch: uint64(index + 1), CatalogSHA256: sources[index].SHA256},
					control:       modeledQueryReceiptControl(t, mode == "open_requests"),
					priorPhysical: &epochLogicalPrior{catalog: prior}, priorLogical: &epochReturnPrior{catalog: prior},
				}
				row := ExecutionPhaseInspection{Phase: phase, ServerEpoch: uint64(index + 1), Final: &ExecutionInspectionFinal{Ordinal: 2,
					Authority: currentAuthority, Projection: PhaseStateProjection{Phase: phase, CatalogSource: sources[index]}}}
				reader := &executionEpochInspection{run: run, plan: Plan{Schema: PlanV3Schema}, projection: PhaseStateProjection{Phase: phase},
					finalUsed: true, progressReady: true, tail: epochTailReadiness{Status: "ready"},
					warmAuthority:     AuthorityPhaseResult{AuthorityState: priorAuthority},
					physicalAuthority: AuthorityPhaseResult{AuthorityState: priorAuthority},
					logicalAuthority:  AuthorityPhaseResult{AuthorityState: priorAuthority},
					returnAuthority:   AuthorityPhaseResult{AuthorityState: currentAuthority},
				}
				if index == 0 {
					reader.physicalAuthority.AuthorityState = currentAuthority
				}
				if index == 1 {
					reader.logicalAuthority.AuthorityState = currentAuthority
				}
				reader.evidence.rows = []ExecutionPhaseInspection{before, row}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				switch mode {
				case "missing_prepared":
					run.flow.epochs.logicalChanges = nil
				case "missing_prior":
					reader.evidence.rows = reader.evidence.rows[1:]
					run.priorPhysical.catalog = nil
					run.priorLogical.catalog = nil
				case "prior_unaccepted":
					old.evidence.rows[0].SelectorAccepted = false
					if _, err := old.acceptedCatalogPrefix(ctx, priorPhase); err == nil {
						t.Fatal("unaccepted prior captured")
					}
					reader.evidence.rows[0].SelectorAccepted = false
					run.priorPhysical.catalog = nil
					run.priorLogical.catalog = nil
				case "prior_source":
					prior.Source.SHA256 = testDigest("wrong")
					reader.evidence.rows[0].Final.Projection.CatalogSource.SHA256 = testDigest("wrong")
				case "current_source":
					reader.evidence.rows[1].Final.Projection.CatalogSource.SHA256 = testDigest("wrong")
				case "current_authority":
					reader.evidence.rows[1].Final.Authority.CatalogRootSHA256 = testDigest("wrong")
				case "wrong_epoch":
					run.epoch.Epoch = 5
				case "incomplete_final":
					reader.finalUsed = false
				case "canceled":
					cancel()
				case "failed_reader":
					reader.err = errEpochInspection
				case "failed_run":
					run.err = ErrExecutionEpochOne
				case "duplicate":
					reader.evidence.rows[1].SelectorAccepted = true
				}
				err = reader.acceptInspectionPhase(ctx)
				got := reader.evidence.rows[len(reader.evidence.rows)-1].LogicalChanges
				if mode != "accepted" {
					if err == nil || got.Complete {
						t.Fatal("incomplete boundary acquired content evidence", got, err)
					}
					return
				}
				want := uint64(0)
				if index > 0 {
					want = prepared.Counts[index-1]
				}
				if err != nil || !got.Complete || got.ChangedAcceptedServices != want || got.Prior != sources[beforeIndex] || got.Current != sources[index] {
					t.Fatal(got, err)
				}
				// Existing value-only evidence copying and handoff must detach retention.
				copyRows := cloneInspectionEvidence(reader.evidence.rows)
				copyRows[len(copyRows)-1].LogicalChanges.Current.SHA256 = testDigest("caller")
				if !reflect.DeepEqual(reader.evidence.rows[len(reader.evidence.rows)-1].LogicalChanges, got) {
					t.Fatal("count evidence aliased caller")
				}
				old.evidence.rows[0].Final.Projection.CatalogSource.SHA256 = testDigest("changed-after-handoff")
				if prior.Source != sources[beforeIndex] {
					t.Fatal("prior source handoff aliased old reader")
				}
			})
		}
	}
}
