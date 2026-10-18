//go:build linux

package t421

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestLinuxInputTreeControlRightsRetainOwnershipOnRefusal(t *testing.T) {
	rights := unix.UnixRights(17)
	malformed := append(bytes.Clone(rights), make([]byte, unix.CmsgLen(0))...)
	badAligned := bytes.Clone(rights)
	// Native endian CMSG length: preserve one complete right and add one
	// invalid byte without asking ParseUnixRights to access a partial int32.
	if len(badAligned) < unix.CmsgLen(5) {
		t.Fatal("fixture control space")
	}
	if strconv.IntSize == 64 {
		binary.NativeEndian.PutUint64(badAligned, uint64(unix.CmsgLen(5)))
	} else {
		t.Fatal("64-bit platforms only")
	}
	for _, test := range []struct {
		name string
		raw  []byte
		want []int
	}{
		{"later malformed control", malformed, []int{17}},
		{"partial rights word", badAligned, []int{17}},
		{"short header", []byte{1}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, valid := inputTreeControlRights(test.raw, nil)
			if valid || !slices.Equal(got, test.want) {
				t.Fatal("delivered ownership lost or malformed control accepted", got, valid)
			}
		})
	}
	peer := &unix.Ucred{Pid: 1, Uid: 2, Gid: 3}
	for _, test := range []struct {
		name     string
		raw      []byte
		expected *unix.Ucred
		valid    bool
	}{
		{"exact", unix.UnixCredentials(peer), peer, true},
		{"missing", nil, peer, false},
		{"foreign", unix.UnixCredentials(&unix.Ucred{Pid: 2, Uid: 2, Gid: 3}), peer, false},
		{"duplicate", append(unix.UnixCredentials(peer), unix.UnixCredentials(peer)...), peer, false},
		{"unexpected", unix.UnixCredentials(peer), nil, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, valid := inputTreeControlRights(test.raw, test.expected)
			if valid != test.valid {
				t.Fatal("credential admission", valid)
			}
		})
	}
}

func TestLinuxInputTreeSocketRefusesAndClosesDeliveredRights(t *testing.T) {
	for _, count := range []int{1, 2, 65} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = unix.Close(pair[0]); _ = unix.Close(pair[1]) }()
			file, err := os.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = file.Close() }()
			fds := make([]int, count)
			for i := range fds {
				fds[i] = int(file.Fd())
			}
			before := linuxInputFDCount(t)
			raw, err := encodeInputTreeFrame(treeWireReady, 1, []byte("neutral"))
			if err != nil {
				t.Fatal(err)
			}
			if unix.Sendmsg(pair[0], raw, unix.UnixRights(fds...), nil, unix.MSG_NOSIGNAL) != nil {
				t.Fatal("send rights")
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			wire := inputTreeSocket{fd: pair[1]}
			if _, root, err := wire.receive(ctx, treeWireReady, false); err == nil || root != nil || linuxInputFDCount(t) != before {
				t.Fatal("refused/truncated rights leaked", err)
			}
		})
	}
}

func TestLinuxInputTreeSocketWrongFrameClosesAcceptedRight(t *testing.T) {
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unix.Close(pair[0]); _ = unix.Close(pair[1]) }()
	file, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	before := linuxInputFDCount(t)
	raw, err := encodeInputTreeFrame(treeWireAck, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if unix.Sendmsg(pair[0], raw, unix.UnixRights(int(file.Fd())), nil, unix.MSG_NOSIGNAL) != nil {
		t.Fatal("send")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, root, err := (&inputTreeSocket{fd: pair[1]}).receive(ctx, treeWireReady, true); err == nil || root != nil || linuxInputFDCount(t) != before {
		t.Fatal("accepted right lost on later frame refusal", err)
	}
}

func TestLinuxInputTreeDeadlineAndLiveMountBinding(t *testing.T) {
	for _, duration := range []time.Duration{-time.Second, 31 * time.Minute} {
		raw := make([]byte, 48)
		binary.BigEndian.PutUint64(raw, uint64(time.Now().Add(duration).UnixNano()))
		if _, _, err := inputTreeDeadline(raw); err == nil {
			t.Fatal("invalid deadline accepted")
		}
	}
	raw := make([]byte, 48)
	binary.BigEndian.PutUint64(raw, uint64(time.Now().Add(time.Minute).UnixNano()))
	ctx, cancel, err := inputTreeDeadline(raw)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if ctx.Err() == nil {
		t.Fatal("cancel absent")
	}
	const line = "19 1 0:1 / /private/stage ro,nosuid,nodev - tmpfs tmpfs rw,size=2097152k,nr_inodes=100002\n"
	if !inputTreeHelperMountMatches([]byte(line), "/private/stage", 19) {
		t.Fatal("actual mount fixture refused")
	}
	for _, bad := range []string{
		strings.Replace(line, "ro,nosuid,nodev", "rw,nosuid,nodev", 1), strings.Replace(line, "/private/stage", "/other", 1),
		strings.Replace(line, " - tmpfs", " shared:1 - tmpfs", 1), strings.Replace(line, "- tmpfs tmpfs", "- ext4 /dev/loop0", 1),
		strings.Replace(line, " / /private", " /subtree /private", 1), line + line, strings.TrimSuffix(line, "\n"),
	} {
		if inputTreeHelperMountMatches([]byte(bad), "/private/stage", 19) {
			t.Fatal("unbound/foreign/writable mount accepted")
		}
	}
}

func TestLinuxInputTreeDroppedStatusRequiresClosedPrivileges(t *testing.T) {
	const good = "Name:\tinput-tree\nCapEff:\t0000000000000000\nCapPrm:\t0000000000000000\nCapInh:\t0000000000000000\nCapAmb:\t0000000000000000\nCapBnd:\t0000000000000000\nNoNewPrivs:\t1\n"
	if !inputTreeDroppedStatus([]byte(good)) {
		t.Fatal("closed privilege status refused")
	}
	for _, field := range []string{"CapEff", "CapPrm", "CapInh", "CapAmb", "CapBnd", "NoNewPrivs"} {
		for _, bad := range []string{
			strings.Replace(good, field+":", "Absent:", 1),
			good + field + ":\t0\n",
			strings.Replace(good, field+":\t0000000000000000", field+":\t0000000000000001", 1),
		} {
			if bad != good && inputTreeDroppedStatus([]byte(bad)) {
				t.Fatal("missing/duplicate/retained capability accepted", field)
			}
		}
	}
	if inputTreeDroppedStatus([]byte(strings.Replace(good, "NoNewPrivs:\t1", "NoNewPrivs:\t0", 1))) {
		t.Fatal("exec privilege elevation permitted")
	}
}

func TestLinuxInputTreeGatherRejectsAliasesLinksAndCancellation(t *testing.T) {
	base := t.TempDir()
	parent := filepath.Join(base, "parent")
	source := filepath.Join(base, "source")
	for _, name := range []string{parent, source} {
		if os.Mkdir(name, 0o700) != nil {
			t.Fatal("mkdir")
		}
	}
	if os.WriteFile(filepath.Join(source, "data"), []byte("neutral"), 0o600) != nil {
		t.Fatal("fixture")
	}
	for _, test := range []struct {
		name    string
		setup   func() func()
		sources []LinuxInputTreeSource
		cancel  bool
	}{
		{"source inside parent", nil, []LinuxInputTreeSource{{Name: "source", Path: parent}}, false},
		{"overlap", nil, []LinuxInputTreeSource{{Name: "one", Path: source}, {Name: "two", Path: source}}, false},
		{"duplicate name", nil, []LinuxInputTreeSource{{Name: "one", Path: source}, {Name: "one", Path: source}}, false},
		{"symlink", func() func() {
			link := filepath.Join(source, "link")
			if os.Symlink("data", link) != nil {
				t.Fatal("link")
			}
			return func() { _ = os.Remove(link) }
		}, []LinuxInputTreeSource{{Name: "source", Path: source}}, false},
		{"hardlink", func() func() {
			link := filepath.Join(source, "link")
			if os.Link(filepath.Join(source, "data"), link) != nil {
				t.Fatal("link")
			}
			return func() { _ = os.Remove(link) }
		}, []LinuxInputTreeSource{{Name: "source", Path: source}}, false},
		{"cancel", nil, []LinuxInputTreeSource{{Name: "source", Path: source}}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.setup != nil {
				defer test.setup()()
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if test.cancel {
				cancel()
			}
			before := linuxInputFDCount(t)
			if roots, _, err := gatherInputTreeSources(ctx, parent, test.sources); err == nil || roots != nil || linuxInputFDCount(t) != before {
				t.Fatal("refusal leaked or invalid selection accepted", err)
			}
		})
	}
}

func TestLinuxInputTreeRefusesUninitializedAndConcurrentConstruction(t *testing.T) {
	ctx := t.Context()
	tree := &LinuxExecutionInputTree{}
	if _, err := tree.Check(ctx); err == nil {
		t.Fatal("uninitialized tree accepted")
	}
	if err := tree.WithRoot(ctx, func(*os.File) error { t.Fatal("callback reached"); return nil }); err == nil {
		t.Fatal("uninitialized loan accepted")
	}
	if err := tree.Close(ctx); err == nil {
		t.Fatal("uninitialized close accepted")
	}
	parent := t.TempDir()
	helper := &LinuxInputTreeHelper{tool: &LinuxExecutionToolCustody{}}
	helper.mu.Lock()
	defer helper.mu.Unlock()
	if tree, err := ProtectLinuxExecutionInputTree(ctx, parent, []LinuxInputTreeSource{{Name: "source", Path: "/not-read"}}, helper); tree != nil || err == nil {
		t.Fatal("concurrent constructor queued work or accepted")
	}
}

func TestLinuxInputTreeProtectedManifestRejectsExtraMissingAndChangedInputs(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(string)
		valid  bool
	}{
		{"complete", nil, true},
		{"extra top", func(base string) {
			if os.Mkdir(filepath.Join(base, "unexpected"), 0o700) != nil {
				t.Fatal("fixture")
			}
		}, false},
		{"extra child", func(base string) {
			if os.WriteFile(filepath.Join(base, "source", "extra"), nil, 0o400) != nil {
				t.Fatal("fixture")
			}
		}, false},
		{"missing", func(base string) {
			if os.Remove(filepath.Join(base, "source", "data")) != nil {
				t.Fatal("fixture")
			}
		}, false},
		{"content", func(base string) {
			if os.WriteFile(filepath.Join(base, "source", "data"), []byte("abd"), 0o400) != nil {
				t.Fatal("fixture")
			}
		}, false},
		{"mode", func(base string) {
			if os.Chmod(filepath.Join(base, "source", "data"), 0o500) != nil {
				t.Fatal("fixture")
			}
		}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			base := t.TempDir()
			if os.Mkdir(filepath.Join(base, "source"), 0o700) != nil || os.WriteFile(filepath.Join(base, "source", "data"), []byte("abc"), 0o600) != nil {
				t.Fatal("fixture")
			}
			if test.mutate != nil {
				test.mutate(base)
			}
			if test.name != "missing" && test.name != "mode" {
				if os.Chmod(filepath.Join(base, "source", "data"), 0o400) != nil {
					t.Fatal("fixture")
				}
			}
			var census inputTreeCensus
			for _, row := range []inputTreeEntry{{name: "source", mode: 0o700}, {name: "source/data", mode: 0o400, size: 3, digest: sha256.Sum256([]byte("abc"))}} {
				if census.add(row) != nil {
					t.Fatal("census")
				}
			}
			root, err := os.OpenRoot(base)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = root.Close() }()
			if err := verifyProtectedInputTree(t.Context(), root, census); (err == nil) != test.valid {
				t.Fatal("protected census mismatch", err)
			}
		})
	}
}
