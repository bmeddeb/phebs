package provider

import (
	"context"
	"encoding/json"
	"net/url"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bmeddeb/phebs/internal/typedbazel/launcher"
	"github.com/bmeddeb/phebs/internal/typedbazel/planner"
	"github.com/bmeddeb/phebs/internal/typedindex"
)

func bindSelection(ctx context.Context, i Invocation, raw []byte) (Selection, error) {
	s, e := decode[Selection](raw, MaxSelectionBytes)
	if e != nil {
		return s, e
	}
	var entry typedindex.BundleFile
	for _, f := range i.Inventory.Files() {
		if f.Path == SelectionFile {
			entry = f
		}
	}
	if entry.Executable || entry.Bytes != int64(len(raw)) || entry.Digest != hash(raw) || s.Schema != SelectionSchema || s.Source != i.Parent.Request().Source || len(s.Targets) == 0 || len(s.Targets) > typedindex.MaxBundleTargets || identity(s.Targets) != i.Parent.Request().UniverseDigest {
		return s, typedindex.Stale
	}
	if _, e = planner.NativeCommands(s.Roots); e != nil {
		return s, typedindex.Invalid
	}
	u, e := url.Parse(s.Remote)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(s.Remote) > 4096 || s.Module == "" || len(s.Module) > 4096 || strings.ContainsAny(s.Module, "\\\x00\r\n \t") || strings.ContainsAny(s.Remote, "\x00\r\n") {
		return s, typedindex.Invalid
	}
	previous := ""
	for _, t := range s.Targets {
		if !digest(t.ID) || t.ID <= previous || !slices.IsSorted(t.Dependencies) || !slices.IsSorted(t.Units) {
			return s, typedindex.Invalid
		}
		previous = t.ID
	}
	return s, ctx.Err()
}
func configuredRoots(p planner.Plan, s Selection) ([]planner.Configured, error) {
	var roots []planner.Configured
	for _, label := range s.Roots {
		n := 0
		for _, t := range p.Targets {
			if t.Label == "@@"+label {
				roots = append(roots, t.Configured)
				n++
			}
		}
		if n != 1 {
			return nil, typedindex.Unsupported
		}
	}
	return roots, nil
}
func unitID(id string) (typedindex.PackageUnitID, error) {
	return typedindex.NewPackageUnitID("sha256:" + id)
}
func targetID(c planner.Configured) string { return identity(c) }

// mapPlan preserves every configured target/dependency/package edge. Only
// root-package compiled Go documents are SCIP members; dependencies remain load
// units. Omitted generated documents are explicit observations, not exclusions.
func mapPlan(ctx context.Context, i Invocation, s Selection, p planner.Plan, roots []planner.Configured) (typedindex.PackagePlan, []DocumentOutcome, []typedindex.UnitOutcome, error) {
	if len(p.Units) > typedindex.MaxBundleUnits || len(p.Targets) > typedindex.MaxBundleTargets || len(p.Documents) > planner.MaxDocuments {
		return typedindex.PackagePlan{}, nil, nil, typedindex.Capacity
	}
	if p.Version != planner.PlanV2 {
		return typedindex.PackagePlan{}, nil, nil, typedindex.Invalid
	}
	d := typedindex.PackagePlanDefinition{Schema: typedindex.PackagePlanSchema, ParentRequestDigest: i.Parent.Digest(), Targets: []typedindex.PlannedTarget{}, Units: []typedindex.PlannedUnit{}, Documents: []typedindex.PlannedDocument{}}
	remainingEdges := typedindex.MaxBundleEdges
	for _, t := range p.Targets {
		n := len(t.Inputs) + len(t.Units)
		if n > remainingEdges {
			return typedindex.PackagePlan{}, nil, nil, typedindex.Capacity
		}
		remainingEdges -= n
	}
	for _, u := range p.Units {
		if len(u.Imports) > remainingEdges {
			return typedindex.PackagePlan{}, nil, nil, typedindex.Capacity
		}
		remainingEdges -= len(u.Imports)
	}
	sources := map[string]typedindex.BundleFile{}
	for _, f := range i.Inventory.Files() {
		if strings.HasPrefix(f.Path, "source/") {
			sources[strings.TrimPrefix(f.Path, "source/")] = f
		}
	}
	docs := map[string]planner.Document{}
	rootUnits := map[string]bool{}
	testUnits := map[string]bool{}
	for _, t := range p.Targets {
		if t.Kind == "go_test" {
			for _, id := range t.Units {
				testUnits[id] = true
			}
		}
		if slices.Contains(roots, t.Configured) {
			for _, id := range t.Units {
				rootUnits[id] = true
			}
		}
	}
	for _, doc := range p.Documents {
		if e := ctx.Err(); e != nil {
			return typedindex.PackagePlan{}, nil, nil, e
		}
		if doc.Kind == "source" {
			f, ok := sources[doc.Path]
			if !ok || doc.Repository != "" || doc.ExecPath != doc.Path || f.Bytes != int64(doc.Bytes) || f.Digest != "sha256:"+doc.SHA256 {
				return typedindex.PackagePlan{}, nil, nil, typedindex.Stale
			}
		}
		docs[doc.ID] = doc
	}
	for _, t := range p.Targets {
		row := typedindex.PlannedTarget{ID: targetID(t.Configured), Dependencies: []string{}, Units: []typedindex.PackageUnitID{}, Test: t.Kind == "go_test"}
		for _, dep := range t.Inputs {
			row.Dependencies = append(row.Dependencies, targetID(dep))
		}
		for _, id := range t.Units {
			v, e := unitID(id)
			if e != nil {
				return typedindex.PackagePlan{}, nil, nil, e
			}
			row.Units = append(row.Units, v)
		}
		slices.Sort(row.Dependencies)
		slices.Sort(row.Units)
		d.Targets = append(d.Targets, row)
	}
	slices.SortFunc(d.Targets, func(a, b typedindex.PlannedTarget) int { return strings.Compare(a.ID, b.ID) })
	if identity(d.Targets) != identity(s.Targets) {
		return typedindex.PackagePlan{}, nil, nil, typedindex.Stale
	}
	var observations []DocumentOutcome
	var outcomes []typedindex.UnitOutcome
	for _, u := range p.Units {
		id, e := unitID(u.ID)
		if e != nil {
			return typedindex.PackagePlan{}, nil, nil, e
		}
		row := typedindex.PlannedUnit{ID: id, Imports: []typedindex.PackageUnitID{}, Documents: []string{}, Test: testUnits[u.ID]}
		state := typedindex.UnitComplete
		if row.Test {
			state = typedindex.UnitExcluded
		} else if u.Tool {
			state = typedindex.UnitUnsupported
		}
		for _, im := range u.Imports {
			v, e := unitID(im.Unit)
			if e != nil {
				return typedindex.PackagePlan{}, nil, nil, e
			}
			row.Imports = append(row.Imports, v)
		}
		if rootUnits[u.ID] && !row.Test {
			for _, docID := range u.CompiledGoFiles {
				doc, ok := docs[docID]
				if !ok {
					return typedindex.PackagePlan{}, nil, nil, typedindex.Invalid
				}
				base := launcher.ExecRoot
				switch doc.Kind {
				case "source":
					base = launcher.Workspace
				case "external":
					base = launcher.OutputBase
				case "generated":
				default:
					return typedindex.PackagePlan{}, nil, nil, typedindex.Invalid
				}
				rel, e := filepath.Rel(launcher.Workspace, path.Join(base, doc.ExecPath))
				if e != nil {
					return typedindex.PackagePlan{}, nil, nil, typedindex.Invalid
				}
				observation := DocumentOutcome{Document: doc.ID, Unit: id, RawPath: filepath.ToSlash(rel), State: "included"}
				planned := typedindex.PlannedDocument{Member: "main", Path: doc.Path, Unit: id, Bytes: int64(doc.Bytes), Digest: "sha256:" + doc.SHA256}
				switch doc.Kind {
				case "source":
					if doc.Repository != "" {
						return typedindex.PackagePlan{}, nil, nil, typedindex.Unsupported
					}
				case "generated":
					if i.Profile.Definition().Config.GeneratedDocuments == "omit" {
						observation.State = "generated_omitted"
					} else {
						planned.Path, e = typedindex.GeneratedPath(id, doc.Path)
						if e != nil {
							return typedindex.PackagePlan{}, nil, nil, e
						}
						planned.Generated = true
						planned.ProvenanceDigest = identity(doc)
					}
				case "external":
					observation.State = "external_unsupported"
					state = typedindex.UnitUnsupported
				}
				if observation.State == "included" {
					observation.Path = planned.Path
					d.Documents = append(d.Documents, planned)
					row.Documents = append(row.Documents, planned.Path)
				}
				observations = append(observations, observation)
			}
		}
		d.Units = append(d.Units, row)
		outcomes = append(outcomes, typedindex.UnitOutcome{Unit: id, State: state})
	}
	// Indexed reverse propagation visits each unit and dependency edge once.
	reverse := make(map[typedindex.PackageUnitID][]int, len(d.Units))
	queue := []int{}
	for j, u := range d.Units {
		for _, im := range u.Imports {
			reverse[im] = append(reverse[im], j)
		}
		if outcomes[j].State != typedindex.UnitComplete {
			queue = append(queue, j)
		}
	}
	for head := 0; head < len(queue); head++ {
		if e := ctx.Err(); e != nil {
			return typedindex.PackagePlan{}, nil, nil, e
		}
		for _, j := range reverse[d.Units[queue[head]].ID] {
			if outcomes[j].State == typedindex.UnitComplete {
				outcomes[j].State = typedindex.UnitUnsupported
				queue = append(queue, j)
			}
		}
	}
	sealed, e := typedindex.SealPackagePlan(ctx, i.Parent, d)
	return sealed, observations, outcomes, e
}
func samePlan(a, b typedindex.PackagePlan) bool {
	return a.Digest() == b.Digest() && string(a.Bytes()) == string(b.Bytes())
}
func selectionBytes(s Selection) []byte { b, _ := json.Marshal(s); return b }
