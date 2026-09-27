package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/typedbazel/planner"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"google.golang.org/protobuf/encoding/protowire"
)

func provisionBytes(n protowire.Number, b []byte) []byte {
	return protowire.AppendBytes(protowire.AppendTag(nil, n, protowire.BytesType), b)
}
func provisionString(n protowire.Number, s string) []byte { return provisionBytes(n, []byte(s)) }
func provisionInt(n protowire.Number, v uint64) []byte {
	return protowire.AppendVarint(protowire.AppendTag(nil, n, protowire.VarintType), v)
}
func provisionJoin(b ...[]byte) []byte { return bytes.Join(b, nil) }
func provisionFrame(b []byte) []byte   { return protowire.AppendBytes(nil, b) }
func provisionJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func provisionInventory(t *testing.T, files []typedindex.BundleFile) typedindex.Inventory {
	t.Helper()
	slices.SortFunc(files, func(a, b typedindex.BundleFile) int { return strings.Compare(a.Path, b.Path) })
	raw := provisionJSON(t, typedindex.InventoryDefinition{Schema: typedindex.InventorySchema, Files: files})
	inv, e := typedindex.DecodeInventory(t.Context(), raw, hash(raw))
	if e != nil {
		t.Fatal(e)
	}
	return inv
}

// Neutral native wire fixture: two aliases reach one source package under two
// configurations. No Bazel, source tool, retained private data or filesystem read.
func provisionFixture(t *testing.T) Provisioning {
	t.Helper()
	i, s, _, _, _ := fixture(t)
	in := Provisioning{Source: s.Source, Inventory: i.Inventory, Roots: []string{"//app:a", "//app:b"}, Module: s.Module, Remote: s.Remote, Projections: map[string][]byte{}}
	common := "@@//lib:lib"
	source := "@@//lib:lib.go"
	in.Cquery = provisionFrame(provisionBytes(1, provisionBytes(1, provisionJoin(provisionInt(1, 2), provisionBytes(3, provisionString(1, source))))))
	in.Aquery = provisionJoin(provisionBytes(3, provisionJoin(provisionInt(1, 1), provisionString(2, common))), provisionBytes(6, provisionJoin(provisionInt(1, 1), provisionString(2, "@@//phebs_plan:aspect.bzl%phebs_plan"))))
	for j := uint64(1); j <= 2; j++ {
		cfg := strings.Repeat(string(rune('a'+j)), 64)
		config := provisionJoin(provisionInt(1, j), provisionString(4, cfg))
		in.Cquery = append(in.Cquery, provisionFrame(provisionBytes(2, config))...)
		in.Aquery = append(in.Aquery, provisionBytes(5, config)...)
		rule := provisionJoin(provisionString(1, common), provisionString(2, "go_library"), provisionBytes(15, provisionString(1, source)))
		target := provisionJoin(provisionBytes(1, provisionJoin(provisionInt(1, 1), provisionBytes(2, rule))), provisionInt(3, j))
		in.Cquery = append(in.Cquery, provisionFrame(provisionBytes(1, target))...)
		alias := provisionJoin(provisionString(1, "@@"+in.Roots[j-1]), provisionString(2, "alias"), provisionBytes(15, provisionJoin(provisionString(1, common), provisionString(2, cfg), provisionInt(3, j))))
		in.Cquery = append(in.Cquery, provisionFrame(provisionBytes(1, provisionJoin(provisionBytes(1, provisionJoin(provisionInt(1, 1), provisionBytes(2, alias))), provisionInt(3, j))))...)
		name := "lib" + string(rune('0'+j)) + ".phebs-plan.json"
		parts := []string{"bazel-out", "cfg", "bin", "lib", name}
		for k, part := range parts {
			id := (j-1)*5 + uint64(k) + 1
			parent := uint64(0)
			if k > 0 {
				parent = id - 1
			}
			in.Aquery = append(in.Aquery, provisionBytes(8, provisionJoin(provisionInt(1, id), provisionString(2, part), provisionInt(3, parent)))...)
		}
		in.Aquery = append(in.Aquery, provisionBytes(1, provisionJoin(provisionInt(1, j), provisionInt(2, j*5)))...)
		in.Aquery = append(in.Aquery, provisionBytes(2, provisionJoin(provisionInt(1, 1), provisionInt(2, 1), provisionString(4, "PhebsPlan"), provisionInt(5, j), provisionInt(9, j)))...)
		file := planner.File{Artifact: planner.Artifact{Path: "lib/lib.go", ShortPath: "lib/lib.go", Owner: source, Source: true}, SHA256: strings.TrimPrefix(hash([]byte("package lib\n")), "sha256:"), Bytes: 12}
		export := "bazel-out/cfg/bin/lib/lib" + string(rune('0'+j)) + ".x"
		archive := map[string]any{"name": "lib", "label": common, "export": export, "import_path": "example.test/lib", "import_map": "example.test/lib", "go_files": []planner.File{file}, "compiled_go_files": []planner.File{file}, "imports": []planner.Import{}}
		in.Projections[strings.Join(parts, "/")] = provisionJSON(t, map[string]any{"version": planner.ProjectionV2, "owner": common, "roots": []string{export}, "embeds": []string{}, "archives": []any{archive}})
	}
	return in
}
func TestBuildSelection(t *testing.T) {
	in := provisionFixture(t)
	if _, err := planner.AssembleRootsV2(in.Cquery, in.Aquery, in.Projections, in.Roots); err != nil {
		t.Fatal(err)
	}
	raw, u, e := BuildSelection(t.Context(), in)
	if e != nil {
		t.Fatal(e)
	}
	var s Selection
	if e = json.Unmarshal(raw, &s); e != nil {
		t.Fatal(e)
	}
	if len(s.Targets) != 5 || s.Source != in.Source || u != identity(s.Targets) {
		t.Fatal(s, u)
	}
	p, e := planner.AssembleRootsV2(in.Cquery, in.Aquery, in.Projections, in.Roots)
	if e != nil {
		t.Fatal(e)
	}
	rows, e := mappedTargets(t.Context(), p)
	if e != nil || identity(rows) != u || len(p.Units) != 2 {
		t.Fatal(rows, e)
	}
	in.Roots[0], in.Roots[1] = in.Roots[1], in.Roots[0]
	var frames [][]byte
	for raw := in.Cquery; len(raw) > 0; {
		_, n := protowire.ConsumeBytes(raw)
		if n < 0 {
			t.Fatal("fixture frame")
		}
		frames = append(frames, bytes.Clone(raw[:n]))
		raw = raw[n:]
	}
	slices.Reverse(frames)
	in.Cquery = bytes.Join(frames, nil)
	again, u2, e := BuildSelection(t.Context(), in)
	if e != nil || !bytes.Equal(raw, again) || u != u2 {
		t.Fatal("permutation", e)
	}
	for _, change := range []string{"wildcard", "empty", "duplicate-root", "missing-root", "truncated", "duplicate-target", "ambiguous-root", "extra-target", "missing-projection", "extra-projection", "source-bytes", "source-digest", "no-inventory", "source-identity", "remote", "cancel"} {
		t.Run(change, func(t *testing.T) {
			v := provisionFixture(t)
			ctx := t.Context()
			switch change {
			case "wildcard":
				v.Roots = []string{"//..."}
			case "empty":
				v.Roots = nil
			case "duplicate-root":
				v.Roots = append(v.Roots, v.Roots[0])
			case "missing-root":
				v.Roots = []string{"//missing:missing"}
			case "truncated":
				v.Cquery = v.Cquery[:len(v.Cquery)-1]
			case "duplicate-target":
				v.Cquery = append(v.Cquery, v.Cquery...)
			case "ambiguous-root":
				rule := provisionJoin(provisionString(1, "@@"+v.Roots[0]), provisionString(2, "alias"))
				v.Cquery = append(v.Cquery, provisionFrame(provisionBytes(1, provisionJoin(provisionBytes(1, provisionJoin(provisionInt(1, 1), provisionBytes(2, rule))), provisionInt(3, 2))))...)
			case "extra-target":
				v.Roots = v.Roots[:1]
			case "missing-projection":
				for k := range v.Projections {
					delete(v.Projections, k)
					break
				}
			case "extra-projection":
				v.Projections["extra.json"] = []byte("{}")
			case "source-bytes", "source-digest":
				fs := v.Inventory.Files()
				for j := range fs {
					if fs[j].Path == "source/lib/lib.go" {
						if change == "source-bytes" {
							fs[j].Bytes++
						} else {
							fs[j].Digest = hash([]byte("changed"))
						}
					}
				}
				v.Inventory = provisionInventory(t, fs)
			case "no-inventory":
				v.Inventory = typedindex.Inventory{}
			case "source-identity":
				v.Source.Commit = "not-a-commit"
			case "remote":
				v.Remote = "https://user:secret@example.test/repo"
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if _, _, e := BuildSelection(ctx, v); e == nil {
				t.Fatal("accepted", change)
			}
		})
	}
}
func TestSelectionSharedMapAndBounds(t *testing.T) {
	i, s, p, _, _ := fixture(t)
	p.Targets[0].Kind = "go_test"
	rows, e := mappedTargets(t.Context(), p)
	if e != nil || len(rows) != 1 || !rows[0].Test || len(rows[0].Units) != 1 {
		t.Fatal(rows, e)
	}
	p.Targets[0].Kind = "go_library"
	roots, _ := configuredRoots(p, s)
	planned, _, _, e := mapPlan(t.Context(), i, s, p, roots)
	if e != nil {
		t.Fatal(e)
	}
	var def typedindex.PackagePlanDefinition
	if e = json.Unmarshal(planned.Bytes(), &def); e != nil || identity(def.Targets) != identity(s.Targets) {
		t.Fatal(e)
	}
	for _, which := range []string{"units", "targets", "edges", "projection-count", "projection-bytes", "projection-aggregate", "cquery-bytes", "aquery-bytes"} {
		t.Run(which, func(t *testing.T) {
			in := provisionFixture(t)
			_, _, p, _, _ := fixture(t)
			switch which {
			case "units":
				p.Units = make([]planner.Unit, typedindex.MaxBundleUnits+1)
			case "targets":
				p.Targets = make([]planner.Target, typedindex.MaxBundleTargets+1)
			case "edges":
				p.Targets[0].Inputs = make([]planner.Configured, typedindex.MaxBundleEdges+1)
			case "cquery-bytes":
				in.Cquery = make([]byte, planner.MaxProtoBytes+1)
			case "aquery-bytes":
				in.Aquery = make([]byte, planner.MaxProtoBytes+1)
			case "projection-aggregate":
				block := make([]byte, planner.MaxProjectionBytes)
				for j := 0; j < 9; j++ {
					in.Projections["p"+string(rune('a'+j))+".json"] = block
				}
			case "projection-count":
				for j := 0; j <= planner.MaxProjections; j++ {
					in.Projections[string(rune(j+100))] = []byte("x")
				}
			case "projection-bytes":
				in.Projections["huge.json"] = make([]byte, planner.MaxProjectionBytes+1)
			}
			if strings.HasPrefix(which, "projection") || which == "cquery-bytes" || which == "aquery-bytes" {
				_, _, e = BuildSelection(t.Context(), in)
			} else {
				_, e = mappedTargets(t.Context(), p)
			}
			if !errors.Is(e, typedindex.Capacity) {
				t.Fatal(e)
			}
		})
	}
}
