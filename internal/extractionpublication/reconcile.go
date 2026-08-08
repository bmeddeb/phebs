package extractionpublication

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/store"
)

type CandidateOpener func(context.Context, string) (*candidate.Publication, error)
type CandidateReferenceReader func(context.Context, string) (candidate.State, error)
type AuthorityReader func(context.Context, string) (source, observation string, err error)

type extractionRunStore interface {
	store.PartitionedExtractionRunStore
}

// Reconciler turns the current strict candidate and observation controls into
// one reserved execution generation. It performs no source-content read; the
// scheduler's GitSparseSource owns those leases later.
type Reconciler struct {
	Root               string
	CandidateRoot      string
	Runtime            *Runtime
	Evidence           extractionRunStore
	OpenCandidate      CandidateOpener
	Authority          AuthorityReader
	CandidateReference CandidateReferenceReader
	AuthorityReference AuthorityReader

	mu [64]sync.Mutex
}

func reconcileShard(repository string) int {
	sum := sha256.Sum256([]byte(repository))
	return int(sum[0]) % 64
}

func (reconciler *Reconciler) Reconcile(ctx context.Context, repository string) (string, error) {
	if reconciler == nil || reconciler.Runtime == nil || reconciler.Evidence == nil ||
		reconciler.OpenCandidate == nil || reconciler.Authority == nil ||
		reconciler.CandidateReference == nil || reconciler.AuthorityReference == nil ||
		!filepath.IsAbs(reconciler.Root) || !filepath.IsAbs(reconciler.CandidateRoot) {
		return "", invalid("reconciler configuration")
	}
	lock := &reconciler.mu[reconcileShard(repository)]
	lock.Lock()
	defer lock.Unlock()
	state, err := reconciler.CandidateReference(ctx, repository)
	if err != nil || state.Repository != repository {
		return "", errors.Join(err, ErrStale)
	}
	sourceReference, observationReference, err := reconciler.AuthorityReference(ctx, repository)
	if err != nil {
		return "", err
	}
	if target, reused, err := reconciler.Runtime.ReuseAuthority(ctx, PlanningAuthority{
		Repository:                  repository,
		CandidateManifestDigest:     state.ManifestDigest,
		CandidateGenerationDigest:   state.GenerationDigest,
		CandidatePolicyDigest:       state.PolicyDigest,
		SourceGenerationDigest:      sourceReference,
		ObservationGenerationDigest: observationReference,
	}); err != nil {
		return "", err
	} else if reused {
		confirmedState, candidateErr := reconciler.CandidateReference(ctx, repository)
		confirmedSource, confirmedObservation, authorityErr :=
			reconciler.AuthorityReference(ctx, repository)
		if candidateErr != nil || authorityErr != nil || confirmedState != state ||
			confirmedSource != sourceReference || confirmedObservation != observationReference {
			return "", errors.Join(candidateErr, authorityErr, ErrStale)
		}
		return target, nil
	}
	publication, err := reconciler.OpenCandidate(ctx, repository)
	if err != nil {
		return "", err
	}
	sparse, err := reconciler.prepareSparse(ctx, publication)
	if err != nil {
		return "", err
	}
	sourceDigest, observationDigest, err := reconciler.Authority(ctx, repository)
	if err != nil {
		return "", err
	}
	root := sparse.Root()
	plans := make([]candidate.DomainResultPlan, 0, len(root.Domains))
	for _, descriptor := range root.Domains {
		domain, err := sparse.OpenDomain(ctx, descriptor.Domain, descriptor.Version)
		if err != nil {
			return "", err
		}
		plan, err := BuildReservedPlan(domain, candidate.DomainResultAuthority{
			SourceGenerationDigest:      sourceDigest,
			ObservationGenerationDigest: observationDigest,
			ExtractorVersion:            descriptor.Version,
			ExtractionPolicyDigest:      root.PolicyDigest,
		})
		if err != nil {
			return "", err
		}
		plans = append(plans, plan)
	}
	if existing, present, err := reconciler.Runtime.ExistingPlans(ctx, repository, plans); err != nil {
		return "", err
	} else if present {
		return reconciler.Runtime.Reconcile(ctx, repository, existing)
	}
	publicationState := publication.State()
	domains := make([]DomainPlan, 0, len(plans))
	for _, plan := range plans {
		run, err := reconciler.Evidence.BeginPartitionedExtractionRun(ctx, store.ExtractionScope{
			Repository: repository, Commit: publicationState.Commit, UnitDigest: publicationState.UnitDigest,
			Domain: plan.Domain,
		}, plan.ExtractorVersion, plan.Digest, plan.CandidateManifestDigest,
			store.PartitionedExtractionRunLimits{
				Facts: plan.Reserved.Facts, Rows: plan.Reserved.Rows,
				References: plan.Reserved.References,
			})
		if err != nil || run == nil || run.ID == "" {
			reconciler.abortRuns(domains)
			if err == nil {
				err = errors.New("begin extraction run returned no identity")
			}
			return "", err
		}
		domains = append(domains, DomainPlan{Schema: DomainSchema, RunID: run.ID, Plan: plan})
	}
	return reconciler.Runtime.Reconcile(ctx, repository, domains)
}

func (reconciler *Reconciler) abortRuns(domains []DomainPlan) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, domain := range domains {
		_ = reconciler.Evidence.AbortExtractionRun(ctx, domain.RunID)
	}
}

func (reconciler *Reconciler) sparseDirectory(repository, generation string) string {
	return filepath.Join(reconciler.Root, "candidates", repositoryKey(repository), strings.TrimPrefix(generation, "sha256:"))
}

func (reconciler *Reconciler) prepareSparse(
	ctx context.Context,
	publication *candidate.Publication,
) (*candidate.SparsePublication, error) {
	if publication == nil {
		return nil, invalid("reconciler candidate publication")
	}
	state := publication.State()
	destination := reconciler.sparseDirectory(state.Repository, state.GenerationDigest)
	open := func() (*candidate.SparsePublication, error) {
		raw, err := readBounded(filepath.Join(destination, candidate.SparseRootFileName), candidate.MaxSparseRootBytes)
		if err != nil {
			return nil, err
		}
		// Candidate sparse controls deliberately carry one canonical trailing
		// newline. OpenSparse below revalidates the complete stage; this shallow
		// read obtains only the root authority needed to invoke it.
		control := bytes.TrimSuffix(raw, []byte{'\n'})
		if len(control)+1 != len(raw) {
			return nil, invalid("sparse root control")
		}
		var root candidate.SparseRoot
		if decodeExact(control, &root) != nil {
			return nil, invalid("sparse root control")
		}
		return candidate.OpenSparse(
			ctx, destination, reconciler.CandidateRoot, state, root.Digest, nil,
		)
	}
	if existing, err := open(); err == nil {
		return existing, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	parent := filepath.Dir(destination)
	if err := ensurePrivateDirectory(parent); err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp(parent, ".stage-sparse-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if err := os.Chmod(stage, 0o700); err != nil {
		return nil, err
	}
	root, err := candidate.BuildSparseRoot(ctx, stage, publication, nil)
	if err != nil {
		return nil, err
	}
	if err := os.Rename(stage, destination); err != nil {
		if _, openErr := open(); openErr != nil {
			return nil, errors.Join(err, openErr)
		}
	} else if err := syncDirectory(parent); err != nil {
		return nil, err
	}
	sparse, err := candidate.OpenSparse(
		ctx, destination, reconciler.CandidateRoot, state, root.Digest, nil,
	)
	if err != nil {
		return nil, fmt.Errorf("open prepared sparse candidate: %w", err)
	}
	return sparse, nil
}

// OpenDomain reopens exactly the candidate/sparse authority bound by plan.
func (reconciler *Reconciler) OpenDomain(
	ctx context.Context,
	plan candidate.DomainResultPlan,
) (*candidate.SparseDomain, error) {
	publication, err := reconciler.OpenCandidate(ctx, plan.Repository)
	if err != nil {
		return nil, err
	}
	state := publication.State()
	if state.ManifestDigest != plan.CandidateManifestDigest ||
		state.GenerationDigest != plan.CandidateGenerationDigest {
		return nil, ErrStale
	}
	directory := reconciler.sparseDirectory(plan.Repository, plan.CandidateGenerationDigest)
	sparse, err := candidate.OpenSparse(
		ctx, directory, reconciler.CandidateRoot, state,
		plan.CandidatePartitionRootDigest, nil,
	)
	if err != nil {
		return nil, err
	}
	return sparse.OpenDomain(ctx, plan.Domain, plan.Version)
}
