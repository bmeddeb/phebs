package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/store"
)

// ghAPIBase is a var so tests can point the adapter at a fake server.
var ghAPIBase = "https://api.github.com"

const (
	maxHostPages        = 1000
	hostRequestTimeout  = 30 * time.Second
	maxHostRedirects    = 10
	maxRateLimitWait    = time.Minute
	maxRateLimitRetries = 1
)

// ghRepo is the subset of the GitHub REST repository object phebs consumes.
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

// ghUser is the subset of a collaborator object consumed by permission
// syncing (T10.3).
type ghUser struct {
	Login string `json:"login"`
}

func syncGitHub(ctx context.Context, st store.Store, dataDir string, conn config.Connection, acl store.PermissionStore) ([]string, error) {
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
	// Installation repositories are authoritative for App-authenticated user
	// selectors: /users/{name}/repos is public-only even with an installation
	// token. Filter the granted set locally so private personal repositories
	// are included without reaching the identity-only /user endpoints.
	if appMode && (len(conn.Users) > 0 || len(conn.Orgs)+len(conn.Users)+len(conn.Repos) == 0) {
		granted, err := listInstallationRepos(ctx, c)
		if err != nil {
			return nil, fmt.Errorf("connection %s: installation repos: %w", conn.Name, err)
		}
		if len(conn.Users) == 0 {
			if err := collect(granted, nil); err != nil {
				return nil, err
			}
		} else if err := collect(filterInstallationUsers(granted, conn.Users), nil); err != nil {
			return nil, err
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
		if appMode {
			continue
		}
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
		cloneURL, err := SanitizeURL(r.CloneURL)
		if err != nil {
			return names, fmt.Errorf("connection %s: clone url for %s: %w", conn.Name, name, err)
		}
		if err := checkCloneURL(conn, cloneURL); err != nil {
			return names, fmt.Errorf("connection %s: %w", conn.Name, err)
		}
		repo := store.Repo{
			Name:             name,
			DisplayName:      r.FullName,
			CloneURL:         cloneURL,
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
		if err := mirrorAndUpsert(ctx, st, dataDir, repo, gitCfg...); err != nil {
			return names, fmt.Errorf("connection %s: %w", conn.Name, err)
		}
		names = append(names, name)
		if acl != nil && r.Private {
			// affiliation=all includes owner, direct, outside, and
			// org/team-derived collaborators.
			collaborators, err := listPages[ghUser](ctx, c,
				"/repos/"+r.FullName+"/collaborators?per_page=100&affiliation=all")
			identities := make([]string, 0, len(collaborators))
			for _, u := range collaborators {
				if u.Login != "" {
					identities = append(identities, hostIdentity("github.com", u.Login))
				}
			}
			mirrorACL(ctx, acl, name, identities, err)
		}
	}
	return names, nil
}

func filterInstallationUsers(repos []ghRepo, users []string) []ghRepo {
	selected := make([]ghRepo, 0, len(repos))
	for _, repo := range repos {
		owner, _, ok := strings.Cut(repo.FullName, "/")
		if !ok {
			continue
		}
		for _, user := range users {
			if strings.EqualFold(owner, user) {
				selected = append(selected, repo)
				break
			}
		}
	}
	return selected
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
	client *http.Client
}

// listPages follows Link rel="next" pagination until exhausted.
func listPages[T any](ctx context.Context, c *hostClient, p string) ([]T, error) {
	var all []T
	current, err := c.endpoint(p)
	if err != nil {
		return nil, err
	}
	visited := map[string]bool{}
	pages := 0
	for current != "" {
		if pages >= maxHostPages {
			return nil, fmt.Errorf("pagination exceeds %d pages", maxHostPages)
		}
		key := paginationKey(current)
		if visited[key] {
			return nil, fmt.Errorf("pagination cycle detected")
		}
		visited[key] = true
		var page []T
		next, err := c.getJSON(ctx, current, &page)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		current = next
		pages++
	}
	return all, nil
}

// getJSON performs one GET, waiting out rate limits (Retry-After or
// X-RateLimit-Reset) and returning the rel="next" link if any.
func (c *hostClient) getJSON(ctx context.Context, rawURL string, v any) (next string, err error) {
	requestURL, err := c.resolveRequestURL(rawURL)
	if err != nil {
		return "", err
	}
	client := c.requestClient()
	rateLimitRetries := 0
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
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
		resp, err := client.Do(req)
		if err != nil {
			return "", hostRequestError(err)
		}

		if wait, limited, limitErr := rateLimited(resp); limited {
			_ = resp.Body.Close()
			if limitErr != nil {
				return "", limitErr
			}
			if rateLimitRetries >= maxRateLimitRetries {
				return "", fmt.Errorf("rate-limit retry budget exhausted after %d retry", maxRateLimitRetries)
			}
			rateLimitRetries++
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(wait):
			}
			continue
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			return "", fmt.Errorf("API GET returned HTTP %d", resp.StatusCode)
		}
		err = json.NewDecoder(resp.Body).Decode(v)
		_ = resp.Body.Close()
		if err != nil {
			return "", fmt.Errorf("decode API response: %w", err)
		}
		rawNext := nextLink(resp.Header.Get("Link"))
		if rawNext == "" {
			return "", nil
		}
		responseURL := requestURL
		if resp.Request != nil && resp.Request.URL != nil {
			responseURL = resp.Request.URL.String()
		}
		return c.resolveLink(responseURL, rawNext)
	}
}

func (c *hostClient) endpoint(p string) (string, error) {
	return c.resolveRequestURL(strings.TrimRight(c.base, "/") + "/" + strings.TrimLeft(p, "/"))
}

func (c *hostClient) resolveRequestURL(raw string) (string, error) {
	base, err := url.Parse(c.base)
	if err != nil {
		return "", errors.New("invalid API base URL")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", errors.New("invalid API URL")
	}
	if !u.IsAbs() {
		u = base.ResolveReference(u)
	}
	if err := sameAPIOrigin(base, u); err != nil {
		return "", err
	}
	u.Fragment = ""
	return u.String(), nil
}

func (c *hostClient) resolveLink(current, rawLink string) (string, error) {
	currentURL, err := url.Parse(current)
	if err != nil {
		return "", err
	}
	ref, err := url.Parse(rawLink)
	if err != nil {
		return "", errors.New("invalid pagination link")
	}
	return c.resolveRequestURL(currentURL.ResolveReference(ref).String())
}

func sameAPIOrigin(base, candidate *url.URL) error {
	baseScheme, baseHost, ok := normalizedOrigin(base)
	if !ok {
		return errors.New("API base must be an HTTP(S) URL without userinfo")
	}
	candidateScheme, candidateHost, ok := normalizedOrigin(candidate)
	if !ok || candidateScheme != baseScheme || candidateHost != baseHost {
		return fmt.Errorf("refusing off-origin API URL (expected %s://%s)", baseScheme, baseHost)
	}
	return nil
}

func normalizedOrigin(u *url.URL) (scheme, host string, ok bool) {
	scheme = strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" || u.User != nil || u.Hostname() == "" {
		return "", "", false
	}
	name := strings.ToLower(u.Hostname())
	port := u.Port()
	if port == "" {
		if scheme == "http" {
			port = "80"
		} else {
			port = "443"
		}
	}
	return scheme, net.JoinHostPort(name, port), true
}

func (c *hostClient) requestClient() *http.Client {
	base := http.DefaultClient
	if c.client != nil {
		base = c.client
	}
	client := *base
	if client.Timeout <= 0 || client.Timeout > hostRequestTimeout {
		client.Timeout = hostRequestTimeout
	}
	previous := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxHostRedirects {
			return fmt.Errorf("stopped after %d redirects", maxHostRedirects)
		}
		baseURL, err := url.Parse(c.base)
		if err != nil {
			return err
		}
		if err := sameAPIOrigin(baseURL, req.URL); err != nil {
			return err
		}
		if previous != nil {
			return previous(req, via)
		}
		return nil
	}
	return &client
}

func paginationKey(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.Fragment = ""
	return u.String()
}

func hostRequestError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return fmt.Errorf("API request failed: %w", urlErr.Err)
	}
	return fmt.Errorf("API request failed: %w", err)
}

func rateLimited(resp *http.Response) (time.Duration, bool, error) {
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusTooManyRequests {
		return 0, false, nil
	}
	// Retry-After is delta-seconds OR an HTTP-date (RFC 9110); proxies/CDNs
	// emit either. A bare Atoi on the date form yields 0 → a hot retry loop,
	// so fall back to date parsing, then to a sane floor.
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(strings.TrimSpace(ra)); err == nil {
			if secs > int(maxRateLimitWait/time.Second) {
				return 0, true, fmt.Errorf("rate-limit wait exceeds maximum %s", maxRateLimitWait)
			}
			return boundedRateLimitWait(time.Duration(secs) * time.Second)
		}
		if t, err := http.ParseTime(ra); err == nil {
			return boundedRateLimitWait(time.Until(t))
		}
		return boundedRateLimitWait(time.Minute) // unparseable: back off, don't spin
	}
	if resp.Header.Get("X-RateLimit-Remaining") == "0" {
		reset, _ := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64)
		return boundedRateLimitWait(time.Until(time.Unix(reset, 0)))
	}
	return 0, false, nil // plain 403: permissions, not rate limiting
}

func boundedRateLimitWait(d time.Duration) (time.Duration, bool, error) {
	d = clampWait(d)
	if d > maxRateLimitWait {
		return 0, true, fmt.Errorf("rate-limit wait exceeds maximum %s", maxRateLimitWait)
	}
	return d, true, nil
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
