package codenav

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"github.com/scip-code/scip/bindings/go/scip"
)

func TestMalformedOccurrenceDoesNotRejectSnapshot(t *testing.T) {
	index := readFixtureIndex(t)
	doc := index.GetDocuments()[0]
	originalCount := len(doc.GetOccurrences())
	doc.Occurrences = append(doc.Occurrences, &scip.Occurrence{
		Symbol: doc.GetOccurrences()[0].GetSymbol(),
	})

	snapshot, err := parseSnapshot(
		context.Background(),
		marshalFixtureIndex(t, index),
		newParseLimits(Options{}),
	)
	if err != nil {
		t.Fatalf("parseSnapshot: %v", err)
	}
	if got := len(snapshot.documents[doc.GetRelativePath()].occurrences); got != originalCount {
		t.Fatalf("retained occurrences = %d, want %d", got, originalCount)
	}
	if got, want := snapshot.occurrenceCount, originalCount+len(index.GetDocuments()[1].GetOccurrences())+1; got != want {
		t.Fatalf("budgeted occurrence count = %d, want %d", got, want)
	}
}

func TestUnsupportedDocumentEncodingDoesNotRejectSnapshot(t *testing.T) {
	index := readFixtureIndex(t)
	validOccurrenceCount := 0
	for _, doc := range index.GetDocuments() {
		validOccurrenceCount += len(doc.GetOccurrences())
	}
	index.Documents = append([]*scip.Document{{
		RelativePath:     "bad/unknown.go",
		PositionEncoding: scip.PositionEncoding_UnspecifiedPositionEncoding,
		Occurrences: []*scip.Occurrence{{
			Range:  []int32{0, 0, 1},
			Symbol: index.GetDocuments()[0].GetOccurrences()[0].GetSymbol(),
		}},
	}}, index.Documents...)

	snapshot, err := parseSnapshot(
		context.Background(),
		marshalFixtureIndex(t, index),
		newParseLimits(Options{}),
	)
	if err != nil {
		t.Fatalf("parseSnapshot: %v", err)
	}
	if snapshot.unsupportedDocuments != 1 {
		t.Fatalf("unsupported documents = %d, want 1", snapshot.unsupportedDocuments)
	}
	if _, retained := snapshot.documents["bad/unknown.go"]; retained {
		t.Fatal("unsupported document entered the navigation snapshot")
	}
	if len(snapshot.documents) != len(index.GetDocuments())-1 {
		t.Fatalf("retained documents = %d, want %d", len(snapshot.documents), len(index.GetDocuments())-1)
	}
	if snapshot.occurrenceCount != validOccurrenceCount+1 {
		t.Fatalf("budgeted occurrences = %d, want %d", snapshot.occurrenceCount, validOccurrenceCount+1)
	}
}

func TestQueriesSkipInvalidResultRanges(t *testing.T) {
	fixture := newFixture(t, true)
	index := readFixtureIndex(t)
	symbol := index.GetDocuments()[0].GetOccurrences()[0].GetSymbol()

	// The definition spans the entire file and ends on its final line. The
	// source intentionally has no trailing newline.
	index.GetDocuments()[0].GetOccurrences()[0].SetSourceRange(scip.Range{
		Start: scip.Position{Line: 0, Character: 0},
		End:   scip.Position{Line: 3, Character: 1},
	})
	invalidReference := &scip.Occurrence{Symbol: symbol}
	invalidReference.SetSourceRange(scip.Range{
		Start: scip.Position{Line: 0, Character: 0},
		End:   scip.Position{Line: 0, Character: 99},
	})
	invalidDefinition := &scip.Occurrence{
		Symbol: symbol, SymbolRoles: int32(scip.SymbolRole_Definition),
	}
	invalidDefinition.SetSourceRange(scip.Range{
		Start: scip.Position{Line: 0, Character: 0},
		End:   scip.Position{Line: 0, Character: 100},
	})
	index.Documents = append(index.Documents, &scip.Document{
		RelativePath:     "aaa.go",
		PositionEncoding: scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart,
		Occurrences:      []*scip.Occurrence{invalidReference, invalidDefinition},
	})
	if err := os.WriteFile(
		filepath.Join(fixture.origin, "lib/rocket.go"),
		[]byte("package lib\nfunc Rocket() string {\n\treturn \"ready\"\n}"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.origin, "aaa.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeIndex(t, fixture.origin, index)
	revision := fixture.commit(t, "stale SCIP ranges")
	fixture.fetch(t)

	service := New(Options{DataDir: fixture.dataDir})
	definition, err := service.Definition(context.Background(), Query{
		Repo: fixtureRepo, Revision: revision, Path: "use/use.go",
		Line: 1, Character: 29,
	})
	if err != nil {
		t.Fatalf("Definition: %v", err)
	}
	if definition.Location == nil || definition.Location.Path != "lib/rocket.go" {
		t.Fatalf("Definition = %#v, want valid lib/rocket.go location", definition)
	}
	if got, want := definition.Location.Range.End, (Position{Line: 3, Character: 1}); got != want {
		t.Fatalf("multiline definition end = %#v, want %#v", got, want)
	}

	references, err := service.References(context.Background(), Query{
		Repo: fixtureRepo, Revision: revision, Path: "lib/rocket.go",
		Line: 1, Character: 6,
	})
	if err != nil {
		t.Fatalf("References: %v", err)
	}
	if len(references.Locations) != 1 || references.Locations[0].Path != "use/use.go" {
		t.Fatalf("References = %#v, want only valid use/use.go location", references)
	}
}

func TestReferenceRelationshipsAreDirectAndBidirectional(t *testing.T) {
	snapshot := &snapshot{
		referenceTargets: make(map[string]map[string]struct{}),
		referenceSources: make(map[string]map[string]struct{}),
		symbols:          make(map[string]*symbolInfo),
	}
	const edgeCount = 10_000
	for i := 0; i < edgeCount; i++ {
		snapshot.addReferenceRelationship("symbol"+strconv.Itoa(i), "symbol"+strconv.Itoa(i+1))
	}

	if got, want := snapshot.relatedReferenceSymbols("symbol0"), []string{"symbol0", "symbol1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("endpoint references = %v, want %v", got, want)
	}
	if got, want := snapshot.relatedReferenceSymbols("symbol1"), []string{"symbol0", "symbol1", "symbol2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("inverse references = %v, want %v", got, want)
	}

	snapshot.symbols["symbol0"] = &symbolInfo{definitionSymbols: []string{"symbol1"}}
	snapshot.symbols["symbol1"] = &symbolInfo{definitionSymbols: []string{"symbol2"}}
	if got, want := snapshot.relatedDefinitionSymbols("symbol0"), []string{"symbol0", "symbol1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("definition targets = %v, want %v", got, want)
	}
}

func TestMalformedIndexFailureIsNegativelyCached(t *testing.T) {
	fixture := newFixture(t, false)
	if err := os.WriteFile(filepath.Join(fixture.origin, DefaultIndexPath), []byte{0xff, 0xff, 0xff}, 0o644); err != nil {
		t.Fatal(err)
	}
	revision := fixture.commit(t, "malformed SCIP index")
	fixture.fetch(t)

	service := New(Options{DataDir: fixture.dataDir})
	query := Query{
		Repo: fixtureRepo, Revision: revision, Path: "lib/rocket.go",
		Line: 1, Character: 6,
	}
	_, firstErr := service.Definition(context.Background(), query)
	if firstErr == nil {
		t.Fatal("Definition accepted malformed index")
	}
	entry, ok := service.loadEntry(fixtureRepo, revision)
	if !ok || entry.loadErr == nil || entry.index != nil {
		t.Fatalf("negative cache entry = %#v, found %t", entry, ok)
	}

	// A cache miss would now observe an absent index. The identical parse error
	// proves the immutable revision's failure was served from the LRU entry.
	service.indexPath = "missing.scip"
	_, secondErr := service.Definition(context.Background(), query)
	if secondErr == nil || secondErr.Error() != firstErr.Error() {
		t.Fatalf("cached error = %v, want %v", secondErr, firstErr)
	}
}
