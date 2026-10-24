package t421

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
)

const (
	executionLinuxPressureAuthorBrokerMode    = "run-t422-linux-pressure-author-broker-v1"
	executionLinuxPressureAuthorBootstrapMode = "run-t422-linux-pressure-author-bootstrap-v1"
	executionLinuxPressureAuthorTargetMode    = "run-t422-linux-pressure-author-target-v1"
	executionLinuxPressureAuthorSchema        = "t422-linux-pressure-author-v1"
	linuxPressureAuthorInputRecipe            = "linux-input-tree-pressure-namespace-v2"
	linuxPressureAuthorReferenceRecipe        = "linux-go-reference-pressure-namespace-v2"
)

// Every field is comparison/selection data. Only the live source graph issues
// the namespace, producer and retirement capabilities in the receiving process.
type linuxPressureAuthorSelection struct {
	Schema              string                       `json:"schema"`
	Borrow              linuxPressureBorrowSelection `json:"borrow"`
	AuthorBinary        string                       `json:"author_binary"`
	ImageBytes          uint64                       `json:"image_bytes"`
	ViewNamespaceDevice uint64                       `json:"view_namespace_device"`
	ViewNamespaceInode  uint64                       `json:"view_namespace_inode"`
	ViewMount           uint64                       `json:"view_mount"`
}

func encodeLinuxPressureAuthor(value linuxPressureAuthorSelection) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > maxExecutionSelectionBytes {
		return "", ErrExecutionLauncher
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
func decodeLinuxPressureAuthor(encoded string) (linuxPressureAuthorSelection, error) {
	var value linuxPressureAuthorSelection
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
	if err != nil || !bytes.Equal(raw, canonical) || value.Schema != executionLinuxPressureAuthorSchema {
		return value, ErrExecutionLauncher
	}
	return value, nil
}
func linuxPressureAuthorReceipt(attempt string, retained bool) string {
	phase := "pressure-not-retained"
	if retained {
		phase = "actors-retired-pressure-retained"
	}
	return executionLinuxPressureAuthorSchema + ":" + phase + ":" + attempt + "\n"
}
