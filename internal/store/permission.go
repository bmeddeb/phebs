package store

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

var _ PermissionStore = (*Surreal)(nil)

type repoPermissionRec struct {
	Repo     string `json:"repo"`
	Identity string `json:"identity"`
}

// SetRepoPermissions replaces repo's edge set in one transaction
// (the SetRepoConnections replace-set shape).
func (s *Surreal) SetRepoPermissions(ctx context.Context, repo string, identities []string) error {
	if repo == "" {
		return fmt.Errorf("set repo permissions: empty repo name")
	}
	cleaned := make([]string, 0, len(identities))
	for _, id := range identities {
		if id = strings.ToLower(strings.TrimSpace(id)); id != "" {
			cleaned = append(cleaned, id)
		}
	}
	slices.Sort(cleaned)
	cleaned = slices.Compact(cleaned) // the unique pair index rejects duplicates
	_, err := storeQuery[any](ctx, s.accounting, s.db,
		`BEGIN;
DELETE repo_permission WHERE repo = $repo;
FOR $identity IN $identities { CREATE repo_permission CONTENT { repo: $repo, identity: $identity } };
COMMIT;`,
		map[string]any{"repo": repo, "identities": cleaned}, storeUnsupported())
	if err != nil {
		return fmt.Errorf("set repo permissions for %s: %w", repo, err)
	}
	return nil
}

func (s *Surreal) ListPermittedRepos(ctx context.Context, identities []string) ([]string, error) {
	if len(identities) == 0 {
		return nil, nil
	}
	lowered := make([]string, 0, len(identities))
	for _, id := range identities {
		lowered = append(lowered, strings.ToLower(id))
	}
	results, err := storeQuery[[]repoPermissionRec](ctx, s.accounting, s.db,
		"SELECT repo, identity FROM repo_permission WHERE identity IN $identities",
		map[string]any{"identities": lowered}, storeRead())
	if err != nil {
		return nil, fmt.Errorf("list permitted repos: %w", err)
	}
	seen := map[string]bool{}
	var names []string
	for _, result := range *results {
		for _, row := range result.Result {
			if !seen[row.Repo] {
				seen[row.Repo] = true
				names = append(names, row.Repo)
			}
		}
	}
	return names, nil
}

func (s *Surreal) DeleteRepoPermissions(ctx context.Context, repo string) error {
	_, err := storeQuery[any](ctx, s.accounting, s.db,
		"DELETE repo_permission WHERE repo = $repo", map[string]any{"repo": repo}, storeUnsupported())
	if err != nil {
		return fmt.Errorf("delete repo permissions for %s: %w", repo, err)
	}
	return nil
}
