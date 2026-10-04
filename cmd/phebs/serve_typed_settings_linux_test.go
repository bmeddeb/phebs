//go:build linux

package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/auth"
	"github.com/bmeddeb/phebs/internal/codenav"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/store"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
	"github.com/bmeddeb/phebs/internal/typedindex"
)

var typedSettingsBrowserUI = flag.String("typed-settings-browser-ui", "", "absolute built UI directory for the opt-in authenticated rendered Settings fixture")

// An external browser drives the unchanged UI. Commands only pause or advance
// real fixture state; they do not supply API responses. Sealed publication bytes
// remain fixture-authored, with no native indexer or public restore round trip.
func TestTypedSettingsBrowserLinux(t *testing.T) {
	if *typedSettingsBrowserUI == "" {
		t.Skip("explicit -typed-settings-browser-ui directory required")
	}
	dist := *typedSettingsBrowserUI
	resolved, err := filepath.EvalSymlinks(dist)
	if err != nil || !filepath.IsAbs(dist) || filepath.Clean(dist) != dist || resolved != dist {
		t.Fatal("built UI directory must be absolute, canonical and resolved", err)
	}
	index, err := os.Lstat(filepath.Join(dist, "index.html"))
	if err != nil || !index.Mode().IsRegular() || index.Size() == 0 || index.Size() > 2<<20 {
		t.Fatal("built UI index is not a bounded regular file", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	defer cancel()
	f := typedNavigationFixtureFor(t, typedNavigationServer(t))
	commit := typedSettingsBrowserMirror(ctx, t, f)
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	check(f.state.SetRepoIndexed(ctx, f.repo, commit, time.Now()))
	before, err := f.state.ReadTypedIndexOperator(ctx, f.repo)
	check(err)
	owners, err := dispatchadmission.NewOwners(ctx, dispatchadmission.OwnerLimits{Owners: 1, Requests: 1})
	check(err)
	const adminEmail, ordinaryEmail = "admin@settings.invalid", "ordinary@settings.invalid"
	var secret [32]byte
	_, err = rand.Read(secret[:])
	check(err)
	password := hex.EncodeToString(secret[:])
	var auditFailed atomic.Bool
	audit := func(ctx context.Context, event store.AuditEvent) {
		if principal, ok := auth.PrincipalFromContext(ctx); ok && principal.User != nil {
			event.ActorID, event.ActorEmail = principal.User.ID, principal.User.Email
			event.AuthMethod = principal.AuthMethod
		}
		if err := f.state.AppendAuditEvent(ctx, event); err != nil {
			auditFailed.Store(true)
		}
	}
	authService, err := auth.New(ctx, auth.Options{
		Store: f.state, Config: config.Auth{BootstrapUser: config.BootstrapUser{
			Email: adminEmail, DisplayName: "Neutral administrator", Password: password,
		}}, Owners: owners, ArgonConcurrency: 1, Audit: audit,
	})
	check(err)
	t.Cleanup(func() { cancel(); authService.WaitCleanup() })
	admin, err := f.state.GetUserByEmail(ctx, adminEmail)
	check(err)
	if admin == nil || !admin.IsAdmin || admin.PasswordHash == "" {
		t.Fatal("real administrator bootstrap missing")
	}
	ordinary, err := f.state.CreateUser(ctx, store.User{
		ID: "settings-ordinary", Email: ordinaryEmail, NormalizedEmail: ordinaryEmail,
		DisplayName: "Neutral reader", PasswordHash: admin.PasswordHash,
	})
	check(err)
	if ordinary == nil || ordinary.IsAdmin {
		t.Fatal("ordinary fixture user has administrator authority")
	}
	resolver, err := newTypedCodeNavigationResolver(f.state, f.base)
	check(err)
	navigation := codenav.New(codenav.Options{DataDir: f.base, RoutedResolver: resolver})
	monitor, err := lifecycle.NewStatusMonitor(false, []lifecycle.Owner{lifecycle.JobOwnerImpl{Store: f.state}})
	check(err)
	server := func(available bool) *httptest.Server {
		options := api.Options{
			Version: "neutral-settings-fixture", Store: f.state, DataDir: f.base, CodeNav: navigation,
			TypedIndexAvailable: available, AuditRecord: audit, AuditLog: f.state,
			LifecycleStatusSource: func(context.Context) lifecycle.Status { return monitor.Snapshot() },
			IsAdmin: func(ctx context.Context) bool {
				principal, ok := auth.PrincipalFromContext(ctx)
				return ok && principal.IsAdmin
			},
			Principal: func(ctx context.Context) string {
				principal, ok := auth.PrincipalFromContext(ctx)
				if !ok || principal.User == nil {
					return ""
				}
				return "user:" + principal.User.ID
			},
		}
		handler := newHTTPHandler(authService, api.New(options), http.NotFoundHandler(), http.NotFoundHandler(), http.FileServerFS(os.DirFS(dist)), config.Server{})
		out := httptest.NewUnstartedServer(handler)
		out.Config.BaseContext = func(net.Listener) context.Context { return ctx }
		out.Config.ReadHeaderTimeout = 5 * time.Second
		out.Config.ReadTimeout = 10 * time.Second
		out.Config.WriteTimeout = 30 * time.Second
		out.Config.IdleTimeout = 10 * time.Second
		out.StartTLS()
		t.Cleanup(out.Close)
		return out
	}
	installed, dark := server(true), server(false)
	deadline, _ := ctx.Deadline()
	// Reopen the launcher's pipe with an independent nonblocking description;
	// inherited SSH stdin itself is not registered with Go's poller. This keeps
	// deadline-bound reads on this test goroutine without a reader goroutine.
	input, err := os.OpenFile("/proc/self/fd/0", os.O_RDONLY|syscall.O_NONBLOCK, 0)
	check(err)
	t.Cleanup(func() { _ = input.Close() })
	check(input.SetReadDeadline(deadline))
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 4096)
	emit := func(event map[string]any) {
		t.Helper()
		raw, err := json.Marshal(event)
		check(err)
		if len(raw) > 4096 {
			t.Fatal("fixture event exceeds protocol bound")
		}
		_, err = fmt.Fprintf(os.Stdout, "PHEBS_SETTINGS %s\n", raw)
		check(err)
	}
	expect := func(want string) {
		t.Helper()
		if !scanner.Scan() {
			t.Fatal("fixture command missing", want, scanner.Err())
		}
		var command struct {
			Command string `json:"command"`
		}
		decoder := json.NewDecoder(strings.NewReader(scanner.Text()))
		decoder.DisallowUnknownFields()
		check(decoder.Decode(&command))
		if decoder.Decode(new(any)) != io.EOF || command.Command != want {
			t.Fatal("unexpected fixture command", want)
		}
		check(ctx.Err())
	}
	jobs := func(want int) []store.Job {
		t.Helper()
		page, err := f.state.ListJobsPage(ctx, store.JobPageQuery{Kind: store.JobTypedIndex, Limit: 4})
		check(err)
		if len(page.Jobs) != want || page.Next != nil {
			t.Fatal("unexpected coordinator census", len(page.Jobs), want)
		}
		return page.Jobs
	}
	port := func(s *httptest.Server) int { return s.Listener.Addr().(*net.TCPAddr).Port }
	// The launcher keeps this credentials-bearing readiness event in a private
	// pipe; receipts contain only source-free assertions, never credentials.
	emit(map[string]any{"event": "ready", "port": port(installed), "darkPort": port(dark), "repo": f.repo,
		"adminEmail": adminEmail, "ordinaryEmail": ordinaryEmail, "password": password,
		"commit": commit, "sourcePath": "unicode.go", "sourceBytes": len("😀x\nx\n")})
	var queuedDigest, coordinatorID string
	var current typedindex.PublicationPointer
	for _, command := range []string{"preview", "queued", "queued", "publish", "failed", "canceled", "stale", "restore", "finish"} {
		expect(command)
		event := map[string]any{"event": "done", "command": command}
		switch command {
		case "preview":
			after, err := f.state.ReadTypedIndexOperator(ctx, f.repo)
			check(err)
			if !reflect.DeepEqual(before, after) || after.Status.Desired != "" {
				t.Fatal("browser preview or refusal changed typed authority")
			}
			jobs(0)
			event["state"] = "absent"
		case "queued":
			status, err := f.state.GetTypedIndexStatus(ctx, f.repo)
			check(err)
			page := jobs(1)
			if status.Desired == "" || status.Current != nil || page[0].Status != store.StatusPending || page[0].Attempts != 0 {
				t.Fatal("exact browser retry did not retain one pending request")
			}
			if queuedDigest != "" && (queuedDigest != status.Desired || coordinatorID != page[0].ID) {
				t.Fatal("transport retry changed desired authority or coordinator")
			}
			queuedDigest, coordinatorID = status.Desired, page[0].ID
			event["state"], event["requestDigest"] = "planning", queuedDigest
		case "publish":
			publication := f.publishQueuedSteps(t, true, func(state string) {
				op, err := f.state.ReadTypedIndexOperator(ctx, f.repo)
				check(err)
				stage := map[string]store.TypedIndexStage{"planning": "", "indexing": store.TypedExecution, "validating": store.TypedValidation, "publishing": store.TypedPublication}[state]
				if op.Coordinator != store.StatusDone || op.Status.Stage != stage || op.Schedule == nil {
					t.Fatal("durable fixture stage differs from reader checkpoint", state, op.Status.Stage)
				}
				emit(map[string]any{"event": "stage", "state": state})
				expect("continue")
			})
			if publication.parent.Digest() != queuedDigest || publication.parent.Request().Source.Commit != commit {
				t.Fatal("published authority differs from exact browser request")
			}
			current, err = f.state.ResolveTypedIndexCurrent(ctx, f.repo)
			check(err)
			page := jobs(1)
			if page[0].ID != coordinatorID || page[0].Status != store.StatusDone || current.RootDigest != publication.bundle.RootDigest() {
				t.Fatal("published current or coordinator differs")
			}
			reference, err := typedindex.GeneratedPath(f.unit, "reference.go")
			check(err)
			event["state"], event["definitionPath"], event["referencePath"] = "current", f.document, reference
			event["commit"], event["parentDigest"], event["executionDigest"] = commit, queuedDigest, publication.execution.Digest()
			event["members"], event["generatedDocuments"], event["generatedSourceBytes"] = 2, 2, 2*len("😀x\nx\n")
		case "failed", "canceled":
			op, err := f.state.ReadTypedIndexOperator(ctx, f.repo)
			check(err)
			purpose := typedindex.Canary
			if command == "canceled" {
				purpose = typedindex.DryRun
			}
			if !op.DesiredFresh || op.Desired.Purpose != purpose || op.Coordinator != store.StatusPending {
				t.Fatal("browser did not enqueue the expected fresh purpose", purpose)
			}
			if command == "failed" {
				jobs(2)
				job, err := f.state.ClaimJob(ctx, store.JobTypedIndex, "settings-failed-fixture")
				check(err)
				if job == nil {
					t.Fatal("failure coordinator missing")
				}
				check(f.state.SetJobStatus(ctx, *job, store.StatusRunning, ""))
				check(f.state.SetJobStatus(ctx, *job, store.StatusFailed, "private /unretained/driver-output"))
			} else {
				jobs(3)
				check(f.state.CancelTypedIndex(ctx, f.repo, op.Status.Desired))
				count, err := f.state.CancelPendingJobs(ctx, store.JobTypedIndex, f.repo)
				check(err)
				if count != 1 {
					t.Fatal("cancel did not settle the one pending coordinator", count)
				}
			}
			op, err = f.state.ReadTypedIndexOperator(ctx, f.repo)
			check(err)
			retained, err := f.state.ResolveTypedIndexCurrent(ctx, f.repo)
			check(err)
			want := store.StatusFailed
			if command == "canceled" {
				want = store.StatusCanceled
			}
			if op.Coordinator != want || !reflect.DeepEqual(retained, current) || command == "canceled" && !op.Status.Canceled {
				t.Fatal("terminal fixture state lost prior current", command)
			}
			event["state"] = command
		case "stale":
			check(f.state.SetRepoIndexed(ctx, f.repo, strings.Repeat("b", 40), time.Now()))
			op, err := f.state.ReadTypedIndexOperator(ctx, f.repo)
			check(err)
			if !op.Status.Stale || op.DesiredFresh || op.Revision == before.Revision {
				t.Fatal("source drift did not fence the old selection")
			}
			event["state"] = "stale"
		case "restore":
			check(f.state.ClearTypedIndexForRestore(ctx))
			status, err := f.state.GetTypedIndexStatus(ctx, f.repo)
			check(err)
			if !status.RestoreRequired || status.Desired != "" || status.Current != nil {
				t.Fatal("restore clearing retained readable managed authority")
			}
			jobs(0)
			event["state"] = "stale"
		case "finish":
			status, err := f.state.GetTypedIndexStatus(ctx, f.repo)
			check(err)
			if !status.RestoreRequired || status.Desired != "" || status.Current != nil || auditFailed.Load() {
				t.Fatal("final restored authority or real audit failed")
			}
			jobs(0)
			auditEvents, err := f.state.ListAuditEvents(ctx, 0, 32)
			check(err)
			enqueues := 0
			for _, event := range auditEvents {
				if event.Action == "enqueue-code-navigation-indexing" && event.Status == http.StatusOK {
					if event.ActorID != admin.ID || event.AuthMethod != "session" || event.Target != f.repo {
						t.Fatal("real successful enqueue audit lost its authenticated actor or repository")
					}
					enqueues++
				}
			}
			if enqueues < 4 {
				t.Fatal("successful first enqueue, retry, canary and dry-run audits missing", enqueues)
			}
			event["auditEnqueues"] = enqueues
		}
		emit(event)
	}
	t.Log("real auth/CSRF + API/store + rendered Settings fixture complete; sealed bytes are fixture-authored, native generation and public restore are unestablished")
}

func typedSettingsBrowserMirror(ctx context.Context, t *testing.T, f typedNavigationFixture) string {
	t.Helper()
	origin := t.TempDir()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	// Remove inherited Git routing/configuration before isolated commands. Every
	// Git mutation names only a fresh test directory, never the real checkout.
	var environment []string
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			environment = append(environment, entry)
		}
	}
	environment = append(environment, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	run := func(args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		fixed := []string{"-c", "user.name=Neutral fixture", "-c", "user.email=fixture@example.invalid", "-c", "core.hooksPath=/dev/null", "-C", origin}
		cmd := exec.CommandContext(ctx, git, append(fixed, args...)...)
		cmd.Env = environment
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("isolated fixture Git failed: %v: %s", err, out)
		}
		return string(out)
	}
	run("init", "--template=", "-b", "main")
	body := []byte("😀x\nx\n")
	if err := os.WriteFile(filepath.Join(origin, "unicode.go"), body, 0600); err != nil {
		t.Fatal(err)
	}
	run("add", "--", "unicode.go")
	run("commit", "-m", "neutral rendered Settings source")
	commit := strings.TrimSpace(run("rev-parse", "HEAD"))
	if len(commit) != 40 || run("show", "HEAD:unicode.go") != string(body) {
		t.Fatal("isolated source commit or bytes differ")
	}
	mirror := phebssync.RepoDir(f.base, f.repo)
	if err := os.MkdirAll(filepath.Dir(mirror), 0700); err != nil {
		t.Fatal(err)
	}
	run("clone", "--bare", "--no-hardlinks", "--", origin, mirror)
	return commit
}
