package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/index"
)

func TestT422RetentionClosedRequests(t *testing.T) {
	for _, command := range []bool{false, true} {
		method, path := http.MethodGet, t422RetentionReadPath
		if command {
			method, path = http.MethodPost, t422RetentionPinPath
		}
		for _, test := range []struct {
			name   string
			change func(*http.Request)
		}{
			{"method", func(r *http.Request) { r.Method = http.MethodDelete }},
			{"query", func(r *http.Request) { r.URL.RawQuery = "generation=caller" }},
			{"empty query", func(r *http.Request) { r.URL.ForceQuery = true }},
			{"body", func(r *http.Request) { r.ContentLength = 1 }},
			{"chunked", func(r *http.Request) { r.TransferEncoding = []string{"chunked"} }},
			{"encoded", func(r *http.Request) { r.URL.RawPath = strings.Replace(path, "retention", "%72etention", 1) }},
			{"unknown", func(r *http.Request) { r.URL.Path += "/extra" }},
		} {
			t.Run(fmt.Sprint(command)+"/"+test.name, func(t *testing.T) {
				r := httptest.NewRequest(method, path, nil)
				if !t422RetentionRequest(r, command) {
					t.Fatal("closed empty request refused")
				}
				test.change(r)
				if t422RetentionRequest(r, command) {
					t.Fatal("open request accepted")
				}
			})
		}
	}
	if t422RetentionRequest(nil, false) {
		t.Fatal("nil request accepted")
	}
}

func TestT422RetentionUnadmittedRefusal(t *testing.T) {
	launch := &t422SemanticLaunch{fail: func(error) {}}
	launch.request.ServerEpoch = 1
	launch.request.Repository = "test.invalid/retention"
	pins := &focusedindex.SearchGenerationPins{}
	owner := lifecycle.SearchGenerationOwnerImpl{IndexDir: t.TempDir(), Pins: pins, Acquire: func(context.Context) (func(), error) { return func() {}, nil }}
	if control, err := newT422RetentionControl(t.Context(), launch, owner, pins); control != nil || err == nil {
		t.Fatal("unadmitted constructor succeeded")
	}
	control := &t422RetentionControl{ctx: t.Context(), launch: launch, owner: owner, pins: pins}
	if _, finish, err := control.read(t.Context()); err == nil || finish != nil || control.err == nil {
		t.Fatal("unadmitted read succeeded")
	}
}

func t422RetentionFixtureControl(t *testing.T, directory, repository, digest string, files uint64) *t422RetentionControl {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	pins := &focusedindex.SearchGenerationPins{}
	control := &t422RetentionControl{ctx: ctx, cancel: cancel, pins: pins, old: digest, files: files,
		launch: &t422SemanticLaunch{fail: func(error) {}}, sink: func([]byte) error { return nil }, contextJoined: make(chan struct{})}
	control.launch.request.Repository = repository
	control.owner = lifecycle.SearchGenerationOwnerImpl{IndexDir: directory, Pins: pins, Acquire: func(ctx context.Context) (func(), error) {
		return focusedindex.AcquireMutationLock(ctx, directory)
	}}
	control.stopContext = context.AfterFunc(ctx, func() { control.releasePin(); close(control.contextJoined) })
	t.Cleanup(control.Close)
	return control
}

func TestT422RetentionPinTerminalCleanup(t *testing.T) {
	for _, mode := range []string{"close", "cancel", "failure", "failure-panic"} {
		t.Run(mode, func(t *testing.T) {
			control := t422RetentionFixtureControl(t, t.TempDir(), "test.invalid/retention", testRuntimeDigest("a"), 2)
			if err := control.acquirePin(); err != nil || !control.pins.Pinned(control.launch.request.Repository, control.old) {
				t.Fatal("actual pin absent", err)
			}
			if control.acquirePin() == nil {
				t.Fatal("repeated pin acquired")
			}
			switch mode {
			case "close":
				control.Close()
			case "cancel":
				control.cancel()
				<-control.contextJoined
			case "failure":
				_ = control.stop()
			case "failure-panic":
				control.launch.fail = func(error) { panic("private failure callback") }
				func() {
					defer func() {
						if recover() == nil {
							t.Error("callback panic swallowed")
						}
					}()
					_ = control.stop()
				}()
			}
			if control.pins.Pinned(control.launch.request.Repository, control.old) {
				t.Fatal("terminal path retained pin")
			}
			control.Close()
		})
	}
}

// Native publication/reader evidence only: this fixture does not install a
// dispatch runtime or issue final-authority/input admission. Two tiny real
// source generations exercise the exact same concrete observe method as R.
func TestT422RetentionNativeCurrentPrior(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	const repository = "test.invalid/retention"
	repositoryDir, indexDir := t.TempDir(), t.TempDir()
	oldContent, newContent := []byte("package sample\nconst Revision = \"a\"\n"), []byte("package sample\nconst Revision = \"b\"\n")
	old := t422RetentionPublish(t, ctx, repositoryDir, indexDir, repository, oldContent, true)
	control := t422RetentionFixtureControl(t, indexDir, repository, old, 2)
	if err := control.acquirePin(); err != nil {
		t.Fatal(err)
	}
	newDigest := t422RetentionPublish(t, ctx, repositoryDir, indexDir, repository, newContent, false)
	var events []t422RetentionSweep
	control.sink = func(raw []byte) error {
		if len(raw) > 1<<10 {
			return errors.New("event exceeds bound")
		}
		var value t422RetentionSweep
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		events = append(events, value)
		return nil
	}
	readCtx, ledger, err := readaccounting.Start(ctx, readaccounting.Counts{ControlFileReads: 41, MemberVisits: 4})
	if err != nil {
		t.Fatal(err)
	}
	value, err := control.observe(readCtx)
	counts, finishErr := ledger.Finish()
	if err != nil || finishErr != nil || counts != (readaccounting.Counts{ControlFileReads: 41, MemberVisits: 4}) {
		t.Fatalf("native observe: %v, finish %v, counts %+v, value %+v", err, finishErr, counts, value)
	}
	wantQuery := t422RetentionRecipe("t422-reader-query-v1", t421FinalSHA256([]byte(t422RetentionProbePath)), t422RetentionTestBlob(oldContent), t422RetentionTestBlob(newContent))
	if value.OldSearchGenerationSHA256 != old || value.NewSearchGenerationSHA256 != newDigest || value.QuerySHA256 != wantQuery ||
		value.OldRecords != 1 || value.NewRecords != 1 || value.PostReleaseRecords != 1 || !value.OldReaderHeldThroughReprobe ||
		value.OldProjectionSHA256 != t422RetentionRecipe("t422-reader-projection-v1", wantQuery, t422RetentionTestBlob(oldContent)) ||
		value.NewProjectionSHA256 != t422RetentionRecipe("t422-reader-projection-v1", wantQuery, t422RetentionTestBlob(newContent)) ||
		value.PostReleaseProjectionSHA256 != value.OldProjectionSHA256 || value.PinnedAtUnixNano <= 0 || value.ReleasedAtUnixNano < value.PinnedAtUnixNano {
		t.Fatalf("actual reader facts changed: %+v", value)
	}
	if len(events) != 2 || events[0] != (t422RetentionSweep{Attempt: 1, Completeness: "exact"}) || events[1] != (t422RetentionSweep{Attempt: 2, Completeness: "exact"}) ||
		control.turns != 2 || control.deleted != 0 || control.maxDeleted != 0 || control.pins.Pinned(repository, old) {
		t.Fatalf("native sweeps/pin differ: %+v", events)
	}
	if _, err := control.sweep(ctx); err == nil || control.turns != 2 || len(events) != 2 {
		t.Fatal("third native turn admitted")
	}
}

func TestT422RetentionNativePositiveFailure(t *testing.T) {
	for _, mode := range []string{"overshoot", "sink-panic", "cancel-result"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			const repository = "test.invalid/retention"
			directory := t.TempDir()
			control := t422RetentionFixtureControl(t, directory, repository, testRuntimeDigest("a"), 2)
			// The real owner recognizes and deletes only this task-owned native
			// metadata entry. It demonstrates positive rejected work, not a fake Sweep.
			base := focusedindex.SearchGenerationRootDirectory(directory)
			if err := os.MkdirAll(base, 0o700); err != nil {
				t.Fatal(err)
			}
			metadata := filepath.Join(base, ".DS_Store")
			if err := os.WriteFile(metadata, []byte("owned fixture"), 0o600); err != nil {
				t.Fatal(err)
			}
			var returned atomic.Uint64
			control.sink = func(raw []byte) error {
				returned.Add(1)
				var event t422RetentionSweep
				if json.Unmarshal(raw, &event) != nil || event.Deleted != 1 || control.deleted != 1 {
					t.Error("positive deletion absent before sink")
				}
				if mode == "sink-panic" {
					panic("private sink")
				}
				if mode == "cancel-result" {
					cancel()
				}
				return nil
			}
			_, err := control.sweep(ctx)
			if err == nil || returned.Load() != 1 || control.turns != 1 || control.deleted != 1 || control.maxDeleted != 1 {
				t.Fatalf("actual positive failure lost: %v, turns %d deletions %d", err, control.turns, control.deleted)
			}
			if _, err := os.Stat(metadata); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("native deletion was not real", err)
			}
		})
	}
}

func t422RetentionTestBlob(content []byte) string {
	raw := append([]byte(fmt.Sprintf("blob %d\x00", len(content))), content...)
	sum := sha1.Sum(raw)
	return hex.EncodeToString(sum[:])
}

func t422RetentionPublish(t *testing.T, ctx context.Context, repositoryDir, indexDir, repository string, content []byte, initial bool) string {
	t.Helper()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repositoryDir}, args...)...)
		raw, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("owned fixture Git: %v %s", err, raw)
		}
		return strings.TrimSpace(string(raw))
	}
	if initial {
		git("init", "-q", "-b", "main")
		git("config", "user.name", "retention fixture")
		git("config", "user.email", "fixture@example.invalid")
	}
	path := filepath.Join(repositoryDir, filepath.FromSlash(t422RetentionProbePath))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	const otherPath = "other.go"
	other := []byte("package other\n")
	if err := os.WriteFile(filepath.Join(repositoryDir, otherPath), other, 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "--", t422RetentionProbePath, otherPath)
	git("commit", "-q", "-m", "owned revision")
	commit := git("rev-parse", "HEAD")
	revisions := []store.IndexedRevision{{Selector: "HEAD", Branch: "HEAD", Commit: commit}}
	shardStage := t.TempDir()
	builder, err := index.NewBuilder(index.Options{IndexDir: shardStage, ShardPrefixOverride: "retention", ShardMax: 100 << 20,
		Parallelism: 1, DisableCTags: true, RepositoryDescription: zoekt.Repository{Name: repository, Branches: []zoekt.RepositoryBranch{{Name: "HEAD", Version: commit}}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range []struct {
		name    string
		content []byte
	}{{t422RetentionProbePath, content}, {otherPath, other}} {
		if err := builder.Add(index.Document{Name: file.name, Content: bytes.Clone(file.content), Branches: []string{"HEAD"}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := builder.Finish(); err != nil {
		t.Fatal(err)
	}
	sourceStage := filepath.Join(t.TempDir(), "source")
	source, err := repositoryindex.BuildSourceGeneration(ctx, repositoryDir, sourceStage, repository, revisions)
	if err != nil {
		t.Fatal(err)
	}
	if err := focusedindex.PublishWholeGeneration(ctx, indexDir, shardStage, sourceStage, repository, revisions, source); err != nil {
		t.Fatal(err)
	}
	if err := focusedindex.FinishPublication(indexDir, repository); err != nil {
		t.Fatal(err)
	}
	root, err := focusedindex.ReadSearchGenerationRootContext(ctx, indexDir, repository)
	if err != nil {
		t.Fatal(err)
	}
	return root.Current.GenerationDigest
}
