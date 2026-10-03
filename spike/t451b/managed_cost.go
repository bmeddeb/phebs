package t451b

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/bmeddeb/phebs/internal/typedsandbox"
)

const ManagedCostSchema = "phebs-typed-native-corpus-cost-v1"
const MaxManagedCostBytes = 4 << 10
const ManagedCostStopSchema = "phebs-typed-native-corpus-cost-stop-v1"

// ManagedCost is source-free operational evidence from a test-only measured
// helper. Its enclosing native completion token must bind the exact worker
// stdout and stderr before any caller treats these measurements as established.
// It cannot grant execution, publication or registration authority.
type ManagedCost struct {
	Schema       string                  `json:"schema"`
	Observations Observations            `json:"observations"`
	Cache        PrivateCacheObservation `json:"cache"`
}

// ManagedCostStop preserves bounded failure-only sampler facts. It is never a
// complete measurement or completion authority, including when native output
// lacks a successful completion token. No raw error or source path is encoded.
type ManagedCostStop struct {
	Schema        string        `json:"schema"`
	Stage         string        `json:"stage"`
	CacheComplete bool          `json:"cache_complete"`
	Observations  *Observations `json:"observations,omitempty"`
}

func validateManagedCostStop(m ManagedCostStop) error {
	if m.Schema != ManagedCostStopSchema || m.CacheComplete {
		return errors.New("invalid managed cost stop")
	}
	switch m.Stage {
	case "observer_start":
		if m.Observations != nil {
			return errors.New("unexpected initial stop observations")
		}
		return nil
	case "worker", "observations", "cache", "cost_encode":
	default:
		return errors.New("invalid managed cost stop stage")
	}
	o := m.Observations
	if o == nil || o.Version != "phebs-t451b-sampled-observations-v1" || o.IntervalNanoseconds != observationInterval.Nanoseconds() || o.DurationNanoseconds < 0 || !o.ChildLifetimesLowerBound || !o.FDCountsNonAtomic || o.SampledChildLifetimes > maxObservedLifetimes || o.SampledProcessFDPeak > typedsandbox.DescriptorLimit || o.SampledAggregateFDPeak > typedsandbox.DescriptorLimit*typedsandbox.TaskLimit {
		return errors.New("invalid managed cost stop observations")
	}
	if o.Unavailable {
		if m.Stage != "observations" || o.UnexpectedErrors == 0 || !slices.Contains([]string{"inventory", "process", "worker", "context"}, o.Failure) {
			return errors.New("invalid sticky managed cost stop")
		}
	} else if m.Stage == "observations" || o.UnexpectedErrors != 0 || o.Failure != "" || o.FailureProcess != nil {
		return errors.New("contradictory managed cost stop observations")
	}
	if p := o.FailureProcess; p != nil {
		if o.Failure != "process" || len(p.Comm) > 64 || !utf8.ValidString(p.Comm) || strings.ContainsAny(p.Comm, "/\\") || strings.ContainsFunc(p.Comm, unicode.IsControl) || len(p.State) != 1 || !strings.ContainsAny(p.State, "RSDZTtXxKWPI") || len(p.CapPrm) != 16 || strings.Trim(p.CapPrm, "0123456789abcdef") != "" {
			return errors.New("invalid bounded managed process diagnostic")
		}
	}
	return nil
}

func EncodeManagedCostStop(m ManagedCostStop) ([]byte, error) {
	if err := validateManagedCostStop(m); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(m)
	if err != nil || len(raw)+1 > MaxManagedCostBytes {
		return nil, errors.New("managed cost stop byte bound")
	}
	return append(raw, '\n'), nil
}

func DecodeManagedCostStop(raw []byte) (ManagedCostStop, error) {
	var m ManagedCostStop
	if len(raw) == 0 || len(raw) > MaxManagedCostBytes {
		return m, errors.New("managed cost stop byte bound")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&m); err != nil {
		return m, err
	}
	canonical, err := EncodeManagedCostStop(m)
	if err != nil || !bytes.Equal(raw, canonical) {
		return ManagedCostStop{}, errors.New("managed cost stop is not exact bounded evidence")
	}
	return m, nil
}

func validateManagedCost(m ManagedCost) error {
	o, c := m.Observations, m.Cache
	if m.Schema != ManagedCostSchema || o.Version != "phebs-t451b-sampled-observations-v1" || o.IntervalNanoseconds != observationInterval.Nanoseconds() || o.DurationNanoseconds <= 0 || o.DurationNanoseconds > int64(typedsandbox.WallLimit) || o.Samples < 2 || o.SampledChildLifetimes == 0 || o.SampledChildLifetimes > maxObservedLifetimes || !o.ChildLifetimesLowerBound || !o.FDCountsNonAtomic || o.SampledProcessFDPeak == 0 || o.SampledProcessFDPeak > typedsandbox.DescriptorLimit || o.SampledAggregateFDPeak < o.SampledProcessFDPeak || o.SampledAggregateFDPeak > typedsandbox.DescriptorLimit*typedsandbox.TaskLimit || o.Unavailable || o.UnexpectedErrors != 0 || o.Failure != "" || o.FailureProcess != nil {
		return errors.New("managed process measurement unavailable or invalid")
	}
	roots := []string{"/scratch/bazel-user", "/scratch/bazel-output", "/scratch/repository-cache", "/scratch/gocache", "/scratch/gomodcache", "/scratch/cache"}
	if c.Version != "phebs-t451b-private-cache-v1" || !c.Complete || !slices.Equal(c.Roots, roots) || c.Entries == 0 || c.Entries > typedsandbox.ScratchInodes || c.LogicalBytes > typedsandbox.ScratchBytes || c.AllocatedBytes > typedsandbox.ScratchBytes || c.RegularFiles > c.Entries || c.Directories > c.Entries-c.RegularFiles || c.Symlinks != c.Entries-c.RegularFiles-c.Directories || c.UniqueInodes > c.Entries {
		return errors.New("managed private cache measurement unavailable or invalid")
	}
	missing := map[string]bool{}
	for _, name := range c.MissingRoots {
		if !slices.Contains(roots, name) || missing[name] {
			return errors.New("managed missing cache root")
		}
		missing[name] = true
	}
	return nil
}

// EncodeManagedCost accepts only complete qualified measurements under the
// frozen managed limits. Failure diagnostics never become successful evidence.
func EncodeManagedCost(m ManagedCost) ([]byte, error) {
	if err := validateManagedCost(m); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(m)
	if err != nil || len(raw)+1 > MaxManagedCostBytes {
		return nil, errors.New("managed cost byte bound")
	}
	return append(raw, '\n'), nil
}

func DecodeManagedCost(raw []byte) (ManagedCost, error) {
	var m ManagedCost
	if len(raw) == 0 || len(raw) > MaxManagedCostBytes {
		return m, errors.New("managed cost byte bound")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&m); err != nil {
		return m, err
	}
	canonical, err := EncodeManagedCost(m)
	if err != nil || !bytes.Equal(raw, canonical) {
		return ManagedCost{}, errors.New("managed cost is not exact complete evidence")
	}
	return m, nil
}
