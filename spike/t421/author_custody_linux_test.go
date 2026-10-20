//go:build linux

package t421

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

func TestLinuxAuthorClosedRequestFullWidth(t *testing.T) {
	request := linuxCorpusAuthorRequest{Schema: linuxCorpusAuthorRequestSchema, PlanSHA256: SHA256([]byte("plan")), AuthorSHA256: SHA256([]byte("author")), SourcePath: "/private/source", Controller: 42, GitFD: 123, Revision: "a", SourceIdentity: LinuxExecutionCorpusSourceIdentity{Device: 1<<40 | 7, Inode: 1<<48 | 9, Mount: 1<<41 | 11, NamespaceDevice: 1<<39 | 13, NamespaceInode: 1<<49 | 15}}
	raw, err := corpusAuthorCanonical(request, MaxExecutionCorpusAuthorRequestBytes)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := decodeLinuxCorpusAuthorRequest(raw, sha256.Sum256(raw), 42)
	if err != nil || actual.SourceIdentity != request.SourceIdentity || linuxAuthorGitCore(42, 123) != "/proc/42/fd/123/core" {
		t.Fatal("full-width closed source request lost native identity", err)
	}
	for name, mutate := range map[string]func(*linuxCorpusAuthorRequest){
		"legacy": func(r *linuxCorpusAuthorRequest) { r.Schema = ExecutionCorpusAuthorObservedRequestSchema }, "foreign-parent": func(r *linuxCorpusAuthorRequest) { r.Controller = 43 }, "control-fd": func(r *linuxCorpusAuthorRequest) { r.GitFD = 2 }, "huge-fd": func(r *linuxCorpusAuthorRequest) { r.GitFD = 1<<20 + 1 }, "relative-root": func(r *linuxCorpusAuthorRequest) { r.SourcePath = "relative" }, "missing-mount": func(r *linuxCorpusAuthorRequest) { r.SourceIdentity.Mount = 0 }, "missing-namespace": func(r *linuxCorpusAuthorRequest) { r.SourceIdentity.NamespaceInode = 0 }, "unsealed-plan": func(r *linuxCorpusAuthorRequest) { r.PlanSHA256 = "unbound" }, "unbound-author": func(r *linuxCorpusAuthorRequest) { r.AuthorSHA256 = "unbound" }, "future-revision": func(r *linuxCorpusAuthorRequest) { r.Revision = "b" }, "caller-previous": func(r *linuxCorpusAuthorRequest) { r.Previous = &ExecutionCorpusAuthorResponse{} },
	} {
		t.Run(name, func(t *testing.T) {
			changed := request
			mutate(&changed)
			encoded, _ := corpusAuthorCanonical(changed, MaxExecutionCorpusAuthorRequestBytes)
			if _, err := decodeLinuxCorpusAuthorRequest(encoded, sha256.Sum256(encoded), 42); err == nil {
				t.Fatal("malformed or foreign request admitted")
			}
		})
	}
	for _, malformed := range [][]byte{nil, bytes.Repeat([]byte{'x'}, MaxExecutionCorpusAuthorRequestBytes+1), append(bytes.Clone(raw), '\n'), bytes.Replace(raw, []byte(`"controller":42`), []byte(`"controller":42,"controller":42`), 1), bytes.Replace(raw, []byte(`"revision":`), []byte(`"unknown":`), 1)} {
		if _, err := decodeLinuxCorpusAuthorRequest(malformed, sha256.Sum256(malformed), 42); err == nil {
			t.Fatal("noncanonical request admitted")
		}
	}
	if _, err := decodeLinuxCorpusAuthorRequest(raw, [32]byte{1}, 42); err == nil {
		t.Fatal("wrong authenticated digest admitted")
	}
	if _, err := decodeCorpusAuthorRequest(raw, sha256.Sum256(raw)); err == nil {
		t.Fatal("Linux wire widened historical decoder")
	}
}

func TestLinuxAuthorNativeRootsLeaseAndRetention(t *testing.T) {
	for _, mode := range []string{"intact", "source-replaced", "home-symlink", "lease-replaced", "lease-hardlink", "parent-replaced"} {
		t.Run(mode, func(t *testing.T) {
			parent := filepath.Join(t.TempDir(), "parent")
			if os.Mkdir(parent, 0o700) != nil {
				t.Fatal("parent")
			}
			owner := &LinuxExecutionAuthorCustody{}
			if owner.createRoots(t.Context(), parent) != nil || owner.checkRoots(t.Context()) != nil || owner.checkSource(t.Context(), nil) != nil {
				t.Fatal("native roots/lease creation")
			}
			t.Cleanup(func() { _ = owner.Close(context.Background()) })
			if len(owner.PrivateDirectories()) != 3 || owner.roots[1].identity.Inode == 0 || owner.roots[1].identity.Mount == 0 {
				t.Fatal("missing full native custody")
			}
			second := &LinuxExecutionAuthorCustody{}
			if second.createRoots(t.Context(), parent) == nil {
				t.Fatal("second source owner acquired lease")
			}
			_ = second.Close(context.Background())
			preserved := owner.roots[1].file
			switch mode {
			case "source-replaced", "home-symlink":
				path := owner.roots[1].path
				if mode == "home-symlink" {
					path = owner.roots[2].path
				}
				if os.Rename(path, path+"-held") != nil {
					t.Fatal("rename")
				}
				if mode == "home-symlink" {
					if os.Symlink(path+"-held", path) != nil {
						t.Fatal("symlink")
					}
				} else if os.Mkdir(path, 0o700) != nil {
					t.Fatal("replacement")
				}
			case "lease-replaced":
				path := filepath.Join(parent, linuxAuthorLeaseName)
				if os.Rename(path, path+"-held") != nil || os.WriteFile(path, nil, 0o600) != nil {
					t.Fatal("lease replacement")
				}
			case "lease-hardlink":
				if os.Link(filepath.Join(parent, linuxAuthorLeaseName), filepath.Join(parent, "alias")) != nil {
					t.Fatal("lease link")
				}
			case "parent-replaced":
				if os.Rename(parent, parent+"-held") != nil || os.Mkdir(parent, 0o700) != nil {
					t.Fatal("parent replacement")
				}
			}
			if err := owner.checkRoots(t.Context()); (err == nil) != (mode == "intact") {
				t.Fatal("native root/lease drift outcome", mode, err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			attempt := &linuxAuthorAttempt{done: make(chan struct{}), cancel: cancel}
			owner.active = attempt
			deadline, stop := context.WithTimeout(t.Context(), time.Millisecond)
			defer stop()
			if owner.Close(deadline) == nil || ctx.Err() == nil {
				t.Fatal("unknown worker released or not canceled")
			}
			if _, err := preserved.Stat(); err != nil {
				t.Fatal("unknown use released held source inode")
			}
			close(attempt.done)
			if owner.Close(t.Context()) != nil || !owner.released {
				t.Fatal("joined close barrier")
			}
			if _, err := preserved.Stat(); !errors.Is(err, os.ErrClosed) {
				t.Fatal("joined descriptor remained open")
			}
			path := owner.Directory()
			if mode == "parent-replaced" {
				path = strings.Replace(path, parent, parent+"-held", 1)
			}
			if _, err := os.Lstat(path); err != nil {
				t.Fatal("Close removed source/replacement bytes")
			}
		})
	}
	for _, owner := range []*LinuxExecutionAuthorCustody{nil, {}, {roots: []linuxAuthorRoot{{path: "/private/retained"}}}} {
		if len(owner.PrivateDirectories()) != 0 {
			t.Fatal("partial owner exposed nonexistent source")
		}
	}
}

func TestLinuxAuthorNoCallerAuthorityAndTerminalBudgets(t *testing.T) {
	parent := t.TempDir()
	for _, request := range []LinuxExecutionAuthorRequest{{}, {Git: &LinuxExecutionGitCustody{}}, {Git: &LinuxExecutionGitCustody{}, Builds: &LinuxExecutionGoBuildCustody{}, Author: &LinuxExecutionToolCustody{}, Plan: &LinuxExecutionInputCustody{}}} {
		if owner, err := PrepareLinuxExecutionAuthor(t.Context(), parent, request); err == nil || owner != nil {
			t.Fatal("caller-created identity issued author authority")
		}
	}
	for index, want := range []uint64{4, 3, 3} {
		config, control := linuxAuthorCustodyConfig(index, [32]byte{1})
		if config.Limits.Attempts != want || config.Phases[0].Roles[0].Attempts != want || !control.TerminalAuthor || control.MaximumWireBytes != 4*dispatchadmission.FrameBytes || control.InitialPhase != 1 || control.MaximumPhases != 1 {
			t.Fatal("terminal finite recipe changed", index, config, control)
		}
	}
	// Legacy global Linux Darwin-style gates remain refusals.
	info, err := os.Lstat(parent)
	if err != nil {
		t.Fatal(err)
	}
	if inputCustodyOwned(info) || inputCustodyProtected(info) {
		t.Fatal("global legacy seam widened")
	}
	file, err := os.Open(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if _, err := inputCustodyVolume(file); err == nil {
		t.Fatal("legacy volume refusal widened")
	}
}

func TestLinuxAuthorResultDefensiveCopies(t *testing.T) {
	owner := &LinuxExecutionAuthorCustody{results: []ExecutionAuthorResult{{Revision: "a", Response: &ExecutionCorpusAuthorResponse{ChangedPhysicalFiles: &ExecutionChangedPhysicalFiles{Count: 3, Complete: true}}}}}
	result := owner.Results()
	result[0].Response.ChangedPhysicalFiles.Count = 999
	result[0].Revision = "foreign"
	if owner.Results()[0].Response.ChangedPhysicalFiles.Count != 3 || owner.Results()[0].Revision != "a" {
		t.Fatal("result aliases owner")
	}
}

func TestLinuxAuthorPendingTimeoutKeepsRootsAndFinalPrefix(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "parent")
	if os.Mkdir(parent, 0o700) != nil {
		t.Fatal("private parent fixture")
	}
	builds := &LinuxExecutionGoBuildCustody{}
	owner := &LinuxExecutionAuthorCustody{request: LinuxExecutionAuthorRequest{Builds: builds}}
	if owner.createRoots(t.Context(), parent) != nil {
		t.Fatal("native roots")
	}
	owner.projection.expected[0].Name = "a"
	owner.projection.deadlines[0] = time.Millisecond
	builds.mu.Lock()
	result, err := owner.AuthorNext(t.Context())
	if err == nil || result.Revision != "a" || result.Completed {
		builds.mu.Unlock()
		t.Fatal("pending timeout issued success")
	}
	owner.mu.Lock()
	attempt := owner.active
	owner.mu.Unlock()
	if attempt == nil {
		builds.mu.Unlock()
		t.Fatal("timed out worker was discarded")
	}
	deadline, cancel := context.WithTimeout(t.Context(), time.Millisecond)
	closeErr := owner.Close(deadline)
	cancel()
	if closeErr == nil || owner.released {
		builds.mu.Unlock()
		t.Fatal("timeout substituted for worker join")
	}
	for _, root := range owner.roots {
		if _, err := root.file.Stat(); err != nil {
			builds.mu.Unlock()
			t.Fatal("pending source descriptor released")
		}
	}
	builds.mu.Unlock()
	<-attempt.done
	results := owner.Results()
	if len(results) != 1 || results[0].Completed || results[0].RootStarted || results[0].Response != nil {
		t.Fatal("actual failed pre-start prefix replaced")
	}
	if owner.Close(t.Context()) == nil || !owner.released {
		t.Fatal("known failed Close lost sticky failure or retained joined descriptors")
	}
}
