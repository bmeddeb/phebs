package t421extractionprojection

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/t421sourceprojection"
)

func TestDeriveClosesExactDomainAndEmitsSourceFreeEvidence(t *testing.T) {
	fixture := newProjectionFixture(t)
	result, err := Derive(
		t.Context(), fixture.snapshot, fixture.domain, fixture.candidates, fixture.candidateProof,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Projection.Domain != "typed-local" || result.Projection.Availability != "admitted" ||
		result.Projection.MemberPartitions != 1 || result.Projection.TypedPartitions != 1 ||
		result.Projection.ApplicablePartitions != 2 || result.Projection.TypedScopeRecords != 1 ||
		result.Projection.TypedScopePathBytes != uint64(len("service/main.go")) ||
		result.Projection.TypedScopeEncodedBytes == 0 || !validDigest(result.Projection.TypedScopeSHA256) ||
		result.Projection.TypedScopeSHA256 != result.Projection.TypedScopeContentSHA256 {
		t.Fatalf("phase projection = %+v", result.Projection)
	}
	if !result.Root.Current || result.Root.Members.Records != 2 ||
		result.Root.Members.FramedBytes == 0 || !validDigest(result.Root.Members.SHA256) ||
		result.Root.GenerationSHA256 != "" || result.Root.ScheduleSHA256 != "" ||
		!validDigest(result.Root.PartitionResultsSHA256) || len(result.Root.PartitionResults) != 2 ||
		result.Root.PartitionResults[0].MemberOrdinal != 0 ||
		result.Root.PartitionResults[1].MemberOrdinal != -1 {
		t.Fatalf("root result = %+v", result.Root)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{fixture.repository, "service/main.go", ".ndjson"} {
		if bytes.Contains(raw, []byte(private)) {
			t.Fatalf("source-free result contains %q", private)
		}
	}

	wrongCandidates := fixture.candidates
	wrongCandidates.Records++
	if _, err := Derive(
		t.Context(), fixture.snapshot, fixture.domain, wrongCandidates, fixture.candidateProof,
	); !errors.Is(err, ErrInvalid) {
		t.Fatalf("candidate mismatch error = %v", err)
	}
	proof := t421sourceprojection.NewCandidateProof("typed-local")
	if err := proof.Add(
		"service/other.go", strings.Repeat("8", 40), 16, true,
	); err != nil {
		t.Fatal(err)
	}
	wrongProofValue, err := proof.Finish()
	if err != nil {
		t.Fatal(err)
	}
	wrongProof := SetIdentity(wrongProofValue)
	if _, err := Derive(
		t.Context(), fixture.snapshot, fixture.domain, fixture.candidates, wrongProof,
	); !errors.Is(err, ErrInvalid) {
		t.Fatalf("same-count candidate content mismatch error = %v", err)
	}
	wrongAuthority := fixture.snapshot
	wrongAuthority.Authority.PlanDigest = "sha256:" + strings.Repeat("7", 64)
	if _, err := Derive(
		t.Context(), wrongAuthority, fixture.domain, fixture.candidates, fixture.candidateProof,
	); !errors.Is(err, ErrInvalid) {
		t.Fatalf("authority mismatch error = %v", err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Derive(
		canceled, fixture.snapshot, fixture.domain, fixture.candidates, fixture.candidateProof,
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled derive error = %v", err)
	}
}

func TestExactSparseOpenAndProjectionReadAccounting(t *testing.T) {
	fixture := newProjectionFixture(t)
	ctx, ledger, err := readaccounting.Start(
		t.Context(), readaccounting.Counts{ControlFileReads: 4, MemberVisits: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	publication, err := candidate.OpenSparse(
		ctx, fixture.sparseDirectory, fixture.candidateDirectory,
		fixture.state, fixture.sparseRootDigest, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	domain, err := publication.OpenDomain(ctx, "typed-local", "1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Derive(
		ctx, fixture.snapshot, domain, fixture.candidates, fixture.candidateProof,
	); err != nil {
		t.Fatal(err)
	}
	counts, err := ledger.Finish()
	if err != nil || counts != (readaccounting.Counts{ControlFileReads: 4, MemberVisits: 1}) {
		t.Fatalf("exact read accounting = %+v, %v", counts, err)
	}

	limited, limitedLedger, err := readaccounting.Start(
		t.Context(), readaccounting.Counts{ControlFileReads: 3, MemberVisits: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	publication, err = candidate.OpenSparse(
		limited, fixture.sparseDirectory, fixture.candidateDirectory,
		fixture.state, fixture.sparseRootDigest, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	domain, err = publication.OpenDomain(limited, "typed-local", "1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Derive(
		limited, fixture.snapshot, domain, fixture.candidates, fixture.candidateProof,
	); !errors.Is(err, readaccounting.ErrLimit) {
		t.Fatalf("control limit error = %v", err)
	}
	counts, err = limitedLedger.Finish()
	if !errors.Is(err, readaccounting.ErrLimit) ||
		counts != (readaccounting.Counts{ControlFileReads: 4, MemberVisits: 1}) {
		t.Fatalf("limited read accounting = %+v, %v", counts, err)
	}
}

func TestGenerationDigestMatchesProductionRecipe(t *testing.T) {
	repository := "example.invalid/repository"
	plans := []string{"sha256:" + strings.Repeat("1", 64), "sha256:" + strings.Repeat("2", 64)}
	got, err := extractionpublication.GenerationDigest(repository, plans)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(struct {
		Schema     string   `json:"schema"`
		Repository string   `json:"repository"`
		Plans      []string `json:"plans"`
	}{Schema: extractionpublication.GenerationSchema, Repository: repository, Plans: plans})
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(extractionpublication.GenerationSchema))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(payload)
	want := "sha256:" + hex.EncodeToString(hash.Sum(nil))
	if got != want {
		t.Fatalf("generation digest = %q, want %q", got, want)
	}
	if _, err := extractionpublication.GenerationDigest(repository, nil); !errors.Is(err, extractionpublication.ErrInvalid) {
		t.Fatalf("nil plan inventory error = %v", err)
	}
}

type projectionFixture struct {
	repository         string
	candidateDirectory string
	sparseDirectory    string
	sparseRootDigest   string
	state              candidate.State
	domain             *candidate.SparseDomain
	snapshot           extractionpublication.DomainSnapshot
	candidates         SetIdentity
	candidateProof     SetIdentity
}

func newProjectionFixture(t *testing.T) projectionFixture {
	t.Helper()
	repository := "example.invalid/t421-extraction"
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "config", "user.email", "t421@example.invalid")
	runGit(t, repo, "config", "user.name", "T42.1 Test")
	write := func(name, content string) {
		t.Helper()
		path := filepath.Join(repo, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("service/main.go", "package service\n")
	write("service/index.scip", "typed-index")
	runGit(t, repo, "add", "--", "service/main.go", "service/index.scip")
	runGit(t, repo, "commit", "-q", "-m", "fixture")
	commit := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	scope := analysisunit.Scope{
		Repository: repository, Name: "service", Primary: []string{"service/main.go"},
		Supporting: []string{"service/index.scip"},
		TypedIndex: &analysisunit.TypedIndex{Kind: analysisunit.TypedIndexKindSCIP, Path: "service/index.scip"},
	}
	unit, err := scope.State()
	if err != nil {
		t.Fatal(err)
	}
	policies := []candidate.Policy{{
		Domain: "typed-local", Version: "1", EnumerationPolicy: "typed-local-v1",
		Plane: candidate.PlaneLocal, TypedInputs: []string{analysisunit.TypedIndexKindSCIP},
		Enumerate: func(path string) bool { return strings.HasSuffix(path, ".go") },
		Required:  func(path string) bool { return strings.HasSuffix(path, ".go") },
	}}
	identities, err := candidate.PolicyIdentities(policies)
	if err != nil {
		t.Fatal(err)
	}
	candidateDirectory := t.TempDir()
	manifest, err := candidate.Build(t.Context(), candidate.Request{
		RepoDir: repo, OutputDir: candidateDirectory, Repository: repository,
		Commit: commit, Unit: unit, Policies: policies,
	})
	if err != nil {
		t.Fatal(err)
	}
	publication, err := candidate.Open(candidateDirectory, candidate.Expected{
		Repository: repository, Commit: commit, Unit: unit, Policies: identities,
	})
	if err != nil {
		t.Fatal(err)
	}
	sparseDirectory := t.TempDir()
	sparseRoot, err := candidate.BuildSparseRoot(t.Context(), sparseDirectory, publication, nil)
	if err != nil {
		t.Fatal(err)
	}
	sparse, err := candidate.OpenSparse(
		t.Context(), sparseDirectory, candidateDirectory, manifest.State(), sparseRoot.Digest, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	domain, err := sparse.OpenDomain(t.Context(), "typed-local", "1")
	if err != nil {
		t.Fatal(err)
	}
	proof := t421sourceprojection.NewCandidateProof("typed-local")
	for index, partition := range domain.Partitions() {
		if partition.Kind != candidate.PartitionKindCandidateMember {
			continue
		}
		if err := domain.ReadPartition(t.Context(), index, func(record candidate.Record) error {
			return proof.Add(record.Path, record.OID, record.DeclaredBytes, record.Required)
		}); err != nil {
			t.Fatal(err)
		}
	}
	candidateProof, err := proof.Finish()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := extractionpublication.BuildReservedPlan(domain, candidate.DomainResultAuthority{
		SourceGenerationDigest:      "sha256:" + strings.Repeat("3", 64),
		ObservationGenerationDigest: "sha256:" + strings.Repeat("4", 64),
		ExtractorVersion:            "1",
		ExtractionPolicyDigest:      "sha256:" + strings.Repeat("5", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	results := make([]candidate.PartitionResult, len(plan.Expected))
	for index := range results {
		results[index], err = candidate.BuildPartitionResult(plan, index, candidate.PartitionResultSpec{
			Disposition: candidate.PartitionResultEmpty,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	root, err := candidate.BuildDomainResultRoot(plan, results)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := candidate.NewDownstreamDomainAuthority(plan, root, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	return projectionFixture{
		repository: repository, candidateDirectory: candidateDirectory,
		sparseDirectory: sparseDirectory, sparseRootDigest: sparseRoot.Digest,
		state: manifest.State(), domain: domain,
		snapshot:       extractionpublication.DomainSnapshot{Plan: plan, Root: root, Authority: authority},
		candidates:     SetIdentity{Records: 1, FramedBytes: 99, SHA256: "sha256:" + strings.Repeat("6", 64)},
		candidateProof: SetIdentity(candidateProof),
	}
}

func runGit(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return string(output)
}
