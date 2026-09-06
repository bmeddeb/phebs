package storeaccounting

import (
	"context"
	"io"
	"math"
	"net"
	"os"
	"sync"
	"time"
)

type clientCall struct {
	used        bool
	kind        Kind
	transaction int
	submission  Submission
}

type clientTransaction struct {
	used, busy  bool
	token, rows uint64
}

// Client owns one explicit socket and fixed live slots, not native UUIDs. The
// future SDK adapter must retain its independent ALL-call/typed-decode lifetime
// through actual completion. This gate never spans SDK I/O. Do not copy.
type Client struct {
	mu                       sync.Mutex
	conn                     *net.UnixConn
	config                   ClientConfig
	ctx                      context.Context
	cancel                   context.CancelFunc
	joinContext              func()
	closeOnce                sync.Once
	gate                     chan struct{}
	entrants                 int
	phase                    uint32
	sequence, ordinal, bytes uint64
	calls                    [MaximumCalls]clientCall
	transactions             [MaximumTransactions]clientTransaction
	fenced, closed           bool
	sdkOwned                 bool
	err                      error
}

// NewClient consumes file on every path and returns only after actual Attach
// ACK. Config is mechanical explicit input, never an executable/plan issuer.
func NewClient(ctx context.Context, file *os.File, config ClientConfig) (*Client, error) {
	conn, err := adopt(file)
	if err != nil {
		return nil, err
	}
	if ctx == nil || ctx.Err() != nil || !wireProducerID(config.Producer) || config.Binding == ([32]byte{}) ||
		!phaseIn(config.Phases, config.Phase) || config.Phases>>MaximumPhases != 0 ||
		config.Calls < 1 || config.Calls > MaximumCalls || config.Transactions < 1 || config.Transactions > MaximumTransactions ||
		config.AckTimeout <= 0 || config.WireBytes < 4*pairBytes {
		_ = conn.Close()
		return nil, ErrConfig
	}
	c := &Client{conn: conn, config: config, phase: config.Phase, gate: make(chan struct{}, 1)}
	c.ctx, c.cancel = context.WithCancel(ctx)
	c.joinContext = closeOnCancel(c.ctx, conn)
	if _, err := c.exchange(ctx, opAttach, 0, 0, 0); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Client) Context() context.Context { return c.ctx }

// Capacities returns the immutable mechanical limits, not source admission.
func (c *Client) Capacities() (calls, transactions int) {
	if c == nil {
		return 0, 0
	}
	return c.config.Calls, c.config.Transactions
}

// ClaimSDKOwner reserves the one ALL-call owner for this client lifetime. It
// is never released, including after failure, and grants no source authority.
func (c *Client) ClaimSDKOwner() error {
	if c == nil {
		return ErrConfig
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ctx == nil || c.conn == nil {
		return ErrConfig
	}
	if c.err != nil {
		return c.err
	}
	if c.closed || c.ctx.Err() != nil {
		return ErrCanceled
	}
	if c.sdkOwned {
		return ErrConfig
	}
	c.sdkOwned = true
	return nil
}

func (c *Client) closeOwned() {
	c.closeOnce.Do(func() { c.cancel(); _ = c.conn.Close(); c.joinContext() })
}

func (c *Client) failure(reason error) error {
	c.mu.Lock()
	if c.err == nil {
		c.err = failureReason(failureKind(reason))
	}
	err := c.err
	c.mu.Unlock()
	c.closeOwned()
	return err
}

// Bound entrants before any gate wait: <=40 operations plus one control slot.
// Acquiring this gate admits no native work and reserves no wire bytes.
func (c *Client) acquire(ctx context.Context) (func(), error) {
	if c == nil {
		return nil, ErrConfig
	}
	if ctx == nil || ctx.Err() != nil {
		return nil, c.failure(ErrCanceled)
	}
	c.mu.Lock()
	if c.err != nil || c.closed || c.ctx.Err() != nil {
		err := c.err
		c.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return nil, c.failure(ErrCanceled)
	}
	if c.entrants >= c.config.Calls+1 {
		c.mu.Unlock()
		return nil, c.failure(ErrLimit)
	}
	c.entrants++
	c.mu.Unlock()
	wait, cancel := context.WithTimeout(ctx, c.config.AckTimeout)
	select {
	case c.gate <- struct{}{}:
		cancel()
		return func() { <-c.gate; c.mu.Lock(); c.entrants--; c.mu.Unlock() }, nil
	case <-wait.Done():
	case <-c.ctx.Done():
	}
	cancel()
	c.mu.Lock()
	c.entrants--
	c.mu.Unlock()
	return nil, c.failure(ErrCanceled)
}

// exchange requires the gate except during unpublished construction. The
// reserved reply remains charged on short I/O, cancellation, or a lost ACK.
func (c *Client) exchange(ctx context.Context, op byte, kind Kind, rows, token uint64) (wireFrame, error) {
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return wireFrame{}, err
	}
	if ctx == nil || ctx.Err() != nil || c.ctx.Err() != nil || c.closed {
		c.mu.Unlock()
		return wireFrame{}, c.failure(ErrCanceled)
	}
	if c.sequence == math.MaxUint64 || c.config.WireBytes-c.bytes < pairBytes {
		c.mu.Unlock()
		return wireFrame{}, c.failure(ErrLimit)
	}
	c.sequence++
	c.bytes += pairBytes
	request := wireFrame{op: op, kind: byte(kind), rows: uint16(rows), phase: c.phase,
		sequence: c.sequence, token: token, binding: c.config.Binding}
	c.mu.Unlock()
	deadline := time.Now().Add(c.config.AckTimeout)
	if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
		deadline = end
	}
	if err := c.conn.SetDeadline(deadline); err != nil {
		return wireFrame{}, c.failure(ErrTransport)
	}
	join := closeOnCancel(ctx, c.conn)
	defer join()
	if ctx.Err() != nil || c.ctx.Err() != nil {
		return wireFrame{}, c.failure(ErrCanceled)
	}
	raw := request.encode()
	if n, err := c.conn.Write(raw[:]); err != nil || n != len(raw) {
		return wireFrame{}, c.failure(ErrTransport)
	}
	if op == opFail {
		return wireFrame{}, nil
	}
	var ack [FrameBytes]byte
	if _, err := io.ReadFull(c.conn, ack[:]); err != nil {
		return wireFrame{}, c.failure(ErrTransport)
	}
	response, err := decodeFrame(ack, true)
	if err != nil {
		return wireFrame{}, c.failure(err)
	}
	want := request
	want.op |= replyBit
	if op == opSubmit {
		want.token = response.token
	}
	if response != want || (op == opSubmit && response.token == 0) {
		return wireFrame{}, c.failure(ErrProtocol)
	}
	if ctx.Err() != nil || c.ctx.Err() != nil {
		return wireFrame{}, c.failure(ErrCanceled)
	}
	return response, nil
}

func (c *Client) Submit(ctx context.Context, kind Kind, transaction, rows uint64) (Submission, error) {
	release, err := c.acquire(ctx)
	if err != nil {
		return Submission{}, err
	}
	defer release()
	c.mu.Lock()
	if c.fenced && kind != Commit && kind != Cancel {
		c.mu.Unlock()
		return Submission{}, ErrFenced
	}
	if kind < ImplicitWrite || kind > Cancel || rows > MaximumRows ||
		((kind == Begin || kind == Commit || kind == Cancel) && rows != 0) || (kind == Append && rows == 0) ||
		((kind == ImplicitWrite || kind == Begin) && transaction != 0) || (kind >= Append && transaction == 0) {
		c.mu.Unlock()
		return Submission{}, c.failure(ErrDescriptor)
	}
	callIndex, txIndex := -1, -1
	for i := 0; i < c.config.Calls; i++ {
		if !c.calls[i].used {
			callIndex = i
			break
		}
	}
	for i := 0; i < c.config.Transactions; i++ {
		tx := c.transactions[i]
		if (kind == Begin && !tx.used) || (transaction != 0 && tx.used && tx.token == transaction) {
			txIndex = i
			break
		}
	}
	if kind >= Append && (txIndex < 0 || c.transactions[txIndex].busy) {
		c.mu.Unlock()
		return Submission{}, c.failure(ErrProtocol)
	}
	if callIndex < 0 || (kind == Begin && txIndex < 0) {
		c.mu.Unlock()
		return Submission{}, c.failure(ErrLimit)
	}
	if kind == Append && rows > MaximumRows-c.transactions[txIndex].rows {
		c.mu.Unlock()
		return Submission{}, c.failure(ErrLimit)
	}
	c.calls[callIndex] = clientCall{used: true, kind: kind, transaction: txIndex}
	if kind == Begin {
		c.transactions[txIndex] = clientTransaction{used: true, busy: true}
	}
	if kind >= Append {
		c.transactions[txIndex].busy = true
	}
	c.mu.Unlock()
	ack, err := c.exchange(ctx, opSubmit, kind, rows, transaction)
	if err != nil {
		return Submission{}, err
	} // retain pending reservation
	c.mu.Lock()
	if ack.token <= c.ordinal {
		c.mu.Unlock()
		return Submission{}, c.failure(ErrProtocol)
	}
	c.ordinal = ack.token
	out := Submission{Producer: c.config.Producer, Ordinal: ack.token, Transaction: transaction}
	if kind == Begin {
		out.Transaction = ack.token
		c.transactions[txIndex].token = ack.token
	}
	if kind == Append {
		c.transactions[txIndex].rows += rows
	}
	c.calls[callIndex].submission = out
	c.mu.Unlock()
	return out, nil
}

// Settle must follow the reducer's native-outcome rules; uncertainty calls Fail.
func (c *Client) Settle(ctx context.Context, submission Submission) error {
	release, err := c.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	c.mu.Lock()
	index := -1
	for i := 0; i < c.config.Calls; i++ {
		if c.calls[i].used && c.calls[i].submission == submission && submission.Ordinal != 0 {
			index = i
			break
		}
	}
	if index < 0 {
		c.mu.Unlock()
		return c.failure(ErrProtocol)
	}
	active := c.calls[index]
	c.mu.Unlock()
	if _, err := c.exchange(ctx, opSettle, 0, 0, submission.Ordinal); err != nil {
		return err
	}
	c.mu.Lock()
	if active.kind == Commit || active.kind == Cancel {
		c.transactions[active.transaction] = clientTransaction{}
	} else if active.transaction >= 0 {
		c.transactions[active.transaction].busy = false
	}
	c.calls[index] = clientCall{}
	c.mu.Unlock()
	return nil
}

func (c *Client) busyLocked() bool {
	for i := 0; i < c.config.Calls; i++ {
		if c.calls[i].used {
			return true
		}
	}
	for i := 0; i < c.config.Transactions; i++ {
		if c.transactions[i].used {
			return true
		}
	}
	return false
}

func (c *Client) Checkpoint(ctx context.Context) error {
	release, err := c.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	c.mu.Lock()
	if c.busyLocked() {
		c.mu.Unlock()
		return ErrBusy
	}
	if c.fenced {
		c.mu.Unlock()
		return ErrFenced
	}
	c.mu.Unlock()
	if _, err := c.exchange(ctx, opCheckpoint, 0, 0, 0); err != nil {
		return err
	}
	c.mu.Lock()
	c.fenced = true
	c.mu.Unlock()
	return nil
}

// Resume carries no UUID or call and sends no fabricated phase event. The next
// actual request must match the independently advanced parent's current phase.
func (c *Client) Resume(phase uint32) error {
	if c == nil {
		return ErrConfig
	}
	select {
	case c.gate <- struct{}{}:
		defer func() { <-c.gate }()
	default:
		return ErrBusy
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return c.err
	}
	if c.closed || c.ctx.Err() != nil {
		return ErrCanceled
	}
	if !c.fenced || c.busyLocked() {
		return ErrBusy
	}
	next := c.phase + 1
	for next <= MaximumPhases && !phaseIn(c.config.Phases, next) {
		next++
	}
	if phase != next || phase > MaximumPhases {
		return ErrProtocol
	}
	c.phase, c.fenced = phase, false
	return nil
}

func (c *Client) Fail(ctx context.Context, reason error) error {
	release, err := c.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	_, _ = c.exchange(ctx, opFail, Kind(failureKind(reason)), 0, 0)
	return c.failure(reason)
}

// Close waits for exact ACK, half-closes, and requires parent EOF under the same
// ACK deadline. A successful exchange alone cannot claim parent or child exit.
func (c *Client) Close(ctx context.Context) error {
	if c == nil {
		return ErrConfig
	}
	c.mu.Lock()
	closed, existing := c.closed, c.err
	c.mu.Unlock()
	if closed || existing != nil {
		c.closeOwned()
		return existing
	}
	release, err := c.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	c.mu.Lock()
	if c.busyLocked() {
		c.mu.Unlock()
		return ErrBusy
	}
	c.mu.Unlock()
	if _, err := c.exchange(ctx, opClose, 0, 0, 0); err != nil {
		return err
	}
	join := closeOnCancel(ctx, c.conn)
	defer join()
	if err := c.conn.CloseWrite(); err != nil {
		return c.failure(ErrTransport)
	}
	var extra [1]byte
	n, err := c.conn.Read(extra[:])
	if n != 0 {
		return c.failure(ErrProtocol)
	}
	if err != io.EOF {
		return c.failure(ErrTransport)
	}
	if ctx.Err() != nil || c.ctx.Err() != nil {
		return c.failure(ErrCanceled)
	}
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	c.closeOwned()
	return nil
}
