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

func TestObserveProcessTreeIncludesCurrentProcess(t *testing.T) {
	observed, err := ObserveProcessTree(t.Context(), os.Getpid())
	if err != nil || observed.RSSBytes <= 0 || observed.Descendants < 0 {
		t.Fatalf("process tree = %+v, %v", observed, err)
	}
}

func TestSummarizeProcessTreeAcceptsZeroResidentChild(t *testing.T) {
	const rootPID, childPID = 41, 42
	processes := map[int]processSnapshot{
		rootPID:  {rssBytes: 4096},
		childPID: {parent: rootPID},
	}
	observed, err := summarizeProcessTree(rootPID, []int{rootPID, childPID}, processes)
	if err != nil || observed.RSSBytes != 4096 || observed.Descendants != 1 {
		t.Fatalf("zero-resident child process tree = %+v, %v", observed, err)
	}
	delete(processes, childPID)
	if _, err := summarizeProcessTree(rootPID, []int{rootPID, childPID}, processes); err == nil {
		t.Fatal("missing child process record passed")
	}
}

func TestObserveProcessTreeTracksDescendantExit(t *testing.T) {
	child := exec.Command("/bin/sleep", "30")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if child.ProcessState == nil {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	}()
	observed, err := ObserveProcessTree(t.Context(), os.Getpid())
	if err != nil || observed.Descendants < 1 {
		t.Fatalf("live descendant process tree = %+v, %v", observed, err)
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err == nil {
		t.Fatal("killed child returned success")
	}
	deadline := time.Now().Add(time.Second)
	for {
		observed, err = ObserveProcessTree(t.Context(), os.Getpid())
		if err == nil && observed.Descendants == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("exited descendant process tree = %+v, %v", observed, err)
		}
		time.Sleep(10 * time.Millisecond)
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

func TestDarwinNativeSamplerAccountsExecClassEpoch(t *testing.T) {
	command := exec.CommandContext(t.Context(), "/bin/sh", "-c",
		`/bin/sh -c 'sleep 1; exec /usr/bin/git hash-object --stdin' child <&0 & wait`)
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := isolatePrivateServerSession(command); err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = input.Close()
		_ = killPrivateServerSession(command.Process.Pid)
		_ = command.Wait()
		_ = finishCustodyCommandSession(command.Process.Pid)
	}()

	sampler := newRSSSampler(command.Process.Pid, true)
	sampler.captureRootIdentity()
	seenOther := make(map[processIdentity]struct{})
	transitioned := false
	deadline := time.Now().Add(5 * time.Second)
	for !transitioned && time.Now().Before(deadline) {
		sampler.sample()
		sampler.mu.Lock()
		for _, child := range sampler.activeChildren {
			if child.class == processClassOther {
				seenOther[child.identity] = struct{}{}
			}
			if child.class == processClassGit {
				_, transitioned = seenOther[child.identity]
			}
		}
		sampler.mu.Unlock()
		if !transitioned {
			time.Sleep(20 * time.Millisecond)
		}
	}
	sampler.sample()
	metrics, err := sampler.phaseMetrics()
	if err != nil || !transitioned || metrics.GitChildren != 1 || metrics.OtherChildren == 0 ||
		metrics.OtherToGitTransitions != 1 || sampler.failedSamples != 0 || sampler.samples < 2 {
		t.Fatalf("native exec epochs = transitioned:%t metrics:%+v samples:%d failed:%d err=%v",
			transitioned, metrics, sampler.samples, sampler.failedSamples, err)
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
		missing, err := reconcileDarwinDeniedChild(11, 10, func(int) (int, error) {
			return 9, nil
		})
		if err != nil || !missing {
			t.Fatalf("denied reused PID = missing:%t err:%v", missing, err)
		}
		missing, err = reconcileDarwinDeniedChild(11, 10, func(int) (int, error) {
			return 10, nil
		})
		if err != nil || missing {
			t.Fatalf("denied current child = missing:%t err:%v", missing, err)
		}
		pids, processes, err := collectDarwinProcessSnapshot(
			t.Context(), 10,
			func(pid int) ([]int, error) {
				if pid == 10 {
					return []int{1 << 30}, nil
				}
				return nil, nil
			},
			func(pid int) (processSnapshot, error) {
				if pid == 10 {
					return root, nil
				}
				return processSnapshot{}, unix.EPERM
			},
		)
		if err != nil || !slices.Equal(pids, []int{10}) || len(processes) != 1 {
			t.Fatalf("denied missing child = pids:%v processes:%v err:%v", pids, processes, err)
		}
	})
}

func TestDarwinRootPermissionDenialIsSticky(t *testing.T) {
	observations := 0
	_, _, err := collectDarwinProcessSnapshot(
		t.Context(), 10,
		func(int) ([]int, error) { return nil, nil },
		func(int) (processSnapshot, error) {
			observations++
			return processSnapshot{}, unix.EPERM
		},
	)
	if !errors.Is(err, unix.EPERM) || observations != 1 {
		t.Fatalf("root permission denial = observations:%d err:%v", observations, err)
	}
}
