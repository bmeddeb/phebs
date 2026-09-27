package store_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/bmeddeb/phebs/internal/typedworkspace"
)

// This runs the real portable owner constructors and store APIs together.
// The directory inode is deliberately synthetic: no filesystem readiness is
// inferred, and no unsupported-platform filesystem API is exercised.
func TestTypedIndexCustodyOwnerAPICompatibility(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx := t.Context()
	s, err := store.OpenLocalMemory(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	hash := func(b []byte) string { h := sha256.Sum256(b); return "sha256:" + hex.EncodeToString(h[:]) }
	encode := func(v any) []byte {
		t.Helper()
		raw, e := json.Marshal(v)
		if e != nil {
			t.Fatal(e)
		}
		return raw
	}
	repo := "example.invalid/owner-api"
	if err = s.UpsertRepo(ctx, store.Repo{Name: repo}); err != nil {
		t.Fatal(err)
	}
	if err = s.SetRepoIndexed(ctx, repo, strings.Repeat("a", 40), time.Now()); err != nil {
		t.Fatal(err)
	}
	tool := typedindex.Tool{Version: "0.2.7", Digest: hash([]byte("tool"))}
	profile, err := typedindex.DecodeProfile(ctx, encode(typedindex.ProfileDefinition{Schema: typedindex.ProfileSchema, Name: "reduced", Provider: typedindex.ProviderID, Tools: typedindex.Tools{Bazel: tool, RulesGo: tool, Go: tool, Driver: tool, Indexer: tool, Planner: tool, Launcher: tool}, Config: typedindex.ReducedConfig(), Policy: typedindex.MeasuredPolicy(), BundleDigest: hash([]byte("bundle")), ImageDigest: hash([]byte("image"))}))
	if err != nil {
		t.Fatal(err)
	}
	intent, err := s.InstallTypedProfile(ctx, repo, profile, hash([]byte("universe")), 0)
	if err != nil {
		t.Fatal(err)
	}
	source, err := s.GetTypedSource(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	request := typedindex.NewRequest(source, profile, uint64(intent.ProfileEpoch), intent.UniverseDigest, "owner")
	if _, err = s.EnqueueTypedIndex(ctx, repo, encode(request)); err != nil {
		t.Fatal(err)
	}
	spec, err := s.TypedIndexSchedule(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.EnqueueGenerationSchedule(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ExpandGenerationSchedule(ctx, repo, spec.Stage, spec.Generation); err != nil {
		t.Fatal(err)
	}
	chunk, err := s.ClaimGenerationChunk(ctx, store.GenerationResourceTypedIndex, "owner")
	if err != nil {
		t.Fatal(err)
	}
	work, err := s.BeginTypedIndex(ctx, *chunk)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := typedworkspace.NewOwnerIdentity(work.Parent, chunk.Identity, chunk.LeaseToken)
	if err != nil {
		t.Fatal(err)
	}
	if owner.AttemptDigest != work.AttemptDigest || owner.PlanningDigest != work.RootDigest || owner.LeaseDigest != store.GenerationLeaseTokenDigest(chunk.LeaseToken) {
		t.Fatal("owner/store identities diverge")
	}
	manifest := typedworkspace.OwnerManifest{Schema: typedworkspace.OwnerSchema, Identity: owner, Directory: typedworkspace.Node{Path: owner.RelativeName(), Directory: true, Device: 1, Inode: 42}, Budget: typedworkspace.OwnerBudget{Bytes: 16 * typedworkspace.MaxOwnerBytes, Inodes: 16}, Revision: 1}
	if manifest.Digest() == "" {
		t.Fatal("real owner manifest constructor refused fixture")
	}
	reference := store.TypedIndexCustody{PlanningDigest: owner.PlanningDigest, AttemptDigest: owner.AttemptDigest, ManifestDigest: manifest.Digest(), Revision: manifest.Revision, DirectoryDevice: manifest.Directory.Device, DirectoryInode: manifest.Directory.Inode}
	domain := store.TypedIndexGrowthDomain{Device: 1, BaseInode: 2, BlockBytes: 4096, TotalBytes: 1 << 30, AvailableBytes: 1 << 29, TotalInodes: 1 << 20, FreeInodes: 1 << 19, FutureBytes: 1 << 20, FutureInodes: 100}
	if _, err = s.AcquireTypedIndexGrowth(ctx, *chunk, store.TypedIndexGrowthSpec{Workspace: domain, Host: domain}); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveTypedIndexCustody(ctx, *chunk, "", reference); err != nil {
		t.Fatal(err)
	}
	reopened, err := s.BeginTypedIndex(ctx, *chunk)
	if err != nil || reopened.Custody == nil || *reopened.Custody != reference {
		t.Fatalf("stored owner reference: %+v %v", reopened, err)
	}
}
