// Package storeaccounting retains a parent-accepted logical store-submission
// prefix. It does not inspect SQL, attest a native transaction's start/commit,
// authenticate producers, or issue a frozen execution binding.
package storeaccounting

import (
	"context"
	"errors"
	"math"
	"sync"
)

const (
	MaximumPhases       = 15
	MaximumProducers    = 7
	MaximumCalls        = 40
	MaximumTransactions = 2
	MaximumRows         = 512
)

var (
	ErrConfig     = errors.New("store accounting configuration invalid")
	ErrDescriptor = errors.New("store accounting descriptor invalid")
	ErrProtocol   = errors.New("store accounting protocol invalid")
	ErrLimit      = errors.New("store accounting limit exceeded")
	ErrTransport  = errors.New("store accounting transport unavailable")
	ErrCanceled   = errors.New("store accounting canceled")
	ErrIncomplete = errors.New("store accounting incomplete")
	ErrFenced     = errors.New("store accounting fenced")
	ErrBusy       = errors.New("store accounting not quiescent")
)

// Producer and Phase are policy inputs, not admitted identities. The genuine
// owner must later bind their IDs, capacities and budgets to its frozen plan.
// Calls bounds tracked submissions; the SDK adapter must independently track
// ALL calls, including read-only tails that need no parent row event.
type Producer struct {
	ID           uint32
	Calls        int
	Transactions int
}

type Phase struct {
	ID           uint32
	Transactions uint64
	Rows         uint64
}

type Config struct {
	Producers []Producer
	Phases    []Phase
}

type Kind uint8

const (
	ImplicitWrite Kind = iota + 1
	Begin
	Append
	Commit
	Cancel
)

// Request describes the actual source submission immediately before its final
// parent ACK gate. It must already have a complete source-owned descriptor.
// Begin has zero rows and no transaction token. ImplicitWrite may have zero
// rows: an empty write-capable logical transaction still counts once. Append
// contributes positive submitted rows to an already accepted Begin. A zero-row
// in-transaction read/control call is tracked locally, not as a fake append.
type Request struct {
	Producer    uint32
	Phase       uint32
	Kind        Kind
	Transaction uint64
	Rows        uint64
}

// Submission tokens are controller-global ordinals, never native UUIDs or PIDs.
type Submission struct {
	Producer    uint32
	Ordinal     uint64
	Transaction uint64
}

type PhaseCount struct {
	Phase        uint32
	Transactions uint64
	Rows         uint64
	MaximumRows  uint64
}

type ProducerCount struct {
	Producer uint32
	// Ordinal is this producer's last controller-global submission ID, not
	// its submission count. Tokens cannot alias another producer's live UUID.
	Ordinal      uint64
	Calls        int
	Transactions int
	Checkpoint   uint32
	Attached     bool
	Closed       bool
}

// Complete means reducer closure only: the final phase is fenced and every
// configured lifetime closed without failure. Actual SDK/read-tail drain and
// authenticated phase handoff remain the future adapter/transport's duty.
type Snapshot struct {
	Transactions uint64
	Rows         uint64
	MaximumRows  uint64
	Phase        uint32
	Phases       []PhaseCount
	Producers    []ProducerCount
	Complete     bool
}

type transaction struct {
	ordinal uint64
	rows    uint64
	busy    bool
}

type call struct {
	ordinal     uint64
	kind        Kind
	transaction int
}

type producerState struct {
	config       Producer
	ordinal      uint64
	calls        [MaximumCalls]call
	transactions [MaximumTransactions]transaction
	checkpoint   uint32
	attached     bool
	closed       bool
}

// Controller retains only configured rows and live slots, never outcome or
// source history. It must not be copied. No method performs I/O or starts a
// goroutine; cancellation is observed at the next method call.
type Controller struct {
	// ponytail: one mutex over <=7 producers, 40 calls and 2 UUID slots each;
	// split only if measured contention warrants it. Never held across SDK I/O.
	mu            sync.Mutex
	ctx           context.Context
	ordinal       uint64
	producers     [MaximumProducers]producerState
	producerCount int
	phases        [MaximumPhases]Phase
	counts        [MaximumPhases]PhaseCount
	phaseCount    int
	phase         int
	fenced        bool
	transactions  uint64
	rows          uint64
	maximumRows   uint64
	err           error
}

func New(ctx context.Context, config Config) (*Controller, error) {
	if ctx == nil || ctx.Err() != nil || len(config.Producers) == 0 || len(config.Producers) > MaximumProducers ||
		len(config.Phases) == 0 || len(config.Phases) > MaximumPhases {
		return nil, ErrConfig
	}
	c := &Controller{producerCount: len(config.Producers), phaseCount: len(config.Phases)}
	for i, p := range config.Producers {
		if p.ID == 0 || p.Calls < 1 || p.Calls > MaximumCalls || p.Transactions < 1 || p.Transactions > MaximumTransactions {
			return nil, ErrConfig
		}
		for j := 0; j < i; j++ {
			if c.producers[j].config.ID == p.ID {
				return nil, ErrConfig
			}
		}
		c.producers[i].config = p
	}
	var transactions, rows uint64
	for i, p := range config.Phases {
		if p.ID == 0 || p.ID > MaximumPhases || (i > 0 && p.ID <= config.Phases[i-1].ID) ||
			p.Transactions > math.MaxUint64-transactions || p.Rows > math.MaxUint64-rows {
			return nil, ErrConfig
		}
		transactions += p.Transactions
		rows += p.Rows
		c.phases[i], c.counts[i] = p, PhaseCount{Phase: p.ID}
	}
	c.ctx = ctx
	return c, nil
}

func (c *Controller) failLocked(err error) error {
	if c.err == nil {
		c.err = err
	}
	return c.err
}

func (c *Controller) checkLocked() error {
	if c.ctx == nil {
		return ErrConfig
	}
	if c.err != nil {
		return c.err
	}
	if c.ctx.Err() != nil {
		return c.failLocked(ErrCanceled)
	}
	return nil
}

func (c *Controller) producerLocked(id uint32) *producerState {
	for i := 0; i < c.producerCount; i++ {
		if c.producers[i].config.ID == id {
			return &c.producers[i]
		}
	}
	return nil
}

func (c *Controller) Attach(producer, phase uint32) error {
	if c == nil {
		return ErrConfig
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkLocked(); err != nil {
		return err
	}
	p := c.producerLocked(producer)
	if c.fenced || phase != c.phases[c.phase].ID || p == nil || p.attached || p.closed {
		return c.failLocked(ErrProtocol)
	}
	p.attached = true
	return nil
}

func (c *Controller) Submit(request Request) (Submission, error) {
	if c == nil {
		return Submission{}, ErrConfig
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkLocked(); err != nil {
		return Submission{}, err
	}
	p := c.producerLocked(request.Producer)
	if p == nil || !p.attached || p.closed || request.Phase != c.phases[c.phase].ID || p.checkpoint == request.Phase {
		return Submission{}, c.failLocked(ErrProtocol)
	}
	if request.Kind < ImplicitWrite || request.Kind > Cancel ||
		((request.Kind == Begin || request.Kind == Commit || request.Kind == Cancel) && request.Rows != 0) ||
		(request.Kind == Append && request.Rows == 0) ||
		((request.Kind == Begin || request.Kind == ImplicitWrite) && request.Transaction != 0) ||
		(request.Kind >= Append && request.Transaction == 0) {
		return Submission{}, c.failLocked(ErrDescriptor)
	}
	if c.fenced && request.Kind != Commit && request.Kind != Cancel {
		return Submission{}, c.failLocked(ErrFenced)
	}
	callIndex, txIndex := -1, -1
	for i := 0; i < p.config.Calls; i++ {
		if p.calls[i].ordinal == 0 {
			callIndex = i
			break
		}
	}
	for i := 0; i < p.config.Transactions; i++ {
		if (request.Kind == Begin && p.transactions[i].ordinal == 0) ||
			(request.Transaction != 0 && p.transactions[i].ordinal == request.Transaction) {
			txIndex = i
			break
		}
	}
	if request.Kind >= Append && (txIndex < 0 || p.transactions[txIndex].busy) {
		return Submission{}, c.failLocked(ErrProtocol)
	}
	if callIndex < 0 || (request.Kind == Begin && txIndex < 0) || c.ordinal == math.MaxUint64 {
		return Submission{}, c.failLocked(ErrLimit)
	}
	addTransactions, txRows := uint64(0), request.Rows
	if request.Kind == Begin || request.Kind == ImplicitWrite {
		addTransactions = 1
	}
	if request.Kind == Append {
		if request.Rows > MaximumRows-p.transactions[txIndex].rows {
			return Submission{}, c.failLocked(ErrLimit)
		}
		txRows += p.transactions[txIndex].rows
	}
	budget, count := c.phases[c.phase], c.counts[c.phase]
	if txRows > MaximumRows || addTransactions > budget.Transactions-count.Transactions || request.Rows > budget.Rows-count.Rows {
		return Submission{}, c.failLocked(ErrLimit)
	}
	// This is the source-attempt linearization point, before any future ACK.
	// Every validation precedes it. Lost ACKs and failures cannot undo it.
	c.ordinal++
	p.ordinal = c.ordinal
	p.calls[callIndex] = call{ordinal: p.ordinal, kind: request.Kind, transaction: txIndex}
	result := Submission{Producer: request.Producer, Ordinal: p.ordinal, Transaction: request.Transaction}
	if request.Kind == Begin {
		p.transactions[txIndex] = transaction{ordinal: p.ordinal, busy: true}
		result.Transaction = p.ordinal
	} else if request.Kind >= Append {
		p.transactions[txIndex].busy = true
		if request.Kind == Append {
			p.transactions[txIndex].rows = txRows
		}
	}
	c.counts[c.phase].Transactions += addTransactions
	c.counts[c.phase].Rows += request.Rows
	c.counts[c.phase].MaximumRows = max(c.counts[c.phase].MaximumRows, txRows)
	c.transactions += addTransactions
	c.rows += request.Rows
	c.maximumRows = max(c.maximumRows, txRows)
	return result, nil
}

// Settle releases one classified completed call, not its counted attempt.
// Begin requires an actual successful reply and leaves its UUID reservation.
// Append/ImplicitWrite may settle after a definitive native SQL error; their
// attempted rows remain counted. Commit/Cancel require successful terminal
// replies. ANY uncertain reply or failed terminal operation must call Fail
// instead: this reducer intentionally has no reservation-erasure escape hatch.
func (c *Controller) Settle(submission Submission) error {
	if c == nil {
		return ErrConfig
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkLocked(); err != nil {
		return err
	}
	p := c.producerLocked(submission.Producer)
	if p == nil || !p.attached || p.closed || submission.Ordinal == 0 {
		return c.failLocked(ErrProtocol)
	}
	for i := 0; i < p.config.Calls; i++ {
		active := p.calls[i]
		if active.ordinal != submission.Ordinal {
			continue
		}
		wantTransaction := uint64(0)
		if active.transaction >= 0 {
			wantTransaction = p.transactions[active.transaction].ordinal
		}
		if submission.Transaction != wantTransaction {
			return c.failLocked(ErrProtocol)
		}
		if active.kind == Commit || active.kind == Cancel {
			p.transactions[active.transaction] = transaction{}
		} else if active.transaction >= 0 {
			p.transactions[active.transaction].busy = false
		}
		p.calls[i] = call{}
		return nil
	}
	return c.failLocked(ErrProtocol)
}

// Fail retains all live slots and positive counts, latches only a closed reason,
// and forbids further forwarding. Cleanup is owned outside this reducer.
func (c *Controller) Fail(reason error) error {
	if c == nil {
		return ErrConfig
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkLocked(); err != nil {
		return err
	}
	switch reason {
	case ErrDescriptor, ErrProtocol, ErrLimit, ErrTransport, ErrCanceled, ErrIncomplete:
	default:
		reason = ErrProtocol
	}
	return c.failLocked(reason)
}

func (c *Controller) Fence() error {
	if c == nil {
		return ErrConfig
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkLocked(); err != nil {
		return err
	}
	c.fenced = true
	return nil
}

func (p *producerState) busy() bool {
	for i := 0; i < p.config.Calls; i++ {
		if p.calls[i].ordinal != 0 {
			return true
		}
	}
	for i := 0; i < p.config.Transactions; i++ {
		if p.transactions[i].ordinal != 0 {
			return true
		}
	}
	return false
}

// Checkpoint requires a fence and no tracked call or UUID, with no carry.
// ErrBusy is nonterminal. The genuine adapter must first drain its read calls
// and semantic owners; this method alone cannot establish that precondition.
func (c *Controller) Checkpoint(producer, phase uint32) error {
	if c == nil {
		return ErrConfig
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkLocked(); err != nil {
		return err
	}
	p := c.producerLocked(producer)
	if !c.fenced || phase != c.phases[c.phase].ID || p == nil || !p.attached || p.closed || p.checkpoint == phase {
		return c.failLocked(ErrProtocol)
	}
	if p.busy() {
		return ErrBusy
	}
	p.checkpoint = phase
	return nil
}

func (c *Controller) Advance() error {
	if c == nil {
		return ErrConfig
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkLocked(); err != nil {
		return err
	}
	if !c.fenced || c.phase+1 >= c.phaseCount {
		return c.failLocked(ErrProtocol)
	}
	for i := 0; i < c.producerCount; i++ {
		p := &c.producers[i]
		if p.attached && !p.closed && p.checkpoint != c.phases[c.phase].ID {
			return ErrBusy
		}
	}
	c.phase++
	c.fenced = false
	return nil
}

// Close ends an attached producer lifetime only when all its tracked calls and
// UUIDs have settled. Future, never-attached lifetimes cannot be closed here.
func (c *Controller) Close(producer uint32) error {
	if c == nil {
		return ErrConfig
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkLocked(); err != nil {
		return err
	}
	p := c.producerLocked(producer)
	if p == nil || !p.attached || p.closed {
		return c.failLocked(ErrProtocol)
	}
	if p.busy() {
		return ErrBusy
	}
	p.closed = true
	return nil
}

func (c *Controller) Snapshot() (Snapshot, error) {
	if c == nil {
		return Snapshot{}, ErrConfig
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	err := c.checkLocked()
	out := Snapshot{Transactions: c.transactions, Rows: c.rows, MaximumRows: c.maximumRows,
		Phase: c.phases[c.phase].ID, Complete: err == nil && c.fenced && c.phase+1 == c.phaseCount,
		Phases:    append([]PhaseCount(nil), c.counts[:c.phaseCount]...),
		Producers: make([]ProducerCount, 0, c.producerCount)}
	for i := 0; i < c.producerCount; i++ {
		p := &c.producers[i]
		row := ProducerCount{Producer: p.config.ID, Ordinal: p.ordinal, Checkpoint: p.checkpoint, Attached: p.attached, Closed: p.closed}
		for j := 0; j < p.config.Calls; j++ {
			if p.calls[j].ordinal != 0 {
				row.Calls++
			}
		}
		for j := 0; j < p.config.Transactions; j++ {
			if p.transactions[j].ordinal != 0 {
				row.Transactions++
			}
		}
		out.Producers = append(out.Producers, row)
		out.Complete = out.Complete && p.closed
	}
	return out, err
}
