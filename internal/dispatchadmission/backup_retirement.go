package dispatchadmission

import "context"

// retireBackupEndpoint is reached only by the already reserved final Pause
// of a parent-bound epoch-four archive-capable server. No Resume is issued:
// the phase mask still ends at eleven, and the native engine alone stays live.
func (client *Client) retireBackupEndpoint(ctx context.Context) error {
	client.mu.Lock()
	lifetime := client.storeLifetime
	valid := client.backupEndpointCarry && !client.backupRetiring && client.phase == 11 && client.paused && client.ownersRequired && !client.ownerRequestsOpen &&
		lifetime != nil && lifetime.producerID == 5 && lifetime.semanticMode == ProductionSemanticV3 && !client.closed && client.err == nil
	if valid {
		owners := client.owners
		valid = owners != nil
		if owners != nil {
			owners.mu.Lock()
			valid = owners.paused && owners.pausedReady && owners.requestsFenced && owners.requestsReady && owners.active == 0 && owners.requests == 0 && owners.err == nil && owners.ctx.Err() == nil
			owners.mu.Unlock()
		}
	}
	if valid {
		client.backupRetiring = true
	}
	client.mu.Unlock()
	if !valid {
		return ErrProtocol
	}
	return client.Checkpoint(ctx)
}

func (client *Client) checkpointStoreForPhase(ctx context.Context) error {
	client.mu.Lock()
	retiring, lifetime := client.backupRetiring, client.storeLifetime
	client.mu.Unlock()
	if !retiring {
		return lifetime.checkpointStore(ctx)
	}
	// Close proves no in-flight SDK call or transaction, and irrevocably fences
	// later admission. It does not stop or independently attest the engine.
	if err := lifetime.closeStore(ctx); err != nil {
		return err
	}
	lifetime.storeMu.Lock()
	lifetime.storeRetired = true
	lifetime.storeMu.Unlock()
	return nil
}

// RetireBackupEndpoint records the parent's observed final Pause echo and
// already joined SA endpoint. The actual launcher owns that ordering. This
// permits only terminal DA Close after the adjacent global advance; it grants
// no work, phase mask, producer launch or permission to restore.
func (c *Controller) RetireBackupEndpoint() error {
	if c == nil {
		return ErrConfig
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkLocked(); err != nil {
		return err
	}
	p := c.producers[5]
	if !c.fenced || c.phases[c.phase].ID != 11 || p == nil || !p.attached || p.closed || p.hardDeath || p.backupRetired || p.checkpoint != 11 {
		return c.failLocked(ErrProtocol)
	}
	for _, active := range p.active {
		if !active.persistent || !active.carried {
			return c.failLocked(ErrBusy)
		}
	}
	p.backupRetired = true
	return nil
}
