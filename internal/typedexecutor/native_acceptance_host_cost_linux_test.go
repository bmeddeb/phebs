//go:build linux

package typedexecutor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/bmeddeb/phebs/internal/typedsandbox"
	"github.com/bmeddeb/phebs/spike/t451a"
	"github.com/bmeddeb/phebs/spike/t451b"
	"golang.org/x/sys/unix"
)

const acceptanceHostCostSchema = "phebs-typed-native-host-cost-witness-v2"

// This is a separately collected test witness, never worker stderr or native
// completion authority. Root sampler descriptors live outside the private PID
// namespace. Its FD observations remain sequential/non-atomic and child
// lifetimes remain a sampled lower bound. All v1 evidence retains its method.
type acceptanceCorpusHostWitness struct {
	Schema          string                       `json:"schema"`
	Control         typedsandbox.ControlIdentity `json:"control"`
	Allowance       typedsandbox.Allowance       `json:"allowance"`
	ContainerID     string                       `json:"container_id"`
	WorkerStart     string                       `json:"worker_start"`
	SupervisorStart string                       `json:"supervisor_start"`
	PrivateWorker   uint32                       `json:"private_worker_pid"`
	NamespaceDevice uint64                       `json:"namespace_device"`
	NamespaceInode  uint64                       `json:"namespace_inode"`
	ProcDevice      uint64                       `json:"proc_device"`
	ProcInode       uint64                       `json:"proc_inode"`
	StdoutSHA256    string                       `json:"stdout_sha256"`
	StderrSHA256    string                       `json:"stderr_sha256"`
	Observations    t451b.Observations           `json:"observations"`
	verified        bool
}

type acceptanceCorpusHostStop struct {
	Schema         string                       `json:"schema"`
	Stage          string                       `json:"stage"`
	DiagnosticOnly bool                         `json:"diagnostic_only"`
	Witness        *acceptanceCorpusHostWitness `json:"witness"`
}

func acceptanceHostCostStop(stage string, witness *acceptanceCorpusHostWitness) *acceptanceCorpusHostStop {
	copy := *witness
	// Reuse the closed v1 failure diagnostic boundary. An invalid diagnostic is
	// omitted, never used to repair the sticky failure or establish completion.
	if copy.Observations.FailureProcess != nil {
		_, err := t451b.EncodeManagedCostStop(t451b.ManagedCostStop{Schema: t451b.ManagedCostStopSchema, Stage: "observations", Observations: &copy.Observations})
		if err != nil {
			copy.Observations.FailureProcess = nil
		}
	}
	return &acceptanceCorpusHostStop{Schema: "phebs-typed-native-host-cost-stop-v2", Stage: stage, DiagnosticOnly: true, Witness: &copy}
}

// Both barriers belong only to the sealed tagged helper. No repository tool
// runs before the first CONT, and tools/cache writers are joined before the
// final STOP. A phase too short to establish resumed state honestly refuses.
func acceptanceRunRootMeasured(ctx context.Context, c nativeAcceptanceConfig, s *store.Surreal, run func(context.Context, typedsandbox.Options, typedsandbox.ScratchAuthority) (typedsandbox.Result, error), options typedsandbox.Options, authority typedsandbox.ScratchAuthority) (native typedsandbox.Result, witness *acceptanceCorpusHostWitness, stage string, err error) {
	witness = &acceptanceCorpusHostWitness{Schema: acceptanceHostCostSchema}
	stage = "configuration"
	if os.Geteuid() != 0 || ctx == nil || c.Schema != acceptanceCorpusSchema || options.Control.Validate() != nil || options.Allowance.Validate() != nil {
		return native, witness, stage, errors.New("host cost configuration")
	}
	witness.Control, witness.Allowance = options.Control, options.Allowance
	selectedRaw, e := acceptanceRead(filepath.Join(c.caseRoot(*acceptanceCase), "selected.json"), 16384)
	var selected acceptanceSelected
	if e != nil || acceptanceDecode(selectedRaw, 16384, &selected) != nil || selected.Attempt != options.Control.AttemptDigest || selected.Chunk.Generation != options.Control.PlanningDigest {
		return native, witness, stage, errors.New("host cost selected custody")
	}
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type finished struct {
		result typedsandbox.Result
		err    error
	}
	done := make(chan finished, 1)
	go func() { result, e := run(childCtx, options, authority); done <- finished{result, e} }()
	joined := false
	// Production Run owns bounded stop-only teardown. Always join that call;
	// never return a still-running native mutation or manufacture a completion.
	defer func() {
		if !joined {
			cancel()
			result := <-done
			native = result.result
			err = errors.Join(err, result.err)
		}
		witness.StdoutSHA256, witness.StderrSHA256 = acceptanceDigest(native.Stdout), acceptanceDigest(native.Stderr)
	}()
	docker := newAcceptanceDocker()
	defer docker.client.CloseIdleConnections()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	var initial *acceptanceObservation
	var proc *os.Root
	workerFD := -1
	var stop func() (t451b.Observations, error)
	defer func() {
		if stop != nil {
			facts, e := stop()
			witness.Observations = facts
			err = errors.Join(err, e)
		}
		if err != nil {
			cancel()
		}
		if proc != nil {
			err = errors.Join(err, proc.Close())
		}
		if workerFD >= 0 {
			err = errors.Join(err, unix.Close(workerFD))
		}
		if err != nil {
			witness.verified = false
			if stage == "" {
				stage = "cleanup"
			}
		}
	}()
	resumed, complete := false, false
	for {
		select {
		case result := <-done:
			joined, native = true, result.result
			if result.err != nil {
				return native, witness, "native", result.err
			}
			if !complete {
				return native, witness, "barrier", errors.New("native returned before complete host interval")
			}
			if e = typedsandbox.VerifyCompletion(options.Allowance, options.Control, native); e != nil || native.ContainerID != witness.ContainerID {
				return native, witness, "completion", errors.New("host witness native completion differs")
			}
			witness.StdoutSHA256, witness.StderrSHA256 = acceptanceDigest(native.Stdout), acceptanceDigest(native.Stderr)
			witness.verified = true
			return native, witness, "", nil
		case <-ctx.Done():
			return native, witness, "context", ctx.Err()
		case <-ticker.C:
			if e = options.Allowance.CheckLive(ctx); e != nil {
				return native, witness, "allowance", e
			}
			if complete {
				continue
			}
			if initial == nil {
				observed, unpin, e := docker.observe(ctx, c, s, selected, map[string]bool{})
				if e != nil {
					return native, witness, "authentication", e
				}
				if observed == nil {
					continue
				}
				e = func() error {
					defer unpin()
					if observed.Phase != options.Control.Phase || observed.Seal != options.Control.SealDigest || observed.Allowance != options.Allowance || observed.Scratch.Authority != authority {
						return errors.New("host cost native custody differs")
					}
					workerStart, state, e := t451a.ReadProcessState(os.DirFS("/proc"), uint32(observed.WorkerPID))
					if e != nil || strconv.FormatUint(workerStart, 10) != observed.WorkerStart || state != 'T' {
						return errors.New("initial worker barrier absent")
					}
					workerFD, e = unix.PidfdOpen(observed.WorkerPID, 0)
					if e != nil {
						return e
					}
					proc, witness.PrivateWorker, e = acceptanceOpenPrivateProc(observed, witness)
					if e != nil {
						return e
					}
					stop, e = t451b.StartRootManagedObservations(ctx, proc.FS(), witness.PrivateWorker)
					if e != nil {
						return e
					}
					if e = acceptanceSignalPinned(workerFD, *observed, unix.SIGCONT); e != nil {
						return e
					}
					initial = observed
					return nil
				}()
				if e != nil {
					return native, witness, "start", e
				}
				continue
			}
			start, state, e := t451a.ReadProcessState(proc.FS(), witness.PrivateWorker)
			if e != nil || strconv.FormatUint(start, 10) != witness.WorkerStart {
				return native, witness, "worker_identity", errors.New("measured worker lifetime changed")
			}
			end, stateErr := acceptanceMeasuredBarrier(state, resumed)
			if stateErr != nil {
				return native, witness, "worker_state", stateErr
			}
			if !end {
				resumed = true
				continue
			}
			observed, unpin, e := docker.observeFinal(ctx, c, s, selected)
			if e != nil {
				return native, witness, "finish_authentication_" + acceptanceObservationSite(e), e
			}
			if observed == nil {
				return native, witness, "finish_authentication_observe_unavailable", errors.New("final worker custody absent")
			}
			e = func() error {
				defer unpin()
				if !reflect.DeepEqual(initial, observed) {
					return errors.New("final worker custody differs")
				}
				var ns unix.Stat_t
				_, supervisorStart, e := acceptanceProc(observed.SupervisorPID)
				privateStart, _, privateErr := t451a.ReadProcessState(proc.FS(), 1)
				if e != nil || privateErr != nil || supervisorStart != witness.SupervisorStart || strconv.FormatUint(privateStart, 10) != witness.SupervisorStart || unix.Stat(fmt.Sprintf("/proc/%d/ns/pid", observed.WorkerPID), &ns) != nil || uint64(ns.Dev) != witness.NamespaceDevice || ns.Ino != witness.NamespaceInode {
					return errors.New("final private namespace differs")
				}
				facts, e := stop()
				witness.Observations = facts
				if e != nil {
					return e
				}
				if e = t451b.ValidateManagedObservations(facts); e != nil {
					return e
				}
				if e = acceptanceSignalPinned(workerFD, *observed, unix.SIGCONT); e != nil {
					return e
				}
				complete = true
				return nil
			}()
			if e != nil {
				return native, witness, "finish", e
			}
		}
	}
}

func acceptanceSignalPinned(fd int, observed acceptanceObservation, signal unix.Signal) error {
	parent, start, err := acceptanceProc(observed.WorkerPID)
	if err != nil || parent != observed.SupervisorPID || start != observed.WorkerStart {
		return errors.New("pinned signal target changed")
	}
	return unix.PidfdSendSignal(fd, signal, nil, 0)
}

func acceptanceOpenPrivateProc(observed *acceptanceObservation, witness *acceptanceCorpusHostWitness) (*os.Root, uint32, error) {
	raw, err := acceptanceSystemRead(fmt.Sprintf("/proc/%d/status", observed.WorkerPID), 8192)
	if err != nil {
		return nil, 0, err
	}
	var private uint32
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[0] == "NSpid:" && fields[1] == strconv.Itoa(observed.WorkerPID) && private == 0 {
			n, e := strconv.ParseUint(fields[2], 10, 32)
			if e == nil && n > 1 {
				private = uint32(n)
			}
		}
	}
	if private == 0 {
		return nil, 0, errors.New("private worker PID unproved")
	}
	var ns unix.Stat_t
	if err = unix.Stat(fmt.Sprintf("/proc/%d/ns/pid", observed.WorkerPID), &ns); err != nil {
		return nil, 0, err
	}
	root, err := os.OpenRoot(fmt.Sprintf("/proc/%d/root/proc", observed.SupervisorPID))
	if err != nil {
		return nil, 0, err
	}
	fail := func(e error) (*os.Root, uint32, error) { return nil, 0, errors.Join(e, root.Close()) }
	dir, err := root.Open(".")
	if err != nil {
		return fail(err)
	}
	var st unix.Stat_t
	var kind unix.Statfs_t
	err = errors.Join(unix.Fstat(int(dir.Fd()), &st), unix.Fstatfs(int(dir.Fd()), &kind), dir.Close())
	if err != nil || kind.Type != unix.PROC_SUPER_MAGIC {
		return fail(errors.New("anchored proc filesystem differs"))
	}
	workerStart, workerState, err := t451a.ReadProcessState(root.FS(), private)
	_, supervisorStart, supervisorErr := acceptanceProc(observed.SupervisorPID)
	privateSupervisorStart, _, privateErr := t451a.ReadProcessState(root.FS(), 1)
	if err != nil || supervisorErr != nil || privateErr != nil || strconv.FormatUint(workerStart, 10) != observed.WorkerStart || workerState != 'T' || strconv.FormatUint(privateSupervisorStart, 10) != supervisorStart {
		return fail(errors.New("private proc lifetime binding differs"))
	}
	witness.ContainerID, witness.WorkerStart = observed.ID, observed.WorkerStart
	witness.SupervisorStart = supervisorStart
	witness.NamespaceDevice, witness.NamespaceInode = uint64(ns.Dev), ns.Ino
	witness.ProcDevice, witness.ProcInode = uint64(st.Dev), st.Ino
	return root, private, nil
}

func acceptanceCorpusHostCost(ctx context.Context, options typedsandbox.Options, result typedsandbox.Result, witness *acceptanceCorpusHostWitness) (acceptanceCorpusPhaseCost, error) {
	var cost acceptanceCorpusPhaseCost
	if witness == nil || !witness.verified || witness.Control != options.Control || witness.Allowance != options.Allowance || witness.ContainerID != result.ContainerID || typedsandbox.VerifyCompletion(options.Allowance, options.Control, result) != nil {
		return cost, errors.New("unverified host cost completion")
	}
	phase, err := acceptanceCorpusResultPhase(ctx, options, result)
	if err != nil {
		return cost, err
	}
	cache, err := t451b.DecodeManagedCache(result.Stderr)
	if err != nil {
		return cost, err
	}
	cost = acceptanceCorpusPhaseCost{Phase: phase, PlanningDigest: options.Control.PlanningDigest, AttemptDigest: options.Control.AttemptDigest, RequestDigest: options.Control.RequestDigest, SealDigest: options.Control.SealDigest, StdoutSHA256: acceptanceDigest(result.Stdout), StderrSHA256: acceptanceDigest(result.Stderr), WorkerOutputBytes: int64(len(result.Stdout) + len(result.Stderr)), HostWitness: witness, HostWitnessSHA256: acceptanceDigest(acceptanceJSON(witness)), WorkerCache: &cache}
	return cost, acceptanceValidateHostCost(cost)
}

func acceptanceValidateHostCost(cost acceptanceCorpusPhaseCost) error {
	w := cost.HostWitness
	if w == nil || w.Schema != acceptanceHostCostSchema || w.Control.Validate() != nil || w.Allowance.Validate() != nil || w.Control.PlanningDigest != cost.PlanningDigest || w.Control.AttemptDigest != cost.AttemptDigest || w.Control.RequestDigest != cost.RequestDigest || w.Control.Phase != string(cost.Phase) || w.Control.SealDigest != cost.SealDigest || w.Allowance.PlanningDigest != cost.PlanningDigest || w.Allowance.AttemptDigest != cost.AttemptDigest || !acceptanceHash("sha256:"+w.ContainerID) || w.PrivateWorker < 2 || w.NamespaceDevice == 0 || w.NamespaceInode == 0 || w.ProcDevice == 0 || w.ProcInode == 0 || w.StdoutSHA256 != cost.StdoutSHA256 || w.StderrSHA256 != cost.StderrSHA256 || acceptanceDigest(acceptanceJSON(w)) != cost.HostWitnessSHA256 {
		return errors.New("invalid separately bound host witness")
	}
	start, err := strconv.ParseUint(w.WorkerStart, 10, 64)
	if err != nil || start == 0 || strconv.FormatUint(start, 10) != w.WorkerStart {
		return errors.New("invalid host worker lifetime")
	}
	supervisor, err := strconv.ParseUint(w.SupervisorStart, 10, 64)
	if err != nil || supervisor == 0 || strconv.FormatUint(supervisor, 10) != w.SupervisorStart {
		return errors.New("invalid host supervisor lifetime")
	}
	return t451b.ValidateManagedObservations(w.Observations)
}

func acceptanceMeasuredBarrier(state byte, resumed bool) (bool, error) {
	if state == 'T' {
		if !resumed {
			return false, errors.New("resumed worker interval unproved")
		}
		return true, nil
	}
	if !strings.ContainsRune("RSDWKPI", rune(state)) {
		return false, errors.New("measured worker state refused")
	}
	return false, nil
}

func TestNativeAcceptanceMeasuredBarrier(t *testing.T) {
	for _, tc := range []struct {
		state            byte
		resumed, end, ok bool
	}{
		{'T', false, false, false}, {'T', true, true, true},
		{'R', false, false, true}, {'S', true, false, true},
		{'t', true, false, false}, {'Z', true, false, false}, {'X', true, false, false}, {'x', true, false, false}, {'?', true, false, false},
	} {
		end, err := acceptanceMeasuredBarrier(tc.state, tc.resumed)
		if end != tc.end || (err == nil) != tc.ok {
			t.Fatalf("barrier %q resumed=%v: end=%v err=%v", tc.state, tc.resumed, end, err)
		}
	}
}

func TestNativeAcceptanceHostCost(t *testing.T) {
	fixture := func() []acceptanceCorpusPhaseCost {
		cache := t451b.ManagedCache{Schema: t451b.ManagedCacheSchema, Cache: t451b.PrivateCacheObservation{Version: "phebs-t451b-private-cache-v1", Roots: []string{"/scratch/bazel-user", "/scratch/bazel-output", "/scratch/repository-cache", "/scratch/gocache", "/scratch/gomodcache", "/scratch/cache"}, MissingRoots: []string{}, Entries: 6, Directories: 6, UniqueInodes: 6, Complete: true}}
		raw, err := t451b.EncodeManagedCache(cache)
		if err != nil {
			t.Fatal(err)
		}
		planning, attempt := acceptanceDigest([]byte("planning")), acceptanceDigest([]byte("attempt"))
		var costs []acceptanceCorpusPhaseCost
		for i, phase := range []typedindex.Action{typedindex.Plan, typedindex.Execute} {
			request := planning
			if i == 1 {
				request = acceptanceDigest([]byte("execute"))
			}
			control := typedsandbox.ControlIdentity{PlanningDigest: planning, AttemptDigest: attempt, RequestDigest: request, Phase: string(phase), SealDigest: acceptanceDigest([]byte(phase)), Device: 1, Inode: 2}
			allowance := typedsandbox.Allowance{Schema: "phebs-typed-allowance-v1", PlanningDigest: planning, AttemptDigest: attempt, BootID: "12345678-1234-1234-1234-123456789abc", TimeDevice: 1, TimeInode: 2, Start: 1, Deadline: 1 + int64(typedsandbox.WallLimit)}
			if i == 1 {
				allowance.WorkerBytesUsed, allowance.WireBytesUsed = 4096, 8192
			}
			w := &acceptanceCorpusHostWitness{Schema: acceptanceHostCostSchema, Control: control, Allowance: allowance, ContainerID: strings.Repeat("a", 64), WorkerStart: "2", SupervisorStart: "1", PrivateWorker: 2, NamespaceDevice: 1, NamespaceInode: 2, ProcDevice: 3, ProcInode: 4, StdoutSHA256: acceptanceDigest([]byte("result")), StderrSHA256: acceptanceDigest(raw), Observations: t451b.Observations{Version: "phebs-t451b-sampled-observations-v1", IntervalNanoseconds: 50000000, DurationNanoseconds: 1000000, Samples: 2, SampledChildLifetimes: 1, ChildLifetimesLowerBound: true, SampledProcessFDPeak: 2, SampledAggregateFDPeak: 2, FDCountsNonAtomic: true}}
			costs = append(costs, acceptanceCorpusPhaseCost{Phase: phase, PlanningDigest: planning, AttemptDigest: attempt, RequestDigest: request, SealDigest: control.SealDigest, StdoutSHA256: w.StdoutSHA256, StderrSHA256: w.StderrSHA256, WorkerOutputBytes: 4096, HostWitness: w, HostWitnessSHA256: acceptanceDigest(acceptanceJSON(w)), WorkerCache: &cache})
		}
		return costs
	}
	for _, tc := range []struct {
		name   string
		mutate func([]acceptanceCorpusPhaseCost)
		valid  bool
	}{
		{"bound-two-phases", func([]acceptanceCorpusPhaseCost) {}, true},
		{"changed-witness", func(c []acceptanceCorpusPhaseCost) { c[1].HostWitness.ProcInode++ }, false},
		{"foreign-control", func(c []acceptanceCorpusPhaseCost) {
			c[1].HostWitness.Control.RequestDigest = c[1].PlanningDigest
			c[1].HostWitnessSHA256 = acceptanceDigest(acceptanceJSON(c[1].HostWitness))
		}, false},
		{"shared-output", func(c []acceptanceCorpusPhaseCost) {
			c[1].HostWitness.Allowance.WorkerBytesUsed++
			c[1].HostWitnessSHA256 = acceptanceDigest(acceptanceJSON(c[1].HostWitness))
		}, false},
		{"sticky-unavailable", func(c []acceptanceCorpusPhaseCost) {
			c[1].HostWitness.Observations.Unavailable = true
			c[1].HostWitnessSHA256 = acceptanceDigest(acceptanceJSON(c[1].HostWitness))
		}, false},
		{"cache-stream-change", func(c []acceptanceCorpusPhaseCost) { c[1].WorkerCache.Cache.AllocatedBytes = 512 }, false},
		{"cache-is-not-v1-process-witness", func(c []acceptanceCorpusPhaseCost) { c[1].Metrics = &t451b.ManagedCost{} }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			costs := fixture()
			tc.mutate(costs)
			r := &acceptanceCorpusResult{CostGate: "unavailable", CostMissing: []string{"sampled_child_lifetimes", "sampled_fd_counts", "private_cache_inventory"}}
			if err := acceptanceCorpusSetCost(r, costs); (err == nil) != tc.valid {
				t.Fatal("separate host/worker binding", err)
			}
		})
	}
	// Healthy-looking host facts and a cache frame cannot mint the native token,
	// even when the caller tries to set the unexported local witness flag.
	cost := fixture()[0]
	cost.HostWitness.verified = true
	raw, err := t451b.EncodeManagedCache(*cost.WorkerCache)
	if err != nil {
		t.Fatal(err)
	}
	fake := typedsandbox.Result{ExitCode: 0, Removed: true, ContainerID: cost.HostWitness.ContainerID, Stdout: []byte(`{"schema":"phebs-bazel-worker-result-v1","phase":"plan","request_digest":"` + cost.RequestDigest + `"}`), Stderr: raw}
	if _, err := acceptanceCorpusHostCost(t.Context(), typedsandbox.Options{Control: cost.HostWitness.Control, Allowance: cost.HostWitness.Allowance}, fake, cost.HostWitness); err == nil {
		t.Fatal("copied root witness bypassed native completion")
	}
	cost.HostWitness.Observations.Unavailable = true
	cost.HostWitness.Observations.UnexpectedErrors = 1
	cost.HostWitness.Observations.Failure = "process"
	cost.HostWitness.Observations.FailureProcess = &t451a.ProcessDiagnostic{Comm: "private/source", State: "R", CapPrm: "0000000000000000"}
	stopped := acceptanceHostCostStop("finish", cost.HostWitness)
	if !stopped.DiagnosticOnly || !stopped.Witness.Observations.Unavailable || stopped.Witness.Observations.FailureProcess != nil || cost.HostWitness.Observations.FailureProcess == nil {
		t.Fatal("unsafe diagnostic leaked or repaired original sticky refusal")
	}
}
