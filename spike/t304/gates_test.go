package t304

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/internal/extract/extractors/grpcgo"
	"github.com/bmeddeb/phebs/internal/extract/extractors/kafkago"
	"github.com/bmeddeb/phebs/internal/extract/extractors/protodecl"
	"github.com/bmeddeb/phebs/internal/extract/extractors/scipfield"
	"github.com/bmeddeb/phebs/internal/extract/extractors/thriftdecl"
	"github.com/bmeddeb/phebs/internal/extract/extractors/thriftfield"
	"github.com/bmeddeb/phebs/internal/extract/extractors/thriftgo"
	"github.com/bmeddeb/phebs/spike/t301"
)

const neutralRepository = "example.com/neutral/monorepo"

func TestT304CandidatePlannerChild(t *testing.T) {
	if os.Getenv("T304_CHILD") != "1" {
		return
	}
	repoDir := requiredEnv(t, "T304_CHILD_REPO")
	outputDir := requiredEnv(t, "T304_CHILD_STAGE")
	resultPath := requiredEnv(t, "T304_CHILD_RESULT")
	commit := requiredEnv(t, "T304_CHILD_COMMIT")
	corpusDigest := requiredEnv(t, "T304_CHILD_CORPUS_DIGEST")

	unit := measurementUnit(t, neutralRepository)
	policies := measurementPolicies(t)
	started := time.Now()
	manifest, err := candidate.Build(t.Context(), candidate.Request{
		RepoDir: repoDir, OutputDir: outputDir,
		Repository: neutralRepository, Commit: commit,
		Unit: unit, Policies: policies,
	})
	if err != nil {
		t.Fatalf("build candidate plan: %v", err)
	}
	wall := time.Since(started)
	snapshot, err := snapshotStage(outputDir)
	if err != nil {
		t.Fatal(err)
	}
	scratchUpperBound, err := validationScratchUpperBound(
		outputDir, manifest,
	)
	if err != nil {
		t.Fatal(err)
	}
	plannerScratchUpperBound, err := plannerScratchUpperBound(manifest)
	if err != nil {
		t.Fatal(err)
	}
	run := plannerRun(
		corpusDigest, manifest, snapshot, plannerScratchUpperBound,
		scratchUpperBound, wall,
	)
	encoded, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(resultPath, append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestT304CandidatePlannerMeasurement(t *testing.T) {
	if testing.Short() {
		t.Skip("generates and plans the 200,008-regular-file neutral corpus")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	repoDir := filepath.Join(t.TempDir(), "neutral.git")
	corpus, err := t301.GenerateRepository(
		ctx, repoDir, t301.DefaultIrrelevantFiles,
	)
	if err != nil {
		t.Fatalf("generate T30.1 neutral corpus: %v", err)
	}
	if corpus.Repository != neutralRepository ||
		corpus.RegularFiles != ExpectedRegularFiles {
		t.Fatalf("unexpected neutral corpus: %+v", corpus)
	}

	unit := measurementUnit(t, corpus.Repository)
	policies := measurementPolicies(t)
	identities, err := candidate.PolicyIdentities(policies)
	if err != nil {
		t.Fatal(err)
	}

	stage1 := filepath.Join(t.TempDir(), "stage-1")
	run1 := runPlannerChild(t, ctx, repoDir, stage1, corpus)
	stage2 := filepath.Join(t.TempDir(), "stage-2")
	run2 := runPlannerChild(t, ctx, repoDir, stage2, corpus)

	equal, err := stagesEqual(stage1, stage2)
	if err != nil {
		t.Fatal(err)
	}
	if !equal {
		t.Fatal("repeat candidate builds were not byte-identical")
	}
	if stablePlannerRun(run1) != stablePlannerRun(run2) {
		t.Fatalf("repeat candidate identities differ:\nrun1=%+v\nrun2=%+v", run1, run2)
	}
	for index, run := range []PlannerRun{run1, run2} {
		assertGates(t, fmt.Sprintf("run%d", index+1), run)
		if run.Census.RegularCount != corpus.RegularFiles {
			t.Fatalf(
				"run%d regular census = %d, want %d",
				index+1, run.Census.RegularCount, corpus.RegularFiles,
			)
		}
		if run.RepositoryCandidateRows <= 0 ||
			run.CallerCandidateRows <= 0 ||
			len(run.CallerLeaves) == 0 {
			t.Fatalf("run%d did not exercise both candidate planes: %+v", index+1, run)
		}
	}

	manifest, err := readManifest(stage1, corpus.Repository)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.UnitDigest != unit.Digest ||
		!equalPolicyIdentities(manifest.Policies, identities) {
		t.Fatalf("planner identities do not match real registry: %+v", manifest)
	}
	results := Results{
		Schema: SchemaVersion, Decision: "GO",
		Reason:    "The production streamed planner stayed within the prospective local gates on the deterministic 200,008-file corpus, and a second build reproduced every staged byte.",
		GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Corpus: corpus, UnitDigest: unit.Digest, Policies: identities,
		PartitionPolicy: manifest.PartitionPolicy,
		FrozenGates: FrozenGates{
			PlannerWallNanos:   PlannerWallLimit.Nanoseconds(),
			PeakRSSBytes:       PeakRSSLimit,
			CandidateDiskBytes: CandidateDiskBytesLimit,
		},
		Run1: run1, Run2: run2, CanonicalBytesEqual: equal,
	}

	if resultPath := os.Getenv("T304_RESULTS_PATH"); resultPath != "" {
		cleaned, err := cleanResultPath(resultPath)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.MarshalIndent(results, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cleaned, append(encoded, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}

	retained := loadCommittedResults(t)
	if retained.Corpus.Digest != results.Corpus.Digest ||
		retained.Corpus.Head != results.Corpus.Head ||
		retained.UnitDigest != results.UnitDigest ||
		!equalPolicyIdentities(retained.Policies, results.Policies) ||
		retained.PartitionPolicy != results.PartitionPolicy ||
		stablePlannerRun(retained.Run1) != stablePlannerRun(results.Run1) {
		t.Fatalf(
			"retained T30.4 identity drifted:\nmeasured=%+v\nretained=%+v",
			results, retained,
		)
	}
}

func equalPolicyIdentities(
	left, right []candidate.PolicyIdentity,
) bool {
	return slices.EqualFunc(
		left,
		right,
		func(left, right candidate.PolicyIdentity) bool {
			return left.Domain == right.Domain &&
				left.Version == right.Version &&
				left.EnumerationPolicy == right.EnumerationPolicy &&
				left.SymlinkPolicy == right.SymlinkPolicy &&
				left.Plane == right.Plane &&
				left.MaxRecords == right.MaxRecords &&
				left.ExecutionPartitionPolicy == right.ExecutionPartitionPolicy &&
				left.ExecutionMaxRecords == right.ExecutionMaxRecords &&
				slices.Equal(left.TypedInputs, right.TypedInputs)
		},
	)
}

func TestT304CommittedResults(t *testing.T) {
	results := loadCommittedResults(t)
	if results.Schema != SchemaVersion ||
		results.Decision != "GO" ||
		results.Corpus.RegularFiles != ExpectedRegularFiles ||
		results.Run1.CorpusDigest != results.Corpus.Digest ||
		results.Run2.CorpusDigest != results.Corpus.Digest ||
		results.Run1.UnitDigest != results.UnitDigest ||
		results.Run2.UnitDigest != results.UnitDigest ||
		results.Run1.Census.RegularCount != ExpectedRegularFiles ||
		results.Run2.Census.RegularCount != ExpectedRegularFiles ||
		results.Run1.RepositoryCandidateRows <= 0 ||
		results.Run1.CallerCandidateRows <= 0 ||
		len(results.Run1.CallerLeaves) == 0 ||
		len(results.Policies) != 10 ||
		!results.CanonicalBytesEqual ||
		stablePlannerRun(results.Run1) != stablePlannerRun(results.Run2) {
		t.Fatalf("invalid retained T30.4 result: %+v", results)
	}
	if results.FrozenGates != (FrozenGates{
		PlannerWallNanos:   PlannerWallLimit.Nanoseconds(),
		PeakRSSBytes:       PeakRSSLimit,
		CandidateDiskBytes: CandidateDiskBytesLimit,
	}) {
		t.Fatalf("retained frozen gates drifted: %+v", results.FrozenGates)
	}
	assertGates(t, "retained run1", results.Run1)
	assertGates(t, "retained run2", results.Run2)
	if results.PartitionPolicy.EnumerationPolicy != candidate.EnumerationPolicyVersion ||
		results.PartitionPolicy.CallerHashPolicy != candidate.CallerHashPolicy ||
		results.PartitionPolicy.InitialPrefixBits != candidate.InitialCallerPrefixBits ||
		results.PartitionPolicy.MaxRecords != candidate.MaxRecordsPerArtifact ||
		results.PartitionPolicy.MaxDeclaredBytes != candidate.MaxDeclaredBytesPerArtifact {
		t.Fatalf("retained partition bounds drifted: %+v", results.PartitionPolicy)
	}
	for _, policy := range results.Policies {
		if policy.SymlinkPolicy != "fixed-root-regular-v1" {
			t.Fatalf("retained symlink policy drifted: %+v", policy)
		}
	}
	policyDigest, err := candidate.PolicyDigest(results.Policies)
	if err != nil || policyDigest != results.Run1.PolicyDigest ||
		policyDigest != results.Run2.PolicyDigest {
		t.Fatalf(
			"retained policy digest = %q, %v; runs=%q/%q",
			policyDigest, err,
			results.Run1.PolicyDigest, results.Run2.PolicyDigest,
		)
	}
}

func measurementUnit(t *testing.T, repository string) *analysisunit.State {
	t.Helper()
	state, err := (analysisunit.Scope{
		Repository: repository,
		Name:       "payments",
		Primary:    []string{"services/payments/src"},
		Supporting: []string{
			"contracts/payment.proto",
			"services/payments/generated",
			"services/payments/go.mod",
		},
	}).State()
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func measurementPolicies(t *testing.T) []candidate.Policy {
	t.Helper()
	// This reconstructs the production registry as frozen by T30.4, with every
	// independent dark gate enabled. T40.R1 later added optional focused-local
	// physical and whole-repository execution bounds to the shared registry;
	// zero both here so this historical measurement continues proving its
	// original candidate identity and bytes.
	extractors := []extract.Extractor{
		protodecl.New(), grpcgo.New(), scipfield.New(), gocaller.NewGRPC(),
		thriftdecl.New(), thriftgo.New(), gocaller.NewThrift(),
		thriftfield.New(), kafkago.NewProducer(), kafkago.NewConsumer(),
	}
	policies, err := extract.CandidatePolicies(extractors)
	if err != nil {
		t.Fatal(err)
	}
	for index := range policies {
		if policies[index].Domain == "proto-contract" || policies[index].Domain == "thrift-contract" {
			policies[index].MaxRecords = 0
			policies[index].ExecutionPartitionPolicy = ""
			policies[index].ExecutionMaxRecords = 0
		}
	}
	return policies
}

type stageSnapshot struct {
	files  map[string][]byte
	bytes  int64
	digest string
}

func snapshotStage(directory string) (stageSnapshot, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return stageSnapshot{}, err
	}
	names := make([]string, 0, len(entries))
	files := make(map[string][]byte, len(entries))
	var total int64
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return stageSnapshot{}, fmt.Errorf("unexpected staged entry %q", entry.Name())
		}
		info, err := entry.Info()
		if err != nil {
			return stageSnapshot{}, err
		}
		if !info.Mode().IsRegular() {
			return stageSnapshot{}, fmt.Errorf("staged entry %q is not regular", entry.Name())
		}
		raw, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			return stageSnapshot{}, err
		}
		names = append(names, entry.Name())
		files[entry.Name()] = raw
		total += int64(len(raw))
	}
	sort.Strings(names)
	hash := sha256.New()
	_, _ = hash.Write([]byte("t304-canonical-stage-v1\x00"))
	var size [8]byte
	for _, name := range names {
		raw := files[name]
		binary.BigEndian.PutUint64(size[:], uint64(len(name)))
		_, _ = hash.Write(size[:])
		_, _ = hash.Write([]byte(name))
		binary.BigEndian.PutUint64(size[:], uint64(len(raw)))
		_, _ = hash.Write(size[:])
		_, _ = hash.Write(raw)
	}
	return stageSnapshot{
		files: files, bytes: total,
		digest: "sha256:" + hex.EncodeToString(hash.Sum(nil)),
	}, nil
}

func plannerRun(
	corpusDigest string,
	manifest candidate.Manifest,
	snapshot stageSnapshot,
	plannerScratchUpperBound int64,
	validationScratchUpperBound int64,
	wall time.Duration,
) PlannerRun {
	var repositoryRows, callerRows int
	for _, member := range manifest.RepositoryMembers {
		repositoryRows += member.RecordCount
	}
	leaves := make([]CallerLeafMeasurement, 0, len(manifest.CallerLeaves))
	for _, leaf := range manifest.CallerLeaves {
		callerRows += leaf.RecordCount
		leaves = append(leaves, CallerLeafMeasurement{
			Prefix: leaf.Prefix, PrefixBits: leaf.PrefixBits,
			RecordCount: leaf.RecordCount, DeclaredBytes: leaf.DeclaredBytes,
			ContentBytes: leaf.ContentBytes,
		})
	}
	return PlannerRun{
		CorpusDigest: corpusDigest, UnitDigest: manifest.UnitDigest,
		PolicyDigest:     manifest.PolicyDigest,
		GenerationDigest: manifest.GenerationDigest,
		ManifestDigest:   manifest.Digest, Census: manifest.Corpus,
		RepositoryCandidateRows: repositoryRows,
		CallerCandidateRows:     callerRows, CallerLeaves: leaves,
		StagedArtifactCount:              len(snapshot.files),
		StagedArtifactBytes:              snapshot.bytes,
		PlannerScratchUpperBoundBytes:    plannerScratchUpperBound,
		ValidationScratchUpperBoundBytes: validationScratchUpperBound,
		PeakCandidateDiskBytes: snapshot.bytes + max(
			plannerScratchUpperBound,
			validationScratchUpperBound,
		),
		CanonicalOutputDigest: snapshot.digest,
		PlannerWallNanos:      wall.Nanoseconds(),
	}
}

func plannerScratchUpperBound(manifest candidate.Manifest) (int64, error) {
	var callerBytes int64
	for _, leaf := range manifest.CallerLeaves {
		if leaf.ContentBytes > math.MaxInt64-callerBytes {
			return 0, errors.New("planner scratch upper bound overflows")
		}
		callerBytes += leaf.ContentBytes
	}
	if callerBytes > math.MaxInt64/2 {
		return 0, errors.New("planner scratch upper bound overflows")
	}
	// During a split, the input spool and both output spools can coexist.
	// Their canonical record encodings have the same total bytes as the final
	// caller artifacts. Other roots/leaves cannot raise aggregate scratch
	// above twice the final caller content.
	return callerBytes * 2, nil
}

func validationScratchUpperBound(
	directory string,
	manifest candidate.Manifest,
) (int64, error) {
	type projection struct {
		Path          string          `json:"path"`
		OID           string          `json:"oid"`
		DeclaredBytes int64           `json:"declared_bytes"`
		InUnit        bool            `json:"in_unit"`
		Shared        bool            `json:"shared"`
		Plane         candidate.Plane `json:"plane"`
	}
	var projectionBytes int64
	measure := func(
		name string,
		plane candidate.Plane,
	) (resultErr error) {
		file, err := os.Open(filepath.Join(directory, name))
		if err != nil {
			return err
		}
		defer func() {
			resultErr = errors.Join(resultErr, file.Close())
		}()
		decoder := json.NewDecoder(file)
		for {
			var record candidate.Record
			if err := decoder.Decode(&record); errors.Is(err, io.EOF) {
				return nil
			} else if err != nil {
				return err
			}
			raw, err := json.Marshal(projection{
				Path: record.Path, OID: record.OID,
				DeclaredBytes: record.DeclaredBytes,
				InUnit:        record.InUnit, Shared: record.Shared,
				Plane: plane,
			})
			if err != nil {
				return err
			}
			if int64(len(raw)+1) > math.MaxInt64-projectionBytes {
				return errors.New("validation projection byte count overflows")
			}
			projectionBytes += int64(len(raw) + 1)
		}
	}
	for _, member := range manifest.RepositoryMembers {
		if err := measure(member.Name, candidate.PlaneRepository); err != nil {
			return 0, err
		}
	}
	for _, leaf := range manifest.CallerLeaves {
		if err := measure(leaf.Name, candidate.PlaneCaller); err != nil {
			return 0, err
		}
	}
	if projectionBytes > math.MaxInt64/2 {
		return 0, errors.New("validation scratch upper bound overflows")
	}
	// The binary external merge retains at most all existing runs plus one
	// output run for the subset currently being merged.
	return projectionBytes * 2, nil
}

func runPlannerChild(
	t *testing.T,
	ctx context.Context,
	repoDir, stageDir string,
	corpus t301.Corpus,
) PlannerRun {
	t.Helper()
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	resultPath := filepath.Join(t.TempDir(), "child-result.json")
	cmd := exec.CommandContext(
		ctx, executable, "-test.run=^TestT304CandidatePlannerChild$",
	)
	cmd.Env = append(os.Environ(),
		"T304_CHILD=1",
		"T304_CHILD_REPO="+repoDir,
		"T304_CHILD_STAGE="+stageDir,
		"T304_CHILD_RESULT="+resultPath,
		"T304_CHILD_COMMIT="+corpus.Head,
		"T304_CHILD_CORPUS_DIGEST="+corpus.Digest,
	)
	output, runErr := cmd.CombinedOutput()
	if runErr != nil {
		t.Fatalf("candidate planner child: %v\n%s", runErr, output)
	}
	raw, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatal(err)
	}
	var run PlannerRun
	if err := decodeJSONStrict(raw, &run); err != nil {
		t.Fatal(err)
	}
	run.PeakRSSBytes = peakRSS(cmd.ProcessState)
	return run
}

func stagesEqual(left, right string) (bool, error) {
	leftSnapshot, err := snapshotStage(left)
	if err != nil {
		return false, err
	}
	rightSnapshot, err := snapshotStage(right)
	if err != nil {
		return false, err
	}
	if leftSnapshot.digest != rightSnapshot.digest ||
		len(leftSnapshot.files) != len(rightSnapshot.files) {
		return false, nil
	}
	for name, leftRaw := range leftSnapshot.files {
		rightRaw, ok := rightSnapshot.files[name]
		if !ok || !bytes.Equal(leftRaw, rightRaw) {
			return false, nil
		}
	}
	return true, nil
}

func readManifest(directory, repository string) (candidate.Manifest, error) {
	raw, err := os.ReadFile(filepath.Join(directory, candidate.ManifestName(repository)))
	if err != nil {
		return candidate.Manifest{}, err
	}
	var manifest candidate.Manifest
	if err := decodeJSONStrict(raw, &manifest); err != nil {
		return candidate.Manifest{}, err
	}
	return manifest, nil
}

func stablePlannerRun(run PlannerRun) string {
	run.PlannerWallNanos = 0
	run.PeakRSSBytes = 0
	raw, err := json.Marshal(run)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func assertGates(t *testing.T, label string, run PlannerRun) {
	t.Helper()
	if run.PlannerWallNanos <= 0 ||
		run.PeakRSSBytes <= 0 ||
		run.StagedArtifactBytes <= 0 ||
		run.PlannerScratchUpperBoundBytes <= 0 ||
		run.ValidationScratchUpperBoundBytes <= 0 ||
		run.PeakCandidateDiskBytes <
			run.StagedArtifactBytes+run.PlannerScratchUpperBoundBytes ||
		run.PeakCandidateDiskBytes <
			run.StagedArtifactBytes+run.ValidationScratchUpperBoundBytes ||
		time.Duration(run.PlannerWallNanos) > PlannerWallLimit ||
		run.PeakRSSBytes > PeakRSSLimit ||
		run.PeakCandidateDiskBytes > CandidateDiskBytesLimit {
		t.Fatalf(
			"%s exceeds frozen gates: wall=%s peak_rss=%d staged=%d planner_scratch_upper=%d validation_scratch_upper=%d peak_disk=%d",
			label, time.Duration(run.PlannerWallNanos),
			run.PeakRSSBytes, run.StagedArtifactBytes,
			run.PlannerScratchUpperBoundBytes,
			run.ValidationScratchUpperBoundBytes,
			run.PeakCandidateDiskBytes,
		)
	}
}

func requiredEnv(t *testing.T, name string) string {
	t.Helper()
	value := os.Getenv(name)
	if value == "" {
		t.Fatalf("%s is required", name)
	}
	return value
}

func decodeJSONStrict(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}

func cleanResultPath(value string) (string, error) {
	cleaned := filepath.Clean(value)
	if value == "" || !filepath.IsAbs(cleaned) {
		return "", errors.New("result path must be absolute")
	}
	return cleaned, nil
}

func loadCommittedResults(t *testing.T) Results {
	t.Helper()
	raw, err := os.ReadFile("results.json")
	if err != nil {
		t.Fatal(err)
	}
	var results Results
	if err := decodeJSONStrict(raw, &results); err != nil {
		t.Fatal(err)
	}
	return results
}

func peakRSS(state *os.ProcessState) int64 {
	if state == nil {
		return 0
	}
	usage, ok := state.SysUsage().(*syscall.Rusage)
	if !ok {
		return 0
	}
	value := int64(usage.Maxrss)
	if runtime.GOOS != "darwin" {
		value *= 1024
	}
	return value
}

func TestPlannerScratchUpperBound(t *testing.T) {
	t.Parallel()
	bound, err := plannerScratchUpperBound(candidate.Manifest{
		CallerLeaves: []candidate.CallerLeaf{
			{Artifact: candidate.Artifact{ContentBytes: 3}},
			{Artifact: candidate.Artifact{ContentBytes: 4}},
		},
	})
	if err != nil || bound != 14 {
		t.Fatalf("planner scratch bound = %d, %v", bound, err)
	}
	if _, err := plannerScratchUpperBound(candidate.Manifest{
		CallerLeaves: []candidate.CallerLeaf{
			{Artifact: candidate.Artifact{ContentBytes: math.MaxInt64}},
		},
	}); err == nil {
		t.Fatal("planner scratch overflow succeeded")
	}
}

func TestStableRunProjectionIgnoresOnlyMeasurements(t *testing.T) {
	left := PlannerRun{
		CorpusDigest:     "sha256:" + strings.Repeat("a", 64),
		PlannerWallNanos: 1, PeakRSSBytes: 2,
	}
	right := left
	right.PlannerWallNanos = 3
	right.PeakRSSBytes = 4
	if stablePlannerRun(left) != stablePlannerRun(right) {
		t.Fatal("dynamic resource observations entered the stable projection")
	}
	right.StagedArtifactBytes = 1
	if stablePlannerRun(left) == stablePlannerRun(right) {
		t.Fatal("stable staged output was omitted from the stable projection")
	}
	right = left
	right.PlannerScratchUpperBoundBytes = 1
	if stablePlannerRun(left) == stablePlannerRun(right) {
		t.Fatal("planner scratch bound was omitted from the stable projection")
	}
	right = left
	right.ValidationScratchUpperBoundBytes = 1
	if stablePlannerRun(left) == stablePlannerRun(right) {
		t.Fatal("validation scratch bound was omitted from the stable projection")
	}
}
