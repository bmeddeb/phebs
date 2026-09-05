package dispatchadmission

import "sort"

// ProducerLaunch is a copied view of one configured, never-attached producer
// at the controller's current phase. It is not tool or input admission.
type ProducerLaunch struct {
	Producer Producer
	Limits   Limits
	Phase    uint32
}

func (c *Controller) ProducerLaunch(producer uint32) (ProducerLaunch, error) {
	if c == nil {
		return ProducerLaunch{}, ErrConfig
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkLocked(); err != nil {
		return ProducerLaunch{}, err
	}
	p := c.producers[producer]
	if c.fenced || p == nil || p.attached || p.closed || p.ordinal != 0 || p.sequence != 0 || p.checkpoint != 0 || p.pid != 0 || p.eof || p.hardDeath || len(p.active) != 0 {
		return ProducerLaunch{}, ErrProtocol
	}
	view := ProducerLaunch{Producer: Producer{ID: producer, Binding: p.binding}, Limits: c.limits, Phase: c.phases[c.phase].ID}
	for _, site := range p.sites {
		view.Producer.Sites = append(view.Producer.Sites, site)
	}
	sort.Slice(view.Producer.Sites, func(i, j int) bool { return view.Producer.Sites[i].ID < view.Producer.Sites[j].ID })
	return view, nil
}

// ProducerCount copies only one actual scalar row. It neither snapshots all
// phases nor hashes, allocates or reports successful process outcomes.
func (c *Controller) ProducerCount(producer uint32) (ProducerCount, error) {
	if c == nil {
		return ProducerCount{}, ErrConfig
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	p := c.producers[producer]
	if p == nil {
		return ProducerCount{}, ErrConfig
	}
	return ProducerCount{Producer: producer, Ordinal: p.ordinal, Active: len(p.active),
		Checkpoint: p.checkpoint, Attached: p.attached, Closed: p.closed}, c.checkLocked()
}
