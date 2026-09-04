package resolvernamespace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/internal/readaccounting"
)

func TestOpenGenerationControlReadAccounting(t *testing.T) {
	root := t.TempDir()
	publication := buildPublication(
		t, root, "sha256:"+strings.Repeat("1", 64),
		[]gocaller.DirectDescriptor{descriptor(
			"grpc", "example.test/read-accounting", "ReaderClient", "Read",
			"reader.Service/Read", "read-accounting",
		)}, nil,
	)
	wantRoot := publication.Root()
	repository := wantRoot.Authority.Repository

	ctx, ledger, err := readaccounting.Start(
		t.Context(), readaccounting.Counts{ControlFileReads: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := OpenGeneration(
		ctx, root, repository, wantRoot.GenerationDigest, wantRoot.Digest,
	)
	if err != nil || opened.Root().Digest != wantRoot.Digest {
		t.Fatalf("opened resolver root = %+v, %v", opened, err)
	}
	want := readaccounting.Counts{ControlFileReads: 1}
	if counts, err := ledger.Finish(); err != nil || counts != want {
		t.Fatalf("resolver root controls = %+v, %v", counts, err)
	}
	if wantRoot.RecordCount != 1 {
		t.Fatalf("fixture records = %d", wantRoot.RecordCount)
	}

	ctx, ledger, err = readaccounting.Start(
		t.Context(), readaccounting.Counts{ControlFileReads: 2, MemberVisits: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	opened, err = OpenGeneration(
		ctx, root, repository, wantRoot.GenerationDigest, wantRoot.Digest,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := opened.ValidateComplete(ctx); err != nil {
		t.Fatal(err)
	}
	wantComplete := readaccounting.Counts{ControlFileReads: 2, MemberVisits: 1}
	if counts, err := ledger.Finish(); err != nil || counts != wantComplete {
		t.Fatalf("complete resolver reads = %+v, %v", counts, err)
	}

	ctx, ledger, err = readaccounting.Start(t.Context(), readaccounting.Counts{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenGeneration(
		ctx, root, repository, wantRoot.GenerationDigest, wantRoot.Digest,
	); !errors.Is(err, readaccounting.ErrLimit) {
		t.Fatalf("resolver root limit = %v", err)
	}
	if counts, err := ledger.Finish(); !errors.Is(err, readaccounting.ErrLimit) || counts != want {
		t.Fatalf("resolver root refusal = %+v, %v", counts, err)
	}

	ctx, ledger, err = readaccounting.Start(
		t.Context(), readaccounting.Counts{ControlFileReads: 1, MemberVisits: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	opened, err = OpenGeneration(
		ctx, root, repository, wantRoot.GenerationDigest, wantRoot.Digest,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := opened.ValidateComplete(ctx); !errors.Is(err, readaccounting.ErrLimit) {
		t.Fatalf("resolver member control limit = %v", err)
	}
	wantControlRefusal := readaccounting.Counts{ControlFileReads: 2}
	if counts, err := ledger.Finish(); !errors.Is(err, readaccounting.ErrLimit) || counts != wantControlRefusal {
		t.Fatalf("resolver member control refusal = %+v, %v", counts, err)
	}

	ctx, ledger, err = readaccounting.Start(
		t.Context(), readaccounting.Counts{ControlFileReads: 2},
	)
	if err != nil {
		t.Fatal(err)
	}
	opened, err = OpenGeneration(
		ctx, root, repository, wantRoot.GenerationDigest, wantRoot.Digest,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := opened.ValidateComplete(ctx); !errors.Is(err, readaccounting.ErrLimit) {
		t.Fatalf("resolver member visit limit = %v", err)
	}
	wantMemberRefusal := readaccounting.Counts{ControlFileReads: 2, MemberVisits: 1}
	if counts, err := ledger.Finish(); !errors.Is(err, readaccounting.ErrLimit) || counts != wantMemberRefusal {
		t.Fatalf("resolver member visit refusal = %+v, %v", counts, err)
	}

	if err := os.Remove(filepath.Join(opened.directory, wantRoot.Namespaces[len(wantRoot.Namespaces)-1].Member)); err != nil {
		t.Fatal(err)
	}
	if err := opened.ValidateComplete(t.Context()); err == nil {
		t.Fatal("missing final resolver member was accepted")
	}
}
