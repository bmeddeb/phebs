//go:build linux

package codenav_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/codenav"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/bmeddeb/phebs/internal/typedworkspace"
	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"
)

// One fixed trusted attempt, not a binding→authority cache or runtime registration.
type workspaceResolver struct {
	base      string
	authority typedworkspace.RoutedAuthority
	checks    int
	warm      int
}

func (r *workspaceResolver) ResolveRoutedIndex(ctx context.Context, _, _ string) (codenav.RoutedBinding, error) {
	r.checks++
	if r.checks%2 == 0 {
		bounded, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
		defer cancel()
		release, e := typedworkspace.AcquirePublicationMutation(bounded, filepath.Join(r.base, r.authority.Owner.RelativeName()))
		if e == nil {
			release()
			return codenav.RoutedBinding{}, fmt.Errorf("final current check lost attempt pin")
		}
	}
	return codenav.RoutedBinding{Selected: true, Identity: r.authority.Identity(), RootDigest: r.authority.RootDigest, Source: r.authority.Parent.Request().Source}, nil
}
func (r *workspaceResolver) OpenRoutedIndex(ctx context.Context, b codenav.RoutedBinding, m codenav.RoutedMetadata) (codenav.RoutedReader, codenav.RoutedMetadata, error) {
	if b.Identity != r.authority.Identity() {
		return nil, nil, typedworkspace.ErrCustody
	}
	var cached *typedworkspace.RoutedMetadata
	if m != nil {
		var ok bool
		cached, ok = m.(*typedworkspace.RoutedMetadata)
		if !ok {
			return nil, nil, typedworkspace.ErrCustody
		}
		r.warm++
	}
	p, e := typedworkspace.OpenRoutedPublication(ctx, r.base, r.authority, cached)
	if e != nil {
		return nil, nil, e
	}
	return p, p.Metadata(), nil
}
func TestRoutedWorkspaceConcreteAdapter(t *testing.T) {
	ctx := t.Context()
	hash := func(b []byte) string { return fmt.Sprintf("sha256:%x", sha256.Sum256(b)) }
	wire := func(v any) []byte {
		b, e := json.Marshal(v)
		if e != nil {
			t.Fatal(e)
		}
		return b
	}
	check := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
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
	src, base := filepath.Join(root, "source"), filepath.Join(root, "owned")
	check(os.Mkdir(src, 0700))
	check(os.Mkdir(base, 0700))
	check(os.WriteFile(filepath.Join(base, ".phebs-index-publication.lock"), nil, 0600))
	check(os.WriteFile(filepath.Join(src, "input"), []byte("x"), 0600))
	invraw := wire(typedindex.InventoryDefinition{Schema: typedindex.InventorySchema, Files: []typedindex.BundleFile{{Path: "input", Bytes: 1, Digest: hash([]byte("x"))}}})
	inv, e := typedindex.DecodeInventory(ctx, invraw, hash(invraw))
	check(e)
	tool := typedindex.Tool{Version: "0.2.7", Digest: hash([]byte("tool"))}
	profile, e := typedindex.DecodeProfile(ctx, wire(typedindex.ProfileDefinition{Schema: typedindex.GeneratedProfileSchema, Name: "reader", Provider: typedindex.ProviderID, Tools: typedindex.Tools{Bazel: tool, RulesGo: tool, Go: tool, Driver: tool, Indexer: tool, Planner: tool, Launcher: tool}, Config: typedindex.GeneratedConfig(), Policy: typedindex.MeasuredPolicy(), BundleDigest: inv.Digest(), ImageDigest: hash([]byte("image"))}))
	check(e)
	unit, e := typedindex.NewPackageUnitID(hash([]byte("unit")))
	check(e)
	name, e := typedindex.GeneratedPath(unit, "unicode.go")
	check(e)
	body := []byte("😀x\n")
	symbol := "scip-go gomod example.test v1 X#"
	targets := []typedindex.PlannedTarget{{ID: hash([]byte("target")), Dependencies: []string{}, Units: []typedindex.PackageUnitID{unit}}}
	source := typedindex.Source{Repository: "example.test/repo", Incarnation: "one", Generation: hash([]byte("source")), Commit: strings.Repeat("a", 40)}
	auth := typedindex.Authority{Enabled: true, Administrator: true, Source: source, Profile: typedindex.Epoch{Number: 1, Digest: profile.Digest()}, UniverseDigest: hash(wire(targets))}
	parent, e := typedindex.Admit(ctx, auth, profile, wire(typedindex.NewRequest(source, profile, 1, auth.UniverseDigest, "reader")))
	check(e)
	plan, e := typedindex.SealPackagePlan(ctx, parent, typedindex.PackagePlanDefinition{Schema: typedindex.PackagePlanSchema, ParentRequestDigest: parent.Digest(), Targets: targets, Units: []typedindex.PlannedUnit{{ID: unit, Imports: []typedindex.PackageUnitID{}, Documents: []string{name}}}, Documents: []typedindex.PlannedDocument{{Path: name, Member: "one", Unit: unit, Bytes: int64(len(body)), Digest: hash(body), Generated: true, ProvenanceDigest: hash([]byte("generator"))}}})
	check(e)
	request, e := typedindex.PlannedSuccessor(ctx, parent, plan.Digest())
	check(e)
	auth.ParentRequestDigest, auth.PlanDigest = parent.Digest(), plan.Digest()
	execution, e := typedindex.Admit(ctx, auth, profile, wire(request))
	check(e)
	member, e := proto.Marshal(&scip.Index{Metadata: &scip.Metadata{ToolInfo: &scip.ToolInfo{Name: "scip-go", Version: "0.2.7"}, ProjectRoot: "file:///workspace", TextDocumentEncoding: scip.TextEncoding_UTF8}, Documents: []*scip.Document{{RelativePath: name, PositionEncoding: scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart, Occurrences: []*scip.Occurrence{{Range: []int32{0, 4, 5}, Symbol: symbol, SymbolRoles: 1}}, Symbols: []*scip.SymbolInformation{{Symbol: symbol, Documentation: []string{"generated"}}}}}})
	check(e)
	bundle, e := typedindex.BuildBundle(ctx, execution, plan, []typedindex.UnitOutcome{{Unit: unit, State: typedindex.UnitComplete}}, []typedindex.MemberInput{{Name: "one", SCIP: member}}, map[string][]byte{name: body})
	check(e)
	id, e := typedworkspace.NewOwnerIdentity(parent, hash([]byte("chunk")), "lease")
	check(e)
	observation, e := typedworkspace.ObserveCapacity(ctx, base)
	check(e)
	gate := lifecycle.NewGate(base)
	owner, e := typedworkspace.CreateOwner(ctx, base, observation, id, typedworkspace.OwnerBudget{Bytes: 1 << 20, Inodes: 128}, gate)
	check(e)
	attempt := filepath.Join(base, id.RelativeName())
	inputs, e := typedworkspace.Copy(ctx, src, attempt, inv, gate)
	check(e)
	owner, e = typedworkspace.SaveOwnerInputs(ctx, base, id, owner.Digest(), invraw, inputs, gate)
	check(e)
	publication, e := typedworkspace.InstallPublication(ctx, attempt, parent, execution, plan, bundle, gate)
	check(e)
	owner, e = typedworkspace.SaveOwnerPublication(ctx, base, id, owner.Digest(), parent, execution, publication, gate)
	check(e)
	resolver := &workspaceResolver{base: base, authority: typedworkspace.RoutedAuthority{Owner: id, ManifestDigest: owner.Digest(), DirectoryDevice: owner.Directory.Device, DirectoryInode: owner.Directory.Inode, PublicationReceiptDigest: owner.Publication.Digest, Parent: parent, Execution: execution, RootDigest: bundle.RootDigest()}}
	service := codenav.New(codenav.Options{DataDir: filepath.Join(root, "no-git-repository"), RoutedResolver: resolver})
	q := codenav.Query{Repo: source.Repository, Revision: source.Commit, Path: name, Line: 0, Character: 2, Encoding: codenav.EncodingUTF16}
	for i := 0; i < 2; i++ {
		result, e := service.Definition(ctx, q)
		check(e)
		if result.Location == nil || result.Location.Range.Start.Character != 2 || result.Location.Range.End.Character != 3 {
			t.Fatal(result)
		}
	}
	if resolver.warm != 1 || resolver.checks != 4 {
		t.Fatal("cold/warm/current checks", resolver)
	}
	release, e := typedworkspace.AcquirePublicationMutation(ctx, attempt)
	check(e)
	release()
	generatedFile := filepath.Join(attempt, publication.Name, name)
	check(os.Chmod(generatedFile, 0600))
	check(os.WriteFile(generatedFile, []byte("😀y\n"), 0600))
	check(os.Chmod(generatedFile, 0444))
	if _, e = service.Definition(ctx, q); e == nil {
		t.Fatal("warm member cache bypassed generated-byte authentication")
	}
}
