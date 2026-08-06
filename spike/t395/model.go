// Package t395 retains the source-free release, suspension, and continuation
// decision for T39.5. Production packages must not import spike packages.
package t395

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"

	"github.com/bmeddeb/phebs/spike/t391"
	"github.com/bmeddeb/phebs/spike/t392"
	"github.com/bmeddeb/phebs/spike/t393"
	"github.com/bmeddeb/phebs/spike/t394"
)

const (
	Schema          = "t395-release-suspension-continuation-decision-v1"
	MaxReceiptBytes = 64 << 10

	T391ReceiptSHA256     = "sha256:301d01bccd1ed35b0b539cacdf624cda586e4fbf0267d5c2fe886e097b5501c6"
	T392ReceiptSHA256     = "sha256:20e95fc8e7bf11fb9ae317aa57aeda2966fbf1c866d812f37fadb11aabd26a41"
	T393ReceiptSHA256     = "sha256:8ccfabee2ed675f5539d1921fdce3280a947f22f51eaf94816eb702b8e694051"
	T394ReceiptSHA256     = "sha256:46c039eb6193afc63e94ae28ab1837a09b7870fc48a09292184254c02e863e97"
	ProtoGRPCCardsSHA256  = "sha256:96a7c06e72dfbf1fdbd6bf1c9667b13590724c79eb80af0d80aad8e2b9eb06de"
	ThriftCardsSHA256     = "sha256:68b233f68695399d1550e0cadda109b87e1877b36debf6e157fd6b75820c07db"
	KafkaCardsSHA256      = "sha256:3db269c9601d7384f664bcfcfe211d3c58ad5da79abc404c920fcfa828b85f14"
	RetainedReceiptSHA256 = "sha256:7143aaa34a38a8f14bdaa9d7f695ee65f5b89dcf3f4592ec9f6b4ea64d45677d"
)

type Receipt struct {
	Schema       string               `json:"schema"`
	Outcome      string               `json:"outcome"`
	CompletedOn  string               `json:"completed_on"`
	Inputs       []InputBinding       `json:"inputs"`
	GateSummary  GateSummary          `json:"gate_summary"`
	Packs        []PackDecision       `json:"pack_decisions"`
	Validation   ValidationDecision   `json:"validation_decision"`
	Continuation ContinuationDecision `json:"human_continuation_decision"`
	Lifecycle    LifecycleDecision    `json:"lifecycle"`
	Review       ReviewPolicy         `json:"independent_review"`
	Claims       ReceiptClaims        `json:"claims"`
}

type InputBinding struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	SHA256 string `json:"sha256"`
}

type GateSummary struct {
	NeutralMechanics  string `json:"neutral_mechanics"`
	TargetOperating   string `json:"target_operating"`
	SecurityLifecycle string `json:"security_lifecycle"`
	EvidenceWorkflow  string `json:"evidence_workflow"`
	T392Superseded    bool   `json:"t392_superseded"`
	T394Superseded    bool   `json:"t394_superseded"`
}

type PackDecision struct {
	PackID        string `json:"pack_id"`
	PilotRelevant bool   `json:"pilot_relevant"`
	Status        string `json:"status"`
	Decision      string `json:"decision"`
	Reason        string `json:"reason"`
}

type ValidationDecision struct {
	GateStatus       string `json:"gate_status"`
	ReleaseDecision  string `json:"release_decision"`
	ShadowEligible   bool   `json:"shadow_eligible"`
	AdvisoryEligible bool   `json:"advisory_eligible"`
	ReleasedEligible bool   `json:"released_eligible"`
	MeasuredEnvelope bool   `json:"measured_envelope"`
	MeasuredQuality  bool   `json:"measured_quality"`
	MeasuredWorkflow bool   `json:"measured_workflow"`
	Reason           string `json:"reason"`
}

type ContinuationDecision struct {
	Status                         string `json:"status"`
	Decision                       string `json:"decision"`
	Reason                         string `json:"reason"`
	ActingPrincipal                string `json:"acting_principal"`
	PositiveContinuationAuthorized bool   `json:"positive_continuation_authorized"`
	RerunAuthorized                bool   `json:"rerun_authorized"`
	AuthorizedNextAction           string `json:"authorized_next_action"`
}

type LifecycleDecision struct {
	CurrentStatus            string   `json:"current_status"`
	DefaultDark              bool     `json:"default_dark"`
	SuspensionAppliesNow     bool     `json:"suspension_applies_now"`
	SuspensionReason         string   `json:"suspension_reason"`
	FutureSuspensionTriggers []string `json:"future_suspension_triggers"`
	Expiry                   string   `json:"expiry"`
	Rollback                 string   `json:"rollback"`
	RollbackRewritesHistory  bool     `json:"rollback_rewrites_history"`
	RevalidationRequirements []string `json:"revalidation_requirements"`
}

type ReviewPolicy struct {
	Required                       bool   `json:"required"`
	ImplementerMayApprove          bool   `json:"implementer_may_approve"`
	MergeRequiresIndependentReview bool   `json:"merge_requires_independent_review"`
	ReceiptSelfCertifiesReview     bool   `json:"receipt_self_certifies_review"`
	Authority                      string `json:"authority"`
}

type ReceiptClaims struct {
	SourceFree                    bool `json:"source_free"`
	AuthorizesShadow              bool `json:"authorizes_shadow"`
	AuthorizesAdvisory            bool `json:"authorizes_advisory"`
	AuthorizesRelease             bool `json:"authorizes_release"`
	AuthorizesPilotContinuation   bool `json:"authorizes_pilot_continuation"`
	EstablishesRuntimeCallers     bool `json:"establishes_runtime_callers"`
	EstablishesAccuracy           bool `json:"establishes_accuracy"`
	EstablishesMigrationComplete  bool `json:"establishes_migration_complete"`
	EstablishesDecommissionSafety bool `json:"establishes_decommission_safety"`
	ChangesProductionBehavior     bool `json:"changes_production_behavior"`
}

var inputSpecs = []InputBinding{
	{Path: "spike/t391/results.json", Kind: t391.Schema, SHA256: T391ReceiptSHA256},
	{Path: "spike/t392/results.json", Kind: t392.ReceiptSchema, SHA256: T392ReceiptSHA256},
	{Path: "spike/t393/results.json", Kind: t393.Schema, SHA256: T393ReceiptSHA256},
	{Path: "spike/t394/results.json", Kind: t394.Schema, SHA256: T394ReceiptSHA256},
	{Path: "docs/PROTO_GRPC_PACK_CARDS.md", Kind: "protobuf_grpc_pack_cards", SHA256: ProtoGRPCCardsSHA256},
	{Path: "docs/THRIFT_PACK_CARDS.md", Kind: "thrift_pack_cards", SHA256: ThriftCardsSHA256},
	{Path: "docs/KAFKA_PACK_CARDS.md", Kind: "kafka_pack_cards", SHA256: KafkaCardsSHA256},
}

var packSpecs = []PackDecision{
	{PackID: "phebs.protobuf.contract", PilotRelevant: true, Status: "experimental_dark", Decision: "do_not_release", Reason: "validation_and_operating_gates_stopped"},
	{PackID: "phebs.grpc.consumer.go", PilotRelevant: true, Status: "experimental_dark", Decision: "do_not_release", Reason: "validation_and_operating_gates_stopped"},
	{PackID: "phebs.grpc.caller.go", PilotRelevant: true, Status: "experimental_dark", Decision: "do_not_release", Reason: "validation_and_operating_gates_stopped"},
	{PackID: "phebs.protobuf.field.go", PilotRelevant: true, Status: "experimental_dark", Decision: "do_not_release", Reason: "validation_and_operating_gates_stopped"},
	{PackID: "phebs.thrift.contract", Status: "experimental_dark", Decision: "do_not_release", Reason: "outside_t39_pilot_scope"},
	{PackID: "phebs.thrift.consumer.go", Status: "experimental_dark", Decision: "do_not_release", Reason: "outside_t39_pilot_scope"},
	{PackID: "phebs.thrift.field.apache", Status: "experimental_dark", Decision: "do_not_release", Reason: "outside_t39_pilot_scope"},
	{PackID: "phebs.thrift.field.thriftrw", Status: "experimental_dark", Decision: "do_not_release", Reason: "outside_t39_pilot_scope"},
	{PackID: "phebs.kafka.producer", Status: "experimental_dark", Decision: "do_not_release", Reason: "outside_t39_pilot_scope"},
	{PackID: "phebs.kafka.consumer", Status: "experimental_dark", Decision: "do_not_release", Reason: "outside_t39_pilot_scope"},
}

func Build(root string) (Receipt, error) {
	inputs, err := readInputs(root)
	if err != nil {
		return Receipt{}, err
	}
	receipt := Receipt{
		Schema: Schema, Outcome: "stop", CompletedOn: "2026-08-06", Inputs: inputs,
		GateSummary: GateSummary{
			NeutralMechanics: "passed", TargetOperating: "stopped",
			SecurityLifecycle: "passed_nonpromoting", EvidenceWorkflow: "stopped_before_unsealing",
		},
		Packs: slices.Clone(packSpecs),
		Validation: ValidationDecision{
			GateStatus: "NOT_ESTABLISHED", ReleaseDecision: "DO_NOT_RELEASE",
			Reason: "required_target_operating_and_evidence_workflow_gates_stopped",
		},
		Continuation: ContinuationDecision{
			Status: "not_eligible", Decision: "none",
			Reason: "validation_gate_not_established", ActingPrincipal: "none",
			AuthorizedNextAction: "none",
		},
		Lifecycle: BuildLifecycleDecision(),
		Review: ReviewPolicy{
			Required: true, MergeRequiresIndependentReview: true, Authority: "outside_receipt",
		},
		Claims: ReceiptClaims{SourceFree: true},
	}
	if err := ValidateReceipt(receipt); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

func ValidateReceipt(receipt Receipt) error {
	if receipt.Schema != Schema || receipt.Outcome != "stop" || receipt.CompletedOn != "2026-08-06" {
		return errors.New("T39.5 receipt identity is invalid")
	}
	if !slices.Equal(receipt.Inputs, inputSpecs) {
		return errors.New("T39.5 input bindings differ from the frozen set")
	}
	wantSummary := GateSummary{
		NeutralMechanics: "passed", TargetOperating: "stopped",
		SecurityLifecycle: "passed_nonpromoting", EvidenceWorkflow: "stopped_before_unsealing",
	}
	if receipt.GateSummary != wantSummary {
		return errors.New("T39.5 gate summary changed")
	}
	if !reflect.DeepEqual(receipt.Packs, packSpecs) {
		return errors.New("T39.5 pack decision set changed")
	}
	wantValidation := ValidationDecision{
		GateStatus: "NOT_ESTABLISHED", ReleaseDecision: "DO_NOT_RELEASE",
		Reason: "required_target_operating_and_evidence_workflow_gates_stopped",
	}
	if receipt.Validation != wantValidation {
		return errors.New("T39.5 validation decision changed")
	}
	wantContinuation := ContinuationDecision{
		Status: "not_eligible", Decision: "none", Reason: "validation_gate_not_established",
		ActingPrincipal: "none", AuthorizedNextAction: "none",
	}
	if receipt.Continuation != wantContinuation {
		return errors.New("T39.5 continuation decision changed")
	}
	wantLifecycle := BuildLifecycleDecision()
	if !reflect.DeepEqual(receipt.Lifecycle, wantLifecycle) {
		return errors.New("T39.5 lifecycle decision changed")
	}
	wantReview := ReviewPolicy{
		Required: true, MergeRequiresIndependentReview: true, Authority: "outside_receipt",
	}
	if receipt.Review != wantReview {
		return errors.New("T39.5 independent-review policy changed")
	}
	if receipt.Claims != (ReceiptClaims{SourceFree: true}) {
		return errors.New("T39.5 negative claims changed")
	}
	return nil
}

func BuildLifecycleDecision() LifecycleDecision {
	return LifecycleDecision{
		CurrentStatus: "experimental_dark", DefaultDark: true,
		SuspensionReason: "not_applicable_pack_never_released",
		FutureSuspensionTriggers: []string{
			"validation_expiry", "artifact_or_supported_scope_drift",
			"authorization_or_reproduction_failure", "quality_coverage_or_workflow_threshold_failure",
			"operating_envelope_exceeded",
		},
		Expiry:   "no_applicable_validation_exists",
		Rollback: "disable_opt_in_and_withdraw_pack_release_without_rewriting_historical_evidence",
		RevalidationRequirements: []string{
			"new_approved_digest_frozen_gate0_package", "sealed_independent_gold_and_workflow_baseline",
			"fresh_unseen_adequately_powered_round", "complete_target_operating_envelope",
			"current_security_review", "named_validation_and_workflow_owners",
			"explicit_expiry_and_signed_pack_release", "t39r1_before_t392_rerun",
		},
	}
}

func Marshal(receipt Receipt) ([]byte, error) {
	if err := ValidateReceipt(receipt); err != nil {
		return nil, err
	}
	encoded, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return nil, err
	}
	encoded = append(encoded, '\n')
	if len(encoded) > MaxReceiptBytes {
		return nil, fmt.Errorf("T39.5 receipt bytes %d exceed %d", len(encoded), MaxReceiptBytes)
	}
	return encoded, nil
}

func Decode(data []byte) (Receipt, error) {
	if len(data) == 0 || len(data) > MaxReceiptBytes {
		return Receipt{}, errors.New("T39.5 receipt size is outside the fixed bound")
	}
	var receipt Receipt
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		return Receipt{}, err
	}
	var trailing struct{}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return Receipt{}, err
	}
	if err := ValidateReceipt(receipt); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

func readInputs(root string) ([]InputBinding, error) {
	inputs := slices.Clone(inputSpecs)
	for _, input := range inputs {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(input.Path)))
		if err != nil {
			return nil, err
		}
		if Digest(content) != input.SHA256 {
			return nil, fmt.Errorf("input %s digest changed", input.Path)
		}
		switch input.Path {
		case "spike/t391/results.json":
			if _, err := t391.Decode(content); err != nil {
				return nil, fmt.Errorf("validate T39.1 input: %w", err)
			}
		case "spike/t392/results.json":
			receipt, err := t392.DecodeReceipt(content)
			if err != nil || receipt.Outcome != "stopped" {
				return nil, errors.New("T39.2 input is not the retained stop")
			}
		case "spike/t393/results.json":
			receipt, err := t393.Decode(content)
			if err != nil || receipt.Outcome != "passed" || !receipt.Claims.T392StopPreserved {
				return nil, errors.New("T39.3 input is not the nonpromoting security pass")
			}
		case "spike/t394/results.json":
			receipt, err := t394.Decode(content)
			if err != nil || receipt.Outcome != "stopped_before_unsealing" || !receipt.Claims.T392StopPreserved {
				return nil, errors.New("T39.4 input is not the retained pre-unsealing stop")
			}
		}
	}
	return inputs, nil
}

func Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
