package t421

import (
	"encoding/binary"
	"errors"
	"os"

	"github.com/bmeddeb/phebs/spike/t4013"
)

const (
	externalMachO64Magic = 0xfeedfacf
	externalArm64CPU     = 0x0100000c
	externalMachOExecute = 2
	externalMachOHeader  = 32
	externalFatSliceCap  = 8
)

// validateExternalToolImage screens only bounded native executable headers. It
// proves neither vendor authenticity, load-command semantics, CPU-feature or
// entitlement compatibility, nor the absence of native delegation at runtime.
// The caller separately restricts the frozen host and hashes the image; neither
// observation establishes immutable executable custody.
func validateExternalToolImage(path string) (retErr error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0o111 == 0 || before.Size() < 8 {
		return errors.New("external tool image is not an executable regular file")
	}
	file, err := t4013.OpenHostImage(path)
	if err != nil {
		return errors.New("external tool image cannot be opened")
	}
	defer func() {
		after, statErr := file.Stat()
		current, pathErr := os.Lstat(path)
		closeErr := file.Close()
		if statErr != nil || pathErr != nil || closeErr != nil ||
			!sameCheckoutFile(before, after) || !sameCheckoutFile(before, current) {
			retErr = errors.Join(retErr, errors.New("external tool image changed during header inspection"))
		}
	}()
	opened, err := file.Stat()
	if err != nil || !sameCheckoutFile(before, opened) {
		return errors.New("external tool image changed before header inspection")
	}
	var prefix [8]byte
	if _, err := file.ReadAt(prefix[:], 0); err != nil {
		return errors.New("external tool image header is truncated")
	}
	if binary.LittleEndian.Uint32(prefix[:4]) == externalMachO64Magic {
		return validateExternalMachOSlice(file, 0, uint64(before.Size()), nil)
	}
	var order binary.ByteOrder
	var wide bool
	switch binary.BigEndian.Uint32(prefix[:4]) {
	case 0xcafebabe:
		order = binary.BigEndian
	case 0xbebafeca:
		order = binary.LittleEndian
	case 0xcafebabf:
		order, wide = binary.BigEndian, true
	case 0xbfbafeca:
		order, wide = binary.LittleEndian, true
	default:
		return errors.New("external tool image is not native Mach-O64")
	}
	count := order.Uint32(prefix[4:])
	if count == 0 || count > externalFatSliceCap {
		return errors.New("external tool fat-image slice count exceeds its bound")
	}
	stride := uint64(20)
	if wide {
		stride = 32
	}
	tableEnd := uint64(8) + uint64(count)*stride
	fileSize := uint64(before.Size())
	if tableEnd > fileSize {
		return errors.New("external tool fat-image table is truncated")
	}
	type slice struct{ offset, size uint64 }
	var intervals [externalFatSliceCap]slice
	var selected slice
	var subtype uint32
	selectedCount := 0
	for index := uint32(0); index < count; index++ {
		var raw [32]byte
		if _, err := file.ReadAt(raw[:stride], int64(8+uint64(index)*stride)); err != nil {
			return errors.New("external tool fat-image record is truncated")
		}
		cpu, currentSubtype := order.Uint32(raw[:4]), order.Uint32(raw[4:8])
		current := slice{offset: uint64(order.Uint32(raw[8:12])), size: uint64(order.Uint32(raw[12:16]))}
		alignment := order.Uint32(raw[16:20])
		if wide {
			current.offset, current.size = order.Uint64(raw[8:16]), order.Uint64(raw[16:24])
			alignment = order.Uint32(raw[24:28])
			if order.Uint32(raw[28:]) != 0 {
				return errors.New("external tool fat-image reserved field is invalid")
			}
		}
		if current.offset < tableEnd || current.offset > fileSize || current.size < externalMachOHeader ||
			current.size > fileSize-current.offset || alignment >= 64 || current.offset&((uint64(1)<<alignment)-1) != 0 {
			return errors.New("external tool fat-image slice bounds are invalid")
		}
		for _, prior := range intervals[:index] {
			if current.offset < prior.offset+prior.size && prior.offset < current.offset+current.size {
				return errors.New("external tool fat-image slices overlap")
			}
		}
		intervals[index] = current
		if cpu == externalArm64CPU {
			selectedCount++
			if selectedCount > 1 {
				return errors.New("external tool fat-image repeats its arm64 slice")
			}
			selected, subtype = current, currentSubtype
		}
	}
	if selectedCount != 1 {
		return errors.New("external tool fat-image has no arm64 slice")
	}
	return validateExternalMachOSlice(file, selected.offset, selected.size, &subtype)
}

func validateExternalMachOSlice(file *os.File, offset, size uint64, subtype *uint32) error {
	if size < externalMachOHeader {
		return errors.New("external tool Mach-O64 header is truncated")
	}
	var raw [externalMachOHeader]byte
	if _, err := file.ReadAt(raw[:], int64(offset)); err != nil {
		return errors.New("external tool Mach-O64 header is truncated")
	}
	order := binary.LittleEndian
	if order.Uint32(raw[:4]) != externalMachO64Magic || order.Uint32(raw[4:8]) != externalArm64CPU ||
		order.Uint32(raw[12:16]) != externalMachOExecute || order.Uint32(raw[28:]) != 0 ||
		subtype != nil && order.Uint32(raw[8:12]) != *subtype {
		return errors.New("external tool image is not an arm64 executable")
	}
	commands, commandBytes := order.Uint32(raw[16:20]), order.Uint32(raw[20:24])
	if uint64(commandBytes) > size-externalMachOHeader || commandBytes%8 != 0 || commands > commandBytes/8 ||
		commands == 0 && commandBytes != 0 {
		return errors.New("external tool Mach-O64 command bounds are invalid")
	}
	return nil
}
