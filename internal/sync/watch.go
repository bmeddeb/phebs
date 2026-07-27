package sync

// Watch mode (T2.5): live search over local working repos. HEAD plus the
// configured revision allowlist is the exact change signal — commits, branch
// switches, and allowlisted branch/tag moves trigger; working-tree edits don't.
//
// ponytail: exec-git polling every Interval instead of fsnotify — one ~5ms
// `rev-parse HEAD` per repo per tick, no dependency, no recursive-watch or
// kqueue trouble. Swap in fsnotify if watched-repo counts ever make polling
// measurable.

import (
	"context"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/store"
)

type Watcher struct {
	Store     store.Store
	Conns     []config.Connection // watched local connections only
	Revisions config.RevisionAllowlist
	Interval  time.Duration // default 3s
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
			source, err := resolveGenericGitSource(conn.URL)
			if err != nil {
				continue
			}
			if source.localPath == "" {
				continue
			}
			fingerprint, err := watchFingerprint(ctx, source.localPath, w.Revisions[source.repoName])
			if err != nil {
				continue // repo busy or gone; next tick retries
			}
			prev, seen := last[conn.Name]
			if !seen || prev != fingerprint {
				if err := store.EnqueuePending(ctx, w.Store, store.JobSync, conn.Name, false); err != nil {
					continue // do not advance the baseline until the intent is durable
				}
				if seen {
					log.Printf("watch: %s indexed revision moved, sync requested", conn.Name)
				}
			}
			last[conn.Name] = fingerprint
		}
	}
}

func watchFingerprint(ctx context.Context, dir string, configured map[string]string) (string, error) {
	head, err := runGit(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	refs := make([]string, 0, len(configured))
	for _, ref := range configured {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	if len(refs) == 0 {
		return "HEAD=" + head, nil
	}
	args := []string{"for-each-ref", "--format=%(refname)=%(objectname)"}
	args = append(args, refs...)
	out, err := runGit(ctx, dir, args...)
	if err != nil {
		return "", err
	}
	values := make(map[string]string, len(refs))
	for _, line := range strings.Split(out, "\n") {
		ref, hash, ok := strings.Cut(line, "=")
		if ok {
			values[ref] = hash
		}
	}
	parts := []string{"HEAD=" + head}
	for _, ref := range refs {
		parts = append(parts, ref+"="+values[ref]) // empty records an absent ref
	}
	return strings.Join(parts, "\n"), nil
}
