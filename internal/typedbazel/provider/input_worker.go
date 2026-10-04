package provider

import (
	"bytes"
	"context"
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"io"
	"path"
	"runtime"
	"slices"
	"strings"

	"github.com/bmeddeb/phebs/internal/typedbazel/launcher"
	"github.com/bmeddeb/phebs/internal/typedbazel/planner"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/bmeddeb/phebs/internal/typedmodule"
	"github.com/bmeddeb/phebs/internal/typedsandbox"
)

const inputResultSchema = "phebs-typed-input-result-v1"

// Only the selected go list projection crosses the worker boundary. All source
// paths, imports and document membership are checked against the presealed map.
type loadedInputPackage struct {
	Dir        string           `json:"Dir"`
	ImportPath string           `json:"ImportPath"`
	GoFiles    []string         `json:"GoFiles"`
	CgoFiles   []string         `json:"CgoFiles"`
	Imports    []string         `json:"Imports"`
	Incomplete bool             `json:"Incomplete"`
	Error      *json.RawMessage `json:"Error,omitempty"`
}

type inputResult struct {
	Schema        string                   `json:"schema"`
	RequestDigest string                   `json:"request_digest"`
	Phase         typedindex.Action        `json:"phase"`
	Plan          json.RawMessage          `json:"plan"`
	Packages      []loadedInputPackage     `json:"packages"`
	Members       []typedindex.MemberInput `json:"members"`
}

func inputRequest(i Invocation) string {
	if i.Phase == typedindex.Execute {
		return i.Execution.Digest()
	}
	return i.Parent.Digest()
}

type inputOperations struct {
	read        func(typedindex.Inventory, string, int64) ([]byte, error)
	tools       func(context.Context, Invocation) error
	materialize func(context.Context, Invocation) ([]original, error)
	verify      func(context.Context, planner.Plan, []original) error
	command     func(context.Context, string, []string, []string, *outputBudget, string) ([]byte, []byte, error)
	output      func(string, int64) ([]byte, error)
	quiesce     func() error
}

func nativeInputOperations() inputOperations {
	return inputOperations{readInventory, verifyInputTools, materializeSource, verifyWorkspace, runCommandAt, readBounded, ensureQuiescentWorker}
}

func runInput(ctx context.Context, i Invocation, o inputOperations) (raw []byte, err error) {
	// The sealed profile architecture is the executing host, as for Bazel commands.
	if i.Profile.Definition().Config.GOARCH != runtime.GOARCH {
		return nil, typedindex.Unsupported
	}
	name, e := typedindex.SelectionFile(i.Parent.Request().Provider)
	if e != nil {
		return nil, e
	}
	control, e := o.read(i.Inventory, name, typedindex.MaxPlanBytes)
	if e != nil {
		return nil, e
	}
	b, e := bindInputSelection(ctx, i, control)
	if e != nil {
		return nil, e
	}
	if e = o.tools(ctx, i); e != nil {
		return nil, e
	}
	defer func() {
		err = errors.Join(err, o.quiesce(), ctx.Err())
		if err != nil {
			raw = nil
		}
	}()
	result := inputResult{Schema: inputResultSchema, RequestDigest: inputRequest(i), Phase: i.Phase, Plan: b.plan.Bytes(), Packages: []loadedInputPackage{}, Members: []typedindex.MemberInput{}}
	if b.selection.Module != nil {
		originals, e := o.materialize(ctx, i)
		if e != nil {
			return nil, e
		}
		defer func() {
			err = errors.Join(err, o.verify(ctx, planner.Plan{}, originals))
			if err != nil {
				raw = nil
			}
		}()
		// Re-discover from the authenticated immutable controls. A stored
		// selection alone cannot fabricate a module name or workspace use set.
		m := b.selection.Module
		discovered, e := typedmodule.Discover(ctx, inputControlSource{i.Inventory, o.read}, m.Mode, m.Entry, m.Packages)
		if e != nil || discovered.Digest() != m.Digest() {
			return nil, typedindex.Stale
		}
		env := inputEnvironment(*m, i.Profile.Definition().Config.GOARCH)
		args := []string{"list", "-json", "-mod=readonly"}
		for _, p := range b.packages {
			args = append(args, p.importPath)
		}
		budget := &outputBudget{remaining: maxClientBytes}
		stdout, _, e := o.command(ctx, "/inputs/tools/go/bin/go", args, env, budget, inputDirectory(*m))
		if e != nil {
			return nil, e
		}
		result.Packages, e = decodeInputPackages(ctx, stdout)
		if e != nil {
			return nil, e
		}
		if e = verifyInputPackages(b, result.Packages); e != nil {
			return nil, e
		}
		if i.Phase == typedindex.Execute {
			for n := range m.Roots {
				patterns := inputPatterns(b, n)
				if len(patterns) == 0 {
					continue
				}
				argv := inputSCIPArguments(b, n)
				if _, _, e = o.command(ctx, SCIPPath, argv, env, budget, path.Join(launcher.Workspace, m.Roots[n].Path)); e != nil {
					return nil, e
				}
				member, e := o.output(inputOutput(n), typedindex.MaxSCIPMemberBytes)
				if e != nil {
					return nil, e
				}
				result.Members = append(result.Members, typedindex.MemberInput{Name: inputSlot(n), SCIP: member})
			}
		}
	} else if i.Phase == typedindex.Execute {
		for n, artifact := range b.selection.Import.Artifacts {
			member, e := o.read(i.Inventory, artifact.Path, typedindex.MaxSCIPMemberBytes)
			if e != nil {
				return nil, e
			}
			result.Members = append(result.Members, typedindex.MemberInput{Name: inputSlot(n), SCIP: member})
		}
	}
	if i.Phase == typedindex.Execute {
		if _, e := finalizeInput(ctx, i, b, result.Members); e != nil {
			return nil, e
		}
	}
	raw, e = json.Marshal(result)
	if e != nil {
		return nil, e
	}
	if int64(len(raw))+1 > int64(typedsandbox.OutputBytes)-i.Allowance.WorkerBytesUsed {
		return nil, typedindex.Capacity
	}
	return append(raw, '\n'), nil
}

type inputControlSource struct {
	inventory typedindex.Inventory
	read      func(typedindex.Inventory, string, int64) ([]byte, error)
}

func (s inputControlSource) Read(ctx context.Context, name string, limit int) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	row, err := inventoryFile(s.inventory, "source/"+name)
	if err != nil || row.Executable {
		return nil, typedindex.Unprepared
	}
	return s.read(s.inventory, row.Path, int64(limit))
}

func verifyInputTools(ctx context.Context, i Invocation) error {
	p := i.Profile.Definition()
	roles := []struct {
		name, digest, main string
		typed              bool
	}{{typedindex.ManagedHelperFile, p.Tools.Launcher.Digest, productionHelperMain, false}}
	if p.Provider == typedindex.ModuleProviderID {
		goSDK, scip, _, ok := NativeToolDigests(p.Config.GOARCH)
		if !ok || p.Tools.Go.Digest != goSDK || p.Tools.Indexer.Digest != scip {
			return typedindex.Unsupported
		}
		roles = append(roles, struct {
			name, digest, main string
			typed              bool
		}{"tools/go/bin/go", p.Tools.Go.Digest, "cmd/go", false}, struct {
			name, digest, main string
			typed              bool
		}{"tools/bin/scip-go", p.Tools.Indexer.Digest, "github.com/scip-code/scip-go/cmd/scip-go", true})
	}
	for _, role := range roles {
		row, err := inventoryFile(i.Inventory, role.name)
		if err != nil || !row.Executable || row.Digest != role.digest || row.Bytes <= 0 {
			return typedindex.Unprepared
		}
		if _, err = readInventory(i.Inventory, row.Path, typedindex.MaxFileBytes); err != nil {
			return err
		}
		info, err := buildinfo.ReadFile("/inputs/" + row.Path)
		if err != nil || checkBuild(info, role.main, role.typed) != nil {
			return typedindex.Unsupported
		}
	}
	return ctx.Err()
}

func inputDirectory(m typedmodule.ModuleSelection) string {
	if m.Mode == typedmodule.ModeSingle {
		return path.Join(launcher.Workspace, m.Roots[0].Path)
	}
	return path.Join(launcher.Workspace, path.Dir(m.Entry))
}

func inputEnvironment(m typedmodule.ModuleSelection, arch string) []string {
	work := "off"
	if m.Mode == typedmodule.ModeWorkspace {
		work = path.Join(launcher.Workspace, m.Entry)
	}
	variant := "GOARM64=v8.0"
	if arch == "amd64" {
		variant = "GOAMD64=v1"
	}
	return []string{"HOME=/scratch/home", "GOOS=linux", "GOARCH=" + arch, variant, "CGO_ENABLED=0", "PATH=/inputs/tools/go/bin:/inputs/tools/bin:/usr/bin:/bin", "GOROOT=/inputs/tools/go", "TMPDIR=/scratch/tmp", "GOCACHE=/scratch/cache/go-build", "GOMODCACHE=/inputs/tools/modcache", "GOPROXY=off", "GOSUMDB=off", "GOTOOLCHAIN=local", "GOTELEMETRY=off", "GOENV=off", "GOFLAGS=-mod=readonly", "GOWORK=" + work, "GOPACKAGESDRIVER=off"}
}

func inputOutput(n int) string { return "/scratch/" + inputSlot(n) + ".scip" }

func inputPatterns(b inputBinding, n int) []string {
	var out []string
	for _, p := range b.packages {
		if p.root == n {
			out = append(out, p.importPath)
		}
	}
	return out
}

func inputSCIPArguments(b inputBinding, n int) []string {
	r := b.selection.Module.Roots[n]
	return append([]string{"index", "--module-root=" + path.Join(launcher.Workspace, r.Path), "--module-path=" + r.Module, "--module-version=" + b.selection.Source.Commit, "--repository-remote=https://" + b.selection.Source.Repository, "--go-version=go1.25.0", "--skip-tests", "--skip-implementations", "--output=" + inputOutput(n)}, inputPatterns(b, n)...)
}

func decodeInputPackages(ctx context.Context, raw []byte) ([]loadedInputPackage, error) {
	if len(raw) == 0 || len(raw) > maxClientBytes {
		return nil, typedindex.Capacity
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	var out []loadedInputPackage
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var p loadedInputPackage
		if err := d.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, typedindex.Invalid
		}
		if len(out) >= typedindex.MaxBundleUnits || len(p.GoFiles) > typedindex.MaxBundleDocuments || len(p.Imports) > typedindex.MaxBundleEdges {
			return nil, typedindex.Capacity
		}
		out = append(out, p)
	}
	return out, nil
}

func verifyInputPackages(b inputBinding, packages []loadedInputPackage) error {
	if len(packages) != len(b.packages) || len(packages) > typedindex.MaxBundleUnits {
		return typedindex.Stale
	}
	byImport := make(map[string]inputPackage, len(b.packages))
	for _, p := range b.packages {
		byImport[p.importPath] = p
	}
	seen := make(map[string]bool, len(packages))
	files, edges := 0, 0
	units := make(map[typedindex.PackageUnitID]typedindex.PlannedUnit, len(b.selection.Plan.Units))
	for _, u := range b.selection.Plan.Units {
		units[u.ID] = u
	}
	for _, loaded := range packages {
		if len(loaded.GoFiles) > typedindex.MaxBundleDocuments-files || len(loaded.Imports) > typedindex.MaxBundleEdges-edges {
			return typedindex.Capacity
		}
		files += len(loaded.GoFiles)
		edges += len(loaded.Imports)
		p, ok := byImport[loaded.ImportPath]
		if !ok || seen[loaded.ImportPath] || loaded.Incomplete || loaded.Error != nil || len(loaded.CgoFiles) != 0 || loaded.Dir != path.Join(launcher.Workspace, p.directory) {
			return typedindex.Stale
		}
		seen[loaded.ImportPath] = true
		var documents []string
		for _, file := range loaded.GoFiles {
			if !safeRelative(file) || path.Base(file) != file {
				return typedindex.Invalid
			}
			documents = append(documents, path.Join(p.directory, file))
		}
		slices.Sort(documents)
		if !slices.Equal(documents, units[p.unit].Documents) {
			return typedindex.Stale
		}
		var imports []typedindex.PackageUnitID
		seenImports := map[string]bool{}
		for _, imported := range loaded.Imports {
			if seenImports[imported] {
				return typedindex.Invalid
			}
			seenImports[imported] = true
			if selected, ok := byImport[imported]; ok {
				imports = append(imports, selected.unit)
				continue
			}
			for _, root := range b.selection.Module.Roots {
				if imported == root.Module || strings.HasPrefix(imported, root.Module+"/") {
					return typedindex.Stale
				}
			}
		}
		slices.Sort(imports)
		if !slices.Equal(imports, units[p.unit].Imports) {
			return typedindex.Stale
		}
	}
	return nil
}
