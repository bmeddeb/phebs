//go:build linux

package t421

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"runtime"
	"strings"
	"sync"

	"github.com/bmeddeb/phebs/spike/t4013"
)

const (
	linuxEpochRSSReservation       = uint64(4 << 30)
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
	Tracing                   string `json:"tracing"`
	DyingGroups               string `json:"dying_groups"`
	IndexRuntime              string `json:"index_runtime"`
	GitRuntime                string `json:"git_runtime"`
	EngineMeasurement         string `json:"engine_measurement,omitempty"`
}

func linuxEpochPolicyV7() linuxEpochResourcePolicy {
	return linuxEpochResourcePolicy{
		Schema:              "t42-linux-epoch-operational-resources-v7",
		RSSReservationBytes: linuxEpochRSSReservation, KernelCacheAllowanceBytes: linuxEpochKernelCacheAllowance, HostHeadroomBytes: linuxEpochHostHeadroom,
		Scope:        "unprivileged-linux-amd64;one-bound-controller-lifetime-and-all-descendants;mnt-user-pid-cgroup-namespace-pins;controller-heap-included;server-subgauges-not-added",
		Concurrency:  "one-server-and-one-serial-author;return-after-predecessor-join;no-preparation-children-or-failed-prefixes",
		IndexRuntime: "resource-issued-authenticated-git-and-zoekt-binding=t42-linux-foreground-git-serial-index-v1;server-producers=2,3,4;exact-existing-HEAD-index-argv-then-one-parallelism=1-before-final-repository;one-child-and-synchronous-shard-builds;current-bindings-compared-before-every-DA-Start;no-new-workspace-authority-or-fit-promise",
		GitRuntime:   "resource-checked-server-git-and-zoekt-environments;gc.autoDetach=false;maintenance.autoDetach=false;automatic-maintenance-remains-enabled-and-foreground;exact-five-config-pairs-authenticated-and-compared-before-every-DA-Start;original-parent-birth-SID-namespace-pidfd-image-RSS-and-genuine-Wait-gates-unchanged;no-orphan-adoption",
		Payload:      "tree-order=build-inputs,git,catalogs,configs;four-distinct-readonly-tmpfs-mounts:checked-(blocks-bfree)*bsize;image-order=t422-author,phebs,zoekt-git-index,surreal,plan;five-distinct-sealed-images-plan:max(page-rounded-size,512*st_blocks);metadata-in-kernel-cache-allowance;RSS-overlap-deliberately-conservative",
		Initial:      "H2l-native-complete-ancestry;H2j-full-RSS-plus-payload-plus-kernel-cache-plus-host-headroom-fits-capacity-and-available;no-owned-memory-credit;bounded-8192-controller-FD-inventory-refuses-extra-memfd-or-tmpfs",
		Ongoing:      "same-physical-ancestry-limits-namespaces-and-payloads;full-initial-required-fits-capacity;fresh-available-at-least-kernel-cache-plus-host-headroom;RSS-at-most-reservation;no-subtraction-or-re-reservation",
		Sampling:     "serialized-whole-and-server-native-census-memory-image-inspections;bounded-exact-row-class-cache-per-census;event-owned-defunct-descendants-at-classification:current-original-group-pidfd-unconsumed-actual-terminal-CLD-birth-real-parent-name-SID-namespace-tracer:no-exec-claim;preserve-native-row-RSS;nonroot-tracer-terminal-copy-through-real-parent-pidfd-HUP-shares-probe-gate;direct-root-unconsumed-terminal-original-SID-drain-then-sole-genuine-Wait:gate-released-at-actual-OS-reap-not-DA-or-pump-completion;slow-reap-or-gate-contention-over-2s-may-refuse;pure-aggregation-may-overlap;250ms-tickers-drop-missed-ticks;2s-cooperative-probe-context;native-H2l-cooperative;failure-sticky-cancellation-only;not-continuous-enforcement",
		Tracing:      fmt.Sprintf("held-resource-issued;two-root-actors;queued-probe-gate-acquisition-within-existing-2s-turn-including-native-work:no-scheduler-latency-guarantee;stop-and-grace-wakes-may-buffer-during-that-budget;pidfd-poll-EINTR-only-same-fd-existing-context-retry;unbounded-cleanup-loops-one-real-poll-per-existing-wake-or-tick:EINTR-grants-no-readiness;actual-DA-Start-and-sole-root-Wait-on-locked-creator;Start-and-initial-capture-share-probe-gate;process-leader-cap=%d;active-plus-witnessed-EXIT-TID-cap=%d;original-process-pidfds-at-most-leader-cap;recent-event-ring-per-root=%d;completed-three-server-ring-and-map-backing-retained-in-controller-RSS;overflow-safe-counters-no-cumulative-history;SIGCHLD-wakes-plus-%dms-fallback;targeted-registered-waits-no-global-wait-no-syscall-tracing;actual-held-image-before-root-entry-inherited-registration-and-every-EXEC-continue;no-process-leader-detach-before-terminal-copy;queued-cancellation-serviced-through-real-joins;final-retirement-actor-TracerPid-status-census-before-and-after-observer-join:host-entry-cap=%d,status-read-cap=64KiB:no-new-signal-or-reap-authority;unknown-closure-retains-actor-and-loans", linuxEpochTraceMaxLeaders, linuxEpochTraceMaxTIDs, linuxEpochTraceRecentEvents, linuxEpochTraceFallback.Milliseconds(), linuxEpochTraceMaxHostEntries),
		DyingGroups:  "late-current-nonleader-custody-only;original-registered-unreaped-process-pidfd-and-TGID;original-parent-birth-SID-pid-namespace-own-TracerPid;fresh-bracketed-task-birth-name-and-actual-held-mm-before-owned-stop-continuation;no-recovered-creation-event-or-history;no-process-leader-adoption;bounded-known-group-task-directory-under-global-active-TID-cap-and-overflow-sentinel;only-after-registered-siblings-drain;witnessed-nonleader-EXIT-detach-keeps-existing-global-TID-reservation-until-current-absence-new-birth-or-actual-group-CLD:no-signal-or-reattach-authority;dying-group-cleanup-only;leader-exit-through-actual-group-terminal-CLD-shares-existing-probe-gate;Z-leader-with-live-siblings-grants-no-zero-RSS-category;same-existing-2s-cooperative-probe-and-always-live-whole-sampler-no-new-group-duration-bound;actual-sole-root-Wait-status-preserved",
	}
}

func linuxEpochPolicy() linuxEpochResourcePolicy {
	policy := linuxEpochPolicyV7()
	policy.Schema = "t42-linux-epoch-operational-resources-v8"
	policy.EngineMeasurement = "authenticated-EM01-FD7-only-with-existing-FD6;server-producers=2,3,4;maximum-points=6,1,2;existing-phases=2-3-4,5,6;original-producer-binding-and-input-SHA;no-request-PID-path-image-or-action;unique-already-registered-held-Surreal-direct-child-original-pidfd-parent-birth-SID-namespace-TracerPid-and-mm;SIGSTOP-forwarded-to-actual-owned-groupstop-every-current-source-registered-TID;two-bounded-equal-task-censuses-under-existing-global-1024-cap;hold-ACK-only-after-all-actual-groupstop-witnesses;probe-gate-released-through-walk-and-whole-meter-continues;SIGCONT-then-CONT-only-original-owned-held-slots;release-ACK-after-real-all-thread-resume;existing-2s-cooperative-actor-turn-and-original-operation-deadline;existing-5s-cleanup-only;incomplete-stop-abort-original-pidfd-kill-retains-genuine-Wait-and-custody;one-extra-socket-pair-one-endpoint-goroutine-one-lease-per-live-server;finite-152+80*points-wire-bytes-subtracted-from-existing-output-stream-ceiling;no-query-sync-retry-publication-work-no-new-child-or-corpus-hash;historical-v7-bytes-and-formal-V1-V6-unchanged"
	return policy
}

func linuxEpochPolicySHA256() string {
	raw, err := json.Marshal(linuxEpochPolicy())
	if err != nil {
		panic("fixed Linux epoch resource policy cannot encode")
	}
	return SHA256(raw)
}

type linuxEpochResourceAdmission struct {
	mu             sync.Mutex
	held           *linuxEpochHeldFlow
	cancel         context.CancelFunc
	probeGate      chan struct{}
	trace          *linuxEpochTraceOwner               // Issued before operations; retired before namespace pins.
	ownedStops     map[int]*linuxEpochSessionAuthority // Only while probeGate is held.
	namespaces     linuxEpochResourceNamespaces
	footprint      linuxEpochResourceFootprint
	initial        t4013.LinuxMemoryObservation
	calculation    t4013.LinuxMemoryCalculation
	policySHA      string
	checks         uint64
	uid, euid      int
	issued         bool
	closed         bool
	err            error
	closeErr       error
	privateFailure string // First bounded private predicate, never public observation/receipt authority.
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
	stage := "initial-namespace-capture"
	a.namespaces, err = captureLinuxEpochResourceNamespaces()
	if err == nil {
		stage = "initial-payload-observation"
		a.footprint, err = collectLinuxEpochResourceFootprint(ctx, held)
	}
	if err == nil {
		stage = "initial-prepared-directories"
		err = checkLinuxEpochPreparedDirectories(held)
	}
	if err == nil {
		stage = "initial-anonymous-inventory"
		err = checkLinuxEpochInitialAnonymousObjects(a.footprint)
	}
	if err == nil {
		stage = "initial-native-census"
		var rows []t4013.NativeProcessRecord
		rows, err = t4013.ObserveProcessTreeRecords(ctx, held.controllerPID)
		if err == nil && (len(rows) != 1 || rows[0].PID != held.controllerPID || rows[0].StartIdentity != held.controllerStart || rows[0].RSSBytes <= 0 || uint64(rows[0].RSSBytes) > linuxEpochRSSReservation) {
			err = errLinuxEpochResources
		}
	}
	if err == nil {
		stage = "initial-native-memory"
		a.initial, err = t4013.ObserveLinuxMemory(ctx)
	}
	if err == nil {
		stage = "initial-memory-admission"
		a.calculation, err = evaluateLinuxEpochResources(a.initial, a.footprint.PayloadBytes)
	}
	if err == nil {
		stage = "initial-namespace-after"
		err = a.namespaces.check()
	}
	if err != nil || ctx.Err() != nil {
		if err == nil {
			stage = "initial-context-after"
		}
		a.failDetail(stage)
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
	if ctx == nil || ctx.Err() != nil || !a.issued || a.closed || a.closeErr != nil || a.err != nil || a.held == nil || a.held.controllerPID != os.Getpid() || a.uid != os.Getuid() || a.euid != os.Geteuid() || a.checks == math.MaxUint64 {
		return a.refuseLocked("resource-binding-or-context")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if a.namespaces.check() != nil {
		return a.refuseLocked("namespace-before")
	}
	current, err := collectLinuxEpochResourceFootprint(ctx, a.held)
	if err != nil {
		return a.refuseLocked("payload-observation")
	}
	if current != a.footprint {
		return a.refuseLocked("payload-binding")
	}
	observation, err := t4013.ObserveLinuxMemory(ctx)
	if err != nil {
		return a.refuseLocked("native-memory-observation")
	}
	if checkLinuxEpochLiveMemory(a.initial, a.calculation.RequiredBytes, observation) != nil {
		return a.refuseLocked(fmt.Sprintf("live-memory-rule physical=%d available=%d required=%d ancestors=%d same_binding=%t", observation.PhysicalBytes, observation.AvailableBytes, a.calculation.RequiredBytes, len(observation.CgroupAncestors), sameLinuxEpochMemoryBinding(a.initial, observation)))
	}
	if a.namespaces.check() != nil {
		return a.refuseLocked("namespace-after")
	}
	if ctx.Err() != nil {
		return a.refuseLocked("resource-context-after")
	}
	a.checks++
	return nil
}

// Fail only latches and requests cancellation. It never joins children, closes
// loans or calls back into the sampler whose lock may currently be held.
func (a *linuxEpochResourceAdmission) Fail() { a.failDetail("resource-cancellation-notify") }

// Diagnostic writes precede cancellation; later cleanup cannot replace the first
// predicate. No arbitrary native error text or path enters this bounded field.
func (a *linuxEpochResourceAdmission) refuseLocked(detail string) error {
	if a.privateFailure == "" {
		a.privateFailure = strings.Clone(detail[:min(len(detail), maxPrivateProcessRefusalBytes)])
	}
	return errLinuxEpochResources
}

func (a *linuxEpochResourceAdmission) privateRefusal() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.privateFailure
}

func (a *linuxEpochResourceAdmission) failDetail(detail string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	first := a.err == nil
	_ = a.refuseLocked(detail)
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
	detail := "server-process-observation-notify"
	if a.held != nil && pid == a.held.controllerPID {
		detail = "whole-process-observation-notify"
	}
	var failed sync.Once
	notify := func() { failed.Do(func() { a.failDetail(detail); fail() }) }
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
			a.failDetail("probe-gate-context")
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
			a.failDetail("native-process-census")
			notify()
			return nil, err
		}
		if len(rows) == 0 || len(rows) > t4013.MaxNativeProcessRecords {
			a.failDetail(fmt.Sprintf("native-census-count rows=%d", len(rows)))
			notify()
			return nil, errLinuxEpochResources
		}
		classes := make(map[t4013.NativeProcessRecord]int, len(rows))
		for _, row := range rows {
			class, ok := classify(ctx, row)
			if !ok || class < 0 || class >= len(gauge.observation.Classes) || ctx.Err() != nil {
				a.failDetail(linuxEpochRowRefusal("classification-or-context", row))
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
	custodyErr := a.closeCustody()
	return errors.Join(a.operationErr(), custodyErr)
}

// The joined flow carries its operational refusal separately from actual
// keeper retirement. A successful close cannot clear that refusal.
func (a *linuxEpochResourceAdmission) closeCustody() error {
	if a == nil {
		return errLinuxEpochResources
	}
	// The actor may own probeGate while entering Check (which needs mu).
	// Join its scoped notifier and registrations before holding mu or retiring
	// namespace pins; a refused trace close leaves those pins available.
	var traceErr error
	if a.trace != nil {
		traceErr = a.trace.close()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if traceErr != nil {
		a.closeErr = errLinuxEpochResources
		return a.closeErr
	}
	return a.closeCustodyLocked()
}

func (a *linuxEpochResourceAdmission) operationErr() error {
	if a == nil {
		return errLinuxEpochResources
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.err
}

func (a *linuxEpochResourceAdmission) closeCustodyLocked() error {
	if !a.closed {
		a.closed = true
		if a.namespaces.close() != nil {
			a.closeErr = errLinuxEpochResources
		}
	}
	return a.closeErr
}
