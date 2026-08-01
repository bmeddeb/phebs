package api_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/internal/extract/extractors/protodecl"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/indexer"
	"github.com/bmeddeb/phebs/internal/store"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
	"github.com/bmeddeb/phebs/spike/t201"
)

const (
	t2014RunEnv     = "T2014_RUN"
	t2014ResultsEnv = "T2014_RESULTS_PATH"
)

// TestT2014ScaleFailureAndEndToEndClosure is the retained live acceptance
// gate for Epic 20. It is opt-in because it starts a real SurrealDB child and
// publishes the complete 10,010-call profile. The ordinary merge bar retains
// the component, schema, determinism, dark-posture, and UI guards; `make
// t20-closure` runs this complete empty-data journey with the same binaries
// and package seams as `make dev`.
func TestT2014ScaleFailureAndEndToEndClosure(t *testing.T) {
	if os.Getenv(t2014RunEnv) != "1" {
		t.Skip("set T2014_RUN=1 or run make t20-closure")
	}
	for _, binary := range []string{"git", "surreal"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Fatalf("%s is required for the T20.14 live gate: %v", binary, err)
		}
	}
	zoekt, err := indexer.FindBinary()
	if err != nil {
		t.Fatalf("zoekt-git-index is required for the T20.14 live gate: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Minute)
	defer cancel()
	root := t.TempDir()
	origin := filepath.Join(root, "origin")
	dataDir := filepath.Join(root, "data")
	if err := os.Mkdir(origin, 0o700); err != nil {
		t.Fatal(err)
	}
	profile, err := t201.GenerateProfile(t201.ScaleProfileName)
	if err != nil {
		t.Fatalf("generate frozen profile: %v", err)
	}
	if err := t201.WriteProfile(origin, profile); err != nil {
		t.Fatalf("write frozen profile: %v", err)
	}
	initRepository(t, origin)
	healthyCommit := gitOutput(t, origin, "rev-parse", "HEAD")

	st, err := store.OpenLocal(ctx, dataDir)
	if err != nil {
		t.Fatalf("open empty store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	connection := config.Connection{
		Name: "t20-closure", Type: "git", URL: "file://" + origin,
	}

	measurements := t2014Measurements{
		Schema:         "t20-closure-measurement-v1",
		GoVersion:      runtime.Version(),
		GOOS:           runtime.GOOS,
		GOARCH:         runtime.GOARCH,
		GOMAXPROCS:     runtime.GOMAXPROCS(0),
		Profile:        profile.Name,
		ManifestDigest: t201.ManifestDigest(profile.Files),
		ProfileFiles:   len(profile.Files),
		Calls:          len(profile.Oracle.Calls),
		UnitMappings:   len(profile.Oracle.UnitMappings),
	}

	started := time.Now()
	repositories, err := phebssync.SyncConnection(
		ctx, st, dataDir, connection, nil,
	)
	measurements.SyncWallMilliseconds = time.Since(started).Milliseconds()
	if err != nil || len(repositories) != 1 {
		t.Fatalf("sync generated repository = %v, %v", repositories, err)
	}
	repository := repositories[0]

	started = time.Now()
	ix := &indexer.Indexer{DataDir: dataDir, Bin: zoekt, Store: st}
	if err := ix.Index(ctx, store.Repo{Name: repository}, false); err != nil {
		t.Fatalf("index generated repository: %v", err)
	}
	measurements.IndexWallMilliseconds = time.Since(started).Milliseconds()
	indexed, err := st.GetRepo(ctx, repository)
	if err != nil || indexed.IndexedCommitHash != healthyCommit {
		t.Fatalf("indexed repository = %+v, %v; want %s", indexed, err, healthyCommit)
	}

	// The first extractor fails deliberately. The two later real domains must
	// still publish, proving T19.8's per-domain isolation on the scale corpus.
	worker := &extract.Worker{
		Repos: st, Evidence: st, NewCorpus: extract.GitCorpus(dataDir),
		Extractors: []extract.Extractor{
			t2014FailingExtractor{},
			protodecl.New(),
			gocaller.NewGRPC(),
		},
	}
	started = time.Now()
	extractErr := worker.Handle(ctx, store.Job{
		Kind: store.JobExtract, Target: repository,
	})
	measurements.ExtractWallMilliseconds = time.Since(started).Milliseconds()
	if extractErr == nil || !strings.Contains(extractErr.Error(), "injected-domain-failure") {
		t.Fatalf("scale extraction did not retain injected failure: %v", extractErr)
	}
	measurements.InjectedDomainFailure = true
	for _, domain := range []string{"proto-contract", "grpc-caller"} {
		run, err := st.LatestPublishedRun(ctx, store.ExtractionScope{
			Repository: repository,
			Commit:     healthyCommit,
			Domain:     domain,
		})
		if err != nil || run.Commit != healthyCommit || run.Status != "published" {
			t.Fatalf("%s did not publish after an earlier domain failure: %+v, %v",
				domain, run, err)
		}
	}

	visible := true
	opts := api.Options{
		Store: st, Evidence: st, CallerMapEnabled: true,
		Principal:             func(context.Context) string { return "user:t20-closure" },
		AuthorizationProvider: "t20-closure-permissions-v1",
		Visible: func(context.Context) func(store.Repo) bool {
			return func(store.Repo) bool { return visible }
		},
	}
	catalog := api.NewContractCatalogService(opts)
	callers := api.NewLegacyCallerMapService(opts)
	comparison := api.NewCallerComparisonService(opts)
	if catalog == nil || callers == nil || comparison == nil {
		t.Fatal("T20.14 shared services were not constructed")
	}

	// Discovery supplies both endpoint identities. No canonical operation is
	// entered into the harness or copied from the oracle.
	started = time.Now()
	atlas, err := catalog.List(ctx, api.ContractCatalogQuery{
		Repository: repository,
		Protocol:   "protobuf",
	}, 100, "")
	measurements.AtlasWallMilliseconds = time.Since(started).Milliseconds()
	if err != nil {
		t.Fatalf("discover endpoints in Atlas: %v", err)
	}
	oldEndpoint, replacementEndpoint := discoverT2014Endpoints(t, atlas)
	measurements.DiscoveredOperations = []string{
		oldEndpoint.Operation, replacementEndpoint.Operation,
	}

	started = time.Now()
	firstPage, err := callers.List(ctx, api.CallerMapQuery{
		Endpoint: oldEndpoint,
		Ordering: "source",
	}, 100, "")
	measurements.CallerMapWallMilliseconds = time.Since(started).Milliseconds()
	if err != nil {
		t.Fatalf("page discovered endpoint callers: %v", err)
	}
	if len(firstPage.Rows) != 100 || firstPage.TotalMatchingRows == nil ||
		*firstPage.TotalMatchingRows < t201.ScaleLogicalUnits ||
		firstPage.Pagination.NextCursor == "" {
		t.Fatalf("scale Caller Map first page = %d/%d, cursor=%t",
			len(firstPage.Rows), *firstPage.TotalMatchingRows,
			firstPage.Pagination.NextCursor != "")
	}
	citation := firstPage.Rows[0].Source
	if citation.Repository != repository || citation.Commit != healthyCommit ||
		citation.Path == "" || citation.EndByte <= citation.StartByte ||
		citation.StartLine < 1 {
		t.Fatalf("Caller Map did not reach an immutable citation: %+v", citation)
	}
	measurements.CallerMapRows = len(firstPage.Rows)
	measurements.CallerMapTotalRows = *firstPage.TotalMatchingRows
	measurements.CitedCaller = t2014Citation{
		Repository: "<generated-local-profile>", Commit: citation.Commit,
		Path: citation.Path, StartByte: citation.StartByte,
		EndByte: citation.EndByte, StartLine: citation.StartLine,
		EndLine: citation.EndLine,
	}

	started = time.Now()
	comparisonPage, err := comparison.Compare(ctx, api.CallerComparisonQuery{
		Old: oldEndpoint, Replacement: replacementEndpoint,
		Ordering: "source", Level: "occurrence",
	}, 100, "")
	measurements.ComparisonWallMilliseconds = time.Since(started).Milliseconds()
	if err != nil {
		t.Fatalf("compare discovered endpoints: %v", err)
	}
	if len(comparisonPage.Rows) != 100 ||
		comparisonPage.TotalRows < t201.ScaleLogicalUnits ||
		comparisonPage.Pagination.NextCursor == "" {
		t.Fatalf("scale comparison first page = %d/%d, cursor=%t",
			len(comparisonPage.Rows), comparisonPage.TotalRows,
			comparisonPage.Pagination.NextCursor != "")
	}
	measurements.ComparisonRows = len(comparisonPage.Rows)
	measurements.ComparisonTotalRows = comparisonPage.TotalRows

	// Advance the immutable repository to a malformed unit snapshot. The
	// declaration domain may publish independently, but the caller domain
	// must abort and leave its healthy publication intact.
	if err := os.WriteFile(
		filepath.Join(origin, "unit-snapshot.json"),
		[]byte("{\"version\":\"t20-unit-snapshot-v1\",\"mappings\":[\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	runGit(t, origin, "add", "unit-snapshot.json")
	runGit(t, origin, "commit", "-m", "inject malformed unit snapshot")
	malformedCommit := gitOutput(t, origin, "rev-parse", "HEAD")
	if malformedCommit == healthyCommit {
		t.Fatal("malformed fixture did not advance the immutable commit")
	}
	if _, err := phebssync.SyncConnection(ctx, st, dataDir, connection, nil); err != nil {
		t.Fatalf("sync malformed revision: %v", err)
	}
	if err := ix.Index(ctx, store.Repo{Name: repository}, true); err != nil {
		t.Fatalf("index malformed revision: %v", err)
	}
	malformedWorker := &extract.Worker{
		Repos: st, Evidence: st, NewCorpus: extract.GitCorpus(dataDir),
		Extractors: []extract.Extractor{protodecl.New(), gocaller.NewGRPC()},
	}
	malformedErr := malformedWorker.Handle(ctx, store.Job{
		Kind: store.JobExtract, Target: repository,
	})
	if malformedErr == nil || !strings.Contains(malformedErr.Error(), "unit-snapshot.json") {
		t.Fatalf("malformed attribution snapshot did not fail closed: %v", malformedErr)
	}
	callerRun, err := st.LatestPublishedRun(ctx, store.ExtractionScope{
		Repository: repository,
		Commit:     healthyCommit,
		Domain:     "grpc-caller",
	})
	if err != nil || callerRun.Commit != healthyCommit {
		t.Fatalf("malformed replacement displaced healthy caller run: %+v, %v",
			callerRun, err)
	}
	attempt, err := st.LatestExtractionAttempt(ctx, store.ExtractionScope{
		Repository: repository,
		Commit:     malformedCommit,
		Domain:     "grpc-caller",
	})
	if err != nil || attempt.Commit != malformedCommit || attempt.Status != "aborted" {
		t.Fatalf("malformed caller attempt = %+v, %v", attempt, err)
	}
	measurements.MalformedUnitSnapshotRefused = true

	_, err = callers.List(ctx, api.CallerMapQuery{
		Endpoint: oldEndpoint,
		Ordering: "source",
	}, 100, firstPage.Pagination.NextCursor)
	assertStatusError(t, err, http.StatusConflict)
	measurements.CursorInvalidated = true

	visible = false
	_, err = callers.List(ctx, api.CallerMapQuery{
		Endpoint: oldEndpoint,
		Ordering: "source",
	}, 100, "")
	assertStatusError(t, err, http.StatusNotFound)
	measurements.PermissionRevocationRefused = true

	measurements.StoreReceipt = loadT2014StoreReceipt(t)
	measurements.DOM = t2014DOMMeasurement{
		Source:                   "CallerMapPage and CallerComparisonPage Vitest profiles",
		ServerPageRows:           100,
		MountedCallerRows:        100,
		MountedComparisonRows:    100,
		CallerProfileTotalRows:   10_000,
		AccumulatedPriorPageRows: 0,
	}
	writeT2014Measurements(t, measurements)
}

type t2014FailingExtractor struct{}

func (t2014FailingExtractor) Domain() string  { return "t20-injected-failure" }
func (t2014FailingExtractor) Version() string { return "1.0.0" }
func (t2014FailingExtractor) Candidate(string) bool {
	return false
}
func (t2014FailingExtractor) Extract(
	context.Context,
	sdk.Corpus,
	sdk.Emit,
) (sdk.Coverage, error) {
	return sdk.Coverage{Failures: []string{"injected-domain-failure"}},
		errors.New("injected-domain-failure")
}

type t2014Measurements struct {
	Schema                       string              `json:"schema"`
	GoVersion                    string              `json:"go_version"`
	GOOS                         string              `json:"goos"`
	GOARCH                       string              `json:"goarch"`
	GOMAXPROCS                   int                 `json:"gomaxprocs"`
	Profile                      string              `json:"profile"`
	ManifestDigest               string              `json:"manifest_digest"`
	ProfileFiles                 int                 `json:"profile_files"`
	Calls                        int                 `json:"calls"`
	UnitMappings                 int                 `json:"unit_mappings"`
	SyncWallMilliseconds         int64               `json:"sync_wall_ms"`
	IndexWallMilliseconds        int64               `json:"index_wall_ms"`
	ExtractWallMilliseconds      int64               `json:"extract_wall_ms"`
	AtlasWallMilliseconds        int64               `json:"atlas_wall_ms"`
	CallerMapWallMilliseconds    int64               `json:"caller_map_wall_ms"`
	ComparisonWallMilliseconds   int64               `json:"comparison_wall_ms"`
	CallerMapRows                int                 `json:"caller_map_rows"`
	CallerMapTotalRows           int                 `json:"caller_map_total_rows"`
	ComparisonRows               int                 `json:"comparison_rows"`
	ComparisonTotalRows          int                 `json:"comparison_total_rows"`
	DiscoveredOperations         []string            `json:"discovered_operations"`
	CitedCaller                  t2014Citation       `json:"cited_caller"`
	InjectedDomainFailure        bool                `json:"injected_domain_failure"`
	MalformedUnitSnapshotRefused bool                `json:"malformed_unit_snapshot_refused"`
	CursorInvalidated            bool                `json:"cursor_invalidated"`
	PermissionRevocationRefused  bool                `json:"permission_revocation_refused"`
	StoreReceipt                 t2014StoreReceipt   `json:"store_receipt"`
	DOM                          t2014DOMMeasurement `json:"dom"`
}

type t2014Citation struct {
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
	Path       string `json:"path"`
	StartByte  int    `json:"start_byte"`
	EndByte    int    `json:"end_byte"`
	StartLine  int    `json:"start_line"`
	EndLine    int    `json:"end_line"`
}

type t2014StoreReceipt struct {
	Path                      string `json:"path"`
	SHA256                    string `json:"sha256"`
	Schema                    string `json:"schema"`
	PublishWallMilliseconds   int64  `json:"publish_wall_ms"`
	SweepWallMilliseconds     int64  `json:"sweep_wall_ms"`
	SweepSteps                int    `json:"sweep_steps"`
	TargetRows                int    `json:"target_rows"`
	CompleteSweepVerified     bool   `json:"complete_sweep_verified"`
	AtomicPublicationVerified bool   `json:"atomic_publication_verified"`
}

type t2014DOMMeasurement struct {
	Source                   string `json:"source"`
	ServerPageRows           int    `json:"server_page_rows"`
	MountedCallerRows        int    `json:"mounted_caller_rows"`
	MountedComparisonRows    int    `json:"mounted_comparison_rows"`
	CallerProfileTotalRows   int    `json:"caller_profile_total_rows"`
	AccumulatedPriorPageRows int    `json:"accumulated_prior_page_rows"`
}

func discoverT2014Endpoints(
	t *testing.T,
	atlas *api.ContractCatalogList,
) (api.CallerMapEndpoint, api.CallerMapEndpoint) {
	t.Helper()
	var operations []api.CallerMapEndpoint
	for _, item := range atlas.Items {
		if item.Kind != "operation" ||
			item.Protocol != "protobuf" ||
			item.Repository == "" ||
			item.Lineage == "" ||
			item.Operation == "" {
			continue
		}
		operations = append(operations, api.CallerMapEndpoint{
			Protocol: item.Protocol, Repository: item.Repository,
			Lineage: item.Lineage, Operation: item.Operation,
		})
	}
	slices.SortFunc(operations, func(left, right api.CallerMapEndpoint) int {
		return strings.Compare(left.Operation, right.Operation)
	})
	if len(operations) < 2 {
		t.Fatalf("Atlas discovered %d usable protobuf operations: %+v",
			len(operations), atlas.Items)
	}
	var replacementEndpoint api.CallerMapEndpoint
	for _, endpoint := range operations {
		if strings.HasSuffix(endpoint.Operation, "/Watch") {
			replacementEndpoint = endpoint
			break
		}
	}
	var oldEndpoint api.CallerMapEndpoint
	for _, endpoint := range operations {
		if endpoint.Repository == replacementEndpoint.Repository &&
			endpoint.Lineage == replacementEndpoint.Lineage &&
			strings.HasSuffix(endpoint.Operation, "/Get") {
			oldEndpoint = endpoint
			break
		}
	}
	if oldEndpoint.Operation == "" || replacementEndpoint.Operation == "" {
		t.Fatalf("Atlas did not discover the neutral Get/Watch pair: %+v", operations)
	}
	return oldEndpoint, replacementEndpoint
}

func assertStatusError(t *testing.T, err error, status int) {
	t.Helper()
	statusErr, ok := err.(huma.StatusError)
	if !ok || statusErr.GetStatus() != status {
		t.Fatalf("error = %v (%T), want HTTP %d", err, err, status)
	}
}

func initRepository(t *testing.T, directory string) {
	t.Helper()
	runGit(t, directory, "init", "-b", "main")
	runGit(t, directory, "add", "--all")
	runGit(t, directory, "commit", "-m", "T20.14 generated scale profile")
}

func runGit(t *testing.T, directory string, arguments ...string) {
	t.Helper()
	command := exec.CommandContext(t.Context(), "git", append([]string{
		"-c", "commit.gpgsign=false",
		"-c", "user.name=T20 Closure",
		"-c", "user.email=t20-closure@example.invalid",
		"-C", directory,
	}, arguments...)...)
	command.Env = append(os.Environ(),
		"GIT_AUTHOR_DATE=2026-07-26T12:00:00Z",
		"GIT_COMMITTER_DATE=2026-07-26T12:00:00Z",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
}

func gitOutput(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.CommandContext(t.Context(), "git", append([]string{
		"-C", directory,
	}, arguments...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func writeT2014Measurements(t *testing.T, measurements t2014Measurements) {
	t.Helper()
	encoded, err := json.MarshalIndent(measurements, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("T20.14 closure measurement:\n%s", encoded)
	if target := strings.TrimSpace(os.Getenv(t2014ResultsEnv)); target != "" {
		if err := os.WriteFile(target, append(encoded, '\n'), 0o600); err != nil {
			t.Fatalf("write %s: %v", t2014ResultsEnv, err)
		}
	}
}

func TestT2014ClosureGateConstants(t *testing.T) {
	if t201.ScaleLogicalUnits != 10_000 ||
		t201.ScaleTotalCalls != 10_010 ||
		t201.ScaleTotalMappings != 10_005 {
		t.Fatal("T20.14 frozen scale population changed; review and rerun the live gate")
	}
	_ = loadT2014StoreReceipt(t)
	_ = loadT2014ClosureReceipt(t)
}

func TestT2014ClosureMeasurementJSONShape(t *testing.T) {
	value := t2014Measurements{
		Schema: "t20-closure-measurement-v1",
		CitedCaller: t2014Citation{
			Repository: "local/example", Commit: strings.Repeat("a", 40),
			Path: "src/caller.go", StartByte: 1, EndByte: 2,
			StartLine: 1, EndLine: 1,
		},
		StoreReceipt: t2014StoreReceipt{CompleteSweepVerified: true},
		DOM:          t2014DOMMeasurement{ServerPageRows: 100},
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{
		`"schema":"t20-closure-measurement-v1"`,
		`"cited_caller"`,
		`"store_receipt"`,
		`"dom"`,
	} {
		if !strings.Contains(string(encoded), field) {
			t.Fatalf("measurement omitted %s: %s", field, encoded)
		}
	}
}

func loadT2014StoreReceipt(t *testing.T) t2014StoreReceipt {
	t.Helper()
	const receiptName = "spike/t201/results-current-writer-v7.json"
	raw, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(receiptName)))
	if err != nil {
		t.Fatalf("read current writer receipt: %v", err)
	}
	var stored struct {
		Schema                      string `json:"schema"`
		TargetRows                  int    `json:"target_rows"`
		PublishWallMilliseconds     int64  `json:"publish_wall_ms"`
		SweepWallMilliseconds       int64  `json:"sweep_wall_ms"`
		SweepSteps                  int    `json:"sweep_steps"`
		AtomicSupersessionVerified  bool   `json:"atomic_supersession_verified"`
		CompleteTargetSweepVerified bool   `json:"complete_target_sweep_verified"`
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatalf("decode current writer receipt: %v", err)
	}
	if stored.Schema != "t20-store-measurement-v3" ||
		stored.TargetRows != 20_020 ||
		stored.PublishWallMilliseconds != 166 ||
		stored.SweepWallMilliseconds != 1_897 ||
		stored.SweepSteps != 42 ||
		!stored.AtomicSupersessionVerified ||
		!stored.CompleteTargetSweepVerified {
		t.Fatalf("current writer receipt changed; review and rerun T20.14: %+v", stored)
	}
	sum := sha256.Sum256(raw)
	return t2014StoreReceipt{
		Path:                      receiptName,
		SHA256:                    "sha256:" + fmt.Sprintf("%x", sum),
		Schema:                    stored.Schema,
		PublishWallMilliseconds:   stored.PublishWallMilliseconds,
		SweepWallMilliseconds:     stored.SweepWallMilliseconds,
		SweepSteps:                stored.SweepSteps,
		TargetRows:                stored.TargetRows,
		CompleteSweepVerified:     stored.CompleteTargetSweepVerified,
		AtomicPublicationVerified: stored.AtomicSupersessionVerified,
	}
}

func loadT2014ClosureReceipt(t *testing.T) t2014Measurements {
	t.Helper()
	const (
		receiptName = "spike/t201/results-t20.14.json"
		receiptHash = "bad98140f0974a5f929355390d4b9bbb538d8f503d62421ca20fa2888046e1f2"
	)
	raw, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(receiptName)))
	if err != nil {
		t.Fatalf("read T20.14 closure receipt: %v", err)
	}
	sum := sha256.Sum256(raw)
	if fmt.Sprintf("%x", sum) != receiptHash {
		t.Fatalf("T20.14 closure receipt changed; review and rerun make t20-closure")
	}
	var stored t2014Measurements
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatalf("decode T20.14 closure receipt: %v", err)
	}
	profile, err := t201.GenerateProfile(t201.ScaleProfileName)
	if err != nil {
		t.Fatalf("regenerate T20.14 profile: %v", err)
	}
	if stored.Schema != "t20-closure-measurement-v1" ||
		stored.Profile != t201.ScaleProfileName ||
		stored.ManifestDigest != t201.ManifestDigest(profile.Files) ||
		stored.ProfileFiles != 225 ||
		stored.Calls != t201.ScaleTotalCalls ||
		stored.UnitMappings != t201.ScaleTotalMappings ||
		stored.CallerMapRows != 100 ||
		stored.CallerMapTotalRows != 10_004 ||
		stored.ComparisonRows != 100 ||
		stored.ComparisonTotalRows != 10_004 ||
		!slices.Equal(stored.DiscoveredOperations, []string{
			"/synthetic.orders.v1.Orders/Get",
			"/synthetic.orders.v1.Orders/Watch",
		}) ||
		stored.CitedCaller.Commit == "" ||
		stored.CitedCaller.Path == "" ||
		!stored.InjectedDomainFailure ||
		!stored.MalformedUnitSnapshotRefused ||
		!stored.CursorInvalidated ||
		!stored.PermissionRevocationRefused ||
		stored.StoreReceipt.SHA256 != "sha256:f4b7e4e591797c2672049b135a202ffde0ce868ced69a6fdd02ee4a45adb963b" ||
		stored.DOM.ServerPageRows != 100 ||
		stored.DOM.MountedCallerRows != 100 ||
		stored.DOM.MountedComparisonRows != 100 ||
		stored.DOM.CallerProfileTotalRows != 10_000 ||
		stored.DOM.AccumulatedPriorPageRows != 0 {
		t.Fatalf("T20.14 closure receipt is inconsistent: %+v", stored)
	}
	return stored
}
