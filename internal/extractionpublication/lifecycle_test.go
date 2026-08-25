package extractionpublication

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/candidate"
)

func TestRecoverStagesRetiresCurrentCompatibleAndSparseWithoutDeleting(t *testing.T) {
	root := filepath.Join(t.TempDir(), "extraction-publications")
	repository := strings.Repeat("a", 64)
	directory := filepath.Join(root, repository)
	generation := filepath.Join(directory, stageGenerationPrefix+"1")
	compatibleGenerationSuffix := strings.Repeat("1", 64)
	compatibleGeneration := filepath.Join(
		directory, stageGenerationPrefix+compatibleGenerationSuffix,
	)
	restore := filepath.Join(directory, stageRestorePrefix+"2")
	compatibleRestoreSuffix := strings.Repeat("2", 64)
	compatibleRestore := filepath.Join(directory, stageRestorePrefix+compatibleRestoreSuffix)
	writeStageFixture(t, generation, stageGeneration, 1)
	writeStageFixture(t, compatibleGeneration, stageGeneration, 1)
	writeStageFixture(t, restore, stageRestore, 3)
	writeStageFixture(t, compatibleRestore, stageRestore, 3)

	sparseRepository := strings.Repeat("b", 64)
	sparseDirectory := filepath.Join(root, "candidates", sparseRepository)
	sparse := filepath.Join(sparseDirectory, stageSparsePrefix+"3")
	writeSparseStageFixture(t, sparse)

	authoritative := filepath.Join(directory, strings.Repeat("c", 64))
	if err := os.MkdirAll(authoritative, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(authoritative, "sentinel")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	modified := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	for _, path := range []string{
		generation, compatibleGeneration, restore, compatibleRestore, sparse,
	} {
		setStageTime(t, path, modified)
	}

	report, err := RecoverStages(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if report.Repositories != 2 || report.Retired != 5 || report.Work <= 0 ||
		report.Work > MaxStageRecoveryWork || report.PeakDescriptors > MaxStageLifecycleDescriptors {
		t.Fatalf("stage recovery = %+v", report)
	}
	for _, test := range []struct {
		raw, retired string
	}{
		{generation, filepath.Join(directory, retiredStageGenerationPrefix+"1")},
		{compatibleGeneration, filepath.Join(
			directory, retiredStageGenerationPrefix+compatibleGenerationSuffix,
		)},
		{restore, filepath.Join(directory, retiredStageRestorePrefix+"2")},
		{compatibleRestore, filepath.Join(
			directory, retiredStageRestorePrefix+compatibleRestoreSuffix,
		)},
		{sparse, filepath.Join(sparseDirectory, retiredStageSparsePrefix+"3")},
	} {
		if _, err := os.Lstat(test.raw); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("raw stage remains at %q: %v", test.raw, err)
		}
		info, err := os.Lstat(test.retired)
		if err != nil {
			t.Fatalf("retired stage %q: %v", test.retired, err)
		}
		if !info.ModTime().Equal(modified) {
			t.Fatalf("retirement changed stage mtime: got %v want %v", info.ModTime(), modified)
		}
	}
	if raw, err := os.ReadFile(sentinel); err != nil || string(raw) != "keep" {
		t.Fatalf("authoritative sentinel changed: %q, %v", raw, err)
	}
	second, err := RecoverStages(t.Context(), root)
	if err != nil || second.Retired != 0 {
		t.Fatalf("idempotent recovery = %+v, %v", second, err)
	}
}

func TestStageNameGrammarCoversOnlyCurrentAndLegacyWriters(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{stageGenerationPrefix + strings.Repeat("a", 64), true},
		{stageRestorePrefix + "4294967295", true},
		{stageSparsePrefix + "1", true},
		{retiredStageGenerationPrefix + "7", true},
		{collectingStageSparsePrefix + strings.Repeat("f", 64), true},
		{stageRestorePrefix + "01", false},
		{stageGenerationPrefix + "4294967296", false},
		{stageSparsePrefix + "arbitrary", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, got := parseStageName(test.name)
			if got != test.want {
				t.Fatalf("parseStageName(%q) = %v, want %v", test.name, got, test.want)
			}
		})
	}
}

func TestStageLifecycleHonorsGraceCountAndCollectingResume(t *testing.T) {
	root := filepath.Join(t.TempDir(), "extraction-publications")
	repository := strings.Repeat("d", 64)
	directory := filepath.Join(root, repository)
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	for index := 1; index <= 3; index++ {
		path := filepath.Join(directory, stageGenerationPrefix+strconv.Itoa(index))
		writeStageFixture(t, path, stageGeneration, 1)
		setStageTime(t, path, now.Add(-time.Duration(index)*time.Hour))
	}
	if _, err := RecoverStages(t.Context(), root); err != nil {
		t.Fatal(err)
	}

	first, err := SweepStageLifecycle(t.Context(), root, now, "", testStageLimits())
	if err != nil {
		t.Fatal(err)
	}
	if first.Deleted == 0 || first.Deleted > MaxStageLifecycleDeletes || first.Active {
		t.Fatalf("count-excess turn = %+v", first)
	}
	if _, err := os.Lstat(filepath.Join(directory, retiredStageGenerationPrefix+"3")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("oldest count-excess stage remains: %v", err)
	}
	for _, suffix := range []string{"1", "2"} {
		if _, err := os.Lstat(filepath.Join(directory, retiredStageGenerationPrefix+suffix)); err != nil {
			t.Fatalf("grace-retained stage %s changed: %v", suffix, err)
		}
	}
	runStageLifecyclePass(t, root, now, first.Cursor)
	runStageLifecyclePass(t, root, now.Add(25*time.Hour), "")
	for _, suffix := range []string{"1", "2"} {
		if _, err := os.Lstat(filepath.Join(directory, retiredStageGenerationPrefix+suffix)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("aged stage %s remains: %v", suffix, err)
		}
	}

	long := filepath.Join(directory, retiredStageRestorePrefix+"9")
	writeStageFixture(t, long, stageRestore, 20)
	setStageTime(t, long, now.Add(-25*time.Hour))
	limits := testStageLimits()
	limits.Deletes = 2
	result, err := SweepStageLifecycle(t.Context(), root, now, "", limits)
	if err != nil || result.Deleted != 2 || !result.More {
		t.Fatalf("first collecting turn = %+v, %v", result, err)
	}
	collecting := filepath.Join(directory, collectingStageRestorePrefix+"9")
	if _, err := os.Lstat(collecting); err != nil {
		t.Fatalf("eligible stage was not collecting: %v", err)
	}
	setStageTime(t, collecting, now)
	for turn := 0; turn < 32; turn++ {
		result, err = SweepStageLifecycle(t.Context(), root, now, result.Cursor, limits)
		if err != nil {
			t.Fatal(err)
		}
		if !result.More {
			break
		}
	}
	if _, err := os.Lstat(collecting); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("collecting stage stalled after mtime refresh: %v", err)
	}
}

func TestStageLifecycleOwnsSparseResidueAndPreservesAuthority(t *testing.T) {
	root := filepath.Join(t.TempDir(), "extraction-publications")
	repository := strings.Repeat("e", 64)
	directory := filepath.Join(root, "candidates", repository)
	stage := filepath.Join(directory, stageSparsePrefix+"4")
	writeSparseStageFixture(t, stage)
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	setStageTime(t, stage, now.Add(-25*time.Hour))
	authoritative := filepath.Join(directory, strings.Repeat("f", 64))
	if err := os.Mkdir(authoritative, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(authoritative, "sentinel")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RecoverStages(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	runStageLifecyclePass(t, root, now, "")
	for _, path := range []string{
		stage,
		filepath.Join(directory, retiredStageSparsePrefix+"4"),
		filepath.Join(directory, collectingStageSparsePrefix+"4"),
	} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("sparse stage state remains at %q: %v", path, err)
		}
	}
	if raw, err := os.ReadFile(sentinel); err != nil || string(raw) != "keep" {
		t.Fatalf("sparse authority changed: %q, %v", raw, err)
	}
}

func TestStageRecoveryRefusesUnknownAndSymlinkState(t *testing.T) {
	tests := []struct {
		name  string
		build func(*testing.T, string) string
	}{
		{
			name: "unknown member",
			build: func(t *testing.T, root string) string {
				stage := filepath.Join(root, strings.Repeat("1", 64), stageGenerationPrefix+"5")
				if err := os.MkdirAll(stage, 0o700); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(stage, "unknown")
				if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
				return path
			},
		},
		{
			name: "stage symlink",
			build: func(t *testing.T, root string) string {
				directory := filepath.Join(root, strings.Repeat("2", 64))
				if err := os.MkdirAll(directory, 0o700); err != nil {
					t.Fatal(err)
				}
				outside := filepath.Join(t.TempDir(), "sentinel")
				if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Dir(outside), filepath.Join(directory, stageRestorePrefix+"6")); err != nil {
					t.Fatal(err)
				}
				return outside
			},
		},
		{
			name: "nested symlink",
			build: func(t *testing.T, root string) string {
				stage := filepath.Join(root, strings.Repeat("3", 64), stageRestorePrefix+"7")
				writeStageFixture(t, stage, stageRestore, 0)
				outside := filepath.Join(t.TempDir(), "sentinel")
				if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(stage, strings.Repeat("f", 64), "result-00000.json")); err != nil {
					t.Fatal(err)
				}
				return outside
			},
		},
		{
			name: "sparse namespace symlink",
			build: func(t *testing.T, root string) string {
				outside := t.TempDir()
				sentinel := filepath.Join(outside, "sentinel")
				if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(root, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(root, "candidates")); err != nil {
					t.Fatal(err)
				}
				return sentinel
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "extraction-publications")
			sentinel := test.build(t, root)
			if _, err := RecoverStages(t.Context(), root); !errors.Is(err, ErrInvalid) {
				t.Fatalf("recovery error = %v", err)
			}
			if raw, err := os.ReadFile(sentinel); err != nil || string(raw) != "keep" {
				t.Fatalf("refused state changed: %q, %v", raw, err)
			}
		})
	}
}

func TestStageRootAuthoritySurvivesPathSwapWithoutEscaping(t *testing.T) {
	root := t.TempDir()
	repositoryPath := filepath.Join(root, "repository")
	stageName := collectingStageGenerationPrefix + "8"
	writeStageFixture(t, filepath.Join(repositoryPath, stageName), stageGeneration, 0)
	budget, err := newLifecycleStageBudget(testStageLimits())
	if err != nil {
		t.Fatal(err)
	}
	authority, err := openStableStageRoot(repositoryPath, budget)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = authority.close() }()
	moved := filepath.Join(root, "moved")
	if err := os.Rename(repositoryPath, moved); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	outsideStage := filepath.Join(outside, stageName)
	writeStageFixture(t, outsideStage, stageGeneration, 0)
	sentinel := filepath.Join(outsideStage, "sentinel")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, repositoryPath); err != nil {
		t.Fatal(err)
	}
	complete, err := drainStage(
		t.Context(), authority,
		stageCandidate{name: stageName, kind: stageGeneration, state: stageCollecting}, budget,
	)
	if err != nil || !complete {
		t.Fatalf("anchored drain = %v, %v", complete, err)
	}
	if _, err := os.Lstat(filepath.Join(moved, stageName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("anchored stage remains: %v", err)
	}
	if raw, err := os.ReadFile(sentinel); err != nil || string(raw) != "keep" {
		t.Fatalf("path-swap target changed: %q, %v", raw, err)
	}
}

func TestStageRepositoryOpenRefusesNamespaceSwapAfterInventory(t *testing.T) {
	for _, sparse := range []bool{false, true} {
		name := "publication"
		if sparse {
			name = "sparse"
		}
		t.Run(name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "extraction-publications")
			repository := strings.Repeat("3", 64)
			scope := root
			stagePrefix := collectingStageGenerationPrefix
			kind := stageGeneration
			if sparse {
				scope = filepath.Join(root, "candidates")
				stagePrefix = collectingStageSparsePrefix
				kind = stageSparse
			}
			originalRepository := filepath.Join(scope, repository)
			originalStage := filepath.Join(originalRepository, stagePrefix+"13")
			if sparse {
				writeSparseStageFixture(t, originalStage)
			} else {
				writeStageFixture(t, originalStage, kind, 0)
			}

			budget, err := newLifecycleStageBudget(testStageLimits())
			if err != nil {
				t.Fatal(err)
			}
			repositories, err := stageRepositories(root, sparse, budget)
			if err != nil || len(repositories) != 1 {
				t.Fatalf("inventory = %d, %v", len(repositories), err)
			}

			swapped := root
			if sparse {
				swapped = scope
			}
			moved := swapped + "-moved"
			if err := os.Rename(swapped, moved); err != nil {
				t.Fatal(err)
			}
			replacementRepository := filepath.Join(swapped, repository)
			replacementStage := filepath.Join(replacementRepository, stagePrefix+"13")
			if sparse {
				writeSparseStageFixture(t, replacementStage)
			} else {
				writeStageFixture(t, replacementStage, kind, 0)
			}
			originalSentinel := filepath.Join(moved, repository, "original-sentinel")
			replacementSentinel := filepath.Join(replacementRepository, "replacement-sentinel")
			for _, path := range []string{originalSentinel, replacementSentinel} {
				if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			authority, err := openStageRepository(root, repositories[0], budget)
			if authority != nil {
				_ = authority.close()
			}
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("namespace swap open error = %v", err)
			}
			for _, path := range []string{originalSentinel, replacementSentinel} {
				raw, readErr := os.ReadFile(path)
				if readErr != nil || string(raw) != "keep" {
					t.Fatalf("namespace swap changed %q: %q, %v", path, raw, readErr)
				}
			}
		})
	}
}

func TestStageLifecycleEnforcesEveryControllerLimit(t *testing.T) {
	t.Run("max restore drain", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "extraction-publications")
		directory := filepath.Join(root, strings.Repeat("4", 64))
		stage := filepath.Join(directory, collectingStageRestorePrefix+"10")
		writeStageFixture(t, stage, stageRestore, candidate.MaxDomainResultPartitions)
		result, err := SweepStageLifecycle(
			t.Context(), root, time.Now().UTC(), "", testStageLimits(),
		)
		if err != nil {
			t.Fatal(err)
		}
		if result.Scanned > MaxStageLifecycleCandidates || result.Deleted != MaxStageLifecycleDeletes ||
			result.Stats > MaxStageLifecycleStats || result.MetadataBytes > MaxStageLifecycleMetadataBytes ||
			result.PeakDescriptors > MaxStageLifecycleDescriptors {
			t.Fatalf("bounded max restore turn = %+v", result)
		}
	})

	t.Run("max domains with full candidate inventory", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "extraction-publications")
		directory := filepath.Join(root, strings.Repeat("9", 64))
		stage := filepath.Join(directory, collectingStageGenerationPrefix+"100")
		writeGenerationStageEmptyDomains(t, stage, MaxDomains)
		for index := 0; index < MaxStageLifecycleCandidates-1; index++ {
			if err := os.Mkdir(
				filepath.Join(directory, retiredStageGenerationPrefix+strconv.Itoa(101+index)), 0o700,
			); err != nil {
				t.Fatal(err)
			}
		}
		result, err := SweepStageLifecycle(
			t.Context(), root, time.Now().UTC(), "", testStageLimits(),
		)
		if err != nil || !result.More || result.Deleted == 0 || result.Deleted > MaxStageLifecycleDeletes ||
			result.Scanned != MaxStageLifecycleCandidates || result.Stats > MaxStageLifecycleStats ||
			result.MetadataBytes > MaxStageLifecycleMetadataBytes ||
			result.PeakDescriptors > MaxStageLifecycleDescriptors {
			t.Fatalf("max-domain progress = %+v, %v", result, err)
		}
	})

	t.Run("candidate cap", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "extraction-publications")
		directory := filepath.Join(root, strings.Repeat("5", 64))
		for index := 1; index <= MaxStageLifecycleCandidates+1; index++ {
			if err := os.MkdirAll(filepath.Join(directory, stageGenerationPrefix+strconv.Itoa(index)), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		result, err := SweepStageLifecycle(
			t.Context(), root, time.Now().UTC(), "", testStageLimits(),
		)
		if !errors.Is(err, ErrLimit) || result.Scanned != MaxStageLifecycleCandidates || result.Deleted != 0 {
			t.Fatalf("candidate cap = %+v, %v", result, err)
		}
	})

	for _, test := range []struct {
		name   string
		adjust func(*StageLifecycleLimits)
		check  func(StageLifecycleResult) bool
	}{
		{"stats", func(limits *StageLifecycleLimits) { limits.Stats = 8 }, func(result StageLifecycleResult) bool {
			return result.Stats <= 8
		}},
		{"metadata", func(limits *StageLifecycleLimits) { limits.MetadataBytes = maxStageNameBytes - 1 }, func(result StageLifecycleResult) bool {
			return result.MetadataBytes <= maxStageNameBytes-1
		}},
		{"descriptors", func(limits *StageLifecycleLimits) { limits.Descriptors = 3 }, func(result StageLifecycleResult) bool {
			return result.PeakDescriptors <= 3
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "extraction-publications")
			stage := filepath.Join(root, strings.Repeat("6", 64), collectingStageRestorePrefix+"11")
			writeStageFixture(t, stage, stageRestore, 2)
			limits := testStageLimits()
			test.adjust(&limits)
			result, err := SweepStageLifecycle(t.Context(), root, time.Now().UTC(), "", limits)
			if !errors.Is(err, ErrLimit) || result.Deleted != 0 || !test.check(result) {
				t.Fatalf("%s cap = %+v, %v", test.name, result, err)
			}
		})
	}
}

func TestStageLifecycleCursorCompletesCleanMultiRepositoryPass(t *testing.T) {
	root := filepath.Join(t.TempDir(), "extraction-publications")
	for _, repository := range []string{strings.Repeat("7", 64), strings.Repeat("8", 64)} {
		if err := os.MkdirAll(filepath.Join(root, repository), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	first, err := SweepStageLifecycle(t.Context(), root, now, "", testStageLimits())
	if err != nil || !first.More || first.Cursor == "" {
		t.Fatalf("first repository turn = %+v, %v", first, err)
	}
	second, err := SweepStageLifecycle(t.Context(), root, now, first.Cursor, testStageLimits())
	if err != nil || !second.More || second.Cursor != "a0:s/" {
		t.Fatalf("publication boundary turn = %+v, %v", second, err)
	}
	third, err := SweepStageLifecycle(t.Context(), root, now, second.Cursor, testStageLimits())
	if err != nil || third.More || third.Cursor != "" {
		t.Fatalf("completed pass = %+v, %v", third, err)
	}

	live := filepath.Join(root, strings.Repeat("7", 64), stageGenerationPrefix+"12")
	writeStageFixture(t, live, stageGeneration, 0)
	last := runStageLifecyclePass(t, root, now, "")
	if !last.Active || last.More || last.Deleted != 0 {
		t.Fatalf("live raw final status = %+v", last)
	}
	if _, err := os.Lstat(live); err != nil {
		t.Fatalf("lifecycle mutated live raw stage: %v", err)
	}
}

func TestStageRecoveryWorkCapDoesNotOvershoot(t *testing.T) {
	budget := newRecoveryStageBudget()
	budget.work = MaxStageRecoveryWork - 1
	if err := budget.takeWork(); err != nil {
		t.Fatal(err)
	}
	if err := budget.takeWork(); !errors.Is(err, ErrLimit) {
		t.Fatalf("one-over recovery work = %v", err)
	}
	if budget.work != MaxStageRecoveryWork {
		t.Fatalf("recovery work overshot: %d", budget.work)
	}
}

func TestRawStageRetirementStopsBeforeMutationWhenCanceled(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repository")
	rawName := stageGenerationPrefix + "14"
	rawPath := filepath.Join(root, rawName)
	writeStageFixture(t, rawPath, stageGeneration, 0)
	info, err := os.Lstat(rawPath)
	if err != nil {
		t.Fatal(err)
	}
	stages := []stageCandidate{{
		name: rawName, kind: stageGeneration, state: stageRaw, expected: info,
	}}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	for _, test := range []struct {
		name string
		run  func(*stageRootAuthority, *stageBudget) error
	}{
		{
			name: "preflight",
			run: func(authority *stageRootAuthority, budget *stageBudget) error {
				return preflightRawRetirement(ctx, authority, stages, budget)
			},
		},
		{
			name: "rename",
			run: func(authority *stageRootAuthority, budget *stageBudget) error {
				_, _, err := retireRawStages(ctx, authority, stages, budget)
				return err
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			budget := newRecoveryStageBudget()
			authority, err := openStableStageRoot(root, budget)
			if err != nil {
				t.Fatal(err)
			}
			runErr := test.run(authority, budget)
			closeErr := authority.close()
			if !errors.Is(runErr, context.Canceled) || closeErr != nil {
				t.Fatalf("canceled retirement = %v, close = %v", runErr, closeErr)
			}
			if _, err := os.Lstat(rawPath); err != nil {
				t.Fatalf("canceled retirement changed raw stage: %v", err)
			}
		})
	}
}

func runStageLifecyclePass(
	t *testing.T, root string, now time.Time, cursor string,
) StageLifecycleResult {
	t.Helper()
	var result StageLifecycleResult
	for turn := 0; turn < 64; turn++ {
		var err error
		result, err = SweepStageLifecycle(t.Context(), root, now, cursor, testStageLimits())
		if err != nil {
			t.Fatal(err)
		}
		if !result.More {
			return result
		}
		cursor = result.Cursor
	}
	t.Fatal("stage lifecycle pass did not converge")
	return result
}

func testStageLimits() StageLifecycleLimits {
	return StageLifecycleLimits{
		Candidates: MaxStageLifecycleCandidates, Deletes: MaxStageLifecycleDeletes,
		Stats: MaxStageLifecycleStats, Descriptors: MaxStageLifecycleDescriptors,
		MetadataBytes: MaxStageLifecycleMetadataBytes,
	}
}

func writeStageFixture(t *testing.T, directory string, kind stageKind, results int) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, generationName()), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "plan-000.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	domain := filepath.Join(directory, strings.Repeat("f", 64))
	if err := os.Mkdir(domain, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(domain, completionName()), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if kind != stageRestore {
		return
	}
	if err := os.WriteFile(filepath.Join(domain, rootName()), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	for ordinal := range results {
		if err := os.WriteFile(
			filepath.Join(domain, fmt.Sprintf("result-%05d.json", ordinal)), []byte("{}"), 0o600,
		); err != nil {
			t.Fatal(err)
		}
	}
}

func writeSparseStageFixture(t *testing.T, directory string) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		candidate.SparseRootFileName,
		"candidate-partition-domain-000.json",
		"candidate-partition-typed-scope-000.bin",
	} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func writeGenerationStageEmptyDomains(t *testing.T, directory string, domains int) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{generationName(), "plan-000.json"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for ordinal := 0; ordinal < domains; ordinal++ {
		domain := filepath.Join(directory, fmt.Sprintf("%064x", ordinal+1))
		if err := os.Mkdir(domain, 0o700); err != nil {
			t.Fatal(err)
		}
	}
}

func setStageTime(t *testing.T, path string, modified time.Time) {
	t.Helper()
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatal(err)
	}
}
