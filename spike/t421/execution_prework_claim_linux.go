//go:build linux

package t421

import (
	"sync"
	"sync/atomic"
)

// Issued only with the successful protected executor reference publication.
// Copies and new wrappers share the same spent state and exact original owners.
type linuxExecutorPreworkClaim struct {
	mu      sync.Mutex
	used    atomic.Bool
	tool    *LinuxExecutionToolCustody
	builds  *LinuxExecutionGoBuildCustody
	outer   *linuxExecutorPrework
	inner   *linuxExecutorInnerPrework
	purpose uint8
}

func (claim *linuxExecutorPreworkClaim) bindOuter(owner *linuxExecutorPrework) {
	claim.bindOuterPurpose(owner, 0)
}

func (claim *linuxExecutorPreworkClaim) bindPressureOuter(owner *linuxExecutorPrework) {
	claim.bindOuterPurpose(owner, 1)
}

func (claim *linuxExecutorPreworkClaim) bindOuterPurpose(owner *linuxExecutorPrework, purpose uint8) {
	if claim == nil {
		return
	}
	claim.mu.Lock()
	defer claim.mu.Unlock()
	if claim.outer == nil && claim.inner == nil && claim.tool == owner.tool && claim.builds == owner.builds {
		claim.outer = owner
		claim.purpose = purpose
	}
}

func (claim *linuxExecutorPreworkClaim) consumeOuter(owner *linuxExecutorPrework) bool {
	if claim == nil || !claim.used.CompareAndSwap(false, true) {
		return false
	}
	claim.mu.Lock()
	defer claim.mu.Unlock()
	return claim.outer == owner && claim.inner == nil && claim.tool == owner.tool && claim.builds == owner.builds && claim.purpose == linuxExecutorPreworkPurpose(owner)
}

func (claim *linuxExecutorPreworkClaim) bindInner(owner *linuxExecutorInnerPrework) bool {
	return claim.bindInnerPurpose(owner, 0)
}

func (claim *linuxExecutorPreworkClaim) bindPressureInner(owner *linuxExecutorInnerPrework) bool {
	return claim.bindInnerPurpose(owner, 1)
}

func (claim *linuxExecutorPreworkClaim) bindInnerPurpose(owner *linuxExecutorInnerPrework, purpose uint8) bool {
	if claim == nil {
		return false
	}
	claim.mu.Lock()
	defer claim.mu.Unlock()
	if claim.outer != nil || claim.inner != nil || claim.tool != owner.tool || claim.builds != owner.builds {
		return false
	}
	claim.inner = owner
	claim.purpose = purpose
	return true
}

func (claim *linuxExecutorPreworkClaim) consumeInner(owner *linuxExecutorInnerPrework) bool {
	return claim.consumeInnerPurpose(owner, 0)
}

func (claim *linuxExecutorPreworkClaim) consumePressureInner(owner *linuxExecutorInnerPrework) bool {
	return claim.consumeInnerPurpose(owner, 1)
}

func (claim *linuxExecutorPreworkClaim) consumeInnerPurpose(owner *linuxExecutorInnerPrework, purpose uint8) bool {
	if claim == nil || !claim.used.CompareAndSwap(false, true) {
		return false
	}
	claim.mu.Lock()
	defer claim.mu.Unlock()
	return claim.inner == owner && claim.outer == nil && claim.tool == owner.tool && claim.builds == owner.builds && claim.purpose == purpose
}

func linuxExecutorPreworkPurpose(owner *linuxExecutorPrework) uint8 {
	if owner.pressure == nil {
		return 0
	}
	if owner.pressure.author != nil {
		return 2
	}
	return 1
}
func (claim *linuxExecutorPreworkClaim) bindAuthorOuter(owner *linuxExecutorPrework) {
	claim.bindOuterPurpose(owner, 2)
}
func (claim *linuxExecutorPreworkClaim) bindAuthorInner(owner *linuxExecutorInnerPrework) bool {
	return claim.bindInnerPurpose(owner, 2)
}
func (claim *linuxExecutorPreworkClaim) consumeAuthorInner(owner *linuxExecutorInnerPrework) bool {
	return claim.consumeInnerPurpose(owner, 2)
}
