package t4013

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const (
	runRootLockHelperMode  = "PHEBS_T4013_RUN_ROOT_LOCK_HELPER"
	runRootLockHelperRoot  = "PHEBS_T4013_RUN_ROOT_LOCK_ROOT"
	runRootLockHelperReady = "PHEBS_T4013_RUN_ROOT_LOCK_READY"
)

func TestRunRootLockCompetingProcessStaleFileAndCrashRelease(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("V25 run-root locking requires Unix flock")
	}
	switch os.Getenv(runRootLockHelperMode) {
	case "hold", "inherited":
		lock, err := lockRunRoot(os.Getenv(runRootLockHelperRoot))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = lock.Close() }()
		if err := os.WriteFile(os.Getenv(runRootLockHelperReady), []byte("ready\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		select {}
	case "adopt":
		if err := ValidateInheritedRunRootLock(os.Getenv(runRootLockHelperRoot)); err != nil {
			t.Fatal(err)
		}
		return
	case "exec":
		command, err := filepath.Abs(os.Args[0])
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Setenv(runRootLockHelperMode, "inherited"); err != nil {
			t.Fatal(err)
		}
		if err := ExecRunRootLocked(
			os.Getenv(runRootLockHelperRoot), command,
			[]string{"-test.run=^TestRunRootLockCompetingProcessStaleFileAndCrashRelease$"},
		); err != nil {
			t.Fatal(err)
		}
		return
	}

	for _, mode := range []string{"hold", "exec"} {
		t.Run(mode, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			child := startRunRootLockHolder(t, root, mode)
			if contender, err := lockRunRoot(root); err == nil {
				_ = contender.Close()
				t.Fatal("competing process acquired the V25 run-root lock")
			} else if !strings.Contains(err.Error(), "already active") {
				t.Fatalf("competing lock error = %v", err)
			}
			if err := child.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			if err := child.Wait(); err == nil {
				t.Fatal("killed run-root lock helper exited successfully")
			}
			lock, err := lockRunRoot(root)
			if err != nil {
				t.Fatalf("process death retained the V25 run-root lock: %v", err)
			}
			if err := lock.Close(); err != nil {
				t.Fatal(err)
			}
			lockPath := filepath.Join(root, runRootLockName)
			info, err := os.Lstat(lockPath)
			if err != nil || !info.Mode().IsRegular() || info.Size() != 0 {
				t.Fatalf("stale lock inode = %v, %v", info, err)
			}
			lock, err = lockRunRoot(root)
			if err != nil {
				t.Fatalf("stale lock inode blocked reuse: %v", err)
			}
			if err := lock.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}

	t.Run("adopt inherited descriptor", func(t *testing.T) {
		root, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		file, err := openRunRootLock(filepath.Join(root, runRootLockName))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = file.Close() }()
		command := exec.Command(
			os.Args[0], "-test.run=^TestRunRootLockCompetingProcessStaleFileAndCrashRelease$",
		)
		command.ExtraFiles = []*os.File{file}
		command.Env = []string{
			runRootLockHelperMode + "=adopt",
			runRootLockHelperRoot + "=" + root,
			runRootLockEnv + "=3",
		}
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("adopt inherited descriptor: %v: %s", err, output)
		}
		if contender, err := lockRunRoot(root); err == nil {
			_ = contender.Close()
			t.Fatal("adopted parent descriptor did not retain the lock")
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		lock, err := lockRunRoot(root)
		if err != nil {
			t.Fatalf("closing adopted parent descriptor retained the lock: %v", err)
		}
		if err := lock.Close(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestV25AdmissionRevalidatesExactPlanBytes(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, planBytes := v25TestPlan(t)
	planPath := filepath.Join(root, "plan.json")
	if err := os.WriteFile(planPath, planBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	identity, _, err := readPlanIdentity(planPath)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := lockRunRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	changed := append(append([]byte(nil), planBytes...), ' ')
	if err := os.WriteFile(planPath, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := identity.revalidate(); err == nil || !strings.Contains(err.Error(), "changed before admission") {
		t.Fatalf("changed plan identity = %v", err)
	}
}

func TestV25PrepareRefusesPreparedOutputInsideCustodyOrModule(t *testing.T) {
	for _, target := range []string{"custody", "module"} {
		t.Run(target, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			module := filepath.Join(root, "module")
			if err := os.Mkdir(module, 0o700); err != nil {
				t.Fatal(err)
			}
			_, planBytes := v25TestPlan(t)
			planPath := filepath.Join(root, "plan.json")
			if err := os.WriteFile(planPath, planBytes, 0o600); err != nil {
				t.Fatal(err)
			}
			workspace := filepath.Join(root, "custody")
			output := workspace
			if target == "module" {
				output = filepath.Join(module, "prepared.json")
			}
			_, err = PrepareToOutput(t.Context(), PrepareRequest{
				ModuleRoot: module, Workspace: workspace, PlanPath: planPath,
				Confirm: PrepareConfirm, BasePort: 41731,
			}, output)
			if err == nil || !strings.Contains(err.Error(), "prepared output is invalid") {
				t.Fatalf("protected prepared output = %v", err)
			}
			for _, path := range []string{workspace, output, workspace + ".t4013-supervision"} {
				if path == module || isWithin(path, module) {
					continue
				}
				if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("refused prepare changed %s: %v", path, statErr)
				}
			}
		})
	}
}

func TestPreparedAdmissionEncodingIsBounded(t *testing.T) {
	prepared := minimalV25Prepared("/bounded-custody", "sha256:"+strings.Repeat("a", 64))
	prepared.Profiles[0].Config = "/" + strings.Repeat("x", MaxObservationBytes)
	if _, err := MarshalPrepared(prepared); err == nil || !strings.Contains(err.Error(), "fixed byte bound") {
		t.Fatalf("oversize prepared admission = %v", err)
	}
}

func TestV25MutatorsShareOneRunRootLock(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("V25 run-root locking requires Unix flock")
	}
	t.Run("prepare execute cleanup destroy", func(t *testing.T) {
		root, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		module := filepath.Join(root, "module")
		workspace := filepath.Join(root, "custody")
		for _, path := range []string{module, workspace} {
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		_, planBytes := v25TestPlan(t)
		planPath := filepath.Join(root, "plan.json")
		if err := os.WriteFile(planPath, planBytes, 0o600); err != nil {
			t.Fatal(err)
		}
		prepared := minimalV25Prepared(workspace, PlanDigest(planBytes))
		preparedRaw, err := MarshalPrepared(prepared)
		if err != nil {
			t.Fatal(err)
		}
		preparedPath := filepath.Join(root, "prepared.json")
		if err := os.WriteFile(preparedPath, preparedRaw, 0o600); err != nil {
			t.Fatal(err)
		}
		_ = startRunRootLockHolder(t, root, "hold")

		otherWorkspace := filepath.Join(root, "other-custody")
		otherPrepared := filepath.Join(root, "other-prepared.json")
		if _, err := PrepareToOutput(t.Context(), PrepareRequest{
			ModuleRoot: module, Workspace: otherWorkspace, PlanPath: planPath,
			Confirm: PrepareConfirm, BasePort: 41731,
		}, otherPrepared); err == nil || !strings.Contains(err.Error(), "already active") {
			t.Fatalf("competing Prepare = %v", err)
		}
		if _, err := newExecution(t.Context(), ExecuteRequest{
			ModuleRoot: module, PlanPath: planPath, Prepared: preparedPath,
			Observation: filepath.Join(root, "observation.json"), Confirm: ExecuteConfirm,
		}, time.Now()); err == nil || !strings.Contains(err.Error(), "already active") {
			t.Fatalf("competing Execute = %v", err)
		}
		if err := CleanupPrepared(module, planPath, preparedPath, CleanupConfirm); err == nil ||
			!strings.Contains(err.Error(), "already active") {
			t.Fatalf("competing Cleanup = %v", err)
		}
		if err := DestroyPrepared(prepared, module); err == nil ||
			!strings.Contains(err.Error(), "already active") {
			t.Fatalf("competing Destroy = %v", err)
		}
		for _, path := range []string{workspace, preparedPath} {
			if _, err := os.Lstat(path); err != nil {
				t.Fatalf("competing mutation removed %s: %v", path, err)
			}
		}
		for _, path := range []string{
			otherWorkspace, otherPrepared, otherPrepared + ".preparing",
			preparedPath + ".preparing", filepath.Join(workspace, executedMarkerName),
			filepath.Join(root, "observation.json"),
		} {
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("competing mutation created %s: %v", path, err)
			}
		}
	})

	t.Run("resume", func(t *testing.T) {
		run := newCompletedTeardownExecution(t)
		if err := run.persistTeardownCheckpoint(time.Now(), 7, 8); err != nil {
			t.Fatal(err)
		}
		root, err := filepath.EvalSymlinks(filepath.Dir(run.workspace))
		if err != nil {
			t.Fatal(err)
		}
		_ = startRunRootLockHolder(t, root, "hold")
		if _, err := ResumeObservation(
			run.observationPath, run.planBytes, PlanDigest(run.planBytes),
		); err == nil || !strings.Contains(err.Error(), "already active") {
			t.Fatalf("competing Resume = %v", err)
		}
		for _, path := range []string{run.workspace, run.observationPath + ".teardown"} {
			if _, err := os.Lstat(path); err != nil {
				t.Fatalf("competing Resume changed %s: %v", path, err)
			}
		}
	})
}

func startRunRootLockHolder(t *testing.T, root, mode string) *exec.Cmd {
	t.Helper()
	ready := filepath.Join(root, "lock-ready-"+mode)
	child := exec.Command(os.Args[0], "-test.run=^TestRunRootLockCompetingProcessStaleFileAndCrashRelease$")
	environment := make([]string, 0, len(os.Environ())+3)
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, runRootLockEnv+"=") &&
			!strings.HasPrefix(value, runRootLockHelperMode+"=") &&
			!strings.HasPrefix(value, runRootLockHelperRoot+"=") &&
			!strings.HasPrefix(value, runRootLockHelperReady+"=") {
			environment = append(environment, value)
		}
	}
	child.Env = append(environment,
		runRootLockHelperMode+"="+mode,
		runRootLockHelperRoot+"="+root,
		runRootLockHelperReady+"="+ready,
	)
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = child.Process.Kill()
		_ = child.Wait()
	})
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Lstat(ready); err == nil {
			return child
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("run-root lock helper did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func v25TestPlan(t *testing.T) (Plan, []byte) {
	t.Helper()
	plan, err := frozenV25PlanWithHostToolchain(testSourceCommit, fakeHostToolchainV25())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := MarshalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	return plan, raw
}

func minimalV25Prepared(workspace, planDigest string) Prepared {
	profile := func(name, lane, port string) PreparedProfile {
		root := filepath.Join(workspace, lane)
		return PreparedProfile{
			Name: name, Repository: filepath.Join(root, "repository.git"),
			RepositoryName: "local.invalid/" + lane,
			Config:         filepath.Join(root, "phebs.yaml"),
			Credential:     filepath.Join(root, "api-key"),
			DataDir:        filepath.Join(root, "data"),
			Address:        "127.0.0.1:" + port,
			Catalog:        filepath.Join(root, "catalog.json"),
			Revisions: map[string]string{
				"a": testSourceCommit, "b": testSourceCommit, "a-return": testSourceCommit,
			},
		}
	}
	return Prepared{
		Schema: PreparedSchemaV2, PlanDigest: planDigest,
		SupervisionToken:        strings.Repeat("a", 64),
		ExecutionControlsSHA256: "sha256:" + strings.Repeat("b", 64),
		Profiles: []PreparedProfile{
			profile("structural-2m-v1", "structural", "41731"),
			profile("semantic-262144-v1", "semantic", "41732"),
		},
	}
}
