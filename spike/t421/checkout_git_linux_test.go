//go:build linux

package t421

import (
	"context"
	"testing"
)

func TestLinuxCheckoutGitRetainsUnjoinedAttempt(t *testing.T) {
	attempt := &linuxCheckoutGitAttempt{done: make(chan struct{}), pid: 42}
	git := &linuxCheckoutGit{active: attempt}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if git.close(ctx) != ErrExecutionGitCustody || git.active != attempt || !git.closing {
		t.Fatal("unfinished attempt lost retained ownership")
	}
	if _, err := git.runInput(t.Context(), "/", nil, 0, "version"); err != ErrExecutionGitCustody || git.active != attempt {
		t.Fatal("closing source transport started another command")
	}
	// These are modeled completion fields, not native join evidence. The
	// required native source/build composition must measure the real joins.
	attempt.joined, attempt.empty = true, true
	close(attempt.done)
	if git.close(t.Context()) != nil || git.active != nil {
		t.Fatal("completed attempt did not release transport state")
	}
}

func TestLinuxCheckoutGitRefusesIncompleteDrainRecord(t *testing.T) {
	for _, flags := range []struct{ joined, empty bool }{{false, false}, {true, false}, {false, true}} {
		attempt := &linuxCheckoutGitAttempt{done: make(chan struct{}), pid: 42, joined: flags.joined, empty: flags.empty}
		close(attempt.done)
		git := &linuxCheckoutGit{active: attempt}
		if git.close(t.Context()) != ErrExecutionGitCustody || git.active != attempt {
			t.Fatal("unsubstantiated session/root drain released state")
		}
	}
}
