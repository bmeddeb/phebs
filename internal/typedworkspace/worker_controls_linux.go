//go:build linux

package typedworkspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"io"
	"os"

	"github.com/bmeddeb/phebs/internal/typedsandbox"
	"golang.org/x/sys/unix"
)

func workerReadOnly(root *os.File) error {
	var fs unix.Statfs_t
	if unix.Fstatfs(int(root.Fd()), &fs) != nil || fs.Flags&unix.ST_RDONLY == 0 {
		return ErrCustody
	}
	return nil
}
func workerSameStat(a, b unix.Stat_t) bool {
	return a.Dev == b.Dev && a.Ino == b.Ino && a.Mode == b.Mode && a.Uid == b.Uid && a.Gid == b.Gid && a.Nlink == b.Nlink && a.Size == b.Size && a.Mtim == b.Mtim && a.Ctim == b.Ctim
}
func workerFileStat(root *os.File, name string, directory unix.Stat_t, limit int) (unix.Stat_t, error) {
	var st unix.Stat_t
	if unix.Fstatat(int(root.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW) != nil || st.Mode != unix.S_IFREG|0444 || st.Nlink != 1 || st.Dev != directory.Dev || st.Uid != directory.Uid || st.Gid != directory.Gid || st.Size <= 0 || st.Size > int64(limit) {
		return st, ErrCustody
	}
	return st, nil
}
func workerReadFile(ctx context.Context, root *os.File, name string, directory unix.Stat_t, limit int, expected *OwnerControl) ([]byte, unix.Stat_t, error) {
	if err := ctx.Err(); err != nil {
		return nil, unix.Stat_t{}, err
	}
	named, err := workerFileStat(root, name, directory, limit)
	if err != nil {
		return nil, named, err
	}
	if expected != nil && (uint64(named.Dev) != expected.Device || named.Ino != expected.Inode || named.Size != expected.Bytes) {
		return nil, named, ErrCustody
	}
	file, err := openRelative(root, name, false)
	if err != nil {
		return nil, named, err
	}
	var before, after unix.Stat_t
	if unix.Fstat(int(file.Fd()), &before) != nil || !workerSameStat(named, before) {
		_ = file.Close()
		return nil, named, ErrCustody
	}
	raw, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	statErr := unix.Fstat(int(file.Fd()), &after)
	closeErr := file.Close()
	final, finalErr := workerFileStat(root, name, directory, limit)
	if err != nil || statErr != nil || closeErr != nil || finalErr != nil || len(raw) != int(before.Size) || !workerSameStat(before, after) || !workerSameStat(after, final) || expected != nil && publicationDigest(raw) != expected.Digest {
		return nil, named, ErrCustody
	}
	return raw, final, ctx.Err()
}

// The path and readonly seam are private to neutral filesystem tests. The only
// exported path enters with the opaque process token and fixed /controls.
func loadWorkerControls(ctx context.Context, path string, allowance typedsandbox.Allowance, phase, request, expectedSeal string, readonly func(*os.File) error) (out WorkerControls, err error) {
	if ctx == nil || allowance.Validate() != nil || !publicationHash(expectedSeal) {
		return out, ErrCustody
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	root, err := openDirectory(path, false)
	if err != nil {
		return out, err
	}
	defer func() {
		err = errors.Join(err, root.Close(), ctx.Err())
		if err != nil {
			out = WorkerControls{}
		}
	}()
	var before unix.Stat_t
	if unix.Fstat(int(root.Fd()), &before) != nil || before.Mode != unix.S_IFDIR|0555 || before.Nlink == 0 || readonly(root) != nil {
		return out, ErrCustody
	}
	sealRaw, sealStat, err := workerReadFile(ctx, root, ControlSealFile, before, MaxControlSealBytes, nil)
	if err != nil || publicationDigest(sealRaw) != expectedSeal {
		return out, ErrCustody
	}
	seal, err := decodeControlSeal(sealRaw)
	if err != nil || seal.Identity.Allowance != allowance || string(seal.Identity.Phase) != phase || seal.Identity.RequestDigest != request || seal.Directory.Device != uint64(before.Dev) || seal.Directory.Inode != before.Ino {
		return out, ErrCustody
	}
	if err = controlChildren(root, seal.Files); err != nil {
		return out, err
	}
	data := make(map[string][]byte, len(seal.Files))
	observations := map[string]unix.Stat_t{ControlSealFile: sealStat}
	for _, file := range seal.Files {
		raw, st, e := workerReadFile(ctx, root, file.Name, before, controlLimit(file.Name), &file.Control)
		if e != nil {
			return out, e
		}
		data[file.Name], observations[file.Name] = raw, st
	}
	// Authenticate scratch shape even though ValidateWorker separately checks its
	// actual geometry. Admission never treats this retained JSON as mount proof.
	if _, err = typedsandbox.DecodeScratchAuthority(data[typedsandbox.ScratchAuthorityFile]); err != nil {
		return out, ErrCustody
	}
	named, err := openDirectory(path, false)
	if err != nil {
		return out, err
	}
	defer func() {
		err = errors.Join(err, named.Close())
		if err != nil {
			out = WorkerControls{}
		}
	}()
	var after unix.Stat_t
	if unix.Fstat(int(named.Fd()), &after) != nil || !workerSameStat(before, after) || readonly(named) != nil || controlChildren(named, seal.Files) != nil {
		return out, ErrCustody
	}
	for name, st := range observations {
		limit := controlLimit(name)
		if name == ControlSealFile {
			limit = MaxControlSealBytes
		}
		final, e := workerFileStat(named, name, after, limit)
		if e != nil || !workerSameStat(st, final) {
			return out, ErrCustody
		}
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	return decodeWorkerControls(ctx, seal, data)
}

// decodeWorkerControls is reached only after the expected supervisor seal and
// every file's authenticated bytes and custody metadata have been checked.
// Constructing Authority from
// these bytes is safe precisely because they are authenticated controller
// snapshots; doing this to an ordinary request would be self-admission.
func decodeWorkerControls(ctx context.Context, seal controlSeal, data map[string][]byte) (WorkerControls, error) {
	var out WorkerControls
	out.Allowance = seal.Identity.Allowance
	profile, err := typedindex.DecodeProfile(ctx, data["profile.json"])
	if err != nil || profile.Digest() != seal.Identity.ProfileDigest {
		return out, ErrCustody
	}
	var request typedindex.Request
	if json.Unmarshal(data["parent.json"], &request) != nil || request.Action != typedindex.Plan {
		return out, ErrCustody
	}
	authority := typedindex.Authority{Enabled: true, Administrator: true, Source: request.Source, Profile: typedindex.Epoch{Number: request.ProfileEpoch, Digest: profile.Digest()}, UniverseDigest: request.UniverseDigest}
	parent, err := typedindex.Admit(ctx, authority, profile, data["parent.json"])
	if err != nil || parent.Digest() != seal.Identity.PlanningDigest {
		return out, ErrCustody
	}
	inventory, err := typedindex.DecodeInventory(ctx, data["inventory.json"], parent.Request().BundleDigest)
	if err != nil {
		return out, ErrCustody
	}
	for name, value := range map[string]any{"profile.json": profile.Definition(), "parent.json": parent.Request()} {
		canonical, e := json.Marshal(value)
		if e != nil || !bytes.Equal(canonical, data[name]) {
			return WorkerControls{}, ErrCustody
		}
	}
	out.Parent, out.Profile, out.Inventory, out.Phase = parent, profile, inventory, typedindex.Plan
	if seal.Identity.Phase == ControlsExecution {
		plan, e := typedindex.DecodePackagePlan(ctx, parent, data["plan.json"], seal.Identity.PlanDigest)
		if e != nil {
			return WorkerControls{}, ErrCustody
		}
		authority.ParentRequestDigest, authority.PlanDigest = parent.Digest(), plan.Digest()
		execution, e := typedindex.Admit(ctx, authority, profile, data["request.json"])
		if e != nil || execution.Digest() != seal.Identity.RequestDigest || !executionAuthority(parent, execution, plan.Digest()) {
			return WorkerControls{}, ErrCustody
		}
		canonical, e := json.Marshal(execution.Request())
		if e != nil || !bytes.Equal(canonical, data["request.json"]) {
			return WorkerControls{}, ErrCustody
		}
		out.Plan, out.Execution, out.Phase = plan, execution, typedindex.Execute
	}
	return out, ctx.Err()
}
