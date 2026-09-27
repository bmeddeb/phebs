package generationscheduler

import (
	"context"
	"testing"

	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/typedindex"
)

func typedScheduler() *Scheduler {
	return &Scheduler{Store: &schedulerStore{}, Classes: map[store.GenerationResourceClass]Class{
		store.GenerationResourceTypedIndex: {Concurrency: 1, Budget: TypedIndexBudget(),
			Handle: func(context.Context, store.GenerationChunk, Budget) error { return nil }},
	}}
}

func TestTypedSchedulerAccountsWholeContainer(t *testing.T) {
	p := typedindex.MeasuredPolicy()
	b := TypedIndexBudget()
	if b.MaxMemoryBytes != MaxChunkMemoryBytes+p.MemoryBytes || b.MaxDescriptors != MaxChunkDescriptors+int(p.Tasks*p.Descriptors) {
		t.Fatalf("incomplete container/controller budget: %+v", b)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*Scheduler)
		valid  bool
	}{
		{"exact", func(*Scheduler) {}, true},
		{"controller only", func(s *Scheduler) {
			c := s.Classes[store.GenerationResourceTypedIndex]
			c.Budget = Budget{MaxMemoryBytes: MaxChunkMemoryBytes, MaxDescriptors: MaxChunkDescriptors}
			s.Classes[store.GenerationResourceTypedIndex] = c
		}, false},
		{"per-process descriptors", func(s *Scheduler) {
			c := s.Classes[store.GenerationResourceTypedIndex]
			c.Budget.MaxDescriptors = int(p.Descriptors)
			s.Classes[store.GenerationResourceTypedIndex] = c
		}, false},
		{"memory below", func(s *Scheduler) {
			c := s.Classes[store.GenerationResourceTypedIndex]
			c.Budget.MaxMemoryBytes--
			s.Classes[store.GenerationResourceTypedIndex] = c
		}, false},
		{"memory above", func(s *Scheduler) {
			c := s.Classes[store.GenerationResourceTypedIndex]
			c.Budget.MaxMemoryBytes++
			s.Classes[store.GenerationResourceTypedIndex] = c
		}, false},
		{"two slots", func(s *Scheduler) {
			c := s.Classes[store.GenerationResourceTypedIndex]
			c.Concurrency = 2
			s.Classes[store.GenerationResourceTypedIndex] = c
		}, false},
		{"process memory", func(s *Scheduler) { s.MaxMemoryBytes = b.MaxMemoryBytes - 1 }, false},
		{"process descriptors", func(s *Scheduler) { s.MaxDescriptors = b.MaxDescriptors - 1 }, false},
		{"generic cannot borrow", func(s *Scheduler) {
			c := s.Classes[store.GenerationResourceTypedIndex]
			delete(s.Classes, store.GenerationResourceTypedIndex)
			s.Classes[store.GenerationResourceCPU] = c
		}, false},
		{"generic descriptor pool", func(s *Scheduler) {
			s.Classes[store.GenerationResourceCPU] = Class{Concurrency: 16, Budget: Budget{MaxMemoryBytes: 1, MaxDescriptors: 256}, Handle: func(context.Context, store.GenerationChunk, Budget) error { return nil }}
		}, false},
		{"bounded generic alongside", func(s *Scheduler) {
			s.Classes[store.GenerationResourceCPU] = Class{Concurrency: 1, Budget: Budget{MaxMemoryBytes: 1, MaxDescriptors: 256}, Handle: func(context.Context, store.GenerationChunk, Budget) error { return nil }}
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := typedScheduler()
			tc.mutate(s)
			_, err := s.validate()
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t: %v", tc.valid, err)
			}
		})
	}
}

func TestTypedSchedulerSlotAndGenericPoolAcrossInstances(t *testing.T) {
	first := typedScheduler()
	classes, err := first.validate()
	if err != nil {
		t.Fatal(err)
	}
	release, err := first.acquireProcessAdmission(classes)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	processAdmission.Lock()
	slots, descriptors, memory := processAdmission.typedSlots, processAdmission.descriptors, processAdmission.memoryBytes
	processAdmission.Unlock()
	if slots != 1 || descriptors != MaxChunkDescriptors || memory != TypedIndexBudget().MaxMemoryBytes {
		t.Fatalf("reservation slots=%d descriptors=%d memory=%d", slots, descriptors, memory)
	}
	second := typedScheduler()
	classes, err = second.validate()
	if err != nil {
		t.Fatal(err)
	}
	if r, err := second.acquireProcessAdmission(classes); err == nil {
		r()
		t.Fatal("second typed instance admitted")
	}
	generic := &Scheduler{Store: &schedulerStore{}, Classes: map[store.GenerationResourceClass]Class{store.GenerationResourceCPU: {Concurrency: 16, Budget: Budget{MaxMemoryBytes: 1, MaxDescriptors: 256}, Handle: func(context.Context, store.GenerationChunk, Budget) error { return nil }}}}
	gc, err := generic.validate()
	if err != nil {
		t.Fatal(err)
	}
	if r, err := generic.acquireProcessAdmission(gc); err == nil {
		r()
		t.Fatal("generic borrowed the container descriptor allowance")
	}
	c := generic.Classes[store.GenerationResourceCPU]
	c.Concurrency = 15
	generic.Classes[store.GenerationResourceCPU] = c
	gc, err = generic.validate()
	if err != nil {
		t.Fatal(err)
	}
	gr, err := generic.acquireProcessAdmission(gc)
	if err != nil {
		t.Fatal(err)
	}
	gr()
	release()
	release() // idempotent release must not create a negative reservation
	r, err := second.acquireProcessAdmission(classes)
	if err != nil {
		t.Fatal(err)
	}
	r()
	processAdmission.Lock()
	defer processAdmission.Unlock()
	if processAdmission.typedSlots != 0 || processAdmission.concurrency != 0 || processAdmission.memoryBytes != 0 || processAdmission.descriptors != 0 {
		t.Fatal("reservation leaked")
	}
}
