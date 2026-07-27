// Command author creates the reviewed T22.5 synthetic field-zero SCIP input.
//
// The committed index is the demo input. Normal tests and make dev never run
// this command: they verify and consume the authored bytes. The symbols are
// deliberately asserted rather than produced by a real indexer, following the
// prepared-once policy recorded by T20.1 and T22.1.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"
)

const symbol = "scip-go gomod example.invalid/t225-thrift-field-demo v0.0.0 demo/Meta_Health_Result#GetSuccess()."

func main() {
	root := filepath.Join("docs", "fixtures", "thrift-field", "repo")
	if len(os.Args) == 2 {
		root = filepath.Clean(os.Args[1])
	}
	generated := mustRead(root, "generated/health.go")
	consumer := mustRead(root, "consumer/use.go")
	index := &scip.Index{
		Metadata: &scip.Metadata{
			ToolInfo:             &scip.ToolInfo{Name: "phebs-t225-authored-preparer", Version: "1"},
			ProjectRoot:          "file:///fixture/t225-thrift-field-demo",
			TextDocumentEncoding: scip.TextEncoding_UTF8,
		},
		Documents: []*scip.Document{
			document("generated/health.go", generated, "GetSuccess", scip.SymbolRole_Definition|scip.SymbolRole_Generated),
			document("consumer/use.go", consumer, "GetSuccess", scip.SymbolRole_ReadAccess),
		},
	}
	encoded, err := (proto.MarshalOptions{Deterministic: true}).Marshal(index)
	if err != nil {
		panic(err)
	}
	target := filepath.Join(root, "index.scip")
	if err := os.WriteFile(target, encoded, 0o644); err != nil {
		panic(err)
	}
	sum := sha256.Sum256(encoded)
	fmt.Printf("sha256:%s  %s\n", hex.EncodeToString(sum[:]), target)
}

func mustRead(root, name string) string {
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		panic(err)
	}
	return string(content)
}

func document(path, content, identifier string, roles scip.SymbolRole) *scip.Document {
	offset := strings.Index(content, identifier)
	if offset < 0 {
		panic(fmt.Sprintf("%s: identifier %q not found", path, identifier))
	}
	line := int32(strings.Count(content[:offset], "\n"))
	lineStart := strings.LastIndex(content[:offset], "\n") + 1
	character := int32(offset - lineStart)
	return &scip.Document{
		RelativePath:     path,
		PositionEncoding: scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart,
		Occurrences: []*scip.Occurrence{{
			Range:       []int32{line, character, character + int32(len(identifier))},
			Symbol:      symbol,
			SymbolRoles: int32(roles),
		}},
	}
}
