package typedsandbox

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const ScratchAuthorityFile = "typed-scratch.json"

// MaxScratchAuthorityBytes is the existing worker/control decoder byte ceiling.
const MaxScratchAuthorityBytes = 4096

// ScratchAuthority is captured by the privileged host owner from the private fixed
// filesystem before launch, then sealed into the read-only controls directory.
// Blocks describes statfs usable blocks, not the larger filesystem image.
type ScratchAuthority struct {
	Source      string `json:"source"`
	DeviceMajor uint32 `json:"device_major"`
	DeviceMinor uint32 `json:"device_minor"`
	BlockSize   uint64 `json:"block_size"`
	Blocks      uint64 `json:"blocks"`
	Inodes      uint64 `json:"inodes"`
	ImageBytes  uint64 `json:"image_bytes"`
}

func (a ScratchAuthority) Validate() error {
	const base = "/var/lib/phebs-typed-index/"
	suffix := strings.TrimPrefix(a.Source, base)
	if suffix == a.Source || !strings.HasSuffix(suffix, "/scratch") || strings.Count(suffix, "/") != 1 || filepath.Clean(a.Source) != a.Source {
		return ErrRefused
	}
	id := strings.TrimSuffix(suffix, "/scratch")
	if len(id) < 1 || len(id) > 80 {
		return ErrRefused
	}
	for _, c := range id {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return ErrRefused
		}
	}
	if a.DeviceMajor != 7 || a.BlockSize != 4096 || a.ImageBytes != ScratchBytes/4096*4096 || a.Blocks == 0 || a.Blocks > a.ImageBytes/a.BlockSize || a.Inodes != ScratchInodes {
		return ErrRefused
	}
	return nil
}

func DecodeScratchAuthority(raw []byte) (ScratchAuthority, error) {
	var a ScratchAuthority
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if len(raw) > MaxScratchAuthorityBytes || dec.Decode(&a) != nil || dec.Decode(new(any)) != io.EOF || a.Validate() != nil {
		return a, ErrRefused
	}
	canonical, err := EncodeScratchAuthority(a)
	if err != nil || !bytes.Equal(raw, canonical) {
		return ScratchAuthority{}, ErrRefused
	}
	return a, nil
}

func scratchDevice(a ScratchAuthority) string {
	return fmt.Sprintf("%d:%d", a.DeviceMajor, a.DeviceMinor)
}

func EncodeScratchAuthority(a ScratchAuthority) ([]byte, error) {
	if a.Validate() != nil {
		return nil, ErrRefused
	}
	return json.Marshal(a)
}

// A bind of the filesystem root may not hide an extra writable submount. The
// loop device is not exposed to the container, and neither process can mount.
func verifyScratchMounts(raw string, a ScratchAuthority) error {
	return verifyScratchMountpoint(raw, a, "/scratch")
}

func verifyScratchMountpoint(raw string, a ScratchAuthority, mountpoint string) error {
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 {
			return ErrRefused
		}
		if strings.HasPrefix(fields[4], mountpoint+"/") {
			return ErrRefused
		}
		if fields[4] != mountpoint {
			continue
		}
		count++
		if fields[2] != scratchDevice(a) || fields[3] != "/" {
			return ErrRefused
		}
		flags := "," + fields[5] + ","
		for _, flag := range []string{"rw", "nosuid", "nodev"} {
			if !strings.Contains(flags, ","+flag+",") {
				return ErrRefused
			}
		}
		if strings.Contains(flags, ",noexec,") {
			return ErrRefused
		}
		delimiter := -1
		for i := 6; i < len(fields); i++ {
			if fields[i] == "-" {
				delimiter = i
				break
			}
			if strings.HasPrefix(fields[i], "shared:") || strings.HasPrefix(fields[i], "master:") {
				return ErrRefused
			}
		}
		if delimiter < 0 || len(fields) != delimiter+4 || fields[delimiter+1] != "ext4" || !strings.Contains(","+fields[delimiter+3]+",", ",rw,") {
			return ErrRefused
		}
	}
	if count != 1 {
		return ErrRefused
	}
	return nil
}

// Check only one directory entry before the Phase 2 worker can write anything.
func scratchEmpty(root string) bool {
	d, err := os.Open(root)
	if err != nil {
		return false
	}
	entries, readErr := d.ReadDir(1)
	closeErr := d.Close()
	return len(entries) == 0 && readErr == io.EOF && closeErr == nil
}
