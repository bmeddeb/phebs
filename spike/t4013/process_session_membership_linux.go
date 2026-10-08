//go:build linux

package t4013

// nativeSessionMemberHooks supplies the Linux half of the shared session fence:
// one coherent /proc observation per call, and the native stat state narrowed to
// the defunct question the fence actually asks.
func nativeSessionMemberHooks() (func(int) (processSnapshot, error), func(int) (bool, bool, error)) {
	return linuxProcessObservation, linuxProcessDefunctStatus
}
