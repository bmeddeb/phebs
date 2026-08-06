// Package t394 retains the source-free evidence-quality and workflow gate for
// T39.4. Production packages must not import spike packages.
package t394

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
	"strings"

	"github.com/bmeddeb/phebs/spike/t392"
)

const (
	Schema          = "t394-evidence-quality-workflow-gate-v1"
	MaxReceiptBytes = 64 << 10

	T392ReceiptSHA256      = "sha256:20e95fc8e7bf11fb9ae317aa57aeda2966fbf1c866d812f37fadb11aabd26a41"
	PilotCharterSHA256     = "sha256:eed8290d0d6185a2a672c050aa535e6e503a83d4721efcaa6b4de8e969d134d0"
	AccuracyProtocolSHA256 = "sha256:31024a617fe0fde6e8a5c56e24f7d85e496cda40f5fb7a7e37daa6b613ea4248"
	WorkflowProtocolSHA256 = "sha256:d273ce456063044b8a72418680acaeabc216ddbaccd804d3a8d02803bfa25dc8"
	RetainedReceiptSHA256  = "sha256:46c039eb6193afc63e94ae28ab1837a09b7870fc48a09292184254c02e863e97"
	protocolStopReason     = "preregistration_not_execution_binding"
	unmeasuredOutcome      = "not_run"
	unmeasuredValueState   = "unmeasured"
	unsealedProtocolReason = "unsealed_preregistration"
)

type Receipt struct {
	Schema      string              `json:"schema"`
	Outcome     string              `json:"outcome"`
	CompletedOn string              `json:"completed_on"`
	Inputs      []InputBinding      `json:"inputs"`
	PriorStop   PriorStop           `json:"t392_stop_authority"`
	Authority   AuthorityAssessment `json:"measurement_authority"`
	Gates       []GateResult        `json:"gates"`
	Policy      EvaluationPolicy    `json:"evaluation_policy"`
	Review      ReviewPolicy        `json:"independent_review"`
	StopReason  string              `json:"stop_reason"`
	Claims      ReceiptClaims       `json:"claims"`
}

type InputBinding struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	SHA256 string `json:"sha256"`
}

type PriorStop struct {
	Outcome          string `json:"outcome"`
	FailurePhase     string `json:"failure_phase"`
	FailureClass     string `json:"failure_class"`
	TerminalFailures int    `json:"terminal_failures"`
	LaterPhases      string `json:"later_phases"`
	PreservedAsStop  bool   `json:"preserved_as_stop"`
	Superseded       bool   `json:"superseded"`
}

type AuthorityAssessment struct {
	PilotCharterStatus           string `json:"pilot_charter_status"`
	AccuracyProtocolStatus       string `json:"accuracy_protocol_status"`
	WorkflowProtocolStatus       string `json:"workflow_protocol_status"`
	Gate0Approved                bool   `json:"gate0_approved"`
	ThresholdsFrozenBeforeUnseal bool   `json:"thresholds_frozen_before_unseal"`
	IndependentGoldSealed        bool   `json:"independent_gold_sealed"`
	IndependentBaselineSealed    bool   `json:"independent_baseline_sealed"`
	MeasurementAuthorized        bool   `json:"measurement_authorized"`
	PredictionsUnsealed          bool   `json:"predictions_unsealed"`
	TargetEvidenceAccessed       bool   `json:"target_evidence_accessed"`
	ExistingPilotScopeBroadened  bool   `json:"existing_pilot_scope_broadened"`
}

type GateResult struct {
	Name       string `json:"name"`
	Outcome    string `json:"outcome"`
	ValueState string `json:"value_state"`
	Reason     string `json:"reason"`
}

type EvaluationPolicy struct {
	UnderpoweredOrInconclusiveIsPass bool `json:"underpowered_or_inconclusive_is_pass"`
	MissingValuesImputed             bool `json:"missing_values_imputed"`
	CorrectionsCounted               bool `json:"corrections_counted"`
	OwnerRoutingCounted              bool `json:"owner_routing_counted"`
	ThresholdChangesAllowed          bool `json:"threshold_changes_allowed"`
	StopBeforeUnsealing              bool `json:"stop_before_unsealing"`
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
	T392StopPreserved             bool `json:"t392_stop_preserved"`
	EstablishesAccuracy           bool `json:"establishes_accuracy"`
	EstablishesCoverage           bool `json:"establishes_coverage"`
	EstablishesWorkflowValue      bool `json:"establishes_workflow_value"`
	AuthorizesRelease             bool `json:"authorizes_release"`
	EstablishesMigrationComplete  bool `json:"establishes_migration_complete"`
	EstablishesDecommissionSafety bool `json:"establishes_decommission_safety"`
	ChangesProductionBehavior     bool `json:"changes_production_behavior"`
}

var inputSpecs = []InputBinding{
	{Path: "spike/t392/results.json", Kind: t392.ReceiptSchema, SHA256: T392ReceiptSHA256},
	{Path: "docs/PILOT_CHARTER.md", Kind: "pilot_charter_draft", SHA256: PilotCharterSHA256},
	{Path: "docs/ACCURACY_GOLD_PROTOCOL.md", Kind: "accuracy_preregistration_draft", SHA256: AccuracyProtocolSHA256},
	{Path: "docs/CURRENT_WORKFLOW_BASELINE_PROTOCOL.md", Kind: "workflow_preregistration_draft", SHA256: WorkflowProtocolSHA256},
}

var gateNames = []string{
	"go_grpc_call_site_quality",
	"processing_coverage",
	"build_target_attribution",
	"deployable_attribution",
	"service_attribution",
	"owner_record_resolution",
	"end_to_end_service_operation_quality",
	"evidence_reproduction",
	"unresolved_state_rate",
	"workflow_inventory_time",
	"workflow_labor",
	"post_acceptance_correction_cost",
	"owner_routing_cost",
}

func Build(root string) (Receipt, error) {
	inputs, prior, err := readInputs(root)
	if err != nil {
		return Receipt{}, err
	}
	receipt := Receipt{
		Schema: Schema, Outcome: "stopped_before_unsealing", CompletedOn: "2026-08-06",
		Inputs: inputs, PriorStop: prior,
		Authority: AuthorityAssessment{
			PilotCharterStatus:     "design_draft",
			AccuracyProtocolStatus: "unsealed_synthetic_fixture",
			WorkflowProtocolStatus: "unsealed_synthetic_fixture",
		},
		Gates: gateResults(),
		Policy: EvaluationPolicy{
			CorrectionsCounted: true, OwnerRoutingCounted: true, StopBeforeUnsealing: true,
		},
		Review: ReviewPolicy{
			Required: true, MergeRequiresIndependentReview: true, Authority: "outside_receipt",
		},
		StopReason: protocolStopReason,
		Claims:     ReceiptClaims{SourceFree: true, T392StopPreserved: true},
	}
	if err := ValidateReceipt(receipt); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

func ValidateReceipt(receipt Receipt) error {
	if receipt.Schema != Schema || receipt.Outcome != "stopped_before_unsealing" || receipt.CompletedOn != "2026-08-06" {
		return errors.New("T39.4 receipt identity is invalid")
	}
	if !slices.Equal(receipt.Inputs, inputSpecs) {
		return errors.New("T39.4 input bindings differ from the frozen set")
	}
	wantStop := PriorStop{
		Outcome: "stopped", FailurePhase: "incremental", FailureClass: "pipeline",
		TerminalFailures: 1, LaterPhases: "not_run", PreservedAsStop: true,
	}
	if receipt.PriorStop != wantStop {
		return fmt.Errorf("T39.2 stop authority changed: %+v", receipt.PriorStop)
	}
	wantAuthority := AuthorityAssessment{
		PilotCharterStatus:     "design_draft",
		AccuracyProtocolStatus: "unsealed_synthetic_fixture",
		WorkflowProtocolStatus: "unsealed_synthetic_fixture",
	}
	if receipt.Authority != wantAuthority {
		return errors.New("T39.4 measurement authority changed")
	}
	if !reflect.DeepEqual(receipt.Gates, gateResults()) {
		return errors.New("T39.4 unmeasured gate set changed")
	}
	wantPolicy := EvaluationPolicy{
		CorrectionsCounted: true, OwnerRoutingCounted: true, StopBeforeUnsealing: true,
	}
	if receipt.Policy != wantPolicy {
		return errors.New("T39.4 evaluation policy changed")
	}
	wantReview := ReviewPolicy{
		Required: true, MergeRequiresIndependentReview: true, Authority: "outside_receipt",
	}
	if receipt.Review != wantReview {
		return errors.New("T39.4 independent-review policy changed")
	}
	if receipt.StopReason != protocolStopReason {
		return errors.New("T39.4 stop reason changed")
	}
	wantClaims := ReceiptClaims{SourceFree: true, T392StopPreserved: true}
	if receipt.Claims != wantClaims {
		return errors.New("T39.4 negative claims changed")
	}
	return nil
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
		return nil, fmt.Errorf("T39.4 receipt bytes %d exceed %d", len(encoded), MaxReceiptBytes)
	}
	return encoded, nil
}

func Decode(data []byte) (Receipt, error) {
	if len(data) == 0 || len(data) > MaxReceiptBytes {
		return Receipt{}, errors.New("T39.4 receipt size is outside the fixed bound")
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

func readInputs(root string) ([]InputBinding, PriorStop, error) {
	inputs := slices.Clone(inputSpecs)
	var prior PriorStop
	for _, input := range inputs {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(input.Path)))
		if err != nil {
			return nil, PriorStop{}, err
		}
		if Digest(content) != input.SHA256 {
			return nil, PriorStop{}, fmt.Errorf("input %s digest changed", input.Path)
		}
		switch input.Path {
		case "spike/t392/results.json":
			receipt, err := t392.DecodeReceipt(content)
			if err != nil {
				return nil, PriorStop{}, fmt.Errorf("validate T39.2 input: %w", err)
			}
			if len(receipt.Failures) != 1 || receipt.NoOp.Outcome != "not_run" ||
				receipt.Query.Outcome != "not_run" || receipt.Recovery.Outcome != "not_run" ||
				receipt.Restore.Outcome != "not_run" || receipt.Retention.Outcome != "not_run" {
				return nil, PriorStop{}, errors.New("T39.2 input no longer has one failure and uniformly unrun later phases")
			}
			terminal := 0
			for _, failure := range receipt.Failures {
				if failure.Terminal {
					terminal++
				}
			}
			prior = PriorStop{
				Outcome: receipt.Outcome, FailurePhase: receipt.Failures[0].Phase,
				FailureClass: receipt.Failures[0].Class, TerminalFailures: terminal,
				LaterPhases: receipt.NoOp.Outcome, PreservedAsStop: true,
			}
		case "docs/PILOT_CHARTER.md":
			if !strings.Contains(string(content), "any blank, placeholder, or unexplained `N/A` blocks release") {
				return nil, PriorStop{}, errors.New("pilot charter no longer carries its fail-closed placeholder rule")
			}
		case "docs/ACCURACY_GOLD_PROTOCOL.md":
			if !strings.Contains(string(content), "preregistration draft") ||
				!strings.Contains(string(content), "Every `<Gate 0>` placeholder") {
				return nil, PriorStop{}, errors.New("accuracy protocol no longer declares its unsealed state")
			}
		case "docs/CURRENT_WORKFLOW_BASELINE_PROTOCOL.md":
			if !strings.Contains(string(content), "preregistration draft") ||
				!strings.Contains(string(content), "Measurement authority | none") {
				return nil, PriorStop{}, errors.New("workflow protocol no longer declares its unsealed state")
			}
		}
	}
	return inputs, prior, nil
}

func gateResults() []GateResult {
	results := make([]GateResult, 0, len(gateNames))
	for _, name := range gateNames {
		results = append(results, GateResult{
			Name: name, Outcome: unmeasuredOutcome, ValueState: unmeasuredValueState,
			Reason: unsealedProtocolReason,
		})
	}
	return results
}

func Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
