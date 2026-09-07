package dispatchadmission

import "context"

// TerminalQuiesce is the selected phase-eight held-owner exception, not normal
// DrainOwners or an SDK checkpoint. Its bound main callback must retain the
// exact native claim, join its report tail, drain other owners and requests,
// and join its heartbeat before dispatch is paused and the PC echo is written.
func (control *PhaseControl) TerminalQuiesce(ctx context.Context) error {
	return control.exchange(ctx, phaseTerminalQuiesce)
}

// ProductionTerminalPhase returns optional authenticated configuration, never
// quiescence or health evidence. A failed selected lifetime cannot fall back.
func ProductionTerminalPhase() (uint32, error) {
	lifetime := productionRuntime.Load()
	if lifetime == nil {
		return 0, nil
	}
	client := lifetime.client
	if client == nil {
		return 0, ErrProductionBootstrap
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.closed || client.err != nil || client.ctx.Err() != nil {
		return 0, ErrProductionBootstrap
	}
	return client.controlTerminalPhase, nil
}

// BindProductionTerminalQuiescence installs exactly one main-owned callback
// after the real owner barrier is bound and before workers start. The callback
// owns native claim/hook/report-tail validation; callers supply no readiness
// boolean or replacement SDK/owner token. Ordinary modes register nothing.
func BindProductionTerminalQuiescence(quiesce func(context.Context) error) error {
	lifetime := productionRuntime.Load()
	if lifetime == nil || lifetime.program != ProgramPhebs || lifetime.semanticMode != ProductionSemanticV3 ||
		lifetime.producerID != 4 || lifetime.inputSHA256 == ([32]byte{}) || lifetime.storeClient == nil || lifetime.client == nil {
		return ErrProductionBootstrap
	}
	client := lifetime.client
	client.mu.Lock()
	valid := quiesce != nil && client.storeLifetime == lifetime && client.controlTerminalPhase == 8 && client.phase == 6 && client.ownersRequired && client.owners != nil &&
		client.terminalQuiesce == nil && client.terminalState == 0 && !client.closed && client.err == nil && client.ctx.Err() == nil
	if valid {
		client.terminalQuiesce = quiesce
	}
	client.mu.Unlock()
	if !valid {
		return client.fail(ErrProductionBootstrap)
	}
	return nil
}

func (client *Client) quiesceTerminal(ctx context.Context, phase uint32) error {
	client.mu.Lock()
	quiesce := client.terminalQuiesce
	valid := ctx != nil && ctx.Err() == nil && phase == 8 && client.controlTerminalPhase == phase && client.phase == phase && quiesce != nil &&
		client.ownersRequired && client.owners != nil && client.terminalState == 0 &&
		!client.closed && !client.paused && !client.fenced && !client.checkpoint && client.err == nil && client.ctx.Err() == nil
	if valid {
		client.terminalState = phaseTerminalQuiesce
		client.ownerRequestsOpen = false
	}
	client.mu.Unlock()
	if !valid {
		return ErrProtocol
	}
	// No client/owner mutex is held across the native tail and heartbeat join.
	// Pause must follow that join so existing owners can still finish children.
	if err := quiesce(ctx); err != nil {
		return err
	}
	return client.Pause(ctx)
}
