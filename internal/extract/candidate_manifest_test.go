package extract

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/store"
)

type manifestProviderFunc func(
	context.Context,
	CandidateManifestRequest,
) (CandidateManifest, error)

func (f manifestProviderFunc) OpenCandidateManifest(
	ctx context.Context,
	request CandidateManifestRequest,
) (CandidateManifest, error) {
	return f(ctx, request)
}

type memoryCandidateManifest struct {
	identity    string
	corpusFiles int
	gitlinks    CandidateManifestGitlinks
	records     []CandidateManifestFile
	walkErr     error
}

func (m *memoryCandidateManifest) Identity() string { return m.identity }
func (m *memoryCandidateManifest) CorpusFileCount() int {
	return m.corpusFiles
}
func (m *memoryCandidateManifest) GitlinkBoundaries() CandidateManifestGitlinks {
	return m.gitlinks
}
func (m *memoryCandidateManifest) ForEachRepositoryFile(
	ctx context.Context,
	_, _ string,
	visit func(CandidateManifestFile) error,
) error {
	if m.walkErr != nil {
		return m.walkErr
	}
	for _, record := range m.records {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(record); err != nil {
			return err
		}
	}
	return nil
}

func validMemoryCandidateManifest() *memoryCandidateManifest {
	return &memoryCandidateManifest{
		identity:    "sha256:" + strings.Repeat("a", 64),
		corpusFiles: 200_008,
		gitlinks: CandidateManifestGitlinks{
			Digest: emptyGitlinkInventory().digest,
		},
		records: []CandidateManifestFile{
			{
				Path: "read.proto", ObjectID: strings.Repeat("b", 40),
				DeclaredBytes: int64(len("same blob")), Required: true,
			},
			{
				Path: "same.proto", ObjectID: strings.Repeat("c", 40),
				DeclaredBytes: int64(len("same blob")),
			},
		},
	}
}

func TestWorkerCandidateManifestGatePrecedesRunAndExtractor(t *testing.T) {
	tests := []struct {
		name     string
		manifest *memoryCandidateManifest
		openErr  error
	}{
		{
			name:    "stale publication refused by open",
			openErr: errors.New("stale candidate publication"),
		},
		{
			name: "malformed identity",
			manifest: func() *memoryCandidateManifest {
				value := validMemoryCandidateManifest()
				value.identity = "bad"
				return value
			}(),
		},
		{
			name: "partial domain member",
			manifest: func() *memoryCandidateManifest {
				value := validMemoryCandidateManifest()
				value.walkErr = errors.New("partial candidate member")
				return value
			}(),
		},
		{
			name: "required ledger mismatch",
			manifest: func() *memoryCandidateManifest {
				value := validMemoryCandidateManifest()
				value.records[0].Required = false
				return value
			}(),
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			repo := &store.Repo{Name: "host/repo", IndexedCommitHash: unitCommit}
			evidence := newMemoryEvidence()
			extractorCalls := 0
			extractor := unitExtractor{
				domain: "proto-contract", version: "1",
				candidate: func(filePath string) bool {
					return filePath == "read.proto"
				},
				extract: func(
					context.Context, sdk.Corpus, sdk.Emit,
				) (sdk.Coverage, error) {
					extractorCalls++
					return sdk.Coverage{}, nil
				},
			}
			worker := Worker{
				Repos: readyRepoGetter(repo), Evidence: evidence,
				NewCorpus: unitFactory(nil),
				Manifests: manifestProviderFunc(func(
					context.Context,
					CandidateManifestRequest,
				) (CandidateManifest, error) {
					return testCase.manifest, testCase.openErr
				}),
				Extractors: []Extractor{extractor},
			}
			err := worker.Handle(context.Background(), store.Job{Target: repo.Name})
			if err == nil {
				t.Fatal("candidate manifest refusal unexpectedly succeeded")
			}
			if extractorCalls != 0 || evidence.nextRun != 0 {
				t.Fatalf(
					"refused manifest called extractor %d time(s) and began %d run(s)",
					extractorCalls, evidence.nextRun)
			}
		})
	}
}

func TestWorkerCandidateManifestBindsCoverageAndShortCircuit(t *testing.T) {
	repo := &store.Repo{Name: "host/repo", IndexedCommitHash: unitCommit}
	evidence := newMemoryEvidence()
	manifest := validMemoryCandidateManifest()
	opens := 0
	extractions := 0
	extractor := unitExtractor{
		domain: "proto-contract", version: "1",
		candidate: func(filePath string) bool {
			return filePath == "read.proto"
		},
		extract: func(
			ctx context.Context, corpus sdk.Corpus, _ sdk.Emit,
		) (sdk.Coverage, error) {
			extractions++
			var walked []string
			if err := corpus.WalkFiles(ctx, func(filePath string) error {
				walked = append(walked, filePath)
				return nil
			}); err != nil {
				return sdk.Coverage{}, err
			}
			if strings.Join(walked, ",") != "read.proto,same.proto" {
				return sdk.Coverage{}, errors.New("unexpected manifest repository view")
			}
			if _, err := corpus.Read(ctx, "read.proto"); err != nil {
				return sdk.Coverage{}, err
			}
			return sdk.Coverage{}, nil
		},
	}
	worker := Worker{
		Repos: readyRepoGetter(repo), Evidence: evidence,
		NewCorpus: unitFactory(nil),
		Manifests: manifestProviderFunc(func(
			_ context.Context,
			request CandidateManifestRequest,
		) (CandidateManifest, error) {
			opens++
			if request.Repository != repo.Name ||
				request.Commit != repo.IndexedCommitHash ||
				len(request.Domains) != 1 ||
				request.Domains[0] != (CandidateManifestDomain{
					Domain: "proto-contract", Version: "1",
				}) {
				return nil, errors.New("incorrect candidate manifest request")
			}
			return manifest, nil
		}),
		Extractors: []Extractor{extractor},
	}
	job := store.Job{Target: repo.Name}
	if err := worker.Handle(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if err := worker.Handle(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	wantPolicy := candidateManifestInventoryPrefix + strings.Repeat("a", 64)
	if opens != 2 || extractions != 1 || evidence.nextRun != 1 ||
		evidence.publishedWith.InventoryPolicy != wantPolicy ||
		evidence.publishedWith.CorpusFileCount != 200_008 ||
		evidence.publishedWith.CandidateFileCount != 1 {
		t.Fatalf(
			"opens/extractions/runs/coverage = %d/%d/%d/%+v",
			opens, extractions, evidence.nextRun, evidence.publishedWith)
	}

	// A content-only change keeps path assignment but changes manifest
	// identity, so it must supersede the same commit/extractor publication.
	manifest.identity = "sha256:" + strings.Repeat("d", 64)
	if err := worker.Handle(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if opens != 3 || extractions != 2 || evidence.nextRun != 2 ||
		evidence.publishedWith.InventoryPolicy !=
			candidateManifestInventoryPrefix+strings.Repeat("d", 64) {
		t.Fatalf(
			"changed identity opens/extractions/runs/coverage = %d/%d/%d/%+v",
			opens, extractions, evidence.nextRun, evidence.publishedWith)
	}
}
