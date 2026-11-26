package storeaccounting

import (
	"context"
	"errors"
	"testing"

	surrealdb "github.com/surrealdb/surrealdb.go"
)

// TestUnownedSDKCallsNeverPanic pins the PHEBS-45 gate fix: an unaccounted
// SDK call that panics inside the SDK (seen as a nil reply on a closed
// connection) returns a non-terminal ErrProtocol, as the owned path does,
// instead of crashing the caller.
func TestUnownedSDKCallsNeverPanic(t *testing.T) {
	ctx := context.Background()
	var db *surrealdb.DB
	var tx *surrealdb.Transaction
	for name, call := range map[string]func() error{
		"query": func() error {
			result, err := SDKQuery[any](ctx, nil, db, "RETURN 1", nil, SDKRead())
			if result != nil {
				t.Fatalf("panicking query returned a result: %v", result)
			}
			return err
		},
		"begin": func() error {
			begun, err := SDKBegin(ctx, nil, db)
			if begun != nil {
				t.Fatal("panicking begin returned a transaction")
			}
			return err
		},
		"commit": func() error { return SDKCommit(ctx, nil, tx) },
		"cancel": func() error { return SDKCancel(ctx, nil, tx) },
	} {
		if err := call(); !errors.Is(err, ErrProtocol) {
			t.Fatalf("unowned %s = %v, want ErrProtocol", name, err)
		}
	}
}

func TestRecoverUnownedSDKLeavesNormalReturns(t *testing.T) {
	want := errors.New("ordinary SDK error")
	got := func() (err error) {
		defer recoverUnownedSDK(&err)
		return want
	}()
	if got != want {
		t.Fatalf("recoverUnownedSDK changed an ordinary return: %v", got)
	}
}
