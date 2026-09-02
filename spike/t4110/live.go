package t4110

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/indexer"
	"github.com/bmeddeb/phebs/internal/recovery"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogingest"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
	"github.com/bmeddeb/phebs/internal/store"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
	"github.com/bmeddeb/phebs/spike/t4013"
	"github.com/bmeddeb/phebs/spike/t411"
)

const (
	liveRepository = "neutral.invalid/t4110/target"
	liveConfig     = "t4110-neutral-local-store-v1\n"
)

type LiveOptions struct {
	RepositoryRoot string
	PhebsBinary    string
	PhebsVersion   string
	ZoektBinary    string
	BrowserBinary  string
}

func measuredUTCDate(now time.Time) string {
	return now.UTC().Format("2006-01-02")
}

// RunAndAuthor executes the complete gate and creates one receipt. The private
// process-session invariant prevents callers from bypassing custody teardown.
func RunAndAuthor(
	ctx context.Context,
	options LiveOptions,
	destination string,
) (ArtifactIdentity, error) {
	if err := requirePrivateAuthorSession(); err != nil {
		return ArtifactIdentity{}, err
	}
	receipt, err := runLive(ctx, options)
	if err != nil {
		return ArtifactIdentity{}, err
	}
	gitExecutable, err := admitNamedExecutable("git")
	if err != nil || gitExecutable.sha256 != receipt.Implementation.GitExecutableSHA256 {
		return ArtifactIdentity{}, errors.Join(errors.New("final Git executable identity changed"), err)
	}
	commit, err := verifyCleanCommitWithGit(ctx, options.RepositoryRoot, gitExecutable.path)
	if err != nil || commit != receipt.Implementation.Commit {
		return ArtifactIdentity{}, errors.Join(errors.New("clean HEAD changed before receipt authoring"), err)
	}
	if err := requirePrivateAuthorSession(); err != nil {
		return ArtifactIdentity{}, err
	}
	return author(destination, receipt)
}

type liveHarness struct {
	root                string
	dataDir             string
	config              []byte
	state               *store.Surreal
	corpus              t411.Corpus
	transition          t411.TransitionCorpus
	catalog             servicecatalog.Catalog
	catalogPath         string
	generation          servicecatalogv3.Generation
	selector            store.ServiceRuntimeSelector
	relationshipRuntime *relationshippublication.Runtime
	searchDigest        string
	commit              string
	zoektBinary         string
	zoektSHA256         string
	gitBinary           string
	phebsBinary         string
	phebsVersion        string
	composedRoot        string
	composedTools       composedToolchain
	browser             admittedExecutable
}

type preimageInventory struct {
	rows            uint64
	summaries       uint64
	collectingRoots int
}

func runLive(ctx context.Context, options LiveOptions) (_ Receipt, retErr error) {
	if err := requirePrivateAuthorSession(); err != nil {
		return Receipt{}, err
	}
	if err := verifyNoAmbientGitEnvironment(); err != nil {
		return Receipt{}, err
	}
	gitExecutable, err := admitNamedExecutable("git")
	if err != nil {
		return Receipt{}, fmt.Errorf("admit Git executable: %w", err)
	}
	commit, err := verifyCleanCommitWithGit(ctx, options.RepositoryRoot, gitExecutable.path)
	if err != nil {
		return Receipt{}, err
	}
	composedRoot, err := exportComposedTree(ctx, options.RepositoryRoot, gitExecutable)
	if err != nil {
		return Receipt{}, fmt.Errorf("export exact HEAD tree: %w", err)
	}
	composedRemoved := false
	defer func() {
		if !composedRemoved {
			retErr = errors.Join(retErr, removeComposedTree(composedRoot))
		}
	}()
	if err := verifyCheckoutMatchesExport(
		ctx, options.RepositoryRoot, composedRoot, gitExecutable,
	); err != nil {
		return Receipt{}, err
	}
	if err := bindComposedTreeGit(
		ctx, options.RepositoryRoot, composedRoot, commit, gitExecutable,
	); err != nil {
		return Receipt{}, err
	}
	phebsBinary, err := verifyPhebsBinaryCommit(options.PhebsBinary, commit)
	if err != nil {
		return Receipt{}, err
	}
	authorPath, err := os.Executable()
	if err != nil {
		return Receipt{}, err
	}
	authorPath, err = verifyAuthorBinaryCommit(authorPath, commit)
	if err != nil {
		return Receipt{}, fmt.Errorf("verify T41.10 author build identity: %w", err)
	}
	authorExecutable, err := admitExecutable(authorPath)
	if err != nil {
		return Receipt{}, fmt.Errorf("admit T41.10 author executable: %w", err)
	}
	phebsExecutable, err := admitExecutable(phebsBinary)
	if err != nil {
		return Receipt{}, fmt.Errorf("admit Phebs executable: %w", err)
	}
	actualPhebsVersion, err := commandVersion(ctx, phebsBinary)
	if err != nil {
		return Receipt{}, err
	}
	phebsVersion := actualPhebsVersion
	if options.PhebsVersion != "" {
		if options.PhebsVersion != actualPhebsVersion {
			return Receipt{}, errors.New("supplied Phebs version differs from binary output")
		}
		phebsVersion = options.PhebsVersion
	}
	zoektBinary := options.ZoektBinary
	if zoektBinary == "" {
		zoektBinary, err = indexer.FindBinary()
	} else {
		err = indexer.VerifyBinaryPin(zoektBinary)
	}
	if err != nil {
		return Receipt{}, fmt.Errorf("resolve zoekt-git-index: %w", err)
	}
	zoektExecutable, err := admitExecutable(zoektBinary)
	if err != nil {
		return Receipt{}, fmt.Errorf("admit zoekt-git-index executable: %w", err)
	}
	zoektBinary = zoektExecutable.path
	if err := indexer.VerifyBinaryPin(zoektBinary); err != nil {
		return Receipt{}, fmt.Errorf("verify zoekt-git-index module identity: %w", err)
	}
	surrealExecutable, err := store.FindSurrealBinary()
	if err != nil {
		return Receipt{}, fmt.Errorf("admit SurrealDB executable: %w", err)
	}
	restoreSurrealEnvironment, err := bindSurrealExecutable(surrealExecutable)
	if err != nil {
		return Receipt{}, err
	}
	defer restoreSurrealEnvironment()
	goExecutable, err := admitNamedExecutable("go")
	if err != nil {
		return Receipt{}, fmt.Errorf("admit Go executable: %w", err)
	}
	nodeExecutable, err := admitNamedExecutable("node")
	if err != nil {
		return Receipt{}, fmt.Errorf("admit Node executable: %w", err)
	}
	npmExecutable, err := admitNamedExecutable("npm")
	if err != nil {
		return Receipt{}, fmt.Errorf("admit npm executable: %w", err)
	}
	if options.BrowserBinary == "" {
		return Receipt{}, errors.New("an explicit browser executable is required")
	}
	browserExecutable, err := admitExecutable(options.BrowserBinary)
	if err != nil {
		return Receipt{}, fmt.Errorf("admit browser executable: %w", err)
	}
	composedTools := composedToolchain{
		git: gitExecutable, goTool: goExecutable, node: nodeExecutable,
		npm: npmExecutable,
		surreal: admittedExecutable{
			path: surrealExecutable.Path, sha256: surrealExecutable.SHA256,
		},
	}
	if err := prepareComposedTree(ctx, composedRoot, composedTools); err != nil {
		return Receipt{}, err
	}
	if err := buildComposedUI(ctx, composedRoot, composedTools); err != nil {
		return Receipt{}, err
	}
	if err := verifyCheckoutMatchesExport(
		ctx, options.RepositoryRoot, composedRoot, gitExecutable,
	); err != nil {
		return Receipt{}, fmt.Errorf("exact UI build changed tracked source: %w", err)
	}
	exactCommit, err := verifyCleanCommitWithGit(ctx, composedRoot, gitExecutable.path)
	if err != nil || exactCommit != commit {
		return Receipt{}, errors.Join(errors.New("prepared exact tree is not clean HEAD"), err)
	}
	referenceBinaries, err := buildComposedReferenceBinaries(
		ctx, composedRoot, commit, composedTools,
	)
	if err != nil {
		return Receipt{}, err
	}
	if err := compareGoBinaryBuildIdentity(
		phebsExecutable.path, referenceBinaries.phebs.path,
	); err != nil || phebsExecutable.sha256 != referenceBinaries.phebs.sha256 {
		return Receipt{}, errors.Join(
			errors.New("supplied Phebs binary differs from its exact reference build"), err,
		)
	}
	if err := compareGoBinaryBuildIdentity(
		authorExecutable.path, referenceBinaries.author.path,
	); err != nil || authorExecutable.sha256 != referenceBinaries.author.sha256 {
		return Receipt{}, errors.Join(
			errors.New("running author binary differs from its exact reference build"), err,
		)
	}
	receipt, err := newDraft(commit, measuredUTCDate(time.Now()), Environment{
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, GoVersion: runtime.Version(),
		LogicalCPUs: runtime.NumCPU(), GOMAXPROCS: runtime.GOMAXPROCS(0),
		RSSMethod: RSSMethodProcessTree, DiskMethod: DiskMethodWalk,
	})
	if err != nil {
		return Receipt{}, err
	}
	receipt.Implementation.AuthorExecutableSHA256 = authorExecutable.sha256
	receipt.Implementation.PhebsExecutableSHA256 = phebsExecutable.sha256
	receipt.Implementation.ZoektExecutableSHA256 = zoektExecutable.sha256
	receipt.Implementation.GitExecutableSHA256 = gitExecutable.sha256
	receipt.Implementation.SurrealExecutableSHA256 = surrealExecutable.SHA256
	receipt.Implementation.GoExecutableSHA256 = goExecutable.sha256
	receipt.Implementation.NodeExecutableSHA256 = nodeExecutable.sha256
	receipt.Implementation.NPMExecutableSHA256 = npmExecutable.sha256
	receipt.Implementation.BrowserExecutableSHA256 = browserExecutable.sha256

	root, err := os.MkdirTemp("", "phebs-t4110-live-")
	if err != nil {
		return Receipt{}, err
	}
	harness := &liveHarness{
		root: root, dataDir: filepath.Join(root, "data"), config: []byte(liveConfig),
		zoektBinary: zoektBinary, zoektSHA256: zoektExecutable.sha256,
		gitBinary:   gitExecutable.path,
		phebsBinary: phebsBinary, phebsVersion: phebsVersion,
		composedRoot: composedRoot, composedTools: composedTools,
		browser: browserExecutable,
	}
	removed := false
	defer func() {
		if harness.state != nil {
			retErr = errors.Join(retErr, closeLiveStore(harness.state))
			harness.state = nil
		}
		if !removed {
			retErr = errors.Join(retErr, os.RemoveAll(root))
		}
	}()

	phase, err := measurePhase(ctx, root, measuredPhaseNames[0], func(ctx context.Context, cost *PhaseCost) error {
		return harness.cold(ctx, cost, &receipt)
	})
	if err != nil {
		return Receipt{}, err
	}
	receipt.MeasuredPhases[0] = phase

	phaseWork := []func(context.Context, *PhaseCost) error{
		harness.warmNoop,
		func(ctx context.Context, cost *PhaseCost) error {
			result, err := querySelectedServices(
				ctx, harness.state, harness.selector, serviceKeys(harness.catalog), true,
			)
			if err != nil {
				return err
			}
			*cost = result.Cost
			receipt.Queryability.IndependentQueries = result.Queries
			receipt.Queryability.IndependentMatches = result.Matches
			receipt.Queryability.MissingServices = result.Missing
			receipt.Queryability.UnexpectedServices = result.Unexpected
			return nil
		},
		harness.oneServiceDelta,
		harness.percentDelta,
		harness.removalReadd,
		harness.aba,
		harness.transitionProfile,
		harness.archiveRestore,
		harness.collection,
		harness.finalProductReads,
	}
	for index, work := range phaseWork {
		phaseIndex := index + 1
		phase, err := measurePhase(
			ctx, root, measuredPhaseNames[phaseIndex], work,
		)
		if err != nil {
			return Receipt{}, err
		}
		receipt.MeasuredPhases[phaseIndex] = phase
	}

	finalSummary, err := harness.state.GetServiceStateV3Summary(ctx, liveRepository)
	if err != nil {
		return Receipt{}, fmt.Errorf("read final service summary: %w", err)
	}
	finalAccepted, err := harness.state.ListAcceptedServiceStateV3Rows(
		ctx, liveRepository, servicecatalogv3.MaxTotalServices,
	)
	if err != nil {
		return Receipt{}, fmt.Errorf("read final accepted services: %w", err)
	}
	receipt.Queryability.PublishedAcceptedServices = harness.generation.Root.Dispositions.Accepted
	receipt.Queryability.CurrentAcceptedServices = len(finalAccepted)
	if finalSummary.CatalogServiceCount != t411.AcceptedServiceTarget {
		return Receipt{}, errors.New("final target catalog no longer contains 10,000 services")
	}

	if err := closeLiveStore(harness.state); err != nil {
		return Receipt{}, fmt.Errorf("close live store: %w", err)
	}
	harness.state = nil
	receipt.Teardown.StoreClosed = true
	if err := os.RemoveAll(root); err != nil {
		return Receipt{}, fmt.Errorf("remove live custody: %w", err)
	}
	removed = true
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		return Receipt{}, errors.New("live temporary custody remained after teardown")
	}
	receipt.Teardown.TemporaryCustodyRemoved = true

	gates, err := runComposedGates(ctx, composedRoot, composedTools)
	if err != nil {
		return Receipt{}, err
	}
	receipt.ComposedGates = gates
	finalCommit, err := verifyCleanCommitWithGit(ctx, options.RepositoryRoot, gitExecutable.path)
	if err != nil || finalCommit != commit {
		return Receipt{}, errors.Join(errors.New("authoring checkout changed during live gate"), err)
	}
	if err := verifyCheckoutMatchesExport(
		ctx, options.RepositoryRoot, composedRoot, gitExecutable,
	); err != nil {
		return Receipt{}, fmt.Errorf("final exact HEAD byte and mode fence: %w", err)
	}
	exactCommit, err = verifyCleanCommitWithGit(ctx, composedRoot, gitExecutable.path)
	if err != nil || exactCommit != commit {
		return Receipt{}, errors.Join(errors.New("final exact tree is not clean HEAD"), err)
	}
	if _, err := verifyPhebsBinaryCommit(phebsBinary, commit); err != nil {
		return Receipt{}, fmt.Errorf("final Phebs binary fence: %w", err)
	}
	if _, err := verifyAuthorBinaryCommit(authorExecutable.path, commit); err != nil {
		return Receipt{}, fmt.Errorf("final author binary fence: %w", err)
	}
	if _, err := verifyPhebsBinaryCommit(referenceBinaries.phebs.path, commit); err != nil {
		return Receipt{}, fmt.Errorf("final reference Phebs binary fence: %w", err)
	}
	if _, err := verifyAuthorBinaryCommit(referenceBinaries.author.path, commit); err != nil {
		return Receipt{}, fmt.Errorf("final reference author binary fence: %w", err)
	}
	if err := errors.Join(
		compareGoBinaryBuildIdentity(phebsExecutable.path, referenceBinaries.phebs.path),
		compareGoBinaryBuildIdentity(authorExecutable.path, referenceBinaries.author.path),
	); err != nil {
		return Receipt{}, fmt.Errorf("final exact-reference build identity fence: %w", err)
	}
	if phebsExecutable.sha256 != referenceBinaries.phebs.sha256 ||
		authorExecutable.sha256 != referenceBinaries.author.sha256 {
		return Receipt{}, errors.New("final supplied and exact-reference binary digests differ")
	}
	for _, executable := range []struct {
		name string
		admittedExecutable
	}{
		{name: "author", admittedExecutable: authorExecutable},
		{name: "phebs", admittedExecutable: phebsExecutable},
		{name: "zoekt", admittedExecutable: zoektExecutable},
		{name: "git", admittedExecutable: gitExecutable},
		{name: "go", admittedExecutable: goExecutable},
		{name: "node", admittedExecutable: nodeExecutable},
		{name: "npm", admittedExecutable: npmExecutable},
		{name: "browser", admittedExecutable: browserExecutable},
		{name: "reference-author", admittedExecutable: referenceBinaries.author},
		{name: "reference-phebs", admittedExecutable: referenceBinaries.phebs},
	} {
		if err := executable.verify(); err != nil {
			return Receipt{}, fmt.Errorf("final %s executable fence: %w", executable.name, err)
		}
	}
	finalSurreal, err := store.InspectSurrealBinary(surrealExecutable.Path)
	if err != nil {
		return Receipt{}, fmt.Errorf("final SurrealDB executable fence: %w", err)
	}
	if finalSurreal.SHA256 != surrealExecutable.SHA256 {
		return Receipt{}, errors.New("final SurrealDB executable identity changed")
	}
	if err := removeComposedTree(composedRoot); err != nil {
		return Receipt{}, fmt.Errorf("remove exact composed tree: %w", err)
	}
	composedRemoved = true
	children, err := remainingProcessChildren(ctx)
	if err != nil {
		return Receipt{}, fmt.Errorf("verify live child teardown: %w", err)
	}
	receipt.Teardown.ChildrenRemaining = children
	if children != 0 {
		return Receipt{}, fmt.Errorf("live child teardown retained %d descendant(s)", children)
	}
	if err := requirePrivateAuthorSession(); err != nil {
		return Receipt{}, err
	}
	for index := range receipt.Checks {
		receipt.Checks[index].Outcome = StepPassed
	}
	receipt.Outcome = OutcomePassed
	if err := ValidateReceipt(receipt); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

func closeLiveStore(state *store.Surreal) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return state.Close(ctx)
}

func requirePrivateAuthorSession() error {
	members, err := t4013.PrivateProcessSessionMembers(os.Getpid())
	if err != nil || members != 1 {
		return errors.Join(
			fmt.Errorf("T41.10 author process session has %d live member(s)", members),
			err,
		)
	}
	return nil
}

func bindSurrealExecutable(identity store.SurrealIdentity) (func(), error) {
	previousPath, hadPath := os.LookupEnv("PHEBS_SURREAL")
	previousDigest, hadDigest := os.LookupEnv("PHEBS_SURREAL_SHA256")
	restore := func() {
		if hadPath {
			_ = os.Setenv("PHEBS_SURREAL", previousPath)
		} else {
			_ = os.Unsetenv("PHEBS_SURREAL")
		}
		if hadDigest {
			_ = os.Setenv("PHEBS_SURREAL_SHA256", previousDigest)
		} else {
			_ = os.Unsetenv("PHEBS_SURREAL_SHA256")
		}
	}
	// ponytail: the author is one private-session process; environment binding
	// is smaller than a production store API used by only this offline gate.
	if err := os.Setenv("PHEBS_SURREAL", identity.Path); err != nil {
		return nil, err
	}
	if err := os.Setenv("PHEBS_SURREAL_SHA256", identity.SHA256); err != nil {
		restore()
		return nil, err
	}
	return restore, nil
}

func (h *liveHarness) cold(
	ctx context.Context,
	cost *PhaseCost,
	receipt *Receipt,
) error {
	corpus, err := t411.BuildTargetCorpus()
	if err != nil {
		return err
	}
	if corpus.ProfileDigest != receipt.Inputs.TargetProfileSHA256 {
		return errors.New("live target corpus differs from frozen T41.1 identity")
	}
	transition, err := t411.BuildTransitionCorpus()
	if err != nil {
		return err
	}
	if transition.ProfileDigest != receipt.Inputs.TransitionProfileSHA256 {
		return errors.New("live transition corpus differs from frozen T41.1 identity")
	}
	h.corpus = corpus
	h.transition = transition
	h.catalog = cloneCatalog(corpus.Catalog)
	if err := os.MkdirAll(h.dataDir, 0o700); err != nil {
		return err
	}
	commit, err := writeBareCorpus(
		ctx, phebssync.RepoDir(h.dataDir, liveRepository), h.gitBinary, corpus.Files,
	)
	if err != nil {
		return err
	}
	h.commit = commit
	h.state, err = store.OpenLocalWithConfig(ctx, h.dataDir, recovery.ConfigDigest(h.config))
	if err != nil {
		return fmt.Errorf("open live store: %w", err)
	}
	if err := h.state.UpsertRepo(ctx, store.Repo{
		Name:     liveRepository,
		CloneURL: "https://neutral.invalid/t4110/target.git",
	}); err != nil {
		return fmt.Errorf("seed target repository: %w", err)
	}
	index := &indexer.Indexer{
		DataDir: h.dataDir, Bin: h.zoektBinary,
		BinSHA256: receipt.Implementation.ZoektExecutableSHA256, Store: h.state,
	}
	if err := index.Index(ctx, store.Repo{Name: liveRepository}, false); err != nil {
		return fmt.Errorf("index target repository: %w", err)
	}
	indexed, err := h.state.GetRepo(ctx, liveRepository)
	if err != nil || indexed.IndexedCommitHash != commit {
		return errors.Join(errors.New("target indexed revision differs from fixture HEAD"), err)
	}
	searchRoot, err := focusedindex.ReadSearchGenerationRoot(
		filepath.Join(h.dataDir, "index"), liveRepository,
	)
	if err != nil {
		return fmt.Errorf("read target search generation: %w", err)
	}
	h.searchDigest = searchRoot.Current.GenerationDigest
	controls, err := focusedindex.ReadSearchGenerationControls(
		ctx, filepath.Join(h.dataDir, "index"), liveRepository, h.searchDigest,
	)
	if err != nil {
		return fmt.Errorf("read target search controls: %w", err)
	}
	if controls.Source.RegularOwnerCount != receipt.Population.RegularFiles ||
		controls.Source.RegularDeclaredBytes != receipt.Population.FixtureContentBytes ||
		controls.Receipt.FilesOffered != receipt.Population.RegularFiles {
		return errors.New("target search/source census differs from frozen population")
	}
	contentReads := controls.Receipt.BatchReadCount + controls.Receipt.FallbackReadCount
	if contentReads != controls.Receipt.FilesOffered {
		return errors.New("target search content-read count differs from files offered")
	}
	receipt.PhysicalWork.CorpusPasses = 1
	cost.SearchFilesOffered = uint64(controls.Receipt.FilesOffered)
	cost.SearchContentReads = uint64(contentReads)
	cost.SearchDeclaredBytes = uint64(controls.Source.RegularDeclaredBytes)

	catalogPath := filepath.Join(h.root, "target-catalog.json")
	encoded, err := encodeLiveV3Catalog(corpus.Catalog)
	if err != nil {
		return err
	}
	if err := os.WriteFile(catalogPath, encoded, 0o600); err != nil {
		return err
	}
	h.catalogPath = catalogPath
	reconciler := servicecatalogingest.V3Reconciler{
		DataDir: h.dataDir,
		Store:   h.state,
		Selections: map[string]config.ServiceCatalog{
			liveRepository: {
				Kind: corpus.Catalog.Authority.Kind, ID: corpus.Catalog.Authority.ID,
				Version: corpus.Catalog.Authority.Version, Path: catalogPath,
				Runtime: config.ServiceCatalogRuntimeV3,
			},
		},
	}
	outcome, err := reconciler.ReconcileRepository(ctx, liveRepository)
	if err != nil || outcome != servicecatalogingest.OutcomePublished {
		return errors.Join(fmt.Errorf("publish target catalog outcome %q", outcome), err)
	}
	candidate, err := h.state.GetServiceCatalogV3Candidate(ctx, liveRepository)
	if err != nil {
		return err
	}
	h.generation = candidate.Generation
	reconcile, err := h.state.BeginServiceStateV3Reconcile(ctx, liveRepository)
	if err != nil {
		return err
	}
	reconcileCost, err := runServiceStatePlan(ctx, h.state, reconcile, "t4110-cold-reconcile")
	if err != nil {
		return err
	}
	activation, err := h.state.BeginServiceStateV3Activation(ctx, liveRepository, h.searchDigest)
	if err != nil {
		return err
	}
	activationCost, err := runServiceStatePlan(ctx, h.state, activation, "t4110-cold-activate")
	if err != nil {
		return err
	}
	mergeStateCost(cost, reconcileCost, true)
	mergeStateCost(cost, activationCost, false)
	if cost.ChangedRows != t411.AcceptedServiceTarget {
		return fmt.Errorf("cold changed rows = %d", cost.ChangedRows)
	}
	readCost, _, _, err := h.acceptedSnapshot(ctx)
	if err != nil {
		return err
	}
	mergePhaseCosts(cost, readCost)
	if err := h.prepareRelationshipRuntimeInputs(ctx, cost); err != nil {
		return fmt.Errorf("prepare cold relationship runtime: %w", err)
	}
	relationship, err := h.reconcileEmptyRelationshipV3(ctx, cost)
	if err != nil {
		return fmt.Errorf("reconcile cold relationship: %w", err)
	}
	h.selector, err = h.selectRuntime(ctx, store.ServiceRuntimeSelector{}, relationship)
	if err != nil {
		return err
	}
	return nil
}

func encodeLiveV3Catalog(catalog servicecatalog.Catalog) ([]byte, error) {
	return json.Marshal(catalog)
}
