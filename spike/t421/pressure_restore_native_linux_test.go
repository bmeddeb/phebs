//go:build linux && phebs_t42_native && phebs_t42_pressure_restore_native

package t421

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/releasebundle"
	"golang.org/x/sys/unix"
)

// MED-498 (T42.H2) focused Linux pressure, backup and restore composition.
//
// Three processes from this one root-owned test image:
//   - root test: owns the H2m ext4 pressure image and borrows one fresh leaf
//     to an unprivileged UID; after the view child is joined it proves no
//     process of that UID remains, the leaf is empty, then removes the leaf
//     and performs the nonforced unmount and exact loop detach.
//   - view (root, private mount namespace): mounts a tmpfs over the selected
//     empty view directory, binds the borrowed leaf at view/work, and runs
//     the driver with dropped credentials. Its mounts die with it.
//   - driver (unprivileged): runs the verified release's phebs serve, backup
//     and restore against a data root on the leaf. It drives ballast to the
//     product's 80/90/75 watermarks and checks admission, latch, resume,
//     reads under refusal, backup, restore, restored successor and teardown.
//
// Under the 2026-11-08 rule this tagged test and the gates are the acceptance.

const (
	pressureRestoreRoleEnv      = "PHEBS_T42_PRESSURE_RESTORE_ROLE"
	pressureRestoreSelectionEnv = "PHEBS_T42_PRESSURE_RESTORE_SELECTION"
	pressureRestoreRequestEnv   = "PHEBS_T42_PRESSURE_RESTORE_REQUEST"
	pressureRestoreRoleView     = "view-v1"
	pressureRestoreRoleDriver   = "driver-v1"
	pressureRestoreSchema       = "t42-med498-pressure-restore-v1"
	pressureRestoreTimeout      = 45 * time.Minute
	pressureRestoreRoleTimeout  = 40 * time.Minute
	pressureRestoreLogLimit     = 8 << 20
	pressureRestoreViewTmpfs    = "size=512m,nr_inodes=65536,mode=0755"
	pressureRestoreMinimumImage = 2 << 30
)

// pressureRestoreRequest is trusted operator input read by the root test.
type pressureRestoreRequest struct {
	Pressure      LinuxPressureRequest `json:"pressure"`
	UID           uint32               `json:"uid"`
	GID           uint32               `json:"gid"`
	Bundle        string               `json:"bundle"`
	Surreal       string               `json:"surreal"`
	SurrealSHA256 string               `json:"surreal_sha256"`
	Git           string               `json:"git"`
	GitSHA256     string               `json:"git_sha256"`
	View          string               `json:"view"`
}

// pressureRestoreSelection is what the root test hands its own children.
type pressureRestoreSelection struct {
	Schema     string                 `json:"schema"`
	Request    pressureRestoreRequest `json:"request"`
	Version    string                 `json:"version"`
	LeafDevice uint64                 `json:"leaf_device"`
	LeafInode  uint64                 `json:"leaf_inode"`
	// LeafPath locates the leaf inside the view's copied mount namespace; a
	// descriptor from another namespace cannot be a bind source. The view
	// accepts it only if its device and inode match the inherited descriptor.
	LeafPath string `json:"leaf_path"`
}

func init() {
	switch os.Getenv(pressureRestoreRoleEnv) {
	case pressureRestoreRoleView:
		os.Exit(pressureRestoreRoleExit("view", runPressureRestoreView))
	case pressureRestoreRoleDriver:
		os.Exit(pressureRestoreRoleExit("driver", runPressureRestoreDriver))
	}
}

func pressureRestoreRoleExit(name string, run func(context.Context, pressureRestoreSelection) error) int {
	selection, err := decodePressureRestoreSelection(os.Getenv(pressureRestoreSelectionEnv))
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), pressureRestoreRoleTimeout)
		err = run(ctx, selection)
		cancel()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "MED498 %s refused: %v\n", name, err)
		return 1
	}
	return 0
}

func encodePressureRestoreSelection(value pressureRestoreSelection) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > maxExecutionSelectionBytes {
		return "", errPressureRestorePlan
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodePressureRestoreSelection(encoded string) (pressureRestoreSelection, error) {
	var value pressureRestoreSelection
	raw, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || len(raw) == 0 || len(raw) > maxExecutionSelectionBytes {
		return value, errPressureRestorePlan
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF || value.Schema != pressureRestoreSchema ||
		!validPressureRestoreRequest(value.Request) || value.Version == "" || value.LeafInode == 0 ||
		!filepath.IsAbs(value.LeafPath) || filepath.Clean(value.LeafPath) != value.LeafPath || filepath.Base(value.LeafPath) != linuxPressureBorrowLeafName ||
		!inputTreePathsOverlap(value.LeafPath, value.Request.Pressure.Parent) {
		return value, errPressureRestorePlan
	}
	return value, nil
}

func validPressureRestoreRequest(request pressureRestoreRequest) bool {
	for _, path := range []string{request.Bundle, request.Surreal, request.Git, request.View} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" || len(path) > 512 {
			return false
		}
	}
	return validLinuxPressureRequest(request.Pressure) && request.Pressure.ImageBytes >= pressureRestoreMinimumImage &&
		(linuxPressureBorrowPolicy{request.UID, request.GID}).valid() &&
		validExecutionSHA256(request.SurrealSHA256) && validExecutionSHA256(request.GitSHA256) &&
		!inputTreePathsOverlap(request.View, request.Pressure.Parent) && !inputTreePathsOverlap(request.View, request.Bundle)
}

// pressureRestoreProtected requires path and every ancestor to be root-owned,
// not writable by group or others, and free of symlinks, so the borrowing UID
// cannot replace a tool after the root test has verified it.
func pressureRestoreProtected(path string) error {
	for current := path; ; current = filepath.Dir(current) {
		var stat unix.Stat_t
		if err := unix.Lstat(current, &stat); err != nil {
			return fmt.Errorf("inspect %s: %w", current, err)
		}
		if stat.Mode&unix.S_IFMT == unix.S_IFLNK || stat.Uid != 0 || stat.Mode&0o022 != 0 {
			return fmt.Errorf("%s is not root-owned and protected", current)
		}
		if current == "/" {
			return nil
		}
	}
}

func pressureRestoreDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func verifyPressureRestoreTools(request pressureRestoreRequest) (*releasebundle.Manifest, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	for _, path := range []string{self, request.Bundle, request.Surreal, request.Git} {
		if err := pressureRestoreProtected(path); err != nil {
			return nil, err
		}
	}
	if err := filepath.WalkDir(request.Bundle, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		var stat unix.Stat_t
		if err := unix.Lstat(path, &stat); err != nil || stat.Uid != 0 || stat.Mode&0o022 != 0 || stat.Mode&unix.S_IFMT == unix.S_IFLNK {
			return fmt.Errorf("bundle member %s is not root-owned and protected", path)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	manifest, err := releasebundle.Verify(request.Bundle)
	if err != nil {
		return nil, fmt.Errorf("verify release bundle: %w", err)
	}
	if manifest.GOOS != "linux" || manifest.GOARCH != "amd64" {
		return nil, errors.New("release bundle is not linux/amd64")
	}
	for _, tool := range []struct{ path, want string }{{request.Surreal, request.SurrealSHA256}, {request.Git, request.GitSHA256}} {
		got, err := pressureRestoreDigest(tool.path)
		if err != nil || got != tool.want {
			return nil, fmt.Errorf("tool %s digest %s does not match request", tool.path, got)
		}
	}
	return manifest, nil
}

// pressureRestoreUIDProcesses counts live processes whose real or effective
// UID is uid, excluding the caller's own PID.
func pressureRestoreUIDProcesses(uid uint32) (int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, err
	}
	count := 0
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "status"))
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
			continue
		}
		if err != nil {
			return 0, err
		}
		real, effective, ok := pressureRestoreStatusUIDs(raw)
		if !ok {
			return 0, fmt.Errorf("unreadable status for pid %d", pid)
		}
		if real == uid || effective == uid {
			count++
		}
	}
	return count, nil
}

// pressureRestoreDrained waits up to five seconds for exited descendants to
// be reaped; it never signals anything.
func pressureRestoreDrained(uid uint32) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		count, err := pressureRestoreUIDProcesses(uid)
		if err != nil {
			return err
		}
		if count == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%d process(es) of uid %d remain", count, uid)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

type pressureRestoreLog struct {
	mu        sync.Mutex
	data      []byte
	truncated bool
}

func (log *pressureRestoreLog) Write(data []byte) (int, error) {
	log.mu.Lock()
	defer log.mu.Unlock()
	remaining := pressureRestoreLogLimit - len(log.data)
	if len(data) > remaining {
		log.data = append(log.data, data[:max(remaining, 0)]...)
		log.truncated = true
	} else {
		log.data = append(log.data, data...)
	}
	return len(data), nil
}

func (log *pressureRestoreLog) String() string {
	log.mu.Lock()
	defer log.mu.Unlock()
	if log.truncated {
		return string(log.data) + "\n[log truncated]"
	}
	return string(log.data)
}

func TestLinuxPressureRestoreRequiredNative(t *testing.T) {
	if _, bounded := t.Deadline(); bounded {
		t.Fatal("required native pressure custody needs -test.timeout=0")
	}
	if runtime.GOARCH != "amd64" || os.Getuid() != 0 || os.Geteuid() != 0 {
		t.Fatal("root Linux amd64 required; no skip")
	}
	raw, err := os.ReadFile(os.Getenv(pressureRestoreRequestEnv))
	if err != nil || len(raw) > maxExecutionSelectionBytes {
		t.Fatal("bounded pressure/restore request required")
	}
	var request pressureRestoreRequest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || decoder.Decode(new(any)) != io.EOF || !validPressureRestoreRequest(request) {
		t.Fatal("exact pressure/restore request required")
	}
	manifest, err := verifyPressureRestoreTools(request)
	if err != nil {
		t.Fatal("protected tools:", err)
	}
	if err := pressureRestoreDrained(request.UID); err != nil {
		t.Fatal("borrowing uid must start with no processes:", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), pressureRestoreTimeout)
	defer cancel()
	started := time.Now()
	owner, err := PrepareLinuxPressureVolume(ctx, request.Pressure)
	if owner != nil {
		t.Cleanup(func() {
			if token, phase, removed := owner.RetainedOwner(); !removed {
				t.Logf("pressure image retained: t42h2m-%s phase=%s under %s", token, phase, request.Pressure.Parent)
			}
			if closeErr := owner.Close(); closeErr != nil {
				t.Error("descriptor close retained unresolved custody", closeErr)
			}
		})
	}
	if err != nil {
		t.Fatal("H2m pressure image preparation refused:", err)
	}
	before, err := owner.Geometry(ctx)
	if err != nil || !validLinuxPressureGeometry(before) {
		t.Fatal("actual ext4 geometry", err)
	}
	t.Logf("pressure image: nominal=%d usable=%d block=%d available=%d inodes=%d", before.ImageBytes, before.Blocks*before.BlockBytes, before.BlockBytes, before.AvailableBytes, before.Inodes)
	log := &pressureRestoreLog{}
	var leafStat unix.Stat_t
	err = owner.WithRoot(ctx, func(root *os.File) error {
		leaf, err := createLinuxPressureBorrowLeaf(ctx, root, request.UID, request.GID, &executionPreworkObservation{})
		if err != nil {
			return fmt.Errorf("borrow leaf: %w", err)
		}
		leafStat = leaf.stat
		closed := false
		defer func() {
			if !closed {
				_ = leaf.Close()
			}
		}()
		if err := runPressureRestoreViewChild(ctx, request, manifest.Version, leaf, log); err != nil {
			return fmt.Errorf("view child: %w", err) // the populated leaf stays retained
		}
		if err := pressureRestoreDrained(request.UID); err != nil {
			return fmt.Errorf("owner drain: %w", err)
		}
		if err := leaf.checkLink(ctx, root); err != nil {
			return fmt.Errorf("leaf identity after join: %w", err)
		}
		names, err := pressureRestoreDirectoryNames(leaf.file)
		if err != nil || len(names) != 0 {
			return fmt.Errorf("leaf not empty after driver teardown: %v %v", names, err)
		}
		closed = true
		if err := leaf.Close(); err != nil {
			return err
		}
		var linked unix.Stat_t
		if err := unix.Fstatat(int(root.Fd()), linuxPressureBorrowLeafName, &linked, unix.AT_SYMLINK_NOFOLLOW); err != nil ||
			linked.Dev != leafStat.Dev || linked.Ino != leafStat.Ino {
			return errors.New("leaf link changed before removal")
		}
		if err := unix.Unlinkat(int(root.Fd()), linuxPressureBorrowLeafName, unix.AT_REMOVEDIR); err != nil {
			return err
		}
		return root.Sync()
	})
	childLog := log.String()
	t.Logf("child log:\n%s", childLog)
	if err != nil {
		t.Fatal("composition refused; image and leaf retained:", err)
	}
	after, err := owner.Geometry(ctx)
	if err != nil || after.Device != before.Device || after.FSID != before.FSID || after.MountID != before.MountID || after.Blocks != before.Blocks {
		t.Fatal("pressure filesystem identity changed across the composition", err)
	}
	if err := owner.Remove(ctx); err != nil {
		t.Fatal("nonforced unmount/exact detach refused; image retained:", err)
	}
	if _, phase, removed := owner.RetainedOwner(); !removed || phase != "image-removed" {
		t.Fatal("pressure image removal incomplete", phase)
	}
	line, err := pressureRestoreResultLine(childLog)
	if err != nil {
		t.Fatal("driver result line missing")
	}
	var driver map[string]any
	if json.Unmarshal([]byte(line), &driver) != nil {
		t.Fatal("driver result undecodable")
	}
	summary, _ := json.Marshal(map[string]any{
		"schema": pressureRestoreSchema, "bundle_version": manifest.Version, "bundle_commit": manifest.Commit,
		"image_bytes": before.ImageBytes, "usable_bytes": before.Blocks * before.BlockBytes, "block_bytes": before.BlockBytes,
		"available_before": before.AvailableBytes, "available_after": after.AvailableBytes,
		"leaf_device": leafStat.Dev, "leaf_inode": leafStat.Ino, "elapsed_seconds": time.Since(started).Seconds(),
		"driver": driver, "leaf_removed": true, "image_removed": true, "uid_drained": true,
	})
	t.Logf("MED498-RESULT %s", summary)
}

func pressureRestoreDirectoryNames(dir *os.File) ([]string, error) {
	fd, err := unix.Dup(int(dir.Fd()))
	if err != nil {
		return nil, err
	}
	duplicate := os.NewFile(uintptr(fd), "leaf-listing")
	defer func() { _ = duplicate.Close() }()
	if _, err := duplicate.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	return duplicate.Readdirnames(-1)
}

func runPressureRestoreViewChild(ctx context.Context, request pressureRestoreRequest, version string, leaf *linuxPressureBorrowLeaf, log io.Writer) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	leafPath, err := os.Readlink(fmt.Sprintf("/proc/self/fd/%d", leaf.file.Fd()))
	if err != nil {
		return err
	}
	encoded, err := encodePressureRestoreSelection(pressureRestoreSelection{Schema: pressureRestoreSchema, Request: request, Version: version,
		LeafDevice: leaf.stat.Dev, LeafInode: leaf.stat.Ino, LeafPath: leafPath})
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, self, "-test.run=^$")
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", pressureRestoreRoleEnv + "=" + pressureRestoreRoleView, pressureRestoreSelectionEnv + "=" + encoded}
	command.Dir = "/"
	command.ExtraFiles = []*os.File{leaf.file}
	command.SysProcAttr = &syscall.SysProcAttr{Unshareflags: syscall.CLONE_NEWNS, Setsid: true, Pdeathsig: syscall.SIGKILL}
	command.Stdout, command.Stderr = log, log
	command.WaitDelay = time.Minute
	return command.Run()
}

// runPressureRestoreView runs as root inside its own private mount namespace.
func runPressureRestoreView(ctx context.Context, selection pressureRestoreSelection) (retErr error) {
	if os.Getuid() != 0 || os.Geteuid() != 0 {
		return errors.New("view must run as root")
	}
	var own, initial unix.Stat_t
	if unix.Stat("/proc/self/ns/mnt", &own) != nil || unix.Stat("/proc/1/ns/mnt", &initial) != nil || own.Ino == initial.Ino {
		return errors.New("view is not in a private mount namespace")
	}
	if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
		return fmt.Errorf("make view namespace private: %w", err)
	}
	var leaf unix.Stat_t
	if err := unix.Fstat(3, &leaf); err != nil || leaf.Dev != selection.LeafDevice || leaf.Ino != selection.LeafInode ||
		leaf.Mode != unix.S_IFDIR|0o700 || leaf.Uid != selection.Request.UID || leaf.Gid != selection.Request.GID {
		return errors.New("inherited leaf descriptor is not the borrowed leaf")
	}
	view := selection.Request.View
	var viewStat unix.Stat_t
	if err := unix.Lstat(view, &viewStat); err != nil || viewStat.Mode&unix.S_IFMT != unix.S_IFDIR || viewStat.Uid != 0 || viewStat.Mode&0o022 != 0 {
		return errors.New("view directory must be a root-owned protected directory")
	}
	if entries, err := os.ReadDir(view); err != nil || len(entries) != 0 {
		return errors.New("view directory must be empty")
	}
	if err := unix.Mount("tmpfs", view, "tmpfs", unix.MS_NOSUID|unix.MS_NODEV, pressureRestoreViewTmpfs); err != nil {
		return fmt.Errorf("mount view tmpfs: %w", err)
	}
	work, home, tmp := filepath.Join(view, "work"), filepath.Join(view, "home"), filepath.Join(view, "tmp")
	workMounted := false
	defer func() {
		if workMounted {
			retErr = errors.Join(retErr, unix.Unmount(work, 0))
		}
		retErr = errors.Join(retErr, unix.Unmount(view, 0))
	}()
	if err := os.Mkdir(work, 0o755); err != nil {
		return err
	}
	for _, private := range []string{home, tmp} {
		if err := os.Mkdir(private, 0o700); err != nil || os.Chown(private, int(selection.Request.UID), int(selection.Request.GID)) != nil {
			return errors.New("view private directories")
		}
	}
	var located unix.Stat_t
	if err := unix.Lstat(selection.LeafPath, &located); err != nil || located.Dev != leaf.Dev || located.Ino != leaf.Ino {
		return errors.New("leaf path in the view namespace is not the borrowed leaf")
	}
	if err := unix.Mount(selection.LeafPath, work, "", unix.MS_BIND, ""); err != nil {
		return fmt.Errorf("bind borrowed leaf: %w", err)
	}
	workMounted = true
	var bound unix.Stat_t
	if err := unix.Stat(work, &bound); err != nil || bound.Dev != leaf.Dev || bound.Ino != leaf.Ino {
		return errors.New("bound view is not the borrowed leaf")
	}
	if err := unix.Close(3); err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	encoded, err := encodePressureRestoreSelection(selection)
	if err != nil {
		return err
	}
	driver := exec.CommandContext(ctx, self, "-test.run=^$")
	driver.Dir = work
	driver.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "HOME=" + home, "TMPDIR=" + tmp, "GIT_CONFIG_NOSYSTEM=1",
		pressureRestoreRoleEnv + "=" + pressureRestoreRoleDriver, pressureRestoreSelectionEnv + "=" + encoded}
	driver.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: selection.Request.UID, Gid: selection.Request.GID, Groups: []uint32{}},
		Setsid: true, Pdeathsig: syscall.SIGKILL}
	driver.Stdout, driver.Stderr = os.Stdout, os.Stderr
	driver.WaitDelay = time.Minute
	if err := driver.Run(); err != nil {
		return fmt.Errorf("driver: %w", err)
	}
	if err := pressureRestoreDrained(selection.Request.UID); err != nil {
		return fmt.Errorf("driver descendants: %w", err)
	}
	return nil
}
