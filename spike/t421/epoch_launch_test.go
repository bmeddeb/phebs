package t421

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

func TestExecutionEpochOneRefusesUnavailableOwners(t *testing.T) {
	for _, flow := range []*ExecutionEpochOne{nil, {}} {
		if run, err := flow.Start(t.Context()); run != nil || !errors.Is(err, ErrExecutionEpochOne) {
			t.Fatal("unavailable Start admitted", err)
		}
		if result, err := flow.AuthorA(t.Context()); result.RootStarted || !errors.Is(err, ErrExecutionEpochOne) {
			t.Fatal("unavailable author admitted", err)
		}
	}
	for _, run := range []*ExecutionEpochOneRun{nil, {}} {
		if err := run.Health(t.Context()); !errors.Is(err, ErrExecutionEpochOne) {
			t.Fatal("unavailable health admitted", err)
		}
		if _, err := run.Stop(t.Context()); !errors.Is(err, ErrExecutionEpochOne) {
			t.Fatal("unavailable stop admitted", err)
		}
		if _, err := run.Wait(t.Context()); !errors.Is(err, ErrExecutionEpochOne) {
			t.Fatal("unavailable wait admitted", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, ctx := range []context.Context{nil, ctx, t.Context()} {
		if flow, err := PrepareExecutionEpochOne(ctx, nil, nil, nil, nil); flow != nil || !errors.Is(err, ErrExecutionEpochOne) {
			t.Fatal("unavailable constructor admitted", err)
		}
	}
}

func TestExecutionEpochOneClosedPrefixRequiresNativeAndBothTransports(t *testing.T) {
	base := func() ExecutionEpochOneResult {
		return ExecutionEpochOneResult{RootStarted: true, RootJoined: true, SessionEmpty: true,
			Accounting: dispatchadmission.Snapshot{Producers: []dispatchadmission.ProducerCount{
				{Producer: 1, Attached: true, Closed: true, Ordinal: 2}, {Producer: 2, Attached: true, Closed: true},
			}},
			Store: storeaccounting.WireSnapshot{Opened: 1, TerminalEOF: 1, Store: storeaccounting.Snapshot{Producers: []storeaccounting.ProducerCount{{Producer: 2, Attached: true, Closed: true}}}},
		}
	}
	if !epochOneClosedPrefix(t.Context(), base()) {
		t.Fatal("actual closed prefix rejected solely for unused future phases")
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	expired, expire := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer expire()
	for _, ctx := range []context.Context{nil, canceled, expired} {
		result := base()
		if epochOneClosedPrefix(ctx, result) || !result.RootJoined || !result.SessionEmpty {
			t.Fatal("expired lifetime admitted a clean Stop or lost joined-prefix evidence")
		}
	}
	for _, test := range []struct {
		name   string
		change func(*ExecutionEpochOneResult)
	}{
		{"no native Start", func(r *ExecutionEpochOneResult) { r.RootStarted = false }},
		{"no native Wait", func(r *ExecutionEpochOneResult) { r.RootJoined = false }},
		{"session remains", func(r *ExecutionEpochOneResult) { r.SessionEmpty = false }},
		{"unopened SA", func(r *ExecutionEpochOneResult) { r.Store.Opened = 0 }},
		{"missing SA EOF", func(r *ExecutionEpochOneResult) { r.Store.TerminalEOF = 0 }},
		{"active root", func(r *ExecutionEpochOneResult) { r.Accounting.Producers[0].Active = 1 }},
		{"missing root Start", func(r *ExecutionEpochOneResult) { r.Accounting.Producers[0].Ordinal = 1 }},
		{"unclosed child DA", func(r *ExecutionEpochOneResult) { r.Accounting.Producers[1].Closed = false }},
		{"active child DA", func(r *ExecutionEpochOneResult) { r.Accounting.Producers[1].Active = 1 }},
		{"unclosed child SA", func(r *ExecutionEpochOneResult) { r.Store.Store.Producers[0].Closed = false }},
		{"live SA call", func(r *ExecutionEpochOneResult) { r.Store.Store.Producers[0].Calls = 1 }},
		{"live SA transaction", func(r *ExecutionEpochOneResult) { r.Store.Store.Producers[0].Transactions = 1 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := base()
			test.change(&result)
			if epochOneClosedPrefix(t.Context(), result) {
				t.Fatal("incomplete prefix admitted")
			}
		})
	}
}
