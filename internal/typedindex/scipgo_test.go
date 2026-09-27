package typedindex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

var scipGoRetained = flag.String("scip-go-retained", "", "read-only directory of the three sealed public SCIP files; no target execution")

func scipGoProfile(t *testing.T) Profile {
	t.Helper()
	p, _, _, _ := fixture(t)
	d := p.Definition()
	d.Tools.Indexer = Tool{Version: "0.2.7", Digest: SCIPGoIndexerDigest}
	p, err := DecodeProfile(context.Background(), wire(t, d))
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func scipGoFixture() *scip.Index {
	index := canonicalFixture()
	blank := "scip-go gomod example.com v1 pkg/_."
	index.Documents[0].Symbols = append(index.Documents[0].Symbols, &scip.SymbolInformation{Symbol: blank, Documentation: []string{"first declaration"}, SignatureDocumentation: &scip.Signature{Text: "var _ error"}}, &scip.SymbolInformation{Symbol: blank, Documentation: []string{"second declaration"}, SignatureDocumentation: &scip.Signature{Text: "var _ error"}})
	return index
}
func restoreSCIPGo(t *testing.T, original, adapted []byte, member SCIPGoMemberReceipt) {
	t.Helper()
	var want, got scip.Index
	if proto.Unmarshal(original, &want) != nil || proto.Unmarshal(adapted, &got) != nil {
		t.Fatal("decode")
	}
	if member.RawDigest != hash(original) || member.OutputDigest != hash(adapted) {
		t.Fatal("receipt byte identity")
	}
	count := 0
	for _, doc := range got.Documents {
		for _, symbol := range doc.Symbols {
			for _, mapping := range member.Mappings {
				if mapping.Document == doc.RelativePath && mapping.Local == symbol.Symbol {
					symbol.Symbol = mapping.Original
					count++
					break
				}
			}
		}
	}
	if count != len(member.Mappings) || !proto.Equal(&want, &got) {
		t.Fatal("metadata or multiplicity changed beyond recorded identities", count, len(member.Mappings))
	}
}
func TestSCIPGoBlankMetadata(t *testing.T) {
	ctx := context.Background()
	p := scipGoProfile(t)
	index := scipGoFixture()
	// Identical metadata remains duplicated, while distinct declarations survive.
	index.Documents[0].Symbols = append(index.Documents[0].Symbols, proto.Clone(index.Documents[0].Symbols[2]).(*scip.SymbolInformation))
	raw := canonicalWire(t, index)
	before := bytes.Clone(raw)
	if _, err := CanonicalSCIP(ctx, raw); !errors.Is(err, Invalid) {
		t.Fatal("generic conflict must remain refused", err)
	}
	out, receipt, err := AdaptSCIPGoBlanks(ctx, p, [][]byte{raw})
	if err != nil || len(receipt.Members[0].Mappings) != 3 || receipt.Schema != SCIPGoBlankAdapter {
		t.Fatal(receipt, err)
	}
	if !bytes.Equal(raw, before) {
		t.Fatal("caller bytes changed")
	}
	restoreSCIPGo(t, raw, out[0], receipt.Members[0])
	expected, err := CanonicalSCIP(ctx, out[0])
	if err != nil {
		t.Fatal(err)
	}
	locals := map[string]bool{}
	for _, m := range receipt.Members[0].Mappings {
		locals[m.Local] = true
	}
	if len(locals) != 2 {
		t.Fatal("distinct metadata or duplicate identity lost")
	}
	for range 8 {
		slices.Reverse(index.Documents)
		for _, doc := range index.Documents {
			slices.Reverse(doc.Symbols)
			slices.Reverse(doc.Occurrences)
		}
		other, rec, e := AdaptSCIPGoBlanks(ctx, p, [][]byte{canonicalWire(t, index)})
		if e != nil {
			t.Fatal(e)
		}
		canonical, e := CanonicalSCIP(ctx, other[0])
		if e != nil || !bytes.Equal(canonical, expected) {
			t.Fatal("permutation changed semantic result", e)
		}
		if !slices.Equal(rec.Members[0].Mappings, receipt.Members[0].Mappings) {
			t.Fatal("unstable mappings")
		}
	}
	again, rec, err := AdaptSCIPGoBlanks(ctx, p, out)
	if err != nil || !bytes.Equal(again[0], out[0]) || len(rec.Members[0].Mappings) != 0 {
		t.Fatal("adaptation not idempotent", err)
	}
}

//nolint:staticcheck // Exercise the legacy producer range representation unchanged.
func TestSCIPGoReferenceRefusal(t *testing.T) {
	blank := "scip-go gomod example.com v1 pkg/_."
	cases := []struct {
		name string
		edit func(*scip.Index)
	}{
		{"occurrence", func(i *scip.Index) { i.Documents[1].Occurrences[0].Symbol = blank }},
		{"escaped alias", func(i *scip.Index) { i.Documents[1].Occurrences[0].Symbol = "scip-go gomod example.com v1 pkg/`_`." }},
		{"enclosing", func(i *scip.Index) {
			i.Documents[1].Symbols = []*scip.SymbolInformation{{Symbol: "local x", EnclosingSymbol: blank}}
		}},
		{"relationship", func(i *scip.Index) { i.Documents[0].Symbols[0].Relationships[0].Symbol = blank }},
		{"signature", func(i *scip.Index) {
			i.Documents[0].Symbols[0].SignatureDocumentation = &scip.Signature{Occurrences: []*scip.Occurrence{{Range: []int32{0, 0, 1}, Symbol: blank}}}
		}},
		{"external enclosing", func(i *scip.Index) { i.ExternalSymbols[0].EnclosingSymbol = blank }},
		{"external relationship", func(i *scip.Index) { i.ExternalSymbols[0].Relationships = []*scip.Relationship{{Symbol: blank}} }},
		{"external signature", func(i *scip.Index) {
			i.ExternalSymbols[0].SignatureDocumentation = &scip.Signature{Occurrences: []*scip.Occurrence{{Range: []int32{0, 0, 1}, Symbol: blank}}}
		}},
		{"external blank", func(i *scip.Index) {
			i.ExternalSymbols = append(i.ExternalSymbols, &scip.SymbolInformation{Symbol: blank})
		}},
	}
	p := scipGoProfile(t)
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			index := scipGoFixture()
			test.edit(index)
			if _, _, err := AdaptSCIPGoBlanks(context.Background(), p, [][]byte{canonicalWire(t, index)}); !errors.Is(err, Unsupported) {
				t.Fatal(err)
			}
			if _, _, err := AdaptSCIPGoBlanks(context.Background(), p, [][]byte{canonicalWire(t, scipGoFixture())}); err != nil {
				t.Fatal("restored positive", err)
			}
		})
	}
	// A reference in another member, even before the declaration, forbids repair.
	first := canonicalFixture()
	first.Documents[0].Occurrences[0].Symbol = blank
	second := scipGoFixture()
	for _, members := range [][][]byte{{canonicalWire(t, first), canonicalWire(t, second)}, {canonicalWire(t, second), canonicalWire(t, first)}} {
		if _, _, err := AdaptSCIPGoBlanks(context.Background(), p, members); !errors.Is(err, Unsupported) {
			t.Fatal("cross member", err)
		}
	}
}
func TestSCIPGoCollisionAndPin(t *testing.T) {
	ctx := context.Background()
	p := scipGoProfile(t)
	raw := canonicalWire(t, scipGoFixture())
	_, receipt, err := AdaptSCIPGoBlanks(ctx, p, [][]byte{raw})
	if err != nil {
		t.Fatal(err)
	}
	local := receipt.Members[0].Mappings[0].Local
	for _, reference := range []bool{false, true} {
		index := scipGoFixture()
		if reference {
			index.Documents[0].Occurrences[0].Symbol = local
		} else {
			index.Documents[0].Symbols = append(index.Documents[0].Symbols, &scip.SymbolInformation{Symbol: local})
		}
		if _, _, err := AdaptSCIPGoBlanks(ctx, p, [][]byte{canonicalWire(t, index)}); !errors.Is(err, Invalid) {
			t.Fatal("local capture", reference, err)
		}
	}
	for _, edit := range []func(*ProfileDefinition){func(d *ProfileDefinition) { d.Tools.Indexer.Version = "0.2.8" }, func(d *ProfileDefinition) { d.Tools.Indexer.Digest = hash([]byte("different")) }} {
		d := p.Definition()
		edit(&d)
		bad, e := DecodeProfile(ctx, wire(t, d))
		if e != nil {
			t.Fatal(e)
		}
		if _, _, e := AdaptSCIPGoBlanks(ctx, bad, [][]byte{raw}); !errors.Is(e, Unsupported) {
			t.Fatal("pin", e)
		}
	}
	for _, edit := range []func(*scip.Index){func(i *scip.Index) { i.Metadata.ToolInfo.Name = "other" }, func(i *scip.Index) { i.Metadata.ToolInfo.Version = "0.2.8" }, func(i *scip.Index) { i.Documents[0].Symbols[2].Symbol = "other gomod example.com v1 pkg/_." }} {
		index := scipGoFixture()
		edit(index)
		if _, _, err := AdaptSCIPGoBlanks(ctx, p, [][]byte{canonicalWire(t, index)}); !errors.Is(err, Unsupported) {
			t.Fatal(err)
		}
	}
	// Metadata-only adaptation never makes an ordinary conflict acceptable.
	index := canonicalFixture()
	index.Documents[0].Symbols = append(index.Documents[0].Symbols, &scip.SymbolInformation{Symbol: "local 1", Documentation: []string{"conflict"}})
	out, _, err := AdaptSCIPGoBlanks(ctx, p, [][]byte{canonicalWire(t, index)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CanonicalSCIP(ctx, out[0]); !errors.Is(err, Invalid) {
		t.Fatal("ordinary conflict weakened", err)
	}
}
func TestSCIPGoBoundsAndWire(t *testing.T) {
	ctx := context.Background()
	p := scipGoProfile(t)
	raw := canonicalWire(t, scipGoFixture())
	unknown := protowire.AppendTag(bytes.Clone(raw), 63, protowire.VarintType)
	unknown = protowire.AppendVarint(unknown, 1)
	for _, test := range []struct {
		members [][]byte
		want    error
	}{
		{nil, Capacity}, {make([][]byte, MaxSCIPMembers+1), Capacity}, {[][]byte{nil}, Invalid}, {[][]byte{make([]byte, MaxSCIPMemberBytes+1)}, Capacity}, {[][]byte{unknown}, Unsupported}, {[][]byte{raw[:len(raw)-1]}, Invalid},
	} {
		if _, _, err := AdaptSCIPGoBlanks(ctx, p, test.members); !errors.Is(err, test.want) {
			t.Fatalf("got %v want %v", err, test.want)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := AdaptSCIPGoBlanks(canceled, p, [][]byte{raw}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	// Wire remains within the SCIP cap while duplicated mappings exceed the shared
	// attempt receipt budget. Refuse rather than truncate records or gain a cap.
	index := canonicalFixture()
	index.Documents[0].Symbols = nil
	index.Documents[0].RelativePath = strings.Repeat("a", 500) + ".go"
	blank := &scip.SymbolInformation{Symbol: "scip-go gomod example.com v1 pkg/_."}
	for range MaxSCIPSymbols - 3 {
		index.Documents[0].Symbols = append(index.Documents[0].Symbols, blank)
	}
	bounded := canonicalWire(t, index)
	if len(bounded) > MaxSCIPMemberBytes {
		t.Fatal("invalid bound fixture")
	}
	if _, _, err := AdaptSCIPGoBlanks(ctx, p, [][]byte{bounded}); !errors.Is(err, Capacity) {
		t.Fatal("receipt bound", err)
	}
	index.Documents[0].Symbols = index.Documents[0].Symbols[:1]
	_, receipt, err := AdaptSCIPGoBlanks(ctx, p, [][]byte{canonicalWire(t, index)})
	if err != nil {
		t.Fatal("restored small receipt", err)
	}
	encoded, err := json.Marshal(receipt)
	if err != nil || len(encoded) > MaxAttemptBytes {
		t.Fatal(err)
	}
}
func TestSCIPGoRetainedCompatibility(t *testing.T) {
	if *scipGoRetained == "" {
		t.Skip("explicit read-only retained-public-artifact path required")
	}
	cases := []struct {
		name, hash string
		mappings   int
		canonical  bool
	}{
		{"ordinary", "sha256:f4b6ff5131159826ead98ad3086d694384c84bb1623e02552612fbc8cd1df5e5", 0, true},
		{"proto", "sha256:59b6693822d3f66c885599f42454f7cbb24f0edf7acc5a949563911558c7b8f6", 2, false},
		{"fanout", "sha256:621a7bd790180bb154a5f784fc12b63af70fdb7d1fce42acd4890d6e2e0bf71f", 2, true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(*scipGoRetained, test.name+".scip")
			info, err := os.Stat(path)
			if err != nil || !info.Mode().IsRegular() || info.Size() > MaxSCIPMemberBytes {
				t.Fatal("bounded regular artifact", err)
			}
			raw, err := os.ReadFile(path)
			if err != nil || hash(raw) != test.hash {
				t.Fatal("historical identity", err)
			}
			out, receipt, err := AdaptSCIPGoBlanks(context.Background(), scipGoProfile(t), [][]byte{raw})
			if err != nil || len(receipt.Members[0].Mappings) != test.mappings {
				t.Fatal(receipt, err)
			}
			restoreSCIPGo(t, raw, out[0], receipt.Members[0])
			_, err = CanonicalSCIP(context.Background(), out[0])
			if (err == nil) != test.canonical {
				t.Fatal("generated path remains an independent admission", err)
			}
			if test.name == "ordinary" && !bytes.Equal(raw, out[0]) {
				t.Fatal("ordinary bytes changed")
			}
			again, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(raw, again) {
				t.Fatal("retained artifact changed", err)
			}
			t.Logf("raw=%s adapted=%s mappings=%d", test.hash, receipt.Members[0].OutputDigest, test.mappings)
		})
	}
}

func TestSCIPGoBlankParsing(t *testing.T) {
	for _, value := range []string{"scip-go gomod example.com v1 pkg/_.", "scip-go gomod example.com v1 pkg/`_`."} {
		blank, err := scipGoBlank(value)
		if err != nil || !blank {
			t.Fatal(value, blank, err)
		}
	}
	for _, suffix := range []string{"_#", "method().", "local 1"} {
		value := "scip-go gomod example.com v1 pkg/" + suffix
		if strings.HasPrefix(suffix, "local ") {
			value = suffix
		}
		blank, err := scipGoBlank(value)
		if err != nil || blank {
			t.Fatal(value, blank, err)
		}
	}
}

//nolint:staticcheck // Preserve producer legacy ranges while testing metadata key ordering.
func TestSCIPGoNestedOrderingAndAggregate(t *testing.T) {
	ctx := context.Background()
	p := scipGoProfile(t)
	index := scipGoFixture()
	for _, symbol := range index.Documents[0].Symbols[2:] {
		symbol.Relationships = []*scip.Relationship{{Symbol: "local 8", IsReference: true}, {Symbol: "local 9", IsDefinition: true}}
		symbol.SignatureDocumentation.Occurrences = []*scip.Occurrence{{Range: []int32{0, 0, 1}, Symbol: "local 8"}, {Range: []int32{1, 0, 1}, Symbol: "local 9"}}
	}
	raw := canonicalWire(t, index)
	out, receipt, err := AdaptSCIPGoBlanks(ctx, p, [][]byte{raw})
	if err != nil {
		t.Fatal(err)
	}
	restoreSCIPGo(t, raw, out[0], receipt.Members[0])
	for _, symbol := range index.Documents[0].Symbols[2:] {
		slices.Reverse(symbol.Relationships)
		slices.Reverse(symbol.SignatureDocumentation.Occurrences)
	}
	reversed := canonicalWire(t, index)
	out2, receipt2, err := AdaptSCIPGoBlanks(ctx, p, [][]byte{reversed})
	if err != nil {
		t.Fatal(err)
	}
	restoreSCIPGo(t, reversed, out2[0], receipt2.Members[0])
	if !slices.Equal(receipt.Members[0].Mappings, receipt2.Members[0].Mappings) {
		t.Fatal("nested unordered fields changed identity")
	}
	a, err := CanonicalSCIP(ctx, out[0])
	if err != nil {
		t.Fatal(err)
	}
	b, err := CanonicalSCIP(ctx, out2[0])
	if err != nil || !bytes.Equal(a, b) {
		t.Fatal("nested semantic order changed", err)
	}
	large := canonicalFixture()
	large.Documents[0].Symbols[0].Documentation = make([]string, 36)
	for i := range large.Documents[0].Symbols[0].Documentation {
		large.Documents[0].Symbols[0].Documentation[i] = strings.Repeat("x", 60000)
	}
	raw = canonicalWire(t, large)
	if len(raw) > MaxSCIPMemberBytes || 4*len(raw) <= MaxSCIPAggregateBytes {
		t.Fatal("aggregate fixture is not decisive", len(raw))
	}
	if _, _, err := AdaptSCIPGoBlanks(ctx, p, [][]byte{raw}); err != nil {
		t.Fatal("individually legal member", err)
	}
	if _, _, err := AdaptSCIPGoBlanks(ctx, p, [][]byte{raw, raw, raw, raw}); !errors.Is(err, Capacity) {
		t.Fatal("aggregate bound", err)
	}
	// Local identity collision across members must also refuse.
	original := canonicalWire(t, scipGoFixture())
	_, rec, err := AdaptSCIPGoBlanks(ctx, p, [][]byte{original})
	if err != nil {
		t.Fatal(err)
	}
	referenced := canonicalFixture()
	referenced.Documents[0].Occurrences[0].Symbol = rec.Members[0].Mappings[0].Local
	if _, _, err := AdaptSCIPGoBlanks(ctx, p, [][]byte{canonicalWire(t, referenced), original}); !errors.Is(err, Invalid) {
		t.Fatal("cross member local capture", err)
	}
}
