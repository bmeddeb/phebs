//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package store

// Non-Unix platforms cannot perform a signal-zero liveness probe. Treat every
// valid predecessor as live: requiring operator removal is safer than starting
// two database children against one data directory.
func processAlivePlatform(int) bool { return true }
