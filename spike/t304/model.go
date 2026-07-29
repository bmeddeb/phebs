// Package t304 retains the prospective candidate-planner measurement used to
// freeze T30.4's partition and resource bounds.
package t304

import (
	"time"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/spike/t301"
)

const (
	SchemaVersion = "t304-candidate-planner-measurement-v1"

	ExpectedRegularFiles = 200_008

	PlannerWallLimit        = 10 * time.Second
	PeakRSSLimit            = int64(256 << 20)
	CandidateDiskBytesLimit = int64(16 << 20)
)

type FrozenGates struct {
	PlannerWallNanos   int64 `json:"planner_wall_nanos"`
	PeakRSSBytes       int64 `json:"peak_rss_bytes"`
	CandidateDiskBytes int64 `json:"candidate_disk_bytes"`
}

type CallerLeafMeasurement struct {
	Prefix        string `json:"prefix"`
	PrefixBits    int    `json:"prefix_bits"`
	RecordCount   int    `json:"record_count"`
	DeclaredBytes int64  `json:"declared_bytes"`
	ContentBytes  int64  `json:"content_bytes"`
}

type PlannerRun struct {
	CorpusDigest                     string                  `json:"corpus_digest"`
	UnitDigest                       string                  `json:"unit_digest"`
	PolicyDigest                     string                  `json:"policy_digest"`
	GenerationDigest                 string                  `json:"generation_digest"`
	ManifestDigest                   string                  `json:"manifest_digest"`
	Census                           candidate.CorpusSummary `json:"census"`
	RepositoryCandidateRows          int                     `json:"repository_candidate_rows"`
	CallerCandidateRows              int                     `json:"caller_candidate_rows"`
	CallerLeaves                     []CallerLeafMeasurement `json:"caller_leaves"`
	StagedArtifactCount              int                     `json:"staged_artifact_count"`
	StagedArtifactBytes              int64                   `json:"staged_artifact_bytes"`
	PlannerScratchUpperBoundBytes    int64                   `json:"planner_scratch_upper_bound_bytes"`
	ValidationScratchUpperBoundBytes int64                   `json:"validation_scratch_upper_bound_bytes"`
	PeakCandidateDiskBytes           int64                   `json:"peak_candidate_disk_bytes"`
	CanonicalOutputDigest            string                  `json:"canonical_output_digest"`
	PlannerWallNanos                 int64                   `json:"planner_wall_nanos"`
	PeakRSSBytes                     int64                   `json:"peak_rss_bytes"`
}

type Results struct {
	Schema              string                     `json:"schema"`
	Decision            string                     `json:"decision"`
	Reason              string                     `json:"reason"`
	GoVersion           string                     `json:"go_version"`
	GOOS                string                     `json:"goos"`
	GOARCH              string                     `json:"goarch"`
	Corpus              t301.Corpus                `json:"corpus"`
	UnitDigest          string                     `json:"unit_digest"`
	Policies            []candidate.PolicyIdentity `json:"policies"`
	PartitionPolicy     candidate.PartitionPolicy  `json:"partition_policy"`
	FrozenGates         FrozenGates                `json:"frozen_gates"`
	Run1                PlannerRun                 `json:"run1"`
	Run2                PlannerRun                 `json:"run2"`
	CanonicalBytesEqual bool                       `json:"canonical_bytes_equal"`
}
