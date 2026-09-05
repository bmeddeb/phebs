package t421

import (
	"errors"
	"math"
	"slices"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

const (
	executionRootProducer  = uint32(1)
	executionProducerCount = 11
	executionPhaseCount    = 15
	executionRolePhebs     = uint32(5)
	executionRoleAuthor    = uint32(6)
	executionRoleHdiutil   = uint32(7)
	executionSiteServe     = uint32(1001)
	executionSiteBackup    = uint32(1002)
	executionSiteRestore   = uint32(1003)
	executionSiteAuthor    = uint32(1004)
	executionSiteDetach    = uint32(1005)

	// Selected HEAD-only server and closed ceremony HTTP recipes: at most two held direct children per
	// complete ordinary owner/request turn, plus its one persistent engine.
	// This conservative construction ceiling is not an attainable peak, nor
	// a bound for additional indexed revisions or arbitrary token-authorized
	// HTTP/MCP requests (including unmarked legacy JSON-RPC batches).
	executionServerOwners        = 29
	executionServerRequests      = 1
	executionHeldChildrenPerTurn = 2
	executionActivePerProducer   = executionHeldChildrenPerTurn*(executionServerOwners+executionServerRequests) + 1
)

var errExecutionDispatchFlow = errors.New("T42.2 fixed operational dispatch flow is invalid")

func executionRootSites() []dispatchadmission.Site {
	return []dispatchadmission.Site{
		{ID: executionSiteServe, Role: executionRolePhebs, Persistent: true},
		{ID: executionSiteBackup, Role: executionRolePhebs},
		{ID: executionSiteRestore, Role: executionRolePhebs},
		{ID: executionSiteAuthor, Role: executionRoleAuthor},
		{ID: executionSiteDetach, Role: executionRoleHdiutil},
	}
}

// These are the actual fixed lifetimes, not permissions to launch in every
// listed phase. The owner still checks the current phase at each direct start.
// Epoch three includes its prepared phase-eight hit before intentional death.
func executionProducerPhases(producer uint32) []uint32 {
	switch producer {
	case executionRootProducer:
		return []uint32{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	case 2:
		return []uint32{2, 3, 4}
	case 3:
		return []uint32{5}
	case 4:
		return []uint32{6, 7, 8}
	case 5:
		return []uint32{8, 9, 10, 11}
	case 6:
		return []uint32{12, 13, 14}
	case 7:
		return []uint32{2}
	case 8:
		return []uint32{4}
	case 9:
		return []uint32{6}
	case 10, 11:
		return []uint32{12}
	default:
		return nil
	}
}

func executionDispatchRole(name string) uint32 {
	switch name {
	case "git":
		return dispatchadmission.RoleGit
	case "surreal":
		return dispatchadmission.RoleSurreal
	case "zoekt-git-index":
		return dispatchadmission.RoleZoekt
	case "compatibility":
		return dispatchadmission.RoleCompatibility
	case "phebs":
		return executionRolePhebs
	case "t422-author":
		return executionRoleAuthor
	case "hdiutil":
		return executionRoleHdiutil
	default:
		return 0
	}
}

// executionDispatchConfig projects the unchanged V3 operational role budgets
// onto the concrete eleven-producer topology. It creates no controller, tool,
// child or private admission binding and selects no PC01/preparation/signing
// budget. Actual bootstrap, fixed start recipes and cleanup remain mandatory.
func executionDispatchConfig(plan Plan, bindings [executionProducerCount][32]byte) (dispatchadmission.Config, error) {
	if plan.Schema != PlanV3Schema || plan.ProcessAccounting == nil ||
		len(plan.PhaseOrder) != executionPhaseCount || validatePlan(plan, &plan.Revisions) != nil ||
		len(plan.ProcessAccounting.DispatchBudgets) != executionPhaseCount {
		return dispatchadmission.Config{}, errExecutionDispatchFlow
	}
	config := dispatchadmission.Config{}
	checkpointSlots := uint64(0)
	siteCount := 0
	for index, binding := range bindings {
		if binding == ([32]byte{}) || slices.Contains(bindings[:index], binding) {
			return dispatchadmission.Config{}, errExecutionDispatchFlow
		}
		id := uint32(index + 1)
		var sites []dispatchadmission.Site
		switch {
		case id == executionRootProducer:
			sites = executionRootSites()
		case id >= 7 && id <= 9:
			sites = dispatchadmission.AuthorSites()
		default:
			sites = dispatchadmission.ProductionSites()
		}
		config.Producers = append(config.Producers, dispatchadmission.Producer{ID: id, Binding: binding, Sites: sites})
		siteCount += len(sites)
		checkpointSlots += uint64(len(executionProducerPhases(id)))
	}
	var total uint64
	for index, budget := range plan.ProcessAccounting.DispatchBudgets {
		if budget.Phase != plan.PhaseOrder[index] || len(budget.Roles) != 8 || budget.MaximumAttempts > math.MaxUint64-total {
			return dispatchadmission.Config{}, errExecutionDispatchFlow
		}
		phase := dispatchadmission.Phase{ID: uint32(index + 1)}
		var seen [8]bool
		for _, bound := range budget.Roles {
			role := executionDispatchRole(bound.Name)
			if role == 0 {
				// The frozen focused-index role remains an explicit zero in the
				// receipt, not a permission for this whole-repository executor.
				if bound.Name != "phebs-focused-index" || bound.Minimum != 0 || bound.Maximum != 0 {
					return dispatchadmission.Config{}, errExecutionDispatchFlow
				}
				continue
			}
			if seen[role] || bound.Minimum != 0 {
				return dispatchadmission.Config{}, errExecutionDispatchFlow
			}
			seen[role] = true
			phase.Roles = append(phase.Roles, dispatchadmission.RoleBudget{Role: role, Attempts: bound.Maximum})
		}
		if len(phase.Roles) != 7 {
			return dispatchadmission.Config{}, errExecutionDispatchFlow
		}
		slices.SortFunc(phase.Roles, func(a, b dispatchadmission.RoleBudget) int { return int(a.Role) - int(b.Role) })
		config.Phases = append(config.Phases, phase)
		total += budget.MaximumAttempts
	}
	// Two pairs per admission (admit+settle), at most one checkpoint per
	// configured producer-phase, ten one-time carried handles (five root-held
	// servers and their five native engines), eleven terminal closes and one
	// possible unused idle/EOF reservation per producer. Serial recovery
	// engines join within phase twelve and never carry. Some checkpoint slots
	// are unused by terminal-only producers; that is a ceiling, not an opcode
	// grant. PC01/PB01, stdio and outer-stage commands are separate budgets.
	pairs, err := checkedMultiply(total, 2)
	if err == nil {
		pairs, err = checkedInspectionReadSum(pairs, checkpointSlots, 10, executionProducerCount, executionProducerCount)
	}
	var wire uint64
	if err == nil {
		wire, err = checkedMultiply(pairs, 2*dispatchadmission.FrameBytes)
	}
	if err != nil || total == 0 || siteCount != 120 || checkpointSlots != 34 {
		return dispatchadmission.Config{}, errExecutionDispatchFlow
	}
	config.Limits = dispatchadmission.Limits{
		Producers: executionProducerCount, Sites: siteCount, Roles: 7, Phases: executionPhaseCount,
		ActivePerProducer: executionActivePerProducer, Attempts: total, WireBytes: wire, AckTimeout: 5 * time.Second,
	}
	return config, nil
}
