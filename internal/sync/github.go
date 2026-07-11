package sync

import (
	"context"
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
	c := &hostClient{base: ghAPIBase, accept: "application/vnd.github+json"}
	// token precedence: App installation token when configured, else PAT,
	// else anonymous (public repos only). gitToken is what git fetches use.
	gitToken := conn.Token
	appMode := !conn.App.IsZero()
	if appMode {
		tok, err := appToken(ctx, ghAPIBase, conn.App)
		if err != nil {
			return nil, fmt.Errorf("connection %s: %w", conn.Name, err)
		}
		gitToken = tok
	}
	if gitToken != "" {
		c.auth = "Bearer " + gitToken
	}

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
		if err := collect(listPages[ghRepo](ctx, c, "/orgs/"+org+"/repos?per_page=100&type=all")); err != nil {
			return nil, fmt.Errorf("connection %s: org %s: %w", conn.Name, org, err)
		}
	}
	// App mode with no selectors: sync exactly what the installation was
	// granted.
	if appMode && len(conn.Orgs)+len(conn.Users)+len(conn.Repos) == 0 {
		if err := collect(listInstallationRepos(ctx, c)); err != nil {
			return nil, fmt.Errorf("connection %s: installation repos: %w", conn.Name, err)
		}
	}

	// /users/{name}/repos only ever returns public repos, even with a PAT
	// (verified live, T6.3), so the token owner's account is ALSO listed via
	// /user/repos for its private repos. Union, not replacement: /user/repos
	// alone misses public repos when a fine-grained PAT is restricted to
	// select repositories, and a shrunken listing plus cleanup_orphans would
	// silently delete mirrors and shards. Installation tokens have no "own
	// account" (/user is 403 for them), so App mode skips the resolution.
	var login string
	if conn.Token != "" && !appMode && len(conn.Users) > 0 {
		var me struct {
			Login string `json:"login"`
		}
		if _, err := c.getJSON(ctx, c.base+"/user", &me); err != nil {
			return nil, fmt.Errorf("connection %s: resolve token owner: %w", conn.Name, err)
		}
		login = me.Login
	}
	for _, user := range conn.Users {
		if err := collect(listPages[ghRepo](ctx, c, "/users/"+user+"/repos?per_page=100&type=owner")); err != nil {
			return nil, fmt.Errorf("connection %s: user %s: %w", conn.Name, user, err)
		}
		if strings.EqualFold(user, login) { // GitHub logins are case-insensitive
			if err := collect(listPages[ghRepo](ctx, c, "/user/repos?per_page=100&type=owner")); err != nil {
				return nil, fmt.Errorf("connection %s: user %s: %w", conn.Name, user, err)
			}
		}
	}
	for _, full := range conn.Repos {
		var r ghRepo
		if _, err := c.getJSON(ctx, c.base+"/repos/"+full, &r); err != nil {
			return nil, fmt.Errorf("connection %s: repo %s: %w", conn.Name, full, err)
		}
		seen[r.FullName] = r
	}

	// git config for authenticated clone/fetch (installation tokens work as
	// x-access-token too); the token never lands in the mirror's config or
	// the repo row.
	var gitCfg []string
	if gitToken != "" {
		gitCfg = basicExtraheader("x-access-token:" + gitToken)
	}

	var names []string
	for _, r := range seen {
		if excluded(r.FullName, r.Archived, r.Fork, conn.Exclude) {
			continue
		}
		name := "github.com/" + r.FullName
		if err := checkCloneURL(conn, r.CloneURL); err != nil {
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

// excluded applies a connection's exclude filters to one listed repo,
// matched on its host-native full path (github "owner/name", gitlab
// "group/subgroup/project").
func excluded(fullName string, archived, fork bool, ex config.Exclude) bool {
	if ex.Archived && archived || ex.Forks && fork {
		return true
	}
	for _, pat := range ex.Repos {
		if ok, _ := path.Match(pat, fullName); ok {
			return true
		}
	}
	return false
}

// hostClient is the shared REST plumbing for code-host adapters: one GET
// with rate-limit waits plus Link-header pagination (GitHub, GitLab, and
// Gitea all speak RFC 5988 Link + Retry-After). auth is the full
// Authorization header value ("Bearer x", "token x"); empty sends none.
type hostClient struct {
	base   string
	auth   string
	accept string // Accept header; empty means "application/json"
}

// listPages follows Link rel="next" pagination until exhausted.
func listPages[T any](ctx context.Context, c *hostClient, p string) ([]T, error) {
	var all []T
	url := c.base + p
	for url != "" {
		var page []T
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
func (c *hostClient) getJSON(ctx context.Context, url string, v any) (next string, err error) {
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return "", err
		}
		accept := c.accept
		if accept == "" {
			accept = "application/json"
		}
		req.Header.Set("Accept", accept)
		if c.auth != "" {
			req.Header.Set("Authorization", c.auth)
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
	// Retry-After is delta-seconds OR an HTTP-date (RFC 9110); proxies/CDNs
	// emit either. A bare Atoi on the date form yields 0 → a hot retry loop,
	// so fall back to date parsing, then to a sane floor.
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(strings.TrimSpace(ra)); err == nil {
			return clampWait(time.Duration(secs) * time.Second), true
		}
		if t, err := http.ParseTime(ra); err == nil {
			return clampWait(time.Until(t)), true
		}
		return 60 * time.Second, true // unparseable: back off, don't spin
	}
	if resp.Header.Get("X-RateLimit-Remaining") == "0" {
		reset, _ := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64)
		return clampWait(time.Until(time.Unix(reset, 0))), true
	}
	return 0, false // plain 403: permissions, not rate limiting
}

// clampWait floors a rate-limit wait at 1s so a zero/negative value (clock
// skew, already-past reset) can't spin the retry loop.
func clampWait(d time.Duration) time.Duration {
	if d < time.Second {
		return time.Second
	}
	return d
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
