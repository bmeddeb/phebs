// Package t39r1 retains the source-free mirror-lock contention diagnosis and
// authorized-rerun precondition closure. Production packages must not import
// spike packages.
package t39r1

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

	"github.com/bmeddeb/phebs/spike/t392"
	"github.com/bmeddeb/phebs/spike/t395"
)

const (
	Schema          = "t39r1-mirror-lock-contention-closure-v1"
	MaxReceiptBytes = 32 << 10

	T392ReceiptSHA256 = "sha256:20e95fc8e7bf11fb9ae317aa57aeda2966fbf1c866d812f37fadb11aabd26a41"
	T395ReceiptSHA256 = "sha256:7143aaa34a38a8f14bdaa9d7f695ee65f5b89dcf3f4592ec9f6b4ea64d45677d"
	RetainedSHA256    = "sha256:92a2751a9d6f31f5e3c9d692bb9fd2022ac84d9bdf9e143f76504cab5c1e502e"
)

type Receipt struct {
	Schema      string         `json:"schema"`
	Outcome     string         `json:"outcome"`
	CompletedOn string         `json:"completed_on"`
	Inputs      []InputBinding `json:"inputs"`
	Diagnosis   Diagnosis      `json:"diagnosis"`
	Resolution  Resolution     `json:"resolution"`
	Gates       []Gate         `json:"gates"`
	Rerun       RerunPolicy    `json:"rerun_policy"`
	Claims      Claims         `json:"claims"`
}

type InputBinding struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	SHA256 string `json:"sha256"`
}

type Diagnosis struct {
	Holder             string `json:"holder"`
	Waiter             string `json:"waiter"`
	SharedBoundary     string `json:"shared_boundary"`
	PriorBudgetStart   string `json:"prior_budget_start"`
	FailureMechanism   string `json:"failure_mechanism"`
	FailureClass       string `json:"failure_class"`
	RequiredChange     string `json:"required_change"`
	AdmissionChange    bool   `json:"admission_change"`
	RetryChange        bool   `json:"retry_accounting_change"`
	TimeoutLimitRaised bool   `json:"timeout_limit_raised"`
	AttemptLimitRaised bool   `json:"attempt_limit_raised"`
}

type Resolution struct {
	PreflightBudgetMilliseconds int64  `json:"preflight_budget_milliseconds"`
	MirrorWaitContext           string `json:"mirror_wait_context"`
	ExecutionBudgetStart        string `json:"execution_budget_start"`
	ExecutionBudgetMilliseconds int64  `json:"execution_budget_milliseconds"`
	MaxAttempts                 int    `json:"max_attempts"`
	Serialization               string `json:"serialization"`
	Cancellation                string `json:"cancellation"`
	LeaseLoss                   string `json:"lease_loss"`
	PriorPublication            string `json:"prior_publication"`
}

type Gate struct {
	Name   string `json:"name"`
	Proves string `json:"proves"`
}

type RerunPolicy struct {
	T392Superseded      bool     `json:"t392_superseded"`
	RerunAuthorized     bool     `json:"rerun_authorized"`
	RequiredAuthorities []string `json:"required_authorities"`
	ReceiptRequirement  string   `json:"receipt_requirement"`
}

type Claims struct {
	SourceFree                  bool `json:"source_free"`
	ChangesProductionScheduling bool `json:"changes_production_scheduling"`
	AuthorizesTargetRerun       bool `json:"authorizes_target_rerun"`
	SupersedesStoppedRun        bool `json:"supersedes_stopped_run"`
	RaisesTimeout               bool `json:"raises_timeout"`
	RaisesAttemptLimit          bool `json:"raises_attempt_limit"`
	EstablishesSLO              bool `json:"establishes_slo"`
	EstablishesRelease          bool `json:"establishes_release"`
}

var inputSpecs = []InputBinding{
	{Path: "spike/t392/results.json", Kind: t392.ReceiptSchema, SHA256: T392ReceiptSHA256},
	{Path: "spike/t395/results.json", Kind: t395.Schema, SHA256: T395ReceiptSHA256},
}

var gateSpecs = []Gate{
	{Name: "TestWorkerMirrorWaitCannotConsumeAttemptBudget", Proves: "lock wait can exceed the tightened execution budget without returning an attempt-consuming error"},
	{Name: "TestWorkerMirrorWaitCancellationMutatesNoCallerState", Proves: "parent cancellation interrupts lock wait before caller plan or durable caller mutation"},
	{Name: "TestWorkerReplayDeadlineStaysRetryableWithoutSuccessorMark", Proves: "a genuine execution-budget expiry remains an ordinary retryable pair failure"},
	{Name: "TestWorkerParentCancellationIsDistinctFromWorkerDeadline", Proves: "parent cancellation remains distinguishable from execution-budget expiry"},
	{Name: "TestRunnerStopsHandlerWhenHeartbeatLosesLease", Proves: "lease loss cancels the handler without a stale terminal queue transition"},
	{Name: "TestWorkerRepairsStateWithoutFileQueueBeforeClear", Proves: "repair queues a successor and retires a bad pointer before changing prior leaf outcomes"},
}

func Build(root string) (Receipt, error) {
	inputs, err := readInputs(root)
	if err != nil {
		return Receipt{}, err
	}
	receipt := Receipt{
		Schema: Schema, Outcome: "precondition_closed", CompletedOn: "2026-08-06",
		Inputs: inputs,
		Diagnosis: Diagnosis{
			Holder: "aggregate_extraction", Waiter: "caller_leaf_execution",
			SharedBoundary:   "repository_immutable_mirror_lock",
			PriorBudgetStart: "before_mirror_lock_wait",
			FailureMechanism: "admitted_extraction_lock_occupancy_consumed_caller_execution_budget_and_ordinary_retries",
			FailureClass:     "pipeline_contention", RequiredChange: "bounded_serialization_boundary",
		},
		Resolution: Resolution{
			PreflightBudgetMilliseconds: 300000,
			MirrorWaitContext:           "lease_heartbeated_parent_job_context",
			ExecutionBudgetStart:        "after_repository_mirror_lock_acquisition",
			ExecutionBudgetMilliseconds: 300000, MaxAttempts: 3,
			Serialization:    "one_repository_lock_unchanged",
			Cancellation:     "interrupt_wait_without_caller_state_mutation",
			LeaseLoss:        "runner_cancels_wait_and_performs_no_stale_transition",
			PriorPublication: "preserved_until_existing_successor_first_repair_fences_complete",
		},
		Gates: slices.Clone(gateSpecs),
		Rerun: RerunPolicy{
			RequiredAuthorities: []string{
				"new_explicit_approval", "new_nonce", "new_frozen_plan",
				"new_teardown_deadline", "source_free_receipt",
			},
			ReceiptRequirement: "new_receipt_cannot_rewrite_or_supersede_t392_without_a_completed_authorized_run",
		},
		Claims: Claims{SourceFree: true, ChangesProductionScheduling: true},
	}
	if err := Validate(receipt); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

func Validate(receipt Receipt) error {
	if receipt.Schema != Schema || receipt.Outcome != "precondition_closed" ||
		receipt.CompletedOn != "2026-08-06" {
		return errors.New("T39.R1 receipt identity is invalid")
	}
	if !slices.Equal(receipt.Inputs, inputSpecs) {
		return errors.New("T39.R1 input bindings changed")
	}
	wantDiagnosis := Diagnosis{
		Holder: "aggregate_extraction", Waiter: "caller_leaf_execution",
		SharedBoundary:   "repository_immutable_mirror_lock",
		PriorBudgetStart: "before_mirror_lock_wait",
		FailureMechanism: "admitted_extraction_lock_occupancy_consumed_caller_execution_budget_and_ordinary_retries",
		FailureClass:     "pipeline_contention", RequiredChange: "bounded_serialization_boundary",
	}
	if receipt.Diagnosis != wantDiagnosis {
		return errors.New("T39.R1 diagnosis changed")
	}
	wantResolution := Resolution{
		PreflightBudgetMilliseconds: 300000,
		MirrorWaitContext:           "lease_heartbeated_parent_job_context",
		ExecutionBudgetStart:        "after_repository_mirror_lock_acquisition",
		ExecutionBudgetMilliseconds: 300000, MaxAttempts: 3,
		Serialization:    "one_repository_lock_unchanged",
		Cancellation:     "interrupt_wait_without_caller_state_mutation",
		LeaseLoss:        "runner_cancels_wait_and_performs_no_stale_transition",
		PriorPublication: "preserved_until_existing_successor_first_repair_fences_complete",
	}
	if receipt.Resolution != wantResolution {
		return errors.New("T39.R1 resolution changed")
	}
	if !reflect.DeepEqual(receipt.Gates, gateSpecs) {
		return errors.New("T39.R1 executable gates changed")
	}
	wantRerun := RerunPolicy{
		RequiredAuthorities: []string{
			"new_explicit_approval", "new_nonce", "new_frozen_plan",
			"new_teardown_deadline", "source_free_receipt",
		},
		ReceiptRequirement: "new_receipt_cannot_rewrite_or_supersede_t392_without_a_completed_authorized_run",
	}
	if !reflect.DeepEqual(receipt.Rerun, wantRerun) {
		return errors.New("T39.R1 rerun policy changed")
	}
	if receipt.Claims != (Claims{SourceFree: true, ChangesProductionScheduling: true}) {
		return errors.New("T39.R1 claims changed")
	}
	return nil
}

func Marshal(receipt Receipt) ([]byte, error) {
	if err := Validate(receipt); err != nil {
		return nil, err
	}
	encoded, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return nil, err
	}
	encoded = append(encoded, '\n')
	if len(encoded) > MaxReceiptBytes {
		return nil, errors.New("T39.R1 receipt exceeds its byte bound")
	}
	return encoded, nil
}

func Decode(encoded []byte) (Receipt, error) {
	if len(encoded) == 0 || len(encoded) > MaxReceiptBytes {
		return Receipt{}, errors.New("T39.R1 receipt has invalid size")
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var receipt Receipt
	if err := decoder.Decode(&receipt); err != nil {
		return Receipt{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return Receipt{}, errors.New("T39.R1 receipt has trailing value")
		}
		return Receipt{}, err
	}
	if err := Validate(receipt); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

func Digest(encoded []byte) string {
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func readInputs(root string) ([]InputBinding, error) {
	inputs := slices.Clone(inputSpecs)
	for _, input := range inputs {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(input.Path)))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", input.Path, err)
		}
		if got := Digest(content); got != input.SHA256 {
			return nil, fmt.Errorf("%s digest = %s, want %s", input.Path, got, input.SHA256)
		}
	}
	return inputs, nil
}
