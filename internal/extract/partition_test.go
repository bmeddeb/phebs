package extract

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extract/extractors/protodecl"
	"github.com/bmeddeb/phebs/internal/extract/extractors/thriftdecl"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/store"
)

type partitionExecutorCorpus struct {
	repository string
	commit     string
	path       string
	content    string
}

func (corpus partitionExecutorCorpus) RepoName() string { return corpus.repository }
func (corpus partitionExecutorCorpus) Commit() string   { return corpus.commit }
func (corpus partitionExecutorCorpus) WalkFiles(ctx context.Context, visit func(string) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return visit(corpus.path)
}
func (corpus partitionExecutorCorpus) Read(_ context.Context, path string) (sdk.Blob, error) {
	if path != corpus.path {
		return sdk.Blob{}, store.ErrNotFound
	}
	digest := sha256.Sum256([]byte(corpus.content))
	return sdk.Blob{Content: corpus.content, Digest: "sha256:" + hex.EncodeToString(digest[:])}, nil
}

type partitionExecutorLease struct {
	corpus      sdk.Corpus
	records     int
	memberBytes int64
	members     int64
}

type inflatedReferenceEvidence struct {
	*memoryEvidence
	references int64
}

func (evidence inflatedReferenceEvidence) GetEvidenceChunkAccounting(
	ctx context.Context,
	runID,
	chunkID string,
) (store.EvidenceChunkAccounting, error) {
	receipt, err := evidence.memoryEvidence.GetEvidenceChunkAccounting(ctx, runID, chunkID)
	receipt.ReferenceDelta = evidence.references
	return receipt, err
}

func (m *memoryEvidence) GetEvidenceChunkAccounting(
	_ context.Context,
	runID,
	chunkID string,
) (store.EvidenceChunkAccounting, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runs[runID] == nil || m.batchCount == 0 {
		return store.EvidenceChunkAccounting{}, store.ErrNotFound
	}
	batch := m.batches[len(m.batches)-1]
	return store.EvidenceChunkAccounting{
		RunID: runID, ChunkID: chunkID,
		ContentDigest: "sha256:" + strings.Repeat("a", 64),
		FactCount:     int64(len(batch.atoms)), RowDelta: int64(len(batch.assocs) + len(batch.asserts)),
		ReferenceDelta: int64(len(batch.asserts)),
	}, nil
}

func (lease partitionExecutorLease) Corpus() sdk.Corpus                { return lease.corpus }
func (lease partitionExecutorLease) CandidateRecords() int             { return lease.records }
func (partitionExecutorLease) TypedSourceRecords() int                 { return 0 }
func (partitionExecutorLease) TypedInput() *candidate.SparseTypedInput { return nil }
func (lease partitionExecutorLease) MemberBytes() int64                { return lease.memberBytes }
func (lease partitionExecutorLease) Members() int64                    { return lease.members }
func (partitionExecutorLease) Release()                                {}

func TestEvidencePartitionExecutorStagesOnlyBoundedT407Chunks(t *testing.T) {
	plan, content, commit := partitionExecutorPlan(t)
	evidence := newMemoryEvidence()
	run, err := evidence.BeginExtractionRun(t.Context(), store.ExtractionScope{
		Repository: plan.Repository, Commit: commit, Domain: plan.Domain,
	}, plan.ExtractorVersion)
	if err != nil {
		t.Fatal(err)
	}
	extractor := unitExtractor{
		domain: plan.Domain, version: plan.ExtractorVersion,
		candidate: func(path string) bool { return strings.HasSuffix(path, ".proto") },
		extract: func(ctx context.Context, corpus sdk.Corpus, emit sdk.Emit) (sdk.Coverage, error) {
			var path string
			if err := corpus.WalkFiles(ctx, func(value string) error { path = value; return nil }); err != nil {
				return sdk.Coverage{}, err
			}
			blob, err := corpus.Read(ctx, path)
			if err != nil {
				return sdk.Coverage{}, err
			}
			return sdk.Coverage{}, emit(sdk.Fact{
				Path: path, StartLine: 1, EndLine: 1,
				Atom: sdk.AtomInput{
					SchemaVersion: "partition-v1", BlobDigest: blob.Digest,
					StartByte: 0, EndByte: len(blob.Content), RuleID: "partition-rule",
					AdapterConfigDigest: "none", FactFingerprint: "partition-fact",
				},
				Assertion: sdk.AssertionInput{
					Predicate: "DECLARES", Subject: path, Object: "Fixture",
					Tier: store.TierExact, CodeRole: "production",
				},
			})
		},
	}
	lease := partitionExecutorLease{
		corpus: partitionExecutorCorpus{
			repository: plan.Repository, commit: commit,
			path: "schema.proto", content: content,
		},
		records: 1, memberBytes: min(20, plan.Expected[0].Reservation.MemberBytes), members: 1,
	}
	executor := &EvidencePartitionExecutor{Evidence: evidence, Extractors: []Extractor{extractor}}
	result, err := executor.ExecutePartition(t.Context(), plan, 0, lease, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != candidate.PartitionResultSuccess || result.Totals.Facts != 1 ||
		result.Totals.Rows != 2 || result.Totals.References != 1 ||
		result.Totals.MemberBytes != lease.memberBytes || evidence.batchCount != 1 || evidence.stagedFacts != 1 {
		t.Fatalf("partition result/evidence = %+v / batches=%d facts=%d", result, evidence.batchCount, evidence.stagedFacts)
	}
	if _, err := candidate.BuildPartitionResult(plan, 0, result); err != nil {
		t.Fatalf("staged result does not close under T40.9: %v", err)
	}

	tooLarge := lease
	tooLarge.memberBytes = plan.Expected[0].Reservation.MemberBytes + 1
	if _, err := executor.ExecutePartition(t.Context(), plan, 0, tooLarge, run.ID); err == nil || evidence.batchCount != 1 {
		t.Fatalf("member over-reservation error/batches = %v/%d", err, evidence.batchCount)
	}

	inflated := inflatedReferenceEvidence{
		memoryEvidence: newMemoryEvidence(),
		references:     plan.Expected[0].Reservation.References + 1,
	}
	inflatedRun, err := inflated.BeginExtractionRun(t.Context(), store.ExtractionScope{
		Repository: plan.Repository, Commit: commit, Domain: plan.Domain,
	}, plan.ExtractorVersion)
	if err != nil {
		t.Fatal(err)
	}
	inflatedExecutor := &EvidencePartitionExecutor{
		Evidence: inflated, Extractors: []Extractor{extractor},
	}
	if _, err := inflatedExecutor.ExecutePartition(
		t.Context(), plan, 0, lease, inflatedRun.ID,
	); err == nil || !strings.Contains(err.Error(), "exceeds its partition reservation") {
		t.Fatalf("inflated reference accounting error = %v", err)
	}
}

func TestEvidencePartitionExecutorReturnsClosedTerminalResult(t *testing.T) {
	plan, content, commit := partitionExecutorPlan(t)
	evidence := newMemoryEvidence()
	run, err := evidence.BeginExtractionRun(t.Context(), store.ExtractionScope{
		Repository: plan.Repository, Commit: commit, Domain: plan.Domain,
	}, plan.ExtractorVersion)
	if err != nil {
		t.Fatal(err)
	}
	extractor := unitExtractor{
		domain: plan.Domain, version: plan.ExtractorVersion,
		candidate: func(path string) bool { return strings.HasSuffix(path, ".proto") },
		extract: func(context.Context, sdk.Corpus, sdk.Emit) (sdk.Coverage, error) {
			return sdk.Coverage{}, errors.New("private deterministic detail")
		},
	}
	lease := partitionExecutorLease{
		corpus: partitionExecutorCorpus{
			repository: plan.Repository, commit: commit,
			path: "schema.proto", content: content,
		},
		records: 1, memberBytes: min(20, plan.Expected[0].Reservation.MemberBytes), members: 1,
	}
	executor := &EvidencePartitionExecutor{Evidence: evidence, Extractors: []Extractor{extractor}}
	result, err := executor.ExecutePartition(t.Context(), plan, 0, lease, run.ID)
	if !store.IsTerminal(err) || result.Disposition != candidate.PartitionResultTerminalRefusal ||
		result.Reason != "deterministic_refusal" || result.Totals != (candidate.DomainResultTotals{}) {
		t.Fatalf("terminal executor result = %+v, %v", result, err)
	}
	if _, err := candidate.BuildPartitionResult(plan, 0, result); err != nil {
		t.Fatalf("terminal result does not close under T40.9: %v", err)
	}
}

func TestEvidencePartitionExecutorKeepsFileLocalProtoAndThriftGapsSettled(t *testing.T) {
	tests := []struct {
		name, domain, version, suffix, content, marker string
		extractor                                      Extractor
	}{
		{
			name: "unresolved protobuf declaration", domain: "proto-contract",
			version: "3.0.0", suffix: ".proto",
			content: `syntax = "proto3"; message Fixture { Missing value = 1; }` + "\n",
			marker:  "DECLARATION_NOT_FOUND", extractor: protodecl.New(),
		},
		{
			name: "well formed unsupported thrift construct", domain: "thrift-contract",
			version: "1.0.0", suffix: ".thrift",
			content: "struct Fixture { string value }\n",
			marker:  "THRIFT_EXTRACTION_GAP", extractor: thriftdecl.New(),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan, path, commit := partitionExecutorPlanFor(
				t, test.domain, test.version, test.suffix, test.content,
			)
			evidence := newMemoryEvidence()
			run, err := evidence.BeginExtractionRun(t.Context(), store.ExtractionScope{
				Repository: plan.Repository, Commit: commit, Domain: plan.Domain,
			}, plan.ExtractorVersion)
			if err != nil {
				t.Fatal(err)
			}
			lease := partitionExecutorLease{
				corpus: partitionExecutorCorpus{
					repository: plan.Repository, commit: commit, path: path, content: test.content,
				},
				records: 1, memberBytes: min(20, plan.Expected[0].Reservation.MemberBytes), members: 1,
			}
			executor := &EvidencePartitionExecutor{Evidence: evidence, Extractors: []Extractor{test.extractor}}
			result, err := executor.ExecutePartition(t.Context(), plan, 0, lease, run.ID)
			if err != nil || result.Disposition != candidate.PartitionResultSuccess {
				t.Fatalf("gap partition result = %+v, %v", result, err)
			}
			found := false
			for _, batch := range evidence.batches {
				for _, assertion := range batch.asserts {
					if strings.Contains(assertion.Detail, test.marker) || assertion.Predicate == test.marker {
						found = true
					}
				}
			}
			if !found {
				t.Fatalf("classified marker %q absent from staged evidence", test.marker)
			}
		})
	}
}

func partitionExecutorPlan(t *testing.T) (candidate.DomainResultPlan, string, string) {
	plan, _, commit := partitionExecutorPlanFor(
		t, "proto-contract", "partition-test-v1", ".proto", "message Fixture {}\n",
	)
	return plan, "message Fixture {}\n", commit
}

func partitionExecutorPlanFor(
	t *testing.T,
	domainName,
	version,
	suffix,
	content string,
) (candidate.DomainResultPlan, string, string) {
	t.Helper()
	repository := "example.invalid/executor"
	directory := t.TempDir()
	partitionGit(t, directory, "init", "-q")
	partitionGit(t, directory, "config", "user.email", "executor@example.invalid")
	partitionGit(t, directory, "config", "user.name", "Executor Test")
	path := "schema" + suffix
	if err := os.WriteFile(filepath.Join(directory, path), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	partitionGit(t, directory, "add", "--", path)
	partitionGit(t, directory, "commit", "-q", "-m", "fixture")
	commit := strings.TrimSpace(partitionGit(t, directory, "rev-parse", "HEAD"))
	policies := []candidate.Policy{{
		Domain: domainName, Version: version,
		EnumerationPolicy: domainName + "-partition-paths-v1", Plane: candidate.PlaneRepository,
		Enumerate: func(path string) bool { return strings.HasSuffix(path, suffix) },
		Required:  func(path string) bool { return strings.HasSuffix(path, suffix) },
	}}
	identities, err := candidate.PolicyIdentities(policies)
	if err != nil {
		t.Fatal(err)
	}
	policyDigest, err := candidate.PolicyDigest(identities)
	if err != nil {
		t.Fatal(err)
	}
	candidateDirectory := t.TempDir()
	manifest, err := candidate.Build(t.Context(), candidate.Request{
		RepoDir: directory, OutputDir: candidateDirectory,
		Repository: repository, Commit: commit, Policies: policies,
	})
	if err != nil {
		t.Fatal(err)
	}
	publication, err := candidate.Open(candidateDirectory, candidate.Expected{
		Repository: repository, Commit: commit, Policies: identities,
		PolicyDigest: policyDigest, GenerationDigest: manifest.GenerationDigest,
		ManifestDigest: manifest.Digest,
	})
	if err != nil {
		t.Fatal(err)
	}
	sparseDirectory := t.TempDir()
	sparseRoot, err := candidate.BuildSparseRoot(t.Context(), sparseDirectory, publication, nil)
	if err != nil {
		t.Fatal(err)
	}
	sparse, err := candidate.OpenSparse(t.Context(), sparseDirectory, candidateDirectory, manifest.State(), sparseRoot.Digest, nil)
	if err != nil {
		t.Fatal(err)
	}
	domain, err := sparse.OpenDomain(t.Context(), domainName, version)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := extractionpublication.BuildReservedPlan(domain, candidate.DomainResultAuthority{
		SourceGenerationDigest:      "sha256:" + strings.Repeat("c", 64),
		ObservationGenerationDigest: "sha256:" + strings.Repeat("d", 64),
		ExtractorVersion:            version,
		ExtractionPolicyDigest:      "sha256:" + strings.Repeat("e", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	return plan, path, commit
}

func partitionGit(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return string(output)
}
