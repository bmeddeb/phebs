package t421

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func linuxPressureTestGeometry() LinuxPressureGeometry {
	return LinuxPressureGeometry{ImageBytes: linuxPressureMinimumImage, Device: 1799, FSID: [2]int32{17, 19}, MountID: 103,
		BlockBytes: 4096, Blocks: 15000, AvailableBytes: 50 << 20, Inodes: 16000, FreeInodes: 15998}
}

func linuxPressureTestRecord(phase string) linuxPressureRecord {
	record := linuxPressureRecord{Schema: "phebs-linux-pressure-owner-v1", Token: strings.Repeat("a", 32), Phase: phase,
		Parent: linuxPressureNode{8, 100}, Root: linuxPressureNode{8, 101}, ImageBytes: linuxPressureMinimumImage,
		FormatterSHA256: "sha256:" + strings.Repeat("a", 64), FormatterRole: linuxPressureRole, Loop: -1}
	if linuxPressurePhase(phase) >= linuxPressurePhase("image") {
		record.Image = linuxPressureNode{8, 102}
	}
	if linuxPressurePhase(phase) >= linuxPressurePhase("loop-selected") {
		record.Loop = 7
	}
	if linuxPressurePhase(phase) >= linuxPressurePhase("ready") {
		record.Geometry = linuxPressureTestGeometry()
	}
	return record
}

func TestLinuxPressureRequestBounds(t *testing.T) {
	base := LinuxPressureRequest{Parent: "/var/lib/phebs-pressure", ImageBytes: linuxPressureMinimumImage, FormatterSHA256: "sha256:" + strings.Repeat("a", 64)}
	for _, size := range []uint64{linuxPressureMinimumImage, linuxPressureMaximumImage} {
		request := base
		request.ImageBytes = size
		if !validLinuxPressureRequest(request) {
			t.Fatal("valid image mechanism bound refused", size)
		}
	}
	for _, test := range []struct {
		name string
		edit func(*LinuxPressureRequest)
	}{
		{"relative", func(r *LinuxPressureRequest) { r.Parent = "relative" }},
		{"root", func(r *LinuxPressureRequest) { r.Parent = "/" }},
		{"unclean", func(r *LinuxPressureRequest) { r.Parent += "/../other" }},
		{"mount_escape", func(r *LinuxPressureRequest) { r.Parent += " space" }},
		{"backslash", func(r *LinuxPressureRequest) { r.Parent += `\040` }},
		{"long_path", func(r *LinuxPressureRequest) { r.Parent = "/" + strings.Repeat("a", 801) }},
		{"small", func(r *LinuxPressureRequest) { r.ImageBytes-- }},
		{"large", func(r *LinuxPressureRequest) { r.ImageBytes = linuxPressureMaximumImage + 4096 }},
		{"unaligned", func(r *LinuxPressureRequest) { r.ImageBytes++ }},
		{"overflow", func(r *LinuxPressureRequest) { r.ImageBytes = math.MaxUint64 }},
		{"raw_digest", func(r *LinuxPressureRequest) { r.FormatterSHA256 = strings.Repeat("a", 64) }},
		{"upper_digest", func(r *LinuxPressureRequest) { r.FormatterSHA256 = "sha256:" + strings.Repeat("A", 64) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := base
			test.edit(&request)
			if validLinuxPressureRequest(request) {
				t.Fatal("malformed operator selection admitted")
			}
		})
	}
}

func TestLinuxPressureBackingProjection(t *testing.T) {
	for _, block := range []uint64{512, 4096, 1 << 20} {
		budget, err := linuxPressureBackingBudget(linuxPressureMaximumImage, block)
		if err != nil || budget < linuxPressureMaximumImage+2*linuxPressureRecordLimit || budget > linuxPressureMaximumImage+18*block+2*linuxPressureRecordLimit {
			t.Fatal("wrong complete-image plus bounded metadata projection", block, budget, err)
		}
	}
	for _, block := range []uint64{0, 511, 513, 1<<20 + 1, math.MaxUint64} {
		if _, err := linuxPressureBackingBudget(linuxPressureMinimumImage, block); err == nil {
			t.Fatal("invalid allocation unit admitted", block)
		}
	}
	for _, test := range []struct {
		name                      string
		total, available, reserve uint64
		want                      bool
	}{
		{"normal", 100, 30, 9, true},
		{"first_collect_percent", 100, 30, 10, false},
		{"ceil_not_floor", 101, 22, 1, false},
		{"oversize", 100, 30, 31, false},
		{"invalid_available", 100, 101, 0, false},
		{"zero", 0, 0, 0, false},
		{"overflow", math.MaxUint64, math.MaxUint64, 0, false},
		{"large_safe", math.MaxInt64, math.MaxInt64, linuxPressureMaximumImage, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := linuxPressureBackingProjection(test.total, test.available, test.reserve); got != test.want {
				t.Fatal("wrong fixed-image capacity projection", got)
			}
		})
	}
	if total, available, err := linuxPressureFilesystemBytes(200, 150, 4096); err != nil || total != 819200 || available != 614400 {
		t.Fatal("wrong native capacity arithmetic", total, available, err)
	}
	for _, test := range [][3]uint64{{0, 0, 4096}, {1, 2, 4096}, {math.MaxUint64, 0, 4096}, {1, 0, 0}, {1, 0, 4097}} {
		if _, _, err := linuxPressureFilesystemBytes(test[0], test[1], test[2]); err == nil {
			t.Fatal("invalid native capacity arithmetic admitted", test)
		}
	}
}

func TestLinuxPressureGeometryIdentity(t *testing.T) {
	base := linuxPressureTestGeometry()
	if !validLinuxPressureGeometry(base) || base.Blocks*base.BlockBytes == base.ImageBytes {
		t.Fatal("actual usable geometry must be allowed to differ from nominal image bytes")
	}
	dynamic := base
	dynamic.AvailableBytes, dynamic.FreeInodes = 0, 0
	if !validLinuxPressureGeometry(dynamic) || !sameLinuxPressureGeometry(base, dynamic) {
		t.Fatal("capacity samples were treated as fixed identity")
	}
	for _, test := range []struct {
		name string
		edit func(*LinuxPressureGeometry)
	}{
		{"foreign_device", func(g *LinuxPressureGeometry) { g.Device++ }},
		{"foreign_fsid", func(g *LinuxPressureGeometry) { g.FSID[0]++ }},
		{"foreign_mount", func(g *LinuxPressureGeometry) { g.MountID++ }},
		{"other_total", func(g *LinuxPressureGeometry) { g.Blocks-- }},
		{"other_inodes", func(g *LinuxPressureGeometry) { g.Inodes++ }},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := base
			test.edit(&changed)
			if sameLinuxPressureGeometry(base, changed) {
				t.Fatal("foreign filesystem identity admitted")
			}
		})
	}
	for _, edit := range []func(*LinuxPressureGeometry){
		func(g *LinuxPressureGeometry) { g.Device = 0 }, func(g *LinuxPressureGeometry) { g.FSID = [2]int32{} },
		func(g *LinuxPressureGeometry) { g.MountID = 0 }, func(g *LinuxPressureGeometry) { g.BlockBytes = 512 },
		func(g *LinuxPressureGeometry) { g.Blocks = math.MaxUint64 }, func(g *LinuxPressureGeometry) { g.AvailableBytes = g.ImageBytes + 1 },
		func(g *LinuxPressureGeometry) { g.FreeInodes = g.Inodes + 1 },
	} {
		changed := base
		edit(&changed)
		if validLinuxPressureGeometry(changed) {
			t.Fatal("invalid geometry admitted", changed)
		}
	}
}

func TestLinuxPressureRetainedRecordTransitions(t *testing.T) {
	for index, phase := range linuxPressurePhases {
		t.Run(phase, func(t *testing.T) {
			record := linuxPressureTestRecord(phase)
			raw, err := json.Marshal(record)
			decoded, decodeErr := decodeLinuxPressureRecord(raw)
			if err != nil || decodeErr != nil || decoded != record {
				t.Fatal("valid retained phase did not round-trip", err, decodeErr)
			}
			if linuxPressureNextRecord(record, record) {
				t.Fatal("same-phase retry admitted")
			}
			if index > 0 && !linuxPressureNextRecord(linuxPressureTestRecord(linuxPressurePhases[index-1]), record) {
				t.Fatal("adjacent owning transition refused")
			}
			if index > 1 && linuxPressureNextRecord(linuxPressureTestRecord(linuxPressurePhases[index-2]), record) {
				t.Fatal("skipped owning transition admitted")
			}
		})
	}
	base := linuxPressureTestRecord("ready")
	raw, _ := json.Marshal(base)
	for _, invalid := range [][]byte{nil, append(append([]byte{}, raw...), '\n'), append(append([]byte{}, raw...), raw...),
		[]byte(strings.Replace(string(raw), `"phase":"ready"`, `"phase":"ready","phase":"ready"`, 1)),
		[]byte(strings.Replace(string(raw), `"phase":"ready"`, `"phase":"ready","unknown":1`, 1)),
		[]byte(strings.Repeat("a", linuxPressureRecordLimit+1))} {
		if _, err := decodeLinuxPressureRecord(invalid); err == nil {
			t.Fatal("noncanonical or oversized retained record admitted")
		}
	}
	for _, edit := range []func(*linuxPressureRecord){
		func(r *linuxPressureRecord) { r.FormatterRole = "system-tool" }, func(r *linuxPressureRecord) { r.FormatterSHA256 = "" },
		func(r *linuxPressureRecord) { r.Token = strings.Repeat("A", 32) }, func(r *linuxPressureRecord) { r.Phase = "unknown" },
		func(r *linuxPressureRecord) { r.Parent.Device = 0 }, func(r *linuxPressureRecord) { r.Image = r.Root },
		func(r *linuxPressureRecord) { r.Root = r.Parent }, func(r *linuxPressureRecord) { r.Image.Device++ },
		func(r *linuxPressureRecord) { r.Loop = -1 }, func(r *linuxPressureRecord) { r.Loop = 1048576 },
		func(r *linuxPressureRecord) { r.Geometry.Device = r.Parent.Device }, func(r *linuxPressureRecord) { r.Geometry.ImageBytes += 4096 },
	} {
		changed := base
		edit(&changed)
		encoded, _ := json.Marshal(changed)
		if _, err := decodeLinuxPressureRecord(encoded); err == nil {
			t.Fatal("malformed or crossed retained identity admitted", changed)
		}
	}
	for _, edit := range []func(*linuxPressureRecord){
		func(r *linuxPressureRecord) { r.Token = strings.Repeat("b", 32) },
		func(r *linuxPressureRecord) { r.Image.Inode++ },
		func(r *linuxPressureRecord) { r.Loop++ },
		func(r *linuxPressureRecord) { r.Geometry.FSID[0]++ },
	} {
		next := linuxPressureTestRecord("retiring")
		edit(&next)
		if linuxPressureNextRecord(base, next) {
			t.Fatal("cross-owner cleanup transition admitted")
		}
	}
}

func TestLinuxPressureLoopBinding(t *testing.T) {
	record := linuxPressureTestRecord("attached")
	base := linuxPressureLoop{Number: 7, Flags: 16, Device: record.Image.Device, Inode: record.Image.Inode, Limit: record.ImageBytes}
	if !linuxPressureLoopMatches(record, base) {
		t.Fatal("exact loop-to-image binding refused")
	}
	for _, edit := range []func(*linuxPressureLoop){
		func(l *linuxPressureLoop) { l.Number++ }, func(l *linuxPressureLoop) { l.Device++ }, func(l *linuxPressureLoop) { l.Inode++ },
		func(l *linuxPressureLoop) { l.Offset = 4096 }, func(l *linuxPressureLoop) { l.Limit-- },
		func(l *linuxPressureLoop) { l.Flags |= 4 }, func(l *linuxPressureLoop) { l.Flags = 0 },
		func(l *linuxPressureLoop) { l.Encryption = 1 }, func(l *linuxPressureLoop) { l.KeyBytes = 1 },
	} {
		changed := base
		edit(&changed)
		if linuxPressureLoopMatches(record, changed) {
			t.Fatal("foreign or differently interpreted loop admitted", changed)
		}
	}
}

func TestLinuxPressureMountObservation(t *testing.T) {
	const path = "/var/lib/phebs-pressure/t42h2m-aaaa/mount"
	const unrelated = "50 1 8:1 / / rw,relatime - ext4 /dev/sda1 rw\n"
	const selected = "103 50 7:7 / " + path + " rw,nosuid,nodev,relatime - ext4 /dev/loop7 rw\n"
	if mount, err := linuxPressureMountObservation([]byte(unrelated+selected), path, 7, 50, true); err != nil || mount.ID != 103 || mount.ParentID != 50 {
		t.Fatal("exact private ext4 mount refused", mount, err)
	}
	if _, err := linuxPressureMountObservation([]byte(unrelated), path, 7, 50, false); err != nil {
		t.Fatal("exact revealed underlay observation refused", err)
	}
	for _, test := range []struct{ name, raw string }{
		{"foreign_loop", strings.Replace(selected, "7:7", "7:8", 1)},
		{"foreign_source", strings.Replace(selected, "/dev/loop7", "/dev/loop8", 1)},
		{"foreign_parent", strings.Replace(selected, "103 50", "103 51", 1)},
		{"noncanonical_id", strings.Replace(selected, "103 50", "0103 50", 1)},
		{"zero_id", strings.Replace(selected, "103 50", "0 50", 1)},
		{"subtree", strings.Replace(selected, "7:7 / ", "7:7 /other ", 1)},
		{"foreign_fs", strings.Replace(selected, "- ext4", "- xfs", 1)},
		{"shared", strings.Replace(selected, " - ext4", " shared:1 - ext4", 1)},
		{"suid", strings.Replace(selected, ",nosuid", "", 1)},
		{"device_nodes", strings.Replace(selected, ",nodev", "", 1)},
		{"readonly", strings.Replace(selected, "rw,nosuid", "ro,nosuid", 1)},
		{"noexec", strings.Replace(selected, "rw,nosuid", "rw,noexec,nosuid", 1)},
		{"discard", strings.TrimSuffix(selected, "rw\n") + "rw,discard\n"},
		{"duplicate", selected + selected},
		{"nested", selected + "104 103 0:3 / " + path + "/child rw - tmpfs tmpfs rw\n"},
		{"same_device_alias", selected + "104 50 7:7 / /other rw - ext4 /dev/loop7 rw\n"},
		{"truncated", strings.TrimSuffix(selected, "\n")},
		{"oversized", strings.Repeat("a", linuxPressureMountLimit) + "\n"},
		{"missing", unrelated},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := linuxPressureMountObservation([]byte(unrelated+test.raw), path, 7, 50, true); err == nil {
				t.Fatal("unsubstantiated mount observation admitted")
			}
		})
	}
	if _, err := linuxPressureMountObservation([]byte(unrelated+selected), path, 7, 50, false); err == nil {
		t.Fatal("still-mounted filesystem treated as removed underlay")
	}
}

func TestLinuxPressurePrivateBacking(t *testing.T) {
	const row = "50 1 8:1 / / rw,relatime - ext4 /dev/sda1 rw\n"
	if !linuxPressurePrivateBacking([]byte(row), 50, "8:1") ||
		!linuxPressurePrivateBacking([]byte(strings.Replace(row, "- ext4", "- xfs", 1)), 50, "8:1") {
		t.Fatal("actual preexisting private local backing refused")
	}
	for _, invalid := range []string{
		strings.Replace(row, " - ext4", " shared:1 - ext4", 1),
		strings.Replace(row, " - ext4", " master:1 - ext4", 1),
		strings.Replace(row, " - ext4", " unbindable - ext4", 1),
		strings.Replace(row, "8:1", "8:2", 1),
		strings.Replace(row, "50 1", "51 1", 1),
		strings.Replace(row, "rw,relatime", "ro,relatime", 1),
		strings.Replace(row, "- ext4", "- overlay", 1),
		row + row,
		strings.TrimSuffix(row, "\n"),
	} {
		if linuxPressurePrivateBacking([]byte(invalid), 50, "8:1") {
			t.Fatal("shared/foreign/unsubstantiated backing admitted")
		}
	}
}
