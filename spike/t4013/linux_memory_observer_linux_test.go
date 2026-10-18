//go:build linux

package t4013

import (
	"os"
	"runtime"
	"strconv"
	"testing"
)

// These explicit target-host tests are authored, not qualified on the Mac.
// Ben deferred Linux execution until October 20. A compile-only result never
// satisfies this opt-in native gate or the parent H2 readiness gate.
func requireLinuxMemoryNative(t *testing.T) {
	t.Helper()
	if os.Getenv("T42H2L_NATIVE") != "1" {
		t.Skip("unrun Linux target gate: set T42H2L_NATIVE=1 only for the authorized October 20 native qualification")
	}
}

func TestLinuxMemoryObserverNative(t *testing.T) {
	requireLinuxMemoryNative(t)
	observation, err := ObserveLinuxMemory(t.Context())
	if err != nil {
		t.Fatal("target observer refused; no native qualification", err)
	}
	if !observation.CgroupAncestryComplete || len(observation.CgroupAncestors) == 0 ||
		!observation.CgroupAncestors[0].HierarchyRoot {
		t.Fatal("native observer omitted the real hierarchy root")
	}
	if _, _, err := linuxMemoryCapacity(observation); err != nil {
		t.Fatal("native facts refused by the memory model", err)
	}
	t.Logf("sampled physical=%d available=%d ancestors=%d; no reservation/admission claim",
		observation.PhysicalBytes, observation.AvailableBytes, len(observation.CgroupAncestors))
}

func TestLinuxMemoryObserverNativeCurrentTaskAnchor(t *testing.T) {
	requireLinuxMemoryNative(t)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	source, err := newLinuxMemoryNativeSource()
	if err != nil {
		t.Fatal("native proc anchor unavailable", err)
	}
	defer func() {
		if err := source.close(); err != nil {
			t.Error(err)
		}
	}()
	if err := linuxMemoryCurrentLink(source.proc, source.procObject, "self", strconv.Itoa(os.Getpid()+1)); err == nil {
		t.Fatal("another translated TGID was accepted as the caller")
	}
	if err := linuxMemoryCurrentLink(source.proc, source.procObject, "thread-self", strconv.Itoa(os.Getpid())+"/task/0"); err == nil {
		t.Fatal("another translated TID was accepted as the caller")
	}
}
