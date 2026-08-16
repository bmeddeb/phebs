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
	"time"

	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/spike/t401"
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

var frozenSafetyV4 = SafetyEnvelope{
	MinimumMemoryBytes: 24 << 30, MinimumAvailableDiskBytes: 120 << 30,
	MaximumTotalWallMS: 8 * 60 * 60 * 1000, MaximumPeakRSSBytes: 20 << 30,
	MaximumDataAllocatedBytes: 96 << 30, MaximumRetriesPerUnit: 5,
	ServerHealthDeadlineMS:    15 * 60 * 1000,
	FullConvergenceDeadlineMS: 2 * 60 * 60 * 1000,
	RevalidationDeadlineMS:    20 * 60 * 1000,
}

var frozenSafetyV5 = SafetyEnvelope{
	MinimumMemoryBytes: 24 << 30, MinimumAvailableDiskBytes: 120 << 30,
	MaximumTotalWallMS: 8 * 60 * 60 * 1000, MaximumPeakRSSBytes: 20 << 30,
	MaximumDataAllocatedBytes: 96 << 30, MaximumRetriesPerUnit: 5,
	ServerHealthDeadlineMS:    15 * 60 * 1000,
	FullConvergenceDeadlineMS: 4 * 60 * 60 * 1000,
	RevalidationDeadlineMS:    20 * 60 * 1000,
}

var frozenSafetyV6 = frozenSafetyV5

var frozenSafetyV7 = frozenSafetyV6

var frozenSafetyV8 = frozenSafetyV7

var frozenSafetyV9 = frozenSafetyV8

var frozenSafetyV10 = SafetyEnvelope{
	MinimumMemoryBytes: 24 << 30, MinimumAvailableDiskBytes: 120 << 30,
	MaximumTotalWallMS: 8 * 60 * 60 * 1000, MaximumPeakRSSBytes: 20 << 30,
	MaximumDataAllocatedBytes: 96 << 30, MaximumRetriesPerUnit: 5,
	ServerHealthDeadlineMS:      15 * 60 * 1000,
	FullConvergenceDeadlineMS:   4 * 60 * 60 * 1000,
	RevalidationDeadlineMS:      20 * 60 * 1000,
	PressureTargetUsedPercent:   82,
	MaximumPressureBallastBytes: 80 << 30,
}

var frozenSafetyV11 = frozenSafetyV10
var frozenSafetyV12 = frozenSafetyV11
var frozenSafetyV13 = frozenSafetyV12
var frozenSafetyV14 = frozenSafetyV13

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

// FrozenHostPlan creates the current plan by observing and binding the exact
// host executables that can influence a ceremony before custody exists.
func FrozenHostPlan(ctx context.Context, sourceCommit string) (Plan, error) {
	if err := validateV14ConvergenceRoutes(); err != nil {
		return Plan{}, err
	}
	hostToolchain, err := ObserveHostToolchain(ctx)
	if err != nil {
		return Plan{}, err
	}
	return freshV14PlanWithHostToolchain(sourceCommit, hostToolchain, time.Now())
}

func validateV14ConvergenceRoutes() error {
	profiles, err := t401.FrozenProfiles()
	if err != nil {
		return err
	}
	for _, profile := range profiles {
		// V14 requires the selected segmented observation route. A frozen input
		// that expects refusal on that required convergence lane is not freezeable.
		if profile.Aggregate.UniqueGoBlobs > observationpublication.MaxInventoryRecordsV2 {
			return errors.New("T40.13 selected observation route is expected to refuse")
		}
	}
	return nil
}

func frozenV14PlanWithHostToolchain(sourceCommit string, hostToolchain []HostToolObservation) (Plan, error) {
	value, err := FrozenPlan(sourceCommit)
	if err != nil {
		return Plan{}, err
	}
	value.Schema = PlanSchemaV14
	value.HostToolchain = slices.Clone(hostToolchain)
	value.Safety = frozenSafetyV14
	if err := ValidatePlan(value); err != nil {
		return Plan{}, err
	}
	return value, nil
}

func freshV14PlanWithHostToolchain(
	sourceCommit string,
	hostToolchain []HostToolObservation,
	frozenAt time.Time,
) (Plan, error) {
	if frozenAt.IsZero() {
		return Plan{}, errors.New("T40.13 fresh freeze time is required")
	}
	value, err := frozenV14PlanWithHostToolchain(sourceCommit, hostToolchain)
	if err != nil {
		return Plan{}, err
	}
	value.FrozenOn = frozenAt.UTC().Format(time.DateOnly)
	if err := ValidatePlan(value); err != nil {
		return Plan{}, err
	}
	return value, nil
}

func frozenV13PlanWithHostToolchain(sourceCommit string, hostToolchain []HostToolObservation) (Plan, error) {
	value, err := FrozenPlan(sourceCommit)
	if err != nil {
		return Plan{}, err
	}
	value.Schema = PlanSchemaV13
	value.HostToolchain = slices.Clone(hostToolchain)
	value.Safety = frozenSafetyV13
	if err := ValidatePlan(value); err != nil {
		return Plan{}, err
	}
	return value, nil
}

func frozenV12PlanWithHostToolchain(sourceCommit string, hostToolchain []HostToolObservation) (Plan, error) {
	value, err := FrozenPlan(sourceCommit)
	if err != nil {
		return Plan{}, err
	}
	value.Schema = PlanSchemaV12
	value.HostToolchain = slices.Clone(hostToolchain)
	value.Safety = frozenSafetyV12
	if err := ValidatePlan(value); err != nil {
		return Plan{}, err
	}
	return value, nil
}

func frozenV11PlanWithHostToolchain(sourceCommit string, hostToolchain []HostToolObservation) (Plan, error) {
	value, err := FrozenPlan(sourceCommit)
	if err != nil {
		return Plan{}, err
	}
	value.Schema = PlanSchemaV11
	value.HostToolchain = slices.Clone(hostToolchain)
	value.Safety = frozenSafetyV11
	if err := ValidatePlan(value); err != nil {
		return Plan{}, err
	}
	return value, nil
}

func frozenV10PlanWithHostToolchain(sourceCommit string, hostToolchain []HostToolObservation) (Plan, error) {
	value, err := FrozenPlan(sourceCommit)
	if err != nil {
		return Plan{}, err
	}
	value.Schema = PlanSchemaV10
	value.HostToolchain = slices.Clone(hostToolchain)
	value.Safety = frozenSafetyV10
	if err := ValidatePlan(value); err != nil {
		return Plan{}, err
	}
	return value, nil
}

func frozenV9PlanWithHostToolchain(sourceCommit string, hostToolchain []HostToolObservation) (Plan, error) {
	value, err := FrozenPlan(sourceCommit)
	if err != nil {
		return Plan{}, err
	}
	value.Schema = PlanSchemaV9
	value.HostToolchain = slices.Clone(hostToolchain)
	value.Safety = frozenSafetyV9
	if err := ValidatePlan(value); err != nil {
		return Plan{}, err
	}
	return value, nil
}

func frozenV8PlanWithHostToolchain(sourceCommit string, hostToolchain []HostToolObservation) (Plan, error) {
	value, err := FrozenPlan(sourceCommit)
	if err != nil {
		return Plan{}, err
	}
	value.Schema = PlanSchemaV8
	value.HostToolchain = slices.Clone(hostToolchain)
	value.Safety = frozenSafetyV8
	if err := ValidatePlan(value); err != nil {
		return Plan{}, err
	}
	return value, nil
}

func frozenV7PlanWithHostToolchain(sourceCommit string, hostToolchain []HostToolObservation) (Plan, error) {
	value, err := FrozenPlan(sourceCommit)
	if err != nil {
		return Plan{}, err
	}
	value.Schema = PlanSchemaV7
	value.HostToolchain = slices.Clone(hostToolchain)
	value.Safety = frozenSafetyV7
	if err := ValidatePlan(value); err != nil {
		return Plan{}, err
	}
	return value, nil
}

func frozenV6PlanWithHostToolchain(sourceCommit string, hostToolchain []HostToolObservation) (Plan, error) {
	value, err := FrozenPlan(sourceCommit)
	if err != nil {
		return Plan{}, err
	}
	value.Schema = PlanSchemaV6
	value.HostToolchain = slices.Clone(hostToolchain)
	value.Safety = frozenSafetyV6
	if err := ValidatePlan(value); err != nil {
		return Plan{}, err
	}
	return value, nil
}

func frozenV5PlanWithHostToolchain(sourceCommit string, hostToolchain []HostToolObservation) (Plan, error) {
	value, err := FrozenPlan(sourceCommit)
	if err != nil {
		return Plan{}, err
	}
	value.Schema = PlanSchemaV5
	value.HostToolchain = slices.Clone(hostToolchain)
	value.Safety = frozenSafetyV5
	if err := ValidatePlan(value); err != nil {
		return Plan{}, err
	}
	return value, nil
}

func frozenV4PlanWithHostToolchain(sourceCommit string, hostToolchain []HostToolObservation) (Plan, error) {
	value, err := FrozenPlan(sourceCommit)
	if err != nil {
		return Plan{}, err
	}
	value.Schema = PlanSchemaV4
	value.HostToolchain = slices.Clone(hostToolchain)
	value.Safety = frozenSafetyV4
	if err := ValidatePlan(value); err != nil {
		return Plan{}, err
	}
	return value, nil
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
