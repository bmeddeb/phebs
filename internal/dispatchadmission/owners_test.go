package dispatchadmission

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"
)

func TestOwnersConstructorLimits(t *testing.T) {
	for _, limits := range []OwnerLimits{{}, {1, 0}, {0, 1}, {65, 1}, {1, 65}, {-1, 1}} {
		if _, err := NewOwners(t.Context(), limits); !errors.Is(err, ErrConfig) {
			t.Fatalf("limits %+v = %v", limits, err)
		}
	}
	for _, limits := range []OwnerLimits{{1, 1}, {29, 1}, {64, 64}} {
		if _, err := NewOwners(t.Context(), limits); err != nil {
			t.Fatalf("limits %+v = %v", limits, err)
		}
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, ctx := range []context.Context{nil, canceled} {
		if _, err := NewOwners(ctx, OwnerLimits{1, 1}); !errors.Is(err, ErrConfig) {
			t.Fatalf("invalid context = %v", err)
		}
	}
}

func TestOwnersOrdinaryAndHealthyTurnsAllocateNothing(t *testing.T) {
	ctx := t.Context()
	for _, owners := range []*Owners{nil, ownerTestNew(t, 1)} {
		if allocations := testing.AllocsPerRun(100, func() {
			turn, err := owners.Enter(ctx)
			if err != nil {
				panic(err)
			}
			turn.End()
			request, err := owners.EnterRequest(ctx)
			if err != nil {
				panic(err)
			}
			request.End()
		}); allocations != 0 {
			t.Fatalf("owners enabled %t: allocations = %g", owners != nil, allocations)
		}
	}
	if owners, err := NewProductionOwners(ctx, OwnerLimits{}); owners != nil || err != nil {
		t.Fatalf("ordinary production = %v, %v", owners, err)
	}
}

func TestOwnersPauseDrainsWholeTurnBeforeResume(t *testing.T) {
	owners := ownerTestNew(t, 2)
	turn, err := owners.Enter(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	paused := make(chan error, 1)
	go func() { paused <- owners.Pause(t.Context()) }()
	ownerTestWait(t, owners, func() bool { return owners.paused })
	entered := make(chan OwnerTurn, 1)
	go func() {
		next, enterErr := owners.Enter(t.Context())
		if enterErr == nil {
			entered <- next
		}
	}()
	ownerTestWait(t, owners, func() bool { return owners.waiters == 1 })
	select {
	case err := <-paused:
		t.Fatalf("pause completed before durable tail: %v", err)
	default:
	}
	turn.End()
	if err := ownerTestResult(t, paused); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
		t.Fatal("parked owner entered before resume")
	default:
	}
	if err := owners.Resume(); err != nil {
		t.Fatal(err)
	}
	select {
	case next := <-entered:
		next.End()
	case <-time.After(time.Second):
		t.Fatal("resume did not release owner")
	}
}

func TestOwnersParkedCancellationDoesNotLatch(t *testing.T) {
	owners := ownerTestNew(t, 1)
	if err := owners.Pause(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancelCause(t.Context())
	done := make(chan error, 1)
	go func() { _, err := owners.Enter(ctx); done <- err }()
	ownerTestWait(t, owners, func() bool { return owners.waiters == 1 })
	cancel(errors.New("private caller cause"))
	if err := ownerTestResult(t, done); err != context.Canceled || owners.Err() != nil {
		t.Fatalf("parked cancellation = %v, %v", err, owners.Err())
	}
	if err := owners.Resume(); err != nil {
		t.Fatal(err)
	}
	turn, err := owners.Enter(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	turn.End()
}

func TestOwnersLimitsAndStaleReleaseLatch(t *testing.T) {
	for _, kind := range []string{"active", "request", "waiter", "stale", "generation"} {
		t.Run(kind, func(t *testing.T) {
			owners := ownerTestNew(t, 1)
			want := ErrLimit
			switch kind {
			case "active":
				turn, _ := owners.Enter(t.Context())
				defer turn.End()
				_, _ = owners.Enter(t.Context())
			case "request":
				turn, _ := owners.EnterRequest(t.Context())
				defer turn.End()
				_, _ = owners.EnterRequest(t.Context())
			case "waiter":
				if err := owners.Pause(t.Context()); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() { _, err := owners.Enter(t.Context()); done <- err }()
				ownerTestWait(t, owners, func() bool { return owners.waiters == 1 })
				_, _ = owners.Enter(t.Context())
				if err := ownerTestResult(t, done); !errors.Is(err, ErrLimit) {
					t.Fatal(err)
				}
			case "stale":
				first, _ := owners.Enter(t.Context())
				first.End()
				second, _ := owners.Enter(t.Context())
				first.End()
				if owners.active != 1 {
					t.Fatal("stale turn released current owner")
				}
				second.End()
				want = ErrProtocol
			case "generation":
				owners.generations[0] = math.MaxUint64
				_, _ = owners.Enter(t.Context())
			}
			if err := owners.Err(); !errors.Is(err, want) || owners.Context().Err() == nil {
				t.Fatalf("latched error = %v, context = %v", err, owners.Context().Err())
			}
		})
	}
}

func TestOwnersRequestFenceIncludesTailAndDoesNotPark(t *testing.T) {
	owners := ownerTestNew(t, 1)
	request, _ := owners.EnterRequest(t.Context())
	fenced := make(chan error, 1)
	go func() { fenced <- owners.FenceRequests(t.Context()) }()
	ownerTestWait(t, owners, func() bool { return owners.requestsFenced })
	if _, err := owners.EnterRequest(t.Context()); !errors.Is(err, ErrFenced) || owners.Err() != nil {
		t.Fatalf("fenced request = %v, %v", err, owners.Err())
	}
	select {
	case err := <-fenced:
		t.Fatalf("fence skipped outer response/session tail: %v", err)
	default:
	}
	request.End()
	if err := ownerTestResult(t, fenced); err != nil {
		t.Fatal(err)
	}
	if err := owners.Pause(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := owners.OpenRequests(); err != nil {
		t.Fatal(err)
	}
	request, err := owners.EnterRequest(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	request.End()
	if !owners.paused {
		t.Fatal("opening request capacity resumed workers")
	}
}

func TestOwnersFailedDrainRetainsActiveUntilOwnerEnds(t *testing.T) {
	for _, request := range []bool{false, true} {
		t.Run(map[bool]string{false: "loop", true: "request"}[request], func(t *testing.T) {
			owners := ownerTestNew(t, 1)
			enter, fence := owners.Enter, owners.Pause
			if request {
				enter, fence = owners.EnterRequest, owners.FenceRequests
			}
			turn, _ := enter(t.Context())
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if err := fence(ctx); !errors.Is(err, ErrCanceled) {
				t.Fatal(err)
			}
			if owners.active|owners.requests == 0 {
				t.Fatal("drain timeout invented owner completion")
			}
			turn.End()
			if owners.active|owners.requests != 0 || !errors.Is(owners.Err(), ErrCanceled) {
				t.Fatal("cleanup lost retained failure or active slot")
			}
		})
	}
}

func TestOwnersPrematureAndRepeatedControlRefuse(t *testing.T) {
	for _, operation := range []string{"resume", "open_requests", "double_pause", "double_fence", "active_resume"} {
		t.Run(operation, func(t *testing.T) {
			owners := ownerTestNew(t, 1)
			var err error
			switch operation {
			case "resume":
				err = owners.Resume()
			case "open_requests":
				err = owners.OpenRequests()
			case "double_pause":
				_ = owners.Pause(t.Context())
				err = owners.Pause(t.Context())
			case "double_fence":
				_ = owners.FenceRequests(t.Context())
				err = owners.FenceRequests(t.Context())
			case "active_resume":
				turn, _ := owners.Enter(t.Context())
				done := make(chan error, 1)
				go func() { done <- owners.Pause(t.Context()) }()
				ownerTestWait(t, owners, func() bool { return owners.paused })
				err = owners.Resume()
				turn.End()
				_ = ownerTestResult(t, done)
			}
			if err == nil || owners.Err() == nil {
				t.Fatal("invalid control sequence accepted")
			}
		})
	}
}

func ownerTestNew(t *testing.T, limit int) *Owners {
	t.Helper()
	owners, err := NewOwners(t.Context(), OwnerLimits{Owners: limit, Requests: 1})
	if err != nil {
		t.Fatal(err)
	}
	return owners
}

func ownerTestWait(t *testing.T, owners *Owners, predicate func() bool) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		owners.mu.Lock()
		ready := predicate()
		owners.mu.Unlock()
		if ready {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("owner state did not settle")
		case <-ticker.C:
		}
	}
}

func ownerTestResult(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(time.Second):
		t.Fatal("owner operation did not finish")
		return nil
	}
}
