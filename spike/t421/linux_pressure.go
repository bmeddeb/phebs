package t421

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bmeddeb/phebs/internal/lifecycle"
)

const (
	linuxPressureMinimumImage = 64 << 20
	linuxPressureMaximumImage = 96 << 30
	linuxPressureDiskFloor    = 120 << 30
	linuxPressureRecordLimit  = 8 << 10
	linuxPressureMountLimit   = 1 << 20
	linuxPressureRole         = "linux-pressure-mkfs-ext4"
)

var ErrLinuxPressure = errors.New("linux pressure filesystem unavailable or retained")

// LinuxPressureRequest is trusted operator input, never browser input or host
// admission. Parent must already be a canonical root-private directory. The
// formatter digest must come from a prospective operator binding, not from
// hashing the installed path and accepting whatever is there. ImageBytes is a
// mechanism bound; it is not the usable ext4 capacity or historical APFS facts.
// The actual backing mount must already be private, for example inside a
// future admitted privileged launcher's owned mount namespace. This primitive
// observes that condition and never changes host propagation to obtain it.
type LinuxPressureRequest struct {
	Parent          string
	ImageBytes      uint64
	FormatterSHA256 string // sha256: followed by 64 lowercase hexadecimal digits
}

// LinuxPressureGeometry is a native observation, not a pressure-phase result,
// process-drain certificate, immutable-input claim or permission to execute.
// Available bytes and free inodes are samples and can change during a loan.
type LinuxPressureGeometry struct {
	ImageBytes     uint64   `json:"image_bytes"`
	Device         uint64   `json:"device"`
	FSID           [2]int32 `json:"fsid"`
	MountID        uint64   `json:"mount_id"`
	BlockBytes     uint64   `json:"block_bytes"`
	Blocks         uint64   `json:"blocks"`
	AvailableBytes uint64   `json:"available_bytes"`
	Inodes         uint64   `json:"inodes"`
	FreeInodes     uint64   `json:"free_inodes"`
}

func validLinuxPressureRequest(request LinuxPressureRequest) bool {
	if request.Parent == "/" || !filepath.IsAbs(request.Parent) || filepath.Clean(request.Parent) != request.Parent ||
		len(request.Parent) > 800 || !validExecutionSHA256(request.FormatterSHA256) ||
		request.ImageBytes < linuxPressureMinimumImage || request.ImageBytes > linuxPressureMaximumImage || request.ImageBytes%4096 != 0 {
		return false
	}
	// Keep the owned mount path literal in mountinfo; no escaped/private path
	// or alternate unmount target is admitted by this small operator API.
	for _, character := range request.Parent {
		if character != '/' && character != '.' && character != '_' && character != '-' &&
			(character < 'a' || character > 'z') && (character < 'A' || character > 'Z') && (character < '0' || character > '9') {
			return false
		}
	}
	return true
}

func validLinuxPressureGeometry(value LinuxPressureGeometry) bool {
	return value.ImageBytes >= linuxPressureMinimumImage && value.ImageBytes <= linuxPressureMaximumImage && value.ImageBytes%4096 == 0 &&
		value.Device != 0 && value.FSID != ([2]int32{}) && value.MountID != 0 && value.BlockBytes == 4096 &&
		value.Blocks > 0 && value.Blocks <= value.ImageBytes/value.BlockBytes &&
		value.AvailableBytes <= value.Blocks*value.BlockBytes && value.Inodes > 0 && value.FreeInodes <= value.Inodes
}

func sameLinuxPressureGeometry(left, right LinuxPressureGeometry) bool {
	left.AvailableBytes, left.FreeInodes = 0, 0
	right.AvailableBytes, right.FreeInodes = 0, 0
	return left == right
}

// A new-file full-range fallocate reservation is charged in addition to bounded
// owner/journal metadata. This is conservative supplied arithmetic, not a
// continuous backing-space reservation against unrelated host writers.
func linuxPressureBackingBudget(imageBytes, block uint64) (uint64, error) {
	if imageBytes < linuxPressureMinimumImage || imageBytes > linuxPressureMaximumImage || imageBytes%4096 != 0 ||
		block < 512 || block > 1<<20 || block&(block-1) != 0 {
		return 0, ErrLinuxPressure
	}
	round := func(value uint64) uint64 { return (value + block - 1) / block * block }
	return round(imageBytes) + 2*round(linuxPressureRecordLimit) + 16*block, nil
}

type linuxPressureNode struct {
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
}

type linuxPressureRecord struct {
	Schema          string                `json:"schema"`
	Token           string                `json:"token"`
	Phase           string                `json:"phase"`
	Parent          linuxPressureNode     `json:"parent"`
	Root            linuxPressureNode     `json:"root"`
	Image           linuxPressureNode     `json:"image"`
	ImageBytes      uint64                `json:"image_bytes"`
	FormatterSHA256 string                `json:"formatter_sha256"`
	FormatterRole   string                `json:"formatter_role"`
	Loop            int                   `json:"loop"`
	Geometry        LinuxPressureGeometry `json:"geometry"`
}

var linuxPressurePhases = [...]string{"created", "image", "allocated", "formatting", "formatted", "loop-selected", "attached", "mounted", "ready", "retiring", "unmounted", "detached", "image-removed"}

func linuxPressurePhase(phase string) int {
	for index, candidate := range linuxPressurePhases {
		if phase == candidate {
			return index
		}
	}
	return -1
}

func validLinuxPressureRecord(record linuxPressureRecord) bool {
	phase := linuxPressurePhase(record.Phase)
	if record.Schema != "phebs-linux-pressure-owner-v1" || record.FormatterRole != linuxPressureRole || phase < 0 || len(record.Token) != 32 ||
		!validExecutionSHA256(record.FormatterSHA256) || record.Parent.Device == 0 || record.Parent.Inode == 0 || record.Root.Inode == 0 || record.Parent == record.Root ||
		record.Parent.Device != record.Root.Device || record.ImageBytes < linuxPressureMinimumImage ||
		record.ImageBytes > linuxPressureMaximumImage || record.ImageBytes%4096 != 0 || record.Loop < -1 || record.Loop > 1048575 {
		return false
	}
	for _, character := range record.Token {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	if phase == 0 && record.Image != (linuxPressureNode{}) ||
		phase > 0 && (record.Image.Inode == 0 || record.Image.Device != record.Root.Device || record.Image == record.Root || record.Image == record.Parent) ||
		phase < linuxPressurePhase("loop-selected") && record.Loop != -1 || phase >= linuxPressurePhase("loop-selected") && record.Loop < 0 {
		return false
	}
	if phase < linuxPressurePhase("ready") {
		return record.Geometry == (LinuxPressureGeometry{})
	}
	return validLinuxPressureGeometry(record.Geometry) && record.Geometry.ImageBytes == record.ImageBytes &&
		record.Geometry.Device != record.Parent.Device
}

func decodeLinuxPressureRecord(raw []byte) (linuxPressureRecord, error) {
	var record linuxPressureRecord
	if len(raw) == 0 || len(raw) > linuxPressureRecordLimit || json.Unmarshal(raw, &record) != nil || !validLinuxPressureRecord(record) {
		return record, ErrLinuxPressure
	}
	canonical, err := json.Marshal(record)
	if err != nil || !bytes.Equal(raw, canonical) {
		return linuxPressureRecord{}, ErrLinuxPressure
	}
	return record, nil
}

type linuxPressureLoop struct {
	Number, Flags, Encryption, KeyBytes uint32
	Device, Inode, Offset, Limit        uint64
}

func linuxPressureLoopMatches(record linuxPressureRecord, observed linuxPressureLoop) bool {
	// Linux LO_FLAGS_DIRECT_IO=16. No autoclear, partition scan, encryption,
	// readonly or offset interpretation is admitted by this owner.
	return record.Loop >= 0 && observed.Number == uint32(record.Loop) && observed.Device == record.Image.Device &&
		observed.Inode == record.Image.Inode && observed.Offset == 0 && observed.Limit == record.ImageBytes &&
		observed.Flags == 16 && observed.Encryption == 0 && observed.KeyBytes == 0
}

type linuxPressureMount struct {
	ID, ParentID uint64
}

// A new mount under a shared/slave backing mount can propagate before it is
// made private. Require actual preexisting private backing, without changing
// the host's mount propagation or inferring namespace privilege from EUID0.
func linuxPressurePrivateBacking(raw []byte, id uint64, device string) bool {
	if len(raw) == 0 || len(raw) > linuxPressureMountLimit || !bytes.HasSuffix(raw, []byte("\n")) || id == 0 || device == "" {
		return false
	}
	found := false
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 {
			return false
		}
		observed, valid := linuxPressureDecimal(fields[0])
		if !valid {
			return false
		}
		if observed != id {
			continue
		}
		options := "," + fields[5] + ","
		if found || len(fields) != 10 || fields[6] != "-" || fields[2] != device ||
			fields[7] != "ext4" && fields[7] != "xfs" || !strings.Contains(options, ",rw,") || strings.Contains(options, ",ro,") {
			return false
		}
		found = true
	}
	return found
}

func linuxPressureDecimal(value string) (uint64, bool) {
	parsed, err := strconv.ParseUint(value, 10, 64)
	return parsed, err == nil && strconv.FormatUint(parsed, 10) == value
}

// Inspect only a bounded mount table. Duplicate/foreign mounts and any child
// mount or same-loop alias in this namespace refuse. Other namespaces and
// escaped descriptors remain outside this observation; detach is still required.
func linuxPressureMountObservation(raw []byte, path string, loop int, expectedParent uint64, wantMounted bool) (linuxPressureMount, error) {
	if len(raw) == 0 || len(raw) > linuxPressureMountLimit || !bytes.HasSuffix(raw, []byte("\n")) ||
		path == "" || loop < 0 || loop > 1048575 || expectedParent == 0 {
		return linuxPressureMount{}, ErrLinuxPressure
	}
	wantDevice := "7:" + strconv.Itoa(loop)
	wantSource := "/dev/loop" + strconv.Itoa(loop)
	var found linuxPressureMount
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 {
			return linuxPressureMount{}, ErrLinuxPressure
		}
		if strings.HasPrefix(fields[4], path+"/") || fields[2] == wantDevice && fields[4] != path {
			return linuxPressureMount{}, ErrLinuxPressure
		}
		if fields[4] != path {
			continue
		}
		// A fresh private ext4 mount has no optional propagation fields.
		if !wantMounted || found.ID != 0 || len(fields) != 10 || fields[6] != "-" ||
			fields[2] != wantDevice || fields[3] != "/" || fields[7] != "ext4" || fields[8] != wantSource {
			return linuxPressureMount{}, ErrLinuxPressure
		}
		id, idOK := linuxPressureDecimal(fields[0])
		parent, parentOK := linuxPressureDecimal(fields[1])
		if !idOK || !parentOK || id == 0 || id == parent || parent != expectedParent {
			return linuxPressureMount{}, ErrLinuxPressure
		}
		options := "," + fields[5] + ","
		for _, required := range []string{"rw", "nosuid", "nodev"} {
			if !strings.Contains(options, ","+required+",") {
				return linuxPressureMount{}, ErrLinuxPressure
			}
		}
		if strings.Contains(options, ",ro,") || strings.Contains(options, ",noexec,") || strings.Contains(options, ",suid,") || strings.Contains(options, ",dev,") ||
			!strings.Contains(","+fields[9]+",", ",rw,") || strings.Contains(","+fields[9]+",", ",ro,") || strings.Contains(","+fields[9]+",", ",discard,") {
			return linuxPressureMount{}, ErrLinuxPressure
		}
		found = linuxPressureMount{ID: id, ParentID: parent}
	}
	if wantMounted != (found.ID != 0) {
		return linuxPressureMount{}, ErrLinuxPressure
	}
	return found, nil
}

func linuxPressureFilesystemBytes(blocks, available, block uint64) (uint64, uint64, error) {
	if block < 512 || block > 1<<20 || block&(block-1) != 0 || blocks == 0 || available > blocks || blocks > math.MaxInt64/block {
		return 0, 0, ErrLinuxPressure
	}
	return blocks * block, available * block, nil
}

// The shared lifecycle gate admits at most 48 GiB per ordinary production
// reservation. This standalone image can be larger, so keep that gate's actual
// observed-capacity latch and apply this separate checked projection without
// widening the production reservation cap or manufacturing an observation.
func linuxPressureBackingProjection(total, available, reservation uint64) bool {
	if total == 0 || total > math.MaxInt64 || available > total || reservation > available {
		return false
	}
	projected := total - available + reservation
	// Require ceil(projected*100/total) < the existing normal watermark,
	// without multiplying potentially large filesystem byte counts by 100.
	ceiling := uint64(lifecycle.SoftWatermarkPercent - 1)
	limit := total/100*ceiling + total%100*ceiling/100
	return projected <= limit
}

func linuxPressureNextRecord(previous, next linuxPressureRecord) bool {
	if !validLinuxPressureRecord(previous) || !validLinuxPressureRecord(next) ||
		linuxPressurePhase(next.Phase) != linuxPressurePhase(previous.Phase)+1 {
		return false
	}
	allowed := previous
	allowed.Phase = next.Phase
	if next.Phase == "image" {
		allowed.Image = next.Image
	}
	if next.Phase == "loop-selected" {
		allowed.Loop = next.Loop
	}
	if next.Phase == "ready" {
		allowed.Geometry = next.Geometry
	}
	return next == allowed
}
