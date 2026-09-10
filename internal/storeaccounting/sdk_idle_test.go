//go:build darwin || linux

package storeaccounting

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestSDKIdleMeasurement(t *testing.T) {
	ctx, owner, controller := storeAccountingFixture(t, 2, 1)
	before, err := controller.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("measurement failed")
	var missingContext context.Context
	for _, test := range []struct {
		name string
		run  func() error
		want error
	}{
		{"success", func() error { return owner.WithIdle(ctx, func() error { return nil }) }, nil},
		{"callback", func() error { return owner.WithIdle(ctx, func() error { return sentinel }) }, sentinel},
		{"nil_callback", func() error { return owner.WithIdle(ctx, nil) }, ErrConfig},
		{"nil_context", func() error { return owner.WithIdle(missingContext, func() error { return nil }) }, ErrConfig},
		{"nil_owner", func() error { return (*SDKOwner)(nil).WithIdle(ctx, func() error { return nil }) }, ErrConfig},
		{"unconstructed_owner", func() error { return (&SDKOwner{}).WithIdle(ctx, func() error { return nil }) }, ErrConfig},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.run(); !errors.Is(err, test.want) {
				t.Fatalf("got %v want %v", err, test.want)
			}
		})
	}
	canceled, cancel := context.WithCancel(ctx)
	if err := owner.WithIdle(canceled, func() error { cancel(); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := owner.WithIdle(canceled, func() error { t.Fatal("canceled callback ran"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	func() {
		defer func() {
			if recover() != sentinel {
				t.Error("callback panic was replaced")
			}
		}()
		_ = owner.WithIdle(ctx, func() error { panic(sentinel) })
	}()
	if err := owner.WithIdle(ctx, func() error { return nil }); err != nil {
		t.Fatal("panic retained mutex", err)
	}
	after, err := controller.Snapshot()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("local measurement changed accounting", err)
	}
}

func TestSDKIdleRefusesActiveWork(t *testing.T) {
	for _, transaction := range []bool{false, true} {
		ctx, owner, controller := storeAccountingFixture(t, 2, 1)
		db, _ := storeAccountingDB(t, ctx, owner)
		if transaction {
			tx, err := SDKBegin(ctx, owner, db)
			if err != nil {
				t.Fatal(err)
			}
			if err := owner.WithIdle(ctx, func() error { t.Fatal("live transaction measured"); return nil }); !errors.Is(err, ErrBusy) {
				t.Fatal(err)
			}
			if err := SDKCancel(ctx, owner, tx); err != nil {
				t.Fatal(err)
			}
		} else {
			call, err := owner.acquire(ctx, 0, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := owner.WithIdle(ctx, func() error { t.Fatal("live call measured"); return nil }); !errors.Is(err, ErrBusy) {
				t.Fatal(err)
			}
			owner.mu.Lock()
			owner.releaseLocked(call)
			owner.mu.Unlock()
		}
		if err := controller.Fence(); err != nil {
			t.Fatal(err)
		}
		if err := owner.Checkpoint(ctx); err != nil {
			t.Fatal(err)
		}
		if err := owner.WithIdle(ctx, func() error { t.Fatal("checkpoint measured"); return nil }); !errors.Is(err, ErrFenced) {
			t.Fatal(err)
		}
	}
}

func TestSDKIdleBlocksFreshAdmission(t *testing.T) {
	for _, cancelBlocked := range []bool{false, true} {
		ctx, owner, _ := storeAccountingFixture(t, 2, 1)
		request, cancel := context.WithCancel(ctx)
		started, result := make(chan struct{}), make(chan error, 1)
		if err := owner.WithIdle(ctx, func() error {
			go func() {
				close(started)
				call, err := owner.acquire(request, 0, nil)
				if call != nil {
					owner.mu.Lock()
					owner.releaseLocked(call)
					owner.mu.Unlock()
				}
				result <- err
			}()
			<-started
			select {
			case err := <-result:
				t.Fatalf("SDK entered during measurement: %v", err)
			case <-time.After(20 * time.Millisecond):
			}
			if cancelBlocked {
				cancel()
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		err := <-result
		cancel()
		if cancelBlocked && !errors.Is(err, ErrCanceled) || !cancelBlocked && err != nil {
			t.Fatal(err)
		}
	}
}
