package auth

import (
	"context"
	"time"

	"github.com/bmeddeb/phebs/internal/store"
)

// sessionStore adapts Surreal persistence to scs.CtxStore. SCS hashes each
// random cookie token before calling this adapter, so no replayable session
// credential is stored in the database.
type sessionStore struct {
	store store.AuthStore
}

func (s sessionStore) Delete(token string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.DeleteCtx(ctx, token)
}

func (s sessionStore) Find(token string) ([]byte, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.FindCtx(ctx, token)
}

func (s sessionStore) Commit(token string, data []byte, expiry time.Time) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.CommitCtx(ctx, token, data, expiry)
}

func (s sessionStore) DeleteCtx(ctx context.Context, token string) error {
	return s.store.DeleteAuthSession(ctx, token)
}

func (s sessionStore) FindCtx(ctx context.Context, token string) ([]byte, bool, error) {
	return s.store.FindAuthSession(ctx, token, time.Now().UTC())
}

func (s sessionStore) CommitCtx(ctx context.Context, token string, data []byte, expiry time.Time) error {
	return s.store.CommitAuthSession(ctx, token, data, expiry)
}
