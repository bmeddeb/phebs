//go:build linux

package typedexecutor

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/typedbazel/provider"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/bmeddeb/phebs/internal/typedmodule"
)

// This fixed 176-byte workspace supplies no target or service admission.
func nativeInputWorkspaceControls() inputNativeControls {
	return inputNativeControls{
		"go.work":  []byte("go 1.25.0\nuse (\n ./a\n ./b\n)\n"),
		"a/go.mod": []byte("module example.test/a\ngo 1.25.0\n"),
		"b/go.mod": []byte("module example.test/b\ngo 1.25.0\n"),
		"a/a.go":   []byte("package a\nimport \"example.test/b\"\nvar Answer = b.Answer\n"),
		"b/b.go":   []byte("package b\nconst Answer = 42\n"),
	}
}

func acceptanceWorkspaceSourceFiles() []typedindex.BundleFile {
	files := make([]typedindex.BundleFile, 0, 5)
	for name, raw := range nativeInputWorkspaceControls() {
		files = append(files, typedindex.BundleFile{Path: "source/" + name, Bytes: int64(len(raw)), Digest: acceptanceDigest(raw)})
	}
	slices.SortFunc(files, func(a, b typedindex.BundleFile) int { return strings.Compare(a.Path, b.Path) })
	return files
}

func acceptanceWorkspaceSources(inventory typedindex.Inventory) bool {
	expected := acceptanceWorkspaceSourceFiles()
	actual := make([]typedindex.BundleFile, 0, len(expected))
	for _, file := range inventory.Files() {
		if strings.HasPrefix(file.Path, "source/") {
			if len(actual) == len(expected) {
				return false
			}
			actual = append(actual, file)
		}
	}
	return reflect.DeepEqual(actual, expected)
}

func acceptanceWorkspaceSelection(ctx context.Context, source typedindex.Source) (provider.InputSelection, error) {
	files := nativeInputWorkspaceControls()
	selectors := []string{"example.test/a", "example.test/b"}
	module, err := typedmodule.Discover(ctx, files, typedmodule.ModeWorkspace, "go.work", selectors)
	if err != nil {
		return provider.InputSelection{}, err
	}
	selection := provider.InputSelection{Schema: provider.InputSelectionSchema, Source: source, Module: &module, Plan: typedindex.PackagePlanDefinition{Schema: typedindex.PackagePlanSchema}}
	targets := []string{provider.InputTargetID(typedindex.ModuleProviderID, module.Digest(), selectors[0]), provider.InputTargetID(typedindex.ModuleProviderID, module.Digest(), selectors[1])}
	units := make([]typedindex.PackageUnitID, len(targets))
	for n, target := range targets {
		units[n], err = typedindex.NewPackageUnitID(target)
		if err != nil {
			return provider.InputSelection{}, err
		}
	}
	for n, path := range []string{"a/a.go", "b/b.go"} {
		dependencies, imports := []string{}, []typedindex.PackageUnitID{}
		if n == 0 {
			dependencies = append(dependencies, targets[1])
			imports = append(imports, units[1])
		}
		selection.Plan.Targets = append(selection.Plan.Targets, typedindex.PlannedTarget{ID: targets[n], Dependencies: dependencies, Units: []typedindex.PackageUnitID{units[n]}})
		selection.Plan.Units = append(selection.Plan.Units, typedindex.PlannedUnit{ID: units[n], Imports: imports, Documents: []string{path}})
		selection.Plan.Documents = append(selection.Plan.Documents, typedindex.PlannedDocument{Member: "input-" + string(rune('0'+n)), Path: path, Unit: units[n], Bytes: int64(len(files[path])), Digest: acceptanceDigest(files[path])})
	}
	slices.SortFunc(selection.Plan.Targets, func(a, b typedindex.PlannedTarget) int { return strings.Compare(a.ID, b.ID) })
	slices.SortFunc(selection.Plan.Units, func(a, b typedindex.PlannedUnit) int { return strings.Compare(string(a.ID), string(b.ID)) })
	return selection, nil
}

func acceptanceWorkspaceProfile(ctx context.Context, c nativeAcceptanceConfig, profile typedindex.Profile, raw, selectionRaw []byte) error {
	config, err := typedindex.InputConfig(typedindex.ModuleProviderID, runtime.GOARCH)
	if err != nil {
		return err
	}
	goSDK, scip, _, ok := provider.NativeToolDigests(runtime.GOARCH)
	if !ok {
		return typedindex.Unsupported
	}
	helper := typedindex.Tool{Version: "neutral-t45.7", Digest: c.HelperSHA256}
	tools := typedindex.Tools{Planner: helper, Launcher: helper, Go: typedindex.Tool{Version: "1.25.0", Digest: goSDK}, Indexer: typedindex.Tool{Version: "0.2.7", Digest: scip}}
	d := profile.Definition()
	if acceptanceDigest(raw) != c.ProfileSHA256 || d.Schema != typedindex.InputProfileSchema || d.Name != "neutral-input" || d.Provider != typedindex.ModuleProviderID || d.Config != config || d.Policy != c.Policy || d.BundleDigest != c.InventorySHA256 || d.ImageDigest != c.ImageSHA256 || d.Tools != tools || d.RCDigest != "" {
		return errors.New("workspace profile differs from approved tools/policy")
	}
	var selection provider.InputSelection
	if err := acceptanceDecode(selectionRaw, provider.MaxSelectionBytes, &selection); err != nil {
		return err
	}
	expected, err := acceptanceWorkspaceSelection(ctx, c.Source)
	if err != nil {
		return err
	}
	if acceptanceDigest(selectionRaw) != c.SelectionSHA256 || !reflect.DeepEqual(selection, expected) || acceptanceDigest(acceptanceJSON(selection.Plan.Targets)) != c.UniverseSHA256 {
		return errors.New("neutral workspace selection")
	}
	return nil
}

func TestNativeWorkspaceFaultSourceInventory(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func([]typedindex.BundleFile) []typedindex.BundleFile
		want   bool
	}{{"exact", func(files []typedindex.BundleFile) []typedindex.BundleFile { return files }, true}, {"missing", func(files []typedindex.BundleFile) []typedindex.BundleFile { return files[1:] }, false}, {"extra", func(files []typedindex.BundleFile) []typedindex.BundleFile {
		return append(files, typedindex.BundleFile{Path: "source/private.go", Bytes: 1, Digest: acceptanceDigest([]byte("x"))})
	}, false}, {"changed", func(files []typedindex.BundleFile) []typedindex.BundleFile {
		files[0].Digest = acceptanceDigest([]byte("different source"))
		return files
	}, false}, {"executable", func(files []typedindex.BundleFile) []typedindex.BundleFile {
		files[0].Executable = true
		return files
	}, false}, {"wrong-path", func(files []typedindex.BundleFile) []typedindex.BundleFile {
		files[0].Path = "tools/a/a.go"
		return files
	}, false}} {
		t.Run(tc.name, func(t *testing.T) {
			files := tc.change(acceptanceWorkspaceSourceFiles())
			slices.SortFunc(files, func(a, b typedindex.BundleFile) int { return strings.Compare(a.Path, b.Path) })
			raw := acceptanceJSON(typedindex.InventoryDefinition{Schema: typedindex.InventorySchema, Files: files})
			inventory, err := typedindex.DecodeInventory(t.Context(), raw, acceptanceDigest(raw))
			if err != nil {
				t.Fatal(err)
			}
			if acceptanceWorkspaceSources(inventory) != tc.want {
				t.Fatal("workspace source contract")
			}
		})
	}
}

func TestNativeWorkspaceFaultProfile(t *testing.T) {
	h := acceptanceDigest([]byte("fixed"))
	source := typedindex.Source{Repository: acceptanceRepo, Incarnation: "fixture", Generation: h, Commit: strings.Repeat("a", 40)}
	config, err := typedindex.InputConfig(typedindex.ModuleProviderID, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	goSDK, scip, _, _ := provider.NativeToolDigests(runtime.GOARCH)
	helper := typedindex.Tool{Version: "neutral-t45.7", Digest: h}
	definition := typedindex.ProfileDefinition{Schema: typedindex.InputProfileSchema, Name: "neutral-input", Provider: typedindex.ModuleProviderID, Tools: typedindex.Tools{Planner: helper, Launcher: helper, Go: typedindex.Tool{Version: "1.25.0", Digest: goSDK}, Indexer: typedindex.Tool{Version: "0.2.7", Digest: scip}}, Config: config, Policy: typedindex.MeasuredPolicy(), BundleDigest: h, ImageDigest: h}
	for _, tc := range []struct {
		name   string
		change func(*nativeAcceptanceConfig, *typedindex.ProfileDefinition, *provider.InputSelection)
		want   bool
	}{{"exact", func(*nativeAcceptanceConfig, *typedindex.ProfileDefinition, *provider.InputSelection) {}, true}, {"helper", func(_ *nativeAcceptanceConfig, d *typedindex.ProfileDefinition, _ *provider.InputSelection) {
		d.Tools.Planner.Digest, d.Tools.Launcher.Digest = acceptanceDigest([]byte("other helper")), acceptanceDigest([]byte("other helper"))
	}, false}, {"go-pin", func(_ *nativeAcceptanceConfig, d *typedindex.ProfileDefinition, _ *provider.InputSelection) {
		d.Tools.Go.Digest = h
	}, false}, {"name", func(_ *nativeAcceptanceConfig, d *typedindex.ProfileDefinition, _ *provider.InputSelection) {
		d.Name = "target-input"
	}, false}, {"image", func(_ *nativeAcceptanceConfig, d *typedindex.ProfileDefinition, _ *provider.InputSelection) {
		d.ImageDigest = acceptanceDigest([]byte("other image"))
	}, false}, {"bundle", func(_ *nativeAcceptanceConfig, d *typedindex.ProfileDefinition, _ *provider.InputSelection) {
		d.BundleDigest = acceptanceDigest([]byte("other bundle"))
	}, false}, {"bazel", func(_ *nativeAcceptanceConfig, d *typedindex.ProfileDefinition, _ *provider.InputSelection) {
		d.Tools.Bazel = helper
	}, false}, {"policy", func(c *nativeAcceptanceConfig, _ *typedindex.ProfileDefinition, _ *provider.InputSelection) {
		c.Policy.WallSeconds++
	}, false}, {"legacy-schema", func(c *nativeAcceptanceConfig, _ *typedindex.ProfileDefinition, _ *provider.InputSelection) {
		c.Schema = acceptanceSchema
	}, false}, {"source", func(_ *nativeAcceptanceConfig, _ *typedindex.ProfileDefinition, s *provider.InputSelection) {
		s.Source.Commit = strings.Repeat("b", 40)
	}, false}, {"module", func(_ *nativeAcceptanceConfig, _ *typedindex.ProfileDefinition, s *provider.InputSelection) {
		s.Module = nil
	}, false}, {"parent", func(_ *nativeAcceptanceConfig, _ *typedindex.ProfileDefinition, s *provider.InputSelection) {
		s.Plan.ParentRequestDigest = h
	}, false}, {"document", func(_ *nativeAcceptanceConfig, _ *typedindex.ProfileDefinition, s *provider.InputSelection) {
		s.Plan.Documents[0].Digest = h
	}, false}, {"extra-target", func(_ *nativeAcceptanceConfig, _ *typedindex.ProfileDefinition, s *provider.InputSelection) {
		s.Plan.Targets = append(s.Plan.Targets, s.Plan.Targets[0])
	}, false}, {"universe", func(c *nativeAcceptanceConfig, _ *typedindex.ProfileDefinition, _ *provider.InputSelection) {
		c.UniverseSHA256 = h
	}, false}} {
		t.Run(tc.name, func(t *testing.T) {
			selection, err := acceptanceWorkspaceSelection(t.Context(), source)
			if err != nil {
				t.Fatal(err)
			}
			c := nativeAcceptanceConfig{Schema: acceptanceWorkspaceFaultSchema, Source: source, HelperSHA256: h, InventorySHA256: h, ImageSHA256: h, Policy: typedindex.MeasuredPolicy()}
			d := definition
			tc.change(&c, &d, &selection)
			raw, selectionRaw := acceptanceJSON(d), acceptanceJSON(selection)
			// Altered controls get coherent hashes so refusal tests exercise the
			// closed semantic contract rather than just mismatched digest fields.
			c.ProfileSHA256, c.SelectionSHA256 = acceptanceDigest(raw), acceptanceDigest(selectionRaw)
			if tc.name != "universe" {
				c.UniverseSHA256 = acceptanceDigest(acceptanceJSON(selection.Plan.Targets))
			}
			_, err = acceptanceProfile(t.Context(), c, raw, selectionRaw)
			if (err == nil) != tc.want {
				t.Fatal("workspace profile contract", err)
			}
		})
	}
}
