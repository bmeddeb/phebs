package t421

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
)

var (
	executionFreezeFixtureOnce sync.Once
	executionFreezeFixture     ExecutionFreeze
	executionFreezeFixtureErr  error
)

func TestExecutionFreezeIsCanonicalBoundedAndExact(t *testing.T) {
	plan := frozenTestPlan(t)
	commits := executionFreezeTestCommits()
	tools := executionFreezeTestTools(plan)
	checkout := executionFreezeTestCheckout(t, commits, tools)
	host := executionFreezeTestHost()
	profile := executionProfileTestAdmission(t, plan, tools, host)
	freeze, err := BuildExecutionFreeze(plan, commits, tools, host, executionFreezeTestSigner(), checkout, profile)
	if err != nil {
		t.Fatal(err)
	}
	tools[0].Version = "changed-after-build"
	if freeze.Tools[0].Version == tools[0].Version {
		t.Fatal("execution freeze retained the caller's tool slice")
	}
	raw, err := MarshalCanonical(freeze)
	if err != nil {
		t.Fatal(err)
	}
	if uint64(len(raw)) > plan.ToolPolicy.MaximumExecutionFreezeBytes {
		t.Fatalf("execution freeze bytes = %d, limit = %d", len(raw), plan.ToolPolicy.MaximumExecutionFreezeBytes)
	}
	decoded, err := DecodeExecutionFreeze(raw, plan, commits, executionFreezeTestSigner(), checkout, profile)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, freeze) {
		t.Fatal("strict decode changed the execution freeze")
	}

	pressure := freeze.Pressure
	if pressure.Model != pressureGeometryModel || len(pressure.Targets) != 3 || !pressure.SameVolume ||
		pressure.PressureVolumeBytes != host.PressureTotalDiskBytes ||
		pressure.BackingVolumeIdentity != host.BackingVolumeIdentity ||
		pressure.DataVolumeIdentity != host.DataVolumeIdentity ||
		pressure.BallastVolumeIdentity != host.BallastVolumeIdentity ||
		pressure.BallastCeilingBytes != plan.SafetyEnvelope.MaximumPressureBallastBytes ||
		pressure.MinimumPrePressureUsedBytes != plan.SafetyEnvelope.MinimumPrePressureUsedBytes ||
		pressure.MaximumPrePressureUsedBytes != plan.SafetyEnvelope.MaximumPrePressureUsedBytes ||
		pressure.MinimumPrePressureBytes != plan.SafetyEnvelope.MinimumPrePressureBytes ||
		pressure.MaximumPrePressureBytes != plan.SafetyEnvelope.MaximumPrePressureBytes {
		t.Fatalf("pressure geometry = %+v", pressure)
	}
	for index, want := range []struct {
		percent     uint64
		action      string
		disposition string
	}{
		{80, "add", "collect"},
		{90, "add", "refuse"},
		{75, "remove", "refuse"},
	} {
		target := pressure.Targets[index]
		minimum := percentOf(pressure.PressureVolumeBytes, want.percent-1) + 1
		maximum := percentOf(pressure.PressureVolumeBytes, want.percent)
		midpoint := minimum + (maximum-minimum)/2
		if target.TargetUsedPercent != want.percent || target.Action != want.action ||
			target.ExpectedDisposition != want.disposition || target.MinimumUsedBytes != minimum ||
			target.MaximumUsedBytes != maximum || target.TargetUsedBytes != midpoint ||
			target.TargetAvailableBytes != pressure.PressureVolumeBytes-midpoint {
			t.Fatalf("pressure target %d = %+v", index, target)
		}
	}
	if pressure.CustodyMarginBytes != plan.SafetyEnvelope.MaximumDataAllocatedBytes-pressure.Targets[1].TargetUsedBytes ||
		pressure.CustodyMarginBytes < plan.SafetyEnvelope.MinimumPressureMarginBytes ||
		pressure.Recovery.Action != "remove" || pressure.Recovery.MaximumUsedPercent != 74 ||
		pressure.Recovery.ExpectedDisposition != "normal" || pressure.Recovery.RequiredBallastBytes != 0 {
		t.Fatalf("pressure custody or recovery geometry = %+v", pressure)
	}
}

func TestPressureTargetGeometryHasExactPercentageBoundaries(t *testing.T) {
	pressure := mustExecutionFreeze(t, frozenTestPlan(t), executionFreezeTestCommits()).Pressure
	for _, target := range pressure.Targets {
		t.Run(fmt.Sprintf("percent_%d", target.TargetUsedPercent), func(t *testing.T) {
			checks := []struct {
				name string
				used uint64
				want uint64
			}{
				{"minimum_minus_one", target.MinimumUsedBytes - 1, target.TargetUsedPercent - 1},
				{"minimum", target.MinimumUsedBytes, target.TargetUsedPercent},
				{"maximum", target.MaximumUsedBytes, target.TargetUsedPercent},
				{"maximum_plus_one", target.MaximumUsedBytes + 1, target.TargetUsedPercent + 1},
			}
			for _, check := range checks {
				t.Run(check.name, func(t *testing.T) {
					if got := usedPercentCeiling(check.used, pressure.PressureVolumeBytes); got != check.want {
						t.Fatalf("used percent = %d, want %d", got, check.want)
					}
				})
			}
		})
	}
}

func TestBuildExecutionFreezeRejectsMalformedPressureTargetsWithoutPanic(t *testing.T) {
	tests := []struct {
		name    string
		targets []uint64
	}{
		{"missing", nil},
		{"one", []uint64{80}},
		{"two", []uint64{80, 90}},
		{"extra", []uint64{80, 90, 75, 70}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := clonePlan(t, frozenTestPlan(t))
			plan.SafetyEnvelope.PressureUsedPercents = test.targets
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("BuildExecutionFreeze panicked: %v", recovered)
				}
			}()
			if _, err := BuildExecutionFreeze(
				plan, executionFreezeTestCommits(), executionFreezeTestTools(plan), executionFreezeTestHost(),
				executionFreezeTestSigner(), CheckoutAdmissionBinding{}, ExecutionProfileAdmissionBinding{},
			); err == nil {
				t.Fatal("malformed pressure targets were accepted")
			}
		})
	}
}

func TestDecodeExecutionFreezeRejectsNoncanonicalAndSourceBearing(t *testing.T) {
	plan := frozenTestPlan(t)
	commits := executionFreezeTestCommits()
	tools := executionFreezeTestTools(plan)
	checkout := executionFreezeTestCheckout(t, commits, tools)
	profile := executionProfileTestAdmission(t, plan, tools, executionFreezeTestHost())
	freeze := mustExecutionFreeze(t, plan, commits)
	raw, err := MarshalCanonical(freeze)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		raw  func() []byte
	}{
		{
			name: "unknown field",
			raw: func() []byte {
				return append([]byte("{\n  \"unknown\": true,"), raw[1:]...)
			},
		},
		{name: "trailing value", raw: func() []byte { return append(bytes.Clone(raw), []byte("{}")...) }},
		{
			name: "noncanonical",
			raw: func() []byte {
				compact, marshalErr := json.Marshal(freeze)
				if marshalErr != nil {
					t.Fatal(marshalErr)
				}
				return compact
			},
		},
		{
			name: "oversized",
			raw: func() []byte {
				return bytes.Repeat([]byte{'x'}, int(plan.ToolPolicy.MaximumExecutionFreezeBytes)+1)
			},
		},
		{
			name: "source bearing",
			raw: func() []byte {
				changed := cloneExecutionFreeze(t, freeze)
				changed.Tools[0].Version = "package neutral"
				changedRaw, marshalErr := MarshalCanonical(changed)
				if marshalErr != nil {
					t.Fatal(marshalErr)
				}
				return changedRaw
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := DecodeExecutionFreeze(
				test.raw(), plan, commits, executionFreezeTestSigner(), checkout, profile,
			); err == nil {
				t.Fatal("invalid execution freeze was accepted")
			}
		})
	}
}

func TestValidateExecutionFreezeRejectsAuthorityInventoryAndHostDrift(t *testing.T) {
	plan := frozenTestPlan(t)
	commits := executionFreezeTestCommits()
	tools := executionFreezeTestTools(plan)
	checkout := executionFreezeTestCheckout(t, commits, tools)
	profile := executionProfileTestAdmission(t, plan, tools, executionFreezeTestHost())
	freeze := mustExecutionFreeze(t, plan, commits)
	otherDigest := SHA256([]byte("other"))
	otherCommit := strings.Repeat("d", 40)
	unadmitted := commits
	unadmitted.IntegratedMainCommit = otherCommit
	unadmitted.IntegratedMainTree = strings.Repeat("3", 40)

	tests := []struct {
		name            string
		mutate          func(*ExecutionFreeze)
		expectedCommits ExecutionCommits
	}{
		{name: "schema", mutate: func(value *ExecutionFreeze) { value.Schema = "other" }, expectedCommits: commits},
		{name: "plan digest", mutate: func(value *ExecutionFreeze) { value.PlanSHA256 = otherDigest }, expectedCommits: commits},
		{name: "integrated main", mutate: func(value *ExecutionFreeze) { value.Commits.IntegratedMainCommit = otherCommit }, expectedCommits: commits},
		{name: "T42.2 source", mutate: func(value *ExecutionFreeze) { value.Commits.T422SourceCommit = otherCommit }, expectedCommits: commits},
		{name: "clean tree", mutate: func(value *ExecutionFreeze) { value.Commits.CleanTree = false }, expectedCommits: commits},
		{name: "well-formed unadmitted commit and tree", mutate: func(value *ExecutionFreeze) { value.Commits = unadmitted }, expectedCommits: unadmitted},
		{name: "missing tool", mutate: func(value *ExecutionFreeze) { value.Tools = value.Tools[:len(value.Tools)-1] }, expectedCommits: commits},
		{name: "tool order", mutate: func(value *ExecutionFreeze) { value.Tools[0], value.Tools[1] = value.Tools[1], value.Tools[0] }, expectedCommits: commits},
		{name: "nonregular tool", mutate: func(value *ExecutionFreeze) { value.Tools[0].FileType = "symlink" }, expectedCommits: commits},
		{name: "tool digest", mutate: func(value *ExecutionFreeze) { value.Tools[0].SHA256 = "bad" }, expectedCommits: commits},
		{name: "tool version path", mutate: func(value *ExecutionFreeze) { value.Tools[0].Version = "browser /Users/private/tool" }, expectedCommits: commits},
		{name: "OS product version", mutate: func(value *ExecutionFreeze) { value.Host.OSProductVersion = "macOS 15.6" }, expectedCommits: commits},
		{name: "OS build version", mutate: func(value *ExecutionFreeze) { value.Host.OSBuildVersion = "24G84 beta" }, expectedCommits: commits},
		{name: "backing capacity", mutate: func(value *ExecutionFreeze) {
			value.Host.BackingAvailableDiskBytes = plan.SafetyEnvelope.MinimumAvailableDiskBytes - 1
		}, expectedCommits: commits},
		{name: "pressure capacity", mutate: func(value *ExecutionFreeze) { value.Host.PressureTotalDiskBytes-- }, expectedCommits: commits},
		{name: "raw backing identity", mutate: func(value *ExecutionFreeze) { value.Host.BackingVolumeIdentity = "/dev/private-volume" }, expectedCommits: commits},
		{name: "backing equals data", mutate: func(value *ExecutionFreeze) { value.Host.BackingVolumeIdentity = value.Host.DataVolumeIdentity }, expectedCommits: commits},
		{name: "different ballast volume", mutate: func(value *ExecutionFreeze) { value.Host.BallastVolumeIdentity = otherDigest }, expectedCommits: commits},
		{name: "same-volume claim", mutate: func(value *ExecutionFreeze) { value.Pressure.SameVolume = false }, expectedCommits: commits},
		{name: "custody margin", mutate: func(value *ExecutionFreeze) { value.Pressure.CustodyMarginBytes-- }, expectedCommits: commits},
		{name: "execution profile", mutate: func(value *ExecutionFreeze) { value.Profile.InvocationSHA256 = otherDigest }, expectedCommits: commits},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := cloneExecutionFreeze(t, freeze)
			test.mutate(&changed)
			// BuildExecutionFreeze above already validated the exact plan; each row
			// targets the remaining freeze boundary without rebuilding the corpus.
			if err := validateExecutionFreeze(
				changed, plan, test.expectedCommits, executionFreezeTestSigner(), checkout, profile,
			); err == nil {
				t.Fatal("mutated execution freeze was accepted")
			}
		})
	}
}

func TestBuildExecutionFreezeRejectsProfileAdmissionDrift(t *testing.T) {
	plan := frozenTestPlan(t)
	commits := executionFreezeTestCommits()
	tools := executionFreezeTestTools(plan)
	host := executionFreezeTestHost()
	profile := executionProfileTestAdmission(t, plan, tools, host)
	profile.closedEnvironment = false
	if _, err := BuildExecutionFreeze(
		plan, commits, tools, host, executionFreezeTestSigner(),
		executionFreezeTestCheckout(t, commits, tools), profile,
	); err == nil {
		t.Fatal("drifted execution-profile admission was accepted")
	}
}

func TestValidateExecutionFreezeRejectsAdmittedInvalidToolProvenance(t *testing.T) {
	plan := frozenTestPlan(t)
	commits := executionFreezeTestCommits()
	freeze := mustExecutionFreeze(t, plan, commits)

	tests := []struct {
		name   string
		mutate func(*ExecutionToolIdentity)
		index  int
	}{
		{
			name:  "repository tool external provenance",
			index: slices.Index(plan.ToolPolicy.RequiredTools, "phebs"),
			mutate: func(tool *ExecutionToolIdentity) {
				tool.Provenance = "external-executed-file-v1"
				tool.BuildVCSRevision = ""
			},
		},
		{
			name:  "repository tool wrong revision",
			index: slices.Index(plan.ToolPolicy.RequiredTools, "t422-author"),
			mutate: func(tool *ExecutionToolIdentity) {
				tool.BuildVCSRevision = strings.Repeat("d", 40)
			},
		},
		{
			name:  "repository tool modified",
			index: slices.Index(plan.ToolPolicy.RequiredTools, "t422-execute"),
			mutate: func(tool *ExecutionToolIdentity) {
				tool.BuildVCSModified = true
			},
		},
		{
			name:  "external tool repository provenance",
			index: slices.Index(plan.ToolPolicy.RequiredTools, "git"),
			mutate: func(tool *ExecutionToolIdentity) {
				tool.Provenance = "go-build-info-vcs-v1"
				tool.BuildVCSRevision = commits.T422SourceCommit
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.index < 0 {
				t.Fatal("required test tool is absent")
			}
			changed := cloneExecutionFreeze(t, freeze)
			test.mutate(&changed.Tools[test.index])
			checkout := executionFreezeTestCheckout(t, commits, changed.Tools)
			if err := validateExecutionFreeze(
				changed, plan, commits, executionFreezeTestSigner(), checkout,
				executionProfileTestAdmission(t, plan, changed.Tools, changed.Host),
			); err == nil {
				t.Fatal("invalid admitted tool provenance was accepted")
			}
		})
	}
}

func executionFreezeTestCommits() ExecutionCommits {
	return ExecutionCommits{
		IntegratedMainCommit:                 strings.Repeat("b", 40),
		IntegratedMainTree:                   strings.Repeat("1", 40),
		T422SourceCommit:                     strings.Repeat("c", 40),
		T422SourceTree:                       strings.Repeat("2", 40),
		CleanTree:                            true,
		IntegratedMainDescendsFromPlanSource: true,
		SourceDescendsFromIntegratedMain:     true,
	}
}

func executionFreezeTestTools(plan Plan) []ExecutionToolIdentity {
	tools := make([]ExecutionToolIdentity, 0, len(plan.ToolPolicy.RequiredTools))
	for _, role := range plan.ToolPolicy.RequiredTools {
		version := "version-1.0.0"
		if role == "go" {
			version = "go version go1.26.1 darwin/arm64"
		}
		tool := ExecutionToolIdentity{
			Role: role, FileType: regularFileType,
			SHA256: SHA256([]byte("t422-tool-" + role)), Version: version,
			Provenance: "external-executed-file-v1",
		}
		switch role {
		case "phebs", "phebs-focused-index", "t422-author", "t422-execute":
			tool.Provenance = "go-build-info-vcs-v1"
			tool.BuildVCSRevision = executionFreezeTestCommits().T422SourceCommit
		case "buf":
			tool.Provenance = "go-module-build-v1"
			tool.ModulePath = plan.ToolPolicy.BufModulePath
			tool.ModuleVersion = plan.ToolPolicy.BufModuleVersion
			tool.ModuleSum = plan.ToolPolicy.BufModuleSum
			tool.BuildRecipeSHA256 = recipeDigest(
				"t422-buf-build-recipe-v1", tool.ModulePath, tool.ModuleVersion,
				tool.ModuleSum, plan.ToolPolicy.BufBuildRecipe,
			)
		case "zoekt-git-index":
			tool.Provenance = "go-module-build-v1"
			tool.ModulePath = plan.ToolPolicy.ZoektModulePath
			tool.ModuleVersion = plan.ToolPolicy.ZoektModuleVersion
			tool.ModuleSum = plan.ToolPolicy.ZoektModuleSum
			tool.BuildRecipeSHA256 = recipeDigest(
				"t422-zoekt-build-recipe-v1", tool.ModulePath, tool.ModuleVersion,
				tool.ModuleSum, plan.ToolPolicy.ZoektBuildRecipe,
			)
		}
		tools = append(tools, tool)
	}
	return tools
}

func executionFreezeTestHost() ExecutionHost {
	return ExecutionHost{
		GOOS: "darwin", GOARCH: "arm64", OSProductVersion: "15.6", OSBuildVersion: "24G84",
		LogicalCPUs: 8, MemoryBytes: 32 << 30,
		BackingTotalDiskBytes: 320 << 30, BackingAvailableDiskBytes: 160 << 30,
		BackingVolumeIdentity:       SHA256([]byte("t422-test-backing-volume")),
		PressureTotalDiskBytes:      96 << 30,
		PressureAvailableDiskBytes:  96 << 30,
		PressureAllocationUnitBytes: 4096,
		DataVolumeIdentity:          SHA256([]byte("t422-test-pressure-volume")),
		BallastVolumeIdentity:       SHA256([]byte("t422-test-pressure-volume")),
		VolumeIdentityMethod:        "statfs-fsid-sha256-v1",
	}
}

func executionFreezeTestSigner() string {
	return "SHA256:" + strings.Repeat("A", 43)
}

func mustExecutionFreeze(t *testing.T, plan Plan, commits ExecutionCommits) ExecutionFreeze {
	t.Helper()
	executionFreezeFixtureOnce.Do(func() {
		tools := executionFreezeTestTools(plan)
		host := executionFreezeTestHost()
		executionFreezeFixture, executionFreezeFixtureErr = BuildExecutionFreeze(
			plan, commits, tools, host, executionFreezeTestSigner(),
			executionFreezeTestCheckout(t, commits, tools),
			executionProfileTestAdmission(t, plan, tools, host),
		)
	})
	if executionFreezeFixtureErr != nil {
		t.Fatal(executionFreezeFixtureErr)
	}
	return cloneExecutionFreeze(t, executionFreezeFixture)
}

func executionProfileTestAdmission(
	t *testing.T,
	plan Plan,
	tools []ExecutionToolIdentity,
	host ExecutionHost,
) ExecutionProfileAdmissionBinding {
	t.Helper()
	admission := ExecutionProfileAdmissionBinding{
		schema:                    ExecutionProfileSchema,
		harnessCommandSetSHA256:   SHA256([]byte("t422-test-harness-commands")),
		pressureCommandSetSHA256:  SHA256([]byte("t422-test-pressure-commands")),
		configBytesSHA256:         SHA256([]byte("t422-test-config")),
		recoveryEnvironmentSHA256: SHA256([]byte("t422-test-recovery-environment")),
		serverEnvironmentSHA256:   SHA256([]byte("t422-test-server-environment")),
		rootVolumeBindingsSHA256:  SHA256([]byte("t422-test-root-volume-bindings")),
		closedEnvironment:         true,
		verifiedBeforeWork:        true,
	}
	var err error
	if plan.Schema == PlanV2Schema {
		for _, epoch := range correctedExecutionServerEpochs() {
			state := slices.IndexFunc(plan.PhaseStates, func(value PhaseState) bool { return value.Phase == epoch.LaunchPhase })
			admission.epochConfigBytesSHA256 = append(admission.epochConfigBytesSHA256, SHA256([]byte("t422-test-config/"+plan.PhaseStates[state].LogicalRevision)))
		}
		admission.configBytesSHA256, err = canonicalSHA256(admission.epochConfigBytesSHA256)
		if err != nil {
			t.Fatal(err)
		}
	}
	admission.commandsSHA256, err = canonicalSHA256(frozenExecutionCommands())
	if err != nil {
		t.Fatal(err)
	}
	config := frozenExecutionConfig(plan, admission.configBytesSHA256)
	admission.configProjectionSHA256, err = executionConfigProjectionSHA256(config)
	if err != nil {
		t.Fatal(err)
	}
	config.ProjectionSHA256 = admission.configProjectionSHA256
	epochs, err := admittedExecutionServerEpochs(plan, admission)
	if err != nil {
		t.Fatal(err)
	}
	profile := ExecutionProfile{
		Schema:                   plan.ToolPolicy.ExecutionProfileSchema,
		Posture:                  "ordinary-production-workers-exact-v1",
		RuntimeBindingSchema:     PhaseRuntimeBindingSchema,
		PhaseRecipeSHA256:        executionPhaseRecipeSHA256(plan),
		Commands:                 frozenExecutionCommands(),
		HarnessCommandSetSHA256:  admission.harnessCommandSetSHA256,
		PressureCommandSetSHA256: admission.pressureCommandSetSHA256,
		Environment:              frozenExecutionEnvironment(admission),
		Config:                   config,
		Runtime:                  frozenExecutionRuntime(plan),
		Roots:                    frozenExecutionRoots(host, admission.rootVolumeBindingsSHA256),
		Epochs:                   epochs,
	}
	profile.InvocationSHA256, err = executionInvocationSHA256(profile, tools)
	if err != nil {
		t.Fatal(err)
	}
	admission.invocationSHA256 = profile.InvocationSHA256
	admission.profileSHA256, err = canonicalSHA256(profile)
	if err != nil {
		t.Fatal(err)
	}
	return admission
}

func executionFreezeTestCheckout(
	t *testing.T,
	commits ExecutionCommits,
	tools []ExecutionToolIdentity,
) CheckoutAdmissionBinding {
	t.Helper()
	toolsSHA256, err := canonicalSHA256(tools)
	if err != nil {
		t.Fatal(err)
	}
	return CheckoutAdmissionBinding{commits: commits, toolsSHA256: toolsSHA256, verified: true}
}

func cloneExecutionFreeze(t *testing.T, value ExecutionFreeze) ExecutionFreeze {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var result ExecutionFreeze
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func clonePlan(t *testing.T, value Plan) Plan {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var result Plan
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
