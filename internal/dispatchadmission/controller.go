// Package dispatchadmission counts permission to attempt explicitly controlled
// dispatches. It does not attest input admission, process births, native helper
// history, process-session custody, or successful command outcomes.
package dispatchadmission

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"sync"
	"time"
)

var (
	ErrConfig      = errors.New("dispatch admission configuration invalid")
	ErrProtocol    = errors.New("dispatch admission protocol invalid")
	ErrLimit       = errors.New("dispatch admission limit exceeded")
	ErrTransport   = errors.New("dispatch admission transport unavailable")
	ErrCanceled    = errors.New("dispatch admission canceled")
	ErrFenced      = errors.New("dispatch admission fenced")
	ErrBusy        = errors.New("dispatch admission not quiescent")
	ErrIncomplete  = errors.New("dispatch admission incomplete")
	ErrPanic       = errors.New("dispatch admission panic")
	errTerminating = errors.New("dispatch producer terminating")
)

// Site and all other numeric IDs are closed, source-owned tags. Binding binds
// a producer lifetime to its already-admitted parent endpoint, not a PID claim.
type Site struct {
	ID         uint32
	Role       uint32
	Persistent bool
}

type Producer struct {
	ID      uint32
	Binding [32]byte
	Sites   []Site
}

type RoleBudget struct {
	Role     uint32
	Attempts uint64
}

type Phase struct {
	ID    uint32
	Roles []RoleBudget
}

// Limits are supplied by the owning frozen flow, not inferred by this package.
// WireBytes bounds reservations: each request reserves one fixed request and
// one fixed ACK before reading. An idle/EOF receive can retain one unused pair
// per producer. No limit includes command output, disk, or native process RSS.
type Limits struct {
	Producers         int
	Sites             int
	Roles             int
	Phases            int
	ActivePerProducer int
	Attempts          uint64
	WireBytes         uint64
	AckTimeout        time.Duration
}

type Config struct {
	Limits    Limits
	Producers []Producer
	Phases    []Phase
}

type RoleCount struct {
	Role     uint32
	Attempts uint64
}

type PhaseCount struct {
	Phase    uint32
	Attempts uint64
	Roles    []RoleCount
}

type ProducerCount struct {
	Producer   uint32
	Ordinal    uint64
	Active     int
	Checkpoint uint32
	// Attached means the controller bound an endpoint, not that a process was
	// born. Closed with Attached=false denotes an irrevocably unused prefix.
	Attached bool
	Closed   bool
}

// Snapshot is only a controller-committed prefix. Complete additionally requires
// closure of every configured producer and a final fence, without any failure.
type Snapshot struct {
	Attempts          uint64
	Digest            [32]byte
	ReservedWireBytes uint64
	Phases            []PhaseCount
	Producers         []ProducerCount
	Complete          bool
}

type activeDispatch struct {
	persistent bool
	carried    bool
}

type producerState struct {
	binding    [32]byte
	sites      map[uint32]Site
	ordinal    uint64
	sequence   uint64
	active     map[uint64]activeDispatch
	checkpoint uint32
	attached   bool
	pid        int
	eof        bool
	closed     bool
	hardDeath  bool
}

// Controller retains only bounded configured rows and currently active tokens.
// It must not be copied. Its context is canceled by its first failure. It owns
// no command, disk file, environment lookup, or autonomous background loop.
type Controller struct {
	// ponytail: one bounded-state mutex; split only after measured contention.
	// It is never held across transport I/O, callbacks, or child execution/wait.
	mu            sync.Mutex
	ctx           context.Context
	cancel        context.CancelCauseFunc
	limits        Limits
	producers     map[uint32]*producerState
	producerOrder []uint32
	phases        []Phase
	counts        []PhaseCount
	phase         int
	fenced        bool
	attempts      uint64
	wireBytes     uint64
	digest        [32]byte
	err           error
}

func New(ctx context.Context, config Config) (*Controller, error) {
	l := config.Limits
	if ctx == nil || ctx.Err() != nil || l.Producers < 1 || l.Sites < 1 || l.Roles < 1 || l.Phases < 1 ||
		l.ActivePerProducer < 1 || l.Attempts == 0 || l.Attempts == math.MaxUint64 ||
		l.WireBytes < 2*FrameBytes || l.AckTimeout <= 0 ||
		len(config.Producers) == 0 || len(config.Producers) > l.Producers ||
		len(config.Phases) == 0 || len(config.Phases) > l.Phases {
		return nil, ErrConfig
	}
	c := &Controller{limits: l, producers: make(map[uint32]*producerState)}
	roles := make(map[uint32]bool)
	bindings := make(map[[32]byte]bool)
	sites := 0
	for _, p := range config.Producers {
		if p.ID == 0 || p.Binding == ([32]byte{}) || bindings[p.Binding] || c.producers[p.ID] != nil || len(p.Sites) == 0 || len(p.Sites) > l.Sites-sites {
			return nil, ErrConfig
		}
		bindings[p.Binding] = true
		state := &producerState{binding: p.Binding, sites: make(map[uint32]Site), active: make(map[uint64]activeDispatch)}
		for _, site := range p.Sites {
			if site.ID == 0 || site.Role == 0 || state.sites[site.ID].ID != 0 {
				return nil, ErrConfig
			}
			state.sites[site.ID] = site
			roles[site.Role] = true
		}
		sites += len(p.Sites)
		c.producers[p.ID] = state
		c.producerOrder = append(c.producerOrder, p.ID)
	}
	if len(roles) > l.Roles {
		return nil, ErrConfig
	}
	phaseIDs := make(map[uint32]bool)
	for _, p := range config.Phases {
		if p.ID == 0 || phaseIDs[p.ID] || len(p.Roles) != len(roles) {
			return nil, ErrConfig
		}
		phaseIDs[p.ID] = true
		seen := make(map[uint32]bool)
		count := PhaseCount{Phase: p.ID}
		for _, r := range p.Roles {
			if !roles[r.Role] || seen[r.Role] || r.Attempts > l.Attempts {
				return nil, ErrConfig
			}
			seen[r.Role] = true
			count.Roles = append(count.Roles, RoleCount{Role: r.Role})
		}
		c.phases = append(c.phases, Phase{ID: p.ID, Roles: append([]RoleBudget(nil), p.Roles...)})
		c.counts = append(c.counts, count)
	}
	c.ctx, c.cancel = context.WithCancelCause(ctx)
	return c, nil
}

func (c *Controller) Context() context.Context { return c.ctx }

func (c *Controller) failLocked(err error) error {
	if c.err == nil {
		c.err = err
		c.cancel(err)
	}
	return c.err
}

func (c *Controller) fail(err error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.failLocked(err)
}

func (c *Controller) checkLocked() error {
	if c.err != nil {
		return c.err
	}
	if c.ctx.Err() != nil {
		return c.failLocked(ErrCanceled)
	}
	return nil
}

// Fence stops further admission without preventing settlement/checkpoints.
// Owners must also stop their work sources before checkpointing producers.
func (c *Controller) Fence() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkLocked(); err != nil {
		return err
	}
	c.fenced = true
	return nil
}

// Advance reopens only the next configured phase. ErrBusy is nonterminal: the
// owner can await the outstanding bounded checkpoints and try again.
func (c *Controller) Advance() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkLocked(); err != nil {
		return err
	}
	if !c.fenced || c.phase+1 >= len(c.phases) {
		return c.failLocked(ErrProtocol)
	}
	for _, p := range c.producers {
		if p.attached && !p.closed && (p.hardDeath || p.checkpoint != c.phases[c.phase].ID) {
			return ErrBusy
		}
	}
	c.phase++
	c.fenced = false
	return nil
}

// ExpectHardDeath must precede intentional producer termination. It fences that
// producer and permits EOF to await actual owned Wait evidence, never to imply
// successful termination or descendant/session closure by itself.
func (c *Controller) ExpectHardDeath(producer uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkLocked(); err != nil {
		return err
	}
	p := c.producers[producer]
	if !c.fenced || p == nil || !p.attached || p.closed || p.hardDeath {
		return c.failLocked(ErrProtocol)
	}
	p.hardDeath = true
	return nil
}

// CancelUnused irrevocably closes a configured, never-attached producer's empty
// admission prefix. It requires a controller fence and prevents every later
// attach/admission for that lifetime. Unattached does not mean no process was
// born: the launcher separately owns endpoints, attempted producer starts,
// native joins, sessions and custody. No command outcome is invented here.
func (c *Controller) CancelUnused(producer uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkLocked(); err != nil {
		return err
	}
	p := c.producers[producer]
	if !c.fenced || p == nil || p.attached || p.closed {
		return c.failLocked(ErrProtocol)
	}
	p.closed = true
	return nil
}

// CloseHardDeath accepts only native Wait evidence for the exact PID attached
// by Serve, after EOF. ProcessState is produced by os/exec or os.Process.Wait;
// this package does not accept a boolean "joined" assertion. The launcher still
// owns session/lease/custody checks outside this admission-prefix closure.
func (c *Controller) CloseHardDeath(producer uint32, waited *os.ProcessState) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkLocked(); err != nil {
		return err
	}
	p := c.producers[producer]
	if !c.fenced || p == nil || !p.hardDeath || !p.eof || p.closed || waited == nil || waited.Pid() != p.pid {
		return c.failLocked(ErrIncomplete)
	}
	clear(p.active)
	p.closed = true
	return nil
}

func (c *Controller) Snapshot() (Snapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	err := c.checkLocked()
	out := Snapshot{Attempts: c.attempts, Digest: c.digest, ReservedWireBytes: c.wireBytes, Complete: c.fenced && err == nil}
	for _, row := range c.counts {
		row.Roles = append([]RoleCount(nil), row.Roles...)
		out.Phases = append(out.Phases, row)
	}
	for _, id := range c.producerOrder {
		p := c.producers[id]
		out.Producers = append(out.Producers, ProducerCount{Producer: id, Ordinal: p.ordinal, Active: len(p.active), Checkpoint: p.checkpoint, Attached: p.attached, Closed: p.closed})
		out.Complete = out.Complete && p.closed
	}
	return out, err
}

func (c *Controller) accept(producer uint32, frame frame) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkLocked(); err != nil {
		return err
	}
	p := c.producers[producer]
	if p != nil && p.hardDeath {
		return errTerminating
	}
	if p == nil || !p.attached || p.closed || frame.binding != p.binding ||
		p.sequence == math.MaxUint64 || frame.sequence != p.sequence+1 {
		return c.failLocked(ErrProtocol)
	}
	// A carried handle may finish between global Advance and local Resume.
	// Settlement changes no phase count and may use that producer's last fence.
	if frame.phase != c.phases[c.phase].ID && (frame.op != opSettle || frame.phase != p.checkpoint) {
		return c.failLocked(ErrProtocol)
	}
	p.sequence++
	switch frame.op {
	case opAdmit:
		site, ok := p.sites[frame.site]
		if !ok || p.ordinal == math.MaxUint64 || frame.ordinal != p.ordinal+1 || p.checkpoint == frame.phase {
			return c.failLocked(ErrProtocol)
		}
		if c.fenced {
			return c.failLocked(ErrFenced)
		}
		if len(p.active) >= c.limits.ActivePerProducer || c.attempts >= c.limits.Attempts {
			return c.failLocked(ErrLimit)
		}
		role := -1
		for i, r := range c.phases[c.phase].Roles {
			if r.Role == site.Role {
				role = i
				break
			}
		}
		if role < 0 || c.counts[c.phase].Roles[role].Attempts >= c.phases[c.phase].Roles[role].Attempts {
			return c.failLocked(ErrLimit)
		}
		// Linearization point precedes ACK. No subsequent failure rolls it back.
		p.ordinal++
		p.active[frame.ordinal] = activeDispatch{persistent: site.Persistent}
		c.attempts++
		c.counts[c.phase].Attempts++
		c.counts[c.phase].Roles[role].Attempts++
		raw := frame.encode()
		var event [104]byte
		copy(event[:32], c.digest[:])
		copy(event[32:96], raw[:])
		binary.BigEndian.PutUint32(event[96:100], producer)
		binary.BigEndian.PutUint32(event[100:], site.Role)
		c.digest = sha256.Sum256(event[:])
	case opSettle, opCarry:
		active, ok := p.active[frame.ordinal]
		if !ok || frame.site != 0 {
			return c.failLocked(ErrProtocol)
		}
		if frame.op == opSettle {
			delete(p.active, frame.ordinal)
		} else {
			if !active.persistent || active.carried {
				return c.failLocked(ErrProtocol)
			}
			active.carried = true
			p.active[frame.ordinal] = active
		}
	case opCheckpoint, opClose:
		// Terminal closure fences only this producer. A serial one-shot may
		// finish while other producers still work in the same global phase.
		// Checkpoints and Advance retain their global fence requirement.
		if frame.site != 0 || frame.ordinal != p.ordinal ||
			(frame.op == opCheckpoint && (!c.fenced || p.checkpoint == frame.phase)) {
			return c.failLocked(ErrProtocol)
		}
		for _, active := range p.active {
			if frame.op == opClose || !active.persistent || !active.carried {
				return c.failLocked(ErrBusy)
			}
		}
		p.checkpoint = frame.phase
		p.closed = frame.op == opClose
	default:
		return c.failLocked(ErrProtocol)
	}
	return nil
}
