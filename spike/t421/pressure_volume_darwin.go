//go:build darwin

package t421

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

var errPressureVolume = errors.New("execution pressure volume unavailable or retained")

// This is rehearsal-volume custody, not full launcher admission. Populated
// release requires the bound successful rehearsal and closed input owners;
// full outer-stage accounting and durable hard-death supervision remain open.
type executionPressureVolume struct {
	mu                                                                                sync.Mutex
	parent                                                                            productionRoot
	root                                                                              productionRoot
	mount                                                                             productionRoot
	workspace                                                                         productionRoot
	image                                                                             *os.File
	imageInfo                                                                         os.FileInfo
	underlay                                                                          os.FileInfo
	tool                                                                              *ExecutionSystemToolCustody
	lock                                                                              io.Closer
	device                                                                            string
	sessions                                                                          []int // Registered only by this owner's successful native Start.
	ready                                                                             bool
	closed                                                                            bool
	removed                                                                           bool
	unsettled                                                                         bool
	borrowed                                                                          bool
	flow                                                                              *ExecutionEpochOne
	ballast                                                                           *executionPressureBallast
	bytes                                                                             *custodyByteObservation
	preparationBytes                                                                  custodyBytePhase // Actual preparation samples, never phase-one evidence.
	byteErr                                                                           error
	teardownRun                                                                       *ExecutionEpochOneRun // Non-nil consumes the one operational cleanup attempt, including failure.
	teardownInitial, teardownBefore, teardownPostDetach, teardownAfter, teardownClose SessionCensusEvidence // Actual samples; event ordinals unset.
	teardownDetached, teardownImageRemoved                                            bool
	teardownByteSamples                                                               uint32 // Only successful actual phase-15 parent walks; at most two call sites.
}

// prepareExecutionPressureVolume owns a fresh sparse image, never a supplied
// image or mount. A nonnil result on error retains all disk custody; Close only
// releases descriptors. Native command failure is never automatically retried.
func prepareExecutionPressureVolume(ctx context.Context, parent string) (_ *executionPressureVolume, retErr error) {
	if ctx == nil || ctx.Err() != nil || runtime.GOARCH != "arm64" || len(parent) > 800 ||
		!executionGitPrivateDirectory(parent) {
		return nil, errPressureVolume
	}
	v := &executionPressureVolume{}
	var err error
	v.parent, err = openProductionRoot(parent)
	if err != nil {
		return nil, errPressureVolume
	}
	defer func() {
		if retErr != nil {
			_ = v.Close()
			retErr = errPressureVolume
		}
	}()
	// Check the actual backing descriptor before creating even the operation
	// lock. A sparse image's available capacity may be capped by its backing
	// filesystem. This is the existing pre-run floor, not a repeated runtime
	// requirement after this run has allocated its own image contents.
	var backing unix.Statfs_t
	if unix.Fstatfs(int(v.parent.file.Fd()), &backing) != nil ||
		!pressureBackingCapacity(backing, v.parent.volume) || ctx.Err() != nil {
		return v, errPressureVolume
	}
	v.lock, err = t4013.LockRunRoot(parent)
	if err != nil {
		return v, errPressureVolume
	}
	v.tool, err = HoldExecutionSystemTool(ctx, "hdiutil")
	if err != nil {
		return v, errPressureVolume
	}
	directory, err := os.MkdirTemp(parent, "t422-pressure-")
	if err != nil {
		return v, errPressureVolume
	}
	// Preserve the newly created path even if opening its descriptor refuses.
	v.root.path = directory
	v.root, err = openProductionRoot(directory)
	if err != nil {
		v.root.path = directory
		return v, errPressureVolume
	}
	for _, name := range []string{"home", "tmp", "mount"} {
		if err := os.Mkdir(filepath.Join(directory, name), 0o700); err != nil {
			return v, errPressureVolume
		}
	}
	imagePath := filepath.Join(directory, "pressure.sparseimage")
	if _, err := v.command(ctx, "create", "-size", "96g", "-layout", "NONE", "-type", "SPARSE", "-fs", "APFS",
		"-volname", "phebs-t422-private", "-nospotlight", imagePath); err != nil {
		return v, errPressureVolume
	}
	v.image, err = t4013.OpenHostImage(imagePath)
	if err != nil {
		return v, errPressureVolume
	}
	v.imageInfo, err = v.image.Stat()
	if err != nil || !inputCustodyOwned(v.imageInfo) || !v.imageInfo.Mode().IsRegular() || v.image.Chmod(0o600) != nil {
		return v, errPressureVolume
	}
	v.imageInfo, err = v.image.Stat()
	if err != nil || !pressureImageOwned(v.imageInfo) {
		return v, errPressureVolume
	}
	mountPath := filepath.Join(directory, "mount")
	v.underlay, err = os.Lstat(mountPath)
	if err != nil || !inputCustodyOwned(v.underlay) || !v.underlay.IsDir() {
		return v, errPressureVolume
	}
	raw, err := v.command(ctx, "attach", "-owners", "on", "-nobrowse", "-noautoopen", "-mountpoint", mountPath, "-plist", imagePath)
	if err != nil {
		return v, errPressureVolume
	}
	device, mountedDevice, err := pressureAttachment(raw, mountPath)
	if err != nil {
		return v, errPressureVolume
	}
	v.device = device
	mountFile, err := t4013.OpenHostImage(mountPath)
	if err != nil {
		return v, errPressureVolume
	}
	v.mount = productionRoot{path: mountPath, file: mountFile}
	var stat unix.Statfs_t
	if unix.Fstatfs(int(mountFile.Fd()), &stat) != nil || !pressureFilesystem(stat, v.root.volume, mountedDevice, mountPath) {
		return v, errPressureVolume
	}
	v.mount.info, err = mountFile.Stat()
	v.mount.volume = stat.Fsid.Val
	if err != nil || !inputCustodyOwned(v.mount.info) || !v.mount.info.IsDir() {
		return v, errPressureVolume
	}
	workspacePath := filepath.Join(mountPath, "workspace")
	if err := os.Mkdir(workspacePath, 0o700); err != nil {
		return v, errPressureVolume
	}
	v.workspace, err = openProductionRoot(workspacePath)
	if err != nil || v.workspace.volume != v.mount.volume || v.check() != nil || ctx.Err() != nil {
		return v, errPressureVolume
	}
	v.ready = true
	return v, nil
}

func pressureBackingCapacity(stat unix.Statfs_t, volume [2]int32) bool {
	return volume != ([2]int32{}) && stat.Fsid.Val == volume && stat.Bsize != 0 && stat.Blocks != 0 &&
		stat.Bavail <= stat.Blocks && stat.Blocks <= math.MaxUint64/uint64(stat.Bsize) &&
		stat.Bavail*uint64(stat.Bsize) >= frozenSafetyEnvelope().MinimumAvailableDiskBytes
}

// command has a finite local recipe: create, attach, detach, with at most one
// native attempt each. It is not a V3 operational/preparation budget issuer.
// Attach's recorded session is retained until detach: native helpers may live
// as long as the image. Root Wait is joined before its bounded output is read.
func (v *executionPressureVolume) command(ctx context.Context, args ...string) ([]byte, error) {
	if ctx == nil || ctx.Err() != nil || len(args) == 0 || len(v.sessions) >= 3 {
		return nil, errPressureVolume
	}
	_, tool, err := v.tool.Check(ctx, "hdiutil")
	if err != nil {
		return nil, errPressureVolume
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	output := &checkoutCommandOutput{remaining: 64 << 10, cancel: cancel}
	command := exec.CommandContext(ctx, tool, args...)
	command.Dir = v.root.path
	command.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C", "HOME=" + filepath.Join(v.root.path, "home"),
		"TMPDIR=" + filepath.Join(v.root.path, "tmp")}
	command.Stdout, command.Stderr = output, output
	command.WaitDelay = 5 * time.Second
	prepareProductionSession(command)
	var wait func() error
	if v.teardownRun != nil {
		if len(args) != 2 || args[0] != "detach" || args[1] != v.device {
			return nil, errPressureVolume
		}
		handle, err := v.flow.parent.StartInPhase(ctx, 15, dispatchadmission.Site{ID: executionSiteDetach, Role: executionRoleHdiutil}, command)
		if err != nil {
			return nil, errPressureVolume
		}
		wait = handle.Wait
	} else {
		if err := command.Start(); err != nil {
			return nil, errPressureVolume
		}
		wait = command.Wait
	}
	v.sessions = append(v.sessions, command.Process.Pid)
	waitErr := wait() // One native Wait; operational cleanup includes its actual DA settlement.
	if waitErr != nil || output.err != nil || ctx.Err() != nil {
		deadline := time.Now().Add(5 * time.Second)
		if v.teardownRun != nil {
			if caller, ok := ctx.Deadline(); ok && caller.Before(deadline) {
				deadline = caller
			}
		}
		_, empty, _ := finishExecutionProcessSession(command.Process.Pid, nil, true, waitErr, deadline)
		v.unsettled = !empty
		return nil, errPressureVolume
	}
	if _, _, err := v.tool.Check(ctx, "hdiutil"); err != nil {
		return nil, errPressureVolume
	}
	return output.buffer.Bytes(), nil
}

func pressureImageOwned(info os.FileInfo) bool {
	return info != nil && inputCustodyOwned(info) && info.Mode().IsRegular() && info.Mode().Perm()&0o077 == 0 && info.Size() > 0
}

func pressureFilesystem(stat unix.Statfs_t, backing [2]int32, device, mount string) bool {
	return unix.ByteSliceToString(stat.Fstypename[:]) == "apfs" && stat.Flags&(unix.MNT_RDONLY|unix.MNT_IGNORE_OWNERSHIP) == 0 &&
		stat.Fsid.Val != backing && stat.Fsid.Val != ([2]int32{}) && stat.Bsize == 4096 &&
		stat.Blocks == (96<<30)/4096 && unix.ByteSliceToString(stat.Mntfromname[:]) == device &&
		unix.ByteSliceToString(stat.Mntonname[:]) == mount
}

func (v *executionPressureVolume) check() error {
	if v.closed || v.unsettled || v.image == nil || v.mount.file == nil || v.workspace.file == nil {
		return errPressureVolume
	}
	if pressureRootsUnchanged(v.parent, v.root, v.mount, v.workspace) != nil {
		return errPressureVolume
	}
	held, err := v.image.Stat()
	current, pathErr := os.Lstat(filepath.Join(v.root.path, "pressure.sparseimage"))
	if err != nil || pathErr != nil || !pressureImageOwned(held) || !pressureImageOwned(current) ||
		!os.SameFile(v.imageInfo, held) || !os.SameFile(held, current) {
		return errPressureVolume
	}
	var stat unix.Statfs_t
	if unix.Fstatfs(int(v.mount.file.Fd()), &stat) != nil || stat.Fsid.Val != v.mount.volume ||
		stat.Flags&(unix.MNT_RDONLY|unix.MNT_IGNORE_OWNERSHIP) != 0 || stat.Bsize != 4096 || stat.Blocks != (96<<30)/4096 {
		return errPressureVolume
	}
	return nil
}

func pressureRootsUnchanged(roots ...productionRoot) error {
	for _, root := range roots {
		if root.file == nil {
			return errPressureVolume
		}
		held, err := root.file.Stat()
		current, pathErr := os.Lstat(root.path)
		volume, volumeErr := inputCustodyVolume(root.file)
		canonical, canonicalErr := filepath.EvalSymlinks(root.path)
		if err != nil || pathErr != nil || volumeErr != nil || canonicalErr != nil || canonical != root.path ||
			!os.SameFile(root.info, held) || !os.SameFile(held, current) || !inputCustodyOwned(current) ||
			current.Mode().Perm() != root.info.Mode().Perm() || volume != root.volume {
			return errPressureVolume
		}
	}
	return nil
}

// removeEmpty cannot erase a populated workspace. No recursive deletion runs,
// even on failure. The inherited/full execution lease and producer joins are
// deliberately not synthesized from emptiness; this API is not its teardown.
func (v *executionPressureVolume) removeEmpty(ctx context.Context) error {
	if v == nil || ctx == nil || ctx.Err() != nil {
		return errPressureVolume
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.remove(ctx, true)
}

// remove is called under mu. Only successful bound rehearsal closure may
// select populated removal; both routes retain the same detach/image barrier.
func (v *executionPressureVolume) remove(ctx context.Context, emptyOnly bool) error {
	if v.removed {
		return nil
	}
	if !v.ready || v.borrowed || v.ballast != nil && !v.ballast.removed || v.check() != nil || emptyOnly && !pressureDirectoryEmpty(v.workspace.path) ||
		!pressureDirectoryEmpty(filepath.Join(v.root.path, "home")) || !pressureDirectoryEmpty(filepath.Join(v.root.path, "tmp")) {
		return errPressureVolume
	}
	// Refuse unexpected writable custody outside the owned workspace too.
	if !pressureMountEmpty(v.mount.path) {
		return errPressureVolume
	}
	// Populated callers have already closed their bound operational borrower.
	// Freshly inspect the nonshrinking preparation and operational scope;
	// earlier RootJoined/SessionEmpty results do not replace this census.
	if v.teardownRun != nil {
		var err error
		v.teardownBefore, err = v.censusTeardownSessions(ctx, true)
		if err != nil {
			return err
		}
	} else {
		for _, session := range v.recordedSessionsLocked() {
			if err := t4013.WaitPrivateProcessSession(session, time.Now().Add(5*time.Second)); err != nil {
				return errPressureVolume
			}
		}
	}
	v.ready = false // One teardown attempt; a busy/uncertain detach retains all disk custody.
	if errors.Join(v.workspace.file.Close(), v.mount.file.Close()) != nil {
		return errPressureVolume
	}
	v.workspace.file, v.mount.file = nil, nil
	// No -force and no mounted deletion. Native success is the writer barrier,
	// not proof of no escaped helper or source in another process's memory.
	if _, err := v.command(ctx, "detach", v.device); err != nil {
		return errPressureVolume
	}
	if v.teardownRun != nil {
		v.teardownDetached = true
	}
	if v.teardownRun != nil {
		var err error
		v.teardownPostDetach, err = v.censusTeardownSessions(ctx, true)
		if err != nil {
			return err
		}
	} else {
		for _, session := range v.recordedSessionsLocked() {
			if err := t4013.WaitPrivateProcessSession(session, time.Now().Add(5*time.Second)); err != nil {
				return errPressureVolume
			}
		}
	}
	// Detach must reveal the original backing filesystem, never another mount.
	var stat unix.Statfs_t
	if pressureRootsUnchanged(v.parent, v.root) != nil || unix.Statfs(v.root.path, &stat) != nil || stat.Fsid.Val != v.root.volume {
		return errPressureVolume
	}
	if mount, err := os.Lstat(v.mount.path); err == nil {
		if !os.SameFile(v.underlay, mount) || !mount.IsDir() || mount.Mode()&os.ModeSymlink != 0 || unix.Statfs(v.mount.path, &stat) != nil || stat.Fsid.Val != v.root.volume ||
			os.Remove(v.mount.path) != nil {
			return errPressureVolume
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return errPressureVolume
	}
	current, err := os.Lstat(filepath.Join(v.root.path, "pressure.sparseimage"))
	if err != nil || !os.SameFile(v.imageInfo, current) || !pressureImageOwned(current) || v.image.Close() != nil {
		return errPressureVolume
	}
	v.image = nil
	for _, name := range []string{"pressure.sparseimage", "home", "tmp"} {
		if v.teardownRun != nil && ctx.Err() != nil {
			return errPressureVolume
		}
		if err := os.Remove(filepath.Join(v.root.path, name)); err != nil {
			return errPressureVolume
		}
		if v.teardownRun != nil && name == "pressure.sparseimage" {
			v.teardownImageRemoved = true
		}
	}
	if v.teardownRun != nil && ctx.Err() != nil || v.root.file.Sync() != nil || os.Remove(v.root.path) != nil || v.parent.file.Sync() != nil {
		return errPressureVolume
	}
	if _, err := os.Lstat(v.root.path); !errors.Is(err, os.ErrNotExist) {
		return errPressureVolume
	}
	v.removed = true
	if v.teardownRun != nil {
		var err error
		v.teardownAfter, err = v.censusTeardownSessions(ctx, true)
		if err != nil {
			return err
		}
	}
	return nil
}

func pressureMountEmpty(path string) bool {
	entries, err := os.Open(path)
	if err != nil {
		return false
	}
	names, readErr := entries.Readdirnames(8)
	overflow, endErr := entries.Readdirnames(1)
	closeErr := entries.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) || len(names) > 5 || len(overflow) != 0 || !errors.Is(endErr, io.EOF) || closeErr != nil {
		return false
	}
	for _, name := range names {
		switch name {
		case "workspace", ".fseventsd", ".Trashes", ".Spotlight-V100", ".metadata_never_index":
		default:
			return false
		}
	}
	return true
}

func pressureDirectoryEmpty(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	names, readErr := file.Readdirnames(1)
	return file.Close() == nil && len(names) == 0 && errors.Is(readErr, io.EOF)
}

// recordedSessionsLocked snapshots only the closed launch recipe: three image
// commands, five servers, three authors and two archive commands. Callers hold
// volume.mu; brief flow→author locks copy actual Start IDs, never old emptiness
// booleans. Those locks are released before any native census. IDs survive
// joins, errors, input Close and epoch handoffs; zero unused operational slots
// are omitted, while even an invalid recorded preparation ID remains fail-closed.
func (v *executionPressureVolume) recordedSessionsLocked() []int {
	sessions := make([]int, 0, 13)
	sessions = append(sessions, v.sessions...)
	if v.flow == nil {
		return sessions
	}
	flow := v.flow
	flow.mu.Lock()
	defer flow.mu.Unlock()
	for _, session := range flow.serverSessions {
		if session != 0 {
			sessions = append(sessions, session)
		}
	}
	for _, session := range flow.archiveSessions {
		if session != 0 {
			sessions = append(sessions, session)
		}
	}
	if flow.epochs != nil && flow.epochs.author != nil {
		author := flow.epochs.author
		author.mu.Lock()
		for _, session := range author.sessions {
			if session != 0 {
				sessions = append(sessions, session)
			}
		}
		author.mu.Unlock()
	}
	return sessions
}

// Close never unmounts or deletes; failures retain the exact path for an
// explicit operator decision. It also releases the shared mutation lock.
func (v *executionPressureVolume) Close() error {
	if v == nil {
		return nil
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.unsettled || v.borrowed {
		return errPressureVolume
	}
	if v.closed {
		return nil
	}
	// Cover every failure boundary, including a successful command followed by
	// malformed output or mount refusal. Root Wait alone never releases custody.
	if v.teardownRun != nil {
		var censusErr error
		v.teardownClose, censusErr = v.censusTeardownSessions(v.teardownRun.teardownContext, false)
		if censusErr != nil {
			v.unsettled = true
			return censusErr
		}
	} else {
		for _, session := range v.recordedSessionsLocked() {
			members, err := t4013.PrivateProcessSessionMembers(session)
			if err != nil || members != 0 {
				v.unsettled = true
				return errPressureVolume
			}
		}
	}
	v.closed = true
	var err error
	for _, file := range []*os.File{v.workspace.file, v.mount.file, v.image, v.root.file, v.parent.file} {
		if file != nil {
			err = errors.Join(err, file.Close())
		}
	}
	err = errors.Join(err, v.tool.Close())
	if v.lock != nil {
		err = errors.Join(err, v.lock.Close())
	}
	return err
}

// Decode only the three entities emitted by the fixed partitionless APFS
// attach recipe. Unknown fields, nested values and noncanonical devices refuse.
func pressureAttachment(raw []byte, mount string) (string, string, error) {
	if len(raw) == 0 || len(raw) > 64<<10 {
		return "", "", errPressureVolume
	}
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	// hdiutil emits an XML declaration and a plist DOCTYPE. The standard
	// decoder does not fetch external entities.
	token, err := pressureXMLToken(decoder)
	for err == nil {
		switch token.(type) {
		case xml.ProcInst, xml.Directive:
			token, err = pressureXMLToken(decoder)
		default:
			goto document
		}
	}
document:
	start, ok := token.(xml.StartElement)
	if err != nil || !ok || start.Name != (xml.Name{Local: "plist"}) || len(start.Attr) != 1 ||
		start.Attr[0].Name != (xml.Name{Local: "version"}) || start.Attr[0].Value != "1.0" ||
		!pressureXMLMark(decoder, "dict", false) {
		return "", "", errPressureVolume
	}
	key, err := pressureXMLText(decoder, "key")
	if err != nil || key != "system-entities" || !pressureXMLMark(decoder, "array", false) {
		return "", "", errPressureVolume
	}
	var device, mounted, container string
	seen := make(map[string]bool, 3)
	for range 3 {
		if !pressureXMLMark(decoder, "dict", false) {
			return "", "", errPressureVolume
		}
		values := make(map[string]string, 6)
		for {
			token, err := pressureXMLToken(decoder)
			if err != nil {
				return "", "", errPressureVolume
			}
			if end, ok := token.(xml.EndElement); ok && end.Name == (xml.Name{Local: "dict"}) {
				break
			}
			start, ok := token.(xml.StartElement)
			if !ok || start.Name != (xml.Name{Local: "key"}) || len(start.Attr) != 0 || len(values) >= 6 {
				return "", "", errPressureVolume
			}
			name, err := pressureXMLValue(decoder, "key")
			if _, duplicate := values[name]; err != nil || duplicate {
				return "", "", errPressureVolume
			}
			var value string
			switch name {
			case "dev-entry", "content-hint", "mount-point", "volume-kind", "unmapped-content-hint":
				value, err = pressureXMLText(decoder, "string")
			case "potentially-mountable":
				token, tokenErr := pressureXMLToken(decoder)
				start, ok := token.(xml.StartElement)
				if tokenErr != nil || !ok || start.Name.Space != "" || len(start.Attr) != 0 ||
					(start.Name.Local != "true" && start.Name.Local != "false") ||
					!pressureXMLMark(decoder, start.Name.Local, true) {
					return "", "", errPressureVolume
				}
				value = start.Name.Local
			default:
				return "", "", errPressureVolume
			}
			if err != nil {
				return "", "", errPressureVolume
			}
			values[name] = value
		}
		dev := values["dev-entry"]
		if !pressureDevice(dev) || seen[dev] {
			return "", "", errPressureVolume
		}
		seen[dev] = true
		switch {
		case len(values) == 6 && values["mount-point"] == mount && values["volume-kind"] == "apfs" &&
			values["potentially-mountable"] == "true" && values["content-hint"] == "41504653-0000-11AA-AA11-00306543ECAC" &&
			values["unmapped-content-hint"] == values["content-hint"]:
			mounted = dev
		case len(values) == 4 && values["content-hint"] == "EF57347C-0000-11AA-AA11-00306543ECAC" &&
			values["unmapped-content-hint"] == values["content-hint"] && values["potentially-mountable"] == "false":
			container = dev
		case len(values) == 2 && values["potentially-mountable"] == "false":
			device = dev
		default:
			return "", "", errPressureVolume
		}
	}
	if !pressureXMLMark(decoder, "array", true) || !pressureXMLMark(decoder, "dict", true) ||
		!pressureXMLMark(decoder, "plist", true) {
		return "", "", errPressureVolume
	}
	if _, err := pressureXMLToken(decoder); err != io.EOF || device == "" || container == "" ||
		mounted != container+"s1" || strings.Contains(strings.TrimPrefix(device, "/dev/disk"), "s") ||
		strings.Contains(strings.TrimPrefix(container, "/dev/disk"), "s") {
		return "", "", errPressureVolume
	}
	return device, mounted, nil
}

func pressureDevice(device string) bool {
	if !strings.HasPrefix(device, "/dev/disk") || len(device) > 32 {
		return false
	}
	for _, number := range strings.Split(strings.TrimPrefix(device, "/dev/disk"), "s") {
		if number == "" || len(number) > 1 && number[0] == '0' {
			return false
		}
		for _, digit := range number {
			if digit < '0' || digit > '9' {
				return false
			}
		}
	}
	return strings.Count(device, "s") <= 2 // One in "disk", at most one partition suffix.
}

func pressureXMLToken(decoder *xml.Decoder) (xml.Token, error) {
	for {
		token, err := decoder.Token()
		if text, ok := token.(xml.CharData); err == nil && ok && strings.TrimSpace(string(text)) == "" {
			continue
		}
		return token, err
	}
}

func pressureXMLMark(decoder *xml.Decoder, name string, end bool) bool {
	token, err := pressureXMLToken(decoder)
	if err != nil {
		return false
	}
	if end {
		value, ok := token.(xml.EndElement)
		return ok && value.Name == (xml.Name{Local: name})
	}
	value, ok := token.(xml.StartElement)
	return ok && value.Name == (xml.Name{Local: name}) && len(value.Attr) == 0
}

func pressureXMLText(decoder *xml.Decoder, name string) (string, error) {
	if !pressureXMLMark(decoder, name, false) {
		return "", errPressureVolume
	}
	return pressureXMLValue(decoder, name)
}

func pressureXMLValue(decoder *xml.Decoder, name string) (string, error) {
	var value strings.Builder
	for {
		token, err := decoder.Token()
		if err != nil {
			return "", errPressureVolume
		}
		switch token := token.(type) {
		case xml.CharData:
			value.Write(token)
		case xml.EndElement:
			if token.Name == (xml.Name{Local: name}) {
				return value.String(), nil
			}
			return "", errPressureVolume
		default:
			return "", errPressureVolume
		}
	}
}
