package sync

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/store"
)

// ghAPIBase is a var so tests can point the adapter at a fake server.
var ghAPIBase = "https://api.github.com"

// ghRepo is the subset of the REST repo object phebs consumes (PORT_MAP §5).
type ghRepo struct {
	ID            int64      `json:"id"`
	FullName      string     `json:"full_name"`
	CloneURL      string     `json:"clone_url"`
	HTMLURL       string     `json:"html_url"`
	DefaultBranch string     `json:"default_branch"`
	Fork          bool       `json:"fork"`
	Archived      bool       `json:"archived"`
	Private       bool       `json:"private"`
	PushedAt      *time.Time `json:"pushed_at"`
}

func syncGitHub(ctx context.Context, st store.Store, dataDir string, conn config.Connection) ([]string, error) {
	c := &ghClient{base: ghAPIBase, token: conn.Token}

	seen := map[string]ghRepo{}
	collect := func(repos []ghRepo, err error) error {
		if err != nil {
			return err
		}
		for _, r := range repos {
			seen[r.FullName] = r
		}
		return nil
	}
	for _, org := range conn.Orgs {
		if err := collect(c.listRepos(ctx, "/orgs/"+org+"/repos?per_page=100&type=all")); err != nil {
			return nil, fmt.Errorf("connection %s: org %s: %w", conn.Name, org, err)
		}
	}
	for _, user := range conn.Users {
		if err := collect(c.listRepos(ctx, "/users/"+user+"/repos?per_page=100&type=owner")); err != nil {
			return nil, fmt.Errorf("connection %s: user %s: %w", conn.Name, user, err)
		}
	}
	for _, full := range conn.Repos {
		var r ghRepo
		if _, err := c.getJSON(ctx, c.base+"/repos/"+full, &r); err != nil {
			return nil, fmt.Errorf("connection %s: repo %s: %w", conn.Name, full, err)
		}
		seen[r.FullName] = r
	}

	// git config for authenticated clone/fetch; the token never lands in the
	// mirror's config or the repo row.
	var gitCfg []string
	if conn.Token != "" {
		basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + conn.Token))
		gitCfg = []string{"-c", "http.extraheader=Authorization: Basic " + basic}
	}

	var names []string
	for _, r := range seen {
		if excluded(r, conn.Exclude) {
			continue
		}
		name := "github.com/" + r.FullName
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
			PushedAt:         r.PushedAt,
			ExternalID:       strconv.FormatInt(r.ID, 10),
			ExternalHostType: "github",
			ExternalHostURL:  "https://github.com",
		}
		if err := st.UpsertRepo(ctx, repo); err != nil {
			return nil, fmt.Errorf("connection %s: upsert %s: %w", conn.Name, name, err)
		}
		names = append(names, name)
	}
	return names, nil
}

func excluded(r ghRepo, ex config.Exclude) bool {
	if ex.Archived && r.Archived || ex.Forks && r.Fork {
		return true
	}
	for _, pat := range ex.Repos {
		if ok, _ := path.Match(pat, r.FullName); ok {
			return true
		}
	}
	return false
}

type ghClient struct {
	base  string
	token string
}

// listRepos follows Link rel="next" pagination until exhausted.
func (c *ghClient) listRepos(ctx context.Context, p string) ([]ghRepo, error) {
	var all []ghRepo
	url := c.base + p
	for url != "" {
		var page []ghRepo
		next, err := c.getJSON(ctx, url, &page)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		url = next
	}
	return all, nil
}

// getJSON performs one GET, waiting out rate limits (Retry-After or
// X-RateLimit-Reset) and returning the rel="next" link if any.
func (c *ghClient) getJSON(ctx context.Context, url string, v any) (next string, err error) {
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return "", err
		}

		if wait, limited := rateLimited(resp); limited {
			_ = resp.Body.Close()
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(wait):
			}
			continue
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			return "", fmt.Errorf("GET %s: %s", url, resp.Status)
		}
		err = json.NewDecoder(resp.Body).Decode(v)
		_ = resp.Body.Close()
		if err != nil {
			return "", fmt.Errorf("GET %s: decode: %w", url, err)
		}
		return nextLink(resp.Header.Get("Link")), nil
	}
}

func rateLimited(resp *http.Response) (time.Duration, bool) {
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusTooManyRequests {
		return 0, false
	}
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		secs, _ := strconv.Atoi(ra)
		return time.Duration(secs) * time.Second, true
	}
	if resp.Header.Get("X-RateLimit-Remaining") == "0" {
		reset, _ := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64)
		wait := time.Until(time.Unix(reset, 0))
		if wait < 0 {
			wait = 0
		}
		return wait, true
	}
	return 0, false // plain 403: permissions, not rate limiting
}

// nextLink extracts the rel="next" URL from a Link header.
func nextLink(link string) string {
	for _, part := range strings.Split(link, ",") {
		u, rel, ok := strings.Cut(part, ";")
		if ok && strings.Contains(rel, `rel="next"`) {
			return strings.Trim(strings.TrimSpace(u), "<>")
		}
	}
	return ""
}
