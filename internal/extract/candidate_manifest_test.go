package extract

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/candidate"
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

type splitManifestProvider struct {
	identity func(context.Context, CandidateManifestRequest) (string, error)
	open     func(context.Context, CandidateManifestRequest) (CandidateManifest, error)
}

func (provider splitManifestProvider) CandidateManifestIdentity(
	ctx context.Context,
	request CandidateManifestRequest,
) (string, error) {
	return provider.identity(ctx, request)
}

func (provider splitManifestProvider) OpenCandidateManifest(
	ctx context.Context,
	request CandidateManifestRequest,
) (CandidateManifest, error) {
	return provider.open(ctx, request)
}

type focusedManifestCorpusFactory struct {
	files     map[string]string
	typedPath string
}

func (focusedManifestCorpusFactory) Lock(
	context.Context,
	string,
) (func(), error) {
	return func() {}, nil
}

func (factory focusedManifestCorpusFactory) New(
	repository,
	commit string,
) sdk.Corpus {
	return &focusedManifestCorpus{
		repository: repository,
		commit:     commit,
		files:      factory.files,
		typedPath:  factory.typedPath,
	}
}

type focusedManifestCorpus struct {
	repository string
	commit     string
	files      map[string]string
	typedPath  string
}

func (corpus *focusedManifestCorpus) RepoName() string {
	return corpus.repository
}

func (corpus *focusedManifestCorpus) Commit() string {
	return corpus.commit
}

func (corpus *focusedManifestCorpus) WalkFiles(
	ctx context.Context,
	visit func(string) error,
) error {
	paths := make([]string, 0, len(corpus.files))
	for filePath := range corpus.files {
		paths = append(paths, filePath)
	}
	sort.Strings(paths)
	for _, filePath := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(filePath); err != nil {
			return err
		}
	}
	return nil
}

func (corpus *focusedManifestCorpus) Read(
	_ context.Context,
	filePath string,
) (sdk.Blob, error) {
	content, ok := corpus.files[filePath]
	if !ok {
		return sdk.Blob{}, store.ErrNotFound
	}
	digest := sha256.Sum256([]byte(content))
	return sdk.Blob{
		Content: content,
		Digest:  "sha256:" + hex.EncodeToString(digest[:]),
	}, nil
}

func (corpus *focusedManifestCorpus) ReadSCIPIndex(
	ctx context.Context,
) (sdk.SCIPInput, error) {
	blob, err := corpus.Read(ctx, corpus.typedPath)
	if err != nil {
		return sdk.SCIPInput{}, err
	}
	return sdk.SCIPInput{
		Path:    corpus.typedPath,
		Blob:    blob,
		Present: true,
	}, nil
}

type memoryCandidateManifest struct {
	identity    string
	corpusFiles int
	gitlinks    CandidateManifestGitlinks
	records     []CandidateManifestFile
	walkErr     error
	unitDigest  string
	plane       candidate.Plane
	typed       CandidateManifestTypedInput
	inScope     func(string) bool
}

func (m *memoryCandidateManifest) Identity() string { return m.identity }
func (m *memoryCandidateManifest) CorpusFileCount() int {
	return m.corpusFiles
}
func (m *memoryCandidateManifest) GitlinkBoundaries() CandidateManifestGitlinks {
	return m.gitlinks
}
func (m *memoryCandidateManifest) DomainScope(
	_, _ string,
) (CandidateManifestScope, error) {
	plane := m.plane
	if plane == "" {
		plane = candidate.PlaneLocal
	}
	required := 0
	var declared int64
	for _, record := range m.records {
		if record.Required {
			required++
		}
		declared += record.DeclaredBytes
	}
	return CandidateManifestScope{
		ManifestDigest:         m.identity,
		UnitDigest:             m.unitDigest,
		Plane:                  plane,
		CorpusFileCount:        m.corpusFiles,
		CorpusDeclaredBytes:    declared,
		CorpusDigest:           "sha256:" + strings.Repeat("d", 64),
		CandidateFileCount:     len(m.records),
		RequiredFileCount:      required,
		CandidateDeclaredBytes: declared,
		CandidateDigest:        "sha256:" + strings.Repeat("e", 64),
	}, nil
}
func (m *memoryCandidateManifest) TypedInput(
	_, _, kind string,
) (CandidateManifestTypedInput, error) {
	if m.typed.Kind == "" {
		return CandidateManifestTypedInput{Kind: kind}, nil
	}
	return m.typed, nil
}
func (m *memoryCandidateManifest) PathInScope(filePath string) bool {
	return m.inScope == nil || m.inScope(filePath)
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

func TestWorkerNoOpRefusesInvalidStoredCoverage(t *testing.T) {
	repo := &store.Repo{
		Name:              "host/repo",
		IndexedCommitHash: unitCommit,
	}
	evidence := newMemoryEvidence()
	evidence.latestErr = errors.New(
		"latest published run: invalid stored coverage",
	)
	manifest := validMemoryCandidateManifest()
	opens := 0
	locks := 0
	extractions := 0
	worker := Worker{
		Repos:    readyRepoGetter(repo),
		Evidence: evidence,
		NewCorpus: unitFactory(func(
			context.Context,
			string,
		) (func(), error) {
			locks++
			return func() {}, nil
		}),
		Manifests: splitManifestProvider{
			identity: func(
				context.Context,
				CandidateManifestRequest,
			) (string, error) {
				return manifest.Identity(), nil
			},
			open: func(
				context.Context,
				CandidateManifestRequest,
			) (CandidateManifest, error) {
				opens++
				return manifest, nil
			},
		},
		Extractors: []Extractor{unitExtractor{
			domain:  "proto-contract",
			version: "1",
			candidate: func(filePath string) bool {
				return filePath == "read.proto"
			},
			extract: func(
				context.Context,
				sdk.Corpus,
				sdk.Emit,
			) (sdk.Coverage, error) {
				extractions++
				return sdk.Coverage{}, nil
			},
		}},
	}
	err := worker.Handle(
		context.Background(),
		store.Job{Target: repo.Name},
	)
	if err == nil ||
		!strings.Contains(err.Error(), "invalid stored coverage") {
		t.Fatalf("invalid stored coverage no-op = %v", err)
	}
	if opens != 0 ||
		locks != 0 ||
		extractions != 0 ||
		evidence.nextRun != 0 {
		t.Fatalf(
			"invalid no-op opened/locked/extracted/began = %d/%d/%d/%d",
			opens,
			locks,
			extractions,
			evidence.nextRun,
		)
	}
}

func TestWorkerCandidateManifestBindsCoverageAndShortCircuit(t *testing.T) {
	repo := &store.Repo{Name: "host/repo", IndexedCommitHash: unitCommit}
	evidence := newMemoryEvidence()
	manifest := validMemoryCandidateManifest()
	identityChecks := 0
	opens := 0
	locks := 0
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
		NewCorpus: unitFactory(func(
			context.Context, string,
		) (func(), error) {
			locks++
			return func() {}, nil
		}),
		Manifests: splitManifestProvider{
			identity: func(
				_ context.Context,
				request CandidateManifestRequest,
			) (string, error) {
				identityChecks++
				if err := validateManifestRequest(request, repo); err != nil {
					return "", err
				}
				return manifest.Identity(), nil
			},
			open: func(
				_ context.Context,
				request CandidateManifestRequest,
			) (CandidateManifest, error) {
				opens++
				if err := validateManifestRequest(request, repo); err != nil {
					return nil, err
				}
				return manifest, nil
			},
		},
		Extractors: []Extractor{extractor},
	}
	job := store.Job{Target: repo.Name}
	if err := worker.Handle(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if identityChecks != 1 || opens != 1 || locks != 1 ||
		extractions != 1 {
		t.Fatalf(
			"initial identity/open/lock/extraction = %d/%d/%d/%d",
			identityChecks, opens, locks, extractions,
		)
	}
	if err := worker.Handle(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	wantPolicy := "candidate-manifest-v3-" + strings.Repeat("a", 64)
	if identityChecks != 2 || opens != 1 || locks != 1 ||
		extractions != 1 || evidence.nextRun != 1 ||
		evidence.publishedWith.InventoryPolicy != wantPolicy ||
		evidence.publishedWith.CorpusFileCount != 200_008 ||
		evidence.publishedWith.CandidateFileCount != 1 {
		t.Fatalf(
			"no-op identity/open/lock/extractions/runs/coverage = %d/%d/%d/%d/%d/%+v",
			identityChecks, opens, locks, extractions, evidence.nextRun,
			evidence.publishedWith,
		)
	}

	// A content-only change keeps path assignment but changes manifest
	// identity, so it must supersede the same commit/extractor publication.
	manifest.identity = "sha256:" + strings.Repeat("d", 64)
	if err := worker.Handle(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if identityChecks != 3 || opens != 2 || locks != 2 ||
		extractions != 2 || evidence.nextRun != 2 ||
		evidence.publishedWith.InventoryPolicy !=
			"candidate-manifest-v3-"+strings.Repeat("d", 64) {
		t.Fatalf(
			"changed identity/open/lock/extractions/runs/coverage = %d/%d/%d/%d/%d/%+v",
			identityChecks, opens, locks, extractions, evidence.nextRun,
			evidence.publishedWith,
		)
	}
}

func TestWorkerPublishesExactFocusedScopeAndDoesNotReuseOtherUnit(
	t *testing.T,
) {
	const repository = "host/focused"
	typedPath := "index.scip"
	unitA, err := (analysisunit.Config{
		Name:       "read-service",
		Primary:    []string{"read.proto"},
		Supporting: []string{typedPath, "same.proto"},
		TypedIndex: &analysisunit.TypedIndex{
			Kind: analysisunit.TypedIndexKindSCIP,
			Path: typedPath,
		},
	}).Scope(repository).State()
	if err != nil {
		t.Fatal(err)
	}
	unitB, err := (analysisunit.Config{
		Name:       "same-service",
		Primary:    []string{"same.proto"},
		Supporting: []string{typedPath, "read.proto"},
		TypedIndex: &analysisunit.TypedIndex{
			Kind: analysisunit.TypedIndexKindSCIP,
			Path: typedPath,
		},
	}).Scope(repository).State()
	if err != nil {
		t.Fatal(err)
	}
	if unitA.Digest == unitB.Digest {
		t.Fatal("focused fixtures unexpectedly share a unit digest")
	}

	repo := &store.Repo{
		Name:                repository,
		IndexedCommitHash:   unitCommit,
		IndexedAnalysisUnit: unitA,
	}
	evidence := newMemoryEvidence()
	manifest := validMemoryCandidateManifest()
	manifest.corpusFiles = 3
	manifest.unitDigest = unitA.Digest
	manifest.identity = "sha256:" + strings.Repeat("1", 64)
	typedContent := "typed index"
	manifest.typed = CandidateManifestTypedInput{
		Kind:          analysisunit.TypedIndexKindSCIP,
		Path:          typedPath,
		ObjectID:      strings.Repeat("f", 40),
		DeclaredBytes: int64(len(typedContent)),
		Present:       true,
	}
	files := map[string]string{
		"read.proto": "same blob",
		"same.proto": "same blob",
		typedPath:    typedContent,
	}
	identityChecks := 0
	opens := 0
	extractions := 0
	checkRequest := func(request CandidateManifestRequest) error {
		if request.Repository != repository ||
			request.Commit != unitCommit ||
			request.AnalysisUnit == nil ||
			request.AnalysisUnit.Digest != manifest.unitDigest ||
			len(request.Domains) != 1 ||
			request.Domains[0].Domain != "scip-proto-field" {
			return errors.New(
				"candidate request did not bind the current focused scope",
			)
		}
		return nil
	}
	worker := Worker{
		Repos:    readyRepoGetter(repo),
		Evidence: evidence,
		NewCorpus: focusedManifestCorpusFactory{
			files:     files,
			typedPath: typedPath,
		},
		Manifests: splitManifestProvider{
			identity: func(
				_ context.Context,
				request CandidateManifestRequest,
			) (string, error) {
				identityChecks++
				if err := checkRequest(request); err != nil {
					return "", err
				}
				return manifest.Identity(), nil
			},
			open: func(
				_ context.Context,
				request CandidateManifestRequest,
			) (CandidateManifest, error) {
				opens++
				if err := checkRequest(request); err != nil {
					return nil, err
				}
				return manifest, nil
			},
		},
		Extractors: []Extractor{unitExtractor{
			domain:  "scip-proto-field",
			version: "focused-v1",
			candidate: func(filePath string) bool {
				return filePath == "read.proto"
			},
			extract: func(
				ctx context.Context,
				corpus sdk.Corpus,
				_ sdk.Emit,
			) (sdk.Coverage, error) {
				extractions++
				scipCorpus, ok := corpus.(sdk.SCIPCorpus)
				if !ok {
					return sdk.Coverage{}, errors.New(
						"verified corpus omitted SCIP capability",
					)
				}
				typed, err := scipCorpus.ReadSCIPIndex(ctx)
				if err != nil {
					return sdk.Coverage{}, err
				}
				if !typed.Present ||
					typed.Path != typedPath ||
					typed.Content != typedContent {
					return sdk.Coverage{}, errors.New(
						"typed input did not preserve its exact receipt",
					)
				}
				if _, err := corpus.Read(ctx, "read.proto"); err != nil {
					return sdk.Coverage{}, err
				}
				return sdk.Coverage{
					Protocols: []string{"scip"},
				}, nil
			},
		}},
	}
	job := store.Job{Target: repository}
	if err := worker.Handle(context.Background(), job); err != nil {
		t.Fatalf("first focused extraction: %v", err)
	}

	scopeA := store.ExtractionScope{
		Repository: repository,
		Commit:     unitCommit,
		UnitDigest: unitA.Digest,
		Domain:     "scip-proto-field",
	}
	runA := evidence.latestByScope[memoryScopeKey(scopeA)]
	if runA == nil ||
		runA.Repo != scopeA.Repository ||
		runA.Commit != scopeA.Commit ||
		runA.UnitDigest != scopeA.UnitDigest ||
		runA.Domain != scopeA.Domain ||
		runA.Status != "published" {
		t.Fatalf("focused run identity = %+v, want %+v", runA, scopeA)
	}
	coverageA := runA.Coverage
	typedDigestBytes := sha256.Sum256([]byte(typedContent))
	typedDigest := "sha256:" +
		hex.EncodeToString(typedDigestBytes[:])
	if coverageA.ScopePosture != "focused-local" ||
		coverageA.CandidateManifestDigest != manifest.Identity() ||
		coverageA.CandidatePlane != string(candidate.PlaneLocal) ||
		coverageA.TypedInputKind != analysisunit.TypedIndexKindSCIP ||
		coverageA.TypedInputPath != typedPath ||
		coverageA.TypedInputObjectID != strings.Repeat("f", 40) ||
		coverageA.TypedInputDeclaredBytes != int64(len(typedContent)) ||
		coverageA.TypedInputDigest != typedDigest ||
		!coverageA.TypedInputPresent {
		t.Fatalf("focused candidate/typed coverage = %+v", coverageA)
	}

	// The exact same scope takes the pointer-only no-op path.
	if err := worker.Handle(context.Background(), job); err != nil {
		t.Fatalf("same-unit no-op: %v", err)
	}
	if identityChecks != 2 ||
		opens != 1 ||
		extractions != 1 ||
		evidence.nextRun != 1 {
		t.Fatalf(
			"same-unit identity/open/extract/run = %d/%d/%d/%d",
			identityChecks,
			opens,
			extractions,
			evidence.nextRun,
		)
	}

	// The commit remains unchanged, but a different committed unit is a new
	// publication slot and must run rather than reusing unit A's no-op.
	repo.IndexedAnalysisUnit = unitB
	manifest.unitDigest = unitB.Digest
	manifest.identity = "sha256:" + strings.Repeat("2", 64)
	if err := worker.Handle(context.Background(), job); err != nil {
		t.Fatalf("same-HEAD different-unit extraction: %v", err)
	}
	scopeB := scopeA
	scopeB.UnitDigest = unitB.Digest
	runB := evidence.latestByScope[memoryScopeKey(scopeB)]
	if runB == nil ||
		runB.UnitDigest != unitB.Digest ||
		runB.Commit != unitCommit ||
		runB.Coverage.CandidateManifestDigest != manifest.Identity() ||
		runB.Coverage.ScopePosture != "focused-local" {
		t.Fatalf("different-unit focused run = %+v", runB)
	}
	if identityChecks != 3 ||
		opens != 2 ||
		extractions != 2 ||
		evidence.nextRun != 2 {
		t.Fatalf(
			"different-unit identity/open/extract/run = %d/%d/%d/%d",
			identityChecks,
			opens,
			extractions,
			evidence.nextRun,
		)
	}
	if evidence.latestByScope[memoryScopeKey(scopeA)] != runA {
		t.Fatal("different unit overwrote the retained unit A publication")
	}
}

func TestFocusedMissingSCIPSettlesUnavailableWithoutLegacyEmptyPublication(
	t *testing.T,
) {
	repository := "host/missing-scip"
	unit, err := (analysisunit.Config{
		Name:       "service",
		Primary:    []string{"service/api.proto"},
		Supporting: []string{"service/index.scip"},
		TypedIndex: &analysisunit.TypedIndex{
			Kind: analysisunit.TypedIndexKindSCIP,
			Path: "service/index.scip",
		},
	}).Scope(repository).State()
	if err != nil {
		t.Fatal(err)
	}
	manifest := validMemoryCandidateManifest()
	manifest.corpusFiles = 0
	manifest.records = nil
	manifest.unitDigest = unit.Digest
	manifest.typed = CandidateManifestTypedInput{
		Kind:    analysisunit.TypedIndexKindSCIP,
		Path:    "service/index.scip",
		Present: false,
	}
	evidence := newMemoryEvidence()
	extractions := 0
	extractor := unitExtractor{
		domain:  "scip-proto-field",
		version: "1.3.0",
		extract: func(
			context.Context, sdk.Corpus, sdk.Emit,
		) (sdk.Coverage, error) {
			extractions++
			return sdk.Coverage{
				Protocols: []string{"scip-index-absent"},
			}, nil
		},
	}
	worker := Worker{
		Repos: readyRepoGetter(&store.Repo{
			Name: repository, IndexedCommitHash: unitCommit,
			IndexedAnalysisUnit: unit,
		}),
		Evidence: evidence,
		NewCorpus: focusedManifestCorpusFactory{
			files:     map[string]string{},
			typedPath: "service/index.scip",
		},
		Manifests: splitManifestProvider{
			identity: func(
				context.Context, CandidateManifestRequest,
			) (string, error) {
				return manifest.Identity(), nil
			},
			open: func(
				context.Context, CandidateManifestRequest,
			) (CandidateManifest, error) {
				return manifest, nil
			},
		},
		Extractors: []Extractor{extractor},
	}
	job := store.Job{Target: repository}
	if err := worker.Handle(context.Background(), job); err != nil {
		t.Fatalf("focused missing SCIP: %v", err)
	}
	scope := store.ExtractionScope{
		Repository: repository, Commit: unitCommit,
		UnitDigest: unit.Digest, Domain: "scip-proto-field",
	}
	outcome, err := evidence.LatestExtractionDomainOutcome(
		context.Background(), scope,
	)
	if err != nil ||
		outcome.Disposition != store.DomainOutcomeUnavailablePrerequisite ||
		extractions != 0 || evidence.nextRun != 0 || evidence.published {
		t.Fatalf(
			"focused missing SCIP outcome=%+v err=%v extractions=%d runs=%d published=%v",
			outcome, err, extractions, evidence.nextRun, evidence.published,
		)
	}
	if err := worker.Handle(
		context.Background(),
		store.Job{Target: repository, Force: true},
	); err != nil {
		t.Fatalf("forced settled missing SCIP: %v", err)
	}
	if extractions != 0 || evidence.nextRun != 0 {
		t.Fatalf(
			"forced settled generation reran: extractions=%d runs=%d",
			extractions, evidence.nextRun,
		)
	}

	// Without a committed focused unit the historical extractor behavior is
	// retained: the absent index is an empty publication, not unavailable.
	wholeEvidence := newMemoryEvidence()
	manifest.unitDigest = ""
	whole := worker
	whole.Repos = readyRepoGetter(&store.Repo{
		Name: repository, IndexedCommitHash: unitCommit,
	})
	whole.Evidence = wholeEvidence
	extractions = 0
	if err := whole.Handle(context.Background(), job); err != nil {
		t.Fatalf("whole-repository missing SCIP: %v", err)
	}
	wholeScope := scope
	wholeScope.UnitDigest = ""
	wholeOutcome, err := wholeEvidence.LatestExtractionDomainOutcome(
		context.Background(), wholeScope,
	)
	if err != nil ||
		wholeOutcome.Disposition != store.DomainOutcomePublished ||
		extractions != 1 || !wholeEvidence.published {
		t.Fatalf(
			"whole missing SCIP outcome=%+v err=%v extractions=%d published=%v",
			wholeOutcome, err, extractions, wholeEvidence.published,
		)
	}
}

func TestWorkerCandidateMarkerRefusesAtNoOpPreflight(t *testing.T) {
	repo := &store.Repo{Name: "host/repo", IndexedCommitHash: unitCommit}
	evidence := newMemoryEvidence()
	manifest := validMemoryCandidateManifest()
	markerCovered := false
	locks := 0
	opens := 0
	extractions := 0
	worker := Worker{
		Repos: readyRepoGetter(repo), Evidence: evidence,
		NewCorpus: unitFactory(func(
			context.Context, string,
		) (func(), error) {
			locks++
			return func() {}, nil
		}),
		Manifests: splitManifestProvider{
			identity: func(
				context.Context,
				CandidateManifestRequest,
			) (string, error) {
				if markerCovered {
					return "", candidate.ErrPublishing
				}
				return manifest.Identity(), nil
			},
			open: func(
				context.Context,
				CandidateManifestRequest,
			) (CandidateManifest, error) {
				opens++
				return manifest, nil
			},
		},
		Extractors: []Extractor{unitExtractor{
			domain: "proto-contract", version: "1",
			candidate: func(filePath string) bool {
				return filePath == "read.proto"
			},
			extract: func(
				ctx context.Context, corpus sdk.Corpus, _ sdk.Emit,
			) (sdk.Coverage, error) {
				extractions++
				_, err := corpus.Read(ctx, "read.proto")
				return sdk.Coverage{}, err
			},
		}},
	}
	job := store.Job{Target: repo.Name}
	if err := worker.Handle(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	markerCovered = true
	if err := worker.Handle(
		context.Background(), job,
	); !errors.Is(err, candidate.ErrPublishing) {
		t.Fatalf("marker-covered no-op error = %v", err)
	}
	if locks != 1 || opens != 1 || extractions != 1 ||
		evidence.nextRun != 1 {
		t.Fatalf(
			"marker-covered lock/open/extractions/runs = %d/%d/%d/%d",
			locks, opens, extractions, evidence.nextRun,
		)
	}
}

func validateManifestRequest(
	request CandidateManifestRequest,
	repo *store.Repo,
) error {
	if request.Repository != repo.Name ||
		request.Commit != repo.IndexedCommitHash ||
		len(request.Domains) != 1 ||
		request.Domains[0] != (CandidateManifestDomain{
			Domain: "proto-contract", Version: "1",
		}) {
		return errors.New("incorrect candidate manifest request")
	}
	return nil
}
