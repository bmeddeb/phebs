package typedindex

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"
)

// These reduce-first ceilings admit the three sealed public cohorts, not a
// large-monorepo envelope. Member bytes retain the measured T45.1b ceiling.
const (
	PackagePlanSchema          = "phebs-typed-package-plan-v1"
	AttemptSchema              = "phebs-scip-attempt-v1"
	SCIPBundleSchema           = "phebs-scip-bundle-v1"
	MaxPlanBytes               = 16 << 20
	MaxAttemptBytes            = 2 << 20
	MaxRootBytes               = 4 << 10
	MaxRouteBytes              = 2 << 20
	MaxSCIPMembers             = 4
	MaxSCIPAggregateBytes      = 8 << 20
	MaxBundleDocuments         = 128
	MaxBundleTargets           = 8192
	MaxBundleUnits             = 512
	MaxBundleEdges             = 131072
	MaxBundleSymbols           = 32768
	MaxGeneratedAggregateBytes = 16 << 20
	MaxPublicationBytes        = 32 << 20
)

// IDs name configured targets and package-load units, never services or files.
// The provider obtains target IDs from its sealed configured universe.
type PlannedTarget struct {
	ID           string          `json:"id"`
	Dependencies []string        `json:"dependencies"`
	Units        []PackageUnitID `json:"units"`
	Test         bool            `json:"test"`
}
type PlannedUnit struct {
	ID        PackageUnitID   `json:"id"`
	Imports   []PackageUnitID `json:"imports"`
	Documents []string        `json:"documents"`
	Test      bool            `json:"test"`
}
type PlannedDocument struct {
	Member           string        `json:"member"`
	Path             string        `json:"path"`
	Unit             PackageUnitID `json:"unit"`
	Bytes            int64         `json:"bytes"`
	Digest           string        `json:"digest"`
	Generated        bool          `json:"generated"`
	ProvenanceDigest string        `json:"provenance_digest"`
}
type PackagePlanDefinition struct {
	Schema              string            `json:"schema"`
	ParentRequestDigest string            `json:"parent_request_digest"`
	Targets             []PlannedTarget   `json:"targets"`
	Units               []PlannedUnit     `json:"units"`
	Documents           []PlannedDocument `json:"documents"`
}
type PackagePlan struct {
	definition PackagePlanDefinition
	raw        []byte
	digest     string
}

func (p PackagePlan) Digest() string { return p.digest }
func (p PackagePlan) Bytes() []byte  { return bytes.Clone(p.raw) }

// SealPackagePlan is called on owned planner output before member execution.
// Targets must reproduce the independently admitted configured-universe digest.
// DecodePackagePlan subsequently requires this exact sealed identity; driver
// output cannot add or remove units, edges, documents or exclusions.
func SealPackagePlan(ctx context.Context, parent Admission, input PackagePlanDefinition) (PackagePlan, error) {
	if err := ctx.Err(); err != nil {
		return PackagePlan{}, err
	}
	if parent.digest == "" || parent.request.Action != Plan {
		return PackagePlan{}, Invalid
	}
	if !planDimensions(input) {
		return PackagePlan{}, Capacity
	}
	raw, err := json.Marshal(input)
	if err != nil || len(raw) > MaxPlanBytes {
		return PackagePlan{}, Capacity
	}
	var d PackagePlanDefinition
	if err := decode(raw, MaxPlanBytes, &d); err != nil {
		return PackagePlan{}, err
	}
	if d.Schema != PackagePlanSchema || d.ParentRequestDigest != parent.digest || len(d.Targets) == 0 || len(d.Targets) > MaxBundleTargets || len(d.Units) == 0 || len(d.Units) > MaxBundleUnits || len(d.Documents) > MaxBundleDocuments {
		return PackagePlan{}, Invalid
	}
	sort.Slice(d.Targets, func(i, j int) bool { return d.Targets[i].ID < d.Targets[j].ID })
	sort.Slice(d.Units, func(i, j int) bool { return d.Units[i].ID < d.Units[j].ID })
	sort.Slice(d.Documents, func(i, j int) bool { return d.Documents[i].Path < d.Documents[j].Path })
	targets := map[string]bool{}
	units := map[PackageUnitID]PlannedUnit{}
	docs := map[string]PlannedDocument{}
	owned := map[PackageUnitID]bool{}
	edges := 0
	addEdges := func(n int) bool {
		if n > MaxBundleEdges-edges {
			return false
		}
		edges += n
		return true
	}
	for i := range d.Targets {
		t := &d.Targets[i]
		if !digest(t.ID) || targets[t.ID] || !addEdges(len(t.Dependencies)+len(t.Units)) {
			return PackagePlan{}, Invalid
		}
		targets[t.ID] = true
		slices.Sort(t.Dependencies)
		slices.Sort(t.Units)
		if duplicates(t.Dependencies) || duplicates(t.Units) {
			return PackagePlan{}, Invalid
		}
	}
	if identity(d.Targets) != parent.request.UniverseDigest {
		return PackagePlan{}, Stale
	}
	for i := range d.Units {
		u := &d.Units[i]
		if !validPackageUnit(u.ID) || units[u.ID].ID != "" || !addEdges(len(u.Imports)+len(u.Documents)) {
			return PackagePlan{}, Invalid
		}
		slices.Sort(u.Imports)
		slices.Sort(u.Documents)
		if duplicates(u.Imports) || duplicates(u.Documents) {
			return PackagePlan{}, Invalid
		}
		units[u.ID] = *u
	}
	var sourceBytes int64
	for _, d := range d.Documents {
		if !bundlePath(d.Path) || docs[d.Path].Path != "" || !validPackageUnit(d.Unit) || units[d.Unit].ID == "" || d.Bytes < 0 || d.Bytes > 8<<20 || !digest(d.Digest) {
			return PackagePlan{}, Invalid
		}
		if d.Generated {
			if !parent.profile.permitsGenerated() {
				return PackagePlan{}, Unsupported
			}
			prefix := ".phebs-generated/" + strings.TrimPrefix(string(d.Unit), "package-load:sha256:") + "/"
			canonical, err := GeneratedPath(d.Unit, strings.TrimPrefix(d.Path, prefix))
			if err != nil || canonical != d.Path || !strings.HasPrefix(d.Path, prefix) || !digest(d.ProvenanceDigest) {
				return PackagePlan{}, Invalid
			}
		} else if IsGeneratedPath(d.Path) || d.ProvenanceDigest != "" {
			return PackagePlan{}, Invalid
		}
		if d.Bytes > MaxGeneratedAggregateBytes-sourceBytes {
			return PackagePlan{}, Capacity
		}
		sourceBytes += d.Bytes
		docs[d.Path] = d
	}
	for _, t := range d.Targets {
		for _, dep := range t.Dependencies {
			if !targets[dep] || dep == t.ID {
				return PackagePlan{}, Invalid
			}
		}
		for _, id := range t.Units {
			u, ok := units[id]
			if !ok || t.Test != u.Test {
				return PackagePlan{}, Invalid
			}
			owned[id] = true
		}
	}
	memberSlots := map[string]bool{}
	for _, doc := range d.Documents {
		if !token(doc.Member) {
			return PackagePlan{}, Invalid
		}
		memberSlots[doc.Member] = true
	}
	if len(memberSlots) > MaxSCIPMembers {
		return PackagePlan{}, Capacity
	}
	if _, ok := targetOrder(d.Targets); !ok {
		return PackagePlan{}, Invalid
	}
	if !unitAcyclic(d.Units) {
		return PackagePlan{}, Invalid
	}
	referencedDocs := map[string]bool{}
	for _, u := range d.Units {
		if !owned[u.ID] {
			return PackagePlan{}, Invalid
		}
		for _, id := range u.Imports {
			if units[id].ID == "" || id == u.ID || (!u.Test && units[id].Test) {
				return PackagePlan{}, Invalid
			}
		}
		for _, p := range u.Documents {
			doc, ok := docs[p]
			if !ok || doc.Unit != u.ID {
				return PackagePlan{}, Invalid
			}
			referencedDocs[p] = true
		}
	}
	if len(referencedDocs) != len(docs) {
		return PackagePlan{}, Invalid
	}
	raw, err = json.Marshal(d)
	if err != nil || len(raw) > MaxPlanBytes {
		return PackagePlan{}, Capacity
	}
	return PackagePlan{d, raw, hash(raw)}, nil
}
func duplicates[S ~[]E, E comparable](v S) bool {
	for i := 1; i < len(v); i++ {
		if v[i] == v[i-1] {
			return true
		}
	}
	return false
}
func DecodePackagePlan(ctx context.Context, parent Admission, raw []byte, expected string) (PackagePlan, error) {
	if len(raw) > MaxPlanBytes || !digest(expected) || hash(raw) != expected {
		return PackagePlan{}, Invalid
	}
	var d PackagePlanDefinition
	if err := decode(raw, MaxPlanBytes, &d); err != nil {
		return PackagePlan{}, err
	}
	p, err := SealPackagePlan(ctx, parent, d)
	if err != nil {
		return PackagePlan{}, err
	}
	if !bytes.Equal(p.raw, raw) {
		return PackagePlan{}, Invalid
	}
	return p, nil
}

type UnitState string

const (
	UnitComplete    UnitState = "complete"
	UnitFailed      UnitState = "failed"
	UnitUnsupported UnitState = "unsupported"
	UnitExcluded    UnitState = "predeclared-excluded"
)

type UnitOutcome struct {
	Unit  PackageUnitID `json:"unit"`
	State UnitState     `json:"state"`
}
type TargetOutcome struct {
	Target string    `json:"target"`
	State  UnitState `json:"state"`
}
type ContentReference struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
	Bytes  int    `json:"bytes"`
}
type MemberInput struct {
	Name string
	SCIP []byte
}
type SCIPMember struct {
	Slot string `json:"slot"`
	ContentReference
	Documents   int `json:"documents"`
	Occurrences int `json:"occurrences"`
	Symbols     int `json:"symbols"`
}
type DocumentRoute struct {
	Path   string        `json:"path"`
	Unit   PackageUnitID `json:"unit"`
	Member string        `json:"member"`
}
type SymbolRoute struct {
	Symbol        string   `json:"symbol"`
	LocalDocument string   `json:"local_document"`
	Members       []string `json:"members"`
}
type AttemptManifest struct {
	SCIPGo    *SCIPGoAdapterReceipt `json:"scip_go,omitempty"`
	Schema    string                `json:"schema"`
	Request   Request               `json:"request"`
	Units     []UnitOutcome         `json:"units"`
	Targets   []TargetOutcome       `json:"targets"`
	Members   []SCIPMember          `json:"members"`
	Documents []DocumentRoute       `json:"documents"`
	Generated []GeneratedDocument   `json:"generated"`
	Complete  bool                  `json:"complete"`
}
type BundleRoot struct {
	Schema    string            `json:"schema"`
	Binding   GenerationBinding `json:"binding"`
	Attempt   ContentReference  `json:"attempt"`
	Documents ContentReference  `json:"documents"`
	Symbols   ContentReference  `json:"symbols"`
}

// Bundle is an immutable verified result. Incomplete attempts have no root and
// cannot propose a current pointer. Byte accessors always return private copies.
type Bundle struct {
	attempt  []byte
	root     []byte
	contents map[string][]byte
	binding  GenerationBinding
}

func (b Bundle) AttemptBytes() []byte { return bytes.Clone(b.attempt) }
func (b Bundle) RootBytes() []byte    { return bytes.Clone(b.root) }
func (b Bundle) RootDigest() string {
	if len(b.root) == 0 {
		return ""
	}
	return hash(b.root)
}
func (b Bundle) Content(name string) []byte { return bytes.Clone(b.contents[name]) }
func (b Bundle) Names() []string {
	out := make([]string, 0, len(b.contents))
	for k := range b.contents {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
func generationBinding(a Admission) GenerationBinding {
	return GenerationBinding{Source: a.request.Source, RequestDigest: a.digest, ProfileDigest: a.request.ProfileDigest, ToolsDigest: a.request.ToolsDigest, PlanDigest: a.request.PlanDigest}
}

// BuildBundle verifies one complete terminal attempt, independent of its order.
// It derives routes from verified members; callers cannot propose routing facts.
// A failed/unsupported required unit yields only a non-current attempt manifest.
func BuildBundle(ctx context.Context, a Admission, p PackagePlan, outcomes []UnitOutcome, members []MemberInput, generated map[string][]byte) (Bundle, error) {
	return buildBundle(ctx, a, p, outcomes, members, generated, nil)
}

func buildBundle(ctx context.Context, a Admission, p PackagePlan, outcomes []UnitOutcome, members []MemberInput, generated map[string][]byte, receipt *SCIPGoAdapterReceipt) (Bundle, error) {
	if err := ctx.Err(); err != nil {
		return Bundle{}, err
	}
	if a.digest == "" || a.request.Action != Execute || p.digest == "" || a.request.PlanDigest != p.digest || a.request.ParentRequestDigest != p.definition.ParentRequestDigest {
		return Bundle{}, Stale
	}
	parentRequest := a.request
	parentRequest.Action, parentRequest.ParentRequestDigest, parentRequest.PlanDigest = Plan, "", ""
	if identity(parentRequest) != p.definition.ParentRequestDigest || identity(a.request) != a.digest {
		return Bundle{}, Stale
	}
	if len(outcomes) != len(p.definition.Units) || len(members) > MaxSCIPMembers || len(generated) > MaxBundleDocuments {
		return Bundle{}, Invalid
	}
	// Recheck the policy independently of plan sealing: retained plans produced
	// before this fence cannot turn an omit profile into generated authority.
	if !a.profile.permitsGenerated() && (len(generated) != 0 || slices.ContainsFunc(p.definition.Documents, func(d PlannedDocument) bool { return d.Generated })) {
		return Bundle{}, Unsupported
	}
	states := map[PackageUnitID]UnitState{}
	for _, o := range outcomes {
		if !validPackageUnit(o.Unit) || states[o.Unit] != "" {
			return Bundle{}, Invalid
		}
		switch o.State {
		case UnitComplete, UnitFailed, UnitUnsupported, UnitExcluded:
		default:
			return Bundle{}, Invalid
		}
		states[o.Unit] = o.State
	}
	manifest := AttemptManifest{Schema: AttemptSchema, Request: a.request, Units: []UnitOutcome{}, Targets: []TargetOutcome{}, Members: []SCIPMember{}, Documents: []DocumentRoute{}, Generated: []GeneratedDocument{}, Complete: true}
	if receipt != nil {
		if err := validateSCIPGoReceipt(ctx, a.profile, receipt); err != nil {
			return Bundle{}, err
		}
		canonicalReceipt := canonicalSCIPGoReceipt(*receipt)
		manifest.Schema, manifest.SCIPGo = SCIPGoAttemptSchema, &canonicalReceipt
	}
	for _, u := range p.definition.Units {
		state := states[u.ID]
		if state == "" {
			return Bundle{}, Invalid
		}
		// The frozen reduced profile predeclares only test-unit exclusion. Required
		// generated documents cannot become post-hoc exclusions after tool failure.
		if u.Test {
			if !a.profile.definition.Config.SkipTests || state != UnitExcluded {
				return Bundle{}, Invalid
			}
		} else if state == UnitExcluded {
			return Bundle{}, Invalid
		}
		if state == UnitFailed || state == UnitUnsupported {
			manifest.Complete = false
		}
		manifest.Units = append(manifest.Units, UnitOutcome{u.ID, state})
	}
	// A completed load cannot depend on a failed, unsupported or excluded load.
	for _, u := range p.definition.Units {
		if states[u.ID] == UnitComplete {
			for _, id := range u.Imports {
				if states[id] != UnitComplete {
					return Bundle{}, Invalid
				}
			}
		}
	}
	order, ok := targetOrder(p.definition.Targets)
	if !ok {
		return Bundle{}, Invalid
	}
	targetStates := map[string]UnitState{}
	for _, index := range order {
		t := p.definition.Targets[index]
		state := UnitComplete
		if t.Test {
			state = UnitExcluded
		} else {
			combine := func(other UnitState) {
				if other == UnitFailed || (other == UnitUnsupported && state != UnitFailed) {
					state = other
				}
			}
			for _, id := range t.Units {
				combine(states[id])
			}
			for _, id := range t.Dependencies {
				combine(targetStates[id])
			}
		}
		targetStates[t.ID] = state
	}
	for _, t := range p.definition.Targets {
		manifest.Targets = append(manifest.Targets, TargetOutcome{t.ID, targetStates[t.ID]})
	}
	plannedDocs := map[string]PlannedDocument{}
	plannedSlots := map[string]bool{}
	seenSlots := map[string]bool{}
	for _, d := range p.definition.Documents {
		plannedDocs[d.Path] = d
		plannedSlots[d.Member] = true
	}
	b := Bundle{contents: map[string][]byte{}, binding: generationBinding(a)}
	seenDocs := map[string]bool{}
	symbolMembers := map[string]map[string]bool{}
	symbols := map[string]SymbolRoute{}
	globalInfo := map[string][]byte{}
	metadata := []byte(nil)
	var total int
	for _, input := range members {
		raw := input.SCIP
		if !plannedSlots[input.Name] || seenSlots[input.Name] {
			return Bundle{}, Invalid
		}
		seenSlots[input.Name] = true
		if err := ctx.Err(); err != nil {
			return Bundle{}, err
		}
		if len(raw) > MaxSCIPAggregateBytes-total {
			return Bundle{}, Capacity
		}
		total += len(raw)
		canonical, err := CanonicalSCIP(ctx, raw)
		if err != nil {
			return Bundle{}, err
		}
		name := "members/" + strings.TrimPrefix(hash(canonical), "sha256:") + ".scip"
		if b.contents[name] != nil {
			return Bundle{}, Invalid
		}
		var index scip.Index
		if proto.Unmarshal(canonical, &index) != nil {
			return Bundle{}, Invalid
		}
		if len(index.Documents) == 0 || index.Metadata.ToolInfo.Name != "scip-go" || index.Metadata.ToolInfo.Version != a.profile.definition.Tools.Indexer.Version {
			return Bundle{}, Invalid
		}
		md, err := proto.MarshalOptions{Deterministic: true}.Marshal(index.Metadata)
		if err != nil {
			return Bundle{}, Invalid
		}
		if metadata != nil && !bytes.Equal(metadata, md) {
			return Bundle{}, Invalid
		}
		metadata = md
		member := SCIPMember{Slot: input.Name, ContentReference: ContentReference{name, hash(canonical), len(canonical)}, Documents: len(index.Documents), Symbols: len(index.ExternalSymbols)}
		addSymbol := func(symbol, path string) error {
			if symbol == "" {
				return nil
			}
			local := ""
			if strings.HasPrefix(symbol, "local ") {
				local = path
				if path == "" {
					return Invalid
				}
			}
			key := local + "\x00" + symbol
			if symbolMembers[key] == nil {
				if len(symbolMembers) >= MaxBundleSymbols {
					return Capacity
				}
				symbolMembers[key] = map[string]bool{}
				symbols[key] = SymbolRoute{Symbol: symbol, LocalDocument: local}
			}
			symbolMembers[key][name] = true
			return nil
		}
		addInfo := func(info *scip.SymbolInformation, path string) error {
			if !strings.HasPrefix(info.Symbol, "local ") {
				encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(info)
				if err != nil {
					return Invalid
				}
				if prior, ok := globalInfo[info.Symbol]; ok && !bytes.Equal(prior, encoded) {
					return Invalid
				}
				globalInfo[info.Symbol] = encoded
			}
			if err := addSymbol(info.Symbol, path); err != nil {
				return err
			}
			for _, r := range info.Relationships {
				if err := addSymbol(r.Symbol, path); err != nil {
					return err
				}
			}
			return nil
		}
		for _, doc := range index.Documents {
			d, ok := plannedDocs[doc.RelativePath]
			if !ok || d.Member != input.Name || seenDocs[d.Path] || states[d.Unit] != UnitComplete {
				return Bundle{}, Invalid
			}
			seenDocs[d.Path] = true
			manifest.Documents = append(manifest.Documents, DocumentRoute{d.Path, d.Unit, name})
			member.Occurrences += len(doc.Occurrences)
			member.Symbols += len(doc.Symbols)
			for _, o := range doc.Occurrences {
				if err := addSymbol(o.Symbol, d.Path); err != nil {
					return Bundle{}, err
				}
			}
			for _, info := range doc.Symbols {
				if err := addInfo(info, d.Path); err != nil {
					return Bundle{}, err
				}
			}
		}
		for _, info := range index.ExternalSymbols {
			if err := addInfo(info, ""); err != nil {
				return Bundle{}, err
			}
		}
		b.contents[name] = canonical
		manifest.Members = append(manifest.Members, member)
	}
	for _, d := range p.definition.Documents {
		if states[d.Unit] == UnitComplete && !seenDocs[d.Path] {
			return Bundle{}, Invalid
		}
	}
	var generatedBytes int64
	for _, d := range p.definition.Documents {
		if !d.Generated || !seenDocs[d.Path] {
			continue
		}
		raw, ok := generated[d.Path]
		if !ok || int64(len(raw)) != d.Bytes || hash(raw) != d.Digest || d.Bytes > MaxGeneratedAggregateBytes-generatedBytes {
			return Bundle{}, Invalid
		}
		generatedBytes += d.Bytes
		gd := GeneratedDocument{Binding: b.binding, Unit: d.Unit, Path: d.Path, ProvenanceDigest: d.ProvenanceDigest, Bytes: d.Bytes, Digest: d.Digest}
		if err := VerifyGeneratedDocument(ctx, gd, b.binding, d.Unit, raw); err != nil {
			return Bundle{}, err
		}
		manifest.Generated = append(manifest.Generated, gd)
		b.contents[d.Path] = bytes.Clone(raw)
	}
	if len(manifest.Generated) != len(generated) {
		return Bundle{}, Invalid
	}
	sort.Slice(manifest.Members, func(i, j int) bool { return manifest.Members[i].Name < manifest.Members[j].Name })
	sort.Slice(manifest.Documents, func(i, j int) bool { return manifest.Documents[i].Path < manifest.Documents[j].Path })
	raw, err := json.Marshal(manifest)
	if err != nil || len(raw) > MaxAttemptBytes {
		return Bundle{}, Capacity
	}
	b.attempt = raw
	if !manifest.Complete {
		return b, nil
	}
	routes := make([]SymbolRoute, 0, len(symbols))
	for key, r := range symbols {
		r.Members = make([]string, 0, len(symbolMembers[key]))
		for name := range symbolMembers[key] {
			r.Members = append(r.Members, name)
		}
		slices.Sort(r.Members)
		routes = append(routes, r)
	}
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Symbol != routes[j].Symbol {
			return routes[i].Symbol < routes[j].Symbol
		}
		return routes[i].LocalDocument < routes[j].LocalDocument
	})
	addControl := func(kind string, v any, limit int) (ContentReference, error) {
		raw, err := json.Marshal(v)
		if err != nil || len(raw) > limit {
			return ContentReference{}, Capacity
		}
		ref := ContentReference{kind + "/" + strings.TrimPrefix(hash(raw), "sha256:") + ".json", hash(raw), len(raw)}
		b.contents[ref.Name] = raw
		return ref, nil
	}
	attempt, err := addControl("attempts", manifest, MaxAttemptBytes)
	if err != nil {
		return Bundle{}, err
	}
	docRoutes, err := addControl("documents", manifest.Documents, MaxRouteBytes)
	if err != nil {
		return Bundle{}, err
	}
	symRoutes, err := addControl("symbols", routes, MaxRouteBytes)
	if err != nil {
		return Bundle{}, err
	}
	root := BundleRoot{SCIPBundleSchema, b.binding, attempt, docRoutes, symRoutes}
	b.root, err = json.Marshal(root)
	if err != nil || len(b.root) > MaxRootBytes {
		return Bundle{}, Capacity
	}
	total = len(b.root)
	for _, raw := range b.contents {
		if len(raw) > MaxPublicationBytes-total {
			return Bundle{}, Capacity
		}
		total += len(raw)
	}
	return b, nil
}

// VerifyBundle re-derives every routing byte and descriptor rather than trusting
// a persisted route. Missing, extra, corrupt or divergent content fails closed.
func VerifyBundle(ctx context.Context, a Admission, p PackagePlan, attempt, root []byte, contents map[string][]byte) (Bundle, error) {
	if len(contents) > MaxSCIPMembers+MaxBundleDocuments+3 {
		return Bundle{}, Capacity
	}
	if err := scipGoAttemptDimensions(ctx, attempt); err != nil {
		return Bundle{}, err
	}
	var m AttemptManifest
	if err := decode(attempt, MaxAttemptBytes, &m); err != nil {
		return Bundle{}, err
	}
	if len(m.Members) > MaxSCIPMembers || len(m.Units) > MaxBundleUnits || len(m.Targets) > MaxBundleTargets || len(m.Documents) > MaxBundleDocuments || len(m.Generated) > MaxBundleDocuments {
		return Bundle{}, Capacity
	}
	validSchema := m.Schema == AttemptSchema && m.SCIPGo == nil || m.Schema == SCIPGoAttemptSchema && m.SCIPGo != nil
	if !validSchema || m.Request != a.request {
		return Bundle{}, Stale
	}
	members := make([]MemberInput, 0, len(m.Members))
	for _, member := range m.Members {
		raw, ok := contents[member.Name]
		if !ok {
			return Bundle{}, Invalid
		}
		members = append(members, MemberInput{member.Slot, raw})
	}
	generated := map[string][]byte{}
	for _, doc := range m.Generated {
		raw, ok := contents[doc.Path]
		if !ok {
			return Bundle{}, Invalid
		}
		generated[doc.Path] = raw
	}
	b, err := buildBundle(ctx, a, p, m.Units, members, generated, m.SCIPGo)
	if err != nil {
		return Bundle{}, err
	}
	if !bytes.Equal(attempt, b.attempt) || !bytes.Equal(root, b.root) || len(contents) != len(b.contents) {
		return Bundle{}, Invalid
	}
	for name, want := range b.contents {
		if !bytes.Equal(contents[name], want) {
			return Bundle{}, Invalid
		}
	}
	return b, nil
}

// PublicationPointer is the sole current authority. The durable caller must
// compare the complete expected pointer and current Admission in one fenced
// transaction; this pure transition does not itself mutate store or filesystem.
type PublicationPointer struct {
	Epoch      uint64            `json:"epoch"`
	Binding    GenerationBinding `json:"binding"`
	RootDigest string            `json:"root_digest"`
}

func NextPublication(ctx context.Context, current, expected PublicationPointer, authority Admission, b Bundle) (PublicationPointer, error) {
	if err := ctx.Err(); err != nil {
		return current, err
	}
	if current != expected || authority.digest == "" || b.binding != generationBinding(authority) || len(b.root) == 0 {
		return current, Stale
	}
	if authority.Purpose() != Publish {
		return current, Invalid
	}
	if current.Epoch == math.MaxUint64 {
		return current, Capacity
	}
	return PublicationPointer{current.Epoch + 1, b.binding, b.RootDigest()}, nil
}

// Check typed caller dimensions before making an encoded copy. The wire decoder
// independently caps bytes before allocating its bounded JSON representation.
func planDimensions(d PackagePlanDefinition) bool {
	if len(d.Targets) > MaxBundleTargets || len(d.Units) > MaxBundleUnits || len(d.Documents) > MaxBundleDocuments {
		return false
	}
	textBytes, edges := 0, 0
	text := func(s string, limit int) bool {
		if len(s) > limit || len(s) > MaxPlanBytes-textBytes {
			return false
		}
		textBytes += len(s)
		return true
	}
	edge := func(n int) bool {
		if n > MaxBundleEdges-edges {
			return false
		}
		edges += n
		return true
	}
	if !text(d.Schema, 64) || !text(d.ParentRequestDigest, 71) {
		return false
	}
	for _, t := range d.Targets {
		if !text(t.ID, 71) || !edge(len(t.Dependencies)+len(t.Units)) {
			return false
		}
		for _, id := range t.Dependencies {
			if !text(id, 71) {
				return false
			}
		}
		for _, id := range t.Units {
			if !text(string(id), 84) {
				return false
			}
		}
	}
	for _, u := range d.Units {
		if !text(string(u.ID), 84) || !edge(len(u.Imports)+len(u.Documents)) {
			return false
		}
		for _, id := range u.Imports {
			if !text(string(id), 84) {
				return false
			}
		}
		for _, name := range u.Documents {
			if !text(name, 512) {
				return false
			}
		}
	}
	for _, doc := range d.Documents {
		if !text(doc.Member, 64) || !text(doc.Path, 512) || !text(string(doc.Unit), 84) || !text(doc.Digest, 71) || !text(doc.ProvenanceDigest, 71) {
			return false
		}
	}
	return true
}

// Topological ordering visits each configured edge once; cycles never become
// fabricated complete outcomes. The returned order has dependencies first.
func targetOrder(targets []PlannedTarget) ([]int, bool) {
	index := make(map[string]int, len(targets))
	for i, t := range targets {
		index[t.ID] = i
	}
	remaining := make([]int, len(targets))
	parents := make([][]int, len(targets))
	order := make([]int, 0, len(targets))
	for i, t := range targets {
		remaining[i] = len(t.Dependencies)
		for _, id := range t.Dependencies {
			j, ok := index[id]
			if !ok {
				return nil, false
			}
			parents[j] = append(parents[j], i)
		}
		if remaining[i] == 0 {
			order = append(order, i)
		}
	}
	for head := 0; head < len(order); head++ {
		for _, parent := range parents[order[head]] {
			remaining[parent]--
			if remaining[parent] == 0 {
				order = append(order, parent)
			}
		}
	}
	return order, len(order) == len(targets)
}
func unitAcyclic(units []PlannedUnit) bool {
	// Reuse the bounded graph walk; these temporary rows contain only unit IDs
	// and import edges, not any source or encoded content.
	targets := make([]PlannedTarget, len(units))
	for i, u := range units {
		targets[i].ID = string(u.ID)
		for _, id := range u.Imports {
			targets[i].Dependencies = append(targets[i].Dependencies, string(id))
		}
	}
	_, ok := targetOrder(targets)
	return ok
}
