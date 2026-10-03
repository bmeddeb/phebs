package t451b

import (
	"bytes"
	"slices"
	"testing"

	"github.com/bmeddeb/phebs/internal/typedsandbox"
)

func TestManagedCache(t *testing.T) {
	fixture := ManagedCache{Schema: ManagedCacheSchema, Cache: fixtureManagedCost().Cache}
	raw, err := EncodeManagedCache(fixture)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeManagedCache(raw)
	if err != nil {
		t.Fatal(err)
	}
	again, err := EncodeManagedCache(decoded)
	if err != nil || !bytes.Equal(raw, again) || len(raw) > MaxManagedCostBytes {
		t.Fatal("cache-only physical frame roundtrip", err)
	}
	if _, err := DecodeManagedCost(raw); err == nil {
		t.Fatal("cache frame became a v1 process witness")
	}
	v1, err := EncodeManagedCost(fixtureManagedCost())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeManagedCache(v1); err == nil {
		t.Fatal("v1 process witness became a v2 cache frame")
	}
	for _, tc := range []struct {
		name   string
		mutate func(*ManagedCache)
		wire   func([]byte) []byte
	}{
		{name: "schema", mutate: func(m *ManagedCache) { m.Schema = ManagedCostSchema }},
		{name: "missing-cache", wire: func([]byte) []byte {
			return []byte(`{"schema":"` + ManagedCacheSchema + `"}` + "\n")
		}},
		{name: "incomplete", mutate: func(m *ManagedCache) { m.Cache.Complete = false }},
		{name: "unfixed-roots", mutate: func(m *ManagedCache) { m.Cache.Roots = []string{"/inputs"} }},
		{name: "inode-cap", mutate: func(m *ManagedCache) { m.Cache.Entries = typedsandbox.ScratchInodes + 1 }},
		{name: "logical-cap", mutate: func(m *ManagedCache) { m.Cache.LogicalBytes = typedsandbox.ScratchBytes + 1 }},
		{name: "allocated-cap", mutate: func(m *ManagedCache) { m.Cache.AllocatedBytes = typedsandbox.ScratchBytes + 1 }},
		{name: "missing-root", mutate: func(m *ManagedCache) { m.Cache.MissingRoots = []string{"/scratch/unknown"} }},
		{name: "duplicate-missing-root", mutate: func(m *ManagedCache) {
			m.Cache.MissingRoots = []string{"/scratch/cache", "/scratch/cache"}
		}},
		{name: "count-shape", mutate: func(m *ManagedCache) { m.Cache.RegularFiles = m.Cache.Entries + 1 }},
		{name: "unknown-field", wire: func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"schema":`), []byte(`"observations":{},"schema":`), 1)
		}},
		{name: "duplicate-schema", wire: func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"schema":`), []byte(`"schema":"`+ManagedCacheSchema+`","schema":`), 1)
		}},
		{name: "changed-framing", wire: bytes.TrimSpace},
		{name: "trailing-LF", wire: func(b []byte) []byte { return append(slices.Clone(b), '\n') }},
		{name: "trailing-object", wire: func(b []byte) []byte { return append(slices.Clone(b), []byte("{}\n")...) }},
		{name: "physical-cap", wire: func([]byte) []byte { return bytes.Repeat([]byte{' '}, MaxManagedCostBytes+1) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.mutate != nil {
				m := ManagedCache{Schema: ManagedCacheSchema, Cache: fixtureManagedCost().Cache}
				tc.mutate(&m)
				if _, err := EncodeManagedCache(m); err == nil {
					t.Fatal("invalid cache became healthy evidence")
				}
				return
			}
			if _, err := DecodeManagedCache(tc.wire(raw)); err == nil {
				t.Fatal("noncanonical or incomplete cache frame admitted")
			}
		})
	}
}
