package t4013

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/callerexecute"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/downstreamauthority"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/sourcepartition"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	t40r1CallerPartitionedSchema = "t4013-caller-terminal-partitioned-authority-witness-v1"
	t40r1CallerPartitionedEnv    = "T40R1_CALLER_PARTITIONED_AUTHORITY_WITNESS"
)

type t40r1CallerPartitionedDomain struct {
	Domain              string `json:"domain"`
	Version             string `json:"version"`
	Disposition         string `json:"disposition"`
	CandidatePartitions int    `json:"candidate_partitions"`
	PlanRootCurrent     bool   `json:"plan_root_current"`
}

type t40r1CallerPartitionedAuthority struct {
	ObservationVersion           string                         `json:"observation_version"`
	ObservationCurrent           bool                           `json:"observation_current"`
	ObservationRecords           int                            `json:"observation_records"`
	ObservedRecords              int                            `json:"observed_records"`
	CandidateControlRecords      int                            `json:"candidate_control_records"`
	RequiredDomains              int                            `json:"required_domains"`
	Domains                      []t40r1CallerPartitionedDomain `json:"domains"`
	UpstreamUsable               bool                           `json:"upstream_usable"`
	CallerUpstreamDigestBound    bool                           `json:"caller_upstream_digest_bound"`
	CallerUpstreamDigestExact    bool                           `json:"caller_upstream_digest_exact"`
	CallerUpstreamCanonicalExact bool                           `json:"caller_upstream_canonical_exact"`
	CallerGenerationRederived    bool                           `json:"caller_generation_rederived"`
}

type t40r1CallerPartitionedWitness struct {
	Schema               string                          `json:"schema"`
	Method               string                          `json:"method"`
	Profile              string                          `json:"profile"`
	CallerPath           t40r1CallerUpstreamWitness      `json:"caller_path"`
	Authority            t40r1CallerPartitionedAuthority `json:"authority"`
	Boundary             string                          `json:"boundary"`
	ClearsBoundary       bool                            `json:"clears_boundary"`
	EstablishesScalePass bool                            `json:"establishes_scale_pass"`
	AuthorizesCeremony   bool                            `json:"authorizes_ceremony"`
}

type t40r1CallerPartitionedPlan struct {
	plan       candidate.DomainResultPlan
	root       candidate.DomainResultRoot
	partitions int
}

type t40r1CallerPartitionedRun struct {
	commit      string
	manifest    candidate.Manifest
	observation observationpublication.DownstreamAuthority
	plans       []t40r1CallerPartitionedPlan
	authorities []candidate.DownstreamDomainAuthority
	required    []downstreamauthority.DomainIdentity
	upstream    downstreamauthority.Authority
	report      t40r1CallerPartitionedAuthority
}

func TestT40R1CallerTerminalPartitionedAuthorityWitness(t *testing.T) {
	if os.Getenv(t40r1CallerPartitionedEnv) != "1" {
		t.Skip("set " + t40r1CallerPartitionedEnv + "=1 to run the disk-backed partitioned-authority witness")
	}
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary is not installed")
	}
	callerPath, partitioned := runT40R1CallerTerminalUpstreamWitnessWithOptions(
		t, t40r1CallerUpstreamRunOptions{partitionedAuthority: true},
	)
	if partitioned == nil {
		t.Fatal("partitioned-authority run did not retain its authority report")
	}
	callerPath.Method = "the retained semantic caller cardinality is replayed through the production partitioned-authority projection, resolver catalog, caller worker, durable admission/publication, shared reader, and exact Caller Map"
	callerPath.Boundary = "partitioned_observation_extraction_authority"
	witness := t40r1CallerPartitionedWitness{
		Schema:  t40r1CallerPartitionedSchema,
		Method:  "publish a real current observation-inventory v2 generation and exact current gRPC/Thrift partitioned extraction plan/root controls, then require the caller worker to derive and bind their usable aggregate digest before replaying the already-cleared resolver/product path",
		Profile: "semantic-262144-v1", CallerPath: callerPath,
		Authority: partitioned.report,
		Boundary:  "partitioned_observation_extraction_authority",
	}
	witness.ClearsBoundary =
		witness.CallerPath.ClearsUpstreamBoundary &&
			witness.Authority.ObservationVersion == observationpublication.DownstreamAuthorityV2 &&
			witness.Authority.ObservationCurrent &&
			witness.Authority.ObservationRecords == 1 &&
			witness.Authority.ObservedRecords == 1 &&
			witness.Authority.CandidateControlRecords == 2 &&
			witness.Authority.RequiredDomains == 2 &&
			len(witness.Authority.Domains) == 2 &&
			witness.Authority.UpstreamUsable &&
			witness.Authority.CallerUpstreamDigestBound &&
			witness.Authority.CallerUpstreamDigestExact &&
			witness.Authority.CallerUpstreamCanonicalExact &&
			witness.Authority.CallerGenerationRederived
	for _, domain := range witness.Authority.Domains {
		witness.ClearsBoundary = witness.ClearsBoundary &&
			domain.Disposition == candidate.PartitionResultEmpty &&
			domain.CandidatePartitions == 2 && domain.PlanRootCurrent
	}
	if !witness.ClearsBoundary {
		t.Fatalf("caller partitioned-authority witness did not clear: %+v", witness)
	}
	encoded, err := json.MarshalIndent(witness, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("caller-terminal-partitioned-authority-witness.json")
	if err != nil {
		t.Fatalf("retained caller partitioned-authority witness unavailable: %v\n%s", err, encoded)
	}
	var retained t40r1CallerPartitionedWitness
	if err := json.Unmarshal(raw, &retained); err != nil || !reflect.DeepEqual(retained, witness) {
		t.Fatalf("retained caller partitioned-authority witness differs: %v\n%s", err, encoded)
	}
	t.Log(string(encoded))
}

func TestT40R1RetainedCallerTerminalPartitionedAuthorityWitnessIsClosed(t *testing.T) {
	raw, err := os.ReadFile("caller-terminal-partitioned-authority-witness.json")
	if err != nil {
		t.Fatal(err)
	}
	var witness t40r1CallerPartitionedWitness
	if err := json.Unmarshal(raw, &witness); err != nil {
		t.Fatal(err)
	}
	if witness.Schema != t40r1CallerPartitionedSchema ||
		witness.Profile != "semantic-262144-v1" ||
		witness.Boundary != "partitioned_observation_extraction_authority" ||
		!witness.ClearsBoundary || witness.EstablishesScalePass || witness.AuthorizesCeremony ||
		!witness.CallerPath.ClearsUpstreamBoundary ||
		witness.Authority.ObservationVersion != observationpublication.DownstreamAuthorityV2 ||
		!witness.Authority.ObservationCurrent ||
		witness.Authority.ObservationRecords != 1 || witness.Authority.ObservedRecords != 1 ||
		witness.Authority.CandidateControlRecords != 2 ||
		witness.Authority.RequiredDomains != 2 || len(witness.Authority.Domains) != 2 ||
		!witness.Authority.UpstreamUsable || !witness.Authority.CallerUpstreamDigestBound ||
		!witness.Authority.CallerUpstreamDigestExact ||
		!witness.Authority.CallerUpstreamCanonicalExact ||
		!witness.Authority.CallerGenerationRederived {
		t.Fatalf("retained caller partitioned-authority witness is not closed: %+v", witness)
	}
	for _, domain := range witness.Authority.Domains {
		if domain.Disposition != candidate.PartitionResultEmpty ||
			domain.CandidatePartitions != 2 || !domain.PlanRootCurrent {
			t.Fatalf("retained partitioned domain is not exact: %+v", domain)
		}
	}
}

func prepareT40R1CallerPartitionedRun(
	t *testing.T,
	ctx context.Context,
	dataDir string,
	state *store.Surreal,
	registry *callerexecute.Registry,
) *t40r1CallerPartitionedRun {
	t.Helper()
	repositoryDirectory, commit := t40r1CallerPartitionedRepository(t, ctx)
	policies, err := extract.CandidatePolicies([]extract.Extractor{
		gocaller.NewGRPC(), gocaller.NewThrift(),
	})
	if err != nil {
		t.Fatal(err)
	}
	identities, err := candidate.PolicyIdentities(policies)
	if err != nil {
		t.Fatal(err)
	}
	candidateDirectory := t.TempDir()
	manifest, err := candidate.Build(ctx, candidate.Request{
		RepoDir: repositoryDirectory, OutputDir: candidateDirectory,
		Repository: t40r1CallerUpstreamRepository, Commit: commit, Policies: policies,
	})
	if err != nil {
		t.Fatal(err)
	}
	publication, err := candidate.Open(candidateDirectory, candidate.Expected{
		Repository: t40r1CallerUpstreamRepository, Commit: commit, Policies: identities,
	})
	if err != nil {
		t.Fatal(err)
	}
	sparseDirectory := t.TempDir()
	sparseRoot, err := candidate.BuildSparseRoot(ctx, sparseDirectory, publication, nil)
	if err != nil {
		t.Fatal(err)
	}
	sparse, err := candidate.OpenSparse(
		ctx, sparseDirectory, candidateDirectory, manifest.State(), sparseRoot.Digest, nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	observation := publishT40R1CallerPartitionedObservation(
		t, ctx, dataDir, repositoryDirectory, commit,
	)
	run := &t40r1CallerPartitionedRun{
		commit: commit, manifest: manifest, observation: observation,
		required: make([]downstreamauthority.DomainIdentity, len(registry.Adapters())),
		report: t40r1CallerPartitionedAuthority{
			ObservationVersion: observation.Version, ObservationCurrent: true,
			ObservationRecords:      observation.RecordCount,
			ObservedRecords:         observation.ObservedCount,
			CandidateControlRecords: manifest.Corpus.RegularCount,
			RequiredDomains:         len(registry.Adapters()),
		},
	}
	for index, adapter := range registry.Adapters() {
		run.required[index] = downstreamauthority.DomainIdentity{
			Domain: adapter.Domain, Version: adapter.Version,
		}
		domain, err := sparse.OpenDomain(ctx, adapter.Domain, adapter.Version)
		if err != nil {
			t.Fatal(err)
		}
		partitions := domain.Partitions()
		plan, err := candidate.BuildDomainResultPlan(
			domain,
			candidate.DomainResultAuthority{
				SourceGenerationDigest:      observation.SourceGenerationDigest,
				ObservationGenerationDigest: observation.ObservationGenerationDigest,
				ExtractorVersion:            adapter.Version,
				ExtractionPolicyDigest: t40r1CallerUpstreamDigest(
					"partitioned-extraction-policy:" + adapter.Domain + ":" + adapter.Version,
				),
			},
			make([]candidate.DomainResultTotals, len(partitions)),
		)
		if err != nil {
			t.Fatal(err)
		}
		results := make([]candidate.PartitionResult, len(partitions))
		for ordinal := range partitions {
			results[ordinal], err = candidate.BuildPartitionResult(
				plan, ordinal,
				candidate.PartitionResultSpec{Disposition: candidate.PartitionResultEmpty},
			)
			if err != nil {
				t.Fatal(err)
			}
		}
		root, err := candidate.BuildDomainResultRoot(plan, results)
		if err != nil {
			t.Fatal(err)
		}
		run.plans = append(run.plans, t40r1CallerPartitionedPlan{
			plan: plan, root: root, partitions: len(partitions),
		})
	}
	return run
}

func (run *t40r1CallerPartitionedRun) publish(
	t *testing.T,
	ctx context.Context,
	state *store.Surreal,
) {
	t.Helper()
	publisher := extractionpublication.StorePublisher{Store: state}
	for _, current := range run.plans {
		extractionRun, err := state.BeginPartitionedExtractionRun(
			ctx,
			store.ExtractionScope{
				Repository: t40r1CallerUpstreamRepository,
				Commit:     run.commit, Domain: current.plan.Domain,
			},
			"t40r1-caller-partitioned-authority-"+current.plan.Domain,
			current.plan.Digest, run.manifest.Digest, current.plan.Schema,
			store.PartitionedExtractionRunLimits{},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := publisher.PublishDomain(
			ctx, current.plan, current.root, extractionRun.ID,
		); err != nil {
			t.Fatal(err)
		}
		authority, err := extractionpublication.CurrentDomainAuthority(
			ctx, state, t40r1CallerUpstreamRepository, current.plan.Domain,
		)
		if err != nil {
			t.Fatal(err)
		}
		planRootCurrent :=
			authority.PlanDigest == current.plan.Digest &&
				authority.RootDigest == current.root.Digest &&
				authority.CandidateManifestDigest == run.manifest.Digest &&
				authority.SourceGenerationDigest == run.observation.SourceGenerationDigest &&
				authority.ObservationGenerationDigest == run.observation.ObservationGenerationDigest
		run.authorities = append(run.authorities, authority)
		run.report.Domains = append(run.report.Domains, t40r1CallerPartitionedDomain{
			Domain: current.plan.Domain, Version: current.plan.Version,
			Disposition:         authority.Disposition,
			CandidatePartitions: current.partitions, PlanRootCurrent: planRootCurrent,
		})
	}
	upstream, err := downstreamauthority.BuildRequired(
		run.observation, run.required, run.authorities,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := downstreamauthority.RequireUsable(upstream); err != nil {
		t.Fatal(err)
	}
	run.upstream = upstream
	run.report.UpstreamUsable = true
}

func (run *t40r1CallerPartitionedRun) close(
	t *testing.T,
	pointer *store.CallerGenerationPublication,
) {
	t.Helper()
	if pointer == nil {
		t.Fatal("partitioned-authority witness did not publish caller authority")
	}
	bound := pointer.Generation.UpstreamDigest
	run.report.CallerUpstreamDigestBound = strings.HasPrefix(bound, "sha256:") && len(bound) == 71
	run.report.CallerUpstreamDigestExact = bound == run.upstream.Digest
	canonical, err := json.Marshal(run.upstream)
	if err != nil {
		t.Fatal(err)
	}
	run.report.CallerUpstreamCanonicalExact = pointer.Upstream == string(canonical)
	legacy := pointer.Generation
	legacy.UpstreamDigest = ""
	legacy.Digest = ""
	run.report.CallerGenerationRederived =
		pointer.Generation.Digest != store.ComputeCallerGenerationDigest(legacy)
	if !run.report.CallerUpstreamDigestBound || !run.report.CallerUpstreamDigestExact ||
		!run.report.CallerUpstreamCanonicalExact ||
		!run.report.CallerGenerationRederived {
		t.Fatalf("caller generation did not bind partitioned authority: %+v", run.report)
	}
}

func publishT40R1CallerPartitionedObservation(
	t *testing.T,
	ctx context.Context,
	dataDir,
	repositoryDirectory,
	commit string,
) observationpublication.DownstreamAuthority {
	t.Helper()
	root := filepath.Join(dataDir, "observations")
	transition, err := observationpublication.BeginInventoryPublicationV2(
		root, t40r1CallerUpstreamRepository,
	)
	if err != nil {
		t.Fatal(err)
	}
	sourceDirectory := filepath.Join(t.TempDir(), "source")
	source, err := repositoryindex.BuildSourceGeneration(
		ctx, repositoryDirectory, sourceDirectory, t40r1CallerUpstreamRepository,
		[]store.IndexedRevision{{Selector: "HEAD", Branch: "HEAD", Commit: commit}},
	)
	if err != nil {
		t.Fatal(err)
	}
	sourceRoot, err := sourcepartition.BuildSuperRoot(ctx, sourcepartition.BuildRequest{
		SourceDirectory: sourceDirectory, OutputDirectory: transition.SourceDirectory,
		Repository: t40r1CallerUpstreamRepository, Source: source,
		Policy: sourcepartition.Policy{
			Schema: sourcepartition.PolicySchema, Name: "go-source", Version: "1.0.0",
			IncludeSuffixes: []string{".go"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := sourcepartition.OpenSuperRoot(ctx, transition.SourceDirectory, sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := observationpublication.BuildInventoryStageV2(
		ctx,
		observationpublication.InventoryBuildRequestV2{
			OutputDirectory:     transition.InventoryDirectory,
			RepositoryDirectory: repositoryDirectory, Plan: plan,
		},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := observationpublication.CompleteInventoryPublicationV2(
		ctx, root, t40r1CallerUpstreamRepository, transition.TransitionID, nil,
	); err != nil {
		t.Fatal(err)
	}
	authority, err := observationpublication.CurrentInventoryDownstreamAuthorityV2(
		ctx, root, t40r1CallerUpstreamRepository,
	)
	if err != nil {
		t.Fatal(err)
	}
	return authority
}

func t40r1CallerPartitionedRepository(
	t *testing.T,
	ctx context.Context,
) (string, string) {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "repository")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	t40r1CallerPartitionedGit(t, ctx, directory, "init", "-q")
	if err := os.WriteFile(
		filepath.Join(directory, "main.go"),
		[]byte("package neutral\n\nfunc Call() {}\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(directory, "index.scip"), []byte("t40r1-neutral-scip-control\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	t40r1CallerPartitionedGit(t, ctx, directory, "add", "--", "main.go", "index.scip")
	t40r1CallerPartitionedGit(t, ctx, directory, "commit", "-q", "-m", "partitioned authority control")
	return directory, strings.TrimSpace(
		t40r1CallerPartitionedGit(t, ctx, directory, "rev-parse", "HEAD"),
	)
}

func t40r1CallerPartitionedGit(
	t *testing.T,
	ctx context.Context,
	directory string,
	arguments ...string,
) string {
	t.Helper()
	command := exec.CommandContext(ctx, "git", arguments...)
	command.Dir = directory
	command.Env = append(slices.DeleteFunc(os.Environ(), func(value string) bool {
		return strings.HasPrefix(value, "GIT_")
	}),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=T40R1 Authority Witness",
		"GIT_AUTHOR_EMAIL=t40r1-authority@example.invalid",
		"GIT_AUTHOR_DATE=2000-01-01T00:00:00Z",
		"GIT_COMMITTER_NAME=T40R1 Authority Witness",
		"GIT_COMMITTER_EMAIL=t40r1-authority@example.invalid",
		"GIT_COMMITTER_DATE=2000-01-01T00:00:00Z",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(arguments, " "), err, output)
	}
	return string(output)
}
