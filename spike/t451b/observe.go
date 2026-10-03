package t451b

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bmeddeb/phebs/spike/t451a"
	"github.com/bmeddeb/phebs/spike/t451a/sandbox"
)

const observationInterval = 50 * time.Millisecond
const maxObservedLifetimes = 65536

// Observations are sampled within the private PID namespace. Child lifetimes
// exclude the worker, PID 1 and other effective UIDs. FD counts include the
// worker (and the observer's temporary descriptors), exclude PID 1 and other
// UIDs, and are sequential, non-atomic directory-entry observations. In
// particular their aggregate is not an exact simultaneous peak or a guaranteed
// lower bound on that peak. Unavailable is sticky; no field proves completeness.
// Validated zombies contribute zero descriptors but retain lifetime accounting.
type Observations struct {
	Version                  string                   `json:"version"`
	IntervalNanoseconds      int64                    `json:"interval_nanoseconds"`
	DurationNanoseconds      int64                    `json:"duration_nanoseconds"`
	Samples                  uint64                   `json:"samples"`
	SampledChildLifetimes    uint64                   `json:"sampled_child_lifetimes"`
	ChildLifetimesLowerBound bool                     `json:"child_lifetimes_lower_bound"`
	SampledProcessFDPeak     uint64                   `json:"sampled_process_fd_peak"`
	SampledAggregateFDPeak   uint64                   `json:"sampled_aggregate_fd_peak"`
	FDCountsNonAtomic        bool                     `json:"fd_counts_non_atomic"`
	Vanished                 uint64                   `json:"vanished"`
	Raced                    uint64                   `json:"raced"`
	UnexpectedErrors         uint64                   `json:"unexpected_errors"`
	Unavailable              bool                     `json:"unavailable"`
	Failure                  string                   `json:"failure,omitempty"`
	FailureProcess           *t451a.ProcessDiagnostic `json:"failure_process,omitempty"`
}

type observedLifetime struct {
	pid   uint32
	start uint64
}

type processObserver struct {
	proc  fs.FS
	pid   uint32
	uid   uint32
	seen  map[observedLifetime]struct{}
	facts Observations
	err   error
	tasks int
}

func newProcessObserver(proc fs.FS, pid, uid uint32) *processObserver {
	return newProcessObserverBounded(proc, pid, uid, sandbox.TaskLimit)
}

func newProcessObserverBounded(proc fs.FS, pid, uid uint32, tasks int) *processObserver {
	return &processObserver{proc: proc, pid: pid, uid: uid, tasks: tasks, seen: make(map[observedLifetime]struct{}), facts: Observations{
		Version: "phebs-t451b-sampled-observations-v1", IntervalNanoseconds: observationInterval.Nanoseconds(),
		ChildLifetimesLowerBound: true, FDCountsNonAtomic: true,
	}}
}

func (o *processObserver) fail(stage string, err error) {
	o.facts.Unavailable = true
	o.facts.UnexpectedErrors++
	if o.err == nil {
		o.facts.Failure = stage
		o.err = fmt.Errorf("process observation %s: %w", stage, err)
	}
}

func vanished(err error) bool {
	return t451a.ProcessGone(err)
}

func permissionDenied(err error) bool {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, part := range joined.Unwrap() {
			if !permissionDenied(part) {
				return false
			}
		}
		return len(joined.Unwrap()) > 0
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return permissionDenied(wrapped.Unwrap())
	}
	return errors.Is(err, fs.ErrPermission)
}

func observationRead(proc fs.FS, name string) ([]byte, error) {
	f, err := proc.Open(name)
	if err != nil {
		return nil, err
	}
	b, readErr := io.ReadAll(io.LimitReader(f, 8193))
	closeErr := f.Close()
	if err = errors.Join(readErr, closeErr); err != nil {
		return nil, err
	}
	if len(b) > 8192 {
		return nil, errors.New("proc record byte bound")
	}
	return b, nil
}

func observationDirectory(root fs.FS, name string, limit int) ([]fs.DirEntry, error) {
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	dir, ok := f.(fs.ReadDirFile)
	if !ok {
		return nil, errors.Join(errors.New("directory reader unavailable"), f.Close())
	}
	entries, readErr := dir.ReadDir(limit + 1)
	if readErr == io.EOF {
		readErr = nil
	}
	if closeErr := f.Close(); closeErr != nil {
		// Close failures are independent of a denied read and cannot recover
		// through a later zombie/disappearance observation.
		return entries, errors.Join(readErr, closeErr, errors.New("directory close failure"))
	}
	if readErr != nil {
		return entries, readErr
	}
	if len(entries) > limit {
		return nil, errors.New("directory entry bound")
	}
	return entries, nil
}

func observationUID(raw []byte) (uint32, error) {
	var uid uint32
	count := 0
	for line := range strings.SplitSeq(string(raw), "\n") {
		if !strings.HasPrefix(line, "Uid:") {
			continue
		}
		count++
		fields := strings.Fields(line)
		if len(fields) != 5 {
			return 0, errors.New("invalid proc UID shape")
		}
		for i, field := range fields[1:] {
			n, err := strconv.ParseUint(field, 10, 32)
			if err != nil {
				return 0, errors.New("invalid proc UID")
			}
			if i == 1 {
				uid = uint32(n)
			}
		}
	}
	if count != 1 {
		return 0, errors.New("missing or repeated proc UID")
	}
	return uid, nil
}

func (o *processObserver) process(pid uint32) (fdsCount, start uint64, err error) {
	base := strconv.FormatUint(uint64(pid), 10)
	start, state, err := t451a.ReadProcessState(o.proc, pid)
	if err != nil {
		return 0, start, err
	}
	status, err := observationRead(o.proc, base+"/status")
	if err != nil {
		return 0, start, err
	}
	uid, err := observationUID(status)
	if err != nil {
		return 0, start, err
	}
	if uid != o.uid {
		if pid == o.pid {
			return 0, start, errors.New("worker UID changed")
		}
		return 0, start, nil
	}
	var fds []fs.DirEntry
	var fdErr error
	if state != 'Z' {
		fds, fdErr = observationDirectory(o.proc, base+"/fd", sandbox.DescriptorLimit)
		if fdErr != nil && (len(fds) != 0 || !permissionDenied(fdErr)) {
			return 0, start, fdErr
		}
	}
	// A permission denial without partial entries gets one bounded lifetime/state
	// recheck, never an FD retry. Partial data must not hide a bound/shape failure.
	// Only the same zombie or a genuinely vanished process can recover a denial.
	for _, fd := range fds {
		if n, err := strconv.ParseUint(fd.Name(), 10, 32); err != nil || strconv.FormatUint(n, 10) != fd.Name() {
			return 0, start, errors.New("invalid proc descriptor")
		}
	}
	end, endState, err := t451a.ReadProcessState(o.proc, pid)
	if err != nil {
		if fdErr != nil && !vanished(err) {
			return 0, start, errors.Join(fdErr, err)
		}
		return 0, start, err
	}
	if start != end {
		o.facts.Raced++
		return 0, start, errors.Join(fdErr, errors.New("proc lifetime changed during descriptor observation"))
	}
	if fdErr != nil && endState != 'Z' {
		return 0, start, fdErr
	}
	if state == 'Z' && endState != 'Z' {
		return 0, start, errors.New("proc zombie state changed during observation")
	}
	if endState == 'Z' {
		fds = nil
	}
	if pid != o.pid {
		key := observedLifetime{pid, start}
		if _, ok := o.seen[key]; !ok {
			if len(o.seen) == maxObservedLifetimes {
				return 0, start, errors.New("observed lifetime bound")
			}
			o.seen[key] = struct{}{}
			o.facts.SampledChildLifetimes++
		}
	}
	return uint64(len(fds)), start, nil
}

func (o *processObserver) sample() {
	o.facts.Samples++
	// /proc also contains non-process kernel entries. Bound those independently
	// through an independently bounded inventory, then enforce the selected
	// legacy or closed managed process ceiling.
	entries, err := observationDirectory(o.proc, ".", 2*o.tasks)
	if err != nil {
		o.fail("inventory", err)
		return
	}
	var total uint64
	count := 0
	worker := false
	for _, entry := range entries {
		n, err := strconv.ParseUint(entry.Name(), 10, 32)
		if err != nil {
			continue
		}
		if n == 0 || strconv.FormatUint(n, 10) != entry.Name() {
			o.fail("inventory", errors.New("invalid proc PID"))
			return
		}
		count++
		if count > o.tasks {
			o.fail("inventory", errors.New("process inventory bound"))
			return
		}
		if n == 1 {
			continue
		}
		pid := uint32(n)
		worker = worker || pid == o.pid
		fds, start, err := o.process(pid)
		if vanished(err) && pid != o.pid {
			o.facts.Vanished++
			continue
		}
		if err != nil {
			// Capture once for the sticky offending lifetime, never retry the
			// observer or replace its refusal if these diagnostic reads fail.
			if o.err == nil && start != 0 {
				o.facts.FailureProcess = t451a.ReadProcessDiagnostic(o.proc, pid, start)
			}
			o.fail("process", err)
			continue
		}
		o.facts.SampledProcessFDPeak = max(o.facts.SampledProcessFDPeak, fds)
		total += fds
	}
	if !worker {
		o.fail("worker", errors.New("worker absent from proc inventory"))
	}
	o.facts.SampledAggregateFDPeak = max(o.facts.SampledAggregateFDPeak, total)
}

func startObservations(ctx context.Context, proc fs.FS, pid, uid uint32) (func() (Observations, error), error) {
	return startObservationsBounded(ctx, proc, pid, uid, sandbox.TaskLimit)
}

func startObservationsBounded(ctx context.Context, proc fs.FS, pid, uid uint32, tasks int) (func() (Observations, error), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	o := newProcessObserverBounded(proc, pid, uid, tasks)
	started := time.Now()
	o.sample()
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(observationInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				o.sample()
			case <-stop:
				if err := ctx.Err(); err != nil {
					o.fail("context", err)
				} else {
					o.sample()
				}
				o.facts.DurationNanoseconds = time.Since(started).Nanoseconds()
				return
			case <-ctx.Done():
				o.fail("context", ctx.Err())
				o.facts.DurationNanoseconds = time.Since(started).Nanoseconds()
				return
			}
		}
	}()
	var once sync.Once
	return func() (Observations, error) {
		once.Do(func() { close(stop) })
		<-done
		return o.facts, o.err
	}, nil
}

// PrivateCacheObservation requires a quiescent worker. Entries include each
// declared root and its descendants; missing roots are reported separately.
// Logical bytes count regular files once per inode. Allocated bytes count
// st_blocks*512 once per inode, including directory and symlink metadata.
// Symlink targets, undeclared scratch paths, /inputs and /dev/shm are excluded.
type PrivateCacheObservation struct {
	Version        string   `json:"version"`
	Roots          []string `json:"roots"`
	MissingRoots   []string `json:"missing_roots"`
	Entries        uint64   `json:"entries"`
	RegularFiles   uint64   `json:"regular_files"`
	Directories    uint64   `json:"directories"`
	Symlinks       uint64   `json:"symlinks"`
	UniqueInodes   uint64   `json:"unique_inodes"`
	LogicalBytes   uint64   `json:"logical_bytes"`
	AllocatedBytes uint64   `json:"allocated_bytes"`
	Complete       bool     `json:"complete"`
}

type cacheInode struct{ device, inode uint64 }

// ObservePrivateCache inventories only the six fixed private cache directories.
// The caller must stop all cache writers before calling it.
func ObservePrivateCache() (PrivateCacheObservation, error) {
	return observePrivateCache("/scratch")
}

func observePrivateCache(scratch string) (PrivateCacheObservation, error) {
	return observePrivateCacheBounded(scratch, sandbox.ScratchBytes)
}

func observePrivateCacheBounded(scratch string, bytes uint64) (PrivateCacheObservation, error) {
	result := PrivateCacheObservation{Version: "phebs-t451b-private-cache-v1", MissingRoots: []string{}}
	seen := make(map[cacheInode]struct{})
	for _, name := range []string{"bazel-user", "bazel-output", "repository-cache", "gocache", "gomodcache", "cache"} {
		result.Roots = append(result.Roots, "/scratch/"+name)
		root := path.Join(scratch, name)
		info, err := os.Lstat(root)
		if errors.Is(err, fs.ErrNotExist) {
			result.MissingRoots = append(result.MissingRoots, "/scratch/"+name)
			continue
		}
		if err != nil {
			return result, err
		}
		if !info.IsDir() {
			return result, errors.New("private cache root is not a directory")
		}
		err = walkCacheBounded(root, &result, seen, bytes)
		if err != nil {
			return result, err
		}
	}
	result.Complete = true
	return result, nil
}

func walkCache(root string, result *PrivateCacheObservation, seen map[cacheInode]struct{}) error {
	return walkCacheBounded(root, result, seen, sandbox.ScratchBytes)
}

func walkCacheBounded(root string, result *PrivateCacheObservation, seen map[cacheInode]struct{}, bytes uint64) error {
	// Read directories in bounded batches instead of WalkDir's unbounded sort.
	queue := []string{root}
	for len(queue) > 0 {
		name := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		info, err := os.Lstat(name)
		if err != nil {
			return err
		}
		if result.Entries == sandbox.NativeT451bScratchInodes {
			return errors.New("private cache entry bound")
		}
		result.Entries++
		switch {
		case info.Mode().IsRegular():
			result.RegularFiles++
		case info.IsDir():
			result.Directories++
		case info.Mode()&fs.ModeSymlink != 0:
			result.Symlinks++
		default:
			return errors.New("unsupported private cache inode")
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Ino == 0 || stat.Blocks < 0 || info.Size() < 0 || uint64(stat.Blocks) > bytes/512 {
			return errors.New("private cache allocation unavailable or over bound")
		}
		key := cacheInode{uint64(stat.Dev), stat.Ino}
		if _, exists := seen[key]; !exists {
			seen[key] = struct{}{}
			result.UniqueInodes++
			allocated := uint64(stat.Blocks) * 512
			logical := uint64(0)
			if info.Mode().IsRegular() {
				logical = uint64(info.Size())
			}
			if allocated > bytes-result.AllocatedBytes || logical > bytes-result.LogicalBytes {
				return errors.New("private cache byte bound")
			}
			result.AllocatedBytes += allocated
			result.LogicalBytes += logical
		}
		if !info.IsDir() {
			continue
		}
		f, err := os.OpenFile(name, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		if err != nil {
			return err
		}
		entries, readErr := f.ReadDir(int(sandbox.NativeT451bScratchInodes-result.Entries) + 1)
		if readErr == io.EOF {
			readErr = nil
		}
		if err = errors.Join(readErr, f.Close()); err != nil {
			return err
		}
		if len(entries)+len(queue) > int(sandbox.NativeT451bScratchInodes-result.Entries) {
			return errors.New("private cache pending entry bound")
		}
		for _, entry := range entries {
			queue = append(queue, path.Join(name, entry.Name()))
		}
	}
	return nil
}
