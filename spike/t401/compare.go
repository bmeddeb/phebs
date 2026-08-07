package t401

import (
	"bufio"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/query"
	zoektsearch "github.com/sourcegraph/zoekt/search"
	"golang.org/x/mod/modfile"
)

type ComparisonRequest struct {
	ModuleRoot string
	Binary     string
	Repository string
	WorkDir    string
	Queries    []string
}

func CompareBlobReaders(ctx context.Context, request ComparisonRequest) (ComparisonReceipt, error) {
	if ctx == nil || !filepath.IsAbs(request.ModuleRoot) || !filepath.IsAbs(request.Binary) ||
		!filepath.IsAbs(request.Repository) || !filepath.IsAbs(request.WorkDir) || len(request.Queries) == 0 {
		return ComparisonReceipt{}, errors.New("T40.1 comparison request is incomplete")
	}
	tool, err := verifyComparisonBinary(request.Binary, request.ModuleRoot)
	if err != nil {
		return ComparisonReceipt{}, err
	}
	profileDigest, manifestDigest, fixtureTree, err := openComparisonFixture(ctx, request.Repository)
	if err != nil {
		return ComparisonReceipt{}, err
	}
	if !slices.Equal(request.Queries, FrozenBlobReaderTrial().Queries) {
		return ComparisonReceipt{}, errors.New("T40.1 comparison query set differs from the frozen fixture")
	}
	querySetDigest, err := QuerySetDigest(request.Queries)
	if err != nil {
		return ComparisonReceipt{}, err
	}
	if err := os.Mkdir(request.WorkDir, 0o755); err != nil {
		return ComparisonReceipt{}, err
	}
	modes := FrozenBlobReaderTrial().Modes
	measurements := make([]ModeMeasurement, 0, len(modes))
	for _, mode := range modes {
		measurement, err := measureReaderMode(
			ctx, request.Binary, request.Repository,
			filepath.Join(request.WorkDir, "index-"+mode.Name), mode, request.Queries,
		)
		if err != nil {
			return ComparisonReceipt{}, fmt.Errorf("T40.1 mode %s: %w", mode.Name, err)
		}
		measurements = append(measurements, measurement)
	}
	probes := make([]BoundaryProbe, 0, 6)
	for _, mode := range modes {
		probe, err := probeCancellation(ctx, request.Binary, request.Repository,
			filepath.Join(request.WorkDir, "cancel-"+mode.Name), mode)
		if err != nil {
			return ComparisonReceipt{}, err
		}
		probes = append(probes, probe)
	}
	for _, kind := range []string{"missing_object", "corrupt_object"} {
		repository := filepath.Join(request.WorkDir, "fault-"+kind+".git")
		if err := createFaultRepository(ctx, repository, kind); err != nil {
			return ComparisonReceipt{}, err
		}
		for _, mode := range modes {
			probe, err := probeObjectFailure(ctx, request.Binary, repository,
				filepath.Join(request.WorkDir, "fault-index-"+kind+"-"+mode.Name), mode, kind)
			if err != nil {
				return ComparisonReceipt{}, err
			}
			probes = append(probes, probe)
		}
	}
	receipt := ComparisonReceipt{
		Schema: ComparisonSchema, Binary: tool,
		FixtureProfileDigest: profileDigest, FixtureManifestDigest: manifestDigest,
		FixtureTree: fixtureTree, Queries: slices.Clone(request.Queries), QuerySetDigest: querySetDigest,
		Modes: measurements,
		IndexBytesEqual: measurements[0].IndexSHA256 == measurements[1].IndexSHA256 &&
			measurements[0].IndexBytes == measurements[1].IndexBytes,
		IndexedContentEqual: measurements[0].ContentSHA256 == measurements[1].ContentSHA256 &&
			measurements[0].FilesSeen == measurements[1].FilesSeen,
		QueryResultsEqual: measurements[0].QuerySHA256 == measurements[1].QuerySHA256,
		BoundaryProbes:    probes,
		Claims:            ComparisonClaims{SameBinary: true, SmallNeutralFixture: true},
	}
	if err := ValidateComparison(receipt); err != nil {
		return ComparisonReceipt{}, err
	}
	return receipt, nil
}

func openComparisonFixture(ctx context.Context, repository string) (string, string, string, error) {
	if filepath.Base(repository) != "repository.git" {
		return "", "", "", errors.New("T40.1 comparison repository is outside the authored fixture shape")
	}
	root := filepath.Dir(repository)
	profileBytes, err := os.ReadFile(filepath.Join(root, "profile.json"))
	if err != nil {
		return "", "", "", err
	}
	profile, err := DecodeStrict[Profile](profileBytes)
	if err != nil || ValidateProfile(profile) != nil || profile.Name != "structural-projection-v1" || profile.Scale != "projection" {
		return "", "", "", errors.New("T40.1 comparison profile is not the frozen small structural fixture")
	}
	wantProfile, err := ProjectionProfile("structural")
	if err != nil || !reflect.DeepEqual(profile, wantProfile) {
		return "", "", "", errors.New("T40.1 comparison profile differs from the frozen projection")
	}
	oracle, err := DeriveOracle(profile)
	if err != nil {
		return "", "", "", err
	}
	manifestBytes, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		return "", "", "", err
	}
	manifest, err := DecodeStrict[Manifest](manifestBytes)
	if err != nil || ValidateManifest(manifest, profile, oracle) != nil {
		return "", "", "", errors.New("T40.1 comparison manifest is invalid")
	}
	tree, err := runGit(ctx, repository, nil, "rev-parse", "refs/heads/main^{tree}")
	if err != nil {
		return "", "", "", err
	}
	tree = strings.TrimSpace(tree)
	if tree != manifest.Revisions[2].Tree {
		return "", "", "", errors.New("T40.1 comparison repository differs from its manifest")
	}
	profileDigest, err := ProfileDigest(profile)
	if err != nil {
		return "", "", "", err
	}
	return profileDigest, SHA256(manifestBytes), tree, nil
}

func measureReaderMode(
	ctx context.Context,
	binary, repository, indexDirectory string,
	mode ReaderMode,
	queries []string,
) (ModeMeasurement, error) {
	resources, reaped, err := runZoektBuild(ctx, binary, repository, indexDirectory, mode, nil)
	if err != nil {
		return ModeMeasurement{}, err
	}
	if !reaped {
		return ModeMeasurement{}, errors.New("zoekt child was not reaped")
	}
	indexDigest, indexBytes, shards, err := digestIndex(indexDirectory)
	if err != nil {
		return ModeMeasurement{}, err
	}
	contentDigest, queryDigest, filesSeen, err := digestSearch(ctx, indexDirectory, queries)
	if err != nil {
		return ModeMeasurement{}, err
	}
	return ModeMeasurement{
		Name: mode.Name, Environment: mode.Environment,
		IndexSHA256: indexDigest, IndexBytes: indexBytes, ShardCount: shards,
		ContentSHA256: contentDigest, QuerySHA256: queryDigest, FilesSeen: filesSeen,
		Resources: resources,
	}, nil
}

func runZoektBuild(
	ctx context.Context,
	binary, repository, indexDirectory string,
	mode ReaderMode,
	afterStart func(int),
) (ResourceObservation, bool, error) {
	if err := os.Mkdir(indexDirectory, 0o755); err != nil {
		return ResourceObservation{}, false, err
	}
	arguments := []string{
		"-index", indexDirectory, "-incremental=false", "-disable_ctags",
		"-branches=main", repository,
	}
	command := exec.CommandContext(ctx, binary, arguments...)
	if afterStart != nil {
		if err := isolateProcessGroup(command); err != nil {
			return ResourceObservation{}, false, err
		}
	}
	command.Env = deterministicGitEnvironment([]string{
		mode.EnvironmentName + "=" + mode.Environment,
	})
	var output boundedBuffer
	output.maximum = 8 << 10
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		if ctx.Err() != nil {
			return ResourceObservation{}, false, ctx.Err()
		}
		return ResourceObservation{}, false, err
	}
	stopSampling := make(chan struct{})
	resourceResult := make(chan ResourceObservation, 1)
	go sampleProcessTree(command.Process.Pid, stopSampling, resourceResult)
	if afterStart != nil {
		afterStart(command.Process.Pid)
	}
	err := command.Wait()
	close(stopSampling)
	resources := <-resourceResult
	if err != nil {
		if ctx.Err() != nil {
			return resources, true, ctx.Err()
		}
		return resources, true, fmt.Errorf("zoekt index build refused: %w", err)
	}
	return resources, true, nil
}

func probeCancellation(
	ctx context.Context,
	binary, repository, indexDirectory string,
	mode ReaderMode,
) (BoundaryProbe, error) {
	probeContext, cancel := context.WithCancel(ctx)
	defer cancel()
	boundary := "zoekt_child_started"
	var gitPIDs []int
	var observationErr error
	_, reaped, err := runZoektBuild(probeContext, binary, repository, indexDirectory, mode, func(pid int) {
		if mode.Name == "cat_file_batch_candidate" {
			gitPIDs, observationErr = stopAtGitDescendant(pid, 10*time.Second)
			if len(gitPIDs) != 0 {
				boundary = "git_descendant_observed"
			}
		}
		cancel()
	})
	if len(gitPIDs) != 0 {
		// The exact Git descendants are deliberately held at a deterministic
		// observation barrier until the CommandContext-owned parent has exited.
		// Resuming them then proves the broken parent pipes let each child exit;
		// the probe never kills a Git child and cannot mistake a fast successful
		// build for cancellation.
		if resumeErr := resumeProcesses(gitPIDs); resumeErr != nil && observationErr == nil {
			observationErr = resumeErr
		}
	}
	if observationErr != nil {
		return BoundaryProbe{}, observationErr
	}
	if !errors.Is(err, context.Canceled) || !reaped {
		return BoundaryProbe{}, fmt.Errorf("T40.1 mode %s cancellation = %v reaped=%t", mode.Name, err, reaped)
	}
	if mode.Name == "cat_file_batch_candidate" && boundary != "git_descendant_observed" {
		return BoundaryProbe{}, errors.New("T40.1 candidate cancellation did not reach a Git descendant")
	}
	gitState := "not_observed"
	if len(gitPIDs) != 0 {
		if !waitForProcessExit(gitPIDs, 5*time.Second) {
			return BoundaryProbe{}, errors.New("T40.1 candidate cancellation left a Git descendant alive")
		}
		gitState = "observed_reaped"
	}
	return BoundaryProbe{
		Mode: mode.Name, Kind: "cancellation", Boundary: boundary,
		Outcome: "refused", Class: "cancellation", ParentReaped: true,
		GitDescendantState: gitState,
	}, nil
}

func probeObjectFailure(
	ctx context.Context,
	binary, repository, indexDirectory string,
	mode ReaderMode,
	kind string,
) (BoundaryProbe, error) {
	_, reaped, err := runZoektBuild(ctx, binary, repository, indexDirectory, mode, nil)
	if !reaped {
		return BoundaryProbe{}, fmt.Errorf("T40.1 mode %s did not reap %s probe", mode.Name, kind)
	}
	outcome := "refused"
	if err == nil {
		outcome = "completed_with_gap"
	}
	return BoundaryProbe{
		Mode: mode.Name, Kind: kind, Boundary: "object_read",
		Outcome: outcome, Class: kind, ParentReaped: true, GitDescendantState: "not_measured",
	}, nil
}

func stopAtGitDescendant(root int, maximum time.Duration) ([]int, error) {
	deadline := time.Now().Add(maximum)
	if err := stopProcessGroup(root); err != nil {
		return nil, err
	}
	stopped := true
	defer func() {
		if stopped {
			_ = continueProcessGroup(root)
		}
	}()
	for time.Now().Before(deadline) {
		command := exec.Command("ps", "-axo", "pid=,ppid=,state=,command=")
		command.Env = deterministicBaseEnvironment()
		output, err := command.Output()
		if err == nil {
			pids := processTreeGitDescendants(root, string(output))
			if len(pids) != 0 {
				stopped = false // The caller owns resuming the exact children.
				return pids, nil
			}
		}
		if err := continueProcessGroup(root); err != nil {
			return nil, err
		}
		stopped = false
		time.Sleep(time.Millisecond)
		if err := stopProcessGroup(root); err != nil {
			return nil, err
		}
		stopped = true
	}
	return nil, errors.New("T40.1 candidate cancellation did not observe git cat-file --batch --buffer")
}

func processTreeGitDescendants(root int, listing string) []int {
	type row struct {
		parent  int
		state   string
		command string
	}
	rows := make(map[int]row)
	children := make(map[int][]int)
	for _, line := range strings.Split(listing, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		parent, parentErr := strconv.Atoi(fields[1])
		if pidErr != nil || parentErr != nil {
			continue
		}
		rows[pid] = row{
			parent: parent, state: fields[2], command: strings.Join(fields[3:], " "),
		}
		children[parent] = append(children[parent], pid)
	}
	pids := []int{root}
	var gitPIDs []int
	for index := 0; index < len(pids); index++ {
		for _, child := range children[pids[index]] {
			command := rows[child].command
			if !strings.HasPrefix(rows[child].state, "Z") &&
				strings.Contains(command, "git cat-file --batch --buffer") {
				gitPIDs = append(gitPIDs, child)
			}
			pids = append(pids, child)
		}
	}
	return gitPIDs
}

func waitForProcessExit(pids []int, maximum time.Duration) bool {
	wanted := make(map[int]struct{}, len(pids))
	for _, pid := range pids {
		wanted[pid] = struct{}{}
	}
	deadline := time.Now().Add(maximum)
	for time.Now().Before(deadline) {
		command := exec.Command("ps", "-axo", "pid=")
		command.Env = deterministicBaseEnvironment()
		output, err := command.Output()
		if err == nil {
			remaining := 0
			for _, field := range strings.Fields(string(output)) {
				pid, parseErr := strconv.Atoi(field)
				if parseErr == nil {
					if _, exists := wanted[pid]; exists {
						remaining++
					}
				}
			}
			if remaining == 0 {
				return true
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

func digestIndex(directory string) (string, uint64, uint64, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return "", 0, 0, err
	}
	names := make([]string, 0)
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".zoekt") {
			names = append(names, entry.Name())
		}
	}
	slices.Sort(names)
	if len(names) == 0 {
		return "", 0, 0, errors.New("zoekt build published no shards")
	}
	hash := newDigestWriter()
	var total uint64
	for _, name := range names {
		file, err := os.Open(filepath.Join(directory, name))
		if err != nil {
			return "", 0, 0, err
		}
		if err := hash.writeString(name); err != nil {
			_ = file.Close()
			return "", 0, 0, err
		}
		written, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil {
			return "", 0, 0, copyErr
		}
		if closeErr != nil {
			return "", 0, 0, closeErr
		}
		total += uint64(written)
	}
	return hash.digest(), total, uint64(len(names)), nil
}

type contentProjection struct {
	Path     string   `json:"path"`
	Version  string   `json:"version"`
	Branches []string `json:"branches"`
	SHA256   string   `json:"sha256"`
}

type queryProjection struct {
	Query string              `json:"query"`
	Files []contentProjection `json:"files"`
}

func digestSearch(ctx context.Context, directory string, expressions []string) (string, string, uint64, error) {
	searcher, err := zoektsearch.NewDirectorySearcher(directory)
	if err != nil {
		return "", "", 0, err
	}
	defer searcher.Close()
	inventory, err := searcher.Search(ctx, &query.Const{Value: true}, &zoekt.SearchOptions{
		Whole: true, TotalMaxMatchCount: 100_000, MaxDocDisplayCount: 10_000,
	})
	if err != nil {
		return "", "", 0, err
	}
	contents := projectFiles(inventory.Files)
	contentBytes, err := json.Marshal(contents)
	if err != nil {
		return "", "", 0, err
	}
	projections := make([]queryProjection, 0, len(expressions))
	for _, expression := range expressions {
		compiled, err := query.Parse(expression)
		if err != nil {
			return "", "", 0, err
		}
		result, err := searcher.Search(ctx, compiled, &zoekt.SearchOptions{
			Whole: true, TotalMaxMatchCount: 100_000, MaxDocDisplayCount: 10_000,
		})
		if err != nil {
			return "", "", 0, err
		}
		projections = append(projections, queryProjection{Query: expression, Files: projectFiles(result.Files)})
	}
	queryBytes, err := json.Marshal(projections)
	if err != nil {
		return "", "", 0, err
	}
	return SHA256(contentBytes), SHA256(queryBytes), uint64(len(contents)), nil
}

func projectFiles(files []zoekt.FileMatch) []contentProjection {
	projection := make([]contentProjection, 0, len(files))
	for _, file := range files {
		branches := slices.Clone(file.Branches)
		slices.Sort(branches)
		projection = append(projection, contentProjection{
			Path: file.FileName, Version: file.Version, Branches: branches, SHA256: SHA256(file.Content),
		})
	}
	slices.SortFunc(projection, func(left, right contentProjection) int {
		return strings.Compare(left.Path, right.Path)
	})
	return projection
}

func createFaultRepository(ctx context.Context, repository, kind string) error {
	if kind != "missing_object" && kind != "corrupt_object" {
		return errors.New("unknown T40.1 object fault")
	}
	if _, err := runGit(ctx, "", nil, "init", "--bare", "--quiet", "--initial-branch=main", repository); err != nil {
		return err
	}
	if _, err := runGit(ctx, repository, nil, "config", "zoekt.name", RepositoryName+"-fault"); err != nil {
		return err
	}
	oid := strings.Repeat("1", 40)
	if kind == "corrupt_object" {
		oid = strings.Repeat("2", 40)
	}
	tree, err := runGitInput(ctx, repository, nil,
		[]byte("100644 blob "+oid+"\tbroken.go\n"), "mktree", "--missing")
	if err != nil {
		return err
	}
	identity := frozenGenerator()
	environment := []string{
		"GIT_AUTHOR_NAME=" + identity.AuthorName, "GIT_AUTHOR_EMAIL=" + identity.AuthorEmail,
		"GIT_COMMITTER_NAME=" + identity.AuthorName, "GIT_COMMITTER_EMAIL=" + identity.AuthorEmail,
		fmt.Sprintf("GIT_AUTHOR_DATE=%d +0000", identity.Timestamp),
		fmt.Sprintf("GIT_COMMITTER_DATE=%d +0000", identity.Timestamp),
	}
	commit, err := runGitInput(ctx, repository, environment, []byte("fixture: "+kind+"\n"),
		"commit-tree", strings.TrimSpace(tree))
	if err != nil {
		return err
	}
	if _, err := runGit(ctx, repository, nil, "update-ref", "refs/heads/main", strings.TrimSpace(commit)); err != nil {
		return err
	}
	if kind == "corrupt_object" {
		object := filepath.Join(repository, "objects", oid[:2], oid[2:])
		if err := os.MkdirAll(filepath.Dir(object), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(object, []byte("not-a-zlib-git-object"), 0o444); err != nil {
			return err
		}
	}
	return nil
}

func runGitInput(
	ctx context.Context,
	directory string,
	extraEnvironment []string,
	input []byte,
	arguments ...string,
) (string, error) {
	command := exec.CommandContext(ctx, "git", arguments...)
	command.Dir = directory
	command.Env = deterministicGitEnvironment(extraEnvironment)
	command.Stdin = strings.NewReader(string(input))
	var stdout boundedBuffer
	stdout.maximum = 1 << 20
	var stderr boundedBuffer
	stderr.maximum = 8 << 10
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("git command failed: %w", err)
	}
	return stdout.String(), nil
}

func verifyComparisonBinary(binary, moduleRoot string) (ToolIdentity, error) {
	info, err := os.Stat(binary)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		return ToolIdentity{}, errors.New("T40.1 zoekt binary is not an executable regular file")
	}
	candidate, err := buildinfo.ReadFile(binary)
	if err != nil {
		return ToolIdentity{}, errors.New("read T40.1 zoekt build metadata")
	}
	if candidate.Path != "github.com/sourcegraph/zoekt/cmd/zoekt-git-index" {
		return ToolIdentity{}, errors.New("T40.1 binary package is not zoekt-git-index")
	}
	goMod, err := os.ReadFile(filepath.Join(moduleRoot, "go.mod"))
	if err != nil {
		return ToolIdentity{}, err
	}
	parsed, err := modfile.Parse("go.mod", goMod, nil)
	if err != nil {
		return ToolIdentity{}, err
	}
	expectedVersion := ""
	for _, requirement := range parsed.Require {
		if requirement.Mod.Path == "github.com/sourcegraph/zoekt" {
			expectedVersion = requirement.Mod.Version
			break
		}
	}
	goSum, err := os.ReadFile(filepath.Join(moduleRoot, "go.sum"))
	if err != nil {
		return ToolIdentity{}, err
	}
	expectedSum := ""
	for _, line := range strings.Split(string(goSum), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[0] == "github.com/sourcegraph/zoekt" && fields[1] == expectedVersion {
			expectedSum = fields[2]
			break
		}
	}
	if expectedVersion == "" || expectedSum == "" || candidate.Main.Path != "github.com/sourcegraph/zoekt" ||
		candidate.Main.Version != expectedVersion || candidate.Main.Sum != expectedSum {
		return ToolIdentity{}, errors.New("T40.1 zoekt binary differs from the current module version or checksum")
	}
	content, err := os.ReadFile(binary)
	if err != nil {
		return ToolIdentity{}, err
	}
	return ToolIdentity{
		Name: "zoekt-git-index", Version: candidate.Main.Path + "@" + candidate.Main.Version + " " + candidate.Main.Sum,
		SHA256: SHA256(content),
	}, nil
}

func sampleProcessTree(root int, stop <-chan struct{}, result chan<- ResourceObservation) {
	observation := ResourceObservation{}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		rss, descriptors, descriptorAvailable, err := processTreeSample(root)
		if err == nil {
			observation.RSSState = "sampled"
			observation.RSSMethod = "sampled-10ms-ps-process-tree-rss-kib"
			observation.SampledMaxRSSBytes = max(observation.SampledMaxRSSBytes, rss)
			observation.IncludesDescendants = true
			if descriptorAvailable {
				observation.DescriptorState = "sampled"
				observation.DescriptorMethod = "sampled-10ms-proc-or-lsof-process-tree"
				observation.SampledMaxDescriptors = max(observation.SampledMaxDescriptors, descriptors)
			}
		}
		select {
		case <-stop:
			if observation.RSSState == "" {
				observation.RSSState = "unavailable"
			}
			if observation.DescriptorState == "" {
				observation.DescriptorState = "unavailable"
			}
			result <- observation
			return
		case <-ticker.C:
		}
	}
}

func processTreeSample(root int) (uint64, uint64, bool, error) {
	command := exec.Command("ps", "-axo", "pid=,ppid=,rss=")
	command.Env = deterministicBaseEnvironment()
	output, err := command.Output()
	if err != nil {
		return 0, 0, false, err
	}
	type process struct {
		parent int
		rss    uint64
	}
	processes := map[int]process{}
	children := map[int][]int{}
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 3 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		parent, parentErr := strconv.Atoi(fields[1])
		rss, rssErr := strconv.ParseUint(fields[2], 10, 64)
		if pidErr != nil || parentErr != nil || rssErr != nil {
			continue
		}
		processes[pid] = process{parent: parent, rss: rss}
		children[parent] = append(children[parent], pid)
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, false, err
	}
	if _, ok := processes[root]; !ok {
		return 0, 0, false, errors.New("root process is absent")
	}
	pids := []int{root}
	for index := 0; index < len(pids); index++ {
		pids = append(pids, children[pids[index]]...)
	}
	var rssKiB uint64
	for _, pid := range pids {
		rssKiB += processes[pid].rss
	}
	descriptors, available := processTreeDescriptors(pids)
	return rssKiB * 1_024, descriptors, available, nil
}

func processTreeDescriptors(pids []int) (uint64, bool) {
	var total uint64
	procAvailable := true
	for _, pid := range pids {
		entries, err := os.ReadDir(filepath.Join("/proc", strconv.Itoa(pid), "fd"))
		if err != nil {
			procAvailable = false
			break
		}
		total += uint64(len(entries))
	}
	if procAvailable {
		return total, true
	}
	values := make([]string, len(pids))
	for index, pid := range pids {
		values[index] = strconv.Itoa(pid)
	}
	command := exec.Command("lsof", "-a", "-p", strings.Join(values, ","), "-Ff")
	command.Env = deterministicBaseEnvironment()
	output, err := command.Output()
	if err != nil {
		return 0, false
	}
	total = 0
	for _, line := range strings.Split(string(output), "\n") {
		if len(line) > 1 && line[0] == 'f' && line[1] >= '0' && line[1] <= '9' {
			total++
		}
	}
	return total, true
}

type digestWriter struct {
	hash hash.Hash
}

func newDigestWriter() *digestWriter {
	return &digestWriter{hash: sha256.New()}
}

func (writer *digestWriter) Write(content []byte) (int, error) {
	return writer.hash.Write(content)
}

func (writer *digestWriter) writeString(value string) error {
	_, err := io.WriteString(writer, value+"\x00")
	return err
}

func (writer *digestWriter) digest() string {
	return "sha256:" + fmt.Sprintf("%x", writer.hash.Sum(nil))
}
