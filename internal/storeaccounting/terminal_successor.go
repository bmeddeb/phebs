package storeaccounting

// ReopenAfterTerminalEOF reopens the current phase once for a configured,
// unopened successor after the predecessor's real terminal receiver has joined.
// It preserves all phase counters, ordinals and closure flags. The owning
// launcher must separately prove the exact owned kill, native Wait and empty
// session before calling, and must latch any partially failed DA/SA cutover.
func (t *Transport) ReopenAfterTerminalEOF(predecessor, successor, phase uint32) error {
	if t == nil {
		return ErrConfig
	}
	t.mu.Lock()
	err := t.err
	prior, next := t.peerLocked(predecessor), t.peerLocked(successor)
	if err == nil && (t.closing || t.ctx.Err() != nil) {
		err = ErrCanceled
	}
	if err == nil && (predecessor == successor || t.terminalSuccessor != 0 || prior == nil || next == nil ||
		!prior.opened || !prior.eof || prior.err != nil || next.opened || !phaseIn(next.config.Phases, phase)) {
		err = ErrProtocol
	}
	if err == nil {
		select {
		case <-prior.done:
		default:
			err = ErrIncomplete
		}
	}
	if err == nil {
		err = t.controller.reopenAfterTerminalEOF(predecessor, successor, phase)
	}
	if err == nil {
		t.terminalSuccessor = successor
	}
	t.mu.Unlock()
	if err != nil {
		return t.failure(err)
	}
	return nil
}

func (c *Controller) reopenAfterTerminalEOF(predecessor, successor, phase uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkLocked(); err != nil {
		return err
	}
	prior, next := c.producerLocked(predecessor), c.producerLocked(successor)
	if !c.fenced || phase != c.phases[c.phase].ID || prior == nil || next == nil ||
		!prior.terminalEOF || prior.terminalPhase != phase || prior.checkpoint != phase ||
		next.attached || next.closed || next.ordinal != 0 || next.checkpoint != 0 || next.terminalPhase != 0 {
		return c.failLocked(ErrProtocol)
	}
	for i := 0; i < c.producerCount; i++ {
		p := &c.producers[i]
		if p.busy() || p.attached && !p.closed && !p.terminalEOF {
			return c.failLocked(ErrIncomplete)
		}
	}
	c.fenced = false
	return nil
}
