package sync

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/bmeddeb/phebs/internal/config"
)

// appToken exchanges a GitHub App's private key for a fresh installation
// access token (~1h validity). Called once per sync run, so every run
// refreshes; the exchange is one signed JWT + one POST — no cache to go
// stale. ponytail: a single sync outliving the token (>1h of clones) fails
// its remaining fetches and heals on the next run; cache + mid-run refresh
// if that ever bites.
func appToken(ctx context.Context, apiBase string, app config.GitHubApp) (string, error) {
	key, err := loadAppKey(app)
	if err != nil {
		return "", err
	}
	jwt, err := appJWT(app.ID, key, time.Now())
	if err != nil {
		return "", err
	}

	c := &hostClient{base: apiBase}
	tokenURL, err := c.endpoint(fmt.Sprintf("/app/installations/%d/access_tokens", app.InstallationID))
	if err != nil {
		return "", fmt.Errorf("create installation token: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+jwt)
	resp, err := c.requestClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("create installation token: %w", hostRequestError(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("create installation token: HTTP %d", resp.StatusCode)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("create installation token: decode: %w", err)
	}
	if out.Token == "" {
		return "", fmt.Errorf("create installation token: empty token in response")
	}
	return out.Token, nil
}

func loadAppKey(app config.GitHubApp) (*rsa.PrivateKey, error) {
	pemBytes := []byte(app.PrivateKey)
	if app.PrivateKeyPath != "" {
		b, err := os.ReadFile(app.PrivateKeyPath)
		if err != nil {
			return nil, fmt.Errorf("app private key: %w", err)
		}
		pemBytes = b
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("app private key: no PEM block found")
	}
	// GitHub issues PKCS#1 ("RSA PRIVATE KEY"); accept PKCS#8 too
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("app private key: parse: %w", err)
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("app private key: not an RSA key")
	}
	return rk, nil
}

// appJWT builds the App's RS256 self-issued JWT (iss = app id, ≤10min
// lifetime, 60s clock-skew backdate) — the only JWT phebs ever needs, so
// stdlib crypto instead of a JWT dependency.
func appJWT(appID int64, key *rsa.PrivateKey, now time.Time) (string, error) {
	b64 := base64.RawURLEncoding.EncodeToString
	header := b64([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]any{
		"iat": now.Add(-60 * time.Second).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": strconv.FormatInt(appID, 10),
	})
	signing := header + "." + b64(claims)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("sign app jwt: %w", err)
	}
	return signing + "." + b64(sig), nil
}

// listInstallationRepos pages GET /installation/repositories — the repos the
// installation was granted. Unlike the other listings the response is a
// wrapper object, not a bare array.
func listInstallationRepos(ctx context.Context, c *hostClient) ([]ghRepo, error) {
	var all []ghRepo
	current, err := c.endpoint("/installation/repositories?per_page=100")
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
		var page struct {
			Repositories []ghRepo `json:"repositories"`
		}
		next, err := c.getJSON(ctx, current, &page)
		if err != nil {
			return nil, err
		}
		all = append(all, page.Repositories...)
		current = next
		pages++
	}
	return all, nil
}
