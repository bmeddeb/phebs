package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/extractors/grpcgo"
	"github.com/bmeddeb/phebs/internal/extract/extractors/kafkago"
	"github.com/bmeddeb/phebs/internal/extract/extractors/protodecl"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
)

func TestBindT307NeutralServiceDemoIsExplicitAndPipelineBacked(t *testing.T) {
	fixture := t307FixturePath(t)
	repository, err := phebssync.RepoName(fixture)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("ordinary serve remains unchanged", func(t *testing.T) {
		cfg := &config.Config{}
		if err := bindT307NeutralServiceDemo(cfg, ""); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(cfg, &config.Config{}) {
			t.Fatalf("empty setting changed config: %+v", cfg)
		}
	})

	t.Run("exact bundle registers the real scoped pipelines", func(t *testing.T) {
		cfg := &config.Config{}
		if err := bindT307NeutralServiceDemo(cfg, fixture); err != nil {
			t.Fatal(err)
		}
		if len(cfg.Connections) != 1 ||
			cfg.Connections[0].Name != "t30-service-scope-demo" ||
			cfg.Connections[0].Type != "git" ||
			cfg.Connections[0].URL != fixture {
			t.Fatalf("demo connection = %+v", cfg.Connections)
		}
		if !cfg.Experimental.ProvisionalProtoExtraction ||
			!cfg.Experimental.ProvisionalKafkaExtraction ||
			!cfg.Experimental.ProvisionalWorkbench ||
			cfg.Experimental.ProvisionalThriftExtraction ||
			cfg.Experimental.ProvisionalThriftFieldExtraction {
			t.Fatalf("demo feature posture = %+v", cfg.Experimental)
		}
		if len(cfg.AnalysisUnits) != 1 {
			t.Fatalf("analysis units = %+v", cfg.AnalysisUnits)
		}
		state, err := cfg.AnalysisUnits[repository].Scope(repository).State()
		if err != nil {
			t.Fatal(err)
		}
		if state.Name != "orders-service" ||
			!reflect.DeepEqual(state.PrimaryPaths, []string{"service/orders"}) ||
			!reflect.DeepEqual(state.SupportingPaths, []string{
				"api/orders.proto",
				"gen/ordersv1/orders_grpc.pb.go",
				"generated-from-snapshot.json",
				"go.mod",
			}) ||
			state.SearchIndexPosture != analysisunit.SearchIndexFocused ||
			state.TypedIndexPosture != analysisunit.TypedIndexRepositoryRootUnbound {
			t.Fatalf("demo unit state = %+v", state)
		}
	})

	t.Run("repeat binding is idempotent", func(t *testing.T) {
		cfg := &config.Config{}
		for range 2 {
			if err := bindT307NeutralServiceDemo(cfg, fixture); err != nil {
				t.Fatal(err)
			}
		}
		if len(cfg.Connections) != 1 || len(cfg.AnalysisUnits) != 1 {
			t.Fatalf("repeat binding widened config: %+v", cfg)
		}
	})

	t.Run("existing exact git source is not duplicated", func(t *testing.T) {
		cfg := &config.Config{Connections: []config.Connection{{
			Name: "already-present", Type: "git", URL: fixture,
		}}}
		if err := bindT307NeutralServiceDemo(cfg, fixture); err != nil {
			t.Fatal(err)
		}
		if len(cfg.Connections) != 1 || len(cfg.AnalysisUnits) != 1 {
			t.Fatalf("existing source changed unexpectedly: %+v", cfg)
		}
	})

	t.Run("invalid settings fail before mutation", func(t *testing.T) {
		directory := filepath.Join(t.TempDir(), "t307-neutral-service.bundle")
		if err := os.Mkdir(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		rows := []struct {
			name    string
			fixture string
			cfg     *config.Config
		}{
			{
				name: "nil config", fixture: fixture, cfg: nil,
			},
			{
				name: "relative", fixture: filepath.Join(
					"docs", "fixtures", "t30.7-neutral-service",
					"t307-neutral-service.bundle",
				), cfg: &config.Config{},
			},
			{name: "surrounding space", fixture: fixture + " ", cfg: &config.Config{}},
			{
				name: "wrong basename",
				fixture: filepath.Join(
					filepath.Dir(fixture), "another.bundle",
				),
				cfg: &config.Config{},
			},
			{
				name: "missing",
				fixture: filepath.Join(
					t.TempDir(), "t307-neutral-service.bundle",
				),
				cfg: &config.Config{},
			},
			{name: "directory", fixture: directory, cfg: &config.Config{}},
			{
				name: "connection collision", fixture: fixture,
				cfg: &config.Config{Connections: []config.Connection{{
					Name: "t30-service-scope-demo", Type: "git",
					URL: "/different/source",
				}}},
			},
			{
				name: "source kind collision", fixture: fixture,
				cfg: &config.Config{Connections: []config.Connection{{
					Name: "already-present", Type: "github", URL: fixture,
				}}},
			},
			{
				name: "analysis unit collision", fixture: fixture,
				cfg: &config.Config{AnalysisUnits: map[string]analysisunit.Config{
					repository: {
						Name: "another-service", Primary: []string{"other"},
					},
				}},
			},
		}
		for _, row := range rows {
			t.Run(row.name, func(t *testing.T) {
				if row.cfg == nil {
					if err := bindT307NeutralServiceDemo(nil, row.fixture); err == nil {
						t.Fatal("nil config succeeded")
					}
					return
				}
				before := cloneT307Config(row.cfg)
				if err := bindT307NeutralServiceDemo(row.cfg, row.fixture); err == nil {
					t.Fatalf("invalid fixture setting %q succeeded", row.fixture)
				}
				if !reflect.DeepEqual(*row.cfg, before) {
					t.Fatalf(
						"failed binding changed config: before=%+v after=%+v",
						before,
						*row.cfg,
					)
				}
			})
		}
	})
}

func TestT307NeutralServiceFixtureIsPinnedAndExtractable(t *testing.T) {
	fixture := t307FixturePath(t)
	receiptPath := filepath.Join(filepath.Dir(fixture), "receipt.json")
	receiptBytes, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	var receipt struct {
		Commit             string `json:"commit"`
		BundleSHA256       string `json:"bundle_sha256"`
		BundleBytes        int    `json:"bundle_bytes"`
		RegularFiles       int    `json:"regular_files"`
		CandidateRecords   int    `json:"candidate_records"`
		BaseLaneRecords    int    `json:"base_lane_records"`
		GoTestLaneRecords  int    `json:"go_test_lane_records"`
		FocusedSearchFiles int    `json:"focused_search_files"`
	}
	if err := json.Unmarshal(receiptBytes, &receipt); err != nil {
		t.Fatal(err)
	}
	bundle, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	bundleDigest := sha256.Sum256(bundle)
	if got := "sha256:" + hex.EncodeToString(bundleDigest[:]); got != receipt.BundleSHA256 || len(bundle) != receipt.BundleBytes {
		t.Fatalf(
			"bundle receipt = %s/%d, observed %s/%d",
			receipt.BundleSHA256,
			receipt.BundleBytes,
			got,
			len(bundle),
		)
	}

	checkout := filepath.Join(t.TempDir(), "repo")
	command := exec.Command("git", "clone", "--quiet", fixture, checkout)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("clone retained demo bundle: %v\n%s", err, output)
	}
	if got := strings.TrimSpace(t307Git(t, checkout, "rev-parse", "HEAD")); got != receipt.Commit {
		t.Fatalf("bundle commit = %s, want %s", got, receipt.Commit)
	}
	paths := strings.Fields(t307Git(t, checkout, "ls-tree", "-r", "--name-only", "HEAD"))
	if len(paths) != receipt.RegularFiles {
		t.Fatalf("bundle paths = %d, want %d: %v", len(paths), receipt.RegularFiles, paths)
	}

	retainedSource := filepath.Join(filepath.Dir(fixture), "repo")
	files := make(map[string]string, len(paths))
	policies, err := extract.CandidatePolicies(
		evidenceExtractors(true, false, false, true),
	)
	if err != nil {
		t.Fatal(err)
	}
	candidateCount := 0
	baseCount := 0
	testCount := 0
	focusedCount := 0
	for _, relative := range paths {
		fromBundle, err := os.ReadFile(filepath.Join(checkout, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatal(err)
		}
		fromSource, err := os.ReadFile(filepath.Join(retainedSource, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(fromBundle, fromSource) {
			t.Fatalf("bundle path %s differs from retained source", relative)
		}
		files[relative] = string(fromBundle)
		candidateRecord := false
		for _, policy := range policies {
			candidateRecord = candidateRecord || policy.Enumerate(relative)
		}
		if candidateRecord {
			candidateCount++
			switch candidate.SourceLaneForPath(relative) {
			case candidate.SourceLaneBase:
				baseCount++
			case candidate.SourceLaneGoTest:
				testCount++
			default:
				t.Fatalf("unexpected source lane for %s", relative)
			}
		}
		if t307FocusedPath(relative) {
			focusedCount++
		}
	}
	if candidateCount != receipt.CandidateRecords ||
		baseCount != receipt.BaseLaneRecords ||
		testCount != receipt.GoTestLaneRecords ||
		focusedCount != receipt.FocusedSearchFiles {
		t.Fatalf(
			"fixture counts candidate/base/test/focused = %d/%d/%d/%d, want %d/%d/%d/%d",
			candidateCount,
			baseCount,
			testCount,
			focusedCount,
			receipt.CandidateRecords,
			receipt.BaseLaneRecords,
			receipt.GoTestLaneRecords,
			receipt.FocusedSearchFiles,
		)
	}
	if t307FocusedPath("consumers/external/caller.go") ||
		t307FocusedPath("bulk/irrelevant.txt") {
		t.Fatal("outside caller or bulk needle entered focused scope")
	}
	callerOverlay := false
	localKafka := 0
	for _, policy := range policies {
		switch policy.Domain {
		case "grpc-caller":
			callerOverlay = policy.Plane == candidate.PlaneCaller &&
				policy.Enumerate("consumers/external/caller.go")
		case "kafka-producer", "kafka-consumer":
			if policy.Plane == candidate.PlaneLocal &&
				policy.Enumerate("service/orders/kafka.go") {
				localKafka++
			}
		}
	}
	if !callerOverlay || localKafka != 2 {
		t.Fatalf(
			"candidate policy posture caller-overlay/local-kafka = %v/%d",
			callerOverlay,
			localKafka,
		)
	}
	if !strings.Contains(
		files["service/orders/service.go"],
		"T307_FOCUSED_SERVICE_NEEDLE",
	) || !strings.Contains(
		files["bulk/irrelevant.txt"],
		"T307_OUTSIDE_UNIT_BULK_NEEDLE",
	) || !strings.Contains(
		files["consumers/external/caller.go"],
		"client.Create(ctx, request)",
	) {
		t.Fatal("fixture lost a focused, excluded-bulk, or overlay-caller sentinel")
	}

	assertT307Fact(t, protodecl.New(), files,
		"DECLARES_OPERATION", "demo.orders.v1.Orders/Create", "api/orders.proto")
	assertT307Fact(t, grpcgo.New(), files,
		"REGISTERS_GRPC_SERVICE", "demo.orders.v1.Orders", "service/orders/service.go")
	assertT307Fact(t, kafkago.NewProducer(), files,
		"PRODUCES_TO_TOPIC", "topic:orders.created.v1", "service/orders/kafka.go")
	assertT307Fact(t, kafkago.NewConsumer(), files,
		"CONSUMES_FROM_TOPIC", "topic:orders.created.v1", "service/orders/kafka.go")
	for _, extractor := range []sdk.Extractor{
		kafkago.NewProducer(), kafkago.NewConsumer(),
	} {
		facts := runT307Extractor(t, extractor, files)
		for _, fact := range facts {
			if fact.Path == "service/orders/fixture_test.go" ||
				fact.Assertion.Object == "topic:orders.test-only.v1" {
				t.Fatalf("%s emitted test-only evidence: %+v", extractor.Domain(), fact)
			}
		}
	}
}

func TestBindT335ServiceDirectoryDemoIsExplicitAndCatalogBacked(t *testing.T) {
	fixture := t307FixturePath(t)
	catalogPath := t335CatalogPath(t)
	repository, err := phebssync.RepoName(fixture)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("ordinary serve remains unchanged", func(t *testing.T) {
		cfg := &config.Config{}
		if err := bindT335ServiceDirectoryDemo(cfg, "", ""); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(cfg, &config.Config{}) {
			t.Fatalf("empty setting changed config: %+v", cfg)
		}
	})

	t.Run("exact companion catalog selects the ordinary v2 ingestion path", func(t *testing.T) {
		cfg := &config.Config{}
		if err := bindT307NeutralServiceDemo(cfg, fixture); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if err := bindT335ServiceDirectoryDemo(cfg, fixture, catalogPath); err != nil {
				t.Fatal(err)
			}
		}
		selection, ok := cfg.ServiceCatalogs[repository]
		if !ok || len(cfg.ServiceCatalogs) != 1 ||
			selection != (config.ServiceCatalog{
				Kind: servicecatalog.AuthorityOperator, ID: "t335-demo",
				Version: "v1", Path: catalogPath,
			}) {
			t.Fatalf("service catalog selection = %+v", cfg.ServiceCatalogs)
		}
	})

	t.Run("invalid settings fail before mutation", func(t *testing.T) {
		directory := filepath.Join(t.TempDir(), "t335-service-catalog.json")
		if err := os.Mkdir(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		symlink := filepath.Join(t.TempDir(), "t335-service-catalog.json")
		if err := os.Symlink(catalogPath, symlink); err != nil {
			t.Fatal(err)
		}
		drifted := filepath.Join(t.TempDir(), "t335-service-catalog.json")
		raw, err := os.ReadFile(catalogPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(drifted, append(raw, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
		bound := func() *config.Config {
			cfg := &config.Config{}
			if err := bindT307NeutralServiceDemo(cfg, fixture); err != nil {
				t.Fatal(err)
			}
			return cfg
		}
		collision := bound()
		collision.ServiceCatalogs = map[string]config.ServiceCatalog{
			repository: {
				Kind: servicecatalog.AuthorityOperator, ID: "another",
				Version: "v2", Path: catalogPath,
			},
		}
		rows := []struct {
			name    string
			fixture string
			catalog string
			cfg     *config.Config
		}{
			{name: "nil config", fixture: fixture, catalog: catalogPath},
			{name: "missing cohort", fixture: fixture, catalog: catalogPath, cfg: &config.Config{}},
			{name: "missing bundle", catalog: catalogPath, cfg: bound()},
			{name: "relative catalog", fixture: fixture, catalog: filepath.Join("docs", "fixtures", "t33.5-service-directory", "t335-service-catalog.json"), cfg: bound()},
			{name: "padded catalog", fixture: fixture, catalog: catalogPath + " ", cfg: bound()},
			{name: "wrong basename", fixture: fixture, catalog: filepath.Join(filepath.Dir(catalogPath), "catalog.json"), cfg: bound()},
			{name: "missing catalog", fixture: fixture, catalog: filepath.Join(t.TempDir(), "t335-service-catalog.json"), cfg: bound()},
			{name: "catalog directory", fixture: fixture, catalog: directory, cfg: bound()},
			{name: "catalog symlink", fixture: fixture, catalog: symlink, cfg: bound()},
			{name: "catalog byte drift", fixture: fixture, catalog: drifted, cfg: bound()},
			{name: "selection collision", fixture: fixture, catalog: catalogPath, cfg: collision},
		}
		for _, row := range rows {
			t.Run(row.name, func(t *testing.T) {
				if row.cfg == nil {
					if err := bindT335ServiceDirectoryDemo(nil, row.fixture, row.catalog); err == nil {
						t.Fatal("nil config succeeded")
					}
					return
				}
				before := cloneT307Config(row.cfg)
				if err := bindT335ServiceDirectoryDemo(row.cfg, row.fixture, row.catalog); err == nil {
					t.Fatal("invalid T33.5 demo setting succeeded")
				}
				if !reflect.DeepEqual(*row.cfg, before) {
					t.Fatalf("failed binding changed config: before=%+v after=%+v", before, *row.cfg)
				}
			})
		}
	})
}

func TestT335ServiceDirectoryCatalogIsPinnedAndNeutral(t *testing.T) {
	catalogPath := t335CatalogPath(t)
	raw, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := servicecatalog.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := servicecatalog.Digest(catalog)
	if err != nil {
		t.Fatal(err)
	}
	encodedDigest := sha256.Sum256(raw)
	const wantEncodedDigest = t335CatalogEncodedSHA256
	const wantCatalogDigest = "sha256:8d82e26cbebafa3062791389818e29b537ddcac8b4f789b27cc3cca82badcd84"
	encoded := "sha256:" + hex.EncodeToString(encodedDigest[:])
	if got := encoded; got != wantEncodedDigest ||
		digest != wantCatalogDigest {
		t.Fatalf("catalog digests = %s/%s", got, digest)
	}
	receiptBytes, err := os.ReadFile(filepath.Join(filepath.Dir(catalogPath), "receipt.json"))
	if err != nil {
		t.Fatal(err)
	}
	var receipt struct {
		CatalogEncodedSHA256  string `json:"catalog_encoded_sha256"`
		CatalogSemanticSHA256 string `json:"catalog_semantic_sha256"`
		CatalogEncodedBytes   int    `json:"catalog_encoded_bytes"`
		ServiceCount          int    `json:"service_count"`
		MembershipCount       int    `json:"membership_count"`
		UnownedCount          int    `json:"unowned_placement_count"`
		RoleCounts            struct {
			Primary    int `json:"primary"`
			Supporting int `json:"supporting"`
			Shared     int `json:"shared"`
			Generated  int `json:"generated"`
			Typed      int `json:"typed"`
		} `json:"role_counts"`
	}
	if err := json.Unmarshal(receiptBytes, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.CatalogEncodedSHA256 != encoded ||
		receipt.CatalogSemanticSHA256 != digest ||
		receipt.CatalogEncodedBytes != len(raw) {
		t.Fatalf("catalog receipt = %+v, observed %s/%s/%d", receipt, encoded, digest, len(raw))
	}
	if len(catalog.Services) != 5 || len(catalog.Memberships) != 11 ||
		len(catalog.Unowned) != 2 ||
		receipt.ServiceCount != len(catalog.Services) ||
		receipt.MembershipCount != len(catalog.Memberships) ||
		receipt.UnownedCount != len(catalog.Unowned) {
		t.Fatalf("catalog shape = services %d memberships %d unowned %d", len(catalog.Services), len(catalog.Memberships), len(catalog.Unowned))
	}
	roleCounts := map[string]int{}
	for _, membership := range catalog.Memberships {
		roleCounts[membership.Role]++
	}
	if roleCounts[servicecatalog.RolePrimary] != receipt.RoleCounts.Primary ||
		roleCounts[servicecatalog.RoleSupporting] != receipt.RoleCounts.Supporting ||
		roleCounts[servicecatalog.RoleShared] != receipt.RoleCounts.Shared ||
		roleCounts[servicecatalog.RoleGenerated] != receipt.RoleCounts.Generated ||
		roleCounts[servicecatalog.RoleTyped] != receipt.RoleCounts.Typed {
		t.Fatalf("catalog role counts = %v, receipt %+v", roleCounts, receipt.RoleCounts)
	}
	states := map[string]string{}
	for _, service := range catalog.Services {
		states[service.Key] = service.Disposition
	}
	for key, disposition := range map[string]string{
		"billing-control":  servicecatalog.DispositionConflict,
		"legacy-orders":    servicecatalog.DispositionRejected,
		"orders-api":       servicecatalog.DispositionAccepted,
		"orders-events":    servicecatalog.DispositionAccepted,
		"returns-proposal": servicecatalog.DispositionProposal,
	} {
		if states[key] != disposition {
			t.Fatalf("service %s disposition = %q", key, states[key])
		}
	}
}

func t307FixturePath(t *testing.T) string {
	t.Helper()
	fixture, err := filepath.Abs(filepath.Join(
		"..", "..", "docs", "fixtures", "t30.7-neutral-service",
		"t307-neutral-service.bundle",
	))
	if err != nil {
		t.Fatal(err)
	}
	return fixture
}

func t335CatalogPath(t *testing.T) string {
	t.Helper()
	catalog, err := filepath.Abs(filepath.Join(
		"..", "..", "docs", "fixtures", "t33.5-service-directory",
		"t335-service-catalog.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func cloneT307Config(source *config.Config) config.Config {
	clone := *source
	clone.Connections = append([]config.Connection(nil), source.Connections...)
	if source.AnalysisUnits != nil {
		clone.AnalysisUnits = make(
			map[string]analysisunit.Config,
			len(source.AnalysisUnits),
		)
		for repository, unit := range source.AnalysisUnits {
			unit.Primary = append([]string(nil), unit.Primary...)
			unit.Supporting = append([]string(nil), unit.Supporting...)
			clone.AnalysisUnits[repository] = unit
		}
	}
	if source.ServiceCatalogs != nil {
		clone.ServiceCatalogs = make(
			map[string]config.ServiceCatalog,
			len(source.ServiceCatalogs),
		)
		for repository, catalog := range source.ServiceCatalogs {
			clone.ServiceCatalogs[repository] = catalog
		}
	}
	return clone
}

func t307Git(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
	return string(output)
}

func t307FocusedPath(filePath string) bool {
	if filePath == "service/orders" || strings.HasPrefix(filePath, "service/orders/") {
		return true
	}
	switch filePath {
	case "api/orders.proto",
		"gen/ordersv1/orders_grpc.pb.go",
		"generated-from-snapshot.json",
		"go.mod":
		return true
	default:
		return false
	}
}

type t307Corpus struct {
	paths []string
	files map[string]string
}

func newT307Corpus(files map[string]string) t307Corpus {
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return t307Corpus{paths: paths, files: files}
}

func (t307Corpus) RepoName() string { return "example.invalid/t307-neutral-service" }
func (t307Corpus) Commit() string   { return strings.Repeat("0", 40) }

func (corpus t307Corpus) WalkFiles(
	ctx context.Context,
	visit func(string) error,
) error {
	for _, path := range corpus.paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(path); err != nil {
			return err
		}
	}
	return nil
}

func (corpus t307Corpus) Read(_ context.Context, path string) (sdk.Blob, error) {
	content, ok := corpus.files[path]
	if !ok {
		return sdk.Blob{}, fmt.Errorf("missing fixture path %s", path)
	}
	digest := sha256.Sum256([]byte(content))
	return sdk.Blob{
		Content: content,
		Digest:  "sha256:" + hex.EncodeToString(digest[:]),
	}, nil
}

func runT307Extractor(
	t *testing.T,
	extractor sdk.Extractor,
	files map[string]string,
) []sdk.Fact {
	t.Helper()
	var facts []sdk.Fact
	coverage, err := extractor.Extract(
		context.Background(),
		newT307Corpus(files),
		func(fact sdk.Fact) error {
			facts = append(facts, fact)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("%s extract: %v", extractor.Domain(), err)
	}
	if len(coverage.Failures) != 0 {
		t.Fatalf("%s coverage failures = %v", extractor.Domain(), coverage.Failures)
	}
	return facts
}

func assertT307Fact(
	t *testing.T,
	extractor sdk.Extractor,
	files map[string]string,
	predicate, object, path string,
) {
	t.Helper()
	for _, fact := range runT307Extractor(t, extractor, files) {
		if fact.Assertion.Predicate == predicate &&
			fact.Assertion.Object == object && fact.Path == path {
			return
		}
	}
	t.Fatalf(
		"%s did not emit %s %s at %s",
		extractor.Domain(),
		predicate,
		object,
		path,
	)
}

var _ sdk.Corpus = t307Corpus{}
