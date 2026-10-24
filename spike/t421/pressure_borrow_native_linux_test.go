//go:build linux && phebs_t42_native && phebs_t42_pressure_borrow_native

package t421

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

type nativeLinuxPressureBorrowRequest struct {
	Pressure LinuxPressureRequest          `json:"pressure"`
	UID      uint32                        `json:"uid"`
	GID      uint32                        `json:"gid"`
	Target   linuxExecutorPreworkSelection `json:"target"`
}

// Compilation is not execution/admission. This no-skip body needs newly exact
// root/target/helper/source/UID/namespace/resource admission. Work remains60min;
// test.timeout=0 preserves actual unknown owners and failed namespace fixtures.
func TestLinuxPressureBorrowRequiredNativeComposition(t *testing.T) {
	if _, bounded := t.Deadline(); bounded {
		t.Fatal("required native pressure custody needs -test.timeout=0")
	}
	if runtime.GOARCH != "amd64" || runtime.Version() != "go1.26.5" || os.Getuid() != 0 || os.Geteuid() != 0 || os.Getgid() != 0 || os.Getegid() != 0 || !linuxPressureBorrowBounding() {
		t.Fatal("exact root Linux amd64 Go1.26.5/frozen bounding context required; no skip")
	}
	request := readNativeGoReferenceRequest(t, "PHEBS_T42_PRESSURE_EXECUTOR_REQUEST")
	treeRequest := readNativeGoReferenceRequest(t, "PHEBS_T42_PRESSURE_INPUT_TREE_REQUEST")
	helperRequest := readNativeGoReferenceRequest(t, "PHEBS_T42_PRESSURE_GO_REFERENCE_REQUEST")
	if request.Role != "t422-execute" || request.PlanSchema != "" || treeRequest.Role != linuxInputTreeRole || treeRequest.PlanSchema != linuxInputTreeRecipe || helperRequest.Role != linuxGoReferenceRole || helperRequest.PlanSchema != linuxGoReferenceRecipe {
		t.Fatal("exact root executor/helper roles required")
	}
	for _, helper := range []ReferenceToolRequest{treeRequest, helperRequest} {
		if helper.RepositoryRoot != request.RepositoryRoot || helper.PlanSourceCommit != request.PlanSourceCommit || helper.IntegratedMainCommit != request.IntegratedMainCommit || helper.SourceCommit != request.SourceCommit || helper.GoRoot != request.GoRoot || helper.ModuleCache != request.ModuleCache || helper.GitBinary != request.GitBinary {
			t.Fatal("root counterpart helper lineage differs")
		}
	}
	selectionFile, err := os.Open(os.Getenv("PHEBS_T42_PRESSURE_BORROW_REQUEST"))
	if err != nil {
		t.Fatal("prospective root/target pressure request required")
	}
	raw, readErr := io.ReadAll(io.LimitReader(selectionFile, maxExecutionSelectionBytes+1))
	closeErr := selectionFile.Close()
	var selected nativeLinuxPressureBorrowRequest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if readErr != nil || closeErr != nil || len(raw) > maxExecutionSelectionBytes || decoder.Decode(&selected) != nil || decoder.Decode(new(any)) != io.EOF || !validLinuxPressureRequest(selected.Pressure) || !(linuxPressureBorrowPolicy{selected.UID, selected.GID}).valid() {
		t.Fatal("bounded exact pressure selection required")
	}
	base, err := os.MkdirTemp("", "phebs-pressure-borrow-")
	if err != nil {
		t.Fatal("new private root controller")
	}
	controllerRoot, brokerRoot := filepath.Join(base, "controller"), filepath.Join(base, "broker")
	controller := &linuxExecutorInnerPrework{observation: &executionPreworkObservation{}}
	var prework *linuxExecutorPrework
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal("controller failure nonce; retain", base)
	}
	failure := linuxExecutorPreworkSelection{PreparationRoot: controllerRoot, SourceCommit: request.SourceCommit, Attempt: hex.EncodeToString(nonce[:])}
	t.Cleanup(func() {
		failureAttempted := false
		if err := writeLinuxExecutorPreworkFailureOnce(&failureAttempted, failure, controller.observation, nil); err != nil {
			t.Error("controller first-observed failure publication refused; retain", base)
		}
		// The original caller remains present through all settlement. No root,
		// mounted residue, failed target leaf or private record is deleted here.
		if t.Failed() {
			t.Log("private pressure/controller roots retained", base, selected.Pressure.Parent)
		}
		if prework != nil {
			reported := false
			for {
				if err := prework.Close(context.Background()); err != nil && !reported {
					t.Error("original caller actor/source retirement refused or work failed; retain", base)
					reported = true
				}
				if prework.released {
					break
				}
				time.Sleep(time.Second)
			}
			<-prework.joined
		}
		firstClose := controller.Close(context.Background())
		if firstClose != nil {
			t.Error("controller original close refused; retain; first record is not terminal evidence", base)
		}
		if err := writeLinuxExecutorPreworkFailureOnce(&failureAttempted, failure, controller.observation, nil); err != nil {
			t.Error("new first-close failure publication refused; preserve partial record", base)
		}
		reported := firstClose != nil
		for !controller.retired() || !controller.observation.settle(context.Background()) {
			time.Sleep(time.Second)
			closeErr := controller.Close(context.Background())
			if err := writeLinuxExecutorPreworkFailureOnce(&failureAttempted, failure, controller.observation, nil); err != nil {
				t.Error("later first-close failure publication refused; preserve partial record", base)
			}
			if closeErr != nil && !reported {
				t.Error("controller actual cleanup failed; retain", base)
				reported = true
			}
		}
		if prework != nil && prework.pressure.retained {
			t.Error("actors retired but failed pressure fixture/namespace remains; no cleanup PASS", base, selected.Pressure.Parent)
			prework.holdRetainedPressure() // Strong original pin/process, not locator/GC lifetime.
		}
		if t.Failed() {
			return
		}
		for _, path := range []string{brokerRoot, controllerRoot, base} {
			entries, err := os.ReadDir(path)
			if err != nil || len(entries) != 0 || os.Remove(path) != nil {
				t.Error("healthy exact empty root retirement refused; retain", base)
				return
			}
		}
	})
	if os.Mkdir(controllerRoot, 0o700) != nil || os.Mkdir(brokerRoot, 0o700) != nil {
		t.Fatal("new controller/broker roots; retain", base)
	}
	ctx, cancel := context.WithTimeout(t.Context(), executionLinuxPreworkMaximum)
	defer cancel()
	controller.inputHelper, err = protectLinuxInputTreeHelperObserved(ctx, treeRequest, controller.observation)
	if err != nil {
		t.Fatal("admitted actual root input helper")
	}
	controller.git, err = protectLinuxExecutionGitObserved(ctx, controllerRoot, request.GitBinary, controller.inputHelper, controller.observation)
	if err != nil {
		t.Fatal("actual root protected Git")
	}
	controller.builds, err = protectLinuxExecutionGoBuildInputsObserved(ctx, controllerRoot, LinuxExecutionGoBuildRequest{Git: controller.git, RepositoryRoot: request.RepositoryRoot, PlanSourceCommit: request.PlanSourceCommit, IntegratedMainCommit: request.IntegratedMainCommit, SourceCommit: request.SourceCommit, GoRoot: request.GoRoot, ModuleCache: request.ModuleCache}, controller.inputHelper, controller.observation)
	if err != nil {
		t.Fatal("actual root source/SDK/module custody")
	}
	controller.referenceHelper, err = protectLinuxGoReferenceHelperObserved(ctx, helperRequest, controller.observation)
	if err != nil {
		t.Fatal("admitted actual root reference helper")
	}
	controller.tool, err = controller.builds.ProtectReferenceTool(ctx, controllerRoot, "t422-execute", request.Binary, controller.referenceHelper)
	if err != nil {
		t.Fatal("genuine joined root executor reference")
	}
	value := linuxPressureBorrowSelection{Schema: executionLinuxPressureSchema, UID: selected.UID, GID: selected.GID, Pressure: selected.Pressure, Target: selected.Target,
		Broker: linuxExecutorPreworkSelection{PreparationRoot: brokerRoot, RepositoryRoot: request.RepositoryRoot, PlanSourceCommit: request.PlanSourceCommit, IntegratedMainCommit: request.IntegratedMainCommit, SourceCommit: request.SourceCommit, GoRoot: request.GoRoot, ModuleCache: request.ModuleCache, GitBinary: request.GitBinary, InputHelperBinary: treeRequest.Binary, ReferenceHelperBinary: helperRequest.Binary}}
	prework = newLinuxPressureExecutorPrework(controller.builds, controller.tool, value)
	if prework.Run(ctx) != nil {
		t.Fatal("actual root broker/target/volume composition refused; preserve fixture and original custody", base)
	}
	if prework.command == nil || prework.command.Process == nil || prework.command.ProcessState == nil || !prework.command.ProcessState.Success() || prework.command.ProcessState.ExitCode() != 0 || !prework.retired || prework.pressure.retained {
		t.Fatal("genuine root broker Wait/positive pressure removal absent")
	}
	if prework.Close(ctx) != nil || !prework.released || !prework.pressure.namespace.closed || prework.pressure.namespace.closeErr != nil {
		t.Fatal("actual actor/source/image/namespace retirement incomplete")
	}
	if prework.Run(ctx) == nil {
		t.Fatal("shared original executor binding reset after pressure mode")
	}
	t.Log("finite root pressure preparation only: root and target independently joined reference/input lineage; actual credentialed leafFD/cwd work and child/broker Wait; nonforced H2m removal/exact detach; no formal profile/phase/scale acceptance")
}
