//go:build darwin || linux

package t4013

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCustodyCreationRecoversEveryPreLiveBoundary(t *testing.T) {
	for _, boundary := range []string{
		"none", "directory", "controller", "locks", "state-stage", "state", "published",
	} {
		t.Run(boundary, func(t *testing.T) {
			workspace, planDigest, token, _, stage := stageAtomicCustodyCreation(t, boundary)

			supervision, err := beginPrepareCustody(workspace, planDigest, token)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = supervision.Close() }()
			if supervision.Token() != token || supervision.state.Phase != custodyPhaseLive {
				t.Fatalf("recovered custody = %#v", supervision.state)
			}
			if _, err := os.Lstat(stage); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("creation stage survived publication: %v", err)
			}
		})
	}
}

func TestCustodyCleanupRecoversStagedAndPublishedCreatedControl(t *testing.T) {
	for _, boundary := range []string{"state-stage", "published"} {
		t.Run(boundary, func(t *testing.T) {
			workspace, planDigest, token, control, stage := stageAtomicCustodyCreation(t, boundary)
			if err := destroySupervisedPreparedCustody(
				workspace, t.TempDir(), planDigest, token,
			); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{
				workspace, control, stage, control + custodyRetiringSuffix, control + custodyRetiredSuffix,
			} {
				if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("created-custody cleanup retained %q: %v", path, err)
				}
			}
		})
	}
}

func TestCustodyPreReturnTransitionsDiscardUncommittedState(t *testing.T) {
	t.Run("prepare-live", func(t *testing.T) {
		workspace, planDigest, token, control, _ := stageAtomicCustodyCreation(t, "published")
		staged := custodyControlState{
			Schema: custodyControlSchema, Token: token, PlanDigest: planDigest,
			Workspace: workspace, Operation: custodyOperationPrepare, Phase: custodyPhaseLive,
		}
		writeCustodyTransitionStage(t, control, staged)
		status, supervision, err := inspectCustodySupervision(
			workspace, planDigest, token, custodyOperationPrepare, "",
		)
		if err != nil || status != custodyStatusCreated || supervision == nil {
			t.Fatalf("recover prepare admission: status=%q held=%t err=%v",
				status, supervision != nil, err)
		}
		if err := supervision.Close(); err != nil {
			t.Fatal(err)
		}
		assertNoCustodyTransitionStage(t, control)
	})

	t.Run("execute-live", func(t *testing.T) {
		workspace := filepath.Join(t.TempDir(), "workspace")
		planDigest := digest([]byte("execute-transition"))
		token := mustCustodyToken(t)
		prepare, err := beginPrepareCustody(workspace, planDigest, token)
		if err != nil {
			t.Fatal(err)
		}
		if err := prepare.Drain(""); err != nil {
			t.Fatal(err)
		}
		staged := prepare.state
		staged.Operation = custodyOperationExecute
		staged.Phase = custodyPhaseLive
		writeCustodyTransitionStage(t, prepare.directory, staged)
		if err := prepare.Close(); err != nil {
			t.Fatal(err)
		}
		execute, err := beginExecuteCustody(workspace, planDigest, token)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = execute.Close() }()
		if execute.state.Operation != custodyOperationExecute || execute.state.Phase != custodyPhaseLive {
			t.Fatalf("execute admission state = %#v", execute.state)
		}
		assertNoCustodyTransitionStage(t, execute.directory)
	})

	t.Run("finalizing", func(t *testing.T) {
		workspace := filepath.Join(t.TempDir(), "workspace")
		planDigest := digest([]byte("finalizing-transition"))
		checkpointDigest := digest([]byte("finalizing-checkpoint"))
		token := mustCustodyToken(t)
		prepare, err := beginPrepareCustody(workspace, planDigest, token)
		if err != nil {
			t.Fatal(err)
		}
		if err := prepare.Drain(""); err != nil {
			t.Fatal(err)
		}
		if err := prepare.Close(); err != nil {
			t.Fatal(err)
		}
		execute, err := beginExecuteCustody(workspace, planDigest, token)
		if err != nil {
			t.Fatal(err)
		}
		if err := execute.Drain(checkpointDigest); err != nil {
			t.Fatal(err)
		}
		staged := execute.state
		staged.Phase = custodyPhaseFinalizing
		writeCustodyTransitionStage(t, execute.directory, staged)
		if err := execute.Close(); err != nil {
			t.Fatal(err)
		}
		status, held, err := inspectCustodySupervision(
			workspace, planDigest, token, custodyOperationExecute, checkpointDigest,
		)
		if err != nil || status != custodyStatusDrained || held == nil {
			t.Fatalf("recover finalizer admission: status=%q held=%t err=%v",
				status, held != nil, err)
		}
		defer func() { _ = held.Close() }()
		assertNoCustodyTransitionStage(t, held.directory)
	})
}

func TestCustodyPostProofTransitionsCommitStagedState(t *testing.T) {
	t.Run("prepare-drain", func(t *testing.T) {
		workspace := filepath.Join(t.TempDir(), "workspace")
		planDigest := digest([]byte("prepare-drain"))
		token := mustCustodyToken(t)
		supervision, err := beginPrepareCustody(workspace, planDigest, token)
		if err != nil {
			t.Fatal(err)
		}
		staged := supervision.state
		staged.Phase = custodyPhaseDrained
		writeCustodyTransitionStage(t, supervision.directory, staged)
		if err := supervision.Close(); err != nil {
			t.Fatal(err)
		}
		assertRecoveredCustodyStatus(
			t, workspace, planDigest, token, custodyOperationPrepare, "", custodyStatusDrained,
		)
	})

	for _, test := range []struct {
		name       string
		checkpoint string
		staged     func(custodyControlState) custodyControlState
		operation  custodyOperation
		want       custodyStatus
	}{
		{
			name: "execute-drain", checkpoint: digest([]byte("execute-drain-checkpoint")),
			operation: custodyOperationExecute, want: custodyStatusDrained,
			staged: func(state custodyControlState) custodyControlState {
				state.Phase = custodyPhaseDrained
				state.CheckpointDigest = digest([]byte("execute-drain-checkpoint"))
				return state
			},
		},
		{
			name: "execute-abort", operation: custodyOperationPrepare, want: custodyStatusDrained,
			staged: func(state custodyControlState) custodyControlState {
				state.Operation = custodyOperationPrepare
				state.Phase = custodyPhaseDrained
				return state
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			workspace, planDigest, token, execute := liveExecuteCustodyForAtomicTest(t, test.name)
			writeCustodyTransitionStage(t, execute.directory, test.staged(execute.state))
			if err := execute.Close(); err != nil {
				t.Fatal(err)
			}
			assertRecoveredCustodyStatus(
				t, workspace, planDigest, token, test.operation, test.checkpoint, test.want,
			)
		})
	}

	t.Run("terminal-drain", func(t *testing.T) {
		workspace, planDigest, token, execute := liveExecuteCustodyForAtomicTest(t, "terminal-drain")
		checkpoint := digest([]byte("terminal-drain-checkpoint"))
		if err := execute.Drain(checkpoint); err != nil {
			t.Fatal(err)
		}
		if err := execute.BeginFinalization(checkpoint); err != nil {
			t.Fatal(err)
		}
		staged := execute.state
		staged.Phase = custodyPhaseTerminal
		writeCustodyTransitionStage(t, execute.directory, staged)
		if err := execute.Close(); err != nil {
			t.Fatal(err)
		}
		assertRecoveredCustodyStatus(
			t, workspace, planDigest, token, custodyOperationExecute, checkpoint, custodyStatusTerminal,
		)
	})
}

func liveExecuteCustodyForAtomicTest(
	t *testing.T, label string,
) (string, string, string, *custodySupervision) {
	t.Helper()
	workspace := filepath.Join(t.TempDir(), "workspace")
	planDigest := digest([]byte(label))
	token := mustCustodyToken(t)
	prepare, err := beginPrepareCustody(workspace, planDigest, token)
	if err != nil {
		t.Fatal(err)
	}
	if err := prepare.Drain(""); err != nil {
		t.Fatal(err)
	}
	if err := prepare.Close(); err != nil {
		t.Fatal(err)
	}
	execute, err := beginExecuteCustody(workspace, planDigest, token)
	if err != nil {
		t.Fatal(err)
	}
	return workspace, planDigest, token, execute
}

func assertRecoveredCustodyStatus(
	t *testing.T,
	workspace, planDigest, token string,
	operation custodyOperation,
	checkpoint string,
	want custodyStatus,
) {
	t.Helper()
	status, supervision, err := inspectCustodySupervision(
		workspace, planDigest, token, operation, checkpoint,
	)
	if err != nil || status != want || supervision == nil {
		t.Fatalf("recovered custody: status=%q held=%t err=%v", status, supervision != nil, err)
	}
	if err := supervision.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCustodyCreationStageCannotBeMistakenForRetirement(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	planDigest := digest([]byte("unpublished-creation"))
	token := mustCustodyToken(t)
	workspace, control, err := custodyControlDirectory(workspace)
	if err != nil {
		t.Fatal(err)
	}
	stage := control + custodyCreatingInfix + token
	if err := os.Mkdir(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	expected := custodyControlState{
		Schema: custodyControlSchema, Token: token, PlanDigest: planDigest,
		Workspace: workspace, Operation: custodyOperationPrepare, Phase: custodyPhaseTerminal,
	}
	if err := confirmAtomicTestRetirement(expected); err == nil {
		t.Fatal("unpublished creation was mistaken for retired custody")
	}
	if _, err := os.Lstat(stage); err != nil {
		t.Fatalf("unpublished creation authority was removed: %v", err)
	}
}

func TestCustodyRetirementRecoversEveryCommittedBoundary(t *testing.T) {
	for _, boundary := range []string{
		"renamed", "lease-removed", "locks-removed", "authority-moved", "directory-removed",
	} {
		t.Run(boundary, func(t *testing.T) {
			supervision, control := terminalCustodyForAtomicTest(t, boundary)
			defer func() { _ = supervision.Close() }()
			retiring := control + custodyRetiringSuffix
			retired := control + custodyRetiredSuffix
			if err := os.Rename(control, retiring); err != nil {
				t.Fatal(err)
			}
			if boundary == "lease-removed" || boundary == "locks-removed" ||
				boundary == "authority-moved" || boundary == "directory-removed" {
				if err := os.Remove(filepath.Join(retiring, custodyLeaseName)); err != nil {
					t.Fatal(err)
				}
			}
			if boundary == "locks-removed" || boundary == "authority-moved" ||
				boundary == "directory-removed" {
				if err := os.Remove(filepath.Join(retiring, custodyControllerName)); err != nil {
					t.Fatal(err)
				}
			}
			if boundary == "authority-moved" || boundary == "directory-removed" {
				if err := os.Rename(filepath.Join(retiring, custodyStateName), retired); err != nil {
					t.Fatal(err)
				}
			}
			if boundary == "directory-removed" {
				if err := os.Remove(retiring); err != nil {
					t.Fatal(err)
				}
			}

			if err := confirmAtomicTestRetirement(supervision.state); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{control, retiring, retired} {
				if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("retirement residue %q survived: %v", path, err)
				}
			}
		})
	}
}

func TestCustodyRetirementRetainsAuthorityOnCleanupFailure(t *testing.T) {
	supervision, control := terminalCustodyForAtomicTest(t, "remove-failure")
	defer func() { _ = supervision.Close() }()
	retiring := control + custodyRetiringSuffix
	if err := os.Rename(control, retiring); err != nil {
		t.Fatal(err)
	}
	unexpected := filepath.Join(retiring, "unexpected")
	writeEmptyCustodyFile(t, unexpected)
	if err := confirmAtomicTestRetirement(supervision.state); err == nil {
		t.Fatal("retirement unexpectedly ignored foreign residue")
	}
	if _, err := os.Lstat(filepath.Join(retiring, custodyStateName)); err != nil {
		t.Fatalf("terminal retirement authority was lost: %v", err)
	}
	if err := os.Remove(unexpected); err != nil {
		t.Fatal(err)
	}
	if err := confirmAtomicTestRetirement(supervision.state); err != nil {
		t.Fatal(err)
	}
}

func TestCustodyRetirementRejectsDifferentRecoveryAuthority(t *testing.T) {
	supervision, control := terminalCustodyForAtomicTest(t, "identity-mismatch")
	defer func() { _ = supervision.Close() }()
	retiring := control + custodyRetiringSuffix
	if err := os.Rename(control, retiring); err != nil {
		t.Fatal(err)
	}
	wrong := supervision.state
	wrong.Token = mustCustodyToken(t)
	if err := confirmAtomicTestRetirement(wrong); err == nil {
		t.Fatal("retirement accepted a different external authority")
	}
	if _, err := os.Lstat(filepath.Join(retiring, custodyStateName)); err != nil {
		t.Fatalf("mismatched retirement destroyed terminal authority: %v", err)
	}
	if err := confirmAtomicTestRetirement(supervision.state); err != nil {
		t.Fatal(err)
	}
}

func terminalCustodyForAtomicTest(t *testing.T, label string) (*custodySupervision, string) {
	t.Helper()
	workspace := filepath.Join(t.TempDir(), "workspace")
	planDigest := digest([]byte("retirement-" + label))
	supervision, err := beginPrepareCustody(workspace, planDigest, mustCustodyToken(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := supervision.Drain(""); err != nil {
		t.Fatal(err)
	}
	if err := supervision.BeginFinalization(""); err != nil {
		t.Fatal(err)
	}
	if err := supervision.DrainTerminal(); err != nil {
		t.Fatal(err)
	}
	return supervision, supervision.directory
}

func confirmAtomicTestRetirement(state custodyControlState) error {
	return confirmCustodySupervisionRetired(
		state.Workspace, state.PlanDigest, state.Token, state.Operation, state.CheckpointDigest,
	)
}

func writeEmptyCustodyFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeCustodyTransitionStage(t *testing.T, directory string, state custodyControlState) {
	t.Helper()
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(filepath.Join(directory, custodyStateName+".tmp"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertNoCustodyTransitionStage(t *testing.T, directory string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(directory, custodyStateName+".tmp")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("uncommitted custody state survived: %v", err)
	}
}

func stageAtomicCustodyCreation(
	t *testing.T,
	boundary string,
) (workspace, planDigest, token, control, stage string) {
	t.Helper()
	workspace = filepath.Join(t.TempDir(), "workspace")
	planDigest = digest([]byte("creation-" + boundary))
	token = mustCustodyToken(t)
	var err error
	workspace, control, err = custodyControlDirectory(workspace)
	if err != nil {
		t.Fatal(err)
	}
	stage = control + custodyCreatingInfix + token
	state := custodyControlState{
		Schema: custodyControlSchema, Token: token, PlanDigest: planDigest,
		Workspace: workspace, Operation: custodyOperationPrepare, Phase: custodyPhaseCreated,
	}
	if boundary != "none" {
		if err := os.Mkdir(stage, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if boundary == "controller" || boundary == "locks" || boundary == "state-stage" ||
		boundary == "state" || boundary == "published" {
		writeEmptyCustodyFile(t, filepath.Join(stage, custodyControllerName))
	}
	if boundary == "locks" || boundary == "state-stage" || boundary == "state" ||
		boundary == "published" {
		writeEmptyCustodyFile(t, filepath.Join(stage, custodyLeaseName))
	}
	if boundary == "state-stage" || boundary == "state" || boundary == "published" {
		if err := writeCustodyState(stage, state); err != nil {
			t.Fatal(err)
		}
	}
	if boundary == "state-stage" {
		if err := os.Rename(
			filepath.Join(stage, custodyStateName), filepath.Join(stage, custodyStateName+".tmp"),
		); err != nil {
			t.Fatal(err)
		}
	}
	if boundary == "published" {
		if err := os.Rename(stage, control); err != nil {
			t.Fatal(err)
		}
	}
	return workspace, planDigest, token, control, stage
}
