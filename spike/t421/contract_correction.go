package t421

import (
	"errors"
	"reflect"
	"slices"
)

const (
	combinedProfileV2Schema = "t421-combined-corpus-profile-v2"
	retainedPlanSHA256      = "sha256:96ba209147858c8f38b922fcaf8766dc6d796051d2e8b0999960ed2e114faf34"
)

// ContractCorrection is prospective. The retained v1 artifact and validator
// remain exact; these derivations neither authorize execution nor report one.
type ContractCorrection struct {
	SupersedesSHA256          string                    `json:"supersedes_sha256"`
	IdentityDerivations       []IdentityDerivation      `json:"identity_derivations"`
	ChildBudgets              []PhaseBudgetDerivation   `json:"child_budgets"`
	AuthorGitCommands         []ExecutionCommandProfile `json:"author_git_commands"`
	RecoveryPreparations      []RecoveryPreparation     `json:"recovery_preparations"`
	ObservationPolicy         string                    `json:"observation_policy"`
	SourceReadSemantics       string                    `json:"source_read_semantics"`
	LogicalUpdatePolicy       string                    `json:"logical_update_policy"`
	StartupDeadlineDerivation string                    `json:"startup_deadline_derivation"`
	NativeGitAdmissionPolicy  string                    `json:"native_git_admission_policy"`
	ProcessAccountingPolicy   string                    `json:"process_accounting_policy"`
	ReadAccountingPolicy      string                    `json:"read_accounting_policy"`
	RequiredReadiness         []string                  `json:"required_readiness"`
}

type RecoveryPreparation struct {
	Phase                       string       `json:"phase"`
	Precondition                string       `json:"precondition"`
	ScheduleAction              string       `json:"schedule_action"`
	ControlAction               string       `json:"control_action"`
	Preservation                string       `json:"preservation"`
	LockOrder                   []string     `json:"lock_order"`
	ScheduleWrites              uint64       `json:"schedule_writes"`
	Chunks                      uint64       `json:"chunks"`
	Starts                      uint64       `json:"starts"`
	Successes                   uint64       `json:"successes"`
	Requeues                    uint64       `json:"requeues"`
	PreparationCompletionWrites uint64       `json:"preparation_completion_writes"`
	PreparationDeletes          uint64       `json:"preparation_deletes"`
	RecoveryCompletionWrites    uint64       `json:"recovery_completion_writes"`
	RecoveryRootInstalls        uint64       `json:"recovery_root_installs"`
	PublicationCalls            CounterBound `json:"publication_calls"`
	Derivation                  string       `json:"derivation"`
}

func correctedPhaseStates() []PhaseState {
	states := frozenPhaseStates()
	for index := range states {
		switch states[index].Phase {
		case "stale_lease":
			states[index].ObservationAction = "reuse"
			states[index].RecoveryPreparation = "predecessor-bound-same-generation-stale-lease-reuse"
		case "process_restart":
			states[index].ObservationAction = "reuse"
			states[index].RecoveryPreparation = "predecessor-bound-result-preserving-checkpoint-reuse"
		case "archive_restore":
			states[index].CatalogAction = "restore_exact_binding_reuse"
		}
	}
	return states
}

func correctedRecoveryPreparations() []RecoveryPreparation {
	preparations := make([]RecoveryPreparation, 0, 2)
	var chunks uint64
	for _, domain := range frozenExtractionDomains() {
		chunks += domain.ApplicablePartitions
	}
	for _, phase := range []string{"stale_lease", "process_restart"} {
		value := RecoveryPreparation{
			Phase: phase, Precondition: "complete-a-return-authority-and-settled-current-extraction-schedule",
			ScheduleAction: "Runtime.enqueue:one-predecessor-bound-operational-schedule;same-immutable-generation;no-old-job-reset-or-delete",
			ControlAction:  "none",
			Preservation:   "all-canonical-results,sealed-evidence,plans,generation,store-domain-roots,prior-roots,relationship-roots-and-pins",
			LockOrder:      []string{"repository-reconcile-shard", "target-domain-assembly", "exclusive-publication-mutation-fence"},
			ScheduleWrites: 1, Chunks: chunks, Starts: chunks + 1, Successes: chunks, Requeues: 1,
			PublicationCalls: CounterBound{Minimum: chunks, Maximum: chunks},
			Derivation:       "chunks=sum(frozen-domain-partitions);starts=chunks+one-reaped-lease;successes=chunks;successor-attempts=0;each-publication-call-includes-native-exact-row-and-chunk-recounts",
		}
		if phase == "process_restart" {
			var targetPartitions uint64
			for _, domain := range frozenExtractionDomains() {
				if domain.Domain == "proto-contract" {
					targetPartitions = domain.ApplicablePartitions
				}
			}
			value.ControlAction = "preserve-proto-contract-result-ordinal-2;clear-only-completion-bit-2-and-decrement-count-in-existing-file;remove-only-local-domain-root-and-local-current-pointer;sync;then-enqueue"
			value.PreparationCompletionWrites, value.PreparationDeletes = 1, 2
			value.RecoveryCompletionWrites, value.RecoveryRootInstalls = 1, 1
			value.PublicationCalls.Minimum = chunks - targetPartitions + 1
			value.Derivation += ";minimum-publication-calls=chunks-target-domain-partitions+one-final-assembly;maximum=chunks;preparation=one-bitmap-write+two-exact-control-removals;recovery=one-bitmap-write+one-root-install"
		}
		preparations = append(preparations, value)
	}
	return preparations
}

func applyContractCorrection(plan *Plan) error {
	plan.Profile.Schema = combinedProfileV2Schema
	plan.Profile.Bytes.CombinedObservationInputBytes = plan.Profile.Bytes.StructuralDeclaredGoBytes - plan.Profile.Bytes.StructuralNonCandidateBytes + plan.Profile.Bytes.OverlayGoBytes
	plan.Profile.Bytes.CombinedNonObservationBytes = plan.Profile.Bytes.CombinedLogicalSourceBytes - plan.Profile.Bytes.CombinedObservationInputBytes
	var err error
	plan.Revisions, err = revisionHistoryForScope(plan.Profile, plan.Revisions.Logical, true)
	if err != nil {
		return err
	}
	return applyExecutionCorrection(plan)
}

func applyExecutionCorrection(plan *Plan) error {
	plan.PhaseStates = correctedPhaseStates()
	plan.FailurePoints = frozenFailurePoints()
	for index := range plan.FailurePoints {
		point := &plan.FailurePoints[index]
		if point.Name == "checkpointed_hard_restart" {
			point.Boundary = "after_existing_durable_partition_checkpoint_validation_before_domain_assembly"
			point.PartialResidue = "durable_partition_result_with_completion_bit_clear_and_local_root_absent"
		}
	}
	work, budgets, err := correctedWorkEnvelope(plan.Profile)
	if err != nil {
		return err
	}
	plan.WorkEnvelope = work
	for index := range plan.WorkEnvelope.Phases {
		row := &plan.WorkEnvelope.Phases[index]
		state := plan.PhaseStates[index]
		var reuse uint64
		for _, action := range []string{state.SourceAction, state.SearchAction, state.ObservationAction, state.CatalogAction, state.RelationshipAction} {
			if action == "reuse" {
				reuse++
			}
		}
		row.ReuseDecisions = CounterBound{Minimum: reuse, Maximum: reuse}
		switch row.Phase {
		case "cold":
			row.ObservationParses = CounterBound{Minimum: plan.Profile.Pipeline.SupportedGoFiles, Maximum: plan.Profile.Pipeline.SupportedGoFiles}
		case "physical_delta_b", "return_a":
			row.ObservationParses = CounterBound{Maximum: plan.Profile.Pipeline.SupportedGoFiles * plan.SafetyEnvelope.MaximumRetriesPerUnit}
		}
		for _, preparation := range correctedRecoveryPreparations() {
			if preparation.Phase != row.Phase {
				continue
			}
			row.JobAttempts.Minimum = preparation.Starts
			row.PublicationWrites = preparation.PublicationCalls
		}
	}
	plan.MeterPolicy = frozenMeterPolicy()
	plan.MeterPolicy.Schema = "t422-combined-meter-policy-v2"
	plan.MeterPolicy.Authority = "t4013-resource-gauge-contract-plus-t422-bounded-executable-image-epoch-ledger"
	plan.MeterPolicy.ProcessGaugeSemantics = "peak_rss=max_coherent_root_and_descendant_resident_bytes_during_phase;children=cumulative_admitted_descendant_executable_image_epochs;an_exec_image_change_counts_even_with_same_pid;not_peak_concurrency"
	plan.ToolPolicy = frozenToolPolicy()
	plan.ToolPolicy.RequiredTools = append(plan.ToolPolicy.RequiredTools, "sh")
	slices.Sort(plan.ToolPolicy.RequiredTools)
	plan.ReceiptContract = frozenReceiptContract()
	plan.ReceiptContract.Schema = "t422-combined-convergence-receipt-v2"
	plan.ReceiptContract.RequiredMetrics = correctedReceiptMetricNames()
	plan.MeterPolicy.RequiredMetricsSHA256 = recipeDigest("t422-required-metrics-v2", plan.ReceiptContract.RequiredMetrics...)
	plan.Claims = frozenClaims()
	plan.Claims.ChangesProductionBehavior = false
	plan.Correction = &ContractCorrection{
		SupersedesSHA256:    retainedPlanSHA256,
		IdentityDerivations: frozenIdentityDerivations(), ChildBudgets: budgets, AuthorGitCommands: correctedAuthorGitCommands(),
		RecoveryPreparations:      correctedRecoveryPreparations(),
		ObservationPolicy:         "ordinary-go-source-policy:go-only;IDL-remains-in-candidate-and-extraction-inventories;parses-count-native-ParsedBlobs-events-not-inventory-records;unchanged-members-and-content-cache-hits-do-not-parse",
		SourceReadSemantics:       "git_reads=native-git-object-content-reads;index_files=zoekt-go-git-input-offers;census_children=ordinary-catalog-ls-tree-invocations;census_records=regular-tree-records-classified-by-those-invocations;all-Git-executable-image-epochs-including-census-and-watcher-count-in-git-role;no-metadata-census-is-control-only-or-free",
		LogicalUpdatePolicy:       "new-operator-catalog-version-requires-new-config-and-ordinary-restart;epochs-derived-from-phase-revision-changes-plus-hard-restart-and-restore;one-live-server-at-a-time;physical-return-and-logical-return-share-one-restart;no-live-reload-claim",
		StartupDeadlineDerivation: "each-new-epoch-inherits-SafetyEnvelope.ServerHealthDeadlineMS-from-retained-T40/T41-host-readiness-policy;no-deadline-increase;phase-and-total-deadlines-still-apply",
		NativeGitAdmissionPolicy:  "resolve-and-hash-actual-native-Git-image-not-Apple-launcher-shim;prove-builtin-aliases-resolve-to-that-image;admit-transport-shell-separately-as-sh-tool/git-transport-shell-role;Git-helper-slots=upload-pack,pack-objects,one-of-index-pack-or-unpack-objects,rev-list,maintenance;record-packed-and-loose-object-posture-after-clone-and-each-fetch-under-unchanged-admitted-Git-config;any-extra-helper-or-image-refuses;no-gc-disable-or-fetch-flag-change",
		ProcessAccountingPolicy:   "T42-requires-new-bounded-admitted-image-epoch-accounting-before-runner-readiness;retained-T40-8192-image-cap-and-validators-remain-exact;stream-bounded-events-with-checked-cumulative-counts;enforce-closed-role-budgets-and-resource-gauges-before-work;no-relabeling-missed-processes-as-zero",
		ReadAccountingPolicy:      "v2-actual-scoped-events:inspection,readiness,native-preparation,public-query;not-all-pipeline-I/O;T40-topology-proxy-is-not-events;control=file-control-read-attempt-or-read-only-store-query-attempt;metadata-probes-excluded;prep-files=24+4*domains+partitions+checkpoint+cold-open+[0,1]-binding-reread;prep-queries=domains+10+four-schedule-reads*native-store-attempts[1,64];phase-controls=prep-files+prep-queries+other-scoped-controls;unchanged-phase-cap-includes-all-subtotals;member=decoded-artifact-or-projection-spool-record-visit-including-rereads;warm-prep-members=0;cold-open<=1;store-writes=attempted-enqueue-transactions[1,64];schedule-writes=one-native-enqueue-call;no-read-query-is-a-write-transaction",
		RequiredReadiness:         []string{"production-constructor-derived-completed-fixture-accepted", "post-logical-restart-authorized-search-caller-and-relationship-reads:zero-resolver-and-caller-materialization;Git-children-exactly-observed-watcher-plus-census-plus-frozen-startup-commands", "result-preserving-preparation-and-checkpoint-reuse-counterexamples", "actual-scoped-read-event-ledger-and-complete-nonpreparation-inspection-budget-within-unchanged-phase-caps-before-execution", "fresh-independent-exact-source-review-before-new-freeze"},
	}
	return nil
}

func correctedReceiptMetricNames() []string {
	return append(slices.Clone(receiptMetricNames), "census_children", "census_records")
}

func validatePlanExecutionContract(plan Plan) error {
	want := Plan{
		PhaseOrder: frozenPhaseOrder(), PhaseStates: frozenPhaseStates(), PhaseDeadlines: frozenPhaseDeadlines(),
		FailurePoints: frozenFailurePoints(), SafetyEnvelope: frozenSafetyEnvelope(), WorkEnvelope: frozenWorkEnvelope(plan.Profile),
		MeterPolicy: frozenMeterPolicy(), ToolPolicy: frozenToolPolicy(), SealPolicy: frozenSealPolicy(), StopRules: frozenStopRules(),
		Teardown: frozenTeardownRule(), ReceiptContract: frozenReceiptContract(), Claims: frozenClaims(), Profile: plan.Profile,
	}
	if plan.Schema == PlanV2Schema {
		if err := applyExecutionCorrection(&want); err != nil {
			return err
		}
	}
	if !reflect.DeepEqual(plan.PhaseOrder, want.PhaseOrder) || !reflect.DeepEqual(plan.PhaseStates, want.PhaseStates) ||
		!reflect.DeepEqual(plan.PhaseDeadlines, want.PhaseDeadlines) || !reflect.DeepEqual(plan.FailurePoints, want.FailurePoints) ||
		!reflect.DeepEqual(plan.SafetyEnvelope, want.SafetyEnvelope) || !reflect.DeepEqual(plan.WorkEnvelope, want.WorkEnvelope) ||
		plan.MeterPolicy != want.MeterPolicy || !reflect.DeepEqual(plan.ToolPolicy, want.ToolPolicy) ||
		!reflect.DeepEqual(plan.SealPolicy, want.SealPolicy) || !reflect.DeepEqual(plan.StopRules, want.StopRules) ||
		plan.Teardown != want.Teardown || !reflect.DeepEqual(plan.ReceiptContract, want.ReceiptContract) ||
		plan.Claims != want.Claims || !reflect.DeepEqual(plan.Correction, want.Correction) {
		return errors.New("T42.1 execution contract differs from the frozen plan")
	}
	return nil
}
