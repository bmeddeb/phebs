//go:build linux

package t421

import (
	"strings"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

// Descriptors prepare protocol choices, not resource or successor authority.
type linuxEpochRecipe uint8

const (
	linuxEpochLegacyV11 linuxEpochRecipe = iota + 1
	linuxEpochCheckpointV12
	linuxEpochLegacyPolicySHA256 = "sha256:0300a26c359a9f4526cf9542617682d2c51152a8e51bd2995d81ab1b18b850e5"
)

func (recipe linuxEpochRecipe) policy() (linuxEpochResourcePolicy, bool) {
	switch recipe {
	case linuxEpochLegacyV11:
		return linuxEpochPolicyV11(), true
	case linuxEpochCheckpointV12:
		policy := linuxEpochPolicyV11()
		policy.Schema = "t42-linux-epoch-operational-resources-v12"
		policy.IndexRuntime = "prepared-native-checkpoint;server-producers=2,3:resource-issued-authenticated-git-and-zoekt-binding=t42-linux-foreground-git-serial-index-v2;server-producers=4,5:resource-issued-authenticated-git-and-zoekt-binding=t42-linux-foreground-git-serial-index-v3;exact-existing-HEAD-index-argv-then-one-parallelism=1-before-final-repository;index-child-only-GOGC=50;one-child-and-synchronous-shard-builds;current-bindings-compared-before-every-DA-Start;unchanged-whole-RSS-and-workload-deadlines;no-selection-or-fit-promise"
		policy.EngineMeasurement = strings.Replace(policy.EngineMeasurement,
			"authenticated-EM01-FD7-only-with-existing-FD6;server-producers=2,3,4;maximum-points=6,1,2;existing-phases=2-3-4,5,6;",
			"prepared-FD7-only-with-existing-FD6;EM01-server-producers=2,3-maximum-points=6,1-existing-phases=2-3-4,5;EM02-server-producers=4,5-maximum-points=7,1;producer4-terminal-phase8;producer5-measure-phase8-control-phases8-11;paired-runtime-V3;wire-bytes712,232-within-existing-combined-ceiling;no-backup-or-archive-carry;descriptor-alone-issues-no-measurement-checkpoint-or-successor-authority;", 1)
		policy.Tracing = strings.Replace(policy.Tracing, "completed-three-server-ring-and-map-backing", "prepared-four-server-ring-and-map-backing", 1)
		return policy, true
	default:
		return linuxEpochResourcePolicy{}, false
	}
}

// Epoch four is described only. Its issued native context/listener release and
// checkpoint entry remain unavailable; no constructor issues this descriptor.
func (recipe linuxEpochRecipe) engine(epoch uint64) (string, uint32) {
	if recipe != linuxEpochLegacyV11 && recipe != linuxEpochCheckpointV12 {
		return "", 0
	}
	switch epoch {
	case 1:
		return "", 6
	case 2:
		return "", 1
	case 3:
		if recipe == linuxEpochCheckpointV12 {
			return dispatchadmission.EngineMeasurementCheckpointV2, 7
		}
		return "", 2
	case 4:
		if recipe == linuxEpochCheckpointV12 {
			return dispatchadmission.EngineMeasurementCheckpointV2, 1
		}
	}
	return "", 0
}

func (recipe linuxEpochRecipe) runtime(epoch uint64) string {
	engine, maximum := recipe.engine(epoch)
	if maximum == 0 {
		return ""
	}
	return epochEngineRuntimeRecipe(engine)
}

// Only the existing coordinator constructor issues an identity, always v11.
// The shared pointer binds admission, tool choices and trace issuance to it.
type linuxEpochRecipeIdentity struct {
	coordinator *linuxEpochCoordinator
	recipe      linuxEpochRecipe
	policySHA   string
}

func issueLinuxEpochLegacyRecipe(adapter *linuxEpochCoordinator) *linuxEpochRecipeIdentity {
	return &linuxEpochRecipeIdentity{coordinator: adapter, recipe: linuxEpochLegacyV11, policySHA: linuxEpochPolicySHA256()}
}

func (identity *linuxEpochRecipeIdentity) matches(adapter *linuxEpochCoordinator) bool {
	return identity != nil && adapter != nil && identity.coordinator == adapter && adapter.recipe == identity &&
		identity.recipe == linuxEpochLegacyV11 && identity.policySHA == linuxEpochLegacyPolicySHA256
}

func linuxEpochRecipeForHeld(held *linuxEpochHeldFlow) *linuxEpochRecipeIdentity {
	if held == nil || held.flow == nil || held.flow.held != held || held.flow.core == nil {
		return nil
	}
	adapter, ok := held.flow.core.native.(*linuxEpochCoordinator)
	if !ok || adapter == nil || adapter.held != held || !adapter.issued() {
		return nil
	}
	return adapter.recipe
}

func (a *linuxEpochResourceAdmission) recipeBound() bool {
	return a != nil && a.recipe != nil && linuxEpochRecipeForHeld(a.held) == a.recipe && a.policySHA == a.recipe.policySHA
}
