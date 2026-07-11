// Package search parses queries and serves zoekt shard searches (Epic 4).
//
// Query language is zoekt's native syntax via query.Parse — no grammar port
// (PLAN §1). The phebs pre-pass rewrites repo-metadata atoms (archived:,
// fork:, public:) into a query.RepoSet resolved against the DB, because our
// shards don't carry those flags.
package search

import (
	"context"
	"fmt"

	"github.com/sourcegraph/zoekt/query"

	"github.com/bmeddeb/phebs/internal/store"
)

// Compile parses raw and applies the metadata pre-pass. `context:` (search
// contexts, P2) will hook in here too; the per-user RepoSet hook for
// permission filtering stays reserved (CLAUDE.md).
func Compile(ctx context.Context, st store.Store, raw string) (query.Q, error) {
	q, err := query.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse query: %w", err)
	}

	// Rewrite each repo-metadata atom (fork:/archived:/public:) IN PLACE with
	// a RepoSet of the repos it matches, resolved against the DB — our shards
	// don't carry those flags. In-place (not hoisted into one global AND)
	// preserves negation and OR: `-fork:yes` becomes NOT{RepoSet(forks)} and
	// `(archived:yes or lang:go)` keeps its OR, both correct. The repo list is
	// loaded lazily, only when an atom is actually present.
	// ponytail: full scan + filter in Go; fine at single-node repo counts,
	// push into a DB query if it ever shows up in profiles.
	var repos []store.Repo
	var loadErr error
	loaded := false
	q = query.Map(q, func(x query.Q) query.Q {
		rc, ok := x.(query.RawConfig)
		if !ok {
			return x
		}
		if !loaded {
			repos, loadErr = st.ListRepos(ctx)
			loaded = true
		}
		var names []string
		for _, r := range repos {
			if matchesRawConfig(r, rc) {
				names = append(names, r.Name)
			}
		}
		return query.NewRepoSet(names...)
	})
	if loadErr != nil {
		return nil, fmt.Errorf("resolve repo filters: %w", loadErr)
	}
	return query.Simplify(q), nil
}

func matchesRawConfig(r store.Repo, rc query.RawConfig) bool {
	checks := []struct {
		flag query.RawConfig
		ok   bool
	}{
		{query.RcOnlyPublic, r.IsPublic},
		{query.RcOnlyPrivate, !r.IsPublic},
		{query.RcOnlyForks, r.IsFork},
		{query.RcNoForks, !r.IsFork},
		{query.RcOnlyArchived, r.IsArchived},
		{query.RcNoArchived, !r.IsArchived},
	}
	for _, c := range checks {
		if rc&c.flag != 0 && !c.ok {
			return false
		}
	}
	return true
}
