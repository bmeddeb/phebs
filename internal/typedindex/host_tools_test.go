package typedindex

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"testing"
)

func hostToolsAdmission(t *testing.T, files []BundleFile) (Admission, Inventory) {
	t.Helper()
	raw := wire(t, InventoryDefinition{Schema: InventorySchema, Files: files})
	inv, err := DecodeInventory(t.Context(), raw, hash(raw))
	if err != nil {
		t.Fatal(err)
	}
	p, authority, request, _ := fixture(t)
	d := p.Definition()
	d.BundleDigest = inv.Digest()
	p, err = DecodeProfile(t.Context(), wire(t, d))
	if err != nil {
		t.Fatal(err)
	}
	authority.Profile.Digest = p.Digest()
	request = NewRequest(request.Source, p, request.ProfileEpoch, request.UniverseDigest, request.IdempotencyKey)
	return admit(t, p, authority, request), inv
}

func TestBoundHostTools(t *testing.T) {
	raw := wire(t, HostToolsDefinition{HostToolsSchema, hash([]byte("formatter"))})
	files := []BundleFile{{ManagedHelperFile, 1, hash([]byte("helper")), true}, {HostToolsFile, int64(len(raw)), hash(raw), false}}
	a, inv := hostToolsAdmission(t, files)
	got, err := BindHostTools(t.Context(), a, inv, raw)
	if err != nil || got.Helper != files[0] || got.MkfsDigest != hash([]byte("formatter")) {
		t.Fatal(got, err)
	}
	for _, tc := range []struct {
		name   string
		change func([]BundleFile) []BundleFile
	}{
		{"helper missing", func(f []BundleFile) []BundleFile { return f[1:] }},
		{"metadata missing", func(f []BundleFile) []BundleFile { return f[:1] }},
		{"helper not executable", func(f []BundleFile) []BundleFile { f[0].Executable = false; return f }},
		{"empty helper", func(f []BundleFile) []BundleFile { f[0].Bytes = 0; return f }},
		{"executable metadata", func(f []BundleFile) []BundleFile { f[1].Executable = true; return f }},
		{"wrong length", func(f []BundleFile) []BundleFile { f[1].Bytes++; return f }},
		{"wrong hash", func(f []BundleFile) []BundleFile { f[1].Digest = hash([]byte("different")); return f }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			admitted, inventory := hostToolsAdmission(t, tc.change(slices.Clone(files)))
			if _, err := BindHostTools(t.Context(), admitted, inventory, raw); !errors.Is(err, Invalid) {
				t.Fatal(err)
			}
		})
	}
	_, other := hostToolsAdmission(t, []BundleFile{files[0]})
	if _, err := BindHostTools(t.Context(), a, other, raw); !errors.Is(err, Invalid) {
		t.Fatal("cross-inventory", err)
	}
	for name, bad := range map[string][]byte{
		"unknown":    append(bytes.Clone(raw[:len(raw)-1]), []byte(`,"path":"/bin/sh"}`)...),
		"duplicate":  bytes.Replace(raw, []byte(`"schema":`), []byte(`"schema":"old","schema":`), 1),
		"missing":    []byte(`{"schema":"phebs-typed-host-tools-v1"}`),
		"whitespace": append([]byte(" "), raw...),
		"schema":     wire(t, HostToolsDefinition{"future", hash([]byte("formatter"))}),
		"digest":     wire(t, HostToolsDefinition{HostToolsSchema, "not-a-digest"}),
		"oversize":   bytes.Repeat([]byte("x"), MaxHostToolsBytes+1),
		"empty":      {},
	} {
		t.Run(name, func(t *testing.T) {
			changed := slices.Clone(files)
			changed[1].Bytes, changed[1].Digest = int64(len(bad)), hash(bad)
			admitted, inventory := hostToolsAdmission(t, changed)
			if _, err := BindHostTools(t.Context(), admitted, inventory, bad); !errors.Is(err, Invalid) {
				t.Fatal(err)
			}
		})
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := BindHostTools(canceled, a, inv, raw); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	//nolint:staticcheck // Deliberate invalid-context refusal regression.
	if _, err := BindHostTools(nil, a, inv, raw); !errors.Is(err, Invalid) {
		t.Fatal(err)
	}
	if _, err := BindHostTools(t.Context(), Admission{}, inv, raw); !errors.Is(err, Invalid) {
		t.Fatal(err)
	}
}
