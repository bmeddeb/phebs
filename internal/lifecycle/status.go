package lifecycle

import (
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
)

const StatusSchema = "phebs-lifecycle-status-v1"

type StatusPolicy struct {
	Enabled                bool `json:"enabled"`
	Owners                 int  `json:"owners"`
	SoftWatermarkPercent   int  `json:"soft_watermark_percent"`
	HardWatermarkPercent   int  `json:"hard_watermark_percent"`
	ResumeWatermarkPercent int  `json:"resume_watermark_percent"`
	MaxCandidatesPerTurn   int  `json:"max_candidates_per_turn"`
	MaxDeletesPerTurn      int  `json:"max_deletes_per_turn"`
	MaxQueriesPerTurn      int  `json:"max_queries_per_turn"`
}

type CapacityStatus struct {
	Completeness Completeness `json:"completeness" enum:"exact,unavailable"`
	Pressure     Pressure     `json:"pressure" enum:"normal,collect,refuse,unavailable"`
	TotalBytes   *int64       `json:"total_bytes,omitempty"`
	UsedBytes    *int64       `json:"used_bytes,omitempty"`
	UsedPercent  *int         `json:"used_percent,omitempty"`
	ObservedAt   *time.Time   `json:"observed_at,omitempty"`
}

type OwnerStatus struct {
	Name         string       `json:"name"`
	State        string       `json:"state" enum:"not_run,ok,error"`
	Completeness Completeness `json:"completeness" enum:"exact,lower_bound,unavailable"`
	Scanned      int          `json:"scanned"`
	Deleted      int          `json:"deleted"`
	LogicalBytes int64        `json:"logical_bytes"`
	RootBytes    int64        `json:"root_bytes"`
	MemberBytes  int64        `json:"member_bytes"`
	Backlog      bool         `json:"backlog"`
	AttemptedAt  *time.Time   `json:"attempted_at,omitempty"`
}

type Status struct {
	SchemaVersion string         `json:"schema" enum:"phebs-lifecycle-status-v1"`
	Policy        StatusPolicy   `json:"policy"`
	Capacity      CapacityStatus `json:"capacity"`
	Owners        []OwnerStatus  `json:"owners"`
}

// StatusMonitor retains only one bounded, source-free result per closed owner
// and the most recent capacity observation. It is operational visibility, not
// a second lifecycle authority or a persistence boundary.
type StatusMonitor struct {
	mu     sync.RWMutex
	status Status
	now    func() time.Time
}

func NewStatusMonitor(enabled bool, owners []Owner) (*StatusMonitor, error) {
	registered := make([]registeredOwner, 0, len(owners))
	for _, owner := range owners {
		if owner == nil {
			return nil, errors.New("lifecycle status owner is nil")
		}
		registered = append(registered, registeredOwner{name: owner.Name(), owner: owner})
	}
	normalized, err := normalizeOwners(registered)
	if err != nil {
		return nil, err
	}
	status := Status{
		SchemaVersion: StatusSchema,
		Policy: StatusPolicy{
			Enabled: enabled, Owners: len(normalized),
			SoftWatermarkPercent:   SoftWatermarkPercent,
			HardWatermarkPercent:   HardWatermarkPercent,
			ResumeWatermarkPercent: ResumeWatermarkPercent,
			MaxCandidatesPerTurn:   MaxCandidatesPerTick,
			MaxDeletesPerTurn:      MaxDeletesPerTick,
			MaxQueriesPerTurn:      MaxQueriesPerTick,
		},
		Capacity: CapacityStatus{
			Completeness: Unavailable, Pressure: PressureUnavailable,
		},
		Owners: make([]OwnerStatus, 0, len(normalized)),
	}
	for _, owner := range normalized {
		status.Owners = append(status.Owners, OwnerStatus{
			Name: owner.name, State: "not_run", Completeness: Unavailable,
		})
	}
	return &StatusMonitor{status: status, now: time.Now}, nil
}

func (monitor *StatusMonitor) ObserveOwner(result OwnerResult) {
	if monitor == nil {
		return
	}
	monitor.mu.Lock()
	defer monitor.mu.Unlock()
	index, ok := slices.BinarySearchFunc(monitor.status.Owners, result.Owner, func(status OwnerStatus, name string) int {
		if status.Name < name {
			return -1
		}
		if status.Name > name {
			return 1
		}
		return 0
	})
	if !ok {
		return
	}
	attempted := result.AttemptedAt
	if attempted.IsZero() {
		attempted = monitor.now().UTC()
	}
	state := "ok"
	if result.Err != nil {
		state = "error"
	}
	monitor.status.Owners[index] = OwnerStatus{
		Name: result.Owner, State: state, Completeness: result.Completeness,
		Scanned: result.Scanned, Deleted: result.Deleted, Backlog: result.More,
		LogicalBytes: result.LogicalBytes, RootBytes: result.RootBytes,
		MemberBytes: result.MemberBytes,
		AttemptedAt: &attempted,
	}
}

func (monitor *StatusMonitor) ObserveCapacity(capacity Capacity, err error) {
	if monitor == nil {
		return
	}
	monitor.mu.Lock()
	defer monitor.mu.Unlock()
	if capacity.Pressure == PressureUnavailable || errors.Is(err, ErrCapacityUnavailable) {
		monitor.status.Capacity = CapacityStatus{
			Completeness: Unavailable, Pressure: PressureUnavailable,
		}
		return
	}
	observed := monitor.now().UTC()
	total, used, percent := capacity.TotalBytes, capacity.UsedBytes, capacity.UsedPercent
	monitor.status.Capacity = CapacityStatus{
		Completeness: Exact, Pressure: capacity.Pressure,
		TotalBytes: &total, UsedBytes: &used, UsedPercent: &percent,
		ObservedAt: &observed,
	}
}

func (monitor *StatusMonitor) Snapshot() Status {
	if monitor == nil {
		return Status{}
	}
	monitor.mu.RLock()
	defer monitor.mu.RUnlock()
	result := monitor.status
	result.Owners = append([]OwnerStatus(nil), monitor.status.Owners...)
	return result
}

func ValidateStatus(status Status) error {
	if status.SchemaVersion != StatusSchema || status.Policy.Owners < 1 ||
		status.Policy.Owners != len(status.Owners) ||
		status.Policy.SoftWatermarkPercent != SoftWatermarkPercent ||
		status.Policy.HardWatermarkPercent != HardWatermarkPercent ||
		status.Policy.ResumeWatermarkPercent != ResumeWatermarkPercent ||
		status.Policy.MaxCandidatesPerTurn != MaxCandidatesPerTick ||
		status.Policy.MaxDeletesPerTurn != MaxDeletesPerTick ||
		status.Policy.MaxQueriesPerTurn != MaxQueriesPerTick {
		return errors.New("lifecycle status policy is invalid")
	}
	previous := ""
	for _, owner := range status.Owners {
		if owner.Name == "" || owner.Name <= previous ||
			(owner.State != "not_run" && owner.State != "ok" && owner.State != "error") ||
			(owner.Completeness != Exact && owner.Completeness != LowerBound && owner.Completeness != Unavailable) ||
			owner.Scanned < 0 || owner.Scanned > MaxCandidatesPerTick ||
			owner.Deleted < 0 || owner.Deleted > MaxDeletesPerTick ||
			owner.LogicalBytes < 0 || owner.LogicalBytes > servicecatalogv3.MaxLogicalBytes ||
			owner.RootBytes < 0 || owner.RootBytes > servicecatalogv3.MaxRootBytes ||
			owner.MemberBytes < 0 || owner.MemberBytes > servicecatalogv3.MaxMemberBytes {
			return errors.New("lifecycle owner status is invalid")
		}
		previous = owner.Name
	}
	if status.Capacity.Completeness == Unavailable {
		if status.Capacity.Pressure != PressureUnavailable || status.Capacity.TotalBytes != nil ||
			status.Capacity.UsedBytes != nil || status.Capacity.UsedPercent != nil {
			return errors.New("unavailable lifecycle capacity has values")
		}
		return nil
	}
	if status.Capacity.Completeness != Exact || status.Capacity.Pressure == PressureUnavailable ||
		status.Capacity.TotalBytes == nil || status.Capacity.UsedBytes == nil ||
		status.Capacity.UsedPercent == nil || *status.Capacity.TotalBytes <= 0 ||
		*status.Capacity.UsedBytes < 0 || *status.Capacity.UsedBytes > *status.Capacity.TotalBytes ||
		*status.Capacity.UsedPercent < 0 || *status.Capacity.UsedPercent > 100 {
		return errors.New("exact lifecycle capacity is invalid")
	}
	return nil
}
