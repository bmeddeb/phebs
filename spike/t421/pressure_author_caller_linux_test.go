//go:build linux

package t421

import (
	"strings"
	"testing"
)

// Direct canonical/claim shapes only. A genuine private view, pressure volume,
// target exec and author transition remain opt-in native work.
func TestLinuxPressureAuthorCallerClaimAndSelection(t *testing.T) {
	tool, builds := &LinuxExecutionToolCustody{}, &LinuxExecutionGoBuildCustody{}
	claim := &linuxExecutorPreworkClaim{tool: tool, builds: builds}
	tool.executorClaim = claim
	author := linuxPressureAuthorSelection{Schema: executionLinuxPressureAuthorSchema}
	owner := newLinuxPressureAuthorExecutorPrework(builds, tool, author)
	if owner.claim != claim || owner.pressure == nil || owner.pressure.author == nil || claim.outer != owner || claim.purpose != 2 || linuxExecutorPreworkPurpose(owner) != 2 {
		t.Fatal("author outer purpose not bound")
	}
	if !claim.consumeOuter(owner) || claim.consumeOuter(owner) {
		t.Fatal("author outer claim reusable")
	}
	for _, presented := range []*linuxExecutorPrework{
		newLinuxPressureAuthorExecutorPrework(builds, tool, author),
		newLinuxPressureExecutorPrework(builds, tool, linuxPressureBorrowSelection{}),
		newLinuxExecutorPrework(builds, tool, linuxExecutorPreworkSelection{}),
	} {
		if presented.claim.consumeOuter(presented) {
			t.Fatal("spent author claim accepted another mode")
		}
	}

	innerTool, innerBuilds := &LinuxExecutionToolCustody{}, &LinuxExecutionGoBuildCustody{}
	innerClaim := &linuxExecutorPreworkClaim{tool: innerTool, builds: innerBuilds}
	innerTool.executorClaim = innerClaim
	inner := &linuxExecutorInnerPrework{tool: innerTool, builds: innerBuilds}
	if !innerClaim.bindAuthorInner(inner) || innerClaim.purpose != 2 || innerClaim.consumeInner(inner) || !innerClaim.used.Load() || innerClaim.consumeAuthorInner(inner) {
		t.Fatal("author inner purpose accepted another mode")
	}
	freshTool, freshBuilds := &LinuxExecutionToolCustody{}, &LinuxExecutionGoBuildCustody{}
	freshClaim := &linuxExecutorPreworkClaim{tool: freshTool, builds: freshBuilds}
	freshTool.executorClaim = freshClaim
	fresh := &linuxExecutorInnerPrework{tool: freshTool, builds: freshBuilds}
	if !freshClaim.bindAuthorInner(fresh) || !freshClaim.consumeAuthorInner(fresh) {
		t.Fatal("fresh canonical author inner refused")
	}

	value := linuxPressureAuthorSelection{Schema: executionLinuxPressureAuthorSchema}
	encoded, err := encodeLinuxPressureAuthor(value)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeLinuxPressureAuthor(encoded)
	if err != nil || decoded != value {
		t.Fatal("canonical author selection")
	}
	for _, raw := range []string{"", encoded + "=", strings.Repeat("A", maxExecutionSelectionCharacters+1)} {
		if _, err := decodeLinuxPressureAuthor(raw); err == nil {
			t.Fatal("unbounded/noncanonical author selection")
		}
	}
	if linuxPressureAuthorReceipt("a", false) != executionLinuxPressureAuthorSchema+":pressure-not-retained:a\n" ||
		linuxPressureAuthorReceipt("a", true) != executionLinuxPressureAuthorSchema+":actors-retired-pressure-retained:a\n" {
		t.Fatal("author terminal receipt")
	}
	if owner.pressure.receipt("a", false) != linuxPressureAuthorReceipt("a", false) ||
		owner.pressure.receipt("a", true) != linuxPressureAuthorReceipt("a", true) {
		t.Fatal("author prework success receipt")
	}
	ordinary := newLinuxPressureExecutorPrework(builds, tool, linuxPressureBorrowSelection{})
	if ordinary.pressure.receipt("a", false) != linuxPressureBorrowReceipt("a", false) ||
		ordinary.pressure.receipt("a", true) != linuxPressureBorrowReceipt("a", true) {
		t.Fatal("ordinary pressure prework receipt")
	}
}
