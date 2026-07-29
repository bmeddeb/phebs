package codenav

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"
)

func TestQuerySourceBudgetIsAggregate(t *testing.T) {
	fixture := newFixture(t, true)
	service := New(Options{
		DataDir: fixture.dataDir,
		// The query file is 53 bytes and the reference file is 52 bytes.
		// Each is below the per-file limit but together exceed this budget.
		MaxQuerySourceBytes: 80,
	})
	_, err := service.References(context.Background(), Query{
		Repo: fixtureRepo, Revision: fixture.revision, Path: "lib/rocket.go",
		Line: 1, Character: 6,
	})
	if !errors.Is(err, ErrQuerySourceBudget) {
		t.Fatalf("References error = %v, want ErrQuerySourceBudget", err)
	}
}

func TestSnapshotCacheLRUEvictionAndBudget(t *testing.T) {
	service := New(Options{DataDir: t.TempDir(), MaxCacheBytes: 800})
	revision := strings.Repeat("a", 40)
	entry := func() cacheEntry {
		return cacheEntry{revision: revision, index: &snapshot{estimatedBytes: 100}}
	}
	if err := service.storeEntry("a", entry()); err != nil {
		t.Fatal(err)
	}
	if err := service.storeEntry("b", entry()); err != nil {
		t.Fatal(err)
	}
	if _, ok := service.loadEntry("a", revision); !ok {
		t.Fatal("cache lost a before it became most recently used")
	}
	if err := service.storeEntry("c", entry()); err != nil {
		t.Fatal(err)
	}
	if _, ok := service.loadEntry("b", revision); ok {
		t.Fatal("least recently used entry b was not evicted")
	}
	if _, ok := service.loadEntry("a", revision); !ok {
		t.Fatal("most recently used entry a was evicted")
	}
	if _, ok := service.loadEntry("c", revision); !ok {
		t.Fatal("new entry c was evicted")
	}
	service.mu.Lock()
	cacheBytes := service.cacheBytes
	service.mu.Unlock()
	if cacheBytes > service.maxCacheBytes {
		t.Fatalf("cache uses %d bytes, limit %d", cacheBytes, service.maxCacheBytes)
	}

	if err := service.storeEntry("oversized", cacheEntry{
		revision: revision, index: &snapshot{estimatedBytes: 801},
	}); !errors.Is(err, ErrCacheBudget) {
		t.Fatalf("oversized snapshot error = %v, want ErrCacheBudget", err)
	}
}

func TestParseSemanticLimits(t *testing.T) {
	data := marshalFixtureIndex(t, readFixtureIndex(t))
	for _, test := range []struct {
		name    string
		options Options
	}{
		{name: "documents", options: Options{MaxDocuments: 1}},
		{name: "occurrences", options: Options{MaxOccurrences: 1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseSnapshot(context.Background(), data, newParseLimits(test.options))
			if !errors.Is(err, ErrSemanticLimit) {
				t.Fatalf("parse error = %v, want ErrSemanticLimit", err)
			}
		})
	}
}

func TestUnsupportedEncodingDocumentStillConsumesSemanticLimits(t *testing.T) {
	index := &scip.Index{
		Metadata: &scip.Metadata{},
		Documents: []*scip.Document{{
			RelativePath:     "bad.go",
			PositionEncoding: scip.PositionEncoding_UnspecifiedPositionEncoding,
			Occurrences: []*scip.Occurrence{
				{Range: []int32{0, 0, 1}, Symbol: "local 1"},
				{Range: []int32{0, 1, 2}, Symbol: "local 2"},
			},
		}},
	}
	_, err := parseSnapshot(
		context.Background(),
		marshalFixtureIndex(t, index),
		newParseLimits(Options{MaxOccurrences: 1}),
	)
	if !errors.Is(err, ErrSemanticLimit) {
		t.Fatalf("parse error = %v, want ErrSemanticLimit", err)
	}
}

func TestUnsupportedEncodingDocumentStillValidatesBoundedPayloads(t *testing.T) {
	tests := []struct {
		name    string
		options Options
		mutate  func(*scip.Document)
		want    error
	}{
		{
			name:    "occurrence symbol bytes",
			options: Options{MaxSymbolBytes: 16},
			mutate: func(doc *scip.Document) {
				doc.Occurrences = []*scip.Occurrence{{
					Symbol: "local " + strings.Repeat("x", 32),
				}}
			},
			want: ErrSemanticLimit,
		},
		{
			name:    "occurrence hover bytes",
			options: Options{MaxHoverBytes: 16},
			mutate: func(doc *scip.Document) {
				doc.Occurrences = []*scip.Occurrence{{
					Symbol:                "local 1",
					OverrideDocumentation: []string{strings.Repeat("x", 32)},
				}}
			},
			want: ErrHoverTooLarge,
		},
		{
			name:    "symbol hover bytes",
			options: Options{MaxHoverBytes: 16},
			mutate: func(doc *scip.Document) {
				doc.Symbols = []*scip.SymbolInformation{{
					Symbol:        "local 1",
					Documentation: []string{strings.Repeat("x", 32)},
				}}
			},
			want: ErrHoverTooLarge,
		},
		{
			name:    "relationship symbol bytes",
			options: Options{MaxSymbolBytes: 16},
			mutate: func(doc *scip.Document) {
				doc.Symbols = []*scip.SymbolInformation{{
					Symbol: "local 1",
					Relationships: []*scip.Relationship{{
						Symbol: "local " + strings.Repeat("x", 32),
					}},
				}}
			},
			want: ErrSemanticLimit,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			doc := &scip.Document{
				RelativePath:     "bad.go",
				PositionEncoding: scip.PositionEncoding_UnspecifiedPositionEncoding,
			}
			test.mutate(doc)
			index := &scip.Index{
				Metadata:  &scip.Metadata{},
				Documents: []*scip.Document{doc},
			}
			_, err := parseSnapshot(
				context.Background(),
				marshalFixtureIndex(t, index),
				newParseLimits(test.options),
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("parse error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestParseRejectsHoverAmplificationAndOversizedSymbols(t *testing.T) {
	t.Run("merged duplicate documentation", func(t *testing.T) {
		index := readFixtureIndex(t)
		doc := index.GetDocuments()[0]
		symbol := doc.GetSymbols()[0].GetSymbol()
		doc.Symbols = []*scip.SymbolInformation{
			{Symbol: symbol, Documentation: []string{strings.Repeat("a", 40)}},
			{Symbol: symbol, Documentation: []string{strings.Repeat("b", 40)}},
		}
		_, err := parseSnapshot(context.Background(), marshalFixtureIndex(t, index), newParseLimits(Options{MaxHoverBytes: 64}))
		if !errors.Is(err, ErrHoverTooLarge) {
			t.Fatalf("parse error = %v, want ErrHoverTooLarge", err)
		}
	})

	t.Run("display name", func(t *testing.T) {
		index := readFixtureIndex(t)
		info := index.GetDocuments()[0].GetSymbols()[0]
		info.Documentation = nil
		info.SignatureDocumentation = nil
		info.DisplayName = strings.Repeat("n", 65)
		_, err := parseSnapshot(context.Background(), marshalFixtureIndex(t, index), newParseLimits(Options{MaxHoverBytes: 64}))
		if !errors.Is(err, ErrHoverTooLarge) {
			t.Fatalf("parse error = %v, want ErrHoverTooLarge", err)
		}
	})

	t.Run("occurrence override", func(t *testing.T) {
		index := readFixtureIndex(t)
		index.GetDocuments()[0].GetOccurrences()[0].OverrideDocumentation = []string{strings.Repeat("d", 65)}
		_, err := parseSnapshot(context.Background(), marshalFixtureIndex(t, index), newParseLimits(Options{MaxHoverBytes: 64}))
		if !errors.Is(err, ErrHoverTooLarge) {
			t.Fatalf("parse error = %v, want ErrHoverTooLarge", err)
		}
	})

	t.Run("occurrence symbol before parse", func(t *testing.T) {
		index := readFixtureIndex(t)
		index.GetDocuments()[0].GetOccurrences()[0].Symbol = "local " + strings.Repeat("x", 32)
		_, err := parseSnapshot(context.Background(), marshalFixtureIndex(t, index), newParseLimits(Options{MaxSymbolBytes: 16}))
		if !errors.Is(err, ErrSemanticLimit) {
			t.Fatalf("parse error = %v, want ErrSemanticLimit", err)
		}
	})

	t.Run("combined symbol and override response", func(t *testing.T) {
		err := validateHoverResponse(&HoverInfo{
			Signature:     strings.Repeat("s", 40),
			Documentation: []string{strings.Repeat("d", 40)},
		}, newParseLimits(Options{MaxHoverBytes: 64}))
		if !errors.Is(err, ErrHoverTooLarge) {
			t.Fatalf("hover response error = %v, want ErrHoverTooLarge", err)
		}
	})

	t.Run("combined response through service", func(t *testing.T) {
		fixture := newFixture(t, true)
		index := readFixtureIndex(t)
		info := index.GetDocuments()[0].GetSymbols()[0]
		info.DisplayName = ""
		info.Documentation = nil
		info.SignatureDocumentation.Text = strings.Repeat("s", 40)
		index.GetDocuments()[1].GetOccurrences()[0].OverrideDocumentation = []string{strings.Repeat("d", 40)}
		writeIndex(t, fixture.origin, index)
		revision := fixture.commit(t, "combined hover")
		fixture.fetch(t)
		service := New(Options{DataDir: fixture.dataDir, MaxHoverBytes: 64})
		_, err := service.Hover(context.Background(), Query{
			Repo: fixtureRepo, Revision: revision, Path: "use/use.go",
			Line: 1, Character: 29,
		})
		if !errors.Is(err, ErrHoverTooLarge) {
			t.Fatalf("Hover error = %v, want ErrHoverTooLarge", err)
		}
	})
}

func TestMissingFullRevisionHasSentinel(t *testing.T) {
	fixture := newFixture(t, true)
	service := New(Options{DataDir: fixture.dataDir})
	_, err := service.Definition(context.Background(), Query{
		Repo: fixtureRepo, Revision: strings.Repeat("f", 40), Path: "lib/rocket.go",
		Line: 1, Character: 6,
	})
	if !errors.Is(err, ErrRevisionNotFound) {
		t.Fatalf("Definition error = %v, want ErrRevisionNotFound", err)
	}
}

func marshalFixtureIndex(t *testing.T, index *scip.Index) []byte {
	t.Helper()
	data, err := proto.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
