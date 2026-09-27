package provider

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strings"

	"github.com/bmeddeb/phebs/internal/typedbazel/planner"
	"github.com/bmeddeb/phebs/internal/typedindex"
)

// Provisioning contains already independently reviewed planner outputs and the
// exact source inventory. BuildSelection checks their content, not their native
// execution origin. The trusted provisioner must bind these inputs to the same
// source/tools/configuration evidence; a digest or raw Plan is not that proof.
// Inventory precedes insertion of SelectionFile; its digest is not the final
// bundle digest. Install the returned selection, then seal inventory/profile.
// Callers must not mutate input slices/maps concurrently with this operation.
type Provisioning struct {
	Source         typedindex.Source
	Inventory      typedindex.Inventory
	Roots          []string
	Module, Remote string
	Cquery, Aquery []byte
	Projections    map[string][]byte
}

// BuildSelection emits existing Selection-v1 bytes, never request admission,
// runtime readiness, a publication, or permission to execute a target.
func BuildSelection(ctx context.Context, in Provisioning) ([]byte, string, error) {
	if ctx == nil {
		return nil, "", typedindex.Invalid
	}
	if e := ctx.Err(); e != nil {
		return nil, "", e
	}
	if in.Source.Validate() != nil || in.Inventory.Digest() == "" || !selectionLocation(in.Module, in.Remote) {
		return nil, "", typedindex.Invalid
	}
	if _, e := planner.NativeCommands(in.Roots); e != nil {
		return nil, "", typedindex.Invalid
	}
	if len(in.Cquery) == 0 || len(in.Cquery) > planner.MaxProtoBytes || len(in.Aquery) == 0 || len(in.Aquery) > planner.MaxProtoBytes || len(in.Projections) > planner.MaxProjections {
		return nil, "", typedindex.Capacity
	}
	total := 0
	for name, raw := range in.Projections {
		if e := ctx.Err(); e != nil {
			return nil, "", e
		}
		if !safeRelative(name) || len(raw) == 0 || len(raw) > planner.MaxProjectionBytes || len(raw) > planner.MaxProtoBytes-total {
			return nil, "", typedindex.Capacity
		}
		total += len(raw)
	}
	p, e := planner.AssembleRootsV2(in.Cquery, in.Aquery, in.Projections, in.Roots)
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if e != nil {
		return nil, "", typedindex.Invalid
	}
	rows, e := mappedTargets(ctx, p)
	if e != nil {
		return nil, "", e
	}
	if _, e = mappedSources(ctx, in.Inventory, p); e != nil {
		return nil, "", e
	}
	roots := slices.Clone(in.Roots)
	slices.Sort(roots)
	s := Selection{Schema: SelectionSchema, Source: in.Source, Roots: roots, Targets: rows, Module: in.Module, Remote: in.Remote}
	if _, e = configuredRoots(p, s); e != nil {
		return nil, "", e
	}
	raw, e := json.Marshal(s)
	if e != nil || len(raw) > MaxSelectionBytes {
		return nil, "", typedindex.Capacity
	}
	if e = ctx.Err(); e != nil {
		return nil, "", e
	}
	return raw, identity(rows), nil
}

func selectionLocation(module, remote string) bool {
	u, e := url.Parse(remote)
	return e == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && len(remote) <= 4096 && module != "" && len(module) <= 4096 && !strings.ContainsAny(module, "\\\x00\r\n \t") && !strings.ContainsAny(remote, "\x00\r\n")
}

// mappedTargets is shared by provisioning and the independently rebuilt worker
// map. Bounds precede target-row allocation; the owned planner proves closure.
func mappedTargets(ctx context.Context, p planner.Plan) ([]typedindex.PlannedTarget, error) {
	if len(p.Units) > typedindex.MaxBundleUnits || len(p.Targets) > typedindex.MaxBundleTargets || len(p.Documents) > planner.MaxDocuments {
		return nil, typedindex.Capacity
	}
	if p.Version != planner.PlanV2 {
		return nil, typedindex.Invalid
	}
	remaining := typedindex.MaxBundleEdges
	for _, t := range p.Targets {
		n := len(t.Inputs) + len(t.Units)
		if n > remaining {
			return nil, typedindex.Capacity
		}
		remaining -= n
	}
	for _, u := range p.Units {
		if len(u.Imports) > remaining {
			return nil, typedindex.Capacity
		}
		remaining -= len(u.Imports)
	}
	rows := make([]typedindex.PlannedTarget, 0, len(p.Targets))
	for _, t := range p.Targets {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		row := typedindex.PlannedTarget{ID: targetID(t.Configured), Dependencies: []string{}, Units: []typedindex.PackageUnitID{}, Test: t.Kind == "go_test"}
		for _, dep := range t.Inputs {
			row.Dependencies = append(row.Dependencies, targetID(dep))
		}
		for _, id := range t.Units {
			v, e := unitID(id)
			if e != nil {
				return nil, e
			}
			row.Units = append(row.Units, v)
		}
		slices.Sort(row.Dependencies)
		slices.Sort(row.Units)
		rows = append(rows, row)
	}
	slices.SortFunc(rows, func(a, b typedindex.PlannedTarget) int { return strings.Compare(a.ID, b.ID) })
	return rows, nil
}
func mappedSources(ctx context.Context, inv typedindex.Inventory, p planner.Plan) (map[string]planner.Document, error) {
	sources := map[string]typedindex.BundleFile{}
	for _, f := range inv.Files() {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		if strings.HasPrefix(f.Path, "source/") {
			sources[strings.TrimPrefix(f.Path, "source/")] = f
		}
	}
	docs := make(map[string]planner.Document, len(p.Documents))
	for _, doc := range p.Documents {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		if doc.Kind == "source" {
			f, ok := sources[doc.Path]
			if !ok || doc.Repository != "" || doc.ExecPath != doc.Path || f.Bytes != int64(doc.Bytes) || f.Digest != "sha256:"+doc.SHA256 {
				return nil, typedindex.Stale
			}
		}
		docs[doc.ID] = doc
	}
	return docs, nil
}
