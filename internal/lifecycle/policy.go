// Package lifecycle implements T35's bounded, owner-separated maintenance
// and disk-pressure admission substrate. Owners retain responsibility for
// proving their exact roots immediately before destructive work.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	Schema = "phebs-lifecycle-v1"

	// These fixed filesystem batches are available only to an explicitly
	// selected cleanup collector. Relationship V3 still releases at most one
	// root's store pins per turn; only its filesystem drain grows.
	SelectedCleanupObservationDeletes  = 1_024
	SelectedCleanupSearchDeletes       = 64
	SelectedCleanupRelationshipDeletes = 1_024

	SoftWatermarkPercent   = 80
	HardWatermarkPercent   = 90
	ResumeWatermarkPercent = 75

	MaxOwnersPerController = 32
	MaxCandidatesPerTick   = 64
	MaxDeletesPerTick      = 16
	MaxQueriesPerTick      = 16
	// Nominal scan-planning allowance after four cursor operations; owners
	// may perform nested reads/writes. This is not an enforced SDK-call cap.
	MaxOwnerQueriesPerTick = 12
	// Historical planning inputs, not whole-sweep filesystem meters. In
	// particular inventory/search preflight and batch deletion are separate.
	MaxStatsPerTick         = 256
	MaxDescriptorsPerTick   = 8
	MaxMetadataBytesPerTick = 1 << 20

	// One pressure-dependent admission may reserve at most the prospectively
	// measured T40.5 search-generation ceiling. Whole search passes its exact
	// source-census-derived reservation; other owners keep their smaller
	// existing estimates. This is a validation bound, not a fixed 48-GiB
	// reservation for every build.
	MaxPressureDependentAdmissionBytes int64 = 48 << 30
	GenerationScheduleRetained               = 2
)

// SelectedCleanupDeleteLimit is the closed selected cleanup owner profile;
// it does not change the ordinary production policy.
func SelectedCleanupDeleteLimit(owner string) int {
	switch owner {
	case ObservationV2Owner:
		return SelectedCleanupObservationDeletes
	case SearchOwner:
		return SelectedCleanupSearchDeletes
	case RelationshipV3Owner:
		return SelectedCleanupRelationshipDeletes
	default:
		return MaxDeletesPerTick
	}
}

var (
	ErrCapacityUnavailable = errors.New("lifecycle filesystem capacity is unavailable")
	ErrPressureRefusal     = errors.New("lifecycle hard disk watermark reached")
	ErrCursorConflict      = errors.New("lifecycle cursor changed")
)

type Limits struct {
	Candidates    int
	Deletes       int
	Queries       int
	Stats         int
	Descriptors   int
	MetadataBytes int64
}

func DefaultLimits() Limits {
	return Limits{
		Candidates:    MaxCandidatesPerTick,
		Deletes:       MaxDeletesPerTick,
		Queries:       MaxQueriesPerTick,
		Stats:         MaxStatsPerTick,
		Descriptors:   MaxDescriptorsPerTick,
		MetadataBytes: MaxMetadataBytesPerTick,
	}
}

func (limits Limits) validate() error {
	if limits.Candidates < 1 || limits.Candidates > MaxCandidatesPerTick ||
		limits.Deletes < 1 || limits.Deletes > MaxDeletesPerTick ||
		limits.Queries < 1 || limits.Queries > MaxQueriesPerTick ||
		limits.Stats < 1 || limits.Stats > MaxStatsPerTick ||
		limits.Descriptors < 1 || limits.Descriptors > MaxDescriptorsPerTick ||
		limits.MetadataBytes < 1 || limits.MetadataBytes > MaxMetadataBytesPerTick {
		return errors.New("lifecycle tick limits exceed the fixed envelope")
	}
	return nil
}

type Completeness string

const (
	Exact       Completeness = "exact"
	LowerBound  Completeness = "lower_bound"
	Unavailable Completeness = "unavailable"
)

type OwnerResult struct {
	Owner        string
	Cursor       string
	Scanned      int
	Deleted      int
	LogicalBytes int64
	RootBytes    int64
	MemberBytes  int64
	More         bool
	// AttemptedAt is stamped by Controller immediately before the owner sweep.
	// Status publication must not move this fence past work that straddled a
	// pressure or restart boundary.
	AttemptedAt time.Time
	// CycleStart identifies the first owner in the controller's stable sorted
	// order. It lets a newly started runner distinguish the end of a durable
	// rotation suffix from the end of one complete process-observed cycle.
	CycleStart    bool
	CycleComplete bool
	// AdvanceOnError is reserved for owners that have isolated a malformed
	// namespace after selecting its exact durable cursor. It prevents one bad
	// namespace from starving healthy siblings without relabeling the turn as
	// successful or weakening the malformed namespace's refusal.
	AdvanceOnError bool
	Completeness   Completeness
	Err            error
}

type Owner interface {
	Name() string
	Sweep(context.Context, time.Time, string, Limits) OwnerResult
}

type registeredOwner struct {
	name  string
	owner Owner
}

func normalizeOwners(owners []registeredOwner) ([]registeredOwner, error) {
	if len(owners) == 0 || len(owners) > MaxOwnersPerController {
		return nil, fmt.Errorf("lifecycle owner count must be from 1 through %d", MaxOwnersPerController)
	}
	result := append([]registeredOwner(nil), owners...)
	sort.Slice(result, func(i, j int) bool { return result[i].name < result[j].name })
	for i, owner := range result {
		if owner.name == "" || strings.TrimSpace(owner.name) != owner.name || owner.owner == nil {
			return nil, errors.New("lifecycle owner registration is invalid")
		}
		if i > 0 && result[i-1].name == owner.name {
			return nil, fmt.Errorf("duplicate lifecycle owner %q", owner.name)
		}
	}
	return result, nil
}
