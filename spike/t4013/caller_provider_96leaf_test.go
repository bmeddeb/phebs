package t4013

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/callerexecute"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/candidatejob"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	t40r1CallerProvider96Schema       = "t4013-caller-provider-96leaf-physical-distribution-v1"
	t40r1CallerProvider96Env          = "T40R1_CALLER_PROVIDER_96LEAF_DIAGNOSTIC"
	t40r1CallerProvider96Files        = 261_769
	t40r1CallerProvider96Leaves       = 96
	t40r1CallerProvider96Profile      = "physical-261769-96leaf-v1"
	t40r1CallerProvider96LeafDigest   = "sha256:3e71323ee770e46b01dd7e8f175224fb059f825f2e3b5b46304e73aa88d69ea3"
	t40r1CallerProvider96ReplayDigest = "sha256:8e0931bbe3a25a87466fa3550b35f227afba29e04f11120217fc6ee8583d8027"
)

var t40r1CallerProvider96Content = []byte("package neutral\n\nfunc Call() {}\n")

type t40r1CallerProvider96Prefix struct {
	Bits       int `json:"bits"`
	Leaves     int `json:"leaves"`
	Records    int `json:"records"`
	MinRecords int `json:"min_records"`
	MaxRecords int `json:"max_records"`
}

type t40r1CallerProvider96Domain struct {
	Domain          string `json:"domain"`
	Version         string `json:"version"`
	LeafReplays     int    `json:"leaf_replays"`
	VisitedRecords  int    `json:"visited_records"`
	DeclaredBytes   int64  `json:"declared_bytes"`
	MemberReadBytes int64  `json:"member_read_bytes"`
	ReplayDigest    string `json:"replay_digest"`
}

type t40r1CallerProvider96Witness struct {
	Schema                  string                        `json:"schema"`
	Method                  string                        `json:"method"`
	Profile                 string                        `json:"profile"`
	RegularFiles            int                           `json:"regular_files"`
	UniqueGitBlobs          int                           `json:"unique_git_blobs"`
	SharedBlobDeclaredBytes int                           `json:"shared_blob_declared_bytes"`
	CorpusDeclaredBytes     int64                         `json:"corpus_declared_bytes"`
	CandidateRecords        int                           `json:"candidate_records"`
	CallerLeaves            int                           `json:"caller_leaves"`
	LeafSetDigest           string                        `json:"leaf_set_digest"`
	LeafContentBytes        int64                         `json:"leaf_content_bytes"`
	LeafDeclaredBytes       int64                         `json:"leaf_declared_bytes"`
	MinLeafRecords          int                           `json:"min_leaf_records"`
	MaxLeafRecords          int                           `json:"max_leaf_records"`
	PrefixDistribution      []t40r1CallerProvider96Prefix `json:"prefix_distribution"`
	PeakSpoolBytes          int64                         `json:"peak_spool_bytes"`
	ManifestDigestBound     bool                          `json:"manifest_digest_bound"`
	ControlRevisionBound    bool                          `json:"control_revision_bound"`
	ProviderLeavesExact     bool                          `json:"provider_leaves_exact"`
	Domains                 []t40r1CallerProvider96Domain `json:"domains"`
	TotalLeafReplays        int                           `json:"total_leaf_replays"`
	TotalVisitedRecords     int                           `json:"total_visited_records"`
	TotalMemberReadBytes    int64                         `json:"total_member_read_bytes"`
	Boundary                string                        `json:"boundary"`
	ClearsBoundary          bool                          `json:"clears_boundary"`
	EstablishesScalePass    bool                          `json:"establishes_scale_pass"`
	AuthorizesCeremony      bool                          `json:"authorizes_ceremony"`
}

type t40r1CallerProvider96Measurement struct {
	BuildDuration        time.Duration
	ProviderOpenDuration time.Duration
	ReplayDuration       time.Duration
	BuildAllocatedBytes  uint64
	OpenAllocatedBytes   uint64
	ReplayAllocatedBytes uint64
	CandidateDiskBytes   int64
	RepositoryDiskBytes  int64
}

func TestT40R1CallerProvider96LeafPhysicalDistributionDiagnostic(t *testing.T) {
	if os.Getenv(t40r1CallerProvider96Env) != "1" {
		t.Skip("set " + t40r1CallerProvider96Env + "=1 to run the provider-only 96-leaf diagnostic")
	}
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary is not installed")
	}
	witness, measurement := runT40R1CallerProvider96LeafDiagnostic(t)
	encoded, err := json.MarshalIndent(witness, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf(
		"build=%s open=%s replay=%s build_alloc=%d open_alloc=%d replay_alloc=%d candidate_disk=%d repository_disk=%d",
		measurement.BuildDuration, measurement.ProviderOpenDuration,
		measurement.ReplayDuration, measurement.BuildAllocatedBytes,
		measurement.OpenAllocatedBytes, measurement.ReplayAllocatedBytes,
		measurement.CandidateDiskBytes, measurement.RepositoryDiskBytes,
	)
	raw, err := os.ReadFile("caller-provider-96leaf-physical-distribution.json")
	if err != nil {
		t.Fatalf("retained 96-leaf provider witness unavailable: %v\n%s", err, encoded)
	}
	var retained t40r1CallerProvider96Witness
	if err := json.Unmarshal(raw, &retained); err != nil || !reflect.DeepEqual(retained, witness) {
		t.Fatalf("retained 96-leaf provider witness differs: %v\n%s", err, encoded)
	}
	t.Log(string(encoded))
}

func TestT40R1RetainedCallerProvider96LeafPhysicalDistributionIsClosed(t *testing.T) {
	raw, err := os.ReadFile("caller-provider-96leaf-physical-distribution.json")
	if err != nil {
		t.Fatal(err)
	}
	var witness t40r1CallerProvider96Witness
	if err := json.Unmarshal(raw, &witness); err != nil {
		t.Fatal(err)
	}
	if witness.Schema != t40r1CallerProvider96Schema ||
		witness.Profile != t40r1CallerProvider96Profile ||
		witness.RegularFiles != t40r1CallerProvider96Files || witness.UniqueGitBlobs != 1 ||
		witness.SharedBlobDeclaredBytes != len(t40r1CallerProvider96Content) ||
		witness.CorpusDeclaredBytes != int64(t40r1CallerProvider96Files*len(t40r1CallerProvider96Content)) ||
		witness.CandidateRecords != t40r1CallerProvider96Files ||
		witness.CallerLeaves != t40r1CallerProvider96Leaves ||
		witness.LeafSetDigest != t40r1CallerProvider96LeafDigest ||
		witness.LeafContentBytes != 93_975_071 ||
		witness.LeafDeclaredBytes != witness.CorpusDeclaredBytes ||
		witness.MinLeafRecords < 1 || witness.MaxLeafRecords > candidate.MaxRecordsPerArtifact ||
		!reflect.DeepEqual(witness.PrefixDistribution, []t40r1CallerProvider96Prefix{
			{Bits: 6, Leaves: 32, Records: 129_114, MinRecords: 3_946, MaxRecords: 4_096},
			{Bits: 7, Leaves: 64, Records: 132_655, MinRecords: 1_953, MaxRecords: 2_166},
		}) || witness.PeakSpoolBytes != 117_543_780 ||
		!witness.ManifestDigestBound || !witness.ControlRevisionBound ||
		!witness.ProviderLeavesExact || len(witness.Domains) != 2 ||
		witness.TotalLeafReplays != 2*t40r1CallerProvider96Leaves ||
		witness.TotalVisitedRecords != 2*t40r1CallerProvider96Files ||
		witness.TotalMemberReadBytes != 2*witness.LeafContentBytes ||
		witness.Boundary != "provider_only_96_leaf_physical_distribution" ||
		!witness.ClearsBoundary || witness.EstablishesScalePass || witness.AuthorizesCeremony {
		t.Fatalf("retained 96-leaf provider witness is not closed: %+v", witness)
	}
	expectedDomains := []string{"grpc-caller", "thrift-caller"}
	for index, domain := range witness.Domains {
		if domain.Domain != expectedDomains[index] || domain.Version != "1.5.0" ||
			domain.LeafReplays != t40r1CallerProvider96Leaves ||
			domain.VisitedRecords != t40r1CallerProvider96Files ||
			domain.DeclaredBytes != witness.CorpusDeclaredBytes ||
			domain.MemberReadBytes != witness.LeafContentBytes ||
			domain.ReplayDigest != t40r1CallerProvider96ReplayDigest {
			t.Fatalf("retained 96-leaf domain replay is not exact: %+v", domain)
		}
	}
}

func runT40R1CallerProvider96LeafDiagnostic(
	t *testing.T,
) (t40r1CallerProvider96Witness, t40r1CallerProvider96Measurement) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Minute)
	defer cancel()
	dataDir := t.TempDir()
	state, err := store.OpenLocal(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close(context.Background()) })
	repositoryDirectory, commit, blobOID := t40r1CallerProvider96Repository(t, ctx)
	measurement := t40r1CallerProvider96Measurement{}

	extractors := []extract.Extractor{gocaller.NewGRPC(), gocaller.NewThrift()}
	policies, err := extract.CandidatePolicies(extractors)
	if err != nil {
		t.Fatal(err)
	}
	policySet, err := candidatejob.CompilePolicies(extractors)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := callerexecute.NewRegistry(extractors)
	if err != nil {
		t.Fatal(err)
	}
	candidateRoot := candidatejob.CandidateRoot(dataDir)
	if err := os.MkdirAll(candidateRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	var metrics candidate.BuildMetrics
	buildBefore := t40r1CallerProvider96MemStats()
	buildStarted := time.Now()
	manifest, err := candidate.Build(ctx, candidate.Request{
		RepoDir: repositoryDirectory, OutputDir: candidateRoot,
		Repository: t40r1CallerUpstreamRepository, Commit: commit,
		Policies: policies, Metrics: &metrics,
	})
	measurement.BuildDuration = time.Since(buildStarted)
	measurement.BuildAllocatedBytes = t40r1CallerProvider96AllocatedSince(buildBefore)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.UpsertRepo(ctx, store.Repo{
		Name:     t40r1CallerUpstreamRepository,
		CloneURL: "https://github.com/phebs/t40r1-caller-provider-96.invalid.git",
	}); err != nil {
		t.Fatal(err)
	}
	if err := state.SetRepoIndexed(
		ctx, t40r1CallerUpstreamRepository, commit, time.Now().UTC(),
	); err != nil {
		t.Fatal(err)
	}
	if err := state.PublishCandidateManifest(ctx, store.CandidateManifestPublication{
		Repository: t40r1CallerUpstreamRepository, HeadCommit: commit,
		PolicyDigest: manifest.PolicyDigest, ManifestDigest: manifest.Digest,
		GenerationDigest: manifest.GenerationDigest,
		ManifestPath:     candidate.ManifestName(t40r1CallerUpstreamRepository),
	}); err != nil {
		t.Fatal(err)
	}
	pointer, err := state.GetCandidateManifestPublication(
		ctx, t40r1CallerUpstreamRepository,
	)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := candidatejob.NewProvider(dataDir, state, policySet)
	if err != nil {
		t.Fatal(err)
	}
	openBefore := t40r1CallerProvider96MemStats()
	openStarted := time.Now()
	plan, err := provider.OpenCandidateCallerPlan(ctx, extract.CandidateManifestRequest{
		Repository: t40r1CallerUpstreamRepository, Commit: commit,
		Domains: registry.CandidateDomains(),
	})
	measurement.ProviderOpenDuration = time.Since(openStarted)
	measurement.OpenAllocatedBytes = t40r1CallerProvider96AllocatedSince(openBefore)
	if err != nil {
		t.Fatal(err)
	}

	witness := t40r1CallerProvider96Witness{
		Schema:       t40r1CallerProvider96Schema,
		Method:       "author 261,769 deterministic Git paths over one shared blob, build the production candidate-v4 generation, publish its exact store pointer, open the narrow production provider, and replay all 96 immutable physical leaves once for each caller domain",
		Profile:      t40r1CallerProvider96Profile,
		RegularFiles: t40r1CallerProvider96Files, UniqueGitBlobs: 1,
		SharedBlobDeclaredBytes: len(t40r1CallerProvider96Content),
		CorpusDeclaredBytes:     int64(t40r1CallerProvider96Files * len(t40r1CallerProvider96Content)),
		CandidateRecords:        manifest.Corpus.RegularCount,
		CallerLeaves:            len(manifest.CallerLeaves), PeakSpoolBytes: metrics.PeakSpoolBytes,
		ManifestDigestBound:  plan.Identity() == pointer.ManifestDigest,
		ControlRevisionBound: plan.CandidateControlRevision() == pointer.ControlRevision,
		Boundary:             "provider_only_96_leaf_physical_distribution",
	}
	witness.ProviderLeavesExact = t40r1CallerProvider96LeavesExact(
		manifest.CallerLeaves, plan.CallerLeaves(),
	)
	witness.LeafSetDigest, witness.PrefixDistribution,
		witness.LeafContentBytes, witness.LeafDeclaredBytes,
		witness.MinLeafRecords, witness.MaxLeafRecords =
		t40r1CallerProvider96LeafSummary(t, manifest.CallerLeaves)

	replayBefore := t40r1CallerProvider96MemStats()
	replayStarted := time.Now()
	for _, domain := range plan.CallerDomains() {
		current := t40r1CallerProvider96Domain{
			Domain: domain.Domain, Version: domain.Version,
		}
		digest := sha256.New()
		for _, leaf := range plan.CallerLeaves() {
			current.LeafReplays++
			current.MemberReadBytes += leaf.ContentBytes
			if err := plan.ForEachCallerLeafFile(
				ctx, domain.Domain, domain.Version, leaf,
				func(file extract.CandidateManifestFile) error {
					if file.ObjectID != blobOID || file.DeclaredBytes != int64(len(t40r1CallerProvider96Content)) ||
						file.SourceLane != candidate.SourceLaneBase || !file.Required || file.InUnit {
						return fmt.Errorf("physical caller record is not exact: %+v", file)
					}
					current.VisitedRecords++
					current.DeclaredBytes += file.DeclaredBytes
					t40r1CallerProvider96DigestRecord(digest, file)
					return nil
				},
			); err != nil {
				t.Fatal(err)
			}
		}
		current.ReplayDigest = "sha256:" + hex.EncodeToString(digest.Sum(nil))
		witness.Domains = append(witness.Domains, current)
		witness.TotalLeafReplays += current.LeafReplays
		witness.TotalVisitedRecords += current.VisitedRecords
		witness.TotalMemberReadBytes += current.MemberReadBytes
	}
	measurement.ReplayDuration = time.Since(replayStarted)
	measurement.ReplayAllocatedBytes = t40r1CallerProvider96AllocatedSince(replayBefore)
	measurement.CandidateDiskBytes = t40r1CallerProvider96DirectoryBytes(t, candidateRoot)
	measurement.RepositoryDiskBytes = t40r1CallerProvider96DirectoryBytes(t, repositoryDirectory)

	witness.ClearsBoundary =
		witness.CandidateRecords == t40r1CallerProvider96Files &&
			witness.CallerLeaves == t40r1CallerProvider96Leaves &&
			witness.MinLeafRecords > 0 && witness.MaxLeafRecords <= candidate.MaxRecordsPerArtifact &&
			witness.ManifestDigestBound && witness.ControlRevisionBound &&
			witness.ProviderLeavesExact && len(witness.Domains) == 2 &&
			witness.TotalLeafReplays == 2*t40r1CallerProvider96Leaves &&
			witness.TotalVisitedRecords == 2*t40r1CallerProvider96Files &&
			witness.TotalMemberReadBytes == 2*witness.LeafContentBytes &&
			witness.Domains[0].ReplayDigest == witness.Domains[1].ReplayDigest
	if !witness.ClearsBoundary {
		t.Fatalf("provider-only 96-leaf diagnostic did not clear: %+v", witness)
	}
	return witness, measurement
}

func t40r1CallerProvider96Repository(
	t *testing.T,
	ctx context.Context,
) (string, string, string) {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "repository.git")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	t40r1CallerPartitionedGit(t, ctx, directory, "init", "--bare", "-q")
	blobCommand := exec.CommandContext(
		ctx, "git", "--git-dir", directory, "hash-object", "-w", "--stdin",
	)
	blobCommand.Stdin = strings.NewReader(string(t40r1CallerProvider96Content))
	blobOutput, err := blobCommand.CombinedOutput()
	if err != nil {
		t.Fatalf("hash shared caller blob: %v: %s", err, blobOutput)
	}
	blobOID := strings.TrimSpace(string(blobOutput))
	command := exec.CommandContext(
		ctx, "git", "--git-dir", directory, "fast-import", "--quiet",
	)
	command.Env = append(slices.DeleteFunc(os.Environ(), func(value string) bool {
		return strings.HasPrefix(value, "GIT_")
	}), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	writer := bufio.NewWriterSize(stdin, 1<<20)
	message := "provider-only 96-leaf physical distribution"
	_, writeErr := fmt.Fprintf(writer,
		"commit refs/heads/main\nmark :1\nauthor T40R1 Provider <t40r1-provider@example.invalid> 946684800 +0000\ncommitter T40R1 Provider <t40r1-provider@example.invalid> 946684800 +0000\ndata %d\n%s\n",
		len(message), message,
	)
	for ordinal := 0; writeErr == nil && ordinal < t40r1CallerProvider96Files; ordinal++ {
		_, writeErr = fmt.Fprintf(
			writer, "M 100644 %s semantic/go/f%06d.go\n", blobOID, ordinal,
		)
	}
	if writeErr == nil {
		_, writeErr = writer.WriteString("\ndone\n")
	}
	if flushErr := writer.Flush(); writeErr == nil {
		writeErr = flushErr
	}
	if closeErr := stdin.Close(); writeErr == nil {
		writeErr = closeErr
	}
	waitErr := command.Wait()
	if writeErr != nil || waitErr != nil {
		t.Fatalf("author 96-leaf Git tree: write=%v wait=%v", writeErr, waitErr)
	}
	commit := strings.TrimSpace(t40r1CallerPartitionedGit(
		t, ctx, directory, "rev-parse", "refs/heads/main",
	))
	return directory, commit, blobOID
}

func t40r1CallerProvider96LeavesExact(
	manifest []candidate.CallerLeaf,
	provider []extract.CandidateCallerLeaf,
) bool {
	if len(manifest) != len(provider) {
		return false
	}
	for index, leaf := range manifest {
		opened := provider[index]
		if opened.Name != leaf.Name || opened.Ordinal != leaf.Ordinal ||
			opened.Prefix != leaf.Prefix || opened.PrefixBits != leaf.PrefixBits ||
			opened.RecordCount != leaf.RecordCount ||
			opened.DeclaredBytes != leaf.DeclaredBytes ||
			opened.ContentBytes != leaf.ContentBytes ||
			opened.ContentDigest != leaf.ContentDigest {
			return false
		}
	}
	return true
}

func t40r1CallerProvider96LeafSummary(
	t *testing.T,
	leaves []candidate.CallerLeaf,
) (string, []t40r1CallerProvider96Prefix, int64, int64, int, int) {
	t.Helper()
	type identity struct {
		Name          string `json:"name"`
		Ordinal       int    `json:"ordinal"`
		Prefix        string `json:"prefix"`
		PrefixBits    int    `json:"prefix_bits"`
		RecordCount   int    `json:"record_count"`
		DeclaredBytes int64  `json:"declared_bytes"`
		ContentBytes  int64  `json:"content_bytes"`
		ContentDigest string `json:"content_digest"`
	}
	identities := make([]identity, len(leaves))
	byBits := make(map[int]*t40r1CallerProvider96Prefix)
	var contentBytes, declaredBytes int64
	minRecords, maxRecords := 0, 0
	for index, leaf := range leaves {
		identities[index] = identity{
			Name: leaf.Name, Ordinal: leaf.Ordinal,
			Prefix: leaf.Prefix, PrefixBits: leaf.PrefixBits,
			RecordCount: leaf.RecordCount, DeclaredBytes: leaf.DeclaredBytes,
			ContentBytes: leaf.ContentBytes, ContentDigest: leaf.ContentDigest,
		}
		contentBytes += leaf.ContentBytes
		declaredBytes += leaf.DeclaredBytes
		if minRecords == 0 || leaf.RecordCount < minRecords {
			minRecords = leaf.RecordCount
		}
		if leaf.RecordCount > maxRecords {
			maxRecords = leaf.RecordCount
		}
		bucket := byBits[leaf.PrefixBits]
		if bucket == nil {
			bucket = &t40r1CallerProvider96Prefix{
				Bits: leaf.PrefixBits, MinRecords: leaf.RecordCount,
			}
			byBits[leaf.PrefixBits] = bucket
		}
		bucket.Leaves++
		bucket.Records += leaf.RecordCount
		if leaf.RecordCount < bucket.MinRecords {
			bucket.MinRecords = leaf.RecordCount
		}
		if leaf.RecordCount > bucket.MaxRecords {
			bucket.MaxRecords = leaf.RecordCount
		}
	}
	distribution := make([]t40r1CallerProvider96Prefix, 0, len(byBits))
	for _, bucket := range byBits {
		distribution = append(distribution, *bucket)
	}
	slices.SortFunc(distribution, func(left, right t40r1CallerProvider96Prefix) int {
		return left.Bits - right.Bits
	})
	raw, err := json.Marshal(identities)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(append([]byte(t40r1CallerProvider96Schema+"\x00"), raw...))
	return "sha256:" + hex.EncodeToString(sum[:]), distribution,
		contentBytes, declaredBytes, minRecords, maxRecords
}

func t40r1CallerProvider96DigestRecord(
	digest hash.Hash,
	file extract.CandidateManifestFile,
) {
	_, _ = digest.Write([]byte(file.Path))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write([]byte(file.ObjectID))
	_, _ = digest.Write([]byte{0})
}

func t40r1CallerProvider96MemStats() runtime.MemStats {
	var result runtime.MemStats
	runtime.ReadMemStats(&result)
	return result
}

func t40r1CallerProvider96AllocatedSince(before runtime.MemStats) uint64 {
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

func t40r1CallerProvider96DirectoryBytes(t *testing.T, root string) int64 {
	t.Helper()
	var total int64
	if err := filepath.WalkDir(root, func(
		path string,
		entry os.DirEntry,
		err error,
	) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			total += info.Size()
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return total
}
