//go:build darwin || linux

package t4013

import (
	"errors"

	"golang.org/x/sys/unix"
)

// PrivateProcessSessionMembership returns records from a bounded native session
// census, including orphans. Membership and liveness checks are bracketed by
// equal native lifetime identities; a non-defunct state is observed only at the
// middle check and can change afterward.
// Vanished, departed, defunct or changing members are omitted; a denied or
// malformed observation refuses the result. This is not an atomic session
// snapshot or complete history. Names and identities are private controller
// inputs, not executable-image authority or source-free evidence. Unsupported
// platforms fail before any inventory or helper process is started.
func PrivateProcessSessionMembership(sessionID int) ([]NativeProcessRecord, error) {
	observe, defunctOf := nativeSessionMemberHooks()
	pids, err := privateServerSessionPIDs(sessionID)
	if err != nil {
		return nil, err
	}
	records := make([]NativeProcessRecord, 0, len(pids))
	for _, pid := range pids {
		record, present, err := privateProcessSessionMember(pid, sessionID, observe, unix.Getsid, defunctOf)
		if err != nil {
			return nil, err
		}
		if present {
			records = append(records, record)
		}
	}
	return records, nil
}

func privateProcessSessionMember(
	pid, sessionID int,
	observe func(int) (processSnapshot, error),
	sessionOf func(int) (int, error),
	defunctOf func(int) (bool, bool, error),
) (NativeProcessRecord, bool, error) {
	if pid <= 0 || sessionID <= 0 {
		return NativeProcessRecord{}, false, errors.New("native session-member scope is invalid")
	}
	before, err := observe(pid)
	if errors.Is(err, errProcessIdentityMissing) {
		return NativeProcessRecord{}, false, nil
	}
	if err != nil {
		return NativeProcessRecord{}, false, err
	}
	if !validNativeSessionMember(pid, before) {
		return NativeProcessRecord{}, false, errors.New("native session-member observation is incomplete")
	}
	for check := 0; check < 2; check++ {
		session, err := sessionOf(pid)
		if errors.Is(err, unix.ESRCH) || err == nil && session != sessionID {
			return NativeProcessRecord{}, false, nil
		}
		if err != nil {
			return NativeProcessRecord{}, false, err
		}
		if check == 0 {
			defunct, present, err := defunctOf(pid)
			if err != nil {
				return NativeProcessRecord{}, false, err
			}
			if !present || defunct {
				return NativeProcessRecord{}, false, nil
			}
		}
	}
	after, err := observe(pid)
	if errors.Is(err, errProcessIdentityMissing) {
		return NativeProcessRecord{}, false, nil
	}
	if err != nil {
		return NativeProcessRecord{}, false, err
	}
	if !validNativeSessionMember(pid, after) {
		return NativeProcessRecord{}, false, errors.New("native session-member observation is incomplete")
	}
	if before.identityToken != after.identityToken {
		return NativeProcessRecord{}, false, nil
	}
	return NativeProcessRecord{PID: pid, ParentPID: after.parent, RSSBytes: after.rssBytes,
		StartIdentity: after.identityToken, ObservedName: after.name}, true, nil
}

func validNativeSessionMember(pid int, observed processSnapshot) bool {
	return observed.coherent && observed.parent >= 0 && observed.parent != pid && observed.rssBytes >= 0 &&
		len(observed.identityToken) > 0 && len(observed.identityToken) <= 64 &&
		len(observed.name) > 0 && len(observed.name) <= 16
}
