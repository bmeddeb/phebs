package typedindex

import (
	"bytes"
	"context"
	"math"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Member ceilings are deliberately narrower than the legacy reader. The sealed
// fan-out member measured 1,523,879 bytes, 13 documents, 13,278 occurrences and 2,342 symbols.
// These are admission ceilings, not a claim about larger repositories.
const (
	MaxSCIPMemberBytes   = 2285819
	MaxSCIPDocuments     = 32
	MaxSCIPOccurrences   = 20000
	MaxSCIPSymbols       = 8192
	MaxSCIPRelationships = 16384
	MaxSCIPSymbolBytes   = 4096
	MaxSCIPTextBytes     = 64 << 10
	MaxSCIPParts         = 128
	MaxSCIPMessages      = 65536
)

// CanonicalSCIP returns an owned conforming Index without changing ranges,
// roles, symbol identities or ordered documentation/arguments. Only documents,
// occurrences, symbols and relationships are reordered. It neither reads source
// nor trusts embedded source text or metadata.project_root as source authority.
//
// A no-copy wire pass bounds every collection before protobuf allocation and
// rejects duplicate singular/oneof fields instead of accepting last-write-wins.
// Sorting costs O(n log n) with one bounded encoded key per record; peak retained
// data is O(wire bytes + bounded message counts). There is no persistent cache,
// lock, filesystem access or child process. Call once before member sealing.
func CanonicalSCIP(ctx context.Context, raw []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(raw) > MaxSCIPMemberBytes {
		return nil, Capacity
	}
	if len(raw) == 0 {
		return nil, Invalid
	}
	counts := scipCounts{}
	if err := counts.wire(ctx, raw, (&scip.Index{}).ProtoReflect().Descriptor(), 0); err != nil {
		return nil, err
	}
	var index scip.Index
	if err := proto.Unmarshal(raw, &index); err != nil {
		return nil, Invalid
	}
	m := index.Metadata
	if m == nil || m.ToolInfo == nil || m.ToolInfo.Name == "" {
		return nil, Invalid
	}
	u, err := url.Parse(m.ProjectRoot)
	if err != nil || u.Scheme != "file" || u.Host != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(u.Path) == 0 || u.Path[0] != '/' {
		return nil, Invalid
	}
	symbols := make(map[string][]byte)
	for _, d := range index.Documents {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !bundlePath(d.RelativePath) {
			return nil, Invalid
		}
		switch d.PositionEncoding {
		case scip.PositionEncoding_UnspecifiedPositionEncoding:
			if m.ToolInfo.Name != "scip-go" {
				return nil, Unsupported
			}
		case scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart, scip.PositionEncoding_UTF16CodeUnitOffsetFromLineStart, scip.PositionEncoding_UTF32CodeUnitOffsetFromLineStart:
		default:
			return nil, Unsupported
		}
		if err := canonicalOccurrences(d.Occurrences); err != nil {
			return nil, err
		}
		if err := canonicalSymbols(d.Symbols, d.RelativePath, symbols); err != nil {
			return nil, err
		}
	}
	if err := canonicalSymbols(index.ExternalSymbols, "", symbols); err != nil {
		return nil, err
	}
	slices.SortFunc(index.Documents, func(a, b *scip.Document) int { return strings.Compare(a.RelativePath, b.RelativePath) })
	for i := 1; i < len(index.Documents); i++ {
		if index.Documents[i-1].RelativePath == index.Documents[i].RelativePath {
			return nil, Invalid
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out, err := (proto.MarshalOptions{Deterministic: true}).Marshal(&index)
	if err != nil {
		return nil, Invalid
	}
	if len(out) > MaxSCIPMemberBytes {
		return nil, Capacity
	}
	return out, nil
}

type scipCounts struct{ documents, occurrences, symbols, relationships, messages int }

// wire does not construct strings or protobuf messages. The fixed field arrays
// cover the pinned SCIP schema (field numbers <64, fewer than 8 oneofs).
func (c *scipCounts) wire(ctx context.Context, raw []byte, desc protoreflect.MessageDescriptor, depth int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.messages++
	if c.messages > MaxSCIPMessages {
		return Capacity
	}
	if depth > 8 {
		return Capacity
	}
	switch desc.Name() {
	case "Document":
		c.documents++
		if c.documents > MaxSCIPDocuments {
			return Capacity
		}
	case "Occurrence":
		c.occurrences++
		if c.occurrences > MaxSCIPOccurrences {
			return Capacity
		}
	case "SymbolInformation":
		c.symbols++
		if c.symbols > MaxSCIPSymbols {
			return Capacity
		}
	case "Relationship":
		c.relationships++
		if c.relationships > MaxSCIPRelationships {
			return Capacity
		}
	}
	var seen [64]int
	var oneofs [8]bool
	for len(raw) > 0 {
		num, typ, n := protowire.ConsumeTag(raw)
		if n < 0 || num <= 0 || num >= 64 {
			return Invalid
		}
		raw = raw[n:]
		field := desc.Fields().ByNumber(num)
		if field == nil {
			return Unsupported
		}
		if !field.IsList() && seen[num] > 0 {
			return Invalid
		}
		if group := field.ContainingOneof(); group != nil {
			if group.Index() >= len(oneofs) || oneofs[group.Index()] {
				return Invalid
			}
			oneofs[group.Index()] = true
		}
		seen[num]++
		limit := MaxSCIPParts
		if field.Kind() == protoreflect.MessageKind {
			limit = MaxSCIPOccurrences
		}
		if field.IsList() && seen[num] > limit {
			return Capacity
		}
		switch field.Kind() {
		case protoreflect.MessageKind, protoreflect.StringKind:
			if typ != protowire.BytesType {
				return Invalid
			}
			value, size := protowire.ConsumeBytes(raw)
			if size < 0 {
				return Invalid
			}
			raw = raw[size:]
			if field.Kind() == protoreflect.MessageKind {
				if err := c.wire(ctx, value, field.Message(), depth+1); err != nil {
					return err
				}
			} else {
				bound := MaxSCIPTextBytes
				switch field.Name() {
				case "symbol", "enclosing_symbol":
					bound = MaxSCIPSymbolBytes
				case "relative_path":
					bound = 512
				case "text":
					if desc.Name() == "Document" {
						bound = MaxSCIPMemberBytes
					}
				}
				if len(value) > bound {
					return Capacity
				}
				if !utf8.Valid(value) {
					return Invalid
				}
			}
		case protoreflect.Int32Kind, protoreflect.EnumKind, protoreflect.BoolKind:
			if field.IsList() && typ == protowire.BytesType {
				value, size := protowire.ConsumeBytes(raw)
				if size < 0 {
					return Invalid
				}
				raw = raw[size:]
				// SCIP packed fields are range coordinates and diagnostic tags. Count
				// elements, not segments, so split packing cannot evade the bound.
				seen[num]--
				for len(value) > 0 {
					v, size := protowire.ConsumeVarint(value)
					if size < 0 {
						return Invalid
					}
					value = value[size:]
					seen[num]++
					if seen[num] > scipScalarLimit(field) {
						return Capacity
					}
					if err := scipScalar(field, v); err != nil {
						return err
					}
				}
			} else {
				if typ != protowire.VarintType {
					return Invalid
				}
				v, size := protowire.ConsumeVarint(raw)
				if size < 0 {
					return Invalid
				}
				raw = raw[size:]
				if field.IsList() && seen[num] > scipScalarLimit(field) {
					return Capacity
				}
				if err := scipScalar(field, v); err != nil {
					return err
				}
			}
		default:
			return Unsupported
		}
	}
	return nil
}
func scipScalarLimit(f protoreflect.FieldDescriptor) int {
	if f.Name() == "range" || f.Name() == "enclosing_range" {
		return 4
	}
	return MaxSCIPParts
}
func scipScalar(f protoreflect.FieldDescriptor, v uint64) error {
	if v > math.MaxInt32 {
		return Invalid
	}
	if f.Kind() == protoreflect.BoolKind && v > 1 {
		return Invalid
	}
	if f.Kind() == protoreflect.EnumKind && f.Enum().Values().ByNumber(protoreflect.EnumNumber(v)) == nil {
		return Unsupported
	}
	return nil
}
func canonicalSymbol(s string, optional bool) error {
	if s == "" && optional {
		return nil
	}
	if s == "" {
		return Invalid
	}
	if len(s) > MaxSCIPSymbolBytes {
		return Capacity
	}
	if _, err := scip.ParseSymbol(s); err != nil {
		return Invalid
	}
	return nil
}
func canonicalSymbols(values []*scip.SymbolInformation, doc string, global map[string][]byte) error {
	for _, s := range values {
		if err := canonicalSymbol(s.Symbol, false); err != nil {
			return err
		}
		if err := canonicalSymbol(s.EnclosingSymbol, true); err != nil {
			return err
		}
		if doc == "" && (scip.IsLocalSymbol(s.Symbol) || scip.IsLocalSymbol(s.EnclosingSymbol)) {
			return Invalid
		}
		for _, r := range s.Relationships {
			if err := canonicalSymbol(r.Symbol, false); err != nil {
				return err
			}
			if doc == "" && scip.IsLocalSymbol(r.Symbol) {
				return Invalid
			}
		}
		if err := canonicalRecords(s.Relationships, func(r *scip.Relationship) string { return r.Symbol }); err != nil {
			return err
		}
		if s.SignatureDocumentation != nil {
			if err := canonicalOccurrences(s.SignatureDocumentation.Occurrences); err != nil {
				return err
			}
		}
		key := s.Symbol
		if scip.IsLocalSymbol(key) {
			key = doc + "\x00" + key
		}
		encoded, err := proto.Marshal(s)
		if err != nil {
			return Invalid
		}
		if old, ok := global[key]; ok && !bytes.Equal(old, encoded) {
			return Invalid
		}
		global[key] = encoded
	}
	return canonicalRecords(values, func(s *scip.SymbolInformation) string { return s.Symbol })
}

//nolint:staticcheck // Pinned scip-go uses legacy ranges; validate both representations without changing either.
func canonicalOccurrences(values []*scip.Occurrence) error {
	for _, o := range values {
		r, ok := o.SourceRange()
		if !ok || r.Validate() != nil {
			return Invalid
		}
		if len(o.GetRange()) != 0 {
			legacy, err := scip.NewRange(o.GetRange())
			if err != nil || legacy != r {
				return Invalid
			}
		}
		if e, ok := o.EnclosingSourceRange(); ok {
			if len(o.GetEnclosingRange()) != 0 {
				legacy, err := scip.NewRange(o.GetEnclosingRange())
				if err != nil || legacy != e {
					return Invalid
				}
			}
			if e.Validate() != nil || e.Start.Line > r.Start.Line || (e.Start.Line == r.Start.Line && e.Start.Character > r.Start.Character) || e.End.Line < r.End.Line || (e.End.Line == r.End.Line && e.End.Character < r.End.Character) {
				return Invalid
			}
		} else if len(o.GetEnclosingRange()) > 0 || o.TypedEnclosingRange != nil {
			return Invalid
		}
		if err := canonicalSymbol(o.Symbol, true); err != nil {
			return err
		}
		if o.SymbolRoles & ^int32(127) != 0 {
			return Unsupported
		}
	}
	return canonicalRecords(values, func(o *scip.Occurrence) string {
		// A range+symbol identifies one occurrence; inconsistent roles, hover or
		// diagnostic metadata at the same identity are refused, never picked by order.
		r, _ := o.SourceRange()
		b := protowire.AppendVarint(nil, uint64(r.Start.Line))
		b = protowire.AppendVarint(b, uint64(r.Start.Character))
		b = protowire.AppendVarint(b, uint64(r.End.Line))
		b = protowire.AppendVarint(b, uint64(r.End.Character))
		return string(b) + "\x00" + o.Symbol
	})
}
func canonicalRecords[T proto.Message](values []T, key func(T) string) error {
	type record struct {
		value   T
		encoded []byte
	}
	records := make([]record, len(values))
	seen := make(map[string][]byte, len(values))
	for i, value := range values {
		encoded, err := proto.Marshal(value)
		if err != nil {
			return Invalid
		}
		k := key(value)
		if old, ok := seen[k]; ok && !bytes.Equal(old, encoded) {
			return Invalid
		}
		seen[k] = encoded
		records[i] = record{value, encoded}
	}
	slices.SortFunc(records, func(a, b record) int { return bytes.Compare(a.encoded, b.encoded) })
	for i, r := range records {
		values[i] = r.value
	}
	return nil
}
