//go:build linux

package t421

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

func epochEngineLaunchTestRecord(epoch uint64, recipe string) (*ExecutionEpochOneRun, dispatchadmission.ProductionBootstrap, uint32) {
	phases := map[uint64][]uint32{1: {2, 3, 4}, 2: {5}, 3: {6, 7, 8}, 4: {8, 9, 10, 11}}[epoch]
	maximum := map[uint64]uint32{1: 6, 2: 1, 3: 2}[epoch]
	run := &ExecutionEpochOneRun{epoch: ExecutionEpochConfig{Epoch: epoch}}
	if recipe == dispatchadmission.EngineMeasurementCheckpointV2 {
		maximum = map[uint64]uint32{3: 7, 4: 1}[epoch]
		run.checkpointAllowed = epoch == 3
		if epoch == 4 {
			run.checkpointRecovery, run.checkpointPrior = &epochCheckpointRecoveryInput{}, &AuthorityPhaseResult{}
		}
	}
	base := []string{"HOME=/private/tmp", "TMPDIR=/private/tmp", "TMP=/private/tmp", "TEMP=/private/tmp", "PATH=/private/tmp", "LANG=C", "LC_ALL=C", "TZ=UTC"}
	git := linuxEpochForegroundGitEnvironment("/private/tmp", "/private/tmp", "/private/tmp")
	producer := uint32(epoch + 1)
	var mask uint16
	for _, phase := range phases {
		mask |= 1 << (phase - 1)
	}
	record := dispatchadmission.ProductionBootstrap{Program: dispatchadmission.ProgramPhebs, SemanticMode: dispatchadmission.ProductionSemanticV3,
		InputSHA256: [32]byte{7}, Producer: dispatchadmission.Producer{ID: producer, Binding: [32]byte{4}, Sites: dispatchadmission.ProductionSites()}, Phase: phases[0],
		Limits:    dispatchadmission.Limits{Producers: 12, Sites: 16, Roles: 4, Phases: 15, ActivePerProducer: 8, Attempts: 30, WireBytes: 64 << 10, AckTimeout: time.Second},
		Control:   dispatchadmission.PhaseControlConfig{OwnerControl: true, Phases: phases, InitialPhase: phases[0], MaximumPhases: len(phases), MaximumWireBytes: 64 << 10, Timeout: time.Second},
		Store:     &storeaccounting.ClientConfig{Producer: producer, Binding: [32]byte{9}, Phase: phases[0], Phases: mask, Calls: storeaccounting.MaximumCalls, Transactions: storeaccounting.MaximumTransactions, WireBytes: 4096, AckTimeout: time.Second},
		Workspace: &dispatchadmission.ProductionWorkspaceBinding{Path: "/private/workspace", Inode: 1, FSID: [2]int32{1, 2}},
		Tools: []dispatchadmission.ProductionToolBinding{{Role: "git", Path: "/bin/sh", Environment: git, RuntimeRecipe: epochEngineRuntimeRecipe(recipe)},
			{Role: "surreal", Path: "/bin/sh", Environment: base},
			{Role: "zoekt-git-index", Path: "/bin/sh", Environment: append(slices.Clone(git), dispatchadmission.IndexOfferEnvironment+"=v1", "ZOEKT_DISABLE_CATFILE_BATCH=true", "GOGC=50"), RuntimeRecipe: epochEngineRuntimeRecipe(recipe)}},
	}
	if run.checkpointAllowed {
		record.Control.TerminalPhase = 8
	}
	return run, record, maximum
}

func epochEngineLaunchTestPipe(t *testing.T) (*os.File, *net.UnixConn) {
	t.Helper()
	parent, child, err := dispatchadmission.NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = parent.Close(); _ = child.Close() })
	conn, err := adoptAuthorCustodySocket(child)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return parent, conn
}

func TestEpochEngineLaunchActualBootstrapAndEndpointBinding(t *testing.T) {
	for _, test := range []struct {
		name, recipe string
		epoch        uint64
		wire         int64
	}{
		{"legacy-cold", "", 1, 632}, {"legacy-logical", "", 2, 232}, {"legacy-return", "", 3, 312},
		{"checkpoint", dispatchadmission.EngineMeasurementCheckpointV2, 3, 712}, {"recovery", dispatchadmission.EngineMeasurementCheckpointV2, 4, 232},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			descriptor := linuxEpochLegacyV11
			if test.recipe == dispatchadmission.EngineMeasurementCheckpointV2 {
				descriptor = linuxEpochCheckpointV12
			}
			recipe, maximum := descriptor.engine(test.epoch)
			run, record, expectedMaximum := epochEngineLaunchTestRecord(test.epoch, test.recipe)
			if recipe != test.recipe || maximum != expectedMaximum {
				t.Fatal("descriptor changed authenticated bootstrap selection")
			}
			binding, err := epochEngineLaunchBinding(recipe, maximum, run, record.Producer, record.InputSHA256, record.Tools)
			if err != nil {
				t.Fatal(err)
			}
			output := &checkoutCommandOutput{remaining: 64 << 20}
			if reserveEpochEngineWire(output, binding, maximum) != nil || output.remaining != 64<<20-test.wire {
				t.Fatal("wire escaped existing combined output ceiling", output.remaining)
			}
			record.EngineMeasurements, record.EngineMeasurementRecipe = maximum, binding.Recipe
			raw, err := json.Marshal(record)
			if err != nil || test.recipe == "" && bytes.Contains(raw, []byte("EngineMeasurementRecipe")) {
				t.Fatal("legacy omitted bootstrap bytes changed", err)
			}
			daParent, da := epochEngineLaunchTestPipe(t)
			pcParent, pc := epochEngineLaunchTestPipe(t)
			deadline, _ := ctx.Deadline()
			if da.SetDeadline(deadline) != nil || pc.SetDeadline(deadline) != nil {
				t.Fatal("bootstrap deadline")
			}
			sent := make(chan error, 1)
			go func() {
				defer close(sent)
				sent <- dispatchadmission.SendProductionBootstrap(ctx, daParent, pcParent, record)
			}()
			t.Cleanup(func() {
				cancel()
				_ = daParent.Close()
				_ = pcParent.Close()
				_ = da.Close()
				_ = pc.Close()
				<-sent
			})
			var daHeader, pcHeader [72]byte
			if _, err := io.ReadFull(pc, pcHeader[:]); err != nil {
				t.Fatal(err)
			}
			if _, err := io.ReadFull(da, daHeader[:]); err != nil || binary.BigEndian.Uint32(daHeader[4:8]) != uint32(len(raw)) {
				t.Fatal("bootstrap header", err)
			}
			observed := make([]byte, len(raw))
			if _, err := io.ReadFull(da, observed); err != nil || !bytes.Equal(observed, raw) {
				t.Fatal("bootstrap recipe/binding bytes changed in transit", err)
			}
			if _, err := da.Write(daHeader[:]); err != nil {
				t.Fatal(err)
			}
			if _, err := pc.Write(pcHeader[:]); err != nil {
				t.Fatal(err)
			}
			if err := <-sent; err != nil {
				t.Fatal("production bootstrap validation/ACK", err)
			}
			engineParent, child := epochEngineLaunchTestPipe(t)
			engine, err := adoptAuthorCustodySocket(engineParent)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = engine.Close() })
			if child.SetDeadline(deadline) != nil {
				t.Fatal("engine deadline")
			}
			served := make(chan error, 1)
			go func() {
				defer close(served)
				served <- dispatchadmission.ServeEngineMeasurement(ctx, engine, binding, maximum, func(context.Context, uint32, func(context.Context, dispatchadmission.EngineMeasurementIdentity) error) error {
					return ErrExecutionEpochOne // This wire-only control never grants a native HOLD.
				})
			}()
			t.Cleanup(func() {
				cancel()
				_ = engine.Close()
				_ = child.Close()
				<-served
			})
			var header [76]byte
			if _, err := io.ReadFull(child, header[:]); err != nil || binary.BigEndian.Uint32(header[4:8]) != record.Producer.ID ||
				binary.BigEndian.Uint32(header[8:12]) != record.EngineMeasurements || !bytes.Equal(header[12:44], record.Producer.Binding[:]) || !bytes.Equal(header[44:], record.InputSHA256[:]) {
				t.Fatal("endpoint differs from actual bootstrap", err)
			}
			magic := "EM01"
			if record.EngineMeasurementRecipe != "" {
				magic = "EM02"
			}
			if string(header[:4]) != magic {
				t.Fatal("endpoint recipe differs from actual bootstrap")
			}
			if _, err := child.Write(header[:]); err != nil || child.Close() != nil {
				t.Fatal("engine echo/EOF", err)
			}
			if err := <-served; err != nil {
				t.Fatal("engine endpoint join", err)
			}
		})
	}
}

func TestEpochEngineLaunchSelectionRefusals(t *testing.T) {
	for _, name := range []string{"unknown", "no-maximum", "old-maximum", "foreign-producer", "no-binding", "no-input", "old-runtime", "unpaired-runtime", "no-checkpoint", "backup", "archive", "no-recovery", "no-prior", "wrong-epoch"} {
		t.Run(name, func(t *testing.T) {
			recipe := dispatchadmission.EngineMeasurementCheckpointV2
			run, record, maximum := epochEngineLaunchTestRecord(3, recipe)
			switch name {
			case "unknown":
				recipe = "unknown"
			case "no-maximum":
				maximum = 0
			case "old-maximum":
				maximum = 2
			case "foreign-producer":
				record.Producer.ID++
			case "no-binding":
				record.Producer.Binding = [32]byte{}
			case "no-input":
				record.InputSHA256 = [32]byte{}
			case "old-runtime":
				record.Tools[0].RuntimeRecipe, record.Tools[2].RuntimeRecipe = dispatchadmission.ProductionLinuxForegroundGitV2, dispatchadmission.ProductionLinuxForegroundGitV2
			case "unpaired-runtime":
				record.Tools[2].RuntimeRecipe = dispatchadmission.ProductionLinuxForegroundGitV2
			case "no-checkpoint":
				run.checkpointAllowed = false
			case "backup":
				run.backupAllowed = true
			case "archive":
				run.archiveInput = &epochArchiveInput{}
			case "no-recovery", "no-prior":
				run, record, maximum = epochEngineLaunchTestRecord(4, recipe)
				if name == "no-recovery" {
					run.checkpointRecovery = nil
				} else {
					run.checkpointPrior = nil
				}
			case "wrong-epoch":
				run.epoch.Epoch = 1
				record.Producer.ID = 2
			}
			if binding, err := epochEngineLaunchBinding(recipe, maximum, run, record.Producer, record.InputSHA256, record.Tools); err == nil || binding != (dispatchadmission.EngineMeasurementBinding{}) {
				t.Fatal("foreign launch acquired engine binding")
			}
		})
	}
}

func TestEpochEngineWireDebitRefusesWithoutMutation(t *testing.T) {
	run, record, maximum := epochEngineLaunchTestRecord(3, dispatchadmission.EngineMeasurementCheckpointV2)
	binding, err := epochEngineLaunchBinding(dispatchadmission.EngineMeasurementCheckpointV2, maximum, run, record.Producer, record.InputSHA256, record.Tools)
	if err != nil {
		t.Fatal(err)
	}
	for _, remaining := range []int64{-1, 0, 711, 712} {
		output := &checkoutCommandOutput{remaining: remaining}
		err := reserveEpochEngineWire(output, binding, maximum)
		if remaining == 712 {
			if err != nil || output.remaining != 0 {
				t.Fatal("exact ceiling refused", err)
			}
		} else if err == nil || output.remaining != remaining {
			t.Fatal("refusal changed output capacity", remaining, output.remaining)
		}
	}
	if reserveEpochEngineWire(nil, binding, maximum) == nil {
		t.Fatal("nil output admitted")
	}
	adapter := linuxEpochRecipeTestCoordinator(t)
	for _, epoch := range []uint64{1, 2, 3, 4, 5} {
		recipe, selected := adapter.engineMeasurements(epoch)
		if recipe != "" || selected != map[uint64]uint32{1: 6, 2: 1, 3: 2}[epoch] {
			t.Fatal("legacy constructor selection changed", epoch)
		}
	}
}
