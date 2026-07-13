package sync

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/repowork"
	"github.com/bmeddeb/phebs/internal/store"
)

// basicExtraheader wraps basic-auth userinfo in the per-invocation git
// config every adapter uses; redactArgs scrubs it from persisted errors so
// tokens never land anywhere durable.
func basicExtraheader(userinfo string) []string {
	basic := base64.StdEncoding.EncodeToString([]byte(userinfo))
	return []string{"-c", "http.extraheader=Authorization: Basic " + basic}
}

// cloneAuth rebuilds a connection's clone/fetch credentials outside a full
// sync (the webhook fetch path). Each host wants a different basic-auth
// userinfo shape; app-authed github connections exchange a fresh
// installation token here.
func cloneAuth(ctx context.Context, conn config.Connection) ([]string, error) {
	switch conn.Type {
	case "github":
		tok := conn.Token
		if !conn.App.IsZero() {
			t, err := appToken(ctx, ghAPIBase, conn.App)
			if err != nil {
				return nil, err
			}
			tok = t
		}
		if tok == "" {
			return nil, nil
		}
		return basicExtraheader("x-access-token:" + tok), nil
	case "gitlab":
		if conn.Token == "" {
			return nil, nil
		}
		return basicExtraheader("oauth2:" + conn.Token), nil
	case "gitea":
		if conn.Token == "" {
			return nil, nil
		}
		// Gitea resolves basic-auth usernames as tokens
		return basicExtraheader(conn.Token + ":"), nil
	case "git":
		return HTTPBasicAuthConfig(conn.HTTPAuth.Username, conn.HTTPAuth.Password), nil
	default:
		return nil, nil
	}
}

// FetchHandler adapts webhook-driven single-repo fetches to the store.Runner
// (T7.4): the job target is a repo name; the mirror is fetched with the
// claiming connection's credentials and indexing is chained. Unlike a
// connection sync this never lists the host — one push, one fetch.
func FetchHandler(cfg *config.Config, st store.Store) func(context.Context, store.Job) error {
	return func(ctx context.Context, job store.Job) error {
		dir, err := SafeRepoDir(cfg.Server.DataDir, job.Target)
		if err != nil {
			return fmt.Errorf("fetch %s: %w", job.Target, err)
		}
		unlock, err := repowork.LockContext(ctx, dir)
		if err != nil {
			return fmt.Errorf("fetch %s: lock mirror: %w", job.Target, err)
		}
		defer unlock()

		repo, err := st.GetRepo(ctx, job.Target)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil // cleanup won after this fetch was queued
			}
			return fmt.Errorf("fetch %s: %w", job.Target, err)
		}
		if repo.Deleting {
			return nil
		}
		conn, err := claimingConnection(ctx, cfg, st, job.Target)
		if err != nil {
			return err
		}
		// the stored CloneURL is server-supplied data: re-check it against
		// the claiming connection before sending its credentials anywhere
		// (also fails closed when the connection was repurposed since the
		// row was written)
		if err := checkCloneURL(conn, repo.CloneURL); err != nil {
			return fmt.Errorf("fetch %s: %w", job.Target, err)
		}
		gitCfg, err := cloneAuth(ctx, conn)
		if err != nil {
			return fmt.Errorf("fetch %s: %w", job.Target, err)
		}
		if err := mirrorLocked(ctx, repo.CloneURL, dir, gitCfg...); err != nil {
			return fmt.Errorf("fetch %s: %w", job.Target, err)
		}
		// Cleanup may mark the row while the fetch owns the mirror lock. Keep
		// the lock through this identity check so an old job cannot mutate a
		// delete-and-recreated repository generation.
		current, err := st.GetRepo(ctx, job.Target)
		if errors.Is(err, store.ErrNotFound) || err == nil && current.Deleting {
			return os.RemoveAll(dir)
		}
		if err != nil {
			return fmt.Errorf("fetch %s: reload repo: %w", job.Target, err)
		}
		return store.EnqueueUnlessInFlight(ctx, st, store.JobIndex, job.Target)
	}
}

// claimingConnection resolves the configured connection that owns a repo, so
// a fetch can reuse its credentials.
func claimingConnection(ctx context.Context, cfg *config.Config, st store.Store, repoName string) (config.Connection, error) {
	claims, err := st.GetRepoConnections(ctx, repoName)
	if err != nil {
		return config.Connection{}, err
	}
	for _, c := range cfg.Connections {
		if slices.Contains(claims, c.Name) {
			return c, nil
		}
	}
	return config.Connection{}, fmt.Errorf("fetch %s: no configured connection claims it", repoName)
}
