//go:build linux

package t421

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

// Real DA/SA construction and logical bindings only. Workspace placeholders
// cannot issue native resource/image authority or start an operational child.
func linuxEpochRecipeTestCoordinator(t *testing.T) *linuxEpochCoordinator {
	t.Helper()
	return linuxEpochRecipeTestCoordinatorForRecipe(t, linuxEpochLegacyV11)
}

func linuxEpochRecipeTestCoordinatorForRecipe(t *testing.T, recipe linuxEpochRecipe) *linuxEpochCoordinator {
	t.Helper()
	operation, cancel := context.WithCancel(context.Background())
	settlement, settleCancel := context.WithCancel(context.Background())
	flow := &LinuxExecutionEpochFlow{recipe: recipe, lifetime: operation, cancel: cancel, settlement: settlement, settleCancel: settleCancel}
	held := linuxEpochWorkspaceFixture(t)
	held.flow = flow
	core, err := newLinuxEpochCoordinator(flow, held, accountingTestPlan(t))
	if err != nil {
		cancel()
		settleCancel()
		t.Fatal("real control constructor", err)
	}
	flow.core, flow.held = core, held
	t.Cleanup(func() {
		if err := core.Close(); err != nil {
			t.Error("real control closure", err)
		}
		cancel()
		settleCancel()
	})
	return core.native.(*linuxEpochCoordinator)
}

func TestLinuxEpochRecipeHistoricalPolicyAndPreparedDescriptor(t *testing.T) {
	legacy, ok := linuxEpochLegacyV11.policy()
	if !ok || legacy != linuxEpochPolicyV11() || legacy != linuxEpochPolicy() {
		t.Fatal("default is no longer exact historical v11")
	}
	raw, err := json.Marshal(legacy)
	if err != nil || SHA256(raw) != linuxEpochLegacyPolicySHA256 || linuxEpochPolicySHA256() != linuxEpochLegacyPolicySHA256 {
		t.Fatal("historical v11 canonical bytes changed", err)
	}
	checkpoint, ok := linuxEpochCheckpointV12.policy()
	if !ok || checkpoint.Schema != "t42-linux-epoch-operational-resources-v12" || checkpoint.RSSReservationBytes != 4<<30 ||
		!strings.Contains(checkpoint.EngineMeasurement, "EM02-server-producers=4,5-maximum-points=7,1") ||
		strings.Contains(checkpoint.EngineMeasurement, "server-producers=2,3,4;") ||
		!strings.Contains(checkpoint.IndexRuntime, dispatchadmission.ProductionLinuxForegroundGitV3) ||
		!strings.Contains(checkpoint.RootFirst, "checkpoint-entry-remains-closed") ||
		!strings.Contains(checkpoint.Tracing, "prepared-four-server-ring-and-map-backing") {
		t.Fatal("prepared descriptor lost a closed relationship")
	}
	// Only explicitly prospective recipe text changes; all other policy bytes,
	// numeric allowances, custody rules and frozen deadlines stay exact.
	checkpoint.Schema, checkpoint.IndexRuntime = legacy.Schema, legacy.IndexRuntime
	checkpoint.EngineMeasurement, checkpoint.Tracing = legacy.EngineMeasurement, legacy.Tracing
	if checkpoint != legacy {
		t.Fatal("preparation changed an unrelated policy constraint")
	}
}

func TestLinuxEpochRecipeEngineRuntimeAndWireRelationships(t *testing.T) {
	for _, test := range []struct {
		name    string
		recipe  linuxEpochRecipe
		epoch   uint64
		engine  string
		maximum uint32
		wire    uint64
	}{
		{"legacy-cold", linuxEpochLegacyV11, 1, "", 6, 632},
		{"legacy-logical", linuxEpochLegacyV11, 2, "", 1, 232},
		{"legacy-return", linuxEpochLegacyV11, 3, "", 2, 312},
		{"prepared-cold", linuxEpochCheckpointV12, 1, "", 6, 632},
		{"prepared-logical", linuxEpochCheckpointV12, 2, "", 1, 232},
		{"prepared-checkpoint", linuxEpochCheckpointV12, 3, dispatchadmission.EngineMeasurementCheckpointV2, 7, 712},
		{"prepared-recovery", linuxEpochCheckpointV12, 4, dispatchadmission.EngineMeasurementCheckpointV2, 1, 232},
	} {
		t.Run(test.name, func(t *testing.T) {
			engine, maximum := test.recipe.engine(test.epoch)
			if engine != test.engine || maximum != test.maximum || test.recipe.runtime(test.epoch) != epochEngineRuntimeRecipe(test.engine) {
				t.Fatal("policy descriptor split runtime from engine", engine, maximum)
			}
			run, record, _ := epochEngineLaunchTestRecord(test.epoch, test.engine)
			record.Tools[0].RuntimeRecipe, record.Tools[2].RuntimeRecipe = test.recipe.runtime(test.epoch), test.recipe.runtime(test.epoch)
			binding, err := epochEngineLaunchBinding(engine, maximum, run, record.Producer, record.InputSHA256, record.Tools)
			if err != nil {
				t.Fatal("prepared descriptor failed real launcher binding", err)
			}
			wire, err := binding.WireBytes(maximum)
			if err != nil || wire != test.wire {
				t.Fatal("wrong existing combined-ceiling debit", wire, err)
			}
			for _, maximum := range []uint32{0, maximum - 1, maximum + 1} {
				if _, err := binding.WireBytes(maximum); err == nil {
					t.Fatal("foreign maximum gained authority", maximum)
				}
			}
		})
	}
	for _, recipe := range []linuxEpochRecipe{0, linuxEpochLegacyV11, linuxEpochCheckpointV12, 255} {
		for _, epoch := range []uint64{0, 4, 5, math.MaxUint64} {
			if recipe == linuxEpochCheckpointV12 && epoch == 4 {
				continue
			}
			engine, maximum := recipe.engine(epoch)
			if engine != "" || maximum != 0 || recipe.runtime(epoch) != "" {
				t.Fatal("unknown selection acquired a fallback", recipe, epoch)
			}
		}
		if recipe == 0 || recipe == 255 {
			if policy, ok := recipe.policy(); ok || policy != (linuxEpochResourcePolicy{}) {
				t.Fatal("unknown policy acquired a fallback")
			}
		}
	}
}

func TestLinuxEpochRecipeConstructorKeepsLegacySelection(t *testing.T) {
	adapter := linuxEpochRecipeTestCoordinator(t)
	if !adapter.issued() || !adapter.recipe.matches(adapter) || linuxEpochRecipeForHeld(adapter.held) != adapter.recipe {
		t.Fatal("real constructor did not share its identity")
	}
	for _, epoch := range []uint64{0, 1, 2, 3, 4, 5} {
		engine, maximum := adapter.engineMeasurements(epoch)
		if engine != "" || maximum != map[uint64]uint32{1: 6, 2: 1, 3: 2}[epoch] {
			t.Fatal("current constructor selected a prospective recipe", epoch)
		}
	}
	// The checkpoint descriptor can be inspected, but cannot issue an identity.
	adapter.recipe.recipe = linuxEpochCheckpointV12
	defer func() { adapter.recipe.recipe = linuxEpochLegacyV11 }()
	if adapter.issued() || linuxEpochRecipeForHeld(adapter.held) != nil {
		t.Fatal("prepared descriptor enabled native issuance")
	}
	if _, maximum := adapter.engineMeasurements(3); maximum != 0 {
		t.Fatal("prepared descriptor enabled the endpoint")
	}
}

func TestLinuxEpochRecipeIdentityRefusals(t *testing.T) {
	for _, name := range []string{"missing", "cloned", "foreign-coordinator", "coordinator-copy", "resource-digest", "identity-digest", "prepared-recipe", "unknown-recipe", "foreign-held", "missing-core", "typed-nil-adapter", "missing-config", "typed-nil-config", "foreign-author"} {
		t.Run(name, func(t *testing.T) {
			adapter := linuxEpochRecipeTestCoordinator(t)
			held := adapter.held
			a := &linuxEpochResourceAdmission{held: held, recipe: adapter.recipe, policySHA: adapter.recipe.policySHA, uid: os.Getuid(), euid: os.Geteuid()}
			held.controllerPID = os.Getpid()
			held.resources = a
			if !a.recipeBound() {
				t.Fatal("connected identity missing before mutation")
			}
			// Explicitly modeled issued flag to reach the refusal boundary, never
			// native issuance: every mutation must fail before any host inspection.
			a.issued = true
			copyAdapter, copyIdentity := *adapter, *adapter.recipe
			savedCore, savedEpochs, savedAuthor := held.flow.core, adapter.core.epochs, adapter.core.epochs.author
			savedConfig := adapter.core.epochs.native
			t.Cleanup(func() {
				*adapter = copyAdapter
				*adapter.recipe = copyIdentity
				held.flow.core, adapter.core.epochs = savedCore, savedEpochs
				adapter.core.native = adapter
				adapter.core.epochs.author = savedAuthor
				adapter.core.epochs.native = savedConfig
			})
			switch name {
			case "missing":
				a.recipe = nil
			case "cloned":
				clone := *adapter.recipe
				a.recipe = &clone
			case "foreign-coordinator":
				adapter.recipe.coordinator = &copyAdapter
			case "coordinator-copy":
				clone := *adapter.recipe
				adapter.recipe = &clone
			case "resource-digest":
				a.policySHA = SHA256([]byte("foreign policy"))
			case "identity-digest":
				adapter.recipe.policySHA = SHA256([]byte("foreign policy"))
				a.policySHA = adapter.recipe.policySHA
			case "prepared-recipe":
				adapter.recipe.recipe = linuxEpochCheckpointV12
			case "unknown-recipe":
				adapter.recipe.recipe = 255
			case "foreign-held":
				a.held = &linuxEpochHeldFlow{flow: held.flow, controllerPID: os.Getpid()}
			case "missing-core":
				held.flow.core = nil
			case "typed-nil-adapter":
				adapter.core.native = (*linuxEpochCoordinator)(nil)
			case "missing-config":
				adapter.core.epochs = nil
			case "typed-nil-config":
				adapter.core.epochs.native = (*linuxEpochConfigAdapter)(nil)
			case "foreign-author":
				adapter.core.epochs.author = &ExecutionAuthorCustody{}
			}
			if a.recipeBound() {
				t.Fatal("foreign identity bound")
			}
			if owner, err := newLinuxEpochTraceOwner(a); err == nil || owner != nil {
				t.Fatal("foreign identity allocated an operational trace owner")
			}
			if err := a.Check(t.Context()); err == nil || a.checks != 0 || a.privateRefusal() != "resource-recipe-binding" {
				t.Fatal("foreign identity reached native inspection", err, a.checks, a.privateRefusal())
			}
		})
	}
	for _, adapter := range []*linuxEpochCoordinator{nil, {}} {
		if recipe, maximum := adapter.engineMeasurements(1); recipe != "" || maximum != 0 {
			t.Fatal("unissued coordinator acquired endpoint selection")
		}
	}
	// No supplied recipe or digest replaces the genuine held-flow issuer.
	if a, err := admitLinuxEpochResources(t.Context(), &linuxEpochHeldFlow{controllerPID: os.Getpid(), controllerStart: "supplied"}, func() {}); err == nil || a != nil {
		t.Fatal("supplied held placeholder issued resources")
	}
}

func TestLinuxEpochRecipeLegacyBootstrapBytes(t *testing.T) {
	adapter := linuxEpochRecipeTestCoordinator(t)
	for _, epoch := range []uint64{1, 2, 3} {
		run, record, maximum := epochEngineLaunchTestRecord(epoch, "")
		record.EngineMeasurements = maximum
		before, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		engine, selectedMaximum := adapter.engineMeasurements(epoch)
		binding, err := epochEngineLaunchBinding(engine, selectedMaximum, run, record.Producer, record.InputSHA256, record.Tools)
		if err != nil || selectedMaximum != maximum {
			t.Fatal("legacy launcher choice changed", err)
		}
		record.EngineMeasurementRecipe = binding.Recipe
		record.EngineMeasurements = selectedMaximum
		after, err := json.Marshal(record)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("omitted legacy bootstrap field changed", err)
		}
	}
}
