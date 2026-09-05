package dispatchadmission

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"
)

// LocalProducer owns the real in-process producer's DA01 pair and receiver,
// borrowing the exact controller. It is mechanical transport composition, not
// an immutable tool/input or full profile issuer. Callers perform their actual
// protected custody checks immediately before Start. Never copy this owner.
type LocalProducer struct {
	controller *Controller
	client     *Client
	producer   uint32
	done       <-chan error
	cancel     context.CancelFunc
	closeOnce  sync.Once
	closeErr   error
}

// NewLocalProducer derives all identity from this controller. Socket adoption
// and the one fresh, same-phase attachment complete synchronously before any
// caller can Start. No caller Client, binding, callback or verified bit is used.
func (c *Controller) NewLocalProducer(ctx context.Context, producer uint32) (*LocalProducer, error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, ErrCanceled
	}
	view, err := c.ProducerLaunch(producer)
	if err != nil {
		return nil, err
	}
	parent, child, err := NewPipe()
	if err != nil {
		return nil, err
	}
	conn, err := adopt(parent)
	if err != nil {
		_ = child.Close()
		return nil, err
	}
	lifetime, cancel := context.WithCancel(ctx)
	client, err := NewClient(lifetime, child, view.Producer, view.Phase, view.Limits)
	if err != nil {
		cancel()
		_ = conn.Close()
		return nil, err
	}
	c.mu.Lock()
	err = c.checkLocked()
	if err == nil && ctx.Err() != nil {
		err = c.failLocked(ErrCanceled)
	}
	p := c.producers[producer]
	if err == nil && (c.fenced || p == nil || p.attached || p.closed || p.ordinal != 0 || p.sequence != 0 || p.checkpoint != 0 || p.pid != 0 || p.eof || p.hardDeath || len(p.active) != 0 || c.phases[c.phase].ID != view.Phase) {
		err = c.failLocked(ErrProtocol)
	}
	if err == nil {
		p.attached, p.pid = true, os.Getpid()
	}
	c.mu.Unlock()
	if err != nil {
		cancel()
		_ = conn.Close()
		client.stopContext()
		_ = client.conn.Close()
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- c.serveAttached(lifetime, producer, conn, nil); close(done) }()
	return &LocalProducer{controller: c, client: client, producer: producer, done: done, cancel: cancel}, nil
}

// OnController checks the private constructor-established identity, not a
// caller-supplied producer binding or claimed transport relationship.
func (p *LocalProducer) OnController(c *Controller) bool {
	return p != nil && c != nil && p.controller == c && p.client != nil
}

func (p *LocalProducer) Start(ctx context.Context, site uint32, command *exec.Cmd) (Handle, error) {
	if p == nil || p.client == nil {
		return Handle{}, ErrConfig // No ordinary nil-client pass-through.
	}
	return p.client.Start(ctx, site, command)
}

// StartInPhase checks the selected source-derived phase under the admission
// gate before assigning its ordinal. A concurrent Resume cannot move this
// direct launch into a different phase between an external check and admission.
func (p *LocalProducer) StartInPhase(ctx context.Context, phase uint32, site Site, command *exec.Cmd) (Handle, error) {
	if p == nil || p.client == nil || phase == 0 {
		return Handle{}, ErrConfig
	}
	// This source-site map is immutable after NewClient; hold the existing
	// mutex for the copied comparison, never through transport or native work.
	p.client.mu.Lock()
	configured, exists := p.client.sites[site.ID]
	p.client.mu.Unlock()
	if !exists || configured != site {
		return Handle{}, ErrConfig
	}
	return p.client.start(ctx, site.ID, command, phase)
}

func (p *LocalProducer) Count() (ProducerCount, error) {
	if p == nil {
		return ProducerCount{}, ErrConfig
	}
	return p.controller.ProducerCount(p.producer)
}

func (p *LocalProducer) Pause(ctx context.Context) error {
	if p == nil || p.client == nil {
		return ErrConfig
	}
	return p.client.Pause(ctx)
}
func (p *LocalProducer) Checkpoint(ctx context.Context) error {
	if p == nil || p.client == nil {
		return ErrConfig
	}
	return p.client.Checkpoint(ctx)
}
func (p *LocalProducer) Resume(phase uint32) error {
	if p == nil || p.client == nil {
		return ErrConfig
	}
	return p.client.Resume(phase)
}

// Close drains the actual Client then joins its owned receiver. It does not
// fence or close other producers. A failed close cancels the owned transport;
// existing handles still require their sole Wait before input custody release.
func (p *LocalProducer) Close(ctx context.Context) error {
	if p == nil {
		return nil
	}
	p.closeOnce.Do(func() {
		closeCtx := ctx
		if ctx != nil {
			var cancel context.CancelFunc
			closeCtx, cancel = context.WithTimeout(ctx, p.client.limits.AckTimeout)
			defer cancel()
		}
		p.closeErr = p.client.Close(closeCtx)
		p.cancel()
		p.closeErr = errors.Join(p.closeErr, <-p.done)
	})
	return p.closeErr
}
