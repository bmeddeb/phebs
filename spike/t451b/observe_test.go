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
		{name: "oversized proc record", mutate: func(m fstest.MapFS, _ *processObserver) { m["3/status"].Data = []byte(strings.Repeat("x", 8193)) }, unavailable: true},
		{name: "FD overflow", mutate: func(m fstest.MapFS, _ *processObserver) { addProc(m, 3, 101, 65534, sandbox.DescriptorLimit+1) }, unavailable: true},
		{name: "process overflow", mutate: func(m fstest.MapFS, _ *processObserver) {
			for pid := 5; pid <= sandbox.TaskLimit+1; pid++ {
				addProc(m, pid, uint64(pid), 0, 0)
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
		facts := PrivateCacheObservation{Entries: sandbox.ScratchInodes}
		err := walkCache(root, &facts, map[cacheInode]struct{}{})
		if err == nil || facts.Entries != sandbox.ScratchInodes {
			t.Fatal("entry overflow accepted")
		}
		facts.Entries--
		if err := walkCache(root, &facts, map[cacheInode]struct{}{}); err != nil || facts.Entries != sandbox.ScratchInodes {
			t.Fatalf("exact boundary refused: %v", err)
		}
	})
}
