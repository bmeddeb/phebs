package callerexecute

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/callerpublication"
	"github.com/bmeddeb/phebs/internal/callerpublicationid"
	"github.com/bmeddeb/phebs/internal/store"
)

type publicationReaderTestStore struct {
	*workerTestStore
	mu              sync.Mutex
	authorityValues []bool
	authorityErr    error
	authorityCalls  int
	jointValues     []bool
	jointErr        error
	jointCalls      int
	exactValues     []bool
	exactErr        error
	exactCalls      int
	fullReads       int
	summaryReads    int
	resolverReads   int
}

func (state *publicationReaderTestStore) GetCallerGenerationPublication(
	ctx context.Context,
	repository string,
) (*store.CallerGenerationPublication, error) {
	state.mu.Lock()
	state.fullReads++
	state.mu.Unlock()
	return state.workerTestStore.GetCallerGenerationPublication(ctx, repository)
}

func (state *publicationReaderTestStore) GetCallerGenerationPublicationSummary(
	ctx context.Context,
	repository string,
) (*store.CallerGenerationPublicationSummary, error) {
	state.mu.Lock()
	state.summaryReads++
	state.mu.Unlock()
	return state.workerTestStore.GetCallerGenerationPublicationSummary(
		ctx, repository,
	)
}

func (state *publicationReaderTestStore) GetResolverCatalogPublication(
	ctx context.Context,
	repository string,
) (*store.ResolverCatalogPublication, error) {
	state.mu.Lock()
	state.resolverReads++
	state.mu.Unlock()
	return state.workerTestStore.GetResolverCatalogPublication(ctx, repository)
}

func (state *publicationReaderTestStore) CallerGenerationPublicationSummaryCurrent(
	ctx context.Context,
	publication store.CallerGenerationPublicationSummary,
) (bool, error) {
	state.mu.Lock()
	state.exactCalls++
	calls := state.exactCalls
	err := state.exactErr
	values := state.exactValues
	state.mu.Unlock()
	if err != nil {
		return false, err
	}
	if len(values) > 0 {
		index := min(calls-1, len(values)-1)
		return values[index], nil
	}
	return state.workerTestStore.CallerGenerationPublicationSummaryCurrent(
		ctx, publication,
	)
}

func (state *publicationReaderTestStore) CallerGenerationPublicationSummaryAuthorityCurrent(
	ctx context.Context,
	publication store.CallerGenerationPublicationSummary,
) (bool, error) {
	state.mu.Lock()
	state.authorityCalls++
	calls := state.authorityCalls
	err := state.authorityErr
	values := state.authorityValues
	state.mu.Unlock()
	if err != nil {
		return false, err
	}
	if len(values) > 0 {
		index := min(calls-1, len(values)-1)
		return values[index], nil
	}
	return state.workerTestStore.CallerGenerationPublicationSummaryAuthorityCurrent(
		ctx, publication,
	)
}

func (state *publicationReaderTestStore) CallerGenerationPublicationSummariesAuthorityCurrent(
	ctx context.Context,
	publications []store.CallerGenerationPublicationSummary,
) (bool, error) {
	state.mu.Lock()
	state.jointCalls++
	calls := state.jointCalls
	err := state.jointErr
	values := state.jointValues
	state.mu.Unlock()
	if err != nil {
		return false, err
	}
	if len(values) > 0 {
		index := min(calls-1, len(values)-1)
		return values[index], nil
	}
	return state.workerTestStore.
		CallerGenerationPublicationSummariesAuthorityCurrent(ctx, publications)
}

func newHarnessPublicationReader(
	t *testing.T,
	harness workerHarness,
	state PublicationReadStore,
) *PublicationReader {
	t.Helper()
	reader, err := NewPublicationReader(
		state, harness.worker.registry, harness.worker.publications,
	)
	if err != nil {
		t.Fatal(err)
	}
	return reader
}

func TestPublicationReaderReturnsExactCurrentLeaseAndFinalFence(t *testing.T) {
	harness := newWorkerHarness(t, 1)
	harness.settle(t)
	reader := newHarnessPublicationReader(t, harness, harness.state)

	read, err := reader.Open(t.Context(), harness.state.repo.Name)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = read.Release() }()
	if err := read.validate(); err != nil {
		t.Fatal(err)
	}
	if read.Availability != PublicationCurrent || read.Lease() == nil ||
		read.Publication == nil || read.Summary == nil || read.Resolver == nil ||
		read.State == nil ||
		read.Revision == 0 || read.Revision != read.Publication.PublicationRevision ||
		read.Summary.PublicationRevision != read.Revision ||
		read.Publication.Generation != read.ExpectedGeneration ||
		read.Lease().State().Generation.Digest != read.ExpectedGeneration.Digest {
		t.Fatalf("current publication read = %+v", read)
	}
	if current, err := reader.Current(t.Context(), read); err != nil || !current {
		t.Fatalf("result-time current = %v, %v", current, err)
	}
	if _, err := read.Binding(); err != nil {
		t.Fatalf("current binding: %v", err)
	}

	// The pointer and its lease remain immutable, but a control transition
	// invalidates result visibility before serialization.
	harness.state.candidate.ControlRevision++
	if current, err := reader.Current(t.Context(), read); err != nil || current {
		t.Fatalf("transitioned result-time current = %v, %v", current, err)
	}
	if err := read.Release(); err != nil {
		t.Fatal(err)
	}
	if err := read.Release(); err != nil {
		t.Fatalf("second release = %v", err)
	}
}

func TestPublicationReaderCurrentPairUsesOneJointFence(t *testing.T) {
	harness := newWorkerHarness(t, 1)
	harness.settle(t)
	state := &publicationReaderTestStore{workerTestStore: harness.state}
	reader := newHarnessPublicationReader(t, harness, state)
	read, err := reader.Open(t.Context(), harness.state.repo.Name)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = read.Release() }()

	current, err := reader.CurrentPair(t.Context(), read, read)
	if err != nil || !current || state.jointCalls != 1 {
		t.Fatalf("joint current = %t, calls=%d, err=%v", current, state.jointCalls, err)
	}

	state.jointValues = []bool{false}
	current, err = reader.CurrentPair(t.Context(), read, read)
	if err != nil || current || state.jointCalls != 2 {
		t.Fatalf("joint transition = %t, calls=%d, err=%v", current, state.jointCalls, err)
	}

	wantedErr := errors.New("joint authority unavailable")
	state.jointErr = wantedErr
	current, err = reader.CurrentPair(t.Context(), read, read)
	if current || !errors.Is(err, wantedErr) || state.jointCalls != 3 {
		t.Fatalf("joint failure = %t, calls=%d, err=%v", current, state.jointCalls, err)
	}

	badState := cloneCallerPublicationState(*read.State)
	badState.ManifestDigest = "sha256:" + strings.Repeat("9", 64)
	invalid := &PublicationRead{
		Availability: PublicationCurrent, Summary: read.Summary,
		State: &badState, lease: read.lease,
	}
	current, err = reader.CurrentPair(t.Context(), read, invalid)
	if err != nil || current || state.jointCalls != 3 {
		t.Fatalf("invalid pair = %t, calls=%d, err=%v", current, state.jointCalls, err)
	}
}

func TestPublicationReaderReopensBoundWarmPageWithoutPairPayloadRead(t *testing.T) {
	harness := newWorkerHarness(t, 1)
	harness.state.resolver.SourceLanePolicy = "candidate-source-lane-base-v1"
	harness.state.resolver.Declarations = []store.ResolverCatalogDeclarationPublication{{
		Domain: "proto-contract", RunID: "run-declarations",
		GenerationDigest: harness.state.resolver.GenerationDigest,
	}}
	harness.settle(t)

	coldReader := newHarnessPublicationReader(t, harness, harness.state)
	cold, err := coldReader.Open(t.Context(), harness.state.repo.Name)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := cold.Binding()
	if err != nil {
		t.Fatal(err)
	}
	if err := cold.Release(); err != nil {
		t.Fatal(err)
	}

	state := &publicationReaderTestStore{workerTestStore: harness.state}
	warmReader := newHarnessPublicationReader(t, harness, state)
	warm, err := warmReader.Reopen(t.Context(), bound)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = warm.Release() }()
	if err := warm.validate(); err != nil {
		t.Fatal(err)
	}
	if warm.Availability != PublicationCurrent || warm.Publication != nil ||
		warm.Lease() == nil || warm.State == nil || warm.Summary == nil ||
		warm.Resolver == nil || len(warm.Resolver.Declarations) != 1 ||
		warm.Resolver.Declarations[0].RunID != "run-declarations" {
		t.Fatalf("warm read = %+v", warm)
	}
	if state.fullReads != 0 || state.exactCalls != 0 ||
		state.authorityCalls != 2 || state.resolverReads != 1 {
		t.Fatalf(
			"warm costs: full=%d exact=%d authority=%d resolver=%d",
			state.fullReads, state.exactCalls, state.authorityCalls,
			state.resolverReads,
		)
	}
	if current, err := warmReader.Current(t.Context(), warm); err != nil || !current {
		t.Fatalf("warm result-time current = %v, %v", current, err)
	}
	if state.fullReads != 0 || state.exactCalls != 0 || state.authorityCalls != 3 {
		t.Fatalf(
			"warm final costs: full=%d exact=%d authority=%d",
			state.fullReads, state.exactCalls, state.authorityCalls,
		)
	}
}

func TestPublicationReaderReopensCompactDescriptorWithoutPairPayloadRead(
	t *testing.T,
) {
	harness := newWorkerHarness(t, 1)
	harness.state.resolver.SourceLanePolicy = "candidate-source-lane-base-v1"
	harness.state.resolver.Declarations = []store.ResolverCatalogDeclarationPublication{{
		Domain: "proto-contract", RunID: "run-compact-declarations",
		GenerationDigest: harness.state.resolver.GenerationDigest,
	}}
	harness.settle(t)
	harness.state.publication.PublicationIncarnation = "sha256:" +
		strings.Repeat("1", 64)

	coldReader := newHarnessPublicationReader(t, harness, harness.state)
	cold, err := coldReader.Open(t.Context(), harness.state.repo.Name)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := cold.Descriptor()
	if err != nil {
		t.Fatal(err)
	}
	if err := descriptor.Validate(); err != nil {
		t.Fatalf("validate descriptor: %v", err)
	}
	changedSummary := *cold.Summary
	changedSummary.PublishedAt = changedSummary.PublishedAt.Add(time.Nanosecond)
	changedDescriptor, err := publicationDescriptorFromSummary(changedSummary)
	if err != nil {
		t.Fatal(err)
	}
	if changedDescriptor.Repository != descriptor.Repository ||
		changedDescriptor.GenerationDigest != descriptor.GenerationDigest ||
		changedDescriptor.ManifestDigest != descriptor.ManifestDigest ||
		changedDescriptor.PairSetDigest != descriptor.PairSetDigest ||
		changedDescriptor.PublicationRevision != descriptor.PublicationRevision ||
		changedDescriptor.PublicationIncarnation != descriptor.PublicationIncarnation ||
		changedDescriptor.SummaryDigest == descriptor.SummaryDigest {
		t.Fatalf(
			"full-summary commitment did not isolate scalar change: before=%+v after=%+v",
			descriptor, changedDescriptor,
		)
	}
	if err := cold.Release(); err != nil {
		t.Fatal(err)
	}
	for _, observation := range []struct {
		availability PublicationAvailability
	}{
		{availability: PublicationStale},
		{availability: PublicationFailed},
	} {
		observed := &PublicationRead{
			Availability: observation.availability, Summary: cold.Summary,
			Revision: cold.Revision,
		}
		observedDescriptor, err := observed.Descriptor()
		if err != nil || observedDescriptor != descriptor {
			t.Fatalf(
				"%s observed descriptor = %+v, want %+v, err=%v",
				observation.availability, observedDescriptor, descriptor, err,
			)
		}
	}
	inconsistent := &PublicationRead{
		Availability: PublicationFailed, Summary: cold.Summary,
		Revision: cold.Revision + 1,
	}
	if _, err := inconsistent.Descriptor(); err == nil {
		t.Fatal("inconsistent observed revision minted a descriptor")
	}

	state := &publicationReaderTestStore{workerTestStore: harness.state}
	warmReader := newHarnessPublicationReader(t, harness, state)
	warm, err := warmReader.ReopenDescriptor(t.Context(), descriptor)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = warm.Release() }()
	if err := warm.validate(); err != nil {
		t.Fatal(err)
	}
	if warm.Availability != PublicationCurrent || warm.Publication != nil ||
		warm.Lease() == nil || warm.State == nil || warm.Summary == nil ||
		warm.Resolver == nil || len(warm.Resolver.Declarations) != 1 ||
		warm.Resolver.Declarations[0].RunID != "run-compact-declarations" {
		t.Fatalf("compact warm read = %+v", warm)
	}
	if state.fullReads != 0 || state.summaryReads != 1 ||
		state.exactCalls != 0 || state.authorityCalls != 2 ||
		state.resolverReads != 1 {
		t.Fatalf(
			"compact warm costs: full=%d summary=%d exact=%d authority=%d resolver=%d",
			state.fullReads, state.summaryReads, state.exactCalls,
			state.authorityCalls, state.resolverReads,
		)
	}
	reopenedDescriptor, err := warm.Descriptor()
	if err != nil || reopenedDescriptor != descriptor {
		t.Fatalf(
			"reopened descriptor = %+v, want %+v, err=%v",
			reopenedDescriptor, descriptor, err,
		)
	}
}

func TestPublicationReaderCompactDescriptorRejectsABAIncarnation(t *testing.T) {
	harness := newWorkerHarness(t, 1)
	harness.state.resolver.SourceLanePolicy = "candidate-source-lane-base-v1"
	harness.settle(t)
	harness.state.publication.PublicationIncarnation = "sha256:" +
		strings.Repeat("1", 64)

	reader := newHarnessPublicationReader(t, harness, harness.state)
	first, err := reader.Open(t.Context(), harness.state.repo.Name)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := first.Descriptor()
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}

	// Model A -> B -> A with the same repository-local revision and exact
	// generation bytes. Only the store-owned incarnation distinguishes the
	// recreated A publication from the authority named by the old descriptor.
	original := *harness.state.publication
	transitioned := original
	transitioned.ManifestDigest = "sha256:" + strings.Repeat("9", 64)
	transitioned.PublicationRevision++
	harness.state.publication = &transitioned
	recreated := original
	recreated.PublicationIncarnation = "sha256:" + strings.Repeat("2", 64)
	harness.state.publication = &recreated

	state := &publicationReaderTestStore{workerTestStore: harness.state}
	reopened, err := newHarnessPublicationReader(t, harness, state).
		ReopenDescriptor(t.Context(), descriptor)
	if err != nil || reopened == nil ||
		reopened.Availability != PublicationStale || reopened.Lease() != nil {
		t.Fatalf("A-B-A compact reopen = %+v, %v", reopened, err)
	}
	if state.fullReads != 0 || state.summaryReads != 1 ||
		state.authorityCalls != 0 || state.resolverReads != 0 {
		t.Fatalf(
			"A-B-A compact costs: full=%d summary=%d authority=%d resolver=%d",
			state.fullReads, state.summaryReads, state.authorityCalls,
			state.resolverReads,
		)
	}
}

func TestPublicationDescriptorValidatesIndependently(t *testing.T) {
	digest := func(fill string) string {
		return "sha256:" + strings.Repeat(fill, 64)
	}
	valid := PublicationDescriptor{
		Schema: publicationDescriptorSchema, Repository: "github.com/acme/repo",
		GenerationDigest: digest("1"), ManifestDigest: digest("2"),
		PairSetDigest: digest("3"), PublicationRevision: 1,
		PublicationIncarnation: digest("4"), SummaryDigest: digest("5"),
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid descriptor: %v", err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*PublicationDescriptor)
	}{
		{name: "schema", mutate: func(value *PublicationDescriptor) { value.Schema = "v0" }},
		{name: "repository", mutate: func(value *PublicationDescriptor) { value.Repository = "" }},
		{name: "generation", mutate: func(value *PublicationDescriptor) { value.GenerationDigest = "sha256:short" }},
		{name: "manifest", mutate: func(value *PublicationDescriptor) { value.ManifestDigest = "sha256:short" }},
		{name: "pair set", mutate: func(value *PublicationDescriptor) { value.PairSetDigest = "sha256:short" }},
		{name: "revision", mutate: func(value *PublicationDescriptor) { value.PublicationRevision = 0 }},
		{name: "incarnation", mutate: func(value *PublicationDescriptor) { value.PublicationIncarnation = "sha256:short" }},
		{name: "summary", mutate: func(value *PublicationDescriptor) { value.SummaryDigest = "sha256:short" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.mutate(&candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatalf("invalid descriptor accepted: %+v", candidate)
			}
		})
	}
}

func TestPublicationReaderBoundReopenRejectsTransitionAndInvalidState(t *testing.T) {
	harness := newWorkerHarness(t, 1)
	harness.state.resolver.SourceLanePolicy = "candidate-source-lane-base-v1"
	harness.settle(t)
	coldReader := newHarnessPublicationReader(t, harness, harness.state)
	cold, err := coldReader.Open(t.Context(), harness.state.repo.Name)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := cold.Binding()
	if err != nil {
		t.Fatal(err)
	}
	if err := cold.Release(); err != nil {
		t.Fatal(err)
	}

	t.Run("transition after lease", func(t *testing.T) {
		state := &publicationReaderTestStore{
			workerTestStore: harness.state,
			authorityValues: []bool{true, false},
		}
		reader := newHarnessPublicationReader(t, harness, state)
		read, err := reader.Reopen(t.Context(), bound)
		if err != nil || read.Availability != PublicationStale ||
			read.Lease() != nil || state.fullReads != 0 || state.exactCalls != 0 ||
			state.authorityCalls != 2 {
			t.Fatalf(
				"transition read = %+v full=%d exact=%d authority=%d err=%v",
				read, state.fullReads, state.exactCalls, state.authorityCalls, err,
			)
		}
	})

	t.Run("invalid bound state", func(t *testing.T) {
		invalid := bound
		invalid.State.ManifestDigest = "sha256:" +
			"9999999999999999999999999999999999999999999999999999999999999999"
		state := &publicationReaderTestStore{workerTestStore: harness.state}
		reader := newHarnessPublicationReader(t, harness, state)
		read, err := reader.Reopen(t.Context(), invalid)
		if err != nil || read.Availability != PublicationFailed ||
			read.Lease() != nil || state.fullReads != 0 ||
			state.authorityCalls != 0 || state.resolverReads != 0 {
			t.Fatalf("invalid read = %+v, state=%+v, err=%v", read, state, err)
		}
	})
}

func TestPublicationReaderClassifiesMissingFailedAndStale(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		harness := newWorkerHarness(t, 1)
		reader := newHarnessPublicationReader(t, harness, harness.state)
		read, err := reader.Open(t.Context(), harness.state.repo.Name)
		if err != nil || read.Availability != PublicationMissing ||
			read.Lease() != nil || read.ExpectedGeneration.Repository == "" {
			t.Fatalf("missing read = %+v, %v", read, err)
		}
	})

	t.Run("terminal expected generation", func(t *testing.T) {
		harness := newWorkerHarness(t, 1)
		current, err := harness.worker.currentAuthority(
			t.Context(), harness.state.repo.Name,
		)
		if err != nil || current == nil {
			t.Fatalf("current authority = %+v, %v", current, err)
		}
		harness.state.admission = &store.CallerGenerationAdmission{
			Generation:  current.stored,
			Disposition: store.CallerGenerationTerminalGenerationRefusal,
			RecordedAt:  time.Now().UTC(),
		}
		reader := newHarnessPublicationReader(t, harness, harness.state)
		read, err := reader.Open(t.Context(), harness.state.repo.Name)
		if err != nil || read.Availability != PublicationFailed ||
			read.Lease() != nil {
			t.Fatalf("failed read = %+v, %v", read, err)
		}
	})

	t.Run("stale pointer", func(t *testing.T) {
		harness := newWorkerHarness(t, 1)
		harness.settle(t)
		harness.state.candidate.ControlRevision++
		reader := newHarnessPublicationReader(t, harness, harness.state)
		read, err := reader.Open(t.Context(), harness.state.repo.Name)
		if err != nil || read.Availability != PublicationStale ||
			read.Publication == nil || read.Revision == 0 || read.Lease() != nil {
			t.Fatalf("stale read = %+v, %v", read, err)
		}
	})

	t.Run("deterministically invalid pointer", func(t *testing.T) {
		harness := newWorkerHarness(t, 1)
		harness.settle(t)
		state := &publicationReaderTestStore{
			workerTestStore: harness.state,
			exactErr:        store.ErrInvalidCallerGenerationPublication,
		}
		reader := newHarnessPublicationReader(t, harness, state)
		read, err := reader.Open(t.Context(), harness.state.repo.Name)
		if err != nil || read.Availability != PublicationFailed || read.Lease() != nil {
			t.Fatalf("invalid read = %+v, %v", read, err)
		}
	})
}

func TestPublicationReaderNeverReturnsLeaseAcrossTransition(t *testing.T) {
	harness := newWorkerHarness(t, 1)
	harness.settle(t)
	state := &publicationReaderTestStore{
		workerTestStore: harness.state,
		authorityValues: []bool{true},
		exactValues:     []bool{false},
	}
	reader := newHarnessPublicationReader(t, harness, state)
	read, err := reader.Open(t.Context(), harness.state.repo.Name)
	if err != nil || read.Availability != PublicationStale || read.Lease() != nil ||
		state.authorityCalls != 1 || state.exactCalls != 1 {
		t.Fatalf(
			"transition read = %+v, authority=%d exact=%d err=%v",
			read, state.authorityCalls, state.exactCalls, err,
		)
	}
}

func TestPublicationReaderPreservesOperationalFailures(t *testing.T) {
	harness := newWorkerHarness(t, 1)
	harness.settle(t)
	want := errors.New("database unavailable")
	state := &publicationReaderTestStore{
		workerTestStore: harness.state,
		authorityErr:    want,
	}
	reader := newHarnessPublicationReader(t, harness, state)
	if read, err := reader.Open(
		t.Context(), harness.state.repo.Name,
	); !errors.Is(err, want) || read != nil {
		t.Fatalf("operational read = %+v, %v", read, err)
	}
}

func TestPublicationReaderNegativeCachesColdFilesystemFailure(t *testing.T) {
	harness := newWorkerHarness(t, 1)
	harness.settle(t)
	if harness.state.publication == nil || len(harness.state.publication.Pairs) != 1 {
		t.Fatalf("published fixture = %+v", harness.state.publication)
	}
	artifactPath := filepath.Join(
		harness.worker.root,
		callerpublicationid.RepositoryDirectory(harness.state.repo.Name),
		harness.state.publication.Pairs[0].Receipt.ArtifactName,
	)
	if err := os.WriteFile(artifactPath, []byte("invalid\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	publications := callerpublication.NewRegistry(harness.worker.root)
	reader, err := NewPublicationReader(
		harness.state, harness.worker.registry, publications,
	)
	if err != nil {
		t.Fatal(err)
	}
	const requests = 8
	admitted := make(chan struct{}, requests)
	releaseLeader := make(chan struct{})
	var releaseOnce sync.Once
	releaseAll := func() { releaseOnce.Do(func() { close(releaseLeader) }) }
	t.Cleanup(releaseAll)
	reader.testAfterAdmission = func(bool) { admitted <- struct{}{} }
	var coldAcquires atomic.Int32
	reader.testBeforeAcquire = func() {
		coldAcquires.Add(1)
		<-releaseLeader
	}
	type openResult struct {
		read *PublicationRead
		err  error
	}
	results := make(chan openResult, requests)
	start := make(chan struct{})
	for range requests {
		go func() {
			<-start
			read, openErr := reader.Open(t.Context(), harness.state.repo.Name)
			results <- openResult{read: read, err: openErr}
		}()
	}
	close(start)
	for range requests {
		select {
		case <-admitted:
		case <-time.After(time.Second):
			t.Fatal("concurrent readers did not join the exact admission flight")
		}
	}
	releaseAll()
	for index := range requests {
		result := <-results
		if result.err != nil || result.read == nil ||
			result.read.Availability != PublicationFailed ||
			!result.read.scalarFailureFence || result.read.Lease() != nil {
			t.Fatalf("cold failure %d = %+v, %v", index, result.read, result.err)
		}
	}
	if coldAcquires.Load() != 1 {
		t.Fatalf("concurrent cold acquisitions = %d, want 1", coldAcquires.Load())
	}
	reader.testAfterAdmission = nil
	reader.testBeforeAcquire = nil
	// Closing the registry makes any second cold acquisition fail. The exact
	// failed-state cache must answer before that boundary, and the result fence
	// must remain a scalar authority check rather than a third cold admission.
	if err := publications.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := reader.Open(t.Context(), harness.state.repo.Name)
	if err != nil || second.Availability != PublicationFailed ||
		!second.scalarFailureFence || second.Lease() != nil {
		t.Fatalf("negative-cached cold failure = %+v, %v", second, err)
	}
	if current, err := reader.UnavailableCurrent(
		t.Context(), second,
	); err != nil || !current {
		t.Fatalf("scalar failed-gap fence = %v, %v", current, err)
	}
}

func TestPublicationReaderBoundsDistinctAdmissionFlights(t *testing.T) {
	reader := &PublicationReader{
		admissions: make(map[string]*publicationAdmissionFlight),
	}
	finishes := make([]func(), 0, publicationAdmissionFlightLimit)
	for index := range publicationAdmissionFlightLimit {
		leader, pending, finish, err := reader.beginAdmission(
			fmt.Sprintf("generation-%d", index),
		)
		if err != nil || !leader || pending != nil || finish == nil {
			t.Fatalf(
				"admission %d = leader:%t pending:%v finish:%t err:%v",
				index, leader, pending, finish != nil, err,
			)
		}
		finishes = append(finishes, finish)
	}
	if _, _, _, err := reader.beginAdmission("overflow"); !errors.Is(
		err, callerleaf.ErrCapacity,
	) {
		t.Fatalf("overflow admission = %v, want ErrCapacity", err)
	}
	for _, finish := range finishes {
		finish()
	}
}

func TestPublicationReaderRechecksFailureAfterRegisteringNewFlight(t *testing.T) {
	harness := newWorkerHarness(t, 1)
	harness.settle(t)
	if harness.state.publication == nil {
		t.Fatal("caller publication fixture is missing")
	}
	summary := callerPublicationSummary(*harness.state.publication)
	publicationState, err := callerPublicationStateFromStore(
		summary, harness.worker.registry.ExtractorIdentities(),
	)
	if err != nil {
		t.Fatal(err)
	}
	publications := callerpublication.NewRegistry(harness.worker.root)
	t.Cleanup(func() { _ = publications.Close() })
	reader, err := NewPublicationReader(
		harness.state, harness.worker.registry, publications,
	)
	if err != nil {
		t.Fatal(err)
	}
	key := publicationFailureKey(summary, publicationState)
	reader.testBeforeAdmission = func() {
		reader.retainFailedAdmission(key)
		reader.testBeforeAdmission = nil
	}
	var acquisitions atomic.Int32
	reader.testBeforeAcquire = func() { acquisitions.Add(1) }
	read, err := reader.Open(t.Context(), harness.state.repo.Name)
	if err != nil || read == nil || read.Availability != PublicationFailed ||
		!read.scalarFailureFence || read.Lease() != nil {
		t.Fatalf("post-lookup cached failure = %+v, %v", read, err)
	}
	if acquisitions.Load() != 0 {
		t.Fatalf("cold acquisitions after cached failure = %d", acquisitions.Load())
	}
}

func TestPublicationFailureKeyBindsPublicationIncarnationWithinOneSecond(t *testing.T) {
	summary := store.CallerGenerationPublicationSummary{
		Generation: store.CallerGenerationIdentity{
			Repository: "github.com/acme/aba",
		},
		PublicationRevision:    1,
		PublicationIncarnation: "sha256:" + strings.Repeat("1", 64),
		PublishedAt:            time.Unix(1_700_000_000, 0).UTC(),
	}
	state := callerpublication.State{
		Generation:     callerleaf.GenerationIdentity{Digest: "generation"},
		ManifestDigest: "manifest", PairSetDigest: "pairs",
	}
	first := publicationFailureKey(summary, state)
	summary.PublicationIncarnation = "sha256:" + strings.Repeat("2", 64)
	if second := publicationFailureKey(summary, state); second == first {
		t.Fatal("publication failure key aliases same-second delete/recreate authority")
	}
}

func TestPublicationReaderConstructorRefusesIncompleteConfiguration(t *testing.T) {
	harness := newWorkerHarness(t, 1)
	for _, test := range []struct {
		name         string
		state        PublicationReadStore
		adapters     *Registry
		publications *callerpublication.Registry
	}{
		{name: "store", adapters: harness.worker.registry, publications: harness.worker.publications},
		{name: "adapters", state: harness.state, publications: harness.worker.publications},
		{name: "publications", state: harness.state, adapters: harness.worker.registry},
	} {
		t.Run(test.name, func(t *testing.T) {
			if reader, err := NewPublicationReader(
				test.state, test.adapters, test.publications,
			); err == nil || reader != nil {
				t.Fatalf("reader = %+v, %v", reader, err)
			}
		})
	}
}
