package t421

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/store"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
)

func typedExtractionTestPlan(t *testing.T) Plan {
	t.Helper()
	plan := restoreContinuityTestPlan(t)
	if err := applyCallerRestoreContinuityCorrection(&plan); err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestTypedExtractionV6ContractAndHistoricalFence(t *testing.T) {
	prior := typedExtractionTestPlan(t)
	before, err := MarshalCanonical(prior)
	if err != nil {
		t.Fatal(err)
	}
	next := prior
	if err := applyTypedExtractionCorrection(&next); err != nil {
		t.Fatal(err)
	}
	after, err := MarshalCanonical(prior)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("V6 mutated V5 preimage", err)
	}
	if err := validatePlan(next, &next.Revisions); err != nil {
		t.Fatal(err)
	}
	if next.Schema != PlanV6Schema || next.ToolPolicy.ExecutionFreezeSchema != ExecutionFreezeV6Schema || next.ReceiptContract.Schema != ReceiptV6Schema || activeExecutionPlanSchema != PlanV5Schema {
		t.Fatal("prospective version or formal default changed")
	}
	raw, err := MarshalCanonical(next)
	if err != nil || len(raw) > MaxPlanV3AuthorBytes {
		t.Fatalf("V6 plan bytes=%d: %v", len(raw), err)
	}
	for _, version := range []string{PlanSchema, PlanV2Schema, PlanV3Schema, PlanV4Schema, PlanV5Schema} {
		bad := next
		bad.Schema = version
		if validatePlan(bad, &bad.Revisions) == nil {
			t.Fatalf("%s accepted V6 extraction authority", version)
		}
	}
	for _, phase := range next.WorkEnvelope.Phases {
		want := uint64(0)
		switch phase.Phase {
		case "cold", "physical_delta_b", "return_a":
			want = 1352899
		case "archive_restore":
			want = 1244894
		}
		if want != 0 && phase.GitReads.Maximum != want {
			t.Fatalf("%s source-read maximum=%d", phase.Phase, phase.GitReads.Maximum)
		}
	}
	host := executionFreezeTestHost()
	if err := validateExecutionHost(host, prior); err != nil {
		t.Fatal("historical valid host rejected", err)
	}
	if validateExecutionHost(host, next) == nil {
		t.Fatal("prospective Linux host was admitted")
	}
	for _, domain := range next.Profile.Pipeline.ExtractionDomains {
		if err := validateFrozenExtractionDomain(domain); err != nil {
			t.Fatalf("%s: %v", domain.Domain, err)
		}
	}
}

func TestTypedExtractionV6ConfigPolicyFence(t *testing.T) {
	prior := typedExtractionTestPlan(t)
	next := prior
	if err := applyTypedExtractionCorrection(&next); err != nil {
		t.Fatal(err)
	}
	source := productionSourceURL("/tmp/t422-v6/source")
	repository, err := phebssync.RepoName(source)
	if err != nil {
		t.Fatal(err)
	}
	epoch := ExecutionEpochConfig{Epoch: 1, LogicalRevision: "a", Repository: repository, Listen: "127.0.0.1:23871", APIKey: strings.Repeat("11", 32), DataRoot: "/tmp/t422-v6/data", CatalogPath: "/tmp/t422-v6/catalog"}
	oldRaw, oldConfig, err := epochConfigBytesParsed(prior, epoch, source)
	if err != nil {
		t.Fatal(err)
	}
	newRaw, newConfig, err := epochConfigBytesParsed(next, epoch, source)
	if err != nil {
		t.Fatal(err)
	}
	if oldConfig.Experimental.TypedScopeReservations || bytes.Contains(oldRaw, []byte("typed_scope_reservations")) || !newConfig.Experimental.TypedScopeReservations {
		t.Fatal("config silently changed old contract")
	}
	if _, err := parseEpochConfigBytes(next, epoch, source, oldRaw); err == nil {
		t.Fatal("V6 accepted historical allocation")
	}
	if _, err := parseEpochConfigBytes(prior, epoch, source, newRaw); err == nil {
		t.Fatal("V5 accepted corrected allocation")
	}
	oldProjection, newProjection := frozenExecutionConfig(prior, ""), frozenExecutionConfig(next, "")
	oldDigest, _ := executionConfigProjectionSHA256(oldProjection)
	newDigest, _ := executionConfigProjectionSHA256(newProjection)
	if oldDigest == newDigest || !newProjection.TypedScopeReservations {
		t.Fatal("allocation omitted from semantic binding")
	}
	oldJSON, err := json.Marshal(oldProjection)
	if err != nil || bytes.Contains(oldJSON, []byte("typed_scope_reservations")) {
		t.Fatal("historical profile wire changed", err)
	}
}

// This bounded source fixture contains every admitted candidate and the one
// structural candidate, not the full physical population or a native ceremony.
func TestTypedExtractionV6ProductionReplay(t *testing.T) {
	// Race instrumentation traverses the same complete fixture under this test-only bound.
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Minute)
	defer cancel()
	profile := accountingTestPlan(t).Profile
	profile.Pipeline.ExtractionDomains = typedScopeExtractionDomains()
	data := t.TempDir()
	repositoryDir, commit := t421ProductionReplayRepositoryFixture(t, ctx, data)
	extractors := t421ProductionReplayExtractors()
	policies, err := extract.CandidatePolicies(extractors)
	if err != nil {
		t.Fatal(err)
	}
	identities, err := candidate.PolicyIdentities(policies)
	if err != nil {
		t.Fatal(err)
	}
	candidateDir := filepath.Join(data, "candidates")
	if err = os.Mkdir(candidateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest, err := candidate.Build(ctx, candidate.Request{RepoDir: repositoryDir, OutputDir: candidateDir, Repository: t421ProductionReplayRepository, Commit: commit, Policies: policies})
	if err != nil {
		t.Fatal(err)
	}
	publication, err := candidate.Open(candidateDir, candidate.Expected{Repository: manifest.Repository, Commit: commit, Policies: identities, PolicyDigest: manifest.PolicyDigest, GenerationDigest: manifest.GenerationDigest, ManifestDigest: manifest.Digest})
	if err != nil {
		t.Fatal(err)
	}
	sparseDir := filepath.Join(data, "sparse")
	if err = os.Mkdir(sparseDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sparseRoot, err := candidate.BuildSparseRoot(ctx, sparseDir, publication, nil)
	if err != nil {
		t.Fatal(err)
	}
	sparse, err := candidate.OpenSparse(ctx, sparseDir, candidateDir, manifest.State(), sparseRoot.Digest, nil)
	if err != nil {
		t.Fatal(err)
	}
	evidence := &typedReplayEvidence{t421ProductionReplayEvidence: newT421ProductionReplayEvidence(), atoms: map[string]store.EvidenceAtom{}, placements: map[string]store.SnapshotEvidence{}}
	executor := &extract.EvidencePartitionExecutor{Evidence: evidence, Extractors: extractors, StoreAccounting: true, TypedScopeReservations: true}
	policy, err := candidate.ExtractionPolicyDigest(manifest.PolicyDigest, true, true)
	if err != nil {
		t.Fatal(err)
	}
	var reads uint64
	for _, expected := range profile.Pipeline.ExtractionDomains {
		var version string
		for _, identity := range identities {
			if identity.Domain == expected.Domain {
				version = identity.Version
			}
		}
		domain, err := sparse.OpenDomain(ctx, expected.Domain, version)
		if err != nil {
			t.Fatal(err)
		}
		authority := candidate.DomainResultAuthority{SourceGenerationDigest: SHA256([]byte("source")), ObservationGenerationDigest: SHA256([]byte("observation")), ExtractorVersion: version, ExtractionPolicyDigest: policy}
		plan, err := extractionpublication.BuildReservedPlan(domain, authority, true)
		if err != nil {
			t.Fatal(err)
		}
		t421ProductionReplayCheckPlan(t, plan, domain.Partitions(), expected)
		for ordinal, partition := range domain.Partitions() {
			if partition.TypedScope == nil {
				continue
			}
			scope, err := domain.TypedSourceScope(ctx, ordinal)
			if err != nil {
				t.Fatal(err)
			}
			summary, err := scope.Summary(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if uint64(summary.Records) != expected.TypedScopeRecords || uint64(summary.PathBytes) != expected.TypedScopePathBytes || uint64(summary.EncodedBytes) != expected.TypedScopeEncodedBytes ||
				len(expected.TypedScopeRevisions) != 3 || summary.ContentDigest != expected.TypedScopeRevisions[0].SHA256 || summary.ContentDigest != expected.TypedScopeRevisions[0].DescriptorContentSHA256 || partition.TypedScope.ContentDigest != summary.ContentDigest {
				t.Fatalf("%s typed source scope changed: %+v", expected.Domain, summary)
			}
		}

		source := extractionpublication.GitSparseSource{DataDir: data, OpenDomain: func(context.Context, candidate.DomainResultPlan) (*candidate.SparseDomain, error) { return domain, nil }}
		results := make([]candidate.PartitionResult, 0, len(plan.Expected))
		var domainReads uint64
		for ordinal := range plan.Expected {
			observed, err := readaccounting.WithSourceObserver(ctx, func() error { domainReads++; return nil })
			if err != nil {
				t.Fatal(err)
			}
			lease, err := source.AcquirePartition(observed, plan, ordinal)
			if err != nil {
				t.Fatal(err)
			}
			spec, executeErr := executor.ExecutePartition(observed, plan, ordinal, lease, "v6-"+expected.Domain)
			lease.Release()
			if executeErr != nil {
				t.Fatalf("%s/%d: %v", expected.Domain, ordinal, executeErr)
			}
			if spec.Totals != t421ProductionReplayTotals(expected.Partitions[ordinal].Expected) {
				t.Fatalf("%s/%d totals=%+v want=%+v", expected.Domain, ordinal, spec.Totals, expected.Partitions[ordinal].Expected)
			}
			result, err := candidate.BuildPartitionResult(plan, ordinal, spec)
			if err != nil {
				t.Fatal(err)
			}
			results = append(results, result)
		}
		root, err := candidate.BuildDomainResultRoot(plan, results)
		if err != nil || root.Totals != t421ProductionReplayTotals(expected.Expected) {
			t.Fatalf("%s root: %+v %v", expected.Domain, root, err)
		}

		wantReads := map[string]uint64{"grpc-caller": 53205, "grpc-consumer": 21601, "kafka-consumer": 21601, "kafka-producer": 21601, "proto-contract": 10000, "scip-proto-field": 1, "thrift-caller": 64806, "thrift-consumer": 43202, "thrift-contract": 0}
		if want, exists := wantReads[expected.Domain]; !exists || domainReads != want {
			t.Fatalf("%s native offers=%d, want%d", expected.Domain, domainReads, want)
		}
		reads += domainReads
		t.Logf("%s native content offers=%d", expected.Domain, domainReads)
		if expected.Domain == "grpc-caller" {
			authority.ExtractionPolicyDigest, err = candidate.ExtractionPolicyDigest(manifest.PolicyDigest, true, false)
			if err != nil {
				t.Fatal(err)
			}
			historical, err := extractionpublication.BuildReservedPlan(domain, authority, false)
			if err != nil {
				t.Fatal(err)
			}
			if historical.Digest == plan.Digest || historical.ExtractionPolicyDigest == policy {
				t.Fatal("new allocation reused historical authority")
			}
			lease, err := source.AcquirePartition(ctx, historical, 8)
			if err != nil {
				t.Fatal(err)
			}
			oldExecutor := &extract.EvidencePartitionExecutor{Evidence: newT421ProductionReplayEvidence(), Extractors: extractors, StoreAccounting: true}
			_, executeErr := oldExecutor.ExecutePartition(ctx, historical, 8, lease, "historical")
			lease.Release()
			refusal, ok := pipelinerefusal.From(executeErr)
			if !ok || refusal.Stage != pipelinerefusal.StageEvidenceStaging || refusal.Observed != 3 || refusal.Limit != 2 {
				t.Fatalf("historical refusal=%+v %v", refusal, executeErr)
			}
		}
	}
	if reads != 236017 {
		t.Fatalf("native content offers=%d, want236017", reads)
	}
	assertTypedCallerSourceEdgeGaps(t, evidence, commit)
}

// Expected edges come from independent graph arithmetic; source rendering is
// used only to locate the exact selector bytes, without calling an extractor.
func assertTypedCallerSourceEdgeGaps(t *testing.T, evidence *typedReplayEvidence, commit string) {
	run := evidence.runs["v6-grpc-caller"]
	t.Helper()
	type expectedGap struct {
		edge      relationshipEdge
		atom      store.EvidenceAtom
		placement store.SnapshotEvidence
	}
	expected := make(map[string]expectedGap, 10999)
	add := func(edge relationshipEdge) {
		source, err := renderRelationshipMain(edge.consumer)
		if err != nil {
			t.Fatal(err)
		}
		method := edge.identity[strings.LastIndexByte(edge.identity, '/')+1:]
		selector := fmt.Sprintf("client%05d.%s", edge.provider, method)
		start := strings.Index(string(source), selector+"(")
		if start < 0 || strings.Count(string(source), selector+"(") != 1 {
			t.Fatal("independent edge lacks unique source selector", edge)
		}
		subject := fmt.Sprintf("services/service-%05d/main.go:%d-%d", edge.consumer, start, start+len(selector))
		key := subject + "\x00" + edge.identity
		if _, exists := expected[key]; exists {
			t.Fatal("duplicate independent edge", key)
		}
		atom := store.EvidenceAtom{SchemaVersion: "t20-caller-v1", BlobDigest: SHA256(source), StartByte: start, EndByte: start + len(selector), RuleID: "go-caller-syntax-abstention-v1", ExtractorVersion: "1.6.0", AdapterConfigDigest: SHA256(nil), FactFingerprint: "UNRESOLVED_CALLER|" + edge.identity + "||no_attribution_snapshot"}
		atom.ID = store.ComputeAtomID(atom)
		path := fmt.Sprintf("services/service-%05d/main.go", edge.consumer)
		line := bytes.Count(source[:start], []byte{'\n'}) + 1
		expected[key] = expectedGap{edge: edge, atom: atom, placement: store.SnapshotEvidence{AtomID: atom.ID, Repo: t421ProductionReplayRepository, Commit: commit, Path: path, StartLine: line, EndLine: line, VisibilityScope: "repo:" + t421ProductionReplayRepository}}
	}
	for consumer := 1; consumer < relationshipServiceCount; consumer++ {
		add(independentRPCEdge("chain", consumer-1, consumer, 0))
	}
	for provider := 0; provider < (relationshipLayerCount-1)*relationshipLayerWidth; provider++ {
		next := (provider/relationshipLayerWidth + 1) * relationshipLayerWidth
		for slot, consumer := range []int{next + provider%relationshipLayerWidth, next + (provider+1)%relationshipLayerWidth} {
			add(independentRPCEdge("layered_dag", provider, consumer, slot))
		}
	}
	for provider := 0; provider < relationshipServiceCount; provider++ {
		if provider < 50 || provider >= 9950 {
			for slot := 1; slot <= relationshipFanout; slot++ {
				add(independentRPCEdge("bounded_fanout", provider, (provider+slot+1)%relationshipServiceCount, slot))
			}
		}
	}
	if run == nil || len(run.assertions) != 10999 || len(expected) != 10999 {
		t.Fatal("incomplete caller source-edge population")
	}
	for _, a := range run.assertions {
		wantGap, exists := expected[a.Subject+"\x00"+a.Object]
		edge := wantGap.edge
		if !exists || a.Predicate != "UNRESOLVED_CALLER" || a.Tier != store.TierUnresolved || a.CodeRole != "production" || a.Lineage != "" || len(a.Supporting) != 1 || len(a.Contradicting) != 0 {
			t.Fatalf("caller claim does not match independent source edge: %+v", a)
		}

		if a.Repo != t421ProductionReplayRepository || a.Supporting[0] != wantGap.atom.ID || evidence.atoms[a.Supporting[0]] != wantGap.atom || evidence.placements[a.Supporting[0]] != wantGap.placement {
			t.Fatalf("caller source citation changed: %+v", a)
		}
		var detail map[string]any
		if json.Unmarshal([]byte(a.Detail), &detail) != nil {
			t.Fatal("caller detail decode")
		}
		want := map[string]any{"schema": "go-caller-detail-v1", "resolution": "syntax", "protocol": "grpc", "generated_symbol": "", "generated_path": fmt.Sprintf("services/service-%05d/api_grpc.pb.go", edge.provider), "generator_relative_path": fmt.Sprintf("contracts/service-%05d/api.proto", edge.provider), "unresolved_reason": "no_attribution_snapshot", "unit_state": "unavailable", "unit_reason": "no_attribution_snapshot"}
		if !reflect.DeepEqual(detail, want) {
			t.Fatalf("gap detail changed: %+v", detail)
		}
		delete(expected, a.Subject+"\x00"+a.Object)
	}
	if len(expected) != 0 {
		t.Fatal("missing independent source edges", len(expected))
	}
}

// Preserve the real staged atom and placement before the strict replay model
// projects its accounting. Only this test's caller run is retained here.
type typedReplayEvidence struct {
	*t421ProductionReplayEvidence
	atoms      map[string]store.EvidenceAtom
	placements map[string]store.SnapshotEvidence
}

func (e *typedReplayEvidence) AddEvidenceChunk(ctx context.Context, runID, chunkID string, facts int, atoms []store.EvidenceAtom, placements []store.SnapshotEvidence, assertions []store.Assertion) error {
	if err := e.t421ProductionReplayEvidence.AddEvidenceChunk(ctx, runID, chunkID, facts, atoms, placements, assertions); err != nil {
		return err
	}
	if runID == "v6-grpc-caller" {
		for _, a := range atoms {
			e.atoms[a.ID] = a
		}
		for _, p := range placements {
			e.placements[p.AtomID] = p
		}
	}
	return nil
}

func TestTypedExtractionV6ReadAndWireBounds(t *testing.T) {
	prior := typedExtractionTestPlan(t)
	plan := prior
	if err := applyTypedExtractionCorrection(&plan); err != nil {
		t.Fatal(err)
	}
	// Same accepted compact families as the existing simultaneous-headroom proof;
	// census bodies, ordinary and future logs remain outside this subtotal.
	for producer := uint32(2); producer <= 6; producer++ {
		var total, source uint64
		total = 10 * 79 // fixed family bindings, including source/catalog census
		for _, phase := range executionProducerPhases(producer) {
			row := plan.WorkEnvelope.Phases[phase-1]
			source += 8 * row.GitReads.Maximum
			total += 3*row.IndexFiles.Maximum + 8*row.ObservationParses.Maximum + 10*row.JobAttempts.Maximum +
				9*(row.CacheLookups.Maximum+row.CacheMisses.Maximum) + 8*row.PublicationWrites.Maximum +
				25*row.ResolverBlobReads.Maximum + 9*(row.RelationshipBuildAttempts.Maximum+row.RelationshipProjections.Maximum) + 26*row.ServiceReferences.Maximum +
				17*plan.WorkEnvelope.MaximumRetriesPerUnit
			for _, role := range row.ControlledDispatchRoles {
				if role.Name == "zoekt-git-index" {
					total += role.Maximum * uint64(9+len(strconv.FormatUint(row.IndexFiles.Maximum, 10)))
				}
			}
		}
		drives := uint64(0)
		if producer == 5 {
			drives = 2
		}
		if producer == 6 {
			drives = 1
		}
		total += source + drives*uint64(lifecycle.MaxCycleObservationTurns)*(maxExecutionLifecycleEvent+5)
		if producer == 4 {
			total += 81
		}
		if total >= 64<<20 {
			t.Fatalf("producer%d known compact subtotal%d exceeds cap", producer, total)
		}
		t.Logf("V6 producer%d known compact bytes=%d sourcebytes=%d", producer, total, source)
	}
	out := ExecutionAttemptObservation{SourceBound: true}
	maximum := plan.WorkEnvelope.Phases[1].GitReads.Maximum
	out.Phases[1].SourceBlobAttempts = maximum - 1
	if seen, err := observeBlobEvent([]byte("SR1:2:2\n"), plan, 2, "", &out); !seen || err != nil {
		t.Fatal("exact source ceiling refused", err)
	}
	if seen, err := observeBlobEvent([]byte("SR1:2:2\n"), plan, 2, "", &out); !seen || err == nil || out.Phases[1].SourceBlobAttempts != maximum+1 {
		t.Fatal("source overshoot lost")
	}
	changed := plan.Profile
	changed.TypedIndex.ContentSHA256 = SHA256([]byte("other"))
	if _, err := typedExtractionSourceReadMaximum(changed, true); err == nil {
		t.Fatal("read recipe admitted arbitrary SCIP input")
	}
}
