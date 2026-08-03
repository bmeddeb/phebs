package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/bmeddeb/phebs/internal/callerleafid"
	"github.com/bmeddeb/phebs/internal/resolvercatalog"
	"github.com/bmeddeb/phebs/internal/store"
)

func callerGeneration(
	repository, commit string,
	candidate *store.CandidateManifestPublication,
	catalog *store.ResolverCatalogPublication,
) store.CallerGenerationIdentity {
	return store.CallerGenerationIdentity{
		Repository:               repository,
		HeadCommit:               commit,
		UnitDigest:               candidate.UnitDigest,
		DeclarationSetDigest:     catalog.DeclarationSetDigest,
		CandidateManifestDigest:  candidate.ManifestDigest,
		CandidatePolicyDigest:    candidate.PolicyDigest,
		CandidateControlRevision: candidate.ControlRevision,
		ResolverGenerationDigest: catalog.GenerationDigest,
		ResolverManifestDigest:   catalog.ManifestDigest,
		ResolverControlRevision:  catalog.ControlRevision,
		ResolverWriterSchema:     catalog.WriterSchema,
		SourceLanePolicy:         store.CallerBaseSourceLanePolicy,
		CallerPolicyDigest:       candidateDigest('d'),
		ExtractorSetDigest:       candidateDigest('e'),
	}
}

func callerPair(domain, prefix string, ordinal int) store.CallerLeafPair {
	return store.CallerLeafPair{
		Domain:                 domain,
		ExtractorVersion:       "1.0.0",
		LeafAdapterVersion:     "1.0.0",
		LeafOrdinal:            ordinal,
		LeafPrefix:             prefix,
		LeafPrefixBits:         len(prefix),
		CandidateMemberName:    "phebs-candidate-neutral-v4-caller-000001.ndjson",
		CandidateRecordCount:   2,
		CandidateDeclaredBytes: 64,
		CandidateContentBytes:  128,
		CandidateContentDigest: candidateDigest('f'),
	}
}

func callerReceipt(
	generation store.CallerGenerationIdentity,
	pair store.CallerLeafPair,
	contentDigest string,
	results, abstentions int,
) *store.CallerLeafArtifactReceipt {
	pair.PairDigest = store.ComputeCallerLeafPairDigest(generation, pair)
	return &store.CallerLeafArtifactReceipt{
		ArtifactName: callerleafid.ArtifactName(pair.PairDigest, contentDigest), ArtifactCount: 1,
		ResultCount: results, AbstentionCount: abstentions,
		CanonicalBytes: 256, StagingBytes: 256,
		ContentDigest: contentDigest, MetadataDigest: candidateDigest('2'),
	}
}

func TestCallerLeafStoreLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	repository := "github.com/acme/caller-leaf-store"
	commit := candidateCommit('1')
	if err := s.UpsertRepo(ctx, store.Repo{
		Name: repository, CloneURL: "https://" + repository + ".git",
	}); err != nil {
		t.Fatal(err)
	}
	setCandidateIndexedState(t, ctx, s, repository, commit, nil, nil)
	if err := s.PublishCandidateManifest(ctx, candidatePublication(repository, commit, "")); err != nil {
		t.Fatal(err)
	}
	candidate, err := s.GetCandidateManifestPublication(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PublishResolverCatalog(ctx, resolverPublication(repository, commit, candidate.ManifestDigest)); err != nil {
		t.Fatal(err)
	}
	catalog, err := s.GetResolverCatalogPublication(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	generation := callerGeneration(repository, commit, candidate, catalog)
	progress, err := s.GetCallerLeafOutcomeProgress(ctx, generation)
	if err != nil || progress != (store.CallerLeafOutcomeProgress{}) {
		t.Fatalf("empty progress = %+v, %v", progress, err)
	}

	if _, err := s.EnqueuePending(ctx, store.JobCallerLeaf, repository, false); err != nil {
		t.Fatal(err)
	}
	job, err := s.ClaimJob(ctx, store.JobCallerLeaf, "caller-leaf-test")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetJobStatus(ctx, *job, store.StatusRunning, ""); err != nil {
		t.Fatal(err)
	}
	job.Status = store.StatusRunning

	grpc := callerPair("grpc-caller", "00", 0)
	grpcOutcome := store.CallerLeafOutcome{
		Generation: generation, Pair: grpc,
		Disposition: store.CallerLeafSucceeded,
		Receipt:     callerReceipt(generation, grpc, candidateDigest('1'), 1, 1),
	}
	if err := s.RecordCallerLeafOutcome(ctx, *job, grpcOutcome); err != nil {
		t.Fatalf("record success: %v", err)
	}
	// Exact retry is idempotent and cannot duplicate the lossless successor.
	if err := s.RecordCallerLeafOutcome(ctx, *job, grpcOutcome); err != nil {
		t.Fatalf("exact retry: %v", err)
	}
	pending, err := s.ListJobs(ctx, store.JobCallerLeaf, store.StatusPending)
	if err != nil || len(pending) != 1 || pending[0].Target != repository {
		t.Fatalf("caller successor = %+v, %v; want one", pending, err)
	}
	conflict := grpcOutcome
	conflict.Receipt = callerReceipt(generation, grpc, candidateDigest('3'), 1, 1)
	if err := s.RecordCallerLeafOutcome(ctx, *job, conflict); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("conflicting success = %v, want ErrConflict", err)
	}

	thrift := callerPair("thrift-caller", "00", 0)
	terminal := store.CallerLeafOutcome{
		Generation: generation, Pair: thrift,
		Disposition: store.CallerLeafTerminalGenerationRefusal,
	}
	if err := s.RecordCallerLeafOutcome(ctx, *job, terminal); err != nil {
		t.Fatalf("record sibling terminal: %v", err)
	}
	if err := s.RecordCallerLeafOutcome(ctx, *job, terminal); err != nil {
		t.Fatalf("exact terminal retry: %v", err)
	}
	outcomes, err := s.ListCallerLeafOutcomes(ctx, generation)
	if err != nil || len(outcomes) != 2 ||
		outcomes[0].Disposition != store.CallerLeafSucceeded ||
		outcomes[1].Disposition != store.CallerLeafTerminalGenerationRefusal {
		t.Fatalf("outcomes = %+v, %v", outcomes, err)
	}
	progress, err = s.GetCallerLeafOutcomeProgress(ctx, generation)
	if err != nil || progress.SettledCount != 2 ||
		progress.SucceededCount != 1 || progress.RefusedCount != 1 {
		t.Fatalf("settled progress = %+v, %v", progress, err)
	}
	otherGeneration := generation
	otherGeneration.CallerPolicyDigest = candidateDigest('9')
	progress, err = s.GetCallerLeafOutcomeProgress(ctx, otherGeneration)
	if err != nil || progress != (store.CallerLeafOutcomeProgress{}) {
		t.Fatalf("other-generation progress = %+v, %v", progress, err)
	}

	if err := s.SetJobStatus(ctx, *job, store.StatusDone, ""); err != nil {
		t.Fatal(err)
	}
	successor, err := s.ClaimJob(ctx, store.JobCallerLeaf, "caller-admission-test")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetJobStatus(ctx, *successor, store.StatusRunning, ""); err != nil {
		t.Fatal(err)
	}
	successor.Status = store.StatusRunning
	pairs := []store.CallerLeafPair{grpc, thrift}
	if err := s.RecordCallerGenerationAdmission(ctx, *successor,
		store.CallerGenerationAdmission{Generation: generation, Disposition: store.CallerGenerationAdmitted},
		pairs); !errors.Is(err, store.ErrInvalidCallerLeafState) {
		t.Fatalf("admit terminal sibling = %v, want invalid state", err)
	}
	if err := s.RecordCallerGenerationAdmission(ctx, *successor,
		store.CallerGenerationAdmission{
			Generation:    generation,
			Disposition:   store.CallerGenerationTerminalGenerationRefusal,
			PeakOpenFiles: 2,
		}, pairs); err != nil {
		t.Fatalf("terminal admission: %v", err)
	}
	admission, err := s.GetCallerGenerationAdmission(ctx, generation)
	if err != nil || admission.PairCount != 2 || admission.ArtifactCount != 1 ||
		admission.ResultCount != 1 || admission.AbstentionCount != 1 ||
		admission.CanonicalBytes != 256 || admission.StagingBytes != 256 {
		t.Fatalf("admission = %+v, %v", admission, err)
	}

	// Candidate and resolver controls are mutable validation fences, not
	// semantic generation identity. A descriptor repair to the same bytes may
	// reuse the exact artifact/outcome after the caller has revalidated it.
	if err := s.SetJobStatus(ctx, *successor, store.StatusDone, ""); err != nil {
		t.Fatal(err)
	}
	repairedCandidate := *candidate
	repairedCandidate.ControlRevision++
	if err := s.PublishCandidateManifest(ctx, repairedCandidate); err != nil {
		t.Fatalf("advance candidate control: %v", err)
	}
	repairedCatalog := *catalog
	repairedCatalog.ControlRevision = 0
	if err := s.PublishResolverCatalog(ctx, repairedCatalog); err != nil {
		t.Fatalf("republish semantic catalog after control repair: %v", err)
	}
	currentCandidate, err := s.GetCandidateManifestPublication(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	currentCatalog, err := s.GetResolverCatalogPublication(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	repairedGeneration := callerGeneration(
		repository, commit, currentCandidate, currentCatalog,
	)
	if store.ComputeCallerGenerationDigest(repairedGeneration) !=
		store.ComputeCallerGenerationDigest(generation) {
		t.Fatal("control-only repair changed semantic caller generation digest")
	}
	reused, err := s.GetCallerLeafOutcome(ctx, repairedGeneration, grpc)
	if err != nil || reused.Generation.CandidateControlRevision !=
		repairedGeneration.CandidateControlRevision {
		t.Fatalf("control-normalized outcome = %+v, %v", reused, err)
	}
	repairJob, err := s.ClaimJob(ctx, store.JobCallerLeaf, "caller-control-repair-test")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetJobStatus(ctx, *repairJob, store.StatusRunning, ""); err != nil {
		t.Fatal(err)
	}
	repairJob.Status = store.StatusRunning
	reusedOutcome := grpcOutcome
	reusedOutcome.Generation = repairedGeneration
	if err := s.RecordCallerLeafOutcome(ctx, *repairJob, reusedOutcome); err != nil {
		t.Fatalf("record validated control-only reuse: %v", err)
	}
	if err := s.RecordCallerGenerationAdmission(ctx, *repairJob,
		store.CallerGenerationAdmission{
			Generation:    repairedGeneration,
			Disposition:   store.CallerGenerationTerminalGenerationRefusal,
			PeakOpenFiles: 2,
		}, pairs); err != nil {
		t.Fatalf("reuse semantic admission across controls: %v", err)
	}
	reusedAdmission, err := s.GetCallerGenerationAdmission(ctx, repairedGeneration)
	if err != nil || reusedAdmission.Generation.CandidateControlRevision !=
		repairedGeneration.CandidateControlRevision {
		t.Fatalf("control-normalized admission = %+v, %v", reusedAdmission, err)
	}
	if err := s.SetJobStatus(ctx, *repairJob, store.StatusDone, ""); err != nil {
		t.Fatal(err)
	}

	if err := s.ClearCallerLeafOutcome(ctx, repairedGeneration, grpc); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetCallerLeafOutcome(ctx, repairedGeneration, grpc); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cleared outcome = %v, want not found", err)
	}
	if _, err := s.GetCallerGenerationAdmission(ctx, repairedGeneration); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("derived admission after pair clear = %v, want not found", err)
	}
	remaining, err := s.ListCallerLeafOutcomes(ctx, repairedGeneration)
	if err != nil || len(remaining) != 1 || remaining[0].Pair.Domain != "thrift-caller" {
		t.Fatalf("sibling outcome = %+v, %v", remaining, err)
	}
	progress, err = s.GetCallerLeafOutcomeProgress(ctx, repairedGeneration)
	if err != nil || progress.SettledCount != 1 ||
		progress.SucceededCount != 0 || progress.RefusedCount != 1 {
		t.Fatalf("progress after clear = %+v, %v", progress, err)
	}
}

func TestCallerLeafIdentityDigestsIgnoreCachedValues(t *testing.T) {
	generation := store.CallerGenerationIdentity{
		Repository: "github.com/acme/identity", HeadCommit: candidateCommit('1'),
		DeclarationSetDigest:    candidateDigest('1'),
		CandidateManifestDigest: candidateDigest('2'), CandidatePolicyDigest: candidateDigest('3'),
		CandidateControlRevision: 1,
		ResolverGenerationDigest: candidateDigest('4'), ResolverManifestDigest: candidateDigest('5'),
		ResolverControlRevision: 1, ResolverWriterSchema: "phebs-resolver-catalog-store-v1",
		SourceLanePolicy:   store.CallerBaseSourceLanePolicy,
		CallerPolicyDigest: candidateDigest('6'), ExtractorSetDigest: candidateDigest('7'),
	}
	first := store.ComputeCallerGenerationDigest(generation)
	generation.Digest = "caller_generation_v1_forged"
	if second := store.ComputeCallerGenerationDigest(generation); second != first {
		t.Fatalf("cached generation digest affected authority: %q != %q", second, first)
	}
	pair := callerPair("grpc-caller", "00", 0)
	firstPair := store.ComputeCallerLeafPairDigest(generation, pair)
	pair.PairDigest = "caller_leaf_pair_v1_forged"
	if secondPair := store.ComputeCallerLeafPairDigest(generation, pair); secondPair != firstPair {
		t.Fatalf("cached pair digest affected authority: %q != %q", secondPair, firstPair)
	}
}

func TestResolverCatalogPublicationCallerLeafFanout(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	repository := "github.com/acme/caller-leaf-fanout"
	commit := candidateCommit('1')
	if err := s.UpsertRepo(ctx, store.Repo{
		Name: repository, CloneURL: "https://" + repository + ".git",
	}); err != nil {
		t.Fatal(err)
	}
	setCandidateIndexedState(t, ctx, s, repository, commit, nil, nil)
	candidate := candidatePublication(repository, commit, "")
	if err := s.PublishCandidateManifest(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	catalog := resolverPublication(repository, commit, candidate.ManifestDigest)
	if err := s.PublishResolverCatalog(ctx, catalog); err != nil {
		t.Fatal(err)
	}
	assertPendingCallerLeafSuccessor(t, ctx, s, repository, true)
	if canceled, err := s.CancelPendingJobs(ctx, store.JobCallerLeaf, repository); err != nil || canceled != 1 {
		t.Fatalf("cancel forced successor = %d, %v", canceled, err)
	}

	// An exact publication retry repairs the queue gap without claiming force.
	if err := s.PublishResolverCatalog(ctx, catalog); err != nil {
		t.Fatalf("exact retry: %v", err)
	}
	assertPendingCallerLeafSuccessor(t, ctx, s, repository, false)
	if _, err := s.EnqueuePending(ctx, store.JobCallerLeaf, repository, true); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishResolverCatalog(ctx, catalog); err != nil {
		t.Fatalf("exact retry over force: %v", err)
	}
	assertPendingCallerLeafSuccessor(t, ctx, s, repository, true)
	if canceled, err := s.CancelPendingJobs(ctx, store.JobCallerLeaf, repository); err != nil || canceled != 1 {
		t.Fatalf("cancel upgraded successor = %d, %v", canceled, err)
	}

	// A real catalog generation transition forces replacement work.
	identity, err := resolvercatalog.NewIdentity(
		repository, commit, "", candidate.ManifestDigest, nil,
		[]resolvercatalog.ResolverPack{{Name: "neutral", Version: "1.0.0"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	next := catalog
	next.ResolverPacks = []store.ResolverCatalogPack{{Name: "neutral", Version: "1.0.0"}}
	next.ResolverPackSetDigest = identity.ResolverPackSetDigest
	next.CatalogPolicyDigest = identity.CatalogPolicyDigest
	next.GenerationDigest = identity.GenerationDigest
	next.ManifestDigest = candidateDigest('9')
	next.ControlRevision = 0
	if err := s.PublishResolverCatalog(ctx, next); err != nil {
		t.Fatalf("next catalog generation: %v", err)
	}
	assertPendingCallerLeafSuccessor(t, ctx, s, repository, true)
}

func assertPendingCallerLeafSuccessor(
	t *testing.T,
	ctx context.Context,
	s *store.Surreal,
	repository string,
	wantForce bool,
) {
	t.Helper()
	jobs, err := s.ListJobs(ctx, store.JobCallerLeaf, store.StatusPending)
	if err != nil {
		t.Fatal(err)
	}
	var matching []store.Job
	for _, job := range jobs {
		if job.Target == repository {
			matching = append(matching, job)
		}
	}
	if len(matching) != 1 || matching[0].Force != wantForce {
		t.Fatalf("caller leaf successor = %+v, want one force=%v", matching, wantForce)
	}
}
