package dispatchadmission

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
)

// ProductionRequestHeader is private request admission, not API authentication.
// The normal auth/session stack still runs inside the complete request owner.
const ProductionRequestHeader = "X-Phebs-Execution-Request"

const phasePreparingRequests byte = 8 // Local state, never a wire operation.

// DrainOwners precedes Pause: admitted owners can still dispatch every child
// needed to finish their durable/report tails. A timeout does not join them.
func (control *PhaseControl) DrainOwners(ctx context.Context) error {
	return control.exchange(ctx, phaseOwnerDrain)
}

// OpenRequests opens only the bounded, parent-token-authorized observation
// window after owner drainage or next-phase Resume, while dispatch stays open.
// It never reopens ordinary owners or permits work after an accounting fence.
func (control *PhaseControl) OpenRequests(ctx context.Context) error {
	return control.exchange(ctx, phaseRequestsOpen)
}

func (control *PhaseControl) FenceRequests(ctx context.Context) error {
	return control.exchange(ctx, phaseRequestsFence)
}

// ReopenOwners follows Resume and any required next-phase preparation on the
// live application. Resume alone intentionally leaves owners and requests shut.
func (control *PhaseControl) ReopenOwners(ctx context.Context) error {
	return control.exchange(ctx, phaseOwnersReopen)
}

func nextConfiguredControlState(state byte, index int, op byte, config PhaseControlConfig) (byte, int, error) {
	if config.TerminalAuthor {
		if op == phasePause && state == 0 {
			return phasePause, index, nil
		}
		return 0, 0, ErrProtocol
	}
	if !config.OwnerControl {
		return nextControlState(state, index, op, config.Phases)
	}
	switch {
	case op == phaseOwnerDrain && state == 0:
		return phaseOwnerDrain, index, nil
	case op == phasePause && (state == phaseOwnerDrain || state == phaseResume):
		// Resume keeps the previously proven owner/request fences. A fenced
		// preparation window returns here too, so consecutive quiet phases
		// need not reopen ordinary work just to close dispatch again.
		return phasePause, index, nil
	case op == phaseCheckpoint && state == phasePause:
		return phaseCheckpoint, index, nil
	case op == phaseRequestsOpen && state == phaseOwnerDrain:
		return phaseRequestsOpen, index, nil
	case op == phaseRequestsOpen && state == phaseResume:
		return phasePreparingRequests, index, nil
	case op == phaseRequestsFence && state == phaseRequestsOpen:
		return phaseOwnerDrain, index, nil
	case op == phaseRequestsFence && state == phasePreparingRequests:
		return phaseResume, index, nil
	case op == phaseResume && state == phaseCheckpoint && index+1 < len(config.Phases):
		return phaseResume, index + 1, nil
	case op == phaseOwnersReopen && state == phaseResume:
		return 0, index, nil
	default:
		return 0, 0, ErrProtocol
	}
}

// RequestToken binds private producer/phase/window identity. No secret is sent
// to the public evidence stream. One fixed-size hash is computed per caller
// request, not a native image hash, and an old window token cannot reopen entry.
func (control *PhaseControl) RequestToken() string {
	if control == nil {
		return ""
	}
	control.mu.Lock()
	defer control.mu.Unlock()
	if !control.config.OwnerControl || control.closed || control.err != nil || control.ctx.Err() != nil ||
		(control.state != 0 && control.state != phaseRequestsOpen && control.state != phasePreparingRequests) {
		return ""
	}
	return ownerRequestToken(control.binding, control.config.Phases[control.index], control.requestSequence)
}

func ownerRequestToken(binding [32]byte, phase uint32, sequence uint64) string {
	var input [64]byte
	copy(input[:20], "t422-owner-request-1")
	copy(input[20:52], binding[:])
	binary.BigEndian.PutUint32(input[52:56], phase)
	binary.BigEndian.PutUint64(input[56:], sequence)
	digest := sha256.Sum256(input[:])
	return hex.EncodeToString(digest[:])
}

func (client *Client) ownerRequestAllowed(token string) bool {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.ownerRequestAllowedLocked(token)
}

func (client *Client) ownerRequestAllowedLocked(token string) bool {
	if !client.ownersRequired {
		return true
	}
	if !client.ownerRequestsOpen || client.closed || client.err != nil || client.ctx.Err() != nil || len(token) != 64 {
		return false
	}
	expected := ownerRequestToken(client.binding, client.phase, client.ownerRequestSequence)
	return subtle.ConstantTimeCompare([]byte(token), []byte(expected)) == 1
}

// EnterProductionRequest atomically binds token validation to the actual owner
// slot. Lock order is client then owners; control never holds these in reverse.
// Invalid tokens consume no slot and never reach the ordinary auth/session stack.
func EnterProductionRequest(ctx context.Context, owners *Owners, token string) (OwnerTurn, error) {
	runtime := productionRuntime.Load()
	if runtime == nil {
		return owners.EnterRequest(ctx)
	}
	return runtime.client.enterOwnerRequest(ctx, owners, token)
}

func (client *Client) enterOwnerRequest(ctx context.Context, owners *Owners, token string) (OwnerTurn, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.ownersRequired && (owners == nil || owners != client.owners) || !client.ownerRequestAllowedLocked(token) {
		return OwnerTurn{}, ErrFenced
	}
	return owners.EnterRequest(ctx)
}

// BindProductionOwners registers exactly the main-owned barrier, after main has
// entered its startup turn and before launching subordinate owners. A private
// endpoint cannot treat an unregistered/unfinished startup as zero active work.
func BindProductionOwners(owners *Owners) error {
	runtime := productionRuntime.Load()
	if runtime == nil && owners == nil {
		return nil
	}
	if runtime == nil || runtime.program != ProgramPhebs {
		return ErrProductionBootstrap
	}
	return runtime.client.bindOwners(owners)
}

func (client *Client) bindOwners(owners *Owners) error {
	client.mu.Lock()
	defer client.mu.Unlock()
	if !client.ownersRequired && owners == nil {
		return nil
	}
	if !client.ownersRequired || client.ownersReady == nil || owners == nil || client.owners != nil ||
		client.closed || client.err != nil || client.ctx.Err() != nil || owners.Context().Err() != nil {
		return ErrProductionBootstrap
	}
	client.owners = owners
	close(client.ownersReady)
	return nil
}

func (client *Client) awaitOwners(ctx context.Context) (*Owners, error) {
	client.mu.Lock()
	ready, required := client.ownersReady, client.ownersRequired
	client.mu.Unlock()
	if !required || ready == nil {
		return nil, ErrProtocol
	}
	select {
	case <-ready:
	case <-ctx.Done():
		return nil, ErrCanceled
	case <-client.ctx.Done():
		return nil, ErrCanceled
	}
	client.mu.Lock()
	owners := client.owners
	valid := owners != nil && !client.closed && client.err == nil && client.ctx.Err() == nil && ctx.Err() == nil
	client.mu.Unlock()
	if !valid {
		return nil, ErrCanceled
	}
	return owners, nil
}

func (client *Client) controlOwners(ctx context.Context, frame phaseControlFrame) error {
	owners, err := client.awaitOwners(ctx)
	if err != nil {
		return err
	}
	switch frame.op {
	case phaseOwnerDrain, phaseRequestsFence:
		client.mu.Lock()
		client.ownerRequestsOpen = false
		client.mu.Unlock()
		if err := owners.FenceRequests(ctx); err != nil {
			return err
		}
		if frame.op == phaseOwnerDrain {
			return owners.Pause(ctx)
		}
		return nil
	case phaseOwnersReopen, phaseRequestsOpen:
		if frame.op == phaseOwnersReopen {
			if err := owners.Resume(); err != nil {
				return err
			}
		}
		if err := owners.OpenRequests(); err != nil {
			return err
		}
		client.mu.Lock()
		client.ownerRequestSequence = frame.sequence
		client.ownerRequestsOpen = true
		client.mu.Unlock()
		return nil
	default:
		return ErrProtocol
	}
}
