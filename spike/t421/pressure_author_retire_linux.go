//go:build linux

package t421

import (
	"bytes"
	"context"
	"encoding/binary"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	linuxPressureRetireEntries             = 4_194_304
	linuxPressureRetireDirectories         = 1_048_576
	linuxPressureRetireChildren            = 65_536
	linuxPressureRetireDepth               = 64
	linuxPressureRetirePath                = 4096
	linuxPressureRetireNames               = 512 << 20
	linuxPressureRetirePathBytes           = 4 << 30
	linuxPressureRetireFileBytes           = 8 << 30
	linuxPressureRetireLogicalBytes        = 96 << 30
	linuxPressureRetireAttempts     uint64 = 17_481_860_096
	linuxPressureRetireChunk               = 1024
	linuxPressureRetireNameSlab            = 64 << 10
)

// Every row is nonpointer backing; only the fixed chunk tables contain pointers.
type linuxPressureAuthorRetirementRow struct {
	ino, size, blocks              uint64
	mtime, mtimeNS, ctime, ctimeNS int64
	parent, name, mode, children   uint32
	nameLen, depth, pathLen        uint16
}

type linuxPressureAuthorRetirementJournal struct {
	rows                                                                                           [linuxPressureRetireEntries / linuxPressureRetireChunk][]linuxPressureAuthorRetirementRow
	names                                                                                          [linuxPressureRetireNames / linuxPressureRetireNameSlab][]byte
	entries, directories, nameBytes, pathBytes, logicalBytes, allocatedBytes                       uint64
	rowBacking, nameBacking, nativeAttempts, deletionReserve, deletionPathReserve, removed, synced uint64
	maxDepth                                                                                       uint16
	firstErr                                                                                       error
	firstCloseErr                                                                                  error
	firstCloseFD                                                                                   int
	started, closeFailed, inventoryComplete, complete                                              bool
	retainedFDs                                                                                    [8]int
	retainedFDCount                                                                                int
	openFDs, peakFDs                                                                               int
}

type linuxPressureAuthorRetirementEngine struct {
	permit            *linuxPressureAuthorRetirement
	root              int
	uid, gid          uint32
	device            uint64
	fsid              [2]int32
	mount, imageBytes uint64
	observation       *executionPreworkObservation
	journal           *linuxPressureAuthorRetirementJournal
	deleting          bool
}

// Only the creation-scoped issuer's consumed permit reaches this engine.
func retireLinuxPressureAuthorWorkspace(ctx context.Context, permit *linuxPressureAuthorRetirement) error {
	if permit == nil {
		return ErrLinuxPressure
	}
	if err := permit.begin(ctx); err != nil {
		return err
	}
	journal := &linuxPressureAuthorRetirementJournal{firstCloseFD: -1}
	permit.journal = journal
	engine := linuxPressureAuthorRetirementEngine{permit: permit, root: int(permit.root.Fd()), uid: permit.uid, gid: permit.gid,
		device: permit.device, fsid: permit.fsid, mount: permit.mount, imageBytes: permit.imageBytes,
		observation: permit.observation, journal: journal}
	return engine.run(ctx)
}

func (engine *linuxPressureAuthorRetirementEngine) fail() error {
	if engine.journal.firstErr == nil {
		engine.journal.firstErr = ErrLinuxPressure
	}
	return engine.journal.firstErr
}

// Keep eight native attempts available for closing the at-most-three live FDs.
func (engine *linuxPressureAuthorRetirementEngine) charge(ctx context.Context) error {
	journal := engine.journal
	if ctx == nil || ctx.Err() != nil || journal.nativeAttempts >= linuxPressureRetireAttempts-8 ||
		engine.deleting && journal.deletionReserve <= 8 {
		return engine.fail()
	}
	journal.nativeAttempts++
	if engine.deleting {
		journal.deletionReserve--
	}
	return nil
}

func (engine *linuxPressureAuthorRetirementEngine) closeFD(fd int) error {
	journal := engine.journal
	if journal.nativeAttempts >= linuxPressureRetireAttempts || engine.deleting && journal.deletionReserve == 0 {
		if journal.retainedFDCount < len(journal.retainedFDs) {
			journal.retainedFDs[journal.retainedFDCount] = fd
			journal.retainedFDCount++
		}
		return engine.fail()
	}
	journal.nativeAttempts++
	if engine.deleting {
		journal.deletionReserve--
	}
	if err := engine.observation.close(unix.Close(fd)); err != nil {
		if !journal.closeFailed {
			journal.firstCloseFD = fd
			journal.firstCloseErr = err
		}
		journal.closeFailed = true
		return engine.fail()
	}
	if journal.openFDs > 0 {
		journal.openFDs--
	}
	return nil
}

func (journal *linuxPressureAuthorRetirementJournal) row(index uint32) *linuxPressureAuthorRetirementRow {
	return &journal.rows[index/linuxPressureRetireChunk][index%linuxPressureRetireChunk]
}

func (engine *linuxPressureAuthorRetirementEngine) appendRow(row linuxPressureAuthorRetirementRow, name []byte) error {
	journal := engine.journal
	if journal.entries >= linuxPressureRetireEntries || len(name) > 255 || row.depth > linuxPressureRetireDepth ||
		row.pathLen > linuxPressureRetirePath || journal.nameBytes+uint64(len(name))+1 > linuxPressureRetireNames ||
		journal.pathBytes+uint64(row.pathLen) > linuxPressureRetirePathBytes {
		return engine.fail()
	}
	chunk := journal.entries / linuxPressureRetireChunk
	if journal.rows[chunk] == nil {
		backing := uint64(unsafe.Sizeof(row)) * linuxPressureRetireChunk
		if unsafe.Sizeof(row) > 128 || journal.rowBacking+backing > 512<<20 {
			return engine.fail()
		}
		journal.rows[chunk] = make([]linuxPressureAuthorRetirementRow, linuxPressureRetireChunk)
		journal.rowBacking += backing
	}
	row.name, row.nameLen = uint32(journal.nameBytes), uint16(len(name))
	for i := 0; i <= len(name); i++ {
		at := journal.nameBytes
		slab := at / linuxPressureRetireNameSlab
		if journal.names[slab] == nil {
			journal.names[slab] = make([]byte, linuxPressureRetireNameSlab)
			journal.nameBacking += linuxPressureRetireNameSlab
		}
		if i < len(name) {
			journal.names[slab][at%linuxPressureRetireNameSlab] = name[i]
		}
		journal.nameBytes++
	}
	*journal.row(uint32(journal.entries)) = row
	journal.entries++
	if row.depth > journal.maxDepth {
		journal.maxDepth = row.depth
	}
	return nil
}

func (journal *linuxPressureAuthorRetirementJournal) name(row *linuxPressureAuthorRetirementRow, buffer *[256]byte) []byte {
	for i := uint32(0); i < uint32(row.nameLen); i++ {
		at := row.name + i
		buffer[i] = journal.names[at/linuxPressureRetireNameSlab][at%linuxPressureRetireNameSlab]
	}
	return buffer[:row.nameLen]
}

func (engine *linuxPressureAuthorRetirementEngine) ancestry(index uint32, chain *[linuxPressureRetireDepth + 1]uint32) (int, error) {
	n := 0
	for index != 0 {
		if uint64(index) >= engine.journal.entries || n == linuxPressureRetireDepth {
			return 0, engine.fail()
		}
		chain[n] = index
		parent := engine.journal.row(index).parent
		if parent >= index {
			return 0, engine.fail()
		}
		index = parent
		n++
	}
	return n, nil
}

func (engine *linuxPressureAuthorRetirementEngine) relative(index uint32, buffer *[linuxPressureRetirePath]byte) (string, error) {
	var chain [linuxPressureRetireDepth + 1]uint32
	n, err := engine.ancestry(index, &chain)
	if err != nil {
		return "", err
	}
	used := 0
	var name [256]byte
	for i := n - 1; i >= 0; i-- {
		part := engine.journal.name(engine.journal.row(chain[i]), &name)
		if used != 0 {
			if used == len(buffer) {
				return "", engine.fail()
			}
			buffer[used] = '/'
			used++
		}
		if len(part) > len(buffer)-used {
			return "", engine.fail()
		}
		used += copy(buffer[used:], part)
	}
	return engine.path(buffer[:used])
}

func (engine *linuxPressureAuthorRetirementEngine) path(raw []byte) (string, error) {
	journal := engine.journal
	if uint64(len(raw)) > linuxPressureRetirePathBytes-journal.pathBytes ||
		engine.deleting && uint64(len(raw)) > journal.deletionPathReserve {
		return "", engine.fail()
	}
	journal.pathBytes += uint64(len(raw))
	if engine.deleting {
		journal.deletionPathReserve -= uint64(len(raw))
	}
	return string(raw), nil
}

func linuxPressureAuthorRetirementMode(relative string, mode uint32) bool {
	parts := strings.Split(relative, "/")
	if len(parts) == 0 || parts[0] != "preparation" && parts[0] != "author" && parts[0] != "author-plan.json" {
		return false
	}
	if parts[0] == "author-plan.json" {
		return len(parts) == 1 && mode == unix.S_IFREG|0o400
	}
	if len(parts) == 1 {
		return mode == unix.S_IFDIR|0o700
	}
	if mode == unix.S_IFDIR|0o700 {
		return true
	}
	if mode&unix.S_IFMT != unix.S_IFREG {
		return false
	}
	if parts[0] == "preparation" {
		for _, part := range parts[1:] {
			if part == "payload" {
				return mode == unix.S_IFREG|0o400 || mode == unix.S_IFREG|0o500
			}
		}
		// Only the protected reference-source checkout materializes Git 100755
		// blobs as 0755: preparation/<reference-workspace>/source/<blob>. No
		// other preparation path and no other role admits group/other bits.
		if len(parts) >= 4 && strings.HasPrefix(parts[1], "phebs-t422-reference-") && parts[2] == "source" && mode == unix.S_IFREG|0o755 {
			return true
		}
	}
	return mode == unix.S_IFREG|0o400 || mode == unix.S_IFREG|0o500 || mode == unix.S_IFREG|0o600 || mode == unix.S_IFREG|0o700
}

func (engine *linuxPressureAuthorRetirementEngine) statFD(ctx context.Context, fd int) (unix.Stat_t, error) {
	var stat unix.Stat_t
	var filesystem unix.Statfs_t
	var extended unix.Statx_t
	if engine.charge(ctx) != nil {
		return stat, engine.fail()
	}
	flags, flagsErr := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if engine.charge(ctx) != nil {
		return stat, engine.fail()
	}
	fdFlags, fdErr := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
	if flagsErr != nil || fdErr != nil || flags&unix.O_ACCMODE != unix.O_RDONLY || flags&unix.O_PATH != 0 || fdFlags&unix.FD_CLOEXEC == 0 ||
		engine.charge(ctx) != nil || unix.Fstat(fd, &stat) != nil || engine.charge(ctx) != nil || unix.Fstatfs(fd, &filesystem) != nil ||
		engine.charge(ctx) != nil || unix.Statx(fd, "", unix.AT_EMPTY_PATH|unix.AT_SYMLINK_NOFOLLOW, unix.STATX_MNT_ID, &extended) != nil ||
		uint64(stat.Dev) != engine.device || filesystem.Fsid.Val != engine.fsid || extended.Mask&unix.STATX_MNT_ID == 0 || extended.Mnt_id != engine.mount ||
		stat.Uid != engine.uid || stat.Gid != engine.gid || stat.Mode != unix.S_IFDIR|0o700 {
		return stat, engine.fail()
	}
	return stat, nil
}

func linuxPressureAuthorRetirementSame(row *linuxPressureAuthorRetirementRow, stat unix.Stat_t, mutableDirectory bool) bool {
	if row.ino != stat.Ino || row.mode != stat.Mode || stat.Size < 0 || stat.Blocks < 0 {
		return false
	}
	if mutableDirectory && row.mode&unix.S_IFMT == unix.S_IFDIR {
		return true // This remover changes parent directory timestamps and sizes.
	}
	return row.size == uint64(stat.Size) && row.blocks == uint64(stat.Blocks) && row.mtime == stat.Mtim.Sec && row.mtimeNS == stat.Mtim.Nsec &&
		row.ctime == stat.Ctim.Sec && row.ctimeNS == stat.Ctim.Nsec
}

func (engine *linuxPressureAuthorRetirementEngine) openDirectory(ctx context.Context, index uint32) (int, error) {
	var chain [linuxPressureRetireDepth + 1]uint32
	n, err := engine.ancestry(index, &chain)
	if err != nil {
		return -1, err
	}
	current := engine.root
	var name [256]byte
	for i := n; i >= 0; i-- {
		var part string
		var pathErr error
		row := engine.journal.row(0)
		if i < n {
			row = engine.journal.row(chain[i])
			part, pathErr = engine.path(engine.journal.name(row, &name))
		} else {
			part, pathErr = engine.path([]byte("."))
		}
		if pathErr != nil || engine.journal.openFDs >= 8 || engine.charge(ctx) != nil {
			if current != engine.root {
				_ = engine.closeFD(current)
			}
			return -1, engine.fail()
		}
		next, openErr := unix.Openat(current, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if openErr != nil {
			if current != engine.root {
				_ = engine.closeFD(current)
			}
			return -1, engine.fail()
		}
		engine.journal.openFDs++
		if engine.journal.openFDs > engine.journal.peakFDs {
			engine.journal.peakFDs = engine.journal.openFDs
		}
		stat, statErr := engine.statFD(ctx, next)
		closeErr := error(nil)
		if current != engine.root {
			closeErr = engine.closeFD(current)
		}
		if statErr != nil || closeErr != nil || !linuxPressureAuthorRetirementSame(row, stat, engine.deleting) {
			_ = engine.closeFD(next)
			return -1, engine.fail()
		}
		current = next
	}
	return current, nil
}

func (engine *linuxPressureAuthorRetirementEngine) childStat(ctx context.Context, fd int, name []byte) (unix.Stat_t, error) {
	var stat unix.Stat_t
	var extended unix.Statx_t
	path, pathErr := engine.path(name)
	if pathErr != nil || engine.charge(ctx) != nil || unix.Fstatat(fd, path, &stat, unix.AT_SYMLINK_NOFOLLOW) != nil || engine.charge(ctx) != nil ||
		unix.Statx(fd, path, unix.AT_SYMLINK_NOFOLLOW, unix.STATX_MNT_ID, &extended) != nil ||
		uint64(stat.Dev) != engine.device || stat.Uid != engine.uid || stat.Gid != engine.gid || extended.Mask&unix.STATX_MNT_ID == 0 || extended.Mnt_id != engine.mount ||
		stat.Size < 0 || stat.Blocks < 0 || stat.Nlink == 0 {
		return stat, engine.fail()
	}
	return stat, nil
}

func (engine *linuxPressureAuthorRetirementEngine) add(ctx context.Context, parent uint32, fd int, name []byte, inode uint64) error {
	journal := engine.journal
	row := journal.row(parent)
	if row.children == linuxPressureRetireChildren || len(name) == 0 || len(name) > 255 || bytes.IndexByte(name, '/') >= 0 {
		return engine.fail()
	}
	for _, value := range name {
		if value < 0x20 || value == 0x7f {
			return engine.fail()
		}
	}
	stat, err := engine.childStat(ctx, fd, name)
	if err != nil || stat.Ino != inode {
		return engine.fail()
	}
	length := uint64(row.pathLen) + uint64(len(name))
	if parent != 0 {
		length++
	}
	if length > linuxPressureRetirePath || row.depth == linuxPressureRetireDepth {
		return engine.fail()
	}
	next := linuxPressureAuthorRetirementRow{ino: stat.Ino, size: uint64(stat.Size), blocks: uint64(stat.Blocks), mode: stat.Mode, parent: parent,
		mtime: stat.Mtim.Sec, mtimeNS: stat.Mtim.Nsec, ctime: stat.Ctim.Sec, ctimeNS: stat.Ctim.Nsec, depth: row.depth + 1, pathLen: uint16(length)}
	if stat.Mode&unix.S_IFMT == unix.S_IFDIR {
		if journal.directories == linuxPressureRetireDirectories {
			return engine.fail()
		}
	} else if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1 || next.size > linuxPressureRetireFileBytes || journal.logicalBytes+next.size > linuxPressureRetireLogicalBytes {
		return engine.fail()
	}
	if journal.allocatedBytes > engine.imageBytes || next.blocks > ^uint64(0)/512 || next.blocks*512 > engine.imageBytes-journal.allocatedBytes {
		return engine.fail()
	}
	index := uint32(journal.entries)
	if engine.appendRow(next, name) != nil {
		return engine.fail()
	}
	var path [linuxPressureRetirePath]byte
	relative, pathErr := engine.relative(index, &path)
	if pathErr != nil || !linuxPressureAuthorRetirementMode(relative, stat.Mode) {
		return engine.fail()
	}
	// Six complete relative-path lengths cover all three ancestry walks,
	// child metadata bookends and the final basename-only unlink.
	journal.deletionPathReserve += 6 * (uint64(next.pathLen) + 1)
	row.children++
	journal.allocatedBytes += next.blocks * 512
	if stat.Mode&unix.S_IFMT == unix.S_IFDIR {
		journal.directories++
	} else {
		journal.logicalBytes += next.size
	}
	return nil
}

func (engine *linuxPressureAuthorRetirementEngine) inventoryDirectory(ctx context.Context, index uint32) (retErr error) {
	fd, err := engine.openDirectory(ctx, index)
	if err != nil {
		return err
	}
	defer func() {
		if engine.closeFD(fd) != nil {
			retErr = engine.fail()
		}
	}()
	var buffer [16 << 10]byte
	dots := uint8(0)
	for {
		if engine.charge(ctx) != nil {
			return engine.fail()
		}
		size, readErr := unix.Getdents(fd, buffer[:])
		if readErr != nil || size < 0 || size > len(buffer) {
			return engine.fail()
		}
		if size == 0 {
			break
		}
		for offset := 0; offset < size; {
			// A batch never processes more than 128 directory rows before cancellation.
			for batch := 0; batch < 128 && offset < size; batch++ {
				if size-offset < 20 {
					return engine.fail()
				}
				length := int(binary.NativeEndian.Uint16(buffer[offset+16:]))
				inode := binary.NativeEndian.Uint64(buffer[offset:])
				if length < 20 || length%8 != 0 || length > size-offset || inode == 0 {
					return engine.fail()
				}
				part := buffer[offset+19 : offset+length]
				end := bytes.IndexByte(part, 0)
				if end < 0 {
					return engine.fail()
				}
				part = part[:end]
				offset += length
				if bytes.Equal(part, []byte(".")) || bytes.Equal(part, []byte("..")) {
					bit := uint8(1)
					if len(part) == 2 {
						bit = 2
					}
					if dots&bit != 0 {
						return engine.fail()
					}
					dots |= bit
					continue
				}
				if engine.add(ctx, index, fd, part, inode) != nil {
					return engine.fail()
				}
			}
			if ctx.Err() != nil {
				return engine.fail()
			}
		}
	}
	stat, statErr := engine.statFD(ctx, fd)
	if dots != 3 || statErr != nil || !linuxPressureAuthorRetirementSame(engine.journal.row(index), stat, false) {
		return engine.fail()
	}
	return nil
}

func (engine *linuxPressureAuthorRetirementEngine) dispose(ctx context.Context, index uint32) (retErr error) {
	row := engine.journal.row(index)
	parent, err := engine.openDirectory(ctx, row.parent)
	if err != nil {
		return err
	}
	defer func() {
		if engine.closeFD(parent) != nil {
			retErr = engine.fail()
		}
	}()
	var name [256]byte
	part := engine.journal.name(row, &name)
	stat, statErr := engine.childStat(ctx, parent, part)
	if statErr != nil || row.mode&unix.S_IFMT == unix.S_IFREG && stat.Nlink != 1 || !linuxPressureAuthorRetirementSame(row, stat, true) {
		return engine.fail()
	}
	flags := 0
	if row.mode&unix.S_IFMT == unix.S_IFDIR {
		child, openErr := engine.openDirectory(ctx, index)
		if openErr != nil {
			return openErr
		}
		syncErr := engine.charge(ctx)
		if syncErr == nil {
			syncErr = unix.Fsync(child)
		}
		closeErr := engine.closeFD(child)
		if syncErr != nil || closeErr != nil {
			return engine.fail()
		}
		engine.journal.synced++
		flags = unix.AT_REMOVEDIR
	}
	if engine.linkedParent(ctx, row.parent, parent) != nil {
		return engine.fail()
	}
	// The exclusive-writer loan is required: this is a linked bookend, not
	// an atomic compare-and-unlink against a concurrent trusted-root mutator.
	stat, statErr = engine.childStat(ctx, parent, part)
	if statErr != nil || row.mode&unix.S_IFMT == unix.S_IFREG && stat.Nlink != 1 || !linuxPressureAuthorRetirementSame(row, stat, true) {
		return engine.fail()
	}
	path, pathErr := engine.path(part)
	if pathErr != nil || engine.charge(ctx) != nil || unix.Unlinkat(parent, path, flags) != nil {
		return engine.fail()
	}
	engine.journal.removed++
	return nil
}

func (engine *linuxPressureAuthorRetirementEngine) linkedParent(ctx context.Context, index uint32, pinned int) (retErr error) {
	linked, err := engine.openDirectory(ctx, index)
	if err != nil {
		return err
	}
	current, currentErr := engine.statFD(ctx, pinned)
	named, namedErr := engine.statFD(ctx, linked)
	closeErr := engine.closeFD(linked)
	if currentErr != nil || namedErr != nil || closeErr != nil || current.Dev != named.Dev || current.Ino != named.Ino {
		return engine.fail()
	}
	return engine.rootLink(ctx)
}

func (engine *linuxPressureAuthorRetirementEngine) rootLink(ctx context.Context) error {
	if engine.permit == nil {
		return nil // Mechanics core only; the production entry always supplies its consumed permit.
	}
	if engine.permit.checkRoot(ctx, engine.charge) != nil {
		return engine.fail()
	}
	return nil
}

func (engine *linuxPressureAuthorRetirementEngine) run(ctx context.Context) error {
	journal := engine.journal
	if journal == nil {
		return ErrLinuxPressure
	}
	if journal.started {
		return engine.fail()
	}
	journal.started = true
	if engine.inventory(ctx) != nil {
		return engine.fail()
	}
	for index := journal.entries; index > 1; index-- {
		if engine.dispose(ctx, uint32(index-1)) != nil {
			return engine.fail()
		}
	}
	rootFD, openErr := engine.openDirectory(ctx, 0)
	if openErr != nil {
		return engine.fail()
	}
	emptyErr := engine.empty(ctx, rootFD)
	syncErr := emptyErr
	if syncErr == nil {
		syncErr = engine.rootLink(ctx)
	}
	if syncErr == nil {
		syncErr = engine.charge(ctx)
	}
	if syncErr == nil {
		syncErr = unix.Fsync(rootFD)
	}
	closeErr := engine.closeFD(rootFD)
	if emptyErr != nil || syncErr != nil || closeErr != nil {
		return engine.fail()
	}
	journal.synced++
	journal.complete = true
	return nil
}

func (engine *linuxPressureAuthorRetirementEngine) inventory(ctx context.Context) error {
	journal := engine.journal
	if ctx == nil {
		return engine.fail()
	}
	deadline, bounded := ctx.Deadline()
	if journal.entries != 0 || !bounded || !time.Now().Before(deadline) || deadline.After(time.Now().Add(time.Hour)) || engine.imageBytes == 0 {
		return engine.fail()
	}
	stat, err := engine.statFD(ctx, engine.root)
	if err != nil || stat.Size < 0 || stat.Blocks < 0 || uint64(stat.Blocks) > engine.imageBytes/512 {
		return engine.fail()
	}
	root := linuxPressureAuthorRetirementRow{ino: stat.Ino, size: uint64(stat.Size), blocks: uint64(stat.Blocks), mode: stat.Mode,
		mtime: stat.Mtim.Sec, mtimeNS: stat.Mtim.Nsec, ctime: stat.Ctim.Sec, ctimeNS: stat.Ctim.Nsec}
	if engine.appendRow(root, nil) != nil {
		return engine.fail()
	}
	journal.directories, journal.allocatedBytes, journal.deletionPathReserve = 1, root.blocks*512, 8
	for index := uint32(0); uint64(index) < journal.entries; index++ {
		if journal.row(index).mode&unix.S_IFMT == unix.S_IFDIR && engine.inventoryDirectory(ctx, index) != nil {
			return engine.fail()
		}
	}
	journal.inventoryComplete = true
	// Inventory is below 16E(H+1)+16D+512. Disposal is below48E(H+1)+
	//16D+512: <=3 ancestry walks, <=7 calls/edge, the charged7-call
	// issuer root-link guard, repeated child metadata and sync/close slack.
	reserve := 48*journal.entries*(uint64(journal.maxDepth)+1) + 16*journal.directories + 512
	if reserve > linuxPressureRetireAttempts-8-journal.nativeAttempts || journal.deletionPathReserve > linuxPressureRetirePathBytes-journal.pathBytes {
		return engine.fail()
	}
	journal.deletionReserve, engine.deleting = reserve, true
	return nil
}

func (engine *linuxPressureAuthorRetirementEngine) empty(ctx context.Context, fd int) error {
	var buffer [16 << 10]byte
	dots := uint8(0)
	for {
		if engine.charge(ctx) != nil {
			return engine.fail()
		}
		size, err := unix.Getdents(fd, buffer[:])
		if err != nil || size < 0 || size > len(buffer) {
			return engine.fail()
		}
		if size == 0 {
			if dots != 3 {
				return engine.fail()
			}
			return nil
		}
		for offset := 0; offset < size; {
			if size-offset < 20 {
				return engine.fail()
			}
			length := int(binary.NativeEndian.Uint16(buffer[offset+16:]))
			if length < 20 || length%8 != 0 || length > size-offset {
				return engine.fail()
			}
			name := buffer[offset+19 : offset+length]
			end := bytes.IndexByte(name, 0)
			if end < 0 {
				return engine.fail()
			}
			name = name[:end]
			bit := uint8(0)
			if bytes.Equal(name, []byte(".")) {
				bit = 1
			} else if bytes.Equal(name, []byte("..")) {
				bit = 2
			}
			if bit == 0 || dots&bit != 0 {
				return engine.fail()
			}
			dots |= bit
			offset += length
		}
	}
}
