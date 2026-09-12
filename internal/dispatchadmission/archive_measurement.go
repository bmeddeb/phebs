package dispatchadmission

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const (
	archiveMeasurementHeaderBytes = 76
	archiveMeasurementFrameBytes  = 16
	archiveMeasurementHold        = 1
	archiveMeasurementRelease     = 2
)

// ArchiveMeasurementBinding is the existing bootstrap identity copied onto
// the extra private socket. It creates no new producer or input authority.
type ArchiveMeasurementBinding struct {
	ProducerID      uint32
	ProducerBinding [32]byte
	InputSHA256     [32]byte
}

func (record ProductionBootstrap) validateArchiveMeasurements() error {
	if record.ArchiveMeasurements == 0 {
		return nil
	}
	if record.Program != ProgramPhebs || record.SemanticMode != "" || (record.Producer.ID != 10 && record.Producer.ID != 11) ||
		record.Phase != 12 || record.Workspace == nil || record.ArchiveDeadlineUnixNano <= 0 || record.Store == nil || record.InputSHA256 == ([32]byte{}) {
		return ErrProductionBootstrap
	}
	return nil
}

// ArchiveMeasurementWireBytes derives the complete finite traffic reservation:
// one header and echo, four frames per checkpoint, then byte-free terminal EOF.
// The uint32 checkpoint bound makes multiplication and addition overflow-free.
func ArchiveMeasurementWireBytes(maximum uint32) (uint64, error) {
	if maximum == 0 {
		return 0, ErrConfig
	}
	return 2*archiveMeasurementHeaderBytes + uint64(maximum)*4*archiveMeasurementFrameBytes, nil
}

func (binding ArchiveMeasurementBinding) header(maximum uint32) ([archiveMeasurementHeaderBytes]byte, error) {
	var header [archiveMeasurementHeaderBytes]byte
	if binding.ProducerID != 10 || binding.ProducerBinding == ([32]byte{}) || binding.InputSHA256 == ([32]byte{}) || maximum == 0 {
		return header, ErrProductionBootstrap
	}
	copy(header[:4], "AM01")
	binary.BigEndian.PutUint32(header[4:8], binding.ProducerID)
	binary.BigEndian.PutUint32(header[8:12], maximum)
	copy(header[12:44], binding.ProducerBinding[:])
	copy(header[44:], binding.InputSHA256[:])
	return header, nil
}

func archiveMeasurementFrame(op byte, ordinal uint64) [archiveMeasurementFrameBytes]byte {
	var frame [archiveMeasurementFrameBytes]byte
	copy(frame[:4], "AM01")
	frame[4] = op
	binary.BigEndian.PutUint64(frame[8:], ordinal)
	return frame
}

// The returned join prevents a cancellation callback from closing a borrowed
// socket after its synchronous owner has returned. No worker outlives the join.
func archiveMeasurementCancellation(ctx context.Context, conn *net.UnixConn) func() {
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = conn.Close(); close(done) })
	return func() {
		if !stop() {
			<-done
		}
	}
}

func archiveMeasurementDeadline(ctx context.Context, conn *net.UnixConn) error {
	if ctx == nil || conn == nil || ctx.Err() != nil {
		return ErrProductionBootstrap
	}
	deadline, bounded := ctx.Deadline()
	if !bounded {
		return ErrProductionBootstrap
	}
	return conn.SetDeadline(deadline)
}

// ServeArchiveMeasurement borrows the parent's exact FD7 endpoint until it
// returns. The guard must own the retired engine and return only after resume
// and unlock. HOLD is acknowledged inside that guard; RELEASE is acknowledged
// only after it returns. EOF, malformed traffic or cancellation unwinds the
// guard before this server returns. No action, path or PID travels here.
func ServeArchiveMeasurement(ctx context.Context, conn *net.UnixConn, binding ArchiveMeasurementBinding, maximum uint32, guard func(context.Context, func(context.Context) error) error) (retErr error) {
	defer func() {
		if recover() != nil {
			retErr = ErrPanic
		}
	}()
	header, err := binding.header(maximum)
	if err != nil || guard == nil || archiveMeasurementDeadline(ctx, conn) != nil {
		return ErrProductionBootstrap
	}
	join := archiveMeasurementCancellation(ctx, conn)
	defer join()
	defer func() { _ = conn.Close() }()
	hello, cancel := context.WithTimeout(ctx, ProductionBootstrapTimeout)
	defer cancel()
	if archiveMeasurementDeadline(hello, conn) != nil {
		return ErrProductionBootstrap
	}
	if n, err := conn.Write(header[:]); err != nil || n != len(header) {
		return ErrTransport
	}
	var echo [archiveMeasurementHeaderBytes]byte
	if _, err := io.ReadFull(conn, echo[:]); err != nil || echo != header || hello.Err() != nil {
		return ErrProductionBootstrap
	}
	if archiveMeasurementDeadline(ctx, conn) != nil {
		return ErrTransport
	}
	for ordinal := uint64(1); ; ordinal++ {
		var frame [archiveMeasurementFrameBytes]byte
		n, err := io.ReadFull(conn, frame[:])
		if n == 0 && err == io.EOF && ctx.Err() == nil {
			return conn.CloseWrite()
		}
		if err != nil {
			return errors.Join(ErrTransport, err)
		}
		if ordinal > uint64(maximum) || frame != archiveMeasurementFrame(archiveMeasurementHold, ordinal) {
			return ErrProtocol
		}
		var callbackMu sync.Mutex
		var completed atomic.Bool
		var callbackErr error
		invoked, closed := 0, false
		valid := false
		func() {
			defer func() {
				if recover() != nil {
					err = ErrPanic
				}
				completedBeforeReturn := completed.Load()
				if !completedBeforeReturn {
					_ = conn.Close() // Unblock and join an invalid escaped callback, including guard panic.
				}
				callbackMu.Lock()
				closed = true
				err = errors.Join(err, callbackErr)
				valid = invoked == 1 && completedBeforeReturn
				callbackMu.Unlock()
			}()
			err = guard(ctx, func(context.Context) (retErr error) {
				callbackMu.Lock()
				defer callbackMu.Unlock()
				defer func() { callbackErr = errors.Join(callbackErr, retErr) }()
				if closed || invoked != 0 {
					return ErrProtocol
				}
				invoked++
				defer completed.Store(true)
				// Retain our original context and absolute deadline even if the
				// supplied guard attempts to replace the callback's context.
				if n, err := conn.Write(frame[:]); err != nil || n != len(frame) {
					return ErrTransport
				}
				if _, err := io.ReadFull(conn, frame[:]); err != nil || frame != archiveMeasurementFrame(archiveMeasurementRelease, ordinal) {
					return ErrProtocol
				}
				return ctx.Err()
			})
		}()
		if err != nil || !valid || ctx.Err() != nil {
			return errors.Join(ErrIncomplete, err, ctx.Err())
		}
		if archiveMeasurementDeadline(ctx, conn) != nil {
			return ErrTransport
		}
		if n, err := conn.Write(frame[:]); err != nil || n != len(frame) {
			return ErrTransport
		}
	}
}

type archiveMeasurementClient struct {
	mu       sync.Mutex
	conn     *net.UnixConn
	ctx      context.Context
	deadline time.Time
	maximum  uint32
	ordinal  uint64
	closed   bool
	err      error
}

func newArchiveMeasurementClient(bootstrap, operation context.Context, conn *net.UnixConn, binding ArchiveMeasurementBinding, maximum uint32) (*archiveMeasurementClient, error) {
	header, err := binding.header(maximum)
	if err != nil || archiveMeasurementDeadline(operation, conn) != nil || archiveMeasurementDeadline(bootstrap, conn) != nil {
		return nil, ErrProductionBootstrap
	}
	join := archiveMeasurementCancellation(bootstrap, conn)
	defer join()
	var actual [archiveMeasurementHeaderBytes]byte
	if _, err := io.ReadFull(conn, actual[:]); err != nil || actual != header || bootstrap.Err() != nil {
		return nil, ErrProductionBootstrap
	}
	if n, err := conn.Write(header[:]); err != nil || n != len(header) {
		return nil, ErrTransport
	}
	deadline, _ := operation.Deadline()
	return &archiveMeasurementClient{conn: conn, ctx: operation, deadline: deadline, maximum: maximum}, nil
}

func (client *archiveMeasurementClient) exchange(op byte, ordinal uint64) error {
	frame := archiveMeasurementFrame(op, ordinal)
	if n, err := client.conn.Write(frame[:]); err != nil || n != len(frame) {
		return ErrTransport
	}
	var ack [archiveMeasurementFrameBytes]byte
	if _, err := io.ReadFull(client.conn, ack[:]); err != nil || ack != frame {
		return ErrTransport
	}
	return nil
}

func (client *archiveMeasurementClient) measure(ctx context.Context, measure func(context.Context) error) (retErr error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.closed || client.err != nil || client.ctx.Err() != nil || ctx == nil || measure == nil {
		return ErrProductionBootstrap
	}
	deadline, bounded := ctx.Deadline()
	if !bounded || deadline.After(client.deadline) || archiveMeasurementDeadline(ctx, client.conn) != nil {
		return ErrProductionBootstrap
	}
	join := archiveMeasurementCancellation(ctx, client.conn)
	defer join()
	joinLifetime := archiveMeasurementCancellation(client.ctx, client.conn)
	defer joinLifetime()
	defer func() {
		if recover() != nil {
			retErr = ErrPanic
		}
		if retErr != nil {
			client.err = retErr
			_ = client.conn.Close()
		}
	}()
	if client.ordinal >= uint64(client.maximum) {
		return ErrLimit
	}
	client.ordinal++
	if err := client.exchange(archiveMeasurementHold, client.ordinal); err != nil {
		return err
	}
	// Also runs during callback panic. A failed/canceled release closes the
	// endpoint so the parent unwinds its guard; it never invents a release ACK.
	defer func() { retErr = errors.Join(retErr, client.exchange(archiveMeasurementRelease, client.ordinal)) }()
	return measure(ctx)
}

func (client *archiveMeasurementClient) close(ctx context.Context) (retErr error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.closed {
		return client.err
	}
	client.closed = true
	defer func() { client.err = errors.Join(client.err, retErr) }()
	defer func() { _ = client.conn.Close() }()
	if client.err != nil {
		return client.err
	}
	if err := archiveMeasurementDeadline(ctx, client.conn); err != nil {
		return err
	}
	deadline, _ := ctx.Deadline()
	if deadline.After(client.deadline) {
		return ErrProductionBootstrap
	}
	join := archiveMeasurementCancellation(ctx, client.conn)
	defer join()
	if err := client.conn.CloseWrite(); err != nil {
		return err
	}
	var extra [1]byte
	n, err := client.conn.Read(extra[:])
	if n != 0 || err != io.EOF || ctx.Err() != nil {
		return ErrIncomplete
	}
	return nil
}

func (lifetime *ProductionLifetime) closeArchiveMeasurement(ctx context.Context) error {
	if lifetime.archiveMeasurement == nil {
		return nil
	}
	return lifetime.archiveMeasurement.close(ctx)
}

// ProductionArchiveMeasurements returns the authenticated source-derived
// ceiling. Ordinary and legacy omitted-workspace launches acquire no state.
func ProductionArchiveMeasurements() (uint32, error) {
	lifetime := productionRuntime.Load()
	if lifetime == nil {
		return 0, nil
	}
	lifetime.workspaceMu.Lock()
	defer lifetime.workspaceMu.Unlock()
	if lifetime.workspace == nil && lifetime.archiveMeasurements == 0 {
		return 0, nil
	}
	if lifetime.program != ProgramPhebs || lifetime.semanticMode != "" || (lifetime.producerID != 10 && lifetime.producerID != 11) ||
		lifetime.workspace == nil || lifetime.archiveMeasurements == 0 || lifetime.client == nil || lifetime.client.Context().Err() != nil {
		return 0, ErrProductionBootstrap
	}
	return lifetime.archiveMeasurements, nil
}

// ProductionArchiveMeasurement guards a selected backup's synchronous local
// workspace observation. The parent controls actual retired-engine ownership.
func ProductionArchiveMeasurement(ctx context.Context, measure func(context.Context) error) error {
	lifetime := productionRuntime.Load()
	if lifetime == nil || lifetime.producerID != 10 || lifetime.archiveMeasurement == nil || lifetime.archiveMeasurements == 0 {
		return ErrProductionBootstrap
	}
	if err := lifetime.archiveMeasurement.measure(ctx, measure); err != nil {
		return errors.Join(err, lifetime.client.fail(ErrIncomplete))
	}
	return nil
}
