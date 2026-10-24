//go:build linux

package t421

import (
	"encoding/json"
	"strings"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

// Descriptors prepare protocol choices, not resource or successor authority.
type linuxEpochRecipe uint8

const (
	linuxEpochLegacyV11 linuxEpochRecipe = iota + 1
	linuxEpochCheckpointV12
	linuxEpochCheckpointV13
	linuxEpochCheckpointPolicySHA256 = "sha256:8e863962e2a5ddab6e6f8ec847eb595a58defd1a27bb7b5827d82e28f8d6ccc5"
	linuxEpochLegacyPolicySHA256     = "sha256:0300a26c359a9f4526cf9542617682d2c51152a8e51bd2995d81ab1b18b850e5"
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
	case linuxEpochCheckpointV13:
		policy, _ := linuxEpochCheckpointV12.policy()
		policy.Schema = "t42-linux-epoch-operational-resources-v13"
		policy.IndexRuntime = strings.Replace(policy.IndexRuntime, "prepared-native-checkpoint", "source-selected-native-checkpoint", 1)
		policy.IndexRuntime = strings.Replace(policy.IndexRuntime, "no-selection-or-fit-promise", "no-fit-promise", 1)
		policy.Tracing = strings.Replace(policy.Tracing, "prepared-four-server", "source-selected-four-server", 1)
		policy.EngineMeasurement = strings.Replace(policy.EngineMeasurement, "prepared-FD7", "source-selected-FD7", 1)
		policy.RootFirst = strings.Replace(policy.RootFirst, "checkpoint-entry-remains-closed-pending-successor-qualification", "source-only-qualification-checkpoint-entry;exact-prior-and-next-issued-after-actual-terminal-Wait-closed-prefix-same-phase-PC-DA-SA-cutover-and-parent-workspace-sample;ordered-original-config4-listener-release-only;ordinary-entry-pressure-backup-and-epoch5-remain-closed;no-native-acceptance-or-fit-promise", 1)
		return policy, true
	default:
		return linuxEpochResourcePolicy{}, false
	}
}

// Prepared v12 describes epoch four without issuing it. Selected v13 still
// needs the separate completed predecessor/cutover capability for that epoch.
func (recipe linuxEpochRecipe) engine(epoch uint64) (string, uint32) {
	if recipe != linuxEpochLegacyV11 && recipe != linuxEpochCheckpointV12 && recipe != linuxEpochCheckpointV13 {
		return "", 0
	}
	switch epoch {
	case 1:
		return "", 6
	case 2:
		return "", 1
	case 3:
		if recipe == linuxEpochCheckpointV12 || recipe == linuxEpochCheckpointV13 {
			return dispatchadmission.EngineMeasurementCheckpointV2, 7
		}
		return "", 2
	case 4:
		if recipe == linuxEpochCheckpointV12 || recipe == linuxEpochCheckpointV13 {
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

// Only the coordinator constructor issues an identity. Ordinary entry is v11;
// the separate source-only qualification constructor selects v13.
// The shared pointer binds admission, tool choices and trace issuance to it.
type linuxEpochRecipeIdentity struct {
	coordinator *linuxEpochCoordinator
	recipe      linuxEpochRecipe
	policySHA   string
}

func issueLinuxEpochLegacyRecipe(adapter *linuxEpochCoordinator) *linuxEpochRecipeIdentity {
	return &linuxEpochRecipeIdentity{coordinator: adapter, recipe: linuxEpochLegacyV11, policySHA: linuxEpochPolicySHA256()}
}

func issueLinuxEpochCheckpointRecipe(adapter *linuxEpochCoordinator) *linuxEpochRecipeIdentity {
	policy, _ := linuxEpochCheckpointV13.policy()
	raw, err := json.Marshal(policy)
	if err != nil {
		panic("fixed Linux checkpoint policy cannot encode")
	}
	return &linuxEpochRecipeIdentity{coordinator: adapter, recipe: linuxEpochCheckpointV13, policySHA: SHA256(raw)}
}

func (identity *linuxEpochRecipeIdentity) matches(adapter *linuxEpochCoordinator) bool {
	if identity == nil || adapter == nil || identity.coordinator != adapter || adapter.recipe != identity || adapter.held == nil || adapter.held.flow == nil {
		return false
	}
	selected := adapter.held.flow.recipe
	if selected == 0 {
		selected = linuxEpochLegacyV11
	} // Historical internal constructor fixtures.
	return identity.recipe == selected && (selected == linuxEpochLegacyV11 && identity.policySHA == linuxEpochLegacyPolicySHA256 || selected == linuxEpochCheckpointV13 && identity.policySHA == linuxEpochCheckpointPolicySHA256)
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
