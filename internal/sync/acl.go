package sync

import (
	"context"
	"log"
	"strings"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/store"
)

// T10.3 permission syncing: while a connection syncs, each PRIVATE repo's
// collaborator list is mirrored into repo_permission edges keyed
// "<host>:<login>" (public repos are visible to every authenticated user, so
// their ACLs are never fetched — no extra API cost). A failed listing keeps
// the previous edge set: stale grants until the next successful sync beat
// locking every user out on a transient host error.

// ACLStore returns the permission sink for a sync run: nil unless the config
// enables enforcement (a permissions block exists) and the store can persist
// edges. Enabling permissions therefore requires a re-sync before private
// repos become visible to non-administrators.
func ACLStore(cfg *config.Config, st store.Store) store.PermissionStore {
	if cfg.Permissions == nil {
		return nil
	}
	ps, _ := st.(store.PermissionStore)
	return ps
}

// hostIdentity builds the canonical lower-cased edge key. host is the repo
// name's first segment ("github.com", self-hosted host:port), so config
// identities and edges always speak the same dialect.
func hostIdentity(host, login string) string {
	return strings.ToLower(host) + ":" + strings.ToLower(login)
}

// mirrorACL writes one repo's replacement grant set, keeping stale edges on
// any failure. listErr is the collaborator-listing outcome so callers stay a
// one-liner.
func mirrorACL(ctx context.Context, acl store.PermissionStore, repo string, identities []string, listErr error) {
	if listErr != nil {
		log.Printf("permission sync %s: %v (keeping previous grants)", repo, listErr)
		return
	}
	if err := acl.SetRepoPermissions(ctx, repo, identities); err != nil {
		log.Printf("permission sync %s: %v (keeping previous grants)", repo, err)
	}
}
