package indexer

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"os/exec"
	"strconv"
	"sync"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

const IndexOfferEnvironment = dispatchadmission.IndexOfferEnvironment

type indexOfferKey struct{}

// IndexOfferEvent comes only from the selected native child's stream.
// Begin/offers occur while it runs; terminal events require its joined Wait.
// An offer is pre-go-git submission, not a successful read or indexed document.
type IndexOfferEvent struct {
	Kind  byte // b=begin, i=actual offer, e/f=successful/failed checked end
	Count uint64
}

func WithIndexOfferObserver(ctx context.Context, sink func(IndexOfferEvent) error) (context.Context, error) {
	if ctx == nil || sink == nil || ctx.Value(indexOfferKey{}) != nil {
		return nil, errors.New("index offer observer unavailable")
	}
	return context.WithValue(ctx, indexOfferKey{}, sink), nil
}

type indexOfferOutput struct {
	mu                     sync.Mutex
	ctx                    context.Context
	diagnostic             io.Writer
	sink                   func(IndexOfferEvent) error
	pending                []byte
	ordinary               bool
	begun, ended, finished bool
	count                  uint64
	err                    error
}

func selectedIndexOfferOutput(ctx context.Context, diagnostic io.Writer, focused bool) (*indexOfferOutput, error) {
	var sink func(IndexOfferEvent) error
	if ctx != nil {
		sink, _ = ctx.Value(indexOfferKey{}).(func(IndexOfferEvent) error)
	}
	if focused || sink == nil || diagnostic == nil || ctx.Err() != nil {
		return nil, dispatchadmission.FailProductionIndexObservation()
	}
	return &indexOfferOutput{ctx: ctx, diagnostic: diagnostic, sink: sink}, nil
}

func (out *indexOfferOutput) refuse() error {
	out.err = dispatchadmission.FailProductionIndexObservation()
	return out.err
}

func (out *indexOfferOutput) emit(kind byte, count uint64) (err error) {
	defer func() {
		if recover() != nil {
			err = out.refuse()
		}
	}()
	if out.ctx.Err() != nil || out.sink(IndexOfferEvent{Kind: kind, Count: count}) != nil || out.ctx.Err() != nil {
		return out.refuse()
	}
	return nil
}

// Stdout/stderr continue to share one existing os/exec pipe. Native tokens are
// intercepted before the advisory tail/sanitizer. Long unrelated log lines
// stream through with bounded pending memory; no new ordinary line limit.
func (out *indexOfferOutput) Write(p []byte) (int, error) {
	out.mu.Lock()
	defer out.mu.Unlock()
	if out.err != nil {
		return 0, out.err
	}
	written := len(p)
	for len(p) > 0 {
		limit := min(len(p), maxVerboseLineBytes+1-len(out.pending))
		n := bytes.IndexByte(p[:limit], '\n') + 1
		if n == 0 {
			n = limit
		}
		part := p[:n]
		p = p[n:]
		if out.ordinary {
			if _, err := out.diagnostic.Write(part); err != nil {
				return 0, out.refuse()
			}
		} else {
			out.pending = append(out.pending, part...)
			if len(out.pending) > maxVerboseLineBytes {
				if bytes.HasPrefix(out.pending, []byte("ZI")) {
					return 0, out.refuse()
				}
				if _, err := out.diagnostic.Write(out.pending); err != nil {
					return 0, out.refuse()
				}
				out.pending = out.pending[:0]
				out.ordinary = true
			}
		}
		if part[len(part)-1] != '\n' {
			continue
		}
		if !out.ordinary {
			if err := out.line(out.pending); err != nil {
				return 0, err
			}
		}
		out.pending, out.ordinary = out.pending[:0], false
	}
	return written, nil
}

func (out *indexOfferOutput) line(line []byte) error {
	switch {
	case bytes.Equal(line, []byte("ZIB1\n")):
		if out.begun || out.ended {
			return out.refuse()
		}
		out.begun = true
		return out.emit('b', 0)
	case bytes.Equal(line, []byte("ZI1\n")):
		if !out.begun || out.ended || out.count == math.MaxUint64 {
			return out.refuse()
		}
		out.count++
		return out.emit('i', 1)
	case bytes.HasPrefix(line, []byte("ZIE1:")):
		value := string(line[5 : len(line)-1])
		count, err := strconv.ParseUint(value, 10, 64)
		if !out.begun || out.ended || err != nil || strconv.FormatUint(count, 10) != value || count != out.count {
			return out.refuse()
		}
		out.ended = true
		// End is forwarded only after joined Wait and a closed error-tree check.
		return nil
	case bytes.HasPrefix(line, []byte("ZI")):
		return out.refuse()
	default:
		_, err := out.diagnostic.Write(line)
		if err != nil {
			return out.refuse()
		}
		return nil
	}
}

func (out *indexOfferOutput) finish(runErr error) error {
	out.mu.Lock()
	defer out.mu.Unlock()
	if out.err != nil || out.finished || !out.begun || !out.ended || len(out.pending) != 0 || out.ordinary {
		return out.refuse()
	}
	kind := byte('e')
	if runErr != nil {
		if !indexOfferExitFailure(runErr) {
			return out.refuse()
		}
		kind = 'f'
	}
	out.finished = true
	return out.emit(kind, out.count)
}

// A normally exited child preserves the original worker retry behavior.
// Missing termination (including hard death), transport or admission errors
// remain unavailable telemetry, even when complete positive tokens survived.
func indexOfferExitFailure(err error) bool {
	switch value := err.(type) {
	case *exec.ExitError:
		return value != nil && value.ProcessState != nil && value.ExitCode() > 0
	case interface{ Unwrap() []error }:
		children := value.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !indexOfferExitFailure(child) {
				return false
			}
		}
		return true
	case interface{ Unwrap() error }:
		return indexOfferExitFailure(value.Unwrap())
	default:
		return false
	}
}
