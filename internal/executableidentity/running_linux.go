package executableidentity

import "os"

// Linux exposes the executed inode even after rename or unlink. Following this
// kernel-owned proc link deliberately avoids the mutable deployment pathname.
func openRunningExecutable() (*os.File, error) {
	return os.Open("/proc/self/exe")
}
