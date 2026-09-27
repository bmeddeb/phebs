package typedindex

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

func routingFixture(t *testing.T) (bundleFixtureData, Bundle, BundleRoot, Routing) {
	t.Helper()
	f := bundleFixture(t)
	b := buildFixture(t, f)
	var root BundleRoot
	if e := json.Unmarshal(b.RootBytes(), &root); e != nil {
		t.Fatal(e)
	}
	r, e := DecodeRouting(t.Context(), f.execution, b.RootDigest(), b.RootBytes(), b.AttemptBytes(), b.Content(root.Documents.Name), b.Content(root.Symbols.Name))
	if e != nil {
		t.Fatal(e)
	}
	return f, b, root, r
}
func TestRoutingExactSelectedMembers(t *testing.T) {
	_, b, _, r := routingFixture(t)
	if r.Digest() != b.RootDigest() || r.AccountedBytes() <= 0 {
		t.Fatal("identity/accounting")
	}
	for _, name := range b.Names() {
		if _, ok := r.Member(name); ok {
			if e := r.VerifyMember(t.Context(), name, b.Content(name)); e != nil {
				t.Fatal(e)
			}
		}
	}
	for _, ref := range r.Files() {
		if len(b.Content(ref.Name)) != ref.Bytes {
			t.Fatal(ref.Name)
		}
	}
	doc, ok := r.Document("a.go")
	if !ok {
		t.Fatal("missing route")
	}
	names := r.SymbolMembers("a.go", "scip-go gomod example.com v1 Command#")
	if len(names) != 2 {
		t.Fatal(names)
	}
	names[0] = "changed"
	if r.SymbolMembers("a.go", "scip-go gomod example.com v1 Command#")[0] == "changed" {
		t.Fatal("mutable route")
	}
	raw := b.Content(doc.Member)
	raw[len(raw)-1] ^= 1
	if e := r.VerifyMember(t.Context(), doc.Member, raw); e == nil {
		t.Fatal("changed member")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if e := r.VerifyMember(ctx, doc.Member, b.Content(doc.Member)); e == nil {
		t.Fatal("cancellation")
	}
}
func TestRoutingControlAttacks(t *testing.T) {
	f, b, root, _ := routingFixture(t)
	for _, kind := range []string{"root", "attempt", "documents", "symbols", "authority", "path", "unknown-member", "missing-symbol"} {
		t.Run(kind, func(t *testing.T) {
			root := root
			rraw, araw, draw, sraw := b.RootBytes(), b.AttemptBytes(), b.Content(root.Documents.Name), b.Content(root.Symbols.Name)
			expect := b.RootDigest()
			switch kind {
			case "root":
				rraw = append(rraw, ' ')
			case "attempt":
				araw = append(araw, ' ')
			case "documents":
				draw = append(draw, ' ')
			case "symbols":
				sraw = append(sraw, ' ')
			case "authority":
				expect = hash([]byte("other"))
			default:
				var rows []SymbolRoute
				if e := json.Unmarshal(sraw, &rows); e != nil {
					t.Fatal(e)
				}
				switch kind {
				case "path":
					rows[0].LocalDocument = "../outside"
				case "unknown-member":
					rows[0].Members[0] = "members/unknown.scip"
				case "missing-symbol":
					rows = rows[:0]
				}
				sraw = wire(t, rows)
				root.Symbols = ContentReference{Name: "symbols/" + hash(sraw)[7:] + ".json", Digest: hash(sraw), Bytes: len(sraw)}
				rraw = wire(t, root)
				expect = hash(rraw)
			}
			r, e := DecodeRouting(t.Context(), f.execution, expect, rraw, araw, draw, sraw)
			if kind == "missing-symbol" {
				if e != nil {
					t.Fatal(e)
				}
				d, _ := r.Document("a.go")
				if e = r.VerifyMember(t.Context(), d.Member, b.Content(d.Member)); e == nil {
					t.Fatal("missing postings accepted")
				}
				return
			}
			if e == nil {
				t.Fatal("attack accepted")
			}
		})
	}
}
func TestRoutingPredecodeSymbolCounts(t *testing.T) {
	raw := append([]byte(`[{"symbol":"x","local_document":"","members":[`), bytes.Repeat([]byte(`"x",`), MaxSCIPMembers)...)
	raw = append(raw, []byte(`"x"]}]`)...)
	if e := routingSymbolDimensions(t.Context(), raw); e == nil {
		t.Fatal("member overflow")
	}
}
