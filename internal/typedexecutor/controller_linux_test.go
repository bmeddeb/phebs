//go:build linux

package typedexecutor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/bmeddeb/phebs/internal/typedsandbox"
	"github.com/bmeddeb/phebs/internal/typedworkspace"
)

// This explicit server-mode fixture does not exercise OpenLocal's executable
// version admission. The installed engine is started only on container loopback;
// its direct child is killed and joined even when a test fails.
func testServer(t *testing.T) string {
	t.Helper()
	binary, err := exec.LookPath("surreal")
	if err != nil {
		t.Fatal("required real SurrealDB fixture:", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	cmd := exec.CommandContext(t.Context(), binary, "start", "--bind", address, "--user", "root", "--pass", "fixture", "memory")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var waitErr error
	go func() { waitErr = cmd.Wait(); close(done) }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("test engine failed to join")
		}
	})
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		connection, e := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if e == nil {
			_ = connection.Close()
			return "ws://" + address
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("engine readiness timeout")
		case <-done:
			t.Fatalf("engine exited: %v", waitErr)
		}
	}
}
func testHash(b []byte) string { h := sha256.Sum256(b); return "sha256:" + hex.EncodeToString(h[:]) }
func encode(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

type fixture struct {
	c      *Controller
	s      *store.Surreal
	chunk  store.GenerationChunk
	source string
	raw    []byte
}

func preparationFixture(t *testing.T, endpoint, database string) fixture {
	t.Helper()
	ctx := t.Context()
	s, err := store.Open(ctx, endpoint, "root", "fixture", "executor", database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	base := t.TempDir()
	t.Cleanup(func() {
		_ = filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				_ = os.Chmod(path, 0700)
			}
			return nil
		})
	})
	workspace, source, host, index := filepath.Join(base, "workspace"), filepath.Join(base, "source"), filepath.Join(base, "host"), filepath.Join(base, "index")
	for _, p := range []string{workspace, source, host, index} {
		if err = os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.WriteFile(filepath.Join(workspace, ".phebs-index-publication.lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	data := []byte("fixture")
	if err = os.WriteFile(filepath.Join(source, "data"), data, 0600); err != nil {
		t.Fatal(err)
	}
	raw := encode(t, typedindex.InventoryDefinition{Schema: typedindex.InventorySchema, Files: []typedindex.BundleFile{{Path: "data", Bytes: int64(len(data)), Digest: testHash(data)}}})
	tool := typedindex.Tool{Version: "0.2.7", Digest: testHash([]byte("tool"))}
	profile, err := typedindex.DecodeProfile(ctx, encode(t, typedindex.ProfileDefinition{Schema: typedindex.ProfileSchema, Name: "reduced", Provider: typedindex.ProviderID, Tools: typedindex.Tools{Bazel: tool, RulesGo: tool, Go: tool, Driver: tool, Indexer: tool, Planner: tool, Launcher: tool}, Config: typedindex.ReducedConfig(), Policy: typedindex.MeasuredPolicy(), BundleDigest: testHash(raw), ImageDigest: testHash([]byte("image"))}))
	if err != nil {
		t.Fatal(err)
	}
	repo := "example.invalid/preparation"
	if err = s.UpsertRepo(ctx, store.Repo{Name: repo}); err != nil {
		t.Fatal(err)
	}
	if err = s.SetRepoIndexed(ctx, repo, strings.Repeat("a", 40), time.Now()); err != nil {
		t.Fatal(err)
	}
	intent, err := s.InstallTypedProfile(ctx, repo, profile, testHash([]byte("universe")), 0)
	if err != nil {
		t.Fatal(err)
	}
	sourceID, err := s.GetTypedSource(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	request := typedindex.NewRequest(sourceID, profile, uint64(intent.ProfileEpoch), intent.UniverseDigest, "preparation")
	if _, err = s.EnqueueTypedIndex(ctx, repo, encode(t, request)); err != nil {
		t.Fatal(err)
	}
	schedule, err := s.TypedIndexSchedule(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.EnqueueGenerationSchedule(ctx, schedule); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ExpandGenerationSchedule(ctx, repo, schedule.Stage, schedule.Generation); err != nil {
		t.Fatal(err)
	}
	chunk, err := s.ClaimGenerationChunk(ctx, store.GenerationResourceTypedIndex, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	c, err := New(Config{Store: s, Workspace: workspace, Acquire: func(ctx context.Context) (func(), error) { return focusedindex.AcquireMutationLock(ctx, index) }})
	if err != nil {
		t.Fatal(err)
	}
	// No privileged host observation is claimed. This seam supplies actual
	// private-directory statfs identity/capacity plus an explicitly empty native
	// namespace. All workspace operations and database queries remain real.
	c.observeHost = func(ctx context.Context, _ string) (typedsandbox.HostObservation, error) {
		o, e := typedworkspace.ObserveCapacity(ctx, host)
		return typedsandbox.HostObservation{Capacity: typedsandbox.HostCapacity(o)}, e
	}
	return fixture{c, s, *chunk, source, raw}
}
func TestTypedExecutorPreparation(t *testing.T) {
	endpoint := testServer(t)
	t.Run("prepare-reopen-and-census", func(t *testing.T) {
		f := preparationFixture(t, endpoint, "positive")
		ctx := t.Context()
		if _, err := f.c.Prepare(ctx, f.chunk, f.source, f.raw); !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
		if err := f.c.Startup(ctx); err != nil {
			t.Fatal(err)
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := f.c.Prepare(canceled, f.chunk, f.source, f.raw); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		entries, err := os.ReadDir(f.c.config.Workspace)
		if err != nil || len(entries) != 1 {
			t.Fatal("canceled grew", entries, err)
		}
		prepared, err := f.c.Prepare(ctx, f.chunk, f.source, f.raw)
		if err != nil {
			t.Fatal(err)
		}
		if prepared.Manifest.Revision != 2 {
			t.Fatal(prepared)
		}
		before := prepared.Manifest.Digest()
		if err = f.c.Startup(ctx); err != nil {
			t.Fatal("consistent restart", err)
		}

		outcomes := make(chan error, 2)
		for range 2 {
			go func() {
				p, e := f.c.Prepare(ctx, f.chunk, "/does-not-exist", f.raw)
				if e == nil && p.Manifest.Digest() != before {
					e = ErrHeld
				}
				outcomes <- e
			}()
		}
		for range 2 {
			if e := <-outcomes; e != nil {
				t.Fatal("serialized replay", e)
			}
		}
		again, err := f.c.Prepare(ctx, f.chunk, "/does-not-exist", f.raw)
		if err != nil || again.Manifest.Digest() != before {
			t.Fatal("replay copied again", err)
		}
		g, err := f.s.GetTypedIndexGrowth(ctx)
		if err != nil || g.State != "held" {
			t.Fatal("holder released", g, err)
		}
		wrong := f.chunk
		wrong.LeaseToken = "different"
		if _, err = f.c.Prepare(ctx, wrong, f.source, f.raw); !store.IsDeferral(err) {
			t.Fatal("different holder", err)
		}
		orphan := filepath.Join(f.c.config.Workspace, strings.Repeat("f", 64))
		if err = os.Mkdir(orphan, 0700); err != nil {
			t.Fatal(err)
		}
		if err = f.c.Startup(ctx); err == nil {
			t.Fatal("orphan root accepted")
		}
		if err = os.Remove(orphan); err != nil {
			t.Fatal(err)
		}
		if err = f.c.Startup(ctx); err != nil {
			t.Fatal(err)
		}

		if err = f.s.SetRepoIndexed(ctx, f.chunk.Repository, strings.Repeat("b", 40), time.Now()); err != nil {
			t.Fatal(err)
		}
		if err = f.c.Startup(ctx); err != nil {
			t.Fatal("obsolete custody must remain inspectable", err)
		}
		if _, err = f.c.Prepare(ctx, f.chunk, f.source, f.raw); err == nil {
			t.Fatal("stale source admitted")
		}
		path := filepath.Join(f.c.config.Workspace, prepared.Manifest.RelativeName(), "owner.json")
		saved, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.Chmod(path, 0600); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, []byte("{}"), 0600); err != nil {
			t.Fatal(err)
		}
		if err = f.c.Startup(ctx); err == nil {
			t.Fatal("changed DB custody accepted")
		}
		if err = os.WriteFile(path, saved, 0600); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("durable-interrupted-prefixes", func(t *testing.T) {
		for _, stage := range []string{"holder", "owner", "revision1", "filesystem-revision2"} {
			t.Run(stage, func(t *testing.T) {
				f := preparationFixture(t, endpoint, strings.ReplaceAll(stage, "-", ""))
				ctx := t.Context()
				if err := f.c.Startup(ctx); err != nil {
					t.Fatal(err)
				}
				work, err := f.s.BeginTypedIndex(ctx, f.chunk)
				if err != nil {
					t.Fatal(err)
				}
				id, err := typedworkspace.NewOwnerIdentity(work.Parent, f.chunk.Identity, f.chunk.LeaseToken)
				if err != nil {
					t.Fatal(err)
				}
				inv, err := typedindex.DecodeInventory(ctx, f.raw, work.Parent.Request().BundleDigest)
				if err != nil {
					t.Fatal(err)
				}
				w, h, err := f.c.observe(ctx)
				if err != nil {
					t.Fatal(err)
				}
				budget, err := typedworkspace.DeriveOwnerBudget(ctx, inv, w.BlockSize)
				if err != nil {
					t.Fatal(err)
				}
				hb, err := typedsandbox.DeriveHostScratchBudget(h.BlockSize)
				if err != nil {
					t.Fatal(err)
				}
				spec, err := f.c.admit(ctx, w, h, budget, hb)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = f.s.AcquireTypedIndexGrowth(ctx, f.chunk, spec); err != nil {
					t.Fatal(err)
				}
				if stage != "holder" {
					m, e := typedworkspace.CreateOwner(ctx, f.c.config.Workspace, w, id, budget, f.c.gates[w.Device])
					if e != nil {
						t.Fatal(e)
					}
					if stage != "owner" {
						if e = f.s.SaveTypedIndexCustody(ctx, f.chunk, "", custody(m)); e != nil {
							t.Fatal(e)
						}
						if stage == "filesystem-revision2" {
							r, e := typedworkspace.Copy(ctx, f.source, filepath.Join(f.c.config.Workspace, id.RelativeName()), inv, f.c.gates[w.Device])
							if e != nil {
								t.Fatal(e)
							}
							if _, e = typedworkspace.SaveOwnerInputs(ctx, f.c.config.Workspace, id, m.Digest(), f.raw, r, f.c.gates[w.Device]); e != nil {
								t.Fatal(e)
							}
						}
					}
				}
				names := func() []string {
					var out []string
					_ = filepath.WalkDir(f.c.config.Workspace, func(path string, _ os.DirEntry, e error) error {
						if e != nil {
							t.Fatal(e)
						}
						out = append(out, path)
						return nil
					})
					return out
				}
				before := strings.Join(names(), "\n")
				for range 2 {
					if _, err = f.c.Prepare(ctx, f.chunk, f.source, f.raw); !errors.Is(err, ErrHeld) {
						t.Fatalf("%s replay: %v", stage, err)
					}
				}
				if before != strings.Join(names(), "\n") {
					t.Fatal("replayed prefix grew")
				}
				err = f.c.Startup(ctx)
				if (stage == "owner" || stage == "filesystem-revision2") && err == nil {
					t.Fatal("unbound filesystem prefix adopted")
				}
			})
		}
	})

	t.Run("base-replaced-before-growth", func(t *testing.T) {
		f := preparationFixture(t, endpoint, "replacement")
		ctx := t.Context()
		if err := f.c.Startup(ctx); err != nil {
			t.Fatal(err)
		}
		original := f.c.observeHost
		f.c.observeHost = func(ctx context.Context, name string) (typedsandbox.HostObservation, error) {
			if err := os.Rename(f.c.config.Workspace, f.c.config.Workspace+"-old"); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(f.c.config.Workspace, 0700); err != nil {
				t.Fatal(err)
			}
			return original(ctx, name)
		}
		if _, err := f.c.Prepare(ctx, f.chunk, f.source, f.raw); err == nil {
			t.Fatal("replacement admitted")
		}
		entries, err := os.ReadDir(f.c.config.Workspace)
		if err != nil || len(entries) != 0 {
			t.Fatal("grew replacement", entries, err)
		}
		if _, err = f.s.GetTypedIndexGrowth(ctx); err != nil {
			t.Fatal("uncertain holder lost", err)
		}
		f.c.observeHost = original
		if err = f.c.Startup(ctx); err == nil {
			t.Fatal("replaced base became new authority")
		}
	})

	t.Run("failed-copy-never-regrows", func(t *testing.T) {
		f := preparationFixture(t, endpoint, "failed")
		ctx := t.Context()
		if err := f.c.Startup(ctx); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(f.source, "data"), []byte("badtext"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := f.c.Prepare(ctx, f.chunk, f.source, f.raw); err == nil {
			t.Fatal("bad copy passed")
		}
		work, err := f.s.BeginTypedIndex(ctx, f.chunk)
		if err != nil {
			t.Fatal(err)
		}
		if work.Custody == nil || work.Custody.Revision != 1 || work.Growth == nil {
			t.Fatal("prefix not durable", work)
		}
		parent := filepath.Join(f.c.config.Workspace, work.RootDigest[7:], work.AttemptDigest[7:])
		before, err := os.ReadDir(parent)
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if _, err = f.c.Prepare(ctx, f.chunk, f.source, f.raw); !errors.Is(err, ErrHeld) {
				t.Fatal("partial replay", err)
			}
		}
		after, err := os.ReadDir(parent)
		if err != nil || len(after) != len(before) {
			t.Fatal("replay grew", before, after, err)
		}
		if err = f.c.Startup(ctx); err == nil {
			t.Fatal("partial stage accepted at restart")
		}
	})
}
