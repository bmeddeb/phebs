// Package closure retains the T45.9 source-free closure. It binds the
// existing neutral receipts and records the unbound target. It does not
// execute a native rehearsal or a target repository.
package closure

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

const Schema = "phebs-t459-closure-v1"

const (
	DesignAcceptedServices int64 = 5000
	DesignAdmittedBytes    int64 = 12_000_000_000
	MeasuredServices       int64 = 0
	MeasuredBytes          int64 = 176
)

type Receipt struct {
	Schema                  string       `json:"schema"`
	T459Acceptance          string       `json:"t459_acceptance"`
	GeneratedBundleEvidence string       `json:"generated_bundle_evidence"`
	T456CorpusWaiver        string       `json:"t456_corpus_waiver"`
	OrdinaryProviders       string       `json:"ordinary_providers"`
	Release                 string       `json:"release"`
	Neutral                 Neutral      `json:"neutral"`
	Target                  Target       `json:"target"`
	DesignTarget            DesignTarget `json:"design_target"`
	Measured                Dimensions   `json:"measured"`
	Inputs                  []Input      `json:"inputs"`
}

type Neutral struct {
	Outcome    string     `json:"outcome"`
	Repository string     `json:"repository"`
	Behaviors  []Behavior `json:"behaviors"`
}

type Behavior struct {
	Name     string `json:"name"`
	Receipt  string `json:"receipt"`
	Evidence string `json:"evidence"`
}

type Target struct {
	Outcome                     string `json:"outcome"`
	Corpus                      string `json:"corpus"`
	Commit                      string `json:"commit"`
	ArchiveSHA256               string `json:"archive_sha256"`
	Profile                     string `json:"profile"`
	Tools                       string `json:"tools"`
	Host                        string `json:"host"`
	SourcePaths                 int    `json:"source_paths"`
	SourceBytes                 int64  `json:"source_bytes"`
	AcceptedServiceIncarnations int64  `json:"accepted_service_incarnations"`
	BehaviorsExecuted           bool   `json:"behaviors_executed"`
	Reason                      string `json:"reason"`
}

type DesignTarget struct {
	AcceptedServiceIncarnations int64 `json:"accepted_service_incarnations"`
	AdmittedSourceBlobBytes     int64 `json:"admitted_source_blob_bytes"`
}

type Dimensions struct {
	AcceptedServiceIncarnations int64 `json:"accepted_service_incarnations"`
	AdmittedSourceBlobBytes     int64 `json:"admitted_source_blob_bytes"`
	SatisfiesDesignTarget       bool  `json:"satisfies_design_target"`
}

type Input struct {
	Path   string `json:"path"`
	Schema string `json:"schema"`
	Digest string `json:"digest"`
}

func Build() Receipt {
	measured := Dimensions{AcceptedServiceIncarnations: MeasuredServices, AdmittedSourceBlobBytes: MeasuredBytes}
	measured.SatisfiesDesignTarget = satisfies(measured)
	return Receipt{
		Schema:                  Schema,
		T459Acceptance:          "OPEN",
		GeneratedBundleEvidence: "STOP",
		T456CorpusWaiver:        "WAIVED_NOT_PASS",
		OrdinaryProviders:       "unavailable",
		Release:                 "unchanged",
		Neutral: Neutral{
			Outcome:    "completed",
			Repository: "example.invalid/phebs-native-neutral",
			Behaviors: []Behavior{
				{Name: "cold_generation", Receipt: "spike/t459/native_coordinator_1.json", Evidence: "fresh native coordinator, two launches"},
				{Name: "bounded_failure", Receipt: "spike/t459/native_faults_1.json", Evidence: "cancel, wall, and hard-death passed"},
				{Name: "restart_resume", Receipt: "spike/t459/native_faults_1.json", Evidence: "hard-death recovery process joined and refused replay"},
				{Name: "stale_source_fencing", Receipt: "spike/t459/settings_native_3.json", Evidence: "source transition fences rendered state and cached native reads"},
				{Name: "cross_member_queries", Receipt: "spike/t459/settings_native_3.json", Evidence: "cold and warm cross-member HTTP navigation"},
				{Name: "warm_reuse", Receipt: "spike/t459/settings_native_3.json", Evidence: "warm current retained one lookup"},
				{Name: "pressure_lifecycle", Receipt: "spike/t459/native_pressure_lifecycle_1.json", Evidence: "1211 selected lifecycle turns"},
				{Name: "regenerate_on_restore", Receipt: "spike/t459/native_restore_1.json", Evidence: "restored authority unavailable, then fresh publication"},
				{Name: "settings_api_parity", Receipt: "spike/t459/settings_browser_2.json", Evidence: "rendered Settings pass plus native bridge pass"},
				{Name: "clean_teardown", Receipt: "spike/t459/settings_native_3.json", Evidence: "joined runtime, drained scratch, zero containers"},
			},
		},
		Target: Target{
			Outcome:                     "below_design_target",
			Corpus:                      "github.com/bazelbuild/remote-apis-sdks",
			Commit:                      "d5824b1a2286806b07efd030aa3a139c4f540157",
			ArchiveSHA256:               "sha256:c9ecf680cd7bd0d88d8a6d1a0084a09c0a9dc45145fc28fbdcda888586d54bcc",
			Profile:                     "bazel 9.0.0 / rules_go 0.59.0",
			Tools:                       "go 1.25.0 / scip-go 0.2.7",
			SourcePaths:                 128,
			SourceBytes:                 1079184,
			AcceptedServiceIncarnations: 0,
			BehaviorsExecuted:           false,
			Reason:                      "The frozen public archive measures 128 paths and 1079184 source bytes, with no accepted service catalog. No retained host executed the closure behaviors, and the measurement is below the design target.",
		},
		DesignTarget: DesignTarget{
			AcceptedServiceIncarnations: DesignAcceptedServices,
			AdmittedSourceBlobBytes:     DesignAdmittedBytes,
		},
		Measured: measured,
		Inputs: []Input{
			{Path: "spike/t459/native_coordinator_1.json", Schema: "phebs-t459-native-coordinator-v1", Digest: "sha256:05d2d78e2f53da0e7a3c6194217d08677ba4b6812d3e35bb59b40fef38213aa5"},
			{Path: "spike/t459/native_faults_1.json", Schema: "phebs-t459-native-workspace-faults-v1", Digest: "sha256:bb976bb8017264f5854decdaba144a9f9fa0f07ac2f40008cf52056f0ac6888b"},
			{Path: "spike/t459/native_pressure_lifecycle_1.json", Schema: "phebs-t459-native-pressure-lifecycle-v1", Digest: "sha256:925e3f7133b55dc1e647717e1ceba00bbcc9664a2247f85608154b25c753c056"},
			{Path: "spike/t459/native_restore_1.json", Schema: "phebs-t459-native-restore-v1", Digest: "sha256:ed44d4e8fe7f6df1e722ba5ae9a3b140c699c7bca3499e83114ba64dfcb56547"},
			{Path: "spike/t459/settings_browser_2.json", Schema: "phebs-t459-rendered-settings-review-correction-v1", Digest: "sha256:907d7f843514aa73176648119c093aca04cd6b0c2a970c49e53d6cccbd7f0771"},
			{Path: "spike/t459/settings_native_3.json", Schema: "phebs-t459-authenticated-settings-native-exact-rehearsal-v1", Digest: "sha256:03a52880e0b82c6841922d3c7d45dbf6f0d7d88d75c1b99abe295a8348b3a124"},
		},
	}
}

func satisfies(d Dimensions) bool {
	return d.AcceptedServiceIncarnations >= DesignAcceptedServices && d.AdmittedSourceBlobBytes >= DesignAdmittedBytes
}

// TargetSatisfied is true only for a bound target whose retained measurement
// meets the design dimensions. The neutral fixture cannot satisfy it.
func TargetSatisfied(target Target, measured Dimensions) bool {
	if target.Corpus == "" || target.Commit == "" || target.Profile == "" || target.Tools == "" || target.Host == "" || !target.BehaviorsExecuted {
		return false
	}
	return satisfies(measured)
}

func Marshal(receipt Receipt) ([]byte, error) {
	raw, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

func Digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}
