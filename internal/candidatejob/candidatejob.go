// Package candidatejob owns the index-to-extraction boundary for candidate
// manifests. It serializes planning with mirror mutation, publishes derived
// artifacts before their guarded database pointer, and repairs either half of
// that transition on retry.
package candidatejob

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/repowork"
	"github.com/bmeddeb/phebs/internal/store"
	reposync "github.com/bmeddeb/phebs/internal/sync"
)

// Store is the narrow state boundary needed by the candidate planner.
type Store interface {
	store.CandidateManifestPublicationStore
	GetRepo(ctx context.Context, name string) (*store.Repo, error)
}

// PolicySet is one validated immutable planner/provider policy generation.
// Keeping the predicates and serialized identities behind this type prevents
// the job and extraction adapter from independently reconstructing policy.
type PolicySet struct {
	policies   []candidate.Policy
	identities []candidate.PolicyIdentity
	digest     string
}

// CompilePolicies freezes the current extractor registry into one shared
// candidate policy set.
func CompilePolicies(extractors []extract.Extractor) (*PolicySet, error) {
	policies, err := extract.CandidatePolicies(slices.Clone(extractors))
	if err != nil {
		return nil, fmt.Errorf("candidate policies: %w", err)
	}
	identities, err := candidate.PolicyIdentities(policies)
	if err != nil {
		return nil, fmt.Errorf("candidate policy identities: %w", err)
	}
	digest, err := candidate.PolicyDigest(identities)
	if err != nil {
		return nil, fmt.Errorf("candidate policy digest: %w", err)
	}
	return &PolicySet{
		policies:   slices.Clone(policies),
		identities: slices.Clone(identities),
		digest:     digest,
	}, nil
}

// Digest returns the content identity shared by planner and provider.
func (policies *PolicySet) Digest() string {
	if policies == nil {
		return ""
	}
	return policies.digest
}

func (policies *PolicySet) validate() error {
	if policies == nil {
		return errors.New("candidate policies are required")
	}
	identities, err := candidate.PolicyIdentities(policies.policies)
	if err != nil {
		return err
	}
	digest, err := candidate.PolicyDigest(identities)
	if err != nil {
		return err
	}
	if !candidate.EqualPolicyIdentities(identities, policies.identities) ||
		digest != policies.digest {
		return errors.New("candidate policy set is inconsistent")
	}
	return nil
}

// Worker plans and publishes one current candidate generation.
type Worker struct {
	dataDir      string
	root         string
	store        Store
	policies     *PolicySet
	fingerprints *controlFingerprintCache
	open         func(context.Context, string, candidate.Expected) (*candidate.Publication, error)
	recover      func(context.Context, string, candidate.Expected) (*candidate.Publication, error)
}

// New constructs the planner and extraction provider from the same frozen
// policy object. This is the preferred production wiring.
func New(
	dataDir string,
	state Store,
	extractors []extract.Extractor,
) (*Worker, *Provider, error) {
	policies, err := CompilePolicies(extractors)
	if err != nil {
		return nil, nil, err
	}
	worker, err := NewWorker(dataDir, state, policies)
	if err != nil {
		return nil, nil, err
	}
	provider, err := NewProvider(dataDir, state, policies)
	if err != nil {
		return nil, nil, err
	}
	return worker, provider, nil
}

// NewWorker constructs a planner from an already frozen policy set.
func NewWorker(dataDir string, state Store, policies *PolicySet) (*Worker, error) {
	if state == nil {
		return nil, errors.New("candidate worker store is required")
	}
	if err := validateDataDir(dataDir); err != nil {
		return nil, err
	}
	if err := policies.validate(); err != nil {
		return nil, fmt.Errorf("candidate worker policies: %w", err)
	}
	return &Worker{
		dataDir:      dataDir,
		root:         CandidateRoot(dataDir),
		store:        state,
		policies:     policies,
		fingerprints: newControlFingerprintCache(),
		open:         candidate.OpenContext,
		recover:      candidate.OpenPublishingContext,
	}, nil
}

// CandidateRoot returns the derived-artifact root for one data directory.
func CandidateRoot(dataDir string) string {
	return filepath.Join(dataDir, "candidates")
}

func validateDataDir(dataDir string) error {
	if dataDir == "" || !filepath.IsAbs(dataDir) {
		return errors.New("candidate data directory must be absolute")
	}
	return nil
}

// Handle adapts candidate planning to store.Runner.
func (worker *Worker) Handle(ctx context.Context, job store.Job) error {
	if err := worker.handle(ctx, job); err != nil {
		return store.WithClass(
			store.ClassExtract,
			fmt.Errorf("candidate manifest %s: %w", job.Target, err),
		)
	}
	return nil
}

func (worker *Worker) handle(ctx context.Context, job store.Job) error {
	if worker == nil || worker.store == nil || worker.policies == nil ||
		worker.fingerprints == nil || worker.open == nil ||
		worker.recover == nil {
		return errors.New("worker is not initialized")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	repoDir, err := reposync.SafeRepoDir(worker.dataDir, job.Target)
	if err != nil {
		return fmt.Errorf("mirror path: %w", err)
	}
	unlock, err := repowork.LockContext(ctx, repoDir)
	if err != nil {
		return fmt.Errorf("lock mirror: %w", err)
	}
	defer unlock()

	repository, err := worker.store.GetRepo(ctx, job.Target)
	if errors.Is(err, store.ErrNotFound) {
		worker.fingerprints.forget(job.Target)
		return nil
	}
	if err != nil {
		return fmt.Errorf("load repository: %w", err)
	}
	if repository == nil {
		return errors.New("repository store returned nil")
	}
	if repository.Deleting || repository.IndexedCommitHash == "" {
		worker.fingerprints.forget(repository.Name)
		return nil
	}
	if repository.Name != job.Target {
		return fmt.Errorf("stored repository name is %q", repository.Name)
	}
	if repository.IndexedAnalysisUnit != nil {
		if err := repository.IndexedAnalysisUnit.Validate(repository.Name); err != nil {
			return fmt.Errorf("committed analysis unit: %w", err)
		}
	}
	if err := ensureCandidateRoot(worker.root, true); err != nil {
		return err
	}

	expected, err := worker.expected(repository)
	if err != nil {
		return err
	}
	currentPointer, pointerErr := worker.store.GetCandidateManifestPublication(
		ctx, repository.Name,
	)
	var persistedState *candidate.State
	var persistedControlRevision uint64
	if errors.Is(pointerErr, store.ErrInvalidCandidateManifestPublication) {
		// Candidate publications are derived state. Under the same mirror lock
		// that fences extraction, discard a corrupt pointer so strict
		// crash-marker recovery can reuse complete bytes or rebuild them.
		if err := worker.store.ClearCandidateManifestPublication(
			ctx, repository.Name,
		); err != nil {
			return fmt.Errorf("clear invalid publication pointer: %w", err)
		}
		currentPointer = nil
		pointerErr = store.ErrNotFound
	}
	if pointerErr != nil && !errors.Is(pointerErr, store.ErrNotFound) {
		return fmt.Errorf("load publication pointer: %w", pointerErr)
	}
	if pointerErr == nil {
		if currentPointer == nil {
			return errors.New("publication store returned nil")
		}
		state, err := pointerState(*currentPointer)
		if err != nil {
			return fmt.Errorf("publication pointer: %w", err)
		}
		persistedState = &state
		persistedControlRevision = currentPointer.ControlRevision
	}
	repairNeeded := false
	if persistedState != nil {
		if outcomes, ok := worker.store.(store.CandidateControlOutcomeStore); ok {
			repairNeeded, err = outcomes.CandidateControlRepairNeeded(
				ctx, *currentPointer,
			)
			if err != nil {
				return fmt.Errorf("load candidate control outcome: %w", err)
			}
		}
	}

	// A marker may mean the database commit succeeded and only marker cleanup
	// was interrupted, or that filesystem publication stopped midway. Keep it
	// installed throughout strict recovery: a second crash must remain
	// distinguishable from a clean exact-pointer no-op. The guarded database
	// transition removes it only after matching state and fan-out are durable.
	hadMarker := candidate.IsPublishing(worker.root, repository.Name)
	// Unmarked filesystem bytes without a database pointer have no authority:
	// they may be an orphan or a self-consistent forged generation. Reuse is
	// limited to an exact persisted pointer or the marker left by Publish
	// before its guarded database transition.
	controlReady := false
	if persistedState != nil {
		controlReady, err = publicationControlReady(
			worker.root, *persistedState,
		)
		if err != nil {
			return err
		}
	}
	if persistedState != nil && controlReady && !hadMarker && !job.Force &&
		!repairNeeded &&
		publicationMatchesExpected(*persistedState, expected) {
		known, unchanged, err := worker.fingerprints.matches(
			ctx, worker.root, *persistedState,
		)
		if err != nil {
			return fmt.Errorf("check publication control fingerprint: %w", err)
		}
		if known && unchanged {
			// The guarded pointer and process-local file identities are
			// unchanged. Re-publishing repairs a missing extraction fan-out
			// without re-reading, hashing, or externally sorting any member.
			return worker.commitPublication(
				ctx, repository, *persistedState, 0,
			)
		}
		if !known {
			// A cold worker establishes only a manifest/member-identity
			// fingerprint. Actual extraction remains the strict first
			// consumer; a later identity change forces the strict path below.
			fingerprint, captureErr :=
				candidate.CaptureControlFingerprintContext(
					ctx, worker.root, *persistedState,
				)
			if captureErr == nil {
				worker.fingerprints.remember(
					*persistedState, fingerprint,
				)
				return worker.commitPublication(
					ctx, repository, *persistedState, 0,
				)
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			// Missing, special, wrong-size, or invalid control bytes are
			// derived-state damage. Rebuild below without adopting them.
		}
	}
	if hadMarker {
		reuseExpected := expected
		if persistedState != nil {
			reuseExpected.ManifestDigest = persistedState.ManifestDigest
		}
		if published, openErr := worker.recover(
			ctx, worker.root, reuseExpected,
		); openErr == nil &&
			(persistedState == nil || published.State() == *persistedState) {
			if err := worker.rememberFingerprint(
				ctx, published.State(),
			); err == nil {
				return worker.commitPublication(
					ctx, repository, published.State(),
					nextControlRevision(persistedControlRevision),
				)
			}
		}
	}
	if !hadMarker && persistedState != nil && controlReady {
		reuseExpected := expected
		reuseExpected.ManifestDigest = persistedState.ManifestDigest
		if published, openErr := worker.open(
			ctx, worker.root, reuseExpected,
		); openErr == nil && published.State() == *persistedState {
			if err := worker.rememberFingerprint(
				ctx, published.State(),
			); err == nil {
				return worker.commitPublication(
					ctx, repository, published.State(),
					nextControlRevision(persistedControlRevision),
				)
			}
		}
	}

	stage, err := candidate.NewStage(worker.root)
	if err != nil {
		return fmt.Errorf("create stage: %w", err)
	}
	defer func() { _ = os.RemoveAll(stage) }()

	manifest, err := candidate.Build(ctx, candidate.Request{
		RepoDir: repoDir, OutputDir: stage,
		Repository: repository.Name, Commit: repository.IndexedCommitHash,
		Unit:     analysisunit.CloneState(repository.IndexedAnalysisUnit),
		Policies: slices.Clone(worker.policies.policies),
	})
	if err != nil {
		return fmt.Errorf("build: %w", err)
	}
	expected.ManifestDigest = manifest.Digest
	state, err := candidate.PublishContext(ctx, worker.root, stage, expected)
	if err != nil {
		return fmt.Errorf("publish files: %w", err)
	}
	if err := worker.rememberFingerprint(ctx, state); err != nil {
		return fmt.Errorf("record publication control fingerprint: %w", err)
	}
	return worker.commitPublication(
		ctx, repository, state,
		nextControlRevision(persistedControlRevision),
	)
}

func nextControlRevision(current uint64) uint64 {
	if current == 0 {
		return 0
	}
	return current + 1
}

func (worker *Worker) rememberFingerprint(
	ctx context.Context,
	state candidate.State,
) error {
	fingerprint, err := candidate.CaptureControlFingerprintContext(
		ctx, worker.root, state,
	)
	if err != nil {
		return err
	}
	worker.fingerprints.remember(state, fingerprint)
	return nil
}

func publicationControlReady(root string, state candidate.State) (bool, error) {
	if err := state.Validate(); err != nil {
		return false, err
	}
	info, err := os.Lstat(filepath.Join(root, state.Manifest))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect candidate manifest control file: %w", err)
	}
	return info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0, nil
}

func publicationMatchesExpected(
	state candidate.State,
	expected candidate.Expected,
) bool {
	return state.Repository == expected.Repository &&
		state.Commit == expected.Commit &&
		state.UnitDigest == expectedUnitDigest(expected.Unit) &&
		state.PolicyDigest == expected.PolicyDigest &&
		state.GenerationDigest == expected.GenerationDigest &&
		state.Manifest == candidate.ManifestName(expected.Repository)
}

func expectedUnitDigest(unit *analysisunit.State) string {
	if unit == nil {
		return ""
	}
	return unit.Digest
}

func (worker *Worker) expected(repository *store.Repo) (candidate.Expected, error) {
	_, err := checkedUnitDigest(
		repository.Name, repository.IndexedAnalysisUnit,
	)
	if err != nil {
		return candidate.Expected{}, err
	}
	generation, err := candidate.GenerationDigest(
		repository.Name, repository.IndexedCommitHash,
		repository.IndexedAnalysisUnit,
		worker.policies.identities,
	)
	if err != nil {
		return candidate.Expected{}, fmt.Errorf("generation identity: %w", err)
	}
	return candidate.Expected{
		Repository:       repository.Name,
		Commit:           repository.IndexedCommitHash,
		Unit:             analysisunit.CloneState(repository.IndexedAnalysisUnit),
		Policies:         slices.Clone(worker.policies.identities),
		PolicyDigest:     worker.policies.digest,
		GenerationDigest: generation,
	}, nil
}

func (worker *Worker) commitPublication(
	ctx context.Context,
	repository *store.Repo,
	state candidate.State,
	controlRevision uint64,
) error {
	publication, err := publicationFromState(state)
	if err != nil {
		return err
	}
	publication.ControlRevision = controlRevision
	if err := worker.store.PublishCandidateManifest(ctx, publication); err != nil {
		if !errors.Is(err, store.ErrConflict) {
			// Keep the marker installed. A retry can validate and reuse these
			// exact bytes before repairing the database transition.
			return fmt.Errorf("commit pointer and extraction fan-out: %w", err)
		}
		current, currentErr := worker.store.GetRepo(ctx, repository.Name)
		if errors.Is(currentErr, store.ErrNotFound) ||
			currentErr == nil && !sameIndexedGeneration(current, repository) {
			// Index advance and deletion own the successor. The marker remains
			// fail-closed until that candidate job or deletion cleanup runs.
			return nil
		}
		if currentErr != nil {
			return fmt.Errorf("reload after publication conflict: %w", currentErr)
		}
		return fmt.Errorf(
			"commit pointer and extraction fan-out: deterministic publication conflict: %w",
			err,
		)
	}
	if err := candidate.FinishPublication(worker.root, repository.Name); err != nil {
		return fmt.Errorf("finish publication: %w", err)
	}
	return nil
}

func sameIndexedGeneration(left, right *store.Repo) bool {
	return left != nil && right != nil &&
		left.Name == right.Name &&
		!left.Deleting &&
		left.IndexedCommitHash != "" &&
		left.IndexedCommitHash == right.IndexedCommitHash &&
		analysisunit.EqualState(
			left.IndexedAnalysisUnit, right.IndexedAnalysisUnit,
		)
}

func checkedUnitDigest(
	repository string,
	unit *analysisunit.State,
) (string, error) {
	if unit == nil {
		return "", nil
	}
	if err := unit.Validate(repository); err != nil {
		return "", fmt.Errorf("analysis unit: %w", err)
	}
	return unit.Digest, nil
}

func publicationFromState(
	state candidate.State,
) (store.CandidateManifestPublication, error) {
	if err := state.Validate(); err != nil {
		return store.CandidateManifestPublication{}, err
	}
	return store.CandidateManifestPublication{
		Repository:       state.Repository,
		HeadCommit:       state.Commit,
		UnitDigest:       state.UnitDigest,
		PolicyDigest:     state.PolicyDigest,
		ManifestDigest:   state.ManifestDigest,
		GenerationDigest: state.GenerationDigest,
		ManifestPath:     state.Manifest,
	}, nil
}

func pointerState(
	publication store.CandidateManifestPublication,
) (candidate.State, error) {
	if publication.PublishedAt.IsZero() {
		return candidate.State{}, errors.New("publication timestamp is missing")
	}
	state := candidate.State{
		Schema:     candidate.StateSchema,
		Repository: publication.Repository, Commit: publication.HeadCommit,
		UnitDigest:       publication.UnitDigest,
		PolicyDigest:     publication.PolicyDigest,
		ManifestDigest:   publication.ManifestDigest,
		GenerationDigest: publication.GenerationDigest,
		Manifest:         publication.ManifestPath,
	}
	if err := state.Validate(); err != nil {
		return candidate.State{}, err
	}
	return state, nil
}

func ensureCandidateRoot(root string, create bool) error {
	if create {
		if err := os.MkdirAll(root, 0o700); err != nil {
			return fmt.Errorf("create candidate root: %w", err)
		}
	}
	info, err := os.Lstat(root)
	if err != nil {
		return fmt.Errorf("candidate root: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("candidate root is not a real directory")
	}
	return nil
}
