package dispatchadmission

import (
	"context"
	"errors"
	"io"
	"math"
	"net"
	"os"
	"os/exec"
	"sync"
	"time"
)

type commandState struct {
	pending    bool
	persistent bool
	carried    bool
}

// Client is an explicitly adopted producer endpoint, never ambient authority.
// Do not copy it. Constructors/owners must already have verified tool/input
// identity and bound this producer's sites to the selected compiled source.
type Client struct {
	mu                            sync.Mutex
	conn                          *net.UnixConn
	ctx                           context.Context
	cancel                        context.CancelCauseFunc
	stopContext                   func() bool
	gate                          chan struct{}
	changed                       chan struct{}
	binding                       [32]byte
	sites                         map[uint32]Site
	limits                        Limits
	phase                         uint32
	ordinal                       uint64
	sequence                      uint64
	wireBytes                     uint64
	active                        map[uint64]*commandState
	paused                        bool
	waiters                       int
	controlAttached               bool
	controlCheckpointAcknowledged bool
	ownersRequired                bool
	ownersReady                   chan struct{}
	owners                        *Owners
	ownerRequestsOpen             bool
	ownerRequestSequence          uint64
	fenced                        bool
	checkpoint                    bool
	closed                        bool
	err                           error
}

// NewClient adopts and closes file even on failure. The file is explicitly
// supplied by the caller; this function performs no bootstrap or env lookup.
func NewClient(ctx context.Context, file *os.File, producer Producer, phase uint32, limits Limits) (*Client, error) {
	conn, err := adopt(file)
	if err != nil {
		return nil, err
	}
	if ctx == nil || ctx.Err() != nil || producer.ID == 0 || producer.Binding == ([32]byte{}) || phase == 0 ||
		limits.ActivePerProducer < 1 || limits.AckTimeout <= 0 || limits.WireBytes < 2*FrameBytes ||
		len(producer.Sites) == 0 || len(producer.Sites) > limits.Sites {
		_ = conn.Close()
		return nil, ErrConfig
	}
	c := &Client{conn: conn, binding: producer.Binding, phase: phase, limits: limits, sites: make(map[uint32]Site), active: make(map[uint64]*commandState), gate: make(chan struct{}, 1), changed: make(chan struct{})}
	for _, site := range producer.Sites {
		if site.ID == 0 || site.Role == 0 || c.sites[site.ID].ID != 0 {
			_ = conn.Close()
			return nil, ErrConfig
		}
		c.sites[site.ID] = site
	}
	c.ctx, c.cancel = context.WithCancelCause(ctx)
	c.stopContext = context.AfterFunc(c.ctx, func() { _ = conn.Close() })
	return c, nil
}

func (c *Client) Context() context.Context { return c.ctx }

func (c *Client) notifyLocked() {
	close(c.changed)
	c.changed = make(chan struct{})
}

func (c *Client) fail(err error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err == nil {
		c.err = err
		c.cancel(err)
		c.notifyLocked()
	}
	return c.err
}

func (c *Client) acquire(ctx context.Context) (func(), error) {
	if ctx == nil {
		return nil, c.fail(ErrCanceled)
	}
	waitCtx, cancel := context.WithTimeout(ctx, c.limits.AckTimeout)
	select {
	case c.gate <- struct{}{}:
		cancel()
		return func() { <-c.gate }, nil
	case <-waitCtx.Done():
		cancel()
		return nil, c.fail(ErrCanceled)
	case <-c.ctx.Done():
		cancel()
		return nil, c.fail(ErrCanceled)
	}
}

// exchange requires the request gate, but never a state mutex, across I/O.
// Every transport/cancellation error is sticky; an uncertain ACK cannot retry.
func (c *Client) exchange(ctx context.Context, op byte, site uint32, ordinal uint64) error {
	c.mu.Lock()
	err := c.err
	if err == nil && (ctx == nil || ctx.Err() != nil || c.ctx.Err() != nil || c.closed) {
		err = ErrCanceled
	}
	if err == nil && (c.sequence == math.MaxUint64 || c.limits.WireBytes-c.wireBytes < 2*FrameBytes) {
		err = ErrLimit
	}
	if err != nil {
		c.mu.Unlock()
		return c.fail(err)
	}
	c.sequence++
	c.wireBytes += 2 * FrameBytes
	f := frame{op: op, phase: c.phase, site: site, ordinal: ordinal, sequence: c.sequence, binding: c.binding}
	c.mu.Unlock()
	deadline := time.Now().Add(c.limits.AckTimeout)
	if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
		deadline = end
	}
	if err := c.conn.SetDeadline(deadline); err != nil {
		return c.fail(ErrTransport)
	}
	stop := context.AfterFunc(ctx, func() { _ = c.conn.Close() })
	defer stop()
	raw := f.encode()
	if n, err := c.conn.Write(raw[:]); err != nil || n != len(raw) {
		return c.fail(ErrTransport)
	}
	var ack [FrameBytes]byte
	if _, err := io.ReadFull(c.conn, ack[:]); err != nil {
		return c.fail(ErrTransport)
	}
	if ack != raw {
		return c.fail(ErrProtocol)
	}
	if ctx.Err() != nil || c.ctx.Err() != nil {
		return c.fail(ErrCanceled)
	}
	return nil
}

// Handle owns exactly the command started by Start. Call Wait exactly once and
// do not copy a started handle. Wait always joins the command before reporting
// settlement, so even a lost settlement ACK cannot hide an unjoined command.
// Command deadlines, cancellation and WaitDelay remain the caller's existing
// responsibility; the admission timeout does not limit child execution.
type Handle struct {
	command *exec.Cmd
	client  *Client
	ordinal uint64
}

// Start admits once after the caller's own pre-admission setup, immediately
// before Cmd.Start. A nil client is the ordinary pass-through path and creates
// no admission state. Failed starts remain counted and are settled separately.
func (c *Client) Start(ctx context.Context, site uint32, command *exec.Cmd) (Handle, error) {
	if command == nil {
		return Handle{}, ErrConfig
	}
	if c == nil {
		return Handle{command: command}, command.Start()
	}
	ordinal, err := c.admit(ctx, site, command)
	if err != nil {
		return Handle{}, err
	}
	// The active pending token exists through this entire ACK-to-Start gap.
	if ctx.Err() != nil || c.ctx.Err() != nil {
		return Handle{}, c.fail(ErrCanceled)
	}
	err = command.Start()
	c.mu.Lock()
	c.active[ordinal].pending = false
	c.notifyLocked()
	c.mu.Unlock()
	if err != nil {
		settleErr := c.settle(ordinal)
		return Handle{}, errors.Join(err, settleErr)
	}
	return Handle{command: command, client: c, ordinal: ordinal}, nil
}

// admit is private so callers cannot bypass the owned Start/Wait boundary.
func (c *Client) admit(ctx context.Context, site uint32, command *exec.Cmd) (uint64, error) {
	var release func()
	var err error
	for {
		if err := c.waitUnpaused(ctx); err != nil {
			return 0, err
		}
		release, err = c.acquire(ctx)
		if err != nil {
			return 0, err
		}
		c.mu.Lock()
		if !c.paused || c.err != nil || c.closed {
			break
		}
		// Pause may have won after waitUnpaused but before gate acquisition.
		// Release the RPC gate before parking, so settlement can still drain.
		c.mu.Unlock()
		release()
	}
	bound, known := c.sites[site]
	if c.err != nil {
		err = c.err
	} else if !known || command.Process != nil {
		err = ErrProtocol
	} else if c.fenced || c.closed {
		err = ErrFenced
	} else if len(c.active) >= c.limits.ActivePerProducer || c.ordinal == math.MaxUint64 {
		err = ErrLimit
	}
	if err != nil {
		c.mu.Unlock()
		release()
		return 0, c.fail(err)
	}
	c.ordinal++
	ordinal := c.ordinal
	c.active[ordinal] = &commandState{pending: true, persistent: bound.Persistent}
	c.notifyLocked()
	c.mu.Unlock()
	err = c.exchange(ctx, opAdmit, site, ordinal)
	release()
	if err != nil {
		return 0, err
	}
	return ordinal, nil
}

// waitUnpaused reserves a bounded waiter, not a dispatch token. The reservation
// survives change notifications until this caller resumes or cancels. A canceled
// unadmitted caller changes no accounting state and is not a producer failure.
func (c *Client) waitUnpaused(ctx context.Context) error {
	if ctx == nil {
		return c.fail(ErrCanceled)
	}
	c.mu.Lock()
	if !c.paused || c.err != nil || c.closed {
		c.mu.Unlock()
		return nil
	}
	if err := ctx.Err(); err != nil {
		c.mu.Unlock()
		return err
	}
	if c.waiters >= c.limits.ActivePerProducer {
		c.mu.Unlock()
		return c.fail(ErrLimit)
	}
	c.waiters++
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.waiters--
		c.mu.Unlock()
	}()
	for {
		c.mu.Lock()
		err, paused, closed, changed := c.err, c.paused, c.closed, c.changed
		c.mu.Unlock()
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if closed {
			return context.Canceled
		}
		if !paused {
			return nil
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		case <-c.ctx.Done():
			c.mu.Lock()
			err := c.err
			c.mu.Unlock()
			if err != nil {
				return err
			}
			// Parent cancellation causes may contain private diagnostics.
			return c.ctx.Err()
		}
	}
}

// Pause reversibly parks new dispatch callers before admission and returns only
// after the existing request gate clears. Already admitted commands, including
// the ACK-to-Start gap, remain owned by their handles and the later Checkpoint.
// Parked callers are separately capped at ActivePerProducer and hold neither an
// RPC gate nor a state mutex. Overflow is a sticky limit refusal.
//
// The owner must quiesce its work sources before requesting a phase checkpoint.
// Pausing dispatch is not worker/store/authority readiness: an admitted owner
// that needs another child can otherwise stall behind Pause until the owner's
// checkpoint deadline refuses. No positive admission prefix is erased.
func (c *Client) Pause(ctx context.Context) error {
	if c == nil {
		return nil
	}
	if ctx == nil {
		return c.fail(ErrCanceled)
	}
	c.mu.Lock()
	err := c.err
	if err == nil && (c.paused || c.fenced || c.closed || c.checkpoint) {
		err = ErrProtocol
	}
	if err == nil {
		c.paused = true
		c.notifyLocked()
	}
	c.mu.Unlock()
	if err != nil {
		return c.fail(err)
	}
	release, err := c.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	if ctx.Err() != nil || c.ctx.Err() != nil {
		return c.fail(ErrCanceled)
	}
	return nil
}

func (c *Client) settle(ordinal uint64) error {
	// Cleanup receives its own bounded ACK wait, independent of a command's
	// expired context. It still respects the producer's sticky lifetime failure.
	ctx := context.Background()
	release, err := c.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	err = c.exchange(ctx, opSettle, 0, ordinal)
	c.mu.Lock()
	delete(c.active, ordinal)
	c.notifyLocked()
	c.mu.Unlock()
	return err
}

func (h *Handle) Wait() error {
	if h.command == nil {
		return ErrConfig
	}
	err := h.command.Wait()
	if h.client != nil {
		err = errors.Join(err, h.client.settle(h.ordinal))
	}
	return err
}

func (c *Client) Run(ctx context.Context, site uint32, command *exec.Cmd) error {
	if c == nil {
		if command == nil {
			return ErrConfig
		}
		return command.Run()
	}
	handle, err := c.Start(ctx, site, command)
	if err != nil {
		return err
	}
	return handle.Wait()
}

func (c *Client) quiescentLocked(closeAll bool) bool {
	for _, active := range c.active {
		if closeAll || active.pending || !active.persistent {
			return false
		}
	}
	return true
}

// Checkpoint fences this producer and waits for all one-shots (including
// admitted-but-not-started tokens) to settle. Only already-started explicitly
// persistent handles can carry. Controller.Fence must precede the RPC.
func (c *Client) Checkpoint(ctx context.Context) error { return c.checkpointOrClose(ctx, false) }

// Close requires every handle, including persistent handles, to be joined.
// It sends a terminal checkpoint and closes the endpoint. It cannot replace
// intentional hard-death closure, native session census, or custody checks.
func (c *Client) Close(ctx context.Context) error {
	if c == nil {
		return nil
	}
	return c.checkpointOrClose(ctx, true)
}

func (c *Client) checkpointOrClose(ctx context.Context, closeAll bool) error {
	if ctx == nil {
		return c.fail(ErrCanceled)
	}
	c.mu.Lock()
	c.fenced = true
	c.mu.Unlock()
	for {
		c.mu.Lock()
		err := c.err
		if err == nil && (c.closed || (!closeAll && c.checkpoint)) {
			err = ErrProtocol
		}
		ready := c.quiescentLocked(closeAll)
		changed := c.changed
		c.mu.Unlock()
		if err != nil {
			return c.fail(err)
		}
		if ready {
			break
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return c.fail(ErrCanceled)
		case <-c.ctx.Done():
			return c.fail(ErrCanceled)
		}
	}
	release, err := c.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	c.mu.Lock()
	ordinal := c.ordinal
	carry := make([]uint64, 0, len(c.active))
	for token, active := range c.active {
		if !active.carried {
			carry = append(carry, token)
		}
	}
	c.mu.Unlock()
	for _, token := range carry {
		if err := c.exchange(ctx, opCarry, 0, token); err != nil {
			return err
		}
		c.mu.Lock()
		c.active[token].carried = true
		c.mu.Unlock()
	}
	op := opCheckpoint
	if closeAll {
		op = opClose
	}
	if err := c.exchange(ctx, op, 0, ordinal); err != nil {
		return err
	}
	c.mu.Lock()
	c.checkpoint = true
	c.closed = closeAll
	c.notifyLocked()
	c.mu.Unlock()
	if closeAll {
		c.stopContext()
		_ = c.conn.Close()
		c.cancel(nil)
	}
	return nil
}

// Resume changes only this producer's local phase after the controller's
// successful Advance. A wrong/unknown phase is refused by the next exact RPC.
func (c *Client) Resume(phase uint32) error {
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return err
	}
	if !c.checkpoint || c.closed || phase == 0 || phase == c.phase {
		c.mu.Unlock()
		return c.fail(ErrProtocol)
	}
	c.phase = phase
	c.controlCheckpointAcknowledged = false
	c.paused = false
	c.fenced = false
	c.checkpoint = false
	c.notifyLocked()
	c.mu.Unlock()
	return nil
}
