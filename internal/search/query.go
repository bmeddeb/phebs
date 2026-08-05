// Package search parses queries and serves zoekt shard searches (Epic 4).
//
// Query language is zoekt's native syntax via query.Parse — no grammar port
// (PLAN §1). The phebs pre-pass rewrites repo-metadata atoms (archived:,
// fork:, public:) into a query.RepoSet resolved against the DB, because our
// shards don't carry those flags.
package search

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/sourcegraph/zoekt/query"

	"github.com/bmeddeb/phebs/internal/store"
)

var ErrInvalidQuery = errors.New("invalid search query")

func invalidQueryf(format string, args ...any) error {
	return fmt.Errorf("%w: parse query: %s", ErrInvalidQuery, fmt.Sprintf(format, args...))
}

func invalidQueryWrap(err error) error {
	return fmt.Errorf("%w: parse query: %v", ErrInvalidQuery, err)
}

// Compile parses raw and applies the pre-passes: `context:<name>` atoms
// (T8.1) become a RepoSet of the named config-defined repo set, and repo
// metadata atoms are rewritten against the DB. Per-user permission filtering
// (T10.3) is a system constraint, not query syntax — it lives in
// (*Searcher).compile via the Visible hook, intersecting everything Compile
// produces (context: expansions included).
func Compile(ctx context.Context, st store.Store, contexts map[string][]string, raw string) (query.Q, error) {
	q, _, err := compileQuery(ctx, st, contexts, raw)
	return q, err
}

// compileQuery additionally returns phebs' rev: scope. Revision resolution is
// intentionally left to Searcher.compile, after its per-principal visibility
// predicate has run.
func compileQuery(ctx context.Context, st store.Store, contexts map[string][]string, raw string) (query.Q, string, error) {
	revisions, rest, err := extractRevisions(raw)
	if err != nil {
		return nil, "", err
	}
	if len(revisions) > 1 {
		return nil, "", invalidQueryf("exactly one rev: scope is allowed")
	}
	// context: is phebs syntax, not zoekt's — extract it string-level
	// before query.Parse ever sees it.
	ctxNames, rest, err := extractContexts(rest)
	if err != nil {
		return nil, "", err
	}
	if (len(ctxNames) > 0 || len(revisions) > 0) && strings.TrimSpace(rest) == "" {
		return nil, "", invalidQueryf("context:/rev: needs search terms alongside it")
	}

	q, err := query.Parse(rest)
	if err != nil {
		return nil, "", invalidQueryWrap(err)
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
		return nil, "", fmt.Errorf("resolve repo filters: %w", loadErr)
	}

	// contexts AND onto the whole query as one RepoSet; multiple context:
	// atoms union their sets (a repo in any named set matches)
	if len(ctxNames) > 0 {
		set := map[string]bool{}
		for _, name := range ctxNames {
			patterns, ok := contexts[name]
			if !ok {
				return nil, "", fmt.Errorf("unknown search context %q", name)
			}
			if !loaded {
				repos, loadErr = st.ListRepos(ctx)
				loaded = true
				if loadErr != nil {
					return nil, "", fmt.Errorf("resolve search context: %w", loadErr)
				}
			}
			for _, r := range repos {
				for _, pat := range patterns {
					if ok, _ := path.Match(pat, r.Name); ok {
						set[r.Name] = true
						break
					}
				}
			}
		}
		names := make([]string, 0, len(set))
		for n := range set {
			names = append(names, n)
		}
		q = query.NewAnd(query.NewRepoSet(names...), q)
	}
	revision := ""
	if len(revisions) == 1 {
		revision = revisions[0]
	}
	return query.Simplify(q), revision, nil
}

var revisionQuerySelectorRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)

func extractRevisions(raw string) (names []string, rest string, err error) {
	var kept []string
	for _, tok := range splitQuery(raw) {
		switch {
		case strings.HasPrefix(tok, "rev:"):
			name := tok[len("rev:"):]
			if !revisionQuerySelectorRE.MatchString(name) || strings.HasSuffix(name, "/") ||
				strings.Contains(name, "//") || strings.Contains(name, "..") {
				return nil, "", invalidQueryf("rev: takes one bare revision selector, got %q", tok)
			}
			names = append(names, name)
		case strings.HasPrefix(tok, "-rev:"):
			return nil, "", invalidQueryf("-rev: is not supported (revisions are scopes, not predicates)")
		case strings.HasPrefix(strings.TrimLeft(tok, "("), "rev:"):
			return nil, "", invalidQueryf("rev: cannot be grouped in parentheses, got %q", tok)
		default:
			kept = append(kept, tok)
		}
	}
	return names, strings.Join(kept, " "), nil
}

// extractContexts strips top-level `context:<name>` atoms from a raw query,
// leaving the rest for query.Parse. A context is a scope, not a predicate:
// it is only honored as a bare whitespace-delimited term. Grouped (`(context:…`)
// and negated (`-context:…`) forms are rejected rather than passed to zoekt,
// which would silently treat them as substring matches.
func extractContexts(raw string) (names []string, rest string, err error) {
	var kept []string
	for _, tok := range splitQuery(raw) {
		switch {
		case strings.HasPrefix(tok, "context:"):
			name := tok[len("context:"):]
			if name == "" || strings.ContainsAny(name, "()") {
				return nil, "", invalidQueryf("context: takes a bare name, got %q", tok)
			}
			names = append(names, name)
		case strings.HasPrefix(tok, "-context:"):
			return nil, "", invalidQueryf("-context: is not supported (contexts are scopes, not predicates)")
		case strings.HasPrefix(strings.TrimLeft(tok, "("), "context:"):
			// ponytail: rejects a regex atom literally starting "(context:"
			// too — pathological; a loud error beats a silent mis-scope.
			return nil, "", invalidQueryf("context: cannot be grouped in parentheses, got %q", tok)
		default:
			kept = append(kept, tok)
		}
	}
	return names, strings.Join(kept, " "), nil
}

// splitQuery fields raw into zoekt-level tokens the way query.Parse lexes:
// a backslash escapes the next rune, and a double-quoted span (with \"
// escapes inside) is one token. Only unescaped, unquoted whitespace
// separates tokens, so each token's bytes are preserved exactly — rejoining
// the non-context tokens with single spaces can't change what zoekt matches
// (an escaped tab stays an escaped tab, not a space).
func splitQuery(raw string) []string {
	var out []string
	var cur strings.Builder
	inQuote, esc := false, false
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range raw {
		switch {
		case esc:
			cur.WriteRune(r)
			esc = false
		case r == '\\':
			cur.WriteRune(r)
			esc = true
		case r == '"':
			inQuote = !inQuote
			cur.WriteRune(r)
		case !inQuote && (r == ' ' || r == '\t' || r == '\n' || r == '\r'):
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return out
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
