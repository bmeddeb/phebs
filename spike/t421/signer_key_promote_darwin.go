//go:build darwin

package t421

import "golang.org/x/sys/unix"

// renameExecutionSignerExclusive atomically renames oldName to newName inside
// the same held directory descriptor, refusing any existing destination.
// Callers retain the exact hold, identity and durability checks around it.
func renameExecutionSignerExclusive(oldDirectory, newDirectory int, oldName, newName string) error {
	return unix.RenameatxNp(oldDirectory, oldName, newDirectory, newName, unix.RENAME_EXCL)
}
