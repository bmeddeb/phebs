package dispatchadmission

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// EngineMeasurementCheckpointV2 selects new checkpoint/recovery points;
	// omission retains every historical EM01 budget and wire byte.
	EngineMeasurementCheckpointV2 = "t42-linux-checkpoint-engine-v2"

	engineMeasurementHeaderBytes        = 76
	engineMeasurementFrameBytes         = 16
	engineMeasurementIdentityBytes      = 32
	engineMeasurementHold          byte = 1
	engineMeasurementRelease       byte = 2
	// Cleanup alone may outlive a failed measurement. It grants no new walk.
	engineMeasurementCleanupTimeout = 5 * time.Second
)

// EngineMeasurementIdentity is compared with the child's already-owned engine.
// The endpoint derives these facts; a request never supplies a PID or action.
type EngineMeasurementIdentity struct {
	PID    uint32
	Birth  uint64
	Tracer uint32
}

// EngineMeasurementBinding copies the authenticated producer/input binding.
// It does not issue native process, image or measurement authority.
type EngineMeasurementBinding struct {
	Recipe          string
	ProducerID      uint32
	ProducerBinding [32]byte
	InputSHA256     [32]byte
}

func (binding EngineMeasurementBinding) maximum() uint32 {
	switch binding.Recipe {
	case "":
		return engineMeasurementMaximum(binding.ProducerID)
	case EngineMeasurementCheckpointV2:
		switch binding.ProducerID {
		case 4:
			return 7
		case 5:
			return 1
		}
	}
	return 0
}

func (binding EngineMeasurementBinding) phaseAllowed(phase uint32) bool {
	if binding.Recipe == "" {
		return engineMeasurementPhase(binding.ProducerID, phase)
	}
	return binding.Recipe == EngineMeasurementCheckpointV2 &&
		(binding.ProducerID == 4 && phase >= 6 && phase <= 8 || binding.ProducerID == 5 && phase == 8)
}

func engineMeasurementMaximum(producer uint32) uint32 {
	switch producer {
	case 2:
		return 6
	case 3:
		return 1
	case 4:
		return 2
	default:
		return 0
	}
}

func engineMeasurementPhase(producer, phase uint32) bool {
	return producer == 2 && phase >= 2 && phase <= 4 || producer == 3 && phase == 5 || producer == 4 && phase == 6
}

func (record ProductionBootstrap) validateEngineMeasurements() error {
	if record.EngineMeasurements == 0 {
		if record.EngineMeasurementRecipe == "" {
			return nil
		}
		return ErrProductionBootstrap
	}
	phases := map[uint32][]uint32{2: {2, 3, 4}, 3: {5}, 4: {6, 7, 8}}[record.Producer.ID]
	binding := EngineMeasurementBinding{Recipe: record.EngineMeasurementRecipe, ProducerID: record.Producer.ID}
	if binding.Recipe == EngineMeasurementCheckpointV2 {
		phases = map[uint32][]uint32{4: {6, 7, 8}, 5: {8, 9, 10, 11}}[binding.ProducerID]
		if record.Control.BackupEndpointCarry || record.Producer.ID == 4 && record.Control.TerminalPhase != 8 || record.Producer.ID == 5 && record.Control.TerminalPhase != 0 {
			return ErrProductionBootstrap
		}
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || record.ArchiveMeasurements != 0 ||
		record.Program != ProgramPhebs || record.SemanticMode != ProductionSemanticV3 || record.Store == nil || record.Workspace == nil ||
		record.InputSHA256 == ([32]byte{}) || record.EngineMeasurements != binding.maximum() || len(phases) == 0 ||
		record.Phase != phases[0] || !slices.Equal(record.Control.Phases, phases) || len(record.Tools) != 3 ||
		!foregroundGitRuntimeRecipe(record.Tools[0].RuntimeRecipe) || record.Tools[2].RuntimeRecipe != record.Tools[0].RuntimeRecipe ||
		(binding.Recipe == EngineMeasurementCheckpointV2) != (record.Tools[0].RuntimeRecipe == ProductionLinuxForegroundGitV3) {
		return ErrProductionBootstrap
	}
	return nil
}

// EngineMeasurementPhase reads only the current controller phase and the
// already-attached producer. A pause/checkpoint does not manufacture a phase.
func (c *Controller) EngineMeasurementPhase(producer uint32) (uint32, error) {
	return c.EngineMeasurementPhaseForRecipe(producer, "")
}

// EngineMeasurementPhaseForRecipe retains the actual attached producer/current
// phase check for the separately authenticated checkpoint measurement recipe.
func (c *Controller) EngineMeasurementPhaseForRecipe(producer uint32, recipe string) (uint32, error) {
	if c == nil || (EngineMeasurementBinding{Recipe: recipe, ProducerID: producer}).maximum() == 0 {
		return 0, ErrConfig
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkLocked(); err != nil {
		return 0, err
	}
	p := c.producers[producer]
	if p == nil || !p.attached || p.closed || p.hardDeath || p.pid <= 0 || c.phase < 0 || c.phase >= len(c.phases) {
		return 0, ErrProtocol
	}
	return c.phases[c.phase].ID, nil
}

// EngineMeasurementWireBytes reserves header/echo and one HOLD/identity ACK/
// RELEASE/ACK per existing source point. EOF adds no bytes or DA admissions.
func EngineMeasurementWireBytes(maximum uint32) (uint64, error) {
	if maximum == 0 || maximum > 6 {
		return 0, ErrConfig
	}
	return 2*engineMeasurementHeaderBytes + uint64(maximum)*(3*engineMeasurementFrameBytes+engineMeasurementIdentityBytes), nil
}

func (binding EngineMeasurementBinding) header(maximum uint32) ([engineMeasurementHeaderBytes]byte, error) {
	var out [engineMeasurementHeaderBytes]byte
	if maximum == 0 || maximum != binding.maximum() || binding.ProducerBinding == ([32]byte{}) || binding.InputSHA256 == ([32]byte{}) {
		return out, ErrProductionBootstrap
	}
	copy(out[:4], "EM01")
	if binding.Recipe == EngineMeasurementCheckpointV2 {
		out[3] = '2'
	}
	binary.BigEndian.PutUint32(out[4:8], binding.ProducerID)
	binary.BigEndian.PutUint32(out[8:12], maximum)
	copy(out[12:44], binding.ProducerBinding[:])
	copy(out[44:], binding.InputSHA256[:])
	return out, nil
}

// WireBytes validates the complete selected binding before reserving its
// header/echo and bounded HOLD/RELEASE exchanges within existing output limits.
func (binding EngineMeasurementBinding) WireBytes(maximum uint32) (uint64, error) {
	if _, err := binding.header(maximum); err != nil {
		return 0, err
	}
	return 2*engineMeasurementHeaderBytes + uint64(maximum)*(3*engineMeasurementFrameBytes+engineMeasurementIdentityBytes), nil
}

func (binding EngineMeasurementBinding) frame(op byte, sequence, phase uint32) [engineMeasurementFrameBytes]byte {
	frame := engineMeasurementFrame(op, sequence, phase)
	if binding.Recipe == EngineMeasurementCheckpointV2 {
		frame[3] = '2'
	}
	return frame
}

func engineMeasurementFrame(op byte, sequence, phase uint32) [engineMeasurementFrameBytes]byte {
	var frame [engineMeasurementFrameBytes]byte
	copy(frame[:4], "EM01")
	frame[4] = op
	binary.BigEndian.PutUint32(frame[8:12], phase)
	binary.BigEndian.PutUint32(frame[12:16], sequence)
	return frame
}

func engineMeasurementIdentityFrame(frame [engineMeasurementFrameBytes]byte, identity EngineMeasurementIdentity) [engineMeasurementIdentityBytes]byte {
	var ack [engineMeasurementIdentityBytes]byte
	copy(ack[:16], frame[:])
	binary.BigEndian.PutUint32(ack[16:20], identity.PID)
	binary.BigEndian.PutUint32(ack[20:24], identity.Tracer)
	binary.BigEndian.PutUint64(ack[24:32], identity.Birth)
	return ack
}

func validEngineMeasurementIdentity(identity EngineMeasurementIdentity) bool {
	return identity.PID != 0 && identity.Birth != 0 && identity.Tracer != 0
}

// Deadline interruption, rather than closing the socket, leaves the child able
// to send RELEASE under its separate cleanup bound. Joining prevents late
// callbacks from changing that cleanup deadline or touching a retired endpoint.
func engineMeasurementCancellation(ctx context.Context, conn *net.UnixConn) func() {
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()); close(done) })
	var once sync.Once
	return func() {
		once.Do(func() {
			if !stop() {
				<-done
			}
		})
	}
}

// ServeEngineMeasurement owns this endpoint until its synchronous guard and
// every socket cancellation callback join. The native guard derives identity,
// proves whole-group quiescence before invoking the callback exactly once, and
// returns only after actual resume. No root.done wait belongs in that guard.
func ServeEngineMeasurement(ctx context.Context, conn *net.UnixConn, binding EngineMeasurementBinding, maximum uint32, guard func(context.Context, uint32, func(context.Context, EngineMeasurementIdentity) error) error) (retErr error) {
	defer func() {
		if recover() != nil {
			retErr = errors.Join(retErr, ErrPanic)
		}
	}()
	header, err := binding.header(maximum)
	if err != nil || guard == nil || archiveMeasurementDeadline(ctx, conn) != nil {
		return ErrProductionBootstrap
	}
	join := engineMeasurementCancellation(ctx, conn)
	defer func() { join(); _ = conn.Close() }()
	hello, cancel := context.WithTimeout(ctx, ProductionBootstrapTimeout)
	defer cancel()
	if archiveMeasurementDeadline(hello, conn) != nil {
		return ErrProductionBootstrap
	}
	if n, e := conn.Write(header[:]); e != nil || n != len(header) {
		return ErrTransport
	}
	var echo [engineMeasurementHeaderBytes]byte
	if _, e := io.ReadFull(conn, echo[:]); e != nil || echo != header || hello.Err() != nil {
		return ErrProductionBootstrap
	}
	if archiveMeasurementDeadline(ctx, conn) != nil {
		return ErrTransport
	}
	for sequence := uint32(1); ; sequence++ {
		var frame [engineMeasurementFrameBytes]byte
		n, e := io.ReadFull(conn, frame[:])
		if n == 0 && e == io.EOF && ctx.Err() == nil {
			return conn.CloseWrite()
		}
		if e != nil {
			return errors.Join(ErrTransport, e)
		}
		phase := binary.BigEndian.Uint32(frame[8:12])
		if sequence > maximum || !binding.phaseAllowed(phase) || frame != binding.frame(engineMeasurementHold, sequence, phase) {
			return ErrProtocol
		}
		var mu sync.Mutex
		var complete atomic.Bool
		var callbackErr error
		invoked, closed, valid := 0, false, false
		func() {
			defer func() {
				if recover() != nil {
					err = ErrPanic
				}
				finished := complete.Load()
				if !finished {
					_ = conn.Close()
				}
				mu.Lock()
				closed = true
				err = errors.Join(err, callbackErr)
				valid = invoked == 1 && finished
				mu.Unlock()
			}()
			err = guard(ctx, phase, func(guardCtx context.Context, identity EngineMeasurementIdentity) (callbackResult error) {
				mu.Lock()
				defer mu.Unlock()
				defer func() { callbackErr = errors.Join(callbackErr, callbackResult) }()
				if closed || invoked != 0 {
					return ErrProtocol
				}
				invoked++
				defer complete.Store(true)
				if !validEngineMeasurementIdentity(identity) || guardCtx == nil || ctx.Err() != nil {
					return ErrProtocol
				}
				guardDeadline, bounded := guardCtx.Deadline()
				endpointDeadline, _ := ctx.Deadline()
				if !bounded || guardDeadline.After(endpointDeadline) || archiveMeasurementDeadline(guardCtx, conn) != nil {
					return ErrProtocol
				}
				joinGuard := engineMeasurementCancellation(guardCtx, conn)
				defer joinGuard()
				ack := engineMeasurementIdentityFrame(frame, identity)
				if n, e := conn.Write(ack[:]); e != nil || n != len(ack) {
					return ErrTransport
				}
				var release [engineMeasurementFrameBytes]byte
				if _, e := io.ReadFull(conn, release[:]); e != nil || release != binding.frame(engineMeasurementRelease, sequence, phase) {
					return errors.Join(ErrProtocol, e)
				}
				return errors.Join(ctx.Err(), guardCtx.Err())
			})
		}()
		if err != nil || !valid || ctx.Err() != nil {
			return errors.Join(ErrIncomplete, err, ctx.Err())
		}
		if archiveMeasurementDeadline(ctx, conn) != nil {
			return ErrTransport
		}
		ack := binding.frame(engineMeasurementRelease, sequence, phase)
		if n, e := conn.Write(ack[:]); e != nil || n != len(ack) {
			return ErrTransport
		}
	}
}

type engineMeasurementClient struct {
	mu                sync.Mutex
	conn              *net.UnixConn
	ctx               context.Context
	binding           EngineMeasurementBinding
	maximum, sequence uint32
	closed            bool
	err               error
}

func newEngineMeasurementClient(bootstrap, operation context.Context, conn *net.UnixConn, binding EngineMeasurementBinding, maximum uint32) (*engineMeasurementClient, error) {
	header, err := binding.header(maximum)
	if err != nil || operation == nil || operation.Err() != nil || archiveMeasurementDeadline(bootstrap, conn) != nil {
		return nil, ErrProductionBootstrap
	}
	join := engineMeasurementCancellation(bootstrap, conn)
	defer join()
	var actual [engineMeasurementHeaderBytes]byte
	if _, e := io.ReadFull(conn, actual[:]); e != nil || actual != header || bootstrap.Err() != nil {
		return nil, ErrProductionBootstrap
	}
	if n, e := conn.Write(header[:]); e != nil || n != len(header) {
		return nil, ErrTransport
	}
	return &engineMeasurementClient{conn: conn, ctx: operation, binding: binding, maximum: maximum}, nil
}

func (client *engineMeasurementClient) measure(ctx context.Context, phase uint32, expected EngineMeasurementIdentity, measure func(context.Context) error) (retErr error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.closed || client.err != nil || client.ctx.Err() != nil || measure == nil || !validEngineMeasurementIdentity(expected) || !client.binding.phaseAllowed(phase) || archiveMeasurementDeadline(ctx, client.conn) != nil {
		return ErrProductionBootstrap
	}
	if deadline, ok := client.ctx.Deadline(); ok {
		current, _ := ctx.Deadline()
		if current.After(deadline) {
			return ErrProductionBootstrap
		}
	}
	if client.sequence >= client.maximum {
		return ErrLimit
	}
	join := engineMeasurementCancellation(ctx, client.conn)
	joinLifetime := engineMeasurementCancellation(client.ctx, client.conn)
	defer func() {
		if recover() != nil {
			retErr = errors.Join(retErr, ErrPanic)
		}
		join()
		joinLifetime()
		retErr = errors.Join(retErr, ctx.Err(), client.ctx.Err())
		if retErr != nil {
			client.err = retErr
			_ = client.conn.Close()
		}
	}()
	client.sequence++
	frame := client.binding.frame(engineMeasurementHold, client.sequence, phase)
	if n, e := client.conn.Write(frame[:]); e != nil || n != len(frame) {
		return ErrTransport
	}
	var ack [engineMeasurementIdentityBytes]byte
	if _, e := io.ReadFull(client.conn, ack[:]); e != nil || string(ack[:16]) != string(frame[:]) {
		return errors.Join(ErrTransport, e)
	}
	identity := EngineMeasurementIdentity{PID: binary.BigEndian.Uint32(ack[16:20]), Tracer: binary.BigEndian.Uint32(ack[20:24]), Birth: binary.BigEndian.Uint64(ack[24:32])}
	// A complete HOLD ACK owns cleanup even when identity comparison fails.
	defer func() {
		join()
		joinLifetime()
		cleanup, cancel := context.WithTimeout(context.Background(), engineMeasurementCleanupTimeout)
		defer cancel()
		if e := archiveMeasurementDeadline(cleanup, client.conn); e != nil {
			retErr = errors.Join(retErr, e)
			return
		}
		joinCleanup := engineMeasurementCancellation(cleanup, client.conn)
		defer joinCleanup()
		release := client.binding.frame(engineMeasurementRelease, client.sequence, phase)
		if n, e := client.conn.Write(release[:]); e != nil || n != len(release) {
			retErr = errors.Join(retErr, ErrTransport, e)
			return
		}
		var response [engineMeasurementFrameBytes]byte
		if _, e := io.ReadFull(client.conn, response[:]); e != nil || response != release || cleanup.Err() != nil {
			retErr = errors.Join(retErr, ErrTransport, e, cleanup.Err())
		}
	}()
	if identity != expected {
		return ErrProtocol
	}
	return measure(ctx)
}

func (client *engineMeasurementClient) close(ctx context.Context) (retErr error) {
	if !client.mu.TryLock() {
		return ErrBusy
	}
	defer client.mu.Unlock()
	if client.closed {
		return client.err
	}
	client.closed = true
	defer func() { client.err = errors.Join(client.err, retErr); _ = client.conn.Close() }()
	if client.err != nil {
		return client.err
	}
	if archiveMeasurementDeadline(ctx, client.conn) != nil {
		return ErrProductionBootstrap
	}
	join := engineMeasurementCancellation(ctx, client.conn)
	defer join()
	if err := client.conn.CloseWrite(); err != nil {
		return err
	}
	var extra [1]byte
	n, err := client.conn.Read(extra[:])
	if n != 0 || err != io.EOF || ctx.Err() != nil {
		return errors.Join(ErrIncomplete, err, ctx.Err())
	}
	return nil
}

// ProductionEngineMeasurement selects only the bootstrap-bound endpoint. An
// unselected ordinary lifetime has no new state; a traced selected engine may
// never fall back to an unauthenticated local stop.
func ProductionEngineMeasurement(ctx context.Context, expected EngineMeasurementIdentity, measure func(context.Context) error) (bool, error) {
	lifetime := productionRuntime.Load()
	if lifetime == nil {
		return false, nil
	}
	if lifetime.engineMeasurements == 0 {
		if expected.Tracer == 0 {
			return false, nil
		}
		return true, ErrProductionBootstrap
	}
	if lifetime.program != ProgramPhebs || lifetime.semanticMode != ProductionSemanticV3 || lifetime.engineMeasurement == nil || lifetime.client == nil {
		return true, ErrProductionBootstrap
	}
	lifetime.client.mu.Lock()
	phase := lifetime.client.phase
	err := lifetime.client.err
	if err == nil && (lifetime.client.closed || lifetime.client.ctx.Err() != nil) {
		err = ErrProductionBootstrap
	}
	lifetime.client.mu.Unlock()
	if err == nil {
		err = lifetime.engineMeasurement.measure(ctx, phase, expected, measure)
	}
	if err != nil {
		return true, errors.Join(err, lifetime.client.fail(ErrIncomplete))
	}
	return true, nil
}
