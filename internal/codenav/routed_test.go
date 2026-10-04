package codenav

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"
)

func routedHash(b []byte) string { return fmt.Sprintf("sha256:%x", sha256.Sum256(b)) }
func routedJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return b
}

type routedTestMetadata struct{ routing typedindex.Routing }

func (m *routedTestMetadata) Routing() typedindex.Routing { return m.routing }
func (m *routedTestMetadata) AccountedBytes() int64       { return m.routing.AccountedBytes() + 256 }

type routedTestResolver struct {
	mu                                  sync.Mutex
	binding                             RoutedBinding
	metadata                            *routedTestMetadata
	content                             map[string][]byte
	opens, cold, pins, reads, generated int
	mutate                              bool
	selectAfter, resolves               int
	failReads                           int
}

func (r *routedTestResolver) ResolveRoutedIndex(ctx context.Context, _, _ string) (RoutedBinding, error) {
	if e := ctx.Err(); e != nil {
		return RoutedBinding{}, e
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resolves++
	if r.selectAfter > 0 && r.resolves >= r.selectAfter {
		r.binding.Selected = true
	}
	return r.binding, nil
}
func (r *routedTestResolver) OpenRoutedIndex(ctx context.Context, b RoutedBinding, m RoutedMetadata) (RoutedReader, RoutedMetadata, error) {
	if e := ctx.Err(); e != nil {
		return nil, nil, e
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if b != r.binding {
		return nil, nil, ErrBindingChanged
	}
	r.opens++
	r.pins++
	if m == nil {
		r.cold++
	}
	return &routedTestReader{r: r}, r.metadata, nil
}

type routedTestReader struct {
	r      *routedTestResolver
	closed bool
}

func (p *routedTestReader) Close() error {
	p.r.mu.Lock()
	defer p.r.mu.Unlock()
	if !p.closed {
		p.r.pins--
		p.closed = true
	}
	return nil
}
func (p *routedTestReader) ReadMember(ctx context.Context, n string) ([]byte, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	p.r.mu.Lock()
	defer p.r.mu.Unlock()
	p.r.reads++
	if p.r.failReads > 0 {
		p.r.failReads--
		return nil, errors.New("transient physical read")
	}
	if p.r.mutate {
		p.r.binding.Identity = routedHash([]byte("replacement"))
		p.r.mutate = false
	}
	return slices.Clone(p.r.content[n]), nil
}
func (p *routedTestReader) ReadGenerated(ctx context.Context, n string) ([]byte, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	p.r.mu.Lock()
	defer p.r.mu.Unlock()
	p.r.generated++
	return slices.Clone(p.r.content[n]), nil
}

// Build real sealed multi-member controls, with exactly the source bytes that
// the ordinary Git oracle reads. Only the physical reader is neutral here;
// Linux workspace tests separately exercise actual custody and kernel pins.
func routedFixture(t *testing.T, f *fixture, index *scip.Index, generated map[string][]byte) *routedTestResolver {
	t.Helper()
	ctx := t.Context()
	tool := typedindex.Tool{Version: "0.2.7", Digest: routedHash([]byte("tool"))}
	pd := typedindex.ProfileDefinition{Schema: typedindex.GeneratedProfileSchema, Name: "reader", Provider: typedindex.ProviderID, Tools: typedindex.Tools{Bazel: tool, RulesGo: tool, Go: tool, Driver: tool, Indexer: tool, Planner: tool, Launcher: tool}, Config: typedindex.GeneratedConfig(), Policy: typedindex.MeasuredPolicy(), BundleDigest: routedHash([]byte("bundle")), ImageDigest: routedHash([]byte("image"))}
	profile, e := typedindex.DecodeProfile(ctx, routedJSON(t, pd))
	if e != nil {
		t.Fatal(e)
	}
	source := typedindex.Source{Repository: fixtureRepo, Incarnation: "repo1", Generation: routedHash([]byte("generation")), Commit: f.revision}
	plan := typedindex.PackagePlanDefinition{Schema: typedindex.PackagePlanSchema}
	var outcomes []typedindex.UnitOutcome
	var members []typedindex.MemberInput
	for i, doc := range index.Documents {
		unit, e := typedindex.NewPackageUnitID(routedHash([]byte(fmt.Sprint(i))))
		if e != nil {
			t.Fatal(e)
		}
		slot := fmt.Sprintf("member%d", i)
		body, gen := generated[doc.RelativePath]
		if !gen {
			body, e = os.ReadFile(filepath.Join(f.origin, doc.RelativePath))
			if e != nil {
				t.Fatal(e)
			}
		}
		d := typedindex.PlannedDocument{Path: doc.RelativePath, Member: slot, Unit: unit, Bytes: int64(len(body)), Digest: routedHash(body), Generated: gen}
		if gen {
			d.ProvenanceDigest = routedHash([]byte("generator"))
		}
		plan.Documents = append(plan.Documents, d)
		plan.Units = append(plan.Units, typedindex.PlannedUnit{ID: unit, Imports: []typedindex.PackageUnitID{}, Documents: []string{doc.RelativePath}})
		plan.Targets = append(plan.Targets, typedindex.PlannedTarget{ID: routedHash([]byte(slot)), Dependencies: []string{}, Units: []typedindex.PackageUnitID{unit}})
		outcomes = append(outcomes, typedindex.UnitOutcome{Unit: unit, State: typedindex.UnitComplete})
		single := &scip.Index{Metadata: &scip.Metadata{ToolInfo: &scip.ToolInfo{Name: "scip-go", Version: "0.2.7"}, ProjectRoot: "file:///workspace", TextDocumentEncoding: scip.TextEncoding_UTF8}, Documents: []*scip.Document{proto.Clone(doc).(*scip.Document)}}
		raw, e := proto.Marshal(single)
		if e != nil {
			t.Fatal(e)
		}
		members = append(members, typedindex.MemberInput{Name: slot, SCIP: raw})
	}
	slices.SortFunc(plan.Targets, func(a, b typedindex.PlannedTarget) int { return strings.Compare(a.ID, b.ID) })
	universe := routedHash(routedJSON(t, plan.Targets))
	auth := typedindex.Authority{Enabled: true, Administrator: true, Source: source, Profile: typedindex.Epoch{Number: 1, Digest: profile.Digest()}, UniverseDigest: universe}
	request := typedindex.NewRequest(source, profile, 1, universe, "reader")
	parent, e := typedindex.Admit(ctx, auth, profile, routedJSON(t, request))
	if e != nil {
		t.Fatal(e)
	}
	plan.ParentRequestDigest = parent.Digest()
	sealed, e := typedindex.SealPackagePlan(ctx, parent, plan)
	if e != nil {
		t.Fatal(e)
	}
	next, e := typedindex.PlannedSuccessor(ctx, parent, sealed.Digest())
	if e != nil {
		t.Fatal(e)
	}
	auth.ParentRequestDigest, auth.PlanDigest = parent.Digest(), sealed.Digest()
	execution, e := typedindex.Admit(ctx, auth, profile, routedJSON(t, next))
	if e != nil {
		t.Fatal(e)
	}
	bundle, e := typedindex.BuildBundle(ctx, execution, sealed, outcomes, members, generated)
	if e != nil {
		t.Fatal(e)
	}
	var root typedindex.BundleRoot
	if e = json.Unmarshal(bundle.RootBytes(), &root); e != nil {
		t.Fatal(e)
	}
	routing, e := typedindex.DecodeRouting(ctx, execution, bundle.RootDigest(), bundle.RootBytes(), bundle.AttemptBytes(), bundle.Content(root.Documents.Name), bundle.Content(root.Symbols.Name))
	if e != nil {
		t.Fatal(e)
	}
	content := map[string][]byte{}
	for _, name := range bundle.Names() {
		content[name] = bundle.Content(name)
	}
	return &routedTestResolver{binding: RoutedBinding{Selected: true, Identity: routedHash([]byte("custody")), RootDigest: bundle.RootDigest(), Source: source}, metadata: &routedTestMetadata{routing}, content: content}
}
func compareRouted(t *testing.T, a, b *Service, q Query) {
	t.Helper()
	da, ea := a.Definition(t.Context(), q)
	db, eb := b.Definition(t.Context(), q)
	if !reflect.DeepEqual(da, db) || !reflect.DeepEqual(ea, eb) {
		t.Fatalf("definition parity: %#v %v / %#v %v", da, ea, db, eb)
	}
	ra, ea := a.References(t.Context(), q)
	rb, eb := b.References(t.Context(), q)
	if !reflect.DeepEqual(ra, rb) || !reflect.DeepEqual(ea, eb) {
		t.Fatalf("references parity: %#v %v / %#v %v", ra, ea, rb, eb)
	}
	ha, ea := a.Hover(t.Context(), q)
	hb, eb := b.Hover(t.Context(), q)
	if !reflect.DeepEqual(ha, hb) || !reflect.DeepEqual(ea, eb) {
		t.Fatalf("hover parity: %#v %v / %#v %v", ha, ea, hb, eb)
	}
}
func TestRoutedCrossMemberOracleAndWarmCache(t *testing.T) {
	f := newFixture(t, true)
	r := routedFixture(t, f, readFixtureIndex(t), nil)
	ordinary := New(Options{DataDir: f.dataDir})
	routed := New(Options{DataDir: f.dataDir, RoutedResolver: r})
	for _, tc := range []struct {
		encoding  PositionEncoding
		character int32
	}{{EncodingUTF8, 31}, {EncodingUTF16, 29}, {EncodingUTF32, 28}} {
		compareRouted(t, ordinary, routed, Query{Repo: fixtureRepo, Revision: f.revision, Path: "use/use.go", Line: 1, Character: tc.character, Encoding: tc.encoding})
	}
	compareRouted(t, ordinary, routed, Query{Repo: fixtureRepo, Revision: f.revision, Path: "lib/rocket.go", Line: 1, Character: 6})
	compareRouted(t, ordinary, routed, Query{Repo: fixtureRepo, Revision: f.revision, Path: "absent.go"})
	a, e := routed.Ingest(t.Context(), fixtureRepo, f.revision)
	if e != nil || !a.Available || a.Documents != 2 {
		t.Fatal(a, e)
	}
	if r.reads != 2 || r.cold != 1 || r.pins != 0 {
		t.Fatalf("cost: %+v", r)
	}
	if len(routed.routed.entries) != 3 {
		t.Fatal("members not separately cached")
	}
	if e := routed.Remove(fixtureRepo); e != nil {
		t.Fatal(e)
	}
	if len(routed.routed.entries) != 0 {
		t.Fatal("idle invalidation")
	}
}
func TestRoutedFailureCurrentAndBudget(t *testing.T) {
	for _, kind := range []string{"changed", "corrupt", "budget", "unavailable", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t, true)
			r := routedFixture(t, f, readFixtureIndex(t), nil)
			opts := Options{DataDir: f.dataDir, RoutedResolver: r}
			switch kind {
			case "changed":
				r.mutate = true
			case "corrupt":
				d, _ := r.metadata.routing.Document("use/use.go")
				r.content[d.Member][0] ^= 1
			case "budget":
				opts.MaxRoutedCacheBytes = 1
			case "unavailable":
				r.binding.RootDigest = ""
			}
			s := New(opts)
			ctx := t.Context()
			if kind == "cancel" {
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			}
			result, e := s.Definition(ctx, Query{Repo: fixtureRepo, Revision: f.revision, Path: "use/use.go", Line: 1, Character: 29})
			if kind == "unavailable" {
				if e != nil || result.Available {
					t.Fatal(result, e)
				}
			} else if e == nil {
				t.Fatal("refusal absent")
			}
			if kind == "changed" && !errors.Is(e, ErrBindingChanged) {
				t.Fatal(e)
			}
			if r.pins != 0 || len(s.routed.queries) != 0 {
				t.Fatal("leaked pin/query")
			}
			if kind == "corrupt" {
				reads := r.reads
				_, _ = s.Definition(ctx, Query{Repo: fixtureRepo, Revision: f.revision, Path: "use/use.go", Line: 1, Character: 29})
				if reads != r.reads {
					t.Fatal("negative member not cached")
				}
			}
		})
	}
}

func TestRoutedGeneratedUnicodeOracle(t *testing.T) {
	f := newFixture(t, true)
	index := readFixtureIndex(t)
	unit, _ := typedindex.NewPackageUnitID(routedHash([]byte("1")))
	name, e := typedindex.GeneratedPath(unit, "use.go")
	if e != nil {
		t.Fatal(e)
	}
	body, e := os.ReadFile(filepath.Join(f.origin, "use/use.go"))
	if e != nil {
		t.Fatal(e)
	}
	index.Documents[1].RelativePath = name
	if e = os.MkdirAll(filepath.Dir(filepath.Join(f.origin, name)), 0755); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(f.origin, name), body, 0644); e != nil {
		t.Fatal(e)
	}
	writeIndex(t, f.origin, index)
	f.revision = f.commit(t, "generated oracle")
	f.fetch(t)
	r := routedFixture(t, f, index, map[string][]byte{name: body})
	s := New(Options{DataDir: f.dataDir, RoutedResolver: r})
	oracle := New(Options{DataDir: f.dataDir})
	for _, tc := range []struct {
		encoding  PositionEncoding
		character int32
	}{{EncodingUTF8, 31}, {EncodingUTF16, 29}, {EncodingUTF32, 28}} {
		compareRouted(t, oracle, s, Query{Repo: fixtureRepo, Revision: f.revision, Path: name, Line: 1, Character: tc.character, Encoding: tc.encoding})
	}
	compareRouted(t, oracle, s, Query{Repo: fixtureRepo, Revision: f.revision, Path: "lib/rocket.go", Line: 1, Character: 6})
	if r.generated == 0 || r.reads != 2 || r.pins != 0 {
		t.Fatal("generated source path/cost", r.generated, r.reads, r.pins)
	}
}

// NewRoutedEvidenceFixture bridges the external test package because extract
// already imports codenav. Generated bytes exist only in the sealed bundle;
// the physical reader is modeled, with native custody covered separately.
// Costs returns resolve/open/member/generated/pin counts.
func NewRoutedEvidenceFixture(t *testing.T, posture string) (*Service, Query, string, []byte, func() [5]int) {
	t.Helper()
	f := newFixture(t, false)
	index := readFixtureIndex(t)
	var committed []byte
	var err error
	switch posture {
	case "absent":
	case "valid":
		committed, err = proto.Marshal(index)
	case "corrupt":
		committed = []byte("not SCIP")
	default:
		t.Fatalf("unknown committed SCIP posture %q", posture)
	}
	if err != nil {
		t.Fatal(err)
	}
	if committed != nil {
		if err = os.WriteFile(filepath.Join(f.origin, DefaultIndexPath), committed, 0644); err != nil {
			t.Fatal(err)
		}
		f.revision = f.commit(t, posture+" committed SCIP")
		f.fetch(t)
	}
	unit, err := typedindex.NewPackageUnitID(routedHash([]byte("1")))
	if err != nil {
		t.Fatal(err)
	}
	name, err := typedindex.GeneratedPath(unit, "use.go")
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(f.origin, "use/use.go"))
	if err != nil {
		t.Fatal(err)
	}
	index.Documents[1].RelativePath = name
	r := routedFixture(t, f, index, map[string][]byte{name: body})
	costs := func() [5]int {
		r.mu.Lock()
		defer r.mu.Unlock()
		return [5]int{r.resolves, r.opens, r.reads, r.generated, r.pins}
	}
	query := Query{Repo: fixtureRepo, Revision: f.revision, Path: name, Line: 1, Character: 29}
	return New(Options{DataDir: f.dataDir, RoutedResolver: r}), query, f.dataDir, committed, costs
}

func TestRoutedOneHopSharedLibraryAndTruncation(t *testing.T) {
	f := newFixture(t, true)
	symbols := []string{"scip-go gomod example.test v1 A#", "scip-go gomod example.test v1 B#", "scip-go gomod example.test v1 C#", "scip-go gomod example.test v1 D#"}
	index := &scip.Index{Metadata: &scip.Metadata{ToolInfo: &scip.ToolInfo{Name: "scip-go", Version: "0.2.7"}, TextDocumentEncoding: scip.TextEncoding_UTF8}}
	for i, symbol := range symbols {
		name := fmt.Sprintf("member%d.go", i)
		body := "x\n"
		count := 1
		if i == 1 {
			count = MaxReferenceLocations + 10
			body = strings.Repeat("x\n", count)
		}
		if e := os.WriteFile(filepath.Join(f.origin, name), []byte(body), 0644); e != nil {
			t.Fatal(e)
		}
		doc := &scip.Document{RelativePath: name, PositionEncoding: scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart, Symbols: []*scip.SymbolInformation{{Symbol: symbol, Documentation: []string{fmt.Sprint(i)}}}}
		for j := 0; j < count; j++ {
			role := int32(0)
			if j == 0 {
				role = 1
			}
			doc.Occurrences = append(doc.Occurrences, &scip.Occurrence{TypedRange: &scip.Occurrence_SingleLineRange{SingleLineRange: &scip.SingleLineRange{Line: int32(j), StartCharacter: 0, EndCharacter: 1}}, Symbol: symbol, SymbolRoles: role})
		}
		if i < 2 {
			doc.Symbols[0].Relationships = []*scip.Relationship{{Symbol: symbols[i+1], IsReference: true, IsDefinition: true}}
		}
		index.Documents = append(index.Documents, doc)
	}
	writeIndex(t, f.origin, index)
	f.revision = f.commit(t, "one hop shared library")
	f.fetch(t)
	r := routedFixture(t, f, index, nil)
	s := New(Options{DataDir: f.dataDir, RoutedResolver: r})
	oracle := New(Options{DataDir: f.dataDir})
	q := Query{Repo: fixtureRepo, Revision: f.revision, Path: "member0.go", Line: 0, Character: 0}
	compareRouted(t, oracle, s, q)
	refs, e := s.References(t.Context(), q)
	if e != nil || !refs.Truncated || len(refs.Locations) != MaxReferenceLocations {
		t.Fatal(len(refs.Locations), refs.Truncated, e)
	}
	for _, loc := range refs.Locations {
		if loc.Path == "member2.go" {
			t.Fatal("transitive relationship leaked")
		}
	}
	if r.reads != 2 {
		t.Fatalf("wanted direct route members 0,1 but no transitive or unrelated member: %d", r.reads)
	}
	compareRouted(t, oracle, s, Query{Repo: fixtureRepo, Revision: f.revision, Path: "member1.go", Line: 0, Character: 0})
}
func TestRoutedCacheActiveInvalidationAndQueryBound(t *testing.T) {
	c := newRoutedCache(Options{MaxRoutedCacheBytes: 100, MaxRoutedCacheEntries: 2, MaxRoutedQueries: 100})
	a := &routedItem{key: "a", repo: "r", bytes: 60}
	if e := c.put(a); e != nil {
		t.Fatal(e)
	}
	if e := c.put(&routedItem{key: "b", bytes: 60}); !errors.Is(e, ErrCacheBudget) {
		t.Fatal("active allocation evicted", e)
	}
	c.remove("r")
	if c.get("a") != nil || c.bytes != 60 {
		t.Fatal("active invalidation discarded accounting")
	}
	c.release([]*routedItem{a})
	if c.bytes != 0 || len(c.entries) != 0 {
		t.Fatal("retired retained")
	}
	if cap(c.queries) != 16 {
		t.Fatal("query bound")
	}
	f := newFixture(t, true)
	r := routedFixture(t, f, readFixtureIndex(t), nil)
	s := New(Options{DataDir: f.dataDir, RoutedResolver: r, MaxRoutedQueries: 2})
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Go(func() {
			_, e := s.Definition(t.Context(), Query{Repo: fixtureRepo, Revision: f.revision, Path: "use/use.go", Line: 1, Character: 29})
			errs <- e
		})
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	if r.reads != 2 || r.cold != 1 || r.pins != 0 || len(s.routed.queries) != 0 {
		t.Fatal("concurrent cost/custody", r.reads, r.cold, r.pins)
	}
	// A canceled waiter does not obtain another physical reader.
	s.routed.queries <- struct{}{}
	s.routed.queries <- struct{}{}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, e := s.Definition(ctx, Query{Repo: fixtureRepo, Revision: f.revision, Path: "use/use.go", Line: 1, Character: 29})
	if !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	<-s.routed.queries
	<-s.routed.queries
}

func TestRoutedExactCurrentEpochAndLocalSymbols(t *testing.T) {
	f := newFixture(t, true)
	index := readFixtureIndex(t)
	// Same local identifier in separate documents cannot route across members.
	for _, doc := range index.Documents {
		for _, o := range doc.Occurrences {
			o.Symbol = "local 0"
		}
		for _, s := range doc.Symbols {
			s.Symbol = "local 0"
		}
	}
	writeIndex(t, f.origin, index)
	f.revision = f.commit(t, "local symbols")
	f.fetch(t)
	r := routedFixture(t, f, index, nil)
	s := New(Options{DataDir: f.dataDir, RoutedResolver: r})
	oracle := New(Options{DataDir: f.dataDir})
	q := Query{Repo: fixtureRepo, Revision: f.revision, Path: "use/use.go", Line: 1, Character: 29}
	compareRouted(t, oracle, s, q)
	if r.reads != 1 {
		t.Fatal("local symbol crossed member", r.reads)
	}
	r.binding.Identity = routedHash([]byte("same root new custody epoch"))
	compareRouted(t, oracle, s, q)
	if r.reads != 2 || r.cold != 2 {
		t.Fatal("root-only cache reused old custody", r.reads, r.cold)
	}
}

func TestRoutedIngestLegacyFinalSelectionFence(t *testing.T) {
	for _, flip := range []bool{false, true} {
		t.Run(fmt.Sprint(flip), func(t *testing.T) {
			f := newFixture(t, true)
			r := routedFixture(t, f, readFixtureIndex(t), nil)
			r.binding.Selected = false
			if flip {
				r.selectAfter = 2
			}
			s := New(Options{DataDir: f.dataDir, RoutedResolver: r})
			result, e := s.Ingest(t.Context(), fixtureRepo, f.revision)
			if flip {
				if !errors.Is(e, ErrBindingChanged) || result.Available {
					t.Fatal(result, e)
				}
			} else if e != nil || !result.Available {
				t.Fatal(result, e)
			}
			if r.opens != 0 {
				t.Fatal("legacy path opened routed publication")
			}
		})
	}
}

func TestRoutedTransientPhysicalReadIsRetried(t *testing.T) {
	f := newFixture(t, true)
	r := routedFixture(t, f, readFixtureIndex(t), nil)
	r.failReads = 1
	s := New(Options{DataDir: f.dataDir, RoutedResolver: r})
	q := Query{Repo: fixtureRepo, Revision: f.revision, Path: "use/use.go", Line: 1, Character: 29}
	if _, e := s.Definition(t.Context(), q); e == nil {
		t.Fatal("missing physical failure")
	}
	result, e := s.Definition(t.Context(), q)
	if e != nil || result.Location == nil {
		t.Fatal("physical failure poisoned binding", result, e)
	}
	if r.reads != 3 || r.pins != 0 {
		t.Fatal("read retry/custody", r.reads, r.pins)
	}
}

func TestRoutedIngestRetainedOccurrenceParity(t *testing.T) {
	f := newFixture(t, true)
	index := readFixtureIndex(t)
	index.Documents[0].Occurrences = append(index.Documents[0].Occurrences, &scip.Occurrence{TypedRange: &scip.Occurrence_SingleLineRange{SingleLineRange: &scip.SingleLineRange{Line: 0, StartCharacter: 0, EndCharacter: 1}}})
	writeIndex(t, f.origin, index)
	f.revision = f.commit(t, "charged empty symbol occurrence")
	f.fetch(t)
	r := routedFixture(t, f, index, nil)
	s := New(Options{DataDir: f.dataDir, RoutedResolver: r})
	oracle := New(Options{DataDir: f.dataDir})
	got, e := s.Ingest(t.Context(), fixtureRepo, f.revision)
	want, we := oracle.Ingest(t.Context(), fixtureRepo, f.revision)
	if e != nil || we != nil || got != want {
		t.Fatal(got, e, want, we)
	}
	if r.reads != 2 {
		t.Fatal("force-load did not parse exact members", r.reads)
	}
}

func TestRoutedColdResultCannotRepopulateAfterRemove(t *testing.T) {
	c := newRoutedCache(Options{})
	old := c.epoch()
	c.remove("repo")
	if e := c.put(&routedItem{key: "old", repo: "repo", bytes: 100, generation: old}); !errors.Is(e, ErrBindingChanged) {
		t.Fatal("cold result crossed invalidation", e)
	}
	if len(c.entries) != 0 || c.bytes != 0 {
		t.Fatal("invalidation admitted stale allocation")
	}
	fresh := &routedItem{key: "new", repo: "repo", bytes: 100, generation: c.epoch()}
	if e := c.put(fresh); e != nil {
		t.Fatal(e)
	}
	c.release([]*routedItem{fresh})
}
