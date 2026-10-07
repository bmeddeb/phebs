//go:build darwin || linux

package t421

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func newExecutionSignerNamespaceTestBinding(t *testing.T) executionSignerNamespaceBinding {
	t.Helper()
	root := filepath.Join(t.TempDir(), "signer")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	custody, err := holdExecutionSignerNamespace(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = custody.Close() })
	binding, err := custody.check(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return binding
}

func TestExecutionSignerNamespaceRetainsExactPrivateDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "signer")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	custody, err := holdExecutionSignerNamespace(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := custody.check(context.Background())
	if err != nil || !binding.valid() {
		t.Fatal("exact signer namespace was not retained", err)
	}
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := custody.check(context.Background()); err == nil {
		t.Fatal("changed signer namespace mode remained admitted")
	}
	if _, err := binding.recheck(context.Background()); err == nil {
		t.Fatal("stale signer namespace binding remained admitted")
	}
	if err := custody.Close(); err != nil || binding.valid() {
		t.Fatal("closed signer namespace retained authority", err)
	}
}

func TestExecutionSignerNamespaceRejectsSymlink(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "signer")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(parent, "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	if custody, err := holdExecutionSignerNamespace(context.Background(), link); err == nil || custody != nil {
		t.Fatal("symlink signer namespace was admitted")
	}
}

func TestExecutionFreezeReceiptBindingRechecksSignerNamespace(t *testing.T) {
	plan := accountingTestPlan(t)
	commits := executionFreezeTestCommits()
	tools, host := executionFreezeTestTools(plan, commits), executionFreezeTestHost()
	namespace := newExecutionSignerNamespaceTestBinding(t)
	profile := executionProfileTestAdmission(t, plan, tools, host, namespace.digest)
	checkout := executionFreezeTestCheckout(t, commits, tools)
	freeze, err := BuildExecutionFreeze(plan, commits, tools, host, executionFreezeTestSigner(), checkout, profile)
	if err != nil {
		t.Fatal(err)
	}
	admission := executionFreezeTestAdmission(t, plan, freeze, namespace)
	if _, err := BindExecutionFreezeForReceipt(freeze, plan, commits, executionFreezeTestSigner(), checkout, profile, admission); err != nil {
		t.Fatal("live signer namespace was refused", err)
	}
	if err := os.Chmod(namespace.owner.path, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := BindExecutionFreezeForReceipt(freeze, plan, commits, executionFreezeTestSigner(), checkout, profile, admission); err == nil {
		t.Fatal("stale signer namespace remained receipt-authoritative")
	}
}
