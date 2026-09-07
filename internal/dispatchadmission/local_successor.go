package dispatchadmission

import "context"

// ReopenAfterHardDeath reopens this genuine local launcher's current phase once
// after a checkpointed predecessor was retired by actual EOF and native Wait.
// It cannot reopen a remote producer or reset phase/attempt/ordinal accounting.
// The outer launcher must also prove session emptiness and the matching SA
// cutover, and latch either failure before launching the configured successor.
func (p *LocalProducer) ReopenAfterHardDeath(ctx context.Context, predecessor, successor, phase uint32) (retErr error) {
	if p == nil || p.client == nil || p.controller == nil {
		return ErrConfig
	}
	client, controller := p.client, p.controller
	defer func() {
		if retErr != nil {
			retErr = controller.fail(client.fail(retErr))
		}
	}()
	if ctx == nil || ctx.Err() != nil {
		return ErrCanceled
	}
	release, err := client.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	client.mu.Lock()
	defer client.mu.Unlock()
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if err := controller.checkLocked(); err != nil {
		return err
	}
	if ctx.Err() != nil || client.ctx.Err() != nil || client.err != nil {
		return ErrCanceled
	}
	local, prior, next := controller.producers[p.producer], controller.producers[predecessor], controller.producers[successor]
	if !controller.fenced || controller.phases[controller.phase].ID != phase || phase == 0 ||
		predecessor == successor || predecessor == p.producer || successor == p.producer || p.reopenedDeath != 0 ||
		!client.paused || !client.fenced || !client.checkpoint || client.closed || client.phase != phase ||
		client.storeLifetime != nil || client.ownersRequired || len(client.active) != 0 ||
		local == nil || !local.attached || local.closed || local.hardDeath || local.checkpoint != phase ||
		prior == nil || !prior.attached || !prior.hardDeath || !prior.eof || !prior.closed || prior.checkpoint != phase ||
		next == nil || next.attached || next.closed || next.pid != 0 || next.ordinal != 0 || next.sequence != 0 || next.checkpoint != 0 {
		return ErrProtocol
	}
	for id, producer := range controller.producers {
		if len(producer.active) != 0 || producer.attached && !producer.closed && id != p.producer {
			return ErrIncomplete
		}
	}
	// No I/O occurs under these locks. The owned client's existing admission
	// gate prevents a concurrent Start from observing only half of this cutover.
	local.checkpoint = 0
	controller.fenced = false
	client.paused, client.fenced, client.checkpoint = false, false, false
	client.controlCheckpointAcknowledged = false
	p.reopenedDeath = predecessor
	client.notifyLocked()
	return nil
}
