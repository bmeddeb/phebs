package typedindex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func receiptFixture(t *testing.T) (bundleFixtureData, SCIPGoAdapterReceipt) {
	t.Helper()
	f := bundleFixture(t)
	d := f.execution.profile.Definition()
	d.Tools.Indexer = Tool{Version: "0.2.7", Digest: SCIPGoIndexerDigest}
	p, e := DecodeProfile(context.Background(), wire(t, d))
	if e != nil {
		t.Fatal(e)
	}
	r := NewRequest(f.parent.request.Source, p, f.parent.request.ProfileEpoch, f.parent.request.UniverseDigest, f.parent.request.IdempotencyKey)
	a := Authority{Enabled: true, Administrator: true, Source: r.Source, Profile: Epoch{r.ProfileEpoch, p.Digest()}, UniverseDigest: r.UniverseDigest}
	f.parent = admit(t, p, a, r)
	definition := f.plan.definition
	definition.ParentRequestDigest = f.parent.Digest()
	f.plan, e = SealPackagePlan(context.Background(), f.parent, definition)
	if e != nil {
		t.Fatal(e)
	}
	r, e = PlannedSuccessor(context.Background(), f.parent, f.plan.Digest())
	if e != nil {
		t.Fatal(e)
	}
	a.ParentRequestDigest, a.PlanDigest = f.parent.Digest(), f.plan.Digest()
	f.execution = admit(t, p, a, r)
	input := make([][]byte, len(f.members))
	for j, m := range f.members {
		input[j] = m.SCIP
	}
	out, receipt, e := AdaptSCIPGoBlanks(context.Background(), p, input)
	if e != nil {
		t.Fatal(e)
	}
	for j := range f.members {
		f.members[j].SCIP = out[j]
	}
	return f, receipt
}

func TestSCIPGoReceiptPersistence(t *testing.T) {
	ctx := context.Background()
	f, receipt := receiptFixture(t)
	legacy, e := BuildBundle(ctx, f.execution, f.plan, f.outcomes, f.members, f.generated)
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(legacy.AttemptBytes(), []byte(`"scip_go"`)) {
		t.Fatal("legacy bytes gained field")
	}
	var old AttemptManifest
	if e = json.Unmarshal(legacy.AttemptBytes(), &old); e != nil || old.Schema != AttemptSchema {
		t.Fatal(e)
	}
	b, e := BuildBundleWithSCIPGoReceipt(ctx, f.execution, f.plan, f.outcomes, f.members, f.generated, receipt)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = VerifyBundle(ctx, f.execution, f.plan, b.AttemptBytes(), b.RootBytes(), bundleContents(b)); e != nil {
		t.Fatal(e)
	}
	slices.Reverse(f.members)
	slices.Reverse(receipt.Members)
	again, e := BuildBundleWithSCIPGoReceipt(ctx, f.execution, f.plan, f.outcomes, f.members, f.generated, receipt)
	if e != nil || b.RootDigest() != again.RootDigest() {
		t.Fatal("member order changed audit/root", e)
	}
	legacyAgain, e := BuildBundle(ctx, f.execution, f.plan, f.outcomes, f.members, f.generated)
	if e != nil || !bytes.Equal(legacy.AttemptBytes(), legacyAgain.AttemptBytes()) || legacy.RootDigest() != legacyAgain.RootDigest() {
		t.Fatal("legacy changed", e)
	}
	var m AttemptManifest
	if e = json.Unmarshal(b.AttemptBytes(), &m); e != nil {
		t.Fatal(e)
	}
	m.SCIPGo.Members[0].RawDigest = hash([]byte("different historical producer bytes"))
	if _, e = VerifyBundle(ctx, f.execution, f.plan, wire(t, m), b.RootBytes(), bundleContents(b)); e == nil {
		t.Fatal("audit not root-bound")
	}
}

func TestSCIPGoReceiptMalformedAndBudget(t *testing.T) {
	ctx := context.Background()
	f, receipt := receiptFixture(t)
	for _, kind := range []string{"schema", "digest", "local", "original", "nil-mappings", "too-many-members"} {
		raw := wire(t, receipt)
		var bad SCIPGoAdapterReceipt
		if e := json.Unmarshal(raw, &bad); e != nil {
			t.Fatal(e)
		}
		m := SCIPGoBlankMapping{Document: "a.go", Original: "scip-go gomod example.com v1 pkg/_.", MetadataDigest: hash([]byte("metadata"))}
		key, _ := json.Marshal([]string{SCIPGoBlankAdapter, m.Document, m.MetadataDigest})
		m.Local = "local phebs_blank_" + strings.TrimPrefix(hash(key), "sha256:")
		bad.Members[0].Mappings = []SCIPGoBlankMapping{m}
		switch kind {
		case "schema":
			bad.Schema = "other"
		case "digest":
			bad.Members[0].RawDigest = "sha256:invalid"
		case "local":
			bad.Members[0].Mappings[0].Local = "local arbitrary"
		case "original":
			bad.Members[0].Mappings[0].Original = "scip-go gomod example.com v1 pkg/value."
		case "nil-mappings":
			bad.Members[0].Mappings = nil
		case "too-many-members":
			bad.Members = append(bad.Members, bad.Members...)
			bad.Members = append(bad.Members, bad.Members[0])
		}
		if _, e := BuildBundleWithSCIPGoReceipt(ctx, f.execution, f.plan, f.outcomes, f.members, f.generated, bad); e == nil {
			t.Fatal(kind)
		}
	}
	// Audit alone fits, but consumes the same attempt budget as normal facts.
	large := receipt
	large.Members = slices.Clone(receipt.Members)
	for j := 0; j < 31; j++ {
		m := SCIPGoBlankMapping{Document: fmt.Sprintf("%02d", j) + strings.Repeat("a", MaxSCIPTextBytes-2), Original: "scip-go gomod example.com v1 pkg/_.", MetadataDigest: hash([]byte("metadata"))}
		key, _ := json.Marshal([]string{SCIPGoBlankAdapter, m.Document, m.MetadataDigest})
		m.Local = "local phebs_blank_" + strings.TrimPrefix(hash(key), "sha256:")
		large.Members[0].Mappings = append(large.Members[0].Mappings, m)
	}
	base := len(wire(t, large))
	remaining := MaxAttemptBytes - base - 2
	m := SCIPGoBlankMapping{Document: "zz" + strings.Repeat("a", remaining-400), Original: "scip-go gomod example.com v1 pkg/_.", MetadataDigest: hash([]byte("metadata"))}
	key, _ := json.Marshal([]string{SCIPGoBlankAdapter, m.Document, m.MetadataDigest})
	m.Local = "local phebs_blank_" + strings.TrimPrefix(hash(key), "sha256:")
	large.Members[0].Mappings = append(large.Members[0].Mappings, m)
	if n := len(wire(t, large)); n > MaxAttemptBytes {
		t.Fatal("fixture audit alone too large", n)
	}
	if _, e := BuildBundleWithSCIPGoReceipt(ctx, f.execution, f.plan, f.outcomes, f.members, f.generated, large); !errors.Is(e, Capacity) {
		t.Fatal("combined attempt cap", e, len(wire(t, large)))
	}
	oversize := []byte(`{"scip_go":{"members":[{"mappings":[` + strings.Repeat(`{},`, MaxSCIPSymbols) + `{}]}]}}`)
	if e := scipGoAttemptDimensions(ctx, oversize); !errors.Is(e, Capacity) {
		t.Fatal("receipt predecode cap", e)
	}
	if _, e := VerifyBundle(ctx, f.execution, f.plan, oversize, nil, nil); !errors.Is(e, Capacity) {
		t.Fatal("persistence bypassed preflight", e)
	}
}

func TestSCIPGoReceiptZeroMappingsCanChangeWire(t *testing.T) {
	f, _ := receiptFixture(t)
	var index scip.Index
	if e := proto.Unmarshal(f.members[0].SCIP, &index); e != nil {
		t.Fatal(e)
	}
	// Preserve unique fields and change only the top-level wire order; a zero-
	// mapping adapter run can therefore truthfully record distinct digests.
	metadata, e := proto.Marshal(index.Metadata)
	if e != nil {
		t.Fatal(e)
	}
	doc, e := proto.Marshal(index.Documents[0])
	if e != nil {
		t.Fatal(e)
	}
	raw := append(pbReceipt(2, doc), pbReceipt(1, metadata)...)
	_, receipt, e := AdaptSCIPGoBlanks(context.Background(), f.execution.profile, [][]byte{raw})
	if e != nil {
		t.Fatal(e)
	}
	if len(receipt.Members[0].Mappings) != 0 || receipt.Members[0].RawDigest == receipt.Members[0].OutputDigest {
		t.Fatal("fixture did not reorder wire")
	}
	if e = validateSCIPGoReceipt(context.Background(), f.execution.profile, &receipt); e != nil {
		t.Fatal(e)
	}
}

func pbReceipt(n protowire.Number, b []byte) []byte {
	return protowire.AppendBytes(protowire.AppendTag(nil, n, protowire.BytesType), b)
}
