// Package t457 retains the measured linux/amd64 tool inventory. The bytes
// were hashed from the pinned release and recipe outputs; this package does
// not download or execute them.
package t457

import "github.com/bmeddeb/phebs/internal/typedindex"

func ToolInventory() typedindex.InventoryDefinition {
	return typedindex.InventoryDefinition{
		Schema: typedindex.InventorySchema,
		Files: []typedindex.BundleFile{
			{Path: "tools/bin/bazel", Bytes: 65821854, Digest: "sha256:c44a93f25398c68f904fa1d19b61d321de6c0d2f09dca375d7bc0dc9b9428403", Executable: true},
			{Path: "tools/bin/gopackagesdriver", Bytes: 5210483, Digest: "sha256:2b58a9c9a294fc8d9c899bd66f881f7236ed4422a998a4cebab07662ec373bb8", Executable: true},
			{Path: "tools/bin/scip-go", Bytes: 18104856, Digest: "sha256:31bf2f3bbbcb25efd4bba6964e08971a9c9c2fba745db4345c0d438ef28b93c4", Executable: true},
			{Path: "tools/cc-sysroot.zip", Bytes: 72352400, Digest: "sha256:2f2ec79d40bb602c2c957ffa2f4be62ec2709a53e28060c57e5d5664a7f8dcde", Executable: false},
			{Path: "tools/go/bin/go", Bytes: 14939693, Digest: "sha256:b93cdfdbc72f1afc3f21498c80bf3d155a44a9b95e2d690c940511051574bc25", Executable: true},
		},
	}
}
