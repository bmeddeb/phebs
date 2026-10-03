package typedindex

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"slices"
	"strings"

	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"
)

// Routing authenticates navigation metadata against an already admitted current
// root. It does not replace publication-time VerifyBundle or grant current authority.
// It retains no member/source bytes, filesystem handles or mutable public maps.
type Routing struct {
	root      BundleRoot
	digest    string
	members   map[string]SCIPMember
	documents map[string]DocumentRoute
	symbols   map[string][]string
	generated map[string]GeneratedDocument
	bytes     int64
}

func (r Routing) Digest() string        { return r.digest }
func (r Routing) Root() BundleRoot      { return r.root }
func (r Routing) AccountedBytes() int64 { return r.bytes }
func (r Routing) Counts() (int, int) {
	n := 0
	for _, m := range r.members {
		n += m.Occurrences
	}
	return len(r.documents), n
}
func (r Routing) Document(name string) (DocumentRoute, bool) {
	d, ok := r.documents[name]
	return d, ok
}
func (r Routing) Member(name string) (SCIPMember, bool) { m, ok := r.members[name]; return m, ok }
func (r Routing) Generated(name string) (GeneratedDocument, bool) {
	d, ok := r.generated[name]
	return d, ok
}
func (r Routing) SymbolMembers(document, symbol string) []string {
	if !scip.IsLocalSymbol(symbol) {
		document = ""
	}
	return slices.Clone(r.symbols[document+"\x00"+symbol])
}
func (r Routing) Files() []ContentReference {
	out := []ContentReference{r.root.Attempt, r.root.Documents, r.root.Symbols}
	for _, m := range r.members {
		out = append(out, m.ContentReference)
	}
	for _, d := range r.generated {
		out = append(out, ContentReference{Name: d.Path, Digest: d.Digest, Bytes: int(d.Bytes)})
	}
	slices.SortFunc(out, func(a, b ContentReference) int { return strings.Compare(a.Name, b.Name) })
	return out
}
func routingReference(r ContentReference, kind, extension string, limit int) bool {
	return digest(r.Digest) && r.Bytes > 0 && r.Bytes <= limit && r.Name == kind+"/"+r.Digest[7:]+extension
}

// DecodeRoutingRoot validates fixed references before any filesystem control read.
func DecodeRoutingRoot(ctx context.Context, a Admission, expected string, root []byte) (BundleRoot, error) {
	if ctx == nil || a.digest == "" || a.request.Action != Execute || !digest(expected) || len(root) > MaxRootBytes || hash(root) != expected {
		return BundleRoot{}, Stale
	}
	if err := ctx.Err(); err != nil {
		return BundleRoot{}, err
	}
	var b BundleRoot
	if err := decode(root, MaxRootBytes, &b); err != nil {
		return BundleRoot{}, err
	}
	if b.Schema != SCIPBundleSchema || b.Binding != generationBinding(a) || !routingReference(b.Attempt, "attempts", ".json", MaxAttemptBytes) || !routingReference(b.Documents, "documents", ".json", MaxRouteBytes) || !routingReference(b.Symbols, "symbols", ".json", MaxRouteBytes) {
		return BundleRoot{}, Invalid
	}
	return b, nil
}

// DecodeRouting reads only four bounded controls. The expected root/admission
// must come from trusted current state; an arbitrary self-hashed root is not authority.
func DecodeRouting(ctx context.Context, a Admission, expected string, root, attempt, documents, symbols []byte) (Routing, error) {
	var z Routing
	b, err := DecodeRoutingRoot(ctx, a, expected, root)
	if err != nil {
		return z, err
	}
	for _, pair := range []struct {
		ref ContentReference
		raw []byte
	}{{b.Attempt, attempt}, {b.Documents, documents}, {b.Symbols, symbols}} {
		if len(pair.raw) != pair.ref.Bytes || hash(pair.raw) != pair.ref.Digest {
			return z, Invalid
		}
	}
	if err := scipGoAttemptDimensions(ctx, attempt); err != nil {
		return z, err
	}
	var m AttemptManifest
	if err := decode(attempt, MaxAttemptBytes, &m); err != nil {
		return z, err
	}
	if m.Request != a.request || !m.Complete || len(m.Members) == 0 || len(m.Members) > MaxSCIPMembers || len(m.Documents) > MaxBundleDocuments || len(m.Generated) > MaxBundleDocuments || len(m.Units) > MaxBundleUnits || len(m.Targets) > MaxBundleTargets {
		return z, Invalid
	}
	if err := validateInputReceipt(a.profile, m.Input); err != nil {
		return z, err
	}
	if m.Schema == SCIPGoAttemptSchema {
		if err := validateSCIPGoReceipt(ctx, a.profile, m.SCIPGo); err != nil {
			return z, err
		}
	} else if m.Schema != AttemptSchema || m.SCIPGo != nil {
		return z, Invalid
	}
	// Document routes repeat the exact attempt facts; symbols are an independent
	// authenticated index whose members must all belong to this attempt.
	want, _ := json.Marshal(m.Documents)
	if !bytes.Equal(want, documents) {
		return z, Invalid
	}
	r := Routing{root: b, digest: expected, members: map[string]SCIPMember{}, documents: map[string]DocumentRoute{}, symbols: map[string][]string{}, generated: map[string]GeneratedDocument{}, bytes: 512 + int64(len(root)+len(attempt)+len(documents)+len(symbols))}
	units := map[PackageUnitID]UnitState{}
	for _, u := range m.Units {
		if !validPackageUnit(u.Unit) || units[u.Unit] != "" || u.State != UnitComplete && u.State != UnitExcluded {
			return z, Invalid
		}
		units[u.Unit] = u.State
	}
	previous := ""
	var memberBytes int
	for _, member := range m.Members {
		if !routingReference(member.ContentReference, "members", ".scip", MaxSCIPMemberBytes) || member.Name <= previous || !token(member.Slot) || member.Documents < 1 || member.Documents > MaxSCIPDocuments || member.Occurrences < 0 || member.Occurrences > MaxSCIPOccurrences || member.Symbols < 0 || member.Symbols > MaxSCIPSymbols || member.Bytes > MaxSCIPAggregateBytes-memberBytes {
			return z, Invalid
		}
		previous = member.Name
		memberBytes += member.Bytes
		r.members[member.Name] = member
		r.bytes += 256 + int64(len(member.Name)+len(member.Digest)+len(member.Slot))
	}
	counts := map[string]int{}
	previous = ""
	for _, d := range m.Documents {
		if !bundlePath(d.Path) || d.Path <= previous || units[d.Unit] != UnitComplete || r.members[d.Member].Name == "" {
			return z, Invalid
		}
		previous = d.Path
		r.documents[d.Path] = d
		counts[d.Member]++
		r.bytes += 192 + int64(len(d.Path)+len(d.Unit)+len(d.Member))
	}
	for name, member := range r.members {
		if counts[name] != member.Documents {
			return z, Invalid
		}
	}
	previous = ""
	var generatedBytes int64
	for _, d := range m.Generated {
		route, ok := r.documents[d.Path]
		if !ok || !validGeneratedDocument(d) || d.Binding != b.Binding || d.Unit != route.Unit || d.Path <= previous || d.Bytes > MaxGeneratedAggregateBytes-generatedBytes {
			return z, Invalid
		}
		previous = d.Path
		generatedBytes += d.Bytes
		r.generated[d.Path] = d
		r.bytes += 512 + int64(len(d.Path)+len(d.ProvenanceDigest))
	}
	for name := range r.documents {
		if IsGeneratedPath(name) {
			if _, ok := r.generated[name]; !ok {
				return z, Invalid
			}
		}
	}
	// Bound symbol rows and each member list before typed allocation.
	if err := routingSymbolDimensions(ctx, symbols); err != nil {
		return z, err
	}
	var routes []SymbolRoute
	if err := decode(symbols, MaxRouteBytes, &routes); err != nil {
		return z, err
	}
	previous = ""
	for _, s := range routes {
		if err := ctx.Err(); err != nil {
			return z, err
		}
		key := s.LocalDocument + "\x00" + s.Symbol
		if s.Symbol == "" || len(s.Symbol) > MaxSCIPSymbolBytes || key <= previous || len(s.Members) == 0 || len(s.Members) > MaxSCIPMembers {
			return z, Invalid
		}
		if _, err := scip.ParseSymbol(s.Symbol); err != nil {
			return z, Invalid
		}
		if scip.IsLocalSymbol(s.Symbol) {
			if _, ok := r.documents[s.LocalDocument]; !ok {
				return z, Invalid
			}
		} else if s.LocalDocument != "" {
			return z, Invalid
		}
		for j, name := range s.Members {
			if r.members[name].Name == "" || j > 0 && name <= s.Members[j-1] {
				return z, Invalid
			}
		}
		previous = key
		r.symbols[key] = s.Members
		r.bytes += 128 + int64(len(key))
		for _, name := range s.Members {
			r.bytes += 32 + int64(len(name))
		}
	}
	return r, nil
}

// VerifyMember authenticates just the selected immutable canonical member and
// checks its document and symbol routes against the already authenticated index.
func (r Routing) VerifyMember(ctx context.Context, name string, raw []byte) error {
	m, ok := r.members[name]
	if !ok || len(raw) != m.Bytes || hash(raw) != m.Digest {
		return Invalid
	}
	canonical, err := CanonicalSCIP(ctx, raw)
	if err != nil {
		return err
	}
	if !bytes.Equal(canonical, raw) {
		return Invalid
	}
	var index scip.Index
	if proto.Unmarshal(raw, &index) != nil || len(index.Documents) != m.Documents {
		return Invalid
	}
	count, symbols := 0, len(index.ExternalSymbols)
	check := func(doc, symbol string) error {
		if !scip.IsLocalSymbol(symbol) {
			doc = ""
		}
		if symbol != "" && !slices.Contains(r.symbols[doc+"\x00"+symbol], name) {
			return Invalid
		}
		return nil
	}
	info := func(doc string, s *scip.SymbolInformation) error {
		if err := check(doc, s.Symbol); err != nil {
			return err
		}
		for _, rel := range s.Relationships {
			if err := check(doc, rel.Symbol); err != nil {
				return err
			}
		}
		return nil
	}
	for _, d := range index.Documents {
		route, ok := r.documents[d.RelativePath]
		if !ok || route.Member != name {
			return Invalid
		}
		count += len(d.Occurrences)
		symbols += len(d.Symbols)
		for _, o := range d.Occurrences {
			if err := check(d.RelativePath, o.Symbol); err != nil {
				return err
			}
		}
		for _, s := range d.Symbols {
			if err := info(d.RelativePath, s); err != nil {
				return err
			}
		}
	}
	for _, s := range index.ExternalSymbols {
		if err := info("", s); err != nil {
			return err
		}
	}
	if count != m.Occurrences || symbols != m.Symbols {
		return Invalid
	}
	return nil
}

func routingSymbolDimensions(ctx context.Context, raw []byte) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	token := func(want json.Token) bool { v, e := d.Token(); return e == nil && v == want }
	text := func(limit int) bool { v, e := d.Token(); s, ok := v.(string); return e == nil && ok && len(s) <= limit }
	if !token(json.Delim('[')) {
		return Invalid
	}
	for n := 0; d.More(); n++ {
		if e := ctx.Err(); e != nil {
			return e
		}
		if n >= MaxBundleSymbols {
			return Capacity
		}
		if !token(json.Delim('{')) || !token("symbol") || !text(MaxSCIPSymbolBytes) || !token("local_document") || !text(512) || !token("members") || !token(json.Delim('[')) {
			return Invalid
		}
		for j := 0; d.More(); j++ {
			if j >= MaxSCIPMembers {
				return Capacity
			}
			if !text(512) {
				return Invalid
			}
		}
		if !token(json.Delim(']')) || !token(json.Delim('}')) {
			return Invalid
		}
	}
	if !token(json.Delim(']')) {
		return Invalid
	}
	if _, e := d.Token(); e != io.EOF {
		return Invalid
	}
	return nil
}
