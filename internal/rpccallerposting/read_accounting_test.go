package rpccallerposting

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/sourcepartition"
)

type rpcReadAccountingFixture struct {
	root        string
	operation   string
	publication *Publication
	receipt     MemberReceipt
	receiptAt   int
}

func TestMemberVisitAccounting(t *testing.T) {
	t.Run("valid rereads charge one root control and members", func(t *testing.T) {
		fixture := newRPCReadAccountingFixture(t)
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
			values, readErr := opened.ReadOperation(ctx, "grpc", fixture.operation)
			if readErr != nil || len(values) != 1 {
				t.Fatalf("operation reread = %d, %v", len(values), readErr)
			}
		}
		counts, err := ledger.Finish()
		expected := readaccounting.Counts{ControlFileReads: 3, MemberVisits: want}
		if err != nil || counts != expected {
			t.Fatalf("reread counts = %+v, %v", counts, err)
		}
	})

	t.Run("root control limit refuses before members", func(t *testing.T) {
		fixture := newRPCReadAccountingFixture(t)
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
		fixture := newRPCReadAccountingFixture(t)
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
		if _, err := fixture.publication.ReadOperation(ctx, "grpc", fixture.operation); !errors.Is(err, ErrInvalid) {
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
		fixture := newRPCReadAccountingFixture(t)
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
		fixture := newRPCReadAccountingFixture(t)
		limit := uint64(fixture.receipt.PostingCount - 1)
		ctx, ledger, err := readaccounting.Start(
			t.Context(), readaccounting.Counts{
				ControlFileReads: uint64(len(fixture.publication.rootValue.Members)),
				MemberVisits:     limit,
			},
		)
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
		want := limit + 1
		if !errors.Is(finishErr, readaccounting.ErrLimit) ||
			counts.ControlFileReads == 0 || counts.MemberVisits != want {
			t.Fatalf("limit counts = %+v, %v; want %d", counts, finishErr, want)
		}
	})

	t.Run("member control limit refuses before decode", func(t *testing.T) {
		fixture := newRPCReadAccountingFixture(t)
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

func newRPCReadAccountingFixture(t *testing.T) rpcReadAccountingFixture {
	t.Helper()
	root := t.TempDir()
	operation := "payments.v1.Payments/Charge"
	descriptor := grpcDescriptor(
		"example.test/payments/v1", "PaymentsClient", "Charge", operation, "read-accounting",
	)
	resolver := resolverPublication(t, root, []gocaller.DirectDescriptor{descriptor})
	source := `package app
import pb "example.test/payments/v1"
func charge(client pb.PaymentsClient) { client.Charge(nil) }
`
	observed := observationFixture(t, "cmd/charge.go", source, []sourcepartition.Placement{{
		Path: "cmd/charge.go", Mode: "100644", Revisions: []int{0},
	}})
	prepared, err := buildSources(
		t.Context(), root, fakeSource(t, []observedFixture{observed}), resolver,
	)
	if err != nil {
		t.Fatal(err)
	}
	publication, err := prepared.Publish(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	bucket := partitionBucket(operation)
	for index, receipt := range publication.rootValue.Members {
		if receipt.Protocol == "grpc" && receipt.Bucket == bucket {
			return rpcReadAccountingFixture{
				root: root, operation: operation, publication: publication,
				receipt: receipt, receiptAt: index,
			}
		}
	}
	t.Fatal("missing RPC read-accounting member")
	return rpcReadAccountingFixture{}
}
