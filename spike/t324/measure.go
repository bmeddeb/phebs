package t324

import (
	"bytes"
	"context"
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/index"
	"github.com/sourcegraph/zoekt/query"
	zoektsearch "github.com/sourcegraph/zoekt/search"
	"golang.org/x/mod/modfile"

	"github.com/bmeddeb/phebs/spike/t322"
	"github.com/bmeddeb/phebs/spike/t323"
)

const (
	repositoryName = "example.invalid/t324-neutral-direct"
	queryTopK      = 3
)

func Measure(ctx context.Context, root, zoektBinary string) (Receipt, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return Receipt{}, err
	}
	zoektBinary, err = filepath.Abs(zoektBinary)
	if err != nil {
		return Receipt{}, err
	}
	if err := verifyZoektBinary(zoektBinary, root); err != nil {
		return Receipt{}, err
	}
	inputs, baseline, err := readInputs(root)
	if err != nil {
		return Receipt{}, err
	}
	binaryBytes, err := os.ReadFile(zoektBinary)
	if err != nil {
		return Receipt{}, fmt.Errorf("read zoekt binary: %w", err)
	}
	temporary, err := os.MkdirTemp("", "phebs-t324-measure-*")
	if err != nil {
		return Receipt{}, err
	}
	defer func() { _ = os.RemoveAll(temporary) }()

	correctness, neutral, err := measureNeutral(ctx, root, temporary, zoektBinary)
	if err != nil {
		return Receipt{}, err
	}
	profiles := make([]ProfileMeasurement, 0, 2)
	for _, services := range []int{1_000, 5_000} {
		measurement, err := measureProfile(ctx, temporary, zoektBinary, services)
		if err != nil {
			return Receipt{}, fmt.Errorf("measure %d-service profile: %w", services, err)
		}
		profiles = append(profiles, measurement)
	}
	receipt := Receipt{
		Schema: ReceiptSchema, MeasuredOn: "2026-08-04", Inputs: inputs,
		Environment: Environment{
			GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, GoVersion: runtime.Version(),
			LogicalCPUs: runtime.NumCPU(), ZoektSHA256: SHA256(binaryBytes), CtagsDisabled: true,
		},
		Trigger: TriggerDecision{
			BaselineOutcome: baseline.Outcome, IndexOutcome: baseline.Index.Outcome,
			SearchOutcome: baseline.Search.Outcome, Triggered: false,
			Reason: "T32.2 completed its prospectively frozen direct whole-repository envelope",
		},
		Correctness: correctness, NeutralCost: neutral, Profiles: profiles,
		Decisions: []TopologyDecision{
			{Topology: "direct", Decision: "go", Reason: "target baseline completed and the neutral oracle is exact with pre-ranking service predicates", NextGate: "T32.5"},
			{Topology: "bounded-cohorts", Decision: "no_go", Reason: "the prospectively frozen cohort trigger was not met; cohort duplication and top-K merge cost remain unmeasured", NextGate: "reopen only after a named direct-topology limit fails"},
			{Topology: "p6", Decision: "no_go", Reason: "no single-node capacity or correctness failure triggered fleet escalation", NextGate: "demand-driven P6 review"},
		},
		Claims: Claims{SourceFree: true, SelectsInitialTopology: true},
	}
	if err := ValidateReceipt(receipt); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

func readInputs(root string) (Inputs, t322.Receipt, error) {
	t322Bytes, err := os.ReadFile(filepath.Join(root, "spike/t322/results.json"))
	if err != nil {
		return Inputs{}, t322.Receipt{}, err
	}
	var baseline t322.Receipt
	if err := json.Unmarshal(t322Bytes, &baseline); err != nil {
		return Inputs{}, t322.Receipt{}, err
	}
	if baseline.Schema != t322.ReceiptSchema || baseline.Outcome != "completed" ||
		baseline.Index.Outcome != "succeeded" || baseline.Search.Outcome != "succeeded" ||
		!baseline.Claims.SourceFree || baseline.Claims.SelectsTopology {
		return Inputs{}, t322.Receipt{}, errors.New("T32.2 input is not the completed source-free baseline")
	}
	t323Bytes, err := os.ReadFile(filepath.Join(root, "spike/t323/receipt.json"))
	if err != nil {
		return Inputs{}, t322.Receipt{}, err
	}
	t323Receipt, err := t323.DecodeStrict[t323.Receipt](t323Bytes)
	if err != nil {
		return Inputs{}, t322.Receipt{}, err
	}
	if t323Receipt.Schema != t323.ReceiptSchema || len(t323Receipt.Revisions) != 5 ||
		!t323Receipt.Claims.Synthetic || t323Receipt.Claims.EstablishesTargetSLO ||
		t323Receipt.Claims.EstablishesAccuracy || t323Receipt.Claims.SelectsTopology ||
		t323Receipt.Claims.ProductionRegistration {
		return Inputs{}, t322.Receipt{}, errors.New("T32.3 input is not the closed neutral corpus receipt")
	}
	bundleBytes, err := os.ReadFile(filepath.Join(root, "spike/t323/t323-neutral-corpus.bundle"))
	if err != nil {
		return Inputs{}, t322.Receipt{}, err
	}
	if got := t323.SHA256(bundleBytes); got != t323Receipt.Bundle.SHA256 {
		return Inputs{}, t322.Receipt{}, fmt.Errorf("T32.3 bundle digest %s differs from receipt %s", got, t323Receipt.Bundle.SHA256)
	}
	return Inputs{
		T322ResultsSHA256: SHA256(t322Bytes), T323ReceiptSHA256: SHA256(t323Bytes),
		T323BundleSHA256: SHA256(bundleBytes),
	}, baseline, nil
}

func verifyZoektBinary(binary, root string) error {
	candidate, err := buildinfo.ReadFile(binary)
	if err != nil {
		return fmt.Errorf("read zoekt build metadata: %w", err)
	}
	if candidate.Path != "github.com/sourcegraph/zoekt/cmd/zoekt-git-index" {
		return fmt.Errorf("unexpected zoekt binary package %q", candidate.Path)
	}
	goModBytes, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return fmt.Errorf("read current go.mod: %w", err)
	}
	parsed, err := modfile.Parse("go.mod", goModBytes, nil)
	if err != nil {
		return fmt.Errorf("parse current go.mod: %w", err)
	}
	expectedVersion := ""
	for _, requirement := range parsed.Require {
		if requirement.Mod.Path == "github.com/sourcegraph/zoekt" {
			expectedVersion = requirement.Mod.Version
			break
		}
	}
	if expectedVersion == "" {
		return errors.New("current go.mod omits the zoekt module pin")
	}
	goSumBytes, err := os.ReadFile(filepath.Join(root, "go.sum"))
	if err != nil {
		return fmt.Errorf("read current go.sum: %w", err)
	}
	expectedSum := ""
	for _, line := range strings.Split(string(goSumBytes), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[0] == "github.com/sourcegraph/zoekt" && fields[1] == expectedVersion {
			expectedSum = fields[2]
			break
		}
	}
	if expectedSum == "" {
		return errors.New("current go.sum omits the zoekt module checksum")
	}
	if candidate.Main.Path != "github.com/sourcegraph/zoekt" ||
		candidate.Main.Version != expectedVersion || candidate.Main.Sum != expectedSum {
		return fmt.Errorf(
			"zoekt binary module %s@%s %s differs from current pin %s@%s %s",
			candidate.Main.Path, candidate.Main.Version, candidate.Main.Sum,
			"github.com/sourcegraph/zoekt", expectedVersion, expectedSum,
		)
	}
	return nil
}

func measureNeutral(
	ctx context.Context,
	root, temporary, zoektBinary string,
) (Correctness, NeutralCost, error) {
	snapshots, err := t323.SmallSnapshots()
	if err != nil {
		return Correctness{}, NeutralCost{}, err
	}
	receiptBytes, err := os.ReadFile(filepath.Join(root, "spike/t323/receipt.json"))
	if err != nil {
		return Correctness{}, NeutralCost{}, err
	}
	fixtureReceipt, err := t323.DecodeStrict[t323.Receipt](receiptBytes)
	if err != nil {
		return Correctness{}, NeutralCost{}, err
	}
	repository := filepath.Join(temporary, "neutral-repository")
	bundle := filepath.Join(root, "spike/t323/t323-neutral-corpus.bundle")
	if _, err := runGit("", "clone", "-q", bundle, repository); err != nil {
		return Correctness{}, NeutralCost{}, err
	}
	branches := make([]string, 0, len(fixtureReceipt.Revisions))
	for _, revision := range fixtureReceipt.Revisions {
		if _, err := runGit(repository, "branch", revision.Revision, revision.Commit); err != nil {
			return Correctness{}, NeutralCost{}, err
		}
		branches = append(branches, revision.Revision)
	}
	if _, err := runGit(repository, "config", "zoekt.name", repositoryName); err != nil {
		return Correctness{}, NeutralCost{}, err
	}
	indexDir := filepath.Join(temporary, "neutral-index")
	build, err := buildZoekt(ctx, zoektBinary, repository, indexDir, branches)
	if err != nil {
		return Correctness{}, NeutralCost{}, err
	}
	baseDescriptors, descriptorMethod := descriptorCount()
	searcher, err := zoektsearch.NewDirectorySearcher(indexDir)
	if err != nil {
		return Correctness{}, NeutralCost{}, err
	}
	openDescriptors, openMethod := descriptorCount()
	if openMethod != descriptorMethod {
		searcher.Close()
		return Correctness{}, NeutralCost{}, errors.New("descriptor measurement method changed while opening reader")
	}
	defer searcher.Close()
	reader := ReaderMeasurement{
		MMapCount: build.ShardCount, MMapMethod: "one-zoekt-mmap-per-visible-shard",
		DescriptorBase: baseDescriptors, DescriptorOpen: openDescriptors,
		DescriptorDelta:  openDescriptors - baseDescriptors,
		DescriptorMethod: descriptorMethod,
	}

	coldStart := time.Now()
	correctness, coldResults, err := executeNeutralQueries(ctx, searcher, snapshots)
	if err != nil {
		return Correctness{}, NeutralCost{}, err
	}
	coldWall := time.Since(coldStart)
	warmStart := time.Now()
	warmCorrectness, warmResults, err := executeNeutralQueries(ctx, searcher, snapshots)
	if err != nil {
		return Correctness{}, NeutralCost{}, err
	}
	warmWall := time.Since(warmStart)
	if correctness != warmCorrectness || !slices.Equal(coldResults, warmResults) {
		return Correctness{}, NeutralCost{}, errors.New("cold and warm neutral query results differ")
	}
	correctness.AdversarialRankingStable, correctness.TopKEqual, err = checkRankingAndTopK(ctx, searcher, snapshots[0])
	if err != nil {
		return Correctness{}, NeutralCost{}, err
	}
	correctness.RevisionBound, correctness.PriorRevisionRepeatable, correctness.BranchABASelectionEqual, err =
		checkBranchSelection(ctx, searcher)
	if err != nil {
		return Correctness{}, NeutralCost{}, err
	}
	return correctness, NeutralCost{
		Build: build, Reader: reader, ColdQueryMS: coldWall.Milliseconds(),
		WarmQueryMS: warmWall.Milliseconds(), QueryCount: correctness.ExecutedQueries,
		ResultDigest: digestStrings(coldResults),
	}, nil
}

func executeNeutralQueries(
	ctx context.Context,
	searcher zoekt.Searcher,
	snapshots []t323.Snapshot,
) (Correctness, []string, error) {
	byRevision := make(map[string]t323.Snapshot, len(snapshots))
	for _, snapshot := range snapshots {
		byRevision[snapshot.Revision] = snapshot
	}
	result := Correctness{
		Revisions: len(snapshots), AllCodeEqual: true, ServiceEqual: true,
		PredicatesInsideZoekt: true, BroadQueriesEqual: true,
	}
	var observed []string
	for _, snapshot := range snapshots {
		for _, expectation := range snapshot.Oracle.Queries {
			result.OracleQueries++
			if expectation.State == "not_found" || expectation.State == "unavailable" {
				if len(expectation.Paths) != 0 {
					return Correctness{}, nil, fmt.Errorf("%s/%s authority-empty query has paths", snapshot.Revision, expectation.ID)
				}
				if expectation.State == "not_found" {
					if placements := catalogPlacements(snapshot.Catalog, expectation.ServiceKey, expectation.Principal); len(placements) != 0 {
						return Correctness{}, nil, fmt.Errorf("%s/%s not-found query has admitted catalog placements", snapshot.Revision, expectation.ID)
					}
				} else {
					state, ok := serviceState(snapshot.Oracle, expectation.ServiceKey)
					if !ok || state.Search != "unavailable" {
						return Correctness{}, nil, fmt.Errorf("%s/%s unavailable query lacks unavailable service state", snapshot.Revision, expectation.ID)
					}
				}
				result.AuthorityEmptyQueries++
				continue
			}
			activeRevision := snapshot.Revision
			if state, ok := serviceState(snapshot.Oracle, expectation.ServiceKey); ok && state.Search == "stale" {
				activeRevision = state.ActiveRevision
			}
			var compiled query.Q
			var err error
			if expectation.Scope == "all-code" {
				compiled, err = compileAllCodeQuery(expectation.Expression, activeRevision)
			} else {
				active, ok := byRevision[activeRevision]
				if !ok {
					return Correctness{}, nil, fmt.Errorf("unknown active revision %q", activeRevision)
				}
				placements := catalogPlacements(active.Catalog, expectation.ServiceKey, expectation.Principal)
				compiled, err = compileServiceQuery(expectation.Expression, activeRevision, placements)
				if err == nil {
					result.PredicatesInsideZoekt = result.PredicatesInsideZoekt && queryContainsPathPredicate(compiled)
				}
			}
			if err != nil {
				return Correctness{}, nil, fmt.Errorf("%s/%s: %w", snapshot.Revision, expectation.ID, err)
			}
			paths, err := searchPaths(ctx, searcher, compiled, 10_000)
			if err != nil {
				return Correctness{}, nil, err
			}
			want := slices.Clone(expectation.Paths)
			slices.Sort(want)
			if !slices.Equal(paths, want) {
				if expectation.Scope == "all-code" {
					result.AllCodeEqual = false
				} else {
					result.ServiceEqual = false
				}
				return Correctness{}, nil, fmt.Errorf("%s/%s paths %v, want %v", snapshot.Revision, expectation.ID, paths, want)
			}
			result.ExecutedQueries++
			for _, path := range paths {
				observed = append(observed, snapshot.Revision+"/"+expectation.ID+"/"+path)
			}
		}
		broad, err := compileAllCodeQuery("package", snapshot.Revision)
		if err != nil {
			return Correctness{}, nil, err
		}
		got, err := searchPaths(ctx, searcher, broad, 10_000)
		if err != nil {
			return Correctness{}, nil, err
		}
		want := pathsContaining(snapshot.Files, []byte("package"))
		if !slices.Equal(got, want) {
			result.BroadQueriesEqual = false
			return Correctness{}, nil, fmt.Errorf("%s broad paths %v, want %v", snapshot.Revision, got, want)
		}
	}
	result.AuthorityAdmissionChecked = true
	result.PerRevisionCatalogEqual = true
	return result, observed, nil
}

func checkRankingAndTopK(ctx context.Context, searcher zoekt.Searcher, snapshot t323.Snapshot) (bool, bool, error) {
	// A branch-bound TRUE query makes every document equally eligible and is
	// the adversarial ranking-tie case. It is constructed directly because the
	// user query grammar intentionally rejects an empty/bare scope query.
	compiled := query.NewAnd(
		&query.Branch{Pattern: snapshot.Revision, Exact: true},
		&query.Const{Value: true},
	)
	full, err := searcher.Search(ctx, compiled, &zoekt.SearchOptions{
		TotalMaxMatchCount: 100_000, MaxDocDisplayCount: 10_000,
	})
	if err != nil {
		return false, false, err
	}
	repeated, err := searcher.Search(ctx, compiled, &zoekt.SearchOptions{
		TotalMaxMatchCount: 100_000, MaxDocDisplayCount: 10_000,
	})
	if err != nil {
		return false, false, err
	}
	stable := len(full.Files) >= queryTopK && slices.Equal(orderedPaths(full.Files), orderedPaths(repeated.Files))
	limit := min(queryTopK, len(full.Files))
	truncated, err := searcher.Search(ctx, compiled, &zoekt.SearchOptions{
		TotalMaxMatchCount: 100_000, MaxDocDisplayCount: limit,
	})
	if err != nil {
		return false, false, err
	}
	fullPaths, truncatedPaths := orderedPaths(full.Files[:limit]), orderedPaths(truncated.Files)
	return stable, slices.Equal(fullPaths, truncatedPaths), nil
}

func checkBranchSelection(ctx context.Context, searcher zoekt.Searcher) (bool, bool, bool, error) {
	r0Billing, err := searchOrdered(ctx, searcher, "T323_BILLING_CALLER", "r0-baseline", 10)
	if err != nil {
		return false, false, false, err
	}
	r4Billing, err := searchOrdered(ctx, searcher, "T323_BILLING_CALLER", "r4-removal", 10)
	if err != nil {
		return false, false, false, err
	}
	revisionBound := len(r0Billing) == 1 && len(r4Billing) == 0
	priorRepeated, err := searchOrdered(ctx, searcher, "T323_BILLING_CALLER", "r0-baseline", 10)
	if err != nil {
		return false, false, false, err
	}
	priorRepeatable := slices.Equal(r0Billing, priorRepeated)
	a, err := searchOrdered(ctx, searcher, "package", "r0-baseline", 10_000)
	if err != nil {
		return false, false, false, err
	}
	b, err := searchOrdered(ctx, searcher, "package", "r1-rename", 10_000)
	if err != nil {
		return false, false, false, err
	}
	aAgain, err := searchOrdered(ctx, searcher, "package", "r0-baseline", 10_000)
	if err != nil {
		return false, false, false, err
	}
	return revisionBound, priorRepeatable, !slices.Equal(a, b) && slices.Equal(a, aAgain), nil
}

func measureProfile(
	ctx context.Context,
	temporary, zoektBinary string,
	serviceCount int,
) (ProfileMeasurement, error) {
	profile, err := t323.GenerateLoadProfile(serviceCount)
	if err != nil {
		return ProfileMeasurement{}, err
	}
	contents, err := t323.GenerateLoadProfileContents(serviceCount)
	if err != nil {
		return ProfileMeasurement{}, err
	}
	if err := verifyProfileContents(profile, contents); err != nil {
		return ProfileMeasurement{}, err
	}
	profileRoot := filepath.Join(temporary, fmt.Sprintf("profile-%d", serviceCount))
	repository := filepath.Join(profileRoot, "repository")
	if err := materializeRepository(repository, contents); err != nil {
		return ProfileMeasurement{}, err
	}
	indexDir := filepath.Join(profileRoot, "index")
	build, err := buildZoekt(ctx, zoektBinary, repository, indexDir, []string{"main"})
	if err != nil {
		return ProfileMeasurement{}, err
	}
	baseDescriptors, descriptorMethod := descriptorCount()
	searcher, err := zoektsearch.NewDirectorySearcher(indexDir)
	if err != nil {
		return ProfileMeasurement{}, err
	}
	openDescriptors, openMethod := descriptorCount()
	if openMethod != descriptorMethod {
		searcher.Close()
		return ProfileMeasurement{}, errors.New("descriptor measurement method changed while opening reader")
	}
	queries, err := measureProfileQueries(ctx, searcher, profile)
	searcher.Close()
	if err != nil {
		return ProfileMeasurement{}, err
	}
	reader := ReaderMeasurement{
		MMapCount: build.ShardCount, MMapMethod: "one-zoekt-mmap-per-visible-shard",
		DescriptorBase: baseDescriptors, DescriptorOpen: openDescriptors,
		DescriptorDelta:  openDescriptors - baseDescriptors,
		DescriptorMethod: descriptorMethod,
	}
	predicates, err := measurePredicates(profile)
	if err != nil {
		return ProfileMeasurement{}, err
	}
	editPath := filepath.Join(repository, "services/service-00000/main.go")
	original, err := os.ReadFile(editPath)
	if err != nil {
		return ProfileMeasurement{}, err
	}
	if err := os.WriteFile(editPath, append(original, []byte("\nconst T324Edited = true\n")...), 0o644); err != nil {
		return ProfileMeasurement{}, err
	}
	if _, err := runGit(repository, "add", "services/service-00000/main.go"); err != nil {
		return ProfileMeasurement{}, err
	}
	if _, err := runGit(repository, "commit", "-q", "-m", "fixture: single file edit"); err != nil {
		return ProfileMeasurement{}, err
	}
	desired, err := runGit(repository, "rev-parse", "HEAD")
	if err != nil {
		return ProfileMeasurement{}, err
	}
	desired = strings.TrimSpace(desired)
	updateDir := filepath.Join(profileRoot, "update-index")
	update, err := buildZoekt(ctx, zoektBinary, repository, updateDir, []string{"main"})
	if err != nil {
		return ProfileMeasurement{}, err
	}
	active, err := publishedBranchVersion(updateDir, repositoryName, "main")
	if err != nil {
		return ProfileMeasurement{}, err
	}
	beforeBytes, _, err := shardStats(updateDir)
	if err != nil {
		return ProfileMeasurement{}, err
	}
	noOpStart := time.Now()
	current := active == desired
	noOpWall := time.Since(noOpStart)
	if !current {
		return ProfileMeasurement{}, errors.New("no-op state comparison did not recognize current generation")
	}
	afterBytes, _, err := shardStats(updateDir)
	if err != nil {
		return ProfileMeasurement{}, err
	}
	return ProfileMeasurement{
		Name: profile.Name, Services: serviceCount, Files: profile.Cardinality.Files,
		Memberships: profile.Cardinality.Memberships, Build: build, SingleFileEdit: update,
		Reader: reader, Queries: queries, Predicates: predicates,
		NoOp: NoOpMeasurement{
			WallUS: noOpWall.Microseconds(), StateComparisons: 1,
			ChildRuns: 0, FileScans: 0, ShardReads: 0, ShardBytesDelta: afterBytes - beforeBytes,
		},
	}, nil
}

func measureProfileQueries(
	ctx context.Context,
	searcher zoekt.Searcher,
	profile t323.LoadProfile,
) (QueryMeasurement, error) {
	last := len(profile.Services) - 1
	queries := []struct {
		q     query.Q
		limit int
	}{
		{mustAllCode("T323_LOAD_SERVICE_00000", "main"), 10},
		{mustService("T323_LOAD_SHARED_0000", "main", profile.Services[0].Placements), 10},
		{mustAllCode("T323_LOAD_SERVICE_", "main"), 20},
		{mustService("Needle", "main", profile.Services[last].Placements), 1},
	}
	run := func() ([]string, time.Duration, error) {
		start := time.Now()
		var results []string
		for index, testQuery := range queries {
			paths, err := searchOrderedQ(ctx, searcher, testQuery.q, testQuery.limit)
			if err != nil {
				return nil, 0, err
			}
			if len(paths) == 0 {
				return nil, 0, fmt.Errorf("profile query %d returned no result", index)
			}
			for _, path := range paths {
				results = append(results, fmt.Sprintf("%d/%s", index, path))
			}
		}
		return results, time.Since(start), nil
	}
	cold, coldWall, err := run()
	if err != nil {
		return QueryMeasurement{}, err
	}
	warm, warmWall, err := run()
	if err != nil {
		return QueryMeasurement{}, err
	}
	return QueryMeasurement{
		QueryCount: len(queries), ColdTotalUS: coldWall.Microseconds(), WarmTotalUS: warmWall.Microseconds(),
		ColdWarmEqual: slices.Equal(cold, warm), BroadTopK: 20, ServiceTopK: 1,
		ResultDigest: digestStrings(cold),
	}, nil
}

func measurePredicates(profile t323.LoadProfile) (PredicateMeasurement, error) {
	start := time.Now()
	measurement := PredicateMeasurement{CompiledServices: len(profile.Services)}
	for _, service := range profile.Services {
		compiled, err := compileServiceQuery("T323_LOAD", "main", service.Placements)
		if err != nil {
			return PredicateMeasurement{}, err
		}
		if !queryContainsPathPredicate(compiled) {
			return PredicateMeasurement{}, errors.New("compiled profile query omits path predicate")
		}
		encoded := len(compiled.String())
		measurement.Placements += len(service.Placements)
		measurement.CanonicalBytes += int64(encoded)
		measurement.MaxBytes = max(measurement.MaxBytes, encoded)
	}
	measurement.WallUS = time.Since(start).Microseconds()
	return measurement, nil
}

func buildZoekt(
	ctx context.Context,
	zoektBinary, repository, indexDir string,
	branches []string,
) (BuildMeasurement, error) {
	if err := os.MkdirAll(indexDir, 0o755); err != nil {
		return BuildMeasurement{}, err
	}
	arguments := []string{
		"-index", indexDir, "-incremental=false", "-disable_ctags",
		"-branches=" + strings.Join(branches, ","), repository,
	}
	command := exec.CommandContext(ctx, zoektBinary, arguments...)
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	start := time.Now()
	err := command.Run()
	wall := time.Since(start)
	if err != nil {
		if output.Len() > 8_192 {
			output.Next(output.Len() - 8_192)
		}
		return BuildMeasurement{}, fmt.Errorf("zoekt build: %w\n%s", err, output.String())
	}
	peakRSS := int64(1)
	if usage, ok := command.ProcessState.SysUsage().(*syscall.Rusage); ok {
		peakRSS = usage.Maxrss
		if runtime.GOOS == "linux" {
			peakRSS *= 1_024
		}
		peakRSS = max(peakRSS, int64(1))
	}
	shardBytes, shardCount, err := shardStats(indexDir)
	if err != nil {
		return BuildMeasurement{}, err
	}
	return BuildMeasurement{
		WallMS: wall.Milliseconds(), PeakRSSBytes: peakRSS,
		ShardBytes: shardBytes, ShardCount: shardCount,
	}, nil
}

func shardStats(indexDir string) (int64, int, error) {
	entries, err := os.ReadDir(indexDir)
	if err != nil {
		return 0, 0, err
	}
	var total int64
	count := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".zoekt") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return 0, 0, err
		}
		total += info.Size()
		count++
	}
	return total, count, nil
}

func publishedBranchVersion(indexDir, repository, branch string) (string, error) {
	entries, err := os.ReadDir(indexDir)
	if err != nil {
		return "", err
	}
	var version string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".zoekt") {
			continue
		}
		repositories, _, err := index.ReadMetadataPathAlive(filepath.Join(indexDir, entry.Name()))
		if err != nil {
			return "", err
		}
		for _, candidate := range repositories {
			if candidate.Name != repository {
				continue
			}
			for _, candidateBranch := range candidate.Branches {
				if candidateBranch.Name != branch {
					continue
				}
				if version != "" && version != candidateBranch.Version {
					return "", fmt.Errorf("published branch %q has mixed versions", branch)
				}
				version = candidateBranch.Version
			}
		}
	}
	if version == "" {
		return "", fmt.Errorf("published repository %q branch %q has no version", repository, branch)
	}
	return version, nil
}

func materializeRepository(repository string, contents map[string][]byte) error {
	if err := os.MkdirAll(repository, 0o755); err != nil {
		return err
	}
	if _, err := runGit(repository, "init", "-q", "-b", "main"); err != nil {
		return err
	}
	for key, value := range map[string]string{
		"user.name": "phebs-t324-fixture-author", "user.email": "fixture@example.invalid",
		"core.autocrlf": "false", "core.filemode": "false", "commit.gpgsign": "false",
		"zoekt.name": repositoryName,
	} {
		if _, err := runGit(repository, "config", key, value); err != nil {
			return err
		}
	}
	paths := make([]string, 0, len(contents))
	for path := range contents {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	for _, path := range paths {
		target := filepath.Join(repository, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, contents[path], 0o644); err != nil {
			return err
		}
	}
	if _, err := runGit(repository, "add", "--all"); err != nil {
		return err
	}
	_, err := runGit(repository, "commit", "-q", "-m", "fixture: frozen load profile")
	return err
}

func verifyProfileContents(profile t323.LoadProfile, contents map[string][]byte) error {
	if len(contents) != len(profile.Files) {
		return errors.New("materialized profile file count differs")
	}
	for _, file := range profile.Files {
		content, ok := contents[file.Path]
		if !ok || len(content) != file.Bytes || t323.SHA256(content) != file.SHA256 {
			return fmt.Errorf("materialized profile differs at %q", file.Path)
		}
	}
	return nil
}

func catalogPlacements(catalog t323.Catalog, serviceKey, principal string) []t323.Placement {
	for _, record := range catalog.Records {
		if record.ServiceKey != serviceKey || record.Disposition != "accepted" {
			continue
		}
		if record.Visibility == "reader" || principal == "admin" && record.Visibility == "restricted" {
			return slices.Clone(record.Placements)
		}
	}
	return nil
}

func serviceState(oracle t323.Oracle, serviceKey string) (t323.ServiceState, bool) {
	for _, state := range oracle.States {
		if state.ServiceKey == serviceKey {
			return state, true
		}
	}
	return t323.ServiceState{}, false
}

func pathsContaining(files map[string][]byte, needle []byte) []string {
	paths := make([]string, 0)
	for path, content := range files {
		if bytes.IndexByte(content, 0) < 0 && bytes.Contains(content, needle) {
			paths = append(paths, path)
		}
	}
	slices.Sort(paths)
	return paths
}

func searchPaths(ctx context.Context, searcher zoekt.Searcher, q query.Q, limit int) ([]string, error) {
	paths, err := searchOrderedQ(ctx, searcher, q, limit)
	if err != nil {
		return nil, err
	}
	slices.Sort(paths)
	return paths, nil
}

func searchOrdered(ctx context.Context, searcher zoekt.Searcher, expression, revision string, limit int) ([]string, error) {
	compiled, err := compileAllCodeQuery(expression, revision)
	if err != nil {
		return nil, err
	}
	return searchOrderedQ(ctx, searcher, compiled, limit)
}

func searchOrderedQ(ctx context.Context, searcher zoekt.Searcher, q query.Q, limit int) ([]string, error) {
	result, err := searcher.Search(ctx, q, &zoekt.SearchOptions{
		TotalMaxMatchCount: 100_000, MaxDocDisplayCount: limit,
	})
	if err != nil {
		return nil, err
	}
	return orderedPaths(result.Files), nil
}

func orderedPaths(files []zoekt.FileMatch) []string {
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.FileName)
	}
	return paths
}

func mustAllCode(expression, revision string) query.Q {
	compiled, err := compileAllCodeQuery(expression, revision)
	if err != nil {
		panic(err)
	}
	return compiled
}

func mustService(expression, revision string, placements []t323.Placement) query.Q {
	compiled, err := compileServiceQuery(expression, revision, placements)
	if err != nil {
		panic(err)
	}
	return compiled
}

func descriptorCount() (int, string) {
	if entries, err := os.ReadDir("/proc/self/fd"); err == nil {
		return len(entries), "proc-self-fd"
	}
	command := exec.Command("lsof", "-a", "-p", strconv.Itoa(os.Getpid()), "-Ff")
	output, err := command.Output()
	if err == nil {
		count := 0
		for _, line := range strings.Split(string(output), "\n") {
			if len(line) > 1 && line[0] == 'f' && line[1] >= '0' && line[1] <= '9' {
				count++
			}
		}
		return count, "lsof-numeric-fd"
	}
	return 0, "unavailable"
}

func runGit(directory string, arguments ...string) (string, error) {
	command := exec.Command("git", arguments...)
	if directory != "" {
		command.Dir = directory
	}
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %v: %w\n%s", arguments, err, output)
	}
	return string(output), nil
}
