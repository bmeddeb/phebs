package sync

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/store"
)

// glProject is the subset of the GitLab project object phebs consumes
// (PORT_MAP §5).
type glProject struct {
	ID                int64      `json:"id"`
	PathWithNamespace string     `json:"path_with_namespace"`
	HTTPURLToRepo     string     `json:"http_url_to_repo"`
	WebURL            string     `json:"web_url"`
	DefaultBranch     string     `json:"default_branch"`
	Archived          bool       `json:"archived"`
	Visibility        string     `json:"visibility"` // public | internal | private
	LastActivityAt    *time.Time `json:"last_activity_at"`
	ForkedFrom        *struct {
		ID int64 `json:"id"`
	} `json:"forked_from_project"`
}

func syncGitLab(ctx context.Context, st store.Store, dataDir string, conn config.Connection) ([]string, error) {
	base := strings.TrimSuffix(conn.URL, "/")
	if base == "" {
		base = "https://gitlab.com"
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("connection %s: gitlab url %q has no host", conn.Name, base)
	}
	host := u.Host

	c := &hostClient{base: base + "/api/v4"}
	if conn.Token != "" {
		c.auth = "Bearer " + conn.Token
	}

	seen := map[string]glProject{}
	collect := func(projects []glProject, err error) error {
		if err != nil {
			return err
		}
		for _, p := range projects {
			seen[p.PathWithNamespace] = p
		}
		return nil
	}
	// group and project paths contain slashes ("group/subgroup"); GitLab
	// takes them URL-encoded in the path segment.
	for _, group := range conn.Groups {
		p := "/groups/" + url.PathEscape(group) + "/projects?per_page=100&include_subgroups=true"
		if err := collect(listPages[glProject](ctx, c, p)); err != nil {
			return nil, fmt.Errorf("connection %s: group %s: %w", conn.Name, group, err)
		}
	}
	// visibility is requester-scoped: a token's own private projects are
	// included here (unlike GitHub's public-only user listing, T6.3).
	for _, user := range conn.Users {
		p := "/users/" + url.PathEscape(user) + "/projects?per_page=100"
		if err := collect(listPages[glProject](ctx, c, p)); err != nil {
			return nil, fmt.Errorf("connection %s: user %s: %w", conn.Name, user, err)
		}
	}
	for _, proj := range conn.Repos {
		var p glProject
		if _, err := c.getJSON(ctx, c.base+"/projects/"+url.PathEscape(proj), &p); err != nil {
			return nil, fmt.Errorf("connection %s: project %s: %w", conn.Name, proj, err)
		}
		seen[p.PathWithNamespace] = p
	}

	// authenticated clone/fetch: HTTP basic with the oauth2 pseudo-user; the
	// token never lands in the mirror's config or the repo row (redactArgs
	// scrubs it from persisted errors).
	var gitCfg []string
	if conn.Token != "" {
		basic := base64.StdEncoding.EncodeToString([]byte("oauth2:" + conn.Token))
		gitCfg = []string{"-c", "http.extraheader=Authorization: Basic " + basic}
	}

	var names []string
	for _, p := range seen {
		if excluded(p.PathWithNamespace, p.Archived, p.ForkedFrom != nil, conn.Exclude) {
			continue
		}
		// a self-hosted server is less trusted input than gitlab.com: reject
		// paths that would escape $DATA
		name, err := safeName(host+"/"+p.PathWithNamespace, p.HTTPURLToRepo)
		if err != nil {
			return nil, fmt.Errorf("connection %s: %w", conn.Name, err)
		}
		dir := RepoDir(dataDir, name)
		if err := Mirror(ctx, p.HTTPURLToRepo, dir, gitCfg...); err != nil {
			return nil, fmt.Errorf("connection %s: mirror %s: %w", conn.Name, name, err)
		}
		repo := store.Repo{
			Name:             name,
			DisplayName:      p.PathWithNamespace,
			CloneURL:         p.HTTPURLToRepo,
			WebURL:           p.WebURL,
			DefaultBranch:    p.DefaultBranch,
			IsFork:           p.ForkedFrom != nil,
			IsArchived:       p.Archived,
			IsPublic:         p.Visibility == "public",
			PushedAt:         p.LastActivityAt,
			ExternalID:       strconv.FormatInt(p.ID, 10),
			ExternalHostType: "gitlab",
			ExternalHostURL:  base,
		}
		if err := st.UpsertRepo(ctx, repo); err != nil {
			return nil, fmt.Errorf("connection %s: upsert %s: %w", conn.Name, name, err)
		}
		names = append(names, name)
	}
	return names, nil
}
