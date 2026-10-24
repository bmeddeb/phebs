package resolvermaterialize

import (
	"context"
	"crypto/sha1" // Git fixture object identity, not a trust digest.
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/resolvercatalog"
	"github.com/bmeddeb/phebs/internal/store"
	reposync "github.com/bmeddeb/phebs/internal/sync"
)

// Count actual exec'ed Git lifetimes, then prove each recorded PID is gone.
// The wrapper execs the real Git image; it does not emulate its protocol.
func resolverLeaseGitFixture(t *testing.T, directory string, count int) ([]extract.CandidateManifestFile, string, uint64) {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), git, "init", "--bare", "--object-format=sha1", directory)
	if raw, err := command.CombinedOutput(); err != nil {
		t.Fatalf("fixture repository: %v %s", err, raw)
	}
	files := make([]extract.CandidateManifestFile, count)
	paths := make([]string, count)
	inputDirectory := t.TempDir()
	var total uint64
	for i := range files {
		content := fmt.Sprintf("module example.invalid/module%03d\n", i)
		paths[i] = filepath.Join(inputDirectory, fmt.Sprintf("module%03d", i))
		if err := os.WriteFile(paths[i], []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		oid := sha1.Sum([]byte(fmt.Sprintf("blob %d\x00%s", len(content), content)))
		files[i] = extract.CandidateManifestFile{Path: fmt.Sprintf("modules/%03d/go.mod", i), ObjectID: fmt.Sprintf("%x", oid),
			DeclaredBytes: int64(len(content)), Mode: "100644", SourceLane: candidate.SourceLaneBase}
		total += uint64(len(content))
	}
	if count != 0 {
		command = exec.CommandContext(t.Context(), git, "-C", directory, "hash-object", "-w", "--stdin-paths")
		command.Stdin = strings.NewReader(strings.Join(paths, "\n") + "\n")
		if raw, err := command.CombinedOutput(); err != nil {
			t.Fatalf("fixture objects: %v %s", err, raw)
		}
	}
	wrapper := t.TempDir()
	log := filepath.Join(wrapper, "pids")
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
	script := "#!/bin/sh\nprintf '%s\\n' \"$$\" >> " + quote(log) + "\nexec " + quote(git) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(wrapper, "git"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", wrapper+string(os.PathListSeparator)+os.Getenv("PATH"))
	return files, log, total
}

func resolverLeaseJoinedChildren(t *testing.T, path string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	rows := strings.Fields(string(raw))
	for _, row := range rows {
		pid, err := strconv.Atoi(row)
		if err != nil || pid <= 0 {
			t.Fatal("invalid fixture PID")
		}
		if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
			t.Fatalf("Git PID %d remains unjoined or unavailable: %v", pid, err)
		}
	}
	return len(rows)
}

func resolverLeaseObservedContext(t *testing.T, base context.Context, source *int, reads, bytes *uint64) context.Context {
	t.Helper()
	ctx, err := readaccounting.WithSourceObserver(base, func() error { *source++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	ctx, err = readaccounting.WithResolverBlobObserver(ctx, func(size uint64) error { *reads++; *bytes += size; return nil })
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

func TestResolverBuildBatchLeaseExactWorkAndChildCost(t *testing.T) {
	repository := filepath.Join(t.TempDir(), "repo.git")
	files, log, total := resolverLeaseGitFixture(t, repository, 256)
	registry := newMaterializeTestRegistry(t, ProtocolGRPC)
	manifest := &materializeTestManifest{identity: testManifestDigest, files: files}
	request := BuildRequest{RepositoryDir: repository, Registry: registry,
		Manifest: manifest, Identity: newMaterializeTestIdentity(t, registry, manifest.identity, nil), Assertions: &materializeAssertionReader{}}
	var states [2]resolvercatalog.State
	for index, mode := range []string{"former_per_blob", "default_batch"} {
		t.Run(mode, func(t *testing.T) {
			if err := os.WriteFile(log, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			request.Root = t.TempDir()
			request.ReadBlob = nil
			if index == 0 {
				request.ReadBlob = gitobj.ReadBlob // The exact former production child-cost path.
			}
			var source int
			var reads, bytes uint64
			ctx := resolverLeaseObservedContext(t, t.Context(), &source, &reads, &bytes)
			prepared, err := Build(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = prepared.Discard() }()
			states[index] = prepared.State()
			children := 1
			if index == 0 {
				children = len(files)
			}
			if got := resolverLeaseJoinedChildren(t, log); got != children || source != len(files) || reads != uint64(len(files)) || bytes != total {
				t.Fatalf("children=%d want=%d source=%d reads=%d bytes=%d want=%d", got, children, source, reads, bytes, total)
			}
		})
	}
	if states[0].ManifestDigest != states[1].ManifestDigest || states[0].GenerationDigest != states[1].GenerationDigest {
		t.Fatal("batching changed sealed resolver authority")
	}
}

func TestResolverWorkerBatchLeaseJoinsBeforePublicationAndNoop(t *testing.T) {
	fixture := newWorkerFixture(t)
	fixture.publishDeclaration(fixture.digest)
	repository, err := reposync.SafeRepoDir(fixture.dataDir, fixture.repository)
	if err != nil {
		t.Fatal(err)
	}
	files, log, total := resolverLeaseGitFixture(t, repository, 8)
	fixture.manifest.files = files
	fixture.worker.readBlob = nil // Constructor-owned production default.
	fixture.state.publishHook = func(context.Context, store.ResolverCatalogPublication) error {
		if resolverLeaseJoinedChildren(t, log) != 1 {
			t.Fatal("publication preceded the sole Git join")
		}
		return nil
	}
	var source int
	var reads, bytes uint64
	ctx := resolverLeaseObservedContext(t, t.Context(), &source, &reads, &bytes)
	job := store.Job{Kind: store.JobResolverCatalog, Target: fixture.repository}
	if err := fixture.worker.Handle(ctx, job); err != nil {
		t.Fatal(err)
	}
	if source != len(files) || reads != uint64(len(files)) || bytes != total || fixture.state.publishCalls != 1 {
		t.Fatal("actual worker read/publication counts changed", source, reads, bytes, fixture.state.publishCalls)
	}
	if err := fixture.worker.Handle(ctx, job); err != nil || resolverLeaseJoinedChildren(t, log) != 1 || source != len(files) || fixture.state.publishCalls != 1 {
		t.Fatal("exact-current no-op reopened or reread Git", err)
	}
}

func TestResolverWorkerBatchLeaseFailureJoin(t *testing.T) {
	for _, mode := range []string{"read", "build", "canceled", "custom", "empty"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newWorkerFixture(t)
			fixture.publishDeclaration(fixture.digest)
			repository, err := reposync.SafeRepoDir(fixture.dataDir, fixture.repository)
			if err != nil {
				t.Fatal(err)
			}
			files, log, _ := resolverLeaseGitFixture(t, repository, 2)
			fixture.manifest.files, fixture.worker.readBlob = files, nil
			failure := errors.New("controlled resolver failure")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			wantChildren := 1
			switch mode {
			case "read":
				fixture.manifest.files[0].ObjectID = strings.Repeat("f", 40)
			case "build":
				fixture.state.assertionErr = failure // After actual module reads.
			case "canceled":
				ctx, err = readaccounting.WithResolverBlobObserver(ctx, func(uint64) error { cancel(); return nil })
				if err != nil {
					t.Fatal(err)
				}
			case "custom":
				wantChildren = 0
				fixture.worker.readBlob = func(context.Context, string, string, int64) ([]byte, error) { return nil, failure }
			case "empty":
				wantChildren = 0
				fixture.manifest.files = nil
			}
			err = fixture.worker.Handle(ctx, store.Job{Kind: store.JobResolverCatalog, Target: fixture.repository})
			if (err == nil) != (mode == "empty") || resolverLeaseJoinedChildren(t, log) != wantChildren {
				t.Fatal("failure/empty build lost join or started a needless child", err)
			}
			if mode != "empty" && fixture.state.publishCalls != 0 {
				t.Fatal("failed build published")
			}
			if mode == "canceled" && !errors.Is(err, context.Canceled) || (mode == "custom" || mode == "build") && !errors.Is(err, failure) {
				t.Fatal("original failure lost", err)
			}
		})
	}
}

type resolverLeaseCloseFailure struct {
	resolverBlobSession
	err error
}

func (session resolverLeaseCloseFailure) Close() error {
	return errors.Join(session.resolverBlobSession.Close(), session.err)
}

func TestResolverBuildBatchLeaseOpenAndCloseFailure(t *testing.T) {
	for _, mode := range []string{"open", "close"} {
		t.Run(mode, func(t *testing.T) {
			repository := filepath.Join(t.TempDir(), "repo.git")
			files, log, _ := resolverLeaseGitFixture(t, repository, 1)
			registry := newMaterializeTestRegistry(t, ProtocolGRPC)
			manifest := &materializeTestManifest{identity: testManifestDigest, files: files}
			request := BuildRequest{Root: t.TempDir(), RepositoryDir: repository, Registry: registry,
				Manifest: manifest, Identity: newMaterializeTestIdentity(t, registry, manifest.identity, nil), Assertions: &materializeAssertionReader{}}
			failure := errors.New("controlled session failure")
			open := func(ctx context.Context, directory string) (resolverBlobSession, error) {
				if mode == "open" {
					return nil, failure
				}
				reader, err := openResolverBlobSession(ctx, directory)
				if err != nil {
					return nil, err
				}
				return resolverLeaseCloseFailure{resolverBlobSession: reader, err: failure}, nil
			}
			prepared, err := buildWithBlobSession(t.Context(), request, open)
			if prepared != nil || !errors.Is(err, failure) {
				t.Fatal("session failure returned publishable authority", err)
			}
			children := 0
			if mode == "close" {
				children = 1
			}
			if resolverLeaseJoinedChildren(t, log) != children {
				t.Fatal("session failure lost actual child join")
			}
		})
	}
}
