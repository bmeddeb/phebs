package sync

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/store"
)

// gtRepo is the subset of the Gitea repository object phebs consumes
// (PORT_MAP §5). Gitea's API is a near-superset of GitHub's shape.
type gtRepo struct {
	ID            int64      `json:"id"`
	FullName      string     `json:"full_name"`
	CloneURL      string     `json:"clone_url"`
	HTMLURL       string     `json:"html_url"`
	DefaultBranch string     `json:"default_branch"`
	Fork          bool       `json:"fork"`
	Archived      bool       `json:"archived"`
	Private       bool       `json:"private"`
	UpdatedAt     *time.Time `json:"updated_at"`
}

func syncGitea(ctx context.Context, st store.Store, dataDir string, conn config.Connection) ([]string, error) {
	base := strings.TrimSuffix(conn.URL, "/")
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("connection %s: gitea url %q has no host", conn.Name, base)
	}
	host := u.Host

	c := &hostClient{base: base + "/api/v1"}
	if conn.Token != "" {
		c.auth = "token " + conn.Token
	}

	seen := map[string]gtRepo{}
	collect := func(repos []gtRepo, err error) error {
		if err != nil {
			return err
		}
		for _, r := range repos {
			seen[r.FullName] = r
		}
		return nil
	}
	// listings are requester-scoped: the token's accessible private repos
	// are included (no GitHub-style public/private union needed, T6.3)
	for _, org := range conn.Orgs {
		if err := collect(listPages[gtRepo](ctx, c, "/orgs/"+url.PathEscape(org)+"/repos?limit=50")); err != nil {
			return nil, fmt.Errorf("connection %s: org %s: %w", conn.Name, org, err)
		}
	}
	for _, user := range conn.Users {
		if err := collect(listPages[gtRepo](ctx, c, "/users/"+url.PathEscape(user)+"/repos?limit=50")); err != nil {
			return nil, fmt.Errorf("connection %s: user %s: %w", conn.Name, user, err)
		}
	}
	for _, full := range conn.Repos {
		var r gtRepo
		if _, err := c.getJSON(ctx, c.base+"/repos/"+full, &r); err != nil {
			return nil, fmt.Errorf("connection %s: repo %s: %w", conn.Name, full, err)
		}
		seen[r.FullName] = r
	}

	// authenticated clone/fetch: Gitea accepts the PAT as the basic-auth
	// username with an empty password; injected per-invocation and scrubbed
	// from persisted errors by redactArgs.
	var gitCfg []string
	if conn.Token != "" {
		gitCfg = basicExtraheader(conn.Token + ":")
	}

	var names []string
	for _, r := range seen {
		if excluded(r.FullName, r.Archived, r.Fork, conn.Exclude) {
			continue
		}
		// self-hosted servers are less trusted input than a SaaS host
		name, err := safeName(host+"/"+r.FullName, r.CloneURL)
		if err != nil {
			return nil, fmt.Errorf("connection %s: %w", conn.Name, err)
		}
		dir := RepoDir(dataDir, name)
		if err := Mirror(ctx, r.CloneURL, dir, gitCfg...); err != nil {
			return nil, fmt.Errorf("connection %s: mirror %s: %w", conn.Name, name, err)
		}
		repo := store.Repo{
			Name:             name,
			DisplayName:      r.FullName,
			CloneURL:         r.CloneURL,
			WebURL:           r.HTMLURL,
			DefaultBranch:    r.DefaultBranch,
			IsFork:           r.Fork,
			IsArchived:       r.Archived,
			IsPublic:         !r.Private,
			PushedAt:         r.UpdatedAt,
			ExternalID:       strconv.FormatInt(r.ID, 10),
			ExternalHostType: "gitea",
			ExternalHostURL:  base,
		}
		if err := st.UpsertRepo(ctx, repo); err != nil {
			return nil, fmt.Errorf("connection %s: upsert %s: %w", conn.Name, name, err)
		}
		names = append(names, name)
	}
	return names, nil
}
