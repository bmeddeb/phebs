//go:build linux

package t421

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxGoReferenceCloseRetainsUnjoinedConsumer(t *testing.T) {
	custody := newLinuxGoOwnerCleanupFixture(t)
	canceled := false
	attempt := &linuxGoReferenceAttempt{done: make(chan struct{}), cancel: func() { canceled = true },
		tree: &LinuxExecutionInputTree{pid: 42}}
	custody.active = attempt
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if custody.Close(ctx) != ErrExecutionGoBuildCustody || !canceled || custody.active != attempt || custody.released {
		t.Fatal("consumer deadline lost retained ownership")
	}
	if _, err := os.Lstat(custody.directory); err != nil {
		t.Fatal("unjoined consumer lost workspace")
	}
	close(attempt.done) // Modeled completion, deliberately missing drain facts.
	if custody.Close(t.Context()) != ErrExecutionGoBuildCustody || custody.active != attempt || custody.released {
		t.Fatal("done channel substituted for actual root/session drain")
	}
}

func TestLinuxGoReferenceBusyOwnerDoesNotEnterTreeLoan(t *testing.T) {
	custody := &LinuxExecutionGoBuildCustody{tree: &LinuxExecutionInputTree{}, active: &linuxGoReferenceAttempt{}}
	if custody.Check(t.Context()) != ErrExecutionGoBuildCustody || custody.err != nil {
		t.Fatal("busy check entered or poisoned the tree")
	}
	if _, err := custody.CheckGo(t.Context()); err != ErrExecutionGoBuildCustody {
		t.Fatal("busy owner published driver identity")
	}
}

func TestLinuxGoReferenceCloseReleasesStickyCandidate(t *testing.T) {
	custody := newLinuxGoOwnerCleanupFixture(t)
	source := filepath.Join(t.TempDir(), "candidate-bytes")
	raw := []byte("cleanup fixture only, no executable identity")
	if os.WriteFile(source, raw, 0o400) != nil {
		t.Fatal("candidate fixture")
	}
	input, err := ProtectLinuxExecutionInputs(t.Context(), []ExecutionInputCopy{{Name: "t422-author", Path: source, SHA256: SHA256(raw)}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = input.Close() })
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := input.Check(ctx, "t422-author"); err != ErrExecutionInputCustody {
		t.Fatal("fixture cancellation did not latch")
	}
	_, attemptCancel := context.WithCancel(t.Context())
	attempt := &linuxGoReferenceAttempt{done: make(chan struct{}), cancel: attemptCancel, tool: &LinuxExecutionToolCustody{input: input}}
	if err := custody.prepareGoReferenceAttempt(attempt); err != nil {
		t.Fatal(err)
	}
	close(attempt.done) // No native process started in this cleanup control.
	custody.active = attempt
	if custody.Close(t.Context()) != ErrExecutionGoBuildCustody || !custody.released || !attempt.released || !input.closed || attempt.tool != nil {
		t.Fatal("sticky refusal concealed safe resource/workspace release")
	}
	if _, err := os.Lstat(custody.directory); !os.IsNotExist(err) {
		t.Fatal("joined candidate cleanup retained scratch", err)
	}
}

func TestLinuxGoReferenceOutputCleanupRefusesReplacedRoot(t *testing.T) {
	path := t.TempDir()
	if os.Chmod(path, 0o700) != nil || os.WriteFile(filepath.Join(path, "reference"), []byte("output"), 0o500) != nil {
		t.Fatal("output")
	}
	expected, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if os.Rename(path, path+"-retained") != nil || os.Mkdir(path, 0o700) != nil {
		t.Fatal("replacement")
	}
	if staging, err := observeGoReferenceOutputs(t.Context(), path, expected); err != ErrExecutionGoBuildCustody || staging != nil {
		t.Fatal("replacement output root adopted")
	}
	if raw, err := os.ReadFile(filepath.Join(path+"-retained", "reference")); err != nil || string(raw) != "output" {
		t.Fatal("retained output lost", err)
	}
}

func TestLinuxGoReferenceCleanupRefusalRetainsCandidateUntilClose(t *testing.T) {
	custody := newLinuxGoOwnerCleanupFixture(t)
	raw := []byte("cleanup fixture only")
	source := filepath.Join(t.TempDir(), "candidate")
	if os.WriteFile(source, raw, 0o400) != nil {
		t.Fatal("candidate")
	}
	input, err := ProtectLinuxExecutionInputs(t.Context(), []ExecutionInputCopy{{Name: "t422-author", Path: source, SHA256: SHA256(raw)}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = input.Close() })
	_, cancel := context.WithCancel(t.Context())
	attempt := &linuxGoReferenceAttempt{done: make(chan struct{}), cancel: cancel, tool: &LinuxExecutionToolCustody{input: input}}
	if custody.prepareGoReferenceAttempt(attempt) != nil {
		t.Fatal("attempt")
	}
	// Model the cleanup owner's sticky error independently of its actual safe
	// resource release. No native process or lineage is issued by this control.
	attempt.tree.err = ErrLinuxInputTree
	close(attempt.done)
	custody.active = attempt
	if custody.closeGoReferenceAttempt(t.Context(), attempt, true) != ErrExecutionGoBuildCustody || custody.err == nil ||
		!attempt.tree.closed || !attempt.released || attempt.tool == nil || input.closed {
		t.Fatal("failed cleanup barrier permitted candidate publication or lost custody")
	}
	if custody.Close(t.Context()) != ErrExecutionGoBuildCustody || !custody.released || !input.closed || attempt.tool != nil {
		t.Fatal("failed publication left the retained sealed candidate unclosed")
	}
}
