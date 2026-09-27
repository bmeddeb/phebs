//go:build linux

package typedworkspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/custodybytes"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/scip-code/scip/bindings/go/scip"
	"golang.org/x/sys/unix"
	"google.golang.org/protobuf/proto"
)

type publicationFixture struct {
	dir               string
	gate              *lifecycle.Gate
	parent, execution typedindex.Admission
	plan              typedindex.PackagePlan
	bundle            typedindex.Bundle
}

func publicationJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func newPublicationFixture(t *testing.T) publicationFixture {
	t.Helper()
	_, dir, _, gate := fixture(t)
	tool := typedindex.Tool{Version: "0.2.7", Digest: digest([]byte("tool"))}
	p, err := typedindex.DecodeProfile(t.Context(), publicationJSON(t, typedindex.ProfileDefinition{Schema: typedindex.ProfileSchema, Name: "reduced", Provider: typedindex.ProviderID, Tools: typedindex.Tools{Bazel: tool, RulesGo: tool, Go: tool, Driver: tool, Indexer: tool, Planner: tool, Launcher: tool}, Config: typedindex.ReducedConfig(), Policy: typedindex.MeasuredPolicy(), BundleDigest: digest([]byte("inputs")), ImageDigest: digest([]byte("image"))}))
	if err != nil {
		t.Fatal(err)
	}
	unit, _ := typedindex.NewPackageUnitID(digest([]byte("unit")))
	targets := []typedindex.PlannedTarget{{ID: digest([]byte("target")), Dependencies: []string{}, Units: []typedindex.PackageUnitID{unit}}}
	source := typedindex.Source{Repository: "example.test/repo", Incarnation: "repo-1", Generation: digest([]byte("source")), Commit: strings.Repeat("a", 40)}
	auth := typedindex.Authority{Enabled: true, Administrator: true, Source: source, Profile: typedindex.Epoch{Number: 1, Digest: p.Digest()}, UniverseDigest: digest(publicationJSON(t, targets))}
	request := typedindex.NewRequest(source, p, 1, auth.UniverseDigest, "one")
	parent, err := typedindex.Admit(t.Context(), auth, p, publicationJSON(t, request))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := typedindex.SealPackagePlan(t.Context(), parent, typedindex.PackagePlanDefinition{Schema: typedindex.PackagePlanSchema, ParentRequestDigest: parent.Digest(), Targets: targets, Units: []typedindex.PlannedUnit{{ID: unit, Imports: []typedindex.PackageUnitID{}, Documents: []string{"a.go"}}}, Documents: []typedindex.PlannedDocument{{Member: "a", Path: "a.go", Unit: unit, Bytes: 1, Digest: digest([]byte("a"))}}})
	if err != nil {
		t.Fatal(err)
	}
	next, err := typedindex.PlannedSuccessor(t.Context(), parent, plan.Digest())
	if err != nil {
		t.Fatal(err)
	}
	auth.ParentRequestDigest, auth.PlanDigest = parent.Digest(), plan.Digest()
	execution, err := typedindex.Admit(t.Context(), auth, p, publicationJSON(t, next))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := proto.Marshal(&scip.Index{Metadata: &scip.Metadata{ToolInfo: &scip.ToolInfo{Name: "scip-go", Version: "0.2.7"}, ProjectRoot: "file:///workspace", TextDocumentEncoding: scip.TextEncoding_UTF8}, Documents: []*scip.Document{{RelativePath: "a.go", PositionEncoding: scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart, Occurrences: []*scip.Occurrence{{Range: []int32{0, 0, 1}, Symbol: "scip-go gomod example.test v1 A#", SymbolRoles: 1}}}}})
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := typedindex.BuildBundle(t.Context(), execution, plan, []typedindex.UnitOutcome{{Unit: unit, State: typedindex.UnitComplete}}, []typedindex.MemberInput{{Name: "a", SCIP: raw}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return publicationFixture{dir, gate, parent, execution, plan, bundle}
}
func (f publicationFixture) install(t *testing.T) PublicationReceipt {
	t.Helper()
	r, e := InstallPublication(t.Context(), f.dir, f.parent, f.execution, f.plan, f.bundle, f.gate)
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func (f publicationFixture) open(ctx context.Context, r PublicationReceipt) (*Publication, error) {
	return OpenPublication(ctx, f.dir, f.parent, f.execution, f.plan.Digest(), f.bundle.RootDigest(), r)
}
func TestPublicationInstallOpenAndLease(t *testing.T) {
	f := newPublicationFixture(t)
	r := f.install(t)
	raw, err := EncodePublicationReceipt(r)
	if err != nil {
		t.Fatal(err)
	}
	r, err = DecodePublicationReceipt(raw)
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.open(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Close() }()
	q, err := f.open(t.Context(), r)
	if err != nil {
		t.Fatal("second reader", err)
	}
	if err = q.Close(); err != nil {
		t.Fatal(err)
	}
	for _, name := range f.bundle.Names() {
		got, e := p.ReadMember(t.Context(), name)
		if e != nil || !bytes.Equal(got, f.bundle.Content(name)) {
			t.Fatalf("read %s: %v", name, e)
		}
	}
	if p.Root().Binding.RequestDigest != f.execution.Digest() {
		t.Fatal("wrong root")
	}
	for _, name := range []string{"../root.json", "root.json", "missing"} {
		if _, e := p.ReadMember(t.Context(), name); e == nil {
			t.Fatal("undeclared read", name)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if release, e := AcquirePublicationMutation(ctx, f.dir); e == nil {
		release()
		t.Fatal("writer overlapped reader")
	}
	if err = p.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = p.ReadMember(t.Context(), f.bundle.Names()[0]); err == nil {
		t.Fatal("read after close")
	}
	release, err := AcquirePublicationMutation(t.Context(), f.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx2, cancel2 := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel2()
	if reader, e := f.open(ctx2, r); e == nil {
		_ = reader.Close()
		t.Fatal("reader overlapped writer")
	}
}

func TestPublicationRefusesMutatedCustody(t *testing.T) {
	for _, kind := range []string{"missing", "extra", "corrupt", "inode", "symlink", "fifo", "hardlink", "writable", "directory-mode", "directory-inode", "root-inode", "wrong-receipt", "stale-plan", "stale-root", "stage", "unsafe-lock", "symlink-parent"} {
		t.Run(kind, func(t *testing.T) {
			f := newPublicationFixture(t)
			r := f.install(t)
			root := filepath.Join(f.dir, r.Name)
			name := f.bundle.Names()[0]
			file := filepath.Join(root, name)
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			switch kind {
			case "wrong-receipt":
				r.Nodes[0].Inode++
			case "stale-plan":
				r.PlanDigest = digest([]byte("other"))
			case "stale-root":
				r.RootDigest = digest([]byte("other"))
			case "stage":
				r.Name += ".stage"
			case "extra":
				must(os.Chmod(root, 0755))
				must(os.WriteFile(filepath.Join(root, "extra"), []byte("x"), 0444))
				must(os.Chmod(root, 0555))
			case "directory-mode":
				must(os.Chmod(filepath.Dir(file), 0755))
			case "directory-inode":
				must(os.Chmod(root, 0755))
				old := filepath.Dir(file)
				must(os.Rename(old, old+".old"))
				must(os.Mkdir(old, 0555))
				must(os.Chmod(root, 0555))
			case "root-inode":
				must(os.Rename(root, root+".old"))
				must(os.Mkdir(root, 0555))
			case "unsafe-lock":
				must(os.Remove(filepath.Join(f.dir, publicationLock)))
				must(os.Symlink(file, filepath.Join(f.dir, publicationLock)))
			case "symlink-parent":
				must(os.Rename(f.dir, f.dir+".old"))
				must(os.Symlink(f.dir+".old", f.dir))
			default:
				must(os.Chmod(filepath.Dir(file), 0755))
				switch kind {
				case "missing":
					must(os.Remove(file))
				case "corrupt":
					must(os.Chmod(file, 0644))
					b := f.bundle.Content(name)
					b[0] ^= 1
					must(os.WriteFile(file, b, 0444))
					must(os.Chmod(file, 0444))
				case "inode":
					must(os.Rename(file, file+".old"))
					must(os.WriteFile(file, f.bundle.Content(name), 0444))
					must(os.Remove(file + ".old"))
				case "symlink":
					must(os.Remove(file))
					must(os.Symlink("/etc/passwd", file))
				case "fifo":
					must(os.Remove(file))
					must(unix.Mkfifo(file, 0444))
				case "hardlink":
					must(os.Link(file, filepath.Join(f.dir, "other-link")))
				case "writable":
					must(os.Chmod(file, 0644))
				}
				must(os.Chmod(filepath.Dir(file), 0555))
			}
			if p, e := f.open(t.Context(), r); e == nil {
				_ = p.Close()
				t.Fatal("unsafe custody accepted")
			}
		})
	}
}

func TestPublicationReadDetectsReplacement(t *testing.T) {
	f := newPublicationFixture(t)
	r := f.install(t)
	p, e := f.open(t.Context(), r)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = p.Close() }()
	name := f.bundle.Names()[0]
	file := filepath.Join(f.dir, r.Name, name)
	if e = os.Chmod(filepath.Dir(file), 0755); e != nil {
		t.Fatal(e)
	}
	if e = os.Rename(file, file+".old"); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(file, f.bundle.Content(name), 0444); e != nil {
		t.Fatal(e)
	}
	if _, e = p.ReadMember(t.Context(), name); e == nil {
		t.Fatal("inode replacement accepted by existing reader")
	}
}

func TestPublicationCapacityCancellationAndResidue(t *testing.T) {
	for _, kind := range []string{"bytes", "inodes", "pressure", "canceled", "checkpoint"} {
		t.Run(kind, func(t *testing.T) {
			f := newPublicationFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			probe := func(*os.File) (space, error) {
				return space{bytes: 1 << 30, total: 2 << 30, inodes: 1000, block: 4096}, nil
			}
			switch kind {
			case "bytes":
				probe = func(*os.File) (space, error) { return space{bytes: 1, total: 1 << 30, inodes: 1000, block: 4096}, nil }
			case "inodes":
				probe = func(*os.File) (space, error) {
					return space{bytes: 1 << 30, total: 2 << 30, inodes: 1, block: 4096}, nil
				}
			case "pressure":
				probe = func(*os.File) (space, error) {
					return space{bytes: 1 << 20, total: 1 << 30, inodes: 1000, block: 4096}, nil
				}
			case "canceled":
				cancel()
			case "checkpoint":
				ctx = custodybytes.WithCheckpoint(ctx, func(context.Context) error { cancel(); return context.Canceled })
			}
			r, e := installPublication(ctx, f.dir, f.parent, f.execution, f.plan, f.bundle, f.gate, probe)
			if e == nil {
				t.Fatal("refusal missing")
			}
			entries, readErr := os.ReadDir(f.dir)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if kind != "checkpoint" {
				if len(entries) != 0 || r.Name != "" {
					t.Fatal("growth before capacity/cancellation", entries, r.Name)
				}
			} else {
				if !strings.HasSuffix(r.Name, ".stage") {
					t.Fatal("unnamed residue", r)
				}
				if _, e = EncodePublicationReceipt(r); e != nil {
					t.Fatal("unserializable residue", e)
				}
				if p, e := f.open(t.Context(), r); e == nil {
					_ = p.Close()
					t.Fatal("partial opened")
				}
			}
		})
	}
}

func TestPublicationReceiptAndBounds(t *testing.T) {
	f := newPublicationFixture(t)
	r := f.install(t)
	raw, e := EncodePublicationReceipt(r)
	if e != nil {
		t.Fatal(e)
	}
	for _, bad := range [][]byte{append(bytes.Clone(raw), ' '), append([]byte("{\"unknown\":1,"), raw[1:]...), append(bytes.Clone(raw), raw...), []byte("{}"), append([]byte("{\"schema\":\"duplicate\","), raw[1:]...)} {
		if _, e = DecodePublicationReceipt(bad); e == nil {
			t.Fatal("noncanonical receipt accepted")
		}
	}
	for _, kind := range []string{"count", "negative", "member-cap", "plan-cap", "path", "executable", "duplicate", "node-count"} {
		t.Run(kind, func(t *testing.T) {
			x := r
			x.Files = slices.Clone(r.Files)
			x.Nodes = slices.Clone(r.Nodes)
			switch kind {
			case "count":
				x.Files = make([]typedindex.BundleFile, maxPublicationFiles+1)
			case "node-count":
				x.Nodes = append(x.Nodes, Node{})
			case "duplicate":
				x.Files = append(x.Files, x.Files[len(x.Files)-1])
			case "negative":
				x.Files[0].Bytes = -1
			case "path":
				x.Files[0].Path = "../evil"
			case "executable":
				x.Files[0].Executable = true
			case "member-cap":
				for i := range x.Files {
					if strings.HasPrefix(x.Files[i].Path, "members/") {
						x.Files[i].Bytes = typedindex.MeasuredPolicy().SCIPBytes + 1
					}
				}
			case "plan-cap":
				for i := range x.Files {
					if x.Files[i].Path == "plan.json" {
						x.Files[i].Bytes = typedindex.MaxPlanBytes + 1
					}
				}
			}
			if _, e = EncodePublicationReceipt(x); e == nil {
				t.Fatal("bound accepted")
			}
		})
	}
	if _, e = InstallPublication(t.Context(), f.dir, typedindex.Admission{}, f.execution, f.plan, f.bundle, f.gate); !errors.Is(e, ErrCustody) {
		t.Fatal("empty parent", e)
	}
	if _, e = InstallPublication(t.Context(), f.dir, f.parent, f.execution, f.plan, typedindex.Bundle{}, f.gate); !errors.Is(e, ErrCustody) {
		t.Fatal("incomplete bundle", e)
	}
}

func TestPublicationPersistentPressureAndFreshIdentity(t *testing.T) {
	f := newPublicationFixture(t)
	gate := lifecycle.NewGateWithProbe(f.dir, func(context.Context, string) (lifecycle.Capacity, error) {
		t.Fatal("publication must observe its actual opened destination")
		return lifecycle.Capacity{}, nil
	})
	var prior PublicationReceipt
	for _, used := range []uint64{100, 85, 73, 95, 85, 73} {
		r, err := installPublication(t.Context(), f.dir, f.parent, f.execution, f.plan, f.bundle, gate, func(*os.File) (space, error) {
			return space{total: 100 << 20, bytes: (100 - used) << 20, inodes: 1000, block: 4096}, nil
		})
		if used >= 75 {
			if !errors.Is(err, lifecycle.ErrPressureRefusal) || r.Name != "" {
				t.Fatalf("latch lost at %d: %+v %v", used, r, err)
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			if prior.Name != "" && (prior.Name == r.Name || prior.Nodes[0].Inode == r.Nodes[0].Inode) {
				t.Fatal("reused published inode")
			}
			prior = r
		}
	}
}

func TestPublicationKindAndAggregateBounds(t *testing.T) {
	f := newPublicationFixture(t)
	receipt := f.install(t)
	for _, tc := range []struct {
		prefix string
		limit  int64
	}{
		{"plan.json", typedindex.MaxPlanBytes}, {"root.json", typedindex.MaxRootBytes},
		{"parent.json", typedindex.MaxRequestBytes}, {"request.json", typedindex.MaxRequestBytes},
		{"attempts/", typedindex.MaxAttemptBytes}, {"documents/", typedindex.MaxRouteBytes},
		{"symbols/", typedindex.MaxRouteBytes}, {"members/", typedindex.MaxSCIPMemberBytes},
	} {
		t.Run(tc.prefix, func(t *testing.T) {
			for _, extra := range []int64{0, 1} {
				files := slices.Clone(receipt.Files)
				for i := range files {
					if strings.HasPrefix(files[i].Path, tc.prefix) {
						files[i].Bytes = tc.limit + extra
					}
				}
				_, err := publicationLayout(files)
				if (err != nil) != (extra == 1) {
					t.Fatalf("limit+%d: %v", extra, err)
				}
			}
		})
	}
	for _, kind := range []string{"members", "generated"} {
		t.Run(kind, func(t *testing.T) {
			files := []typedindex.BundleFile{}
			for _, f := range receipt.Files {
				if !strings.HasPrefix(f.Path, "members/") {
					files = append(files, f)
				}
			}
			count := 4
			if kind == "generated" {
				count = 3
			}
			for i := 0; i < count; i++ {
				hash := digest([]byte{byte(i)})
				row := typedindex.BundleFile{Path: "members/" + hash[7:] + ".scip", Digest: hash, Bytes: typedindex.MaxSCIPMemberBytes}
				if kind == "generated" {
					row.Path = ".phebs-generated/" + hash[7:] + "/file.go"
					row.Bytes = typedindex.MaxGeneratedBytes
				}
				files = append(files, row)
			}
			slices.SortFunc(files, func(a, b typedindex.BundleFile) int { return strings.Compare(a.Path, b.Path) })
			if _, err := publicationLayout(files); err == nil {
				t.Fatal("aggregate exceeded")
			}
		})
	}
	// Token dimension pass refuses before allocating an oversized typed slice.
	receipt.Nodes = make([]Node, typedindex.MaxInventoryDirectories+maxPublicationFiles+2)
	if _, err := DecodePublicationReceipt(publicationJSON(t, receipt)); err == nil {
		t.Fatal("oversized node list accepted")
	}
}

func TestPublicationCancellationBeforeReadonlySeal(t *testing.T) {
	f := newPublicationFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	writes := 0
	ctx = custodybytes.WithCheckpoint(ctx, func(context.Context) error {
		writes++
		if writes == len(f.bundle.Names())+4 {
			cancel()
		}
		return nil
	})
	r, err := InstallPublication(ctx, f.dir, f.parent, f.execution, f.plan, f.bundle, f.gate)
	if !errors.Is(err, context.Canceled) || !strings.HasSuffix(r.Name, ".stage") {
		t.Fatal(r, err)
	}
	st, err := os.Stat(filepath.Join(f.dir, r.Name))
	if err != nil || st.Mode().Perm() != 0700 {
		t.Fatal("canceled directory was sealed", st, err)
	}
	if _, err = EncodePublicationReceipt(r); err != nil {
		t.Fatal("residue not serializable", err)
	}
}

func TestPublicationMutationCannotGrow(t *testing.T) {
	f := newPublicationFixture(t)
	if release, err := AcquirePublicationMutation(t.Context(), f.dir); err == nil {
		release()
		t.Fatal("mutation guard created unadmitted lock")
	}
	entries, err := os.ReadDir(f.dir)
	if err != nil || len(entries) != 0 {
		t.Fatal("mutation guard grew private custody", entries, err)
	}
	f.install(t)
	release, err := AcquirePublicationMutation(t.Context(), f.dir)
	if err != nil {
		t.Fatal("admitted existing lock refused", err)
	}
	release()
}

func TestPublicationProcessLockHelper(t *testing.T) {
	dir := os.Getenv("PHEBS_PUBLICATION_TEST_PARENT")
	if dir == "" {
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	release, err := AcquirePublicationMutation(ctx, dir)
	if os.Getenv("PHEBS_PUBLICATION_TEST_BLOCKED") == "1" {
		if err == nil {
			release()
			t.Fatal("cross-process writer passed reader")
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("wrong refusal", err)
		}
		return
	}
	if err != nil {
		t.Fatal("cross-process positive control refused", err)
	}
	release()
}

func TestPublicationCrossProcessReaderPin(t *testing.T) {
	f := newPublicationFixture(t)
	r := f.install(t)
	reader, err := f.open(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	run := func(blocked string) {
		t.Helper()
		child := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestPublicationProcessLockHelper$")
		child.Env = append(os.Environ(), "PHEBS_PUBLICATION_TEST_PARENT="+f.dir, "PHEBS_PUBLICATION_TEST_BLOCKED="+blocked)
		output, err := child.CombinedOutput()
		if err != nil {
			t.Fatalf("process lock: %v %s", err, output)
		}
	}
	run("1")
	if err = reader.Close(); err != nil {
		t.Fatal(err)
	}
	run("0")
}
