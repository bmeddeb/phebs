package t451b

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/bmeddeb/phebs/internal/typedsandbox"
	"github.com/bmeddeb/phebs/spike/t451a"
	"github.com/bmeddeb/phebs/spike/t451a/sandbox"
)

func fixtureManagedCost() ManagedCost {
	return ManagedCost{
		Schema:       ManagedCostSchema,
		Observations: Observations{Version: "phebs-t451b-sampled-observations-v1", IntervalNanoseconds: observationInterval.Nanoseconds(), DurationNanoseconds: 1, Samples: 2, SampledChildLifetimes: 1, ChildLifetimesLowerBound: true, SampledProcessFDPeak: 2, SampledAggregateFDPeak: 2, FDCountsNonAtomic: true},
		Cache:        PrivateCacheObservation{Version: "phebs-t451b-private-cache-v1", Roots: []string{"/scratch/bazel-user", "/scratch/bazel-output", "/scratch/repository-cache", "/scratch/gocache", "/scratch/gomodcache", "/scratch/cache"}, MissingRoots: []string{}, Entries: 6, Directories: 6, UniqueInodes: 6, Complete: true},
	}
}

func TestManagedCostStop(t *testing.T) {
	o := fixtureManagedCost().Observations
	o.Unavailable, o.UnexpectedErrors, o.Failure = true, 1, "process"
	o.FailureProcess = &t451a.ProcessDiagnostic{Comm: "bazel", State: "S", PPID: 1, PGID: 2, SID: 2, UID: [4]uint32{65534, 65534, 65534, 65534}, GID: [4]uint32{65534, 65534, 65534, 65534}, NoNewPrivs: true, CapPrm: "0000000000000000"}
	stopped := ManagedCostStop{Schema: ManagedCostStopSchema, Stage: "observations", Observations: &o}
	raw, err := EncodeManagedCostStop(stopped)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeManagedCostStop(raw)
	if err != nil || decoded.Observations.FailureProcess.Comm != "bazel" || decoded.Observations.Failure != "process" {
		t.Fatal("first denial facts were lost", err)
	}
	if _, err := DecodeManagedCost(raw); err == nil {
		t.Fatal("diagnostic stop became healthy cost")
	}
	healthy, err := EncodeManagedCost(fixtureManagedCost())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeManagedCostStop(healthy); err == nil {
		t.Fatal("healthy cost became diagnostic stop")
	}
	for _, stage := range []string{"observer_start", "worker", "cache", "cost_encode"} {
		s := ManagedCostStop{Schema: ManagedCostStopSchema, Stage: stage}
		if stage != "observer_start" {
			o := fixtureManagedCost().Observations
			s.Observations = &o
		}
		if _, err := EncodeManagedCostStop(s); err != nil {
			t.Fatal("closed stop stage refused", stage, err)
		}
	}
	for _, raw := range [][]byte{bytes.TrimSpace(raw), append(slices.Clone(raw), '\n'), bytes.Replace(raw, []byte(`"stage":"observations"`), []byte(`"stage":"/private/path"`), 1), bytes.Replace(raw, []byte(`"cache_complete":false`), []byte(`"cache_complete":true`), 1), bytes.Replace(raw, []byte(`"comm":"bazel"`), []byte(`"comm":"/private/path"`), 1), bytes.Replace(raw, []byte(`"schema":`), []byte(`"raw_error":"private","schema":`), 1), bytes.Repeat([]byte{' '}, MaxManagedCostBytes+1)} {
		if _, err := DecodeManagedCostStop(raw); err == nil {
			t.Fatal("noncanonical, healthy or unsafe diagnostic admitted")
		}
	}
}

func TestManagedCost(t *testing.T) {
	m := fixtureManagedCost()
	raw, err := EncodeManagedCost(m)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeManagedCost(raw)
	if err != nil {
		t.Fatal(err)
	}
	again, err := EncodeManagedCost(decoded)
	if err != nil || !bytes.Equal(raw, again) {
		t.Fatal("qualified cost roundtrip", err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*ManagedCost)
	}{
		{"schema", func(m *ManagedCost) { m.Schema = "legacy" }},
		{"sticky-denial", func(m *ManagedCost) { m.Observations.Unavailable = true }},
		{"unexpected-error", func(m *ManagedCost) { m.Observations.UnexpectedErrors++ }},
		{"failure", func(m *ManagedCost) { m.Observations.Failure = "worker" }},
		{"zero-samples", func(m *ManagedCost) { m.Observations.Samples = 0 }},
		{"zero-lifetimes", func(m *ManagedCost) { m.Observations.SampledChildLifetimes = 0 }},
		{"unqualified-lifetimes", func(m *ManagedCost) { m.Observations.ChildLifetimesLowerBound = false }},
		{"atomic-descriptor-claim", func(m *ManagedCost) { m.Observations.FDCountsNonAtomic = false }},
		{"descriptor-cap", func(m *ManagedCost) { m.Observations.SampledProcessFDPeak = typedsandbox.DescriptorLimit + 1 }},
		{"aggregate-descriptor-cap", func(m *ManagedCost) {
			m.Observations.SampledAggregateFDPeak = typedsandbox.TaskLimit*typedsandbox.DescriptorLimit + 1
		}},
		{"duration", func(m *ManagedCost) { m.Observations.DurationNanoseconds = int64(typedsandbox.WallLimit) + 1 }},
		{"incomplete-cache", func(m *ManagedCost) { m.Cache.Complete = false }},
		{"cache-inodes", func(m *ManagedCost) { m.Cache.Entries = typedsandbox.ScratchInodes + 1 }},
		{"cache-bytes", func(m *ManagedCost) { m.Cache.LogicalBytes = typedsandbox.ScratchBytes + 1 }},
		{"cache-allocated", func(m *ManagedCost) { m.Cache.AllocatedBytes = typedsandbox.ScratchBytes + 1 }},
		{"cache-root", func(m *ManagedCost) { m.Cache.Roots[0] = "/inputs" }},
		{"missing-root", func(m *ManagedCost) { m.Cache.MissingRoots = []string{"/scratch/unknown"} }},
		{"repeated-missing-root", func(m *ManagedCost) { m.Cache.MissingRoots = []string{"/scratch/cache", "/scratch/cache"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := fixtureManagedCost()
			tc.mutate(&v)
			if _, err := EncodeManagedCost(v); err == nil {
				t.Fatal("invalid cost became successful evidence")
			}
		})
	}
	for _, malformed := range [][]byte{bytes.TrimSpace(raw), append(slices.Clone(raw), '\n'), bytes.Replace(raw, []byte(`"schema":`), []byte(`"unknown":1,"schema":`), 1), bytes.Replace(raw, []byte(`"samples":2`), []byte(`"samples":2,"samples":2`), 1), bytes.Repeat([]byte{' '}, MaxManagedCostBytes+1)} {
		if _, err := DecodeManagedCost(malformed); err == nil {
			t.Fatal("noncanonical or oversized metrics admitted")
		}
	}
}

func TestManagedObservationBoundsPreserveLegacy(t *testing.T) {
	if sandbox.DescriptorLimit != typedsandbox.DescriptorLimit || sandbox.NativeT451bScratchInodes != typedsandbox.ScratchInodes {
		t.Fatal("shared fixed descriptor/inode ceilings diverged")
	}
	m := observationProc()
	for pid := 5; pid <= typedsandbox.TaskLimit; pid++ {
		addProc(m, pid, uint64(pid), 0, 0)
	}
	legacy := newProcessObserver(m, 2, 65534)
	legacy.sample()
	if legacy.err == nil || !legacy.facts.Unavailable {
		t.Fatal("legacy process cap was widened")
	}
	managed := newProcessObserverBounded(m, 2, 65534, typedsandbox.TaskLimit)
	managed.sample()
	if managed.err != nil || managed.facts.Unavailable {
		t.Fatal("frozen managed process cap refused", managed.err)
	}
	addProc(m, typedsandbox.TaskLimit+1, 400, 0, 0)
	managed.sample()
	if managed.err == nil || !managed.facts.Unavailable {
		t.Fatal("managed process cap was widened")
	}
	scratch := t.TempDir()
	cache := filepath.Join(scratch, "cache")
	if err := os.Mkdir(cache, 0700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(cache, "sparse"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := f.Truncate(sandbox.ScratchBytes + 1); err != nil {
		t.Fatal(err)
	}
	if _, err := observePrivateCache(scratch); err == nil {
		t.Fatal("legacy cache cap was widened")
	}
	if c, err := observePrivateCacheBounded(scratch, typedsandbox.ScratchBytes); err != nil || !c.Complete || c.LogicalBytes != sandbox.ScratchBytes+1 {
		t.Fatal("managed cache bound does not use frozen managed scratch", err)
	}
	if err := f.Truncate(typedsandbox.ScratchBytes + 1); err != nil {
		t.Fatal(err)
	}
	if _, err := observePrivateCacheBounded(scratch, typedsandbox.ScratchBytes); err == nil {
		t.Fatal("managed cache cap was widened")
	}
}
