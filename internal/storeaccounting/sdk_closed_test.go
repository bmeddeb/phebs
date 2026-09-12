//go:build darwin || linux

package storeaccounting

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestSDKClosedMeasurement(t *testing.T) {
	ctx, owner, controller := storeAccountingFixture(t, 2, 1)
	refused := func() error { t.Error("unclosed owner measured"); return nil }
	if err := owner.WithClosed(ctx, refused); !errors.Is(err, ErrFenced) {
		t.Fatal(err)
	}
	if err := controller.Fence(); err != nil {
		t.Fatal(err)
	}
	if err := owner.Checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	if err := owner.WithClosed(ctx, refused); !errors.Is(err, ErrFenced) {
		t.Fatal("checkpoint was treated as successful close", err)
	}
	if err := owner.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if owner.client.Context().Err() == nil || !owner.closed {
		t.Fatal("actual client closure not recorded")
	}
	before, err := controller.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("closed measurement failure")
	var missingContext context.Context
	for _, test := range []struct {
		name string
		run  func() error
		want error
	}{
		{"success", func() error { return owner.WithClosed(ctx, func() error { return nil }) }, nil},
		{"callback_failure", func() error { return owner.WithClosed(ctx, func() error { return sentinel }) }, sentinel},
		{"missing_callback", func() error { return owner.WithClosed(ctx, nil) }, ErrConfig},
		{"missing_context", func() error { return owner.WithClosed(missingContext, func() error { return nil }) }, ErrConfig},
		{"missing_owner", func() error { return (*SDKOwner)(nil).WithClosed(ctx, func() error { return nil }) }, ErrConfig},
		{"invalid_owner", func() error { return (&SDKOwner{}).WithClosed(ctx, func() error { return nil }) }, ErrConfig},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.run(); !errors.Is(err, test.want) {
				t.Fatalf("got %v want %v", err, test.want)
			}
		})
	}
	canceled, cancel := context.WithCancel(ctx)
	if err := owner.WithClosed(canceled, func() error { cancel(); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := owner.WithClosed(canceled, refused); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	func() {
		defer func() {
			if recover() != sentinel {
				t.Error("closed callback panic changed")
			}
		}()
		_ = owner.WithClosed(ctx, func() error { panic(sentinel) })
	}()
	if err := owner.WithClosed(ctx, func() error { return nil }); err != nil {
		t.Fatal("panic retained mutex", err)
	}
	if err := owner.WithIdle(ctx, refused); err == nil {
		t.Fatal("closed owner became idle")
	}
	if err := owner.Resume(2); err == nil {
		t.Fatal("closed owner resumed")
	}
	after, err := controller.Snapshot()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("retired measurement changed accounting", before, after, err)
	}
}

func TestSDKClosedMeasurementRefusesUnprovenState(t *testing.T) {
	for _, test := range []string{"client_only_close", "failed_close", "call", "transaction", "unfenced", "owner_error"} {
		t.Run(test, func(t *testing.T) {
			ctx, owner, _ := storeAccountingFixture(t, 2, 1)
			switch test {
			case "client_only_close":
				if err := owner.client.Close(ctx); err != nil {
					t.Fatal(err)
				}
			case "failed_close":
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				if err := owner.Close(canceled); err == nil || owner.closed {
					t.Fatal("failed close recorded success", err)
				}
			default:
				if err := owner.Close(ctx); err != nil {
					t.Fatal(err)
				}
				// Deliberately corrupt post-close state to exercise defensive
				// predicates. No forged state is accepted as positive evidence.
				switch test {
				case "call":
					owner.calls[0] = &storeSDKCall{}
				case "transaction":
					owner.transactions[0].used = true
				case "unfenced":
					owner.fenced = false
				case "owner_error":
					owner.err = ErrTransport
				}
			}
			if err := owner.WithClosed(ctx, func() error { t.Error("unproven closed state measured"); return nil }); err == nil {
				t.Fatal("unproven closed state admitted")
			}
		})
	}
}

func TestSDKClosedMeasurementSerializesOwner(t *testing.T) {
	ctx, owner, _ := storeAccountingFixture(t, 2, 1)
	if err := owner.Close(ctx); err != nil {
		t.Fatal(err)
	}
	started, done := make(chan struct{}), make(chan error, 1)
	if err := owner.WithClosed(ctx, func() error {
		go func() { close(started); done <- owner.Resume(2) }()
		<-started
		select {
		case <-done:
			return errors.New("owner mutex not held during retired measurement")
		case <-time.After(20 * time.Millisecond):
			return nil
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil {
		t.Fatal("retired owner resumed after measurement")
	}
}
