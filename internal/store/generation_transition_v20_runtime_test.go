package store_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/candidateid"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/extractors/grpcgo"
	"github.com/bmeddeb/phebs/internal/extract/extractors/protodecl"
	"github.com/bmeddeb/phebs/internal/extract/extractors/thriftdecl"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/generationscheduler"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/store"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// Only new test-owned persistent databases and real production extraction,
// scheduler, reaper and exact runtime reader are used. This is not a diagnosis
// of the retained V20 database or native custody/accounting/RSS acceptance.
type v21SDKPointerChangingStore struct {
	*store.Surreal
	prior   store.GenerationSchedule
	calls   int
	changed bool
}

func (state *v21SDKPointerChangingStore) ReadGenerationStaleLeaseTransition(ctx context.Context, request store.GenerationStaleLeaseTransitionRequest) (store.GenerationStaleLeaseTransition, error) {
	state.calls++
	value, err := state.Surreal.ReadGenerationStaleLeaseTransition(ctx, request)
	if state.calls == 1 && err == nil {
		if err := state.RetireCurrentGenerationSchedule(ctx, state.prior); err != nil {
			return store.GenerationStaleLeaseTransition{}, err
		}
		state.changed = true
	}
	return value, err
}

func TestV20CheckpointPersistentMultidomainSelectedSDKReader(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	for _, mode := range []string{"ordinary", "diagnostic", "pointer_change", "immediate_callback"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
			defer cancel()
			fixture := newV21ReaderFixture(t, ctx)
			// The test owns the SA lifetime. Close its real store/owner before
			// the earlier deferred cancel; testing cleanup then observes the same
			// idempotent close result instead of canceling a live owner first.
			defer func() {
				cleanup, cancelCleanup := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancelCleanup()
				if err := fixture.close(cleanup); err != nil {
					t.Error("component SDK lifetime closure refused", err)
				}
			}()

			generation, err := fixture.reconciler.Reconcile(ctx, fixture.repository)
			if err != nil {
				t.Fatal(err)
			}
			initial := fixture.schedule(t, ctx)
			if initial.TotalChunks != 56 || initial.RepositoryTokens != 1 {
				t.Fatal("fixture is not fifty-six genuine multidomain parts with one repository token", initial.TotalChunks)
			}
			if _, err := fixture.state.ExpandGenerationSchedule(ctx, fixture.repository, extractionpublication.ScheduleStage, initial.Generation); err != nil {
				t.Fatal(err)
			}
			for ordinal := range 56 {
				chunk, err := fixture.state.ClaimGenerationChunk(ctx, store.GenerationResourceExtraction, "checkpoint-cold")
				if err != nil || chunk == nil || chunk.Offset != int64(ordinal) {
					t.Fatal("claim cold part", ordinal, err)
				}
				if err := fixture.runtime.Handle(ctx, *chunk); err != nil {
					t.Fatal("execute real cold part", ordinal, err)
				}
				if err := fixture.state.CompleteGenerationChunk(ctx, *chunk); err != nil {
					t.Fatal(err)
				}
			}
			baseline := fixture.publication(t, ctx)
			originalAcquisitions, originalAppends := fixture.source.acquisitions, fixture.evidence.appends
			if baseline.root.ResultCount != 3 || baseline.root.Totals.Facts == 0 || len(baseline.assertions) == 0 {
				t.Fatal("missing real three-part target evidence")
			}
			fixture.reconciler.RecoveryPreparationEnabled = true
			prior := fixture.schedule(t, ctx)
			prefix, err := fixture.state.GetPartitionedExtractionDomain(ctx, fixture.repository, "grpc-consumer")
			if err != nil || prefix == nil {
				t.Fatal("real preceding domain publication unavailable", err)
			}

			suffix, err := fixture.state.GetPartitionedExtractionDomain(ctx, fixture.repository, "thrift-contract")
			if err != nil || suffix == nil {
				t.Fatal("real trailing domain publication unavailable", err)
			}
			prepared, err := fixture.reconciler.PrepareRecovery(ctx, extractionpublication.RecoveryPreparationRequest{
				Schema: extractionpublication.RecoveryPreparationSchema, Authority: fixture.authority,
				GenerationDigest: generation, PriorScheduleDigest: prior.Digest,
				Roots: []extractionpublication.RecoveryPreparationRoot{{Domain: prefix.Domain, PlanDigest: prefix.PlanDigest, RootDigest: prefix.RootDigest}, {Domain: baseline.root.Domain, PlanDigest: baseline.root.PlanDigest, RootDigest: baseline.root.Digest}, {Domain: suffix.Domain, PlanDigest: suffix.PlanDigest, RootDigest: suffix.RootDigest}},
				Mode:  extractionpublication.RecoveryPreparationCheckpoint, TargetDomain: baseline.root.Domain, TargetOrdinal: 2,
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.state.ExpandGenerationSchedule(ctx, fixture.repository, extractionpublication.ScheduleStage, prepared.Generation); err != nil {
				t.Fatal(err)
			}
			cut := errors.New("test-owned ordinal-two checkpoint interruption")
			var checkpoint store.GenerationStaleLeaseTransition
			fixture.runtime.OnPartitionCheckpoint = func(_ context.Context, event store.GenerationStaleLeaseTransition) error {
				if event.Offset != 29 {
					return nil
				}
				checkpoint = event
				return cut
			}
			var selected store.GenerationChunk
			for ordinal := range 56 {
				chunk, err := fixture.state.ClaimGenerationChunk(ctx, store.GenerationResourceExtraction, "checkpoint-predecessor")
				if err != nil || chunk == nil || chunk.Offset != int64(ordinal) {
					t.Fatal("claim checkpoint part", ordinal, err)
				}
				err = fixture.runtime.Handle(ctx, *chunk)
				if ordinal == 29 {
					if !errors.Is(err, cut) || checkpoint.ChunkIdentity != chunk.Identity {
						t.Fatal("actual reused ordinal two was not interrupted", err)
					}
					selected = *chunk
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				if err := fixture.state.CompleteGenerationChunk(ctx, *chunk); err != nil {
					t.Fatal(err)
				}
			}
			request := extractionpublication.CheckpointRestartTransitionRequest{Transition: checkpoint, TargetGeneration: generation,
				PriorScheduleDigest: prior.Digest, Domain: baseline.root.Domain, Ordinal: 2, PlanDigest: baseline.root.PlanDigest,
				ResultIdentity: baseline.root.Results[2].Identity}
			hit, err := fixture.runtime.ReadCheckpointRestartTransition(ctx, request)
			if err != nil || hit.Current || hit.RootExists || hit.CompletionBitSet || !hit.CanonicalResultExists {
				t.Fatal("actual public checkpoint hit unavailable", err)
			}
			if err := fixture.close(ctx); err != nil {
				t.Fatal(err)
			}
			next, closeNext, err := store.V21OpenLocalSDKTest(ctx, fixture.dataDir)
			if err != nil {
				t.Fatal("new persistent engine lifetime", err)
			}
			fixture.state, fixture.evidence.Surreal = next, next
			fixture.close = closeNext
			fixture.runtime.Store, fixture.reconciler.Evidence = next, next
			fence := fixture.runtime.Fence.(extractionpublication.AuthorityFence)
			fence.Store = next
			fixture.runtime.Fence = fence
			fixture.runtime.Publisher = extractionpublication.StorePublisher{Store: next}
			// Preserve the production twenty-second stale lease, including the
			// SDK's whole-second query cutoff. No native deadline is changed.
			timer := time.NewTimer(21*time.Second + 100*time.Millisecond)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				t.Fatal(ctx.Err())
			}
			var mu sync.Mutex
			var attempts []store.GenerationChunk
			var transitions []store.GenerationStaleLeaseTransitionPoint
			var readErr error
			var readCounts readaccounting.Counts
			var recovered extractionpublication.CheckpointRestartTransition
			var recoveredEvent store.GenerationStaleLeaseTransition
			settledReady := make(chan struct{})
			var healthyFailures int
			// Enroll each complete plan/reap/worker turn so shutdown drains
			// real calls before cancellation, matching the production owner contract.
			owners, err := dispatchadmission.NewOwners(ctx, dispatchadmission.OwnerLimits{Owners: 4, Requests: 1})
			if err != nil {
				t.Fatal(err)
			}
			scheduler := &generationscheduler.Scheduler{Store: next, Owners: owners, PollEvery: 10 * time.Millisecond, HeartbeatEvery: time.Second,
				StaleAfter: 20 * time.Second, Backoff: func(int) time.Duration { return 20 * time.Millisecond },
				Classes: map[store.GenerationResourceClass]generationscheduler.Class{store.GenerationResourceExtraction: {
					Concurrency: 2, Budget: generationscheduler.Budget{MaxMemoryBytes: 1, MaxDescriptors: 1},
					Handle: func(ctx context.Context, chunk store.GenerationChunk, _ generationscheduler.Budget) error {
						mu.Lock()
						attempts = append(attempts, chunk)
						mu.Unlock()
						return fixture.runtime.Handle(ctx, chunk)
					},
					OnStaleLeaseTransition: func(ctx context.Context, event store.GenerationStaleLeaseTransition) error {
						mu.Lock()
						transitions = append(transitions, event.Point)
						mu.Unlock()
						if event.Point != store.GenerationStaleLeaseTransitionRecovered {
							return nil
						}
						// The genuine recovered callback occurs in the middle of the
						// schedule. Keep the reader's settled-schedule contract strict:
						// a second scheduler worker drains trailing parts, then the test
						// releases this callback within its unchanged store-call deadline.
						// The immediate case reads directly at the real callback boundary.
						// Ordinary cases keep the earlier external barrier as a separate control.
						if mode != "immediate_callback" {
							select {
							case <-settledReady:
							case <-ctx.Done():
								return ctx.Err()
							}
						}
						accounted, ledger, err := readaccounting.Start(ctx, readaccounting.Counts{ControlFileReads: 7, StoreReadAttempts: 4})
						if err != nil {
							return err
						}
						observed, projection := readaccounting.CaptureRecoverySchedule(accounted)
						if mode != "ordinary" {
							observed = readaccounting.WithTransitionReadFailureObserver(observed, func(readaccounting.TransitionReadSide, readaccounting.TransitionReadStage, error, readaccounting.TransitionReadFailureFlags) {
								mu.Lock()
								healthyFailures++
								mu.Unlock()
							})
						}

						current := request
						current.Transition = event
						value, err := fixture.runtime.ReadCheckpointRestartTransition(observed, current)
						counts, finishErr := ledger.Finish()
						inventory, available := projection.Observation()
						if !available || inventory.Chunks != 56 || inventory.Successes != 56 {
							err = errors.Join(err, errors.New("actual recovery schedule is not complete"))
						}
						mu.Lock()
						readErr, readCounts, recovered = errors.Join(err, finishErr), counts, value
						recoveredEvent = event
						mu.Unlock()
						return errors.Join(err, finishErr)
					},
				}}}
			runCtx, stop := context.WithCancel(ctx)
			joined := make(chan error, 1)
			go func() { joined <- scheduler.Run(runCtx) }()
			var joinOnce sync.Once
			var schedulerErr error
			join := func() error {
				joinOnce.Do(func() {
					drain, cancelDrain := context.WithTimeout(ctx, 5*time.Second)
					drainErr := owners.Pause(drain)
					cancelDrain()
					stop()
					schedulerErr = errors.Join(drainErr, <-joined)
				})
				return schedulerErr
			}
			defer func() {
				if err := join(); err != nil {
					t.Error("scheduler join", err)
				}
			}()
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for {
				current := fixture.schedule(t, ctx)
				if current.Status == store.GenerationScheduleSettled {
					if current.Succeeded != 56 || current.Failed != 0 {
						t.Fatal("recovery did not durably complete", current.Succeeded, current.Failed)
					}
					break
				}
				select {
				case <-ticker.C:
				case <-ctx.Done():
					t.Fatal("bounded scheduler recovery", ctx.Err())
				}
			}
			close(settledReady)
			// Join callbacks before inspecting their saved diagnostics.
			if err := join(); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if !reflect.DeepEqual(baseline, fixture.publication(t, ctx)) {
				t.Fatal("publication/evidence changed across the real restart")
			}
			if fixture.source.acquisitions != originalAcquisitions || fixture.evidence.appends != originalAppends {
				t.Fatal("completed-result recovery reacquired source or appended evidence")
			}
			seen := make(map[int64]bool, 27)
			for _, attempt := range attempts {
				identity, err := store.GenerationChunkIdentity(selected.ScheduleDigest, attempt.Offset, 0)
				wantPriority := store.GenerationPriorityNeverRun
				if attempt.Offset == 29 {
					wantPriority = store.GenerationPriorityStale
				}
				if err != nil || attempt.Offset < 29 || attempt.Offset >= 56 || seen[attempt.Offset] || attempt.Attempt != 0 ||
					attempt.Identity != identity || attempt.Priority != wantPriority {
					t.Fatal("original middle/trailing attempt identity differs")
				}
				seen[attempt.Offset] = true
			}
			if len(attempts) != 27 || len(seen) != 27 || !seen[29] || readErr != nil || recovered.Point != store.GenerationStaleLeaseTransitionRecovered ||
				!recovered.Current || !recovered.RootExists || !recovered.CompletionBitSet || recovered.PrivateLeaseTokenDigest == hit.PrivateLeaseTokenDigest ||
				readCounts != (readaccounting.Counts{ControlFileReads: 7, StoreReadAttempts: 4}) || recovered.RootDigest != baseline.root.Digest {
				t.Fatal("actual ordinal-two R unavailable", len(attempts), readErr, readCounts)
			}
			if healthyFailures != 0 {
				t.Fatal("healthy read emitted a failure or callback discriminator missing")
			}
			if selected.Offset != 29 || request.Ordinal != 2 || selected.Attempt != 0 || recoveredEvent.ChunkIdentity != selected.Identity {
				t.Fatal("multidomain local/global original-attempt binding differs")
			}
			if mode == "pointer_change" {
				// Change only this fresh test-owned current pointer after the genuine
				// before snapshot. The real runtime must refuse its after snapshot.
				changing := &v21SDKPointerChangingStore{Surreal: next, prior: fixture.schedule(t, ctx)}
				fixture.runtime.Store = changing
				request.Transition = recoveredEvent
				accounted, ledger, err := readaccounting.Start(ctx, readaccounting.Counts{ControlFileReads: 7, StoreReadAttempts: 4})
				if err != nil {
					t.Fatal(err)
				}
				var side readaccounting.TransitionReadSide
				var stage readaccounting.TransitionReadStage
				accounted = readaccounting.WithTransitionReadFailureObserver(accounted, func(s readaccounting.TransitionReadSide, p readaccounting.TransitionReadStage, _ error, _ readaccounting.TransitionReadFailureFlags) {
					side, stage = s, p
				})
				value, err := fixture.runtime.ReadCheckpointRestartTransition(accounted, request)
				counts, finish := ledger.Finish()
				if !errors.Is(err, store.ErrNotFound) || finish != nil || value != (extractionpublication.CheckpointRestartTransition{}) ||
					counts != (readaccounting.Counts{ControlFileReads: 7, StoreReadAttempts: 3}) || changing.calls != 2 || !changing.changed || side != readaccounting.TransitionReadAfter || stage != readaccounting.TransitionReadScheduleQuery {
					t.Fatal("pointer change gained credit or accounting differed", errors.Is(err, store.ErrNotFound), counts, changing.calls, changing.changed)
				}
				t.Log("deterministic between-snapshot pointer removal refused: controls=7 store=3; differs from V20 controls=0 store=2")
			}
			t.Logf("real_persistent_multidomain mode=%s attempts=%d transitions=%v R=%s controls=%d store=%d", mode, len(attempts), transitions, recovered.Point, readCounts.ControlFileReads, readCounts.StoreReadAttempts)
		})
	}
}

type v21ReaderFixture struct {
	repository string
	dataDir    string
	state      *store.Surreal
	close      func(context.Context) error
	runtime    *extractionpublication.Runtime
	reconciler *extractionpublication.Reconciler
	authority  extractionpublication.PlanningAuthority
	commit     string
	source     *v21SDKFixtureSource
	evidence   *v21SDKFixtureEvidence
}

func newV21ReaderFixture(t *testing.T, ctx context.Context) *v21ReaderFixture {
	t.Helper()
	const parts, prefixParts, suffixParts = 3, 27, 26
	if parts < 1 || parts > 3 {
		t.Fatal("bounded fixture requires one to three parts")
	}
	const repository = "example.invalid/recovery-preparation"
	dataDir := t.TempDir()
	state, closeStore, err := store.V21OpenLocalSDKTest(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &v21ReaderFixture{repository: repository, state: state, dataDir: dataDir, close: closeStore}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := fixture.close(cleanup); err != nil {
			t.Errorf("close recovery preparation store: %v", err)
		}
	})
	repositoryDir, err := phebssync.SafeRepoDir(dataDir, repository)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repositoryDir, 0o700); err != nil {
		t.Fatal(err)
	}
	v21SDKFixtureGit(t, ctx, repositoryDir, "init", "-q")
	paths := make([]string, 0, parts)
	for index := range parts {
		path := "orders.proto"
		if parts != 1 {
			path = fmt.Sprintf("orders%d.proto", index)
		}
		namespace := "neutral"
		if parts != 1 {
			namespace = fmt.Sprintf("neutral%d", index)
		}
		proto := fmt.Sprintf("syntax = \"proto3\";\npackage %s;\nmessage Request {}\nmessage Response {}\nservice Orders { rpc Get(Request) returns (Response); }\n", namespace)
		if err := os.WriteFile(filepath.Join(repositoryDir, path), []byte(proto), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	if prefixParts != 0 && prefixParts != 27 {
		t.Fatal("bounded fixture prefix differs")
	}
	for index := range prefixParts {
		path := fmt.Sprintf("prefix%02d.go", index)
		if err := os.WriteFile(filepath.Join(repositoryDir, path), []byte("package neutral\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	if suffixParts != 0 && (suffixParts != 26 || prefixParts != 27) {
		t.Fatal("bounded fixture suffix differs")
	}
	for index := range suffixParts {
		path := fmt.Sprintf("suffix%02d.thrift", index)
		if err := os.WriteFile(filepath.Join(repositoryDir, path), []byte(fmt.Sprintf("namespace go neutral%d\nstruct Request { 1: string value }\n", index)), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	v21SDKFixtureGit(t, ctx, repositoryDir, append([]string{"add", "--"}, paths...)...)
	v21SDKFixtureGit(t, ctx, repositoryDir, "-c", "user.name=Neutral", "-c", "user.email=neutral@example.invalid", "commit", "-q", "-m", "neutral recovery fixture")
	commit := strings.TrimSpace(v21SDKFixtureGit(t, ctx, repositoryDir, "rev-parse", "HEAD"))
	extractor := protodecl.New()
	policies := []candidate.Policy{{
		Domain: extractor.Domain(), Version: extractor.Version(), Plane: candidate.PlaneRepository,
		EnumerationPolicy: "proto-contract-paths-v1", Enumerate: extractor.Candidate, Required: extractor.Candidate,
	}}
	if parts > 1 {
		policies[0].Plane = candidate.PlaneLocal
		policies[0].ExecutionPartitionPolicy = candidate.WholeRepositoryExecutionSubrangePolicy
		policies[0].ExecutionMaxRecords = 1
	}
	extractors := []extract.Extractor{extractor}
	if prefixParts != 0 {
		prefix := grpcgo.New()
		extractors = append(extractors, prefix)
		policies = append(policies, candidate.Policy{Domain: prefix.Domain(), Version: prefix.Version(),
			Plane: candidate.PlaneLocal, EnumerationPolicy: "grpc-consumer-paths-v1", Enumerate: prefix.Candidate, Required: prefix.Candidate,
			ExecutionPartitionPolicy: candidate.WholeRepositoryExecutionSubrangePolicy, ExecutionMaxRecords: 1})
	}
	if suffixParts != 0 {
		suffix := thriftdecl.New()
		extractors = append(extractors, suffix)
		policies = append(policies, candidate.Policy{Domain: suffix.Domain(), Version: suffix.Version(), Plane: candidate.PlaneLocal,
			EnumerationPolicy: "thrift-contract-paths-v1", Enumerate: suffix.Candidate, Required: suffix.Candidate,
			ExecutionPartitionPolicy: candidate.WholeRepositoryExecutionSubrangePolicy, ExecutionMaxRecords: 1})
	}
	identities, err := candidate.PolicyIdentities(policies)
	if err != nil {
		t.Fatal(err)
	}
	candidateRoot, candidateStage := filepath.Join(dataDir, "candidates"), filepath.Join(dataDir, "candidate-stage")
	for _, directory := range []string{candidateRoot, candidateStage} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	manifest, err := candidate.Build(ctx, candidate.Request{
		RepoDir: repositoryDir, OutputDir: candidateStage, Repository: repository, Commit: commit, Policies: policies,
	})
	if err != nil {
		t.Fatal(err)
	}
	expected := candidate.Expected{
		Repository: repository, Commit: commit, Policies: identities, PolicyDigest: manifest.PolicyDigest,
		GenerationDigest: manifest.GenerationDigest, ManifestDigest: manifest.Digest,
	}
	selected, err := candidate.PublishContext(ctx, candidateRoot, candidateStage, expected)
	if err != nil {
		t.Fatal(err)
	}
	if err := candidate.FinishPublication(candidateRoot, repository); err != nil {
		t.Fatal(err)
	}
	if err := state.UpsertRepo(ctx, store.Repo{Name: repository, CloneURL: "file://" + repositoryDir}); err != nil {
		t.Fatal(err)
	}
	if err := state.SetRepoIndexed(ctx, repository, commit, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := state.PublishCandidateManifest(ctx, store.CandidateManifestPublication{
		Repository: repository, HeadCommit: commit, ManifestPath: candidateid.ManifestName(repository),
		PolicyDigest: selected.PolicyDigest, ManifestDigest: selected.ManifestDigest, GenerationDigest: selected.GenerationDigest,
	}); err != nil {
		t.Fatal(err)
	}
	fixture.commit = commit
	fixture.authority = extractionpublication.PlanningAuthority{
		Repository: repository, CandidateManifestDigest: selected.ManifestDigest,
		CandidateGenerationDigest: selected.GenerationDigest, CandidatePolicyDigest: selected.PolicyDigest,
		SourceGenerationDigest:      v21SDKFixtureDigest("selected test source"),
		ObservationGenerationDigest: v21SDKFixtureDigest("selected test observation"),
	}
	fixture.evidence = &v21SDKFixtureEvidence{Surreal: state}
	fixture.runtime = &extractionpublication.Runtime{
		Root: filepath.Join(dataDir, "extraction-publications"), Store: state,
		Executor:  &extract.EvidencePartitionExecutor{Evidence: fixture.evidence, Extractors: extractors},
		Publisher: extractionpublication.StorePublisher{Store: state},
	}
	readAuthority := func(context.Context, candidate.State) (string, string, error) {
		return fixture.authority.SourceGenerationDigest, fixture.authority.ObservationGenerationDigest, nil
	}
	readCandidate := func(ctx context.Context, repository string) (candidate.State, error) {
		current, err := fixture.state.GetCandidateManifestPublication(ctx, repository)
		if err != nil {
			return candidate.State{}, err
		}
		if current == nil {
			return candidate.State{}, store.ErrNotFound
		}
		value := selected
		value.Repository, value.Commit, value.UnitDigest = current.Repository, current.HeadCommit, current.UnitDigest
		value.PolicyDigest, value.ManifestDigest = current.PolicyDigest, current.ManifestDigest
		value.GenerationDigest, value.Manifest = current.GenerationDigest, current.ManifestPath
		return value, nil
	}
	fixture.reconciler = &extractionpublication.Reconciler{
		Root: fixture.runtime.Root, CandidateRoot: candidateRoot, Runtime: fixture.runtime, Evidence: state,
		OpenCandidate: func(ctx context.Context, _ string) (*candidate.Publication, error) {
			return candidate.OpenContext(ctx, candidateRoot, expected)
		},
		CandidateReference: readCandidate,
		Authority:          readAuthority, AuthorityReference: readAuthority,
	}
	fixture.source = &v21SDKFixtureSource{Source: extractionpublication.GitSparseSource{
		DataDir: dataDir, OpenDomain: fixture.reconciler.OpenDomain,
	}}
	fixture.runtime.Source = fixture.source
	fixture.runtime.Fence = extractionpublication.AuthorityFence{
		Store: state,
		Acquire: func(ctx context.Context) (func(), error) {
			return focusedindex.AcquireExclusiveMutationLock(ctx, filepath.Join(dataDir, "index"))
		},
		Current: func(ctx context.Context, plan candidate.DomainResultPlan) error {
			current, err := readCandidate(ctx, plan.Repository)
			if err != nil {
				return err
			}
			if current.Repository != repository || current.Commit != commit || current.UnitDigest != selected.UnitDigest ||
				current.ManifestDigest != plan.CandidateManifestDigest || current.GenerationDigest != plan.CandidateGenerationDigest ||
				current.PolicyDigest != plan.CandidatePolicyDigest || plan.SourceGenerationDigest != fixture.authority.SourceGenerationDigest ||
				plan.ObservationGenerationDigest != fixture.authority.ObservationGenerationDigest {
				return extractionpublication.ErrStale
			}
			return nil
		},
	}
	return fixture
}

func (fixture *v21ReaderFixture) schedule(t *testing.T, ctx context.Context) store.GenerationSchedule {
	t.Helper()
	value, err := fixture.state.GetGenerationSchedule(ctx, fixture.repository, extractionpublication.ScheduleStage)
	if err != nil || value == nil || store.ValidateGenerationSchedule(*value) != nil {
		t.Fatalf("read actual schedule: %+v, %v", value, err)
	}
	return *value
}

type v21SDKFixturePublication struct {
	stored     store.PartitionedExtractionDomain
	root       candidate.DomainResultRoot
	assertions []store.Assertion
	atoms      map[string]store.EvidenceResolution
}

func (fixture *v21ReaderFixture) publication(t *testing.T, ctx context.Context) v21SDKFixturePublication {
	t.Helper()
	stored, err := fixture.state.GetPartitionedExtractionDomain(ctx, fixture.repository, "proto-contract")
	if err != nil || stored == nil {
		t.Fatalf("read current store root: %v", err)
	}
	plan, err := candidate.DecodeDomainResultPlanControl(strings.NewReader(stored.Plan))
	if err != nil {
		t.Fatal(err)
	}
	root, err := candidate.DecodeDomainResultRoot(strings.NewReader(stored.Root), plan)
	if err != nil {
		t.Fatal(err)
	}
	authority := store.PartitionedAssertionAuthority{
		Repository: fixture.repository, Domain: stored.Domain, RunID: stored.RunID,
		PlanDigest: stored.PlanDigest, RootDigest: stored.RootDigest, Commit: fixture.commit,
		CandidateManifestDigest: plan.CandidateManifestDigest, CandidatePolicyDigest: plan.CandidatePolicyDigest,
	}
	assertions, err := fixture.state.ListPartitionedAssertions(ctx, store.AssertionQuery{
		Repo: fixture.repository, RunID: stored.RunID, Limit: 128,
	}, authority)
	if err != nil {
		t.Fatal(err)
	}
	atoms := make(map[string]store.EvidenceResolution)
	for _, assertion := range assertions {
		for _, refs := range [][]string{assertion.Supporting, assertion.Contradicting} {
			for _, atomID := range refs {
				if _, present := atoms[atomID]; present {
					continue
				}
				if len(atoms) >= 128 {
					t.Fatal("small evidence fixture exceeded its atom bound")
				}
				atom, err := fixture.state.ResolvePartitionedEvidence(ctx, authority, atomID)
				if err != nil || atom == nil {
					t.Fatalf("resolve original native evidence: %v", err)
				}
				atoms[atomID] = *atom
			}
		}
	}
	return v21SDKFixturePublication{stored: *stored, root: root, assertions: assertions, atoms: atoms}
}

type v21SDKFixtureSource struct {
	extractionpublication.Source
	acquisitions int
}

func (source *v21SDKFixtureSource) AcquirePartition(ctx context.Context, plan candidate.DomainResultPlan, ordinal int) (extractionpublication.PartitionLease, error) {
	source.acquisitions++
	return source.Source.AcquirePartition(ctx, plan, ordinal)
}

type v21SDKFixtureEvidence struct {
	*store.Surreal
	appends int
}

func (evidence *v21SDKFixtureEvidence) AddEvidenceChunk(ctx context.Context, runID, chunkID string, factCount int, atoms []store.EvidenceAtom, associations []store.SnapshotEvidence, assertions []store.Assertion) error {
	evidence.appends++
	return evidence.Surreal.AddEvidenceChunk(ctx, runID, chunkID, factCount, atoms, associations, assertions)
}

func v21SDKFixtureGit(t *testing.T, ctx context.Context, directory string, args ...string) string {
	t.Helper()
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("neutral Git fixture: %v: %s", err, output)
	}
	return string(output)
}

func v21SDKFixtureDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}
