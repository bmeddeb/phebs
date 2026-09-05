package dispatchadmission

import (
	"context"
	"encoding/binary"
	"io"
	"math"
	"net"
	"os"
	"slices"
	"sync"
	"time"
)

const (
	phasePause byte = iota + 1
	phaseCheckpoint
	phaseResume
)

// PhaseControlConfig bounds an explicitly inherited control endpoint. These
// caller-owned limits are not frozen ceremony limits or tool/input admission.
// The separate control socket preserves DA01's single-request echo protocol.
type PhaseControlConfig struct {
	Phases           []uint32
	InitialPhase     uint32
	MaximumPhases    int
	MaximumWireBytes uint64
	Timeout          time.Duration
}

func (config PhaseControlConfig) validate() (int, error) {
	if config.MaximumPhases < 1 || len(config.Phases) == 0 || len(config.Phases) > config.MaximumPhases ||
		config.MaximumWireBytes < 2*FrameBytes || config.Timeout <= 0 {
		return 0, ErrConfig
	}
	// ponytail: O(P²) within MaximumPhases; use a set if the caller-owned cap grows.
	for index, phase := range config.Phases {
		if phase == 0 || slices.Contains(config.Phases[:index], phase) {
			return 0, ErrConfig
		}
	}
	index := slices.Index(config.Phases, config.InitialPhase)
	if index < 0 {
		return 0, ErrConfig
	}
	return index, nil
}

type phaseControlFrame struct {
	op       byte
	phase    uint32
	sequence uint64
	binding  [32]byte
}

func (frame phaseControlFrame) encode() [FrameBytes]byte {
	var raw [FrameBytes]byte
	copy(raw[:4], "PC01")
	raw[4] = frame.op
	binary.BigEndian.PutUint32(raw[8:12], frame.phase)
	binary.BigEndian.PutUint64(raw[16:24], frame.sequence)
	copy(raw[32:], frame.binding[:])
	return raw
}

func decodePhaseControl(raw [FrameBytes]byte) (phaseControlFrame, error) {
	frame := phaseControlFrame{op: raw[4], phase: binary.BigEndian.Uint32(raw[8:12]),
		sequence: binary.BigEndian.Uint64(raw[16:24])}
	copy(frame.binding[:], raw[32:])
	if frame.op < phasePause || frame.op > phaseResume || frame.encode() != raw {
		return phaseControlFrame{}, ErrProtocol
	}
	return frame, nil
}

// PhaseControl is the parent's concrete, serialized control endpoint. It owns
// no producer, process or admission socket. Its failure closes this endpoint;
// the remote server latches the corresponding producer failure. The owner must
// also propagate a returned control failure to its execution failure latch.
// No autonomous reader or heartbeat runs on the parent side.
type PhaseControl struct {
	mu          sync.Mutex
	conn        *net.UnixConn
	ctx         context.Context
	cancel      context.CancelCauseFunc
	stopContext func() bool
	gate        chan struct{}
	config      PhaseControlConfig
	binding     [32]byte
	index       int
	state       byte
	sequence    uint64
	wireBytes   uint64
	closed      bool
	err         error
}

// NewPhaseControl adopts file on every path. The owner must have bound both
// inherited endpoints to the same producer lifetime before using this control.
// Neither an environment selector nor this constructor issues private admission.
func NewPhaseControl(ctx context.Context, file *os.File, binding [32]byte, config PhaseControlConfig) (*PhaseControl, error) {
	conn, err := adopt(file)
	if err != nil {
		return nil, err
	}
	index, err := config.validate()
	if err != nil || ctx == nil || ctx.Err() != nil || binding == ([32]byte{}) {
		_ = conn.Close()
		return nil, ErrConfig
	}
	config.Phases = slices.Clone(config.Phases)
	control := &PhaseControl{conn: conn, config: config, binding: binding, index: index, gate: make(chan struct{}, 1)}
	control.ctx, control.cancel = context.WithCancelCause(ctx)
	control.stopContext = context.AfterFunc(control.ctx, func() { _ = conn.Close() })
	return control, nil
}

func (control *PhaseControl) Context() context.Context { return control.ctx }

// ReservedWireBytes counts fixed request/ACK reservations, not observed bytes.
// The receiving endpoint may additionally reserve one unused idle/EOF pair.
func (control *PhaseControl) ReservedWireBytes() uint64 {
	control.mu.Lock()
	defer control.mu.Unlock()
	return control.wireBytes
}

func (control *PhaseControl) fail(err error) error {
	control.mu.Lock()
	defer control.mu.Unlock()
	if control.err == nil {
		control.err = err
		control.cancel(err)
	}
	return control.err
}

// Pause must be called for every live producer before Controller.Fence. Owners
// first quiesce their work sources; parking a nested dispatch while waiting for
// its earlier piped command can otherwise stall until the bounded deadline.
// This pauses admission, not workers, durable mutations or semantic authority.
func (control *PhaseControl) Pause(ctx context.Context) error {
	return control.exchange(ctx, phasePause)
}

// Checkpoint follows Pause-all and Controller.Fence. Only settled one-shots and
// explicitly carried persistent handles satisfy the accounting checkpoint.
func (control *PhaseControl) Checkpoint(ctx context.Context) error {
	return control.exchange(ctx, phaseCheckpoint)
}

// Resume selects only the next configured phase, after Controller.Advance.
// It must not precede the owner's independent quiet/current-authority checks.
func (control *PhaseControl) Resume(ctx context.Context) error {
	return control.exchange(ctx, phaseResume)
}

func nextControlState(state byte, index int, op byte, phases []uint32) (byte, int, error) {
	switch {
	case op == phasePause && state == 0:
		return phasePause, index, nil
	case op == phaseCheckpoint && state == phasePause:
		return phaseCheckpoint, index, nil
	case op == phaseResume && state == phaseCheckpoint && index+1 < len(phases):
		return 0, index + 1, nil
	default:
		return 0, 0, ErrProtocol
	}
}

func (control *PhaseControl) exchange(ctx context.Context, op byte) error {
	if ctx == nil {
		return control.fail(ErrCanceled)
	}
	opCtx, cancel := context.WithTimeout(ctx, control.config.Timeout)
	defer cancel()
	select {
	case control.gate <- struct{}{}:
		defer func() { <-control.gate }()
	case <-opCtx.Done():
		return control.fail(ErrCanceled)
	case <-control.ctx.Done():
		return control.fail(ErrCanceled)
	}
	control.mu.Lock()
	state, index, err := nextControlState(control.state, control.index, op, control.config.Phases)
	if control.err != nil {
		err = control.err
	} else if control.closed || control.ctx.Err() != nil || opCtx.Err() != nil {
		err = ErrCanceled
	} else if control.sequence == math.MaxUint64 || control.config.MaximumWireBytes-control.wireBytes < 2*FrameBytes {
		err = ErrLimit
	}
	if err != nil {
		control.mu.Unlock()
		return control.fail(err)
	}
	control.sequence++
	control.wireBytes += 2 * FrameBytes
	frame := phaseControlFrame{op: op, phase: control.config.Phases[index], sequence: control.sequence, binding: control.binding}
	control.mu.Unlock()
	deadline, _ := opCtx.Deadline()
	if err := control.conn.SetDeadline(deadline); err != nil {
		return control.fail(ErrTransport)
	}
	stop := context.AfterFunc(opCtx, func() { _ = control.conn.Close() })
	defer stop()
	raw := frame.encode()
	if count, err := control.conn.Write(raw[:]); err != nil || count != len(raw) {
		return control.fail(ErrTransport)
	}
	var ack [FrameBytes]byte
	if _, err := io.ReadFull(control.conn, ack[:]); err != nil {
		return control.fail(ErrTransport)
	}
	if ack != raw {
		return control.fail(ErrProtocol)
	}
	if opCtx.Err() != nil || control.ctx.Err() != nil {
		return control.fail(ErrCanceled)
	}
	control.mu.Lock()
	control.state, control.index = state, index
	control.mu.Unlock()
	return nil
}

// Close releases only the control endpoint. Clean producer completion requires
// a separate successful Client.Close after owned handles and custody drain.
func (control *PhaseControl) Close() error {
	control.mu.Lock()
	defer control.mu.Unlock()
	if !control.closed {
		control.closed = true
		control.stopContext()
		_ = control.conn.Close()
		control.cancel(nil)
	}
	return control.err
}

func clientTerminalClosed(client *Client) bool {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.closed && client.err == nil
}

// StartPhaseControl synchronously adopts and protects the inherited file before
// starting its one owned receiver goroutine. Returning first and adopting in a
// goroutine would let an immediate native child inherit the original descriptor.
// The owner joins the returned completion channel after Client.Close; it carries
// one terminal result and is then closed. No idle timer or polling loop runs.
// Private binding and phases must come from the owning admitted bootstrap, not
// ambient discovery; this endpoint by itself does not implement that bootstrap.
func StartPhaseControl(ctx context.Context, file *os.File, client *Client, config PhaseControlConfig) (_ <-chan error, err error) {
	conn, err := adopt(file)
	if err != nil {
		if client != nil {
			return nil, client.fail(err)
		}
		return nil, err
	}
	transferred := false
	var cancel context.CancelFunc
	var stopContext, stopClient func() bool
	defer func() {
		if !transferred {
			if stopContext != nil {
				stopContext()
			}
			if stopClient != nil {
				stopClient()
			}
			if cancel != nil {
				cancel()
			}
			_ = conn.Close()
		}
		if recover() != nil {
			err = ErrPanic
			if client != nil {
				err = client.fail(err)
			}
		}
	}()
	index, err := config.validate()
	if err != nil || ctx == nil || ctx.Err() != nil || client == nil {
		if client != nil {
			return nil, client.fail(ErrConfig)
		}
		return nil, ErrConfig
	}
	config.Phases = slices.Clone(config.Phases)
	client.mu.Lock()
	valid := client.phase == config.InitialPhase && !client.closed && client.err == nil &&
		!client.controlAttached && client.ctx.Err() == nil
	binding := client.binding
	if valid {
		client.controlAttached = true
	}
	client.mu.Unlock()
	if !valid {
		return nil, client.fail(ErrConfig)
	}
	var runCtx context.Context
	runCtx, cancel = context.WithCancel(ctx)
	stopContext = context.AfterFunc(runCtx, func() { _ = conn.Close() })
	stopClient = context.AfterFunc(client.Context(), func() { cancel(); _ = conn.Close() })
	done := make(chan error, 1)
	go func() {
		result := servePhaseControl(runCtx, conn, client, config, index, binding)
		stopContext()
		stopClient()
		cancel()
		_ = conn.Close()
		done <- result
		close(done)
	}()
	transferred = true
	return done, nil
}

// Each request's deadline includes frame transfer, local pause/drain and its
// admission-socket ACKs. Unexpected loss or partial/invalid requests fail sticky.
func servePhaseControl(ctx context.Context, conn *net.UnixConn, client *Client, config PhaseControlConfig, index int, binding [32]byte) (err error) {
	defer func() {
		if recover() != nil {
			err = client.fail(ErrPanic)
		}
	}()
	var sequence, wireBytes uint64
	var state byte
	for {
		if clientTerminalClosed(client) {
			return nil
		}
		if config.MaximumWireBytes-wireBytes < 2*FrameBytes {
			return client.fail(ErrLimit)
		}
		wireBytes += 2 * FrameBytes
		if err := conn.SetDeadline(time.Time{}); err != nil {
			if clientTerminalClosed(client) {
				return nil
			}
			return client.fail(ErrTransport)
		}
		var raw [FrameBytes]byte
		if _, err := io.ReadFull(conn, raw[:1]); err != nil {
			if clientTerminalClosed(client) {
				return nil
			}
			return client.fail(ErrIncomplete)
		}
		deadline := time.Now().Add(config.Timeout)
		if err := conn.SetDeadline(deadline); err != nil {
			return client.fail(ErrTransport)
		}
		if _, err := io.ReadFull(conn, raw[1:]); err != nil {
			return client.fail(ErrTransport)
		}
		frame, err := decodePhaseControl(raw)
		if err != nil || frame.binding != binding || sequence == math.MaxUint64 || frame.sequence != sequence+1 {
			return client.fail(ErrProtocol)
		}
		nextState, nextIndex, err := nextControlState(state, index, frame.op, config.Phases)
		if err != nil || frame.phase != config.Phases[nextIndex] {
			return client.fail(ErrProtocol)
		}
		sequence++
		opCtx, stop := context.WithDeadline(ctx, deadline)
		switch frame.op {
		case phasePause:
			err = client.Pause(opCtx)
		case phaseCheckpoint:
			err = client.Checkpoint(opCtx)
		case phaseResume:
			err = client.Resume(frame.phase)
		}
		if err == nil && opCtx.Err() != nil {
			err = ErrCanceled
		}
		stop()
		if err != nil {
			return client.fail(err)
		}
		state, index = nextState, nextIndex
		if count, err := conn.Write(raw[:]); err != nil || count != len(raw) {
			return client.fail(ErrTransport)
		}
	}
}
