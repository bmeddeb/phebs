//go:build linux

package t421

import (
	"context"
	"errors"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

// This private Linux category describes current owned kernel death, never an
// executable image at census time. Frozen V1-V5 class validators stay exact.
const linuxEpochOwnedDefunctClass = "owned-defunct"

// Only the fixed native finish calls this, before its first stop signal.
// DrainOwners/Pause do not terminate the server root. Cancellation and unexpected
// exit do not manufacture an expected-stop registration.
func (a *linuxEpochResourceAdmission) armOwnedStop(ctx context.Context, authority *linuxEpochSessionAuthority) error {
	if a == nil || a.held == nil || authority == nil || authority.held != a.held || ctx == nil {
		return ErrExecutionEpochOne
	}
	ctx, cancel := context.WithTimeout(ctx, epochProcessProbeTimeout)
	defer cancel()
	select {
	case a.probeGate <- struct{}{}:
		defer func() { <-a.probeGate }()
	case <-ctx.Done():
		return ErrExecutionEpochOne
	}
	if authority.checkBinding(ctx) != nil {
		return ErrExecutionEpochOne
	}
	if previous := a.ownedStops[authority.pid]; previous != nil {
		if previous == authority {
			return nil
		}
		return ErrExecutionEpochOne
	}
	if len(a.ownedStops) != 0 {
		return ErrExecutionEpochOne
	}
	row, err := linuxEpochProcessRow(authority.pid)
	poll := []unix.PollFd{{Fd: int32(authority.pidfd), Events: unix.POLLIN}}
	n, pollErr := unix.Poll(poll, 0)
	current, currentErr := authority.rootRow(false)
	lastN, lastPollErr := unix.Poll(poll, 0)
	if err != nil || pollErr != nil || n != 0 || lastPollErr != nil || lastN != 0 || poll[0].Revents != 0 || currentErr != nil || row.StartIdentity != authority.birth || row.ObservedName != authority.name || !linuxEpochSameProcessRow(row, current) || ctx.Err() != nil {
		return ErrExecutionEpochOne
	}
	if a.ownedStops == nil {
		a.ownedStops = make(map[int]*linuxEpochSessionAuthority, 1)
	}
	a.ownedStops[authority.pid] = authority
	return nil
}

// The caller holds probeGate through the complete native census/classification.
// A positive RSS observed before exit stays positive; only the category records
// that this same original, unreaped, source-stopped root is now proven Z.
func (a *linuxEpochResourceAdmission) ownedDefunctClass(ctx context.Context, gauge *ProcessObservationGauge, row t4013.NativeProcessRecord) (int, bool) {
	if a == nil || a.held == nil || gauge == nil || gauge.rootPID != a.held.controllerPID || row.PID == gauge.rootPID {
		return 0, false
	}
	authority := a.ownedStops[row.PID]
	if authority == nil || authority.held != a.held || row.ParentPID != a.held.controllerPID || row.StartIdentity != authority.birth || row.ObservedName != authority.name || authority.checkCurrentExit(ctx) != nil {
		return 0, false
	}
	for index, class := range gauge.observation.Classes {
		if class.Class == linuxEpochOwnedDefunctClass {
			return index, true
		}
	}
	return 0, false
}

// Only the actual sole Handle.Wait owner calls this after anchored session
// drain. The gate prevents reaping a row between census and classification.
// Handle.Wait also joins pumps and DA settlement: a hold longer than the
// unchanged two-second probe deadline can honestly stop qualification.
func (a *linuxEpochResourceAdmission) waitOwnedRoot(authority *linuxEpochSessionAuthority, handle dispatchadmission.Handle) error {
	var retainedErr error
	for {
		ctx, cancel := context.WithTimeout(context.Background(), epochProcessProbeTimeout)
		acquired := false
		if a != nil && a.held != nil && authority != nil && authority.held == a.held {
			select {
			case a.probeGate <- struct{}{}:
				acquired = true
			case <-ctx.Done():
			}
		}
		if acquired && (a.ownedStops[authority.pid] == nil || a.ownedStops[authority.pid] == authority) && authority.checkCurrentExit(ctx) == nil {
			cancel()
			err := errors.Join(retainedErr, handle.Wait()) // Sole genuine native Wait and admission settlement.
			if a.ownedStops[authority.pid] == authority {
				delete(a.ownedStops, authority.pid)
			}
			<-a.probeGate
			return err
		}
		if acquired {
			<-a.probeGate
		}
		cancel()
		retainedErr = ErrExecutionEpochOne
		// Unknown gate/anchor cannot authorize a reap or release a loan.
		if a != nil {
			a.Fail()
		}
		time.Sleep(time.Second)
	}
}
