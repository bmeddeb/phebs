// Command prepare-index creates the one-time T20.1 synthetic SCIP artifact.
//
// The checked-in bytes are the reviewed input. Corpus generation and tests
// only validate and copy that artifact; they never invoke this command or an
// indexer.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"

	"github.com/bmeddeb/phebs/spike/t201"
)

func main() {
	root := filepath.Clean(filepath.Join("spike", "t201"))
	if len(os.Args) == 2 {
		root = filepath.Clean(os.Args[1])
	}
	files := t201.SmallSourceFiles()
	smallDocuments := []*scip.Document{
		document(files, "gen/proto/orders/v1/orders_grpc.pb.go", []indexedOccurrence{
			{needle: "Get(ctx", symbol: t201.ProtoGetSymbol, roles: scip.SymbolRole_Definition | scip.SymbolRole_Generated},
			{needle: "Watch(ctx", symbol: t201.ProtoWatchSymbol, roles: scip.SymbolRole_Definition | scip.SymbolRole_Generated},
		}),
		document(files, "gen/thrift/ledger/ledger.go", []indexedOccurrence{
			{needle: "Get(ctx", symbol: t201.ThriftGetSymbol, roles: scip.SymbolRole_Definition | scip.SymbolRole_Generated},
		}),
		document(files, "src/checkout/caller.go", []indexedOccurrence{
			{needle: "Get(ctx", symbol: t201.ProtoGetSymbol, roles: scip.SymbolRole_ReadAccess},
		}),
		document(files, "src/alias/caller.go", []indexedOccurrence{
			{needle: "Get(ctx", symbol: t201.ProtoGetSymbol, roles: scip.SymbolRole_ReadAccess},
		}),
		document(files, "src/embedded/caller.go", []indexedOccurrence{
			{needle: "Get(ctx", symbol: t201.ProtoGetSymbol, roles: scip.SymbolRole_ReadAccess},
		}),
		document(files, "src/wrapper/client.go", []indexedOccurrence{
			{needle: "Get(ctx", symbol: t201.ProtoGetSymbol, roles: scip.SymbolRole_ReadAccess},
		}),
		document(files, "src/thrift/caller.go", []indexedOccurrence{
			{needle: "Get(ctx", symbol: t201.ThriftGetSymbol, roles: scip.SymbolRole_ReadAccess},
		}),
	}
	slices.SortFunc(smallDocuments, func(a, b *scip.Document) int {
		return strings.Compare(a.RelativePath, b.RelativePath)
	})
	writeIndex(root, "testdata/index.scip", smallDocuments)

	scaleDocuments := append([]*scip.Document(nil), smallDocuments...)
	for fileIndex := range t201.ScaleCallFiles {
		relPath, content := t201.ScaleCallerFile(fileIndex)
		occurrences := make([]indexedOccurrence, 0, t201.ScaleCallsPerFile)
		for callIndex := range t201.ScaleCallsPerFile {
			occurrences = append(occurrences, indexedOccurrence{
				needle: "client.Get", occurrence: callIndex,
				symbol: t201.ProtoGetSymbol, roles: scip.SymbolRole_ReadAccess,
			})
		}
		scaleDocuments = append(scaleDocuments, documentContent(relPath, content, occurrences))
	}
	slices.SortFunc(scaleDocuments, func(a, b *scip.Document) int {
		return strings.Compare(a.RelativePath, b.RelativePath)
	})
	writeIndex(root, "testdata/scale.index.scip", scaleDocuments)
}

func writeIndex(root, name string, documents []*scip.Document) {
	index := &scip.Index{
		Metadata: &scip.Metadata{
			ToolInfo: &scip.ToolInfo{Name: "phebs-t20-neutral-preparer", Version: "1"},
		},
		Documents: documents,
	}
	encoded, err := (proto.MarshalOptions{Deterministic: true}).Marshal(index)
	if err != nil {
		panic(err)
	}
	target := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(target, encoded, 0o644); err != nil {
		panic(err)
	}
	sum := sha256.Sum256(encoded)
	fmt.Printf("sha256:%s  %s\n", hex.EncodeToString(sum[:]), target)
}

type indexedOccurrence struct {
	needle     string
	occurrence int
	symbol     string
	roles      scip.SymbolRole
}

func document(files map[string][]byte, path string, occurrences []indexedOccurrence) *scip.Document {
	content, ok := files[path]
	if !ok {
		panic("missing template " + path)
	}
	return documentContent(path, content, occurrences)
}

func documentContent(path string, content []byte, occurrences []indexedOccurrence) *scip.Document {
	out := &scip.Document{
		RelativePath:     path,
		PositionEncoding: scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart,
	}
	for _, occurrence := range occurrences {
		out.Occurrences = append(out.Occurrences, &scip.Occurrence{
			Range:       sourceRange(string(content), occurrence.needle, occurrence.occurrence),
			Symbol:      occurrence.symbol,
			SymbolRoles: int32(occurrence.roles),
		})
	}
	return out
}

func sourceRange(content, needle string, occurrence int) []int32 {
	offset := -1
	from := 0
	for range occurrence + 1 {
		next := strings.Index(content[from:], needle)
		if next < 0 {
			panic(fmt.Sprintf("%q occurrence %d not found", needle, occurrence))
		}
		offset = from + next
		from = offset + len(needle)
	}
	line := int32(strings.Count(content[:offset], "\n"))
	lineStart := strings.LastIndex(content[:offset], "\n") + 1
	character := int32(offset - lineStart)
	return []int32{line, character, character + int32(len(strings.TrimSuffix(needle, "(ctx")))}
}
