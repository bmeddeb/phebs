package lifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/bmeddeb/phebs/internal/store"
)

const (
	TerminalJobAge   = 30 * 24 * time.Hour
	TerminalJobCount = int64(100_000)
)

var lifecycleJobKinds = []store.JobKind{
	store.JobSync,
	store.JobIndex,
	store.JobFetch,
	store.JobCandidate,
	store.JobExtract,
	store.JobResolverCatalog,
	store.JobCallerLeaf,
	store.JobInvestigate,
}

type JobStore interface {
	ScanJobLifecycle(context.Context, store.JobKind, string, int) (store.JobLifecyclePage, error)
	DeleteOldestTerminalJobs(context.Context, store.JobKind, *time.Time, int) (int, error)
}

type JobOwnerImpl struct {
	Store   JobStore
	Acquire func(context.Context) (func(), error)
}

func (JobOwnerImpl) Name() string { return JobOwner }

type jobCursor struct {
	Kind      int    `json:"kind"`
	Phase     string `json:"phase"`
	After     string `json:"after,omitempty"`
	Count     int64  `json:"count,omitempty"`
	Remaining int64  `json:"remaining,omitempty"`
}

func (owner JobOwnerImpl) Sweep(
	ctx context.Context, now time.Time, cursor string, limits Limits,
) OwnerResult {
	if owner.Store == nil || owner.Acquire == nil {
		return OwnerResult{Cursor: cursor, Completeness: Unavailable, Err: errors.New("job lifecycle owner is incomplete")}
	}
	state, err := decodeJobCursor(cursor)
	if err != nil {
		return OwnerResult{Cursor: cursor, Completeness: Unavailable, Err: err}
	}
	release, err := owner.Acquire(ctx)
	if err != nil {
		return OwnerResult{Cursor: cursor, Completeness: Unavailable, Err: err}
	}
	defer release()

	result := OwnerResult{Completeness: LowerBound, More: true}
	kind := lifecycleJobKinds[state.Kind]
	switch state.Phase {
	case "age":
		cutoff := now.Add(-TerminalJobAge)
		deleted, deleteErr := owner.Store.DeleteOldestTerminalJobs(
			ctx, kind, &cutoff, limits.Deletes,
		)
		if deleteErr != nil {
			return unavailableOwnerResult(cursor, deleteErr)
		}
		result.Deleted = deleted
		if deleted == 0 {
			state.Phase = "count"
		}
	case "count":
		page, scanErr := owner.Store.ScanJobLifecycle(
			ctx, kind, state.After, limits.Candidates,
		)
		if scanErr != nil {
			return unavailableOwnerResult(cursor, scanErr)
		}
		result.Scanned = len(page.Rows)
		for _, row := range page.Rows {
			if terminalJobStatus(row.Status) {
				if state.Count == int64(^uint64(0)>>1) {
					return unavailableOwnerResult(cursor, errors.New("terminal job count overflows int64"))
				}
				state.Count++
			}
		}
		if page.Next != "" {
			state.After = page.Next
		} else if state.Count > TerminalJobCount {
			state.Phase = "trim"
			state.After = ""
			state.Remaining = state.Count - TerminalJobCount
		} else {
			return advanceJobKind(state, result)
		}
	case "trim":
		limit := limits.Deletes
		if int64(limit) > state.Remaining {
			limit = int(state.Remaining)
		}
		deleted, deleteErr := owner.Store.DeleteOldestTerminalJobs(ctx, kind, nil, limit)
		if deleteErr != nil {
			return unavailableOwnerResult(cursor, deleteErr)
		}
		result.Deleted = deleted
		state.Remaining -= int64(deleted)
		if state.Remaining <= 0 {
			return advanceJobKind(state, result)
		}
		if deleted == 0 {
			// Concurrent lifecycle changes invalidated the lower-bound count.
			// Restart this table's bounded census rather than spinning or
			// deleting a nonterminal row.
			state = jobCursor{Kind: state.Kind, Phase: "age"}
		}
	default:
		return unavailableOwnerResult(cursor, errors.New("job lifecycle phase is invalid"))
	}
	result.Cursor, err = encodeJobCursor(state)
	if err != nil {
		return unavailableOwnerResult(cursor, err)
	}
	return result
}

func advanceJobKind(state jobCursor, result OwnerResult) OwnerResult {
	state.Kind++
	if state.Kind == len(lifecycleJobKinds) {
		result.Cursor = ""
		result.More = false
		// The bounded census is restart-resumable but does not freeze queue
		// writers across its pages. Completion is therefore a lower bound, not
		// a fabricated exact installation-wide snapshot.
		result.Completeness = LowerBound
		return result
	}
	state.Phase = "age"
	state.After = ""
	state.Count = 0
	state.Remaining = 0
	encoded, err := encodeJobCursor(state)
	if err != nil {
		return unavailableOwnerResult("", err)
	}
	result.Cursor = encoded
	return result
}

func decodeJobCursor(raw string) (jobCursor, error) {
	if raw == "" {
		return jobCursor{Phase: "age"}, nil
	}
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	var state jobCursor
	if err := decoder.Decode(&state); err != nil {
		return jobCursor{}, fmt.Errorf("decode job lifecycle cursor: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return jobCursor{}, errors.New("decode job lifecycle cursor: trailing value")
	}
	if state.Kind < 0 || state.Kind >= len(lifecycleJobKinds) ||
		(state.Phase != "age" && state.Phase != "count" && state.Phase != "trim") ||
		state.Count < 0 || state.Remaining < 0 ||
		(state.Phase == "age" && (state.After != "" || state.Count != 0 || state.Remaining != 0)) ||
		(state.Phase == "count" && state.Remaining != 0) ||
		(state.Phase == "trim" && (state.After != "" || state.Remaining == 0)) {
		return jobCursor{}, errors.New("job lifecycle cursor is invalid")
	}
	return state, nil
}

func encodeJobCursor(state jobCursor) (string, error) {
	raw, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	if len(raw) > store.MaxLifecycleCursorBytes {
		return "", errors.New("job lifecycle cursor exceeds store bound")
	}
	return string(raw), nil
}

func terminalJobStatus(status store.JobStatus) bool {
	return status == store.StatusDone || status == store.StatusFailed ||
		status == store.StatusCanceled
}

func unavailableOwnerResult(cursor string, err error) OwnerResult {
	return OwnerResult{Cursor: cursor, Completeness: Unavailable, Err: err}
}
