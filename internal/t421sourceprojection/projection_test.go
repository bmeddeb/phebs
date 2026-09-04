package t421sourceprojection

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/repositoryindex"
)

func TestAccumulatorIsDeterministicCancellableAndFailClosed(t *testing.T) {
	policies := []candidate.Policy{
		testPolicy("proto-domain", ".proto"),
		testPolicy("go-domain", ".go"),
	}
	records := []repositoryindex.SourceRecord{
		testSourceRecord("a.go", "100644", "regular", strings.Repeat("1", 40), 3),
		testSourceRecord("dir/b.proto", "100755", "regular", strings.Repeat("2", 40), 5),
		testSourceRecord("dir/link.thrift", "120000", "symlink", strings.Repeat("3", 40), 7),
		testSourceRecord("submodule", "160000", "gitlink", strings.Repeat("4", 40), 0),
	}

	t.Run("exact deterministic identities", func(t *testing.T) {
		got := projectRecords(t, context.Background(), policies, false, records)
		want := Projection{
			TreeInventory: SetIdentity{
				Records: 4, FramedBytes: 403,
				SHA256: "sha256:748dbb23bd6d0739cdc8419ec36fa3c827bd3180e7d49607f4c0aed1a897b4bb",
			},
			ObservationInputInventory: SetIdentity{
				Records: 3, FramedBytes: 319,
				SHA256: "sha256:5712b93a102c9cd3f28fd44f8d5395f12d687c2bdb4b54e998699e6b090bfc94",
			},
			CandidateInventories: []CandidateInventory{
				{
					Domain: "go-domain",
					Candidates: SetIdentity{
						Records: 1, FramedBytes: 137,
						SHA256: "sha256:97e2cb82b107b5eec85f2e9fe47b17194afd3262fdae55707c8316e8b26d4e8a",
					},
					Proof: SetIdentity{
						Records: 1, FramedBytes: 297,
						SHA256: "sha256:8ac2425cc532a354b94bf11b778955551351696dd7170818eaafe185873fdb4e",
					},
				},
				{
					Domain: "proto-domain",
					Candidates: SetIdentity{
						Records: 1, FramedBytes: 147,
						SHA256: "sha256:d661b2b643dbd79a9600f20ea551fc8f3b93aeef0a90cf5eaed3211fe22716ab",
					},
					Proof: SetIdentity{
						Records: 1, FramedBytes: 310,
						SHA256: "sha256:5e35dd446e1daa1d1cca0328b675bcb1fc3c7227870c7d62df814a7045d79f1f",
					},
				},
			},
			// Verified independently with git mktree in a SHA-1 repository.
			TreeOID: "a8ff5577246b0880a7c62ef09c23c551fc683de4",
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("projection = %+v, want %+v", got, want)
		}

		reordered := []candidate.Policy{policies[1], policies[0]}
		if again := projectRecords(t, context.Background(), reordered, false, records); !reflect.DeepEqual(again, got) {
			t.Fatalf("policy order changed projection: %+v", again)
		}

		goOnly := projectRecords(t, context.Background(), policies, true, records)
		wantGoOnly := SetIdentity{
			Records: 1, FramedBytes: 135,
			SHA256: "sha256:b327e3659345d289e0e5925e29a1b0909c4bd39097166a3c4726c46ab81329cc",
		}
		if goOnly.ObservationInputInventory != wantGoOnly ||
			goOnly.TreeInventory != got.TreeInventory || goOnly.TreeOID != got.TreeOID ||
			!reflect.DeepEqual(goOnly.CandidateInventories, got.CandidateInventories) {
			t.Fatalf("go-only projection = %+v", goOnly)
		}

		sha256Record := testSourceRecord(
			"a.go", "100644", "regular", strings.Repeat("1", 64), 3,
		)
		sha256Projection := projectRecords(
			t, context.Background(), policies, false, []repositoryindex.SourceRecord{sha256Record},
		)
		// Verified independently with git mktree in a SHA-256 repository.
		if sha256Projection.TreeOID != "f88635f24893ef546d04f571c07cd0a91261cc36735c09d52cd9fcd5f06f7003" {
			t.Fatalf("SHA-256 tree = %s", sha256Projection.TreeOID)
		}
	})

	t.Run("cancellation is sticky", func(t *testing.T) {
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := New(canceled, policies, false); !errors.Is(err, context.Canceled) {
			t.Fatalf("new canceled accumulator = %v", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		accumulator, err := New(ctx, policies, false)
		if err != nil {
			t.Fatal(err)
		}
		if err := accumulator.Add(records[0]); err != nil {
			t.Fatal(err)
		}
		cancel()
		if err := accumulator.Add(records[1]); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled add = %v", err)
		}
		if _, err := accumulator.Finish(); !errors.Is(err, context.Canceled) {
			t.Fatalf("finish after canceled add = %v", err)
		}
	})

	t.Run("invalid input poisons the accumulator", func(t *testing.T) {
		for _, test := range []struct {
			name string
			add  []repositoryindex.SourceRecord
		}{
			{
				name: "unordered",
				add:  []repositoryindex.SourceRecord{records[1], records[0]},
			},
			{
				name: "negative declared bytes",
				add: []repositoryindex.SourceRecord{
					testSourceRecord("bad.go", "100644", "regular", strings.Repeat("1", 40), -1),
				},
			},
			{
				name: "invalid special mode",
				add: []repositoryindex.SourceRecord{
					testSourceRecord("bad", "100644", "gitlink", strings.Repeat("1", 40), 0),
				},
			},
			{
				name: "file directory collision",
				add: []repositoryindex.SourceRecord{
					testSourceRecord("node", "100644", "regular", strings.Repeat("1", 40), 1),
					testSourceRecord("node/child.go", "100644", "regular", strings.Repeat("2", 40), 1),
				},
			},
			{
				name: "mixed object formats",
				add: []repositoryindex.SourceRecord{
					testSourceRecord("a.go", "100644", "regular", strings.Repeat("1", 40), 1),
					testSourceRecord("b.go", "100644", "regular", strings.Repeat("2", 64), 1),
				},
			},
		} {
			t.Run(test.name, func(t *testing.T) {
				accumulator, err := New(context.Background(), policies, false)
				if err != nil {
					t.Fatal(err)
				}
				var addErr error
				for _, record := range test.add {
					if addErr = accumulator.Add(record); addErr != nil {
						break
					}
				}
				if !errors.Is(addErr, ErrInvalid) {
					t.Fatalf("invalid add = %v", addErr)
				}
				if _, err := accumulator.Finish(); !errors.Is(err, ErrInvalid) {
					t.Fatalf("finish after invalid add = %v", err)
				}
			})
		}

		empty, err := New(context.Background(), policies, false)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := empty.Finish(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("empty projection = %v", err)
		}
	})
}

func TestCandidateProofIsOrderIndependentAndContentExact(t *testing.T) {
	build := func(records [][4]string) SetIdentity {
		t.Helper()
		proof := NewCandidateProof("caller")
		for _, record := range records {
			if err := proof.Add(
				record[0], record[1], 7, record[3] == "required",
			); err != nil {
				t.Fatal(err)
			}
		}
		result, err := proof.Finish()
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	a := [4]string{"a.go", strings.Repeat("1", 40), "7", "required"}
	b := [4]string{"b.go", strings.Repeat("2", 40), "7", ""}
	if left, right := build([][4]string{a, b}), build([][4]string{b, a}); left != right {
		t.Fatalf("candidate proof changed with order: %+v != %+v", left, right)
	}
	b[1] = strings.Repeat("3", 40)
	if left, right := build([][4]string{a, b}), build([][4]string{a, {
		"b.go", strings.Repeat("2", 40), "7", "",
	}}); left == right {
		t.Fatal("same-count candidate change retained the proof")
	}
}

func testPolicy(domain, suffix string) candidate.Policy {
	return candidate.Policy{
		Domain: domain, Version: "test-v1",
		EnumerationPolicy: "suffix-test-v1", SymlinkPolicy: "none",
		Plane: candidate.PlaneRepository,
		Enumerate: func(path string) bool {
			return strings.HasSuffix(path, suffix)
		},
	}
}

func testSourceRecord(
	path, mode, kind, objectID string,
	declaredBytes int64,
) repositoryindex.SourceRecord {
	return repositoryindex.SourceRecord{
		Schema: repositoryindex.SourceMemberSchema,
		Path:   path, Mode: mode, Kind: kind, ObjectID: objectID,
		DeclaredBytes: declaredBytes, Revisions: []int{0},
	}
}

func projectRecords(
	t *testing.T,
	ctx context.Context,
	policies []candidate.Policy,
	goOnly bool,
	records []repositoryindex.SourceRecord,
) Projection {
	t.Helper()
	accumulator, err := New(ctx, policies, goOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if err := accumulator.Add(record); err != nil {
			t.Fatal(err)
		}
	}
	projection, err := accumulator.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return projection
}
