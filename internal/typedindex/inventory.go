package typedindex

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"path"
	"slices"
	"strings"
)

// Inventory ceilings retain the T45.1a importer limits. This decoder is a pure
// preflight contract; T45.4 must inventory and verify the actual private copy.
const (
	InventorySchema         = "phebs-typed-prehydration-v1"
	MaxInventoryBytes       = 16 << 20
	MaxInventoryFiles       = 50000
	MaxInventoryDirectories = 20000
	MaxBundleBytes          = int64(2 << 30)
	MaxFileBytes            = int64(256 << 20)
)

type BundleFile struct {
	Path       string `json:"path"`
	Bytes      int64  `json:"bytes"`
	Digest     string `json:"digest"`
	Executable bool   `json:"executable"`
}
type InventoryDefinition struct {
	Schema string       `json:"schema"`
	Files  []BundleFile `json:"files"`
}
type Inventory struct {
	digest string
	files  []BundleFile
	bytes  int64
}

func (i Inventory) Digest() string      { return i.digest }
func (i Inventory) Files() []BundleFile { return slices.Clone(i.files) }
func (i Inventory) Bytes() int64        { return i.bytes }

// File looks up an immutable entry without copying the complete inventory.
func (i Inventory) File(name string) (BundleFile, bool) {
	n, ok := slices.BinarySearchFunc(i.files, name, func(f BundleFile, name string) int { return strings.Compare(f.Path, name) })
	if !ok {
		return BundleFile{}, false
	}
	return i.files[n], true
}
func DecodeInventory(ctx context.Context, raw []byte, expected string) (Inventory, error) {
	if err := ctx.Err(); err != nil {
		return Inventory{}, err
	}
	if len(raw) > MaxInventoryBytes || !digest(expected) || hash(raw) != expected {
		return Inventory{}, Invalid
	}
	// Decode one entry at a time so the count cap applies before allocating
	// an attacker-selected array. The final canonical comparison also rejects
	// duplicate/missing fields inside each bounded file record.
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	take := func(want any) bool { got, err := dec.Token(); return err == nil && got == want }
	var d InventoryDefinition
	if !take(json.Delim('{')) || !take("schema") || dec.Decode(&d.Schema) != nil || !take("files") || !take(json.Delim('[')) {
		return Inventory{}, Invalid
	}
	for dec.More() {
		if err := ctx.Err(); err != nil {
			return Inventory{}, err
		}
		if len(d.Files) >= MaxInventoryFiles {
			return Inventory{}, Invalid
		}
		var f BundleFile
		if dec.Decode(&f) != nil {
			return Inventory{}, Invalid
		}
		d.Files = append(d.Files, f)
	}
	if !take(json.Delim(']')) || !take(json.Delim('}')) {
		return Inventory{}, Invalid
	}
	want, err := json.Marshal(d)
	if err != nil {
		return Inventory{}, Invalid
	}
	var compact bytes.Buffer
	if json.Compact(&compact, raw) != nil || !bytes.Equal(want, compact.Bytes()) {
		return Inventory{}, Invalid
	}
	if d.Schema != InventorySchema || len(d.Files) == 0 || len(d.Files) > MaxInventoryFiles {
		return Inventory{}, Invalid
	}
	dirs := map[string]bool{}
	files := make(map[string]bool, len(d.Files))
	var total int64
	previous := ""
	for _, f := range d.Files {
		if err := ctx.Err(); err != nil {
			return Inventory{}, err
		}
		if !bundlePath(f.Path) || f.Path <= previous || !digest(f.Digest) || f.Bytes < 0 || f.Bytes > MaxFileBytes || f.Bytes > MaxBundleBytes-total {
			return Inventory{}, Invalid
		}
		files[f.Path] = true
		total += f.Bytes
		previous = f.Path
		for dir := path.Dir(f.Path); dir != "."; dir = path.Dir(dir) {
			if dirs[dir] {
				break
			}
			dirs[dir] = true
			if len(dirs) > MaxInventoryDirectories {
				return Inventory{}, Invalid
			}
		}
	}
	for dir := range dirs {
		if files[dir] {
			return Inventory{}, Invalid
		}
	}
	return Inventory{expected, d.Files, total}, nil
}
func bundlePath(s string) bool {
	if s == "." || len(s) > 512 || !fs.ValidPath(s) || strings.ContainsAny(s, "\\:") {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	for _, c := range strings.Split(s, "/") {
		if len(c) > 255 {
			return false
		}
	}
	return true
}
