package store

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/loopbackowner"
)

// childPassTestTimeout bounds lock waits and child startups in these tests.
const childPassTestTimeout = 2 * time.Minute

func TestResolveChildPassVolatileEngineSkipsLock(t *testing.T) {
	t.Parallel()
	pass, err := resolveChildPass(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if !validChildPass(pass) || pass == legacyRootPass {
		t.Fatalf("volatile engine resolved to %q, want a fresh random password", pass)
	}
}

func TestResolveChildPassRejectsNilContext(t *testing.T) {
	t.Parallel()
	if _, err := resolveChildPass(nil, t.TempDir()); err == nil { //nolint:staticcheck // Deliberate nil-context input verifies refusal.
		t.Fatal("resolveChildPass with nil context succeeded")
	}
}

// TestResolveChildPassPausedPublicationBlocksConcurrentResolve is the
// source-level proof for the publication race: publication is paused after
// the temp credential is fully written but before it is synced, a concurrent
// resolution must not observe or return anything while the lock is held, and
// an injected sync failure must refuse the first resolution without ever
// handing its uncommitted password to a child start.
func TestResolveChildPassPausedPublicationBlocksConcurrentResolve(t *testing.T) {
	// Uses the process-wide durability seam: not parallel.
	dataDir := t.TempDir()
	ctx := context.Background()

	entered := make(chan struct{})
	var enteredOnce sync.Once
	releasePause := make(chan struct{})
	// Unblock the paused publication on any early failure so no goroutine
	// stays wedged on the test seam.
	t.Cleanup(func() {
		select {
		case <-releasePause:
		default:
			close(releasePause)
		}
	})
	var syncCalls atomic.Int32
	var dirSyncCalls atomic.Int32
	restore := setChildPassSyncSeam(childPassSyncSeam{
		pauseBeforeFileSync: func() {
			enteredOnce.Do(func() { close(entered) })
			<-releasePause
		},
		syncFile: func(file *os.File) error {
			if syncCalls.Add(1) == 1 {
				return errors.New("injected temp sync failure")
			}
			return file.Sync()
		},
		syncDir: func(dir string) error {
			dirSyncCalls.Add(1)
			return fsyncDir(dir)
		},
	})
	defer restore()

	type outcome struct {
		pass string
		err  error
	}
	firstDone := make(chan outcome, 1)
	go func() {
		pass, err := resolveChildPass(ctx, dataDir)
		firstDone <- outcome{pass, err}
	}()

	select {
	case <-entered:
	case <-time.After(childPassTestTimeout):
		t.Fatal("first resolution never reached the publication pause")
	}

	// A concurrent resolution must block on the credential lock while the
	// first publication is paused: it may not return any password yet.
	secondDone := make(chan outcome, 1)
	go func() {
		pass, err := resolveChildPass(ctx, dataDir)
		secondDone <- outcome{pass, err}
	}()
	select {
	case res := <-secondDone:
		t.Fatalf("concurrent resolution returned while publication was paused: pass=%q err=%v", res.pass, res.err)
	case <-time.After(500 * time.Millisecond):
	}

	// Release the pause; the first publication's injected sync failure must
	// refuse it and clean up only its unpublished temp file.
	close(releasePause)
	var first outcome
	select {
	case first = <-firstDone:
	case <-time.After(childPassTestTimeout):
		t.Fatal("first resolution never finished after the pause was released")
	}
	if first.err == nil || !strings.Contains(first.err.Error(), "sync child password") {
		t.Fatalf("first resolution error = %v, want the injected sync failure", first.err)
	}
	if first.pass != "" {
		t.Fatalf("failed publication returned password %q: no child may start with an uncommitted credential", first.pass)
	}

	// The loser of the race adopts the winner: the second resolution
	// publishes its own credential durably and returns it.
	var second outcome
	select {
	case second = <-secondDone:
	case <-time.After(childPassTestTimeout):
		t.Fatal("second resolution never finished")
	}
	if second.err != nil {
		t.Fatalf("second resolution error = %v", second.err)
	}
	if !validChildPass(second.pass) {
		t.Fatalf("second resolution returned invalid password %q", second.pass)
	}

	// Exactly one durable publication completed: the temp sync ran for both
	// attempts, but the directory sync — the final durability step — ran
	// once, for the credential that was actually returned.
	if got := syncCalls.Load(); got != 2 {
		t.Fatalf("temp sync calls = %d, want 2 (one failed, one durable)", got)
	}
	if got := dirSyncCalls.Load(); got != 1 {
		t.Fatalf("directory sync calls = %d, want exactly 1 durable publication", got)
	}

	final, err := readChildPassFile(dataDir)
	if err != nil {
		t.Fatalf("read published credential: %v", err)
	}
	if final != second.pass {
		t.Fatal("published credential does not match the returned password")
	}
	info, err := os.Stat(filepath.Join(dataDir, localChildPassName))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("published credential mode = %v, %v; want 0600", info, err)
	}
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".surreal-child-pass.tmp.") {
			t.Fatalf("unpublished temp file %q survived a failed publication", entry.Name())
		}
	}
}

// TestResolveChildPassAdoptsInterruptedPublication covers a previous writer
// that published complete bytes but died before completing its syncs: the
// next resolution must validate the credential and re-sync the file and the
// directory before its password may start a child.
func TestResolveChildPassAdoptsInterruptedPublication(t *testing.T) {
	// Uses the process-wide durability seam: not parallel.
	dataDir := t.TempDir()
	pass, err := newSurrealChildPass()
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the interrupted writer: complete, valid bytes are visible
	// under the final name, but neither the file nor the directory was
	// synced afterwards.
	contents := `{"schema":"phebs-surreal-child-pass-v1","pass":"` + pass + `"}` + "\n"
	if err := os.WriteFile(filepath.Join(dataDir, localChildPassName), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var events []string
	restore := setChildPassSyncSeam(childPassSyncSeam{
		syncFile: func(file *os.File) error {
			mu.Lock()
			events = append(events, "file")
			mu.Unlock()
			return file.Sync()
		},
		syncDir: func(dir string) error {
			mu.Lock()
			events = append(events, "dir")
			mu.Unlock()
			return fsyncDir(dir)
		},
	})
	defer restore()

	adopted, err := resolveChildPass(context.Background(), dataDir)
	if err != nil {
		t.Fatalf("resolveChildPass: %v", err)
	}
	if adopted != pass {
		t.Fatal("interrupted publication was not adopted")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(events) != 2 || events[0] != "file" || events[1] != "dir" {
		t.Fatalf("durability events = %v, want [file dir] before child admission", events)
	}
	data, err := os.ReadFile(filepath.Join(dataDir, localChildPassName))
	if err != nil || string(data) != contents {
		t.Fatalf("adoption changed the credential bytes: %q, %v", data, err)
	}
}

// TestResolveChildPassIgnoresStaleTempFiles covers a previous writer that
// died before its rename: the leftover temp is owned by nobody, so the next
// resolution must neither adopt it nor delete it, and must publish its own
// credential instead.
func TestResolveChildPassIgnoresStaleTempFiles(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	stale := filepath.Join(dataDir, ".surreal-child-pass.tmp.12345")
	if err := os.WriteFile(stale, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	pass, err := resolveChildPass(context.Background(), dataDir)
	if err != nil {
		t.Fatalf("resolveChildPass: %v", err)
	}
	if !validChildPass(pass) {
		t.Fatalf("resolved invalid password %q", pass)
	}
	final, err := readChildPassFile(dataDir)
	if err != nil || final != pass {
		t.Fatalf("published credential = %q, %v; want the resolved password", final, err)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Fatalf("stale temp file was touched: %v", err)
	}
}

// TestResolveChildPassSyncsFileAndDirectoryBeforeReturn asserts the
// durability contract: the temp file is synced, then renamed, then the
// directory is synced — all before the password is returned. A directory
// sync failure preserves the renamed final credential and refuses startup;
// the next resolution durably adopts it.
func TestResolveChildPassSyncsFileAndDirectoryBeforeReturn(t *testing.T) {
	// Uses the process-wide durability seam: not parallel.
	dataDir := t.TempDir()

	var mu sync.Mutex
	var events []string
	record := func(name string) {
		mu.Lock()
		events = append(events, name)
		mu.Unlock()
	}
	restore := setChildPassSyncSeam(childPassSyncSeam{
		syncFile: func(file *os.File) error { record("file"); return file.Sync() },
		syncDir:  func(dir string) error { record("dir"); return fsyncDir(dir) },
	})
	defer restore()

	pass, err := resolveChildPass(context.Background(), dataDir)
	if err != nil {
		t.Fatalf("resolveChildPass: %v", err)
	}
	mu.Lock()
	ordered := append([]string(nil), events...)
	mu.Unlock()
	if len(ordered) != 2 || ordered[0] != "file" || ordered[1] != "dir" {
		t.Fatalf("durability events = %v, want [file dir] in order before the password is returned", ordered)
	}
	if final, err := readChildPassFile(dataDir); err != nil || final != pass {
		t.Fatalf("published credential = %q, %v; want the returned password", final, err)
	}

	// A directory sync failure after the rename is an uncertain publication:
	// the final credential must be preserved and startup refused.
	otherDir := t.TempDir()
	var dirCalls atomic.Int32
	restore()
	restoreFailDir := setChildPassSyncSeam(childPassSyncSeam{
		syncDir: func(dir string) error {
			if dirCalls.Add(1) == 1 {
				return errors.New("injected directory sync failure")
			}
			return fsyncDir(dir)
		},
	})
	defer restoreFailDir()
	if _, err := resolveChildPass(context.Background(), otherDir); err == nil ||
		!strings.Contains(err.Error(), "sync child password directory") {
		t.Fatalf("resolveChildPass error = %v, want the injected directory sync failure", err)
	}
	preserved, err := readChildPassFile(otherDir)
	if err != nil {
		t.Fatalf("uncertain publication deleted the final credential: %v", err)
	}
	if !validChildPass(preserved) {
		t.Fatalf("preserved credential is invalid: %q", preserved)
	}

	// The next resolution validates the preserved credential and durably
	// adopts it instead of replacing it.
	again, err := resolveChildPass(context.Background(), otherDir)
	if err != nil {
		t.Fatalf("resolveChildPass after uncertain publication: %v", err)
	}
	if again != preserved {
		t.Fatal("uncertain publication was replaced instead of adopted")
	}
	if got := dirCalls.Load(); got != 2 {
		t.Fatalf("directory sync calls = %d, want 2 (one failed, one adopting)", got)
	}
}

// TestResolveChildPassConcurrentFirstStartsAgree proves concurrent first
// starts serialize on the credential lock: they all return the one published
// password and none replaces or deletes the credential another startup
// selected.
func TestResolveChildPassConcurrentFirstStartsAgree(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dataDir := t.TempDir()
	const racers = 16
	results := make([]string, racers)
	errs := make([]error, racers)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = resolveChildPass(ctx, dataDir)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("racer %d: %v", i, err)
		}
		if results[i] != results[0] {
			t.Fatalf("racer %d resolved %q, want the single published password", i, results[i])
		}
	}
	before, err := os.ReadFile(filepath.Join(dataDir, localChildPassName))
	if err != nil {
		t.Fatal(err)
	}
	// A second wave against the now-existing credential must adopt it
	// without replacing or deleting it.
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = resolveChildPass(ctx, dataDir)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("second-wave racer %d: %v", i, err)
		}
		if results[i] != results[0] {
			t.Fatalf("second-wave racer %d changed the credential", i)
		}
	}
	after, err := os.ReadFile(filepath.Join(dataDir, localChildPassName))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("concurrent resolutions replaced the selected credential")
	}
	info, err := os.Stat(filepath.Join(dataDir, localChildPassName))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("credential mode = %v, %v; want 0600", info, err)
	}
}

// TestResolveChildPassMissingFileOnExistingDatabaseNeedsVerify asserts that a
// missing password file on a genuinely initialized database is never treated
// as proof of a legacy database: resolution reports errChildPassLegacyVerify
// instead of guessing root or regenerating a random password. The database
// directory holds content here, so the empty-directory fresh path does not
// apply.
func TestResolveChildPassMissingFileOnExistingDatabaseNeedsVerify(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "db")
	if err := os.MkdirAll(dbPath, 0o700); err != nil {
		t.Fatal(err)
	}
	// Simulate a genuinely initialized database: any directory entry may
	// belong to a database initialized with an unknown password.
	if err := os.WriteFile(filepath.Join(dbPath, "MANIFEST-000001"), []byte("manifest"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveChildPass(ctx, dataDir); !errors.Is(err, errChildPassLegacyVerify) {
		t.Fatalf("resolveChildPass = %v, want errChildPassLegacyVerify", err)
	}
	// Nothing was persisted or guessed.
	if _, err := os.Lstat(filepath.Join(dataDir, localChildPassName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("resolution wrote a credential file while refusing to guess: %v", err)
	}
}

// surrealTestBinary returns the pinned SurrealDB binary for real-engine
// credential tests, or skips when it cannot be found.
func surrealTestBinary(t *testing.T) string {
	t.Helper()
	if path := os.Getenv("PHEBS_SURREAL"); path != "" {
		return path
	}
	for _, candidate := range []string{"/tmp/phebs-surreal/surreal"} {
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate
		}
	}
	t.Skip("pinned SurrealDB binary not available for real-engine credential tests")
	return ""
}

// rawSurrealChildForTest is the internal-test analogue of the production
// supervised child: exactly one unconditional cleanup owner per child. The
// owner requests shutdown (SIGINT), waits a bounded grace period, escalates
// to SIGKILL when the child ignores the request, and then joins the Wait
// goroutine exactly once. The owner is registered with t.Cleanup immediately
// after Start, so every failure path — including a t.Fatalf before any
// explicit stop — still reaps the child. The cleanup runs before the test's
// TempDir removal (LIFO: callers create the data directory before starting
// the child), so the child never outlives its database directory. Captured
// output may only be read after the Wait goroutine is joined (waitResult):
// exec's output-copying goroutines finish before Wait returns, and reading
// the buffer earlier races them.
type rawSurrealChildForTest struct {
	cmd       *exec.Cmd
	output    *bytes.Buffer
	grace     time.Duration
	waited    chan error
	done      chan struct{}
	stopOnce  sync.Once
	awaitOnce sync.Once
	waitErr   error
}

// startRawSurrealChildForTest starts the raw child and registers its single
// unconditional cleanup owner. The caller must not start, wait on, signal,
// or kill the process itself; every shutdown goes through child.stop
// (explicitly or via the registered cleanup).
func startRawSurrealChildForTest(t *testing.T, cmd *exec.Cmd, grace time.Duration) *rawSurrealChildForTest {
	t.Helper()
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatalf("start raw test child: %v", err)
	}
	child := &rawSurrealChildForTest{
		cmd: cmd, output: &output, grace: grace,
		waited: make(chan error, 1), done: make(chan struct{}),
	}
	go func() { child.waited <- cmd.Wait() }()
	t.Cleanup(child.stop)
	return child
}

// await joins the Wait goroutine exactly once. The captured output is fully
// copied when it returns.
func (c *rawSurrealChildForTest) await() {
	c.awaitOnce.Do(func() {
		c.waitErr = <-c.waited
		close(c.done)
	})
}

// waitResult joins the Wait goroutine and returns its fully-copied captured
// output plus the child's exit result. Call only after the child has been
// asked to stop; otherwise it blocks until the registered cleanup stops it.
func (c *rawSurrealChildForTest) waitResult() (output string, waitErr error) {
	c.await()
	return c.output.String(), c.waitErr
}

// stop is the single unconditional cleanup owner: request shutdown, wait a
// bounded grace period, kill if necessary, then join the Wait goroutine.
func (c *rawSurrealChildForTest) stop() {
	c.stopOnce.Do(func() {
		go c.await()
		if c.cmd.Process != nil {
			_ = c.cmd.Process.Signal(os.Interrupt)
		}
		select {
		case <-c.done:
		case <-time.After(c.grace):
			_ = c.cmd.Process.Kill()
			// Cleanup cannot release the database directory before Wait has
			// joined, even if shutdown outlives the grace period.
			<-c.done
		}
	})
}

// waitRawSurrealChildHealthy polls the loopback /health endpoint until ctx
// ends. Each request carries its own client timeout: the overall deadline
// cannot bound a blocked bare http.Get, so the loop never issues one.
func waitRawSurrealChildHealthy(ctx context.Context, url string) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil) //nolint:gosec // loopback test child, constructed addr
		if err != nil {
			return false
		}
		resp, err := client.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return true
			}
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// startRawSurrealOnLoopbackForTest starts the raw child that command builds
// for a freshly reserved loopback address and returns that address once the
// child serves /health on it. Another process can take the reserved port
// before the child binds it; where listeners can be attributed, the health
// answer is trusted only from the child's own listener, and a port proven
// lost moves the start to a fresh port, at most localPortAttempts times. The
// child has exactly one unconditional cleanup owner, registered inside
// startRawSurrealChildForTest before readiness is awaited: whatever fails
// below, the child is asked to stop, killed after a bounded grace if it
// ignores the request, and its Wait goroutine is joined before the test — and
// the temporary data directory — goes away.
func startRawSurrealOnLoopbackForTest(
	t *testing.T, ctx context.Context, grace time.Duration, command func(addr string) *exec.Cmd,
) (string, *rawSurrealChildForTest) {
	t.Helper()
	for attempt := 1; ; attempt++ {
		addr, err := reserveLoopbackAddr()
		if err != nil {
			t.Fatal(err)
		}
		child := startRawSurrealChildForTest(t, command(addr), grace)
		err = loopbackowner.Await(ctx, child.cmd.Process.Pid, addr)
		if errors.Is(err, loopbackowner.ErrForeign) && attempt < localPortAttempts {
			child.stop()
			continue
		}
		if err == nil || errors.Is(err, loopbackowner.ErrUnsupported) {
			if waitRawSurrealChildHealthy(ctx, "http://"+addr+"/health") {
				return addr, child
			}
			err = errors.New("never became healthy")
		}
		child.stop()
		// Output is read only after the Wait goroutine is joined: exec's
		// output-copying goroutines finish before Wait returns.
		output, _ := child.waitResult()
		t.Fatalf("raw surreal child on %s: %v\n%s", addr, err, output)
	}
}

// startRawSurrealForTest initializes a database with the given root password
// by running the real binary directly and waits until it is healthy. It
// returns a stop func that fully stops the child; callers must stop it before
// opening the same database through startOwnedEngine, since SurrealKV holds
// an exclusive lock on the database directory.
func startRawSurrealForTest(t *testing.T, binary, dbPath, pass string) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), childPassTestTimeout)
	defer cancel()
	_, child := startRawSurrealOnLoopbackForTest(t, ctx, 30*time.Second, func(addr string) *exec.Cmd {
		cmd := exec.Command(binary, "start",
			"--bind", addr,
			"--user", "root",
			"--log", "warn",
			"surrealkv:"+dbPath,
		)
		// The root password travels via SURREAL_PASS, never on argv.
		cmd.Env = append(os.Environ(), "SURREAL_USER=root", "SURREAL_PASS="+pass)
		return cmd
	})
	return child.stop
}

// TestStartOwnedEngineLegacyDatabaseVerifiesBySignIn is the real-engine proof
// for genuine legacy compatibility: a database initialized with the
// historical root/root credential is verified by signing in, and only then
// is the legacy password persisted under the credential lock.
func TestStartOwnedEngineLegacyDatabaseVerifiesBySignIn(t *testing.T) {
	binary := surrealTestBinary(t)
	t.Setenv("PHEBS_SURREAL", binary)
	ctx, cancel := context.WithTimeout(context.Background(), childPassTestTimeout)
	defer cancel()

	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "db")
	stopRaw := startRawSurrealForTest(t, binary, dbPath, legacyRootPass)
	stopRaw()

	runtime, engine, err := startOwnedEngine(ctx, "surrealkv:"+dbPath)
	if engine != nil {
		// Registered immediately: the assertions below can fatal before
		// any explicit stop, and engine.stop is idempotent.
		t.Cleanup(engine.stop)
	}
	if err != nil {
		t.Fatalf("startOwnedEngine on legacy database: %v", err)
	}
	if runtime.Pass != legacyRootPass {
		t.Fatalf("legacy database runtime password = %q, want the verified legacy root password", runtime.Pass)
	}
	persisted, err := readChildPassFile(dataDir)
	if err != nil {
		t.Fatalf("read published legacy credential: %v", err)
	}
	if persisted != legacyRootPass {
		t.Fatalf("published legacy credential = %q, want the legacy root password", persisted)
	}
	engine.stop()

	// A later start reuses the persisted credential without another probe.
	runtime, engine, err = startOwnedEngine(ctx, "surrealkv:"+dbPath)
	if engine != nil {
		t.Cleanup(engine.stop)
	}
	if err != nil {
		t.Fatalf("second startOwnedEngine on legacy database: %v", err)
	}
	if runtime.Pass != legacyRootPass {
		t.Fatalf("second start password = %q, want the persisted legacy root password", runtime.Pass)
	}
}

// TestStartOwnedEngineRefusesDatabaseWithLostCredential proves the
// never-regenerate rule end to end: a database initialized with a random
// password but missing its password file fails the legacy sign-in probe, so
// startup is refused and no credential is written that could lock the
// database out.
func TestStartOwnedEngineRefusesDatabaseWithLostCredential(t *testing.T) {
	binary := surrealTestBinary(t)
	t.Setenv("PHEBS_SURREAL", binary)
	ctx, cancel := context.WithTimeout(context.Background(), childPassTestTimeout)
	defer cancel()

	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "db")
	lost, err := newSurrealChildPass()
	if err != nil {
		t.Fatal(err)
	}
	stopRaw := startRawSurrealForTest(t, binary, dbPath, lost)
	stopRaw()

	_, engine, err := startOwnedEngine(ctx, "surrealkv:"+dbPath)
	if engine != nil {
		// Unexpected success: the assertion below fatals before any
		// explicit stop could run, so own the engine now.
		t.Cleanup(engine.stop)
	}
	if err == nil || !strings.Contains(err.Error(), "refusing child startup") {
		t.Fatalf("startOwnedEngine error = %v, want a refusal to start with an unverified credential", err)
	}
	if _, statErr := os.Lstat(filepath.Join(dataDir, localChildPassName)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("refused startup wrote a credential file for a database with an unknown password")
	}
}

// TestResolveChildPassDatabaseDirectoryEmptiness is the table-driven
// regression for the empty/precreated-directory defect: an empty db/
// directory is proven uninitialized and takes the fresh path — its
// credential is published under the credential lock before any child may
// start it — while any directory content keeps the legacy-verification
// refusal so a potentially-initializing password is never discarded.
func TestResolveChildPassDatabaseDirectoryEmptiness(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tests := []struct {
		name      string
		setup     func(t *testing.T, dataDir string)
		wantFresh bool
	}{
		{
			name:      "no database directory",
			setup:     func(t *testing.T, dataDir string) {},
			wantFresh: true,
		},
		{
			name: "empty database directory",
			setup: func(t *testing.T, dataDir string) {
				if err := os.MkdirAll(filepath.Join(dataDir, "db"), 0o700); err != nil {
					t.Fatal(err)
				}
			},
			wantFresh: true,
		},
		{
			name: "database directory with a file",
			setup: func(t *testing.T, dataDir string) {
				if err := os.MkdirAll(filepath.Join(dataDir, "db"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dataDir, "db", "MANIFEST-000001"), []byte("manifest"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "database directory with a subdirectory",
			setup: func(t *testing.T, dataDir string) {
				if err := os.MkdirAll(filepath.Join(dataDir, "db", "wal"), 0o700); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dataDir := t.TempDir()
			tt.setup(t, dataDir)
			pass, err := resolveChildPass(ctx, dataDir)
			if !tt.wantFresh {
				if !errors.Is(err, errChildPassLegacyVerify) {
					t.Fatalf("resolveChildPass = %q, %v; want errChildPassLegacyVerify", pass, err)
				}
				if _, statErr := os.Lstat(filepath.Join(dataDir, localChildPassName)); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatal("legacy-verification refusal wrote a credential file")
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveChildPass: %v", err)
			}
			if !validChildPass(pass) || pass == legacyRootPass {
				t.Fatalf("resolved password %q is not a fresh random credential", pass)
			}
			persisted, err := readChildPassFile(dataDir)
			if err != nil {
				t.Fatalf("read published credential: %v", err)
			}
			if persisted != pass {
				t.Fatal("the fresh credential was not persisted before it was returned")
			}
		})
	}
}

// TestStartOwnedEngineEmptyDatabaseDirectoryInitializesFresh is the
// real-engine regression for the discarded-throwaway defect: a precreated
// but empty db/ directory opens cleanly with a persisted fresh credential
// (not the legacy root password), and a hard kill after healthy startup
// converges on restart using the same persisted credential. It does not
// inject an interruption before database initialization completes.
func TestStartOwnedEngineEmptyDatabaseDirectoryInitializesFresh(t *testing.T) {
	binary := surrealTestBinary(t)
	t.Setenv("PHEBS_SURREAL", binary)
	ctx, cancel := context.WithTimeout(context.Background(), childPassTestTimeout)
	defer cancel()

	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "db")
	if err := os.MkdirAll(dbPath, 0o700); err != nil {
		t.Fatal(err)
	}

	runtime, engine, err := startOwnedEngine(ctx, "surrealkv:"+dbPath)
	if engine != nil {
		t.Cleanup(engine.stop)
	}
	if err != nil {
		t.Fatalf("startOwnedEngine on empty database directory: %v", err)
	}
	if !validChildPass(runtime.Pass) || runtime.Pass == legacyRootPass {
		t.Fatalf("empty database initialized with %q, want a fresh random credential", runtime.Pass)
	}
	persisted, err := readChildPassFile(dataDir)
	if err != nil {
		t.Fatalf("read published credential: %v", err)
	}
	if persisted != runtime.Pass {
		t.Fatal("empty-database startup did not persist the credential it used")
	}
	// Hard-kill the healthy child instead of stopping it gracefully: the
	// restart must converge on the persisted credential.
	if err := engine.process.Kill(); err != nil {
		t.Fatalf("kill child: %v", err)
	}
	engine.stop()

	runtime, engine, err = startOwnedEngine(ctx, "surrealkv:"+dbPath)
	if engine != nil {
		t.Cleanup(engine.stop)
	}
	if err != nil {
		t.Fatalf("startOwnedEngine after healthy-start hard kill: %v", err)
	}
	if runtime.Pass != persisted {
		t.Fatalf("restart password = %q, want the persisted credential %q", runtime.Pass, persisted)
	}
	if again, err := readChildPassFile(dataDir); err != nil || again != persisted {
		t.Fatalf("restart changed the persisted credential: %q, %v", again, err)
	}
}

// TestStartOwnedEngineRefusesLegacyVerifyInSelectedOwnerMode proves the
// selected-owner fencing: with an authenticated process SDK owner present,
// legacy verification is refused before any child starts and before any
// probe connection is made, so the raw probe can never bypass the owner's
// final-send, strict-reply, failure-latching, and completion checks.
// Ordinary non-selected migration stays supported and is covered by
// TestStartOwnedEngineLegacyDatabaseVerifiesBySignIn.
func TestStartOwnedEngineRefusesLegacyVerifyInSelectedOwnerMode(t *testing.T) {
	binary := surrealTestBinary(t)
	t.Setenv("PHEBS_SURREAL", binary)
	ctx, cancel := context.WithTimeout(context.Background(), childPassTestTimeout)
	defer cancel()

	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "db")
	stopRaw := startRawSurrealForTest(t, binary, dbPath, legacyRootPass)
	stopRaw()

	// Uses the process-wide owner seam: not parallel.
	restore := setChildProcessOwner(func() (*storeCallOwner, error) { return &storeCallOwner{}, nil })
	defer restore()

	_, engine, err := startOwnedEngine(ctx, "surrealkv:"+dbPath)
	if engine != nil {
		t.Cleanup(engine.stop)
	}
	if !errors.Is(err, errLegacyVerifySelectedOwner) {
		t.Fatalf("startOwnedEngine error = %v, want selected-owner legacy refusal", err)
	}
	if _, statErr := os.Lstat(filepath.Join(dataDir, localChildPassName)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("refused legacy verification wrote a credential file")
	}
}
