package typedindex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"
)

// generatedExport points at the sealed proto-17 generated-export receipt. The
// test reads only the receipt and its referenced private file; it performs no
// target execution, no build, and no child process.
var generatedExport = flag.String("generated-export", "", "read-only path to the sealed proto-17 generated-export JSON receipt; no target execution")

// TestRetainedOrdinaryCanonicalOrdering is the T45.3 canonical-ordering
// follow-up grounded on the real sealed ordinary SCIP rather than a synthetic
// fixture. The Phase-1 ordinary attempt stopped before producing bytes, so the
// sealed ordinary member is the authoritative real artifact: the proof is
// ordering-invariance (every semantic permutation of the real bytes
// re-canonicalizes byte-identically) plus decisive content-sensitivity and
// conflict-refusal controls. The retained artifact is never mutated in place.
//
//nolint:staticcheck // The pinned scip-go producer emits legacy ranges; real retained bytes must be measured as-is.
func TestRetainedOrdinaryCanonicalOrdering(t *testing.T) {
	if *scipGoRetained == "" {
		t.Skip("explicit read-only retained-public-artifact path required")
	}
	ctx := context.Background()
	path := filepath.Join(*scipGoRetained, "ordinary.scip")
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > MaxSCIPMemberBytes {
		t.Fatal("bounded regular artifact", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const sealedDigest = "sha256:f4b6ff5131159826ead98ad3086d694384c84bb1623e02552612fbc8cd1df5e5"
	if hash(raw) != sealedDigest {
		t.Fatalf("historical identity: got %s want %s", hash(raw), sealedDigest)
	}
	// CanonicalSCIP accepts the sealed ordinary member and fixes its canonical
	// form. That form need not equal the producer's original wire bytes byte for
	// byte (deterministic re-marshaling may reorder within-message fields); the
	// follow-up's requirement is ordering-invariance, proved below by driving
	// every semantic permutation of the real bytes to this same canonical form.
	expected, err := CanonicalSCIP(ctx, raw)
	if err != nil {
		t.Fatal("sealed ordinary is canonical", err)
	}
	var base scip.Index
	if err := proto.Unmarshal(raw, &base); err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewPCG(45, 17))
	baseWire := canonicalWire(t, &base)
	nonTrivial := 0
	for n := 0; n < 200; n++ {
		index := proto.Clone(&base).(*scip.Index)
		rng.Shuffle(len(index.Documents), func(i, j int) { index.Documents[i], index.Documents[j] = index.Documents[j], index.Documents[i] })
		slices.Reverse(index.ExternalSymbols)
		for _, d := range index.Documents {
			rng.Shuffle(len(d.Occurrences), func(i, j int) { d.Occurrences[i], d.Occurrences[j] = d.Occurrences[j], d.Occurrences[i] })
			rng.Shuffle(len(d.Symbols), func(i, j int) { d.Symbols[i], d.Symbols[j] = d.Symbols[j], d.Symbols[i] })
			for _, s := range d.Symbols {
				rng.Shuffle(len(s.Relationships), func(i, j int) { s.Relationships[i], s.Relationships[j] = s.Relationships[j], s.Relationships[i] })
			}
		}
		permRaw, err := proto.Marshal(index)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(permRaw, baseWire) {
			nonTrivial++
		}
		before := bytes.Clone(permRaw)
		got, err := CanonicalSCIP(ctx, permRaw)
		if err != nil {
			t.Fatalf("permutation %d refused real sealed content: %v", n, err)
		}
		if !bytes.Equal(got, expected) {
			t.Fatalf("permutation %d changed canonical bytes", n)
		}
		if !bytes.Equal(before, permRaw) {
			t.Fatal("mutated caller bytes")
		}
		again, err := CanonicalSCIP(ctx, got)
		if err != nil || !bytes.Equal(again, got) {
			t.Fatal("not idempotent", err)
		}
	}
	if nonTrivial == 0 {
		t.Fatal("no permutation differed from the sealed wire; ordering-invariance untested")
	}
	// Content-sensitivity control: adding real sealed content must change the
	// canonical bytes, so the oracle is not a constant function.
	altered := proto.Clone(&base).(*scip.Index)
	var target *scip.Document
	for _, d := range altered.Documents {
		if len(d.Occurrences) > 0 {
			target = d
			break
		}
	}
	if target == nil {
		t.Fatal("no occurrence for content-sensitivity control")
	}
	target.Occurrences = append(target.Occurrences, proto.Clone(target.Occurrences[0]).(*scip.Occurrence))
	alteredCanon, err := CanonicalSCIP(ctx, canonicalWire(t, altered))
	if err != nil {
		t.Fatal("identical duplicate occurrence refused", err)
	}
	if bytes.Equal(alteredCanon, expected) {
		t.Fatal("added occurrence did not change canonical bytes")
	}
	// Conflict-refusal control: cloning a real global symbol and altering its
	// metadata must be refused, proving the oracle discriminates conflicting
	// duplicates rather than silently picking an input order.
	conflict := proto.Clone(&base).(*scip.Index)
	found := false
	for _, d := range conflict.Documents {
		for _, s := range d.Symbols {
			if strings.HasPrefix(s.Symbol, "local ") {
				continue
			}
			dup := proto.Clone(s).(*scip.SymbolInformation)
			dup.Documentation = []string{"injected conflicting metadata"}
			d.Symbols = append(d.Symbols, dup)
			found = true
			break
		}
		if found {
			break
		}
	}
	if !found {
		t.Fatal("no global symbol for conflict-refusal control")
	}
	if _, err := CanonicalSCIP(ctx, canonicalWire(t, conflict)); !errors.Is(err, Invalid) {
		t.Fatal("conflicting duplicate real symbol accepted", err)
	}
	// The retained artifact must be byte-unchanged after the whole proof.
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(raw, after) {
		t.Fatal("retained artifact changed", err)
	}
	t.Logf("sealed ordinary %s canonical across 200 permutations (%d non-trivial)", sealedDigest, nonTrivial)
}

// TestRetainedGeneratedProtoExport is the T45.3 generated-document-lane
// follow-up grounded on the real sealed proto-17 generated export (the
// protoc-gen-go command.pb.go bytes) rather than a synthetic stub. It proves the
// lane binds the exact real bytes/digest/size into the canonical generated
// namespace, never leaks the producer build path, refuses corruption/absence and
// stale authority, and leaves the reduced v1 profile omitting the lane. The
// receipt and its private file are read-only and never mutated in place.
func TestRetainedGeneratedProtoExport(t *testing.T) {
	if *generatedExport == "" {
		t.Skip("explicit read-only sealed generated-export receipt path required")
	}
	ctx := context.Background()
	receiptRaw, err := os.ReadFile(*generatedExport)
	if err != nil {
		t.Fatal(err)
	}
	var receipt struct {
		ReceiptSHA256 string `json:"receipt_sha256"`
		Documents     []struct {
			DocumentID  string `json:"document_id"`
			ExecPath    string `json:"exec_path"`
			Bytes       int64  `json:"bytes"`
			SHA256      string `json:"sha256"`
			PrivateFile string `json:"private_file"`
		} `json:"documents"`
	}
	if err := json.Unmarshal(receiptRaw, &receipt); err != nil {
		t.Fatal(err)
	}
	if len(receipt.Documents) != 1 {
		t.Fatalf("sealed proto-17 export document count %d", len(receipt.Documents))
	}
	entry := receipt.Documents[0]
	const (
		wantBytes  = int64(40814)
		wantDigest = "sha256:2db759decf2653c9d2ebdeb27f99dc0a755468b44d02af3650093d2b1d33dc1d"
	)
	if entry.Bytes != wantBytes || entry.SHA256 != wantDigest {
		t.Fatalf("unexpected sealed proto-17 export identity: %+v", entry)
	}
	if entry.DocumentID == "" || entry.ExecPath == "" || entry.PrivateFile == "" {
		t.Fatalf("sealed proto-17 export entry has an empty identity field: %+v", entry)
	}
	privatePath := filepath.Join(filepath.Dir(*generatedExport), entry.PrivateFile)
	raw, err := os.ReadFile(privatePath)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(raw)) != entry.Bytes || hash(raw) != entry.SHA256 {
		t.Fatalf("generated export identity: bytes=%d hash=%s", len(raw), hash(raw))
	}
	// Bind the real generated bytes into the sealed generated-document lane.
	_, _, request, _ := fixture(t)
	binding := GenerationBinding{Source: request.Source, RequestDigest: identity(request), ProfileDigest: request.ProfileDigest, ToolsDigest: request.ToolsDigest, PlanDigest: hash([]byte("sealed-plan"))}
	unit, err := NewPackageUnitID(hash([]byte("proto-17-generated-export:" + entry.DocumentID)))
	if err != nil {
		t.Fatal(err)
	}
	provenance := hash([]byte(entry.ExecPath + "+pinned-protoc-gen-go+proto-17-sealed-action"))
	logical := "go/api/command/command.pb.go"
	doc, err := NewGeneratedDocument(ctx, binding, unit, logical, provenance, raw)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Bytes != entry.Bytes || doc.Digest != entry.SHA256 {
		t.Fatalf("descriptor lost real generated identity: %+v", doc)
	}
	wantPath, err := GeneratedPath(unit, logical)
	if err != nil || doc.Path != wantPath || !IsGeneratedPath(doc.Path) {
		t.Fatalf("descriptor path %q != %q (%v)", doc.Path, wantPath, err)
	}
	if strings.Contains(doc.Path, "bazel-out") || strings.Contains(doc.Path, entry.ExecPath) {
		t.Fatalf("producer build path leaked into generated namespace: %q", doc.Path)
	}
	if err := VerifyGeneratedDocument(ctx, doc, binding, unit, raw); err != nil {
		t.Fatal(err)
	}
	// The producer's raw exec path and the bare logical path are never reserved
	// as generated, and absolute/traversal forms of the producer path refuse.
	if IsGeneratedPath(entry.ExecPath) || IsGeneratedPath(logical) {
		t.Fatal("producer or logical path wrongly reserved as generated")
	}
	if _, err := GeneratedPath(unit, "/"+entry.ExecPath); !errors.Is(err, Invalid) {
		t.Fatal("absolute producer path accepted", err)
	}
	if _, err := GeneratedPath(unit, "../"+entry.ExecPath); !errors.Is(err, Invalid) {
		t.Fatal("traversal producer path accepted", err)
	}
	// Corruption of the real bytes cannot pass verification; no alias is held.
	corrupt := bytes.Clone(raw)
	corrupt[0] ^= 1
	if !errors.Is(VerifyGeneratedDocument(ctx, doc, binding, unit, corrupt), Invalid) {
		t.Fatal("corrupted generated bytes accepted")
	}
	if !errors.Is(VerifyGeneratedDocument(ctx, doc, binding, unit, nil), Invalid) {
		t.Fatal("missing generated bytes accepted")
	}
	// A stale binding is refused before hashing.
	stale := binding
	stale.PlanDigest = hash([]byte("other-plan"))
	if !errors.Is(VerifyGeneratedDocument(ctx, doc, stale, unit, raw), Stale) {
		t.Fatal("stale authority accepted")
	}
	// The reduced v1 profile still omits the generated lane.
	if ReducedConfig().GeneratedDocuments != "omit" {
		t.Fatal("reduced profile activated generated documents")
	}
	// The retained private file is byte-unchanged after the proof.
	after, err := os.ReadFile(privatePath)
	if err != nil || !bytes.Equal(raw, after) {
		t.Fatal("retained generated export changed", err)
	}
	t.Logf("sealed proto-17 export %s (%d bytes) bound into generated lane %s", entry.SHA256, entry.Bytes, doc.Path)
}
