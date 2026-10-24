package t421

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
)

const (
	executionLinuxPressureBrokerMode = "run-t422-linux-pressure-broker-v1"
	executionLinuxPressureChildMode  = "run-t422-linux-pressure-child-v1"
	executionLinuxPressureSchema     = "t422-linux-pressure-borrow-v1"
)

// Request/comparison values only. The broker creates its actual volume and
// opaque live issuer locally; neither a capability nor provenance is decoded.
type linuxPressureBorrowSelection struct {
	Schema       string                        `json:"schema"`
	Broker       linuxExecutorPreworkSelection `json:"broker"`
	Target       linuxExecutorPreworkSelection `json:"target"`
	Pressure     LinuxPressureRequest          `json:"pressure"`
	UID          uint32                        `json:"uid"`
	GID          uint32                        `json:"gid"`
	NamespaceDev uint64                        `json:"namespace_device"`
	NamespaceIno uint64                        `json:"namespace_inode"`
	LeafDevice   uint64                        `json:"leaf_device"`
	LeafInode    uint64                        `json:"leaf_inode"`
	LeafMount    uint64                        `json:"leaf_mount"`
	LeafFSID     [2]int32                      `json:"leaf_fsid"`
}

func encodeLinuxPressureBorrow(value linuxPressureBorrowSelection) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > maxExecutionSelectionBytes {
		return "", ErrExecutionLauncher
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeLinuxPressureBorrow(encoded string) (linuxPressureBorrowSelection, error) {
	var value linuxPressureBorrowSelection
	if len(encoded) == 0 || len(encoded) > maxExecutionSelectionCharacters {
		return value, ErrExecutionLauncher
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || len(raw) > maxExecutionSelectionBytes || base64.RawURLEncoding.EncodeToString(raw) != encoded {
		return value, ErrExecutionLauncher
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF {
		return value, ErrExecutionLauncher
	}
	canonical, err := json.Marshal(value)
	if err != nil || !bytes.Equal(raw, canonical) || value.Schema != executionLinuxPressureSchema {
		return value, ErrExecutionLauncher
	}
	return value, nil
}

func linuxPressureBorrowReceipt(attempt string, retained bool) string {
	phase := "pressure-not-retained"
	if retained {
		phase = "actors-retired-pressure-retained"
	}
	return "t422-linux-pressure-borrow-v1:" + phase + ":" + attempt + "\n"
}
