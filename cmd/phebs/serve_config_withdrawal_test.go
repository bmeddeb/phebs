package main

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/packrelease"
)

// captureWithdrawalLog returns exactly what logWithdrawnSelection wrote, so
// the operator-facing line is pinned byte for byte rather than only exercised.
func captureWithdrawalLog(t *testing.T, withdrawn []packrelease.Withdrawal) string {
	t.Helper()
	return captureLogDuring(t, func() { logWithdrawnSelection(withdrawn) })
}

// TestRevokedReleaseIDsKeepsTheDarkDefaultAllocationFree pins that an absent
// revocation list reshapes to nil rather than an empty map: the selection
// directory ships dark, and the dark startup must stay allocation-free and
// behaviorally identical to a build with no revocation surface at all.
func TestRevokedReleaseIDsKeepsTheDarkDefaultAllocationFree(t *testing.T) {
	for _, ids := range [][]string{nil, {}} {
		if got := revokedReleaseIDs(ids); got != nil {
			t.Fatalf("revokedReleaseIDs(%#v) = %#v, want nil", ids, got)
		}
	}
	got := revokedReleaseIDs([]string{"rel-0001", "rel-0002"})
	want := map[string]struct{}{"rel-0001": {}, "rel-0002": {}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("revokedReleaseIDs = %#v, want %#v", got, want)
	}
}

func TestLogWithdrawnSelectionNamesPacksAndBoundsItsDetail(t *testing.T) {
	t.Run("no withdrawal logs nothing", func(t *testing.T) {
		if got := captureWithdrawalLog(t, nil); got != "" {
			t.Fatalf("logWithdrawnSelection(nil) wrote %q, want silence so a dark startup is unchanged", got)
		}
	})

	t.Run("one withdrawal names its pack and cause", func(t *testing.T) {
		got := captureWithdrawalLog(t, []packrelease.Withdrawal{
			{PackID: "phebs.suspended.pack", Cause: packrelease.StatusSuspended},
		})
		want := "pack release selection: 1 configured pack(s) not admitted: phebs.suspended.pack=suspended\n"
		if got != want {
			t.Fatalf("log = %q, want %q", got, want)
		}
	})

	t.Run("detail is bounded and the omission counted", func(t *testing.T) {
		total := maxLoggedWithdrawals + 4
		withdrawn := make([]packrelease.Withdrawal, 0, total)
		for i := range total {
			withdrawn = append(withdrawn, packrelease.Withdrawal{
				PackID: fmt.Sprintf("phebs.pack%02d", i), Cause: packrelease.CauseRevoked,
			})
		}
		got := captureWithdrawalLog(t, withdrawn)
		// The count states the whole withdrawal set, not just what was detailed,
		// so a bound on the diagnostic never hides the size of the withdrawal.
		if !strings.Contains(got, fmt.Sprintf("%d configured pack(s) not admitted:", total)) {
			t.Fatalf("log %q must state the whole withdrawal count", got)
		}
		if !strings.Contains(got, ", 4 more omitted") {
			t.Fatalf("log %q must count what the bound omitted", got)
		}
		if !strings.Contains(got, fmt.Sprintf("phebs.pack%02d=revoked", maxLoggedWithdrawals-1)) {
			t.Fatalf("log %q must detail the last entry inside the bound", got)
		}
		if strings.Contains(got, fmt.Sprintf("phebs.pack%02d", maxLoggedWithdrawals)) {
			t.Fatalf("log %q detailed a pack past the %d bound", got, maxLoggedWithdrawals)
		}
		// One pack=cause pair per detailed entry and nothing else: the line
		// discloses no record contents, signature, approval set, referenced
		// artifact or repository source, so a denied pack stays as undisclosed
		// as an absent one.
		if pairs := strings.Count(got, "="); pairs != maxLoggedWithdrawals {
			t.Fatalf("log %q carries %d pack=cause pairs, want %d", got, pairs, maxLoggedWithdrawals)
		}
	})
}
