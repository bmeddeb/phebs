//go:build darwin

package t4013

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"slices"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestDarwinProcessObservationIsCoherent(t *testing.T) {
	observed, err := darwinProcessObservation(os.Getpid())
	if err != nil || !observed.coherent || observed.identityToken == "" ||
		observed.parent != os.Getppid() || observed.name == "" || observed.rssBytes <= 0 {
		t.Fatalf("native process observation = %+v, %v", observed, err)
	}
	identity, err := processStartIdentity(os.Getpid(), processSnapshot{})
	if err != nil || observed.identityToken != identity.token || observed.parent != identity.parent ||
		classifyProcess(observed.name) != classifyProcess(identity.name) {
		t.Fatalf("native/sysctl identity mismatch = native:%+v sysctl:%+v err:%v",
			observed, identity, err)
	}
}

func TestDarwinChildPIDsAndMissingObservation(t *testing.T) {
	child := exec.Command("/bin/sleep", "30")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = child.Process.Kill()
		_ = child.Wait()
	}()
	children, err := darwinChildPIDs(os.Getpid())
	if err != nil || !slices.Contains(children, child.Process.Pid) {
		t.Fatalf("native children = %v, %v; missing %d", children, err, child.Process.Pid)
	}
	if _, err := darwinProcessObservation(1 << 30); !errors.Is(err, errProcessIdentityMissing) {
		t.Fatalf("missing native process = %v", err)
	}
	if parent, err := darwinProcessShortParent(os.Getpid()); err != nil || parent != os.Getppid() {
		t.Fatalf("native short parent = %d, %v", parent, err)
	}
}

func TestDarwinNativeSamplerSurvivesSustainedChildChurn(t *testing.T) {
	command := exec.CommandContext(context.Background(), "/bin/sh", "-c", "sleep 30 & while :; do /usr/bin/true; done")
	if err := isolatePrivateServerSession(command); err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = killPrivateServerSession(command.Process.Pid)
		_ = command.Wait()
		_ = finishCustodyCommandSession(command.Process.Pid)
	}()
	time.Sleep(10 * time.Millisecond)
	sampler := newRSSSampler(command.Process.Pid, true)
	sampler.captureRootIdentity()
	for range 50 {
		sampler.sample()
	}
	peak, _, _, otherChildren, err := sampler.metrics()
	if err != nil || sampler.samples != 50 || peak <= 0 || otherChildren == 0 {
		t.Fatalf("native churn sample = samples:%d peak:%d other:%d err:%v",
			sampler.samples, peak, otherChildren, err)
	}
}

func TestDarwinSnapshotRefusesParentDriftAndCandidateOverflow(t *testing.T) {
	root := processSnapshot{parent: 1, name: "phebs", identityToken: "root", coherent: true}
	t.Run("parent-drift", func(t *testing.T) {
		_, _, err := collectDarwinProcessSnapshot(
			t.Context(), 10,
			func(pid int) ([]int, error) {
				if pid == 10 {
					return []int{11}, nil
				}
				return nil, nil
			},
			func(pid int) (processSnapshot, error) {
				if pid == 10 {
					return root, nil
				}
				return processSnapshot{parent: 9, name: "git", identityToken: "child", coherent: true}, nil
			},
		)
		if err == nil || err.Error() != "T40.13 native process parent changed during observation" {
			t.Fatalf("parent drift = %v", err)
		}
	})
	t.Run("candidate-overflow", func(t *testing.T) {
		children := make([]int, maxProcessDescendants)
		for index := range children {
			children[index] = index + 11
		}
		observations := 0
		_, _, err := collectDarwinProcessSnapshot(
			t.Context(), 10,
			func(pid int) ([]int, error) {
				switch pid {
				case 10:
					return children, nil
				case 11:
					return []int{1000}, nil
				default:
					return nil, nil
				}
			},
			func(pid int) (processSnapshot, error) {
				observations++
				if pid == 10 {
					return root, nil
				}
				parent := 10
				if pid == 1000 {
					parent = 11
				}
				return processSnapshot{parent: parent, name: "git", identityToken: "child", coherent: true}, nil
			},
		)
		if err == nil || err.Error() != "T40.13 native process candidate inventory exceeds its bound" ||
			observations != maxProcessDescendants+1 {
			t.Fatalf("candidate overflow = observations:%d err:%v", observations, err)
		}
	})
	t.Run("denied-reused-pid", func(t *testing.T) {
		pids, processes, err := collectDarwinProcessSnapshot(
			t.Context(), 10,
			func(pid int) ([]int, error) {
				if pid == 10 {
					return []int{11}, nil
				}
				return nil, nil
			},
			func(pid int) (processSnapshot, error) {
				if pid == 10 {
					return root, nil
				}
				return processSnapshot{}, &darwinProcessPermissionError{
					parent: 9, err: unix.EPERM,
				}
			},
		)
		if err != nil || !slices.Equal(pids, []int{10}) || len(processes) != 1 {
			t.Fatalf("denied reused PID = pids:%v processes:%v err:%v", pids, processes, err)
		}
	})
}
