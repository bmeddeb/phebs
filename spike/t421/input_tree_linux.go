//go:build linux

package t421

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

// LinuxInputTreeHelper is issued only by the exact-source offline rebuild and
// sealed-image owner. Source/SDK bootstrap observation remains trusted-host
// verification, not the protected-tree authority subsequently issued here.
// Keep the helper open until every borrowing construction has actually joined.
type LinuxInputTreeHelper struct {
	mu     sync.Mutex // One construction, with no pending constructor queue.
	tool   *LinuxExecutionToolCustody
	closed bool
}

func ProtectLinuxInputTreeHelper(ctx context.Context, request ReferenceToolRequest) (*LinuxInputTreeHelper, error) {
	return protectLinuxInputTreeHelperObserved(ctx, request, nil)
}

func protectLinuxInputTreeHelperObserved(ctx context.Context, request ReferenceToolRequest, observation *executionPreworkObservation) (*LinuxInputTreeHelper, error) {
	request.Role, request.PlanSchema = linuxInputTreeRole, linuxInputTreeRecipe
	tool, err := protectLinuxExecutionReferenceToolObserved(ctx, request, observation)
	if tool == nil {
		return nil, ErrLinuxInputTree
	}
	helper := &LinuxInputTreeHelper{tool: tool}
	if err != nil {
		return helper, ErrLinuxInputTree
	}
	return helper, nil
}

func (helper *LinuxInputTreeHelper) Close() error {
	if helper == nil || helper.tool == nil {
		return ErrLinuxInputTree
	}
	helper.mu.Lock()
	defer helper.mu.Unlock()
	helper.closed = true
	if helper.tool.Close() != nil {
		return ErrLinuxInputTree
	}
	return nil
}

// Source paths select actual bytes, never protected authority or tool admission.
// Sources must be disjoint owned trees with no links or set-ID objects. The
// trusted source owner excludes external writers through the complete copy.
type LinuxInputTreeSource struct{ Name, Path string }

type LinuxInputTreeIdentity struct {
	ManifestSHA256, HelperSHA256 string
	Entries, Files, Bytes        uint64
	PathBytes                    uint64
}

type inputTreeSourceRoot struct {
	selection   LinuxInputTreeSource
	file        *os.File
	tree        *os.Root
	info        os.FileInfo
	observation *executionPreworkObservation
}

type inputTreeSourceEntry struct {
	entry    inputTreeEntry
	root     *inputTreeSourceRoot
	relative string
	info     os.FileInfo
}

type inputTreeKernelIdentity struct {
	stat  unix.Stat_t
	fsid  [2]int32
	mount uint64
}

// LinuxExecutionInputTree retains an anonymous read-only foreign mount through
// its root descriptor after the fixed constructor helper has joined. There is
// no host Directory/path or supplied readiness boolean. It grants neither a
// command permission, provenance for selected source bytes nor a ceremony plan.
type LinuxExecutionInputTree struct {
	mu          sync.Mutex // Entire loan including trusted joined callback use.
	done        chan struct{}
	cancel      context.CancelFunc
	root        *os.File
	kernel      inputTreeKernelIdentity
	identity    LinuxInputTreeIdentity
	err         error
	closeErr    error // Native descriptor-close failure, never a cleanup waiver.
	observation *executionPreworkObservation
	closed      bool

	stagePath string
	stage     *os.File
	stageInfo os.FileInfo
	pid       int
	joined    bool
	empty     bool
}

// ProtectLinuxExecutionInputTree copies at most three selected source roots,
// 100,000 total entries, 2 GiB logical bytes, 256 MiB/file and 64 MiB names.
// One fixed helper owns a new bounded tmpfs only in its private user/mount
// namespace. Construction uses one serial transfer/hash and one protected-copy
// hash/census. All writers close before read-only publication. No host mount,
// capability or daemon privilege is provisioned. Unsupported kernel/LSM policy
// refuses; there is no bind/chmod/immutable-flag fallback.
//
// After private-stage creation an error returns non-nil retained custody. The
// owner must cancel/Close that exact handle; it may still own a tracked Wait
// until a kernel-blocked helper joins. No success/cleanup is inferred from a
// timeout. Native syscalls remain cooperative, not hard syscall deadlines.
func ProtectLinuxExecutionInputTree(ctx context.Context, parent string, sources []LinuxInputTreeSource, helper *LinuxInputTreeHelper) (*LinuxExecutionInputTree, error) {
	return protectLinuxExecutionInputTreeObserved(ctx, parent, sources, helper, nil)
}

func protectLinuxExecutionInputTreeObserved(ctx context.Context, parent string, sources []LinuxInputTreeSource, helper *LinuxInputTreeHelper, observation *executionPreworkObservation) (*LinuxExecutionInputTree, error) {
	if runtime.GOARCH != "amd64" || ctx == nil || ctx.Err() != nil || helper == nil || helper.tool == nil ||
		!executionGitAbsolutePath(parent) || strings.ContainsAny(parent, " \t\\") || len(sources) == 0 || len(sources) > 3 {
		return nil, ErrLinuxInputTree
	}
	canonical, err := filepath.EvalSymlinks(parent)
	parentInfo, statErr := os.Lstat(parent)
	if err != nil || canonical != parent || statErr != nil || !parentInfo.IsDir() || parentInfo.Mode().Perm() != 0o700 || !inputCustodyCopyOwned(parentInfo) {
		return nil, ErrLinuxInputTree
	}
	if !helper.mu.TryLock() {
		return nil, ErrLinuxInputTree
	}
	preparing := true
	defer func() {
		if preparing {
			helper.mu.Unlock()
		}
	}()
	if helper.closed {
		return nil, ErrLinuxInputTree
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	transferred := false
	defer func() {
		if !transferred {
			cancel()
		}
	}()
	roots, rows, err := gatherInputTreeSourcesObserved(ctx, parent, slices.Clone(sources), observation)
	if err != nil {
		return nil, ErrLinuxInputTree
	}
	stagePath, err := os.MkdirTemp(parent, "t42-input-tree-")
	if err != nil {
		_ = closeInputTreeSources(roots)
		return nil, ErrLinuxInputTree
	}
	tree := &LinuxExecutionInputTree{stagePath: stagePath, done: make(chan struct{}), cancel: cancel, observation: observation}
	transferred = true
	tree.stage, err = t4013.OpenHostImage(stagePath)
	if err == nil {
		tree.stageInfo, err = tree.stage.Stat()
	}
	if err != nil {
		_ = closeInputTreeSources(roots)
		tree.err = ErrLinuxInputTree
		close(tree.done)
		cancel()
		return tree, ErrLinuxInputTree
	}
	preparing = false // Worker retains this helper gate through the actual Wait.
	go tree.construct(ctx, roots, rows, helper)
	select {
	case <-tree.done:
		if tree.err != nil {
			return tree, ErrLinuxInputTree
		}
		return tree, nil
	case <-ctx.Done():
		// A completed construction closes done before releasing its context.
		// Prefer its terminal result when both select arms are already ready.
		select {
		case <-tree.done:
			if tree.err == nil {
				return tree, nil
			}
		default:
		}
		cancel()
		return tree, ErrLinuxInputTree
	}
}

func gatherInputTreeSources(ctx context.Context, parent string, selections []LinuxInputTreeSource) (_ []*inputTreeSourceRoot, _ []inputTreeSourceEntry, retErr error) {
	return gatherInputTreeSourcesObserved(ctx, parent, selections, nil)
}

func gatherInputTreeSourcesObserved(ctx context.Context, parent string, selections []LinuxInputTreeSource, observation *executionPreworkObservation) (_ []*inputTreeSourceRoot, _ []inputTreeSourceEntry, retErr error) {
	var roots []*inputTreeSourceRoot
	defer func() {
		if retErr != nil {
			_ = closeInputTreeSources(roots)
		}
	}()
	var rows []inputTreeSourceEntry
	var pathBytes, logicalBytes uint64
	names := map[string]bool{}
	for _, selection := range selections {
		if len(selection.Name) > 64 || !validSanitizedToken(strings.ReplaceAll(selection.Name, "-", ""), 64) || names[selection.Name] ||
			!executionGitAbsolutePath(selection.Path) || inputTreePathsOverlap(parent, selection.Path) {
			return nil, nil, ErrLinuxInputTree
		}
		for _, earlier := range roots {
			if inputTreePathsOverlap(earlier.selection.Path, selection.Path) {
				return nil, nil, ErrLinuxInputTree
			}
		}
		names[selection.Name] = true
		canonical, err := filepath.EvalSymlinks(selection.Path)
		if err != nil || canonical != selection.Path {
			return nil, nil, ErrLinuxInputTree
		}
		root := &inputTreeSourceRoot{selection: selection, observation: observation}
		roots = append(roots, root)
		root.file, err = t4013.OpenHostImage(selection.Path)
		if err == nil {
			root.info, err = root.file.Stat()
		}
		if err != nil || !root.info.IsDir() || !inputCustodyCopyOwned(root.info) {
			return nil, nil, ErrLinuxInputTree
		}
		root.tree, err = os.OpenRoot(selection.Path)
		if err != nil {
			return nil, nil, ErrLinuxInputTree
		}
		opened, err := root.tree.Stat(".")
		if err != nil || !inputCustodySame(root.info, opened) {
			return nil, nil, ErrLinuxInputTree
		}
		err = walkGoBuildTree(ctx, root.tree, ".", func(relative string, info os.FileInfo) error {
			if len(rows) >= maxGoBuildEntries {
				return ErrLinuxInputTree
			}
			entry := inputTreeEntry{name: selection.Name, mode: 0o700}
			if relative != "." {
				entry.name += "/" + relative
			}
			if !info.IsDir() {
				if info.Size() < 0 || info.Size() > maxInputCustodyFileBytes {
					return ErrLinuxInputTree
				}
				entry.mode, entry.size = 0o400, uint64(info.Size())
				if info.Mode().Perm()&0o111 != 0 {
					entry.mode = 0o500
				}
			}
			// Charge the combined three-root census before retaining its name.
			if len(entry.name) > maxInputCustodyPathBytes || uint64(len(entry.name)) > maxGoBuildPathBytes-pathBytes ||
				entry.size > maxGoBuildBytes-logicalBytes || entry.mode == 0o500 && entry.size == 0 {
				return ErrLinuxInputTree
			}
			pathBytes += uint64(len(entry.name))
			logicalBytes += entry.size
			rows = append(rows, inputTreeSourceEntry{entry: entry, root: root, relative: relative, info: info})
			return nil
		})
		if err != nil {
			return nil, nil, ErrLinuxInputTree
		}
	}
	slices.SortFunc(rows, func(a, b inputTreeSourceEntry) int { return strings.Compare(a.entry.name, b.entry.name) })
	var census inputTreeCensus
	for _, row := range rows {
		if census.add(row.entry) != nil {
			return nil, nil, ErrLinuxInputTree
		}
	}
	return roots, rows, nil
}

func inputTreePathsOverlap(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

func closeInputTreeSources(roots []*inputTreeSourceRoot) error {
	var failed bool
	for _, root := range roots {
		if root.tree != nil && root.observation.close(root.tree.Close()) != nil {
			failed = true
		}
		if root.file != nil && root.observation.close(root.file.Close()) != nil {
			failed = true
		}
	}
	if failed {
		return ErrLinuxInputTree
	}
	return nil
}

func (tree *LinuxExecutionInputTree) construct(ctx context.Context, roots []*inputTreeSourceRoot, rows []inputTreeSourceEntry, helper *LinuxInputTreeHelper) {
	defer completeInputTreeConstruction(tree.done, tree.cancel)
	defer helper.mu.Unlock()
	defer func() {
		if closeInputTreeSources(roots) != nil {
			tree.err = ErrLinuxInputTree
		}
	}()
	err := helper.tool.WithImage(ctx, linuxInputTreeRole, func(identity ExecutionToolIdentity, image *os.File) (retErr error) {
		pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, 0)
		if err != nil {
			return ErrLinuxInputTree
		}
		parent, child := os.NewFile(uintptr(pair[0]), "input-tree-controller"), os.NewFile(uintptr(pair[1]), "input-tree-helper")
		defer func() {
			if tree.observation.close(parent.Close()) != nil {
				retErr = ErrLinuxInputTree
			}
			if child != nil && tree.observation.close(child.Close()) != nil {
				retErr = ErrLinuxInputTree
			}
		}()
		if unix.SetsockoptInt(pair[0], unix.SOL_SOCKET, unix.SO_PASSCRED, 1) != nil {
			return ErrLinuxInputTree
		}
		command := exec.Command("/proc/self/fd/3", linuxInputTreeRecipe)
		command.ExtraFiles = []*os.File{image, child, tree.stage}
		command.Dir, command.Env = tree.stagePath, []string{"GOMAXPROCS=2"}
		command.SysProcAttr = &syscall.SysProcAttr{
			Cloneflags: unix.CLONE_NEWUSER | unix.CLONE_NEWNS, Setsid: true, Pdeathsig: syscall.SIGKILL,
			UidMappings:                []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Geteuid(), Size: 1}},
			GidMappings:                []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getegid(), Size: 1}},
			GidMappingsEnableSetgroups: false,
		}
		// Pdeathsig refers to the creating Linux thread. Keep that thread alive
		// through the sole Wait, including retained cleanup after caller timeout.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if command.Start() != nil {
			return ErrLinuxInputTree // Includes denied kernel/LSM namespace policy.
		}
		tree.pid = command.Process.Pid
		waited := make(chan error, 1)
		go func() { waited <- command.Wait() }()
		useErr := func() error {
			closeErr := tree.observation.close(child.Close())
			child = nil
			namespaces, namespaceErr := observeInputTreeNamespaces(tree.pid)
			if closeErr != nil || t4013.MatchLinuxProcessExecutableImage(ctx, tree.pid, image) != nil || namespaceErr != nil {
				return ErrLinuxInputTree
			}
			wire := &inputTreeSocket{fd: pair[0], peer: &unix.Ucred{Pid: int32(tree.pid), Uid: uint32(os.Geteuid()), Gid: uint32(os.Getegid())}}
			initial := make([]byte, 48)
			deadline, _ := ctx.Deadline()
			binary.BigEndian.PutUint64(initial, uint64(deadline.UnixNano()))
			binary.BigEndian.PutUint32(initial[8:], uint32(os.Geteuid()))
			binary.BigEndian.PutUint32(initial[12:], uint32(os.Getegid()))
			if _, err := rand.Read(initial[16:]); err != nil || wire.send(ctx, treeWireInit, initial, nil) != nil {
				return ErrLinuxInputTree
			}
			census, err := sendInputTreeSources(ctx, wire, roots, rows)
			if err != nil || wire.send(ctx, treeWireEnd, inputTreeManifest(census), nil) != nil {
				return ErrLinuxInputTree
			}
			ready, view, err := wire.receive(ctx, treeWireReady, true)
			if view != nil {
				tree.root = view // Retained ownership even when subsequent checks fail.
			}
			expected := append(bytes.Clone(initial[16:]), inputTreeManifest(census)...)
			readyNamespaces, namespaceErr := observeInputTreeNamespaces(tree.pid)
			if err != nil || !bytes.Equal(ready, expected) || tree.checkStage() != nil ||
				namespaceErr != nil || namespaces != readyNamespaces || observeInputTreeDroppedPrivileges(tree.pid) != nil ||
				t4013.MatchLinuxProcessExecutableImage(ctx, tree.pid, image) != nil {
				return ErrLinuxInputTree
			}
			tree.kernel, err = observeInputTreeRoot(tree.root)
			if err != nil || observeInputTreeHelperMount(tree.pid, tree.stagePath, tree.kernel.mount) != nil ||
				wire.send(ctx, treeWireAck, initial[16:], nil) != nil {
				return ErrLinuxInputTree
			}
			tree.identity = LinuxInputTreeIdentity{
				ManifestSHA256: "sha256:" + hex.EncodeToString(expected[len(expected)-32:]), HelperSHA256: identity.SHA256,
				Entries: census.inventory.entries, Files: census.inventory.files, Bytes: census.inventory.bytes, PathBytes: census.inventory.pathBytes,
			}
			return nil
		}()
		deadline, _ := ctx.Deadline()
		if useErr != nil || ctx.Err() != nil {
			_ = unix.Kill(-tree.pid, unix.SIGKILL)
			deadline = time.Now().Add(5 * time.Second)
		}
		waitErr, joined := waitExecutionProcessRoot(waited, deadline)
		if !joined {
			_ = unix.Kill(-tree.pid, unix.SIGKILL)
			// Tracked retained cleanup: never release the image loan, locked
			// launching thread or owned FDs while this actual Wait is unknown.
			waitErr = <-waited
			joined = true
			useErr = ErrLinuxInputTree
		}
		tree.joined = joined
		tree.empty = t4013.WaitPrivateProcessSession(tree.pid, time.Now().Add(5*time.Second)) == nil
		if useErr != nil || waitErr != nil || !tree.empty || ctx.Err() != nil {
			return ErrLinuxInputTree
		}
		return nil
	})
	if err != nil || tree.root == nil || !tree.joined || !tree.empty || tree.checkRoot() != nil {
		tree.err = ErrLinuxInputTree
	}
}

func sendInputTreeSources(ctx context.Context, wire *inputTreeSocket, roots []*inputTreeSourceRoot, rows []inputTreeSourceEntry) (inputTreeCensus, error) {
	var census inputTreeCensus
	buffer := make([]byte, treeWireChunk)
	for _, row := range rows {
		kind := byte(treeWireFile)
		if row.entry.mode == 0o700 {
			kind = treeWireDirectory
		}
		if census.add(row.entry) != nil || wire.send(ctx, kind, encodeInputTreeEntry(row.entry), nil) != nil {
			return inputTreeCensus{}, ErrLinuxInputTree
		}
		if kind == treeWireDirectory {
			continue
		}
		entry, err := sendInputTreeSourceFile(ctx, wire, row, buffer)
		if err != nil {
			return inputTreeCensus{}, ErrLinuxInputTree
		}
		census.rows[len(census.rows)-1] = entry
	}
	// Revalidate the complete selected source snapshot, including entries copied
	// earlier in this serial transfer. This is metadata only, not a second hash.
	for _, row := range rows {
		current, err := row.root.tree.Lstat(row.relative)
		if ctx.Err() != nil || err != nil || !inputCustodySame(row.info, current) {
			return inputTreeCensus{}, ErrLinuxInputTree
		}
	}
	for _, root := range roots {
		held, err := root.file.Stat()
		linked, pathErr := os.Lstat(root.selection.Path)
		if err != nil || pathErr != nil || !inputCustodySame(root.info, held) || !inputCustodySame(held, linked) {
			return inputTreeCensus{}, ErrLinuxInputTree
		}
	}
	return census, nil
}

func sendInputTreeSourceFile(ctx context.Context, wire *inputTreeSocket, row inputTreeSourceEntry, buffer []byte) (_ inputTreeEntry, retErr error) {
	file, err := row.root.tree.Open(row.relative)
	if err != nil {
		return inputTreeEntry{}, ErrLinuxInputTree
	}
	defer func() {
		if row.root.observation.close(file.Close()) != nil {
			retErr = ErrLinuxInputTree
		}
	}()
	held, err := file.Stat()
	linked, pathErr := row.root.tree.Lstat(row.relative)
	if err != nil || pathErr != nil || !inputCustodySame(row.info, held) || !inputCustodySame(held, linked) {
		return inputTreeEntry{}, ErrLinuxInputTree
	}
	digest := sha256.New()
	remaining := row.entry.size
	for remaining != 0 {
		length := min(uint64(len(buffer)), remaining)
		count, err := io.ReadFull(executionInputReader{ctx, file}, buffer[:length])
		if err != nil || uint64(count) != length || wire.send(ctx, treeWireData, buffer[:count], nil) != nil {
			return inputTreeEntry{}, ErrLinuxInputTree
		}
		_, _ = digest.Write(buffer[:count])
		remaining -= uint64(count)
	}
	var sentinel [1]byte
	_, tailErr := io.ReadFull(executionInputReader{ctx, file}, sentinel[:])
	after, statErr := file.Stat()
	current, pathErr := row.root.tree.Lstat(row.relative)
	if !errors.Is(tailErr, io.EOF) || statErr != nil || pathErr != nil || !inputCustodySame(held, after) || !inputCustodySame(after, current) {
		return inputTreeEntry{}, ErrLinuxInputTree
	}
	entry := row.entry
	copy(entry.digest[:], digest.Sum(nil))
	if wire.send(ctx, treeWireFileEnd, entry.digest[:], nil) != nil {
		return inputTreeEntry{}, ErrLinuxInputTree
	}
	return entry, nil
}
