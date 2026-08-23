//go:build darwin || linux

package t4013

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	custodyControllerHelper   = "T4013_CUSTODY_CONTROLLER_HELPER"
	custodyIntermediateHelper = "T4013_CUSTODY_INTERMEDIATE_HELPER"
	custodyDescendantHelper   = "T4013_CUSTODY_DESCENDANT_HELPER"
	custodyWorkspaceEnv       = "T4013_CUSTODY_WORKSPACE"
	custodyPlanDigestEnv      = "T4013_CUSTODY_PLAN_DIGEST"
	custodyControllerReady    = "T4013_CUSTODY_CONTROLLER_READY"
	custodyDescendantReady    = "T4013_CUSTODY_DESCENDANT_READY"
	custodyReleaseEnv         = "T4013_CUSTODY_RELEASE"
)

func TestCustodySupervisionLifecycle(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	planDigest := digest([]byte("plan"))
	checkpointDigest := digest([]byte("checkpoint"))
	token := mustCustodyToken(t)

	prepare, err := beginPrepareCustody(workspace, planDigest, token)
	if err != nil {
		t.Fatal(err)
	}
	if prepare.controller.Fd() < custodyMinimumFD || prepare.lease.Fd() < custodyMinimumFD {
		t.Fatalf("custody descriptors are not reserved: controller=%d lease=%d",
			prepare.controller.Fd(), prepare.lease.Fd())
	}
	if err := prepare.Drain(""); err != nil {
		t.Fatal(err)
	}
	if err := prepare.Close(); err != nil {
		t.Fatal(err)
	}

	status, held, err := inspectCustodySupervision(
		workspace, planDigest, token, custodyOperationPrepare, "",
	)
	if err != nil || status != custodyStatusDrained || held == nil {
		t.Fatalf("inspect prepare custody: status=%q held=%t err=%v", status, held != nil, err)
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}

	execute, err := beginExecuteCustody(workspace, planDigest, token)
	if err != nil {
		t.Fatal(err)
	}
	if err := execute.AbortExecuteAdmission(); err != nil {
		t.Fatal(err)
	}
	if err := execute.Close(); err != nil {
		t.Fatal(err)
	}
	execute, err = beginExecuteCustody(workspace, planDigest, token)
	if err != nil {
		t.Fatal(err)
	}
	if err := execute.Drain(checkpointDigest); err != nil {
		t.Fatal(err)
	}
	if err := execute.BeginFinalization(checkpointDigest); err != nil {
		t.Fatal(err)
	}
	release := filepath.Join(filepath.Dir(workspace), "finalizer-release")
	ready := filepath.Join(filepath.Dir(workspace), "finalizer-ready")
	command := exec.Command(os.Args[0], "-test.run=^TestCustodySupervisionHelper$")
	command.Env = append(os.Environ(),
		custodyDescendantHelper+"=1",
		custodyDescendantReady+"="+ready,
		custodyReleaseEnv+"="+release,
	)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	if err := waitForCustodyFile(ready, 5*time.Second); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatal(err)
	}
	if err := execute.DrainTerminal(); !errors.Is(err, errCustodyDescendantsLive) {
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatalf("terminal drain with live finalizer = %v", err)
	}
	if err := os.WriteFile(release, []byte("release\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := execute.DrainTerminal(); err != nil {
		t.Fatal(err)
	}
	if err := execute.Retire(); err != nil {
		t.Fatal(err)
	}
	_, directory, err := custodyControlDirectory(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("terminal custody directory survived retirement: %v", err)
	}
}

func TestCustodySupervisionSurvivesControllerSIGKILL(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	controllerReady := filepath.Join(root, "controller-ready")
	descendantReady := filepath.Join(root, "descendant-ready")
	release := filepath.Join(root, "release")
	planDigest := digest([]byte("hard-death-plan"))

	command := exec.Command(os.Args[0], "-test.run=^TestCustodySupervisionHelper$")
	command.Env = append(os.Environ(),
		custodyControllerHelper+"=1",
		custodyWorkspaceEnv+"="+workspace,
		custodyPlanDigestEnv+"="+planDigest,
		custodyControllerReady+"="+controllerReady,
		custodyDescendantReady+"="+descendantReady,
		custodyReleaseEnv+"="+release,
	)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.WriteFile(release, []byte("release\n"), 0o600)
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()
	if err := waitForCustodyFile(controllerReady, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	tokenBytes, err := os.ReadFile(controllerReady)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimSpace(string(tokenBytes))
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err == nil {
		t.Fatal("controller unexpectedly survived SIGKILL")
	}

	status, held, err := inspectCustodySupervision(
		workspace, planDigest, token, custodyOperationPrepare, "",
	)
	if err != nil || status != custodyStatusLive || held != nil {
		t.Fatalf("inspect escaped descendant: status=%q held=%t err=%v", status, held != nil, err)
	}
	if err := os.WriteFile(release, []byte("release\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		status, held, err = inspectCustodySupervision(
			workspace, planDigest, token, custodyOperationPrepare, "",
		)
		if err == nil && status == custodyStatusIndeterminate && held == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("custody did not become indeterminate: status=%q held=%t err=%v", status, held != nil, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestBeginExecuteCustodyRejectsTerminalWithoutLeakingLocks(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	planDigest := digest([]byte("terminal-prepare-plan"))
	token := mustCustodyToken(t)
	prepare, err := beginPrepareCustody(workspace, planDigest, token)
	if err != nil {
		t.Fatal(err)
	}
	if err := prepare.Drain(""); err != nil {
		t.Fatal(err)
	}
	if err := prepare.BeginFinalization(""); err != nil {
		t.Fatal(err)
	}
	if err := prepare.DrainTerminal(); err != nil {
		t.Fatal(err)
	}
	if err := prepare.Close(); err != nil {
		t.Fatal(err)
	}
	if supervision, err := beginExecuteCustody(workspace, planDigest, token); err == nil || supervision != nil {
		t.Fatalf("begin execute from terminal custody = %v, %v", supervision, err)
	}
	status, held, err := inspectCustodySupervision(
		workspace, planDigest, token, custodyOperationPrepare, "",
	)
	if err != nil || status != custodyStatusTerminal || held == nil {
		t.Fatalf("inspect terminal custody after rejection: status=%q held=%t err=%v",
			status, held != nil, err)
	}
	if err := held.Retire(); err != nil {
		t.Fatal(err)
	}
}

func TestCustodySupervisionHelper(t *testing.T) {
	if os.Getenv(custodyDescendantHelper) == "1" {
		if err := os.WriteFile(os.Getenv(custodyDescendantReady), []byte("ready\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := waitForCustodyFile(os.Getenv(custodyReleaseEnv), 10*time.Second); err != nil {
			t.Fatal(err)
		}
		return
	}
	if os.Getenv(custodyIntermediateHelper) == "1" {
		command := exec.Command(os.Args[0], "-test.run=^TestCustodySupervisionHelper$")
		command.Env = append(os.Environ(), custodyDescendantHelper+"=1")
		command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		if err := waitForCustodyFile(os.Getenv(custodyDescendantReady), 5*time.Second); err != nil {
			t.Fatal(err)
		}
		return
	}
	if os.Getenv(custodyControllerHelper) != "1" {
		return
	}
	supervision, err := beginPrepareCustody(
		os.Getenv(custodyWorkspaceEnv), os.Getenv(custodyPlanDigestEnv), mustCustodyToken(t),
	)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestCustodySupervisionHelper$")
	command.Env = append(os.Environ(), custodyIntermediateHelper+"=1")
	if err := command.Run(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		os.Getenv(custodyControllerReady), []byte(supervision.Token()+"\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := waitForCustodyFile(os.Getenv(custodyReleaseEnv), 30*time.Second); err != nil {
		t.Fatal(err)
	}
}

func mustCustodyToken(t *testing.T) string {
	t.Helper()
	token, err := newCustodyToken()
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func waitForCustodyFile(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("timed out waiting for T40.13 custody helper")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
