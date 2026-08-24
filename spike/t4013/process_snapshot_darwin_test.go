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
