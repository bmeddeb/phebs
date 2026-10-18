package api_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/api"
)

// These labels describe the five committed neutral source records, independently
// of the comparison's grouping code: record 3 shares record 2's exact span/unit.
func TestExactCallerComparisonFrozenSourceAndUnitGold(t *testing.T) {
	gold := []struct {
		start, end       int
		classification   string
		old, replacement int
		unit             string
	}{
		{0, 11, "old_only_evidence", 1, 0, "b"},
		{12, 23, "new_only_evidence", 0, 1, "c"},
		{24, 35, "both_evidence", 1, 1, "d"},
		{48, 59, "unresolved", 1, 1, ""},
	}
	for _, level := range []string{"occurrence", "unit"} {
		t.Run(level, func(t *testing.T) {
			fixture, service := newExactCallerComparisonFixture(t)
			query := exactComparisonQuery(fixture)
			query.Level = level
			want := make(map[string]int, len(gold))
			for index, label := range gold {
				key := fmt.Sprintf("%s@%s:src/calls.go:%d-%d", fixture.repository, fixture.commit, label.start, label.end)
				if level == "unit" {
					if label.unit == "" {
						key = "unresolved:" + key
					} else {
						key = fixture.repository + ":au_" + strings.Repeat(label.unit, 64)
					}
				}
				want[key] = index
			}
			cursor := ""
			for pageIndex := range len(gold) {
				page, err := service.Compare(t.Context(), query, 1, cursor)
				if err != nil || page == nil || page.TotalRows == nil || *page.TotalRows != len(gold) || len(page.Rows) != 1 {
					t.Fatalf("gold page %d = %+v, %v", pageIndex, page, err)
				}
				row := page.Rows[0]
				index, ok := want[row.Key]
				if !ok {
					t.Fatalf("unexpected or repeated source/unit key %q", row.Key)
				}
				delete(want, row.Key)
				label := gold[index]
				if row.Classification != label.classification || row.Old.OccurrenceCount != label.old || row.Replacement.OccurrenceCount != label.replacement {
					t.Fatalf("gold label %+v returned %+v", label, row)
				}
				if (row.Unit != nil) != (level == "unit" && label.unit != "") {
					t.Fatalf("gold unit attribution = %+v", row.Unit)
				}
				for _, side := range []api.CallerComparisonSide{row.Old, row.Replacement} {
					for _, sample := range side.Rows {
						if sample.Source.Repository != fixture.repository || sample.Source.Commit != fixture.commit || sample.Source.Path != "src/calls.go" || sample.Source.StartByte != label.start || sample.Source.EndByte != label.end || sample.Source.Citation == "" {
							t.Fatalf("gold source identity = %+v", sample.Source)
						}
					}
				}
				cursor = page.Pagination.NextCursor
				if page.Pagination.Complete != (pageIndex == len(gold)-1) || (cursor == "") != page.Pagination.Complete {
					t.Fatalf("gold pagination = %+v", page.Pagination)
				}
			}
			if len(want) != 0 {
				t.Fatalf("gold rows missing: %v", want)
			}
		})
	}
}
