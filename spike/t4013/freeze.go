package t4013

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

var expectedInputs = []InputBinding{
	{Path: "spike/t401/envelope.json", Schema: "t401-neutral-scale-envelope-v1", SHA256: "sha256:92cce848e6e42942c24e2fa066968571fb5693252b7b41b7a91c889881fe7f94"},
	{Path: "spike/t401/structural/manifest.json", Schema: "t401-neutral-git-manifest-v1", SHA256: "sha256:4ae92b8efa58d459fe8fa10ba23c5cedad3adc7b2dddbd7618ea8d96c306604b"},
	{Path: "spike/t401/semantic/manifest.json", Schema: "t401-neutral-git-manifest-v1", SHA256: "sha256:ca4925f3ca3ddad42955e5c3dc0e9b5610e7fa8ac4ce3e614a9ad091e23362a8"},
	{Path: "spike/t401/comparison.json", Schema: "t401-zoekt-blob-reader-comparison-v1", SHA256: "sha256:3527bec297c80c71b6c5081b1b386d25efc9ec8894643f599c7c57848be3b402"},
}

var frozenStopRules = []StopRule{
	{Decision: "continue", Trigger: "all exact mechanics checks pass inside production bounds and frozen host safety ceilings"},
	{Decision: "reduce", Trigger: "a frozen production bound, host prerequisite, or exact mechanics oracle refuses before complete authority"},
	{Decision: "cohort_experiment", Trigger: "direct mechanics converge but a frozen wall, RSS, allocation, recovery, or collection review ceiling is crossed"},
	{Decision: "p6_investigation", Trigger: "work multiplies by logical service count, cannot remain repository-bounded, or direct recovery cannot converge within fixed production bounds"},
}

var frozenSafety = SafetyEnvelope{
	MinimumMemoryBytes: 24 << 30, MinimumAvailableDiskBytes: 120 << 30,
	MaximumTotalWallMS: 8 * 60 * 60 * 1000, MaximumPeakRSSBytes: 20 << 30,
	MaximumDataAllocatedBytes: 96 << 30, MaximumRetriesPerUnit: 5,
}

var frozenSafetyV3 = SafetyEnvelope{
	MinimumMemoryBytes: 24 << 30, MinimumAvailableDiskBytes: 120 << 30,
	MaximumTotalWallMS: 8 * 60 * 60 * 1000, MaximumPeakRSSBytes: 20 << 30,
	MaximumDataAllocatedBytes: 96 << 30, MaximumRetriesPerUnit: 5,
	ServerHealthDeadlineMS: 15 * 60 * 1000,
}

func FrozenPlan(sourceCommit string) (Plan, error) {
	value := Plan{
		Schema: PlanSchema, FrozenOn: "2026-08-08", SourceCommit: sourceCommit,
		Inputs:     append([]InputBinding(nil), expectedInputs...),
		PhaseOrder: append([]string(nil), phaseOrder...),
		Safety:     frozenSafety,
		StopRules:  append([]StopRule(nil), frozenStopRules...),
		Claims:     PlanClaims{Neutral: true, SourceFreeReceipt: true},
	}
	if err := ValidatePlan(value); err != nil {
		return Plan{}, err
	}
	return value, nil
}

// FrozenHostPlan creates the prospective v2 plan by observing and binding the
// exact host executables that can influence a ceremony before custody exists.
func FrozenHostPlan(ctx context.Context, sourceCommit string) (Plan, error) {
	hostToolchain, err := ObserveHostToolchain(ctx)
	if err != nil {
		return Plan{}, err
	}
	return frozenPlanWithHostToolchain(sourceCommit, hostToolchain)
}

func frozenPlanWithHostToolchain(sourceCommit string, hostToolchain []HostToolObservation) (Plan, error) {
	value, err := FrozenPlan(sourceCommit)
	if err != nil {
		return Plan{}, err
	}
	value.Schema = PlanSchemaV3
	value.HostToolchain = slices.Clone(hostToolchain)
	value.Safety = frozenSafetyV3
	if err := ValidatePlan(value); err != nil {
		return Plan{}, err
	}
	return value, nil
}

func frozenV2PlanWithHostToolchain(sourceCommit string, hostToolchain []HostToolObservation) (Plan, error) {
	value, err := FrozenPlan(sourceCommit)
	if err != nil {
		return Plan{}, err
	}
	value.Schema = PlanSchemaV2
	value.HostToolchain = slices.Clone(hostToolchain)
	if err := ValidatePlan(value); err != nil {
		return Plan{}, err
	}
	return value, nil
}

func VerifyInputs(root string) error {
	if !filepath.IsAbs(root) {
		return errors.New("T40.13 input root must be absolute")
	}
	for _, input := range expectedInputs {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(input.Path)))
		if err != nil {
			return fmt.Errorf("read frozen input %s: %w", input.Path, err)
		}
		sum := sha256.Sum256(raw)
		if "sha256:"+hex.EncodeToString(sum[:]) != input.SHA256 {
			return fmt.Errorf("frozen input %s digest changed", input.Path)
		}
		var envelope struct {
			Schema string `json:"schema"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil || envelope.Schema != input.Schema {
			return fmt.Errorf("frozen input %s schema changed", input.Path)
		}
	}
	return nil
}

func MarshalPlan(value Plan) ([]byte, error) {
	if err := ValidatePlan(value); err != nil {
		return nil, err
	}
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	encoded = append(encoded, '\n')
	if len(encoded) > MaxPlanBytes {
		return nil, errors.New("T40.13 plan exceeds its fixed byte bound")
	}
	return encoded, nil
}
