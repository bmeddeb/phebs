package t201

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"

	"github.com/bmeddeb/phebs/internal/extract/extractors/grpcgo"
	"github.com/bmeddeb/phebs/internal/extract/extractors/thriftgo"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
)

func TestGeneratedProfilesAreByteIdentical(t *testing.T) {
	for _, name := range []string{SmallProfileName, ScaleProfileName} {
		t.Run(name, func(t *testing.T) {
			first := mustProfile(t, name)
			second := mustProfile(t, name)
			if !reflect.DeepEqual(first, second) {
				t.Fatal("two generations differ")
			}
			if got, want := ManifestDigest(first.Files), ManifestDigest(second.Files); got != want {
				t.Fatalf("manifest digest = %s, want %s", got, want)
			}
			firstRoot := t.TempDir()
			secondRoot := t.TempDir()
			if err := WriteProfile(firstRoot, first); err != nil {
				t.Fatalf("write first: %v", err)
			}
			if err := WriteProfile(secondRoot, second); err != nil {
				t.Fatalf("write second: %v", err)
			}
			if got, want := gitTree(t, firstRoot), gitTree(t, secondRoot); got != want {
				t.Fatalf("Git tree = %s, want %s", got, want)
			}
		})
	}
}

func TestPinnedSCIPInputIsCopiedVerbatim(t *testing.T) {
	profile := mustProfile(t, SmallProfileName)
	prepared, err := os.ReadFile("testdata/index.scip")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(profile.Files["index.scip"], prepared) {
		t.Fatal("generated corpus did not copy prepared index bytes verbatim")
	}
	sum := sha256.Sum256(prepared)
	if got := hex.EncodeToString(sum[:]); got != "46d3930208d141e5281a8310800ef5b6a0aeac79c0d89592650f293ffb017b05" {
		t.Fatalf("SCIP digest = %s", got)
	}
	var index scip.Index
	if err := proto.Unmarshal(prepared, &index); err != nil {
		t.Fatalf("decode prepared SCIP: %v", err)
	}
	var paths []string
	references := 0
	for _, document := range index.Documents {
		paths = append(paths, document.RelativePath)
		for _, occurrence := range document.Occurrences {
			if occurrence.SymbolRoles&int32(scip.SymbolRole_Definition) == 0 {
				references++
			}
		}
	}
	if !slices.IsSorted(paths) {
		t.Fatalf("SCIP documents are not sorted: %v", paths)
	}
	if len(index.Documents) != 7 || references != 5 {
		t.Fatalf("SCIP shape = %d documents, %d references; want 7, 5", len(index.Documents), references)
	}
}

func TestSmallOraclePinsResolutionAndAttributionCases(t *testing.T) {
	profile := mustProfile(t, SmallProfileName)
	if len(profile.Oracle.Calls) != 10 {
		t.Fatalf("calls = %d, want 10", len(profile.Oracle.Calls))
	}
	known, unresolved := 0, 0
	roles := map[string]bool{}
	for _, call := range profile.Oracle.Calls {
		if got := string(profile.Files[call.Path][call.StartByte:call.EndByte]); got == "" {
			t.Fatalf("%s has an empty cited span", call.ID)
		}
		roles[call.CodeRole] = true
		switch call.Resolution {
		case ResolutionKnown:
			known++
			if call.GeneratedSymbol == "" || call.CanonicalOperation == "" {
				t.Fatalf("known call lacks typed identity: %+v", call)
			}
		case ResolutionUnresolved:
			unresolved++
			if call.Reason == "" {
				t.Fatalf("unresolved call lacks reason: %+v", call)
			}
		default:
			t.Fatalf("unsupported resolution: %+v", call)
		}
	}
	if known != 5 || unresolved != 5 {
		t.Fatalf("resolution counts = known %d unresolved %d, want 5/5", known, unresolved)
	}
	for _, role := range []string{"production", "test", "mock", "vendor"} {
		if !roles[role] {
			t.Errorf("missing %s role", role)
		}
	}
	stateByPath := map[string]string{}
	for _, relation := range profile.Oracle.GeneratedFrom {
		stateByPath[relation.GeneratedPath] = relation.State
	}
	for path, want := range map[string]string{
		"gen/proto/orders/v1/orders_grpc.pb.go":      "resolved",
		"gen/proto-copy/orders/v1/orders_grpc.pb.go": "resolved",
		"gen/thrift/missing/missing.go":              "missing",
		"gen/thrift/conflict/conflict.go":            "conflict",
	} {
		if got := stateByPath[path]; got != want {
			t.Errorf("%s state = %q, want %q", path, got, want)
		}
	}
	if !bytes.Equal(profile.Files["idl/thrift/ledger.thrift"], profile.Files["vendor/thrift/ledger.thrift"]) {
		t.Fatal("vendored duplicate IDL is not byte-identical")
	}
	if got := strings.Count(string(profile.Files["generated-from-snapshot.json"]),
		`"generator_relative_path":"orders/v1/orders.proto"`); got != 2 {
		t.Fatalf("generator-relative protobuf mappings = %d, want 2", got)
	}
}

func TestScaleOraclePinsTargetCardinalities(t *testing.T) {
	profile := mustProfile(t, ScaleProfileName)
	if got := len(profile.Oracle.Calls); got != ScaleTotalCalls {
		t.Fatalf("calls = %d, want %d", got, ScaleTotalCalls)
	}
	if got := len(profile.Oracle.UnitMappings); got != ScaleTotalMappings {
		t.Fatalf("unit mappings = %d, want %d", got, ScaleTotalMappings)
	}
	units := make(map[string]bool, ScaleLogicalUnits)
	for _, mapping := range profile.Oracle.UnitMappings {
		if strings.HasPrefix(mapping.CallID, "scale-call-") && len(mapping.LogicalService) != 1 {
			t.Fatalf("mapping lacks one logical service: %+v", mapping)
		}
		for _, unit := range mapping.LogicalService {
			units[unit] = true
		}
	}
	if len(units) != ScaleTotalMappings {
		t.Fatalf("distinct logical units = %d, want %d", len(units), ScaleTotalMappings)
	}
	sameOperation := 0
	for _, call := range profile.Oracle.Calls {
		if call.CanonicalOperation == "/synthetic.orders.v1.Orders/Get" {
			sameOperation++
		}
	}
	if sameOperation < HighFanoutReferences {
		t.Fatalf("same-operation references = %d, want at least %d", sameOperation, HighFanoutReferences)
	}
	placementDigests := map[string]int{}
	for path, content := range profile.Files {
		if strings.HasPrefix(path, "copies/") {
			sum := sha256.Sum256(content)
			placementDigests[hex.EncodeToString(sum[:])]++
		}
	}
	if len(placementDigests) != 1 {
		t.Fatalf("repeated placement digests = %d, want 1", len(placementDigests))
	}
	for _, count := range placementDigests {
		if count != RepeatedPlacements {
			t.Fatalf("same-content placements = %d, want %d", count, RepeatedPlacements)
		}
	}
	var snapshot struct {
		Mappings []UnitMappingExpectation `json:"mappings"`
	}
	if err := json.Unmarshal(profile.Files["unit-snapshot.json"], &snapshot); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshot.Mappings, profile.Oracle.UnitMappings) {
		t.Fatal("materialized scale unit snapshot differs from oracle")
	}
}

func TestCurrentGlobalNameResolversAbstainOnCollisions(t *testing.T) {
	profile := mustProfile(t, SmallProfileName)
	corpus := memoryCorpus{files: profile.Files}
	tests := []struct {
		name      string
		extractor sdk.Extractor
		predicate string
		path      string
	}{
		{"grpc", grpcgo.New(), "UNRESOLVED_GRPC_CALL", "src/checkout/caller.go"},
		{"thrift", thriftgo.New(), "UNRESOLVED_THRIFT_CALL", "src/thrift/caller.go"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			var facts []sdk.Fact
			coverage, err := testCase.extractor.Extract(context.Background(), corpus, func(fact sdk.Fact) error {
				facts = append(facts, fact)
				return nil
			})
			if err != nil {
				t.Fatalf("extract: %v", err)
			}
			found := false
			for _, fact := range facts {
				if fact.Path == testCase.path && fact.Assertion.Predicate == testCase.predicate {
					found = true
				}
				if fact.Path == testCase.path && fact.Assertion.Predicate == "CALLS_OPERATION" {
					t.Fatalf("collision was guessed as resolved: %+v", fact)
				}
			}
			if !found || coverage.UnresolvedCount == 0 {
				t.Fatalf("collision abstention missing: coverage=%+v facts=%+v", coverage, facts)
			}
		})
	}
}

func TestCurrentExtractionBaseline(t *testing.T) {
	got, err := MeasureCurrentExtraction(context.Background(), mustProfile(t, SmallProfileName))
	if err != nil {
		t.Fatal(err)
	}
	want := []ExtractionMeasurement{
		{Domain: "grpc-consumer", Version: "1.1.0", Facts: 9, UnresolvedCount: 9, Predicates: map[string]int{
			"GRPC_EXTRACTION_GAP": 1, "UNRESOLVED_GRPC_CALL": 8,
		}},
		{Domain: "proto-contract", Version: "3.0.0", Facts: 13, Predicates: map[string]int{
			"DECLARES_FIELD": 4, "DECLARES_MESSAGE": 4, "DECLARES_OPERATION": 3, "DECLARES_SERVICE": 2,
		}},
		{Domain: "scip-proto-field", Version: "1.0.0", Predicates: map[string]int{}},
		{Domain: "thrift-consumer", Version: "1.1.0", Facts: 25, UnresolvedCount: 25, Predicates: map[string]int{
			"THRIFT_EXTRACTION_GAP": 1, "UNRESOLVED_THRIFT_CALL": 24,
		}},
		{Domain: "thrift-contract", Version: "1.0.0", Facts: 20, Predicates: map[string]int{
			"DECLARES_FIELD": 8, "DECLARES_MESSAGE": 8, "DECLARES_OPERATION": 2, "DECLARES_SERVICE": 2,
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("current extraction = %#v, want %#v", got, want)
	}
}

func TestCorpusContainsOnlyNeutralOwnedNames(t *testing.T) {
	for _, name := range []string{SmallProfileName, ScaleProfileName} {
		profile := mustProfile(t, name)
		for path, content := range profile.Files {
			lower := strings.ToLower(path + "\n" + string(content))
			for _, forbidden := range []string{"uber.com", "corp.", "jaeger", "internal.company"} {
				if strings.Contains(lower, forbidden) {
					t.Fatalf("%s contains forbidden external/employer marker %q", path, forbidden)
				}
			}
		}
	}
}

func mustProfile(t *testing.T, name string) Profile {
	t.Helper()
	profile, err := GenerateProfile(name)
	if err != nil {
		t.Fatalf("GenerateProfile(%s): %v", name, err)
	}
	return profile
}

func gitTree(t *testing.T, root string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	runGit(t, root, "init", "--quiet")
	runGit(t, root, "add", "--all")
	command := exec.Command("git", "write-tree")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git write-tree: %v", err)
	}
	return strings.TrimSpace(string(output))
}

func runGit(t *testing.T, root string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = root
	command.Env = append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+filepath.Join(root, "missing-global-config"),
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

type memoryCorpus struct {
	files map[string][]byte
}

func (memoryCorpus) RepoName() string { return "synthetic.invalid/mono" }
func (memoryCorpus) Commit() string   { return strings.Repeat("a", 40) }

func (c memoryCorpus) WalkFiles(ctx context.Context, visit func(string) error) error {
	paths := make([]string, 0, len(c.files))
	for path := range c.files {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(path); err != nil {
			return err
		}
	}
	return nil
}

func (c memoryCorpus) Read(_ context.Context, path string) (sdk.Blob, error) {
	content, ok := c.files[path]
	if !ok {
		return sdk.Blob{}, os.ErrNotExist
	}
	sum := sha256.Sum256(content)
	return sdk.Blob{
		Content: string(content),
		Digest:  "sha256:" + hex.EncodeToString(sum[:]),
	}, nil
}
