// Package sandbox owns one closed, disposable Linux container for the T45.1a
// neutral feasibility spike. It is not a production provider or a Docker client.
package sandbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"
)

const (
	MemoryBytes       = 3 << 30
	ScratchBytes      = 2032 << 20
	ScratchInodes     = 65536
	SharedMemoryBytes = 16 << 20
	TaskLimit         = 256
	DescriptorLimit   = 128
	OutputBytes       = 16 << 20
	WallLimit         = 5 * time.Minute
	apiVersion        = "/v1.47"
	maxResponseBytes  = 1 << 20
	maxWireBytes      = 24 << 20
	ownerLabel        = "phebs.t451a.owner"
)

var (
	ErrRefused   = errors.New("T45.1a container boundary unavailable or changed")
	ErrExecution = errors.New("T45.1a container execution refused")
	ErrCustody   = errors.New("T45.1a container cleanup is unproven; private custody retained")
)

// StageError carries only a closed local stage, never daemon or repository text.
type StageError struct {
	Stage string
	Cause error
}

func (e *StageError) Error() string { return "T45.1a " + e.Stage + ": " + e.Cause.Error() }
func (e *StageError) Unwrap() error { return e.Cause }

type Options struct{ Socket, ImageID, Inputs string }

// Resources distinguish kernel-enforced ceilings from sampled observations.
// RSS sums may count shared pages more than once; no complete RSS/FD history is claimed.
type Resources struct {
	MemoryOOMEvents            uint64 `json:"memory_oom_events"`
	MemoryOOMKills             uint64 `json:"memory_oom_kills"`
	MemoryLimitEvents          uint64 `json:"memory_limit_events"`
	TaskLimitEvents            uint64 `json:"task_limit_events"`
	SamplingUnavailable        bool   `json:"sampling_unavailable"`
	SamplingFailureStage       string `json:"sampling_failure_stage,omitempty"`
	LimitsVerified             bool   `json:"limits_verified"`
	MemoryPeakBytes            uint64 `json:"memory_peak_bytes"`
	SampledPeakRSSBytes        uint64 `json:"sampled_peak_rss_bytes"`
	SampledPeakProcesses       uint64 `json:"sampled_peak_processes"`
	SampledPeakScratchBytes    uint64 `json:"sampled_peak_scratch_bytes"`
	SampledPeakScratchInodes   uint64 `json:"sampled_peak_scratch_inodes"`
	Samples                    uint64 `json:"samples"`
	PerProcessDescriptors      uint64 `json:"per_process_descriptors"`
	AggregateDescriptorCeiling uint64 `json:"aggregate_descriptor_ceiling"`
}

type Result struct {
	OOMKilled      bool
	StopReason     string
	Stdout, Stderr []byte
	ContainerID    string
	ExitCode       int
	Removed        bool
	Resources      Resources
}

func readSmall(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	value, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read bounded input: %w", err)
	}
	if int64(len(value)) > limit {
		return nil, ErrRefused
	}
	return value, nil
}

type supervisorReport struct {
	StopReason string    `json:"stop_reason,omitempty"`
	Schema     string    `json:"schema"`
	Stdout     []byte    `json:"stdout"`
	Stderr     []byte    `json:"stderr"`
	ExitCode   int       `json:"exit_code"`
	Complete   bool      `json:"complete"`
	Resources  Resources `json:"resources"`
}

type journal struct {
	Schema, Name, ContainerID, DaemonID, ImageID, Socket, Inputs string
}

type client struct{ http *http.Client }

type config struct {
	Hostname                   string
	Image                      string
	User                       string
	WorkingDir                 string
	Entrypoint, Cmd, Env       []string
	Labels                     map[string]string
	Healthcheck                struct{ Test []string }
	AttachStdout, AttachStderr bool
	Tty, OpenStdin             bool
	Volumes                    map[string]struct{} `json:",omitempty"`
	HostConfig                 hostConfig
}

type hostConfig struct {
	Mounts                                                           []bindMount
	NetworkMode, IpcMode, PidMode, CgroupnsMode, UsernsMode, Runtime string
	ReadonlyRootfs, Privileged, AutoRemove, PublishAllPorts          bool
	Init                                                             bool
	CapDrop, CapAdd, SecurityOpt, GroupAdd                           []string
	Binds                                                            []string
	Tmpfs                                                            map[string]string
	Memory, MemorySwap, NanoCpus, PidsLimit, ShmSize                 int64
	Ulimits                                                          []ulimit
	RestartPolicy                                                    struct {
		Name              string
		MaximumRetryCount int
	}
	LogConfig struct {
		Type   string
		Config map[string]string
	}
}

type ulimit struct {
	Name       string
	Soft, Hard int64
}

type bindMount struct {
	Type, Source, Target string
	ReadOnly             bool
	BindOptions          struct {
		Propagation                    string
		CreateMountpoint, NonRecursive bool
	}
}

type inspection struct {
	ID              string `json:"Id"`
	Image           string
	Name            string
	Config          config
	HostConfig      hostConfig
	AppArmorProfile string
	State           struct {
		Running, Paused, Restarting, OOMKilled, Dead bool
		Pid, ExitCode                                int
	}
	Mounts []struct {
		Type, Source, Destination string
		RW                        bool
	}
}

func environment() []string {
	return []string{"HOME=/scratch/home", "HOSTNAME=phebs-t451a", "LANG=C", "LC_ALL=C", "TZ=UTC",
		"PATH=/inputs/tools/bin:/usr/bin:/bin", "TMPDIR=/scratch/tmp", "TMP=/scratch/tmp", "TEMP=/scratch/tmp",
		"XDG_CACHE_HOME=/scratch/cache", "XDG_CONFIG_HOME=/scratch/home", "XDG_DATA_HOME=/scratch/home",
		"GOENV=off", "GOPROXY=off", "GOSUMDB=off", "GOTELEMETRY=off", "GOTOOLCHAIN=local", "GOWORK=off"}
}

func recipe(options Options, name string) config {
	c := config{Hostname: "phebs-t451a", Image: options.ImageID, User: "0:0", WorkingDir: "/scratch",
		Entrypoint: []string{"/inputs/t451a"}, Cmd: []string{"__supervisor"}, Env: environment(),
		Labels: map[string]string{ownerLabel: name}, AttachStdout: true, AttachStderr: true}
	c.Healthcheck.Test = []string{"NONE"}
	c.HostConfig = hostConfig{NetworkMode: "none", IpcMode: "private", CgroupnsMode: "private", Runtime: "runc",
		ReadonlyRootfs: true, CapDrop: []string{"ALL"}, CapAdd: []string{"SETUID", "SETGID"},
		SecurityOpt: []string{"no-new-privileges", "apparmor=docker-default"},
		Tmpfs:       map[string]string{"/scratch": fmt.Sprintf("rw,exec,nosuid,nodev,size=%d,nr_inodes=%d,mode=1777", ScratchBytes, ScratchInodes)},
		Memory:      MemoryBytes, MemorySwap: MemoryBytes, NanoCpus: 1_000_000_000, PidsLimit: TaskLimit, ShmSize: SharedMemoryBytes,
		Ulimits: []ulimit{{"nofile", DescriptorLimit, DescriptorLimit}, {"core", 0, 0}}}
	c.HostConfig.RestartPolicy.Name = "no"
	mount := bindMount{Type: "bind", Source: options.Inputs, Target: "/inputs", ReadOnly: true}
	mount.BindOptions.Propagation = "rprivate"
	mount.BindOptions.NonRecursive = true
	c.HostConfig.Mounts = []bindMount{mount}
	c.HostConfig.LogConfig.Type = "none"
	c.HostConfig.LogConfig.Config = map[string]string{}
	return c
}

func newClient(options Options) (*client, error) {
	if !filepath.IsAbs(options.Socket) || filepath.Clean(options.Socket) != options.Socket ||
		!filepath.IsAbs(options.Inputs) || filepath.Clean(options.Inputs) != options.Inputs ||
		strings.ContainsAny(options.Inputs, ":\x00\r\n") || !imageID(options.ImageID) {
		return nil, ErrRefused
	}
	info, err := os.Stat(options.Socket)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		return nil, ErrRefused
	}
	canonical, err := filepath.EvalSymlinks(options.Inputs)
	if err != nil || canonical != options.Inputs {
		return nil, ErrRefused
	}
	info, err = os.Lstat(options.Inputs)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o022 != 0 {
		return nil, ErrRefused
	}
	transport := &http.Transport{Proxy: nil, DisableCompression: true, MaxResponseHeaderBytes: 16 << 10,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", options.Socket)
		}}
	return &client{http: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return ErrRefused }}}, nil
}

func imageID(value string) bool {
	if len(value) != 71 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(value[7:])
	return err == nil && value == strings.ToLower(value)
}

func containerID(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}

func (c *client) request(ctx context.Context, method, path string, body any, out any, statuses ...int) (int, error) {
	if !strings.Contains(path, "/wait?") {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, ErrRefused
		}
		reader = strings.NewReader(string(raw))
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://docker"+apiVersion+path, reader)
	if err != nil {
		return 0, ErrRefused
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(req)
	if err != nil {
		return 0, ErrRefused
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(raw) > maxResponseBytes || !slices.Contains(statuses, response.StatusCode) {
		return response.StatusCode, ErrRefused
	}
	if out != nil && response.StatusCode != http.StatusNotFound && json.Unmarshal(raw, out) != nil {
		return response.StatusCode, ErrRefused
	}
	return response.StatusCode, nil
}

func (c *client) preflight(ctx context.Context, options Options) (string, error) {
	var info struct {
		ID, OSType, CgroupVersion                      string
		MemoryLimit, SwapLimit, PidsLimit, CPUCfsQuota bool
		SecurityOptions                                []string
	}
	if _, err := c.request(ctx, "GET", "/info", nil, &info, 200); err != nil {
		return "", err
	}
	seccomp, apparmor := false, false
	for _, value := range info.SecurityOptions {
		seccomp = seccomp || value == "name=seccomp,profile=builtin"
		apparmor = apparmor || value == "name=apparmor"
	}
	if info.ID == "" || info.OSType != "linux" || info.CgroupVersion != "2" || !info.MemoryLimit || !info.SwapLimit || !info.PidsLimit || !info.CPUCfsQuota || !seccomp || !apparmor {
		return "", ErrRefused
	}
	var image struct {
		ID               string `json:"Id"`
		OS, Architecture string
		Config           config
	}
	if _, err := c.request(ctx, "GET", "/images/"+options.ImageID+"/json", nil, &image, 200); err != nil {
		return "", err
	}
	if image.ID != options.ImageID || image.OS != "linux" || image.Architecture != "arm64" || len(image.Config.Volumes) != 0 {
		return "", ErrRefused
	}
	allowed := map[string]bool{}
	for _, value := range environment() {
		key, _, _ := strings.Cut(value, "=")
		allowed[key] = true
	}
	seen := map[string]bool{}
	for _, value := range image.Config.Env {
		key, _, ok := strings.Cut(value, "=")
		if !ok || !allowed[key] || seen[key] {
			return "", ErrRefused
		}
		seen[key] = true
	}
	return info.ID, nil
}

func (c *client) inspect(ctx context.Context, id string) (inspection, int, error) {
	var got inspection
	status, err := c.request(ctx, "GET", "/containers/"+url.PathEscape(id)+"/json", nil, &got, 200, 404)
	return got, status, err
}

func verify(got inspection, options Options, owner journal) error {
	want := recipe(options, owner.Name)
	if !containerID(got.ID) || got.Image != options.ImageID || got.Name != "/"+owner.Name || got.Config.Labels[ownerLabel] != owner.Name ||
		owner.ContainerID != "" && got.ID != owner.ContainerID || got.AppArmorProfile != "docker-default" {
		return ErrRefused
	}
	actual := got.Config
	actual.HostConfig = hostConfig{}
	actual.Env = slices.Clone(actual.Env)
	want.Env = slices.Clone(want.Env)
	slices.Sort(actual.Env)
	slices.Sort(want.Env)
	if len(actual.Volumes) == 0 {
		actual.Volumes = nil
	}
	// Docker may preserve harmless image labels; only the controller label issues ownership.
	actual.Labels = want.Labels
	if !reflect.DeepEqual(actual, wantWithoutHost(want)) {
		return ErrRefused
	}
	actualHost := got.HostConfig
	if len(actualHost.GroupAdd) == 0 {
		actualHost.GroupAdd = nil
	}
	if len(actualHost.LogConfig.Config) == 0 {
		actualHost.LogConfig.Config = map[string]string{}
	}
	if !reflect.DeepEqual(actualHost, want.HostConfig) {
		return ErrRefused
	}
	binds := 0
	for _, mount := range got.Mounts {
		if mount.Type == "bind" && mount.Source == options.Inputs && mount.Destination == "/inputs" && !mount.RW {
			binds++
			continue
		}
		if mount.Type == "tmpfs" && mount.Destination == "/scratch" && mount.RW {
			continue
		}
		return ErrRefused
	}
	if binds != 1 {
		return ErrRefused
	}
	return nil
}

func wantWithoutHost(c config) config { c.HostConfig = hostConfig{}; return c }

// Run never pulls images, builds Dockerfiles, discovers contexts, or accepts a
// caller command. A durable sibling journal survives controller hard death.
func Run(ctx context.Context, options Options) (result Result, retErr error) {
	stage := "configuration"
	defer func() {
		if retErr != nil {
			retErr = &StageError{Stage: stage, Cause: retErr}
		}
	}()
	result.ExitCode = -1
	if ctx == nil {
		return result, ErrRefused
	}
	ctx, cancel := context.WithTimeout(ctx, WallLimit+15*time.Second)
	defer cancel()
	c, err := newClient(options)
	if err != nil {
		return result, err
	}
	defer c.http.CloseIdleConnections()
	stage = "preflight"
	daemon, err := c.preflight(ctx, options)
	if err != nil {
		return result, err
	}
	var token [16]byte
	if _, err = rand.Read(token[:]); err != nil {
		return result, ErrRefused
	}
	owner := journal{Schema: "t451a-container-owner-v1", Name: "phebs-t451a-" + hex.EncodeToString(token[:]), DaemonID: daemon, ImageID: options.ImageID, Socket: options.Socket, Inputs: options.Inputs}
	stage = "journal"
	if err = writeJournal(options, owner, true); err != nil {
		return result, err
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanupCancel()
		removed, id, cleanupErr := c.cleanup(cleanupCtx, options, owner)
		result.Removed = removed
		if result.ContainerID == "" {
			result.ContainerID = id
		}
		retErr = errors.Join(retErr, cleanupErr)
	}()
	var created struct {
		ID       string `json:"Id"`
		Warnings []string
	}
	stage = "create"
	if _, err = c.request(ctx, "POST", "/containers/create?name="+owner.Name, recipe(options, owner.Name), &created, 201); err != nil {
		return result, err
	}
	if !containerID(created.ID) || len(created.Warnings) != 0 {
		return result, ErrRefused
	}
	owner.ContainerID = created.ID
	result.ContainerID = created.ID
	stage = "journal_identity"
	if err = writeJournal(options, owner, false); err != nil {
		return result, err
	}
	stage = "inspect_created"
	got, _, err := c.inspect(ctx, created.ID)
	if err != nil || verify(got, options, owner) != nil || got.State.Running || got.State.Pid != 0 {
		return result, ErrRefused
	}
	stage = "attach"
	stream, err := c.attach(ctx, created.ID)
	if err != nil {
		return result, err
	}
	output := make(chan wireResult, 1)
	outputDone := make(chan struct{})
	go func() { defer close(outputDone); output <- readWire(stream) }()
	defer func() { cancel(); _ = stream.Close(); <-outputDone }()
	stage = "start"
	if _, err = c.request(ctx, "POST", "/containers/"+created.ID+"/start", nil, nil, 204); err != nil {
		return result, err
	}
	var waited struct {
		StatusCode int
		Error      *struct{ Message string }
	}
	stage = "wait"
	if _, err = c.request(ctx, "POST", "/containers/"+created.ID+"/wait?condition=not-running", nil, &waited, 200); err != nil {
		return result, err
	}
	result.ExitCode = waited.StatusCode
	got, _, err = c.inspect(ctx, created.ID)
	if err != nil || verify(got, options, owner) != nil {
		return result, ErrExecution
	}
	result.OOMKilled = got.State.OOMKilled
	stage = "output"
	var wire wireResult
	select {
	case wire = <-output:
	case <-ctx.Done():
		return result, ErrExecution
	}
	if wire.err != nil || waited.Error != nil {
		return result, ErrExecution
	}
	var report supervisorReport
	stage = "supervisor_report"
	if json.Unmarshal(wire.stdout, &report) != nil || len(wire.stderr) != 0 || report.Schema != "t451a-supervisor-v1" ||
		len(report.Stdout)+len(report.Stderr) > OutputBytes {
		return result, ErrExecution
	}
	result.Stdout, result.Stderr, result.Resources = report.Stdout, report.Stderr, report.Resources
	result.StopReason = report.StopReason
	stage = "inspect_stopped"
	got, _, err = c.inspect(ctx, created.ID)
	if err != nil || verify(got, options, owner) != nil || got.State.Running || got.State.Pid != 0 || got.State.OOMKilled ||
		got.State.ExitCode != waited.StatusCode || waited.StatusCode != report.ExitCode || report.ExitCode != 0 || !report.Complete || !report.Resources.LimitsVerified {
		return result, ErrExecution
	}
	return result, nil
}

// Recover only addresses the exact durable owner. A lost create response is
// resolved by its exact random name, never a daemon-wide list or label search.
func Recover(ctx context.Context, options Options) (result Result, err error) {
	result.ExitCode = -1
	if ctx == nil {
		return result, ErrRefused
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	c, err := newClient(options)
	if err != nil {
		return result, err
	}
	defer c.http.CloseIdleConnections()
	owner, err := readJournal(options)
	if err != nil {
		return result, err
	}
	result.Removed, result.ContainerID, err = c.cleanup(ctx, options, owner)
	return result, err
}

func (c *client) cleanup(ctx context.Context, options Options, owner journal) (bool, string, error) {
	var info struct{ ID string }
	if _, err := c.request(ctx, "GET", "/info", nil, &info, 200); err != nil || info.ID != owner.DaemonID {
		return false, owner.ContainerID, ErrCustody
	}
	identity := owner.ContainerID
	if identity == "" {
		identity = owner.Name
	}
	got, status, err := c.inspect(ctx, identity)
	if err != nil {
		return false, owner.ContainerID, ErrCustody
	}
	if status == 404 {
		if removeJournal(options) != nil {
			return false, owner.ContainerID, ErrCustody
		}
		return true, owner.ContainerID, nil
	}
	if verify(got, options, owner) != nil {
		return false, owner.ContainerID, ErrCustody
	}
	id := got.ID
	if got.State.Running {
		if _, err = c.request(ctx, "POST", "/containers/"+id+"/kill?signal=KILL", nil, nil, 204, 409); err != nil {
			return false, id, ErrCustody
		}
		var waited struct{ StatusCode int }
		if _, err = c.request(ctx, "POST", "/containers/"+id+"/wait?condition=not-running", nil, &waited, 200); err != nil {
			return false, id, ErrCustody
		}
	}
	got, _, err = c.inspect(ctx, id)
	if err != nil || got.State.Running || got.State.Pid != 0 || verify(got, options, owner) != nil {
		return false, id, ErrCustody
	}
	if _, err = c.request(ctx, "DELETE", "/containers/"+id+"?v=1", nil, nil, 204); err != nil {
		return false, id, ErrCustody
	}
	if _, status, err = c.inspect(ctx, id); err != nil || status != 404 {
		return false, id, ErrCustody
	}
	if removeJournal(options) != nil {
		return false, id, ErrCustody
	}
	return true, id, nil
}

func journalPath(options Options) string { return options.Inputs + ".t451a-container.json" }

func writeJournal(options Options, owner journal, initial bool) error {
	raw, err := json.Marshal(owner)
	if err != nil || len(raw) > 8192 {
		return ErrCustody
	}
	path := journalPath(options)
	target := path
	if !initial {
		target += ".next"
	}
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return ErrCustody
	}
	_, writeErr := file.Write(raw)
	err = errors.Join(writeErr, file.Sync(), file.Close())
	if err != nil {
		return ErrCustody
	}
	if !initial {
		if os.Rename(target, path) != nil {
			return ErrCustody
		}
	}
	return syncParent(path)
}

func readJournal(options Options) (journal, error) {
	var owner journal
	file, err := os.Open(journalPath(options))
	if err != nil {
		return owner, ErrCustody
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 8192 || info.Mode().Perm() != 0o600 {
		return owner, ErrCustody
	}
	raw, err := io.ReadAll(io.LimitReader(file, 8193))
	if err != nil || json.Unmarshal(raw, &owner) != nil || owner.Schema != "t451a-container-owner-v1" ||
		owner.Socket != options.Socket || owner.Inputs != options.Inputs || owner.ImageID != options.ImageID || owner.DaemonID == "" ||
		!strings.HasPrefix(owner.Name, "phebs-t451a-") || len(owner.Name) != 44 || owner.ContainerID != "" && !containerID(owner.ContainerID) {
		return journal{}, ErrCustody
	}
	if _, err = hex.DecodeString(strings.TrimPrefix(owner.Name, "phebs-t451a-")); err != nil {
		return journal{}, ErrCustody
	}
	return owner, nil
}

func removeJournal(options Options) error {
	if err := os.Remove(journalPath(options)); err != nil {
		return ErrCustody
	}
	return syncParent(journalPath(options))
}

func syncParent(path string) error {
	file, err := os.Open(filepath.Dir(path))
	if err != nil {
		return ErrCustody
	}
	if errors.Join(file.Sync(), file.Close()) != nil {
		return ErrCustody
	}
	return nil
}
