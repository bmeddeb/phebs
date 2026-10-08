//go:build darwin

package t4013

// nativeSessionMemberHooks supplies the Darwin half of the shared session fence:
// one coherent task-all-info observation per call, and the native short-BSD
// status narrowed to the defunct question the fence actually asks.
func nativeSessionMemberHooks() (func(int) (processSnapshot, error), func(int) (bool, bool, error)) {
	return darwinProcessObservation, darwinProcessDefunct
}

// darwinProcessDefunct reports whether one live native lifetime is a zombie.
// present is false once the lifetime is gone; a denial or malformed record
// refuses rather than guessing liveness.
func darwinProcessDefunct(pid int) (bool, bool, error) {
	status, present, err := darwinProcessStatus(pid)
	if err != nil || !present {
		return false, present, err
	}
	return status == darwinProcessZombie, true, nil
}
