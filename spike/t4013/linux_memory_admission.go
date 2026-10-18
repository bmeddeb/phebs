package t4013

import (
	"errors"
	"fmt"
	"strings"
)

const (
	maxLinuxMemoryAncestors = 64
	maxLinuxMemoryScopes    = 32
	maxLinuxMemoryScopeName = 96
)

// LinuxMemoryObservation contains supplied facts, not a native measurement or
// an authenticated admission. A future observer must establish the complete
// cgroup-v2 ancestry, its identity/stability, and the physical/available values.
// No swap allowance is represented. AvailableBytes is physical MemAvailable;
// it must not include reclaimable swap or an allowance from memory.swap.max.
type LinuxMemoryObservation struct {
	PhysicalBytes          int64                          `json:"physical_bytes"`
	AvailableBytes         int64                          `json:"available_bytes"`
	CgroupAncestryComplete bool                           `json:"cgroup_ancestry_complete"`
	CgroupAncestors        []LinuxMemoryCgroupObservation `json:"cgroup_ancestors"`
}

// LinuxMemoryCgroupObservation rows are ordered root to leaf. Identities are
// source-free SHA-256 bindings supplied by the observer. The root has no parent;
// every later parent is the preceding identity. Unlimited represents literal
// memory.max=max and requires LimitBytes=0; CurrentBytes remains mandatory.
type LinuxMemoryCgroupObservation struct {
	Identity       string `json:"identity"`
	ParentIdentity string `json:"parent_identity"`
	Unlimited      bool   `json:"unlimited"`
	LimitBytes     int64  `json:"limit_bytes"`
	CurrentBytes   int64  `json:"current_bytes"`
}

// LinuxMemoryContract is a prospective source-supplied scope/concurrency census
// and its reservation. These rows do not prove process ownership, complete
// descendants, enforced limits, or the amount of memory an instance will use.
// RequiredScopes and Reservations must have the same canonical name order.
type LinuxMemoryContract struct {
	HostHeadroomBytes int64                    `json:"host_headroom_bytes"`
	RequiredScopes    []LinuxMemoryScope       `json:"required_scopes"`
	Reservations      []LinuxMemoryReservation `json:"reservations"`
}

// LinuxMemoryScope names disjoint accounting scopes. A slash denotes scope
// containment, so a parent and its descendant cannot both occur in the census.
// MaximumConcurrent is a concurrent instance count, never a dispatch-attempt
// count. A process-tree reservation must include all its descendants' memory.
type LinuxMemoryScope struct {
	Name              string `json:"name"`
	MaximumConcurrent int64  `json:"maximum_concurrent"`
}

type LinuxMemoryReservation struct {
	Scope             string `json:"scope"`
	MaximumConcurrent int64  `json:"maximum_concurrent"`
	BytesPerInstance  int64  `json:"bytes_per_instance"`
}

// LinuxMemoryCalculation is arithmetic over supplied inputs. It carries no
// PASS, native authority, plan version, or permission to launch a child.
type LinuxMemoryCalculation struct {
	EffectiveCapacityBytes int64 `json:"effective_capacity_bytes"`
	AvailableBytes         int64 `json:"available_bytes"`
	ReservedBytes          int64 `json:"reserved_bytes"`
	HostHeadroomBytes      int64 `json:"host_headroom_bytes"`
	RequiredBytes          int64 `json:"required_bytes"`
}

// EvaluateLinuxMemoryAdmission conservatively requires the complete reservation
// plus headroom to fit both physical/effective-cgroup capacity and observed
// available slack. It does not credit memory already occupied by owned work:
// attribution and a native admission boundary are intentionally not established.
// Every refusal returns a zero calculation. It performs no I/O or mutation.
func EvaluateLinuxMemoryAdmission(observation LinuxMemoryObservation, contract LinuxMemoryContract) (LinuxMemoryCalculation, error) {
	capacity, available, err := linuxMemoryCapacity(observation)
	if err != nil {
		return LinuxMemoryCalculation{}, err
	}
	reserved, err := linuxMemoryReservations(contract)
	if err != nil {
		return LinuxMemoryCalculation{}, err
	}
	required, err := checkedAddInt64(reserved, contract.HostHeadroomBytes)
	if err != nil {
		return LinuxMemoryCalculation{}, fmt.Errorf("sum Linux memory reservation and headroom: %w", err)
	}
	if required > capacity || required > available {
		return LinuxMemoryCalculation{}, errors.New("Linux memory reservation and headroom exceed supplied capacity or available slack")
	}
	return LinuxMemoryCalculation{
		EffectiveCapacityBytes: capacity, AvailableBytes: available,
		ReservedBytes: reserved, HostHeadroomBytes: contract.HostHeadroomBytes,
		RequiredBytes: required,
	}, nil
}

func linuxMemoryCapacity(observation LinuxMemoryObservation) (int64, int64, error) {
	if observation.PhysicalBytes <= 0 || observation.AvailableBytes < 0 ||
		observation.AvailableBytes > observation.PhysicalBytes || !observation.CgroupAncestryComplete ||
		len(observation.CgroupAncestors) == 0 || len(observation.CgroupAncestors) > maxLinuxMemoryAncestors {
		return 0, 0, errors.New("Linux memory observation is incomplete or invalid")
	}
	capacity, available := observation.PhysicalBytes, observation.AvailableBytes
	identities := make(map[string]struct{}, len(observation.CgroupAncestors))
	parent := ""
	for _, ancestor := range observation.CgroupAncestors {
		if !digestIdentity(ancestor.Identity) {
			return 0, 0, errors.New("Linux memory cgroup identity is invalid")
		}
		if _, duplicate := identities[ancestor.Identity]; duplicate || ancestor.ParentIdentity != parent ||
			ancestor.CurrentBytes < 0 || ancestor.LimitBytes < 0 ||
			ancestor.Unlimited && ancestor.LimitBytes != 0 ||
			!ancestor.Unlimited && (ancestor.LimitBytes == 0 || ancestor.CurrentBytes > ancestor.LimitBytes) {
			return 0, 0, errors.New("Linux memory cgroup ancestry or limit is invalid")
		}
		identities[ancestor.Identity] = struct{}{}
		parent = ancestor.Identity
		if !ancestor.Unlimited {
			capacity = min(capacity, ancestor.LimitBytes)
			available = min(available, ancestor.LimitBytes-ancestor.CurrentBytes)
		}
	}
	return capacity, available, nil
}

func linuxMemoryReservations(contract LinuxMemoryContract) (int64, error) {
	if contract.HostHeadroomBytes <= 0 || len(contract.RequiredScopes) == 0 ||
		len(contract.RequiredScopes) > maxLinuxMemoryScopes || len(contract.Reservations) != len(contract.RequiredScopes) {
		return 0, errors.New("Linux memory reservation census or headroom is invalid")
	}
	names := make(map[string]struct{}, len(contract.RequiredScopes))
	previous := ""
	var reserved int64
	for index, scope := range contract.RequiredScopes {
		reservation := contract.Reservations[index]
		if !validLinuxMemoryScope(scope.Name) || scope.Name <= previous || scope.MaximumConcurrent <= 0 ||
			reservation.Scope != scope.Name || reservation.MaximumConcurrent != scope.MaximumConcurrent ||
			reservation.BytesPerInstance <= 0 {
			return 0, errors.New("Linux memory scope or matching reservation is invalid")
		}
		for offset := 0; offset < len(scope.Name); offset++ {
			if scope.Name[offset] == '/' {
				if _, overlaps := names[scope.Name[:offset]]; overlaps {
					return 0, errors.New("Linux memory reservation scopes overlap")
				}
			}
		}
		names[scope.Name] = struct{}{}
		previous = scope.Name
		bytes, err := checkedMulInt64(scope.MaximumConcurrent, reservation.BytesPerInstance)
		if err != nil {
			return 0, fmt.Errorf("multiply Linux memory concurrent reservation: %w", err)
		}
		reserved, err = checkedAddInt64(reserved, bytes)
		if err != nil {
			return 0, fmt.Errorf("sum Linux memory concurrent reservations: %w", err)
		}
	}
	return reserved, nil
}

func validLinuxMemoryScope(name string) bool {
	if len(name) == 0 || len(name) > maxLinuxMemoryScopeName ||
		strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") || strings.Contains(name, "//") {
		return false
	}
	for _, value := range name {
		if value != '/' && value != '-' && value != '_' &&
			(value < 'a' || value > 'z') && (value < '0' || value > '9') {
			return false
		}
	}
	return true
}
