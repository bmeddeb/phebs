//go:build linux && phebs_t42_native && phebs_t42_pressure_restore_native

package t421

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/bmeddeb/phebs/internal/lifecycle"
	"golang.org/x/sys/unix"
)

const (
	pressureRestoreAdminEmail    = "med498-driver@example.invalid"
	pressureRestoreAdminPassword = "med498-driver-password"
	pressureRestoreNeedle        = "phebsMed498Needle"
	pressureRestoreCorpusFiles   = 300
	pressureRestoreHTTPLimit     = 4 << 20
	pressureRestoreWait          = 3 * time.Minute
	pressureRestoreRefuseHold    = 10 * time.Second
)

type pressureRestorePhaseResult struct {
	Name           string             `json:"name"`
	BallastBytes   uint64             `json:"ballast_bytes"`
	StatfsPercent  int                `json:"statfs_used_percent"`
	Pressure       lifecycle.Pressure `json:"product_pressure"`
	ProductPercent int                `json:"product_used_percent"`
	IndexedCommit  string             `json:"indexed_commit"`
	PushedCommit   string             `json:"pushed_commit"`
	ElapsedSeconds float64            `json:"elapsed_seconds"`
}

type pressureRestoreDriverResult struct {
	Version           string                       `json:"version"`
	Repository        string                       `json:"repository"`
	FilesystemBytes   uint64                       `json:"filesystem_bytes"`
	BaselinePercent   int                          `json:"baseline_used_percent"`
	Commits           []string                     `json:"commits"`
	Phases            []pressureRestorePhaseResult `json:"phases"`
	BackupOutput      string                       `json:"backup_output"`
	RestoreOutput     string                       `json:"restore_output"`
	RestoredMatches   int                          `json:"restored_search_matches"`
	RestoredSuccessor string                       `json:"restored_successor_commit"`
	LeafEmpty         bool                         `json:"leaf_empty"`
	ElapsedSeconds    float64                      `json:"elapsed_seconds"`
}

type pressureRestoreServer struct {
	command *exec.Cmd
	waited  chan error
	log     *pressureRestoreLog
	base    string
	client  *http.Client
}

type pressureRestoreDriver struct {
	selection   pressureRestoreSelection
	work, home  string
	origin      string
	config      string
	repository  string
	env         []string
	ballast     *os.File
	ballastSize uint64
	commits     []string
	result      pressureRestoreDriverResult
	server      *pressureRestoreServer
}

func pressureRestoreDriverf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "MED498 driver %s "+format+"\n", append([]any{time.Now().UTC().Format(time.RFC3339)}, args...)...)
}

func runPressureRestoreDriver(ctx context.Context, selection pressureRestoreSelection) (retErr error) {
	started := time.Now()
	uid, gid := uint32(os.Getuid()), uint32(os.Getgid())
	if uid == 0 || uid != selection.Request.UID || uint32(os.Geteuid()) != uid || gid != selection.Request.GID || uint32(os.Getegid()) != gid {
		return errors.New("driver must run with exactly the borrowing credentials")
	}
	var cwd unix.Stat_t
	var filesystem unix.Statfs_t
	if unix.Stat(".", &cwd) != nil || cwd.Dev != selection.LeafDevice || cwd.Ino != selection.LeafInode ||
		unix.Statfs(".", &filesystem) != nil || filesystem.Type != unix.EXT4_SUPER_MAGIC {
		return errors.New("driver cwd is not the borrowed ext4 leaf")
	}
	work, err := os.Getwd()
	if err != nil {
		return err
	}
	home := os.Getenv("HOME")
	if !filepath.IsAbs(home) || inputTreePathsOverlap(home, work) {
		return errors.New("driver home must sit outside the leaf")
	}
	request := selection.Request
	d := &pressureRestoreDriver{selection: selection, work: work, home: home, origin: filepath.Join(home, "origin"), config: filepath.Join(home, "phebs.yaml")}
	d.result.Version = selection.Version
	d.env = []string{"PATH=/usr/bin:/bin", "LANG=C", "HOME=" + home, "TMPDIR=" + os.Getenv("TMPDIR"), "GIT_CONFIG_NOSYSTEM=1",
		"PHEBS_ZOEKT_GIT_INDEX=" + filepath.Join(request.Bundle, "bin", "zoekt-git-index"),
		"PHEBS_FOCUSED_INDEX=" + filepath.Join(request.Bundle, "bin", "phebs-focused-index"),
		"PHEBS_BUF=" + filepath.Join(request.Bundle, "bin", "buf"),
		"PHEBS_SURREAL=" + request.Surreal}
	defer func() {
		if d.server != nil {
			retErr = errors.Join(retErr, d.stop(), errors.New("server still running at driver exit"))
		}
		if d.ballast != nil {
			retErr = errors.Join(retErr, d.ballast.Close())
		}
	}()
	if err := d.run(ctx); err != nil {
		return err
	}
	d.result.ElapsedSeconds = time.Since(started).Seconds()
	encoded, err := json.Marshal(d.result)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "%s%s\n", pressureRestoreResultPrefix, encoded)
	return nil
}

func (d *pressureRestoreDriver) run(ctx context.Context) error {
	if err := d.createOrigin(ctx); err != nil {
		return fmt.Errorf("origin: %w", err)
	}
	if err := d.writeConfig(); err != nil {
		return err
	}
	var filesystem unix.Statfs_t
	if err := unix.Statfs(d.work, &filesystem); err != nil {
		return err
	}
	d.result.FilesystemBytes = filesystem.Blocks * uint64(filesystem.Bsize)

	// Baseline: an empty data root on the leaf syncs, indexes and serves.
	if err := d.start(ctx); err != nil {
		return fmt.Errorf("first server: %w", err)
	}
	if err := d.waitIndexed(ctx, d.commits[0]); err != nil {
		return fmt.Errorf("baseline index: %w", err)
	}
	if err := d.requireNeedles(ctx, 1, d.commits[0]); err != nil {
		return fmt.Errorf("baseline search: %w", err)
	}
	_, baseline, err := d.statfs()
	if err != nil {
		return err
	}
	d.result.BaselinePercent = baseline
	pressureRestoreDriverf("baseline indexed %s at %d%% used", d.commits[0], d.result.BaselinePercent)
	ballast, err := os.OpenFile(filepath.Join(d.work, "ballast"), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	d.ballast = ballast
	bands := pressureRestoreBands()
	if !validPressureRestoreBands(bands) {
		return errors.New("fixed pressure bands cross a watermark")
	}

	// 80% soft watermark: collection posture, new work is still admitted.
	if err := d.collect(ctx, bands[0]); err != nil {
		return fmt.Errorf("collect: %w", err)
	}
	// 90% hard watermark: admission refuses and latches; reads keep serving.
	if err := d.refuse(ctx, bands[1]); err != nil {
		return fmt.Errorf("refuse: %w", err)
	}
	// Between 75% and 80%: the latch still refuses.
	if err := d.latched(ctx, bands[2]); err != nil {
		return fmt.Errorf("latched: %w", err)
	}
	// Below 75%: admission resumes and the deferred head is indexed.
	if err := d.resumed(ctx, bands[3]); err != nil {
		return fmt.Errorf("resumed: %w", err)
	}
	if err := d.ballast.Close(); err != nil {
		return err
	}
	d.ballast = nil
	if err := os.Remove(filepath.Join(d.work, "ballast")); err != nil {
		return err
	}
	return d.backupRestore(ctx)
}

func (d *pressureRestoreDriver) git(ctx context.Context, args ...string) (string, error) {
	full := append([]string{"-C", d.origin, "-c", "user.name=MED-498 driver", "-c", "user.email=" + pressureRestoreAdminEmail, "-c", "commit.gpgsign=false"}, args...)
	command := exec.CommandContext(ctx, d.selection.Request.Git, full...)
	command.Env = d.env
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, bytes.TrimSpace(output))
	}
	return strings.TrimSpace(string(output)), nil
}

func (d *pressureRestoreDriver) createOrigin(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Join(d.origin, "corpus"), 0o700); err != nil {
		return err
	}
	if _, err := d.git(ctx, "init", "--initial-branch=main"); err != nil {
		return err
	}
	filler := strings.Repeat("\t// MED-498 pressure corpus filler line for a realistic index payload.\n", 40)
	for index := range pressureRestoreCorpusFiles {
		source := fmt.Sprintf("package corpus\n\n// File %03d.\nfunc Corpus%03d() int {\n%s\treturn %d\n}\n", index, index, filler, index)
		if err := os.WriteFile(filepath.Join(d.origin, "corpus", fmt.Sprintf("f%03d.go", index)), []byte(source), 0o600); err != nil {
			return err
		}
	}
	_, err := d.commit(ctx)
	if err != nil {
		return err
	}
	name := strings.Trim(filepath.ToSlash(filepath.Clean(d.origin)), "/")
	d.repository = "local/" + name
	d.result.Repository = d.repository
	return nil
}

// commit adds the next needle file and returns the new head.
func (d *pressureRestoreDriver) commit(ctx context.Context) (string, error) {
	index := len(d.commits)
	if err := os.MkdirAll(filepath.Join(d.origin, "pressure"), 0o700); err != nil {
		return "", err
	}
	source := fmt.Sprintf("package pressure\n\nconst %s%d = %d\n", pressureRestoreNeedle, index, index)
	if err := os.WriteFile(filepath.Join(d.origin, "pressure", fmt.Sprintf("needle_%d.go", index)), []byte(source), 0o600); err != nil {
		return "", err
	}
	if _, err := d.git(ctx, "add", "-A"); err != nil {
		return "", err
	}
	if _, err := d.git(ctx, "commit", "-q", "-m", fmt.Sprintf("MED-498 needle %d", index)); err != nil {
		return "", err
	}
	head, err := d.git(ctx, "rev-parse", "HEAD")
	if err != nil || len(head) != 40 {
		return "", fmt.Errorf("resolve head %q: %w", head, err)
	}
	d.commits = append(d.commits, head)
	d.result.Commits = append(d.result.Commits, head)
	return head, nil
}

func (d *pressureRestoreDriver) writeConfig() error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		return err
	}
	type connection struct {
		Name  string `json:"name"`
		Type  string `json:"type"`
		URL   string `json:"url"`
		Watch bool   `json:"watch"` // local origins re-sync only through HEAD watching
	}
	config := map[string]any{
		"server": map[string]any{"addr": addr, "data_dir": filepath.Join(d.work, "data")},
		"auth": map[string]any{"cookie_secure": false,
			"bootstrap_user": map[string]string{"email": pressureRestoreAdminEmail, "password": pressureRestoreAdminPassword}},
		"sync":        map[string]string{"poll_interval": "100ms", "resync_interval": "0"},
		"connections": []connection{{Name: "med498-pressure", Type: "git", URL: d.origin, Watch: true}},
	}
	raw, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(d.config, append(raw, '\n'), 0o600)
}

func (d *pressureRestoreDriver) phebs(ctx context.Context, args ...string) (string, error) {
	command := exec.CommandContext(ctx, filepath.Join(d.selection.Request.Bundle, "phebs"), args...)
	command.Dir, command.Env = d.home, d.env
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("phebs %s: %w: %s %s", args[0], err, bytes.TrimSpace(stdout.Bytes()), bytes.TrimSpace(stderr.Bytes()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

func (d *pressureRestoreDriver) start(ctx context.Context) error {
	if d.server != nil {
		return errors.New("server already running")
	}
	log := &pressureRestoreLog{}
	command := exec.Command(filepath.Join(d.selection.Request.Bundle, "phebs"), "serve", "-config", d.config)
	command.Dir, command.Env = d.home, d.env
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	command.Stdout, command.Stderr = log, log
	if err := command.Start(); err != nil {
		return err
	}
	server := &pressureRestoreServer{command: command, waited: make(chan error, 1), log: log}
	go func() { server.waited <- command.Wait() }()
	d.server = server
	raw, err := os.ReadFile(d.config)
	if err != nil {
		return err
	}
	var config struct {
		Server struct {
			Addr string `json:"addr"`
		} `json:"server"`
	}
	if json.Unmarshal(raw, &config) != nil {
		return errors.New("config address")
	}
	server.base = "http://" + config.Server.Addr
	jar, err := cookiejar.New(nil)
	if err != nil {
		return err
	}
	server.client = &http.Client{Jar: jar, Timeout: 15 * time.Second}
	deadline := time.Now().Add(pressureRestoreWait)
	for {
		status, _, err := d.request(ctx, http.MethodGet, "/api/health", nil)
		if err == nil && status == http.StatusOK {
			break
		}
		select {
		case waitErr := <-server.waited:
			d.server = nil
			return fmt.Errorf("server exited before health: %v\n%s", waitErr, log.String())
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("server health timeout\n%s", log.String())
		}
	}
	body, _ := json.Marshal(map[string]string{"email": pressureRestoreAdminEmail, "password": pressureRestoreAdminPassword})
	status, response, err := d.request(ctx, http.MethodPost, "/api/auth/login", body)
	if err != nil || status != http.StatusOK {
		return fmt.Errorf("login %d: %v %s", status, err, response)
	}
	status, response, err = d.request(ctx, http.MethodGet, "/api/version", nil)
	var version struct {
		Version string `json:"version"`
	}
	if err != nil || status != http.StatusOK || json.Unmarshal(response, &version) != nil || version.Version != d.selection.Version {
		return fmt.Errorf("running version %q does not match bundle %q", version.Version, d.selection.Version)
	}
	return nil
}

// stop interrupts the server and requires a clean exit and a drained UID.
func (d *pressureRestoreDriver) stop() error {
	server := d.server
	if server == nil {
		return nil
	}
	d.server = nil
	if err := server.command.Process.Signal(os.Interrupt); err != nil {
		return err
	}
	select {
	case err := <-server.waited:
		if err != nil {
			return fmt.Errorf("server shutdown: %w\n%s", err, server.log.String())
		}
	case <-time.After(time.Minute):
		_ = syscall.Kill(-server.command.Process.Pid, syscall.SIGKILL)
		<-server.waited
		return fmt.Errorf("server did not stop within a minute\n%s", server.log.String())
	}
	return pressureRestoreDrained(d.selection.Request.UID)
}

func (d *pressureRestoreDriver) request(ctx context.Context, method, path string, body []byte) (int, []byte, error) {
	if d.server == nil {
		return 0, nil, errors.New("no server")
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, d.server.base+path, reader)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := d.server.client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, pressureRestoreHTTPLimit+1))
	if err != nil || len(data) > pressureRestoreHTTPLimit {
		return 0, nil, errors.New("bounded response")
	}
	return response.StatusCode, data, nil
}

type pressureRestoreRepoRow struct {
	Name          string `json:"name"`
	IndexedCommit string `json:"indexed_commit_hash"`
	LatestStatus  string `json:"latest_indexing_job_status"`
	LastIndexJob  *struct {
		Status string `json:"status"`
		Error  string `json:"error"`
	} `json:"last_index_job"`
}

func (d *pressureRestoreDriver) repoRow(ctx context.Context) (pressureRestoreRepoRow, error) {
	status, body, err := d.request(ctx, http.MethodGet, "/api/repo-status", nil)
	if err != nil || status != http.StatusOK {
		return pressureRestoreRepoRow{}, fmt.Errorf("repo-status %d: %v", status, err)
	}
	var rows []pressureRestoreRepoRow
	if err := json.Unmarshal(body, &rows); err != nil {
		return pressureRestoreRepoRow{}, err
	}
	for _, row := range rows {
		if row.Name == d.repository {
			return row, nil
		}
	}
	return pressureRestoreRepoRow{}, nil
}

func (d *pressureRestoreDriver) waitIndexed(ctx context.Context, commit string) error {
	deadline := time.Now().Add(pressureRestoreWait)
	var row pressureRestoreRepoRow
	for time.Now().Before(deadline) {
		var err error
		row, err = d.repoRow(ctx)
		if err != nil {
			return err
		}
		if row.IndexedCommit == commit && row.LatestStatus == "done" {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	detail := ""
	if row.LastIndexJob != nil {
		detail = row.LastIndexJob.Status + ": " + row.LastIndexJob.Error
	}
	return fmt.Errorf("index of %s not reached (indexed %q, latest %q, last job %s)\n%s", commit, row.IndexedCommit, row.LatestStatus, detail, d.server.log.String())
}

type pressureRestoreHit struct {
	Repo string `json:"repo"`
	Path string `json:"path"`
	Ref  string `json:"ref"`
}

func (d *pressureRestoreDriver) search(ctx context.Context, needle int) ([]pressureRestoreHit, error) {
	query := url.Values{"q": {fmt.Sprintf("%s%d", pressureRestoreNeedle, needle)}, "max_matches": {"5"}}
	status, body, err := d.request(ctx, http.MethodGet, "/api/search?"+query.Encode(), nil)
	if err != nil || status != http.StatusOK {
		return nil, fmt.Errorf("search %d: %v %s", status, err, body)
	}
	var result struct {
		Files []pressureRestoreHit `json:"files"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	return result.Files, nil
}

// requireNeedles requires needles [0, count) to be served exactly at ref.
func (d *pressureRestoreDriver) requireNeedles(ctx context.Context, count int, ref string) error {
	for needle := range count {
		hits, err := d.search(ctx, needle)
		if err != nil {
			return err
		}
		want := pressureRestoreHit{Repo: d.repository, Path: fmt.Sprintf("pressure/needle_%d.go", needle), Ref: ref}
		if len(hits) != 1 || hits[0] != want {
			return fmt.Errorf("needle %d served %+v, want %+v", needle, hits, want)
		}
	}
	return nil
}

func (d *pressureRestoreDriver) requireAbsent(ctx context.Context, needle int) error {
	hits, err := d.search(ctx, needle)
	if err != nil {
		return err
	}
	if len(hits) != 0 {
		return fmt.Errorf("needle %d unexpectedly served %+v", needle, hits)
	}
	return nil
}

func (d *pressureRestoreDriver) statfs() (uint64, int, error) {
	var filesystem unix.Statfs_t
	if err := unix.Statfs(d.work, &filesystem); err != nil {
		return 0, 0, err
	}
	total, available := filesystem.Blocks*uint64(filesystem.Bsize), filesystem.Bavail*uint64(filesystem.Bsize)
	return total, pressureRestorePercent(total-available, total), nil
}

// setBallast resizes the zero-created ballast until the observed used percent
// sits inside band. Concurrent product writes can move usage, so it re-observes
// after every mutation; at most four mutations.
func (d *pressureRestoreDriver) setBallast(ctx context.Context, band pressureRestoreBand) (int, error) {
	for range 4 {
		var filesystem unix.Statfs_t
		if err := unix.Fstatfs(int(d.ballast.Fd()), &filesystem); err != nil {
			return 0, err
		}
		total, available := filesystem.Blocks*uint64(filesystem.Bsize), filesystem.Bavail*uint64(filesystem.Bsize)
		size, err := pressureRestoreBallastSize(total, available, d.ballastSize, band.Target)
		if err != nil {
			return 0, err
		}
		if size != d.ballastSize {
			if err := resizeExecutionPressureBallast(ctx, d.ballast, d.ballastSize, size); err != nil {
				return 0, fmt.Errorf("ballast %d -> %d: %w", d.ballastSize, size, err)
			}
			d.ballastSize = size
		}
		_, percent, err := d.statfs()
		if err != nil {
			return 0, err
		}
		if band.contains(percent) {
			return percent, nil
		}
	}
	_, percent, _ := d.statfs()
	return percent, fmt.Errorf("ballast could not hold %s band [%d,%d]: %d%%", band.Name, band.Low, band.High, percent)
}

type pressureRestoreCapacity struct {
	Pressure    lifecycle.Pressure `json:"pressure"`
	UsedPercent *int               `json:"used_percent"`
	ObservedAt  *time.Time         `json:"observed_at"`
}

func (d *pressureRestoreDriver) capacity(ctx context.Context) (pressureRestoreCapacity, error) {
	status, body, err := d.request(ctx, http.MethodGet, "/api/lifecycle-status", nil)
	if err != nil || status != http.StatusOK {
		return pressureRestoreCapacity{}, fmt.Errorf("lifecycle-status %d: %v %s", status, err, body)
	}
	var value struct {
		Schema   string                  `json:"schema"`
		Capacity pressureRestoreCapacity `json:"capacity"`
	}
	if err := json.Unmarshal(body, &value); err != nil || value.Schema != lifecycle.StatusSchema {
		return pressureRestoreCapacity{}, errors.New("lifecycle status schema")
	}
	return value.Capacity, nil
}

// waitPressure waits for a product capacity observation made after since that
// reports want with a used percent accepted by valid.
func (d *pressureRestoreDriver) waitPressure(ctx context.Context, since time.Time, want lifecycle.Pressure, valid func(int) bool) (pressureRestoreCapacity, error) {
	deadline := time.Now().Add(pressureRestoreWait)
	var last pressureRestoreCapacity
	for time.Now().Before(deadline) {
		value, err := d.capacity(ctx)
		if err != nil {
			return value, err
		}
		last = value
		if value.ObservedAt != nil && value.ObservedAt.After(since) && value.UsedPercent != nil {
			if value.Pressure == want && valid(*value.UsedPercent) {
				return value, nil
			}
			if value.Pressure != want && value.Pressure != lifecycle.PressureUnavailable {
				return value, fmt.Errorf("product reported %s at %d%%, want %s", value.Pressure, *value.UsedPercent, want)
			}
		}
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return last, fmt.Errorf("no %s observation after %s (last %+v)", want, since.Format(time.RFC3339Nano), last)
}

func (d *pressureRestoreDriver) recordPhase(name string, started time.Time, statfsPercent int, capacity pressureRestoreCapacity, pushed string) error {
	row, err := d.repoRow(context.Background())
	if err != nil {
		return err
	}
	phase := pressureRestorePhaseResult{Name: name, BallastBytes: d.ballastSize, StatfsPercent: statfsPercent, Pressure: capacity.Pressure,
		IndexedCommit: row.IndexedCommit, PushedCommit: pushed, ElapsedSeconds: time.Since(started).Seconds()}
	if capacity.UsedPercent != nil {
		phase.ProductPercent = *capacity.UsedPercent
	}
	d.result.Phases = append(d.result.Phases, phase)
	pressureRestoreDriverf("phase %s: statfs=%d%% product=%s/%d%% indexed=%s pushed=%s ballast=%d", name, statfsPercent, phase.Pressure, phase.ProductPercent, row.IndexedCommit, pushed, d.ballastSize)
	return nil
}

func (d *pressureRestoreDriver) collect(ctx context.Context, band pressureRestoreBand) error {
	started := time.Now()
	percent, err := d.setBallast(ctx, band)
	if err != nil {
		return err
	}
	since := time.Now()
	head, err := d.commit(ctx)
	if err != nil {
		return err
	}
	if err := d.waitIndexed(ctx, head); err != nil {
		return err
	}
	if err := d.requireNeedles(ctx, len(d.commits), head); err != nil {
		return err
	}
	capacity, err := d.waitPressure(ctx, since, lifecycle.PressureCollect, func(used int) bool {
		return used >= lifecycle.SoftWatermarkPercent && used < lifecycle.HardWatermarkPercent
	})
	if err != nil {
		return err
	}
	return d.recordPhase(band.Name, started, percent, capacity, head)
}

func (d *pressureRestoreDriver) refuse(ctx context.Context, band pressureRestoreBand) error {
	started := time.Now()
	admitted := d.commits[len(d.commits)-1]
	percent, err := d.setBallast(ctx, band)
	if err != nil {
		return err
	}
	since := time.Now()
	head, err := d.commit(ctx)
	if err != nil {
		return err
	}
	capacity, err := d.waitPressure(ctx, since, lifecycle.PressureRefuse, func(used int) bool { return used >= lifecycle.HardWatermarkPercent })
	if err != nil {
		return err
	}
	if err := d.holdRefused(ctx, admitted, len(d.commits)-1); err != nil {
		return err
	}
	return d.recordPhase(band.Name, started, percent, capacity, head)
}

// holdRefused requires the admitted head to keep serving, unadmitted needles
// from absent onward to stay absent, and the indexed commit not to advance.
func (d *pressureRestoreDriver) holdRefused(ctx context.Context, admitted string, absent int) error {
	deadline := time.Now().Add(pressureRestoreRefuseHold)
	for {
		row, err := d.repoRow(ctx)
		if err != nil {
			return err
		}
		if row.IndexedCommit != admitted {
			return fmt.Errorf("index advanced to %s under refusal", row.IndexedCommit)
		}
		if time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	if err := d.requireNeedles(ctx, absent, admitted); err != nil {
		return fmt.Errorf("reads under refusal: %w", err)
	}
	for needle := absent; needle < len(d.commits); needle++ {
		if err := d.requireAbsent(ctx, needle); err != nil {
			return err
		}
	}
	params := url.Values{"repo": {d.repository}, "path": {"pressure/needle_0.go"}, "ref": {admitted}}
	status, body, err := d.request(ctx, http.MethodGet, "/api/source?"+params.Encode(), nil)
	if err != nil || status != http.StatusOK || !bytes.Contains(body, []byte(pressureRestoreNeedle+"0")) {
		return fmt.Errorf("pinned source browse under refusal %d: %v", status, err)
	}
	return nil
}

func (d *pressureRestoreDriver) latched(ctx context.Context, band pressureRestoreBand) error {
	started := time.Now()
	row, err := d.repoRow(ctx)
	if err != nil {
		return err
	}
	admitted := row.IndexedCommit
	absent := slices.Index(d.commits, admitted) + 1
	if absent == 0 {
		return errors.New("admitted head is not a driver commit")
	}
	percent, err := d.setBallast(ctx, band)
	if err != nil {
		return err
	}
	since := time.Now()
	head, err := d.commit(ctx) // a fresh admission attempt below the hard watermark
	if err != nil {
		return err
	}
	capacity, err := d.waitPressure(ctx, since, lifecycle.PressureRefuse, func(used int) bool {
		return used >= lifecycle.ResumeWatermarkPercent && used < lifecycle.HardWatermarkPercent
	})
	if err != nil {
		return err
	}
	if err := d.holdRefused(ctx, admitted, absent); err != nil {
		return err
	}
	return d.recordPhase(band.Name, started, percent, capacity, head)
}

func (d *pressureRestoreDriver) resumed(ctx context.Context, band pressureRestoreBand) error {
	started := time.Now()
	percent, err := d.setBallast(ctx, band)
	if err != nil {
		return err
	}
	since := time.Now()
	head, err := d.commit(ctx) // a new head always gets a new admission attempt
	if err != nil {
		return err
	}
	if err := d.waitIndexed(ctx, head); err != nil {
		return err
	}
	if err := d.requireNeedles(ctx, len(d.commits), head); err != nil {
		return err
	}
	capacity, err := d.waitPressure(ctx, since, lifecycle.PressureNormal, func(used int) bool { return used < lifecycle.SoftWatermarkPercent })
	if err != nil {
		return err
	}
	return d.recordPhase(band.Name, started, percent, capacity, head)
}

func (d *pressureRestoreDriver) backupRestore(ctx context.Context) error {
	head := d.commits[len(d.commits)-1]
	before := make([][]pressureRestoreHit, len(d.commits))
	for needle := range d.commits {
		hits, err := d.search(ctx, needle)
		if err != nil {
			return err
		}
		before[needle] = hits
	}
	backup := filepath.Join(d.work, "backup")
	output, err := d.phebs(ctx, "backup", "-config", d.config, "-output", backup)
	if err != nil || !strings.HasPrefix(output, "backup published: "+backup) {
		return fmt.Errorf("live backup: %v %s", err, output)
	}
	d.result.BackupOutput = output
	pressureRestoreDriverf("%s", output)
	if err := d.stop(); err != nil {
		return fmt.Errorf("stop before restore: %w", err)
	}
	data, original := filepath.Join(d.work, "data"), filepath.Join(d.work, "data-original")
	if err := os.Rename(data, original); err != nil {
		return err
	}
	output, err = d.phebs(ctx, "restore", "-config", d.config, "-backup", backup)
	if err != nil || !strings.HasPrefix(output, "restore verified and imported: ") {
		return fmt.Errorf("restore: %v %s", err, output)
	}
	d.result.RestoreOutput = output
	pressureRestoreDriverf("%s", output)
	if err := d.start(ctx); err != nil {
		return fmt.Errorf("restored server: %w", err)
	}
	if err := d.waitIndexed(ctx, head); err != nil {
		return fmt.Errorf("restored index: %w", err)
	}
	for needle := range d.commits {
		hits, err := d.search(ctx, needle)
		if err != nil {
			return err
		}
		if !slices.Equal(hits, before[needle]) {
			return fmt.Errorf("restored needle %d served %+v, before backup %+v", needle, hits, before[needle])
		}
		d.result.RestoredMatches += len(hits)
	}
	// The restored successor must admit and serve new work.
	since := time.Now()
	successor, err := d.commit(ctx)
	if err != nil {
		return err
	}
	if err := d.waitIndexed(ctx, successor); err != nil {
		return fmt.Errorf("restored successor index: %w", err)
	}
	if err := d.requireNeedles(ctx, len(d.commits), successor); err != nil {
		return err
	}
	if _, err := d.waitPressure(ctx, since, lifecycle.PressureNormal, func(used int) bool { return used < lifecycle.SoftWatermarkPercent }); err != nil {
		return fmt.Errorf("restored admission: %w", err)
	}
	d.result.RestoredSuccessor = successor
	if err := d.stop(); err != nil {
		return fmt.Errorf("stop restored server: %w", err)
	}
	for _, path := range []string{data, original, backup} {
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(d.work)
	if err != nil || len(entries) != 0 {
		return fmt.Errorf("leaf not empty after teardown: %d entries %v", len(entries), err)
	}
	d.result.LeafEmpty = true
	return nil
}
