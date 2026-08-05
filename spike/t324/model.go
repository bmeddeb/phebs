// Package t324 measures the T32.4 direct-search topology against T32.2's
// source-free trigger and T32.3's independent public oracle. Production
// packages must not import spike packages.
package t324

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

const ReceiptSchema = "t324-search-topology-cost-receipt-v1"

type Receipt struct {
	Schema      string               `json:"schema"`
	MeasuredOn  string               `json:"measured_on"`
	Inputs      Inputs               `json:"inputs"`
	Environment Environment          `json:"environment"`
	Trigger     TriggerDecision      `json:"cohort_trigger"`
	Correctness Correctness          `json:"correctness"`
	NeutralCost NeutralCost          `json:"neutral_cost"`
	Profiles    []ProfileMeasurement `json:"profiles"`
	Decisions   []TopologyDecision   `json:"decisions"`
	Claims      Claims               `json:"claims"`
}

type Inputs struct {
	T322ResultsSHA256 string `json:"t322_results_sha256"`
	T323ReceiptSHA256 string `json:"t323_receipt_sha256"`
	T323BundleSHA256  string `json:"t323_bundle_sha256"`
}

type Environment struct {
	GOOS          string `json:"goos"`
	GOARCH        string `json:"goarch"`
	GoVersion     string `json:"go_version"`
	LogicalCPUs   int    `json:"logical_cpus"`
	ZoektSHA256   string `json:"zoekt_binary_sha256"`
	CtagsDisabled bool   `json:"ctags_disabled"`
}

type TriggerDecision struct {
	BaselineOutcome string `json:"baseline_outcome"`
	IndexOutcome    string `json:"index_outcome"`
	SearchOutcome   string `json:"search_outcome"`
	Triggered       bool   `json:"triggered"`
	Reason          string `json:"reason"`
}

type Correctness struct {
	Revisions                 int  `json:"revisions"`
	OracleQueries             int  `json:"oracle_queries"`
	ExecutedQueries           int  `json:"executed_queries"`
	AuthorityEmptyQueries     int  `json:"authority_empty_queries"`
	AuthorityAdmissionChecked bool `json:"authority_admission_checked"`
	AllCodeEqual              bool `json:"all_code_equal"`
	ServiceEqual              bool `json:"service_equal"`
	PredicatesInsideZoekt     bool `json:"predicates_inside_zoekt"`
	BroadQueriesEqual         bool `json:"broad_queries_equal"`
	AdversarialRankingStable  bool `json:"adversarial_ranking_stable"`
	TopKEqual                 bool `json:"top_k_equal"`
	RevisionBound             bool `json:"revision_bound"`
	PerRevisionCatalogEqual   bool `json:"per_revision_catalog_equal"`
	PriorRevisionRepeatable   bool `json:"prior_revision_repeatable"`
	BranchABASelectionEqual   bool `json:"branch_a_b_a_selection_equal"`
}

type NeutralCost struct {
	Build        BuildMeasurement  `json:"build"`
	Reader       ReaderMeasurement `json:"reader"`
	ColdQueryMS  int64             `json:"cold_query_ms"`
	WarmQueryMS  int64             `json:"warm_query_ms"`
	QueryCount   int               `json:"query_count"`
	ResultDigest string            `json:"result_digest"`
}

type ProfileMeasurement struct {
	Name           string               `json:"name"`
	Services       int                  `json:"services"`
	Files          int                  `json:"files"`
	Memberships    int                  `json:"memberships"`
	Build          BuildMeasurement     `json:"build"`
	SingleFileEdit BuildMeasurement     `json:"single_file_edit"`
	Reader         ReaderMeasurement    `json:"reader"`
	Queries        QueryMeasurement     `json:"queries"`
	Predicates     PredicateMeasurement `json:"predicates"`
	NoOp           NoOpMeasurement      `json:"no_op"`
}

type BuildMeasurement struct {
	WallMS       int64 `json:"wall_ms"`
	PeakRSSBytes int64 `json:"peak_rss_bytes"`
	ShardBytes   int64 `json:"shard_bytes"`
	ShardCount   int   `json:"shard_count"`
}

type ReaderMeasurement struct {
	MMapCount        int    `json:"mmap_count"`
	MMapMethod       string `json:"mmap_method"`
	DescriptorBase   int    `json:"descriptor_base"`
	DescriptorOpen   int    `json:"descriptor_open"`
	DescriptorDelta  int    `json:"descriptor_delta"`
	DescriptorMethod string `json:"descriptor_method"`
}

type QueryMeasurement struct {
	QueryCount    int    `json:"query_count"`
	ColdTotalUS   int64  `json:"cold_total_us"`
	WarmTotalUS   int64  `json:"warm_total_us"`
	ColdWarmEqual bool   `json:"cold_warm_equal"`
	BroadTopK     int    `json:"broad_top_k"`
	ServiceTopK   int    `json:"service_top_k"`
	ResultDigest  string `json:"result_digest"`
}

type PredicateMeasurement struct {
	CompiledServices int   `json:"compiled_services"`
	Placements       int   `json:"placements"`
	WallUS           int64 `json:"wall_us"`
	CanonicalBytes   int64 `json:"canonical_bytes"`
	MaxBytes         int   `json:"max_bytes"`
}

type NoOpMeasurement struct {
	WallUS           int64 `json:"wall_us"`
	StateComparisons int   `json:"state_comparisons"`
	ChildRuns        int   `json:"child_runs"`
	FileScans        int   `json:"file_scans"`
	ShardReads       int   `json:"shard_reads"`
	ShardBytesDelta  int64 `json:"shard_bytes_delta"`
}

type TopologyDecision struct {
	Topology string `json:"topology"`
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
	NextGate string `json:"next_gate"`
}

type Claims struct {
	SourceFree                    bool `json:"source_free"`
	SelectsInitialTopology        bool `json:"selects_initial_topology"`
	EstablishesSLO                bool `json:"establishes_slo"`
	EstablishesAccuracy           bool `json:"establishes_accuracy"`
	AuthorizesMultiServiceRelease bool `json:"authorizes_multi_service_release"`
	ChangesProductionBehavior     bool `json:"changes_production_behavior"`
}

func ValidateReceipt(receipt Receipt) error {
	if receipt.Schema != ReceiptSchema || receipt.MeasuredOn == "" ||
		!validDigest(receipt.Inputs.T322ResultsSHA256) ||
		!validDigest(receipt.Inputs.T323ReceiptSHA256) ||
		!validDigest(receipt.Inputs.T323BundleSHA256) ||
		!validDigest(receipt.Environment.ZoektSHA256) || receipt.Environment.GOOS == "" ||
		receipt.Environment.GOARCH == "" || receipt.Environment.GoVersion == "" ||
		receipt.Environment.LogicalCPUs < 1 || !receipt.Environment.CtagsDisabled {
		return errors.New("receipt envelope or input binding is invalid")
	}
	if receipt.Trigger.BaselineOutcome != "completed" || receipt.Trigger.IndexOutcome != "succeeded" ||
		receipt.Trigger.SearchOutcome != "succeeded" || receipt.Trigger.Triggered || receipt.Trigger.Reason == "" {
		return errors.New("cohort trigger must retain the completed direct baseline decision")
	}
	correctness := receipt.Correctness
	if correctness.Revisions != 5 || correctness.OracleQueries < 1 ||
		correctness.ExecutedQueries+correctness.AuthorityEmptyQueries != correctness.OracleQueries ||
		!correctness.AuthorityAdmissionChecked ||
		!correctness.AllCodeEqual || !correctness.ServiceEqual || !correctness.PredicatesInsideZoekt ||
		!correctness.BroadQueriesEqual || !correctness.AdversarialRankingStable || !correctness.TopKEqual ||
		!correctness.RevisionBound || !correctness.PerRevisionCatalogEqual ||
		!correctness.PriorRevisionRepeatable || !correctness.BranchABASelectionEqual {
		return fmt.Errorf("correctness gate is incomplete: %+v", correctness)
	}
	if err := validateBuild(receipt.NeutralCost.Build); err != nil ||
		receipt.NeutralCost.QueryCount != correctness.ExecutedQueries ||
		!validDigest(receipt.NeutralCost.ResultDigest) {
		return errors.New("neutral measurement is incomplete")
	}
	if err := validateReader(receipt.NeutralCost.Reader, receipt.NeutralCost.Build.ShardCount); err != nil {
		return err
	}
	if len(receipt.Profiles) != 2 || receipt.Profiles[0].Services != 1_000 || receipt.Profiles[1].Services != 5_000 {
		return errors.New("frozen profile measurements are missing or unordered")
	}
	for _, profile := range receipt.Profiles {
		if profile.Name == "" || profile.Files < 1 || profile.Memberships != profile.Services*5 ||
			profile.Predicates.CompiledServices != profile.Services || profile.Predicates.Placements != profile.Memberships ||
			profile.Predicates.CanonicalBytes < 1 || profile.Predicates.MaxBytes < 1 ||
			profile.Queries.QueryCount < 1 || !profile.Queries.ColdWarmEqual ||
			!validDigest(profile.Queries.ResultDigest) || profile.NoOp.StateComparisons != 1 ||
			profile.NoOp.ChildRuns != 0 || profile.NoOp.FileScans != 0 || profile.NoOp.ShardReads != 0 ||
			profile.NoOp.ShardBytesDelta != 0 {
			return fmt.Errorf("profile %q measurement is incomplete", profile.Name)
		}
		if err := validateBuild(profile.Build); err != nil {
			return fmt.Errorf("profile %q build: %w", profile.Name, err)
		}
		if err := validateBuild(profile.SingleFileEdit); err != nil {
			return fmt.Errorf("profile %q edit: %w", profile.Name, err)
		}
		if err := validateReader(profile.Reader, profile.Build.ShardCount); err != nil {
			return fmt.Errorf("profile %q reader: %w", profile.Name, err)
		}
	}
	if len(receipt.Decisions) != 3 {
		return errors.New("topology decision table must have three rows")
	}
	want := []struct{ topology, decision string }{{"direct", "go"}, {"bounded-cohorts", "no_go"}, {"p6", "no_go"}}
	for index, expected := range want {
		decision := receipt.Decisions[index]
		if decision.Topology != expected.topology || decision.Decision != expected.decision ||
			decision.Reason == "" || decision.NextGate == "" {
			return errors.New("topology decision table is invalid")
		}
	}
	claims := receipt.Claims
	if !claims.SourceFree || !claims.SelectsInitialTopology || claims.EstablishesSLO ||
		claims.EstablishesAccuracy || claims.AuthorizesMultiServiceRelease || claims.ChangesProductionBehavior {
		return errors.New("receipt claims are invalid")
	}
	return nil
}

func validateBuild(build BuildMeasurement) error {
	if build.WallMS < 0 || build.PeakRSSBytes < 1 || build.ShardBytes < 1 || build.ShardCount < 1 {
		return errors.New("build measurement is invalid")
	}
	return nil
}

func validateReader(reader ReaderMeasurement, shards int) error {
	if reader.MMapCount != shards || reader.MMapMethod != "one-zoekt-mmap-per-visible-shard" ||
		reader.DescriptorBase < 0 || reader.DescriptorOpen < 0 ||
		reader.DescriptorDelta != reader.DescriptorOpen-reader.DescriptorBase || reader.DescriptorMethod == "" {
		return errors.New("reader measurement is invalid")
	}
	return nil
}

func MarshalCanonical(value any) ([]byte, error) {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func DecodeStrict[T any](data []byte) (T, error) {
	var value T
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return value, err
	}
	return value, nil
}

func SHA256(data []byte) string {
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func digestStrings(values []string) string {
	values = slices.Clone(values)
	slices.Sort(values)
	return SHA256([]byte(strings.Join(values, "\n") + "\n"))
}

func validDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}
