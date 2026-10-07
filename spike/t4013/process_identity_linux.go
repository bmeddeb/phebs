//go:build linux

package t4013

func processStartIdentity(pid int, _ processSnapshot) (processIdentityObservation, error) {
	record, err := linuxProcessStatAt("/proc", pid)
	if err != nil {
		return processIdentityObservation{}, err
	}
	return processIdentityObservation{token: record.snapshot.identityToken,
		parent: record.snapshot.parent, name: record.snapshot.name}, nil
}
