package typedsandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/lifecycle"
)

func hostFixture() HostScratchOptions {
	return HostScratchOptions{Base: HostBaseIdentity{Device: 1, Inode: 2, BlockSize: 4096}, RequestDigest: "sha256:" + strings.Repeat("a", 64), AttemptDigest: "sha256:" + strings.Repeat("d", 64), Socket: "/run/docker.sock", MkfsDigest: "sha256:" + strings.Repeat("b", 64)}
}
func TestHostAuthorityAndRecipe(t *testing.T) {
	o := hostFixture()
	if !o.valid() || len(strings.TrimPrefix(o.root(), HostScratchBase+"/")) != 64 {
		t.Fatal("valid owner refused")
	}
	next := o
	next.AttemptDigest = "sha256:" + strings.Repeat("e", 64)
	if next.root() == o.root() {
		t.Fatal("attempt aliases")
	}
	next = o
	next.RequestDigest = "sha256:" + strings.Repeat("c", 64)
	if next.root() == o.root() {
		t.Fatal("request aliases")
	}
	for _, edit := range []func(*HostScratchOptions){func(o *HostScratchOptions) { o.AttemptDigest = "" }, func(o *HostScratchOptions) { o.Socket = "../daemon" }, func(o *HostScratchOptions) { o.RequestDigest = "sha256:" + strings.Repeat("A", 64) }, func(o *HostScratchOptions) { o.MkfsDigest = "latest" }} {
		bad := o
		edit(&bad)
		if bad.valid() {
			t.Fatal("bad owner accepted")
		}
	}
	want := []string{"-q", "-F", "-b", "4096", "-g", "17536", "-N", "262144", "-m", "0", "-E", "nodiscard,lazy_itable_init=0,lazy_journal_init=0", "/proc/self/fd/4"}
	if !reflect.DeepEqual(mkfsArgs("/proc/self/fd/4"), want) || hostImageBytes != 4573401088 {
		t.Fatal("geometry or argv changed", hostImageBytes)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(hostAllowed(ctx, o), context.Canceled) {
		t.Fatal("cancellation")
	}
	t.Setenv(dispatchadmission.ProductionEnvironment, "")
	if hostAllowed(context.Background(), o) == nil {
		t.Fatal("selected legacy ceremony admitted")
	}
	// Selection refuses before any root check, lock, directory or native operation.
	if _, err := PrepareHostScratch(context.Background(), o, lifecycle.NewGate(HostScratchBase)); err == nil {
		t.Fatal("selected prepare admitted")
	}
}
func TestHostJournalClosedRecovery(t *testing.T) {
	o := hostFixture()
	initial := hostOwner{Schema: hostOwnerSchema, Options: o, Phase: "new", Loop: -1}
	raw, _ := json.Marshal(initial)
	if _, err := decodeHostOwner(raw, o); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{[]byte("{}"), append(raw, ' '), []byte(strings.Repeat("x", 8193)), []byte(strings.Replace(string(raw), `"loop":-1`, `"loop":-1,"loop":-1`, 1))} {
		if _, err := decodeHostOwner(bad, o); err == nil {
			t.Fatal("ambiguous journal admitted")
		}
	}
	states := []hostOwner{initial}
	for _, phase := range []string{"image", "allocated", "formatting", "formatted", "loop-selected", "ready", "retiring"} {
		j := states[len(states)-1]
		j.Phase = phase
		if phase == "image" {
			j.ImageDevice = 9
			j.ImageInode = 10
		}
		if phase == "loop-selected" {
			j.Loop = 3
		}
		if phase == "ready" {
			j.Authority = ScratchAuthority{Source: o.root() + "/scratch", DeviceMajor: 7, DeviceMinor: 3, BlockSize: 4096, Blocks: 100, Inodes: ScratchInodes, ImageBytes: hostImageBytes}
		}
		if phase == "retiring" {
			j.Loop = -1
			j.Authority = ScratchAuthority{}
		}
		states = append(states, j)
	}
	for i, j := range states {
		raw, _ := json.Marshal(j)
		if _, err := decodeHostOwner(raw, o); err != nil {
			t.Fatal(j.Phase, err)
		}
		if i > 0 && !hostOwnerAdvance(states[i-1], j) {
			t.Fatal("valid durable advance refused", j.Phase)
		}
	}
	if hostOwnerAdvance(states[0], states[3]) || hostOwnerAdvance(states[3], states[1]) {
		t.Fatal("stage skip or rollback")
	}
	bad := states[5]
	bad.ImageInode++
	if hostOwnerAdvance(states[4], bad) {
		t.Fatal("backing swap")
	}
	bad = states[6]
	bad.Loop++
	if hostOwnerAdvance(states[5], bad) {
		t.Fatal("loop swap")
	}
	root := t.TempDir()
	if err := os.WriteFile(root+"/owner.json", raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readHostOwner(root, o); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(root+"/owner.json", root+"/real"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root+"/real", root+"/owner.json"); err != nil {
		t.Fatal(err)
	}
	if _, err := readHostOwner(root, o); err == nil {
		t.Fatal("symlink owner")
	}
}
func TestHostLoopAndPressureRefusals(t *testing.T) {
	j := hostOwner{Loop: 3, ImageDevice: 9, ImageInode: 10}
	obs := hostLoopObservation{Number: 3, Device: 9, Inode: 10, Flags: 0x10}
	if !hostLoopMatches(j, obs) {
		t.Fatal("DIO refused")
	}
	for _, edit := range []func(*hostLoopObservation){func(s *hostLoopObservation) { s.Flags = 0 }, func(s *hostLoopObservation) { s.Flags |= 4 }, func(s *hostLoopObservation) { s.Offset = 4096 }, func(s *hostLoopObservation) { s.Size = 4096 }, func(s *hostLoopObservation) { s.Encryption = 1 }, func(s *hostLoopObservation) { s.KeySize = 1 }, func(s *hostLoopObservation) { s.Device++ }, func(s *hostLoopObservation) { s.Inode++ }, func(s *hostLoopObservation) { s.Number++ }} {
		bad := obs
		edit(&bad)
		if hostLoopMatches(j, bad) {
			t.Fatal("changed loop accepted")
		}
	}
	for _, c := range []struct {
		total, free uint64
		want        bool
	}{{1000, 217, true}, {1000, 216, false}, {1000, 15, false}, {0, 0, false}, {100, 101, false}} {
		if hostInodesFree(c.total, c.free) != c.want {
			t.Fatal("inode pressure", c)
		}
	}
	a := testScratchAuthority()
	mount := a.Source
	valid := "101 24 " + scratchDevice(a) + " / " + mount + " rw,nosuid,nodev - ext4 /dev/loop3 rw\n"
	if verifyScratchMountpoint(valid, a, mount) != nil {
		t.Fatal("host mount refused")
	}
	for _, raw := range []string{strings.Replace(valid, "nosuid,", "", 1), strings.Replace(valid, " rw,nosuid", " ro,nosuid", 1), valid + strings.Replace(valid, mount, mount+"/nested", 1), strings.Replace(valid, " - ext4", " shared:4 - ext4", 1)} {
		if verifyScratchMountpoint(raw, a, mount) == nil {
			t.Fatal("unsafe mount accepted")
		}
	}
}
func TestHostContainerInventoryBound(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "typed-host-inventory-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(root) }()
	socket := filepath.Join(root, "d.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	count := 1
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != apiVersion+"/containers/json" || r.URL.Query().Get("all") != "1" || r.URL.Query().Get("limit") != "129" {
			t.Error("unbounded or mutable Docker request")
		}
		rows := make([]hostContainer, count)
		for i := range rows {
			rows[i].ID = strings.Repeat("a", 64)
		}
		_ = json.NewEncoder(w).Encode(rows)
	}))
	server.Listener = listener
	server.Start()
	defer server.Close()
	for _, n := range []int{1, 128, 129} {
		count = n
		rows, err := hostContainers(context.Background(), socket)
		if n <= 128 && (err != nil || len(rows) != n) || n > 128 && err == nil {
			t.Fatal("inventory limit", n, err)
		}
	}
}

func TestHostContainerCustodyRequiresDedicatedDaemon(t *testing.T) {
	scratch := hostFixture().root() + "/scratch"
	for _, kind := range []string{"bind", "volume"} {
		for _, source := range []string{"/", HostScratchBase, filepath.Dir(scratch), scratch, scratch + "/cache", scratch + "-other", "/historical-alias-now-unmounted", ""} {
			t.Run(kind+source, func(t *testing.T) {
				var row hostContainer
				row.Mounts = append(row.Mounts, struct{ Type, Source, Destination string }{kind, source, "/data"})
				// State is deliberately absent: all=1 inventories running and stopped.
				if !hostContainerCustody([]hostContainer{row}) {
					t.Fatal("container custody ignored")
				}
			})
		}
	}
	var memoryOnly hostContainer
	memoryOnly.Mounts = append(memoryOnly.Mounts, struct{ Type, Source, Destination string }{"tmpfs", "", "/tmp"})
	if hostContainerCustody(nil) || !hostContainerCustody([]hostContainer{{}}) || !hostContainerCustody([]hostContainer{memoryOnly}) {
		t.Fatal("cleanup requires an empty daemon, including unclassified device access")
	}
}

func TestHostPrivateMetadata(t *testing.T) {
	for _, tc := range []struct {
		name              string
		mode, uid, gid    uint32
		links             uint64
		ancestor, private bool
	}{
		{"ancestor", 0040755, 0, 0, 2, true, false},
		{"private ancestor", 0040700, 0, 0, 2, true, false},
		{"group writable ancestor", 0040775, 0, 0, 2, false, false},
		{"other writable ancestor", 0040757, 0, 0, 2, false, false},
		{"ancestor symlink", 0120755, 0, 0, 1, false, false},
		{"ancestor owner", 0040755, 1, 0, 2, false, false},
		{"ancestor group", 0040755, 0, 1, 2, false, false},
		{"ancestor setgid", 0042755, 0, 0, 2, false, false},
		{"lock", 0100600, 0, 0, 1, false, true},
		{"lock hardlink", 0100600, 0, 0, 2, false, false},
		{"lock setuid", 0104600, 0, 0, 1, false, false},
		{"lock setgid", 0102600, 0, 0, 1, false, false},
		{"lock sticky", 0101600, 0, 0, 1, false, false},
		{"lock group readable", 0100640, 0, 0, 1, false, false},
		{"lock owner", 0100600, 1, 0, 1, false, false},
		{"lock group", 0100600, 0, 1, 1, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if hostAncestorMetadata(tc.mode, tc.uid, tc.gid) != tc.ancestor || hostPrivateFileMetadata(tc.mode, tc.uid, tc.gid, tc.links) != tc.private {
				t.Fatal("metadata authority mismatch")
			}
		})
	}
}

func TestHostInterruptedFormattingAndPartialRetirement(t *testing.T) {
	o := hostFixture()
	j := hostOwner{Schema: hostOwnerSchema, Options: o, Phase: "formatting", ImageDevice: 1, ImageInode: 2, Loop: -1}
	raw, _ := json.Marshal(j)
	recovered, err := decodeHostOwner(raw, o)
	if err != nil || hostCleanupPhase(recovered.Phase) {
		t.Fatal("lost formatter custody reclaimable", err)
	}
	retiring := j
	retiring.Phase = "retiring"
	if hostOwnerAdvance(j, retiring) {
		t.Fatal("formatting can retire")
	}
	for _, size := range []int64{0, 4096, hostImageBytes} {
		j.Phase = "image"
		if !hostOwnerAdvance(j, retiring) {
			t.Fatal("partial image cannot retire")
		}
		raw, _ = json.Marshal(retiring)
		recovered, err = decodeHostOwner(raw, o)
		if err != nil || !hostCleanupPhase(recovered.Phase) || !hostImageAllocation(size, size/512, hostFullImageRequired(recovered.Phase)) {
			t.Fatal("retiring exact partial image refused", size, err)
		}
	}
	for _, phase := range []string{"allocated", "formatted", "loop-selected", "ready"} {
		if hostImageAllocation(4096, 8, hostFullImageRequired(phase)) {
			t.Fatal("partial allocation accepted", phase)
		}
	}
	if hostImageAllocation(hostImageBytes+1, 0, false) || hostImageAllocation(0, -1, false) || hostImageAllocation(0, (hostImageBytes+65536)/512+1, false) {
		t.Fatal("invalid allocation accepted")
	}
}

func TestHostExactLeaseJournalIdentity(t *testing.T) {
	first := hostFixture()
	second := first
	// Two leases of the same logical scheduler retry must never share scratch.
	second.AttemptDigest = "sha256:" + strings.Repeat("e", 64)
	if first.root() == second.root() {
		t.Fatal("distinct lease custody aliases")
	}
	owner := hostOwner{Schema: hostOwnerSchema, Options: first, Phase: "new", Loop: -1}
	raw, err := json.Marshal(owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = decodeHostOwner(raw, first); err != nil {
		t.Fatal("positive exact owner", err)
	}
	if _, err = decodeHostOwner(raw, second); !errors.Is(err, ErrCustody) {
		t.Fatal("borrowed old lease journal", err)
	}
	secondOwner := owner
	secondOwner.Options = second
	secondOwner.Phase = "image"
	secondOwner.ImageInode = 1
	if hostOwnerAdvance(owner, secondOwner) {
		t.Fatal("journal advance changed lease")
	}
	for name, bad := range map[string]string{
		"old-schema":         strings.Replace(string(raw), hostOwnerSchema, "phebs-typed-host-scratch-v1", 1),
		"numeric-attempt":    strings.Replace(string(raw), `"attempt_digest":"`+first.AttemptDigest+`"`, `"attempt":1`, 1),
		"numeric-and-digest": strings.Replace(string(raw), `"attempt_digest":`, `"attempt":1,"attempt_digest":`, 1),
		"upper-case":         strings.Replace(string(raw), first.AttemptDigest, strings.ToUpper(first.AttemptDigest), 1),
	} {
		t.Run(name, func(t *testing.T) {
			if bad == string(raw) {
				t.Fatal("ineffective mutation")
			}
			if _, err := decodeHostOwner([]byte(bad), first); !errors.Is(err, ErrCustody) {
				t.Fatal("legacy/ambiguous authority accepted", err)
			}
		})
	}
}

func TestHostBackingBudgetAndRootName(t *testing.T) {
	o := hostFixture()
	name, err := HostScratchRootName(o.RequestDigest, o.AttemptDigest)
	if err != nil || name != filepath.Base(o.root()) || name != "3aa40082b93654f16edc5c81fbdf597c6b5b666b395143145422066f9935e9bc" {
		t.Fatal(name, err)
	}
	// Literal independently counted extents: fixed 4,573,401,088-byte image,
	// two journal extents and sixteen metadata blocks. Do not mirror the helper.
	for _, tc := range []struct {
		block uint64
		bytes int64
	}{
		{512, 4573425664}, {4096, 4573483008}, {65536, 4574609408}, {1 << 20, 4592762880},
	} {
		got, e := DeriveHostScratchBudget(tc.block)
		if e != nil || got.Bytes != tc.bytes || got.Inodes != 16 {
			t.Fatal(tc, got, e)
		}
	}
	for _, block := range []uint64{0, 3, 1 << 21, ^uint64(0)} {
		if _, err = DeriveHostScratchBudget(block); err == nil {
			t.Fatal("bad geometry", block)
		}
	}
	if _, err = HostScratchRootName("", o.AttemptDigest); err == nil {
		t.Fatal("invalid root")
	}
	j := hostOwner{Schema: hostOwnerSchema, Options: o, Phase: "new", Loop: -1}
	raw, _ := json.Marshal(j)
	if _, err = decodeHostOwner([]byte(strings.Replace(string(raw), hostOwnerSchema, "phebs-typed-host-scratch-v2", 1)), o); err == nil {
		t.Fatal("unbound legacy journal")
	}
	for _, change := range []func(*HostScratchOptions){func(o *HostScratchOptions) { o.Base.Inode = 0 }, func(o *HostScratchOptions) { o.Base.BlockSize = 0 }} {
		bad := o
		change(&bad)
		if bad.valid() {
			t.Fatal("missing root authority")
		}
	}
}

func TestHostReceiptShape(t *testing.T) {
	o := hostFixture()
	a := testScratchAuthority()
	a.Source = o.root() + "/scratch"
	r := HostScratchReceipt{Schema: hostOwnerSchema, Options: o, Authority: a, ObservedDirectIO: true}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name   string
		change func(*HostScratchReceipt)
	}{
		{"schema", func(r *HostScratchReceipt) { r.Schema = "phebs-typed-host-scratch-v2" }},
		{"base", func(r *HostScratchReceipt) { r.Options.Base.Inode = 0 }},
		{"tool", func(r *HostScratchReceipt) { r.Options.MkfsDigest = "" }},
		{"dio", func(r *HostScratchReceipt) { r.ObservedDirectIO = false }},
		{"authority", func(r *HostScratchReceipt) { r.Authority.Inodes++ }},
		{"source", func(r *HostScratchReceipt) { r.Authority.Source = testScratchAuthority().Source }},
		{"attempt", func(r *HostScratchReceipt) { r.Options.AttemptDigest = "sha256:" + strings.Repeat("c", 64) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			changed := r
			tt.change(&changed)
			if changed.Validate() == nil {
				t.Fatal("invalid receipt admitted")
			}
		})
	}
}

type hostInitializationWriter struct {
	offset  int64
	calls   int
	cancel  context.CancelFunc
	short   bool
	fail    bool
	invalid bool
}

func (w *hostInitializationWriter) WriteAt(p []byte, offset int64) (int, error) {
	w.calls++
	if offset != w.offset || len(p) == 0 || len(p) > 64<<10 || w.calls == 1 && !bytes.Equal(p, make([]byte, len(p))) {
		w.invalid = true
	}
	w.offset += int64(len(p))
	if w.cancel != nil {
		w.cancel()
	}
	if w.fail {
		return 0, io.ErrClosedPipe
	}
	if w.short {
		return len(p) - 1, nil
	}
	return len(p), nil
}
func TestHostInitializationAndRetirementGeometry(t *testing.T) {
	for _, tc := range []struct {
		name                string
		short, fail, cancel bool
		want                error
	}{
		{name: "complete"}, {name: "short", short: true, want: io.ErrShortWrite}, {name: "error", fail: true, want: io.ErrClosedPipe}, {name: "cancel", cancel: true, want: context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			w := &hostInitializationWriter{short: tc.short, fail: tc.fail}
			if tc.cancel {
				w.cancel = cancel
			}
			err := initializeHostImage(ctx, w, hostImageBytes)
			if !errors.Is(err, tc.want) || w.invalid {
				t.Fatal("initialization", err, w)
			}
			if tc.want == nil && (w.offset != hostImageBytes || w.calls != (int(hostImageBytes)+(64<<10)-1)/(64<<10)) {
				t.Fatal("incomplete initialization", w)
			}
			if tc.want != nil && w.calls != 1 {
				t.Fatal("continued after failure", w)
			}
		})
	}
	w := &hostInitializationWriter{}
	if initializeHostImage(t.Context(), w, hostImageBytes+1) == nil || w.calls != 0 {
		t.Fatal("wrong geometry written")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if !errors.Is(initializeHostImage(ctx, w, hostImageBytes), context.Canceled) || w.calls != 0 {
		t.Fatal("canceled image written")
	}
	blocks := int64((hostImageBytes+65536)/512 + 1)
	if !hostImageGeometry(hostImageBytes, blocks, true) || hostImageAllocation(hostImageBytes, blocks, true) {
		t.Fatal("retirement conflated with admission")
	}
	for _, tc := range []struct {
		size, blocks int64
		full         bool
	}{{-1, 0, false}, {hostImageBytes + 1, 0, false}, {0, -1, false}, {4096, 8, true}, {hostImageBytes, 0, true}} {
		if hostImageGeometry(tc.size, tc.blocks, tc.full) {
			t.Fatal("invalid retirement geometry", tc)
		}
	}
}
