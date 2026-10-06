package planner

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestNativeProjectionSelectsArchitectureSources(t *testing.T) {
	if len(nativeGoProfiles) != 2 {
		t.Fatal("native architecture set changed")
	}
	for _, arch := range []string{"arm64", "amd64", "386"} {
		t.Run(arch, func(t *testing.T) {
			const owner = "@@//pkg:pkg"
			a := Archive{Name: "pkg", Label: owner, Export: "bazel-out/cfg/bin/pkg/pkg.x", ImportPath: "example.test/pkg", GOOS: "linux", GOARCH: arch, Imports: []Import{}, Tags: []string{}}
			files := map[string][]byte{}
			for _, sourceArch := range []string{"arm64", "amd64"} {
				name := "pkg/pkg_" + sourceArch + ".go"
				a.Sources = append(a.Sources, Artifact{Path: name, ShortPath: name, Owner: owner, Source: true})
				files[name] = []byte("package pkg\n")
			}
			input, err := json.Marshal(helperInput{Version: "phebs-t451a-projection-input-v1", Owner: owner, Roots: []string{a.Export}, Archives: []Archive{a}})
			if err != nil {
				t.Fatal(err)
			}
			data, err := project(input, func(name string, limit int) ([]byte, error) {
				data, ok := files[name]
				if !ok || len(data) > limit {
					return nil, errors.New("undeclared or oversized projection read")
				}
				return data, nil
			})
			if arch == "386" {
				if err == nil {
					t.Fatal("unsupported projection architecture accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var result projection
			if err := json.Unmarshal(data, &result); err != nil {
				t.Fatal(err)
			}
			if len(result.Archives) != 1 {
				t.Fatalf("unexpected projection: %+v", result)
			}
			got := result.Archives[0]
			if got.Mode.GOARCH != arch || len(got.GoFiles) != 2 || len(got.CompiledGoFiles) != 1 || got.CompiledGoFiles[0].Path != "pkg/pkg_"+arch+".go" {
				t.Fatalf("source selection: %+v", got)
			}
		})
	}
}
