package readaccounting

import "context"

// TransitionReadSide and Stage are private locations, never acceptance proof.
type TransitionReadSide uint8

const (
	TransitionReadSideUnknown TransitionReadSide = iota
	TransitionReadBefore
	TransitionReadAfter
)

func (side TransitionReadSide) String() string {
	switch side {
	case TransitionReadBefore:
		return "before"
	case TransitionReadAfter:
		return "after"
	default:
		return "unknown"
	}
}

type TransitionReadStage uint8

const (
	TransitionReadInput TransitionReadStage = iota + 1
	TransitionReadScheduleQuery
	TransitionReadScheduleFence
	TransitionReadChunkCharge
	TransitionReadChunkQuery
	TransitionReadChunkFence
	TransitionReadRecordID
	TransitionReadTuple
	TransitionReadShape
)

func (stage TransitionReadStage) String() string {
	names := [...]string{"unknown", "input", "schedule_query", "schedule_fence", "chunk_charge", "chunk_query", "chunk_fence", "record_id", "tuple", "shape"}
	if int(stage) >= len(names) {
		return names[0]
	}
	return names[stage]
}

// Zero means not reached. Cardinalities deliberately expose no exact count.
type TransitionReadCardinality uint8

const (
	TransitionReadCountUnknown TransitionReadCardinality = iota
	TransitionReadCountZero
	TransitionReadCountOne
	TransitionReadCountMany
)

func TransitionReadCount(count int) TransitionReadCardinality {
	if count == 0 {
		return TransitionReadCountZero
	}
	if count == 1 {
		return TransitionReadCountOne
	}
	return TransitionReadCountMany
}
func (count TransitionReadCardinality) String() string {
	switch count {
	case TransitionReadCountZero:
		return "zero"
	case TransitionReadCountOne:
		return "one"
	case TransitionReadCountMany:
		return "many"
	default:
		return "unknown"
	}
}

type TransitionReadIDKind uint8

const (
	TransitionReadIDUnknown TransitionReadIDKind = iota
	TransitionReadIDMissing
	TransitionReadIDWrongTable
	TransitionReadIDWrongType
	TransitionReadIDValid
)

func (kind TransitionReadIDKind) String() string {
	names := [...]string{"unknown", "missing", "wrong_table", "wrong_type", "valid"}
	if int(kind) >= len(names) {
		return names[0]
	}
	return names[kind]
}

type TransitionReadFailureFlags struct {
	Envelope, Fence                                                        TransitionReadCardinality
	CurrentChecked, CurrentMatches                                         bool
	SQLAvailable, SQLChunkPresent, SQLRecordIDPresent, SQLRecordIDIsRecord bool
	DecodedID                                                              TransitionReadIDKind
}
type TransitionReadFailureObserver func(TransitionReadSide, TransitionReadStage, error, TransitionReadFailureFlags)
type transitionReadFailureKey struct{}
type transitionReadSideKey struct{}

func HasTransitionReadFailureObserver(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	observe, _ := ctx.Value(transitionReadFailureKey{}).(TransitionReadFailureObserver)
	return observe != nil
}

// Optional, non-replacing diagnostics carry neither a ledger nor authority.
func WithTransitionReadFailureObserver(ctx context.Context, observe TransitionReadFailureObserver) context.Context {
	if ctx == nil || observe == nil || ctx.Value(transitionReadFailureKey{}) != nil {
		return ctx
	}
	return context.WithValue(ctx, transitionReadFailureKey{}, observe)
}

// Ordinary calls retain the exact context and allocate no side binding.
func WithTransitionReadSide(ctx context.Context, side TransitionReadSide) context.Context {
	if !HasTransitionReadFailureObserver(ctx) {
		return ctx
	}
	return context.WithValue(ctx, transitionReadSideKey{}, side)
}
func NoteTransitionReadFailure(ctx context.Context, stage TransitionReadStage, err error, flags TransitionReadFailureFlags) {
	if ctx == nil || err == nil {
		return
	}
	observe, _ := ctx.Value(transitionReadFailureKey{}).(TransitionReadFailureObserver)
	if observe == nil {
		return
	}
	side, _ := ctx.Value(transitionReadSideKey{}).(TransitionReadSide)
	defer func() { _ = recover() }()
	observe(side, stage, err, flags)
}
