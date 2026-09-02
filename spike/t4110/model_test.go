package t4110

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/store"
)

func TestT4110DraftBindsFrozenInputsAndClosedInventories(t *testing.T) {
	draft := newTestDraft(t)
	if draft.Inputs.T411EnvelopeSHA256 != "sha256:99ec8a3dc79537bf1db842234f6fe054abd03c9af7503987f78c5530fdfd525f" ||
		draft.Inputs.T411ReceiptSHA256 != "sha256:c9a30ab63960fee682558a04e79b66f1d1fcf2b9a7f2bfc2e3a012139291dc55" ||
		!validDigest(draft.Inputs.TargetProfileSHA256) ||
		!validDigest(draft.Inputs.TransitionProfileSHA256) {
		t.Fatalf("frozen inputs = %+v", draft.Inputs)
	}
	if draft.Population.AcceptedFloor != 8_000 || draft.Population.AcceptedTarget != 10_000 ||
		draft.Population.AcceptedServices != 10_000 || draft.Population.TotalServiceRecords != 10_000 ||
		draft.Population.Memberships != 60_000 || draft.Population.DistinctPaths != 31_600 ||
		draft.Population.RegularFiles != 31_600 || draft.Population.FixtureContentBytes < 1 {
		t.Fatalf("frozen population = %+v", draft.Population)
	}
	if !slices.Equal(namesOfPhases(draft.MeasuredPhases), MeasuredPhaseNames()) ||
		!slices.Equal(namesOfGates(draft.ComposedGates), ComposedGateNames()) ||
		!slices.Equal(namesOfChecks(draft.Checks), CheckNames()) {
		t.Fatal("draft inventories differ from the closed contract")
	}
	for _, gate := range draft.ComposedGates {
		if len(gate.Tests) == 0 || !slices.Equal(gate.Tests, composedGateTests[gate.Name]) {
			t.Fatalf("composed gate %q test identities = %v", gate.Name, gate.Tests)
		}
	}
}

func TestT4110ReceiptRoundTripIsCanonicalAndSourceFree(t *testing.T) {
	receipt := validTestReceipt(t)
	encoded, err := MarshalCanonical(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) >= MaxReceiptBytes {
		t.Fatalf("receipt bytes = %d", len(encoded))
	}
	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, err := MarshalCanonical(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, reencoded) {
		t.Fatal("decoded receipt did not reproduce canonical bytes")
	}
	lower := strings.ToLower(string(encoded))
	for _, forbidden := range forbiddenReceiptFragments {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("receipt contains forbidden fragment %q", forbidden)
		}
	}
}

func TestT4110DecodeRejectsUnknownNoncanonicalOversizedAndSourceBearingJSON(t *testing.T) {
	encoded, err := MarshalCanonical(validTestReceipt(t))
	if err != nil {
		t.Fatal(err)
	}

	unknown := append([]byte(nil), encoded[:len(encoded)-2]...)
	unknown = append(unknown, []byte(",\n  \"unknown\": true\n}\n")...)
	if _, err := Decode(unknown); err == nil {
		t.Fatal("unknown field was accepted")
	}

	noncanonical := append([]byte(" "), encoded...)
	if _, err := Decode(noncanonical); err == nil {
		t.Fatal("noncanonical whitespace was accepted")
	}

	multiple := append(append([]byte(nil), encoded...), []byte("{}\n")...)
	if _, err := Decode(multiple); err == nil {
		t.Fatal("multiple JSON values were accepted")
	}

	if _, err := Decode(bytes.Repeat([]byte{' '}, MaxReceiptBytes+1)); err == nil {
		t.Fatal("oversized receipt was accepted")
	}

	private := bytes.Replace(encoded, []byte(`"go_version": "go1.26.0"`),
		[]byte(`"go_version": "go1.26.0/Users/ben"`), 1)
	if _, err := Decode(private); err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("private path rejection = %v", err)
	}

	rawError := append([]byte(nil), encoded[:len(encoded)-2]...)
	rawError = append(rawError, []byte(",\n  \"raw_error\": \"detail\"\n}\n")...)
	if _, err := Decode(rawError); err == nil || !strings.Contains(err.Error(), "raw_error") {
		t.Fatalf("raw error rejection = %v", err)
	}
}

func TestT4110ValidationRejectsOpenInventoriesClaimsAndPhysicalMultiplication(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Receipt)
	}{
		{name: "phase order", mutate: func(value *Receipt) {
			value.MeasuredPhases[0], value.MeasuredPhases[1] = value.MeasuredPhases[1], value.MeasuredPhases[0]
		}},
		{name: "phase source reread", mutate: func(value *Receipt) {
			value.MeasuredPhases[1].Cost.SearchContentReads = 1
		}},
		{name: "phase source metadata reread", mutate: func(value *Receipt) {
			value.MeasuredPhases[1].Cost.SourceCensusRecords = 1
		}},
		{name: "phase additive counter overflow", mutate: func(value *Receipt) {
			value.MeasuredPhases[1].Cost.StateRowsRead = ^uint64(0)
		}},
		{name: "phase state rows incoherent", mutate: func(value *Receipt) {
			value.MeasuredPhases[0].Cost.StateRowsRead = 1
		}},
		{name: "phase product queries invented", mutate: func(value *Receipt) {
			value.MeasuredPhases[0].Cost.ProductQueries = ^uint64(0)
		}},
		{name: "phase preimage collection invented", mutate: func(value *Receipt) {
			value.MeasuredPhases[2].Cost.PreimageRowsCollected = ^uint64(0)
		}},
		{name: "phase changed rows invented", mutate: func(value *Receipt) {
			value.MeasuredPhases[8].Cost.ChangedRows = ^uint64(0)
		}},
		{name: "cold corpus multiplication", mutate: func(value *Receipt) {
			value.MeasuredPhases[0].Cost.SearchContentReads = uint64(value.Population.RegularFiles) + 1
		}},
		{name: "blank disk custody", mutate: func(value *Receipt) {
			value.MeasuredPhases[0].Cost.DataAllocatedBytes = 0
		}},
		{name: "composed test identity", mutate: func(value *Receipt) {
			value.ComposedGates[0].Tests[0] = "go:TestInventedGate"
		}},
		{name: "check outcome", mutate: func(value *Receipt) {
			value.Checks[0].Outcome = "not_run"
		}},
		{name: "queryability", mutate: func(value *Receipt) {
			value.Queryability.IndependentMatches--
		}},
		{name: "product transport request count", mutate: func(value *Receipt) {
			value.MeasuredPhases[10].Cost.ProductQueries = 3
		}},
		{name: "browser product read count", mutate: func(value *Receipt) {
			value.MeasuredPhases[10].Cost.BrowserProductReads = 1
		}},
		{name: "candidate read accounting invented", mutate: func(value *Receipt) {
			value.MeasuredPhases[0].Cost.CandidateInputReadsUnavailable = false
		}},
		{name: "cold relationship schedule absent", mutate: func(value *Receipt) {
			value.MeasuredPhases[0].Cost.RelationshipScheduleChunks = 0
		}},
		{name: "pressure turn omitted", mutate: func(value *Receipt) {
			value.MeasuredPhases[9].Cost.LifecycleOwnerTurns++
		}},
		{name: "lifecycle member bytes omitted", mutate: func(value *Receipt) {
			value.MeasuredPhases[9].Cost.LifecycleDeletedMemberBytes = 0
		}},
		{name: "archive artifact count drift", mutate: func(value *Receipt) {
			value.MeasuredPhases[8].Cost.ArchiveArtifactCount--
		}},
		{name: "restore byte mismatch", mutate: func(value *Receipt) {
			value.MeasuredPhases[8].Cost.RestoredArtifactBytes++
		}},
		{name: "archive byte bound", mutate: func(value *Receipt) {
			value.MeasuredPhases[8].Cost.ArchiveArtifactBytes = maxRecoveryArtifactBytes + 1
			value.MeasuredPhases[8].Cost.RestoredArtifactBytes = maxRecoveryArtifactBytes + 1
		}},
		{name: "archive work in query phase", mutate: func(value *Receipt) {
			value.MeasuredPhases[2].Cost.RestoredArtifactCount = recoveryArtifactCount
		}},
		{name: "physical oracle", mutate: func(value *Receipt) {
			value.PhysicalWork.CorpusPasses = 0
		}},
		{name: "physical corpus pass multiplication", mutate: func(value *Receipt) {
			value.PhysicalWork.CorpusPasses = 2
		}},
		{name: "claim promotion", mutate: func(value *Receipt) {
			value.NonClaims.SupportedCustomerLimit = true
		}},
		{name: "teardown", mutate: func(value *Receipt) {
			value.Teardown.ChildrenRemaining = 1
		}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			value := validTestReceipt(t)
			testCase.mutate(&value)
			if err := ValidateReceipt(value); err == nil {
				t.Fatal("mutated receipt was accepted")
			}
		})
	}
}

func TestT4110AuthorIsCreateOnlyAndAtomic(t *testing.T) {
	receipt := validTestReceipt(t)
	want, err := MarshalCanonical(receipt)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	destination := filepath.Join(directory, "results.json")
	identity, err := author(destination, receipt)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) || identity.Bytes != len(want) || identity.SHA256 != digest(want) {
		t.Fatalf("authored receipt identity = %+v", identity)
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("receipt mode = %o", info.Mode().Perm())
	}
	if _, err := author(destination, receipt); err == nil {
		t.Fatal("author overwrote an existing receipt")
	}
	after, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, want) {
		t.Fatal("failed re-author changed retained bytes")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "results.json" {
		t.Fatalf("author left temporary entries: %v", entries)
	}
}

type receiptDirectoryStub struct {
	syncErr  error
	closeErr error
}

func (directory *receiptDirectoryStub) Sync() error  { return directory.syncErr }
func (directory *receiptDirectoryStub) Close() error { return directory.closeErr }

func TestT4110LinkedReceiptDurabilityFailureRemovesDestinationAndJoinsCleanup(t *testing.T) {
	openErr := errors.New("open durability failure")
	syncErr := errors.New("sync durability failure")
	closeErr := errors.New("close cleanup failure")
	removeErr := errors.New("remove cleanup failure")
	tests := []struct {
		name string
		open func(string) (receiptDirectory, error)
		want []error
	}{
		{
			name: "open",
			open: func(string) (receiptDirectory, error) {
				return nil, openErr
			},
			want: []error{openErr, removeErr},
		},
		{
			name: "sync",
			open: func(string) (receiptDirectory, error) {
				return &receiptDirectoryStub{syncErr: syncErr, closeErr: closeErr}, nil
			},
			want: []error{syncErr, closeErr, removeErr},
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			directory := t.TempDir()
			destination := filepath.Join(directory, "results.json")
			if err := os.WriteFile(destination, []byte("linked"), 0o600); err != nil {
				t.Fatal(err)
			}
			err := syncLinkedReceipt(
				directory,
				destination,
				testCase.open,
				func(path string) error {
					return errors.Join(os.Remove(path), removeErr)
				},
			)
			for _, want := range testCase.want {
				if !errors.Is(err, want) {
					t.Fatalf("durability error %v does not contain %v", err, want)
				}
			}
			if _, statErr := os.Lstat(destination); !os.IsNotExist(statErr) {
				t.Fatalf("failed publication destination remains: %v", statErr)
			}
		})
	}
}

func TestT4110LinkedReceiptCleanupRequiresExactHardLink(t *testing.T) {
	directory := t.TempDir()
	temporary := filepath.Join(directory, "temporary.json")
	destination := filepath.Join(directory, "results.json")
	if err := os.WriteFile(temporary, []byte("receipt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(temporary, destination); err != nil {
		t.Fatal(err)
	}
	if err := removeLinkedReceipt(temporary, destination); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Fatalf("exact linked destination remains: %v", err)
	}
	if err := os.WriteFile(destination, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removeLinkedReceipt(temporary, destination); err == nil {
		t.Fatal("replacement destination was removed as the authored receipt")
	}
	got, err := os.ReadFile(destination)
	if err != nil || !bytes.Equal(got, []byte("replacement")) {
		t.Fatalf("replacement destination = %q, %v", got, err)
	}
}

func newTestDraft(t *testing.T) Receipt {
	t.Helper()
	draft, err := newDraft(strings.Repeat("a", 40), "2026-09-01", Environment{
		GOOS: "darwin", GOARCH: "arm64", GoVersion: "go1.26.0",
		LogicalCPUs: 10, GOMAXPROCS: 10,
		RSSMethod: RSSMethodProcessTree, DiskMethod: DiskMethodWalk,
	})
	if err != nil {
		t.Fatal(err)
	}
	return draft
}

func validTestReceipt(t *testing.T) Receipt {
	t.Helper()
	receipt := newTestDraft(t)
	digest := "sha256:" + strings.Repeat("a", 64)
	receipt.Implementation.AuthorExecutableSHA256 = digest
	receipt.Implementation.PhebsExecutableSHA256 = digest
	receipt.Implementation.ZoektExecutableSHA256 = digest
	receipt.Implementation.GitExecutableSHA256 = digest
	receipt.Implementation.SurrealExecutableSHA256 = digest
	receipt.Implementation.GoExecutableSHA256 = digest
	receipt.Implementation.NodeExecutableSHA256 = digest
	receipt.Implementation.NPMExecutableSHA256 = digest
	receipt.Implementation.BrowserExecutableSHA256 = digest
	receipt.Outcome = OutcomePassed
	receipt.Queryability = QueryabilityOracle{
		PublishedAcceptedServices: receipt.Population.AcceptedTarget,
		CurrentAcceptedServices:   receipt.Population.AcceptedTarget,
		IndependentQueries:        receipt.Population.AcceptedTarget,
		IndependentMatches:        receipt.Population.AcceptedTarget,
	}
	receipt.PhysicalWork = PhysicalWorkOracle{CorpusPasses: 1}
	for index := range receipt.MeasuredPhases {
		receipt.MeasuredPhases[index].Outcome = StepPassed
		receipt.MeasuredPhases[index].Cost = PhaseCost{
			WallMilliseconds: 1, PeakRSSBytes: 1,
			DataLogicalBytes: 1, DataAllocatedBytes: 1,
			SelectedStateRootReads: 1, SelectedStateMemberReads: 1,
			SelectedStateRootValidations: 1, SelectedStateMemberValidations: 1,
		}
	}
	receipt.MeasuredPhases[0].Cost.SearchFilesOffered = uint64(receipt.Population.RegularFiles)
	receipt.MeasuredPhases[0].Cost.SearchContentReads = uint64(receipt.Population.RegularFiles)
	receipt.MeasuredPhases[0].Cost.SearchDeclaredBytes = uint64(receipt.Population.FixtureContentBytes)
	receipt.MeasuredPhases[0].Cost.SourceCensusRecords = uint64(receipt.Population.RegularFiles)
	receipt.MeasuredPhases[0].Cost.SourceCensusMembers = 1
	receipt.MeasuredPhases[0].Cost.SourceCensusPlacements = uint64(receipt.Population.RegularFiles)
	receipt.MeasuredPhases[0].Cost.SourceCensusDeclaredBytes = uint64(receipt.Population.FixtureContentBytes)
	receipt.MeasuredPhases[0].Cost.CandidateInputReadsUnavailable = true
	receipt.MeasuredPhases[0].Cost.CandidateDeclaredBytes = uint64(receipt.Population.FixtureContentBytes)
	receipt.MeasuredPhases[0].Cost.RelationshipScheduleChunks = 1
	receipt.MeasuredPhases[0].Cost.RelationshipComponentPublishes = 3
	receipt.MeasuredPhases[0].Cost.RelationshipPublishes = 1
	receipt.MeasuredPhases[0].Cost.RelationshipServiceMembers = 1
	receipt.MeasuredPhases[0].Cost.RelationshipServiceRecords = uint64(receipt.Population.AcceptedTarget)
	receipt.MeasuredPhases[0].Cost.ChangedRows = uint64(receipt.Population.AcceptedTarget)
	receipt.MeasuredPhases[0].Cost.StateRowsRead = uint64(receipt.Population.AcceptedTarget) * 2
	receipt.MeasuredPhases[0].Cost.StateRowsApplied = uint64(receipt.Population.AcceptedTarget) * 2
	receipt.MeasuredPhases[0].Cost.StateChunkTransactions =
		(receipt.MeasuredPhases[0].Cost.StateRowsRead + store.MaxServiceStateV3ChunkRows - 1) /
			store.MaxServiceStateV3ChunkRows
	receipt.MeasuredPhases[1].Cost.ProductQueries = 1
	receipt.MeasuredPhases[2].Cost.ProductQueries = uint64(receipt.Population.AcceptedTarget + 1)
	for index, value := range []struct {
		changed, rows, summaries, collected, retired, queries, records uint64
	}{
		{changed: 1, rows: 1, summaries: 1, queries: 4, records: 10_000},
		{changed: 100, rows: 100, summaries: 1, collected: 1, retired: 1, queries: 103, records: 10_000},
		{changed: 2, rows: 2, summaries: 2, collected: 101, retired: 2, queries: 8, records: 19_999},
		{changed: 2, rows: 2, summaries: 2, collected: 2, retired: 2, queries: 8, records: 20_000},
		{changed: 10, rows: 10, summaries: 4, collected: 8, retired: 4, queries: 32, records: 39_991},
	} {
		phase := &receipt.MeasuredPhases[index+3]
		phase.Cost.ChangedRows = value.changed
		phase.Cost.StateChunkTransactions = 1
		phase.Cost.StateRowsRead = value.changed
		phase.Cost.StateRowsApplied = value.changed
		phase.Cost.PreimageRowsWritten = value.rows
		phase.Cost.PreimageSummariesWritten = value.summaries
		phase.Cost.PreimageRowsCollected = value.collected
		phase.Cost.PreimageSummariesCollected = value.retired
		phase.Cost.LifecycleRecordsDeleted = value.collected + value.retired
		phase.Cost.RelationshipScheduleChunks = value.summaries
		phase.Cost.RelationshipPublishes = value.summaries
		phase.Cost.RelationshipServiceMembers = value.summaries
		phase.Cost.RelationshipServiceRecords = value.records
		phase.Cost.ProductQueries = value.queries
	}
	receipt.MeasuredPhases[8].Cost.ProductQueries = 2
	receipt.MeasuredPhases[8].Cost.LifecycleRecordsDeleted = 1
	receipt.MeasuredPhases[8].Cost.ArchiveArtifactCount = recoveryArtifactCount
	receipt.MeasuredPhases[8].Cost.ArchiveArtifactBytes = recoveryArtifactCount
	receipt.MeasuredPhases[8].Cost.RestoredArtifactCount = recoveryArtifactCount
	receipt.MeasuredPhases[8].Cost.RestoredArtifactBytes = recoveryArtifactCount
	receipt.MeasuredPhases[9].Cost.ProductQueries = 1
	receipt.MeasuredPhases[9].Cost.LifecycleRecordsDeleted = 4
	receipt.MeasuredPhases[9].Cost.LifecycleRetiredLogicalBytes = 1
	receipt.MeasuredPhases[9].Cost.LifecycleDeletedRootBytes = 1
	receipt.MeasuredPhases[9].Cost.LifecycleDeletedMemberBytes = 1
	receipt.MeasuredPhases[9].Cost.PreimageRowsCollected = 3
	receipt.MeasuredPhases[9].Cost.PreimageSummariesCollected = 1
	receipt.MeasuredPhases[9].Cost.LifecycleOwnerTurns = 3
	receipt.MeasuredPhases[9].Cost.LifecyclePressureCollectObservations = 1
	receipt.MeasuredPhases[9].Cost.LifecyclePressureNormalObservations = 2
	receipt.MeasuredPhases[10].Cost.ProductQueries = 9
	receipt.MeasuredPhases[10].Cost.BrowserProductReads = 3
	for index := range receipt.ComposedGates {
		receipt.ComposedGates[index].Outcome = StepPassed
	}
	for index := range receipt.Checks {
		receipt.Checks[index].Outcome = StepPassed
	}
	receipt.Teardown = Teardown{StoreClosed: true, TemporaryCustodyRemoved: true}
	return receipt
}

func namesOfPhases(values []MeasuredPhase) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.Name
	}
	return result
}

func namesOfGates(values []ComposedGate) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.Name
	}
	return result
}

func namesOfChecks(values []Check) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.Name
	}
	return result
}

func TestT4110ReceiptJSONContainsNoUnmodeledMaps(t *testing.T) {
	encoded, err := json.Marshal(validTestReceipt(t))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(`"metadata"`)) || bytes.Contains(encoded, []byte(`"details"`)) {
		t.Fatal("receipt gained an open metadata/detail map")
	}
}
