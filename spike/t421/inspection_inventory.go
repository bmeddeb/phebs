package t421

import (
	"errors"
	"math"
	"slices"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/kafkatopicposting"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/recovery"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/rpccallerposting"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	correctedInspectionInventorySchema      = "t421-compact-inspection-inventory-v2"
	correctedHealthPollMS                   = uint64(250)
	correctedInspectionPollMS               = uint64(5_000)
	correctedTailControlReads               = uint64(4)
	correctedTailStoreReads                 = uint64(4)
	correctedPhysicalTransitionControlReads = uint64(41)
	correctedReturnTransitionControlReads   = uint64(5)
	correctedPhysicalTransitionReadClass    = "search-reader-current-prior-retention"
	correctedLogicalTransitionReadClass     = "catalog-activation-residue-and-recovery"
	correctedReturnTransitionReadClass      = "relationship-marker-recovery"
	correctedStaleLeaseTransitionReadClass  = "prepared-stale-lease-schedule-and-result"
	correctedCheckpointRestartReadClass     = "prepared-checkpoint-hard-restart"
	correctedPressure80TransitionReadClass  = "pressure-normalization-and-collect-cycle"
	correctedPressure90TransitionReadClass  = "typed-pressure-refusal"
	correctedPressure75TransitionReadClass  = "pressure-refusal-removal-and-recovery-cycle"
	correctedArchiveTransitionReadClass     = "archive-destroy-empty-target-restore-and-semantic-binding"
	correctedLifecycleTransitionReadClass   = "fresh-sixteen-owner-cycle"
	// cmd/phebs/TestT421FinalAuthorityMaximumReadLimitsAndRefusal pins these
	// values to the production admission derivation without exporting a second
	// production API solely for the prospective contract.
	correctedFinalAuthorityControlReadMaximum = uint64(18_469)
	correctedFinalAuthorityStoreReadMaximum   = uint64(528)
	correctedFinalAuthorityMemberReadMaximum  = uint64(589_656_064)
	correctedInspectionPolicy                 = "compact-inspector-v2:H=health@250ms;X=progress@0,+5s-until-ready;T=selected-relationship+resolver-catalog+caller-current@0,+5s-after-X-until-ready;F=coherent-current+selected-activation+authorized-semantics-once-after-T;L=lifecycle@0,+5s-only:p80,p75,lifecycle;R=transition-local;Q=plan-pages;product=T,F,Q,F;archive=R,T,F;other=T,F;attempt-max=1+floor(deadline/cadence);cache=process-epoch-local-immutable-members-after-fresh-complete-key;fresh=pointers,auth,epoch,lifecycle,residue,pages;M=decoded-application-record@candidate-artifact/projection,source-owner,catalog-service/membership/inherited/placement,relationship-fragment/service,rpc/kafka-posting,caller-leaf;before-later-checks;reread=1;root/pointer/receipt/descriptor/response-wrapper/cache-hit=0;warm/empty=0;Q-order=plan-case:http,mcp;Q-exclusive;Q-all-code=shared-current;Q-cache=relationship-prewarmed-by-current-pin,catalog-root/member-cold-once;F-catalog-cache=private-from-Q"
)

// phaseInspectionInventory is the prospective compact T42 inspector call
// graph. It is derived from the phase/deadline/epoch tables; it is not a
// receipt. A nil TransitionRead leaves that phase's R accounting open.
type phaseInspectionInventory struct {
	Phase                     string
	ServerEpoch               uint64
	HealthCalls               CounterBound
	ExtractionProgressCalls   CounterBound
	TailReadinessCalls        CounterBound
	TailControlFileReads      CounterBound
	TailStoreReadAttempts     CounterBound
	FinalAuthorityPasses      CounterBound
	LifecycleStatusCalls      CounterBound
	TransitionReadClass       string
	TransitionRead            *inspectionReadBound
	TransitionReadEpochs      []uint64
	ProductHTTPCalls          CounterBound
	ProductMCPCalls           CounterBound
	ProductControlFileReads   CounterBound
	ProductStoreReadAttempts  CounterBound
	ImmutableMemberReusePhase string
}

type inspectionReadBound struct {
	Calls              CounterBound
	ControlFileReads   CounterBound
	StoreReadAttempts  CounterBound
	MemberReads        CounterBound
	StoreWriteAttempts CounterBound
}

type phaseReadMaximum struct {
	Phase        string
	ControlReads uint64
	MemberReads  uint64
}

// applyCorrectedPhaseReadMaximums replaces the retained topology proxies with
// the versioned scoped ledger. Minima and every unrelated work ceiling
// remain inherited. H and L are in-memory and therefore contribute no native
// reads; X/T/F/R/Q/prep are derived below from their owning call inventories.
func applyCorrectedPhaseReadMaximums(work *WorkEnvelope, plan Plan) error {
	maximums, err := correctedPhaseReadMaximums(plan)
	if err != nil {
		return err
	}
	if len(work.Phases) != len(maximums) {
		return errors.New("corrected phase read maximum inventory differs")
	}
	for index := range work.Phases {
		value, maximum := &work.Phases[index], maximums[index]
		if value.Phase != maximum.Phase || maximum.ControlReads < value.ControlReads.Minimum ||
			maximum.MemberReads < value.MemberReads.Minimum {
			return errors.New("corrected phase read maximum is invalid")
		}
		value.ControlReads.Maximum = maximum.ControlReads
		value.MemberReads.Maximum = maximum.MemberReads
	}
	return nil
}

func correctedPhaseReadMaximums(plan Plan) ([]phaseReadMaximum, error) {
	rows, _, err := correctedInspectionInventory(plan.Profile)
	if err != nil {
		return nil, err
	}
	queryMembers, err := correctedProductQueryMemberReadMaximum(correctedQueryCases())
	if err != nil {
		return nil, err
	}
	result := make([]phaseReadMaximum, len(rows))
	for index, row := range rows {
		controls, members, boundErr := correctedScopedPhaseReadMaximum(plan, row, queryMembers)
		if boundErr != nil {
			return nil, boundErr
		}
		result[index] = phaseReadMaximum{Phase: row.Phase, ControlReads: controls, MemberReads: members}
	}
	return result, nil
}

func correctedScopedPhaseReadMaximum(
	plan Plan,
	row phaseInspectionInventory,
	queryMembers uint64,
) (uint64, uint64, error) {
	domains := uint64(len(plan.Profile.Pipeline.ExtractionDomains))
	xControlPerCall, err := checkedInspectionReadSum(2, domains)
	if err != nil {
		return 0, 0, errors.New("corrected extraction control read maximum overflows")
	}
	xStorePerCall, err := checkedMultiply(2, store.MaxGenerationScheduleReadAttempts)
	if err != nil {
		return 0, 0, errors.New("corrected extraction store read maximum overflows")
	}
	xStorePerCall, err = checkedInspectionReadSum(2, xStorePerCall)
	if err != nil {
		return 0, 0, errors.New("corrected extraction store read maximum overflows")
	}
	xPerCall, err := checkedInspectionReadSum(xControlPerCall, xStorePerCall)
	if err != nil {
		return 0, 0, errors.New("corrected extraction read maximum overflows")
	}
	xReads, err := checkedMultiply(row.ExtractionProgressCalls.Maximum, xPerCall)
	if err != nil {
		return 0, 0, errors.New("corrected extraction phase read maximum overflows")
	}
	tailReads, err := checkedInspectionReadSum(
		row.TailControlFileReads.Maximum,
		row.TailStoreReadAttempts.Maximum,
	)
	if err != nil {
		return 0, 0, errors.New("corrected tail phase read maximum overflows")
	}
	finalPerCall, err := checkedInspectionReadSum(
		correctedFinalAuthorityControlReadMaximum,
		correctedFinalAuthorityStoreReadMaximum,
	)
	if err != nil {
		return 0, 0, errors.New("corrected final-authority read maximum overflows")
	}
	finalReads, err := checkedMultiply(row.FinalAuthorityPasses.Maximum, finalPerCall)
	if err != nil {
		return 0, 0, errors.New("corrected final-authority phase read maximum overflows")
	}
	finalMembers, err := checkedMultiply(
		row.FinalAuthorityPasses.Maximum,
		correctedFinalAuthorityMemberReadMaximum,
	)
	if err != nil {
		return 0, 0, errors.New("corrected final-authority phase member maximum overflows")
	}

	var transitionReads, transitionMembers uint64
	if row.TransitionRead != nil {
		transitionReads, err = checkedInspectionReadSum(
			row.TransitionRead.ControlFileReads.Maximum,
			row.TransitionRead.StoreReadAttempts.Maximum,
		)
		if err != nil {
			return 0, 0, errors.New("corrected transition read maximum overflows")
		}
		transitionMembers = row.TransitionRead.MemberReads.Maximum
	}
	queryReads, err := checkedInspectionReadSum(
		row.ProductControlFileReads.Maximum,
		row.ProductStoreReadAttempts.Maximum,
	)
	if err != nil {
		return 0, 0, errors.New("corrected product read maximum overflows")
	}
	if row.ProductHTTPCalls.Maximum == 0 && row.ProductMCPCalls.Maximum == 0 {
		queryMembers = 0
	}

	var preparationReads, preparationMembers uint64
	for _, preparation := range correctedRecoveryPreparations() {
		if preparation.Phase != row.Phase {
			continue
		}
		files, stores, boundsErr := recoveryPreparationReadBounds(plan, preparation)
		if boundsErr != nil {
			return 0, 0, boundsErr
		}
		// Maximum preparation admits the optional binding reread and one cold
		// candidate manifest open. That cold open owns the strict projection M.
		preparationReads, err = checkedInspectionReadSum(files.Maximum, stores.Maximum, 1)
		if err != nil {
			return 0, 0, errors.New("corrected recovery preparation read maximum overflows")
		}
		preparationMembers = candidate.MaxWholeRepositoryStrictOpenMemberVisits()
		break
	}
	controls, err := checkedInspectionReadSum(
		xReads, tailReads, finalReads, transitionReads, queryReads, preparationReads,
	)
	if err != nil {
		return 0, 0, errors.New("corrected scoped phase control read maximum overflows")
	}
	members, err := checkedInspectionReadSum(
		finalMembers, transitionMembers, queryMembers, preparationMembers,
	)
	if err != nil {
		return 0, 0, errors.New("corrected scoped phase member read maximum overflows")
	}
	return controls, members, nil
}

func checkedInspectionReadSum(values ...uint64) (uint64, error) {
	var result uint64
	for _, value := range values {
		if value > math.MaxUint64-result {
			return 0, errors.New("corrected inspection read sum overflows")
		}
		result += value
	}
	return result, nil
}

func correctedPhysicalTransitionReadBound(profile CombinedProfile) (inspectionReadBound, error) {
	memberReads, err := checkedMultiply(2, profile.Physical.CombinedPhysicalOwners)
	if err != nil {
		return inspectionReadBound{}, err
	}
	return inspectionReadBound{
		Calls:              exactInspectionCalls(1),
		ControlFileReads:   exactInspectionCalls(correctedPhysicalTransitionControlReads),
		StoreReadAttempts:  exactInspectionCalls(0),
		MemberReads:        exactInspectionCalls(memberReads),
		StoreWriteAttempts: exactInspectionCalls(0),
	}, nil
}

func correctedLogicalTransitionReadBound() (inspectionReadBound, error) {
	storeReads, err := checkedMultiply(
		2, store.ServiceStateV3ActivationTransitionStoreReadAttempts,
	)
	if err != nil {
		return inspectionReadBound{}, err
	}
	return inspectionReadBound{
		Calls:              exactInspectionCalls(2),
		ControlFileReads:   exactInspectionCalls(0),
		StoreReadAttempts:  exactInspectionCalls(storeReads),
		MemberReads:        exactInspectionCalls(0),
		StoreWriteAttempts: exactInspectionCalls(0),
	}, nil
}

func correctedReturnTransitionReadBound() (inspectionReadBound, error) {
	controlReads, err := checkedMultiply(2, correctedReturnTransitionControlReads)
	if err != nil {
		return inspectionReadBound{}, err
	}
	return inspectionReadBound{
		Calls:              exactInspectionCalls(2),
		ControlFileReads:   exactInspectionCalls(controlReads),
		StoreReadAttempts:  exactInspectionCalls(0),
		MemberReads:        exactInspectionCalls(0),
		StoreWriteAttempts: exactInspectionCalls(0),
	}, nil
}

func correctedStaleLeaseTransitionReadBound() (inspectionReadBound, error) {
	controlReads, err := checkedMultiply(2, extractionpublication.StaleLeaseTransitionControlFileReads)
	if err != nil {
		return inspectionReadBound{}, err
	}
	storeReads, err := checkedMultiply(2, store.GenerationStaleLeaseTransitionStoreReadAttempts)
	if err != nil {
		return inspectionReadBound{}, err
	}
	return inspectionReadBound{
		Calls:              exactInspectionCalls(2),
		ControlFileReads:   exactInspectionCalls(controlReads),
		StoreReadAttempts:  exactInspectionCalls(storeReads),
		MemberReads:        exactInspectionCalls(0),
		StoreWriteAttempts: exactInspectionCalls(0),
	}, nil
}

func correctedCheckpointRestartReadBound() (inspectionReadBound, error) {
	controlReads, err := checkedMultiply(2, extractionpublication.CheckpointRestartTransitionControlFileReads)
	if err != nil {
		return inspectionReadBound{}, err
	}
	storeReads, err := checkedMultiply(2, store.GenerationStaleLeaseTransitionStoreReadAttempts)
	if err != nil {
		return inspectionReadBound{}, err
	}
	return inspectionReadBound{
		Calls:              exactInspectionCalls(2),
		ControlFileReads:   exactInspectionCalls(controlReads),
		StoreReadAttempts:  exactInspectionCalls(storeReads),
		MemberReads:        exactInspectionCalls(0),
		StoreWriteAttempts: exactInspectionCalls(0),
	}, nil
}

func correctedPressure80TransitionReadBound() inspectionReadBound {
	return inspectionReadBound{
		Calls:              exactInspectionCalls(lifecycle.Pressure80ReportCalls),
		ControlFileReads:   exactInspectionCalls(0),
		StoreReadAttempts:  exactInspectionCalls(0),
		MemberReads:        exactInspectionCalls(0),
		StoreWriteAttempts: exactInspectionCalls(0),
	}
}

func correctedPressure90TransitionReadBound() inspectionReadBound {
	return inspectionReadBound{
		Calls:              exactInspectionCalls(lifecycle.Pressure90ReportCalls),
		ControlFileReads:   exactInspectionCalls(0),
		StoreReadAttempts:  exactInspectionCalls(0),
		MemberReads:        exactInspectionCalls(0),
		StoreWriteAttempts: exactInspectionCalls(0),
	}
}

func correctedPressure75TransitionReadBound() inspectionReadBound {
	return inspectionReadBound{
		Calls:              exactInspectionCalls(lifecycle.Pressure75ReportCalls),
		ControlFileReads:   exactInspectionCalls(0),
		StoreReadAttempts:  exactInspectionCalls(0),
		MemberReads:        exactInspectionCalls(0),
		StoreWriteAttempts: exactInspectionCalls(0),
	}
}

func correctedArchiveTransitionReadBound() inspectionReadBound {
	return inspectionReadBound{
		Calls:              exactInspectionCalls(recovery.ArchiveTransitionReportCalls),
		ControlFileReads:   exactInspectionCalls(1),
		StoreReadAttempts:  exactInspectionCalls(0),
		MemberReads:        exactInspectionCalls(0),
		StoreWriteAttempts: exactInspectionCalls(0),
	}
}

func correctedLifecycleTransitionReadBound() inspectionReadBound {
	return inspectionReadBound{
		Calls:              exactInspectionCalls(lifecycle.FreshCycleReportCalls),
		ControlFileReads:   exactInspectionCalls(0),
		StoreReadAttempts:  exactInspectionCalls(0),
		MemberReads:        exactInspectionCalls(0),
		StoreWriteAttempts: exactInspectionCalls(0),
	}
}

type epochInspectionInventory struct {
	ServerEpoch                         uint64
	HealthCallsMaximum                  uint64
	ExtractionProgressCallsMaximum      uint64
	TailReadinessCallsMaximum           uint64
	TailControlFileReadsMaximum         uint64
	TailStoreReadAttemptsMaximum        uint64
	FinalAuthorityPassesMaximum         uint64
	LifecycleStatusCallsMaximum         uint64
	TransitionReadCallsMaximum          uint64
	TransitionControlFileReadsMaximum   uint64
	TransitionStoreReadAttemptsMaximum  uint64
	TransitionMemberReadsMaximum        uint64
	TransitionStoreWriteAttemptsMaximum uint64
	ProductHTTPCallsMaximum             uint64
	ProductMCPCallsMaximum              uint64
	ProductControlFileReadsMaximum      uint64
	ProductStoreReadAttemptsMaximum     uint64
	// Includes exact operation reports sharing the server-report ordinal.
	AccountedServerRequestsMaximum uint64
}

type tailReadinessTransition struct {
	Phase        string `json:"phase"`
	Relationship string `json:"relationship"`
	Caller       string `json:"caller"`
}

type tailReadinessIdentity struct {
	RelationshipGenerationSHA256 string
	RelationshipRootSHA256       string
	CallerGenerationSHA256       string
	CallerRootSHA256             string
}

func correctedTailReadinessTransitions() []tailReadinessTransition {
	return []tailReadinessTransition{
		{Phase: "cold", Relationship: "initial", Caller: "initial"},
		{Phase: "warm_noop", Relationship: "equal", Caller: "equal"},
		{Phase: "physical_delta_b", Relationship: "both_differ", Caller: "both_differ"},
		{Phase: "logical_delta_b", Relationship: "both_differ", Caller: "equal"},
		{Phase: "return_a", Relationship: "both_differ", Caller: "both_differ"},
		{Phase: "stale_lease", Relationship: "equal", Caller: "equal"},
		{Phase: "process_restart", Relationship: "equal", Caller: "equal"},
		{Phase: "pressure_80", Relationship: "equal", Caller: "equal"},
		{Phase: "pressure_90", Relationship: "equal", Caller: "equal"},
		{Phase: "pressure_75", Relationship: "equal", Caller: "equal"},
		{Phase: "archive_restore", Relationship: "equal_or_both_differ", Caller: "equal"},
		{Phase: "lifecycle_collection", Relationship: "equal", Caller: "equal"},
		{Phase: "product_queries", Relationship: "equal", Caller: "equal"},
	}
}

func correctedTailReadinessTransitionReady(
	phase string,
	prior *tailReadinessIdentity,
	current tailReadinessIdentity,
) (bool, error) {
	var rule *tailReadinessTransition
	for _, candidate := range correctedTailReadinessTransitions() {
		if candidate.Phase == phase {
			value := candidate
			rule = &value
			break
		}
	}
	if rule == nil {
		return false, errors.New("corrected tail-readiness phase is unknown")
	}
	if rule.Relationship == "initial" {
		return prior == nil, nil
	}
	if prior == nil {
		return false, errors.New("corrected tail-readiness prior authority is absent")
	}
	relationshipEqual := current.RelationshipGenerationSHA256 == prior.RelationshipGenerationSHA256 &&
		current.RelationshipRootSHA256 == prior.RelationshipRootSHA256
	relationshipDiffer := current.RelationshipGenerationSHA256 != prior.RelationshipGenerationSHA256 &&
		current.RelationshipRootSHA256 != prior.RelationshipRootSHA256
	callerEqual := current.CallerGenerationSHA256 == prior.CallerGenerationSHA256 &&
		current.CallerRootSHA256 == prior.CallerRootSHA256
	callerDiffer := current.CallerGenerationSHA256 != prior.CallerGenerationSHA256 &&
		current.CallerRootSHA256 != prior.CallerRootSHA256
	relationshipReady := rule.Relationship == "equal" && relationshipEqual ||
		rule.Relationship == "both_differ" && relationshipDiffer ||
		rule.Relationship == "equal_or_both_differ" && (relationshipEqual || relationshipDiffer)
	callerReady := rule.Caller == "equal" && callerEqual ||
		rule.Caller == "both_differ" && callerDiffer
	return relationshipReady && callerReady, nil
}

func correctedInspectionInventory(profile CombinedProfile) ([]phaseInspectionInventory, []epochInspectionInventory, error) {
	phases := frozenPhaseOrder()
	deadlines := frozenPhaseDeadlines()
	epochs := correctedExecutionServerEpochs()
	if len(phases) != len(deadlines) {
		return nil, nil, errors.New("corrected inspection phase/deadline inventory differs")
	}

	rows := make([]phaseInspectionInventory, len(phases))
	for index, phase := range phases {
		if deadlines[index].Phase != phase {
			return nil, nil, errors.New("corrected inspection deadlines are out of phase order")
		}
		row := phaseInspectionInventory{Phase: phase, TransitionReadClass: "none"}
		if phase != "preflight" && phase != "teardown" {
			row.ExtractionProgressCalls = exactInspectionCalls(1)
			row.FinalAuthorityPasses = exactInspectionCalls(1)
		}
		for _, epoch := range epochs {
			if slices.Contains(epoch.Phases, phase) {
				if row.ServerEpoch != 0 {
					return nil, nil, errors.New("corrected inspection phase belongs to multiple epochs")
				}
				row.ServerEpoch = epoch.ServerEpoch
			}
			if epoch.LaunchPhase == phase {
				maximum, err := maximumInspectionAttempts(frozenSafetyEnvelope().ServerHealthDeadlineMS, correctedHealthPollMS)
				if err != nil {
					return nil, nil, err
				}
				row.HealthCalls = CounterBound{Minimum: 1, Maximum: maximum}
			}
		}
		if slices.Contains([]string{"cold", "physical_delta_b", "logical_delta_b", "return_a", "stale_lease", "process_restart", "archive_restore"}, phase) {
			maximum, err := maximumInspectionAttempts(deadlines[index].DeadlineMS, correctedInspectionPollMS)
			if err != nil {
				return nil, nil, err
			}
			row.ExtractionProgressCalls.Maximum = maximum
		}
		if phase != "preflight" && phase != "teardown" {
			row.TailReadinessCalls = row.ExtractionProgressCalls
			controls, err := checkedMultiply(correctedTailControlReads, row.TailReadinessCalls.Maximum)
			if err != nil {
				return nil, nil, errors.New("corrected tail-readiness control reads overflow")
			}
			stores, err := checkedMultiply(correctedTailStoreReads, row.TailReadinessCalls.Maximum)
			if err != nil {
				return nil, nil, errors.New("corrected tail-readiness store reads overflow")
			}
			row.TailControlFileReads = CounterBound{Minimum: correctedTailControlReads, Maximum: controls}
			row.TailStoreReadAttempts = CounterBound{Minimum: correctedTailStoreReads, Maximum: stores}
		}
		switch phase {
		case "warm_noop":
			row.ImmutableMemberReusePhase = "cold"
		case "physical_delta_b":
			row.TransitionReadClass = correctedPhysicalTransitionReadClass
			readBound, err := correctedPhysicalTransitionReadBound(profile)
			if err != nil {
				return nil, nil, err
			}
			row.TransitionRead = &readBound
		case "logical_delta_b":
			row.TransitionReadClass = correctedLogicalTransitionReadClass
			readBound, err := correctedLogicalTransitionReadBound()
			if err != nil {
				return nil, nil, err
			}
			row.TransitionRead = &readBound
		case "return_a":
			row.TransitionReadClass = correctedReturnTransitionReadClass
			readBound, err := correctedReturnTransitionReadBound()
			if err != nil {
				return nil, nil, err
			}
			row.TransitionRead = &readBound
		case "stale_lease":
			row.TransitionReadClass = correctedStaleLeaseTransitionReadClass
			readBound, err := correctedStaleLeaseTransitionReadBound()
			if err != nil {
				return nil, nil, err
			}
			row.TransitionRead = &readBound
			row.ImmutableMemberReusePhase = "return_a"
		case "process_restart":
			beforeEpoch, _, ok := expectedPhaseRuntime(epochs, "stale_lease")
			if !ok {
				return nil, nil, errors.New("corrected checkpoint restart prior epoch is absent")
			}
			row.TransitionReadClass = correctedCheckpointRestartReadClass
			readBound, err := correctedCheckpointRestartReadBound()
			if err != nil {
				return nil, nil, err
			}
			row.TransitionRead = &readBound
			row.TransitionReadEpochs = []uint64{beforeEpoch, row.ServerEpoch}
		case "archive_restore":
			row.TransitionReadClass = correctedArchiveTransitionReadClass
			readBound := correctedArchiveTransitionReadBound()
			row.TransitionRead = &readBound
		case "pressure_80", "pressure_75", "lifecycle_collection":
			maximum, err := maximumInspectionAttempts(deadlines[index].DeadlineMS, correctedInspectionPollMS)
			if err != nil {
				return nil, nil, err
			}
			row.LifecycleStatusCalls = CounterBound{Minimum: 1, Maximum: maximum}
			switch phase {
			case "pressure_80":
				row.TransitionReadClass = correctedPressure80TransitionReadClass
				readBound := correctedPressure80TransitionReadBound()
				row.TransitionRead = &readBound
				row.ImmutableMemberReusePhase = "process_restart"
			case "pressure_75":
				row.TransitionReadClass = correctedPressure75TransitionReadClass
				readBound := correctedPressure75TransitionReadBound()
				row.TransitionRead = &readBound
				row.ImmutableMemberReusePhase = "pressure_90"
			case "lifecycle_collection":
				row.TransitionReadClass = correctedLifecycleTransitionReadClass
				readBound := correctedLifecycleTransitionReadBound()
				row.TransitionRead = &readBound
				row.ImmutableMemberReusePhase = "archive_restore"
			}
		case "pressure_90":
			row.TransitionReadClass = correctedPressure90TransitionReadClass
			readBound := correctedPressure90TransitionReadBound()
			row.TransitionRead = &readBound
			row.ImmutableMemberReusePhase = "pressure_80"
		case "product_queries":
			calls, err := correctedProductQueryCalls()
			if err != nil {
				return nil, nil, err
			}
			controlReads, storeReads, err := correctedProductQueryNativeControlReads()
			if err != nil {
				return nil, nil, err
			}
			row.FinalAuthorityPasses = exactInspectionCalls(2)
			row.ProductHTTPCalls, row.ProductMCPCalls = exactInspectionCalls(calls), exactInspectionCalls(calls)
			row.ProductControlFileReads = exactInspectionCalls(controlReads)
			row.ProductStoreReadAttempts = exactInspectionCalls(storeReads)
			row.ImmutableMemberReusePhase = "lifecycle_collection"
		}
		if phase != "preflight" && phase != "teardown" && row.ServerEpoch == 0 {
			return nil, nil, errors.New("corrected inspection operational phase lacks an epoch")
		}
		rows[index] = row
	}

	epochRows := make([]epochInspectionInventory, len(epochs))
	for index, epoch := range epochs {
		value := epochInspectionInventory{ServerEpoch: epoch.ServerEpoch}
		for _, row := range rows {
			if row.ServerEpoch == epoch.ServerEpoch {
				for destination, add := range map[*uint64]uint64{
					&value.HealthCallsMaximum:              row.HealthCalls.Maximum,
					&value.ExtractionProgressCallsMaximum:  row.ExtractionProgressCalls.Maximum,
					&value.TailReadinessCallsMaximum:       row.TailReadinessCalls.Maximum,
					&value.TailControlFileReadsMaximum:     row.TailControlFileReads.Maximum,
					&value.TailStoreReadAttemptsMaximum:    row.TailStoreReadAttempts.Maximum,
					&value.FinalAuthorityPassesMaximum:     row.FinalAuthorityPasses.Maximum,
					&value.LifecycleStatusCallsMaximum:     row.LifecycleStatusCalls.Maximum,
					&value.ProductHTTPCallsMaximum:         row.ProductHTTPCalls.Maximum,
					&value.ProductMCPCallsMaximum:          row.ProductMCPCalls.Maximum,
					&value.ProductControlFileReadsMaximum:  row.ProductControlFileReads.Maximum,
					&value.ProductStoreReadAttemptsMaximum: row.ProductStoreReadAttempts.Maximum,
				} {
					if add > math.MaxUint64-*destination {
						return nil, nil, errors.New("corrected inspection epoch inventory overflows")
					}
					*destination += add
				}
			}
			ownsTransition, parts := row.ServerEpoch == epoch.ServerEpoch, uint64(1)
			if len(row.TransitionReadEpochs) > 0 {
				ownsTransition = slices.Contains(row.TransitionReadEpochs, epoch.ServerEpoch)
				parts = uint64(len(row.TransitionReadEpochs))
			}
			if row.TransitionRead == nil || !ownsTransition {
				continue
			}
			for destination, bound := range map[*uint64]CounterBound{
				&value.TransitionReadCallsMaximum:          row.TransitionRead.Calls,
				&value.TransitionControlFileReadsMaximum:   row.TransitionRead.ControlFileReads,
				&value.TransitionStoreReadAttemptsMaximum:  row.TransitionRead.StoreReadAttempts,
				&value.TransitionMemberReadsMaximum:        row.TransitionRead.MemberReads,
				&value.TransitionStoreWriteAttemptsMaximum: row.TransitionRead.StoreWriteAttempts,
			} {
				if bound.Minimum != bound.Maximum || bound.Maximum%parts != 0 {
					return nil, nil, errors.New("corrected inspection transition epoch split is not exact")
				}
				add := bound.Maximum / parts
				if add > math.MaxUint64-*destination {
					return nil, nil, errors.New("corrected inspection epoch transition inventory overflows")
				}
				*destination += add
			}
		}
		for _, add := range []uint64{
			value.ExtractionProgressCallsMaximum, value.TailReadinessCallsMaximum,
			value.FinalAuthorityPassesMaximum, value.LifecycleStatusCallsMaximum,
			value.TransitionReadCallsMaximum,
			value.ProductHTTPCallsMaximum, value.ProductMCPCallsMaximum,
		} {
			if add > math.MaxUint64-value.AccountedServerRequestsMaximum {
				return nil, nil, errors.New("corrected inspection request inventory overflows")
			}
			value.AccountedServerRequestsMaximum += add
		}
		epochRows[index] = value
	}
	return rows, epochRows, nil
}

func exactInspectionCalls(value uint64) CounterBound {
	return CounterBound{Minimum: value, Maximum: value}
}

func maximumInspectionAttempts(deadlineMS, cadenceMS uint64) (uint64, error) {
	if deadlineMS == 0 || cadenceMS == 0 {
		return 0, errors.New("corrected inspection deadline or cadence is zero")
	}
	ticks := deadlineMS / cadenceMS
	if ticks == math.MaxUint64 {
		return 0, errors.New("corrected inspection attempt bound overflows")
	}
	return ticks + 1, nil
}

func correctedProductQueryCalls() (uint64, error) {
	var calls uint64
	for _, query := range correctedQueryCases() {
		pages := correctedProductQueryPages(query)
		if pages > math.MaxUint64-calls {
			return 0, errors.New("corrected product query inventory overflows")
		}
		calls += pages
	}
	return calls, nil
}

func correctedProductQueryPages(query QueryCase) uint64 {
	if query.PageSize == 0 {
		return 1
	}
	pages := query.ExpectedRecords / query.PageSize
	if query.ExpectedRecords%query.PageSize != 0 {
		pages++
	}
	return max(uint64(1), pages)
}

// correctedProductQueryNativeControlReads derives C/S for the exclusive,
// plan-ordered HTTP-then-MCP corridor. Startup's current relationship pin has
// already warmed that shared generation; only catalog root/member miss once.
func correctedProductQueryNativeControlReads() (uint64, uint64, error) {
	var controls, stores uint64
	catalogCold := true
	for _, query := range correctedQueryCases() {
		pages := correctedProductQueryPages(query)
		var perTransportControls, perTransportStores uint64
		switch query.Surface {
		case "all_code_search":
			perTransportControls, perTransportStores = 2, 2
		case "service_detail":
			perTransportStores = 7
		case "service_search":
			if query.ExpectedStatus == 404 {
				perTransportStores = 1
			} else {
				perTransportControls, perTransportStores = 16, 11
			}
		case "service_relationships":
			continuations := pages - 1
			continuationControls, err := checkedMultiply(2, continuations)
			if err != nil || continuationControls > math.MaxUint64-5 {
				return 0, 0, errors.New("corrected relationship control reads overflow")
			}
			continuationStores, err := checkedMultiply(3, continuations)
			if err != nil || continuationStores > math.MaxUint64-4 {
				return 0, 0, errors.New("corrected relationship store reads overflow")
			}
			perTransportControls = 5 + continuationControls
			perTransportStores = 4 + continuationStores
		default:
			return 0, 0, errors.New("corrected product query surface is unknown")
		}
		if perTransportControls > (math.MaxUint64-controls)/2 ||
			perTransportStores > (math.MaxUint64-stores)/2 {
			return 0, 0, errors.New("corrected product query native reads overflow")
		}
		controls += 2 * perTransportControls
		stores += 2 * perTransportStores
		if catalogCold && (query.Surface == "service_detail" ||
			query.Surface == "service_search" && query.ExpectedStatus != 404) {
			if stores > math.MaxUint64-4 {
				return 0, 0, errors.New("corrected product query catalog reads overflow")
			}
			stores += 4
			catalogCold = false
		}
	}
	if catalogCold {
		return 0, 0, errors.New("corrected product query inventory never opens the catalog")
	}
	return controls, stores, nil
}

// correctedProductQueryMemberReadMaximum follows the closed execution order:
// all plan-ordered HTTP cases, then all plan-ordered MCP cases. The first
// authorized catalog route owns the only catalog member miss; relationship
// members remain fresh per page and per transport under their route ceilings.
func correctedProductQueryMemberReadMaximum(queries []QueryCase) (uint64, error) {
	catalogMemberMaximum, err := checkedInspectionReadSum(
		servicecatalogv3.MaxServicesPerMember,
		servicecatalogv3.MaxMemberships,
	)
	if err != nil {
		return 0, errors.New("corrected product catalog member maximum overflows")
	}
	relationshipMembers, err := checkedMultiply(
		relationshippublication.MaxProjectionRecords,
		relationshippublication.MaxProjectionBucketsV3,
	)
	if err != nil {
		return 0, errors.New("corrected product relationship member maximum overflows")
	}

	var total uint64
	catalogCold := true
	for range 2 { // HTTP corridor, then MCP corridor.
		for _, query := range queries {
			switch query.Surface {
			case "all_code_search":
				continue
			case "service_detail", "service_search":
				if query.ExpectedStatus == 404 || !catalogCold {
					continue
				}
				if catalogMemberMaximum > math.MaxUint64-total {
					return 0, errors.New("corrected product catalog member total overflows")
				}
				total += catalogMemberMaximum
				catalogCold = false
			case "service_relationships":
				kind := ""
				for _, parameter := range query.Parameters {
					if parameter.Name != "kind" {
						continue
					}
					if kind != "" {
						return 0, errors.New("corrected product relationship kind is duplicated")
					}
					kind = parameter.Value
				}
				var postings uint64
				switch kind {
				case "rpc":
					postings = rpccallerposting.MaxPostingsPerMember
				case "kafka":
					postings = kafkatopicposting.MaxPostingsPerMember
				default:
					return 0, errors.New("corrected product relationship kind is unknown")
				}
				perPage, sumErr := checkedInspectionReadSum(relationshipMembers, postings)
				if sumErr != nil {
					return 0, errors.New("corrected product relationship page maximum overflows")
				}
				pages := correctedProductQueryPages(query)
				queryMembers, multiplyErr := checkedMultiply(perPage, pages)
				if multiplyErr != nil {
					return 0, errors.New("corrected product relationship query maximum overflows")
				}
				queryMembers, sumErr = checkedInspectionReadSum(
					queryMembers,
					relationshippublication.MaxServicesPerServiceMemberV3,
				)
				if sumErr != nil || queryMembers > math.MaxUint64-total {
					return 0, errors.New("corrected product relationship total overflows")
				}
				total += queryMembers
			default:
				return 0, errors.New("corrected product member surface is unknown")
			}
		}
	}
	if catalogCold {
		return 0, errors.New("corrected product member inventory never opens the catalog")
	}
	return total, nil
}

func correctedInspectionInventorySHA256(profile CombinedProfile) (string, error) {
	phases, epochs, err := correctedInspectionInventory(profile)
	if err != nil {
		return "", err
	}
	return canonicalSHA256(struct {
		Schema              string                     `json:"schema"`
		Policy              string                     `json:"policy"`
		HealthCadenceMS     uint64                     `json:"health_cadence_ms"`
		InspectionCadenceMS uint64                     `json:"inspection_cadence_ms"`
		Phases              []phaseInspectionInventory `json:"phases"`
		Epochs              []epochInspectionInventory `json:"epochs"`
		TailTransitions     []tailReadinessTransition  `json:"tail_transitions"`
	}{correctedInspectionInventorySchema, correctedInspectionPolicy,
		correctedHealthPollMS, correctedInspectionPollMS,
		phases, epochs, correctedTailReadinessTransitions()})
}
