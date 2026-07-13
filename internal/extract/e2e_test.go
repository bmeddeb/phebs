package extract_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/extractors/protodecl"
	"github.com/bmeddeb/phebs/internal/store"
)

const demoProto = `syntax = "proto3";

package demo;

service Greeter {
  rpc SayHello(HelloRequest) returns (HelloReply) {
}
  rpc SayGoodbye(HelloRequest) returns (HelloReply) {}
}

message HelloRequest {
  string name = 1;
  int32 retries = 7;
}

message HelloReply {
  string message = 1;
}
`

// This covers the pinned-span portion of the T12.3 AC: on a real mirror,
// every declared operation/field becomes an assertion whose evidence atom
// resolves to the pinned commit and exact span. Canonical descriptor-derived
// lineage remains intentionally unclaimed by this provisional reader.
func TestProtoDeclEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	s := newTestStore(t)
	ctx := context.Background()
	dataDir := t.TempDir()
	repoName := "example.com/demo/protos"

	// Build a bare mirror the way sync lays them out.
	src := t.TempDir()
	git := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_AUTHOR_DATE=2026-01-01T00:00:00Z",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git(src, "init", "-q")
	if err := os.MkdirAll(filepath.Join(src, "api", "v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "api", "v1", "demo.proto"), []byte(demoProto), 0o644); err != nil {
		t.Fatal(err)
	}
	// This legal Git filename is deliberately outside the harness's readable
	// path policy. It must still survive into the persisted corpus file count.
	if err := os.WriteFile(filepath.Join(src, "-fixture.txt"), []byte("non-candidate fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(src, "add", ".")
	git(src, "commit", "-q", "-m", "add proto")
	head := git(src, "rev-parse", "HEAD")

	mirror := filepath.Join(dataDir, "repos", filepath.FromSlash(repoName)+".git")
	if err := os.MkdirAll(filepath.Dir(mirror), 0o755); err != nil {
		t.Fatal(err)
	}
	git(".", "clone", "-q", "--mirror", src, mirror)

	if err := s.UpsertRepo(ctx, store.Repo{Name: repoName, CloneURL: "file://" + src}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoIndexed(ctx, repoName, head, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	w := &extract.Worker{
		Repos: s, Evidence: s,
		NewCorpus:  extract.GitCorpus(dataDir),
		Extractors: []extract.Extractor{protodecl.New()},
	}
	if err := w.Handle(ctx, store.Job{Target: repoName}); err != nil {
		t.Fatalf("handle: %v", err)
	}

	run, err := s.LatestPublishedRun(ctx, repoName, "proto-contract")
	if err != nil {
		t.Fatalf("latest run: %v", err)
	}
	if run.Commit != head {
		t.Fatalf("run pinned to %s, want %s", run.Commit, head)
	}
	if run.Coverage.CorpusFileCount != 2 || run.Coverage.CandidateFileCount != 1 ||
		run.Coverage.ReadFileCount != 1 || run.Coverage.ReadBytes != int64(len(demoProto)) ||
		len(run.Coverage.SourceScopeDigest) != len("sha256:")+64 ||
		!strings.HasPrefix(run.Coverage.SourceScopeDigest, "sha256:") {
		t.Fatalf("source coverage = %+v", run.Coverage)
	}
	if got := strings.Join(run.Coverage.Protocols, ","); got != "lineage-provisional-repo-path-v1,protobuf" {
		t.Fatalf("coverage protocols = %q", got)
	}

	ops, err := s.ListAssertions(ctx, store.AssertionQuery{Repo: repoName, Predicate: "DECLARES_OPERATION"})
	if err != nil {
		t.Fatalf("list ops: %v", err)
	}
	fields, err := s.ListAssertions(ctx, store.AssertionQuery{Repo: repoName, Predicate: "DECLARES_FIELD"})
	if err != nil {
		t.Fatalf("list fields: %v", err)
	}
	wantOps := map[string]bool{"demo.Greeter/SayHello": true, "demo.Greeter/SayGoodbye": true}
	for _, a := range ops {
		delete(wantOps, a.Object)
		if a.Lineage == "" || a.Tier != store.TierExact {
			t.Fatalf("operation assertion incomplete: %+v", a)
		}
	}
	if len(wantOps) != 0 {
		t.Fatalf("missing operations: %v (got %v)", wantOps, ops)
	}
	wantFields := map[string]bool{
		"demo.HelloRequest#1": true, "demo.HelloRequest#7": true,
		"demo.HelloReply#1": true,
	}
	for _, a := range fields {
		delete(wantFields, a.Object)
	}
	if len(wantFields) != 0 {
		t.Fatalf("missing fields: %v", wantFields)
	}

	// Resolve every stored support through the published-only store API and
	// slice the immutable Git blob. This proves the persisted click-through,
	// rather than merely re-running the extractor in memory.
	corpus := extract.GitCorpus(dataDir).New(repoName, head)
	if err := corpus.WalkFiles(ctx, func(string) error { return nil }); err != nil {
		t.Fatalf("walk corpus: %v", err)
	}
	allAssertions := append(append([]store.Assertion(nil), ops...), fields...)
	foundRetries := false
	foundMultilineRPC := false
	for _, assertion := range allAssertions {
		if assertion.RunID != run.ID || len(assertion.Supporting) != 1 {
			t.Fatalf("assertion has invalid run/support binding: %+v", assertion)
		}
		resolution, err := s.ResolveEvidence(ctx, repoName, assertion.RunID, assertion.Supporting[0])
		if err != nil {
			t.Fatalf("resolve %s: %v", assertion.Object, err)
		}
		if resolution.Atom.ID != assertion.Supporting[0] || len(resolution.Occurrences) == 0 {
			t.Fatalf("incomplete resolution for %s: %+v", assertion.Object, resolution)
		}
		matchedSubject := false
		for _, occurrence := range resolution.Occurrences {
			if occurrence.Repo != repoName || occurrence.Commit != head ||
				occurrence.RunID != run.ID || occurrence.VisibilityScope != "repo:"+repoName {
				t.Fatalf("occurrence not pinned/authorized: %+v", occurrence)
			}
			blob, err := corpus.Read(ctx, occurrence.Path)
			if err != nil {
				t.Fatalf("read %s: %v", occurrence.Path, err)
			}
			start, end := resolution.Atom.StartByte, resolution.Atom.EndByte
			if start < 0 || end <= start || end > len(blob.Content) ||
				resolution.Atom.BlobDigest != blob.Digest {
				t.Fatalf("invalid stored atom span/digest: %+v", resolution.Atom)
			}
			source := blob.Content[start:end]
			if source == "" {
				t.Fatalf("empty source span for %s", assertion.Object)
			}
			if occurrence.Path == assertion.Subject {
				matchedSubject = true
			}
			if assertion.Object == "demo.HelloRequest#7" {
				if source != "int32 retries = 7;" {
					t.Fatalf("retries span slices to %q", source)
				}
				if assertion.Detail != `{"schema":"proto-field-detail-v1","name":"retries"}` {
					t.Fatalf("retries detail = %q", assertion.Detail)
				}
				foundRetries = true
			}
			if assertion.Object == "demo.Greeter/SayHello" {
				if source != "rpc SayHello(HelloRequest) returns (HelloReply) {\n}" {
					t.Fatalf("multiline RPC span slices to %q", source)
				}
				if occurrence.StartLine != 6 || occurrence.EndLine != 7 {
					t.Fatalf("multiline RPC line span = %d-%d, want 6-7",
						occurrence.StartLine, occurrence.EndLine)
				}
				foundMultilineRPC = true
			}
		}
		if !matchedSubject {
			t.Fatalf("no occurrence for assertion subject %s", assertion.Subject)
		}
	}
	if !foundRetries {
		t.Fatal("stored retries evidence not found")
	}
	if !foundMultilineRPC {
		t.Fatal("stored multiline RPC evidence not found")
	}

	// Determinism: re-running the worker on the same commit short-circuits
	// (no new run), and re-extraction yields identical atom IDs.
	if err := w.Handle(ctx, store.Job{Target: repoName}); err != nil {
		t.Fatalf("second handle: %v", err)
	}
	run2, _ := s.LatestPublishedRun(ctx, repoName, "proto-contract")
	if run2.ID != run.ID {
		t.Fatalf("short-circuit failed: new run %s", run2.ID)
	}
}
