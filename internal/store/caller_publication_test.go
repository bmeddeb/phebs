package store_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/callerpublicationid"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/downstreamauthority"
	"github.com/bmeddeb/phebs/internal/downstreamauthority/authorityvalidate"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/resolvercatalog"
	"github.com/bmeddeb/phebs/internal/store"
)

type callerPublicationFixture struct {
	repository  string
	commit      string
	generation  store.CallerGenerationIdentity
	job         *store.Job
	publication store.CallerGenerationPublication
}

func newCallerPublicationFixture(
	t *testing.T,
	s *store.Surreal,
	repository string,
) callerPublicationFixture {
	t.Helper()
	ctx := context.Background()
	commit := candidateCommit('1')
	if err := s.UpsertRepo(ctx, store.Repo{
		Name: repository, CloneURL: "https://" + repository + ".git",
	}); err != nil {
		t.Fatal(err)
	}
	setCandidateIndexedState(t, ctx, s, repository, commit, nil, nil)
	if err := s.PublishCandidateManifest(
		ctx, candidatePublication(repository, commit, ""),
	); err != nil {
		t.Fatal(err)
	}
	candidate, err := s.GetCandidateManifestPublication(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PublishResolverCatalog(
		ctx, resolverPublication(repository, commit, candidate.ManifestDigest),
	); err != nil {
		t.Fatal(err)
	}
	catalog, err := s.GetResolverCatalogPublication(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	return finishCallerPublicationFixture(t, s, repository, commit, candidate, catalog)
}

func finishCallerPublicationFixture(
	t *testing.T,
	s *store.Surreal,
	repository,
	commit string,
	candidate *store.CandidateManifestPublication,
	catalog *store.ResolverCatalogPublication,
) callerPublicationFixture {
	return finishCallerPublicationFixtureWithUpstream(
		t, s, repository, commit, candidate, catalog, "", "",
	)
}

func finishCallerPublicationFixtureWithUpstream(
	t *testing.T,
	s *store.Surreal,
	repository,
	commit string,
	candidate *store.CandidateManifestPublication,
	catalog *store.ResolverCatalogPublication,
	upstream,
	upstreamDigest string,
) callerPublicationFixture {
	t.Helper()
	ctx := context.Background()
	generation := callerGeneration(repository, commit, candidate, catalog)
	generation.UpstreamDigest = upstreamDigest
	generation.Digest = store.ComputeCallerGenerationDigest(generation)
	if _, err := s.EnqueuePending(ctx, store.JobCallerLeaf, repository, false); err != nil {
		t.Fatal(err)
	}
	job, err := s.ClaimJob(ctx, store.JobCallerLeaf, "caller-publication-test")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetJobStatus(ctx, *job, store.StatusRunning, ""); err != nil {
		t.Fatal(err)
	}
	job.Status = store.StatusRunning
	grpc := callerPair("grpc-caller", "00", 0)
	thrift := callerPair("thrift-caller", "00", 0)
	pairs := []store.CallerLeafPair{grpc, thrift}
	outcomes := []store.CallerLeafOutcome{
		{
			Generation: generation, Pair: grpc,
			Disposition: store.CallerLeafSucceeded,
			Receipt:     callerReceipt(generation, grpc, candidateDigest('1'), 2, 1),
		},
		{
			Generation: generation, Pair: thrift,
			Disposition: store.CallerLeafSucceeded,
			Receipt:     callerReceipt(generation, thrift, candidateDigest('2'), 3, 2),
		},
	}
	for _, outcome := range outcomes {
		if err := s.RecordCallerLeafOutcome(ctx, *job, outcome); err != nil {
			t.Fatalf("record outcome: %v", err)
		}
	}
	if err := s.RecordCallerGenerationAdmission(
		ctx, *job, store.CallerGenerationAdmission{
			Generation: generation, Disposition: store.CallerGenerationAdmitted,
			PeakOpenFiles: 5,
		}, pairs,
	); err != nil {
		t.Fatalf("record admission: %v", err)
	}
	manifestDigest := candidateDigest('9')
	publication := store.CallerGenerationPublication{
		Generation: generation, Upstream: upstream,
		Pairs: []store.CallerGenerationPairPublication{
			{Pair: grpc, Receipt: *outcomes[0].Receipt},
			{Pair: thrift, Receipt: *outcomes[1].Receipt},
		},
		ManifestDigest: manifestDigest,
		ManifestPath: callerpublicationid.ManifestName(
			store.ComputeCallerGenerationDigest(generation), manifestDigest,
		),
	}
	return callerPublicationFixture{
		repository: repository, commit: commit, generation: generation,
		job: job, publication: publication,
	}
}

func callerPublicationV2Upstream(
	t *testing.T,
	repository,
	candidateManifestDigest,
	candidatePolicyDigest string,
	maximum bool,
) (string, string) {
	t.Helper()
	digest := func(value string) string { return "sha256:" + strings.Repeat(value, 64) }
	observation := observationpublication.DownstreamAuthority{
		Version: observationpublication.DownstreamAuthorityV2, Repository: repository,
		SourceGenerationDigest: digest("1"), SourceRootDigest: digest("2"),
		ObservationGenerationDigest: digest("3"), ObservationRootDigest: digest("4"),
		PartitionPolicyDigest: digest("5"), ObservationPolicyDigest: digest("6"),
		InventoryPolicyDigest: digest("7"), RecordCount: 1, ObservedCount: 1,
	}
	domainCount := 1
	if maximum {
		domainCount = 64
	}
	required := make([]downstreamauthority.DomainIdentity, domainCount)
	domains := make([]candidate.DownstreamDomainAuthority, domainCount)
	for index := range domains {
		domain, version, runID := "grpc-caller", "1.0.0", "run-v2"
		if maximum {
			domain = fmt.Sprintf("%03d%s", index, strings.Repeat("d", 125))
			version = strings.Repeat("v", 128)
			runID = strings.Repeat("r", 512)
		}
		required[index] = downstreamauthority.DomainIdentity{Domain: domain, Version: version}
		domains[index] = candidate.DownstreamDomainAuthority{
			Domain: domain, Version: version, PlanDigest: digest("8"),
			RootDigest: digest("9"), RunID: runID, Disposition: candidate.PartitionResultEmpty,
			CandidateManifestDigest: candidateManifestDigest, CandidatePartitionRootDigest: digest("b"),
			CandidatePolicyDigest: candidatePolicyDigest, SourceGenerationDigest: observation.SourceGenerationDigest,
			ObservationGenerationDigest: observation.ObservationGenerationDigest,
			ExtractionPolicyDigest:      digest("d"), DomainIndexDigest: digest("e"),
			DomainScheduleDigest: digest("f"),
		}
	}
	value, err := downstreamauthority.BuildRequired(observation, required, domains)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw), value.Digest
}

func callerPublicationV1Upstream(
	t *testing.T,
	repository,
	candidateManifestDigest,
	candidatePolicyDigest string,
) (string, string) {
	t.Helper()
	raw, _ := callerPublicationV2Upstream(
		t, repository, candidateManifestDigest, candidatePolicyDigest, false,
	)
	var value downstreamauthority.Authority
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		t.Fatal(err)
	}
	value.Schema = authorityvalidate.SchemaV1
	value.ProvenanceDigest = ""
	value.Digest = ""
	identity, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(append([]byte(value.Schema+"\x00"), identity...))
	value.Digest = "sha256:" + hex.EncodeToString(sum[:])
	sealed, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(sealed), value.Digest
}

func newCallerPublicationFixtureWithDeclaration(
	t *testing.T,
	s *store.Surreal,
	repository,
	domain string,
) callerPublicationFixture {
	t.Helper()
	ctx := context.Background()
	commit := candidateCommit('6')
	if err := s.UpsertRepo(ctx, store.Repo{
		Name: repository, CloneURL: "https://" + repository + ".git",
	}); err != nil {
		t.Fatal(err)
	}
	setCandidateIndexedState(t, ctx, s, repository, commit, nil, nil)
	if err := s.PublishCandidateManifest(
		ctx, candidatePublication(repository, commit, ""),
	); err != nil {
		t.Fatal(err)
	}
	candidate, err := s.GetCandidateManifestPublication(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	scope := store.ExtractionScope{
		Repository: repository, Commit: commit, Domain: domain,
	}
	outcome := durableOutcome(scope, store.DomainOutcomePublished, "1.0.0")
	outcome.Generation.CandidateManifestDigest = candidate.ManifestDigest
	outcome.Generation.CandidatePolicyDigest = candidate.PolicyDigest
	outcome.Generation.CandidateControlRevision = candidate.ControlRevision
	outcome.Generation.InventoryPolicy =
		"candidate-manifest-v4-" + strings.Repeat("b", 64)
	outcome.Generation.Digest =
		store.ComputeExtractionGenerationDigest(outcome.Generation)
	run, err := s.BeginExtractionRun(ctx, scope, outcome.Generation.Extractor)
	if err != nil {
		t.Fatal(err)
	}
	outcome.RunID = run.ID
	if err := s.PublishExtractionRunWithOutcome(
		ctx, run.ID, candidateCoverage(candidate.ManifestDigest), outcome,
	); err != nil {
		t.Fatalf("publish declaration outcome: %v", err)
	}
	declaration := resolvercatalog.DeclarationPublication{
		Domain: domain, RunID: run.ID,
		GenerationDigest: outcome.Generation.Digest,
	}
	identity, err := resolvercatalog.NewIdentity(
		repository, commit, "", candidate.ManifestDigest,
		[]resolvercatalog.DeclarationPublication{declaration}, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	catalogInput := resolverPublication(repository, commit, candidate.ManifestDigest)
	catalogInput.Declarations = []store.ResolverCatalogDeclarationPublication{{
		Domain: domain, RunID: run.ID,
		GenerationDigest: outcome.Generation.Digest,
	}}
	catalogInput.DeclarationSetDigest = identity.DeclarationSetDigest
	catalogInput.GenerationDigest = identity.GenerationDigest
	catalogInput.ManifestDigest = candidateDigest('6')
	if err := s.PublishResolverCatalog(ctx, catalogInput); err != nil {
		t.Fatalf("publish declaration catalog: %v", err)
	}
	catalog, err := s.GetResolverCatalogPublication(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	return finishCallerPublicationFixture(t, s, repository, commit, candidate, catalog)
}

func TestCallerGenerationPublicationRevisionLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	fixture := newCallerPublicationFixture(
		t, s, "github.com/acme/caller-publication-revision",
	)
	if err := s.PublishCallerGeneration(
		ctx, *fixture.job, fixture.publication,
	); err != nil {
		t.Fatalf("publish: %v", err)
	}
	first, err := s.GetCallerGenerationPublication(ctx, fixture.repository)
	if err != nil {
		t.Fatal(err)
	}
	if first.PublicationRevision != 1 || first.WriterSchema !=
		store.CallerGenerationPublicationWriterSchema ||
		!strings.HasPrefix(first.PublicationIncarnation, "sha256:") ||
		first.PairCount != 2 || first.ArtifactCount != 2 ||
		first.ResultCount != 5 || first.AbstentionCount != 3 ||
		first.Pairs[0].RecordCount != 3 || first.Pairs[1].RecordCount != 5 {
		t.Fatalf("first publication = %+v", first)
	}
	current, err := s.CallerGenerationPublicationCurrent(ctx, *first)
	if err != nil || !current {
		t.Fatalf("current = %v, %v", current, err)
	}
	// An exact retry is a true no-op: neither revision nor timestamp changes.
	if err := s.PublishCallerGeneration(
		ctx, *fixture.job, fixture.publication,
	); err != nil {
		t.Fatalf("exact retry: %v", err)
	}
	retried, err := s.GetCallerGenerationPublication(ctx, fixture.repository)
	if err != nil || retried.PublicationRevision != 1 ||
		retried.PublicationIncarnation != first.PublicationIncarnation ||
		!retried.PublishedAt.Equal(first.PublishedAt) {
		t.Fatalf("exact retry = %+v, %v", retried, err)
	}
	staleIncarnation := *first
	staleIncarnation.PublicationIncarnation = candidateDigest('0')
	if staleIncarnation.PublicationIncarnation == first.PublicationIncarnation {
		staleIncarnation.PublicationIncarnation = candidateDigest('f')
	}
	current, err = s.CallerGenerationPublicationCurrent(ctx, staleIncarnation)
	if err != nil || current {
		t.Fatalf("recreated full publication current = %v, %v", current, err)
	}
	conflict := fixture.publication
	conflict.ManifestDigest = candidateDigest('8')
	conflict.ManifestPath = callerpublicationid.ManifestName(
		store.ComputeCallerGenerationDigest(conflict.Generation),
		conflict.ManifestDigest,
	)
	if err := s.PublishCallerGeneration(
		ctx, *fixture.job, conflict,
	); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("same-generation divergent manifest = %v, want conflict", err)
	}
	if err := s.ClearCallerGenerationPublication(ctx, fixture.repository); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetCallerGenerationPublication(
		ctx, fixture.repository,
	); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cleared pointer = %v", err)
	}
	repo, err := s.GetRepo(ctx, fixture.repository)
	if err != nil || repo.CallerPublicationRevision != 2 {
		t.Fatalf("revision after unavailable = %+v, %v", repo, err)
	}
	// Even if one active claim survives an authority reset, a new visibility
	// edge receives a fresh store nonce and cannot reuse its prior incarnation.
	if err := s.PublishCallerGeneration(
		ctx, *fixture.job, fixture.publication,
	); err != nil {
		t.Fatalf("same-claim republish A: %v", err)
	}
	sameClaim, err := s.GetCallerGenerationPublication(ctx, fixture.repository)
	if err != nil || sameClaim.PublicationRevision != 3 ||
		sameClaim.PublicationIncarnation == first.PublicationIncarnation {
		t.Fatalf("same-claim republished A = %+v, %v", sameClaim, err)
	}
	if err := s.ClearCallerGenerationPublication(ctx, fixture.repository); err != nil {
		t.Fatal(err)
	}
	// A new live lease can republish byte-identical A, but it is a new visibility
	// transition and therefore receives revision 5.
	if err := s.SetJobStatus(ctx, *fixture.job, store.StatusDone, ""); err != nil {
		t.Fatal(err)
	}
	successor, err := s.ClaimJob(ctx, store.JobCallerLeaf, "caller-republish-test")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetJobStatus(ctx, *successor, store.StatusRunning, ""); err != nil {
		t.Fatal(err)
	}
	successor.Status = store.StatusRunning
	if err := s.PublishCallerGeneration(
		ctx, *successor, fixture.publication,
	); err != nil {
		t.Fatalf("republish A: %v", err)
	}
	republished, err := s.GetCallerGenerationPublication(ctx, fixture.repository)
	if err != nil || republished.PublicationRevision != 5 ||
		republished.PublicationIncarnation == sameClaim.PublicationIncarnation {
		t.Fatalf("republished A = %+v, %v", republished, err)
	}
	// The old lease remains structurally plausible to the caller but is fenced
	// by the transaction's exact job ownership probe.
	if err := s.PublishCallerGeneration(
		ctx, *fixture.job, fixture.publication,
	); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("lost lease publication = %v, want conflict", err)
	}
}

func TestCallerGenerationPublicationAcceptsCanonicalV2UpstreamAuthority(t *testing.T) {
	testCallerGenerationPublicationAcceptsUpstreamAuthority(t, authorityvalidate.Schema, false)
}

func TestCallerGenerationPublicationAcceptsMaximumV2UpstreamAuthority(t *testing.T) {
	testCallerGenerationPublicationAcceptsUpstreamAuthority(t, authorityvalidate.Schema, true)
}

func TestCallerGenerationPublicationAcceptsHistoricalV1UpstreamAuthority(t *testing.T) {
	testCallerGenerationPublicationAcceptsUpstreamAuthority(t, authorityvalidate.SchemaV1, false)
}

func testCallerGenerationPublicationAcceptsUpstreamAuthority(
	t *testing.T,
	schema string,
	maximum bool,
) {
	t.Helper()
	s := newTestStore(t)
	ctx := t.Context()
	repository := "github.com/acme/caller-publication-v2-upstream"
	if schema == authorityvalidate.SchemaV1 {
		repository = "github.com/acme/caller-publication-v1-upstream"
	}
	if maximum {
		repository += "-maximum"
	}
	commit := candidateCommit('1')
	if err := s.UpsertRepo(ctx, store.Repo{
		Name: repository, CloneURL: "https://" + repository + ".git",
	}); err != nil {
		t.Fatal(err)
	}
	setCandidateIndexedState(t, ctx, s, repository, commit, nil, nil)
	if err := s.PublishCandidateManifest(
		ctx, candidatePublication(repository, commit, ""),
	); err != nil {
		t.Fatal(err)
	}
	candidatePublication, err := s.GetCandidateManifestPublication(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PublishResolverCatalog(
		ctx, resolverPublication(repository, commit, candidatePublication.ManifestDigest),
	); err != nil {
		t.Fatal(err)
	}
	catalog, err := s.GetResolverCatalogPublication(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	var upstream, upstreamDigest string
	if schema == authorityvalidate.SchemaV1 {
		upstream, upstreamDigest = callerPublicationV1Upstream(
			t, repository, candidatePublication.ManifestDigest, candidatePublication.PolicyDigest,
		)
	} else {
		upstream, upstreamDigest = callerPublicationV2Upstream(
			t, repository, candidatePublication.ManifestDigest, candidatePublication.PolicyDigest,
			maximum,
		)
	}
	fixture := finishCallerPublicationFixtureWithUpstream(
		t, s, repository, commit, candidatePublication, catalog, upstream, upstreamDigest,
	)
	if err := s.PublishCallerGeneration(ctx, *fixture.job, fixture.publication); err != nil {
		t.Fatalf("publish canonical v2 upstream authority: %v", err)
	}
	published, err := s.GetCallerGenerationPublication(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	if published.Upstream != upstream || published.Generation.UpstreamDigest != upstreamDigest {
		t.Fatalf("published upstream authority = (%q, %q), want (%q, %q)",
			published.Upstream, published.Generation.UpstreamDigest, upstream, upstreamDigest)
	}
}

func TestCallerGenerationPublicationSummaryReadAndCurrent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	fixture := newCallerPublicationFixture(
		t, s, "github.com/acme/caller-publication-summary",
	)
	if err := s.PublishCallerGeneration(
		ctx, *fixture.job, fixture.publication,
	); err != nil {
		t.Fatal(err)
	}
	publication, err := s.GetCallerGenerationPublication(ctx, fixture.repository)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := s.GetCallerGenerationPublicationSummary(
		ctx, fixture.repository,
	)
	if err != nil {
		t.Fatal(err)
	}
	want := store.CallerGenerationPublicationSummary{
		Generation:        publication.Generation,
		PairPayloadDigest: publication.PairPayloadDigest,
		PairSetDigest:     publication.PairSetDigest,
		PairCount:         publication.PairCount, ArtifactCount: publication.ArtifactCount,
		ResultCount:            publication.ResultCount,
		AbstentionCount:        publication.AbstentionCount,
		CanonicalBytes:         publication.CanonicalBytes,
		StagingBytes:           publication.StagingBytes,
		PeakOpenFiles:          publication.PeakOpenFiles,
		ManifestDigest:         publication.ManifestDigest,
		ManifestPath:           publication.ManifestPath,
		PublicationRevision:    publication.PublicationRevision,
		PublicationIncarnation: publication.PublicationIncarnation,
		WriterSchema:           publication.WriterSchema,
		PublishedAt:            publication.PublishedAt,
	}
	if !reflect.DeepEqual(*summary, want) {
		t.Fatalf("caller publication summary = %+v, want %+v", summary, want)
	}
	current, err := s.CallerGenerationPublicationSummaryCurrent(ctx, *summary)
	if err != nil || !current {
		t.Fatalf("caller publication summary current = %t, %v", current, err)
	}
	stale := *summary
	stale.PublicationRevision++
	current, err = s.CallerGenerationPublicationSummaryCurrent(ctx, stale)
	if err != nil || current {
		t.Fatalf("stale caller publication summary current = %t, %v", current, err)
	}
	stale = *summary
	stale.PublishedAt = stale.PublishedAt.Add(-time.Second)
	current, err = s.CallerGenerationPublicationSummaryAuthorityCurrent(ctx, stale)
	if err != nil || !current {
		t.Fatalf("informational timestamp scalar summary current = %t, %v", current, err)
	}
	current, err = s.CallerGenerationPublicationSummaryCurrent(ctx, stale)
	if err != nil || !current {
		t.Fatalf("informational timestamp exact summary current = %t, %v", current, err)
	}
	stale = *summary
	stale.PublicationIncarnation = "sha256:" + strings.Repeat("0", 64)
	if stale.PublicationIncarnation == summary.PublicationIncarnation {
		stale.PublicationIncarnation = "sha256:" + strings.Repeat("f", 64)
	}
	current, err = s.CallerGenerationPublicationSummaryAuthorityCurrent(ctx, stale)
	if err != nil || current {
		t.Fatalf("recreated scalar summary current = %t, %v", current, err)
	}
	current, err = s.CallerGenerationPublicationSummaryCurrent(ctx, stale)
	if err != nil || current {
		t.Fatalf("recreated exact summary current = %t, %v", current, err)
	}
	if err := s.SetJobStatus(ctx, *fixture.job, store.StatusDone, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CancelPendingJobs(
		ctx, store.JobCallerLeaf, fixture.repository,
	); err != nil {
		t.Fatal(err)
	}
	// Reuse this store process for the two-authority transaction coverage. The
	// first fixture's crash-recovery successor must be canceled because
	// ClaimJob is intentionally global by kind rather than target.
	right := newCallerPublicationFixture(
		t, s, "github.com/acme/caller-publication-summary-right",
	)
	if err := s.PublishCallerGeneration(
		ctx, *right.job, right.publication,
	); err != nil {
		t.Fatal(err)
	}
	rightSummary, err := s.GetCallerGenerationPublicationSummary(
		ctx, right.repository,
	)
	if err != nil {
		t.Fatal(err)
	}
	current, err = s.CallerGenerationPublicationSummariesAuthorityCurrent(
		ctx, []store.CallerGenerationPublicationSummary{*summary, *rightSummary},
	)
	if err != nil || !current {
		t.Fatalf("joint summaries current = %t, %v", current, err)
	}
	current, err = s.CallerGenerationPublicationSummariesAuthorityCurrent(
		ctx, []store.CallerGenerationPublicationSummary{*summary},
	)
	if err != nil || !current {
		t.Fatalf("single summary current = %t, %v", current, err)
	}
	current, err = s.CallerGenerationPublicationSummariesAuthorityCurrent(
		ctx, []store.CallerGenerationPublicationSummary{*summary, *summary},
	)
	if err != nil || !current {
		t.Fatalf("deduplicated summary current = %t, %v", current, err)
	}
	jointStale := *rightSummary
	jointStale.PublicationRevision++
	current, err = s.CallerGenerationPublicationSummariesAuthorityCurrent(
		ctx, []store.CallerGenerationPublicationSummary{*summary, jointStale},
	)
	if err != nil || current {
		t.Fatalf("one stale summary current = %t, %v", current, err)
	}
	if err := s.ClearCallerGenerationPublication(ctx, right.repository); err != nil {
		t.Fatal(err)
	}
	current, err = s.CallerGenerationPublicationSummariesAuthorityCurrent(
		ctx, []store.CallerGenerationPublicationSummary{*summary, *rightSummary},
	)
	if err != nil || current {
		t.Fatalf("transitioned right summary current = %t, %v", current, err)
	}
	if _, err := s.CallerGenerationPublicationSummariesAuthorityCurrent(
		ctx, nil,
	); err == nil {
		t.Fatal("empty joint fence succeeded")
	}
	if _, err := s.CallerGenerationPublicationSummariesAuthorityCurrent(
		ctx, []store.CallerGenerationPublicationSummary{
			*summary, *rightSummary, *summary,
		},
	); err == nil {
		t.Fatal("oversized joint fence succeeded")
	}
	if err := s.ClearCallerGenerationPublication(ctx, fixture.repository); err != nil {
		t.Fatal(err)
	}
	current, err = s.CallerGenerationPublicationSummaryCurrent(ctx, *summary)
	if err != nil || current {
		t.Fatalf("cleared caller publication summary current = %t, %v", current, err)
	}
	if _, err := s.GetCallerGenerationPublicationSummary(
		ctx, fixture.repository,
	); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cleared caller publication summary read = %v", err)
	}
}

func TestCallerPublicationRepositoryEligibleProjectsOnlyExactLiveRepo(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	fixture := newCallerPublicationFixture(
		t, s, "github.com/acme/caller-publication-eligible",
	)
	unindexed := "github.com/acme/caller-publication-unindexed"
	if err := s.UpsertRepo(ctx, store.Repo{
		Name: unindexed, CloneURL: "https://" + unindexed + ".git",
	}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		repository string
		want       bool
	}{
		{repository: fixture.repository, want: true},
		{repository: unindexed},
		{repository: "github.com/acme/caller-publication-absent"},
	} {
		eligible, err := s.CallerPublicationRepositoryEligible(
			ctx, test.repository,
		)
		if err != nil || eligible != test.want {
			t.Fatalf(
				"caller publication repository %q eligible = %t, %v; want %t",
				test.repository, eligible, err, test.want,
			)
		}
	}
	if err := s.SetRepoDeleting(ctx, fixture.repository, true); err != nil {
		t.Fatal(err)
	}
	eligible, err := s.CallerPublicationRepositoryEligible(ctx, fixture.repository)
	if err != nil || eligible {
		t.Fatalf("deleting caller publication repository eligible = %t, %v", eligible, err)
	}
}

func TestCallerPublicationRepositoryPageIsKeysetBoundedAndFiltered(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	eligible := []string{
		"github.com/acme/page-a",
		"github.com/acme/page-b",
		"github.com/acme/page-c",
	}
	deleting := "github.com/acme/page-deleting"
	unindexed := "github.com/acme/page-unindexed"
	for _, repository := range []string{
		eligible[2], deleting, eligible[0], unindexed, eligible[1],
	} {
		if err := s.UpsertRepo(ctx, store.Repo{
			Name: repository, CloneURL: "https://" + repository + ".git",
		}); err != nil {
			t.Fatal(err)
		}
		if repository == unindexed {
			continue
		}
		if err := s.SetRepoIndexed(
			ctx, repository, candidateCommit('5'), time.Now().UTC(),
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetRepoDeleting(ctx, deleting, true); err != nil {
		t.Fatal(err)
	}
	first, err := s.ListCallerPublicationRepositoriesPage(ctx, "", 2)
	if err != nil || !slices.Equal(first, eligible[:2]) {
		t.Fatalf("first caller repository page = %v, %v", first, err)
	}
	second, err := s.ListCallerPublicationRepositoriesPage(
		ctx, first[len(first)-1], 2,
	)
	if err != nil || !slices.Equal(second, eligible[2:]) {
		t.Fatalf("second caller repository page = %v, %v", second, err)
	}
	last, err := s.ListCallerPublicationRepositoriesPage(ctx, second[0], 2)
	if err != nil || len(last) != 0 {
		t.Fatalf("last caller repository page = %v, %v", last, err)
	}
	if _, err := s.ListCallerPublicationRepositoriesPage(
		ctx, "../not-canonical", 1,
	); err == nil {
		t.Fatal("caller repository page accepted a non-canonical keyset cursor")
	}
	for _, limit := range []int{0, store.MaxCallerPublicationRepositoryPage + 1} {
		if _, err := s.ListCallerPublicationRepositoriesPage(
			ctx, "", limit,
		); err == nil {
			t.Fatalf("caller repository page accepted limit %d", limit)
		}
	}
}

func TestCallerGenerationPublicationRepublishesAfterControlOnlyRepair(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	repository := "github.com/acme/caller-publication-control-repair"
	commit := candidateCommit('4')
	if err := s.UpsertRepo(ctx, store.Repo{
		Name: repository, CloneURL: "https://" + repository + ".git",
	}); err != nil {
		t.Fatal(err)
	}
	setCandidateIndexedState(t, ctx, s, repository, commit, nil, nil)
	if err := s.PublishCandidateManifest(
		ctx, candidatePublication(repository, commit, ""),
	); err != nil {
		t.Fatal(err)
	}
	candidate, err := s.GetCandidateManifestPublication(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}

	// Publish A, then B, then A again so A's current resolver control
	// revision is greater than one before the caller generation is admitted.
	// Clearing and repairing it below will therefore change both mutable
	// control revisions while restoring byte-identical semantic authority.
	catalogA := resolverPublication(repository, commit, candidate.ManifestDigest)
	if err := s.PublishResolverCatalog(ctx, catalogA); err != nil {
		t.Fatal(err)
	}
	alternateIdentity, err := resolvercatalog.NewIdentity(
		repository, commit, "", candidate.ManifestDigest, nil,
		[]resolvercatalog.ResolverPack{{
			Name: "grpc-generated-attribution", Version: "1.1.0",
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	catalogB := resolverPublication(repository, commit, candidate.ManifestDigest)
	catalogB.ResolverPacks = []store.ResolverCatalogPack{{
		Name: "grpc-generated-attribution", Version: "1.1.0",
	}}
	catalogB.ResolverPackSetDigest = alternateIdentity.ResolverPackSetDigest
	catalogB.CatalogPolicyDigest = alternateIdentity.CatalogPolicyDigest
	catalogB.GenerationDigest = alternateIdentity.GenerationDigest
	catalogB.ManifestDigest = candidateDigest('7')
	if err := s.PublishResolverCatalog(ctx, catalogB); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishResolverCatalog(ctx, catalogA); err != nil {
		t.Fatal(err)
	}
	catalog, err := s.GetResolverCatalogPublication(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	if catalog.ControlRevision != 3 {
		t.Fatalf("resolver revision before repair = %d, want 3", catalog.ControlRevision)
	}

	fixture := finishCallerPublicationFixture(
		t, s, repository, commit, candidate, catalog,
	)
	if err := s.PublishCallerGeneration(
		ctx, *fixture.job, fixture.publication,
	); err != nil {
		t.Fatalf("initial caller publication: %v", err)
	}
	initial, err := s.GetCallerGenerationPublication(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}

	candidateRepair := *candidate
	candidateRepair.ControlRevision++
	if err := s.PublishCandidateManifest(ctx, candidateRepair); err != nil {
		t.Fatalf("advance candidate control revision: %v", err)
	}
	repairedCandidate, err := s.GetCandidateManifestPublication(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	catalogRepair := *catalog
	catalogRepair.ControlRevision = 0
	if err := s.PublishResolverCatalog(ctx, catalogRepair); err != nil {
		t.Fatalf("repair resolver authority: %v", err)
	}
	repairedCatalog, err := s.GetResolverCatalogPublication(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	if repairedCandidate.ControlRevision != 2 || repairedCatalog.ControlRevision != 1 {
		t.Fatalf(
			"repaired controls = candidate %d, resolver %d; want 2, 1",
			repairedCandidate.ControlRevision, repairedCatalog.ControlRevision,
		)
	}

	repairedGeneration := callerGeneration(
		repository, commit, repairedCandidate, repairedCatalog,
	)
	if got, want := store.ComputeCallerGenerationDigest(repairedGeneration),
		initial.Generation.Digest; got != want {
		t.Fatalf("control-only repair changed semantic generation: got %s, want %s", got, want)
	}
	republication := fixture.publication
	republication.Generation = repairedGeneration
	if err := s.SetJobStatus(ctx, *fixture.job, store.StatusDone, ""); err != nil {
		t.Fatal(err)
	}
	successor, err := s.ClaimJob(ctx, store.JobCallerLeaf, "caller-control-repair-test")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetJobStatus(ctx, *successor, store.StatusRunning, ""); err != nil {
		t.Fatal(err)
	}
	successor.Status = store.StatusRunning
	if err := s.PublishCallerGeneration(ctx, *successor, republication); err != nil {
		t.Fatalf("republish caller generation after control-only repair: %v", err)
	}
	republished, err := s.GetCallerGenerationPublication(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	if republished.PublicationRevision != 3 ||
		republished.Generation.CandidateControlRevision != 2 ||
		republished.Generation.ResolverControlRevision != 1 {
		t.Fatalf("repaired caller publication = %+v", republished)
	}
	current, err := s.CallerGenerationPublicationCurrent(ctx, *republished)
	if err != nil || !current {
		t.Fatalf("repaired caller publication current = %v, %v", current, err)
	}
}

func TestCallerGenerationPublicationInvalidatedBySameHeadUnitTransition(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	fixture := newCallerPublicationFixture(
		t, s, "github.com/acme/caller-publication-unit",
	)
	if err := s.PublishCallerGeneration(ctx, *fixture.job, fixture.publication); err != nil {
		t.Fatal(err)
	}
	unit, err := (analysisunit.Scope{
		Repository: fixture.repository,
		Name:       "service",
		Primary:    []string{"service/src"},
	}).State()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoIndexedState(
		ctx, fixture.repository, fixture.commit,
		[]store.IndexedRevision{{Selector: "HEAD", Branch: "HEAD", Commit: fixture.commit}},
		unit, time.Now().UTC(),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetCallerGenerationPublication(
		ctx, fixture.repository,
	); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("caller pointer survived unit transition: %v", err)
	}
	repo, err := s.GetRepo(ctx, fixture.repository)
	if err != nil || repo.CallerPublicationRevision != 2 {
		t.Fatalf("unit transition revision = %+v, %v", repo, err)
	}
}

func TestCallerGenerationPublicationRejectsStaleAuthority(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	fixture := newCallerPublicationFixture(
		t, s, "github.com/acme/caller-publication-stale",
	)
	candidate, err := s.GetCandidateManifestPublication(ctx, fixture.repository)
	if err != nil {
		t.Fatal(err)
	}
	candidate.ControlRevision++
	if err := s.PublishCandidateManifest(ctx, *candidate); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishCallerGeneration(
		ctx, *fixture.job, fixture.publication,
	); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale authority publication = %v, want conflict", err)
	}
	if _, err := s.GetCallerGenerationPublication(
		ctx, fixture.repository,
	); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("stale attempt created pointer: %v", err)
	}
	repo, err := s.GetRepo(ctx, fixture.repository)
	if err != nil || repo.CallerPublicationRevision != 0 {
		t.Fatalf("stale attempt revision = %+v, %v", repo, err)
	}
}

func TestCallerGenerationPublicationRejectsPartialSettledSet(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	fixture := newCallerPublicationFixture(
		t, s, "github.com/acme/caller-publication-partial",
	)
	if err := s.ClearCallerLeafOutcome(
		ctx, fixture.generation, fixture.publication.Pairs[0].Pair,
	); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishCallerGeneration(
		ctx, *fixture.job, fixture.publication,
	); err == nil {
		t.Fatal("partial settled set became visible")
	}
	if _, err := s.GetCallerGenerationPublication(
		ctx, fixture.repository,
	); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("partial publication created pointer: %v", err)
	}
	repo, err := s.GetRepo(ctx, fixture.repository)
	if err != nil || repo.CallerPublicationRevision != 0 {
		t.Fatalf("partial publication revision = %+v, %v", repo, err)
	}
}

func TestCallerGenerationPublicationUpstreamInvalidationsAdvanceRevision(t *testing.T) {
	t.Run("candidate clear", func(t *testing.T) {
		s := newTestStore(t)
		ctx := context.Background()
		fixture := newCallerPublicationFixture(
			t, s, "github.com/acme/caller-invalidate-candidate",
		)
		if err := s.PublishCallerGeneration(
			ctx, *fixture.job, fixture.publication,
		); err != nil {
			t.Fatal(err)
		}
		if err := s.ClearCandidateManifestPublication(ctx, fixture.repository); err != nil {
			t.Fatal(err)
		}
		assertCallerPublicationUnavailableAtRevision(t, ctx, s, fixture.repository, 2)
	})
	t.Run("resolver replacement", func(t *testing.T) {
		s := newTestStore(t)
		ctx := context.Background()
		fixture := newCallerPublicationFixture(
			t, s, "github.com/acme/caller-invalidate-resolver",
		)
		if err := s.PublishCallerGeneration(
			ctx, *fixture.job, fixture.publication,
		); err != nil {
			t.Fatal(err)
		}
		catalog, err := s.GetResolverCatalogPublication(ctx, fixture.repository)
		if err != nil {
			t.Fatal(err)
		}
		nextIdentity, err := resolvercatalog.NewIdentity(
			fixture.repository, fixture.commit, catalog.UnitDigest,
			catalog.CandidateManifestDigest, nil,
			[]resolvercatalog.ResolverPack{{Name: "neutral", Version: "1.0.0"}},
		)
		if err != nil {
			t.Fatal(err)
		}
		catalog.ResolverPacks = []store.ResolverCatalogPack{{
			Name: "neutral", Version: "1.0.0",
		}}
		catalog.ResolverPackSetDigest = nextIdentity.ResolverPackSetDigest
		catalog.CatalogPolicyDigest = nextIdentity.CatalogPolicyDigest
		catalog.GenerationDigest = nextIdentity.GenerationDigest
		catalog.ManifestDigest = candidateDigest('7')
		catalog.ControlRevision = 0
		if err := s.PublishResolverCatalog(ctx, *catalog); err != nil {
			t.Fatal(err)
		}
		assertCallerPublicationUnavailableAtRevision(t, ctx, s, fixture.repository, 2)
	})
	t.Run("caller state clear", func(t *testing.T) {
		s := newTestStore(t)
		ctx := context.Background()
		fixture := newCallerPublicationFixture(
			t, s, "github.com/acme/caller-invalidate-state",
		)
		if err := s.PublishCallerGeneration(
			ctx, *fixture.job, fixture.publication,
		); err != nil {
			t.Fatal(err)
		}
		if err := s.ClearCallerLeafOutcome(
			ctx, fixture.generation, fixture.publication.Pairs[0].Pair,
		); err != nil {
			t.Fatal(err)
		}
		assertCallerPublicationUnavailableAtRevision(t, ctx, s, fixture.repository, 2)
	})
}

func TestCallerGenerationPublicationReactivationForcesSuccessor(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	fixture := newCallerPublicationFixture(
		t, s, "github.com/acme/caller-reactivation",
	)
	if err := s.PublishCallerGeneration(
		ctx, *fixture.job, fixture.publication,
	); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoDeleting(ctx, fixture.repository, true); err != nil {
		t.Fatal(err)
	}
	assertCallerPublicationUnavailableAtRevision(
		t, ctx, s, fixture.repository, 2,
	)
	if err := s.SetRepoDeleting(ctx, fixture.repository, false); err != nil {
		t.Fatal(err)
	}
	assertPendingCallerLeafSuccessor(t, ctx, s, fixture.repository, true)
	repo, err := s.GetRepo(ctx, fixture.repository)
	if err != nil || repo.Deleting || repo.CallerPublicationRevision != 2 {
		t.Fatalf("reactivated repository = %+v, %v", repo, err)
	}
	if canceled, err := s.CancelPendingJobs(
		ctx, store.JobCallerLeaf, fixture.repository,
	); err != nil || canceled != 1 {
		t.Fatalf("cancel reactivation successor = %d, %v", canceled, err)
	}
	if err := s.SetRepoDeleting(ctx, fixture.repository, true); err != nil {
		t.Fatal(err)
	}

	// Deletion cleanup may leave or race with an ordinary pending request.
	// Reactivation must atomically promote it instead of creating a second row
	// or exposing a live pointerless repository with no repair authority.
	if _, err := s.EnqueuePending(
		ctx, store.JobCallerLeaf, fixture.repository, false,
	); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoDeleting(ctx, fixture.repository, false); err != nil {
		t.Fatal(err)
	}
	assertPendingCallerLeafSuccessor(t, ctx, s, fixture.repository, true)
	repo, err = s.GetRepo(ctx, fixture.repository)
	if err != nil || repo.Deleting || repo.CallerPublicationRevision != 2 {
		t.Fatalf("reactivated repository = %+v, %v", repo, err)
	}

	// Ordinary active-state refreshes are queue-neutral.
	if err := s.SetRepoDeleting(ctx, fixture.repository, false); err != nil {
		t.Fatal(err)
	}
	assertPendingCallerLeafSuccessor(t, ctx, s, fixture.repository, true)
}

func TestCallerGenerationPublicationEvidenceInvalidationsAdvanceRevision(t *testing.T) {
	for _, test := range []struct {
		name      string
		published bool
	}{
		{name: "published declaration replacement", published: true},
		{name: "nonpublished dependent replacement"},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := newTestStore(t)
			ctx := context.Background()
			repository := "github.com/acme/caller-evidence-" +
				strings.ReplaceAll(test.name, " ", "-")
			fixture := newCallerPublicationFixtureWithDeclaration(
				t, s, repository, "proto-contract",
			)
			if err := s.PublishCallerGeneration(
				ctx, *fixture.job, fixture.publication,
			); err != nil {
				t.Fatal(err)
			}
			candidate, err := s.GetCandidateManifestPublication(ctx, repository)
			if err != nil {
				t.Fatal(err)
			}
			scope := store.ExtractionScope{
				Repository: repository, Commit: fixture.commit,
				Domain: "proto-contract",
			}
			replacement := durableOutcome(
				scope, store.DomainOutcomeTerminalGenerationRefusal, "2.0.0",
			)
			replacement.Generation.CandidateManifestDigest = candidate.ManifestDigest
			replacement.Generation.CandidatePolicyDigest = candidate.PolicyDigest
			replacement.Generation.CandidateControlRevision = candidate.ControlRevision
			replacement.Generation.InventoryPolicy =
				"candidate-manifest-v4-" + strings.Repeat("b", 64)
			replacement.Generation.Digest =
				store.ComputeExtractionGenerationDigest(replacement.Generation)
			if test.published {
				run, beginErr := s.BeginExtractionRun(
					ctx, scope, replacement.Generation.Extractor,
				)
				if beginErr != nil {
					t.Fatal(beginErr)
				}
				replacement.Disposition = store.DomainOutcomePublished
				replacement.RunID = run.ID
				if err := s.PublishExtractionRunWithOutcome(
					ctx, run.ID, candidateCoverage(candidate.ManifestDigest), replacement,
				); err != nil {
					t.Fatalf("publish replacement outcome: %v", err)
				}
			} else if err := s.RecordExtractionDomainOutcome(ctx, replacement); err != nil {
				t.Fatalf("record replacement outcome: %v", err)
			}
			assertCallerPublicationUnavailableAtRevision(
				t, ctx, s, repository, 2,
			)
		})
	}
}

func assertCallerPublicationUnavailableAtRevision(
	t *testing.T,
	ctx context.Context,
	s *store.Surreal,
	repository string,
	want int64,
) {
	t.Helper()
	if _, err := s.GetCallerGenerationPublication(
		ctx, repository,
	); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("caller publication remains: %v", err)
	}
	repo, err := s.GetRepo(ctx, repository)
	if err != nil || repo.CallerPublicationRevision != want {
		t.Fatalf("caller revision = %+v, %v; want %d", repo, err, want)
	}
}
