package extractionpublication

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestReservationWeightsTypedScopePolicy(t *testing.T) {
	typed := func(records int) candidate.ExtractionPartition {
		return candidate.ExtractionPartition{
			Kind:       candidate.PartitionKindTypedInput,
			TypedScope: &candidate.SparseTypedScopeDescriptor{Records: records},
		}
	}
	member := func(records int) candidate.ExtractionPartition {
		return candidate.ExtractionPartition{
			Kind: candidate.PartitionKindCandidateMember, AdmittedRecords: records,
		}
	}
	for _, test := range []struct {
		name       string
		partitions []candidate.ExtractionPartition
		typedMode  bool
		want       []int64
	}{
		{
			name:       "historical mixed plan",
			partitions: []candidate.ExtractionPartition{member(300), member(200), typed(1110)},
			want:       []int64{300, 200, 1},
		},
		{
			name:       "typed-scope mixed plan",
			partitions: []candidate.ExtractionPartition{member(300), member(200), typed(1110)},
			typedMode:  true,
			want:       []int64{300, 200, 1110},
		},
		{
			name:       "empty typed scope keeps minimum one",
			partitions: []candidate.ExtractionPartition{member(7), typed(0)},
			typedMode:  true,
			want:       []int64{7, 1},
		},
		{
			name:       "empty scope historical mode unchanged",
			partitions: []candidate.ExtractionPartition{member(7), typed(0)},
			want:       []int64{7, 1},
		},
		{
			name:       "zero admitted member keeps minimum one",
			partitions: []candidate.ExtractionPartition{member(0), typed(5)},
			typedMode:  true,
			want:       []int64{1, 5},
		},
		{name: "no partitions", partitions: nil, want: []int64{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			weights := reservationWeights(test.partitions, test.typedMode)
			if !reflect.DeepEqual(weights, test.want) {
				t.Fatalf("weights = %v, want %v", weights, test.want)
			}
		})
	}
}

func TestAssignReservedRoundingCapsAndBoundaries(t *testing.T) {
	assign := func(weights []int64, perPartition, aggregate int64) []int64 {
		values := make([]int64, len(weights))
		assignReserved(weights, perPartition, aggregate, func(index int, value int64) {
			values[index] = value
		})
		return values
	}
	if got := assign([]int64{1, 1, 1}, 4, 10); !reflect.DeepEqual(got, []int64{4, 3, 3}) {
		t.Fatalf("largest-remainder allocation = %v", got)
	}
	if got := assign([]int64{1, 1, 1}, 4, 10); !reflect.DeepEqual(got, []int64{4, 3, 3}) {
		t.Fatalf("allocation replay = %v", got)
	}
	if got := assign([]int64{5, 1}, 3, 100); !reflect.DeepEqual(got, []int64{3, 3}) {
		t.Fatalf("capped allocation = %v", got)
	}
	if got := assign([]int64{1, 1}, 4, 0); !reflect.DeepEqual(got, []int64{0, 0}) {
		t.Fatalf("zero-aggregate allocation = %v", got)
	}
	if got := assign(nil, 4, 10); len(got) != 0 {
		t.Fatalf("empty allocation = %v", got)
	}
	boundary := assign([]int64{1, 1}, math.MaxInt64, math.MaxInt64)
	if boundary[0] != boundary[1]+1 || boundary[0]+boundary[1] != math.MaxInt64 {
		t.Fatalf("near-overflow allocation = %v", boundary)
	}
	product := new(big.Int).Mul(big.NewInt(math.MaxInt64), big.NewInt(2))
	share, remainder := proportionalShare(math.MaxInt64, 2, 3)
	if big.NewInt(share).Cmp(new(big.Int).Div(product, big.NewInt(3))) != 0 ||
		big.NewInt(remainder).Cmp(new(big.Int).Mod(product, big.NewInt(3))) != 0 {
		t.Fatalf("overflow-safe share = %d rem %d", share, remainder)
	}
}

func TestReservedPlanTypedScopeWeightsAndPolicyIdentity(t *testing.T) {
	repositoryDirectory := t.TempDir()
	runGit(t, repositoryDirectory, "init", "-q")
	runGit(t, repositoryDirectory, "config", "user.email", "planner@example.invalid")
	runGit(t, repositoryDirectory, "config", "user.name", "Planner Test")
	paths := make([]string, 0, 5)
	for index := range 3 {
		name := fmt.Sprintf("schema%d.proto", index)
		if err := os.WriteFile(
			filepath.Join(repositoryDirectory, name),
			[]byte("syntax = \"proto3\";\nmessage Fixture {}\n"), 0o600,
		); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, name)
	}
	if err := os.WriteFile(
		filepath.Join(repositoryDirectory, "index.scip"), []byte("typed-index"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	paths = append(paths, "index.scip")
	runGit(t, repositoryDirectory, append([]string{"add", "--"}, paths...)...)
	runGit(t, repositoryDirectory, "commit", "-q", "-m", "fixture")
	commit := strings.TrimSpace(runGit(t, repositoryDirectory, "rev-parse", "HEAD"))
	repository := "example.invalid/planner-typed"
	policies := []candidate.Policy{{
		Domain: "proto-contract", Version: "3.0.0",
		EnumerationPolicy: "proto-contract-paths-v1", Plane: candidate.PlaneRepository,
		Enumerate:   func(path string) bool { return strings.HasSuffix(path, ".proto") },
		Required:    func(path string) bool { return strings.HasSuffix(path, ".proto") },
		TypedInputs: []string{"scip"},
	}}
	identities, err := candidate.PolicyIdentities(policies)
	if err != nil {
		t.Fatal(err)
	}
	policyDigest, err := candidate.PolicyDigest(identities)
	if err != nil {
		t.Fatal(err)
	}
	candidateDirectory := t.TempDir()
	manifest, err := candidate.Build(t.Context(), candidate.Request{
		RepoDir: repositoryDirectory, OutputDir: candidateDirectory,
		Repository: repository, Commit: commit, Policies: policies,
	})
	if err != nil {
		t.Fatal(err)
	}
	publication, err := candidate.Open(candidateDirectory, candidate.Expected{
		Repository: repository, Commit: commit, Policies: identities,
		PolicyDigest: policyDigest, GenerationDigest: manifest.GenerationDigest,
		ManifestDigest: manifest.Digest,
	})
	if err != nil {
		t.Fatal(err)
	}
	sparseDirectory := t.TempDir()
	root, err := candidate.BuildSparseRoot(t.Context(), sparseDirectory, publication, nil)
	if err != nil {
		t.Fatal(err)
	}
	sparse, err := candidate.OpenSparse(
		t.Context(), sparseDirectory, candidateDirectory, manifest.State(), root.Digest, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	domain, err := sparse.OpenDomain(t.Context(), "proto-contract", "3.0.0")
	if err != nil {
		t.Fatal(err)
	}
	partitions := domain.Partitions()
	memberOrdinal, typedOrdinal := -1, -1
	for ordinal, partition := range partitions {
		switch partition.Kind {
		case candidate.PartitionKindCandidateMember:
			memberOrdinal = ordinal
		case candidate.PartitionKindTypedInput:
			typedOrdinal = ordinal
		}
	}
	if len(partitions) != 2 || memberOrdinal != 0 || typedOrdinal != 1 ||
		partitions[memberOrdinal].AdmittedRecords != 3 {
		t.Fatalf("fixture partitions = %+v", partitions)
	}
	if typed := partitions[typedOrdinal]; typed.AdmittedRecords != 0 ||
		typed.TypedScope == nil || typed.TypedScope.Records != 3 {
		t.Fatalf("fixture typed partition = %+v", partitions[typedOrdinal])
	}
	authority := func(policy string) candidate.DomainResultAuthority {
		return candidate.DomainResultAuthority{
			SourceGenerationDigest:      "sha256:" + strings.Repeat("c", 64),
			ObservationGenerationDigest: "sha256:" + strings.Repeat("d", 64),
			ExtractorVersion:            "3.0.0",
			ExtractionPolicyDigest:      policy,
		}
	}
	historicalPolicy, err := candidate.ExtractionPolicyDigest(policyDigest, false, false)
	if err != nil {
		t.Fatal(err)
	}
	correctedPolicy, err := candidate.ExtractionPolicyDigest(policyDigest, false, true)
	if err != nil || historicalPolicy == correctedPolicy {
		t.Fatalf("policy identities not separated: %v", err)
	}
	historical, err := BuildReservedPlan(domain, authority(historicalPolicy), false)
	if err != nil {
		t.Fatal(err)
	}
	corrected, err := BuildReservedPlan(domain, authority(correctedPolicy), true)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := BuildReservedPlan(domain, authority(correctedPolicy), true)
	if err != nil || !reflect.DeepEqual(corrected, replayed) {
		t.Fatalf("corrected plan replay diverged: %v", err)
	}
	if historical.ExtractionPolicyDigest != historicalPolicy ||
		corrected.ExtractionPolicyDigest != correctedPolicy ||
		historical.Digest == corrected.Digest {
		t.Fatalf("plans did not bind their own policy identity: %s, %s", historical.Digest, corrected.Digest)
	}
	quarter, half := candidate.MaxDomainResultEncodedBytes/4, candidate.MaxDomainResultEncodedBytes/2
	if historical.Expected[typedOrdinal].Reservation.EncodedBytes != quarter ||
		historical.Expected[memberOrdinal].Reservation.EncodedBytes != 3*quarter ||
		corrected.Expected[typedOrdinal].Reservation.EncodedBytes != half ||
		corrected.Expected[memberOrdinal].Reservation.EncodedBytes != half {
		t.Fatalf(
			"encoded reservations historical=(%d, %d) corrected=(%d, %d)",
			historical.Expected[memberOrdinal].Reservation.EncodedBytes,
			historical.Expected[typedOrdinal].Reservation.EncodedBytes,
			corrected.Expected[memberOrdinal].Reservation.EncodedBytes,
			corrected.Expected[typedOrdinal].Reservation.EncodedBytes,
		)
	}
}

func TestCandidateMemberAggregateMeasurementPinsFrozenStructuralProfile(t *testing.T) {
	partitions := make([]candidate.ExtractionPartition, 489)
	for index := range partitions {
		bytes := int64(4096 * 396)
		if index == len(partitions)-1 {
			bytes = 1152 * 396
		}
		partitions[index].Member = &candidate.Artifact{ContentBytes: bytes}
	}
	err := measureCandidateMemberBytes(partitions, candidate.MaxDomainResultMemberBytes)
	var measurement *pipelinerefusal.Measurement
	if !errors.Is(err, ErrLimit) || !errors.Is(err, candidate.ErrInvalidDomainResult) ||
		!errors.As(err, &measurement) ||
		measurement.Dimension != pipelinerefusal.DimensionCandidateMemberBytes ||
		measurement.Observed != 792_000_000 || measurement.Limit != 67_108_864 {
		t.Fatalf("measurement = %+v, error=%v", measurement, err)
	}
}

func TestCandidateMemberAggregateExactBoundAndOneOver(t *testing.T) {
	for _, test := range []struct {
		name      string
		bytes     int64
		wantLimit bool
	}{
		{name: "exact", bytes: candidate.MaxDomainResultAggregateMemberBytesV2},
		{name: "one over", bytes: candidate.MaxDomainResultAggregateMemberBytesV2 + 1, wantLimit: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := measureCandidateMemberBytes([]candidate.ExtractionPartition{{
				Member: &candidate.Artifact{ContentBytes: test.bytes},
			}}, candidate.MaxDomainResultAggregateMemberBytesV2)
			if errors.Is(err, ErrLimit) != test.wantLimit {
				t.Fatalf("error = %v, want limit=%t", err, test.wantLimit)
			}
		})
	}
}

func TestPlanningLimitIsClosedTerminalAndDurable(t *testing.T) {
	private := errors.New("private repository/path")
	measured := pipelinerefusal.Measure(
		private, pipelinerefusal.DimensionCandidateMemberBytes,
		792_000_000, 67_108_864,
	)
	err := terminalPlanFailure(errors.Join(ErrLimit, measured))
	if !store.IsTerminal(err) || !errors.Is(err, private) {
		t.Fatalf("terminal error = %v", err)
	}
	receipt, ok := pipelinerefusal.From(err)
	if !ok || receipt.Stage != pipelinerefusal.StageDomainInventory ||
		receipt.GenerationKind != pipelinerefusal.GenerationExtractionDomain ||
		receipt.Classification != pipelinerefusal.ClassificationLimit ||
		receipt.Dimension != pipelinerefusal.DimensionCandidateMemberBytes ||
		strings.Contains(store.DurableErrorText(err), "private") {
		t.Fatalf("durable refusal = %+v text=%q", receipt, store.DurableErrorText(err))
	}
}
