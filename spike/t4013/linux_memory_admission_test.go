package t4013

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestLinuxMemoryAdmissionCalculation(t *testing.T) {
	tests := []struct {
		name   string
		change func(*LinuxMemoryObservation, *LinuxMemoryContract)
		want   LinuxMemoryCalculation
	}{
		{
			name: "finite ancestry",
			want: LinuxMemoryCalculation{800, 700, 500, 100, 600},
		},
		{
			name: "available slack equality",
			change: func(_ *LinuxMemoryObservation, contract *LinuxMemoryContract) {
				contract.HostHeadroomBytes = 200
			},
			want: LinuxMemoryCalculation{800, 700, 500, 200, 700},
		},
		{
			name: "capacity equality",
			change: func(observation *LinuxMemoryObservation, contract *LinuxMemoryContract) {
				observation.AvailableBytes = 1_000
				observation.CgroupAncestors[0].CurrentBytes = 0
				observation.CgroupAncestors[1].CurrentBytes = 0
				contract.HostHeadroomBytes = 300
			},
			want: LinuxMemoryCalculation{800, 800, 500, 300, 800},
		},
		{
			name: "tighter ancestor wins over leaf",
			change: func(observation *LinuxMemoryObservation, _ *LinuxMemoryContract) {
				observation.CgroupAncestors[0].LimitBytes = 650
				observation.CgroupAncestors[0].CurrentBytes = 0
			},
			want: LinuxMemoryCalculation{650, 650, 500, 100, 600},
		},
		{
			name: "ancestor current usage limits slack",
			change: func(observation *LinuxMemoryObservation, _ *LinuxMemoryContract) {
				observation.CgroupAncestors[0].CurrentBytes = 400
			},
			want: LinuxMemoryCalculation{800, 600, 500, 100, 600},
		},
		{
			name: "explicit unlimited ancestry uses physical capacity",
			change: func(observation *LinuxMemoryObservation, _ *LinuxMemoryContract) {
				for index := range observation.CgroupAncestors {
					observation.CgroupAncestors[index].Unlimited = true
					observation.CgroupAncestors[index].LimitBytes = 0
				}
			},
			want: LinuxMemoryCalculation{1_000, 900, 500, 100, 600},
		},
		{
			name: "physical capacity is below finite cgroup limits",
			change: func(observation *LinuxMemoryObservation, _ *LinuxMemoryContract) {
				observation.PhysicalBytes = 700
				observation.AvailableBytes = 650
			},
			want: LinuxMemoryCalculation{700, 650, 500, 100, 600},
		},
		{
			name: "modeled H1 physical size is below old 16 and 24 GiB floors",
			change: func(observation *LinuxMemoryObservation, contract *LinuxMemoryContract) {
				// Only the physical scalar comes from H1. Available bytes and
				// every reservation here are synthetic, not observed admission.
				observation.PhysicalBytes = 15_991_791_616
				observation.AvailableBytes = 12 << 30
				for index := range observation.CgroupAncestors {
					observation.CgroupAncestors[index].Unlimited = true
					observation.CgroupAncestors[index].LimitBytes = 0
				}
				contract.HostHeadroomBytes = 2 << 30
				contract.Reservations[0].BytesPerInstance = 1 << 30
				contract.Reservations[1].BytesPerInstance = 4 << 30
			},
			want: LinuxMemoryCalculation{15_991_791_616, 12 << 30, 9 << 30, 2 << 30, 11 << 30},
		},
		{
			name: "bounded maximum ancestry",
			change: func(observation *LinuxMemoryObservation, _ *LinuxMemoryContract) {
				observation.CgroupAncestors = linuxMemoryTestAncestors(maxLinuxMemoryAncestors)
			},
			want: LinuxMemoryCalculation{1_000, 900, 500, 100, 600},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			observation, contract := linuxMemoryTestInputs()
			if test.change != nil {
				test.change(&observation, &contract)
			}
			got, err := EvaluateLinuxMemoryAdmission(observation, contract)
			if err != nil || got != test.want {
				t.Fatalf("calculation = %+v, %v; want %+v", got, err, test.want)
			}
		})
	}
}

func TestLinuxMemoryAdmissionRefusals(t *testing.T) {
	tests := []struct {
		name     string
		change   func(*LinuxMemoryObservation, *LinuxMemoryContract)
		overflow bool
	}{
		{"physical unavailable", func(o *LinuxMemoryObservation, _ *LinuxMemoryContract) { o.PhysicalBytes = 0 }, false},
		{"negative available", func(o *LinuxMemoryObservation, _ *LinuxMemoryContract) { o.AvailableBytes = -1 }, false},
		{"available exceeds physical", func(o *LinuxMemoryObservation, _ *LinuxMemoryContract) { o.AvailableBytes = 1_001 }, false},
		{"ancestry unknown", func(o *LinuxMemoryObservation, _ *LinuxMemoryContract) { o.CgroupAncestryComplete = false }, false},
		{"ancestry absent", func(o *LinuxMemoryObservation, _ *LinuxMemoryContract) { o.CgroupAncestors = nil }, false},
		{"ancestry over bound", func(o *LinuxMemoryObservation, _ *LinuxMemoryContract) {
			o.CgroupAncestors = linuxMemoryTestAncestors(maxLinuxMemoryAncestors + 1)
		}, false},
		{"root has parent", func(o *LinuxMemoryObservation, _ *LinuxMemoryContract) {
			o.CgroupAncestors[0].ParentIdentity = o.CgroupAncestors[1].Identity
		}, false},
		{"ancestor identity unavailable", func(o *LinuxMemoryObservation, _ *LinuxMemoryContract) {
			o.CgroupAncestors[1].Identity = ""
		}, false},
		{"ancestor chain changed or missing", func(o *LinuxMemoryObservation, _ *LinuxMemoryContract) {
			o.CgroupAncestors[1].ParentIdentity = digest([]byte("different-parent"))
		}, false},
		{"ancestor identity repeats", func(o *LinuxMemoryObservation, _ *LinuxMemoryContract) {
			o.CgroupAncestors[1].Identity = o.CgroupAncestors[0].Identity
		}, false},
		{"unlimited with numeric allowance", func(o *LinuxMemoryObservation, _ *LinuxMemoryContract) {
			o.CgroupAncestors[1].Unlimited = true
		}, false},
		{"finite limit absent", func(o *LinuxMemoryObservation, _ *LinuxMemoryContract) { o.CgroupAncestors[1].LimitBytes = 0 }, false},
		{"negative limit", func(o *LinuxMemoryObservation, _ *LinuxMemoryContract) { o.CgroupAncestors[1].LimitBytes = -1 }, false},
		{"current unavailable", func(o *LinuxMemoryObservation, _ *LinuxMemoryContract) { o.CgroupAncestors[1].CurrentBytes = -1 }, false},
		{"current over limit", func(o *LinuxMemoryObservation, _ *LinuxMemoryContract) { o.CgroupAncestors[1].CurrentBytes = 801 }, false},
		{"headroom required", func(_ *LinuxMemoryObservation, c *LinuxMemoryContract) { c.HostHeadroomBytes = 0 }, false},
		{"negative headroom", func(_ *LinuxMemoryObservation, c *LinuxMemoryContract) { c.HostHeadroomBytes = -1 }, false},
		{"census absent", func(_ *LinuxMemoryObservation, c *LinuxMemoryContract) { c.RequiredScopes = nil; c.Reservations = nil }, false},
		{"missing child reservation", func(_ *LinuxMemoryObservation, c *LinuxMemoryContract) { c.Reservations = c.Reservations[:1] }, false},
		{"extra reservation", func(_ *LinuxMemoryObservation, c *LinuxMemoryContract) {
			c.Reservations = append(c.Reservations, c.Reservations[0])
		}, false},
		{"unknown reservation scope", func(_ *LinuxMemoryObservation, c *LinuxMemoryContract) { c.Reservations[1].Scope = "unknown" }, false},
		{"understated concurrency", func(_ *LinuxMemoryObservation, c *LinuxMemoryContract) { c.Reservations[1].MaximumConcurrent = 1 }, false},
		{"zero census concurrency", func(_ *LinuxMemoryObservation, c *LinuxMemoryContract) { c.RequiredScopes[1].MaximumConcurrent = 0 }, false},
		{"negative census concurrency", func(_ *LinuxMemoryObservation, c *LinuxMemoryContract) { c.RequiredScopes[1].MaximumConcurrent = -1 }, false},
		{"zero reservation", func(_ *LinuxMemoryObservation, c *LinuxMemoryContract) { c.Reservations[1].BytesPerInstance = 0 }, false},
		{"negative reservation", func(_ *LinuxMemoryObservation, c *LinuxMemoryContract) { c.Reservations[1].BytesPerInstance = -1 }, false},
		{"capacity insufficient with no swap credit", func(o *LinuxMemoryObservation, _ *LinuxMemoryContract) {
			o.PhysicalBytes = 599
			o.AvailableBytes = 599
		}, false},
		{"physical slack exhausted", func(o *LinuxMemoryObservation, _ *LinuxMemoryContract) { o.AvailableBytes = 599 }, false},
		{"cgroup slack exhausted", func(o *LinuxMemoryObservation, _ *LinuxMemoryContract) { o.CgroupAncestors[1].CurrentBytes = 201 }, false},
		{"one byte above slack", func(_ *LinuxMemoryObservation, c *LinuxMemoryContract) { c.HostHeadroomBytes = 201 }, false},
		{"multiplication overflow", func(_ *LinuxMemoryObservation, c *LinuxMemoryContract) {
			c.Reservations[1].BytesPerInstance = maxInt64Value
		}, true},
		{"reservation sum overflow", func(_ *LinuxMemoryObservation, c *LinuxMemoryContract) {
			c.RequiredScopes[1].MaximumConcurrent = 1
			c.Reservations[1].MaximumConcurrent = 1
			c.Reservations[1].BytesPerInstance = maxInt64Value
		}, true},
		{"headroom sum overflow", func(_ *LinuxMemoryObservation, c *LinuxMemoryContract) { c.HostHeadroomBytes = maxInt64Value }, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			observation, contract := linuxMemoryTestInputs()
			test.change(&observation, &contract)
			got, err := EvaluateLinuxMemoryAdmission(observation, contract)
			if err == nil || got != (LinuxMemoryCalculation{}) || test.overflow && !errors.Is(err, errArithmeticOverflow) {
				t.Fatalf("refusal = %+v, %v; expected zero calculation and overflow=%v", got, err, test.overflow)
			}
		})
	}
}

func TestLinuxMemoryAdmissionScopeCensus(t *testing.T) {
	tests := []struct {
		name   string
		names  []string
		accept bool
	}{
		{"siblings", []string{"controller", "server/indexer", "server/store"}, true},
		{"prefix is not containment", []string{"server", "server-other"}, true},
		{"nonadjacent overlap", []string{"server", "server-other", "server/store"}, false},
		{"duplicate", []string{"server", "server"}, false},
		{"unsorted", []string{"server", "controller"}, false},
		{"empty", []string{""}, false},
		{"absolute", []string{"/server"}, false},
		{"trailing slash", []string{"server/"}, false},
		{"empty segment", []string{"server//store"}, false},
		{"parent segment", []string{"server/../store"}, false},
		{"uppercase", []string{"Server"}, false},
		{"control byte", []string{"server\n"}, false},
		{"maximum name", []string{strings.Repeat("s", maxLinuxMemoryScopeName)}, true},
		{"name over bound", []string{strings.Repeat("s", maxLinuxMemoryScopeName+1)}, false},
		{"maximum census", linuxMemoryTestScopeNames(maxLinuxMemoryScopes), true},
		{"census over bound", linuxMemoryTestScopeNames(maxLinuxMemoryScopes + 1), false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			observation, contract := linuxMemoryTestInputs()
			contract.RequiredScopes = nil
			contract.Reservations = nil
			for _, name := range test.names {
				contract.RequiredScopes = append(contract.RequiredScopes, LinuxMemoryScope{name, 1})
				contract.Reservations = append(contract.Reservations, LinuxMemoryReservation{name, 1, 1})
			}
			got, err := EvaluateLinuxMemoryAdmission(observation, contract)
			if (err == nil) != test.accept || !test.accept && got != (LinuxMemoryCalculation{}) {
				t.Fatalf("scope census calculation = %+v, %v; accept=%v", got, err, test.accept)
			}
		})
	}
}

func TestLinuxMemoryAdmissionDoesNotMutateInputs(t *testing.T) {
	observation, contract := linuxMemoryTestInputs()
	wantObservation, wantContract := linuxMemoryTestInputs()
	if _, err := EvaluateLinuxMemoryAdmission(observation, contract); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(observation, wantObservation) || !reflect.DeepEqual(contract, wantContract) {
		t.Fatal("resource calculation mutated supplied inputs")
	}
}

func linuxMemoryTestInputs() (LinuxMemoryObservation, LinuxMemoryContract) {
	root, leaf := digest([]byte("root")), digest([]byte("leaf"))
	return LinuxMemoryObservation{
			PhysicalBytes: 1_000, AvailableBytes: 900, CgroupAncestryComplete: true,
			CgroupAncestors: []LinuxMemoryCgroupObservation{
				{Identity: root, LimitBytes: 1_000, CurrentBytes: 100},
				{Identity: leaf, ParentIdentity: root, LimitBytes: 800, CurrentBytes: 100},
			},
		}, LinuxMemoryContract{
			HostHeadroomBytes: 100,
			RequiredScopes:    []LinuxMemoryScope{{"controller", 1}, {"server", 2}},
			Reservations:      []LinuxMemoryReservation{{"controller", 1, 100}, {"server", 2, 200}},
		}
}

func linuxMemoryTestAncestors(count int) []LinuxMemoryCgroupObservation {
	rows := make([]LinuxMemoryCgroupObservation, 0, count)
	parent := ""
	for index := range count {
		identity := digest([]byte(strconv.Itoa(index)))
		rows = append(rows, LinuxMemoryCgroupObservation{Identity: identity, ParentIdentity: parent, Unlimited: true})
		parent = identity
	}
	return rows
}

func linuxMemoryTestScopeNames(count int) []string {
	names := make([]string, 0, count)
	for index := range count {
		names = append(names, "scope-"+strconv.Itoa(100+index))
	}
	return names
}
