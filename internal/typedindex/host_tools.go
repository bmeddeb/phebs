package typedindex

import (
	"bytes"
	"context"
	"encoding/json"
)

const (
	ManagedHelperFile = "phebs-typed-worker"
	HostToolsFile     = "typed-host-tools.json"
	HostToolsSchema   = "phebs-typed-host-tools-v1"
	MaxHostToolsBytes = 1024
)

// HostToolsDefinition belongs to the immutable prehydration bundle. The host
// formatter path and arguments remain compiled into the sandbox owner.
type HostToolsDefinition struct {
	Schema     string `json:"schema"`
	MkfsDigest string `json:"mkfs_sha256"`
}

type BoundHostTools struct {
	Helper     BundleFile
	MkfsDigest string
}

// BindHostTools authenticates expected tool identities through the already
// admitted inventory. It does not read files or prove native readiness. The
// controller must verify the private input copy, read metadata from it and keep
// its lifetime pin; native preparation still hashes the opened formatter.
// Cost: one bounded inventory pass without cloning it and at most 1KiB of
// metadata hashing/decoding. No I/O, locks, cache, goroutines or child processes.
func BindHostTools(ctx context.Context, admitted Admission, inventory Inventory, raw []byte) (BoundHostTools, error) {
	if ctx == nil {
		return BoundHostTools{}, Invalid
	}
	if err := ctx.Err(); err != nil {
		return BoundHostTools{}, err
	}
	if admitted.Digest() == "" || inventory.Digest() == "" || admitted.Request().BundleDigest != inventory.Digest() || len(raw) == 0 || len(raw) > MaxHostToolsBytes {
		return BoundHostTools{}, Invalid
	}
	var helper, metadata BundleFile
	for _, file := range inventory.files {
		if err := ctx.Err(); err != nil {
			return BoundHostTools{}, err
		}
		switch file.Path {
		case ManagedHelperFile:
			helper = file
		case HostToolsFile:
			metadata = file
		}
	}
	if helper.Path == "" || !helper.Executable || helper.Bytes == 0 || metadata.Path == "" || metadata.Executable || metadata.Bytes != int64(len(raw)) || metadata.Digest != hash(raw) {
		return BoundHostTools{}, Invalid
	}
	var definition HostToolsDefinition
	if err := decode(raw, MaxHostToolsBytes, &definition); err != nil {
		return BoundHostTools{}, err
	}
	canonical, err := json.Marshal(definition)
	if err != nil || !bytes.Equal(raw, canonical) || definition.Schema != HostToolsSchema || !digest(definition.MkfsDigest) {
		return BoundHostTools{}, Invalid
	}
	return BoundHostTools{Helper: helper, MkfsDigest: definition.MkfsDigest}, nil
}
