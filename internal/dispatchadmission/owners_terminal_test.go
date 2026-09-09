package dispatchadmission

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestOwnersTerminalRetainsExactTurnAndDrainsTails(t *testing.T) {
	for _, slot := range []int{0, 63} {
		t.Run(map[int]string{0: "first", 63: "last"}[slot], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			owners, err := NewOwners(ctx, OwnerLimits{Owners: 64, Requests: 2})
			if err != nil {
				t.Fatal(err)
			}
			var turns [64]OwnerTurn
			for i := range turns {
				turns[i], err = owners.Enter(ctx)
				if err != nil {
					t.Fatal(err)
				}
			}
			request, _ := owners.EnterRequest(ctx)
			lastRequest, _ := owners.EnterRequest(ctx)
			done := make(chan error, 1)
			go func() { done <- turns[slot].FenceTerminal(ctx) }()
			ownerTestWait(t, owners, func() bool { return owners.terminal != 0 })
			if _, err := owners.EnterRequest(ctx); !errors.Is(err, ErrFenced) || owners.Err() != nil {
				t.Fatal("new request entered terminal fence", err)
			}
			parkCtx, stopPark := context.WithCancel(ctx)
			defer stopPark()
			parked := make(chan error, 1)
			go func() { _, err := owners.Enter(parkCtx); parked <- err }()
			ownerTestWait(t, owners, func() bool { return owners.waiters == 1 })
			request.End()
			for i := range turns {
				if i != slot {
					turns[i].End()
				}
			}
			select {
			case err := <-done:
				t.Fatal("terminal fence skipped final request tail", err)
			default:
			}
			lastRequest.End()
			if err := ownerTestResult(t, done); err != nil {
				t.Fatal(err)
			}
			stopPark()
			if err := ownerTestResult(t, parked); !errors.Is(err, context.Canceled) {
				t.Fatal("terminal fence reopened a parked owner", err)
			}
			owners.mu.Lock()
			valid := owners.active == uint64(1)<<slot && owners.terminal == owners.active && owners.requests == 0 &&
				owners.paused && owners.requestsFenced && !owners.pausedReady && !owners.requestsReady && owners.err == nil
			owners.mu.Unlock()
			if !valid {
				t.Fatal("exact terminal hold became ordinary drainage")
			}
			// No End: a successful terminal hold is intentionally not released.
		})
	}
}

func TestOwnersTerminalInvalidAuthorityAndContextLatch(t *testing.T) {
	for _, mode := range []string{"zero", "stale", "request", "slot", "nil", "canceled", "expired", "unbounded"} {
		t.Run(mode, func(t *testing.T) {
			owners := ownerTestNew(t, 1)
			turn, _ := owners.Enter(t.Context())
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			selected := ctx
			candidate := turn
			want := ErrProtocol
			switch mode {
			case "zero":
				candidate.generation = 0
			case "stale":
				turn.End()
				turn, _ = owners.Enter(ctx)
			case "request":
				candidate, _ = owners.EnterRequest(ctx)
			case "slot":
				candidate.slot = 64
			case "nil":
				selected, want = nil, ErrCanceled
			case "canceled":
				cancel()
				want = ErrCanceled
			case "expired":
				var stop context.CancelFunc
				selected, stop = context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
				defer stop()
				want = ErrCanceled
			case "unbounded":
				selected, want = context.Background(), ErrConfig
			}
			if err := candidate.FenceTerminal(selected); !errors.Is(err, want) || !errors.Is(owners.Err(), want) || owners.Context().Err() == nil {
				t.Fatal("invalid terminal authority did not latch", err, owners.Err())
			}
			if owners.active != uint64(1)<<turn.slot || owners.terminal != 0 {
				t.Fatal("invalid turn changed current owner")
			}
		})
	}
	if err := (OwnerTurn{}).FenceTerminal(t.Context()); !errors.Is(err, ErrConfig) {
		t.Fatal("absent authority admitted", err)
	}
}

func TestOwnersTerminalCancellationAndHeldEndRetainPrefix(t *testing.T) {
	for _, mode := range []string{"caller", "owner", "held-end", "copied-acquire"} {
		t.Run(mode, func(t *testing.T) {
			ownerCtx, stopOwner := context.WithCancel(t.Context())
			defer stopOwner()
			owners, err := NewOwners(ownerCtx, OwnerLimits{Owners: 2, Requests: 1})
			if err != nil {
				t.Fatal(err)
			}
			held, _ := owners.Enter(ownerCtx)
			other, _ := owners.Enter(ownerCtx)
			request, _ := owners.EnterRequest(ownerCtx)
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- held.FenceTerminal(ctx) }()
			ownerTestWait(t, owners, func() bool { return owners.terminal != 0 })
			want := ErrProtocol
			switch mode {
			case "caller":
				cancel()
				want = ErrCanceled
			case "owner":
				stopOwner()
				want = ErrCanceled
			case "held-end":
				copy := held
				copy.End()
			case "copied-acquire":
				copy := held
				if err := copy.FenceTerminal(ctx); !errors.Is(err, ErrProtocol) {
					t.Fatal(err)
				}
			}
			if err := ownerTestResult(t, done); !errors.Is(err, want) || !errors.Is(owners.Err(), want) {
				t.Fatal("terminal wait failure not retained", err, owners.Err())
			}
			if owners.active != 3 || owners.requests != 1 || owners.terminal != 1 || owners.pausedReady || owners.requestsReady {
				t.Fatal("failed terminal wait fabricated completion")
			}
			other.End()
			request.End()
			if owners.active != 1 || owners.requests != 0 || !errors.Is(owners.Err(), want) {
				t.Fatal("real cleanup lost exact retained turn or failure")
			}
		})
	}
}

func TestOwnersTerminalCannotEndOrReopen(t *testing.T) {
	for _, mode := range []string{"end", "copy", "resume", "open", "pause", "request-fence"} {
		t.Run(mode, func(t *testing.T) {
			owners := ownerTestNew(t, 1)
			turn, _ := owners.Enter(t.Context())
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			if err := turn.FenceTerminal(ctx); err != nil {
				t.Fatal(err)
			}
			var err error
			switch mode {
			case "end":
				turn.End()
				err = owners.Err()
			case "copy":
				copy := turn
				err = copy.FenceTerminal(ctx)
			case "resume":
				err = owners.Resume()
			case "open":
				err = owners.OpenRequests()
			case "pause":
				err = owners.Pause(ctx)
			case "request-fence":
				err = owners.FenceRequests(ctx)
			}
			if !errors.Is(err, ErrProtocol) || !errors.Is(owners.Err(), ErrProtocol) || owners.active != 1 || owners.terminal != 1 || owners.pausedReady || owners.requestsReady {
				t.Fatal("terminal hold reopened or disappeared", err)
			}
		})
	}
}
