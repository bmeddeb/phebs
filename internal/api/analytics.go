package api

import (
	"context"
	"sort"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

// T10.2: the dashboard aggregates local usage events in Go over a bounded
// window. Zero telemetry — everything stays in the local database.

const analyticsTopRepos = 10

func registerAnalytics(a huma.API, opts Options) {
	type analyticsIn struct {
		Days int `query:"days" minimum:"0" maximum:"365" doc:"window in days ending today, default 30"`
	}
	type dayCount struct {
		Date  string `json:"date"` // "2026-07-11" (UTC)
		Count int    `json:"count"`
	}
	type repoCount struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	type analyticsOut struct {
		Body struct {
			TotalSearches int         `json:"total_searches"`
			AvgDurationMS int64       `json:"avg_duration_ms"`
			Daily         []dayCount  `json:"daily"`
			TopRepos      []repoCount `json:"top_repos"`
		}
	}
	huma.Get(a, "/api/analytics", func(ctx context.Context, in *analyticsIn) (*analyticsOut, error) {
		if opts.IsAdmin != nil && !opts.IsAdmin(ctx) {
			return nil, huma.Error403Forbidden("administrator access required")
		}
		if opts.Analytics == nil {
			return nil, huma.Error503ServiceUnavailable("analytics unavailable")
		}
		days := in.Days
		if days <= 0 {
			days = 30
		}
		today := time.Now().UTC().Truncate(24 * time.Hour)
		windowStart := today.AddDate(0, 0, -(days - 1))
		events, err := opts.Analytics.ListUsageEvents(ctx, windowStart.Add(-time.Nanosecond))
		if err != nil {
			return nil, huma.Error500InternalServerError("list usage events", err)
		}

		perDay := make(map[string]int, days)
		perRepo := map[string]int{}
		var totalDuration int64
		searches := 0
		for _, event := range events {
			if event.Kind != "search" {
				continue
			}
			searches++
			totalDuration += event.DurationMS
			perDay[event.CreatedAt.UTC().Format(time.DateOnly)]++
			for _, repo := range event.Repos {
				perRepo[repo]++
			}
		}

		out := &analyticsOut{}
		out.Body.TotalSearches = searches
		if searches > 0 {
			out.Body.AvgDurationMS = totalDuration / int64(searches)
		}
		out.Body.Daily = make([]dayCount, 0, days)
		for day := windowStart; !day.After(today); day = day.AddDate(0, 0, 1) {
			date := day.Format(time.DateOnly)
			out.Body.Daily = append(out.Body.Daily, dayCount{Date: date, Count: perDay[date]})
		}
		out.Body.TopRepos = make([]repoCount, 0, len(perRepo))
		for name, count := range perRepo {
			out.Body.TopRepos = append(out.Body.TopRepos, repoCount{Name: name, Count: count})
		}
		sort.Slice(out.Body.TopRepos, func(i, j int) bool {
			a, b := out.Body.TopRepos[i], out.Body.TopRepos[j]
			if a.Count != b.Count {
				return a.Count > b.Count
			}
			return a.Name < b.Name
		})
		if len(out.Body.TopRepos) > analyticsTopRepos {
			out.Body.TopRepos = out.Body.TopRepos[:analyticsTopRepos]
		}
		return out, nil
	})
}
