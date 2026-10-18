package t4013

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"
)

const (
	linuxMemoryProcMagic       = 0x9fa0
	linuxMemoryCgroupMagic     = 0x63677270
	linuxMemoryInfoBytes       = 64 << 10
	linuxMemoryMembershipBytes = 8 << 10
	linuxMemoryMountBytes      = 256 << 10
	linuxMemoryMountRows       = 4096
	linuxMemoryValueBytes      = 128
	linuxMemoryPathBytes       = 4096
	linuxMemoryDeadline        = 5 * time.Second
	linuxMemoryMountPoint      = "/sys/fs/cgroup"
)

// ErrLinuxMemoryObservation contains no cgroup path or native diagnostic.
var ErrLinuxMemoryObservation = errors.New("linux memory observation unavailable or changed")

type linuxMemoryObject struct {
	Filesystem               int64
	DeviceMajor, DeviceMinor uint32
	Inode, MountID           uint64
}

func (object linuxMemoryObject) identity() string {
	return digest([]byte(fmt.Sprintf("linux-memory-object-v1:%d:%d:%d:%d:%d",
		object.Filesystem, object.DeviceMajor, object.DeviceMinor, object.Inode, object.MountID)))
}

func linuxMemorySameMount(left, right linuxMemoryObject) bool {
	return left.Filesystem == right.Filesystem && left.DeviceMajor == right.DeviceMajor &&
		left.DeviceMinor == right.DeviceMinor && left.MountID == right.MountID
}

type linuxMemorySnapshot struct {
	identity               string
	membership, mountinfo  []byte
	pid, tid               int
	currentPID, currentTID int
}

func (snapshot linuxMemorySnapshot) valid() bool {
	return digestIdentity(snapshot.identity) && snapshot.pid > 0 && snapshot.tid > 0 &&
		snapshot.pid == snapshot.currentPID && snapshot.tid == snapshot.currentTID
}

// Only the fixed-root Linux backend is reachable through the public observer.
// This private seam lets portable filesystem controls exercise every collection
// boundary; implementing it does not confer native authority on supplied facts.
type linuxMemorySource interface {
	snapshot(context.Context) (linuxMemorySnapshot, error)
	meminfo(context.Context) ([]byte, error)
	openRoot(context.Context) (linuxMemoryDirectory, error)
	close() error
}

type linuxMemoryDirectory interface {
	object() (linuxMemoryObject, error)
	openChild(context.Context, string) (linuxMemoryDirectory, error)
	read(context.Context, string, int) ([]byte, error)
	exists(context.Context, string) (bool, error)
	close() error
}

type linuxMemoryPass struct {
	physical, available int64
	objects             []linuxMemoryObject
	rows                []LinuxMemoryCgroupObservation
}

func observeLinuxMemorySource(ctx context.Context, source linuxMemorySource) (result LinuxMemoryObservation, retErr error) {
	callerContext := ctx
	if source == nil {
		return result, ErrLinuxMemoryObservation
	}
	defer func() {
		retErr = errors.Join(retErr, source.close())
		if retErr != nil {
			deadline := errors.Is(retErr, context.DeadlineExceeded)
			result = LinuxMemoryObservation{}
			retErr = ErrLinuxMemoryObservation
			if deadline {
				retErr = errors.Join(retErr, context.DeadlineExceeded)
			} else if callerContext != nil && callerContext.Err() != nil {
				retErr = errors.Join(retErr, callerContext.Err())
			}
		}
	}()
	if ctx == nil || ctx.Err() != nil {
		return result, ErrLinuxMemoryObservation
	}
	ctx, cancel := context.WithTimeout(ctx, linuxMemoryDeadline)
	defer cancel()
	before, err := source.snapshot(ctx)
	if err != nil || !before.valid() {
		return result, ErrLinuxMemoryObservation
	}
	parts, err := parseLinuxMemoryMembership(before.membership)
	if err != nil {
		return result, err
	}
	mount, err := parseLinuxMemoryMount(before.mountinfo)
	if err != nil {
		return result, err
	}
	first, held, err := collectLinuxMemoryPass(ctx, source, parts, mount, nil)
	defer func() {
		for _, directory := range held {
			retErr = errors.Join(retErr, directory.close())
		}
	}()
	if err != nil {
		return result, err
	}
	second, _, err := collectLinuxMemoryPass(ctx, source, parts, mount, first.objects)
	if err != nil {
		return result, err
	}
	after, err := source.snapshot(ctx)
	if err != nil || !after.valid() || before.identity != after.identity || before.pid != after.pid || before.tid != after.tid ||
		!bytes.Equal(before.membership, after.membership) ||
		!bytes.Equal(before.mountinfo, after.mountinfo) || first.physical != second.physical || ctx.Err() != nil {
		return result, ErrLinuxMemoryObservation
	}
	for index := range first.rows {
		left, right := &first.rows[index], second.rows[index]
		if left.Identity != right.Identity || left.ParentIdentity != right.ParentIdentity ||
			left.HierarchyRoot != right.HierarchyRoot || left.Unlimited != right.Unlimited || left.LimitBytes != right.LimitBytes {
			return result, ErrLinuxMemoryObservation
		}
		left.CurrentBytes = max(left.CurrentBytes, right.CurrentBytes)
	}
	result = LinuxMemoryObservation{PhysicalBytes: first.physical, AvailableBytes: min(first.available, second.available),
		CgroupAncestryComplete: true, CgroupAncestors: first.rows}
	if _, _, err := linuxMemoryCapacity(result); err != nil {
		return LinuxMemoryObservation{}, err
	}
	return result, nil
}

func collectLinuxMemoryPass(ctx context.Context, source linuxMemorySource, parts []string, mount linuxMemoryMount, previous []linuxMemoryObject) (pass linuxMemoryPass, held []linuxMemoryDirectory, retErr error) {
	raw, err := source.meminfo(ctx)
	if err != nil {
		return pass, held, err
	}
	pass.physical, pass.available, err = parseLinuxMemoryInfo(raw)
	if err != nil {
		return pass, held, err
	}
	current, err := source.openRoot(ctx)
	if err != nil {
		return pass, held, err
	}
	// The first pass pins every ancestor against inode reuse. The second pass
	// reopens the live paths, keeping only its current parent and child: at most
	// 64 first-pass descriptors plus two second-pass descriptors are held here.
	keep := previous == nil
	if keep {
		held = append(held, current)
	} else {
		defer func() { retErr = errors.Join(retErr, current.close()) }()
	}
	for index := 0; index <= len(parts); index++ {
		if ctx.Err() != nil {
			return pass, held, ctx.Err()
		}
		object, err := current.object()
		if err != nil || object.Filesystem != linuxMemoryCgroupMagic || object.Inode == 0 || object.MountID != mount.id ||
			object.DeviceMajor != mount.major || object.DeviceMinor != mount.minor ||
			previous != nil && object != previous[index] {
			return pass, held, ErrLinuxMemoryObservation
		}
		row, err := readLinuxMemoryRow(ctx, current, index == 0)
		if err != nil {
			return pass, held, err
		}
		row.Identity = object.identity()
		if index > 0 {
			row.ParentIdentity = pass.rows[index-1].Identity
		}
		pass.objects = append(pass.objects, object)
		pass.rows = append(pass.rows, row)
		confirmed, err := current.object()
		if err != nil || confirmed != object {
			return pass, held, ErrLinuxMemoryObservation
		}
		if index == len(parts) {
			break
		}
		child, err := current.openChild(ctx, parts[index])
		if err != nil {
			return pass, held, err
		}
		parent := current
		current = child
		if keep {
			held = append(held, child)
		} else if err := parent.close(); err != nil {
			return pass, held, err
		}
	}
	return pass, held, nil
}

func readLinuxMemoryRow(ctx context.Context, directory linuxMemoryDirectory, root bool) (LinuxMemoryCgroupObservation, error) {
	if root {
		// Linux v7.0 kernel/cgroup/cgroup.c:5094-5124 registers cgroup.type
		// with CFTYPE_NOT_ON_ROOT, but cgroup.procs/controllers on all nodes.
		// The held real filesystem and mount identity are checked separately.
		for _, name := range []string{"cgroup.type", "memory.max", "memory.current"} {
			present, err := directory.exists(ctx, name)
			if err != nil || present {
				return LinuxMemoryCgroupObservation{}, ErrLinuxMemoryObservation
			}
		}
		present, err := directory.exists(ctx, "cgroup.procs")
		if err != nil || !present {
			return LinuxMemoryCgroupObservation{}, ErrLinuxMemoryObservation
		}
		controllers, err := directory.read(ctx, "cgroup.controllers", linuxMemoryValueBytes)
		if err != nil || !slicesContainMemoryController(controllers) {
			return LinuxMemoryCgroupObservation{}, ErrLinuxMemoryObservation
		}
		return LinuxMemoryCgroupObservation{HierarchyRoot: true}, nil
	}
	typeRaw, err := directory.read(ctx, "cgroup.type", linuxMemoryValueBytes)
	if err != nil || string(typeRaw) != "domain\n" {
		return LinuxMemoryCgroupObservation{}, ErrLinuxMemoryObservation
	}
	limit, err := directory.read(ctx, "memory.max", linuxMemoryValueBytes)
	if err != nil {
		return LinuxMemoryCgroupObservation{}, err
	}
	current, err := directory.read(ctx, "memory.current", linuxMemoryValueBytes)
	if err != nil {
		return LinuxMemoryCgroupObservation{}, err
	}
	confirmed, err := directory.read(ctx, "memory.max", linuxMemoryValueBytes)
	if err != nil || !bytes.Equal(limit, confirmed) {
		return LinuxMemoryCgroupObservation{}, ErrLinuxMemoryObservation
	}
	row := LinuxMemoryCgroupObservation{Unlimited: string(limit) == "max\n"}
	if !row.Unlimited {
		row.LimitBytes, err = parseLinuxMemoryValue(limit)
		if err != nil || row.LimitBytes == 0 {
			return LinuxMemoryCgroupObservation{}, ErrLinuxMemoryObservation
		}
	}
	row.CurrentBytes, err = parseLinuxMemoryValue(current)
	return row, err
}

func slicesContainMemoryController(raw []byte) bool {
	if len(raw) == 0 || len(raw) > linuxMemoryValueBytes || bytes.ContainsAny(raw, "\x00\r") {
		return false
	}
	for _, controller := range strings.Fields(string(raw)) {
		if controller == "memory" {
			return true
		}
	}
	return false
}

func parseLinuxMemoryValue(raw []byte) (int64, error) {
	if len(raw) < 2 || len(raw) > linuxMemoryValueBytes || raw[len(raw)-1] != '\n' {
		return 0, ErrLinuxMemoryObservation
	}
	value := raw[:len(raw)-1]
	if len(value) > 1 && value[0] == '0' {
		return 0, ErrLinuxMemoryObservation
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return 0, ErrLinuxMemoryObservation
		}
	}
	parsed, err := strconv.ParseInt(string(value), 10, 64)
	return parsed, err
}

func parseLinuxMemoryInfo(raw []byte) (int64, int64, error) {
	if len(raw) == 0 || len(raw) > linuxMemoryInfoBytes || bytes.ContainsAny(raw, "\x00\r") {
		return 0, 0, ErrLinuxMemoryObservation
	}
	values := make(map[string]int64, 2)
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "MemTotal:" && fields[0] != "MemAvailable:" {
			continue
		}
		if _, duplicate := values[fields[0]]; duplicate || len(fields) != 3 || fields[2] != "kB" {
			return 0, 0, ErrLinuxMemoryObservation
		}
		value, err := parseLinuxMemoryValue([]byte(fields[1] + "\n"))
		if err != nil {
			return 0, 0, err
		}
		value, err = checkedMulInt64(value, 1024)
		if err != nil {
			return 0, 0, err
		}
		values[fields[0]] = value
	}
	physical, totalPresent := values["MemTotal:"]
	available, availablePresent := values["MemAvailable:"]
	if !totalPresent || !availablePresent || physical <= 0 || available > physical {
		return 0, 0, ErrLinuxMemoryObservation
	}
	return physical, available, nil
}

func parseLinuxMemoryMembership(raw []byte) ([]string, error) {
	if len(raw) < 5 || len(raw) > linuxMemoryMembershipBytes || !bytes.HasPrefix(raw, []byte("0::/")) || raw[len(raw)-1] != '\n' {
		return nil, ErrLinuxMemoryObservation
	}
	name := string(raw[3 : len(raw)-1])
	if len(name) > linuxMemoryPathBytes || path.Clean(name) != name || strings.ContainsAny(name, "\x00\r\n\t\\") {
		return nil, ErrLinuxMemoryObservation
	}
	if name == "/" {
		return nil, nil
	}
	parts := strings.Split(name[1:], "/")
	if len(parts) >= maxLinuxMemoryAncestors {
		return nil, ErrLinuxMemoryObservation
	}
	return parts, nil
}

type linuxMemoryMount struct {
	id           uint64
	major, minor uint32
}

func parseLinuxMemoryMount(raw []byte) (linuxMemoryMount, error) {
	if len(raw) == 0 || len(raw) > linuxMemoryMountBytes || bytes.ContainsAny(raw, "\x00\r") {
		return linuxMemoryMount{}, ErrLinuxMemoryObservation
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines) > linuxMemoryMountRows {
		return linuxMemoryMount{}, ErrLinuxMemoryObservation
	}
	var selected linuxMemoryMount
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 10 {
			return selected, ErrLinuxMemoryObservation
		}
		if strings.HasPrefix(fields[4], linuxMemoryMountPoint+"/") {
			return selected, ErrLinuxMemoryObservation // A nested mount could mask an ancestor/control file.
		}
		if fields[4] != linuxMemoryMountPoint {
			continue
		}
		separator := 6
		for separator < len(fields) && fields[separator] != "-" {
			separator++
		}
		if selected.id != 0 || fields[3] != "/" || separator+4 != len(fields) || fields[separator+1] != "cgroup2" {
			return selected, ErrLinuxMemoryObservation
		}
		device := strings.Split(fields[2], ":")
		if len(device) != 2 {
			return selected, ErrLinuxMemoryObservation
		}
		id, idErr := strconv.ParseUint(fields[0], 10, 64)
		major, majorErr := strconv.ParseUint(device[0], 10, 32)
		minor, minorErr := strconv.ParseUint(device[1], 10, 32)
		if idErr != nil || id == 0 || majorErr != nil || minorErr != nil {
			return selected, ErrLinuxMemoryObservation
		}
		selected = linuxMemoryMount{id, uint32(major), uint32(minor)}
	}
	if selected.id == 0 {
		return selected, ErrLinuxMemoryObservation
	}
	return selected, nil
}
