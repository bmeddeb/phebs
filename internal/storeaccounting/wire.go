package storeaccounting

import (
	"encoding/binary"
	"math"
	"math/bits"
	"time"
)

const FrameBytes = 64
const pairBytes = 2 * FrameBytes
const replyBit = 0x80

const (
	opAttach byte = iota + 1
	opSubmit
	opSettle
	opCheckpoint
	opClose
	opFail
)

// WireProducer binds one mechanical socket lifetime. Neither its nonce nor its
// phase mask attests a launched executable, source descriptor, or frozen plan.
// Bit zero denotes phase one. The actual owner supplies the source-owned flow.
type WireProducer struct {
	ID      uint32
	Binding [32]byte
	Phases  uint16
}

type WireConfig struct {
	Producers  []WireProducer
	AckTimeout time.Duration
}

// ClientConfig is the immutable view returned with the actual child endpoint.
// It is an explicit constructor input, not ambient bootstrap or an issuer.
type ClientConfig struct {
	Producer     uint32
	Binding      [32]byte
	Phase        uint32
	Phases       uint16
	Calls        int
	Transactions int
	WireBytes    uint64
	AckTimeout   time.Duration
}

type wireFrame struct {
	op       byte
	kind     byte
	rows     uint16
	phase    uint32
	sequence uint64
	token    uint64
	binding  [32]byte
}

func (f wireFrame) encode() [FrameBytes]byte {
	var raw [FrameBytes]byte
	copy(raw[:4], "SA01")
	raw[4], raw[5] = f.op, f.kind
	binary.BigEndian.PutUint16(raw[6:8], f.rows)
	binary.BigEndian.PutUint32(raw[8:12], f.phase)
	binary.BigEndian.PutUint64(raw[16:24], f.sequence)
	binary.BigEndian.PutUint64(raw[24:32], f.token)
	copy(raw[32:], f.binding[:])
	return raw
}

func decodeFrame(raw [FrameBytes]byte, reply bool) (wireFrame, error) {
	op := raw[4]
	if reply {
		if op&replyBit == 0 {
			return wireFrame{}, ErrProtocol
		}
		op &^= replyBit
	}
	if string(raw[:4]) != "SA01" || op < opAttach || op > opFail ||
		raw[12] != 0 || raw[13] != 0 || raw[14] != 0 || raw[15] != 0 {
		return wireFrame{}, ErrProtocol
	}
	f := wireFrame{op: raw[4], kind: raw[5], rows: binary.BigEndian.Uint16(raw[6:8]),
		phase: binary.BigEndian.Uint32(raw[8:12]), sequence: binary.BigEndian.Uint64(raw[16:24]),
		token: binary.BigEndian.Uint64(raw[24:32])}
	copy(f.binding[:], raw[32:])
	if f.phase == 0 || f.phase > MaximumPhases || f.sequence == 0 || f.rows > MaximumRows {
		return wireFrame{}, ErrProtocol
	}
	switch op {
	case opAttach, opCheckpoint, opClose:
		if f.kind != 0 || f.rows != 0 || f.token != 0 {
			return wireFrame{}, ErrProtocol
		}
	case opSettle:
		if f.kind != 0 || f.rows != 0 || f.token == 0 {
			return wireFrame{}, ErrProtocol
		}
	case opSubmit:
		if f.kind < byte(ImplicitWrite) || f.kind > byte(Cancel) {
			return wireFrame{}, ErrProtocol
		}
	case opFail:
		if f.kind < 1 || f.kind > 6 || f.rows != 0 || f.token != 0 {
			return wireFrame{}, ErrProtocol
		}
	}
	return f, nil
}

func wireProducerID(id uint32) bool { return id >= 2 && id <= 6 || id == 10 || id == 11 }

func phaseIn(mask uint16, phase uint32) bool {
	return phase > 0 && phase <= MaximumPhases && mask&(1<<(phase-1)) != 0
}

// A<=min(R,512*B), I+B<=T and terminal calls<=B, hence accepted submissions
// <=2*T+min(R,512*T). Each needs a Submit pair and a Settle pair. Control
// allowance includes checkpoints plus Attach, Close, one Fail and one unused
// failed/EOF receive reservation per lifetime. Reservations are not allocation
// or expected traffic, and this conservative bound is not a frozen issuer.
func wireBudget(transactions, rows, checkpoints, producers uint64) (uint64, error) {
	appends := rows
	if transactions <= math.MaxUint64/MaximumRows {
		appends = min(appends, transactions*MaximumRows)
	}
	if transactions > math.MaxUint64/4 || appends > math.MaxUint64/2 || producers > math.MaxUint64/4 {
		return 0, ErrConfig
	}
	pairs := 4 * transactions
	for _, extra := range [...]uint64{2 * appends, checkpoints, 4 * producers} {
		if extra > math.MaxUint64-pairs {
			return 0, ErrConfig
		}
		pairs += extra
	}
	if pairs > math.MaxUint64/pairBytes {
		return 0, ErrConfig
	}
	return pairs * pairBytes, nil
}

func (c *Controller) wireConfiguration(config WireConfig) ([MaximumProducers]ClientConfig, uint64, error) {
	var clients [MaximumProducers]ClientConfig
	if c == nil || config.AckTimeout <= 0 || len(config.Producers) == 0 {
		return clients, 0, ErrConfig
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.checkLocked() != nil || c.wireOwned || c.ordinal != 0 || c.fenced || c.phase != 0 || len(config.Producers) != c.producerCount {
		return clients, 0, ErrConfig
	}
	var known uint16
	var totalTransactions, totalRows, checkpoints uint64
	for i := 0; i < c.phaseCount; i++ {
		phase := c.phases[i]
		known |= 1 << (phase.ID - 1)
		totalTransactions += phase.Transactions // New already validated both sums.
		totalRows += phase.Rows
	}
	for i, input := range config.Producers {
		p := c.producerLocked(input.ID)
		if !wireProducerID(input.ID) || p == nil || p.attached || p.closed ||
			input.Binding == ([32]byte{}) || input.Phases == 0 || input.Phases & ^known != 0 {
			return clients, 0, ErrConfig
		}
		for j := 0; j < i; j++ {
			if clients[j].Producer == input.ID || clients[j].Binding == input.Binding {
				return clients, 0, ErrConfig
			}
		}
		var transactions, rows uint64
		for j := 0; j < c.phaseCount; j++ {
			phase := c.phases[j]
			if phaseIn(input.Phases, phase.ID) {
				transactions += phase.Transactions
				rows += phase.Rows
			}
		}
		count := uint64(bits.OnesCount16(input.Phases))
		wire, err := wireBudget(transactions, rows, count, 1)
		if err != nil {
			return clients, 0, err
		}
		checkpoints += count
		clients[i] = ClientConfig{Producer: input.ID, Binding: input.Binding, Phases: input.Phases,
			Calls: p.config.Calls, Transactions: p.config.Transactions, WireBytes: wire, AckTimeout: config.AckTimeout}
	}
	global, err := wireBudget(totalTransactions, totalRows, checkpoints, uint64(c.producerCount))
	if err == nil {
		for _, input := range config.Producers {
			c.producerLocked(input.ID).lastPhase = uint32(bits.Len16(input.Phases))
		}
		c.wireOwned = true
	}
	return clients, global, err
}

func (c *Controller) wirePhase() (uint32, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkLocked(); err != nil {
		return 0, err
	}
	return c.phases[c.phase].ID, nil
}

// Retrieve the actual reducer-owned call, never a caller-supplied UUID or a
// second transport map. Settle subsequently revalidates under its own lock.
func (c *Controller) wireSubmission(producer uint32, ordinal uint64) (Submission, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkLocked(); err != nil {
		return Submission{}, err
	}
	p := c.producerLocked(producer)
	if p == nil || ordinal == 0 {
		return Submission{}, c.failLocked(ErrProtocol)
	}
	for i := 0; i < p.config.Calls; i++ {
		active := p.calls[i]
		if active.ordinal == ordinal {
			out := Submission{Producer: producer, Ordinal: ordinal}
			if active.transaction >= 0 {
				out.Transaction = p.transactions[active.transaction].ordinal
			}
			return out, nil
		}
	}
	return Submission{}, c.failLocked(ErrProtocol)
}

func failureKind(reason error) byte {
	for i, known := range [...]error{ErrDescriptor, ErrProtocol, ErrLimit, ErrTransport, ErrCanceled, ErrIncomplete} {
		if reason == known {
			return byte(i + 1)
		}
	}
	return 2
}

func failureReason(kind byte) error {
	if kind < 1 || kind > 6 {
		return ErrProtocol
	}
	return [...]error{ErrDescriptor, ErrProtocol, ErrLimit, ErrTransport, ErrCanceled, ErrIncomplete}[kind-1]
}
