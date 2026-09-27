package t451b

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"testing/fstest"

	"github.com/bmeddeb/phebs/spike/t451a/sandbox"
)

func procStat(pid int, start uint64) []byte {
	return fmt.Appendf(nil, "%d (name ) with spaces) S%s %d 0 0\n", pid, strings.Repeat(" 0", 18), start)
}

func addProc(m fstest.MapFS, pid int, start uint64, uid, fds int) {
	base := strconv.Itoa(pid)
	m[base+"/stat"] = &fstest.MapFile{Data: procStat(pid, start)}
	m[base+"/status"] = &fstest.MapFile{Data: fmt.Appendf(nil, "Name:\ttest\nUid:\t%d\t%d\t%d\t%d\n", uid, uid, uid, uid)}
	m[base+"/fd"] = &fstest.MapFile{Mode: fs.ModeDir}
	for fd := range fds {
		m[fmt.Sprintf("%s/fd/%d", base, fd)] = &fstest.MapFile{Mode: fs.ModeSymlink}
	}
}

func observationProc() fstest.MapFS {
	m := fstest.MapFS{"1/stat": {Data: []byte("must not read PID 1")}}
	addProc(m, 2, 100, 65534, 2)
	addProc(m, 3, 101, 65534, 3)
	addProc(m, 4, 102, 0, 200)
	return m
}

type procOpenFS struct {
	fs.FS
	open func(string) (fs.File, error)
}

func (p procOpenFS) Open(name string) (fs.File, error) { return p.open(name) }

type procErrorDirectory struct {
	fs.File
	entries           []fs.DirEntry
	readErr, closeErr error
	limit             *int
}

func (d procErrorDirectory) ReadDir(limit int) ([]fs.DirEntry, error) {
	*d.limit = limit
	return d.entries[:min(limit, len(d.entries))], d.readErr
}

func (d procErrorDirectory) Close() error { return errors.Join(d.File.Close(), d.closeErr) }

func TestObservations(t *testing.T) {
	t.Run("sample and PID reuse", func(t *testing.T) {
		m := observationProc()
		o := newProcessObserver(m, 2, 65534)
		o.sample()
		o.sample()
		if o.err != nil || o.facts.SampledChildLifetimes != 1 || o.facts.SampledProcessFDPeak != 3 || o.facts.SampledAggregateFDPeak != 5 || o.facts.Unavailable {
			t.Fatalf("facts=%+v err=%v", o.facts, o.err)
		}
		m["3/stat"].Data = procStat(3, 200)
		o.sample()
		if o.facts.SampledChildLifetimes != 2 || !o.facts.ChildLifetimesLowerBound || !o.facts.FDCountsNonAtomic || o.facts.Samples != 3 {
			t.Fatalf("PID reuse collapsed: %+v", o.facts)
		}
	})
	t.Run("exact process and descriptor ceilings", func(t *testing.T) {
		m := observationProc()
		addProc(m, 3, 101, 65534, sandbox.DescriptorLimit)
		for pid := 5; pid <= sandbox.TaskLimit; pid++ {
			addProc(m, pid, uint64(pid), 0, 0)
		}
		o := newProcessObserver(m, 2, 65534)
		o.sample()
		if o.err != nil || o.facts.SampledProcessFDPeak != sandbox.DescriptorLimit || o.facts.SampledAggregateFDPeak != sandbox.DescriptorLimit+2 {
			t.Fatalf("facts=%+v err=%v", o.facts, o.err)
		}
	})
	for _, tc := range []struct {
		name        string
		mutate      func(fstest.MapFS, *processObserver)
		open        func(string, fstest.MapFS, *int) (fs.File, error)
		vanished    uint64
		raced       uint64
		unavailable bool
	}{
		{name: "vanished", open: func(name string, m fstest.MapFS, _ *int) (fs.File, error) {
			if name == "3/fd" {
				return nil, fs.ErrNotExist
			}
			return m.Open(name)
		}, vanished: 1},
		{name: "permission sticky", open: func(name string, m fstest.MapFS, _ *int) (fs.File, error) {
			if name == "3/fd" {
				return nil, fs.ErrPermission
			}
			return m.Open(name)
		}, unavailable: true},
		{name: "mixed vanished and unexpected errors", open: func(name string, m fstest.MapFS, _ *int) (fs.File, error) {
			if name == "3/fd" {
				return nil, errors.Join(fs.ErrNotExist, fs.ErrPermission)
			}
			return m.Open(name)
		}, unavailable: true},
		{name: "PID changes during FD read", open: func(name string, m fstest.MapFS, reads *int) (fs.File, error) {
			if name == "3/stat" {
				*reads++
				if *reads == 2 {
					return fstest.MapFS{"stat": {Data: procStat(3, 999)}}.Open("stat")
				}
			}
			return m.Open(name)
		}, raced: 1, unavailable: true},
		{name: "malformed stat", mutate: func(m fstest.MapFS, _ *processObserver) { m["3/stat"].Data = []byte("broken") }, unavailable: true},
		{name: "malformed UID", mutate: func(m fstest.MapFS, _ *processObserver) { m["3/status"].Data = []byte("Uid: 1 2 3\n") }, unavailable: true},
		{name: "worker UID changed", mutate: func(m fstest.MapFS, _ *processObserver) { addProc(m, 2, 100, 0, 2) }, unavailable: true},
		{name: "zombie UID malformed", mutate: func(m fstest.MapFS, _ *processObserver) {
			m["3/stat"].Data = []byte(strings.Replace(string(procStat(3, 101)), ") S ", ") Z ", 1))
			m["3/status"].Data = []byte("Uid: broken\n")
		}, unavailable: true},
		{name: "zombie worker UID changed", mutate: func(m fstest.MapFS, _ *processObserver) {
			addProc(m, 2, 100, 0, 2)
			m["2/stat"].Data = []byte(strings.Replace(string(procStat(2, 100)), ") S ", ") Z ", 1))
		}, unavailable: true},
		{name: "oversized proc record", mutate: func(m fstest.MapFS, _ *processObserver) { m["3/status"].Data = []byte(strings.Repeat("x", 8193)) }, unavailable: true},
		{name: "FD overflow", mutate: func(m fstest.MapFS, _ *processObserver) { addProc(m, 3, 101, 65534, sandbox.DescriptorLimit+1) }, unavailable: true},
		{name: "process overflow", mutate: func(m fstest.MapFS, _ *processObserver) {
			for pid := 5; pid <= sandbox.TaskLimit+1; pid++ {
				addProc(m, pid, uint64(pid), 0, 0)
			}
		}, unavailable: true},
		{name: "zombie process overflow", mutate: func(m fstest.MapFS, _ *processObserver) {
			for pid := 5; pid <= sandbox.TaskLimit+1; pid++ {
				addProc(m, pid, uint64(pid), 65534, 0)
				m[fmt.Sprintf("%d/stat", pid)].Data = []byte(strings.Replace(string(procStat(pid, uint64(pid))), ") S ", ") Z ", 1))
			}
		}, unavailable: true},
		{name: "root inventory overflow", mutate: func(m fstest.MapFS, _ *processObserver) {
			for i := range 513 {
				m[fmt.Sprintf("extra%d", i)] = &fstest.MapFile{}
			}
		}, unavailable: true},
		{name: "lifetime overflow", mutate: func(_ fstest.MapFS, o *processObserver) {
			for i := range maxObservedLifetimes {
				o.seen[observedLifetime{3, uint64(i + 1000)}] = struct{}{}
			}
		}, unavailable: true},
		{name: "worker absent", mutate: func(m fstest.MapFS, _ *processObserver) {
			for name := range m {
				if strings.HasPrefix(name, "2/") {
					delete(m, name)
				}
			}
		}, unavailable: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := observationProc()
			o := newProcessObserver(m, 2, 65534)
			if tc.mutate != nil {
				tc.mutate(m, o)
			}
			if tc.open != nil {
				reads := 0
				o.proc = procOpenFS{FS: m, open: func(name string) (fs.File, error) { return tc.open(name, m, &reads) }}
			}
			o.sample()
			if o.facts.Unavailable != tc.unavailable || (o.err != nil) != tc.unavailable || o.facts.Vanished != tc.vanished || o.facts.Raced != tc.raced {
				t.Fatalf("facts=%+v err=%v", o.facts, o.err)
			}
			priorErr := o.err
			o.proc = observationProc()
			o.sample()
			if o.facts.Unavailable != tc.unavailable || priorErr != o.err {
				t.Fatal("failure did not remain sticky")
			}
		})
	}
	t.Run("stop joins and is concurrent safe", func(t *testing.T) {
		stop, err := startObservations(context.Background(), observationProc(), 2, 65534)
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				facts, err := stop()
				if err != nil || facts.Samples < 2 || facts.DurationNanoseconds <= 0 || facts.Unavailable {
					t.Errorf("facts=%+v err=%v", facts, err)
				}
			})
		}
		wg.Wait()
	})
	t.Run("cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		stop, err := startObservations(ctx, observationProc(), 2, 65534)
		if err != nil {
			t.Fatal(err)
		}
		cancel()
		facts, err := stop()
		if !errors.Is(err, context.Canceled) || !facts.Unavailable {
			t.Fatalf("facts=%+v err=%v", facts, err)
		}
		if _, err := startObservations(ctx, observationProc(), 2, 65534); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
}

func TestObservationsFailureDiagnostic(t *testing.T) {
	t.Run("healthy observation has no diagnostic reads", func(t *testing.T) {
		m := observationProc()
		reads := map[string]int{}
		o := newProcessObserver(procOpenFS{FS: m, open: func(name string) (fs.File, error) {
			reads[name]++
			return m.Open(name)
		}}, 2, 65534)
		o.sample()
		if o.err != nil || o.facts.FailureProcess != nil || reads["3/stat"] != 2 || reads["3/status"] != 1 {
			t.Fatalf("healthy cost changed: %+v %v", o.facts, reads)
		}
	})
	for _, reused := range []bool{false, true} {
		t.Run(fmt.Sprintf("lifetime_reused_%t", reused), func(t *testing.T) {
			m := observationProc()
			m["3/stat"].Data = []byte(strings.Replace(string(procStat(3, 101)), "name ) with spaces", "child", 1))
			m["3/status"].Data = []byte("Pid:\t3\nUid:\t65534\t65534\t65534\t65534\nGid:\t65534\t65534\t65534\t65534\nNoNewPrivs:\t1\nCapPrm:\t0000000000000000\n")
			reads := map[string]int{}
			proc := procOpenFS{FS: m, open: func(name string) (fs.File, error) {
				reads[name]++
				if name == "3/fd" {
					if reused {
						m["3/stat"].Data = procStat(3, 999)
					}
					return nil, fs.ErrPermission
				}
				return m.Open(name)
			}}
			o := newProcessObserver(proc, 2, 65534)
			o.sample()
			if !errors.Is(o.err, fs.ErrPermission) || !o.facts.Unavailable || o.facts.UnexpectedErrors != 1 {
				t.Fatal("diagnostic relaxed observer", o.err)
			}
			if reused {
				if o.facts.FailureProcess != nil {
					t.Fatal("attributed a replacement lifetime")
				}
			} else if o.facts.FailureProcess == nil || o.facts.FailureProcess.Comm != "child" || reads["3/stat"] != 4 || reads["3/status"] != 2 {
				t.Fatalf("missing or unbounded capture: %+v %v", o.facts.FailureProcess, reads)
			}
			statReads, statusReads := reads["3/stat"], reads["3/status"]
			firstError, firstDiagnostic := o.err, o.facts.FailureProcess
			o.sample()
			if o.err != firstError || o.facts.FailureProcess != firstDiagnostic || o.facts.UnexpectedErrors != 2 || reads["3/stat"] != statReads+2 || reads["3/status"] != statusReads+1 {
				t.Fatal("sticky diagnostic/refusal changed or capture retried")
			}
		})
	}
}

func TestObservationsZombieBoundary(t *testing.T) {
	for _, tc := range []struct {
		name                          string
		initial, after                string
		fdErr, statErr                error
		reused, malformed             bool
		wantErr, gone                 bool
		fdReads, statReads, lifetimes uint64
		fds                           uint64
	}{
		{name: "healthy live", initial: "S", after: "S", fdReads: 1, statReads: 2, lifetimes: 1, fds: 3},
		{name: "initial zombie", initial: "Z", after: "Z", statReads: 2, lifetimes: 1},
		{name: "successful FD read then zombie", initial: "S", after: "Z", fdReads: 1, statReads: 2, lifetimes: 1},
		{name: "denied then zombie", initial: "S", after: "Z", fdErr: fs.ErrPermission, fdReads: 1, statReads: 2, lifetimes: 1},
		{name: "denied then gone", initial: "S", fdErr: fs.ErrPermission, statErr: fs.ErrNotExist, fdReads: 1, statReads: 2, gone: true},
		{name: "denied then ESRCH", initial: "S", fdErr: fs.ErrPermission, statErr: syscall.ESRCH, fdReads: 1, statReads: 2, gone: true},
		{name: "denied then live", initial: "S", after: "S", fdErr: fs.ErrPermission, fdReads: 1, statReads: 2, wantErr: true},
		{name: "denied then unreadable", initial: "S", fdErr: fs.ErrPermission, statErr: fs.ErrPermission, fdReads: 1, statReads: 2, wantErr: true},
		{name: "denied then malformed", initial: "S", fdErr: fs.ErrPermission, malformed: true, fdReads: 1, statReads: 2, wantErr: true},
		{name: "denied then unknown state", initial: "S", after: "?", fdErr: fs.ErrPermission, fdReads: 1, statReads: 2, wantErr: true},
		{name: "denied then reused zombie", initial: "S", after: "Z", fdErr: fs.ErrPermission, reused: true, fdReads: 1, statReads: 2, wantErr: true},
		{name: "mixed FD error", initial: "S", after: "Z", fdErr: errors.Join(fs.ErrPermission, syscall.EIO), fdReads: 1, statReads: 1, wantErr: true},
		{name: "non-permission FD error", initial: "S", after: "Z", fdErr: syscall.EIO, fdReads: 1, statReads: 1, wantErr: true},
		{name: "mixed gone error", initial: "S", fdErr: fs.ErrPermission, statErr: errors.Join(fs.ErrNotExist, syscall.EIO), fdReads: 1, statReads: 2, wantErr: true},
		{name: "zombie changes to live", initial: "Z", after: "S", statReads: 2, wantErr: true},
		{name: "zombie lifetime reused", initial: "Z", after: "Z", reused: true, statReads: 2, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := observationProc()
			var fdReads, statReads uint64
			proc := procOpenFS{FS: m, open: func(name string) (fs.File, error) {
				if name == "3/stat" {
					statReads++
					state, start := tc.initial, uint64(101)
					if statReads == 2 {
						if tc.statErr != nil {
							return nil, tc.statErr
						}
						if tc.malformed {
							return fstest.MapFS{"stat": {Data: []byte("malformed")}}.Open("stat")
						}
						state = tc.after
						if tc.reused {
							start++
						}
					}
					data := []byte(strings.Replace(string(procStat(3, start)), ") S ", ") "+state+" ", 1))
					return fstest.MapFS{"stat": {Data: data}}.Open("stat")
				}
				if name == "3/fd" {
					fdReads++
					if tc.fdErr != nil {
						return nil, tc.fdErr
					}
				}
				return m.Open(name)
			}}
			o := newProcessObserver(proc, 2, 65534)
			fds, start, err := o.process(3)
			if vanished(err) != tc.gone || (err != nil && !vanished(err)) != tc.wantErr || fds != tc.fds || start != 101 || o.facts.SampledChildLifetimes != tc.lifetimes || fdReads != tc.fdReads || statReads != tc.statReads {
				t.Fatalf("fds=%d start=%d err=%v facts=%+v reads=%d/%d", fds, start, err, o.facts, fdReads, statReads)
			}
			if tc.reused && o.facts.Raced != 1 {
				t.Fatal("lost lifetime race count")
			}
		})
	}
	for _, tc := range []struct {
		name              string
		entries           int
		malformed         bool
		readErr, closeErr error
		wantErr           bool
	}{
		{name: "empty read denied", readErr: fs.ErrPermission},
		{name: "empty close denied", closeErr: fs.ErrPermission, wantErr: true},
		{name: "read and close denied", readErr: fs.ErrPermission, closeErr: fs.ErrPermission, wantErr: true},
		{name: "close reports disappearance", closeErr: fs.ErrNotExist, wantErr: true},
		{name: "partial read denied", entries: 1, readErr: fs.ErrPermission, wantErr: true},
		{name: "partial close denied", entries: 1, closeErr: fs.ErrPermission, wantErr: true},
		{name: "malformed partial read denied", entries: 1, malformed: true, readErr: fs.ErrPermission, wantErr: true},
		{name: "malformed partial close denied", entries: 1, malformed: true, closeErr: fs.ErrPermission, wantErr: true},
		{name: "overflow partial read denied", entries: sandbox.DescriptorLimit + 1, readErr: fs.ErrPermission, wantErr: true},
		{name: "overflow partial close denied", entries: sandbox.DescriptorLimit + 1, closeErr: fs.ErrPermission, wantErr: true},
		{name: "mixed read and close", readErr: fs.ErrPermission, closeErr: syscall.EIO, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := observationProc()
			fdMap := fstest.MapFS{}
			for fd := range tc.entries {
				fdMap[strconv.Itoa(fd)] = &fstest.MapFile{}
			}
			if tc.malformed {
				delete(fdMap, "0")
				fdMap["invalid"] = &fstest.MapFile{}
			}
			entries, err := fs.ReadDir(fdMap, ".")
			if err != nil {
				t.Fatal(err)
			}
			statReads, readLimit := 0, 0
			o := newProcessObserver(procOpenFS{FS: m, open: func(name string) (fs.File, error) {
				if name == "3/stat" {
					statReads++
					if statReads == 2 {
						data := strings.Replace(string(procStat(3, 101)), ") S ", ") Z ", 1)
						return fstest.MapFS{"stat": {Data: []byte(data)}}.Open("stat")
					}
				}
				file, err := m.Open(name)
				if err == nil && name == "3/fd" {
					return procErrorDirectory{File: file, entries: entries, readErr: tc.readErr, closeErr: tc.closeErr, limit: &readLimit}, nil
				}
				return file, err
			}}, 2, 65534)
			fds, start, err := o.process(3)
			if (err != nil) != tc.wantErr || fds != 0 || start != 101 || readLimit != sandbox.DescriptorLimit+1 {
				t.Fatalf("fds=%d start=%d err=%v read limit=%d", fds, start, err, readLimit)
			}
			if tc.wantErr && (statReads != 1 || o.facts.SampledChildLifetimes != 0) || !tc.wantErr && (statReads != 2 || o.facts.SampledChildLifetimes != 1) {
				t.Fatalf("unexpected recovery: stats=%d facts=%+v", statReads, o.facts)
			}
		})
	}
	t.Run("healthy zombies preserve bounds and sticky errors", func(t *testing.T) {
		m := observationProc()
		m["3/stat"].Data = []byte(strings.Replace(string(procStat(3, 101)), ") S ", ") Z ", 1))
		o := newProcessObserver(procOpenFS{FS: m, open: func(name string) (fs.File, error) {
			if name == "3/fd" {
				t.Fatal("read zombie FDs")
			}
			return m.Open(name)
		}}, 2, 65534)
		o.sample()
		o.sample()
		if o.err != nil || o.facts.SampledChildLifetimes != 1 || o.facts.SampledAggregateFDPeak != 2 || o.facts.SampledProcessFDPeak != 2 {
			t.Fatalf("zombie sample: %+v %v", o.facts, o.err)
		}
		o.fail("prior", fs.ErrPermission)
		prior := o.err
		o.sample()
		if o.err != prior || !o.facts.Unavailable || o.facts.Failure != "prior" || o.facts.UnexpectedErrors != 1 {
			t.Fatal("zombie recovery cleared sticky failure")
		}
		for i := range maxObservedLifetimes {
			o.seen[observedLifetime{3, uint64(i + 1000)}] = struct{}{}
		}
		delete(o.seen, observedLifetime{3, 101})
		if _, _, err := o.process(3); err == nil {
			t.Fatal("zombie bypassed lifetime bound")
		}
	})
	t.Run("worker gone after FD denial remains fatal", func(t *testing.T) {
		m := observationProc()
		denied := false
		o := newProcessObserver(procOpenFS{FS: m, open: func(name string) (fs.File, error) {
			if name == "2/fd" {
				denied = true
				return nil, fs.ErrPermission
			}
			if denied && name == "2/stat" {
				return nil, fs.ErrNotExist
			}
			return m.Open(name)
		}}, 2, 65534)
		o.sample()
		if o.err == nil || !o.facts.Unavailable || o.facts.Vanished != 0 {
			t.Fatal("missing worker became benign")
		}
	})
}

func TestObservationsPrivateCache(t *testing.T) {
	t.Run("exact inventory excludes symlink targets and deduplicates hardlinks", func(t *testing.T) {
		scratch := t.TempDir()
		root := filepath.Join(scratch, "gocache")
		if err := os.Mkdir(root, 0700); err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(root, "data")
		if err := os.WriteFile(file, []byte("1234567"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(file, filepath.Join(root, "hardlink")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(scratch, filepath.Join(root, "excluded")); err != nil {
			t.Fatal(err)
		}
		facts, err := observePrivateCache(scratch)
		if err != nil || !facts.Complete || facts.Entries != 4 || facts.UniqueInodes != 3 || facts.RegularFiles != 2 || facts.Directories != 1 || facts.Symlinks != 1 || facts.LogicalBytes != 7 || len(facts.Roots) != 6 || len(facts.MissingRoots) != 5 {
			t.Fatalf("facts=%+v err=%v", facts, err)
		}
		again, err := observePrivateCache(scratch)
		if err != nil || !reflect.DeepEqual(facts, again) {
			t.Fatalf("quiescent inventory changed: %+v %v", again, err)
		}
	})
	t.Run("symlink root refused", func(t *testing.T) {
		scratch := t.TempDir()
		if err := os.Symlink(t.TempDir(), filepath.Join(scratch, "cache")); err != nil {
			t.Fatal(err)
		}
		facts, err := observePrivateCache(scratch)
		if err == nil || facts.Complete {
			t.Fatal("accepted symlink root")
		}
	})
	t.Run("logical byte overflow", func(t *testing.T) {
		scratch := t.TempDir()
		root := filepath.Join(scratch, "cache")
		if err := os.Mkdir(root, 0700); err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(filepath.Join(root, "sparse"))
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Truncate(sandbox.ScratchBytes + 1); err != nil {
			_ = f.Close()
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		facts, err := observePrivateCache(scratch)
		if err == nil || facts.Complete || facts.LogicalBytes != 0 {
			t.Fatalf("facts=%+v err=%v", facts, err)
		}
	})
	t.Run("entry overflow sentinel", func(t *testing.T) {
		root := t.TempDir()
		facts := PrivateCacheObservation{Entries: sandbox.NativeT451bScratchInodes}
		err := walkCache(root, &facts, map[cacheInode]struct{}{})
		if err == nil || facts.Entries != sandbox.NativeT451bScratchInodes {
			t.Fatal("entry overflow accepted")
		}
		facts.Entries--
		if err := walkCache(root, &facts, map[cacheInode]struct{}{}); err != nil || facts.Entries != sandbox.NativeT451bScratchInodes {
			t.Fatalf("exact boundary refused: %v", err)
		}
	})
}
