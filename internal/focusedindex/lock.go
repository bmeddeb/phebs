package focusedindex

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
)

const publicationLockName = ".phebs-index-publication.lock"

// AcquireMutationLock admits one index/reconciliation mutation alongside
// other repository mutations while excluding the database+focused-index
// snapshot taken by online backup. Kernel lock release makes crashes safe.
func AcquireMutationLock(ctx context.Context, indexDir string) (func(), error) {
	return acquireLock(ctx, indexDir, false)
}

// AcquireBackupLock waits until every publication/state mutation is complete,
// then excludes new ones until the caller has exported the database and copied
// all focused publication bytes.
func AcquireBackupLock(ctx context.Context, indexDir string) (func(), error) {
	return acquireLock(ctx, indexDir, true)
}

func acquireLock(
	ctx context.Context,
	indexDir string,
	exclusive bool,
) (func(), error) {
	if err := os.MkdirAll(indexDir, 0o755); err != nil {
		return nil, err
	}
	if err := ensureRealDirectory(indexDir); err != nil {
		return nil, err
	}
	lock := flock.New(
		filepath.Join(indexDir, publicationLockName),
		flock.SetPermissions(0o600),
	)
	var (
		acquired bool
		err      error
	)
	if exclusive {
		acquired, err = lock.TryLockContext(ctx, 10*time.Millisecond)
	} else {
		acquired, err = lock.TryRLockContext(ctx, 10*time.Millisecond)
	}
	if err != nil {
		_ = lock.Close()
		return nil, err
	}
	if !acquired {
		_ = lock.Close()
		return nil, fmt.Errorf("index publication lock was not acquired")
	}
	return func() {
		_ = lock.Unlock()
		_ = lock.Close()
	}, nil
}
