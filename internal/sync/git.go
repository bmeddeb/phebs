package sync

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/repowork"
	"github.com/bmeddeb/phebs/internal/store"
)

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, dispatchadmission.ProductionTool("git"), args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := dispatchadmission.RunProduction(ctx, dispatchadmission.SiteSyncGit, cmd); err != nil {
		return "", gitCommandError(ctx, args, out.String(), err)
	}
	return strings.TrimSpace(out.String()), nil
}

func gitCommandError(
	ctx context.Context,
	args []string,
	output string,
	err error,
) error {
	werr := fmt.Errorf(
		"git %s: %w\n%s",
		redactArgs(args),
		err,
		redactGitOutput(output, args),
	)
	if ctxErr := ctx.Err(); ctxErr != nil {
		// CommandContext commonly reports a killed child. Cancellation remains
		// the retry/classification authority while the redacted process error
		// stays in the message for diagnosis.
		return fmt.Errorf("%v: %w", werr, ctxErr)
	}
	if isAuthFailure(output) {
		return store.WithClass(store.ClassAuth, werr)
	}
	return werr
}

func redactGitOutput(output string, args []string) string {
	const prefix = "http.extraheader=Authorization: Basic "
	for _, arg := range args {
		if !strings.HasPrefix(arg, prefix) {
			continue
		}
		encoded := strings.TrimPrefix(arg, prefix)
		output = strings.ReplaceAll(output, encoded, "<redacted>")
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			continue
		}
		userinfo := string(decoded)
		output = strings.ReplaceAll(output, userinfo, "<redacted>")
		username, password, _ := strings.Cut(userinfo, ":")
		for _, credential := range []string{username, password} {
			if credential != "" {
				output = strings.ReplaceAll(output, credential, "<redacted>")
			}
		}
	}
	return redactURLCredentials(output)
}

// urlCredRE matches userinfo in a URL (scheme://user:pass@host) so legacy
// mirror configuration and untrusted server responses can be scrubbed from
// error strings.
var urlCredRE = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.\-]*://)[^/@\s]+@`)

// opaqueHTTPCredRE also catches malformed/opaque spellings such as
// https:user:password@host and https:/user@host. Git may echo these strings
// even when URL parsing rejects them.
var opaqueHTTPCredRE = regexp.MustCompile(`(?i)(https?:/*)[^/?#@\s]*@`)

var schemePrefixRE = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.\-]*:`)

func redactURLCredentials(s string) string {
	s = opaqueHTTPCredRE.ReplaceAllString(s, "$1<redacted>@")
	return urlCredRE.ReplaceAllString(s, "$1<redacted>@")
}

// redactArgs joins argv for error messages with credentials scrubbed — errors
// are persisted to the job row and logged, so neither the GitHub PAT (injected
// via `-c http.extraheader=Authorization: …`) nor URL userinfo may appear
// there.
func redactArgs(args []string) string {
	out := make([]string, len(args))
	for i, a := range args {
		if strings.Contains(a, "http.extraheader") || strings.Contains(a, "Authorization") {
			out[i] = "http.extraheader=<redacted>"
			continue
		}
		out[i] = redactURLCredentials(a)
	}
	return strings.Join(out, " ")
}

// isAuthFailure sniffs git's credential complaints (T3.3 clone-auth class).
func isAuthFailure(output string) bool {
	for _, marker := range []string{
		"Authentication failed",
		"could not read Username",
		"could not read Password",
		"Permission denied",
		"Invalid username or token",
		"HTTP Basic: Access denied",
	} {
		if strings.Contains(output, marker) {
			return true
		}
	}
	return false
}

// SanitizeURL canonicalizes clone URLs before persistence or command use.
// HTTP(S) URLs must be hierarchical and have a host; their userinfo, query,
// and fragment are stripped. SSH may retain a username but never a password.
// Userinfo on other schemes is rejected.
func SanitizeURL(raw string) (string, error) {
	if isSCPLikeURL(raw) {
		return raw, nil
	}
	if !schemePrefixRE.MatchString(raw) && !strings.HasPrefix(raw, "//") {
		return raw, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", errors.New("parse clone url: invalid URL")
	}
	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "http", "https":
		if u.Opaque != "" || u.Hostname() == "" {
			return "", errors.New("clone URL must use hierarchical HTTP(S) syntax with a host")
		}
		u.User = nil
		u.RawQuery = ""
		u.ForceQuery = false
		u.Fragment = ""
		return u.String(), nil
	case "ssh", "git+ssh", "ssh+git", "sftp":
		if u.User != nil {
			if _, hasPassword := u.User.Password(); hasPassword {
				return "", errors.New("clone URL must not contain an SSH password")
			}
			return stripURLSuffix(raw, u), nil
		}
		if userinfo, ok := opaqueUserinfo(u.Opaque); ok && strings.Contains(userinfo, ":") {
			return "", errors.New("clone URL must not contain an SSH password")
		}
		return stripURLSuffix(raw, u), nil
	default:
		if u.User != nil {
			return "", errors.New("clone URL must not contain userinfo for this scheme")
		}
		if _, ok := opaqueUserinfo(u.Opaque); ok {
			return "", errors.New("clone URL must not contain userinfo for this scheme")
		}
		return stripURLSuffix(raw, u), nil
	}
}

// SanitizeHTTPURL applies the persisted public-link policy: only hierarchical
// HTTP(S) URLs with a host are retained, and userinfo/query/fragment data is
// stripped before storage or serialization.
func SanitizeHTTPURL(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	safe, err := SanitizeURL(raw)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(safe)
	if err != nil || u.Opaque != "" || u.Hostname() == "" || !strings.EqualFold(u.Scheme, "http") && !strings.EqualFold(u.Scheme, "https") {
		return "", errors.New("public URL must use hierarchical HTTP(S) syntax with a host")
	}
	return safe, nil
}

func isSCPLikeURL(raw string) bool {
	host, repoPath, ok := strings.Cut(raw, ":")
	if !ok || repoPath == "" || strings.Contains(host, "/") || strings.HasPrefix(repoPath, "//") {
		return false
	}
	switch strings.ToLower(host) {
	case "http", "https", "ssh", "git", "ftp", "file", "sftp", "rsync", "git+ssh", "ssh+git":
		return false
	default:
		return true
	}
}

func stripURLSuffix(raw string, u *url.URL) string {
	if u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" {
		return raw
	}
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	return u.String()
}

func opaqueUserinfo(opaque string) (string, bool) {
	authority, _, _ := strings.Cut(opaque, "/")
	userinfo, _, ok := strings.Cut(authority, "@")
	return userinfo, ok
}

// HTTPBasicAuthConfig builds transient git configuration for Basic auth.
// Callers pass the returned arguments to Mirror; they are applied only to
// that git process and never written into the bare repository's config.
func HTTPBasicAuthConfig(username, password string) []string {
	if username == "" && password == "" {
		return nil
	}
	basic := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	return []string{"-c", "http.extraheader=Authorization: Basic " + basic}
}

// Mirror clones cloneURL as a bare mirror into dir, or incrementally fetches
// if the mirror already exists. gitCfg holds per-invocation `-c` flags (e.g.
// auth headers) that must never persist into the mirror's config.
func Mirror(ctx context.Context, cloneURL, dir string, gitCfg ...string) error {
	unlock, err := repowork.LockContext(ctx, dir)
	if err != nil {
		return fmt.Errorf("lock mirror: %w", err)
	}
	defer unlock()
	return mirrorLocked(ctx, cloneURL, dir, gitCfg...)
}

// mirrorLocked performs Mirror's mutation while the caller holds the
// process-wide repository lock. It lets higher-level operations keep repo
// identity checks, disk mutation, and row publication in one critical section.
func mirrorLocked(ctx context.Context, cloneURL, dir string, gitCfg ...string) error {
	sanitizedURL, err := SanitizeURL(cloneURL)
	if err != nil {
		return err
	}

	if _, err := os.Stat(filepath.Join(dir, "HEAD")); err == nil {
		// Always refresh origin before fetching. This scrubs credentials from
		// mirrors created by older versions and applies configured URL changes.
		if _, err := runGit(ctx, dir, "remote", "set-url", "origin", sanitizedURL); err != nil {
			return err
		}
		_, err = runGit(ctx, dir, append(gitCfg, "fetch", "--prune", "origin")...)
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return fmt.Errorf("create repo dir: %w", err)
	}
	_, err = runGit(ctx, "", append(gitCfg, "clone", "--mirror", sanitizedURL, dir)...)
	return err
}

// GitConfig sets a config key in a repo (used for zoekt.name).
func GitConfig(ctx context.Context, dir, key, value string) (string, error) {
	return runGit(ctx, dir, "config", key, value)
}

// DefaultBranch reads the symref target of HEAD in a bare repo.
func DefaultBranch(ctx context.Context, dir string) (string, error) {
	return runGit(ctx, dir, "symbolic-ref", "--short", "HEAD")
}

// Head returns the commit hash HEAD points at in a bare repo.
func Head(ctx context.Context, dir string) (string, error) {
	return runGit(ctx, dir, "rev-parse", "HEAD")
}

// ResolveCommit peels an admitted branch/tag ref to the immutable commit that
// zoekt will record for it. The `^{commit}` suffix rejects refs that do not
// ultimately name a commit.
func ResolveCommit(ctx context.Context, dir, ref string) (string, error) {
	return runGit(ctx, dir, "rev-parse", ref+"^{commit}")
}
