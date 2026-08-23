package t401

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/sourcepartition"
)

func TestDirectoryBytesClosesSystemToolOnlyWhenRequested(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "source"), []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(bin, "du"), []byte("#!/bin/sh\nprintf '12345\\t%s\\n' \"$2\"\n"), 0o700,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	_, legacyAllocated, err := directoryBytes(t.Context(), root, false)
	if err != nil || legacyAllocated != 12_641_280 {
		t.Fatalf("historical allocated bytes = %d, %v", legacyAllocated, err)
	}
	_, closedAllocated, err := directoryBytes(t.Context(), root, true)
	if err != nil || closedAllocated == legacyAllocated {
		t.Fatalf("closed allocated bytes = %d, %v", closedAllocated, err)
	}
}

func TestFrozenEnvelopeIsExactDeterministicAndSourceFree(t *testing.T) {
	first, err := BuildEnvelope()
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildEnvelope()
	if err != nil {
		t.Fatal(err)
	}
	firstBytes, err := MarshalCanonical(first)
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := MarshalCanonical(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("frozen envelope is not deterministic")
	}
	retained, err := os.ReadFile("envelope.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(retained, firstBytes) {
		t.Fatal("retained envelope differs from the canonical frozen envelope")
	}
	decoded, err := DecodeStrict[Envelope](retained)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateEnvelope(decoded); err != nil {
		t.Fatal(err)
	}
	structural, semantic := first.Profiles[0], first.Profiles[1]
	if structural.Aggregate.PhysicalOwners != 2_000_002 || structural.Aggregate.RegularFiles != 2_000_002 ||
		structural.Aggregate.EligibleGoFiles != 2_000_000 || structural.Aggregate.RepositoryLanePlacements != 2_000_000 ||
		structural.Aggregate.CallerLanePlacements != 1_000_000 || structural.Aggregate.UniqueGoBlobs != 512 ||
		structural.Aggregate.DeclaredRepositoryGoBytes != 9_216_000_000 {
		t.Fatalf("structural aggregate = %+v", structural.Aggregate)
	}
	if semantic.Aggregate.UniqueGoBlobs != 262_144 || semantic.Aggregate.IDLCandidates != 32_768 ||
		semantic.Aggregate.PhysicalOwners != 294_914 {
		t.Fatalf("semantic aggregate = %+v", semantic.Aggregate)
	}
	if structural.Adapters == nil || len(structural.Adapters) != 0 {
		t.Fatalf("structural adapter expectations = %#v", structural.Adapters)
	}
	if got := structural.ExpectedOutcomes[0]; got.Outcome != "pass" || got.Observed != 3_907 ||
		got.Limit != sourcepartition.MaxPlacementsPerPartition {
		t.Fatalf("structural planning expectation = %+v", got)
	}
	if got := semantic.ExpectedOutcomes[1]; got.Outcome != "refused" ||
		got.Dimension != "generation_records" || got.Observed != 262_144 ||
		got.Limit != observationpublication.MaxGenerationRecords {
		t.Fatalf("semantic publication expectation = %+v", got)
	}
	if first.HistoricalDiagnostic.Outcome != "unknown" {
		t.Fatalf("historical diagnostic = %+v", first.HistoricalDiagnostic)
	}
	report, err := os.ReadFile(filepath.Join("..", "large-monorepo-20260806", "REPORT.md"))
	if err != nil {
		t.Fatal(err)
	}
	if SHA256(report) != first.HistoricalDiagnostic.SHA256 {
		t.Fatal("historical diagnostic digest drifted")
	}
	if !first.Claims.GiantAuthoringExplicit || first.Claims.GeneratesGiantInTests ||
		first.Claims.QualifiesSupportedScale || first.Claims.AuthorizesRelease {
		t.Fatalf("envelope claims = %+v", first.Claims)
	}
}

func TestStructuralPartitionProfileIsExactAndParentBound(t *testing.T) {
	profile, err := StructuralPartitionProfile()
	if err != nil {
		t.Fatal(err)
	}
	if profile.Name != "structural-partition-throughput-v1" ||
		profile.Scale != "projection" || profile.ParentProfileDigest == "" ||
		profile.Shape.GoFileBytes != 4_608 || profile.Shape.GoBlobReuseClasses != 512 ||
		profile.Aggregate.EligibleGoFiles != 4_096 {
		t.Fatalf("throughput profile = %+v", profile)
	}
}

func TestRetainedExplicitArtifactsAreStrictSourceFreeAndValid(t *testing.T) {
	profiles, err := FrozenProfiles()
	if err != nil {
		t.Fatal(err)
	}
	byKind := make(map[string]Profile, len(profiles))
	for _, profile := range profiles {
		byKind[profile.Kind] = profile
	}
	digests := map[string]string{
		"envelope.json":            "sha256:92cce848e6e42942c24e2fa066968571fb5693252b7b41b7a91c889881fe7f94",
		"comparison.json":          "sha256:3527bec297c80c71b6c5081b1b386d25efc9ec8894643f599c7c57848be3b402",
		"reproducibility.json":     "sha256:b7b0491af659007eb8e903279ca63c6f8178878a8af114a9af0cd407e52ccb1a",
		"structural/manifest.json": "sha256:4ae92b8efa58d459fe8fa10ba23c5cedad3adc7b2dddbd7618ea8d96c306604b",
		"structural/receipt.json":  "sha256:bd80bef34f61f35c2f701d0877d4c013ec3c7d0ce62ec3756b32b7a4f103b2c2",
		"semantic/manifest.json":   "sha256:ca4925f3ca3ddad42955e5c3dc0e9b5610e7fa8ac4ce3e614a9ad091e23362a8",
		"semantic/receipt.json":    "sha256:e096b17faccd3ace38f0272234bc7fdfff97b0dfb1ccd23fa388e888d966d6d3",
	}
	for name, digest := range digests {
		content, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if SHA256(content) != digest {
			t.Fatalf("retained %s digest = %s, want %s", name, SHA256(content), digest)
		}
		for _, private := range []string{"/Users/", "/private/", "Desktop/", "phebs-t401-explicit"} {
			if bytes.Contains(content, []byte(private)) {
				t.Fatalf("retained %s contains private marker %q", name, private)
			}
		}
	}
	comparisonBytes, err := os.ReadFile("comparison.json")
	if err != nil {
		t.Fatal(err)
	}
	comparison, err := DecodeStrict[ComparisonReceipt](comparisonBytes)
	if err != nil || ValidateComparison(comparison) != nil {
		t.Fatalf("retained comparison validation = %v", err)
	}
	if comparison.IndexBytesEqual || !comparison.IndexedContentEqual ||
		!comparison.QueryResultsEqual || comparison.Claims.SelectsProductionReader ||
		comparison.BoundaryProbes[4].Outcome != "refused" ||
		comparison.BoundaryProbes[5].Outcome != "completed_with_gap" {
		t.Fatalf("retained comparison posture = %+v", comparison)
	}
	reproducibilityBytes, err := os.ReadFile("reproducibility.json")
	if err != nil {
		t.Fatal(err)
	}
	reproducibility, err := DecodeStrict[ReproducibilityReceipt](reproducibilityBytes)
	if err != nil || ValidateReproducibilityReceipt(reproducibility) != nil {
		t.Fatalf("retained reproducibility validation = %v", err)
	}
	mutatedReproducibility := reproducibility
	mutatedReproducibility.Profiles = slices.Clone(reproducibility.Profiles)
	mutatedReproducibility.Profiles[0].ManifestBytesEqual = false
	if err := ValidateReproducibilityReceipt(mutatedReproducibility); err == nil {
		t.Fatal("reproducibility validator accepted unequal manifest bytes")
	}
	mutatedReproducibility = reproducibility
	mutatedReproducibility.Profiles = slices.Clone(reproducibility.Profiles)
	mutatedReproducibility.Profiles[0].Builds = slices.Clone(reproducibility.Profiles[0].Builds)
	for index := range mutatedReproducibility.Profiles[0].Builds {
		mutatedReproducibility.Profiles[0].Builds[index].Artifacts =
			slices.Clone(reproducibility.Profiles[0].Builds[index].Artifacts)
		mutatedReproducibility.Profiles[0].Builds[index].Artifacts[0].SHA256 =
			"sha256:0000000000000000000000000000000000000000000000000000000000000000"
	}
	if err := ValidateReproducibilityReceipt(mutatedReproducibility); err == nil {
		t.Fatal("reproducibility validator accepted equal forged artifact identities")
	}
	for _, kind := range []string{"structural", "semantic"} {
		profile := byKind[kind]
		oracle, err := DeriveOracle(profile)
		if err != nil {
			t.Fatal(err)
		}
		manifestBytes, err := os.ReadFile(filepath.Join(kind, "manifest.json"))
		if err != nil {
			t.Fatal(err)
		}
		manifest, err := DecodeStrict[Manifest](manifestBytes)
		if err != nil || ValidateManifest(manifest, profile, oracle) != nil {
			t.Fatalf("retained %s manifest validation = %v", kind, err)
		}
		receiptBytes, err := os.ReadFile(filepath.Join(kind, "receipt.json"))
		if err != nil {
			t.Fatal(err)
		}
		receipt, err := DecodeStrict[Receipt](receiptBytes)
		if err != nil || ValidateReceipt(receipt, profile, manifest) != nil {
			t.Fatalf("retained %s receipt validation = %v", kind, err)
		}
		mutated := receipt
		mutated.Artifacts = slices.Clone(receipt.Artifacts)
		mutated.Artifacts[0], mutated.Artifacts[1] =
			mutated.Artifacts[1], mutated.Artifacts[0]
		if err := ValidateReceipt(mutated, profile, manifest); err == nil {
			t.Fatalf("retained %s receipt accepted reordered artifact authority", kind)
		}
	}
}

func TestEveryDimensionAcceptsPositiveAndExactThenRefusesOneOver(t *testing.T) {
	profiles, err := FrozenProfiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range profiles {
		for index, dimension := range profile.Dimensions {
			t.Run(profile.Kind+"/"+dimension.Name, func(t *testing.T) {
				for _, observed := range []uint64{1, dimension.Admitted} {
					if err := CheckDimension(dimension.Name, observed, dimension.Admitted); err != nil {
						t.Fatalf("observed %d: %v", observed, err)
					}
				}
				if err := CheckDimension(dimension.Name, dimension.Admitted+1, dimension.Admitted); !errors.Is(err, ErrDimensionExceeded) {
					t.Fatalf("one-over error = %v", err)
				}
				mutated := profile
				mutated.Dimensions = slices.Clone(profile.Dimensions)
				mutated.Dimensions[index].Baseline++
				if err := ValidateProfile(mutated); err == nil {
					t.Fatal("validator accepted a drifted dimension")
				}
			})
		}
	}
}

func TestIndependentOracleRejectsProfileDerivationDivergence(t *testing.T) {
	profiles, err := FrozenProfiles()
	if err != nil {
		t.Fatal(err)
	}
	profile := profiles[0]
	oracle, err := DeriveOracle(profile)
	if err != nil {
		t.Fatal(err)
	}
	profile.Aggregate.PhysicalOwners++
	err = ValidateOracle(oracle, profile)
	if err == nil || !strings.Contains(err.Error(), "derivations differ") {
		t.Fatalf("profile/oracle divergence error = %v", err)
	}
}

func TestProjectionAuthorIsDeterministicAndReturnsToATree(t *testing.T) {
	for _, kind := range []string{"structural", "semantic"} {
		t.Run(kind, func(t *testing.T) {
			moduleRoot := testModuleRoot(t)
			profile, err := ProjectionProfile(kind)
			if err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			var receipts []Receipt
			for _, name := range []string{"first", "second"} {
				output := filepath.Join(root, name)
				receipt, err := Author(t.Context(), AuthorRequest{ModuleRoot: moduleRoot, Output: output, Profile: profile})
				if err != nil {
					t.Fatal(err)
				}
				receipts = append(receipts, receipt)
				for _, file := range []string{"profile.json", "oracle.json", "manifest.json"} {
					content, err := os.ReadFile(filepath.Join(output, file))
					if err != nil {
						t.Fatal(err)
					}
					if name == "first" {
						t.Cleanup(func() {})
					} else {
						firstContent, readErr := os.ReadFile(filepath.Join(root, "first", file))
						if readErr != nil || !bytes.Equal(firstContent, content) {
							t.Fatalf("%s differs across builds: %v", file, readErr)
						}
					}
				}
				manifestBytes, err := os.ReadFile(filepath.Join(output, "manifest.json"))
				if err != nil {
					t.Fatal(err)
				}
				manifest, err := DecodeStrict[Manifest](manifestBytes)
				if err != nil {
					t.Fatal(err)
				}
				oracle, err := DeriveOracle(profile)
				if err != nil || ValidateManifest(manifest, profile, oracle) != nil {
					t.Fatalf("manifest validation = %v", err)
				}
			}
			if !reflect.DeepEqual(
				deterministicAuthorReceipt(receipts[0]),
				deterministicAuthorReceipt(receipts[1]),
			) {
				t.Fatalf("deterministic receipt identity differs:\n%+v\n%+v", receipts[0], receipts[1])
			}
			pair, err := CompareAuthoredPair(
				filepath.Join(root, "first"), filepath.Join(root, "second"),
			)
			if err != nil || pair.Scale != "projection" || len(pair.Builds) != 2 ||
				!pair.ProfileBytesEqual || !pair.OracleBytesEqual ||
				!pair.ManifestBytesEqual || !pair.RevisionIdentityEqual {
				t.Fatalf("paired author receipt = %+v, error=%v", pair, err)
			}
			if kind == "structural" {
				if receipts[0].Revisions[2].Tree != structuralProjectionTree {
					t.Fatalf("structural projection tree = %s", receipts[0].Revisions[2].Tree)
				}
				if receipts[0].Artifacts[2].SHA256 != structuralProjectionManifestDigest {
					t.Fatalf("structural projection manifest = %s", receipts[0].Artifacts[2].SHA256)
				}
				output, err := runGit(t.Context(), filepath.Join(root, "first", "repository.git"), nil,
					"cat-file", "--batch-check=%(objecttype)", "--batch-all-objects")
				if err != nil {
					t.Fatal(err)
				}
				if count := strings.Count(output, "blob\n"); count != int(profile.Shape.GoBlobReuseClasses)+3 {
					t.Fatalf("blob objects = %d, want %d", count, profile.Shape.GoBlobReuseClasses+3)
				}
			}
			receiptPath := filepath.Join(root, "second", "receipt.json")
			receiptBytes, err := os.ReadFile(receiptPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(receiptPath, append(receiptBytes, '\n'), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := CompareAuthoredPair(
				filepath.Join(root, "first"), filepath.Join(root, "second"),
			); err == nil {
				t.Fatal("paired author validator accepted a noncanonical artifact")
			}
		})
	}
}

func deterministicAuthorReceipt(receipt Receipt) Receipt {
	receipt.ByteMetrics = slices.Clone(receipt.ByteMetrics)
	for index := range receipt.ByteMetrics {
		switch receipt.ByteMetrics[index].Kind {
		case "bare_git_logical", "bare_git_allocated":
			receipt.ByteMetrics[index].Bytes = 0
		}
	}
	return receipt
}

func TestAuthorFailurePointsResumeFromClosedCheckpoint(t *testing.T) {
	profile, err := ProjectionProfile("structural")
	if err != nil {
		t.Fatal(err)
	}
	moduleRoot := testModuleRoot(t)
	for _, point := range FrozenFailurePoints() {
		t.Run(point.Name, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "corpus")
			_, err := Author(t.Context(), AuthorRequest{ModuleRoot: moduleRoot, Output: output, Profile: profile, FailAt: point.Name})
			if !errors.Is(err, ErrInjected) {
				t.Fatalf("injection error = %v", err)
			}
			if _, err := os.Lstat(output); !os.IsNotExist(err) {
				t.Fatal("failure made the final target visible")
			}
			building := output + ".building"
			_, buildingErr := os.Lstat(building)
			if point.Name == "before_git_init" && !os.IsNotExist(buildingErr) {
				t.Fatal("pre-init injection unexpectedly created a checkpoint")
			}
			if point.Name != "before_git_init" && buildingErr != nil {
				t.Fatalf("resumable checkpoint = %v", buildingErr)
			}
			if _, err := Author(t.Context(), AuthorRequest{ModuleRoot: moduleRoot, Output: output, Profile: profile}); err != nil {
				t.Fatalf("resume: %v", err)
			}
			if _, err := os.Stat(filepath.Join(output, "receipt.json")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestStrictDecoderRejectsUnknownFields(t *testing.T) {
	if _, err := DecodeStrict[Envelope]([]byte(`{"schema":"x","unknown":true}`)); err == nil {
		t.Fatal("strict decoder accepted an unknown field")
	}
}

func TestAuthorRefusesWorktreeOutputBeforeCreatingAnything(t *testing.T) {
	profile, err := ProjectionProfile("structural")
	if err != nil {
		t.Fatal(err)
	}
	moduleRoot := testModuleRoot(t)
	output := filepath.Join(moduleRoot, "spike", "t401", "forbidden-author-output")
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		t.Fatalf("test target already exists: %v", err)
	}
	_, err = Author(t.Context(), AuthorRequest{ModuleRoot: moduleRoot, Output: output, Profile: profile})
	if err == nil || !strings.Contains(err.Error(), "may not be inside") {
		t.Fatalf("worktree refusal = %v", err)
	}
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		t.Fatal("worktree refusal created its output")
	}
}

func TestSameSHABlobReaderComparisonOnSmallProjection(t *testing.T) {
	if testing.Short() {
		t.Skip("same-SHA child comparison is an explicit focused gate")
	}
	profile, err := ProjectionProfile("structural")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	authored := filepath.Join(root, "authored")
	moduleRoot := testModuleRoot(t)
	if _, err := Author(t.Context(), AuthorRequest{ModuleRoot: moduleRoot, Output: authored, Profile: profile}); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "zoekt-git-index")
	buildContext, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(buildContext, "go", "build", "-trimpath", "-o", binary,
		"github.com/sourcegraph/zoekt/cmd/zoekt-git-index")
	command.Dir = moduleRoot
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build zoekt-git-index: %v\n%s", err, output)
	}
	receipt, err := CompareBlobReaders(t.Context(), ComparisonRequest{
		ModuleRoot: moduleRoot, Binary: binary,
		Repository: filepath.Join(authored, "repository.git"),
		WorkDir:    filepath.Join(root, "comparison"), Queries: []string{"T401", "padding"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.IndexedContentEqual || !receipt.QueryResultsEqual || receipt.Claims.SelectsProductionReader {
		t.Fatalf("comparison = %+v", receipt)
	}
	if receipt.FixtureProfileDigest == "" || receipt.FixtureManifestDigest == "" ||
		receipt.FixtureTree == "" || !slices.Equal(receipt.Queries, FrozenBlobReaderTrial().Queries) {
		t.Fatalf("comparison fixture binding = %+v", receipt)
	}
	drifted := receipt
	drifted.Queries = []string{"different"}
	if err := ValidateComparison(drifted); err == nil {
		t.Fatal("comparison validator accepted a different query authority")
	}
	// Raw shard bytes are retained as an observation. Semantic identity, not
	// pack-encoding coincidence, is the required equality fence.
	t.Logf("raw shard bytes equal: %t", receipt.IndexBytesEqual)
}

func TestCancellationProbeSelectsOnlyLiveCatFileDescendants(t *testing.T) {
	listing := strings.Join([]string{
		"100 1 S /tmp/zoekt-git-index -index /tmp/index",
		"101 100 T /usr/bin/git cat-file --batch --buffer --filter=blob:limit=2097152",
		"102 100 Z /usr/bin/git cat-file --batch --buffer",
		"103 100 S /usr/bin/git rev-parse HEAD",
		"104 101 S helper",
	}, "\n")
	if got := processTreeGitDescendants(100, listing); !slices.Equal(got, []int{101}) {
		t.Fatalf("live cat-file descendants = %v, want [101]", got)
	}
}

func testModuleRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}
