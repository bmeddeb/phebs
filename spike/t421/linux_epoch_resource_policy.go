//go:build linux

package t421

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"runtime"
	"sync"

	"github.com/bmeddeb/phebs/spike/t4013"
)

const (
	linuxEpochRSSReservation       = uint64(6 << 30)
	linuxEpochKernelCacheAllowance = uint64(1 << 30)
	linuxEpochHostHeadroom         = uint64(1 << 30)
)

var errLinuxEpochResources = errors.New("linux epoch operational resource binding unavailable")

// These are prospective rehearsal constraints, not predicted consumption or
// kernel-enforced reservations. Formal Linux plan/profile admission is separate.
type linuxEpochResourcePolicy struct {
	Schema                    string `json:"schema"`
	RSSReservationBytes       uint64 `json:"rss_reservation_bytes"`
	KernelCacheAllowanceBytes uint64 `json:"kernel_cache_allowance_bytes"`
	HostHeadroomBytes         uint64 `json:"host_headroom_bytes"`
	Scope                     string `json:"scope"`
	Concurrency               string `json:"concurrency"`
	Payload                   string `json:"payload"`
	Initial                   string `json:"initial"`
	Ongoing                   string `json:"ongoing"`
	Sampling                  string `json:"sampling"`
}

func linuxEpochPolicy() linuxEpochResourcePolicy {
	return linuxEpochResourcePolicy{
		Schema:              "t42-linux-epoch-operational-resources-v1",
		RSSReservationBytes: linuxEpochRSSReservation, KernelCacheAllowanceBytes: linuxEpochKernelCacheAllowance, HostHeadroomBytes: linuxEpochHostHeadroom,
		Scope:       "unprivileged-linux-amd64;one-bound-controller-lifetime-and-all-descendants;mnt-user-pid-cgroup-namespace-pins;controller-heap-included;server-subgauges-not-added",
		Concurrency: "one-server-and-one-serial-author;return-after-predecessor-join;no-preparation-children-or-failed-prefixes",
		Payload:     "tree-order=build-inputs,git,catalogs,configs;four-distinct-readonly-tmpfs-mounts:checked-(blocks-bfree)*bsize;image-order=t422-author,phebs,zoekt-git-index,surreal,plan;five-distinct-sealed-images-plan:max(page-rounded-size,512*st_blocks);metadata-in-kernel-cache-allowance;RSS-overlap-deliberately-conservative",
		Initial:     "H2l-native-complete-ancestry;H2j-full-RSS-plus-payload-plus-kernel-cache-plus-host-headroom-fits-capacity-and-available;no-owned-memory-credit;bounded-8192-controller-FD-inventory-refuses-extra-memfd-or-tmpfs",
		Ongoing:     "same-physical-ancestry-limits-namespaces-and-payloads;full-initial-required-fits-capacity;fresh-available-at-least-kernel-cache-plus-host-headroom;RSS-at-most-reservation;no-subtraction-or-re-reservation",
		Sampling:    "serialized-whole-and-server-native-census-memory-image-inspections;bounded-exact-row-class-cache-per-census;pure-aggregation-may-overlap;250ms-tickers-drop-missed-ticks;2s-cooperative-probe-context;native-H2l-cooperative;failure-sticky-cancellation-only;not-continuous-enforcement",
	}
}

func linuxEpochPolicySHA256() string {
	raw, err := json.Marshal(linuxEpochPolicy())
	if err != nil {
		panic("fixed Linux epoch resource policy cannot encode")
	}
	return SHA256(raw)
}

type linuxEpochResourceAdmission struct {
	mu          sync.Mutex
	held        *linuxEpochHeldFlow
	cancel      context.CancelFunc
	probeGate   chan struct{}
	namespaces  linuxEpochResourceNamespaces
	footprint   linuxEpochResourceFootprint
	initial     t4013.LinuxMemoryObservation
	calculation t4013.LinuxMemoryCalculation
	policySHA   string
	checks      uint64
	uid, euid   int
	issued      bool
	closed      bool
	err         error
}

// The only production issuer receives the genuine complete held-loan scope.
// Supplied observations, contracts, paths and size claims cannot issue it.
func admitLinuxEpochResources(ctx context.Context, held *linuxEpochHeldFlow, cancel context.CancelFunc) (*linuxEpochResourceAdmission, error) {
	if ctx == nil || ctx.Err() != nil || runtime.GOARCH != "amd64" || os.Getuid() == 0 || os.Geteuid() == 0 || os.Getuid() != os.Geteuid() || held == nil || cancel == nil || held.controllerPID <= 1 || held.controllerPID != os.Getpid() || held.controllerStart == "" {
		return nil, errLinuxEpochResources
	}
	a := &linuxEpochResourceAdmission{held: held, cancel: cancel, probeGate: make(chan struct{}, 1), policySHA: linuxEpochPolicySHA256(), uid: os.Getuid(), euid: os.Geteuid()}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var err error
	a.namespaces, err = captureLinuxEpochResourceNamespaces()
	if err == nil {
		a.footprint, err = collectLinuxEpochResourceFootprint(ctx, held)
	}
	if err == nil {
		err = checkLinuxEpochPreparedDirectories(held)
	}
	if err == nil {
		err = checkLinuxEpochInitialAnonymousObjects(a.footprint)
	}
	if err == nil {
		var rows []t4013.NativeProcessRecord
		rows, err = t4013.ObserveProcessTreeRecords(ctx, held.controllerPID)
		if err == nil && (len(rows) != 1 || rows[0].PID != held.controllerPID || rows[0].StartIdentity != held.controllerStart || rows[0].RSSBytes <= 0 || uint64(rows[0].RSSBytes) > linuxEpochRSSReservation) {
			err = errLinuxEpochResources
		}
	}
	if err == nil {
		a.initial, err = t4013.ObserveLinuxMemory(ctx)
	}
	if err == nil {
		a.calculation, err = evaluateLinuxEpochResources(a.initial, a.footprint.PayloadBytes)
	}
	if err == nil {
		err = a.namespaces.check()
	}
	if err != nil || ctx.Err() != nil {
		a.Fail()
		return a, errLinuxEpochResources
	}
	a.issued = true
	return a, nil
}

func evaluateLinuxEpochResources(observation t4013.LinuxMemoryObservation, payload uint64) (t4013.LinuxMemoryCalculation, error) {
	if payload == 0 || payload > math.MaxInt64 {
		return t4013.LinuxMemoryCalculation{}, errLinuxEpochResources
	}
	return t4013.EvaluateLinuxMemoryAdmission(observation, t4013.LinuxMemoryContract{
		HostHeadroomBytes: int64(linuxEpochHostHeadroom),
		RequiredScopes:    []t4013.LinuxMemoryScope{{Name: "immutable-payloads", MaximumConcurrent: 1}, {Name: "kernel-cache-allowance", MaximumConcurrent: 1}, {Name: "operational-controller-tree", MaximumConcurrent: 1}},
		Reservations:      []t4013.LinuxMemoryReservation{{Scope: "immutable-payloads", MaximumConcurrent: 1, BytesPerInstance: int64(payload)}, {Scope: "kernel-cache-allowance", MaximumConcurrent: 1, BytesPerInstance: int64(linuxEpochKernelCacheAllowance)}, {Scope: "operational-controller-tree", MaximumConcurrent: 1, BytesPerInstance: int64(linuxEpochRSSReservation)}},
	})
}

func sameLinuxEpochMemoryBinding(before, after t4013.LinuxMemoryObservation) bool {
	if before.PhysicalBytes != after.PhysicalBytes || !before.CgroupAncestryComplete || !after.CgroupAncestryComplete || len(before.CgroupAncestors) != len(after.CgroupAncestors) {
		return false
	}
	for i, row := range before.CgroupAncestors {
		current := after.CgroupAncestors[i]
		if row.Identity != current.Identity || row.ParentIdentity != current.ParentIdentity || row.HierarchyRoot != current.HierarchyRoot || row.Unlimited != current.Unlimited || row.LimitBytes != current.LimitBytes {
			return false
		}
	}
	return true
}

// No sampled RSS is credited against MemAvailable or memory.current. The full
// reservation remains held; this independent live rule only tests spare slack.
func checkLinuxEpochLiveMemory(initial t4013.LinuxMemoryObservation, required int64, current t4013.LinuxMemoryObservation) error {
	if required <= 0 || !sameLinuxEpochMemoryBinding(initial, current) {
		return errLinuxEpochResources
	}
	calculation, err := t4013.EvaluateLinuxMemoryAdmission(current, t4013.LinuxMemoryContract{
		HostHeadroomBytes: int64(linuxEpochHostHeadroom),
		RequiredScopes:    []t4013.LinuxMemoryScope{{Name: "kernel-cache-allowance", MaximumConcurrent: 1}},
		Reservations:      []t4013.LinuxMemoryReservation{{Scope: "kernel-cache-allowance", MaximumConcurrent: 1, BytesPerInstance: int64(linuxEpochKernelCacheAllowance)}},
	})
	if err != nil || required > calculation.EffectiveCapacityBytes {
		return errLinuxEpochResources
	}
	return nil
}

// Check performs fixed native metadata/allocation inspection and a complete
// H2l observation, never a source walk, payload hash or child launch.
func (a *linuxEpochResourceAdmission) Check(ctx context.Context) error {
	if a == nil {
		return errLinuxEpochResources
	}
	a.mu.Lock()
	err := a.checkLocked(ctx)
	first := err != nil && a.err == nil
	if err != nil {
		a.err = errLinuxEpochResources
	}
	a.mu.Unlock()
	if err != nil {
		if first && a.cancel != nil {
			a.cancel()
		}
		return errLinuxEpochResources
	}
	return nil
}

func (a *linuxEpochResourceAdmission) checkLocked(ctx context.Context) error {
	if ctx == nil || ctx.Err() != nil || !a.issued || a.closed || a.err != nil || a.held == nil || a.held.controllerPID != os.Getpid() || a.uid != os.Getuid() || a.euid != os.Geteuid() || a.checks == math.MaxUint64 {
		return errLinuxEpochResources
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if a.namespaces.check() != nil {
		return errLinuxEpochResources
	}
	current, err := collectLinuxEpochResourceFootprint(ctx, a.held)
	if err != nil || current != a.footprint {
		return errLinuxEpochResources
	}
	observation, err := t4013.ObserveLinuxMemory(ctx)
	if err != nil || checkLinuxEpochLiveMemory(a.initial, a.calculation.RequiredBytes, observation) != nil || a.namespaces.check() != nil || ctx.Err() != nil {
		return errLinuxEpochResources
	}
	a.checks++
	return nil
}

// Fail only latches and requests cancellation. It never joins children, closes
// loans or calls back into the sampler whose lock may currently be held.
func (a *linuxEpochResourceAdmission) Fail() {
	if a == nil {
		return
	}
	a.mu.Lock()
	first := a.err == nil
	a.err = errLinuxEpochResources
	a.mu.Unlock()
	if first && a.cancel != nil {
		a.cancel()
	}
}

func (a *linuxEpochResourceAdmission) RSSLimit() uint64 {
	if a == nil {
		return 0
	}
	return linuxEpochRSSReservation
}

func (a *linuxEpochResourceAdmission) startProcessObservation(ctx context.Context, phase uint32, rootName string, gauge *ProcessObservationGauge) (*epochProcessObservation, error) {
	if a == nil || a.held == nil || gauge == nil || gauge.expectedRootStartIdentity != a.held.controllerStart {
		return nil, errLinuxEpochResources
	}
	return a.startServerObservation(ctx, a.held.controllerPID, phase, rootName, gauge, func() {})
}

// Server meters retain their own root/phase evidence. Their RSS is never added
// to the controller meter; both native probe paths share this one bounded gate.
func (a *linuxEpochResourceAdmission) startServerObservation(ctx context.Context, pid int, phase uint32, rootName string, gauge *ProcessObservationGauge, fail func()) (*epochProcessObservation, error) {
	if a == nil || gauge == nil || gauge.classify == nil || gauge.expectedRootStartIdentity == "" || fail == nil || a.Check(ctx) != nil {
		return nil, errLinuxEpochResources
	}
	var failed sync.Once
	notify := func() { failed.Do(func() { a.Fail(); fail() }) }
	classify := gauge.classify
	var accepted map[t4013.NativeProcessRecord]int
	// The gauge owns this cache under its existing sampling mutex. The initial
	// constructor uses it before starting that ticker. Only exact census rows
	// can consume classifications; kernel names and birth identities stay intact.
	gauge.classify = func(ctx context.Context, row t4013.NativeProcessRecord) (int, bool) {
		if ctx == nil || ctx.Err() != nil {
			return 0, false
		}
		class, ok := accepted[row]
		return class, ok
	}
	probe := func(ctx context.Context, root int) ([]t4013.NativeProcessRecord, error) {
		accepted = nil
		select {
		case a.probeGate <- struct{}{}:
			defer func() { <-a.probeGate }()
		case <-ctx.Done():
			notify()
			return nil, ctx.Err()
		}
		if a.Check(ctx) != nil {
			notify()
			return nil, errLinuxEpochResources
		}
		// Check resources before the census so bounded H2l inspection does not
		// widen the observed-row to executable-object classification window.
		rows, err := t4013.ObserveProcessTreeRecords(ctx, root)
		if err != nil {
			notify()
			return nil, err
		}
		if len(rows) == 0 || len(rows) > t4013.MaxNativeProcessRecords {
			notify()
			return nil, errLinuxEpochResources
		}
		classes := make(map[t4013.NativeProcessRecord]int, len(rows))
		for _, row := range rows {
			class, ok := classify(ctx, row)
			if !ok || class < 0 || class >= len(gauge.observation.Classes) || ctx.Err() != nil {
				notify()
				return nil, errLinuxEpochResources
			}
			classes[row] = class
		}
		accepted = classes
		return rows, nil
	}
	meter, err := newExecutionProcessObservationWithGauge(ctx, pid, phase, rootName, notify, probe, a.RSSLimit(), gauge)
	if err != nil {
		notify()
	}
	return meter, err
}

// Only the tracked worker calls this after its whole sampler and every actual
// operational/cleanup borrower have joined. Uncertain prefixes retain the pins.
func (a *linuxEpochResourceAdmission) Close() error {
	if a == nil {
		return errLinuxEpochResources
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.closed {
		a.closed = true
		if a.namespaces.close() != nil {
			a.err = errLinuxEpochResources
		}
	}
	return a.err
}
