package t421

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash"
	"path"
	"strings"
	"unicode/utf8"
)

// These are copy-construction ceilings, not host resource or command admission.
const (
	linuxInputTreeRecipe = "linux-input-tree-namespace-v1"
	linuxInputTreeRole   = "t42-input-tree-helper"
	treeWireHeader       = 24
	treeWireChunk        = 32 << 10
	treeWireMaximum      = treeWireHeader + treeWireChunk + maxInputCustodyPathBytes
	treeWireInit         = 1
	treeWireDirectory    = 2
	treeWireFile         = 3
	treeWireData         = 4
	treeWireFileEnd      = 5
	treeWireEnd          = 6
	treeWireReady        = 7
	treeWireAck          = 8
)

var ErrLinuxInputTree = errors.New("linux protected input tree unavailable or changed")

// Publish the terminal fields before releasing the construction context. A
// caller selecting the cancellation arm can then observe that exact terminal
// result, rather than treating successful cleanup cancellation as a failure.
func completeInputTreeConstruction(done chan struct{}, cancel func()) {
	close(done)
	cancel()
}

type inputTreeEntry struct {
	name   string
	mode   uint32
	size   uint64
	digest [32]byte
}

type inputTreeInventory struct {
	entries, files, pathBytes uint64
	bytes                     uint64
}

// One census is retained per side. File bodies are never retained in Go memory.
type inputTreeCensus struct {
	rows      []inputTreeEntry
	names     map[string]bool
	inventory inputTreeInventory
	last      string
}

func (census *inputTreeCensus) add(entry inputTreeEntry) error {
	if entry.name == "" || len(entry.name) > maxInputCustodyPathBytes || !utf8.ValidString(entry.name) ||
		strings.ContainsAny(entry.name, "\\\x00\r\n") || path.Clean(entry.name) != entry.name ||
		entry.name == "." || strings.HasPrefix(entry.name, "/") || strings.HasPrefix(entry.name, "../") ||
		entry.name <= census.last || census.inventory.entries >= maxGoBuildEntries ||
		uint64(len(entry.name)) > maxGoBuildPathBytes-census.inventory.pathBytes ||
		entry.mode != 0o700 && entry.mode != 0o400 && entry.mode != 0o500 ||
		entry.mode == 0o700 && entry.size != 0 || entry.mode == 0o500 && entry.size == 0 || entry.size > maxInputCustodyFileBytes ||
		entry.size > maxGoBuildBytes-census.inventory.bytes {
		return ErrLinuxInputTree
	}
	parent := path.Dir(entry.name)
	if parent != "." && !census.names[parent] {
		return ErrLinuxInputTree
	}
	if census.names == nil {
		census.names = make(map[string]bool)
	}
	census.names[entry.name] = entry.mode == 0o700
	census.last = entry.name
	census.inventory.entries++
	census.inventory.pathBytes += uint64(len(entry.name))
	census.inventory.bytes += entry.size
	if entry.mode != 0o700 {
		census.inventory.files++
	}
	census.rows = append(census.rows, entry)
	return nil
}

func encodeInputTreeEntry(entry inputTreeEntry) []byte {
	raw := make([]byte, 12+len(entry.name))
	binary.BigEndian.PutUint32(raw, entry.mode)
	binary.BigEndian.PutUint64(raw[4:], entry.size)
	copy(raw[12:], entry.name)
	return raw
}

func decodeInputTreeEntry(raw []byte) (inputTreeEntry, error) {
	if len(raw) < 13 || len(raw) > 12+maxInputCustodyPathBytes {
		return inputTreeEntry{}, ErrLinuxInputTree
	}
	return inputTreeEntry{name: string(raw[12:]), mode: binary.BigEndian.Uint32(raw), size: binary.BigEndian.Uint64(raw[4:])}, nil
}

func inputTreeManifest(census inputTreeCensus) []byte {
	digest := sha256.New()
	_, _ = digest.Write([]byte(linuxInputTreeRecipe + "\x00"))
	for _, entry := range census.rows {
		writeInputTreeManifestEntry(digest, entry)
	}
	raw := make([]byte, 64)
	binary.BigEndian.PutUint64(raw, census.inventory.entries)
	binary.BigEndian.PutUint64(raw[8:], census.inventory.files)
	binary.BigEndian.PutUint64(raw[16:], census.inventory.bytes)
	binary.BigEndian.PutUint64(raw[24:], census.inventory.pathBytes)
	copy(raw[32:], digest.Sum(nil))
	return raw
}

func writeInputTreeManifestEntry(digest hash.Hash, entry inputTreeEntry) {
	raw := encodeInputTreeEntry(entry)
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(raw)))
	_, _ = digest.Write(length[:])
	_, _ = digest.Write(raw)
	_, _ = digest.Write(entry.digest[:])
}

func encodeInputTreeFrame(kind byte, ordinal uint64, payload []byte) ([]byte, error) {
	if kind < treeWireInit || kind > treeWireAck || ordinal == 0 || len(payload) > treeWireMaximum-treeWireHeader {
		return nil, ErrLinuxInputTree
	}
	raw := make([]byte, treeWireHeader+len(payload))
	copy(raw, "ITN1")
	raw[4] = kind
	binary.BigEndian.PutUint64(raw[8:], ordinal)
	binary.BigEndian.PutUint32(raw[16:], uint32(len(payload)))
	copy(raw[treeWireHeader:], payload)
	return raw, nil
}

func decodeInputTreeFrame(raw []byte, ordinal uint64) (byte, []byte, error) {
	if len(raw) < treeWireHeader || len(raw) > treeWireMaximum || string(raw[:4]) != "ITN1" ||
		raw[4] < treeWireInit || raw[4] > treeWireAck || !bytes.Equal(raw[5:8], []byte{0, 0, 0}) ||
		!bytes.Equal(raw[20:24], []byte{0, 0, 0, 0}) || ordinal == 0 || binary.BigEndian.Uint64(raw[8:]) != ordinal ||
		uint64(binary.BigEndian.Uint32(raw[16:])) != uint64(len(raw)-treeWireHeader) {
		return 0, nil, ErrLinuxInputTree
	}
	return raw[4], raw[treeWireHeader:], nil
}
