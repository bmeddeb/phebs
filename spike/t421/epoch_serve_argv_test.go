package t421

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"slices"
	"testing"
)

// Source binding to actual shared callers and the unchanged ordinary builder,
// not native Start or profile admission
// evidence. The real CLI parser is exercised separately in cmd/phebs.
func TestT422ServeBuildersMatchFrozenArgv(t *testing.T) {
	var frozen []string
	for _, command := range frozenExecutionCommands() {
		if command.Name == "serve" {
			frozen = command.NormalizedArgv
		}
	}
	if !slices.Equal(frozen, []string{"serve", "-config", "@config"}) {
		t.Fatal("frozen serve argv changed", frozen)
	}
	for _, test := range []struct {
		path, function, builder string
		arguments               []string
	}{
		{"epoch_launch.go", "launchEpoch", "executionPhebsCommand", []string{"path", "author.parent", `"serve"`, "epoch", "environment"}},
		{"epoch_backup.go", "runNativeArchive", "executionPhebsCommand", []string{"path", "author.parent", "verb", "run.epoch", "environment"}},
		{"production_custody_run.go", "StartServe", "exec.Command", []string{"custody.phebsPath", `"serve"`, `"-config"`, "custody.configPath"}},
	} {
		t.Run(test.function, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, test.path, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			render := func(node ast.Node) string {
				t.Helper()
				var value bytes.Buffer
				if err := format.Node(&value, fset, node); err != nil {
					t.Fatal(err)
				}
				return value.String()
			}
			seen := 0
			for _, declaration := range file.Decls {
				function, ok := declaration.(*ast.FuncDecl)
				if !ok || function.Name.Name != test.function {
					continue
				}
				ast.Inspect(function.Body, func(node ast.Node) bool {
					call, ok := node.(*ast.CallExpr)
					if !ok || render(call.Fun) != test.builder {
						return true
					}
					seen++
					want := test.arguments
					var got []string
					for _, argument := range call.Args {
						got = append(got, render(argument))
					}
					if call.Ellipsis.IsValid() || !slices.Equal(got, want) {
						t.Errorf("actual fixed builder argv = %v, want %v", got, want)
					}
					return true
				})
			}
			if seen != 1 {
				t.Fatal("actual fixed builder not uniquely covered", seen)
			}
		})
	}
}
