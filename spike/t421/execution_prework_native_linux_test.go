//go:build linux && phebs_t42_native && phebs_t42_executor_native

package t421

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// Required opt-in composition of the genuine source-built executor, not the
// default sealed-test-image kernel controls. Exact image/helper/namespace/input
// and aggregate-resource admission are prerequisites outside this test. Use
// -test.timeout=0: the 60min work bound remains, but unknown original owners
// retain this process during settlement instead of being lost to a hard timeout.
func TestLinuxExecutorRequiredNativePreworkComposition(t *testing.T) {
	if _, bounded := t.Deadline(); bounded {
		t.Fatal("required native custody test must use -test.timeout=0;60min work remains bounded")
	}
	if os.Getuid() == 0 || os.Geteuid() == 0 || runtime.GOARCH != "amd64" || runtime.Version() != "go1.26.5" {
		t.Fatal("required unprivileged Linux amd64 Go1.26.5; no skip")
	}
	request := readNativeGoReferenceRequest(t, "PHEBS_T42_EXECUTOR_CANDIDATE_REQUEST")
	treeRequest := readNativeGoReferenceRequest(t, "PHEBS_T42_INPUT_TREE_REQUEST")
	helperRequest := readNativeGoReferenceRequest(t, "PHEBS_T42_GO_REFERENCE_REQUEST")
	if request.Role != "t422-execute" || request.PlanSchema != "" || treeRequest.Role != linuxInputTreeRole || treeRequest.PlanSchema != linuxInputTreeRecipe ||
		helperRequest.Role != linuxGoReferenceRole || helperRequest.PlanSchema != linuxGoReferenceRecipe {
		t.Fatal("exact executor and helper roles/recipes required")
	}
	for _, helper := range []ReferenceToolRequest{treeRequest, helperRequest} {
		if helper.RepositoryRoot != request.RepositoryRoot || helper.PlanSourceCommit != request.PlanSourceCommit ||
			helper.IntegratedMainCommit != request.IntegratedMainCommit || helper.SourceCommit != request.SourceCommit ||
			helper.GoRoot != request.GoRoot || helper.ModuleCache != request.ModuleCache || helper.GitBinary != request.GitBinary {
			t.Fatal("same source/SDK/module/Git selection required")
		}
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal("private controller attempt nonce")
	}
	base, err := os.MkdirTemp("", "phebs-linux-executor-prework-")
	if err != nil {
		t.Fatal("private preparation root")
	}
	controllerRoot, innerRoot := filepath.Join(base, "controller"), filepath.Join(base, "inner")
	controller := &linuxExecutorInnerPrework{observation: &executionPreworkObservation{}} // Cleanup owner only; no consumed profile authority.
	failureSelection := linuxExecutorPreworkSelection{PreparationRoot: controllerRoot, SourceCommit: request.SourceCommit, Attempt: hex.EncodeToString(nonce[:])}
	var prework *linuxExecutorPrework
	t.Cleanup(func() {
		// Preserve first-observed helper/workspace facts before settlement can
		// retain this process indefinitely; no failed root/record is deleted.
		if err := writeLinuxExecutorPreworkFailure(failureSelection, controller.observation); err != nil {
			t.Error("controller private failure publication refused; retain", base)
		}
		if t.Failed() || controller.observation.nativeCloseFailed.Load() || controller.observation.unknown() {
			t.Log("private controller failure roots/records retained before original-owner settlement", base)
		}
		if prework != nil {
			reported := false
			for {
				err := prework.Close(context.Background())
				if err != nil && !reported {
					t.Error("prework failed or retained custody; no cleanup PASS", base)
					reported = true
				}
				if prework.released {
					break
				}
				time.Sleep(time.Second)
			}
			<-prework.joined // Real source/image keeper retirement, never SID alone.
		}
		reported := false
		for {
			err := controller.Close(context.Background())
			if err != nil && !reported {
				t.Error("controller native custody cleanup refused; no PASS", base)
				reported = true
			}
			if controller.retired() && controller.observation.settle(context.Background()) {
				break
			}
			time.Sleep(time.Second)
		}
		if t.Failed() {
			t.Log("failed private roots/records/workspaces retained", base)
			return
		}
		for _, root := range []string{innerRoot, controllerRoot, base} {
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 || os.Remove(root) != nil {
				t.Error("exact empty preparation retirement refused; retain", base)
				return
			}
		}
	})
	if os.Mkdir(controllerRoot, 0o700) != nil || os.Mkdir(innerRoot, 0o700) != nil {
		t.Fatal("private preparation roots; retain", base)
	}
	ctx, cancel := context.WithTimeout(t.Context(), executionLinuxPreworkMaximum)
	defer cancel()
	controller.inputHelper, err = protectLinuxInputTreeHelperObserved(ctx, treeRequest, controller.observation)
	if err != nil {
		t.Fatal("admitted exact source input helper required")
	}
	controller.git, err = protectLinuxExecutionGitObserved(ctx, controllerRoot, request.GitBinary, controller.inputHelper, controller.observation)
	if err != nil {
		t.Fatal("protected controller Git required")
	}
	controller.builds, err = protectLinuxExecutionGoBuildInputsObserved(ctx, controllerRoot, LinuxExecutionGoBuildRequest{Git: controller.git,
		RepositoryRoot: request.RepositoryRoot, PlanSourceCommit: request.PlanSourceCommit, IntegratedMainCommit: request.IntegratedMainCommit,
		SourceCommit: request.SourceCommit, GoRoot: request.GoRoot, ModuleCache: request.ModuleCache}, controller.inputHelper, controller.observation)
	if err != nil {
		t.Fatal("protected controller source/SDK/module inputs required")
	}
	controller.referenceHelper, err = protectLinuxGoReferenceHelperObserved(ctx, helperRequest, controller.observation)
	if err != nil {
		t.Fatal("admitted exact source reference helper required")
	}
	controller.tool, err = controller.builds.ProtectReferenceTool(ctx, controllerRoot, "t422-execute", request.Binary, controller.referenceHelper)
	if err != nil {
		t.Fatal("genuine joined source-built executor required")
	}
	identity, err := controller.tool.Check(ctx, "t422-execute")
	if err != nil || controller.tool.referenceInputs != controller.builds || identity.BuildVCSRevision != request.SourceCommit || !controller.builds.last.joined || !controller.builds.last.empty {
		t.Fatal("controller executor provenance was not actually issued")
	}
	prework = newLinuxExecutorPrework(controller.builds, controller.tool, linuxExecutorPreworkSelection{PreparationRoot: innerRoot,
		RepositoryRoot: request.RepositoryRoot, PlanSourceCommit: request.PlanSourceCommit, IntegratedMainCommit: request.IntegratedMainCommit,
		SourceCommit: request.SourceCommit, GoRoot: request.GoRoot, ModuleCache: request.ModuleCache, GitBinary: request.GitBinary,
		InputHelperBinary: treeRequest.Binary, ReferenceHelperBinary: helperRequest.Binary})
	if err := prework.Run(ctx); err != nil {
		t.Fatal("actual finite executor prework refused; retain original evidence", base)
	}
	if prework.command == nil || prework.command.Process == nil || prework.command.ProcessState == nil || !prework.command.ProcessState.Success() ||
		prework.command.ProcessState.ExitCode() != 0 || !prework.retired {
		t.Fatal("actual outer Wait/control retirement missing")
	}
	if err := prework.Close(ctx); err != nil || !prework.released {
		t.Fatal("actual source-root/image loan retirement incomplete")
	}
	if prework.Run(ctx) == nil {
		t.Fatal("consumed executor prework was reusable")
	}
	t.Log("genuine source-built executor and independent inner reference; actual outer/inner Wait/control/watcher/loan retirement; finite prework only")
}
