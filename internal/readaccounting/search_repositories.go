package readaccounting

import "context"

type searchRepositoriesKey struct{}

// WithSearchRepositories requires one completed native search-scope observation
// in an existing request ledger. This is not a new read or work-count unit.
func WithSearchRepositories(ctx context.Context) (context.Context, error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, ErrScope
	}
	ledger, _ := ctx.Value(contextKey{}).(*Ledger)
	if ledger == nil {
		return nil, ErrScope
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if ledger.closed || ledger.err != nil || ledger.searchRepositoriesRequired {
		if ledger.err == nil {
			ledger.err = ErrScope
		}
		return nil, ledger.err
	}
	ledger.searchRepositoriesRequired = true
	return context.WithValue(ctx, searchRepositoriesKey{}, ledger), nil
}

// ObserveSearchRepositories records the cardinality of the actual completed
// visibility/index/revision-admitted native set, never displayed result hits.
// An ordinary search performs only the absent-context lookup.
func ObserveSearchRepositories(ctx context.Context, count uint64) error {
	if ctx == nil {
		return nil
	}
	ledger, _ := ctx.Value(searchRepositoriesKey{}).(*Ledger)
	if ledger == nil {
		return nil
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if ledger.err != nil {
		return ledger.err
	}
	if ledger.closed || ledger.searchRepositories != nil || ctx.Err() != nil {
		ledger.err = ErrEvent
		return ledger.err
	}
	ledger.searchRepositories = &count // Observed zero is distinct from absent.
	return nil
}

// SearchRepositories returns a detached optional observation after joined work.
// Finish separately certifies whether the required observation completed.
func (ledger *Ledger) SearchRepositories() *uint64 {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if ledger.searchRepositories == nil {
		return nil
	}
	value := *ledger.searchRepositories
	return &value
}
