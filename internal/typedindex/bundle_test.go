package typedindex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"
)

type bundleFixtureData struct {
	parent, execution Admission
	plan              PackagePlan
	outcomes          []UnitOutcome
	members           []MemberInput
	generated         map[string][]byte
}

func bundleFixture(t *testing.T) bundleFixtureData {
	t.Helper()
	ctx := context.Background()
	profile, auth, request, _ := fixture(t)
	pd := profile.Definition()
	pd.Schema, pd.Config = GeneratedProfileSchema, GeneratedConfig()
	pd.Tools.Indexer.Version = "0.2.7"
	var profileErr error
	profile, profileErr = DecodeProfile(ctx, wire(t, pd))
	if profileErr != nil {
		t.Fatal(profileErr)
	}
	auth.Profile.Digest = profile.Digest()
	request = NewRequest(auth.Source, profile, auth.Profile.Number, auth.UniverseDigest, request.IdempotencyKey)
	first, _ := NewPackageUnitID(hash([]byte("package-a")))
	second, _ := NewPackageUnitID(hash([]byte("package-b")))
	generatedPath, _ := GeneratedPath(second, "command.pb.go")
	source := []byte("package command\ntype Command struct{}\n")
	d := PackagePlanDefinition{Schema: PackagePlanSchema, Targets: []PlannedTarget{{ID: hash([]byte("target-a")), Dependencies: []string{}, Units: []PackageUnitID{first}}, {ID: hash([]byte("target-b")), Dependencies: []string{}, Units: []PackageUnitID{second}}}, Units: []PlannedUnit{{ID: first, Imports: []PackageUnitID{}, Documents: []string{"a.go"}}, {ID: second, Imports: []PackageUnitID{first}, Documents: []string{generatedPath}}}, Documents: []PlannedDocument{{Path: "a.go", Member: "a", Unit: first, Bytes: 1, Digest: hash([]byte("a"))}, {Path: generatedPath, Member: "b", Unit: second, Bytes: int64(len(source)), Digest: hash(source), Generated: true, ProvenanceDigest: hash([]byte("protoc-command-action"))}}}
	slices.SortFunc(d.Targets, func(a, b PlannedTarget) int { return strings.Compare(a.ID, b.ID) })
	request.UniverseDigest = identity(d.Targets)
	auth.UniverseDigest = request.UniverseDigest
	parent := admit(t, profile, auth, request)
	d.ParentRequestDigest = parent.Digest()
	plan, err := SealPackagePlan(ctx, parent, d)
	if err != nil {
		t.Fatal(err)
	}
	successor, err := PlannedSuccessor(ctx, parent, plan.Digest())
	if err != nil {
		t.Fatal(err)
	}
	auth.ParentRequestDigest = parent.Digest()
	auth.PlanDigest = plan.Digest()
	execution := admit(t, profile, auth, successor)
	symbol := "scip-go gomod example.com v1 Command#"
	member := func(path string, role int32) []byte {
		return canonicalWire(t, &scip.Index{Metadata: &scip.Metadata{ToolInfo: &scip.ToolInfo{Name: "scip-go", Version: "0.2.7"}, ProjectRoot: "file:///workspace", TextDocumentEncoding: scip.TextEncoding_UTF8}, Documents: []*scip.Document{{RelativePath: path, Occurrences: []*scip.Occurrence{{Range: []int32{1, 0, 4}, Symbol: symbol, SymbolRoles: role}}, Symbols: []*scip.SymbolInformation{}}}})
	}
	return bundleFixtureData{parent, execution, plan, []UnitOutcome{{first, UnitComplete}, {second, UnitComplete}}, []MemberInput{{"a", member("a.go", 0)}, {"b", member(generatedPath, 1)}}, map[string][]byte{generatedPath: source}}
}
func bundleContents(b Bundle) map[string][]byte {
	out := map[string][]byte{}
	for _, name := range b.Names() {
		out[name] = b.Content(name)
	}
	return out
}
func buildFixture(t *testing.T, f bundleFixtureData) Bundle {
	t.Helper()
	b, err := BuildBundle(context.Background(), f.execution, f.plan, f.outcomes, f.members, f.generated)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestBundleCanonicalAndRoutes(t *testing.T) {
	ctx := context.Background()
	f := bundleFixture(t)
	b := buildFixture(t, f)
	if b.RootDigest() == "" {
		t.Fatal("missing complete root")
	}
	if _, err := VerifyBundle(ctx, f.execution, f.plan, b.AttemptBytes(), b.RootBytes(), bundleContents(b)); err != nil {
		t.Fatal(err)
	}
	slices.Reverse(f.members)
	slices.Reverse(f.outcomes)
	again := buildFixture(t, f)
	if !bytes.Equal(b.RootBytes(), again.RootBytes()) || !bytes.Equal(b.AttemptBytes(), again.AttemptBytes()) {
		t.Fatal("permutation changed root")
	}
	var root BundleRoot
	if json.Unmarshal(b.RootBytes(), &root) != nil {
		t.Fatal("root decode")
	}
	var routes []SymbolRoute
	if json.Unmarshal(b.Content(root.Symbols.Name), &routes) != nil || len(routes) != 1 || len(routes[0].Members) != 2 {
		t.Fatalf("cross-member routing: %+v", routes)
	}
	raw := b.RootBytes()
	raw[0] ^= 1
	if bytes.Equal(raw, b.RootBytes()) {
		t.Fatal("mutable root")
	}
	for _, name := range b.Names() {
		raw = b.Content(name)
		raw[0] ^= 1
		if bytes.Equal(raw, b.Content(name)) {
			t.Fatal("mutable content")
		}
	}
	var d PackagePlanDefinition
	if json.Unmarshal(f.plan.Bytes(), &d) != nil {
		t.Fatal("plan")
	}
	slices.Reverse(d.Targets)
	slices.Reverse(d.Units)
	slices.Reverse(d.Documents)
	plan, err := SealPackagePlan(ctx, f.parent, d)
	if err != nil || plan.Digest() != f.plan.Digest() {
		t.Fatal("plan not canonical", err)
	}
	if _, err := DecodePackagePlan(ctx, f.parent, plan.Bytes(), plan.Digest()); err != nil {
		t.Fatal(err)
	}
}
func TestBundlePlanRefusals(t *testing.T) {
	f := bundleFixture(t)
	cases := []struct {
		name string
		edit func(*PackagePlanDefinition)
	}{
		{"missing target", func(d *PackagePlanDefinition) { d.Targets = d.Targets[:1] }},
		{"extra target", func(d *PackagePlanDefinition) { d.Targets = append(d.Targets, d.Targets[0]) }},
		{"target edge", func(d *PackagePlanDefinition) {
			d.Targets[0].Dependencies = append(d.Targets[0].Dependencies, hash([]byte("unknown")))
		}},
		{"missing unit", func(d *PackagePlanDefinition) { d.Units = d.Units[:1] }},
		{"extra unit", func(d *PackagePlanDefinition) { d.Units = append(d.Units, d.Units[0]) }},
		{"wrong required identity kind", func(d *PackagePlanDefinition) { d.Units[0].ID = PackageUnitID(d.Targets[0].ID) }},
		{"corrupt package edge", func(d *PackagePlanDefinition) {
			id, _ := NewPackageUnitID(hash([]byte("unknown")))
			d.Units[0].Imports = append(d.Units[0].Imports, id)
		}},
		{"missing document", func(d *PackagePlanDefinition) { d.Documents = d.Documents[:1] }},
		{"extra document", func(d *PackagePlanDefinition) { d.Documents = append(d.Documents, d.Documents[0]) }},
		{"document assignment", func(d *PackagePlanDefinition) { d.Documents[0].Unit = d.Documents[1].Unit }},
		{"raw generated path", func(d *PackagePlanDefinition) { d.Documents[0].Path = "../bazel-out/command.pb.go" }},
		{"namespace collision", func(d *PackagePlanDefinition) {
			for i := range d.Documents {
				if d.Documents[i].Generated {
					d.Documents[i].Generated = false
					d.Documents[i].ProvenanceDigest = ""
				}
			}
		}},
		{"missing provenance", func(d *PackagePlanDefinition) {
			for i := range d.Documents {
				d.Documents[i].ProvenanceDigest = ""
			}
		}},
		{"stale request", func(d *PackagePlanDefinition) { d.ParentRequestDigest = hash([]byte("other")) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var d PackagePlanDefinition
			if json.Unmarshal(f.plan.Bytes(), &d) != nil {
				t.Fatal("decode")
			}
			tc.edit(&d)
			if _, err := SealPackagePlan(context.Background(), f.parent, d); err == nil {
				t.Fatal("corrupt plan admitted")
			}
		})
	}
	raw := f.plan.Bytes()
	raw[0] ^= 1
	if _, err := DecodePackagePlan(context.Background(), f.parent, raw, f.plan.Digest()); err == nil {
		t.Fatal("corrupt sealed plan admitted")
	}
}
func TestBundleMissingCorruptAndDivergent(t *testing.T) {
	f := bundleFixture(t)
	b := buildFixture(t, f)
	ctx := context.Background()
	cases := []struct {
		name string
		edit func(map[string][]byte)
	}{
		{"missing member", func(c map[string][]byte) {
			for k := range c {
				if strings.HasPrefix(k, "members/") {
					delete(c, k)
					return
				}
			}
		}},
		{"extra member", func(c map[string][]byte) { c["members/unplanned.scip"] = []byte{1} }},
		{"corrupt bytes", func(c map[string][]byte) {
			for k := range c {
				c[k][0] ^= 1
				return
			}
		}},
		{"divergent document route", func(c map[string][]byte) {
			for k := range c {
				if strings.HasPrefix(k, "documents/") {
					c[k] = []byte("[]")
				}
			}
		}},
		{"divergent symbol route", func(c map[string][]byte) {
			for k := range c {
				if strings.HasPrefix(k, "symbols/") {
					c[k] = []byte("[]")
				}
			}
		}},
		{"missing generated", func(c map[string][]byte) {
			for k := range c {
				if IsGeneratedPath(k) {
					delete(c, k)
				}
			}
		}},
		{"corrupt generated", func(c map[string][]byte) {
			for k := range c {
				if IsGeneratedPath(k) {
					c[k][0] ^= 1
				}
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := bundleContents(b)
			tc.edit(c)
			if _, err := VerifyBundle(ctx, f.execution, f.plan, b.AttemptBytes(), b.RootBytes(), c); err == nil {
				t.Fatal("corrupt bundle admitted")
			}
		})
	}
	for _, tc := range []struct {
		name string
		edit func(*bundleFixtureData)
	}{
		{"missing document member", func(f *bundleFixtureData) { f.members = f.members[:1] }},
		{"duplicate member", func(f *bundleFixtureData) { f.members = append(f.members, f.members[0]) }},
		{"duplicate outcome", func(f *bundleFixtureData) { f.outcomes[1] = f.outcomes[0] }},
		{"required excluded", func(f *bundleFixtureData) { f.outcomes[0].State = UnitExcluded }},
		{"wrong metadata", func(f *bundleFixtureData) {
			var i scip.Index
			_ = proto.Unmarshal(f.members[1].SCIP, &i)
			i.Metadata.ToolInfo.Version = "other"
			f.members[1].SCIP = canonicalWire(t, &i)
		}},
		{"stale HEAD", func(f *bundleFixtureData) {
			f.execution.request.Source.Commit = strings.Repeat("b", 40)
			f.execution.digest = identity(f.execution.request)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := bundleFixture(t)
			tc.edit(&f)
			if _, err := BuildBundle(ctx, f.execution, f.plan, f.outcomes, f.members, f.generated); err == nil {
				t.Fatal("invalid attempt admitted")
			}
		})
	}
}
func TestBundleFailedAttemptsAndFencedPointer(t *testing.T) {
	ctx := context.Background()
	f := bundleFixture(t)
	b := buildFixture(t, f)
	current, err := NextPublication(ctx, PublicationPointer{}, PublicationPointer{}, f.execution, b)
	if err != nil || current.Epoch != 1 {
		t.Fatal("initial publication", err)
	}
	stale := f.execution
	stale.request.Source.Generation = hash([]byte("new-source"))
	stale.digest = identity(stale.request)
	for _, a := range []Admission{stale, {}} {
		got, err := NextPublication(ctx, current, current, a, b)
		if !errors.Is(err, Stale) || got != current {
			t.Fatal("stale changed prior", err)
		}
	}
	got, err := NextPublication(ctx, current, PublicationPointer{}, f.execution, b)
	if !errors.Is(err, Stale) || got != current {
		t.Fatal("CAS mismatch replaced prior")
	}
	for _, state := range []UnitState{UnitFailed, UnitUnsupported} {
		t.Run(string(state), func(t *testing.T) {
			f := bundleFixture(t)
			for i := range f.outcomes {
				f.outcomes[i].State = state
			}
			f.members = nil
			f.generated = nil
			failed := buildFixture(t, f)
			if len(failed.RootBytes()) != 0 || failed.RootDigest() != "" {
				t.Fatal("partial became current")
			}
			if _, err := VerifyBundle(ctx, f.execution, f.plan, failed.AttemptBytes(), nil, bundleContents(failed)); err != nil {
				t.Fatal(err)
			}
			got, err := NextPublication(ctx, current, current, f.execution, failed)
			if !errors.Is(err, Stale) || got != current {
				t.Fatal("failed replaced prior")
			}
		})
	}
	full := current
	full.Epoch = math.MaxUint64
	if _, err := NextPublication(ctx, full, full, f.execution, b); !errors.Is(err, Capacity) {
		t.Fatal("epoch overflow", err)
	}
	cancel, cancelFn := context.WithCancel(ctx)
	cancelFn()
	if _, err := NextPublication(cancel, current, current, f.execution, b); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation")
	}
}

func TestBundleConflictingCrossMemberSymbols(t *testing.T) {
	f := bundleFixture(t)
	for i, raw := range f.members {
		var index scip.Index
		if proto.Unmarshal(raw.SCIP, &index) != nil {
			t.Fatal("decode")
		}
		index.Documents[0].Symbols = []*scip.SymbolInformation{{Symbol: "scip-go gomod example.com v1 Command#", Documentation: []string{strings.Repeat("x", i+1)}}}
		f.members[i].SCIP = canonicalWire(t, &index)
	}
	if _, err := BuildBundle(context.Background(), f.execution, f.plan, f.outcomes, f.members, f.generated); !errors.Is(err, Invalid) {
		t.Fatal("conflicting global metadata admitted", err)
	}
}
func TestBundleFrozenTestExclusion(t *testing.T) {
	f := bundleFixture(t)
	var d PackagePlanDefinition
	if json.Unmarshal(f.plan.Bytes(), &d) != nil {
		t.Fatal("decode")
	}
	profile := f.parent.profile
	request := f.parent.request
	for i := range d.Targets {
		d.Targets[i].Test = true
	}
	for i := range d.Units {
		d.Units[i].Test = true
	}
	request.UniverseDigest = identity(d.Targets)
	authority := Authority{Enabled: true, Administrator: true, Source: request.Source, Profile: Epoch{request.ProfileEpoch, profile.Digest()}, UniverseDigest: request.UniverseDigest}
	parent := admit(t, profile, authority, request)
	d.ParentRequestDigest = parent.Digest()
	plan, err := SealPackagePlan(context.Background(), parent, d)
	if err != nil {
		t.Fatal(err)
	}
	next, err := PlannedSuccessor(context.Background(), parent, plan.Digest())
	if err != nil {
		t.Fatal(err)
	}
	authority.ParentRequestDigest = parent.Digest()
	authority.PlanDigest = plan.Digest()
	execution := admit(t, profile, authority, next)
	outcomes := slices.Clone(f.outcomes)
	for i := range outcomes {
		outcomes[i].State = UnitExcluded
	}
	b, err := BuildBundle(context.Background(), execution, plan, outcomes, nil, nil)
	if err != nil || b.RootDigest() == "" {
		t.Fatal("predeclared test exclusion", err)
	}
	outcomes[0].State = UnitComplete
	if _, err := BuildBundle(context.Background(), execution, plan, outcomes, nil, nil); err == nil {
		t.Fatal("frozen exclusion changed at execution")
	}
}
func TestBundleBoundsAndControls(t *testing.T) {
	ctx := context.Background()
	f := bundleFixture(t)
	b := buildFixture(t, f)
	if len(b.RootBytes()) > MaxRootBytes || len(b.AttemptBytes()) > MaxAttemptBytes {
		t.Fatal("control cap")
	}
	if _, err := DecodePackagePlan(ctx, f.parent, bytes.Repeat([]byte(" "), MaxPlanBytes+1), hash([]byte("bad"))); err == nil {
		t.Fatal("oversize plan")
	}
	if _, err := VerifyBundle(ctx, f.execution, f.plan, bytes.Repeat([]byte(" "), MaxAttemptBytes+1), b.RootBytes(), bundleContents(b)); err == nil {
		t.Fatal("oversize manifest")
	}
	many := make([]MemberInput, MaxSCIPMembers+1)
	if _, err := BuildBundle(ctx, f.execution, f.plan, f.outcomes, many, nil); err == nil {
		t.Fatal("member count")
	}
	for _, kind := range []string{"targets", "units", "documents", "edges"} {
		t.Run(kind, func(t *testing.T) {
			var d PackagePlanDefinition
			_ = json.Unmarshal(f.plan.Bytes(), &d)
			switch kind {
			case "targets":
				d.Targets = make([]PlannedTarget, MaxBundleTargets+1)
			case "units":
				d.Units = make([]PlannedUnit, MaxBundleUnits+1)
			case "documents":
				d.Documents = make([]PlannedDocument, MaxBundleDocuments+1)
			case "edges":
				d.Targets[0].Dependencies = make([]string, MaxBundleEdges+1)
			}
			if _, err := SealPackagePlan(ctx, f.parent, d); err == nil {
				t.Fatal("collection cap bypass")
			}
		})
	}
	cancel, cancelFn := context.WithCancel(ctx)
	cancelFn()
	if _, err := BuildBundle(cancel, f.execution, f.plan, f.outcomes, f.members, f.generated); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation")
	}
	// The durable attempt cannot substitute untrusted state even with valid JSON.
	raw := b.AttemptBytes()
	var m AttemptManifest
	_ = json.Unmarshal(raw, &m)
	m.Complete = false
	if _, err := VerifyBundle(ctx, f.execution, f.plan, wire(t, m), b.RootBytes(), bundleContents(b)); err == nil {
		t.Fatal("terminal state forged")
	}
}

func resealBundleFixture(t *testing.T, f bundleFixtureData, d PackagePlanDefinition) (bundleFixtureData, error) {
	t.Helper()
	ctx := context.Background()
	slices.SortFunc(d.Targets, func(a, b PlannedTarget) int { return strings.Compare(a.ID, b.ID) })
	for i := range d.Targets {
		slices.Sort(d.Targets[i].Dependencies)
		slices.Sort(d.Targets[i].Units)
	}
	r := f.parent.request
	r.UniverseDigest = identity(d.Targets)
	a := Authority{Enabled: true, Administrator: true, Source: r.Source, Profile: Epoch{r.ProfileEpoch, r.ProfileDigest}, UniverseDigest: r.UniverseDigest}
	f.parent = admit(t, f.parent.profile, a, r)
	d.ParentRequestDigest = f.parent.Digest()
	p, err := SealPackagePlan(ctx, f.parent, d)
	if err != nil {
		return f, err
	}
	f.plan = p
	r, err = PlannedSuccessor(ctx, f.parent, p.Digest())
	if err != nil {
		return f, err
	}
	a.ParentRequestDigest = f.parent.Digest()
	a.PlanDigest = p.Digest()
	f.execution = admit(t, f.parent.profile, a, r)
	return f, nil
}
func TestBundleReviewProducerAndSlots(t *testing.T) {
	cases := []struct {
		name string
		edit func(*bundleFixtureData)
	}{
		{"all wrong version", func(f *bundleFixtureData) {
			for i := range f.members {
				var index scip.Index
				_ = proto.Unmarshal(f.members[i].SCIP, &index)
				index.Metadata.ToolInfo.Version = "definitely-other"
				f.members[i].SCIP = canonicalWire(t, &index)
			}
		}},
		{"all wrong name", func(f *bundleFixtureData) {
			for i := range f.members {
				var index scip.Index
				_ = proto.Unmarshal(f.members[i].SCIP, &index)
				index.Metadata.ToolInfo.Name = "other"
				index.Documents[0].PositionEncoding = scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart
				f.members[i].SCIP = canonicalWire(t, &index)
			}
		}},
		{"moved documents", func(f *bundleFixtureData) {
			f.members[0].Name, f.members[1].Name = f.members[1].Name, f.members[0].Name
		}},
		{"extra slot", func(f *bundleFixtureData) { f.members[0].Name = "extra" }},
		{"duplicate slot", func(f *bundleFixtureData) { f.members[1].Name = f.members[0].Name }},
		{"metadata-only slot", func(f *bundleFixtureData) {
			var index scip.Index
			_ = proto.Unmarshal(f.members[1].SCIP, &index)
			index.Documents = nil
			index.ExternalSymbols = nil
			f.members[1].SCIP = canonicalWire(t, &index)
		}},
		{"extra metadata-only member", func(f *bundleFixtureData) {
			var index scip.Index
			_ = proto.Unmarshal(f.members[0].SCIP, &index)
			index.Documents = nil
			index.ExternalSymbols = nil
			f.members = append(f.members, MemberInput{"extra", canonicalWire(t, &index)})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := bundleFixture(t)
			tc.edit(&f)
			if _, err := BuildBundle(context.Background(), f.execution, f.plan, f.outcomes, f.members, f.generated); err == nil {
				t.Fatal("review regression accepted")
			}
		})
	}
}
func TestBundleReviewDependencyOutcomes(t *testing.T) {
	for _, state := range []UnitState{UnitFailed, UnitUnsupported} {
		t.Run(string(state), func(t *testing.T) {
			f := bundleFixture(t)
			var d PackagePlanDefinition
			_ = json.Unmarshal(f.plan.Bytes(), &d)
			for i := range d.Units {
				d.Units[i].Imports = []PackageUnitID{}
			}
			leaf, mid, alias, root := hash([]byte("leaf")), hash([]byte("mid")), hash([]byte("alias")), hash([]byte("root"))
			d.Targets = []PlannedTarget{{ID: leaf, Dependencies: []string{}, Units: []PackageUnitID{f.outcomes[0].Unit}}, {ID: mid, Dependencies: []string{leaf}, Units: []PackageUnitID{f.outcomes[1].Unit}}, {ID: alias, Dependencies: []string{leaf}, Units: []PackageUnitID{}}, {ID: root, Dependencies: []string{mid, alias}, Units: []PackageUnitID{}}}
			var err error
			f, err = resealBundleFixture(t, f, d)
			if err != nil {
				t.Fatal(err)
			}
			f.outcomes[0].State = state
			f.members = f.members[1:]
			b := buildFixture(t, f)
			var m AttemptManifest
			if json.Unmarshal(b.AttemptBytes(), &m) != nil {
				t.Fatal("decode")
			}
			if m.Complete || len(b.RootBytes()) != 0 {
				t.Fatal("failed complete")
			}
			for _, target := range m.Targets {
				if target.State != state {
					t.Fatalf("lost transitive %s: %+v", state, target)
				}
			}
		})
	}
	f := bundleFixture(t)
	f.outcomes[0].State = UnitFailed
	f.members = f.members[1:]
	if _, err := BuildBundle(context.Background(), f.execution, f.plan, f.outcomes, f.members, f.generated); !errors.Is(err, Invalid) {
		t.Fatal("completed package imports failed package", err)
	}
	for _, kind := range []string{"targets", "packages"} {
		t.Run(kind+" cycle", func(t *testing.T) {
			f := bundleFixture(t)
			var d PackagePlanDefinition
			_ = json.Unmarshal(f.plan.Bytes(), &d)
			if kind == "targets" {
				d.Targets[0].Dependencies = []string{d.Targets[1].ID}
				d.Targets[1].Dependencies = []string{d.Targets[0].ID}
			} else {
				d.Units[0].Imports = []PackageUnitID{d.Units[1].ID}
				d.Units[1].Imports = []PackageUnitID{d.Units[0].ID}
			}
			if _, err := resealBundleFixture(t, f, d); err == nil {
				t.Fatal("cycle admitted")
			}
		})
	}
}
func TestBundleMeasuredEnvelope(t *testing.T) {
	f := bundleFixture(t)
	b := buildFixture(t, f)
	total := len(b.RootBytes())
	for _, name := range b.Names() {
		total += len(b.Content(name))
	}
	t.Logf("neutral two-member generated bundle: plan=%d attempt=%d root=%d total=%d bytes", len(f.plan.Bytes()), len(b.AttemptBytes()), len(b.RootBytes()), total)
	if total > MaxPublicationBytes {
		t.Fatal("publication bound")
	}
	// Synthetic configured-universe upper boundary with a valid acyclic graph;
	// this measures contract admission, not a native provider or scale run.
	var d PackagePlanDefinition
	_ = json.Unmarshal(f.plan.Bytes(), &d)
	d.Targets = make([]PlannedTarget, MaxBundleTargets)
	for i := range d.Targets {
		d.Targets[i] = PlannedTarget{ID: hash([]byte(strings.Repeat("t", i+1))), Dependencies: []string{}, Units: []PackageUnitID{f.outcomes[i%2].Unit}}
	}
	d.Units = make([]PlannedUnit, MaxBundleUnits)
	for i := range d.Units {
		id, _ := NewPackageUnitID(hash([]byte(strings.Repeat("u", i+1))))
		d.Units[i] = PlannedUnit{ID: id, Imports: []PackageUnitID{}, Documents: []string{}}
		d.Targets[i].Units = []PackageUnitID{id}
	}
	for i := MaxBundleUnits; i < len(d.Targets); i++ {
		d.Targets[i].Units = []PackageUnitID{d.Units[0].ID}
	}
	d.Documents = []PlannedDocument{}
	enlarged, err := resealBundleFixture(t, f, d)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("neutral maximum configured-target/unit counts: targets=%d units=%d encoded_plan=%d bytes", len(d.Targets), len(d.Units), len(enlarged.plan.Bytes()))
}

func TestReducedProfileRefusesGeneratedAuthority(t *testing.T) {
	ctx := t.Context()
	f := bundleFixture(t)
	original := buildFixture(t, f)
	d := f.parent.profile.Definition()
	d.Schema, d.Config = ProfileSchema, ReducedConfig()
	profile, err := DecodeProfile(ctx, wire(t, d))
	if err != nil {
		t.Fatal(err)
	}
	request := NewRequest(f.parent.Request().Source, profile, f.parent.Request().ProfileEpoch, f.parent.Request().UniverseDigest, f.parent.Request().IdempotencyKey)
	authority := Authority{Enabled: true, Administrator: true, Source: request.Source, Profile: Epoch{Number: request.ProfileEpoch, Digest: profile.Digest()}, UniverseDigest: request.UniverseDigest}
	parent := admit(t, profile, authority, request)
	definition := f.plan.definition
	definition.ParentRequestDigest = parent.Digest()
	raw := wire(t, definition)
	if _, err = SealPackagePlan(ctx, parent, definition); !errors.Is(err, Unsupported) {
		t.Fatalf("omit plan: %v", err)
	}
	if _, err = DecodePackagePlan(ctx, parent, raw, hash(raw)); !errors.Is(err, Unsupported) {
		t.Fatalf("omit decoded plan: %v", err)
	}
	// Model a plan already sealed by the old implementation. Its public admission
	// and digests are valid, so the independent publication policy fence must fire.
	legacy := PackagePlan{definition: definition, raw: raw, digest: hash(raw)}
	successor, err := PlannedSuccessor(ctx, parent, legacy.Digest())
	if err != nil {
		t.Fatal(err)
	}
	authority.ParentRequestDigest, authority.PlanDigest = parent.Digest(), legacy.Digest()
	execution := admit(t, profile, authority, successor)
	if _, err = BuildBundle(ctx, execution, legacy, f.outcomes, f.members, f.generated); !errors.Is(err, Unsupported) {
		t.Fatalf("omit bundle: %v", err)
	}
	var manifest AttemptManifest
	if err = json.Unmarshal(original.AttemptBytes(), &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Request = successor
	if _, err = VerifyBundle(ctx, execution, legacy, wire(t, manifest), original.RootBytes(), bundleContents(original)); !errors.Is(err, Unsupported) {
		t.Fatalf("omit verify: %v", err)
	}
	// A zero generated map cannot hide a generated plan from publication policy.
	if _, err = BuildBundle(ctx, execution, legacy, f.outcomes, f.members, nil); !errors.Is(err, Unsupported) {
		t.Fatalf("omit plan with absent payload: %v", err)
	}
	for _, state := range []UnitState{UnitFailed, UnitUnsupported} {
		outcomes := slices.Clone(f.outcomes)
		outcomes[0].State = state
		if _, err = BuildBundle(ctx, execution, legacy, outcomes, nil, nil); !errors.Is(err, Unsupported) {
			t.Fatalf("omit terminal %s: %v", state, err)
		}
	}
}

func TestReducedProfileOrdinaryBundle(t *testing.T) {
	ctx := t.Context()
	f := bundleFixture(t)
	d := f.parent.profile.Definition()
	d.Schema, d.Config = ProfileSchema, ReducedConfig()
	profile, err := DecodeProfile(ctx, wire(t, d))
	if err != nil {
		t.Fatal(err)
	}
	definition := f.plan.definition
	// Keep the ordinary unit and its configured target, with no generated data.
	definition.Documents = slices.DeleteFunc(definition.Documents, func(d PlannedDocument) bool { return d.Generated })
	unit := definition.Documents[0].Unit
	definition.Units = slices.DeleteFunc(definition.Units, func(u PlannedUnit) bool { return u.ID != unit })
	definition.Targets = slices.DeleteFunc(definition.Targets, func(target PlannedTarget) bool { return !slices.Contains(target.Units, unit) })
	request := NewRequest(f.parent.Request().Source, profile, f.parent.Request().ProfileEpoch, identity(definition.Targets), f.parent.Request().IdempotencyKey)
	authority := Authority{Enabled: true, Administrator: true, Source: request.Source, Profile: Epoch{Number: request.ProfileEpoch, Digest: profile.Digest()}, UniverseDigest: request.UniverseDigest}
	parent := admit(t, profile, authority, request)
	definition.ParentRequestDigest = parent.Digest()
	plan, err := SealPackagePlan(ctx, parent, definition)
	if err != nil {
		t.Fatal(err)
	}
	successor, err := PlannedSuccessor(ctx, parent, plan.Digest())
	if err != nil {
		t.Fatal(err)
	}
	authority.ParentRequestDigest, authority.PlanDigest = parent.Digest(), plan.Digest()
	execution := admit(t, profile, authority, successor)
	bundle, err := BuildBundle(ctx, execution, plan, []UnitOutcome{{unit, UnitComplete}}, f.members[:1], nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = VerifyBundle(ctx, execution, plan, bundle.AttemptBytes(), bundle.RootBytes(), bundleContents(bundle)); err != nil {
		t.Fatal(err)
	}
}
