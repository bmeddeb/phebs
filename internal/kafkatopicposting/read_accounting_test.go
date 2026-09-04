package kafkatopicposting

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/sourcepartition"
)

type kafkaReadAccountingFixture struct {
	root        string
	publication *Publication
	receipt     MemberReceipt
	receiptAt   int
}

func TestMemberVisitAccounting(t *testing.T) {
	t.Run("valid rereads charge one root control and members", func(t *testing.T) {
		fixture := newKafkaReadAccountingFixture(t)
		want := uint64(2 * fixture.receipt.PostingCount)
		ctx, ledger, err := readaccounting.Start(
			t.Context(), readaccounting.Counts{
				ControlFileReads: 3, MemberVisits: want,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		root := fixture.publication.Root()
		opened, err := OpenGeneration(
			ctx, fixture.root, root.Authority.Repository, root.GenerationDigest, root.Digest,
		)
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			values, readErr := opened.ReadTopic(ctx, "producer", "orders-v1")
			if readErr != nil || len(values) != 1 {
				t.Fatalf("topic reread = %d, %v", len(values), readErr)
			}
		}
		counts, err := ledger.Finish()
		expected := readaccounting.Counts{ControlFileReads: 3, MemberVisits: want}
		if err != nil || counts != expected {
			t.Fatalf("reread counts = %+v, %v", counts, err)
		}
	})

	t.Run("root control limit refuses before members", func(t *testing.T) {
		fixture := newKafkaReadAccountingFixture(t)
		ctx, ledger, err := readaccounting.Start(
			t.Context(), readaccounting.Counts{MemberVisits: 1},
		)
		if err != nil {
			t.Fatal(err)
		}
		root := fixture.publication.Root()
		if _, err := OpenGeneration(
			ctx, fixture.root, root.Authority.Repository, root.GenerationDigest, root.Digest,
		); !errors.Is(err, readaccounting.ErrLimit) {
			t.Fatalf("root control limit = %v", err)
		}
		counts, finishErr := ledger.Finish()
		if !errors.Is(finishErr, readaccounting.ErrLimit) ||
			counts != (readaccounting.Counts{ControlFileReads: 1}) {
			t.Fatalf("root control refusal = %+v, %v", counts, finishErr)
		}
	})

	t.Run("decoded member rejected later is charged", func(t *testing.T) {
		fixture := newKafkaReadAccountingFixture(t)
		path := filepath.Join(fixture.publication.directory, fixture.receipt.Name)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		raw = append(raw, ' ')
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		fixture.publication.rootValue.Members[fixture.receiptAt].ContentBytes = int64(len(raw))
		want := uint64(fixture.receipt.PostingCount)
		ctx, ledger, err := readaccounting.Start(
			t.Context(), readaccounting.Counts{ControlFileReads: 1, MemberVisits: want},
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.publication.ReadTopic(ctx, "producer", "orders-v1"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("noncanonical decoded member = %v", err)
		}
		counts, err := ledger.Finish()
		if err != nil || counts != (readaccounting.Counts{
			ControlFileReads: 1, MemberVisits: want,
		}) {
			t.Fatalf("rejected counts = %+v, %v", counts, err)
		}
	})

	t.Run("complete walk charges every member open and posting", func(t *testing.T) {
		fixture := newKafkaReadAccountingFixture(t)
		root := fixture.publication.Root()
		want := readaccounting.Counts{
			ControlFileReads: uint64(len(root.Members)),
			MemberVisits:     uint64(root.PostingCount),
		}
		ctx, ledger, err := readaccounting.Start(t.Context(), want)
		if err != nil {
			t.Fatal(err)
		}
		delivered := 0
		if err := fixture.publication.WalkPostings(ctx, func(Posting) error {
			delivered++
			return nil
		}); err != nil || delivered != root.PostingCount {
			t.Fatalf("complete walk = delivered %d, error %v", delivered, err)
		}
		if counts, err := ledger.Finish(); err != nil || counts != want {
			t.Fatalf("complete walk counts = %+v, %v", counts, err)
		}
	})

	t.Run("limit refuses before visitor delivery", func(t *testing.T) {
		fixture := newKafkaReadAccountingFixture(t)
		ctx, ledger, err := readaccounting.Start(t.Context(), readaccounting.Counts{
			ControlFileReads: uint64(len(fixture.publication.rootValue.Members)),
		})
		if err != nil {
			t.Fatal(err)
		}
		delivered := 0
		err = fixture.publication.WalkPostings(ctx, func(Posting) error {
			delivered++
			return nil
		})
		if !errors.Is(err, readaccounting.ErrLimit) || delivered != 0 {
			t.Fatalf("limited walk = delivered %d, error %v", delivered, err)
		}
		counts, finishErr := ledger.Finish()
		if !errors.Is(finishErr, readaccounting.ErrLimit) ||
			counts.ControlFileReads == 0 || counts.MemberVisits != 1 {
			t.Fatalf("limit counts = %+v, %v", counts, finishErr)
		}
	})

	t.Run("member control limit refuses before decode", func(t *testing.T) {
		fixture := newKafkaReadAccountingFixture(t)
		ctx, ledger, err := readaccounting.Start(t.Context(), readaccounting.Counts{})
		if err != nil {
			t.Fatal(err)
		}
		delivered := 0
		err = fixture.publication.WalkPostings(ctx, func(Posting) error {
			delivered++
			return nil
		})
		if !errors.Is(err, readaccounting.ErrLimit) || delivered != 0 {
			t.Fatalf("limited member open = delivered %d, error %v", delivered, err)
		}
		want := readaccounting.Counts{ControlFileReads: 1}
		if counts, err := ledger.Finish(); !errors.Is(err, readaccounting.ErrLimit) || counts != want {
			t.Fatalf("member control refusal = %+v, %v", counts, err)
		}
	})
}

func newKafkaReadAccountingFixture(t *testing.T) kafkaReadAccountingFixture {
	t.Helper()
	root := t.TempDir()
	observed := observationFixture(t, "cmd/kafka.go", kafkaFixture, []sourcepartition.Placement{{
		Path: "cmd/kafka.go", Mode: "100644", Revisions: []int{0},
	}})
	prepared, err := buildSource(t.Context(), root, fakeSource(t, []observedFixture{observed}))
	if err != nil {
		t.Fatal(err)
	}
	publication, err := prepared.Publish(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	bucket := partitionBucket("orders-v1")
	for index, receipt := range publication.rootValue.Members {
		if receipt.Plane == "producer" && receipt.Bucket == bucket {
			return kafkaReadAccountingFixture{
				root: root, publication: publication, receipt: receipt, receiptAt: index,
			}
		}
	}
	t.Fatal("missing Kafka read-accounting member")
	return kafkaReadAccountingFixture{}
}
