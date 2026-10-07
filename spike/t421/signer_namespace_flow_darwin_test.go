//go:build darwin

package t421

import (
	"os"
	"path/filepath"
	"testing"
)

// TestExecutionProfileSignerNamespaceBindingIsOneShot exercises the Darwin-only
// preparation flow that spends the single signer-namespace slot; Linux has no
// equivalent profile/execution-admission state.
func TestExecutionProfileSignerNamespaceBindingIsOneShot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "signer")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	selection, _ := testExecutionSelection(t)
	selection.SignerControlRoot = root
	flow := &ExecutionEpochOne{plan: Plan{Schema: PlanV3Schema}, epochs: &ExecutionEpochConfigCustody{author: &ExecutionAuthorCustody{}}}
	if err := flow.bindProfileSignerNamespace(t.Context(), selection); err != nil || flow.profileSignerNamespace == nil {
		t.Fatal("signer namespace binding failed", err)
	}
	if err := flow.bindProfileSignerNamespace(t.Context(), selection); err == nil {
		t.Fatal("signer namespace binding was reusable")
	}
	binding, err := flow.profileSignerNamespace.check(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := flow.profileSignerNamespace.Close(); err != nil || binding.valid() {
		t.Fatal("closed signer namespace retained binding", err)
	}
}
