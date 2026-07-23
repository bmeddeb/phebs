// Command release-smoke exercises an assembled release from empty state.
// The server and both Go children come exclusively from the verified bundle;
// Git and the pinned SurrealDB executable are explicit host prerequisites.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/bmeddeb/phebs/internal/releasebundle"
)

const (
	adminEmail    = "release-smoke@example.invalid"
	adminPassword = "release-smoke-password"
	searchNeedle  = "phebsReleaseSmokeNeedle"
	maxHTTPBody   = 4 << 20
)

var errServerExited = errors.New("bundled phebs exited")

type options struct {
	bundle  string
	surreal string
	timeout time.Duration
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("release-smoke", flag.ContinueOnError)
	var opts options
	flags.StringVar(&opts.bundle, "bundle", "", "verified release directory")
	flags.StringVar(&opts.surreal, "surreal", "", "SurrealDB executable (default: PATH)")
	flags.DurationVar(&opts.timeout, "timeout", 2*time.Minute, "end-to-end deadline")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(opts.bundle) == "" {
		return errors.New("usage: go run ./scripts/release-smoke -bundle DIR [-surreal PATH]")
	}
	if opts.timeout <= 0 {
		return errors.New("timeout must be positive")
	}
	return smoke(opts)
}

func smoke(opts options) error {
	bundle, err := filepath.Abs(opts.bundle)
	if err != nil {
		return fmt.Errorf("resolve bundle: %w", err)
	}
	manifest, err := releasebundle.Verify(bundle)
	if err != nil {
		return fmt.Errorf("verify bundle before smoke: %w", err)
	}
	if manifest.GOOS != runtime.GOOS || manifest.GOARCH != runtime.GOARCH {
		return fmt.Errorf("bundle target %s/%s cannot run on smoke host %s/%s",
			manifest.GOOS, manifest.GOARCH, runtime.GOOS, runtime.GOARCH)
	}
	surreal, err := prerequisite(opts.surreal, "surreal")
	if err != nil {
		return err
	}
	if _, err := prerequisite("", "git"); err != nil {
		return err
	}

	root, err := os.MkdirTemp("", "phebs-release-smoke-")
	if err != nil {
		return fmt.Errorf("create smoke workspace: %w", err)
	}
	defer func() { _ = os.RemoveAll(root) }()
	fixture := filepath.Join(root, "fixture-repo")
	commit, err := createFixture(fixture)
	if err != nil {
		return err
	}
	repoName, err := localRepoName(fixture)
	if err != nil {
		return err
	}
	addr, err := availableAddress()
	if err != nil {
		return err
	}
	configPath := filepath.Join(root, "phebs-smoke.yaml")
	if err := writeConfig(configPath, filepath.Join(root, "data"), addr, fixture); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), opts.timeout)
	defer cancel()
	logs := &boundedLog{limit: 1 << 20}
	server := exec.CommandContext(ctx, filepath.Join(bundle, "phebs"), "serve", "-config", configPath)
	server.Dir = bundle
	server.Env = exactServerEnv(os.Environ(), bundle, surreal)
	server.Stdout, server.Stderr = logs, logs
	if err := server.Start(); err != nil {
		return fmt.Errorf("start bundled phebs: %w", err)
	}
	waited := make(chan error, 1)
	go func() { waited <- server.Wait() }()
	stopped := false
	defer func() {
		if !stopped && server.Process != nil {
			_ = server.Process.Kill()
			<-waited
		}
	}()

	baseURL := "http://" + addr
	jar, err := cookiejar.New(nil)
	if err != nil {
		return fmt.Errorf("create cookie jar: %w", err)
	}
	client := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	if err := waitForHealth(ctx, client, baseURL, waited); err != nil {
		if errors.Is(err, errServerExited) {
			stopped = true // waitForHealth consumed the one Wait result.
		}
		return smokeError(err, logs)
	}
	if err := login(ctx, client, baseURL); err != nil {
		return smokeError(err, logs)
	}
	if err := verifyVersion(ctx, client, baseURL, manifest.Version); err != nil {
		return smokeError(err, logs)
	}
	if err := waitForIndex(ctx, client, baseURL, repoName, commit); err != nil {
		return smokeError(err, logs)
	}
	if err := verifySearchAndBrowse(ctx, client, baseURL, repoName, commit); err != nil {
		return smokeError(err, logs)
	}
	if err := verifyAtlasDark(ctx, client, baseURL); err != nil {
		return smokeError(err, logs)
	}
	waitConsumed, err := stopServer(server, waited)
	if waitConsumed {
		stopped = true
	}
	if err != nil {
		return smokeError(err, logs)
	}
	fmt.Printf("release smoke passed: %s %s sync->index->search, pinned browse, Contract Atlas dark\n",
		manifest.Version, manifest.Commit)
	return nil
}

func prerequisite(configured, name string) (string, error) {
	path := strings.TrimSpace(configured)
	if path == "" {
		found, err := exec.LookPath(name)
		if err != nil {
			return "", fmt.Errorf("declared prerequisite %s not found: %w", name, err)
		}
		path = found
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s prerequisite: %w", name, err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("inspect %s prerequisite: %w", name, err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("declared prerequisite %s is not an executable regular file", name)
	}
	return absolute, nil
}

func createFixture(dir string) (string, error) {
	if err := os.Mkdir(dir, 0o755); err != nil {
		return "", fmt.Errorf("create fixture repo: %w", err)
	}
	if err := runGit(dir, "init", "--initial-branch=main"); err != nil {
		return "", err
	}
	if err := runGit(dir, "config", "user.name", "Phebs Release Smoke"); err != nil {
		return "", err
	}
	if err := runGit(dir, "config", "user.email", adminEmail); err != nil {
		return "", err
	}
	source := "package smoke\n\nconst " + searchNeedle + " = \"bundle-only\"\n"
	if err := os.WriteFile(filepath.Join(dir, "smoke.go"), []byte(source), 0o644); err != nil {
		return "", fmt.Errorf("write fixture source: %w", err)
	}
	if err := runGit(dir, "add", "smoke.go"); err != nil {
		return "", err
	}
	if err := runGit(dir, "commit", "-m", "release smoke fixture"); err != nil {
		return "", err
	}
	command := exec.Command("git", "-C", dir, "rev-parse", "HEAD")
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("resolve fixture commit: %w", err)
	}
	commit := strings.TrimSpace(string(output))
	if len(commit) != 40 {
		return "", fmt.Errorf("fixture commit has unexpected shape %q", commit)
	}
	return commit, nil
}

func runGit(dir string, args ...string) error {
	full := append([]string{"-C", dir}, args...)
	command := exec.Command("git", full...)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func localRepoName(path string) (string, error) {
	clean, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve fixture repo: %w", err)
	}
	clean = strings.TrimSuffix(clean, ".git")
	relative := strings.Trim(filepath.ToSlash(filepath.Clean(clean)), "/")
	if relative == "" || strings.Contains(relative, "..") {
		return "", errors.New("fixture path cannot produce a safe repository name")
	}
	return "local/" + relative, nil
}

func availableAddress() (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("reserve smoke address: %w", err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		return "", fmt.Errorf("release smoke address: %w", err)
	}
	return addr, nil
}

func writeConfig(path, dataDir, addr, fixture string) error {
	config := struct {
		Server struct {
			Addr    string `json:"addr"`
			DataDir string `json:"data_dir"`
		} `json:"server"`
		Auth struct {
			CookieSecure bool `json:"cookie_secure"`
			Bootstrap    struct {
				Email    string `json:"email"`
				Password string `json:"password"`
			} `json:"bootstrap_user"`
		} `json:"auth"`
		Sync struct {
			PollInterval   string `json:"poll_interval"`
			ResyncInterval string `json:"resync_interval"`
		} `json:"sync"`
		Connections []struct {
			Name string `json:"name"`
			Type string `json:"type"`
			URL  string `json:"url"`
		} `json:"connections"`
	}{}
	config.Server.Addr, config.Server.DataDir = addr, dataDir
	config.Auth.CookieSecure = false
	config.Auth.Bootstrap.Email, config.Auth.Bootstrap.Password = adminEmail, adminPassword
	config.Sync.PollInterval, config.Sync.ResyncInterval = "100ms", "0"
	config.Connections = append(config.Connections, struct {
		Name string `json:"name"`
		Type string `json:"type"`
		URL  string `json:"url"`
	}{Name: "release-smoke", Type: "git", URL: fixture})
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("encode smoke config: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write smoke config: %w", err)
	}
	return nil
}

func exactServerEnv(environ []string, bundle, surreal string) []string {
	denied := map[string]bool{
		"PHEBS_ZOEKT_GIT_INDEX":        true,
		"PHEBS_BUF":                    true,
		"PHEBS_SURREAL":                true,
		"PHEBS_INVESTIGATION_FIXTURES": true,
		"PHEBS_CONTRACT_ATLAS_FIXTURE": true,
	}
	filtered := make([]string, 0, len(environ)+3)
	for _, value := range environ {
		key, _, _ := strings.Cut(value, "=")
		if !denied[key] {
			filtered = append(filtered, value)
		}
	}
	return append(filtered,
		"PHEBS_ZOEKT_GIT_INDEX="+filepath.Join(bundle, "bin", "zoekt-git-index"),
		"PHEBS_BUF="+filepath.Join(bundle, "bin", "buf"),
		"PHEBS_SURREAL="+surreal,
	)
}

func waitForHealth(ctx context.Context, client *http.Client, baseURL string, exited <-chan error) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/health", nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err == nil {
			_, readErr := readResponse(response)
			if readErr == nil && response.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case err := <-exited:
			return fmt.Errorf("%w before health: %v", errServerExited, err)
		case <-ctx.Done():
			return fmt.Errorf("wait for authenticated server health: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func login(ctx context.Context, client *http.Client, baseURL string) error {
	body, err := json.Marshal(map[string]string{"email": adminEmail, "password": adminPassword})
	if err != nil {
		return err
	}
	status, response, err := request(ctx, client, http.MethodPost, baseURL+"/api/auth/login", body)
	if err != nil {
		return fmt.Errorf("bootstrap login: %w", err)
	}
	if status != http.StatusOK {
		return fmt.Errorf("bootstrap login status %d: %s", status, response)
	}
	var result struct {
		Authenticated bool   `json:"authenticated"`
		CSRFToken     string `json:"csrf_token"`
	}
	if err := json.Unmarshal(response, &result); err != nil {
		return fmt.Errorf("decode bootstrap login: %w", err)
	}
	if !result.Authenticated || result.CSRFToken == "" {
		return errors.New("bootstrap login did not establish an authenticated browser session")
	}
	return nil
}

func verifyVersion(ctx context.Context, client *http.Client, baseURL, want string) error {
	status, body, err := request(ctx, client, http.MethodGet, baseURL+"/api/version", nil)
	if err != nil {
		return fmt.Errorf("read authenticated version: %w", err)
	}
	if status != http.StatusOK {
		return fmt.Errorf("authenticated version status %d: %s", status, body)
	}
	var result struct {
		Version      string   `json:"version"`
		Capabilities []string `json:"capabilities"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("decode authenticated version: %w", err)
	}
	if result.Version != want {
		return fmt.Errorf("running version %q does not match manifest %q", result.Version, want)
	}
	for _, capability := range result.Capabilities {
		if capability == "contract-atlas" {
			return errors.New("default-dark smoke exposed the Contract Atlas capability")
		}
	}
	return nil
}

func waitForIndex(ctx context.Context, client *http.Client, baseURL, repoName, commit string) error {
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	for {
		status, body, err := request(ctx, client, http.MethodGet, baseURL+"/api/repo-status", nil)
		if err == nil && status == http.StatusOK {
			var rows []struct {
				Name          string `json:"name"`
				IndexedCommit string `json:"indexed_commit_hash"`
				LatestStatus  string `json:"latest_indexing_job_status"`
				LastIndexJob  *struct {
					Status string `json:"status"`
					Error  string `json:"error"`
				} `json:"last_index_job"`
			}
			if err := json.Unmarshal(body, &rows); err != nil {
				return fmt.Errorf("decode repository status: %w", err)
			}
			for _, row := range rows {
				if row.Name != repoName {
					continue
				}
				if row.LastIndexJob != nil && row.LastIndexJob.Status == "failed" {
					return fmt.Errorf("fixture indexing failed: %s", row.LastIndexJob.Error)
				}
				if row.IndexedCommit == commit && row.LatestStatus == "done" {
					return nil
				}
			}
		} else if err == nil {
			return fmt.Errorf("repository status %d: %s", status, body)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for sync and index of %s: %w", repoName, ctx.Err())
		case <-ticker.C:
		}
	}
}

func verifySearchAndBrowse(ctx context.Context, client *http.Client, baseURL, repoName, commit string) error {
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	for {
		found, err := searchFixture(ctx, client, baseURL, repoName, commit)
		if err != nil {
			return err
		}
		if found {
			break
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for indexed fixture to become searchable: %w", ctx.Err())
		case <-ticker.C:
		}
	}

	params := url.Values{"repo": {repoName}, "path": {"smoke.go"}, "ref": {commit}}
	status, body, err := request(ctx, client, http.MethodGet, baseURL+"/api/source?"+params.Encode(), nil)
	if err != nil {
		return fmt.Errorf("browse pinned fixture source: %w", err)
	}
	if status != http.StatusOK {
		return fmt.Errorf("browse pinned fixture source status %d: %s", status, body)
	}
	var source struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if err := json.Unmarshal(body, &source); err != nil {
		return fmt.Errorf("decode pinned fixture source: %w", err)
	}
	if source.Encoding != "utf8" || !strings.Contains(source.Content, searchNeedle) {
		return errors.New("pinned fixture source did not contain the indexed needle")
	}

	params.Del("path")
	status, body, err = request(ctx, client, http.MethodGet, baseURL+"/api/folder_contents?"+params.Encode(), nil)
	if err != nil {
		return fmt.Errorf("browse pinned fixture folder: %w", err)
	}
	if status != http.StatusOK || !bytes.Contains(body, []byte(`"name":"smoke.go"`)) {
		return fmt.Errorf("pinned fixture folder response %d: %s", status, body)
	}
	return nil
}

func searchFixture(ctx context.Context, client *http.Client, baseURL, repoName, commit string) (bool, error) {
	query := url.Values{"q": {searchNeedle}, "max_matches": {"5"}}
	status, body, err := request(ctx, client, http.MethodGet, baseURL+"/api/search?"+query.Encode(), nil)
	if err != nil {
		return false, fmt.Errorf("search fixture: %w", err)
	}
	if status != http.StatusOK {
		return false, fmt.Errorf("search fixture status %d: %s", status, body)
	}
	var search struct {
		Files []struct {
			Repo string `json:"repo"`
			Path string `json:"path"`
			Ref  string `json:"ref"`
		} `json:"files"`
		Stats struct {
			MatchCount int `json:"match_count"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(body, &search); err != nil {
		return false, fmt.Errorf("decode fixture search: %w", err)
	}
	return len(search.Files) == 1 && search.Stats.MatchCount >= 1 &&
		search.Files[0].Repo == repoName && search.Files[0].Path == "smoke.go" &&
		search.Files[0].Ref == commit, nil
}

func verifyAtlasDark(ctx context.Context, client *http.Client, baseURL string) error {
	status, body, err := request(ctx, client, http.MethodGet, baseURL+"/api/contract_atlas", nil)
	if err != nil {
		return fmt.Errorf("probe default-dark Contract Atlas: %w", err)
	}
	if status != http.StatusNotFound {
		return fmt.Errorf("default-dark Contract Atlas status %d, want 404: %s", status, body)
	}
	return nil
}

func request(ctx context.Context, client *http.Client, method, target string, body []byte) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	data, err := readResponse(response)
	return response.StatusCode, data, err
}

func readResponse(response *http.Response) ([]byte, error) {
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxHTTPBody+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxHTTPBody {
		return nil, errors.New("HTTP response exceeded smoke limit")
	}
	return data, nil
}

func stopServer(command *exec.Cmd, waited <-chan error) (bool, error) {
	if err := command.Process.Signal(os.Interrupt); err != nil {
		return false, fmt.Errorf("signal bundled phebs: %w", err)
	}
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	select {
	case err := <-waited:
		if err != nil {
			return true, fmt.Errorf("bundled phebs shutdown: %w", err)
		}
		return true, nil
	case <-timer.C:
		_ = command.Process.Kill()
		<-waited
		return true, errors.New("bundled phebs did not stop within 15s")
	}
}

func smokeError(err error, logs *boundedLog) error {
	return fmt.Errorf("%w\n--- bundled phebs log ---\n%s", err, logs.String())
}

type boundedLog struct {
	mu        sync.Mutex
	data      []byte
	limit     int
	truncated bool
}

func (log *boundedLog) Write(data []byte) (int, error) {
	log.mu.Lock()
	defer log.mu.Unlock()
	accepted := len(data)
	remaining := log.limit - len(log.data)
	if remaining > 0 {
		if len(data) > remaining {
			log.data = append(log.data, data[:remaining]...)
			log.truncated = true
		} else {
			log.data = append(log.data, data...)
		}
	} else {
		log.truncated = true
	}
	return accepted, nil
}

func (log *boundedLog) String() string {
	log.mu.Lock()
	defer log.mu.Unlock()
	value := string(log.data)
	if log.truncated {
		value += "\n[log truncated]"
	}
	return value
}
