//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

// V21OpenLocalSDKTest is available only to external Go tests of this package.
// It composes the real local SDK bootstrap with a genuine bounded component
// accounting owner. It cannot be linked into the application and supplies no
// process admission, native producer identity, custody or acceptance credit.
func V21OpenLocalSDKTest(ctx context.Context, dir string) (_ *Surreal, _ func(context.Context) error, resultErr error) {
	controller, err := storeaccounting.New(ctx, storeaccounting.Config{
		Producers: []storeaccounting.Producer{{ID: 2, Calls: 40, Transactions: 2}},
		Phases:    []storeaccounting.Phase{{ID: 1, Transactions: 10000, Rows: 100000}},
	})
	if err != nil {
		return nil, nil, err
	}
	transport, err := storeaccounting.NewTransport(ctx, controller, storeaccounting.WireConfig{
		Producers: []storeaccounting.WireProducer{{ID: 2, Binding: [32]byte{1}, Phases: 1}}, AckTimeout: time.Second,
	})
	if err != nil {
		return nil, nil, err
	}
	var state *Surreal
	var owner *storeCallOwner
	var once sync.Once
	var closeErr error
	close := func(ctx context.Context) error {
		once.Do(func() {
			if state != nil {
				closeErr = errors.Join(closeErr, state.Close(ctx))
			}
			if owner != nil {
				snapshot, err := controller.Snapshot()
				if err != nil || len(snapshot.Producers) != 1 || snapshot.Producers[0].Calls != 0 || snapshot.Producers[0].Transactions != 0 {
					closeErr = errors.Join(closeErr, errors.New("component SDK calls or transactions did not drain"), err)
				}
				closeErr = errors.Join(closeErr, owner.Close(ctx))
			}
			closeErr = errors.Join(closeErr, transport.Close())
		})
		return closeErr
	}
	defer func() {
		if resultErr != nil {
			_ = close(context.Background())
		}
	}()
	file, config, err := transport.Open(2)
	if err != nil {
		return nil, nil, err
	}
	client, err := storeaccounting.NewClient(ctx, file, config)
	if err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	owner, err = newStoreCallOwner(client)
	if err != nil {
		_ = client.Close(context.Background())
		return nil, nil, err
	}
	if err := checkLocalRuntimeAvailable(dir); err != nil {
		return nil, nil, err
	}
	runtime, engine, err := startOwnedEngine(ctx, "surrealkv:"+filepath.Join(dir, "db"))
	if err != nil {
		return nil, nil, err
	}
	state, err = openLocalRootWithOwner(ctx, runtime, owner)
	if err != nil {
		engine.stop()
		return nil, nil, err
	}
	remove, err := PublishLocalRuntime(dir, runtime)
	if err != nil {
		_ = state.db.Close(context.Background())
		engine.stop()
		return nil, nil, err
	}
	engine.removeRuntime = remove
	state.engine, state.stop = engine, engine.stop
	return state, close, nil
}
