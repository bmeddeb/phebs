package t421

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
)

const (
	ExecutionProfileSchema    = "t422-ordinary-production-execution-profile-v1"
	PhaseRuntimeBindingSchema = "t422-phase-runtime-binding-v1"
)

// ExecutionProfile is the canonical, source-free projection of the commands,
// environment, configuration, runtime constants, and root roles admitted for
// one execution. Dynamic paths and secrets are represented only by role tokens
// or by digests measured by the external admission verifier.
type ExecutionProfile struct {
	Schema                   string                        `json:"schema"`
	Posture                  string                        `json:"posture"`
	RuntimeBindingSchema     string                        `json:"runtime_binding_schema"`
	PhaseRecipeSHA256        string                        `json:"phase_recipe_sha256"`
	Commands                 []ExecutionCommandProfile     `json:"commands"`
	HarnessCommandSetSHA256  string                        `json:"harness_command_set_sha256"`
	PressureCommandSetSHA256 string                        `json:"pressure_command_set_sha256"`
	Environment              ExecutionEnvironmentProfile   `json:"environment"`
	Config                   ExecutionConfigProfile        `json:"config"`
	Runtime                  ExecutionRuntimeProfile       `json:"runtime"`
	Roots                    ExecutionRootProfile          `json:"roots"`
	Epochs                   []ExecutionServerEpochProfile `json:"server_epochs"`
	InvocationSHA256         string                        `json:"invocation_sha256"`
}

type ExecutionCommandProfile struct {
	Name             string   `json:"name"`
	ToolRole         string   `json:"tool_role"`
	EnvironmentClass string   `json:"environment_class"`
	NormalizedArgv   []string `json:"normalized_argv"`
}

type ExecutionEnvironmentProfile struct {
	Schema              string   `json:"schema"`
	Policy              string   `json:"policy"`
	Canonicalization    string   `json:"canonicalization"`
	BaseVariables       []string `json:"base_variables"`
	ServerVariables     []string `json:"server_variables"`
	RecoverySHA256      string   `json:"recovery_sha256"`
	ServerSHA256        string   `json:"server_sha256"`
	RejectUnlisted      bool     `json:"reject_unlisted"`
	RejectDemoVariables bool     `json:"reject_demo_variables"`
}

type ExecutionConfigProfile struct {
	Schema                           string   `json:"schema"`
	Policy                           string   `json:"policy"`
	BytesSHA256                      string   `json:"bytes_sha256"`
	ProjectionSHA256                 string   `json:"projection_sha256"`
	ListenSurface                    string   `json:"listen_surface"`
	AddressOverride                  bool     `json:"address_override"`
	StoreMode                        string   `json:"store_mode"`
	Authentication                   string   `json:"authentication"`
	ConnectionPosture                string   `json:"connection_posture"`
	ConnectionCount                  uint64   `json:"connection_count"`
	ServiceCatalogRuntime            string   `json:"service_catalog_runtime"`
	ServiceCatalogCount              uint64   `json:"service_catalog_count"`
	AnalysisUnitPosture              string   `json:"analysis_unit_posture"`
	LifecycleEnabled                 bool     `json:"lifecycle_enabled"`
	SyncPollMilliseconds             uint64   `json:"sync_poll_milliseconds"`
	ResyncDisabled                   bool     `json:"resync_disabled"`
	DiagnosticsJobs                  bool     `json:"diagnostics_jobs"`
	DiagnosticsCandidates            bool     `json:"diagnostics_candidates"`
	DiagnosticsExtraction            bool     `json:"diagnostics_extraction"`
	DiagnosticsExtractorDetails      bool     `json:"diagnostics_extractor_details"`
	ProvisionalProtoExtraction       bool     `json:"provisional_proto_extraction"`
	ProvisionalThriftExtraction      bool     `json:"provisional_thrift_extraction"`
	ProvisionalThriftFieldExtraction bool     `json:"provisional_thrift_field_extraction"`
	ProvisionalKafkaExtraction       bool     `json:"provisional_kafka_extraction"`
	ProvisionalWorkbench             bool     `json:"provisional_workbench"`
	EnabledExtractorDomains          []string `json:"enabled_extractor_domains"`
	AbsentOptionalConfiguration      []string `json:"absent_optional_configuration"`
}

type ExecutionRuntimeProfile struct {
	Schema                         string `json:"schema"`
	StoreRunnerConcurrencyPerKind  uint64 `json:"store_runner_concurrency_per_kind"`
	StoreRunnerMaxAttempts         uint64 `json:"store_runner_max_attempts"`
	GenerationMaxAttempts          uint64 `json:"generation_max_attempts"`
	ObservationIOConcurrency       uint64 `json:"observation_io_concurrency"`
	ObservationCPUConcurrency      uint64 `json:"observation_cpu_concurrency"`
	RelationshipConcurrency        uint64 `json:"relationship_concurrency"`
	ExtractionConcurrency          uint64 `json:"extraction_concurrency"`
	ObservationRepositoryTokens    uint64 `json:"observation_repository_tokens"`
	RelationshipRepositoryTokens   uint64 `json:"relationship_repository_tokens"`
	ExtractionRepositoryTokens     uint64 `json:"extraction_repository_tokens"`
	MaximumStoreRowsPerTransaction uint64 `json:"maximum_store_rows_per_transaction"`
	MaximumLifecycleDeletesPerTurn uint64 `json:"maximum_lifecycle_deletes_per_turn"`
	MaximumAggregatePartitions     uint64 `json:"maximum_aggregate_partitions"`
}

type ExecutionRootProfile struct {
	Schema                    string `json:"schema"`
	BackingRootRole           string `json:"backing_root_role"`
	SourceRootRole            string `json:"source_root_role"`
	ConfigRootRole            string `json:"config_root_role"`
	DataRootRole              string `json:"data_root_role"`
	BallastRootRole           string `json:"ballast_root_role"`
	BackupRootRole            string `json:"backup_root_role"`
	BackingVolumeIdentity     string `json:"backing_volume_identity"`
	DataVolumeIdentity        string `json:"data_volume_identity"`
	BallastVolumeIdentity     string `json:"ballast_volume_identity"`
	PressureRootsOnSameVolume bool   `json:"pressure_roots_on_same_volume"`
	BindingSHA256             string `json:"binding_sha256"`
}

type ExecutionServerEpochProfile struct {
	ServerEpoch            uint64   `json:"server_epoch"`
	LaunchPhase            string   `json:"launch_phase"`
	Phases                 []string `json:"phases"`
	LogicalRevision        string   `json:"logical_revision,omitempty"`
	CatalogSourceSHA256    string   `json:"catalog_source_sha256,omitempty"`
	ConfigBytesSHA256      string   `json:"config_bytes_sha256,omitempty"`
	ServerHealthDeadlineMS uint64   `json:"server_health_deadline_ms,omitempty"`
}

// ExecutionProfileAdmissionBinding is issued by the T42.2 private launcher
// after it has measured the real argv, closed environments, config bytes and
// semantic projection, and root-volume bindings before the first child launch.
// Private fields prevent an execution freeze from admitting itself.
type ExecutionProfileAdmissionBinding struct {
	schema                    string
	commandsSHA256            string
	harnessCommandSetSHA256   string
	pressureCommandSetSHA256  string
	configBytesSHA256         string
	epochConfigBytesSHA256    []string
	configProjectionSHA256    string
	recoveryEnvironmentSHA256 string
	serverEnvironmentSHA256   string
	profileSHA256             string
	invocationSHA256          string
	rootVolumeBindingsSHA256  string
	closedEnvironment         bool
	verifiedBeforeWork        bool
}

// PhaseRuntimeBinding ties one phase observation to the exact admitted serve
// invocation and to the source-free identity of the server process epoch that
// produced it.
type PhaseRuntimeBinding struct {
	Schema                string `json:"schema"`
	Phase                 string `json:"phase"`
	ProfileSHA256         string `json:"profile_sha256"`
	InvocationSHA256      string `json:"invocation_sha256"`
	ProcessImageSHA256    string `json:"process_image_sha256"`
	ProcessIdentitySHA256 string `json:"process_identity_sha256"`
	ServerEpoch           uint64 `json:"server_epoch"`
	StartEventOrdinal     uint64 `json:"start_event_ordinal"`
}

func expectedExecutionProfile(
	plan Plan,
	tools []ExecutionToolIdentity,
	host ExecutionHost,
	admission ExecutionProfileAdmissionBinding,
) (ExecutionProfile, error) {
	if admission.schema != ExecutionProfileSchema ||
		!validExecutionSHA256(admission.harnessCommandSetSHA256) ||
		!validExecutionSHA256(admission.pressureCommandSetSHA256) ||
		!validExecutionSHA256(admission.configBytesSHA256) ||
		!validExecutionSHA256(admission.recoveryEnvironmentSHA256) ||
		!validExecutionSHA256(admission.serverEnvironmentSHA256) ||
		admission.recoveryEnvironmentSHA256 == admission.serverEnvironmentSHA256 ||
		!validExecutionSHA256(admission.rootVolumeBindingsSHA256) ||
		!admission.closedEnvironment || !admission.verifiedBeforeWork {
		return ExecutionProfile{}, errors.New("T42.2 execution profile lacks external pre-work admission")
	}
	commands := frozenExecutionCommands()
	commandsSHA256, err := canonicalSHA256(commands)
	if err != nil {
		return ExecutionProfile{}, err
	}
	config := frozenExecutionConfig(plan, admission.configBytesSHA256)
	config.ProjectionSHA256, err = executionConfigProjectionSHA256(config)
	if err != nil {
		return ExecutionProfile{}, err
	}
	epochs, err := admittedExecutionServerEpochs(plan, admission)
	if err != nil {
		return ExecutionProfile{}, err
	}
	profile := ExecutionProfile{
		Schema:                   plan.ToolPolicy.ExecutionProfileSchema,
		Posture:                  "ordinary-production-workers-exact-v1",
		RuntimeBindingSchema:     PhaseRuntimeBindingSchema,
		PhaseRecipeSHA256:        executionPhaseRecipeSHA256(plan),
		Commands:                 commands,
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
		return ExecutionProfile{}, err
	}
	profileSHA256, err := canonicalSHA256(profile)
	if err != nil {
		return ExecutionProfile{}, err
	}
	if admission.commandsSHA256 != commandsSHA256 ||
		admission.configProjectionSHA256 != config.ProjectionSHA256 ||
		admission.profileSHA256 != profileSHA256 ||
		admission.invocationSHA256 != profile.InvocationSHA256 {
		return ExecutionProfile{}, errors.New("T42.2 execution profile differs from its external admission")
	}
	return profile, nil
}

func validateExecutionProfile(
	profile ExecutionProfile,
	plan Plan,
	tools []ExecutionToolIdentity,
	host ExecutionHost,
	admission ExecutionProfileAdmissionBinding,
) error {
	want, err := expectedExecutionProfile(plan, tools, host, admission)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(profile, want) {
		return errors.New("T42.2 execution profile differs from the exact admitted profile")
	}
	return nil
}

func frozenExecutionCommands() []ExecutionCommandProfile {
	return []ExecutionCommandProfile{
		{Name: "backup", ToolRole: "phebs", EnvironmentClass: "recovery", NormalizedArgv: []string{"backup", "-config", "@config", "-output", "@backup"}},
		{Name: "restore", ToolRole: "phebs", EnvironmentClass: "recovery", NormalizedArgv: []string{"restore", "-config", "@config", "-backup", "@backup"}},
		{Name: "serve", ToolRole: "phebs", EnvironmentClass: "server", NormalizedArgv: []string{"serve", "-config", "@config"}},
	}
}

func frozenExecutionEnvironment(admission ExecutionProfileAdmissionBinding) ExecutionEnvironmentProfile {
	return ExecutionEnvironmentProfile{
		Schema:           "t422-closed-execution-environment-v1",
		Policy:           "closed-exact-name-and-value-set-v1",
		Canonicalization: "sort-by-name;role-tokenize-private-paths;sha256-length-framed-name-value-records-v1",
		BaseVariables: []string{
			"CGO_ENABLED=0", "GIT_ATTR_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=@null-device",
			"GIT_CONFIG_NOSYSTEM=1", "GIT_NO_LAZY_FETCH=1", "GIT_NO_REPLACE_OBJECTS=1",
			"GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "GOARCH=@goarch",
			"GOCACHE=@build-cache", "GOENV=off", "GOEXPERIMENT=", "GOFIPS140=off",
			"GOFLAGS=-mod=readonly", "GOMODCACHE=@module-cache", "GOOS=@goos",
			"GOPROXY=off", "GOSUMDB=off", "GOTELEMETRY=off", "GOTOOLCHAIN=local",
			"GOWORK=off", "HOME=@home", "LANG=C", "LC_ALL=C", "PATH=@git-exec",
			"PHEBS_BUF_SHA256=@buf-sha256", "PHEBS_FOCUSED_INDEX_SHA256=@focused-index-sha256",
			"PHEBS_SURREAL=@surreal", "PHEBS_SURREAL_SHA256=@surreal-sha256",
			"PHEBS_ZOEKT_GIT_INDEX_SHA256=@zoekt-git-index-sha256", "TEMP=@temp",
			"TMP=@temp", "TMPDIR=@temp", "TZ=UTC", "XDG_CACHE_HOME=@temp",
			"XDG_CONFIG_HOME=@home", "XDG_DATA_HOME=@home",
		},
		ServerVariables: []string{
			"PHEBS_BUF=@buf", "PHEBS_FOCUSED_INDEX=@focused-index",
			"PHEBS_T4013_EXACT_REPORTS=source-free-v1",
			"PHEBS_T4013_STARTUP_DIAGNOSTICS=source-free-v1",
			"PHEBS_ZOEKT_GIT_INDEX=@zoekt-git-index",
		},
		RecoverySHA256: admission.recoveryEnvironmentSHA256,
		ServerSHA256:   admission.serverEnvironmentSHA256,
		RejectUnlisted: true, RejectDemoVariables: true,
	}
}

func frozenExecutionConfig(plan Plan, bytesSHA256 string) ExecutionConfigProfile {
	value := ExecutionConfigProfile{
		Schema: "t422-execution-config-projection-v1", Policy: "exact-bytes-and-closed-semantic-projection-v1",
		BytesSHA256: bytesSHA256, ListenSurface: "ipv4-loopback-reserved-config-only-v1", AddressOverride: false,
		StoreMode: "supervised-local-surrealkv-v1", Authentication: "generated-single-tenant-api-key-loopback-cookie-v1",
		ConnectionPosture: "one-watched-local-git-v1", ConnectionCount: 1,
		ServiceCatalogRuntime: "v3", ServiceCatalogCount: 1,
		AnalysisUnitPosture: "absent-whole-repository-root-unbound-v1",
		LifecycleEnabled:    true, SyncPollMilliseconds: 250, ResyncDisabled: true,
		DiagnosticsJobs: true, DiagnosticsCandidates: true, DiagnosticsExtraction: true,
		DiagnosticsExtractorDetails: false,
		ProvisionalProtoExtraction:  true, ProvisionalThriftExtraction: true,
		ProvisionalThriftFieldExtraction: false, ProvisionalKafkaExtraction: true,
		ProvisionalWorkbench: false,
		EnabledExtractorDomains: []string{
			"grpc-caller", "grpc-consumer", "kafka-consumer", "kafka-producer", "proto-contract",
			"scip-proto-field", "thrift-caller", "thrift-consumer", "thrift-contract",
		},
		AbsentOptionalConfiguration: []string{
			"analysis_units", "contexts", "demo_environment", "permissions", "revisions", "webhook",
		},
	}
	if plan.Schema == PlanV2Schema {
		value.Schema = "t422-execution-config-projection-v2"
		value.Policy = "ordered-epoch-config-bytes-set-and-closed-semantic-projection-v2"
	}
	return value
}

func executionConfigProjectionSHA256(value ExecutionConfigProfile) (string, error) {
	value.BytesSHA256 = ""
	value.ProjectionSHA256 = ""
	return canonicalSHA256(value)
}

func frozenExecutionRuntime(plan Plan) ExecutionRuntimeProfile {
	return ExecutionRuntimeProfile{
		Schema:                        "t422-production-runtime-constants-v1",
		StoreRunnerConcurrencyPerKind: 1, StoreRunnerMaxAttempts: 3, GenerationMaxAttempts: 5,
		ObservationIOConcurrency: 1, ObservationCPUConcurrency: 2,
		RelationshipConcurrency: 1, ExtractionConcurrency: 2,
		ObservationRepositoryTokens: 2, RelationshipRepositoryTokens: 1, ExtractionRepositoryTokens: 1,
		MaximumStoreRowsPerTransaction: plan.WorkEnvelope.MaximumStoreRowsPerTransaction,
		MaximumLifecycleDeletesPerTurn: plan.WorkEnvelope.MaximumLifecycleDeletesPerTurn,
		MaximumAggregatePartitions:     plan.WorkEnvelope.MaximumAggregatePartitions,
	}
}

func frozenExecutionRoots(host ExecutionHost, bindingSHA256 string) ExecutionRootProfile {
	return ExecutionRootProfile{
		Schema:                    "t422-execution-root-roles-v1",
		BackingRootRole:           "pressure-image-on-admitted-backing-volume-v1",
		SourceRootRole:            "authored-source-on-mounted-pressure-volume-v1",
		ConfigRootRole:            "config-and-catalog-on-mounted-pressure-volume-v1",
		DataRootRole:              "server-data-on-mounted-pressure-volume-v1",
		BallastRootRole:           "pressure-ballast-on-mounted-pressure-volume-v1",
		BackupRootRole:            "archive-on-mounted-pressure-volume-v1",
		BackingVolumeIdentity:     host.BackingVolumeIdentity,
		DataVolumeIdentity:        host.DataVolumeIdentity,
		BallastVolumeIdentity:     host.BallastVolumeIdentity,
		PressureRootsOnSameVolume: host.DataVolumeIdentity == host.BallastVolumeIdentity,
		BindingSHA256:             bindingSHA256,
	}
}

func frozenExecutionServerEpochs() []ExecutionServerEpochProfile {
	return []ExecutionServerEpochProfile{
		{ServerEpoch: 1, LaunchPhase: "cold", Phases: []string{"cold", "warm_noop", "physical_delta_b", "logical_delta_b", "return_a", "stale_lease"}},
		{ServerEpoch: 2, LaunchPhase: "process_restart", Phases: []string{"process_restart", "pressure_80", "pressure_90", "pressure_75"}},
		{ServerEpoch: 3, LaunchPhase: "archive_restore", Phases: []string{"archive_restore", "lifecycle_collection", "product_queries"}},
	}
}

func admittedExecutionServerEpochs(plan Plan, admission ExecutionProfileAdmissionBinding) ([]ExecutionServerEpochProfile, error) {
	if plan.Schema != PlanV2Schema {
		if len(admission.epochConfigBytesSHA256) != 0 {
			return nil, errors.New("v1 admission retained prospective epoch configs")
		}
		return frozenExecutionServerEpochs(), nil
	}
	epochs := correctedExecutionServerEpochs()
	if len(admission.epochConfigBytesSHA256) != len(epochs) {
		return nil, errors.New("each derived epoch requires externally admitted config bytes")
	}
	digest, err := canonicalSHA256(admission.epochConfigBytesSHA256)
	if err != nil || digest != admission.configBytesSHA256 {
		return nil, errors.New("epoch config set differs from admission")
	}
	for index := range epochs {
		epoch := &epochs[index]
		state := slices.IndexFunc(plan.PhaseStates, func(value PhaseState) bool { return value.Phase == epoch.LaunchPhase })
		if state < 0 || !validExecutionSHA256(admission.epochConfigBytesSHA256[index]) {
			return nil, errors.New("epoch configuration binding is invalid")
		}
		epoch.LogicalRevision = plan.PhaseStates[state].LogicalRevision
		logical := slices.IndexFunc(plan.Revisions.Logical, func(value LogicalRevision) bool { return value.Name == epoch.LogicalRevision })
		if logical < 0 {
			return nil, errors.New("epoch catalog revision is absent")
		}
		epoch.CatalogSourceSHA256 = plan.Revisions.Logical[logical].CatalogSource.SHA256
		epoch.ConfigBytesSHA256 = admission.epochConfigBytesSHA256[index]
		epoch.ServerHealthDeadlineMS = plan.SafetyEnvelope.ServerHealthDeadlineMS
		if index > 0 && epoch.LogicalRevision != epochs[index-1].LogicalRevision && epoch.ConfigBytesSHA256 == epochs[index-1].ConfigBytesSHA256 {
			return nil, errors.New("changed operator version reused prior config bytes")
		}
	}
	return epochs, nil
}

func executionPhaseRecipeSHA256(plan Plan) string {
	phaseOrderSHA256, _ := canonicalSHA256(plan.PhaseOrder)
	phaseStatesSHA256, _ := canonicalSHA256(plan.PhaseStates)
	phaseDeadlinesSHA256, _ := canonicalSHA256(plan.PhaseDeadlines)
	failurePointsSHA256, _ := canonicalSHA256(plan.FailurePoints)
	return recipeDigest(
		"t422-execution-phase-recipe-v1", phaseOrderSHA256, phaseStatesSHA256,
		phaseDeadlinesSHA256, failurePointsSHA256,
	)
}

func executionInvocationSHA256(profile ExecutionProfile, tools []ExecutionToolIdentity) (string, error) {
	profile.InvocationSHA256 = ""
	profileSHA256, err := canonicalSHA256(profile)
	if err != nil {
		return "", err
	}
	toolsSHA256, err := canonicalSHA256(tools)
	if err != nil {
		return "", err
	}
	return recipeDigest("t422-execution-invocation-v1", profileSHA256, toolsSHA256), nil
}

func cloneExecutionProfile(value ExecutionProfile) ExecutionProfile {
	value.Commands = slices.Clone(value.Commands)
	for index := range value.Commands {
		value.Commands[index].NormalizedArgv = slices.Clone(value.Commands[index].NormalizedArgv)
	}
	value.Environment.BaseVariables = slices.Clone(value.Environment.BaseVariables)
	value.Environment.ServerVariables = slices.Clone(value.Environment.ServerVariables)
	value.Config.EnabledExtractorDomains = slices.Clone(value.Config.EnabledExtractorDomains)
	value.Config.AbsentOptionalConfiguration = slices.Clone(value.Config.AbsentOptionalConfiguration)
	value.Epochs = slices.Clone(value.Epochs)
	for index := range value.Epochs {
		value.Epochs[index].Phases = slices.Clone(value.Epochs[index].Phases)
	}
	return value
}

func expectedPhaseRuntime(epochs []ExecutionServerEpochProfile, phase string) (epoch uint64, launchPhase string, ok bool) {
	for _, value := range epochs {
		if slices.Contains(value.Phases, phase) {
			return value.ServerEpoch, value.LaunchPhase, true
		}
	}
	return 0, "", false
}

func validatePhaseRuntimeBindings(
	values []ExactPhaseEvidence,
	phases []string,
	outcomes map[string]string,
	measurements []PhaseMeasurement,
	freeze ExecutionFreeze,
) error {
	profileSHA256, err := canonicalSHA256(freeze.Profile)
	if err != nil {
		return err
	}
	processImageSHA256 := ""
	for _, tool := range freeze.Tools {
		if tool.Role == "phebs" {
			processImageSHA256 = tool.SHA256
			break
		}
	}
	if !validExecutionSHA256(processImageSHA256) {
		return errors.New("runtime binding lacks the admitted phebs image")
	}
	measurementByPhase := make(map[string]PhaseMeasurement, len(measurements))
	for _, value := range measurements {
		measurementByPhase[value.Phase] = value
	}
	type epochIdentity struct {
		startEventOrdinal     uint64
		processIdentitySHA256 string
	}
	epochs := make(map[uint64]epochIdentity, len(freeze.Profile.Epochs))
	for index, phase := range phases {
		value := values[index]
		epoch, launchPhase, ok := expectedPhaseRuntime(freeze.Profile.Epochs, phase)
		if !ok {
			return fmt.Errorf("phase %q lacks a frozen server epoch", phase)
		}
		measurement, measured := measurementByPhase[phase]
		launchMeasurement, launchMeasured := measurementByPhase[launchPhase]
		launchPhebsStarted := false
		if launchMeasured {
			roleIndex := slices.IndexFunc(launchMeasurement.ChildProcessRoles, func(value Count) bool {
				return value.Name == "phebs"
			})
			launchPhebsStarted = roleIndex >= 0 && launchMeasurement.ChildProcessRoles[roleIndex].Count > 0
		}
		runtimeRequired := value.Outcome == "passed" || value.Observed != nil ||
			value.Outcome == "stopped" && (outcomes[launchPhase] == "passed" || launchPhebsStarted)
		if !runtimeRequired && value.Runtime == nil {
			if value.RuntimeSHA256 != "" {
				return fmt.Errorf("phase %q retained a runtime digest without runtime evidence", phase)
			}
			continue
		}
		if value.Runtime == nil {
			return fmt.Errorf("phase %q lacks runtime evidence", phase)
		}
		binding := *value.Runtime
		bindingSHA256, digestErr := receiptSHA256(binding)
		expectedProcessIdentity := recipeDigest(
			"t422-phebs-process-identity-v1", processImageSHA256, fmt.Sprint(epoch),
		)
		if !measured || !launchMeasured || !launchPhebsStarted || digestErr != nil ||
			binding.Schema != freeze.Profile.RuntimeBindingSchema || binding.Phase != phase ||
			binding.ProfileSHA256 != profileSHA256 || binding.InvocationSHA256 != freeze.Profile.InvocationSHA256 ||
			binding.ProcessImageSHA256 != processImageSHA256 ||
			binding.ProcessIdentitySHA256 != expectedProcessIdentity ||
			binding.ServerEpoch != epoch || binding.StartEventOrdinal <= 1 ||
			binding.StartEventOrdinal < launchMeasurement.StartEventOrdinal ||
			binding.StartEventOrdinal >= launchMeasurement.FinishEventOrdinal ||
			binding.StartEventOrdinal >= measurement.FinishEventOrdinal ||
			value.RuntimeSHA256 != bindingSHA256 {
			return fmt.Errorf("phase %q runtime binding is invalid", phase)
		}
		identity := epochIdentity{binding.StartEventOrdinal, binding.ProcessIdentitySHA256}
		if previous, present := epochs[epoch]; present && previous != identity {
			return fmt.Errorf("phase %q changed server identity inside epoch %d", phase, epoch)
		}
		for otherEpoch, previous := range epochs {
			if otherEpoch != epoch && previous.processIdentitySHA256 == binding.ProcessIdentitySHA256 {
				return fmt.Errorf("phase %q reused a process identity across server epochs", phase)
			}
		}
		epochs[epoch] = identity
		if value.Outcome != outcomes[phase] {
			return fmt.Errorf("phase %q runtime outcome binding is invalid", phase)
		}
	}
	return nil
}

func validatePhaseRuntimeTransitionBindings(
	states []ExactPhaseEvidence,
	transitions []TransitionResult,
	epochs []ExecutionServerEpochProfile,
) error {
	runtimeFor := func(phase string) *PhaseRuntimeBinding {
		index := slices.IndexFunc(states, func(value ExactPhaseEvidence) bool { return value.Phase == phase })
		if index < 0 {
			return nil
		}
		return states[index].Runtime
	}
	if restart, ok := namedTransition(transitions, "process_restart"); ok && restart.Outcome == "passed" {
		index := slices.IndexFunc(restart.Injections, func(value InjectionTransition) bool {
			return value.FailurePoint == "checkpointed_hard_restart"
		})
		before, after := runtimeFor("stale_lease"), runtimeFor("process_restart")
		if index < 0 || before == nil || after == nil {
			return errors.New("process restart lacks its phase runtime binding")
		}
		injection := restart.Injections[index]
		beforeEpoch, _, beforeOK := expectedPhaseRuntime(epochs, "stale_lease")
		afterEpoch, _, afterOK := expectedPhaseRuntime(epochs, "process_restart")
		if !beforeOK || !afterOK || before.ServerEpoch != beforeEpoch || after.ServerEpoch != afterEpoch ||
			before.ProcessIdentitySHA256 != injection.ProcessIdentityBeforeSHA256 ||
			after.ProcessIdentitySHA256 != injection.ProcessIdentityAfterSHA256 ||
			after.ProcessImageSHA256 != injection.ProcessImageSHA256 ||
			after.StartEventOrdinal != injection.ProcessStartEventOrdinal {
			return errors.New("process restart differs from its phase runtime binding")
		}
	}
	if archive, ok := namedTransition(transitions, "archive_restore"); ok && archive.Outcome == "passed" {
		runtime := runtimeFor("archive_restore")
		epoch, _, found := expectedPhaseRuntime(epochs, "archive_restore")
		if !found || runtime == nil || archive.Archive == nil || runtime.ServerEpoch != epoch ||
			runtime.StartEventOrdinal <= archive.Archive.RestoreStartedEventOrdinal ||
			runtime.StartEventOrdinal >= archive.Archive.ComparisonEventOrdinal {
			return errors.New("archive restore differs from its phase runtime binding")
		}
	}
	return nil
}
