package t421

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func externalTestThinImage(cpu, subtype, kind uint32) []byte {
	raw := make([]byte, externalMachOHeader)
	binary.LittleEndian.PutUint32(raw[:4], externalMachO64Magic)
	binary.LittleEndian.PutUint32(raw[4:8], cpu)
	binary.LittleEndian.PutUint32(raw[8:12], subtype)
	binary.LittleEndian.PutUint32(raw[12:16], kind)
	return raw
}

func externalTestFatImage(order binary.ByteOrder, wide bool, cpus ...uint32) []byte {
	stride := 20
	magic := uint32(0xcafebabe)
	if wide {
		stride, magic = 32, 0xcafebabf
	}
	tableEnd := 8 + len(cpus)*stride
	raw := make([]byte, tableEnd+len(cpus)*externalMachOHeader)
	order.PutUint32(raw[:4], magic)
	order.PutUint32(raw[4:8], uint32(len(cpus)))
	for index, cpu := range cpus {
		record := raw[8+index*stride : 8+(index+1)*stride]
		order.PutUint32(record[:4], cpu)
		offset := tableEnd + index*externalMachOHeader
		if wide {
			order.PutUint64(record[8:16], uint64(offset))
			order.PutUint64(record[16:24], externalMachOHeader)
		} else {
			order.PutUint32(record[8:12], uint32(offset))
			order.PutUint32(record[12:16], externalMachOHeader)
		}
		copy(raw[offset:], externalTestThinImage(cpu, 0, externalMachOExecute))
	}
	return raw
}

func TestValidateExternalToolImageReadsOnlyNativeHeaders(t *testing.T) {
	x86 := uint32(0x01000007)
	for _, test := range []struct {
		name string
		raw  []byte
	}{
		{"thin arm64", externalTestThinImage(externalArm64CPU, 0, externalMachOExecute)},
		{"thin arm64e metadata", externalTestThinImage(externalArm64CPU, 0x80000002, externalMachOExecute)},
		{"fat32 big", externalTestFatImage(binary.BigEndian, false, x86, externalArm64CPU)},
		{"fat32 little", externalTestFatImage(binary.LittleEndian, false, externalArm64CPU, x86)},
		{"fat64 big", externalTestFatImage(binary.BigEndian, true, x86, externalArm64CPU)},
		{"fat64 little", externalTestFatImage(binary.LittleEndian, true, externalArm64CPU, x86)},
		{"fat eight slices", externalTestFatImage(binary.BigEndian, false, 1, 2, 3, 4, 5, 6, 7, externalArm64CPU)},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "header-only-never-executed")
			if err := os.WriteFile(path, test.raw, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := validateExternalToolImage(path); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestValidateExternalToolImageRejectsMalformedHeaders(t *testing.T) {
	thin := func(mutate func([]byte)) []byte {
		raw := externalTestThinImage(externalArm64CPU, 0, externalMachOExecute)
		mutate(raw)
		return raw
	}
	fat := func(wide bool, mutate func([]byte)) []byte {
		raw := externalTestFatImage(binary.BigEndian, wide, externalArm64CPU)
		mutate(raw)
		return raw
	}
	for _, test := range []struct {
		name string
		raw  []byte
	}{
		{"empty", nil},
		{"shebang", []byte("#!/bin/sh\nexit 0\n")},
		{"ELF", []byte("\x7fELF\x02\x01\x01\x00\x00\x00\x00\x00")},
		{"truncated thin", externalTestThinImage(externalArm64CPU, 0, externalMachOExecute)[:31]},
		{"wrong thin arch", externalTestThinImage(0x01000007, 0, externalMachOExecute)},
		{"dylib", externalTestThinImage(externalArm64CPU, 0, 6)},
		{"object", externalTestThinImage(externalArm64CPU, 0, 1)},
		{"thin reserved", thin(func(raw []byte) { binary.LittleEndian.PutUint32(raw[28:], 1) })},
		{"truncated load commands", thin(func(raw []byte) {
			binary.LittleEndian.PutUint32(raw[16:20], 1)
			binary.LittleEndian.PutUint32(raw[20:24], 8)
		})},
		{"command count overflow", thin(func(raw []byte) { binary.LittleEndian.PutUint32(raw[16:20], ^uint32(0)) })},
		{"empty fat", fat(false, func(raw []byte) { binary.BigEndian.PutUint32(raw[4:8], 0) })},
		{"fat count cap", fat(false, func(raw []byte) { binary.BigEndian.PutUint32(raw[4:8], externalFatSliceCap+1) })},
		{"fat count overflow", fat(false, func(raw []byte) { binary.BigEndian.PutUint32(raw[4:8], ^uint32(0)) })},
		{"truncated fat table", externalTestFatImage(binary.BigEndian, false, externalArm64CPU)[:27]},
		{"no selected arch", externalTestFatImage(binary.BigEndian, false, 0x01000007)},
		{"duplicate selected arch", externalTestFatImage(binary.BigEndian, false, externalArm64CPU, externalArm64CPU)},
		{"selected CPU differs", fat(false, func(raw []byte) { binary.LittleEndian.PutUint32(raw[32:36], 0x01000007) })},
		{"selected subtype differs", fat(false, func(raw []byte) { binary.BigEndian.PutUint32(raw[12:16], 2) })},
		{"selected library", fat(false, func(raw []byte) { binary.LittleEndian.PutUint32(raw[40:44], 6) })},
		{"slice overlaps table", fat(false, func(raw []byte) { binary.BigEndian.PutUint32(raw[16:20], 8) })},
		{"slice exceeds file", fat(false, func(raw []byte) { binary.BigEndian.PutUint32(raw[20:24], ^uint32(0)) })},
		{"slice undersized", fat(false, func(raw []byte) { binary.BigEndian.PutUint32(raw[20:24], 31) })},
		{"alignment overflow", fat(false, func(raw []byte) { binary.BigEndian.PutUint32(raw[24:28], 64) })},
		{"unaligned slice", fat(false, func(raw []byte) { binary.BigEndian.PutUint32(raw[24:28], 5) })},
		{"fat64 offset overflow", fat(true, func(raw []byte) { binary.BigEndian.PutUint64(raw[16:24], ^uint64(0)) })},
		{"fat64 size overflow", fat(true, func(raw []byte) { binary.BigEndian.PutUint64(raw[24:32], ^uint64(0)) })},
		{"fat64 reserved", fat(true, func(raw []byte) { binary.BigEndian.PutUint32(raw[36:40], 1) })},
		{"overlapping slices", func() []byte {
			raw := externalTestFatImage(binary.BigEndian, false, 0x01000007, externalArm64CPU)
			binary.BigEndian.PutUint32(raw[36:40], 48)
			return raw
		}()},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "invalid-header-never-executed")
			if err := os.WriteFile(path, test.raw, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := validateExternalToolImage(path); err == nil {
				t.Fatal("unsupported or malformed native image header was accepted")
			}
		})
	}
}

func TestValidateExternalToolImageRejectsLinkedOrNonExecutableFiles(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "header-only-never-executed")
	if err := os.WriteFile(path, externalTestThinImage(externalArm64CPU, 0, externalMachOExecute), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateExternalToolImage(path); err == nil {
		t.Fatal("non-executable regular file was accepted")
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{link, root, filepath.Join(root, "missing")} {
		if err := validateExternalToolImage(invalid); err == nil {
			t.Fatal("linked, directory, or absent image was accepted")
		}
	}
}
