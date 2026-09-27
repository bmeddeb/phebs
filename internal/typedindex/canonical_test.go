package typedindex

import (
	"bytes"
	"context"
	"errors"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

//nolint:staticcheck // Match the legacy range representation emitted by the pinned scip-go producer.
func canonicalFixture() *scip.Index {
	return &scip.Index{Metadata: &scip.Metadata{ToolInfo: &scip.ToolInfo{Name: "scip-go", Version: "0.2.7", Arguments: []string{"index", "--skip-tests"}}, ProjectRoot: "file:///workspace", TextDocumentEncoding: scip.TextEncoding_UTF8}, Documents: []*scip.Document{
		{RelativePath: "a.go", Language: "go", Occurrences: []*scip.Occurrence{{Range: []int32{0, 0, 1}, Symbol: "local 1", SymbolRoles: 1, OverrideDocumentation: []string{"first", "second"}}, {Range: []int32{1, 0, 1}, Symbol: "local 2"}}, Symbols: []*scip.SymbolInformation{{Symbol: "local 1", Documentation: []string{"first", "second"}, Relationships: []*scip.Relationship{{Symbol: "local 2", IsReference: true}, {Symbol: "local 3", IsImplementation: true}}}, {Symbol: "local 2"}}},
		{RelativePath: "b.go", PositionEncoding: scip.PositionEncoding_UTF16CodeUnitOffsetFromLineStart, Occurrences: []*scip.Occurrence{{Range: []int32{0, 0, 1}, Symbol: "local 1"}}},
	}, ExternalSymbols: []*scip.SymbolInformation{{Symbol: "scip-go gomod example.com v1 Ext#"}, {Symbol: "scip-go gomod example.com v1 Other#"}}}
}
func canonicalWire(t testing.TB, index *scip.Index) []byte {
	t.Helper()
	b, err := proto.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestCanonicalSCIPPermutations(t *testing.T) {
	ctx := context.Background()
	input := canonicalFixture()
	original := canonicalWire(t, input)
	expected, err := CanonicalSCIP(ctx, original)
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewPCG(45, 3))
	for n := 0; n < 100; n++ {
		index := proto.Clone(input).(*scip.Index)
		rng.Shuffle(len(index.Documents), func(i, j int) { index.Documents[i], index.Documents[j] = index.Documents[j], index.Documents[i] })
		slices.Reverse(index.ExternalSymbols)
		for _, d := range index.Documents {
			rng.Shuffle(len(d.Occurrences), func(i, j int) { d.Occurrences[i], d.Occurrences[j] = d.Occurrences[j], d.Occurrences[i] })
			rng.Shuffle(len(d.Symbols), func(i, j int) { d.Symbols[i], d.Symbols[j] = d.Symbols[j], d.Symbols[i] })
			for _, s := range d.Symbols {
				rng.Shuffle(len(s.Relationships), func(i, j int) { s.Relationships[i], s.Relationships[j] = s.Relationships[j], s.Relationships[i] })
			}
		}
		raw := canonicalWire(t, index)
		before := bytes.Clone(raw)
		actual, err := CanonicalSCIP(ctx, raw)
		if err != nil || !bytes.Equal(expected, actual) {
			t.Fatalf("permutation %d: %v", n, err)
		}
		if !bytes.Equal(before, raw) {
			t.Fatal("mutated caller bytes")
		}
		again, err := CanonicalSCIP(ctx, actual)
		if err != nil || !bytes.Equal(again, actual) {
			t.Fatal("not idempotent", err)
		}
	}
	if !bytes.Equal(canonicalWire(t, input), original) {
		t.Fatal("mutated fixture")
	}
	var decoded scip.Index
	if err := proto.Unmarshal(expected, &decoded); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(decoded.Metadata.ToolInfo.Arguments, input.Metadata.ToolInfo.Arguments) || !slices.Equal(decoded.Documents[0].Symbols[0].Documentation, []string{"first", "second"}) || !slices.Equal(decoded.Documents[0].Occurrences[0].OverrideDocumentation, []string{"first", "second"}) {
		t.Fatal("ordered fields changed")
	}
	// Reordering semantic-order lists must remain distinguishable.
	input.Metadata.ToolInfo.Arguments = []string{"--skip-tests", "index"}
	altered, err := CanonicalSCIP(ctx, canonicalWire(t, input))
	if err != nil || bytes.Equal(altered, expected) {
		t.Fatal("arguments lost ordering", err)
	}
}

//nolint:staticcheck // Pinned scip-go emits legacy ranges; corruption of that wire representation must be tested.
func TestCanonicalSCIPRejects(t *testing.T) {
	tests := []struct {
		name string
		edit func(*scip.Index)
		want error
	}{
		{"missing metadata", func(i *scip.Index) { i.Metadata = nil }, Invalid},
		{"missing producer", func(i *scip.Index) { i.Metadata.ToolInfo = nil }, Invalid},
		{"relative root", func(i *scip.Index) { i.Metadata.ProjectRoot = "workspace" }, Invalid},
		{"network root", func(i *scip.Index) { i.Metadata.ProjectRoot = "file://host/workspace" }, Invalid},
		{"parent path", func(i *scip.Index) { i.Documents[0].RelativePath = "../a.go" }, Invalid},
		{"absolute path", func(i *scip.Index) { i.Documents[0].RelativePath = "/a.go" }, Invalid},
		{"noncanonical path", func(i *scip.Index) { i.Documents[0].RelativePath = "x/../a.go" }, Invalid},
		{"windows path", func(i *scip.Index) { i.Documents[0].RelativePath = "a\\b.go" }, Invalid},
		{"control path", func(i *scip.Index) { i.Documents[0].RelativePath = "a\x00.go" }, Invalid},
		{"duplicate document", func(i *scip.Index) { i.Documents = append(i.Documents, proto.Clone(i.Documents[0]).(*scip.Document)) }, Invalid},
		{"conflicting symbol", func(i *scip.Index) {
			s := proto.Clone(i.Documents[0].Symbols[0]).(*scip.SymbolInformation)
			s.Documentation = []string{"different"}
			i.Documents[0].Symbols = append(i.Documents[0].Symbols, s)
		}, Invalid},
		{"conflicting occurrence", func(i *scip.Index) {
			o := proto.Clone(i.Documents[0].Occurrences[0]).(*scip.Occurrence)
			o.SymbolRoles = 0
			i.Documents[0].Occurrences = append(i.Documents[0].Occurrences, o)
		}, Invalid},
		{"conflicting relationship", func(i *scip.Index) {
			i.Documents[0].Symbols[0].Relationships = append(i.Documents[0].Symbols[0].Relationships, &scip.Relationship{Symbol: "local 2", IsDefinition: true})
		}, Invalid},
		{"invalid symbol", func(i *scip.Index) { i.Documents[0].Symbols[0].Symbol = "not a symbol" }, Invalid},
		{"external local", func(i *scip.Index) { i.ExternalSymbols[0].Symbol = "local 1" }, Invalid},
		{"bad range", func(i *scip.Index) { i.Documents[0].Occurrences[0].Range = []int32{0, 5, 1} }, Invalid},
		{"negative range", func(i *scip.Index) { i.Documents[0].Occurrences[0].Range = []int32{-1, 0, 1} }, Invalid},
		{"no range", func(i *scip.Index) { i.Documents[0].Occurrences[0].Range = nil }, Invalid},
		{"bad enclosure", func(i *scip.Index) { i.Documents[0].Occurrences[0].EnclosingRange = []int32{2, 0, 1} }, Invalid},
		{"unknown role", func(i *scip.Index) { i.Documents[0].Occurrences[0].SymbolRoles = 128 }, Unsupported},
		{"unknown encoding", func(i *scip.Index) { i.Documents[0].PositionEncoding = 99 }, Unsupported},
		{"unknown unspecified producer", func(i *scip.Index) { i.Metadata.ToolInfo.Name = "other" }, Unsupported},
		{"symbol length", func(i *scip.Index) { i.Documents[0].Occurrences[0].Symbol = strings.Repeat("x", MaxSCIPSymbolBytes+1) }, Capacity},
		{"hover parts", func(i *scip.Index) { i.Documents[0].Symbols[0].Documentation = make([]string, MaxSCIPParts+1) }, Capacity},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			i := canonicalFixture()
			test.edit(i)
			_, err := CanonicalSCIP(context.Background(), canonicalWire(t, i))
			if !errors.Is(err, test.want) {
				t.Fatalf("got %v want %v", err, test.want)
			}
		})
	}
}
func TestCanonicalSCIPWire(t *testing.T) {
	valid := canonicalWire(t, canonicalFixture())
	metadata := canonicalWire(t, &scip.Index{Metadata: canonicalFixture().Metadata})
	unknown := protowire.AppendTag(bytes.Clone(valid), 63, protowire.VarintType)
	unknown = protowire.AppendVarint(unknown, 1)
	cases := []struct {
		name string
		raw  []byte
		want error
	}{
		{"empty", nil, Invalid}, {"truncated", valid[:len(valid)-1], Invalid},
		{"duplicate metadata", append(bytes.Clone(valid), metadata...), Invalid},
		{"unknown field", unknown, Unsupported}, {"wire bound", make([]byte, MaxSCIPMemberBytes+1), Capacity},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := CanonicalSCIP(context.Background(), test.raw)
			if !errors.Is(err, test.want) {
				t.Fatalf("got %v want %v", err, test.want)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CanonicalSCIP(ctx, valid); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	// Preallocation census refuses many empty records before their semantic
	// invalidity is considered: no attacker-selected protobuf slice is allocated.
	for _, test := range []struct {
		name       string
		descriptor protoreflect.MessageDescriptor
		field      protowire.Number
		count      int
	}{
		{"documents", (&scip.Index{}).ProtoReflect().Descriptor(), 2, MaxSCIPDocuments},
		{"occurrences", (&scip.Document{}).ProtoReflect().Descriptor(), 2, MaxSCIPOccurrences},
		{"symbols", (&scip.Document{}).ProtoReflect().Descriptor(), 3, MaxSCIPSymbols},
		{"relationships", (&scip.SymbolInformation{}).ProtoReflect().Descriptor(), 4, MaxSCIPRelationships},
	} {
		t.Run(test.name, func(t *testing.T) {
			var raw []byte
			for n := 0; n < test.count; n++ {
				raw = protowire.AppendTag(raw, test.field, protowire.BytesType)
				raw = protowire.AppendBytes(raw, nil)
			}
			c := scipCounts{}
			if err := c.wire(context.Background(), raw, test.descriptor, 0); err != nil {
				t.Fatal("at cap", err)
			}
			raw = protowire.AppendTag(raw, test.field, protowire.BytesType)
			raw = protowire.AppendBytes(raw, nil)
			c = scipCounts{}
			if err := c.wire(context.Background(), raw, test.descriptor, 0); err != Capacity {
				t.Fatal("over cap", err)
			}
		})
	}
}
func TestCanonicalSCIPKnownAndGenerated(t *testing.T) {
	for _, encoding := range []scip.PositionEncoding{0, 1, 2, 3} {
		i := canonicalFixture()
		i.Documents[0].PositionEncoding = encoding
		i.Documents[0].RelativePath = ".phebs-generated/package/generated.go"
		if _, err := CanonicalSCIP(context.Background(), canonicalWire(t, i)); err != nil {
			t.Fatal(encoding, err)
		}
	}
	i := canonicalFixture()
	i.Documents[0].Occurrences = append(i.Documents[0].Occurrences, proto.Clone(i.Documents[0].Occurrences[0]).(*scip.Occurrence))
	raw, err := CanonicalSCIP(context.Background(), canonicalWire(t, i))
	if err != nil {
		t.Fatal(err)
	}
	var got scip.Index
	if err := proto.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Documents[0].Occurrences) != 3 {
		t.Fatal("identical duplicates silently removed")
	}
}

func TestCanonicalSCIPBoundaryAndWireAmbiguity(t *testing.T) {
	// The full byte ceiling is accepted; its next byte refuses before decode.
	i := canonicalFixture()
	i.Documents[0].Text = strings.Repeat("x", MaxSCIPMemberBytes-len(canonicalWire(t, i))-8)
	raw := canonicalWire(t, i)
	if len(raw) != MaxSCIPMemberBytes {
		t.Fatalf("boundary fixture size %d", len(raw))
	}
	if _, err := CanonicalSCIP(context.Background(), raw); err != nil {
		t.Fatal("exact wire cap", err)
	}
	i.Documents[0].Text += "x"
	if _, err := CanonicalSCIP(context.Background(), canonicalWire(t, i)); err != Capacity {
		t.Fatal("cap plus one", err)
	}
	for _, test := range []struct {
		name string
		desc protoreflect.MessageDescriptor
		raw  []byte
		want error
	}{
		{"duplicate string", (&scip.ToolInfo{}).ProtoReflect().Descriptor(), []byte{10, 1, 'a', 10, 1, 'b'}, Invalid},
		{"wrong wire type", (&scip.ToolInfo{}).ProtoReflect().Descriptor(), []byte{8, 1}, Invalid},
		{"invalid UTF8", (&scip.ToolInfo{}).ProtoReflect().Descriptor(), []byte{10, 1, 255}, Invalid},
		{"split packed range", (&scip.Occurrence{}).ProtoReflect().Descriptor(), []byte{10, 3, 0, 0, 1, 10, 2, 1, 1}, Capacity},
		{"unpacked range", (&scip.Occurrence{}).ProtoReflect().Descriptor(), []byte{8, 0, 8, 0, 8, 1, 8, 1, 8, 1}, Capacity},
		{"conflicting oneof", (&scip.Occurrence{}).ProtoReflect().Descriptor(), []byte{66, 0, 74, 0}, Invalid},
		{"bool overflow", (&scip.Relationship{}).ProtoReflect().Descriptor(), []byte{16, 2}, Invalid},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := scipCounts{}
			if err := c.wire(context.Background(), test.raw, test.desc, 0); err != test.want {
				t.Fatal(err)
			}
		})
	}
	c := scipCounts{messages: MaxSCIPMessages}
	if err := c.wire(context.Background(), nil, (&scip.Diagnostic{}).ProtoReflect().Descriptor(), 0); err != Capacity {
		t.Fatal("aggregate messages", err)
	}
}

func TestCanonicalSCIPBlankIdentifierConflict(t *testing.T) {
	// scip-go can emit one global blank-identifier key for distinct declarations.
	// This mirrors the returned proto/fan-out collision without copying source.
	// Input order must never choose which declaration's metadata becomes current.
	i := canonicalFixture()
	i.Documents[0].Symbols = []*scip.SymbolInformation{
		{Symbol: "scip-go gomod example.com v1 pkg/_.", Documentation: []string{"first declaration"}, SignatureDocumentation: &scip.Signature{Language: "go", Text: "const _ = 20"}},
		{Symbol: "scip-go gomod example.com v1 pkg/_.", Documentation: []string{"second declaration"}, SignatureDocumentation: &scip.Signature{Language: "go", Text: "const _ = 16"}},
	}
	for n := 0; n < 2; n++ {
		if _, err := CanonicalSCIP(context.Background(), canonicalWire(t, i)); err != Invalid {
			t.Fatal(err)
		}
		slices.Reverse(i.Documents[0].Symbols)
	}
	// Identical local keys belong to different documents; metadata may differ.
	i = canonicalFixture()
	i.Documents[1].Symbols = []*scip.SymbolInformation{{Symbol: "local 1", Documentation: []string{"other scope"}}}
	if _, err := CanonicalSCIP(context.Background(), canonicalWire(t, i)); err != nil {
		t.Fatal("local scoping", err)
	}
	// The same global key in separate documents must agree.
	i.Documents[0].Symbols[0].Symbol = "scip-go gomod example.com v1 pkg/X."
	i.Documents[1].Symbols[0].Symbol = i.Documents[0].Symbols[0].Symbol
	if _, err := CanonicalSCIP(context.Background(), canonicalWire(t, i)); err != Invalid {
		t.Fatal("global metadata conflict", err)
	}
}

//nolint:staticcheck // Compatibility must reject conflicting legacy and typed ranges.
func TestCanonicalSCIPTypedRangesAndSignature(t *testing.T) {
	i := canonicalFixture()
	o := i.Documents[0].Occurrences[0]
	o.TypedRange = &scip.Occurrence_SingleLineRange{SingleLineRange: &scip.SingleLineRange{Line: 0, StartCharacter: 0, EndCharacter: 1}}
	o.EnclosingRange = []int32{0, 0, 10}
	o.TypedEnclosingRange = &scip.Occurrence_SingleLineEnclosingRange{SingleLineEnclosingRange: &scip.SingleLineRange{Line: 0, StartCharacter: 0, EndCharacter: 10}}
	i.Documents[0].Symbols[0].SignatureDocumentation = &scip.Signature{Language: "go", Text: "func F()", Occurrences: []*scip.Occurrence{{Range: []int32{0, 5, 6}, Symbol: "local 1"}, {Range: []int32{0, 0, 4}}}}
	first, err := CanonicalSCIP(context.Background(), canonicalWire(t, i))
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(i.Documents[0].Symbols[0].SignatureDocumentation.Occurrences)
	second, err := CanonicalSCIP(context.Background(), canonicalWire(t, i))
	if err != nil || !bytes.Equal(first, second) {
		t.Fatal("signature ordering", err)
	}
	o.Range = []int32{0, 1, 2}
	if _, err := CanonicalSCIP(context.Background(), canonicalWire(t, i)); err != Invalid {
		t.Fatal("source range conflict", err)
	}
	o.Range = []int32{0, 0, 1}
	o.EnclosingRange = []int32{0, 0, 20}
	if _, err := CanonicalSCIP(context.Background(), canonicalWire(t, i)); err != Invalid {
		t.Fatal("enclosing range conflict", err)
	}
}

//nolint:staticcheck // Measure the pinned producer wire format.
func BenchmarkCanonicalSCIP(b *testing.B) {
	i := canonicalFixture()
	i.Documents = i.Documents[:1]
	d := i.Documents[0]
	d.Occurrences = nil
	for n := 0; n < MaxSCIPOccurrences; n++ {
		d.Occurrences = append(d.Occurrences, &scip.Occurrence{Range: []int32{int32(n), 0, 1}, Symbol: "local 1"})
	}
	raw := canonicalWire(b, i)
	b.SetBytes(int64(len(raw)))
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		if _, err := CanonicalSCIP(context.Background(), raw); err != nil {
			b.Fatal(err)
		}
	}
}

func FuzzCanonicalSCIP(f *testing.F) {
	f.Add(canonicalWire(f, canonicalFixture()))
	f.Add([]byte{10, 0})
	f.Fuzz(func(t *testing.T, raw []byte) {
		before := bytes.Clone(raw)
		got, err := CanonicalSCIP(context.Background(), raw)
		if !bytes.Equal(before, raw) {
			t.Fatal("mutated input")
		}
		if err != nil {
			if err != Invalid && err != Unsupported && err != Capacity {
				t.Fatal("unclassified error", err)
			}
			return
		}
		if len(got) > MaxSCIPMemberBytes {
			t.Fatal("unbounded output")
		}
		again, err := CanonicalSCIP(context.Background(), got)
		if err != nil || !bytes.Equal(got, again) {
			t.Fatal("noncanonical output", err)
		}
	})
}
