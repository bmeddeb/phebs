package storeaccounting

import (
	"context"
	"io"
	"math"
	"net"
	"os"
	"sync"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

type wirePeer struct {
	config                        ClientConfig
	conn                          *net.UnixConn
	done                          chan struct{}
	opened, attached, closed, eof bool
	sequence, bytes               uint64
	err                           error
}

// Transport owns at most seven authenticated socket receivers. The caller
// still owns genuine child launch/Wait and all SDK/read-tail drain. Do not copy.
type Transport struct {
	mu             sync.Mutex // short fixed-array state only; never held across socket I/O
	controller     *Controller
	ctx            context.Context
	cancel         context.CancelFunc
	stopController func() bool
	controllerDone chan struct{}
	peers          [MaximumProducers]wirePeer
	count          int
	bytes, limit   uint64
	closing        bool
	err            error
	closeOnce      sync.Once
}

type WireSnapshot struct {
	Store                       Snapshot
	ReservedBytes, MaximumBytes uint64
	Opened, TerminalEOF         int
	// Complete is protocol closure, NOT proof that a child consumed its final
	// ACK, exited, or drained every SDK call. Genuine Handle.Wait is separate.
	Complete bool
}

func NewTransport(ctx context.Context, controller *Controller, config WireConfig) (*Transport, error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, ErrConfig
	}
	clients, limit, err := controller.wireConfiguration(config)
	if err != nil {
		return nil, err
	}
	t := &Transport{controller: controller, count: len(config.Producers), limit: limit, controllerDone: make(chan struct{})}
	t.ctx, t.cancel = context.WithCancel(ctx)
	for i := 0; i < t.count; i++ {
		t.peers[i].config = clients[i]
	}
	t.stopController = context.AfterFunc(controller.ctx, func() { t.cancel(); close(t.controllerDone) })
	return t, nil
}

func (t *Transport) peerLocked(producer uint32) *wirePeer {
	for i := 0; i < t.count; i++ {
		if t.peers[i].config.Producer == producer {
			return &t.peers[i]
		}
	}
	return nil
}

func (t *Transport) failure(reason error) error {
	reason = t.controller.Fail(reason)
	t.mu.Lock()
	if t.err == nil {
		t.err = reason
	}
	err := t.err
	t.mu.Unlock()
	t.cancel()
	return err
}

// Open consumes the sole opening opportunity before creating either endpoint.
// It returns an owned child file, not a caller-supplied receiver or Client.
// The receiver performs actual Attach only after the authenticated first frame.
func (t *Transport) Open(producer uint32) (*os.File, ClientConfig, error) {
	if t == nil {
		return nil, ClientConfig{}, ErrConfig
	}
	phase, err := t.controller.wirePhase()
	if err != nil {
		return nil, ClientConfig{}, err
	}
	t.mu.Lock()
	p := t.peerLocked(producer)
	if p == nil || p.opened || t.closing || t.err != nil || t.ctx.Err() != nil || !phaseIn(p.config.Phases, phase) {
		t.mu.Unlock()
		return nil, ClientConfig{}, ErrConfig
	}
	p.opened, p.done, p.config.Phase = true, make(chan struct{}), phase
	config := p.config
	t.mu.Unlock()
	parent, child, err := dispatchadmission.NewPipe()
	if err == nil {
		p.conn, err = adopt(parent)
	}
	if err != nil {
		if child != nil {
			_ = child.Close()
		}
		t.finish(p, ErrTransport, false)
		return nil, ClientConfig{}, ErrTransport
	}
	go t.serve(p)
	return child, config, nil
}

func (t *Transport) finish(p *wirePeer, err error, eof bool) {
	if err != nil {
		err = t.failure(err)
	}
	if p.conn != nil {
		_ = p.conn.Close()
	}
	t.mu.Lock()
	p.err, p.eof = err, eof
	t.mu.Unlock()
	close(p.done)
}

func (t *Transport) reserve(p *wirePeer) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.err != nil {
		return t.err
	}
	if t.ctx.Err() != nil {
		return ErrCanceled
	}
	if t.limit-t.bytes < pairBytes || p.config.WireBytes-p.bytes < pairBytes {
		return ErrLimit
	}
	t.bytes += pairBytes
	p.bytes += pairBytes
	return nil
}

func (t *Transport) serve(p *wirePeer) {
	join := closeOnCancel(t.ctx, p.conn)
	var terminal error
	var eof bool
	defer func() { join(); t.finish(p, terminal, eof) }()
	for {
		if terminal = t.reserve(p); terminal != nil {
			return
		}
		// Idle is bounded by the owner's lifetime, not an invented work deadline.
		if terminal = p.conn.SetDeadline(time.Time{}); terminal != nil {
			terminal = ErrTransport
			return
		}
		var raw [FrameBytes]byte
		if _, err := io.ReadFull(p.conn, raw[:1]); err != nil {
			terminal = ErrIncomplete
			return
		}
		if err := p.conn.SetDeadline(time.Now().Add(p.config.AckTimeout)); err != nil {
			terminal = ErrTransport
			return
		}
		if _, err := io.ReadFull(p.conn, raw[1:]); err != nil {
			terminal = ErrTransport
			return
		}
		request, err := decodeFrame(raw, false)
		if err != nil {
			terminal = err
			return
		}
		phase, err := t.controller.wirePhase()
		if err != nil {
			terminal = err
			return
		}
		if request.binding != p.config.Binding || p.sequence == math.MaxUint64 || request.sequence != p.sequence+1 ||
			request.phase != phase || !phaseIn(p.config.Phases, phase) || (!p.attached && request.op != opAttach) {
			terminal = ErrProtocol
			return
		}
		p.sequence = request.sequence // only this receiver reads/writes sequence
		response := request
		response.op |= replyBit
		switch request.op {
		case opAttach:
			err = t.controller.Attach(p.config.Producer, phase)
			if err == nil {
				p.attached = true
			}
		case opSubmit:
			var submission Submission
			submission, err = t.controller.Submit(Request{Producer: p.config.Producer, Phase: phase,
				Kind: Kind(request.kind), Rows: uint64(request.rows), Transaction: request.token})
			response.token = submission.Ordinal
		case opSettle:
			var submission Submission
			submission, err = t.controller.wireSubmission(p.config.Producer, request.token)
			if err == nil {
				err = t.controller.Settle(submission)
			}
		case opCheckpoint:
			err = t.controller.Checkpoint(p.config.Producer, phase)
		case opClose:
			err = t.controller.Close(p.config.Producer)
		case opFail:
			terminal = failureReason(request.kind)
			return
		}
		if err != nil {
			terminal = err
			return
		}
		ack := response.encode()
		if n, err := p.conn.Write(ack[:]); err != nil || n != len(ack) {
			terminal = ErrTransport
			return
		}
		if request.op == opClose {
			t.mu.Lock()
			p.closed = true
			t.mu.Unlock()
			if terminal = t.reserve(p); terminal != nil {
				return
			}
			// Close ACK and terminal half-close/EOF share this finite deadline.
			var extra [1]byte
			n, err := p.conn.Read(extra[:])
			if n != 0 {
				terminal = ErrProtocol
				return
			}
			if err != io.EOF {
				terminal = ErrTransport
				return
			}
			eof = true
			return
		}
	}
}

// Fence must follow genuine semantic-owner and ALL-SDK-call drain. This method
// cannot establish either precondition and does not stop native work itself.
func (t *Transport) Fence() error {
	if t == nil {
		return ErrConfig
	}
	if t.ctx.Err() != nil {
		return t.failure(ErrCanceled)
	}
	return t.controller.Fence()
}

func (t *Transport) Advance() error {
	if t == nil {
		return ErrConfig
	}
	if t.ctx.Err() != nil {
		return t.failure(ErrCanceled)
	}
	return t.controller.Advance()
}

func (t *Transport) Snapshot() (WireSnapshot, error) {
	if t == nil {
		return WireSnapshot{}, ErrConfig
	}
	store, err := t.controller.Snapshot()
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.err != nil {
		err = t.err
	}
	if err == nil && t.ctx.Err() != nil && !t.closing {
		err = ErrCanceled
	}
	out := WireSnapshot{Store: store, ReservedBytes: t.bytes, MaximumBytes: t.limit, Complete: store.Complete && err == nil}
	for i := 0; i < t.count; i++ {
		p := &t.peers[i]
		if p.opened {
			out.Opened++
		}
		if p.eof {
			out.TerminalEOF++
		}
		out.Complete = out.Complete && p.eof
	}
	return out, err
}

// Wait joins the actual receiver even when the waiting context expires.
func (t *Transport) Wait(ctx context.Context, producer uint32) error {
	if t == nil || ctx == nil {
		return ErrConfig
	}
	t.mu.Lock()
	p := t.peerLocked(producer)
	if p == nil || !p.opened {
		t.mu.Unlock()
		return ErrConfig
	}
	done := p.done
	t.mu.Unlock()
	select {
	case <-done:
	case <-ctx.Done():
		_ = t.failure(ErrCanceled)
		<-done
	}
	t.mu.Lock()
	err := p.err
	t.mu.Unlock()
	return err
}

// Close refuses incomplete protocol lifetimes, cancels them, and joins every
// receiver and cancellation callback. It never waits for GC or a borrowed FD.
func (t *Transport) Close() error {
	if t == nil {
		return ErrConfig
	}
	t.closeOnce.Do(func() {
		t.mu.Lock()
		t.closing = true
		var pending [MaximumProducers]chan struct{}
		complete := true
		for i := 0; i < t.count; i++ {
			pending[i] = t.peers[i].done
			complete = complete && t.peers[i].eof
		}
		t.mu.Unlock()
		if !complete {
			_ = t.failure(ErrIncomplete)
		}
		t.cancel()
		for _, done := range pending {
			if done != nil {
				<-done
			}
		}
		if !t.stopController() {
			<-t.controllerDone
		}
	})
	t.mu.Lock()
	err := t.err
	t.mu.Unlock()
	return err
}
