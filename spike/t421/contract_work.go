package t421

import (
	"errors"
	"math"
	"slices"

	"github.com/bmeddeb/phebs/internal/sourcepartition"
)

const PhaseBudgetDerivationSchema = "t422-phase-child-budget-derivation-v1"

// PhaseBudgetDerivation separates cumulative Git executable-image epochs by
// cause. A future executor must count every admitted descendant image,
// including watcher polls; none of these fields describes the maximum number
// of simultaneous processes.
type PhaseBudgetDerivation struct {
	Schema                  string            `json:"schema"`
	Phase                   string            `json:"phase"`
	DeadlineMS              uint64            `json:"deadline_ms"`
	ServerEpochStarts       uint64            `json:"server_epoch_starts"`
	SyncIntentSources       []string          `json:"sync_intent_sources"`
	CatalogCensusChildren   CounterBound      `json:"catalog_census_children"`
	CatalogCensusRecords    CounterBound      `json:"catalog_census_records"`
	NativeSourceReadMaximum uint64            `json:"native_source_read_maximum"`
	NativeSourceReadFormula string            `json:"native_source_read_formula"`
	TransportShellTerm      *ChildBudgetTerm  `json:"transport_shell_term,omitempty"`
	GitTerms                []ChildBudgetTerm `json:"git_terms"`
	MaximumGitChildren      uint64            `json:"maximum_git_children"`
	MaximumChildrenAllRoles uint64            `json:"maximum_children_all_roles"`
}

// correctedWorkEnvelope changes the prospective accounting contract only.
// Resource, retry, transaction-row, partition, and lifecycle safety ceilings
// remain those of the retained v1 envelope. Each child bound counts admitted
// descendant executable-image epochs, including allowed retries and same-PID
// exec transitions, rather than kernel lifetimes or peak live concurrency.
func correctedWorkEnvelope(profile CombinedProfile) (WorkEnvelope, []PhaseBudgetDerivation, error) {
	if err := checkRetainedWorkArithmetic(profile); err != nil {
		return WorkEnvelope{}, nil, err
	}
	contentTerms, err := correctedFullPassGitTerms(profile)
	if err != nil {
		return WorkEnvelope{}, nil, err
	}
	work := frozenWorkEnvelope(profile)
	work.Schema = "t422-phase-work-envelope-v2"
	work.LifecycleOwners = correctedLifecycleOwners()
	work.ChildProcessRoles = slices.Insert(work.ChildProcessRoles, 1, "git-transport-shell")
	for index := range work.Phases {
		work.Phases[index].ChildProcessRoles = slices.Insert(work.Phases[index].ChildProcessRoles, 1, RoleBound{Name: "git-transport-shell"})
	}
	work.MaximumChildProcessesPerPhase = 0
	runtime := frozenExecutionRuntime(Plan{})
	states := correctedPhaseStates()
	deadlines := frozenPhaseDeadlines()
	epochs := correctedExecutionServerEpochs()
	derivations := make([]PhaseBudgetDerivation, 0, len(work.Phases))
	for index := range work.Phases {
		value := &work.Phases[index]
		state := states[index]
		if value.Phase != state.Phase || value.Phase != deadlines[index].Phase {
			return WorkEnvelope{}, nil, errors.New("corrected child budget phase sources differ")
		}
		derivation := PhaseBudgetDerivation{
			Schema: PhaseBudgetDerivationSchema, Phase: value.Phase,
			DeadlineMS:        deadlines[index].DeadlineMS,
			SyncIntentSources: []string{}, GitTerms: []ChildBudgetTerm{},
		}
		for _, epoch := range epochs {
			if epoch.LaunchPhase == value.Phase {
				derivation.ServerEpochStarts++
			}
		}
		fullPass := state.SourceAction == "author" || state.SourceAction == "advance_one_file" || state.SourceAction == "return_one_file"
		add := func(name, unit string, units, children, attempts uint64) error {
			term, termErr := childBudgetTerm(name, unit, units, children, attempts)
			if termErr != nil {
				return termErr
			}
			derivation.GitTerms = append(derivation.GitTerms, term)
			return nil
		}
		if fullPass {
			derivation.GitTerms = append(derivation.GitTerms, contentTerms...)
			readMaximum, readErr := correctedNativeSourceReadMaximum(profile)
			if readErr != nil {
				return WorkEnvelope{}, nil, readErr
			}
			value.GitReads.Maximum = readMaximum
			derivation.NativeSourceReadMaximum = readMaximum
			derivation.NativeSourceReadFormula = "go_files*generation_attempts+(resolver_blobs+nongenerated_base_go_paths)*store_runner_attempts+sum(domain_candidate_records+typed_partitions)*generation_attempts;zoekt-go-git-offers-count-only-in-index_files"
			authorCommands := uint64(len(correctedAuthorGitCommands()))
			if state.SourceAction != "author" {
				authorCommands-- // Existing bare repository: no second init.
			}
			if err := add("revision_author", "closed_author_git_command", authorCommands, 1, 1); err != nil {
				return WorkEnvelope{}, nil, err
			}
		}
		if derivation.ServerEpochStarts > 0 {
			// EnqueueMissing and the first ordinary watcher tick are distinct
			// intents; they can coalesce, but the budget does not assume that.
			derivation.SyncIntentSources = append(derivation.SyncIntentSources, "startup_enqueue_missing", "watcher_initial_baseline")
			if value.Phase != "cold" {
				// The single existing mirror has one origin and no push URL.
				// ReconcileArtifacts still performs both bounded config reads.
				if err := add("startup_remote_metadata", "existing_origin", derivation.ServerEpochStarts, 2, 1); err != nil {
					return WorkEnvelope{}, nil, err
				}
			}
		} else if state.SourceAction == "advance_one_file" {
			derivation.SyncIntentSources = append(derivation.SyncIntentSources, "watcher_physical_head_change")
		}
		syncIntents := uint64(len(derivation.SyncIntentSources))
		if syncIntents > 0 {
			// mirrorLocked uses set-url + fetch (cold clone is cheaper),
			// followSourceHead uses two symbolic-ref commands, and the final
			// default-branch read uses one. One sync attempt can submit an
			// index intent; every such index attempt resolves HEAD once.
			if err := add("ordinary_sync_metadata", "admitted_sync_intent", syncIntents, 5, runtime.StoreRunnerMaxAttempts); err != nil {
				return WorkEnvelope{}, nil, err
			}
			if err := add("ordinary_index_head", "admitted_sync_intent", syncIntents, runtime.StoreRunnerMaxAttempts, runtime.StoreRunnerMaxAttempts); err != nil {
				return WorkEnvelope{}, nil, err
			}
			// These command roles resolve to the admitted real Git image;
			// they are not necessarily separate executables. A changed
			// fetch can unpack a small pack rather than index a large one,
			// and both clone/fetch can run the connectivity rev-list.
			// Auto-maintenance is admitted once, not recursively exempted:
			// any additional helper or image refuses exact admission.
			for _, helper := range []string{"upload_pack", "pack_objects", "one_of_index_or_unpack_objects", "rev_list", "maintenance"} {
				if err := add("native_git_"+helper, "admitted_sync_intent", syncIntents, 1, runtime.StoreRunnerMaxAttempts); err != nil {
					return WorkEnvelope{}, nil, err
				}
			}
			// file:// transport may start the exact admitted shell, which
			// then execs upload-pack. The shell epoch counts independently
			// even if the kernel process identity is unchanged by exec.
			shellTerm, shellErr := childBudgetTerm("native_git_transport_shell", "admitted_sync_intent", syncIntents, 1, runtime.StoreRunnerMaxAttempts)
			if shellErr != nil {
				return WorkEnvelope{}, nil, shellErr
			}
			derivation.TransportShellTerm = &shellTerm
			setCorrectedRole(value, "git-transport-shell", 0, shellTerm.MaximumChildren)
		}
		if fullPass {
			// A successful generation short-circuits subsequent index and
			// candidate callbacks. These are per-generation retry ceilings,
			// not a multiplied allowance per service or source file.
			for _, command := range []string{"index_source_census", "index_zoekt_name_config", "candidate_commit_type", "candidate_tree_census"} {
				if err := add(command, "physical_generation", 1, 1, runtime.StoreRunnerMaxAttempts); err != nil {
					return WorkEnvelope{}, nil, err
				}
			}
			setCorrectedRole(value, "zoekt-git-index", 1, runtime.StoreRunnerMaxAttempts)
		}
		if value.Phase == "archive_restore" {
			// Full restore discards candidate controls, extraction domain
			// authority, resolver pointers, and caller outcomes/admissions.
			// Its ordinary workers reconstruct those from the same source.
			// The source/search and Go observation archives remain exact:
			// neither a new index child nor observation parsing is needed.
			for _, term := range contentTerms {
				if term.Name != "observation_batch_reads" {
					derivation.GitTerms = append(derivation.GitTerms, term)
				}
			}
			for _, command := range []string{"candidate_commit_type", "candidate_tree_census"} {
				if err := add(command, "restored_candidate_generation", 1, 1, runtime.StoreRunnerMaxAttempts); err != nil {
					return WorkEnvelope{}, nil, err
				}
			}
			readMaximum, readErr := correctedNativeSourceReadMaximumFor(profile, false)
			if readErr != nil {
				return WorkEnvelope{}, nil, readErr
			}
			value.GitReads = CounterBound{Minimum: 1, Maximum: readMaximum}
			value.ResolverBlobReads = CounterBound{Minimum: profile.Pipeline.ResolverBlobReadsPerBuild, Maximum: profile.Pipeline.ResolverBlobReadsPerBuild}
			value.ResolverBlobBytes = CounterBound{Minimum: profile.Pipeline.ResolverBlobBytesPerBuild, Maximum: profile.Pipeline.ResolverBlobBytesPerBuild}
			derivation.NativeSourceReadMaximum = readMaximum
			derivation.NativeSourceReadFormula = "(resolver_blobs+nongenerated_base_go_paths)*store_runner_attempts+sum(domain_candidate_records+typed_partitions)*generation_attempts;restored-search-and-Go-observations-reused"
		}
		if catalogBindingTransition(state.CatalogAction) {
			// The accepted path permits one serialized successful re-ingest.
			// Later callbacks return current without a census. A second scan
			// is measured and refuses this envelope; the per-job retry limit
			// does not invent a bound for calls from unrelated job owners.
			if err := add("catalog_binding_census", "selected_binding_transition", 1, 1, 1); err != nil {
				return WorkEnvelope{}, nil, err
			}
			value.CensusChildren = CounterBound{Minimum: 1, Maximum: 1}
			value.CensusRecords = CounterBound{Minimum: profile.Physical.CombinedRegularFiles, Maximum: profile.Physical.CombinedRegularFiles}
			derivation.CatalogCensusChildren, derivation.CatalogCensusRecords = value.CensusChildren, value.CensusRecords
		}
		if value.Phase != "preflight" {
			// Teardown also owns any final watcher tick before cancellation.
			// Offline backup/restore periods reduce this allowance; epochs
			// never overlap, so they do not multiply the phase deadline.
			watcher, watcherErr := correctedWatcherChildTerm(derivation.DeadlineMS)
			if watcherErr != nil {
				return WorkEnvelope{}, nil, watcherErr
			}
			derivation.GitTerms = append(derivation.GitTerms, watcher)
			if value.Phase != "cold" {
				// A prior slow Git child can delay delivery of one ticker
				// event across the reset. Its immediate delivery and the
				// next aligned tick need not be three seconds apart. Only
				// the inherited watcher can carry that one event; a newly
				// started watcher first waits a complete interval.
				if err := add("delayed_watcher_tick_at_phase_boundary", "single_inherited_watcher", 1, 1, 1); err != nil {
					return WorkEnvelope{}, nil, err
				}
			}
		}
		if value.Phase == "logical_delta_b" {
			value.ResolverBlobReads, value.ResolverBlobBytes = CounterBound{}, CounterBound{}
			value.GitReads = CounterBound{}
		}
		if value.Phase == "physical_delta_b" {
			// A remains the production root's prior generation after A-to-B.
			// Both lifecycle turns therefore protect it and delete nothing.
			value.LifecycleDeleted = CounterBound{}
		}
		if derivation.ServerEpochStarts > 0 {
			starts := derivation.ServerEpochStarts
			if value.Phase == "archive_restore" {
				starts += 2 // One backup command and one restore command.
			}
			setCorrectedRole(value, "phebs", starts, starts)
			setCorrectedRole(value, "surreal", starts, starts)
		}
		derivation.MaximumGitChildren, err = sumChildBudgetTerms(derivation.GitTerms)
		if err != nil {
			return WorkEnvelope{}, nil, err
		}
		minimumGit := uint64(0)
		if fullPass || value.CensusChildren.Minimum > 0 {
			minimumGit = 1
		}
		setCorrectedRole(value, "git", minimumGit, derivation.MaximumGitChildren)
		for _, role := range value.ChildProcessRoles {
			if role.Maximum > math.MaxUint64-derivation.MaximumChildrenAllRoles {
				return WorkEnvelope{}, nil, errors.New("phase child role sum overflows")
			}
			derivation.MaximumChildrenAllRoles += role.Maximum
		}
		work.MaximumChildProcessesPerPhase = max(work.MaximumChildProcessesPerPhase, derivation.MaximumChildrenAllRoles)
		derivations = append(derivations, derivation)
	}
	return work, derivations, nil
}

func catalogBindingTransition(action string) bool {
	return slices.Contains([]string{
		"publish_actual_source_binding", "rebind_actual_source",
		"replace_one_service_with_chunk_recovery", "replace_content_return",
	}, action)
}

func setCorrectedRole(value *PhaseWorkBounds, name string, minimum, maximum uint64) {
	for index := range value.ChildProcessRoles {
		if value.ChildProcessRoles[index].Name == name {
			value.ChildProcessRoles[index].Minimum, value.ChildProcessRoles[index].Maximum = minimum, maximum
			return
		}
	}
}

// Check the expressions used by frozenWorkEnvelope before calling that exact
// retained implementation. It intentionally remains byte-for-byte unchanged.
func checkRetainedWorkArithmetic(profile CombinedProfile) error {
	regular, partitions := profile.Physical.CombinedRegularFiles, profile.Physical.CombinedModeledPartitions
	goFiles, idlFiles := profile.Pipeline.SupportedGoFiles, profile.Pipeline.SupportedIDLFiles
	services := profile.Logical.AcceptedServices
	if regular == 0 || partitions > math.MaxUint64-regular || regular+partitions > math.MaxUint64/2 ||
		idlFiles > math.MaxUint64-goFiles || partitions > math.MaxUint64-(goFiles+idlFiles) ||
		goFiles+idlFiles+partitions > math.MaxUint64/frozenSafetyEnvelope().MaximumRetriesPerUnit ||
		services >= math.MaxUint64/6 ||
		profile.Pipeline.ServiceReferences > math.MaxUint64-(regular+partitions) {
		return errors.New("retained work arithmetic is invalid or overflows")
	}
	return nil
}

// Native blob events are not child events: a batch child can read many blobs.
// The indexer's go-git input offers remain in IndexFiles, not GitReads. Each
// extraction domain owns its selected candidates and optional typed artifact;
// observation owns Go only, independently of the IDL extraction inventories.
func correctedNativeSourceReadMaximum(profile CombinedProfile) (uint64, error) {
	return correctedNativeSourceReadMaximumFor(profile, true)
}

func correctedNativeSourceReadMaximumFor(profile CombinedProfile, observation bool) (uint64, error) {
	if profile.Pipeline.GeneratedMappings > profile.Pipeline.SupportedGoFiles {
		return 0, errors.New("native source budget has an invalid caller population")
	}
	runtime := frozenExecutionRuntime(Plan{})
	definitions := []struct {
		name     string
		units    uint64
		attempts uint64
	}{
		{name: "resolver", units: profile.Pipeline.ResolverBlobReadsPerBuild, attempts: runtime.StoreRunnerMaxAttempts},
		{name: "caller", units: profile.Pipeline.SupportedGoFiles - profile.Pipeline.GeneratedMappings, attempts: runtime.StoreRunnerMaxAttempts},
	}
	if observation {
		definitions = append(definitions, struct {
			name     string
			units    uint64
			attempts uint64
		}{name: "observation", units: profile.Pipeline.SupportedGoFiles, attempts: runtime.GenerationMaxAttempts})
	}
	for _, domain := range profile.Pipeline.ExtractionDomains {
		if domain.TypedPartitions > math.MaxUint64-domain.CandidateRecords {
			return 0, errors.New("native source domain inventory overflows")
		}
		definitions = append(definitions, struct {
			name     string
			units    uint64
			attempts uint64
		}{name: domain.Domain, units: domain.CandidateRecords + domain.TypedPartitions, attempts: runtime.GenerationMaxAttempts})
	}
	terms := make([]ChildBudgetTerm, 0, len(definitions))
	for _, definition := range definitions {
		term, err := childBudgetTerm(definition.name, "native_blob_read", definition.units, 1, definition.attempts)
		if err != nil {
			return 0, err
		}
		terms = append(terms, term)
	}
	return sumChildBudgetTerms(terms)
}

// ChildBudgetTerm is a checked product, not a measured event count. Its named
// units and attempt ceiling make each prospective allowance independently
// recomputable without deriving evidence from an expected receipt.
type ChildBudgetTerm struct {
	Name            string `json:"name"`
	Unit            string `json:"unit"`
	Units           uint64 `json:"units"`
	ChildrenPerUnit uint64 `json:"children_per_unit"`
	MaximumAttempts uint64 `json:"maximum_attempts"`
	MaximumChildren uint64 `json:"maximum_children"`
}

// correctedExecutionServerEpochs follows the selected catalog versions. The
// ordinary server reads configuration once, so a changed logical selection is
// a new serve epoch even when the physical source remains current. Backup and
// restore are commands, not additional concurrently running serve epochs.
func correctedExecutionServerEpochs() []ExecutionServerEpochProfile {
	var epochs []ExecutionServerEpochProfile
	var logicalRevision string
	for _, state := range correctedPhaseStates() {
		if state.Phase == "preflight" || state.Phase == "teardown" {
			continue
		}
		launch := len(epochs) == 0 || state.Phase == "process_restart" ||
			state.Phase == "archive_restore" || state.LogicalRevision != logicalRevision
		if launch {
			epochs = append(epochs, ExecutionServerEpochProfile{
				ServerEpoch: uint64(len(epochs)) + 1, LaunchPhase: state.Phase,
			})
		}
		epochs[len(epochs)-1].Phases = append(epochs[len(epochs)-1].Phases, state.Phase)
		logicalRevision = state.LogicalRevision
	}
	return epochs
}

// correctedAuthorGitCommands is a prospective closed command inventory, not
// a claim that an author executable has already been implemented or admitted.
// Cold uses all four commands; each subsequent physical revision uses the last
// three once. Fast-import receives only that revision, never future commits.
func correctedAuthorGitCommands() []ExecutionCommandProfile {
	return []ExecutionCommandProfile{
		{Name: "init_bare", ToolRole: "git", EnvironmentClass: "recovery", NormalizedArgv: []string{"init", "--bare", "--initial-branch=main", "@source"}},
		{Name: "import_revision", ToolRole: "git", EnvironmentClass: "recovery", NormalizedArgv: []string{"-C", "@source", "fast-import", "--quiet", "--date-format=raw"}},
		{Name: "verify_revision", ToolRole: "git", EnvironmentClass: "recovery", NormalizedArgv: []string{"-C", "@source", "rev-parse", "HEAD", "HEAD^{tree}"}},
		{Name: "verify_tree_inventory", ToolRole: "git", EnvironmentClass: "recovery", NormalizedArgv: []string{"-C", "@source", "ls-tree", "-rz", "--full-tree", "HEAD"}},
	}
}

// correctedFullPassGitTerms covers source-content work only. Authoring, sync,
// metadata/census commands, queries, and watcher polls are separate terms; an
// execution budget must not treat this subtotal as its complete Git budget.
func correctedFullPassGitTerms(profile CombinedProfile) ([]ChildBudgetTerm, error) {
	if profile.Pipeline.SupportedGoFiles == 0 ||
		profile.Pipeline.GeneratedMappings > profile.Pipeline.SupportedGoFiles ||
		profile.Pipeline.ResolverBlobReadsPerBuild == 0 ||
		profile.Physical.CombinedModeledPartitions == 0 {
		return nil, errors.New("combined child budget has an invalid workload")
	}
	runtime := frozenExecutionRuntime(Plan{})
	// Resolver materialization opens each frozen module/control/generated blob
	// with one gitobj.ReadBlob child. The direct gRPC caller opens every base Go
	// path except those generated descriptors; Thrift has no resolver and opens
	// no blobs. The exact caller population is checked by the retained replay.
	callerReads := profile.Pipeline.SupportedGoFiles - profile.Pipeline.GeneratedMappings
	// Observation hash buckets are not sized solely by ceil(files/4096): hash,
	// placement, and byte splits can produce smaller nonempty members. Every
	// member has at least one Go input, and the production aggregate member cap
	// supplies the other independent ceiling. Each member uses one batch child.
	observationMembers := min(profile.Pipeline.SupportedGoFiles, uint64(sourcepartition.MaxAggregateMembers))
	definitions := []struct {
		name, unit string
		units      uint64
		attempts   uint64
	}{
		{name: "resolver_blob_materialization", unit: "frozen_resolver_blob", units: profile.Pipeline.ResolverBlobReadsPerBuild, attempts: runtime.StoreRunnerMaxAttempts},
		{name: "caller_blob_materialization", unit: "nongenerated_base_go_path", units: callerReads, attempts: runtime.StoreRunnerMaxAttempts},
		{name: "observation_batch_reads", unit: "nonempty_go_source_member", units: observationMembers, attempts: runtime.GenerationMaxAttempts},
		{name: "extraction_batch_reads", unit: "frozen_extraction_partition", units: profile.Physical.CombinedModeledPartitions, attempts: runtime.GenerationMaxAttempts},
	}
	terms := make([]ChildBudgetTerm, 0, len(definitions))
	for _, definition := range definitions {
		term, err := childBudgetTerm(definition.name, definition.unit, definition.units, 1, definition.attempts)
		if err != nil {
			return nil, err
		}
		terms = append(terms, term)
	}
	return terms, nil
}

// correctedWatcherChildTerm covers the ordinary three-second tick alignment
// using ceil(deadline/interval). The caller separately accounts for the single
// delayed delivery that an inherited watcher can carry across a phase reset.
func correctedWatcherChildTerm(deadlineMS uint64) (ChildBudgetTerm, error) {
	if deadlineMS == 0 {
		return ChildBudgetTerm{}, errors.New("watcher child budget requires a phase deadline")
	}
	const intervalMS = uint64(3_000)
	ticks := deadlineMS / intervalMS
	if deadlineMS%intervalMS != 0 {
		ticks++
	}
	return childBudgetTerm("ordinary_watcher", "three_second_tick", ticks, 1, 1)
}

func childBudgetTerm(name, unit string, units, childrenPerUnit, attempts uint64) (ChildBudgetTerm, error) {
	if name == "" || unit == "" || childrenPerUnit == 0 || attempts == 0 ||
		units > math.MaxUint64/childrenPerUnit ||
		units*childrenPerUnit > math.MaxUint64/attempts {
		return ChildBudgetTerm{}, errors.New("child budget term is invalid or overflows")
	}
	return ChildBudgetTerm{
		Name: name, Unit: unit, Units: units, ChildrenPerUnit: childrenPerUnit,
		MaximumAttempts: attempts, MaximumChildren: units * childrenPerUnit * attempts,
	}, nil
}

func sumChildBudgetTerms(terms []ChildBudgetTerm) (uint64, error) {
	var total uint64
	for _, term := range terms {
		checked, err := childBudgetTerm(term.Name, term.Unit, term.Units, term.ChildrenPerUnit, term.MaximumAttempts)
		if err != nil || checked != term || term.MaximumChildren > math.MaxUint64-total {
			return 0, errors.New("child budget sum is invalid or overflows")
		}
		total += term.MaximumChildren
	}
	return total, nil
}
