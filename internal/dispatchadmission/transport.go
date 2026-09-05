package dispatchadmission

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"os"
	"time"
)

// FrameBytes is the complete, fixed wire frame size, including its version.
// There are no length-prefixed allocations or caller-controlled text fields.
const FrameBytes = 64

const (
	opAdmit byte = iota + 1
	opSettle
	opCarry
	opCheckpoint
	opClose
)

type frame struct {
	op       byte
	phase    uint32
	site     uint32
	ordinal  uint64
	sequence uint64
	binding  [32]byte
}

func (f frame) encode() [FrameBytes]byte {
	var raw [FrameBytes]byte
	copy(raw[:4], "DA01")
	raw[4] = f.op
	binary.BigEndian.PutUint32(raw[8:12], f.phase)
	binary.BigEndian.PutUint32(raw[12:16], f.site)
	binary.BigEndian.PutUint64(raw[16:24], f.ordinal)
	binary.BigEndian.PutUint64(raw[24:32], f.sequence)
	copy(raw[32:], f.binding[:])
	return raw
}

func decode(raw [FrameBytes]byte) (frame, error) {
	if string(raw[:4]) != "DA01" || raw[4] < opAdmit || raw[4] > opClose || raw[5] != 0 || raw[6] != 0 || raw[7] != 0 {
		return frame{}, ErrProtocol
	}
	f := frame{op: raw[4], phase: binary.BigEndian.Uint32(raw[8:12]), site: binary.BigEndian.Uint32(raw[12:16]), ordinal: binary.BigEndian.Uint64(raw[16:24]), sequence: binary.BigEndian.Uint64(raw[24:32])}
	copy(f.binding[:], raw[32:])
	return f, nil
}

// adopt duplicates a socket into Go's network poller and closes the passed
// descriptor on every path. The duplicate is close-on-exec; native children
// inherit it only if a caller explicitly violates custody with ExtraFiles.
func adopt(file *os.File) (*net.UnixConn, error) {
	if file == nil {
		return nil, ErrConfig
	}
	if err := protectInheritance(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	conn, err := net.FileConn(file)
	closeErr := file.Close()
	if err != nil {
		return nil, ErrTransport
	}
	unix, ok := conn.(*net.UnixConn)
	if !ok || closeErr != nil {
		_ = conn.Close()
		return nil, ErrTransport
	}
	return unix, nil
}

func (c *Controller) attach(producer uint32, pid int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkLocked(); err != nil {
		return err
	}
	p := c.producers[producer]
	if p == nil || p.attached || p.closed || pid <= 0 {
		return c.failLocked(ErrProtocol)
	}
	p.attached = true
	p.pid = pid
	return nil
}

func (c *Controller) reservePair() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkLocked(); err != nil {
		return err
	}
	if c.limits.WireBytes-c.wireBytes < 2*FrameBytes {
		return c.failLocked(ErrLimit)
	}
	c.wireBytes += 2 * FrameBytes
	return nil
}

func (c *Controller) endStream(producer uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	p := c.producers[producer]
	p.eof = true
	if p.closed || p.hardDeath {
		return c.err
	}
	return c.failLocked(ErrIncomplete)
}

// Serve binds one parent-owned endpoint to a configured producer and its owned
// successful-start PID (or the declared controller root). It adopts/closes file
// and blocks until terminal closure or failure. Call it once per producer.
// Idle reads need no activity heartbeat; partial frames and ACK writes have the
// frozen timeout. Both context cancellation and controller failure close the FD.
func (c *Controller) Serve(ctx context.Context, producer uint32, pid int, file *os.File) (err error) {
	if ctx == nil || ctx.Err() != nil {
		if file != nil {
			_ = file.Close()
		}
		return c.fail(ErrCanceled)
	}
	conn, err := adopt(file)
	if err != nil {
		return c.fail(err)
	}
	defer func() {
		_ = conn.Close()
		if recover() != nil {
			err = c.fail(ErrPanic)
		}
	}()
	if err := c.attach(producer, pid); err != nil {
		return err
	}
	stopContext := context.AfterFunc(ctx, func() { _ = conn.Close() })
	stopController := context.AfterFunc(c.ctx, func() { _ = conn.Close() })
	defer stopContext()
	defer stopController()
	for {
		if err := c.reservePair(); err != nil {
			return err
		}
		if err := conn.SetDeadline(time.Time{}); err != nil {
			return c.fail(ErrTransport)
		}
		var raw [FrameBytes]byte
		_, err := io.ReadFull(conn, raw[:1])
		if err == io.EOF {
			return c.endStream(producer)
		}
		if err != nil {
			return c.fail(ErrTransport)
		}
		if err := conn.SetDeadline(time.Now().Add(c.limits.AckTimeout)); err != nil {
			return c.fail(ErrTransport)
		}
		_, err = io.ReadFull(conn, raw[1:])
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return c.endStream(producer)
		}
		if err != nil {
			return c.fail(ErrTransport)
		}
		request, err := decode(raw)
		if err != nil {
			return c.fail(err)
		}
		if err := c.accept(producer, request); err != nil {
			if err == errTerminating {
				continue
			}
			return err
		}
		// This exact echo is the ACK. Controller state is already committed.
		if n, err := conn.Write(raw[:]); err != nil || n != len(raw) {
			return c.fail(ErrTransport)
		}
		if request.op == opClose {
			return nil
		}
	}
}
