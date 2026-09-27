//go:build linux

package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/codenav"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/bmeddeb/phebs/internal/typedworkspace"
	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"
)

// Explicit test-owned loopback server; no production OpenLocal version bypass.
// The existing installed engine child is always killed/joined within ten seconds.
func typedNavigationServer(t *testing.T) string {
	t.Helper()
	binary, e := exec.LookPath("surreal")
	if e != nil {
		t.Fatal(e)
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	cmd := exec.CommandContext(t.Context(), binary, "start", "--bind", address, "--user", "root", "--pass", "fixture", "memory")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	done := make(chan struct{})
	var waitErr error
	go func() { waitErr = cmd.Wait(); close(done) }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("engine failed to join")
		}
	})
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		connection, e := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if e == nil {
			_ = connection.Close()
			return "ws://" + address
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("engine readiness timeout")
		case <-done:
			t.Fatal("engine exited", waitErr)
		}
	}
}
func typedNavigationBytes(raw []byte) string { return fmt.Sprintf("sha256:%x", sha256.Sum256(raw)) }

type typedNavigationPublication struct {
	owner             typedworkspace.OwnerManifest
	parent, execution typedindex.Admission
	bundle            typedindex.Bundle
	path              string
}
type typedNavigationFixture struct {
	state              *store.Surreal
	base, source, repo string
	profile            typedindex.Profile
	inventory          typedindex.Inventory
	inventoryRaw       []byte
	unit               typedindex.PackageUnitID
	targets            []typedindex.PlannedTarget
	document           string
}

func typedNavigationFixtureFor(t *testing.T, endpoint string) typedNavigationFixture {
	t.Helper()
	ctx := t.Context()
	check := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	s, e := store.Open(ctx, endpoint, "root", "fixture", "navigation", "fixture")
	check(e)
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	root := t.TempDir()
	check(os.Chmod(root, 0700))
	t.Cleanup(func() {
		_ = filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
			if e == nil && d.IsDir() {
				_ = os.Chmod(p, 0700)
			}
			return nil
		})
	})
	base, src := filepath.Join(root, "owned"), filepath.Join(root, "source")
	check(os.Mkdir(base, 0700))
	check(os.Mkdir(src, 0700))
	check(os.WriteFile(filepath.Join(base, ".phebs-index-publication.lock"), nil, 0600))
	check(os.WriteFile(filepath.Join(src, "input"), []byte("x"), 0600))
	raw := typedNavigationJSON(t, typedindex.InventoryDefinition{Schema: typedindex.InventorySchema, Files: []typedindex.BundleFile{{Path: "input", Bytes: 1, Digest: typedNavigationBytes([]byte("x"))}}})
	inv, e := typedindex.DecodeInventory(ctx, raw, typedNavigationBytes(raw))
	check(e)
	profile := typedNavigationTestProfile(t, inv.Digest())
	unit, e := typedindex.NewPackageUnitID(typedNavigationHash("unit"))
	check(e)
	name, e := typedindex.GeneratedPath(unit, "unicode.go")
	check(e)
	targets := []typedindex.PlannedTarget{{ID: typedNavigationHash("target"), Dependencies: []string{}, Units: []typedindex.PackageUnitID{unit}}}
	repo := "example.invalid/routed"
	check(s.UpsertRepo(ctx, store.Repo{Name: repo}))
	check(s.SetRepoIndexed(ctx, repo, strings.Repeat("a", 40), time.Now()))
	_, e = s.InstallTypedProfile(ctx, repo, profile, typedNavigationBytes(typedNavigationJSON(t, targets)), 0)
	check(e)
	return typedNavigationFixture{s, base, src, repo, profile, inv, raw, unit, targets, name}
}
func (f typedNavigationFixture) publish(t *testing.T, key string) typedNavigationPublication {
	t.Helper()
	ctx := t.Context()
	check := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	source, e := f.state.GetTypedSource(ctx, f.repo)
	check(e)
	intent, e := f.state.GetTypedIndexIntent(ctx, f.repo)
	check(e)
	raw := typedNavigationJSON(t, typedindex.NewRequest(source, f.profile, uint64(intent.ProfileEpoch), intent.UniverseDigest, key))
	_, e = f.state.EnqueueTypedIndex(ctx, f.repo, raw)
	check(e)
	schedule, e := f.state.TypedIndexSchedule(ctx, f.repo)
	check(e)
	_, e = f.state.EnqueueGenerationSchedule(ctx, schedule)
	check(e)
	_, e = f.state.ExpandGenerationSchedule(ctx, f.repo, schedule.Stage, schedule.Generation)
	check(e)
	chunk, e := f.state.ClaimGenerationChunk(ctx, store.GenerationResourceTypedIndex, "reader-fixture")
	check(e)
	if chunk == nil {
		t.Fatal("no chunk")
	}
	work, e := f.state.BeginTypedIndex(ctx, *chunk)
	check(e)
	observation, e := typedworkspace.ObserveCapacity(ctx, f.base)
	check(e)
	domain := store.TypedIndexGrowthDomain{Device: observation.Device, BaseInode: observation.Inode, BlockBytes: observation.BlockSize, TotalBytes: observation.TotalBytes, AvailableBytes: observation.AvailableBytes, TotalInodes: observation.TotalInodes, FreeInodes: observation.FreeInodes, FutureBytes: 1 << 20, FutureInodes: 128}
	// Tiny local publication only: two explicit 1MiB promises, never native caps.
	_, e = f.state.AcquireTypedIndexGrowth(ctx, *chunk, store.TypedIndexGrowthSpec{Workspace: domain, Host: domain})
	check(e)
	id, e := typedworkspace.NewOwnerIdentity(work.Parent, chunk.Identity, chunk.LeaseToken)
	check(e)
	gate := lifecycle.NewGate(f.base)
	owner, e := typedworkspace.CreateOwner(ctx, f.base, observation, id, typedworkspace.OwnerBudget{Bytes: 1 << 20, Inodes: 128}, gate)
	check(e)
	save := func(before string, plan, root string) {
		c := store.TypedIndexCustody{PlanningDigest: id.PlanningDigest, AttemptDigest: id.AttemptDigest, ManifestDigest: owner.Digest(), Revision: owner.Revision, DirectoryDevice: owner.Directory.Device, DirectoryInode: owner.Directory.Inode}
		if owner.Inputs != nil {
			c.InputReceiptDigest = owner.Inputs.Digest
		}
		if owner.Publication != nil {
			c.PublicationReceiptDigest = owner.Publication.Digest
			c.PublicationRequestDigest = work.Admission.Digest()
			c.PublicationPlanDigest = plan
			c.PublicationRootDigest = root
		}
		check(f.state.SaveTypedIndexCustody(ctx, *chunk, before, c))
	}
	save("", "", "")
	before := owner.Digest()
	attempt := filepath.Join(f.base, id.RelativeName())
	inputs, e := typedworkspace.Copy(ctx, f.source, attempt, f.inventory, gate)
	check(e)
	owner, e = typedworkspace.SaveOwnerInputs(ctx, f.base, id, before, f.inventoryRaw, inputs, gate)
	check(e)
	save(before, "", "")
	check(f.state.AdvanceTypedIndex(ctx, *chunk, store.TypedPreflight))
	body := []byte("😀x\nx\n")
	plan, e := typedindex.SealPackagePlan(ctx, work.Parent, typedindex.PackagePlanDefinition{Schema: typedindex.PackagePlanSchema, ParentRequestDigest: work.Parent.Digest(), Targets: f.targets, Units: []typedindex.PlannedUnit{{ID: f.unit, Imports: []typedindex.PackageUnitID{}, Documents: []string{f.document}}}, Documents: []typedindex.PlannedDocument{{Path: f.document, Member: "one", Unit: f.unit, Bytes: int64(len(body)), Digest: typedNavigationBytes(body), Generated: true, ProvenanceDigest: typedNavigationHash("generator")}}})
	check(e)
	admission, e := f.state.SealTypedIndexPlan(ctx, *chunk, plan)
	check(e)
	work.Admission = admission
	symbol := "scip-go gomod example.test v1 X#"
	member, e := proto.Marshal(&scip.Index{Metadata: &scip.Metadata{ToolInfo: &scip.ToolInfo{Name: "scip-go", Version: "0.2.7"}, ProjectRoot: "file:///workspace", TextDocumentEncoding: scip.TextEncoding_UTF8}, Documents: []*scip.Document{{RelativePath: f.document, PositionEncoding: scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart, Occurrences: []*scip.Occurrence{{TypedRange: &scip.Occurrence_SingleLineRange{SingleLineRange: &scip.SingleLineRange{Line: 0, StartCharacter: 4, EndCharacter: 5}}, Symbol: symbol, SymbolRoles: 1}, {TypedRange: &scip.Occurrence_SingleLineRange{SingleLineRange: &scip.SingleLineRange{Line: 1, StartCharacter: 0, EndCharacter: 1}}, Symbol: symbol}}, Symbols: []*scip.SymbolInformation{{Symbol: symbol, Documentation: []string{"generated"}}}}}})
	check(e)
	bundle, e := typedindex.BuildBundle(ctx, admission, plan, []typedindex.UnitOutcome{{Unit: f.unit, State: typedindex.UnitComplete}}, []typedindex.MemberInput{{Name: "one", SCIP: member}}, map[string][]byte{f.document: body})
	check(e)
	check(f.state.AdvanceTypedIndex(ctx, *chunk, store.TypedExecution))
	pub, e := typedworkspace.InstallPublication(ctx, attempt, work.Parent, admission, plan, bundle, gate)
	check(e)
	before = owner.Digest()
	owner, e = typedworkspace.SaveOwnerPublication(ctx, f.base, id, before, work.Parent, admission, pub, gate)
	check(e)
	save(before, plan.Digest(), bundle.RootDigest())
	check(f.state.AdvanceTypedIndex(ctx, *chunk, store.TypedValidation))
	replacement, e := f.state.ReadExpectedTypedIndexCurrent(ctx, *chunk)
	check(e)
	_, e = f.state.PublishTypedIndexReplacement(ctx, *chunk, replacement, bundle)
	check(e)
	check(f.state.CompleteGenerationChunk(ctx, *chunk))
	release, e := f.state.InspectTypedIndexGrowthRelease(ctx, work.AttemptDigest)
	check(e)
	check(f.state.ReleaseTypedIndexGrowth(ctx, release)) // No native child ever existed in this fixture.
	return typedNavigationPublication{owner, work.Parent, admission, bundle, f.document}
}

type typedNavigationRace struct {
	*typedCodeNavigationResolver
	t       *testing.T
	attempt string
	calls   int
	change  func()
}

func (r *typedNavigationRace) ResolveRoutedIndex(ctx context.Context, repo, revision string) (codenav.RoutedBinding, error) {
	r.calls++
	if r.calls == 2 {
		bounded, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
		defer cancel()
		release, e := typedworkspace.AcquirePublicationMutation(bounded, r.attempt)
		if e == nil {
			release()
			r.t.Fatal("final current lookup lost physical pin")
		}
		r.change()
	}
	return r.typedCodeNavigationResolver.ResolveRoutedIndex(ctx, repo, revision)
}
func TestTypedNavigationLinux(t *testing.T) {
	f := typedNavigationFixtureFor(t, typedNavigationServer(t))
	first := f.publish(t, "first")
	resolver, e := newTypedCodeNavigationResolver(f.state, f.base)
	if e != nil {
		t.Fatal(e)
	}
	service := codenav.New(codenav.Options{DataDir: filepath.Join(f.base, "no-git"), RoutedResolver: resolver})
	q := codenav.Query{Repo: f.repo, Revision: first.parent.Request().Source.Commit, Path: f.document, Line: 0, Character: 2, Encoding: codenav.EncodingUTF16}
	for i := 0; i < 2; i++ {
		readctx, ledger, e := readaccounting.Start(t.Context(), readaccounting.Counts{StoreReadAttempts: 20, ControlFileReads: 100, MemberVisits: 1000})
		if e != nil {
			t.Fatal(e)
		}
		d, e := service.Definition(readctx, q)
		counts, countErr := ledger.Finish()
		if countErr != nil || counts.StoreReadAttempts != 20 || counts.StoreWriteAttempts != 0 {
			t.Fatal("complete query accounting", counts, countErr)
		}
		if e != nil || d.Location == nil || d.Location.Range.Start.Character != 2 {
			t.Fatal(d, e)
		}
		refs, e := service.References(t.Context(), q)
		if e != nil || len(refs.Locations) != 1 || refs.Locations[0].Range.Start.Line != 1 {
			t.Fatal(refs, e)
		}
		h, e := service.Hover(t.Context(), q)
		if e != nil || h.Hover == nil || len(h.Hover.Documentation) != 1 {
			t.Fatal(h, e)
		}
	}
	historical := q
	historical.Revision = strings.Repeat("b", 40)
	d, e := service.Definition(t.Context(), historical)
	if e != nil || d.Available {
		t.Fatal("typed history fell through legacy", d, e)
	}
	oldBinding, e := resolver.ResolveRoutedIndex(t.Context(), f.repo, q.Revision)
	if e != nil {
		t.Fatal(e)
	}
	racing := &typedNavigationRace{typedCodeNavigationResolver: resolver, t: t, attempt: filepath.Join(f.base, first.owner.RelativeName()), change: func() {
		if e := f.state.SetRepoIndexed(t.Context(), f.repo, strings.Repeat("b", 40), time.Now()); e != nil {
			t.Fatal(e)
		}
	}}
	raced := codenav.New(codenav.Options{DataDir: f.base, RoutedResolver: racing})
	d, e = raced.Definition(t.Context(), q)
	if !errors.Is(e, codenav.ErrBindingChanged) || d.Available {
		t.Fatal("stale result escaped", d, e)
	}
	if p, _, e := resolver.OpenRoutedIndex(t.Context(), oldBinding, nil); e == nil {
		_ = p.Close()
		t.Fatal("old binding reopened after source change")
	}
	second := f.publish(t, "second")
	q.Revision = second.parent.Request().Source.Commit
	d, e = service.Definition(t.Context(), q)
	if e != nil || d.Location == nil {
		t.Fatal("replacement", d, e)
	}
	selection, e := f.state.InspectTypedIndexRetirement(t.Context(), first.parent.Digest())
	if e != nil {
		t.Fatal(e)
	}
	if e = f.state.BeginTypedIndexRetirement(t.Context(), selection); e != nil {
		t.Fatal(e)
	}
	authority, e := typedworkspace.RetirementAuthority(first.owner)
	if e != nil {
		t.Fatal(e)
	}
	for turns := 0; turns < 30; turns++ {
		report, e := typedworkspace.DrainOwner(t.Context(), f.base, authority)
		if e != nil {
			t.Fatal(e)
		}
		if report.Done {
			break
		}
		if turns == 29 {
			t.Fatal("reader idle cache blocked retirement", report)
		}
	}
	d, e = service.Definition(t.Context(), q)
	if e != nil || d.Location == nil {
		t.Fatal("retirement affected new current", d, e)
	}
}
