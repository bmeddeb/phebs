//go:build darwin

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/auth"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/candidateid"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/sourcepartition"
	"github.com/bmeddeb/phebs/internal/store"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
	"github.com/bmeddeb/phebs/internal/t421extractionprojection"
)

// Actual reduced Git/candidate/source/observation, 9-domain/56-partition
// execution and settled schedule precede the real selected preparation POST.
// Prior F routing/auth and the unused lifecycle owners are supplied; every
// authority/root/target/schedule used by preparation is derived from actual
// native publications. No full F, stale lease, reaping or whole-phase pass.
func TestT422WorkspacePreparationNativeComposition(t *testing.T) {
	if os.Getenv("PHEBS_T422_NATIVE_PREPARATION") != "1" {
		t.Skip("opt-in real 31,605-file preparation fixture")
	}
	testT422ArchiveRetiredNativeEndpointFailure(t, false, true, false, "workspace-preparation")
}

func seedT422PreparationNative(t *testing.T, ctx context.Context, data string, st *store.Surreal, launch *t422SemanticLaunch) (*extractionpublication.Reconciler, t422StaleFinal, store.GenerationSchedule) {
	t.Helper()
	repository, commit := launch.request.Repository, launch.request.ReturnSourceCommit
	repoDir, err := phebssync.SafeRepoDir(data, repository)
	if err != nil {
		t.Fatal(err)
	}
	extractors := evidenceExtractors(true, true, false, true)
	policies, err := extract.CandidatePolicies(extractors)
	if err != nil {
		t.Fatal(err)
	}
	identities, err := candidate.PolicyIdentities(policies)
	if err != nil {
		t.Fatal(err)
	}
	candidateRoot, candidateStage := filepath.Join(data, "candidates"), filepath.Join(data, "candidate-stage")
	if err := os.Mkdir(candidateRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(candidateStage, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest, err := candidate.Build(ctx, candidate.Request{RepoDir: repoDir, OutputDir: candidateStage,
		Repository: repository, Commit: commit, Policies: policies})
	if err != nil {
		t.Fatal("actual candidate build", err)
	}
	expected := candidate.Expected{Repository: repository, Commit: commit, Policies: identities,
		PolicyDigest: manifest.PolicyDigest, GenerationDigest: manifest.GenerationDigest, ManifestDigest: manifest.Digest}
	selected, err := candidate.PublishContext(ctx, candidateRoot, candidateStage, expected)
	if err != nil {
		t.Fatal(err)
	}
	if err := candidate.FinishPublication(candidateRoot, repository); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertRepo(ctx, store.Repo{Name: repository, CloneURL: "file://" + repoDir}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetRepoIndexed(ctx, repository, commit, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := st.PublishCandidateManifest(ctx, store.CandidateManifestPublication{Repository: repository,
		HeadCommit: commit, ManifestPath: candidateid.ManifestName(repository), PolicyDigest: selected.PolicyDigest,
		ManifestDigest: selected.ManifestDigest, GenerationDigest: selected.GenerationDigest}); err != nil {
		t.Fatal(err)
	}

	// Build the real source and observation publications instead of the
	// integration fixture's supplied source/observation digest strings.
	indexRoot, observationRoot := filepath.Join(data, "index"), filepath.Join(data, "observations")
	source, err := repositoryindex.BuildSourceGeneration(ctx, repoDir, indexRoot, repository,
		[]store.IndexedRevision{{Selector: "HEAD", Branch: "HEAD", Commit: commit}})
	if err != nil {
		t.Fatal(err)
	}
	transition, err := observationpublication.BeginInventoryPublicationV2(observationRoot, repository)
	if err != nil {
		t.Fatal(err)
	}
	super, err := sourcepartition.BuildSuperRoot(ctx, sourcepartition.BuildRequest{SourceDirectory: indexRoot,
		OutputDirectory: transition.SourceDirectory, Repository: repository, Source: source,
		Policy: sourcepartition.Policy{Schema: sourcepartition.PolicySchema, Name: "go-source", Version: "1.0.0", IncludeSuffixes: []string{".go"}}})
	if err != nil {
		t.Fatal(err)
	}
	sourcePlan, err := sourcepartition.OpenSuperRoot(ctx, transition.SourceDirectory, super)
	if err != nil {
		t.Fatal(err)
	}
	var metrics observationpublication.InventoryMetricsV2
	if _, err := observationpublication.BuildInventoryStageV2(ctx, observationpublication.InventoryBuildRequestV2{
		OutputDirectory: transition.InventoryDirectory, RepositoryDirectory: repoDir, Plan: sourcePlan, Metrics: &metrics}); err != nil {
		t.Fatal(err)
	}
	if _, err := observationpublication.CompleteInventoryPublicationV2(ctx, observationRoot, repository, transition.TransitionID, nil); err != nil {
		t.Fatal(err)
	}

	readCandidate := func(operation context.Context, name string) (candidate.State, error) {
		publication, err := st.GetCandidateManifestPublication(operation, name)
		if err != nil {
			return candidate.State{}, err
		}
		if publication == nil {
			return candidate.State{}, store.ErrNotFound
		}
		value := selected
		value.Repository, value.Commit, value.UnitDigest = publication.Repository, publication.HeadCommit, publication.UnitDigest
		value.PolicyDigest, value.ManifestDigest = publication.PolicyDigest, publication.ManifestDigest
		value.GenerationDigest, value.Manifest = publication.GenerationDigest, publication.ManifestPath
		return value, nil
	}
	readAuthority := func(operation context.Context, state candidate.State) (string, string, error) {
		return partitionFenceAuthority(operation, observationRoot, indexRoot, state)
	}
	native := &extractionpublication.Runtime{Root: filepath.Join(data, "extraction-publications"), Store: st,
		Executor:  &extract.EvidencePartitionExecutor{Evidence: st, Extractors: extractors, StoreAccounting: true},
		Publisher: extractionpublication.StorePublisher{Store: st}}
	reconciler := &extractionpublication.Reconciler{Root: native.Root, CandidateRoot: candidateRoot,
		Runtime: native, Evidence: st, StoreAccounting: true, CandidateReference: readCandidate,
		Authority: readAuthority, AuthorityReference: readAuthority,
		OpenCandidate: func(operation context.Context, _ string) (*candidate.Publication, error) {
			return candidate.OpenContext(operation, candidateRoot, expected)
		}}
	native.Source = extractionpublication.GitSparseSource{DataDir: data, OpenDomain: reconciler.OpenDomain}
	native.Fence = extractionpublication.AuthorityFence{Store: st,
		Acquire: func(operation context.Context) (func(), error) {
			return focusedindex.AcquireExclusiveMutationLock(operation, indexRoot)
		},
		Current: func(operation context.Context, plan candidate.DomainResultPlan) error {
			state, err := readCandidate(operation, repository)
			if err != nil {
				return err
			}
			source, observation, err := readAuthority(operation, state)
			policy, policyErr := candidate.ExtractionPolicyDigest(state.PolicyDigest, true)
			if err != nil {
				return err
			}
			if policyErr != nil {
				return policyErr
			}
			if state.Repository != plan.Repository || state.Commit != commit ||
				state.ManifestDigest != plan.CandidateManifestDigest || state.GenerationDigest != plan.CandidateGenerationDigest ||
				state.PolicyDigest != plan.CandidatePolicyDigest || source != plan.SourceGenerationDigest ||
				observation != plan.ObservationGenerationDigest || policy != plan.ExtractionPolicyDigest {
				return extractionpublication.ErrStale
			}
			return nil
		}}
	generation, err := reconciler.Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := st.GetGenerationSchedule(ctx, repository, extractionpublication.ScheduleStage)
	if err != nil || schedule == nil || schedule.TotalItems != 56 {
		t.Fatal("actual 56-partition schedule", schedule, err)
	}
	if _, err := st.ExpandGenerationSchedule(ctx, repository, extractionpublication.ScheduleStage, schedule.Generation); err != nil {
		t.Fatal(err)
	}
	var completed uint64
	for completed < 56 {
		chunk, err := st.ClaimGenerationChunk(ctx, store.GenerationResourceExtraction, "native-preparation-fixture")
		if err != nil || chunk == nil || chunk.ScheduleDigest != schedule.Digest || chunk.Length != 1 || chunk.Attempt != 0 {
			t.Fatal("actual first-attempt partition claim", chunk, err)
		}
		if err := native.Handle(ctx, *chunk); err != nil {
			t.Fatal(err)
		}
		if err := st.CompleteGenerationChunk(ctx, *chunk); err != nil {
			t.Fatal(err)
		}
		completed++
	}
	settled, err := st.GetGenerationSchedule(ctx, repository, extractionpublication.ScheduleStage)
	if err != nil || settled == nil || settled.Digest != schedule.Digest || settled.Status != store.GenerationScheduleSettled ||
		settled.Succeeded != 56 || settled.Pending != 0 || settled.Running != 0 || settled.Failed != 0 {
		t.Fatal("actual settled partition schedule", settled, err)
	}
	sourceDigest, observationDigest, err := readAuthority(ctx, selected)
	if err != nil {
		t.Fatal(err)
	}
	// This is a supplied F protocol envelope, NOT the full native F reader.
	// Its entire preparation-relevant authority is read from actual publications.
	response := t421FinalAuthorityResponse{Schema: t421FinalAuthoritySchema,
		Authority: t421FinalAuthorityState{Current: true, PhysicalCommit: commit,
			CandidateGenerationSHA256: selected.GenerationDigest, SourceGenerationSHA256: sourceDigest,
			ObservationGenerationSHA256: observationDigest}}
	domains := make([]string, 0, len(policies))
	for _, policy := range policies {
		domains = append(domains, policy.Domain)
	}
	slices.Sort(domains)
	for _, domain := range domains {
		actual, err := extractionpublication.CurrentSnapshot(ctx, st, repository, domain)
		if err != nil {
			t.Fatal(err)
		}
		if actual.Plan.CandidateGenerationDigest != selected.GenerationDigest || actual.Plan.SourceGenerationDigest != sourceDigest ||
			actual.Plan.ObservationGenerationDigest != observationDigest || len(actual.Root.Results) != len(actual.Plan.Expected) {
			t.Fatal("actual root authority mismatch", domain)
		}
		if domain == "grpc-caller" {
			sparse, err := reconciler.OpenDomain(ctx, actual.Plan)
			if err != nil {
				t.Fatal(err)
			}
			partitions := sparse.Partitions()
			if len(partitions) <= 6 || partitions[6].Kind != candidate.PartitionKindCandidateMember ||
				partitions[6].SourceStart != 16126 || partitions[6].SourceEnd != 18830 {
				t.Fatal("actual frozen grpc-caller ordinal six", partitions)
			}
		}
		response.ExtractionRoots = append(response.ExtractionRoots, t421extractionprojection.RootResult{
			Domain: domain, Current: true, GenerationSHA256: generation, PlanSHA256: actual.Plan.Digest, RootSHA256: actual.Root.Digest,
			CandidateGenerationSHA256: selected.GenerationDigest, SourceGenerationSHA256: sourceDigest,
			ObservationGenerationSHA256: observationDigest, ApplicablePartitions: uint64(len(actual.Plan.Expected))})
	}
	prior, err := t422StaleFinalSnapshot(selected, response)
	if err != nil {
		t.Fatal("actual preparation inventory", err)
	}
	t.Logf("actual preparation setup: partitions=%d observation_read_blobs=%d parsed_blobs=%d", completed, metrics.ReadBlobs, metrics.ParsedBlobs)
	return reconciler, prior, *settled
}

type t422PreparationNativeWriter struct {
	*httptest.ResponseRecorder
	before func()
}

func (writer *t422PreparationNativeWriter) Write(raw []byte) (int, error) {
	writer.before()
	return writer.ResponseRecorder.Write(raw)
}

func runT422PreparationNative(t *testing.T, ctx context.Context, root string, st *store.Surreal,
	launch *t422SemanticLaunch, workspace *t422LifecycleControl, owners *dispatchadmission.Owners, authService *auth.Service) {
	t.Helper()
	// Real builders require the same selected native read/publication observers
	// as serve. No synthetic successful events or unmetered bypass.
	var err error
	ctx, err = bindT422SourceReports(ctx, launch.fail)
	if err != nil {
		t.Fatal("actual preparation source reports", err)
	}
	ctx, err = bindT422ObservationReports(ctx, launch.fail)
	if err != nil {
		t.Fatal("actual preparation observation reports", err)
	}
	reconciler, prior, settled := seedT422PreparationNative(t, ctx, filepath.Join(root, "data"), st, launch)
	stale, err := newT422StaleControl(ctx, launch, reconciler)
	if err != nil {
		t.Fatal(err)
	}
	defer stale.cancel()
	stale.workspacePreparation = workspace.sampleRecoveryPreparation
	// Only prior F routing is modeled; never assign target, schedule, counters,
	// point ordinals or native transition events. Preparation obtains these itself.
	stale.final, stale.captured, stale.confirmed = prior, true, true
	var sinkObserved, beforeBody bool
	stale.sink = func(body []byte) error {
		var actual t422StalePreparationObservation
		if json.Unmarshal(body, &actual) != nil || actual.Workspace == nil ||
			actual.Workspace.LogicalBytes == 0 || actual.Workspace.AllocatedBytes == 0 ||
			actual.PriorSchedule != settled.Digest || actual.RecoverySchedule == settled.Digest ||
			actual.TargetGeneration != prior.generation || actual.Domain != "grpc-caller" || actual.Ordinal != 6 ||
			actual.PlanDigest == "" || actual.ResultIdentity == "" || actual.ControlFileReads == 0 ||
			actual.StoreReadAttempts == 0 || actual.StoreWriteAttempts == 0 {
			return errT422StaleControl
		}
		stale.mu.Lock()
		beforeArm := stale.preparing && !stale.armed
		stale.mu.Unlock()
		snapshot := workspace.workspaceByteSnapshot()
		if !beforeArm || snapshot.Unavailable || !snapshot.Phases[6].Completed {
			return errT422StaleControl
		}
		sinkObserved = true
		return nil
	}
	readState := t421NewExactReadAccountingState(func([]byte) error { return errT422StaleControl }, launch.fail)
	readState.semantic, readState.lifecycle, readState.stale = launch, workspace, stale
	handler := t422OwnerHTTPHandler(owners, authService.Require(readState.wrap(http.NotFoundHandler())), launch)
	runtime, err := store.ReadLocalRuntime(filepath.Join(root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("endpoint=" + runtime.Endpoint)
	input := bufio.NewScanner(os.Stdin)
	request := func(path, point string) *httptest.ResponseRecorder {
		t.Helper()
		if !input.Scan() {
			t.Fatal("actual request token", input.Err())
		}
		value := httptest.NewRequest(http.MethodPost, path, nil).WithContext(ctx)
		value.Header.Set("Authorization", "Bearer "+t421ExactReadTestCredential)
		value.Header.Set(dispatchadmission.ProductionRequestHeader, input.Text())
		if point != "" {
			value.Header.Set(t422WorkspacePointHeader, point)
		}
		response := httptest.NewRecorder()
		if path == t422StalePreparePath {
			writer := &t422PreparationNativeWriter{ResponseRecorder: response, before: func() {
				workspace.mu.Lock()
				point := workspace.workspacePoint
				workspace.mu.Unlock()
				stale.mu.Lock()
				ready := stale.preparing && !stale.armed
				stale.mu.Unlock()
				observed := workspace.workspaceByteSnapshot()
				if beforeBody || !ready || point != 3 || observed.Unavailable || !observed.Phases[6].Completed {
					t.Fatal("actual completed internal walk must precede first body write and arming", observed, point)
				}
				beforeBody = true
			}}
			handler.ServeHTTP(writer, value)
		} else {
			handler.ServeHTTP(response, value)
		}
		if response.Code != http.StatusOK {
			t.Fatal("actual preparation fixture request", path, response.Code, response.Body.String())
		}
		return response
	}
	for _, point := range []string{"finish", "start"} {
		response := request(t422WorkspaceSamplePath, point)
		var sample t422WorkspaceSampleResponse
		if json.Unmarshal(response.Body.Bytes(), &sample) != nil || sample.LogicalBytes == 0 || sample.AllocatedBytes == 0 {
			t.Fatal("actual preparation boundary walk", response.Body.String())
		}
		fmt.Println("preparation_" + point + "_measured")
	}
	response := request(t422StalePreparePath, "")
	var actual t422StalePreparationObservation
	if json.Unmarshal(response.Body.Bytes(), &actual) != nil || actual.Workspace == nil || !sinkObserved || !beforeBody {
		t.Fatal("actual preparation body/sink", response.Body.String())
	}
	stale.mu.Lock()
	target, armed, preparing := stale.target, stale.armed, stale.preparing
	stale.mu.Unlock()
	if !armed || preparing || target.PriorScheduleDigest != settled.Digest || target.Schedule.Digest != actual.RecoverySchedule ||
		target.ResultIdentity != actual.ResultIdentity || target.Domain != "grpc-caller" || target.Ordinal != 6 {
		t.Fatal("actual prepared target was not armed", target)
	}
	if _, err := st.ListRepos(ctx); err != nil {
		t.Fatal("real SDK did not resume", err)
	}
	fmt.Printf("preparation_measured_armed:%d:%d:%d\n", actual.ControlFileReads, actual.StoreReadAttempts, actual.StoreWriteAttempts)
	if !input.Scan() || strings.TrimSpace(input.Text()) != "close" {
		t.Fatal("joined preparation close", input.Err())
	}
}
