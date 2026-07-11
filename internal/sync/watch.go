package sync

// Watch mode (T2.5): live search over local working repos. HEAD-only
// indexing means the source's HEAD hash is the exact change signal — a
// commit or branch switch moves it, working-tree edits don't.
//
// ponytail: exec-git polling every Interval instead of fsnotify — one ~5ms
// `rev-parse HEAD` per repo per tick, no dependency, no recursive-watch or
// kqueue trouble. Swap in fsnotify if watched-repo counts ever make polling
// measurable.

import (
	"context"
	"log"
	"time"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/store"
)

type Watcher struct {
	Store    store.Store
	Conns    []config.Connection // watched local connections only
	Interval time.Duration       // default 3s
}

// Watched filters cfg down to the connections a Watcher should poll.
func Watched(cfg *config.Config) []config.Connection {
	var out []config.Connection
	for _, c := range cfg.Connections {
		if c.Watch {
			out = append(out, c)
		}
	}
	return out
}

// Run polls until ctx is cancelled. The first tick only records baselines —
// boot-time EnqueueMissing already syncs everything once. A moved HEAD
// enqueues the connection's sync job; dedupe and the sync→index chain do
// the rest. Debounce is inherent: at most one in-flight sync per connection,
// and the next tick re-checks after it lands.
func (w *Watcher) Run(ctx context.Context) {
	if w.Interval == 0 {
		w.Interval = 3 * time.Second
	}
	last := map[string]string{}
	ticker := time.NewTicker(w.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		for _, conn := range w.Conns {
			head, err := runGit(ctx, LocalPath(conn.URL), "rev-parse", "HEAD")
			if err != nil {
				continue // repo busy or gone; next tick retries
			}
			prev, seen := last[conn.Name]
			if !seen || prev != head {
				if err := store.EnqueuePending(ctx, w.Store, store.JobSync, conn.Name, false); err != nil {
					continue // do not advance the baseline until the intent is durable
				}
				if seen {
					log.Printf("watch: %s HEAD moved to %.10s, sync requested", conn.Name, head)
				}
			}
			last[conn.Name] = head
		}
	}
}
