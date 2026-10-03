//go:build linux

package typedexecutor

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/generationscheduler"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/typedbazel/provider"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/bmeddeb/phebs/internal/typedsandbox"
	"github.com/bmeddeb/phebs/internal/typedworkspace"
	"github.com/scip-code/scip/bindings/go/scip"
	"golang.org/x/sys/unix"
	"google.golang.org/protobuf/proto"
)

const acceptanceSocket = "/var/run/docker.sock"

type acceptanceSelected struct {
	Chunk   store.GenerationChunk `json:"chunk"`
	Attempt string                `json:"attempt"`
}
type acceptanceChildResult struct {
	Schema              string  `json:"schema"`
	Config              string  `json:"config"`
	Case                string  `json:"case"`
	Recovery            bool    `json:"recovery"`
	Outcome             Outcome `json:"outcome"`
	ExecutionError      bool    `json:"execution_error"`
	Settled             bool    `json:"settled"`
	NoReplay            bool    `json:"no_replay"`
	PublicationVerified bool    `json:"publication_verified"`
	GrowthReleased      bool    `json:"growth_released"`
	FailureSite         string  `json:"failure_site,omitempty"`
}
type acceptanceObservation struct {
	ID            string                          `json:"id"`
	Phase         string                          `json:"phase"`
	Allowance     typedsandbox.Allowance          `json:"allowance"`
	Seal          string                          `json:"seal"`
	SupervisorPID int                             `json:"supervisor_pid"`
	WorkerPID     int                             `json:"worker_pid"`
	WorkerStart   string                          `json:"worker_start"`
	Scratch       typedsandbox.HostScratchReceipt `json:"scratch"`
	Kernel        acceptanceKernel                `json:"kernel"`
}
type acceptanceFinal struct {
	Schema             string                  `json:"schema"`
	Config             string                  `json:"config"`
	Case               string                  `json:"case"`
	Pass               bool                    `json:"pass"`
	Injected           bool                    `json:"injected"`
	Observations       []acceptanceObservation `json:"observations"`
	Result             acceptanceChildResult   `json:"result"`
	NativeAbsent       bool                    `json:"native_absent"`
	LifecycleTurns     int                     `json:"lifecycle_turns"`
	WorkspaceDrained   bool                    `json:"workspace_drained"`
	RetainedTombstones int                     `json:"retained_tombstones"`
	EngineJoined       bool                    `json:"engine_joined"`
	Error              string                  `json:"error,omitempty"`
	Emergency          string                  `json:"emergency,omitempty"`
}

// Open every component without following links. Only reviewed root-owned regular
// single-link files can supply native authority. Read-only tests below exercise
// this independently of any daemon or privileged native operation.
func acceptanceOpen(name string) (*os.File, error) {
	if !filepath.IsAbs(name) || filepath.Clean(name) != name {
		return nil, errors.New("noncanonical path")
	}
	fd, e := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	parts := strings.Split(strings.TrimPrefix(name, "/"), "/")
	for i, p := range parts {
		flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC
		if i < len(parts)-1 {
			flags |= unix.O_DIRECTORY
		}
		next, e := unix.Openat(fd, p, flags, 0)
		_ = unix.Close(fd)
		if e != nil {
			return nil, e
		}
		fd = next
		var st unix.Stat_t
		if e = unix.Fstat(fd, &st); e != nil || (st.Uid != uint32(os.Geteuid()) && (i == len(parts)-1 || st.Uid != 0)) || (st.Mode&0022 != 0 && (i >= len(parts)-1 || st.Mode&unix.S_ISVTX == 0)) {
			_ = unix.Close(fd)
			return nil, errors.New("unsafe file ownership")
		}
		if i == len(parts)-1 && (st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1) {
			_ = unix.Close(fd)
			return nil, errors.New("unsafe file kind")
		}
	}
	return os.NewFile(uintptr(fd), name), nil
}
func acceptanceRead(name string, limit int64) ([]byte, error) {
	f, e := acceptanceOpen(name)
	if e != nil {
		return nil, e
	}
	defer func() { _ = f.Close() }()
	b, e := io.ReadAll(io.LimitReader(f, limit+1))
	if e != nil || int64(len(b)) > limit {
		return nil, errors.New("file bound")
	}
	return b, nil
}
func acceptanceFileHash(name string, limit int64) (string, error) {
	f, e := acceptanceOpen(name)
	if e != nil {
		return "", e
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, e := io.Copy(h, io.LimitReader(f, limit+1))
	if e != nil || n > limit {
		return "", errors.New("hash bound")
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// The system formatter may be the standard mkfs.ext4 -> mke2fs alias. Resolve
// only that fixed system path; staged inputs still forbid aliases in every component.
func acceptanceFormatterHash() (string, error) {
	name, err := filepath.EvalSymlinks(typedsandbox.HostMkfsPath)
	if err != nil {
		return "", err
	}
	return acceptanceFileHash(name, 32<<20)
}

func TestNativeAcceptanceFormatterAlias(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("fixed system formatter ownership requires root")
	}
	raw, err := acceptanceSystemRead(typedsandbox.HostMkfsPath, 32<<20)
	if errors.Is(err, os.ErrNotExist) {
		t.Skip("fixed system formatter is not installed")
	}
	if err != nil {
		t.Fatal(err)
	}
	got, err := acceptanceFormatterHash()
	if err != nil || got != acceptanceDigest(raw) {
		t.Fatalf("formatter identity: digest=%s err=%v", got, err)
	}
}

func acceptanceVerify(ctx context.Context, c nativeAcceptanceConfig, configPath string) (typedindex.Profile, []byte, error) {
	var p typedindex.Profile
	if configPath != filepath.Join(c.root(), "config.json") {
		return p, nil, errors.New("config path")
	}
	exe, e := os.Executable()
	if e != nil || exe != filepath.Join(c.root(), "native-acceptance.test") {
		return p, nil, errors.New("test executable location")
	}
	for _, x := range []struct{ path, digest string }{{exe, c.TestSHA256}, {filepath.Join(c.root(), "surreal"), c.EngineSHA256}} {
		h, e := acceptanceFileHash(x.path, typedindex.MaxFileBytes)
		if e != nil || h != x.digest {
			return p, nil, errors.New("executable identity")
		}
	}
	formatter, e := acceptanceFormatterHash()
	if e != nil || formatter != c.MkfsSHA256 {
		return p, nil, errors.New("formatter identity")
	}
	raw, e := acceptanceRead(filepath.Join(c.root(), "inventory.json"), typedindex.MaxInventoryBytes)
	if e != nil {
		return p, nil, e
	}
	inv, e := typedindex.DecodeInventory(ctx, raw, c.InventorySHA256)
	if e != nil {
		return p, nil, e
	}
	required := map[string]bool{"source/MODULE.bazel": false, "source/MODULE.bazel.lock": false, "source/go.mod": false, "source/lib/BUILD.bazel": false, "source/lib/lib.go": false, typedindex.ManagedHelperFile: false, provider.SelectionFile: false, typedindex.HostToolsFile: false}
	for _, f := range inv.Files() {
		if e := ctx.Err(); e != nil {
			return p, nil, e
		}
		if strings.HasPrefix(f.Path, "source/") {
			if _, ok := required[f.Path]; !ok {
				return p, nil, errors.New("non-neutral source member")
			}
		}
		h, e := acceptanceFileHash(filepath.Join(c.root(), "bundle", f.Path), f.Bytes)
		if e != nil || h != f.Digest {
			return p, nil, errors.New("inventory member identity")
		}
		if _, ok := required[f.Path]; ok {
			required[f.Path] = true
		}
		if f.Path == typedindex.ManagedHelperFile && f.Digest != c.HelperSHA256 {
			return p, nil, errors.New("helper identity")
		}
	}
	for _, present := range required {
		if !present {
			return p, nil, errors.New("missing neutral input")
		}
	}
	source, e := acceptanceRead(filepath.Join(c.root(), "bundle/source/lib/lib.go"), 1024)
	if e != nil || string(source) != acceptanceSource {
		return p, nil, errors.New("neutral source oracle")
	}
	profile, e := acceptanceRead(filepath.Join(c.root(), "profile.json"), typedindex.MaxProfileBytes)
	if e != nil {
		return p, nil, e
	}
	selection, e := acceptanceRead(filepath.Join(c.root(), "bundle", provider.SelectionFile), provider.MaxSelectionBytes)
	if e != nil {
		return p, nil, e
	}
	p, e = acceptanceProfile(ctx, c, profile, selection)
	return p, raw, e
}

func TestTypedNativeAcceptance(t *testing.T) {
	if *acceptanceConfigPath == "" {
		if *acceptanceCase != "" || *acceptanceRole != "" || *acceptanceEndpoint != "" || *acceptanceParent != 0 {
			t.Fatal("partial native invocation")
		}
		t.Skip("requires a separately reviewed and approved native configuration")
	}
	if runtime.GOARCH != "arm64" || os.Geteuid() != 0 || !acceptanceCaseValid(*acceptanceCase) {
		t.Fatal("native platform/case refused")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 540*time.Second)
	defer cancel()
	raw, e := acceptanceRead(*acceptanceConfigPath, acceptanceMaxConfig)
	if e != nil {
		t.Fatal(e)
	}
	c, e := parseAcceptance(raw)
	if e != nil {
		t.Fatal(e)
	}
	p, inventory, e := acceptanceVerify(ctx, c, *acceptanceConfigPath)
	if e != nil {
		t.Fatal(e)
	}
	if *acceptanceRole != "" {
		if (*acceptanceRole != "run" && *acceptanceRole != "recover") || *acceptanceParent != os.Getppid() || *acceptanceParent < 2 {
			t.Fatal("child owner")
		}
		if e = acceptanceChildAuthority(c); e != nil {
			t.Fatal(e)
		}
		u, e := url.Parse(*acceptanceEndpoint)
		if e != nil || u.Scheme != "ws" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.Path != "" || u.User != nil || u.RawQuery != "" {
			t.Fatal("fixture endpoint")
		}
		if e = acceptanceRunChild(ctx, c, p, inventory, *acceptanceRole == "recover"); e != nil {
			t.Fatal(e)
		}
		return
	}
	if *acceptanceEndpoint != "" || *acceptanceParent != 0 {
		t.Fatal("unexpected child fields")
	}
	if e = acceptanceRunParent(ctx, c, p); e != nil {
		t.Fatal(e)
	}
}

func acceptanceEngine(ctx context.Context, c nativeAcceptanceConfig) (endpoint string, stop func() error, err error) {
	base := c.caseRoot(*acceptanceCase)
	if err = os.Mkdir(base, 0700); err != nil {
		return "", nil, err
	}
	for _, n := range []string{"workspace", "index", "db"} {
		if err = os.Mkdir(filepath.Join(base, n), 0700); err != nil {
			return "", nil, err
		}
	}
	if err = acceptanceReceipt(filepath.Join(base, "workspace/.phebs-index-publication.lock"), nil); err != nil {
		return "", nil, err
	}
	// The publication lock must be empty, not a JSON control.
	if err = os.Truncate(filepath.Join(base, "workspace/.phebs-index-publication.lock"), 0); err != nil {
		return "", nil, err
	}
	secret := make([]byte, 32)
	if _, err = rand.Read(secret); err != nil {
		return "", nil, err
	}
	password := hex.EncodeToString(secret)
	if err = os.WriteFile(filepath.Join(base, "credential"), []byte(password), 0600); err != nil {
		return "", nil, err
	}
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return "", nil, e
	}
	address := l.Addr().String()
	_ = l.Close()
	deadline, ok := ctx.Deadline()
	if !ok {
		return "", nil, errors.New("missing outer deadline")
	}
	engineCtx, cancelEngine := context.WithDeadline(context.Background(), deadline.Add(50*time.Second))
	cmd := exec.CommandContext(engineCtx, filepath.Join(c.root(), "surreal"), "start", "--bind", address, "--user", "root", "--pass", password, "surrealkv://"+filepath.Join(base, "db", "data"))
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + base}
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		cancelEngine()
		return "", nil, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	stop = func() error {
		defer cancelEngine()
		_ = cmd.Process.Kill()
		select {
		case <-done:
			return nil
		case <-time.After(10 * time.Second):
			return errors.New("engine join timeout")
		}
	}
	ready, ca := context.WithTimeout(ctx, 15*time.Second)
	defer ca()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		conn, e := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if e == nil {
			_ = conn.Close()
			break
		}
		select {
		case <-ready.Done():
			return "", stop, ready.Err()
		case <-tick.C:
		}
	}
	seed, e := acceptanceRead(filepath.Join(c.root(), "seed.surql"), 4<<20)
	if e != nil || acceptanceDigest(seed) != c.SeedSHA256 {
		return "", stop, errors.New("seed identity")
	}
	if e = acceptanceBootstrap(ctx, address, password); e != nil {
		return "", stop, e
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+address+"/import", bytes.NewReader(seed))
	if e != nil {
		return "", stop, e
	}
	req.SetBasicAuth("root", password)
	req.Header.Set("Surreal-NS", "t454")
	req.Header.Set("Surreal-DB", "neutral")
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{Proxy: nil}}
	response, e := client.Do(req)
	if e != nil {
		return "", stop, e
	}
	body, e := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	_ = response.Body.Close()
	client.CloseIdleConnections()
	if e != nil || response.StatusCode != 200 || len(body) >= 1<<20 {
		return "", stop, errors.New("seed import failed")
	}
	if e = acceptanceImportResponse(body); e != nil {
		return "", stop, e
	}
	return "ws://" + address, stop, nil
}

// OPTION IMPORT suppresses result rows; the empty array is valid, null is not.
// Imported authority is verified separately before any controller dispatch.
func acceptanceImportResponse(body []byte) error {
	var rows []struct {
		Status string `json:"status"`
	}
	if len(body) >= 1<<20 || json.Unmarshal(body, &rows) != nil || rows == nil {
		return errors.New("seed import response")
	}
	for _, row := range rows {
		if row.Status != "OK" {
			return errors.New("seed import statement failed")
		}
	}
	return nil
}

func TestNativeAcceptanceImportResponse(t *testing.T) {
	for _, body := range []string{`[]`, `[{"status":"OK"}]`, `null`, `{}`, `[{"status":"ERR"}]`, `[{"result":null}]`, strings.Repeat(" ", 1<<20)} {
		err := acceptanceImportResponse([]byte(body))
		if (err == nil) != (body == `[]` || body == `[{"status":"OK"}]`) {
			t.Fatal("import response gate")
		}
	}
}

// Fresh engine namespaces do not exist yet; bootstrap only this compiled neutral scope.
func acceptanceBootstrap(ctx context.Context, address, password string) error {
	const query = "DEFINE NAMESPACE IF NOT EXISTS t454; DEFINE DATABASE IF NOT EXISTS neutral;"
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+address+"/sql", strings.NewReader(query))
	if e != nil {
		return errors.New("seed namespace bootstrap")
	}
	req.SetBasicAuth("root", password)
	req.Header.Set("Surreal-NS", "t454")
	req.Header.Set("Surreal-DB", "neutral")
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	res, e := client.Do(req)
	if e != nil {
		return errors.New("seed namespace bootstrap")
	}
	defer func() { _ = res.Body.Close() }()
	raw, e := io.ReadAll(io.LimitReader(res.Body, 16385))
	var rows []struct {
		Status string `json:"status"`
	}
	if e != nil || res.StatusCode != 200 || len(raw) > 16384 || json.Unmarshal(raw, &rows) != nil || len(rows) != 2 || rows[0].Status != "OK" || rows[1].Status != "OK" {
		return errors.New("seed namespace bootstrap")
	}
	return nil
}

func TestNativeAcceptanceBootstrap(t *testing.T) {
	for _, body := range []string{`[{"status":"OK"},{"status":"OK"}]`, `[]`, `[{"status":"OK"},{"status":"ERR"}]`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, pass, ok := r.BasicAuth()
			raw, e := io.ReadAll(io.LimitReader(r.Body, 513))
			if e != nil || !ok || user != "root" || pass != "fixture" || r.Method != http.MethodPost || r.URL.Path != "/sql" || r.Header.Get("Surreal-NS") != "t454" || r.Header.Get("Surreal-DB") != "neutral" || string(raw) != "DEFINE NAMESPACE IF NOT EXISTS t454; DEFINE DATABASE IF NOT EXISTS neutral;" {
				t.Error("bootstrap escaped fixed neutral protocol")
			}
			_, _ = io.WriteString(w, body)
		}))
		err := acceptanceBootstrap(t.Context(), strings.TrimPrefix(server.URL, "http://"), "fixture")
		server.Close()
		if (err == nil) != (body == `[{"status":"OK"},{"status":"OK"}]`) {
			t.Fatal("bootstrap status gate", err)
		}
	}
}

func acceptanceStore(ctx context.Context, c nativeAcceptanceConfig, endpoint string) (*store.Surreal, error) {
	pw, e := acceptanceRead(filepath.Join(c.caseRoot(*acceptanceCase), "credential"), 64)
	if e != nil || len(pw) != 64 {
		return nil, errors.New("fixture credential")
	}
	return store.Open(ctx, endpoint, "root", string(pw), "t454", "neutral")
}
func acceptanceController(c nativeAcceptanceConfig, s *store.Surreal) (*Controller, *Runtime, error) {
	base := c.caseRoot(*acceptanceCase)
	controller, e := New(Config{Store: s, Workspace: filepath.Join(base, "workspace"), Socket: acceptanceSocket, Image: c.ImageSHA256, Acquire: func(ctx context.Context) (func(), error) {
		return focusedindex.AcquireMutationLock(ctx, filepath.Join(base, "index"))
	}})
	if e != nil {
		return nil, nil, e
	}
	r, e := NewRuntime(controller, func(ctx context.Context, a typedindex.Admission) (string, []byte, error) {
		if a.Request().Source != c.Source || a.Request().BundleDigest != c.InventorySHA256 {
			return "", nil, typedindex.Stale
		}
		b, e := acceptanceRead(filepath.Join(c.root(), "inventory.json"), typedindex.MaxInventoryBytes)
		return filepath.Join(c.root(), "bundle"), b, e
	})
	return controller, r, e
}
func acceptanceSeed(ctx context.Context, c nativeAcceptanceConfig, s *store.Surreal, p typedindex.Profile) error {
	source, e := s.GetTypedSource(ctx, acceptanceRepo)
	if e != nil || source != c.Source {
		return errors.New("seed source authority")
	}
	intent, e := s.GetTypedIndexIntent(ctx, acceptanceRepo)
	if e != nil || intent.ProfileDigest != p.Digest() || intent.ProfileEpoch != c.ProfileEpoch || intent.UniverseDigest != c.UniverseSHA256 || intent.Desired != "" || intent.Canceled || intent.RestoreRequired {
		return errors.New("seed profile authority")
	}
	for _, kind := range []store.TypedIndexControlKind{store.TypedIndexIntents, store.TypedIndexRequests, store.TypedIndexPlans, store.TypedIndexAttempts, store.TypedIndexStates, store.TypedIndexCurrents} {
		page, e := s.ScanTypedIndexControls(ctx, kind, "", 2)
		want := 0
		if kind == store.TypedIndexIntents {
			want = 1
		}
		if e != nil || len(page.Rows) != want || page.Next != "" {
			return errors.New("seed execution history")
		}
	}
	if _, e = s.GetTypedIndexGrowth(ctx); !errors.Is(e, store.ErrNotFound) {
		return errors.New("seed growth")
	}
	return nil
}
func acceptanceRunChild(ctx context.Context, c nativeAcceptanceConfig, p typedindex.Profile, inventory []byte, recovery bool) (err error) {
	s, e := acceptanceStore(ctx, c, *acceptanceEndpoint)
	if e != nil {
		return e
	}
	defer func() { _ = s.Close(context.Background()) }()
	controller, r, e := acceptanceController(c, s)
	if e != nil {
		return e
	}
	result := acceptanceChildResult{Schema: acceptanceSchema, Config: acceptanceDigest(acceptanceJSON(c)), Case: *acceptanceCase, Recovery: recovery}
	defer func() {
		name := "child.json"
		if recovery {
			name = "recovery.json"
		}
		err = errors.Join(err, acceptanceReceipt(filepath.Join(c.caseRoot(*acceptanceCase), name), result))
	}()
	if recovery {
		if e = r.Reconcile(ctx); e != nil {
			return e
		}
		selectedRaw, e := acceptanceRead(filepath.Join(c.caseRoot(*acceptanceCase), "selected.json"), 16384)
		if e != nil {
			return e
		}
		var selected acceptanceSelected
		if e = acceptanceDecode(selectedRaw, 16384, &selected); e != nil {
			return e
		}
		next, e := s.ClaimGenerationChunk(ctx, store.GenerationResourceTypedIndex, "native-recovery")
		if e != nil || next == nil {
			return errors.New("missing reaped lease")
		}
		before, e := s.ScanTypedIndexControls(ctx, store.TypedIndexAttempts, "", 64)
		if e != nil {
			return e
		}
		d, e := s.InspectTypedIndexDisposition(ctx, *next)
		if e != nil || d.State() != store.TypedIndexInterrupted {
			return errors.New("interrupted history not protected")
		}
		e = r.Class().Handle(ctx, *next, r.Class().Budget)
		if e == nil || !store.IsTerminal(e) {
			return errors.New("interrupted work replay allowed")
		}
		after, e := s.ScanTypedIndexControls(ctx, store.TypedIndexAttempts, "", 64)
		if e != nil || !bytes.Equal(acceptanceJSON(before), acceptanceJSON(after)) {
			return errors.New("replay changed history")
		}
		if e = s.FailGenerationChunk(ctx, *next, "neutral no-replay proof"); e != nil {
			return e
		}
		if e = r.Class().AfterSettlement(ctx, *next); e != nil {
			return e
		}
		result.NoReplay = true
		result.Settled = true
	} else {
		if e = acceptanceSeed(ctx, c, s, p); e != nil {
			return e
		}
		if e = r.Reconcile(ctx); e != nil {
			return e
		}
		request := typedindex.NewRequest(c.Source, p, uint64(c.ProfileEpoch), c.UniverseSHA256, "native-neutral")
		purpose := acceptanceCheckedPurpose(*acceptanceCase)
		if purpose != "" {
			request = typedindex.NewManagedRequest(c.Source, p, uint64(c.ProfileEpoch), c.UniverseSHA256, purpose)
		}
		if _, e = s.EnqueueTypedIndex(ctx, acceptanceRepo, acceptanceJSON(request)); e != nil {
			return e
		}
		if e = r.Coordinator(ctx, store.Job{Kind: store.JobTypedIndex, Target: acceptanceRepo}); e != nil {
			return e
		}
		scheduler, e := r.Scheduler(ctx)
		if e != nil {
			return e
		}
		runCtx, cancel := signal.NotifyContext(ctx, unix.SIGTERM)
		defer cancel()
		class := scheduler.Classes[store.GenerationResourceTypedIndex]
		handle, settle := class.Handle, class.AfterSettlement
		_ = inventory
		// Assignment below uses the scheduler's exact budget type, without replacing
		// its native operation or settlement implementation.
		class.Handle = acceptanceHandle(c, func(ctx context.Context, chunk store.GenerationChunk, budget generationscheduler.Budget) error {
			if e := handle(ctx, chunk, budget); e != nil || purpose == "" {
				return e
			}
			result.FailureSite = "checked_reuse"
			if e := acceptanceCheckedReuse(ctx, c, s, r, chunk, result.Outcome, purpose); e != nil {
				return e
			}
			result.NoReplay = true
			result.FailureSite = ""
			return nil
		})
		class.AfterSettlement = func(ctx context.Context, chunk store.GenerationChunk) error {
			e := settle(ctx, chunk)
			result.Settled = e == nil
			cancel()
			return e
		}
		scheduler.Classes[store.GenerationResourceTypedIndex] = class
		var reportFailed atomic.Bool
		r.Report = func(out Outcome, e error) { result.Outcome = out; result.ExecutionError = e != nil }
		scheduler.Report = func(e error) { reportFailed.Store(true) }
		e = scheduler.Run(runCtx)
		if e != nil && !errors.Is(e, context.Canceled) {
			return e
		}
		if reportFailed.Load() || !result.Settled {
			return errors.New("scheduler settlement failed")
		}
		if purpose != "" {
			result.FailureSite = "checked_status"
			if result.ExecutionError || !result.NoReplay {
				return errors.New("checked execution or reuse refused")
			}
			if e = acceptanceChecked(ctx, c, s, result.Outcome, purpose); e != nil {
				return e
			}
			if e = acceptanceSettledNoReplay(ctx, c, s); e != nil {
				return e
			}
			result.FailureSite = ""
		} else if *acceptanceCase == "success" {
			if result.ExecutionError {
				return errors.New("positive execution refused")
			}
			if e = acceptancePublication(ctx, c, s); e != nil {
				return e
			}
			result.PublicationVerified = true
			result.FailureSite = "schedule_before"
			before, e := s.GetGenerationSchedule(ctx, acceptanceRepo, store.TypedIndexScheduleStage)
			if e != nil {
				return e
			}
			result.FailureSite = "schedule_before_shape"
			if before.Status != store.GenerationScheduleSettled || before.Succeeded != 1 {
				return errors.New("success schedule not settled")
			}
			result.FailureSite = "duplicate_coordinator"
			if e = r.Coordinator(ctx, store.Job{Kind: store.JobTypedIndex, Target: acceptanceRepo}); e != nil {
				return e
			}
			result.FailureSite = "schedule_after"
			after, e := s.GetGenerationSchedule(ctx, acceptanceRepo, store.TypedIndexScheduleStage)
			if e != nil {
				return e
			}
			result.FailureSite = "schedule_changed"
			if !bytes.Equal(acceptanceJSON(before), acceptanceJSON(after)) {
				return errors.New("duplicate coordinator changed settled schedule")
			}
			result.FailureSite = "warm_claim"
			next, e := s.ClaimGenerationChunk(ctx, store.GenerationResourceTypedIndex, "native-warm")
			if e != nil && !errors.Is(e, store.ErrNotFound) {
				return e
			}
			result.FailureSite = "warm_unexpected"
			if next != nil {
				return errors.New("duplicate scheduled native work")
			}
			result.NoReplay = true
			result.FailureSite = ""

		} else {
			if !result.ExecutionError {
				return errors.New("negative execution succeeded")
			}
			if _, e = s.ResolveTypedIndexCurrentCustody(ctx, acceptanceRepo); e == nil {
				return errors.New("negative published")
			}
			if *acceptanceCase == "wall" && (result.Outcome.Reports[1].StopReason != "wall_limit" || result.Outcome.Reports[1].Watchdog == nil || result.Outcome.Reports[1].ExitCode != 124) {
				return errors.New("missing native watchdog wall evidence")
			}
			if e = acceptanceSettledNoReplay(ctx, c, s); e != nil {
				return e
			}
			result.NoReplay = true
		}
	}
	result.FailureSite = "growth_release"
	if _, e = s.GetTypedIndexGrowth(ctx); !errors.Is(e, store.ErrNotFound) {
		return errors.New("settled growth not released")
	}
	result.GrowthReleased = true
	result.FailureSite = ""
	_ = controller
	return nil
}

func acceptanceChecked(ctx context.Context, c nativeAcceptanceConfig, s *store.Surreal, out Outcome, purpose typedindex.Purpose) error {
	if out.Check == nil || out.Check.Validate() != nil || out.Check.Purpose != purpose || out.Pointer != (typedindex.PublicationPointer{}) || out.Check.Members != 1 || out.Check.Documents != 1 || out.Check.Generated != 0 {
		return errors.New("neutral checked summary")
	}
	for n, phase := range []typedindex.Action{typedindex.Plan, typedindex.Execute} {
		report := out.Reports[n]
		if report.Phase != phase || report.ExitCode != 0 || !report.Removed || report.StopReason != "" || report.Failure != nil {
			return errors.New("checked cold phase completion")
		}
	}
	states := [5]string{"complete", "complete", "complete", "complete", "not_requested"}
	status, err := s.GetTypedIndexStatus(ctx, acceptanceRepo)
	if err != nil || status.Stage != store.TypedChecked || status.States != states || status.Current != nil || status.Stale || status.Canceled || status.RestoreRequired || status.Check == nil || *status.Check != *out.Check || status.Desired != out.Check.RequestDigest {
		return errors.New("checked status or current")
	}
	current, err := s.ScanTypedIndexControls(ctx, store.TypedIndexCurrents, "", 2)
	if err != nil || len(current.Rows) != 0 || current.Next != "" {
		return errors.New("checked current installed")
	}
	attempt, err := s.InspectTypedIndexAttempt(ctx, out.AttemptDigest)
	if err != nil || attempt.Parent.Schema != typedindex.ManagedRequestSchema || attempt.Parent.Purpose != purpose || attempt.Parent.Source != c.Source || attempt.Stage != store.TypedChecked || attempt.States != states || attempt.Check == nil || *attempt.Check != *out.Check || attempt.PlanningDigest != out.Check.ParentDigest || attempt.RequestDigest != out.Check.RequestDigest || attempt.PlanDigest != out.Check.PlanDigest || attempt.Custody == nil || attempt.Custody.Revision != 2 {
		return errors.New("checked attempt authority")
	}
	id := typedworkspace.OwnerIdentity{PlanningDigest: attempt.PlanningDigest, AttemptDigest: attempt.AttemptDigest, ChunkIdentity: attempt.ChunkIdentity, LeaseDigest: attempt.LeaseDigest, Request: attempt.Parent}
	base := filepath.Join(c.caseRoot(*acceptanceCase), "workspace")
	manifest, err := typedworkspace.LoadOwner(ctx, base, id)
	if err != nil || manifest.Revision != 2 || manifest.Publication != nil || manifest.PublicationName != "" {
		return errors.New("checked publication custody")
	}
	entries, err := os.ReadDir(filepath.Join(base, id.RelativeName()))
	if err != nil || len(entries) > 16 {
		return errors.New("checked owner census")
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "bundle-") || entry.Name() == "publication-receipt.json" {
			return errors.New("checked publication files")
		}
	}
	return nil
}

// Reuse the same live lease through the ready runtime before scheduler settlement.
// Guard input/native entrypoints so a regression cannot launch a third container.
func acceptanceCheckedReuse(ctx context.Context, c nativeAcceptanceConfig, s *store.Surreal, r *Runtime, chunk store.GenerationChunk, out Outcome, purpose typedindex.Purpose) error {
	if err := acceptanceChecked(ctx, c, s, out, purpose); err != nil {
		return err
	}
	before, err := s.ScanTypedIndexControls(ctx, store.TypedIndexAttempts, "", 64)
	if err != nil {
		return err
	}
	growth, err := s.GetTypedIndexGrowth(ctx)
	if err != nil {
		return err
	}
	priorNative, priorBundle := r.controller.native, r.bundle
	defer func() { r.controller.native, r.bundle = priorNative, priorBundle }()
	var lookup, native atomic.Bool
	r.controller.native.begin = func(context.Context, string, string) (typedsandbox.Allowance, error) {
		native.Store(true)
		return typedsandbox.Allowance{}, ErrHeld
	}
	r.controller.native.prepare = func(context.Context, typedsandbox.HostScratchOptions, *lifecycle.Gate) (typedsandbox.HostScratchReceipt, error) {
		native.Store(true)
		return typedsandbox.HostScratchReceipt{}, ErrHeld
	}
	r.controller.native.run = func(context.Context, typedsandbox.Options, typedsandbox.ScratchAuthority) (typedsandbox.Result, error) {
		native.Store(true)
		return typedsandbox.Result{}, ErrHeld
	}
	r.bundle = func(context.Context, typedindex.Admission) (string, []byte, error) {
		lookup.Store(true)
		return "", nil, ErrHeld
	}
	if err = r.Class().Handle(ctx, chunk, r.Class().Budget); err != nil {
		return err
	}
	after, err := s.ScanTypedIndexControls(ctx, store.TypedIndexAttempts, "", 64)
	if err != nil || !bytes.Equal(acceptanceJSON(before), acceptanceJSON(after)) {
		return errors.New("checked reuse changed attempts")
	}
	nextGrowth, err := s.GetTypedIndexGrowth(ctx)
	if err != nil || !bytes.Equal(acceptanceJSON(growth), acceptanceJSON(nextGrowth)) || lookup.Load() || native.Load() {
		return errors.New("checked reuse copied, grew or launched")
	}
	return acceptanceChecked(ctx, c, s, out, purpose)
}

func acceptanceHandle(c nativeAcceptanceConfig, handle generationscheduler.Handler) generationscheduler.Handler {
	return func(ctx context.Context, chunk store.GenerationChunk, budget generationscheduler.Budget) error {
		selected := acceptanceSelected{Chunk: chunk, Attempt: acceptanceDigest([]byte(chunk.Identity + "\x00" + chunk.LeaseToken))}
		if e := acceptanceReceipt(filepath.Join(c.caseRoot(*acceptanceCase), "selected.json"), selected); e != nil {
			return e
		}
		return handle(ctx, chunk, budget)
	}
}
func acceptancePublication(ctx context.Context, c nativeAcceptanceConfig, s *store.Surreal) error {
	current, e := s.ResolveTypedIndexCurrentCustody(ctx, acceptanceRepo)
	if e != nil {
		return e
	}
	id := typedworkspace.OwnerIdentity{PlanningDigest: current.PlanningDigest, AttemptDigest: current.AttemptDigest, ChunkIdentity: current.ChunkIdentity, LeaseDigest: current.LeaseDigest, Request: current.Parent.Request()}
	base := filepath.Join(c.caseRoot(*acceptanceCase), "workspace")
	p, e := typedworkspace.OpenOwnerPublication(ctx, base, id, current.Parent, current.Admission, current.Pointer.Binding.PlanDigest, current.Pointer.RootDigest)
	if e != nil {
		return e
	}
	defer func() { _ = p.Close() }()
	root := p.Root()
	raw, e := p.ReadMember(ctx, root.Attempt.Name)
	if e != nil {
		return e
	}
	var manifest typedindex.AttemptManifest
	if e = json.Unmarshal(raw, &manifest); e != nil {
		return e
	}
	if !manifest.Complete || len(manifest.Members) != 1 {
		return errors.New("neutral publication member cardinality")
	}
	for _, member := range manifest.Members {
		raw, e = p.ReadMember(ctx, member.Name)
		if e != nil {
			return e
		}
		var index scip.Index
		if proto.Unmarshal(raw, &index) != nil || !acceptanceNeutralDefinition(&index) {
			return errors.New("neutral Answer definition oracle")
		}
	}

	blocked, ca := context.WithTimeout(ctx, 75*time.Millisecond)
	defer ca()
	release, e := typedworkspace.AcquirePublicationMutation(blocked, filepath.Join(base, id.RelativeName()))
	if e == nil {
		release()
		return errors.New("reader pin did not protect actual publication")
	}
	return nil
}

// Monitor Docker's bounded dedicated namespace; it never creates or removes a
// container. Only the production controller owns those mutations.
type acceptanceDocker struct{ client *http.Client }

func newAcceptanceDocker() *acceptanceDocker {
	tr := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", acceptanceSocket)
	}}
	return &acceptanceDocker{&http.Client{Transport: tr, Timeout: 2 * time.Second}}
}
func (d *acceptanceDocker) get(ctx context.Context, path string, out any) error {
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/v1.47"+path, nil)
	if e != nil {
		return e
	}
	res, e := d.client.Do(req)
	if e != nil {
		return e
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != 200 {
		return errors.New("Docker observation unavailable")
	}
	b, e := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if e != nil || len(b) > 1<<20 {
		return errors.New("Docker observation overflow")
	}
	return json.Unmarshal(b, out)
}
func (d *acceptanceDocker) list(ctx context.Context) ([]struct {
	ID string `json:"Id"`
}, error) {
	var rows []struct {
		ID string `json:"Id"`
	}
	e := d.get(ctx, "/containers/json?all=1&limit=129", &rows)
	if e != nil || len(rows) > 1 {
		return nil, errors.New("dedicated daemon not exclusive")
	}
	return rows, nil
}

// The returned pin must remain held through the caller's exact pidfd signal.
// Only closed site tokens enter the source-free receipt; wrapped private causes
// remain available to error classification without being serialized.
type acceptanceObservationError struct {
	site  string
	cause error
}

func (e *acceptanceObservationError) Error() string { return "native observation refused" }
func (e *acceptanceObservationError) Unwrap() error { return e.cause }
func acceptanceObservationSite(err error) string {
	var e *acceptanceObservationError
	if errors.As(err, &e) {
		switch e.site {
		case "container_list", "container_identity", "container_state", "attempt", "pin_open", "pin_lock", "owner_load", "owner_compare", "recorded", "phase", "host_observe", "host_verify", "worker_recheck", "kernel", "recorded_recheck":
			return "observe_" + e.site
		}
	}
	return "observe_unavailable"
}

func (d *acceptanceDocker) observe(ctx context.Context, c nativeAcceptanceConfig, s *store.Surreal, selected acceptanceSelected, seen map[string]bool) (observation *acceptanceObservation, unpin func(), err error) {
	site := "container_list"
	defer func() {
		if err != nil {
			err = &acceptanceObservationError{site: site, cause: err}
		}
	}()
	noop := func() {}
	rows, e := d.list(ctx)
	if e != nil || len(rows) == 0 {
		return nil, noop, e
	}
	site = "container_identity"
	id := rows[0].ID
	if seen[id] {
		return nil, noop, nil
	}
	if len(id) != 64 || !acceptanceHash("sha256:"+id) {
		return nil, noop, errors.New("container identity")
	}
	// This preliminary observation only avoids expensive custody reads before the
	// worker exists. It supplies no authority to record or signal anything.
	site = "container_state"
	var state struct {
		State struct {
			Pid     int
			Running bool
		}
	}
	if e = d.get(ctx, "/containers/"+id+"/json", &state); e != nil {
		return nil, noop, e
	}
	if !state.State.Running {
		return nil, noop, nil
	}
	worker, start, e := acceptanceWorker(state.State.Pid)
	if e != nil {
		return nil, noop, nil
	}
	site = "attempt"
	attempt, e := s.InspectTypedIndexAttempt(ctx, selected.Attempt)
	if e != nil || attempt.Custody == nil || attempt.PlanningDigest != selected.Chunk.Generation || attempt.ChunkIdentity != selected.Chunk.Identity {
		return nil, noop, errors.New("selected attempt custody")
	}
	identity := typedworkspace.OwnerIdentity{PlanningDigest: attempt.PlanningDigest, AttemptDigest: attempt.AttemptDigest, ChunkIdentity: attempt.ChunkIdentity, LeaseDigest: attempt.LeaseDigest, Request: attempt.Parent}
	base := filepath.Join(c.caseRoot(*acceptanceCase), "workspace")
	attemptPath := filepath.Join(base, identity.RelativeName())
	pins := []*os.File{}
	release := func() {
		for i := len(pins) - 1; i >= 0; i-- {
			_ = pins[i].Close()
		}
	}
	for _, dir := range []string{base, attemptPath} {
		site = "pin_open"
		pin, err := acceptanceOpen(filepath.Join(dir, ".phebs-index-publication.lock"))
		if err != nil {
			release()
			return nil, noop, err
		}
		pins = append(pins, pin)
		site = "pin_lock"
		if err = unix.Flock(int(pin.Fd()), unix.LOCK_SH|unix.LOCK_NB); err != nil {
			release()
			return nil, noop, err
		}
	}
	fail := func(err error) (*acceptanceObservation, func(), error) { release(); return nil, noop, err }
	site = "owner_load"
	manifest, e := typedworkspace.LoadOwnerWithNativeCustody(ctx, base, identity)
	if e != nil {
		return fail(e)
	}
	site = "owner_compare"
	if manifest.Digest() != attempt.Custody.ManifestDigest || manifest.Directory.Device != attempt.Custody.DirectoryDevice || manifest.Directory.Inode != attempt.Custody.DirectoryInode || manifest.InputName == "" {
		return fail(errors.New("retained owner mismatch"))
	}
	site = "recorded"
	observed, e := typedsandbox.InspectRecorded(ctx, typedsandbox.RecoveryOptions{Socket: acceptanceSocket, ImageID: c.ImageSHA256, Inputs: filepath.Join(attemptPath, manifest.InputName), PlanningDigest: attempt.PlanningDigest, AttemptDigest: attempt.AttemptDigest})
	if e != nil || observed.ContainerID != id || observed.PID != state.State.Pid {
		return fail(errors.New("authenticated container mismatch"))
	}
	site = "phase"
	if observed.Control.RequestDigest != attempt.RequestDigest || observed.Controls != filepath.Join(attemptPath, "controls-"+string(observed.Control.Phase)) {
		return fail(errors.New("authenticated phase mismatch"))
	}
	name, _ := typedsandbox.HostScratchRootName(attempt.PlanningDigest, attempt.AttemptDigest)
	site = "host_observe"
	host, e := typedsandbox.ObserveHostScratch(ctx, name)
	if e != nil || host.Held || host.Selected == nil {
		return fail(errors.New("host scratch ownership"))
	}
	site = "host_verify"
	receipt, e := typedsandbox.VerifyHostScratch(ctx, host.Selected.Options)
	if e != nil || receipt.Authority != observed.Scratch {
		return fail(errors.New("host scratch authority mismatch"))
	}
	site = "worker_recheck"
	if parent, current, e := acceptanceProc(worker); e != nil || parent != observed.PID || current != start {
		return fail(errors.New("worker changed during authentication"))
	}
	site = "kernel"
	kernel, e := acceptanceKernelLimits(observed.PID, worker)
	if e != nil {
		return fail(e)
	}
	site = "recorded_recheck"
	confirmed, e := typedsandbox.InspectRecorded(ctx, typedsandbox.RecoveryOptions{Socket: acceptanceSocket, ImageID: c.ImageSHA256, Inputs: observed.Inputs, PlanningDigest: attempt.PlanningDigest, AttemptDigest: attempt.AttemptDigest})
	if e != nil || confirmed != observed {
		return fail(errors.New("running container changed during authentication"))
	}
	return &acceptanceObservation{ID: id, Phase: string(observed.Control.Phase), Allowance: observed.Allowance, Seal: observed.Control.SealDigest, SupervisorPID: observed.PID, WorkerPID: worker, WorkerStart: start, Scratch: receipt, Kernel: kernel}, release, nil
}
func acceptanceProc(pid int) (ppid int, start string, err error) {
	raw, e := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if e != nil || len(raw) > 4096 {
		return 0, "", errors.New("proc stat")
	}
	i := bytes.LastIndex(raw, []byte(") "))
	if i < 0 {
		return 0, "", errors.New("proc stat framing")
	}
	fields := strings.Fields(string(raw[i+2:]))
	if len(fields) < 22 {
		return 0, "", errors.New("proc stat fields")
	}
	ppid, e = strconv.Atoi(fields[1])
	if e != nil {
		return 0, "", e
	}
	return ppid, fields[19], nil
}

// /proc children is per creating thread, not per thread group. Bound the
// complete task census; disappearing threads refuse this observation.
func acceptanceWorkerPID(tasks string) (int, error) {
	f, err := os.Open(tasks)
	if err != nil {
		return 0, errors.New("worker census")
	}
	threads, err := f.ReadDir(257)
	closeErr := f.Close()
	if err != nil && !errors.Is(err, io.EOF) || closeErr != nil || len(threads) == 0 || len(threads) > 256 {
		return 0, errors.New("worker task census")
	}
	worker := 0
	for _, thread := range threads {
		tid, parseErr := strconv.Atoi(thread.Name())
		if parseErr != nil || tid < 2 || !thread.IsDir() {
			return 0, errors.New("worker task identity")
		}
		children, openErr := os.Open(filepath.Join(tasks, thread.Name(), "children"))
		if openErr != nil {
			return 0, errors.New("worker census")
		}
		b, readErr := io.ReadAll(io.LimitReader(children, 4097))
		closeErr = children.Close()
		if readErr != nil || closeErr != nil || len(b) > 4096 {
			return 0, errors.New("worker census")
		}
		for _, field := range strings.Fields(string(b)) {
			pid, parseErr := strconv.Atoi(field)
			if parseErr != nil || pid < 2 || worker != 0 && worker != pid {
				return 0, errors.New("worker cardinality")
			}
			worker = pid
		}
	}
	if worker == 0 {
		return 0, errors.New("worker cardinality")
	}
	return worker, nil
}

func acceptanceWorker(supervisor int) (int, string, error) {
	if supervisor < 2 {
		return 0, "", errors.New("supervisor pid")
	}
	pid, e := acceptanceWorkerPID(fmt.Sprintf("/proc/%d/task", supervisor))
	if e != nil {
		return 0, "", e
	}
	parent, start, e := acceptanceProc(pid)
	if e != nil || parent != supervisor {
		return 0, "", errors.New("worker ancestry")
	}
	b, e := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if e != nil || len(b) > 16384 {
		return 0, "", errors.New("worker status")
	}
	uid, gid, nspid := false, false, false
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) == 5 && f[0] == "Uid:" {
			uid = strings.Join(f[1:], ",") == "65534,65534,65534,65534"
		}
		if len(f) == 5 && f[0] == "Gid:" {
			gid = strings.Join(f[1:], ",") == "65534,65534,65534,65534"
		}
		if len(f) >= 3 && f[0] == "NSpid:" {
			n, parseErr := strconv.Atoi(f[len(f)-1])
			nspid = parseErr == nil && n > 1
		}
	}
	var workerNS, parentNS unix.Stat_t
	if unix.Stat(fmt.Sprintf("/proc/%d/ns/pid", pid), &workerNS) != nil || unix.Stat(fmt.Sprintf("/proc/%d/ns/pid", supervisor), &parentNS) != nil || workerNS.Dev != parentNS.Dev || workerNS.Ino != parentNS.Ino {
		return 0, "", errors.New("worker PID namespace changed")
	}
	if !uid || !gid || !nspid {
		return 0, "", errors.New("worker credentials/namespace")
	}
	return pid, start, nil
}
func acceptanceSignal(pid, parent int, start string, sig unix.Signal) error {
	fd, e := unix.PidfdOpen(pid, 0)
	if e != nil {
		return e
	}
	defer func() { _ = unix.Close(fd) }()
	p, s, e := acceptanceProc(pid)
	if e != nil || p != parent || s != start {
		return errors.New("signal target changed")
	}
	return unix.PidfdSendSignal(fd, sig, nil, 0)
}
func acceptanceChild(ctx context.Context, c nativeAcceptanceConfig, endpoint, role string) (*exec.Cmd, <-chan error, error) {
	exe, e := os.Executable()
	if e != nil {
		return nil, nil, e
	}
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestTypedNativeAcceptance$", "-test.count=1", "-test.timeout=540s", "-typed-native-config="+*acceptanceConfigPath, "-typed-native-case="+*acceptanceCase, "-typed-native-role="+role, "-typed-native-endpoint="+endpoint, "-typed-native-parent="+strconv.Itoa(os.Getpid()))
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + c.caseRoot(*acceptanceCase)}
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	reader, writer, e := os.Pipe()
	if e != nil {
		return nil, nil, e
	}
	frame := acceptanceChildFrame(c, endpoint, role)
	if _, e = writer.Write(frame); e != nil {
		_ = reader.Close()
		_ = writer.Close()
		return nil, nil, e
	}
	_ = writer.Close()
	cmd.ExtraFiles = []*os.File{reader}
	defer func() { _ = reader.Close() }()
	if e = cmd.Start(); e != nil {
		return nil, nil, e
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	return cmd, done, nil
}

// Only fixed local engine refusals refine the stage token; library errors remain generic.
func acceptanceEngineFailureSite(err error) string {
	switch err.Error() {
	case "seed namespace bootstrap":
		return "engine_seed_namespace_bootstrap"
	case "seed identity":
		return "engine_seed_identity"
	case "seed import failed":
		return "engine_seed_import_http"
	case "seed import response":
		return "engine_seed_import_response"
	case "seed import statement failed":
		return "engine_seed_import_statement"
	default:
		return "engine"
	}
}

// Only fixed harness sites survive the private child's discarded stderr.
func acceptanceChildFailureSiteValid(site string) bool {
	switch site {
	case "", "schedule_before", "schedule_before_shape", "duplicate_coordinator", "schedule_after", "schedule_changed", "warm_claim", "warm_unexpected", "growth_release", "checked_reuse", "checked_status":
		return true
	default:
		return false
	}
}

func TestNativeAcceptanceChildFailureSite(t *testing.T) {
	for _, site := range []string{"", "schedule_before", "schedule_before_shape", "duplicate_coordinator", "schedule_after", "schedule_changed", "warm_claim", "warm_unexpected", "growth_release", "checked_reuse", "checked_status", "private/path/credential", "unknown"} {
		want := site != "private/path/credential" && site != "unknown"
		if acceptanceChildFailureSiteValid(site) != want {
			t.Fatal("closed child failure classification")
		}
	}
}

func TestNativeAcceptanceFailureSite(t *testing.T) {
	if acceptanceEngineFailureSite(errors.New("seed import response")) != "engine_seed_import_response" || acceptanceEngineFailureSite(errors.New("private/path/credential")) != "engine" {
		t.Fatal("closed source-free failure classification")
	}
}

func acceptanceRunParent(ctx context.Context, c nativeAcceptanceConfig, p typedindex.Profile) (err error) {
	failureSite := "deployment"
	final := acceptanceFinal{Schema: acceptanceSchema, Config: acceptanceDigest(acceptanceJSON(c)), Case: *acceptanceCase, Observations: []acceptanceObservation{}}
	// Receipt is bounded and exclusive even on failure, never a raw error channel.
	defer func() {
		if err != nil {
			final.Error = "native_acceptance_failed"
			if failureSite != "" {
				final.Error += "/" + failureSite
			}
			final.Pass = false
		}
		err = errors.Join(err, acceptanceReceipt(filepath.Join(c.caseRoot(*acceptanceCase), "receipt.json"), final))
	}()
	docker := newAcceptanceDocker()
	defer docker.client.CloseIdleConnections()
	if e := acceptanceDeployment(ctx, c, docker); e != nil {
		return e
	}
	failureSite = "daemon_empty"
	rows, e := docker.list(ctx)
	if e != nil || len(rows) != 0 {
		return errors.New("daemon not initially empty")
	}
	failureSite = "scratch_empty"
	host, e := typedsandbox.ObserveHostScratch(ctx, "")
	if e != nil || host.Held || host.Overflow || len(host.Names) != 0 {
		return errors.New("host not initially empty")
	}
	failureSite = "engine"
	endpoint, stop, e := acceptanceEngine(ctx, c)
	if stop != nil {
		defer func() {
			stopErr := stop()
			final.EngineJoined = stopErr == nil
			if err == nil && stopErr != nil {
				failureSite = "engine_join"
			}
			err = errors.Join(err, stopErr)
		}()
	}
	if e != nil {
		failureSite = acceptanceEngineFailureSite(e)
		return e
	}
	failureSite = "store_open"
	s, e := acceptanceStore(ctx, c, endpoint)
	if e != nil {
		return e
	}
	defer func() { _ = s.Close(context.Background()) }()
	failureSite = "seed_authority"
	if e = acceptanceSeed(ctx, c, s, p); e != nil {
		return e
	}
	failureSite = "scheduler_empty"
	if e = acceptanceEmptyScheduler(ctx, c, endpoint); e != nil {
		return e
	}
	failureSite = "dispatch_marker"
	if e = acceptanceReceipt(filepath.Join(c.caseRoot(*acceptanceCase), "dispatched.json"), map[string]string{"config": final.Config, "case": *acceptanceCase}); e != nil {
		return e
	}
	failureSite = "controller_child"
	cmd, done, e := acceptanceChild(ctx, c, endpoint, "run")
	if e != nil {
		return e
	}
	failureSite = "controller_identity"
	_, controllerStart, e := acceptanceProc(cmd.Process.Pid)
	if e != nil {
		_ = cmd.Process.Kill()
		select {
		case <-done:
			return e
		case <-time.After(10 * time.Second):
			return errors.Join(e, errors.New("initial controller join unproved; custody held"))
		}
	}
	failureSite = ""
	// Never leave the owned controller running after a monitor failure. Exact
	// production recovery below is attempted only after its process is joined.
	joined := false
	defer func() {
		if joined && final.Result.Config == "" {
			for _, name := range []string{"recovery.json", "child.json"} {
				raw, e := acceptanceRead(filepath.Join(c.caseRoot(*acceptanceCase), name), acceptanceMaxReceipt)
				var partial acceptanceChildResult
				if e == nil && acceptanceDecode(raw, acceptanceMaxReceipt, &partial) == nil && partial.Schema == acceptanceSchema && partial.Config == final.Config && partial.Case == final.Case && acceptanceChildFailureSiteValid(partial.FailureSite) {
					final.Result = partial
					break
				}
			}
		}
		if err != nil && joined {
			emergencyErr := acceptanceEmergency(c, s)
			final.Emergency = "completed"
			if emergencyErr != nil {
				final.Emergency = "held"
			}
			err = errors.Join(err, emergencyErr)
		}
	}()
	defer func() {
		if !joined {
			_ = cmd.Process.Kill()
			select {
			case <-done:
				joined = true
			case <-time.After(10 * time.Second):
				err = errors.Join(err, errors.New("controller join failed"))
			}
		}
	}()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	seen := map[string]bool{}
	var selected acceptanceSelected
	var childErr error
loop:
	for {
		select {
		case childErr = <-done:
			joined = true
			break loop
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if selected.Attempt == "" {
				b, e := acceptanceRead(filepath.Join(c.caseRoot(*acceptanceCase), "selected.json"), 16384)
				if errors.Is(e, os.ErrNotExist) {
					continue
				}
				if e != nil || acceptanceDecode(b, 16384, &selected) != nil {
					return errors.New("selected authority")
				}
			}
			observed, unpin, e := docker.observe(ctx, c, s, selected, seen)
			if e != nil {
				failureSite = acceptanceObservationSite(e)
				return e
			}
			if observed == nil {
				continue
			}
			if !seen[observed.ID] {
				if len(seen) == 2 {
					unpin()
					return errors.New("native container count exceeded")
				}
				seen[observed.ID] = true
				final.Observations = append(final.Observations, *observed)
			}
			if observed.Phase == "execute" && !final.Injected && acceptanceFaultCase(*acceptanceCase) {
				switch *acceptanceCase {
				case "cancel":
					e = acceptanceSignal(cmd.Process.Pid, os.Getpid(), controllerStart, unix.SIGTERM)
				case "wall":
					e = acceptanceSignal(observed.WorkerPID, observed.SupervisorPID, observed.WorkerStart, unix.SIGSTOP)
				case "hard-death":
					e = acceptanceSignal(cmd.Process.Pid, os.Getpid(), controllerStart, unix.SIGKILL)
				}
				if e != nil {
					unpin()
					return e
				}
				final.Injected = true
			}
			unpin()
		}
	}
	if len(final.Observations) != 2 || final.Observations[0].Phase != "plan" || final.Observations[1].Phase != "execute" {
		return errors.New("missing two cold phase observations")
	}
	a, b := final.Observations[0].Allowance, final.Observations[1].Allowance
	if a.Start != b.Start || a.Deadline != b.Deadline || a.BootID != b.BootID || a.TimeDevice != b.TimeDevice || a.TimeInode != b.TimeInode || b.WorkerBytesUsed <= 0 || b.WireBytesUsed <= 0 {
		return errors.New("shared absolute allowance")
	}
	if acceptanceFaultCase(*acceptanceCase) && !final.Injected {
		return errors.New("missing fault injection")
	}
	resultName := "child.json"
	if *acceptanceCase == "hard-death" {
		var exit *exec.ExitError
		if !errors.As(childErr, &exit) || exit.ProcessState.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
			return errors.New("controller death not established")
		}
		timer := time.NewTimer(21 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		recoveryCmd, recovered, e := acceptanceChild(ctx, c, endpoint, "recover")
		if e != nil {
			return e
		}
		select {
		case e = <-recovered:
			if e != nil {
				return errors.New("fresh recovery failed")
			}
		case <-ctx.Done():
			_ = recoveryCmd.Process.Kill()
			select {
			case <-recovered:
			case <-time.After(10 * time.Second):
				joined = false
				return errors.New("recovery child join unproved; custody held")
			}
			return ctx.Err()
		}
		resultName = "recovery.json"
	} else if childErr != nil {
		return errors.New("controller proof failed")
	}
	resultRaw, e := acceptanceRead(filepath.Join(c.caseRoot(*acceptanceCase), resultName), acceptanceMaxReceipt)
	if e != nil || acceptanceDecode(resultRaw, acceptanceMaxReceipt, &final.Result) != nil || final.Result.Schema != acceptanceSchema || final.Result.Recovery != (*acceptanceCase == "hard-death") || final.Result.Config != final.Config || final.Result.Case != final.Case || !final.Result.GrowthReleased || !final.Result.Settled || !final.Result.NoReplay || final.Result.FailureSite != "" {
		return errors.New("child evidence")
	}
	rows, e = docker.list(ctx)
	host, he := typedsandbox.ObserveHostScratch(ctx, "")
	if e != nil || len(rows) != 0 || he != nil || host.Held || host.Overflow || len(host.Names) != 0 {
		return errors.New("native cleanup not proven")
	}
	final.NativeAbsent = true
	if *acceptanceCase == "success" {
		if !final.Result.PublicationVerified {
			return errors.New("publication proof missing")
		}
	} else if purpose := acceptanceCheckedPurpose(*acceptanceCase); purpose != "" {
		if final.Injected || final.Result.ExecutionError || final.Result.PublicationVerified {
			return errors.New("checked case injected or published")
		}
		if e = acceptanceChecked(ctx, c, s, final.Result.Outcome, purpose); e != nil {
			return e
		}
	}
	if e = acceptanceDrain(ctx, c, s, &final); e != nil {
		return e
	}
	final.Pass = true
	return nil
}
func acceptanceDrain(ctx context.Context, c nativeAcceptanceConfig, s *store.Surreal, final *acceptanceFinal) error {
	controller, r, e := acceptanceController(c, s)
	if e != nil {
		return e
	}
	if e = r.Reconcile(ctx); e != nil {
		return e
	}
	// No mirror, shard or other repository artifact exists in this neutral-only
	// fixture. Drop logical current/intent using the ordinary exact repository
	// deletion transaction under the shared lifecycle guard; custody rows remain.
	release, e := controller.enter(ctx)
	if e != nil {
		return e
	}
	e = s.DeleteRepo(ctx, acceptanceRepo)
	release()
	if e != nil {
		return e
	}

	owner := LifecycleOwner{Controller: controller}
	cursor := ""
	for n := 0; n < 12000; n++ {
		if e = ctx.Err(); e != nil {
			return e
		}
		result := owner.Sweep(ctx, time.Now(), cursor, lifecycle.DefaultLimits())
		final.LifecycleTurns++
		if result.Err != nil {
			return result.Err
		}
		if result.Deleted > 16 {
			return errors.New("lifecycle mutation bound")
		}
		cursor = result.Cursor
		entries, e := os.ReadDir(filepath.Join(c.caseRoot(*acceptanceCase), "workspace"))
		if e != nil {
			return e
		}
		if len(entries) == 1 && entries[0].Name() == ".phebs-index-publication.lock" {
			drained, tombstones, err := acceptanceControlsDrained(ctx, s)
			if err != nil {
				return err
			}
			final.RetainedTombstones = tombstones
			if drained {
				final.WorkspaceDrained = true
				return nil
			}
		}
	}
	return errors.New("bounded lifecycle drain unfinished")
}

func TestNativeAcceptanceSafeFiles(t *testing.T) {
	base := t.TempDir()
	good := filepath.Join(base, "good")
	if e := os.WriteFile(good, []byte("x"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := acceptanceRead(good, 1); e != nil {
		t.Fatal(e)
	}
	if _, e := acceptanceRead(good, 0); e == nil {
		t.Fatal("bound")
	}
	link := filepath.Join(base, "link")
	if e := os.Symlink(good, link); e != nil {
		t.Fatal(e)
	}
	if _, e := acceptanceRead(link, 1); e == nil {
		t.Fatal("symlink")
	}
	hard := filepath.Join(base, "hard")
	if e := os.Link(good, hard); e != nil {
		t.Fatal(e)
	}
	if _, e := acceptanceRead(good, 1); e == nil {
		t.Fatal("hardlink")
	}
	fifo := filepath.Join(base, "fifo")
	if e := unix.Mkfifo(fifo, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := acceptanceRead(fifo, 1); e == nil {
		t.Fatal("FIFO")
	}
}

func acceptanceEmptyScheduler(ctx context.Context, c nativeAcceptanceConfig, endpoint string) error {
	password, e := acceptanceRead(filepath.Join(c.caseRoot(*acceptanceCase), "credential"), 64)
	if e != nil {
		return e
	}
	// Closed physical first-row probes; no COUNT, corpus JSON decode or SQL from
	// configuration. The imported trusted seed must contain only preparation.
	query := "SELECT VALUE id FROM generation_schedule LIMIT 1; SELECT VALUE id FROM generation_schedule_chunk LIMIT 1; SELECT VALUE id FROM generation_schedule_current LIMIT 1; SELECT VALUE id FROM typed_index_job LIMIT 1; SELECT VALUE name FROM repo LIMIT 2;"
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+strings.TrimPrefix(endpoint, "ws://")+"/sql", strings.NewReader(query))
	if e != nil {
		return e
	}
	req.SetBasicAuth("root", string(password))
	req.Header.Set("Surreal-NS", "t454")
	req.Header.Set("Surreal-DB", "neutral")
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	res, e := client.Do(req)
	if e != nil {
		return e
	}
	defer func() { _ = res.Body.Close() }()
	raw, e := io.ReadAll(io.LimitReader(res.Body, 16385))
	if e != nil || len(raw) > 16384 || res.StatusCode != 200 {
		return errors.New("seed scheduler inspection")
	}
	var rows []struct {
		Status string            `json:"status"`
		Result []json.RawMessage `json:"result"`
	}
	if json.Unmarshal(raw, &rows) != nil || len(rows) != 5 {
		return errors.New("seed scheduler response")
	}
	for _, r := range rows[:4] {
		if r.Status != "OK" || len(r.Result) != 0 {
			return errors.New("seed contains scheduled history")
		}
	}
	if rows[4].Status != "OK" || len(rows[4].Result) != 1 || !bytes.Equal(rows[4].Result[0], acceptanceJSON(acceptanceRepo)) {
		return errors.New("seed repository universe")
	}
	return nil
}
func acceptanceEmergency(c nativeAcceptanceConfig, s *store.Surreal) error {
	// Called only after the exact owned controller is joined. Give its last real
	// heartbeat the unchanged stale age; fresh reconciliation owns all cleanup.
	// A failed/ambiguous prefix is retained. This cannot dispatch a worker.
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	timer := time.NewTimer(21 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	_, r, e := acceptanceController(c, s)
	if e != nil {
		return e
	}
	return r.Reconcile(ctx)
}

func TestNativeAcceptanceSignalIdentity(t *testing.T) {
	pid := os.Getpid()
	parent, start, e := acceptanceProc(pid)
	if e != nil {
		t.Fatal(e)
	}
	if e = acceptanceSignal(pid, parent+1, start, 0); e == nil {
		t.Fatal("wrong parent accepted")
	}
	if e = acceptanceSignal(pid, parent, start+"0", 0); e == nil {
		t.Fatal("PID lifetime changed")
	}
	if e = acceptanceSignal(pid, parent, start, 0); e != nil {
		t.Fatal("exact signal-0 probe", e)
	}
}

func acceptanceChildFrame(c nativeAcceptanceConfig, endpoint, role string) []byte {
	return []byte(acceptanceDigest(acceptanceJSON(c)) + "\x00" + *acceptanceCase + "\x00" + endpoint + "\x00" + role)
}
func acceptanceChildAuthority(c nativeAcceptanceConfig) error {
	var pipe unix.Stat_t
	if unix.Fstat(3, &pipe) != nil || pipe.Mode&unix.S_IFMT != unix.S_IFIFO || pipe.Uid != 0 {
		return errors.New("missing parent control pipe")
	}
	self, e := os.Stat(filepath.Join(c.root(), "native-acceptance.test"))
	if e != nil {
		return e
	}
	parent, e := os.Stat(fmt.Sprintf("/proc/%d/exe", os.Getppid()))
	if e != nil || !os.SameFile(self, parent) {
		return errors.New("unowned parent executable")
	}
	if unix.SetNonblock(3, true) != nil {
		return errors.New("parent pipe nonblocking")
	}
	unix.CloseOnExec(3)
	f := os.NewFile(3, "native-parent")
	defer func() { _ = f.Close() }()
	raw := make([]byte, 513)
	n, e := unix.Read(3, raw)
	if e != nil || n > 512 || !bytes.Equal(raw[:n], acceptanceChildFrame(c, *acceptanceEndpoint, *acceptanceRole)) {
		return errors.New("parent control identity")
	}
	return nil
}

type nativeDeployment struct {
	Schema          string `json:"schema"`
	Profile         string `json:"profile"`
	VMConfigSHA256  string `json:"vm_config_sha256"`
	KernelRelease   string `json:"kernel_release"`
	OSReleaseSHA256 string `json:"os_release_sha256"`
	Architecture    string `json:"architecture"`
	CPUs            int    `json:"cpus"`
	MemoryTotalKB   uint64 `json:"memory_total_kb"`
	DaemonID        string `json:"daemon_id"`
	DockerVersion   string `json:"docker_version"`
	CgroupDriver    string `json:"cgroup_driver"`
	RuntimesSHA256  string `json:"runtimes_sha256"`
}

func acceptanceSystemRead(name string, limit int64) ([]byte, error) {
	f, e := os.Open(name)
	if e != nil {
		return nil, e
	}
	defer func() { _ = f.Close() }()
	b, e := io.ReadAll(io.LimitReader(f, limit+1))
	if e != nil || int64(len(b)) > limit {
		return nil, errors.New("system fact bound")
	}
	return b, nil
}
func acceptanceDeployment(ctx context.Context, c nativeAcceptanceConfig, d *acceptanceDocker) error {
	raw, e := acceptanceRead(filepath.Join(c.root(), "deployment.json"), 16384)
	if e != nil || acceptanceDigest(raw) != c.DeploymentSHA256 {
		return errors.New("deployment identity")
	}
	var expected nativeDeployment
	if acceptanceDecode(raw, 16384, &expected) != nil || expected.Schema != "phebs-typed-native-deployment-v1" || expected.Profile != "phebs-t451a" || !acceptanceHash(expected.VMConfigSHA256) || !acceptanceHash(expected.OSReleaseSHA256) || !acceptanceHash(expected.RuntimesSHA256) || expected.Architecture != "arm64" || expected.CPUs != 2 || expected.MemoryTotalKB == 0 || expected.DaemonID == "" || expected.DockerVersion == "" || expected.KernelRelease == "" || expected.CgroupDriver == "" {
		return errors.New("unfilled deployment")
	}
	kernel, e := acceptanceSystemRead("/proc/sys/kernel/osrelease", 256)
	if e != nil || strings.TrimSpace(string(kernel)) != expected.KernelRelease {
		return errors.New("kernel identity")
	}
	osrelease, e := acceptanceSystemRead("/etc/os-release", 16384)
	if e != nil || acceptanceDigest(osrelease) != expected.OSReleaseSHA256 {
		return errors.New("OS image identity")
	}
	memory, e := acceptanceSystemRead("/proc/meminfo", 16384)
	if e != nil {
		return e
	}
	var total uint64
	for _, line := range strings.Split(string(memory), "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			if _, e = fmt.Sscanf(line, "MemTotal: %d kB", &total); e != nil {
				return e
			}
		}
	}
	if total != expected.MemoryTotalKB || runtime.NumCPU() != expected.CPUs || runtime.GOARCH != expected.Architecture {
		return errors.New("VM geometry changed")
	}
	var info struct {
		ID, ServerVersion, CgroupDriver, KernelVersion string
		Runtimes                                       map[string]json.RawMessage
	}
	if e = d.get(ctx, "/info", &info); e != nil {
		return e
	}
	if info.ID != expected.DaemonID || info.ServerVersion != expected.DockerVersion || info.CgroupDriver != expected.CgroupDriver || info.KernelVersion != expected.KernelRelease || acceptanceDigest(acceptanceJSON(info.Runtimes)) != expected.RuntimesSHA256 {
		return errors.New("daemon/runtime identity changed")
	}
	return nil
}

// The installation contains one reviewed neutral repository/root only. These
// bounded point-sized census pages prove no child controls remain after drain.
func acceptanceControlsDrained(ctx context.Context, s *store.Surreal) (bool, int, error) {
	complete, tombstones := true, 0
	for _, kind := range []store.TypedIndexControlKind{store.TypedIndexRequests, store.TypedIndexPlans, store.TypedIndexAttempts, store.TypedIndexStates, store.TypedIndexCurrents, store.TypedIndexIntents} {
		page, e := s.ScanTypedIndexControls(ctx, kind, "", 3)
		if e != nil {
			return false, 0, e
		}
		if page.Next != "" {
			return false, 0, errors.New("unexpected retained control count")
		}
		for _, row := range page.Rows {
			if kind == store.TypedIndexRequests && row.Parent && row.ID == row.Root && row.State == "collecting" && row.Repository == acceptanceRepo {
				tombstones++
				continue
			}
			complete = false
		}
	}
	if tombstones > 1 {
		return false, 0, errors.New("unexpected retained tombstones")
	}
	return complete, tombstones, nil
}
func acceptanceSettledNoReplay(ctx context.Context, c nativeAcceptanceConfig, s *store.Surreal) error {
	before, e := s.GetGenerationSchedule(ctx, acceptanceRepo, store.TypedIndexScheduleStage)
	if e != nil || before.Running != 0 || before.Status != store.GenerationScheduleSettled && before.Status != store.GenerationScheduleActive {
		return errors.New("negative schedule not released or settled")
	}
	attempts, e := s.ScanTypedIndexControls(ctx, store.TypedIndexAttempts, "", 64)
	if e != nil {
		return e
	}
	_, fresh, e := acceptanceController(c, s)
	if e != nil {
		return e
	}
	if e = fresh.Reconcile(ctx); e != nil {
		return e
	}
	if e = fresh.Coordinator(ctx, store.Job{Kind: store.JobTypedIndex, Target: acceptanceRepo}); e != nil {
		return e
	}
	after, e := s.GetGenerationSchedule(ctx, acceptanceRepo, store.TypedIndexScheduleStage)
	if e != nil || !bytes.Equal(acceptanceJSON(before), acceptanceJSON(after)) {
		return errors.New("fresh runtime changed negative schedule")
	}
	next, e := s.ClaimGenerationChunk(ctx, store.GenerationResourceTypedIndex, "native-negative-replay")
	if e != nil && !errors.Is(e, store.ErrNotFound) {
		return e
	}
	if next != nil {
		if before.Status != store.GenerationScheduleActive || next.Generation != before.Generation || next.ScheduleDigest != before.Digest {
			return errors.New("unexpected replacement lease")
		}
		disposition, e := s.InspectTypedIndexDisposition(ctx, *next)
		if e != nil || disposition.State() != store.TypedIndexInterrupted {
			return errors.New("negative root replay not protected")
		}
		if e = fresh.Class().Handle(ctx, *next, fresh.Class().Budget); !store.IsTerminal(e) {
			return errors.New("negative root native replay allowed")
		}
		if e = s.FailGenerationChunk(ctx, *next, "neutral no-replay proof"); e != nil {
			return e
		}
		if e = fresh.Class().AfterSettlement(ctx, *next); e != nil {
			return e
		}
	} else if before.Status != store.GenerationScheduleSettled {
		return errors.New("released negative lease unavailable")
	}
	settled, e := s.GetGenerationSchedule(ctx, acceptanceRepo, store.TypedIndexScheduleStage)
	if e != nil || settled.Status != store.GenerationScheduleSettled || settled.Running != 0 || settled.Pending != 0 {
		return errors.New("negative replay settlement")
	}

	final, e := s.ScanTypedIndexControls(ctx, store.TypedIndexAttempts, "", 64)
	if e != nil || !bytes.Equal(acceptanceJSON(attempts), acceptanceJSON(final)) {
		return errors.New("fresh runtime changed negative attempts")
	}
	return nil
}

// These flags exist only in the test executable. No configuration is shipped.
var acceptanceConfigPath = flag.String("typed-native-config", "", "reviewed private neutral native config")
var acceptanceCase = flag.String("typed-native-case", "", "success, cancel, wall, hard-death, canary or dry-run")
var acceptanceRole = flag.String("typed-native-role", "", "owned controller child: run or recover")
var acceptanceEndpoint = flag.String("typed-native-endpoint", "", "parent-owned loopback fixture")
var acceptanceParent = flag.Int("typed-native-parent", 0, "exact parent PID")

const acceptanceBase = "/var/lib/phebs-typed-acceptance"

const acceptanceSource = "package lib\n\nfunc Answer() int { return 42 }\n"

func (c nativeAcceptanceConfig) root() string                { return filepath.Join(acceptanceBase, c.ID) }
func (c nativeAcceptanceConfig) caseRoot(name string) string { return filepath.Join(c.root(), name) }

func acceptanceProfile(ctx context.Context, c nativeAcceptanceConfig, raw, selectionRaw []byte) (typedindex.Profile, error) {
	p, e := typedindex.DecodeProfile(ctx, raw)
	if e != nil {
		return p, e
	}
	d := p.Definition()
	if acceptanceDigest(raw) != c.ProfileSHA256 || d.Schema != typedindex.ProfileSchema || d.Config != typedindex.ReducedConfig() || d.Policy != c.Policy || d.BundleDigest != c.InventorySHA256 || d.ImageDigest != c.ImageSHA256 || d.Tools.Planner.Digest != c.HelperSHA256 || d.Tools.Launcher.Digest != c.HelperSHA256 || d.Tools.Go.Digest != provider.GoDigest || d.Tools.Bazel.Digest != provider.BazelDigest || d.Tools.Indexer.Digest != provider.SCIPDigest || d.Tools.Driver.Digest != "sha256:f49a0ff4339e32cc699c6fbb5a80b9d8f936b3bfe6b08a924fa19495e35b2bfd" || d.Tools.RulesGo.Digest != "sha256:68af54cb97fbdee5e5e8fe8d210d15a518f9d62abfd71620c3eaff3b26a5ff86" || d.RCDigest != "" {
		return p, errors.New("profile differs from approved tools/policy")
	}
	var s provider.Selection
	if e = acceptanceDecode(selectionRaw, provider.MaxSelectionBytes, &s); e != nil {
		return p, e
	}
	if acceptanceDigest(selectionRaw) != c.SelectionSHA256 || s.Schema != provider.SelectionSchema || s.Source != c.Source || s.Module != acceptanceRepo || s.Remote != "https://"+acceptanceRepo || !reflect.DeepEqual(s.Roots, []string{"//lib:lib"}) || acceptanceDigest(acceptanceJSON(s.Targets)) != c.UniverseSHA256 {
		return p, errors.New("neutral selection")
	}
	return p, nil
}

type acceptanceKernel struct {
	MemoryMax string `json:"memory_max"`
	SwapMax   string `json:"memory_swap_max"`
	PidsMax   string `json:"pids_max"`
	CPU       string `json:"cpu_max"`
	OpenFiles string `json:"worker_open_files"`
}

func acceptanceCgroup(raw []byte) (string, error) {
	if bytes.ContainsAny(raw, "\r\x00") {
		return "", errors.New("cgroup control bytes")
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "0::/") {
		return "", errors.New("cgroup v2 identity")
	}
	name := strings.TrimPrefix(lines[0], "0::")
	if name == "/" || filepath.Clean(name) != name || strings.ContainsAny(name, "\x00\r\n") {
		return "", errors.New("cgroup path")
	}
	return name, nil
}
func acceptanceKernelLimits(supervisor, worker int) (acceptanceKernel, error) {
	var k acceptanceKernel
	raw, e := acceptanceSystemRead(fmt.Sprintf("/proc/%d/cgroup", supervisor), 4096)
	if e != nil {
		return k, e
	}
	name, e := acceptanceCgroup(raw)
	if e != nil {
		return k, e
	}
	child, e := acceptanceSystemRead(fmt.Sprintf("/proc/%d/cgroup", worker), 4096)
	if e != nil || !bytes.Equal(raw, child) {
		return k, errors.New("worker cgroup differs")
	}
	root := "/sys/fs/cgroup" + name
	for _, x := range []struct {
		name, want string
		value      *string
	}{{"memory.max", "4533092352", &k.MemoryMax}, {"memory.swap.max", "0", &k.SwapMax}, {"pids.max", "294", &k.PidsMax}, {"cpu.max", "200000 100000", &k.CPU}} {
		value, e := acceptanceSystemRead(filepath.Join(root, x.name), 128)
		if e != nil || strings.TrimSpace(string(value)) != x.want {
			return k, errors.New("kernel containment value")
		}
		*x.value = x.want
	}
	limits, e := acceptanceSystemRead(fmt.Sprintf("/proc/%d/limits", worker), 8192)
	if e != nil {
		return k, e
	}
	for _, line := range strings.Split(string(limits), "\n") {
		if strings.HasPrefix(line, "Max open files") {
			fields := strings.Fields(line)
			if len(fields) != 6 || fields[3] != "128" || fields[4] != "128" || fields[5] != "files" {
				return k, errors.New("worker descriptor limit")
			}
			k.OpenFiles = "128 128"
		}
	}
	if k.OpenFiles == "" {
		return k, errors.New("worker descriptor limit missing")
	}
	return k, nil
}
func TestNativeAcceptanceCgroup(t *testing.T) {
	for _, tc := range []struct {
		raw string
		ok  bool
	}{{"0::/system.slice/docker-owned.scope\n", true}, {"0::/", false}, {"0::/a/../b", false}, {"0::/a\n1:cpu:/a", false}, {"1:cpu:/a", false}, {"0::/a\r", false}} {
		_, e := acceptanceCgroup([]byte(tc.raw))
		if (e == nil) != tc.ok {
			t.Fatalf("%q: %v", tc.raw, e)
		}
	}
}

func TestNativeAcceptancePartialInvocation(t *testing.T) {
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	for _, arg := range []string{"-typed-native-role=run", "-typed-native-case=success", "-typed-native-endpoint=ws://127.0.0.1:1", "-typed-native-parent=1"} {
		t.Run(strings.Split(arg, "=")[0], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, exe, "-test.run=^TestTypedNativeAcceptance$", arg)
			out, err := command.CombinedOutput()
			if err == nil || len(out) > 4096 || !bytes.Contains(out, []byte("partial native invocation")) {
				t.Fatalf("partial invocation was not refused before preparation: %v %s", err, out)
			}
		})
	}
}

// Source oracle uses the frozen ASCII token span, not optional display metadata.
func acceptanceNeutralDefinition(index *scip.Index) bool {
	if index == nil || len(index.Documents) != 1 || index.Documents[0] == nil || index.Documents[0].RelativePath != "lib/lib.go" {
		return false
	}
	matches := 0
	for _, o := range index.Documents[0].Occurrences {
		if o == nil || o.Symbol == "" || o.SymbolRoles&int32(scip.SymbolRole_Definition) == 0 {
			continue
		}
		if span, ok := o.SourceRange(); ok && span.Start.Line == 2 && span.Start.Character == 5 && span.End.Line == 2 && span.End.Character == 11 {
			matches++
		}
	}
	return matches == 1
}

//nolint:staticcheck // The pinned scip-go producer emits the legacy range wire representation.
func TestNativeAcceptanceNeutralDefinition(t *testing.T) {
	for _, fault := range []string{"good", "long range", "path", "role", "range", "empty symbol", "extra document", "display only"} {
		t.Run(fault, func(t *testing.T) {
			occurrence := &scip.Occurrence{Range: []int32{2, 5, 11}, Symbol: "scip-go gomod example.invalid/phebs-native-neutral test lib/Answer().", SymbolRoles: int32(scip.SymbolRole_Definition)}
			doc := &scip.Document{RelativePath: "lib/lib.go", Occurrences: []*scip.Occurrence{occurrence}}
			index := &scip.Index{Documents: []*scip.Document{doc}}
			switch fault {
			case "long range":
				occurrence.Range = []int32{2, 5, 2, 11}
			case "path":
				doc.RelativePath = "other.go"
			case "role":
				occurrence.SymbolRoles = 0
			case "range":
				occurrence.Range = []int32{2, 6, 12}
			case "empty symbol":
				occurrence.Symbol = ""
			case "extra document":
				index.Documents = append(index.Documents, doc)
			case "display only":
				doc.Occurrences = nil
				doc.Symbols = []*scip.SymbolInformation{{DisplayName: "Answer"}}
			}
			if acceptanceNeutralDefinition(index) != (fault == "good" || fault == "long range") {
				t.Fatal("neutral oracle")
			}
		})
	}
}

func TestNativeAcceptanceWorkerTaskCensus(t *testing.T) {
	for _, tc := range []struct {
		name     string
		children []string
		want     int
	}{
		{"other-thread", []string{"", "1234"}, 1234}, {"duplicate", []string{"1234", "1234"}, 1234},
		{"absent", []string{"", ""}, 0}, {"multiple", []string{"1234", "1235"}, 0}, {"malformed", []string{"no"}, 0},
		{"invalid-pid", []string{"1"}, 0}, {"overflow", []string{strings.Repeat(" ", 4097)}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for i, children := range tc.children {
				dir := filepath.Join(root, strconv.Itoa(100+i))
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "children"), []byte(children), 0600); err != nil {
					t.Fatal(err)
				}
			}
			pid, err := acceptanceWorkerPID(root)
			if tc.want == 0 && err == nil || tc.want != 0 && (err != nil || pid != tc.want) {
				t.Fatal(pid, err)
			}
		})
	}
	root := t.TempDir()
	for i := 0; i < 257; i++ {
		if err := os.Mkdir(filepath.Join(root, strconv.Itoa(100+i)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := acceptanceWorkerPID(root); err == nil {
		t.Fatal("unbounded tasks")
	}
}

func TestNativeAcceptanceObservationSite(t *testing.T) {
	private := errors.New("/private/path credential")
	for _, site := range []string{"owner_load", "owner_compare", "recorded", "host_verify", "kernel", "/private/path credential"} {
		err := &acceptanceObservationError{site: site, cause: private}
		token := acceptanceObservationSite(err)
		if strings.Contains(token, "private") || !errors.Is(err, private) {
			t.Fatal("private cause lost or exposed")
		}
		if site == "/private/path credential" && token != "observe_unavailable" {
			t.Fatal("unknown site accepted")
		}
	}
	if acceptanceObservationSite(private) != "observe_unavailable" {
		t.Fatal("raw error classified")
	}
}
