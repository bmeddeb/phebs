package store

// T1.3 spike: characterize job-claim semantics under concurrent pollers.
// Two candidate claim statements race W workers over a queue of N jobs;
// the harness measures double-claims, lost races / txn conflicts, and claim
// latency. Results land as an ADR in PLAN.md; the winner ships as ClaimJob.
// Kept (not deleted) so the P2 invariant-checker and Elle work can rerun it.

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
)

// Candidate A is claimSQL (surreal.go) — the shipped winner.
// txnSQL wraps select+update in a transaction and leans on SurrealDB's
// optimistic concurrency: conflicting commits error out and the caller retries.
const txnSQL = `
BEGIN;
LET $cand = (SELECT id, created_at FROM type::table($table) WHERE status = 'pending' ORDER BY created_at LIMIT 1)[0].id;
RETURN IF $cand != NONE THEN
    (UPDATE $cand SET status = 'claimed', claimed_by = $who, claimed_at = time::now() RETURN AFTER)
ELSE [] END;
COMMIT;`

type spikeStats struct {
	claimed      int
	doubleClaims int
	lostRaces    int // empty result while pending jobs remained
	conflicts    int // transaction conflict errors
	latencies    []time.Duration
	wall         time.Duration
}

func (st *spikeStats) percentile(p float64) time.Duration {
	if len(st.latencies) == 0 {
		return 0
	}
	// ponytail: nearest-rank on a sorted copy; fine for a spike
	sorted := append([]time.Duration(nil), st.latencies...)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j] < sorted[j-1]; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	idx := int(p*float64(len(sorted))) - 1
	if idx < 0 {
		idx = 0
	}
	return sorted[idx]
}

func runSpike(t *testing.T, sql string, jobs, workers int) spikeStats {
	t.Helper()
	ctx := context.Background()
	s, err := OpenLocal(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })

	for i := range jobs {
		if _, err := s.CreateJob(ctx, JobSync, fmt.Sprintf("conn-%d", i)); err != nil {
			t.Fatalf("seed job %d: %v", i, err)
		}
	}

	var (
		mu      sync.Mutex
		stats   spikeStats
		claimed = map[string]string{} // job id -> worker
	)
	pendingLeft := func() bool {
		n, err := s.countPending(ctx, JobSync)
		return err == nil && n > 0
	}

	start := time.Now()
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func(who string) {
			defer wg.Done()
			for {
				t0 := time.Now()
				res, err := surrealdb.Query[[]jobRec](ctx, s.db, sql,
					map[string]any{"table": string(JobSync), "who": who})
				lat := time.Since(t0)
				if err != nil {
					if strings.Contains(err.Error(), "retry") || strings.Contains(strings.ToLower(err.Error()), "conflict") {
						mu.Lock()
						stats.conflicts++
						mu.Unlock()
						continue
					}
					t.Errorf("worker %s: %v", who, err)
					return
				}
				rows := firstNonEmpty(res)
				if len(rows) == 0 {
					if !pendingLeft() {
						return
					}
					mu.Lock()
					stats.lostRaces++
					mu.Unlock()
					continue
				}
				rec := rows[0].toJob(JobSync)
				mu.Lock()
				stats.claimed++
				stats.latencies = append(stats.latencies, lat)
				if prev, dup := claimed[rec.ID]; dup {
					stats.doubleClaims++
					t.Errorf("job %s claimed by both %s and %s", rec.ID, prev, who)
				}
				claimed[rec.ID] = who
				mu.Unlock()
			}
		}(fmt.Sprintf("worker-%d", w))
	}
	wg.Wait()
	stats.wall = time.Since(start)

	if stats.claimed != jobs {
		t.Errorf("claimed %d of %d jobs", stats.claimed, jobs)
	}
	return stats
}

func TestSpikeClaimStrategies(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	if testing.Short() {
		t.Skip("spike: skipped in -short")
	}
	const jobs, workers = 200, 8
	for _, tt := range []struct {
		name string
		sql  string
	}{
		{"optimistic_conditional_update", claimSQL},
		{"explicit_transaction", txnSQL},
	} {
		t.Run(tt.name, func(t *testing.T) {
			st := runSpike(t, tt.sql, jobs, workers)
			t.Logf("%s: claimed=%d double=%d lostRaces=%d conflicts=%d p50=%v p95=%v wall=%v",
				tt.name, st.claimed, st.doubleClaims, st.lostRaces, st.conflicts,
				st.percentile(0.50), st.percentile(0.95), st.wall)
		})
	}
}
