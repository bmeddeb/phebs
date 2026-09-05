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

// glProject is the subset of the GitLab project object phebs consumes.
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

// glMember is the subset of a project member object consumed by permission
// syncing (T10.3). Reporter (20) is the lowest level that reads code in a
// private project; /members/all includes inherited group members.
type glMember struct {
	Username    string `json:"username"`
	State       string `json:"state"`
	AccessLevel int    `json:"access_level"`
}

func syncGitLab(ctx context.Context, st store.Store, dataDir string, conn config.Connection, acl store.PermissionStore) ([]string, error) {
	base := strings.TrimSuffix(conn.URL, "/")
	if base == "" {
		base = "https://gitlab.com"
	}
	prefix, err := hostPrefix(base)
	if err != nil {
		return nil, fmt.Errorf("connection %s: gitlab: %w", conn.Name, err)
	}

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
	// takes them URL-encoded in the path segment. with_shared=false: projects
	// merely shared INTO the group live in foreign namespaces the user never
	// selected (Epic 7 review).
	for _, group := range conn.Groups {
		p := "/groups/" + url.PathEscape(group) + "/projects?per_page=100&include_subgroups=true&with_shared=false"
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
		gitCfg = basicExtraheader("oauth2:" + conn.Token)
	}

	var names []string
	for _, p := range seen {
		if excluded(p.PathWithNamespace, p.Archived, p.ForkedFrom != nil, conn.Exclude) {
			continue
		}
		cloneURL, err := SanitizeURL(p.HTTPURLToRepo)
		if err != nil {
			return names, fmt.Errorf("connection %s: sanitize clone url: %w", conn.Name, err)
		}
		// a self-hosted server is less trusted input than gitlab.com: reject
		// paths that would escape $DATA
		name, err := safeName(prefix+"/"+p.PathWithNamespace, cloneURL)
		if err != nil {
			return names, fmt.Errorf("connection %s: %w", conn.Name, err)
		}
		if err := checkCloneURL(conn, cloneURL); err != nil {
			return names, fmt.Errorf("connection %s: %w", conn.Name, err)
		}
		repo := store.Repo{
			Name:             name,
			DisplayName:      p.PathWithNamespace,
			CloneURL:         cloneURL,
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
		if err := mirrorAndUpsert(ctx, st, dataDir, repo, gitCfg...); err != nil {
			return names, fmt.Errorf("connection %s: %w", conn.Name, err)
		}
		names = append(names, name)
		if acl != nil && p.Visibility != "public" { // "internal" also fails closed
			members, err := listPages[glMember](ctx, c,
				"/projects/"+strconv.FormatInt(p.ID, 10)+"/members/all?per_page=100")
			identities := make([]string, 0, len(members))
			for _, m := range members {
				if m.Username != "" && m.State == "active" && m.AccessLevel >= 20 {
					identities = append(identities, hostIdentity(prefix, m.Username))
				}
			}
			mirrorACL(ctx, acl, name, identities, err)
		}
	}
	return names, nil
}
