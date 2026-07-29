package candidatejob

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/store"
	reposync "github.com/bmeddeb/phebs/internal/sync"
)

type policyExtractor struct {
	domain, version, requiredSuffix string
}

func (extractor policyExtractor) Domain() string  { return extractor.domain }
func (extractor policyExtractor) Version() string { return extractor.version }
func (extractor policyExtractor) Candidate(filePath string) bool {
	return strings.HasSuffix(filePath, extractor.requiredSuffix)
}
func (extractor policyExtractor) Extract(
	context.Context,
	sdk.Corpus,
	sdk.Emit,
) (sdk.Coverage, error) {
	return sdk.Coverage{}, nil
}

type manifestStore struct {
	mu sync.Mutex

	repository *store.Repo
	pointer    *store.CandidateManifestPublication

	publishFailure  error
	pointerFailure  error
	clearFailure    error
	beforePublish   func(*manifestStore)
	publishCalls    int
	fanoutCount     int
	getPointerCalls int
	clearCalls      int
}

func (state *manifestStore) GetRepo(
	_ context.Context,
	name string,
) (*store.Repo, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.repository == nil || state.repository.Name != name {
		return nil, store.ErrNotFound
	}
	return cloneRepository(state.repository), nil
}

func (state *manifestStore) GetCandidateManifestPublication(
	_ context.Context,
	repository string,
) (*store.CandidateManifestPublication, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.getPointerCalls++
	if state.pointerFailure != nil {
		return nil, state.pointerFailure
	}
	if state.pointer == nil || state.pointer.Repository != repository {
		return nil, store.ErrNotFound
	}
	result := *state.pointer
	return &result, nil
}

func (state *manifestStore) PublishCandidateManifest(
	_ context.Context,
	publication store.CandidateManifestPublication,
) error {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.publishCalls++
	if state.beforePublish != nil {
		hook := state.beforePublish
		state.beforePublish = nil
		hook(state)
	}
	if state.publishFailure != nil {
		err := state.publishFailure
		state.publishFailure = nil
		return err
	}
	if state.repository == nil ||
		state.repository.Name != publication.Repository ||
		state.repository.Deleting ||
		state.repository.IndexedCommitHash != publication.HeadCommit ||
		unitDigest(state.repository.IndexedAnalysisUnit) != publication.UnitDigest {
		return store.ErrConflict
	}
	if state.pointer != nil && samePublicationScope(*state.pointer, publication) &&
		!samePublication(*state.pointer, publication) {
		return store.ErrConflict
	}
	if state.pointer == nil || !samePublication(*state.pointer, publication) {
		publication.PublishedAt = time.Now().UTC()
		state.pointer = &publication
	} else {
		publication.PublishedAt = state.pointer.PublishedAt
		state.pointer = &publication
	}
	if state.fanoutCount == 0 {
		state.fanoutCount++
	}
	return nil
}

func (state *manifestStore) ClearCandidateManifestPublication(
	_ context.Context,
	repository string,
) error {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.clearCalls++
	if state.clearFailure != nil {
		return state.clearFailure
	}
	if state.pointer != nil && state.pointer.Repository == repository {
		state.pointer = nil
	}
	state.pointerFailure = nil
	return nil
}

func (state *manifestStore) ListCandidateManifestPublications(
	context.Context,
) ([]store.CandidateManifestPublication, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.pointer == nil {
		return nil, nil
	}
	return []store.CandidateManifestPublication{*state.pointer}, nil
}

func cloneRepository(input *store.Repo) *store.Repo {
	if input == nil {
		return nil
	}
	result := *input
	result.IndexedAnalysisUnit = analysisunit.CloneState(input.IndexedAnalysisUnit)
	result.IndexedRevisions = slices.Clone(input.IndexedRevisions)
	return &result
}

func unitDigest(unit *analysisunit.State) string {
	if unit == nil {
		return ""
	}
	return unit.Digest
}

func samePublicationScope(
	left, right store.CandidateManifestPublication,
) bool {
	return left.Repository == right.Repository &&
		left.HeadCommit == right.HeadCommit &&
		left.UnitDigest == right.UnitDigest &&
		left.PolicyDigest == right.PolicyDigest
}

func samePublication(left, right store.CandidateManifestPublication) bool {
	return samePublicationScope(left, right) &&
		left.ManifestDigest == right.ManifestDigest &&
		left.GenerationDigest == right.GenerationDigest &&
		left.ManifestPath == right.ManifestPath
}

func TestWorkerRetryRepairsPublicationAndProviderStreamsBothPlanes(
	t *testing.T,
) {
	t.Parallel()
	ctx := context.Background()
	dataDir, repository, commit := candidateGitFixture(t)
	unit, err := (analysisunit.Scope{
		Repository: repository,
		Name:       "service",
		Primary:    []string{"service"},
		Supporting: []string{},
	}).State()
	if err != nil {
		t.Fatal(err)
	}
	state := &manifestStore{
		repository: &store.Repo{
			Name: repository, IndexedCommitHash: commit,
			IndexedAnalysisUnit: unit,
		},
		publishFailure: errors.New("injected pointer failure"),
	}
	extractors := []extract.Extractor{
		policyExtractor{
			domain: "proto-contract", version: "proto-v1",
			requiredSuffix: ".proto",
		},
		policyExtractor{
			domain: "grpc-caller", version: "caller-v1",
			requiredSuffix: ".go",
		},
	}
	worker, provider, err := New(dataDir, state, extractors)
	if err != nil {
		t.Fatal(err)
	}
	strictOpens := 0
	recoverPublication := worker.recover
	worker.recover = func(
		ctx context.Context,
		root string,
		expected candidate.Expected,
	) (*candidate.Publication, error) {
		strictOpens++
		if !candidate.IsPublishing(root, repository) {
			t.Fatal("recovery removed the marker before strict validation")
		}
		publication, recoverErr := recoverPublication(ctx, root, expected)
		if !candidate.IsPublishing(root, repository) {
			t.Fatal("strict recovery removed the marker before state commit")
		}
		return publication, recoverErr
	}
	if worker.policies != provider.policies ||
		worker.policies.Digest() != provider.PolicyDigest() {
		t.Fatal("planner and provider did not share one frozen policy generation")
	}

	job := store.Job{Kind: store.JobCandidate, Target: repository}
	firstErr := worker.Handle(ctx, job)
	if firstErr == nil ||
		store.Classify(firstErr) != store.ClassExtract ||
		!strings.Contains(firstErr.Error(), "injected pointer failure") {
		t.Fatalf("first Handle error = %v", firstErr)
	}
	root := CandidateRoot(dataDir)
	if !candidate.IsPublishing(root, repository) {
		t.Fatal("failed database transition did not leave the fail-closed marker")
	}
	manifestPath := filepath.Join(root, candidate.ManifestName(repository))
	before, err := os.Stat(manifestPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := worker.Handle(ctx, job); err != nil {
		t.Fatalf("retry Handle: %v", err)
	}
	if candidate.IsPublishing(root, repository) {
		t.Fatal("successful retry left the publication marker")
	}
	after, err := os.Stat(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("retry rebuilt a strictly reusable filesystem publication")
	}
	if state.publishCalls != 2 || state.fanoutCount != 1 || state.pointer == nil {
		t.Fatalf(
			"publication calls/fanout/pointer = %d/%d/%+v",
			state.publishCalls, state.fanoutCount, state.pointer,
		)
	}
	if strictOpens != 1 {
		t.Fatalf("marker recovery strict opens = %d, want 1", strictOpens)
	}

	request := extract.CandidateManifestRequest{
		Repository: repository, Commit: commit,
		AnalysisUnit: analysisunit.CloneState(unit),
		Domains:      slices.Clone(provider.domains),
	}
	providerStrictOpens := 0
	openProviderPublication := provider.open
	provider.open = func(
		ctx context.Context,
		root string,
		expected candidate.Expected,
	) (*candidate.Publication, error) {
		providerStrictOpens++
		return openProviderPublication(ctx, root, expected)
	}
	identity, err := provider.CandidateManifestIdentity(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if identity != state.pointer.ManifestDigest || providerStrictOpens != 0 {
		t.Fatalf(
			"pointer identity/strict opens = %q/%d",
			identity, providerStrictOpens,
		)
	}
	opened, err := provider.OpenCandidateManifest(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if providerStrictOpens != 1 {
		t.Fatalf("provider strict opens = %d, want 1", providerStrictOpens)
	}
	if opened.Identity() != state.pointer.ManifestDigest ||
		opened.CorpusFileCount() != 3 {
		t.Fatalf(
			"manifest identity/count = %q/%d",
			opened.Identity(), opened.CorpusFileCount(),
		)
	}
	if boundaries := opened.GitlinkBoundaries(); boundaries.Count != 0 ||
		boundaries.Digest == "" || boundaries.SampleTruncated {
		t.Fatalf("gitlink boundaries = %+v", boundaries)
	}

	type seenFile struct {
		path     string
		required bool
		inUnit   bool
	}
	var protoFiles, callerFiles []seenFile
	if err := opened.ForEachRepositoryFile(
		ctx, "proto-contract", "proto-v1",
		func(file extract.CandidateManifestFile) error {
			protoFiles = append(protoFiles, seenFile{
				path: file.Path, required: file.Required, inUnit: file.InUnit,
			})
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	if err := opened.ForEachRepositoryFile(
		ctx, "grpc-caller", "caller-v1",
		func(file extract.CandidateManifestFile) error {
			callerFiles = append(callerFiles, seenFile{
				path: file.Path, required: file.Required, inUnit: file.InUnit,
			})
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(protoFiles, []seenFile{{
		path: "service/api.proto", required: true, inUnit: true,
	}}) {
		t.Fatalf("proto files = %+v", protoFiles)
	}
	if !slices.Equal(callerFiles, []seenFile{{
		path: "client/use.go", required: true, inUnit: false,
	}}) {
		t.Fatalf("caller files = %+v", callerFiles)
	}
}

func TestWorkerExactPointerReuseRepairsFanoutWithoutStrictOpen(
	t *testing.T,
) {
	t.Parallel()
	dataDir, repository, commit := candidateGitFixture(t)
	state := &manifestStore{
		repository: &store.Repo{
			Name: repository, IndexedCommitHash: commit,
		},
	}
	worker, _, err := New(
		dataDir, state, []extract.Extractor{policyExtractor{
			domain: "proto-contract", version: "proto-v1",
			requiredSuffix: ".proto",
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	job := store.Job{Kind: store.JobCandidate, Target: repository}
	if err := worker.Handle(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	state.mu.Lock()
	state.fanoutCount = 0
	state.mu.Unlock()
	strictOpens := 0
	worker.open = func(
		context.Context,
		string,
		candidate.Expected,
	) (*candidate.Publication, error) {
		strictOpens++
		return nil, errors.New("unexpected strict publication open")
	}
	if err := worker.Handle(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	if strictOpens != 0 || state.publishCalls != 2 ||
		state.fanoutCount != 1 {
		t.Fatalf(
			"steady-state strict opens/publishes/fanout = %d/%d/%d",
			strictOpens, state.publishCalls, state.fanoutCount,
		)
	}
}

func TestWorkerMissingPointerControlRebuildsWithoutStrictReuseOpen(
	t *testing.T,
) {
	t.Parallel()
	dataDir, repository, commit := candidateGitFixture(t)
	state := &manifestStore{
		repository: &store.Repo{
			Name: repository, IndexedCommitHash: commit,
		},
	}
	worker, _, err := New(
		dataDir, state, []extract.Extractor{policyExtractor{
			domain: "proto-contract", version: "proto-v1",
			requiredSuffix: ".proto",
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	job := store.Job{Kind: store.JobCandidate, Target: repository}
	if err := worker.Handle(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(
		CandidateRoot(dataDir), candidate.ManifestName(repository),
	)
	if err := os.Remove(manifestPath); err != nil {
		t.Fatal(err)
	}
	strictOpens := 0
	worker.open = func(
		context.Context,
		string,
		candidate.Expected,
	) (*candidate.Publication, error) {
		strictOpens++
		return nil, errors.New("unexpected strict reuse open")
	}
	if err := worker.Handle(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	if strictOpens != 0 || state.publishCalls != 2 {
		t.Fatalf(
			"missing-control strict opens/publishes = %d/%d",
			strictOpens, state.publishCalls,
		)
	}
	if info, err := os.Lstat(manifestPath); err != nil ||
		!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("rebuilt manifest control = %+v, %v", info, err)
	}
}

func TestWorkerDoesNotAdoptUnmarkedFilesystemBytesWithoutValidPointer(
	t *testing.T,
) {
	t.Parallel()
	for _, testCase := range []struct {
		name           string
		invalidPointer bool
	}{
		{name: "missing pointer"},
		{name: "invalid pointer", invalidPointer: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			dataDir, repository, commit := candidateGitFixture(t)
			state := &manifestStore{
				repository: &store.Repo{
					Name: repository, IndexedCommitHash: commit,
				},
			}
			if testCase.invalidPointer {
				state.pointer = &store.CandidateManifestPublication{
					Repository: repository,
				}
				state.pointerFailure = fmt.Errorf(
					"tampered pointer: %w",
					store.ErrInvalidCandidateManifestPublication,
				)
			}
			worker, provider, err := New(
				dataDir, state, []extract.Extractor{policyExtractor{
					domain: "proto-contract", version: "proto-v1",
					requiredSuffix: ".proto",
				}},
			)
			if err != nil {
				t.Fatal(err)
			}

			root := CandidateRoot(dataDir)
			if err := ensureCandidateRoot(root, true); err != nil {
				t.Fatal(err)
			}
			stage, err := candidate.NewStage(root)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = os.RemoveAll(stage) }()
			forgedPolicies := slices.Clone(worker.policies.policies)
			for index := range forgedPolicies {
				forgedPolicies[index].Enumerate = func(string) bool {
					return false
				}
				forgedPolicies[index].Required = nil
			}
			forged, err := candidate.Build(t.Context(), candidate.Request{
				RepoDir:   reposync.RepoDir(dataDir, repository),
				OutputDir: stage, Repository: repository, Commit: commit,
				Policies: forgedPolicies,
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(forged.Domains) != 1 ||
				forged.Domains[0].RepositoryCandidateCount != 0 {
				t.Fatalf("forged fixture retained candidates: %+v", forged.Domains)
			}
			expected, err := worker.expected(state.repository)
			if err != nil {
				t.Fatal(err)
			}
			expected.ManifestDigest = forged.Digest
			if _, err := candidate.PublishContext(
				t.Context(), root, stage, expected,
			); err != nil {
				t.Fatal(err)
			}
			if err := candidate.FinishPublication(root, repository); err != nil {
				t.Fatal(err)
			}
			if candidate.IsPublishing(root, repository) {
				t.Fatal("forged fixture unexpectedly retained a crash marker")
			}

			if err := worker.Handle(
				t.Context(),
				store.Job{Kind: store.JobCandidate, Target: repository},
			); err != nil {
				t.Fatal(err)
			}
			if state.pointer == nil ||
				state.pointer.ManifestDigest == forged.Digest {
				t.Fatalf(
					"unmarked filesystem generation was adopted: %+v",
					state.pointer,
				)
			}
			if testCase.invalidPointer && state.clearCalls != 1 {
				t.Fatalf("invalid pointer clear calls = %d", state.clearCalls)
			}

			opened, err := provider.OpenCandidateManifest(
				t.Context(), extract.CandidateManifestRequest{
					Repository: repository, Commit: commit,
					Domains: slices.Clone(provider.domains),
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			files := 0
			if err := opened.ForEachRepositoryFile(
				t.Context(), "proto-contract", "proto-v1",
				func(file extract.CandidateManifestFile) error {
					files++
					if file.Path != "service/api.proto" {
						t.Fatalf("rebuilt candidate = %+v", file)
					}
					return nil
				},
			); err != nil {
				t.Fatal(err)
			}
			if files != 1 {
				t.Fatalf("rebuilt candidate count = %d", files)
			}
		})
	}
}

func TestWorkerRepairsOnlyTypedInvalidPublicationPointers(t *testing.T) {
	t.Parallel()
	extractor := policyExtractor{
		domain: "proto-contract", version: "proto-v1",
		requiredSuffix: ".proto",
	}

	t.Run("invalid derived pointer is cleared and rebuilt", func(t *testing.T) {
		dataDir, repository, commit := candidateGitFixture(t)
		state := &manifestStore{
			repository: &store.Repo{
				Name: repository, IndexedCommitHash: commit,
			},
			pointer: &store.CandidateManifestPublication{
				Repository: repository,
			},
			pointerFailure: fmt.Errorf(
				"tampered pointer: %w",
				store.ErrInvalidCandidateManifestPublication,
			),
		}
		worker, provider, err := New(
			dataDir, state, []extract.Extractor{extractor},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := worker.Handle(
			t.Context(),
			store.Job{Kind: store.JobCandidate, Target: repository},
		); err != nil {
			t.Fatal(err)
		}
		if state.clearCalls != 1 || state.pointerFailure != nil ||
			state.pointer == nil || state.fanoutCount != 1 {
			t.Fatalf(
				"repair clear/error/pointer/fanout = %d/%v/%+v/%d",
				state.clearCalls, state.pointerFailure, state.pointer,
				state.fanoutCount,
			)
		}
		if _, err := provider.OpenCandidateManifest(
			t.Context(),
			extract.CandidateManifestRequest{
				Repository: repository, Commit: commit,
				Domains: slices.Clone(provider.domains),
			},
		); err != nil {
			t.Fatalf("open rebuilt publication: %v", err)
		}
	})

	t.Run("pointer store failure remains fail closed", func(t *testing.T) {
		dataDir, repository, commit := candidateGitFixture(t)
		state := &manifestStore{
			repository: &store.Repo{
				Name: repository, IndexedCommitHash: commit,
			},
			pointerFailure: errors.New("database unavailable"),
		}
		worker, _, err := New(
			dataDir, state, []extract.Extractor{extractor},
		)
		if err != nil {
			t.Fatal(err)
		}
		err = worker.Handle(
			t.Context(),
			store.Job{Kind: store.JobCandidate, Target: repository},
		)
		if err == nil || !strings.Contains(err.Error(), "database unavailable") {
			t.Fatalf("pointer store failure = %v", err)
		}
		if state.clearCalls != 0 || state.publishCalls != 0 {
			t.Fatalf(
				"store failure clear/publish calls = %d/%d",
				state.clearCalls, state.publishCalls,
			)
		}
	})

	t.Run("invalid pointer clear failure remains fail closed", func(t *testing.T) {
		dataDir, repository, commit := candidateGitFixture(t)
		state := &manifestStore{
			repository: &store.Repo{
				Name: repository, IndexedCommitHash: commit,
			},
			pointerFailure: fmt.Errorf(
				"tampered pointer: %w",
				store.ErrInvalidCandidateManifestPublication,
			),
			clearFailure: errors.New("database unavailable"),
		}
		worker, _, err := New(
			dataDir, state, []extract.Extractor{extractor},
		)
		if err != nil {
			t.Fatal(err)
		}
		err = worker.Handle(
			t.Context(),
			store.Job{Kind: store.JobCandidate, Target: repository},
		)
		if err == nil ||
			!strings.Contains(err.Error(), "clear invalid publication pointer") {
			t.Fatalf("invalid pointer clear failure = %v", err)
		}
		if state.clearCalls != 1 || state.publishCalls != 0 {
			t.Fatalf(
				"clear failure clear/publish calls = %d/%d",
				state.clearCalls, state.publishCalls,
			)
		}
	})
}

func TestWorkerPreservesFixedRootAndRequiredSymlinkPosture(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name, alias, target, domain, requiredSuffix, want string
	}{
		{
			name: "SCIP fixed root", alias: "index.scip",
			target: "target.go", domain: "scip-proto-field",
			requiredSuffix: "index.scip", want: "is forbidden",
		},
		{
			name: "attribution fixed root", alias: "layout-snapshot.json",
			target: "target.go", domain: "grpc-caller",
			requiredSuffix: ".go", want: "is forbidden",
		},
		{
			name: "required alias target", alias: "api.proto",
			target: "target.proto", domain: "proto-contract",
			requiredSuffix: "api.proto", want: "does not preserve required domain",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			dataDir, repository, commit := candidateSymlinkGitFixture(
				t, testCase.alias, testCase.target,
			)
			state := &manifestStore{
				repository: &store.Repo{
					Name: repository, IndexedCommitHash: commit,
				},
			}
			worker, _, err := New(
				dataDir,
				state,
				[]extract.Extractor{policyExtractor{
					domain: testCase.domain, version: "test-v1",
					requiredSuffix: testCase.requiredSuffix,
				}},
			)
			if err != nil {
				t.Fatal(err)
			}
			err = worker.Handle(
				t.Context(),
				store.Job{Kind: store.JobCandidate, Target: repository},
			)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("symlink admission error = %v, want %q", err, testCase.want)
			}
			if state.publishCalls != 0 || state.pointer != nil ||
				state.fanoutCount != 0 {
				t.Fatalf(
					"refused symlink publish/pointer/fanout = %d/%+v/%d",
					state.publishCalls, state.pointer, state.fanoutCount,
				)
			}
		})
	}

	t.Run("enumeration-only broken symlink is skipped", func(t *testing.T) {
		dataDir, repository, commit := candidateSymlinkGitFixtureWithTarget(
			t, "broken.go", "missing.go", false,
		)
		state := &manifestStore{
			repository: &store.Repo{
				Name: repository, IndexedCommitHash: commit,
			},
		}
		worker, _, err := New(
			dataDir,
			state,
			[]extract.Extractor{policyExtractor{
				domain: "scip-proto-field", version: "test-v1",
				requiredSuffix: "index.scip",
			}},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := worker.Handle(
			t.Context(),
			store.Job{Kind: store.JobCandidate, Target: repository},
		); err != nil {
			t.Fatalf("enumeration-only symlink: %v", err)
		}
		if state.publishCalls != 1 || state.pointer == nil ||
			state.fanoutCount != 1 {
			t.Fatalf(
				"enumeration-only publish/pointer/fanout = %d/%+v/%d",
				state.publishCalls, state.pointer, state.fanoutCount,
			)
		}
	})

	t.Run("one required alias blocks the shared generation", func(t *testing.T) {
		dataDir, repository, commit := candidateSymlinkGitFixture(
			t, "api.proto", "target.proto",
		)
		state := &manifestStore{
			repository: &store.Repo{
				Name: repository, IndexedCommitHash: commit,
			},
		}
		worker, _, err := New(
			dataDir,
			state,
			[]extract.Extractor{
				policyExtractor{
					domain: "proto-contract", version: "test-v1",
					requiredSuffix: "api.proto",
				},
				policyExtractor{
					domain: "kafka-producer", version: "test-v1",
					requiredSuffix: ".go",
				},
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		err = worker.Handle(
			t.Context(),
			store.Job{Kind: store.JobCandidate, Target: repository},
		)
		if err == nil ||
			!strings.Contains(err.Error(), "does not preserve required domain") {
			t.Fatalf("shared-generation symlink error = %v", err)
		}
		if state.publishCalls != 0 || state.pointer != nil ||
			state.fanoutCount != 0 {
			t.Fatalf(
				"blocked shared generation publish/pointer/fanout = %d/%+v/%d",
				state.publishCalls, state.pointer, state.fanoutCount,
			)
		}
	})
}

func TestProviderRefusesPartialStaleMalformedAndTamperedPublications(
	t *testing.T,
) {
	t.Parallel()
	ctx := context.Background()
	dataDir, repository, commit := candidateGitFixture(t)
	state := &manifestStore{
		repository: &store.Repo{
			Name: repository, IndexedCommitHash: commit,
		},
	}
	worker, provider, err := New(
		dataDir,
		state,
		[]extract.Extractor{policyExtractor{
			domain: "proto-contract", version: "proto-v1",
			requiredSuffix: ".proto",
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.Handle(
		ctx, store.Job{Kind: store.JobCandidate, Target: repository},
	); err != nil {
		t.Fatal(err)
	}
	request := extract.CandidateManifestRequest{
		Repository: repository, Commit: commit,
		Domains: slices.Clone(provider.domains),
	}

	t.Run("partial domain request", func(t *testing.T) {
		before := state.getPointerCalls
		partial := request
		partial.Domains = nil
		if _, err := provider.OpenCandidateManifest(ctx, partial); err == nil {
			t.Fatal("partial domain request succeeded")
		}
		if state.getPointerCalls != before {
			t.Fatal("partial domain request reached the pointer store")
		}
	})

	t.Run("stale commit", func(t *testing.T) {
		stale := request
		stale.Commit = strings.Repeat("a", len(commit))
		if _, err := provider.OpenCandidateManifest(ctx, stale); err == nil ||
			!strings.Contains(err.Error(), "does not match") {
			t.Fatalf("stale commit error = %v", err)
		}
	})

	t.Run("malformed pointer path", func(t *testing.T) {
		state.mu.Lock()
		original := *state.pointer
		state.pointer.ManifestPath = "nested/manifest.json"
		state.mu.Unlock()
		defer func() {
			state.mu.Lock()
			state.pointer = &original
			state.mu.Unlock()
		}()
		if _, err := provider.OpenCandidateManifest(ctx, request); err == nil ||
			!strings.Contains(err.Error(), "publication pointer") {
			t.Fatalf("malformed pointer error = %v", err)
		}
	})

	t.Run("stable publication marker", func(t *testing.T) {
		markerPath := filepath.Join(
			CandidateRoot(dataDir), candidate.PublishingName(repository),
		)
		if err := os.WriteFile(markerPath, []byte("publishing\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := os.Remove(markerPath); err != nil &&
				!errors.Is(err, os.ErrNotExist) {
				t.Errorf("remove marker: %v", err)
			}
		}()
		before := state.getPointerCalls
		if _, err := provider.CandidateManifestIdentity(
			ctx, request,
		); !errors.Is(err, candidate.ErrPublishing) {
			t.Fatalf("marker identity error = %v", err)
		}
		if state.getPointerCalls != before {
			t.Fatal("marker-covered identity reached the pointer store")
		}
	})

	t.Run("tampered manifest bytes", func(t *testing.T) {
		manifestPath := filepath.Join(
			CandidateRoot(dataDir), candidate.ManifestName(repository),
		)
		file, err := os.OpenFile(manifestPath, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.WriteString(" "); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := provider.OpenCandidateManifest(ctx, request); err == nil ||
			!errors.Is(err, candidate.ErrInvalidManifest) {
			t.Fatalf("tampered manifest error = %v", err)
		}
	})
}

func TestWorkerDistinguishesStaleAndDeterministicPublicationConflicts(
	t *testing.T,
) {
	t.Parallel()
	ctx := context.Background()
	extractor := policyExtractor{
		domain: "proto-contract", version: "proto-v1",
		requiredSuffix: ".proto",
	}

	t.Run("stale indexed generation is a no-op", func(t *testing.T) {
		dataDir, repository, commit := candidateGitFixture(t)
		state := &manifestStore{
			repository: &store.Repo{
				Name: repository, IndexedCommitHash: commit,
			},
		}
		state.beforePublish = func(current *manifestStore) {
			current.repository.IndexedCommitHash = strings.Repeat("b", len(commit))
		}
		worker, _, err := New(
			dataDir, state, []extract.Extractor{extractor},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := worker.Handle(
			ctx, store.Job{Kind: store.JobCandidate, Target: repository},
		); err != nil {
			t.Fatalf("stale candidate job = %v", err)
		}
		if !candidate.IsPublishing(CandidateRoot(dataDir), repository) {
			t.Fatal("stale transition did not remain fail-closed for its successor")
		}
	})

	t.Run("same generation conflict fails loudly", func(t *testing.T) {
		dataDir, repository, commit := candidateGitFixture(t)
		state := &manifestStore{
			repository: &store.Repo{
				Name: repository, IndexedCommitHash: commit,
			},
		}
		worker, _, err := New(
			dataDir, state, []extract.Extractor{extractor},
		)
		if err != nil {
			t.Fatal(err)
		}
		job := store.Job{Kind: store.JobCandidate, Target: repository}
		if err := worker.Handle(ctx, job); err != nil {
			t.Fatal(err)
		}
		state.mu.Lock()
		state.pointer.ManifestDigest = "sha256:" + strings.Repeat("c", 64)
		state.mu.Unlock()

		job.Force = true
		err = worker.Handle(ctx, job)
		if err == nil || !errors.Is(err, store.ErrConflict) ||
			store.Classify(err) != store.ClassExtract ||
			!strings.Contains(err.Error(), "deterministic publication conflict") {
			t.Fatalf("deterministic conflict error = %v", err)
		}
	})
}

func TestWorkerNoOpsForMissingDeletingAndUnindexedRepositories(
	t *testing.T,
) {
	t.Parallel()
	dataDir := t.TempDir()
	repository := "example.com/acme/service"
	state := &manifestStore{}
	worker, _, err := New(
		dataDir,
		state,
		[]extract.Extractor{policyExtractor{
			domain: "proto-contract", version: "proto-v1",
			requiredSuffix: ".proto",
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	job := store.Job{Kind: store.JobCandidate, Target: repository}
	if err := worker.Handle(context.Background(), job); err != nil {
		t.Fatalf("missing repository: %v", err)
	}
	state.repository = &store.Repo{Name: repository, Deleting: true}
	if err := worker.Handle(context.Background(), job); err != nil {
		t.Fatalf("deleting repository: %v", err)
	}
	state.repository = &store.Repo{Name: repository}
	if err := worker.Handle(context.Background(), job); err != nil {
		t.Fatalf("unindexed repository: %v", err)
	}
	if _, err := os.Lstat(CandidateRoot(dataDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("no-op jobs created candidate root: %v", err)
	}
}

func candidateGitFixture(t *testing.T) (dataDir, repository, commit string) {
	t.Helper()
	dataDir = t.TempDir()
	repository = "example.com/acme/service"
	work := filepath.Join(t.TempDir(), "work")
	runGit(t, "", "init", work)
	runGit(t, work, "config", "user.email", "candidate@example.invalid")
	runGit(t, work, "config", "user.name", "Candidate Test")
	writeFixtureFile(t, work, "service/api.proto", "syntax = \"proto3\";\n")
	writeFixtureFile(t, work, "client/use.go", "package client\n")
	writeFixtureFile(t, work, "notes.txt", "not a candidate\n")
	runGit(t, work, "add", ".")
	runGit(t, work, "commit", "-m", "fixture")
	commit = strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))

	repoDir := reposync.RepoDir(dataDir, repository)
	if err := os.MkdirAll(filepath.Dir(repoDir), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, "", "clone", "--bare", work, repoDir)
	return dataDir, repository, commit
}

func candidateSymlinkGitFixture(
	t *testing.T,
	alias, target string,
) (dataDir, repository, commit string) {
	return candidateSymlinkGitFixtureWithTarget(
		t, alias, target, true,
	)
}

func candidateSymlinkGitFixtureWithTarget(
	t *testing.T,
	alias, target string,
	writeTarget bool,
) (dataDir, repository, commit string) {
	t.Helper()
	dataDir = t.TempDir()
	repository = "example.com/acme/symlink-service"
	work := filepath.Join(t.TempDir(), "work")
	runGit(t, "", "init", work)
	runGit(t, work, "config", "user.email", "candidate@example.invalid")
	runGit(t, work, "config", "user.name", "Candidate Test")
	if writeTarget {
		writeFixtureFile(t, work, target, "package target\n")
	}
	aliasPath := filepath.Join(work, filepath.FromSlash(alias))
	if err := os.MkdirAll(filepath.Dir(aliasPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, aliasPath); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "add", ".")
	runGit(t, work, "commit", "-m", "symlink fixture")
	commit = strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))

	repoDir := reposync.RepoDir(dataDir, repository)
	if err := os.MkdirAll(filepath.Dir(repoDir), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, "", "clone", "--bare", work, repoDir)
	return dataDir, repository, commit
}

func writeFixtureFile(t *testing.T, root, relative, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	if directory != "" {
		command.Dir = directory
	}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
	return string(output)
}
