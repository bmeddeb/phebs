package main

import "sort"

// visibilityContext is the spike's principal model for Gate 1. The caller's
// visible corpus is fixed before any filtering or aggregation happens.
type visibilityContext struct {
	Principal      string
	VisibleSystems map[string]bool
}

type evidenceCoverage struct {
	Systems    []string       `json:"systems"`
	FactCount  int            `json:"fact_count"`
	Predicates map[string]int `json:"predicates"`
}

// evidenceView is deliberately self-contained: callers never receive raw
// corpus-wide counts alongside a filtered fact set.
type evidenceView struct {
	Principal string           `json:"principal"`
	Facts     []Fact           `json:"facts"`
	Coverage  evidenceCoverage `json:"coverage"`
}

// buildEvidenceView enforces the Gate-1 ordering invariant: reject invisible
// occurrences first, then derive every assertion and coverage count from the
// surviving set. In particular, neither system names nor counts from an
// invisible repository can enter the result.
func buildEvidenceView(facts []Fact, visibility visibilityContext) evidenceView {
	view := evidenceView{
		Principal: visibility.Principal,
		Facts:     []Fact{},
		Coverage: evidenceCoverage{
			Systems:    []string{},
			Predicates: map[string]int{},
		},
	}
	systems := map[string]bool{}
	for _, fact := range facts {
		if !visibility.VisibleSystems[fact.System] {
			continue
		}
		view.Facts = append(view.Facts, fact)
		view.Coverage.FactCount++
		view.Coverage.Predicates[fact.Predicate]++
		systems[fact.System] = true
	}

	for system := range systems {
		view.Coverage.Systems = append(view.Coverage.Systems, system)
	}
	sort.Strings(view.Coverage.Systems)
	sort.Slice(view.Facts, func(i, j int) bool {
		a, b := view.Facts[i], view.Facts[j]
		if a.System != b.System {
			return a.System < b.System
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		if a.StartByte != b.StartByte {
			return a.StartByte < b.StartByte
		}
		if a.Predicate != b.Predicate {
			return a.Predicate < b.Predicate
		}
		if a.Subject != b.Subject {
			return a.Subject < b.Subject
		}
		return a.Object < b.Object
	})
	return view
}
