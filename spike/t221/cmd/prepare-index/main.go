// Command prepare-index authors the one-time T22.1 SCIP artifacts over the
// pinned corpora. The checked-in bytes are the reviewed input: gates only
// digest-verify and load them, they never invoke this command or an indexer
// (spike/t201 prepared-once-reviewed-and-copied-verbatim policy).
//
// Occurrence ranges are located in the real pinned corpus bytes, so the
// committed indexes carry genuine spans; the authoring circularity that
// remains (symbols are asserted, not produced by scip-go) is disclosed in
// the spike README.
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

	t221 "github.com/bmeddeb/phebs/spike/t221"
)

// Symbols asserted for the needles. Grammar is scip-go's: scheme, manager,
// module, version, then backticked-namespace / Type# / member descriptors.
const (
	cadenceWorkflowIDSymbol = "scip-go gomod github.com/uber/cadence 98f3d45 `.gen/go/shared`/WorkflowExecution#GetWorkflowId()."
	cadenceRunIDSymbol      = "scip-go gomod github.com/uber/cadence 98f3d45 `.gen/go/shared`/WorkflowExecution#GetRunId()."
	cadenceSuccessSymbol    = "scip-go gomod github.com/uber/cadence 98f3d45 `.gen/go/health`/Meta_Health_Result#GetSuccess()."
	cadenceIsSetSymbol      = "scip-go gomod github.com/uber/cadence 98f3d45 `.gen/go/health`/Meta_Health_Result#IsSetSuccess()."
	jaegerProcessSymbol     = "scip-go gomod github.com/uber/jaeger-client-go 8d8e8fc `thrift-gen/jaeger`/Batch#Process."
	jaegerSpansSymbol       = "scip-go gomod github.com/uber/jaeger-client-go 8d8e8fc `thrift-gen/jaeger`/Batch#Spans."
)

func main() {
	spikeDir := filepath.Clean(filepath.Join("spike", "t221"))
	if len(os.Args) == 2 {
		spikeDir = filepath.Clean(os.Args[1])
	}
	entries, err := t221.LoadCorpusLock(filepath.Join(spikeDir, "corpus.lock.json"))
	if err != nil {
		panic(err)
	}
	root := t221.CorpusRoot(spikeDir)
	dirs := make(map[string]string, len(entries))
	for _, entry := range entries {
		dir := filepath.Join(root, entry.Name)
		if err := t221.VerifyCorpusCheckout(dir, entry); err != nil {
			panic(fmt.Sprintf("corpus must be fetched and pinned before authoring: %v", err))
		}
		dirs[entry.Name] = dir
	}

	cadence := builder{corpusDir: dirs["cadence"]}
	cadenceDocs := []*scip.Document{
		cadence.document(".gen/go/shared/shared.go", []authoredOccurrence{
			// Good thriftrw definition: span covers exactly "GetWorkflowId".
			{needle: "func (v *WorkflowExecution) GetWorkflowId(", ident: "GetWorkflowId",
				symbol: cadenceWorkflowIDSymbol, roles: scip.SymbolRole_Definition | scip.SymbolRole_Generated},
		}),
		cadence.document(".gen/go/health/health.go", []authoredOccurrence{
			// Field 0 needle: the result-struct success accessor.
			{needle: "func (v *Meta_Health_Result) GetSuccess(", ident: "GetSuccess",
				symbol: cadenceSuccessSymbol, roles: scip.SymbolRole_Definition | scip.SymbolRole_Generated},
			// Adversarial: real identifier, span shifted one byte right —
			// exact byte-span equality must reject it.
			{needle: "func (v *Meta_Health_Result) IsSetSuccess(", ident: "IsSetSuccess", shift: 1,
				symbol: cadenceIsSetSymbol, roles: scip.SymbolRole_Definition | scip.SymbolRole_Generated},
		}),
		cadence.document("common/types/mapper/thrift/shared.go", []authoredOccurrence{
			// Handwritten read site joining the good definition.
			{needle: ".GetWorkflowId()", ident: "GetWorkflowId",
				symbol: cadenceWorkflowIDSymbol, roles: scip.SymbolRole_ReadAccess},
			// Adversarial: real accessor reference whose symbol has no
			// authored definition — must stay unbound.
			{needle: ".GetRunId()", ident: "GetRunId",
				symbol: cadenceRunIDSymbol, roles: scip.SymbolRole_ReadAccess},
		}),
	}
	writeIndex(spikeDir, "testdata/cadence.index.scip", cadenceDocs)

	jaeger := builder{corpusDir: dirs["jaeger-client-go"]}
	jaegerDocs := []*scip.Document{
		jaeger.document("thrift-gen/jaeger/jaeger.go", []authoredOccurrence{
			// Apache definition: the tagged struct field identifier.
			{needle: "Process *Process `thrift:", ident: "Process",
				symbol: jaegerProcessSymbol, roles: scip.SymbolRole_Definition | scip.SymbolRole_Generated},
		}),
		jaeger.document("transport_udp.go", []authoredOccurrence{
			// Handwritten composite-literal write site.
			{needle: "Process: s.process", ident: "Process",
				symbol: jaegerProcessSymbol, roles: scip.SymbolRole_WriteAccess},
			// Adversarial: a definition claimed inside a handwritten file —
			// document eligibility must reject it even though the span is real.
			{needle: "Spans:   s.spanBuffer", ident: "Spans",
				symbol: jaegerSpansSymbol, roles: scip.SymbolRole_Definition | scip.SymbolRole_Generated},
		}),
	}
	writeIndex(spikeDir, "testdata/jaeger.index.scip", jaegerDocs)
}

type authoredOccurrence struct {
	needle string
	ident  string
	shift  int
	symbol string
	roles  scip.SymbolRole
}

type builder struct {
	corpusDir string
}

// document locates each needle in the real pinned corpus file and emits an
// occurrence whose range covers the identifier inside that needle (plus any
// deliberate adversarial shift).
func (b builder) document(rel string, occurrences []authoredOccurrence) *scip.Document {
	content, err := os.ReadFile(filepath.Join(b.corpusDir, filepath.FromSlash(rel)))
	if err != nil {
		panic(err)
	}
	doc := &scip.Document{
		RelativePath:     rel,
		PositionEncoding: scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart,
	}
	text := string(content)
	for _, occurrence := range occurrences {
		needleOffset := strings.Index(text, occurrence.needle)
		if needleOffset < 0 {
			panic(fmt.Sprintf("%s: needle %q not found", rel, occurrence.needle))
		}
		identInNeedle := strings.Index(occurrence.needle, occurrence.ident)
		if identInNeedle < 0 {
			panic(fmt.Sprintf("identifier %q not inside needle %q", occurrence.ident, occurrence.needle))
		}
		offset := needleOffset + identInNeedle + occurrence.shift
		line := int32(strings.Count(text[:offset], "\n"))
		lineStart := strings.LastIndex(text[:offset], "\n") + 1
		character := int32(offset - lineStart)
		doc.Occurrences = append(doc.Occurrences, &scip.Occurrence{
			Range:       []int32{line, character, character + int32(len(occurrence.ident))},
			Symbol:      occurrence.symbol,
			SymbolRoles: int32(occurrence.roles),
		})
	}
	return doc
}

func writeIndex(spikeDir, name string, documents []*scip.Document) {
	index := &scip.Index{
		Metadata: &scip.Metadata{
			ToolInfo: &scip.ToolInfo{Name: "phebs-t221-authored-preparer", Version: "1"},
		},
		Documents: documents,
	}
	encoded, err := (proto.MarshalOptions{Deterministic: true}).Marshal(index)
	if err != nil {
		panic(err)
	}
	target := filepath.Join(spikeDir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(target, encoded, 0o644); err != nil {
		panic(err)
	}
	sum := sha256.Sum256(encoded)
	fmt.Printf("sha256:%s  %s\n", hex.EncodeToString(sum[:]), target)
}
