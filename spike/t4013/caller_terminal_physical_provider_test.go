package t4013

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/callerexecute"
	"github.com/bmeddeb/phebs/internal/candidatejob"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	t40r1CallerPhysicalProviderSchema = "t4013-caller-terminal-physical-provider-witness-v1"
	t40r1CallerPhysicalProviderEnv    = "T40R1_CALLER_PHYSICAL_PROVIDER_WITNESS"
)

type t40r1CallerPhysicalLeaf struct {
	Name          string `json:"name"`
	Ordinal       int    `json:"ordinal"`
	Prefix        string `json:"prefix"`
	PrefixBits    int    `json:"prefix_bits"`
	RecordCount   int    `json:"record_count"`
	DeclaredBytes int64  `json:"declared_bytes"`
	ContentBytes  int64  `json:"content_bytes"`
	ContentDigest string `json:"content_digest"`
}

type t40r1CallerPhysicalDomain struct {
	Domain         string `json:"domain"`
	Version        string `json:"version"`
	LeafReplays    int    `json:"leaf_replays"`
	VisitedRecords int    `json:"visited_records"`
}

type t40r1CallerPhysicalProviderReport struct {
	ProductionProvider       bool                        `json:"production_provider"`
	ManifestDigestExact      bool                        `json:"manifest_digest_exact"`
	ControlRevisionExact     bool                        `json:"control_revision_exact"`
	WorkerPlanOpens          int                         `json:"worker_plan_opens"`
	ExpectedWorkerPlanOpens  int                         `json:"expected_worker_plan_opens"`
	LeafMembers              []t40r1CallerPhysicalLeaf   `json:"leaf_members"`
	Domains                  []t40r1CallerPhysicalDomain `json:"domains"`
	PhysicalCandidateRecords int                         `json:"physical_candidate_records"`
	AllLeafDigestsBound      bool                        `json:"all_leaf_digests_bound"`
}

type t40r1CallerPhysicalProviderWitness struct {
	Schema               string                            `json:"schema"`
	Method               string                            `json:"method"`
	CallerPath           t40r1CallerUpstreamWitness        `json:"caller_path"`
	Provider             t40r1CallerPhysicalProviderReport `json:"provider"`
	Boundary             string                            `json:"boundary"`
	ClearsBoundary       bool                              `json:"clears_boundary"`
	EstablishesScalePass bool                              `json:"establishes_scale_pass"`
	AuthorizesCeremony   bool                              `json:"authorizes_ceremony"`
}

type t40r1CallerPhysicalProvider struct {
	provider *candidatejob.Provider
	request  extract.CandidateManifestRequest
	opens    int
}

func newT40R1CallerPhysicalProvider(
	t *testing.T,
	dataDir string,
	state *store.Surreal,
	run *t40r1CallerPartitionedRun,
	registry *callerexecute.Registry,
) *t40r1CallerPhysicalProvider {
	t.Helper()
	if run == nil || run.policies == nil {
		t.Fatal("physical caller provider has no compiled policy generation")
	}
	provider, err := candidatejob.NewProvider(dataDir, state, run.policies)
	if err != nil {
		t.Fatal(err)
	}
	return &t40r1CallerPhysicalProvider{
		provider: provider,
		request: extract.CandidateManifestRequest{
			Repository: t40r1CallerUpstreamRepository,
			Commit:     run.commit,
			Domains:    registry.CandidateDomains(),
		},
	}
}

func (provider *t40r1CallerPhysicalProvider) OpenCandidateCallerPlan(
	ctx context.Context,
	request extract.CandidateManifestRequest,
) (extract.CandidateCallerPlan, error) {
	provider.opens++
	return provider.provider.OpenCandidateCallerPlan(ctx, request)
}

func (provider *t40r1CallerPhysicalProvider) OpenCount() int { return provider.opens }

func (provider *t40r1CallerPhysicalProvider) report(
	t *testing.T,
	state *store.Surreal,
	expectedWorkerOpens int,
) t40r1CallerPhysicalProviderReport {
	t.Helper()
	pointer, err := state.GetCandidateManifestPublication(
		t.Context(), t40r1CallerUpstreamRepository,
	)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := provider.provider.OpenCandidateCallerPlan(t.Context(), provider.request)
	if err != nil {
		t.Fatal(err)
	}
	report := t40r1CallerPhysicalProviderReport{
		ProductionProvider:      true,
		ManifestDigestExact:     plan.Identity() == pointer.ManifestDigest,
		ControlRevisionExact:    plan.CandidateControlRevision() == pointer.ControlRevision,
		WorkerPlanOpens:         provider.opens,
		ExpectedWorkerPlanOpens: expectedWorkerOpens,
		AllLeafDigestsBound:     true,
	}
	leaves := plan.CallerLeaves()
	for _, leaf := range leaves {
		report.LeafMembers = append(report.LeafMembers, t40r1CallerPhysicalLeaf{
			Name: leaf.Name, Ordinal: leaf.Ordinal,
			Prefix: leaf.Prefix, PrefixBits: leaf.PrefixBits,
			RecordCount: leaf.RecordCount, DeclaredBytes: leaf.DeclaredBytes,
			ContentBytes: leaf.ContentBytes, ContentDigest: leaf.ContentDigest,
		})
		report.PhysicalCandidateRecords += leaf.RecordCount
		report.AllLeafDigestsBound = report.AllLeafDigestsBound &&
			strings.HasPrefix(leaf.ContentDigest, "sha256:") && len(leaf.ContentDigest) == 71
	}
	for _, domain := range plan.CallerDomains() {
		current := t40r1CallerPhysicalDomain{
			Domain: domain.Domain, Version: domain.Version,
		}
		for _, leaf := range leaves {
			current.LeafReplays++
			if err := plan.ForEachCallerLeafFile(
				t.Context(), domain.Domain, domain.Version, leaf,
				func(extract.CandidateManifestFile) error {
					current.VisitedRecords++
					return nil
				},
			); err != nil {
				t.Fatal(err)
			}
		}
		report.Domains = append(report.Domains, current)
	}
	return report
}

func TestT40R1CallerTerminalPhysicalProviderWitness(t *testing.T) {
	if os.Getenv(t40r1CallerPhysicalProviderEnv) != "1" {
		t.Skip("set " + t40r1CallerPhysicalProviderEnv + "=1 to run the physical-provider witness")
	}
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary is not installed")
	}
	callerPath, run := runT40R1CallerTerminalUpstreamWitnessWithOptions(
		t,
		t40r1CallerUpstreamRunOptions{
			partitionedAuthority: true,
			physicalCallerPlan:   true,
		},
	)
	if run == nil || run.physicalProvider == nil {
		t.Fatal("physical-provider run did not retain its production provider")
	}
	provider := run.physicalProvider.report(
		t, run.state, callerPath.ExpectedPairs+1,
	)
	witness := t40r1CallerPhysicalProviderWitness{
		Schema:     t40r1CallerPhysicalProviderSchema,
		Method:     "build and store-publish one exact physical candidate generation, open it through candidatejob.Provider and candidate.OpenCallerPlanContext, replay every immutable caller leaf for both domains, and require worker/publication/Caller Map parity",
		CallerPath: callerPath, Provider: provider,
		Boundary: "physical_candidate_plan_provider_membership",
	}
	witness.ClearsBoundary =
		witness.CallerPath.ClearsUpstreamBoundary &&
			provider.ProductionProvider && provider.ManifestDigestExact &&
			provider.ControlRevisionExact && provider.AllLeafDigestsBound &&
			provider.WorkerPlanOpens == provider.ExpectedWorkerPlanOpens &&
			provider.PhysicalCandidateRecords == callerPath.CandidateRecords &&
			len(provider.LeafMembers) == callerPath.CandidateLeaves &&
			len(provider.Domains) == callerPath.Protocols
	if !witness.ClearsBoundary {
		t.Fatalf("caller physical-provider witness did not clear: %+v", witness)
	}
	encoded, err := json.MarshalIndent(witness, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("caller-terminal-physical-provider-witness.json")
	if err != nil {
		t.Fatalf("retained caller physical-provider witness unavailable: %v\n%s", err, encoded)
	}
	var retained t40r1CallerPhysicalProviderWitness
	if err := json.Unmarshal(raw, &retained); err != nil || !reflect.DeepEqual(retained, witness) {
		t.Fatalf("retained caller physical-provider witness differs: %v\n%s", err, encoded)
	}
	t.Log(string(encoded))
}

func TestT40R1RetainedCallerTerminalPhysicalProviderWitnessIsClosed(t *testing.T) {
	raw, err := os.ReadFile("caller-terminal-physical-provider-witness.json")
	if err != nil {
		t.Fatal(err)
	}
	var witness t40r1CallerPhysicalProviderWitness
	if err := json.Unmarshal(raw, &witness); err != nil {
		t.Fatal(err)
	}
	if witness.Schema != t40r1CallerPhysicalProviderSchema ||
		witness.Boundary != "physical_candidate_plan_provider_membership" ||
		!witness.ClearsBoundary || witness.EstablishesScalePass || witness.AuthorizesCeremony ||
		witness.CallerPath.Profile != "physical-provider-control-v1" ||
		witness.CallerPath.CandidateRecords != 1 || witness.CallerPath.CandidateLeaves != 1 ||
		witness.CallerPath.Protocols != 2 || witness.CallerPath.ExpectedPairs != 2 ||
		!witness.Provider.ProductionProvider || !witness.Provider.ManifestDigestExact ||
		!witness.Provider.ControlRevisionExact || !witness.Provider.AllLeafDigestsBound ||
		witness.Provider.WorkerPlanOpens != witness.Provider.ExpectedWorkerPlanOpens ||
		witness.Provider.PhysicalCandidateRecords != witness.CallerPath.CandidateRecords ||
		len(witness.Provider.LeafMembers) != 1 ||
		len(witness.Provider.Domains) != witness.CallerPath.Protocols {
		t.Fatalf("retained caller physical-provider witness is not closed: %+v", witness)
	}
	leaf := witness.Provider.LeafMembers[0]
	if leaf.Ordinal != 0 || leaf.Prefix != "01" || leaf.PrefixBits != 2 ||
		leaf.RecordCount != 1 || leaf.DeclaredBytes != 32 || leaf.ContentBytes != 344 ||
		!strings.HasPrefix(leaf.ContentDigest, "sha256:") {
		t.Fatalf("retained physical leaf is not exact: %+v", leaf)
	}
	for _, domain := range witness.Provider.Domains {
		if domain.LeafReplays != 1 || domain.VisitedRecords != 1 {
			t.Fatalf("retained physical domain replay is not exact: %+v", domain)
		}
	}
}
