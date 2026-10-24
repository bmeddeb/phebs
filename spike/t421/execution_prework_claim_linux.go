//go:build linux

package t421

import (
	"sync"
	"sync/atomic"
)

// Issued only with the successful protected executor reference publication.
// Copies and new wrappers share the same spent state and exact original owners.
type linuxExecutorPreworkClaim struct {
	mu     sync.Mutex
	used   atomic.Bool
	tool   *LinuxExecutionToolCustody
	builds *LinuxExecutionGoBuildCustody
	outer  *linuxExecutorPrework
	inner  *linuxExecutorInnerPrework
}

func (claim *linuxExecutorPreworkClaim) bindOuter(owner *linuxExecutorPrework) {
	if claim == nil {
		return
	}
	claim.mu.Lock()
	defer claim.mu.Unlock()
	if claim.outer == nil && claim.inner == nil && claim.tool == owner.tool && claim.builds == owner.builds {
		claim.outer = owner
	}
}

func (claim *linuxExecutorPreworkClaim) consumeOuter(owner *linuxExecutorPrework) bool {
	if claim == nil || !claim.used.CompareAndSwap(false, true) {
		return false
	}
	claim.mu.Lock()
	defer claim.mu.Unlock()
	return claim.outer == owner && claim.inner == nil && claim.tool == owner.tool && claim.builds == owner.builds
}

func (claim *linuxExecutorPreworkClaim) bindInner(owner *linuxExecutorInnerPrework) bool {
	if claim == nil {
		return false
	}
	claim.mu.Lock()
	defer claim.mu.Unlock()
	if claim.outer != nil || claim.inner != nil || claim.tool != owner.tool || claim.builds != owner.builds {
		return false
	}
	claim.inner = owner
	return true
}

func (claim *linuxExecutorPreworkClaim) consumeInner(owner *linuxExecutorInnerPrework) bool {
	if claim == nil || !claim.used.CompareAndSwap(false, true) {
		return false
	}
	claim.mu.Lock()
	defer claim.mu.Unlock()
	return claim.inner == owner && claim.outer == nil && claim.tool == owner.tool && claim.builds == owner.builds
}
