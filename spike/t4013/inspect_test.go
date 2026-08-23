package t4013

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apiresponse "github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/downstreamauthority"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/search"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestProfileInspectionContractTracksPlanSchema(t *testing.T) {
	tests := []struct {
		schema string
		want   profileInspectionContract
	}{
		{PlanSchema, profileInspectionLegacy},
		{PlanSchemaV14, profileInspectionV14},
		{PlanSchemaV15, profileInspectionV15},
		{PlanSchemaV16, profileInspectionV16},
		{PlanSchemaV19, profileInspectionV16},
		{PlanSchemaV20, profileInspectionV20},
		{PlanSchemaV21, profileInspectionV21},
		{PlanSchemaV25, profileInspectionV21},
	}
	for _, test := range tests {
		if got := profileInspectionForPlan(test.schema); got != test.want {
			t.Fatalf("inspection contract for %s = %d, want %d", test.schema, got, test.want)
		}
	}
}

func TestCallerGenerationTerminalClassificationIsImmediateAndTyped(t *testing.T) {
	total := 192
	bound := apiresponse.CallerMapPage{Generation: &apiresponse.CallerMapGeneration{
		State: "failed", GenerationDigest: "sha256:caller",
		PartitionProgress: &apiresponse.CallerMapPartitionProgress{
			State: "complete", SettledPairCount: total, SucceededPairCount: 38,
			RefusedPairCount: 154, TotalPairCount: &total,
			Refusals: []apiresponse.CallerMapRefusalSummary{{
				Stage:          pipelinerefusal.StageCallerGenerationAdmission,
				GenerationKind: pipelinerefusal.GenerationCaller,
				Classification: pipelinerefusal.ClassificationLimit,
				Dimension:      pipelinerefusal.DimensionCallerGenerationAbstentions,
				Observed:       100_306, Limit: callerleaf.MaxAggregateAbstentionRecords,
				OutcomeCount: 154,
			}},
		},
	}}
	probe, err := classifyCallerGeneration(bound, callerJobProbe{}, profileInspectionLegacy)
	if probe.Stage != "caller_generation" || !errors.Is(err, errCallerGenerationBoundRefusal) {
		t.Fatalf("typed caller refusal = %+v, %v", probe, err)
	}

	unknown := bound
	unknown.Generation = &apiresponse.CallerMapGeneration{
		State: "failed", PartitionProgress: &apiresponse.CallerMapPartitionProgress{
			State: "complete", SettledPairCount: total, SucceededPairCount: 38,
			RefusedPairCount: 154, TotalPairCount: &total,
		},
	}
	if _, err := classifyCallerGeneration(unknown, callerJobProbe{}, profileInspectionLegacy); !errors.Is(err, errCallerGenerationTerminal) {
		t.Fatalf("untyped caller refusal = %v", err)
	}

	currentTotal := 1
	current := apiresponse.CallerMapPage{Generation: &apiresponse.CallerMapGeneration{
		State: "current", GenerationDigest: "sha256:" + strings.Repeat("c", 64), PartitionProgress: &apiresponse.CallerMapPartitionProgress{
			State: "complete", SettledPairCount: 1, SucceededPairCount: 1,
			TotalPairCount: &currentTotal,
		},
	}}
	currentProbe, err := classifyCallerGeneration(current, callerJobProbe{}, profileInspectionLegacy)
	if err != nil {
		t.Fatalf("current caller generation rejected: %v", err)
	}
	if currentProbe.callerGenerationDigest != current.Generation.GenerationDigest {
		t.Fatalf("current caller authority = %q", currentProbe.callerGenerationDigest)
	}
	for _, state := range []string{"missing", "stale"} {
		t.Run(state, func(t *testing.T) {
			page := apiresponse.CallerMapPage{Generation: &apiresponse.CallerMapGeneration{
				State: state, PartitionProgress: &apiresponse.CallerMapPartitionProgress{
					State: "unavailable",
				},
			}}
			_, err := classifyCallerGeneration(page, callerJobProbe{}, profileInspectionLegacy)
			if err == nil || classifyConvergenceInspection(err).class != "pending" {
				t.Fatalf("%s caller generation = %v", state, err)
			}
		})
	}
	settledTotal := 8
	for _, state := range []string{"missing", "stale"} {
		t.Run(state+" complete work", func(t *testing.T) {
			page := apiresponse.CallerMapPage{Generation: &apiresponse.CallerMapGeneration{
				State: state, PartitionProgress: &apiresponse.CallerMapPartitionProgress{
					State: "complete", SettledPairCount: settledTotal,
					SucceededPairCount: settledTotal, TotalPairCount: &settledTotal,
				},
			}}
			if _, err := classifyCallerGeneration(page, callerJobProbe{}, profileInspectionV16); err == nil ||
				classifyConvergenceInspection(err).class != "pending" {
				t.Fatalf("v19-compatible %s complete work = %v", state, err)
			}
			if _, err := classifyCallerGeneration(page, callerJobProbe{}, profileInspectionV20); !errors.Is(
				err, errCallerPublicationMissing,
			) {
				t.Fatalf("v20 %s complete work = %v, want terminal", state, err)
			}
		})
	}
	invalidCurrent := current
	invalidCurrent.Generation = &apiresponse.CallerMapGeneration{State: "current"}
	if _, err := classifyCallerGeneration(invalidCurrent, callerJobProbe{}, profileInspectionLegacy); !errors.Is(err, errCallerGenerationTerminal) {
		t.Fatalf("invalid current caller generation = %v", err)
	}
}

func TestRelationshipGenerationTerminalClassificationIsImmediateAndTyped(t *testing.T) {
	scheduleDigest := "sha256:" + strings.Repeat("a", 64)
	generationDigest := "sha256:" + strings.Repeat("b", 64)
	active := store.GenerationScheduleProgress{
		Schema:         store.GenerationScheduleProgressSchema,
		ScheduleDigest: scheduleDigest, Generation: generationDigest,
		Status: store.GenerationScheduleActive, Total: 1, Materialized: 1,
		Running: 1, MaxAttempts: relationshippublication.ScheduleMaxAttempts,
	}
	probe, err := classifyRelationshipGeneration(active)
	if probe.Stage != "relationship_publication" ||
		classifyConvergenceInspection(err).class != "pending" {
		t.Fatalf("active relationship generation = %+v, %v", probe, err)
	}
	malformedActive := active
	malformedActive.Failure = &store.GenerationScheduleFailure{}
	if _, err := classifyRelationshipGeneration(malformedActive); !errors.Is(err, errRelationshipTerminal) {
		t.Fatalf("active relationship generation carried a failure: %v", err)
	}

	refusal := pipelinerefusal.Receipt{
		Schema:         pipelinerefusal.Schema,
		Stage:          pipelinerefusal.StageRelationshipKafkaProjection,
		GenerationKind: pipelinerefusal.GenerationRelationship,
		Classification: pipelinerefusal.ClassificationLimit,
		Dimension:      pipelinerefusal.DimensionResidentBytes,
		Observed:       relationshippublication.KafkaResidentLimit + 1,
		Limit:          relationshippublication.KafkaResidentLimit,
	}
	settled := active
	settled.Status, settled.Running, settled.Failed = store.GenerationScheduleSettled, 0, 1
	settled.Failure = &store.GenerationScheduleFailure{
		ScheduleDigest: scheduleDigest, Generation: generationDigest,
		Attempt: relationshippublication.ScheduleMaxAttempts - 1, Refusal: &refusal,
	}
	probe, err = classifyRelationshipGeneration(settled)
	if probe.Stage != "relationship_publication" ||
		!errors.Is(err, errRelationshipBoundRefusal) ||
		classifyConvergenceInspection(err).class != "terminal" {
		t.Fatalf("settled relationship refusal = %+v, %v", probe, err)
	}
	for _, stage := range []pipelinerefusal.Stage{
		pipelinerefusal.StageRelationshipResolverNamespaces,
		pipelinerefusal.StageRelationshipRPCPostings,
	} {
		sibling := settled
		siblingRefusal := refusal
		siblingRefusal.Stage = stage
		siblingRefusal.Observed, siblingRefusal.Limit = 9, 8
		sibling.Failure = &store.GenerationScheduleFailure{
			ScheduleDigest: scheduleDigest, Generation: generationDigest,
			Attempt: relationshippublication.ScheduleMaxAttempts - 1,
			Refusal: &siblingRefusal,
		}
		if _, err := classifyRelationshipGeneration(sibling); !errors.Is(err, errRelationshipBoundRefusal) {
			t.Fatalf("%s refusal = %v", stage, err)
		}
	}

	ordinary := settled
	ordinary.Failure = &store.GenerationScheduleFailure{
		ScheduleDigest: scheduleDigest, Generation: generationDigest,
		Attempt: 0,
	}
	if _, err := classifyRelationshipGeneration(ordinary); !errors.Is(err, errRelationshipTerminal) {
		t.Fatalf("untyped relationship failure = %v", err)
	}
	// Settled successful while the root read still saw nothing is the
	// publish/settle race: the worker publishes before completion settles, so
	// the classification stays pending and the next poll observes the root.
	succeeded := active
	succeeded.Status, succeeded.Running, succeeded.Succeeded = store.GenerationScheduleSettled, 0, 1
	probe, err = classifyRelationshipGeneration(succeeded)
	if probe.Stage != "relationship_publication" ||
		classifyConvergenceInspection(err).class != "pending" {
		t.Fatalf("settled-successful publish race = %+v, %v", probe, err)
	}
}

func TestRelationshipPublicationAbsenceUsesScheduleAuthority(t *testing.T) {
	inspector := &profileInspector{
		readRelationshipScheduleFunc: func(
			context.Context, PreparedProfile,
		) (store.GenerationScheduleProgress, error) {
			return store.GenerationScheduleProgress{}, store.ErrNotFound
		},
	}
	probe, err := inspector.relationshipGenerationTerminal(t.Context(), PreparedProfile{})
	if probe.Stage != "relationship_publication" ||
		classifyConvergenceInspection(err).class != "pending" {
		t.Fatalf("missing relationship schedule = %+v, %v", probe, err)
	}
	wrapped := fmt.Errorf("open relationship publication: %w", relationshippublication.ErrNotFound)
	if classifyConvergenceInspection(wrapped).class != "pending" {
		t.Fatalf("relationship publication absence = %v", wrapped)
	}
}

func TestSettledRelationshipSuccessRechecksPublicationRoot(t *testing.T) {
	progress := store.GenerationScheduleProgress{
		Schema:         store.GenerationScheduleProgressSchema,
		ScheduleDigest: "sha256:" + strings.Repeat("a", 64),
		Generation:     "sha256:" + strings.Repeat("b", 64),
		Status:         store.GenerationScheduleSettled,
		Total:          1,
		Materialized:   1,
		Succeeded:      1,
		MaxAttempts:    relationshippublication.ScheduleMaxAttempts,
	}
	for _, test := range []struct {
		name      string
		rootError error
		wantClass string
	}{
		{name: "publish-settle race", wantClass: "pending"},
		{name: "settled root absent", rootError: relationshippublication.ErrNotFound, wantClass: "terminal"},
	} {
		t.Run(test.name, func(t *testing.T) {
			inspector := &profileInspector{
				readRelationshipScheduleFunc: func(
					context.Context, PreparedProfile,
				) (store.GenerationScheduleProgress, error) {
					return progress, nil
				},
				readRelationshipRootFunc: func(context.Context, PreparedProfile) error {
					return test.rootError
				},
			}
			probe, err := inspector.relationshipGenerationTerminal(t.Context(), PreparedProfile{})
			if probe.Stage != "relationship_publication" ||
				classifyConvergenceInspection(err).class != test.wantClass {
				t.Fatalf("root recheck = %+v, %v", probe, err)
			}
		})
	}
	malformed := progress
	malformed.Pending = 1
	inspector := &profileInspector{
		readRelationshipScheduleFunc: func(
			context.Context, PreparedProfile,
		) (store.GenerationScheduleProgress, error) {
			return malformed, nil
		},
		readRelationshipRootFunc: func(context.Context, PreparedProfile) error {
			t.Fatal("malformed settled schedule rechecked the root")
			return nil
		},
	}
	if _, err := inspector.relationshipGenerationTerminal(
		t.Context(), PreparedProfile{},
	); !errors.Is(err, errRelationshipTerminal) {
		t.Fatalf("malformed settled schedule = %v", err)
	}
}

func TestV16RelationshipCurrentAndSchedulePairClassification(t *testing.T) {
	scheduleDigest := "sha256:" + strings.Repeat("a", 64)
	generationDigest := "sha256:" + strings.Repeat("b", 64)
	active := store.GenerationScheduleProgress{
		Schema:         store.GenerationScheduleProgressSchema,
		ScheduleDigest: scheduleDigest, Generation: generationDigest,
		Status: store.GenerationScheduleActive, Total: 1, Materialized: 1,
		Running: 1, MaxAttempts: relationshippublication.ScheduleMaxAttempts,
	}
	inspector := &profileInspector{
		contract: profileInspectionV16,
		readRelationshipScheduleFunc: func(
			context.Context, PreparedProfile,
		) (store.GenerationScheduleProgress, error) {
			return active, nil
		},
	}

	probe, err := inspector.classifyRelationshipOpenFailure(
		t.Context(), PreparedProfile{}, relationshippublication.ErrPublishing,
	)
	if probe.RelationshipFailureClass != relationshipFailureCurrentControl ||
		classifyConvergenceInspection(err).class != "pending" {
		t.Fatalf("changing current control = %+v, %v", probe, err)
	}
	probe, err = inspector.classifyRelationshipOpenFailure(
		t.Context(), PreparedProfile{}, relationshippublication.ErrInvalid,
	)
	if probe.RelationshipFailureClass != relationshipFailureCurrentControl ||
		!errors.Is(err, errRelationshipTerminal) ||
		classifyConvergenceInspection(err).class != "terminal" {
		t.Fatalf("invalid current control = %+v, %v", probe, err)
	}

	probe, err = inspector.classifyRelationshipMismatch(
		t.Context(), PreparedProfile{}, generationDigest,
		relationshipFailureAuthorityDrift,
	)
	if probe.RelationshipFailureClass != relationshipFailureAuthorityDrift ||
		classifyConvergenceInspection(err).class != "pending" {
		t.Fatalf("active successor mismatch = %+v, %v", probe, err)
	}
	probe, err = inspector.classifyRelationshipMismatch(
		t.Context(), PreparedProfile{}, generationDigest,
		relationshipFailureAuthorityGap,
	)
	if probe.RelationshipFailureClass != relationshipFailureAuthorityGap ||
		classifyConvergenceInspection(err).class != "pending" {
		t.Fatalf("active successor authority gap = %+v, %v", probe, err)
	}

	settled := active
	settled.Status, settled.Running, settled.Succeeded = store.GenerationScheduleSettled, 0, 1
	inspector.readRelationshipScheduleFunc = func(
		context.Context, PreparedProfile,
	) (store.GenerationScheduleProgress, error) {
		return settled, nil
	}
	probe, err = inspector.classifyRelationshipMismatch(
		t.Context(), PreparedProfile{}, generationDigest,
		relationshipFailureAuthorityDrift,
	)
	if probe.RelationshipFailureClass != relationshipFailureAuthorityDrift ||
		!errors.Is(err, errRelationshipTerminal) ||
		classifyConvergenceInspection(err).class != "terminal" {
		t.Fatalf("settled successor mismatch = %+v, %v", probe, err)
	}
	inspector.readRelationshipRootFunc = func(context.Context, PreparedProfile) error {
		return relationshippublication.ErrNotFound
	}
	probe, err = inspector.classifyRelationshipAbsent(t.Context(), PreparedProfile{})
	if probe.RelationshipFailureClass != relationshipFailureSuccessorLost ||
		!errors.Is(err, errRelationshipTerminal) {
		t.Fatalf("settled successor without current root = %+v, %v", probe, err)
	}

	inspector.readRelationshipScheduleFunc = func(
		context.Context, PreparedProfile,
	) (store.GenerationScheduleProgress, error) {
		return store.GenerationScheduleProgress{}, store.ErrNotFound
	}
	probe, err = inspector.classifyRelationshipAbsent(t.Context(), PreparedProfile{})
	if probe.RelationshipFailureClass != relationshipFailureSuccessorAbsent ||
		!errors.Is(err, errRelationshipTerminal) {
		t.Fatalf("absent root and successor = %+v, %v", probe, err)
	}
}

func TestV16RelationshipRootChangeRefreshesExtractionAuthorityOnce(t *testing.T) {
	reads := 0
	inspector := &profileInspector{
		contract: profileInspectionV16,
		inspectExtractionFunc: func(
			context.Context, PreparedProfile,
		) (extractionSnapshot, string, error) {
			reads++
			return extractionSnapshot{generation: fmt.Sprintf("generation-%d", reads)},
				fmt.Sprintf("progress-%d", reads), nil
		},
	}
	probeSHA := "sha256:" + strings.Repeat("c", 64)
	for _, relationshipGeneration := range []string{"", "", "root-a", "root-a", "root-b"} {
		if _, _, err := inspector.extractionForRelationship(
			t.Context(), PreparedProfile{}, probeSHA, relationshipGeneration,
		); err != nil {
			t.Fatal(err)
		}
	}
	if reads != 3 {
		t.Fatalf("extraction scans = %d, want initial plus two relationship-root transitions", reads)
	}
}

func TestSnapshotAuthorityIncludesCallerGeneration(t *testing.T) {
	left := privateProfileSnapshot{CallerGeneration: "sha256:caller-a"}
	right := left
	right.CallerGeneration = "sha256:caller-b"
	if snapshotAuthority(left) == snapshotAuthority(right) ||
		privateSnapshotEqual(left, right) {
		t.Fatal("caller generation change escaped snapshot revalidation")
	}
}

func TestSnapshotRecoveryAuthorityUsesStableRelationshipSemantics(t *testing.T) {
	root := relationshippublication.Root{
		Schema: "relationship-root",
		Authority: relationshippublication.Authority{
			Repository:                  "github.com/acme/repo",
			ServiceStateSetDigest:       "sha256:service-set",
			ServiceStateSummaryDigest:   "sha256:summary-a",
			ServiceStateControlRevision: 2,
		},
		AuthorityDigest:  "sha256:authority-a",
		GenerationDigest: "sha256:generation-a",
		Digest:           "sha256:root-a",
		ServiceCount:     1,
	}
	first, err := relationshipSemanticDigest(root, profileInspectionV21)
	if err != nil {
		t.Fatal(err)
	}
	root.Authority.ServiceStateSummaryDigest = "sha256:summary-b"
	root.Authority.ServiceStateControlRevision = 6
	root.AuthorityDigest = "sha256:authority-b"
	root.GenerationDigest = "sha256:generation-b"
	root.Digest = "sha256:root-b"
	second, err := relationshipSemanticDigest(root, profileInspectionV21)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("service-summary transition fence changed relationship semantic identity")
	}
	root.Authority.ServiceStateSetDigest = "sha256:service-set-b"
	changed, err := relationshipSemanticDigest(root, profileInspectionV21)
	if err != nil {
		t.Fatal(err)
	}
	if changed == first {
		t.Fatal("relationship content change escaped semantic identity")
	}

	// V2 roots embed the full upstream authority. Extraction run identity and
	// its provenance digest are exact provenance, not authority: interruption
	// restart re-mints RunIDs for byte-equivalent content (the n30 class), and
	// the recovery comparator must not re-key on them.
	root.Authority.Upstream = &downstreamauthority.Authority{
		Schema:     downstreamauthority.Schema,
		Repository: "github.com/acme/repo",
		Domains: []candidate.DownstreamDomainAuthority{{
			Domain: "go", Version: "v1", RootDigest: "sha256:domain-root-a",
			RunID: "run-a",
		}},
		ProvenanceDigest: "sha256:provenance-a",
		Digest:           "sha256:upstream-semantic",
	}
	withUpstream, err := relationshipSemanticDigest(root, profileInspectionV21)
	if err != nil {
		t.Fatal(err)
	}
	root.Authority.Upstream.Domains[0].RunID = "run-b"
	root.Authority.Upstream.ProvenanceDigest = "sha256:provenance-b"
	reminted, err := relationshipSemanticDigest(root, profileInspectionV21)
	if err != nil {
		t.Fatal(err)
	}
	if reminted != withUpstream {
		t.Fatal("upstream run provenance re-keyed relationship semantic identity")
	}
	if root.Authority.Upstream.Domains[0].RunID != "run-b" ||
		root.Authority.Upstream.ProvenanceDigest != "sha256:provenance-b" {
		t.Fatal("relationshipSemanticDigest mutated the caller's upstream authority")
	}
	root.Authority.Upstream.Domains[0].RootDigest = "sha256:domain-root-b"
	upstreamChanged, err := relationshipSemanticDigest(root, profileInspectionV21)
	if err != nil {
		t.Fatal(err)
	}
	if upstreamChanged == withUpstream {
		t.Fatal("upstream content change escaped semantic identity")
	}
	// Component transition digests hash the embedded upstream whole, so they
	// re-key on the same restart re-mints; V21 excludes them from semantic
	// identity while V20 keeps the frozen historical derivation.
	root.Authority.ResolverGenerationDigest = "sha256:resolver-generation-b"
	root.Authority.ResolverRootDigest = "sha256:resolver-root-b"
	root.Authority.RPCGenerationDigest = "sha256:rpc-generation-b"
	root.Authority.RPCRootDigest = "sha256:rpc-root-b"
	root.Authority.KafkaGenerationDigest = "sha256:kafka-generation-b"
	root.Authority.KafkaRootDigest = "sha256:kafka-root-b"
	componentShift, err := relationshipSemanticDigest(root, profileInspectionV21)
	if err != nil {
		t.Fatal(err)
	}
	if componentShift != upstreamChanged {
		t.Fatal("component transition digests re-keyed v21 relationship semantic identity")
	}
	v20First, err := relationshipSemanticDigest(root, profileInspectionV20)
	if err != nil {
		t.Fatal(err)
	}
	root.Authority.ResolverRootDigest = "sha256:resolver-root-c"
	v20Shift, err := relationshipSemanticDigest(root, profileInspectionV20)
	if err != nil {
		t.Fatal(err)
	}
	if v20Shift == v20First {
		t.Fatal("v20 historical derivation lost component-digest sensitivity")
	}
	root.Authority.Upstream.Domains[0].RunID = "run-c"
	v20RunShift, err := relationshipSemanticDigest(root, profileInspectionV20)
	if err != nil {
		t.Fatal(err)
	}
	if v20RunShift == v20Shift {
		t.Fatal("v20 historical derivation stopped hashing upstream run identity")
	}
	root.Authority.Upstream = nil

	left := privateProfileSnapshot{
		RelationshipGeneration:     "sha256:generation-a",
		RelationshipRootDigest:     "sha256:root-a",
		RelationshipSemanticDigest: first,
	}
	right := left
	right.RelationshipGeneration = "sha256:generation-b"
	right.RelationshipRootDigest = "sha256:root-b"
	if snapshotRecoveryAuthority(left) != snapshotRecoveryAuthority(right) {
		t.Fatal("relationship transition identity changed recovery authority")
	}
	right.RelationshipSemanticDigest = changed
	if snapshotRecoveryAuthority(left) == snapshotRecoveryAuthority(right) {
		t.Fatal("relationship semantic change escaped recovery authority")
	}
}

func TestRestoreProductParityExcludesRebuiltControlIdentities(t *testing.T) {
	left := privateProfileSnapshot{
		SourceGeneration: "source", SearchGeneration: "search",
		ObservationGeneration: "observation", ExtractionGeneration: "extraction",
		CallerGeneration: "caller-a", RelationshipGeneration: "relationship-a",
		RelationshipRootDigest: "root-a", RelationshipSemanticDigest: "semantic-a",
		ExtractionFacts: 3, RelationshipPublished: true,
	}
	right := left
	right.CallerGeneration = "caller-b"
	right.RelationshipGeneration = "relationship-b"
	right.RelationshipRootDigest = "root-b"
	right.RelationshipSemanticDigest = "semantic-b"
	if !privateRestoreProductEqual(left, right) {
		t.Fatal("restore product parity rejected rebuilt control identities")
	}
	right.ExtractionFacts++
	if privateRestoreProductEqual(left, right) {
		t.Fatal("restore product parity accepted changed product content")
	}
}

func TestProfileInspectorReadsDeclarationIndependentCallerProgress(t *testing.T) {
	for _, test := range []struct {
		state     string
		progress  string
		wantClass string
	}{
		{
			state: "current", wantClass: "complete",
			progress: `{"state":"complete","settled_pair_count":1,` +
				`"succeeded_pair_count":1,"refused_pair_count":0,"total_pair_count":1}`,
		},
		{
			state: "missing", wantClass: "pending",
			progress: `{"state":"unavailable","settled_pair_count":0,` +
				`"succeeded_pair_count":0,"refused_pair_count":0}`,
		},
		{
			state: "stale", wantClass: "pending",
			progress: `{"state":"unavailable","settled_pair_count":0,` +
				`"succeeded_pair_count":0,"refused_pair_count":0}`,
		},
	} {
		t.Run(test.state, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if request.URL.Path != apiresponse.CallerGenerationProgressPath ||
					request.URL.Query().Get("repository") != "example.invalid/neutral" ||
					request.Header.Get("Authorization") != "Bearer private-test-token" {
					http.NotFound(response, request)
					return
				}
				response.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(response,
					`{"$schema":"http://%s/schemas/CallerGenerationProgress.json",`+
						`"schema_version":"caller-generation-progress-v3","generation":{`+
						`"state":%q,"plane":"repository-overlay",`+
						`"repository":"example.invalid/neutral",`+
						`"generation_digest":"sha256:%s",`+
						`"partition_progress":%s},`+
						`"scope":{"repository":"example.invalid/neutral",`+
						`"commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",`+
						`"scope_posture":"whole-repository"},`+
						`"caller_job_state":"unavailable",`+
						`"resolver_job_state":"unavailable"}`,
					request.Host, test.state, strings.Repeat("c", 64), test.progress,
				)
			}))
			defer server.Close()
			inspector := &profileInspector{
				client: server.Client(), credential: "private-test-token",
				contract: profileInspectionV21,
			}
			probe, err := inspector.callerGenerationTerminal(t.Context(), PreparedProfile{
				Address: server.Listener.Addr().String(), RepositoryName: "example.invalid/neutral",
			})
			if probe.Stage != "caller_generation" || probe.SHA256 == "" ||
				classifyConvergenceInspection(err).class != test.wantClass {
				t.Fatalf("caller generation progress probe = %+v, %v", probe, err)
			}
		})
	}
}

func TestProfileInspectorClassifiesClosedObservationProgressStatuses(t *testing.T) {
	tests := []struct {
		name   string
		status int
		detail string
		reason string
	}{
		{name: "stale", status: http.StatusConflict, detail: apiresponse.ObservationProgressDetailStale, reason: httpReason409Stale},
		{name: "control absent", status: http.StatusConflict, detail: apiresponse.ObservationProgressDetailControlAbsent, reason: httpReason409ControlAbsent},
		{name: "store", status: http.StatusInternalServerError, detail: apiresponse.ObservationProgressDetailStore, reason: httpReason500Store},
		{name: "projection response", status: http.StatusInternalServerError, detail: apiresponse.ObservationProgressDetailProjection, reason: httpReason500Response},
		{name: "projection control", status: http.StatusInternalServerError, detail: apiresponse.ObservationProgressDetailControl, reason: httpReason500Control},
		{name: "projection publication", status: http.StatusInternalServerError, detail: apiresponse.ObservationProgressDetailPublication, reason: httpReason500Publication},
		{name: "projection planning", status: http.StatusInternalServerError, detail: apiresponse.ObservationProgressDetailPlanning, reason: httpReason500Planning},
		{name: "projection schedule", status: http.StatusInternalServerError, detail: apiresponse.ObservationProgressDetailSchedule, reason: httpReason500Schedule},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				response.Header().Set("Content-Type", "application/problem+json")
				response.WriteHeader(test.status)
				_, _ = fmt.Fprintf(response,
					`{"$schema":"http://%s/schemas/ErrorModel.json","title":"closed","status":%d,"detail":%q}`,
					request.Host, test.status, test.detail,
				)
			}))
			defer server.Close()
			inspector := &profileInspector{client: server.Client(), credential: "private-test-token"}
			profile := PreparedProfile{Address: strings.TrimPrefix(server.URL, "http://")}
			var target struct{}
			err := inspector.get(t.Context(), profile, "/api/observation-progress", &target)
			var statusErr *privateHTTPStatusError
			if !errors.As(err, &statusErr) || statusErr.Status != test.status || statusErr.Reason != test.reason {
				t.Fatalf("status error = %+v, %v", statusErr, err)
			}
			diagnostic := classifyConvergenceInspection(err)
			if diagnostic.class != "status" || diagnostic.httpStatus != test.status ||
				diagnostic.httpReason != test.reason {
				t.Fatalf("diagnostic = %+v", diagnostic)
			}
		})
	}
}

func TestProfileInspectorPreservesHistoricalResponseErrors(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantText string
	}{
		{
			name: "oversized", body: strings.Repeat("x", MaxObservationBytes+1),
			wantText: "T40.13 HTTP response exceeds its bound",
		},
		{name: "invalid", body: "{", wantText: "T40.13 HTTP response is invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(response, test.body)
			}))
			defer server.Close()
			inspector := &profileInspector{client: server.Client(), credential: "private-test-token"}
			profile := PreparedProfile{Address: strings.TrimPrefix(server.URL, "http://")}
			var target struct{}
			err := inspector.get(t.Context(), profile, "/bounded", &target)
			if err == nil || err.Error() != test.wantText || !errors.Is(err, errHTTPResponse) {
				t.Fatalf("response error = %v, want %q wrapping response sentinel", err, test.wantText)
			}
		})
	}
}

func TestObservationProgressTerminalClassificationIsImmediateAndClosed(t *testing.T) {
	limit := pipelinerefusal.Receipt{
		Schema:         pipelinerefusal.Schema,
		Stage:          pipelinerefusal.StageObservationPublication,
		GenerationKind: pipelinerefusal.GenerationObservation,
		Classification: pipelinerefusal.ClassificationLimit,
		Dimension:      pipelinerefusal.DimensionGenerationRecords,
		Observed:       262_144, Limit: 250_000,
	}
	tests := []struct {
		name     string
		progress observationpublication.Progress
		want     error
	}{
		{name: "building", progress: observationpublication.Progress{
			State: "building", Planning: &observationpublication.PlanningProgress{State: "active"},
		}},
		{name: "closed limit", progress: observationpublication.Progress{
			State: "failed", Planning: &observationpublication.PlanningProgress{State: "failed", Refusal: &limit},
		}, want: errObservationBoundRefusal},
		{name: "unrelated limit remains unclassified", progress: observationpublication.Progress{
			State: "failed", Planning: &observationpublication.PlanningProgress{State: "failed", Refusal: &pipelinerefusal.Receipt{
				Schema: pipelinerefusal.Schema, Stage: pipelinerefusal.StageObservationPublication,
				GenerationKind: pipelinerefusal.GenerationObservation,
				Classification: pipelinerefusal.ClassificationLimit,
				Dimension:      pipelinerefusal.DimensionGenerationEncodedBytes,
				Observed:       2, Limit: 1,
			}},
		}, want: errObservationTerminal},
		{name: "wrong frozen limit remains unclassified", progress: observationpublication.Progress{
			State: "failed", Planning: &observationpublication.PlanningProgress{State: "failed", Refusal: &pipelinerefusal.Receipt{
				Schema: pipelinerefusal.Schema, Stage: pipelinerefusal.StageObservationPublication,
				GenerationKind: pipelinerefusal.GenerationObservation,
				Classification: pipelinerefusal.ClassificationLimit,
				Dimension:      pipelinerefusal.DimensionGenerationRecords,
				Observed:       262_144, Limit: 249_999,
			}},
		}, want: errObservationTerminal},
		{name: "terminal without limit", progress: observationpublication.Progress{
			State: "failed", Planning: &observationpublication.PlanningProgress{State: "failed"},
		}, want: errObservationTerminal},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := observationTerminal(test.progress)
			if !errors.Is(err, test.want) {
				t.Fatalf("terminal = %v, want %v", err, test.want)
			}
			wantClass := "complete"
			if test.want != nil {
				wantClass = "terminal"
			}
			if got := classifyConvergenceInspection(err).class; got != wantClass {
				t.Fatalf("class = %q, want %q", got, wantClass)
			}
		})
	}
}

type humaResponseStore struct {
	store.Store
	repositories []store.Repo
	statuses     []store.RepoStatus
}

func (value humaResponseStore) ListRepos(context.Context) ([]store.Repo, error) {
	return append([]store.Repo(nil), value.repositories...), nil
}

func (value humaResponseStore) RepoStatuses(context.Context) ([]store.RepoStatus, error) {
	return append([]store.RepoStatus(nil), value.statuses...), nil
}

func TestProfileInspectorConsumesRealHumaObjectAndArrayResponses(t *testing.T) {
	handler := apiresponse.New(apiresponse.Options{
		Version: "test", APIKey: "private-test-token",
		Store: humaResponseStore{
			repositories: []store.Repo{{Name: "example/repository"}},
			statuses: []store.RepoStatus{{
				Repo:              store.Repo{Name: "example/repository"},
				LastIndexJobState: store.JobProjectionExact,
				LastIndexJob:      &store.Job{Status: store.StatusRunning, Attempts: 2},
			}},
		},
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	address := strings.TrimPrefix(server.URL, "http://")
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/api/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer private-test-token")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	raw, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		t.Fatal(readErr, closeErr)
	}
	var directHealth struct {
		Status string `json:"status"`
	}
	if err := decodeHumaResponse(raw, address, &directHealth); err != nil {
		t.Fatalf("decode real Huma health %q: %v", raw, err)
	}
	inspector := &profileInspector{client: server.Client(), credential: "private-test-token"}
	profile := PreparedProfile{Address: address}
	class, err := inspector.healthClass(t.Context(), profile)
	if err != nil || class != "ok" {
		t.Fatalf("real Huma health = %q, %v", class, err)
	}
	var repositories []store.Repo
	if err := inspector.get(t.Context(), profile, "/api/repos", &repositories); err != nil {
		t.Fatal(err)
	}
	if len(repositories) != 1 || repositories[0].Name != "example/repository" {
		t.Fatalf("real Huma repositories = %+v", repositories)
	}
	status, err := inspector.currentRepositoryStatus(t.Context(), PreparedProfile{
		Address: address, RepositoryName: "example/repository",
	})
	if err != nil || status.LastIndexJobState != store.JobProjectionExact ||
		status.LastIndexJob == nil || status.LastIndexJob.Status != store.StatusRunning ||
		status.LastIndexJob.Attempts != 2 {
		t.Fatalf("real Huma repository status = %+v, %v", status, err)
	}
}

func TestRepositoryIndexProbeUsesClosedLatestJobProjection(t *testing.T) {
	expected := strings.Repeat("a", 40)
	tests := []struct {
		name       string
		status     store.RepoStatus
		wantClass  string
		wantClosed bool
	}{
		{name: "projection unavailable", status: store.RepoStatus{
			Repo: store.Repo{}, LastIndexJobState: store.JobProjectionUnavailable,
		}, wantClass: "pending"},
		{name: "pending", status: projectedIndexStatus(store.StatusPending, 0), wantClass: "pending"},
		{name: "claimed", status: projectedIndexStatus(store.StatusClaimed, 1), wantClass: "pending"},
		{name: "running retry", status: projectedIndexStatus(store.StatusRunning, 2), wantClass: "pending"},
		{name: "done before expected publication", status: projectedIndexStatus(store.StatusDone, 1), wantClass: "pending"},
		{name: "failed", status: projectedIndexStatus(store.StatusFailed, 3), wantClass: "terminal", wantClosed: true},
		{name: "canceled", status: projectedIndexStatus(store.StatusCanceled, 1), wantClass: "terminal", wantClosed: true},
		{name: "exact projection without job", status: store.RepoStatus{
			LastIndexJobState: store.JobProjectionExact,
		}, wantClass: "control"},
		{name: "unknown projection state", status: store.RepoStatus{
			LastIndexJobState: store.JobProjectionState("unknown"),
		}, wantClass: "control"},
		{name: "published commit wins", status: func() store.RepoStatus {
			value := projectedIndexStatus(store.StatusFailed, 3)
			value.IndexedCommitHash = expected
			return value
		}(), wantClass: "complete"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			probe, err := repositoryIndexProbe(test.status, expected)
			if probe.Stage != "repository_index" || !digestIdentity(probe.SHA256) {
				t.Fatalf("probe = %+v", probe)
			}
			if got := classifyConvergenceInspection(err).class; got != test.wantClass {
				t.Fatalf("class = %q, want %q; err=%v", got, test.wantClass, err)
			}
			if errors.Is(err, errRepositoryIndexTerminal) != test.wantClosed {
				t.Fatalf("terminal = %t, want %t; err=%v",
					errors.Is(err, errRepositoryIndexTerminal), test.wantClosed, err)
			}
		})
	}

	first := projectedIndexStatus(store.StatusFailed, 3)
	first.LastIndexJob.Error = "private first failure"
	second := projectedIndexStatus(store.StatusFailed, 3)
	second.LastIndexJob.Error = "different private failure"
	firstProbe, _ := repositoryIndexProbe(first, expected)
	secondProbe, _ := repositoryIndexProbe(second, expected)
	if firstProbe.SHA256 != secondProbe.SHA256 {
		t.Fatal("repository-index probe retained a raw job error")
	}
	first.LastIndexJob.Error = "heartbeat: context deadline exceeded"
	second.LastIndexJob.Error = "heartbeat: private store detail changed"
	firstProbe, _ = repositoryIndexProbe(first, expected)
	secondProbe, _ = repositoryIndexProbe(second, expected)
	if firstProbe.RepositoryIndexFailureClass != "lease_heartbeat" ||
		secondProbe.RepositoryIndexFailureClass != "lease_heartbeat" ||
		firstProbe.SHA256 != secondProbe.SHA256 {
		t.Fatalf("closed heartbeat classes = %+v / %+v", firstProbe, secondProbe)
	}
	second.LastIndexJob.Error = "different private child failure"
	secondProbe, _ = repositoryIndexProbe(second, expected)
	if secondProbe.RepositoryIndexFailureClass != "other" || firstProbe.SHA256 == secondProbe.SHA256 {
		t.Fatalf("closed other class = %+v", secondProbe)
	}
}

func projectedIndexStatus(status store.JobStatus, attempts int) store.RepoStatus {
	return store.RepoStatus{
		LastIndexJobState: store.JobProjectionExact,
		LastIndexJob:      &store.Job{Status: status, Attempts: attempts},
	}
}

func TestHumaResponseDecoderCoversEveryCeremonyObjectTarget(t *testing.T) {
	const address = "127.0.0.1:41731"
	tests := []struct {
		name   string
		target func() any
	}{
		{name: "health", target: func() any {
			return &struct {
				Status string `json:"status"`
			}{}
		}},
		{name: "observation progress", target: func() any { return &observationpublication.Progress{} }},
		{name: "extraction progress", target: func() any { return &extractionpublication.Progress{} }},
		{name: "lifecycle status", target: func() any { return &lifecycle.Status{} }},
		{name: "search", target: func() any { return &search.Result{} }},
		{name: "service inventory", target: func() any { return &apiresponse.ServiceInventory{} }},
		{name: "relationships", target: func() any { return &apiresponse.RelationshipPage{} }},
		{name: "citation", target: func() any { return &apiresponse.RelationshipCitation{} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := []byte(`{"$schema":"http://127.0.0.1:41731/schemas/CeremonyResponse.json"}`)
			if err := decodeHumaResponse(raw, address, test.target()); err != nil {
				t.Fatal(err)
			}
		})
	}
	var repositories []store.Repo
	if err := decodeHumaResponse([]byte(`[]`), address, &repositories); err != nil {
		t.Fatalf("top-level repository array: %v", err)
	}
}

func TestExtractionConvergenceProbeClosesTerminalRefusal(t *testing.T) {
	progress := extractionpublication.Progress{
		State: "unavailable",
	}
	refusal := pipelinerefusal.Receipt{
		Schema:         pipelinerefusal.Schema,
		Stage:          pipelinerefusal.StageDomainInventory,
		GenerationKind: pipelinerefusal.GenerationExtractionDomain,
		Classification: pipelinerefusal.ClassificationLimit,
		Dimension:      pipelinerefusal.DimensionCandidateMemberBytes,
		Observed:       792_000_000, Limit: 67_108_864,
	}
	repository := store.RepoStatus{
		LastExtractionJobState: store.JobProjectionExact,
		LastExtractionJob: &store.ExtractionJobProjection{
			Status: store.StatusFailed, Attempts: 1, Refusal: &refusal,
		},
	}
	probe, err := extractionConvergenceProbe(progress, repository, profileInspectionLegacy)
	if !errors.Is(err, errExtractionBoundRefusal) || probe.ExtractionProgress == nil ||
		probe.ExtractionProgress.RefusalDimension != "candidate_member_bytes" ||
		probe.ExtractionProgress.RefusalObserved != 792_000_000 ||
		probe.ExtractionProgress.RefusalLimit != 67_108_864 ||
		classifyConvergenceInspection(err).class != "terminal" {
		t.Fatalf("probe = %+v, error=%v", probe, err)
	}
	repository.LastExtractionJob.Refusal = nil
	_, err = extractionConvergenceProbe(progress, repository, profileInspectionLegacy)
	if !errors.Is(err, errExtractionJobTerminal) {
		t.Fatalf("ordinary terminal error = %v", err)
	}
	repository.LastExtractionJob.Refusal = &pipelinerefusal.Receipt{
		Schema: pipelinerefusal.Schema, Stage: pipelinerefusal.StageObservationPublication,
		GenerationKind: pipelinerefusal.GenerationObservation,
		Classification: pipelinerefusal.ClassificationLimit,
		Dimension:      pipelinerefusal.DimensionGenerationRecords, Observed: 2, Limit: 1,
	}
	_, err = extractionConvergenceProbe(progress, repository, profileInspectionLegacy)
	if !errors.Is(err, errExtractionJobTerminal) {
		t.Fatalf("unrelated limit error = %v", err)
	}
}

func TestExtractionConvergenceProbeRetainsTypedPendingProjection(t *testing.T) {
	progress := extractionpublication.Progress{
		State: "active", Total: 489, Materialized: 489,
		Pending: 460, Running: 4, Succeeded: 25, Domains: 4,
	}
	repository := store.RepoStatus{LastExtractionJobState: store.JobProjectionUnavailable}
	probe, err := extractionConvergenceProbe(progress, repository, profileInspectionLegacy)
	if err == nil || classifyConvergenceInspection(err).class != "pending" ||
		probe.ExtractionProgress == nil || probe.ExtractionProgress.Succeeded != 25 ||
		probe.ExtractionProgress.Running != 4 || probe.ExtractionProgress.Total != 489 {
		t.Fatalf("probe = %+v, error=%v", probe, err)
	}
}

func TestExtractionConvergenceProbeStopsOnlySettledFailedSchedule(t *testing.T) {
	repository := store.RepoStatus{
		LastExtractionJobState: store.JobProjectionExact,
		LastExtractionJob: &store.ExtractionJobProjection{
			Status: store.StatusDone, Attempts: 1,
		},
	}
	settled := extractionpublication.Progress{
		State: string(store.GenerationScheduleSettled), Total: 32, Materialized: 32,
		Succeeded: 0, Failed: 32, Domains: 4,
	}
	probe, err := extractionConvergenceProbe(settled, repository, profileInspectionLegacy)
	if !errors.Is(err, errExtractionScheduleTerminal) ||
		classifyConvergenceInspection(err).class != "terminal" ||
		probe.ExtractionProgress == nil || probe.ExtractionProgress.Failed != 32 {
		t.Fatalf("settled probe = %+v, error=%v", probe, err)
	}
	active := settled
	active.State, active.Pending = "active", 1
	active.Failed, active.Total = 31, 32
	if _, err := extractionConvergenceProbe(active, repository, profileInspectionLegacy); errors.Is(err, errExtractionScheduleTerminal) {
		t.Fatalf("active schedule stopped terminally: %v", err)
	}
	short := settled
	short.Failed = 31
	if probe, err := extractionConvergenceProbe(short, repository, profileInspectionLegacy); err != nil ||
		probe.ExtractionProgress == nil || probe.ExtractionProgress.Failed != 31 {
		t.Fatalf("incomplete settled counters = %+v, error=%v", probe, err)
	}
	repository.LastExtractionJob.Status = store.StatusFailed
	if _, err := extractionConvergenceProbe(settled, repository, profileInspectionLegacy); !errors.Is(err, errExtractionJobTerminal) {
		t.Fatalf("v14 job terminal did not retain historical precedence: %v", err)
	}
	if _, err := extractionConvergenceProbe(settled, repository, profileInspectionV15); !errors.Is(
		err, errExtractionScheduleTerminal,
	) {
		t.Fatalf("v15 generation-bound schedule did not retain precedence: %v", err)
	}
	repository.LastExtractionJobState, repository.LastExtractionJob = store.JobProjectionUnavailable, nil
	probe, err = extractionConvergenceProbe(settled, repository, profileInspectionLegacy)
	if !errors.Is(err, errExtractionScheduleTerminal) || probe.ExtractionProgress.JobState != "" ||
		validateExtractionProgress(*probe.ExtractionProgress) != nil {
		t.Fatalf("collected-job settled probe = %+v, error=%v", probe, err)
	}
}

func TestExtractionConvergenceProbeAcceptsCurrentAuthorityAfterJobCollection(t *testing.T) {
	progress := extractionpublication.Progress{
		State: "current", Total: 489, Materialized: 489,
		Succeeded: 489, Domains: 4, CurrentDomains: 4,
	}
	repository := store.RepoStatus{LastExtractionJobState: store.JobProjectionUnavailable}
	probe, err := extractionConvergenceProbe(progress, repository, profileInspectionLegacy)
	if err != nil || probe.ExtractionProgress == nil ||
		probe.ExtractionProgress.State != "current" ||
		probe.ExtractionProgress.Succeeded != 489 {
		t.Fatalf("probe = %+v, error=%v", probe, err)
	}
}

func TestExtractionConvergenceProbeDoesNotLetUnboundFailedJobPoisonCurrentSchedule(t *testing.T) {
	progress := extractionpublication.Progress{
		State: "current", Total: 1956, Materialized: 1956,
		Succeeded: 1956, Domains: 9, CurrentDomains: 9,
	}
	repository := store.RepoStatus{
		LastExtractionJobState: store.JobProjectionExact,
		LastExtractionJob: &store.ExtractionJobProjection{
			Status: store.StatusFailed, Attempts: 2,
		},
	}
	historicalProbe, historicalErr := extractionConvergenceProbe(progress, repository, profileInspectionLegacy)
	if !errors.Is(historicalErr, errExtractionJobTerminal) ||
		historicalProbe.SHA256 != "sha256:d4703c2d327d13d0116fb2795774cb3caf21c8d98ac8c91ab163777bf7c05600" {
		t.Fatalf("v14 historical probe = %+v, error=%v", historicalProbe, historicalErr)
	}
	probe, err := extractionConvergenceProbe(progress, repository, profileInspectionV15)
	if err != nil || probe.ExtractionProgress == nil ||
		probe.SHA256 != "sha256:d4703c2d327d13d0116fb2795774cb3caf21c8d98ac8c91ab163777bf7c05600" ||
		probe.ExtractionProgress.State != "current" ||
		probe.ExtractionProgress.JobState != string(store.StatusFailed) ||
		probe.ExtractionProgress.Succeeded != 1956 {
		t.Fatalf("current schedule probe = %+v, error=%v", probe, err)
	}
	v16Probe, v16Err := extractionConvergenceProbe(progress, repository, profileInspectionV16)
	if v16Err != nil || v16Probe.SHA256 != probe.SHA256 ||
		v16Probe.ExtractionProgress == nil || v16Probe.ExtractionProgress.Succeeded != 1956 {
		t.Fatalf("v16 did not inherit current extraction authority = %+v, %v", v16Probe, v16Err)
	}

	active := progress
	active.State, active.Pending, active.Succeeded = "active", 1, 1955
	if _, err := extractionConvergenceProbe(active, repository, profileInspectionV15); err == nil ||
		classifyConvergenceInspection(err).class != "pending" {
		t.Fatalf("active schedule with unbound failed job = %v, want pending", err)
	}

	// V15: with no live actor left (settled successful awaiting promotion,
	// superseded, or no schedule at all) the terminal job row is conclusive
	// rather than pending to the ceremony deadline.
	for _, state := range []string{
		string(store.GenerationScheduleSettled),
		string(store.GenerationScheduleSuperseded),
		"unavailable",
	} {
		dead := progress
		dead.State, dead.CurrentDomains = state, 0
		if _, err := extractionConvergenceProbe(dead, repository, profileInspectionV15); !errors.Is(
			err, errExtractionJobTerminal,
		) {
			t.Fatalf("%s schedule beside a terminal job = %v, want job terminal", state, err)
		}
	}

	// The job row's refusal payload follows the same rule as its failure:
	// conclusive only when no live or converged schedule contradicts it.
	refusalRepository := store.RepoStatus{
		LastExtractionJobState: store.JobProjectionExact,
		LastExtractionJob: &store.ExtractionJobProjection{
			Status: store.StatusFailed, Attempts: 2,
			Refusal: &pipelinerefusal.Receipt{
				Schema:         pipelinerefusal.Schema,
				Stage:          pipelinerefusal.StageDomainInventory,
				GenerationKind: pipelinerefusal.GenerationExtractionDomain,
				Classification: pipelinerefusal.ClassificationLimit,
				Dimension:      pipelinerefusal.DimensionCandidateMemberBytes,
				Observed:       2, Limit: 1,
			},
		},
	}
	if _, err := extractionConvergenceProbe(progress, refusalRepository, profileInspectionV15); err != nil {
		t.Fatalf("typed refusal beside a current schedule = %v, want convergence", err)
	}
	deadRefusal := extractionpublication.Progress{State: "unavailable"}
	if _, err := extractionConvergenceProbe(deadRefusal, refusalRepository, profileInspectionV15); !errors.Is(
		err, errExtractionBoundRefusal,
	) {
		t.Fatalf("conclusive typed refusal = %v, want bound refusal", err)
	}

	unavailable := extractionpublication.Progress{State: "unavailable"}
	if _, err := extractionConvergenceProbe(unavailable, repository, profileInspectionLegacy); !errors.Is(
		err, errExtractionJobTerminal,
	) {
		t.Fatalf("failed job without schedule = %v, want extraction terminal", err)
	}
}

func TestHumaResponseDecoderKeepsSchemaAndApplicationFieldsFailClosed(t *testing.T) {
	const address = "127.0.0.1:41731"
	validSchema := `"$schema":"http://127.0.0.1:41731/schemas/HealthOutBody.json"`
	tests := []struct {
		name string
		raw  string
	}{
		{name: "missing schema", raw: `{"status":"ok"}`},
		{name: "duplicate schema", raw: `{` + validSchema + `,` + validSchema + `,"status":"ok"}`},
		{name: "wrong host", raw: `{"$schema":"http://127.0.0.1:41732/schemas/HealthOutBody.json","status":"ok"}`},
		{name: "wrong scheme", raw: `{"$schema":"https://127.0.0.1:41731/schemas/HealthOutBody.json","status":"ok"}`},
		{name: "nested schema path", raw: `{"$schema":"http://127.0.0.1:41731/schemas/private/Health.json","status":"ok"}`},
		{name: "unknown application field", raw: `{` + validSchema + `,"status":"ok","extra":true}`},
		{name: "duplicate application field", raw: `{` + validSchema + `,"status":"ok","status":"ok"}`},
		{name: "trailing value", raw: `{` + validSchema + `,"status":"ok"} {}`},
		{name: "primitive", raw: `true`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var target struct {
				Status string `json:"status"`
			}
			if err := decodeHumaResponse([]byte(test.raw), address, &target); err == nil {
				t.Fatal("invalid Huma response passed")
			}
		})
	}
}

// V21 reads the repository-keyed caller-leaf job beside the caller progress
// page: an active job holds the missing-publication terminal (the publisher
// is in a requeue backoff window), and a settled-failed job makes a partial
// caller generation a typed terminal instead of pending to the wall deadline.
func TestClassifyCallerGenerationV21JobProjection(t *testing.T) {
	total := 8
	complete := apiresponse.CallerMapPage{Generation: &apiresponse.CallerMapGeneration{
		State: "missing", PartitionProgress: &apiresponse.CallerMapPartitionProgress{
			State: "complete", SettledPairCount: total,
			SucceededPairCount: total, TotalPairCount: &total,
		},
	}}
	partial := apiresponse.CallerMapPage{Generation: &apiresponse.CallerMapGeneration{
		State: "missing", PartitionProgress: &apiresponse.CallerMapPartitionProgress{
			State: "partial", SettledPairCount: 3,
			SucceededPairCount: 3, TotalPairCount: &total,
		},
	}}
	running := callerJobProbe{State: store.JobProjectionExact,
		Job:           &store.CallerJobProjection{Status: store.StatusRunning, Attempts: 1},
		ResolverState: store.JobProjectionUnavailable}
	dead := callerJobProbe{State: store.JobProjectionExact,
		Job:           &store.CallerJobProjection{Status: store.StatusFailed, Attempts: 3},
		ResolverState: store.JobProjectionUnavailable}
	absent := callerJobProbe{
		State: store.JobProjectionUnavailable, ResolverState: store.JobProjectionUnavailable,
	}
	resolverRunning := callerJobProbe{
		State: store.JobProjectionUnavailable, ResolverState: store.JobProjectionExact,
		ResolverJob: &store.ResolverJobProjection{Status: store.StatusRunning, Attempts: 1},
	}
	resolverDead := callerJobProbe{
		State:         store.JobProjectionExact,
		Job:           &store.CallerJobProjection{Status: store.StatusDone, Attempts: 1},
		ResolverState: store.JobProjectionExact,
		ResolverJob:   &store.ResolverJobProjection{Status: store.StatusFailed, Attempts: 3},
	}
	resolverDeadCallerRunning := callerJobProbe{
		State:         store.JobProjectionExact,
		Job:           &store.CallerJobProjection{Status: store.StatusRunning, Attempts: 1},
		ResolverState: store.JobProjectionExact,
		ResolverJob:   &store.ResolverJobProjection{Status: store.StatusFailed, Attempts: 3},
	}

	tests := []struct {
		name             string
		page             apiresponse.CallerMapPage
		job              callerJobProbe
		terminal         bool
		jobState         string
		resolverJobState string
	}{
		{name: "complete with live job holds terminal", page: complete, job: running, jobState: "running"},
		{name: "complete with absent job is terminal", page: complete, job: absent, terminal: true},
		{name: "complete with dead job is terminal", page: complete, job: dead, terminal: true, jobState: "failed"},
		{name: "partial with dead job is terminal", page: partial, job: dead, terminal: true, jobState: "failed"},
		{name: "partial with live job is pending", page: partial, job: running, jobState: "running"},
		{name: "partial with absent job is pending", page: partial, job: absent},
		{name: "complete with live resolver holds terminal", page: complete, job: resolverRunning,
			resolverJobState: "running"},
		{name: "partial with dead resolver is terminal", page: partial, job: resolverDead,
			terminal: true, jobState: "done", resolverJobState: "failed"},
		{name: "partial with live resolver is pending", page: partial, job: resolverRunning,
			resolverJobState: "running"},
		{name: "dead resolver behind live caller job is held", page: partial,
			job: resolverDeadCallerRunning, jobState: "running", resolverJobState: "failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			probe, err := classifyCallerGeneration(test.page, test.job, profileInspectionV21)
			if got := errors.Is(err, errCallerPublicationMissing); got != test.terminal {
				t.Fatalf("terminal = %v (%v), want %v", got, err, test.terminal)
			}
			if probe.CallerProgress == nil || probe.CallerProgress.JobState != test.jobState ||
				probe.CallerProgress.ResolverJobState != test.resolverJobState {
				t.Fatalf("caller progress = %+v, want job state %q / resolver %q",
					probe.CallerProgress, test.jobState, test.resolverJobState)
			}
			if got := expectedCallerGenerationTerminal(probe.CallerProgress); got != test.terminal {
				t.Fatalf("receipt terminal = %v, runtime terminal = %v", got, test.terminal)
			}
			if expectedCallerGenerationBoundRefusal(probe.CallerProgress) {
				t.Fatal("non-refusal caller probe validated as a bound refusal")
			}
		})
	}

	current := apiresponse.CallerMapPage{Generation: &apiresponse.CallerMapGeneration{
		State: "current", GenerationDigest: "sha256:" + strings.Repeat("d", 64),
		PartitionProgress: &apiresponse.CallerMapPartitionProgress{
			State: "complete", SettledPairCount: total,
			SucceededPairCount: total, TotalPairCount: &total,
		},
	}}
	currentProbe, err := classifyCallerGeneration(current, callerJobProbe{
		State:         store.JobProjectionExact,
		Job:           &store.CallerJobProjection{Status: store.StatusDone, Attempts: 1},
		ResolverState: store.JobProjectionUnavailable,
	}, profileInspectionV21)
	if err != nil || expectedCallerGenerationTerminal(currentProbe.CallerProgress) ||
		expectedCallerGenerationBoundRefusal(currentProbe.CallerProgress) {
		t.Fatalf("current caller classifier/receipt mismatch = %+v, %v", currentProbe.CallerProgress, err)
	}

	bound := apiresponse.CallerMapPage{Generation: &apiresponse.CallerMapGeneration{
		State: "failed", PartitionProgress: &apiresponse.CallerMapPartitionProgress{
			State: "complete", SettledPairCount: total, RefusedPairCount: total,
			TotalPairCount: &total, Refusals: []apiresponse.CallerMapRefusalSummary{{
				Stage:          pipelinerefusal.StageCallerGenerationAdmission,
				GenerationKind: pipelinerefusal.GenerationCaller,
				Classification: pipelinerefusal.ClassificationLimit,
				Dimension:      pipelinerefusal.DimensionCallerGenerationAbstentions,
				Observed:       callerleaf.MaxAggregateAbstentionRecords + 1,
				Limit:          callerleaf.MaxAggregateAbstentionRecords, OutcomeCount: total,
			}},
		},
	}}
	boundProbe, err := classifyCallerGeneration(bound, running, profileInspectionV21)
	if !errors.Is(err, errCallerGenerationBoundRefusal) ||
		!expectedCallerGenerationBoundRefusal(boundProbe.CallerProgress) ||
		expectedCallerGenerationTerminal(boundProbe.CallerProgress) {
		t.Fatalf("bound caller classifier/receipt mismatch = %+v, %v", boundProbe.CallerProgress, err)
	}

	// V20 stays byte-exact: the job projection is ignored and the probe digest
	// matches a job-less classification, so sealed V20 evidence cannot drift.
	v20WithJob, err := classifyCallerGeneration(complete, running, profileInspectionV20)
	if !errors.Is(err, errCallerPublicationMissing) {
		t.Fatalf("v20 with job = %v, want terminal", err)
	}
	v20WithoutJob, err := classifyCallerGeneration(complete, callerJobProbe{}, profileInspectionV20)
	if !errors.Is(err, errCallerPublicationMissing) {
		t.Fatalf("v20 without job = %v, want terminal", err)
	}
	if v20WithJob.SHA256 != v20WithoutJob.SHA256 || v20WithJob.CallerProgress != nil {
		t.Fatal("v20 caller probe drifted under the job projection")
	}
	// A V21 job transition perturbs the probe digest, so a requeued publisher
	// can never satisfy the identical-second-probe confirmation.
	v21Running, err := classifyCallerGeneration(complete, running, profileInspectionV21)
	if err == nil || errors.Is(err, errCallerGenerationTerminal) {
		t.Fatalf("v21 live-job probe = %v, want pending", err)
	}
	v21Dead, _ := classifyCallerGeneration(complete, dead, profileInspectionV21)
	if v21Running.SHA256 == v21Dead.SHA256 {
		t.Fatal("v21 probe digest ignores the caller job transition")
	}
}
