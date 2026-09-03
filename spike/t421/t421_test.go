package t421

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
)

const testSourceCommit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

var (
	testPlanOnce sync.Once
	testPlan     Plan
	testPlanErr  error
)

func frozenTestPlan(t *testing.T) Plan {
	t.Helper()
	testPlanOnce.Do(func() { testPlan, testPlanErr = BuildPlan(testSourceCommit) })
	if testPlanErr != nil {
		t.Fatal(testPlanErr)
	}
	return testPlan
}

func TestPlanIsByteIdenticalAndStrict(t *testing.T) {
	first := frozenTestPlan(t)
	second, err := BuildPlan(testSourceCommit)
	if err != nil {
		t.Fatal(err)
	}
	firstRaw, err := MarshalCanonical(first)
	if err != nil {
		t.Fatal(err)
	}
	secondRaw, err := MarshalCanonical(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstRaw, secondRaw) {
		t.Fatal("two T42.1 builds differ")
	}
	decoded, err := DecodePlan(firstRaw)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, first) {
		t.Fatal("strict decode changed the T42.1 plan")
	}
	if first.Profile.Physical.CombinedRegularFiles != 2_031_604 ||
		first.Profile.Logical.PotentialCartesianOwnerPairs != 20_316_040_000 ||
		first.Profile.Logical.MaterializedCartesianOwnerPairs != 0 ||
		first.Profile.Logical.StructuralUnownedPrefixes != 3 {
		t.Fatalf("combined profile = %+v", first.Profile)
	}
	if len(firstRaw) > MaxPlanBytes {
		t.Fatalf("plan bytes = %d, limit = %d", len(firstRaw), MaxPlanBytes)
	}
}

func TestPlanRejectsNoncanonicalSourceBearingAndMutatedInputs(t *testing.T) {
	plan := frozenTestPlan(t)
	raw, err := MarshalCanonical(plan)
	if err != nil {
		t.Fatal(err)
	}
	unknown := append([]byte("{\n  \"unknown\": true,"), raw[1:]...)
	if _, err := DecodePlan(unknown); err == nil {
		t.Fatal("unknown field was accepted")
	}
	if _, err := DecodePlan(append(bytes.Clone(raw), []byte("{}")...)); err == nil {
		t.Fatal("trailing value was accepted")
	}
	compact, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodePlan(compact); err == nil {
		t.Fatal("noncanonical JSON was accepted")
	}
	if err := rejectSourceBearingPlan([]byte(`{"path":"services/private.go"}`)); err == nil {
		t.Fatal("source-bearing plan bytes were accepted")
	}

	for name, mutate := range map[string]func(*Plan){
		"physical": func(value *Plan) { value.Profile.Physical.CombinedPhysicalOwners++ },
		"logical":  func(value *Plan) { value.Profile.Logical.Memberships++ },
		"input":    func(value *Plan) { value.Inputs[0].Identity = "sha256:" + strings.Repeat("0", 64) },
		"phase":    func(value *Plan) { value.PhaseOrder[0] = "changed" },
		"ceiling":  func(value *Plan) { value.SafetyEnvelope.MaximumRetriesPerUnit++ },
		"revision": func(value *Plan) { value.Revisions.Physical[0].BaseTree = strings.Repeat("0", 40) },
		"query":    func(value *Plan) { value.Oracle.QueryCases[0].ExpectedPaths-- },
	} {
		t.Run(name, func(t *testing.T) {
			var changed Plan
			if err := json.Unmarshal(raw, &changed); err != nil {
				t.Fatal(err)
			}
			mutate(&changed)
			if err := ValidatePlan(changed); err == nil {
				t.Fatal("mutated plan was accepted")
			}
		})
	}
}

func TestRelationshipSourcesAndIndependentEdges(t *testing.T) {
	for _, service := range []int{0, 5_000, 9_999} {
		ordinal := fmt.Sprintf("%05d", service)
		protoPath := "contracts/service-" + ordinal + "/api.proto"
		path, proto, relationship, err := combinedOverlayFile(protoPath, []byte("old"))
		if err != nil || path != protoPath || !relationship || !bytes.Contains(proto, []byte("service Service")) {
			t.Fatalf("proto service %d = %q relationship=%t err=%v", service, path, relationship, err)
		}
		for _, sourcePath := range []string{
			"generated/service-" + ordinal + "/client.pb.go",
			"services/service-" + ordinal + "/main.go",
		} {
			path, source, relationship, err := combinedOverlayFile(sourcePath, []byte("old"))
			if err != nil || !relationship {
				t.Fatalf("Go service %d path=%q relationship=%t err=%v", service, path, relationship, err)
			}
			if _, err := parser.ParseFile(token.NewFileSet(), path, source, parser.AllErrors); err != nil {
				t.Fatalf("parse %q: %v", path, err)
			}
		}
	}
	path, content, relationship, err := combinedOverlayFile("shared/group-0000/library.go", []byte("invalid old Go"))
	if err != nil || path != "shared/group-0000/library.go" || relationship ||
		!bytes.Contains(content, []byte(`const FixturePath = "shared/group-0000/library.go"`)) {
		t.Fatalf("fallback path=%q relationship=%t err=%v", path, relationship, err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), path, content, parser.AllErrors); err != nil {
		t.Fatalf("parse fallback %q: %v", path, err)
	}
	authored, err := authoredRelationshipFamilies()
	if err != nil {
		t.Fatal(err)
	}
	independent, err := independentRelationshipFamilies()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(authored, independent) {
		t.Fatal("authored and independent relationship families differ")
	}
}

func TestSealPlanIsCreateOnlyAndPrivate(t *testing.T) {
	path := t.TempDir() + "/plan.json"
	if err := sealPlan(path, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := sealPlan(path, []byte("second")); err == nil {
		t.Fatal("existing plan was replaced")
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "first" {
		t.Fatalf("sealed plan = %q, %v", raw, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("sealed plan mode = %o", info.Mode().Perm())
	}
}
