package t4013

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLinuxMemoryObserverModeledFilesystem(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*testing.T, *linuxMemoryModel)
	}{
		{"finite leaf", nil},
		{"literal max leaf", func(t *testing.T, model *linuxMemoryModel) { model.write(t, "cgroup/group/memory.max", "max\n") }},
		{"hierarchy root only", func(t *testing.T, model *linuxMemoryModel) { model.write(t, "proc/cgroup", "0::/\n") }},
		{"current increases and available decreases", func(_ *testing.T, model *linuxMemoryModel) {
			model.hook = func(key string, count int) error {
				if key == "read:cgroup/group/memory.current" && count == 2 {
					return os.WriteFile(filepath.Join(model.base, "cgroup/group/memory.current"), []byte("4096\n"), 0o600)
				}
				if key == "read:proc/meminfo" && count == 2 {
					return os.WriteFile(filepath.Join(model.base, "proc/meminfo"), []byte("MemTotal: 32 kB\nMemAvailable: 20 kB\n"), 0o600)
				}
				return nil
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := newLinuxMemoryModel(t)
			if test.change != nil {
				test.change(t, model)
			}
			observation, err := observeLinuxMemorySource(t.Context(), model)
			if err != nil || !observation.CgroupAncestryComplete || observation.PhysicalBytes != 32*1024 ||
				!observation.CgroupAncestors[0].HierarchyRoot || observation.CgroupAncestors[0].Unlimited {
				t.Fatalf("modeled observation = %+v, %v", observation, err)
			}
			if test.name == "current increases and available decreases" &&
				(observation.AvailableBytes != 20*1024 || observation.CgroupAncestors[1].CurrentBytes != 4096) {
				t.Fatal("two-pass conservative samples lost", observation)
			}
			if test.name == "hierarchy root only" && len(observation.CgroupAncestors) != 1 {
				t.Fatal("invented hierarchy-root memory counters", observation)
			}
			_, err = EvaluateLinuxMemoryAdmission(observation, LinuxMemoryContract{
				HostHeadroomBytes: 1024, RequiredScopes: []LinuxMemoryScope{{"controller", 1}},
				Reservations: []LinuxMemoryReservation{{"controller", 1, 4096}},
			})
			if err != nil {
				t.Fatal("observer output did not feed supplied-model calculation", err)
			}
			model.assertClosed(t)
		})
	}
}

func TestLinuxMemoryObserverModeledRefusals(t *testing.T) {
	tests := []struct {
		name   string
		change func(*testing.T, *linuxMemoryModel)
	}{
		{"namespace root hides ancestors despite slash", func(t *testing.T, m *linuxMemoryModel) { m.write(t, "cgroup/cgroup.type", "domain\n") }},
		{"root counter is not an absent ABI", func(t *testing.T, m *linuxMemoryModel) { m.write(t, "cgroup/memory.max", "max\n") }},
		{"root current is not absent", func(t *testing.T, m *linuxMemoryModel) { m.write(t, "cgroup/memory.current", "0\n") }},
		{"root core file missing", func(t *testing.T, m *linuxMemoryModel) { m.remove(t, "cgroup/cgroup.procs") }},
		{"memory controller unavailable", func(t *testing.T, m *linuxMemoryModel) { m.write(t, "cgroup/cgroup.controllers", "cpu io\n") }},
		{"missing child memory controller", func(t *testing.T, m *linuxMemoryModel) { m.remove(t, "cgroup/group/memory.max") }},
		{"threaded memory domain", func(t *testing.T, m *linuxMemoryModel) { m.write(t, "cgroup/group/cgroup.type", "threaded\n") }},
		{"transient current above limit refuses capacity", func(t *testing.T, m *linuxMemoryModel) { m.write(t, "cgroup/group/memory.current", "16385\n") }},
		{"current proc is another PID namespace", func(_ *testing.T, m *linuxMemoryModel) { m.currentPID++ }},
		{"thread-self is another task", func(_ *testing.T, m *linuxMemoryModel) { m.currentTID++ }},
		{"wrong filesystem", func(_ *testing.T, m *linuxMemoryModel) {
			m.objects["cgroup"] = linuxMemoryObject{Filesystem: linuxMemoryProcMagic}
		}},
		{"overlaid root mount", func(_ *testing.T, m *linuxMemoryModel) {
			object := m.objects["cgroup"]
			object.MountID++
			m.objects["cgroup"] = object
		}},
		{"memory file denied", func(_ *testing.T, m *linuxMemoryModel) {
			m.hook = func(key string, _ int) error {
				if key == "read:cgroup/group/memory.current" {
					return os.ErrPermission
				}
				return nil
			}
		}},
		{"counter mount overlay", func(_ *testing.T, m *linuxMemoryModel) {
			object := m.objects["cgroup/group"]
			object.MountID++
			m.fileObjects["cgroup/group/memory.current"] = object
		}},
		{"counter device overlay", func(_ *testing.T, m *linuxMemoryModel) {
			object := m.objects["cgroup/group"]
			object.DeviceMinor++
			m.fileObjects["cgroup/group/memory.current"] = object
		}},
		{"counter filesystem overlay", func(_ *testing.T, m *linuxMemoryModel) {
			object := m.objects["cgroup/group"]
			object.Filesystem = linuxMemoryProcMagic
			m.fileObjects["cgroup/group/memory.current"] = object
		}},
		{"absent marker permission is not ENOENT", func(_ *testing.T, m *linuxMemoryModel) {
			m.hook = func(key string, _ int) error {
				if key == "exists:cgroup/cgroup.type" {
					return os.ErrPermission
				}
				return nil
			}
		}},
		{"limit changes within a pass", func(_ *testing.T, m *linuxMemoryModel) {
			m.hook = func(key string, count int) error {
				if key == "read:cgroup/group/memory.max" && count == 2 {
					return os.WriteFile(filepath.Join(m.base, "cgroup/group/memory.max"), []byte("8192\n"), 0o600)
				}
				return nil
			}
		}},
		{"ancestor replaced between passes", func(_ *testing.T, m *linuxMemoryModel) {
			m.hook = func(key string, count int) error {
				if key == "read:proc/meminfo" && count == 2 {
					object := m.objects["cgroup/group"]
					object.Inode++
					m.objects["cgroup/group"] = object
				}
				return nil
			}
		}},
		{"namespace or lifetime changes", func(_ *testing.T, m *linuxMemoryModel) {
			m.hook = func(key string, count int) error {
				if key == "snapshot" && count == 2 {
					m.identity = digest([]byte("new namespace"))
				}
				return nil
			}
		}},
		{"membership changes", func(_ *testing.T, m *linuxMemoryModel) {
			m.hook = func(key string, count int) error {
				if key == "snapshot" && count == 2 {
					return os.WriteFile(filepath.Join(m.base, "proc/cgroup"), []byte("0::/\n"), 0o600)
				}
				return nil
			}
		}},
		{"mount view changes", func(_ *testing.T, m *linuxMemoryModel) {
			m.hook = func(key string, count int) error {
				if key == "snapshot" && count == 2 {
					return os.WriteFile(filepath.Join(m.base, "proc/mountinfo"), []byte("43 1 0:30 / /sys/fs/cgroup rw - cgroup2 none rw\n"), 0o600)
				}
				return nil
			}
		}},
		{"physical total changes", func(_ *testing.T, m *linuxMemoryModel) {
			m.hook = func(key string, count int) error {
				if key == "read:proc/meminfo" && count == 2 {
					return os.WriteFile(filepath.Join(m.base, "proc/meminfo"), []byte("MemTotal: 33 kB\nMemAvailable: 24 kB\n"), 0o600)
				}
				return nil
			}
		}},
		{"late descriptor close failure", func(_ *testing.T, m *linuxMemoryModel) { m.closeError = true }},
		{"source close failure", func(_ *testing.T, m *linuxMemoryModel) { m.sourceCloseError = true }},
		{"symlink ancestor", func(t *testing.T, m *linuxMemoryModel) {
			if err := os.Rename(filepath.Join(m.base, "cgroup/group"), filepath.Join(m.base, "actual")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(m.base, "actual"), filepath.Join(m.base, "cgroup/group")); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := newLinuxMemoryModel(t)
			test.change(t, model)
			got, err := observeLinuxMemorySource(t.Context(), model)
			if !errors.Is(err, ErrLinuxMemoryObservation) || !reflect.DeepEqual(got, LinuxMemoryObservation{}) {
				t.Fatalf("refusal = %+v, %v", got, err)
			}
			model.assertClosed(t)
		})
	}
}

func TestLinuxMemoryObserverCancellation(t *testing.T) {
	for _, duringRead := range []bool{false, true} {
		model := newLinuxMemoryModel(t)
		ctx, cancel := context.WithCancel(t.Context())
		if duringRead {
			model.hook = func(key string, _ int) error {
				if key == "read:cgroup/group/memory.current" {
					cancel()
				}
				return nil
			}
		} else {
			cancel()
		}
		got, err := observeLinuxMemorySource(ctx, model)
		cancel()
		if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(got, LinuxMemoryObservation{}) {
			t.Fatalf("canceled observation = %+v, %v", got, err)
		}
		model.assertClosed(t)
	}
}

func TestLinuxMemoryObserverMaximumAncestry(t *testing.T) {
	m := newLinuxMemoryModel(t)
	var parts []string
	for index := 1; index < maxLinuxMemoryAncestors; index++ {
		parts = append(parts, "g")
		name := "cgroup/" + strings.Join(parts, "/")
		if err := os.MkdirAll(filepath.Join(m.base, name), 0o700); err != nil {
			t.Fatal(err)
		}
		m.objects[name] = linuxMemoryObject{linuxMemoryCgroupMagic, 0, 30, uint64(100 + index), 42}
		for file, raw := range map[string]string{"cgroup.type": "domain\n", "memory.max": "16384\n", "memory.current": "2048\n"} {
			m.write(t, name+"/"+file, raw)
		}
	}
	m.write(t, "proc/cgroup", "0::/"+strings.Join(parts, "/")+"\n")
	observation, err := observeLinuxMemorySource(t.Context(), m)
	if err != nil || len(observation.CgroupAncestors) != maxLinuxMemoryAncestors || m.peakDirectories != maxLinuxMemoryAncestors+2 {
		t.Fatal("maximum ancestor walk", err, len(observation.CgroupAncestors), m.peakDirectories)
	}
	m.assertClosed(t)
	if _, err := parseLinuxMemoryMembership([]byte("0::/" + strings.Join(append(parts, "overflow"), "/") + "\n")); err == nil {
		t.Fatal("accepted a 65th ancestor")
	}
}

func TestLinuxMemoryObserverParsers(t *testing.T) {
	for _, raw := range []string{"", "1::/a\n", "0::/a\n1:memory:/a\n", "0::/a/../b\n", "0:://a\n", "0::/a\\012b\n", "0::/a\\b\n", "0::/a\nmore\n", "0::/" + strings.Repeat("x/", 64) + "x\n"} {
		if _, err := parseLinuxMemoryMembership([]byte(raw)); err == nil {
			t.Fatalf("accepted membership %q", raw)
		}
	}
	for _, raw := range []string{"MemTotal: 1 kB\n", "MemTotal: 1 kB\nMemTotal: 1 kB\nMemAvailable: 0 kB\n", "MemTotal: 1 B\nMemAvailable: 0 kB\n", "MemTotal: 9223372036854775807 kB\nMemAvailable: 0 kB\n", "MemTotal: 1 kB\nMemAvailable: 2 kB\n"} {
		if _, _, err := parseLinuxMemoryInfo([]byte(raw)); err == nil {
			t.Fatalf("accepted meminfo %q", raw)
		}
	}
	for _, raw := range []string{"max\n", "+1\n", "-1\n", "01\n", "1", "1\v\n", "9223372036854775808\n", strings.Repeat("1", linuxMemoryValueBytes) + "\n"} {
		if _, err := parseLinuxMemoryValue([]byte(raw)); err == nil {
			t.Fatalf("accepted numeric value %q", raw)
		}
	}
	valid := "42 1 0:30 / /sys/fs/cgroup rw shared:2 - cgroup2 none rw\n"
	for _, raw := range []string{"", strings.ReplaceAll(valid, " / /sys", " /.. /sys"), strings.ReplaceAll(valid, "cgroup2", "cgroup"), valid + valid, valid + "43 42 0:30 /file /sys/fs/cgroup/memory.max ro - cgroup2 none rw\n", strings.Repeat("x", linuxMemoryMountBytes+1)} {
		if _, err := parseLinuxMemoryMount([]byte(raw)); err == nil {
			t.Fatal("accepted unsupported mount view")
		}
	}
	if _, err := parseLinuxMemoryMount([]byte(valid)); err != nil {
		t.Fatal(err)
	}
}

// This adapter uses actual private temporary filesystem reads and handles, but
// its kernel metadata/process identities are explicitly modeled for Mac tests.
type linuxMemoryModel struct {
	base, identity                       string
	objects                              map[string]linuxMemoryObject
	fileObjects                          map[string]linuxMemoryObject
	calls                                map[string]int
	hook                                 func(string, int) error
	currentPID, currentTID               int
	openDirectories, peakDirectories     int
	closed, closeError, sourceCloseError bool
}

func newLinuxMemoryModel(t *testing.T) *linuxMemoryModel {
	t.Helper()
	m := &linuxMemoryModel{base: t.TempDir(), identity: digest([]byte("modeled current process and namespaces")),
		objects: map[string]linuxMemoryObject{"cgroup": {linuxMemoryCgroupMagic, 0, 30, 1, 42}, "cgroup/group": {linuxMemoryCgroupMagic, 0, 30, 2, 42}}, fileObjects: make(map[string]linuxMemoryObject), calls: make(map[string]int), currentPID: 100, currentTID: 101}
	for _, name := range []string{"proc", "cgroup/group"} {
		if err := os.MkdirAll(filepath.Join(m.base, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for name, raw := range map[string]string{"proc/meminfo": "MemTotal: 32 kB\nMemAvailable: 24 kB\nSwapFree: 9999999 kB\n", "proc/cgroup": "0::/group\n", "proc/mountinfo": "42 1 0:30 / /sys/fs/cgroup rw - cgroup2 none rw\n", "cgroup/cgroup.controllers": "cpu io memory pids\n", "cgroup/cgroup.procs": "", "cgroup/group/cgroup.type": "domain\n", "cgroup/group/memory.max": "16384\n", "cgroup/group/memory.current": "2048\n"} {
		m.write(t, name, raw)
	}
	return m
}

func (m *linuxMemoryModel) write(t *testing.T, name, raw string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(m.base, name), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
}
func (m *linuxMemoryModel) remove(t *testing.T, name string) {
	t.Helper()
	if err := os.Remove(filepath.Join(m.base, name)); err != nil {
		t.Fatal(err)
	}
}
func (m *linuxMemoryModel) event(key string) error {
	m.calls[key]++
	if m.hook != nil {
		return m.hook(key, m.calls[key])
	}
	return nil
}
func (m *linuxMemoryModel) read(ctx context.Context, name string, maximum int) ([]byte, error) {
	if err := m.event("read:" + name); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if object, overlaid := m.fileObjects[name]; overlaid && !linuxMemorySameMount(object, m.objects[filepath.ToSlash(filepath.Dir(name))]) {
		return nil, ErrLinuxMemoryObservation
	}
	file, err := os.Open(filepath.Join(m.base, name))
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, int64(maximum)+1))
	if err != nil || len(raw) > maximum {
		return nil, ErrLinuxMemoryObservation
	}
	return raw, nil
}
func (m *linuxMemoryModel) snapshot(ctx context.Context) (linuxMemorySnapshot, error) {
	if err := m.event("snapshot"); err != nil {
		return linuxMemorySnapshot{}, err
	}
	membership, err := m.read(ctx, "proc/cgroup", linuxMemoryMembershipBytes)
	if err != nil {
		return linuxMemorySnapshot{}, err
	}
	mountinfo, err := m.read(ctx, "proc/mountinfo", linuxMemoryMountBytes)
	if err != nil {
		return linuxMemorySnapshot{}, err
	}
	return linuxMemorySnapshot{identity: m.identity, membership: membership, mountinfo: mountinfo, pid: 100, tid: 101, currentPID: m.currentPID, currentTID: m.currentTID}, nil
}
func (m *linuxMemoryModel) meminfo(ctx context.Context) ([]byte, error) {
	return m.read(ctx, "proc/meminfo", linuxMemoryInfoBytes)
}
func (m *linuxMemoryModel) openRoot(ctx context.Context) (linuxMemoryDirectory, error) {
	return m.open(ctx, "cgroup")
}
func (m *linuxMemoryModel) open(ctx context.Context, name string) (linuxMemoryDirectory, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := os.Lstat(filepath.Join(m.base, name))
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrLinuxMemoryObservation
	}
	root, err := os.OpenRoot(filepath.Join(m.base, name))
	if err != nil {
		return nil, err
	}
	m.openDirectories++
	m.peakDirectories = max(m.peakDirectories, m.openDirectories)
	return &linuxMemoryModelDirectory{model: m, root: root, name: name, binding: m.objects[name]}, nil
}
func (m *linuxMemoryModel) close() error {
	m.closed = true
	if m.sourceCloseError {
		return os.ErrPermission
	}
	return nil
}
func (m *linuxMemoryModel) assertClosed(t *testing.T) {
	t.Helper()
	if !m.closed || m.openDirectories != 0 {
		t.Fatalf("unclosed model: source=%v directories=%d", m.closed, m.openDirectories)
	}
}

type linuxMemoryModelDirectory struct {
	model   *linuxMemoryModel
	root    *os.Root
	name    string
	binding linuxMemoryObject
}

func (d *linuxMemoryModelDirectory) object() (linuxMemoryObject, error) { return d.binding, nil }
func (d *linuxMemoryModelDirectory) openChild(ctx context.Context, name string) (linuxMemoryDirectory, error) {
	return d.model.open(ctx, d.name+"/"+name)
}
func (d *linuxMemoryModelDirectory) read(ctx context.Context, name string, maximum int) ([]byte, error) {
	return d.model.read(ctx, d.name+"/"+name, maximum)
}
func (d *linuxMemoryModelDirectory) exists(ctx context.Context, name string) (bool, error) {
	if err := d.model.event("exists:" + d.name + "/" + name); err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	_, err := d.root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}
func (d *linuxMemoryModelDirectory) close() error {
	d.model.openDirectories--
	err := d.root.Close()
	if d.model.closeError {
		return errors.Join(err, os.ErrPermission)
	}
	return err
}
