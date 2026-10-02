package typedsandbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

const HostScratchBase = "/var/lib/phebs-typed-index"
const HostMkfsPath = "/usr/sbin/mkfs.ext4"
const hostImageBytes = ScratchBytes / 4096 * 4096
const hostOwnerSchema = "phebs-typed-host-scratch-v3"
const hostContainerLimit = 128

// HostScratchOptions are trusted operator/request inputs, never browser options.
// AttemptDigest is the exact durable per-lease attempt identity, never a retry
// ordinal or a truncated hash. MkfsDigest must be bound into the prospective request/profile before use.
// The base directory must already be provisioned root:root, mode 0700, with
// its empty root:root 0600 single-link .lock. Base binds the observed allocation
// root; replaced roots refuse even if a journal was copied into the replacement.
type HostBaseIdentity struct {
	Device    uint64 `json:"device"`
	Inode     uint64 `json:"inode"`
	BlockSize uint64 `json:"block_size"`
}

func (b HostBaseIdentity) valid() bool { return b.Inode != 0 && hostBudgetBlock(b.BlockSize) }

// HostScratchBudget is a conservative physical backing-filesystem envelope,
// not the ext4 scratch capacity or a reservation against unrelated writers.
type HostScratchBudget struct {
	Bytes  int64
	Inodes uint64
}

func hostBudgetBlock(block uint64) bool { return block > 0 && block <= 1<<20 && block&(block-1) == 0 }

// DeriveHostScratchBudget charges the fixed image rounded to actual filesystem
// blocks, main+pending 8KiB journals and sixteen metadata/inode units. No I/O.
func DeriveHostScratchBudget(block uint64) (HostScratchBudget, error) {
	if !hostBudgetBlock(block) {
		return HostScratchBudget{}, ErrRefused
	}
	round := func(n uint64) uint64 { return (n + block - 1) / block * block }
	bytes := round(uint64(hostImageBytes)) + 2*round(8192) + 16*block
	if bytes > math.MaxInt64 {
		return HostScratchBudget{}, ErrRefused
	}
	return HostScratchBudget{Bytes: int64(bytes), Inodes: 16}, nil
}

type HostScratchOptions struct {
	Base          HostBaseIdentity `json:"base"`
	RequestDigest string           `json:"request_digest"`
	AttemptDigest string           `json:"attempt_digest"`
	Socket        string           `json:"socket"`
	MkfsDigest    string           `json:"mkfs_digest"`
}

type HostScratchReceipt struct {
	Schema           string             `json:"schema"`
	Options          HostScratchOptions `json:"options"`
	Authority        ScratchAuthority   `json:"authority"`
	ObservedDirectIO bool               `json:"observed_direct_io"`
}

// Validate checks receipt shape and exact internal identity only. A caller must
// still obtain fresh native evidence through VerifyHostScratch before launch.
func (r HostScratchReceipt) Validate() error {
	if r.Schema != hostOwnerSchema || !r.Options.valid() || !r.ObservedDirectIO || r.Authority.Validate() != nil || r.Authority.Source != r.Options.root()+"/scratch" {
		return ErrRefused
	}
	return nil
}

type hostOwner struct {
	Schema      string             `json:"schema"`
	Options     HostScratchOptions `json:"options"`
	Phase       string             `json:"phase"`
	ImageDevice uint64             `json:"image_device"`
	ImageInode  uint64             `json:"image_inode"`
	Loop        int                `json:"loop"`
	Authority   ScratchAuthority   `json:"authority"`
}

func hostDigest(s string) bool {
	if len(s) != 71 || !strings.HasPrefix(s, "sha256:") {
		return false
	}
	for _, c := range s[7:] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
func (o HostScratchOptions) valid() bool {
	return o.Base.valid() && hostDigest(o.RequestDigest) && hostDigest(o.AttemptDigest) && hostDigest(o.MkfsDigest) && filepath.IsAbs(o.Socket) && filepath.Clean(o.Socket) == o.Socket && len(o.Socket) <= 512 && !strings.ContainsAny(o.Socket, "\x00\r\n")
}

// HostScratchRootName is the exact deterministic basename, independent of base
// device identity: replacement of the fixed base must refuse, not rename custody.
func HostScratchRootName(requestDigest, attemptDigest string) (string, error) {
	if !hostDigest(requestDigest) || !hostDigest(attemptDigest) {
		return "", ErrRefused
	}
	raw, _ := json.Marshal(struct {
		Request       string
		AttemptDigest string
	}{requestDigest, attemptDigest})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
func (o HostScratchOptions) root() string {
	name, _ := HostScratchRootName(o.RequestDigest, o.AttemptDigest)
	return HostScratchBase + "/" + name
}
func hostSelected() bool {
	_, selected := os.LookupEnv(dispatchadmission.ProductionEnvironment)
	return selected || dispatchadmission.ProductionWorkSelected() || dispatchadmission.ProductionSemanticSelected()
}
func hostAllowed(ctx context.Context, o HostScratchOptions) error {
	if ctx == nil || !o.valid() || hostSelected() {
		return ErrRefused
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}
func mkfsArgs(image string) []string {
	return []string{"-q", "-F", "-b", "4096", "-g", "17536", "-N", "262144", "-m", "0", "-E", "nodiscard,lazy_itable_init=0,lazy_journal_init=0", image}
}
func decodeHostOwner(raw []byte, o HostScratchOptions) (hostOwner, error) {
	var j hostOwner
	if len(raw) > 8192 || !o.valid() || json.Unmarshal(raw, &j) != nil || j.Schema != hostOwnerSchema || j.Options != o {
		return j, ErrCustody
	}
	canonical, _ := json.Marshal(j)
	if !bytes.Equal(raw, canonical) || j.Loop < -1 || j.Loop > 1048575 {
		return j, ErrCustody
	}
	switch j.Phase {
	case "new":
		if j.ImageDevice != 0 || j.ImageInode != 0 || j.Loop != -1 {
			return j, ErrCustody
		}
	case "image", "allocated", "formatting", "formatted", "retiring":
		if j.ImageInode == 0 || j.Loop != -1 {
			return j, ErrCustody
		}
	case "loop-selected":
		if j.ImageInode == 0 || j.Loop < 0 {
			return j, ErrCustody
		}
	case "ready":
		if j.ImageInode == 0 || j.Loop < 0 || j.Authority.Validate() != nil || j.Authority.Source != o.root()+"/scratch" || j.Authority.DeviceMinor != uint32(j.Loop) {
			return j, ErrCustody
		}
	default:
		return j, ErrCustody
	}
	if j.Phase != "ready" && j.Authority != (ScratchAuthority{}) {
		return j, ErrCustody
	}
	return j, nil
}

// Inventory is bounded at cap+one and includes stopped containers: they still
// own bind configuration. Cleanup requires a dedicated daemon: any bind or
// volume retains custody, even if its source appears unrelated on the host.
func hostContainers(ctx context.Context, socket string) ([]hostContainer, error) {
	info, err := os.Stat(socket)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		return nil, ErrCustody
	}
	transport := &http.Transport{Proxy: nil, DisableCompression: true, MaxResponseHeaderBytes: 16 << 10, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", socket)
	}}
	c := &client{http: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return ErrRefused }}}
	defer c.http.CloseIdleConnections()
	var list []hostContainer
	if _, err = c.request(ctx, "GET", "/containers/json?all=1&limit=129", nil, &list, 200); err != nil || len(list) > hostContainerLimit {
		return nil, ErrCustody
	}
	mounts := 0
	for _, item := range list {
		if !containerID(item.ID) {
			return nil, ErrCustody
		}
		mounts += len(item.Mounts)
		if mounts > 512 {
			return nil, ErrCustody
		}
		for _, m := range item.Mounts {
			if len(m.Source) > 512 {
				return nil, ErrCustody
			}
		}
	}
	return list, nil
}

type hostContainer struct {
	ID     string `json:"Id"`
	Mounts []struct{ Type, Source, Destination string }
}

func readHostOwner(root string, o HostScratchOptions) (hostOwner, error) {
	return readHostOwnerFile(root+"/owner.json", o)
}

func readHostOwnerFile(name string, o HostScratchOptions) (hostOwner, error) {
	before, err := os.Lstat(name)
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm() != 0600 || before.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return hostOwner{}, ErrCustody
	}
	f, err := os.Open(name)
	if err != nil {
		return hostOwner{}, ErrCustody
	}
	defer func() { _ = f.Close() }()
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) || after.Size() > 8192 {
		return hostOwner{}, ErrCustody
	}
	raw, err := io.ReadAll(io.LimitReader(f, 8193))
	if err != nil {
		return hostOwner{}, ErrCustody
	}
	return decodeHostOwner(raw, o)
}

func hostOwnerAdvance(a, b hostOwner) bool {
	if a.Schema != b.Schema || a.Options != b.Options {
		return false
	}
	if a.Phase != "new" && (a.ImageDevice != b.ImageDevice || a.ImageInode != b.ImageInode) {
		return false
	}
	if b.Phase == "retiring" {
		return a.Phase != "new" && a.Phase != "formatting"
	}
	next := map[string]string{"new": "image", "image": "allocated", "allocated": "formatting", "formatting": "formatted", "formatted": "loop-selected", "loop-selected": "ready"}
	if next[a.Phase] != b.Phase {
		return false
	}
	return a.Loop < 0 || a.Loop == b.Loop
}
func hostInodesFree(total, free uint64) bool {
	return total > 0 && free <= total && free >= 16 && free-16 > total/5
}

type hostLoopObservation struct {
	Number                      uint32
	Device, Inode, Offset, Size uint64
	Encryption, KeySize, Flags  uint32
}

func hostLoopMatches(j hostOwner, s hostLoopObservation) bool {
	return j.Loop >= 0 && s.Number == uint32(j.Loop) && s.Device == j.ImageDevice && s.Inode == j.ImageInode && s.Offset == 0 && s.Size == 0 && s.Encryption == 0 && s.KeySize == 0 && s.Flags == 0x10
}

// Historical recursive binds and privileged device access are not disproved by
// current mount paths. Cleanup therefore requires an empty dedicated daemon.
func hostContainerCustody(list []hostContainer) bool { return len(list) != 0 }

// These metadata predicates use Linux stat mode bits, without touching the host.
func hostAncestorMetadata(mode, uid, gid uint32) bool {
	return mode&0170000 == 0040000 && mode&07022 == 0 && uid == 0 && gid == 0
}

func hostPrivateFileMetadata(mode, uid, gid uint32, links uint64) bool {
	return mode&0177777 == 0100600 && uid == 0 && gid == 0 && links == 1
}

func hostCleanupPhase(phase string) bool { return phase != "formatting" }

func hostFullImageRequired(phase string) bool { return phase != "image" && phase != "retiring" }

func hostImageGeometry(size, blocks int64, full bool) bool {
	return size >= 0 && size <= hostImageBytes && blocks >= 0 &&
		(!full || size == hostImageBytes && blocks >= hostImageBytes/512)
}

func hostImageAllocation(size, blocks int64, full bool) bool {
	return hostImageGeometry(size, blocks, full) && blocks <= (hostImageBytes+65536)/512
}

// Real writes convert fallocate's unwritten mappings before scattered guest
// writes can split them. The existing allocation checks still govern admission.
func initializeHostImage(ctx context.Context, image io.WriterAt, size int64) error {
	if size != hostImageBytes {
		return ErrCustody
	}
	zero := make([]byte, 64<<10)
	for offset := int64(0); offset < size; {
		if err := ctx.Err(); err != nil {
			return err
		}
		chunk := zero[:min(int64(len(zero)), size-offset)]
		n, err := image.WriteAt(chunk, offset)
		if err != nil {
			return err
		}
		if n != len(chunk) {
			return io.ErrShortWrite
		}
		offset += int64(n)
	}
	return ctx.Err()
}
