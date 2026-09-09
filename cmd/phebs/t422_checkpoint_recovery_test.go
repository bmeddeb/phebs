package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"math"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/store"
)

// Supplied bounded native-shaped data; no old process or filesystem proof.
func t422CheckpointRecoveryFixture() t422CheckpointRecoveryInput {
	digest := t422CheckpointTestDigest
	other := "sha256:" + strings.Repeat("b", 64)
	input := t422CheckpointRecoveryInput{Offset: 27, Prior: t421FinalAuthorityState{
		PhysicalCommit: strings.Repeat("a", 40), PhysicalTree: strings.Repeat("a", 40), Current: true,
		SourceGenerationSHA256: digest, SearchGenerationSHA256: digest, ObservationGenerationSHA256: digest, CandidateGenerationSHA256: digest,
		CatalogRootSHA256: digest, CatalogActivationPlanSHA256: digest, CatalogActivationScheduleSHA256: digest, CatalogActivationUnitSHA256: digest,
		ResolverCatalogGenerationSHA256: digest, ResolverCatalogRootSHA256: digest, CallerGenerationSHA256: digest, CallerRootSHA256: digest,
		RelationshipGenerationSHA256: digest, RelationshipRootSHA256: digest, RelationshipProvenanceSHA256: digest, ExtractionRootsSHA256: digest,
		SearchInventory: t421FinalSetIdentity{Records: 1, FramedBytes: 1, SHA256: digest}, ObservationInputInventory: t421FinalSetIdentity{Records: 1, FramedBytes: 1, SHA256: digest},
	}, Hit: extractionpublication.CheckpointRestartTransition{
		Point: store.GenerationStaleLeaseTransitionCheckpointHit, TargetGeneration: digest, ScheduleGeneration: other,
		PriorScheduleDigest: other, ScheduleDigest: digest, Domain: "proto-contract", Ordinal: 2,
		PlanDigest: digest, ResultIdentity: digest, ResultDigest: digest, ExpectationDigest: digest, PartitionDigest: digest,
		CandidateGenerationDigest: digest, SourceGenerationDigest: digest, ObservationGenerationDigest: digest, ExtractorVersion: "fixture-v1", ExtractionPolicyDigest: digest,
		ScheduleStatus: store.GenerationScheduleActive, Priority: store.GenerationPriorityNeverRun, ChunkStatus: store.GenerationChunkRunning,
		Leased: true, CanonicalResultExists: true, CompletionFileExists: true,
	}}
	for i, domain := range []string{"grpc-caller", "grpc-consumer", "kafka-consumer", "kafka-producer", "proto-contract", "scip-proto-field", "thrift-caller", "thrift-consumer", "thrift-contract"} {
		input.Roots[i] = extractionpublication.RecoveryPreparationRoot{Domain: domain, PlanDigest: digest, RootDigest: digest}
	}
	input.Hit.ChunkIdentity, _ = store.GenerationChunkIdentity(input.Hit.ScheduleDigest, int64(input.Offset), 0)
	return input
}

func t422CheckpointRecoveryEnvelope(t *testing.T, input *t422CheckpointRecoveryInput) ([]byte, dispatchadmission.ProductionSemanticSnapshot) {
	t.Helper()
	raw, snapshot := t422SemanticTestRequest(t)
	var request t422SemanticLaunchRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		t.Fatal(err)
	}
	request.ServerEpoch, request.CheckpointRecovery = 4, input
	raw, _ = json.Marshal(request)
	raw = append(raw, '\n')
	snapshot.ProducerID, snapshot.Phase, snapshot.InputSHA256 = 5, 8, sha256.Sum256(raw)
	return raw, snapshot
}

func TestT422CheckpointRecoveryInput(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*t422CheckpointRecoveryInput)
	}{
		{"exact", func(*t422CheckpointRecoveryInput) {}},
		{"wrong_local", func(v *t422CheckpointRecoveryInput) { v.Hit.Ordinal = 6 }},
		{"wrong_global", func(v *t422CheckpointRecoveryInput) { v.Offset++ }},
		{"aliased_generation", func(v *t422CheckpointRecoveryInput) { v.Hit.TargetGeneration = v.Hit.ScheduleGeneration }},
		{"aliased_schedule", func(v *t422CheckpointRecoveryInput) { v.Hit.PriorScheduleDigest = v.Hit.ScheduleDigest }},
		{"private_token", func(v *t422CheckpointRecoveryInput) { v.Hit.PrivateLeaseTokenDigest = t422CheckpointTestDigest }},
		{"private_checkpoint", func(v *t422CheckpointRecoveryInput) { v.Hit.CheckpointStateDigest = t422CheckpointTestDigest }},
		{"complete_bit", func(v *t422CheckpointRecoveryInput) { v.Hit.CompletionBitSet = true }},
		{"old_root", func(v *t422CheckpointRecoveryInput) { v.Hit.RootExists = true }},
		{"old_pointer", func(v *t422CheckpointRecoveryInput) { v.Hit.Current = true }},
		{"no_result", func(v *t422CheckpointRecoveryInput) { v.Hit.CanonicalResultExists = false }},
		{"retry", func(v *t422CheckpointRecoveryInput) { v.Hit.Attempt = 1 }},
		{"reclaimed", func(v *t422CheckpointRecoveryInput) { v.Hit.Priority = 2 }},
		{"unleased", func(v *t422CheckpointRecoveryInput) { v.Hit.Leased = false }},
		{"mixed_source", func(v *t422CheckpointRecoveryInput) { v.Prior.SourceGenerationSHA256 = v.Hit.ScheduleGeneration }},
		{"invalid_tree", func(v *t422CheckpointRecoveryInput) { v.Prior.PhysicalTree = "tree" }},
		{"unbounded_extractor", func(v *t422CheckpointRecoveryInput) { v.Hit.ExtractorVersion = strings.Repeat("x", 129) }},
		{"missing_root", func(v *t422CheckpointRecoveryInput) { v.Roots[0] = extractionpublication.RecoveryPreparationRoot{} }},
		{"unsorted", func(v *t422CheckpointRecoveryInput) { v.Roots[0], v.Roots[1] = v.Roots[1], v.Roots[0] }},
		{"target_plan", func(v *t422CheckpointRecoveryInput) { v.Roots[4].PlanDigest = v.Hit.ScheduleGeneration }},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := t422CheckpointRecoveryFixture()
			test.mutate(&input)
			if validT422CheckpointRecoveryInput(input) != (test.name == "exact") {
				t.Fatal("input validation")
			}
			if test.name == "private_token" || test.name == "private_checkpoint" {
				return
			} // json:- intentionally does not serialize private fields.
			raw, snapshot := t422CheckpointRecoveryEnvelope(t, &input)
			if _, err := decodeT422SemanticLaunch(raw, snapshot); (err == nil) != (test.name == "exact") {
				t.Fatal("canonical input", err)
			}
		})
	}
}

func TestT422CheckpointRecoveryEnvelopeBindingAndCeiling(t *testing.T) {
	input := t422CheckpointRecoveryFixture()
	for _, mode := range []string{"producer", "phase", "epoch", "unknown", "private", "null", "oversize"} {
		t.Run(mode, func(t *testing.T) {
			raw, snapshot := t422CheckpointRecoveryEnvelope(t, &input)
			switch mode {
			case "producer":
				snapshot.ProducerID = 4
			case "phase":
				snapshot.Phase = 9
			case "epoch":
				raw = bytes.Replace(raw, []byte(`"server_epoch":4`), []byte(`"server_epoch":3`), 1)
			case "unknown":
				raw = bytes.Replace(raw, []byte(`"checkpoint_recovery":{`), []byte(`"checkpoint_recovery":{"verified":true,`), 1)
			case "private":
				raw = bytes.Replace(raw, []byte(`"hit":{`), []byte(`"hit":{"private_lease_token_digest":"hidden",`), 1)
			case "null":
				raw, _ = t422CheckpointRecoveryEnvelope(t, nil)
				raw = bytes.Replace(raw, []byte("}\n"), []byte(",\"checkpoint_recovery\":null}\n"), 1)
			case "oversize":
				raw = append(raw, bytes.Repeat([]byte{' '}, t422SemanticLaunchBytes)...)
			}
			snapshot.InputSHA256 = sha256.Sum256(raw)
			if _, err := decodeT422SemanticLaunch(raw, snapshot); err == nil {
				t.Fatal("unbound input accepted")
			}
		})
	}
	// Maximum widths of every variable-width scalar admitted by this payload.
	input.Hit.ExtractorVersion = strings.Repeat("\x01", 128) // Six JSON bytes per admitted byte.
	for i := range input.Roots {
		if i != 4 {
			input.Roots[i].Domain = string("\x01\x02\x03\x04pqrst"[i]) + strings.Repeat("\x01", 127)
		}
	}
	input.Prior.SearchInventory.Records, input.Prior.SearchInventory.FramedBytes = math.MaxUint64, math.MaxUint64
	input.Prior.ObservationInputInventory.Records, input.Prior.ObservationInputInventory.FramedBytes = math.MaxUint64, math.MaxUint64
	input.Offset = int(store.MaxGenerationItems - 1)
	input.Hit.ChunkIdentity, _ = store.GenerationChunkIdentity(input.Hit.ScheduleDigest, int64(input.Offset), 0)
	raw, snapshot := t422CheckpointRecoveryEnvelope(t, &input)
	var request t422SemanticLaunchRequest
	_ = json.Unmarshal(raw, &request)
	request.Repository = strings.Repeat("\x01", 256)
	raw, _ = json.Marshal(request)
	raw = append(raw, '\n')
	snapshot.InputSHA256 = sha256.Sum256(raw)
	if len(raw) > t422SemanticLaunchBytes {
		t.Fatal("existing input cap exceeded", len(raw))
	}
	if _, err := decodeT422SemanticLaunch(raw, snapshot); err != nil {
		t.Fatal("maximal input", err)
	}
	t.Logf("maximum-width supplied input: %d/%d bytes", len(raw), t422SemanticLaunchBytes)
	oldRaw, oldSnapshot := t422SemanticTestRequest(t)
	launch, err := decodeT422SemanticLaunch(oldRaw, oldSnapshot)
	if err != nil || launch.request.CheckpointRecovery != nil || bytes.Contains(oldRaw, []byte("checkpoint_recovery")) {
		t.Fatal("old omitted input changed", err)
	}
}

func TestT422CheckpointRecoveredNativeIdentity(t *testing.T) {
	input := t422CheckpointRecoveryFixture()
	control := &t422CheckpointRecoveryControl{input: input}
	event := store.GenerationStaleLeaseTransition{PrivateLeaseTokenDigest: store.GenerationLeaseTokenDigest("new-claim")}
	// Native wire shape: current all-success schedule, completed result and root,
	// with the callback's new lease fingerprint, not the cleared store-row token.
	recovered := input.Hit
	recovered.Point, recovered.Priority, recovered.ScheduleStatus, recovered.ChunkStatus, recovered.Leased = store.GenerationStaleLeaseTransitionRecovered, 2, store.GenerationScheduleSettled, store.GenerationChunkDone, false
	recovered.CompletionBitSet, recovered.RootExists, recovered.Current = true, true, true
	recovered.RootDigest = input.Roots[4].RootDigest
	recovered.PrivateLeaseTokenDigest, recovered.CheckpointStateDigest = event.PrivateLeaseTokenDigest, t422CheckpointTestDigest
	if !control.matchesRecovered(recovered, event) {
		t.Fatal("native-shaped recovered refused")
	}
	value := reflect.ValueOf(recovered)
	for i := 0; i < value.NumField(); i++ {
		t.Run(value.Type().Field(i).Name, func(t *testing.T) {
			changed := recovered
			field := reflect.ValueOf(&changed).Elem().Field(i)
			switch field.Kind() {
			case reflect.String:
				field.SetString("changed")
			case reflect.Bool:
				field.SetBool(!field.Bool())
			case reflect.Int:
				field.SetInt(field.Int() + 1)
			default:
				t.Fatal("new unchecked native field")
			}
			if control.matchesRecovered(changed, event) {
				t.Fatal("native identity/state mutation accepted")
			}
		})
	}
	state := t421NewExactReadAccountingState(func([]byte) error { return nil }, func(error) {})
	request := httptest.NewRequest("GET", t422CheckpointRecoveredPath, nil)
	if state.checkpointRecoveredRead(request) != nil {
		t.Fatal("unbound route available")
	}
}
