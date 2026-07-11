package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

var _ AnalyticsStore = (*Surreal)(nil)

func usageEventID(id string) models.RecordID { return models.NewRecordID("usage_event", id) }

type usageEventRec struct {
	RecID      *models.RecordID `json:"id"`
	Kind       string           `json:"kind"`
	ActorID    string           `json:"actor_id"`
	APIKeyID   string           `json:"api_key_id"`
	Repos      []string         `json:"repos"`
	MatchCount int              `json:"match_count"`
	FileCount  int              `json:"file_count"`
	DurationMS int64            `json:"duration_ms"`
	CreatedAt  time.Time        `json:"created_at"`
}

func (r usageEventRec) event() UsageEvent {
	id := ""
	if r.RecID != nil {
		id = r.RecID.ID.(string)
	}
	return UsageEvent{
		ID: id, Kind: r.Kind, ActorID: r.ActorID, APIKeyID: r.APIKeyID,
		Repos: r.Repos, MatchCount: r.MatchCount, FileCount: r.FileCount,
		DurationMS: r.DurationMS, CreatedAt: r.CreatedAt,
	}
}

func (s *Surreal) RecordUsageEvent(ctx context.Context, event UsageEvent) error {
	if event.Kind == "" {
		return errors.New("usage event needs a kind")
	}
	id := event.ID
	if id == "" {
		var err error
		if id, err = authRandomID(); err != nil {
			return err
		}
	}
	createdAt := event.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	repos := event.Repos
	if repos == nil {
		repos = []string{}
	}
	results, err := surrealdb.Query[[]usageEventRec](ctx, s.db,
		`CREATE $rid SET kind = $kind, actor_id = $actor_id, api_key_id = $api_key_id,
            repos = $repos, match_count = $match_count, file_count = $file_count,
            duration_ms = $duration_ms, created_at = $created_at RETURN AFTER`,
		map[string]any{
			"rid": usageEventID(id), "kind": event.Kind, "actor_id": event.ActorID,
			"api_key_id": event.APIKeyID, "repos": repos,
			"match_count": event.MatchCount, "file_count": event.FileCount,
			"duration_ms": event.DurationMS, "created_at": createdAt,
		})
	if err != nil {
		return fmt.Errorf("record usage event: %w", err)
	}
	for _, result := range *results {
		if len(result.Result) > 0 {
			return nil
		}
	}
	return errors.New("record usage event returned no row")
}

// ListUsageEvents returns the window for Go-side aggregation.
// ponytail: windowed full scan; push GROUP BY into SurrealQL if the
// dashboard gets slow at real volume.
func (s *Surreal) ListUsageEvents(ctx context.Context, since time.Time) ([]UsageEvent, error) {
	results, err := surrealdb.Query[[]usageEventRec](ctx, s.db,
		"SELECT * FROM usage_event WHERE created_at > $since ORDER BY created_at ASC",
		map[string]any{"since": since})
	if err != nil {
		return nil, fmt.Errorf("list usage events: %w", err)
	}
	var events []UsageEvent
	for _, result := range *results {
		for _, row := range result.Result {
			events = append(events, row.event())
		}
	}
	return events, nil
}

func (s *Surreal) PruneUsageEvents(ctx context.Context, cutoff time.Time) (int, error) {
	results, err := surrealdb.Query[[]usageEventRec](ctx, s.db,
		"DELETE usage_event WHERE created_at <= $cutoff RETURN BEFORE",
		map[string]any{"cutoff": cutoff})
	if err != nil {
		return 0, fmt.Errorf("prune usage events: %w", err)
	}
	n := 0
	for _, result := range *results {
		n += len(result.Result)
	}
	return n, nil
}
