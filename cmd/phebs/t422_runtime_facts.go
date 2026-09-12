package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

// Shared with the actual serve scheduler class construction, not an expected
// execution-profile projection. These do not assert that workers were started.
const (
	observationIOConcurrency  = 1
	observationCPUConcurrency = 2
	relationshipConcurrency   = 1
	t422RuntimeFactsCommand   = "t422-runtime-facts"
	t422RuntimeFactsSchema    = "t422-runtime-facts-v1"
)

type t422ScheduleFacts struct {
	MaxAttempts      int `json:"max_attempts"`
	RepositoryTokens int `json:"repository_tokens"`
}

// This private recipe reports native configuration only. Target partition
// totals, registered domains, serial runner topology and selected lifecycle
// construction require separate facts; none is inferred from this record.
type t422RuntimeFacts struct {
	Schema                           string            `json:"schema"`
	StoreRunnerDefaultMaxAttempts    int               `json:"store_runner_default_max_attempts"`
	ObservationIOConcurrency         int               `json:"observation_io_concurrency"`
	ObservationCPUConcurrency        int               `json:"observation_cpu_concurrency"`
	RelationshipConcurrency          int               `json:"relationship_concurrency"`
	ExtractionConcurrency            int               `json:"extraction_concurrency"`
	ObservationPlanning              t422ScheduleFacts `json:"observation_planning"`
	ObservationInventory             t422ScheduleFacts `json:"observation_inventory"`
	ObservationExecution             t422ScheduleFacts `json:"observation_execution"`
	Relationship                     t422ScheduleFacts `json:"relationship"`
	Extraction                       t422ScheduleFacts `json:"extraction"`
	NativeMaximumAggregatePartitions int               `json:"native_maximum_aggregate_partitions"`
	StoreGenerationMaxAttempts       int               `json:"store_generation_max_attempts"`
	SelectedJobAcceptedAttempts      int               `json:"selected_job_accepted_attempts"`
	SelectedChunkAcceptedAttempts    int               `json:"selected_chunk_accepted_attempts"`
	MaximumStoreRowsPerTransaction   int               `json:"maximum_store_rows_per_transaction"`
}

func configuredT422RuntimeFacts() t422RuntimeFacts {
	return t422RuntimeFacts{
		Schema:                           t422RuntimeFactsSchema,
		StoreRunnerDefaultMaxAttempts:    store.DefaultRunnerMaxAttempts,
		ObservationIOConcurrency:         observationIOConcurrency,
		ObservationCPUConcurrency:        observationCPUConcurrency,
		RelationshipConcurrency:          relationshipConcurrency,
		ExtractionConcurrency:            extractionpublication.ScheduleClassConcurrency,
		ObservationPlanning:              t422ScheduleFacts{MaxAttempts: observationpublication.PlanningScheduleMaxAttempts, RepositoryTokens: observationpublication.PlanningScheduleRepositoryTokens},
		ObservationInventory:             t422ScheduleFacts{MaxAttempts: observationpublication.InventoryScheduleMaxAttemptsV2, RepositoryTokens: observationpublication.InventoryScheduleRepositoryTokensV2},
		ObservationExecution:             t422ScheduleFacts{MaxAttempts: observationpublication.ScheduleMaxAttempts, RepositoryTokens: observationpublication.ScheduleRepositoryTokens},
		Relationship:                     t422ScheduleFacts{MaxAttempts: relationshippublication.ScheduleMaxAttempts, RepositoryTokens: relationshippublication.ScheduleRepositoryTokens},
		Extraction:                       t422ScheduleFacts{MaxAttempts: extractionpublication.ScheduleMaxAttempts, RepositoryTokens: extractionpublication.ScheduleRepositoryTokens},
		NativeMaximumAggregatePartitions: candidate.MaxSparseAggregatePartitions,
		StoreGenerationMaxAttempts:       store.MaxGenerationAttempts,
		SelectedJobAcceptedAttempts:      t422JobAcceptedAttempts,
		SelectedChunkAcceptedAttempts:    t422ChunkAcceptedAttempts,
		MaximumStoreRowsPerTransaction:   storeaccounting.MaximumRows,
	}
}

// runPhebs reaches this only after its unchanged bootstrap and command checks.
// A selected server/archive lifetime is never a no-work probe. The future
// parent must independently protect, launch, join and bind this binary; printing
// these bytes neither admits the tool nor issues a profile.
func writeT422RuntimeFacts(ctx context.Context, args []string, output io.Writer, lifetime *dispatchadmission.ProductionLifetime) error {
	if ctx == nil || ctx.Err() != nil || len(args) != 0 || output == nil || lifetime != nil {
		return errors.New("runtime facts require an unbound no-argument command")
	}
	raw, err := json.Marshal(configuredT422RuntimeFacts())
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if err := ctx.Err(); err != nil {
		return err
	}
	n, err := output.Write(raw)
	if err != nil {
		return err
	}
	if n != len(raw) {
		return io.ErrShortWrite
	}
	return ctx.Err()
}
