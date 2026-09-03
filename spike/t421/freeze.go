package t421

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/bits"
	"reflect"
	"slices"
	"strings"
)

const (
	// ExecutionFreezeSchema is the prospective, source-free T42.2 admission
	// record sealed before any measured execution work begins.
	ExecutionFreezeSchema   = "t422-combined-execution-freeze-v1"
	MaxExecutionFreezeBytes = 64 << 10

	regularFileType       = "regular"
	pressureGeometryModel = "live-pressure-ballast-bounded-volume-v1"
)

// ExecutionFreeze binds one exact T42.1 plan to the implementation, tools,
// host, and pressure geometry admitted for a prospective T42.2 execution.
// It intentionally carries no executable path, source path, log, or source
// content.
type ExecutionFreeze struct {
	Schema            string                    `json:"schema"`
	PlanSHA256        string                    `json:"plan_sha256"`
	SignerFingerprint string                    `json:"signer_fingerprint"`
	Commits           ExecutionCommits          `json:"commits"`
	DigestAlgorithm   string                    `json:"digest_algorithm"`
	Tools             []ExecutionToolIdentity   `json:"tools"`
	Host              ExecutionHost             `json:"host"`
	Profile           ExecutionProfile          `json:"execution_profile"`
	Pressure          ExecutionPressureGeometry `json:"pressure_geometry"`
}

// ExecutionCommits are supplied by the integration and execution authorities;
// neither identity nor the clean-tree observation is inferred here.
type ExecutionCommits struct {
	IntegratedMainCommit                 string `json:"integrated_main_commit"`
	IntegratedMainTree                   string `json:"integrated_main_tree"`
	T422SourceCommit                     string `json:"t422_source_commit"`
	T422SourceTree                       string `json:"t422_source_tree"`
	CleanTree                            bool   `json:"clean_tree"`
	IntegratedMainDescendsFromPlanSource bool   `json:"integrated_main_descends_from_plan_source"`
	SourceDescendsFromIntegratedMain     bool   `json:"source_descends_from_integrated_main"`
}

type ExecutionToolIdentity struct {
	Role              string `json:"role"`
	FileType          string `json:"file_type"`
	SHA256            string `json:"sha256"`
	Version           string `json:"version"`
	Provenance        string `json:"provenance"`
	BuildVCSRevision  string `json:"build_vcs_revision,omitempty"`
	BuildVCSModified  bool   `json:"build_vcs_modified"`
	ModulePath        string `json:"module_path,omitempty"`
	ModuleVersion     string `json:"module_version,omitempty"`
	ModuleSum         string `json:"module_sum,omitempty"`
	BuildRecipeSHA256 string `json:"build_recipe_sha256,omitempty"`
}

// CheckoutAdmissionBinding is produced by the T42.2 checkout/tool verifier.
// Its private fields prevent a freeze from authorizing its own Git ancestry,
// cleanliness, or build provenance.
type CheckoutAdmissionBinding struct {
	commits     ExecutionCommits
	toolsSHA256 string
	verified    bool
}

// ExecutionHost separates the admitted backing host from the bounded mounted
// pressure volume. Identities are SHA-256 values, never device or mount paths.
type ExecutionHost struct {
	GOOS                        string `json:"goos"`
	GOARCH                      string `json:"goarch"`
	OSProductVersion            string `json:"os_product_version"`
	OSBuildVersion              string `json:"os_build_version"`
	LogicalCPUs                 uint64 `json:"logical_cpus"`
	MemoryBytes                 uint64 `json:"memory_bytes"`
	BackingTotalDiskBytes       uint64 `json:"backing_total_disk_bytes"`
	BackingAvailableDiskBytes   uint64 `json:"backing_available_disk_bytes"`
	BackingVolumeIdentity       string `json:"backing_volume_identity"`
	PressureTotalDiskBytes      uint64 `json:"pressure_total_disk_bytes"`
	PressureAvailableDiskBytes  uint64 `json:"pressure_available_disk_bytes"`
	PressureAllocationUnitBytes uint64 `json:"pressure_allocation_unit_bytes"`
	DataVolumeIdentity          string `json:"data_volume_identity"`
	BallastVolumeIdentity       string `json:"ballast_volume_identity"`
	VolumeIdentityMethod        string `json:"volume_identity_method"`
}

// ExecutionPressureGeometry is redundant by design: every derived scalar is
// retained so independent review can recompute the admission decision exactly.
type ExecutionPressureGeometry struct {
	Model                       string                   `json:"model"`
	LivePrePressurePolicy       string                   `json:"live_pre_pressure_policy"`
	MinimumPrePressureUsedBytes uint64                   `json:"minimum_pre_pressure_used_bytes"`
	MaximumPrePressureUsedBytes uint64                   `json:"maximum_pre_pressure_used_bytes"`
	MinimumPrePressureBytes     uint64                   `json:"minimum_pre_pressure_bytes"`
	MaximumPrePressureBytes     uint64                   `json:"maximum_pre_pressure_bytes"`
	PressureVolumeBytes         uint64                   `json:"pressure_volume_bytes"`
	BallastCeilingBytes         uint64                   `json:"ballast_ceiling_bytes"`
	CustodyMarginBytes          uint64                   `json:"custody_margin_bytes"`
	Targets                     []PressureTargetGeometry `json:"targets"`
	Recovery                    PressureRecoveryGeometry `json:"recovery"`
	BackingVolumeIdentity       string                   `json:"backing_volume_identity"`
	DataVolumeIdentity          string                   `json:"data_volume_identity"`
	BallastVolumeIdentity       string                   `json:"ballast_volume_identity"`
	SameVolume                  bool                     `json:"same_volume"`
}

type PressureTargetGeometry struct {
	TargetUsedPercent    uint64 `json:"target_used_percent"`
	Action               string `json:"action"`
	ExpectedDisposition  string `json:"expected_disposition"`
	MinimumUsedBytes     uint64 `json:"minimum_used_bytes"`
	MaximumUsedBytes     uint64 `json:"maximum_used_bytes"`
	TargetUsedBytes      uint64 `json:"target_used_bytes"`
	TargetAvailableBytes uint64 `json:"target_available_bytes"`
	ToleranceBytes       uint64 `json:"tolerance_bytes"`
}

type PressureRecoveryGeometry struct {
	Action               string `json:"action"`
	MaximumUsedPercent   uint64 `json:"maximum_used_percent"`
	ExpectedDisposition  string `json:"expected_disposition"`
	RequiredBallastBytes uint64 `json:"required_ballast_bytes"`
}

// BuildExecutionFreeze constructs and validates one prospective execution
// freeze. Tool identities must already have been measured from the executed
// regular files; this package performs no filesystem or process discovery.
func BuildExecutionFreeze(
	plan Plan,
	commits ExecutionCommits,
	tools []ExecutionToolIdentity,
	host ExecutionHost,
	signerFingerprint string,
	checkout CheckoutAdmissionBinding,
	profileAdmission ExecutionProfileAdmissionBinding,
) (ExecutionFreeze, error) {
	planRaw, err := MarshalCanonical(plan)
	if err != nil {
		return ExecutionFreeze{}, fmt.Errorf("marshal T42.1 plan: %w", err)
	}
	pressure, err := expectedExecutionPressureGeometry(plan, host)
	if err != nil {
		return ExecutionFreeze{}, err
	}
	profile, err := expectedExecutionProfile(plan, tools, host, profileAdmission)
	if err != nil {
		return ExecutionFreeze{}, err
	}
	freeze := ExecutionFreeze{
		Schema: plan.ToolPolicy.ExecutionFreezeSchema, PlanSHA256: SHA256(planRaw),
		SignerFingerprint: signerFingerprint,
		Commits:           commits, DigestAlgorithm: plan.ToolPolicy.DigestAlgorithm,
		Tools: append([]ExecutionToolIdentity(nil), tools...),
		Host:  host, Profile: profile, Pressure: pressure,
	}
	if err := ValidateExecutionFreeze(freeze, plan, commits, signerFingerprint, checkout, profileAdmission); err != nil {
		return ExecutionFreeze{}, err
	}
	return freeze, nil
}

// ValidateExecutionFreeze compares a freeze with the exact plan and externally
// selected integration/source commits. Passing the commits from the freeze
// itself would not establish those external authorities.
func ValidateExecutionFreeze(
	freeze ExecutionFreeze,
	plan Plan,
	expectedCommits ExecutionCommits,
	expectedSignerFingerprint string,
	checkout CheckoutAdmissionBinding,
	profileAdmission ExecutionProfileAdmissionBinding,
) error {
	if err := ValidateFrozenPlan(plan); err != nil {
		return fmt.Errorf("validate exact T42.1 plan: %w", err)
	}
	return validateExecutionFreeze(
		freeze, plan, expectedCommits, expectedSignerFingerprint, checkout, profileAdmission,
	)
}

func validateExecutionFreeze(
	freeze ExecutionFreeze,
	plan Plan,
	expectedCommits ExecutionCommits,
	expectedSignerFingerprint string,
	checkout CheckoutAdmissionBinding,
	profileAdmission ExecutionProfileAdmissionBinding,
) error {
	if !validCommit(expectedCommits.IntegratedMainCommit) ||
		!validGitObjectID(expectedCommits.IntegratedMainTree, "sha1") ||
		!validCommit(expectedCommits.T422SourceCommit) ||
		!validGitObjectID(expectedCommits.T422SourceTree, "sha1") ||
		!validSSHSHA256Fingerprint(expectedSignerFingerprint) ||
		plan.ToolPolicy.RequireCleanCommit && !expectedCommits.CleanTree ||
		!expectedCommits.IntegratedMainDescendsFromPlanSource ||
		!expectedCommits.SourceDescendsFromIntegratedMain {
		return errors.New("T42.2 expected commit authority is invalid")
	}
	toolsSHA256, err := canonicalSHA256(freeze.Tools)
	if err != nil || !checkout.verified || checkout.commits != expectedCommits || checkout.toolsSHA256 != toolsSHA256 {
		return errors.New("T42.2 checkout and build provenance lack external admission")
	}
	planRaw, err := MarshalCanonical(plan)
	if err != nil {
		return fmt.Errorf("marshal exact T42.1 plan: %w", err)
	}
	if freeze.Schema != plan.ToolPolicy.ExecutionFreezeSchema ||
		freeze.PlanSHA256 != SHA256(planRaw) ||
		freeze.SignerFingerprint != expectedSignerFingerprint ||
		freeze.Commits != expectedCommits ||
		freeze.DigestAlgorithm != plan.ToolPolicy.DigestAlgorithm {
		return errors.New("T42.2 execution freeze authority differs from the exact plan")
	}
	if err := validateExecutionTools(freeze.Tools, plan.ToolPolicy, expectedCommits.T422SourceCommit); err != nil {
		return err
	}
	if err := validateExecutionHost(freeze.Host, plan); err != nil {
		return err
	}
	if err := validateExecutionProfile(freeze.Profile, plan, freeze.Tools, freeze.Host, profileAdmission); err != nil {
		return err
	}
	wantPressure, err := expectedExecutionPressureGeometry(plan, freeze.Host)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(freeze.Pressure, wantPressure) {
		return errors.New("T42.2 pressure geometry is not the exact computed geometry")
	}
	raw, err := MarshalCanonical(freeze)
	if err != nil {
		return fmt.Errorf("marshal T42.2 execution freeze: %w", err)
	}
	if uint64(len(raw)) > plan.ToolPolicy.MaximumExecutionFreezeBytes {
		return errors.New("T42.2 execution freeze exceeds its byte bound")
	}
	return rejectSourceBearingExecutionFreeze(raw)
}

// DecodeExecutionFreeze accepts only the one canonical JSON representation and
// then applies the complete plan, authority, inventory, and source-free checks.
func DecodeExecutionFreeze(
	raw []byte,
	plan Plan,
	expectedCommits ExecutionCommits,
	expectedSignerFingerprint string,
	checkout CheckoutAdmissionBinding,
	profileAdmission ExecutionProfileAdmissionBinding,
) (ExecutionFreeze, error) {
	if plan.ToolPolicy.MaximumExecutionFreezeBytes == 0 ||
		uint64(len(raw)) > plan.ToolPolicy.MaximumExecutionFreezeBytes ||
		len(raw) > MaxExecutionFreezeBytes {
		return ExecutionFreeze{}, errors.New("T42.2 execution freeze exceeds its byte bound")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var freeze ExecutionFreeze
	if err := decoder.Decode(&freeze); err != nil {
		return ExecutionFreeze{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ExecutionFreeze{}, errors.New("T42.2 execution freeze contains trailing data")
	}
	want, err := MarshalCanonical(freeze)
	if err != nil || !bytes.Equal(raw, want) {
		return ExecutionFreeze{}, errors.New("T42.2 execution freeze is not canonical")
	}
	if err := rejectSourceBearingExecutionFreeze(raw); err != nil {
		return ExecutionFreeze{}, err
	}
	if err := ValidateExecutionFreeze(
		freeze, plan, expectedCommits, expectedSignerFingerprint, checkout, profileAdmission,
	); err != nil {
		return ExecutionFreeze{}, err
	}
	return freeze, nil
}

func validateExecutionTools(tools []ExecutionToolIdentity, policy ToolPolicy, sourceCommit string) error {
	if policy.DigestAlgorithm != "sha256-executed-regular-file-v1" ||
		!policy.FreezeBeforeExecution || len(tools) != len(policy.RequiredTools) {
		return errors.New("T42.2 execution tool inventory differs from the exact plan")
	}
	for index, required := range policy.RequiredTools {
		tool := tools[index]
		if tool.Role != required || tool.FileType != regularFileType ||
			!validExecutionSHA256(tool.SHA256) || !validPublicToolVersion(tool.Version) {
			return fmt.Errorf("T42.2 execution tool %q identity is invalid", required)
		}
		repositoryBuilt := required == "phebs" || required == "phebs-focused-index" ||
			required == "t422-author" || required == "t422-execute"
		buf := required == "buf"
		zoekt := required == "zoekt-git-index"
		wantBufRecipe := recipeDigest(
			"t422-buf-build-recipe-v1", policy.BufModulePath, policy.BufModuleVersion,
			policy.BufModuleSum, policy.BufBuildRecipe,
		)
		wantZoektRecipe := recipeDigest(
			"t422-zoekt-build-recipe-v1", policy.ZoektModulePath, policy.ZoektModuleVersion,
			policy.ZoektModuleSum, policy.ZoektBuildRecipe,
		)
		if repositoryBuilt && (tool.Provenance != "go-build-info-vcs-v1" ||
			tool.BuildVCSRevision != sourceCommit || tool.BuildVCSModified || tool.ModulePath != "" ||
			tool.ModuleVersion != "" || tool.ModuleSum != "" || tool.BuildRecipeSHA256 != "") ||
			buf && (tool.Provenance != "go-module-build-v1" || tool.BuildVCSRevision != "" ||
				tool.BuildVCSModified || tool.ModulePath != policy.BufModulePath ||
				tool.ModuleVersion != policy.BufModuleVersion || tool.ModuleSum != policy.BufModuleSum ||
				tool.BuildRecipeSHA256 != wantBufRecipe) ||
			zoekt && (tool.Provenance != "go-module-build-v1" || tool.BuildVCSRevision != "" ||
				tool.BuildVCSModified || tool.ModulePath != policy.ZoektModulePath ||
				tool.ModuleVersion != policy.ZoektModuleVersion || tool.ModuleSum != policy.ZoektModuleSum ||
				tool.BuildRecipeSHA256 != wantZoektRecipe) ||
			!repositoryBuilt && !buf && !zoekt && (tool.Provenance != "external-executed-file-v1" ||
				tool.BuildVCSRevision != "" || tool.BuildVCSModified || tool.ModulePath != "" ||
				tool.ModuleVersion != "" || tool.ModuleSum != "" || tool.BuildRecipeSHA256 != "") {
			return fmt.Errorf("T42.2 execution tool %q provenance is invalid", required)
		}
	}
	return nil
}

func canonicalSHA256(value any) (string, error) {
	raw, err := MarshalCanonical(value)
	if err != nil {
		return "", err
	}
	return SHA256(raw), nil
}

func validateExecutionHost(host ExecutionHost, plan Plan) error {
	if host.GOOS != "darwin" || host.GOARCH != "arm64" ||
		!validVersionToken(host.OSProductVersion, 32) ||
		!validSanitizedToken(strings.ToLower(host.OSBuildVersion), 32) ||
		host.LogicalCPUs == 0 || host.MemoryBytes < plan.SafetyEnvelope.MinimumMemoryBytes ||
		host.BackingTotalDiskBytes == 0 ||
		host.BackingAvailableDiskBytes > host.BackingTotalDiskBytes ||
		host.BackingAvailableDiskBytes < plan.SafetyEnvelope.MinimumAvailableDiskBytes ||
		host.PressureTotalDiskBytes != plan.SafetyEnvelope.PressureVolumeBytes ||
		host.PressureAvailableDiskBytes > host.PressureTotalDiskBytes ||
		host.PressureAllocationUnitBytes < 512 || host.PressureAllocationUnitBytes > 1<<20 ||
		host.PressureAllocationUnitBytes&(host.PressureAllocationUnitBytes-1) != 0 ||
		!validExecutionSHA256(host.BackingVolumeIdentity) ||
		!validExecutionSHA256(host.DataVolumeIdentity) ||
		!validExecutionSHA256(host.BallastVolumeIdentity) ||
		host.VolumeIdentityMethod != "statfs-fsid-sha256-v1" ||
		host.DataVolumeIdentity != host.BallastVolumeIdentity ||
		host.BackingVolumeIdentity == host.DataVolumeIdentity {
		return errors.New("T42.2 execution host identity or capacity is invalid")
	}
	wantFields := [...]string{
		"backing_available_disk_bytes", "backing_total_disk_bytes", "backing_volume_identity",
		"ballast_volume_identity", "data_volume_identity", "goarch", "goos", "logical_cpus",
		"memory_bytes", "os_build_version", "os_product_version", "pressure_available_disk_bytes", "pressure_total_disk_bytes",
		"pressure_allocation_unit_bytes", "volume_identity_method",
	}
	if len(plan.ToolPolicy.RequiredHostFields) != len(wantFields) {
		return errors.New("T42.2 required host-field inventory is invalid")
	}
	for index, field := range wantFields {
		if plan.ToolPolicy.RequiredHostFields[index] != field {
			return errors.New("T42.2 required host-field inventory is invalid")
		}
	}
	return nil
}

func expectedExecutionPressureGeometry(
	plan Plan,
	host ExecutionHost,
) (ExecutionPressureGeometry, error) {
	total := host.PressureTotalDiskBytes
	if !slices.Equal(plan.SafetyEnvelope.PressureUsedPercents, []uint64{80, 90, 75}) ||
		total != plan.SafetyEnvelope.PressureVolumeBytes || total == 0 ||
		plan.SafetyEnvelope.MinimumPrePressureUsedBytes == 0 ||
		plan.SafetyEnvelope.MinimumPrePressureUsedBytes >= plan.SafetyEnvelope.MaximumPrePressureUsedBytes ||
		plan.SafetyEnvelope.MinimumPrePressureBytes == 0 ||
		plan.SafetyEnvelope.MinimumPrePressureBytes >= plan.SafetyEnvelope.MaximumPrePressureBytes ||
		plan.SafetyEnvelope.MaximumPrePressureUsedBytes >= percentOf(total, 74) ||
		plan.SafetyEnvelope.MaximumPrePressureBytes > plan.SafetyEnvelope.MaximumDataAllocatedBytes {
		return ExecutionPressureGeometry{}, errors.New("T42.2 pressure volume is invalid")
	}
	targets := make([]PressureTargetGeometry, 0, len(plan.SafetyEnvelope.PressureUsedPercents))
	var previous uint64
	for index, target := range plan.SafetyEnvelope.PressureUsedPercents {
		if target == 0 || target >= 100 {
			return ExecutionPressureGeometry{}, errors.New("T42.2 pressure percentage is invalid")
		}
		minimumUsed := percentOf(total, target-1) + 1
		maximumUsed := percentOf(total, target)
		targetUsed := minimumUsed + (maximumUsed-minimumUsed)/2
		targetAvailable := total - targetUsed
		action := "add"
		if index > 0 && targetUsed < previous {
			action = "remove"
		} else if index > 0 && targetUsed == previous {
			return ExecutionPressureGeometry{}, errors.New("T42.2 pressure transition has no ballast action")
		}
		disposition := "collect"
		if target >= 90 || target == 75 {
			disposition = "refuse"
		}
		targets = append(targets, PressureTargetGeometry{
			TargetUsedPercent: target, Action: action,
			ExpectedDisposition: disposition,
			MinimumUsedBytes:    minimumUsed, MaximumUsedBytes: maximumUsed,
			TargetUsedBytes: targetUsed, TargetAvailableBytes: targetAvailable,
			ToleranceBytes: host.PressureAllocationUnitBytes,
		})
		previous = targetUsed
	}
	ceiling := plan.SafetyEnvelope.MaximumPressureBallastBytes
	maximumTarget := targets[1].TargetUsedBytes
	if maximumTarget > plan.SafetyEnvelope.MaximumDataAllocatedBytes ||
		plan.SafetyEnvelope.MaximumDataAllocatedBytes-maximumTarget < plan.SafetyEnvelope.MinimumPressureMarginBytes {
		return ExecutionPressureGeometry{}, errors.New("T42.2 pressure geometry is below its minimum ballast margin")
	}
	return ExecutionPressureGeometry{
		Model: pressureGeometryModel, LivePrePressurePolicy: "collect_noncurrent_no_padding_then_measure_capacity_and_allocated_bytes_before_each_target-v1",
		MinimumPrePressureUsedBytes: plan.SafetyEnvelope.MinimumPrePressureUsedBytes,
		MaximumPrePressureUsedBytes: plan.SafetyEnvelope.MaximumPrePressureUsedBytes,
		MinimumPrePressureBytes:     plan.SafetyEnvelope.MinimumPrePressureBytes,
		MaximumPrePressureBytes:     plan.SafetyEnvelope.MaximumPrePressureBytes,
		PressureVolumeBytes:         total, BallastCeilingBytes: ceiling,
		CustodyMarginBytes: plan.SafetyEnvelope.MaximumDataAllocatedBytes - maximumTarget,
		Targets:            targets,
		Recovery: PressureRecoveryGeometry{
			Action: "remove", MaximumUsedPercent: 74,
			ExpectedDisposition: "normal", RequiredBallastBytes: 0,
		},
		BackingVolumeIdentity: host.BackingVolumeIdentity,
		DataVolumeIdentity:    host.DataVolumeIdentity, BallastVolumeIdentity: host.BallastVolumeIdentity,
		SameVolume: true,
	}, nil
}

// percentOf computes floor(value*percent/100) without overflowing uint64.
func percentOf(value, percent uint64) uint64 {
	return value/100*percent + value%100*percent/100
}

func usedPercentCeiling(used, total uint64) uint64 {
	if used == 0 || total == 0 {
		return 0
	}
	high, low := bits.Mul64(used, 100)
	percent, remainder := bits.Div64(high, low, total)
	if remainder != 0 {
		percent++
	}
	return min(percent, 100)
}

func validSanitizedToken(value string, maximumBytes int) bool {
	if value == "" || len(value) > maximumBytes {
		return false
	}
	for _, current := range []byte(value) {
		if current < 'a' || current > 'z' {
			if current < '0' || current > '9' {
				return false
			}
		}
	}
	return true
}

func validVersionToken(value string, maximumBytes int) bool {
	if value == "" || len(value) > maximumBytes {
		return false
	}
	for _, current := range []byte(value) {
		if current < '0' || current > '9' {
			if current != '.' {
				return false
			}
		}
	}
	return value[0] != '.' && value[len(value)-1] != '.' && !strings.Contains(value, "..")
}

func validExecutionSHA256(value string) bool {
	if !validDigest(value) {
		return false
	}
	for _, current := range value[len("sha256:"):] {
		if current < '0' || current > '9' && current < 'a' || current > 'f' {
			return false
		}
	}
	return true
}

func validPublicToolVersion(value string) bool {
	if value == "" || len(value) > 192 || strings.TrimSpace(value) != value {
		return false
	}
	for _, current := range []byte(value) {
		if current < 0x20 || current > 0x7e || current == '\\' {
			return false
		}
	}
	for index, current := range []byte(value) {
		if current != '/' {
			continue
		}
		if index == 0 {
			return false
		}
		switch value[index-1] {
		case ' ', '=', ':', '(', '[', '{', ',', ';', '\'', '"':
			return false
		}
	}
	return sourceOrWorkspaceFragment([]byte(value)) == ""
}

func validSSHSHA256Fingerprint(value string) bool {
	if len(value) != len("SHA256:")+43 || !strings.HasPrefix(value, "SHA256:") {
		return false
	}
	for _, current := range value[len("SHA256:"):] {
		if current >= 'A' && current <= 'Z' || current >= 'a' && current <= 'z' ||
			current >= '0' && current <= '9' || current == '+' || current == '/' {
			continue
		}
		return false
	}
	return true
}

func rejectSourceBearingExecutionFreeze(raw []byte) error {
	if fragment := sourceOrWorkspaceFragment(raw); fragment != "" {
		return fmt.Errorf("T42.2 source-free execution freeze contains forbidden fragment %q", fragment)
	}
	return nil
}

func sourceOrWorkspaceFragment(raw []byte) string {
	raw = bytes.ToLower(raw)
	for _, fragment := range [...]string{
		".go", ".proto", ".thrift", "services/", "structural/", "example.invalid/",
		"package ", "func ", "syntax =", "/users/", "/home/", "/private/", "/tmp/", "/volumes/",
		"../", "./", "~/", "$home/", "${home}/", "file://",
	} {
		if bytes.Contains(raw, []byte(fragment)) {
			return fragment
		}
	}
	return ""
}
