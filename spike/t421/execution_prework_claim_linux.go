//go:build linux

package t421

import (
	"sync"
	"sync/atomic"
)

// Issued only with the successful protected executor reference publication.
// Copies and new wrappers share the same spent state and exact original owners.
type linuxExecutorPreworkClaim struct {
	mu       sync.Mutex
	used     atomic.Bool
	tool     *LinuxExecutionToolCustody
	builds   *LinuxExecutionGoBuildCustody
	outer    *linuxExecutorPrework
	inner    *linuxExecutorInnerPrework
	pressure bool
}

func (claim *linuxExecutorPreworkClaim) bindOuter(owner *linuxExecutorPrework) {
	claim.bindOuterPurpose(owner, false)
}

func (claim *linuxExecutorPreworkClaim) bindPressureOuter(owner *linuxExecutorPrework) {
	claim.bindOuterPurpose(owner, true)
}

func (claim *linuxExecutorPreworkClaim) bindOuterPurpose(owner *linuxExecutorPrework, pressure bool) {
	if claim == nil {
		return
	}
	claim.mu.Lock()
	defer claim.mu.Unlock()
	if claim.outer == nil && claim.inner == nil && claim.tool == owner.tool && claim.builds == owner.builds {
		claim.outer = owner
		claim.pressure = pressure
	}
}

func (claim *linuxExecutorPreworkClaim) consumeOuter(owner *linuxExecutorPrework) bool {
	if claim == nil || !claim.used.CompareAndSwap(false, true) {
		return false
	}
	claim.mu.Lock()
	defer claim.mu.Unlock()
	return claim.outer == owner && claim.inner == nil && claim.tool == owner.tool && claim.builds == owner.builds && claim.pressure == (owner.pressure != nil)
}

func (claim *linuxExecutorPreworkClaim) bindInner(owner *linuxExecutorInnerPrework) bool {
	return claim.bindInnerPurpose(owner, false)
}

func (claim *linuxExecutorPreworkClaim) bindPressureInner(owner *linuxExecutorInnerPrework) bool {
	return claim.bindInnerPurpose(owner, true)
}

func (claim *linuxExecutorPreworkClaim) bindInnerPurpose(owner *linuxExecutorInnerPrework, pressure bool) bool {
	if claim == nil {
		return false
	}
	claim.mu.Lock()
	defer claim.mu.Unlock()
	if claim.outer != nil || claim.inner != nil || claim.tool != owner.tool || claim.builds != owner.builds {
		return false
	}
	claim.inner = owner
	claim.pressure = pressure
	return true
}

func (claim *linuxExecutorPreworkClaim) consumeInner(owner *linuxExecutorInnerPrework) bool {
	return claim.consumeInnerPurpose(owner, false)
}

func (claim *linuxExecutorPreworkClaim) consumePressureInner(owner *linuxExecutorInnerPrework) bool {
	return claim.consumeInnerPurpose(owner, true)
}

func (claim *linuxExecutorPreworkClaim) consumeInnerPurpose(owner *linuxExecutorInnerPrework, pressure bool) bool {
	if claim == nil || !claim.used.CompareAndSwap(false, true) {
		return false
	}
	claim.mu.Lock()
	defer claim.mu.Unlock()
	return claim.inner == owner && claim.outer == nil && claim.tool == owner.tool && claim.builds == owner.builds && claim.pressure == pressure
}
