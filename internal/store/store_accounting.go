package store

import (
	"context"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/storeaccounting"
	surrealdb "github.com/surrealdb/surrealdb.go"
)

// Compatibility keeps source recipes local while the single concrete SDK owner
// lives beside its real transport. This wrapper holds no counters or call state.
type storeCallOwner struct{ *storeaccounting.SDKOwner }
type storeQueryRecipe = storeaccounting.SDKQueryRecipe

func processStoreCallOwner() (*storeCallOwner, error) {
	owner, err := dispatchadmission.ProcessStoreOwner()
	if err != nil || owner == nil {
		return nil, err
	}
	return &storeCallOwner{SDKOwner: owner}, nil
}

func newStoreCallOwner(client *storeaccounting.Client) (*storeCallOwner, error) {
	owner, err := storeaccounting.NewSDKOwner(client)
	if err != nil {
		return nil, err
	}
	return &storeCallOwner{SDKOwner: owner}, nil
}
func sourceSDKOwner(owner *storeCallOwner) *storeaccounting.SDKOwner {
	if owner == nil {
		return nil
	}
	return owner.SDKOwner
}
func (owner *storeCallOwner) checkpoint(ctx context.Context) error { return owner.Checkpoint(ctx) }

func storeRead() storeQueryRecipe             { return storeaccounting.SDKRead() }
func storeWrite(rows uint64) storeQueryRecipe { return storeaccounting.SDKWrite(rows) }
func storeUnsupported() storeQueryRecipe      { return storeaccounting.SDKUnsupported() }

type storeSDKSender interface {
	*surrealdb.DB | *surrealdb.Transaction
}

func storeQuery[T any, S storeSDKSender](ctx context.Context, owner *storeCallOwner, sender S, sql string, vars map[string]any, recipe storeQueryRecipe) (*[]surrealdb.QueryResult[T], error) {
	return storeaccounting.SDKQuery[T](ctx, sourceSDKOwner(owner), sender, sql, vars, recipe)
}
func storeBegin(ctx context.Context, owner *storeCallOwner, db *surrealdb.DB) (*surrealdb.Transaction, error) {
	return storeaccounting.SDKBegin(ctx, sourceSDKOwner(owner), db)
}
func storeCommit(ctx context.Context, owner *storeCallOwner, tx *surrealdb.Transaction) error {
	return storeaccounting.SDKCommit(ctx, sourceSDKOwner(owner), tx)
}
func storeCancel(ctx context.Context, owner *storeCallOwner, tx *surrealdb.Transaction) error {
	return storeaccounting.SDKCancel(ctx, sourceSDKOwner(owner), tx)
}
