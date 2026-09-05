package t421

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/constant"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"math"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

func TestControlledDispatchBudgetsExactOperationalInventory(t *testing.T) {
	profile := retainedWorkPlan(t).Profile
	before, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	budgets, err := controlledDispatchBudgets(profile)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		phase                                       string
		git, phebs, surreal, author, zoekt, hdiutil uint64
		total                                       uint64
	}{
		{"preflight", 0, 0, 0, 0, 0, 0, 0},
		{"cold", 151_874, 1, 2, 1, 3, 0, 151_881},
		{"warm_noop", 401, 0, 0, 0, 0, 0, 401},
		{"physical_delta_b", 151_850, 0, 0, 1, 3, 0, 151_854},
		{"logical_delta_b", 4_852, 1, 2, 0, 0, 0, 4_855},
		{"return_a", 151_876, 1, 2, 1, 3, 0, 151_883},
		{"stale_lease", 4_801, 0, 0, 0, 0, 0, 4_801},
		{"process_restart", 4_851, 1, 2, 0, 0, 0, 4_854},
		{"pressure_80", 401, 0, 0, 0, 0, 0, 401},
		{"pressure_90", 401, 0, 0, 0, 0, 0, 401},
		{"pressure_75", 401, 0, 0, 0, 0, 0, 401},
		{"archive_restore", 69_946, 3, 10, 0, 0, 0, 69_959},
		{"lifecycle_collection", 4_801, 0, 0, 0, 0, 0, 4_801},
		{"product_queries", 401, 0, 0, 0, 0, 0, 401},
		{"teardown", 301, 0, 0, 0, 0, 1, 302},
	}
	if len(budgets) != len(want) {
		t.Fatalf("phase count = %d, want %d", len(budgets), len(want))
	}
	var total uint64
	for index, expected := range want {
		budget := budgets[index]
		wantRoles := []RoleBound{
			{Name: "compatibility"}, {Name: "git", Maximum: expected.git},
			{Name: "hdiutil", Maximum: expected.hdiutil}, {Name: "phebs", Maximum: expected.phebs},
			{Name: "phebs-focused-index"}, {Name: "surreal", Maximum: expected.surreal},
			{Name: "t422-author", Maximum: expected.author}, {Name: "zoekt-git-index", Maximum: expected.zoekt},
		}
		if budget.Phase != expected.phase || budget.MaximumAttempts != expected.total || !reflect.DeepEqual(budget.Roles, wantRoles) {
			t.Fatalf("phase %s budget = %+v", expected.phase, budget)
		}
		var termSum uint64
		for _, term := range budget.Terms {
			if strings.HasPrefix(term.Name, "native_") || term.Role == "git-transport-shell" ||
				term.MaximumAttempts != term.Units*term.AttemptsPerUnit*term.MaximumUnitAttempts {
				t.Fatalf("non-dispatch or inconsistent term: %+v", term)
			}
			termSum += term.MaximumAttempts
		}
		if termSum != expected.total {
			t.Fatalf("%s term total = %d, want %d", expected.phase, termSum, expected.total)
		}
		total += budget.MaximumAttempts
	}
	if total != 547_195 {
		t.Fatalf("operational maximum = %d", total)
	}
	raw, err := json.Marshal(budgets)
	if err != nil || bytes.Contains(raw, []byte("children")) || bytes.Contains(raw, []byte("child_process")) {
		t.Fatalf("dispatch JSON retained legacy child semantics: %v", err)
	}
	after, err := json.Marshal(profile)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("budget derivation mutated the input profile")
	}
	budgets[0].Roles[0].Name = "changed"
	budgets[1].Terms[0].Name = "changed"
	fresh, err := controlledDispatchBudgets(profile)
	if err != nil || fresh[0].Roles[0].Name != "compatibility" || fresh[1].Terms[0].Name != "resolver_blob_materialization" {
		t.Fatal("budget results share caller-mutable state")
	}
}

func TestControlledDispatchBudgetsRejectInvalidProfile(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*CombinedProfile)
	}{
		{"empty population", func(profile *CombinedProfile) { profile.Physical.CombinedRegularFiles = 0 }},
		{"regular overflow", func(profile *CombinedProfile) { profile.Physical.CombinedRegularFiles = math.MaxUint64 }},
		{"resolver overflow", func(profile *CombinedProfile) { profile.Pipeline.ResolverBlobReadsPerBuild = math.MaxUint64 }},
		{"negative caller population", func(profile *CombinedProfile) {
			profile.Pipeline.GeneratedMappings = profile.Pipeline.SupportedGoFiles + 1
		}},
		{"typed source overflow", func(profile *CombinedProfile) { profile.Pipeline.ExtractionDomains[0].TypedPartitions = math.MaxUint64 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			profile := retainedWorkPlan(t).Profile
			test.mutate(&profile)
			if result, err := controlledDispatchBudgets(profile); err == nil || result != nil {
				t.Fatal("invalid profile issued partial or accepted dispatch budgets")
			}
		})
	}
}

func TestDispatchBudgetTermRejectsOverflowAndForgedTotals(t *testing.T) {
	for _, values := range [][3]uint64{{math.MaxUint64, 2, 1}, {math.MaxUint64, 1, 2}, {1, 0, 1}, {1, 1, 0}} {
		if _, err := dispatchBudgetTerm("git", "test", "unit", values[0], values[1], values[2]); err == nil {
			t.Fatalf("invalid product accepted: %v", values)
		}
	}
	for _, tag := range []string{"role", "name", "unit"} {
		values := []string{"git", "test", "unit"}
		values[slices.Index([]string{"role", "name", "unit"}, tag)] = ""
		if _, err := dispatchBudgetTerm(values[0], values[1], values[2], 1, 1, 1); err == nil {
			t.Fatalf("empty %s accepted", tag)
		}
	}
	maximum, err := dispatchBudgetTerm("git", "maximum", "unit", math.MaxUint64, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	one, err := dispatchBudgetTerm("surreal", "one", "unit", 1, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	budget := PhaseDispatchBudget{Roles: []RoleBound{{Name: "git"}, {Name: "surreal"}}}
	if err := budget.addTerm(maximum); err != nil {
		t.Fatal(err)
	}
	if err := budget.addTerm(one); err == nil {
		t.Fatal("overflowing phase sum accepted")
	}
	for _, test := range []struct {
		name string
		term DispatchBudgetTerm
	}{
		{"duplicate", maximum},
		{"unknown role", DispatchBudgetTerm{Name: "unknown", Role: "unknown", Unit: "unit", Units: 1, AttemptsPerUnit: 1, MaximumUnitAttempts: 1, MaximumAttempts: 1}},
		{"forged total", DispatchBudgetTerm{Name: "forged", Role: "git", Unit: "unit", Units: 1, AttemptsPerUnit: 1, MaximumUnitAttempts: 1, MaximumAttempts: 0}},
	} {
		t.Run(test.name, func(t *testing.T) {
			selected := PhaseDispatchBudget{Roles: []RoleBound{{Name: "git"}, {Name: "surreal"}}}
			if test.name == "duplicate" {
				selected.Terms = []DispatchBudgetTerm{maximum}
			}
			if err := selected.addTerm(test.term); err == nil {
				t.Fatal("invalid term accepted")
			}
		})
	}
}

func TestProductionDispatchSitesMatchActualBoundaries(t *testing.T) {
	sites := productionDispatchSites()
	if len(sites) != 16 {
		t.Fatalf("production dispatch sites = %d, want 16", len(sites))
	}
	var prior string
	var gitSites, surrealSites int
	want := make(map[string]int, len(sites)+2)
	wantIDs := make(map[string]uint64, len(sites))
	wired := dispatchadmission.ProductionSites()
	if len(wired) != len(sites) {
		t.Fatal("production bootstrap site count differs from source inventory")
	}
	for index, site := range sites {
		if site.Tag <= prior || !slices.IsSorted(site.Roles) || len(site.Roles) == 0 {
			t.Fatalf("site inventory is not closed and sorted: %+v", site)
		}
		prior = site.Tag
		if slices.Contains(site.Roles, "git") {
			gitSites++
		}
		if slices.Contains(site.Roles, "surreal") {
			surrealSites++
		}
		want[site.Path+":"+site.Callsite]++
		wantIDs[site.Path+":"+site.Callsite] = uint64(wired[index].ID)
	}
	if gitSites != 11 || surrealSites != 3 {
		t.Fatalf("site role counts git=%d surreal=%d", gitSites, surrealSites)
	}
	// Count both the owned site adapters and their finite internal forwarding
	// boundaries; do not hide the admission package from the launch inventory.
	want["internal/dispatchadmission/client.go:(*Client).Start"] = 2
	want["internal/dispatchadmission/client.go:(*Client).Run"] = 2
	want["internal/dispatchadmission/production.go:StartProduction"] = 1
	want["internal/dispatchadmission/production.go:startProductionCommand"] = 1
	want["internal/dispatchadmission/production.go:RunProduction"] = 2
	want["internal/dispatchadmission/production.go:CombinedOutputProduction"] = 2
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := packages.Load(&packages.Config{
		Context: t.Context(), Dir: root,
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo,
	}, "./internal/...", "./cmd/...")
	if err != nil || len(loaded) == 0 {
		t.Fatalf("independent production package inventory: %v", err)
	}
	got := make(map[string]int)
	for _, pkg := range loaded {
		if len(pkg.Errors) != 0 || pkg.TypesInfo == nil {
			t.Fatalf("production package %s is not type-complete: %v", pkg.PkgPath, pkg.Errors)
		}
		for _, file := range pkg.Syntax {
			path, err := filepath.Rel(root, pkg.Fset.Position(file.Pos()).Filename)
			if err != nil {
				t.Fatal(err)
			}
			for boundary, count := range typedDispatchBoundaries(file, pkg.TypesInfo) {
				got[filepath.ToSlash(path)+":"+boundary] += count
			}
			verifyProductionSiteArguments(t, filepath.ToSlash(path), file, pkg.TypesInfo, wantIDs)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("complete production launch boundary inventory differs:\nobserved=%v\nexpected=%v", got, want)
	}
	sites[0].Roles[0] = "changed"
	if productionDispatchSites()[0].Roles[0] != "git" {
		t.Fatal("site role inventory shares caller-mutable state")
	}
}

// Count resolved launch-method references, including bound method values. A
// reference in a new function/file cannot evade the inventory merely because
// its containing function is not already listed. Receiver types distinguish
// exec.Cmd.Start from parser positions, job runners and package Output helpers.
func typedDispatchBoundaries(file *ast.File, info *types.Info) map[string]int {
	result := make(map[string]int)
	for _, declaration := range file.Decls {
		boundary := "package-initialization"
		if function, ok := declaration.(*ast.FuncDecl); ok {
			boundary = function.Name.Name
			if object, ok := info.Defs[function.Name].(*types.Func); ok {
				if receiver := object.Type().(*types.Signature).Recv(); receiver != nil {
					boundary = "(" + types.TypeString(receiver.Type(), func(*types.Package) string { return "" }) + ")." + boundary
				}
			}
		}
		ast.Inspect(declaration, func(node ast.Node) bool {
			identifier, ok := node.(*ast.Ident)
			if !ok {
				return true
			}
			function, ok := info.Uses[identifier].(*types.Func)
			if !ok || function.Pkg() == nil {
				return true
			}
			launch := false
			switch function.Pkg().Path() {
			case "os/exec":
				if slices.Contains([]string{"Start", "Run", "Output", "CombinedOutput"}, function.Name()) {
					receiver := function.Type().(*types.Signature).Recv()
					launch = receiver != nil && types.TypeString(receiver.Type(), func(*types.Package) string { return "" }) == "*Cmd"
				}
			case "os":
				launch = function.Name() == "StartProcess"
			case "syscall", "golang.org/x/sys/unix":
				launch = slices.Contains([]string{"StartProcess", "ForkExec", "Exec", "Fexecve"}, function.Name())
			case "github.com/bmeddeb/phebs/internal/dispatchadmission":
				launch = slices.Contains([]string{"StartProduction", "StartAuthor", "RunProduction", "CombinedOutputProduction"}, function.Name())
				if slices.Contains([]string{"Start", "Run"}, function.Name()) {
					receiver := function.Type().(*types.Signature).Recv()
					launch = receiver != nil && types.TypeString(receiver.Type(), func(*types.Package) string { return "" }) == "*Client"
				}
			}
			if launch {
				result[boundary]++
			}
			return true
		})
	}
	return result
}

// A listed function must pass its own exact constant, not a different admitted
// site's ID or a runtime-selected expression that the boundary count misses.
func verifyProductionSiteArguments(t *testing.T, path string, file *ast.File, info *types.Info, expected map[string]uint64) {
	t.Helper()
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok {
			continue
		}
		name := function.Name.Name
		if object, ok := info.Defs[function.Name].(*types.Func); ok {
			if receiver := object.Type().(*types.Signature).Recv(); receiver != nil {
				name = "(" + types.TypeString(receiver.Type(), func(*types.Package) string { return "" }) + ")." + name
			}
		}
		want, selected := expected[path+":"+name]
		if !selected {
			continue
		}
		found := 0
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			callee, ok := info.Uses[selector.Sel].(*types.Func)
			if !ok || callee.Pkg() == nil || callee.Pkg().Path() != "github.com/bmeddeb/phebs/internal/dispatchadmission" ||
				!slices.Contains([]string{"StartProduction", "RunProduction", "CombinedOutputProduction"}, callee.Name()) {
				return true
			}
			found++
			if len(call.Args) != 3 || info.Types[call.Args[1]].Value == nil {
				t.Fatalf("dispatch site %s:%s lacks its fixed site constant", path, name)
			}
			got, exact := constant.Uint64Val(info.Types[call.Args[1]].Value)
			if !exact || got != want {
				t.Fatalf("dispatch site %s:%s ID=%d, want %d", path, name, got, want)
			}
			return true
		})
		if found != 1 {
			t.Fatalf("dispatch site %s:%s has %d controlled calls, want one", path, name, found)
		}
	}
}

func TestProductionDispatchInventoryFindsUnlistedAndAliasedLaunches(t *testing.T) {
	const source = `package fixture
import command "os/exec"
import "os"
type unrelated struct{}
func (unrelated) Start() {}
func newUnlisted() { _ = command.Command("git").Run() }
func aliasLaunch() { c := command.Command("git"); start := c.Start; _ = start() }
func rawLaunch() { _, _ = os.StartProcess("git", nil, nil) }
func noLaunch() { unrelated{}.Start() }
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "unlisted.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	info := &types.Info{Defs: make(map[*ast.Ident]types.Object), Uses: make(map[*ast.Ident]types.Object)}
	config := types.Config{Importer: importer.Default()}
	if _, err := config.Check("fixture", fset, []*ast.File{file}, info); err != nil {
		t.Fatal(err)
	}
	got := typedDispatchBoundaries(file, info)
	want := map[string]int{"newUnlisted": 1, "aliasLaunch": 1, "rawLaunch": 1}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("independent launch discovery = %v, want %v", got, want)
	}
}
