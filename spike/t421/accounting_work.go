package t421

import (
	"errors"
	"math"
	"slices"
	"strings"
)

// PhaseDispatchBudget limits accepted repository-controlled dispatch attempts,
// not successful starts or native descendant executions. Semantic phase and
// server-start requirements remain independent of these zero-minimum counters.
type PhaseDispatchBudget struct {
	Phase           string               `json:"phase"`
	Roles           []RoleBound          `json:"roles"`
	Terms           []DispatchBudgetTerm `json:"terms"`
	MaximumAttempts uint64               `json:"maximum_attempts"`
}

// DispatchBudgetTerm is a checked work-unit product, never measured evidence.
type DispatchBudgetTerm struct {
	Name                string `json:"name"`
	Role                string `json:"role"`
	Unit                string `json:"unit"`
	Units               uint64 `json:"units"`
	AttemptsPerUnit     uint64 `json:"attempts_per_unit"`
	MaximumUnitAttempts uint64 `json:"maximum_unit_attempts"`
	MaximumAttempts     uint64 `json:"maximum_attempts"`
}

func controlledDispatchBudgets(profile CombinedProfile) ([]PhaseDispatchBudget, error) {
	work, derivations, err := correctedWorkEnvelope(profile)
	if err != nil {
		return nil, err
	}
	budgets := make([]PhaseDispatchBudget, len(derivations))
	for index, derivation := range derivations {
		budget := &budgets[index]
		budget.Phase, budget.Terms = derivation.Phase, []DispatchBudgetTerm{}
		for _, role := range []string{
			"compatibility", "git", "hdiutil", "phebs", "phebs-focused-index",
			"surreal", "t422-author", "zoekt-git-index",
		} {
			budget.Roles = append(budget.Roles, RoleBound{Name: role})
		}
		add := func(role, name, unit string, units, perUnit, ownerAttempts uint64) error {
			term, termErr := dispatchBudgetTerm(role, name, unit, units, perUnit, ownerAttempts)
			if termErr != nil {
				return termErr
			}
			return budget.addTerm(term)
		}
		for _, term := range derivation.GitTerms {
			switch term.Name {
			case "native_git_upload_pack", "native_git_pack_objects",
				"native_git_one_of_index_or_unpack_objects", "native_git_rev_list", "native_git_maintenance":
				continue
			}
			if strings.HasPrefix(term.Name, "native_") {
				return nil, errors.New("controlled dispatch cannot infer a new native-helper allowance")
			}
			if err := add("git", term.Name, term.Unit, term.Units, term.ChildrenPerUnit, term.MaximumAttempts); err != nil {
				return nil, err
			}
		}
		for _, role := range work.Phases[index].ChildProcessRoles {
			if role.Maximum == 0 {
				continue
			}
			switch role.Name {
			case "t422-author", "zoekt-git-index", "hdiutil":
				// The retained fixed author/detach commands and per-generation
				// index retry ceiling already describe these direct callsites.
				if err := add(role.Name, role.Name+"_dispatch", "phase_command_recipe", 1, 1, role.Maximum); err != nil {
					return nil, err
				}
			}
		}
		if derivation.ServerEpochStarts != 0 {
			for _, command := range []struct{ role, name string }{
				{"phebs", "serve"}, {"surreal", "serve_version"}, {"surreal", "serve_engine"},
			} {
				if err := add(command.role, command.name, "server_epoch", derivation.ServerEpochStarts, 1, 1); err != nil {
					return nil, err
				}
			}
		}
		if budget.Phase == "archive_restore" {
			// Create verifies the live tool then exports. Restore verifies the
			// archive tool, starts/imports one engine, stops it, then opens a
			// second engine for schema/authority validation. The pinned digest
			// disables the supervisor's version cache, so both opens probe.
			for _, command := range []struct{ role, name string }{
				{"phebs", "backup"}, {"phebs", "restore"},
				{"surreal", "backup_version"}, {"surreal", "backup_export"},
				{"surreal", "restore_archive_version"}, {"surreal", "restore_import_version"},
				{"surreal", "restore_import_engine"}, {"surreal", "restore_import"},
				{"surreal", "restore_validation_version"}, {"surreal", "restore_validation_engine"},
			} {
				if err := add(command.role, command.name, "archive_restore_command_recipe", 1, 1, 1); err != nil {
					return nil, err
				}
			}
		}
	}
	return budgets, nil
}

func dispatchBudgetTerm(role, name, unit string, units, perUnit, ownerAttempts uint64) (DispatchBudgetTerm, error) {
	if role == "" {
		return DispatchBudgetTerm{}, errors.New("dispatch budget role is absent")
	}
	checked, err := childBudgetTerm(name, unit, units, perUnit, ownerAttempts)
	if err != nil {
		return DispatchBudgetTerm{}, errors.New("dispatch budget product is invalid or overflows")
	}
	return DispatchBudgetTerm{
		Name: name, Role: role, Unit: unit, Units: units, AttemptsPerUnit: perUnit,
		MaximumUnitAttempts: ownerAttempts, MaximumAttempts: checked.MaximumChildren,
	}, nil
}

func (budget *PhaseDispatchBudget) addTerm(term DispatchBudgetTerm) error {
	checked, err := dispatchBudgetTerm(term.Role, term.Name, term.Unit, term.Units, term.AttemptsPerUnit, term.MaximumUnitAttempts)
	roleIndex := slices.IndexFunc(budget.Roles, func(role RoleBound) bool { return role.Name == term.Role })
	if err != nil || checked != term || roleIndex < 0 ||
		term.MaximumAttempts > math.MaxUint64-budget.MaximumAttempts ||
		term.MaximumAttempts > math.MaxUint64-budget.Roles[roleIndex].Maximum ||
		slices.ContainsFunc(budget.Terms, func(prior DispatchBudgetTerm) bool { return prior.Name == term.Name }) {
		return errors.New("dispatch budget term is unknown, duplicated, invalid or overflows")
	}
	budget.Roles[roleIndex].Maximum += term.MaximumAttempts
	budget.MaximumAttempts += term.MaximumAttempts
	budget.Terms = append(budget.Terms, term)
	return nil
}

// ProductionDispatchSite identifies one actual repository-owned dispatch
// boundary. It does not inventory launches inside native tools or dependencies.
// The indexer has one dispatch boundary with two configured role alternatives.
type ProductionDispatchSite struct {
	Tag      string   `json:"tag"`
	Roles    []string `json:"roles"`
	Path     string   `json:"path"`
	Callsite string   `json:"callsite"`
}

func productionDispatchSites() []ProductionDispatchSite {
	return []ProductionDispatchSite{
		{"candidate_tree", []string{"git"}, "internal/candidate/build.go", "walkTree"},
		{"compatibility_sandbox", []string{"compatibility"}, "internal/compat/sandbox_unix.go", "runSandboxed"},
		{"extract_subtree", []string{"git"}, "internal/extract/corpus.go", "(*gitCorpus).anyRegularUnder"},
		{"extract_tree", []string{"git"}, "internal/extract/corpus.go", "(*gitCorpus).walkTree"},
		{"git_blob_batch", []string{"git"}, "internal/gitobj/batch.go", "NewBatchBlobReader"},
		{"git_output", []string{"git"}, "internal/gitobj/gitobj.go", "Output"},
		{"index_build", []string{"phebs-focused-index", "zoekt-git-index"}, "internal/indexer/indexer.go", "(*Indexer).Index"},
		{"recovery_surreal", []string{"surreal"}, "internal/recovery/recovery.go", "runSurreal"},
		{"repository_tree", []string{"git"}, "internal/repositoryindex/census.go", "startTreeStream"},
		{"service_catalog_census", []string{"git"}, "internal/servicecatalogingest/ingest.go", "(*Reconciler).censusValidated"},
		{"source_partition_batch", []string{"git"}, "internal/sourcepartition/reader.go", "(*Plan).ReadPartition"},
		{"surreal_engine", []string{"surreal"}, "internal/store/child.go", "startEngine"},
		{"surreal_version", []string{"surreal"}, "internal/store/child.go", "inspectSurrealBinary"},
		{"sync_git", []string{"git"}, "internal/sync/git.go", "runGit"},
		{"sync_git_history", []string{"git"}, "internal/sync/githistory.go", "runGitLimited"},
		{"sync_git_read", []string{"git"}, "internal/sync/gitread.go", "runGitRaw"},
	}
}
