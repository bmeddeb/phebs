package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/bmeddeb/phebs/internal/typedimport"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/bmeddeb/phebs/internal/typedmodule"
)

const InputSelectionSchema = "phebs-typed-input-selection-v1"

// InputSelection is trusted installation authority, not a browser request.
// Plan has an empty ParentRequestDigest: embedding a request in its own input
// bundle would be circular. Binding fills that field only after admission.
// Module/import scope and the expected source/target/document map are sealed
// before the worker runs. Returned tool output cannot expand this map.
type InputSelection struct {
	Schema string                           `json:"schema"`
	Source typedindex.Source                `json:"source"`
	Module *typedmodule.ModuleSelection     `json:"module"`
	Import *typedimport.ImportSelection     `json:"import"`
	Plan   typedindex.PackagePlanDefinition `json:"plan"`
}

// InputTargetID names one explicit coverage selector within its complete
// discovery/import identity. Import provenance remains declared, not executed.
func InputTargetID(provider, scope, selector string) string {
	return identity([]string{provider, scope, selector})
}

func inputUnit(provider, scope, selector string) typedindex.PackageUnitID {
	u, _ := typedindex.NewPackageUnitID(InputTargetID(provider, scope, selector))
	return u
}

type inputBinding struct {
	controlDigest string
	selection     InputSelection
	plan          typedindex.PackagePlan
	packages      []inputPackage
}

type inputPackage struct {
	selector, importPath, directory string
	root                            int
	unit                            typedindex.PackageUnitID
}

// BindInputSelection preflights the authenticated inventory and expected map.
// It does no I/O or tool execution and grants no publication capability.
func BindInputSelection(ctx context.Context, i Invocation, raw []byte) (typedindex.PackagePlan, error) {
	b, err := bindInputSelection(ctx, i, raw)
	return b.plan, err
}

func bindInputSelection(ctx context.Context, i Invocation, raw []byte) (inputBinding, error) {
	var out inputBinding
	if err := i.validate(ctx); err != nil {
		return out, err
	}
	name, err := typedindex.SelectionFile(i.Parent.Request().Provider)
	if err != nil {
		return out, err
	}
	f, err := inventoryFile(i.Inventory, name)
	if err != nil || f.Executable || f.Bytes != int64(len(raw)) || f.Digest != hash(raw) {
		return out, typedindex.Unprepared
	}
	s, err := decode[InputSelection](raw, typedindex.MaxPlanBytes)
	if err != nil {
		return out, err
	}
	if s.Schema != InputSelectionSchema || s.Source != i.Parent.Request().Source || s.Plan.Schema != typedindex.PackagePlanSchema || s.Plan.ParentRequestDigest != "" {
		return out, typedindex.Stale
	}
	var selectors []string
	var scope string
	switch i.Parent.Request().Provider {
	case typedindex.ModuleProviderID:
		if s.Module == nil || s.Import != nil {
			return out, typedindex.Invalid
		}
		control, e := json.Marshal(s.Module)
		if e != nil {
			return out, typedindex.Invalid
		}
		discovered, e := typedmodule.DecodeModuleSelection(ctx, control)
		if e != nil {
			return out, e
		}
		selectors, scope = discovered.Packages, discovered.Digest()
		for _, selector := range selectors {
			p, e := resolveInputPackage(discovered, selector)
			if e != nil {
				return out, e
			}
			p.unit = inputUnit(typedindex.ModuleProviderID, scope, selector)
			for _, old := range out.packages {
				if old.importPath == p.importPath || old.directory == p.directory {
					return out, typedindex.Invalid
				}
			}
			out.packages = append(out.packages, p)
		}
		for p, digest := range discovered.Controls {
			row, e := inventoryFile(i.Inventory, "source/"+p)
			if e != nil || row.Executable || row.Bytes <= 0 || row.Bytes > typedmodule.MaxControlBytes || row.Digest != digest {
				return out, typedindex.Unprepared
			}
		}
	case typedindex.ImportProviderID:
		if s.Import == nil || s.Module != nil {
			return out, typedindex.Invalid
		}
		control, e := json.Marshal(s.Import)
		if e != nil {
			return out, typedindex.Invalid
		}
		imported, e := typedimport.DecodeImportSelection(ctx, control)
		if e != nil {
			return out, e
		}
		if imported.Source != s.Source || imported.Producer.Version != i.Profile.Definition().Tools.Indexer.Version || imported.Producer.Digest != i.Profile.Definition().Tools.Indexer.Digest {
			return out, typedindex.Stale
		}
		if len(imported.Artifacts) > typedindex.MaxSCIPMembers {
			return out, typedindex.Capacity
		}
		selectors, scope = imported.Coverage, imported.Digest()
		for _, artifact := range imported.Artifacts {
			row, e := inventoryFile(i.Inventory, artifact.Path)
			if e != nil || row.Executable || row.Bytes != artifact.Bytes || row.Digest != artifact.Digest {
				return out, typedindex.Unprepared
			}
		}
	default:
		return out, typedindex.Unsupported
	}
	if len(s.Plan.Targets) != len(selectors) || len(s.Plan.Units) != len(selectors) || len(s.Plan.Documents) == 0 {
		return out, typedindex.Invalid
	}
	units := make(map[typedindex.PackageUnitID]typedindex.PlannedUnit, len(s.Plan.Units))
	for _, u := range s.Plan.Units {
		units[u.ID] = u
	}
	wanted := make(map[string]typedindex.PackageUnitID, len(selectors))
	for _, selector := range selectors {
		wanted[InputTargetID(i.Parent.Request().Provider, scope, selector)] = inputUnit(i.Parent.Request().Provider, scope, selector)
	}
	for _, target := range s.Plan.Targets {
		unit, ok := wanted[target.ID]
		u, present := units[unit]
		if !ok || !present || target.Test || u.Test || len(target.Units) != 1 || target.Units[0] != unit {
			return out, typedindex.Invalid
		}
		deps := make([]string, 0, len(u.Imports))
		for _, id := range u.Imports {
			deps = append(deps, strings.TrimPrefix(string(id), "package-load:"))
		}
		slices.Sort(deps)
		if !slices.Equal(deps, target.Dependencies) {
			return out, typedindex.Invalid
		}
	}
	for _, document := range s.Plan.Documents {
		if document.Generated || document.ProvenanceDigest != "" {
			return out, typedindex.Unsupported
		}
		row, e := inventoryFile(i.Inventory, "source/"+document.Path)
		if e != nil || row.Bytes != document.Bytes || row.Digest != document.Digest {
			return out, typedindex.Unprepared
		}
		if s.Module != nil {
			found := false
			for _, p := range out.packages {
				if p.unit == document.Unit && path.Dir(document.Path) == p.directory && document.Member == inputSlot(p.root) && strings.HasSuffix(document.Path, ".go") && !strings.HasSuffix(document.Path, "_test.go") {
					found = true
				}
			}
			if !found {
				return out, typedindex.Invalid
			}
		} else {
			found := false
			for n := range s.Import.Artifacts {
				if document.Member == inputSlot(n) {
					found = true
				}
			}
			if !found {
				return out, typedindex.Invalid
			}
		}
	}
	def := s.Plan
	def.ParentRequestDigest = i.Parent.Digest()
	p, err := typedindex.SealPackagePlan(ctx, i.Parent, def)
	if err != nil {
		return out, err
	}
	var canonical typedindex.PackagePlanDefinition
	if json.Unmarshal(p.Bytes(), &canonical) != nil {
		return out, typedindex.Invalid
	}
	canonical.ParentRequestDigest = ""
	if identity(canonical) != identity(s.Plan) {
		return out, typedindex.Invalid
	}
	if i.Phase == typedindex.Execute && !bytes.Equal(p.Bytes(), i.Plan.Bytes()) {
		return out, typedindex.Stale
	}
	out.selection, out.plan, out.controlDigest = s, p, f.Digest
	return out, nil
}

func inputSlot(n int) string { return fmt.Sprintf("input-%d", n) }

func resolveInputPackage(s typedmodule.ModuleSelection, selector string) (inputPackage, error) {
	var selected inputPackage
	specificity := -1
	for n, root := range s.Roots {
		var relative string
		if strings.HasPrefix(selector, "./") {
			dir := strings.TrimPrefix(selector, "./")
			if root.Path == "." {
				relative = dir
			} else if dir == root.Path {
				relative = "."
			} else if strings.HasPrefix(dir, root.Path+"/") {
				relative = strings.TrimPrefix(dir, root.Path+"/")
			} else {
				continue
			}
		} else if selector == root.Module {
			relative = "."
		} else if strings.HasPrefix(selector, root.Module+"/") {
			relative = strings.TrimPrefix(selector, root.Module+"/")
		} else {
			continue
		}
		dir := path.Join(root.Path, relative)
		// A nested admitted module owns its directory; the outer module cannot
		// claim that package through a different spelling.
		shadowed := false
		for j, nested := range s.Roots {
			if j != n && (dir == nested.Path || strings.HasPrefix(dir, nested.Path+"/")) && (root.Path == "." || strings.HasPrefix(nested.Path, root.Path+"/")) {
				shadowed = true
			}
		}
		if shadowed {
			continue
		}
		importPath := root.Module
		if relative != "." {
			importPath += "/" + relative
		}
		weight := len(root.Module)
		if strings.HasPrefix(selector, "./") {
			weight = len(root.Path)
		}
		if weight > specificity {
			selected, specificity = inputPackage{selector: selector, importPath: importPath, directory: dir, root: n}, weight
		}
	}
	if specificity >= 0 {
		return selected, nil
	}
	return inputPackage{}, typedindex.Unsupported
}
