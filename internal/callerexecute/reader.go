package callerexecute

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"sync"

	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/callerpublication"
	"github.com/bmeddeb/phebs/internal/store"
)

// PublicationAvailability is the exact product-read posture for one
// authorized repository. Only Current carries a lease and may expose caller
// rows.
type PublicationAvailability string

const (
	PublicationMissing PublicationAvailability = "missing"
	PublicationStale   PublicationAvailability = "stale"
	PublicationFailed  PublicationAvailability = "failed"
	PublicationCurrent PublicationAvailability = "current"

	publicationFailureCacheEntries  = 8
	publicationAdmissionFlightLimit = 64
)

// PublicationReadStore is the read-only store boundary for exact caller-map
// snapshots. Publication bytes remain filesystem-owned; this interface only
// selects and rechecks their store authority.
type PublicationReadStore interface {
	authorityStore
	GetCallerGenerationPublication(
		context.Context, string,
	) (*store.CallerGenerationPublication, error)
	CallerGenerationPublicationSummaryCurrent(
		context.Context, store.CallerGenerationPublicationSummary,
	) (bool, error)
	CallerGenerationPublicationSummaryAuthorityCurrent(
		context.Context, store.CallerGenerationPublicationSummary,
	) (bool, error)
	GetCallerGenerationAdmission(
		context.Context, store.CallerGenerationIdentity,
	) (*store.CallerGenerationAdmission, error)
}

// PublicationBinding is the bounded immutable cursor input for reopening one
// already exact-validated publication. It deliberately excludes the full pair
// array. Summary binds the store revision and payload commitment; State binds
// the cold-validated filesystem generation admitted by the shared registry.
type PublicationBinding struct {
	Summary store.CallerGenerationPublicationSummary `json:"summary"`
	State   callerpublication.State                  `json:"state"`
}

// PublicationRead is one request-scoped exact caller-generation snapshot.
// The caller must Release it. Summary, State, and Resolver are defensive
// copies of the bounded cursor/fencing inputs. Publication additionally holds
// the exact full pointer on the first Open and is nil on a bound Reopen;
// Revision repeats the monotonic fence so cursor builders need not infer it.
type PublicationRead struct {
	Availability PublicationAvailability

	ExpectedGeneration store.CallerGenerationIdentity
	Resolver           *store.ResolverCatalogPublication
	Publication        *store.CallerGenerationPublication
	Summary            *store.CallerGenerationPublicationSummary
	State              *callerpublication.State
	Revision           uint64

	lease *callerpublication.Lease
	once  sync.Once
	// scalarFailureFence marks a deterministic cold filesystem refusal whose
	// exact store authority can be confirmed without repeating content
	// validation at the end of the same request.
	scalarFailureFence bool
}

// Lease returns the request-scoped immutable publication lease. It is nil for
// every non-serving availability.
func (read *PublicationRead) Lease() *callerpublication.Lease {
	if read == nil {
		return nil
	}
	return read.lease
}

// Binding returns the bounded immutable input for a subsequent page. It is
// available only while the read is serving and never includes the complete
// store pair payload or process-local lease.
func (read *PublicationRead) Binding() (PublicationBinding, error) {
	if read == nil || read.Summary == nil || read.State == nil || read.lease == nil ||
		read.Availability != PublicationCurrent {
		return PublicationBinding{}, errors.New(
			"caller publication read has no current binding",
		)
	}
	return PublicationBinding{
		Summary: *read.Summary,
		State:   cloneCallerPublicationState(*read.State),
	}, nil
}

// Release relinquishes the immutable generation after response construction.
// It is idempotent so transport error paths can defer it immediately.
func (read *PublicationRead) Release() (resultErr error) {
	if read == nil {
		return nil
	}
	read.once.Do(func() {
		if read.lease != nil {
			resultErr = read.lease.Release()
			read.lease = nil
		}
	})
	return resultErr
}

// PublicationReader opens complete caller generations through the same
// runtime extractor identity and process-wide lease registry as the worker.
type PublicationReader struct {
	state        PublicationReadStore
	adapters     *Registry
	publications *callerpublication.Registry

	failureMu    sync.Mutex
	failures     map[string]struct{}
	failureOrder []string

	admissionMu sync.Mutex
	admissions  map[string]*publicationAdmissionFlight

	testBeforeAdmission func()
	testAfterAdmission  func(bool)
	testBeforeAcquire   func()
}

type publicationAdmissionFlight struct {
	done chan struct{}
}

func NewPublicationReader(
	state PublicationReadStore,
	adapters *Registry,
	publications *callerpublication.Registry,
) (*PublicationReader, error) {
	if state == nil || publications == nil {
		return nil, errors.New("caller publication reader is not configured")
	}
	if err := adapters.validate(); err != nil {
		return nil, err
	}
	return &PublicationReader{
		state: state, adapters: adapters, publications: publications,
		failures: make(map[string]struct{}), admissions: make(map[string]*publicationAdmissionFlight),
	}, nil
}

// Open returns one typed availability. Operational store/filesystem failures
// remain errors; deterministic invalid derived state is reported as Failed so
// an authorized caller never mistakes it for zero callers.
func (reader *PublicationReader) Open(
	ctx context.Context,
	repository string,
) (*PublicationRead, error) {
	if reader == nil || reader.state == nil || reader.adapters == nil ||
		reader.publications == nil {
		return nil, errors.New("caller publication reader is not configured")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	result := &PublicationRead{
		Availability:       PublicationMissing,
		ExpectedGeneration: store.CallerGenerationIdentity{Repository: repository},
	}
	current, err := currentAuthority(ctx, reader.state, reader.adapters, repository)
	if err != nil {
		if deterministicPublicationReadFailure(err) {
			result.Availability = PublicationFailed
			return result, nil
		}
		return nil, err
	}
	if current != nil {
		result.ExpectedGeneration = current.stored
		resolver := cloneResolverCatalogPublication(*current.resolver)
		result.Resolver = &resolver
	}
	authorityAvailable := current != nil

	pointer, err := reader.state.GetCallerGenerationPublication(ctx, repository)
	if errors.Is(err, store.ErrNotFound) {
		return reader.classifyMissing(ctx, result)
	}
	if err != nil {
		if deterministicPublicationReadFailure(err) {
			result.Availability = PublicationFailed
			return result, nil
		}
		return nil, err
	}
	if pointer == nil {
		return nil, errors.New("caller publication store returned a nil pointer")
	}
	ownedPointer := cloneCallerGenerationPublication(*pointer)
	summary := callerPublicationSummary(ownedPointer)
	result.Publication = &ownedPointer
	result.Summary = &summary
	result.Revision = ownedPointer.PublicationRevision
	if !authorityAvailable {
		result.Availability = PublicationStale
		return result, nil
	}

	publicationState, err := callerPublicationStateFromStore(
		summary, reader.adapters.ExtractorIdentities(),
	)
	if err != nil {
		result.Availability = PublicationFailed
		return result, nil
	}
	return reader.acquire(ctx, result, publicationState, true)
}

// Reopen reacquires an exact cursor-bound generation without reading or
// hashing the store's complete pair payload. The bounded summary is checked
// against every mutable authority before and after registry acquisition; the
// immutable State selects the already cold-validated filesystem publication.
// A cache miss may cold-validate that filesystem publication, but never loads
// the store pair array. Resolver declarations are re-read and generation-bound
// so endpoint selection uses the same catalog as the leased generation.
func (reader *PublicationReader) Reopen(
	ctx context.Context,
	bound PublicationBinding,
) (*PublicationRead, error) {
	if reader == nil || reader.state == nil || reader.adapters == nil ||
		reader.publications == nil {
		return nil, errors.New("caller publication reader is not configured")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	result := &PublicationRead{
		Availability:       PublicationMissing,
		ExpectedGeneration: bound.Summary.Generation,
		Revision:           bound.Summary.PublicationRevision,
	}
	summary := bound.Summary
	result.Summary = &summary
	wantedState, err := callerPublicationStateFromStore(
		summary, reader.adapters.ExtractorIdentities(),
	)
	if err != nil || !reflect.DeepEqual(wantedState, bound.State) {
		result.Availability = PublicationFailed
		return result, nil
	}
	publicationState := cloneCallerPublicationState(bound.State)

	resolver, err := reader.boundResolver(ctx, summary.Generation)
	if errors.Is(err, store.ErrNotFound) ||
		errors.Is(err, errPublicationReadTransition) {
		result.Availability = PublicationStale
		return result, nil
	}
	if err != nil {
		if deterministicPublicationReadFailure(err) {
			result.Availability = PublicationFailed
			return result, nil
		}
		return nil, err
	}
	result.Resolver = resolver
	return reader.acquire(ctx, result, publicationState, false)
}

var errPublicationReadTransition = errors.New(
	"caller publication authority transitioned",
)

func (reader *PublicationReader) boundResolver(
	ctx context.Context,
	generation store.CallerGenerationIdentity,
) (*store.ResolverCatalogPublication, error) {
	pointer, err := reader.state.GetResolverCatalogPublication(
		ctx, generation.Repository,
	)
	if err != nil {
		return nil, err
	}
	if pointer == nil {
		return nil, errors.New("resolver catalog store returned a nil pointer")
	}
	if pointer.Repository != generation.Repository ||
		pointer.HeadCommit != generation.HeadCommit ||
		pointer.UnitDigest != generation.UnitDigest ||
		pointer.DeclarationSetDigest != generation.DeclarationSetDigest ||
		pointer.CandidateManifestDigest != generation.CandidateManifestDigest ||
		pointer.SourceLanePolicy != generation.SourceLanePolicy ||
		pointer.GenerationDigest != generation.ResolverGenerationDigest ||
		pointer.ManifestDigest != generation.ResolverManifestDigest ||
		pointer.ControlRevision != generation.ResolverControlRevision ||
		pointer.WriterSchema != generation.ResolverWriterSchema {
		return nil, errPublicationReadTransition
	}
	current, err := reader.state.ResolverCatalogPublicationCurrent(ctx, *pointer)
	if err != nil {
		return nil, err
	}
	if !current {
		return nil, errPublicationReadTransition
	}
	owned := cloneResolverCatalogPublication(*pointer)
	return &owned, nil
}

func (reader *PublicationReader) acquire(
	ctx context.Context,
	result *PublicationRead,
	publicationState callerpublication.State,
	exactFinalFence bool,
) (*PublicationRead, error) {
	ownedState := cloneCallerPublicationState(publicationState)
	result.State = &ownedState
	storeCurrent, err := reader.state.
		CallerGenerationPublicationSummaryAuthorityCurrent(ctx, *result.Summary)
	if err != nil {
		if deterministicPublicationReadFailure(err) {
			result.Availability = PublicationFailed
			return result, nil
		}
		return nil, err
	}
	if !storeCurrent {
		result.Availability = PublicationStale
		return result, nil
	}
	failureKey := publicationFailureKey(*result.Summary, publicationState)
	if reader.failedAdmission(failureKey) {
		result.Availability = PublicationFailed
		result.scalarFailureFence = true
		return result, nil
	}
	if reader.testBeforeAdmission != nil {
		reader.testBeforeAdmission()
	}
	leader, pending, finishAdmission, err := reader.beginAdmission(failureKey)
	if err != nil {
		return nil, err
	}
	if reader.testAfterAdmission != nil {
		reader.testAfterAdmission(leader)
	}
	if leader {
		defer finishAdmission()
		// A prior leader may have retained this exact deterministic failure
		// after our optimistic cache lookup but before we registered the next
		// flight. Do not repeat its bounded cold validation.
		if reader.failedAdmission(failureKey) {
			result.Availability = PublicationFailed
			result.scalarFailureFence = true
			return result, nil
		}
	} else {
		select {
		case <-pending:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		// The leader records a deterministic cold failure before releasing
		// waiters. On success, the publication registry is warm and every
		// waiter may take its ordinary concurrent fast path.
		if reader.failedAdmission(failureKey) {
			result.Availability = PublicationFailed
			result.scalarFailureFence = true
			return result, nil
		}
	}
	if reader.testBeforeAcquire != nil {
		reader.testBeforeAcquire()
	}

	lease, err := reader.publications.Acquire(ctx, publicationState)
	if err != nil {
		switch {
		case deterministicFilesystemReadFailure(err):
			reader.retainFailedAdmission(failureKey)
			result.scalarFailureFence = true
			result.Availability = PublicationFailed
			return result, nil
		case deterministicPublicationReadFailure(err):
			result.Availability = PublicationFailed
			return result, nil
		case errors.Is(err, callerpublication.ErrPublishing),
			errors.Is(err, callerpublication.ErrRegistryConflict):
			result.Availability = PublicationStale
			return result, nil
		default:
			return nil, err
		}
	}
	reader.dropFailedAdmission(failureKey)
	result.lease = lease

	if exactFinalFence {
		storeCurrent, err = reader.state.
			CallerGenerationPublicationSummaryCurrent(ctx, *result.Summary)
	} else {
		storeCurrent, err = reader.state.
			CallerGenerationPublicationSummaryAuthorityCurrent(ctx, *result.Summary)
	}
	publicationCurrent := false
	var publicationErr error
	if lease.Publication() != nil {
		publicationCurrent, publicationErr = lease.Publication().CurrentResult()
	}
	if err != nil || publicationErr != nil || !storeCurrent ||
		!publicationCurrent ||
		!reflect.DeepEqual(lease.State(), publicationState) {
		releaseErr := result.Release()
		if err != nil && !deterministicPublicationReadFailure(err) {
			return nil, errors.Join(err, wrapCallerReleaseError(releaseErr))
		}
		if publicationErr != nil {
			return nil, errors.Join(
				publicationErr, wrapCallerReleaseError(releaseErr),
			)
		}
		if releaseErr != nil {
			return nil, fmt.Errorf("release transitioned caller publication: %w", releaseErr)
		}
		if err != nil {
			result.Availability = PublicationFailed
		} else {
			result.Availability = PublicationStale
		}
		return result, nil
	}

	result.Availability = PublicationCurrent
	return result, nil
}

func (reader *PublicationReader) beginAdmission(
	key string,
) (bool, <-chan struct{}, func(), error) {
	reader.admissionMu.Lock()
	defer reader.admissionMu.Unlock()
	if pending := reader.admissions[key]; pending != nil {
		return false, pending.done, nil, nil
	}
	if len(reader.admissions) >= publicationAdmissionFlightLimit {
		return false, nil, nil, fmt.Errorf(
			"%w: caller publication admission flights are full",
			callerleaf.ErrCapacity,
		)
	}
	flight := &publicationAdmissionFlight{done: make(chan struct{})}
	reader.admissions[key] = flight
	return true, nil, func() {
		reader.admissionMu.Lock()
		if reader.admissions[key] == flight {
			delete(reader.admissions, key)
			close(flight.done)
		}
		reader.admissionMu.Unlock()
	}, nil
}

func wrapCallerReleaseError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("release caller publication: %w", err)
}

func (reader *PublicationReader) classifyMissing(
	ctx context.Context,
	result *PublicationRead,
) (*PublicationRead, error) {
	if result.ExpectedGeneration.Repository == "" ||
		result.ExpectedGeneration.Digest == "" {
		return result, nil
	}
	admission, err := reader.state.GetCallerGenerationAdmission(
		ctx, result.ExpectedGeneration,
	)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return result, nil
	case err != nil && deterministicPublicationReadFailure(err):
		result.Availability = PublicationFailed
		return result, nil
	case err != nil:
		return nil, err
	case admission == nil:
		return nil, errors.New("caller admission store returned a nil record")
	case admission.Disposition == store.CallerGenerationTerminalGenerationRefusal:
		result.Availability = PublicationFailed
	}
	return result, nil
}

// Current is the result-time transition fence. Authorization is deliberately
// outside this package and must be rechecked separately by the transport-neutral
// Caller Map service before it serializes rows or citations.
func (reader *PublicationReader) Current(
	ctx context.Context,
	read *PublicationRead,
) (bool, error) {
	if reader == nil || read == nil || read.Summary == nil || read.State == nil ||
		read.lease == nil || read.Availability != PublicationCurrent {
		return false, nil
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	current, err := reader.state.
		CallerGenerationPublicationSummaryAuthorityCurrent(ctx, *read.Summary)
	if err != nil || !current {
		return false, err
	}
	publication := read.lease.Publication()
	if publication == nil {
		return false, nil
	}
	filesystemCurrent, err := publication.CurrentResult()
	if err != nil || !filesystemCurrent {
		return false, err
	}
	return reflect.DeepEqual(read.lease.State(), *read.State), nil
}

// UnavailableCurrent is the result-time fence for a typed missing, stale, or
// failed read. It repeats the same authority/admission selection as Open so a
// pointerless terminal admission or authority transition cannot be serialized
// under an older gap classification. The repeated read is bounded and carries
// no lease unless the state became current, in which case that lease is
// released before returning false.
func (reader *PublicationReader) UnavailableCurrent(
	ctx context.Context,
	read *PublicationRead,
) (bool, error) {
	if reader == nil || read == nil ||
		read.Availability == PublicationCurrent ||
		!read.Availability.valid() || read.ExpectedGeneration.Repository == "" {
		return false, nil
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if read.scalarFailureFence && read.Summary != nil && read.State != nil {
		current, err := reader.state.
			CallerGenerationPublicationSummaryAuthorityCurrent(ctx, *read.Summary)
		if err != nil {
			return false, err
		}
		return current, nil
	}
	confirmed, err := reader.Open(ctx, read.ExpectedGeneration.Repository)
	if err != nil {
		return false, err
	}
	if confirmed == nil {
		return false, errors.New("caller publication reader returned no gap state")
	}
	current := sameUnavailableRead(read, confirmed)
	if releaseErr := confirmed.Release(); releaseErr != nil {
		return false, fmt.Errorf("release confirmed caller publication: %w", releaseErr)
	}
	return current, nil
}

func publicationFailureKey(
	summary store.CallerGenerationPublicationSummary,
	state callerpublication.State,
) string {
	return fmt.Sprintf(
		"%s\x00%s\x00%s\x00%s\x00%d\x00%s\x00%d",
		summary.Generation.Repository, state.Generation.Digest,
		state.ManifestDigest, state.PairSetDigest, summary.PublicationRevision,
		summary.PublicationIncarnation, summary.PublishedAt.UnixNano(),
	)
}

func (reader *PublicationReader) failedAdmission(key string) bool {
	reader.failureMu.Lock()
	defer reader.failureMu.Unlock()
	_, found := reader.failures[key]
	return found
}

func (reader *PublicationReader) retainFailedAdmission(key string) {
	reader.failureMu.Lock()
	defer reader.failureMu.Unlock()
	if _, found := reader.failures[key]; found {
		return
	}
	for len(reader.failureOrder) >= publicationFailureCacheEntries {
		victim := reader.failureOrder[0]
		reader.failureOrder = reader.failureOrder[1:]
		delete(reader.failures, victim)
	}
	reader.failures[key] = struct{}{}
	reader.failureOrder = append(reader.failureOrder, key)
}

func (reader *PublicationReader) dropFailedAdmission(key string) {
	reader.failureMu.Lock()
	defer reader.failureMu.Unlock()
	if _, found := reader.failures[key]; !found {
		return
	}
	delete(reader.failures, key)
	for index, candidate := range reader.failureOrder {
		if candidate == key {
			reader.failureOrder = append(
				reader.failureOrder[:index], reader.failureOrder[index+1:]...,
			)
			break
		}
	}
}

func sameUnavailableRead(left, right *PublicationRead) bool {
	if left == nil || right == nil ||
		left.Availability == PublicationCurrent ||
		right.Availability == PublicationCurrent {
		return false
	}
	return left.Availability == right.Availability &&
		left.ExpectedGeneration == right.ExpectedGeneration &&
		left.Revision == right.Revision &&
		reflect.DeepEqual(left.Summary, right.Summary)
}

func cloneCallerGenerationPublication(
	publication store.CallerGenerationPublication,
) store.CallerGenerationPublication {
	publication.Pairs = slices.Clone(publication.Pairs)
	return publication
}

func cloneCallerPublicationState(
	state callerpublication.State,
) callerpublication.State {
	state.Generation.Extractors = slices.Clone(state.Generation.Extractors)
	return state
}

func cloneResolverCatalogPublication(
	publication store.ResolverCatalogPublication,
) store.ResolverCatalogPublication {
	publication.Declarations = slices.Clone(publication.Declarations)
	publication.ResolverPacks = slices.Clone(publication.ResolverPacks)
	return publication
}

func deterministicPublicationReadFailure(err error) bool {
	return errors.Is(err, store.ErrInvalidCallerGenerationPublication) ||
		errors.Is(err, store.ErrInvalidCallerLeafState) ||
		errors.Is(err, store.ErrInvalidCandidateManifestPublication) ||
		errors.Is(err, store.ErrInvalidResolverCatalogPublication) ||
		errors.Is(err, callerpublication.ErrInvalidManifest) ||
		errors.Is(err, callerleaf.ErrInvalidArtifact) ||
		errors.Is(err, os.ErrNotExist)
}

func deterministicFilesystemReadFailure(err error) bool {
	return errors.Is(err, callerpublication.ErrInvalidManifest) ||
		errors.Is(err, callerleaf.ErrInvalidArtifact) ||
		errors.Is(err, os.ErrNotExist)
}

func (availability PublicationAvailability) valid() bool {
	switch availability {
	case PublicationMissing, PublicationStale, PublicationFailed,
		PublicationCurrent:
		return true
	default:
		return false
	}
}

func (read *PublicationRead) validate() error {
	if read == nil || !read.Availability.valid() {
		return fmt.Errorf("caller publication read availability is invalid")
	}
	serving := read.Availability == PublicationCurrent
	if serving != (read.lease != nil) {
		return errors.New("caller publication read lease disagrees with availability")
	}
	if serving && (read.Summary == nil || read.State == nil ||
		read.Resolver == nil || read.Revision == 0 ||
		read.Revision != read.Summary.PublicationRevision) {
		return errors.New("caller publication serving read is incomplete")
	}
	if read.Publication != nil {
		if read.Summary == nil || read.Revision == 0 ||
			read.Revision != read.Publication.PublicationRevision {
			return errors.New("caller publication read pointer projection is incomplete")
		}
	}
	return nil
}
